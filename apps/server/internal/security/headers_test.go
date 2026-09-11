package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

const testHash = "'sha256-abc123='"

func serveWithHeaders(o HeaderOptions, req *http.Request) http.Header {
	rec := httptest.NewRecorder()
	httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		httpx.Forwarded(1, nil), Headers(o)).ServeHTTP(rec, req)
	return rec.Header()
}

func TestContentSecurityPolicy_MatchesTheDotNetHostPolicy(t *testing.T) {
	want := "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; " +
		"form-action 'self'; script-src 'self' 'sha256-abc123='; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self'; " +
		"frame-src 'self'; worker-src 'self'; manifest-src 'self'"
	if got := ContentSecurityPolicy(testHash); got != want {
		t.Errorf("policy =\n%s\nwant\n%s", got, want)
	}
}

func TestHeaders_SetsTheFullSetOnEveryResponse(t *testing.T) {
	h := serveWithHeaders(HeaderOptions{InlineScriptHash: testHash}, httptest.NewRequest(http.MethodGet, "http://vantigo.example.com/api/x", nil))

	want := map[string]string{
		"Content-Security-Policy": ContentSecurityPolicy(testHash),
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Permissions-Policy":      PermissionsPolicy,
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	if h.Get("Content-Security-Policy-Report-Only") != "" {
		t.Error("report-only header set while enforcing")
	}
}

func TestHeaders_ReportOnlyMovesThePolicyToTheReportOnlyHeader(t *testing.T) {
	h := serveWithHeaders(HeaderOptions{InlineScriptHash: testHash, ReportOnly: true}, httptest.NewRequest(http.MethodGet, "/", nil))

	if h.Get("Content-Security-Policy") != "" {
		t.Error("enforcing header set in report-only mode")
	}
	if h.Get("Content-Security-Policy-Report-Only") != ContentSecurityPolicy(testHash) {
		t.Errorf("report-only header = %q", h.Get("Content-Security-Policy-Report-Only"))
	}
}

func TestHeaders_HSTS(t *testing.T) {
	forwardedHTTPS := func(target string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
		return r
	}
	tests := []struct {
		name string
		hsts bool
		req  *http.Request
		want string
	}{
		{"https behind a trusted proxy", true, forwardedHTTPS("http://vantigo.example.com/"), "max-age=2592000"},
		{"https on the connection", true, httptest.NewRequest(http.MethodGet, "https://vantigo.example.com/", nil), "max-age=2592000"},
		{"plain http", true, httptest.NewRequest(http.MethodGet, "http://vantigo.example.com/", nil), ""},
		{"loopback host", true, forwardedHTTPS("http://localhost:8080/"), ""},
		{"disabled (development)", false, forwardedHTTPS("http://vantigo.example.com/"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := serveWithHeaders(HeaderOptions{InlineScriptHash: testHash, HSTS: tc.hsts}, tc.req)
			if got := h.Get("Strict-Transport-Security"); got != tc.want {
				t.Errorf("Strict-Transport-Security = %q, want %q", got, tc.want)
			}
		})
	}
}
