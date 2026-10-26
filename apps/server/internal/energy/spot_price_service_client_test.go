package energy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/energy"
)

// This file is the spot-price service's client below the worker
// (spot-price service design S7, plan readings 4, 5 and 12): the host
// guard, redirects, gzip, the body's cap, the statuses with their
// sentences, the key kept out of every sentence and log line, the
// private-only rule over http, and the answers of the fixtures in
// testdata/spot_price_service judged day by day.

// testServiceKey is the bearer key every client test sends. It must appear
// in no sentence and no log line.
const testServiceKey = "sp_dGhpcy1pcy1hLXRlc3Qta2V5LW5ldmVyLWxvZ2dlZC1hbnl3aGVyZQ"

func readServiceFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "spot_price_service", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lockedBuffer is a log sink safe for the handler's concurrent writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// serviceClient is a client of srv over its own transport, logging at debug
// level into the buffer it answers, its clock fixed at now.
func serviceClient(t *testing.T, srv *httptest.Server, now time.Time) (*energy.SpotServiceClientForTest, *lockedBuffer) {
	t.Helper()
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c, err := energy.NewSpotServiceClientForTest(srv.Client().Transport, srv.URL, testServiceKey, logger, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return c, logs
}

// countingTransport records that it was asked, and never dials.
type countingTransport struct{ calls atomic.Int32 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return nil, errors.New("dialled")
}

func TestSpotServiceClient_RefusesAnotherHostBeforeDialling(t *testing.T) {
	t.Parallel()
	rt := &countingTransport{}
	c, err := energy.NewSpotServiceClientForTest(rt, "http://vantigo-spotprices:8080", testServiceKey, slog.Default(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"http://metadata.internal/latest/meta-data/",
		"http://vantigo-spotprices:8081/api/v1/areas",
		"https://vantigo-spotprices:8080/api/v1/areas",
		"http://169.254.169.254/latest/meta-data/",
	} {
		if fail := c.Get(context.Background(), target); fail.Sentence != "the spot-price service's address is not one Vantigo may fetch" {
			t.Errorf("a GET of %s answered %q, want it refused as an address Vantigo may not fetch", target, fail.Sentence)
		}
	}
	if n := rt.calls.Load(); n != 0 {
		t.Errorf("the transport was asked %d times, want none: the refusal comes before any dial", n)
	}
	if fail := c.Get(context.Background(), "http://vantigo-spotprices:8080/api/v1/areas"); rt.calls.Load() != 1 || fail.Sentence != "the spot-price service could not be reached" {
		t.Errorf("the configured host answered %q after %d calls, want it asked once", fail.Sentence, rt.calls.Load())
	}
}

func TestSpotServiceClient_SendsTheKeyAndFollowsNoRedirect(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+testServiceKey || r.Header.Get("Accept") != "application/json" ||
			!strings.HasPrefix(r.Header.Get("User-Agent"), "vantigo/") {
			http.Error(w, "unexpected headers", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/api/v1/elsewhere", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c, _ := serviceClient(t, srv, time.Now())
	_, fail := c.Areas(context.Background())
	if fail.Sentence != "the spot-price service redirects; set ENERGY_SPOT_PRICE_SERVICE_URL to the address it serves" {
		t.Errorf("a redirect answered %q, want the redirect's sentence", fail.Sentence)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 || hits[0] != "/api/v1/areas" {
		t.Errorf("the service was asked %v, want only /api/v1/areas: the redirect was followed", hits)
	}
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSpotServiceClient_DecodesGzipAndCapsTheDecodedBody(t *testing.T) {
	t.Parallel()
	areas := readServiceFixture(t, "areas.json")
	huge := append(append([]byte("["), bytes.Repeat([]byte(" "), 2<<20)...), ']')
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			http.Error(w, "no gzip asked for", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		if r.URL.Path == "/api/v1/prices" {
			_, _ = w.Write(gzipped(t, huge)) // a few KiB on the wire, 2 MiB decoded
			return
		}
		_, _ = w.Write(gzipped(t, areas))
	}))
	t.Cleanup(srv.Close)
	c, _ := serviceClient(t, srv, time.Now())
	got, fail := c.Areas(context.Background())
	if fail.Sentence != "" || len(got) != 12 {
		t.Fatalf("the gzipped areas = %d zones, %q; want the twelve", len(got), fail.Sentence)
	}
	if got[3].Code != "NO1" || !got[3].Active || got[3].FirstStart == nil || !got[3].FirstStart.Equal(time.Date(2014, 12, 31, 23, 0, 0, 0, time.UTC)) {
		t.Errorf("NO1 = %+v, want active with its first start", got[3])
	}
	if got[6].Code != "NO4" || got[6].FirstStart != nil || got[5].FirstStart != nil {
		t.Errorf("NO3 %+v and NO4 %+v, want no first start for coverage without one or null", got[5], got[6])
	}
	if _, fail := c.Days(context.Background(), "NO1", "2026-09-10", "2026-09-10"); fail.Sentence != "the spot-price service's answer is larger than 1 MiB" {
		t.Errorf("2 MiB once decoded answered %q, want the cap's sentence", fail.Sentence)
	}
}

func TestSpotServiceClient_AnswersEachStatusWithItsSentenceAndNeverTheKey(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		switch r.URL.Query().Get("area") {
		case "NO1":
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"title":"Unauthorized","status":401,"detail":"a valid API key is required (Authorization: Bearer <key>)"}`))
		case "NO2":
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		case "NO3":
			w.Header().Set("Retry-After", now.Add(2*time.Hour).Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
		case "NO4":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`the service's own words ` + r.Header.Get("Authorization")))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"title":"Bad Request","status":400,"detail":"area \"NO6\" is not a known bidding zone"}`))
		}
	}))
	t.Cleanup(srv.Close)
	c, logs := serviceClient(t, srv, now)
	cases := []struct {
		area     string
		sentence string
		retryAt  time.Time
	}{
		{"NO1", "the spot-price service refused the key", time.Time{}},
		{"NO2", "the spot-price service is rate limiting this installation", now.Add(120 * time.Second)},
		{"NO3", "the spot-price service is rate limiting this installation", now.Add(2 * time.Hour)},
		{"NO4", "the spot-price service answered 503 Service Unavailable", time.Time{}},
		{"NO6", "the spot-price service answered 400 Bad Request", time.Time{}},
	}
	for _, tc := range cases {
		_, fail := c.Days(context.Background(), tc.area, "2026-09-10", "2026-09-10")
		if fail.Sentence != tc.sentence || !fail.RetryAt.Equal(tc.retryAt) {
			t.Errorf("%s answered %+v, want %q retrying at %v", tc.area, fail, tc.sentence, tc.retryAt)
		}
		if strings.Contains(fail.Sentence, testServiceKey) {
			t.Errorf("%s's sentence carries the key", tc.area)
		}
	}
	out := logs.String()
	if !strings.Contains(out, `"level":"DEBUG"`) || !strings.Contains(out, `is not a known bidding zone`) {
		t.Errorf("the 400's detail was not logged at debug level: %s", out)
	}
	if strings.Contains(out, testServiceKey) || strings.Contains(out, "own words") || strings.Contains(out, "currency=") || strings.Contains(out, "from=") {
		t.Errorf("the log carries the key, a body or the query: %s", out)
	}
}

