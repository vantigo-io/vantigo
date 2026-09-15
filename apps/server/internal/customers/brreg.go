package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// This file is BrregLookupEndpoint.cs (customers inventory §5): the
// module's only outbound HTTP dependency, Brønnøysundregisteret's open
// Enhetsregisteret API. Two divergences from .NET are unchanged from the
// first pass, still deliberate, not missed corners:
//
//   - no circuit breaker: .NET's AddStandardResilienceHandler wraps one
//     around the retry, but the endpoint is read-only and idempotent, and
//     adding state that outlives one request buys nothing a bounded retry
//     doesn't already give;
//   - no response cache: .NET never cached a lookup either (inventory §5),
//     so this is not a divergence so much as a fact kept true.
//
// The 502 boundary actually matches .NET, corrected from a first pass that
// got it backwards: .NET's GetFromJsonAsync calls EnsureSuccessStatusCode
// internally (BrregLookupEndpoint.cs:39-41), so *any* non-2xx status raises
// the same HttpRequestException a genuine transport failure does, and the
// endpoint's catch (:56, HttpRequestException/TaskCanceledException) maps
// both to 502 alike (:62-65). So does this port: a 4xx (404 included) is
// never retried but is still "unavailable", the same as an exhausted 5xx or
// a transport error. Only a successful 2xx whose body has no "_embedded"
// key is different — that is zero matches, not a failure (§5:43), and stays
// an empty list.
//
// A malformed body on an otherwise-successful response is the one place
// this port deliberately does NOT follow .NET: there, GetFromJsonAsync's
// JsonException is not HttpRequestException/TaskCanceledException, so it is
// never caught by the endpoint and propagates as an unhandled 500. This
// port answers 502 instead, on purpose: the contract documents 502 for an
// upstream failure on this operation and never documents 500 for it; an
// external service answering with unparsable data is an upstream failure
// by any honest reading; and a 500 here would have forced tests to skip
// contract validation for this whole operation, not just this one case.
//
// The retry backoff (~200ms base, full jitter, so 200/400/800ms before the
// three retries and ~1.4s of sleep at worst) is also a deliberate
// divergence: .NET's Polly default base is ~2s, which alone could sleep
// ~14s across four attempts — more than fits inside §5's own 15s total
// timeout. Task 16 records this as an explicit .NET-divergence decision.
// brregBackoffCap is a guard on the doubling, not a figure this attempt
// count reaches: see its own comment.

const (
	// brregMinSearchLength is BrregLookupEndpoint.MinSearchLength (:15).
	brregMinSearchLength = 2
	// brregMaxResults is BrregLookupEndpoint.MaxResults (:16).
	brregMaxResults = 10
	// brregPath is the Enhetsregisteret search endpoint
	// (BrregLookupEndpoint.cs:40).
	brregPath = "/enhetsregisteret/api/enheter"
)

// brregQuery is one validated lookup request: exactly one of legalID or
// search is set.
type brregQuery struct {
	legalID string
	search  string
}

// validateBrregLookupQuery is BrregLookupEndpoint.Validate
// (BrregLookupEndpoint.cs:69-85): legalId wins whenever it is present and
// non-blank, regardless of whether search is also given — the same order
// the query-building switch tests in (:31-33) — otherwise search must be
// at least brregMinSearchLength characters after trimming; anything else
// is invalid. No .NET test exercises legalId and search both present, so
// this ordering for that combination is this port's own call, chosen to
// agree with the query builder rather than contradict it (pinned by
// TestBrregLookup_LegalIdWinsOverSearchWhenBothGiven, brreg_test.go).
func validateBrregLookupQuery(params gen.GetCustomersLookupBrregParams) (brregQuery, bool) {
	if params.LegalId != nil {
		if trimmed := strings.TrimSpace(*params.LegalId); trimmed != "" {
			return brregQuery{legalID: trimmed}, true
		}
	}
	if params.Search != nil {
		if trimmed := strings.TrimSpace(*params.Search); len(trimmed) >= brregMinSearchLength {
			return brregQuery{search: trimmed}, true
		}
	}
	return brregQuery{}, false
}

// path is the request path BrregLookupEndpoint.Handler builds
// (BrregLookupEndpoint.cs:31-33,40): organisasjonsnummer for an exact
// lookup, navn for a name search, both capped at brregMaxResults.
func (q brregQuery) path() string {
	v := url.Values{}
	if q.legalID != "" {
		v.Set("organisasjonsnummer", q.legalID)
	} else {
		v.Set("navn", q.search)
	}
	v.Set("size", strconv.Itoa(brregMaxResults))
	return brregPath + "?" + v.Encode()
}

