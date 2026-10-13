// Package vipps is the Vipps MobilePay ePayment adapter of the payments port
// (invoices design D13, research R4 §4.4–§4.7): the access token and its
// cache, the sales unit's keys and the system headers on every call, the
// caller's Idempotency-Key on create, capture, cancel and refund, Vipps'
// problems mapped to the port's typed errors, and webhook verification.
//
// The client is the Storecove adapter's shape: the base URL is the
// operator's, so it dials unguarded through the transport the caller hands
// it (Deps.HTTPTransport, http.DefaultTransport when nil), never follows a
// redirect, and bounds every call by one thirty-second deadline. It never
// retries: the caller retries under its persisted key, so a timeout after
// Vipps accepted cannot become a second payment or a second capture. The one
// repeat is a call Vipps answered 401, made once more under a fresh token.
package vipps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
	"github.com/vantigo-io/vantigo/server/internal/payments"
)

const (
	// callTimeout bounds one method call, every request it makes included.
	callTimeout = 30 * time.Second
	// maxAnswerBytes bounds any answer read, the QR image's included.
	maxAnswerBytes = 1 << 20
	// systemName and pluginName are the Vipps-System-* headers' names.
	systemName = "vantigo"
	pluginName = "vantigo-invoices"
	// maxHeaderChars is the length Vipps allows a system header.
	maxHeaderChars = 30
)

// Events is every ePayment webhook event, the registration's list.
var Events = []string{
	"epayments.payment.created.v1",
	"epayments.payment.authorized.v1",
	"epayments.payment.aborted.v1",
	"epayments.payment.expired.v1",
	"epayments.payment.cancelled.v1",
	"epayments.payment.captured.v1",
	"epayments.payment.refunded.v1",
	"epayments.payment.terminated.v1",
}

// referencePattern is Vipps' rule for a reference (R4 §4.4).
var referencePattern = regexp.MustCompile(`^[a-zA-Z0-9-]{8,64}$`)

// Options is one sales unit's keys and how to reach Vipps.
type Options struct {
	// BaseURL is https://api.vipps.no, or https://apitest.vipps.no for the
	// test environment.
	BaseURL, ClientID, ClientSecret, SubscriptionKey, MSN string
	// SystemVersion is the release named in the system headers;
	// buildinfo.Version when empty.
	SystemVersion string
	// Transport is nil for http.DefaultTransport; Deps.HTTPTransport in the module.
	Transport http.RoundTripper
	// Now is the clock the token's expiry and a Retry-After date are judged
	// against; nil is the wall clock.
	Now func() time.Time
}

// Client is the adapter for one sales unit. It is safe for concurrent use.
type Client struct {
	baseURL                                      string
	clientID, clientSecret, subscriptionKey, msn string
	version                                      string
	http                                         *http.Client
	now                                          func() time.Time
	timeout                                      time.Duration
	tokens                                       tokenCache
}

var _ payments.Provider = (*Client)(nil)

