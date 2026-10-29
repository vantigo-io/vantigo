package entra

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

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// This file is the module's Microsoft Graph client (Entra design D12): one
// transport for the process, dialling through netguard with no proxy and no
// redirects; each call under a 30-second budget; a 429 waits Retry-After
// once, a 5xx is retried once after a second — never for a call made
// noRetry, the create of a user, a group or a pass, which the tenant may
// have carried out before it failed —, a 401 fetches a fresh token once;
// every answer read through a 1 MiB cap (8 MiB for a mailbox $batch, 16 MiB
// for a page of the mailbox's delta or a $batch of events read whole); and Graph's refusal parsed into a
// graphError carrying the status and the codes, its words logged at debug
// level with the tenant's id — except a noRetry call's, which may echo the
// password or the pass it carried, and a noWords call's, which may echo a
// user's id — and never stored or answered. The hosts are constants: no
// setting reaches them, and a test's fake comes in through
// Deps.HTTPTransport, never as a base URL.
//
// The lifecycle (provisioner) and the connection's endpoints call Graph
// through get, post, patch, put, putRaw (a photo's bytes) and del with a
// credential credentialFor answered; the token is graph_token.go's.

// The hosts, fixed.
const (
	// graphTokenEndpoint is the client credentials grant's endpoint; %s is
	// the tenant id.
	graphTokenEndpoint = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
	// graphScope is the scope a client credentials token is asked for: every
	// application permission consented.
	graphScope = "https://graph.microsoft.com/.default"
	// graphAPIBase is Graph's v1.0 endpoint every call's path is appended
	// to.
	graphAPIBase = "https://graph.microsoft.com/v1.0"
	// graphAPIHost is graphAPIBase's host, which a built URL is checked
	// against.
	graphAPIHost = "graph.microsoft.com"
)

// The client's bounds (the plan's "Exact values").
const (
	graphDialTimeout            = 10 * time.Second
	graphTLSHandshakeTimeout    = 10 * time.Second
	graphResponseHeaderTimeout  = 15 * time.Second
	graphMaxResponseHeaderBytes = 64 << 10
	// graphIdleConnTimeout is how long a kept connection to one of the two
	// hosts waits for its next call.
	graphIdleConnTimeout = 90 * time.Second
	// graphCallBudget bounds one exchange: the connection, the request, the
	// headers and the body together. A wait between two attempts is not in
	// it.
	graphCallBudget = 30 * time.Second
	// graphMaxRetryAfter is the longest Retry-After a call waits; a longer
	// one fails the call at once, errThrottled, and the work is retried at
	// the next cycle.
	graphMaxRetryAfter = 60 * time.Second
	// graphServerErrorDelay is the wait before a 5xx's one retry, and a
	// 429's when it names no Retry-After.
	graphServerErrorDelay = time.Second
	// graphMaxBody is the most of an answer read; a longer one is refused,
	// errResponseTooLarge.
	graphMaxBody = 1 << 20
	// graphMaxBatchBody is the most of a mailbox $batch's answer read (a
	// call made largeAnswer): twenty events, each answered whole with its
	// body, may outgrow graphMaxBody.
	graphMaxBatchBody = 8 << 20
	// graphMaxDeltaBody is the most of a page of the mailbox's calendar
	// delta read (a call made deltaPage): fifty events answered whole —
	// subject, an HTML body, attendees — about 320 KiB each, which the
	// provider decodes none of but must read past. A $batch of twenty
	// events read whole (wholeEvents) is read through it too: a body asked
	// as text may still come as HTML, and twenty of those outgrow
	// graphMaxBatchBody.
	graphMaxDeltaBody = 16 << 20
	// graphDeltaPrefer is the Prefer header of a deltaPage call: the
	// event's times in UTC and fifty events a page (calendar Outlook sync
	// design O9.1).
	graphDeltaPrefer = `outlook.timezone="UTC", odata.maxpagesize=50`
)

