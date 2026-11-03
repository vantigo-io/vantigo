package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarMailboxNeutralVersion is 00110_calendar_mailbox_neutral.sql: the
// mailbox sync's provider, the marks' index by provider, and the busy rows'
// neutral show_as values
// (docs/superpowers/specs/2026-11-03-google-workspace-design.md, G3 and G10).
const calendarMailboxNeutralVersion = 110

// TestCalendarMailboxNeutral_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what it does to the
// rows it finds: a row with a mailbox is Entra's, one without has no
// provider; 'oof' and 'workingElsewhere' become 'out_of_office' and
// 'working_elsewhere', the other values kept; the index on (provider,
// lower(mailbox)) over the rows on replaces 00086's. The down puts the
// values and the index back and drops the column.
func TestCalendarMailboxNeutral_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, calendarMailboxNeutralVersion)
	migrateTo(t, url, calendarMailboxNeutralVersion-1)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	const kari, ola = "00000000-0000-0000-0000-00000000000a", "00000000-0000-0000-0000-00000000000b"
	if _, err := pool.Exec(ctx, `
		INSERT INTO calendar.mailbox_sync (user_id, enabled, mailbox, created_at, updated_at)
		VALUES ($1, true, 'oid-kari', now(), now()), ($2, true, NULL, now(), now())`, kari, ola); err != nil {
		t.Fatalf("insert the syncs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO calendar.mailbox_busy (user_id, event_id, all_day, start_date, end_date, show_as, updated_at)
		VALUES ($1, 'a', true, '2026-11-10', '2026-11-10', 'oof', now()),
		       ($1, 'b', true, '2026-11-11', '2026-11-11', 'workingElsewhere', now()),
		       ($1, 'c', true, '2026-11-12', '2026-11-12', 'busy', now()),
		       ($1, 'd', true, '2026-11-13', '2026-11-13', 'tentative', now())`, kari); err != nil {
		t.Fatalf("insert the busy rows: %v", err)
	}
	migrateTo(t, url, calendarMailboxNeutralVersion)

	providers := staffCollect(t, ctx, pool, "the providers", `
		SELECT user_id::text || ':' || coalesce(provider, '-') FROM calendar.mailbox_sync ORDER BY user_id`)
	if want := []string{kari + ":entra", ola + ":-"}; !equalStrings(providers, want) {
		t.Errorf("the providers = %v, want %v", providers, want)
	}
	column := staffCollect(t, ctx, pool, "the provider column", `
		SELECT data_type || '(' || character_maximum_length || '):' || is_nullable FROM information_schema.columns
		WHERE table_schema = 'calendar' AND table_name = 'mailbox_sync' AND column_name = 'provider'`)
	if want := []string{"character varying(20):YES"}; !equalStrings(column, want) {
		t.Errorf("calendar.mailbox_sync.provider = %v, want %v", column, want)
	}
	showAs := func() []string {
		return staffCollect(t, ctx, pool, "the busy values", `
			SELECT event_id || ':' || show_as FROM calendar.mailbox_busy ORDER BY event_id`)
	}
	if want := []string{"a:out_of_office", "b:working_elsewhere", "c:busy", "d:tentative"}; !equalStrings(showAs(), want) {
		t.Errorf("the busy values = %v, want %v", showAs(), want)
	}
	indexes := func() []string {
		return staffCollect(t, ctx, pool, "the mailbox indexes", `
			SELECT indexdef FROM pg_indexes
			WHERE schemaname = 'calendar' AND tablename = 'mailbox_sync'
			  AND indexname IN ('ix_calendar_mailbox_sync_mailbox', 'ix_calendar_mailbox_sync_provider_mailbox')`)
	}
	if want := []string{
		"CREATE INDEX ix_calendar_mailbox_sync_provider_mailbox ON calendar.mailbox_sync USING btree (provider, lower((mailbox)::text)) WHERE enabled",
	}; !equalStrings(indexes(), want) {
		t.Errorf("the mailbox indexes = %v, want %v", indexes(), want)
	}

	migrateTo(t, url, calendarMailboxNeutralVersion-1)
	if want := []string{"a:oof", "b:workingElsewhere", "c:busy", "d:tentative"}; !equalStrings(showAs(), want) {
		t.Errorf("the busy values after the down = %v, want %v", showAs(), want)
	}
	if want := []string{
		"CREATE INDEX ix_calendar_mailbox_sync_mailbox ON calendar.mailbox_sync USING btree (lower((mailbox)::text)) WHERE enabled",
	}; !equalStrings(indexes(), want) {
		t.Errorf("the mailbox indexes after the down = %v, want %v", indexes(), want)
	}
	if got := staffCollect(t, ctx, pool, "the provider column after the down", `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'calendar' AND table_name = 'mailbox_sync' AND column_name = 'provider'`); len(got) != 0 {
		t.Errorf("calendar.mailbox_sync.provider after the down = %v, want none", got)
	}
}
