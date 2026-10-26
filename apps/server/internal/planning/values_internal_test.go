package planning

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// TestWeeks is the week arithmetic every view shares: a week runs Monday to
// Sunday (S6), and the weeks of a window are the Mondays it touches — across
// a year's end, where ISO week 53 of 2026 runs into 2027.
func TestWeeks(t *testing.T) {
	for _, tc := range []struct{ day, monday, sunday string }{
		{"2026-10-26", "2026-10-26", "2026-11-01"}, // a Monday
		{"2026-11-01", "2026-10-26", "2026-11-01"}, // a Sunday
		{"2026-10-29", "2026-10-26", "2026-11-01"},
		{"2027-01-01", "2026-12-28", "2027-01-03"},
	} {
		if got := mondayOf(day(tc.day)); !got.Equal(day(tc.monday)) {
			t.Errorf("mondayOf(%s) = %s, want %s", tc.day, got.Format(time.DateOnly), tc.monday)
		}
		if got := sundayOf(day(tc.day)); !got.Equal(day(tc.sunday)) {
			t.Errorf("sundayOf(%s) = %s, want %s", tc.day, got.Format(time.DateOnly), tc.sunday)
		}
	}
	got := mondays(mondaysOf(day("2026-12-23"), day("2027-01-05")))
	if want := []string{"2026-12-21", "2026-12-28", "2027-01-04"}; !slices.Equal(got, want) {
		t.Errorf("mondaysOf = %v, want %v", got, want)
	}
	if got := mondaysOf(day("2026-12-23"), day("2026-12-01")); len(got) != 0 {
		t.Errorf("mondaysOf of an empty window = %v, want none", got)
	}
}

// TestDates is the civil date round trips: a contract date, a date column and
// "today" in the business zone all meet at midnight UTC (rule 1).
func TestDates(t *testing.T) {
	d := day("2026-10-26")
	if got := dayOf(apiDate(d)); !got.Equal(d) {
		t.Errorf("dayOf(apiDate) = %s, want %s", got, d)
	}
	if got := civil(dayParam(d)); !got.Equal(d) {
		t.Errorf("civil(dayParam) = %s, want %s", got, d)
	}
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	// 23:30 UTC on the 25th is 00:30 on the 26th in Oslo.
	if got := civilToday(time.Date(2026, 10, 25, 23, 30, 0, 0, time.UTC), oslo); !got.Equal(d) {
		t.Errorf("civilToday = %s, want %s", got, d)
	}
}

// TestLoad is the two-decimal load (S2): stored exactly as typed, read back
// as the number, and a third decimal refused.
func TestLoad(t *testing.T) {
	for _, v := range []float64{0.01, 37.5, 80, 99.99, 100} {
		h, ok := hundredthsOf(v)
		if !ok || loadOf(numericOfHundredths(h)) != v {
			t.Errorf("%v round-trips as %v (ok %v)", v, loadOf(numericOfHundredths(h)), ok)
		}
	}
	if _, ok := hundredthsOf(12.345); ok {
		t.Error("12.345 was accepted, want three decimals refused")
	}
}

// TestParseIfMatch is the revision an If-Match header carries, bare, quoted
// or weakly tagged; anything else is none.
func TestParseIfMatch(t *testing.T) {
	for raw, want := range map[string]int32{"3": 3, `"3"`: 3, `W/"12"`: 12, " 7 ": 7} {
		if got, ok := parseIfMatch(raw); !ok || got != want {
			t.Errorf("parseIfMatch(%q) = %d, %v; want %d", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "0", "-1", "abc", `"x"`} {
		if _, ok := parseIfMatch(raw); ok {
			t.Errorf("parseIfMatch(%q) accepted, want refused", raw)
		}
	}
}

// TestRefusals is the shapes of §4.7: a refusal carried out of a transaction
// keeps its status and problem, and each problem carries its status and code.
func TestRefusals(t *testing.T) {
	if r, ok := asRefusal(notFound()); !ok || r.status != http.StatusNotFound {
		t.Errorf("notFound = %+v, want a bare 404", r)
	}
	var f fieldErrors
	f.addCoded("toDate", codePeriodTooLong, "The period is at most 1096 days")
	f.add("load", "")
	if r, ok := asRefusal(refuseInvalid(invalidAllocationTitle, f)); !ok || r.status != http.StatusBadRequest ||
		*r.problem.Code != codePeriodTooLong || len(*r.problem.Errors) != 1 {
		t.Errorf("refuseInvalid = %+v, want a 400 period_too_long on toDate alone", r)
	}
	if r, ok := asRefusal(checkRevision("allocation", 4, 3)); !ok || r.status != http.StatusConflict || *r.problem.Code != codeRevisionConflict {
		t.Errorf("checkRevision = %+v, want a 409 revision_conflict", r)
	}
	if err := checkRevision("allocation", 4, 4); err != nil {
		t.Errorf("checkRevision of the current revision = %v, want nil", err)
	}
	if r, ok := asRefusal(refuseConflict(conflict(codeProjectClosed, "Project closed", "x"))); !ok || *r.problem.Code != codeProjectClosed {
		t.Errorf("refuseConflict = %+v, want project_closed", r)
	}
	if p := noPerson(); *p.Status != http.StatusNotFound || *p.Code != codeNoPerson {
		t.Errorf("noPerson = %+v, want a 404 no_person", p)
	}
	if p := dependencyUnavailable(); *p.Status != http.StatusServiceUnavailable || *p.Code != codeDependencyUnavailable {
		t.Errorf("dependencyUnavailable = %+v, want a 503 dependency_unavailable", p)
	}
}

// TestCodesAreTheContracts holds the codes this module answers against the
// contract that documents them: every refusal code named in PlanningProblem's
// description, and the warning codes exactly PlanningWarning's enum — so a
// code added here and not there, or there and not here, fails.
func TestCodesAreTheContracts(t *testing.T) {
	doc, err := openapi.Load(context.Background(), "planning")
	if err != nil {
		t.Fatal(err)
	}
	described := doc.Components.Schemas["PlanningProblem"].Value.Description
	for _, code := range []string{codeOutsideEmployment, codeProjectClosed, codeOverbooked, codePeriodTooLong,
		codeRevisionConflict, codeNoPerson, codeDependencyUnavailable} {
		if !strings.Contains(described, code) {
			t.Errorf("PlanningProblem's description does not name %s", code)
		}
	}
	var enum []string
	for _, v := range doc.Components.Schemas["PlanningWarning"].Value.Properties["code"].Value.Enum {
		enum = append(enum, v.(string))
	}
	if want := []string{warnOverbooked, warnBookedWhileAway, warnOutsideProjectDates}; !slices.Equal(enum, want) {
		t.Errorf("PlanningWarning's codes = %v, want %v", enum, want)
	}
}
