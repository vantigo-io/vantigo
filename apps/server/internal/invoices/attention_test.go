package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

// The dashboard's attention items (invoices payments and reminders design
// D12; plan reading 29): the overdue invoices and the refunds due for every
// caller, the open bank lines, the failed and held letters and the unposted
// print batches for invoices:payments, each until what it is about is done.

const attentionPath = invoicesPath + "/stats/attention"

// attentionItemJSON is one attention item as a client reads it.
type attentionItemJSON struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	OccurredAt string `json:"occurredAt"`
	EntityID   string `json:"entityId"`
	Count      *int32 `json:"count"`
}

// attentionOf reads the attention items as c, failing unless 200.
func attentionOf(t *testing.T, c *modtest.Client) []attentionItemJSON {
	t.Helper()
	res := c.Do(http.MethodGet, attentionPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", attentionPath, res.Status, res.Body)
	}
	var items []attentionItemJSON
	res.JSON(&items)
	return items
}

// ofType is the items of type typ, in the answer's order.
func ofType(items []attentionItemJSON, typ string) []attentionItemJSON {
	var out []attentionItemJSON
	for _, i := range items {
		if i.Type == typ {
			out = append(out, i)
		}
	}
	return out
}

// typesOf is the distinct types of items, in the answer's order.
func typesOf(items []attentionItemJSON) []string {
	var out []string
	for _, i := range items {
		if !slices.Contains(out, i.Type) {
			out = append(out, i.Type)
		}
	}
	return out
}

// entitiesOf is the entity ids of items.
func entitiesOf(items []attentionItemJSON) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.EntityID)
	}
	return out
}

// entity is an id as an item's entity id.
func entity(id int64) string { return strconv.FormatInt(id, 10) }

// itemCount is an item's count, -1 when it has none.
func itemCount(i attentionItemJSON) int32 {
	if i.Count == nil {
		return -1
	}
	return *i.Count
}

// plantCreditNoteAt plants an issued credit note of invoice id of gross,
// issued at at, and answers its id.
func plantCreditNoteAt(t *testing.T, h *harness, id int64, number int64, gross string, at time.Time) int64 {
	t.Helper()
	return plantID(t, h, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, credits_invoice_id, issue_date, exchange_rate_date,
		    seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		SELECT 'credit_note', 'issued', $2, customer_id, id, $4::timestamptz::date, $4::timestamptz::date, 'Selger AS', buyer_name,
		    $3::numeric, $4::timestamptz, gen_random_uuid(), $4::timestamptz, $4::timestamptz
		FROM invoices.invoices WHERE id = $1
		RETURNING id`, id, number, gross, at)
}

// plantBatch plants a print batch to be posted on postOn with one printed
// letter of invoice id, its sequence sequence, and answers the batch.
func plantBatch(t *testing.T, h *harness, id int64, sequence int, postOn string) int64 {
	t.Helper()
	batch := plantID(t, h, `INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
		VALUES ($1::date, now(), gen_random_uuid()) RETURNING id`, postOn)
	plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, print_batch_id, sequence, level, channel, language, created_at,
		    created_by_user_id, status, sent_on, deadline, regime, principal_open, credited, fee_kind, charges_earlier,
		    interest, interest_waived, interest_paid, total)
		VALUES ($1, $2, $3, $4, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'printed', $5::date, $5::date + 14,
		    'inkassolov_1988', 1000, 0, 'none', 0, 0, 0, 0, 1000)
		RETURNING id`, id, plantRun(t, h), batch, sequence, postOn)
	return batch
}

