package energy

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
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is the client of the shared spot-price service
// (vantigo-spotprices, spot-price service design S7, plan readings 4, 5 and
// 12): its two reads, GET /api/v1/areas and GET /api/v1/prices, through a
// client of its own — separate from the public feed's, since this one must
// reach an address on the operator's private network and carries a key. The
// guard is as narrow as the design makes it: the dial goes through
// netguard.DialContextAllowingPrivate, which lets an RFC 1918 or RFC 4193
// address through and refuses every other range netguard refuses; over http
// every address must be private, so the bearer key is never sent in the
// clear to a public address; and a round-tripper refuses any request whose
// host is not the configured one before anything is dialled, so the
// exception cannot be reached by a redirect, a template or a later caller.
// The sync (spot_price_service_sync.go) decides what to ask and what to
// record; a failure is one of the sentences below, never the body, the key
// or the address's query.

// The client's bounds: the feed's, but with keep-alives on, since the
// service is asked several times a cycle while history is copied.
const (
	spotServiceIdleConnTimeout = 90 * time.Second
	// maxSpotServiceProblem is the most of a 400's problem document read for
	// the debug log.
	maxSpotServiceProblem = 64 << 10
	// maxSpotServiceAreas is the most of GET /api/v1/areas read once decoded:
	// twelve zones are a few KiB.
	maxSpotServiceAreas = 1 << 20
	// maxSpotServicePrices is the most of GET /api/v1/prices read once
	// decoded. A 35-day window at quarter-hours is 3 364 intervals; with
	// every field the contract has (start, end, priceEurPerMwh,
	// priceNokPerKwh, exchangeRate, rateSource, state, source, version) at
	// its widest — negative figures with six decimals, a 20-character
	// source, a six-digit version — it measured 787 KB compact and 1.02 MB
	// indented with offsets, over the 1 MiB the areas get. 4 MiB is four
	// times the indented answer, five the compact one.
	maxSpotServicePrices = 4 << 20
)

// The sentences a failed request records as last_error (design S7). They
// name the service and never quote its answer.
const (
	spotServiceRefusedKey     = "the spot-price service refused the key"
	spotServiceRateLimited    = "the spot-price service is rate limiting this installation"
	spotServicePublic         = "the spot-price service's address is public; use https"
	spotServiceDisallowed     = "the spot-price service's address is not one Vantigo may fetch"
	spotServiceTimedOut       = "the spot-price service did not answer in time"
	spotServiceUnreachable    = "the spot-price service could not be reached"
	spotServiceAreasTooLarge  = "the spot-price service's answer is larger than 1 MiB"
	spotServicePricesTooLarge = "the spot-price service's answer is larger than 4 MiB"
	spotServiceRedirects      = "the spot-price service redirects; set ENERGY_SPOT_PRICE_SERVICE_URL to the address it serves"
	spotServiceNotAList       = "the spot-price service did not answer a list of prices"
	spotServiceNotAreas       = "the spot-price service did not answer a list of areas"
	spotServiceUnknownSrc     = "the spot-price service answered an unknown source"
	spotServiceUnknownState   = "the spot-price service answered an unknown state"
	spotServiceNoVersion      = "the spot-price service answered an interval without a version"
)

// spotServiceProvenance is what upstream and rateSource must look like to be
// stored (plan reading 5): the column is varchar(20), and the contract's
// enum is read as text so the service may add sources.
var spotServiceProvenance = regexp.MustCompile(`^[a-z][a-z0-9_]{0,19}$`)

// errSpotServicePublic marks a dial the guard refused because the service's
// host resolved to a public address while the URL is http: the one refusal
// with its own sentence, since its remedy is the operator's.
var errSpotServicePublic = errors.New("energy: the spot-price service resolves to a public address over http")

// spotServiceClient is the service's client: its base URL (scheme and host
// only), the installation's key and the guarded HTTP client.
type spotServiceClient struct {
	base   *url.URL
	key    string
	http   *http.Client
	logger func() *slog.Logger
	now    func() time.Time
}

