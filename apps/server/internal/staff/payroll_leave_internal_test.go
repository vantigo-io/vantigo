package staff

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// The payroll file's absence columns and leave rows (design S18; plan Task 6,
// Revision 2 Q4d, Revision 3 H1 and M1), judged over rows already loaded.

// leaveDescriptionFor is the a-melding description of a seed's key, as
// 00047 maps it.
var leaveDescriptionFor = map[string]string{
	"foreldrepermisjon": "permisjonMedForeldrepenger",
	"velferdspermisjon": "andreIkkeLovfestedePermisjoner",
	"permittering":      "permittering",
}

// payrollAbsence is an approved or registered row of person 1 as the
// export reads one: kind, unit days unless hours is set, percent and degree
// in percent (0 for none), the a-melding description by seed key ("" for
// none), typeID a type of its own.
type payrollAbsence struct {
	id, typeID   int64
	kind, key    string
	first, last  string
	percent      float64
	degree       float64
	hours        float64
	allowPercent bool
}

func (a payrollAbsence) row(t *testing.T) store.AbsencesBetweenRow {
	t.Helper()
	r := store.AbsencesBetweenRow{
		ID: a.id, PersonID: 1, TypeID: a.typeID, FirstDay: pgDay(t, a.first), LastDay: pgDay(t, a.last),
		Status: absenceRegistered, Kind: a.kind, Unit: "days", AllowsPercent: a.allowPercent,
		Percent: numericOfHundredths(10000),
	}
	if a.percent != 0 {
		r.Percent = numericOfHundredths(int64(a.percent * 100))
	}
	if a.degree != 0 {
		r.DegreePercent = numericOfHundredths(int64(a.degree * 100))
	}
	if a.hours != 0 {
		r.Unit = "hours"
		r.Hours = numericOfHundredths(int64(a.hours * 100))
	}
	if d, ok := leaveDescriptionFor[a.key]; ok {
		r.AmeldingLeave = &d
	}
	return r
}

// payrollWithAbsences is September's rows for Kari, employed since 2024 in
// one employment, with absences.
func payrollWithAbsences(t *testing.T, absences ...payrollAbsence) []payrollRow {
	t.Helper()
	m := september(t)
	e := payrollEmployment(t, 1, "2024-01-01", "")
	chains := map[int64][]store.StaffEmploymentTerm{1: {payrollTermsRow(t, 1, "2024-01-01", 100)}}
	rows := make([]store.AbsencesBetweenRow, 0, len(absences))
	for _, a := range absences {
		rows = append(rows, a.row(t))
	}
	org := "923609016"
	return payrollRows(payrollInput{
		People:      map[int64]payrollPerson{1: {EmployeeNumber: "K-1", GivenName: "Kari", FamilyName: "Nordmann"}},
		Employments: []store.StaffEmployment{e},
		Terms:       chains,
		Units:       map[int64]store.StaffBusinessUnit{1: {ID: 1, OrgNumber: &org}},
		Absences:    rows,
	}, m)
}

// leaveRows is rows' leave rows, as [id, start, end, description, percent].
func leaveRows(rows []payrollRow) [][5]string {
	var out [][5]string
	for _, r := range rows {
		if r.LeaveID != "" {
			out = append(out, [5]string{r.LeaveID, r.LeaveStart, r.LeaveEnd, r.LeaveDescription, r.LeavePercent})
		}
	}
	return out
}

