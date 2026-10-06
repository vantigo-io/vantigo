package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The charges (invoices payments and reminders design D9): what an issued
// invoice's sent letters claimed, kept apart from its principal, paid by
// charge payments and released by waivers. The letters are planted by SQL as
// sent, with their facts — the run that writes them arrives in Task 10.

func chargePaymentsPath(id int64) string {
	return fmt.Sprintf("%s/%d/charge-payments", invoicesPath, id)
}

func chargePaymentRemovalPath(id, chargePaymentID int64) string {
	return fmt.Sprintf("%s/%d/charge-payments/%d/remove", invoicesPath, id, chargePaymentID)
}

func waivePath(id int64) string { return fmt.Sprintf("%s/%d/charges/waive", invoicesPath, id) }

// chargesJSON is the document's charges block.
type chargesJSON struct {
	Claimed     float64  `json:"claimed"`
	Waived      float64  `json:"waived"`
	Paid        float64  `json:"paid"`
	Outstanding float64  `json:"outstanding"`
	RefundDue   *float64 `json:"refundDue"`
}

type chargePaymentJSON struct {
	ID                 int64   `json:"id"`
	PaidOn             string  `json:"paidOn"`
	Amount             float64 `json:"amount"`
	Currency           string  `json:"currency"`
	Source             string  `json:"source"`
	BankTransactionID  *int64  `json:"bankTransactionId"`
	Reference          string  `json:"reference"`
	Note               string  `json:"note"`
	RegisteredAt       string  `json:"registeredAt"`
	RegisteredByUserID string  `json:"registeredByUserId"`
	RemovedAt          *string `json:"removedAt"`
	RemovedByUserID    *string `json:"removedByUserId"`
	RemovalReason      *string `json:"removalReason"`
}

type waiverJSON struct {
	ID              int64   `json:"id"`
	ReminderID      int64   `json:"reminderId"`
	Kind            string  `json:"kind"`
	Amount          float64 `json:"amount"`
	InterestThrough *string `json:"interestThrough"`
	Reason          string  `json:"reason"`
	Note            string  `json:"note"`
	WaivedBy        string  `json:"waivedBy"`
	WaivedAt        string  `json:"waivedAt"`
}

type manualDeliveryJSON struct {
	ID               int64   `json:"id"`
	Kind             string  `json:"kind"`
	DeliveredOn      string  `json:"deliveredOn"`
	Note             string  `json:"note"`
	RecordedAt       string  `json:"recordedAt"`
	RecordedByUserID string  `json:"recordedByUserId"`
	RemovedAt        *string `json:"removedAt"`
	RemovedByUserID  *string `json:"removedByUserId"`
	RemovalReason    *string `json:"removalReason"`
}

// receivablesJSON is the part of a document this task adds, each absent
// where it does not apply, with the principal's figures beside them.
type receivablesJSON struct {
	ID               int64                 `json:"id"`
	State            string                `json:"state"`
	OpenAmount       *float64              `json:"openAmount"`
	DueDate          *string               `json:"dueDate"`
	Charges          *chargesJSON          `json:"charges"`
	ChargePayments   *[]chargePaymentJSON  `json:"chargePayments"`
	Waivers          *[]waiverJSON         `json:"waivers"`
	ManualDeliveries *[]manualDeliveryJSON `json:"manualDeliveries"`
}

// receivablesOf reads document id as receivablesJSON.
func receivablesOf(t *testing.T, h *harness, id int64) receivablesJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, fmt.Sprintf("%s/%d", invoicesPath, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/%d = %d %s", id, res.Status, res.Body)
	}
	var got receivablesJSON
	res.JSON(&got)
	return got
}

// okAs asserts res is a 200 document and answers its receivables.
func okAs(t *testing.T, what string, res *modtest.Response) receivablesJSON {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s = %d %s, want 200", what, res.Status, res.Body)
	}
	var got receivablesJSON
	res.JSON(&got)
	return got
}

