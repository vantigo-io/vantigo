// Package bring is the Bring adapter of the shipping port (orders design O49,
// research R §2.9): the Mybring user's three headers on every call, the test
// mode's header, the Shipping Guide (grams and centimetres) for rates, the
// Booking API (kilograms) for bookings, the label behind its redirect, the
// Tracking API for tracking and for finding a booking by its reference, and
// the Pickup Point API. Bring has no cancel call and no idempotency key: a
// cancel answers CancelNotSupported, and the caller's key travels as the
// consignment's correlationId and sender reference, which a retry looks up
// through tracking before it books again.
//
// The products are data (products.json), never Go constants: Bring withdraws
// products yearly. Answers are decoded tolerantly — Bring changes its API in
// place, with no new version, so an unknown field is ignored. Certificates
// are not pinned (Bring moved to Let's Encrypt in 2025). Every call is bounded
// by one deadline, and nothing is retried inside the adapter.
package bring

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/shipping"
)

const (
	// DefaultBaseURL is Bring's API.
	DefaultBaseURL = "https://api.bring.com"
	// callTimeout bounds one method call, every request it makes included.
	callTimeout = 30 * time.Second
	// trackTimeout bounds one reference's tracking, so one that hangs does
	// not hold up the rest of a poll.
	trackTimeout = 10 * time.Second
	// maxAnswerBytes bounds any answer read, a label's included.
	maxAnswerBytes = 8 << 20
	// maxRedirects bounds the label's redirects.
	maxRedirects = 5
	// trackingPage is Bring's public tracking page of a consignment.
	trackingPage = "https://tracking.bring.com/tracking/"
)

// The paths the adapter calls, under the base URL.
const (
	pathShippingGuide = "/shippingguide/api/v2/products"
	pathBooking       = "/booking/api/create"
	pathTracking      = "/tracking/api/v2/tracking.json"
	pathPickupPoints  = "/pickuppoint/api/pickuppoint/"
)

//go:embed products.json
var productsJSON []byte

// product is one entry of products.json.
type product struct {
	Code        string `json:"code"`
	NameEN      string `json:"nameEn"`
	NameNB      string `json:"nameNb"`
	Segment     string `json:"segment"`
	PickupPoint bool   `json:"pickupPoint"`
	Waybill     bool   `json:"waybill"`
}

// products is the data file, read once.
var products = func() []product {
	var file struct {
		Products []product `json:"products"`
	}
	if err := json.Unmarshal(productsJSON, &file); err != nil {
		panic("bring: products.json does not decode: " + err.Error())
	}
	return file.Products
}()

// productOf is the data file's entry of code, false when it lists none.
func productOf(code string) (product, bool) {
	i := slices.IndexFunc(products, func(p product) bool { return p.Code == code })
	if i < 0 {
		return product{}, false
	}
	return products[i], true
}

// Options is one Mybring user's keys and the account's customer numbers.
type Options struct {
	// BaseURL is DefaultBaseURL when empty; a test's bringtest server's
	// otherwise. It must be https.
	BaseURL string
	// UID is the Mybring login (e-mail) and Key its API key.
	UID, Key string
	// ClientURL is the X-Bring-Client-URL header: the URL of the
	// installation calling.
	ClientURL string
	// CustomerNumbers are the account's: the first books parcels, the second
	// (when there is one) cargo.
	CustomerNumbers []string
	// Test sends X-Bring-Test-Indicator: true, Bring's test mode.
	Test bool
	// Transport is nil for http.DefaultTransport.
	Transport http.RoundTripper
	// Now is the clock; nil is the wall clock.
	Now func() time.Time
	// Timeout bounds each call; callTimeout when zero.
	Timeout time.Duration
}

// Client is the adapter for one carrier account. It is safe for concurrent use.
type Client struct {
	base      *url.URL
	uid, key  string
	clientURL string
	customers []string
	test      bool
	http      *http.Client
	now       func() time.Time
	timeout   time.Duration
}