// TestAttention_PerPermission: a reader holding invoices:access alone is
// answered the overdue invoices and the refunds due, and nothing of the
// queue, the letters or the batches — a 200, never a 403; a holder of
// invoices:payments is answered every type; a caller without invoices:access
// is refused by the router.
func TestAttention_PerPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	overdue := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	refund := plantOverdue(t, h, overdueSpec{number: 2, customer: customerAcme, issue: "2026-09-01", due: "2026-10-01"})
	plantPayment(t, h, refund, "1000", "2026-09-05")
	plantCreditNoteAt(t, h, refund, 3, "1000", h.Now())
	plantBankFile(t, h, "2026-09-10")
	h.Exec(t, `INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, amount,
		    currency, fingerprint, ordinal, status, reason)
		SELECT max(id), '1/1', 'camt054', '15032080119', 'credit', DATE '2026-09-10', 100, 'NOK', md5('open'), 1, 'exception', 'no_kid'
		FROM invoices.bank_files`)
	plantFailed(t, h, overdue)
	h.Exec(t, `UPDATE invoices.reminders SET held_reason = 'collection_rates_outdated' WHERE id = $1`, plantQueued(t, h, overdue, 2))
	plantBatch(t, h, overdue, 3, "2026-09-08")

	all := []string{"invoiceOverdue", "invoiceRefundDue", "bankTransactionsOpen", "reminderFailed", "remindersHeld", "reminderBatchUnposted"}
	if got := typesOf(attentionOf(t, payer(t, h))); !slices.Equal(got, all) {
		t.Errorf("invoices:payments is answered %v, want %v", got, all)
	}
	if got := typesOf(attentionOf(t, reader(t, h))); !slices.Equal(got, all[:2]) {
		t.Errorf("invoices:access alone is answered %v, want %v and nothing more", got, all[:2])
	}
	if res := h.SignIn(t, "invoices:create").Do(http.MethodGet, attentionPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:access = %d %s, want 403", res.Status, res.Body)
	}
}

