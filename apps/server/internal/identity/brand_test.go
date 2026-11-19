package identity_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const (
	brandsPath      = "/api/v1/identity/brands"
	brandPath       = "/api/v1/identity/brand"
	brandCSSPath    = "/api/v1/identity/brand.css"
	brandPublicPath = "/api/v1/identity/brand/public"
	brandAssetsPath = "/api/v1/identity/brand-assets/"
)

// withBrandStorage gives the installation a filesystem object store, so brand
// logos can be stored.
func withBrandStorage(t *testing.T) harnessOption {
	dir := t.TempDir()
	return func(s *harnessSetup) {
		s.env["STORAGE_PROVIDER"] = "fs"
		s.env["STORAGE_FS_ROOT"] = dir
		s.env["STORAGE_FS_ALLOW_INSECURE_ROOT"] = "1"
	}
}

// brandBody is the admin's view of a brand as a test reads it.
type brandBody struct {
	ID               string `json:"id"`
	IsDefault        bool   `json:"isDefault"`
	State            string `json:"state"`
	Revision         int    `json:"revision"`
	PublishedVersion int    `json:"publishedVersion"`
	Published        *struct {
		Name    string            `json:"name"`
		Title   string            `json:"title"`
		Primary string            `json:"primary"`
		Radius  string            `json:"radius"`
		Support map[string]string `json:"support"`
		Logos   map[string]struct {
			Sha256      string `json:"sha256"`
			ContentType string `json:"contentType"`
		} `json:"logos"`
		Tokens struct {
			Light struct {
				Shades []string `json:"shades"`
			} `json:"light"`
		} `json:"tokens"`
	} `json:"published"`
	Draft *struct {
		Name    string `json:"name"`
		Primary string `json:"primary"`
		Logos   map[string]struct {
			Sha256 string `json:"sha256"`
		} `json:"logos"`
	} `json:"draft"`
	Checks []struct {
		Pair   string `json:"pair"`
		Scheme string `json:"scheme"`
		Pass   bool   `json:"pass"`
	} `json:"checks"`
	SuggestedPrimary     *string `json:"suggestedPrimary"`
	ImportNotice         *string `json:"importNotice"`
	LogoStorageAvailable bool    `json:"logoStorageAvailable"`
}

// resolvedBody is the payload of GET /brand.
type resolvedBody struct {
	V       int    `json:"v"`
	BrandID string `json:"brandId"`
	Version string `json:"version"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Source  string `json:"source"`
	Support map[string]string
	Logos   map[string]string `json:"logos"`
	Tokens  struct {
		Radius string `json:"radius"`
		Light  struct {
			Shades []string `json:"shades"`
		} `json:"light"`
		Dark struct {
			Derived map[string]string `json:"derived"`
		} `json:"dark"`
	} `json:"tokens"`
}

func getBrand(t *testing.T, c *client) brandBody {
	t.Helper()
	list := c.do(http.MethodGet, brandsPath, nil)
	if list.status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", brandsPath, list.status, list.body)
	}
	var l struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	list.json(&l)
	if len(l.Items) != 1 {
		t.Fatalf("GET %s: %d brands, want exactly the default", brandsPath, len(l.Items))
	}
	r := c.do(http.MethodGet, brandsPath+"/"+l.Items[0].ID, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET brand: status %d body %s", r.status, r.body)
	}
	var b brandBody
	r.json(&b)
	return b
}

func saveDraft(c *client, b brandBody, body map[string]any) *resp {
	return c.do(http.MethodPut, fmt.Sprintf("%s/%s", brandsPath, b.ID), body, header("If-Match", fmt.Sprint(b.Revision)))
}

func publish(c *client, b brandBody) *resp {
	return c.do(http.MethodPost, fmt.Sprintf("%s/%s/publish", brandsPath, b.ID), nil, header("If-Match", fmt.Sprint(b.Revision)))
}

func inputs(name, primary string) map[string]any {
	return map[string]any{"name": name, "title": name, "primary": primary, "radius": "md"}
}

func logoUpload(t *testing.T, data []byte) reqOpt {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="logo"`)
	h.Set("Content-Type", "image/png")
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return rawBody(w.FormDataContentType(), buf.Bytes())
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func putLogo(t *testing.T, c *client, b brandBody, slot string, data []byte) *resp {
	t.Helper()
	return c.do(http.MethodPut, fmt.Sprintf("%s/%s/logos/%s", brandsPath, b.ID, slot), nil, logoUpload(t, data))
}

