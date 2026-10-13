package invoices_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/boombuler/barcode/qr"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

func onSitePath(id int64) string { return fmt.Sprintf("%s/%d/payment-requests", invoicesPath, id) }
func onSiteStatusPath(id, attempt int64) string {
	return fmt.Sprintf("%s/%d", onSitePath(id), attempt)
}
func onSiteCancelPath(id, attempt int64) string { return onSiteStatusPath(id, attempt) + "/cancel" }

// onSiteBody is an on-site payment request's body.
var onSiteBody = map[string]any{"flow": "on_site"}

// onSiteJSON is an on-site payment request's 201 (and the quick invoice's
// paymentRequest) as a client reads it.
type onSiteJSON struct {
	AttemptID int64     `json:"attemptId"`
	Reference string    `json:"reference"`
	QrSvg     *string   `json:"qrSvg"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// onSiteStatusJSON is a request's status as the tablet polls it.
type onSiteStatusJSON struct {
	State      string     `json:"state"`
	CapturedAt *time.Time `json:"capturedAt"`
}

// requestOnSite asks for an on-site payment of invoice id as an issuer.
func requestOnSite(t *testing.T, h *harness, id int64) *modtest.Response {
	t.Helper()
	return issuer(t, h).Do(http.MethodPost, onSitePath(id), onSiteBody)
}

// requestedOnSite asks for an on-site payment, failing unless 201.
func requestedOnSite(t *testing.T, h *harness, id int64) onSiteJSON {
	t.Helper()
	res := requestOnSite(t, h, id)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST %s = %d %s, want 201", onSitePath(id), res.Status, res.Body)
	}
	var out onSiteJSON
	res.JSON(&out)
	return out
}

// onSiteStatus reads a request's status as an issuer, failing unless 200.
func onSiteStatus(t *testing.T, h *harness, id, attempt int64) onSiteStatusJSON {
	t.Helper()
	res := issuer(t, h).Do(http.MethodGet, onSiteStatusPath(id, attempt), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", onSiteStatusPath(id, attempt), res.Status, res.Body)
	}
	var out onSiteStatusJSON
	res.JSON(&out)
	return out
}

// setSnapshot changes issued document id's buyer snapshot past its trigger,
// as a fixture: set is the SET list.
func setSnapshot(t *testing.T, h *harness, id int64, set string) {
	t.Helper()
	h.Exec(t, `ALTER TABLE invoices.invoices DISABLE TRIGGER tr_invoices_immutable`)
	h.Exec(t, `UPDATE invoices.invoices SET `+set+` WHERE id = $1`, id)
	h.Exec(t, `ALTER TABLE invoices.invoices ENABLE TRIGGER tr_invoices_immutable`)
}

// onSiteRefused asserts a 409 with code and answers its detail.
func onSiteRefused(t *testing.T, what string, res *modtest.Response, code string) string {
	t.Helper()
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
		return ""
	}
	p := problemOf(t, res)
	if p.Code != code {
		t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
	return p.Detail
}

// svgReadsAs fails unless svg is text's QR code exactly: one square per
// dark module of the code text encodes to.
func svgReadsAs(t *testing.T, svg, text string) {
	t.Helper()
	code, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	b := code.Bounds()
	want := map[string]bool{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if r, g, bl, _ := code.At(x, y).RGBA(); r+g+bl < 3*0x8000 {
				want[fmt.Sprintf("%d %d", x-b.Min.X+4, y-b.Min.Y+4)] = true
			}
		}
	}
	got := map[string]bool{}
	for _, m := range regexp.MustCompile(`M(\d+) (\d+)h1v1h-1z`).FindAllStringSubmatch(svg, -1) {
		got[m[1]+" "+m[2]] = true
	}
	if len(want) == 0 || len(got) != len(want) {
		t.Fatalf("%d dark squares, want %q's %d dark modules", len(got), text, len(want))
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("module %s is missing: the QR does not read as %q", k, text)
		}
	}
}

// TestOnSite_HardRules (D17's hard rules, none relaxable): an on-site
// request is refused without on-site payment available — the setting off,
// which can only be turned on with the kontantsalg acknowledgement, the
// keys refused; on a draft, a credit note, a settled or handed-off
// invoice; buyer_identity_missing for a snapshot that names no address or
// organisation number, an empty buyer name or an erased customer;
// payment_request_active while one live on-site attempt exists — the
// partial unique index refusing a second as well — and while a parked
// create holds the invoice, saying it must be abandoned; and an attempt
// always covers the whole open amount. It needs invoices:issue.
func TestOnSite_HardRules(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)

	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, onSitePath(issuedAcme(t, h).ID), onSiteBody); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:issue = %d %s, want 403", res.Status, res.Body)
	}
	if res := requestOnSite(t, h, 999999); res.Status != http.StatusNotFound {
		t.Errorf("no document = %d %s, want 404", res.Status, res.Body)
	}

	// The setting off; turned on again only with the acknowledgement.
	off := issuedAcme(t, h)
	onSiteOff(t, h)
	onSiteRefused(t, "on-site payment off", requestOnSite(t, h, off.ID), "on_site_payments_unavailable")
	res := h.SignIn(t, "invoices:access", "invoices:manage").Do(http.MethodPut, paymentSettingsPath,
		paymentSettingsBody(getPaymentSettings(t, h).Revision, map[string]any{"onSitePaymentsEnabled": true}))
	if res.Status != http.StatusBadRequest {
		t.Errorf("on-site turned on without the acknowledgement = %d %s, want 400", res.Status, res.Body)
	}
	onSiteRefused(t, "on-site payment still off", requestOnSite(t, h, off.ID), "on_site_payments_unavailable")
	enablePayments(t, h)
	// The keys refused: on-site payment is not available, whatever the setting.
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = $1`, h.Now())
	onSiteRefused(t, "the keys refused", requestOnSite(t, h, off.ID), "on_site_payments_unavailable")
	h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = NULL`)

	// What the invoice is.
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	onSiteRefused(t, "a draft", requestOnSite(t, h, draft.ID), "invoice_draft")
	creditNote := issued(t, h, creditDraft(t, h, issuedAcme(t, h).ID).ID)
	onSiteRefused(t, "a credit note", requestOnSite(t, h, creditNote.ID), "credit_note_no_payments")
	settled := issuedAcme(t, h)
	registered(t, h, settled.ID, pay(2500, h.Now().Format(time.DateOnly)))
	onSiteRefused(t, "a settled invoice", requestOnSite(t, h, settled.ID), "invoice_settled")
	handed := issuedAcme(t, h)
	if res := payer(t, h).Do(http.MethodPost, handoffPath(handed.ID),
		withField(handoffBody(h.Now().Format(time.DateOnly), "Kredinor AS"), "acknowledgeNotDelivered", true)); res.Status != http.StatusOK {
		t.Fatalf("the hand-off = %d %s", res.Status, res.Body)
	}
	onSiteRefused(t, "a handed-off invoice", requestOnSite(t, h, handed.ID), "invoice_handed_off")

	// The buyer identified, re-judged on the stored snapshot (m9).
	noAddress := issuedAcme(t, h)
	setSnapshot(t, h, noAddress.ID, `buyer_organisation_number = NULL, buyer_address_line1 = NULL`)
	onSiteRefused(t, "a snapshot with no address and no organisation number", requestOnSite(t, h, noAddress.ID), "buyer_identity_missing")
	noPostalCode := issuedAcme(t, h)
	setSnapshot(t, h, noPostalCode.ID, `buyer_organisation_number = NULL, buyer_postal_code = NULL`)
	onSiteRefused(t, "a Norwegian address with no postal code", requestOnSite(t, h, noPostalCode.ID), "buyer_identity_missing")
	noName := issuedAcme(t, h)
	setSnapshot(t, h, noName.ID, `buyer_name = ''`)
	onSiteRefused(t, "an empty buyer name", requestOnSite(t, h, noName.ID), "buyer_identity_missing")
	erased := issuedAcme(t, h)
	h.Exec(t, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerAcme)
	onSiteRefused(t, "an erased customer", requestOnSite(t, h, erased.ID), "buyer_identity_missing")
	h.Exec(t, `DELETE FROM invoices.erased_customers WHERE customer_id = $1`, customerAcme)
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts`); n != 0 {
		t.Fatalf("%d attempts after the refusals, want none", n)
	}
	if creates := vippsCreates(t, h); len(creates) != 0 {
		t.Fatalf("Vipps saw %d creates after the refusals, want none", len(creates))
	}

	// One payment, one invoice: the whole open amount, one live request.
	part := issuedAcme(t, h)
	registered(t, h, part.ID, pay(1000, h.Now().Format(time.DateOnly)))
	first := requestedOnSite(t, h, part.ID)
	if amount := modtest.One[string](t, h.Harness, `SELECT amount::text FROM invoices.payment_attempts WHERE id = $1`, first.AttemptID); amount != "1500.00" {
		t.Errorf("the attempt's amount = %s, want the whole open 1500.00", amount)
	}
	if creates := vippsCreates(t, h); len(creates) != 1 || creates[0].Amount.Value != 150000 {
		t.Errorf("Vipps saw %+v, want one create of 150000 øre", creates)
	}
	onSiteRefused(t, "a second request", requestOnSite(t, h, part.ID), "payment_request_active")
	_, err := h.Pool().Exec(t.Context(), `INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount, cancel_key, state, created_at, expires_at)
		VALUES ($1, 'vipps', '123456', 'on_site', 1500, gen_random_uuid(), 'created', now(), now() + interval '10 minutes')`, part.ID)
	if err == nil || !strings.Contains(err.Error(), "ux_payment_attempts_on_site_live") {
		t.Errorf("a second live on-site attempt inserted directly = %v, want ux_payment_attempts_on_site_live", err)
	}

	// A parked create holds the invoice until a manager abandons it.
	parked := issuedAcme(t, h)
	plantParkedCreate(t, h, parked.ID)
	if detail := onSiteRefused(t, "a parked create", requestOnSite(t, h, parked.ID), "payment_request_active"); !strings.Contains(detail, "abandon") {
		t.Errorf("the parked create's refusal = %q, want it to say the request must be abandoned", detail)
	}
}

