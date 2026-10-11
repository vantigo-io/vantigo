package staff

import (
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// The payroll file's rows (design S18; plan Task 4, reading 12), judged over
// rows already loaded, without a database.

// september is the month most of these tests export.
func september(t *testing.T) payrollMonth {
	t.Helper()
	m, ok := parsePayrollMonth("2026-09")
	if !ok {
		t.Fatal("2026-09 does not parse")
	}
	return m
}

// payrollEmployment is employment id of person 1 from start to end ("" for
// open-ended), in business unit 1.
func payrollEmployment(t *testing.T, id int64, start, end string) store.StaffEmployment {
	t.Helper()
	e := store.StaffEmployment{ID: id, PersonID: 1, EmploymentNumber: "K-1-" + string(rune('0'+id)), BusinessUnitID: 1, StartDate: pgDay(t, start)}
	if end != "" {
		e.EndDate = pgDay(t, end)
		reason := "employee_resigned"
		e.EndReason = &reason
	}
	return e
}

// payrollTermsRow is a full terms row as the file reads one: fte percent
// from validFrom, 37.5 hours, not shift, fast.
func payrollTermsRow(t *testing.T, id int64, validFrom string, fte float64) store.StaffEmploymentTerm {
	t.Helper()
	r := row(t, id, validFrom, fte, "")
	r.EmploymentType, r.AppointmentForm, r.WorkingHoursScheme = "fast", "fast", "not_shift"
	r.FullTimeHoursPerWeek = numericOfHundredths(3750)
	return r
}

// payrollOf is the rows of employments, each with its chain, for Kari
// Nordmann (K-1) in a unit with an org number.
func payrollOf(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, departments map[int64]store.StaffDepartment, m payrollMonth) []payrollRow {
	org := "923609016"
	return payrollRows(payrollInput{
		People:      map[int64]payrollPerson{1: {EmployeeNumber: "K-1", GivenName: "Kari", FamilyName: "Nordmann"}},
		Employments: employments,
		Terms:       chains,
		Units:       map[int64]store.StaffBusinessUnit{1: {ID: 1, OrgNumber: &org}},
		Departments: departments,
	}, m)
}

// A month is YYYY-MM with a month 01 to 12; its first and last days are its
// own, February's last in a leap year included.
func TestParsePayrollMonth(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"2026-1", "2026-13", "2026-00", "abcd", "", " 2026-09", "2026-09 ", "26-09", "2026-09-01", "２０２６-09"} {
		if _, ok := parsePayrollMonth(raw); ok {
			t.Errorf("%q parses, want refused", raw)
		}
	}
	for raw, want := range map[string][2]string{
		"2026-09": {"2026-09-01", "2026-09-30"},
		"2026-02": {"2026-02-01", "2026-02-28"},
		"2024-02": {"2024-02-01", "2024-02-29"},
		"2026-12": {"2026-12-01", "2026-12-31"},
	} {
		m, ok := parsePayrollMonth(raw)
		if !ok {
			t.Errorf("%q refused", raw)
			continue
		}
		if got := [2]string{m.First.Format("2006-01-02"), m.Last.Format("2006-01-02")}; got != want {
			t.Errorf("%q = %v, want %v", raw, got, want)
		}
	}
}

// An employment is in the month when it starts on or before the month's last
// day and has no end or ends on or after its first: one that starts in it,
// ends in it, spans it, starts on its last day or ends on its first is in;
// one that ended before it or starts after it is not.
func TestPayrollRows_TheMonthWindow(t *testing.T) {
	t.Parallel()
	m := september(t)
	for _, c := range []struct {
		name       string
		start, end string
		in         bool
	}{
		{"starts in the month", "2026-09-10", "", true},
		{"ends in the month", "2026-01-01", "2026-09-20", true},
		{"spans the month, open-ended", "2025-01-01", "", true},
		{"spans the month, ending later", "2025-01-01", "2026-12-31", true},
		{"starts on the last day", "2026-09-30", "", true},
		{"ends on the first day", "2026-01-01", "2026-09-01", true},
		{"ended the day before the month", "2026-01-01", "2026-08-31", false},
		{"starts the day after the month", "2026-10-01", "", false},
	} {
		e := payrollEmployment(t, 1, c.start, c.end)
		chain := map[int64][]store.StaffEmploymentTerm{1: {payrollTermsRow(t, 1, c.start, 100)}}
		rows := payrollOf([]store.StaffEmployment{e}, chain, nil, m)
		if got := len(rows) == 1; got != c.in {
			t.Errorf("%s: in the month %v, want %v", c.name, got, c.in)
		}
	}
}