// TestAttention_EachTypeAndItsClearing: each type, the moment it appears
// and what clears it.
func TestAttention_EachTypeAndItsClearing(t *testing.T) {
	t.Parallel()

	// The 20 most overdue, oldest due date first, each the day after its E:
	// 1 August 2026 is a Saturday, so E is Monday the 3rd and the item is
	// from the 4th. A payment clears one, a credit note another, and the
	// next oldest comes in.
	t.Run("overdue", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		ids := make([]int64, 0, 21)
		for n := range 21 {
			due := time.Date(2026, 8, 1+n, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
			ids = append(ids, plantOverdue(t, h, overdueSpec{number: int64(n + 1), customer: customerAcme, issue: "2026-07-01", due: due}))
		}
		want := func(from, to int) []string {
			out := []string{}
			for _, id := range ids[from:to] {
				out = append(out, entity(id))
			}
			return out
		}
		got := ofType(attentionOf(t, reader(t, h)), "invoiceOverdue")
		if !slices.Equal(entitiesOf(got), want(0, 20)) {
			t.Fatalf("the overdue items = %v, want the 20 oldest due %v", entitiesOf(got), want(0, 20))
		}
		first := got[0]
		if first.ID != "invoiceOverdue/"+entity(ids[0]) || first.Title != "Kunde 1" || first.OccurredAt != "2026-08-04T00:00:00Z" ||
			first.Count != nil {
			t.Errorf("the oldest = %+v, want its id, the buyer's name, 4 August (the day after E, Monday 3) and no count", first)
		}
		if got[2].OccurredAt != "2026-08-04T00:00:00Z" || got[3].OccurredAt != "2026-08-05T00:00:00Z" {
			t.Errorf("due Monday 3 and Tuesday 4 = %s and %s, want the 4th and the 5th", got[2].OccurredAt, got[3].OccurredAt)
		}
		plantPayment(t, h, ids[0], "1000", "2026-09-10")
		plantCreditNoteAt(t, h, ids[1], 100, "1000", h.Now())
		if got := entitiesOf(ofType(attentionOf(t, reader(t, h)), "invoiceOverdue")); !slices.Equal(got, want(2, 21)) {
			t.Errorf("after a payment and a credit note = %v, want %v", got, want(2, 21))
		}
	})

	// An invoice due on a Saturday is overdue from the Sunday, but E is the
	// Monday: no item until the Tuesday.
	t.Run("overdue after E", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		id := plantOverdue(t, h, overdueSpec{number: 1, customer: customerAcme, issue: "2026-09-01", due: "2026-09-19"})
		for _, day := range []int{20, 21} {
			moveClockTo(t, h, time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC))
			if got := ofType(attentionOf(t, reader(t, h)), "invoiceOverdue"); len(got) != 0 {
				t.Errorf("on 2026-09-%d = %+v, want none before the day after E", day, got)
			}
		}
		moveClockTo(t, h, time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC))
		if got := ofType(attentionOf(t, reader(t, h)), "invoiceOverdue"); len(got) != 1 || got[0].EntityID != entity(id) ||
			got[0].OccurredAt != "2026-09-22T00:00:00Z" {
			t.Errorf("on Tuesday 22 = %+v, want the invoice from that day", got)
		}
	})

	// A credit note after a payment leaves the invoice owing the buyer: an
	// item from the credit note's issue, cleared when the payment is removed
	// (the money paid back outside Vantigo). A fee paid and then waived is a
	// refund of charges: an item from the waiver, cleared when the charge
	// payment is removed.
	t.Run("refund due", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		c := reader(t, h)
		credited := plantOverdue(t, h, overdueSpec{number: 1, customer: customerAcme, issue: "2026-09-01", due: "2026-10-01"})
		payment := registered(t, h, credited, pay(1000, "2026-09-05")).Payments[0].ID
		if got := ofType(attentionOf(t, c), "invoiceRefundDue"); len(got) != 0 {
			t.Fatalf("a paid invoice = %+v, want no refund due", got)
		}
		h.Advance(time.Hour)
		plantCreditNoteAt(t, h, credited, 2, "1000", h.Now())
		got := ofType(attentionOf(t, c), "invoiceRefundDue")
		if len(got) != 1 || got[0].ID != "invoiceRefundDue/"+entity(credited) || got[0].EntityID != entity(credited) ||
			got[0].Title != "Kunde 1" || got[0].OccurredAt != h.Now().Format(time.RFC3339) {
			t.Errorf("a credit note after the payment = %+v, want the invoice from the credit note's issue", got)
		}
		if res := payer(t, h).Do(http.MethodPost, removalPath(credited, payment), map[string]any{"reason": "Betalt tilbake"}); res.Status != http.StatusOK {
			t.Fatalf("remove the payment = %d %s", res.Status, res.Body)
		}
		if got := ofType(attentionOf(t, c), "invoiceRefundDue"); len(got) != 0 {
			t.Errorf("the payment removed = %+v, want none", got)
		}

		fee := plantOverdue(t, h, overdueSpec{number: 3, customer: customerAcme, issue: "2026-07-01", due: "2026-08-03"})
		letter := plantSent(t, h, fee, sentFacts{sequence: 1, sentOn: "2026-08-20", feeKind: "reminder_fee", fee: "35", interest: "0"})
		paidCharge := okAs(t, "the fee paid", chargePayer(t, h).Do(http.MethodPost, chargePaymentsPath(fee),
			map[string]any{"amount": 35, "paidOn": "2026-09-01"}))
		charge := (*paidCharge.ChargePayments)[0].ID
		if got := ofType(attentionOf(t, c), "invoiceRefundDue"); len(got) != 0 {
			t.Fatalf("a fee paid = %+v, want no refund due", got)
		}
		h.Advance(time.Hour)
		if res := chargePayer(t, h).Do(http.MethodPost, waivePath(fee), waive("goodwill", waiver(letter, "fee"))); res.Status != http.StatusOK {
			t.Fatalf("waive the fee = %d %s", res.Status, res.Body)
		}
		got = ofType(attentionOf(t, c), "invoiceRefundDue")
		if len(got) != 1 || got[0].EntityID != entity(fee) || got[0].OccurredAt != h.Now().Format(time.RFC3339) {
			t.Errorf("the paid fee waived = %+v, want the invoice from the waiver", got)
		}
		if res := chargePayer(t, h).Do(http.MethodPost, chargePaymentRemovalPath(fee, charge),
			map[string]any{"reason": "Betalt tilbake"}); res.Status != http.StatusOK {
			t.Fatalf("remove the charge payment = %d %s", res.Status, res.Body)
		}
		if got := ofType(attentionOf(t, c), "invoiceRefundDue"); len(got) != 0 {
			t.Errorf("the charge payment removed = %+v, want none", got)
		}
	})

	// A file with lines open — an exception, a line matching left pending, a
	// duplicate —
	// is one item counted in lines, the queue's own count, until each is
	// resolved or matched; a file whose lines all matched is none.
	t.Run("bank file", func(t *testing.T) {
		t.Parallel()
		h, _ := matchHarness(t)
		paid, later := kidInvoice(t, h), kidInvoice(t, h)
		toMatchDay(h)
		c := importer(t, h)
		matched, _ := camtLines(t, h, c, "ATT-1", kidEntry(5, 1000, *paid.Kid, "", "PAID"))
		r, ids := camtLines(t, h, c, "ATT-2", noKidEntry(5, 300, "Ukjent", "NOKID"))
		h.Exec(t, `INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, amount,
			    currency, kid, fingerprint, ordinal)
			VALUES ($1, 'late', 'camt054', $2, 'credit', DATE '2026-10-06', 1000, 'NOK', $3, md5('pending'), 1)`,
			r.File.ID, sellerAccount, *later.Kid)
		duplicate := plantID(t, h, `INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction,
			    booked_on, amount, currency, fingerprint, ordinal, status, duplicate_of_id)
			VALUES ($1, 'twice', 'camt054', $2, 'credit', DATE '2026-10-05', 300, 'NOK', md5('twice'), 2, 'duplicate', $3)
			RETURNING id`, r.File.ID, sellerAccount, ids["NOKID"])
		got := ofType(attentionOf(t, c), "bankTransactionsOpen")
		if len(got) != 1 || got[0].ID != "bankTransactionsOpen/"+entity(r.File.ID) || got[0].EntityID != entity(r.File.ID) ||
			itemCount(got[0]) != 3 || got[0].Title != "2026-10-05" {
			t.Fatalf("the open lines = %+v, want file %d with 3 lines and its booking day, not the matched file %d", got, r.File.ID, matched.File.ID)
		}
		counted := 0
		for _, status := range []string{"exception", "pending", "duplicate"} {
			_, n := queueList(t, h, fmt.Sprintf("bankFileId=%d&status=%s", r.File.ID, status))
			counted += n
		}
		if counted != 3 {
			t.Errorf("the queue counts %d open lines, want the item's 3", counted)
		}
		if res := c.Do(http.MethodPost, matchPath(r.File.ID), nil); res.Status != http.StatusOK {
			t.Fatalf("match = %d %s", res.Status, res.Body)
		}
		if got := ofType(attentionOf(t, c), "bankTransactionsOpen"); len(got) != 1 || itemCount(got[0]) != 2 {
			t.Errorf("the pending line matched = %+v, want the exception and the duplicate left", got)
		}
		acted(t, c, ids["NOKID"], "dismiss", map[string]any{"note": "Ikke en kundebetaling"})
		if got := ofType(attentionOf(t, c), "bankTransactionsOpen"); len(got) != 1 || itemCount(got[0]) != 1 {
			t.Errorf("the exception dismissed = %+v, want the duplicate left", got)
		}
		acted(t, c, duplicate, "confirm-duplicate", map[string]any{"note": "Samme linje"})
		if got := ofType(attentionOf(t, c), "bankTransactionsOpen"); len(got) != 0 {
			t.Errorf("every line resolved = %+v, want none", got)
		}
	})

	// A failed letter is one item about its invoice until it is retried.
	t.Run("failed letter", func(t *testing.T) {
		t.Parallel()
		h, _ := workerHarness(t, "")
		id := plantOverdue(t, h, overdueSpec{number: 7, customer: customerAcme, issue: "2026-07-01", due: "2026-08-03"})
		letter := plantFailed(t, h, id)
		c := payer(t, h)
		got := ofType(attentionOf(t, c), "reminderFailed")
		if len(got) != 1 || got[0].ID != fmt.Sprintf("reminderFailed/%d", letter) || got[0].EntityID != entity(id) ||
			got[0].Title != "Kunde 7" || got[0].OccurredAt != h.Now().Format(time.RFC3339) {
			t.Fatalf("the failed letter = %+v, want letter %d of invoice %d at its failure", got, letter, id)
		}
		if res := c.Do(http.MethodPost, letterPath(letter, "retry"), nil); res.Status != http.StatusOK {
			t.Fatalf("retry = %d %s", res.Status, res.Body)
		}
		if got := ofType(attentionOf(t, c), "reminderFailed"); len(got) != 0 {
			t.Errorf("after the retry = %+v, want none", got)
		}
	})

	// Letters waiting on their rates or the review are one item per cause,
	// counted, while they wait; mended, the worker sends them and it clears.
	for _, c := range []struct {
		name, set, issue, due, cause, fix string
	}{
		{"held unreviewed", "regime_reviewed_through = DATE '2026-09-01'", "2026-07-01", "2026-08-03", "collectionRegimeUnreviewed",
			"regime_reviewed_through = DATE '2026-12-31'"},
		{"held outdated", "late_interest = true", "2023-10-02", "2023-11-01", "collectionRatesOutdated", "late_interest = false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h, _ := workerHarness(t, c.set)
			w := invoices.NewReminderWorker(h.Deps())
			for n := range 2 {
				id := plantOverdue(t, h, overdueSpec{number: int64(n + 1), customer: customerAcme, issue: c.issue, due: c.due, deliveredOnIssueOn: true})
				queueLetter(t, h, id, 1, "reminder", false, "nb")
				if !dispatch(t, w) {
					t.Fatal("the worker claimed nothing")
				}
			}
			p := payer(t, h)
			got := ofType(attentionOf(t, p), "remindersHeld")
			if len(got) != 1 || got[0].ID != "remindersHeld/"+c.cause || got[0].EntityID != c.cause || got[0].Title != c.cause ||
				itemCount(got[0]) != 2 {
				t.Fatalf("the held letters = %+v, want one item for %s counting both", got, c.cause)
			}
			h.Exec(t, `UPDATE invoices.reminder_settings SET `+c.fix)
			h.Advance(time.Hour)
			for range 2 {
				if !dispatch(t, w) {
					t.Fatal("the worker claimed nothing once the cause was mended")
				}
			}
			if got := ofType(attentionOf(t, p), "remindersHeld"); len(got) != 0 {
				t.Errorf("both sent = %+v, want none", got)
			}
		})
	}

	// A batch to be posted on the 11th is an item from the 13th, counting
	// its printed letters, until it is confirmed posted or reprinted.
	t.Run("unposted batch", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		id := plantOverdue(t, h, overdueSpec{number: 1, customer: customerAcme, issue: "2026-07-01", due: "2026-08-03"})
		posted, reprinted := plantBatch(t, h, id, 1, "2026-09-11"), plantBatch(t, h, id, 2, "2026-09-11")
		if got := ofType(attentionOf(t, payer(t, h)), "reminderBatchUnposted"); len(got) != 0 {
			t.Fatalf("on the 12th = %+v, want none before post_on + 2", got)
		}
		moveClockTo(t, h, time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC))
		c := payer(t, h)
		got := ofType(attentionOf(t, c), "reminderBatchUnposted")
		if !slices.Equal(entitiesOf(got), []string{entity(posted), entity(reprinted)}) || got[0].ID != "reminderBatchUnposted/"+entity(posted) ||
			got[0].Title != "2026-09-11" || got[0].OccurredAt != "2026-09-13T00:00:00Z" || itemCount(got[0]) != 1 {
			t.Fatalf("on the 13th = %+v, want both batches from that day, each counting its letter", got)
		}
		h.Exec(t, `UPDATE invoices.reminder_print_batches SET posted_on = post_on, posted_at = now(), posted_by_user_id = gen_random_uuid()
			WHERE id = $1`, posted)
		h.Exec(t, `UPDATE invoices.reminder_print_batches SET reprinted_at = now() WHERE id = $1`, reprinted)
		if got := ofType(attentionOf(t, c), "reminderBatchUnposted"); len(got) != 0 {
			t.Errorf("posted and reprinted = %+v, want none", got)
		}
	})
}

