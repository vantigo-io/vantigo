// Package testdb gives every test its own PostgreSQL database, created on the
// server named by TEST_DATABASE_URL (default: docker-compose.test.yml's
// server) and dropped when the test ends. Tests never share tables, so
// packages run in parallel and nothing needs truncating.
//
// Migrated databases are copies of one per-binary template rather than fresh
// migration runs; see Migrated and templateName for why that is both faster
// and still fully isolated.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
)

const defaultURL = "postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable"

// baseURL is the server tests create databases on. It must be URL-form and
// its role must be allowed to CREATE DATABASE.
func baseURL() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultURL
}

// databaseURL returns base's connection string pointed at the database name.
func databaseURL(base, name string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("testdb: TEST_DATABASE_URL is not a URL: %w", err)
	}
	parsed.Path = "/" + name
	return parsed.String(), nil
}

// randomName returns prefix followed by 16 hex characters, which is always a
// legal unquoted SQL identifier, so callers may concatenate it into DDL.
func randomName(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// URL creates a fresh, empty database and returns its connection string. The
// database is dropped (WITH (FORCE), so leaked connections cannot block it)
// when the test ends.
func URL(t testing.TB) string {
	t.Helper()
	return create(t, "")
}

// create makes a database for t and returns its connection string, copying
// template when it is non-empty and creating an empty database otherwise.
// The database is dropped when t ends.
func create(t testing.TB, template string) string {
	t.Helper()
	base := baseURL()
	ctx := context.Background()
	admin := connectAdmin(t, base)
	defer func() { _ = admin.Close(ctx) }()

	name := randomName("t_")
	stmt := "CREATE DATABASE " + name
	if template != "" {
		stmt += " TEMPLATE " + template
	}
	if _, err := admin.Exec(ctx, stmt); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Errorf("testdb: drop %s: %v", name, err)
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("testdb: drop %s: %v", name, err)
		}
	})

	databaseURL, err := databaseURL(base, name)
	if err != nil {
		t.Fatal(err)
	}
	return databaseURL
}

// connectAdmin dials the server's own database, the one CREATE DATABASE and
// DROP DATABASE are issued from.
func connectAdmin(t testing.TB, base string) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), base)
	if err != nil {
		parsed, parseErr := url.Parse(base)
		shown := base
		if parseErr == nil {
			shown = parsed.Redacted()
		}
		t.Fatalf("testdb: cannot reach the test database server %s: %v\n"+
			"Start it with `docker compose -f docker-compose.test.yml up -d --wait`, or point TEST_DATABASE_URL at another server.",
			shown, err)
	}
	return conn
}

// PoolMaxConns is the size of every test pool. The gated concurrency tests
// hold a lock while up to ten requests queue behind it and poll
// pg_stat_activity from the same pool, so the size must not follow the
// host's CPU count: pgxpool's default of max(4, NumCPU) leaves a four-core
// CI runner with too few connections, and the queued requests and the poll
// then wait on each other.
const PoolMaxConns = 16

// templatePrefix names every template database this package creates. The
// name continues <created-unix-millis>_<owning-pid>_<random>, which is what
// sweepStaleTemplates reads back to decide whether a template is finished
// with.
const templatePrefix = "vtpl_"

// templateMaxAge is the fallback for dropping a template whose owning
// process still appears to be alive — a PID the operating system has since
// reused, or an owner this process cannot ask about. It has to be
// comfortably longer than the whole suite takes (~80s), so that it can never
// remove a template a running binary is still copying from.
const templateMaxAge = time.Hour

// migrated is the one migrated template per test binary. sync.Once is what
// makes creation race-free: parallel tests inside a package, and the first
// Migrated call of each of the -p packages running at once, each get the same
// template, built exactly once per process.
var migrated struct {
	once sync.Once
	name string
	err  error
}

// templateName returns the name of this binary's migrated template database,
// creating it on first use.
//
// The template is created, migrated, and then permanently closed to
// connections, because CREATE DATABASE ... TEMPLATE src fails with SQLSTATE
// 55006, `source database "src" is being accessed by other users`, while any
// session is attached to src. Under parallel tests that would fail on a
// different arbitrary subset of tests every run and read as a flaky test
// rather than a design fault. Two things make it impossible here rather than
// merely unlikely: ALLOW_CONNECTIONS false, which makes the server itself
// reject any later connection to the template, and the wait below, which does
// not return until pg_stat_activity shows the migrating backend has actually
// exited (closing a connection client-side does not make its backend gone
// synchronously). Copies themselves are concurrent-safe; only connections to
// the source break them.
func templateName(t testing.TB) string {
	t.Helper()
	migrated.once.Do(func() { migrated.name, migrated.err = createTemplate(context.Background()) })
	if migrated.err != nil {
		t.Fatalf("testdb: %v", migrated.err)
	}
	return migrated.name
}

func createTemplate(ctx context.Context) (string, error) {
	base := baseURL()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		parsed, parseErr := url.Parse(base)
		shown := base
		if parseErr == nil {
			shown = parsed.Redacted()
		}
		return "", fmt.Errorf("cannot reach the test database server %s "+
			"(start it with `docker compose -f docker-compose.test.yml up -d --wait`, "+
			"or point TEST_DATABASE_URL at another server): %w", shown, err)
	}
	defer func() { _ = admin.Close(ctx) }()

	sweepStaleTemplates(ctx, admin)

	name := fmt.Sprintf("%s%d_%d_%s", templatePrefix, time.Now().UnixMilli(), os.Getpid(), randomName(""))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", fmt.Errorf("create template database: %w", err)
	}
	templateURL, err := databaseURL(base, name)
	if err != nil {
		return "", err
	}
	// The one full migration run per test binary. A broken migration still
	// fails every test of every package that uses a migrated database.
	if err := db.ApplyMigrations(ctx, templateURL); err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		return "", fmt.Errorf("migrate: %w", err)
	}

	if _, err := admin.Exec(ctx, "ALTER DATABASE "+name+" WITH ALLOW_CONNECTIONS false"); err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		return "", fmt.Errorf("close the template to connections: %w", err)
	}
	if err := waitForNoBackends(ctx, admin, name); err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		return "", err
	}
	return name, nil
}