// The client's failures. A refusal Graph answered is a *graphError; these
// are the rest.
var (
	// errNotConfigured: the connection names no tenant or no client.
	errNotConfigured = errors.New("entra: the connection has no tenant or client")
	// errNoCredential: no unrevoked, unexpired secret fetched a token.
	errNoCredential = errors.New("entra: no client secret fetched a token")
	// errCredentialInvalid: the token endpoint refused the secret
	// (invalid_client, unauthorized_client), or the sealed secret cannot be
	// opened on this server.
	errCredentialInvalid = errors.New("entra: the tenant refused the client secret")
	// errTenantUnreachable: the tenant did not answer, answered nonsense,
	// or answered 5xx twice (a *graphError of a 5xx matches it too).
	errTenantUnreachable = errors.New("entra: the tenant did not answer")
	// errThrottled: Graph answered 429 after the one wait, or asked for a
	// wait longer than graphMaxRetryAfter.
	errThrottled = errors.New("entra: Graph throttled the call")
	// errResponseTooLarge: an answer longer than graphMaxBody (graphMaxBatchBody
	// for a call made largeAnswer, graphMaxDeltaBody deltaPage or wholeEvents).
	errResponseTooLarge = errors.New("entra: Graph's answer is larger than the client reads")
)

// graphError is a refusal Graph answered: the status, error.code and the
// first error.details[].code (a taken UPN is Request_BadRequest with the
// detail ObjectConflict). The message is logged, never kept here; two flags
// alone are read from it, as Graph names neither by a code of its own:
// Exists, a reference added that is there already ("One or more added
// object references already exist"), and Validation, a subscription's
// create whose handshake with the webhook failed ("Subscription validation
// request failed…", "…validation request timed out"), read from the message
// alone: the code ValidationError is any 400's whose body did not validate,
// which must stay an ordinary failure, not the webhook unreachable.
type graphError struct {
	Status     int
	Code       string
	Detail     string
	Exists     bool
	Validation bool
}

func (e *graphError) Error() string {
	s := fmt.Sprintf("entra: Graph answered %d", e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Detail != "" {
		s += " (" + e.Detail + ")"
	}
	return s
}

// Is makes a 5xx refusal an errTenantUnreachable, so a caller that only
// asks "could the tenant be asked" needs no second check.
func (e *graphError) Is(target error) bool {
	return target == errTenantUnreachable && e.Status >= http.StatusInternalServerError
}

// asGraphError is err's *graphError, when it is one.
func asGraphError(err error) (*graphError, bool) {
	var g *graphError
	ok := errors.As(err, &g)
	return g, ok
}

// isCode reports whether err is a refusal whose error.code or first
// details code is one of codes — isCode(err, "ObjectConflict").
func isCode(err error, codes ...string) bool {
	g, ok := asGraphError(err)
	if !ok {
		return false
	}
	return (g.Code != "" && slices.Contains(codes, g.Code)) || (g.Detail != "" && slices.Contains(codes, g.Detail))
}

// isStatus reports whether err is a refusal of status.
func isStatus(err error, status int) bool {
	g, ok := asGraphError(err)
	return ok && g.Status == status
}

// graphCredential is what a call to the tenant is made with: the tenant and
// the client, and the secret's row id and sealed value — opened only for a
// token request, never held open. roles and oid are the roles and the oid
// claims of the token credentialFor last fetched or found with it.
type graphCredential struct {
	id       int64
	tenantID uuid.UUID
	clientID uuid.UUID
	sealed   []byte
	roles    []string
	oid      *uuid.UUID
}

// graphClient is the client: the transport, the process's token cache, the
// secrets box the client secret is opened with, the clock and the log. It is
// built per Deps (newGraphClient); the transport and the token cache under
// it are the process's.
type graphClient struct {
	http   *http.Client
	tokens *tokenCache
	box    *secrets.Box
	now    func() time.Time
	logger *slog.Logger
	// backoff is Deps.HTTPBackoff: nil in production, meaning the waits
	// Graph and the rule ask for; a test sets it to zero them.
	backoff func(attempt int) time.Duration
	// sleep waits d or until ctx ends; a unit test replaces it to record
	// the waits.
	sleep func(ctx context.Context, d time.Duration) error
}

// graphTransport is the production transport, built once for the process: a
// plain HTTP transport whose dialler goes through netguard, so each host is
// resolved, checked against the one table of forbidden addresses and dialled
// by the address that was checked — the calendar subscriptions' and the
// Peppol lookup's defence, copied rather than imported because no module
// imports another.
var graphTransport = sync.OnceValue(func() *http.Transport {
	return guardedTransport(net.DefaultResolver, (&net.Dialer{Timeout: graphDialTimeout}).DialContext)
})

// guardedTransport is a transport dialling through netguard over resolver.
//
// Proxy is nil on purpose, not http.ProxyFromEnvironment: a proxy would
// send the request to an address the guard never checked. TLS is the
// transport's default, so the certificate is checked against the URL's host
// although the connection went to an IP literal.
//
// Keep-alives are on, unlike the calendar subscriptions' stranger: Graph is
// two fixed hosts called many times a cycle, so a kept connection is reused
// and never sits open to anybody else.
func guardedTransport(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      false,
		DialContext:            netguard.DialContext(resolver, dial),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    graphTLSHandshakeTimeout,
		ResponseHeaderTimeout:  graphResponseHeaderTimeout,
		MaxResponseHeaderBytes: graphMaxResponseHeaderBytes,
		MaxIdleConns:           4,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        graphIdleConnTimeout,
	}
}

