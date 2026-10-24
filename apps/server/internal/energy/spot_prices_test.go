package energy_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the two reads of spot prices (energy consumption billing
// design D2): the intervals of an area over a window, and the coverage.

type spotPriceJSON struct {
	ID           int64     `json:"id"`
	PriceArea    string    `json:"priceArea"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	NokPerKwh    float64   `json:"nokPerKwh"`
	EurPerKwh    *float64  `json:"eurPerKwh"`
	ExchangeRate *float64  `json:"exchangeRate"`
	Source       string    `json:"source"`
}

type spotPriceCoverageJSON struct {
	Areas []struct {
		PriceArea         string     `json:"priceArea"`
		FirstStart        *time.Time `json:"firstStart"`
		LastEnd           *time.Time `json:"lastEnd"`
		IntervalCount     int        `json:"intervalCount"`
		DaysMissingLast30 int        `json:"daysMissingLast30"`
		LastFetch         *struct {
			Day           string     `json:"day"`
			Status        string     `json:"status"`
			Attempts      int        `json:"attempts"`
			Error         *string    `json:"error"`
			AttemptedAt   *time.Time `json:"attemptedAt"`
			NextAttemptAt *time.Time `json:"nextAttemptAt"`
		} `json:"lastFetch"`
	} `json:"areas"`
}

func getSpotPrices(c *modtest.Client, area string, from, to time.Time) *modtest.Response {
	q := url.Values{"area": {area}, "from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	return c.Do(http.MethodGet, "/api/v1/energy/spot-prices?"+q.Encode(), nil)
}

// spotPricesOf is GET /spot-prices, wanted 200.
func spotPricesOf(t *testing.T, c *modtest.Client, area string, from, to time.Time) []spotPriceJSON {
	t.Helper()
	r := getSpotPrices(c, area, from, to)
	if r.Status != http.StatusOK {
		t.Fatalf("list spot prices: status %d body %s", r.Status, r.Body)
	}
	var out []spotPriceJSON
	r.JSON(&out)
	return out
}

func TestSpotPrices_TheReadRefusesABadAreaOrWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "energy:prices-view")
	from := utc(2026, time.January, 1, 0)

	for _, tc := range []struct {
		name, area, field string
		to                time.Time
	}{
		{"a lower-case area", "no1", "area", from.Add(time.Hour)},
		{"an area without digits", "NOX", "area", from.Add(time.Hour)},
		{"to equal to from", "NO1", "to", from},
		{"to before from", "NO1", "to", from.Add(-time.Hour)},
		{"a window past 35 days", "NO1", "to", from.Add(35*24*time.Hour + time.Second)},
	} {
		r := getSpotPrices(c, tc.area, from, tc.to)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", tc.name, r.Status, r.Body)
			continue
		}
		var problem struct {
			Errors map[string][]string `json:"errors"`
		}
		r.JSON(&problem)
		if len(problem.Errors) != 1 || len(problem.Errors[tc.field]) != 1 {
			t.Errorf("%s: errors %v, want one on %s", tc.name, problem.Errors, tc.field)
		}
	}
	// Exactly 35 days is answered.
	if got := spotPricesOf(t, c, "NO1", from, from.Add(35*24*time.Hour)); len(got) != 0 {
		t.Errorf("an empty installation answered %d prices", len(got))
	}
	// The reads need energy:prices-view.
	if r := getSpotPrices(h.SignIn(t, "energy:prices-manage"), "NO1", from, from.Add(time.Hour)); r.Status != http.StatusForbidden {
		t.Errorf("without prices-view: status %d, want 403", r.Status)
	}
	if r := h.SignIn(t).Do(http.MethodGet, "/api/v1/energy/spot-prices/coverage", nil); r.Status != http.StatusForbidden {
		t.Errorf("coverage without prices-view: status %d, want 403", r.Status)
	}
}

func TestSpotPrices_TheReadAnswersTheIntervalsStartingInsideTheWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, spotPricePermissions...)
	importSpotPrices(t, c, spotPriceFile(t, "no1_hourly.csv"))

	// [05:00, 10:00) local: the 04:00 hour ends at from and is left out,
	// the 10:00 hour starts at to and is left out.
	from, to := utc(2026, time.January, 15, 4), utc(2026, time.January, 15, 9)
	got := spotPricesOf(t, c, "NO1", from, to)
	if len(got) != 5 {
		t.Fatalf("%d prices, want 5: %+v", len(got), got)
	}
	for i, p := range got {
		start := from.Add(time.Duration(i) * time.Hour)
		if !p.Start.Equal(start) || !p.End.Equal(start.Add(time.Hour)) || p.PriceArea != "NO1" || p.Source != "Import" || p.ID == 0 {
			t.Errorf("price %d = %+v, want NO1 from %s for an hour, from Import", i, p, start)
		}
	}
	// 05:00 local is the file's sixth row: 0,561725; 0,048846; 11,5.
	if p := got[0]; p.NokPerKwh != 0.561725 || p.EurPerKwh == nil || *p.EurPerKwh != 0.048846 || p.ExchangeRate == nil || *p.ExchangeRate != 11.5 {
		t.Errorf("05:00 = %v / %v / %v, want 0.561725 / 0.048846 / 11.5", p.NokPerKwh, p.EurPerKwh, p.ExchangeRate)
	}
	if other := spotPricesOf(t, c, "NO2", from, to); len(other) != 0 {
		t.Errorf("NO2 answered %d of NO1's prices", len(other))
	}
}

func TestSpotPrices_TheCoverageCountsTheMissingDaysInOsloAcrossADSTDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Today is Monday 26 October 2026 in Oslo, the day after the clocks
	// went back: the thirty days are 27 September to 26 October.
	h.Advance(time.Date(2026, time.October, 26, 10, 0, 0, 0, time.UTC).Sub(h.Now()))
	c := h.SignIn(t, append([]string{"energy:prices-view"}, allEnergyPermissions...)...)

	// NO1 is in use through a supplied point and has no prices.
	point := createMeteringPoint(t, c)
	if r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": h.Now().Add(-24 * time.Hour)}); r.Status != http.StatusCreated {
		t.Fatalf("create supply period: status %d body %s", r.Status, r.Body)
	}

	// NO2 holds, each a day in Oslo but not in UTC at its edge:
	//   26 Sep 23:00 local (21:00Z), before the window;
	//   27 Sep 00:00 local (26 Sep 22:00Z), the window's first day;
	//   25 Oct, the 25-hour day, whole (24 Oct 22:00Z to 25 Oct 23:00Z);
	//   27 Oct 00:00 local (26 Oct 23:00Z), tomorrow, after it.
	hours := func(from time.Time, n int) {
		h.Exec(t, `INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
SELECT 'NO2', g, g + interval '1 hour', 0.5, 'Import', $3 FROM generate_series($1::timestamptz, $2::timestamptz, interval '1 hour') g`,
			from, from.Add(time.Duration(n-1)*time.Hour), h.Now())
	}
	hours(time.Date(2026, time.September, 26, 21, 0, 0, 0, time.UTC), 2)
	hours(time.Date(2026, time.October, 24, 22, 0, 0, 0, time.UTC), 25)
	hours(time.Date(2026, time.October, 26, 23, 0, 0, 0, time.UTC), 1)
	// The feed's record of NO2: the latest attempt is the one answered.
	h.Exec(t, `INSERT INTO energy.spot_price_fetches (price_area, day, status, attempts, last_error, attempted_at, next_attempt_at)
VALUES ('NO2', '2026-10-26', 'ok', 1, NULL, $1, NULL), ('NO2', '2026-10-27', 'pending', 2, 'Not published yet', $2, $3)`,
		h.Now().Add(-2*time.Hour), h.Now().Add(-time.Hour), h.Now())

	r := c.Do(http.MethodGet, "/api/v1/energy/spot-prices/coverage", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("coverage: status %d body %s", r.Status, r.Body)
	}
	var coverage spotPriceCoverageJSON
	r.JSON(&coverage)
	if len(coverage.Areas) != 2 {
		t.Fatalf("areas = %+v, want NO1 and NO2", coverage.Areas)
	}
	no1, no2 := coverage.Areas[0], coverage.Areas[1]
	if no1.PriceArea != "NO1" || no1.FirstStart != nil || no1.LastEnd != nil || no1.IntervalCount != 0 || no1.DaysMissingLast30 != 30 || no1.LastFetch != nil {
		t.Errorf("NO1 = %+v, want no bounds, no fetch and 30 days missing", no1)
	}
	if no2.PriceArea != "NO2" || no2.IntervalCount != 28 ||
		no2.FirstStart == nil || !no2.FirstStart.Equal(time.Date(2026, time.September, 26, 21, 0, 0, 0, time.UTC)) ||
		no2.LastEnd == nil || !no2.LastEnd.Equal(time.Date(2026, time.October, 27, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("NO2 = %+v, want 28 intervals from 26 Sep 21:00Z to 27 Oct 00:00Z", no2)
	}
	// 27 September and 25 October held; the other 28 days missing.
	if no2.DaysMissingLast30 != 28 {
		t.Errorf("NO2 daysMissingLast30 = %d, want 28", no2.DaysMissingLast30)
	}
	if f := no2.LastFetch; f == nil || f.Day != "2026-10-27" || f.Status != "pending" || f.Attempts != 2 ||
		f.Error == nil || *f.Error != "Not published yet" || f.NextAttemptAt == nil || !f.NextAttemptAt.Equal(h.Now()) {
		t.Errorf("NO2 lastFetch = %+v, want the pending attempt for 27 October", f)
	}
}