// newSpotServiceClient builds the client for raw, config.Load's validated
// ENERGY_SPOT_PRICE_SERVICE_URL. rt is the transport a test set
// (modtest.WithTransport), else the guarded transport is built — per
// worker, not once for the process, since it carries the scheme's rule.
// Either way the host-only round-tripper wraps it.
func newSpotServiceClient(raw, key string, rt http.RoundTripper, logger func() *slog.Logger, now func() time.Time) (*spotServiceClient, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("energy: the spot-price service's address must be an absolute http or https URL")
	}
	if rt == nil {
		rt = spotServiceGuardedTransport(net.DefaultResolver,
			(&net.Dialer{Timeout: spotFeedDialTimeout, KeepAlive: 30 * time.Second}).DialContext, u.Scheme == "http")
	}
	return &spotServiceClient{
		base:   &url.URL{Scheme: u.Scheme, Host: u.Host},
		key:    key,
		logger: logger,
		now:    now,
		http: &http.Client{
			Transport: spotServiceHostOnly{scheme: u.Scheme, host: u.Host, next: rt},
			Timeout:   spotFeedFetchBudget,
			// Redirects are never followed: the next address is one the
			// guard never saw. A 3xx is recorded as the failure it is.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// spotServiceGuardedTransport is the production transport: dialling through
// netguard's private-allowing guard over resolver, with requirePrivate when
// the URL is http. Proxy is nil, as the feed's, so no request goes to an
// address the guard did not check; compression is the transport's own, so
// it asks for gzip and decodes it before the cap is applied; keep-alives are
// on (design S7).
func spotServiceGuardedTransport(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error), requirePrivate bool) *http.Transport {
	guarded := netguard.DialContextAllowingPrivate(resolver, dial, requirePrivate)
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := guarded(ctx, network, address)
			if err != nil && requirePrivate && errors.Is(err, netguard.ErrDisallowed) && spotServiceResolvesPublic(ctx, resolver, address) {
				return nil, fmt.Errorf("%w: %w", errSpotServicePublic, err)
			}
			return conn, err
		},
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           4,
		MaxIdleConnsPerHost:    4,
		IdleConnTimeout:        spotServiceIdleConnTimeout,
		TLSHandshakeTimeout:    spotFeedTLSHandshakeTimeout,
		ResponseHeaderTimeout:  spotFeedResponseHeaderTimeout,
		MaxResponseHeaderBytes: spotFeedMaxResponseHeaderSize,
	}
}

// spotServiceResolvesPublic is whether address's host resolves to an
// address the ordinary guard would allow but that is not private: why
// requirePrivate refused it, rather than a loopback or reserved address
// every caller is refused. It runs only after a refusal, never before a
// dial.
func spotServiceResolvesPublic(ctx context.Context, resolver netguard.Resolver, address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	addrs := []netip.Addr{}
	if literal, err := netip.ParseAddr(host); err == nil {
		addrs = append(addrs, literal)
	} else if addrs, err = resolver.LookupNetIP(ctx, "ip", host); err != nil {
		return false
	}
	for _, a := range addrs {
		if !netguard.PrivateNetwork(a) && !netguard.Disallowed(a) {
			return true
		}
	}
	return false
}

// spotServiceHostOnly refuses, before anything is dialled, every request
// whose scheme and host are not the configured service's: over another
// scheme the same host is another rule of the guard (requirePrivate).
type spotServiceHostOnly struct {
	scheme, host string
	next         http.RoundTripper
}

func (h spotServiceHostOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme != h.scheme || req.URL.Host != h.host {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("%w: the spot-price service's client asks only its configured host", netguard.ErrDisallowed)
	}
	return h.next.RoundTrip(req)
}

