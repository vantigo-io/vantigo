package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The staff absence schema (00047_staff_absence.sql; staff foundation design
// §3.5, S8–S12, delivery B): the absence types with their seeds, the holiday
// schemes with the statutory one, the holiday adjustments, the sick cases and
// their milestones, the absences, and the three columns delivery B adds to
// tables 00042 and 00043 created — employments.holiday_scheme_id,
// documents.case_id, settings.auto_carry_over_since and
// people.self_cert_withdrawn_from. Its own file, as
// 00042's and 00043's tests are.

// staffAbsenceVersion is 00047's goose version. 00046 is not this branch's
// (the Invoices delivery planned beside it claims it); goose takes versions
// as numbers, gaps and all, so 00045 → 00047 applies and rolls back as any
// two neighbours do.
const staffAbsenceVersion = 47

// staffAbsenceTables are the tables 00047 creates.
var staffAbsenceTables = []string{
	"absence_types", "absences", "holiday_adjustments", "holiday_schemes", "sick_case_milestones", "sick_cases",
}

// TestStaffAbsence_AppliesAndIsIdempotent proves 00047_staff_absence.sql
// applies, rolls back and re-applies cleanly, and pins what delivery B rests
// on: every column of the six tables and of the three it adds elsewhere; the
// foreign keys; the indexes — the unique key of a type and of a scheme's
// name, at most one default scheme (as ux_business_units_default does for the
// business units), at most one carry-over per person and year it comes from,
// one milestone per kind per case, and the partial index of the requested
// absences; every identity starting at 1001; the type seeds with their flags
// and a-melding descriptions, the reserved hidden absent among them; the
// statutory scheme, the default; and no CHECK constraint (house style). The
// Down removes the six tables and the three columns and leaves 00043's schema
// as it was.
func TestStaffAbsence_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, staffAbsenceVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "columns", `
		SELECT table_name || '.' || column_name || ':' || data_type || ':'
		    || coalesce(character_maximum_length::text, '') || ':' || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'staff' AND (table_name = ANY($1)
		   OR (table_name, column_name) IN (('employments', 'holiday_scheme_id'), ('documents', 'case_id'), ('settings', 'auto_carry_over_since'), ('people', 'self_cert_withdrawn_from')))
		ORDER BY 1`, staffAbsenceTables)
	wantColumns := []string{
		"absence_types.allows_percent:boolean::NO:false",
		"absence_types.amelding_leave:character varying:40:YES:",
		"absence_types.created_at:timestamp with time zone::NO:",
		"absence_types.draws_holiday:boolean::NO:false",
		"absence_types.hidden:boolean::NO:false",
		"absence_types.id:bigint::NO:",
		"absence_types.key:character varying:40:NO:",
		"absence_types.kind:character varying:20:NO:",
		"absence_types.name_en:character varying:100:NO:",
		"absence_types.name_nb:character varying:100:NO:",
		"absence_types.needs_decision:boolean::NO:",
		"absence_types.paid:boolean::NO:",
		"absence_types.revision:integer::NO:1",
		"absence_types.seeded:boolean::NO:false",
		"absence_types.sort:integer::NO:",
		"absence_types.unit:character varying:10:NO:",
		"absence_types.updated_at:timestamp with time zone::NO:",
		"absence_types.visibility:character varying:10:NO:'absent'::character varying",
		"absences.case_id:bigint::YES:",
		"absences.created_at:timestamp with time zone::NO:",
		"absences.decided_at:timestamp with time zone::YES:",
		"absences.decided_by_user_id:uuid::YES:",
		"absences.decision_note:character varying:500:YES:",
		"absences.degree_percent:numeric::YES:",
		"absences.first_day:date::NO:",
		"absences.hours:numeric::YES:",
		"absences.id:bigint::NO:",
		"absences.last_day:date::NO:",
		"absences.note:character varying:500:YES:",
		"absences.percent:numeric::NO:100",
		"absences.person_id:bigint::NO:",
		"absences.requested_by_user_id:uuid::NO:",
		"absences.revision:integer::NO:1",
		"absences.self_approved:boolean::NO:false",
		"absences.status:character varying:12:NO:",
		"absences.type_id:bigint::NO:",
		"absences.updated_at:timestamp with time zone::NO:",
		"documents.case_id:bigint::YES:",
		"employments.holiday_scheme_id:bigint::YES:",
		"holiday_adjustments.by_user_id:uuid::NO:",
		"holiday_adjustments.created_at:timestamp with time zone::NO:",
		"holiday_adjustments.days:numeric::NO:",
		"holiday_adjustments.from_year:smallint::YES:",
		"holiday_adjustments.id:bigint::NO:",
		"holiday_adjustments.note:character varying:500:YES:",
		"holiday_adjustments.person_id:bigint::NO:",
		"holiday_adjustments.reason:character varying:20:NO:",
		"holiday_adjustments.updated_at:timestamp with time zone::NO:",
		"holiday_adjustments.year:smallint::NO:",
		"holiday_schemes.after_september_virkedager:smallint::NO:6",
		"holiday_schemes.archived_at:timestamp with time zone::YES:",
		"holiday_schemes.created_at:timestamp with time zone::NO:",
		"holiday_schemes.entitlement_virkedager:smallint::NO:",
		"holiday_schemes.extra_from_60_virkedager:smallint::NO:",
		"holiday_schemes.id:bigint::NO:",
		"holiday_schemes.is_default:boolean::NO:false",
		"holiday_schemes.main_period_virkedager:smallint::NO:18",
		"holiday_schemes.max_carry_over_virkedager:smallint::NO:12",
		"holiday_schemes.name:character varying:100:NO:",
		"holiday_schemes.revision:integer::NO:1",
		"holiday_schemes.seeded:boolean::NO:false",
		"holiday_schemes.updated_at:timestamp with time zone::NO:",
		"people.self_cert_withdrawn_from:date::YES:",
		"settings.auto_carry_over_since:date::YES:",
		"sick_case_milestones.case_id:bigint::NO:",
		"sick_case_milestones.created_at:timestamp with time zone::NO:",
		"sick_case_milestones.done_at:timestamp with time zone::YES:",
		"sick_case_milestones.done_by_user_id:uuid::YES:",
		"sick_case_milestones.due_date:date::YES:",
		"sick_case_milestones.id:bigint::NO:",
		"sick_case_milestones.kind:character varying:30:NO:",
		"sick_case_milestones.note:character varying:2000:YES:",
		"sick_case_milestones.revision:integer::NO:1",
		"sick_case_milestones.updated_at:timestamp with time zone::NO:",
		"sick_case_milestones.waived:boolean::NO:false",
		"sick_case_milestones.waived_by_user_id:uuid::YES:",
		"sick_case_milestones.waived_reason:character varying:500:YES:",
		"sick_cases.accommodation_note:character varying:2000:YES:",
		"sick_cases.closed_at:timestamp with time zone::YES:",
		"sick_cases.created_at:timestamp with time zone::NO:",
		"sick_cases.first_day:date::NO:",
		"sick_cases.id:bigint::NO:",
		"sick_cases.last_day:date::NO:",
		"sick_cases.max_benefit_estimate:date::YES:",
		"sick_cases.person_id:bigint::NO:",
		"sick_cases.revision:integer::NO:1",
		"sick_cases.updated_at:timestamp with time zone::NO:",
	}
	if !equalStrings(columns, wantColumns) {
		t.Errorf("columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(wantColumns, "\n"))
	}

	// The numeric columns' precision and scale: a percentage to two decimals
	// up to 100.00, hours to two up to 24.00, a day count to one decimal.
	numerics := staffCollect(t, ctx, pool, "numeric precision", `
		SELECT table_name || '.' || column_name || ':' || numeric_precision || ',' || numeric_scale
		FROM information_schema.columns
		WHERE table_schema = 'staff' AND table_name = ANY($1) AND data_type = 'numeric'
		ORDER BY 1`, staffAbsenceTables)
	if want := []string{
		"absences.degree_percent:5,2", "absences.hours:4,2", "absences.percent:5,2", "holiday_adjustments.days:5,1",
	}; !equalStrings(numerics, want) {
		t.Errorf("numeric columns = %v, want %v", numerics, want)
	}

	foreignKeys := staffCollect(t, ctx, pool, "foreign keys", `
		SELECT c.conrelid::regclass::text || ':' || pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		WHERE c.connamespace = 'staff'::regnamespace AND c.contype = 'f'
		  AND (c.conrelid::regclass::text = ANY(SELECT 'staff.' || unnest($1::text[]))
		    OR pg_get_constraintdef(c.oid) LIKE 'FOREIGN KEY (holiday_scheme_id)%'
		    OR (c.conrelid = 'staff.documents'::regclass AND pg_get_constraintdef(c.oid) LIKE 'FOREIGN KEY (case_id)%'))
		ORDER BY 1`, staffAbsenceTables)
	wantForeignKeys := []string{
		"staff.absences:FOREIGN KEY (case_id) REFERENCES staff.sick_cases(id)",
		"staff.absences:FOREIGN KEY (person_id) REFERENCES staff.people(id)",
		"staff.absences:FOREIGN KEY (type_id) REFERENCES staff.absence_types(id)",
		"staff.documents:FOREIGN KEY (case_id) REFERENCES staff.sick_cases(id)",
		"staff.employments:FOREIGN KEY (holiday_scheme_id) REFERENCES staff.holiday_schemes(id)",
		"staff.holiday_adjustments:FOREIGN KEY (person_id) REFERENCES staff.people(id)",
		"staff.sick_case_milestones:FOREIGN KEY (case_id) REFERENCES staff.sick_cases(id)",
		"staff.sick_cases:FOREIGN KEY (person_id) REFERENCES staff.people(id)",
	}
	if !equalStrings(foreignKeys, wantForeignKeys) {
		t.Errorf("foreign keys =\n%s\nwant\n%s", strings.Join(foreignKeys, "\n"), strings.Join(wantForeignKeys, "\n"))
	}

	indexes := staffCollect(t, ctx, pool, "indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'staff' AND indexname NOT LIKE '%_pkey'
		  AND (tablename = ANY($1) OR indexname IN ('ix_employments_holiday_scheme', 'ix_documents_case'))
		ORDER BY indexname`, staffAbsenceTables)
	wantIndexes := []string{
		"CREATE INDEX ix_absences_case ON staff.absences USING btree (case_id)",
		"CREATE INDEX ix_absences_person_first_day ON staff.absences USING btree (person_id, first_day)",
		"CREATE INDEX ix_absences_requested ON staff.absences USING btree (status) WHERE ((status)::text = 'requested'::text)",
		"CREATE INDEX ix_absences_type ON staff.absences USING btree (type_id)",
		"CREATE INDEX ix_documents_case ON staff.documents USING btree (case_id)",
		"CREATE INDEX ix_employments_holiday_scheme ON staff.employments USING btree (holiday_scheme_id)",
		"CREATE INDEX ix_holiday_adjustments_person_year ON staff.holiday_adjustments USING btree (person_id, year)",
		"CREATE INDEX ix_sick_cases_person_first_day ON staff.sick_cases USING btree (person_id, first_day)",
		"CREATE UNIQUE INDEX ux_absence_types_key ON staff.absence_types USING btree (key)",
		"CREATE UNIQUE INDEX ux_holiday_adjustments_carry_over ON staff.holiday_adjustments USING btree (person_id, from_year) WHERE ((reason)::text = ANY ((ARRAY['agreed_carry_over'::character varying, 'forced_carry_over'::character varying])::text[]))",
		"CREATE UNIQUE INDEX ux_holiday_schemes_default ON staff.holiday_schemes USING btree (is_default) WHERE is_default",
		"CREATE UNIQUE INDEX ux_holiday_schemes_name_lower ON staff.holiday_schemes USING btree (lower((name)::text))",
		"CREATE UNIQUE INDEX ux_sick_case_milestones_case_kind ON staff.sick_case_milestones USING btree (case_id, kind)",
	}
	if !equalStrings(indexes, wantIndexes) {
		t.Errorf("indexes =\n%s\nwant\n%s", strings.Join(indexes, "\n"), strings.Join(wantIndexes, "\n"))
	}

	sequences := staffCollect(t, ctx, pool, "sequences", `
		SELECT sequencename || ':' || start_value FROM pg_sequences
		WHERE schemaname = 'staff' AND sequencename = ANY(SELECT unnest($1::text[]) || '_id_seq')
		ORDER BY 1`, staffAbsenceTables)
	if want := []string{
		"absence_types_id_seq:1001", "absences_id_seq:1001", "holiday_adjustments_id_seq:1001",
		"holiday_schemes_id_seq:1001", "sick_case_milestones_id_seq:1001", "sick_cases_id_seq:1001",
	}; !equalStrings(sequences, want) {
		t.Errorf("sequences = %v, want %v", sequences, want)
	}

	checks := staffCollect(t, ctx, pool, "checks", `
		SELECT conname FROM pg_constraint WHERE connamespace = 'staff'::regnamespace AND contype = 'c'`)
	if len(checks) != 0 {
		t.Errorf("staff has CHECK constraints %v, want none (house style)", checks)
	}

	// The type seeds in their order: key, kind, unit, paid, draws on the
	// holiday account, allows a percentage, needs a decision, visibility, the
	// a-melding leave description, hidden, seeded. The reserved absent is
	// hidden and last; the erase writes it in place of a sick or child-sick
	// type (plan reading 12).
	seeds := staffCollect(t, ctx, pool, "type seeds", `
		SELECT key || ':' || kind || ':' || unit || ':' || paid::text || ':' || draws_holiday::text || ':'
		    || allows_percent::text || ':' || needs_decision::text || ':' || visibility || ':'
		    || coalesce(amelding_leave, '-') || ':' || hidden::text || ':' || seeded::text
		FROM staff.absence_types ORDER BY sort`)
	wantSeeds := []string{
		"ferie:holiday:days:true:true:false:true:absent:-:false:true",
		"egenmelding:sick_self:days:true:false:false:false:absent:-:false:true",
		"sykmelding:sick_certified:days:true:false:false:false:absent:-:false:true",
		"svangerskapskontroll:other:hours:true:false:false:false:hr_only:-:false:true",
		"svangerskapspermisjon:leave:days:false:false:false:true:hr_only:permisjonMedForeldrepenger:false:true",
		"omsorgspermisjon:leave:days:false:false:true:true:absent:andreLovfestedePermisjoner:false:true",
		"foreldrepermisjon:leave:days:false:false:true:true:absent:permisjonMedForeldrepenger:false:true",
		"fodselspermisjon:leave:days:false:false:false:true:hr_only:permisjonMedForeldrepenger:false:true",
		"ammefri:leave:hours:false:false:false:true:absent:andreLovfestedePermisjoner:false:true",
		"sykt_barn:child_sick:days:true:false:false:false:absent:-:false:true",
		"pleie_naerstaende:leave:days:false:false:true:true:absent:andreLovfestedePermisjoner:false:true",
		"utdanning_lovfestet:leave:days:false:false:true:true:absent:utdanningspermisjonLovfestet:false:true",
		"utdanning_ikke_lovfestet:leave:days:false:false:true:true:absent:utdanningspermisjonIkkeLovfestet:false:true",
		"militaertjeneste:leave:days:false:false:false:true:absent:permisjonVedMilitaertjeneste:false:true",
		"offentlig_verv:leave:days:false:false:true:true:absent:andreLovfestedePermisjoner:false:true",
		"religios_hoytid:leave:days:false:false:false:true:hr_only:andreLovfestedePermisjoner:false:true",
		"velferdspermisjon:leave:days:true:false:false:true:absent:andreIkkeLovfestedePermisjoner:false:true",
		"permittering:leave:days:false:false:true:true:absent:permittering:false:true",
		"avspasering:time_off:hours:true:false:false:true:absent:-:false:true",
		"helse:other:hours:true:false:false:false:absent:-:false:true",
		"annen_lovfestet:leave:days:false:false:true:true:absent:andreLovfestedePermisjoner:false:true",
		"annen_ikke_lovfestet:leave:days:false:false:true:true:absent:andreIkkeLovfestedePermisjoner:false:true",
		"absent:other:days:false:false:false:false:absent:-:true:true",
	}
	if !equalStrings(seeds, wantSeeds) {
		t.Errorf("type seeds =\n%s\nwant\n%s", strings.Join(seeds, "\n"), strings.Join(wantSeeds, "\n"))
	}

	schemes := staffCollect(t, ctx, pool, "scheme seeds", `
		SELECT name || ':' || entitlement_virkedager || '/' || extra_from_60_virkedager || '/' || main_period_virkedager
		    || '/' || after_september_virkedager || '/' || max_carry_over_virkedager || ':' || is_default::text || ':' || seeded::text
		FROM staff.holiday_schemes ORDER BY id`)
	if want := []string{"Statutory (ferieloven):25/6/18/6/12:true:true"}; !equalStrings(schemes, want) {
		t.Errorf("scheme seeds = %v, want %v", schemes, want)
	}

	// At most one default scheme, as at most one default business unit; a
	// scheme's name is unique however it is cased; a type's key is unique.
	insertScheme := func(name string, isDefault bool) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO staff.holiday_schemes (name, entitlement_virkedager, extra_from_60_virkedager, is_default, created_at, updated_at)
			VALUES ($1, 30, 6, $2, now(), now())`, name, isDefault)
		return err
	}
	if err := insertScheme("Five weeks", true); !uniqueViolationOf(err, "ux_holiday_schemes_default") {
		t.Errorf("a second default scheme: %v, want the partial unique index to refuse it", err)
	}
	if err := insertScheme("Five weeks", false); err != nil {
		t.Fatalf("insert a second scheme: %v", err)
	}
	if err := insertScheme("FIVE WEEKS", false); !uniqueViolationOf(err, "ux_holiday_schemes_name_lower") {
		t.Errorf("a scheme name upper-cased: %v, want the unique index to refuse it", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff.absence_types (key, name_en, name_nb, kind, unit, paid, needs_decision, sort, created_at, updated_at)
		VALUES ('ferie', 'Again', 'Igjen', 'holiday', 'days', true, true, 99, now(), now())`); !uniqueViolationOf(err, "ux_absence_types_key") {
		t.Errorf("a second ferie type: %v, want the unique index to refuse it", err)
	}

	// At most one carry-over per person and the year it comes from, whether
	// agreed or forced; corrections are as many as HR writes.
	var person int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('1', 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`).Scan(&person); err != nil {
		t.Fatalf("insert a person: %v", err)
	}
	adjust := func(reason string, fromYear *int) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO staff.holiday_adjustments (person_id, year, days, reason, from_year, note, by_user_id, created_at, updated_at)
			VALUES ($1, 2027, 5.0, $2, $3, 'x', gen_random_uuid(), now(), now())`, person, reason, fromYear)
		return err
	}
	from := 2026
	if err := adjust("agreed_carry_over", &from); err != nil {
		t.Fatalf("record a carry-over: %v", err)
	}
	if err := adjust("forced_carry_over", &from); !uniqueViolationOf(err, "ux_holiday_adjustments_carry_over") {
		t.Errorf("a second carry-over from 2026: %v, want the partial unique index to refuse it", err)
	}
	for range 2 {
		if err := adjust("correction", nil); err != nil {
			t.Errorf("a correction: %v, want it written", err)
		}
	}

	// One milestone per kind per case.
	var caseID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO staff.sick_cases (person_id, first_day, last_day, created_at, updated_at)
		VALUES ($1, '2026-10-01', '2026-10-03', now(), now()) RETURNING id`, person).Scan(&caseID); err != nil {
		t.Fatalf("insert a case: %v", err)
	}
	milestone := func() error {
		_, err := pool.Exec(ctx, `
			INSERT INTO staff.sick_case_milestones (case_id, kind, due_date, created_at, updated_at)
			VALUES ($1, 'follow_up_plan', '2026-10-29', now(), now())`, caseID)
		return err
	}
	if err := milestone(); err != nil {
		t.Fatalf("insert a milestone: %v", err)
	}
	if err := milestone(); !uniqueViolationOf(err, "ux_sick_case_milestones_case_kind") {
		t.Errorf("a second follow_up_plan for one case: %v, want the unique index to refuse it", err)
	}
	for _, sql := range []string{
		`DELETE FROM staff.sick_case_milestones`, `DELETE FROM staff.sick_cases`,
		`DELETE FROM staff.holiday_adjustments`, `DELETE FROM staff.holiday_schemes WHERE NOT seeded`,
		`DELETE FROM staff.people`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("clear before the rollback (%s): %v", sql, err)
		}
	}

	migrateTo(t, url, staffAbsenceVersion-1)
	left := staffCollect(t, ctx, pool, "tables after the rollback", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'staff' AND table_name = ANY($1)`, staffAbsenceTables)
	if len(left) != 0 {
		t.Errorf("the rollback left %v", left)
	}
	leftColumns := staffCollect(t, ctx, pool, "columns after the rollback", `
		SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'staff'
		  AND (table_name, column_name) IN (('employments', 'holiday_scheme_id'), ('documents', 'case_id'), ('settings', 'auto_carry_over_since'), ('people', 'self_cert_withdrawn_from'))`)
	if len(leftColumns) != 0 {
		t.Errorf("the rollback left the columns %v", leftColumns)
	}
	tables := staffCollect(t, ctx, pool, "tables", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'staff' ORDER BY 1`)
	want := staffCollect(t, ctx, pool, "00042's and 00043's tables, sorted", `
		SELECT unnest($1::text[]) ORDER BY 1`, append(append([]string{}, staffTables...), staffRecordsTables...))
	if !equalStrings(tables, want) {
		t.Errorf("after the rollback staff tables = %v, want 00042's and 00043's %v", tables, want)
	}
}
