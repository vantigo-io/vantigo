package energy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/energy"
)

// This file is the spot price feed below the worker (energy consumption
// billing design D2, plan readings 5 and 13): the template's validation and
// rendering, and the parser against days written in the public feed's shape
// (R5 §5.1) — a plain day, the two DST nights, and the bodies it refuses.

func readFeedFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "spot_prices", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSpotPriceFeed_RendersTheTemplatePerAreaAndDay(t *testing.T) {
	t.Parallel()
	got, err := energy.RenderSpotFeedURLForTest(config.DefaultEnergySpotPriceFeedURL, "NO1", "2026-03-09")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://www.hvakosterstrommen.no/api/v1/prices/2026/03-09_NO1.json"; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
	got, err = energy.RenderSpotFeedURLForTest("https://prices.example.com/{area}/{year}{month}{day}?area={area}", "NO5", "2026-12-24")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://prices.example.com/NO5/20261224?area=NO5"; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestSpotPriceFeed_RefusesATemplateItCannotFetch(t *testing.T) {
	t.Parallel()
	for _, template := range []string{
		"http://www.hvakosterstrommen.no/api/v1/prices/{year}/{month}-{day}_{area}.json",
		"/api/v1/prices/{year}/{month}-{day}_{area}.json",
		"https://www.hvakosterstrommen.no/api/v1/prices/{year}/{month}-{day}.json",
		"https://www.hvakosterstrommen.no/api/v1/prices/{year}/{month}_{area}.json",
		"https:///{year}/{month}-{day}_{area}.json",
		"",
	} {
		if _, err := energy.RenderSpotFeedURLForTest(template, "NO1", "2026-09-10"); err == nil {
			t.Errorf("the template %q was accepted", template)
		}
	}
}

func TestSpotPriceFeed_ParsesADayInTheFeedsShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file, day   string
		intervals   int
		first, last string // the first start and the last end, in UTC
	}{
		{"feed_2026-09-10_NO1.json", "2026-09-10", 24, "2026-09-09T22:00:00Z", "2026-09-10T22:00:00Z"},
		// The autumn night: 02:00 twice, +02:00 then +01:00.
		{"feed_2025-10-26_NO1.json", "2025-10-26", 25, "2025-10-25T22:00:00Z", "2025-10-26T23:00:00Z"},
		// The spring night: no 02:00.
		{"feed_2026-03-29_NO1.json", "2026-03-29", 23, "2026-03-28T23:00:00Z", "2026-03-29T22:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			body := readFeedFixture(t, tc.file)
			got, failure := energy.ParseSpotFeedDayForTest(body, "NO1", tc.day)
			if failure != "" {
				t.Fatalf("refused: %s", failure)
			}
			if len(got) != tc.intervals {
				t.Fatalf("%d intervals, want %d", len(got), tc.intervals)
			}
			if s := got[0].Start.Format(time.RFC3339); s != tc.first {
				t.Errorf("first start %s, want %s", s, tc.first)
			}
			if e := got[len(got)-1].End.Format(time.RFC3339); e != tc.last {
				t.Errorf("last end %s, want %s", e, tc.last)
			}
			var entries []struct {
				Nok float64 `json:"NOK_per_kWh"`
				Eur float64 `json:"EUR_per_kWh"`
				Exr float64 `json:"EXR"`
			}
			if err := json.Unmarshal(body, &entries); err != nil {
				t.Fatal(err)
			}
			for i, iv := range got {
				if iv.PriceArea != "NO1" || iv.End.Sub(iv.Start) != time.Hour {
					t.Errorf("interval %d = %+v, want an hour of NO1", i, iv)
				}
				if iv.NokPerKwh != entries[i].Nok || iv.EurPerKwh == nil || *iv.EurPerKwh != entries[i].Eur ||
					iv.ExchangeRate == nil || *iv.ExchangeRate != entries[i].Exr {
					t.Errorf("interval %d's figures = %v %v %v, want the body's %+v", i, iv.NokPerKwh, iv.EurPerKwh, iv.ExchangeRate, entries[i])
				}
			}
		})
	}
}

func TestSpotPriceFeed_KeepsANegativePrice(t *testing.T) {
	t.Parallel()
	got, failure := energy.ParseSpotFeedDayForTest(readFeedFixture(t, "feed_2026-09-10_NO1.json"), "NO1", "2026-09-10")
	if failure != "" {
		t.Fatalf("refused: %s", failure)
	}
	if got[3].NokPerKwh != -0.00117 {
		t.Errorf("the fourth hour's price = %v, want the body's -0.00117", got[3].NokPerKwh)
	}
}

func TestSpotPriceFeed_RefusesADayItCannotStore(t *testing.T) {
	t.Parallel()
	plain := string(readFeedFixture(t, "feed_2026-09-10_NO1.json"))
	var entries []map[string]any
	if err := json.Unmarshal([]byte(plain), &entries); err != nil {
		t.Fatal(err)
	}
	short, err := json.Marshal(entries[:len(entries)-1])
	if err != nil {
		t.Fatal(err)
	}
	withoutLast := string(short)
	cases := []struct {
		name, body, day, want string
	}{
		{"an object, not a list", string(readFeedFixture(t, "feed_malformed.json")), "2026-09-10", "the feed did not answer a list of prices"},
		{"not JSON", "<html>Not here</html>", "2026-09-10", "the feed did not answer a list of prices"},
		{"null", "null", "2026-09-10", "the feed did not answer a list of prices"},
		{"an empty list", "[]", "2026-09-10", "the feed answered no prices for the day"},
		{"an hour missing", string(readFeedFixture(t, "feed_2026-09-10_NO1_gap.json")), "2026-09-10", "the day's intervals are not contiguous"},
		{"starting an hour late", string(readFeedFixture(t, "feed_2026-09-10_NO1_late.json")), "2026-09-10", "the day's first interval does not start at its midnight"},
		{"another day's file", plain, "2026-09-11", "the day's first interval does not start at its midnight"},
		{"ending an hour early", withoutLast, "2026-09-10", "the day's last interval does not end at the next midnight"},
		{"a time without an offset", `[{"NOK_per_kWh":1,"time_start":"2026-09-10T00:00:00","time_end":"2026-09-11T00:00:00+02:00"}]`, "2026-09-10",
			"an interval's time is not an RFC 3339 time with an offset"},
		{"an interval ending as it starts", `[{"NOK_per_kWh":1,"time_start":"2026-09-10T00:00:00+02:00","time_end":"2026-09-10T00:00:00+02:00"}]`, "2026-09-10",
			"an interval does not end after it starts"},
		{"no NOK price", `[{"EUR_per_kWh":0.1,"time_start":"2026-09-10T00:00:00+02:00","time_end":"2026-09-11T00:00:00+02:00"}]`, "2026-09-10",
			"an interval has no NOK_per_kWh"},
		{"a price the column cannot hold", `[{"NOK_per_kWh":1e7,"time_start":"2026-09-10T00:00:00+02:00","time_end":"2026-09-11T00:00:00+02:00"}]`, "2026-09-10",
			"an interval's price is not a number Vantigo can store"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, failure := energy.ParseSpotFeedDayForTest([]byte(tc.body), "NO1", tc.day)
			if failure != tc.want || len(got) > 0 {
				t.Errorf("parsed %d intervals with %q, want none with %q", len(got), failure, tc.want)
			}
			if strings.Contains(failure, "{") || strings.Contains(failure, "<") {
				t.Errorf("the sentence %q quotes the body", failure)
			}
		})
	}
}
