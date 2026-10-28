package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarMailboxBusyVersion is 00083_calendar_mailbox_busy.sql, Outlook's
// busy time read back (docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md, O9).
const calendarMailboxBusyVersion = 83

// TestCalendarMailboxBusy_AppliesAndIsIdempotent proves the busy-time
// migration applies, rolls back and re-applies cleanly, and pins what the
// module's queries rest on: every column of calendar.mailbox_busy with its
// type and nullability — times and show_as, nothing of what an event is
// about — its key of one row per person per event, the two window indexes,
// and the delta's state on calendar.mailbox_sync; the down drops them all.
func TestCalendarMailboxBusy_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarMailboxBusyVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	columns := func(table, like string) []string {
		return staffCollect(t, ctx, pool, "calendar."+table+" columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'calendar' AND table_name = $1 AND column_name LIKE $2
			ORDER BY ordinal_position`, table, like)
	}
	if got, want := columns("mailbox_busy", "%"), []string{
		"user_id:uuid:NO:",
		"event_id:character varying(512):NO:",
		"all_day:boolean:NO:",
		"starts_at:timestamp with time zone:YES:",
		"ends_at:timestamp with time zone:YES:",
		"start_date:date:YES:",
		"end_date:date:YES:",
		"show_as:character varying(20):NO:",
		"updated_at:timestamp with time zone:NO:",
	}; !equalStrings(got, want) {
		t.Errorf("calendar.mailbox_busy columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "calendar", "mailbox_busy"); !equalStrings(cols, []string{"user_id", "event_id"}) {
		t.Errorf("calendar.mailbox_busy primary key = %v, want [user_id event_id]", cols)
	}
	for _, want := range []struct {
		name    string
		columns []string
	}{
		{"ix_mailbox_busy_starts", []string{"user_id", "starts_at"}},
		{"ix_mailbox_busy_dates", []string{"user_id", "start_date"}},
	} {
		if cols := indexColumns(t, ctx, pool, "calendar", want.name); !equalStrings(cols, want.columns) {
			t.Errorf("%s columns = %v, want %v", want.name, cols, want.columns)
		}
	}
	if got, want := columns("mailbox_sync", "busy_%"), []string{
		"busy_link:text:YES:",
		"busy_from:date:YES:",
		"busy_to:date:YES:",
		"busy_synced_at:timestamp with time zone:YES:",
	}; !equalStrings(got, want) {
		t.Errorf("calendar.mailbox_sync busy columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	pool.Close()

	migrateTo(t, url, calendarMailboxBusyVersion-1)
	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	if got := columns("mailbox_busy", "%"); len(got) != 0 {
		t.Errorf("calendar.mailbox_busy after the down = %v, want no table", got)
	}
	if got := columns("mailbox_sync", "busy_%"); len(got) != 0 {
		t.Errorf("calendar.mailbox_sync busy columns after the down = %v, want none", got)
	}
}
