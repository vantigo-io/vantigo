// Package brreg is the client of Brønnøysundregisteret's open
// Enhetsregisteret API: the search by name or organisation number, one
// entity's full record, and the incremental update feed. It is a platform
// package (module boundaries rule 1): it knows no module, stores nothing and
// speaks only plain Go types, so Customers, which keeps the registry records
// and the feed worker, and Accounting, which looks suppliers up, call the same
// client and map its answers onto their own types.
//
// It was Customers' own client, moved here so the Accounting module can look
// suppliers up too (accounting design A17, R13), and is that client unchanged: the same
// requests, the same retry policy, the same caps and the same errors.
//
// The retry policy, shared by every read: a GET is bounded overall by the
// client's timeout and per attempt by AttemptTimeout, and a transport error, a
// timeout or a retryable status (a 5xx or a 408) is retried up to three more
// times after a jittered exponential backoff from 200 ms. That is shorter than
// .NET's Polly default (~2 s), which alone could sleep longer than the 15 s
// overall timeout. Any other non-2xx status ends the loop at once. Redirects
// are never followed.
package brreg

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
	"time"
)

// searchPath is the Enhetsregisteret search endpoint.
const searchPath = "/enhetsregisteret/api/enheter"

// retryAttempts is how many times a failed GET is retried beyond the first
// attempt: four attempts in all, .NET's AddStandardResilienceHandler default.
const retryAttempts = 3

// AttemptTimeout bounds one attempt, fixed at 4 s and not configurable — only
// the overall timeout is. A caller that runs a read inside a deadline of its
// own (Customers' registry hook) sizes that deadline from it.
const AttemptTimeout = 4 * time.Second

// backoffBase and backoffCap bound defaultBackoff: exponential with full
// jitter from a 200 ms base, so the worst-case total delay across three
// retries (200+400+800 ms, before jitter shrinks each toward 0) is about
// 1.4 s. The cap never binds at retryAttempts = 3: it is a guard on the
// doubling should the attempt count ever rise.
const (
	backoffBase = 200 * time.Millisecond
	backoffCap  = 1 * time.Second
)

// defaultBackoff is the production backoff before retry attempt n (1-indexed:
// the wait before the second overall attempt is defaultBackoff(1)):
// exponential with full jitter — a uniformly random duration in
// [0, min(base*2^(n-1), cap)]. New's backoff argument overrides it in tests,
// so a retry loop's tests never actually sleep.
func defaultBackoff(attempt int) time.Duration {
	shift := attempt - 1
	d := backoffBase * time.Duration(int64(1)<<uint(shift))
	if d <= 0 || d > backoffCap {
		d = backoffCap
	}
	return time.Duration(rand.Int64N(int64(d) + 1))
}

// waitBackoff blocks for d, or until ctx ends first, reporting whether the
// wait completed (false means ctx ended it — the caller stops retrying rather
// than fire an attempt already doomed to fail on a cancelled context).
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

// ErrUnavailable is every read's answer once it could not get a usable
// answer: every attempt failed on a transport error, a timeout or a
// retryable status, a non-retryable non-2xx status ended the loop, or the
// body could not be used. A definite answer about an organisation number —
// unknown (404) or removed (410) — is never this error.
var ErrUnavailable = errors.New("brreg: registry unavailable")

// isSuccessStatus reports a 2xx: the only status treated as an answer to
// decode.
func isSuccessStatus(status int) bool {
	return status >= 200 && status < 300
}

// isRetryableStatus reports whether status is worth retrying rather than
// failing immediately: a 5xx or a 408, the shape .NET's default resilience
// predicate retries a GET on. Any other non-2xx status, 404 included, stops
// the loop on the spot.
func isRetryableStatus(status int) bool {
	return status >= 500 || status == http.StatusRequestTimeout
}

// Client is the Enhetsregisteret HTTP client.
type Client struct {
	baseURL string
	timeout time.Duration
	client  *http.Client
	backoff func(attempt int) time.Duration
}

