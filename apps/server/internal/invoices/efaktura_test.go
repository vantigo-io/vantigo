package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice/mps/mpstest"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// efakturaJSON is an invoice's eFaktura block as a client reads it.
type efakturaJSON struct {
	Status        string  `json:"status"`
	CanSend       bool    `json:"canSend"`
	BlockedBy     *string `json:"blockedBy"`
	Transmissions []struct {
		ID          int64  `json:"id"`
		Status      string `json:"status"`
		PaymentType string `json:"paymentType"`
	} `json:"transmissions"`
	Claim *struct {
		Status string  `json:"status"`
		Kid    string  `json:"kid"`
		Amount float64 `json:"amount"`
	} `json:"claim"`
}

type sendEfakturaJSON struct {
	Invoice    invoiceJSON `json:"invoice"`
	Avtalegiro struct {
		Included bool    `json:"included"`
		Reason   *string `json:"reason"`
	} `json:"avtalegiro"`
}

const (
	consumerProviderPath = "/api/v1/invoices/settings/consumer-provider"
	avtaleGiroPath       = "/api/v1/invoices/settings/avtalegiro"
	personPhone          = "+4791234567"
	personEmail          = "kari@example.no"
)

func sendEfakturaPath(id int64) string { return fmt.Sprintf("%s/%d/send-efaktura", invoicesPath, id) }

// efakturaReady is an installation that can send eFaktura: a complete
// seller under a KID agreement of 10 digits, MOD10; the consumer provider's
// settings with both agreements active; the fake on the harness clock; and
// customerPerson found at it by phone and e-mail, accepting everything.
func efakturaReady(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	withKidAgreement(t, h, 10, kid.Mod10)
	h.mps.SetClock(h.Now)
	if res := manager(t, h).Do(http.MethodPut, consumerProviderPath, map[string]any{
		"provider": "mps", "efakturaAgreement": "active", "avtalegiroAgreement": "active",
		"creditorAccount": "86011117947", "brandName": "Selger AS", "abbreviatedName": "SELGER",
	}); res.Status != http.StatusOK {
		t.Fatalf("PUT the consumer provider = %d %s", res.Status, res.Body)
	}
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) {
		p.Phone, p.InvoiceEmail = "912 34 567", personEmail
	})
	h.mps.AddPerson(mpstest.Person{Identifier: "123456789", Phone: personPhone, Email: personEmail,
		FirstName: "Kari", LastName: "Nordmann", AutoAccept: true})
	return h
}

// efakturaInvoice issues a draft of one line of 1 000 for customer.
func efakturaInvoice(t *testing.T, h *harness, customer int32) invoiceJSON {
	t.Helper()
	draft := createDraft(t, h, draftBody(customer, line("Rådgivning", 1, 1000, vat25)))
	return issued(t, h, draft.ID)
}

func efakturaWorker(t *testing.T, h *harness) *invoices.EfakturaWorker {
	t.Helper()
	return invoices.NewEfakturaWorker(h.Deps())
}

func runEfakturaCycle(t *testing.T, w *invoices.EfakturaWorker) {
	t.Helper()
	if err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
}

