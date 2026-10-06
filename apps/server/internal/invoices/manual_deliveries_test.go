package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
)

// Manual deliveries and the delivery fact (invoices payments and reminders
// design D8): an invoice not validly delivered does not fall due, so a
// charge needs a delivery on or before the due date — an e-mail, a delivered
// EHF transmission, or one recorded by hand.

func manualDeliveriesPath(id int64) string {
	return fmt.Sprintf("%s/%d/manual-deliveries", invoicesPath, id)
}

func manualDeliveryRemovalPath(id, deliveryID int64) string {
	return fmt.Sprintf("%s/%d/manual-deliveries/%d/remove", invoicesPath, id, deliveryID)
}

// delivered is a manual delivery's body.
func delivered(kind, on string) map[string]any {
	return map[string]any{"kind": kind, "deliveredOn": on}
}

// plantEmail plants an e-mail that handed invoice id over at sentAt.
func plantEmail(t *testing.T, h *harness, id int64, sentAt time.Time) {
	t.Helper()
	h.Exec(t, `INSERT INTO invoices.deliveries (invoice_id, recipient, subject, message_id, pdf_sha256, sent_at, sent_by_user_id)
		VALUES ($1, 'faktura@acme.no', 'Faktura', '<m@vantigo.invalid>', repeat('a', 64), $2, gen_random_uuid())`, id, sentAt)
}

// plantEhf plants an EHF transmission of invoice id in status,
// delivered (or failed) at at.
func plantEhf(t *testing.T, h *harness, id int64, status string, at time.Time) {
	t.Helper()
	h.Exec(t, `INSERT INTO invoices.transmissions (invoice_id, provider, idempotency_key, sender_participant, receiver_participant,
		    document_type, process_id, ubl_object_key, ubl_sha256, pdf_sha256, status, next_attempt_at, lookup_registered,
		    lookup_can_receive, lookup_at, queued_at, submitted_at, delivered_at, failed_at, created_by_user_id)
		VALUES ($1, 'storecove', gen_random_uuid(), '0192:974760673', '0192:923609016', 'invoice', 'billing', 'ubl/x.xml',
		    repeat('a', 64), repeat('b', 64), $2::text, $3, true, true, $3, $3, $3,
		    CASE WHEN $2::text = 'delivered' THEN $3::timestamptz END, CASE WHEN $2::text = 'failed' THEN $3::timestamptz END, gen_random_uuid())`,
		id, status, at)
}

// A manual delivery records the invoice handed over or posted (D8): either
// kind, on any day from the issue date to today (Oslo), with a trimmed note,
// who and when; the fields are 400s naming them; no document a bare 404, a
// draft invoice_draft, a credit note — draft or issued —
// credit_note_no_reminders; and it needs invoices:issue.
func TestManualDeliveries_RecordAndRefusals(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h) // issued 2026-09-12
	h.Advance(72 * time.Hour)
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")

	for _, bad := range []struct {
		name, field string
		body        map[string]any
	}{
		{"an unknown kind", "kind", delivered("faxed", "2026-09-13")},
		{"no day", "deliveredOn", map[string]any{"kind": "posted"}},
		{"before the issue date", "deliveredOn", delivered("posted", "2026-09-11")},
		{"after today", "deliveredOn", delivered("posted", "2026-09-16")},
		{"a note over 500", "note", map[string]any{"kind": "posted", "deliveredOn": "2026-09-13", "note": strings.Repeat("n", 501)}},
	} {
		badOn(t, bad.name, c.Do(http.MethodPost, manualDeliveriesPath(inv.ID), bad.body), bad.field)
	}
	bareNotFound(t, "no document", c.Do(http.MethodPost, manualDeliveriesPath(999999), delivered("posted", "2026-09-13")))
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	conflictAs(t, "a draft", c.Do(http.MethodPost, manualDeliveriesPath(draft.ID), delivered("posted", "2026-09-13")), "invoice_draft")
	original := issuedOriginal(t, h)
	conflictAs(t, "a credit-note draft", c.Do(http.MethodPost, manualDeliveriesPath(creditDraft(t, h, original).ID), delivered("posted", "2026-09-15")),
		"credit_note_no_reminders")
	conflictAs(t, "an issued credit note", c.Do(http.MethodPost, manualDeliveriesPath(issued(t, h, creditDraft(t, h, original).ID).ID),
		delivered("posted", "2026-09-15")), "credit_note_no_reminders")
	if res := h.SignIn(t, "invoices:access", "invoices:payments").Do(http.MethodPost, manualDeliveriesPath(inv.ID), delivered("posted", "2026-09-13")); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:issue = %d, want 403", res.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.manual_deliveries`); n != 0 {
		t.Fatalf("manual deliveries = %d after the refusals, want none", n)
	}

	okAs(t, "posted today", c.Do(http.MethodPost, manualDeliveriesPath(inv.ID), delivered("posted", "2026-09-15")))
	got := okAs(t, "handed over on the issue date", c.Do(http.MethodPost, manualDeliveriesPath(inv.ID), map[string]any{
		"kind": "handed_over", "deliveredOn": "2026-09-12", "note": "  Levert i resepsjonen  ",
	}))
	if got.ManualDeliveries == nil || len(*got.ManualDeliveries) != 2 {
		t.Fatalf("manualDeliveries = %v, want both", got.ManualDeliveries)
	}
	ds := *got.ManualDeliveries
	if ds[0].Kind != "handed_over" || ds[0].DeliveredOn != "2026-09-12" || ds[0].Note != "Levert i resepsjonen" ||
		ds[0].RecordedByUserID != userID.String() || ds[0].RemovedAt != nil || ds[1].Kind != "posted" || ds[1].DeliveredOn != "2026-09-15" {
		t.Errorf("manualDeliveries = %+v, want handed_over on the 12th with the trimmed note by %s, then posted on the 15th", ds, userID)
	}
	if at, err := time.Parse(time.RFC3339Nano, ds[0].RecordedAt); err != nil || !at.Equal(h.Now()) {
		t.Errorf("recordedAt = %s (%v), want the clock's %s", ds[0].RecordedAt, err, h.Now())
	}
	if got.State != "open" || money(got.OpenAmount) != 1000.0 {
		t.Errorf("after the deliveries = %s open %v, want the principal untouched", got.State, money(got.OpenAmount))
	}
}

