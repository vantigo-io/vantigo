package energy_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the summary read of spot prices (spot-price service design
// S8): an area's prices per civil hour, day, ISO week or month in
// Europe/Oslo, time-weighted.

type spotPriceSummaryJSON struct {
	PriceArea string `json:"priceArea"`
	Bucket    string `json:"bucket"`
	TimeZone  string `json:"timeZone"`
	Buckets   []struct {
		Start          time.Time `json:"start"`
		End            time.Time `json:"end"`
		AvgNokPerKwh   float64   `json:"avgNokPerKwh"`
		MinNokPerKwh   float64   `json:"minNokPerKwh"`
		MaxNokPerKwh   float64   `json:"maxNokPerKwh"`
		CoveredMinutes int       `json:"coveredMinutes"`
		Preliminary    bool      `json:"preliminary"`
	} `json:"buckets"`
}

// oslo is a wall-clock time in Europe/Oslo.
func oslo(t *testing.T, year int, month time.Month, day, hour int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatalf("load Europe/Oslo: %v", err)
	}
	return time.Date(year, month, day, hour, 0, 0, 0, loc)
}

func getSpotPriceSummary(c *modtest.Client, area, bucket string, from, to time.Time) *modtest.Response {
	q := url.Values{"area": {area}, "bucket": {bucket}, "from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	return c.Do(http.MethodGet, "/api/v1/energy/spot-prices/summary?"+q.Encode(), nil)
}

// spotPriceSummaryOf is GET /spot-prices/summary, wanted 200.
func spotPriceSummaryOf(t *testing.T, c *modtest.Client, area, bucket string, from, to time.Time) spotPriceSummaryJSON {
	t.Helper()
	r := getSpotPriceSummary(c, area, bucket, from, to)
	if r.Status != http.StatusOK {
		t.Fatalf("summarise spot prices: status %d body %s", r.Status, r.Body)
	}
	var out spotPriceSummaryJSON
	r.JSON(&out)
	if out.PriceArea != area || out.Bucket != bucket || out.TimeZone != "Europe/Oslo" {
		t.Errorf("summary of %s per %s = %s per %s in %s", area, bucket, out.PriceArea, out.Bucket, out.TimeZone)
	}
	return out
}

// insertSpotPriceRun stores n consecutive intervals of an area, each step
// long, from from on, at one price and in one state.
func insertSpotPriceRun(t *testing.T, h *modtest.Harness, area string, from time.Time, step time.Duration, n int, price float64, state string) {
	t.Helper()
	h.Exec(t, `INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at, state)
SELECT $1, g, g + make_interval(secs => $4), $5, 'Import', $6, $7
FROM generate_series($2::timestamptz, $3::timestamptz, make_interval(secs => $4)) g`,
		area, from, from.Add(time.Duration(n-1)*step), step.Seconds(), price, h.Now(), state)
}

func TestSpotPriceSummary_TheAverageIsTimeWeightedAcrossTheResolutionChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "energy:prices-view")

	// The week of Monday 29 September 2025: two days of hours at 1 NOK,
	// then 1 October, the first quarter-hourly day, at 2 NOK. Weighted by
	// time: (48 × 1 + 24 × 2) / 72 = 1.333333; a plain mean of the 144
	// intervals would be (48 + 96 × 2) / 144 = 1.666667.
	monday := oslo(t, 2025, time.September, 29, 0)
	insertSpotPriceRun(t, h, "NO1", monday, time.Hour, 48, 1, "final")
	insertSpotPriceRun(t, h, "NO1", oslo(t, 2025, time.October, 1, 0), 15*time.Minute, 96, 2, "final")

	got := spotPriceSummaryOf(t, c, "NO1", "week", monday, monday.AddDate(0, 0, 7))
	if len(got.Buckets) != 1 {
		t.Fatalf("buckets = %+v, want one week", got.Buckets)
	}
	b := got.Buckets[0]
	if !b.Start.Equal(monday) || !b.End.Equal(oslo(t, 2025, time.October, 6, 0)) {
		t.Errorf("week = [%s, %s), want [%s, %s)", b.Start, b.End, monday, oslo(t, 2025, time.October, 6, 0))
	}
	if b.AvgNokPerKwh != 1.333333 || b.MinNokPerKwh != 1 || b.MaxNokPerKwh != 2 || b.CoveredMinutes != 72*60 || b.Preliminary {
		t.Errorf("week = %+v, want average 1.333333, 1 to 2, 4 320 minutes, final", b)
	}

	// The month of September 2025 to October 2025: the same weighting in
	// each, and October's quarter-hours alone average 2.
	months := spotPriceSummaryOf(t, c, "NO1", "month", oslo(t, 2025, time.September, 1, 0), oslo(t, 2025, time.November, 1, 0))
	if len(months.Buckets) != 2 || months.Buckets[0].AvgNokPerKwh != 1 || months.Buckets[1].AvgNokPerKwh != 2 ||
		months.Buckets[0].CoveredMinutes != 48*60 || months.Buckets[1].CoveredMinutes != 24*60 {
		t.Errorf("months = %+v, want September at 1 over 2 880 minutes and October at 2 over 1 440", months.Buckets)
	}
}

