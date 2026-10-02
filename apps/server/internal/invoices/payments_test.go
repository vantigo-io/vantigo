package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func paymentsPath(id int64) string { return fmt.Sprintf("%s/%d/payments", invoicesPath, id) }

func removalPath(id, paymentID int64) string {
	return fmt.Sprintf("%s/%d/payments/%d/remove", invoicesPath, id, paymentID)
}

// payer is a caller who may register and remove payments.
func payer(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access", "invoices:payments")
}

// pay is a registration body: amount paid on paidOn.
func pay(amount float64, paidOn string) map[string]any {
	return map[string]any{"amount": amount, "paidOn": paidOn}
}

// registered posts body against invoice id and answers the document, failing
// unless the payment was registered.
func registered(t *testing.T, h *harness, id int64, body map[string]any) invoiceJSON {
	t.Helper()
	res := payer(t, h).Do(http.MethodPost, paymentsPath(id), body)
	if res.Status != http.StatusOK {
		t.Fatalf("POST /invoices/%d/payments %v = %d %s, want 200", id, body, res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	return inv
}

// paymentRefusal is a payment's 409 as a client reads it, openAmount
// included.
type paymentRefusal struct {
	Code       string   `json:"code"`
	Detail     string   `json:"detail"`
	OpenAmount *float64 `json:"openAmount"`
}

// refusedAs asserts res is a 409 with code and answers it.
func refusedAs(t *testing.T, what string, res *modtest.Response, code string) paymentRefusal {
	t.Helper()
	var p paymentRefusal
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

// thousand is an issued invoice for customerAcme of 1000.00 gross — ten
// units at 80 plus 25 % VAT — issued today, 2026-09-12 on the fixed clock.
func thousand(t *testing.T, h *harness) invoiceJSON {
	t.Helper()
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25))).ID)
	if inv.GrossTotal != 1000 || inv.IssueDate == nil || *inv.IssueDate != "2026-09-12" {
		t.Fatalf("the invoice = gross %v issued %v, want 1000 on 2026-09-12", inv.GrossTotal, inv.IssueDate)
	}
	return inv
}

