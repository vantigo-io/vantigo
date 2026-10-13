// Package vippstest is an httptest Vipps MobilePay for tests only: the
// shapes of the access-token, ePayment and Webhooks APIs that the Vipps
// adapter speaks (invoices design D13, research R4 §4.4–§4.8), served from
// one TLS listener so every test package — the adapter's, the module's, the
// integration test — drives the real adapter over real HTTP without touching
// the network. storecovetest is the same idea for the access point.
//
// It is an ordinary package, not a _test file, so other packages' tests can
// import it; nothing outside a test ever should.
//
// What it does: a payment created under a reference starts CREATED and moves
// along the states a test scripts for it, one step per read, or to
// AUTHORIZED by Approve (and the test environment's approve endpoint); its
// four amounts move with capture, cancel and refund as Vipps documents them,
// and every operation lands in its event log. Create, capture, cancel and
// refund are idempotent by key — the same key twice answers the first answer
// again and changes nothing; a reference already used answers 409 with 4150.
// A QR is asked for as TEXT/TARGETURL and answered as a target URL
// (QRTarget), which the adapter renders itself. A created amount of 151 øre is the test
// environment's insufficient funds: its capture is refused with 6260. Fail
// answers an operation with any status and error code, a reset connection or
// a redirect or a body of the test's own, on demand. RefundInPortal is a refund made in the business
// portal; SignAndPost a signed webhook to a URL the test gives.
//
// A client reaches it through Transport, which trusts the server's
// certificate — a harness passes it as Deps.HTTPTransport.
package vippstest

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The credentials the server accepts.
const (
	ClientID        = "vippstest-client-id"
	ClientSecret    = "vippstest-client-secret"
	SubscriptionKey = "vippstest-subscription-key"
	MSN             = "123456"
	// TokenLifetime is the expires_in of every token, in seconds: an hour, as
	// in Vipps' test environment.
	TokenLifetime = 3600
	// InsufficientFundsAmount is the test environment's special amount (øre)
	// whose capture is refused for lack of funds.
	InsufficientFundsAmount = 151
)

// The payment states.
const (
	Created    = "CREATED"
	Authorized = "AUTHORIZED"
	Aborted    = "ABORTED"
	Expired    = "EXPIRED"
	Terminated = "TERMINATED"
)

// The provider's error codes the server answers on its own.
const (
	CodeInsufficientFunds = "6260"
	CodeCaptureFailed     = "6280"
	CodeAlreadyCancelled  = "6050"
	CodeFullyCaptured     = "6040"
	CodeReferenceReused   = "4150"
	CodeAmountTooHigh     = "6110"
)

// Op names the operation a Fail applies to.
type Op string

const (
	OpToken    Op = "token"
	OpCreate   Op = "create"
	OpGet      Op = "get"
	OpEvents   Op = "events"
	OpCapture  Op = "capture"
	OpCancel   Op = "cancel"
	OpRefund   Op = "refund"
	OpWebhooks Op = "webhooks"
)

// Failure is how an operation is answered instead of normally.
type Failure struct {
	// Status is the HTTP status; with Code, the body is a problem naming it
	// in extraDetails.
	Status int
	Code   string
	// RetryAfter is the Retry-After header, when set.
	RetryAfter string
	// Reset closes the connection without an answer.
	Reset bool
	// Redirect answers 302 to this URL.
	Redirect string
	// Body, when set, is answered as it is under Status (200 when zero), in
	// place of a problem.
	Body []byte
	// Times is how many requests it answers; 0 is every one until Clear.
	Times int
}

// Request is one request the server received, in order.
type Request struct {
	Method, Path string
	Header       http.Header
	Body         []byte
}

