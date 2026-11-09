package invoices_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
)

// The events the virtual employees listen to (design V30): invoices.payment.received,
// published by the registration itself, and invoices.invoice.overdue, published
// once for each invoice by a worker. Neither carries personal data.

// eventPayloads are the payloads of the events named name, in order.
func eventPayloads(t *testing.T, h *harness, name string) []json.RawMessage {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(), `SELECT payload FROM platform.events WHERE name = $1 ORDER BY id`, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		out = append(out, raw)
	}
	return out
}

// keysOf are the keys of a JSON object.
func keysOf(t *testing.T, raw json.RawMessage) map[string]bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func TestPaymentReceived_IsPublishedByTheRegistrationAndByNothingElse(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)

	registered(t, h, inv.ID, map[string]any{"amount": 300.1, "paidOn": "2026-09-12", "reference": "KID 0012345", "note": "Delbetaling til Kari Nordmann"})
	got := eventPayloads(t, h, "invoices.payment.received")
	if len(got) != 1 {
		t.Fatalf("%d events after one registration, want one", len(got))
	}
	var ev contracts.PaymentReceived
	if err := json.Unmarshal(got[0], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.InvoiceID != inv.ID || ev.CustomerID != customerAcme || ev.Amount != "300.10" || ev.Currency != "NOK" || ev.PaidOn != "2026-09-12" || ev.Source != "manual" || ev.PaymentID == 0 || ev.Number == 0 {
		t.Errorf("event = %+v, want the registration's ids, amount, currency, day and source", ev)
	}
	// No personal data: the fields of the contract and no more, and none of the free text the registration carried.
	want := map[string]bool{"paymentId": true, "invoiceId": true, "number": true, "customerId": true, "amount": true, "currency": true, "paidOn": true, "source": true, "registeredAt": true}
	for k := range keysOf(t, got[0]) {
		if !want[k] {
			t.Errorf("the event carries %q, which is not in the contract", k)
		}
	}
	for _, text := range []string{"Kari", "Nordmann", "KID 0012345", "Acme"} {
		if containsFold(string(got[0]), text) {
			t.Errorf("the event carries %q", text)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'invoices.payment.received' AND aggregate = 'invoices.payment' AND aggregate_id = $1`, strconv.FormatInt(ev.PaymentID, 10)); n != 1 {
		t.Errorf("the event is not under the payment's aggregate")
	}

	assertPaymentEvents(t, h)

	// A registration the invoice refuses (more than is open) publishes nothing: the event is on the same transaction.
	res := payer(t, h).Do(http.MethodPost, paymentsPath(inv.ID), pay(5000, "2026-09-12"))
	refusedAs(t, "an overpayment", res, "payment_exceeds_open")
	if n := len(eventPayloads(t, h, "invoices.payment.received")); n != 1 {
		t.Errorf("%d events after a refused registration, want still one", n)
	}
	// Removing a registration is no payment received.
	res = payer(t, h).Do(http.MethodPost, removalPath(inv.ID, getInvoice(t, h, inv.ID).Payments[0].ID), map[string]any{"reason": "Feil beløp"})
	if res.Status != http.StatusOK {
		t.Fatalf("remove: %d %s", res.Status, res.Body)
	}
	if n := len(eventPayloads(t, h, "invoices.payment.received")); n != 1 {
		t.Errorf("%d events after a removal, want still one", n)
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func TestInvoiceOverdue_IsAnnouncedOnceForEachInvoiceThatBecameOverdue(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	late := plantOverdue(t, h, overdueSpec{number: 9001, customer: customerAcme, issue: "2026-07-01", due: "2026-09-08"})
	plantOverdue(t, h, overdueSpec{number: 9004, customer: customerAcme, issue: "2026-06-01", due: "2026-07-01"}) // a backlog, not news
	plantOverdue(t, h, overdueSpec{number: 9002, customer: customerAcme, issue: "2026-09-01", due: "2026-10-15"}) // not due yet
	paid := plantOverdue(t, h, overdueSpec{number: 9003, customer: customerAcme, issue: "2026-07-01", due: "2026-09-08", gross: "500"})
	h.Exec(t, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at)
	           VALUES ($1, '2026-08-05', 500, 'NOK', gen_random_uuid(), now())`, paid)
	// The payment above published its event; this test is about the overdue ones.
	w := invoices.NewOverdueEventsWorker(h.Deps())
	n, err := w.RunCycle(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("first cycle announced %d (%v), want the one late invoice", n, err)
	}
	if n, err := w.RunCycle(context.Background()); err != nil || n != 0 {
		t.Errorf("second cycle announced %d (%v), want none: each invoice is announced once", n, err)
	}
	got := eventPayloads(t, h, "invoices.invoice.overdue")
	if len(got) != 1 {
		t.Fatalf("%d overdue events, want one", len(got))
	}
	var ev contracts.InvoiceOverdue
	if err := json.Unmarshal(got[0], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.InvoiceID != late || ev.Number != 9001 || ev.CustomerID != customerAcme || ev.DueDate != "2026-09-08" || ev.Day != "2026-09-12" || ev.Currency == "" || ev.GrossTotal != "1000.00" {
		t.Errorf("event = %+v, want the late invoice's figures on the business day", ev)
	}
	want := map[string]bool{"invoiceId": true, "number": true, "customerId": true, "dueDate": true, "day": true, "currency": true, "grossTotal": true}
	for k := range keysOf(t, got[0]) {
		if !want[k] {
			t.Errorf("the event carries %q, which is not in the contract", k)
		}
	}
	if containsFold(string(got[0]), "Kunde") || containsFold(string(got[0]), "Selger") {
		t.Errorf("the event carries a name: %s", got[0])
	}
}

// assertPaymentEvents holds every payment of the installation, whichever way it was
// registered (a person, a matched or applied bank line, a Vipps capture), to the
// event: one invoices.payment.received for it, under the contract's name, version
// and aggregate, whose payload decodes into contracts.PaymentReceived with no field
// unknown to it. Any insert into invoices.payments publishes: the trigger cannot know
// the path, and each path holds the invoice's lock.
func assertPaymentEvents(t *testing.T, h *harness) {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(), `SELECT id, invoice_id, amount::text, source FROM invoices.payments ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type pay struct {
		id, invoice int64
		amount, src string
	}
	var pays []pay
	for rows.Next() {
		var p pay
		if err := rows.Scan(&p.id, &p.invoice, &p.amount, &p.src); err != nil {
			t.Fatal(err)
		}
		pays = append(pays, p)
	}
	rows.Close()
	if len(pays) == 0 {
		t.Fatal("the test registered no payment")
	}
	for _, p := range pays {
		var name, aggregate, module string
		var version int
		var raw []byte
		err := h.Pool().QueryRow(context.Background(),
			`SELECT name, version, module, aggregate, payload FROM platform.events WHERE name = 'invoices.payment.received' AND aggregate_id = $1`,
			strconv.FormatInt(p.id, 10)).Scan(&name, &version, &module, &aggregate, &raw)
		if err != nil {
			t.Fatalf("payment %d (%s) has no event: %v", p.id, p.src, err)
		}
		var want contracts.PaymentReceived
		if name != want.EventName() || version != want.EventVersion() || module != "invoices" || aggregate != "invoices.payment" {
			t.Errorf("payment %d: event %s v%d by %s under %s, want the contract's", p.id, name, version, module, aggregate)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var ev contracts.PaymentReceived
		if err := dec.Decode(&ev); err != nil {
			t.Fatalf("payment %d: the payload does not decode into contracts.PaymentReceived: %v", p.id, err)
		}
		if ev.PaymentID != p.id || ev.InvoiceID != p.invoice || ev.Source != p.src || ev.Currency != "NOK" {
			t.Errorf("payment %d: event %+v, want its id, invoice, source and currency", p.id, ev)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'invoices.payment.received'`); n != len(pays) {
		t.Errorf("%d events for %d payments, want one each", n, len(pays))
	}
}