// The whole road (I29-I34): AvtaleGiro on, the invoice's KID one digit
// short, the customer KID printed on the snapshot; the mandate synced from
// the queue and resolved by the customer number; the send as both, with the
// claim; the worker's shipment carrying the customer KID; the poll accepting
// the claim and delivering the eFaktura; and a credit note cancelling the
// claim before its deadline.
func TestEfaktura_SendAsBoth_ThenCreditCancelsTheClaim(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	if res := manager(t, h).Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": true}); res.Status != http.StatusOK {
		t.Fatalf("AvtaleGiro on = %d %s", res.Status, res.Body)
	}
	inv := efakturaInvoice(t, h, customerPerson)
	if inv.Kid == nil || len(*inv.Kid) != 10 || (*inv.Kid)[0] != '0' {
		t.Fatalf("the KID under AvtaleGiro = %v, want 10 digits beginning with 0", inv.Kid)
	}
	customerKID, err := kid.CustomerKID(10002, 10, kid.Mod10)
	if err != nil {
		t.Fatal(err)
	}
	var printed *string
	if err := h.Pool().QueryRow(context.Background(), `SELECT buyer_avtalegiro_kid FROM invoices.invoices WHERE id = $1`, inv.ID).Scan(&printed); err != nil {
		t.Fatal(err)
	}
	if printed == nil || *printed != customerKID {
		t.Errorf("the snapshot's customer KID = %v, want %s", printed, customerKID)
	}

	// The mandate, from the queue.
	h.mps.QueueMandates(mpstest.Mandate{KID: customerKID, Status: "ACTV"})
	w := efakturaWorker(t, h)
	runEfakturaCycle(t, w)
	var mandates struct {
		Items []struct {
			Kid        string `json:"kid"`
			CustomerID *int32 `json:"customerId"`
			Status     string `json:"status"`
		} `json:"items"`
	}
	h.SignIn(t, "invoices:access").Do(http.MethodGet, "/api/v1/invoices/avtalegiro/mandates", nil).JSON(&mandates)
	if len(mandates.Items) != 1 || mandates.Items[0].CustomerID == nil || *mandates.Items[0].CustomerID != customerPerson || mandates.Items[0].Status != "active" {
		t.Fatalf("mandates = %+v, want the one resolved to customer %d", mandates.Items, customerPerson)
	}

	// The send.
	res := issuer(t, h).Do(http.MethodPost, sendEfakturaPath(inv.ID), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("send-efaktura = %d %s", res.Status, res.Body)
	}
	var sent sendEfakturaJSON
	res.JSON(&sent)
	if !sent.Avtalegiro.Included || sent.Invoice.Efaktura == nil || sent.Invoice.Efaktura.Status != "queued" ||
		sent.Invoice.Efaktura.Claim == nil || sent.Invoice.Efaktura.Claim.Status != "submitted" || sent.Invoice.Efaktura.Claim.Kid != customerKID {
		t.Fatalf("the send answered %+v %+v", sent.Avtalegiro, sent.Invoice.Efaktura)
	}
	if again := issuer(t, h).Do(http.MethodPost, sendEfakturaPath(inv.ID), nil); again.Status != http.StatusConflict || !strings.Contains(string(again.Body), "efaktura_already_sent") {
		t.Errorf("a second send = %d %s, want efaktura_already_sent", again.Status, again.Body)
	}

	// The worker submits it.
	runEfakturaCycle(t, w)
	shipped := h.mps.Shipments()
	if len(shipped) != 1 || shipped[0].PaymentType != "EFAKTURA_AND_AVTALEGIRO" || shipped[0].KID != customerKID ||
		shipped[0].Amount != "1250.00" || shipped[0].Identifier != "123456789" || len(shipped[0].PDF) == 0 {
		t.Fatalf("the fake holds %+v", shipped)
	}

	// The poll: accepted, delivered; the claim waits for its due date.
	h.Advance(10 * time.Minute)
	runEfakturaCycle(t, w)
	got := getInvoice(t, h, inv.ID)
	if got.Efaktura == nil || got.Efaktura.Status != "delivered" || got.Efaktura.Claim == nil || got.Efaktura.Claim.Status != "accepted" {
		t.Fatalf("after the poll: %+v", got.Efaktura)
	}

	// A credit note asks the claim to cancel, and the worker cancels it.
	credit := creditDraft(t, h, inv.ID)
	issued(t, h, credit.ID)
	if c := getInvoice(t, h, inv.ID).Efaktura.Claim; c == nil || c.Status != "cancel_requested" {
		t.Fatalf("after the credit note: %+v", c)
	}
	runEfakturaCycle(t, w)
	if c := getInvoice(t, h, inv.ID).Efaktura.Claim; c == nil || c.Status != "cancelled" {
		t.Errorf("after the worker: %+v, want cancelled", c)
	}
	if n := len(eventPayloads(t, h, "invoices.avtalegiro.claim_changed")); n < 4 {
		t.Errorf("%d claim events, want one per move", n)
	}
}

