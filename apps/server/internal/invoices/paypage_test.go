package invoices_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/web"
)

const (
	payReadPath     = "/api/v1/invoices/pay/read"
	payAttemptsPath = "/api/v1/invoices/pay/attempts"
)

func payStatusPath(reference, nonce string) string {
	return payAttemptsPath + "/" + reference + "?nonce=" + url.QueryEscape(nonce)
}

// payPageJSON is the pay page's read as a client reads it.
type payPageJSON struct {
	Seller struct {
		Name               string `json:"name"`
		OrganisationNumber string `json:"organisationNumber"`
	} `json:"seller"`
	InvoiceNumber int64   `json:"invoiceNumber"`
	IssueDate     string  `json:"issueDate"`
	DueDate       *string `json:"dueDate"`
	OpenAmount    float64 `json:"openAmount"`
	Currency      string  `json:"currency"`
	Kid           *string `json:"kid"`
	TermsURL      *string `json:"termsUrl"`
	PrivacyURL    *string `json:"privacyUrl"`
	Language      string  `json:"language"`
	Payable       bool    `json:"payable"`
	Reason        *string `json:"reason"`
}

// payProblemJSON is a pay page's 409: the code and the reason beside it.
type payProblemJSON struct {
	Code   string  `json:"code"`
	Reason *string `json:"reason"`
	Status int     `json:"status"`
}

// payToken is the token of an invoice's live link, from its sealed URL.
func payToken(t *testing.T, h *harness, invoiceID int64) string {
	t.Helper()
	raw := liveLinkURL(t, h, invoiceID)
	if raw == "" {
		t.Fatalf("invoice %d has no live pay link", invoiceID)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("token")
}

// readPay reads the pay page anonymously, failing unless 200.
func readPay(t *testing.T, c *modtest.Client, token string) (payPageJSON, string) {
	t.Helper()
	res := c.Do(http.MethodPost, payReadPath, map[string]any{"token": token})
	if res.Status != http.StatusOK {
		t.Fatalf("POST %s = %d %s, want 200", payReadPath, res.Status, res.Body)
	}
	var page payPageJSON
	res.JSON(&page)
	return page, string(res.Body)
}

// startPay asks for a payment anonymously.
func startPay(c *modtest.Client, token string) *modtest.Response {
	return c.Do(http.MethodPost, payAttemptsPath, map[string]any{"token": token, "acceptTerms": true})
}

// payCreated is a 201's body.
type payCreated struct {
	Reference   string `json:"reference"`
	RedirectURL string `json:"redirectUrl"`
}

// startedPay asks for a payment, failing unless 201.
func startedPay(t *testing.T, c *modtest.Client, token string) payCreated {
	t.Helper()
	res := startPay(c, token)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST %s = %d %s, want 201", payAttemptsPath, res.Status, res.Body)
	}
	var out payCreated
	res.JSON(&out)
	return out
}

// plantLiveAttempt writes a pay-link attempt of invoiceID — under its live
// link — in state; reserved "" leaves no reservation.
func plantLiveAttempt(t *testing.T, h *harness, invoiceID int64, state, reserved string) {
	t.Helper()
	columns, values := "", ""
	if reserved != "" {
		columns, values = ", authorized_amount, reserved_amount", ", "+reserved+", "+reserved
	}
	h.Exec(t, `INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, pay_link_id, return_nonce_sha256,
		amount, cancel_key, state, created_at, expires_at`+columns+`)
		SELECT $1, 'vipps', '123456', 'pay_link', id, repeat('0', 64), 100, gen_random_uuid(), $2, now(), now() + interval '10 minutes'`+values+`
		FROM invoices.pay_links WHERE invoice_id = $1 AND revoked_at IS NULL`,
		invoiceID, state)
}

