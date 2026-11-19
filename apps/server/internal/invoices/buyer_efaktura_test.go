package invoices_test

import (
	"testing"
	"time"
)

// An invoice delivered as eFaktura (phase 6 delivery D) is delivered: its
// transmission on the eFaktura channel clears invoiceUndelivered as an EHF
// one does.
func TestAttention_AnEFakturaDeliveryClearsUndelivered(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	obligated(h, customerAcme, true)
	h.Advance(toYear2028)
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	h.Advance(8 * 24 * time.Hour)
	if items := ofType(attentionOf(t, h.SignIn(t, "invoices:access")), "invoiceUndelivered"); len(items) != 1 {
		t.Fatalf("before the eFaktura = %+v, want the invoice", items)
	}
	h.Exec(t, `INSERT INTO invoices.transmissions (invoice_id, channel, provider, idempotency_key,
		pdf_sha256, next_attempt_at, queued_at, created_by_user_id, efaktura_identifier, shipment_id, payment_type, pdf_object_key,
		status, submitted_at, delivered_at)
		VALUES ($1, 'efaktura', 'mps', gen_random_uuid(), repeat('a', 64), now(), now(), gen_random_uuid(), '123456789',
		gen_random_uuid(), 'efaktura', 'documents/x.pdf', 'delivered', now(), now())`, inv.ID)
	if items := ofType(attentionOf(t, h.SignIn(t, "invoices:access")), "invoiceUndelivered"); len(items) != 0 {
		t.Errorf("after the eFaktura = %+v, want none", items)
	}
}
