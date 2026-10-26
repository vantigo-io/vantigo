package planning

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// This file is an allocation's field rules (design S2–S5, §4.7) and the
// judgements a write makes over what it read before its transaction: the
// window the Workforce reads may be asked about (S8, amendment 14), the
// employment a period must lie in (S4), the closed project (S4) and the
// warnings (rule 6). Every rule here is pure; the handlers of
// allocations_write.go read what they judge.

// The bounds of an allocation's fields (S2, S4, S5).
const (
	maxPeriodDays       = 1096
	maxPercentHundredth = 100_00
	maxHoursHundredth   = 80_00
	maxTitleRunes       = 100
	maxNoteRunes        = 500
)

// maxAllocationsListed is the most rows GET /allocations answers (§6).
const maxAllocationsListed = 500

// The statuses a project takes no new row and no change in but deletion (S4).
var closedProjectStatuses = []string{"completed", "cancelled"}

// allocationStatuses is what status takes, as the contract names it.
var allocationStatuses = []string{statusTentative, statusConfirmed}

// allocationFields is a valid PlanningAllocationInput: the load in
// hundredths, the period at midnight UTC, status nil when it was left out
// (the create's default by the project, S3), title and note nil when empty.
type allocationFields struct {
	personID    int64
	projectID   int32
	from, to    time.Time
	loadUnit    string
	load        int64
	status      *string
	title, note *string
}

// parseAllocation validates an allocation's body (S2–S5): every rule a field
// error on its own field, every failure reported at once, period_too_long
// coded on toDate. requireStatus is the PUT's whole replace, where status is
// one of the required fields.
func parseAllocation(body gen.PlanningAllocationInput, requireStatus bool) (allocationFields, fieldErrors) {
	var f fieldErrors
	a := allocationFields{personID: body.PersonId, projectID: body.ProjectId, loadUnit: string(body.LoadUnit)}
	if body.PersonId <= 0 {
		f.add("personId", "personId is required and must be a person's id")
	}
	if body.ProjectId <= 0 {
		f.add("projectId", "projectId is required and must be a project's id")
	}
	a.from, a.to = dayOf(body.FromDate), dayOf(body.ToDate)
	if body.FromDate.IsZero() {
		f.add("fromDate", "fromDate is required")
	}
	if body.ToDate.IsZero() {
		f.add("toDate", "toDate is required")
	}
	if !body.FromDate.IsZero() && !body.ToDate.IsZero() {
		f.merge(periodErrors(a.from, a.to))
	}
	if !slices.Contains(loadUnits, a.loadUnit) {
		f.add("loadUnit", fmt.Sprintf("loadUnit must be one of %s, but was '%s'", strings.Join(loadUnits, ", "), a.loadUnit))
	} else {
		load, message := loadOfInput(a.loadUnit, body.Load)
		a.load = load
		f.add("load", message)
	}
	switch {
	case body.Status != nil:
		a.status = enumField(&f, "status", (*string)(body.Status), allocationStatuses)
	case requireStatus:
		f.add("status", "status is required: tentative or confirmed")
	}
	a.title = textField(&f, "title", body.Title, maxTitleRunes)
	a.note = textField(&f, "note", body.Note, maxNoteRunes)
	return a, f
}

// periodErrors is S4's rule on a period's own dates: to on or after from, at
// most maxPeriodDays after it.
func periodErrors(from, to time.Time) fieldErrors {
	var f fieldErrors
	switch {
	case to.Before(from):
		f.add("toDate", "toDate must be on or after fromDate")
	case daysBetween(from, to) > maxPeriodDays:
		f.addCoded("toDate", codePeriodTooLong, fmt.Sprintf("An allocation runs at most %d days; toDate is %d days after fromDate", maxPeriodDays, daysBetween(from, to)))
	}
	return f
}

// daysBetween is how many days to is after from, both at midnight UTC.
func daysBetween(from, to time.Time) int {
	return int(to.Sub(from).Hours() / 24)
}

// loadOfInput is a load as hundredths and the message its fault answers
// (S2): more than 0, at most 100 as a percent and 80 as hours a week, two
// decimals at most.
func loadOfInput(unit string, v float64) (int64, string) {
	h, ok := hundredthsOf(v)
	if !ok {
		return 0, "load takes at most two decimals"
	}
	limit, what := int64(maxPercentHundredth), "100 as percent"
	if unit == loadUnitHours {
		limit, what = maxHoursHundredth, "80 as hours_per_week"
	}
	if h <= 0 || h > limit {
		return 0, fmt.Sprintf("load must be more than 0 and at most %s", what)
	}
	return h, ""
}

