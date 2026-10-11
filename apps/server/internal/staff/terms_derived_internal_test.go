package staff

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// day is a civil date, as the module carries one: midnight UTC.
func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func pgDay(t *testing.T, s string) pgtype.Date {
	t.Helper()
	return pgtype.Date{Time: day(t, s), Valid: true}
}

// row is a terms row from validFrom with fte (in percent) and the pay
// components, as JSON, the derivations read.
func row(t *testing.T, id int64, validFrom string, fte float64, pay string) store.StaffEmploymentTerm {
	t.Helper()
	r := store.StaffEmploymentTerm{ID: id, ValidFrom: pgDay(t, validFrom), FtePercent: numericOfHundredths(int64(fte * 100))}
	if pay != "" {
		r.PayComponents = []byte(pay)
	}
	return r
}

func dayString(d *time.Time) string {
	if d == nil {
		return "none"
	}
	return d.Format(time.DateOnly)
}

// The row in force on a date is the latest whose validFrom is on or before
// it, within the employment's own days: a row dated after the date — after
// today, say — is not yet in force, and before the start or after the end
// nothing is.
func TestTermsInForce_TheLatestRowOnOrBeforeTheDate(t *testing.T) {
	t.Parallel()
	open := store.StaffEmployment{StartDate: pgDay(t, "2026-01-01")}
	ended := store.StaffEmployment{StartDate: pgDay(t, "2026-01-01"), EndDate: pgDay(t, "2026-11-30")}
	chain := []store.StaffEmploymentTerm{
		row(t, 1, "2026-01-01", 100, ""), row(t, 2, "2026-03-01", 80, ""), row(t, 3, "2026-12-01", 60, ""),
	}
	for _, c := range []struct {
		name string
		e    store.StaffEmployment
		date string
		want int64
	}{
		{"before the start", open, "2025-12-31", 0},
		{"on the start", open, "2026-01-01", 1},
		{"between the first two rows", open, "2026-02-28", 1},
		{"on the second row's day", open, "2026-03-01", 2},
		{"today, with a row dated later", open, "2026-10-11", 2},
		{"once the later row is in force", open, "2026-12-05", 3},
		{"on the end day", ended, "2026-11-30", 2},
		{"after the end", ended, "2026-12-05", 0},
	} {
		got := termsInForce(c.e, chain, day(t, c.date))
		var id int64
		if got != nil {
			id = got.ID
		}
		if id != c.want {
			t.Errorf("%s (%s): row %d, want %d", c.name, c.date, id, c.want)
		}
	}
	if got := termsInForce(open, nil, day(t, "2026-05-01")); got != nil {
		t.Errorf("no rows: %+v, want none", got)
	}
}

// fteChangedAt is the day the percentage in force took effect: the earliest
// row of the unbroken run with that percentage, so a row that changed only
// the title leaves it where it was; a single-row chain answers its own
// validFrom.
func TestFteChangedAt_TheStartOfTheRunInForce(t *testing.T) {
	t.Parallel()
	chain := []store.StaffEmploymentTerm{
		row(t, 1, "2026-01-01", 100, ""), row(t, 2, "2026-03-01", 100, ""),
		row(t, 3, "2026-06-01", 80, ""), row(t, 4, "2026-09-01", 80, ""), row(t, 5, "2026-11-01", 100, ""),
	}
	for _, c := range []struct {
		chain []store.StaffEmploymentTerm
		date  string
		want  string
	}{
		{chain, "2026-04-01", "2026-01-01"},
		{chain, "2026-10-11", "2026-06-01"},
		{chain, "2026-11-01", "2026-11-01"},
		{chain[:1], "2026-10-11", "2026-01-01"},
		{chain, "2025-12-01", "2026-01-01"},
		{nil, "2026-10-11", "none"},
	} {
		if got := dayString(fteChangedAt(c.chain, day(t, c.date))); got != c.want {
			t.Errorf("fteChangedAt(%d rows, %s) = %s, want %s", len(c.chain), c.date, got, c.want)
		}
	}
}