// vippsCreates is every create request vippstest received, decoded.
func vippsCreates(t *testing.T, h *harness) []vippstestCreate {
	t.Helper()
	var out []vippstestCreate
	for _, r := range h.vipps.Requests() {
		if r.Method != http.MethodPost || r.Path != "/epayment/v1/payments" {
			continue
		}
		var c vippstestCreate
		if err := json.Unmarshal(r.Body, &c); err != nil {
			t.Fatal(err)
		}
		c.key, c.msn = r.Header.Get("Idempotency-Key"), r.Header.Get("Merchant-Serial-Number")
		out = append(out, c)
	}
	return out
}

type vippstestCreate struct {
	Reference string `json:"reference"`
	UserFlow  string `json:"userFlow"`
	ReturnURL string `json:"returnUrl"`
	Amount    struct {
		Value int64 `json:"value"`
	} `json:"amount"`
	MerchantLegalLinks struct {
		Terms   string `json:"termsOfSaleUrl"`
		Privacy string `json:"privacyPolicyUrl"`
	} `json:"merchantLegalLinks"`
	key, msn string
}

// TestPayPage_ReadAnswersAndNoBuyerData: the read answers what the page
// shows — the seller, the number, the dates, the open amount, the KID, the
// legal links, the language — and never the buyer's name or address; each
// reason in its turn; payment_in_progress while a reservation leaves less
// than 1.00 open, amount_out_of_range when no reservation does; a GET on
// the read's path and an unknown token are 404s.
func TestPayPage_ReadAnswersAndNoBuyerData(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	anon := h.Client(t)
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)

	page, raw := readPay(t, anon, token)
	if !page.Payable || page.Reason != nil || page.OpenAmount != 2500 || page.Currency != "NOK" || page.Language != "nb" {
		t.Errorf("the page = %+v, want payable, 2500 NOK, nb", page)
	}
	if page.Seller.Name != "Kraft-Verket AS" || page.Seller.OrganisationNumber == "" || page.InvoiceNumber != *inv.Number ||
		page.IssueDate != *inv.IssueDate || page.DueDate == nil || *page.DueDate != *inv.DueDate {
		t.Errorf("the page = %+v, want the seller and the invoice's number and dates", page)
	}
	if page.TermsURL == nil || *page.TermsURL != termsURL || page.PrivacyURL == nil || *page.PrivacyURL != privacyURL {
		t.Errorf("the legal links = %v %v", page.TermsURL, page.PrivacyURL)
	}
	for _, buyer := range []string{"Acme", "Kundeveien", "0150", "923609016", "PO-77"} {
		if strings.Contains(raw, buyer) {
			t.Errorf("the pay page answers %q of the buyer: %s", buyer, raw)
		}
	}

	reason := func(token, want string) {
		t.Helper()
		page, _ := readPay(t, anon, token)
		if page.Payable || page.Reason == nil || *page.Reason != want {
			t.Errorf("the page's reason = %v (payable %v), want %s", deref(page.Reason), page.Payable, want)
		}
	}
	// A reservation leaving 0.50 open: a payment in progress.
	plantLiveAttempt(t, h, inv.ID, "capturing", "2499.50")
	reason(token, "payment_in_progress")

	// 0.50 open with no reservation: below what Vipps takes.
	small := issuedAcme(t, h)
	registered(t, h, small.ID, pay(2499.50, h.Now().Format(time.DateOnly)))
	reason(payToken(t, h, small.ID), "amount_out_of_range")

	paid := issuedAcme(t, h)
	paidToken := payToken(t, h, paid.ID)
	registered(t, h, paid.ID, pay(2500, h.Now().Format(time.DateOnly)))
	reason(paidToken, "settled")

	credited := issuedAcme(t, h)
	creditedToken := payToken(t, h, credited.ID)
	issued(t, h, creditDraft(t, h, credited.ID).ID)
	reason(creditedToken, "credited")

	handed := issuedAcme(t, h)
	h.Exec(t, `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, agency_reference, note, created_at, created_by_user_id)
		VALUES ($1, $2, 'Inkasso AS', '', '', now(), gen_random_uuid())`, handed.ID, h.Now().Format(time.DateOnly))
	reason(payToken(t, h, handed.ID), "handed_off")

	revoked := issuedAcme(t, h)
	revokedToken := payToken(t, h, revoked.ID)
	if res := issuer(t, h).Do(http.MethodPost, payLinkRevokePath(revoked.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", res.Status, res.Body)
	}
	reason(revokedToken, "revoked")

	open := issuedAcme(t, h)
	openToken := payToken(t, h, open.ID)
	payLinksOff(t, h)
	reason(openToken, "unavailable")

	if res := anon.Do(http.MethodPost, payReadPath, map[string]any{"token": "no-such-token"}); res.Status != http.StatusNotFound {
		t.Errorf("an unknown token = %d, want 404", res.Status)
	}
	if res := anon.Do(http.MethodGet, payReadPath+"?token="+openToken, nil, modtest.SkipContract("the read is a POST")); res.Status != http.StatusNotFound {
		t.Errorf("GET on the read's path = %d, want 404", res.Status)
	}
}