// chargesRefusal is a charges 409 as a client reads it.
type chargesRefusal struct {
	Code               string   `json:"code"`
	Detail             string   `json:"detail"`
	ChargesOutstanding *float64 `json:"chargesOutstanding"`
}

// conflictAs asserts res is a 409 with code and answers it.
func conflictAs(t *testing.T, what string, res *modtest.Response, code string) chargesRefusal {
	t.Helper()
	var p chargesRefusal
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
		return p
	}
	res.JSON(&p)
	if p.Code != code {
		t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
	return p
}

// badOn asserts res is a 400 naming field.
func badOn(t *testing.T, what string, res *modtest.Response, field string) {
	t.Helper()
	if res.Status != http.StatusBadRequest {
		t.Errorf("%s = %d %s, want 400 on %s", what, res.Status, res.Body, field)
		return
	}
	if p := problemOf(t, res); len(p.Errors[field]) == 0 {
		t.Errorf("%s = errors %v, want one on %s", what, p.Errors, field)
	}
}

// bareNotFound asserts res is a bare 404.
func bareNotFound(t *testing.T, what string, res *modtest.Response) {
	t.Helper()
	if res.Status != http.StatusNotFound || len(res.Body) != 0 {
		t.Errorf("%s = %d %q, want a bare 404", what, res.Status, res.Body)
	}
}

// sentFacts is a letter as the run and the worker will have written it:
// sent on sentOn with its facts. feeKind is none, reminder_fee or
// compensation; fee and compensation are "" for none; interest is the
// cumulative interest to its date.
type sentFacts struct {
	sequence                    int
	sentOn                      string
	feeKind                     string
	fee, compensation, interest string
}

// plantRun is a reminder run for planted letters to belong to.
func plantRun(t *testing.T, h *harness) int64 {
	t.Helper()
	return plantID(t, h, `INSERT INTO invoices.reminder_runs (run_on, created_at, created_by_user_id, stale_import_acknowledged)
		VALUES (DATE '2026-10-01', now(), gen_random_uuid(), false) RETURNING id`)
}

// plantSent plants l as a sent letter of invoice id and answers its id.
func plantSent(t *testing.T, h *harness, id int64, l sentFacts) int64 {
	t.Helper()
	return plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id,
		    status, sent_on, deadline, regime, principal_open, fee_kind, fee, compensation, charges_earlier, interest,
		    interest_waived, interest_paid, total, sent_at)
		VALUES ($1, $2, $3, 'reminder', 'email', 'nb', now(), gen_random_uuid(),
		    'sent', $4::date, $4::date + 14, 'inkassolov_1988', 1000, $5, NULLIF($6, '')::numeric, NULLIF($7, '')::numeric, 0,
		    $8::numeric, 0, 0, 1000, now())
		RETURNING id`, id, plantRun(t, h), l.sequence, l.sentOn, l.feeKind, l.fee, l.compensation, l.interest)
}

// plantQueued plants a letter of invoice id still in flight — queued, with
// no facts — and answers its id.
func plantQueued(t *testing.T, h *harness, id int64, sequence int) int64 {
	t.Helper()
	return plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id, status)
		VALUES ($1, $2, $3, 'reminder', 'email', 'nb', now(), gen_random_uuid(), 'queued') RETURNING id`, id, plantRun(t, h), sequence)
}

// chargePayer is a caller who may register and remove charge payments and
// waive charges.
func chargePayer(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:payments")
}

// waiver is one waiver of a waive request.
func waiver(reminderID int64, kind string) map[string]any {
	return map[string]any{"reminderId": reminderID, "kind": kind}
}

// waive is a waive request's body.
func waive(reason string, ws ...map[string]any) map[string]any {
	return map[string]any{"waivers": ws, "reason": reason}
}