// salaryChangedAt compares the pay components as a list sorted by name and
// amount, on name and amount alone: a reordered list, or a change of currency
// or period only, is the same pay; a renamed component or a new amount is a
// change. No components at all and an empty list are the same.
func TestSalaryChangedAt_ComparedOnSortedNamesAndAmounts(t *testing.T) {
	t.Parallel()
	pay := func(components ...[4]any) string {
		var out []map[string]any
		for _, c := range components {
			out = append(out, map[string]any{"name": c[0], "amount": c[1], "currency": c[2], "period": c[3]})
		}
		b, _ := json.Marshal(out)
		return string(b)
	}
	chain := []store.StaffEmploymentTerm{
		row(t, 1, "2026-01-01", 100, pay([4]any{"Base", 50000, "NOK", "monthly"}, [4]any{"Phone", 500, "NOK", "monthly"})),
		row(t, 2, "2026-03-01", 100, pay([4]any{"Phone", 500, "EUR", "yearly"}, [4]any{"Base", 50000.0, "NOK", "monthly"})),
		row(t, 3, "2026-05-01", 80, pay([4]any{"Base", 50000, "NOK", "monthly"}, [4]any{"Telefon", 500, "NOK", "monthly"})),
		row(t, 4, "2026-07-01", 80, pay([4]any{"Base", 52000, "NOK", "monthly"}, [4]any{"Telefon", 500, "NOK", "monthly"})),
	}
	for _, c := range []struct {
		name  string
		chain []store.StaffEmploymentTerm
		date  string
		want  string
	}{
		{"reordered, another currency and period: unchanged", chain, "2026-04-01", "2026-01-01"},
		{"a renamed component is a change", chain, "2026-06-01", "2026-05-01"},
		{"a new amount is a change", chain, "2026-10-11", "2026-07-01"},
		{"a single row answers its own date", chain[:1], "2026-10-11", "2026-01-01"},
		{"no components and an empty list are the same", []store.StaffEmploymentTerm{
			row(t, 1, "2026-01-01", 100, ""), row(t, 2, "2026-02-01", 100, "[]"),
		}, "2026-10-11", "2026-01-01"},
	} {
		if got := dayString(salaryChangedAt(c.chain, day(t, c.date))); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// graph is a small company: ids are people (1xx), departments (2xx) and
// employments (3xx).
func id(v int64) *int64 { return &v }

// managerOf is the terms' manager, else the department's — unless that is
// the person, when the parents are climbed to the first whose manager is
// someone else — else none; a department without a manager is climbed past
// the same way, and a department graph that loops (written past the module)
// ends the climb rather than looping. Concurrent employments give a manager
// each.
func TestManagerOf_TermsThenTheDepartmentClimbingPastThePerson(t *testing.T) {
	t.Parallel()
	start := day(t, "2026-01-01")
	ended := day(t, "2026-05-31")
	g := newManagerGraph(
		[]graphEmployment{
			{ID: 301, PersonID: 101, Start: start},              // named manager
			{ID: 302, PersonID: 102, Start: start},              // department's manager
			{ID: 303, PersonID: 103, Start: start},              // heads their own department
			{ID: 304, PersonID: 104, Start: start},              // department without a manager
			{ID: 305, PersonID: 105, Start: start},              // nothing
			{ID: 306, PersonID: 106, Start: start},              // looping departments
			{ID: 307, PersonID: 107, Start: start},              // two employments …
			{ID: 308, PersonID: 107, Start: start},              // … two managers
			{ID: 309, PersonID: 108, Start: start, End: &ended}, // ended
		},
		[]graphTerms{
			{EmploymentID: 301, ValidFrom: start, Manager: id(110)},
			{EmploymentID: 302, ValidFrom: start, DepartmentID: id(202)},
			{EmploymentID: 303, ValidFrom: start, DepartmentID: id(202)},
			{EmploymentID: 304, ValidFrom: start, DepartmentID: id(203)},
			{EmploymentID: 305, ValidFrom: start},
			{EmploymentID: 306, ValidFrom: start, DepartmentID: id(204)},
			{EmploymentID: 307, ValidFrom: start, Manager: id(112)},
			{EmploymentID: 308, ValidFrom: start, Manager: id(111)},
			{EmploymentID: 309, ValidFrom: start, Manager: id(110)},
			// a row dated later changes 101's manager from then on
			{EmploymentID: 301, ValidFrom: day(t, "2026-12-01"), Manager: id(111)},
		},
		[]graphDepartment{
			{ID: 201, Manager: id(120)},                    // the top
			{ID: 202, Manager: id(103), ParentID: id(201)}, // headed by 103
			{ID: 203, ParentID: id(202)},                   // nobody heads it
			{ID: 204, ParentID: id(205)},
			{ID: 205, ParentID: id(204)},
		},
	)
	today := day(t, "2026-10-11")
	for _, c := range []struct {
		name   string
		person int64
		date   time.Time
		want   []int64
	}{
		{"the terms name the manager", 101, today, []int64{110}},
		{"a row dated after today is not yet in force", 101, today, []int64{110}},
		{"once it is", 101, day(t, "2026-12-01"), []int64{111}},
		{"no manager on the terms: the department's", 102, today, []int64{103}},
		{"the head of their own department: the parent's", 103, today, []int64{120}},
		{"a department nobody heads: climbed past", 104, today, []int64{103}},
		{"neither terms nor department", 105, today, nil},
		{"departments that loop", 106, today, nil},
		{"two employments, two managers", 107, today, []int64{111, 112}},
		{"an ended employment manages nothing", 108, today, nil},
		{"while it lasted", 108, day(t, "2026-05-31"), []int64{110}},
		{"before the start", 101, day(t, "2025-12-31"), nil},
	} {
		if got := g.managerOf(c.person, c.date); !slices.Equal(got, c.want) {
			t.Errorf("%s: managerOf(%d, %s) = %v, want %v", c.name, c.person, c.date.Format(time.DateOnly), got, c.want)
		}
	}
}

// A cycle through a person is judged on the day a change takes effect and on
// every later day the graph changes — a row of someone else's dated later
// that closes the loop counts — but not on a day before it.
func TestCycleFrom_JudgedOnTheDayAndEveryLaterChange(t *testing.T) {
	t.Parallel()
	start := day(t, "2026-01-01")
	ends := day(t, "2026-08-31")
	for _, c := range []struct {
		name   string
		terms  []graphTerms
		depts  []graphDepartment
		end    *time.Time
		person int64
		from   string
		want   string
	}{
		{"a names b, b names a", []graphTerms{
			{EmploymentID: 301, ValidFrom: start, Manager: id(102)},
			{EmploymentID: 302, ValidFrom: start, Manager: id(101)},
		}, nil, nil, 101, "2026-01-01", "2026-01-01"},
		{"b names a only from a later row", []graphTerms{
			{EmploymentID: 301, ValidFrom: start, Manager: id(102)},
			{EmploymentID: 302, ValidFrom: start, Manager: id(103)},
			{EmploymentID: 302, ValidFrom: day(t, "2026-05-01"), Manager: id(101)},
		}, nil, nil, 101, "2026-01-01", "2026-05-01"},
		{"a loop that ended before the day is not judged", []graphTerms{
			{EmploymentID: 301, ValidFrom: start, Manager: id(102)},
			{EmploymentID: 301, ValidFrom: day(t, "2026-04-01")},
			{EmploymentID: 302, ValidFrom: start, Manager: id(101)},
		}, nil, nil, 101, "2026-04-01", "none"},
		{"through the department's manager", []graphTerms{
			{EmploymentID: 301, ValidFrom: start, Manager: id(102)},
			{EmploymentID: 302, ValidFrom: start, DepartmentID: id(201)},
		}, []graphDepartment{{ID: 201, Manager: id(101)}}, nil, 101, "2026-01-01", "2026-01-01"},
		{"until b's employment ends", []graphTerms{
			{EmploymentID: 301, ValidFrom: day(t, "2026-09-01"), Manager: id(102)},
			{EmploymentID: 302, ValidFrom: start, Manager: id(101)},
		}, nil, &ends, 101, "2026-01-01", "none"},
		{"no loop", []graphTerms{
			{EmploymentID: 301, ValidFrom: start, Manager: id(102)},
			{EmploymentID: 302, ValidFrom: start, Manager: id(103)},
		}, nil, nil, 101, "2026-01-01", "none"},
	} {
		g := newManagerGraph([]graphEmployment{
			{ID: 301, PersonID: 101, Start: start},
			{ID: 302, PersonID: 102, Start: start, End: c.end},
			{ID: 303, PersonID: 103, Start: start},
		}, c.terms, c.depts)
		at, found := g.cycleFrom(c.person, day(t, c.from))
		got := "none"
		if found {
			got = at.Format(time.DateOnly)
		}
		if got != c.want {
			t.Errorf("%s: cycleFrom(%d, %s) = %s, want %s", c.name, c.person, c.from, got, c.want)
		}
	}
}
