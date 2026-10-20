package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// staffGroupsVersion is 00062_staff_groups.sql: Staff's groups, their
// members and owners, and their sections per provider
// (docs/superpowers/specs/2026-10-19-entra-design.md, D9, D10 and D14; the
// plan, docs/superpowers/plans/2026-10-20-entra-groups.md, task 2).
const staffGroupsVersion = 62

// TestStaffGroups_AppliesAndIsIdempotent proves the migration applies, rolls
// back and re-applies cleanly, and pins what delivery D's Staff rests on: the
// four tables with every column's type, length, nullability and default; the
// groups' identity starting at 1001 and the unique key; the members' and
// owners' indexes by person and the trait rows' due index; the person
// referenced without a cascade — a person's rows go before the person — and
// every row of a group going with the group; and no CHECK, the module's
// validation owning the closed sets as 00042's house style has it. The Down
// drops the four tables.
func TestStaffGroups_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffGroupsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := func(table string) []string {
		return staffCollect(t, ctx, pool, "staff."+table+" columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'staff' AND table_name = $1
			ORDER BY ordinal_position`, table)
	}
	for _, c := range []struct {
		table string
		want  []string
	}{
		{"groups", []string{
			"id:bigint:NO:",
			"key:character varying(60):NO:",
			"name:character varying(120):NO:",
			"description:character varying(500):YES:",
			"kind:character varying(10):NO:",
			"rule:jsonb:YES:",
			"archived_at:timestamp with time zone:YES:",
			"archived_by_user_id:uuid:YES:",
			"revision:integer:NO:1",
			"created_at:timestamp with time zone:NO:",
			"updated_at:timestamp with time zone:NO:",
		}},
		{"group_members", []string{
			"group_id:bigint:NO:",
			"person_id:bigint:NO:",
			"source:character varying(10):NO:",
			"since:date:NO:",
		}},
		{"group_owners", []string{
			"group_id:bigint:NO:",
			"person_id:bigint:NO:",
		}},
		{"group_traits", []string{
			"group_id:bigint:NO:",
			"provider:character varying(20):NO:",
			"values:jsonb:NO:",
			"enabled:boolean:NO:true",
			"disabled_at:timestamp with time zone:YES:",
			"external_id:character varying(200):YES:",
			"state:character varying(20):NO:",
			"last_values_hash:bytea:YES:",
			"last_members_hash:bytea:YES:",
			"last_error:character varying(200):YES:",
			"last_error_field:character varying(40):YES:",
			"synced_at:timestamp with time zone:YES:",
			"retired_at:timestamp with time zone:YES:",
			"attempts:integer:NO:0",
			"next_attempt_at:timestamp with time zone:YES:",
			"lease_id:uuid:YES:",
			"lease_until:timestamp with time zone:YES:",
		}},
	} {
		if got := columns(c.table); !equalStrings(got, c.want) {
			t.Errorf("staff.%s columns =\n%s\nwant\n%s", c.table, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}

	for table, want := range map[string][]string{
		"groups":        {"id"},
		"group_members": {"group_id", "person_id"},
		"group_owners":  {"group_id", "person_id"},
		"group_traits":  {"group_id", "provider"},
	} {
		if cols := primaryKeyColumns(t, ctx, pool, "staff", table); !equalStrings(cols, want) {
			t.Errorf("staff.%s primary key = %v, want %v", table, cols, want)
		}
	}

	identities := staffCollect(t, ctx, pool, "the groups' identity", `
		SELECT column_name || ':' || identity_generation || ':' || identity_start
		FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = 'groups' AND is_identity = 'YES'`)
	if want := []string{"id:ALWAYS:1001"}; !equalStrings(identities, want) {
		t.Errorf("staff.groups identity = %v, want %v", identities, want)
	}

	indexes := staffCollect(t, ctx, pool, "the groups' indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'staff' AND tablename IN ('groups', 'group_members', 'group_owners', 'group_traits')
		  AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`)
	if want := []string{
		"CREATE INDEX ix_group_members_person ON staff.group_members USING btree (person_id)",
		"CREATE INDEX ix_group_owners_person ON staff.group_owners USING btree (person_id)",
		"CREATE INDEX ix_group_traits_due ON staff.group_traits USING btree (provider, next_attempt_at)",
		"CREATE UNIQUE INDEX ux_groups_key ON staff.groups USING btree (key)",
	}; !equalStrings(indexes, want) {
		t.Errorf("the groups' indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(want, "\n"))
	}

	// a: no action, c: cascade.
	foreign := staffCollect(t, ctx, pool, "the groups' foreign keys", `
		SELECT conrelid::regclass::text || '(' || a.attname || ')->' || confrelid::regclass::text || ':' || confdeltype::text
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f'
		  AND conrelid::regclass::text IN ('staff.groups', 'staff.group_members', 'staff.group_owners', 'staff.group_traits')
		ORDER BY 1`)
	if want := []string{
		"staff.group_members(group_id)->staff.groups:c",
		"staff.group_members(person_id)->staff.people:a",
		"staff.group_owners(group_id)->staff.groups:c",
		"staff.group_owners(person_id)->staff.people:a",
		"staff.group_traits(group_id)->staff.groups:c",
	}; !equalStrings(foreign, want) {
		t.Errorf("the groups' foreign keys =\n%s\nwant\n%s", strings.Join(foreign, "\n"), strings.Join(want, "\n"))
	}
	if checks := staffCollect(t, ctx, pool, "the groups' checks", `
		SELECT conname FROM pg_constraint
		WHERE contype = 'c'
		  AND conrelid::regclass::text IN ('staff.groups', 'staff.group_members', 'staff.group_owners', 'staff.group_traits')`); len(checks) != 0 {
		t.Errorf("the groups' checks = %v, want none: the module validates", checks)
	}

	// A group with a member, an owner and a trait row; a second group with
	// the key refused; the person kept while a row names them; the group's
	// rows going with it.
	var kari, group int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('K-1', 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`).Scan(&kari); err != nil {
		t.Fatalf("insert a person: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.groups (key, name, kind, rule, created_at, updated_at)
		VALUES ('sales', 'Sales', 'derived', '{"kind": "department", "id": 1001, "includeBelow": true}', now(), now())
		RETURNING id`).Scan(&group); err != nil {
		t.Fatalf("insert a group: %v", err)
	}
	if group != 1001 {
		t.Errorf("the first group's id = %d, want 1001", group)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.groups (key, name, kind, created_at, updated_at) VALUES ('sales', 'Sales again', 'listed', now(), now())`); !uniqueViolationOf(err, "ux_groups_key") {
		t.Errorf("a second group with the key: %v, want ux_groups_key", err)
	}
	for _, sql := range []string{
		`INSERT INTO staff.group_members (group_id, person_id, source, since) VALUES ($1, $2, 'derived', current_date)`,
		`INSERT INTO staff.group_owners (group_id, person_id) VALUES ($1, $2)`,
	} {
		if _, err := pool.Exec(ctx, sql, group, kari); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.group_traits (group_id, provider, values, state) VALUES ($1, 'entra', '{"type": "security"}', 'pending')`, group); err != nil {
		t.Fatalf("a trait row: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "the trait row's defaults", `
		SELECT enabled || ':' || attempts || ':' || (values->>'type') FROM staff.group_traits WHERE group_id = $1`, group); !equalStrings(got, []string{"true:0:security"}) {
		t.Errorf("the trait row = %v, want enabled, no attempt, the values as written", got)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); !foreignKeyViolationOf(err, "group_members_person_id_fkey") &&
		!foreignKeyViolationOf(err, "group_owners_person_id_fkey") {
		t.Errorf("deleting a member: %v, want a foreign key violation", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.groups WHERE id = $1`, group); err != nil {
		t.Fatalf("delete the group: %v", err)
	}
	for _, table := range []string{"group_members", "group_owners", "group_traits"} {
		if got := staffCollect(t, ctx, pool, table, `SELECT group_id::text FROM staff.`+table); len(got) != 0 {
			t.Errorf("staff.%s after the group's delete = %v, want its rows gone", table, got)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); err != nil {
		t.Errorf("delete the person: %v", err)
	}

	migrateTo(t, url, staffGroupsVersion-1)
	if got := staffCollect(t, ctx, pool, "the groups' tables after the rollback", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'staff' AND table_name IN ('groups', 'group_members', 'group_owners', 'group_traits')`); len(got) != 0 {
		t.Errorf("the rollback left %v", got)
	}
}