// chargesAre asserts the charges block.
func chargesAre(t *testing.T, what string, got receivablesJSON, claimed, waived, paid, outstanding float64, refundDue *float64) {
	t.Helper()
	c := got.Charges
	if c == nil {
		t.Errorf("%s: no charges block, want claimed %v waived %v paid %v outstanding %v", what, claimed, waived, paid, outstanding)
		return
	}
	refundOK := (c.RefundDue == nil) == (refundDue == nil) && (refundDue == nil || *c.RefundDue == *refundDue)
	if c.Claimed != claimed || c.Waived != waived || c.Paid != paid || c.Outstanding != outstanding || !refundOK {
		t.Errorf("%s: charges = claimed %v waived %v paid %v outstanding %v refundDue %v; want %v %v %v %v %v",
			what, c.Claimed, c.Waived, c.Paid, c.Outstanding, money(c.RefundDue), claimed, waived, paid, outstanding, money(refundDue))
	}
}

// The block (D9): an issued invoice answers claimed — its sent letters' fees
// and compensation and the latest one's cumulative interest — waived, paid
// and outstanding, with refundDue only when a paid charge was then waived; a
// removed charge payment counts for nothing but stays on the record; the
// principal — its open amount and state — never moves; a draft and a credit
// note answer none of it.
func TestCharges_TheBlock(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	c, userID := h.SignInUser(t, "invoices:access", "invoices:payments")

	fresh := receivablesOf(t, h, inv.ID)
	chargesAre(t, "with no letter", fresh, 0, 0, 0, 0, nil)
	if fresh.ChargePayments == nil || len(*fresh.ChargePayments) != 0 || fresh.Waivers == nil || len(*fresh.Waivers) != 0 ||
		fresh.ManualDeliveries == nil || len(*fresh.ManualDeliveries) != 0 {
		t.Errorf("an issued invoice = chargePayments %v waivers %v manualDeliveries %v, want three empty lists",
			fresh.ChargePayments, fresh.Waivers, fresh.ManualDeliveries)
	}

	first := plantSent(t, h, inv.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "4.50"})
	second := plantSent(t, h, inv.ID, sentFacts{2, "2026-10-20", "reminder_fee", "35", "", "10.25"})
	plantQueued(t, h, inv.ID, 3) // in flight: claims nothing
	chargesAre(t, "two sent letters", receivablesOf(t, h, inv.ID), 80.25, 0, 0, 80.25, nil)

	paid := okAs(t, "a charge payment of 20", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), map[string]any{
		"amount": 20, "paidOn": "2026-09-12", "reference": " Bank 4471 ", "note": " Purregebyr ",
	}))
	chargesAre(t, "20 paid", paid, 80.25, 0, 20, 60.25, nil)
	if paid.State != "open" || money(paid.OpenAmount) != 1000.0 {
		t.Errorf("after a charge payment = %s open %v, want the principal untouched: open, 1000", paid.State, money(paid.OpenAmount))
	}
	if paid.ChargePayments == nil || len(*paid.ChargePayments) != 1 {
		t.Fatalf("chargePayments = %v, want the one registered", paid.ChargePayments)
	}
	if p := (*paid.ChargePayments)[0]; p.Amount != 20 || p.PaidOn != "2026-09-12" || p.Currency != "NOK" || p.Source != "manual" ||
		p.BankTransactionID != nil || p.Reference != "Bank 4471" || p.Note != "Purregebyr" || p.RegisteredByUserID != userID.String() ||
		p.RemovedAt != nil {
		t.Errorf("the charge payment = %+v, want 20 NOK manual on 2026-09-12, trimmed, by %s, live", p, userID)
	}

	extra := okAs(t, "a charge payment of 30", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), map[string]any{"amount": 30, "paidOn": "2026-09-12"}))
	extraID := (*extra.ChargePayments)[1].ID
	removed := okAs(t, "its removal", c.Do(http.MethodPost, chargePaymentRemovalPath(inv.ID, extraID), map[string]any{"reason": "Feil faktura"}))
	chargesAre(t, "the 30 removed", removed, 80.25, 0, 20, 60.25, nil)
	if n := len(*removed.ChargePayments); n != 2 {
		t.Errorf("chargePayments = %d, want both, the removed one kept", n)
	}

	// The first fee waived: the 20 now meets the second letter's fee.
	waived := okAs(t, "the first fee waived", c.Do(http.MethodPost, waivePath(inv.ID), waive("claimed_in_error", waiver(first, "fee"))))
	chargesAre(t, "the first fee waived", waived, 80.25, 35, 20, 25.25, nil)
	if waived.Waivers == nil || len(*waived.Waivers) != 1 {
		t.Fatalf("waivers = %v, want the one", waived.Waivers)
	}
	if w := (*waived.Waivers)[0]; w.ReminderID != first || w.Kind != "fee" || w.Amount != 35 || w.InterestThrough != nil ||
		w.Reason != "claimed_in_error" || w.Note != "" || w.WaivedBy != userID.String() {
		t.Errorf("the waiver = %+v, want letter %d's fee of 35, claimed_in_error, by %s", w, first, userID)
	}

	settledCharges := okAs(t, "the rest paid", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), map[string]any{"amount": 25.25, "paidOn": "2026-09-12"}))
	chargesAre(t, "everything paid", settledCharges, 80.25, 35, 45.25, 0, nil)

	// A paid fee waived: the money is owed back.
	refund := okAs(t, "a paid fee waived", c.Do(http.MethodPost, waivePath(inv.ID), waive("objection_upheld", waiver(second, "fee"))))
	chargesAre(t, "a paid fee waived", refund, 80.25, 70, 45.25, 0, ptr(35.0))
	if refund.State != "open" || money(refund.OpenAmount) != 1000.0 {
		t.Errorf("after the charges = %s open %v, want the principal untouched: open, 1000", refund.State, money(refund.OpenAmount))
	}

	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	creditNote := issued(t, h, creditDraft(t, h, thousand(t, h).ID).ID)
	for _, doc := range []struct {
		what string
		id   int64
	}{{"a draft", draft.ID}, {"a credit note", creditNote.ID}} {
		got := receivablesOf(t, h, doc.id)
		if got.Charges != nil || got.ChargePayments != nil || got.Waivers != nil || got.ManualDeliveries != nil {
			t.Errorf("%s = charges %v chargePayments %v waivers %v manualDeliveries %v, want none of them",
				doc.what, got.Charges, got.ChargePayments, got.Waivers, got.ManualDeliveries)
		}
	}
}