// The refusals, each by its code: a credit note, a person not found, a due
// date too far, and an installation without settings.
func TestEfaktura_Refusals(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	c := issuer(t, h)

	inv := efakturaInvoice(t, h, customerPerson)
	credit := creditDraft(t, h, inv.ID)
	issued(t, h, credit.ID)
	if res := c.Do(http.MethodPost, sendEfakturaPath(credit.ID), nil); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "efaktura_credit_note") {
		t.Errorf("a credit note = %d %s", res.Status, res.Body)
	}

	other := efakturaInvoice(t, h, customerAcme)
	if res := c.Do(http.MethodPost, sendEfakturaPath(other.ID), nil); res.Status != http.StatusConflict ||
		!strings.Contains(string(res.Body), "efaktura_not_found") || !strings.Contains(string(res.Body), `"efakturaReason":"not_found"`) {
		t.Errorf("a person not found = %d %s", res.Status, res.Body)
	}
	// No national identity number went anywhere.
	for _, body := range h.mps.Requests() {
		if strings.Contains(body, "socialSecurityNumber") {
			t.Errorf("a request named a fødselsnummer: %s", body)
		}
	}

	h.Exec(t, `ALTER TABLE invoices.invoices DISABLE TRIGGER tr_invoices_immutable`)
	h.Exec(t, `UPDATE invoices.invoices SET due_date = due_date + 400 WHERE id = $1`, inv.ID)
	h.Exec(t, `ALTER TABLE invoices.invoices ENABLE TRIGGER tr_invoices_immutable`)
	if res := c.Do(http.MethodPost, sendEfakturaPath(inv.ID), nil); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "efaktura_due_too_far") {
		t.Errorf("a due date too far = %d %s", res.Status, res.Body)
	}

	if res := manager(t, h).Do(http.MethodDelete, consumerProviderPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	if res := c.Do(http.MethodPost, sendEfakturaPath(other.ID), nil); res.Status != http.StatusServiceUnavailable || !strings.Contains(string(res.Body), "efaktura_unavailable") {
		t.Errorf("without settings = %d %s", res.Status, res.Body)
	}
}

// The settings read back without the certificate, Verify against the fake,
// the lookup read and made — found, sendable, no identifier answered — and an
// unresolved mandate assigned to a customer, once.
func TestEfaktura_SettingsLookupAndMandates(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	m := manager(t, h)
	var stored struct {
		HasCredentials bool   `json:"hasCredentials"`
		OperatorReady  bool   `json:"operatorReady"`
		BrandName      string `json:"brandName"`
	}
	m.Do(http.MethodGet, consumerProviderPath, nil).JSON(&stored)
	if !stored.HasCredentials || !stored.OperatorReady || stored.BrandName != "Selger AS" {
		t.Errorf("GET = %+v", stored)
	}
	if res := m.Do(http.MethodPost, consumerProviderPath+"/verify", nil); res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"result":"ok"`) {
		t.Errorf("verify = %d %s", res.Status, res.Body)
	}

	lookupPath := fmt.Sprintf("/api/v1/invoices/customers/%d/efaktura-lookup", customerPerson)
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, lookupPath, nil); res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"status":"not_checked"`) {
		t.Errorf("GET before a lookup = %d %s", res.Status, res.Body)
	}
	res := issuer(t, h).Do(http.MethodPost, lookupPath, nil)
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"status":"found"`) || !strings.Contains(string(res.Body), `"sendable":true`) ||
		strings.Contains(string(res.Body), "123456789") {
		t.Errorf("POST lookup = %d %s", res.Status, res.Body)
	}

	h.Exec(t, `INSERT INTO invoices.avtalegiro_mandates (kid, status, provider_seen_at, updated_at) VALUES ('9999990', 'active', now(), now())`)
	id := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.avtalegiro_mandates WHERE kid = '9999990'`)
	resolve := fmt.Sprintf("/api/v1/invoices/avtalegiro/mandates/%d/resolve", id)
	payments := h.SignIn(t, "invoices:access", "invoices:payments")
	// The KID carries customer number 99999, not this customer's 10002.
	if res := payments.Do(http.MethodPost, resolve, map[string]any{"customerId": customerPerson}); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "mandate_kid_mismatch") {
		t.Errorf("a mismatched resolve = %d %s", res.Status, res.Body)
	}
	if res := payments.Do(http.MethodPost, resolve, map[string]any{"customerId": customerAcme, "confirmMismatch": true, "reason": "x"}); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "mandate_customer_not_person") {
		t.Errorf("a business = %d %s", res.Status, res.Body)
	}
	if res := payments.Do(http.MethodPost, resolve, map[string]any{"customerId": customerMerged, "confirmMismatch": true, "reason": "x"}); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "customer_merged") {
		t.Errorf("a merged-away customer = %d %s", res.Status, res.Body)
	}
	if res := payments.Do(http.MethodPost, resolve, map[string]any{"customerId": customerPerson, "confirmMismatch": true, "reason": "The payer's old customer number"}); res.Status != http.StatusOK ||
		!strings.Contains(string(res.Body), `"resolutionNote":"The payer's old customer number"`) {
		t.Errorf("a confirmed resolve = %d %s", res.Status, res.Body)
	}
	if res := payments.Do(http.MethodPost, resolve, map[string]any{"customerId": customerPerson, "confirmMismatch": true, "reason": "x"}); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "mandate_resolved") {
		t.Errorf("a second resolve = %d %s", res.Status, res.Body)
	}
	unassign := fmt.Sprintf("/api/v1/invoices/avtalegiro/mandates/%d/unassign", id)
	if res := payments.Do(http.MethodPost, unassign, nil); res.Status != http.StatusOK || strings.Contains(string(res.Body), "customerId") {
		t.Errorf("unassign = %d %s", res.Status, res.Body)
	}
	if res := payments.Do(http.MethodPost, unassign, nil); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "mandate_not_resolved") {
		t.Errorf("a second unassign = %d %s", res.Status, res.Body)
	}
}

