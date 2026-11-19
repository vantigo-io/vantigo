package identity

import (
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func dated(from, to time.Time) campaignSpan { return campaignSpan{StartsOn: from, EndsOn: to} }
func yearly(from, to time.Time) campaignSpan {
	return campaignSpan{StartsOn: from, EndsOn: to, Yearly: true}
}

func TestCampaignActive_DatedIsInclusiveAtBothEnds(t *testing.T) {
	t.Parallel()
	c := dated(day(2026, time.June, 1), day(2026, time.June, 30))
	for _, tc := range []struct {
		d    time.Time
		want bool
	}{
		{day(2026, time.May, 31), false},
		{day(2026, time.June, 1), true},
		{day(2026, time.June, 15), true},
		{day(2026, time.June, 30), true},
		{day(2026, time.July, 1), false},
		{day(2027, time.June, 15), false}, // not yearly
	} {
		if got := campaignActive(c, tc.d); got != tc.want {
			t.Errorf("dated 1-30 June on %s = %v, want %v", tc.d.Format(time.DateOnly), got, tc.want)
		}
	}
}

func TestCampaignActive_YearlyUsesMonthAndDayAndIsNeverActiveBeforeItsFirstYear(t *testing.T) {
	t.Parallel()
	c := yearly(day(2026, time.June, 1), day(2026, time.June, 30))
	for _, tc := range []struct {
		d    time.Time
		want bool
	}{
		{day(2025, time.June, 15), false}, // before the first active year
		{day(2026, time.June, 15), true},
		{day(2030, time.June, 1), true},
		{day(2030, time.June, 30), true},
		{day(2030, time.July, 1), false},
		{day(2030, time.May, 31), false},
	} {
		if got := campaignActive(c, tc.d); got != tc.want {
			t.Errorf("yearly June on %s = %v, want %v", tc.d.Format(time.DateOnly), got, tc.want)
		}
	}
}

func TestCampaignActive_YearlyWrapsTheYearEnd(t *testing.T) {
	t.Parallel()
	c := yearly(day(2026, time.December, 1), day(2027, time.January, 6))
	if !c.wraps() {
		t.Fatal("1 December to 6 January does not wrap")
	}
	for _, tc := range []struct {
		d    time.Time
		want bool
	}{
		{day(2026, time.November, 30), false},
		{day(2026, time.December, 1), true},
		{day(2026, time.December, 31), true},
		{day(2027, time.January, 1), true},
		{day(2027, time.January, 6), true},
		{day(2027, time.January, 7), false},
		{day(2026, time.January, 3), false}, // the end part of a year that has no start part yet
		{day(2026, time.January, 6), false},
		{day(2031, time.December, 31), true},
		{day(2032, time.January, 6), true},
		{day(2032, time.January, 7), false},
	} {
		if got := campaignActive(c, tc.d); got != tc.want {
			t.Errorf("yearly 1 Dec - 6 Jan on %s = %v, want %v", tc.d.Format(time.DateOnly), got, tc.want)
		}
	}
}

func TestCampaignActive_TwentyNinthOfFebruaryIsTheTwentyEighthInAYearWithout(t *testing.T) {
	t.Parallel()
	// A range ending on 29 February ends on the 28th in 2027 and on the 29th in 2028.
	c := yearly(day(2024, time.February, 1), day(2024, time.February, 29))
	for _, tc := range []struct {
		d    time.Time
		want bool
	}{
		{day(2027, time.February, 28), true},
		{day(2027, time.March, 1), false},
		{day(2028, time.February, 28), true},
		{day(2028, time.February, 29), true},
		{day(2028, time.March, 1), false},
	} {
		if got := campaignActive(c, tc.d); got != tc.want {
			t.Errorf("1-29 Feb on %s = %v, want %v", tc.d.Format(time.DateOnly), got, tc.want)
		}
	}
	// A range starting on 29 February starts on the 28th in 2027.
	s := yearly(day(2024, time.February, 29), day(2024, time.March, 10))
	for _, tc := range []struct {
		d    time.Time
		want bool
	}{
		{day(2027, time.February, 27), false},
		{day(2027, time.February, 28), true},
		{day(2028, time.February, 28), false},
		{day(2028, time.February, 29), true},
	} {
		if got := campaignActive(s, tc.d); got != tc.want {
			t.Errorf("29 Feb - 10 Mar on %s = %v, want %v", tc.d.Format(time.DateOnly), got, tc.want)
		}
	}
}

func TestCampaignDays_CountsBothEnds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		c    campaignSpan
		want int
	}{
		{"one day", yearly(day(2026, time.June, 1), day(2026, time.June, 1)), 1},
		{"1 Dec - 6 Jan", yearly(day(2026, time.December, 1), day(2027, time.January, 6)), 37},
		{"a calendar year", yearly(day(2026, time.January, 1), day(2026, time.December, 31)), 365},
		{"a leap calendar year", yearly(day(2028, time.January, 1), day(2028, time.December, 31)), 366},
		{"a year and a day", yearly(day(2026, time.January, 1), day(2027, time.January, 1)), 366},
		{"365 days across the year end", yearly(day(2026, time.March, 2), day(2027, time.March, 1)), 365},
	} {
		if got := campaignDays(tc.c); got != tc.want {
			t.Errorf("%s: %d days, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCampaignsOverlap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		a, b campaignSpan
		want bool
	}{
		{"disjoint dated", dated(day(2026, time.June, 1), day(2026, time.June, 10)), dated(day(2026, time.June, 11), day(2026, time.June, 20)), false},
		{"touching dated", dated(day(2026, time.June, 1), day(2026, time.June, 10)), dated(day(2026, time.June, 10), day(2026, time.June, 20)), true},
		{"yearly against a dated one in a later year", yearly(day(2026, time.December, 1), day(2027, time.January, 6)), dated(day(2030, time.December, 24), day(2030, time.December, 26)), true},
		{"yearly against a dated one before its first year", yearly(day(2026, time.December, 1), day(2027, time.January, 6)), dated(day(2025, time.December, 24), day(2025, time.December, 26)), false},
		{"a wrapping one against one in January", yearly(day(2026, time.December, 1), day(2027, time.January, 6)), yearly(day(2026, time.January, 5), day(2026, time.January, 20)), true},
		{"a wrapping one against one that starts the day after it ends", yearly(day(2026, time.December, 1), day(2027, time.January, 6)), yearly(day(2026, time.January, 7), day(2026, time.January, 20)), false},
		{"two yearly ones apart", yearly(day(2026, time.June, 1), day(2026, time.June, 30)), yearly(day(2026, time.July, 1), day(2026, time.July, 31)), false},
		{"yearly ones whose first years differ", yearly(day(2026, time.June, 1), day(2026, time.June, 30)), yearly(day(2040, time.June, 15), day(2040, time.July, 15)), true},
		{"a yearly one against a dated one far in the future", yearly(day(2026, time.June, 1), day(2026, time.June, 30)), dated(day(2060, time.June, 29), day(2060, time.July, 2)), true},
		{"a leap day", yearly(day(2024, time.February, 29), day(2024, time.March, 5)), dated(day(2027, time.February, 28), day(2027, time.February, 28)), true},
	} {
		if _, got := campaignsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: overlap = %v, want %v", tc.name, got, tc.want)
		}
		if _, got := campaignsOverlap(tc.b, tc.a); got != tc.want {
			t.Errorf("%s (reversed): overlap = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNextCampaignChange(t *testing.T) {
	t.Parallel()
	jul := yearly(day(2026, time.December, 1), day(2027, time.January, 6))
	pride := yearly(day(2026, time.June, 1), day(2026, time.June, 30))
	for _, tc := range []struct {
		name  string
		cs    []campaignSpan
		today time.Time
		want  time.Time
		found bool
	}{
		{"none", nil, day(2026, time.September, 11), time.Time{}, false},
		{"before a yearly start", []campaignSpan{jul}, day(2026, time.September, 11), day(2026, time.December, 1), true},
		{"inside the wrap before New Year", []campaignSpan{jul}, day(2026, time.December, 20), day(2027, time.January, 7), true},
		{"inside the wrap after New Year", []campaignSpan{jul}, day(2027, time.January, 3), day(2027, time.January, 7), true},
		{"the day after the end", []campaignSpan{jul}, day(2027, time.January, 7), day(2027, time.December, 1), true},
		{"the nearest of two", []campaignSpan{jul, pride}, day(2026, time.September, 11), day(2026, time.December, 1), true},
		{"the nearest of two, in spring", []campaignSpan{jul, pride}, day(2027, time.March, 1), day(2027, time.June, 1), true},
		{"a dated one in the future", []campaignSpan{dated(day(2028, time.May, 1), day(2028, time.May, 3))}, day(2026, time.September, 11), day(2028, time.May, 1), true},
		{"a dated one over", []campaignSpan{dated(day(2026, time.May, 1), day(2026, time.May, 3))}, day(2026, time.September, 11), time.Time{}, false},
		{"a dated one ending today", []campaignSpan{dated(day(2026, time.May, 1), day(2026, time.September, 11))}, day(2026, time.September, 11), day(2026, time.September, 12), true},
		{"back to back campaigns", []campaignSpan{dated(day(2026, time.May, 1), day(2026, time.May, 3)), dated(day(2026, time.May, 4), day(2026, time.May, 9))}, day(2026, time.May, 2), day(2026, time.May, 4), true},
		{"a yearly one before its first year", []campaignSpan{yearly(day(2027, time.June, 1), day(2027, time.June, 30))}, day(2026, time.June, 10), day(2027, time.June, 1), true},
	} {
		got, found := nextCampaignChange(tc.cs, tc.today)
		if found != tc.found || !got.Equal(tc.want) {
			t.Errorf("%s: (%s, %v), want (%s, %v)", tc.name, got.Format(time.DateOnly), found, tc.want.Format(time.DateOnly), tc.found)
		}
	}
}
