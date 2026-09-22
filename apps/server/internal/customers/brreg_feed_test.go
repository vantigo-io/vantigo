package customers_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the Brreg client's third operation (registry workers design D1):
// GET /enhetsregisteret/api/oppdateringer/enheter, the incremental update
// feed the feed worker walks. Every body here was recorded from the live API
// on 2026-09-22 and trimmed to the fields this module reads; no test opens a
// socket — modtest.WithTransport is the seam, as brreg_test.go's own fake is.

// feedTransport answers by the request's full URL (path plus query), because
// the cursor a test is asserting on lives in the query string and
// registryTransport records only the path.
type feedTransport struct {
	mu      sync.Mutex
	urls    []string
	respond func(url string) (*http.Response, error)
}

// RoundTrip calls respond unlocked, after f.mu has recorded the request: a
// single (*brregClient).updates call drives its retries one attempt at a
// time from one goroutine, so a test's own closure state (a plain counter,
// say) needs no locking of its own as long as only one updates call is in
// flight against this transport at once — the case every test in this file
// is in, t.Parallel() notwithstanding, since that parallelism is across
// tests, each with its own transport.
func (f *feedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.urls = append(f.urls, r.URL.RequestURI())
	respond := f.respond
	f.mu.Unlock()
	return respond(r.URL.RequestURI())
}

func (f *feedTransport) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

// feedPageBody is one recorded page: three entries, ascending
// oppdateringsid with the registry's own gaps, one of each interesting
// endringstype. The bodies here answer plain application/json — the live feed's
// own content type, not the entity endpoint's pinned v2 media type, since the
// feed negotiates nothing — which is exactly what brreg_test.go's jsonResponse
// already writes, so these tests use it directly.
const feedPageBody = `{
	"_embedded": {"oppdaterteEnheter": [
		{"oppdateringsid": 25255241, "dato": "2026-09-14T04:05:11.193Z", "organisasjonsnummer": "929745760", "endringstype": "Endring",
		 "_links": {"enhet": {"href": "https://data.brreg.no/enhetsregisteret/api/enheter/929745760"}}},
		{"oppdateringsid": 25255244, "dato": "2026-09-14T04:06:02.001Z", "organisasjonsnummer": "923609016", "endringstype": "Ny",
		 "_links": {"enhet": {"href": "https://data.brreg.no/enhetsregisteret/api/enheter/923609016"}}},
		{"oppdateringsid": 25255250, "dato": "2026-09-14T04:07:44.500Z", "organisasjonsnummer": "974760673", "endringstype": "Fjernet"}
	]},
	"_links": {"self": {"href": "https://data.brreg.no/enhetsregisteret/api/oppdateringer/enheter?oppdateringsid=25255241&size=1000"}},
	"page": {"size": 1000, "totalElements": 3, "totalPages": 1, "number": 0}
}`

// emptyFeedBody is what the live API answers when nothing matched: no
// _embedded key at all, and page.totalElements 0. Decoding this as "zero
// entries" rather than as a malformed body is the one thing a reasonable
// implementer gets wrong here.
const emptyFeedBody = `{
	"_links": {"self": {"href": "https://data.brreg.no/enhetsregisteret/api/oppdateringer/enheter?dato=2026-09-22T00:00:00.000Z&size=1000"}},
	"page": {"size": 1000, "totalElements": 0, "totalPages": 0, "number": 0}
}`

func newFeedHarness(t *testing.T, transport http.RoundTripper) *modtest.Harness {
	t.Helper()
	return newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
}

// TestBrregFeed_AsksByDateUntilThereIsACursor pins the bootstrap request
// (design D1): with no stored oppdateringsid the feed is joined at a moment —
// never from the beginning of time — as ?dato=<started_at> in the registry's
// own millisecond-and-Z format, with the page size this module asks for.
func TestBrregFeed_AsksByDateUntilThereIsACursor(t *testing.T) {
	t.Parallel()
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		return jsonResponse(http.StatusOK, emptyFeedBody), nil
	}}
	h := newFeedHarness(t, transport)

	since := time.Date(2026, 9, 22, 6, 30, 0, 0, time.UTC)
	entries, err := customers.FeedPageForTest(context.Background(), h.Deps(), nil, since, 1000)
	if err != nil {
		t.Fatalf("updates: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want none: an empty result has no _embedded at all", entries)
	}
	want := "/enhetsregisteret/api/oppdateringer/enheter?dato=2026-09-22T06%3A30%3A00.000Z&size=1000"
	if got := transport.requests(); len(got) != 1 || got[0] != want {
		t.Errorf("requests = %v, want exactly [%s]", got, want)
	}
}

// TestBrregFeed_AsksByCursorOnceThereIsOne pins the steady-state request and
// every field of a recorded page: ascending ids with the registry's gaps, the
// dato as an instant, and every endringstype carried through verbatim (the
// worker decides what they mean, the client does not).
func TestBrregFeed_AsksByCursorOnceThereIsOne(t *testing.T) {
	t.Parallel()
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		return jsonResponse(http.StatusOK, feedPageBody), nil
	}}
	h := newFeedHarness(t, transport)

	cursor := int64(25255241)
	entries, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor, time.Time{}, 1000)
	if err != nil {
		t.Fatalf("updates: %v", err)
	}
	want := "/enhetsregisteret/api/oppdateringer/enheter?oppdateringsid=25255241&size=1000"
	if got := transport.requests(); len(got) != 1 || got[0] != want {
		t.Errorf("requests = %v, want exactly [%s]", got, want)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[0].UpdateID != 25255241 || entries[0].OrganisationNumber != "929745760" || entries[0].ChangeType != "Endring" {
		t.Errorf("entries[0] = %+v, want 25255241/929745760/Endring", entries[0])
	}
	if got := entries[0].Date.UTC(); !got.Equal(time.Date(2026, 9, 14, 4, 5, 11, 193000000, time.UTC)) {
		t.Errorf("entries[0].Date = %v, want 2026-09-14T04:05:11.193Z", got)
	}
	if entries[1].ChangeType != "Ny" || entries[2].ChangeType != "Fjernet" {
		t.Errorf("change types = %q/%q, want Ny/Fjernet", entries[1].ChangeType, entries[2].ChangeType)
	}
	if entries[2].UpdateID != 25255250 {
		t.Errorf("entries[2].UpdateID = %d, want 25255250 (the ids have gaps)", entries[2].UpdateID)
	}
}