func TestBrand_TheDefaultBrandExistsFromTheFirstMountAndIsVantigos(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)

	b := getBrand(t, owner)
	if !b.IsDefault || b.State != "published" || b.PublishedVersion != 1 || b.Published == nil || b.Draft != nil {
		t.Fatalf("default brand = %+v", b)
	}
	if b.Published.Name != "Vantigo" || b.Published.Title != "Vantigo" || b.Published.Primary != "#4f46e5" || b.Published.Radius != "md" {
		t.Errorf("published = %+v, want Vantigo's own", b.Published)
	}
	if b.Published.Tokens.Light.Shades[0] != "#eef2ff" || b.Published.Tokens.Light.Shades[6] != "#4f46e5" {
		t.Errorf("shades = %v, want Vantigo's own indigo", b.Published.Tokens.Light.Shades)
	}
	for _, c := range b.Checks {
		if !c.Pass {
			t.Errorf("check %s/%s fails for Vantigo's own colour", c.Scheme, c.Pair)
		}
	}
	if b.LogoStorageAvailable {
		t.Error("logoStorageAvailable without a store")
	}

	// A signed-in person sees the payload of B36.
	user := h.signIn(t, h.insertUser(t, "plain@example.test", identity.RoleUserID), false)
	r := user.do(http.MethodGet, brandPath, nil)
	var p resolvedBody
	r.json(&p)
	if r.status != http.StatusOK || p.V != 1 || p.Source != "default" || p.Title != "Vantigo" || p.Tokens.Radius != "calc(0.5rem * var(--mantine-scale))" {
		t.Fatalf("GET /brand: status %d body %s", r.status, r.body)
	}
	if p.Tokens.Dark.Derived["--mantine-color-vantigo-light"] != "rgba(25, 23, 65, 1)" {
		t.Errorf("dark light variant = %q", p.Tokens.Dark.Derived["--mantine-color-vantigo-light"])
	}
	if cc := r.headers.Get("Cache-Control"); cc != "private, no-cache" || r.headers.Get("ETag") == "" || !strings.Contains(r.headers.Get("Vary"), "Cookie") {
		t.Errorf("GET /brand headers = %v", r.headers)
	}
	// ETag round trip.
	r2 := user.do(http.MethodGet, brandPath, nil, header("If-None-Match", r.headers.Get("ETag")))
	if r2.status != http.StatusNotModified {
		t.Errorf("If-None-Match: status %d, want 304", r2.status)
	}
	if anon := h.client(t).do(http.MethodGet, brandPath, nil); anon.status != http.StatusUnauthorized {
		t.Errorf("GET /brand signed out: %d, want 401", anon.status)
	}
}

func TestBrand_PermissionOwnerAndDelegatedRolePassOthersAreRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	role := createRole(t, owner, "Brand editors", "identity:brands")
	editorID := h.insertUser(t, "editor@example.test", identity.RoleUserID)
	h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, editorID, role.ID)
	editor := h.signIn(t, editorID, false)
	plain := h.signIn(t, h.insertUser(t, "plain@example.test", identity.RoleUserID), false)

	if r := editor.do(http.MethodGet, brandsPath, nil); r.status != http.StatusOK {
		t.Errorf("a role with identity:brands: GET /brands = %d", r.status)
	}
	if r := plain.do(http.MethodGet, brandsPath, nil); r.status != http.StatusForbidden {
		t.Errorf("a plain user: GET /brands = %d, want 403", r.status)
	}
	if r := h.client(t).do(http.MethodGet, brandsPath, nil); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous: GET /brands = %d, want 401", r.status)
	}
	b := getBrand(t, owner)
	if r := plain.do(http.MethodPut, fmt.Sprintf("%s/%s", brandsPath, b.ID), inputs("x", "#0f766e"), header("If-Match", "1")); r.status != http.StatusForbidden {
		t.Errorf("a plain user: PUT = %d, want 403", r.status)
	}
	if r := plain.do(http.MethodGet, brandPath+"?preview="+b.ID, nil); r.status != http.StatusForbidden {
		t.Errorf("a plain user: GET /brand?preview = %d, want 403", r.status)
	}
	if r := editor.do(http.MethodGet, brandPath+"?preview="+b.ID, nil); r.status != http.StatusOK {
		t.Errorf("an editor: GET /brand?preview = %d, want 200", r.status)
	}
}

