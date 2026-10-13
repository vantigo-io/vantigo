package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

// The payments port end to end (invoices payments and reminders design
// D13–D19, the payments port plan's Task 12): the real customers and
// invoices modules, Vipps played by vippstest through Deps.HTTPTransport —
// the invoices module's own adapter, built from the credentials an
// administrator stores, reaches it and never Vipps — the payments worker as
// worker mode builds it, the public host set by PUBLIC_BASE_URL and the
// customers module's own export and anonymisation. Nothing of the invoices
// package's own test hooks is reachable from here: what the story sees is
// what an operator, a payer and the customers module see.

// publicHost is PUBLIC_BASE_URL's host: the pay page's, on a host of its
// own beside APP_URL's, under example.com as APP_URL's is.
const publicHost = "pay.example.com"

// onPublicHost is path as a request to the public host: modtest's client
// sends every request to APP_URL's origin, and modtest is not this story's
// to change, so the authority is written "modules.example.com@<public
// host>" — the URL's host is then the public host, which is what the gate
// reads, while httptest's client dials the harness's listener for
// example.com and every host under it. The user part becomes a Basic Authorization header, which nothing
// in Vantigo reads.
func onPublicHost(path string) string { return "@" + publicHost + path }

// portInvoice is an issued invoice as the story reads it.
type portInvoice struct {
	ID         int64    `json:"id"`
	State      string   `json:"state"`
	GrossTotal float64  `json:"grossTotal"`
	OpenAmount *float64 `json:"openAmount"`
	PayLink    *struct {
		ID  int64   `json:"id"`
		URL *string `json:"url"`
	} `json:"payLink"`
	Payments []struct {
		Amount           float64 `json:"amount"`
		Source           string  `json:"source"`
		PaymentAttemptID *int64  `json:"paymentAttemptId"`
	} `json:"payments"`
}

// readPortInvoice reads invoice id.
func readPortInvoice(t *testing.T, c *modtest.Client, id int64) portInvoice {
	t.Helper()
	var doc portInvoice
	okJSON(t, c, http.MethodGet, invoiceAt(id), nil, &doc)
	return doc
}

// osloDay is at's day in Oslo, the module's business day.
func osloDay(t *testing.T, at time.Time) string {
	t.Helper()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	return at.In(oslo).Format(time.DateOnly)
}

// paymentsCycle runs one cycle of the invoices-payments worker: every due
// attempt handled.
func paymentsCycle(t *testing.T, h *modtest.Harness) {
	t.Helper()
	if err := invoices.NewPaymentWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("a payments worker cycle: %v", err)
	}
}

// vippsCaptures is how many captures of reference Vipps was asked for.
func vippsCaptures(vipps *vippstest.Server, reference string) int {
	n := 0
	for _, r := range vipps.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/payments/"+reference+"/capture") {
			n++
		}
	}
	return n
}

