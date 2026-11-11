package manufacturing

// The calendar engine (manufacturing design M9, M35, M35a, §11): a work
// center's open time, worked out day by day in the business time zone, and
// the forward fill the planner cuts a work order into pieces with. It is
// pure — no database, no clock — so every rule is a unit test
// (calendar_internal_test.go).
//
// A day's open intervals are its weekday's pattern, as local wall times on
// the 15-minute grid, each converted to UTC instants on that day: so a day a
// clock change shortens or lengthens has the hours it really has, and its
// capacity is their length. A Norwegian public holiday is closed, and so is
// a day one of the center's closures covers. Free time is the open
// intervals less the time booked (the slots of work orders not done), and a
// work order fills free time in order from an instant until it has the
// working time it needs — around breaks, nights, closed days and other
// bookings.

import (
	"encoding/json"
	"slices"
	"sort"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// planHorizonDays is how far ahead the planner looks from where it starts
// (M35): a work order that does not fit within it is no_capacity.
const planHorizonDays = 365

// span is a stretch of time, from start up to end, as UTC instants.
type span struct {
	start, end time.Time
}

func (s span) length() time.Duration { return s.end.Sub(s.start) }

// clockSpan is a pattern interval: minutes after local midnight, to at most
// 24 × 60.
type clockSpan struct {
	from, to int
}

// dayRange is a closure: civil dates at UTC midnight, both counted.
type dayRange struct {
	from, to time.Time
}

// Why a day is closed (ManufacturingScheduleDay.closed).
const (
	closedNone    = ""
	closedHoliday = "holiday"
	closedClosure = "closure"
)

// workCalendar is one work center's calendar in the business time zone.
type workCalendar struct {
	zone     *time.Location
	week     [7][]clockSpan // by time.Weekday
	closures []dayRange
}

// calendarOf is the work center's calendar: its stored pattern (the default
// when it cannot be read) and its closures, in zone.
func calendarOf(w store.ManufacturingWorkCenter, closures []store.ManufacturingWorkCenterClosure, zone *time.Location) workCalendar {
	var pattern gen.ManufacturingWeekPattern
	if json.Unmarshal(w.Pattern, &pattern) != nil {
		pattern = defaultPattern()
	}
	c := workCalendar{zone: zone}
	for day, intervals := range map[time.Weekday][]gen.ManufacturingInterval{
		time.Monday: pattern.Monday, time.Tuesday: pattern.Tuesday, time.Wednesday: pattern.Wednesday,
		time.Thursday: pattern.Thursday, time.Friday: pattern.Friday, time.Saturday: pattern.Saturday, time.Sunday: pattern.Sunday,
	} {
		for _, in := range intervals {
			from, okFrom := clockMinutes(in.Start, false)
			to, okTo := clockMinutes(in.End, true)
			if okFrom && okTo && from < to {
				c.week[day] = append(c.week[day], clockSpan{from: from, to: to})
			}
		}
		slices.SortFunc(c.week[day], func(a, b clockSpan) int { return a.from - b.from })
	}
	for _, cl := range closures {
		if cl.FromDate.Valid && cl.ToDate.Valid {
			c.closures = append(c.closures, dayRange{from: cl.FromDate.Time, to: cl.ToDate.Time})
		}
	}
	return c
}

// civilDay is the date of y, m, d at UTC midnight, the form every date is
// compared at (holidays.DayIn).
func civilDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// dayStart is the instant the civil day d starts in zone.
func dayStart(d time.Time, zone *time.Location) time.Time {
	y, m, dd := d.Date()
	return time.Date(y, m, dd, 0, 0, 0, 0, zone)
}

// closed is why the civil day d is closed: a closure, a public holiday, or
// neither.
func (c workCalendar) closed(d time.Time) string {
	for _, r := range c.closures {
		if !d.Before(r.from) && !d.After(r.to) {
			return closedClosure
		}
	}
	if holidays.PublicHoliday(d) {
		return closedHoliday
	}
	return closedNone
}

// day is the civil day d's open intervals as instants, in order, and why it
// is closed when it is. Each pattern time is a wall time on that day in the
// zone; one a clock change skips collapses to nothing.
func (c workCalendar) day(d time.Time) ([]span, string) {
	if why := c.closed(d); why != closedNone {
		return nil, why
	}
	y, m, dd := d.Date()
	var out []span
	for _, cs := range c.week[d.Weekday()] {
		start := time.Date(y, m, dd, cs.from/60, cs.from%60, 0, 0, c.zone)
		end := time.Date(y, m, dd, cs.to/60, cs.to%60, 0, 0, c.zone)
		if end.After(start) {
			out = append(out, span{start: start.UTC(), end: end.UTC()})
		}
	}
	return out, closedNone
}

// capacity is the length of the civil day d's open intervals (M9).
func (c workCalendar) capacity(d time.Time) time.Duration {
	open, _ := c.day(d)
	var total time.Duration
	for _, s := range open {
		total += s.length()
	}
	return total
}

// busySpans is booked as disjoint spans in order: sorted by start, those
// that overlap or touch joined, so their ends rise with their starts and a
// binary search or a moving index finds the booking at an instant.
func busySpans(booked []span) []span {
	sorted := booked
	if !slices.IsSortedFunc(booked, func(a, b span) int { return a.start.Compare(b.start) }) {
		sorted = slices.Clone(booked)
		slices.SortFunc(sorted, func(a, b span) int { return a.start.Compare(b.start) })
	}
	out := make([]span, 0, len(sorted))
	for _, b := range sorted {
		if !b.end.After(b.start) {
			continue
		}
		if n := len(out); n > 0 && !b.start.After(out[n-1].end) {
			if b.end.After(out[n-1].end) {
				out[n-1].end = b.end
			}
			continue
		}
		out = append(out, b)
	}
	return out
}

// after is spans cut to what lies after from.
func after(spans []span, from time.Time) []span {
	out := make([]span, 0, len(spans))
	for _, s := range spans {
		if !s.end.After(from) {
			continue
		}
		if s.start.Before(from) {
			s.start = from
		}
		out = append(out, s)
	}
	return out
}

// fill takes need of working time from the calendar's free time — its open
// intervals less booked — from the instant from on, piece by piece in
// order, looking no further than the civil day of until (M35, M35a). It
// answers the pieces and the instant the last one ends, or ok false when the
// horizon comes first. No need at all is no piece, finishing at from.
//
// The bookings are joined once into disjoint spans; a binary search finds
// the first that ends after from — an instant inside one moves on to its
// end, and so to the first day with free time — and from there one index
// moves forward with the days, so a fill reads each booking at most once
// however many there are.
func (c workCalendar) fill(from time.Time, need time.Duration, booked []span, until time.Time) ([]span, time.Time, bool) {
	if need <= 0 {
		return nil, from, true
	}
	busy := busySpans(booked)
	i := sort.Search(len(busy), func(k int) bool { return busy[k].end.After(from) })
	if i < len(busy) && !busy[i].start.After(from) {
		from = busy[i].end
		i++
	}
	last := holidays.DayIn(until, c.zone)
	var pieces []span
	// take uses the free span f; it reports whether need is met.
	take := func(f span) bool {
		if f.length() >= need {
			pieces = append(pieces, span{start: f.start, end: f.start.Add(need)})
			return true
		}
		pieces = append(pieces, f)
		need -= f.length()
		return false
	}
	for d := holidays.DayIn(from, c.zone); !d.After(last); d = d.AddDate(0, 0, 1) {
		open, _ := c.day(d)
		for _, o := range after(open, from) {
			cur := o.start
			for i < len(busy) && !busy[i].end.After(cur) {
				i++
			}
			for cur.Before(o.end) {
				if i >= len(busy) || !busy[i].start.Before(o.end) {
					if take(span{start: cur, end: o.end}) {
						return pieces, pieces[len(pieces)-1].end, true
					}
					break
				}
				if busy[i].start.After(cur) && take(span{start: cur, end: busy[i].start}) {
					return pieces, pieces[len(pieces)-1].end, true
				}
				cur = busy[i].end
				if cur.After(o.end) {
					break // the booking runs on past this interval; the index stays on it
				}
				i++
			}
		}
	}
	return nil, time.Time{}, false
}

// book is booked with pieces added, sorted by start: two sorted lists
// merged, so adding a work order's pieces costs a pass, not a sort.
func book(booked, pieces []span) []span {
	sorted := slices.Clone(pieces)
	slices.SortFunc(sorted, func(a, b span) int { return a.start.Compare(b.start) })
	out := make([]span, 0, len(booked)+len(sorted))
	i, j := 0, 0
	for i < len(booked) && j < len(sorted) {
		if sorted[j].start.Before(booked[i].start) {
			out = append(out, sorted[j])
			j++
		} else {
			out = append(out, booked[i])
			i++
		}
	}
	out = append(out, booked[i:]...)
	return append(out, sorted[j:]...)
}

// overlap is how much of s lies in w.
func overlap(s, w span) time.Duration {
	start, end := s.start, s.end
	if w.start.After(start) {
		start = w.start
	}
	if w.end.Before(end) {
		end = w.end
	}
	if !end.After(start) {
		return 0
	}
	return end.Sub(start)
}

// unionLength is the length of the union of spans: time counted once
// however many of them cover it (M40's machine time, the reports' time at a
// center).
func unionLength(spans []span) time.Duration {
	sorted := slices.Clone(spans)
	slices.SortFunc(sorted, func(a, b span) int { return a.start.Compare(b.start) })
	var total time.Duration
	var cur span
	open := false
	for _, s := range sorted {
		if !s.end.After(s.start) {
			continue
		}
		switch {
		case !open:
			cur, open = s, true
		case s.start.After(cur.end):
			total += cur.length()
			cur = s
		case s.end.After(cur.end):
			cur.end = s.end
		}
	}
	if open {
		total += cur.length()
	}
	return total
}

// clip is spans cut to w, those outside it dropped.
func clip(spans []span, w span) []span {
	out := make([]span, 0, len(spans))
	for _, s := range spans {
		if s.start.Before(w.start) {
			s.start = w.start
		}
		if s.end.After(w.end) {
			s.end = w.end
		}
		if s.end.After(s.start) {
			out = append(out, s)
		}
	}
	return out
}

// previousBusinessDays is the civil day n business days before d (M35a):
// each step goes back to the nearest earlier day that is neither a weekend
// day nor a Norwegian public holiday.
func previousBusinessDays(d time.Time, n int) time.Time {
	for range n {
		d = d.AddDate(0, 0, -1)
		for !holidays.BusinessDay(d) {
			d = d.AddDate(0, 0, -1)
		}
	}
	return d
}

// estimateDayMinutes is the working day the requested start's default
// assumes, 7.5 hours, for that estimate only (M35a).
const estimateDayMinutes = 450

// defaultRequestedStart is M35a's default: the due date less ⌈expected
// minutes / 450⌉ business days less 1 business day, clamped to today;
// today without a due date.
func defaultRequestedStart(due *time.Time, expectedMinutes int64, today time.Time) time.Time {
	if due == nil {
		return today
	}
	days := int((max(expectedMinutes, 0) + estimateDayMinutes - 1) / estimateDayMinutes)
	d := previousBusinessDays(*due, days+1)
	if d.Before(today) {
		return today
	}
	return d
}

// lateByDays is how many calendar days the finish's day in zone falls after
// due (M35): nil when it does not. A finish exactly at midnight belongs to
// the day it ends.
func lateByDays(finish *time.Time, due *time.Time, zone *time.Location) *int32 {
	if finish == nil || due == nil {
		return nil
	}
	day := holidays.DayIn(finish.Add(-time.Nanosecond), zone)
	n := int32(day.Sub(*due) / (24 * time.Hour))
	if n <= 0 {
		return nil
	}
	return &n
}
