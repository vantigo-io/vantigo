package energy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the spot price worker, energy-spot-prices (energy consumption
// billing design D2, plan readings 2–5 and 12): the planner as a pure
// function, and the cycle against a feed served by an httptest server — the
// answers recorded and retried, the twenty-request cap, the lease, the
// overlap refused, and the guard on the production transport. The harness
// clock starts at modtest.Start, 2026-09-12 14:00 in Oslo, so the window is
// 2026-09-05 to 2026-09-13.

var osloZone = func() *time.Location {
	zone, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		panic(err)
	}
	return zone
}()

// windowDays is the civil days from first to last, inclusive, as the planner
// writes them.
func windowDays(first, last string) []string {
	from, _ := time.Parse(time.DateOnly, first)
	to, _ := time.Parse(time.DateOnly, last)
	var out []string
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format(time.DateOnly))
	}
	return out
}

func planned(area string, days []string, tomorrow string) []energy.SpotFeedPlanned {
	out := make([]energy.SpotFeedPlanned, 0, len(days))
	for _, d := range days {
		out = append(out, energy.SpotFeedPlanned{Area: area, Day: d, Tomorrow: d == tomorrow})
	}
	return out
}

func fetchRow(area, day, status string, attempts int32, next *time.Time) store.EnergySpotPriceFetch {
	d, _ := time.Parse(time.DateOnly, day)
	return store.EnergySpotPriceFetch{PriceArea: area, Day: pgtype.Date{Time: d, Valid: true}, Status: status, Attempts: attempts, NextAttemptAt: next}
}

func at(t time.Time) *time.Time { return &t }

func TestSpotPriceWorker_PlansTheDaysToAskFor(t *testing.T) {
	t.Parallel()
	now := modtest.Start // 2026-09-12 14:00 in Oslo
	window := windowDays("2026-09-05", "2026-09-13")
	cases := []struct {
		name  string
		now   time.Time
		areas []energy.SpotFeedPlanArea
		want  []energy.SpotFeedPlanned
	}{
		{name: "no area in use asks for nothing", now: now},
		{
			name:  "an area with nothing recorded asks for today − 7 to tomorrow",
			now:   now,
			areas: []energy.SpotFeedPlanArea{{Area: "NO1"}},
			want:  planned("NO1", window, "2026-09-13"),
		},
		{
			name: "a stored day is skipped whatever its record says",
			now:  now,
			areas: []energy.SpotFeedPlanArea{{
				Area: "NO1", StoredDays: []string{"2026-09-05", "2026-09-12"},
				Fetches: []store.EnergySpotPriceFetch{fetchRow("NO1", "2026-09-05", "failed", 3, at(now.Add(-time.Hour)))},
			}},
			want: append(planned("NO1", windowDays("2026-09-06", "2026-09-11"), ""),
				energy.SpotFeedPlanned{Area: "NO1", Day: "2026-09-13", Tomorrow: true}),
		},
		{
			name: "the record decides the rest: due once next_attempt_at has come, never without one",
			now:  now,
			areas: []energy.SpotFeedPlanArea{{
				Area: "NO1",
				Fetches: []store.EnergySpotPriceFetch{
					fetchRow("NO1", "2026-09-05", "ok", 1, nil),
					fetchRow("NO1", "2026-09-06", "pending", 1, at(now.Add(time.Hour))),
					fetchRow("NO1", "2026-09-07", "failed", 2, at(now.Add(-time.Minute))),
					fetchRow("NO1", "2026-09-08", "missing", 7, nil),
					fetchRow("NO1", "2026-09-09", "missing", 3, at(now)),
				},
			}},
			want: []energy.SpotFeedPlanned{
				{Area: "NO1", Day: "2026-09-07", Attempts: 2},
				{Area: "NO1", Day: "2026-09-09", Attempts: 3},
				{Area: "NO1", Day: "2026-09-10"},
				{Area: "NO1", Day: "2026-09-11"},
				{Area: "NO1", Day: "2026-09-12"},
				{Area: "NO1", Day: "2026-09-13", Tomorrow: true},
			},
		},
		{
			name: "oldest days first, the areas in the order given",
			now:  now,
			areas: []energy.SpotFeedPlanArea{
				{Area: "NO2", StoredDays: windowDays("2026-09-07", "2026-09-13")},
				{Area: "NO1", StoredDays: windowDays("2026-09-07", "2026-09-13")},
			},
			want: []energy.SpotFeedPlanned{
				{Area: "NO2", Day: "2026-09-05"}, {Area: "NO1", Day: "2026-09-05"},
				{Area: "NO2", Day: "2026-09-06"}, {Area: "NO1", Day: "2026-09-06"},
			},
		},
		{
			name:  "twenty a cycle",
			now:   now,
			areas: []energy.SpotFeedPlanArea{{Area: "NO1"}, {Area: "NO2"}, {Area: "NO3"}},
			want: func() []energy.SpotFeedPlanned {
				var out []energy.SpotFeedPlanned
				for _, d := range windowDays("2026-09-05", "2026-09-11") {
					for _, a := range []string{"NO1", "NO2", "NO3"} {
						out = append(out, energy.SpotFeedPlanned{Area: a, Day: d})
					}
				}
				return out[:20]
			}(),
		},
		{
			name:  "today is Oslo's: 22:30 UTC in September is already tomorrow there",
			now:   time.Date(2026, time.September, 12, 22, 30, 0, 0, time.UTC),
			areas: []energy.SpotFeedPlanArea{{Area: "NO1"}},
			want:  planned("NO1", windowDays("2026-09-06", "2026-09-14"), "2026-09-14"),
		},
		{
			name:  "and across a new year in winter time",
			now:   time.Date(2026, time.December, 31, 23, 30, 0, 0, time.UTC),
			areas: []energy.SpotFeedPlanArea{{Area: "NO5"}},
			want:  planned("NO5", windowDays("2026-12-25", "2027-01-02"), "2027-01-02"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := energy.PlanSpotFeedFetchesForTest(tc.now, tc.areas)
			if !slices.Equal(got, tc.want) {
				t.Errorf("planned\n%v\nwant\n%v", got, tc.want)
			}
		})
	}
}

