package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
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

// plantCreditNote plants an issued credit note of invoice id of gross,
// issued at at, and answers its id.
func plantCreditNote(t *testing.T, h *harness, id int64, number int64, gross string, at time.Time) int64 {
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
		    created_by_user_id, status, sent_on, deadline, regime, principal_open, fee_kind, charges_earlier, interest,
		    interest_waived, interest_paid, total)
		VALUES ($1, $2, $3, $4, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'printed', $5::date, $5::date + 14,
		    'inkassolov_1988', 1000, 'none', 0, 0, 0, 0, 1000)
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
	plantCreditNote(t, h, refund, 3, "1000", h.Now())
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
		plantCreditNote(t, h, ids[1], 100, "1000", h.Now())
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
		plantCreditNote(t, h, credited, 2, "1000", h.Now())
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

	// A file with lines open — an exception, a line matching left pending —
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
		got := ofType(attentionOf(t, c), "bankTransactionsOpen")
		if len(got) != 1 || got[0].ID != "bankTransactionsOpen/"+entity(r.File.ID) || got[0].EntityID != entity(r.File.ID) ||
			itemCount(got[0]) != 2 || got[0].Title != "2026-10-05" {
			t.Fatalf("the open lines = %+v, want file %d with 2 lines and its booking day, not the matched file %d", got, r.File.ID, matched.File.ID)
		}
		_, exceptions := queueList(t, h, fmt.Sprintf("bankFileId=%d&status=exception", r.File.ID))
		_, pending := queueList(t, h, fmt.Sprintf("bankFileId=%d&status=pending", r.File.ID))
		if exceptions+pending != 2 {
			t.Errorf("the queue counts %d exception and %d pending, want the item's 2", exceptions, pending)
		}
		if res := c.Do(http.MethodPost, matchPath(r.File.ID), nil); res.Status != http.StatusOK {
			t.Fatalf("match = %d %s", res.Status, res.Body)
		}
		if got := ofType(attentionOf(t, c), "bankTransactionsOpen"); len(got) != 1 || itemCount(got[0]) != 1 {
			t.Errorf("the pending line matched = %+v, want the one exception left", got)
		}
		acted(t, c, ids["NOKID"], "dismiss", map[string]any{"note": "Ikke en kundebetaling"})
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