// TestOnSite_SwitchOff: with INVOICES_VIPPS_ENABLED=0 on-site payment is
// not available, whatever the settings say.
func TestOnSite_SwitchOff(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t, modtest.WithEnv("INVOICES_VIPPS_ENABLED", "0"))
	onSiteRefused(t, "Vipps switched off", requestOnSite(t, h, issuedAcme(t, h).ID), "on_site_payments_unavailable")
	if creates := vippsCreates(t, h); len(creates) != 0 {
		t.Errorf("Vipps saw %d creates, want none", len(creates))
	}
}

// TestOnSite_QRAndCustomerPresent: the request Vipps sees is a QR asked
// for as its target URL with the customer present, of the whole open
// amount; the 201's qrSvg is that target URL's code, rendered here; the
// attempt is on_site, made by the caller, created and due to be polled.
func TestOnSite_QRAndCustomerPresent(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	inv := issuedAcme(t, h)
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")
	res := c.Do(http.MethodPost, onSitePath(inv.ID), onSiteBody)
	if res.Status != http.StatusCreated {
		t.Fatalf("the request = %d %s, want 201", res.Status, res.Body)
	}
	var got onSiteJSON
	res.JSON(&got)
	if got.QrSvg == nil || !strings.HasPrefix(*got.QrSvg, "<svg") || !got.ExpiresAt.Equal(h.Now().Add(10*time.Minute)) {
		t.Fatalf("the answer = %+v, want a QR and expiry ten minutes on", got)
	}
	svgReadsAs(t, *got.QrSvg, vippstest.QRTarget(got.Reference))

	var create struct {
		UserFlow            string `json:"userFlow"`
		CustomerInteraction string `json:"customerInteraction"`
		QRFormat            *struct {
			Format string `json:"format"`
		} `json:"qrFormat"`
		ReturnURL string `json:"returnUrl"`
		Reference string `json:"reference"`
		Amount    struct {
			Value int64 `json:"value"`
		} `json:"amount"`
	}
	n := 0
	for _, r := range h.vipps.Requests() {
		if r.Method == http.MethodPost && r.Path == "/epayment/v1/payments" {
			n++
			if err := json.Unmarshal(r.Body, &create); err != nil {
				t.Fatal(err)
			}
		}
		if r.Method == http.MethodGet && strings.Contains(r.Path, got.Reference) {
			t.Errorf("Vipps was read back (%s); the QR is rendered from the create's own answer", r.Path)
		}
	}
	if n != 1 || create.UserFlow != "QR" || create.CustomerInteraction != "CUSTOMER_PRESENT" || create.QRFormat == nil ||
		create.QRFormat.Format != "TEXT/TARGETURL" || create.ReturnURL != "" || create.Reference != got.Reference || create.Amount.Value != 250000 {
		t.Errorf("Vipps saw %d creates, the last %+v; want one QR TEXT/TARGETURL, CUSTOMER_PRESENT, 250000 øre", n, create)
	}

	var flow, state string
	var by *string
	if err := h.Pool().QueryRow(t.Context(), `SELECT flow, state, created_by_user_id::text FROM invoices.payment_attempts WHERE id = $1`,
		got.AttemptID).Scan(&flow, &state, &by); err != nil {
		t.Fatal(err)
	}
	if flow != "on_site" || state != "created" || by == nil || *by != userID.String() {
		t.Errorf("the attempt = %s %s by %v, want on_site created by %s", flow, state, by, userID)
	}
	if got.Reference != modtest.One[string](t, h.Harness, `SELECT reference FROM invoices.payment_attempts WHERE id = $1`, got.AttemptID) {
		t.Errorf("the reference answered is not the attempt's")
	}
}

