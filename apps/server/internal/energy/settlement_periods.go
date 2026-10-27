package energy

import (
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the billing periods (energy consumption billing design D4,
// energy settlements plan reading 2): the civil month, quarter or year in the
// point's market time zone, clipped to the supply period, and the civil days a
// period counts for proration (reading 5). Pure: no clock, no database.

// billingPeriodFloor is the earliest instant a billing period starts: a
// supply period starting before it is settled from it, so a malformed start
// never enumerates centuries.
var billingPeriodFloor = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// billingPeriod is one half-open period [Start, End), as instants.
type billingPeriod struct {
	Start, End time.Time
}

// marketLocation is the point's market time zone (marketTimeZone) loaded.
func marketLocation(priceArea string) (*time.Location, error) {
	name := marketTimeZone(&priceArea)
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("energy: load the market time zone %s: %w", name, err)
	}
	return loc, nil
}

// cadenceMonths is how many calendar months one period of the cadence spans.
func cadenceMonths(cadence string) (int, error) {
	switch cadence {
	case contracts.CadenceMonthly:
		return 1, nil
	case contracts.CadenceQuarterly:
		return 3, nil
	case contracts.CadenceYearly:
		return 12, nil
	}
	return 0, fmt.Errorf("energy: %q is not a billing cadence", cadence)
}

// cadenceUnitStart is the start, in loc, of the cadence's calendar unit
// holding t: the first of its month, of its quarter (January, April, July,
// October) or of its year.
func cadenceUnitStart(t time.Time, months int, loc *time.Location) time.Time {
	l := t.In(loc)
	month := l.Month()
	switch months {
	case 3:
		month = time.Month((int(month)-1)/3*3 + 1)
	case 12:
		month = time.January
	}
	return time.Date(l.Year(), month, 1, 0, 0, 0, 0, loc)
}

// billingPeriods is the supply period's billing periods that end on or before
// through, oldest first: each calendar unit of the cadence in loc that the
// supply period [supplyStart, supplyEnd) overlaps, clipped to it. supplyEnd
// nil is open-ended. The caller passes the earlier of the request's Through
// and now as through, so a period still running is never answered.
func billingPeriods(supplyStart time.Time, supplyEnd *time.Time, cadence string, loc *time.Location, through time.Time) ([]billingPeriod, error) {
	months, err := cadenceMonths(cadence)
	if err != nil {
		return nil, err
	}
	start := supplyStart
	if start.Before(billingPeriodFloor) {
		start = billingPeriodFloor
	}
	var out []billingPeriod
	for unit := cadenceUnitStart(start, months, loc); unit.Before(through); {
		next := time.Date(unit.Year(), unit.Month()+time.Month(months), 1, 0, 0, 0, 0, loc)
		p := billingPeriod{Start: unit, End: next}
		if p.Start.Before(start) {
			p.Start = start
		}
		if supplyEnd != nil && supplyEnd.Before(p.End) {
			p.End = *supplyEnd
		}
		if !p.End.After(p.Start) {
			break // the supply period ended before this unit
		}
		if p.End.After(through) {
			break
		}
		out = append(out, p)
		if supplyEnd != nil && !supplyEnd.After(next) {
			break
		}
		unit = next
	}
	return out, nil
}

// civilDate is the civil day in loc holding t, as midnight UTC of that date —
// the way a date column reads.
func civilDate(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// countedDays is the civil days (as civilDate answers them) whose midnight in
// loc lies in [from, to): the days a period counts for proration. A day
// belongs to the period holding its start, so two supply periods meeting at
// 14:00 never both count that day (plan reading 5).
func countedDays(from, to time.Time, loc *time.Location) []time.Time {
	var days []time.Time
	first := civilDate(from, loc)
	if !time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc).Equal(from.In(loc)) {
		first = first.AddDate(0, 0, 1) // from is after the day's midnight
	}
	for d := first; ; d = d.AddDate(0, 0, 1) {
		midnight := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
		if !midnight.Before(to) {
			return days
		}
		if !midnight.Before(from) {
			days = append(days, d)
		}
	}
}

// daysInMonth is how many days the civil month of d has.
func daysInMonth(d time.Time) int {
	return time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// daysInYear is how many days the civil year of d has.
func daysInYear(d time.Time) int {
	return time.Date(d.Year(), time.December, 31, 0, 0, 0, 0, time.UTC).YearDay()
}