// New is the adapter for o's sales unit.
func New(o Options) (*Client, error) {
	base, err := url.Parse(o.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, errors.New("vipps: the base URL must be an https URL")
	}
	if o.ClientID == "" || o.ClientSecret == "" || o.SubscriptionKey == "" || o.MSN == "" {
		return nil, errors.New("vipps: the client id, client secret, subscription key and sales unit are all required")
	}
	transport := o.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	version := o.SystemVersion
	if version == "" {
		version = buildinfo.Version
	}
	return &Client{
		baseURL:  strings.TrimSuffix(o.BaseURL, "/"),
		clientID: o.ClientID, clientSecret: o.ClientSecret, subscriptionKey: o.SubscriptionKey, msn: o.MSN,
		version: truncate(version, maxHeaderChars),
		// A 3xx is answered as the non-2xx it is: following one would send
		// the keys to a host no operator named.
		http:    &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now:     now,
		timeout: callTimeout,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// amount is Vipps' {currency, value}, value in minor units.
type amount struct {
	Currency string `json:"currency"`
	Value    int64  `json:"value"`
}

func amountOf(m payments.Money) amount { return amount{Currency: m.Currency, Value: m.Minor} }

func (a amount) money() payments.Money { return payments.Money{Currency: a.Currency, Minor: a.Value} }

// paymentBody is the part of GET's answer, and of a modification's, the port
// reads. The amounts are pointers so that one Vipps left out is told from
// zero.
type paymentBody struct {
	Reference    string  `json:"reference"`
	PSPReference string  `json:"pspReference"`
	State        string  `json:"state"`
	RedirectURL  string  `json:"redirectUrl"`
	Amount       *amount `json:"amount"`
	Aggregate    *struct {
		AuthorizedAmount *amount `json:"authorizedAmount"`
		CapturedAmount   *amount `json:"capturedAmount"`
		CancelledAmount  *amount `json:"cancelledAmount"`
		RefundedAmount   *amount `json:"refundedAmount"`
	} `json:"aggregate"`
	CaptureGuaranteedUntil string `json:"captureGuaranteedUntil"`
}

// paymentOf decodes a payment answered for reference. currency is the
// request's, empty when it named none (a read, a cancel): then the payment's
// own amount's. A body that does not decode, names another reference or a
// state the port does not know, or lacks the aggregate or any of its four
// amounts, or holds one in another currency, is an unknown outcome — never a
// guess, and never a zero.
func paymentOf(reference, currency string, body []byte) (payments.Payment, error) {
	var b paymentBody
	if err := json.Unmarshal(body, &b); err != nil {
		return payments.Payment{}, errors.New("vipps: the payment Vipps answered does not decode")
	}
	if b.Reference != reference {
		return payments.Payment{}, errors.New("vipps: Vipps answered a payment under another reference")
	}
	state := payments.State(b.State)
	switch state {
	case payments.StateCreated, payments.StateAuthorized, payments.StateAborted, payments.StateExpired, payments.StateTerminated:
	default:
		return payments.Payment{}, fmt.Errorf("vipps: Vipps answered a state the port does not know: %q", b.State)
	}
	if currency == "" && b.Amount != nil {
		currency = b.Amount.Currency
	}
	if currency == "" || b.Aggregate == nil {
		return payments.Payment{}, errors.New("vipps: Vipps answered a payment without its amounts")
	}
	amounts := []*amount{b.Aggregate.AuthorizedAmount, b.Aggregate.CapturedAmount, b.Aggregate.CancelledAmount, b.Aggregate.RefundedAmount}
	for _, a := range amounts {
		if a == nil || a.Currency != currency {
			return payments.Payment{}, errors.New("vipps: Vipps answered a payment with an amount missing or in another currency")
		}
	}
	p := payments.Payment{
		Reference: b.Reference, PSPReference: b.PSPReference, State: state,
		Authorized: amounts[0].money(), Captured: amounts[1].money(), Cancelled: amounts[2].money(), Refunded: amounts[3].money(),
	}
	if b.CaptureGuaranteedUntil != "" {
		until, err := time.Parse(time.RFC3339, b.CaptureGuaranteedUntil)
		if err != nil {
			return payments.Payment{}, errors.New("vipps: Vipps answered a captureGuaranteedUntil that does not parse")
		}
		p.CaptureGuaranteedUntil = &until
	}
	return p, nil
}

// checkReference refuses a reference Vipps would refuse, before any request.
func checkReference(reference string) error {
	if !referencePattern.MatchString(reference) {
		return fmt.Errorf("%w: a reference is 8–64 letters, digits and hyphens", payments.ErrInvalidRequest)
	}
	return nil
}

// checkKey refuses a missing or overlong Idempotency-Key: the key is the
// caller's, never one the adapter makes up.
func checkKey(key string) error {
	if key == "" || len(key) > 50 {
		return fmt.Errorf("%w: an idempotency key of 1–50 characters is required", payments.ErrInvalidRequest)
	}
	return nil
}

// checkAmount refuses an amount Vipps would refuse: NOK, and at least one
// øre (Create narrows it to Vipps' range).
func checkAmount(m payments.Money) error {
	if m.Currency != "NOK" || m.Minor <= 0 {
		return fmt.Errorf("%w: an amount is a positive number of øre in NOK", payments.ErrInvalidRequest)
	}
	return nil
}

func paymentPath(reference string) string {
	return "/epayment/v1/payments/" + url.PathEscape(reference)
}

// Get is GET /epayment/v1/payments/{reference}.
func (c *Client) Get(ctx context.Context, reference string) (payments.Payment, error) {
	if err := checkReference(reference); err != nil {
		return payments.Payment{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.get(ctx, reference)
}

func (c *Client) get(ctx context.Context, reference string) (payments.Payment, error) {
	a, err := c.call(ctx, "get", http.MethodGet, paymentPath(reference), nil, "")
	if err != nil {
		return payments.Payment{}, err
	}
	if err := c.classify("get", a); err != nil {
		return payments.Payment{}, err
	}
	return paymentOf(reference, "", a.body)
}

// Capture is POST …/{reference}/capture under the caller's key. Vipps'
// answer carries the aggregate, so the caller judges capturedAmount, never
// the status alone (R4 §4.4).
func (c *Client) Capture(ctx context.Context, reference string, m payments.Money, idempotencyKey string) (payments.Payment, error) {
	return c.modify(ctx, "capture", reference, &m, idempotencyKey)
}

// Refund is POST …/{reference}/refund under the caller's key.
func (c *Client) Refund(ctx context.Context, reference string, m payments.Money, idempotencyKey string) (payments.Payment, error) {
	return c.modify(ctx, "refund", reference, &m, idempotencyKey)
}

// Cancel is POST …/{reference}/cancel under the caller's key. Vipps answers
// a second cancel with 6050 or 6190, "already cancelled", which is success:
// the payment is then read and answered as Vipps holds it.
func (c *Client) Cancel(ctx context.Context, reference string, idempotencyKey string) (payments.Payment, error) {
	return c.modify(ctx, "cancel", reference, nil, idempotencyKey)
}

func (c *Client) modify(ctx context.Context, op, reference string, m *payments.Money, key string) (payments.Payment, error) {
	if err := checkReference(reference); err != nil {
		return payments.Payment{}, err
	}
	if err := checkKey(key); err != nil {
		return payments.Payment{}, err
	}
	body := []byte("{}")
	if m != nil {
		if err := checkAmount(*m); err != nil {
			return payments.Payment{}, err
		}
		raw, err := json.Marshal(map[string]amount{"modificationAmount": amountOf(*m)})
		if err != nil {
			return payments.Payment{}, fmt.Errorf("vipps: encode the %s: %w", op, err)
		}
		body = raw
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	a, err := c.call(ctx, op, http.MethodPost, paymentPath(reference)+"/"+op, body, key)
	if err != nil {
		return payments.Payment{}, err
	}
	if op == "cancel" && alreadyCancelled(a) {
		return c.get(ctx, reference)
	}
	if err := c.classify(op, a); err != nil {
		return payments.Payment{}, err
	}
	currency := ""
	if m != nil {
		currency = m.Currency
	}
	return paymentOf(reference, currency, a.body)
}

// event is one entry of a payment's event log.
type event struct {
	Name      string `json:"name"`
	Timestamp string `json:"timestamp"`
	Success   bool   `json:"success"`
}

// CaptureTime is GET …/{reference}/events: the earliest successful CAPTURED
// event's time — the log is "the authoritative data" for a payment (R4 §4.4).
func (c *Client) CaptureTime(ctx context.Context, reference string) (time.Time, bool, error) {
	if err := checkReference(reference); err != nil {
		return time.Time{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	a, err := c.call(ctx, "events", http.MethodGet, paymentPath(reference)+"/events", nil, "")
	if err != nil {
		return time.Time{}, false, err
	}
	if err := c.classify("events", a); err != nil {
		return time.Time{}, false, err
	}
	var events []event
	if err := json.Unmarshal(a.body, &events); err != nil {
		return time.Time{}, false, errors.New("vipps: the event log Vipps answered does not decode")
	}
	// The earliest by its time, not the first in the list: the API does not
	// promise the log's order.
	var first time.Time
	found := false
	for _, e := range events {
		if e.Name != "CAPTURED" || !e.Success {
			continue
		}
		at, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil {
			return time.Time{}, false, errors.New("vipps: a capture event's timestamp does not parse")
		}
		if !found || at.Before(first) {
			first, found = at, true
		}
	}
	return first, found, nil
}

const webhooksPath = "/webhooks/v1/webhooks"

// Verify is GET /webhooks/v1/webhooks: a token, then an authenticated read
// that names the sales unit — the keys and the MSN together.
func (c *Client) Verify(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	a, err := c.call(ctx, "webhooks", http.MethodGet, webhooksPath, nil, "")
	if err != nil {
		return err
	}
	return c.classify("webhooks", a)
}

// RegisterWebhook is POST /webhooks/v1/webhooks: Vipps answers the
// registration's id and the secret its callbacks are signed under.
func (c *Client) RegisterWebhook(ctx context.Context, target string, events []string) (id, secret string, err error) {
	body, err := json.Marshal(map[string]any{"url": target, "events": events})
	if err != nil {
		return "", "", fmt.Errorf("vipps: encode the registration: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	a, err := c.call(ctx, "webhooks", http.MethodPost, webhooksPath, body, "")
	if err != nil {
		return "", "", err
	}
	if err := c.classify("webhooks", a); err != nil {
		return "", "", err
	}
	var registered struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(a.body, &registered); err != nil || registered.ID == "" || registered.Secret == "" {
		return "", "", errors.New("vipps: Vipps registered the webhook but answered no id and secret")
	}
	return registered.ID, registered.Secret, nil
}

// DeleteWebhook is DELETE /webhooks/v1/webhooks/{id}.
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	a, err := c.call(ctx, "webhooks", http.MethodDelete, webhooksPath+"/"+url.PathEscape(id), nil, "")
	if err != nil {
		return err
	}
	return c.classify("webhooks", a)
}

// answer is a response as read: its status, body and Retry-After.
type answer struct {
	status     int
	body       []byte
	retryAfter string
}

// call makes one authenticated request and reads its answer whole. A 401 is
// answered by dropping the token, fetching a fresh one and making the
// request once more — the token may have expired early; a second 401 is the
// keys. A transport failure or the deadline is its error.
func (c *Client) call(ctx context.Context, op, method, path string, body []byte, key string) (answer, error) {
	token, err := c.token(ctx)
	if err != nil {
		return answer{}, err
	}
	a, err := c.send(ctx, op, method, path, body, key, token)
	if err != nil || a.status != http.StatusUnauthorized {
		return a, err
	}
	c.tokens.drop(token)
	if token, err = c.token(ctx); err != nil {
		return answer{}, err
	}
	return c.send(ctx, op, method, path, body, key, token)
}

func (c *Client) send(ctx context.Context, op, method, path string, body []byte, key, token string) (answer, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return answer{}, fmt.Errorf("vipps: build the request: %w", err)
	}
	// net/http replays a request whose reused connection broke before an
	// answer when it can rebuild the body and the request is idempotent —
	// which an Idempotency-Key header makes a POST. No retry inside the
	// adapter means none there either: without GetBody nothing is replayed.
	req.GetBody = nil
	c.setUnitHeaders(req)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return c.do(req, op)
}

// setUnitHeaders sets the sales unit's subscription key and MSN and the four
// system headers, which every request carries (R4 §4.6).
func (c *Client) setUnitHeaders(req *http.Request) {
	req.Header.Set("Ocp-Apim-Subscription-Key", c.subscriptionKey)
	req.Header.Set("Merchant-Serial-Number", c.msn)
	req.Header.Set("Vipps-System-Name", systemName)
	req.Header.Set("Vipps-System-Version", c.version)
	req.Header.Set("Vipps-System-Plugin-Name", pluginName)
	req.Header.Set("Vipps-System-Plugin-Version", c.version)
}

func (c *Client) do(req *http.Request, op string) (answer, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return answer{}, fmt.Errorf("vipps: %s: %w", op, withoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	read, err := readLimited(resp.Body)
	if err != nil {
		return answer{}, fmt.Errorf("vipps: read Vipps' answer to %s: %w", op, err)
	}
	return answer{status: resp.StatusCode, body: read, retryAfter: resp.Header.Get("Retry-After")}, nil
}

// withoutURL is err less the *url.Error around it, which names the URL.
func withoutURL(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}

// readLimited reads r whole, refusing more than maxAnswerBytes.
func readLimited(r io.Reader) ([]byte, error) {
	read, err := io.ReadAll(io.LimitReader(r, maxAnswerBytes+1))
	if err != nil {
		return nil, err
	}
	if len(read) > maxAnswerBytes {
		return nil, fmt.Errorf("larger than %d bytes", maxAnswerBytes)
	}
	return read, nil
}