// TestOnSite_ProviderFailed: a create Vipps refuses for good is a 502 and
// a failed attempt, after which a new request may be made; one whose
// outcome is unknown — a QR's reference already in use, which is never
// read back — is a 502 that leaves the attempt creating for the payments
// worker, holding the invoice's one live request.
func TestOnSite_ProviderFailed(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	inv := issuedAcme(t, h)

	h.vipps.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusBadRequest, Code: "1000", Times: 1})
	if res := requestOnSite(t, h, inv.ID); res.Status != http.StatusBadGateway || problemOf(t, res).Code != "provider_failed" {
		t.Fatalf("the refused create = %d %s, want 502 provider_failed", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1 AND state = 'failed'`, inv.ID); n != 1 {
		t.Errorf("%d failed attempts, want 1", n)
	}

	h.vipps.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusConflict, Code: vippstest.CodeReferenceReused, Times: 1})
	if res := requestOnSite(t, h, inv.ID); res.Status != http.StatusBadGateway || problemOf(t, res).Code != "provider_failed" {
		t.Fatalf("a QR whose reference is in use = %d %s, want 502 provider_failed", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1 AND state = 'creating'`, inv.ID); n != 1 {
		t.Errorf("%d creating attempts, want the unknown one left for the worker", n)
	}
	onSiteRefused(t, "a request while the unknown one lives", requestOnSite(t, h, inv.ID), "payment_request_active")
}

