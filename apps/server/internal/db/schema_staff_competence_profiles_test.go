package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// staffCompetenceProfilesVersion is 00068_staff_competence_profiles.sql: the
// competence profiles, their items, a person's assignments and a department's
// default (docs/superpowers/specs/2026-10-25-staff-competence-design.md,
// S12–S14 and §3.8; the plan,
// docs/superpowers/plans/2026-10-25-staff-competence-profiles.md, task 2).
const staffCompetenceProfilesVersion = 68

// TestStaffCompetenceProfiles_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what delivery C rests
// on: the three tables with every column's type, length, nullability and
// default; the identities starting at 1001; one profile per key, one item per
// profile and competence and one assignment per person and profile; the
// indexes competence_in_use and the assignment lists read; every reference
// without a cascade; the department's default profile and its index; and no
// CHECK, the module's validation owning the rules as 00042's house style has
// it. The Down drops the column and the three tables.
func TestStaffCompetenceProfiles_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffCompetenceProfilesVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	tables := []string{"competence_profiles", "competence_profile_items", "person_profiles"}
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
		{"competence_profiles", []string{
			"id:bigint:NO:",
			"key:character varying(40):NO:",
			"name_en:character varying(100):NO:",
			"name_nb:character varying(100):NO:",
			"archived_at:timestamp with time zone:YES:",
			"revision:integer:NO:1",
			"created_at:timestamp with time zone:NO:",
			"updated_at:timestamp with time zone:NO:",
		}},
		{"competence_profile_items", []string{
			"id:bigint:NO:",
			"profile_id:bigint:NO:",
			"competence_id:bigint:NO:",
			"min_level:smallint:YES:",
			"must_be_valid:boolean:NO:true",
			"created_at:timestamp with time zone:NO:",
		}},
		{"person_profiles", []string{
			"id:bigint:NO:",
			"person_id:bigint:NO:",
			"profile_id:bigint:NO:",
			"created_at:timestamp with time zone:NO:",
		}},
	} {
		if got := columns(c.table); !equalStrings(got, c.want) {
			t.Errorf("staff.%s columns =\n%s\nwant\n%s", c.table, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}

	department := func() []string {
		return staffCollect(t, ctx, pool, "staff.departments.competence_profile_id", `
			SELECT data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'staff' AND table_name = 'departments' AND column_name = 'competence_profile_id'`)
	}
	if got := department(); !equalStrings(got, []string{"bigint:YES:"}) {
		t.Errorf("staff.departments.competence_profile_id = %v, want bigint:YES:", got)
	}

	identities := staffCollect(t, ctx, pool, "the profile identities", `
		SELECT table_name || '.' || column_name || ':' || identity_generation || ':' || identity_start
		FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = ANY($1) AND is_identity = 'YES'
		ORDER BY 1`, tables)
	if want := []string{
		"competence_profile_items.id:ALWAYS:1001",
		"competence_profiles.id:ALWAYS:1001",
		"person_profiles.id:ALWAYS:1001",
	}; !equalStrings(identities, want) {
		t.Errorf("the profile identities = %v, want %v", identities, want)
	}

	indexes := staffCollect(t, ctx, pool, "the profile indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'staff' AND (tablename = ANY($1) OR indexname = 'ix_departments_competence_profile')
		    AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`, tables)
	if want := []string{
		"CREATE INDEX ix_competence_profile_items_competence ON staff.competence_profile_items USING btree (competence_id)",
		"CREATE INDEX ix_departments_competence_profile ON staff.departments USING btree (competence_profile_id)",
		"CREATE INDEX ix_person_profiles_profile ON staff.person_profiles USING btree (profile_id)",
		"CREATE UNIQUE INDEX ux_competence_profile_items_profile_competence ON staff.competence_profile_items USING btree (profile_id, competence_id)",
		"CREATE UNIQUE INDEX ux_competence_profiles_key ON staff.competence_profiles USING btree (key)",
		"CREATE UNIQUE INDEX ux_person_profiles_person_profile ON staff.person_profiles USING btree (person_id, profile_id)",
	}; !equalStrings(indexes, want) {
		t.Errorf("the profile indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(want, "\n"))
	}

	// a: no action.
	foreign := staffCollect(t, ctx, pool, "the profile foreign keys", `
		SELECT conrelid::regclass::text || '(' || a.attname || ')->' || confrelid::regclass::text || ':' || confdeltype::text
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f' AND (c.conrelid = ANY(
		    SELECT ('staff.' || t)::regclass FROM unnest($1::text[]) AS t)
		    OR c.confrelid = 'staff.competence_profiles'::regclass)
		ORDER BY 1`, tables)
	if want := []string{
		"staff.competence_profile_items(competence_id)->staff.competences:a",
		"staff.competence_profile_items(profile_id)->staff.competence_profiles:a",
		"staff.departments(competence_profile_id)->staff.competence_profiles:a",
		"staff.person_profiles(person_id)->staff.people:a",
		"staff.person_profiles(profile_id)->staff.competence_profiles:a",
	}; !equalStrings(foreign, want) {
		t.Errorf("the profile foreign keys =\n%s\nwant\n%s", strings.Join(foreign, "\n"), strings.Join(want, "\n"))
	}
	if checks := staffCollect(t, ctx, pool, "the profile checks", `
		SELECT conname FROM pg_constraint
		WHERE contype = 'c' AND conrelid = ANY(
		    SELECT ('staff.' || t)::regclass FROM unnest($1::text[]) AS t)`, tables); len(checks) != 0 {
		t.Errorf("the profile checks = %v, want none: the module validates", checks)
	}

	// A profile with an item, assigned to a person and made a department's
	// default: a second profile with the key, a second item on the competence
	// and a second assignment of the profile refused, the profile kept while
	// an assignment names it.
	var kari, fse, profile, dept int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('K-1', 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`).Scan(&kari); err != nil {
		t.Fatalf("insert a person: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM staff.competences WHERE key = 'fse'`).Scan(&fse); err != nil {
		t.Fatalf("the fse seed: %v", err)
	}
	const insertProfile = `
		INSERT INTO staff.competence_profiles (key, name_en, name_nb, created_at, updated_at)
		VALUES ('field-electrician', 'Field electrician', 'Montør', now(), now()) RETURNING id`
	if err := pool.QueryRow(ctx, insertProfile).Scan(&profile); err != nil {
		t.Fatalf("insert a profile: %v", err)
	}
	if profile != 1001 {
		t.Errorf("the first profile's id = %d, want 1001", profile)
	}
	if _, err := pool.Exec(ctx, insertProfile); !uniqueViolationOf(err, "ux_competence_profiles_key") {
		t.Errorf("a second profile with the key: %v, want ux_competence_profiles_key", err)
	}
	const insertItem = `
		INSERT INTO staff.competence_profile_items (profile_id, competence_id, created_at) VALUES ($1, $2, now())`
	if _, err := pool.Exec(ctx, insertItem, profile, fse); err != nil {
		t.Fatalf("insert an item: %v", err)
	}
	if _, err := pool.Exec(ctx, insertItem, profile, fse); !uniqueViolationOf(err, "ux_competence_profile_items_profile_competence") {
		t.Errorf("a second item on the competence: %v, want ux_competence_profile_items_profile_competence", err)
	}
	const insertAssignment = `
		INSERT INTO staff.person_profiles (person_id, profile_id, created_at) VALUES ($1, $2, now())`
	if _, err := pool.Exec(ctx, insertAssignment, kari, profile); err != nil {
		t.Fatalf("insert an assignment: %v", err)
	}
	if _, err := pool.Exec(ctx, insertAssignment, kari, profile); !uniqueViolationOf(err, "ux_person_profiles_person_profile") {
		t.Errorf("a second assignment of the profile: %v, want ux_person_profiles_person_profile", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.departments (code, name, competence_profile_id, created_at, updated_at)
		VALUES ('OPS', 'Operations', $1, now(), now()) RETURNING id`, profile).Scan(&dept); err != nil {
		t.Fatalf("insert a department with a default: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); !foreignKeyViolationOf(err, "person_profiles_person_id_fkey") {
		t.Errorf("deleting a person with an assignment: %v, want person_profiles_person_id_fkey", err)
	}

	migrateTo(t, url, staffCompetenceProfilesVersion-1)
	if got := staffCollect(t, ctx, pool, "the profile tables after the rollback", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'staff' AND table_name = ANY($1)`, tables); len(got) != 0 {
		t.Errorf("the rollback left %v", got)
	}
	if got := department(); len(got) != 0 {
		t.Errorf("the rollback left staff.departments.competence_profile_id")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.departments WHERE id = $1`, dept); err != nil {
		t.Fatalf("clean up the department: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); err != nil {
		t.Fatalf("clean up: %v", err)
	}
}