func TestSpotPriceWorker_BacksOffAFailedDay(t *testing.T) {
	t.Parallel()
	for attempts, want := range map[int32]time.Duration{
		1: 15 * time.Minute, 2: 30 * time.Minute, 3: time.Hour, 4: 2 * time.Hour, 5: 4 * time.Hour,
		6: 6 * time.Hour, 7: 6 * time.Hour, 1000: 6 * time.Hour,
	} {
		if got := energy.SpotFeedBackoffForTest(attempts); got != want {
			t.Errorf("after %d attempts the wait is %v, want %v", attempts, got, want)
		}
	}
}

// feedServer is the public feed's shape over TLS: /prices/{year}/{month}-{day}_{area}.json
// answers the day's hours in Oslo, unless the test set another status or
// body for "AREA YYYY-MM-DD". It records every request in order.
type feedServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []string
	status map[string]int
	bodies map[string]string
}

var feedPath = regexp.MustCompile(`^/prices/(\d{4})/(\d{2})-(\d{2})_([A-Z0-9]+)\.json$`)

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	fs := &feedServer{status: map[string]int{}, bodies: map[string]string{}}
	fs.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := feedPath.FindStringSubmatch(r.URL.Path)
		if m == nil || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "Vantigo-Energy" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		day := m[1] + "-" + m[2] + "-" + m[3]
		key := m[4] + " " + day
		fs.mu.Lock()
		fs.hits = append(fs.hits, key)
		status, body := fs.status[key], fs.bodies[key]
		fs.mu.Unlock()
		if status != 0 {
			http.Error(w, "the feed's own words", status)
			return
		}
		if body == "" {
			body = hourlyFeedDay(t, day)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *feedServer) set(key string, status int) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if status == http.StatusOK {
		delete(fs.status, key)
		return
	}
	fs.status[key] = status
}

// takeHits answers the requests since the last call.
func (fs *feedServer) takeHits() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := fs.hits
	fs.hits = nil
	return out
}

// hourlyFeedDay is day's hours in Oslo as the feed writes them, the NOK
// price 0.5 + hour/100.
func hourlyFeedDay(t *testing.T, day string) string {
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		t.Error(err)
		return ""
	}
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, osloZone)
	end := time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, osloZone)
	var entries []map[string]any
	for i, h := 0, start; h.Before(end); i, h = i+1, h.Add(time.Hour) {
		entries = append(entries, map[string]any{
			"NOK_per_kWh": 0.5 + float64(i)/100, "EUR_per_kWh": 0.04, "EXR": 11.5,
			"time_start": h.In(osloZone).Format(time.RFC3339), "time_end": h.Add(time.Hour).In(osloZone).Format(time.RFC3339),
		})
	}
	b, err := json.Marshal(entries)
	if err != nil {
		t.Error(err)
	}
	return string(b)
}

