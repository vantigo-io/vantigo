package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/brreg"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// This file is BrregLookupEndpoint.cs (customers inventory §5): the
// module's only outbound HTTP dependency, Brønnøysundregisteret's open
// Enhetsregisteret API, through the platform package internal/brreg, which
// holds the client, its retry policy and the entity decoding (accounting
// design R13). Two divergences from .NET are unchanged from the
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
// timeout. Task 16 records this as an explicit .NET-divergence decision;
// internal/brreg keeps it.

const (
	// brregMinSearchLength is BrregLookupEndpoint.MinSearchLength (:15).
	brregMinSearchLength = 2
	// brregMaxResults is BrregLookupEndpoint.MaxResults (:16).
	brregMaxResults = 10
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

// request is the search BrregLookupEndpoint.Handler makes
// (BrregLookupEndpoint.cs:31-33,40): by organisation number for an exact
// lookup, by name for a search, both capped at brregMaxResults.
func (q brregQuery) request() brreg.SearchQuery {
	return brreg.SearchQuery{OrganisationNumber: q.legalID, Name: q.search, Size: brregMaxResults}
}

// brregClient is the module's Enhetsregisteret client: the platform package's
// (internal/brreg), with the feed read the registry feed worker makes through
// it (brreg_feed.go).
type brregClient struct{ *brreg.Client }

// newBrregClient builds it from the config's base URL and timeout, the
// transport from Deps.HTTPTransport and the backoff from Deps.HTTPBackoff —
// nil in production, a fake/zero-delay in every test, so no test in this
// module ever opens a real socket or actually sleeps.
func newBrregClient(baseURL string, timeout time.Duration, transport http.RoundTripper, backoff func(int) time.Duration) *brregClient {
	return &brregClient{brreg.New(baseURL, timeout, transport, backoff)}
}

// entity is the client's entity read (brreg.Client.Entity).
func (c *brregClient) entity(ctx context.Context, orgnr string) (brreg.Entity, brreg.Outcome, error) {
	return c.Entity(ctx, orgnr)
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

	matches, err := s.brreg.Search(ctx, query.request())
	if errors.Is(err, brreg.ErrUndecodable) {
		s.deps.Logger.WarnContext(ctx, "customers: brreg response could not be decoded", "query", query.request(), "error", err.Error())
		return brregUnavailableResponse(), nil
	}
	if err != nil {
		if errors.Is(err, brreg.ErrUnavailable) {
			s.deps.Logger.WarnContext(ctx, "customers: brreg lookup failed", "query", query.request(), "error", err.Error())
			return brregUnavailableResponse(), nil
		}
		return nil, fmt.Errorf("customers: brreg lookup: %w", err)
	}
	data := make([]gen.BrregLookupLookupResult, 0, len(matches))
	for _, m := range matches {
		data = append(data, gen.BrregLookupLookupResult{LegalId: m.OrganisationNumber, LegalName: m.Name})
	}
	return gen.GetCustomersLookupBrreg200JSONResponse{Data: data}, nil
}
