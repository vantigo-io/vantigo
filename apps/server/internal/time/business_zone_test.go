package timetracking_test

import (
	"slices"
	"testing"
	"time"
	_ "time/tzdata" // America/New_York wherever the tests run
)

// "Today", and so "the current week", is the business day: today in the
// workspace's business time zone (time zones design D2, D7), read through the
// business calendar on the request that judges it. At 23:30 UTC on Sunday
// 13 September it is already Monday the 14th in Oslo, so the current week is
// the one of the 14th there, and still the one of the 7th under UTC and in
// New York. A draft dated Tuesday the 8th is last week's hours and an
// unsubmitted past week in Oslo, and this week's hours, not yet attention,
// under the other two; the people overview's window ends with the week each
// zone calls current.
func TestBusinessZone_TheCurrentWeekFollowsTheWorkspace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Advance(time.Date(2026, time.September, 13, 23, 30, 0, 0, time.UTC).Sub(h.Now()))
	viewer, _ := signIn(t, h, "time:view-all")
	me, myID := signInAs(t, h, projectKraftVerket, roleMember)
	createEntry(t, me, map[string]any{"entryDate": "2026-09-08", "hours": 2})

	for _, tc := range []struct {
		zone            string
		thisWeek        float64
		delta           float64
		wantUnsubmitted []string
		window          []week
	}{
		{
			zone: "Europe/Oslo", thisWeek: 0, delta: -2, wantUnsubmitted: []string{"2026-09-07"},
			window: []week{{start: "2026-09-07", hours: 2}, {start: "2026-09-14"}},
		},
		{
			zone: "UTC", thisWeek: 2, delta: 2, wantUnsubmitted: []string{},
			window: []week{{start: "2026-08-31"}, {start: "2026-09-07", hours: 2}},
		},
		{
			zone: "America/New_York", thisWeek: 2, delta: 2, wantUnsubmitted: []string{},
			window: []week{{start: "2026-08-31"}, {start: "2026-09-07", hours: 2}},
		},
	} {
		h.SetBusinessTimeZone(t, tc.zone)

		s := readStatsSummary(t, me, "")
		if s.HoursThisWeek != tc.thisWeek || s.HoursThisWeekDelta != tc.delta {
			t.Errorf("%s: hoursThisWeek = %v (delta %v), want %v (delta %v)",
				tc.zone, s.HoursThisWeek, s.HoursThisWeekDelta, tc.thisWeek, tc.delta)
		}

		unsubmitted := []string{}
		for _, item := range readStatsAttention(t, me) {
			if item.Type == "weekUnsubmitted" {
				unsubmitted = append(unsubmitted, item.EntityId)
			}
		}
		if !slices.Equal(unsubmitted, tc.wantUnsubmitted) {
			t.Errorf("%s: unsubmitted weeks = %v, want %v", tc.zone, unsubmitted, tc.wantUnsubmitted)
		}

		people := getPeople(t, viewer, "weeks=2")
		if len(people) != 1 || people[0].UserId != myID {
			t.Fatalf("%s: people = %+v, want the one person with hours", tc.zone, people)
		}
		if got := weeksOf(people[0]); !slices.Equal(got, tc.window) {
			t.Errorf("%s: weeks = %+v, want %+v", tc.zone, got, tc.window)
		}
	}
}