// Event is one entry of a payment's event log.
type Event struct {
	Reference      string `json:"reference"`
	PSPReference   string `json:"pspReference"`
	Name           string `json:"name"`
	Amount         Amount `json:"amount"`
	Timestamp      string `json:"timestamp"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	Success        bool   `json:"success"`
}

// Amount is Vipps' {currency, value}.
type Amount struct {
	Currency string `json:"currency"`
	Value    int64  `json:"value"`
}

// Payment is a payment as the server holds it.
type Payment struct {
	Reference, PSPReference, State, RedirectURL string
	Amount                                      int64
	// The four aggregate amounts.
	Authorized, Captured, Cancelled, Refunded int64
	// UserFlow, CustomerInteraction and the rest of the create request as
	// sent, decoded.
	Request map[string]any
	// CaptureGuaranteedUntil is answered when set.
	CaptureGuaranteedUntil *time.Time
	script                 []string
}

// Webhook is one registration.
type Webhook struct {
	ID, URL, Secret string
	Events          []string
}

// Server is the fake. Its methods are safe for concurrent use.
type Server struct {
	srv *httptest.Server

	mu            sync.Mutex
	now           func() time.Time
	requests      []Request
	tokens        map[string]bool
	tokenFetches  int
	payments      map[string]*Payment
	scripts       map[string][]string
	guarantees    map[string]time.Time
	events        map[string][]Event
	answers       map[string]answer
	failures      map[Op]*Failure
	webhooks      []Webhook
	nextPSP       int
	nextWebhookID int
}

type answer struct {
	status int
	body   []byte
}

// New starts a server for t and closes it when t ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		now:        time.Now,
		tokens:     map[string]bool{},
		payments:   map[string]*Payment{},
		scripts:    map[string][]string{},
		guarantees: map[string]time.Time{},
		events:     map[string][]Event{},
		answers:    map[string]answer{},
		failures:   map[Op]*Failure{},
		nextPSP:    9_000_000_000,
	}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the base URL to point the adapter at: https, no trailing slash.
func (s *Server) URL() string { return s.srv.URL }

// Transport is a RoundTripper that trusts the server's certificate.
func (s *Server) Transport() http.RoundTripper { return s.srv.Client().Transport }

// SetNow sets the clock the event log's timestamps are read from.
func (s *Server) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// Script sets the states a payment under reference moves through, one per
// read (GET) after its creation; the last one stays. It may be called before
// the payment is created. Reaching AUTHORIZED reserves the created amount.
func (s *Server) Script(reference string, states ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.payments[reference]; ok {
		p.script = append([]string(nil), states...)
		return
	}
	s.scripts[reference] = append([]string(nil), states...)
}

// GuaranteeCapture sets the captureGuaranteedUntil a read answers.
func (s *Server) GuaranteeCapture(reference string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guarantees[reference] = until
}

// Approve authorizes a CREATED payment under reference, as the payer would.
func (s *Server) Approve(reference string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[reference]
	if !ok || p.State != Created {
		return false
	}
	s.moveTo(p, Authorized)
	return true
}

// RefundInPortal refunds minor øre of a payment's capture as the business
// portal would: no request through the API, an event in the log.
func (s *Server) RefundInPortal(reference string, minor int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[reference]
	if !ok || minor <= 0 || p.Refunded+minor > p.Captured {
		return false
	}
	p.Refunded += minor
	s.log(p, "REFUNDED", minor, "")
	return true
}

// Fail sets how op is answered until Clear (or for f.Times requests).
func (s *Server) Fail(op Op, f Failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[op] = &f
}

// Clear ends op's failure.
func (s *Server) Clear(op Op) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, op)
}

// RevokeTokens makes every token issued so far unknown, as if expired.
func (s *Server) RevokeTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = map[string]bool{}
}

// TokenFetches is how many tokens the server has issued.
func (s *Server) TokenFetches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenFetches
}

// Requests is every request received, in order, the QR image's included.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Payment is the payment under reference as the server holds it.
func (s *Server) Payment(reference string) (Payment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[reference]
	if !ok {
		return Payment{}, false
	}
	return *p, true
}

// Events is the event log of the payment under reference.
func (s *Server) Events(reference string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events[reference]...)
}

// Webhooks is every live registration.
func (s *Server) Webhooks() []Webhook {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Webhook(nil), s.webhooks...)
}

// QRTarget is the target URL the server answers for a QR payment under
// reference: what the code encodes, never fetched.
func QRTarget(reference string) string {
	return "https://qr.vippstest.example/p/" + url.PathEscape(reference)
}

// Sign is the headers Vipps sends with a webhook to registeredURL under
// secret at date (R4 §4.7): the content hash of body, and the HMAC-SHA256 of
// "POST\n<pathAndQuery>\n<date>;<host>;<hash>" under the secret's UTF-8
// bytes. It is the fake's own implementation, not the adapter's, so a test
// of one against the other is a test of the algorithm as written —
// self-generated, not Vipps' (whose published vector's signature does not
// reproduce, R4 §7 item 37).
func Sign(registeredURL, secret string, date time.Time, body []byte) (http.Header, error) {
	u, err := url.Parse(registeredURL)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	hash := base64.StdEncoding.EncodeToString(sum[:])
	when := date.UTC().Format(http.TimeFormat)
	toSign := "POST\n" + u.RequestURI() + "\n" + when + ";" + u.Host + ";" + hash
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(toSign))
	h := http.Header{}
	h.Set("x-ms-date", when)
	h.Set("x-ms-content-sha256", hash)
	h.Set("Host", u.Host)
	h.Set("Authorization", "HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature="+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	h.Set("Content-Type", "application/json")
	return h, nil
}

// SignAndPost posts body to target as Vipps would, signed under secret for
// target itself, and answers the status the receiver gave.
func (s *Server) SignAndPost(target, secret string, body []byte) (int, error) {
	s.mu.Lock()
	now := s.now()
	s.mu.Unlock()
	h, err := Sign(target, secret, now, body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	for k, v := range h {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// WebhookBody is the body Vipps posts for one event of a payment.
func WebhookBody(e Event) []byte {
	raw, _ := json.Marshal(map[string]any{
		"msn": MSN, "reference": e.Reference, "pspReference": e.PSPReference, "name": e.Name,
		"amount": e.Amount, "timestamp": e.Timestamp, "idempotencyKey": e.IdempotencyKey, "success": e.Success,
	})
	return raw
}

const paymentsPath = "/epayment/v1/payments"

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.RequestURI(), Header: r.Header.Clone(), Body: body})
	s.mu.Unlock()

	if r.Method == http.MethodPost && r.URL.Path == "/accesstoken/get" {
		if s.failed(w, OpToken) {
			return
		}
		s.token(w, r)
		return
	}
	op, reference, ok := route(r)
	if !ok {
		problem(w, http.StatusNotFound, "")
		return
	}
	if !s.authorized(r) {
		problem(w, http.StatusUnauthorized, "")
		return
	}
	if s.failed(w, op) {
		return
	}
	switch {
	case op == OpCreate:
		s.idempotent(w, r, op, "", body, s.create)
	case op == OpGet && strings.HasSuffix(r.URL.Path, "/approve"):
		if !s.Approve(reference) {
			problem(w, http.StatusBadRequest, "10050")
			return
		}
		w.WriteHeader(http.StatusOK)
	case op == OpGet:
		s.get(w, reference)
	case op == OpEvents:
		s.eventLog(w, reference)
	case op == OpCapture, op == OpCancel, op == OpRefund:
		s.idempotent(w, r, op, reference, body, func(body []byte, key string) (int, []byte) {
			return s.modify(op, reference, body, key)
		})
	case op == OpWebhooks:
		s.webhook(w, r, body)
	}
}

// route names a request's operation and reference.
func route(r *http.Request) (Op, string, bool) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/webhooks/v1/webhooks"):
		return OpWebhooks, "", true
	case strings.HasPrefix(p, "/epayment/v1/test/payments/") && strings.HasSuffix(p, "/approve") && r.Method == http.MethodPost:
		return OpGet, strings.TrimSuffix(strings.TrimPrefix(p, "/epayment/v1/test/payments/"), "/approve"), true
	case p == paymentsPath && r.Method == http.MethodPost:
		return OpCreate, "", true
	}
	rest, ok := strings.CutPrefix(p, paymentsPath+"/")
	if !ok || rest == "" {
		return "", "", false
	}
	reference, action, _ := strings.Cut(rest, "/")
	switch {
	case action == "" && r.Method == http.MethodGet:
		return OpGet, reference, true
	case action == "events" && r.Method == http.MethodGet:
		return OpEvents, reference, true
	case action == "capture" && r.Method == http.MethodPost:
		return OpCapture, reference, true
	case action == "cancel" && r.Method == http.MethodPost:
		return OpCancel, reference, true
	case action == "refund" && r.Method == http.MethodPost:
		return OpRefund, reference, true
	}
	return "", "", false
}

func (s *Server) authorized(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	return ok && s.tokens[token] &&
		r.Header.Get("Ocp-Apim-Subscription-Key") == SubscriptionKey &&
		r.Header.Get("Merchant-Serial-Number") == MSN
}

func (s *Server) failed(w http.ResponseWriter, op Op) bool {
	s.mu.Lock()
	f, ok := s.failures[op]
	if ok && f.Times > 0 {
		f.Times--
		if f.Times == 0 {
			delete(s.failures, op)
		}
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	switch {
	case f.Reset:
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return true
			}
		}
		panic(http.ErrAbortHandler)
	case f.Redirect != "":
		w.Header().Set("Location", f.Redirect)
		w.WriteHeader(http.StatusFound)
	case f.Body != nil:
		status := f.Status
		if status == 0 {
			status = http.StatusOK
		}
		writeRaw(w, status, f.Body)
	default:
		if f.RetryAfter != "" {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		problem(w, f.Status, f.Code)
	}
	return true
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("client_id") != ClientID || r.Header.Get("client_secret") != ClientSecret ||
		r.Header.Get("Ocp-Apim-Subscription-Key") != SubscriptionKey || r.Header.Get("Merchant-Serial-Number") != MSN {
		problem(w, http.StatusUnauthorized, "")
		return
	}
	s.mu.Lock()
	s.tokenFetches++
	token := fmt.Sprintf("vippstest-token-%d", s.tokenFetches)
	s.tokens[token] = true
	s.mu.Unlock()
	// Vipps answers expires_in as a string.
	writeJSON(w, http.StatusOK, map[string]any{
		"token_type": "Bearer", "expires_in": strconv.Itoa(TokenLifetime), "ext_expires_in": strconv.Itoa(TokenLifetime),
		"access_token": token,
	})
}

// idempotent answers a request under an Idempotency-Key once: the same
// operation, reference and key again answer the first answer and do nothing.
func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, op Op, reference string, body []byte, do func([]byte, string) (int, []byte)) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" && op != OpCancel {
		problem(w, http.StatusBadRequest, "4000")
		return
	}
	if key != "" {
		s.mu.Lock()
		first, seen := s.answers[string(op)+"\x00"+reference+"\x00"+key]
		s.mu.Unlock()
		if seen {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(first.status)
			_, _ = w.Write(first.body)
			return
		}
	}
	status, answered := do(body, key)
	if key != "" {
		s.mu.Lock()
		s.answers[string(op)+"\x00"+reference+"\x00"+key] = answer{status: status, body: answered}
		s.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(answered)
}

type createBody struct {
	Amount        Amount `json:"amount"`
	PaymentMethod struct {
		Type string `json:"type"`
	} `json:"paymentMethod"`
	Reference string `json:"reference"`
	UserFlow  string `json:"userFlow"`
	ReturnURL string `json:"returnUrl"`
	QRFormat  *struct {
		Format string `json:"format"`
	} `json:"qrFormat"`
}

func (s *Server) create(raw []byte, key string) (int, []byte) {
	var body createBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return problemBody(http.StatusBadRequest, "")
	}
	if body.Amount.Currency != "NOK" || body.Amount.Value < 100 || body.Amount.Value > 65_000_000 ||
		body.PaymentMethod.Type != "WALLET" || body.Reference == "" ||
		(body.UserFlow == "WEB_REDIRECT" && body.ReturnURL == "") || (body.UserFlow != "WEB_REDIRECT" && body.UserFlow != "QR") ||
		(body.UserFlow == "QR" && (body.QRFormat == nil || body.QRFormat.Format != "TEXT/TARGETURL")) {
		return problemBody(http.StatusBadRequest, "1000")
	}
	var request map[string]any
	_ = json.Unmarshal(raw, &request)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.payments[body.Reference]; exists {
		return problemBody(http.StatusConflict, CodeReferenceReused)
	}
	s.nextPSP++
	p := &Payment{
		Reference: body.Reference, PSPReference: strconv.Itoa(s.nextPSP), State: Created,
		Amount: body.Amount.Value, Request: request, script: s.scripts[body.Reference],
	}
	delete(s.scripts, body.Reference)
	s.payments[body.Reference] = p
	s.log(p, "CREATED", p.Amount, key)
	// A read answers the landing page's URL whatever the flow: Vipps does
	// not promise a QR's target URL there, so the fake does not either.
	p.RedirectURL = s.srv.URL + "/landing?token=" + url.QueryEscape(p.Reference)
	created := p.RedirectURL
	if body.UserFlow == "QR" {
		created = QRTarget(p.Reference)
	}
	raw, _ = json.Marshal(map[string]string{"reference": p.Reference, "redirectUrl": created})
	return http.StatusCreated, raw
}

func (s *Server) get(w http.ResponseWriter, reference string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[reference]
	if !ok {
		status, body := problemBody(http.StatusNotFound, "")
		writeRaw(w, status, body)
		return
	}
	if len(p.script) > 0 {
		next := p.script[0]
		if len(p.script) > 1 {
			p.script = p.script[1:]
		}
		if next != p.State {
			s.moveTo(p, next)
		}
	}
	if until, ok := s.guarantees[reference]; ok {
		p.CaptureGuaranteedUntil = &until
	}
	writeRaw(w, http.StatusOK, s.paymentBody(p, true))
}

// moveTo changes p's state, reserving its amount on AUTHORIZED, and logs it.
func (s *Server) moveTo(p *Payment, state string) {
	p.State = state
	if state == Authorized {
		p.Authorized = p.Amount
	}
	s.log(p, state, p.Amount, "")
}

func (s *Server) eventLog(w http.ResponseWriter, reference string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.payments[reference]; !ok {
		status, body := problemBody(http.StatusNotFound, "")
		writeRaw(w, status, body)
		return
	}
	events := s.events[reference]
	if events == nil {
		events = []Event{}
	}
	raw, _ := json.Marshal(events)
	writeRaw(w, http.StatusOK, raw)
}

func (s *Server) modify(op Op, reference string, raw []byte, key string) (int, []byte) {
	var body struct {
		ModificationAmount *Amount `json:"modificationAmount"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return problemBody(http.StatusBadRequest, "")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[reference]
	if !ok {
		return problemBody(http.StatusNotFound, "")
	}
	switch op {
	case OpCapture:
		if body.ModificationAmount == nil || body.ModificationAmount.Value <= 0 || p.State != Authorized {
			return problemBody(http.StatusBadRequest, "6000")
		}
		if p.Amount == InsufficientFundsAmount {
			s.logFailed(p, "CAPTURED", body.ModificationAmount.Value, key)
			return problemBody(http.StatusBadRequest, CodeInsufficientFunds)
		}
		if p.Captured+p.Cancelled+body.ModificationAmount.Value > p.Authorized {
			return problemBody(http.StatusBadRequest, CodeAmountTooHigh)
		}
		p.Captured += body.ModificationAmount.Value
		s.log(p, "CAPTURED", body.ModificationAmount.Value, key)
	case OpCancel:
		switch {
		case p.State == Terminated || p.Cancelled > 0:
			return problemBody(http.StatusBadRequest, CodeAlreadyCancelled)
		case p.State == Created:
			p.State = Terminated
			s.log(p, "TERMINATED", p.Amount, key)
		case p.State == Authorized && p.Captured >= p.Authorized:
			return problemBody(http.StatusBadRequest, CodeFullyCaptured)
		case p.State == Authorized:
			p.Cancelled = p.Authorized - p.Captured
			s.log(p, "CANCELLED", p.Cancelled, key)
		default:
			return problemBody(http.StatusBadRequest, "6000")
		}
	case OpRefund:
		if body.ModificationAmount == nil || body.ModificationAmount.Value <= 0 || p.Refunded+body.ModificationAmount.Value > p.Captured {
			return problemBody(http.StatusBadRequest, "6200")
		}
		p.Refunded += body.ModificationAmount.Value
		s.log(p, "REFUNDED", body.ModificationAmount.Value, key)
	}
	return http.StatusOK, s.paymentBody(p, false)
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request, raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := strings.CutPrefix(strings.TrimPrefix(r.URL.Path, "/webhooks/v1/webhooks"), "/")
	switch {
	case r.Method == http.MethodPost && id == "":
		var body struct {
			URL    string   `json:"url"`
			Events []string `json:"events"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.URL == "" || len(body.Events) == 0 {
			status, b := problemBody(http.StatusBadRequest, "")
			writeRaw(w, status, b)
			return
		}
		s.nextWebhookID++
		hook := Webhook{
			ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", s.nextWebhookID), URL: body.URL,
			Secret: fmt.Sprintf("vippstest-webhook-secret-%d", s.nextWebhookID), Events: body.Events,
		}
		s.webhooks = append(s.webhooks, hook)
		answered, _ := json.Marshal(map[string]string{"id": hook.ID, "secret": hook.Secret})
		writeRaw(w, http.StatusCreated, answered)
	case r.Method == http.MethodGet && id == "":
		list := make([]map[string]any, 0, len(s.webhooks))
		for _, h := range s.webhooks {
			list = append(list, map[string]any{"id": h.ID, "url": h.URL, "events": h.Events})
		}
		answered, _ := json.Marshal(map[string]any{"webhooks": list})
		writeRaw(w, http.StatusOK, answered)
	case r.Method == http.MethodDelete && id != "":
		for i, h := range s.webhooks {
			if h.ID == id {
				s.webhooks = append(s.webhooks[:i], s.webhooks[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		status, b := problemBody(http.StatusNotFound, "")
		writeRaw(w, status, b)
	default:
		status, b := problemBody(http.StatusNotFound, "")
		writeRaw(w, status, b)
	}
}

func (s *Server) log(p *Payment, name string, value int64, key string) {
	s.nextPSP++
	psp := strconv.Itoa(s.nextPSP)
	if name == "CREATED" {
		psp = p.PSPReference
	}
	s.events[p.Reference] = append(s.events[p.Reference], Event{
		Reference: p.Reference, PSPReference: psp, Name: name, Amount: Amount{Currency: "NOK", Value: value},
		Timestamp: s.now().UTC().Format(time.RFC3339Nano), IdempotencyKey: key, Success: true,
	})
}

func (s *Server) logFailed(p *Payment, name string, value int64, key string) {
	s.log(p, name, value, key)
	events := s.events[p.Reference]
	events[len(events)-1].Success = false
}

// paymentBody is GET's answer (withGet) or a modification's.
func (s *Server) paymentBody(p *Payment, withGet bool) []byte {
	nok := func(v int64) Amount { return Amount{Currency: "NOK", Value: v} }
	body := map[string]any{
		"reference": p.Reference, "pspReference": p.PSPReference, "state": p.State,
		"amount": nok(p.Amount),
		"aggregate": map[string]Amount{
			"authorizedAmount": nok(p.Authorized), "capturedAmount": nok(p.Captured),
			"cancelledAmount": nok(p.Cancelled), "refundedAmount": nok(p.Refunded),
		},
	}
	if withGet {
		body["paymentMethod"] = map[string]string{"type": "WALLET"}
		body["redirectUrl"] = p.RedirectURL
		if p.CaptureGuaranteedUntil != nil {
			body["captureGuaranteedUntil"] = p.CaptureGuaranteedUntil.UTC().Format(time.RFC3339)
		}
	}
	raw, _ := json.Marshal(body)
	return raw
}

// problemBody is an RFC 7807 problem, naming code in extraDetails when set.
func problemBody(status int, code string) (int, []byte) {
	body := map[string]any{
		"type": "https://developer.vippsmobilepay.com/docs/APIs/epayment-api/errors", "title": http.StatusText(status),
		"status": status, "detail": "vippstest", "traceId": "vippstest-trace",
	}
	if code != "" {
		body["extraDetails"] = []map[string]string{{"name": "ErrorCode", "reason": code}}
	}
	raw, _ := json.Marshal(body)
	return status, raw
}

func problem(w http.ResponseWriter, status int, code string) {
	status, body := problemBody(status, code)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	raw, _ := json.Marshal(v)
	writeRaw(w, status, raw)
}