// spotServiceFailure is a request that answered nothing usable: its
// sentence and, for a 429, the instant its Retry-After allows the next
// request (zero when it gave none). permanent is a failure asking again
// does not mend: the service refused the request itself (400), or answered
// what Vantigo cannot read — not a list, a time it cannot parse, an interval
// that does not end after it starts, more than the cap. Transport failures,
// timeouts, 401, 429 and 5xx are transient.
type spotServiceFailure struct {
	sentence    string
	retryAt     time.Time
	rateLimited bool
	permanent   bool
}

func (f spotServiceFailure) failed() bool { return f.sentence != "" }

// spotServicePermanentSentence is whether a recorded last_error is the
// sentence of a permanent failure: the history row keeps the sentence, not
// the failure, and every permanent sentence is one of these.
func spotServicePermanentSentence(sentence *string) bool {
	if sentence == nil {
		return false
	}
	switch *sentence {
	case spotServiceAnswered(http.StatusBadRequest), spotServiceNotAList, spotServiceNotAreas,
		spotServiceAreasTooLarge, spotServicePricesTooLarge, spotFeedBadTime, spotFeedBackwards:
		return true
	}
	return false
}

// get is one GET of path with query under ctx, answering the body of a 200
// of at most limit bytes once decoded, tooLarge the sentence past it.
func (c *spotServiceClient) get(ctx context.Context, path string, query url.Values, limit int, tooLarge string) ([]byte, spotServiceFailure) {
	target := *c.base
	target.Path = path
	target.RawQuery = query.Encode()
	return c.getURL(ctx, target.String(), limit, tooLarge)
}

func (c *spotServiceClient) getURL(ctx context.Context, target string, limit int, tooLarge string) ([]byte, spotServiceFailure) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, spotServiceFailure{sentence: spotServiceUnreachable}
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vantigo/"+buildinfo.Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, spotServiceFailure{sentence: spotServiceTransportFailure(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, spotServiceFailure{sentence: spotServiceRefusedKey}
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, spotServiceFailure{sentence: spotServiceRateLimited, rateLimited: true,
			retryAt: spotServiceRetryAt(resp.Header.Get("Retry-After"), c.now())}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, spotServiceFailure{sentence: spotServiceRedirects}
	case resp.StatusCode == http.StatusBadRequest:
		c.logProblem(resp.Body, req.URL.Path)
		return nil, spotServiceFailure{sentence: spotServiceAnswered(resp.StatusCode), permanent: true}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, spotServiceFailure{sentence: spotServiceAnswered(resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, spotServiceFailure{sentence: spotServiceTransportFailure(err)}
	}
	if len(raw) > limit {
		return nil, spotServiceFailure{sentence: tooLarge, permanent: true}
	}
	return raw, spotServiceFailure{}
}

// logProblem logs a 400's problem detail at debug level only (design S7):
// the service promises it never carries the key or a body, and it is
// never stored. The path is logged, never the query.
func (c *spotServiceClient) logProblem(body io.Reader, path string) {
	var problem struct {
		Detail string `json:"detail"`
	}
	raw, _ := io.ReadAll(io.LimitReader(body, maxSpotServiceProblem))
	if json.Unmarshal(raw, &problem) != nil {
		return
	}
	c.logger().Debug("the spot-price service refused a request", "worker", spotPriceWorkerName, "path", path, "detail", problem.Detail)
}

// maxSpotServiceRetryAfter is the longest a 429's Retry-After is honoured,
// in either form.
const maxSpotServiceRetryAfter = 24 * time.Hour

// spotServiceRetryAt is when a 429's Retry-After allows the next request:
// delay-seconds or an HTTP date (RFC 9110 §10.2.3), at most a day after
// now, zero when absent or unreadable.
func spotServiceRetryAt(header string, now time.Time) time.Time {
	header = strings.TrimSpace(header)
	if header == "" {
		return time.Time{}
	}
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil {
		if seconds < 0 {
			return time.Time{}
		}
		return now.Add(time.Duration(min(seconds, int64(maxSpotServiceRetryAfter/time.Second))) * time.Second)
	}
	if at, err := http.ParseTime(header); err == nil {
		if limit := now.Add(maxSpotServiceRetryAfter); at.After(limit) {
			return limit.UTC()
		}
		return at.UTC()
	}
	return time.Time{}
}

