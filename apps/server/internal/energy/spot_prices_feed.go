package energy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is the spot price feed's one request (energy consumption billing
// design D2): the address rendered from ENERGY_SPOT_PRICE_FEED_URL for one
// area and one civil day, one GET through a client of the module's own —
// every address the host resolves to checked against netguard's one table of
// forbidden ranges and the connection made to the address that was checked,
// no proxy and no redirect followed, a body of at most 1 MiB — and the body
// read into the day's price intervals. It is the calendar subscriptions'
// fetch (internal/calendar/subscriptions_fetch.go) copied rather than
// imported, since no module imports another's code. The worker
// (spot_prices_worker.go) decides which days to ask for and what to record;
// a failure is one of the sentences below, never the response's body.

// The fetch's bounds.
const (
	// spotFeedFetchBudget is one fetch's whole budget, the dial, the headers
	// and the body together — the calendar's.
	spotFeedFetchBudget = 20 * time.Second
	// maxSpotFeedBody is the most of an answer read: a day at a quarter of
	// an hour is under 20 KiB, so 1 MiB is a bound, not a size.
	maxSpotFeedBody = 1 << 20
	// maxSpotFeedPrice bounds a figure to what numeric(12,6) stores.
	maxSpotFeedPrice = 1e6

	// The transport's own bounds, the calendar's and the Peppol lookup's.
	spotFeedDialTimeout           = 10 * time.Second
	spotFeedTLSHandshakeTimeout   = 10 * time.Second
	spotFeedResponseHeaderTimeout = 15 * time.Second
	spotFeedMaxResponseHeaderSize = 64 << 10
)

// The sentences a failed fetch records as last_error. The parser's are about
// the body's shape and never quote it.
const (
	spotFeedRedirects   = "the feed redirects; set ENERGY_SPOT_PRICE_FEED_URL to the address it points at"
	spotFeedTooLarge    = "the feed's answer is larger than 1 MiB"
	spotFeedDisallowed  = "the feed's address is not one Vantigo may fetch"
	spotFeedTimedOut    = "the feed did not answer in time"
	spotFeedUnreachable = "the feed could not be reached"

	spotFeedNotAList      = "the feed did not answer a list of prices"
	spotFeedEmpty         = "the feed answered no prices for the day"
	spotFeedBadTime       = "an interval's time is not an RFC 3339 time with an offset"
	spotFeedBackwards     = "an interval does not end after it starts"
	spotFeedLateStart     = "the day's first interval does not start at its midnight"
	spotFeedNotContiguous = "the day's intervals are not contiguous"
	spotFeedEarlyEnd      = "the day's last interval does not end at the next midnight"
	spotFeedNoPrice       = "an interval has no NOK_per_kWh"
	spotFeedBadPrice      = "an interval's price is not a number Vantigo can store"
)

// spotFeedPlaceholders are what a feed template must carry (plan reading 13),
// the same list config.go checks ENERGY_SPOT_PRICE_FEED_URL against.
var spotFeedPlaceholders = []string{"{year}", "{month}", "{day}", "{area}"}

// spotFeedTemplate is a validated ENERGY_SPOT_PRICE_FEED_URL.
type spotFeedTemplate string

// parseSpotFeedTemplate validates raw as plan reading 13 reads it: an
// absolute https URL carrying all four placeholders. config.Load refuses
// anything else at start-up; this is the worker's own check, for a Config
// built by hand.
func parseSpotFeedTemplate(raw string) (spotFeedTemplate, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("energy: the spot price feed's address must be an absolute https URL")
	}
	for _, ph := range spotFeedPlaceholders {
		if !strings.Contains(raw, ph) {
			return "", errors.New("energy: the spot price feed's address must contain {year}, {month}, {day} and {area}")
		}
	}
	return spotFeedTemplate(raw), nil
}

// render is the address of area's prices on day, a civil date (its year,
// month and day are read, whatever its location), the month and the day
// zero-padded as the public feed names its files.
func (t spotFeedTemplate) render(area string, day time.Time) string {
	return strings.NewReplacer(
		"{year}", fmt.Sprintf("%04d", day.Year()),
		"{month}", fmt.Sprintf("%02d", int(day.Month())),
		"{day}", fmt.Sprintf("%02d", day.Day()),
		"{area}", url.PathEscape(area),
	).Replace(string(t))
}

// spotFeedTransport is the production transport, built once for the
// process: dialling through netguard, so the host is resolved, checked
// against the one table of forbidden addresses, and then dialled by the very
// address that was checked — the DNS-rebinding defence the calendar's fetch
// has.
var spotFeedTransport = sync.OnceValue(func() *http.Transport {
	return spotFeedGuardedTransport(net.DefaultResolver, (&net.Dialer{Timeout: spotFeedDialTimeout}).DialContext)
})

// spotFeedGuardedTransport is a transport dialling through netguard over
// resolver, the calendar's guardedTransport.
//
// Proxy is nil on purpose, and NOT http.ProxyFromEnvironment: a proxy would
// send the request to an address the guard never checked. TLS is left to the
// transport's defaults, which derive the ServerName from the URL's host, not
// from the address dialled, so a certificate issued to somebody else is
// rejected even though the connection went to an IP literal.
//
// Keep-alives are off: the feed is asked a few times a cycle, fifteen minutes
// apart, so a kept connection is never reused in time and only sits open.
func spotFeedGuardedTransport(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		DialContext:            netguard.DialContext(resolver, dial),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    spotFeedTLSHandshakeTimeout,
		ResponseHeaderTimeout:  spotFeedResponseHeaderTimeout,
		MaxResponseHeaderBytes: spotFeedMaxResponseHeaderSize,
	}
}

