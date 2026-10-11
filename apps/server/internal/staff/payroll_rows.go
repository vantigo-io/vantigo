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
}

// payrollColumns is the file's header (plan, Exact values): sentence-case
// English, the Expenses style, in this order. Delivery B appends the leave
// and absence columns after Pay grade — to the right, so a payroll system's
// import mapping of these nineteen keeps working — and payrollRow.cells
// grows with it.
var payrollColumns = []string{
	"Employee number", "Name", "Employment number", "Business unit org number", "Start", "End", "End reason",
	"Occupation code", "Full-time hours", "FTE percent", "FTE changed", "Working hours scheme", "Appointment form",
	"Remuneration type", "Salary changed", "Department", "Cost centre", "Seniority date", "Pay grade",
}

// payrollRow is one employment's line of the file, every cell as the file
// carries it: dates ISO, numbers with the decimal comma, the codes
// Skatteetaten's, a value there is none of the empty string. Delivery B's
// leave and absence cells go after PayGrade, and its rows per leave over
// fourteen days (S18) beside these.
type payrollRow struct {
	EmployeeNumber, Name, EmploymentNumber, BusinessUnitOrgNumber  string
	Start, End, EndReason                                          string
	OccupationCode, FullTimeHours, FTEPercent, FTEChanged          string
	WorkingHoursScheme, AppointmentForm, RemunerationType          string
	SalaryChanged, Department, CostCentre, SeniorityDate, PayGrade string
}

// cells is the row in payrollColumns' order.
func (r payrollRow) cells() []string {
	return []string{
		r.EmployeeNumber, r.Name, r.EmploymentNumber, r.BusinessUnitOrgNumber, r.Start, r.End, r.EndReason,
		r.OccupationCode, r.FullTimeHours, r.FTEPercent, r.FTEChanged, r.WorkingHoursScheme, r.AppointmentForm,
		r.RemunerationType, r.SalaryChanged, r.Department, r.CostCentre, r.SeniorityDate, r.PayGrade,
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
		out = append(out, r)
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
