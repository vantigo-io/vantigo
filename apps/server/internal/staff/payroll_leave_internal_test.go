package staff

import (
	"strconv"
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
		LeaveID:     testLeaveID,
	}, m)
}

// testLeaveID stands in for the export's keyed surrogate in the pure tests.
func testLeaveID(id int64) string { return "L" + strconv.FormatInt(id, 10) }

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
	want := [][5]string{{"L11", "2026-08-20", "2026-10-15", "permisjonMedForeldrepenger", "80,00"}}
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
	if want := [5]string{"L21", "2026-09-10", "2026-09-19", "andreIkkeLovfestedePermisjoner", "100,00"}; len(got) != 1 || got[0] != want {
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
// sykmelding counts as holiday, not sick (ferieloven § 9: the ferie stands
// until the employee's demand is recorded, and no sykepenger are paid on a
// holiday day), until a postponement splits it.
func TestPayrollTotals_PerKind(t *testing.T) {
	t.Parallel()
	rows := payrollWithAbsences(t,
		// Ferie 1–4 September (4 days), a 100 % sykmelding 3–4 September
		// over it and no demand recorded: 4 holiday days, no sick day.
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
		"Holiday days":                    {r.HolidayDays, "4,00"},
		"Self-certified sick days":        {r.SelfCertifiedSickDays, "2,00"},
		"Certified sick days":             {r.CertifiedSickDays, "1,50"},
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

// sharesOnDay is d's shares from absences.
func sharesOnDay(t *testing.T, d string, absences ...payrollAbsence) dayShares {
	t.Helper()
	rows := make([]store.AbsencesBetweenRow, 0, len(absences))
	for _, a := range absences {
		rows = append(rows, a.row(t))
	}
	return sharesOn(rows, day(t, d))
}

// A ferie day is a holiday day whatever sickness covers it, until a recorded
// postponement splits the ferie (ferieloven § 9, Revision 3 H1): a
// sykmelding of any degree or an egenmelding over ferie counts nothing as
// sick — no sykepenger are paid on a holiday day. Beside leave the
// sick share is degree × (100 − the leaves' percent) / 100, the leaves summed
// and capped at a whole day, so sickness on a 100 % leave day counts nothing.
func TestSharesOn_FerieAndLeaveDays(t *testing.T) {
	t.Parallel()
	ferie := payrollAbsence{id: 1, typeID: 1, kind: "holiday", first: "2026-09-07", last: "2026-09-11"}
	for _, c := range []struct {
		name          string
		absences      []payrollAbsence
		holiday, sick int64
	}{
		{"a 100 % sykmelding over ferie", []payrollAbsence{ferie, {id: 2, typeID: 3, kind: "sick_certified", first: "2026-09-07", last: "2026-09-11"}}, wholeDay, 0},
		{"a 50 % sykmelding over ferie", []payrollAbsence{ferie, {id: 2, typeID: 3, kind: "sick_certified", first: "2026-09-07", last: "2026-09-11", degree: 50}}, wholeDay, 0},
		{"an egenmelding over ferie", []payrollAbsence{ferie, {id: 2, typeID: 2, kind: "sick_self", first: "2026-09-07", last: "2026-09-08"}}, wholeDay, 0},
		{"a sykmelding on a 100 % leave day", []payrollAbsence{
			{id: 3, typeID: 7, kind: "leave", first: "2026-09-07", last: "2026-09-11"},
			{id: 4, typeID: 3, kind: "sick_certified", first: "2026-09-07", last: "2026-09-11", degree: 40},
		}, 0, 0},
		{"a 60 % sykmelding beside two 25 % leaves", []payrollAbsence{
			{id: 3, typeID: 7, kind: "leave", first: "2026-09-07", last: "2026-09-11", percent: 25, allowPercent: true},
			{id: 5, typeID: 8, kind: "leave", first: "2026-09-07", last: "2026-09-11", percent: 25, allowPercent: true},
			{id: 4, typeID: 3, kind: "sick_certified", first: "2026-09-07", last: "2026-09-11", degree: 60},
		}, 0, 3000},
	} {
		s := sharesOnDay(t, "2026-09-08", c.absences...)
		if s.Holiday != c.holiday || s.sick() != c.sick {
			t.Errorf("%s: holiday %d, sick %d; want %d and %d", c.name, s.Holiday, s.sick(), c.holiday, c.sick)
		}
	}
}

// The sick-leave rate in the SSB and NAV manner: lost working days by the
// full-time percentage over the agreed working days — those not on holiday
// or leave. Two weeks at 100 %: ferie Monday and Tuesday with a 50 %
// sykmelding the whole first week (the ferie days count no sickness), a
// 100 % leave Monday to Wednesday of the second week: 1.5 sick days over the
// 5 agreed, 30 %.
func TestSickRate_AgreedDaysLeaveHolidayAndLeaveOut(t *testing.T) {
	t.Parallel()
	e := payrollEmployment(t, 1, "2024-01-01", "")
	r := register{
		employments: []store.StaffEmployment{e},
		chains:      map[int64][]store.StaffEmploymentTerm{1: {payrollTermsRow(t, 1, "2024-01-01", 100)}},
	}
	var rows []store.AbsencesBetweenRow
	for _, a := range []payrollAbsence{
		{id: 1, typeID: 1, kind: "holiday", first: "2026-09-07", last: "2026-09-08"},
		{id: 2, typeID: 3, kind: "sick_certified", first: "2026-09-07", last: "2026-09-11", degree: 50},
		{id: 3, typeID: 7, kind: "leave", first: "2026-09-14", last: "2026-09-16"},
	} {
		rows = append(rows, a.row(t))
	}
	if got := sickRate(r, rows, day(t, "2026-09-07"), day(t, "2026-09-18")); got != 30 {
		t.Errorf("sickRate = %v, want 30", got)
	}
}

// Graded sickness and egenmelding over ferie count as holiday in the
// payroll totals, not as sick days.
func TestPayrollTotals_FerieHoldsAgainstGradedSickness(t *testing.T) {
	t.Parallel()
	r := payrollWithAbsences(t,
		payrollAbsence{id: 1, typeID: 1, kind: "holiday", first: "2026-09-07", last: "2026-09-11"},
		payrollAbsence{id: 2, typeID: 3, kind: "sick_certified", first: "2026-09-07", last: "2026-09-09", degree: 50},
		payrollAbsence{id: 3, typeID: 2, kind: "sick_self", first: "2026-09-10", last: "2026-09-11"},
	)[0]
	if r.HolidayDays != "5,00" || r.CertifiedSickDays != "" || r.SelfCertifiedSickDays != "" {
		t.Errorf("holiday %q, certified %q, self-certified %q; want 5,00 and none", r.HolidayDays, r.CertifiedSickDays, r.SelfCertifiedSickDays)
	}
}

// A leave continues across a gap made only of days off: Monday 7 to Friday
// 11 September and Monday 14 to Friday 25 are one leave of nineteen days,
// under the lowest id of its rows — the id that stays when a row is added
// to the chain — not two.
func TestPayrollLeave_AWeekendDoesNotBreakALeave(t *testing.T) {
	t.Parallel()
	got := leaveRows(payrollWithAbsences(t,
		payrollAbsence{id: 31, typeID: 8, kind: "leave", key: "velferdspermisjon", first: "2026-09-07", last: "2026-09-11"},
		payrollAbsence{id: 30, typeID: 8, kind: "leave", key: "velferdspermisjon", first: "2026-09-14", last: "2026-09-25"},
	))
	want := [5]string{"L30", "2026-09-07", "2026-09-25", "andreIkkeLovfestedePermisjoner", "100,00"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("leave rows = %v, want the one %v", got, want)
	}
}

// The twelve months' sum counts each calendar day once: two eight-day
// leaves of two types over the same days are eight days, not sixteen, and
// neither is reported.
func TestPayrollLeave_TheTwelveMonthsCountDistinctDays(t *testing.T) {
	t.Parallel()
	got := leaveRows(payrollWithAbsences(t,
		payrollAbsence{id: 40, typeID: 8, kind: "leave", key: "velferdspermisjon", first: "2026-09-07", last: "2026-09-14", percent: 50, allowPercent: true},
		payrollAbsence{id: 41, typeID: 7, kind: "leave", key: "foreldrepermisjon", first: "2026-09-07", last: "2026-09-14", percent: 50, allowPercent: true},
	))
	if len(got) != 0 {
		t.Errorf("leave rows = %v, want none: eight distinct days", got)
	}
}

// Care days count by their percent, beyond the employer's ten as well:
// ten whole days in August, then two half days in September — one day used
// in the month, all of it beyond.
func TestPayrollTotals_PartialCareDays(t *testing.T) {
	t.Parallel()
	r := payrollWithAbsences(t,
		payrollAbsence{id: 1, typeID: 10, kind: "child_sick", first: "2026-08-17", last: "2026-08-28"},
		payrollAbsence{id: 2, typeID: 10, kind: "child_sick", first: "2026-09-21", last: "2026-09-22", percent: 50},
	)[0]
	if r.ChildSickDays != "1,00" || r.ChildSickBeyondEmployer != "1,00" {
		t.Errorf("child sick %q, beyond %q; want 1,00 and 1,00", r.ChildSickDays, r.ChildSickBeyondEmployer)
	}
}

// A person with two employments in force has the days beyond the
// employer's ten — the person's, refunded once — on one: the main one, the
// highest full-time percentage, ties to the lowest employment number. The
// other totals are on each.
func TestPayrollTotals_CareDaysBeyondOnTheMainEmployment(t *testing.T) {
	t.Parallel()
	m := september(t)
	main, side := payrollEmployment(t, 1, "2024-01-01", ""), payrollEmployment(t, 2, "2024-01-01", "")
	chains := map[int64][]store.StaffEmploymentTerm{
		1: {payrollTermsRow(t, 1, "2024-01-01", 40)},
		2: {payrollTermsRow(t, 2, "2024-01-01", 60)},
	}
	var rows []store.AbsencesBetweenRow
	for _, a := range []payrollAbsence{
		{id: 1, typeID: 10, kind: "child_sick", first: "2026-08-17", last: "2026-08-28"},
		{id: 2, typeID: 10, kind: "child_sick", first: "2026-09-21", last: "2026-09-22"},
	} {
		rows = append(rows, a.row(t))
	}
	org := "923609016"
	out := payrollRows(payrollInput{
		People:      map[int64]payrollPerson{1: {EmployeeNumber: "K-1", GivenName: "Kari", FamilyName: "Nordmann"}},
		Employments: []store.StaffEmployment{main, side},
		Terms:       chains,
		Units:       map[int64]store.StaffBusinessUnit{1: {ID: 1, OrgNumber: &org}},
		Absences:    rows,
	}, m)
	got := map[string][2]string{}
	for _, r := range out {
		got[r.EmploymentNumber] = [2]string{r.ChildSickDays, r.ChildSickBeyondEmployer}
	}
	if got[side.EmploymentNumber] != [2]string{"2,00", "2,00"} || got[main.EmploymentNumber] != [2]string{"2,00", ""} {
		t.Errorf("child sick, beyond per employment = %v; want both days on each, the two beyond on %s (60 %%) alone", got, side.EmploymentNumber)
	}
}
