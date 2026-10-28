package calendar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	ics "github.com/arran4/golang-ical"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is one subscription's fetch (calendar feed design D7): one GET
// through a client of the module's own, every address the host resolves to
// checked against netguard's one table of forbidden ranges and the
// connection made to the address that was checked, no proxy and no redirect
// followed; a conditional request from the row's validators; a body of at
// most 1 MiB that must be a calendar; and the outcome written to the row in
// one of three ways. A calendar of more than 10,000 events is refused too,
// since every load of the calendar parses what is stored. The same function
// serves the worker, with no request behind it, and the handlers that fetch
// inline — a save and Refresh now.
// The error a row carries is one of the sentences below, never the
// response's body or the address the host resolved to.

// The fetch's bounds.
const (
	// subscriptionFetchBudget is one fetch's whole budget, the dial, the
	// headers and the body together.
	subscriptionFetchBudget = 20 * time.Second
	// maxSubscriptionBody is the most of a calendar kept: Google's import
	// limit, and the body column's.
	maxSubscriptionBody = 1 << 20
	// maxSubscriptionRefresh caps the interval a feed asks to be read at.
	maxSubscriptionRefresh = 24 * time.Hour
	// defaultSubscriptionsPoll is the poll of a server built from Deps
	// without a Config — config.go's own default for
	// CALENDAR_SUBSCRIPTIONS_POLL.
	defaultSubscriptionsPoll = 30 * time.Minute
	// The column widths of what a fetch writes.
	maxSubscriptionError        = 500
	maxSubscriptionETag         = 500
	maxSubscriptionLastModified = 100

	// The transport's own bounds, the Peppol lookup's.
	subscriptionDialTimeout           = 10 * time.Second
	subscriptionTLSHandshakeTimeout   = 10 * time.Second
	subscriptionResponseHeaderTimeout = 15 * time.Second
	subscriptionMaxResponseHeaderSize = 64 << 10
)

// The sentences a failed fetch records.
const (
	fetchRedirects   = "the address redirects; subscribe to the address it points at"
	fetchTooLarge    = "the calendar is larger than 1 MiB"
	fetchNotCalendar = "the address did not answer a calendar"
	fetchDisallowed  = "the address is not one Vantigo may fetch"
	fetchTimedOut    = "the address did not answer in time"
	fetchUnreachable = "the address could not be reached"
	// fetchTooManyEvents is a calendar of more VEVENTs than a read takes
	// (maxSubscriptionEvents): it is parsed on every load, so it is never
	// stored.
	fetchTooManyEvents = "the calendar has more than 10,000 events"
)

// subscriptionTransport is the production transport, built once for the
// process: a plain HTTP transport whose dialler goes through netguard, so the
// host is resolved, checked against the one table of forbidden addresses,
// and then dialled by the very address that was checked — the DNS-rebinding
// defence the Peppol lookup and internal/mail have, copied here rather than
// imported because no module imports another's code.
var subscriptionTransport = sync.OnceValue(func() *http.Transport {
	return guardedTransport(net.DefaultResolver, (&net.Dialer{Timeout: subscriptionDialTimeout}).DialContext)
})

// guardedTransport is a transport dialling through netguard over resolver.
//
// Proxy is nil on purpose, and NOT http.ProxyFromEnvironment: a proxy would
// send the request to an address the guard never checked, which would defeat
// the guard entirely. If this server ever needs an outbound proxy, the guard
// has to move to where the proxy is, not be quietly dropped here.
//
// TLS is left to the transport's defaults, which is the other half of the
// guard being safe: http.Transport derives the TLS ServerName from the URL's
// host, not from the address it dialled, so a certificate issued to somebody
// else is rejected even though the connection went to an IP literal.
//
// Keep-alives are off: a subscription is fetched once every half hour, so a
// kept connection is never reused in time and only sits open to a stranger's
// server — by the hundred, one per host a cycle reached. Every fetch dials
// afresh, through the guard, and closes when it is read.
func guardedTransport(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		DialContext:            netguard.DialContext(resolver, dial),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    subscriptionTLSHandshakeTimeout,
		ResponseHeaderTimeout:  subscriptionResponseHeaderTimeout,
		MaxResponseHeaderBytes: subscriptionMaxResponseHeaderSize,
	}
}

