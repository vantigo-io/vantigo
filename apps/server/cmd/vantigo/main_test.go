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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// testAppSecret is a fixture value only: 32+ bytes, never a real secret.
const testAppSecret = "main-test-app-secret-32-bytes!!!"

// setEnv sets the variables config reads and blanks every other one it knows,
// so the developer's own environment cannot leak into a test. It also seeds
// the identity settings that are required whenever APP_ENV defaults to
// production (the zero value of the blanked APP_ENV), so a caller only
// overrides what its case is actually about.
func setEnv(t *testing.T, pairs ...string) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "APP_URL", "APP_BASE_PATH", "PORT",
		"TRUSTED_PROXY_HOPS", "TRUSTED_PROXY_CIDRS", "ALLOW_INSECURE_TRANSPORT", "CSP_REPORT_ONLY", "SHUTDOWN_TIMEOUT", "LOG_LEVEL", "PGSSLMODE",
		"APP_SECRET", "BOOTSTRAP_SECRET", "MAIL_DRIVER", "SMTP_HOST", "SMTP_FROM"} {
		t.Setenv(k, "")
	}
	t.Setenv("APP_SECRET", testAppSecret)
	t.Setenv("BOOTSTRAP_SECRET", "main-test-bootstrap-secret")
	t.Setenv("SMTP_HOST", "smtp.example.invalid")
	t.Setenv("SMTP_FROM", "noreply@example.invalid")
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

// TestRun_SIGTERMDuringMigrationWaitsForItToFinish proves the fix for the
// review finding: the signal handler must be installed before migrate()
// runs, or a SIGTERM arriving mid-migration kills the process outright
// (Go's default disposition for an unhandled SIGTERM) instead of waiting
// for the migration to finish before exiting cleanly.
func TestRun_SIGTERMDuringMigrationWaitsForItToFinish(t *testing.T) {
	databaseURL := testdb.URL(t)

	freeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := freeLn.Addr().(*net.TCPAddr).Port
	if err := freeLn.Close(); err != nil {
		t.Fatal(err)
	}

	setEnv(t, "DATABASE_URL", databaseURL, "APP_URL", "http://localhost:8080",
		"ALLOW_INSECURE_TRANSPORT", "1", "PORT", strconv.Itoa(port))

	ctx := context.Background()
	holder, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", db.MigrationLockKey); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run([]string{"api"}, &stdout, &stderr) }()

	// pg_stat_activity is the ground truth for "the migrator is waiting on
	// the lock", not a sleep — same technique as internal/db's
	// TestApplyMigrations_WaitsForTheAdvisoryLock.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		err := holder.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the migrator never waited on the advisory lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// If the signal handler were not installed yet, this kills the test
	// binary outright instead of being observed by run().
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-done:
		t.Fatalf("run returned %d before the migration finished", code)
	case <-time.After(200 * time.Millisecond):
	}

	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", db.MigrationLockKey); err != nil {
		t.Fatal(err)
	}

	var code int
	select {
	case code = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the migration finished")
	}
	if code != 0 {
		t.Errorf("exit %d, want 0\nstdout %s\nstderr %s", code, stdout.String(), stderr.String())
	}

	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var present bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('platform.rate_limit') IS NOT NULL").Scan(&present); err != nil || !present {
		t.Errorf("platform.rate_limit present = %v (%v)", present, err)
	}
	if !strings.Contains(stdout.String(), "shutdown requested during migration") {
		t.Errorf("stdout is missing the shutdown message: %s", stdout.String())
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
		"SHUTDOWN_TIMEOUT":         "10s",
		"APP_SECRET":               testAppSecret,
		"BOOTSTRAP_SECRET":         "main-test-bootstrap-secret",
		"SMTP_HOST":                "smtp.example.invalid",
		"SMTP_FROM":                "noreply@example.invalid",
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
		// http.DefaultClient's transport can leave a spare connection dialed
		// but idle in its pool without ever sending a request; the server
		// sees that as StateNew, which Server.Shutdown waits out rather than
		// closing immediately (only StateIdle connections are closed right
		// away), so an unlucky dial can eat into the drain budget for no
		// reason. Close it before cancelling so Shutdown only ever waits on
		// connections the test actually used.
		http.DefaultClient.CloseIdleConnections()
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

// bodyNoRedirect is body() with redirects surfaced instead of followed, so a
// test can tell a 307 from whatever the redirect target would have answered.
func bodyNoRedirect(t *testing.T, target string) (int, string, http.Header) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// TestServe_ServesTheBusinessModules proves api mode composes the three
// business modules MODULES enables by default, not identity alone: each
// module's own listing answers its contract's 401 without a session, rather
// than the platform's /api 404 catch-all (which is what a module that was
// never passed to Compose would answer).
func TestServe_ServesTheBusinessModules(t *testing.T) {
	base, stop := startServe(t, true)
	defer stop()

	for _, path := range []string{"/api/v1/customers", "/api/v1/products", "/api/v1/energy/metering-points"} {
		code, payload, _ := body(t, base+path)
		if code != http.StatusUnauthorized || !strings.Contains(payload, "unauthenticated") {
			t.Errorf("%s: %d %q, want 401 unauthenticated", path, code, payload)
		}
	}
}

// TestServe_DoubledAndTrailingSlashesAnswerTheNotFoundProblem is the
// whole-stack confirmation for the path-cleaning suppression in
// internal/server and internal/module: through the real server.New and the
// real customers contract, a doubled or trailing slash answers the 404
// problem rather than a 307 redirect (server.New's mux cleaned these before
// the API handler ever saw them) or a 204 (which is what binding the empty
// segment to a {param} would have produced).
func TestServe_DoubledAndTrailingSlashesAnswerTheNotFoundProblem(t *testing.T) {
	base, stop := startServe(t, true)
	defer stop()

	for _, path := range []string{
		"/api/v1/customers/",
		"/api/v1/customers//timeline",
		"/api/v1/customers//",
		"/api/v1/customers/7/timeline/",
	} {
		code, _, hdr := bodyNoRedirect(t, base+path)
		if code != http.StatusNotFound {
			t.Errorf("%s: status = %d (Location %q), want 404 and never a 307 or a 204",
				path, code, hdr.Get("Location"))
			continue
		}
		if ct := hdr.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("%s: Content-Type = %q, want application/problem+json", path, ct)
		}
	}
}

// TestServe_ServesTheIdentityModule proves api mode wires module.Compose's
// handler into server.Options.API: the identity module answers its own
// contract, not the platform's 404 catch-all.
func TestServe_ServesTheIdentityModule(t *testing.T) {
	base, stop := startServe(t, true)
	defer stop()

	code, status, _ := body(t, base+"/api/v1/identity/system/status")
	if code != http.StatusOK || !strings.Contains(status, `"maintenance":false`) {
		t.Errorf("system/status: %d %q", code, status)
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