func TestBrand_DraftAndPublishAreSeparateAndAudited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	user := h.signIn(t, h.insertUser(t, "plain@example.test", identity.RoleUserID), false)
	b := getBrand(t, owner)

	r := saveDraft(owner, b, map[string]any{
		"name": "Acme", "title": "Acme Portal", "primary": "#0f766e", "radius": "lg",
		"support": map[string]any{"email": "help@acme.test", "phone": "+47 22 33 44 55", "url": "/help"},
	})
	var saved brandBody
	r.json(&saved)
	if r.status != http.StatusOK || saved.Draft == nil || saved.Draft.Name != "Acme" || saved.Revision != b.Revision+1 {
		t.Fatalf("save draft: status %d body %s", r.status, r.body)
	}
	if saved.Published == nil || saved.Published.Name != "Vantigo" {
		t.Errorf("the published version moved: %+v", saved.Published)
	}

	// A draft is invisible to GET /brand and to the stylesheet, visible to the preview.
	var p resolvedBody
	user.do(http.MethodGet, brandPath, nil).json(&p)
	if p.Name != "Vantigo" || p.Tokens.Light.Shades[6] != "#4f46e5" {
		t.Errorf("GET /brand shows the draft: %+v", p)
	}
	if css := h.client(t).do(http.MethodGet, brandCSSPath, nil); strings.Contains(string(css.body), "#0f766e") {
		t.Error("the stylesheet shows the draft")
	}
	owner.do(http.MethodGet, brandPath+"?preview="+b.ID, nil).json(&p)
	if p.Name != "Acme" || p.Source != "preview" || p.Tokens.Light.Shades[6] != "#0f766e" {
		t.Errorf("preview = %+v, want the draft", p)
	}

	// Stale If-Match.
	if r := saveDraft(owner, b, inputs("Stale", "#0f766e")); r.status != http.StatusConflict || r.code() != "stale_revision" {
		t.Errorf("stale save: status %d code %q, want 409 stale_revision", r.status, r.code())
	}
	if r := owner.do(http.MethodPut, fmt.Sprintf("%s/%s", brandsPath, b.ID), inputs("x", "#0f766e"), header("If-Match", "latest")); r.status != http.StatusBadRequest {
		t.Errorf("If-Match latest: %d, want 400", r.status)
	}

	// Publish.
	r = publish(owner, saved)
	var pub brandBody
	r.json(&pub)
	if r.status != http.StatusOK || pub.PublishedVersion != 2 || pub.Draft != nil || pub.Published.Name != "Acme" || pub.Published.Support["url"] != "/help" {
		t.Fatalf("publish: status %d body %s", r.status, r.body)
	}
	user.do(http.MethodGet, brandPath, nil).json(&p)
	if p.Name != "Acme" || p.Title != "Acme Portal" || p.Tokens.Light.Shades[6] != "#0f766e" || p.Tokens.Radius != "calc(1rem * var(--mantine-scale))" {
		t.Errorf("after publish GET /brand = %+v", p)
	}
	if r := publish(owner, pub); r.status != http.StatusConflict || r.code() != "no_draft" {
		t.Errorf("publish without a draft: status %d code %q, want 409 no_draft", r.status, r.code())
	}

	// Discard.
	r = saveDraft(owner, pub, inputs("Discarded", "#7c3aed"))
	var again brandBody
	r.json(&again)
	if r.status != http.StatusOK {
		t.Fatalf("second draft: %d %s", r.status, r.body)
	}
	d := owner.do(http.MethodPost, fmt.Sprintf("%s/%s/discard-draft", brandsPath, b.ID), nil, header("If-Match", fmt.Sprint(again.Revision)))
	var back brandBody
	d.json(&back)
	if d.status != http.StatusOK || back.Draft != nil || back.Published.Name != "Acme" {
		t.Errorf("discard: status %d body %s", d.status, d.body)
	}

	for action, want := range map[string]int{"brand.updated": 3, "brand.published": 1} {
		if got := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action = $1`, action); got != want {
			t.Errorf("audit %s = %d rows, want %d", action, got, want)
		}
	}
}

func TestBrand_InputsAreValidatedAndTokensAreNeverAccepted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	b := getBrand(t, owner)

	for name, support := range map[string]map[string]any{
		"javascript url":      {"url": "javascript:alert(1)"},
		"protocol relative":   {"url": "//evil.example"},
		"backslash host":      {"url": `/\evil.example`},
		"relative path":       {"url": "help/page"},
		"display name":        {"email": "Support <support@example.com>"},
		"phone with letters":  {"phone": "call me"},
		"31 character phone":  {"phone": "1234567890123456789012345678901"},
		"tel scheme in phone": {"phone": "tel:+4722334455"},
	} {
		body := inputs("Acme", "#0f766e")
		body["support"] = support
		r := saveDraft(owner, b, body)
		if r.status != http.StatusUnprocessableEntity || r.code() != "brand_support_invalid" {
			t.Errorf("%s: status %d code %q body %s, want 422 brand_support_invalid", name, r.status, r.code(), r.body)
		}
	}
	for name, primary := range map[string]string{"white": "#ffffff", "black": "#000000"} {
		if r := saveDraft(owner, b, inputs("Acme", primary)); r.status != http.StatusUnprocessableEntity || r.code() != "brand_primary_range" {
			t.Errorf("%s: status %d code %q, want 422 brand_primary_range", name, r.status, r.code())
		}
	}
	for name, body := range map[string]map[string]any{
		"bad colour": inputs("Acme", "teal"),
		"no name":    inputs("", "#0f766e"),
		"bad radius": {"name": "a", "primary": "#0f766e", "radius": "huge"},
	} {
		r := saveDraft(owner, b, body)
		if r.status != http.StatusBadRequest && r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 400 or 422", name, r.status)
		}
	}

	// B37: tokens, shades and the derived set are refused, not ignored.
	for _, member := range []string{"tokens", "shades", "derived", "filledText"} {
		body := inputs("Acme", "#0f766e")
		body[member] = map[string]any{"light": map[string]any{"shades": []string{"#000000"}}}
		r := saveDraft(owner, b, body)
		if r.status != http.StatusBadRequest || r.code() != "invalid_request" {
			t.Errorf("a body with %s: status %d code %q, want 400 invalid_request", member, r.status, r.code())
		}
	}
	// Nothing above wrote a draft.
	if got := getBrand(t, owner); got.Draft != nil || got.Revision != b.Revision {
		t.Errorf("a refused save wrote: %+v", got)
	}
}

func TestBrand_PublishIsRefusedOnAFailingContrastPair(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	b := getBrand(t, owner)

	var failing string
	for _, candidate := range []string{"#ef4444", "#f97316", "#3b82f6"} {
		if !identity.BrandChecksPass(candidate) {
			failing = candidate
			break
		}
	}
	if failing == "" {
		t.Fatal("none of the candidate colours fails a contrast check")
	}
	r := saveDraft(owner, b, inputs("Bright", failing))
	var saved brandBody
	r.json(&saved)
	if r.status != http.StatusOK {
		t.Fatalf("a failing draft may be saved: status %d body %s", r.status, r.body)
	}
	failed := 0
	for _, c := range saved.Checks {
		if !c.Pass {
			failed++
		}
	}
	if failed == 0 || saved.SuggestedPrimary == nil || !identity.BrandChecksPass(*saved.SuggestedPrimary) {
		t.Fatalf("checks = %+v, suggestion = %v", saved.Checks, saved.SuggestedPrimary)
	}
	pr := publish(owner, saved)
	if pr.status != http.StatusUnprocessableEntity || pr.code() != "brand_contrast" {
		t.Fatalf("publish: status %d code %q body %s, want 422 brand_contrast", pr.status, pr.code(), pr.body)
	}
	// The suggested primary can be saved and published.
	r = saveDraft(owner, saved, inputs("Bright", *saved.SuggestedPrimary))
	var fixed brandBody
	r.json(&fixed)
	if pr := publish(owner, fixed); pr.status != http.StatusOK {
		t.Errorf("publish the suggestion: status %d body %s", pr.status, pr.body)
	}
}

func TestBrand_LogosWriteTheDraftAndTheAssetRouteServesOnlyPublishedOnes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withBrandStorage(t))
	owner, _ := h.bootstrapOwner(t)
	plain := h.signIn(t, h.insertUser(t, "plain@example.test", identity.RoleUserID), false)
	anon := h.client(t)
	b := getBrand(t, owner)
	if !b.LogoStorageAvailable {
		t.Fatal("logoStorageAvailable is false with a store")
	}

	r := putLogo(t, owner, b, "mark_light", pngOf(t, 64, 32))
	var withLogo brandBody
	r.json(&withLogo)
	if r.status != http.StatusOK || withLogo.Draft == nil || withLogo.Draft.Logos["mark_light"].Sha256 == "" {
		t.Fatalf("PUT logo: status %d body %s", r.status, r.body)
	}
	if len(withLogo.Published.Logos) != 0 {
		t.Errorf("a logo PUT changed the published version: %+v", withLogo.Published.Logos)
	}
	sha := withLogo.Draft.Logos["mark_light"].Sha256

	// The draft logo streams to identity:brands only; the anonymous route 404s it.
	d := owner.do(http.MethodGet, fmt.Sprintf("%s/%s/logos/mark_light?draft=true", brandsPath, b.ID), nil)
	if d.status != http.StatusOK || d.headers.Get("Content-Type") != "image/png" || d.headers.Get("X-Content-Type-Options") != "nosniff" || !bytes.HasPrefix(d.body, []byte("\x89PNG")) {
		t.Errorf("GET draft logo: status %d headers %v", d.status, d.headers)
	}
	if r := owner.do(http.MethodGet, fmt.Sprintf("%s/%s/logos/mark_light", brandsPath, b.ID), nil); r.status != http.StatusNotFound {
		t.Errorf("GET published logo before a publish: %d, want 404", r.status)
	}
	if r := plain.do(http.MethodGet, fmt.Sprintf("%s/%s/logos/mark_light?draft=true", brandsPath, b.ID), nil); r.status != http.StatusForbidden {
		t.Errorf("a plain user reads a draft logo: %d, want 403", r.status)
	}
	if r := anon.do(http.MethodGet, brandAssetsPath+sha+".png", nil); r.status != http.StatusNotFound {
		t.Errorf("anonymous asset of an unpublished draft: %d, want 404", r.status)
	}

	// Rules of B6: the icon is square, the file is a PNG or JPEG.
	if r := putLogo(t, owner, b, "icon", pngOf(t, 64, 32)); r.status != http.StatusBadRequest || r.code() != "logo_not_square" {
		t.Errorf("a wide icon: status %d code %q, want 400 logo_not_square", r.status, r.code())
	}
	if r := putLogo(t, owner, b, "icon", pngOf(t, 40, 40)); r.status != http.StatusOK {
		t.Errorf("a square icon: %d %s", r.status, r.body)
	}
	if r := putLogo(t, owner, b, "wordmark", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")); r.status != http.StatusBadRequest || r.code() != "logo_unsupported_type" {
		t.Errorf("an SVG: status %d code %q, want 400 logo_unsupported_type", r.status, r.code())
	}
	if r := putLogo(t, owner, b, "mark_dark", pngOf(t, 8, 8)); r.status != http.StatusBadRequest || r.code() != "logo_too_small" {
		t.Errorf("a tiny logo: status %d code %q, want 400 logo_too_small", r.status, r.code())
	}
	if r := putLogo(t, owner, b, "mark_dark", pngOf(t, 2001, 20)); r.status != http.StatusBadRequest || r.code() != "logo_too_wide" {
		t.Errorf("a huge logo: status %d code %q, want 400 logo_too_wide", r.status, r.code())
	}

	// Publish, and the route serves it: anonymous, immutable, nosniff, content-addressed.
	var now brandBody
	owner.do(http.MethodGet, fmt.Sprintf("%s/%s", brandsPath, b.ID), nil).json(&now)
	if pr := publish(owner, now); pr.status != http.StatusOK {
		t.Fatalf("publish: %d %s", pr.status, pr.body)
	}
	a := anon.do(http.MethodGet, brandAssetsPath+sha+".png", nil)
	if a.status != http.StatusOK || a.headers.Get("Cache-Control") != "public, max-age=31536000, immutable" || a.headers.Get("X-Content-Type-Options") != "nosniff" || a.headers.Get("Content-Type") != "image/png" {
		t.Errorf("GET asset: status %d headers %v", a.status, a.headers)
	}
	if r := anon.do(http.MethodGet, brandAssetsPath+sha+".jpg", nil); r.status != http.StatusNotFound {
		t.Errorf("the right hash with the wrong extension: %d, want 404", r.status)
	}
	if r := anon.do(http.MethodGet, brandAssetsPath+strings.Repeat("0", 64)+".png", nil); r.status != http.StatusNotFound {
		t.Errorf("an unknown hash: %d, want 404", r.status)
	}
	if r := anon.do(http.MethodGet, brandAssetsPath+"..%2Fsecret.png", nil, skipContract("a path that is not a file name")); r.status != http.StatusNotFound {
		t.Errorf("a path-like file: %d, want 404", r.status)
	}

	// The payload carries the URL, under the same path.
	var p resolvedBody
	plain.do(http.MethodGet, brandPath, nil).json(&p)
	if !strings.HasSuffix(p.Logos["mark_light"], brandAssetsPath+sha+".png") {
		t.Errorf("logos = %v", p.Logos)
	}

	// Remove one from the draft.
	del := owner.do(http.MethodDelete, fmt.Sprintf("%s/%s/logos/icon", brandsPath, b.ID), nil)
	var after brandBody
	del.json(&after)
	if del.status != http.StatusOK || after.Draft == nil || after.Draft.Logos["icon"].Sha256 != "" || after.Draft.Logos["mark_light"].Sha256 == "" {
		t.Errorf("DELETE logo: status %d body %s", del.status, del.body)
	}
	for action, want := range map[string]int{"brand.logo_set": 2, "brand.logo_removed": 1} {
		if got := h.count(t, `SELECT count(*) FROM identity.authorization_audit_events WHERE action = $1`, action); got != want {
			t.Errorf("audit %s = %d, want %d", action, got, want)
		}
	}
}

func TestBrand_WithoutAnObjectStoreLogosAreRefusedAndBrandsStillWork(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	b := getBrand(t, owner)
	r := putLogo(t, owner, b, "mark_light", pngOf(t, 64, 32))
	if r.status != http.StatusServiceUnavailable || r.code() != "storage_unavailable" {
		t.Errorf("PUT logo: status %d code %q, want 503 storage_unavailable", r.status, r.code())
	}
	if r := saveDraft(owner, b, inputs("Acme", "#0f766e")); r.status != http.StatusOK {
		t.Errorf("brands without a store: %d", r.status)
	}
}

func TestBrand_AssetRouteIsRateLimitedPerAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.client(t)
	path := brandAssetsPath + strings.Repeat("a", 64) + ".png"
	var limited int
	for i := 0; i < 620; i++ {
		if c.do(http.MethodGet, path, nil, skipContract("a rate-limit sweep")).status == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Error("620 requests from one address were never limited")
	}
	if r := h.client(t).do(http.MethodGet, path, nil); r.status != http.StatusNotFound {
		t.Errorf("another address: %d, want 404", r.status)
	}
}

func TestBrandCSS_DefaultWithoutASessionAndOwnWithOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	anon := h.client(t)

	r := anon.do(http.MethodGet, brandCSSPath, nil)
	css := string(r.body)
	if r.status != http.StatusOK || r.headers.Get("Content-Type") != "text/css" ||
		r.headers.Get("Cache-Control") != "public, max-age=60" || r.headers.Get("ETag") == "" ||
		!strings.Contains(r.headers.Get("Vary"), "Cookie") {
		t.Fatalf("GET brand.css: status %d headers %v", r.status, r.headers)
	}
	for _, want := range []string{"--vantigo-brand-loaded:1", "--mantine-color-vantigo-6:#4f46e5;", "--mantine-radius-default:calc(0.5rem * var(--mantine-scale));", `:root[data-mantine-color-scheme="dark"]{`} {
		if !strings.Contains(css, want) {
			t.Errorf("brand.css is missing %q:\n%s", want, css)
		}
	}
	if r := anon.do(http.MethodGet, brandCSSPath, nil, header("If-None-Match", r.headers.Get("ETag"))); r.status != http.StatusNotModified {
		t.Errorf("If-None-Match: %d, want 304", r.status)
	}

	// With a session it is private and revalidated, and still varies on the cookie.
	s := owner.do(http.MethodGet, brandCSSPath, nil)
	if s.status != http.StatusOK || s.headers.Get("Cache-Control") != "private, no-cache" || !strings.Contains(s.headers.Get("Vary"), "Cookie") {
		t.Errorf("GET brand.css with a session: status %d headers %v", s.status, s.headers)
	}

	// A published change reaches it, and only generated values do: no input string.
	b := getBrand(t, owner)
	body := inputs(`<script>alert(1)</script>`, "#0f766e")
	body["support"] = map[string]any{"url": "/help"}
	var saved brandBody
	saveDraft(owner, b, body).json(&saved)
	publish(owner, saved)
	next := string(anon.do(http.MethodGet, brandCSSPath, nil).body)
	if !strings.Contains(next, "--mantine-color-vantigo-6:#0f766e;") || strings.Contains(next, "script") || strings.Contains(next, "help") {
		t.Errorf("brand.css after a publish:\n%s", next)
	}
}

func TestBrandPublic_IsAnonymousAndCached(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("APP_TITLE", "Acme CRM"))
	r := h.client(t).do(http.MethodGet, brandPublicPath, nil)
	var p resolvedBody
	r.json(&p)
	if r.status != http.StatusOK || p.Title != "Acme CRM" || r.headers.Get("Cache-Control") != "public, max-age=60" {
		t.Errorf("GET brand/public: status %d headers %v body %s", r.status, r.headers, r.body)
	}
}

// The environment's branding becomes the default brand once (B30).
func TestBrandImport_TitleAndSupportFromTheEnvironmentAreImportedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t,
		withEnv("APP_TITLE", "Acme CRM"),
		withEnv("APP_SUPPORT_EMAIL", "help@acme.test"),
		withEnv("APP_SUPPORT_PHONE", "+47 22 33 44 55"),
		withEnv("APP_SUPPORT_URL", "https://acme.test/help"),
	)
	owner, _ := h.bootstrapOwner(t)
	b := getBrand(t, owner)
	if b.Published.Title != "Acme CRM" || b.Published.Name != "Acme CRM" || b.Published.Support["email"] != "help@acme.test" ||
		b.Published.Support["phone"] != "+47 22 33 44 55" || b.Published.Support["url"] != "https://acme.test/help" {
		t.Errorf("imported = %+v", b.Published)
	}
	imported, err := identity.BrandingImported(context.Background(), h.deps)
	if err != nil || !imported {
		t.Errorf("BrandingImported = %v, %v, want true (no logo to fetch)", imported, err)
	}
	// A second start does not import again, even with other values.
	h.cfg.Branding.Title = "Other"
	if created, err := identity.EnsureDefaultBrand(context.Background(), h.deps); err != nil || created {
		t.Errorf("a second EnsureDefaultBrand = %v, %v, want not created", created, err)
	}
	if got := getBrand(t, owner); got.Published.Title != "Acme CRM" {
		t.Errorf("title after a second start = %q", got.Published.Title)
	}
}

func TestBrandImport_AnInvalidSupportContactIsLeftOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("APP_SUPPORT_PHONE", "call me maybe"), withEnv("APP_SUPPORT_EMAIL", "help@acme.test"))
	owner, _ := h.bootstrapOwner(t)
	b := getBrand(t, owner)
	if b.Published.Support["phone"] != "" || b.Published.Support["email"] != "help@acme.test" {
		t.Errorf("support = %v", b.Published.Support)
	}
}

func TestBrandImport_TwoReplicasCreateOneDefaultBrand(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("APP_TITLE", "Acme"))
	h.exec(t, `DELETE FROM identity.brands`)
	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := identity.EnsureDefaultBrand(context.Background(), h.deps)
			if err != nil {
				t.Error(err)
			}
			if ok {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Errorf("%d replicas created the default brand, want exactly one", created.Load())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.brands WHERE is_default`); n != 1 {
		t.Errorf("%d default brands", n)
	}
}