// A leave over fourteen days that touches the month is one row after the
// employment's, repeating its keys, with its id, its whole first and last
// day across the month boundary, its description and its percent; a row
// continuing it — the next day, the same type and percent — is the same
// leave, under the first row's id, to the chain's end.
func TestPayrollLeave_OverFourteenDaysAndItsExtension(t *testing.T) {
	t.Parallel()
	rows := payrollWithAbsences(t,
		payrollAbsence{id: 11, typeID: 7, kind: "leave", key: "foreldrepermisjon", first: "2026-08-20", last: "2026-09-10", percent: 80, allowPercent: true},
		payrollAbsence{id: 15, typeID: 7, kind: "leave", key: "foreldrepermisjon", first: "2026-09-11", last: "2026-10-15", percent: 80, allowPercent: true},
	)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want the employment's and one leave row: %+v", len(rows), rows)
	}
	want := [][5]string{{"11", "2026-08-20", "2026-10-15", "permisjonMedForeldrepenger", "80,00"}}
	if got := leaveRows(rows); len(got) != 1 || got[0] != want[0] {
		t.Errorf("leave rows = %v, want %v", got, want)
	}
	leave := rows[1]
	if leave.EmployeeNumber != "K-1" || leave.Name != "Kari Nordmann" || leave.EmploymentNumber != rows[0].EmploymentNumber {
		t.Errorf("the leave row's keys = %q %q %q, want the employment's", leave.EmployeeNumber, leave.Name, leave.EmploymentNumber)
	}
	if leave.Start != "" || leave.FTEPercent != "" || leave.LeaveDays != "" {
		t.Errorf("the leave row carries the employment's cells: %+v", leave)
	}
	if rows[0].LeaveID != "" {
		t.Errorf("the employment's row carries a leave: %+v", rows[0])
	}
	// 1–30 September is 22 working days of leave at 80 %.
	if got := rows[0].LeaveDays; got != "17,60" {
		t.Errorf("Leave days = %q, want 17,60", got)
	}
}

// A leave of fourteen days or fewer is not reported (Skatteetaten,
// "Permisjon"), unless the person's leaves with a description within the
// twelve months to the month's end together pass fourteen days: two ten-day
// leaves, in February and September, are; a ten-day leave alone is not.
// Permittering is reported whatever its length; a child-sick run is never
// leave, however long (omsorgsdager are not reported as leave).
func TestPayrollLeave_FourteenDaysAndTheTwelveMonths(t *testing.T) {
	t.Parallel()
	tenDays := payrollAbsence{id: 21, typeID: 8, kind: "leave", key: "velferdspermisjon", first: "2026-09-10", last: "2026-09-19"}
	if got := leaveRows(payrollWithAbsences(t, tenDays)); len(got) != 0 {
		t.Errorf("a ten-day leave alone gave %v, want no leave row", got)
	}
	february := payrollAbsence{id: 20, typeID: 8, kind: "leave", key: "velferdspermisjon", first: "2026-02-02", last: "2026-02-11"}
	got := leaveRows(payrollWithAbsences(t, february, tenDays))
	if want := [5]string{"21", "2026-09-10", "2026-09-19", "andreIkkeLovfestedePermisjoner", "100,00"}; len(got) != 1 || got[0] != want {
		t.Errorf("two ten-day leaves in twelve months gave %v, want September's %v", got, want)
	}
	lastYear := payrollAbsence{id: 19, typeID: 8, kind: "leave", key: "velferdspermisjon", first: "2025-08-01", last: "2025-08-10"}
	if got := leaveRows(payrollWithAbsences(t, lastYear, tenDays)); len(got) != 0 {
		t.Errorf("a leave thirteen months back counted: %v", got)
	}
	permittering := payrollAbsence{id: 22, typeID: 9, kind: "leave", key: "permittering", first: "2026-09-14", last: "2026-09-16", percent: 50, allowPercent: true}
	if got := leaveRows(payrollWithAbsences(t, permittering)); len(got) != 1 || got[0][3] != "permittering" || got[0][4] != "50,00" {
		t.Errorf("a three-day permittering gave %v, want its row", got)
	}
	sickChild := payrollAbsence{id: 23, typeID: 10, kind: "child_sick", first: "2026-09-01", last: "2026-09-25"}
	if got := leaveRows(payrollWithAbsences(t, sickChild)); len(got) != 0 {
		t.Errorf("a child-sick run gave %v, want no leave row", got)
	}
}

