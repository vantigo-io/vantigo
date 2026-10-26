package planning

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// validInput is an allocation body every rule accepts.
func validInput() gen.PlanningAllocationInput {
	return gen.PlanningAllocationInput{
		PersonId: 1001, ProjectId: 2001,
		FromDate: openapi_types.Date{Time: day("2026-10-05")}, ToDate: openapi_types.Date{Time: day("2026-10-30")},
		LoadUnit: gen.Percent, Load: 50,
	}
}

// TestParseAllocation is S2–S5's field rules, each a field error on its own
// field, and the text fields trimmed, an empty one no text.
func TestParseAllocation(t *testing.T) {
	title, blank := "  Utvikler  ", "   "
	in := validInput()
	in.Title, in.Note = &title, &blank
	got, f := parseAllocation(in, false)
	if f.failed() {
		t.Fatalf("a valid body failed: %+v", f.errs)
	}
	if got.load != 5000 || got.title == nil || *got.title != "Utvikler" || got.note != nil || got.status != nil {
		t.Errorf("parsed = %+v, want 5000 hundredths, the title trimmed, no note, no status", got)
	}
	if _, f := parseAllocation(validInput(), true); len(f.errs["status"]) == 0 {
		t.Errorf("a replace without status passed: %+v", f.errs)
	}

	bad := func(change func(*gen.PlanningAllocationInput)) gen.PlanningAllocationInput {
		in := validInput()
		change(&in)
		return in
	}
	long := strings.Repeat("é", 101)
	maybe := gen.PlanningAllocationStatus("maybe")
	for _, tc := range []struct {
		name  string
		in    gen.PlanningAllocationInput
		field string
	}{
		{"no person", bad(func(in *gen.PlanningAllocationInput) { in.PersonId = 0 }), "personId"},
		{"no project", bad(func(in *gen.PlanningAllocationInput) { in.ProjectId = 0 }), "projectId"},
		{"no from", bad(func(in *gen.PlanningAllocationInput) { in.FromDate = openapi_types.Date{} }), "fromDate"},
		{"to before from", bad(func(in *gen.PlanningAllocationInput) { in.ToDate = openapi_types.Date{Time: day("2026-10-04")} }), "toDate"},
		{"a unit", bad(func(in *gen.PlanningAllocationInput) { in.LoadUnit = "days" }), "loadUnit"},
		{"a load of 0", bad(func(in *gen.PlanningAllocationInput) { in.Load = 0 }), "load"},
		{"a status", bad(func(in *gen.PlanningAllocationInput) { in.Status = &maybe }), "status"},
		{"a title of 101", bad(func(in *gen.PlanningAllocationInput) { in.Title = &long }), "title"},
	} {
		if _, f := parseAllocation(tc.in, false); len(f.errs[tc.field]) == 0 {
			t.Errorf("%s: no field error on %s: %+v", tc.name, tc.field, f.errs)
		}
	}
}

// TestPeriodErrors is S4's period: to on or after from, at most 1096 days
// after it, the excess coded period_too_long.
func TestPeriodErrors(t *testing.T) {
	if f := periodErrors(day("2026-10-05"), day("2026-10-05")); f.failed() {
		t.Errorf("one day failed: %+v", f.errs)
	}
	if f := periodErrors(day("2026-10-05"), day("2029-10-05")); f.failed() {
		t.Errorf("1096 days failed: %+v", f.errs)
	}
	if f := periodErrors(day("2026-10-05"), day("2029-10-06")); f.code != codePeriodTooLong || len(f.errs["toDate"]) == 0 {
		t.Errorf("1097 days = %+v, want period_too_long on toDate", f)
	}
	if f := periodErrors(day("2026-10-05"), day("2026-10-04")); f.code != "" || len(f.errs["toDate"]) == 0 {
		t.Errorf("to before from = %+v, want an uncoded error on toDate", f)
	}
}

