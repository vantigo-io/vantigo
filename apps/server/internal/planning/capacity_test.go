package planning

import (
	"fmt"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// day is a civil date at midnight UTC, as every date of the calculation is.
func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptrFloat(v float64) *float64 { return &v }

// fullTime is one segment of 37.5 hours a week over the whole of 2026 — 7.5
// hours on a weekday.
var fullTime = []contracts.CapacitySegment{{From: day("2026-01-01"), To: day("2026-12-31"), HoursPerWeek: 37.5}}

// The weeks of the tests, in October and November 2026: week 43 starts on
// Monday 19 October, week 44 on Monday 26 October.
const (
	week43 = "2026-10-19"
	week44 = "2026-10-26"
)

// TestCapacityWeeks is the calculation of S6 per figure, a week at a time: each
// case one week (or two) of one person, and the figures the board shows for
// it.
func TestCapacityWeeks(t *testing.T) {
	cases := []struct {
		name string
		in   capacityInput
		want []weekFigures
	}{
		{
			name: "a full week at full time, nothing booked",
			in:   capacityInput{From: day(week43), To: day("2026-10-25"), Segments: fullTime},
			want: []weekFigures{{Monday: day(week43), Capacity: 37.5, Available: 37.5, Free: 37.5, LoadPercent: ptrFloat(0)}},
		},
		{
			name: "two segments in one week: 37.5 to Wednesday, 30 from Thursday",
			in: capacityInput{From: day(week43), To: day("2026-10-25"), Segments: []contracts.CapacitySegment{
				{From: day("2026-01-01"), To: day("2026-10-21"), HoursPerWeek: 37.5},
				{From: day("2026-10-22"), To: day("2026-12-31"), HoursPerWeek: 30},
			}},
			// 3 × 7.5 + 2 × 6
			want: []weekFigures{{Monday: day(week43), Capacity: 34.5, Available: 34.5, Free: 34.5, LoadPercent: ptrFloat(0)}},
		},
		{
			name: "an FTE change mid-row: the percent row follows it, the hours row does not",
			in: capacityInput{From: day(week43), To: day("2026-10-25"), Segments: []contracts.CapacitySegment{
				{From: day("2026-01-01"), To: day("2026-10-21"), HoursPerWeek: 37.5},
				{From: day("2026-10-22"), To: day("2026-12-31"), HoursPerWeek: 30},
			}, Confirmed: []capacityRow{{From: day(week43), To: day("2026-10-25"), LoadUnit: loadUnitPercent, Load: 50}},
				Tentative: []capacityRow{{From: day(week43), To: day("2026-10-25"), LoadUnit: loadUnitHours, Load: 20}}},
			// confirmed: 3 × 3.75 + 2 × 3; tentative: 5 × 4
			want: []weekFigures{{Monday: day(week43), Capacity: 34.5, Available: 34.5, Booked: 17.25, Tentative: 20, Free: 17.25, LoadPercent: ptrFloat(50)}},
		},
		{
			name: "a whole day away: available shrinks, the booking does not",
			in: capacityInput{From: day(week43), To: day("2026-10-25"), Segments: fullTime,
				Away:      []contracts.DateSpan{{From: day("2026-10-21"), To: day("2026-10-21")}},
				Confirmed: []capacityRow{{From: day(week43), To: day("2026-10-25"), LoadUnit: loadUnitPercent, Load: 100}}},
			want: []weekFigures{{Monday: day(week43), Capacity: 37.5, Available: 30, Booked: 37.5, Free: -7.5, LoadPercent: ptrFloat(100), Away: true}},
		},
		{
			name: "a public holiday: Ascension Day 2026 (Thursday 14 May) has no capacity",
			in: capacityInput{From: day("2026-05-11"), To: day("2026-05-17"), Segments: fullTime,
				Confirmed: []capacityRow{{From: day("2026-05-11"), To: day("2026-05-17"), LoadUnit: loadUnitHours, Load: 37.5}}},
			// 4 days of 7.5; the hours row books a fifth on each of the four
			want: []weekFigures{{Monday: day("2026-05-11"), Capacity: 30, Available: 30, Booked: 30, Free: 0, LoadPercent: ptrFloat(100)}},
		},
		{
			name: "a row over a weekend only books nothing",
			in: capacityInput{From: day(week43), To: day("2026-10-25"), Segments: fullTime,
				Confirmed: []capacityRow{{From: day("2026-10-24"), To: day("2026-10-25"), LoadUnit: loadUnitHours, Load: 40}}},
			want: []weekFigures{{Monday: day(week43), Capacity: 37.5, Available: 37.5, Free: 37.5, LoadPercent: ptrFloat(0)}},
		},
		{
			name: "percent against hours: 50 % and 10 hours a week of one person",
			in: capacityInput{From: day(week43), To: day("2026-10-25"), Segments: fullTime,
				Confirmed: []capacityRow{
					{From: day(week43), To: day("2026-10-25"), LoadUnit: loadUnitPercent, Load: 50},
					{From: day(week43), To: day("2026-10-25"), LoadUnit: loadUnitHours, Load: 10},
				}},
			want: []weekFigures{{Monday: day(week43), Capacity: 37.5, Available: 37.5, Booked: 28.75, Free: 8.75, LoadPercent: ptrFloat(76.67)}},
		},
		{
			name: "a row ending mid-week books only its days, and a week with no capacity has no load",
			in: capacityInput{From: day(week43), To: day("2026-11-01"), Segments: []contracts.CapacitySegment{
				{From: day("2026-01-01"), To: day("2026-10-25"), HoursPerWeek: 37.5},
			}, Confirmed: []capacityRow{{From: day("2026-10-21"), To: day("2026-10-27"), LoadUnit: loadUnitHours, Load: 40}}},
			want: []weekFigures{
				{Monday: day(week43), Capacity: 37.5, Available: 37.5, Booked: 24, Free: 13.5, LoadPercent: ptrFloat(64)},
				{Monday: day(week44)},
			},
		},
		{
			name: "a window starting mid-week sums only its days",
			in:   capacityInput{From: day("2026-10-22"), To: day("2026-10-27"), Segments: fullTime},
			want: []weekFigures{
				{Monday: day(week43), Capacity: 15, Available: 15, Free: 15, LoadPercent: ptrFloat(0)},
				{Monday: day(week44), Capacity: 15, Available: 15, Free: 15, LoadPercent: ptrFloat(0)},
			},
		},
		{
			name: "a leap week: Monday 28 February 2028 to Sunday 5 March, 29 February a weekday away",
			in: capacityInput{From: day("2028-02-28"), To: day("2028-03-05"),
				Segments:  []contracts.CapacitySegment{{From: day("2028-01-01"), To: day("2028-12-31"), HoursPerWeek: 37.5}},
				Away:      []contracts.DateSpan{{From: day("2028-02-29"), To: day("2028-02-29")}},
				Confirmed: []capacityRow{{From: day("2028-02-29"), To: day("2028-03-02"), LoadUnit: loadUnitPercent, Load: 50}}},
			// five weekdays of 7.5, the 29th away; the row books 3.75 on 29 February, 1 and 2 March
			want: []weekFigures{{Monday: day("2028-02-28"), Capacity: 37.5, Available: 30, Booked: 11.25, Free: 18.75, LoadPercent: ptrFloat(30), Away: true}},
		},
		{
			name: "outside employment there is no capacity, so nothing is booked",
			in: capacityInput{From: day(week43), To: day("2026-10-25"),
				Confirmed: []capacityRow{{From: day(week43), To: day("2026-10-25"), LoadUnit: loadUnitPercent, Load: 100}}},
			want: []weekFigures{{Monday: day(week43)}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := capacityWeeks(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("weeks = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if !sameWeek(got[i], tc.want[i]) {
					t.Errorf("week %d = %s, want %s", i, describeWeek(got[i]), describeWeek(tc.want[i]))
				}
			}
		})
	}
}

// TestCapacityDays is the per-day figures of S6, each rule on the day it is
// about.
func TestCapacityDays(t *testing.T) {
	in := capacityInput{
		From: day("2026-05-13"), To: day("2026-05-16"), Segments: fullTime,
		Away:      []contracts.DateSpan{{From: day("2026-05-15"), To: day("2026-05-15")}},
		Confirmed: []capacityRow{{From: day("2026-05-13"), To: day("2026-05-16"), LoadUnit: loadUnitPercent, Load: 40}},
		Tentative: []capacityRow{{From: day("2026-05-13"), To: day("2026-05-16"), LoadUnit: loadUnitHours, Load: 10}},
	}
	want := []dayFigures{
		// Wednesday: an ordinary day.
		{Day: day("2026-05-13"), Capacity: 7.5, Available: 7.5, Booked: 3, Tentative: 2, Free: 4.5},
		// Thursday: Ascension Day, no capacity, nothing booked.
		{Day: day("2026-05-14")},
		// Friday: away, wholly — the booking stands and free goes negative.
		{Day: day("2026-05-15"), Capacity: 7.5, Available: 0, Booked: 3, Tentative: 2, Free: -3, Away: true},
		// Saturday: no capacity.
		{Day: day("2026-05-16")},
	}
	got := capacityDays(in)
	if len(got) != len(want) {
		t.Fatalf("days = %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("day %s = %+v, want %+v", want[i].Day.Format(time.DateOnly), got[i], want[i])
		}
	}
}

// sameWeek compares two weeks' figures, LoadPercent by value.
func sameWeek(a, b weekFigures) bool {
	if (a.LoadPercent == nil) != (b.LoadPercent == nil) || (a.LoadPercent != nil && *a.LoadPercent != *b.LoadPercent) {
		return false
	}
	a.LoadPercent, b.LoadPercent = nil, nil
	return a == b
}

func describeWeek(w weekFigures) string {
	load := "none"
	if w.LoadPercent != nil {
		load = fmt.Sprint(*w.LoadPercent)
	}
	return fmt.Sprintf("%s capacity %v available %v booked %v tentative %v free %v load %s away %v",
		w.Monday.Format(time.DateOnly), w.Capacity, w.Available, w.Booked, w.Tentative, w.Free, load, w.Away)
}
