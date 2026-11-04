package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// This file is the module's guarded Google client (Google Workspace design
// G14), Entra's graph.go reshaped: one transport for the process, dialling
// through netguard with no proxy and no redirects; every request — the token
// exchange's included — refused unless its host is one of googleHosts; each
// call under a 30-second budget; a 429 waits Retry-After once (at most ten
// seconds), a 5xx is retried once after a second, a 401 fetches a fresh token
// once; every answer read through a 1 MiB cap (a raw message, a later
// delivery's, through 16 MiB). Google's error text is never logged, stored or
// answered: a refusal is a *googleError carrying the HTTP status and the
// reason code of Google's error body. The hosts are constants: no setting
// reaches them, and a test's fake comes in through Deps.HTTPTransport, never
// as a base URL.

// The hosts, fixed.
const (
	// tokenURL is the token endpoint every delegated token is asked of, and
	// the token_uri a key file must name.
	tokenURL = "https://oauth2.googleapis.com/token"
	// directoryBase is the Admin SDK Directory API's v1 base.
	directoryBase = "https://admin.googleapis.com/admin/directory/v1"
	// licensingBase is the Enterprise License Manager API's v1 base.
	licensingBase = "https://licensing.googleapis.com/apps/licensing/v1"
	// groupsSettingsBase is the Groups Settings API's v1 base, on the host
	// its reference documents (U12: groupssettings.googleapis.com is on the
	// list too, should the test organisation answer only there).
	groupsSettingsBase = "https://www.googleapis.com/groups/v1/groups"
)

// googleHosts are the only hosts the client reaches (G14): the token and
// revoke endpoints, the Directory, Licensing, Calendar v3 and Groups Settings
// (U12 leaves which of its two hosts open) and the JWKS, Gmail, and the OIDC
// discovery document alone on accounts.google.com.
var googleHosts = []string{
	"oauth2.googleapis.com",
	"admin.googleapis.com",
	"licensing.googleapis.com",
	"www.googleapis.com",
	"gmail.googleapis.com",
	"groupssettings.googleapis.com",
	"accounts.google.com",
}

// The client's bounds.
const (
	dialTimeout            = 10 * time.Second
	tlsHandshakeTimeout    = 10 * time.Second
	responseHeaderTimeout  = 15 * time.Second
	maxResponseHeaderBytes = 64 << 10
	idleConnTimeout        = 90 * time.Second
	// callBudget bounds one exchange: the connection, the request, the
	// headers and the body together. A wait between two attempts is not in
	// it.
	callBudget = 30 * time.Second
	// maxRetryAfter is the longest Retry-After a call waits; a longer one
	// fails the call at once, errThrottled.
	maxRetryAfter = 10 * time.Second
	// serverErrorDelay is the wait before a 5xx's one retry, and a 429's
	// when it names no Retry-After.
	serverErrorDelay = time.Second
	// maxBody is the most of an answer read; a longer one is refused,
	// errResponseTooLarge. A raw message (delivery F) will read through 16
	// MiB, passed to do as its limit.
	maxBody = 1 << 20
)

// The client's failures (G14). A refusal Google answered that maps to none
// of them is a *googleError.
var (
	// errNotConfigured: the connection names no admin to impersonate.
	errNotConfigured = errors.New("google: the connection names no admin to impersonate")
	// errNoCredential: no live key fetched a token.
	errNoCredential = errors.New("google: no service account key fetched a token")
	// errCredentialInvalid: the token endpoint refused the key
	// (invalid_grant, invalid_client) or the sealed key cannot be opened on
	// this server.
	errCredentialInvalid = errors.New("google: Google refused the service account key")
	// errSubjectInvalid: the token endpoint refused the subject, not the key
	// (invalid_grant for an address Google does not know, or a suspended
	// user): the admin's problem in a test, that person's in a later call.
	errSubjectInvalid = errors.New("google: Google does not know the subject")
	// errNotDelegated: the token endpoint refused a scope (unauthorized_client
	// or access_denied, U14): the scope is not delegated to the service
	// account's client id.
	errNotDelegated = errors.New("google: the scope is not delegated")
	// errAdminRefused: an Admin SDK call answered 403.
	errAdminRefused = errors.New("google: the admin is not allowed the call")
	// errThrottled: Google answered 429 after the one wait, or asked for a
	// wait longer than maxRetryAfter.
	errThrottled = errors.New("google: Google throttled the call")
	// errNotFound: Google answered 404.
	errNotFound = errors.New("google: Google has no such resource")
	// errGoogleUnreachable: Google did not answer, answered nonsense, or
	// answered 5xx twice (a *googleError of a 5xx matches it too).
	errGoogleUnreachable = errors.New("google: Google did not answer")
	// errResponseTooLarge: an answer longer than the call reads.
	errResponseTooLarge = errors.New("google: Google's answer is larger than the client reads")
)

