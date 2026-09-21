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

	"github.com/vantigo-io/vantigo/server/internal/management"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

var token = strings.Repeat("t", 32)

func handler(o management.Options) http.Handler {
	o.Logger = slog.New(slog.DiscardHandler)
	o.Version = "1.4.2"
	o.Token = token
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
