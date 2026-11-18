package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/apiref"
	"github.com/vantigo-io/vantigo/server/internal/config"
)

const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// A browser's GET of /api is the reference page, with its own policy, through
// the real composition; an API client's is still the API's 404 problem (K26).
func TestNew_APIServesTheReferenceToABrowser(t *testing.T) {
	for _, base := range []string{"", "/vantigo"} {
		f := newFixture(t, func(cfg *config.Config, o *Options) {
			cfg.BasePath = base
			o.API = composedAPI(t)
		})
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := f.do(request(method, "https://vantigo.example.com"+base+"/api", "Accept", browserAccept))
			if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("%s %s/api as a browser: status %d Content-Type %q", method, base, rec.Code, rec.Header().Get("Content-Type"))
			}
			if got := rec.Header().Get("Content-Security-Policy"); got != apiref.ContentSecurityPolicy() {
				t.Errorf("%s/api: CSP %q, want the page's own", base, got)
			}
			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("%s/api: X-Frame-Options %q, want the application's headers kept", base, got)
			}
			if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Accept" {
				t.Errorf("%s/api: Vary %q, want Accept once", base, got)
			}
		}
		for _, accept := range []string{"", "*/*", "application/json"} {
			rec := f.do(request(http.MethodGet, "https://vantigo.example.com"+base+"/api", "Accept", accept))
			if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Errorf("%s/api with Accept %q: status %d Content-Type %q, want the 404 problem", base, accept, rec.Code, rec.Header().Get("Content-Type"))
			}
			if !strings.Contains(rec.Header().Get("Vary"), "Accept") {
				t.Errorf("%s/api with Accept %q: Vary %q, want Accept", base, accept, rec.Header().Get("Vary"))
			}
		}
		rec := f.do(request(http.MethodPost, "https://vantigo.example.com"+base+"/api", "Accept", browserAccept))
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s/api: status %d, want the API's 404", base, rec.Code)
		}
		rec = f.do(request(http.MethodGet, "https://vantigo.example.com"+base+"/api/reference/missing.js"))
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("a missing asset: status %d Content-Type %q, want the 404 problem", rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// The public host's allowlist does not admit the reference (K26): neither the
// page nor its files.
func TestServer_PublicHostRefusesTheReference(t *testing.T) {
	f := newFixture(t, func(cfg *config.Config, _ *Options) {
		cfg.PublicOrigin = "https://pay.example.com"
		cfg.PublicHostname = "pay.example.com"
		cfg.PublicBaseURL = cfg.PublicOrigin
	})
	for _, path := range []string{"/api", "/api/reference/apiref.js", "/api/openapi.json"} {
		rec := f.do(request(http.MethodGet, "https://pay.example.com"+path, "Accept", browserAccept))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s on the public host: status %d, want 404", path, rec.Code)
		}
		if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s on the public host served HTML", path)
		}
	}
	if rec := f.do(request(http.MethodGet, "https://vantigo.example.com/api", "Accept", browserAccept)); rec.Code != http.StatusOK {
		t.Errorf("/api on APP_URL's host: status %d, want the page", rec.Code)
	}
}
