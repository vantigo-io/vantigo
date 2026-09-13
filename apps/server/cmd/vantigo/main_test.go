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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	"github.com/vantigo-io/vantigo/server/internal/worker"
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

// TestRunWorkers pins the exact rule design §3.2 states: workers run
// always in worker mode, in api mode only when WORKERS_IN_PROCESS=1, and
// never in server mode regardless of that setting.
func TestRunWorkers(t *testing.T) {
	for _, tc := range []struct {
		m                mode
		workersInProcess bool
		want             bool
	}{
		{modeWorker, false, true},
		{modeWorker, true, true},
		{modeAPI, true, true},
		{modeAPI, false, false},
		{modeServer, true, false},
		{modeServer, false, false},
	} {
		cfg := &config.Config{WorkersInProcess: tc.workersInProcess}
		if got := runWorkers(tc.m, cfg); got != tc.want {
			t.Errorf("runWorkers(%v, WorkersInProcess=%v) = %v, want %v", tc.m, tc.workersInProcess, got, tc.want)
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
func startServe(t *testing.T, m mode) (string, func() int) {
	t.Helper()
	return startServeEnv(t, m, nil)
}

// startServeEnv is startServe with env overrides applied on top of the usual
// fixture (WORKERS_IN_PROCESS among them) and extraModules merged into the
// module list serve resolves workers from (see serve's own doc) —
// production always passes none; a test passes a module declaring a worker
// (fakeWorkerModule) to drive it through that real resolution.
func startServeEnv(t *testing.T, m mode, env map[string]string, extraModules ...module.Module) (string, func() int) {
	t.Helper()
	_, databaseURL := testdb.Migrated(t)
	envMap := map[string]string{
		"DATABASE_URL":             databaseURL,
		"APP_URL":                  "http://localhost:8080",
		"ALLOW_INSECURE_TRANSPORT": "1",
		"SHUTDOWN_TIMEOUT":         "10s",
		"APP_SECRET":               testAppSecret,
		"BOOTSTRAP_SECRET":         "main-test-bootstrap-secret",
		"SMTP_HOST":                "smtp.example.invalid",
		"SMTP_FROM":                "noreply@example.invalid",
	}
	for k, v := range env {
		envMap[k] = v
	}
	cfg, err := config.Load(envMap)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- serve(ctx, slog.New(slog.DiscardHandler), cfg, ln, m, extraModules...) }()

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

// recordingWorker is a minimal worker.Worker: it signals started once, via
// close, the first time Run is entered, records how many times Run was
// invoked, and blocks on ctx.Done() before returning — so a test can prove
// both that serve actually started it and that cancellation actually
// stopped it, without sleeping to simulate its poll interval.
type recordingWorker struct {
	name    string
	started chan struct{}
	calls   atomic.Int32
	stopped atomic.Bool
}

func newRecordingWorker(name string) *recordingWorker {
	return &recordingWorker{name: name, started: make(chan struct{})}
}

func (w *recordingWorker) Name() string            { return w.name }
func (w *recordingWorker) Interval() time.Duration { return time.Millisecond }
func (w *recordingWorker) Run(ctx context.Context) error {
	w.calls.Add(1)
	close(w.started)
	<-ctx.Done()
	w.stopped.Store(true)
	return nil
}

// waitStarted blocks until w.started fires, failing the test if it does not
// within a generous guard — a hang-safety net, not a simulated poll delay.
func waitStarted(t *testing.T, w *recordingWorker) {
	t.Helper()
	select {
	case <-w.started:
	case <-time.After(5 * time.Second):
		t.Fatalf("worker %q never started", w.name)
	}
}

// fakeWorkerModule wraps w as a module.Module contributing exactly it, so a
// test drives it through the real module.Workers resolution serve itself
// calls (mods := append(businessModules(access), extraModules...); workers
// := module.Workers(deps, mods...)) rather than around it. It is named
// "identity" so module.Workers' own enablement (enabledModules) always
// includes it regardless of MODULES — the same special case the real
// identity module gets — without this test module needing to be a real,
// contract-bearing module Compose could mount too. It is passed only to
// startServeEnv's extraModules, which serve never hands to Compose, so the
// duplicate "identity" name never reaches Compose's own duplicate-name
// check.
func fakeWorkerModule(w worker.Worker) module.Module {
	return module.Module{
		Name:    "identity",
		Workers: func(module.Deps) []worker.Worker { return []worker.Worker{w} },
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
	base, stop := startServe(t, modeAPI)

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
	base, stop := startServe(t, modeAPI)
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
	base, stop := startServe(t, modeAPI)
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
	base, stop := startServe(t, modeAPI)
	defer stop()

	code, status, _ := body(t, base+"/api/v1/identity/system/status")
	if code != http.StatusOK || !strings.Contains(status, `"maintenance":false`) {
		t.Errorf("system/status: %d %q", code, status)
	}
}

func TestServe_WorkerServesOnlyHealth(t *testing.T) {
	base, stop := startServe(t, modeWorker)
	defer stop()
	if code, _, _ := body(t, base+"/"); code != http.StatusNotFound {
		t.Errorf("worker served / with %d", code)
	}
}

// TestServe_WorkerModeRunsAndStopsWorkers is the end-to-end bite for worker
// mode: serve really starts what module.Workers resolves from the real
// module list (extraModules included), and really waits for it to stop on
// shutdown. No real module implements Worker yet (Tasks 11-13), so this
// registers a fake through a test module (fakeWorkerModule) instead —
// proving the production resolution at serve's own module.Workers call,
// not a bypass of it.
func TestServe_WorkerModeRunsAndStopsWorkers(t *testing.T) {
	w := newRecordingWorker("fake")
	_, stop := startServeEnv(t, modeWorker, nil, fakeWorkerModule(w))
	waitStarted(t, w)

	if code := stop(); code != 0 {
		t.Errorf("exit %d after a clean shutdown", code)
	}
	if !w.stopped.Load() {
		t.Error("stop() returned before the worker observed cancellation")
	}
	if w.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (the runner must not restart a worker)", w.calls.Load())
	}
}

// TestServe_APIModeRunsWorkersOnlyWhenConfigured is the end-to-end bite for
// "api mode runs them when WORKERS_IN_PROCESS=1 and not otherwise": the
// same fake worker actually starts under =1 and never starts under =0.
func TestServe_APIModeRunsWorkersOnlyWhenConfigured(t *testing.T) {
	t.Run("WORKERS_IN_PROCESS=1", func(t *testing.T) {
		w := newRecordingWorker("fake")
		_, stop := startServeEnv(t, modeAPI, map[string]string{"WORKERS_IN_PROCESS": "1"}, fakeWorkerModule(w))
		waitStarted(t, w)
		if code := stop(); code != 0 {
			t.Errorf("exit %d after a clean shutdown", code)
		}
		if !w.stopped.Load() {
			t.Error("stop() returned before the worker observed cancellation")
		}
	})

	t.Run("WORKERS_IN_PROCESS=0", func(t *testing.T) {
		w := newRecordingWorker("fake")
		_, stop := startServeEnv(t, modeAPI, map[string]string{"WORKERS_IN_PROCESS": "0"}, fakeWorkerModule(w))
		// waitReady inside startServeEnv already blocked on a real HTTP round
		// trip through the loopback listener, which gives any goroutine the
		// runner would have started far longer to run than it needs; this
		// guard only backstops that instead of standing in for it.
		select {
		case <-w.started:
			t.Fatal("api mode started a worker with WORKERS_IN_PROCESS=0")
		case <-time.After(200 * time.Millisecond):
		}
		if code := stop(); code != 0 {
			t.Errorf("exit %d after a clean shutdown", code)
		}
	})
}

// TestServe_ServerModeNeverRunsWorkersEvenWhenInjected is the strongest
// version of "server mode never runs workers": it registers a worker
// through a real, always-enabled module (fakeWorkerModule), so the only
// thing that can still stop it from running is serve's own mode gate — not
// module enablement, and not this test module having been left out of the
// list.
func TestServe_ServerModeNeverRunsWorkersEvenWhenInjected(t *testing.T) {
	w := newRecordingWorker("fake")
	_, stop := startServeEnv(t, modeServer, nil, fakeWorkerModule(w))
	select {
	case <-w.started:
		t.Fatal("server mode ran an injected worker")
	case <-time.After(200 * time.Millisecond):
	}
	if code := stop(); code != 0 {
		t.Errorf("exit %d after a clean shutdown", code)
	}
}

// stuckDBWorker holds a real pool connection open across shutdown by
// deliberately not respecting ctx (Acquire uses context.Background(), and
// Run does not return until the test releases it), so
// TestServe_WorkerOutlivingTheShutdownTimeoutDoesNotHangTheProcess
// reproduces the exact scenario review fix round 1 flagged: a worker still
// mid-query (here, still holding a checked-out connection) when shutdown's
// deferred pool.Close would otherwise run.
type stuckDBWorker struct {
	pool    *pgxpool.Pool
	started chan struct{}
	release <-chan struct{}
}

func (w *stuckDBWorker) Name() string            { return "stuck-db" }
func (w *stuckDBWorker) Interval() time.Duration { return time.Millisecond }
func (w *stuckDBWorker) Run(context.Context) error {
	conn, err := w.pool.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer conn.Release()
	close(w.started)
	<-w.release // held open until the test says otherwise, ignoring ctx entirely
	return nil
}

// TestServe_WorkerOutlivingTheShutdownTimeoutDoesNotHangTheProcess is the
// end-to-end bite for review fix round 1, item 2: before the fix,
// serve's deferred pool.Close blocked forever on stuckDBWorker's held
// connection, and since serve does not return until its deferred calls do,
// the whole process would hang past SHUTDOWN_TIMEOUT rather than exiting.
// stop()'s own 10s guard would catch that as a hang; this test additionally
// checks the elapsed time is nowhere near it, so a regression fails fast
// instead of only at that outer guard.
func TestServe_WorkerOutlivingTheShutdownTimeoutDoesNotHangTheProcess(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) }) // let the goroutine finish so it does not outlive the test

	started := make(chan struct{})
	mod := module.Module{
		Name: "identity",
		Workers: func(d module.Deps) []worker.Worker {
			return []worker.Worker{&stuckDBWorker{pool: d.Pool, started: started, release: release}}
		},
	}

	_, stop := startServeEnv(t, modeWorker, map[string]string{"SHUTDOWN_TIMEOUT": "200ms"}, mod)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("stuckDBWorker never started")
	}

	start := time.Now()
	code := stop()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("stop() took %s, want well under the 10s hang guard (SHUTDOWN_TIMEOUT was 200ms)", elapsed)
	}
	if code != 1 {
		t.Errorf("exit %d, want 1: a worker outlived the shutdown timeout", code)
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
	go func() {
		done <- serveUntilDone(ctx, slog.New(slog.DiscardHandler), srv, ln, 5*time.Second, nil, func() {})
	}()

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

// TestServeUntilDone_WaitsForWorkersConcurrentlyWithHTTPDrain proves
// shutdown draining HTTP and waiting for workers run at the same time,
// bounded by one shared timeout, rather than the worker wait only starting
// once HTTP has finished draining (which would let the two each eat into
// what should be the other's share of the budget).
func TestServeUntilDone_WaitsForWorkersConcurrentlyWithHTTPDrain(t *testing.T) {
	srv := &http.Server{Handler: http.NotFoundHandler()}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	w := newRecordingWorker("fake")
	runner := worker.NewRunner(slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	runner.Start(ctx, []worker.Worker{w})
	waitStarted(t, w)

	done := make(chan int, 1)
	// cancelWorkers is a no-op here: this worker was started with ctx
	// itself (as serve's graceful path derives workerCtx from ctx too), so
	// cancel() below already stops it without a separate call.
	go func() {
		done <- serveUntilDone(ctx, slog.New(slog.DiscardHandler), srv, ln, 5*time.Second, runner, func() {})
	}()
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveUntilDone did not return")
	}
	if !w.stopped.Load() {
		t.Error("serveUntilDone returned 0 before the worker actually stopped")
	}
}

// blockingWorker ignores ctx entirely until unblock fires — a deliberately
// buggy worker, standing in for one that hangs — so
// TestServeUntilDone_WorkerExceedingTheTimeoutFailsShutdown can prove
// serveUntilDone still returns once its own timeout elapses instead of
// hanging on it forever.
type blockingWorker struct {
	name    string
	unblock <-chan struct{}
}

func (w *blockingWorker) Name() string            { return w.name }
func (w *blockingWorker) Interval() time.Duration { return time.Millisecond }
func (w *blockingWorker) Run(context.Context) error {
	<-w.unblock
	return nil
}

func TestServeUntilDone_WorkerExceedingTheTimeoutFailsShutdown(t *testing.T) {
	srv := &http.Server{Handler: http.NotFoundHandler()}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) }) // let the goroutine finish so it does not outlive the test

	runner := worker.NewRunner(slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx, []worker.Worker{&blockingWorker{name: "stuck", unblock: unblock}})

	done := make(chan int, 1)
	go func() {
		done <- serveUntilDone(ctx, slog.New(slog.DiscardHandler), srv, ln, 50*time.Millisecond, runner, func() {})
	}()
	cancel()

	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("exit %d, want 1 when a worker outlives the shutdown timeout", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveUntilDone did not return")
	}
}

