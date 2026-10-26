package energy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the spot price worker with the shared spot-price service as
// its source (spot-price service design S4–S6, plan readings 8–11), against
// an httptest server playing the service to its contract: the areas
// followed and the history rows kept, the live window's one request and
// each day's status, the history walk across cycles and its resumption
// when the floor moves, the gap read after a downtime, the active flag, the
// twenty-request budget with live windows first, the one-minute cadence
// while copying, failures — a window skipped after three permanent ones,
// never for transient ones — and the lease. The
// harness clock starts at 2026-09-12 14:00 in Oslo, so the live window is
// 2026-08-29 to 2026-09-13 and history walks back from 2026-08-28.

const syncTestKey = "sp_c3luYy10ZXN0LWtleS10aGF0LW11c3QtbmV2ZXItYmUtbG9nZ2Vk"

// fakeService is the spot-price service: /api/v1/areas answers the areas
// set, /api/v1/prices the intervals serviceIntervals writes for each Oslo
// day of the window from the area's floor on, except the days the test
// took out or changed. It records every request in order: "areas", or
// "AREA first..last" with the window's Oslo days.
type fakeService struct {
	srv *httptest.Server

	mu          sync.Mutex
	hits        []string
	areas       []serviceAreaJSON
	areasStatus int
	status      map[string]int // area → a status for its every prices request
	historyFail map[string]int // area → a status for its history requests only
	// windowStatus is a status for one window's request, by its hit.
	windowStatus map[string]int
	retryAfter   string
	absent       map[string]bool // "AREA YYYY-MM-DD"
	preliminary  map[string]bool
	withoutNok   map[string]bool
	gap          map[string]bool
	// held, when set, is the only days the service holds of an area: the
	// rest of a window is absent.
	held map[string]bool
}

type serviceAreaJSON struct {
	Code     string `json:"code"`
	Active   bool   `json:"active"`
	TimeZone string `json:"timeZone"`
	Coverage *struct {
		FirstStart        *time.Time `json:"firstStart,omitempty"`
		MissingDaysLast40 int        `json:"missingDaysLast400"`
	} `json:"coverage"`
}

