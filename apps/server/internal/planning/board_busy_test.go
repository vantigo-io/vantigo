package planning_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// The board's busy figures (calendar Outlook sync design O9.8): the
// calendar's busy time of each person with a user, per week, beside the
// capacity figures and never subtracted from them. The calendar is a
// modtest.FakeCalendarBusy; the business zone is Oslo (UTC+2 in September)
// so the week's edge is not the UTC midnight.

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// busyBoard is a harness with the fake calendar composed, Kari linked to the
// signed-in user (Ola has no user), the zone Oslo, and the board fetched
// by a caller with planning:view.
func busyBoard(t *testing.T, fake *modtest.FakeCalendarBusy, query string) (gen.PlanningBoard, uuid.UUID, int) {
	t.Helper()
	var opts []modtest.Option
	if fake != nil {
		opts = append(opts, modtest.WithCalendarBusy(fake))
	}
	h := newHarness(t, opts...)
	h.SetBusinessTimeZone(t, "Europe/Oslo")
	c, user := signIn(t, h, "planning:view")
	h.workforce.link(user, personKari)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-27", load: 50})
	seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: week38, to: "2026-09-27", load: 50})
	if fake != nil {
		fake.SetBusy(user, contracts.BusyTime{
			Spans: []contracts.TimeSpan{
				{StartsAt: at("2026-09-15T09:00:00Z"), EndsAt: at("2026-09-15T10:30:00Z")},
				{StartsAt: at("2026-09-15T12:00:00Z"), EndsAt: at("2026-09-15T12:20:00Z")},
				// Crosses the week's edge: Sunday 22:00 Oslo to Monday 02:00 Oslo.
				{StartsAt: at("2026-09-20T20:00:00Z"), EndsAt: at("2026-09-21T00:00:00Z")},
			},
			Days: []contracts.DateSpan{
				{From: date("2026-09-16"), To: date("2026-09-17")},
				{From: date("2026-09-20"), To: date("2026-09-22")},
			},
		})
	}
	var b gen.PlanningBoard
	res := c.Do(http.MethodGet, boardPath+query, nil)
	if res.Status == http.StatusOK {
		res.JSON(&b)
	}
	return b, user, res.Status
}

func TestBoard_BusyFigures(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeCalendarBusy()
	b, user, status := busyBoard(t, fake, "")
	if status != http.StatusOK {
		t.Fatalf("board = %d", status)
	}
	k38, k39 := cellOf(t, rowOf(t, b, personKari), week38), cellOf(t, rowOf(t, b, personKari), week39)
	if k38.BusyHours == nil || *k38.BusyHours != 3.8 || k38.BusyDays == nil || *k38.BusyDays != 3 {
		t.Errorf("week 38 busy = %v h, %v d; want 3.8 h (1.5 + 1/3 + 2 up to the edge), 3 days", k38.BusyHours, k38.BusyDays)
	}
	if k39.BusyHours == nil || *k39.BusyHours != 2 || k39.BusyDays == nil || *k39.BusyDays != 2 {
		t.Errorf("week 39 busy = %v h, %v d; want 2 h past the edge, 2 days", k39.BusyHours, k39.BusyDays)
	}
	if k38.Capacity != 37.5 || k38.Available != 37.5 || k38.Booked != 18.75 || k38.Free != 18.75 || k38.LoadPercent == nil || *k38.LoadPercent != 50 {
		t.Errorf("week 38 = %+v, want the capacity figures untouched by the busy time", k38)
	}
	if k40 := cellOf(t, rowOf(t, b, personKari), "2026-09-28"); k40.BusyHours == nil || *k40.BusyHours != 0 || k40.BusyDays == nil || *k40.BusyDays != 0 {
		t.Errorf("week 40 busy = %v h, %v d; want the calendar's answer of none, 0 and 0", k40.BusyHours, k40.BusyDays)
	}
	if o := cellOf(t, rowOf(t, b, personOla), week38); o.BusyHours != nil || o.BusyDays != nil {
		t.Errorf("Ola has no user, busy = %v h, %v d; want none", o.BusyHours, o.BusyDays)
	}
	calls := fake.Calls()
	if len(calls) != 1 || len(calls[0].UserIDs) != 1 || calls[0].UserIDs[0] != user {
		t.Fatalf("calls = %+v, want one, for Kari's user alone", calls)
	}
	if calls[0].From.String() != date(thisMonday).String() || calls[0].To.String() != date("2026-11-29").String() {
		t.Errorf("window = %s..%s, want the board's", calls[0].From, calls[0].To)
	}
}

