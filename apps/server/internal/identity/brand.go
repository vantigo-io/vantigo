package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/brandscale"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is a brand as the module holds it (brands design B1, B5): the
// version document stored in identity.brands.published and .draft, the
// validation of the inputs a person may send, and the mapping to the wire.
// A version is inputs plus the values generated from them (brand_tokens.go);
// the generated values are never read from a request.

// The permission that manages brands (B31), and the codes of its refusals.
const (
	permBrands = "identity:brands"

	codeBrandContrast     = "brand_contrast"
	codeBrandPrimaryRange = "brand_primary_range"
	codeBrandStale        = "stale_revision"
	codeBrandNoDraft      = "no_draft"
	codeBrandNotAccepted  = "invalid_request"

	brandNameMax  = 100
	brandTitleMax = 60

	// brandPayloadVersion is the "v" of the payload and of the browser's mirror (B36).
	brandPayloadVersion = 1

	brandAssetsPath = "/api/v1/identity/brand-assets/"
	brandsPath      = "/api/v1/identity/brands/"

	// Vantigo's own name, title and indigo: the default brand before any
	// environment value or edit.
	builtinBrandName  = "Vantigo"
	builtinBrandTitle = "Vantigo"
)

// brandSlots are the four logo slots, in the order they are shown (B1).
var brandSlots = []string{"mark_light", "mark_dark", "icon", "wordmark"}

func validSlot(s string) bool {
	for _, k := range brandSlots {
		if k == s {
			return true
		}
	}
	return false
}

// brandLogo names a stored logo asset by the hash of its bytes (B6).
type brandLogo struct {
	SHA256      string `json:"sha256"`
	ContentType string `json:"contentType"`
}

// brandSupport is the support contacts (B1), each optional.
type brandSupport struct {
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
	URL   string `json:"url,omitempty"`
}

// brandVersion is one version of a brand: the published one or the pending
// draft. Everything in it is versioned content (B1): none of it is a column.
type brandVersion struct {
	Name      string               `json:"name"`
	Title     string               `json:"title"`
	Primary   string               `json:"primary"`
	Radius    string               `json:"radius"`
	Support   brandSupport         `json:"support"`
	Logos     map[string]brandLogo `json:"logos"`
	Algorithm int                  `json:"algorithm"`
	Tokens    brandTokens          `json:"tokens"`
}

// brandInput is what a person may send: inputs only (B37).
type brandInput struct {
	Name    string
	Title   string
	Primary string
	Radius  string
	Support brandSupport
}

// brandRefusal is why an input or a rule refused a request: its status, a
// stable code, a sentence and the fields it names.
type brandRefusal struct {
	status  int
	code    string
	message string
	fields  map[string][]string
}

func (r *brandRefusal) body() apicommon.AuthErrorResponse {
	return authErrorBody(r.code, r.message, r.fields)
}

func refuseBrand(status int, code, message string, fields map[string][]string) *brandRefusal {
	return &brandRefusal{status: status, code: code, message: message, fields: fields}
}

var hexPrimary = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// newBrandVersion builds the version a set of inputs makes, keeping logos, and
// recomputes every generated value. It refuses a name or title out of bounds, a
// primary that is not #rrggbb or is out of the accepted lightness, an unknown
// radius and an invalid support contact.
func newBrandVersion(in brandInput, logos map[string]brandLogo) (brandVersion, *brandRefusal) {
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > brandNameMax {
		return brandVersion{}, refuseBrand(http.StatusBadRequest, codeBrandNotAccepted,
			"The name is 1 to 100 characters.", map[string][]string{"name": {"The name is 1 to 100 characters."}})
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = name
	}
	if utf8.RuneCountInString(title) > brandTitleMax {
		return brandVersion{}, refuseBrand(http.StatusBadRequest, codeBrandNotAccepted,
			"The title is at most 60 characters.", map[string][]string{"title": {"The title is at most 60 characters."}})
	}
	primary := strings.ToLower(strings.TrimSpace(in.Primary))
	if !hexPrimary.MatchString(primary) {
		return brandVersion{}, refuseBrand(http.StatusBadRequest, codeBrandNotAccepted,
			"The primary colour is written #rrggbb.", map[string][]string{"primary": {"The primary colour is written #rrggbb."}})
	}
	radius := in.Radius
	if radius == "" {
		radius = defaultBrandRadius
	}
	support, bad := validateSupport(in.Support)
	if bad != nil {
		return brandVersion{}, refuseBrand(http.StatusUnprocessableEntity, codeBrandSupportInvalid,
			"A support contact is not valid.", bad)
	}
	tokens, _, err := computeTokens(primary, radius)
	switch {
	case errors.Is(err, brandscale.ErrPrimaryRange):
		return brandVersion{}, refuseBrand(http.StatusUnprocessableEntity, codeBrandPrimaryRange,
			"The primary colour is too light or too dark to build a scale from; choose one that is not near white or black.",
			map[string][]string{"primary": {"Choose a colour that is not near white or black."}})
	case errors.Is(err, errBrandRadius):
		return brandVersion{}, refuseBrand(http.StatusBadRequest, codeBrandNotAccepted,
			"The radius is xs, sm, md, lg or xl.", map[string][]string{"radius": {"The radius is xs, sm, md, lg or xl."}})
	case err != nil:
		return brandVersion{}, refuseBrand(http.StatusBadRequest, codeBrandNotAccepted, "The primary colour is not valid.", nil)
	}
	if logos == nil {
		logos = map[string]brandLogo{}
	}
	return brandVersion{
		Name: name, Title: title, Primary: primary, Radius: radius, Support: support,
		Logos: logos, Algorithm: brandscale.Algorithm, Tokens: tokens,
	}, nil
}

