package invoices_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/consumerinvoice/mps/mpstest"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// bothSent is efakturaReady with AvtaleGiro on, the person's mandate synced
// and one invoice sent as both, not yet submitted.
func bothSent(t *testing.T) (*harness, invoiceJSON, *invoices.EfakturaWorker) {
	t.Helper()
	h := efakturaReady(t)
	if res := manager(t, h).Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": true}); res.Status != http.StatusOK {
		t.Fatalf("AvtaleGiro on = %d %s", res.Status, res.Body)
	}
	inv := efakturaInvoice(t, h, customerPerson)
	customerKID, err := kid.CustomerKID(10002, 10, kid.Mod10)
	if err != nil {
		t.Fatal(err)
	}
	h.mps.QueueMandates(mpstest.Mandate{KID: customerKID, Status: "ACTV"})
	w := efakturaWorker(t, h)
	runEfakturaCycle(t, w)
	res := issuer(t, h).Do(http.MethodPost, sendEfakturaPath(inv.ID), nil)
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"included":true`) {
		t.Fatalf("send-efaktura = %d %s", res.Status, res.Body)
	}
	return h, inv, w
}

func claimStatus(t *testing.T, h *harness, invoiceID int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT status FROM invoices.avtalegiro_claims WHERE invoice_id = $1 ORDER BY id DESC LIMIT 1`, invoiceID)
}

func efakturaStatusOf(t *testing.T, h *harness, invoiceID int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT status FROM invoices.transmissions WHERE invoice_id = $1 AND channel = 'efaktura' ORDER BY id DESC LIMIT 1`, invoiceID)
}

// A submission whose answer never came: unconfirmed after 48 hours with no
// reference, the worker asks by its own shipment id, and the network that
// never received it fails it — and its claim is cancelled.
func TestEfaktura_UnconfirmedIsAskedByItsShipmentID(t *testing.T) {
	t.Parallel()
	h, inv, w := bothSent(t)
	h.mps.FailOn(mpstest.OpShipment, http.StatusInternalServerError)
	runEfakturaCycle(t, w)
	h.Advance(49 * time.Hour)
	runEfakturaCycle(t, w)
	if got := efakturaStatusOf(t, h, inv.ID); got != "unconfirmed" {
		t.Fatalf("after 48 hours: %s, want unconfirmed", got)
	}
	h.Advance(25 * time.Hour)
	runEfakturaCycle(t, w)
	if got := efakturaStatusOf(t, h, inv.ID); got != "failed" {
		t.Errorf("after the poll by shipment id: %s, want failed", got)
	}
	if got := claimStatus(t, h, inv.ID); got != "cancelled" {
		t.Errorf("the claim: %s, want cancelled", got)
	}
}

// A person resolves an unconfirmed eFaktura through the resolve endpoint;
// its claim, never accepted, is cancelled.
func TestEfaktura_APersonResolvesAnUnconfirmedOne(t *testing.T) {
	t.Parallel()
	h, inv, w := bothSent(t)
	h.mps.FailOn(mpstest.OpShipment, http.StatusInternalServerError)
	h.mps.FailOn(mpstest.OpStatus, http.StatusInternalServerError)
	runEfakturaCycle(t, w)
	h.Advance(49 * time.Hour)
	runEfakturaCycle(t, w)
	tid := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.transmissions WHERE invoice_id = $1 AND channel = 'efaktura'`, inv.ID)
	path := fmt.Sprintf("%s/%d/transmissions/%d/resolve", invoicesPath, inv.ID, tid)
	res := issuer(t, h).Do(http.MethodPost, path, map[string]any{"outcome": "failed", "note": "MPS support: never received"})
	if res.Status != http.StatusOK {
		t.Fatalf("resolve = %d %s", res.Status, res.Body)
	}
	if got := efakturaStatusOf(t, h, inv.ID); got != "failed" {
		t.Errorf("after the resolve: %s, want failed", got)
	}
	if got := claimStatus(t, h, inv.ID); got != "cancelled" {
		t.Errorf("the claim: %s, want cancelled", got)
	}
	if again := issuer(t, h).Do(http.MethodPost, path, map[string]any{"outcome": "failed", "note": "again"}); again.Status != http.StatusConflict {
		t.Errorf("a second resolve = %d, want 409", again.Status)
	}
}