// mapResolver resolves each name to the addresses it is given.
type mapResolver map[string][]string

func (m mapResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, a := range m[host] {
		out = append(out, netip.MustParseAddr(a))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no such host %s", host)
	}
	return out, nil
}

// logoImportHarness is an installation with APP_LOGO_URL set to url, a store,
// and a network whose dial goes to dialTo (counted).
func logoImportHarness(t *testing.T, url string, resolver mapResolver, dialTo string) (*harness, *atomic.Int32) {
	t.Helper()
	h := newHarness(t, withBrandStorage(t), withEnv("APP_LOGO_URL", url), withEnv("APP_TITLE", "Acme"))
	var dials atomic.Int32
	identity.SetBrandImportNetwork(h.access, resolver, func(ctx context.Context, network, _ string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, dialTo)
	})
	return h, &dials
}

func TestBrandImport_TheLogoIsFetchedOnceAfterMountThroughTheGuard(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	release := make(chan struct{})
	logo := pngOf(t, 120, 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(logo)
	}))
	defer srv.Close()
	// The fetch is blocked, and the installation has mounted all the same.
	h, _ := logoImportHarness(t, "http://logo.example.com/logo.png", mapResolver{"logo.example.com": {"93.184.215.14"}}, srv.Listener.Addr().String())
	owner, _ := h.bootstrapOwner(t)
	if imported, _ := identity.BrandingImported(context.Background(), h.deps); imported {
		t.Fatal("the import is complete before the logo was fetched")
	}
	if b := getBrand(t, owner); len(b.Published.Logos) != 0 || b.Published.Title != "Acme" {
		t.Fatalf("before the fetch: %+v", b.Published)
	}

	close(release)
	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps)
			if err != nil {
				t.Error(err)
			}
			results[i] = done
		}()
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Errorf("the logo was fetched %d times by two runners, want once", hits.Load())
	}
	imported, _ := identity.BrandingImported(context.Background(), h.deps)
	b := getBrand(t, owner)
	if !imported || b.Published.Logos["mark_light"].Sha256 == "" || b.ImportNotice != nil || b.PublishedVersion != 2 {
		t.Errorf("after the import: imported=%v brand=%+v", imported, b)
	}
	if done, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps); err != nil || !done || hits.Load() != 1 {
		t.Errorf("a later pass: done=%v err=%v hits=%d, want complete without a fetch", done, err, hits.Load())
	}
	if n := h.count(t, `SELECT count(*) FROM identity.brand_logos`); n != 1 {
		t.Errorf("%d registered logos", n)
	}
}