// The AvtaleGiro switch refuses without a KID agreement, and when an issued
// KID already begins with 9 (reading 12).
func TestAvtaleGiro_TheKIDSpace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	saveSeller(t, h, completeSeller(1))
	m := manager(t, h)
	if res := m.Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": true}); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "kid_agreement_missing") {
		t.Errorf("without a KID agreement = %d %s", res.Status, res.Body)
	}
	withKidAgreement(t, h, 4, kid.Mod10)
	if res := m.Do(http.MethodPut, consumerProviderPath, map[string]any{
		"provider": "mps", "efakturaAgreement": "active", "avtalegiroAgreement": "active",
		"creditorAccount": "86011117947", "brandName": "Selger AS", "abbreviatedName": "SELGER",
	}); res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	// A KID of four holds two digits of number with AvtaleGiro on; the series
	// starts at 1, so 999 issued numbers would not fit — 1 does.
	if res := m.Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": true}); res.Status != http.StatusOK {
		t.Fatalf("AvtaleGiro on = %d %s", res.Status, res.Body)
	}
	if res := m.Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": false}); res.Status != http.StatusOK {
		t.Fatalf("AvtaleGiro off = %d %s", res.Status, res.Body)
	}
	h.Exec(t, `ALTER TABLE invoices.invoices DISABLE TRIGGER tr_invoices_immutable`)
	inv := efakturaInvoice(t, h, customerAcme)
	h.Exec(t, `UPDATE invoices.invoices SET kid = '9993' WHERE id = $1`, inv.ID)
	h.Exec(t, `ALTER TABLE invoices.invoices ENABLE TRIGGER tr_invoices_immutable`)
	if res := m.Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": true}); res.Status != http.StatusConflict || !strings.Contains(string(res.Body), "avtalegiro_kid_space_taken") {
		t.Errorf("with a KID beginning with 9 = %d %s", res.Status, res.Body)
	}
}

// The worker on a pool of one connection: a cycle with nothing due, a
// submit, a poll and the housekeeping all finish — no step holds a
// connection while it waits for another.
func TestEfakturaWorker_RunsTheCycleOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	inv := efakturaInvoice(t, h, customerPerson)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	d := h.Deps()
	d.Pool = one
	w := invoices.NewEfakturaWorker(d)
	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle with nothing due on a pool of one: %v", err)
	}
	if res := issuer(t, h).Do(http.MethodPost, sendEfakturaPath(inv.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("send-efaktura = %d %s", res.Status, res.Body)
	}
	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle submitting on a pool of one: %v", err)
	}
	h.Advance(10 * time.Minute)
	if err := w.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle polling on a pool of one: %v", err)
	}
	if got := getInvoice(t, h, inv.ID).Efaktura; got == nil || got.Status != "delivered" {
		t.Errorf("after the cycles: %+v, want delivered", got)
	}
}
