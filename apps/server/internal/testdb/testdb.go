// Package testdb gives every test its own empty PostgreSQL database, created
// on the server named by TEST_DATABASE_URL (default: docker-compose.test.yml's
// server) and dropped when the test ends. Tests never share tables, so
// packages run in parallel and nothing needs truncating.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

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

// URL creates a fresh, empty database and returns its connection string. The
// database is dropped (WITH (FORCE), so leaked connections cannot block it)
// when the test ends.
func URL(t testing.TB) string {
	t.Helper()
	base := baseURL()
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("testdb: TEST_DATABASE_URL is not a URL: %v", err)
	}

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("testdb: cannot reach the test database server %s: %v\n"+
			"Start it with `docker compose -f docker-compose.test.yml up -d --wait`, or point TEST_DATABASE_URL at another server.",
			parsed.Redacted(), err)
	}
	defer func() { _ = admin.Close(ctx) }()

	var b [8]byte
	_, _ = rand.Read(b[:])
	name := "t_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
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

	parsed.Path = "/" + name
	return parsed.String()
}

// PoolMaxConns is the size of every test pool. The gated concurrency tests
// hold a lock while up to ten requests queue behind it and poll
// pg_stat_activity from the same pool, so the size must not follow the
// host's CPU count: pgxpool's default of max(4, NumCPU) leaves a four-core
// CI runner with too few connections, and the queued requests and the poll
// then wait on each other.
const PoolMaxConns = 16

// Migrated creates a fresh database, applies every migration, and returns a
// pool on it (closed when the test ends) together with its connection string.
//
// Applying every migration per test is what bounds how many tests may start at
// once. 00005_energy_baseline.sql creates the range-partitioned
// consumption_intervals and takes ~648 locks in one transaction (145
// AccessExclusive; 25 monthly partitions x ~5 relations). The shared lock table
// is sized as max_locks_per_transaction x (max_connections +
// max_prepared_transactions) — an aggregate, not a per-transaction cap — so
// Postgres's defaults give ~6400 slots and a ceiling near 9 concurrent
// migrators. Beyond it this fails with SQLSTATE 53200, "out of shared memory",
// on a different arbitrary subset of tests every run, which reads as a flaky
// test rather than a resource limit. Run the suite with taskset -c 0-3 (CI's
// sizing) or -p 4 on a many-core host; see CONTRIBUTING.md's Go server
// section. -p is the knob that matters: it bounds how many packages run at
// once, which is what bounds concurrent migrators. -parallel bounds parallel
// tests within one package and caps nothing across them. The migration advisory lock cannot serialise this: advisory locks
// are per-database and every test has its own database, which is why
// production, migrating one database, never sees it.
func Migrated(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	databaseURL := URL(t)
	ctx := context.Background()
	if err := db.ApplyMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("testdb: migrate: %v", err)
	}
	pool, err := db.Open(ctx, databaseURL, db.WithMaxConns(PoolMaxConns))
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, databaseURL
}