func TestSpotPriceSummary_ADSTDayIsTwentyThreeOrTwentyFiveHours(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "energy:prices-view")

	// Hours from 28 March 2026 to 31 March local (the clocks go forward on
	// the 29th) and from 24 to 27 October local (back on the 25th).
	spring, autumn := oslo(t, 2026, time.March, 28, 0), oslo(t, 2026, time.October, 24, 0)
	insertSpotPriceRun(t, h, "NO3", spring, time.Hour, int(oslo(t, 2026, time.March, 31, 0).Sub(spring)/time.Hour), 0.5, "final")
	insertSpotPriceRun(t, h, "NO3", autumn, time.Hour, int(oslo(t, 2026, time.October, 27, 0).Sub(autumn)/time.Hour), 0.5, "final")

	for _, tc := range []struct {
		name    string
		from    time.Time
		minutes []int
	}{
		{"spring", spring, []int{24 * 60, 23 * 60, 24 * 60}},
		{"autumn", autumn, []int{24 * 60, 25 * 60, 24 * 60}},
	} {
		got := spotPriceSummaryOf(t, c, "NO3", "day", tc.from, tc.from.AddDate(0, 0, 3))
		if len(got.Buckets) != 3 {
			t.Fatalf("%s: buckets = %+v, want three days", tc.name, got.Buckets)
		}
		for i, b := range got.Buckets {
			start, end := tc.from.AddDate(0, 0, i), tc.from.AddDate(0, 0, i+1)
			if !b.Start.Equal(start) || !b.End.Equal(end) || b.CoveredMinutes != tc.minutes[i] || int(b.End.Sub(b.Start)/time.Minute) != tc.minutes[i] {
				t.Errorf("%s day %d = [%s, %s) over %d minutes, want [%s, %s) over %d", tc.name, i, b.Start, b.End, b.CoveredMinutes, start, end, tc.minutes[i])
			}
		}
	}
}

