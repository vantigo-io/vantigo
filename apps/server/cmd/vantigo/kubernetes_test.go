package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// These tests cover the Kubernetes readiness design's delivery A: the
// management listener in every command (KR1–KR3), the schema wait (KR4) and
// the drain (KR5). Those that swap listenManagement must not run in
// parallel.

// serveConfig is startServeEnv's fixture, for a test that calls serve
// itself because serve does not become ready on the public listener.
func serveConfig(t *testing.T, databaseURL string, env map[string]string) *config.Config {
	t.Helper()
	m := map[string]string{
		"DATABASE_URL":                     databaseURL,
		"APP_URL":                          "http://localhost:8080",
		"SHUTDOWN_TIMEOUT":                 "10s",
		"APP_SECRET":                       testAppSecret,
		"SMTP_HOST":                        "smtp.example.invalid",
		"SMTP_FROM":                        "noreply@example.invalid",
		"CUSTOMERS_REGISTRY_FEED_ENABLED":  "0",
		"CUSTOMERS_PEPPOL_RECHECK_ENABLED": "0",
		"CUSTOMERS_ANONYMISATION_ENABLED":  "0",
		"STAFF_RETENTION_ENABLED":          "0",
		"STAFF_ACCOUNTS_ENABLED":           "0",
		"STAFF_COMPETENCE_ENABLED":         "0",
	}
	for k, v := range env {
		m[k] = v
	}
	cfg, err := config.Load(m)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// get answers a GET of url's status and body, with token as a bearer when
// it is not empty.
func get(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// eventually polls cond every 20 ms until it holds or limit passes.
func eventually(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: never happened within %s", what, limit)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// KR2: the worker command opens the management listener too, with the
// probes, the status (without the live hub, which a worker has none of) and
// the outbox's deliveries.
func TestServe_WorkerModeServesTheManagementListener(t *testing.T) {
	token := strings.Repeat("m", 32)
	addr := managementAddr(t)
	_, stop := startServeEnv(t, modeWorker, map[string]string{"MANAGEMENT_PORT": "9090", "MANAGEMENT_TOKEN": token})
	mgmt := "http://" + addr()

	if code, text := get(t, mgmt+"/health/ready", ""); code != http.StatusOK {
		t.Errorf("ready: %d %s", code, text)
	}
	code, text := get(t, mgmt+"/management/status", token)
	if code != http.StatusOK || !strings.Contains(text, `"databaseBytes":`) || strings.Contains(text, `"live"`) {
		t.Errorf("status: %d %s, want it without the live hub", code, text)
	}
	if code, text := get(t, mgmt+"/management/events/deliveries", token); code != http.StatusOK || !strings.Contains(text, `"deliveries"`) {
		t.Errorf("deliveries: %d %s", code, text)
	}
	if code := stop(); code != 0 {
		t.Errorf("exit %d", code)
	}
}

// KR1, KR3: MANAGEMENT_PORT without MANAGEMENT_TOKEN serves the probes to
// anyone, with no host filter, and refuses every other route.
func TestServe_ManagementProbesWithoutAToken(t *testing.T) {
	addr := managementAddr(t)
	_, stop := startServeEnv(t, modeServer, map[string]string{"MANAGEMENT_PORT": "9090"})
	mgmt := "http://" + addr()

	for _, path := range []string{"/health/live", "/health/ready"} {
		if code, text := get(t, mgmt+path, ""); code != http.StatusOK || !strings.Contains(text, `"version":"dev"`) {
			t.Errorf("%s: %d %s", path, code, text)
		}
	}
	if code, _ := get(t, mgmt+"/management/status", strings.Repeat("x", 32)); code != http.StatusUnauthorized {
		t.Errorf("status with no token configured: %d, want 401", code)
	}
	if code := stop(); code != 0 {
		t.Errorf("exit %d", code)
	}
}

// KR4: server mode on a database not yet migrated does nothing but answer
// the probes, readiness naming the schema, until the migration lands; then
// it starts and serves.
func TestServe_ServerModeWaitsForTheSchema(t *testing.T) {
	addr := managementAddr(t)
	url := testdb.URL(t)
	cfg := serveConfig(t, url, map[string]string{"MANAGEMENT_PORT": "9090"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- serve(ctx, slog.New(slog.DiscardHandler), cfg, ln, modeServer) }()
	mgmt := "http://" + addr()

	eventually(t, 10*time.Second, "readiness naming the schema", func() bool {
		code, text := get(t, mgmt+"/health/ready", "")
		return code == http.StatusServiceUnavailable && strings.Contains(text, `"schema"`)
	})
	if code, text := get(t, mgmt+"/health/live", ""); code != http.StatusOK {
		t.Errorf("live while waiting: %d %s", code, text)
	}
	// Nothing ran: identity.RunStartup would have failed on the missing
	// schema and serve would have returned.
	select {
	case code := <-done:
		t.Fatalf("serve returned %d while waiting for the schema", code)
	default:
	}

	if err := db.ApplyMigrations(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	waitReady(t, "http://"+ln.Addr().String())
	if code, text := get(t, mgmt+"/health/ready", ""); code != http.StatusOK {
		t.Errorf("management ready after the migration: %d %s", code, text)
	}

	http.DefaultClient.CloseIdleConnections()
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return")
	}
}

// KR4: a signal while waiting for the schema exits cleanly, having started
// nothing.
func TestServe_ASignalWhileWaitingForTheSchemaExits0(t *testing.T) {
	cfg := serveConfig(t, testdb.URL(t), nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	w := newRecordingWorker("fake")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- serve(ctx, slog.New(slog.DiscardHandler), cfg, ln, modeWorker, fakeWorkerModule(w)) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after the signal")
	}
	select {
	case <-w.started:
		t.Error("a worker started before the schema was there")
	default:
	}
}

// KR5, end to end: during SHUTDOWN_DRAIN_DELAY readiness fails with
// "draining" on the management port while the public listener still serves.
func TestServe_ReadinessFailsWhileTheDrainDelayRuns(t *testing.T) {
	addr := managementAddr(t)
	base, stop := startServeEnv(t, modeServer, map[string]string{"MANAGEMENT_PORT": "9090", "SHUTDOWN_DRAIN_DELAY": "1500ms"})
	mgmt := "http://" + addr()

	exit := make(chan int, 1)
	go func() { exit <- stop() }()
	eventually(t, time.Second, "readiness naming draining", func() bool {
		code, text := get(t, mgmt+"/health/ready", "")
		return code == http.StatusServiceUnavailable && strings.Contains(text, `"draining"`)
	})
	if code, _ := get(t, base+"/health/live", ""); code != http.StatusOK {
		t.Errorf("the public listener stopped serving during the delay: %d", code)
	}
	if code := <-exit; code != 0 {
		t.Errorf("exit %d", code)
	}
}

// KR5, the order: readiness fails at the signal; for the delay the server
// keeps serving, the sockets stay open and the workers keep running; then
// the sockets close, before the HTTP drain completes, and the workers stop.
func TestServeUntilDone_DrainsInOrder(t *testing.T) {
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	w := newRecordingWorker("fake")
	runner := worker.NewRunner(slog.New(slog.DiscardHandler))
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	runner.Start(workerCtx, []worker.Worker{w})
	waitStarted(t, w)

	var mu sync.Mutex
	var order []string
	note := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, s)
	}
	closed := make(chan struct{})
	closeSockets := func() { note("sockets closed"); close(closed) }

	const delay = 300 * time.Millisecond
	lc := newLifecycle(delay, 5*time.Second)
	lc.schema.Store(true)
	lc.started.Store(true)
	ctx, signal := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- serveUntilDone(ctx, slog.New(slog.DiscardHandler), srv, ln, lc, runner, func() { note("workers cancelled"); cancelWorkers() }, closeSockets)
	}()

	signalled := time.Now()
	signal()
	eventually(t, time.Second, "draining", func() bool { return lc.draining.Load() })
	if code, body := get(t, "http://"+ln.Addr().String(), ""); code != http.StatusOK || body != "ok" {
		t.Errorf("a request during the delay: %d %q", code, body)
	}
	if w.stopped.Load() {
		t.Error("the worker stopped before the delay ended")
	}

	select {
	case <-closed:
		if elapsed := time.Since(signalled); elapsed < delay {
			t.Errorf("the sockets closed %s after the signal, before the %s delay", elapsed, delay)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sockets never closed")
	}
	http.DefaultClient.CloseIdleConnections()
	if code := <-done; code != 0 {
		t.Errorf("exit %d", code)
	}
	if !w.stopped.Load() {
		t.Error("serveUntilDone returned before the worker stopped")
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ", ") != "sockets closed, workers cancelled" {
		t.Errorf("order = %v", order)
	}
}

// KR5: the whole tail shares one SHUTDOWN_TIMEOUT, started at the first
// call, not one bound each.
func TestLifecycle_TheShutdownBudgetIsShared(t *testing.T) {
	lc := newLifecycle(0, 200*time.Millisecond)
	defer lc.release()
	first := lc.budget()
	time.Sleep(50 * time.Millisecond)
	if lc.budget() != first {
		t.Fatal("a second call started another budget")
	}
	if left := lc.remaining(); left > 160*time.Millisecond || left <= 0 {
		t.Errorf("remaining = %s, want under 150ms of the 200ms", left)
	}
	<-first.Done()
	if lc.remaining() != 0 {
		t.Errorf("remaining after the deadline = %s, want 0", lc.remaining())
	}
}

// The lifecycle's checks name what holds readiness back.
func TestLifecycle_Checks(t *testing.T) {
	lc := newLifecycle(0, time.Second)
	failing := func() []string {
		var names []string
		for _, c := range lc.checks() {
			if c.Run(context.Background()) != nil {
				names = append(names, c.Name)
			}
		}
		return names
	}
	if got := strings.Join(failing(), ","); got != "schema,startup" {
		t.Errorf("at start: %s", got)
	}
	lc.schema.Store(true)
	lc.started.Store(true)
	if got := failing(); len(got) != 0 {
		t.Errorf("serving: %v", got)
	}
	lc.draining.Store(true)
	if got := strings.Join(failing(), ","); got != "draining" {
		t.Errorf("draining: %s", got)
	}
}