// TestPayPage_Attempt: the acceptance is required; an unknown token is a
// 404; each reason a 409 with its code and the reason beside it; the cap of
// five live attempts. A 201 sends Vipps a WEB_REDIRECT payment under the
// stored sales unit, its reference the installation's prefix and the
// invoice's number, the legal links, and a return address to the pay page
// with the attempt's reference and a nonce — never the token — and the
// idempotency key Vipps saw is the one stored on the attempt.
func TestPayPage_Attempt(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	anon := h.Client(t)
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)

	if res := anon.Do(http.MethodPost, payAttemptsPath, map[string]any{"token": token, "acceptTerms": false}); res.Status != http.StatusBadRequest ||
		len(problemOf(t, res).Errors["acceptTerms"]) == 0 {
		t.Errorf("without the acceptance = %d %s, want 400 on acceptTerms", res.Status, res.Body)
	}
	if res := startPay(anon, "no-such-token"); res.Status != http.StatusNotFound {
		t.Errorf("an unknown token = %d, want 404", res.Status)
	}

	created := startedPay(t, anon, token)
	prefix := getPaymentSettings(t, h).ReferencePrefix
	if want := fmt.Sprintf("%s-%d-", prefix, *inv.Number); !strings.HasPrefix(created.Reference, want) {
		t.Errorf("reference %q, want it to start %q", created.Reference, want)
	}
	if !strings.HasPrefix(created.RedirectURL, h.vipps.URL()) {
		t.Errorf("redirectUrl %q, want Vipps' landing page", created.RedirectURL)
	}
	creates := vippsCreates(t, h)
	if len(creates) != 1 {
		t.Fatalf("%d creates sent to Vipps, want 1", len(creates))
	}
	c := creates[0]
	if c.Reference != created.Reference || c.UserFlow != "WEB_REDIRECT" || c.Amount.Value != 250000 || c.msn != vippstest.MSN {
		t.Errorf("the create = %+v, want WEB_REDIRECT of 250000 øre under %s", c, vippstest.MSN)
	}
	if c.MerchantLegalLinks.Terms != termsURL || c.MerchantLegalLinks.Privacy != privacyURL {
		t.Errorf("the legal links sent = %+v", c.MerchantLegalLinks)
	}
	ret, err := url.Parse(c.ReturnURL)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.ReturnURL, token) || ret.Query().Has("token") ||
		!strings.HasPrefix(c.ReturnURL, publicBaseURL+"/pay?") || ret.Query().Get("attempt") != created.Reference || ret.Query().Get("nonce") == "" {
		t.Errorf("returnUrl %q, want %s/pay with the attempt and a nonce, never the token", c.ReturnURL, publicBaseURL)
	}
	stored := modtest.One[string](t, h.Harness, `SELECT create_key::text FROM invoices.payment_attempts WHERE reference = $1`, created.Reference)
	if c.key != stored {
		t.Errorf("Vipps saw the key %q, the attempt stores %q", c.key, stored)
	}

	refused := func(token, code, reason string) {
		t.Helper()
		res := startPay(anon, token)
		var p payProblemJSON
		res.JSON(&p)
		if res.Status != http.StatusConflict || p.Code != code || (reason != "" && (p.Reason == nil || *p.Reason != reason)) {
			t.Errorf("the create = %d %s, want 409 %s (%s)", res.Status, res.Body, code, reason)
		}
	}
	paid := issuedAcme(t, h)
	paidToken := payToken(t, h, paid.ID)
	registered(t, h, paid.ID, pay(2500, h.Now().Format(time.DateOnly)))
	refused(paidToken, "invoice_settled", "settled")

	credited := issuedAcme(t, h)
	creditedToken := payToken(t, h, credited.ID)
	issued(t, h, creditDraft(t, h, credited.ID).ID)
	refused(creditedToken, "invoice_fully_credited", "credited")

	reserved := issuedAcme(t, h)
	plantLiveAttempt(t, h, reserved.ID, "capturing", "2500")
	refused(payToken(t, h, reserved.ID), "payment_in_progress", "payment_in_progress")

	small := issuedAcme(t, h)
	registered(t, h, small.ID, pay(2499.50, h.Now().Format(time.DateOnly)))
	refused(payToken(t, h, small.ID), "amount_out_of_range", "amount_out_of_range")

	revoked := issuedAcme(t, h)
	revokedToken := payToken(t, h, revoked.ID)
	if res := issuer(t, h).Do(http.MethodPost, payLinkRevokePath(revoked.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d", res.Status)
	}
	refused(revokedToken, "pay_links_unavailable", "revoked")

	// The cap: five live attempts, the first ours.
	for range 4 {
		plantLiveAttempt(t, h, inv.ID, "created", "")
	}
	refused(token, "too_many_attempts", "")
	if n := len(vippsCreates(t, h)); n != 1 {
		t.Errorf("%d creates sent to Vipps after the refusals, want still 1", n)
	}
}