// waitForNoBackends returns once no backend is attached to name, terminating
// any that linger. ALLOW_CONNECTIONS false has already barred new ones, so
// this converges; it exists because a client-side Close leaves the server
// backend exiting asynchronously, and a copy started in that window would
// fail with SQLSTATE 55006.
func waitForNoBackends(ctx context.Context, admin *pgx.Conn, name string) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		var backends int
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
			name).Scan(&backends); err != nil {
			return fmt.Errorf("count sessions on the template: %w", err)
		}
		if backends == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("template %s still has %d session(s) after 30s", name, backends)
		}
		if _, err := admin.Exec(ctx,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
			name); err != nil {
			return fmt.Errorf("terminate sessions on the template: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sweepStaleTemplates drops templates nobody is using any more.
//
// Each binary migrates its own template, so a template is never reused across
// runs — that is deliberate: reuse would skip ApplyMigrations, and a broken
// migration would stop failing the suite. What must not happen is
// accumulation. A template is 12MB, every test binary that uses a migrated
// database builds one, and Go's testing package offers no whole-binary exit
// hook to drop it from, so without a sweep a working day's test runs would
// leave gigabytes of dead databases on the shared server.
//
// A template is therefore owned by the process that created it, and any run
// may drop a template whose owner has exited — which covers every template of
// every finished binary, including the earlier binaries of the run now in
// progress, so a suite cleans up after itself as it goes. A template whose
// owner still looks alive is dropped only once it is older than
// templateMaxAge, which covers a PID the system has since reused. Neither
// rule can remove a template a live binary is still copying from, and a
// name in any other shape is left alone.
//
// It is best-effort: a concurrent run may drop the same stale template first,
// and losing that race must not fail anybody's tests.
func sweepStaleTemplates(ctx context.Context, admin *pgx.Conn) {
	rows, err := admin.Query(ctx,
		"SELECT datname FROM pg_database WHERE datname LIKE $1", templatePrefix+"%")
	if err != nil {
		return
	}
	var stale []string
	cutoff := time.Now().Add(-templateMaxAge).UnixMilli()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		millis, pid, ok := parseTemplateName(name)
		if !ok {
			continue
		}
		if !processIsAlive(pid) || millis < cutoff {
			stale = append(stale, name)
		}
	}
	rows.Close()
	for _, name := range stale {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	}
}

// parseTemplateName reads the creation time and the owning PID back out of a
// template database's name, reporting false for a name this package did not
// write.
func parseTemplateName(name string) (millis int64, pid int, ok bool) {
	rest, found := strings.CutPrefix(name, templatePrefix)
	if !found {
		return 0, 0, false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 3 {
		return 0, 0, false
	}
	millis, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	pid, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return millis, pid, true
}

// processIsAlive reports whether pid is still running. It is deliberately
// conservative: only a process the operating system positively reports as
// gone counts as dead, so an owner that cannot be signalled — another user's
// process, or a platform where the question cannot be asked at all — keeps
// its template until templateMaxAge rather than losing it early.
func processIsAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH)
}

// Migrated creates a fresh database holding the full schema and returns a
// pool on it (closed when the test ends) together with its connection string.
//
// The database is a copy of this binary's template (CREATE DATABASE ...
// TEMPLATE, which PostgreSQL implements as a file copy) rather than another
// run of the six migrations: measured on the compose server, a copy costs
// ~17ms against ~244ms for a migration run, and copies do not get slower as
// more of them run at once. Isolation is unchanged — every test still gets
// its own database, created for it and dropped WITH (FORCE) when it ends, and
// the template is only ever read from, never written to or connected to.
//
// This is also what lifted the old limit on how many tests could start at
// once. 00005_energy_baseline.sql creates the range-partitioned
// consumption_intervals and takes ~648 locks in one transaction (145
// AccessExclusive; 25 monthly partitions x ~5 relations). The shared lock
// table is sized as max_locks_per_transaction x (max_connections +
// max_prepared_transactions) — an aggregate, not a per-transaction cap — so
// PostgreSQL's defaults give ~6400 slots and a ceiling near 9 concurrent
// migrators, beyond which migrations failed with SQLSTATE 53200, "out of
// shared memory", on a different arbitrary subset of tests every run. Now
// only one migration runs per test binary, so the number of concurrent
// migrators is bounded by -p (how many packages run at once) rather than by
// how many tests are in flight. Keep running the suite with taskset -c 0-3
// (CI's sizing) or -p 4 on a many-core host; see CONTRIBUTING.md's Go server
// section. The migration advisory lock cannot serialise this: advisory locks
// are per-database and every template is its own database, which is why
// production, migrating one database, never sees it.
func Migrated(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	databaseURL := create(t, templateName(t))
	pool, err := db.Open(context.Background(), databaseURL, db.WithMaxConns(PoolMaxConns))
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, databaseURL
}
