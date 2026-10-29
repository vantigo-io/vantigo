package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarMailboxSyncVersion is 00081_calendar_mailbox_sync.sql, the
// calendar's Outlook sync setting and map, and entraCalendarSyncVersion
// 00082_entra_calendar_sync.sql, the admin switch
// (docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md, O2, O5).
const (
	calendarMailboxSyncVersion = 81
	entraCalendarSyncVersion   = 82
)

// TestCalendarMailboxSync_AppliesAndIsIdempotent proves the Outlook sync
// migration applies, rolls back and re-applies cleanly, and pins what the
// module's queries rest on: every column of the two tables with its type,
// nullability and default, the setting's key of one per person, the map's
// of one per person per item, and the worker's partial due index.
func TestCalendarMailboxSync_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarMailboxSyncVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := func(table string) []string {
		return staffCollect(t, ctx, pool, "calendar."+table+" columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'calendar' AND table_name = $1
			ORDER BY ordinal_position`, table)
	}
	if got, want := columns("mailbox_sync"), []string{
		"user_id:uuid:NO:",
		"enabled:boolean:NO:false",
		"target:character varying(10):NO:'default'::character varying",
		"detail:character varying(10):NO:'full'::character varying",
		"kinds:ARRAY:NO:'{}'::text[]",
		"mailbox:character varying(200):YES:",
		"calendar_id:character varying(512):YES:",
		"state:character varying(12):NO:'pending'::character varying",
		"error:character varying(40):YES:",
		"next_sync_at:timestamp with time zone:YES:",
		"last_synced_at:timestamp with time zone:YES:",
		"attempts:integer:NO:0",
		"reconcile:boolean:NO:true",
		"revision:integer:NO:1",
		"created_at:timestamp with time zone:NO:",
		"updated_at:timestamp with time zone:NO:",
		// 00083's delta state, pinned by
		// TestCalendarMailboxBusy_AppliesAndIsIdempotent.
		"busy_link:text:YES:",
		"busy_from:date:YES:",
		"busy_to:date:YES:",
		"busy_synced_at:timestamp with time zone:YES:",
		// 00084's put-back, pinned by
		// TestCalendarMailboxWriteback_AppliesAndIsIdempotent.
		"put_back_reason:character varying(20):YES:",
		"put_back_key:character varying(200):YES:",
		"put_back_at:timestamp with time zone:YES:",
		// 00087's claim, pinned by
		// TestCalendarMailboxSyncClaim_AppliesAndIsIdempotent.
		"claimed_until:timestamp with time zone:YES:",
	}; !equalStrings(got, want) {
		t.Errorf("calendar.mailbox_sync columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got, want := columns("mailbox_events"), []string{
		"user_id:uuid:NO:",
		"item_key:character varying(200):NO:",
		"event_id:character varying(512):NO:",
		"content_hash:character varying(64):NO:",
		"starts_on:date:NO:",
		"created_at:timestamp with time zone:NO:",
		"updated_at:timestamp with time zone:NO:",
		// 00084's version and release, pinned by
		// TestCalendarMailboxWriteback_AppliesAndIsIdempotent.
		"change_key:character varying(512):YES:",
		"released_at:timestamp with time zone:YES:",
	}; !equalStrings(got, want) {
		t.Errorf("calendar.mailbox_events columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "calendar", "mailbox_sync"); !equalStrings(cols, []string{"user_id"}) {
		t.Errorf("calendar.mailbox_sync primary key = %v, want [user_id]", cols)
	}
	if cols := primaryKeyColumns(t, ctx, pool, "calendar", "mailbox_events"); !equalStrings(cols, []string{"user_id", "item_key"}) {
		t.Errorf("calendar.mailbox_events primary key = %v, want [user_id item_key]", cols)
	}
	if def, want := indexDefinition(t, ctx, pool, "calendar", "ix_mailbox_sync_due"),
		"CREATE INDEX ix_mailbox_sync_due ON calendar.mailbox_sync USING btree (next_sync_at) WHERE (enabled OR ((state)::text = 'stopping'::text))"; def != want {
		t.Errorf("ix_mailbox_sync_due = %q, want %q", def, want)
	}
}

// TestCalendarMailboxSync_DownDropsBothTables proves the down path removes
// the two tables, rows and all, and leaves the rest of the calendar schema
// as 00057 left it.
func TestCalendarMailboxSync_DownDropsBothTables(t *testing.T) {
	url := testdb.URL(t)
	migrateTo(t, url, calendarMailboxSyncVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO calendar.mailbox_sync (user_id, enabled, kinds, created_at, updated_at)
		VALUES (gen_random_uuid(), true, '{calendar.appointment}', now(), now())`); err != nil {
		t.Fatalf("insert a setting: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO calendar.mailbox_events (user_id, item_key, event_id, content_hash, starts_on, created_at, updated_at)
		VALUES (gen_random_uuid(), 'appointment:1', 'AAMk', repeat('a', 64), '2026-11-02', now(), now())`); err != nil {
		t.Fatalf("insert a map row: %v", err)
	}
	pool.Close()

	migrateTo(t, url, calendarMailboxSyncVersion-1)

	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	tables := staffCollect(t, ctx, pool, "calendar tables", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'calendar' ORDER BY table_name`)
	if want := []string{"appointment_attendees", "appointment_exceptions", "appointments", "feed_tokens", "subscriptions"}; !equalStrings(tables, want) {
		t.Errorf("calendar tables after the down = %v, want %v", tables, want)
	}
}

// TestEntraCalendarSync_AppliesAndIsIdempotent proves the switch's migration
// applies, rolls back and re-applies cleanly: entra.connection gains
// calendar_sync, off, and the down drops it again. 00088 renames the column
// outlook_connect (TestEntraOutlookConnect_AppliesAndIsIdempotent), so it is
// read at this migration's version, not at head.
func TestEntraCalendarSync_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, entraCalendarSyncVersion)
	migrateTo(t, url, entraCalendarSyncVersion)

	ctx := context.Background()
	column := func() []string {
		pool, err := db.Open(ctx, url)
		if err != nil {
			t.Fatalf("open pool: %v", err)
		}
		defer pool.Close()
		return staffCollect(t, ctx, pool, "entra.connection.calendar_sync", `
			SELECT column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'entra' AND table_name = 'connection' AND column_name = 'calendar_sync'`)
	}
	if got, want := column(), []string{"calendar_sync:boolean:NO:false"}; !equalStrings(got, want) {
		t.Errorf("entra.connection.calendar_sync = %v, want %v", got, want)
	}
	migrateTo(t, url, entraCalendarSyncVersion-1)
	if got := column(); len(got) != 0 {
		t.Errorf("entra.connection.calendar_sync after the down = %v, want none", got)
	}
}
