package invoices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// recorded reads a Norges Bank answer recorded on 2026-11-18
// (testdata/norgesbank).
func recorded(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/norgesbank/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The two traps of the recorded answer to EUR+SEK+JPY (plan C2): the series
// came back as JPY, EUR, SEK — the currency is read through BASE_CUR's
// values, never the request's order — and UNIT_MULT's values are [2, 0], so
// JPY and SEK, at index 0, are per 100 and EUR, at index 1, per one.
func TestParseRates_TheRecordedAnswer(t *testing.T) {
	got, err := parseRates(recorded(t, "three.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"EUR": {"2026-11-12 10.563000", "2026-11-13 10.569500", "2026-11-16 10.574000", "2026-11-17 10.575500"},
		"JPY": {"2026-11-12 0.059691", "2026-11-13 0.059772", "2026-11-16 0.060155", "2026-11-17 0.060414"},
		"SEK": {"2026-11-12 0.954800", "2026-11-13 0.951600", "2026-11-16 0.953000", "2026-11-17 0.951900"},
	}
	if len(got) != len(want) {
		t.Fatalf("currencies = %v, want EUR, JPY and SEK", got)
	}
	for c, rows := range want {
		var have []string
		for _, r := range got[c] {
			have = append(have, r.Day.Format(time.DateOnly)+" "+r.Rate.FloatString(6))
		}
		if !slices.Equal(have, rows) {
			t.Errorf("%s = %v, want %v", c, have, rows)
		}
	}
}

// Every currency the settings may turn on is one Norges Bank answers a
// series of, and the list is sorted for its binary search.
func TestNBCurrencies_AreNorgesBanks(t *testing.T) {
	var doc sdmxData
	if err := json.Unmarshal(recorded(t, "all.json"), &doc); err != nil {
		t.Fatal(err)
	}
	var answered []string
	for _, v := range doc.Data.Structure.Dimensions.Series[1].Values {
		answered = append(answered, v.ID)
	}
	if !slices.IsSorted(nbCurrencies) {
		t.Error("nbCurrencies is not sorted")
	}
	for _, c := range nbCurrencies {
		if !slices.Contains(answered, c) {
			t.Errorf("%s is listed but Norges Bank answered no series of it", c)
		}
	}
	if _, err := parseRates(recorded(t, "all.json")); err != nil {
		t.Errorf("parse every series: %v", err)
	}
}

// fetchFrom runs fetchRates against handler.
func fetchFrom(t *testing.T, handler http.HandlerFunc) (map[string][]dayRate, []string, error) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		handler(w, r)
	}))
	defer srv.Close()
	day := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	got, err := fetchRates(context.Background(), ratesClient(srv.Client().Transport), srv.URL+"/api/data",
		[]string{"EUR", "SEK", "JPY"}, day.AddDate(0, 0, -5), day)
	return got, seen, err
}

func outcomeOf(err error) string {
	var re *ratesError
	if errors.As(err, &re) {
		return re.Outcome
	}
	return ""
}

// The request is the documented one, and never asks detail=dataonly, which
// strips UNIT_MULT.
func TestFetchRates_TheRequest(t *testing.T) {
	_, seen, err := fetchFrom(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(recorded(t, "three.json")) })
	if err != nil {
		t.Fatal(err)
	}
	want := "/api/data/EXR/B.EUR+SEK+JPY.NOK.SP?format=sdmx-json&startPeriod=2026-11-12&endPeriod=2026-11-17&locale=en"
	if len(seen) != 1 || seen[0] != want {
		t.Errorf("requests = %v, want %s", seen, want)
	}
	if strings.Contains(seen[0], "detail") {
		t.Error("the request asks a detail, which strips UNIT_MULT")
	}
}