func TestBrandImport_RefusedAddressesAreNeverDialled(t *testing.T) {
	t.Parallel()
	hosts := mapResolver{
		"loopback.example.com": {"127.0.0.1"},
		"private.example.com":  {"10.0.0.5"},
		"metadata.example.com": {"169.254.169.254"},
		"v6.example.com":       {"::1"},
		"mixed.example.com":    {"93.184.215.14", "10.0.0.5"},
	}
	for _, url := range []string{
		"http://127.0.0.1/logo.png", "http://10.0.0.5/logo.png", "http://169.254.169.254/latest/meta-data/",
		"http://[::1]/logo.png", "http://loopback.example.com/logo.png", "http://private.example.com/logo.png",
		"http://metadata.example.com/logo.png", "http://v6.example.com/logo.png", "http://mixed.example.com/logo.png",
	} {
		t.Run(url, func(t *testing.T) {
			t.Parallel()
			h, dials := logoImportHarness(t, url, hosts, "127.0.0.1:9")
			owner, _ := h.bootstrapOwner(t)
			done, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps)
			if err != nil || !done {
				t.Fatalf("done=%v err=%v, want the import completed with a notice", done, err)
			}
			if dials.Load() != 0 {
				t.Errorf("%d connections were dialled to a refused address", dials.Load())
			}
			b := getBrand(t, owner)
			if b.ImportNotice == nil || !strings.Contains(*b.ImportNotice, "APP_LOGO_URL") || len(b.Published.Logos) != 0 || b.Published.Title != "Acme" {
				t.Errorf("brand = %+v notice %v: want everything else imported and a notice", b.Published, b.ImportNotice)
			}
			if strings.Contains(*b.ImportNotice, "10.0.0.5") || strings.Contains(*b.ImportNotice, "169.254") {
				t.Errorf("the notice leaks the address: %q", *b.ImportNotice)
			}
			if imported, _ := identity.BrandingImported(context.Background(), h.deps); !imported {
				t.Error("a failed logo must still complete the import")
			}
		})
	}
}

