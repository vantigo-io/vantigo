package db_test

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func tableExists(t *testing.T, databaseURL, table string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var present bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&present); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	return present
}

func TestApplyMigrations_CreatesThePlatformSchema(t *testing.T) {
	url := testdb.URL(t)
	if err := db.ApplyMigrations(context.Background(), url); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if !tableExists(t, url, "platform.rate_limit") {
		t.Error("platform.rate_limit was not created")
	}
}

func TestApplyMigrations_IsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	for i := range 2 {
		if err := db.ApplyMigrations(context.Background(), url); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

// One DATABASE_URL serves both the request pool and the migrator, and the
// deployment docs tell operators to size the pool with pool_max_conns on it.
// pgxpool consumes that key; a plain pgx parse forwards it to PostgreSQL as a
// runtime parameter, which rejects the connection with "unrecognized
// configuration parameter" — so a URL that is right for the pool would fail
// startup at the migration step.
func TestApplyMigrations_AcceptsPoolParametersOnTheURL(t *testing.T) {
	url := testdb.URL(t)
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	url += sep + "pool_max_conns=3&pool_min_conns=1"
	if err := db.ApplyMigrations(context.Background(), url); err != nil {
		t.Fatalf("ApplyMigrations with pool parameters on the URL: %v", err)
	}
}

// Open must apply its options. A signature that accepts them and drops them
// compiles and passes every caller's tests, while the pool silently keeps
// pgxpool's default of max(4, NumCPU): enough on a large workstation, too
// few on a small CI runner, where the gated concurrency tests then starve.
func TestOpen_AppliesWithMaxConns(t *testing.T) {
	pool, err := db.Open(context.Background(), testdb.URL(t), db.WithMaxConns(3))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if got := pool.Config().MaxConns; got != 3 {
		t.Errorf("MaxConns = %d, want 3", got)
	}
}

// A second migrator must wait on the advisory lock rather than race the first.
// pg_stat_activity is the ground truth for "waiting on the lock", not a sleep.
func TestApplyMigrations_WaitsForTheAdvisoryLock(t *testing.T) {
	url := testdb.URL(t)
	ctx := context.Background()

	holder, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", db.MigrationLockKey); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- db.ApplyMigrations(ctx, url) }()

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

	select {
	case err := <-done:
		t.Fatalf("ApplyMigrations finished while the lock was held: %v", err)
	default:
	}
	if tableExists(t, url, "platform.rate_limit") {
		t.Fatal("migrations ran while another session held the lock")
	}

	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", db.MigrationLockKey); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ApplyMigrations: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ApplyMigrations did not finish after the lock was released")
	}
	if !tableExists(t, url, "platform.rate_limit") {
		t.Error("platform.rate_limit is missing after the waiting migrator ran")
	}
}

func TestOpen_ReturnsAPingedPool(t *testing.T) {
	pool, err := db.Open(context.Background(), testdb.URL(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestOpen_GivesUpOnAnUnreachableServer(t *testing.T) {
	defer db.SetRetryDelay(func(int) time.Duration { return 0 })()

	_, err := db.Open(context.Background(), "postgres://nobody:secret@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err == nil || !strings.Contains(err.Error(), "unreachable after 6 attempts") {
		t.Fatalf("err = %v, want it to give up after 6 attempts", err)
	}
}

// A black-holed host never answers; each attempt must give up on its own
// rather than wait for the kernel's connect timeout.
func TestWaitForDatabase_BoundsEachAttempt(t *testing.T) {
	defer db.SetRetryDelay(func(int) time.Duration { return 0 })()
	defer db.SetPingTimeout(20 * time.Millisecond)()

	var attempts atomic.Int32
	blackHole := func(ctx context.Context) error {
		attempts.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- db.WaitForDatabase(context.Background(), blackHole) }()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "unreachable after 6 attempts") {
			t.Fatalf("err = %v, want it to give up after 6 attempts", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("took %v, want each attempt bounded by the ping timeout", elapsed)
		}
		if got := attempts.Load(); got != 6 {
			t.Errorf("%d attempts, want 6", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForDatabase is still waiting on a ping that never answers; each attempt must be bounded")
	}
}

// TestOpen_TracesQueries proves query spans join the request's trace.
// otelpgx only starts a query span when the context already carries a
// recording parent span (the request span telemetry.HTTPHandler installs in
// production), so the query is issued inside one here rather than on a bare
// context.Background(), matching how it is actually reached in serve.
func TestOpen_TracesQueries(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	defer otel.SetTracerProvider(tracenoop.NewTracerProvider())

	pool, err := db.Open(context.Background(), testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx, span := otel.Tracer("test").Start(context.Background(), "request")
	if _, err := pool.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	span.End()
	for _, s := range recorder.Ended() {
		if strings.Contains(s.Name(), "query") {
			return
		}
	}
	t.Errorf("no query span among %d spans", len(recorder.Ended()))
}

var migrationName = regexp.MustCompile(`^\d{5}_[a-z]+_[a-z0-9_]+\.sql$`)

func TestMigrationFilesFollowTheNamingRule(t *testing.T) {
	entries, err := fs.ReadDir(db.MigrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded migrations")
	}
	for _, e := range entries {
		if !migrationName.MatchString(e.Name()) {
			t.Errorf("%s does not match NNNNN_<owner>_<name>.sql", e.Name())
		}
	}
}