var _ shipping.Carrier = (*Client)(nil)

// New is the adapter for o's account.
func New(o Options) (*Client, error) {
	raw := o.BaseURL
	if raw == "" {
		raw = DefaultBaseURL
	}
	base, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, errors.New("bring: the base URL must be an https URL")
	}
	if strings.TrimSpace(o.UID) == "" || strings.TrimSpace(o.Key) == "" {
		return nil, errors.New("bring: the Mybring user id and API key are both required")
	}
	if len(o.CustomerNumbers) == 0 || slices.Contains(o.CustomerNumbers, "") {
		return nil, errors.New("bring: at least one customer number is required")
	}
	clientURL := o.ClientURL
	if clientURL == "" {
		clientURL = "https://vantigo.io"
	}
	transport := o.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = callTimeout
	}
	c := &Client{
		base: base, uid: o.UID, key: o.Key, clientURL: clientURL, customers: slices.Clone(o.CustomerNumbers),
		test: o.Test, now: now, timeout: timeout,
	}
	c.http = &http.Client{Transport: transport, CheckRedirect: c.checkRedirect}
	return c, nil
}

// checkRedirect follows a label's redirect, at most maxRedirects times and
// only to https. The Mybring keys go only to Bring's own host: net/http
// forwards every header but a few it knows, so they are taken off a request
// to any other host.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("bring: too many redirects")
	}
	if req.URL.Scheme != "https" {
		return errors.New("bring: a redirect to a URL that is not https")
	}
	if req.URL.Host != c.base.Host {
		for _, h := range []string{"X-Mybring-API-Uid", "X-Mybring-API-Key"} {
			req.Header.Del(h)
		}
	}
	return nil
}

// Capabilities: Bring answers prices and pickup points, issues PDF labels
// only, has no cancel call and, in this phase, no webhook (Event Cast is
// unsigned; it is polled instead).
func (c *Client) Capabilities() shipping.Capabilities { return Capabilities() }

// Capabilities are Bring's, which need no account to read: a caller voids a
// booking (Bring has no cancel) without opening the account's keys.
func Capabilities() shipping.Capabilities {
	return shipping.Capabilities{Rates: true, PickupPoints: true, LabelFormats: []shipping.Format{shipping.FormatPDF}}
}

// Services are the data file's products.
func (c *Client) Services(context.Context) ([]shipping.Service, error) {
	return Services(), nil
}

// Services are the products of the data file, which need no account to read:
// a settings screen lists them before any key is stored.
func Services() []shipping.Service {
	out := make([]shipping.Service, 0, len(products))
	for _, p := range products {
		out = append(out, shipping.Service{
			Code: p.Code, NameEN: p.NameEN, NameNB: p.NameNB, Segment: p.Segment, PickupPoint: p.PickupPoint, Waybill: p.Waybill,
		})
	}
	return out
}

// customerFor is the customer number a product is booked and priced on: the
// second for cargo when the account has two, else the first.
func (c *Client) customerFor(code string) string {
	if p, ok := productOf(code); ok && p.Segment == "cargo" && len(c.customers) > 1 {
		return c.customers[1]
	}
	return c.customers[0]
}

// Cancel: Bring has no cancel call. The booking is voided by the caller, and
// its label must not be used; Bring bills on hand-in.
func (c *Client) Cancel(context.Context, shipping.Booking, string) (shipping.CancelOutcome, error) {
	return shipping.CancelNotSupported, nil
}

// ParseWebhook: not in this phase (Capabilities.Webhooks is false).
func (c *Client) ParseWebhook(shipping.WebhookRequest, string) ([]shipping.TrackingStatus, error) {
	return nil, shipping.ErrNotSupported
}

// answer is a response as read.
type answer struct {
	status int
	header http.Header
	body   []byte
}