// TestPayPage_AttemptUnknownOutcomeStaysCreating: Vipps failing a create
// with a 5xx leaves the outcome unknown — 502 provider_failed, the attempt
// kept creating for the payments worker, its key stored before the call
// (the one Vipps saw, nothing written after it). A definitive refusal (a
// 400 naming a code) fails the attempt.
func TestPayPage_AttemptUnknownOutcomeStaysCreating(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	anon := h.Client(t)
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)

	h.vipps.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusInternalServerError, Times: 1})
	res := startPay(anon, token)
	if res.Status != http.StatusBadGateway || problemOf(t, res).Code != "provider_failed" {
		t.Fatalf("the create = %d %s, want 502 provider_failed", res.Status, res.Body)
	}
	state := modtest.One[string](t, h.Harness, `SELECT state FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID)
	key := modtest.One[string](t, h.Harness, `SELECT create_key::text FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID)
	if state != "creating" {
		t.Errorf("the attempt is %s, want creating", state)
	}
	if creates := vippsCreates(t, h); len(creates) != 1 || creates[0].key != key {
		t.Errorf("Vipps saw %+v, want one create under the stored key %s", creates, key)
	}

	h.vipps.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusBadRequest, Code: "1000", Times: 1})
	if res := startPay(anon, token); res.Status != http.StatusBadGateway || problemOf(t, res).Code != "provider_failed" {
		t.Fatalf("the refused create = %d %s, want 502 provider_failed", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1 AND state = 'failed' AND finished_at IS NOT NULL`, inv.ID); n != 1 {
		t.Errorf("%d failed attempts, want 1", n)
	}
}

// TestPayPage_Status: the return page reads the attempt's state with its
// nonce only — another attempt's nonce, a wrong one or an unknown reference
// are 404s — mapped to pending, paid, failed or cancelled.
func TestPayPage_Status(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	anon := h.Client(t)
	first := issuedAcme(t, h)
	second := issuedAcme(t, h)
	a := startedPay(t, anon, payToken(t, h, first.ID))
	b := startedPay(t, anon, payToken(t, h, second.ID))
	nonces := map[string]string{}
	for _, c := range vippsCreates(t, h) {
		u, _ := url.Parse(c.ReturnURL)
		nonces[c.Reference] = u.Query().Get("nonce")
	}
	state := func(reference, nonce string) (int, string) {
		res := anon.Do(http.MethodGet, payStatusPath(reference, nonce), nil)
		var out struct {
			State string `json:"state"`
		}
		if res.Status == http.StatusOK {
			res.JSON(&out)
		}
		return res.Status, out.State
	}
	if code, s := state(a.Reference, nonces[a.Reference]); code != http.StatusOK || s != "pending" {
		t.Errorf("a's state = %d %s, want 200 pending", code, s)
	}
	for _, c := range []struct{ ref, nonce string }{
		{a.Reference, nonces[b.Reference]}, {a.Reference, "wrong"}, {"nosuch-1-1", nonces[a.Reference]},
	} {
		if code, _ := state(c.ref, c.nonce); code != http.StatusNotFound {
			t.Errorf("state of %s with nonce %q = %d, want 404", c.ref, c.nonce, code)
		}
	}
	for db, want := range map[string]string{"captured": "paid", "failed": "failed", "aborted": "cancelled", "expired": "cancelled"} {
		other := issuedAcme(t, h)
		created := startedPay(t, anon, payToken(t, h, other.ID))
		var nonce string
		for _, c := range vippsCreates(t, h) {
			if c.Reference == created.Reference {
				u, _ := url.Parse(c.ReturnURL)
				nonce = u.Query().Get("nonce")
			}
		}
		h.Exec(t, `UPDATE invoices.payment_attempts SET state = $1, finished_at = now() WHERE reference = $2`, db, created.Reference)
		if code, s := state(created.Reference, nonce); code != http.StatusOK || s != want {
			t.Errorf("%s's state = %d %s, want %s", db, code, s, want)
		}
	}
}

// TestPayPage_RateLimits: each operation has its own bucket per client
// address — 60 reads, 10 creates, 400 status reads in ten minutes — the next
// a 429 with a Retry-After, before the token is looked up; another bucket and
// another client are untouched.
func TestPayPage_RateLimits(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	anon := h.Client(t)
	ops := []struct {
		name  string
		limit int
		do    func(c *modtest.Client) *modtest.Response
	}{
		{"read", 60, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodPost, payReadPath, map[string]any{"token": "x"})
		}},
		{"create", 10, func(c *modtest.Client) *modtest.Response { return startPay(c, "x") }},
		{"status", 400, func(c *modtest.Client) *modtest.Response {
			return c.Do(http.MethodGet, payStatusPath("x-1-1", "n"), nil)
		}},
	}
	for i, op := range ops {
		for n := range op.limit {
			if res := op.do(anon); res.Status != http.StatusNotFound {
				t.Fatalf("%s %d = %d %s, want 404", op.name, n+1, res.Status, res.Body)
			}
		}
		res := op.do(anon)
		if res.Status != http.StatusTooManyRequests || res.Header("Retry-After") == "" || res.Code() != "rate_limited" {
			t.Errorf("%s past its limit = %d (Retry-After %q) %s, want 429 rate_limited", op.name, res.Status, res.Header("Retry-After"), res.Body)
		}
		if i+1 < len(ops) {
			if res := ops[i+1].do(anon); res.Status != http.StatusNotFound {
				t.Errorf("%s after %s's bucket ran out = %d, want 404", ops[i+1].name, op.name, res.Status)
			}
			// That one hit counts against the next bucket: the loop allows for it.
			ops[i+1].limit--
		}
		if res := op.do(h.Client(t)); res.Status != http.StatusNotFound {
			t.Errorf("another client's %s = %d, want 404", op.name, res.Status)
		}
	}
}

// TestPayPage_CSRF: the pay page's POSTs pass the platform's cross-origin
// protection when same-origin and are refused 403 cross-site — on the
// installation's host through the harness, and on the public host through
// server.New, where the gate admits the path and the proxy keeps Host, so
// the page's own POST is same-origin without the public origin trusted.
func TestPayPage_CSRF(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	anon := h.Client(t)
	token := payToken(t, h, issuedAcme(t, h).ID)
	if res := anon.Do(http.MethodPost, payReadPath, map[string]any{"token": token}, modtest.Header("Sec-Fetch-Site", "same-origin")); res.Status != http.StatusOK {
		t.Errorf("a same-origin read = %d %s, want 200", res.Status, res.Body)
	}
	if res := anon.Do(http.MethodPost, payReadPath, map[string]any{"token": token}, modtest.Header("Sec-Fetch-Site", "cross-site"),
		modtest.SkipContract("the platform's cross-origin refusal")); res.Status != http.StatusForbidden {
		t.Errorf("a cross-site read = %d %s, want 403", res.Status, res.Body)
	}

	cfg := &config.Config{Env: config.Production, AppOrigin: "https://vantigo.example.com", AppHostname: "vantigo.example.com",
		PublicBaseURL: "https://pay.example.com", PublicHostname: "pay.example.com",
		Port: 8080, ShutdownTimeout: time.Second, LogLevel: slog.LevelInfo}
	assets := fstest.MapFS{"index.html": {Data: []byte(`<!doctype html><html><head><title>Vantigo</title></head><body><div id="root"></div></body></html>`)}}
	index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding, cfg.Modules)
	if err != nil {
		t.Fatal(err)
	}
	reached := 0
	handler := server.New(server.Options{Config: cfg, Logger: slog.New(slog.DiscardHandler), Index: index, Assets: assets,
		Health: health.Handler(slog.New(slog.DiscardHandler), "test"),
		API:    http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached++; w.WriteHeader(http.StatusOK) })})
	for _, path := range []string{payReadPath, payAttemptsPath} {
		for site, want := range map[string]int{"same-origin": http.StatusOK, "cross-site": http.StatusForbidden} {
			req := httptest.NewRequest(http.MethodPost, "https://pay.example.com"+path, strings.NewReader(`{}`))
			req.Header.Set("Sec-Fetch-Site", site)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != want {
				t.Errorf("a %s POST %s on the public host = %d, want %d", site, path, rec.Code, want)
			}
		}
	}
	if reached != 2 {
		t.Errorf("the API was reached %d times, want the 2 same-origin POSTs", reached)
	}
}

// TestPayPage_Anonymous: no session is needed, and a signed-in caller is
// answered alike, byte for byte.
func TestPayPage_Anonymous(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	token := payToken(t, h, issuedAcme(t, h).ID)
	_, anon := readPay(t, h.Client(t), token)
	_, signed := readPay(t, h.SignIn(t, "invoices:access", "invoices:manage"), token)
	if anon != signed {
		t.Errorf("signed in, the read = %s; anonymous %s", signed, anon)
	}
	created := startedPay(t, h.SignIn(t, "invoices:access"), token)
	if by := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE reference = $1 AND created_by_user_id IS NULL`, created.Reference); by != 1 {
		t.Error("a signed-in caller's attempt names a user; the pay page's never does")
	}
}