// The month's totals per kind, in working days (each by its share) and
// hours: holiday; egenmelding; sykmelding by its degree, and beside a
// percentage leave by degree × (100 − leave %) / 100; child-sick days and
// those beyond the employer's ten of the year; leave days by their percent;
// time off in hours; other day rows. Approved ferie under a 100 %
// sykmelding counts once, as sick — never as holiday too.
func TestPayrollTotals_PerKind(t *testing.T) {
	t.Parallel()
	rows := payrollWithAbsences(t,
		// Ferie 1–4 September (4 days), a 100 % sykmelding 3–4 September
		// over it: 2 holiday days, 2 certified sick days.
		payrollAbsence{id: 1, typeID: 1, kind: "holiday", first: "2026-09-01", last: "2026-09-04"},
		payrollAbsence{id: 2, typeID: 3, kind: "sick_certified", first: "2026-09-03", last: "2026-09-04"},
		// Egenmelding Monday–Tuesday 7–8 September: 2 days.
		payrollAbsence{id: 3, typeID: 2, kind: "sick_self", first: "2026-09-07", last: "2026-09-08"},
		// A 60 % sykmelding 14–18 September beside a 50 % leave the same
		// week: 5 × 0.6 × 0.5 = 1.5 sick days, 5 × 0.5 = 2.5 leave days.
		payrollAbsence{id: 4, typeID: 3, kind: "sick_certified", first: "2026-09-14", last: "2026-09-18", degree: 60},
		payrollAbsence{id: 5, typeID: 6, kind: "leave", key: "", first: "2026-09-14", last: "2026-09-18", percent: 50, allowPercent: true},
		// Child sick: 9 working days in August, 3 in September — the 10th,
		// 11th and 12th of the year, so 2 beyond the employer's ten.
		payrollAbsence{id: 6, typeID: 10, kind: "child_sick", first: "2026-08-17", last: "2026-08-27"},
		payrollAbsence{id: 7, typeID: 10, kind: "child_sick", first: "2026-09-21", last: "2026-09-23"},
		// Avspasering 3.5 hours, and a day of another kind.
		payrollAbsence{id: 8, typeID: 19, kind: "time_off", first: "2026-09-24", last: "2026-09-24", hours: 3.5},
		payrollAbsence{id: 9, typeID: 23, kind: "other", first: "2026-09-25", last: "2026-09-25"},
	)
	r := rows[0]
	for name, c := range map[string][2]string{
		"Holiday days":                    {r.HolidayDays, "2,00"},
		"Self-certified sick days":        {r.SelfCertifiedSickDays, "2,00"},
		"Certified sick days":             {r.CertifiedSickDays, "3,50"},
		"Child sick days":                 {r.ChildSickDays, "3,00"},
		"Child sick days beyond employer": {r.ChildSickBeyondEmployer, "2,00"},
		"Leave days":                      {r.LeaveDays, "2,50"},
		"Time off hours":                  {r.TimeOffHours, "3,50"},
		"Other days":                      {r.OtherDays, "1,00"},
	} {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
	if got := leaveRows(rows); len(got) != 0 {
		t.Errorf("a leave without a description gave leave rows %v", got)
	}
}

// The header grows to the right of Pay grade, the A1 columns unchanged, and
// every row has a cell for each.
func TestPayrollColumns_TheAbsenceColumnsGoRight(t *testing.T) {
	t.Parallel()
	want := []string{
		"Leave ID", "Leave start", "Leave end", "Leave description", "Leave percent",
		"Holiday days", "Self-certified sick days", "Certified sick days", "Child sick days",
		"Child sick days beyond employer", "Leave days", "Time off hours", "Other days",
	}
	if len(payrollColumns) != 19+len(want) {
		t.Fatalf("columns = %d, want %d", len(payrollColumns), 19+len(want))
	}
	if payrollColumns[18] != "Pay grade" {
		t.Errorf("column 19 = %q, want Pay grade", payrollColumns[18])
	}
	for i, name := range want {
		if payrollColumns[19+i] != name {
			t.Errorf("column %d = %q, want %q", 20+i, payrollColumns[19+i], name)
		}
	}
	if got := len(payrollRow{}.cells()); got != len(payrollColumns) {
		t.Errorf("a row has %d cells for %d columns", got, len(payrollColumns))
	}
}
