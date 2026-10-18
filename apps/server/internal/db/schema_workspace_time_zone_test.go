package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The three migrations of the time zones design
// (docs/superpowers/specs/2026-10-19-time-zones-design.md, D2, D3 and D5):
// 00052 gives the workspace its business time zone and the user a personal
// one, 00053 and 00054 drop Staff's and Expenses' own.
const (
	workspaceTimeZoneVersion = 52
	staffTimeZoneVersion     = 53
	expensesTimeZoneVersion  = 54
)

// TestWorkspaceTimeZone_AppliesAndIsIdempotent proves the three apply, roll
// back and re-apply cleanly, and pins what the business calendar rests on:
// system_settings.business_time_zone, NOT NULL with Europe/Oslo as its
// default — the zone an installation that never sets it runs on, and what a
// missing singleton row means to the calendar — users.time_zone nullable,
// NULL meaning "follow the workspace", and no time_zone column left on
// staff.settings or expenses.settings.
func TestWorkspaceTimeZone_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	for _, version := range []int64{workspaceTimeZoneVersion, staffTimeZoneVersion, expensesTimeZoneVersion} {
		applyUpDownUp(t, url, version)
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	column := func(schema, table, name string) []string {
		return staffCollect(t, ctx, pool, schema+"."+table+"."+name, `
			SELECT data_type || ':' || is_nullable || ':' || coalesce(column_default, '') FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`, schema, table, name)
	}
	if got := column("identity", "system_settings", "business_time_zone"); !equalStrings(got, []string{"character varying:NO:'Europe/Oslo'::character varying"}) {
		t.Errorf("identity.system_settings.business_time_zone = %v, want varchar, NOT NULL, default Europe/Oslo", got)
	}
	if got := column("identity", "users", "time_zone"); !equalStrings(got, []string{"character varying:YES:"}) {
		t.Errorf("identity.users.time_zone = %v, want varchar, nullable, no default", got)
	}
	for _, schema := range []string{"staff", "expenses"} {
		if got := column(schema, "settings", "time_zone"); len(got) != 0 {
			t.Errorf("%s.settings.time_zone = %v, want the column gone — the zone is the workspace's", schema, got)
		}
	}

	// The row the business calendar reads may not exist yet; when it does,
	// a plain insert takes the default.
	var zone string
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.system_settings (id, maintenance_enabled, updated_at) VALUES (1, false, now())
		ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatalf("ensure the settings row: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT business_time_zone FROM identity.system_settings WHERE id = 1`).Scan(&zone); err != nil {
		t.Fatalf("read the business time zone: %v", err)
	}
	if zone != "Europe/Oslo" {
		t.Errorf("a fresh row's business_time_zone = %q, want the default Europe/Oslo", zone)
	}
}
