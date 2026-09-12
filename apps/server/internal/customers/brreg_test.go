package customers_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports Integration/LookupEndpointsTests.cs (customers inventory
// §5, §7): all 6 of its tests, plus tests of this port's own explicit
// divergences from .NET — no circuit breaker, no response cache (recorded
// in the spec, not new here) and, genuinely new, a narrower 502 boundary
// (brreg.go's file doc comment) — and the retry count and per-attempt
// timeout, neither of which any .NET test pins by its own attempt count or
// deadline the way the tests below do. No test here opens a real socket:
// fakeBrregTransport stands in for .NET's StubBrregHandler as an
// http.RoundTripper, wired in through modtest.WithTransport.

// fakeBrregTransport stands in for StubBrregHandler
// (Integration/StubBrregHandler.cs): every attempt goes through onRequest,
// guarded by mu so concurrent attempts from the same lookup's retries (and
// parallel subtests, each with their own instance) never race. Its zero
// value serves the same default canned responses StubBrregHandler did.
type fakeBrregTransport struct {
	mu        sync.Mutex
	attempts  int
	onRequest func(*http.Request) (*http.Response, error)
}

func (f *fakeBrregTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.attempts++
	fn := f.onRequest
	f.mu.Unlock()
	if fn != nil {
		return fn(r)
	}
	return defaultBrregResponse(r), nil
}

// Attempts is how many requests this transport has seen so far.
func (f *fakeBrregTransport) Attempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// setOnRequest overrides the response per request, StubBrregHandler.OnRequest's
// role.
func (f *fakeBrregTransport) setOnRequest(fn func(*http.Request) (*http.Response, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onRequest = fn
}

// jsonResponse is a canned application/json response.
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// defaultBrregResponse is StubBrregHandler.DefaultResponse
// (StubBrregHandler.cs:24-53): an exact match for organisasjonsnummer, or
// two fixed Equinor entities for a name search.
func defaultBrregResponse(r *http.Request) *http.Response {
	if orgNumber := r.URL.Query().Get("organisasjonsnummer"); orgNumber != "" {
		return jsonResponse(http.StatusOK, fmt.Sprintf(
			`{"_embedded":{"enheter":[{"organisasjonsnummer":%q,"navn":"STUB ENTITY AS"}]}}`, orgNumber))
	}
	return jsonResponse(http.StatusOK, `{"_embedded":{"enheter":[`+
		`{"organisasjonsnummer":"923609016","navn":"EQUINOR ASA"},`+
		`{"organisasjonsnummer":"914778271","navn":"EQUINOR ENERGY AS"}`+
		`]}}`)
}

type brregLookupResultJSON struct {
	LegalId   string `json:"legalId"`
	LegalName string `json:"legalName"`
}

type brregLookupResponseJSON struct {
	Data []brregLookupResultJSON `json:"data"`
}

// Ported from Integration/LookupEndpointsTests.cs.
// BrregLookup_RetriesTransientUpstreamFailures.
func TestBrregLookup_RetriesTransientUpstreamFailures(t *testing.T) {
	t.Parallel()
	transport := &fakeBrregTransport{}
	transport.setOnRequest(func(r *http.Request) (*http.Response, error) {
		if transport.Attempts() <= 2 {
			return jsonResponse(http.StatusServiceUnavailable, ""), nil
		}
		return defaultBrregResponse(r), nil
	})
	h := newHarness(t, modtest.WithTransport(transport))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?search=equinor", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var body brregLookupResponseJSON
	r.JSON(&body)
	if len(body.Data) != 2 {
		t.Errorf("len(Data) = %d, want 2", len(body.Data))
	}
	if got := transport.Attempts(); got != 3 {
		t.Errorf("attempts = %d, want 3 (2 failures then a success)", got)
	}
}

// Ported from Integration/LookupEndpointsTests.cs.
// BrregLookup_BySearch_ReturnsMappedEntities.
func TestBrregLookup_BySearch_ReturnsMappedEntities(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithTransport(&fakeBrregTransport{}))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?search=equinor", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var body brregLookupResponseJSON
	r.JSON(&body)
	want := []brregLookupResultJSON{
		{LegalId: "923609016", LegalName: "EQUINOR ASA"},
		{LegalId: "914778271", LegalName: "EQUINOR ENERGY AS"},
	}
	if !slices.Equal(body.Data, want) {
		t.Errorf("Data = %+v, want %+v", body.Data, want)
	}
}

// Ported from Integration/LookupEndpointsTests.cs.
// BrregLookup_ByLegalId_ReturnsExactMatch.
func TestBrregLookup_ByLegalId_ReturnsExactMatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithTransport(&fakeBrregTransport{}))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?legalId=923609016", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var body brregLookupResponseJSON
	r.JSON(&body)
	want := []brregLookupResultJSON{{LegalId: "923609016", LegalName: "STUB ENTITY AS"}}
	if !slices.Equal(body.Data, want) {
		t.Errorf("Data = %+v, want %+v", body.Data, want)
	}
}