// livePayments is how many live registrations invoice id has.
func livePayments(t *testing.T, h *harness, id int64) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM invoices.payments WHERE invoice_id = $1 AND removed_at IS NULL`, id)
}

// A registration against an issued invoice answers the document with its new
// state and money (D2, D3): partly paid, then — the exact open amount
// registered, øre for øre — paid. The row records what was said, the
// document's currency, the caller and the clock.
func TestPayments_RegisterAgainstAnIssuedInvoice(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	c, userID := h.SignInUser(t, "invoices:access", "invoices:payments")

	res := c.Do(http.MethodPost, paymentsPath(inv.ID), map[string]any{
		"amount": 300.1, "paidOn": "2026-09-12", "reference": "  KID 0012345  ", "note": " Delbetaling ",
	})
	if res.Status != http.StatusOK {
		t.Fatalf("the first registration = %d %s, want 200", res.Status, res.Body)
	}
	var partly invoiceJSON
	res.JSON(&partly)
	if partly.State != "partially_paid" || money(partly.PaidAmount) != 300.1 || money(partly.OpenAmount) != 699.9 || partly.RefundDue != nil {
		t.Errorf("after 300.10 = %q paid %v open %v refund %v, want partially_paid, 300.1, 699.9, none",
			partly.State, money(partly.PaidAmount), money(partly.OpenAmount), money(partly.RefundDue))
	}
	if len(partly.Payments) != 1 {
		t.Fatalf("payments = %+v, want the one registered", partly.Payments)
	}
	p := partly.Payments[0]
	if p.Amount != 300.1 || p.Currency != "NOK" || p.PaidOn != "2026-09-12" || p.Reference != "KID 0012345" || p.Note != "Delbetaling" ||
		p.RegisteredByUserID != userID.String() || p.RemovedAt != nil {
		t.Errorf("the registration = %+v, want 300.1 NOK on 2026-09-12, trimmed, by %s, live", p, userID)
	}
	if at, err := time.Parse(time.RFC3339Nano, p.RegisteredAt); err != nil || !at.Equal(h.Now()) {
		t.Errorf("registeredAt = %s (%v), want the clock's %s", p.RegisteredAt, err, h.Now().Format(time.RFC3339Nano))
	}
	if got := modtest.One[string](t, h.Harness, `SELECT amount::text FROM invoices.payments WHERE id = $1`, p.ID); got != "300.10" {
		t.Errorf("the stored amount = %s, want exactly 300.10", got)
	}

	paid := registered(t, h, inv.ID, pay(699.9, "2026-09-12"))
	if paid.State != "paid" || money(paid.PaidAmount) != 1000.0 || money(paid.OpenAmount) != 0.0 || paid.RefundDue != nil || len(paid.Payments) != 2 {
		t.Errorf("after the exact open amount = %q paid %v open %v refund %v, %d payments; want paid, 1000, 0, none, 2",
			paid.State, money(paid.PaidAmount), money(paid.OpenAmount), money(paid.RefundDue), len(paid.Payments))
	}
	if got := getInvoice(t, h, inv.ID); got.State != "paid" || money(got.OpenAmount) != 0.0 {
		t.Errorf("read again = %q open %v, want paid, 0", got.State, money(got.OpenAmount))
	}
}

// Only an issued invoice takes a payment (D2 step 1): no document is a bare
// 404; an invoice draft is invoice_draft; a credit note — issued, or a draft,
// the kind decided first as the credit endpoint decides it — is
// credit_note_no_payments. Nothing is written.
func TestPayments_RefusesWhatIsNotAnIssuedInvoice(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	creditNoteDraft := creditDraft(t, h, inv.ID)
	creditNote := issued(t, h, creditDraft(t, h, inv.ID).ID)
	c := payer(t, h)

	if res := c.Do(http.MethodPost, paymentsPath(999999), pay(100, "2026-09-12")); res.Status != http.StatusNotFound || len(res.Body) != 0 {
		t.Errorf("no document = %d %q, want a bare 404", res.Status, res.Body)
	}
	refusedAs(t, "an invoice draft", c.Do(http.MethodPost, paymentsPath(draft.ID), pay(100, "2026-09-12")), "invoice_draft")
	refusedAs(t, "an issued credit note", c.Do(http.MethodPost, paymentsPath(creditNote.ID), pay(100, "2026-09-12")), "credit_note_no_payments")
	refusedAs(t, "a credit-note draft", c.Do(http.MethodPost, paymentsPath(creditNoteDraft.ID), pay(100, "2026-09-12")), "credit_note_no_payments")
	if n := h.Count(t, `SELECT count(*) FROM invoices.payments`); n != 0 {
		t.Errorf("payments = %d, want none written", n)
	}
}

// Every field rule is a 400 naming its field (D2 step 2): paidOn before the
// issue date or after today (Oslo); amount 0, negative, with three decimals
// or over the document bound; reference over 100 characters, note over 500.
// The edges pass. The fields are judged before anything is locked or summed:
// a settled invoice answers a bad body with the 400, not invoice_settled.
func TestPayments_TheFieldRules(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	c := payer(t, h)
	withField := func(field string, v any) map[string]any {
		body := pay(100, "2026-09-12")
		body[field] = v
		return body
	}
	for _, bad := range []struct {
		name, field string
		body        map[string]any
	}{
		{"paid before the issue date", "paidOn", withField("paidOn", "2026-09-11")},
		{"paid after today", "paidOn", withField("paidOn", "2026-09-13")},
		{"a zero amount", "amount", withField("amount", 0)},
		{"a negative amount", "amount", withField("amount", -5)},
		{"three decimals", "amount", withField("amount", 10.005)},
		{"over the bound", "amount", withField("amount", 100000000000)},
		{"a reference over 100", "reference", withField("reference", strings.Repeat("r", 101))},
		{"a note over 500", "note", withField("note", strings.Repeat("ø", 501))},
	} {
		res := c.Do(http.MethodPost, paymentsPath(inv.ID), bad.body)
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", bad.name, res.Status, res.Body)
			continue
		}
		if p := problemOf(t, res); len(p.Errors[bad.field]) == 0 {
			t.Errorf("%s = errors %v, want one on %s", bad.name, p.Errors, bad.field)
		}
	}
	if n := livePayments(t, h, inv.ID); n != 0 {
		t.Fatalf("payments = %d after the refusals, want none", n)
	}

	edges := pay(1000, "2026-09-12")
	edges["reference"], edges["note"] = strings.Repeat("r", 100), strings.Repeat("ø", 500)
	if got := registered(t, h, inv.ID, edges); got.State != "paid" {
		t.Errorf("the edges = %q, want registered and paid", got.State)
	}
	res := c.Do(http.MethodPost, paymentsPath(inv.ID), withField("amount", 0))
	if res.Status != http.StatusBadRequest {
		t.Errorf("a bad body against a settled invoice = %d %s, want the 400 first", res.Status, res.Body)
	}
}

// Once nothing is open a payment is invoice_settled; one over the open amount
// is payment_exceeds_open carrying the open amount (D2 step 3), whether the
// open amount is the gross, what payments left, or what a credit note left —
// and an invoice credited in full is settled too.
func TestPayments_SettledAndExceeding(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	c := payer(t, h)
	exceeds := func(what string, amount, open float64) {
		t.Helper()
		p := refusedAs(t, what, c.Do(http.MethodPost, paymentsPath(inv.ID), pay(amount, "2026-09-12")), "payment_exceeds_open")
		if p.OpenAmount == nil || *p.OpenAmount != open {
			t.Errorf("%s: openAmount = %v, want %v", what, money(p.OpenAmount), open)
		}
	}

	exceeds("1000.01 against 1000", 1000.01, 1000)
	registered(t, h, inv.ID, pay(400, "2026-09-12"))
	exceeds("600.01 against 600", 600.01, 600)
	if got := registered(t, h, inv.ID, pay(600, "2026-09-12")); got.State != "paid" {
		t.Errorf("the rest = %q, want paid", got.State)
	}
	settled := refusedAs(t, "a payment once paid", c.Do(http.MethodPost, paymentsPath(inv.ID), pay(0.01, "2026-09-12")), "invoice_settled")
	if settled.OpenAmount != nil {
		t.Errorf("invoice_settled carries openAmount %v, want none", *settled.OpenAmount)
	}
	if n := livePayments(t, h, inv.ID); n != 2 {
		t.Errorf("live payments = %d, want the two registered", n)
	}

	partlyCredited := thousand(t, h)
	issued(t, h, creditUnits(t, h, partlyCredited.ID, 3).ID)
	p := refusedAs(t, "800 against what a credit note left", c.Do(http.MethodPost, paymentsPath(partlyCredited.ID), pay(800, "2026-09-12")), "payment_exceeds_open")
	if p.OpenAmount == nil || *p.OpenAmount != 700 {
		t.Errorf("openAmount after a credit note of 300 = %v, want 700", money(p.OpenAmount))
	}

	credited := thousand(t, h)
	issued(t, h, creditDraft(t, h, credited.ID).ID)
	refusedAs(t, "a payment on an invoice credited in full", c.Do(http.MethodPost, paymentsPath(credited.ID), pay(1, "2026-09-12")), "invoice_settled")
}

// A removal with a reason reopens the invoice and is recorded (D2): the row
// stays, struck through — removedAt, who, the trimmed reason — and counts for
// nothing; a second removal is payment_removed; the reason is judged before
// anything is read; a payment is removed only through its own invoice; and
// the money is registered again.
func TestPayments_RemovalReopensAndIsRecorded(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	other := thousand(t, h)
	paid := registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	paymentID := paid.Payments[0].ID
	c, userID := h.SignInUser(t, "invoices:access", "invoices:payments")

	for _, reason := range []string{"", "   ", strings.Repeat("x", 201)} {
		res := c.Do(http.MethodPost, removalPath(999999, paymentID), map[string]any{"reason": reason})
		if res.Status != http.StatusBadRequest || len(problemOf(t, res).Errors["reason"]) == 0 {
			t.Errorf("reason %q = %d %s, want a 400 on reason before the document is looked for", reason, res.Status, res.Body)
		}
	}
	for _, path := range []string{removalPath(999999, paymentID), removalPath(other.ID, paymentID), removalPath(inv.ID, 999999)} {
		if res := c.Do(http.MethodPost, path, map[string]any{"reason": "Feil"}); res.Status != http.StatusNotFound || len(res.Body) != 0 {
			t.Errorf("POST %s = %d %q, want a bare 404", path, res.Status, res.Body)
		}
	}

	res := c.Do(http.MethodPost, removalPath(inv.ID, paymentID), map[string]any{"reason": "  Feil beløp  "})
	if res.Status != http.StatusOK {
		t.Fatalf("the removal = %d %s, want 200", res.Status, res.Body)
	}
	var reopened invoiceJSON
	res.JSON(&reopened)
	if reopened.State != "open" || money(reopened.PaidAmount) != 0.0 || money(reopened.OpenAmount) != 1000.0 || len(reopened.Payments) != 1 {
		t.Fatalf("after the removal = %q paid %v open %v payments %+v, want open, 0, 1000 and the row kept",
			reopened.State, money(reopened.PaidAmount), money(reopened.OpenAmount), reopened.Payments)
	}
	p := reopened.Payments[0]
	if p.ID != paymentID || p.Amount != 1000 || p.RemovedAt == nil || p.RemovedByUserID == nil || *p.RemovedByUserID != userID.String() ||
		p.RemovalReason == nil || *p.RemovalReason != "Feil beløp" {
		t.Errorf("the removed row = %+v, want %d struck through by %s with the reason trimmed", p, paymentID, userID)
	} else if at, err := time.Parse(time.RFC3339Nano, *p.RemovedAt); err != nil || !at.Equal(h.Now()) {
		t.Errorf("removedAt = %s (%v), want the clock's", *p.RemovedAt, err)
	}

	refusedAs(t, "a second removal", c.Do(http.MethodPost, removalPath(inv.ID, paymentID), map[string]any{"reason": "Igjen"}), "payment_removed")
	if got := registered(t, h, inv.ID, pay(1000, "2026-09-12")); got.State != "paid" || len(got.Payments) != 2 {
		t.Errorf("registered again = %q with %d payments, want paid with both rows", got.State, len(got.Payments))
	}
}

// A registration is a record (D2): direct SQL can neither change nor delete
// one; the removal's three columns are set once and never again; and no row
// can be put under a draft or a credit note.
func TestPayments_ImmutableInSQL(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	paymentID := registered(t, h, inv.ID, pay(100, "2026-09-12")).Payments[0].ID
	ctx := context.Background()
	refused := func(message, sql string, args ...any) {
		t.Helper()
		_, err := h.Pool().Exec(ctx, sql, args...)
		if err == nil || !strings.Contains(err.Error(), message) || !strings.Contains(err.Error(), "P0001") {
			t.Errorf("%s: %v, want P0001 %q", sql, err, message)
		}
	}
	const immutable = "invoices: a payment registration is immutable"
	const needsAnInvoice = "invoices: a payment needs an issued invoice"

	refused(immutable, `UPDATE invoices.payments SET amount = 50 WHERE id = $1`, paymentID)
	refused(immutable, `UPDATE invoices.payments SET reference = 'endret' WHERE id = $1`, paymentID)
	refused(immutable, `DELETE FROM invoices.payments WHERE id = $1`, paymentID)
	h.Exec(t, `UPDATE invoices.payments SET removed_at = now(), removed_by_user_id = $2, removal_reason = 'Feil' WHERE id = $1`, paymentID, uuid.New())
	refused(immutable, `UPDATE invoices.payments SET removed_at = now(), removed_by_user_id = $2, removal_reason = 'Igjen' WHERE id = $1`, paymentID, uuid.New())
	refused(immutable, `UPDATE invoices.payments SET removal_reason = 'Annen grunn' WHERE id = $1`, paymentID)

	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	creditNote := issued(t, h, creditDraft(t, h, inv.ID).ID)
	for _, id := range []int64{draft.ID, creditNote.ID} {
		refused(needsAnInvoice, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at)
			VALUES ($1, '2026-09-12', 1, 'NOK', $2, now())`, id, uuid.New())
	}
}

