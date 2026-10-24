package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// staffCompetenceVersion is 00066_staff_competence.sql: the competence
// catalogue and a person's competences
// (docs/superpowers/specs/2026-10-25-staff-competence-design.md, S1–S7 and
// §3.1–3.3; the plan, docs/superpowers/plans/2026-10-25-staff-competence.md,
// task 2).
const staffCompetenceVersion = 66

// TestStaffCompetence_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins what delivery A rests on: the
// three tables with every column's type, length, nullability and default;
// the identities starting at 1001; the unique keys, the one current row per
// person and competence, and the worker's index on the expiring rows; the
// person referenced without a cascade and the evidence cleared when its
// document goes; the five categories and fifteen certificates seeded; and no
// CHECK, the module's validation owning the closed sets as 00042's house
// style has it. The Down drops the three tables.
func TestStaffCompetence_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffCompetenceVersion)

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
		{"competence_categories", []string{
			"id:bigint:NO:",
			"key:character varying(40):NO:",
			"name_en:character varying(100):NO:",
			"name_nb:character varying(100):NO:",
			"seeded:boolean:NO:false",
			"archived_at:timestamp with time zone:YES:",
			"sort:integer:NO:",
			"revision:integer:NO:1",
			"created_at:timestamp with time zone:NO:",
			"updated_at:timestamp with time zone:NO:",
		}},
		{"competences", []string{
			"id:bigint:NO:",
			"category_id:bigint:NO:",
			"key:character varying(60):NO:",
			"name_en:character varying(100):NO:",
			"name_nb:character varying(100):NO:",
			"kind:character varying(12):NO:",
			"has_level:boolean:NO:",
			"validity_months:smallint:YES:",
			"reminder_lead_days:smallint:YES:",
			"issuer_hint:character varying(200):YES:",
			"basis:character varying(200):YES:",
			"seeded:boolean:NO:false",
			"hidden:boolean:NO:false",
			"archived_at:timestamp with time zone:YES:",
			"sort:integer:NO:",
			"revision:integer:NO:1",
			"created_at:timestamp with time zone:NO:",
			"updated_at:timestamp with time zone:NO:",
		}},
		{"person_competences", []string{
			"id:bigint:NO:",
			"person_id:bigint:NO:",
			"competence_id:bigint:NO:",
			"level:smallint:YES:",
			"issued_on:date:YES:",
			"valid_from:date:NO:",
			"valid_to:date:YES:",
			"issuer:character varying(200):YES:",
			"reference:character varying(100):YES:",
			"evidence_document_id:bigint:YES:",
			"source:character varying(10):NO:",
			"verified_at:timestamp with time zone:YES:",
			"verified_by_user_id:uuid:YES:",
			"verified_basis:character varying(10):YES:",
			"verified_note:character varying(200):YES:",
			"superseded_by_id:bigint:YES:",
			"expiring_notified_at:timestamp with time zone:YES:",
			"expired_notified_at:timestamp with time zone:YES:",
			"revision:integer:NO:1",
			"created_at:timestamp with time zone:NO:",
			"updated_at:timestamp with time zone:NO:",
		}},
	} {
		if got := columns(c.table); !equalStrings(got, c.want) {
			t.Errorf("staff.%s columns =\n%s\nwant\n%s", c.table, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}

	identities := staffCollect(t, ctx, pool, "the competence identities", `
		SELECT table_name || '.' || column_name || ':' || identity_generation || ':' || identity_start
		FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name IN ('competence_categories', 'competences', 'person_competences')
		  AND is_identity = 'YES'
		ORDER BY 1`)
	if want := []string{
		"competence_categories.id:ALWAYS:1001",
		"competences.id:ALWAYS:1001",
		"person_competences.id:ALWAYS:1001",
	}; !equalStrings(identities, want) {
		t.Errorf("the competence identities = %v, want %v", identities, want)
	}

	indexes := staffCollect(t, ctx, pool, "the competence indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'staff' AND tablename IN ('competence_categories', 'competences', 'person_competences')
		  AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`)
	if want := []string{
		"CREATE INDEX ix_competences_category ON staff.competences USING btree (category_id)",
		"CREATE INDEX ix_person_competences_competence ON staff.person_competences USING btree (competence_id)",
		"CREATE INDEX ix_person_competences_person ON staff.person_competences USING btree (person_id)",
		"CREATE INDEX ix_person_competences_valid_to ON staff.person_competences USING btree (valid_to) WHERE ((valid_to IS NOT NULL) AND (superseded_by_id IS NULL))",
		"CREATE UNIQUE INDEX ux_competence_categories_key ON staff.competence_categories USING btree (key)",
		"CREATE UNIQUE INDEX ux_competences_key ON staff.competences USING btree (key)",
		"CREATE UNIQUE INDEX ux_person_competences_current ON staff.person_competences USING btree (person_id, competence_id) WHERE (superseded_by_id IS NULL)",
	}; !equalStrings(indexes, want) {
		t.Errorf("the competence indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(want, "\n"))
	}

	// a: no action, n: set null.
	foreign := staffCollect(t, ctx, pool, "the competence foreign keys", `
		SELECT conrelid::regclass::text || '(' || a.attname || ')->' || confrelid::regclass::text || ':' || confdeltype::text
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f'
		  AND conrelid::regclass::text IN ('staff.competence_categories', 'staff.competences', 'staff.person_competences')
		ORDER BY 1`)
	if want := []string{
		"staff.competences(category_id)->staff.competence_categories:a",
		"staff.person_competences(competence_id)->staff.competences:a",
		"staff.person_competences(evidence_document_id)->staff.documents:n",
		"staff.person_competences(person_id)->staff.people:a",
		"staff.person_competences(superseded_by_id)->staff.person_competences:a",
	}; !equalStrings(foreign, want) {
		t.Errorf("the competence foreign keys =\n%s\nwant\n%s", strings.Join(foreign, "\n"), strings.Join(want, "\n"))
	}
	if checks := staffCollect(t, ctx, pool, "the competence checks", `
		SELECT conname FROM pg_constraint
		WHERE contype = 'c'
		  AND conrelid::regclass::text IN ('staff.competence_categories', 'staff.competences', 'staff.person_competences')`); len(checks) != 0 {
		t.Errorf("the competence checks = %v, want none: the module validates", checks)
	}

	// The seeds: five categories in order, fifteen certificates under
	// sertifikat, every one seeded, without a level, with its lead by S7's
	// rule.
	if got := staffCollect(t, ctx, pool, "the seeded categories", `
		SELECT key || ':' || name_en || ':' || name_nb || ':' || seeded || ':' || sort
		FROM staff.competence_categories ORDER BY sort`); !equalStrings(got, []string{
		"fag:Trade:Fag:true:1",
		"sertifikat:Certificate:Sertifikat:true:2",
		"sprak:Language:Språk:true:3",
		"verktoy:Tool:Verktøy:true:4",
		"domene:Domain:Domene:true:5",
	}) {
		t.Errorf("the seeded categories = %v", got)
	}
	if got := staffCollect(t, ctx, pool, "the seeded competences", `
		SELECT count(*)::text FROM staff.competences c
		JOIN staff.competence_categories k ON k.id = c.category_id
		WHERE k.key = 'sertifikat' AND c.kind = 'certificate' AND NOT c.has_level AND c.seeded AND NOT c.hidden
		  AND c.archived_at IS NULL AND c.basis IS NOT NULL`); !equalStrings(got, []string{"15"}) {
		t.Errorf("the seeded certificates = %v, want 15", got)
	}
	if got := staffCollect(t, ctx, pool, "the seeded leads", `
		SELECT key || ':' || coalesce(validity_months::text, '-') || ':' || coalesce(reminder_lead_days::text, '-')
		FROM staff.competences WHERE key IN ('fagbrev', 'fse', 'hms-kort', 'forerkort-b', 'arbeidsvarsling') ORDER BY sort`); !equalStrings(got, []string{
		"fagbrev:-:-",
		"fse:12:30",
		"hms-kort:24:60",
		"forerkort-b:180:180",
		"arbeidsvarsling:60:180",
	}) {
		t.Errorf("the seeded validities and leads = %v", got)
	}

	// A person holding a certificate with its evidence: a second current row
	// for it refused, a renewal beside a superseded one allowed, the evidence
	// reference cleared when the document goes, the person kept while a row
	// names them.
	var kari, fse, doc, first, second int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('K-1', 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`).Scan(&kari); err != nil {
		t.Fatalf("insert a person: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM staff.competences WHERE key = 'fse'`).Scan(&fse); err != nil {
		t.Fatalf("the fse seed: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.documents (person_id, category_id, object_key, file_name, content_type, size_bytes,
		    uploaded_by_user_id, created_at, updated_at)
		SELECT $1, id, 'people/1/fse.pdf', 'fse.pdf', 'application/pdf', 10, gen_random_uuid(), now(), now()
		FROM staff.document_categories WHERE key = 'certificate'
		RETURNING id`, kari).Scan(&doc); err != nil {
		t.Fatalf("insert a document: %v", err)
	}
	const insertRow = `
		INSERT INTO staff.person_competences (person_id, competence_id, valid_from, valid_to, evidence_document_id,
		    source, created_at, updated_at)
		VALUES ($1, $2, DATE '2026-01-01', DATE '2026-12-31', $3, 'self', now(), now()) RETURNING id`
	if err := pool.QueryRow(ctx, insertRow, kari, fse, doc).Scan(&first); err != nil {
		t.Fatalf("insert a person's competence: %v", err)
	}
	if first != 1001 {
		t.Errorf("the first row's id = %d, want 1001", first)
	}
	if _, err := pool.Exec(ctx, insertRow, kari, fse, nil); !uniqueViolationOf(err, "ux_person_competences_current") {
		t.Errorf("a second current row: %v, want ux_person_competences_current", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE staff.person_competences SET superseded_by_id = id WHERE id = $1`, first); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if err := tx.QueryRow(ctx, insertRow, kari, fse, nil).Scan(&second); err != nil {
		t.Fatalf("the renewal beside a superseded row: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE staff.person_competences SET superseded_by_id = $2 WHERE id = $1`, first, second); err != nil {
		t.Fatalf("point the old row at the new: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.documents WHERE id = $1`, doc); err != nil {
		t.Fatalf("delete the evidence: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "the evidence after its delete", `
		SELECT coalesce(evidence_document_id::text, 'null') FROM staff.person_competences WHERE id = $1`, first); !equalStrings(got, []string{"null"}) {
		t.Errorf("the evidence reference = %v, want it cleared", got)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); !foreignKeyViolationOf(err, "person_competences_person_id_fkey") {
		t.Errorf("deleting a holder: %v, want person_competences_person_id_fkey", err)
	}
	// The chain goes in one statement, the superseded row with the row it
	// points at.
	if _, err := pool.Exec(ctx, `DELETE FROM staff.person_competences WHERE person_id = $1`, kari); err != nil {
		t.Fatalf("delete the chain: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff.people WHERE id = $1`, kari); err != nil {
		t.Errorf("delete the person: %v", err)
	}

	migrateTo(t, url, staffCompetenceVersion-1)
	if got := staffCollect(t, ctx, pool, "the competence tables after the rollback", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'staff' AND table_name IN ('competence_categories', 'competences', 'person_competences')`); len(got) != 0 {
		t.Errorf("the rollback left %v", got)
	}
}
