package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

const indexHTML = `<!doctype html><html><head><title>Vantigo</title>` +
	`<script type="module" src="/assets/app-1.js"></script></head><body><div id="root"></div></body></html>`

type fixture struct {
	handler http.Handler
	index   *web.Index
	logs    *bytes.Buffer
}

// TestNew_DoesNotCleanAPIPaths proves the /api subtree bypasses
// http.ServeMux's path cleaning. Registering /api/ on the mux made it
// answer "/api/v1/customers//contacts" with a 307 to the cleaned
// "/api/v1/customers/contacts", so the request never reached the API at all
// — where .NET answered 404 and where the module router's own matcher
// deliberately refuses the empty segment a doubled slash produces. Every
// API path is declared exactly by a contract, so nothing under /api wants
// cleaning; health and the SPA keep the mux's, which they do want.
//
// The API handler is replaced with one that records the path it was given,
// because the fixture's default API is itself an http.ServeMux and would
// clean the path a second time, hiding the behaviour under test.
func TestNew_DoesNotCleanAPIPaths(t *testing.T) {
	var seen []string
	f := newFixture(t, func(_ *config.Config, o *Options) {
		o.API = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r.URL.EscapedPath())
			httpx.NotFound(w, r)
		})
	})

	paths := []string{
		"/api/v1//ping",
		"/api/v1/customers//contacts",
		"/api//v1/ping",
		"/api/v1/customers//",
		"/api/v1/customers/7/timeline/",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, "https://vantigo.example.com"+path, nil)
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)

		if rec.Code == http.StatusTemporaryRedirect || rec.Code == http.StatusMovedPermanently {
			t.Errorf("%s: status = %d with Location %q, want the API handler's own answer and no redirect",
				path, rec.Code, rec.Header().Get("Location"))
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want the API handler's 404 problem", path, rec.Code)
		}
	}
	if len(seen) != len(paths) {
		t.Fatalf("the API handler saw %d of %d requests (%q): every /api path must reach it", len(seen), len(paths), seen)
	}
	for i, path := range paths {
		if seen[i] != path {
			t.Errorf("the API handler saw %q, want %q unchanged", seen[i], path)
		}
	}
}

func newFixture(t *testing.T, mutate func(*config.Config, *Options)) fixture {
	t.Helper()
	cfg := &config.Config{
		Env:             config.Production,
		AppOrigin:       "https://vantigo.example.com",
		AppHostname:     "vantigo.example.com",
		Port:            8080,
		ShutdownTimeout: time.Second,
		LogLevel:        slog.LevelInfo,
	}
	assets := fstest.MapFS{
		"index.html":      {Data: []byte(indexHTML)},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}

	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("pong:" + r.URL.Path)) })
	api.HandleFunc("POST /api/v1/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	api.HandleFunc("GET /api/v1/panic", func(http.ResponseWriter, *http.Request) { panic("boom: secret detail") })
	api.HandleFunc("/", httpx.NotFound)

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	o := Options{
		Config: cfg,
		Logger: logger,
		Assets: assets,
		Health: health.Handler(logger, "test", health.Check{Name: "postgres", Run: func(context.Context) error { return nil }}),
		API:    api,
	}
	if mutate != nil {
		mutate(cfg, &o)
	}
	idx, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding)
	if err != nil {
		t.Fatal(err)
	}
	o.Index = idx
	return fixture{handler: New(o), index: idx, logs: &logs}
}

func (f fixture) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func request(method, target string, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return req
}