// call makes one request with the account's headers and reads its answer
// whole. A transport failure or the deadline is its error.
func (c *Client) call(ctx context.Context, op, method, target string, body any) (answer, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return answer{}, fmt.Errorf("bring: encode the %s: %w", op, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return answer{}, fmt.Errorf("bring: build the %s request: %w", op, err)
	}
	// No replay inside net/http either: a booking sent twice is two bookings.
	req.GetBody = nil
	req.Header.Set("X-Mybring-API-Uid", c.uid)
	req.Header.Set("X-Mybring-API-Key", c.key)
	req.Header.Set("X-Bring-Client-URL", c.clientURL)
	req.Header.Set("User-Agent", "vantigo/"+buildinfo.Version)
	if op == "label" {
		req.Header.Set("Accept", "application/pdf")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.test {
		req.Header.Set("X-Bring-Test-Indicator", "true")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return answer{}, fmt.Errorf("bring: %s: %w", op, withoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	read, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	if err != nil {
		return answer{}, fmt.Errorf("bring: read Bring's answer to %s: %w", op, err)
	}
	if len(read) > maxAnswerBytes {
		return answer{}, fmt.Errorf("bring: Bring's answer to %s is larger than %d bytes", op, maxAnswerBytes)
	}
	return answer{status: resp.StatusCode, header: resp.Header, body: read}, nil
}

// classify maps a non-2xx answer to the port's errors: 401 and 403 are the
// keys, 404 not found, a 400 or 422 with Bring's messages a refusal, and
// anything else — a 5xx above all — an unknown outcome.
func classify(op string, a answer) error {
	switch {
	case a.status >= 200 && a.status < 300:
		return nil
	case a.status == http.StatusUnauthorized || a.status == http.StatusForbidden:
		return shipping.ErrUnauthorized
	case a.status == http.StatusNotFound:
		return shipping.ErrNotFound
	case a.status == http.StatusBadRequest || a.status == http.StatusUnprocessableEntity:
		code, msg := problemOf(a.body)
		return &shipping.CarrierRefusal{Code: code, Message: msg}
	}
	return fmt.Errorf("bring: %s: Bring answered %d", op, a.status)
}

// problemOf reads Bring's error shapes, which differ per API: a list of
// errors with messages, or one message.
func problemOf(body []byte) (code, message string) {
	var p struct {
		Code     any    `json:"code"`
		Message  string `json:"message"`
		Messages []struct {
			Message string `json:"message"`
		} `json:"messages"`
		Errors []bringError `json:"errors"`
	}
	if json.Unmarshal(body, &p) != nil {
		return "", ""
	}
	if len(p.Errors) > 0 {
		return p.Errors[0].code(), p.Errors[0].message()
	}
	if p.Message == "" && len(p.Messages) > 0 {
		p.Message = p.Messages[0].Message
	}
	if p.Code != nil {
		code = fmt.Sprint(p.Code)
	}
	return code, p.Message
}

// bringError is one entry of an errors list: Booking's has messages per
// language, the Shipping Guide's a description.
type bringError struct {
	Code        any    `json:"code"`
	Description string `json:"description"`
	Message     string `json:"message"`
	Messages    []struct {
		Lang    string `json:"lang"`
		Message string `json:"message"`
	} `json:"messages"`
}

func (e bringError) code() string {
	if e.Code == nil {
		return ""
	}
	return fmt.Sprint(e.Code)
}

// message is the English message when there is one, else the first.
func (e bringError) message() string {
	for _, m := range e.Messages {
		if strings.EqualFold(m.Lang, "en") && m.Message != "" {
			return m.Message
		}
	}
	if len(e.Messages) > 0 {
		return e.Messages[0].Message
	}
	if e.Description != "" {
		return e.Description
	}
	return e.Message
}

// withoutURL is err less the *url.Error around it, which names the URL.
func withoutURL(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}

// endpoint is the base URL with path and query.
func (c *Client) endpoint(path string, query url.Values) string {
	u := *c.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return u.String()
}