// spotServiceTransportFailure is the sentence of a request that answered
// nothing: the public address over http, any other refusal of the guard, a
// budget spent, or anything else.
func spotServiceTransportFailure(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, errSpotServicePublic):
		return spotServicePublic
	case errors.Is(err, netguard.ErrDisallowed):
		return spotServiceDisallowed
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return spotServiceTimedOut
	default:
		return spotServiceUnreachable
	}
}

// spotServiceAnswered is the sentence of a status that is none of the
// above: the code and its standard text, never the reason phrase sent.
func spotServiceAnswered(status int) string {
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("the spot-price service answered %d %s", status, text)
	}
	return fmt.Sprintf("the spot-price service answered %d", status)
}

// spotServiceArea is one zone of GET /api/v1/areas as the sync reads it:
// whether the service follows it, and the earliest interval start it holds
// (nil while its coverage is null or it holds none).
type spotServiceArea struct {
	code       string
	active     bool
	firstStart *time.Time
}

// areas is GET /api/v1/areas.
func (c *spotServiceClient) areas(ctx context.Context) ([]spotServiceArea, spotServiceFailure) {
	raw, fail := c.get(ctx, "/api/v1/areas", nil, maxSpotServiceAreas, spotServiceAreasTooLarge)
	if fail.failed() {
		return nil, fail
	}
	var entries []struct {
		Code     string `json:"code"`
		Active   bool   `json:"active"`
		Coverage *struct {
			FirstStart *time.Time `json:"firstStart"`
		} `json:"coverage"`
	}
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) || json.Unmarshal(raw, &entries) != nil {
		return nil, spotServiceFailure{sentence: spotServiceNotAreas, permanent: true}
	}
	out := make([]spotServiceArea, 0, len(entries))
	for _, e := range entries {
		a := spotServiceArea{code: e.Code, active: e.Active}
		if e.Coverage != nil && e.Coverage.FirstStart != nil {
			first := e.Coverage.FirstStart.UTC()
			a.firstStart = &first
		}
		out = append(out, a)
	}
	return out, spotServiceFailure{}
}

// spotServiceInterval is one interval of GET /api/v1/prices as Vantigo would
// store it. hasNok is false when the service has no NOK figure for it yet
// (row.NokPerKwh is then zero and the interval's day is never written), and
// failure is the sentence of a value Vantigo cannot take, which fails the
// interval's day.
type spotServiceInterval struct {
	row     spotPriceRow
	hasNok  bool
	failure string
}

// spotServiceEntry is one PriceInterval of the contract, every figure kept
// as the number's text.
type spotServiceEntry struct {
	Start          string       `json:"start"`
	End            string       `json:"end"`
	PriceEurPerMwh *json.Number `json:"priceEurPerMwh"`
	PriceNokPerKwh *json.Number `json:"priceNokPerKwh"`
	ExchangeRate   *json.Number `json:"exchangeRate"`
	RateSource     *string      `json:"rateSource"`
	State          string       `json:"state"`
	Source         string       `json:"source"`
	Version        *json.Number `json:"version"`
}

