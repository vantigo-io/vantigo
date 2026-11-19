package identity

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
)

// The brand stylesheet (brands design B16): the brand as CSS custom
// properties, linked render-blocking from the document so the first paint is
// right. It is built only from generated values (hex colours, rgba() and var()
// references, calc() radii): no string a person typed reaches it.

// brandLoadedVar is the marker variable the pre-paint script looks for to know
// the stylesheet loaded; without it the script applies the browser's mirror.
const brandLoadedVar = "--vantigo-brand-loaded"

// safeCSSValue is what a generated value may contain.
func safeCSSValue(v string) bool {
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("#(),.% *-", r):
		default:
			return false
		}
	}
	return v != ""
}

// brandCSS renders a version's stylesheet: the shades and the radius on :root
// (they do not depend on the scheme), then each scheme's derived variables.
// Light is the default and dark is Mantine's attribute, as theme.css has it.
func brandCSS(v brandVersion) string {
	var b strings.Builder
	decl := func(name, value string) {
		if !safeCSSValue(value) {
			panic("identity: an unsafe value in the brand stylesheet: " + name) // generated values are safe; a regression must not ship
		}
		fmt.Fprintf(&b, "%s:%s;", name, value)
	}
	sorted := func(m map[string]string) []string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	b.WriteString(":root{")
	decl(brandLoadedVar, "1")
	for i, shade := range v.Tokens.Light.Shades {
		decl(fmt.Sprintf("--mantine-color-vantigo-%d", i), shade)
	}
	decl("--mantine-radius-default", v.Tokens.Radius)
	for _, k := range sorted(v.Tokens.Light.Derived) {
		decl(k, v.Tokens.Light.Derived[k])
	}
	b.WriteString("}\n:root[data-mantine-color-scheme=\"dark\"]{")
	for _, k := range sorted(v.Tokens.Dark.Derived) {
		decl(k, v.Tokens.Dark.Derived[k])
	}
	b.WriteString("}\n")
	return b.String()
}

// cssResponse answers the stylesheet with its validators.
type cssResponse struct {
	css          string
	etag         string
	cacheControl string
}

func (r cssResponse) headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", r.cacheControl)
	w.Header().Set("ETag", r.etag)
	w.Header().Set("Vary", "Cookie")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (r cssResponse) VisitGetIdentityBrandCssResponse(w http.ResponseWriter) error {
	r.headers(w)
	return gen.GetIdentityBrandCss200TextcssResponse{Body: strings.NewReader(r.css), ContentLength: int64(len(r.css))}.VisitGetIdentityBrandCssResponse(w)
}

type cssNotModified struct{ cssResponse }

func (r cssNotModified) VisitGetIdentityBrandCssResponse(w http.ResponseWriter) error {
	r.headers(w)
	return gen.GetIdentityBrandCss304Response{}.VisitGetIdentityBrandCssResponse(w)
}

// GetIdentityBrandCss Get the brand stylesheet (GET /api/v1/identity/brand.css)
//
// Anonymous, and it reads the session cookie itself when there is one (the
// access layer attaches the caller to an anonymous operation that has a live
// session) and never fails for want of one: without a session it is the
// default brand, with one the person's own (B7, B10). A stylesheet answered to
// a session is private and not stored, and every answer varies on the cookie,
// so a shared cache never serves one person's brand to another.
func (s *server) GetIdentityBrandCss(ctx context.Context, req gen.GetIdentityBrandCssRequestObject) (gen.GetIdentityBrandCssResponseObject, error) {
	// Anonymous, the default brand: public for a minute, varying on the cookie.
	// Some CDNs ignore Vary, so a session's own stylesheet is never stored by a
	// shared cache at all (no-store) rather than keyed by a header the edge may
	// not honour; the worst an ignoring CDN can then do is serve the default
	// brand, which is public anyway.
	cache := "public, max-age=60"
	var userID uuid.UUID
	if p, signedIn := callerIsSession(ctx); signedIn {
		cache = "private, no-store"
		userID = p.UserID
	}
	_, v, err := s.resolve(ctx, userID)
	if err != nil {
		return nil, err
	}
	css := brandCSS(v)
	resp := cssResponse{css: css, etag: etagOf([]byte(css)), cacheControl: cache}
	if etagMatchesAny(req.Params.IfNoneMatch, resp.etag) {
		s.metrics.cssRequests.Add(ctx, 1, cssResultAttr("not_modified"))
		return cssNotModified{resp}, nil
	}
	s.metrics.cssRequests.Add(ctx, 1, cssResultAttr("rendered"))
	return resp, nil
}