// New is a client of the registry at baseURL, each read bounded overall by
// timeout. transport nil is http.DefaultTransport and backoff nil the
// jittered production backoff; a test passes a fake transport or a local
// server's URL, and a zero backoff, so it never opens a real socket or
// actually sleeps.
func New(baseURL string, timeout time.Duration, transport http.RoundTripper, backoff func(int) time.Duration) *Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if backoff == nil {
		backoff = defaultBackoff
	}
	return &Client{
		baseURL: baseURL,
		timeout: timeout,
		// Redirects are never followed: a 3xx is returned to the read as-is,
		// which treats it as the non-2xx it is. The base URL is operator
		// configuration, so a redirect to an arbitrary host is not reachable
		// today — but following one would send the request to a host no
		// operator named.
		client:  &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		backoff: backoff,
	}
}

// SearchQuery is one search: by OrganisationNumber when it is not "", else by
// Name; at most Size matches.
type SearchQuery struct {
	OrganisationNumber string
	Name               string
	Size               int
}

// path is the request path: organisasjonsnummer for an exact lookup, navn for
// a name search, both capped at Size.
func (q SearchQuery) path() string {
	v := url.Values{}
	if q.OrganisationNumber != "" {
		v.Set("organisasjonsnummer", q.OrganisationNumber)
	} else {
		v.Set("navn", q.Name)
	}
	v.Set("size", strconv.Itoa(q.Size))
	return searchPath + "?" + v.Encode()
}

// Match is one search result: an entity's organisation number and name.
type Match struct {
	OrganisationNumber string
	Name               string
}

// ErrUndecodable is a search whose 2xx answer would not decode. It is wrapped
// with ErrUnavailable, so a caller that does not care which failure it was
// reads one error; a caller that logs the two apart can.
var ErrUndecodable = errors.New("brreg: search response could not be decoded")

// searchResult is the subset of the search response read. A response with no
// "_embedded" key (zero matches) decodes to a nil Embedded, not an error.
type searchResult struct {
	Embedded *struct {
		Entities []struct {
			OrganisationNumber string `json:"organisasjonsnummer"`
			Name               string `json:"navn"`
		} `json:"enheter"`
	} `json:"_embedded"`
}

// Search asks the registry for the entities q names. Zero matches is an empty
// slice, never an error. Every failure wraps ErrUnavailable; a 2xx body that
// will not decode wraps ErrUndecodable too.
func (c *Client) Search(ctx context.Context, q SearchQuery) ([]Match, error) {
	body, err := c.lookup(ctx, q.path())
	if err != nil {
		return nil, err
	}
	var parsed searchResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("%w: %w: %w", ErrUnavailable, ErrUndecodable, err)
	}
	out := []Match{}
	if parsed.Embedded != nil {
		for _, e := range parsed.Embedded.Entities {
			out = append(out, Match{OrganisationNumber: e.OrganisationNumber, Name: e.Name})
		}
	}
	return out, nil
}

// lookup performs one GET against path, the whole call bounded by c.timeout,
// retrying a failed attempt (a transport error, a timeout, or a retryable
// status) up to retryAttempts further times, each bounded by AttemptTimeout
// and preceded by c.backoff's delay. It returns ErrUnavailable once every
// attempt has failed, or the first attempt answered with a non-retryable
// non-2xx status; a 2xx response's body, whatever it decodes to, is returned.
func (c *Client) lookup(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= retryAttempts; attempt++ {
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
	return nil, fmt.Errorf("%w: %w", ErrUnavailable, lastErr)
}

// attempt performs one GET, bounded by AttemptTimeout (itself bounded by
// ctx's own deadline). The body is read to completion before returning, so a
// cancelled attempt context can never surface as a read error on a response
// the caller still needs.
func (c *Client) attempt(ctx context.Context, path string) (int, []byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, AttemptTimeout)
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