// TestOnSite_StatusAndCancel: the tablet reads the request's state; a
// request not yet approved is cancelled — cancelling, released by the
// payments worker, cancelled — and one approved is captured, reported with
// capturedAt and never cancelled (payment_request_active). A cancel of a
// request already over changes nothing. Another invoice's attempt and a
// pay-link attempt are 404s.
func TestOnSite_StatusAndCancel(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv := issuedAcme(t, h)
	req := requestedOnSite(t, h, inv.ID)
	if got := onSiteStatus(t, h, inv.ID, req.AttemptID); got.State != "created" || got.CapturedAt != nil {
		t.Errorf("the status = %+v, want created", got)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, onSiteStatusPath(inv.ID, req.AttemptID), nil); res.Status != http.StatusForbidden {
		t.Errorf("the status without invoices:issue = %d, want 403", res.Status)
	}
	other := issuedAcme(t, h)
	if res := issuer(t, h).Do(http.MethodGet, onSiteStatusPath(other.ID, req.AttemptID), nil); res.Status != http.StatusNotFound {
		t.Errorf("another invoice's attempt = %d, want 404", res.Status)
	}
	if res := issuer(t, h).Do(http.MethodPost, onSiteCancelPath(other.ID, req.AttemptID), nil); res.Status != http.StatusNotFound {
		t.Errorf("a cancel of another invoice's attempt = %d, want 404", res.Status)
	}
	payLinked := startedPay(t, h.Client(t), payToken(t, h, other.ID))
	linkAttempt := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.payment_attempts WHERE reference = $1`, payLinked.Reference)
	if res := issuer(t, h).Do(http.MethodGet, onSiteStatusPath(other.ID, linkAttempt), nil); res.Status != http.StatusNotFound {
		t.Errorf("a pay-link attempt = %d, want 404", res.Status)
	}

	res := issuer(t, h).Do(http.MethodPost, onSiteCancelPath(inv.ID, req.AttemptID), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("the cancel = %d %s, want 200", res.Status, res.Body)
	}
	var cancelled onSiteStatusJSON
	res.JSON(&cancelled)
	if cancelled.State != "cancelling" {
		t.Errorf("the cancel = %+v, want cancelling", cancelled)
	}
	cycle(t, h)
	if got := onSiteStatus(t, h, inv.ID, req.AttemptID); got.State != "cancelled" {
		t.Errorf("after the worker the status = %+v, want cancelled", got)
	}
	if p, _ := h.vipps.Payment(req.Reference); p.State != vippstest.Terminated {
		t.Errorf("Vipps holds the payment %s, want it terminated", p.State)
	}
	again := issuer(t, h).Do(http.MethodPost, onSiteCancelPath(inv.ID, req.AttemptID), nil)
	if again.Status != http.StatusOK {
		t.Errorf("a cancel of a cancelled request = %d %s, want 200", again.Status, again.Body)
	}

	// A new request, approved and captured: never cancelled.
	paid := requestedOnSite(t, h, inv.ID)
	if !h.vipps.Approve(paid.Reference) {
		t.Fatal("vippstest did not approve the payment")
	}
	for range 4 {
		h.Advance(10 * time.Second)
		cycle(t, h)
	}
	got := onSiteStatus(t, h, inv.ID, paid.AttemptID)
	if got.State != "captured" || got.CapturedAt == nil {
		t.Fatalf("the approved request = %+v, want captured with capturedAt", got)
	}
	onSiteRefused(t, "a cancel of a captured request", issuer(t, h).Do(http.MethodPost, onSiteCancelPath(inv.ID, paid.AttemptID), nil), "payment_request_active")
	if s := modtest.One[string](t, h.Harness, `SELECT state FROM invoices.payment_attempts WHERE id = $1`, paid.AttemptID); s != "captured" {
		t.Errorf("after the refused cancel the attempt is %s, want captured", s)
	}
}

// TestOnSite_NoAnonymousPath (D17 rule 1, R4 §9.5 requirement 3): the
// contract has no anonymous, "kontantkunde" or receipt-only way to take a
// payment. Every operation that makes a payment attempt names an issued
// invoice — the pay page by a pay link's token, the on-site request by the
// invoice's id — and the quick invoice requires customerId; the code makes
// attempts in exactly those two places.
func TestOnSite_NoAnonymousPath(t *testing.T) {
	t.Parallel()
	contract, err := os.ReadFile("../../../../openapi/invoices.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(contract)
	section := func(start string) string {
		t.Helper()
		i := strings.Index(spec, start)
		if i < 0 {
			t.Fatalf("the contract has no %q", start)
		}
		rest := spec[i+len(start):]
		if j := regexp.MustCompile(`\n    /api|\n        [A-Z][A-Za-z]+:\n`).FindStringIndex(rest); j != nil {
			rest = rest[:j[0]]
		}
		return rest
	}
	onSite := section("\n    /api/v1/invoices/{id}/payment-requests:\n")
	if !strings.Contains(onSite, "name: id") || !strings.Contains(onSite, "operationId: postInvoicesByIdPaymentRequests") ||
		!strings.Contains(onSite, "x-vantigo-access: permission:invoices:access+invoices:issue") {
		t.Errorf("the on-site request does not name an invoice by id behind invoices:issue:\n%s", onSite)
	}
	quick := section("\n        InvoicesQuickInvoiceRequest:\n")
	if !regexp.MustCompile(`required:\n\s+- customerId\n`).MatchString(quick) {
		t.Errorf("the quick invoice does not require customerId:\n%s", quick)
	}
	body := strings.ToLower(section("\n        InvoicesOnSitePaymentRequestBody:\n"))
	for _, field := range []string{"customer", "buyer", "name", "address", "amount"} {
		if regexp.MustCompile(`\n\s+` + field + `[a-z]*:\n`).MatchString(body) {
			t.Errorf("the on-site request's body takes %s:\n%s", field, body)
		}
	}
	// Every operation whose path makes a Vipps payment.
	makers := regexp.MustCompile(`(?m)^    (/api/v1/invoices/[^:]*(pay/attempts|payment-requests)):\n        post:`).FindAllStringSubmatch(spec, -1)
	var paths []string
	for _, m := range makers {
		paths = append(paths, m[1])
	}
	if strings.Join(paths, " ") != "/api/v1/invoices/pay/attempts /api/v1/invoices/{id}/payment-requests" {
		t.Errorf("the operations making a payment = %v, want the pay page's and the on-site request's alone", paths)
	}

	// The code makes an attempt in exactly two places.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	callers := map[string]int{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(src), "s.createAttempt("); n > 0 {
			callers[e.Name()] = n
		}
	}
	if len(callers) != 2 || callers["paypage.go"] != 1 || callers["onsite.go"] != 1 {
		t.Errorf("createAttempt is called from %v, want once by the pay page and once by the on-site request", callers)
	}
	h := newHarness(t)
	if res := quicker(t, h).Do(http.MethodPost, quickPath, map[string]any{
		"lines": []map[string]any{line("A", 1, 100, vat25)}, "payOnSite": true,
	}); res.Status != http.StatusBadRequest {
		t.Errorf("a quick invoice without customerId = %d %s, want 400", res.Status, res.Body)
	}
}

// quickOnSiteJSON is the quick invoice's 201 with its payment request.
type quickOnSiteJSON struct {
	Invoice             invoiceJSON  `json:"invoice"`
	PaymentRequest      *onSiteJSON  `json:"paymentRequest"`
	PaymentRequestError *problemJSON `json:"paymentRequestError"`
}

// TestQuickInvoice_PayOnSite (D17): payOnSite on a quick invoice makes the
// on-site request after the issue — 201 with the invoice and
// paymentRequest — or, when the request cannot be made, 201 with the
// invoice issued all the same and paymentRequestError. A replay of the
// same requestId reports the live request, read only, without a QR, and
// Vipps is never asked again.
func TestQuickInvoice_PayOnSite(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	body := quickBody(customerAcme, line("Utrykning", 1, 1000, vat25))
	body["payOnSite"], body["requestId"] = true, "0b9e1c55-7b21-4c43-9a0e-5f1d2c3b4a59"

	res := quicker(t, h).Do(http.MethodPost, quickPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("the quick invoice = %d %s, want 201", res.Status, res.Body)
	}
	var got quickOnSiteJSON
	res.JSON(&got)
	if got.Invoice.Status != "issued" || got.PaymentRequestError != nil || got.PaymentRequest == nil || got.PaymentRequest.QrSvg == nil {
		t.Fatalf("the quick invoice = %+v, want it issued with a payment request and its QR", got)
	}
	svgReadsAs(t, *got.PaymentRequest.QrSvg, vippstest.QRTarget(got.PaymentRequest.Reference))
	if amount := modtest.One[string](t, h.Harness, `SELECT amount::text FROM invoices.payment_attempts WHERE id = $1 AND invoice_id = $2 AND flow = 'on_site'`,
		got.PaymentRequest.AttemptID, got.Invoice.ID); amount != "1250.00" {
		t.Errorf("the request's amount = %s, want the invoice's 1250.00", amount)
	}

	res = quicker(t, h).Do(http.MethodPost, quickPath, body)
	if res.Status != http.StatusOK {
		t.Fatalf("the replay = %d %s, want 200", res.Status, res.Body)
	}
	var replay quickOnSiteJSON
	res.JSON(&replay)
	if replay.Invoice.ID != got.Invoice.ID || replay.PaymentRequest == nil || replay.PaymentRequest.AttemptID != got.PaymentRequest.AttemptID ||
		replay.PaymentRequest.Reference != got.PaymentRequest.Reference || replay.PaymentRequest.QrSvg != nil || replay.PaymentRequestError != nil {
		t.Errorf("the replay = %+v, want the first's invoice and its live request, without a QR", replay)
	}
	if creates := vippsCreates(t, h); len(creates) != 1 {
		t.Errorf("Vipps saw %d creates, want one: a replay never asks again", len(creates))
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1`, got.Invoice.ID); n != 1 {
		t.Errorf("%d attempts after the replay, want 1", n)
	}

	// Vipps refuses the create: the invoice is issued, the error beside it.
	h.vipps.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusBadRequest, Code: "1000", Times: 1})
	failed := quickBody(customerAcme, line("Utrykning", 1, 400, vat25))
	failed["payOnSite"] = true
	res = quicker(t, h).Do(http.MethodPost, quickPath, failed)
	if res.Status != http.StatusCreated {
		t.Fatalf("the quick invoice Vipps refused = %d %s, want 201", res.Status, res.Body)
	}
	var refused quickOnSiteJSON
	res.JSON(&refused)
	if refused.Invoice.Status != "issued" || refused.PaymentRequest != nil || refused.PaymentRequestError == nil ||
		refused.PaymentRequestError.Code != "provider_failed" {
		t.Errorf("the quick invoice Vipps refused = %+v, want it issued with paymentRequestError provider_failed", refused)
	}

	// On-site payment off: issued, on_site_payments_unavailable.
	onSiteOff(t, h)
	res = quicker(t, h).Do(http.MethodPost, quickPath, failed)
	var off quickOnSiteJSON
	res.JSON(&off)
	if res.Status != http.StatusCreated || off.Invoice.Status != "issued" || off.PaymentRequest != nil ||
		off.PaymentRequestError == nil || off.PaymentRequestError.Code != "on_site_payments_unavailable" {
		t.Errorf("the quick invoice with on-site off = %d %+v, want it issued with on_site_payments_unavailable", res.Status, off)
	}
}