// googleError is a refusal Google answered: the status and the reason code
// of its error body (error.errors[0].reason, else error.status, else the
// OAuth error). Google's message is read nowhere.
type googleError struct {
	Status int
	Reason string
}

func (e *googleError) Error() string {
	s := fmt.Sprintf("google: Google answered %d", e.Status)
	if e.Reason != "" {
		s += " " + e.Reason
	}
	return s
}

// Is maps a refusal onto the module's own errors: a 5xx is
// errGoogleUnreachable, a 403 errAdminRefused, a 404 errNotFound and a 429
// errThrottled.
func (e *googleError) Is(target error) bool {
	switch target {
	case errGoogleUnreachable:
		return e.Status >= http.StatusInternalServerError
	case errAdminRefused:
		return e.Status == http.StatusForbidden
	case errNotFound:
		return e.Status == http.StatusNotFound
	case errThrottled:
		return e.Status == http.StatusTooManyRequests
	}
	return false
}

// googleClient is the client: the http.Client over the guarded transport,
// the process's token cache, the secrets box a key is opened with, the clock
// and the log. It is built per Deps (newGoogleClient); the transport and the
// token cache under it are the process's.
type googleClient struct {
	http   *http.Client
	tokens *tokenCache
	box    *secrets.Box
	now    func() time.Time
	logger *slog.Logger
	// backoff is Deps.HTTPBackoff: nil in production, meaning the waits
	// Google and the rule ask for; a test sets it to zero them.
	backoff func(attempt int) time.Duration
	// sleep waits d or until ctx ends; a unit test replaces it to record
	// the waits.
	sleep func(ctx context.Context, d time.Duration) error
}

// processTransport is the production transport, built once for the
// process: a plain HTTP transport whose dialler goes through netguard, so
// each host is resolved, checked against the one table of forbidden
// addresses and dialled by the address that was checked — copied from
// entra's rather than imported, because no module imports another.
var processTransport = sync.OnceValue(func() *http.Transport {
	return guardedTransport(net.DefaultResolver, (&net.Dialer{Timeout: dialTimeout}).DialContext)
})

// guardedTransport is a transport dialling through netguard over resolver.
// Proxy is nil on purpose: a proxy would send the request to an address the
// guard never checked. TLS is the transport's default, so the certificate
// is checked against the URL's host although the connection went to an IP
// literal.
func guardedTransport(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            netguard.DialContext(resolver, dial),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		ResponseHeaderTimeout:  responseHeaderTimeout,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		MaxIdleConns:           4,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        idleConnTimeout,
	}
}

// hostGuard refuses a request to any host but googleHosts, over https, before
// it reaches the transport: the token exchange, which golang.org/x/oauth2
// makes itself, goes through it as every call does.
type hostGuard struct {
	next http.RoundTripper
}

func (g hostGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkURL(req.URL); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return g.next.RoundTrip(req)
}

// checkURL refuses u unless it is https on one of googleHosts, with no
// user, no port and no dot segment.
func checkURL(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		!slices.Contains(googleHosts, u.Hostname()) || slices.Contains(strings.Split(u.Path, "/"), "..") {
		host := ""
		if u != nil {
			host = u.Host
		}
		return fmt.Errorf("google: a request to a host outside the fixed list: %q", host)
	}
	return nil
}