// No observations is Norges Bank's 404 with its errors body: no rates and no
// error. Any other status is a failure naming it and never the body; an
// answer above 1 MiB is too_large; a redirect is never followed.
func TestFetchRates_Outcomes(t *testing.T) {
	got, _, err := fetchFrom(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(recorded(t, "no-data-404.json"))
	})
	if err != nil || len(got) != 0 {
		t.Errorf("the no-data 404 = %v, %v; want no rates and no error", got, err)
	}
	_, _, err = fetchFrom(t, func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	if outcomeOf(err) != "answered_404" {
		t.Errorf("a 404 of another body = %v, want answered_404", err)
	}
	_, _, err = fetchFrom(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("secret stack trace"))
	})
	if outcomeOf(err) != "answered_500" || strings.Contains(err.Error(), "secret") {
		t.Errorf("a 500 = %v, want answered_500 without the body", err)
	}
	_, _, err = fetchFrom(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte(" "), maxRatesBody+10))
	})
	if outcomeOf(err) != "too_large" {
		t.Errorf("an oversized answer = %v, want too_large", err)
	}
	redirects := 0
	_, seen, err := fetchFrom(t, func(w http.ResponseWriter, r *http.Request) {
		redirects++
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	if outcomeOf(err) != "answered_redirect" || redirects != 1 || len(seen) != 1 {
		t.Errorf("a redirect = %v after %d requests, want answered_redirect and no second request", err, len(seen))
	}
	_, _, err = fetchFrom(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":`)) })
	if outcomeOf(err) != "unreadable" {
		t.Errorf("a broken answer = %v, want unreadable", err)
	}
}

// The publication gate (I18): on a business day nothing is due before 16:30,
// and a weekend or a holiday's latest publication is the business day
// before it.
func TestLatestPublication(t *testing.T) {
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skip("no tzdata")
	}
	for _, c := range []struct{ now, want string }{
		{"2026-11-17 16:29", "2026-11-16 16:30"}, // Tuesday before 16:30: Monday's
		{"2026-11-17 16:30", "2026-11-17 16:30"},
		{"2026-11-21 12:00", "2026-11-20 16:30"}, // Saturday: Friday's
		{"2026-12-25 18:00", "2026-12-24 16:30"}, // Christmas Day
		{"2027-05-17 17:00", "2027-05-14 16:30"}, // Constitution Day, a Monday
	} {
		now, _ := time.ParseInLocation("2006-01-02 15:04", c.now, oslo)
		if got := latestPublication(now).Format("2006-01-02 15:04"); got != c.want {
			t.Errorf("latestPublication(%s) = %s, want %s", c.now, got, c.want)
		}
	}
}

// A currency is due once per publication, and again an hour after a fetch
// that did not bring the day's rate — never twice in an hour.
func TestRateFetchDue(t *testing.T) {
	pub := time.Date(2026, 11, 17, 16, 30, 0, 0, time.UTC)
	now := pub.Add(20 * time.Minute)
	at := func(d time.Duration) *time.Time { v := pub.Add(d); return &v }
	day := func(s string) pgtype.Date {
		d, _ := time.Parse(time.DateOnly, s)
		return pgtype.Date{Time: d, Valid: true}
	}
	for _, c := range []struct {
		name   string
		st     store.InvoicesRateFetch
		latest pgtype.Date
		want   bool
	}{
		{"never fetched", store.InvoicesRateFetch{}, pgtype.Date{}, true},
		{"fetched before the publication", store.InvoicesRateFetch{LastSuccessAt: at(-time.Hour), LastAttemptAt: at(-time.Hour)}, day("2026-11-16"), true},
		{"fetched since, day in", store.InvoicesRateFetch{LastSuccessAt: at(time.Minute), LastAttemptAt: at(time.Minute)}, day("2026-11-17"), false},
		{"fetched since, day not in yet", store.InvoicesRateFetch{LastSuccessAt: at(time.Minute), LastAttemptAt: at(time.Minute)}, day("2026-11-16"), false},
		{"failed ten minutes ago", store.InvoicesRateFetch{LastSuccessAt: at(-24 * time.Hour), LastAttemptAt: at(10 * time.Minute), LastError: ptr("unreachable")}, day("2026-11-16"), false},
	} {
		if got := rateFetchDue(c.st, c.latest, now, pub); got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
	later := pub.Add(2 * time.Hour)
	if !rateFetchDue(store.InvoicesRateFetch{LastSuccessAt: at(time.Minute), LastAttemptAt: at(time.Minute)}, day("2026-11-16"), later, pub) {
		t.Error("an hour after a fetch without the day's rate, the currency is not due again")
	}
}