// builtinBrand is the default brand before the environment and any edit: Vantigo's own.
func builtinBrand() brandVersion {
	v, refusal := newBrandVersion(brandInput{Name: builtinBrandName, Title: builtinBrandTitle, Primary: builtinPrimary, Radius: defaultBrandRadius}, nil)
	if refusal != nil {
		panic("identity: the built-in brand is invalid: " + refusal.message) // impossible: constants
	}
	return v
}

func parseBrandVersion(raw []byte) (*brandVersion, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v brandVersion
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("identity: read a brand version: %w", err)
	}
	if v.Logos == nil {
		v.Logos = map[string]brandLogo{}
	}
	return &v, nil
}

func (v brandVersion) marshal() []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("identity: marshal a brand version: " + err.Error()) // impossible: plain types
	}
	return b
}

// brandRow is a stored brand with its versions parsed.
type brandRow struct {
	store.IdentityBrand
	pub, draft *brandVersion
}

func loadBrandRow(b store.IdentityBrand) (brandRow, error) {
	pub, err := parseBrandVersion(b.Published)
	if err != nil {
		return brandRow{}, err
	}
	draft, err := parseBrandVersion(b.Draft)
	if err != nil {
		return brandRow{}, err
	}
	return brandRow{IdentityBrand: b, pub: pub, draft: draft}, nil
}

// editing is the version being edited: the draft, else the published one.
func (b brandRow) editing() brandVersion {
	if b.draft != nil {
		return *b.draft
	}
	if b.pub != nil {
		return *b.pub
	}
	return builtinBrand()
}

// contrastChecks are B4's checks for a version, light then dark.
func contrastChecks(v brandVersion) []brandscale.Check {
	res, err := scaleOf(v.Primary)
	if err != nil {
		return nil
	}
	return brandscale.Checks(res)
}

// failingPairs names the checks below AA as "light:linkPage" and so on.
func failingPairs(checks []brandscale.Check) []string {
	var out []string
	for _, c := range checks {
		if !c.Pass {
			out = append(out, string(c.Scheme)+":"+string(c.Pair))
		}
	}
	return out
}

// parseRevision reads an If-Match header: a bare or quoted (weak or strong)
// positive integer.
func parseRevision(h string) (int32, bool) {
	h = strings.Trim(strings.TrimPrefix(strings.TrimSpace(h), "W/"), `"`)
	n, err := strconv.ParseInt(h, 10, 32)
	if err != nil || n < 1 {
		return 0, false
	}
	return int32(n), true
}

var staleRevision = refuseBrand(http.StatusConflict, codeBrandStale,
	"The brand has changed since you opened it; reload it and try again.", nil)

// --- the wire ---------------------------------------------------------------