// newGoogleClient builds the client over d: Deps.HTTPTransport when a test
// set one (modtest.WithTransport), the guarded transport otherwise, behind
// hostGuard either way. A Deps without a clock or a logger gets the wall
// clock and a discarding log.
func newGoogleClient(d module.Deps) *googleClient {
	var rt http.RoundTripper = processTransport()
	if d.HTTPTransport != nil {
		rt = d.HTTPTransport
	}
	now, logger := d.Clock, d.Logger
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &googleClient{
		http: &http.Client{
			Transport:     hostGuard{next: rt},
			Timeout:       callBudget,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		tokens:  processTokens,
		box:     d.Secrets,
		now:     now,
		logger:  logger,
		backoff: d.HTTPBackoff,
		sleep:   sleepContext,
	}
}

// sleepContext waits d, or answers ctx's error when it ends first.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// get reads target with src's token into into, under do's rules.
func (c *googleClient) get(ctx context.Context, src tokenSource, target string, into any, opts ...callOption) error {
	return c.do(ctx, src, http.MethodGet, target, nil, into, maxBody, opts...)
}

// send is a write to target with src's token: body sent as JSON when not
// nil, the answer decoded into into when not nil, under do's rules.
func (c *googleClient) send(ctx context.Context, src tokenSource, method, target string, body, into any, opts ...callOption) error {
	var payload *requestBody
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("google: encode a %s body: %w", method, err)
		}
		payload = &requestBody{contentType: "application/json", data: data}
	}
	return c.do(ctx, src, method, target, payload, into, maxBody, opts...)
}

// requestBody is what a write sends: its bytes and their content type.
type requestBody struct {
	contentType string
	data        []byte
}

// callOption adjusts one call.
type callOption func(*callOptions)

type callOptions struct {
	// shown is the path the log names in place of the URL's: a path that
	// carries an address (Licensing's userId) is logged without it.
	shown string
	// once is a write made once: a 5xx is not retried, as a second create
	// could make a second user.
	once bool
}

// shownAs names the call in the log by path, in place of the URL's path.
func shownAs(path string) callOption { return func(o *callOptions) { o.shown = path } }

// noRetry makes a write once: its 5xx is the answer.
func noRetry() callOption { return func(o *callOptions) { o.once = true } }

// do is one call to Google with src's token: body sent when not nil, a 2xx
// answer decoded into into when not nil, read through limit. A 429 waits
// Retry-After once (at most maxRetryAfter), then fails errThrottled; a 5xx is
// retried once after serverErrorDelay, unless the call is made once
// (noRetry); a 401 drops the token and is retried once with a fresh one. Any
// other answer is a *googleError, logged by its status and reason alone. The
// log names the host and the path — the one shownAs gives, when given —
// never the query.
func (c *googleClient) do(ctx context.Context, src tokenSource, method, target string, body *requestBody, into any, limit int64, opts ...callOption) error {
	_, data, shown, err := c.doRaw(ctx, src, method, target, body, limit, opts...)
	if err != nil {
		return err
	}
	if into == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%w: %s %s: the answer is not the JSON expected", errGoogleUnreachable, method, shown)
	}
	return nil
}

// doRaw is do's exchange without the decoding: a 2xx answer's headers and
// bytes, read through limit, and the path the log names the call by — for a
// caller reading an answer that is not JSON (the batch endpoint's
// multipart/mixed). Every rule of do holds.
func (c *googleClient) doRaw(ctx context.Context, src tokenSource, method, target string, body *requestBody, limit int64, opts ...callOption) (http.Header, []byte, string, error) {
	var o callOptions
	for _, opt := range opts {
		opt(&o)
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, nil, "", fmt.Errorf("google: a URL that does not parse: %w", err)
	}
	if err := checkURL(u); err != nil {
		return nil, nil, "", err
	}
	shown := u.Host + u.Path
	if o.shown != "" {
		shown = u.Host + o.shown
	}
	throttled, retried, reauthorised := false, false, false
	for attempt := 1; ; attempt++ {
		token, err := src.token(ctx)
		if err != nil {
			return nil, nil, shown, err
		}
		status, header, data, err := c.exchange(ctx, method, u, shown, token, body, limit)
		if err != nil {
			return nil, nil, shown, err
		}
		switch {
		case status >= http.StatusOK && status < http.StatusMultipleChoices:
			return header, data, shown, nil
		case status == http.StatusTooManyRequests:
			wait, ok := retryAfter(header, c.now())
			if throttled || !ok {
				c.logger.DebugContext(ctx, "google: Google throttled a call", "method", method, "path", shown)
				return nil, nil, shown, errThrottled
			}
			throttled = true
			if err := c.pause(ctx, attempt, wait); err != nil {
				return nil, nil, shown, err
			}
		case status == http.StatusForbidden && rateLimited(data):
			// Calendar answers its usage limits 403, rateLimitExceeded or
			// userRateLimitExceeded: a throttle like a 429, waited once.
			if throttled {
				c.logger.DebugContext(ctx, "google: Google throttled a call", "method", method, "path", shown)
				return nil, nil, shown, errThrottled
			}
			throttled = true
			if err := c.pause(ctx, attempt, serverErrorDelay); err != nil {
				return nil, nil, shown, err
			}
		case status >= http.StatusInternalServerError && !retried && !o.once:
			retried = true
			if err := c.pause(ctx, attempt, serverErrorDelay); err != nil {
				return nil, nil, shown, err
			}
		case status == http.StatusUnauthorized && !reauthorised:
			reauthorised = true
			src.dropped()
		default:
			return nil, nil, shown, c.refusal(ctx, method, shown, status, data)
		}
	}
}

