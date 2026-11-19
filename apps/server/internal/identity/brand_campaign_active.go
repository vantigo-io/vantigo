package identity

import (
	"time"
)

// The pure rules of brand campaigns (brands design B22, B23): when a campaign
// is active on a calendar date, whether two campaigns share a date, and the
// next date the active campaign changes. Nothing here reads the clock, the
// database or a time zone: a date is a calendar day, at midnight UTC, in the
// workspace's business time zone, which the caller has already applied.

const (
	// campaignMaxYearlyDays is the longest a yearly range may be (B22).
	campaignMaxYearlyDays = 365

	// campaignMinYear and campaignMaxYear bound the dates a campaign may name,
	// so the overlap check's walk is bounded.
	campaignMinYear = 1970
	campaignMaxYear = 2200
)

// campaignSpan is the part of a campaign `active` reads.
type campaignSpan struct {
	StartsOn time.Time
	EndsOn   time.Time
	Yearly   bool
}

// civil is a month and a day, ordered by compare.
type civil struct {
	month time.Month
	day   int
}

func (a civil) before(b civil) bool {
	return a.month < b.month || a.month == b.month && a.day < b.day
}

func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// inYear is md as it falls in year: 29 February is 28 February in a year
// without one (B22).
func (md civil) inYear(year int) civil {
	if md.month == time.February && md.day == 29 && !isLeap(year) {
		return civil{time.February, 28}
	}
	return md
}

func civilOf(t time.Time) civil { return civil{t.Month(), t.Day()} }

// wraps reports that a yearly range ends before it starts in the calendar year,
// so it crosses the year end (1 December to 6 January).
func (c campaignSpan) wraps() bool {
	return civilOf(c.EndsOn).before(civilOf(c.StartsOn))
}

// campaignActive is the one rule (B22) the resolver, the overlap check and the
// tests all call: whether the campaign is in force on date, a calendar date.
//
//   - Not yearly: StartsOn <= date <= EndsOn.
//   - Yearly: month and day only, StartsOn's year being the first active year.
//     A range that ends before it starts in the calendar wraps the year end, and
//     its end part belongs to the year after the start part's.
//
// 29 February is 28 February in a non-leap year.
func campaignActive(c campaignSpan, date time.Time) bool {
	if !c.Yearly {
		return !date.Before(c.StartsOn) && !date.After(c.EndsOn)
	}
	year := date.Year()
	first := c.StartsOn.Year()
	s := civilOf(c.StartsOn).inYear(year)
	e := civilOf(c.EndsOn).inYear(year)
	d := civilOf(date)
	if !c.wraps() {
		return year >= first && !d.before(s) && !e.before(d)
	}
	// The start part is in the year of date and needs it to be a first-or-later
	// year; the end part is the year after the start's, so it needs the year
	// before date to be one.
	if year >= first && !d.before(s) {
		return true
	}
	return year-1 >= first && !e.before(d)
}

// campaignDays is how many days the range spans, both ends counted. A yearly
// range may not span more than a year (B22: 365 days).
func campaignDays(c campaignSpan) int {
	return int(c.EndsOn.Sub(c.StartsOn).Hours()/24) + 1
}

// campaignWindow is the dates two campaigns are compared over (B23): from the
// earlier start for two years, widened to take in a dated campaign's whole
// range and, for a campaign that begins later, the two years after its start,
// so that a year wrapping New Year and a leap day are both exercised.
func campaignWindow(a, b campaignSpan) (from, to time.Time) {
	from = a.StartsOn
	if b.StartsOn.Before(from) {
		from = b.StartsOn
	}
	to = from.AddDate(2, 0, 0)
	for _, c := range []campaignSpan{a, b} {
		if end := c.StartsOn.AddDate(2, 0, 0); end.After(to) {
			to = end
		}
		if !c.Yearly && c.EndsOn.After(to) {
			to = c.EndsOn
		}
	}
	return from, to
}

// campaignsOverlap reports a date on which both campaigns are active (B23),
// over campaignWindow.
func campaignsOverlap(a, b campaignSpan) (time.Time, bool) {
	from, to := campaignWindow(a, b)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if campaignActive(a, d) && campaignActive(b, d) {
			return d, true
		}
	}
	return time.Time{}, false
}

// activeCampaign is the index in cs of the campaign active on date, or -1. The
// campaigns of one brand never overlap, so there is at most one; on corrupt
// data the first wins.
func activeCampaign(cs []campaignSpan, date time.Time) int {
	for i, c := range cs {
		if campaignActive(c, date) {
			return i
		}
	}
	return -1
}

// nextCampaignChange is the first date after today on which the active campaign
// of cs differs from today's (B24), and whether there is one within the years
// the campaigns can still change in. The boundary candidates are the day each
// campaign starts and the day after it ends, in the current and the following
// two years for a yearly one.
func nextCampaignChange(cs []campaignSpan, today time.Time) (time.Time, bool) {
	current := activeCampaign(cs, today)
	var candidates []time.Time
	add := func(t time.Time) {
		if t.After(today) {
			candidates = append(candidates, t)
		}
	}
	for _, c := range cs {
		if !c.Yearly {
			add(c.StartsOn)
			add(c.EndsOn.AddDate(0, 0, 1))
			continue
		}
		for y := today.Year(); y <= today.Year()+2; y++ {
			s := civilOf(c.StartsOn).inYear(y)
			e := civilOf(c.EndsOn).inYear(y)
			add(time.Date(y, s.month, s.day, 0, 0, 0, 0, time.UTC))
			add(time.Date(y, e.month, e.day+1, 0, 0, 0, 0, time.UTC))
		}
	}
	var best time.Time
	found := false
	for _, t := range candidates {
		if found && !t.Before(best) {
			continue
		}
		if activeCampaign(cs, t) != current {
			best, found = t, true
		}
	}
	return best, found
}
