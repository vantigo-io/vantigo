package health

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func probe(t *testing.T, h http.Handler, method, path string) (int, report, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	var r report
	if rec.Code != http.StatusMethodNotAllowed {
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatalf("decode %s: %v (%q)", path, err, rec.Body.String())
		}
	}
	return rec.Code, r, rec.Header()
}

var (
	ok     = Check{Name: "postgres", Run: func(context.Context) error { return nil }}
	broken = func(name string) Check {
		return Check{Name: name, Run: func(context.Context) error { return errors.New("down") }}
	}
)

func TestLive_DependsOnNothing(t *testing.T) {
	h := Handler(slog.New(slog.DiscardHandler), "1.2.3", broken("postgres"))
	code, r, hdr := probe(t, h, http.MethodGet, "/health/live")
	if code != http.StatusOK || r.Status != "healthy" || r.Version != "1.2.3" {
		t.Errorf("live = %d %+v", code, r)
	}
	if hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", hdr.Get("Cache-Control"))
	}
}

func TestReady_HealthyWhenEveryCheckPasses(t *testing.T) {
	code, r, _ := probe(t, Handler(slog.New(slog.DiscardHandler), "1.2.3", ok), http.MethodGet, "/health/ready")
	if code != http.StatusOK || r.Status != "healthy" || len(r.Failing) != 0 {
		t.Errorf("ready = %d %+v", code, r)
	}
}

func TestReady_ListsEveryFailingCheck(t *testing.T) {
	h := Handler(slog.New(slog.DiscardHandler), "1.2.3", broken("storage"), ok, broken("postgres"))
	code, r, _ := probe(t, h, http.MethodGet, "/health/ready")
	if code != http.StatusServiceUnavailable || r.Status != "unhealthy" {
		t.Errorf("ready = %d %+v", code, r)
	}
	if !slices.Equal(r.Failing, []string{"postgres", "storage"}) {
		t.Errorf("failing = %v, want [postgres storage]", r.Failing)
	}
}

func TestReady_AHangingCheckTimesOut(t *testing.T) {
	previous := checkTimeout
	checkTimeout = 50 * time.Millisecond
	defer func() { checkTimeout = previous }()

	hanging := Check{Name: "postgres", Run: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	start := time.Now()
	code, _, _ := probe(t, Handler(slog.New(slog.DiscardHandler), "1.2.3", hanging), http.MethodGet, "/health/ready")
	if code != http.StatusServiceUnavailable || time.Since(start) > 2*time.Second {
		t.Errorf("status %d after %v", code, time.Since(start))
	}
}

func TestHandler_OnlyAnswersGET(t *testing.T) {
	if code, _, _ := probe(t, Handler(slog.New(slog.DiscardHandler), "1.2.3"), http.MethodPost, "/health/live"); code != http.StatusMethodNotAllowed {
		t.Errorf("POST /health/live = %d, want 405", code)
	}
}

func TestProbe(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/ready" {
			t.Errorf("probed %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()

	if err := Probe(context.Background(), srv.URL); err != nil {
		t.Errorf("healthy server: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := Probe(context.Background(), srv.URL); err == nil {
		t.Error("503 reported as healthy")
	}
	srv.Close()
	if err := Probe(context.Background(), srv.URL); err == nil {
		t.Error("closed server reported as healthy")
	}
}