func newFakeService(t *testing.T) *fakeService {
	t.Helper()
	fs := &fakeService{status: map[string]int{}, historyFail: map[string]int{}, windowStatus: map[string]int{}, absent: map[string]bool{},
		preliminary: map[string]bool{}, withoutNok: map[string]bool{}, gap: map[string]bool{}}
	fs.srv = httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *fakeService) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+syncTestKey || r.Header.Get("Accept") != "application/json" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	switch r.URL.Path {
	case "/api/v1/areas":
		fs.hits = append(fs.hits, "areas")
		if fs.areasStatus != 0 {
			w.WriteHeader(fs.areasStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(fs.areas)
	case "/api/v1/prices":
		q := r.URL.Query()
		area := q.Get("area")
		from, errFrom := time.Parse(time.RFC3339, q.Get("from"))
		to, errTo := time.Parse(time.RFC3339, q.Get("to"))
		if errFrom != nil || errTo != nil || q.Get("currency") != "EUR" || !to.After(from) || to.Sub(from) > 35*24*time.Hour {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"status":400,"detail":"the window from %s to %s is not one this service answers"}`, q.Get("from"), q.Get("to"))
			return
		}
		first, last := from.In(osloZone).Format(time.DateOnly), to.In(osloZone).AddDate(0, 0, -1).Format(time.DateOnly)
		fs.hits = append(fs.hits, area+" "+first+".."+last)
		status := fs.status[area]
		if fail := fs.historyFail[area]; fail != 0 && first != "2026-08-29" {
			status = fail
		}
		if fail := fs.windowStatus[area+" "+first+".."+last]; fail != 0 {
			status = fail
		}
		if status != 0 {
			if fs.retryAfter != "" {
				w.Header().Set("Retry-After", fs.retryAfter)
			}
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fs.intervals(area, from, to))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// setAreas makes the answer of /api/v1/areas: every code given active, its
// floor the Oslo day its coverage starts ("" for a null coverage), and the
// other zones of the contract inactive.
func (fs *fakeService) setAreas(active map[string]string, inactive ...string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.areas = nil
	codes := []string{"DK1", "DK2", "FI", "NO1", "NO2", "NO3", "NO4", "NO5", "SE1", "SE2", "SE3", "SE4"}
	for code := range active {
		if !slices.Contains(codes, code) {
			codes = append(codes, code)
		}
	}
	slices.Sort(codes)
	for _, code := range codes {
		a := serviceAreaJSON{Code: code, TimeZone: "Europe/Oslo"}
		floor, isActive := active[code]
		a.Active = isActive
		if isActive && floor != "" {
			d, _ := time.Parse(time.DateOnly, floor)
			first := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, osloZone).UTC()
			a.Coverage = &struct {
				FirstStart        *time.Time `json:"firstStart,omitempty"`
				MissingDaysLast40 int        `json:"missingDaysLast400"`
			}{FirstStart: &first}
		}
		if slices.Contains(inactive, code) {
			first := time.Date(2015, 1, 1, 0, 0, 0, 0, osloZone).UTC()
			a.Coverage = &struct {
				FirstStart        *time.Time `json:"firstStart,omitempty"`
				MissingDaysLast40 int        `json:"missingDaysLast400"`
			}{FirstStart: &first}
		}
		fs.areas = append(fs.areas, a)
	}
}

func (fs *fakeService) floor(area string) (time.Time, bool) {
	for _, a := range fs.areas {
		if a.Code == area && a.Coverage != nil && a.Coverage.FirstStart != nil {
			return *a.Coverage.FirstStart, true
		}
	}
	return time.Time{}, false
}

// intervals is what the service holds of area in [from, to): from the
// area's floor on, quarter-hours from 2025-10-01 and hours before, the NOK
// price 0.5 + i/1000 for the i-th interval of the day.
func (fs *fakeService) intervals(area string, from, to time.Time) []map[string]any {
	out := []map[string]any{}
	floor, held := fs.floor(area)
	if !held {
		return out
	}
	quarterHours := time.Date(2025, time.October, 1, 0, 0, 0, 0, osloZone)
	for day := from; day.Before(to); day = nextOsloMidnight(day) {
		key := area + " " + day.In(osloZone).Format(time.DateOnly)
		if day.Before(floor) || fs.absent[key] || (fs.held != nil && !fs.held[key]) {
			continue
		}
		step := time.Hour
		if !day.Before(quarterHours) {
			step = 15 * time.Minute
		}
		state := "final"
		if fs.preliminary[key] {
			state = "preliminary"
		}
		end := nextOsloMidnight(day)
		for i, at := 0, day; at.Before(end); i, at = i+1, at.Add(step) {
			if fs.gap[key] && i == 7 {
				continue
			}
			e := map[string]any{
				"start": at.UTC().Format(time.RFC3339), "end": at.Add(step).UTC().Format(time.RFC3339),
				"priceEurPerMwh": json.Number(fmt.Sprintf("%d.5", 40+i)), "state": state, "source": "nordpool_portal", "version": 2,
			}
			if !fs.withoutNok[key] {
				e["priceNokPerKwh"] = json.Number(fmt.Sprintf("0.%d", 5000+i))
				e["exchangeRate"] = json.Number("11.5")
				e["rateSource"] = "nordpool"
			}
			out = append(out, e)
		}
	}
	return out
}

func nextOsloMidnight(t time.Time) time.Time {
	l := t.In(osloZone)
	return time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, osloZone)
}

func (fs *fakeService) takeHits() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := fs.hits
	fs.hits = nil
	return out
}

func (fs *fakeService) set(f func(fs *fakeService)) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	f(fs)
}

// serviceHarness is an installation with the spot-price service as its
// source, served by fs.
func serviceHarness(t *testing.T, fs *fakeService, env ...string) *modtest.Harness {
	t.Helper()
	opts := []modtest.Option{
		modtest.WithTransport(fs.srv.Client().Transport),
		modtest.WithEnv("ENERGY_SPOT_PRICE_SERVICE_URL", fs.srv.URL),
		modtest.WithEnv("ENERGY_SPOT_PRICE_SERVICE_KEY", syncTestKey),
	}
	for i := 0; i+1 < len(env); i += 2 {
		opts = append(opts, modtest.WithEnv(env[i], env[i+1]))
	}
	return newHarness(t, opts...)
}

const liveWindow = "2026-08-29..2026-09-13"

type historyRecord struct {
	Cursor, Floor *string
	LiveThrough   *string
	Active        bool
	DaysSkipped   int32
	Attempts      int32
	LastError     *string
	NextAttempt   *time.Time
}

func historyOf(t *testing.T, h *modtest.Harness, area string) historyRecord {
	t.Helper()
	var r historyRecord
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT cursor_day::text, floor_day::text, live_through::text, active, days_skipped, attempts, last_error, next_attempt_at
		   FROM energy.spot_price_history WHERE price_area = $1`, area).
		Scan(&r.Cursor, &r.Floor, &r.LiveThrough, &r.Active, &r.DaysSkipped, &r.Attempts, &r.LastError, &r.NextAttempt); err != nil {
		t.Fatalf("the history row of %s: %v", area, err)
	}
	return r
}

func text(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// importHours stores area's day in hours from a file.
func importHours(t *testing.T, h *modtest.Harness, area, day, source string) {
	t.Helper()
	h.Exec(t, `INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
	           SELECT $1, s, s + interval '1 hour', 0.5, $3, now()
	             FROM generate_series(($2::date)::timestamp AT TIME ZONE 'Europe/Oslo',
	                                  (($2::date + 1)::timestamp AT TIME ZONE 'Europe/Oslo') - interval '1 hour', interval '1 hour') s`,
		area, day, source)
}

func dayCount(t *testing.T, h *modtest.Harness, area, day, source string) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = $1 AND source = $3
	                    AND "start" >= ($2::date)::timestamp AT TIME ZONE 'Europe/Oslo'
	                    AND "start" < ($2::date + 1)::timestamp AT TIME ZONE 'Europe/Oslo'`, area, day, source)
}

func TestSpotPriceService_RecordsEachDayOfTheLiveWindow(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-08-29"}) // no history before the live window
	fs.absent["NO1 2026-09-01"] = true
	fs.withoutNok["NO1 2026-09-02"] = true
	fs.gap["NO1 2026-09-03"] = true
	fs.preliminary["NO1 2026-09-13"] = true
	h := serviceHarness(t, fs)
	importHours(t, h, "NO1", "2026-09-05", "Import")
	importHours(t, h, "NO1", "2026-09-06", "Feed")
	w := energy.NewSpotPriceWorker(h.Deps())
	start := h.Now()

	if n := runSpotPriceCycle(t, w); n != 2 {
		t.Errorf("the first cycle made %d requests, want the areas and the one window", n)
	}
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO1 " + liveWindow}) {
		t.Errorf("asked for %v, want the areas, then the live window in one request", hits)
	}
	ok := fetchRecordOf(t, h, "NO1", "2026-09-12")
	if ok.Status != "ok" || ok.Attempts != 1 || !sameInstant(ok.FetchedAt, start) || ok.NextAttempt != nil || dayCount(t, h, "NO1", "2026-09-12", "Service") != 96 {
		t.Errorf("today, final = %+v, want ok with its 96 quarter-hours", ok)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO1' AND source = 'Service' AND state = 'final'
	                     AND upstream = 'nordpool_portal' AND rate_source = 'nordpool' AND upstream_version = 2 AND eur_per_kwh = 0.0405`); n != 11 {
		t.Errorf("%d final intervals carry the provenance and 40.5 EUR/MWh as 0.0405, want the first of each of the 11 final days written", n)
	}
	tomorrow := fetchRecordOf(t, h, "NO1", "2026-09-13")
	if tomorrow.Status != "pending" || !sameInstant(tomorrow.NextAttempt, start.Add(time.Hour)) ||
		h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO1' AND state = 'preliminary'`) != 96 {
		t.Errorf("tomorrow, preliminary = %+v, want written and pending, asked again in an hour", tomorrow)
	}
	for day, reason := range map[string]string{
		"2026-09-01": "the spot-price service holds no prices for the day",
		"2026-09-02": "the spot-price service holds no NOK price for the day",
		"2026-09-03": "the day's intervals are not contiguous",
	} {
		r := fetchRecordOf(t, h, "NO1", day)
		if r.Status != "missing" || text(r.LastError) != reason || !sameInstant(r.NextAttempt, start.Add(24*time.Hour)) {
			t.Errorf("%s = %+v (%s), want missing with %q, asked again in a day", day, r, text(r.LastError), reason)
		}
		if n := dayCount(t, h, "NO1", day, "Service"); n != 0 {
			t.Errorf("%s: %d intervals written, want none", day, n)
		}
	}
	refused := fetchRecordOf(t, h, "NO1", "2026-09-05")
	if refused.Status != "failed" || text(refused.LastError) != "the day's intervals overlap stored prices of another length" ||
		!sameInstant(refused.NextAttempt, start.Add(15*time.Minute)) || dayCount(t, h, "NO1", "2026-09-05", "Import") != 24 {
		t.Errorf("over an imported day = %+v, want failed with the overlap's sentence and the file's hours kept", refused)
	}
	if feed, service := dayCount(t, h, "NO1", "2026-09-06", "Feed"), dayCount(t, h, "NO1", "2026-09-06", "Service"); feed != 0 || service != 96 ||
		fetchRecordOf(t, h, "NO1", "2026-09-06").Status != "ok" {
		t.Errorf("over the feed's hours: %d Feed, %d Service intervals, want the day replaced by 96 quarter-hours", feed, service)
	}
	if hr := historyOf(t, h, "NO1"); text(hr.Floor) != "2026-08-29" || text(hr.Cursor) != "2026-08-29" {
		t.Errorf("the history = %+v, want complete at the live window's first day", hr)
	}

	// Nothing is due a minute later: only the areas are asked.
	h.Advance(time.Minute)
	if n := runSpotPriceCycle(t, w); n != 1 || !slices.Equal(fs.takeHits(), []string{"areas"}) {
		t.Errorf("a cycle with nothing due made %d requests, want the areas alone", n)
	}
	// An hour on, tomorrow is due; it has turned final meanwhile.
	fs.set(func(fs *fakeService) { delete(fs.preliminary, "NO1 2026-09-13") })
	h.Advance(time.Hour)
	if n := runSpotPriceCycle(t, w); n != 2 || !slices.Equal(fs.takeHits(), []string{"areas", "NO1 " + liveWindow}) {
		t.Errorf("an hour on the cycle made %d requests, want the window read again", n)
	}
	if r := fetchRecordOf(t, h, "NO1", "2026-09-13"); r.Status != "ok" || r.NextAttempt != nil || dayCount(t, h, "NO1", "2026-09-13", "Service") != 96 ||
		h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO1' AND state = 'preliminary'`) != 0 {
		t.Errorf("tomorrow turned final = %+v, want ok and every interval final", r)
	}
	// A missing day is not asked again before its day has passed, however
	// often the window is read.
	if r := fetchRecordOf(t, h, "NO1", "2026-09-01"); r.Attempts != 1 {
		t.Errorf("the missing day after a second read = %+v, want its one attempt", r)
	}
	if strings.Contains(h.Logs(), syncTestKey) {
		t.Error("the log carries the key")
	}
}

