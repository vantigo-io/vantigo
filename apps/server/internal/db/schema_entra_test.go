package db_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// entraBaselineVersion is 00061_entra_baseline.sql, the entra module's schema
// (docs/superpowers/specs/2026-10-19-entra-design.md, D11; the plan,
// docs/superpowers/plans/2026-10-20-entra-module.md, task 2).
const entraBaselineVersion = 61

// TestEntraBaseline_AppliesAndIsIdempotent proves the entra baseline applies,
// rolls back and re-applies cleanly, and pins what the module's queries rest
// on: the four tables with every column's type, length, nullability and
// default; the one seeded connection row and the CHECK that keeps it one; the
// CHECKs on every closed set of values and on the actor of an event; the
// unique, case-folded employee number of an account; the credentials'
// identity starting at 1001; and the events' index by time.
func TestEntraBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, entraBaselineVersion)
	// What is pinned below is 00061's own schema: the later migrations —
	// 00063's groups and the connection's two columns among them — are
	// rolled back first, and pinned by their own tests.
	migrateTo(t, url, entraBaselineVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	tables := staffCollect(t, ctx, pool, "entra tables", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'entra' ORDER BY table_name`)
	if want := []string{"accounts", "connection", "credentials", "events"}; !equalStrings(tables, want) {
		t.Errorf("entra tables = %v, want %v", tables, want)
	}

	columns := func(table string) []string {
		return staffCollect(t, ctx, pool, "entra."+table+" columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'entra' AND table_name = $1
			ORDER BY ordinal_position`, table)
	}
	for _, c := range []struct {
		table string
		want  []string
	}{
		{"connection", []string{
			"id:smallint:NO:",
			"tenant_id:uuid:YES:",
			"client_id:uuid:YES:",
			"upn_domain:character varying(253):YES:",
			"upn_pattern:character varying(40):NO:'given.family'::character varying",
			"default_usage_location:character(2):NO:'NO'::bpchar",
			"leaver_strip_licences:boolean:NO:true",
			"leaver_remove_groups:character varying(10):NO:'all'::character varying",
			"leaver_revoke_sessions:boolean:NO:true",
			"leaver_write_leave_date:boolean:NO:true",
			"write_hire_date:boolean:NO:true",
			"pass_lifetime_minutes:integer:NO:480",
			"pass_usable_once:boolean:NO:true",
			"pass_delivery:character varying(10):NO:'hr'::character varying",
			"status:character varying(12):NO:'unconfigured'::character varying",
			"status_reason:character varying(60):YES:",
			"permissions_present:ARRAY:NO:'{}'::text[]",
			"verified_domains:ARRAY:NO:'{}'::text[]",
			"organisation_name:character varying(256):YES:",
			"last_checked_at:timestamp with time zone:YES:",
			"sensitive_denied_streak:integer:NO:0",
			"version:integer:NO:1",
			"updated_at:timestamp with time zone:NO:",
			"updated_by_user_id:uuid:YES:",
		}},
		{"credentials", []string{
			"id:bigint:NO:",
			"kind:character varying(20):NO:",
			"label:character varying(100):YES:",
			"sealed:bytea:NO:",
			"expires_at:timestamp with time zone:NO:",
			"created_at:timestamp with time zone:NO:",
			"created_by_user_id:uuid:YES:",
			"last_used_at:timestamp with time zone:YES:",
			"last_failed_at:timestamp with time zone:YES:",
			"revoked_at:timestamp with time zone:YES:",
		}},
		{"accounts", []string{
			"object_id:uuid:NO:",
			"employee_number:character varying(16):NO:",
			"user_principal_name:character varying(254):NO:",
			"state:character varying(10):NO:",
			"pass_sealed:bytea:YES:",
			"pass_expires_at:timestamp with time zone:YES:",
			"pass_revealed_at:timestamp with time zone:YES:",
			"created_by_us:boolean:NO:",
			"reported:ARRAY:NO:'{}'::text[]",
			"last_error:character varying(60):YES:",
			"last_synced_at:timestamp with time zone:YES:",
			"adoption_confirmed_at:timestamp with time zone:YES:",
			"adoption_confirmed_by_user_id:uuid:YES:",
			"created_at:timestamp with time zone:NO:",
			"updated_at:timestamp with time zone:NO:",
		}},
		{"events", []string{
			"id:bigint:NO:",
			"at:timestamp with time zone:NO:",
			"actor_user_id:uuid:YES:",
			"actor_system:character varying(40):YES:",
			"action:character varying(40):NO:",
			"object_id:uuid:YES:",
			"upn:character varying(254):YES:",
			"codes:ARRAY:NO:'{}'::text[]",
		}},
	} {
		if got := columns(c.table); !equalStrings(got, c.want) {
			t.Errorf("entra.%s columns =\n%s\nwant\n%s", c.table, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}

	for table, want := range map[string]string{
		"connection": "id", "credentials": "id", "accounts": "object_id", "events": "id",
	} {
		if cols := primaryKeyColumns(t, ctx, pool, "entra", table); !equalStrings(cols, []string{want}) {
			t.Errorf("entra.%s primary key = %v, want [%s]", table, cols, want)
		}
	}

	identities := staffCollect(t, ctx, pool, "entra identity columns", `
		SELECT table_name || '.' || column_name || ':' || identity_generation || ':' || identity_start
		FROM information_schema.columns
		WHERE table_schema = 'entra' AND is_identity = 'YES'
		ORDER BY table_name`)
	if want := []string{"credentials.id:ALWAYS:1001", "events.id:ALWAYS:1"}; !equalStrings(identities, want) {
		t.Errorf("entra identity columns = %v, want %v", identities, want)
	}

	indexes := staffCollect(t, ctx, pool, "entra indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'entra' AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`)
	if want := []string{
		"CREATE INDEX ix_entra_events_at ON entra.events USING btree (at)",
		"CREATE UNIQUE INDEX ux_entra_accounts_number ON entra.accounts USING btree (upper((employee_number)::text))",
	}; !equalStrings(indexes, want) {
		t.Errorf("entra indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(want, "\n"))
	}

	checks := staffCollect(t, ctx, pool, "entra checks", `
		SELECT conrelid::regclass::text || ' ' || conname FROM pg_constraint
		WHERE connamespace = 'entra'::regnamespace AND contype = 'c'
		ORDER BY 1`)
	if want := []string{
		"entra.accounts accounts_pass_check",
		"entra.accounts accounts_state_check",
		"entra.connection connection_id_check",
		"entra.connection connection_leaver_remove_groups_check",
		"entra.connection connection_pass_delivery_check",
		"entra.connection connection_pass_lifetime_minutes_check",
		"entra.connection connection_sensitive_denied_streak_check",
		"entra.connection connection_status_check",
		"entra.connection connection_upn_pattern_check",
		"entra.credentials credentials_kind_check",
		"entra.events events_actor_check",
	}; !equalStrings(checks, want) {
		t.Errorf("entra checks =\n%s\nwant\n%s", strings.Join(checks, "\n"), strings.Join(want, "\n"))
	}
	if foreign := staffCollect(t, ctx, pool, "entra foreign keys", `
		SELECT conname FROM pg_constraint WHERE connamespace = 'entra'::regnamespace AND contype = 'f'`); len(foreign) != 0 {
		t.Errorf("entra foreign keys = %v, want none: the module references nothing outside its tables", foreign)
	}

	// The one row, seeded with the design's defaults, and no second.
	seeded := staffCollect(t, ctx, pool, "the seeded connection", `
		SELECT id || ':' || status || ':' || upn_pattern || ':' || default_usage_location || ':' || version
		FROM entra.connection`)
	if want := []string{"1:unconfigured:given.family:NO:1"}; !equalStrings(seeded, want) {
		t.Errorf("entra.connection rows = %v, want %v", seeded, want)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entra.connection (id, updated_at) VALUES (2, now())`); !checkViolationOf(err, "connection_id_check") {
		t.Errorf("a second connection row: %v, want connection_id_check", err)
	}
	for _, c := range []struct {
		name, set, constraint string
	}{
		{"an unknown pattern", "upn_pattern = 'family'", "connection_upn_pattern_check"},
		{"an unknown groups rule", "leaver_remove_groups = 'some'", "connection_leaver_remove_groups_check"},
		{"a pass of 9 minutes", "pass_lifetime_minutes = 9", "connection_pass_lifetime_minutes_check"},
		{"a pass of 43201 minutes", "pass_lifetime_minutes = 43201", "connection_pass_lifetime_minutes_check"},
		{"an unknown delivery", "pass_delivery = 'mail'", "connection_pass_delivery_check"},
		{"an unknown status", "status = 'paused'", "connection_status_check"},
		{"a negative streak", "sensitive_denied_streak = -1", "connection_sensitive_denied_streak_check"},
	} {
		if _, err := pool.Exec(ctx, `UPDATE entra.connection SET `+c.set+` WHERE id = 1`); !checkViolationOf(err, c.constraint) {
			t.Errorf("%s: %v, want %s", c.name, err, c.constraint)
		}
	}
	if _, err := pool.Exec(ctx, `
		UPDATE entra.connection SET upn_pattern = 'g.family', leaver_remove_groups = 'none', pass_lifetime_minutes = 43200,
		    pass_delivery = 'both', status = 'failing', sensitive_denied_streak = 3 WHERE id = 1`); err != nil {
		t.Errorf("admitted values: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO entra.credentials (kind, sealed, expires_at, created_at) VALUES ('certificate', '\x00', now(), now())`); !checkViolationOf(err, "credentials_kind_check") {
		t.Errorf("a certificate credential: %v, want credentials_kind_check (a later migration admits it)", err)
	}

	account := func(number, state string, pass bool) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO entra.accounts (object_id, employee_number, user_principal_name, state, pass_sealed, created_by_us, created_at, updated_at)
			VALUES ($1, $2, 'kari@example.test', $3, CASE WHEN $4::boolean THEN '\x00'::bytea END, true, now(), now())`,
			uuid.New(), number, state, pass)
		return err
	}
	if err := account("e-1", "disabled", false); err != nil {
		t.Fatalf("an account: %v", err)
	}
	if err := account("E-1", "enabled", false); !uniqueViolationOf(err, "ux_entra_accounts_number") {
		t.Errorf("a second account for E-1 in another case: %v, want ux_entra_accounts_number", err)
	}
	if err := account("E-2", "archived", false); !checkViolationOf(err, "accounts_state_check") {
		t.Errorf("an unknown state: %v, want accounts_state_check", err)
	}
	if err := account("E-3", "enabled", true); !checkViolationOf(err, "accounts_pass_check") {
		t.Errorf("a pass without its expiry: %v, want accounts_pass_check", err)
	}

	event := func(userID *uuid.UUID, system *string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO entra.events (at, actor_user_id, actor_system, action) VALUES (now(), $1, $2, 'entra.user.prepared')`,
			userID, system)
		return err
	}
	actor := uuid.New()
	system := "system:staff-accounts"
	if err := event(&actor, nil); err != nil {
		t.Errorf("an event by a user: %v", err)
	}
	if err := event(nil, &system); err != nil {
		t.Errorf("an event by the worker: %v", err)
	}
	if err := event(nil, nil); !checkViolationOf(err, "events_actor_check") {
		t.Errorf("an event without an actor: %v, want events_actor_check", err)
	}
	if err := event(&actor, &system); !checkViolationOf(err, "events_actor_check") {
		t.Errorf("an event with two actors: %v, want events_actor_check", err)
	}
}

// TestEntraBaseline_DownDropsTheSchema proves the down path removes the
// schema whole, its rows with it.
func TestEntraBaseline_DownDropsTheSchema(t *testing.T) {
	url := testdb.URL(t)
	migrateTo(t, url, entraBaselineVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO entra.credentials (kind, sealed, expires_at, created_at) VALUES ('client_secret', '\x00', now(), now())`); err != nil {
		t.Fatalf("write a credential: %v", err)
	}
	pool.Close()

	migrateTo(t, url, entraBaselineVersion-1)

	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	if got := staffCollect(t, ctx, pool, "the entra schema", `
		SELECT schema_name FROM information_schema.schemata WHERE schema_name = 'entra'`); len(got) != 0 {
		t.Errorf("schema entra after the down = %v, want it gone", got)
	}
}

// entraGroupsVersion is 00063_entra_groups.sql, the module's groups in the
// tenant and the connection's service principal and licences
// (docs/superpowers/specs/2026-10-19-entra-design.md, D10 and D14; the plan,
// docs/superpowers/plans/2026-10-20-entra-groups.md, task 2).
const entraGroupsVersion = 63

// TestEntraGroups_AppliesAndIsIdempotent proves the migration applies, rolls
// back and re-applies cleanly, and pins what delivery D's module rests on:
// entra.groups with every column's type, length, nullability and default,
// keyed by the object id, one per Staff group id, its type and state
// (present, deleted, retired) held to their values by CHECKs as the
// baseline's are, an adoption's confirmation beside it; the connection's two
// new columns, the service principal null and the licences an empty array
// until a test reads them; and the accounts' licences_assigned, empty. The
// Down drops the table and the three columns.
func TestEntraGroups_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, entraGroupsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := func(table string) []string {
		return staffCollect(t, ctx, pool, "entra."+table+" columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'entra' AND table_name = $1
			ORDER BY ordinal_position`, table)
	}
	if want := []string{
		"object_id:uuid:NO:",
		"group_id:character varying(60):NO:",
		"key:character varying(60):NO:",
		"display_name:character varying(256):NO:",
		"mail_nickname:character varying(64):NO:",
		"type:character varying(12):NO:",
		"team_created_at:timestamp with time zone:YES:",
		"created_by_us:boolean:NO:",
		"state:character varying(10):NO:",
		"last_members_at:timestamp with time zone:YES:",
		"created_at:timestamp with time zone:NO:",
		"updated_at:timestamp with time zone:NO:",
		"adoption_confirmed_at:timestamp with time zone:YES:",
		"adoption_confirmed_by_user_id:uuid:YES:",
	}; !equalStrings(columns("groups"), want) {
		t.Errorf("entra.groups columns =\n%s\nwant\n%s", strings.Join(columns("groups"), "\n"), strings.Join(want, "\n"))
	}
	// 00082's calendar_sync follows them, renamed outlook_connect by 00088
	// (a rename keeps the ordinal), pinned by
	// TestEntraOutlookConnect_AppliesAndIsIdempotent; 00085's notification
	// columns and 00088's three come after it, pinned by
	// TestEntraMailboxSubscriptions_AppliesAndIsIdempotent and the same
	// outlook test, and are left out.
	connection := slices.DeleteFunc(columns("connection"), func(c string) bool {
		return strings.HasPrefix(c, "notifications_") || strings.HasPrefix(c, "last_notification_at:") ||
			strings.HasPrefix(c, "outlook_connect_at:") || strings.HasPrefix(c, "outlook_last_error")
	})
	if tail := connection[max(len(connection)-3, 0):]; !equalStrings(tail, []string{
		"service_principal_id:uuid:YES:",
		"skus:jsonb:NO:'[]'::jsonb",
		"outlook_connect:boolean:NO:false",
	}) {
		t.Errorf("entra.connection's last columns = %v, want service_principal_id, skus and outlook_connect", tail)
	}
	accounts := columns("accounts")
	if tail := accounts[max(len(accounts)-1, 0):]; !equalStrings(tail, []string{"licences_assigned:ARRAY:NO:'{}'::text[]"}) {
		t.Errorf("entra.accounts' last column = %v, want licences_assigned", tail)
	}
	if cols := primaryKeyColumns(t, ctx, pool, "entra", "groups"); !equalStrings(cols, []string{"object_id"}) {
		t.Errorf("entra.groups primary key = %v, want [object_id]", cols)
	}
	if got := staffCollect(t, ctx, pool, "the groups' index", `
		SELECT indexdef FROM pg_indexes WHERE schemaname = 'entra' AND tablename = 'groups' AND indexname NOT LIKE '%_pkey'`); !equalStrings(got, []string{
		"CREATE UNIQUE INDEX ux_entra_groups_group_id ON entra.groups USING btree (group_id)",
	}) {
		t.Errorf("entra.groups indexes = %v", got)
	}
	if got := staffCollect(t, ctx, pool, "the connection's new columns", `
		SELECT coalesce(service_principal_id::text, 'null') || ':' || skus::text FROM entra.connection`); !equalStrings(got, []string{"null:[]"}) {
		t.Errorf("the seeded connection = %v, want no service principal and no SKUs", got)
	}

	group := func(groupID, kind, state string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO entra.groups (object_id, group_id, key, display_name, mail_nickname, type, created_by_us, state, created_at, updated_at)
			VALUES ($1, $2, 'sales', 'Sales', 'sales', $3, true, $4, now(), now())`, uuid.New(), groupID, kind, state)
		return err
	}
	if err := group("1001", "security", "present"); err != nil {
		t.Fatalf("a group: %v", err)
	}
	if err := group("1002", "microsoft365", "deleted"); err != nil {
		t.Errorf("a deleted Microsoft 365 group: %v", err)
	}
	if err := group("1001", "security", "present"); !uniqueViolationOf(err, "ux_entra_groups_group_id") {
		t.Errorf("a second group for Staff's 1001: %v, want ux_entra_groups_group_id", err)
	}
	if err := group("1003", "distribution", "present"); !checkViolationOf(err, "groups_type_check") {
		t.Errorf("an unknown type: %v, want groups_type_check", err)
	}
	if err := group("1004", "security", "archived"); !checkViolationOf(err, "groups_state_check") {
		t.Errorf("an unknown state: %v, want groups_state_check", err)
	}
	if err := group("1005", "security", "retired"); err != nil {
		t.Errorf("a retired security group: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE entra.connection SET service_principal_id = $1,
		    skus = '[{"skuId": "6fd2c87f-b296-42f0-b197-1e91e994b900", "skuPartNumber": "ENTERPRISEPACK", "enabled": 25, "consumed": 20}]'
		WHERE id = 1`, uuid.New()); err != nil {
		t.Errorf("write the service principal and the SKUs: %v", err)
	}

	migrateTo(t, url, entraGroupsVersion-1)
	if got := staffCollect(t, ctx, pool, "entra tables after the rollback", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'entra' AND table_name = 'groups'`); len(got) != 0 {
		t.Errorf("the rollback left entra.groups")
	}
	for _, c := range [][2]string{{"connection", "service_principal_id"}, {"connection", "skus"}, {"accounts", "licences_assigned"}} {
		if got := staffCollect(t, ctx, pool, c[1], `
			SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'entra' AND table_name = $1 AND column_name = $2`, c[0], c[1]); len(got) != 0 {
			t.Errorf("the rollback left entra.%s.%s", c[0], c[1])
		}
	}
}