// raceACreditNote holds a credit note's issue right after it has locked its
// original, starts a registration of amount against that original, waits
// until the registration waits on the original's lock, and releases the
// issue. It answers the registration's response.
func raceACreditNote(t *testing.T, h *harness, original, creditNote int64, amount float64) *modtest.Response {
	t.Helper()
	c := payer(t, h) // signed in here, on the test's goroutine
	var registration *modtest.Response
	done := make(chan struct{})
	var fired atomic.Bool
	restore := invoices.SetCreditIssueAfterOriginalLock(func(_ context.Context, id int64) {
		// The handler's goroutine: t.Errorf, never a t.Fatal.
		if id != original || fired.Swap(true) {
			return
		}
		go func() {
			defer close(done)
			registration = c.Do(http.MethodPost, paymentsPath(original), pay(amount, "2026-09-12"))
		}()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the registration: %v", err)
		}
	})
	defer restore()

	issued(t, h, creditNote)
	<-done
	if !fired.Load() {
		t.Fatal("the credit note's issue never locked the original")
	}
	return registration
}

// A registration racing a credit note's issue on the same invoice waits for
// it — the issue holds the original FOR UPDATE, the registration takes the
// same lock first — and is judged on what the credit note left (D2): a
// credit note of 300 against 1000 leaves 700, so the whole 1000 is
// payment_exceeds_open with openAmount 700 and open stays 700; a payment of
// 400 fits, both commit, and exactly 300 is open. The issue is held on the
// seam right after it has locked the original, so the order is not left to
// the scheduler.
func TestPayments_ARegistrationRacingACreditNoteIssue(t *testing.T) {
	h := readyToIssue(t)
	for _, c := range []struct {
		amount     float64
		code       string
		openAfter  float64
		paidAfter  float64
		stateAfter string
	}{
		{amount: 1000, code: "payment_exceeds_open", openAfter: 700, paidAfter: 0, stateAfter: "open"},
		{amount: 400, openAfter: 300, paidAfter: 400, stateAfter: "partially_paid"},
	} {
		inv := thousand(t, h)
		credit := creditUnits(t, h, inv.ID, 3)
		if credit.GrossTotal != 300 {
			t.Fatalf("the credit note = %v, want 300", credit.GrossTotal)
		}
		res := raceACreditNote(t, h, inv.ID, credit.ID, c.amount)
		what := fmt.Sprintf("a registration of %v racing a credit note of 300", c.amount)
		if c.code != "" {
			p := refusedAs(t, what, res, c.code)
			if p.OpenAmount == nil || *p.OpenAmount != 700 {
				t.Errorf("%s: openAmount = %v, want 700", what, money(p.OpenAmount))
			}
		} else if res.Status != http.StatusOK {
			t.Errorf("%s = %d %s, want 200", what, res.Status, res.Body)
		}
		got := getInvoice(t, h, inv.ID)
		if money(got.OpenAmount) != c.openAfter || money(got.PaidAmount) != c.paidAfter || money(got.CreditedAmount) != 300.0 || got.State != c.stateAfter {
			t.Errorf("%s: then = %q credited %v paid %v open %v, want %s, 300, %v, %v",
				what, got.State, money(got.CreditedAmount), money(got.PaidAmount), money(got.OpenAmount), c.stateAfter, c.paidAfter, c.openAfter)
		}
	}
}

