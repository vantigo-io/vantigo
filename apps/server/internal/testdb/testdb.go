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

// Migrated creates a fresh database, applies every migration, and returns a
// pool on it (closed when the test ends) together with its connection string.
func Migrated(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	databaseURL := URL(t)
	ctx := context.Background()
	if err := db.ApplyMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("testdb: migrate: %v", err)
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, databaseURL
}
