package timetracking_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const peoplePath = "/api/v1/time/people"

// personJSON decodes TimePersonOverview.
type personJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	Weeks       []struct {
		WeekStart     string     `json:"weekStart"`
		Hours         float64    `json:"hours"`
		SubmittedAt   *time.Time `json:"submittedAt"`
		ApprovedHours float64    `json:"approvedHours"`
		RejectedCount int32      `json:"rejectedCount"`
	} `json:"weeks"`
}

// getPeople reads the people overview and fails the test unless it answered
// 200.
func getPeople(t *testing.T, c *modtest.Client, query string) []personJSON {
	t.Helper()
	r := c.Do(http.MethodGet, peoplePath+"?"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("people ?%s: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var people []personJSON
	r.JSON(&people)
	return people
}

// week is one person's week as a test states it.
type week struct {
	start     string
	hours     float64
	approved  float64
	rejected  int32
	submitted bool
}

func weeksOf(p personJSON) []week {
	out := []week{}
	for _, w := range p.Weeks {
		out = append(out, week{w.WeekStart, w.Hours, w.ApprovedHours, w.RejectedCount, w.SubmittedAt != nil})
	}
	return out
}

func TestGetTimePeople_PerPersonAndWeek(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Wednesday 16 September: the current week is the one of Monday the 14th.
	h.Advance(4 * 24 * time.Hour)
	viewer, _ := signIn(t, h, "time:view-all")
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	anna, annaID := signInAs(t, h, projectKraftVerket, roleMember)
	bjorn, bjornID := signInAs(t, h, projectKraftVerket, roleMember)
	carl, carlID := signInAs(t, h, projectKraftVerket, roleMember)
	setDisplayName(t, h, annaID, "Anna")
	setDisplayName(t, h, bjornID, "Bjørn")
	setDisplayName(t, h, carlID, "Carl")

	// Anna: last week submitted as a whole, one entry approved and one
	// rejected; this week a draft, a single submitted entry and an invoiced
	// one.
	approved := createEntry(t, anna, map[string]any{"entryDate": "2026-09-08", "hours": 2})
	rejected := createEntry(t, anna, map[string]any{"entryDate": "2026-09-09", "hours": 1})
	submitWeek(t, anna, "2026-09-07")
	approveEntries(t, manager, approved.Id)
	rejectEntries(t, manager, "Feil dag", rejected.Id)
	createEntry(t, anna, map[string]any{"entryDate": "2026-09-14", "hours": 3})
	submittedEntry(t, anna, map[string]any{"entryDate": "2026-09-15", "hours": 1.25})
	invoiced := submittedEntry(t, anna, map[string]any{"entryDate": "2026-09-16", "hours": 1})
	setStatus(t, h, invoiced.Id, "invoiced")
	// Bjørn: nothing logged, this week reported as empty.
	submitWeek(t, bjorn, "2026-09-14")
	// Carl: only an entry three weeks back.
	createEntry(t, carl, map[string]any{"entryDate": "2026-08-31", "hours": 5})

	people := getPeople(t, viewer, "weeks=2")
	if len(people) != 2 || people[0].UserId != annaID || people[1].UserId != bjornID {
		t.Fatalf("people = %+v, want Anna and Bjørn, by name", people)
	}
	if people[0].DisplayName != "Anna" || people[1].DisplayName != "Bjørn" {
		t.Errorf("names = %q, %q", people[0].DisplayName, people[1].DisplayName)
	}
	wantAnna := []week{
		{"2026-09-07", 3, 2, 1, true},
		{"2026-09-14", 5.25, 1, 0, false},
	}
	if got := weeksOf(people[0]); !slices.Equal(got, wantAnna) {
		t.Errorf("Anna = %+v, want %+v", got, wantAnna)
	}
	wantBjorn := []week{
		{"2026-09-07", 0, 0, 0, false},
		{"2026-09-14", 0, 0, 0, true},
	}
	if got := weeksOf(people[1]); !slices.Equal(got, wantBjorn) {
		t.Errorf("Bjørn = %+v, want %+v", got, wantBjorn)
	}

	// Four weeks by default, which reaches Carl's entry.
	people = getPeople(t, viewer, "")
	if len(people) != 3 || people[2].UserId != carlID {
		t.Fatalf("default window: people = %+v, want Anna, Bjørn and Carl", people)
	}
	wantCarl := []week{
		{"2026-08-24", 0, 0, 0, false},
		{"2026-08-31", 5, 0, 0, false},
		{"2026-09-07", 0, 0, 0, false},
		{"2026-09-14", 0, 0, 0, false},
	}
	if got := weeksOf(people[2]); !slices.Equal(got, wantCarl) {
		t.Errorf("Carl = %+v, want %+v", got, wantCarl)
	}
}

func TestGetTimePeople_WeeksOutOfRangeOrNoViewAll_IsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer, _ := signIn(t, h, "time:view-all")
	for _, q := range []string{"weeks=0", "weeks=13", "weeks=-1"} {
		if r := viewer.Do(http.MethodGet, peoplePath+"?"+q, nil); r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, r.Status)
		}
	}
	if got := getPeople(t, viewer, "weeks=12"); len(got) != 0 {
		t.Errorf("weeks=12 with nobody's time: %+v, want an empty list", got)
	}

	for _, perms := range [][]string{nil, {"time:approve"}, {"time:manage"}} {
		c, _ := signIn(t, h, perms...)
		if r := c.Do(http.MethodGet, peoplePath, nil); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", perms, r.Status)
		}
	}
}
