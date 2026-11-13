// Package foliotest is an httptest Folio for tests only: the shapes of the
// two reads the Folio adapter makes (invoices bank feed design F1, readings
// R2 and R3) — GET /accounts and GET /accounts/{accountNumber}/transactions —
// served from one TLS listener so every test package drives the real adapter
// over real HTTP without touching the network. vippstest is the same idea for
// the payments port.
//
// It is an ordinary package, not a _test file, so other packages' tests can
// import it; nothing outside a test ever should.
//
// What it does: it answers the accounts SetAccounts set, and an account's
// transactions AddTransaction added, in the order added, filtered by
// startDate and endDate (both inclusive; endDate today by the server's clock
// when absent). A request without APIKey as its bearer token is answered 401.
// Fail answers one operation — and only that one — with any status and
// Retry-After, a reset connection, a redirect, a body of the test's own or
// one a byte past the adapter's 10 MiB bound, on demand.
//
// Nothing here is recorded from Folio: the shapes are written from Folio's
// published OpenAPI document, and Folio has no test environment (R6).
//
// A client reaches it through Transport, which trusts the server's
// certificate — a harness passes it as Deps.HTTPTransport.
package foliotest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// APIKey is the one key the server accepts.
const APIKey = "foliotest-api-key"

// MaxAnswerBytes is the adapter's bound on an answer; Oversized answers one
// byte more.
const MaxAnswerBytes = 10 << 20

// Op names the operation a Fail applies to.
type Op string

const (
	OpAccounts     Op = "accounts"
	OpTransactions Op = "transactions"
)

// Failure is how an operation is answered instead of normally.
type Failure struct {
	// Status is the HTTP status, with an empty JSON object as the body.
	Status int
	// RetryAfter is the Retry-After header, when set.
	RetryAfter string
	// Reset closes the connection without an answer.
	Reset bool
	// Redirect answers 302 to this URL.
	Redirect string
	// Body, when set, is answered as it is under Status (200 when zero).
	Body []byte
	// Oversized answers 200 with a body of MaxAnswerBytes+1 bytes.
	Oversized bool
	// Times is how many requests it answers; 0 is every one until Clear.
	Times int
}

// Request is one request the server received, in order.
type Request struct {
	Method, Path string
	Header       http.Header
}

// Account is one of Folio's accounts, as GET /accounts answers it.
type Account struct {
	AccountNumber string `json:"accountNumber"`
	Name          string `json:"name"`
	// Type is Operational, Tax, Savings, Earmarks or Card.
	Type string `json:"type"`
	// Balance is "0.00" when empty.
	Balance string `json:"balance"`
}

// Party is Folio's Creditor or Debtor.
type Party struct {
	Name          string `json:"name,omitempty"`
	AccountNumber string `json:"accountNumber,omitempty"`
}

// Amount is Folio's TransactionAmount or CurrencyAmount: the amount a
// decimal string, unsigned.
type Amount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// CardAuthorization is the part of Folio's card authorization a test sets.
type CardAuthorization struct {
	CardID       string `json:"cardId"`
	MerchantID   string `json:"merchantId"`
	MerchantName string `json:"merchantName"`
	MCC          int    `json:"mcc"`
	TerminalID   string `json:"terminalId"`
	Physical     bool   `json:"physicalTerminal"`
	Amount       Amount `json:"currencyAmount"`
}

// Transaction is one of Folio's booked transactions. CurrencyAmount is the
// transaction amount when zero; Attachments is always answered as a list.
type Transaction struct {
	ID                string             `json:"id"`
	EventID           string             `json:"eventId"`
	PaymentID         string             `json:"paymentId,omitempty"`
	Description       string             `json:"description"`
	BookingDate       string             `json:"bookingDate"`
	KID               string             `json:"kid,omitempty"`
	CreditorEventID   string             `json:"creditorEventId,omitempty"`
	CardAuthorization *CardAuthorization `json:"cardAuthorization,omitempty"`
	Creditor          Party              `json:"creditor"`
	Debtor            Party              `json:"debtor"`
	TransactionAmount Amount             `json:"transactionAmount"`
	CurrencyAmount    Amount             `json:"currencyAmount"`
	Attachments       []json.RawMessage  `json:"attachments"`
	Complete          bool               `json:"complete"`
}

// Server is the fake. Its methods are safe for concurrent use.
type Server struct {
	srv *httptest.Server

	mu           sync.Mutex
	now          func() time.Time
	requests     []Request
	accounts     []Account
	transactions map[string][]Transaction
	failures     map[Op]*Failure
}

// New starts a server for t and closes it when t ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		now:          time.Now,
		transactions: map[string][]Transaction{},
		failures:     map[Op]*Failure{},
	}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the base URL to point the adapter at: https, no trailing slash.
func (s *Server) URL() string { return s.srv.URL }