// A charge payment follows the payment's rules (D2), then the charges' (D9):
// every field rule a 400 naming its field, judged before the lock; no
// document a bare 404; a credit note, draft or issued, credit_note_no_payments
// and an invoice draft invoice_draft, judged before the body; then under the
// invoice's lock no_charges_outstanding and charge_payment_exceeds_outstanding
// carrying the figure. Nothing refused is written; exactly what is
// outstanding is taken, and then nothing more.
func TestChargePayments_RefusalsInOrder(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	c := chargePayer(t, h)
	body := func(field string, v any) map[string]any {
		b := map[string]any{"amount": 10, "paidOn": "2026-09-12"}
		if field != "" {
			b[field] = v
		}
		return b
	}
	for _, bad := range []struct {
		name, field string
		body        map[string]any
	}{
		{"no paidOn", "paidOn", map[string]any{"amount": 10}},
		{"paid before the issue date", "paidOn", body("paidOn", "2026-09-11")},
		{"paid after today", "paidOn", body("paidOn", "2026-09-13")},
		{"a zero amount", "amount", body("amount", 0)},
		{"a negative amount", "amount", body("amount", -5)},
		{"three decimals", "amount", body("amount", 10.005)},
		{"over the bound", "amount", body("amount", 100000000000)},
		{"a reference over 100", "reference", body("reference", strings.Repeat("r", 101))},
		{"a note over 500", "note", body("note", strings.Repeat("ø", 501))},
	} {
		// No letter is sent yet: the field is judged before the charges are.
		badOn(t, bad.name, c.Do(http.MethodPost, chargePaymentsPath(inv.ID), bad.body), bad.field)
	}

	bareNotFound(t, "no document", c.Do(http.MethodPost, chargePaymentsPath(999999), body("", nil)))
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	conflictAs(t, "an invoice draft", c.Do(http.MethodPost, chargePaymentsPath(draft.ID), body("amount", 0)), "invoice_draft")
	original := thousand(t, h)
	conflictAs(t, "a credit-note draft", c.Do(http.MethodPost, chargePaymentsPath(creditDraft(t, h, original.ID).ID), body("", nil)),
		"credit_note_no_payments")
	conflictAs(t, "an issued credit note", c.Do(http.MethodPost, chargePaymentsPath(issued(t, h, creditDraft(t, h, original.ID).ID).ID),
		body("", nil)), "credit_note_no_payments")

	if p := conflictAs(t, "no letter sent", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), body("", nil)), "no_charges_outstanding"); p.ChargesOutstanding != nil {
		t.Errorf("no_charges_outstanding carries chargesOutstanding %v, want none", *p.ChargesOutstanding)
	}
	plantQueued(t, h, inv.ID, 1)
	conflictAs(t, "a letter in flight only", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), body("", nil)), "no_charges_outstanding")

	letterID := plantSent(t, h, inv.ID, sentFacts{2, "2026-10-01", "reminder_fee", "35", "", "1.40"})
	p := conflictAs(t, "36.41 against 36.40", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), body("amount", 36.41)), "charge_payment_exceeds_outstanding")
	if p.ChargesOutstanding == nil || *p.ChargesOutstanding != 36.4 {
		t.Errorf("chargesOutstanding = %v, want 36.4", money(p.ChargesOutstanding))
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.charge_payments`); n != 0 {
		t.Fatalf("charge payments = %d after the refusals, want none written", n)
	}

	chargesAre(t, "exactly what is outstanding", okAs(t, "36.40", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), body("amount", 36.4))),
		36.4, 0, 36.4, 0, nil)
	conflictAs(t, "once paid", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), body("amount", 0.01)), "no_charges_outstanding")
	// A waiver releases what was paid: the payment left over is a refund
	// due, not something outstanding.
	okAs(t, "the fee waived", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(letterID, "fee"))))
	conflictAs(t, "a refund due", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), body("amount", 0.01)), "no_charges_outstanding")

	if res := h.SignIn(t, "invoices:access").Do(http.MethodPost, chargePaymentsPath(inv.ID), body("", nil)); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:payments = %d, want 403", res.Status)
	}
}

// A charge payment is removed as a payment is (D2, D9): the reason judged
// before anything is read; a charge payment that is not the document's a
// bare 404; the removal recorded — when, by whom, the trimmed reason — the
// row kept and counting for nothing; a second removal payment_removed.
func TestChargePayments_Removal(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	other := thousand(t, h)
	plantSent(t, h, inv.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "0"})
	plantSent(t, h, other.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "0"})
	c, userID := h.SignInUser(t, "invoices:access", "invoices:payments")
	paid := okAs(t, "a charge payment", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), map[string]any{"amount": 35, "paidOn": "2026-09-12"}))
	id := (*paid.ChargePayments)[0].ID
	otherPaid := okAs(t, "the other's", c.Do(http.MethodPost, chargePaymentsPath(other.ID), map[string]any{"amount": 35, "paidOn": "2026-09-12"}))
	otherID := (*otherPaid.ChargePayments)[0].ID

	for _, reason := range []string{"", "   ", strings.Repeat("x", 201)} {
		badOn(t, fmt.Sprintf("reason %q", reason), c.Do(http.MethodPost, chargePaymentRemovalPath(999999, id), map[string]any{"reason": reason}), "reason")
	}
	for _, path := range []string{chargePaymentRemovalPath(999999, id), chargePaymentRemovalPath(inv.ID, otherID),
		chargePaymentRemovalPath(other.ID, id), chargePaymentRemovalPath(inv.ID, 999999)} {
		bareNotFound(t, "POST "+path, c.Do(http.MethodPost, path, map[string]any{"reason": "Feil"}))
	}

	got := okAs(t, "the removal", c.Do(http.MethodPost, chargePaymentRemovalPath(inv.ID, id), map[string]any{"reason": "  Feil beløp  "}))
	chargesAre(t, "after the removal", got, 35, 0, 0, 35, nil)
	if got.ChargePayments == nil || len(*got.ChargePayments) != 1 {
		t.Fatalf("chargePayments = %v, want the removed one kept", got.ChargePayments)
	}
	p := (*got.ChargePayments)[0]
	if p.RemovedAt == nil || p.RemovedByUserID == nil || *p.RemovedByUserID != userID.String() || p.RemovalReason == nil || *p.RemovalReason != "Feil beløp" {
		t.Errorf("the removed charge payment = %+v, want removed by %s with the trimmed reason", p, userID)
	} else if at, err := time.Parse(time.RFC3339Nano, *p.RemovedAt); err != nil || !at.Equal(h.Now()) {
		t.Errorf("removedAt = %s (%v), want the clock's %s", *p.RemovedAt, err, h.Now())
	}
	conflictAs(t, "removed again", c.Do(http.MethodPost, chargePaymentRemovalPath(inv.ID, id), map[string]any{"reason": "Igjen"}), "payment_removed")
	if res := h.SignIn(t, "invoices:access", "invoices:issue").Do(http.MethodPost, chargePaymentRemovalPath(other.ID, otherID),
		map[string]any{"reason": "Feil"}); res.Status != http.StatusForbidden {
		t.Errorf("a removal without invoices:payments = %d, want 403", res.Status)
	}
	chargesAre(t, "the other invoice", receivablesOf(t, h, other.ID), 35, 0, 35, 0, nil)
}

// The waive endpoint's refusals (D9): the body's 400s; no document a bare
// 404, a credit note credit_note_no_reminders, a draft invoice_draft; a
// letter that is not the invoice's a bare 404; charge_not_claimed for each
// case — a letter not sent, a charge the letter did not claim, a fee waived
// already, interest with none left unpaid — and one refusal in a request
// writes none of its waivers.
func TestWaive_Refusals(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	other := thousand(t, h)
	c := chargePayer(t, h)
	feeLetter := plantSent(t, h, inv.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "0"})
	queued := plantQueued(t, h, inv.ID, 2)
	othersLetter := plantSent(t, h, other.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "0"})

	many := make([]map[string]any, 51)
	for i := range many {
		many[i] = waiver(feeLetter, "fee")
	}
	for _, bad := range []struct {
		name, field string
		body        map[string]any
	}{
		{"no waivers", "waivers", waive("goodwill")},
		{"51 waivers", "waivers", waive("goodwill", many...)},
		{"an unknown kind", "waivers", waive("goodwill", waiver(feeLetter, "penalty"))},
		{"an unknown reason", "reason", waive("because", waiver(feeLetter, "fee"))},
		{"deadline_met, the match's own", "reason", waive("deadline_met", waiver(feeLetter, "fee"))},
		{"a note over 500", "note", map[string]any{"waivers": []any{waiver(feeLetter, "fee")}, "reason": "goodwill", "note": strings.Repeat("n", 501)}},
	} {
		badOn(t, bad.name, c.Do(http.MethodPost, waivePath(999999), bad.body), bad.field)
	}

	bareNotFound(t, "no document", c.Do(http.MethodPost, waivePath(999999), waive("goodwill", waiver(feeLetter, "fee"))))
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	conflictAs(t, "a draft", c.Do(http.MethodPost, waivePath(draft.ID), waive("goodwill", waiver(feeLetter, "fee"))), "invoice_draft")
	creditNote := issued(t, h, creditDraft(t, h, thousand(t, h).ID).ID)
	conflictAs(t, "a credit note", c.Do(http.MethodPost, waivePath(creditNote.ID), waive("goodwill", waiver(feeLetter, "fee"))), "credit_note_no_reminders")

	bareNotFound(t, "another invoice's letter", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(othersLetter, "fee"))))
	bareNotFound(t, "no such letter", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(999999, "fee"))))
	conflictAs(t, "a letter not sent", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(queued, "fee"))), "charge_not_claimed")
	conflictAs(t, "no compensation claimed", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(feeLetter, "compensation"))), "charge_not_claimed")
	conflictAs(t, "no interest claimed", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(feeLetter, "interest"))), "charge_not_claimed")
	conflictAs(t, "the fee twice in one request", c.Do(http.MethodPost, waivePath(inv.ID),
		waive("goodwill", waiver(feeLetter, "fee"), waiver(feeLetter, "fee"))), "charge_not_claimed")
	if n := h.Count(t, `SELECT count(*) FROM invoices.charge_waivers`); n != 0 {
		t.Fatalf("waivers = %d after the refusals, want none written", n)
	}

	okAs(t, "the fee", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(feeLetter, "fee"))))
	conflictAs(t, "the fee waived already", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(feeLetter, "fee"))), "charge_not_claimed")
	if n := h.Count(t, `SELECT count(*) FROM invoices.charge_waivers`); n != 1 {
		t.Errorf("waivers = %d, want the one", n)
	}
	if res := h.SignIn(t, "invoices:access", "invoices:issue").Do(http.MethodPost, waivePath(other.ID), waive("goodwill", waiver(othersLetter, "fee"))); res.Status != http.StatusForbidden {
		t.Errorf("without invoices:payments = %d, want 403", res.Status)
	}
}

// Interest is waived as an amount (D9, NI1): what the latest sent letter
// claimed less every earlier interest waiver and the charge payments
// allocated to interest — 20 claimed and 12 paid waives 8, and then nothing
// is left (409); a later letter claiming 33 cumulatively waives 33 − 8 − 12
// = 13, never counting the earlier waiver twice; an earlier letter is not
// the one that claims it.
func TestWaive_InterestIsAnAmount(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	c := chargePayer(t, h)
	first := plantSent(t, h, inv.ID, sentFacts{1, "2026-10-01", "none", "", "", "20"})
	okAs(t, "12 paid", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), map[string]any{"amount": 12, "paidOn": "2026-09-12"}))

	got := okAs(t, "the interest", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(first, "interest"))))
	chargesAre(t, "the interest waived", got, 20, 8, 12, 0, nil)
	if got.Waivers == nil || len(*got.Waivers) != 1 {
		t.Fatalf("waivers = %v, want one", got.Waivers)
	}
	if w := (*got.Waivers)[0]; w.Kind != "interest" || w.Amount != 8 || w.ReminderID != first || w.InterestThrough == nil || *w.InterestThrough != "2026-10-01" {
		t.Errorf("the waiver = %+v, want interest 8 of letter %d through 2026-10-01", w, first)
	}
	conflictAs(t, "nothing left", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(first, "interest"))), "charge_not_claimed")

	second := plantSent(t, h, inv.ID, sentFacts{2, "2026-10-20", "none", "", "", "33"})
	chargesAre(t, "a later letter", receivablesOf(t, h, inv.ID), 33, 8, 12, 13, nil)
	conflictAs(t, "an earlier letter", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(first, "interest"))), "charge_not_claimed")
	later := okAs(t, "the later interest", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(second, "interest"))))
	chargesAre(t, "everything waived", later, 33, 21, 12, 0, nil)
	if w := (*later.Waivers)[1]; w.Amount != 13 || w.ReminderID != second || w.InterestThrough == nil || *w.InterestThrough != "2026-10-20" {
		t.Errorf("the second waiver = %+v, want 13 of letter %d through 2026-10-20", w, second)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT string_agg(amount::text, ',' ORDER BY id) FROM invoices.charge_waivers WHERE invoice_id = $1`, inv.ID); got != "8.00,13.00" {
		t.Errorf("the stored waivers = %s, want exactly 8.00,13.00", got)
	}
}

