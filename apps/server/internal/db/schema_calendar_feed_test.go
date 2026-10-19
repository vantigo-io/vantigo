package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarFeedVersion is 00057_calendar_feed.sql, the calendar's feed
// addresses and subscriptions
// (docs/superpowers/specs/2026-10-20-calendar-feed-design.md, D1).
const calendarFeedVersion = 57

// TestCalendarFeed_AppliesAndIsIdempotent proves the feed migration applies,
// rolls back and re-applies cleanly, and pins what the module's feed and
// subscription queries rest on: every column of the two tables with its type
// and nullability, the feed address's key of one per person per variant, the
// unique index its GET finds a token's hash by, the subscription's identity
// starting at 1001, and the two indexes a person's list and the worker's due
// list go through.
func TestCalendarFeed_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarFeedVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := func(table string) []string {
		return staffCollect(t, ctx, pool, "calendar."+table+" columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':' || is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'calendar' AND table_name = $1
			ORDER BY ordinal_position`, table)
	}
	if got, want := columns("feed_tokens"), []string{
		"user_id:uuid:NO",
		"variant:character varying(4):NO",
		"token_sha256:character(64):NO",
		"address_ciphertext:text:NO",
		"created_at:timestamp with time zone:NO",
		"last_used_at:timestamp with time zone:YES",
	}; !equalStrings(got, want) {
		t.Errorf("calendar.feed_tokens columns = %v, want %v", got, want)
	}
	if got, want := columns("subscriptions"), []string{
		"id:bigint:NO",
		"user_id:uuid:NO",
		"name:character varying(100):NO",
		"url:character varying(2048):NO",
		"colour:character varying(10):NO",
		"busy_only:boolean:NO",
		"etag:character varying(500):YES",
		"last_modified:character varying(100):YES",
		"body:text:YES",
		"fetched_at:timestamp with time zone:YES",
		"attempted_at:timestamp with time zone:YES",
		"next_fetch_at:timestamp with time zone:YES",
		"error:character varying(500):YES",
		"created_at:timestamp with time zone:NO",
		"updated_at:timestamp with time zone:NO",
	}; !equalStrings(got, want) {
		t.Errorf("calendar.subscriptions columns = %v, want %v", got, want)
	}

	identity := staffCollect(t, ctx, pool, "the subscription id's identity", `
		SELECT is_identity || ':' || identity_generation || ':' || identity_start FROM information_schema.columns
		WHERE table_schema = 'calendar' AND table_name = 'subscriptions' AND column_name = 'id'`)
	if want := []string{"YES:ALWAYS:1001"}; !equalStrings(identity, want) {
		t.Errorf("calendar.subscriptions.id identity = %v, want %v", identity, want)
	}

	for _, want := range []struct {
		name    string
		columns []string
		unique  bool
	}{
		{"feed_tokens_pkey", []string{"user_id", "variant"}, true},
		{"ux_feed_tokens_hash", []string{"token_sha256"}, true},
		{"subscriptions_pkey", []string{"id"}, true},
		{"ix_subscriptions_user", []string{"user_id"}, false},
		{"ix_subscriptions_due", []string{"next_fetch_at"}, false},
	} {
		if cols := indexColumns(t, ctx, pool, "calendar", want.name); !equalStrings(cols, want.columns) {
			t.Errorf("%s columns = %v, want %v", want.name, cols, want.columns)
		}
		def := indexDefinition(t, ctx, pool, "calendar", want.name)
		if unique := strings.HasPrefix(def, "CREATE UNIQUE INDEX "); unique != want.unique {
			t.Errorf("%s = %q, want unique %v", want.name, def, want.unique)
		}
	}
}

// TestCalendarFeed_DownDropsBothTables proves the down path removes the two
// tables, rows and all, and leaves the rest of the calendar schema as 00056
// left it.
func TestCalendarFeed_DownDropsBothTables(t *testing.T) {
	url := testdb.URL(t)
	migrateTo(t, url, calendarFeedVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO calendar.feed_tokens (user_id, variant, token_sha256, address_ciphertext, created_at)
		VALUES (gen_random_uuid(), 'full', repeat('a', 64), 'sealed', now())`); err != nil {
		t.Fatalf("insert a feed address: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO calendar.subscriptions (user_id, name, url, colour, busy_only, created_at, updated_at)
		VALUES (gen_random_uuid(), 'Helligdager', 'https://example.com/a.ics', 'teal', false, now(), now())`); err != nil {
		t.Fatalf("insert a subscription: %v", err)
	}
	pool.Close()

	migrateTo(t, url, calendarFeedVersion-1)

	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	tables := staffCollect(t, ctx, pool, "calendar tables", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'calendar' ORDER BY table_name`)
	if want := []string{"appointment_attendees", "appointment_exceptions", "appointments"}; !equalStrings(tables, want) {
		t.Errorf("calendar tables after the down = %v, want %v", tables, want)
	}
}