// subscriptionClient is the client a fetch goes through: Deps.HTTPTransport
// when a test set one (modtest.WithTransport), the guarded transport
// otherwise.
func (s *server) subscriptionClient() *http.Client {
	var rt http.RoundTripper = subscriptionTransport()
	if s.deps.HTTPTransport != nil {
		rt = s.deps.HTTPTransport
	}
	return &http.Client{
		Transport: rt,
		Timeout:   subscriptionFetchBudget,
		// Redirects are never followed: the next address is one the guard
		// and the person never saw, and Go would follow up to ten. A 3xx is
		// handed back and recorded as the failure it is.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// fetchTarget is what a fetch sends: the row's id, address and validators.
type fetchTarget struct {
	id           int64
	url          string
	etag         *string
	lastModified *string
}

// targetOf is a row's fetch.
func targetOf(row store.CalendarSubscription) fetchTarget {
	return fetchTarget{id: row.ID, url: row.Url, etag: row.Etag, lastModified: row.LastModified}
}

// fetchOutcome is what one fetch answered: a calendar, a 304, or the
// sentence of a failure.
type fetchOutcome struct {
	notModified  bool
	body         string
	etag         *string
	lastModified *string
	refresh      time.Duration // the calendar's REFRESH-INTERVAL, 0 when it has none
	failure      string
}

// fetchSubscription is one GET of t under ctx, read into its outcome.
func (s *server) fetchSubscription(ctx context.Context, t fetchTarget) fetchOutcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
	if err != nil {
		return fetchOutcome{failure: fetchUnreachable}
	}
	req.Header.Set("User-Agent", "Vantigo-Calendar")
	req.Header.Set("Accept", "text/calendar")
	if t.etag != nil && *t.etag != "" {
		req.Header.Set("If-None-Match", *t.etag)
	}
	if t.lastModified != nil && *t.lastModified != "" {
		req.Header.Set("If-Modified-Since", *t.lastModified)
	}
	resp, err := s.subscriptionClient().Do(req)
	if err != nil {
		return fetchOutcome{failure: transportFailure(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return fetchOutcome{notModified: true}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fetchOutcome{failure: fetchRedirects}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fetchOutcome{failure: answered(resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBody+1))
	if err != nil {
		return fetchOutcome{failure: transportFailure(err)}
	}
	if len(raw) > maxSubscriptionBody {
		return fetchOutcome{failure: fetchTooLarge}
	}
	body := string(raw)
	// A text column takes neither invalid UTF-8 nor a NUL, and no calendar
	// carries either.
	if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		return fetchOutcome{failure: fetchNotCalendar}
	}
	cal, err := parseCalendarBody(body)
	if err != nil {
		return fetchOutcome{failure: fetchNotCalendar}
	}
	if len(cal.Events()) > maxSubscriptionEvents {
		return fetchOutcome{failure: fetchTooManyEvents}
	}
	out := fetchOutcome{body: body, refresh: refreshInterval(cal.CalendarProperties)}
	out.etag = validator(resp.Header.Get("ETag"), maxSubscriptionETag)
	out.lastModified = validator(resp.Header.Get("Last-Modified"), maxSubscriptionLastModified)
	return out
}

// transportFailure is the sentence of a request that answered nothing: a
// dial the guard refused, a budget spent, or anything else.
func transportFailure(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, netguard.ErrDisallowed):
		return fetchDisallowed
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return fetchTimedOut
	default:
		return fetchUnreachable
	}
}

// answered is the sentence of a status that is neither a calendar, a 304
// nor a redirect: the code and its standard text, never the reason phrase
// the server sent.
func answered(status int) string {
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("the address answered %d %s", status, text)
	}
	return fmt.Sprintf("the address answered %d", status)
}

// validator is a response's ETag or Last-Modified as the row keeps it: nil
// when absent, and nil when wider than its column — a validator cut short
// would never match again.
func validator(v string, width int) *string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > width {
		return nil
	}
	return &v
}

// refreshInterval is how often a calendar asks to be read: its
// REFRESH-INTERVAL, or Apple's X-PUBLISHED-TTL, the longer when it has both;
// 0 when it has neither.
func refreshInterval(props []ics.CalendarProperty) time.Duration {
	var longest time.Duration
	for _, p := range props {
		if !strings.EqualFold(p.IANAToken, "REFRESH-INTERVAL") && !strings.EqualFold(p.IANAToken, "X-PUBLISHED-TTL") {
			continue
		}
		if d, ok := parseICalDuration(p.Value); ok && d > longest {
			longest = d
		}
	}
	return longest
}

