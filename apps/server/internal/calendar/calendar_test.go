package calendar

import (
	"slices"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Oslo wherever the tests run
)

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func dates(ds []time.Time) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Format(time.DateOnly)
	}
	return out
}

// The Norwegian public holidays of 2024–2030, pinned by hand (lov om
// helligdager og helligdagsfred § 2, lov om 1. og 17. mai som høgtidsdager):
// New Year's Day, Maundy Thursday, Good Friday, Easter Sunday and Monday,
// 1 May, 17 May, Ascension Day, Whit Sunday and Monday, Christmas Day and
// Boxing Day — in date order. 2027's Whit Monday is 17 May, one day twice.
var holidays = map[int][]string{
	2024: {"2024-01-01", "2024-03-28", "2024-03-29", "2024-03-31", "2024-04-01", "2024-05-01", "2024-05-09", "2024-05-17", "2024-05-19", "2024-05-20", "2024-12-25", "2024-12-26"},
	2025: {"2025-01-01", "2025-04-17", "2025-04-18", "2025-04-20", "2025-04-21", "2025-05-01", "2025-05-17", "2025-05-29", "2025-06-08", "2025-06-09", "2025-12-25", "2025-12-26"},
	2026: {"2026-01-01", "2026-04-02", "2026-04-03", "2026-04-05", "2026-04-06", "2026-05-01", "2026-05-14", "2026-05-17", "2026-05-24", "2026-05-25", "2026-12-25", "2026-12-26"},
	2027: {"2027-01-01", "2027-03-25", "2027-03-26", "2027-03-28", "2027-03-29", "2027-05-01", "2027-05-06", "2027-05-16", "2027-05-17", "2027-12-25", "2027-12-26"},
	2028: {"2028-01-01", "2028-04-13", "2028-04-14", "2028-04-16", "2028-04-17", "2028-05-01", "2028-05-17", "2028-05-25", "2028-06-04", "2028-06-05", "2028-12-25", "2028-12-26"},
	2029: {"2029-01-01", "2029-03-29", "2029-03-30", "2029-04-01", "2029-04-02", "2029-05-01", "2029-05-10", "2029-05-17", "2029-05-20", "2029-05-21", "2029-12-25", "2029-12-26"},
	2030: {"2030-01-01", "2030-04-18", "2030-04-19", "2030-04-21", "2030-04-22", "2030-05-01", "2030-05-17", "2030-05-30", "2030-06-09", "2030-06-10", "2030-12-25", "2030-12-26"},
}

// Every day of 2024–2030 is a public holiday exactly when the pinned table
// says so, whatever weekday it falls on: 17 May 2026 is a Sunday, Christmas
// Day 2027 and Boxing Day 2026 Saturdays, and each is still a holiday.
func TestCalendar_PublicHoliday(t *testing.T) {
	t.Parallel()
	for year := 2024; year <= 2030; year++ {
		want := holidays[year]
		for d := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC); d.Year() == year; d = d.AddDate(0, 0, 1) {
			if got, w := PublicHoliday(d), slices.Contains(want, d.Format(time.DateOnly)); got != w {
				t.Errorf("PublicHoliday(%s, a %s) = %v, want %v", d.Format(time.DateOnly), d.Weekday(), got, w)
			}
		}
	}
	for _, c := range []struct {
		d       string
		weekday time.Weekday
	}{{"2026-05-17", time.Sunday}, {"2027-12-25", time.Saturday}, {"2026-12-26", time.Saturday}, {"2027-05-17", time.Monday}} {
		if d := day(c.d); d.Weekday() != c.weekday || !PublicHoliday(d) {
			t.Errorf("%s: weekday %s (want %s), holiday %v (want true)", c.d, d.Weekday(), c.weekday, PublicHoliday(d))
		}
	}
	for _, s := range []string{"2026-12-24", "2026-12-31", "2026-04-04", "2026-05-02", "2026-05-16"} {
		if PublicHoliday(day(s)) {
			t.Errorf("PublicHoliday(%s) = true; Christmas Eve, New Year's Eve, Holy Saturday and the eves are not holidays in law", s)
		}
	}
}

// A time that is not a UTC midnight is read as its civil date where it is
// stated: 10:00Z and midnight in Oslo on Maundy Thursday and 17 May are those
// holidays, and a business-day question about them gets the same answer.
func TestCalendar_PublicHolidayReadsTheCivilDate(t *testing.T) {
	t.Parallel()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []time.Time{
		time.Date(2026, time.April, 2, 10, 0, 0, 0, time.UTC),
		time.Date(2026, time.April, 2, 0, 0, 0, 0, oslo),
		time.Date(2027, time.May, 17, 10, 0, 0, 0, time.UTC),
		time.Date(2027, time.May, 17, 0, 0, 0, 0, oslo),
		time.Date(2026, time.May, 14, 23, 59, 0, 0, oslo), // Ascension Day
	} {
		if !PublicHoliday(d) || BusinessDay(d) {
			t.Errorf("%s: PublicHoliday %v, BusinessDay %v; want a holiday", d.Format(time.RFC3339), PublicHoliday(d), BusinessDay(d))
		}
	}
	if d := time.Date(2026, time.April, 1, 23, 0, 0, 0, oslo); PublicHoliday(d) {
		t.Errorf("%s, the evening before Maundy Thursday in Oslo, is a holiday", d.Format(time.RFC3339))
	}
}

