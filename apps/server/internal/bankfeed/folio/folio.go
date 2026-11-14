// Package folio is Folio's adapter of the bank feed port (invoices bank feed
// design F1, readings R1–R6): an API key as a bearer token, the accounts it
// can read, and one account's booked transactions over a range of days, each
// folded onto bankfeed.Transaction with the bytes Folio sent for it.
//
// Folio answers no direction: an amount is unsigned and a transaction has no
// credit or debit marker (R4). The adapter reads it from the parties — money
// came in when the creditor's account is the account asked for and the
// debtor's is not, went out the other way — and answers DirectionUnknown when
// neither or both are, when a party's account is written in a form it cannot
// read, and when a would-be credit names a paymentId (our own outgoing
// payment), which the consumer refuses. Account numbers are compared without
// spaces and dots, a Norwegian IBAN as the eleven digits of its BBAN. This
// reading is not verified against a real Folio account.
//
// The client is the Vipps adapter's shape: the base URL is the operator's,
// so it dials through the transport the caller hands it (Deps.HTTPTransport,
// http.DefaultTransport when nil), never follows a redirect, bounds every
// call by one thirty-second deadline and every answer at 10 MiB. It never
// retries and caches nothing: a read that fails is made again by the
// consumer's next cycle.
package folio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
)

const (
	// callTimeout bounds one method call.
	callTimeout = 30 * time.Second
	// maxAnswerBytes bounds any answer read.
	maxAnswerBytes = 10 << 20
)

// amountPattern is Folio's TransactionAmount.amount: unsigned, two decimals.
var amountPattern = regexp.MustCompile(`^(\d+)\.(\d{2})$`)

// Options is one API key and how to reach Folio.
type Options struct {
	// BaseURL is https://api.folio.no/v2, the operator's to change.
	BaseURL, APIKey string
	// Transport is nil for http.DefaultTransport; Deps.HTTPTransport in the module.
	Transport http.RoundTripper
	// Now is the clock a Retry-After date is judged against; nil is the wall
	// clock.
	Now func() time.Time
}

// Client is the adapter for one API key. It is safe for concurrent use.
type Client struct {
	baseURL, apiKey string
	http            *http.Client
	now             func() time.Time
	timeout         time.Duration
}

var _ bankfeed.Feed = (*Client)(nil)

// New is the adapter for o's key.
func New(o Options) (*Client, error) {
	base, err := url.Parse(o.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, errors.New("folio: the base URL must be an https URL")
	}
	if o.APIKey == "" {
		return nil, errors.New("folio: the API key is required")
	}
	transport := o.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		baseURL: strings.TrimSuffix(o.BaseURL, "/"),
		apiKey:  o.APIKey,
		// A 3xx is answered as the non-2xx it is: following one would send
		// the key to a host no operator named.
		http:    &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now:     now,
		timeout: callTimeout,
	}, nil
}

// account is the part of Folio's Account the port reads.
type account struct {
	AccountNumber string `json:"accountNumber"`
	Name          string `json:"name"`
	Type          string `json:"type"`
}

// kinds is Folio's account types; any other is AccountOther.
var kinds = map[string]bankfeed.AccountKind{
	"Operational": bankfeed.AccountOperational,
	"Tax":         bankfeed.AccountTax,
	"Savings":     bankfeed.AccountSavings,
	"Earmarks":    bankfeed.AccountEarmarks,
	"Card":        bankfeed.AccountCard,
}

// Verify is Accounts, the list discarded: the cheapest call the key must be
// good for, and an answer that is not Folio's accounts fails it.
func (c *Client) Verify(ctx context.Context) error {
	_, err := c.Accounts(ctx)
	return err
}

// Accounts is GET /accounts. An answer without its list, or with an account
// without its number, does not decode.
func (c *Client) Accounts(ctx context.Context) ([]bankfeed.Account, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	body, err := c.get(ctx, "accounts", "/accounts")
	if err != nil {
		return nil, err
	}
	var answered struct {
		Accounts *[]account `json:"accounts"`
	}
	if err := json.Unmarshal(body, &answered); err != nil || answered.Accounts == nil {
		return nil, errors.New("folio: the accounts Folio answered do not decode")
	}
	out := make([]bankfeed.Account, 0, len(*answered.Accounts))
	for _, a := range *answered.Accounts {
		if a.AccountNumber == "" {
			return nil, errors.New("folio: Folio answered an account without its number")
		}
		kind, ok := kinds[a.Type]
		if !ok {
			kind = bankfeed.AccountOther
		}
		out = append(out, bankfeed.Account{Number: a.AccountNumber, Name: a.Name, Kind: kind})
	}
	return out, nil
}

