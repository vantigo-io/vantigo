package staff

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The place's department (plan reading 25): of a person's employments, the
// earliest in force today by start and then id, as rulePlaces judges a
// group's rule — its terms row in force today, the department it names. No
// employment in force, no terms row yet, or terms naming no department is
// none.
func TestPlaceDepartmentOf_TheEarliestInForce(t *testing.T) {
	t.Parallel()
	today := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)
	day := func(s string) pgtype.Date {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return dayParam(d)
	}
	employment := func(id int64, start, end string) store.StaffEmployment {
		e := store.StaffEmployment{ID: id, PersonID: 1, StartDate: day(start)}
		if end != "" {
			e.EndDate = day(end)
		}
		return e
	}
	terms := func(employmentID int64, from string, department int64) store.StaffEmploymentTerm {
		t := store.StaffEmploymentTerm{EmploymentID: employmentID, ValidFrom: day(from)}
		if department != 0 {
			t.DepartmentID = &department
		}
		return t
	}
	chains := map[int64][]store.StaffEmploymentTerm{
		10: {terms(10, "2020-01-01", 100), terms(10, "2026-10-01", 101), terms(10, "2026-11-01", 102)},
		11: {terms(11, "2024-01-01", 110)},
		12: {terms(12, "2020-01-01", 120)},
		13: {terms(13, "2026-10-13", 130)},
		14: {terms(14, "2020-01-01", 0)},
		16: {terms(16, "2020-01-01", 160)},
	}
	for _, tc := range []struct {
		name        string
		employments []store.StaffEmployment
		want        int64 // 0: none
	}{
		{"no employment", nil, 0},
		{"one in force: the terms in force today", []store.StaffEmployment{employment(10, "2020-01-01", "")}, 101},
		{"the earlier start of two in force", []store.StaffEmployment{employment(11, "2024-01-01", ""), employment(10, "2020-01-01", "")}, 101},
		{"the same start: the lower id", []store.StaffEmployment{employment(16, "2020-01-01", ""), employment(12, "2020-01-01", "")}, 120},
		{"an ended one is not the place", []store.StaffEmployment{employment(12, "2019-01-01", "2026-10-11"), employment(11, "2024-01-01", "")}, 110},
		{"ending today still is", []store.StaffEmployment{employment(12, "2019-01-01", "2026-10-12"), employment(11, "2024-01-01", "")}, 120},
		{"one not yet started is not", []store.StaffEmployment{employment(16, "2026-10-13", "")}, 0},
		{"terms not yet in force", []store.StaffEmployment{employment(13, "2026-10-01", "")}, 0},
		{"no terms row", []store.StaffEmployment{employment(15, "2020-01-01", "")}, 0},
		{"terms naming no department", []store.StaffEmployment{employment(14, "2020-01-01", "")}, 0},
	} {
		got := placeDepartmentOf(tc.employments, chains, today)
		switch {
		case tc.want == 0 && got != nil:
			t.Errorf("%s: department %d, want none", tc.name, *got)
		case tc.want != 0 && (got == nil || *got != tc.want):
			t.Errorf("%s: department %v, want %d", tc.name, got, tc.want)
		}
	}
}

