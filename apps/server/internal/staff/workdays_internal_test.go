package staff

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// Working days are Monday to Friday without the public holidays of the
// platform calendar (design §4 Days): Easter's Maundy Thursday, Good Friday
// and Easter Monday, 17 May (Whit Monday too, in 2027), New Year's Day, and
// never a Saturday or a Sunday. Calendar days count every day.
func TestWorkingDays(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		from, to string
		working  int
		calendar int
	}{
		{"a Monday-to-Friday week", "2026-06-08", "2026-06-12", 5, 5},
		{"the week of Easter 2026, Monday to Easter Monday", "2026-03-30", "2026-04-06", 3, 8},
		{"the week of 17 May 2027, which is Whit Monday too", "2027-05-17", "2027-05-21", 4, 5},
		{"17 May 2026, a Sunday, in a Friday-to-Monday span", "2026-05-15", "2026-05-18", 2, 4},
		{"a weekend", "2026-06-13", "2026-06-14", 0, 2},
		{"across New Year", "2026-12-31", "2027-01-04", 2, 5},
		{"Christmas 2026", "2026-12-24", "2026-12-27", 1, 4},
		{"one day", "2026-06-10", "2026-06-10", 1, 1},
		{"the wrong way round", "2026-06-12", "2026-06-08", 0, 0},
	} {
		from, to := day(t, c.from), day(t, c.to)
		if got := workingDays(from, to); got != c.working {
			t.Errorf("%s: workingDays(%s, %s) = %d, want %d", c.name, c.from, c.to, got, c.working)
		}
		if got := calendarDays(from, to); got != c.calendar {
			t.Errorf("%s: calendarDays(%s, %s) = %d, want %d", c.name, c.from, c.to, got, c.calendar)
		}
	}
}

// A row's share of each day (plan pre-flight I4): its percent on any type
// but the sick kinds, whose percent is 100 and whose share is their degree —
// 100 when a sykmelding names none — in hundredths of a percent.
func TestShareOfARow(t *testing.T) {
	t.Parallel()
	pct := func(h int64) pgtype.Numeric { return numericOfHundredths(h) }
	for _, c := range []struct {
		name            string
		kind            string
		percent, degree pgtype.Numeric
		want            int64
	}{
		{"a whole day of holiday", "holiday", pct(10000), pgtype.Numeric{}, 10000},
		{"half a day of leave", "leave", pct(5000), pgtype.Numeric{}, 5000},
		{"a graded sykmelding", "sick_certified", pct(10000), pct(4000), 4000},
		{"a sykmelding naming no degree", "sick_certified", pct(10000), pgtype.Numeric{}, 10000},
		{"an egenmelding", "sick_self", pct(10000), pgtype.Numeric{}, 10000},
		{"a leave row's degree is not its share", "leave", pct(7500), pct(2000), 7500},
	} {
		if got := shareOf(c.kind, c.percent, c.degree); got != c.want {
			t.Errorf("%s: shareOf = %d, want %d", c.name, got, c.want)
		}
	}
}
