package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// setEnv sets the variables config reads and blanks every other one it knows,
// so the developer's own environment cannot leak into a test.
func setEnv(t *testing.T, pairs ...string) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "APP_URL", "APP_BASE_PATH", "PORT",
		"TRUSTED_PROXY_HOPS", "ALLOW_INSECURE_TRANSPORT", "CSP_REPORT_ONLY", "SHUTDOWN_TIMEOUT", "LOG_LEVEL", "PGSSLMODE"} {
		t.Setenv(k, "")
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		t.Setenv(pairs[i], pairs[i+1])
	}
}

func runCapture(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestParseArgs(t *testing.T) {
	for args, want := range map[string]mode{
		"":            modeUsage,
		"api":         modeAPI,
		"server":      modeServer,
		"worker":      modeWorker,
		"migrate":     modeMigrate,
		"seed":        modeSeed,
		"healthcheck": modeHealthcheck,
		"migrations":  modeUsage,
		"API":         modeUsage,
	} {
		var argv []string
		if args != "" {
			argv = []string{args}
		}
		if got, _ := parseArgs(argv); got != want {
			t.Errorf("parseArgs(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestRun_UsageErrorsExit2(t *testing.T) {
	code, _, stderr := runCapture()
	if code != 2 || !strings.Contains(stderr, usage) {
		t.Errorf("no command: exit %d stderr %q", code, stderr)
	}
	code, _, stderr = runCapture("bogus")
	if code != 2 || !strings.Contains(stderr, `unknown command "bogus"`) {
		t.Errorf("unknown command: exit %d stderr %q", code, stderr)
	}
}

func TestRun_InvalidConfigurationExits1WithEveryProblem(t *testing.T) {
	setEnv(t, "PORT", "0")
	code, _, stderr := runCapture("migrate")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	for _, want := range []string{"DATABASE_URL: is required", "APP_URL: is required", "PORT: must be an integer"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q: %s", want, stderr)
		}
	}
}

func TestRun_SeedRefusesOutsideDevelopment(t *testing.T) {
	setEnv(t, "DATABASE_URL", "postgres://v:v@127.0.0.1:1/v", "APP_URL", "http://localhost:8080", "ALLOW_INSECURE_TRANSPORT", "1")
	if code, _, stderr := runCapture("seed"); code != 2 || !strings.Contains(stderr, "APP_ENV=development") {
		t.Errorf("exit %d stderr %q", code, stderr)
	}
}

func TestRun_Healthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	// Deliberately no DATABASE_URL: the probe must not need configuration.
	setEnv(t, "PORT", u.Port())

	if code, _, _ := runCapture("healthcheck"); code != 0 {
		t.Errorf("healthy: exit %d", code)
	}
	status = http.StatusServiceUnavailable
	if code, _, stderr := runCapture("healthcheck"); code != 1 || stderr == "" {
		t.Errorf("unhealthy: exit %d stderr %q", code, stderr)
	}
}

func TestRun_MigrateAppliesTheSchema(t *testing.T) {
	databaseURL := testdb.URL(t)
	setEnv(t, "DATABASE_URL", databaseURL, "APP_URL", "http://localhost:8080", "ALLOW_INSECURE_TRANSPORT", "1")

	if code, stdout, stderr := runCapture("migrate"); code != 0 {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var present bool
	if err := conn.QueryRow(context.Background(), "SELECT to_regclass('platform.rate_limit') IS NOT NULL").Scan(&present); err != nil || !present {
		t.Errorf("platform.rate_limit present = %v (%v)", present, err)
	}
}

// startServe runs serve on a loopback listener and returns its base URL and a
// func that stops it and returns the exit code.
func startServe(t *testing.T, withAPI bool) (string, func() int) {
	t.Helper()
	_, databaseURL := testdb.Migrated(t)
	cfg, err := config.Load(map[string]string{
		"DATABASE_URL":             databaseURL,
		"APP_URL":                  "http://localhost:8080",
		"ALLOW_INSECURE_TRANSPORT": "1",
		"SHUTDOWN_TIMEOUT":         "5s",
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- serve(ctx, slog.New(slog.DiscardHandler), cfg, ln, withAPI) }()

	base := "http://" + ln.Addr().String()
	waitReady(t, base)
	return base, func() int {
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(10 * time.Second):
			t.Fatal("serve did not return after cancellation")
			return -1
		}
	}
}

func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/health/ready"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never became ready", base)
}

func body(t *testing.T, target string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestServe_ServesTheWholeStackAndDrainsOnCancel(t *testing.T) {
	base, stop := startServe(t, true)

	if code, html, hdr := body(t, base+"/"); code != http.StatusOK || !strings.Contains(html, "window.__VANTIGO_APP__") || hdr.Get("Content-Security-Policy") == "" {
		t.Errorf("SPA: %d, CSP %q, body %q", code, hdr.Get("Content-Security-Policy"), html)
	}
	if code, _, hdr := body(t, base+"/api/v1/nope"); code != http.StatusNotFound || hdr.Get("Content-Type") != "application/problem+json" {
		t.Errorf("API catch-all: %d %q", code, hdr.Get("Content-Type"))
	}
	if code, ready, _ := body(t, base+"/health/ready"); code != http.StatusOK || !strings.Contains(ready, `"version":"dev"`) {
		t.Errorf("ready: %d %q", code, ready)
	}
	if code := stop(); code != 0 {
		t.Errorf("exit %d after a clean shutdown", code)
	}
}

func TestServe_WorkerServesOnlyHealth(t *testing.T) {
	base, stop := startServe(t, false)
	defer stop()
	if code, _, _ := body(t, base+"/"); code != http.StatusNotFound {
		t.Errorf("worker served / with %d", code)
	}
}

func TestServeUntilDone_LetsInFlightRequestsFinish(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("finished"))
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- serveUntilDone(ctx, slog.New(slog.DiscardHandler), srv, ln, 5*time.Second) }()

	result := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		result <- string(b)
	}()

	<-entered
	cancel() // the shutdown signal arrives mid-request
	select {
	case code := <-done:
		t.Fatalf("returned %d before the in-flight request finished", code)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	if got := <-result; got != "finished" {
		t.Errorf("in-flight request got %q", got)
	}
	if code := <-done; code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}
