package timetracking_test

import (
	"testing"

	gotime "time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
)

// contracts.Timesheets as this module provides it (staff person-glance design
// §3.2): a user's hours in the seven days from a Monday, logged and approved.
// Like the actuals provider it is built from the pool alone, with every
// directory one that may not be called.

// timesheetsProvider builds the provider the way module.Compose does, from
// the harness's own Deps with the directories forbidden.
func timesheetsProvider(t *testing.T, h *harness) contracts.Timesheets {
	t.Helper()
	build := timetracking.Module().Timesheets
	if build == nil {
		t.Fatal("the time module declares no Timesheets provider")
	}
	deps := h.Deps()
	deps.Projects = &forbiddenProjects{t: t}
	deps.Products = &forbiddenCatalog{t: t}
	return build(deps)
}

// weekOf is a calendar date at midnight UTC, as the contract takes it.
func weekOf(t *testing.T, date string) gotime.Time {
	t.Helper()
	d, err := gotime.Parse(gotime.DateOnly, date)
	if err != nil {
		t.Fatalf("parse %q: %v", date, err)
	}
	return d
}

// Logged is every entry of the week but the rejected ones, Approved the
// approved and invoiced ones; an entry the week after counts in neither.
func TestTimesheetsWeekHours_SumsTheWeekByStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := timesheetsProvider(t, h)

	// workDay is a Monday; the week runs to the Sunday six days on.
	for _, e := range []loggedEntry{
		{project: projectKraftVerket, date: workDay, hours: "1.00", status: "draft"},
		{project: projectKraftVerket, date: "2026-09-15", hours: "2.00", status: "submitted"},
		{project: projectKraftVerket, date: "2026-09-16", hours: "4.00", status: "approved"},
		{project: projectKraftVerket, date: "2026-09-17", hours: "8.00", status: "rejected"},
		{project: projectKraftVerket, date: "2026-09-20", hours: "16.25", status: "invoiced"},
		{project: projectKraftVerket, date: "2026-09-21", hours: "32.00", status: "approved"},
	} {
		logEntry(t, h, user, e)
	}

	got, err := p.WeekHours(t.Context(), weekOf(t, workDay), []uuid.UUID{user})
	if err != nil {
		t.Fatalf("week hours: %v", err)
	}
	want := contracts.WeekHours{Logged: 23.25, Approved: 20.25}
	if got[user] != want {
		t.Errorf("WeekHours[user] = %+v, want %+v", got[user], want)
	}
	if len(got) != 1 {
		t.Errorf("WeekHours answered %d users, want 1: %+v", len(got), got)
	}
}

// A user with nothing in the week is absent from the map, not a zero entry:
// the consumer tells "no user asked about" from "nothing logged" by its own
// list, and the provider answers only what it found.
func TestTimesheetsWeekHours_UserWithoutEntriesIsAbsent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, logging := signIn(t, h)
	_, idle := signIn(t, h)
	p := timesheetsProvider(t, h)

	logEntry(t, h, logging, loggedEntry{project: projectKraftVerket, date: workDay, hours: "7.50", status: "submitted"})

	got, err := p.WeekHours(t.Context(), weekOf(t, workDay), []uuid.UUID{logging, idle})
	if err != nil {
		t.Fatalf("week hours: %v", err)
	}
	if _, ok := got[idle]; ok {
		t.Errorf("WeekHours[idle] = %+v, want the user absent", got[idle])
	}
	if want := (contracts.WeekHours{Logged: 7.5}); got[logging] != want {
		t.Errorf("WeekHours[logging] = %+v, want %+v", got[logging], want)
	}
}

// No user ids answer an empty map and no error.
func TestTimesheetsWeekHours_NoUsers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := timesheetsProvider(t, h)

	got, err := p.WeekHours(t.Context(), weekOf(t, workDay), nil)
	if err != nil {
		t.Fatalf("week hours: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("WeekHours(no users) = %#v, want an empty map", got)
	}
}

// The caller passes a Monday; given any other date the provider still reads
// the seven days from it and nothing else — it does not snap to a week.
func TestTimesheetsWeekHours_ReadsSevenDaysFromTheGivenDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := timesheetsProvider(t, h)

	for _, e := range []loggedEntry{
		{project: projectKraftVerket, date: "2026-09-15", hours: "1.00", status: "draft"},    // the day before
		{project: projectKraftVerket, date: "2026-09-16", hours: "2.00", status: "draft"},    // the Wednesday given
		{project: projectKraftVerket, date: "2026-09-22", hours: "4.00", status: "approved"}, // the sixth day after it
		{project: projectKraftVerket, date: "2026-09-23", hours: "8.00", status: "approved"}, // the seventh: outside
	} {
		logEntry(t, h, user, e)
	}

	got, err := p.WeekHours(t.Context(), weekOf(t, "2026-09-16"), []uuid.UUID{user})
	if err != nil {
		t.Fatalf("week hours: %v", err)
	}
	if want := (contracts.WeekHours{Logged: 6, Approved: 4}); got[user] != want {
		t.Errorf("WeekHours[user] = %+v, want %+v", got[user], want)
	}
}
