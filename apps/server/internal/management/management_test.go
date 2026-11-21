package management_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/management"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

var token = strings.Repeat("t", 32)

// handler builds a test handler with the shared test token, unless
// tokenOverride is given — which lets a test configure an empty token, the
// one value the default token constant can never stand in for.
func handler(o management.Options, tokenOverride ...string) http.Handler {
	o.Logger = slog.New(slog.DiscardHandler)
	o.Version = "1.4.2"
	o.Token = token
	if len(tokenOverride) > 0 {
		o.Token = tokenOverride[0]
	}
	if o.Installation == nil {
		o.Installation = func(context.Context) (management.Installation, error) {
			return management.Installation{Bootstrap: "invited", Users: 12, ActiveUsers: 9}, nil
		}
	}
	if o.DatabaseBytes == nil {
		o.DatabaseBytes = func(context.Context) (int64, error) { return 734003200, nil }
	}
	return management.Handler(o)
}

func get(t *testing.T, h http.Handler, path, authorization, host string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestStatus(t *testing.T) {
	// Any Host is accepted: the listener has no host filter, which is what
	// lets a control plane reach it by Service name or pod IP.
	res := get(t, handler(management.Options{}), "/management/status", "Bearer "+token, "10.1.2.3:9090")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	raw, _ := io.ReadAll(res.Body)
	var body struct {
		Version   string `json:"version"`
		Bootstrap string `json:"bootstrap"`
		Usage     struct {
			Users         int   `json:"users"`
			ActiveUsers   int   `json:"activeUsers"`
			DatabaseBytes int64 `json:"databaseBytes"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if body.Version != "1.4.2" || body.Bootstrap != "invited" || body.Usage.Users != 12 || body.Usage.ActiveUsers != 9 || body.Usage.DatabaseBytes != 734003200 {
		t.Errorf("body = %s", raw)
	}
}

// With Options.Live the status carries the live hub's state; without it the
// field is absent.
func TestStatus_CarriesTheLiveHubsState(t *testing.T) {
	h := handler(management.Options{Live: func(context.Context) management.Live {
		return management.Live{Listener: "up", QueueUsage: 0.25, Connections: 3}
	}})
	raw, _ := io.ReadAll(get(t, h, "/management/status", "Bearer "+token, "").Body)
	if !strings.Contains(string(raw), `"live":{"listener":"up","queueUsage":0.25,"connections":3}`) {
		t.Errorf("body = %s", raw)
	}
	raw, _ = io.ReadAll(get(t, handler(management.Options{}), "/management/status", "Bearer "+token, "").Body)
	if strings.Contains(string(raw), `"live"`) {
		t.Errorf("no hub: body = %s", raw)
	}
}

func TestStatus_RequiresTheBearerToken(t *testing.T) {
	h := handler(management.Options{})
	for name, authorization := range map[string]string{
		"missing":      "",
		"wrong token":  "Bearer " + strings.Repeat("x", 32),
		"wrong scheme": "Basic " + token,
		"bare token":   token,
	} {
		res := get(t, h, "/management/status", authorization, "")
		if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: status %d, WWW-Authenticate %q", name, res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
	}
}

// TestStatus_TheBearerSchemeIsCaseInsensitive mirrors identity/scim.go's
// scimBearer: RFC 7235 makes the auth scheme case-insensitive, so "bearer"
// and "BEARER" must authenticate exactly as "Bearer" does, while a scheme
// with no credential, the wrong scheme and a bare token (no scheme at all)
// still get 401.
func TestStatus_TheBearerSchemeIsCaseInsensitive(t *testing.T) {
	h := handler(management.Options{})
	for name, tc := range map[string]struct {
		authorization string
		wantStatus    int
	}{
		"lower case scheme": {"bearer " + token, http.StatusOK},
		"upper case scheme": {"BEARER " + token, http.StatusOK},
		"no credential":     {"Bearer", http.StatusUnauthorized},
		"wrong scheme":      {"Basic " + token, http.StatusUnauthorized},
		"bare token":        {token, http.StatusUnauthorized},
	} {
		res := get(t, h, "/management/status", tc.authorization, "")
		if res.StatusCode != tc.wantStatus {
			t.Errorf("%s: status %d, want %d", name, res.StatusCode, tc.wantStatus)
		}
	}
}

func TestStatus_AnEmptyConfiguredTokenRejectsEverything(t *testing.T) {
	// MANAGEMENT_PORT without MANAGEMENT_TOKEN configures exactly this
	// (KR3): the probes answer, and every protected route must refuse. This
	// listener has no host filter and the token is its only defence, so
	// bearer fails closed on its own. In particular, "Bearer " (the prefix with an empty credential)
	// must never authenticate just because both sides hash to the same
	// digest of "".
	h := handler(management.Options{}, "")
	for name, authorization := range map[string]string{
		"missing":          "",
		"bearer empty":     "Bearer ",
		"bearer non-empty": "Bearer x",
	} {
		res := get(t, h, "/management/status", authorization, "")
		if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: status %d, WWW-Authenticate %q", name, res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
	}
}

func TestStatus_AnUnknownPathStillRequiresTheToken(t *testing.T) {
	// bearer wraps the mux, so an unauthenticated or wrongly authenticated
	// caller gets 401 for a path that does not exist too: it must not learn
	// which routes this listener serves.
	h := handler(management.Options{})
	for _, path := range []string{"/management/other", "/api/v1/identity/session"} {
		for name, authorization := range map[string]string{"missing": "", "wrong": "Bearer " + strings.Repeat("x", 32)} {
			res := get(t, h, path, authorization, "")
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s: status %d, want 401", name, path, res.StatusCode)
			}
		}
	}
}

func TestStatus_OnlyThatOneRoute(t *testing.T) {
	h := handler(management.Options{})
	if res := get(t, h, "/management/other", "Bearer "+token, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path: %d", res.StatusCode)
	}
	if res := get(t, h, "/api/v1/identity/session", "Bearer "+token, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("the API must not be reachable on this listener: %d", res.StatusCode)
	}
	req := httptest.NewRequest(http.MethodPost, "/management/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

// TestProbes_AnswerWithoutTheToken pins KR1 and KR3: the two probes are the
// public health handler's, mounted outside the bearer check, with and
// without a configured token, while every other route still needs it.
func TestProbes_AnswerWithoutTheToken(t *testing.T) {
	failing := errors.New("down")
	probes := health.Handler(slog.New(slog.DiscardHandler), "1.4.2",
		health.Check{Name: "postgres", Run: func(context.Context) error { return nil }},
		health.Check{Name: "schema", Run: func(context.Context) error { return failing }},
	)
	for name, configured := range map[string]string{"with a token": token, "without a token": ""} {
		h := handler(management.Options{Health: probes}, configured)
		if res := get(t, h, "/health/live", "", "10.1.2.3:9090"); res.StatusCode != http.StatusOK {
			t.Errorf("%s: live answered %d, want 200", name, res.StatusCode)
		}
		res := get(t, h, "/health/ready", "", "")
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(raw), `"failing":["schema"]`) {
			t.Errorf("%s: ready answered %d %s, want 503 naming schema", name, res.StatusCode, raw)
		}
		if res := get(t, h, "/management/status", "", ""); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status without a token answered %d, want 401", name, res.StatusCode)
		}
		if res := get(t, h, "/health/other", "", ""); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: another /health path answered %d, want 401", name, res.StatusCode)
		}
	}
	// Without Options.Health the probes are protected routes that do not exist.
	if res := get(t, handler(management.Options{}), "/health/live", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no Health: live answered %d, want 401", res.StatusCode)
	}
}

func TestStatus_ASourceFailureIs503WithoutDetail(t *testing.T) {
	h := handler(management.Options{Installation: func(context.Context) (management.Installation, error) {
		return management.Installation{}, errors.New("secret-database-detail")
	}})
	res := get(t, h, "/management/status", "Bearer "+token, "")
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusServiceUnavailable || strings.Contains(string(raw), "secret-database-detail") {
		t.Errorf("status %d body %s", res.StatusCode, raw)
	}
}

func TestDatabaseBytes(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	n, err := management.DatabaseBytes(pool)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n <= 0 {
		t.Errorf("database size = %d, want a positive number", n)
	}
}