// plantAttemptExpiring writes a pay-link attempt of invoiceID under its live
// link in state, expiring at expires; capturing and capture_unknown reserve
// 1.00.
func plantAttemptExpiring(t *testing.T, h *harness, invoiceID int64, state string, expires time.Time) {
	t.Helper()
	columns, values := "", ""
	if state == "capturing" || state == "capture_unknown" {
		columns, values = ", authorized_amount, reserved_amount", ", 1, 1"
	}
	h.Exec(t, `INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, pay_link_id, return_nonce_sha256,
		amount, cancel_key, state, created_at, expires_at`+columns+`)
		SELECT $1, 'vipps', '123456', 'pay_link', id, repeat('0', 64), 100, gen_random_uuid(), $2, $3::timestamptz - interval '10 minutes', $3`+values+`
		FROM invoices.pay_links WHERE invoice_id = $1 AND revoked_at IS NULL`,
		invoiceID, state, expires)
}

// TestPayPage_ReservationLowersTheAmount: a 2500.00 invoice of which a
// capture in progress reserves 1000.00 shows 1500.00 open, and a payment
// started on it asks Vipps for 150000 øre and stores 1500 as its amount.
func TestPayPage_ReservationLowersTheAmount(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)
	plantLiveAttempt(t, h, inv.ID, "capturing", "1000")

	if page, _ := readPay(t, h.Client(t), token); page.OpenAmount != 1500 || !page.Payable {
		t.Errorf("the read's openAmount = %v (payable %v), want 1500", page.OpenAmount, page.Payable)
	}
	created := startedPay(t, h.Client(t), token)
	for _, c := range vippsCreates(t, h) {
		if c.Reference == created.Reference && c.Amount.Value != 150000 {
			t.Errorf("Vipps was asked for %d øre, want 150000", c.Amount.Value)
		}
	}
	if amount := modtest.One[string](t, h.Harness, `SELECT amount::text FROM invoices.payment_attempts WHERE reference = $1`, created.Reference); amount != "1500.00" {
		t.Errorf("the attempt's amount = %s, want 1500.00", amount)
	}
}