// A credit note before the eFaktura was handed over cancels the claim at
// once, and the network is never asked to cancel what it never got.
func TestEfaktura_CreditBeforeSubmitCancelsLocally(t *testing.T) {
	t.Parallel()
	h, inv, w := bothSent(t)
	issued(t, h, creditDraft(t, h, inv.ID).ID)
	if got := claimStatus(t, h, inv.ID); got != "cancelled" {
		t.Errorf("the claim after the credit note: %s, want cancelled", got)
	}
	runEfakturaCycle(t, w)
	for _, body := range h.mps.Requests() {
		if strings.Contains(body, "cancelTransaction") {
			t.Errorf("the network was asked to cancel: %s", body)
		}
	}
	if shipped := h.mps.Shipments(); len(shipped) != 1 || shipped[0].PaymentType != "EFAKTURA" {
		t.Errorf("the shipment = %+v, want the eFaktura alone", shipped)
	}
}

// A lost answer and a failed store both ask for the same page again, under
// the same request id, so no mandate is lost between pages.
func TestEfaktura_MandatePagesAreNeverLost(t *testing.T) {
	h := efakturaReady(t)
	if res := manager(t, h).Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": true}); res.Status != http.StatusOK {
		t.Fatalf("AvtaleGiro on = %d %s", res.Status, res.Body)
	}
	first, _ := kid.CustomerKID(10002, 10, kid.Mod10)
	second, _ := kid.CustomerKID(10009, 10, kid.Mod10)
	h.mps.SetPageSize(1)
	h.mps.QueueMandates(mpstest.Mandate{KID: first, Status: "ACTV"}, mpstest.Mandate{KID: second, Status: "ACTV"})
	w := efakturaWorker(t, h)
	count := func() int64 {
		return modtest.One[int64](t, h.Harness, `SELECT count(*) FROM invoices.avtalegiro_mandates`)
	}

	h.mps.DropAnswersOn(mpstest.OpMandates, true)
	runEfakturaCycle(t, w)
	if n := count(); n != 0 {
		t.Fatalf("after a lost answer: %d mandates, want none", n)
	}
	h.mps.DropAnswersOn(mpstest.OpMandates, false)

	failed := false
	remove := invoices.SetBeforeMandateUpsert(func(string) error {
		if !failed {
			failed = true
			return errors.New("the store failed")
		}
		return nil
	})
	runEfakturaCycle(t, w)
	remove()
	if n := count(); n != 0 || !failed {
		t.Fatalf("after a failed store: %d mandates (failed %v), want none", n, failed)
	}

	runEfakturaCycle(t, w)
	if n := count(); n != 2 {
		t.Errorf("after the replay: %d mandates, want both", n)
	}
	if pending := modtest.One[bool](t, h.Harness, `SELECT mandate_request_id IS NOT NULL FROM invoices.consumer_provider_credentials`); pending {
		t.Error("a request id is still pending after the queue was drained")
	}
}

// AvtaleGiro cannot be turned on while eFaktura is switched off.
func TestAvtaleGiro_RefusedWhileEfakturaIsOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("INVOICES_EFAKTURA_ENABLED", "0"))
	saveSeller(t, h, completeSeller(1))
	withKidAgreement(t, h, 10, kid.Mod10)
	if res := manager(t, h).Do(http.MethodPut, consumerProviderPath, map[string]any{
		"provider": "mps", "efakturaAgreement": "active", "avtalegiroAgreement": "active",
		"creditorAccount": "86011117947", "brandName": "Selger AS", "abbreviatedName": "SELGER",
	}); res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	if res := manager(t, h).Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": true}); res.Status != http.StatusServiceUnavailable || !strings.Contains(string(res.Body), "efaktura_unavailable") {
		t.Errorf("AvtaleGiro on with eFaktura off = %d %s", res.Status, res.Body)
	}
}