// party is Folio's Creditor or Debtor; both fields may be absent.
type party struct {
	Name          string `json:"name"`
	AccountNumber string `json:"accountNumber"`
}

// transaction is the part of Folio's Transaction the port reads.
type transaction struct {
	ID                string          `json:"id"`
	PaymentID         string          `json:"paymentId"`
	BookingDate       string          `json:"bookingDate"`
	Description       string          `json:"description"`
	KID               string          `json:"kid"`
	CreditorEventID   string          `json:"creditorEventId"`
	CardAuthorization json.RawMessage `json:"cardAuthorization"`
	Creditor          *party          `json:"creditor"`
	Debtor            *party          `json:"debtor"`
	TransactionAmount *struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"transactionAmount"`
}

// Transactions is GET /accounts/{accountNumber}/transactions with startDate
// and endDate, both inclusive, the calendar days of from and to as they are
// written in their own location. The account is sent as it reads after
// normalising (no spaces or dots, a Norwegian IBAN as its BBAN), and must be
// digits. Every booked transaction is answered or
// none: one whose id, booking day or amount does not read fails the whole
// call, and the consumer refuses the batch. currencyAmount, the original
// currency's, is never read.
func (c *Client) Transactions(ctx context.Context, accountNumber string, from, to time.Time) ([]bankfeed.Transaction, error) {
	asked := normalise(accountNumber)
	if !digits(asked) {
		return nil, errors.New("folio: an account number of digits is required")
	}
	// The days compared are the days sent, never the instants.
	startDate, endDate := from.Format(time.DateOnly), to.Format(time.DateOnly)
	if endDate < startDate {
		return nil, errors.New("folio: the range ends before it starts")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	query := url.Values{"startDate": {startDate}, "endDate": {endDate}}
	body, err := c.get(ctx, "transactions", "/accounts/"+url.PathEscape(asked)+"/transactions?"+query.Encode())
	if err != nil {
		return nil, err
	}
	// The list is read as raw elements first, so each transaction's bytes
	// are kept exactly as Folio sent them.
	var answered struct {
		Transactions *struct {
			Booked *[]json.RawMessage `json:"booked"`
		} `json:"transactions"`
	}
	if err := json.Unmarshal(body, &answered); err != nil || answered.Transactions == nil || answered.Transactions.Booked == nil {
		return nil, errors.New("folio: the transactions Folio answered do not decode")
	}
	booked := *answered.Transactions.Booked
	out := make([]bankfeed.Transaction, 0, len(booked))
	for i, raw := range booked {
		t, err := transactionOf(asked, raw)
		if err != nil {
			return nil, fmt.Errorf("folio: booked transaction %d: %w", i, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// transactionOf folds one of Folio's transactions onto the port's, read
// against the normalised account asked for.
func transactionOf(asked string, raw json.RawMessage) (bankfeed.Transaction, error) {
	var t transaction
	if err := json.Unmarshal(raw, &t); err != nil {
		return bankfeed.Transaction{}, errors.New("it does not decode")
	}
	if t.ID == "" {
		return bankfeed.Transaction{}, errors.New("it has no id")
	}
	booked, err := time.Parse(time.DateOnly, t.BookingDate)
	if err != nil {
		return bankfeed.Transaction{}, errors.New("its booking date does not parse")
	}
	if t.TransactionAmount == nil || t.TransactionAmount.Currency == "" {
		return bankfeed.Transaction{}, errors.New("it has no amount")
	}
	minor, err := minorOf(t.TransactionAmount.Amount)
	if err != nil {
		return bankfeed.Transaction{}, err
	}
	out := bankfeed.Transaction{
		ID: t.ID, BookedOn: booked, AmountMinor: minor, Currency: t.TransactionAmount.Currency,
		Direction: directionOf(asked, t), Kind: kindOf(t),
		KID: strings.TrimSpace(t.KID), Text: t.Description,
		Raw: raw,
	}
	if t.Debtor != nil {
		out.DebtorName, out.DebtorAccount = t.Debtor.Name, t.Debtor.AccountNumber
	}
	return out, nil
}

// minorOf reads Folio's amount, digits, a point and two decimals, as minor
// units without a float. Anything else — a sign, three decimals, none, an
// amount past int64 — does not read. An error quotes at most the first 32
// bytes of what Folio sent.
func minorOf(amount string) (int64, error) {
	m := amountPattern.FindStringSubmatch(amount)
	if m == nil {
		return 0, fmt.Errorf("its amount %q is not digits with two decimals", cut(amount))
	}
	whole, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || whole > (1<<63-1-99)/100 {
		return 0, fmt.Errorf("its amount %q is out of range", cut(amount))
	}
	cents, _ := strconv.ParseInt(m[2], 10, 64)
	return whole*100 + cents, nil
}

// maxQuoted is the most of Folio's text an error quotes.
const maxQuoted = 32

// cut is s at most maxQuoted bytes long, on a rune boundary.
func cut(s string) string {
	if len(s) <= maxQuoted {
		return s
	}
	n := maxQuoted
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// directionOf is credit when the creditor's account is the one asked for
// and the debtor's is not, debit the other way, and unknown when neither or
// both are. It is unknown too when either party's account is present but
// reads, after normalising, as neither digits nor an IBAN — it cannot be
// told from the account asked for — and when a would-be credit names a
// paymentId: a transaction our own payment made is never money in.
func directionOf(asked string, t transaction) bankfeed.Direction {
	creditor, debtor := partyAccount(t.Creditor), partyAccount(t.Debtor)
	if !readable(creditor) || !readable(debtor) {
		return bankfeed.DirectionUnknown
	}
	in, out := creditor == asked, debtor == asked
	switch {
	case in && !out:
		if t.PaymentID != "" {
			return bankfeed.DirectionUnknown
		}
		return bankfeed.DirectionCredit
	case out && !in:
		return bankfeed.DirectionDebit
	default:
		return bankfeed.DirectionUnknown
	}
}

// kindOf is internal when the transaction names a creditor event (both
// sides of a transfer between the customer's own accounts do), else card
// when it carries a card authorization, else transfer.
func kindOf(t transaction) bankfeed.Kind {
	switch {
	case t.CreditorEventID != "":
		return bankfeed.KindInternal
	case len(t.CardAuthorization) > 0 && string(t.CardAuthorization) != "null":
		return bankfeed.KindCard
	default:
		return bankfeed.KindTransfer
	}
}

// partyAccount is a party's account normalised, empty when the party or
// its account is absent.
func partyAccount(p *party) string {
	if p == nil {
		return ""
	}
	return normalise(p.AccountNumber)
}

// ibanPattern is an IBAN's form: a country, two check digits, letters and
// digits.
var ibanPattern = regexp.MustCompile(`^[A-Z]{2}\d{2}[A-Z0-9]{11,30}$`)

// readable reports whether a normalised account is absent, digits or an
// IBAN: a form the comparison with the account asked for can trust.
func readable(n string) bool {
	return n == "" || digits(n) || ibanPattern.MatchString(n)
}

// normalise is an account number without its spaces and dots, a Norwegian
// IBAN (NO, two check digits, eleven digits) as its eleven-digit BBAN.
func normalise(number string) string {
	n := strings.ToUpper(strings.NewReplacer(" ", "", ".", "").Replace(number))
	if len(n) == 15 && n[:2] == "NO" && digits(n[2:]) {
		return n[4:]
	}
	return n
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// get makes one authenticated GET and answers its body when Folio answered
// a 2xx. net/http may replay a GET whose reused connection broke before any
// answer; that is the transport's, a read repeated, and the adapter itself
// makes every call once.
func (c *Client) get(ctx context.Context, op, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("folio: build the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vantigo/"+buildinfo.Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("folio: %s: %w", op, withoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readLimited(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("folio: read Folio's answer to %s: %w", op, err)
	}
	if err := c.classify(op, resp.StatusCode, resp.Header.Get("Retry-After")); err != nil {
		return nil, err
	}
	return body, nil
}

// classify is a status as the port's errors: nil for a 2xx, the typed
// errors for 401, 403 and 429, and for anything else — a 3xx, a 5xx, any
// other status — a plain error naming the status. Folio's body never goes
// into it.
func (c *Client) classify(op string, status int, retry string) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return bankfeed.ErrUnauthorized
	case status == http.StatusTooManyRequests:
		return &bankfeed.ErrRateLimited{RetryAfter: retryAfter(retry, c.now())}
	default:
		return fmt.Errorf("folio: %s answered %d", op, status)
	}
}

// maxRetryAfter is the longest Retry-After the adapter answers: a header
// asking for longer — or for more seconds than a duration holds — is read as
// a day, never as an overflow or a wait the caller would take for parking.
const maxRetryAfter = 24 * time.Hour

// retryAfter reads a Retry-After header, seconds or an HTTP date judged
// against now, at most maxRetryAfter; zero when it is absent, unreadable or
// already past.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(v, 10, 64); err == nil && seconds > 0 {
		if seconds >= int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if at.After(now.Add(maxRetryAfter)) {
			return maxRetryAfter
		}
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
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