func TestBoard_BusyAnsweredNothing(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeCalendarBusy()
	fake.SetDeny(true)
	b, _, _ := busyBoard(t, fake, "")
	if k := cellOf(t, rowOf(t, b, personKari), week38); k.BusyHours != nil || k.BusyDays != nil || k.Booked != 18.75 {
		t.Errorf("week 38 = %+v, want no busy figures when the calendar answers nothing", k)
	}
}

func TestBoard_BusyWithoutTheCalendar(t *testing.T) {
	t.Parallel()
	b, _, status := busyBoard(t, nil, "")
	if status != http.StatusOK {
		t.Fatalf("board = %d, want 200 without the calendar", status)
	}
	if k := cellOf(t, rowOf(t, b, personKari), week38); k.BusyHours != nil || k.BusyDays != nil {
		t.Errorf("week 38 = %+v, want no busy figures without the calendar", k)
	}
}

func TestBoard_BusyFailureFailsTheRequest(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeCalendarBusy()
	fake.SetError(errors.New("calendar down"))
	if _, _, status := busyBoard(t, fake, ""); status != http.StatusServiceUnavailable {
		t.Errorf("board = %d, want 503, the board's answer to a contract that fails", status)
	}
}

func TestBoard_BusyByProject(t *testing.T) {
	t.Parallel()
	b, _, _ := busyBoard(t, modtest.NewFakeCalendarBusy(), "?groupBy=project")
	var seenPerson bool
	for _, r := range b.Rows {
		for _, c := range r.Cells {
			switch {
			case r.Person == nil && (c.BusyHours != nil || c.BusyDays != nil):
				t.Fatalf("the project's own row carries busy figures: %+v", c)
			case r.Person != nil && r.Person.Id == personKari && c.Monday.String() == week38:
				seenPerson = c.BusyHours != nil && *c.BusyHours == 3.8
			}
		}
	}
	if !seenPerson {
		t.Errorf("Kari's row under the project lacks her week 38 busy hours: %+v", b.Rows)
	}
}

// Two people sharing a user each get that user's busy figures, the calendar
// asked for the user once.
func TestBoard_BusyOfAUserSharedByTwoPeople(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeCalendarBusy()
	h := newHarness(t, modtest.WithCalendarBusy(fake))
	h.SetBusinessTimeZone(t, "Europe/Oslo")
	c, user := signIn(t, h, "planning:view")
	h.workforce.link(user, personKari)
	h.workforce.link(user, personOla)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-27", load: 50})
	seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: week38, to: "2026-09-27", load: 50})
	fake.SetBusy(user, contracts.BusyTime{Spans: []contracts.TimeSpan{
		{StartsAt: at("2026-09-15T09:00:00Z"), EndsAt: at("2026-09-15T10:30:00Z")},
	}})
	var b gen.PlanningBoard
	if res := c.Do(http.MethodGet, boardPath, nil); res.Status != http.StatusOK {
		t.Fatalf("board = %d", res.Status)
	} else {
		res.JSON(&b)
	}
	for name, person := range map[string]int64{"Kari": personKari, "Ola": personOla} {
		if k := cellOf(t, rowOf(t, b, person), week38); k.BusyHours == nil || *k.BusyHours != 1.5 {
			t.Errorf("%s's week 38 busy = %v h, want the shared user's 1.5", name, k.BusyHours)
		}
	}
	if calls := fake.Calls(); len(calls) != 1 || len(calls[0].UserIDs) != 1 || calls[0].UserIDs[0] != user {
		t.Errorf("calls = %+v, want one, for the user once", calls)
	}
}