func TestSpotPriceService_PlansTheLiveWindow(t *testing.T) {
	t.Parallel()
	now := modtest.Start // 2026-09-12 14:00 in Oslo
	all := windowDays("2026-08-29", "2026-09-13")
	rows := func(status func(day string) (string, *time.Time)) []store.EnergySpotPriceFetch {
		var out []store.EnergySpotPriceFetch
		for _, d := range all {
			s, next := status(d)
			day, _ := time.Parse(time.DateOnly, d)
			out = append(out, store.EnergySpotPriceFetch{PriceArea: "NO1", Day: pgtype.Date{Time: day, Valid: true}, Status: s, NextAttemptAt: next})
		}
		return out
	}
	allOK := rows(func(string) (string, *time.Time) { return "ok", nil })
	cases := []struct {
		name     string
		fetches  []store.EnergySpotPriceFetch
		lastRead *time.Time
		wantDue  []string
		window   bool
	}{
		{name: "never read: every day due", wantDue: all, window: true},
		{name: "every day ok, read an hour ago: nothing", fetches: allOK, lastRead: at(now.Add(-time.Hour))},
		{name: "every day ok, read more than a day ago: the window, no day", fetches: allOK, lastRead: at(now.Add(-25 * time.Hour)), window: true},
		{name: "a missing day given up is never due", lastRead: at(now.Add(-time.Hour)), fetches: rows(func(d string) (string, *time.Time) {
			if d == "2026-09-01" {
				return "missing", nil
			}
			return "ok", nil
		})},
		{name: "a pending day whose hour has come", lastRead: at(now.Add(-time.Hour)), wantDue: []string{"2026-09-13"}, window: true,
			fetches: rows(func(d string) (string, *time.Time) {
				if d == "2026-09-13" {
					return "pending", at(now.Add(-time.Minute))
				}
				if d == "2026-09-12" {
					return "pending", at(now.Add(time.Minute))
				}
				return "ok", nil
			})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			days, window := energy.PlanSpotServiceWindowForTest(now, tc.fetches, tc.lastRead)
			var due, got []string
			for _, d := range days {
				got = append(got, d.Day)
				if d.Due {
					due = append(due, d.Day)
				}
			}
			if !slices.Equal(got, all) {
				t.Errorf("the window = %v, want today − 14 to tomorrow", got)
			}
			if !slices.Equal(due, tc.wantDue) || window != tc.window {
				t.Errorf("due %v (window %v), want %v (window %v)", due, window, tc.wantDue, tc.window)
			}
		})
	}
}