// TestLoadOfInput is S2's bounds per unit and the two decimals.
func TestLoadOfInput(t *testing.T) {
	for _, tc := range []struct {
		unit string
		v    float64
		want int64
		ok   bool
	}{
		{loadUnitPercent, 100, 10000, true},
		{loadUnitPercent, 100.01, 0, false},
		{loadUnitPercent, 0.01, 1, true},
		{loadUnitPercent, 0, 0, false},
		{loadUnitPercent, -5, 0, false},
		{loadUnitHours, 80, 8000, true},
		{loadUnitHours, 80.01, 0, false},
		{loadUnitHours, 37.5, 3750, true},
		{loadUnitHours, 1.005, 0, false},
	} {
		got, message := loadOfInput(tc.unit, tc.v)
		if (message == "") != tc.ok || got != tc.want {
			t.Errorf("loadOfInput(%s, %v) = %d %q, want %d ok=%v", tc.unit, tc.v, got, message, tc.want, tc.ok)
		}
	}
}

// TestDefaultStatusAndClosed is S3's default and S4's closed statuses.
func TestDefaultStatusAndClosed(t *testing.T) {
	for status, want := range map[string]string{"planned": statusTentative, "active": statusConfirmed, "on-hold": statusConfirmed} {
		if got := defaultStatus(contracts.ProjectEntry{Status: status}); got != want {
			t.Errorf("defaultStatus(%s) = %s, want %s", status, got, want)
		}
	}
	for status, want := range map[string]bool{"completed": true, "cancelled": true, "active": false, "planned": false} {
		if got := projectClosed(&contracts.ProjectEntry{Status: status}); got != want {
			t.Errorf("projectClosed(%s) = %v, want %v", status, got, want)
		}
	}
	if projectClosed(nil) {
		t.Error("an unresolved project read as closed")
	}
}

// TestWorkforceBound is amendment 14's bound as Staff computes it — the
// Monday thirteen weeks back, thirty-six months on with the day clamped —
// and the clip of a window to it.
func TestWorkforceBound(t *testing.T) {
	lo, hi := workforceBound(day("2026-09-12"))
	if !lo.Equal(day("2026-06-08")) || !hi.Equal(day("2029-09-12")) {
		t.Errorf("bound = %s..%s, want 2026-06-08..2029-09-12", lo.Format(time.DateOnly), hi.Format(time.DateOnly))
	}
	if got := monthsAfter(day("2028-02-29"), 36); !got.Equal(day("2031-02-28")) {
		t.Errorf("monthsAfter(2028-02-29, 36) = %s, want 2031-02-28", got.Format(time.DateOnly))
	}
	from, to, ok := clipToBound(day("2026-01-05"), day("2030-01-01"), day("2026-09-12"))
	if !ok || !from.Equal(lo) || !to.Equal(hi) {
		t.Errorf("clip = %s..%s %v, want the bound", from.Format(time.DateOnly), to.Format(time.DateOnly), ok)
	}
	if _, _, ok := clipToBound(day("2026-01-05"), day("2026-01-30"), day("2026-09-12")); ok {
		t.Error("a window wholly before the bound clipped to something")
	}
	if f := boundErrors(day("2026-01-05"), day("2026-01-30"), day("2026-09-12")); f.code != codeWindowOutOfBounds || len(f.errs["fromDate"]) == 0 {
		t.Errorf("a past period = %+v, want window_out_of_bounds on fromDate", f)
	}
	if f := boundErrors(day("2029-10-01"), day("2029-10-05"), day("2026-09-12")); len(f.errs["toDate"]) == 0 {
		t.Errorf("a period past the far end = %+v, want an error on toDate", f)
	}
	if f := boundErrors(day("2026-01-05"), day("2026-09-30"), day("2026-09-12")); f.failed() {
		t.Errorf("a period reaching into the bound failed: %+v", f.errs)
	}
}