// A fee and the compensation are waived whole (D9), whatever was paid of
// them — what was paid is then a refund due — with the note and the reason
// the caller gave; a letter that claimed the compensation claimed no fee.
func TestWaive_FeeAndCompensationWhole(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := chargePayer(t, h)

	inv := thousand(t, h)
	fee := plantSent(t, h, inv.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "0"})
	okAs(t, "10 paid", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), map[string]any{"amount": 10, "paidOn": "2026-09-12"}))
	got := okAs(t, "the fee", c.Do(http.MethodPost, waivePath(inv.ID), map[string]any{
		"waivers": []any{waiver(fee, "fee")}, "reason": "goodwill", "note": "  Kunden klaget  ",
	}))
	chargesAre(t, "the fee waived whole", got, 35, 35, 10, 0, ptr(10.0))
	if w := (*got.Waivers)[0]; w.Amount != 35 || w.Note != "Kunden klaget" || w.Reason != "goodwill" {
		t.Errorf("the waiver = %+v, want the whole 35, goodwill, the trimmed note", w)
	}

	business := thousand(t, h)
	compensation := plantSent(t, h, business.ID, sentFacts{1, "2026-10-01", "compensation", "", "360", "2.10"})
	conflictAs(t, "a fee on a compensation letter", c.Do(http.MethodPost, waivePath(business.ID), waive("goodwill", waiver(compensation, "fee"))), "charge_not_claimed")
	whole := okAs(t, "the compensation", c.Do(http.MethodPost, waivePath(business.ID), waive("objection_upheld", waiver(compensation, "compensation"))))
	chargesAre(t, "the compensation waived", whole, 362.1, 360, 0, 2.1, nil)
	if w := (*whole.Waivers)[0]; w.Kind != "compensation" || w.Amount != 360 || w.InterestThrough != nil {
		t.Errorf("the waiver = %+v, want the whole compensation, 360", w)
	}
	conflictAs(t, "the compensation again", c.Do(http.MethodPost, waivePath(business.ID), waive("goodwill", waiver(compensation, "compensation"))), "charge_not_claimed")
}