func TestBrandImport_RedirectsAndBadBodiesAreFailures(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 2<<20+10))
	})
	mux.HandleFunc("/text", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) })
	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	for path, want := range map[string]string{
		"/redirect": "redirects", "/big": "2 MiB", "/text": "not a PNG or JPEG", "/missing": "did not answer 200",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			h, _ := logoImportHarness(t, "http://logo.example.com"+path, mapResolver{"logo.example.com": {"93.184.215.14"}}, srv.Listener.Addr().String())
			owner, _ := h.bootstrapOwner(t)
			if done, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps); err != nil || !done {
				t.Fatalf("done=%v err=%v", done, err)
			}
			b := getBrand(t, owner)
			if b.ImportNotice == nil || !strings.Contains(*b.ImportNotice, want) || len(b.Published.Logos) != 0 {
				notice := "<none>"
				if b.ImportNotice != nil {
					notice = *b.ImportNotice
				}
				t.Errorf("notice = %q logos = %v, want a notice containing %q", notice, b.Published.Logos, want)
			}
		})
	}
}

func TestBrandImport_WithoutAnObjectStoreTheRestIsImportedAndANoticeIsRaised(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("APP_LOGO_URL", "http://logo.example.com/logo.png"), withEnv("APP_TITLE", "Acme"))
	owner, _ := h.bootstrapOwner(t)
	if done, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps); err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	b := getBrand(t, owner)
	if b.ImportNotice == nil || !strings.Contains(*b.ImportNotice, "object store") || b.Published.Title != "Acme" {
		t.Errorf("brand = %+v notice = %v", b.Published, b.ImportNotice)
	}
}