// TestBrregFeed_TheCursorWinsOverTheDate pins the one-or-the-other rule: a
// stored cursor is exact, a date is only a starting point, so a request never
// carries both — sending both would let the registry pick which one it honours.
func TestBrregFeed_TheCursorWinsOverTheDate(t *testing.T) {
	t.Parallel()
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		return jsonResponse(http.StatusOK, emptyFeedBody), nil
	}}
	h := newFeedHarness(t, transport)

	cursor := int64(42)
	if _, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor,
		time.Date(2026, 9, 22, 6, 30, 0, 0, time.UTC), 1000); err != nil {
		t.Fatalf("updates: %v", err)
	}
	got := transport.requests()
	if len(got) != 1 || strings.Contains(got[0], "dato=") || !strings.Contains(got[0], "oppdateringsid=42") {
		t.Errorf("request = %v, want oppdateringsid=42 and no dato", got)
	}
}

// TestBrregFeed_ZeroSizeDefaultsToRegistryFeedPageSize pins feedCursor.path's
// fallback: a caller that passes 0 (the zero value, not an explicit choice)
// gets registryFeedPageSize on the wire, not a literal size=0 the registry
// would either reject or misread as "no page size given".
func TestBrregFeed_ZeroSizeDefaultsToRegistryFeedPageSize(t *testing.T) {
	t.Parallel()
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		return jsonResponse(http.StatusOK, emptyFeedBody), nil
	}}
	h := newFeedHarness(t, transport)

	cursor := int64(1)
	if _, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor, time.Time{}, 0); err != nil {
		t.Fatalf("updates: %v", err)
	}
	want := "/enhetsregisteret/api/oppdateringer/enheter?oppdateringsid=1&size=1000"
	if got := transport.requests(); len(got) != 1 || got[0] != want {
		t.Errorf("requests = %v, want exactly [%s]", got, want)
	}
}

// TestBrregFeed_RetriesAServerErrorAndThenSucceeds pins that the feed reuses
// the search's and the entity read's own retry policy (brreg.go): a 5xx is
// worth another attempt, and the backoff seam keeps the test instant.
func TestBrregFeed_RetriesAServerErrorAndThenSucceeds(t *testing.T) {
	t.Parallel()
	var n int
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		n++
		if n == 1 {
			return jsonResponse(http.StatusInternalServerError, `{}`), nil
		}
		return jsonResponse(http.StatusOK, feedPageBody), nil
	}}
	h := newFeedHarness(t, transport)

	cursor := int64(1)
	entries, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor, time.Time{}, 1000)
	if err != nil {
		t.Fatalf("updates: %v", err)
	}
	if len(entries) != 3 || n != 2 {
		t.Errorf("entries = %d after %d attempts, want 3 after 2", len(entries), n)
	}
}

// TestBrregFeed_ReportsEveryUnusableAnswerAsUnavailable pins the error kinds
// one table at a time. Every one of them is "the feed could not be read", not
// "there are no updates": the worker must leave its cursor exactly where it
// was for all of them, which it can only do if none of them look like success.
func TestBrregFeed_ReportsEveryUnusableAnswerAsUnavailable(t *testing.T) {
	t.Parallel()
	oversized := `{"_embedded":{"oppdaterteEnheter":[` +
		strings.Repeat(`{"oppdateringsid":1,"dato":"2026-09-14T04:05:11.193Z","organisasjonsnummer":"923609016","endringstype":"Ny"},`, 40000) +
		`{"oppdateringsid":2,"dato":"2026-09-14T04:05:11.193Z","organisasjonsnummer":"923609016","endringstype":"Ny"}]}}`

	cases := []struct {
		name        string
		status      int
		body        string
		contentType string
	}{
		{name: "a bad request the registry never retries away", status: http.StatusBadRequest, body: `{"melding":"oppdateringsid er ugyldig"}`},
		{name: "an exhausted server error", status: http.StatusInternalServerError, body: `{}`},
		{name: "an HTML error page from a proxy", status: http.StatusOK, body: `<html>gateway</html>`, contentType: "text/html"},
		{name: "a body that will not decode", status: http.StatusOK, body: `{"_embedded":{"oppdaterteEnheter":[`},
		{name: "a malformed dato", status: http.StatusOK, body: `{"_embedded":{"oppdaterteEnheter":[{"oppdateringsid":1,"dato":"yesterday","organisasjonsnummer":"923609016","endringstype":"Ny"}]}}`},
		{name: "a body past the cap", status: http.StatusOK, body: oversized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transport := &feedTransport{respond: func(string) (*http.Response, error) {
				resp := jsonResponse(tc.status, tc.body)
				if tc.contentType != "" {
					resp.Header.Set("Content-Type", tc.contentType)
				}
				return resp, nil
			}}
			h := newFeedHarness(t, transport)
			cursor := int64(1)
			if _, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor, time.Time{}, 1000); err == nil {
				t.Fatal("updates succeeded, want an error")
			}
		})
	}
}