// The terms are those in force on the month's last day — a row dated after
// it is not yet in force — or on the end day when the employment ends
// earlier, so an employment ending mid-month still reports its terms. FTE
// changed is judged on the same day.
func TestPayrollRows_TheTermsInForceOnTheLastDayOrTheEnd(t *testing.T) {
	t.Parallel()
	m := september(t)
	open := payrollEmployment(t, 1, "2026-01-01", "")
	ended := payrollEmployment(t, 2, "2026-01-01", "2026-09-15")
	chains := map[int64][]store.StaffEmploymentTerm{
		1: {payrollTermsRow(t, 1, "2026-01-01", 100), payrollTermsRow(t, 2, "2026-09-16", 80), payrollTermsRow(t, 3, "2026-10-01", 60)},
		2: {payrollTermsRow(t, 4, "2026-01-01", 100), payrollTermsRow(t, 5, "2026-06-01", 50)},
	}
	rows := payrollOf([]store.StaffEmployment{open, ended}, chains, nil, m)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for i, want := range [][2]string{{"80,00", "2026-09-16"}, {"50,00", "2026-06-01"}} {
		if got := [2]string{rows[i].FTEPercent, rows[i].FTEChanged}; got != want {
			t.Errorf("row %d: FTE and FTE changed = %v, want %v", i, got, want)
		}
	}
}

// End and End reason are filled only in the month the employment ends: the
// same employment exported for the month before has them blank.
func TestPayrollRows_TheEndOnlyInTheEndMonth(t *testing.T) {
	t.Parallel()
	e := payrollEmployment(t, 1, "2026-01-01", "2026-10-15")
	chains := map[int64][]store.StaffEmploymentTerm{1: {payrollTermsRow(t, 1, "2026-01-01", 100)}}
	october, _ := parsePayrollMonth("2026-10")
	for _, c := range []struct {
		m         payrollMonth
		end, why  string
		monthName string
	}{
		{september(t), "", "", "2026-09"},
		{october, "2026-10-15", "arbeidstakerHarSagtOppSelv", "2026-10"},
	} {
		rows := payrollOf([]store.StaffEmployment{e}, chains, nil, c.m)
		if len(rows) != 1 {
			t.Fatalf("%s: rows = %d, want 1", c.monthName, len(rows))
		}
		if rows[0].End != c.end || rows[0].EndReason != c.why {
			t.Errorf("%s: End %q, End reason %q, want %q and %q", c.monthName, rows[0].End, rows[0].EndReason, c.end, c.why)
		}
	}
}

// The cost centre is the terms' own, else the department's, else blank; the
// department is its code.
func TestPayrollRows_TheCostCentreFallsBackToTheDepartment(t *testing.T) {
	t.Parallel()
	termsCC, deptCC := "4200", "4100"
	withCC := int64(10)
	without := int64(11)
	departments := map[int64]store.StaffDepartment{
		withCC:  {ID: withCC, Code: "SALG", CostCentre: &deptCC},
		without: {ID: without, Code: "LAGER"},
	}
	for _, c := range []struct {
		name       string
		dept       *int64
		cc         *string
		department string
		want       string
	}{
		{"the terms' own", &withCC, &termsCC, "SALG", "4200"},
		{"the department's", &withCC, nil, "SALG", "4100"},
		{"neither", &without, nil, "LAGER", ""},
		{"no department", nil, nil, "", ""},
		{"no department, the terms' own", nil, &termsCC, "", "4200"},
	} {
		r := payrollTermsRow(t, 1, "2026-01-01", 100)
		r.DepartmentID, r.CostCentre = c.dept, c.cc
		rows := payrollOf([]store.StaffEmployment{payrollEmployment(t, 1, "2026-01-01", "")},
			map[int64][]store.StaffEmploymentTerm{1: {r}}, departments, september(t))
		if len(rows) != 1 {
			t.Fatalf("%s: rows = %d", c.name, len(rows))
		}
		if rows[0].Department != c.department || rows[0].CostCentre != c.want {
			t.Errorf("%s: department %q, cost centre %q, want %q and %q", c.name, rows[0].Department, rows[0].CostCentre, c.department, c.want)
		}
	}
}

