package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// gateRequest is one request in TestPublicHostGate: a method and a path,
// base-relative, query included.
type gateRequest struct {
	method, target string
}

// The allowlist, exact (payments port design D14 as amended by its plan's
// reading 23: the pay page reads with POST /invoices/pay/read, so the token
// never travels in an API URL).
var publicHostAllowed = []gateRequest{
	{http.MethodGet, "/pay?token=x"},
	{http.MethodGet, "/pay"},
	{http.MethodHead, "/pay"},
	{http.MethodGet, "/assets/index-abc.js"},
	{http.MethodHead, "/assets/index-abc.js"},
	{http.MethodGet, "/assets/fonts/inter.woff2"},
	{http.MethodGet, "/favicon.png"},
	{http.MethodHead, "/favicon.png"},
	{http.MethodPost, "/api/v1/invoices/pay/read"},
	{http.MethodPost, "/api/v1/invoices/pay/attempts"},
	{http.MethodGet, "/api/v1/invoices/pay/attempts/abc"},
	{http.MethodPost, "/api/v1/invoices/vipps/webhooks"},
	{http.MethodGet, "/api/v1/identity/system/status"},
	{http.MethodGet, "/health/ready"},
	{http.MethodHead, "/health/live"},
}

// A sample of every other kind: the SPA's other paths, sign-in and setup,
// another module's API and Invoices' own, the documentation, the allowlisted
// paths under another method or with one segment too many, and a path that
// climbs out of an allowlisted prefix.
var publicHostRefused = []gateRequest{
	{http.MethodGet, "/"},
	{http.MethodGet, "/sign-in"},
	{http.MethodGet, "/setup"},
	{http.MethodGet, "/invoices"},
	{http.MethodGet, "/pay/x"},
	{http.MethodGet, "/pay/"},
	{http.MethodPost, "/pay"},
	{http.MethodGet, "/docs/"},
	{http.MethodGet, "/docs"},
	{http.MethodPost, "/assets/index-abc.js"},
	{http.MethodGet, "/assets/../sign-in"},
	{http.MethodGet, "/health/../api/v1/customers"},
	{http.MethodGet, "/api/v1/identity/me"},
	{http.MethodPost, "/api/v1/identity/sign-in"},
	{http.MethodGet, "/api/v1/customers"},
	{http.MethodGet, "/api/v1/invoices"},
	{http.MethodGet, "/api/v1/invoices/meta"},
	{http.MethodGet, "/api/v1/invoices/pay"},
	{http.MethodPut, "/api/v1/invoices/pay"},
	{http.MethodGet, "/api/v1/invoices/pay/read"},
	{http.MethodGet, "/api/v1/invoices/pay/read?token=x"},
	{http.MethodGet, "/api/v1/invoices/pay/attempts"},
	{http.MethodGet, "/api/v1/invoices/pay/attempts/"},
	{http.MethodGet, "/api/v1/invoices/pay/attempts/abc/def"},
	{http.MethodGet, "/api/v1/invoices/pay/attempts/abc%2Fdef"},
	{http.MethodPost, "/api/v1/invoices/pay/attempts/abc"},
	{http.MethodGet, "/api/v1/invoices/vipps/webhooks"},
	{http.MethodPost, "/api/v1/identity/system/status"},
	{http.MethodGet, "/api"},
}

// TestPublicHostGate is D14's test: on the public host every allowlisted
// method and path reaches the next handler and everything else answers 404 —
// a problem under /api/, an empty body elsewhere — before any routing; on
// APP_URL's host every one of them passes untouched. Under a base path the
// same holds, because the gate runs after StripBasePath and sees base-relative
// paths.
func TestPublicHostGate(t *testing.T) {
	for _, tc := range []struct {
		name, base string
	}{
		{"at the domain root", ""},
		{"under a base path", "/vantigo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := httpx.Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			}),
				httpx.StripBasePath(tc.base),
				PublicHostGate("Pay.Example.com", PayRoutes()),
			)
			do := func(host string, r gateRequest) *httptest.ResponseRecorder {
				req := httptest.NewRequest(r.method, "https://"+host+tc.base+r.target, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}

			for _, r := range publicHostAllowed {
				for _, host := range []string{"pay.example.com", "PAY.example.com:443"} {
					if rec := do(host, r); rec.Code != http.StatusTeapot {
						t.Errorf("%s %s on %s: status %d, want it to reach the next handler", r.method, r.target, host, rec.Code)
					}
				}
			}
			for _, r := range publicHostRefused {
				rec := do("pay.example.com", r)
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s %s on the public host: status %d, want 404", r.method, r.target, rec.Code)
					continue
				}
				isAPI := r.target == "/api" || len(r.target) >= 5 && r.target[:5] == "/api/"
				if isAPI && rec.Header().Get("Content-Type") != "application/problem+json" {
					t.Errorf("%s %s: Content-Type %q, want a problem under /api/", r.method, r.target, rec.Header().Get("Content-Type"))
				}
				if !isAPI && rec.Body.Len() != 0 {
					t.Errorf("%s %s: body %q, want an empty one outside /api/", r.method, r.target, rec.Body.String())
				}
			}
			for _, r := range append(append([]gateRequest{}, publicHostAllowed...), publicHostRefused...) {
				if rec := do("vantigo.example.com", r); rec.Code != http.StatusTeapot {
					t.Errorf("%s %s on APP_URL's host: status %d, want it untouched", r.method, r.target, rec.Code)
				}
			}
		})
	}
}