// Every write of the charges and the deliveries takes one lock, the invoice,
// through lockInvoice (D18: a charge payment, a waiver, a manual delivery
// and its removal — the invoice alone).
func TestCharges_LockOrder(t *testing.T) {
	h := readyToIssue(t)
	inv := thousand(t, h)
	letter := plantSent(t, h, inv.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "0"})
	deliverer := h.SignIn(t, "invoices:access", "invoices:issue")
	c := chargePayer(t, h)
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()
	want := []string{"invoice " + idKey(inv.ID)}
	check := func(what string) {
		t.Helper()
		if got := seen.take(); !slices.Equal(got, want) {
			t.Errorf("%s took %v, want %v", what, got, want)
		}
	}

	paid := okAs(t, "a charge payment", c.Do(http.MethodPost, chargePaymentsPath(inv.ID), map[string]any{"amount": 5, "paidOn": "2026-09-12"}))
	check("a charge payment")
	okAs(t, "its removal", c.Do(http.MethodPost, chargePaymentRemovalPath(inv.ID, (*paid.ChargePayments)[0].ID), map[string]any{"reason": "Feil"}))
	check("a charge payment's removal")
	okAs(t, "a waiver", c.Do(http.MethodPost, waivePath(inv.ID), waive("goodwill", waiver(letter, "fee"))))
	check("a waiver")
	recorded := okAs(t, "a manual delivery", deliverer.Do(http.MethodPost, manualDeliveriesPath(inv.ID), map[string]any{"kind": "posted", "deliveredOn": "2026-09-12"}))
	check("a manual delivery")
	okAs(t, "its removal", deliverer.Do(http.MethodPost, manualDeliveryRemovalPath(inv.ID, (*recorded.ManualDeliveries)[0].ID), map[string]any{"reason": "Feil"}))
	check("a manual delivery's removal")
}