func TestSpotPriceSummary_TheAutumnsRepeatedHourIsTwoHourBuckets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "energy:prices-view")

	// 25 October 2026 from local midnight (22:00Z the day before) to 04:00
	// local (03:00Z), in quarter-hours: five hours, 02:00 twice — at 1 NOK
	// in summer time (00:00Z) and at 3 NOK in winter time (01:00Z).
	from := oslo(t, 2026, time.October, 25, 0)
	insertSpotPriceRun(t, h, "NO1", from, 15*time.Minute, 8, 0.5, "final")
	insertSpotPriceRun(t, h, "NO1", utc(2026, time.October, 25, 0), 15*time.Minute, 4, 1, "final")
	insertSpotPriceRun(t, h, "NO1", utc(2026, time.October, 25, 1), 15*time.Minute, 4, 3, "final")
	insertSpotPriceRun(t, h, "NO1", utc(2026, time.October, 25, 2), 15*time.Minute, 4, 0.5, "final")

	got := spotPriceSummaryOf(t, c, "NO1", "hour", from, oslo(t, 2026, time.October, 25, 4))
	if len(got.Buckets) != 5 {
		t.Fatalf("buckets = %+v, want five hours", got.Buckets)
	}
	wantAvg := []float64{0.5, 0.5, 1, 3, 0.5}
	for i, b := range got.Buckets {
		start := utc(2026, time.October, 24, 22).Add(time.Duration(i) * time.Hour)
		if !b.Start.Equal(start) || !b.End.Equal(start.Add(time.Hour)) || b.AvgNokPerKwh != wantAvg[i] || b.CoveredMinutes != 60 {
			t.Errorf("hour %d = %+v, want [%s, +1h) at %v over 60 minutes", i, b, start, wantAvg[i])
		}
	}

	// An hour is written in Oslo, as a day is: the two 02:00 hours by their
	// offsets, not as 00:00Z and 01:00Z.
	var raw struct {
		Buckets []struct{ Start, End string } `json:"buckets"`
	}
	getSpotPriceSummary(c, "NO1", "hour", from, oslo(t, 2026, time.October, 25, 4)).JSON(&raw)
	wantStart := []string{"2026-10-25T00:00:00+02:00", "2026-10-25T01:00:00+02:00", "2026-10-25T02:00:00+02:00", "2026-10-25T02:00:00+01:00", "2026-10-25T03:00:00+01:00"}
	wantEnd := []string{"2026-10-25T01:00:00+02:00", "2026-10-25T02:00:00+02:00", "2026-10-25T02:00:00+01:00", "2026-10-25T03:00:00+01:00", "2026-10-25T04:00:00+01:00"}
	for i, b := range raw.Buckets {
		if i < len(wantStart) && (b.Start != wantStart[i] || b.End != wantEnd[i]) {
			t.Errorf("hour %d is written %s to %s, want %s to %s", i, b.Start, b.End, wantStart[i], wantEnd[i])
		}
	}
}

func TestSpotPriceSummary_AMonthStartsAtTheFirstsLocalMidnight(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "energy:prices-view")

	// 31 January 2026 at 23:00 local (22:00Z) is January's; 1 February at
	// 00:00 local (31 January 23:00Z) is February's, though the same UTC day.
	insertSpotPriceRun(t, h, "NO5", utc(2026, time.January, 31, 22), time.Hour, 1, 1, "final")
	insertSpotPriceRun(t, h, "NO5", utc(2026, time.January, 31, 23), time.Hour, 1, 3, "final")
	// 1 October 2026 at 00:00 local is 30 September 22:00Z, in summer time;
	// October ends at 1 November 00:00 local, 31 October 23:00Z, in winter.
	insertSpotPriceRun(t, h, "NO5", utc(2026, time.September, 30, 22), time.Hour, 1, 5, "final")

	got := spotPriceSummaryOf(t, c, "NO5", "month", oslo(t, 2026, time.January, 1, 0), oslo(t, 2026, time.November, 1, 0))
	want := []struct {
		start, end time.Time
		avg        float64
	}{
		{utc(2025, time.December, 31, 23), utc(2026, time.January, 31, 23), 1},
		{utc(2026, time.January, 31, 23), utc(2026, time.February, 28, 23), 3},
		{utc(2026, time.September, 30, 22), utc(2026, time.October, 31, 23), 5},
	}
	if len(got.Buckets) != len(want) {
		t.Fatalf("buckets = %+v, want January, February and October", got.Buckets)
	}
	for i, b := range got.Buckets {
		if !b.Start.Equal(want[i].start) || !b.End.Equal(want[i].end) || b.AvgNokPerKwh != want[i].avg || b.CoveredMinutes != 60 {
			t.Errorf("month %d = %+v, want [%s, %s) at %v", i, b, want[i].start, want[i].end, want[i].avg)
		}
	}
}