// fakeResolver answers addrs for every host, and records that it was asked.
type fakeResolver struct{ addrs []netip.Addr }

func (r fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.addrs, nil
}

func TestSpotServiceClient_OverHTTPReachesOnlyAPrivateAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, base, addr string
		dialled          bool
		sentence         string
	}{
		{name: "http to a public address is refused before the dial", base: "http://spotprices.example.com:8080", addr: "93.184.216.34",
			sentence: "the spot-price service's address is public; use https"},
		{name: "http to a private address is dialled", base: "http://vantigo-spotprices:8080", addr: "172.18.0.5",
			dialled: true, sentence: "the spot-price service could not be reached"},
		{name: "https to a public address is dialled", base: "https://spotprices.example.com", addr: "93.184.216.34",
			dialled: true, sentence: "the spot-price service could not be reached"},
		{name: "loopback is refused over http", base: "http://vantigo-spotprices:8080", addr: "127.0.0.1",
			sentence: "the spot-price service's address is not one Vantigo may fetch"},
		{name: "the metadata address is refused over https", base: "https://spotprices.example.com", addr: "169.254.169.254",
			sentence: "the spot-price service's address is not one Vantigo may fetch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var dialled atomic.Bool
			dial := func(context.Context, string, string) (net.Conn, error) {
				dialled.Store(true)
				return nil, errors.New("no network in this test")
			}
			c, err := energy.NewGuardedSpotServiceClientForTest(fakeResolver{addrs: []netip.Addr{netip.MustParseAddr(tc.addr)}}, dial, tc.base, testServiceKey)
			if err != nil {
				t.Fatal(err)
			}
			_, fail := c.Areas(context.Background())
			if fail.Sentence != tc.sentence || dialled.Load() != tc.dialled {
				t.Errorf("answered %q, dialled %v; want %q, dialled %v", fail.Sentence, dialled.Load(), tc.sentence, tc.dialled)
			}
		})
	}
}