// The subset of the Enhetsregisteret search response this module relies on
// (BrregLookupEndpoint.cs:107-117). A response with no "_embedded" key
// (zero matches) decodes to a nil Embedded, not an error.
type brregSearchResult struct {
	Embedded *brregEmbedded `json:"_embedded"`
}

type brregEmbedded struct {
	Entities []brregEntity `json:"enheter"`
}

type brregEntity struct {
	OrganisationNumber string `json:"organisasjonsnummer"`
	Name               string `json:"navn"`
}

// brregRetryAttempts is .NET's AddStandardResilienceHandler default
// MaxRetryAttempts (customers inventory §5): a failed GET is retried this
// many times beyond the first attempt, four attempts in total. Ruled
// correct as-is in review: ".NET's Polly MaxRetryAttempts: 3 convention
// (retries excluding the initial attempt)."
const brregRetryAttempts = 3

// brregAttemptTimeout is .NET's per-attempt timeout, fixed at 4s and not
// configurable — only the overall timeout (BRREG_TIMEOUT) is.
const brregAttemptTimeout = 4 * time.Second

// brregBackoffBase and brregBackoffCap bound brregBackoff: exponential with
// full jitter from a 200ms base, so the worst-case total delay across three
// retries (200+400+800ms, before jitter shrinks each toward 0) is about
// 1.4s — this file's doc comment explains why that is shorter than .NET's
// own default.
//
// The cap never binds at brregRetryAttempts = 3: the largest pre-jitter
// delay the loop can ask for is 800ms, so the 1s cap is a guard on the
// doubling should the attempt count ever rise (attempt 4 would want 1.6s),
// not a figure this configuration produces. Read it as a ceiling on future
// growth rather than as today's maximum.
const (
	brregBackoffBase = 200 * time.Millisecond
	brregBackoffCap  = 1 * time.Second
)

// brregBackoff is the production backoff before retry attempt n (1-indexed:
// the wait before the second overall attempt is brregBackoff(1)):
// exponential with full jitter — a uniformly random duration in
// [0, min(base*2^(n-1), cap)]. Deps.HTTPBackoff overrides this in tests, so
// a retry loop's tests never actually sleep.
func brregBackoff(attempt int) time.Duration {
	shift := attempt - 1
	d := brregBackoffBase * time.Duration(int64(1)<<uint(shift))
	if d <= 0 || d > brregBackoffCap {
		d = brregBackoffCap
	}
	return time.Duration(rand.Int64N(int64(d) + 1))
}

