package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarMailboxSyncClaimVersion is 00087_calendar_mailbox_sync_claim.sql,
// the calendar-mailbox-sync worker's per-person claim, which replaced its
// advisory lease so no connection is held across a sync.
const calendarMailboxSyncClaimVersion = 87

// TestCalendarMailboxSyncClaim_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins the column
// ClaimDueMailboxSync writes: calendar.mailbox_sync's claimed_until, a
// nullable timestamptz. The down drops it.
func TestCalendarMailboxSyncClaim_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarMailboxSyncClaimVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	column := func() []string {
		return staffCollect(t, ctx, pool, "the claim column", `
			SELECT data_type || ':' || is_nullable FROM information_schema.columns
			WHERE table_schema = 'calendar' AND table_name = 'mailbox_sync' AND column_name = 'claimed_until'`)
	}
	if got := column(); !equalStrings(got, []string{"timestamp with time zone:YES"}) {
		t.Errorf("calendar.mailbox_sync.claimed_until = %v", got)
	}

	migrateTo(t, url, calendarMailboxSyncClaimVersion-1)
	if got := column(); len(got) != 0 {
		t.Errorf("the claim column after the down = %v, want none", got)
	}
}