func TestNew_ServesTheSPAWithTheSecurityHeaders(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(request(http.MethodGet, "https://vantigo.example.com/customers/42"))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "window.__VANTIGO_APP__") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' "+f.index.InlineScriptHash) {
		t.Errorf("CSP does not allow the index script: %q", csp)
	}
	for name, want := range map[string]string{
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Strict-Transport-Security": "max-age=2592000",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestNew_HealthRoutesToTheHealthHandler(t *testing.T) {
	rec := newFixture(t, nil).do(request(http.MethodGet, "https://vantigo.example.com/health/ready"))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("status %d Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestNew_UnknownAPIPathsAreProblemsNeverTheSPA(t *testing.T) {
	f := newFixture(t, func(_ *config.Config, o *Options) { o.API = nil })
	for _, path := range []string{"/api", "/api/", "/api/v1/customers"} {
		rec := f.do(request(http.MethodGet, "https://vantigo.example.com"+path))
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: status %d Content-Type %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestNew_RoutesAPIRequestsWithTheirFullPath(t *testing.T) {
	rec := newFixture(t, nil).do(request(http.MethodGet, "https://vantigo.example.com/api/v1/ping"))
	if rec.Body.String() != "pong:/api/v1/ping" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestNew_RejectsForeignHostsButNotLoopback(t *testing.T) {
	f := newFixture(t, nil)
	if rec := f.do(request(http.MethodGet, "https://evil.example/")); rec.Code != http.StatusBadRequest {
		t.Errorf("foreign host: %d, want 400", rec.Code)
	}
	if rec := f.do(request(http.MethodGet, "http://127.0.0.1:8080/health/ready")); rec.Code != http.StatusOK {
		t.Errorf("loopback probe: %d, want 200", rec.Code)
	}
}

func TestNew_CrossOriginProtection(t *testing.T) {
	f := newFixture(t, nil)
	tests := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"cross-site browser POST", request(http.MethodPost, "https://vantigo.example.com/api/v1/ping", "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"), http.StatusForbidden},
		{"same-origin browser POST", request(http.MethodPost, "https://vantigo.example.com/api/v1/ping", "Origin", "https://vantigo.example.com", "Sec-Fetch-Site", "same-origin"), http.StatusNoContent},
		{"non-browser client without either header", request(http.MethodPost, "https://vantigo.example.com/api/v1/ping"), http.StatusNoContent},
		{"APP_URL origin through a proxy that rewrote Host", request(http.MethodPost, "http://localhost:8080/api/v1/ping", "Origin", "https://vantigo.example.com"), http.StatusNoContent},
		{"cross-site GET is safe", request(http.MethodGet, "https://vantigo.example.com/api/v1/ping", "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"), http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(tc.req)
			if rec.Code != tc.want {
				t.Errorf("status %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusForbidden && rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Errorf("rejection is not a problem document")
			}
		})
	}
}

func TestNew_BasePathMountsTheApplication(t *testing.T) {
	f := newFixture(t, func(cfg *config.Config, _ *Options) { cfg.BasePath = "/vantigo" })

	if rec := f.do(request(http.MethodGet, "https://vantigo.example.com/vantigo/")); !strings.Contains(rec.Body.String(), `src="/vantigo/assets/app-1.js"`) {
		t.Errorf("index under the base path: %d %q", rec.Code, rec.Body.String())
	}
	if rec := f.do(request(http.MethodGet, "https://vantigo.example.com/vantigo/api/v1/ping")); rec.Body.String() != "pong:/api/v1/ping" {
		t.Errorf("API under the base path: %q", rec.Body.String())
	}
	if rec := f.do(request(http.MethodGet, "http://127.0.0.1:8080/health/ready")); rec.Code != http.StatusOK {
		t.Errorf("unprefixed probe: %d", rec.Code)
	}
}

func TestNew_HSTS(t *testing.T) {
	dev := newFixture(t, func(cfg *config.Config, _ *Options) { cfg.Env = config.Development })
	if got := dev.do(request(http.MethodGet, "https://vantigo.example.com/")).Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("development sent HSTS %q", got)
	}

	proxied := newFixture(t, func(cfg *config.Config, _ *Options) { cfg.TrustedProxyHops = 1 })
	req := request(http.MethodGet, "http://vantigo.example.com/", "X-Forwarded-Proto", "https", "X-Forwarded-For", "203.0.113.7")
	if got := proxied.do(req).Header().Get("Strict-Transport-Security"); got != "max-age=2592000" {
		t.Errorf("https behind a trusted proxy: HSTS %q", got)
	}
}

func TestNew_WarnsWhenTrustedProxyHopsHasNoCIDRs(t *testing.T) {
	unguarded := newFixture(t, func(cfg *config.Config, _ *Options) { cfg.TrustedProxyHops = 1 })
	if !strings.Contains(unguarded.logs.String(), "TRUSTED_PROXY_HOPS is set without TRUSTED_PROXY_CIDRS") {
		t.Errorf("missing warning: %s", unguarded.logs.String())
	}

	guarded := newFixture(t, func(cfg *config.Config, _ *Options) {
		cfg.TrustedProxyHops = 1
		cfg.TrustedProxyCIDRs = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	})
	if strings.Contains(guarded.logs.String(), "TRUSTED_PROXY_HOPS is set without") {
		t.Errorf("unexpected warning with CIDRs configured: %s", guarded.logs.String())
	}

	noHops := newFixture(t, nil)
	if strings.Contains(noHops.logs.String(), "TRUSTED_PROXY_HOPS is set without") {
		t.Errorf("unexpected warning with hops at their default of 0: %s", noHops.logs.String())
	}
}

func TestNew_APIPanicIsASanitisedProblemCorrelatedWithTheLog(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(request(http.MethodGet, "https://vantigo.example.com/api/v1/panic"))

	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v (%q)", err, rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError || p.Detail != httpx.UnexpectedErrorDetail {
		t.Errorf("status %d problem %+v", rec.Code, p)
	}
	if strings.Contains(rec.Body.String(), "secret detail") {
		t.Error("response leaks the panic value")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("the 500 response lost the security headers")
	}
	if !strings.Contains(f.logs.String(), "secret detail") || !strings.Contains(f.logs.String(), p.TraceID) {
		t.Errorf("log does not carry the panic and trace id %s: %s", p.TraceID, f.logs.String())
	}
}