// An employment with no terms row in force on the day still has its row: the
// employment's columns filled, every terms column blank.
func TestPayrollRows_AnEmploymentWithoutTermsKeepsItsRow(t *testing.T) {
	t.Parallel()
	rows := payrollOf([]store.StaffEmployment{payrollEmployment(t, 1, "2026-01-01", "")}, nil, nil, september(t))
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	cells := rows[0].cells()
	want := []string{"K-1", "Kari Nordmann", "K-1-1", "923609016", "2026-01-01", "", "", "", "", "", "", "", "", "", "", "", "", "", ""}
	if !slices.Equal(cells, want) {
		t.Errorf("cells = %q, want %q", cells, want)
	}
}

// Every cell of a full row: the name is given and family name, the dates
// ISO, the hours and the percentage with the decimal comma, the scheme, the
// end reason and the appointment form Skatteetaten's codes, the remuneration
// type as stored, the seniority date and pay grade when set.
func TestPayrollRows_EveryCell(t *testing.T) {
	t.Parallel()
	october, _ := parsePayrollMonth("2026-10")
	e := payrollEmployment(t, 1, "2026-01-01", "2026-10-15")
	occupation, remuneration, grade, cc := "2512105", "hourly", "52", "4200"
	r := payrollTermsRow(t, 1, "2026-01-01", 62.5)
	r.EmploymentType, r.AppointmentForm, r.WorkingHoursScheme = "midlertidig", "midlertidig_tilkallingsvikar", "shift_365"
	r.FullTimeHoursPerWeek = numericOfHundredths(3650)
	r.OccupationCode, r.RemunerationType, r.PayGrade, r.CostCentre = &occupation, &remuneration, &grade, &cc
	r.SeniorityDate = pgtype.Date{Time: day(t, "2019-05-01"), Valid: true}
	r.PayComponents = []byte(`[{"name":"Timelønn","amount":250,"currency":"NOK","period":"hourly"}]`)
	rows := payrollOf([]store.StaffEmployment{e}, map[int64][]store.StaffEmploymentTerm{1: {r}}, nil, october)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	want := []string{
		"K-1", "Kari Nordmann", "K-1-1", "923609016", "2026-01-01", "2026-10-15", "arbeidstakerHarSagtOppSelv",
		"2512105", "36,50", "62,50", "2026-01-01", "skift365", "midlertidigAnsattSomTilkallingsvikar", "hourly",
		"2026-01-01", "", "4200", "2019-05-01", "52",
	}
	if got := rows[0].cells(); !slices.Equal(got, want) {
		t.Errorf("cells = %q\nwant    %q", got, want)
	}
}