// textField is an optional text of at most limit characters: nil, or empty
// once trimmed, is no text.
func textField(f *fieldErrors, field string, raw *string, limit int) *string {
	if raw == nil {
		return nil
	}
	v := strings.TrimSpace(*raw)
	if v == "" {
		return nil
	}
	if utf8.RuneCountInString(v) > limit {
		f.add(field, fmt.Sprintf("%s is at most %d characters", field, limit))
		return nil
	}
	return &v
}

// defaultStatus is S3's default on a create: tentative on a planned project,
// confirmed on any other.
func defaultStatus(project contracts.ProjectEntry) string {
	if project.Status == "planned" {
		return statusTentative
	}
	return statusConfirmed
}

// projectClosed reports whether a project takes no new row and no change
// (S4).
func projectClosed(project *contracts.ProjectEntry) bool {
	return project != nil && slices.Contains(closedProjectStatuses, project.Status)
}

// refuseProjectClosed is the 409 a write on a closed project answers.
func refuseProjectClosed(project *contracts.ProjectEntry) error {
	return refuseConflict(conflict(codeProjectClosed, "The project is closed",
		fmt.Sprintf("Project %s is %s: it takes no new allocation and no change, only deletion.", project.Code, project.Status)))
}

// workforceBound is the window Staff answers People and Capacity over
// (S8, amendment 14): from the Monday of the week thirteen weeks before today
// to thirty-six months after today, by Staff's month rule.
func workforceBound(today time.Time) (time.Time, time.Time) {
	return addDays(mondayOf(today), -7*13), monthsAfter(today, 36)
}

// monthsAfter is d moved n months, the day clamped to the target month's
// last: Staff's rule for the bound's far end, so a window this module asks
// about never ends a day past it.
func monthsAfter(d time.Time, n int) time.Time {
	y, m, day := d.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(day, last), 0, 0, 0, 0, time.UTC)
}

// clipToBound is from..to cut to the Workforce bound of today, and false
// when nothing of it is inside: the window a write or a read asks Staff
// about, so a row that started long ago, or a padding week past the far end,
// never makes the read refuse.
func clipToBound(from, to, today time.Time) (time.Time, time.Time, bool) {
	lo, hi := workforceBound(today)
	from, to = latestOf(from, lo), earliestOf(to, hi)
	return from, to, !to.Before(from)
}

// boundErrors is the field error of a period with no day inside the
// Workforce bound: Staff can answer nothing about it, neither the person
// nor their employment.
func boundErrors(from, to, today time.Time) fieldErrors {
	var f fieldErrors
	lo, hi := workforceBound(today)
	if to.Before(lo) {
		f.addCoded("fromDate", codeWindowOutOfBounds, fmt.Sprintf("The period must reach %s or later: Staff answers no employment before it", lo.Format(time.DateOnly)))
	}
	if from.After(hi) {
		f.addCoded("toDate", codeWindowOutOfBounds, fmt.Sprintf("The period must start on %s or earlier: Staff answers no employment after it", hi.Format(time.DateOnly)))
	}
	return f
}

func latestOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earliestOf(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// personIn is personID among people, nil when Staff did not answer them.
func personIn(people []contracts.PersonEntry, personID int64) *contracts.PersonEntry {
	for i := range people {
		if people[i].ID == personID {
			return &people[i]
		}
	}
	return nil
}

// employedThroughout reports whether every day of from..to inside lo..hi —
// the window Staff was asked about — lies in one of employed (S4). A day
// outside the window is not judged: Staff answers nothing about it.
func employedThroughout(employed []contracts.DateSpan, from, to, lo, hi time.Time) bool {
	from, to = latestOf(from, lo), earliestOf(to, hi)
	for day := from; !day.After(to); day = addDays(day, 1) {
		if !inSpans(employed, day) {
			return false
		}
	}
	return true
}

// refuseOutsideEmployment is the 409 a period with a day outside the
// person's employment answers (S4).
func refuseOutsideEmployment() error {
	return refuseConflict(conflict(codeOutsideEmployment, "Outside the employment",
		"A day of the period lies outside the person's employment as Staff answers it."))
}

// refuseOverbooked is the 409 a write answers under the setting refuse when
// it makes a week's excess over capacity grow (S7); nothing is written.
func refuseOverbooked(weeks []time.Time) error {
	names := make([]string, len(weeks))
	for i, w := range weeks {
		names[i] = w.Format(time.DateOnly)
	}
	return refuseConflict(conflict(codeOverbooked, "Overbooked",
		"The allocation would book more than the person's capacity in the weeks of "+strings.Join(names, ", ")+
			", and the Planning settings refuse overbooking."))
}

// allocationWarnings is rule 6's warnings of a write: S7's two judgements, and
// outside_project_dates when the project has dates and the period leaves
// them. Never nil, since the contract's list is required.
func allocationWarnings(o overbooking, project *contracts.ProjectEntry, from, to time.Time) []gen.PlanningWarning {
	out := []gen.PlanningWarning{}
	if len(o.Over) > 0 {
		out = append(out, gen.PlanningWarning{Code: warnOverbooked, Weeks: apiDates(o.Over)})
	}
	if len(o.WhileAway) > 0 {
		out = append(out, gen.PlanningWarning{Code: warnBookedWhileAway, Weeks: apiDates(o.WhileAway)})
	}
	if outsideProjectDates(project, from, to) {
		out = append(out, gen.PlanningWarning{Code: warnOutsideProjectDates, Detail: projectDatesDetail(project)})
	}
	return out
}

// nonCompeteHit is the confirmed row whose terms bar a booking (S17.3): its
// id and the non-compete's until date.
type nonCompeteHit struct {
	ID    int64
	Until time.Time
}

// refuseNonCompete is the 409 a write answers when it would place a
// confirmed row of the person on a customer's project their non-compete
// bars (S17.3); nothing is written.
func refuseNonCompete(hit *nonCompeteHit) error {
	return refuseConflict(conflict(codeNonCompete, "The person is under a non-compete",
		fmt.Sprintf("Allocation %d bars a booking on this customer's projects until %s.", hit.ID, hit.Until.Format(time.DateOnly))))
}

// verdict is what a write judged under the person's lock: S7's overbooking
// and, for a row written tentative or a confirmed row the write does not
// place (amendment 90), S17.3's non-compete — the case that refuses a
// confirmed row placed — carried out to the warnings.
type verdict struct {
	overbooking
	nonCompete *nonCompeteHit
}

// verdictWarnings is rule 6's warnings of a write: allocationWarnings', and
// non_compete on a row the non-compete did not refuse, naming the binding
// row and its until date.
func verdictWarnings(v verdict, project *contracts.ProjectEntry, from, to time.Time) []gen.PlanningWarning {
	out := allocationWarnings(v.overbooking, project, from, to)
	if hit := v.nonCompete; hit != nil {
		out = append(out, gen.PlanningWarning{Code: warnNonCompete, AllocationId: ptrTo(hit.ID), Date: ptrTo(apiDate(hit.Until))})
	}
	return out
}

// outsideProjectDates reports whether from..to leaves the project's
// startDate..endDate, judged on the ends it has.
func outsideProjectDates(project *contracts.ProjectEntry, from, to time.Time) bool {
	if project == nil {
		return false
	}
	return (project.StartDate != nil && from.Before(*project.StartDate)) ||
		(project.EndDate != nil && to.After(*project.EndDate))
}

// projectDatesDetail is the sentence naming the project's dates.
func projectDatesDetail(project *contracts.ProjectEntry) *string {
	day := func(t *time.Time) string {
		if t == nil {
			return "…"
		}
		return t.Format(time.DateOnly)
	}
	s := fmt.Sprintf("The project runs from %s to %s.", day(project.StartDate), day(project.EndDate))
	return &s
}

// apiDates is days as the contract's dates.
func apiDates(days []time.Time) *[]openapi_types.Date {
	out := make([]openapi_types.Date, len(days))
	for i, d := range days {
		out[i] = apiDate(d)
	}
	return &out
}

// capacityRowOf is fields as the capacity calculation reads them.
func capacityRowOf(a allocationFields) capacityRow {
	return capacityRow{From: a.from, To: a.to, LoadUnit: a.loadUnit, Load: float64(a.load) / 100}
}

// othersThan is rows without the one of id, as the capacity calculation
// reads them: the person's other confirmed rows (S7).
func othersThan(rows []store.PlanningAllocation, id int64) []capacityRow {
	var out []capacityRow
	for _, r := range rows {
		if r.ID != id {
			out = append(out, rowOf(r))
		}
	}
	return out
}