// TestPayPage_TheCap: five live attempts of one pay link refuse a sixth —
// each of creating, created, authorized, capturing and capture_unknown
// counting while live — but a creating or created one past its expiry no
// longer counts, and a revoked link's attempts do not hold back the new
// link's.
func TestPayPage_TheCap(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	anon := h.Client(t)
	live := h.Now().Add(10 * time.Minute)
	for _, state := range []string{"creating", "created", "authorized", "capturing", "capture_unknown"} {
		inv := issuedAcme(t, h)
		for range 5 {
			plantAttemptExpiring(t, h, inv.ID, state, live)
		}
		res := startPay(anon, payToken(t, h, inv.ID))
		if res.Status != http.StatusConflict || problemOf(t, res).Code != "too_many_attempts" {
			t.Errorf("five %s attempts: the create = %d %s, want 409 too_many_attempts", state, res.Status, res.Body)
		}
	}

	expired := issuedAcme(t, h)
	for range 4 {
		plantAttemptExpiring(t, h, expired.ID, "created", live)
	}
	plantAttemptExpiring(t, h, expired.ID, "created", h.Now().Add(-time.Minute))
	plantAttemptExpiring(t, h, expired.ID, "creating", h.Now().Add(-time.Minute))
	startedPay(t, anon, payToken(t, h, expired.ID))

	relinked := issuedAcme(t, h)
	for range 5 {
		plantAttemptExpiring(t, h, relinked.ID, "created", live)
	}
	c := issuer(t, h)
	if res := c.Do(http.MethodPost, payLinkRevokePath(relinked.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", res.Status, res.Body)
	}
	if res := c.Do(http.MethodPost, payLinkPath(relinked.ID), nil); res.Status != http.StatusCreated {
		t.Fatalf("a new link = %d %s", res.Status, res.Body)
	}
	startedPay(t, anon, payToken(t, h, relinked.ID))
}

// TestPayPage_RefusalsSpeakToThePayer: what an anonymous payer is answered
// never names the operator's switch or the keys — stored keys that cannot be
// opened are a 503 payments_unavailable in a payer's words.
func TestPayPage_RefusalsSpeakToThePayer(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	token := payToken(t, h, issuedAcme(t, h).ID)
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET secret_ciphertext = 'not-sealed'`)
	res := startPay(h.Client(t), token)
	if res.Status != http.StatusServiceUnavailable || problemOf(t, res).Code != "payments_unavailable" {
		t.Fatalf("the create = %d %s, want 503 payments_unavailable", res.Status, res.Body)
	}
	for _, word := range []string{"INVOICES_", "keys", "Keys", "credentials"} {
		if strings.Contains(string(res.Body), word) {
			t.Errorf("the payer is answered %q: %s", word, res.Body)
		}
	}
}

// TestPayPage_MoreReasons: an anonymised customer's link reads revoked;
// keys Vipps refused read unavailable.
func TestPayPage_MoreReasons(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	anon := h.Client(t)
	token := payToken(t, h, issuedAcme(t, h).ID)
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerAcme)
	if page, _ := readPay(t, anon, token); page.Reason == nil || *page.Reason != "revoked" {
		t.Errorf("an anonymised customer's link reads %v, want revoked", deref(page.Reason))
	}

	h2, _ := payReady(t)
	token2 := payToken(t, h2, issuedAcme(t, h2).ID)
	h2.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = now()`)
	if page, _ := readPay(t, h2.Client(t), token2); page.Reason == nil || *page.Reason != "unavailable" {
		t.Errorf("refused keys read %v, want unavailable", deref(page.Reason))
	}
}

