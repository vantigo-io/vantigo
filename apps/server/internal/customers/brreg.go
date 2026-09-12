package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// This file is BrregLookupEndpoint.cs (customers inventory §5): the
// module's only outbound HTTP dependency, Brønnøysundregisteret's open
// Enhetsregisteret API. Two divergences from .NET are deliberate, not
// missed corners:
//
//   - no circuit breaker: .NET's AddStandardResilienceHandler wraps one
//     around the retry, but the endpoint is read-only and idempotent, and
//     adding state that outlives one request buys nothing a bounded retry
//     doesn't already give;
//   - no response cache: .NET never cached a lookup either (inventory §5),
//     so this is not a divergence so much as a fact kept true.
//
// The 502 boundary is a genuine divergence, though: .NET's
// GetFromJsonAsync calls EnsureSuccessStatusCode internally, so *any*
// non-success status becomes the same HttpRequestException a real
// transport failure raises, and both end up mapped to 502 alike. This port
// narrows that: only a request that failed to reach the registry at all
// (a dial/read error) or timed out, on every attempt, is "unavailable".
// An upstream status response, 404 included, is decoded like any other;
// zero matches falls out of the same "no _embedded key" handling .NET's
// own null-coalescing already does, and a body that fails to decode is a
// genuine internal error (500 via the generated wrapper), not a sanitized
// 502 — .NET's GetFromJsonAsync would have thrown a JsonException there
// too, which its endpoint's catch does not touch either, so this is not a
// new gap, just an explicit one.

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
// agree with the query builder rather than contradict it.
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
// many times beyond the first attempt, four attempts in total.
const brregRetryAttempts = 3

// brregAttemptTimeout is .NET's per-attempt timeout, fixed at 4s and not
// configurable — only the overall timeout (BRREG_TIMEOUT) is.
const brregAttemptTimeout = 4 * time.Second

// errBrregUnavailable is lookup's answer when every attempt failed at the
// transport level or timed out: the only case this module maps to 502.
var errBrregUnavailable = errors.New("customers: brreg registry unavailable")

// isRetryableStatus reports whether status is worth retrying: a 5xx or a
// 408, the shape .NET's default resilience predicate (IsTransient) retries
// a GET on. Any other status, 404 included, is handed back to the caller
// on the first attempt, never retried and never turned into 502.
func isRetryableStatus(status int) bool {
	return status >= 500 || status == http.StatusRequestTimeout
}

// brregClient is the Enhetsregisteret HTTP client: baseURL and timeout from
// config, transport from Deps.HTTPTransport — nil in production (meaning
// http.DefaultTransport), a fake in every test, so no test in this module
// ever opens a real socket.
type brregClient struct {
	baseURL string
	timeout time.Duration
	client  *http.Client
}

func newBrregClient(baseURL string, timeout time.Duration, transport http.RoundTripper) *brregClient {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &brregClient{baseURL: baseURL, timeout: timeout, client: &http.Client{Transport: transport}}
}

// lookup performs one GET against path, the whole call bounded by
// c.timeout, retrying a failed attempt (a transport error, a timeout, or a
// retryable status) up to brregRetryAttempts further times, each of those
// bounded by brregAttemptTimeout. It returns errBrregUnavailable only once
// every attempt has failed that way; any response the registry actually
// answered with, whatever its status, is returned to the caller to decode.
func (c *brregClient) lookup(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= brregRetryAttempts; attempt++ {
		status, body, err := c.attempt(ctx, path)
		if err != nil {
			lastErr = err
			continue
		}
		if !isRetryableStatus(status) {
			return body, nil
		}
		lastErr = fmt.Errorf("brreg responded %d", status)
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

// GetCustomersLookupBrreg Look up business entities in Brønnøysundregisteret
// (GET /api/v1/customers/lookup/brreg)
func (s *server) GetCustomersLookupBrreg(ctx context.Context, req gen.GetCustomersLookupBrregRequestObject) (gen.GetCustomersLookupBrregResponseObject, error) {
	query, ok := validateBrregLookupQuery(req.Params)
	if !ok {
		return gen.GetCustomersLookupBrreg400ApplicationProblemPlusJSONResponse(problem(
			"Invalid lookup query",
			fmt.Sprintf("Either 'legalId' or a 'search' of at least %d characters must be provided.", brregMinSearchLength),
		)), nil
	}

	body, err := s.brreg.lookup(ctx, query.path())
	if err != nil {
		if errors.Is(err, errBrregUnavailable) {
			s.deps.Logger.WarnContext(ctx, "customers: brreg lookup failed", "path", query.path(), "error", err.Error())
			return gen.GetCustomersLookupBrreg502ApplicationProblemPlusJSONResponse(problemStatus(
				"Lookup service unavailable",
				"The Brønnøysundregisteret lookup service could not be reached. Please try again later.",
				http.StatusBadGateway,
			)), nil
		}
		return nil, fmt.Errorf("customers: brreg lookup: %w", err)
	}

	var parsed brregSearchResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("customers: decode brreg response: %w", err)
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
