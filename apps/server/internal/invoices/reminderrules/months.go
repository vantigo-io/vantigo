package reminderrules

import (
	"fmt"
	"time"
)

// AddMonthsClamped is d moved n months (n may be negative), on the same day of
// the month clamped to that month's last day: 31 August + 6 is 28 February,
// or 29 in a leap year, where AddDate would overflow into 3 March. Every
// "six months" of D8 is measured with it.
func AddMonthsClamped(d time.Time, n int) time.Time {
	year, month, dd := d.Date()
	months := int(month) - 1 + n
	years := months / 12
	if months%12 < 0 {
		years--
	}
	year += years
	month = time.Month(months-years*12) + 1
	if last := lastDayOf(year, month); dd > last {
		dd = last
	}
	return time.Date(year, month, dd, d.Hour(), d.Minute(), d.Second(), d.Nanosecond(), d.Location())
}

// lastDayOf is the number of days in the month.
func lastDayOf(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// EffectiveDue is E: the due date, moved to the next business day when it is a
// Saturday, a Sunday or a Norwegian public holiday (R4 §2.7, the lenient
// reading; the spec's reading 8 as amended at Task 4's review) — the same
// rule as the deadline's, and it only ever delays a charge.
func EffectiveDue(due time.Time) time.Time { return nextBusinessDay(due) }

// addDays is d moved n calendar days. Days here are UTC midnights, so a day is
// always 24 hours.
func addDays(d time.Time, n int) time.Time { return d.AddDate(0, 0, n) }

// daysFrom is the number of days from from to to, both counted.
func daysFrom(from, to time.Time) int64 { return int64(to.Sub(from)/(24*time.Hour)) + 1 }

// halfYearStart is 1 January or 1 July of the half-year d falls in.
func halfYearStart(d time.Time) time.Time {
	month := time.January
	if d.Month() >= time.July {
		month = time.July
	}
	return time.Date(d.Year(), month, 1, 0, 0, 0, 0, time.UTC)
}

// halfYearName is the half-year d falls in as the contract names it, "2027-H1".
func halfYearName(d time.Time) string {
	half := 1
	if d.Month() >= time.July {
		half = 2
	}
	return fmt.Sprintf("%d-H%d", d.Year(), half)
}

// LetterDeadline is the deadline a letter sent on sentOn states (D8, Task 4
// review): deadlineDays after it, never fewer than 14 (INKL § 9, INKF § 1-3),
// moved to the next business day when it falls on a Saturday, a Sunday or a
// Norwegian public holiday — the debtor-favourable reading, since a payment
// order cannot reach a bank on such a day. E moves by the same rule.
func LetterDeadline(sentOn time.Time, deadlineDays int) time.Time {
	return nextBusinessDay(addDays(sentOn, max(deadlineDays, 14)))
}

// nextBusinessDay is d, or the first day after it that is neither a weekend
// day nor a Norwegian public holiday.
func nextBusinessDay(d time.Time) time.Time {
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday || publicHoliday(d) {
		d = addDays(d, 1)
	}
	return d
}

// publicHoliday is a Norwegian public holiday (lov om helligdager og
// helligdagsfred § 2, lov om 1. og 17. mai som høgtidsdager): New Year's Day,
// Maundy Thursday, Good Friday, Easter Sunday and Monday, 1 May, 17 May,
// Ascension Day, Whit Sunday and Monday, Christmas Day and Boxing Day.
func publicHoliday(d time.Time) bool {
	switch m, day := d.Month(), d.Day(); {
	case m == time.January && day == 1,
		m == time.May && (day == 1 || day == 17),
		m == time.December && (day == 25 || day == 26):
		return true
	}
	easter := easterSunday(d.Year())
	for _, offset := range []int{-3, -2, 0, 1, 39, 49, 50} {
		if d.Equal(addDays(easter, offset)) {
			return true
		}
	}
	return false
}

// easterSunday is the Gregorian Easter Sunday of year (the anonymous
// Gregorian algorithm), as UTC midnight.
func easterSunday(year int) time.Time {
	a, b, c := year%19, year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}
