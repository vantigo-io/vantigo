package identity

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

func setCookie(t *testing.T, a *Access, write func(http.ResponseWriter)) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	write(rec)
	cookies := (&http.Response{Header: rec.Header()}).Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie: %v, want one cookie", rec.Header().Values("Set-Cookie"))
	}
	return cookies[0], rec.Header().Get("Set-Cookie")
}

// TestSessionCookieAttributes pins the session cookie: HttpOnly,
// SameSite=Strict, the base path or /, Secure unless development or
// insecure transport, a Max-Age of the absolute lifetime only when
// persistent, and a clearing that expires it. The login-ticket cookie has
// the same attributes and lives the ticket's five minutes.
func TestSessionCookieAttributes(t *testing.T) {
	cases := []struct {
		name       string
		cfg        config.Config
		wantSecure bool
		wantPath   string
	}{
		{"production", config.Config{Env: config.Production}, true, "/"},
		{"production with a base path", config.Config{Env: config.Production, BasePath: "/vantigo"}, true, "/vantigo"},
		{"development", config.Config{Env: config.Development}, false, "/"},
		{"insecure transport", config.Config{Env: config.Production, AllowInsecureTransport: true}, false, "/"},
	}
	for _, c := range cases {
		c.cfg.Sessions.Absolute = 24 * time.Hour
		a := &Access{cfg: &c.cfg}

		for _, persistent := range []bool{false, true} {
			got, raw := setCookie(t, a, func(w http.ResponseWriter) { cookies{a.newSessionCookie("tok", persistent)}.set(w) })
			if got.Name != "vantigo.session" || got.Value != "tok" || !got.HttpOnly || got.SameSite != http.SameSiteStrictMode ||
				got.Secure != c.wantSecure || got.Path != c.wantPath {
				t.Errorf("%s (persistent %v): Set-Cookie %q", c.name, persistent, raw)
			}
			wantMaxAge := 0
			if persistent {
				wantMaxAge = int((24 * time.Hour).Seconds())
			}
			if got.MaxAge != wantMaxAge || !got.Expires.IsZero() {
				t.Errorf("%s (persistent %v): Max-Age %d Expires %v, want Max-Age %d", c.name, persistent, got.MaxAge, got.Expires, wantMaxAge)
			}
		}

		got, raw := setCookie(t, a, a.clearSessionCookie)
		if got.Value != "" || got.MaxAge != -1 || got.Path != c.wantPath || got.Secure != c.wantSecure {
			t.Errorf("%s: clearing Set-Cookie %q", c.name, raw)
		}

		got, raw = setCookie(t, a, func(w http.ResponseWriter) { cookies{a.newLoginTicketCookie("ticket")}.set(w) })
		if got.Name != "vantigo.2fa" || got.Value != "ticket" || !got.HttpOnly || got.SameSite != http.SameSiteStrictMode ||
			got.Secure != c.wantSecure || got.Path != c.wantPath || got.MaxAge != 300 {
			t.Errorf("%s: login ticket Set-Cookie %q", c.name, raw)
		}
	}
}

// TestSessionTokenFrom proves only a non-empty vantigo.session cookie counts.
func TestSessionTokenFrom(t *testing.T) {
	req := func(cookies ...*http.Cookie) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}
	if tok, ok := sessionTokenFrom(req(&http.Cookie{Name: "vantigo.session", Value: "abc"})); !ok || tok != "abc" {
		t.Errorf("with the cookie: %q, %v", tok, ok)
	}
	for _, r := range []*http.Request{req(), req(&http.Cookie{Name: "vantigo.session", Value: ""}), req(&http.Cookie{Name: "other", Value: "abc"})} {
		if tok, ok := sessionTokenFrom(r); ok {
			t.Errorf("Cookie %q: got token %q", r.Header.Get("Cookie"), tok)
		}
	}
}