// prices is GET /api/v1/prices for area over [from, to), with currency=EUR
// so a day without a rate does not fail the window (design S5): the
// intervals ascending by start. A time Vantigo cannot read, or an interval
// that does not end after it starts, fails the whole answer; any other
// value fails the interval's day only.
func (c *spotServiceClient) prices(ctx context.Context, area string, from, to time.Time) ([]spotServiceInterval, spotServiceFailure) {
	raw, fail := c.get(ctx, "/api/v1/prices", url.Values{
		"area":     {area},
		"from":     {from.UTC().Format(time.RFC3339)},
		"to":       {to.UTC().Format(time.RFC3339)},
		"currency": {"EUR"},
	}, maxSpotServicePrices, spotServicePricesTooLarge)
	if fail.failed() {
		return nil, fail
	}
	var entries []spotServiceEntry
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) || dec.Decode(&entries) != nil {
		return nil, spotServiceFailure{sentence: spotServiceNotAList, permanent: true}
	}
	out := make([]spotServiceInterval, 0, len(entries))
	for _, e := range entries {
		start, errStart := time.Parse(time.RFC3339, e.Start)
		end, errEnd := time.Parse(time.RFC3339, e.End)
		switch {
		case errStart != nil || errEnd != nil:
			return nil, spotServiceFailure{sentence: spotFeedBadTime, permanent: true}
		case !end.After(start):
			return nil, spotServiceFailure{sentence: spotFeedBackwards, permanent: true}
		}
		out = append(out, decodeSpotServiceEntry(e, area, start.UTC(), end.UTC()))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].row.Start.Before(out[j].row.Start) })
	return out, spotServiceFailure{}
}

// decodeSpotServiceEntry reads one entry's figures and provenance (plan
// reading 5): eur_per_kwh is priceEurPerMwh with its decimal point moved
// three places on the number's text, never a float division; every figure
// is rounded as numeric(12,6) stores it and bounded by storablePrice.
func decodeSpotServiceEntry(e spotServiceEntry, area string, start, end time.Time) spotServiceInterval {
	iv := spotServiceInterval{row: spotPriceRow{PriceArea: area, Start: start, End: end}}
	fail := func(sentence string) spotServiceInterval {
		iv.failure = sentence
		return iv
	}
	if !spotServiceProvenance.MatchString(e.Source) || (e.RateSource != nil && !spotServiceProvenance.MatchString(*e.RateSource)) {
		return fail(spotServiceUnknownSrc)
	}
	if e.State != spotPriceStateFinal && e.State != spotPriceStatePreliminary {
		return fail(spotServiceUnknownState)
	}
	if e.Version == nil {
		return fail(spotServiceNoVersion)
	}
	version, err := strconv.ParseInt(e.Version.String(), 10, 32)
	if err != nil {
		return fail(spotServiceNoVersion)
	}
	if e.PriceEurPerMwh == nil {
		return fail(spotFeedBadPrice)
	}
	eur, ok := spotServiceFigure(*e.PriceEurPerMwh, -3)
	if !ok {
		return fail(spotFeedBadPrice)
	}
	iv.row.EurPerKwh = &eur
	if e.PriceNokPerKwh != nil {
		nok, ok := spotServiceFigure(*e.PriceNokPerKwh, 0)
		if !ok {
			return fail(spotFeedBadPrice)
		}
		iv.row.NokPerKwh, iv.hasNok = nok, true
	}
	if e.ExchangeRate != nil {
		exr, ok := spotServiceFigure(*e.ExchangeRate, 0)
		if !ok {
			return fail(spotFeedBadPrice)
		}
		iv.row.ExchangeRate = &exr
	}
	upstream := e.Source
	v := int32(version)
	iv.row.State, iv.row.Upstream, iv.row.RateSource, iv.row.UpstreamVersion = e.State, &upstream, e.RateSource, &v
	return iv
}

// spotServiceFigure is n with its decimal point moved shift places — by
// adding shift to the number's own exponent, so the text is parsed once,
// correctly rounded, with no float arithmetic — rounded to six decimals
// and within what numeric(12,6) stores.
func spotServiceFigure(n json.Number, shift int) (float64, bool) {
	text := n.String()
	mantissa, exponent := text, 0
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		e, err := strconv.Atoi(text[i+1:])
		if err != nil {
			return 0, false
		}
		mantissa, exponent = text[:i], e
	}
	v, err := strconv.ParseFloat(mantissa+"e"+strconv.Itoa(exponent+shift), 64)
	if err != nil || !storablePrice(&v) {
		return 0, false
	}
	return roundPrice(v), true
}