func TestBrandMetrics_CountTheStylesheetPublishesAndTheImport(t *testing.T) {
	t.Parallel()
	reader := metric.NewManualReader()
	h := newHarness(t, withBrandMeter(metric.NewMeterProvider(metric.WithReader(reader))), withEnv("APP_TITLE", "Acme"))
	owner, _ := h.bootstrapOwner(t)
	anon := h.client(t)
	r := anon.do(http.MethodGet, brandCSSPath, nil)
	anon.do(http.MethodGet, brandCSSPath, nil, header("If-None-Match", r.headers.Get("ETag")))
	anon.do(http.MethodGet, brandPublicPath, nil)
	b := getBrand(t, owner)
	var saved brandBody
	saveDraft(owner, b, inputs("Acme", "#0f766e")).json(&saved)
	publish(owner, saved)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	sums := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if s, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range s.DataPoints {
					sums[m.Name] += dp.Value
				}
			}
		}
	}
	for name, min := range map[string]int64{
		"vantigo.identity.brand.css_requests": 2, "vantigo.identity.brand.public_lookups": 1,
		"vantigo.identity.brand.publishes": 1,
	} {
		if sums[name] < min {
			t.Errorf("%s = %d, want at least %d (all: %v)", name, sums[name], min, sums)
		}
	}
}