func TestSpotPriceService_WalksHistoryBackToTheFloorAndResumesWhenItMoves(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-06-01"})
	fs.absent["NO1 2026-07-01"] = true
	h := serviceHarness(t, fs)
	importHours(t, h, "NO1", "2026-07-10", "Import") // hours under the service's quarter-hours: skipped
	importHours(t, h, "NO1", "2026-07-11", "Feed")   // the feed's hours: replaced
	w := energy.NewSpotPriceWorker(h.Deps())

	if n := runSpotPriceCycle(t, w); n != 5 {
		t.Errorf("the cycle made %d requests, want the areas, the live window and three history windows", n)
	}
	if hits := fs.takeHits(); !slices.Equal(hits, []string{
		"areas", "NO1 " + liveWindow, "NO1 2026-07-25..2026-08-28", "NO1 2026-06-20..2026-07-24", "NO1 2026-06-01..2026-06-19",
	}) {
		t.Errorf("asked for %v, want 35 days a window back to the floor", hits)
	}
	hr := historyOf(t, h, "NO1")
	if text(hr.Cursor) != "2026-06-01" || text(hr.Floor) != "2026-06-01" || hr.DaysSkipped != 2 || hr.Attempts != 0 || hr.LastError != nil {
		t.Errorf("the walk = %+v, want complete at the floor with the absent and the imported day skipped", hr)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO1' AND source = 'Service'
	                     AND "start" >= '2026-06-01T00:00:00+02:00' AND "start" < '2026-08-29T00:00:00+02:00'`); n != (89-2)*96 {
		t.Errorf("%d history quarter-hours written, want 87 days of 96", n)
	}
	if dayCount(t, h, "NO1", "2026-07-10", "Import") != 24 || dayCount(t, h, "NO1", "2026-07-11", "Feed") != 0 {
		t.Error("the imported day was overwritten or the feed's day kept")
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_price_fetches WHERE day < '2026-08-29'`); n != 0 {
		t.Errorf("%d fetch rows recorded for history days, want none", n)
	}
	if got := energy.SpotPriceNextWaitForTest(w); got != 15*time.Minute {
		t.Errorf("with the walk complete the next cycle is in %v, want the poll", got)
	}

	// The operator backfills the service further: the walk resumes from its
	// cursor to the new floor.
	fs.set(func(fs *fakeService) {})
	fs.setAreas(map[string]string{"NO1": "2026-05-01"})
	h.Advance(time.Minute)
	if n := runSpotPriceCycle(t, w); n != 2 {
		t.Errorf("after the floor moved the cycle made %d requests, want the areas and one window", n)
	}
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO1 2026-05-01..2026-05-31"}) {
		t.Errorf("asked for %v, want the month the floor moved", hits)
	}
	if hr := historyOf(t, h, "NO1"); text(hr.Cursor) != "2026-05-01" || text(hr.Floor) != "2026-05-01" || hr.DaysSkipped != 2 {
		t.Errorf("the walk = %+v, want complete at the new floor", hr)
	}
}

func TestSpotPriceService_CopiesHourlyHistoryAcrossTheClockChangesAndTheResolutionChange(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO2": "2025-09-01"})
	// The service holds the days that matter here, so the walk of a year
	// writes four days rather than 362.
	fs.held = map[string]bool{"NO2 2025-09-30": true, "NO2 2025-10-01": true, "NO2 2025-10-26": true, "NO2 2026-03-29": true}
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	runSpotPriceCycle(t, w)
	hits := fs.takeHits()
	// A window across the autumn change is a day shorter: 35 days of it are
	// 841 hours, more than the service answers. The one across the spring
	// change is not.
	if !slices.Contains(hits, "NO2 2025-10-19..2025-11-21") || !slices.Contains(hits, "NO2 2026-03-07..2026-04-10") {
		t.Errorf("asked for %v, want the window across 2025-10-26 one day short", hits)
	}
	hr := historyOf(t, h, "NO2")
	if text(hr.Cursor) != "2025-09-01" || hr.DaysSkipped != 362-4 || hr.LastError != nil {
		t.Fatalf("the walk = %+v, want complete with every day but the four held skipped", hr)
	}
	for day, want := range map[string]int{"2025-09-30": 24, "2025-10-01": 96, "2025-10-26": 100, "2026-03-29": 92} {
		if n := dayCount(t, h, "NO2", day, "Service"); n != want {
			t.Errorf("%s: %d intervals, want %d", day, n, want)
		}
	}
}

