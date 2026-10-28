package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarMailboxWritebackVersion is 00084_calendar_mailbox_writeback.sql,
// the write-back from Outlook
// (docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md, O10).
const calendarMailboxWritebackVersion = 84

// TestCalendarMailboxWriteback_AppliesAndIsIdempotent proves the write-back
// migration applies, rolls back and re-applies cleanly, and pins what the
// module's queries rest on: the map's change key and release on
// calendar.mailbox_events, and the put-back on calendar.mailbox_sync, every
// one nullable and with its width; the down drops them all.
func TestCalendarMailboxWriteback_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarMailboxWritebackVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	columns := func(table string, names ...string) []string {
		return staffCollect(t, ctx, pool, "calendar."+table+" columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'calendar' AND table_name = $1 AND column_name = ANY($2)
			ORDER BY ordinal_position`, table, names)
	}
	events := []string{"change_key", "released_at"}
	putBack := []string{"put_back_reason", "put_back_key", "put_back_at"}
	if got, want := columns("mailbox_events", events...), []string{
		"change_key:character varying(512):YES:",
		"released_at:timestamp with time zone:YES:",
	}; !equalStrings(got, want) {
		t.Errorf("calendar.mailbox_events write-back columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got, want := columns("mailbox_sync", putBack...), []string{
		"put_back_reason:character varying(20):YES:",
		"put_back_key:character varying(200):YES:",
		"put_back_at:timestamp with time zone:YES:",
	}; !equalStrings(got, want) {
		t.Errorf("calendar.mailbox_sync put-back columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	pool.Close()

	migrateTo(t, url, calendarMailboxWritebackVersion-1)
	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	if got := columns("mailbox_events", events...); len(got) != 0 {
		t.Errorf("calendar.mailbox_events write-back columns after the down = %v, want none", got)
	}
	if got := columns("mailbox_sync", putBack...); len(got) != 0 {
		t.Errorf("calendar.mailbox_sync put-back columns after the down = %v, want none", got)
	}
}
