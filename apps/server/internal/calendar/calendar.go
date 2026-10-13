package calendar

import "time"

// PublicHoliday is a Norwegian public holiday (lov om helligdager og
// helligdagsfred § 2, lov om 1. og 17. mai som høgtidsdager): New Year's Day,
// Maundy Thursday, Good Friday, Easter Sunday and Monday, 1 May, 17 May,
// Ascension Day, Whit Sunday and Monday, Christmas Day and Boxing Day. An
// ordinary Sunday, a helligdag in law too, is not listed: BusinessDay's
// weekend test covers it. d is read as its civil date in its own location, so
// 10:00Z or midnight in Oslo on a holiday is that holiday.
func PublicHoliday(d time.Time) bool {
	y, mm, dd := d.Date()
	d = time.Date(y, mm, dd, 0, 0, 0, 0, time.UTC)
	switch m, day := d.Month(), d.Day(); {
	case m == time.January && day == 1,
		m == time.May && (day == 1 || day == 17),
		m == time.December && (day == 25 || day == 26):
		return true
	}
	easter := EasterSunday(d.Year())
	for _, offset := range []int{-3, -2, 0, 1, 39, 49, 50} {
		if d.Equal(addDays(easter, offset)) {
			return true
		}
	}
	return false
}

// EasterSunday is the Gregorian Easter Sunday of year (the anonymous
// Gregorian algorithm), as UTC midnight.
func EasterSunday(year int) time.Time {
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

// BusinessDay is a day that is neither a Saturday, a Sunday nor a Norwegian
// public holiday.
func BusinessDay(d time.Time) bool {
	return d.Weekday() != time.Saturday && d.Weekday() != time.Sunday && !PublicHoliday(d)
}

// NextBusinessDay is d, or the first day after it that is neither a weekend
// day nor a Norwegian public holiday.
func NextBusinessDay(d time.Time) time.Time {
	for !BusinessDay(d) {
		d = addDays(d, 1)
	}
	return d
}

// PublicHolidays is the Norwegian public holidays from from to to, both
// counted, in date order and each day once (Whit Monday can be 17 May); none
// when to is before from.
func PublicHolidays(from, to time.Time) []time.Time {
	var out []time.Time
	for d := from; !d.After(to); d = addDays(d, 1) {
		if PublicHoliday(d) {
			out = append(out, d)
		}
	}
	return out
}

// addDays is d moved n calendar days. Days here are UTC midnights, so a day is
// always 24 hours.
func addDays(d time.Time, n int) time.Time { return d.AddDate(0, 0, n) }