func TestSpotPriceService_SpendsTwentyRequestsACycleLiveWindowsFirst(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2015-01-01", "NO2": "2015-01-01", "NO3": "2015-01-01", "NO4": "2015-01-01", "NO5": "2015-01-01"})
	fs.held = map[string]bool{} // the requests are what is counted, not the writes
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	if n := runSpotPriceCycle(t, w); n != 20 {
		t.Errorf("the first cycle made %d requests, want 20", n)
	}
	hits := fs.takeHits()
	if len(hits) != 20 || !slices.Equal(hits[:6], []string{"areas", "NO1 " + liveWindow, "NO2 " + liveWindow, "NO3 " + liveWindow, "NO4 " + liveWindow, "NO5 " + liveWindow}) {
		t.Fatalf("asked for %v, want the areas, then every live window, then history", hits)
	}
	for _, hit := range hits[6:] {
		if !strings.HasPrefix(hit, "NO1 ") {
			t.Errorf("history asked %s, want NO1's walk first", hit)
		}
	}
	first := historyOf(t, h, "NO1")
	if first.Cursor == nil || historyOf(t, h, "NO2").Cursor != nil {
		t.Errorf("after one cycle NO1 = %+v and NO2 = %+v, want NO1's walk begun and NO2's not", first, historyOf(t, h, "NO2"))
	}
	if got := energy.SpotPriceNextWaitForTest(w); got != time.Minute {
		t.Errorf("while history copies the next cycle is in %v, want a minute", got)
	}

	h.Advance(time.Minute)
	if n := runSpotPriceCycle(t, w); n != 20 {
		t.Errorf("the second cycle made %d requests, want 20", n)
	}
	if hits := fs.takeHits(); hits[0] != "areas" || slices.Contains(hits, "NO1 "+liveWindow) {
		t.Errorf("the second cycle asked for %v, want no live window again", hits[:2])
	}
	if next := historyOf(t, h, "NO1"); text(next.Cursor) >= text(first.Cursor) {
		t.Errorf("NO1's cursor went from %s to %s, want it further back", text(first.Cursor), text(next.Cursor))
	}
}

// TestSpotPriceService_FollowsTheActiveAreasOnly pins design S4: with
// /areas answered, only the areas the service is active for are synced — an
// area in use it does not follow gets no request and no fetch rows — and a
// code outside the contract's zones is ignored.
func TestSpotPriceService_FollowsTheActiveAreasOnly(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-08-29", "SE3": "2026-08-29", "NO9": "2026-08-29"}, "NO2")
	h := serviceHarness(t, fs)
	priceAreaInUse(t, h, "NO3") // in use, not active in the service
	w := energy.NewSpotPriceWorker(h.Deps())
	runSpotPriceCycle(t, w)
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO1 " + liveWindow, "SE3 " + liveWindow}) {
		t.Errorf("asked for %v, want the live windows of NO1 and SE3 only: active, and a zone of the contract", hits)
	}
	var fetchRows int
	if err := h.Pool().QueryRow(context.Background(), `SELECT count(*) FROM energy.spot_price_fetches WHERE price_area = 'NO3'`).Scan(&fetchRows); err != nil || fetchRows != 0 {
		t.Errorf("NO3, in use but not followed: %d fetch rows (err %v), want none", fetchRows, err)
	}
	var areas []string
	rows, err := h.Pool().Query(context.Background(), `SELECT price_area FROM energy.spot_price_history ORDER BY price_area`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		areas = append(areas, a)
	}
	if !slices.Equal(areas, []string{"NO1", "SE3"}) {
		t.Errorf("history rows for %v, want only the active areas NO1 and SE3", areas)
	}
}

func TestSpotPriceService_AnAreasFailureLeavesTheWalksAloneAndStillReadsTheAreasInUse(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-06-01", "NO3": "2026-06-01"})
	fs.areasStatus = http.StatusInternalServerError
	h := serviceHarness(t, fs)
	h.Exec(t, `INSERT INTO energy.spot_price_history (price_area, cursor_day, floor_day, updated_at) VALUES ('NO1', '2026-07-01', '2026-06-01', now())`)
	priceAreaInUse(t, h, "NO3")
	w := energy.NewSpotPriceWorker(h.Deps())
	if n := runSpotPriceCycle(t, w); n != 2 {
		t.Errorf("the cycle made %d requests, want the areas and NO3's live window", n)
	}
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO3 " + liveWindow}) {
		t.Errorf("asked for %v, want the areas, then only the area in use", hits)
	}
	// The rows' attempts, error and backoff are their windows' alone: the
	// failure is logged, and nothing walks while the areas are unknown.
	hr := historyOf(t, h, "NO1")
	if text(hr.Cursor) != "2026-07-01" || hr.Attempts != 0 || hr.LastError != nil || hr.NextAttempt != nil {
		t.Errorf("NO1's walk = %+v (%s), want it untouched, its cursor kept", hr, text(hr.LastError))
	}
	if !strings.Contains(h.Logs(), "the spot-price service answered 500 Internal Server Error") {
		t.Error("the areas' failure was not logged")
	}
	if got := energy.SpotPriceNextWaitForTest(w); got != 15*time.Minute {
		t.Errorf("after the failure the next cycle is in %v, want the poll", got)
	}
}

func TestSpotPriceService_AFailedWindowFailsEveryDueDayAndARateLimitEndsTheCycle(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-08-29", "NO2": "2026-08-29"})
	fs.status["NO1"] = http.StatusUnauthorized
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	runSpotPriceCycle(t, w)
	fs.takeHits()
	for _, day := range windowDays("2026-08-29", "2026-09-13") {
		if r := fetchRecordOf(t, h, "NO1", day); r.Status != "failed" || text(r.LastError) != "the spot-price service refused the key" ||
			!sameInstant(r.NextAttempt, h.Now().Add(15*time.Minute)) || r.FetchedAt != nil {
			t.Errorf("%s = %+v, want failed with the refused key, asked again in 15 minutes", day, r)
		}
	}
	if r := fetchRecordOf(t, h, "NO2", "2026-09-12"); r.Status != "ok" {
		t.Errorf("NO2's today = %+v, want it read regardless", r)
	}

	// Fifteen minutes on the service rate limits: NO1's days wait the hour
	// it asks for, and nothing more is asked this cycle.
	// NO3 is newly active, its window due, and not asked.
	fs.set(func(fs *fakeService) {
		fs.status["NO1"] = http.StatusTooManyRequests
		fs.retryAfter = "3600"
	})
	fs.setAreas(map[string]string{"NO1": "2026-08-29", "NO2": "2026-08-29", "NO3": "2026-08-29"})
	h.Advance(15 * time.Minute)
	if n := runSpotPriceCycle(t, w); n != 2 || !slices.Equal(fs.takeHits(), []string{"areas", "NO1 " + liveWindow}) {
		t.Errorf("the rate-limited cycle made %d requests, want the areas and NO1's window, and nothing after", n)
	}
	if r := fetchRecordOf(t, h, "NO1", "2026-09-12"); r.Status != "failed" || r.Attempts != 2 ||
		text(r.LastError) != "the spot-price service is rate limiting this installation" || !sameInstant(r.NextAttempt, h.Now().Add(time.Hour)) {
		t.Errorf("a 429 = %+v, want failed, asked again after its Retry-After hour", r)
	}
	if strings.Contains(h.Logs(), syncTestKey) {
		t.Error("the log carries the key")
	}
}