// graphHTTPClient is the http.Client over rt: the call budget, and no
// redirect ever followed — the next address is one the guard never saw. A
// 3xx is handed back and refused as the answer it is.
func graphHTTPClient(rt http.RoundTripper) *http.Client {
	return &http.Client{
		Transport:     rt,
		Timeout:       graphCallBudget,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// newGraphClient builds the client over d: Deps.HTTPTransport when a test
// set one (modtest.WithTransport), the guarded transport otherwise. A Deps
// without a clock or a logger gets the wall clock and a discarding log.
func newGraphClient(d module.Deps) *graphClient {
	var rt http.RoundTripper = graphTransport()
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
	return &graphClient{
		http:    graphHTTPClient(rt),
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

// callOption changes how one call is made.
type callOption int

// noRetry is a call that creates what must not be created twice: POST
// /users with the create's password, the pass's create, whose answer is the
// pass, and POST /groups. A 5xx is the tenant's answer, not retried — the
// first attempt may have been carried out (the caller re-reads instead) —
// and Graph's message on a refusal is not logged, the status and the codes
// only. A 429 and a 401, which the tenant answers before it acts, are
// retried as for any call.
const noRetry callOption = 1

// noWords is a call whose refusal may echo a user's id in Graph's words — a
// member or an owner added or removed by its reference: retried as any
// call, its refusal logged by its status and codes alone.
const noWords callOption = 2

// eventual is a read Graph answers only as an advanced query — $search,
// $count, an in filter on employeeId — sent with ConsistencyLevel:
// eventual, which Graph requires of one (Entra phase 2, delivery B; plan
// reading 3). It is retried as any call.
const eventual callOption = 3

// largeAnswer is a call whose answer is read through graphMaxBatchBody
// rather than graphMaxBody: the mailbox's $batch of events (mailbox.go).
const largeAnswer callOption = 4

// deltaPage is a read of a page of the mailbox's calendar delta
// (mailbox_delta.go): sent with Prefer: graphDeltaPrefer, its answer read
// through graphMaxDeltaBody.
const deltaPage callOption = 5

// wholeEvents is a mailbox $batch reading twenty events whole
// (mailbox_writeback.go): its answer read through graphMaxDeltaBody, as a
// delta page's, with no Prefer of its own — each request inside carries
// its own.
const wholeEvents callOption = 6

// get reads path into into. path is everything after /v1.0, its query
// already escaped (url.Values.Encode).
func (c *graphClient) get(ctx context.Context, cred graphCredential, path string, into any, opts ...callOption) error {
	return c.do(ctx, cred, http.MethodGet, path, nil, into, opts...)
}

// post sends body to path and reads the answer into into (nil discards it).
func (c *graphClient) post(ctx context.Context, cred graphCredential, path string, body, into any, opts ...callOption) error {
	return c.do(ctx, cred, http.MethodPost, path, body, into, opts...)
}

// patch sends body to path; Graph answers 204.
func (c *graphClient) patch(ctx context.Context, cred graphCredential, path string, body any, opts ...callOption) error {
	return c.do(ctx, cred, http.MethodPatch, path, body, nil, opts...)
}

// put sends body to path (a manager's $ref); Graph answers 204.
func (c *graphClient) put(ctx context.Context, cred graphCredential, path string, body any) error {
	return c.do(ctx, cred, http.MethodPut, path, body, nil)
}

// putRaw sends data to path as it is, with contentType — a user's photo,
// image/jpeg — under the same rules as every call; Graph answers 200.
func (c *graphClient) putRaw(ctx context.Context, cred graphCredential, path, contentType string, data []byte) error {
	return c.send(ctx, cred, http.MethodPut, path, &payload{data: data, contentType: contentType}, nil)
}

// del deletes path; Graph answers 204.
func (c *graphClient) del(ctx context.Context, cred graphCredential, path string, opts ...callOption) error {
	return c.do(ctx, cred, http.MethodDelete, path, nil, nil, opts...)
}

// tokenSource is where a call's bearer token comes from, and what a 401
// does to it (Outlook connection design M5): token answers the token to
// send; dropped forgets it after Graph refused it, before the one retry;
// challenged reads a 401's headers first and answers an error to end the
// call there, without the retry; tenant names the tenant in the client's
// log lines. The application credential (credentialSource) is today's
// behaviour exactly: challenged answers nil, so a 401 drops the token and
// retries once with a fresh one. The person's delegated token
// (outlook_token.go) answers a continuous access evaluation claims
// challenge with contracts.ErrConnectionNeeded instead.
type tokenSource interface {
	token(ctx context.Context) (string, error)
	dropped()
	challenged(ctx context.Context, header http.Header) error
	tenant() uuid.UUID
}

// credentialSource is the application credential cred as a tokenSource.
type credentialSource struct {
	c    *graphClient
	cred graphCredential
}

func (s credentialSource) token(ctx context.Context) (string, error) { return s.c.token(ctx, s.cred) }
func (s credentialSource) dropped()                                  { s.c.tokens.drop(s.cred) }
func (s credentialSource) challenged(context.Context, http.Header) error {
	return nil
}
func (s credentialSource) tenant() uuid.UUID { return s.cred.tenantID }

// getAs reads path into into with src's token: getAs is get for a source
// other than the application credential — the person's delegated token.
func (c *graphClient) getAs(ctx context.Context, src tokenSource, path string, into any, opts ...callOption) error {
	return c.doAs(ctx, src, http.MethodGet, path, nil, into, opts...)
}

// postAs is post with src's token.
func (c *graphClient) postAs(ctx context.Context, src tokenSource, path string, body, into any, opts ...callOption) error {
	return c.doAs(ctx, src, http.MethodPost, path, body, into, opts...)
}

// delAs is del with src's token.
func (c *graphClient) delAs(ctx context.Context, src tokenSource, path string, opts ...callOption) error {
	return c.doAs(ctx, src, http.MethodDelete, path, nil, nil, opts...)
}

// payload is a request's body: the bytes and their media type.
type payload struct {
	data        []byte
	contentType string
}

// do is one call to Graph with cred: body sent as JSON when not nil, a 2xx
// answer decoded into into when not nil, under send's rules.
func (c *graphClient) do(ctx context.Context, cred graphCredential, method, path string, body, into any, opts ...callOption) error {
	return c.doAs(ctx, credentialSource{c: c, cred: cred}, method, path, body, into, opts...)
}

// doAs is do with src's token.
func (c *graphClient) doAs(ctx context.Context, src tokenSource, method, path string, body, into any, opts ...callOption) error {
	var p *payload
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("entra: encode the body of %s %s: %w", method, path, err)
		}
		p = &payload{data: data, contentType: "application/json"}
	}
	return c.sendAs(ctx, src, method, path, p, into, opts...)
}

// send is one call to Graph with cred: body sent when not nil, a 2xx
// answer decoded into into when not nil. A 429 waits Retry-After once (at
// most graphMaxRetryAfter), then fails errThrottled; a 5xx is retried once
// after graphServerErrorDelay, unless opts holds noRetry; a 401 drops the
// cached token and is retried once with a fresh one. Any other answer is a
// *graphError.
func (c *graphClient) send(ctx context.Context, cred graphCredential, method, path string, body *payload, into any, opts ...callOption) error {
	return c.sendAs(ctx, credentialSource{c: c, cred: cred}, method, path, body, into, opts...)
}

// sendAs is send with src's token: every 401, a retry's included, is first
// handed to src.challenged, whose error ends the call; otherwise the token
// is dropped and the call retried once, as send does.
func (c *graphClient) sendAs(ctx context.Context, src tokenSource, method, path string, body *payload, into any, opts ...callOption) error {
	once := slices.Contains(opts, noRetry)
	words := !once && !slices.Contains(opts, noWords)
	extra := http.Header{}
	if slices.Contains(opts, eventual) {
		extra.Set("ConsistencyLevel", "eventual")
	}
	limit := int64(graphMaxBody)
	if slices.Contains(opts, largeAnswer) {
		limit = graphMaxBatchBody
	}
	if slices.Contains(opts, deltaPage) {
		extra.Set("Prefer", graphDeltaPrefer)
		limit = graphMaxDeltaBody
	}
	if slices.Contains(opts, wholeEvents) {
		limit = graphMaxDeltaBody
	}
	target, err := graphURL(path)
	if err != nil {
		return err
	}
	throttled, retried, reauthorised := false, false, false
	for attempt := 1; ; attempt++ {
		token, err := src.token(ctx)
		if err != nil {
			return err
		}
		status, header, data, err := c.exchange(ctx, method, target, token, body, extra, limit)
		if err != nil {
			return err
		}
		switch {
		case status >= http.StatusOK && status < http.StatusMultipleChoices:
			if into == nil || len(bytes.TrimSpace(data)) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, into); err != nil {
				return fmt.Errorf("%w: %s %s: the answer is not the JSON expected: %w", errTenantUnreachable, method, target.Path, err)
			}
			return nil
		case status == http.StatusTooManyRequests:
			wait, ok := retryAfter(header, c.now())
			if throttled || !ok {
				c.logger.DebugContext(ctx, "entra: Graph throttled a call",
					"tenant", src.tenant().String(), "method", method, "path", target.Path, "retryAfter", header.Get("Retry-After"))
				return errThrottled
			}
			throttled = true
			if err := c.pause(ctx, attempt, wait); err != nil {
				return err
			}
		case status >= http.StatusInternalServerError && !retried && !once:
			retried = true
			if err := c.pause(ctx, attempt, graphServerErrorDelay); err != nil {
				return err
			}
		case status == http.StatusUnauthorized:
			if err := src.challenged(ctx, header); err != nil {
				return err
			}
			if reauthorised {
				return c.refusal(ctx, src.tenant(), method, target, status, data, words)
			}
			reauthorised = true
			src.dropped()
		default:
			return c.refusal(ctx, src.tenant(), method, target, status, data, words)
		}
	}
}

// graphURL is graphAPIBase with path appended, refused unless it is still
// on Graph's host under /v1.0/ with no dot segment.
func graphURL(path string) (*url.URL, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("entra: a Graph path starts with /: %q", path)
	}
	u, err := url.Parse(graphAPIBase + path)
	if err != nil {
		return nil, fmt.Errorf("entra: a Graph path that does not parse: %w", err)
	}
	if u.Scheme != "https" || u.Host != graphAPIHost || u.User != nil || !strings.HasPrefix(u.Path, "/v1.0/") ||
		slices.Contains(strings.Split(u.Path, "/"), "..") {
		return nil, fmt.Errorf("entra: a Graph path that leaves Graph: %q", path)
	}
	return u, nil
}