// TestPayPage_UnavailableWithoutPublicURL: without PUBLIC_BASE_URL a link
// still on record reads unavailable, whatever the settings say.
func TestPayPage_UnavailableWithoutPublicURL(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	putVipps(t, h, vippstestKeys())
	h.Exec(t, `UPDATE invoices.payment_settings SET pay_links_enabled = true, terms_url = $1, privacy_url = $2`, termsURL, privacyURL)
	inv := issuedAcme(t, h)
	const token = "test-token-without-public-url_AAAAAAAAAAAAA"
	h.Exec(t, `INSERT INTO invoices.pay_links (invoice_id, token_sha256, url_ciphertext, created_at)
		VALUES ($1, encode(sha256($2::bytea), 'hex'), 'sealed', now())`, inv.ID, token)
	if page, _ := readPay(t, h.Client(t), token); page.Reason == nil || *page.Reason != "unavailable" {
		t.Errorf("without PUBLIC_BASE_URL the link reads %v, want unavailable", deref(page.Reason))
	}
}

// TestPayPage_PayerCannotChooseTheAmount: an amount in the create's body is
// not the payer's to set: Vipps is asked for the open amount.
func TestPayPage_PayerCannotChooseTheAmount(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	token := payToken(t, h, issuedAcme(t, h).ID)
	res := h.Client(t).Do(http.MethodPost, payAttemptsPath, map[string]any{"token": token, "acceptTerms": true, "amount": 1},
		modtest.SkipContract("a field the contract does not name"))
	if res.Status != http.StatusCreated {
		t.Fatalf("the create = %d %s, want 201", res.Status, res.Body)
	}
	if creates := vippsCreates(t, h); len(creates) != 1 || creates[0].Amount.Value != 250000 {
		t.Errorf("Vipps was asked for %+v, want 250000 øre", creates)
	}
}

// TestPayPage_StateOfEveryAttemptState pins how the return page reads each
// of an attempt's states.
func TestPayPage_StateOfEveryAttemptState(t *testing.T) {
	t.Parallel()
	for state, want := range map[string]string{
		"creating": "pending", "created": "pending", "authorized": "pending", "capturing": "pending",
		"capture_unknown": "pending", "captured": "paid", "cancelling": "cancelled", "cancelled": "cancelled",
		"aborted": "cancelled", "expired": "cancelled", "terminated": "cancelled", "capture_failed": "failed",
		"failed": "failed", "abandoned": "failed",
	} {
		if got := invoices.PayStateOf(state); got != want {
			t.Errorf("payStateOf(%s) = %s, want %s", state, got, want)
		}
	}
}
