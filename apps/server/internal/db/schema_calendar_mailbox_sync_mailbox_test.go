package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarMailboxSyncMailboxVersion is 00086_calendar_mailbox_sync_mailbox.sql,
// the index the change notifications' marks read
// (docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md, O11.6).
const calendarMailboxSyncMailboxVersion = 86

// TestCalendarMailboxSyncMailbox_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins the index
// MarkMailboxSyncNotified rests on: calendar.mailbox_sync's mailbox in lower
// case, over the rows that are on. The down drops it.
func TestCalendarMailboxSyncMailbox_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarMailboxSyncMailboxVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	index := func() []string {
		return staffCollect(t, ctx, pool, "the mailbox index", `
			SELECT indexdef FROM pg_indexes
			WHERE schemaname = 'calendar' AND tablename = 'mailbox_sync' AND indexname = 'ix_calendar_mailbox_sync_mailbox'`)
	}
	if got := index(); !equalStrings(got, []string{
		"CREATE INDEX ix_calendar_mailbox_sync_mailbox ON calendar.mailbox_sync USING btree (lower((mailbox)::text)) WHERE enabled",
	}) {
		t.Errorf("calendar.mailbox_sync's mailbox index = %v", got)
	}

	migrateTo(t, url, calendarMailboxSyncMailboxVersion-1)
	if got := index(); len(got) != 0 {
		t.Errorf("the mailbox index after the down = %v, want none", got)
	}
}