// Ported from Integration/LookupEndpointsTests.cs.
// BrregLookup_WhenRegistryReturnsNoMatches_ReturnsEmptyList.
func TestBrregLookup_WhenRegistryReturnsNoMatches_ReturnsEmptyList(t *testing.T) {
	t.Parallel()
	transport := &fakeBrregTransport{}
	transport.setOnRequest(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{}`), nil
	})
	h := newHarness(t, modtest.WithTransport(transport))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?search=no-such-entity", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var body brregLookupResponseJSON
	r.JSON(&body)
	if len(body.Data) != 0 {
		t.Errorf("Data = %+v, want empty", body.Data)
	}
}

// Ported from Integration/LookupEndpointsTests.cs.
// BrregLookup_WithInvalidQuery_ReturnsBadRequest.
func TestBrregLookup_WithInvalidQuery_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	for _, qs := range []string{"", "search=", "search=a", "legalId=%20"} {
		t.Run(qs, func(t *testing.T) {
			t.Parallel()
			var called atomic.Bool
			transport := &fakeBrregTransport{}
			transport.setOnRequest(func(*http.Request) (*http.Response, error) {
				called.Store(true)
				return jsonResponse(http.StatusOK, `{}`), nil
			})
			h := newHarness(t, modtest.WithTransport(transport))
			c := h.SignIn(t, "customers:lookup-view")

			r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?"+qs, nil)
			if r.Status != http.StatusBadRequest {
				t.Errorf("status %d body %s, want 400", r.Status, r.Body)
			}
			if called.Load() {
				t.Error("brreg: the client was reached for an invalid query, want it rejected before any fetch")
			}
		})
	}
}

// TestBrregLookup_InvalidQuery_ProblemTextIsExact asserts
// BrregLookupEndpoint.Validate's exact title and detail
// (BrregLookupEndpoint.cs:81-84), byte for byte.
func TestBrregLookup_InvalidQuery_ProblemTextIsExact(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithTransport(&fakeBrregTransport{}))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	r.JSON(&problem)
	if problem.Title != "Invalid lookup query" {
		t.Errorf("title = %q, want %q", problem.Title, "Invalid lookup query")
	}
	if want := "Either 'legalId' or a 'search' of at least 2 characters must be provided."; problem.Detail != want {
		t.Errorf("detail = %q, want %q", problem.Detail, want)
	}
}

// Ported from Integration/LookupEndpointsTests.cs.
// BrregLookup_WhenRegistryIsUnavailable_ReturnsBadGateway.
func TestBrregLookup_WhenRegistryIsUnavailable_ReturnsBadGateway(t *testing.T) {
	t.Parallel()
	transport := &fakeBrregTransport{}
	transport.setOnRequest(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})
	h := newHarness(t, modtest.WithTransport(transport))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?search=equinor", nil)
	if r.Status != http.StatusBadGateway {
		t.Fatalf("status %d body %s, want 502", r.Status, r.Body)
	}
	var problem struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	r.JSON(&problem)
	if problem.Title != "Lookup service unavailable" {
		t.Errorf("title = %q, want %q", problem.Title, "Lookup service unavailable")
	}
	if want := "The Brønnøysundregisteret lookup service could not be reached. Please try again later."; problem.Detail != want {
		t.Errorf("detail = %q, want %q", problem.Detail, want)
	}
}

// This section is not a port: it pins the retry count, the per-attempt
// timeout and the exact 502 boundary (brreg.go's file doc comment), none of
// which any .NET test measures directly — LookupEndpointsTests.cs only
// proves a transient failure is survived and a persistent one becomes 502,
// never how many attempts either takes or what "transient" excludes.

// TestBrregLookup_ExhaustsRetriesThenReturnsBadGateway pins the exact
// attempt count on total exhaustion: the initial attempt plus
// brregRetryAttempts retries, four in total, every one of them a retryable
// 503. A mutation that dropped a retry, added one, or stopped retrying 5xx
// status responses (as opposed to only transport errors) would change this
// count without failing any other test in this file.
func TestBrregLookup_ExhaustsRetriesThenReturnsBadGateway(t *testing.T) {
	t.Parallel()
	transport := &fakeBrregTransport{}
	transport.setOnRequest(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusServiceUnavailable, ""), nil
	})
	h := newHarness(t, modtest.WithTransport(transport))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?search=equinor", nil)
	if r.Status != http.StatusBadGateway {
		t.Fatalf("status %d body %s, want 502", r.Status, r.Body)
	}
	if got := transport.Attempts(); got != 4 {
		t.Errorf("attempts = %d, want 4 (the initial attempt plus 3 retries, every one exhausted)", got)
	}
}

// TestBrregLookup_PerAttemptTimeoutIsFourSeconds pins the per-attempt
// deadline without ever waiting for it: the fake transport reads the
// deadline off the request context it receives and the test asserts it is
// close to 4s away, not clamped down to (or stretched past) the overall
// BRREG_TIMEOUT.
func TestBrregLookup_PerAttemptTimeoutIsFourSeconds(t *testing.T) {
	t.Parallel()
	var remaining time.Duration
	transport := &fakeBrregTransport{}
	transport.setOnRequest(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("brreg: the attempt's request context carries no deadline")
			return defaultBrregResponse(r), nil
		}
		remaining = time.Until(deadline)
		return defaultBrregResponse(r), nil
	})
	h := newHarness(t, modtest.WithTransport(transport))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?search=equinor", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if remaining <= 3*time.Second || remaining > 4*time.Second {
		t.Errorf("per-attempt deadline had %v left, want (3s, 4s]", remaining)
	}
}

// TestBrregLookup_UpstreamNotFoundIsNotBadGateway pins the narrowed 502
// boundary this port deliberately diverges on (brreg.go's file doc
// comment): a 404 is neither retried nor turned into 502, unlike .NET
// where GetFromJsonAsync's EnsureSuccessStatusCode would have raised the
// same HttpRequestException a genuine transport failure does.
func TestBrregLookup_UpstreamNotFoundIsNotBadGateway(t *testing.T) {
	t.Parallel()
	transport := &fakeBrregTransport{}
	transport.setOnRequest(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNotFound, `{"error":"not found"}`), nil
	})
	h := newHarness(t, modtest.WithTransport(transport))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?search=equinor", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (a non-retryable upstream status is decoded like any other)", r.Status, r.Body)
	}
	var body brregLookupResponseJSON
	r.JSON(&body)
	if len(body.Data) != 0 {
		t.Errorf("Data = %+v, want empty (the 404 body carries no _embedded)", body.Data)
	}
	if got := transport.Attempts(); got != 1 {
		t.Errorf("attempts = %d, want 1 (a 404 is not retried)", got)
	}
}

// TestBrregLookup_MalformedBodyIsNotBadGateway pins the other half of the
// narrowed boundary: a body that fails to decode is a genuine internal
// error (500 via the generated wrapper's default error handling), never a
// sanitized 502 — .NET's GetFromJsonAsync would have raised a JsonException
// there too, which BrregLookupEndpoint's catch does not touch either
// (it only catches HttpRequestException/TaskCanceledException), so this is
// not a new gap, just an explicit one.
func TestBrregLookup_MalformedBodyIsNotBadGateway(t *testing.T) {
	t.Parallel()
	transport := &fakeBrregTransport{}
	transport.setOnRequest(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, "not json"), nil
	})
	h := newHarness(t, modtest.WithTransport(transport))
	c := h.SignIn(t, "customers:lookup-view")

	r := c.Do(http.MethodGet, "/api/v1/customers/lookup/brreg?search=equinor", nil,
		modtest.SkipContract("a malformed upstream body forces the generic internal-error path, 500, which is not in this operation's documented response set"))
	if r.Status != http.StatusInternalServerError {
		t.Errorf("status %d body %s, want 500 (an internal error, not a sanitized 502)", r.Status, r.Body)
	}
}