// subscriptionsPoll is CALENDAR_SUBSCRIPTIONS_POLL, its default when a bare
// Deps has no Config.
func (s *server) subscriptionsPoll() time.Duration {
	if s.deps.Config != nil && s.deps.Config.CalendarSubscriptionsPoll > 0 {
		return s.deps.Config.CalendarSubscriptionsPoll
	}
	return defaultSubscriptionsPoll
}

// nextFetchAt is when a subscription attempted at now is due again: the
// poll, or the feed's own interval when it asks for longer (capped at a
// day), plus a jitter uniform over [0, poll/10) — so two installations
// subscribed to the same calendar drift apart. A failed attempt has no
// interval of its own and waits the poll alone, with no backoff.
func nextFetchAt(now time.Time, poll, refresh time.Duration) time.Time {
	wait := max(poll, min(refresh, maxSubscriptionRefresh))
	if tenth := poll / 10; tenth > 0 {
		wait += rand.N(tenth)
	}
	return now.Add(wait)
}

// errSubscriptionGone is a subscription deleted while its fetch was out: the
// handlers answer it as the 404 an unknown id gets, and the worker passes
// over it.
var errSubscriptionGone = errors.New("calendar: the subscription was deleted while it was fetched")

// refreshSubscription fetches t under the fetch budget and writes the
// outcome through q, the pool's queries, answering the row as it then stands. A
// caller that went away before the fetch answered (a shutdown, a closed
// request) records nothing: that is no failure of the address. The write
// itself is not cancelled with the caller, so a fetch that answered is
// recorded.
//
// The record names t's url as well as its id, so a fetch overtaken by a PUT
// that changed the address writes nothing over the new address's row — its
// calendar, its validators and its due time are the old address's — and is
// logged at debug, no error: the row is read as it stands and answered, and
// one deleted meanwhile is errSubscriptionGone.
func (s *server) refreshSubscription(ctx context.Context, q *store.Queries, t fetchTarget) (store.CalendarSubscription, error) {
	budget, cancel := context.WithTimeout(ctx, subscriptionFetchBudget)
	o := s.fetchSubscription(budget, t)
	cancel()
	if err := ctx.Err(); err != nil {
		return store.CalendarSubscription{}, fmt.Errorf("calendar: fetch subscription %d: %w", t.id, err)
	}
	write := context.WithoutCancel(ctx)
	now := s.deps.Clock()
	poll := s.subscriptionsPoll()
	var row store.CalendarSubscription
	var err error
	switch {
	case o.failure != "":
		s.deps.Logger.WarnContext(ctx, "calendar: a subscription could not be fetched",
			"subscription_id", t.id, "host", hostOf(t.url), "error", o.failure)
		row, err = q.RecordSubscriptionFailed(write, store.RecordSubscriptionFailedParams{
			Now: now, NextFetchAt: nextFetchAt(now, poll, 0), Error: clipRunes(o.failure, maxSubscriptionError), ID: t.id, Url: t.url,
		})
	case o.notModified:
		row, err = q.RecordSubscriptionNotModified(write, store.RecordSubscriptionNotModifiedParams{
			Now: now, NextFetchAt: nextFetchAt(now, poll, 0), ID: t.id, Url: t.url,
		})
	default:
		row, err = q.RecordSubscriptionFetched(write, store.RecordSubscriptionFetchedParams{
			Etag: o.etag, LastModified: o.lastModified, Body: o.body,
			Now: now, NextFetchAt: nextFetchAt(now, poll, o.refresh), ID: t.id, Url: t.url,
		})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		s.deps.Logger.DebugContext(ctx, "calendar: a subscription's address changed while it was fetched; the fetch is not recorded",
			"subscription_id", t.id, "host", hostOf(t.url))
		row, err = q.GetSubscriptionByID(write, t.id)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.CalendarSubscription{}, errSubscriptionGone
		}
	}
	if err != nil {
		return store.CalendarSubscription{}, fmt.Errorf("calendar: record subscription %d's fetch: %w", t.id, err)
	}
	return row, nil
}

// hostOf is an address's host alone, for the log: nothing of the path or
// the query, which may carry a calendar's own token.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// clipRunes is v cut to at most n characters.
func clipRunes(v string, n int) string {
	if utf8.RuneCountInString(v) <= n {
		return v
	}
	return string([]rune(v)[:n])
}