// spotPriceHarness is an installation with the feed on, fetching from fs.
func spotPriceHarness(t *testing.T, fs *feedServer) *modtest.Harness {
	t.Helper()
	return newHarness(t,
		modtest.WithTransport(fs.srv.Client().Transport),
		modtest.WithEnv("ENERGY_SPOT_PRICE_FEED_ENABLED", "1"),
		modtest.WithEnv("ENERGY_SPOT_PRICE_FEED_URL", fs.srv.URL+"/prices/{year}/{month}-{day}_{area}.json"),
	)
}

// priceAreaInUse makes area one the worker fetches for, by a price stored
// long before the window (plan reading 7's second half).
func priceAreaInUse(t *testing.T, h *modtest.Harness, area string) {
	t.Helper()
	h.Exec(t, `INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
	           VALUES ($1, '2026-01-01T00:00:00+01:00', '2026-01-01T01:00:00+01:00', 1, 'Import', now())`, area)
}

type fetchRecord struct {
	Status                              string
	Attempts                            int32
	LastError                           *string
	AttemptedAt, FetchedAt, NextAttempt *time.Time
}

func fetchRecordOf(t *testing.T, h *modtest.Harness, area, day string) fetchRecord {
	t.Helper()
	var r fetchRecord
	err := h.Pool().QueryRow(context.Background(),
		`SELECT status, attempts, last_error, attempted_at, fetched_at, next_attempt_at
		   FROM energy.spot_price_fetches WHERE price_area = $1 AND day = $2::date`, area, day).
		Scan(&r.Status, &r.Attempts, &r.LastError, &r.AttemptedAt, &r.FetchedAt, &r.NextAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("no fetch row for %s %s", area, day)
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func sameInstant(a *time.Time, b time.Time) bool { return a != nil && a.Equal(b) }

func runSpotPriceCycle(t *testing.T, w *energy.SpotPriceWorker) int {
	t.Helper()
	n, err := w.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	return n
}

func TestSpotPriceWorker_RecordsEachAnswerAndRetriesAsReadingFourSays(t *testing.T) {
	t.Parallel()
	fs := newFeedServer(t)
	h := spotPriceHarness(t, fs)
	priceAreaInUse(t, h, "NO1")
	fs.set("NO1 2026-09-13", http.StatusNotFound)            // tomorrow, not yet published
	fs.set("NO1 2026-09-06", http.StatusNotFound)            // a past day the feed has no file for
	fs.set("NO1 2026-09-07", http.StatusInternalServerError) // the feed failing
	fs.bodies["NO1 2026-09-08"] = `{"error":"try later"}`    // a body that is not prices
	w := energy.NewSpotPriceWorker(h.Deps())
	start := h.Now()

	if n := runSpotPriceCycle(t, w); n != 9 {
		t.Fatalf("the first cycle made %d requests, want the window's 9", n)
	}
	if hits := fs.takeHits(); !slices.Equal(hits, []string{
		"NO1 2026-09-05", "NO1 2026-09-06", "NO1 2026-09-07", "NO1 2026-09-08", "NO1 2026-09-09",
		"NO1 2026-09-10", "NO1 2026-09-11", "NO1 2026-09-12", "NO1 2026-09-13",
	}) {
		t.Errorf("asked for %v, want the window oldest first", hits)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO1' AND source = 'Feed' AND received_at = $1`, start); n != 5*24 {
		t.Errorf("%d intervals fetched, want the five days answered (120)", n)
	}
	var nok float64
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT nok_per_kwh::float8 FROM energy.spot_prices WHERE price_area = 'NO1' AND "start" = '2026-09-10T03:00:00+02:00' AND "end" = '2026-09-10T04:00:00+02:00'`).
		Scan(&nok); err != nil || nok != 0.53 {
		t.Errorf("the 2026-09-10 03:00 hour = %v (%v), want 0.53", nok, err)
	}

	ok := fetchRecordOf(t, h, "NO1", "2026-09-05")
	if ok.Status != "ok" || ok.Attempts != 1 || ok.LastError != nil || !sameInstant(ok.FetchedAt, start) || ok.NextAttempt != nil {
		t.Errorf("the day answered = %+v, want ok, fetched now, never asked again", ok)
	}
	pending := fetchRecordOf(t, h, "NO1", "2026-09-13")
	if pending.Status != "pending" || pending.LastError != nil || !sameInstant(pending.NextAttempt, start.Add(time.Hour)) {
		t.Errorf("tomorrow's 404 = %+v, want pending, asked again in an hour", pending)
	}
	missing := fetchRecordOf(t, h, "NO1", "2026-09-06")
	if missing.Status != "missing" || !sameInstant(missing.NextAttempt, start.Add(24*time.Hour)) {
		t.Errorf("a past day's 404 = %+v, want missing, asked again in a day", missing)
	}
	failed := fetchRecordOf(t, h, "NO1", "2026-09-07")
	if failed.Status != "failed" || failed.LastError == nil || *failed.LastError != "the feed answered 500 Internal Server Error" ||
		!sameInstant(failed.NextAttempt, start.Add(15*time.Minute)) || failed.FetchedAt != nil {
		t.Errorf("a 500 = %+v, want failed with its sentence, asked again in 15 minutes", failed)
	}
	if notPrices := fetchRecordOf(t, h, "NO1", "2026-09-08"); notPrices.Status != "failed" || notPrices.LastError == nil ||
		*notPrices.LastError != "the feed did not answer a list of prices" {
		t.Errorf("a body that is not prices = %+v, want failed with the parser's sentence", notPrices)
	}
	logs := h.Logs()
	if !strings.Contains(logs, `"day":"2026-09-07"`) || !strings.Contains(logs, "the feed answered 500 Internal Server Error") {
		t.Error("the failure was not logged with its day and its sentence")
	}
	if strings.Contains(logs, "/prices/") || strings.Contains(logs, "the feed's own words") || strings.Contains(logs, "try later") {
		t.Error("the log carries the feed's path or a body")
	}

	if n := runSpotPriceCycle(t, w); n != 0 {
		t.Errorf("a cycle straight after made %d requests, want none due", n)
	}
	h.Advance(15 * time.Minute)
	if n := runSpotPriceCycle(t, w); n != 2 {
		t.Errorf("15 minutes on, %d requests (%v), want the two failed days", n, fs.takeHits())
	}
	fs.takeHits()
	if again := fetchRecordOf(t, h, "NO1", "2026-09-07"); again.Attempts != 2 || !sameInstant(again.NextAttempt, h.Now().Add(30*time.Minute)) {
		t.Errorf("the second failure = %+v, want the wait doubled to 30 minutes", again)
	}

	// An hour after the first cycle tomorrow is asked again, and is there.
	fs.set("NO1 2026-09-13", http.StatusOK)
	h.Advance(45 * time.Minute)
	runSpotPriceCycle(t, w)
	if hits := fs.takeHits(); !slices.Contains(hits, "NO1 2026-09-13") {
		t.Errorf("an hour on, asked for %v, want tomorrow among them", hits)
	}
	if published := fetchRecordOf(t, h, "NO1", "2026-09-13"); published.Status != "ok" || published.Attempts != 2 || published.NextAttempt != nil {
		t.Errorf("tomorrow once published = %+v, want ok on its second attempt", published)
	}
}

func TestSpotPriceWorker_AsksForAtMostTwentyDaysACycle(t *testing.T) {
	t.Parallel()
	fs := newFeedServer(t)
	h := spotPriceHarness(t, fs)
	for _, area := range []string{"NO1", "NO2", "NO3"} {
		priceAreaInUse(t, h, area)
	}
	w := energy.NewSpotPriceWorker(h.Deps())
	if n := runSpotPriceCycle(t, w); n != 20 {
		t.Fatalf("the first cycle made %d requests, want 20 of the 27 due", n)
	}
	var want []string
	for _, d := range windowDays("2026-09-05", "2026-09-11") {
		for _, a := range []string{"NO1", "NO2", "NO3"} {
			want = append(want, a+" "+d)
		}
	}
	if hits := fs.takeHits(); !slices.Equal(hits, want[:20]) {
		t.Errorf("asked for %v, want the oldest days first, areas in order: %v", hits, want[:20])
	}
	if n := runSpotPriceCycle(t, w); n != 7 {
		t.Errorf("the next cycle made %d requests, want the 7 left", n)
	}
}

func TestSpotPriceWorker_SkipsACycleWhileAnotherReplicaHoldsTheLease(t *testing.T) {
	t.Parallel()
	fs := newFeedServer(t)
	h := spotPriceHarness(t, fs)
	priceAreaInUse(t, h, "NO1")
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
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, energy.SpotPriceLeaseKeyForTest); err != nil {
		t.Fatal(err)
	}
	if n := runSpotPriceCycle(t, w); n != 9 {
		t.Errorf("after the release the cycle made %d requests, want the window's 9", n)
	}
}

func TestSpotPriceWorker_RefusesADayOverlappingStoredPricesOfAnotherLength(t *testing.T) {
	t.Parallel()
	fs := newFeedServer(t)
	h := spotPriceHarness(t, fs)
	// Two hours imported across midnight: 2026-09-11 is stored, and the
	// feed's first hour of 2026-09-12 falls inside the second.
	h.Exec(t, `INSERT INTO energy.spot_prices (price_area, "start", "end", nok_per_kwh, source, received_at)
	           VALUES ('NO1', '2026-09-11T23:00:00+02:00', '2026-09-12T01:00:00+02:00', 1, 'Import', now())`)
	w := energy.NewSpotPriceWorker(h.Deps())
	if n := runSpotPriceCycle(t, w); n != 8 {
		t.Fatalf("the cycle made %d requests, want the 8 days not stored", n)
	}
	refused := fetchRecordOf(t, h, "NO1", "2026-09-12")
	if refused.Status != "failed" || refused.LastError == nil || *refused.LastError != "the day's intervals overlap stored prices of another length" {
		t.Errorf("the overlapping day = %+v, want failed with the overlap's sentence", refused)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.spot_prices WHERE price_area = 'NO1' AND source = 'Feed'
	                     AND "start" >= '2026-09-12T00:00:00+02:00' AND "start" < '2026-09-13T00:00:00+02:00'`); n != 0 {
		t.Errorf("%d intervals of the refused day written, want none", n)
	}
	if ok := fetchRecordOf(t, h, "NO1", "2026-09-13"); ok.Status != "ok" {
		t.Errorf("the next day = %+v, want it written regardless", ok)
	}
}

func TestSpotPriceWorker_GivesUpOnAMissingDayAtItsSeventhAttempt(t *testing.T) {
	t.Parallel()
	fs := newFeedServer(t)
	h := spotPriceHarness(t, fs)
	priceAreaInUse(t, h, "NO1")
	h.Exec(t, `INSERT INTO energy.spot_price_fetches (price_area, day, status, attempts, attempted_at, next_attempt_at)
	           VALUES ('NO1', '2026-09-06', 'missing', 6, $1, $1)`, h.Now().Add(-24*time.Hour))
	fs.set("NO1 2026-09-06", http.StatusNotFound)
	runSpotPriceCycle(t, energy.NewSpotPriceWorker(h.Deps()))
	if r := fetchRecordOf(t, h, "NO1", "2026-09-06"); r.Status != "missing" || r.Attempts != 7 || r.NextAttempt != nil {
		t.Errorf("the seventh 404 = %+v, want missing with no next attempt", r)
	}
}

func TestSpotPriceWorker_TheGuardRefusesALoopbackFeed(t *testing.T) {
	t.Parallel()
	// No WithTransport: the worker's own guarded transport, which must
	// refuse 127.0.0.1 before it dials.
	h := newHarness(t,
		modtest.WithEnv("ENERGY_SPOT_PRICE_FEED_ENABLED", "1"),
		modtest.WithEnv("ENERGY_SPOT_PRICE_FEED_URL", "https://127.0.0.1:1/prices/{year}/{month}-{day}_{area}.json"),
	)
	priceAreaInUse(t, h, "NO1")
	if n := runSpotPriceCycle(t, energy.NewSpotPriceWorker(h.Deps())); n != 9 {
		t.Fatalf("the cycle made %d requests, want the window's 9", n)
	}
	for _, day := range windowDays("2026-09-05", "2026-09-13") {
		r := fetchRecordOf(t, h, "NO1", day)
		if r.Status != "failed" || r.LastError == nil || *r.LastError != "the feed's address is not one Vantigo may fetch" {
			t.Errorf("%s = %+v, want failed as an address Vantigo may not fetch", day, r)
		}
	}
}

func TestSpotPriceWorker_ABuiltByHandConfigWithABadTemplateFailsTheCycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d := h.Deps()
	cfg := *d.Config
	cfg.EnergySpotPriceFeedURL = "http://prices.example.com/{year}/{month}-{day}_{area}.json"
	d.Config = &cfg
	if _, err := energy.NewSpotPriceWorker(d).RunCycle(context.Background()); err == nil {
		t.Error("a cycle over an http template ran, want the template refused")
	}
	if got := fmt.Sprint(energy.NewSpotPriceWorker(h.Deps()).Interval()); got != "15m0s" {
		t.Errorf("the default poll is %s, want 15m0s", got)
	}
}
