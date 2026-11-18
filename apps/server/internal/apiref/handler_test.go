package apiref

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var build = fstest.MapFS{
	"index.html":                  {Data: []byte("<!doctype html><title>Vantigo API reference</title>")},
	"api/reference/apiref-abc.js": {Data: []byte("console.log(1)")},
	"api/reference/scalar-def.js": {Data: []byte("window.Scalar = {}")},
}

const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

func TestPrefersHTML(t *testing.T) {
	for accept, want := range map[string]bool{
		browserAccept:                       true,
		"text/html":                         true,
		"text/html;q=0.5, application/json": false,
		"application/json, text/html;q=0.9": false,
		"application/json":                  false,
		"*/*":                               false,
		"":                                  false,
		"text/*":                            false,
		"text/html;q=0":                     false,
		"application/problem+json":          false,
		"text/html, application/json":       false,
		"application/json, text/html":       false,
		"text/html;q=0.9, application/json;q=0.9":         false,
		"text/html;q=0.5, application/problem+json;q=0.5": false,
		"text/html, application/json;q=0.9":               true,
	} {
		var values []string
		if accept != "" {
			values = []string{accept}
		}
		if got := prefersHTML(values); got != want {
			t.Errorf("prefersHTML(%q) = %v, want %v", accept, got, want)
		}
	}
}

func TestServes(t *testing.T) {
	for _, c := range []struct {
		method, path, accept string
		want                 bool
	}{
		{http.MethodGet, "/api", browserAccept, true},
		{http.MethodHead, "/api", "text/html", true},
		{http.MethodGet, "/api", "*/*", false},
		{http.MethodGet, "/api", "application/json", false},
		{http.MethodPost, "/api", "text/html", false},
		{http.MethodGet, "/api/", browserAccept, false},
		{http.MethodGet, "/api/openapi.json", browserAccept, false},
		{http.MethodGet, "/api/reference/apiref-abc.js", "*/*", true},
		{http.MethodPost, "/api/reference/apiref-abc.js", "*/*", true},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Header.Set("Accept", c.accept)
		if got := Serves(r, c.path); got != c.want {
			t.Errorf("Serves(%s %s, Accept %q) = %v, want %v", c.method, c.path, c.accept, got, c.want)
		}
	}
}

func serve(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Accept", browserAccept)
	Handler(build).ServeHTTP(rec, r)
	return rec
}

func TestHandler_ThePage(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rec := serve(t, method, "/api")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s /api: status %d", method, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s /api: Content-Type %q", method, ct)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != ContentSecurityPolicy() {
			t.Errorf("%s /api: CSP %q", method, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s /api: Cache-Control %q, want no-cache", method, got)
		}
		if method == http.MethodGet && !strings.Contains(rec.Body.String(), "Vantigo API reference") {
			t.Errorf("GET /api: body %q", rec.Body.String())
		}
	}
}

// The policy is K27's to the letter: nothing but this origin, no eval, and no
// framing.
func TestContentSecurityPolicy(t *testing.T) {
	want := "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; " +
		"img-src 'self' data:; font-src 'self'; frame-ancestors 'none'"
	if got := ContentSecurityPolicy(); got != want {
		t.Errorf("policy = %q, want %q", got, want)
	}
}

func TestHandler_ReplacesTheApplicationsPolicy(t *testing.T) {
	for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		rec := httptest.NewRecorder()
		rec.Header().Set(key, "default-src 'self'; script-src 'self' 'sha256-x'")
		r := httptest.NewRequest(http.MethodGet, "/api", nil)
		Handler(build).ServeHTTP(rec, r)
		if got := rec.Header().Get(key); got != ContentSecurityPolicy() {
			t.Errorf("%s = %q, want the page's", key, got)
		}
	}
}

func TestHandler_TheAssets(t *testing.T) {
	rec := serve(t, http.MethodGet, "/api/reference/scalar-def.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "window.Scalar = {}" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control %q, want immutable", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type %q", ct)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != ContentSecurityPolicy() {
		t.Errorf("CSP %q", got)
	}
	for _, path := range []string{"/api/reference/", "/api/reference/missing.js", "/api/reference/../index.html", "/api/reference/x/apiref-abc.js", "/api/reference/%2e%2e%2findex.html"} {
		rec := serve(t, http.MethodGet, path)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: status %d Content-Type %q, want the 404 problem", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	if rec := serve(t, http.MethodPost, "/api/reference/apiref-abc.js"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rec.Code)
	}
}

// The committed placeholder is a page, so a build without the frontend still
// answers /api.
func TestAssets_ThePlaceholder(t *testing.T) {
	data, err := fs.ReadFile(Assets(), "index.html")
	if err != nil || !strings.Contains(string(data), "<html") {
		t.Fatalf("index.html: %v %q", err, data)
	}
}