// exchange sends one request and answers the status, the headers and the
// body read through limit. A request that could not be made, or was not
// answered in time, is errGoogleUnreachable; the caller's own context
// ending is its error.
func (c *googleClient) exchange(ctx context.Context, method string, target *url.URL, shown, token string, body *requestBody, limit int64) (int, http.Header, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body.data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("google: build %s %s: %w", method, shown, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", body.contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, fmt.Errorf("%w: %s %s: %w", errGoogleUnreachable, method, shown, unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := readCapped(resp.Body, limit)
	if err != nil {
		if errors.Is(err, errResponseTooLarge) {
			return 0, nil, nil, fmt.Errorf("%s %s: %w", method, shown, err)
		}
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, fmt.Errorf("%w: %s %s: read the answer: %w", errGoogleUnreachable, method, shown, err)
	}
	return resp.StatusCode, resp.Header, data, nil
}

// unwrapURLError is a transport error without the *url.Error's URL — which
// may carry an address — the caller names by method and shown path.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// readCapped reads r to its end, refusing more than limit.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errResponseTooLarge
	}
	return data, nil
}

// pause waits before attempt's retry: d, or Deps.HTTPBackoff's figure when
// a test set one.
func (c *googleClient) pause(ctx context.Context, attempt int, d time.Duration) error {
	if c.backoff != nil {
		d = c.backoff(attempt)
	}
	return c.sleep(ctx, d)
}

// retryAfter is the wait a 429's Retry-After asks for — seconds, or an HTTP
// date — and false when it asks for more than maxRetryAfter. Without the
// header the wait is serverErrorDelay.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return serverErrorDelay, true
	}
	var d time.Duration
	if n, err := strconv.Atoi(v); err == nil {
		d = time.Duration(n) * time.Second
	} else if at, err := http.ParseTime(v); err == nil {
		d = at.Sub(now)
	} else {
		return serverErrorDelay, true
	}
	if d < 0 {
		d = 0
	}
	return d, d <= maxRetryAfter
}

// errorBody is Google's refusal: the JSON API shape { "error": { "code",
// "message", "status", "errors": [{ "reason" }] } }. The message is never
// decoded.
type errorBody struct {
	Error struct {
		Status string `json:"status"`
		Errors []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	} `json:"error"`
}

// reasonOf is the reason code of a refusal's body: the first
// errors[].reason, else error.status; "" for a body of another shape.
func reasonOf(data []byte) string {
	var body errorBody
	if err := json.Unmarshal(data, &body); err != nil {
		return ""
	}
	if len(body.Error.Errors) > 0 && body.Error.Errors[0].Reason != "" {
		return body.Error.Errors[0].Reason
	}
	return body.Error.Status
}

// rateLimitReasons are the reasons of a 403 that is a usage limit, not a
// refusal: Calendar answers its limits so.
var rateLimitReasons = []string{"rateLimitExceeded", "userRateLimitExceeded"}

// rateLimited reports whether a refusal's body names a usage limit.
func rateLimited(data []byte) bool {
	return slices.Contains(rateLimitReasons, reasonOf(data))
}

// refusal is the *googleError of a refused call, logged at debug level by
// its method, host and path, status and reason — never Google's words.
func (c *googleClient) refusal(ctx context.Context, method, shown string, status int, data []byte) error {
	e := &googleError{Status: status, Reason: reasonOf(data)}
	c.logger.DebugContext(ctx, "google: Google refused a call", "method", method, "path", shown, "status", status, "reason", e.Reason)
	return e
}
