package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// nonUTCDatabase returns a fresh database whose default TimeZone is not UTC,
// as a managed PostgreSQL's may be, with a connection string that asks for
// yet another zone. A plain connection to it is proven not to be in UTC, so
// a session that reports UTC got there by UTCSession.
func nonUTCDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	url := testdb.URL(t)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var name string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `ALTER DATABASE "`+name+`" SET TimeZone = 'Pacific/Auckland'`); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	url += sep + "TimeZone=Europe/Oslo"

	plain, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close(ctx) }()
	var zone string
	if err := plain.QueryRow(ctx, `SHOW TimeZone`).Scan(&zone); err != nil {
		t.Fatal(err)
	}
	if zone == "UTC" {
		t.Fatalf("a plain connection is already in UTC; the test would prove nothing")
	}
	return url
}

// KR42: every pool connection runs in UTC, whatever the database's default
// and whatever the connection string asks for.
func TestOpen_SessionsAreUTC(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, nonUTCDatabase(t), db.WithMaxConns(2))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var zone string
	if err := pool.QueryRow(ctx, `SHOW TimeZone`).Scan(&zone); err != nil {
		t.Fatal(err)
	}
	if zone != "UTC" {
		t.Errorf("SHOW TimeZone = %q, want UTC", zone)
	}
	// The day boundary a date cast draws is UTC's: 23:30 UTC is still the
	// 21st, where Auckland or Oslo would have moved it.
	var day string
	if err := pool.QueryRow(ctx, `SELECT ('2026-11-21T23:30:00Z'::timestamptz)::date::text`).Scan(&day); err != nil {
		t.Fatal(err)
	}
	if day != "2026-11-21" {
		t.Errorf("date cast = %s, want 2026-11-21", day)
	}
}

func TestUTCSession_ReplacesAnySpelling(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://u@localhost/db?timezone=Europe/Oslo&TIMEZONE=Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	db.UTCSession(cfg)
	n := 0
	for k, v := range cfg.RuntimeParams {
		if strings.EqualFold(k, "timezone") {
			n++
			if v != "UTC" {
				t.Errorf("%s = %s, want UTC", k, v)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d TimeZone parameters, want 1: %v", n, cfg.RuntimeParams)
	}
}
