package planning

import (
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
)

// This file is the capacity calculation (design S6), the one place hours are
// summed: every view — the board, a project's plan, a person's timeline, the
// unbooked list, the stats — and the overbooking judgement of a write
// (window.go's overbookingOf) read it, and no handler sums hours of its own
// (rule 2). It is pure: Staff's segments and away spans, the platform's
// public holidays and the rows go in; the figures per day and per week come
// out.
//
// For a person and a civil day:
//
//   - capacity is 0 on a Saturday, a Sunday and a public holiday
//     (internal/holidays), else the hours a week in force that day over five;
//   - away is whether the day lies in one of the person's away spans — a day
//     away is wholly away (reading 1);
//   - available is capacity when not away, else 0;
//   - booked is the confirmed rows' hours that day, tentative the tentative
//     rows' — a percent row books load/100 × capacity, an hours row load/5 on
//     a day with capacity, and neither books anything on a day without (S2);
//   - free is available − booked, which may be negative.
//
// A week runs Monday to Sunday and each figure is the sum of its days inside
// the window; loadPercent is booked / capacity × 100 over the week, absent
// when the week's capacity is 0. Absence never shrinks a booking: a week away
// under a 100 % allocation books its hours and frees nothing, a conflict the
// board shows rather than a silent re-plan.

// The load units an allocation is typed in (S2), as the contract names them.
const (
	loadUnitPercent = "percent"
	loadUnitHours   = "hours_per_week"
)

// workdaysPerWeek is what an hours-a-week figure is spread over: a day with
// capacity carries a fifth of it.
const workdaysPerWeek = 5

// capacityRow is one allocation as the calculation reads it: its period,
// From..To inclusive at midnight UTC, and its load as typed.
type capacityRow struct {
	From, To time.Time
	LoadUnit string // loadUnitPercent or loadUnitHours
	Load     float64
}

// capacityInput is one person over one window: Staff's capacity for the
// window (contracts.Workforce.Capacity) and the person's rows touching it,
// confirmed and tentative apart.
type capacityInput struct {
	From, To  time.Time
	Segments  []contracts.CapacitySegment
	Away      []contracts.DateSpan
	Confirmed []capacityRow
	Tentative []capacityRow
}

// dayFigures is one day's figures (S6), in hours.
type dayFigures struct {
	Day                                          time.Time
	Capacity, Available, Booked, Tentative, Free float64
	Away                                         bool
}

// weekFigures is one week's figures, the sums of its days in the window, in
// hours; LoadPercent is nil when the week's capacity is 0. Away is whether any
// of its days is away.
type weekFigures struct {
	Monday                                       time.Time
	Capacity, Available, Booked, Tentative, Free float64
	LoadPercent                                  *float64
	Away                                         bool
	// BusyHours and BusyDays are what the calendar shows the person busy in
	// the week, nil when it answered nothing for them; shown beside the
	// figures above and never part of them.
	BusyHours *float64
	BusyDays  *int
}

// capacityDays is every day of in's window with its figures, in order. The
// figures are rounded to the hundredth; capacityWeeks sums the unrounded
// ones.
func capacityDays(in capacityInput) []dayFigures {
	raw := rawDays(in)
	out := make([]dayFigures, len(raw))
	for i, d := range raw {
		out[i] = dayFigures{
			Day: d.Day, Capacity: roundHours(d.Capacity), Available: roundHours(d.Available),
			Booked: roundHours(d.Booked), Tentative: roundHours(d.Tentative), Free: roundHours(d.Free), Away: d.Away,
		}
	}
	return out
}

// capacityWeeks is every week in's window touches with its figures, in
// order — a week the window starts or ends inside sums only its days in the
// window.
func capacityWeeks(in capacityInput) []weekFigures {
	var out []weekFigures
	for _, d := range rawDays(in) {
		monday := mondayOf(d.Day)
		if len(out) == 0 || !out[len(out)-1].Monday.Equal(monday) {
			out = append(out, weekFigures{Monday: monday})
		}
		w := &out[len(out)-1]
		w.Capacity += d.Capacity
		w.Available += d.Available
		w.Booked += d.Booked
		w.Tentative += d.Tentative
		w.Away = w.Away || d.Away
	}
	for i := range out {
		w := &out[i]
		if w.Capacity > 0 {
			load := roundHours(w.Booked / w.Capacity * 100)
			w.LoadPercent = &load
		}
		w.Free = roundHours(w.Available - w.Booked)
		w.Capacity, w.Available = roundHours(w.Capacity), roundHours(w.Available)
		w.Booked, w.Tentative = roundHours(w.Booked), roundHours(w.Tentative)
	}
	return out
}

// rawDays is the days of in's window with their figures unrounded.
func rawDays(in capacityInput) []dayFigures {
	var out []dayFigures
	for day := in.From; !day.After(in.To); day = addDays(day, 1) {
		capacity := 0.0
		if holidays.BusinessDay(day) {
			capacity = hoursPerWeekOn(in.Segments, day) / workdaysPerWeek
		}
		away := inSpans(in.Away, day)
		available := capacity
		if away {
			available = 0
		}
		booked := rowsHoursOn(in.Confirmed, day, capacity)
		out = append(out, dayFigures{
			Day: day, Capacity: capacity, Available: available,
			Booked: booked, Tentative: rowsHoursOn(in.Tentative, day, capacity),
			Free: available - booked, Away: away,
		})
	}
	return out
}

// hoursPerWeekOn is the hours a week in force on day: the segment covering
// it, 0 outside employment. Staff answers the segments consecutive and
// non-overlapping, concurrent employments already summed (S8).
func hoursPerWeekOn(segments []contracts.CapacitySegment, day time.Time) float64 {
	for _, s := range segments {
		if !day.Before(s.From) && !day.After(s.To) {
			return s.HoursPerWeek
		}
	}
	return 0
}

// inSpans reports whether day lies in one of spans.
func inSpans(spans []contracts.DateSpan, day time.Time) bool {
	for _, s := range spans {
		if !day.Before(s.From) && !day.After(s.To) {
			return true
		}
	}
	return false
}

// rowsHoursOn is the hours rows book on day, whose capacity is capacity (S2).
func rowsHoursOn(rows []capacityRow, day time.Time, capacity float64) float64 {
	if capacity <= 0 {
		return 0
	}
	total := 0.0
	for _, r := range rows {
		if day.Before(r.From) || day.After(r.To) {
			continue
		}
		switch r.LoadUnit {
		case loadUnitPercent:
			total += r.Load / 100 * capacity
		case loadUnitHours:
			total += r.Load / workdaysPerWeek
		}
	}
	return total
}
