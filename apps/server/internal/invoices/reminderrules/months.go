package reminderrules

import (
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
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
func EffectiveDue(due time.Time) time.Time { return holidays.NextBusinessDay(due) }

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
	return holidays.NextBusinessDay(addDays(sentOn, max(deadlineDays, 14)))
}