// TestServeUntilDone_EarlyServeFailureCancelsAndWaitsForWorkers is the bite
// for review fix round 1, item 3: when srv.Serve fails on its own — ctx is
// never cancelled here, unlike every other serveUntilDone test — nothing
// used to tell the workers to stop before returning. w blocks on ctx.Done()
// (the same shape recordingWorker always uses), so if serveUntilDone's early
// branch never calls cancelWorkers, w never stops and this test's own check
// fails cleanly (it does not hang: nothing here waits on w finishing, only
// on the flag it sets once it does).
func TestServeUntilDone_EarlyServeFailureCancelsAndWaitsForWorkers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	} // srv.Serve(ln) fails immediately on an already-closed listener

	srv := &http.Server{Handler: http.NotFoundHandler()}

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	w := newRecordingWorker("fake")
	runner := worker.NewRunner(slog.New(slog.DiscardHandler))
	runner.Start(workerCtx, []worker.Worker{w})
	waitStarted(t, w)

	// The outer ctx is context.Background(): deliberately never cancelled,
	// so the only way w can stop is serveUntilDone calling cancelWorkers
	// itself in the early-failure branch.
	code := serveUntilDone(context.Background(), slog.New(slog.DiscardHandler), srv, ln, 5*time.Second, runner, cancelWorkers)

	if code != 1 {
		t.Errorf("exit %d, want 1: srv.Serve failed", code)
	}
	if !w.stopped.Load() {
		t.Error("the early srv.Serve failure path did not stop the worker before returning")
	}
}

// TestRunBounded_ReturnsTrueWhenFnFinishesInTime and
// TestRunBounded_ReturnsFalseWhenFnOutlivesTheTimeout are the unit-level
// bite for review fix round 1, item 2: closePool's whole guarantee (the
// process cannot hang past a fixed point waiting for pgxpool.Pool.Close,
// which has no context parameter of its own) rests on runBounded actually
// giving up when fn does not return in time.
func TestRunBounded_ReturnsTrueWhenFnFinishesInTime(t *testing.T) {
	if !runBounded(func() {}, time.Second) {
		t.Error("runBounded = false, want true for a fn that returns immediately")
	}
}

func TestRunBounded_ReturnsFalseWhenFnOutlivesTheTimeout(t *testing.T) {
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) }) // let the goroutine finish so it does not outlive the test

	if runBounded(func() { <-stuck }, 20*time.Millisecond) {
		t.Error("runBounded = true, want false for a fn that never returns within the timeout")
	}
}