// exchange sends one request, body with its media type when not nil and
// the extra headers, and answers the status, the headers and the body read
// through limit. A
// request that could not be made, or was not answered in time, is
// errTenantUnreachable; the caller's own context ending is its error.
func (c *graphClient) exchange(ctx context.Context, method string, target *url.URL, token string, body *payload, extra http.Header, limit int64) (int, http.Header, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body.data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("entra: build %s %s: %w", method, target.Path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", body.contentType)
	}
	for k, v := range extra {
		req.Header[k] = v
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, fmt.Errorf("%w: %s %s: %w", errTenantUnreachable, method, target.Path, unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := readCapped(resp.Body, limit)
	if err != nil {
		if errors.Is(err, errResponseTooLarge) {
			return 0, nil, nil, fmt.Errorf("%s %s: %w", method, target.Path, err)
		}
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, fmt.Errorf("%w: %s %s: read the answer: %w", errTenantUnreachable, method, target.Path, err)
	}
	return resp.StatusCode, resp.Header, data, nil
}

// unwrapURLError is a transport error without the *url.Error's URL, which
// the caller names by method and path.
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
func (c *graphClient) pause(ctx context.Context, attempt int, d time.Duration) error {
	if c.backoff != nil {
		d = c.backoff(attempt)
	}
	return c.sleep(ctx, d)
}

// retryAfter is the wait a 429's Retry-After asks for — seconds, or an
// HTTP date — and false when it asks for more than graphMaxRetryAfter.
// Without the header the wait is graphServerErrorDelay.
func retryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return graphServerErrorDelay, true
	}
	var d time.Duration
	if n, err := strconv.Atoi(v); err == nil {
		d = time.Duration(n) * time.Second
	} else if at, err := http.ParseTime(v); err == nil {
		d = at.Sub(now)
	} else {
		return graphServerErrorDelay, true
	}
	if d < 0 {
		d = 0
	}
	return d, d <= graphMaxRetryAfter
}

