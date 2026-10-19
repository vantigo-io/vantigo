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
// never travels in an API URL), and the calendar's answer page on the same
// pattern (calendar invitations design D5), and the calendar feed, the one
// anonymous GET with a token in its path (calendar feed design D2).
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
	{http.MethodGet, "/health/ready"},
	{http.MethodHead, "/health/live"},
	{http.MethodGet, "/rsvp?token=x"},
	{http.MethodGet, "/rsvp"},
	{http.MethodHead, "/rsvp"},
	{http.MethodPost, "/api/v1/calendar/rsvp/read"},
	{http.MethodPost, "/api/v1/calendar/rsvp"},
	{http.MethodGet, "/api/v1/calendar/feed/abc.ics"},
}

// A sample of every other kind: the SPA's other paths, sign-in and setup,
// another module's API and Invoices' own — the Vipps webhook's path among
// them, which nothing serves while webhooks are deferred (the plan's Task
// 11), and the maintenance poll, which the pay page does not make — the
// documentation, the allowlisted
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
	{http.MethodPost, "/api/v1/invoices/vipps/webhooks"},
	{http.MethodGet, "/api/v1/identity/system/status"},
	{http.MethodPost, "/api/v1/identity/system/status"},
	{http.MethodGet, "/api"},
	{http.MethodGet, "/rsvp/x"},
	{http.MethodGet, "/rsvp/"},
	{http.MethodPost, "/rsvp"},
	{http.MethodGet, "/calendar"},
	{http.MethodGet, "/api/v1/calendar/rsvp/read"},
	{http.MethodGet, "/api/v1/calendar/rsvp/read?token=x"},
	{http.MethodGet, "/api/v1/calendar/rsvp"},
	{http.MethodPost, "/api/v1/calendar/rsvp/x"},
	{http.MethodGet, "/api/v1/calendar/appointments"},
	{http.MethodPost, "/api/v1/calendar/feed/abc.ics"},
	{http.MethodHead, "/api/v1/calendar/feed/abc.ics"},
	{http.MethodGet, "/api/v1/calendar/feed/"},
	{http.MethodGet, "/api/v1/calendar/feed"},
	{http.MethodGet, "/api/v1/calendar/feed/abc/def"},
}

// publicHostRefusedEscaped are paths whose escaping is not the canonical one
// — an encoded "/" above all — so the decoded path the gate would match and
// the escaped path the API router splits disagree: "pay%2Fattempts/pdf"
// decodes to an allowlisted attempt's status yet routes as the document
// "pay/attempts"'s PDF. Each is refused whatever it decodes to. They are sent
// without the base path, since StripBasePath re-derives a stripped path from
// its decoded form, where gate and router agree again.
var publicHostRefusedEscaped = []gateRequest{
	{http.MethodGet, "/api/v1/invoices/pay%2Fattempts/pdf"},
	{http.MethodPost, "/api/v1/invoices/vipps%2Fwebhooks"},
	{http.MethodPost, "/api%2Fv1/invoices/pay/read"},
	{http.MethodGet, "/p%61y"},
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
			doAt := func(host, base string, r gateRequest) *httptest.ResponseRecorder {
				req := httptest.NewRequest(r.method, "https://"+host+base+r.target, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}

			do := func(host string, r gateRequest) *httptest.ResponseRecorder {
				return doAt(host, tc.base, r)
			}

			for _, r := range publicHostAllowed {
				for _, host := range []string{"pay.example.com", "PAY.example.com:443"} {
					if rec := do(host, r); rec.Code != http.StatusTeapot {
						t.Errorf("%s %s on %s: status %d, want it to reach the next handler", r.method, r.target, host, rec.Code)
					}
				}
			}
			for _, r := range publicHostRefusedEscaped {
				if rec := doAt("pay.example.com", "", r); rec.Code != http.StatusNotFound {
					t.Errorf("%s %s on the public host: status %d, want 404 — a non-canonical escape is refused", r.method, r.target, rec.Code)
				}
				if rec := doAt("vantigo.example.com", "", r); rec.Code != http.StatusTeapot {
					t.Errorf("%s %s on APP_URL's host: status %d, want it untouched", r.method, r.target, rec.Code)
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