// Two registrations of the whole open amount: the first is held on the seam
// right after it has locked the invoice, the second is started and waits on
// that lock, the first is released — exactly one 200 and one
// invoice_settled, and one live payment (D2).
func TestPayments_TwoRegistrationsOfTheWholeOpenAmount(t *testing.T) {
	h := readyToIssue(t)
	inv := thousand(t, h)
	second := payer(t, h) // on the test's goroutine
	var secondRes *modtest.Response
	done := make(chan struct{})
	var fired atomic.Bool
	restore := invoices.SetPaymentAfterLock(func(_ context.Context, id int64) {
		if id != inv.ID || fired.Swap(true) {
			return
		}
		go func() {
			defer close(done)
			secondRes = second.Do(http.MethodPost, paymentsPath(inv.ID), pay(1000, "2026-09-12"))
		}()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the second registration: %v", err)
		}
	})
	defer restore()

	first := registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	<-done
	if first.State != "paid" {
		t.Errorf("the first registration = %q, want paid", first.State)
	}
	refusedAs(t, "the second registration", secondRes, "invoice_settled")
	if n := livePayments(t, h, inv.ID); n != 1 {
		t.Errorf("live payments = %d, want exactly one", n)
	}
}

// Two removals of one payment: the first is held on the seam right after it
// has locked the invoice, the second is started and waits on that lock — it
// reads the payment only after the lock — and the first is released: one
// 200, one payment_removed, and the first's reason kept (D2).
func TestPayments_TwoRemovalsOfOnePayment(t *testing.T) {
	h := readyToIssue(t)
	inv := thousand(t, h)
	paymentID := registered(t, h, inv.ID, pay(1000, "2026-09-12")).Payments[0].ID
	second := payer(t, h)
	var secondRes *modtest.Response
	done := make(chan struct{})
	var fired atomic.Bool
	restore := invoices.SetPaymentAfterLock(func(_ context.Context, id int64) {
		if id != inv.ID || fired.Swap(true) {
			return
		}
		go func() {
			defer close(done)
			secondRes = second.Do(http.MethodPost, removalPath(inv.ID, paymentID), map[string]any{"reason": "Den andre"})
		}()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the second removal: %v", err)
		}
	})
	defer restore()

	res := payer(t, h).Do(http.MethodPost, removalPath(inv.ID, paymentID), map[string]any{"reason": "Den første"})
	<-done
	if res.Status != http.StatusOK {
		t.Errorf("the first removal = %d %s, want 200", res.Status, res.Body)
	}
	refusedAs(t, "the second removal", secondRes, "payment_removed")
	if got := modtest.One[string](t, h.Harness, `SELECT removal_reason FROM invoices.payments WHERE id = $1`, paymentID); got != "Den første" {
		t.Errorf("the reason = %q, want the first removal's", got)
	}
}