// spotFeedClient is the client a fetch goes through: rt when a test set one
// (modtest.WithTransport sets Deps.HTTPTransport), the guarded transport
// otherwise.
func spotFeedClient(rt http.RoundTripper) *http.Client {
	if rt == nil {
		rt = spotFeedTransport()
	}
	return &http.Client{
		Transport: rt,
		Timeout:   spotFeedFetchBudget,
		// Redirects are never followed: the next address is one the guard
		// never saw. A 3xx is handed back and recorded as the failure it is.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// spotFeedOutcome is what one fetch answered: the day's intervals, a 404 (the
// feed has no file for the day — not yet published, or never), or the
// sentence of a failure.
type spotFeedOutcome struct {
	rows     []spotPriceRow
	notFound bool
	failure  string
}

// fetchSpotFeedDay is one GET of target under ctx, read into area's
// intervals on day (a civil date in zone).
func fetchSpotFeedDay(ctx context.Context, client *http.Client, target, area string, day time.Time, zone *time.Location) spotFeedOutcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return spotFeedOutcome{failure: spotFeedUnreachable}
	}
	req.Header.Set("User-Agent", "Vantigo-Energy")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return spotFeedOutcome{failure: spotFeedTransportFailure(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return spotFeedOutcome{notFound: true}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return spotFeedOutcome{failure: spotFeedRedirects}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return spotFeedOutcome{failure: spotFeedAnswered(resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSpotFeedBody+1))
	if err != nil {
		return spotFeedOutcome{failure: spotFeedTransportFailure(err)}
	}
	if len(raw) > maxSpotFeedBody {
		return spotFeedOutcome{failure: spotFeedTooLarge}
	}
	rows, failure := parseSpotFeedDay(raw, area, day, zone)
	if failure != "" {
		return spotFeedOutcome{failure: failure}
	}
	return spotFeedOutcome{rows: rows}
}

// spotFeedTransportFailure is the sentence of a request that answered
// nothing: a dial the guard refused, a budget spent, or anything else — the
// calendar's transportFailure.
func spotFeedTransportFailure(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, netguard.ErrDisallowed):
		return spotFeedDisallowed
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return spotFeedTimedOut
	default:
		return spotFeedUnreachable
	}
}

// spotFeedAnswered is the sentence of a status that is neither prices, a 404
// nor a redirect: the code and its standard text, never the reason phrase the
// server sent — the calendar's answered.
func spotFeedAnswered(status int) string {
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("the feed answered %d %s", status, text)
	}
	return fmt.Sprintf("the feed answered %d", status)
}

// spotFeedEntry is one element of the feed's answer (R5 §5.1): the NOK and
// EUR prices per kWh, the exchange rate, and the interval as RFC 3339 times
// carrying the local offset.
type spotFeedEntry struct {
	NokPerKwh *float64 `json:"NOK_per_kWh"`
	EurPerKwh *float64 `json:"EUR_per_kWh"`
	Exr       *float64 `json:"EXR"`
	TimeStart string   `json:"time_start"`
	TimeEnd   string   `json:"time_end"`
}

// parseSpotFeedDay reads body as area's prices on day, a civil date in zone,
// or answers the sentence of why it cannot (plan reading 5): a JSON array of
// entries whose intervals are contiguous, the first starting at the day's
// local midnight and the last ending at the next day's — 24 hours, or 23 and
// 25 on the DST nights, at any resolution — each with a finite NOK price,
// negative allowed (the market clears below zero). Anything else writes
// nothing of the day.
func parseSpotFeedDay(body []byte, area string, day time.Time, zone *time.Location) ([]spotPriceRow, string) {
	var entries []spotFeedEntry
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) || json.Unmarshal(body, &entries) != nil {
		return nil, spotFeedNotAList
	}
	if len(entries) == 0 {
		return nil, spotFeedEmpty
	}
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone)
	next := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, zone)
	rows := make([]spotPriceRow, 0, len(entries))
	for i, e := range entries {
		start, errStart := time.Parse(time.RFC3339, e.TimeStart)
		end, errEnd := time.Parse(time.RFC3339, e.TimeEnd)
		switch {
		case errStart != nil || errEnd != nil:
			return nil, spotFeedBadTime
		case !end.After(start):
			return nil, spotFeedBackwards
		case i == 0 && !start.Equal(midnight):
			return nil, spotFeedLateStart
		case i > 0 && !start.Equal(rows[i-1].End):
			return nil, spotFeedNotContiguous
		case e.NokPerKwh == nil:
			return nil, spotFeedNoPrice
		case !storablePrice(e.NokPerKwh) || !storablePrice(e.EurPerKwh) || !storablePrice(e.Exr):
			return nil, spotFeedBadPrice
		}
		rows = append(rows, spotPriceRow{
			PriceArea: area, Start: start.UTC(), End: end.UTC(),
			NokPerKwh: *e.NokPerKwh, EurPerKwh: e.EurPerKwh, ExchangeRate: e.Exr,
		})
	}
	if !rows[len(rows)-1].End.Equal(next) {
		return nil, spotFeedEarlyEnd
	}
	return rows, ""
}

// storablePrice is whether v, when given, is a finite figure numeric(12,6)
// holds once rounded to six decimals.
func storablePrice(v *float64) bool {
	if v == nil {
		return true
	}
	if math.IsNaN(*v) || math.IsInf(*v, 0) {
		return false
	}
	return math.Abs(roundPrice(*v)) < maxSpotFeedPrice
}

// spotFeedHost is the feed address's host alone, for the log: nothing of the
// path or the query, which may carry a key.
func spotFeedHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// spotFeedDayText is a civil date as the log and the tests write it.
func spotFeedDayText(day time.Time) string {
	return day.Format(time.DateOnly)
}