func TestSpotPriceSummary_AMissingDayIsAbsentAndShortensTheCoverage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "energy:prices-view")

	// Monday 2 March 2026 whole and final; Tuesday missing; Wednesday's
	// first twelve hours, the last of them preliminary.
	monday := oslo(t, 2026, time.March, 2, 0)
	insertSpotPriceRun(t, h, "NO2", monday, time.Hour, 24, 0.4, "final")
	wednesday := oslo(t, 2026, time.March, 4, 0)
	insertSpotPriceRun(t, h, "NO2", wednesday, time.Hour, 11, 0.6, "final")
	insertSpotPriceRun(t, h, "NO2", wednesday.Add(11*time.Hour), time.Hour, 1, 0.9, "preliminary")

	days := spotPriceSummaryOf(t, c, "NO2", "day", monday, monday.AddDate(0, 0, 7))
	if len(days.Buckets) != 2 {
		t.Fatalf("days = %+v, want Monday and Wednesday only", days.Buckets)
	}
	if b := days.Buckets[0]; !b.Start.Equal(monday) || b.CoveredMinutes != 1440 || b.Preliminary || b.AvgNokPerKwh != 0.4 {
		t.Errorf("Monday = %+v, want 1 440 minutes, final, at 0.4", b)
	}
	if b := days.Buckets[1]; !b.Start.Equal(wednesday) || b.CoveredMinutes != 720 || !b.Preliminary ||
		b.AvgNokPerKwh != 0.625 || b.MinNokPerKwh != 0.6 || b.MaxNokPerKwh != 0.9 {
		t.Errorf("Wednesday = %+v, want 720 minutes, preliminary, 0.6 to 0.9 at 0.625", b)
	}

	week := spotPriceSummaryOf(t, c, "NO2", "week", monday, monday.AddDate(0, 0, 7))
	if len(week.Buckets) != 1 || week.Buckets[0].CoveredMinutes != 36*60 || !week.Buckets[0].Preliminary {
		t.Errorf("week = %+v, want 2 160 minutes and preliminary", week.Buckets)
	}
	// Another area holds nothing: no bucket, not zeros.
	if other := spotPriceSummaryOf(t, c, "NO4", "day", monday, monday.AddDate(0, 0, 7)); len(other.Buckets) != 0 {
		t.Errorf("NO4 answered %+v", other.Buckets)
	}
}