// graphErrorBody is Graph's refusal: { "error": { "code", "message",
// "details": [{ "code" }] } }.
type graphErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Code string `json:"code"`
		} `json:"details"`
	} `json:"error"`
}

// refusal is the *graphError of a refused call. Graph's own words are
// logged at debug level with the tenant's id, the method and the path — not
// the query, which may carry an employee number — when words is true, and
// go nowhere else; a noRetry call's refusal is logged by its status and
// codes alone, as its words may echo the password or the pass it carried.
func (c *graphClient) refusal(ctx context.Context, tenant uuid.UUID, method string, target *url.URL, status int, data []byte, words bool) error {
	var body graphErrorBody
	_ = json.Unmarshal(data, &body)
	message := strings.ToLower(body.Error.Message)
	e := &graphError{Status: status, Code: body.Error.Code,
		Exists:     strings.Contains(message, "already exist"),
		Validation: strings.Contains(message, "validation request")}
	if len(body.Error.Details) > 0 {
		e.Detail = body.Error.Details[0].Code
	}
	attrs := []any{"tenant", tenant.String(), "method", method, "path", target.Path,
		"status", status, "code", e.Code, "detail", e.Detail}
	if words {
		attrs = append(attrs, "message", body.Error.Message)
	}
	c.logger.DebugContext(ctx, "entra: Graph refused a call", attrs...)
	return e
}