// A payment has no customer gate (D2 step 1): an anonymised customer's
// invoice takes one, and is paid — and neither write reads the directory.
func TestPayments_AnAnonymisedCustomersInvoiceStillTakesAPayment(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) {
		p.Status, p.Archived, p.Name, p.LegalName, p.InvoiceAddress = "archived", true, "Anonymisert", "", nil
	})
	var reads atomic.Int32
	h.customers.afterProfileRead(func(int32) { reads.Add(1) })

	got := registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	if got.State != "paid" {
		t.Errorf("the anonymised customer's invoice = %q, want paid", got.State)
	}
	res := payer(t, h).Do(http.MethodPost, removalPath(inv.ID, got.Payments[0].ID), map[string]any{"reason": "Feil"})
	if res.Status != http.StatusOK {
		t.Errorf("the removal = %d %s, want 200", res.Status, res.Body)
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("billing profile reads = %d, want none", n)
	}
}

// Both writes need invoices:payments (D1): every other permission of the
// module is a 403 from the access layer, and nothing is written or removed.
func TestPayments_BothWritesNeedThePermission(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	paymentID := registered(t, h, inv.ID, pay(100, "2026-09-12")).Payments[0].ID
	c := h.SignIn(t, "invoices:access", "invoices:create", "invoices:issue", "invoices:manage")

	if res := c.Do(http.MethodPost, paymentsPath(inv.ID), pay(100, "2026-09-12")); res.Status != http.StatusForbidden {
		t.Errorf("register without invoices:payments = %d %s, want 403", res.Status, res.Body)
	}
	if res := c.Do(http.MethodPost, removalPath(inv.ID, paymentID), map[string]any{"reason": "Feil"}); res.Status != http.StatusForbidden {
		t.Errorf("remove without invoices:payments = %d %s, want 403", res.Status, res.Body)
	}
	if n := livePayments(t, h, inv.ID); n != 1 {
		t.Errorf("live payments = %d, want the one, untouched", n)
	}
}