// The profiles that reach a person (readings 25 and 26), read from the
// tables: the assigned ones, archived included, and the default of the
// department of their place today unless archived, merged into one row when
// both, by key. A person with no employment in force has the assigned ones
// alone.
func TestEffectiveProfiles_AssignedAndTheDepartmentDefault(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	today := time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC)
	q := store.New(pool)

	one := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	unit := one(`
		WITH le AS (
			INSERT INTO staff.legal_entities (name, is_default, created_at, updated_at)
			VALUES ('Kraft-Verket AS', false, now(), now()) RETURNING id
		)
		INSERT INTO staff.business_units (legal_entity_id, name, is_default, created_at, updated_at)
		SELECT id, 'Oslo', false, now(), now() FROM le RETURNING id`)
	profile := func(key string, archived bool) int64 {
		t.Helper()
		return one(`
			INSERT INTO staff.competence_profiles (key, name_en, name_nb, archived_at, created_at, updated_at)
			VALUES ($1::text, $1::text, $1::text, CASE WHEN $2::boolean THEN now() END, now(), now()) RETURNING id`, key, archived)
	}
	department := func(code string, profileID int64) int64 {
		t.Helper()
		return one(`
			INSERT INTO staff.departments (code, name, competence_profile_id, created_at, updated_at)
			VALUES ($1::text, $1::text || ' department', $2, now(), now()) RETURNING id`, code, profileID)
	}
	person := func(number string, start, end string, departmentID int64) int64 {
		t.Helper()
		id := one(`
			INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
			VALUES ($1, $1, 'Test', $1, now(), now()) RETURNING id`, number)
		if start == "" {
			return id
		}
		employment := one(`
			INSERT INTO staff.employments (person_id, employment_number, relationship_type, business_unit_id, start_date, end_date,
				end_reason, created_at, updated_at)
			VALUES ($1, $2, 'ordinary', $3, $4::date, NULLIF($5, '')::date, CASE WHEN $5 = '' THEN NULL ELSE 'contract_ended' END,
				now(), now()) RETURNING id`, id, "E-"+number, unit, start, end)
		exec(`
			INSERT INTO staff.employment_terms (employment_id, valid_from, employment_type, appointment_form, fte_percent,
				full_time_hours_per_week, department_id, created_at, updated_at)
			VALUES ($1, $2::date, 'fast', 'fast', 100, 37.5, $3, now(), now())`, employment, start, departmentID)
		return id
	}
	assign := func(personID, profileID int64) {
		t.Helper()
		exec(`INSERT INTO staff.person_profiles (person_id, profile_id, created_at) VALUES ($1, $2, now())`, personID, profileID)
	}

	electrician, operator, welder := profile("electrician", false), profile("operator", false), profile("welder", false)
	retired := profile("retired", true)
	ops := department("OPS", operator)
	old := department("OLD", retired)

	keys := func(personID int64) []string {
		t.Helper()
		got, err := effectiveProfiles(ctx, q, personID, today)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, p := range got {
			key := p.Profile.Key
			if p.Assigned {
				key += "+assigned"
			}
			if p.Department != nil {
				key += "+" + p.Department.Code
			}
			if p.Profile.ArchivedAt != nil {
				key += "+archived"
			}
			out = append(out, key)
		}
		return out
	}
	equal := func(name string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", name, got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: %v, want %v", name, got, want)
				return
			}
		}
	}

	kari := person("P1", "2020-01-01", "", ops)
	assign(kari, welder)
	assign(kari, electrician)
	assign(kari, retired)
	equal("assigned, archived included, and the department's default", keys(kari),
		"electrician+assigned", "operator+OPS", "retired+assigned+archived", "welder+assigned")

	both := person("P2", "2020-01-01", "", ops)
	assign(both, operator)
	equal("assigned and the default: one row saying both", keys(both), "operator+assigned+OPS")

	archivedDefault := person("P3", "2020-01-01", "", old)
	equal("an archived default reaches nobody", keys(archivedDefault))
	assign(archivedDefault, retired)
	equal("an archived profile assigned and the default: assigned alone", keys(archivedDefault), "retired+assigned+archived")

	ended := person("P4", "2019-01-01", "2026-10-11", ops)
	assign(ended, welder)
	equal("no employment in force: the assigned alone", keys(ended), "welder+assigned")

	nobody := person("P5", "", "", 0)
	equal("nobody: nothing", keys(nobody))

	got, err := departmentToday(ctx, q, kari, today)
	if err != nil || got == nil || got.ID != ops || got.CompetenceProfileID == nil || *got.CompetenceProfileID != operator {
		t.Errorf("Kari's department today = %+v (%v), want OPS with its default", got, err)
	}
	if got, err := departmentToday(ctx, q, nobody, today); err != nil || got != nil {
		t.Errorf("nobody's department today = %+v (%v), want none", got, err)
	}

	summary := personProfileResponse(effectiveProfile{
		Profile:  store.StaffCompetenceProfile{ID: retired, Key: "retired", NameEn: "Retired", NameNb: "Pensjonert", ArchivedAt: &today},
		Assigned: true,
	})
	if summary.Profile.Id != retired || summary.Profile.Names.Nb != "Pensjonert" || !summary.Profile.Archived || !summary.Assigned ||
		summary.Department != nil {
		t.Errorf("the response = %+v", summary)
	}
}