// plantFailedCapture writes a capture_failed attempt of invoice id that
// finished at at.
func plantFailedCapture(t *testing.T, h *harness, id int64, at time.Time) int64 {
	t.Helper()
	return plantID(t, h, `
		INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount, cancel_key, state, created_at, expires_at,
		    authorized_amount, authorized_at, capture_key, capture_attempts, finished_at, cancelled_amount)
		VALUES ($1, 'vipps', '123456', 'on_site', 2500, gen_random_uuid(), 'capture_failed', $2::timestamptz,
		    $2::timestamptz + interval '10 minutes', 2500, $2::timestamptz, gen_random_uuid(), 1, $2::timestamptz, 2500)
		RETURNING id`, id, at)
}

// attemptIDOf is the id of the attempt under reference.
func attemptIDOf(t *testing.T, h *harness, reference string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.payment_attempts WHERE reference = $1`, reference)
}

// itemIDs is the ids of items.
func itemIDs(items []attentionItemJSON) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.ID)
	}
	return out
}

// TestAttention_PaymentTypes (D12's 4C types, D16, plan reading 18): each
// Vipps type for invoices:payments alone, its id, title, occurredAt and its
// clearing. paymentCaptureUnknown: a capture_unknown attempt, and money
// captured whose registration failed; paymentCaptureFailed: a failed capture
// of the last 14 days on an invoice still open; paymentReservationStranded:
// a reservation the worker cannot carry — the switch off, another sales
// unit — and an attempt parked for a person; vippsRefundSeen: the refund
// watch's (TestRefundWatch_RaisesAndClears). invoices:access alone is
// answered none of them, never a 403.
func TestAttention_PaymentTypes(t *testing.T) {
	t.Parallel()
	vippsTypes := []string{"paymentCaptureUnknown", "paymentCaptureFailed", "paymentReservationStranded", "vippsRefundSeen"}

	t.Run("paymentCaptureUnknown", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv := issuedAcme(t, h)
		ref := plantReservation(t, h, inv.ID, "capture_unknown", "2500")
		id := attemptIDOf(t, h, ref)
		items := ofType(attentionOf(t, payer(t, h)), "paymentCaptureUnknown")
		if len(items) != 1 || items[0].ID != fmt.Sprintf("paymentCaptureUnknown/%d", id) || items[0].EntityID != entity(inv.ID) ||
			items[0].Title != "Acme AS Norge" || items[0].OccurredAt != h.Now().UTC().Format(time.RFC3339) {
			t.Errorf("the items = %+v, want one: the attempt's, about the invoice, the buyer, from its authorization", items)
		}
		for _, typ := range vippsTypes {
			if got := ofType(attentionOf(t, reader(t, h)), typ); len(got) != 0 {
				t.Errorf("invoices:access alone is answered %v", got)
			}
		}
		setAttempt(t, h, ref, "state = 'abandoned', reserved_amount = NULL, abandoned_by_user_id = gen_random_uuid(), abandoned_at = now(), abandon_reason = 'x'")
		if got := ofType(attentionOf(t, payer(t, h)), "paymentCaptureUnknown"); len(got) != 0 {
			t.Errorf("after the abandon = %v, want none", got)
		}
	})

	t.Run("a failed registration", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		h.Exec(t, `CREATE FUNCTION invoices.test_refuse_vipps() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'test: registration refused'; END $$`)
		h.Exec(t, `CREATE TRIGGER tr_test_refuse_vipps BEFORE INSERT ON invoices.payments FOR EACH ROW
			WHEN (NEW.source = 'vipps') EXECUTE FUNCTION invoices.test_refuse_vipps()`)
		_ = invoices.NewPaymentWorker(h.Deps()).RunCycle(context.Background())
		items := ofType(attentionOf(t, payer(t, h)), "paymentCaptureUnknown")
		if len(items) != 1 || items[0].ID != fmt.Sprintf("paymentCaptureUnknown/%d", attemptIDOf(t, h, ref)) || items[0].EntityID != entity(inv.ID) {
			t.Errorf("a capture not registered = %+v, want one paymentCaptureUnknown", items)
		}
		// A later call failing overwrites last_error; the item stays.
		h.vipps.Fail(vippstest.OpGet, vippstest.Failure{Status: http.StatusInternalServerError, Times: 1})
		h.Advance(10 * time.Minute)
		cycle(t, h)
		if a := attemptOf(t, h, ref); strings.Contains(text(a.LastError), "registered") {
			t.Fatalf("last_error = %s, want the failing read's", text(a.LastError))
		}
		if got := ofType(attentionOf(t, payer(t, h)), "paymentCaptureUnknown"); len(got) != 1 {
			t.Errorf("after a failing read = %+v, want the item kept", got)
		}
		if got := ofType(attentionOf(t, payer(t, h)), "paymentCaptureFailed"); len(got) != 0 {
			t.Errorf("paymentCaptureFailed = %+v, want none", got)
		}
		h.Exec(t, `DROP TRIGGER tr_test_refuse_vipps ON invoices.payments`)
		h.Advance(10 * time.Minute)
		cycle(t, h)
		if got := ofType(attentionOf(t, payer(t, h)), "paymentCaptureUnknown"); len(got) != 0 {
			t.Errorf("once registered = %v, want none", got)
		}
	})

	t.Run("paymentCaptureFailed", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		open := issuedAcme(t, h)
		failed := plantFailedCapture(t, h, open.ID, h.Now())
		paid := issuedAcme(t, h)
		plantFailedCapture(t, h, paid.ID, h.Now())
		registered(t, h, paid.ID, pay(2500, "2026-09-12"))
		old := issuedAcme(t, h)
		plantFailedCapture(t, h, old.ID, h.Now().Add(-15*24*time.Hour))
		retried := issuedAcme(t, h)
		plantFailedCapture(t, h, retried.ID, h.Now().Add(-time.Hour))
		ref := plantReservation(t, h, retried.ID, "capturing", "1000")
		setAttempt(t, h, ref, "state = 'captured', reserved_amount = NULL, captured_amount = 1000, captured_at = now(), finished_at = now()")

		items := ofType(attentionOf(t, payer(t, h)), "paymentCaptureFailed")
		if !slices.Equal(itemIDs(items), []string{fmt.Sprintf("paymentCaptureFailed/%d", failed)}) || items[0].EntityID != entity(open.ID) ||
			items[0].OccurredAt != h.Now().UTC().Format(time.RFC3339) {
			t.Errorf("the items = %+v, want the open invoice's alone: not paid, not older than 14 days, not captured since", items)
		}
		h.Advance(14*24*time.Hour + time.Second)
		if got := ofType(attentionOf(t, payer(t, h)), "paymentCaptureFailed"); len(got) != 0 {
			t.Errorf("after 14 days = %v, want none", got)
		}
	})

	t.Run("paymentReservationStranded", func(t *testing.T) {
		t.Parallel()
		off := newHarness(t, modtest.WithEnv("INVOICES_VIPPS_ENABLED", "0"))
		saveSeller(t, off, completeSeller(1))
		inv := issuedAcme(t, off)
		ref := plantReservation(t, off, inv.ID, "capturing", "2500")
		plantReservation(t, off, issuedAcme(t, off).ID, "authorized", "2500") // no reservation: no item
		items := ofType(attentionOf(t, payer(t, off)), "paymentReservationStranded")
		if !slices.Equal(itemIDs(items), []string{fmt.Sprintf("paymentReservationStranded/%d", attemptIDOf(t, off, ref))}) ||
			items[0].EntityID != entity(inv.ID) || items[0].Title != "Acme AS Norge" {
			t.Errorf("with the switch off = %+v, want the reservation's alone", items)
		}

		h := workerReady(t)
		carried := plantReservation(t, h, issuedAcme(t, h).ID, "capturing", "2500")
		foreign := plantReservation(t, h, issuedAcme(t, h).ID, "capturing", "2500")
		setAttempt(t, h, foreign, "msn = '999999'")
		// A capture of unknown outcome under another sales unit is one item,
		// paymentCaptureUnknown, never a second as stranded.
		foreignUnknown := plantReservation(t, h, issuedAcme(t, h).ID, "capture_unknown", "2500")
		setAttempt(t, h, foreignUnknown, "msn = '999999'")
		parked := plantParkedCreate(t, h, issuedAcme(t, h).ID)
		want := []string{fmt.Sprintf("paymentReservationStranded/%d", attemptIDOf(t, h, foreign)),
			fmt.Sprintf("paymentReservationStranded/%d", attemptIDOf(t, h, parked))}
		all := attentionOf(t, payer(t, h))
		if got := itemIDs(ofType(all, "paymentReservationStranded")); !slices.Equal(got, want) {
			t.Errorf("another sales unit and a parked create = %v, want %v (not %s, which the worker carries)", got, want, carried)
		}
		if got := itemIDs(ofType(all, "paymentCaptureUnknown")); !slices.Equal(got, []string{fmt.Sprintf("paymentCaptureUnknown/%d", attemptIDOf(t, h, foreignUnknown))}) {
			t.Errorf("the foreign capture of unknown outcome = %v, want it once as paymentCaptureUnknown", got)
		}
		abandoned(t, h, attemptIDOf(t, h, parked))
		if got := itemIDs(ofType(attentionOf(t, payer(t, h)), "paymentReservationStranded")); !slices.Equal(got, want[:1]) {
			t.Errorf("after the parked create's abandon = %v, want %v", got, want[:1])
		}
	})
}

// TestAttention_OverdueDropsAFullyReservedInvoice (m14): an overdue invoice
// whose open amount a Vipps capture in progress reserves whole leaves
// invoiceOverdue, as it leaves the overdue list; one reserved in part stays.
func TestAttention_OverdueDropsAFullyReservedInvoice(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	whole := plantOverdue(t, h, overdueSpec{number: 9001, customer: customerAcme, issue: "2026-07-01", due: "2026-08-03"})
	part := plantOverdue(t, h, overdueSpec{number: 9002, customer: customerAcme, issue: "2026-07-01", due: "2026-08-03"})
	plantReservation(t, h, whole, "capturing", "1000")
	plantReservation(t, h, part, "capture_unknown", "400")
	if got := entitiesOf(ofType(attentionOf(t, reader(t, h)), "invoiceOverdue")); !slices.Equal(got, []string{entity(part)}) {
		t.Errorf("the overdue items = %v, want the partly reserved invoice alone", got)
	}
}