// issuedOriginal is an issued invoice's id, an original for a credit note.
func issuedOriginal(t *testing.T, h *harness) int64 {
	t.Helper()
	return issuedFor(t, h, customerAcme, line("Konsulenttime", 10, 80, vat25)).ID
}

// A manual delivery is removed with a reason, once (D8): 404 for one that is
// not the document's; delivery_removed the second time; delivery_relied_on
// while a sent letter carries a charge not waived and no other delivery on
// or before the due date would remain (plan reading 37) — allowed once the
// fee is waived claimed_in_error, and once an e-mail on the due date
// remains, an e-mail after it counting for nothing.
func TestManualDeliveries_Removal(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")
	payer := chargePayer(t, h)
	record := func(id int64) int64 {
		t.Helper()
		got := okAs(t, "a manual delivery", c.Do(http.MethodPost, manualDeliveriesPath(id), delivered("handed_over", "2026-09-12")))
		return (*got.ManualDeliveries)[len(*got.ManualDeliveries)-1].ID
	}
	remove := func(id, deliveryID int64) receivablesJSON {
		t.Helper()
		return okAs(t, "the removal", c.Do(http.MethodPost, manualDeliveryRemovalPath(id, deliveryID), map[string]any{"reason": "  Feil faktura  "}))
	}

	// Waived: the record is relied on until the fee is waived.
	inv := thousand(t, h)
	other := thousand(t, h)
	deliveryID := record(inv.ID)
	othersID := record(other.ID)
	for _, reason := range []string{"", "  ", strings.Repeat("x", 201)} {
		badOn(t, fmt.Sprintf("reason %q", reason), c.Do(http.MethodPost, manualDeliveryRemovalPath(999999, deliveryID), map[string]any{"reason": reason}), "reason")
	}
	for _, path := range []string{manualDeliveryRemovalPath(999999, deliveryID), manualDeliveryRemovalPath(inv.ID, othersID),
		manualDeliveryRemovalPath(inv.ID, 999999)} {
		bareNotFound(t, "POST "+path, c.Do(http.MethodPost, path, map[string]any{"reason": "Feil"}))
	}
	fee := plantSent(t, h, inv.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "0"})
	conflictAs(t, "a sent fee relies on it", c.Do(http.MethodPost, manualDeliveryRemovalPath(inv.ID, deliveryID), map[string]any{"reason": "Feil"}),
		"delivery_relied_on")
	okAs(t, "the fee waived", payer.Do(http.MethodPost, waivePath(inv.ID), waive("claimed_in_error", waiver(fee, "fee"))))
	got := remove(inv.ID, deliveryID)
	if got.ManualDeliveries == nil || len(*got.ManualDeliveries) != 1 {
		t.Fatalf("manualDeliveries = %v, want the removed one kept", got.ManualDeliveries)
	}
	d := (*got.ManualDeliveries)[0]
	if d.RemovedAt == nil || d.RemovedByUserID == nil || *d.RemovedByUserID != userID.String() || d.RemovalReason == nil || *d.RemovalReason != "Feil faktura" {
		t.Errorf("the removed delivery = %+v, want removed by %s with the trimmed reason", d, userID)
	}
	conflictAs(t, "removed again", c.Do(http.MethodPost, manualDeliveryRemovalPath(inv.ID, deliveryID), map[string]any{"reason": "Igjen"}), "delivery_removed")

	// Another delivery: an e-mail after the due date counts for nothing, one
	// on the due date (23:30 in Oslo) lets the record go.
	emailed := thousand(t, h)
	due, err := time.Parse(time.DateOnly, *receivablesOf(t, h, emailed.ID).DueDate)
	if err != nil {
		t.Fatalf("the due date: %v", err)
	}
	emailedID := record(emailed.ID)
	plantSent(t, h, emailed.ID, sentFacts{1, "2026-10-01", "none", "", "", "3.10"})
	plantEmail(t, h, emailed.ID, due.Add(22*time.Hour+30*time.Minute)) // 00:30 the day after, in Oslo
	conflictAs(t, "interest, and an e-mail after the due date", c.Do(http.MethodPost, manualDeliveryRemovalPath(emailed.ID, emailedID),
		map[string]any{"reason": "Feil"}), "delivery_relied_on")
	plantEmail(t, h, emailed.ID, due.Add(21*time.Hour+30*time.Minute)) // 23:30 on the due date, in Oslo
	remove(emailed.ID, emailedID)

	// Nothing sent: nothing relies on it.
	plain := thousand(t, h)
	remove(plain.ID, record(plain.ID))
}