// fixtureService serves one fixture as every /api/v1/prices answer.
func fixtureService(t *testing.T, name string) *energy.SpotServiceClientForTest {
	t.Helper()
	body := readServiceFixture(t, name)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, _ := serviceClient(t, srv, time.Now())
	return c
}

type judged struct {
	day       string
	intervals int
	verdict   string
}

func judgedDays(t *testing.T, c *energy.SpotServiceClientForTest, first, last string) ([]judged, []energy.SpotServiceDay) {
	t.Helper()
	days, fail := c.Days(context.Background(), "NO1", first, last)
	if fail.Sentence != "" {
		t.Fatalf("the answer failed: %s", fail.Sentence)
	}
	out := make([]judged, 0, len(days))
	for _, d := range days {
		out = append(out, judged{d.Day, d.Intervals, d.Verdict})
	}
	return out, days
}

func TestSpotServiceClient_JudgesTheFixturesDayByDay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fixture, first, last string
		want                 []judged
	}{
		{"prices_quarter_hours_spring_2026-03-29.json", "2026-03-29", "2026-03-29", []judged{{"2026-03-29", 92, ""}}},
		{"prices_quarter_hours_autumn_2025-10-26.json", "2025-10-26", "2025-10-26", []judged{{"2025-10-26", 100, ""}}},
		{"prices_hks_hourly_2025-03-29_to_2025-03-31.json", "2025-03-29", "2025-03-31",
			[]judged{{"2025-03-29", 24, ""}, {"2025-03-30", 23, ""}, {"2025-03-31", 24, ""}}},
		{"prices_hks_hourly_2024-10-26_to_2024-10-28.json", "2024-10-26", "2024-10-28",
			[]judged{{"2024-10-26", 24, ""}, {"2024-10-27", 25, ""}, {"2024-10-28", 24, ""}}},
		{"prices_entsoe_without_nok_2024-06-15.json", "2024-06-15", "2024-06-15",
			[]judged{{"2024-06-15", 24, "the spot-price service holds no NOK price for the day"}}},
		{"prices_gap_2026-09-08.json", "2026-09-08", "2026-09-08",
			[]judged{{"2026-09-08", 95, "the day's intervals are not contiguous"}}},
		{"prices_live_2026-09-10_to_2026-09-13.json", "2026-09-10", "2026-09-13", []judged{
			{"2026-09-10", 96, ""}, {"2026-09-11", 0, "the spot-price service holds no prices for the day"},
			{"2026-09-12", 96, ""}, {"2026-09-13", 96, ""},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got, _ := judgedDays(t, fixtureService(t, tc.fixture), tc.first, tc.last)
			if len(got) != len(tc.want) {
				t.Fatalf("judged %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("judged %+v, want %+v", got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSpotServiceClient_ReadsTheFiguresAndTheProvenanceExactly(t *testing.T) {
	t.Parallel()
	_, days := judgedDays(t, fixtureService(t, "prices_live_2026-09-10_to_2026-09-13.json"), "2026-09-10", "2026-09-13")
	if days[0].Preliminary || !days[3].Preliminary {
		t.Errorf("preliminary: 2026-09-10 %v, 2026-09-13 %v; want only tomorrow", days[0].Preliminary, days[3].Preliminary)
	}
	// The first quarter-hour: 40 EUR/MWh, 0.46849 NOK/kWh at 11.71234.
	r := days[0].Rows[0]
	if r.NokPerKwh != 0.46849 || r.EurPerKwh == nil || *r.EurPerKwh != 0.04 || r.ExchangeRate == nil || *r.ExchangeRate != 11.71234 {
		t.Errorf("the first quarter-hour's figures = %v %v %v", r.NokPerKwh, r.EurPerKwh, r.ExchangeRate)
	}
	if r.State != "final" || r.Upstream == nil || *r.Upstream != "nordpool_portal" || r.RateSource == nil || *r.RateSource != "nordpool" ||
		r.UpstreamVersion == nil || *r.UpstreamVersion != 3 {
		t.Errorf("the first quarter-hour's provenance = %+v", r)
	}
	// 41.37 EUR/MWh is 0.04137 EUR/kWh exactly, not 41.37/1000 in floats.
	if e := days[0].Rows[1].EurPerKwh; e == nil || *e != 0.04137 {
		t.Errorf("41.37 EUR/MWh read as %v EUR/kWh, want 0.04137", e)
	}
}

func TestSpotServiceClient_FailsADayWithAValueItCannotStore(t *testing.T) {
	t.Parallel()
	day := string(readServiceFixture(t, "prices_quarter_hours_spring_2026-03-29.json"))
	cases := map[string]struct{ from, to, want string }{
		"an unknown source":   {`"source": "nordpool_portal"`, `"source": "Nord Pool"`, "the spot-price service answered an unknown source"},
		"an unknown rate":     {`"rateSource": "nordpool"`, `"rateSource": "a-bank"`, "the spot-price service answered an unknown source"},
		"an unknown state":    {`"state": "final"`, `"state": "Final"`, "the spot-price service answered an unknown state"},
		"a figure too large":  {`"priceEurPerMwh": 40,`, `"priceEurPerMwh": 4e12,`, "an interval's price is not a number Vantigo can store"},
		"a version not whole": {`"version": 2`, `"version": 2.5`, "the spot-price service answered an interval without a version"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := strings.Replace(day, tc.from, tc.to, 1)
			if body == day {
				t.Fatalf("%q is not in the fixture", tc.from)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			t.Cleanup(srv.Close)
			c, _ := serviceClient(t, srv, time.Now())
			days, fail := c.Days(context.Background(), "NO1", "2026-03-29", "2026-03-29")
			if fail.Sentence != "" || len(days) != 1 || days[0].Verdict != tc.want || !days[0].Failed {
				t.Errorf("answered %+v %q, want the day failed with %q", days, fail.Sentence, tc.want)
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"error":"no"}`)) }))
	t.Cleanup(srv.Close)
	c, _ := serviceClient(t, srv, time.Now())
	if _, fail := c.Days(context.Background(), "NO1", "2026-03-29", "2026-03-29"); fail.Sentence != "the spot-price service did not answer a list of prices" {
		t.Errorf("an object answered %q, want the not-a-list sentence", fail.Sentence)
	}
}
