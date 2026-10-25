package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// staffOneToOneVersion is 00067_staff_one_to_one.sql: what the person wants,
// the Seen stamps, the competence reviews and the review interval
// (docs/superpowers/specs/2026-10-25-staff-competence-design.md, S8–S11 and
// §3.3–3.7, §3.9; the plan, docs/superpowers/plans/2026-10-25-staff-one-to-one.md,
// task 2).
const staffOneToOneVersion = 67

// TestStaffOneToOne_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins what delivery B rests on: the
// four tables with every column's type, length, nullability and default; the
// identities starting at 1001; one interest per person and competence and one
// preference per person and kind; the indexes the lists and the latest review
// read; every person referenced without a cascade; the Seen stamp's two
// columns on person_competences; the review interval on the settings row,
// twelve by default; and no CHECK, the module's validation owning the rules
// as 00042's house style has it. The Down drops the four tables and the three
// columns.
func TestStaffOneToOne_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffOneToOneVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	tables := []string{"person_interests", "person_preferences", "availability_declarations", "competence_reviews"}
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
		{"person_interests", []string{
			"id:bigint:NO:",
			"person_id:bigint:NO:",
			"competence_id:bigint:NO:",
			"seen_at:timestamp with time zone:YES:",
			"seen_by_user_id:uuid:YES:",
			"created_at:timestamp with time zone:NO:",
		}},
		{"person_preferences", []string{
			"id:bigint:NO:",
			"person_id:bigint:NO:",
			"kind:character varying(30):NO:",
			"value:jsonb:NO:",
			"weight:smallint:NO:",
			"seen_at:timestamp with time zone:YES:",
			"seen_by_user_id:uuid:YES:",
			"created_at:timestamp with time zone:NO:",
			"updated_at:timestamp with time zone:NO:",
		}},
		{"availability_declarations", []string{
			"id:bigint:NO:",
			"person_id:bigint:NO:",
			"kind:character varying(12):NO:",
			"weekday:smallint:YES:",
			"from_date:date:YES:",
			"to_date:date:YES:",
			"start_minute:smallint:YES:",
			"end_minute:smallint:YES:",
			"seen_at:timestamp with time zone:YES:",
			"seen_by_user_id:uuid:YES:",
			"created_at:timestamp with time zone:NO:",
		}},
		{"competence_reviews", []string{
			"id:bigint:NO:",
			"person_id:bigint:NO:",
			"reviewed_on:date:NO:",
			"reviewer_user_id:uuid:NO:",
			"reviewer_person_id:bigint:YES:",
			"created_at:timestamp with time zone:NO:",
		}},
	} {
		if got := columns(c.table); !equalStrings(got, c.want) {
			t.Errorf("staff.%s columns =\n%s\nwant\n%s", c.table, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}

	column := func(table, name string) []string {
		return staffCollect(t, ctx, pool, "staff."+table+"."+name, `
			SELECT data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'staff' AND table_name = $1 AND column_name = $2`, table, name)
	}
	added := []struct{ table, name, want string }{
		{"person_competences", "seen_at", "timestamp with time zone:YES:"},
		{"person_competences", "seen_by_user_id", "uuid:YES:"},
		{"settings", "competence_review_interval_months", "smallint:NO:12"},
	}
	for _, c := range added {
		if got := column(c.table, c.name); !equalStrings(got, []string{c.want}) {
			t.Errorf("staff.%s.%s = %v, want %s", c.table, c.name, got, c.want)
		}
	}
	if got := staffCollect(t, ctx, pool, "the settings row's interval", `
		SELECT competence_review_interval_months::text FROM staff.settings WHERE id = 1`); !equalStrings(got, []string{"12"}) {
		t.Errorf("the settings row's review interval = %v, want 12", got)
	}

	identities := staffCollect(t, ctx, pool, "the one-to-one identities", `
		SELECT table_name || '.' || column_name || ':' || identity_generation || ':' || identity_start
		FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = ANY($1) AND is_identity = 'YES'
		ORDER BY 1`, tables)
	if want := []string{
		"availability_declarations.id:ALWAYS:1001",
		"competence_reviews.id:ALWAYS:1001",
		"person_interests.id:ALWAYS:1001",
		"person_preferences.id:ALWAYS:1001",
	}; !equalStrings(identities, want) {
		t.Errorf("the one-to-one identities = %v, want %v", identities, want)
	}

	indexes := staffCollect(t, ctx, pool, "the one-to-one indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'staff' AND tablename = ANY($1) AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`, tables)
	if want := []string{
		"CREATE INDEX ix_availability_declarations_person ON staff.availability_declarations USING btree (person_id)",
		"CREATE INDEX ix_competence_reviews_person_reviewed ON staff.competence_reviews USING btree (person_id, reviewed_on DESC)",
		"CREATE INDEX ix_person_interests_competence ON staff.person_interests USING btree (competence_id)",
		"CREATE UNIQUE INDEX ux_person_interests_person_competence ON staff.person_interests USING btree (person_id, competence_id)",
		"CREATE UNIQUE INDEX ux_person_preferences_person_kind ON staff.person_preferences USING btree (person_id, kind)",
	}; !equalStrings(indexes, want) {
		t.Errorf("the one-to-one indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(want, "\n"))
	}

	// a: no action.
	foreign := staffCollect(t, ctx, pool, "the one-to-one foreign keys", `
		SELECT conrelid::regclass::text || '(' || a.attname || ')->' || confrelid::regclass::text || ':' || confdeltype::text
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f' AND c.conrelid = ANY(
		    SELECT ('staff.' || t)::regclass FROM unnest($1::text[]) AS t)
		ORDER BY 1`, tables)
	if want := []string{
		"staff.availability_declarations(person_id)->staff.people:a",
		"staff.competence_reviews(person_id)->staff.people:a",
		"staff.competence_reviews(reviewer_person_id)->staff.people:n",
		"staff.person_interests(competence_id)->staff.competences:a",
		"staff.person_interests(person_id)->staff.people:a",
		"staff.person_preferences(person_id)->staff.people:a",
	}; !equalStrings(foreign, want) {
		t.Errorf("the one-to-one foreign keys =\n%s\nwant\n%s", strings.Join(foreign, "\n"), strings.Join(want, "\n"))
	}
	if checks := staffCollect(t, ctx, pool, "the one-to-one checks", `
		SELECT conname FROM pg_constraint
		WHERE contype = 'c' AND conrelid = ANY(
		    SELECT ('staff.' || t)::regclass FROM unnest($1::text[]) AS t)`, tables); len(checks) != 0 {
		t.Errorf("the one-to-one checks = %v, want none: the module validates", checks)
	}

	// A person with an interest, a preference, a declaration and a review: a
	// second interest in the same competence and a second preference of the
	// same kind refused, the person kept while a row names them.
	var kari, fse, interest int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('K-1', 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`).Scan(&kari); err != nil {
		t.Fatalf("insert a person: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM staff.competences WHERE key = 'fse'`).Scan(&fse); err != nil {
		t.Fatalf("the fse seed: %v", err)
	}
	const insertInterest = `
		INSERT INTO staff.person_interests (person_id, competence_id, created_at) VALUES ($1, $2, now()) RETURNING id`
	if err := pool.QueryRow(ctx, insertInterest, kari, fse).Scan(&interest); err != nil {
		t.Fatalf("insert an interest: %v", err)
	}
	if interest != 1001 {
		t.Errorf("the first interest's id = %d, want 1001", interest)
	}
	if _, err := pool.Exec(ctx, insertInterest, kari, fse); !uniqueViolationOf(err, "ux_person_interests_person_competence") {
		t.Errorf("a second interest in the competence: %v, want ux_person_interests_person_competence", err)
	}
	const insertPreference = `
		INSERT INTO staff.person_preferences (person_id, kind, value, weight, created_at, updated_at)
		VALUES ($1, 'weekdays', '{"days": [1, 2]}', 2, now(), now())`
	if _, err := pool.Exec(ctx, insertPreference, kari); err != nil {
		t.Fatalf("insert a preference: %v", err)
	}
	if _, err := pool.Exec(ctx, insertPreference, kari); !uniqueViolationOf(err, "ux_person_preferences_person_kind") {
		t.Errorf("a second preference of the kind: %v, want ux_person_preferences_person_kind", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.availability_declarations (person_id, kind, weekday, start_minute, end_minute, created_at)
		VALUES ($1, 'unavailable', 5, 720, 1440, now())`, kari); err != nil {
		t.Fatalf("insert a declaration: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.competence_reviews (person_id, reviewed_on, reviewer_user_id, reviewer_person_id, created_at)
		VALUES ($1, DATE '2026-10-01', gen_random_uuid(), NULL, now())`, kari); err != nil {
		t.Fatalf("insert a review: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); !foreignKeyViolationOf(err, "person_interests_person_id_fkey") {
		t.Errorf("deleting a person with an interest: %v, want person_interests_person_id_fkey", err)
	}

	migrateTo(t, url, staffOneToOneVersion-1)
	if got := staffCollect(t, ctx, pool, "the one-to-one tables after the rollback", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'staff' AND table_name = ANY($1)`, tables); len(got) != 0 {
		t.Errorf("the rollback left %v", got)
	}
	for _, c := range added {
		if got := column(c.table, c.name); len(got) != 0 {
			t.Errorf("the rollback left staff.%s.%s", c.table, c.name)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); err != nil {
		t.Fatalf("clean up: %v", err)
	}
}