// The engine's delivery fact (D8; plan reading 34): deliveriesOf answers the
// Oslo day of each live delivery — an e-mail by its sent_at's, across
// midnight; an EHF transmission delivered by its delivered_at's, a failed
// one not at all; a manual one by its delivered_on, a removed one not at
// all — and none of another invoice's.
func TestDeliveries_TheEnginesFact(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	other := thousand(t, h)
	ctx := context.Background()
	day := func(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }
	daysOf := func(id int64) []time.Time {
		t.Helper()
		days, err := invoices.DeliveriesOf(ctx, h.Pool(), id)
		if err != nil {
			t.Fatalf("deliveriesOf(%d): %v", id, err)
		}
		return days
	}
	if got := daysOf(inv.ID); len(got) != 0 {
		t.Errorf("an invoice never delivered = %v, want none", got)
	}

	plantEmail(t, h, inv.ID, time.Date(2026, 9, 12, 21, 30, 0, 0, time.UTC)) // 23:30 in Oslo: the 12th
	plantEmail(t, h, inv.ID, time.Date(2026, 9, 12, 22, 30, 0, 0, time.UTC)) // 00:30 in Oslo: the 13th
	plantEhf(t, h, inv.ID, "failed", time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC))
	plantEhf(t, h, inv.ID, "delivered", time.Date(2026, 9, 14, 23, 10, 0, 0, time.UTC)) // the 15th in Oslo
	plantEmail(t, h, other.ID, time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC))
	h.Advance(96 * time.Hour)
	c := h.SignIn(t, "invoices:access", "invoices:issue")
	okAs(t, "handed over", c.Do(http.MethodPost, manualDeliveriesPath(inv.ID), delivered("handed_over", "2026-09-14")))
	got := okAs(t, "posted", c.Do(http.MethodPost, manualDeliveriesPath(inv.ID), delivered("posted", "2026-09-16")))
	var posted int64
	for _, d := range *got.ManualDeliveries {
		if d.Kind == "posted" {
			posted = d.ID
		}
	}
	okAs(t, "the posted one removed", c.Do(http.MethodPost, manualDeliveryRemovalPath(inv.ID, posted), map[string]any{"reason": "Aldri sendt"}))

	want := []time.Time{day(12), day(13), day(14), day(15)}
	if got := daysOf(inv.ID); !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("deliveriesOf = %v, want %v: the e-mails' Oslo days, the delivered EHF's, the live manual one's", got, want)
	}
	for _, d := range daysOf(inv.ID) {
		if d.Location() != time.UTC || d.Hour() != 0 || d.Minute() != 0 {
			t.Errorf("a delivery day %v is not a UTC midnight", d)
		}
	}
	if got := daysOf(other.ID); !slices.EqualFunc(got, []time.Time{day(12)}, time.Time.Equal) {
		t.Errorf("the other invoice's = %v, want its own e-mail only", got)
	}
}