// Every enumerated value reaches the file through its code — each scheme,
// each end reason and each appointment form as Skatteetaten's a-melding names
// it — and the remuneration type, which the a-melding has not carried since
// 2019, as the module stores it.
func TestPayrollRows_EveryEnumeratedValueThroughItsCode(t *testing.T) {
	t.Parallel()
	october, _ := parsePayrollMonth("2026-10")
	check := func(name string, values []string, codes map[string]string, set func(*store.StaffEmployment, *store.StaffEmploymentTerm, string), cell func(payrollRow) string) {
		for _, v := range values {
			e := payrollEmployment(t, 1, "2026-01-01", "2026-10-15")
			r := payrollTermsRow(t, 1, "2026-01-01", 100)
			set(&e, &r, v)
			rows := payrollOf([]store.StaffEmployment{e}, map[int64][]store.StaffEmploymentTerm{1: {r}}, nil, october)
			want := v
			if codes != nil {
				want = codes[v]
			}
			if len(rows) != 1 || want == "" || cell(rows[0]) != want {
				t.Errorf("%s %s: cell %v, want %q", name, v, rows, want)
			}
		}
	}
	check("scheme", workingHoursSchemes, workingHoursSchemeCodes,
		func(_ *store.StaffEmployment, r *store.StaffEmploymentTerm, v string) { r.WorkingHoursScheme = v },
		func(r payrollRow) string { return r.WorkingHoursScheme })
	check("end reason", endReasons, endReasonCodes,
		func(e *store.StaffEmployment, _ *store.StaffEmploymentTerm, v string) { e.EndReason = &v },
		func(r payrollRow) string { return r.EndReason })
	check("appointment form", appointmentForms, appointmentFormCodes,
		func(_ *store.StaffEmployment, r *store.StaffEmploymentTerm, v string) { r.AppointmentForm = v },
		func(r payrollRow) string { return r.AppointmentForm })
	check("remuneration type", remunerationTypes, nil,
		func(_ *store.StaffEmployment, r *store.StaffEmploymentTerm, v string) { r.RemunerationType = &v },
		func(r payrollRow) string { return r.RemunerationType })
}

// The rows are ordered by employee number, then employment number, whatever
// order they were loaded in; the name is the given and the family name, not
// the display name; a unit without an org number is a blank cell.
func TestPayrollRows_OrderAndNames(t *testing.T) {
	t.Parallel()
	org := "923609016"
	employment := func(id, person int64, number string, unit int64) store.StaffEmployment {
		return store.StaffEmployment{ID: id, PersonID: person, EmploymentNumber: number, BusinessUnitID: unit, StartDate: pgDay(t, "2026-01-01")}
	}
	rows := payrollRows(payrollInput{
		People: map[int64]payrollPerson{
			1: {EmployeeNumber: "B-1", GivenName: "Kari", FamilyName: "Nordmann"},
			2: {EmployeeNumber: "A-1", GivenName: "Ola", FamilyName: "Hansen"},
		},
		Employments: []store.StaffEmployment{
			employment(1, 1, "B-1-2", 1), employment(2, 2, "A-1-1", 2), employment(3, 1, "B-1-1", 1),
		},
		Units: map[int64]store.StaffBusinessUnit{1: {ID: 1, OrgNumber: &org}, 2: {ID: 2}},
	}, september(t))
	var got [][3]string
	for _, r := range rows {
		got = append(got, [3]string{r.EmployeeNumber, r.EmploymentNumber, r.Name + "/" + r.BusinessUnitOrgNumber})
	}
	want := [][3]string{
		{"A-1", "A-1-1", "Ola Hansen/"},
		{"B-1", "B-1-1", "Kari Nordmann/923609016"},
		{"B-1", "B-1-2", "Kari Nordmann/923609016"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

// The header is the plan's, in sentence-case English, in this order; delivery
// B's leave and absence columns are appended after Pay grade.
func TestPayrollColumns(t *testing.T) {
	t.Parallel()
	want := []string{
		"Employee number", "Name", "Employment number", "Business unit org number", "Start", "End", "End reason",
		"Occupation code", "Full-time hours", "FTE percent", "FTE changed", "Working hours scheme", "Appointment form",
		"Remuneration type", "Salary changed", "Department", "Cost centre", "Seniority date", "Pay grade",
	}
	if !slices.Equal(payrollColumns, want) {
		t.Errorf("payrollColumns = %q, want %q", payrollColumns, want)
	}
	if n := len(payrollRow{}.cells()); n != len(want) {
		t.Errorf("a row has %d cells, want %d", n, len(want))
	}
}