// liveLinks is how many live pay links customer's documents have.
func liveLinks(t *testing.T, h *modtest.Harness, customer int32) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM invoices.pay_links l JOIN invoices.invoices i ON i.id = l.invoice_id
		WHERE i.customer_id = $1 AND l.revoked_at IS NULL`, customer)
}

// TestPaymentsPort_FromOnSiteAndPayLinkToTheErase: an administrator stores
// the Vipps keys and turns pay links and payment on site on, acknowledging
// kontantsalg. A person made through the customers API, with an address, is
// handed a quick invoice paid on site: the QR request is CUSTOMER_PRESENT,
// the payer approves, and the worker reserves, captures once and registers
// it — the invoice paid, on the day's list, the day reconciled once it is
// over. An ordinary invoice issued to them carries a pay link, printed on its
// PDF; past its due date it is overdue and on the dashboard. The payer opens
// the pay page on the public host — where sign-in is a 404 — and pays; the
// worker captures and registers it, and the invoice leaves the overdue
// items. A refund made in the Vipps portal is seen by the refund watch. The
// person's export carries both attempts and each payment's attempt; the
// anonymisation revokes every live pay link of theirs and reports it, and
// the pay page then answers the link revoked.
func TestPaymentsPort_FromOnSiteAndPayLinkToTheErase(t *testing.T) {
	t.Parallel()
	vipps := vippstest.New(t)
	h, admin, _ := invoicesInstallation(t,
		modtest.WithEnv("PUBLIC_BASE_URL", "https://"+publicHost),
		modtest.WithEnv("INVOICES_VIPPS_BASE_URL", vipps.URL()),
		modtest.WithTransport(vipps.Transport()))
	vipps.SetNow(h.Now)
	signIn := func() *modtest.Client {
		c, _ := h.SignInUser(t, "customers:view", "customers:create", "customers:update", "customers:delete",
			"customers:merge", "customers:billing-manage", "customers:legal-identity-view", "customers:legal-identity-manage",
			"customers:personal-data", "customers:timeline-view", "invoices:access", "invoices:create", "invoices:issue", "invoices:manage", "invoices:payments")
		return c
	}

	// Vipps configured, both features on, kontantsalg acknowledged.
	okJSON(t, admin, http.MethodPut, invoicesBase+"/settings/vipps", map[string]any{
		"msn": vippstest.MSN, "clientId": vippstest.ClientID, "clientSecret": vippstest.ClientSecret,
		"subscriptionKey": vippstest.SubscriptionKey,
	}, nil)
	var settings struct {
		Revision int32 `json:"revision"`
	}
	okJSON(t, admin, http.MethodGet, invoicesBase+"/settings/payments", nil, &settings)
	okJSON(t, admin, http.MethodPut, invoicesBase+"/settings/payments", map[string]any{
		"payLinksEnabled": true, "termsUrl": "https://kraft-verket.example/vilkar", "privacyUrl": "https://kraft-verket.example/personvern",
		"onSitePaymentsEnabled": true, "acknowledgeKontantsalg": true, "revision": settings.Revision,
	}, nil)
	var meta struct {
		PayLinksAvailable       bool `json:"payLinksAvailable"`
		OnSitePaymentsAvailable bool `json:"onSitePaymentsAvailable"`
	}
	okJSON(t, admin, http.MethodGet, invoicesBase+"/meta", nil, &meta)
	if !meta.PayLinksAvailable || !meta.OnSitePaymentsAvailable {
		t.Fatalf("meta = %+v, want pay links and payment on site available", meta)
	}

	var person invoiceCustomer
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Kari Nordmann", "type": "person"}, &person)
	invoiceAddress(t, admin, person.Id)
	invoiceBillingProfile(t, admin, person.Id, map[string]any{"invoiceEmail": "kari@example.org"})

	// A quick invoice paid on site.
	var quick struct {
		Invoice        portInvoice `json:"invoice"`
		PaymentRequest *struct {
			AttemptID int64   `json:"attemptId"`
			Reference string  `json:"reference"`
			QrSvg     *string `json:"qrSvg"`
		} `json:"paymentRequest"`
		PaymentRequestError json.RawMessage `json:"paymentRequestError"`
	}
	okJSON(t, admin, http.MethodPost, invoicesBase+"/quick", map[string]any{
		"customerId": person.Id, "payOnSite": true, "requestId": "6f1d8c2e-5b3a-4e7f-9a10-2c4b6d8e0f12",
		"lines": []map[string]any{{"description": "Utrykning", "quantity": 1, "unit": "stk", "unitPrice": 1000, "vatCodeId": 1}},
	}, &quick)
	if quick.PaymentRequest == nil || quick.PaymentRequest.QrSvg == nil || len(quick.PaymentRequestError) > 0 {
		t.Fatalf("the quick invoice = %+v, want it issued with a payment request and its QR", quick)
	}
	onSite := quick.PaymentRequest.Reference
	created, ok := vipps.Payment(onSite)
	if !ok || created.Request["userFlow"] != "QR" || created.Request["customerInteraction"] != "CUSTOMER_PRESENT" || created.Amount != 125000 {
		t.Fatalf("Vipps' payment %s = %+v, want a QR payment of 125000 øre with the customer present", onSite, created)
	}
	paidOnSite := h.Now()
	if !vipps.Approve(onSite) {
		t.Fatalf("vippstest did not approve %s", onSite)
	}
	h.Advance(5 * time.Second)
	paymentsCycle(t, h)
	c := signIn()
	quickPaid := readPortInvoice(t, c, quick.Invoice.ID)
	if quickPaid.State != "paid" || len(quickPaid.Payments) != 1 || quickPaid.Payments[0].Source != "vipps" ||
		quickPaid.Payments[0].Amount != 1250 || quickPaid.Payments[0].PaymentAttemptID == nil ||
		*quickPaid.Payments[0].PaymentAttemptID != quick.PaymentRequest.AttemptID {
		t.Fatalf("the quick invoice after the worker = %+v, want paid by one Vipps payment of its attempt", quickPaid)
	}
	if n := vippsCaptures(vipps, onSite); n != 1 {
		t.Errorf("Vipps was asked for %d captures of %s, want one", n, onSite)
	}
	var status struct {
		State string `json:"state"`
	}
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/payment-requests/%d", invoiceAt(quick.Invoice.ID), quick.PaymentRequest.AttemptID), nil, &status)
	if status.State != "captured" {
		t.Errorf("the on-site request = %s, want captured", status.State)
	}

	// The day's list, and its reconciliation once the day is over.
	day := osloDay(t, paidOnSite)
	var list struct {
		Count       int32   `json:"count"`
		OnSiteTotal float64 `json:"onSiteTotal"`
		Payments    []struct {
			InvoiceID int64  `json:"invoiceId"`
			Flow      string `json:"flow"`
		} `json:"payments"`
	}
	okJSON(t, c, http.MethodGet, invoicesBase+"/vipps-payments?date="+day, nil, &list)
	if list.Count != 1 || list.OnSiteTotal != 1250 || len(list.Payments) != 1 || list.Payments[0].InvoiceID != quick.Invoice.ID ||
		list.Payments[0].Flow != "on_site" {
		t.Errorf("the day's list = %+v, want the on-site payment of 1250", list)
	}
	invoiceRefusedAs(t, "reconciling today", c.Do(http.MethodPost, invoicesBase+"/vipps-payments/reconciliations", map[string]any{"day": day}), "day_not_over")
	h.Advance(12 * time.Hour)
	c = signIn()
	var reconciled struct {
		Payments    int32   `json:"payments"`
		OnSiteTotal float64 `json:"onSiteTotal"`
	}
	okJSON(t, c, http.MethodPost, invoicesBase+"/vipps-payments/reconciliations", map[string]any{"day": day, "note": "Stemmer med oppgjøret"}, &reconciled)
	if reconciled.Payments != 1 || reconciled.OnSiteTotal != 1250 {
		t.Errorf("the reconciliation = %+v, want one on-site payment of 1250", reconciled)
	}

	// An ordinary invoice with its pay link, on its PDF.
	issued := issueDoc(t, c, newDraft(t, c, person.Id).ID)
	doc := readPortInvoice(t, c, issued.ID)
	if doc.PayLink == nil || doc.PayLink.URL == nil || !strings.HasPrefix(*doc.PayLink.URL, "https://"+publicHost+"/pay?token=") {
		t.Fatalf("the issued invoice's pay link = %+v, want one on the public host", doc.PayLink)
	}
	link, err := url.Parse(*doc.PayLink.URL)
	if err != nil {
		t.Fatal(err)
	}
	token := link.Query().Get("token")
	printed := false
	for _, page := range pdfPageTexts(t, c.Do(http.MethodGet, invoiceAt(issued.ID)+"/pdf", nil)) {
		printed = printed || slices.Contains(page, *doc.PayLink.URL)
	}
	if !printed {
		t.Errorf("the PDF does not print the pay link %s", *doc.PayLink.URL)
	}

	// Past its due date it is overdue, on the dashboard.
	h.Advance(16 * 24 * time.Hour)
	c = signIn()
	if got := readPortInvoice(t, c, issued.ID); got.State != "overdue" {
		t.Fatalf("the invoice past its due date = %s, want overdue", got.State)
	}
	if got := attentionTypes(t, c); !slices.Contains(got, fmt.Sprintf("invoiceOverdue %d", issued.ID)) {
		t.Errorf("attention = %v, want the overdue invoice", got)
	}

	// The pay page on the public host, where nothing else is served.
	payer := h.Client(t)
	for _, off := range []string{"/sign-in", invoicesBase + "/meta"} {
		if res := payer.Do(http.MethodGet, onPublicHost(off), nil, modtest.SkipContract("refused by the public host's gate before any route")); res.Status != http.StatusNotFound {
			t.Errorf("GET %s on the public host = %d, want 404", off, res.Status)
		}
	}
	var page struct {
		Payable    bool    `json:"payable"`
		OpenAmount float64 `json:"openAmount"`
		Reason     *string `json:"reason"`
	}
	okJSON(t, payer, http.MethodPost, onPublicHost(invoicesBase+"/pay/read"), map[string]any{"token": token}, &page)
	if !page.Payable || page.OpenAmount != issued.GrossTotal {
		t.Fatalf("the pay page = %+v, want %.2f payable", page, issued.GrossTotal)
	}
	var started struct {
		Reference   string `json:"reference"`
		RedirectURL string `json:"redirectUrl"`
	}
	res := payer.Do(http.MethodPost, onPublicHost(invoicesBase+"/pay/attempts"), map[string]any{"token": token, "acceptTerms": true})
	if res.Status != http.StatusCreated {
		t.Fatalf("the payment's start = %d %s, want 201", res.Status, res.Body)
	}
	res.JSON(&started)
	byLink, ok := vipps.Payment(started.Reference)
	if !ok || started.RedirectURL == "" || byLink.Amount != int64(issued.GrossTotal*100) {
		t.Fatalf("Vipps' payment %s = %+v, want one of the whole invoice", started.Reference, byLink)
	}
	returnURL, err := url.Parse(fmt.Sprint(byLink.Request["returnUrl"]))
	if err != nil || returnURL.Host != publicHost {
		t.Fatalf("the return URL = %v (%v), want the public host's", byLink.Request["returnUrl"], err)
	}
	nonce := returnURL.Query().Get("nonce")
	if !vipps.Approve(started.Reference) {
		t.Fatalf("vippstest did not approve %s", started.Reference)
	}
	h.Advance(5 * time.Second)
	paymentsCycle(t, h)
	var payState struct {
		State string `json:"state"`
	}
	okJSON(t, payer, http.MethodGet, onPublicHost(fmt.Sprintf("%s/pay/attempts/%s?nonce=%s", invoicesBase, started.Reference, url.QueryEscape(nonce))), nil, &payState)
	if payState.State != "paid" {
		t.Errorf("the payer's status = %s, want paid", payState.State)
	}
	c = signIn()
	paid := readPortInvoice(t, c, issued.ID)
	if paid.State != "paid" || len(paid.Payments) != 1 || paid.Payments[0].Source != "vipps" || paid.Payments[0].Amount != issued.GrossTotal {
		t.Fatalf("the invoice after the worker = %+v, want paid by one Vipps payment", paid)
	}
	if n := vippsCaptures(vipps, started.Reference); n != 1 {
		t.Errorf("Vipps was asked for %d captures of %s, want one", n, started.Reference)
	}
	if got := attentionTypes(t, c); slices.Contains(got, fmt.Sprintf("invoiceOverdue %d", issued.ID)) {
		t.Errorf("attention = %v, want the paid invoice gone", got)
	}

	// A refund made in the Vipps portal, seen by the refund watch.
	if !vipps.RefundInPortal(started.Reference, 50000) {
		t.Fatal("vippstest refused the portal refund")
	}
	h.Advance(24 * time.Hour)
	paymentsCycle(t, h)
	c = signIn()
	if got := attentionTypes(t, c); !slices.Contains(got, fmt.Sprintf("vippsRefundSeen %d", issued.ID)) {
		t.Errorf("attention = %v, want the refund seen on invoice %d", got, issued.ID)
	}

	// The person's export carries both attempts and each payment's attempt.
	var file struct {
		Modules map[string]json.RawMessage `json:"modules"`
	}
	okJSON(t, c, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/personal-data", person.Id), nil, &file)
	var section struct {
		Documents []struct {
			Payments []struct {
				Source           string `json:"source"`
				PaymentAttemptID *int64 `json:"paymentAttemptId"`
			} `json:"payments"`
			PayAttempts []struct {
				ID             int64   `json:"id"`
				Reference      string  `json:"reference"`
				Flow           string  `json:"flow"`
				State          string  `json:"state"`
				RefundedAmount *string `json:"refundedAmount"`
			} `json:"payAttempts"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(file.Modules["invoices"], &section); err != nil {
		t.Fatalf("modules.invoices = %s: %v", file.Modules["invoices"], err)
	}
	var attempts []string
	for _, d := range section.Documents {
		for _, a := range d.PayAttempts {
			refunded := "-"
			if a.RefundedAmount != nil {
				refunded = *a.RefundedAmount
			}
			attempts = append(attempts, fmt.Sprintf("%s %s %s %s", a.Reference, a.Flow, a.State, refunded))
			for _, p := range d.Payments {
				if p.Source == "vipps" && (p.PaymentAttemptID == nil || *p.PaymentAttemptID != a.ID) {
					t.Errorf("a Vipps payment names attempt %v, want %d", p.PaymentAttemptID, a.ID)
				}
			}
		}
	}
	slices.Sort(attempts)
	want := []string{onSite + " on_site captured -", started.Reference + " pay_link captured 500.00"}
	slices.Sort(want)
	if !slices.Equal(attempts, want) {
		t.Errorf("the export's attempts = %v, want %v", attempts, want)
	}
	if raw := string(file.Modules["invoices"]); strings.Contains(raw, started.RedirectURL) || strings.Contains(raw, "redirect") {
		t.Errorf("the export carries an attempt's redirect URL: %s", raw)
	}

	// The anonymisation revokes every live pay link of theirs.
	live := liveLinks(t, h, person.Id)
	if live == 0 {
		t.Fatal("the person has no live pay link before the anonymisation")
	}
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	okJSON(t, c, http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/anonymisation", person.Id),
		map[string]any{"anonymiseOn": h.Now().UTC().Format(time.DateOnly)}, nil)
	if ran, err := anonymisationWorker(t, h, modCustomers, modInvoices).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v", ran, err)
	}
	var event struct {
		Erased []invoiceKindCount `json:"erased"`
	}
	invoiceNewestEvent(t, c, person.Id, "customer.anonymised", &event)
	if !slices.Contains(event.Erased, invoiceKindCount{Kind: "invoices.payLinks", Count: int64(live)}) {
		t.Errorf("customer.anonymised's erased = %+v, want invoices.payLinks %d", event.Erased, live)
	}
	if n := liveLinks(t, h, person.Id); n != 0 {
		t.Errorf("%d live pay links after the anonymisation, want none", n)
	}
	okJSON(t, payer, http.MethodPost, onPublicHost(invoicesBase+"/pay/read"), map[string]any{"token": token}, &page)
	if page.Payable || page.Reason == nil || *page.Reason != "revoked" {
		t.Errorf("the pay page after the anonymisation = %+v, want revoked", page)
	}
	if got := readPortInvoice(t, c, issued.ID); got.State != "paid" || len(got.Payments) != 1 {
		t.Errorf("the invoice after the anonymisation = %+v, want it kept, paid", got)
	}
}