// TestRetryMoved is amendments 21 and 35: a row that moves under the write
// is tried again, three times in all, and a row still moving after the last
// is the 409 revision_conflict rather than a fault; any other answer stops
// the retry at once.
func TestRetryMoved(t *testing.T) {
	s := &server{}
	attempts := 0
	err := s.retryMoved(func() error { attempts++; return errRowMoved })
	status, p, ok := answerOf(err)
	if attempts != maxWriteAttempts || !ok || status != http.StatusConflict || p.Code == nil || *p.Code != codeRevisionConflict {
		t.Errorf("a row always moving = %d attempts, %d %+v %v; want %d attempts and 409 revision_conflict", attempts, status, p, ok, maxWriteAttempts)
	}
	attempts = 0
	if err := s.retryMoved(func() error {
		attempts++
		if attempts == 1 {
			return errRowMoved
		}
		return nil
	}); err != nil || attempts != 2 {
		t.Errorf("a row moving once = %v after %d attempts, want success on the second", err, attempts)
	}
}

// TestOutOfStaffBound is Staff's bound refusal on a write — Staff's today a
// day on from this module's — as the field error window_out_of_bounds, on
// fromDate when the window starts on the near end, else on toDate, under the
// title it is given; any other error passes as it is.
func TestOutOfStaffBound(t *testing.T) {
	today := day("2026-09-12")
	near, _ := workforceBound(today)
	for _, tc := range []struct {
		lo    time.Time
		field string
	}{{near, "fromDate"}, {day("2026-10-05"), "toDate"}} {
		r, ok := asRefusal(outOfStaffBound(contracts.ErrWindowOutOfBounds, tc.lo, today, invalidAllocationTitle))
		if !ok || r.status != http.StatusBadRequest || r.problem.Code == nil || *r.problem.Code != codeWindowOutOfBounds || len((*r.problem.Errors)[tc.field]) != 1 {
			t.Errorf("refused from %s = %+v, want window_out_of_bounds on %s", tc.lo.Format(time.DateOnly), r, tc.field)
		}
	}
	if r, ok := asRefusal(outOfStaffBound(contracts.ErrWindowOutOfBounds, near, today, invalidRequestTitle)); !ok ||
		r.problem.Title == nil || *r.problem.Title != invalidRequestTitle {
		t.Errorf("refused for a request = %+v, want the title %q", r, invalidRequestTitle)
	}
	other := errors.New("boom")
	if err := outOfStaffBound(other, near, today, invalidAllocationTitle); !errors.Is(err, other) {
		t.Errorf("another error = %v, want it as it is", err)
	}
}

// TestEmployedThroughout is S4's rule over the days Staff was asked about.
func TestEmployedThroughout(t *testing.T) {
	employed := []contracts.DateSpan{{From: day("2026-12-01"), To: day("2027-06-30")}}
	lo, hi := day("2026-06-08"), day("2029-09-12")
	if !employedThroughout(employed, day("2026-12-01"), day("2026-12-31"), lo, hi) {
		t.Error("a period inside the employment failed")
	}
	if employedThroughout(employed, day("2026-11-30"), day("2026-12-31"), lo, hi) {
		t.Error("a day before the start passed")
	}
	if employedThroughout(employed, day("2027-06-01"), day("2027-07-01"), lo, hi) {
		t.Error("a day after the end passed")
	}
	if !employedThroughout(employed, day("2026-11-01"), day("2026-12-31"), day("2026-12-01"), hi) {
		t.Error("a day outside the window asked was judged")
	}
}

