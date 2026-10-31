package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarSettingsVersion is 00097_calendar_settings.sql, the calendar's first
// workspace setting: the channel invitations are sent from (integrations
// design T6).
const calendarSettingsVersion = 97

// TestCalendarSettings_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly: calendar.settings with its columns, the one
// row seeded with no channel, the single-row CHECK refusing a second, any
// channel id held (no foreign key across schemas) and cleared back to null.
func TestCalendarSettings_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarSettingsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "calendar.settings columns", `
		SELECT column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'calendar' AND table_name = 'settings'
		ORDER BY ordinal_position`)
	if want := []string{
		"id:smallint:NO:",
		"invitation_channel:uuid:YES:",
		"updated_at:timestamp with time zone:NO:",
		"revision:integer:NO:1",
	}; !equalStrings(columns, want) {
		t.Errorf("calendar.settings columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "calendar", "settings"); !equalStrings(cols, []string{"id"}) {
		t.Errorf("calendar.settings primary key = %v, want [id]", cols)
	}

	var rows int
	var channel *uuid.UUID
	var revision int32
	if err := pool.QueryRow(ctx, `SELECT count(*), max(revision) FROM calendar.settings`).Scan(&rows, &revision); err != nil {
		t.Fatalf("count the rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT invitation_channel FROM calendar.settings WHERE id = 1`).Scan(&channel); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if rows != 1 || revision != 1 || channel != nil {
		t.Errorf("rows %d revision %d channel %v, want the one row at revision 1 with no channel", rows, revision, channel)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO calendar.settings (id, updated_at) VALUES (2, now())`); err == nil {
		t.Errorf("a second settings row was accepted, want the single-row CHECK to refuse it")
	}
	if _, err := pool.Exec(ctx, `UPDATE calendar.settings SET invitation_channel = $1 WHERE id = 1`, uuid.New()); err != nil {
		t.Errorf("a channel id of another schema: %v, want it held with no foreign key", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE calendar.settings SET invitation_channel = NULL WHERE id = 1`); err != nil {
		t.Errorf("clearing the channel: %v", err)
	}
}