// Transport is a RoundTripper that trusts the server's certificate.
func (s *Server) Transport() http.RoundTripper { return s.srv.Client().Transport }

// SetNow sets the clock "today" is read from when a read names no endDate.
func (s *Server) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetAccounts sets the accounts GET /accounts answers, in order.
func (s *Server) SetAccounts(accounts ...Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = append([]Account(nil), accounts...)
}

// AddTransaction books tx on account, after those added before.
func (s *Server) AddTransaction(account string, tx Transaction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transactions[account] = append(s.transactions[account], tx)
}

// Fail sets how op is answered until Clear (or for f.Times requests). It
// touches no other operation.
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

// Requests is every request received, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.RequestURI(), Header: r.Header.Clone()})
	s.mu.Unlock()

	op, account, ok := route(r)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+APIKey {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if s.failed(w, op) {
		return
	}
	switch op {
	case OpAccounts:
		s.serveAccounts(w)
	case OpTransactions:
		s.serveTransactions(w, r, account)
	}
}

// route names a request's operation and account.
func route(r *http.Request) (Op, string, bool) {
	if r.Method != http.MethodGet {
		return "", "", false
	}
	p := r.URL.EscapedPath()
	if p == "/accounts" {
		return OpAccounts, "", true
	}
	rest, ok := strings.CutPrefix(p, "/accounts/")
	if !ok {
		return "", "", false
	}
	escaped, ok := strings.CutSuffix(rest, "/transactions")
	if !ok || escaped == "" || strings.Contains(escaped, "/") {
		return "", "", false
	}
	account, err := url.PathUnescape(escaped)
	if err != nil {
		return "", "", false
	}
	return OpTransactions, account, true
}

func (s *Server) failed(w http.ResponseWriter, op Op) bool {
	s.mu.Lock()
	f, ok := s.failures[op]
	var copied Failure
	if ok {
		copied = *f
		if f.Times > 0 {
			f.Times--
			if f.Times == 0 {
				delete(s.failures, op)
			}
		}
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	switch {
	case copied.Reset:
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return true
			}
		}
		panic(http.ErrAbortHandler)
	case copied.Redirect != "":
		w.Header().Set("Location", copied.Redirect)
		w.WriteHeader(http.StatusFound)
	case copied.Oversized:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte(" "), MaxAnswerBytes+1))
	case copied.Body != nil:
		status := copied.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(copied.Body)
	default:
		if copied.RetryAfter != "" {
			w.Header().Set("Retry-After", copied.RetryAfter)
		}
		writeJSON(w, copied.Status, map[string]any{})
	}
	return true
}

// dataTime is every account's balanceUpdatedAt, matchingTransactionsAt and
// completeTransactionsAt.
const dataTime = "2026-01-01T00:00:00Z"

func (s *Server) serveAccounts(w http.ResponseWriter) {
	s.mu.Lock()
	accounts := make([]map[string]any, 0, len(s.accounts))
	for _, a := range s.accounts {
		balance := a.Balance
		if balance == "" {
			balance = "0.00"
		}
		accounts = append(accounts, map[string]any{
			"accountNumber": a.AccountNumber, "name": a.Name, "type": a.Type, "balance": balance,
			"balanceUpdatedAt": dataTime, "matchingTransactionsAt": dataTime, "completeTransactionsAt": dataTime,
		})
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

// serveTransactions answers account's transactions booked from startDate
// to endDate. A missing or unreadable startDate is a 400, as Folio requires
// it; an account the server holds neither as an account nor with a
// transaction is a 404.
func (s *Server) serveTransactions(w http.ResponseWriter, r *http.Request, account string) {
	q := r.URL.Query()
	start, err := time.Parse(time.DateOnly, q.Get("startDate"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "startDate is required"})
		return
	}
	s.mu.Lock()
	now := s.now
	known := false
	for _, a := range s.accounts {
		known = known || a.AccountNumber == account
	}
	booked, held := s.transactions[account]
	booked = append([]Transaction(nil), booked...)
	s.mu.Unlock()
	if !known && !held {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such account"})
		return
	}
	end := now().UTC().Truncate(24 * time.Hour)
	if v := q.Get("endDate"); v != "" {
		if end, err = time.Parse(time.DateOnly, v); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "endDate does not parse"})
			return
		}
	}
	out := make([]Transaction, 0, len(booked))
	for _, tx := range booked {
		day, err := time.Parse(time.DateOnly, tx.BookingDate)
		if err != nil || day.Before(start) || day.After(end) {
			continue
		}
		if tx.CurrencyAmount == (Amount{}) {
			tx.CurrencyAmount = tx.TransactionAmount
		}
		if tx.Attachments == nil {
			tx.Attachments = []json.RawMessage{}
		}
		out = append(out, tx)
	}
	writeJSON(w, http.StatusOK, map[string]any{"transactions": map[string]any{"booked": out}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