// Once AvtaleGiro has been on, the customer KIDs' digit stays reserved after
// it is turned off: a number that would give a KID beginning with 9 is
// refused, never issued into the customer KIDs' space.
func TestAvtaleGiro_TheReservedSpaceOutlivesTheSwitch(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	withKidAgreement(t, h, 4, kid.Mod10)
	m := manager(t, h)
	for _, on := range []bool{true, false} {
		if res := m.Do(http.MethodPut, avtaleGiroPath, map[string]any{"enabled": on}); res.Status != http.StatusOK {
			t.Fatalf("AvtaleGiro %v = %d %s", on, res.Status, res.Body)
		}
	}
	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 950)
		ON CONFLICT (counter_name) DO UPDATE SET next_value = 950`)
	draft := createDraft(t, h, draftBody(customerAcme, line("Rådgivning", 1, 1000, vat25)))
	if p := refusedWith(t, h, draft.ID, "", "kid_length_exceeded"); p.Detail == "" {
		t.Error("kid_length_exceeded without a detail")
	}
}

// A 9 KID that is a mandate's but that no claim fits takes today's path: an
// invoice issued before the space was reserved, carrying that very KID, is
// matched.
func TestMatch_AMandateKIDWithNoClaimFallsThrough(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	customerKID, err := kid.CustomerKID(10002, 7, kid.Mod10)
	if err != nil {
		t.Fatal(err)
	}
	inv := kidded(t, issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Strøm", 1, 1000, vat25))).ID))
	h.Exec(t, `ALTER TABLE invoices.invoices DISABLE TRIGGER tr_invoices_immutable`)
	h.Exec(t, `UPDATE invoices.invoices SET number = 910002, kid = $2 WHERE id = $1`, inv.ID, customerKID)
	h.Exec(t, `ALTER TABLE invoices.invoices ENABLE TRIGGER tr_invoices_immutable`)
	h.Exec(t, `INSERT INTO invoices.avtalegiro_mandates (kid, customer_id, status, provider_seen_at, updated_at)
		VALUES ($1, $2, 'active', now(), now())`, customerKID, customerPerson)
	toMatchDay(h)
	r := imported(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1250, customerKID, "1")))
	if got := stateOf(t, h, lineID(t, h, r.File.ID, "1")); got != "matched -" {
		t.Errorf("the line: %s, want matched to the older invoice", got)
	}
}

// An accepted claim no debit paid within five business days of its due date
// is unpaid, and no longer holds the settings.
func TestEfaktura_AnAcceptedClaimBecomesUnpaid(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	customerKID, _ := kid.CustomerKID(10002, 10, kid.Mod10)
	inv := efakturaInvoice(t, h, customerPerson)
	claim := plantClaim(t, h, inv.ID, customerPerson, customerKID, "2026-09-01", "1250.00")
	if res := manager(t, h).Do(http.MethodDelete, consumerProviderPath, nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE with a past-due accepted claim = %d %s", res.Status, res.Body)
	}
	if err := efakturaWorker(t, h).MarkUnpaid(context.Background()); err != nil {
		t.Fatalf("MarkUnpaid: %v", err)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT status FROM invoices.avtalegiro_claims WHERE id = $1`, claim); got != "unpaid" {
		t.Errorf("the claim = %s, want unpaid", got)
	}
	if n := len(eventPayloads(t, h, "invoices.avtalegiro.claim_changed")); n != 1 {
		t.Errorf("%d claim events, want one", n)
	}
}

// A step that fails puts its row back with the back-off, and the cycle goes
// on: the other rows, the cancellations and the housekeeping still run.
func TestEfakturaWorker_AFailingStepDoesNotStopTheCycle(t *testing.T) {
	t.Parallel()
	h := efakturaReady(t)
	inv := efakturaInvoice(t, h, customerPerson)
	// A transmission sent as both whose claim is missing: its shipment
	// cannot be built.
	h.Exec(t, `INSERT INTO invoices.transmissions (invoice_id, channel, provider, idempotency_key, pdf_sha256, pdf_object_key,
		efaktura_identifier, shipment_id, payment_type, next_attempt_at, queued_at, created_by_user_id)
		SELECT $1, 'efaktura', 'mps', gen_random_uuid(), pdf_sha256, pdf_object_key, '123456789', gen_random_uuid(), 'both', $2, $2, gen_random_uuid()
		FROM invoices.invoices WHERE id = $1`, inv.ID, h.Now())
	if err := efakturaWorker(t, h).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v, want the failing row handled", err)
	}
	if reason := modtest.One[string](t, h.Harness, `SELECT coalesce(last_error, '') FROM invoices.transmissions WHERE invoice_id = $1`, inv.ID); reason == "" {
		t.Error("the failing row has no reason")
	}
	if due := modtest.One[bool](t, h.Harness, `SELECT next_attempt_at > $2 FROM invoices.transmissions WHERE invoice_id = $1`, inv.ID, h.Now()); !due {
		t.Error("the failing row was not put back for later")
	}
	if done := modtest.One[bool](t, h.Harness, `SELECT housekeeping_at IS NOT NULL FROM invoices.consumer_provider_credentials`); !done {
		t.Error("the housekeeping did not run")
	}
}