func genVersion(v *brandVersion) *gen.BrandVersion {
	if v == nil {
		return nil
	}
	return &gen.BrandVersion{
		Name: v.Name, Title: v.Title, Primary: v.Primary, Radius: gen.BrandVersionRadius(v.Radius),
		Support: genSupport(v.Support), Logos: genLogosOf(v.Logos), Algorithm: int32(v.Algorithm), Tokens: genTokens(v.Tokens),
	}
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func genSupport(s brandSupport) gen.BrandSupport {
	return gen.BrandSupport{Email: opt(s.Email), Phone: opt(s.Phone), Url: opt(s.URL)}
}

func genScheme(t schemeTokens) gen.BrandSchemeTokens {
	return gen.BrandSchemeTokens{Shades: t.Shades[:], FilledText: t.FilledText, Link: t.Link, SubtleBg: t.SubtleBg, Derived: t.Derived}
}

func genTokens(t brandTokens) gen.BrandTokens {
	return gen.BrandTokens{Light: genScheme(t.Light), Dark: genScheme(t.Dark), Radius: t.Radius}
}

func genChecks(checks []brandscale.Check) []gen.BrandCheck {
	out := make([]gen.BrandCheck, 0, len(checks))
	for _, c := range checks {
		out = append(out, gen.BrandCheck{
			Pair: gen.BrandCheckPair(c.Pair), Scheme: gen.BrandCheckScheme(c.Scheme),
			Foreground: c.Foreground, Background: c.Background, Ratio: roundRatio(c.Ratio), Pass: c.Pass,
		})
	}
	return out
}

func roundRatio(r float64) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(r, 'f', 2, 64), 64)
	return f
}

// brandResponse is the admin's view of a brand.
func (s *server) brandResponse(b brandRow) gen.BrandResponse {
	editing := b.editing()
	checks := contrastChecks(editing)
	resp := gen.BrandResponse{
		Id:                   b.ID,
		IsDefault:            b.IsDefault,
		State:                gen.BrandResponseState(b.State),
		Revision:             b.Revision,
		PublishedVersion:     b.PublishedVersion,
		Published:            genVersion(b.pub),
		Draft:                genVersion(b.draft),
		Checks:               genChecks(checks),
		ImportNotice:         b.ImportNotice,
		LogoStorageAvailable: s.brandObjects.configured,
	}
	if len(failingPairs(checks)) > 0 {
		if sug, ok := brandscale.Suggest(editing.Primary); ok {
			resp.SuggestedPrimary = &sug
		}
	}
	return resp
}

func brandSummary(b brandRow) gen.BrandSummary {
	e := b.editing()
	return gen.BrandSummary{
		Id: b.ID, Name: e.Name, Title: e.Title, Primary: e.Primary, IsDefault: b.IsDefault,
		State: gen.BrandSummaryState(b.State), HasDraft: b.draft != nil,
		PublishedVersion: b.PublishedVersion, Revision: b.Revision,
	}
}

// logoURLs are a version's logo URLs by slot. A published version's logos are
// served by the anonymous, content-addressed route (B15); a draft's, for
// "preview as me", by the permissioned one.
func logoURLs(basePath string, id uuid.UUID, v brandVersion, draft bool) gen.BrandLogoUrls {
	var out gen.BrandLogoUrls
	for slot, l := range v.Logos {
		var u string
		if draft {
			u = basePath + brandsPath + id.String() + "/logos/" + slot + "?draft=true"
		} else {
			ext := "png"
			if l.ContentType == "image/jpeg" {
				ext = "jpg"
			}
			u = basePath + brandAssetsPath + l.SHA256 + "." + ext
		}
		switch slot {
		case "mark_light":
			out.MarkLight = &u
		case "mark_dark":
			out.MarkDark = &u
		case "icon":
			out.Icon = &u
		case "wordmark":
			out.Wordmark = &u
		}
	}
	return out
}

// resolvedBrand is the payload of B36 for a version of a brand.
func (s *server) resolvedBrand(b brandRow, v brandVersion, draft bool) gen.ResolvedBrand {
	source, key := gen.ResolvedBrandSourceDefault, fmt.Sprintf("%s-%d", b.ID, b.PublishedVersion)
	if draft {
		source, key = gen.ResolvedBrandSourcePreview, fmt.Sprintf("%s-draft-%d", b.ID, b.Revision)
	}
	return gen.ResolvedBrand{
		V: brandPayloadVersion, Algorithm: int32(v.Algorithm), BrandId: b.ID, Version: key,
		Name: v.Name, Title: v.Title, Tokens: genTokens(v.Tokens), Support: genSupport(v.Support),
		Logos: logoURLs(s.deps.Config.BasePath, b.ID, v, draft), Source: source,
	}
}

// etagOf is a strong validator of a response body.
func etagOf(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// etagMatchesAny reports whether an If-None-Match header names tag: "*" or one
// of its comma-separated tags, weak or strong.
func etagMatchesAny(header *string, tag string) bool {
	if header == nil {
		return false
	}
	want := strings.TrimPrefix(tag, "W/")
	for _, t := range strings.Split(*header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == want {
			return true
		}
	}
	return false
}
