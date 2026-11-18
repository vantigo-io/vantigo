package invoices_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// plantClaim records an eFaktura sent as both for invoice and its accepted
// claim on the mandate with customerKID, due on due for amount, as the
// worker would have left them.
func plantClaim(t *testing.T, h *harness, invoice int64, customer int32, customerKID, due, amount string) int64 {
	t.Helper()
	h.Exec(t, `INSERT INTO invoices.avtalegiro_mandates (kid, customer_id, status, provider_seen_at, updated_at)
		VALUES ($1, $2, 'active', now(), now()) ON CONFLICT (kid) DO NOTHING`, customerKID, customer)
	transmission := modtest.One[int64](t, h.Harness, `INSERT INTO invoices.transmissions (invoice_id, channel, provider, idempotency_key,
		pdf_sha256, next_attempt_at, queued_at, created_by_user_id, efaktura_identifier, shipment_id, payment_type, pdf_object_key,
		status, submitted_at, delivered_at)
		VALUES ($1, 'efaktura', 'mps', gen_random_uuid(), repeat('a', 64), now(), now(), gen_random_uuid(), '123456789',
		gen_random_uuid(), 'both', 'documents/x.pdf', 'delivered', now(), now()) RETURNING id`, invoice)
	return modtest.One[int64](t, h.Harness, `INSERT INTO invoices.avtalegiro_claims (invoice_id, transmission_id, mandate_id, amount, due_date, kid,
		status, submitted_at, accepted_at)
		SELECT $1, $2, m.id, $3::numeric, $4::date, $5::text, 'accepted', now(), now() FROM invoices.avtalegiro_mandates m WHERE m.kid = $5::text
		RETURNING id`, invoice, transmission, amount, due, customerKID)
}

// The mandate rule (I32), before invoiceOfKid: a debit on the customer KID
// pays the claim due that day with that amount, which moves to paid in the
// match's transaction; an amount that differs, or two claims that fit, queue
// kid_unknown; and a 9 KID that is no mandate's takes today's path.
func TestMatch_TheMandateRule(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	customerKID, err := kid.CustomerKID(10002, 7, kid.Mod10)
	if err != nil {
		t.Fatal(err)
	}
	draft := func() invoiceJSON {
		return kidded(t, issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Strøm", 1, 1000, vat25))).ID))
	}
	fits, differs, twinA, twinB := draft(), draft(), draft(), draft()
	claim := plantClaim(t, h, fits.ID, customerPerson, customerKID, "2026-10-05", "1250.00")
	plantClaim(t, h, differs.ID, customerPerson, customerKID, "2026-10-02", "1250.00")
	plantClaim(t, h, twinA.ID, customerPerson, customerKID, "2026-10-01", "1250.00")
	plantClaim(t, h, twinB.ID, customerPerson, customerKID, "2026-10-01", "1250.00")
	toMatchDay(h)

	r := imported(t, importer(t, h), bankfiletest.OCR("1",
		kidPay(sellerAccount, 5, 1250, customerKID, "1"),
		kidPay(sellerAccount, 2, 1249, customerKID, "2"),
		kidPay(sellerAccount, 1, 1250, customerKID, "3"),
		kidPay(sellerAccount, 6, 1250, "9876543", "4"),
	))
	if got := stateOf(t, h, lineID(t, h, r.File.ID, "1")); got != "matched -" {
		t.Errorf("the debit that fits: %s, want matched", got)
	}
	if got := paymentsFrom(t, h, lineID(t, h, r.File.ID, "1")); len(got) != 1 || got[0] != paid(fits, "1250.00", "ocr", "2026-10-05", customerKID) {
		t.Errorf("payments from the debit = %v", got)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT status FROM invoices.avtalegiro_claims WHERE id = $1`, claim); got != "paid" {
		t.Errorf("the claim = %s, want paid", got)
	}
	for _, ref := range []string{"2", "3"} {
		if got := stateOf(t, h, lineID(t, h, r.File.ID, ref)); got != "exception kid_unknown" {
			t.Errorf("line %s: %s, want exception kid_unknown", ref, got)
		}
	}
	if got := stateOf(t, h, lineID(t, h, r.File.ID, "4")); got == "exception kid_unknown" || got == "matched -" {
		t.Errorf("a 9 KID that is no mandate's: %s, want today's path (an invalid KID)", got)
	}
}

// An AvtaleGiro debit is never an automatic split (I32 before I14): a line on
// a mandate's KID whose remittance names two of the customer's invoices that
// add up to it — what B-2 would split — is queued kid_unknown with nothing
// paid, and a debit above what its claim's invoice still has open is queued
// exceeds_open; kept as credit there, it pays the claim (plan reading 15).
func TestMatch_AnAvtaleGiroDebitIsNeverSplitAndKeptAsCreditPaysTheClaim(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	customerKID, err := kid.CustomerKID(10002, 7, kid.Mod10)
	if err != nil {
		t.Fatal(err)
	}
	draft := func() invoiceJSON {
		return kidded(t, issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Strøm", 1, 1000, vat25))).ID))
	}
	a, b, claimed := draft(), draft(), draft()
	claim := plantClaim(t, h, claimed.ID, customerPerson, customerKID, "2026-10-05", "1250.00")
	registered(t, h, claimed.ID, pay(250, "2026-09-12"))
	toMatchDay(h)
	c := importer(t, h)

	r := imported(t, c, camtFile("AG-1", sellerAccount,
		camtText(6, 2500, customerKID, "Faktura "+num(a)+" og "+num(b), nil, "SPLIT"),
		camtText(5, 1250, customerKID, "", nil, "OVER"),
	))
	split, over := lineID(t, h, r.File.ID, "SPLIT"), lineID(t, h, r.File.ID, "OVER")
	if got := stateOf(t, h, split); got != "exception kid_unknown" {
		t.Errorf("the debit naming two invoices: %s, want exception kid_unknown", got)
	}
	if got := paymentsFrom(t, h, split); len(got) != 0 {
		t.Errorf("payments from the debit naming two invoices = %v, want none", got)
	}
	if got := stateOf(t, h, over); got != "exception exceeds_open" {
		t.Fatalf("the debit above what is open: %s, want exception exceeds_open", got)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT status FROM invoices.avtalegiro_claims WHERE id = $1`, claim); got != "accepted" {
		t.Errorf("the claim while its debit is queued = %s, want accepted", got)
	}
	l := acted(t, c, over, "keep-as-credit", map[string]any{})
	if l.Status != "resolved" {
		t.Errorf("kept = %s, want resolved", l.Status)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT status FROM invoices.avtalegiro_claims WHERE id = $1`, claim); got != "paid" {
		t.Errorf("the claim after its debit was kept as credit = %s, want paid", got)
	}
	if b := balanceOf(t, h, customerPerson); b != 250 {
		t.Errorf("the credit = %v, want 250", b)
	}
}