// TestOutsideProjectDates is rule 6's warning on each end the project has.
func TestOutsideProjectDates(t *testing.T) {
	start, end := day("2026-09-01"), day("2027-06-30")
	p := &contracts.ProjectEntry{StartDate: &start, EndDate: &end}
	for _, tc := range []struct {
		from, to string
		want     bool
	}{
		{"2026-09-01", "2027-06-30", false},
		{"2026-08-31", "2026-09-30", true},
		{"2027-06-01", "2027-07-01", true},
	} {
		if got := outsideProjectDates(p, day(tc.from), day(tc.to)); got != tc.want {
			t.Errorf("%s..%s = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
	if outsideProjectDates(&contracts.ProjectEntry{}, day("2020-01-01"), day("2030-01-01")) || outsideProjectDates(nil, day("2020-01-01"), day("2030-01-01")) {
		t.Error("a project without dates warned")
	}
	if w := allocationWarnings(overbooking{}, p, start, end); w == nil || len(w) != 0 {
		t.Errorf("no warning = %#v, want an empty list", w)
	}
}

// storedRow is an allocation as the store answers it.
func storedRow(person int64, project int32, from, to, status string, load int64) store.PlanningAllocation {
	return store.PlanningAllocation{
		ID: 1001, PersonID: person, ProjectID: project, FromDate: dayParam(day(from)), ToDate: dayParam(day(to)),
		LoadUnit: loadUnitPercent, Load: numericOfHundredths(load), Status: status,
	}
}

// TestChangesOf is the events' payload: the changed fields before and after,
// the note by its name alone, the status left to its own event.
func TestChangesOf(t *testing.T) {
	note := "Hemmelig"
	before := storedRow(1001, 2001, "2026-10-05", "2026-10-30", statusTentative, 5000)
	after := before
	after.Load, after.Status, after.Note = numericOfHundredths(4000), statusConfirmed, &note

	c := changesOf(&before, &after, true)
	if len(c) != 2 || c["load"].From != 50.0 || c["load"].To != 40.0 || c["note"] != (fieldChange{}) {
		t.Errorf("changes = %+v, want load 50→40 and the note named alone", c)
	}
	if _, ok := changesOf(&before, &after, false)["status"]; !ok {
		t.Error("the status was left out when asked for")
	}
	created := changesOf(nil, &after, false)
	if created["personId"].To != int64(1001) || created["fromDate"].To != "2026-10-05" || created["status"].To != statusConfirmed || created["personId"].From != nil {
		t.Errorf("created = %+v", created)
	}
	if len(changesOf(&before, &before, false)) != 0 {
		t.Error("a row compared with itself changed")
	}
}

// TestReplaceNotices is amendment 5 for a replace, case by case.
func TestReplaceNotices(t *testing.T) {
	kariUser, olaUser := uuid.New(), uuid.New()
	kari := &contracts.PersonEntry{ID: 1001, UserID: &kariUser}
	ola := &contracts.PersonEntry{ID: 1002, UserID: &olaUser}
	unlinked := &contracts.PersonEntry{ID: 1003}
	tentative := storedRow(1001, 2001, "2026-10-05", "2026-10-30", statusTentative, 5000)
	confirmed := storedRow(1001, 2001, "2026-10-05", "2026-10-30", statusConfirmed, 5000)
	titled := confirmed
	titled.Title = ptrOf("Arkitekt")
	longer := confirmed
	longer.ToDate = dayParam(day("2026-11-06"))
	moved := confirmed
	moved.PersonID = 1002

	kinds := func(ns []*allocationNotice) string {
		var out []string
		for _, n := range ns {
			if n == nil {
				out = append(out, "nobody")
				continue
			}
			out = append(out, map[allocationNoticeKind]string{noticePlanned: "planned", noticeChanged: "changed", noticeRemoved: "removed", noticeWithdrawn: "withdrawn"}[n.kind]+":"+n.userID.String()[:4])
		}
		return strings.Join(out, ",")
	}
	k, o := kariUser.String()[:4], olaUser.String()[:4]
	for _, tc := range []struct {
		name          string
		before, after store.PlanningAllocation
		to            *contracts.PersonEntry
		want          string
	}{
		{"set confirmed", tentative, confirmed, kari, "planned:" + k},
		{"set tentative", confirmed, tentative, kari, "withdrawn:" + k},
		{"a tentative row changed", tentative, tentative, kari, ""},
		{"a confirmed row's title", confirmed, titled, kari, ""},
		{"a confirmed row's period", confirmed, longer, kari, "changed:" + k},
		{"moved to another person", confirmed, moved, ola, "removed:" + k + ",planned:" + o},
		{"a person with no user", tentative, confirmed, unlinked, "nobody"},
	} {
		if got := kinds(replaceNotices(tc.before, tc.after, kari, tc.to, nil, nil)); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func ptrOf[T any](v T) *T { return &v }