func TestSpotPriceService_AFailedHistoryWindowBacksOffAndKeepsTheCursor(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-06-01"})
	fs.historyFail["NO1"] = http.StatusBadGateway
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	if n := runSpotPriceCycle(t, w); n != 3 {
		t.Errorf("the cycle made %d requests, want the areas, the live window and one history window", n)
	}
	fs.takeHits()
	hr := historyOf(t, h, "NO1")
	if hr.Cursor != nil || hr.Attempts != 1 || text(hr.LastError) != "the spot-price service answered 502 Bad Gateway" ||
		!sameInstant(hr.NextAttempt, h.Now().Add(15*time.Minute)) {
		t.Errorf("the walk = %+v (%s), want the failure backed off with no cursor yet", hr, text(hr.LastError))
	}
	if got := energy.SpotPriceNextWaitForTest(w); got != 15*time.Minute {
		t.Errorf("while the walk backs off the next cycle is in %v, want the poll", got)
	}
	fs.set(func(fs *fakeService) { delete(fs.historyFail, "NO1") })
	h.Advance(15 * time.Minute)
	runSpotPriceCycle(t, w)
	if hr := historyOf(t, h, "NO1"); text(hr.Cursor) != "2026-06-01" || hr.Attempts != 0 || hr.LastError != nil || hr.NextAttempt != nil {
		t.Errorf("the walk after the backoff = %+v, want it complete and the failure cleared", hr)
	}
}

func TestSpotPriceService_SkipsACycleWhileAnotherReplicaHoldsTheLease(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2015-01-01"})
	fs.held = map[string]bool{}
	h := serviceHarness(t, fs)
	ctx := context.Background()
	conn, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var held bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, energy.SpotPriceLeaseKeyForTest).Scan(&held); err != nil || !held {
		t.Fatalf("take the lease: %v %v", held, err)
	}
	w := energy.NewSpotPriceWorker(h.Deps())
	if n := runSpotPriceCycle(t, w); n != 0 || len(fs.takeHits()) != 0 {
		t.Errorf("with the lease held the cycle made %d requests, want it skipped", n)
	}
	if got := energy.SpotPriceNextWaitForTest(w); got != 15*time.Minute {
		t.Errorf("a skipped cycle waits %v, want the poll", got)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, energy.SpotPriceLeaseKeyForTest); err != nil {
		t.Fatal(err)
	}
	if n := runSpotPriceCycle(t, w); n != 20 {
		t.Errorf("after the release the cycle made %d requests, want 20", n)
	}
}

func TestSpotPriceService_HistoryFromRaisesTheFloor(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2015-01-01"})
	h := serviceHarness(t, fs, "ENERGY_SPOT_PRICE_HISTORY_FROM", "2026-08-01")
	w := energy.NewSpotPriceWorker(h.Deps())
	runSpotPriceCycle(t, w)
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO1 " + liveWindow, "NO1 2026-08-01..2026-08-28"}) {
		t.Errorf("asked for %v, want history only from ENERGY_SPOT_PRICE_HISTORY_FROM", hits)
	}
	if hr := historyOf(t, h, "NO1"); text(hr.Floor) != "2026-08-01" || text(hr.Cursor) != "2026-08-01" {
		t.Errorf("the walk = %+v, want complete at the setting's day", hr)
	}
}

// serviceCoverageArea is one area of the coverage as the service sync
// shapes it.
type serviceCoverageArea struct {
	PriceArea         string `json:"priceArea"`
	FollowedByService *bool  `json:"followedByService"`
	History           *struct {
		Complete    bool    `json:"complete"`
		CursorDay   *string `json:"cursorDay"`
		FloorDay    *string `json:"floorDay"`
		DaysSkipped int32   `json:"daysSkipped"`
		Error       *string `json:"error"`
	} `json:"history"`
}

func serviceCoverageOf(t *testing.T, h *modtest.Harness, area string) serviceCoverageArea {
	t.Helper()
	r := h.SignIn(t, "energy:prices-view").Do(http.MethodGet, "/api/v1/energy/spot-prices/coverage", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("coverage: status %d body %s", r.Status, r.Body)
	}
	var coverage struct {
		Areas []serviceCoverageArea `json:"areas"`
	}
	r.JSON(&coverage)
	for _, a := range coverage.Areas {
		if a.PriceArea == area {
			return a
		}
	}
	t.Fatalf("the coverage has no %s: %s", area, r.Body)
	return serviceCoverageArea{}
}

