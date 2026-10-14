package staff

import (
	"cmp"
	"regexp"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is what the payroll file says for a month (design S18; plan
// Task 4, reading 12): one row per employment in the month, with the terms in
// force on its last day. Every function here is pure over rows already
// loaded, as terms_derived.go's are, so the rules are table-tested without a
// database; the loading is payroll.go's.

// payrollMonthPattern is the one shape a month may have: YYYY-MM, the month
// 01 to 12.
var payrollMonthPattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// payrollMonth is a calendar month: its name as the request gave it and its
// first and last days, civil dates at midnight UTC.
type payrollMonth struct {
	Name        string
	First, Last time.Time
}

// parsePayrollMonth is raw as a month, false for anything that is not one.
func parsePayrollMonth(raw string) (payrollMonth, bool) {
	if !payrollMonthPattern.MatchString(raw) {
		return payrollMonth{}, false
	}
	first, err := time.Parse("2006-01", raw)
	if err != nil {
		return payrollMonth{}, false
	}
	return payrollMonth{Name: raw, First: first, Last: first.AddDate(0, 1, -1)}, true
}

// payrollPerson is what the file reads of a person: the employee number and
// the names. The sealed identifiers are not read at all — payroll already
// holds them (design reading 9).
type payrollPerson struct {
	EmployeeNumber, GivenName, FamilyName string
}

// payrollInput is what one export reads in its snapshot.
type payrollInput struct {
	People      map[int64]payrollPerson
	Employments []store.StaffEmployment
	Terms       map[int64][]store.StaffEmploymentTerm // by employment, oldest first
	Units       map[int64]store.StaffBusinessUnit
	Departments map[int64]store.StaffDepartment
	// Absences is every approved and registered absence (delivery B): the
	// month's totals, the year's child-sick days before it and the leaves
	// whose chains reach into it are all judged on them.
	Absences []store.AbsencesBetweenRow
	// LeaveID is a leave chain's Leave ID from its first row's id: a stable
	// surrogate, never the row's own id (whole-branch review).
	LeaveID func(firstRowID int64) string
}

// payrollColumns is the file's header (plan, Exact values): sentence-case
// English, the Expenses style, in this order. Delivery B appends the leave
// and absence columns after Pay grade — to the right, so a payroll system's
// import mapping of the first nineteen keeps working (plan Task 6, reading
// 8): a leave's five cells, filled on its own row, then the month's totals
// per kind, fixed, filled on the employment's.
var payrollColumns = []string{
	"Employee number", "Name", "Employment number", "Business unit org number", "Start", "End", "End reason",
	"Occupation code", "Full-time hours", "FTE percent", "FTE changed", "Working hours scheme", "Appointment form",
	"Remuneration type", "Salary changed", "Department", "Cost centre", "Seniority date", "Pay grade",
	"Leave ID", "Leave start", "Leave end", "Leave description", "Leave percent",
	"Holiday days", "Self-certified sick days", "Certified sick days", "Child sick days",
	"Child sick days beyond employer", "Leave days", "Time off hours", "Other days",
}

// payrollRow is one line of the file, every cell as the file carries it:
// dates ISO, numbers with the decimal comma, the codes Skatteetaten's, a
// value there is none of — a total of nothing included — the empty string.
// An employment's line fills the A1 cells and the month's totals; a leave's
// line (S18) repeats the employment's three keys and fills the leave cells.
type payrollRow struct {
	EmployeeNumber, Name, EmploymentNumber, BusinessUnitOrgNumber  string
	Start, End, EndReason                                          string
	OccupationCode, FullTimeHours, FTEPercent, FTEChanged          string
	WorkingHoursScheme, AppointmentForm, RemunerationType          string
	SalaryChanged, Department, CostCentre, SeniorityDate, PayGrade string

	LeaveID, LeaveStart, LeaveEnd, LeaveDescription, LeavePercent string

	HolidayDays, SelfCertifiedSickDays, CertifiedSickDays, ChildSickDays string
	ChildSickBeyondEmployer, LeaveDays, TimeOffHours, OtherDays          string
}

// cells is the row in payrollColumns' order.
func (r payrollRow) cells() []string {
	return []string{
		r.EmployeeNumber, r.Name, r.EmploymentNumber, r.BusinessUnitOrgNumber, r.Start, r.End, r.EndReason,
		r.OccupationCode, r.FullTimeHours, r.FTEPercent, r.FTEChanged, r.WorkingHoursScheme, r.AppointmentForm,
		r.RemunerationType, r.SalaryChanged, r.Department, r.CostCentre, r.SeniorityDate, r.PayGrade,
		r.LeaveID, r.LeaveStart, r.LeaveEnd, r.LeaveDescription, r.LeavePercent,
		r.HolidayDays, r.SelfCertifiedSickDays, r.CertifiedSickDays, r.ChildSickDays,
		r.ChildSickBeyondEmployer, r.LeaveDays, r.TimeOffHours, r.OtherDays,
	}
}

// inPayrollMonth reports whether e is in m: started on or before its last
// day, and open-ended or ended on or after its first.
func inPayrollMonth(e store.StaffEmployment, m payrollMonth) bool {
	return !civil(e.StartDate).After(m.Last) && (!e.EndDate.Valid || !e.EndDate.Time.Before(m.First))
}

// payrollTermsDay is the day e's terms are read on in m: the month's last
// day, or the end day when the employment ends earlier — the last day it
// still had terms.
func payrollTermsDay(e store.StaffEmployment, m payrollMonth) time.Time {
	if e.EndDate.Valid && e.EndDate.Time.Before(m.Last) {
		return e.EndDate.Time
	}
	return m.Last
}

// payrollRows is the month's rows: one per employment in it, ordered by
// employee number and then employment number. An employment with no terms
// row in force on the day — which validation does not let happen, the first
// row being dated the start — still has its row, its terms cells blank.
func payrollRows(in payrollInput, m payrollMonth) []payrollRow {
	var out []payrollRow
	leaveID := in.LeaveID
	if leaveID == nil {
		leaveID = func(int64) string { return "" }
	}
	absences := map[int64][]store.AbsencesBetweenRow{}
	for _, a := range in.Absences {
		absences[a.PersonID] = append(absences[a.PersonID], a)
	}
	employments := map[int64][]store.StaffEmployment{}
	for _, e := range in.Employments {
		employments[e.PersonID] = append(employments[e.PersonID], e)
	}
	for _, e := range in.Employments {
		if !inPayrollMonth(e, m) {
			continue
		}
		person := in.People[e.PersonID]
		r := payrollRow{
			EmployeeNumber:   person.EmployeeNumber,
			Name:             person.GivenName + " " + person.FamilyName,
			EmploymentNumber: e.EmploymentNumber,
			Start:            civil(e.StartDate).Format(time.DateOnly),
		}
		if unit, ok := in.Units[e.BusinessUnitID]; ok && unit.OrgNumber != nil {
			r.BusinessUnitOrgNumber = *unit.OrgNumber
		}
		// The end and its reason are reported in the month the employment
		// ends, and in no other.
		if e.EndDate.Valid && !e.EndDate.Time.Before(m.First) && !e.EndDate.Time.After(m.Last) {
			r.End = e.EndDate.Time.Format(time.DateOnly)
			if e.EndReason != nil {
				r.EndReason = endReasonCodes[*e.EndReason]
			}
		}
		chain := in.Terms[e.ID]
		on := payrollTermsDay(e, m)
		if t := termsInForce(e, chain, on); t != nil {
			payrollTerms(&r, *t, chain, on, in.Departments)
		}
		rows := absences[e.PersonID]
		payrollTotals(&r, rows, employments[e.PersonID], e, m, mainEmployment(employments[e.PersonID], in.Terms, m) == e.ID)
		out = append(out, r)
		for _, l := range reportedLeaves(rows, e, m) {
			out = append(out, payrollRow{
				EmployeeNumber: r.EmployeeNumber, Name: r.Name, EmploymentNumber: r.EmploymentNumber,
				LeaveID: leaveID(l.ID), LeaveStart: l.First.Format(time.DateOnly), LeaveEnd: l.Last.Format(time.DateOnly),
				LeaveDescription: l.Description, LeavePercent: payrollNumber(l.Percent),
			})
		}
	}
	slices.SortStableFunc(out, func(a, b payrollRow) int {
		return cmp.Or(cmp.Compare(a.EmployeeNumber, b.EmployeeNumber), cmp.Compare(a.EmploymentNumber, b.EmploymentNumber))
	})
	return out
}

// payrollTerms fills r's terms cells from t, the row of chain in force on
// on: the derived dates judged on the same day, the cost centre the terms'
// own else the department's. Salary changed is blank when t has no pay
// components (NULL, as the class A erase leaves them, or an empty list):
// salaryChangedAt would give the first row's date over a chain with no pay
// left, and a file reporting a pay change there is not would mislead payroll.
func payrollTerms(r *payrollRow, t store.StaffEmploymentTerm, chain []store.StaffEmploymentTerm, on time.Time, departments map[int64]store.StaffDepartment) {
	text := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	date := func(d *time.Time) string {
		if d == nil {
			return ""
		}
		return d.Format(time.DateOnly)
	}
	number := func(hundredths int64) string {
		v := float64(hundredths) / 100
		return formatCSVDecimal(&v)
	}
	r.OccupationCode = text(t.OccupationCode)
	r.FullTimeHours = number(hundredthsOfNumeric(t.FullTimeHoursPerWeek))
	r.FTEPercent = number(hundredthsOfNumeric(t.FtePercent))
	r.FTEChanged = date(fteChangedAt(chain, on))
	r.WorkingHoursScheme = workingHoursSchemeCodes[t.WorkingHoursScheme]
	r.AppointmentForm = appointmentFormCodes[t.AppointmentForm]
	r.RemunerationType = text(t.RemunerationType)
	if len(payKey(t.PayComponents)) > 0 {
		r.SalaryChanged = date(salaryChangedAt(chain, on))
	}
	r.CostCentre = text(t.CostCentre)
	if t.DepartmentID != nil {
		if d, ok := departments[*t.DepartmentID]; ok {
			r.Department = d.Code
			if r.CostCentre == "" {
				r.CostCentre = text(d.CostCentre)
			}
		}
	}
	r.SeniorityDate = date(civilPtr(t.SeniorityDate))
	r.PayGrade = text(t.PayGrade)
}

// payrollNumber is hundredths as the file carries a number, two decimals and
// the decimal comma.
func payrollNumber(hundredths int64) string {
	v := float64(hundredths) / 100
	return formatCSVDecimal(&v)
}

// payrollTotal is a total in hundredths of a percent of a day, as days in
// the file — the empty cell for none.
func payrollTotal(shares int64) string {
	if shares == 0 {
		return ""
	}
	return payrollNumber(shares / 100)
}

// payrollDays is the days of e in m: from the later of the month's first
// day and the start to the earlier of its last day and the end.
func payrollDays(e store.StaffEmployment, m payrollMonth) (time.Time, time.Time) {
	from, to := later(m.First, civil(e.StartDate)), m.Last
	if e.EndDate.Valid {
		to = earlier(to, e.EndDate.Time)
	}
	return from, to
}

// payrollTotals fills r's totals for the month (plan Task 6, reading 8):
// over each working day of e within m, each kind's share (sharesOn); the
// hours of time off; and of the child-sick days, those beyond the
// employer's ten of the year (ftrl. § 9-8) — the year's care days counted
// in order from 1 January by their percent, a day before the right's four
// weeks counting none. A person with two employments in force has their
// absence on each, as it is from both — but the days beyond the employer's
// ten, the person's and refunded once, on the main one alone (mainEmployment).
func payrollTotals(r *payrollRow, rows []store.AbsencesBetweenRow, employments []store.StaffEmployment, e store.StaffEmployment, m payrollMonth, main bool) {
	if len(rows) == 0 {
		return
	}
	from, to := payrollDays(e, m)
	var sum dayShares
	var hours int64
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		for _, a := range rows {
			if a.Kind == kindTimeOff && a.Unit == "hours" && civil(a.FirstDay).Equal(d) {
				hours += hundredthsOfNumeric(a.Hours)
			}
		}
		if !worksOn(d) {
			continue
		}
		s := sharesOn(rows, d)
		sum.Holiday += s.Holiday
		sum.SelfSick += s.SelfSick
		sum.CertifiedSick += s.CertifiedSick
		sum.ChildSick += s.ChildSick
		sum.Leave += s.Leave
		sum.Other += s.Other
	}
	r.HolidayDays, r.SelfCertifiedSickDays, r.CertifiedSickDays = payrollTotal(sum.Holiday), payrollTotal(sum.SelfSick), payrollTotal(sum.CertifiedSick)
	r.ChildSickDays = payrollTotal(sum.ChildSick)
	if main {
		r.ChildSickBeyondEmployer = payrollTotal(childSickBeyondEmployer(rows, employments, from, to))
	}
	r.LeaveDays, r.OtherDays = payrollTotal(sum.Leave), payrollTotal(sum.Other)
	if hours > 0 {
		r.TimeOffHours = payrollNumber(hours)
	}
}

// childSickBeyondEmployer is the share of the care days from..to beyond the
// employer's ten of their year, in hundredths of a percent of a day: each
// working day of a child-sick row on or after the right to care days, by its
// percent, summed from 1 January.
func childSickBeyondEmployer(rows []store.AbsencesBetweenRow, employments []store.StaffEmployment, from, to time.Time) int64 {
	spans := employmentSpans(employments)
	jan1 := time.Date(from.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	const bears = careDaysEmployerBears * wholeDay
	var used, beyond int64
	for d := jan1; !d.After(to); d = d.AddDate(0, 0, 1) {
		if !worksOn(d) {
			continue
		}
		share := sharesOn(rows, d).ChildSick
		if share == 0 {
			continue
		}
		if right := careDayRight(spans, d); right == nil || d.Before(*right) {
			continue
		}
		before := used
		used += share
		if !d.Before(from) && used > bears {
			beyond += used - max(before, bears)
		}
	}
	return beyond
}

// mainEmployment is the person's employment the person-level totals go on
// among employments in month m: the highest full-time percentage in force
// on its terms day, ties to the lowest employment number.
func mainEmployment(employments []store.StaffEmployment, chains map[int64][]store.StaffEmploymentTerm, m payrollMonth) int64 {
	var best int64
	bestFTE, bestNumber := int64(-1), ""
	for _, e := range employments {
		if !inPayrollMonth(e, m) {
			continue
		}
		var fte int64
		if t := termsInForce(e, chains[e.ID], payrollTermsDay(e, m)); t != nil {
			fte = hundredthsOfNumeric(t.FtePercent)
		}
		if fte > bestFTE || (fte == bestFTE && e.EmploymentNumber < bestNumber) {
			best, bestFTE, bestNumber = e.ID, fte, e.EmploymentNumber
		}
	}
	return best
}

// leave is one leave as the a-melding reports it (Skatteetaten,
// "Permisjon"): a chain of rows of one leave type with an a-melding
// description and one percent, each starting no later than the next working
// day after the one before ended — a weekend or a public holiday between
// them does not end it — with its whole first and last day, under the lowest
// id of its rows: the id that stays as rows are added to the chain, so the
// a-melding's permisjonId does not move from month to month.
type leave struct {
	ID          int64
	First, Last time.Time
	Description string
	TypeID      int64
	Percent     int64 // hundredths of a percent
}

// continues is whether a row from next continues a leave whose last day is
// last: it starts by the day after, or only days off lie between.
func continues(last, next time.Time) bool {
	for d := last.AddDate(0, 0, 1); d.Before(next); d = d.AddDate(0, 0, 1) {
		if worksOn(d) {
			return false
		}
	}
	return true
}

// leavesOf is the person's leaves, from rows.
func leavesOf(rows []store.AbsencesBetweenRow) []leave {
	var parts []store.AbsencesBetweenRow
	for _, a := range rows {
		if a.Kind == kindLeave && a.Unit != "hours" && a.AmeldingLeave != nil {
			parts = append(parts, a)
		}
	}
	slices.SortStableFunc(parts, func(a, b store.AbsencesBetweenRow) int {
		return cmp.Or(cmp.Compare(a.TypeID, b.TypeID), cmp.Compare(hundredthsOfNumeric(a.Percent), hundredthsOfNumeric(b.Percent)),
			civil(a.FirstDay).Compare(civil(b.FirstDay)), cmp.Compare(a.ID, b.ID))
	})
	var out []leave
	for _, a := range parts {
		percent := int64(wholeDay)
		if a.Percent.Valid {
			percent = hundredthsOfNumeric(a.Percent)
		}
		if n := len(out); n > 0 {
			l := &out[n-1]
			if l.TypeID == a.TypeID && l.Percent == percent && continues(l.Last, civil(a.FirstDay)) {
				l.Last = maxTime(l.Last, civil(a.LastDay))
				l.ID = min(l.ID, a.ID)
				continue
			}
		}
		out = append(out, leave{ID: a.ID, First: civil(a.FirstDay), Last: civil(a.LastDay), Description: *a.AmeldingLeave, TypeID: a.TypeID, Percent: percent})
	}
	slices.SortStableFunc(out, func(a, b leave) int { return cmp.Or(a.First.Compare(b.First), cmp.Compare(a.ID, b.ID)) })
	return out
}

// The a-melding's reporting rules for leave (Skatteetaten, "Permisjon" and
// "Permittering"; Revision 2 Q4d).
const (
	leaveReportedOverDays = 14
	descriptionLayOff     = "permittering"
)

// reportedLeaves is the leaves of rows the file reports on e's lines in m:
// those touching e's days in the month, and — a leave of fourteen days or
// fewer — only when the person's leaves within the twelve months to the
// month's end together pass fourteen days. Permittering is reported
// whatever its length. A child's sickness is never a leave: its kind is not
// leave, and no child-sick type carries a description.
func reportedLeaves(rows []store.AbsencesBetweenRow, e store.StaffEmployment, m payrollMonth) []leave {
	leaves := leavesOf(rows)
	windowFrom := m.Last.AddDate(-1, 0, 1)
	// The twelve months count each calendar day once, however many leaves
	// lie on it.
	days := map[time.Time]bool{}
	for _, l := range leaves {
		for d := maxTime(l.First, windowFrom); !d.After(minTime(l.Last, m.Last)); d = d.AddDate(0, 0, 1) {
			days[d] = true
		}
	}
	total := len(days)
	from, to := payrollDays(e, m)
	var out []leave
	for _, l := range leaves {
		if l.First.After(to) || l.Last.Before(from) {
			continue
		}
		if l.Description == descriptionLayOff || calendarDays(l.First, l.Last) > leaveReportedOverDays || total > leaveReportedOverDays {
			out = append(out, l)
		}
	}
	return out
}