func TestSpotPriceSummary_TheReadRefusesABadAreaBucketOrWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "energy:prices-view")
	jan := oslo(t, 2026, time.January, 1, 0)     // a Thursday
	monday := oslo(t, 2026, time.January, 5, 0)  // a Monday
	october := oslo(t, 2026, time.October, 1, 0) // in summer time
	y2000 := oslo(t, 2000, time.January, 1, 0)   // the first of a month
	march := oslo(t, 2026, time.March, 2, 0)     // a Monday, in winter time

	for _, tc := range []struct {
		name, area, bucket string
		from, to           time.Time
		fields             []string
	}{
		{"a lower-case area", "no1", "day", jan, jan.AddDate(0, 0, 1), []string{"area"}},
		{"an area without digits", "NOX", "day", jan, jan.AddDate(0, 0, 1), []string{"area"}},
		{"a bucket of a year", "NO1", "year", jan, jan.AddDate(1, 0, 0), []string{"bucket"}},
		{"an empty bucket", "NO1", "", jan, jan.AddDate(0, 0, 1), []string{"bucket"}},
		{"to equal to from", "NO1", "day", jan, jan, []string{"to"}},
		{"to before from", "NO1", "day", jan, jan.AddDate(0, 0, -1), []string{"to"}},
		{"an hour from off the hour", "NO1", "hour", jan.Add(30 * time.Minute), jan.Add(2 * time.Hour), []string{"from"}},
		{"an hour to off the hour", "NO1", "hour", jan, jan.Add(90 * time.Minute), []string{"to"}},
		{"a day from at 01:00", "NO1", "day", jan.Add(time.Hour), jan.AddDate(0, 0, 2), []string{"from"}},
		{"a day from at UTC midnight", "NO1", "day", utc(2026, time.January, 1, 0), jan.AddDate(0, 0, 2), []string{"from"}},
		{"a day to at noon", "NO1", "day", jan, jan.Add(36 * time.Hour), []string{"to"}},
		{"a week from on a Thursday", "NO1", "week", jan, monday.AddDate(0, 0, 7), []string{"from"}},
		{"a week to on a Tuesday", "NO1", "week", monday, monday.AddDate(0, 0, 8), []string{"to"}},
		{"a month from on the 5th", "NO1", "month", monday, oslo(t, 2026, time.March, 1, 0), []string{"from"}},
		{"a month to on the 2nd", "NO1", "month", jan, march, []string{"to"}},
		{"everything at once", "no1", "fortnight", jan, jan.AddDate(0, 0, -1), []string{"area", "bucket", "to"}},
		{"from and to off the day", "NO1", "day", jan.Add(time.Hour), jan.Add(25 * time.Hour), []string{"from", "to"}},
		{"hours past 35 days", "NO1", "hour", jan, jan.AddDate(0, 0, 35).Add(time.Hour), []string{"to"}},
		{"days past 400", "NO1", "day", jan, jan.AddDate(0, 0, 401), []string{"to"}},
		{"weeks past 1 830 days", "NO1", "week", monday, monday.AddDate(0, 0, 1834), []string{"to"}},
		{"months past 10 980 days", "NO1", "month", y2000, y2000.AddDate(0, 361, 0), []string{"to"}},
	} {
		r := getSpotPriceSummary(c, tc.area, tc.bucket, tc.from, tc.to)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", tc.name, r.Status, r.Body)
			continue
		}
		var problem struct {
			Errors map[string][]string `json:"errors"`
		}
		r.JSON(&problem)
		if len(problem.Errors) != len(tc.fields) {
			t.Errorf("%s: errors %v, want one on each of %v", tc.name, problem.Errors, tc.fields)
			continue
		}
		for _, f := range tc.fields {
			if len(problem.Errors[f]) != 1 {
				t.Errorf("%s: errors %v, want one on %s", tc.name, problem.Errors, f)
			}
		}
	}

	// Each limit at its edge is answered, in civil days: 35 days of hours
	// from 1 October local reach 5 November across the autumn change, 35
	// days and an hour of the clock; 400 days from 1 October 2026 reach 5
	// November 2027, likewise an hour over. 1 827 days is the last Monday
	// within 1 830; 360 months from 2000 are 10 958 days.
	for _, tc := range []struct {
		bucket   string
		from, to time.Time
	}{
		{"hour", october, october.AddDate(0, 0, 35)},
		{"hour", march, march.AddDate(0, 0, 35)},
		{"day", october, october.AddDate(0, 0, 400)},
		{"week", monday, monday.AddDate(0, 0, 1827)},
		{"month", y2000, y2000.AddDate(30, 0, 0)},
	} {
		if got := spotPriceSummaryOf(t, c, "NO1", tc.bucket, tc.from, tc.to); len(got.Buckets) != 0 {
			t.Errorf("an empty installation answered %d %s buckets", len(got.Buckets), tc.bucket)
		}
	}

	// The read needs energy:prices-view.
	if r := getSpotPriceSummary(h.SignIn(t, "energy:prices-manage"), "NO1", "day", jan, jan.AddDate(0, 0, 1)); r.Status != http.StatusForbidden {
		t.Errorf("without prices-view: status %d, want 403", r.Status)
	}
}