// TestSpotPriceService_ReadsTheDaysMissedWhileTheWorkerWasAway pins the gap:
// a worker away longer than the live window reaches back reads the days
// between its last live window and today's forward, as history, before the
// live window — and a live window read while the gap is left does not move
// live_through past it.
func TestSpotPriceService_ReadsTheDaysMissedWhileTheWorkerWasAway(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-06-01"})
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	runSpotPriceCycle(t, w)
	fs.takeHits()
	if hr := historyOf(t, h, "NO1"); text(hr.Cursor) != "2026-06-01" || text(hr.LiveThrough) != "2026-09-13" {
		t.Fatalf("after the first cycle the walk = %+v (live through %s), want complete and the live window read through tomorrow", hr, text(hr.LiveThrough))
	}

	// Twenty days away: today is 2026-10-02 and its live window starts on
	// 2026-09-18, so 2026-09-14 to 2026-09-17 were never read. The gap's
	// first request fails; the live window is read regardless and leaves
	// live_through where it was.
	h.Advance(20 * 24 * time.Hour)
	fs.set(func(fs *fakeService) { fs.windowStatus["NO1 2026-09-14..2026-09-17"] = http.StatusServiceUnavailable })
	if hits := fs.takeHits(); len(hits) != 0 {
		t.Fatalf("stray hits %v", hits)
	}
	runSpotPriceCycle(t, w)
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO1 2026-09-14..2026-09-17", "NO1 2026-09-18..2026-10-03"}) {
		t.Errorf("asked for %v, want the areas, the gap, then the live window", hits)
	}
	if hr := historyOf(t, h, "NO1"); text(hr.LiveThrough) != "2026-09-13" || hr.Attempts != 1 || hr.NextAttempt == nil {
		t.Errorf("after the gap failed the walk = %+v (live through %s), want live_through kept and the gap backed off", hr, text(hr.LiveThrough))
	}
	if got := energy.SpotPriceNextWaitForTest(w); got != 15*time.Minute {
		t.Errorf("while the gap backs off the next cycle is in %v, want the poll", got)
	}

	fs.set(func(fs *fakeService) { delete(fs.windowStatus, "NO1 2026-09-14..2026-09-17") })
	h.Advance(15 * time.Minute)
	runSpotPriceCycle(t, w)
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO1 2026-09-14..2026-09-17"}) {
		t.Errorf("asked for %v, want the areas and the gap, the live window read a quarter ago", hits)
	}
	for _, day := range windowDays("2026-09-14", "2026-09-17") {
		if n := dayCount(t, h, "NO1", day, "Service"); n != 96 {
			t.Errorf("%s: %d intervals, want the day read after the downtime", day, n)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_price_fetches WHERE day BETWEEN '2026-09-14' AND '2026-09-17'`); n != 0 {
		t.Errorf("%d fetch rows recorded for the gap's days, want none: they are history", n)
	}
	if hr := historyOf(t, h, "NO1"); text(hr.LiveThrough) != "2026-09-17" || hr.Attempts != 0 || hr.LastError != nil || hr.DaysSkipped != 0 {
		t.Errorf("after the gap the walk = %+v (live through %s), want it through the day before the live window", hr, text(hr.LiveThrough))
	}
}

// TestSpotPriceService_AnAreasFailureLeavesNoErrorOnceTheyAnswer pins that
// one failed /api/v1/areas leaves nothing on the coverage once it answers.
func TestSpotPriceService_AnAreasFailureLeavesNoErrorOnceTheyAnswer(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-08-29"})
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	runSpotPriceCycle(t, w)
	fs.set(func(fs *fakeService) { fs.areasStatus = http.StatusInternalServerError })
	h.Advance(time.Minute)
	runSpotPriceCycle(t, w)
	fs.set(func(fs *fakeService) { fs.areasStatus = 0 })
	h.Advance(time.Minute)
	runSpotPriceCycle(t, w)
	if hr := historyOf(t, h, "NO1"); hr.Attempts != 0 || hr.LastError != nil || hr.NextAttempt != nil {
		t.Errorf("the walk = %+v (%s), want no failure left", hr, text(hr.LastError))
	}
	c := serviceCoverageOf(t, h, "NO1")
	if c.History == nil || !c.History.Complete || c.History.Error != nil || c.FollowedByService == nil || !*c.FollowedByService {
		t.Errorf("the coverage = %+v, want followed and complete with no error", c)
	}
}

// TestSpotPriceService_AnAreaTheServiceStopsFollowingIsNotFollowed pins the
// active flag: an area the service deactivates is answered as not followed,
// keeps what it copied and is no longer read; reactivated, it is again.
func TestSpotPriceService_AnAreaTheServiceStopsFollowingIsNotFollowed(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-08-29", "NO2": "2026-08-29"})
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	runSpotPriceCycle(t, w)
	fs.takeHits()
	fs.setAreas(map[string]string{"NO1": "2026-08-29"}, "NO2")
	h.Advance(25 * time.Hour) // every window due for its re-read
	runSpotPriceCycle(t, w)
	if hits := fs.takeHits(); slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, "NO2 ") }) {
		t.Errorf("asked for %v, want nothing of NO2 once it is inactive", hits)
	}
	if hr := historyOf(t, h, "NO2"); hr.Active || text(hr.Cursor) != "2026-08-29" {
		t.Errorf("NO2's row = %+v, want it kept, inactive", hr)
	}
	no2 := serviceCoverageOf(t, h, "NO2")
	if no2.FollowedByService == nil || *no2.FollowedByService || no2.History == nil || !no2.History.Complete {
		t.Errorf("NO2's coverage = %+v, want not followed, its history kept", no2)
	}
	if no1 := serviceCoverageOf(t, h, "NO1"); no1.FollowedByService == nil || !*no1.FollowedByService {
		t.Errorf("NO1's coverage = %+v, want followed", no1)
	}

	fs.setAreas(map[string]string{"NO1": "2026-08-29", "NO2": "2026-08-29"})
	h.Advance(time.Minute)
	runSpotPriceCycle(t, w)
	if c := serviceCoverageOf(t, h, "NO2"); c.FollowedByService == nil || !*c.FollowedByService || !historyOf(t, h, "NO2").Active {
		t.Errorf("NO2 active again: coverage %+v, want followed", c)
	}
}

// TestSpotPriceService_SkipsAWindowTheServiceRefusesThreeTimes pins the
// permanent failure: a history window the service answers 400 to three
// times in a row is skipped, its days counted, the walk moved past it and
// its error kept until the next window is read.
func TestSpotPriceService_SkipsAWindowTheServiceRefusesThreeTimes(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-07-25"})
	fs.windowStatus["NO1 2026-07-25..2026-08-28"] = http.StatusBadRequest
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	const refused = "the spot-price service answered 400 Bad Request"
	for i, wait := range []time.Duration{0, 15 * time.Minute, 30 * time.Minute} {
		h.Advance(wait)
		runSpotPriceCycle(t, w)
		hr := historyOf(t, h, "NO1")
		if i < 2 && (hr.Cursor != nil || hr.Attempts != int32(i+1) || text(hr.LastError) != refused || hr.NextAttempt == nil) {
			t.Errorf("after refusal %d the walk = %+v (%s), want it backed off with no cursor", i+1, hr, text(hr.LastError))
		}
	}
	if hits := fs.takeHits(); len(slices.DeleteFunc(hits, func(hit string) bool { return hit != "NO1 2026-07-25..2026-08-28" })) != 3 {
		t.Errorf("the refused window was asked %d times, want 3", len(hits))
	}
	hr := historyOf(t, h, "NO1")
	if text(hr.Cursor) != "2026-07-25" || hr.DaysSkipped != 35 || hr.Attempts != 0 || hr.NextAttempt != nil || text(hr.LastError) != refused {
		t.Errorf("after the third refusal the walk = %+v (%s), want it past the window, its 35 days skipped and the error kept", hr, text(hr.LastError))
	}
	if c := serviceCoverageOf(t, h, "NO1"); c.History == nil || !c.History.Complete || text(c.History.Error) != refused || c.History.DaysSkipped != 35 {
		t.Errorf("the coverage = %+v, want complete with the skip and its error", c.History)
	}

	// The floor moves earlier: the next window is read and clears the error.
	fs.setAreas(map[string]string{"NO1": "2026-07-01"})
	h.Advance(time.Minute)
	runSpotPriceCycle(t, w)
	if hr := historyOf(t, h, "NO1"); text(hr.Cursor) != "2026-07-01" || hr.LastError != nil || hr.DaysSkipped != 35 {
		t.Errorf("after the next window the walk = %+v (%s), want it complete and the error gone", hr, text(hr.LastError))
	}
}

// TestSpotPriceService_NeverSkipsAWindowForATransientFailure pins the other
// side: a window failing with 503 four times in a row is backed off and
// asked again, never skipped.
func TestSpotPriceService_NeverSkipsAWindowForATransientFailure(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-07-25"})
	fs.windowStatus["NO1 2026-07-25..2026-08-28"] = http.StatusServiceUnavailable
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	for _, wait := range []time.Duration{0, 15 * time.Minute, 30 * time.Minute, time.Hour} {
		h.Advance(wait)
		runSpotPriceCycle(t, w)
	}
	hr := historyOf(t, h, "NO1")
	if hr.Cursor != nil || hr.DaysSkipped != 0 || hr.Attempts != 4 || text(hr.LastError) != "the spot-price service answered 503 Service Unavailable" ||
		!sameInstant(hr.NextAttempt, h.Now().Add(2*time.Hour)) {
		t.Errorf("after four 503s the walk = %+v (%s), want it backed off two hours with no cursor and nothing skipped", hr, text(hr.LastError))
	}
}

// TestSpotPriceService_AWalkBegunBeforeAnyLiveWindowLeavesNoGap pins the
// walk's first window marking the day before the live window as read
// through: should the live window fail for days while history copies, the
// days between where the walk began and the live window once it answers
// are read as the gap.
func TestSpotPriceService_AWalkBegunBeforeAnyLiveWindowLeavesNoGap(t *testing.T) {
	t.Parallel()
	fs := newFakeService(t)
	fs.setAreas(map[string]string{"NO1": "2026-07-25"})
	fs.windowStatus["NO1 "+liveWindow] = http.StatusServiceUnavailable
	h := serviceHarness(t, fs)
	w := energy.NewSpotPriceWorker(h.Deps())
	runSpotPriceCycle(t, w)
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO1 " + liveWindow, "NO1 2026-07-25..2026-08-28"}) {
		t.Errorf("asked for %v, want the failed live window, then the walk", hits)
	}
	if hr := historyOf(t, h, "NO1"); text(hr.LiveThrough) != "2026-08-28" || text(hr.Cursor) != "2026-07-25" {
		t.Errorf("the walk = %+v (live through %s), want it complete and read through the day before the live window", hr, text(hr.LiveThrough))
	}
	// Two days on the live window answers: 2026-08-29 and 2026-08-30 are
	// behind it now, and read first.
	h.Advance(48 * time.Hour)
	runSpotPriceCycle(t, w)
	if hits := fs.takeHits(); !slices.Equal(hits, []string{"areas", "NO1 2026-08-29..2026-08-30", "NO1 2026-08-31..2026-09-15"}) {
		t.Errorf("asked for %v, want the gap, then the live window", hits)
	}
	if hr := historyOf(t, h, "NO1"); text(hr.LiveThrough) != "2026-09-15" {
		t.Errorf("live through %s, want tomorrow", text(hr.LiveThrough))
	}
	for _, day := range []string{"2026-08-29", "2026-08-30"} {
		if n := dayCount(t, h, "NO1", day, "Service"); n != 96 {
			t.Errorf("%s: %d intervals, want the day read", day, n)
		}
	}
}