// waitBackoff blocks for d, or until ctx ends first, reporting whether the
// wait completed (false means ctx ended it — the caller should stop
// retrying rather than fire an attempt already doomed to fail on a
// cancelled or expired context).
func waitBackoff(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// errBrregUnavailable is lookup's answer once every attempt has failed: a
// transport error or timeout on any attempt, or a non-2xx status on the
// last one (matching .NET's EnsureSuccessStatusCode, this file's doc
// comment). A malformed body on a 2xx response is handled by the caller,
// not here — lookup itself only ever fails this way for what the registry
// never successfully answered at all.
var errBrregUnavailable = errors.New("customers: brreg registry unavailable")

// isSuccessStatus reports a 2xx: the only status lookup treats as an actual
// answer to decode.
func isSuccessStatus(status int) bool {
	return status >= 200 && status < 300
}

// isRetryableStatus reports whether status is worth retrying rather than
// failing immediately: a 5xx or a 408, the shape .NET's default resilience
// predicate (IsTransient) retries a GET on. Any other non-2xx status, 404
// included, stops the loop on the spot — still ultimately unavailable
// (isSuccessStatus is false for it too), just not worth spending the
// remaining attempts on.
func isRetryableStatus(status int) bool {
	return status >= 500 || status == http.StatusRequestTimeout
}

// brregClient is the Enhetsregisteret HTTP client: baseURL and timeout from
// config, transport from Deps.HTTPTransport and backoff from
// Deps.HTTPBackoff — nil in production (meaning http.DefaultTransport and
// brregBackoff respectively), a fake/zero-delay in every test, so no test
// in this module ever opens a real socket or actually sleeps.
type brregClient struct {
	baseURL string
	timeout time.Duration
	client  *http.Client
	backoff func(attempt int) time.Duration
}

func newBrregClient(baseURL string, timeout time.Duration, transport http.RoundTripper, backoff func(int) time.Duration) *brregClient {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if backoff == nil {
		backoff = brregBackoff
	}
	return &brregClient{
		baseURL: baseURL,
		timeout: timeout,
		// Redirects are never followed: a 3xx is returned to lookup as-is,
		// which treats it as the non-2xx it is. The base URL is operator
		// configuration and validated, so a redirect to an arbitrary host is
		// not reachable today — but following one would send this request
		// (and Go would re-send it up to ten times) to a host no operator
		// named, which is not a thing a registry lookup should ever do.
		client:  &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		backoff: backoff,
	}
}

// lookup performs one GET against path, the whole call bounded by
// c.timeout, retrying a failed attempt (a transport error, a timeout, or a
// retryable status) up to brregRetryAttempts further times, each of those
// bounded by brregAttemptTimeout and preceded by c.backoff's delay. It
// returns errBrregUnavailable once every attempt has failed, or the first
// attempt answered with a non-retryable non-2xx status; a successful 2xx
// response's body, whatever it decodes to, is returned to the caller.
func (c *brregClient) lookup(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= brregRetryAttempts; attempt++ {
		if attempt > 0 {
			if !waitBackoff(ctx, c.backoff(attempt)) {
				break
			}
		}
		status, body, err := c.attempt(ctx, path)
		if err != nil {
			lastErr = err
			continue
		}
		if isSuccessStatus(status) {
			return body, nil
		}
		lastErr = fmt.Errorf("brreg responded %d", status)
		if !isRetryableStatus(status) {
			break
		}
	}
	return nil, fmt.Errorf("%w: %w", errBrregUnavailable, lastErr)
}

// attempt performs one GET, bounded by brregAttemptTimeout (itself bounded
// by ctx's own deadline, the overall timeout lookup already applied). The
// body is read to completion before returning, so a canceled attempt
// context can never surface as a read error on a response the caller
// still needs.
func (c *brregClient) attempt(ctx context.Context, path string) (int, []byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, brregAttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// brregUnavailableResponse is the 502 both of GetCustomersLookupBrreg's
// failure paths answer: an exhausted/unretryable upstream (lookup's own
// error) and a 2xx response whose body will not decode (this port's
// deliberate 500->502 divergence, this file's doc comment). Sharing one
// builder keeps the two paths' problem text identical, which they must be —
// a caller cannot distinguish "never got an answer" from "got a nonsense
// one" and should not need to.
func brregUnavailableResponse() gen.GetCustomersLookupBrreg502ApplicationProblemPlusJSONResponse {
	return gen.GetCustomersLookupBrreg502ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus(
		"Lookup service unavailable",
		"The Brønnøysundregisteret lookup service could not be reached. Please try again later.",
		http.StatusBadGateway,
	))
}

// GetCustomersLookupBrreg Look up business entities in Brønnøysundregisteret
// (GET /api/v1/customers/lookup/brreg)
func (s *server) GetCustomersLookupBrreg(ctx context.Context, req gen.GetCustomersLookupBrregRequestObject) (gen.GetCustomersLookupBrregResponseObject, error) {
	query, ok := validateBrregLookupQuery(req.Params)
	if !ok {
		return gen.GetCustomersLookupBrreg400ApplicationProblemPlusJSONResponse(apicommon.Problem(
			"Invalid lookup query",
			fmt.Sprintf("Either 'legalId' or a 'search' of at least %d characters must be provided.", brregMinSearchLength),
		)), nil
	}

	body, err := s.brreg.lookup(ctx, query.path())
	if err != nil {
		if errors.Is(err, errBrregUnavailable) {
			s.deps.Logger.WarnContext(ctx, "customers: brreg lookup failed", "path", query.path(), "error", err.Error())
			return brregUnavailableResponse(), nil
		}
		return nil, fmt.Errorf("customers: brreg lookup: %w", err)
	}

	var parsed brregSearchResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		s.deps.Logger.WarnContext(ctx, "customers: brreg response could not be decoded", "path", query.path(), "error", err.Error())
		return brregUnavailableResponse(), nil
	}

	var entities []brregEntity
	if parsed.Embedded != nil {
		entities = parsed.Embedded.Entities
	}
	data := make([]gen.BrregLookupLookupResult, 0, len(entities))
	for _, e := range entities {
		data = append(data, gen.BrregLookupLookupResult{LegalId: e.OrganisationNumber, LegalName: e.Name})
	}
	return gen.GetCustomersLookupBrreg200JSONResponse{Data: data}, nil
}