// Gregorian Easter Sunday across the algorithm's range: the latest possible
// date (25 April: 1943, 2038), the early ones (23 March: 1913, 2008), the
// century years 1900, 2000 and 2100 (2100 not a leap year), and the table
// Invoices pinned (2024–2028), now this package's.
func TestCalendar_EasterSunday(t *testing.T) {
	t.Parallel()
	for year, want := range map[int]string{
		1900: "1900-04-15", 1913: "1913-03-23", 1943: "1943-04-25", 1954: "1954-04-18", 1961: "1961-04-02",
		1981: "1981-04-19", 2000: "2000-04-23", 2008: "2008-03-23", 2011: "2011-04-24", 2019: "2019-04-21",
		2024: "2024-03-31", 2025: "2025-04-20", 2026: "2026-04-05", 2027: "2027-03-28", 2028: "2028-04-16",
		2029: "2029-04-01", 2030: "2030-04-21", 2038: "2038-04-25", 2049: "2049-04-18", 2076: "2076-04-19",
		2099: "2099-04-12", 2100: "2100-03-28",
	} {
		got := EasterSunday(year)
		if !got.Equal(day(want)) || got.Location() != time.UTC || got.Weekday() != time.Sunday {
			t.Errorf("EasterSunday(%d) = %s (%s, %s), want %s, a Sunday at UTC midnight", year, got.Format(time.RFC3339), got.Weekday(), got.Location(), want)
		}
	}
}

// A business day is neither a Saturday, a Sunday nor a public holiday.
func TestCalendar_BusinessDay(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d    string
		want bool
	}{
		{"2026-10-12", true},  // Monday
		{"2026-10-09", true},  // Friday
		{"2026-10-10", false}, // Saturday
		{"2026-10-11", false}, // Sunday
		{"2026-04-02", false}, // Maundy Thursday
		{"2026-05-14", false}, // Ascension Day, a Thursday
		{"2026-05-25", false}, // Whit Monday
		{"2026-12-24", true},  // Christmas Eve, a Thursday
		{"2027-01-01", false}, // New Year's Day, a Friday
	} {
		if got := BusinessDay(day(c.d)); got != c.want {
			t.Errorf("BusinessDay(%s) = %v, want %v", c.d, got, c.want)
		}
	}
}

// NextBusinessDay is the day itself when it is a business day, else the first
// one after it — across Easter, Christmas and New Year.
func TestCalendar_NextBusinessDay(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ d, want string }{
		{"2026-10-12", "2026-10-12"}, // Monday stays
		{"2026-10-10", "2026-10-12"}, // Saturday → Monday
		{"2027-03-25", "2027-03-30"}, // Maundy Thursday → the Tuesday after Easter Monday
		{"2027-03-27", "2027-03-30"}, // Holy Saturday
		{"2026-04-03", "2026-04-07"}, // Good Friday 2026
		{"2026-12-25", "2026-12-28"}, // Christmas Day, Boxing Day a Saturday, then Sunday
		{"2026-12-31", "2026-12-31"}, // New Year's Eve is a business day
		{"2027-01-01", "2027-01-04"}, // New Year's Day, a Friday, then the weekend
		{"2027-12-25", "2027-12-27"}, // Christmas 2027 on the weekend
		{"2027-05-16", "2027-05-18"}, // Whit Sunday, then Whit Monday that is also 17 May
	} {
		if got := NextBusinessDay(day(c.d)); !got.Equal(day(c.want)) {
			t.Errorf("NextBusinessDay(%s) = %s, want %s", c.d, got.Format(time.DateOnly), c.want)
		}
	}
}

// PublicHolidays lists the holidays from from to to, both counted, in date
// order, each day once: a whole year gives the pinned table, a range across a
// year end takes both years' days, a range without one or an inverted range
// gives none.
func TestCalendar_PublicHolidays(t *testing.T) {
	t.Parallel()
	for year := 2024; year <= 2030; year++ {
		got := PublicHolidays(time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC), time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC))
		if !slices.Equal(dates(got), holidays[year]) {
			t.Errorf("PublicHolidays(%d) = %v, want %v", year, dates(got), holidays[year])
		}
		for _, d := range got {
			if d.Location() != time.UTC || !d.Equal(d.Truncate(24*time.Hour)) {
				t.Errorf("PublicHolidays(%d) gives %s; every day is UTC midnight", year, d.Format(time.RFC3339))
			}
		}
	}
	for _, c := range []struct {
		from, to string
		want     []string
	}{
		{"2026-12-24", "2027-01-02", []string{"2026-12-25", "2026-12-26", "2027-01-01"}},
		{"2026-04-02", "2026-04-02", []string{"2026-04-02"}}, // one day, both ends counted
		{"2026-04-03", "2026-04-06", []string{"2026-04-03", "2026-04-05", "2026-04-06"}},
		{"2026-06-01", "2026-06-30", []string{}},
		{"2026-05-17", "2026-05-01", []string{}}, // inverted
	} {
		if got := dates(PublicHolidays(day(c.from), day(c.to))); !slices.Equal(got, c.want) {
			t.Errorf("PublicHolidays(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
