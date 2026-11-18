package invoices_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The customer's credit (invoices phase 6 design I8-I13, plan delivery B-1):
// an overpayment kept by hand and from the queue, a credit note's surplus
// moved, credit applied and refunded, every removal reversing its movement,
// and the lock order every path takes.

func customerCreditPath(customer int32) string {
	return fmt.Sprintf("%s/customers/%d/credit", invoicesPath, customer)
}

func refundsPath(customer int32) string { return customerCreditPath(customer) + "/refunds" }

func refundRemovalPath(customer int32, refund int64) string {
	return fmt.Sprintf("%s/%d/remove", refundsPath(customer), refund)
}

func applyCreditPath(id int64) string { return fmt.Sprintf("%s/%d/apply-credit", invoicesPath, id) }

// creditJSON is a customer's credit as a client reads it.
type creditJSON struct {
	CustomerID int32 `json:"customerId"`
	Balances   []struct {
		Currency string  `json:"currency"`
		Balance  float64 `json:"balance"`
	} `json:"balances"`
	Movements []struct {
		ID                int64   `json:"id"`
		Kind              string  `json:"kind"`
		Amount            float64 `json:"amount"`
		Currency          string  `json:"currency"`
		CreditDay         string  `json:"creditDay"`
		Note              string  `json:"note"`
		InvoiceID         *int64  `json:"invoiceId"`
		InvoiceNumber     *int64  `json:"invoiceNumber"`
		PaymentID         *int64  `json:"paymentId"`
		BankTransactionID *int64  `json:"bankTransactionId"`
		LineRef           *string `json:"lineRef"`
		DocumentID        *int64  `json:"documentId"`
		RefundID          *int64  `json:"refundId"`
		ReversesID        *int64  `json:"reversesId"`
		Reversed          float64 `json:"reversed"`
	} `json:"movements"`
	Refunds []struct {
		ID          int64   `json:"id"`
		Amount      float64 `json:"amount"`
		BankAccount string  `json:"bankAccount"`
		Note        string  `json:"note"`
		RemovedAt   *string `json:"removedAt"`
	} `json:"refunds"`
}

// customerCreditOf is customer's credit as GET answers it.
func customerCreditOf(t *testing.T, h *harness, customer int32) creditJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, customerCreditPath(customer), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", customerCreditPath(customer), res.Status, res.Body)
	}
	var c creditJSON
	res.JSON(&c)
	return c
}

// balanceOf is customer's NOK credit as GET answers it, 0 with none.
func balanceOf(t *testing.T, h *harness, customer int32) float64 {
	t.Helper()
	for _, b := range customerCreditOf(t, h, customer).Balances {
		if b.Currency == "NOK" {
			return b.Balance
		}
	}
	return 0
}

// creditRefusal is a credit path's 409.
type creditRefusal struct {
	Code       string   `json:"code"`
	Detail     string   `json:"detail"`
	Balance    *float64 `json:"balance"`
	OpenAmount *float64 `json:"openAmount"`
}

// refusedCredit asserts res is a 409 with code and answers it.
func refusedCredit(t *testing.T, what string, res *modtest.Response, code string) creditRefusal {
	t.Helper()
	var p creditRefusal
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

// overpaid is a thousand registered 1200 by hand with the rest kept as
// credit: paid, and 200 of the customer's credit.
func overpaid(t *testing.T, h *harness) invoiceJSON {
	t.Helper()
	inv := thousand(t, h)
	body := pay(1200, "2026-09-12")
	body["keepRestAsCredit"], body["reference"], body["note"] = true, "KID 1", "Betalte for mye"
	return registered(t, h, inv.ID, body)
}

// refund is a refund body of amount in NOK from the seller's account.
func refund(amount float64) map[string]any {
	return map[string]any{"refundedOn": "2026-09-12", "amount": amount, "currency": "NOK", "bankAccount": "15032080119",
		"reference": "Tilbakebetaling", "note": "Til Kari"}
}

// I9 by hand: without the flag an amount over what is open is still
// payment_exceeds_open; with it the payment takes what was open and the
// rest is the customer's overpayment, naming the payment and its note. With
// nothing open it is invoice_settled all the same, and a recovery refuses
// the flag.
func TestCredit_AnOverpaymentKeptByHand(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := payer(t, h)
	plain := thousand(t, h)
	refusedAs(t, "1200 without the flag", c.Do(http.MethodPost, paymentsPath(plain.ID), pay(1200, "2026-09-12")), "payment_exceeds_open")

	inv := overpaid(t, h)
	if inv.State != "paid" || money(inv.PaidAmount) != 1000.0 || money(inv.OpenAmount) != 0.0 || len(inv.Payments) != 1 ||
		inv.Payments[0].Amount != 1000 || inv.Payments[0].Reference != "KID 1" {
		t.Fatalf("kept as credit = %q paid %v open %v %+v, want paid 1000 with one payment of 1000",
			inv.State, money(inv.PaidAmount), money(inv.OpenAmount), inv.Payments)
	}
	credit := customerCreditOf(t, h, customerAcme)
	if len(credit.Balances) != 1 || credit.Balances[0].Balance != 200 || len(credit.Movements) != 1 {
		t.Fatalf("the credit = %+v, want 200 in one movement", credit)
	}
	m := credit.Movements[0]
	if m.Kind != "overpayment" || m.Amount != 200 || m.PaymentID == nil || *m.PaymentID != inv.Payments[0].ID ||
		m.InvoiceID == nil || *m.InvoiceID != inv.ID || m.Note != "Betalte for mye" || m.CreditDay != "2026-09-12" {
		t.Errorf("the movement = %+v, want an overpayment of 200 naming the payment and its note", m)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT amount_nok::text FROM invoices.customer_credits WHERE id = $1`, m.ID); got != "200.00" {
		t.Errorf("amount_nok = %s, want the trigger's 200.00", got)
	}

	body := pay(50, "2026-09-12")
	body["keepRestAsCredit"] = true
	refusedAs(t, "a settled invoice with the flag", c.Do(http.MethodPost, paymentsPath(inv.ID), body), "invoice_settled")
	body["kind"] = "recovered"
	invalidOn(t, "a recovery with the flag", c.Do(http.MethodPost, paymentsPath(plain.ID), body), "keepRestAsCredit")
	if n := h.Count(t, `SELECT count(*) FROM invoices.customer_credits`); n != 1 {
		t.Errorf("movements = %d, want the one", n)
	}
}

// I11: credit applied to another invoice is a payment of source credit and
// an applied movement; the refusals are judged under the customer's lock and
// the invoice's.
func TestCredit_AppliedToAnInvoice(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := payer(t, h)
	overpaid(t, h)
	next := thousand(t, h)
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))

	invalidOn(t, "no amount", c.Do(http.MethodPost, applyCreditPath(next.ID), map[string]any{"amount": 0}), "amount")
	invalidOn(t, "a day before the issue", c.Do(http.MethodPost, applyCreditPath(next.ID), map[string]any{"amount": 10, "paidOn": "2026-01-01"}), "paidOn")
	if res := c.Do(http.MethodPost, applyCreditPath(999999), map[string]any{"amount": 10}); res.Status != http.StatusNotFound {
		t.Errorf("no document = %d, want 404", res.Status)
	}
	refusedCredit(t, "a draft", c.Do(http.MethodPost, applyCreditPath(draft.ID), map[string]any{"amount": 10}), "invoice_draft")
	p := refusedCredit(t, "more than the credit", c.Do(http.MethodPost, applyCreditPath(next.ID), map[string]any{"amount": 250}), "credit_exceeds_balance")
	if p.Balance == nil || *p.Balance != 200 {
		t.Errorf("balance = %v, want 200", money(p.Balance))
	}

	res := c.Do(http.MethodPost, applyCreditPath(next.ID), map[string]any{"amount": 150})
	if res.Status != http.StatusOK {
		t.Fatalf("apply 150 = %d %s, want 200", res.Status, res.Body)
	}
	var got invoiceJSON
	res.JSON(&got)
	if got.State != "partially_paid" || money(got.OpenAmount) != 850.0 || len(got.Payments) != 1 || got.Payments[0].Source != "credit" {
		t.Errorf("after 150 = %q open %v %+v, want partially_paid 850 with a payment of source credit", got.State, money(got.OpenAmount), got.Payments)
	}
	if b := balanceOf(t, h, customerAcme); b != 50 {
		t.Errorf("the credit = %v, want 50", b)
	}
	small := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("B", 1, 20, vat25))).ID)
	refusedCredit(t, "more than is open", c.Do(http.MethodPost, applyCreditPath(small.ID), map[string]any{"amount": 30}), "credit_exceeds_open")
	registered(t, h, small.ID, pay(25, "2026-09-12"))
	refusedCredit(t, "a settled invoice", c.Do(http.MethodPost, applyCreditPath(small.ID), map[string]any{"amount": 10}), "invoice_settled")
}

// I12: a refund is recorded under the customer's lock alone, never above
// the credit, NOK only, from an account with a valid check digit; its
// removal gives the credit back once.
func TestCredit_RefundsAndTheirRemoval(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := payer(t, h)
	overpaid(t, h)

	for field, change := range map[string]func(map[string]any){
		"currency":    func(b map[string]any) { b["currency"] = "EUR" },
		"bankAccount": func(b map[string]any) { b["bankAccount"] = "15032080110" },
		"amount":      func(b map[string]any) { b["amount"] = -1 },
		"refundedOn":  func(b map[string]any) { b["refundedOn"] = "2027-01-01" },
	} {
		b := refund(10)
		change(b)
		invalidOn(t, "a refund with a bad "+field, c.Do(http.MethodPost, refundsPath(customerAcme), b), field)
	}
	refusedCredit(t, "more than the credit", c.Do(http.MethodPost, refundsPath(customerAcme), refund(201)), "credit_exceeds_balance")

	res := c.Do(http.MethodPost, refundsPath(customerAcme), refund(120))
	if res.Status != http.StatusOK {
		t.Fatalf("a refund of 120 = %d %s, want 200", res.Status, res.Body)
	}
	var after creditJSON
	res.JSON(&after)
	if len(after.Balances) != 1 || after.Balances[0].Balance != 80 || len(after.Refunds) != 1 || after.Refunds[0].BankAccount != "15032080119" {
		t.Fatalf("after the refund = %+v, want 80 left and the refund", after)
	}
	id := after.Refunds[0].ID
	invalidOn(t, "a removal without a reason", c.Do(http.MethodPost, refundRemovalPath(customerAcme, id), map[string]any{"reason": " "}), "reason")
	if res := c.Do(http.MethodPost, refundRemovalPath(customerPerson, id), map[string]any{"reason": "Feil"}); res.Status != http.StatusNotFound {
		t.Errorf("another customer's refund = %d, want 404", res.Status)
	}
	res = c.Do(http.MethodPost, refundRemovalPath(customerAcme, id), map[string]any{"reason": "Feil"})
	if res.Status != http.StatusOK {
		t.Fatalf("the removal = %d %s, want 200", res.Status, res.Body)
	}
	res.JSON(&after)
	if after.Balances[0].Balance != 200 || after.Refunds[0].RemovedAt == nil {
		t.Errorf("after the removal = %+v, want 200 back and the refund removed", after)
	}
	refusedCredit(t, "a second removal", c.Do(http.MethodPost, refundRemovalPath(customerAcme, id), map[string]any{"reason": "Igjen"}), "refund_removed")
	kinds := []string{}
	for _, m := range after.Movements {
		kinds = append(kinds, m.Kind)
	}
	if !slices.Equal(kinds, []string{"reversed", "refunded", "overpayment"}) {
		t.Errorf("movements = %v, want the reversal, the refund and the overpayment, newest first", kinds)
	}
}

// Removing a payment with credit reverses its movement: an overpayment that
// was used refuses credit_exceeds_balance until what used it is removed; a
// payment of source credit gives its credit back.
func TestCredit_RemovingAPaymentReversesItsCredit(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := payer(t, h)
	inv := overpaid(t, h)
	next := thousand(t, h)
	res := c.Do(http.MethodPost, applyCreditPath(next.ID), map[string]any{"amount": 150})
	if res.Status != http.StatusOK {
		t.Fatalf("apply = %d %s", res.Status, res.Body)
	}
	var applied invoiceJSON
	res.JSON(&applied)

	p := refusedCredit(t, "the overpaid payment while its credit is used",
		c.Do(http.MethodPost, removalPath(inv.ID, inv.Payments[0].ID), map[string]any{"reason": "Feil"}), "credit_exceeds_balance")
	if p.Balance == nil || *p.Balance != 50 {
		t.Errorf("balance = %v, want 50", money(p.Balance))
	}
	if res := c.Do(http.MethodPost, removalPath(next.ID, applied.Payments[0].ID), map[string]any{"reason": "Feil"}); res.Status != http.StatusOK {
		t.Fatalf("the credit payment's removal = %d %s", res.Status, res.Body)
	}
	if b := balanceOf(t, h, customerAcme); b != 200 {
		t.Errorf("after the credit payment's removal = %v, want 200", b)
	}
	if res := c.Do(http.MethodPost, removalPath(inv.ID, inv.Payments[0].ID), map[string]any{"reason": "Feil"}); res.Status != http.StatusOK {
		t.Fatalf("the overpaid payment's removal = %d %s", res.Status, res.Body)
	}
	if b := balanceOf(t, h, customerAcme); b != 0 {
		t.Errorf("after both removals = %v, want 0", b)
	}
}

// I10: a credit note issued after the invoice was paid moves its surplus to
// the customer — the invoice reads 0 open, never below — and the payment's
// removal returns it, as far as the credit and what is open reach. The
// invoice's attention item for a refund due is gone: it is the customer's
// credit now.
func TestCredit_ACreditNotesSurplusMovesAndReturns(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	c := payer(t, h)
	inv := thousand(t, h)
	paid := registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	credit := creditUnits(t, h, inv.ID, 3)
	cn := issued(t, h, credit.ID)

	got := getInvoice(t, h, inv.ID)
	if money(got.OpenAmount) != 0.0 || got.State != "paid" || got.RefundDue != nil {
		t.Errorf("after the credit note = %q open %v refund %v, want paid, 0 open, no refundDue", got.State, money(got.OpenAmount), money(got.RefundDue))
	}
	moves := customerCreditOf(t, h, customerAcme)
	if len(moves.Movements) != 1 || moves.Movements[0].Kind != "credit_note" || moves.Movements[0].Amount != 300 ||
		moves.Movements[0].DocumentID == nil || *moves.Movements[0].DocumentID != cn.ID {
		t.Fatalf("the credit = %+v, want the credit note's 300", moves)
	}
	if settled := modtest.One[string](t, h.Harness, `SELECT invoices.settled_amount($1)::text`, inv.ID); settled != "700.00" {
		t.Errorf("settled_amount = %s, want 1000 paid less 300 moved", settled)
	}

	res := c.Do(http.MethodPost, removalPath(inv.ID, paid.Payments[0].ID), map[string]any{"reason": "Feil"})
	if res.Status != http.StatusOK {
		t.Fatalf("the removal = %d %s", res.Status, res.Body)
	}
	res.JSON(&got)
	if money(got.OpenAmount) != 700.0 || balanceOf(t, h, customerAcme) != 0 {
		t.Errorf("after the removal = open %v credit %v, want 700 open and the credit back on the invoice", money(got.OpenAmount), balanceOf(t, h, customerAcme))
	}
}

// The issue's useCredit applies the customer's credit after the issue — the
// lesser of the credit and the gross — for an issuer who may apply credit;
// one who may not gets the warning and no payment. With no credit it applies
// nothing and warns of nothing.
func TestCredit_UseCreditOnIssue(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	overpaid(t, h)
	issueAndPay := h.SignIn(t, "invoices:access", "invoices:issue", "invoices:payments")
	plain := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	res := issuer(t, h).Do(http.MethodPost, fmt.Sprintf("%s/%d/issue", invoicesPath, plain.ID), map[string]any{"useCredit": true})
	var refused invoiceJSON
	res.JSON(&refused)
	if res.Status != http.StatusOK || len(refused.Payments) != 0 || !slices.Contains(refused.Warnings, "credit_not_applied") {
		t.Errorf("issued by one without invoices:payments = %d %+v %v, want issued, no payment, credit_not_applied",
			res.Status, refused.Payments, refused.Warnings)
	}
	draft := createDraft(t, h, draftBody(customerAcme, line("Konsulenttime", 10, 80, vat25)))
	res = issueAndPay.Do(http.MethodPost, fmt.Sprintf("%s/%d/issue", invoicesPath, draft.ID), map[string]any{"useCredit": true})
	if res.Status != http.StatusOK {
		t.Fatalf("issue with useCredit = %d %s", res.Status, res.Body)
	}
	var got invoiceJSON
	res.JSON(&got)
	if money(got.OpenAmount) != 800.0 || len(got.Payments) != 1 || got.Payments[0].Source != "credit" || slices.Contains(got.Warnings, "credit_not_applied") {
		t.Errorf("issued = open %v %+v %v, want 800 open after 200 of credit", money(got.OpenAmount), got.Payments, got.Warnings)
	}
	other := createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25)))
	res = issueAndPay.Do(http.MethodPost, fmt.Sprintf("%s/%d/issue", invoicesPath, other.ID), map[string]any{"useCredit": true})
	res.JSON(&got)
	if res.Status != http.StatusOK || len(got.Payments) != 0 || slices.Contains(got.Warnings, "credit_not_applied") {
		t.Errorf("issued with no credit = %d %+v %v, want no payment and no warning", res.Status, got.Payments, got.Warnings)
	}
}

// The invoices.credit_moves worker moves the surplus the issue's follow-up
// did not, once, on a pool of one: it holds no connection across a move.
func TestCreditMovesWorker_MovesWhatTheIssueLeftOnAPoolOfOne(t *testing.T) {
	h := readyToIssue(t)
	inv := thousand(t, h)
	registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	credit := creditUnits(t, h, inv.ID, 2)
	restore := invoices.SetCreditMoveAfterIssue(func(context.Context, int64) error { return errors.New("the follow-up failed") })
	issued(t, h, credit.ID)
	restore()
	if got := getInvoice(t, h, inv.ID); money(got.OpenAmount) != -200.0 {
		t.Fatalf("with the follow-up failed = open %v, want -200 until the worker moves it", money(got.OpenAmount))
	}
	w := invoices.NewCreditMovesWorker(poolOfOneDeps(t, h))
	if n, err := w.RunCycle(context.Background()); err != nil || n != 1 {
		t.Fatalf("the cycle = %d, %v; want one move", n, err)
	}
	if n, err := w.RunCycle(context.Background()); err != nil || n != 0 {
		t.Errorf("a second cycle = %d, %v; want nothing pending", n, err)
	}
	if got := getInvoice(t, h, inv.ID); money(got.OpenAmount) != 0.0 || balanceOf(t, h, customerAcme) != 200 {
		t.Errorf("after the worker = open %v credit %v, want 0 and 200", money(got.OpenAmount), balanceOf(t, h, customerAcme))
	}
	if w.Name() != "invoices.credit_moves" || w.Interval().Minutes() != 5 {
		t.Errorf("the worker = %s every %v", w.Name(), w.Interval())
	}
}

// I9 in the queue: a line queued exceeds_open keeps what the invoice had
// open as a payment and the rest as credit naming the line; one queued
// invoice_settled keeps all of it. The line resolves applied with a
// kept_as_credit event, its applied list shows the credit and nothing is
// left unapplied; it is not reopened while the credit stands, and a
// reversal takes the credit back.
func TestBankQueue_KeepAsCredit(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	over, settled := kidInvoice(t, h), kidInvoice(t, h)
	registered(t, h, settled.ID, pay(1000, "2026-09-12"))
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "KEEP-1",
		kidEntry(6, 1300, *over.Kid, "", "OVER"),
		kidEntry(6, 200, *settled.Kid, "", "SETTLED"),
		noKidEntry(6, 50, "Takk", "NOKID"),
		reversalEntry(7, 200, "REV"),
	)
	queueRefused(t, "a no_kid line", c.Do(http.MethodPost, queueActionPath(ids["NOKID"], "keep-as-credit"), map[string]any{}), "bank_transaction_not_applicable")

	l := acted(t, c, ids["OVER"], "keep-as-credit", map[string]any{"note": "Kunden betalte for mye"})
	if l.Status != "resolved" || l.UnappliedAmount != 0 || len(l.Applied) != 2 || l.Events[len(l.Events)-1].Event != "kept_as_credit" {
		t.Fatalf("kept = %s unapplied %v applied %+v, want resolved with a payment and a credit", l.Status, l.UnappliedAmount, l.Applied)
	}
	kinds := map[string]float64{}
	for _, a := range l.Applied {
		kinds[a.Kind] = a.Amount
	}
	if kinds["payment"] != 1000 || kinds["credit"] != 300 {
		t.Errorf("applied = %v, want a payment of 1000 and a credit of 300", kinds)
	}
	s := acted(t, c, ids["SETTLED"], "keep-as-credit", map[string]any{"invoiceId": settled.ID})
	if len(s.Applied) != 1 || s.Applied[0].Kind != "credit" || s.Applied[0].Amount != 200 {
		t.Errorf("the settled line = %+v, want all 200 as credit", s.Applied)
	}
	if b := balanceOf(t, h, customerAcme); b != 500 {
		t.Errorf("the credit = %v, want 500", b)
	}
	queueRefused(t, "a reopen", c.Do(http.MethodPost, queueActionPath(ids["SETTLED"], "reopen"), nil), "bank_transaction_applied")

	credit := s.Applied[0].ID
	r := acted(t, c, ids["REV"], "handle-reversal", map[string]any{"reverseCredits": []map[string]any{{"creditId": credit}}})
	if r.Status != "resolved" {
		t.Errorf("the reversal = %s, want resolved", r.Status)
	}
	again := queueLine(t, h, ids["SETTLED"])
	if !again.Applied[0].Removed || again.UnappliedAmount != 0 {
		t.Errorf("the reversed line = %+v unapplied %v, want its credit removed and nothing to apply again", again.Applied, again.UnappliedAmount)
	}
	if b := balanceOf(t, h, customerAcme); b != 300 {
		t.Errorf("after the reversal = %v, want 300", b)
	}
	queueRefused(t, "a reversal of a credit taken back", c.Do(http.MethodPost, queueActionPath(ids["REV"], "handle-reversal"),
		map[string]any{"reverseCredits": []map[string]any{{"creditId": credit}}}), "bank_transaction_not_open")
}

// Every path that writes credit takes I17a's order through the helpers: a
// bank line first, then the customer, then the invoices; a refund the
// customer alone; a merge the two customers in ascending id before
// anything; an erase the customer first.
func TestCredit_LockOrder(t *testing.T) {
	h, _ := matchHarness(t)
	over := kidInvoice(t, h)
	inv := thousand(t, h)
	next := thousand(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "CREDIT-LOCKS", kidEntry(6, 1300, *over.Kid, "", "OVER"))

	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()
	customer := "customer " + idKey(int64(customerAcme))

	body := pay(1100, "2026-10-06")
	body["keepRestAsCredit"] = true
	registered(t, h, inv.ID, body)
	if got := seen.take(); !slices.Equal(got, []string{customer, "invoice " + idKey(inv.ID)}) {
		t.Errorf("a payment kept as credit = %v, want the customer, then the invoice", got)
	}
	acted(t, c, ids["OVER"], "keep-as-credit", map[string]any{})
	if got := seen.take(); !slices.Equal(got, []string{"bank_transaction " + idKey(ids["OVER"]), customer, "invoice " + idKey(over.ID)}) {
		t.Errorf("keep-as-credit = %v, want the line, the customer, the invoice", got)
	}
	if res := c.Do(http.MethodPost, applyCreditPath(next.ID), map[string]any{"amount": 50}); res.Status != http.StatusOK {
		t.Fatalf("apply = %d %s", res.Status, res.Body)
	}
	if got := seen.take(); !slices.Equal(got, []string{customer, "invoice " + idKey(next.ID)}) {
		t.Errorf("apply-credit = %v, want the customer, then the invoice", got)
	}
	if res := c.Do(http.MethodPost, refundsPath(customerAcme), refund(10)); res.Status != http.StatusOK {
		t.Fatalf("refund = %d %s", res.Status, res.Body)
	}
	if got := seen.take(); !slices.Equal(got, []string{customer}) {
		t.Errorf("a refund = %v, want the customer alone", got)
	}
	merged(t, h, customerAcme, customerNoTerms)
	if got := seen.take(); len(got) < 2 || !slices.Equal(got[:2], []string{customer, "customer " + idKey(int64(customerNoTerms))}) {
		t.Errorf("a merge = %v, want the two customers in ascending id first", got)
	}
	eraseOf(t, h, disabledDeps(h), customerPerson)
	if got := seen.take(); len(got) == 0 || got[0] != "customer "+idKey(int64(customerPerson)) {
		t.Errorf("an erase = %v, want the customer first", got)
	}
}

// The merge re-points the credit and records what moved; the erase blanks
// the notes and keeps the amounts; the export carries the movements.
func TestCredit_TheMergeTheEraseAndTheExport(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	overpaid(t, h)
	if res := payer(t, h).Do(http.MethodPost, refundsPath(customerAcme), refund(20)); res.Status != http.StatusOK {
		t.Fatalf("refund = %d %s", res.Status, res.Body)
	}
	moved := merged(t, h, customerAcme, customerNoTerms)
	if !slices.Contains(moved, contracts.RepointedReferences{Kind: "invoices.customerCredits", Count: 2}) ||
		!slices.Contains(moved, contracts.RepointedReferences{Kind: "invoices.creditRefunds", Count: 1}) {
		t.Errorf("moved = %+v, want the two movements and the refund", moved)
	}
	if b := balanceOf(t, h, customerNoTerms); b != 180 || balanceOf(t, h, customerAcme) != 0 {
		t.Errorf("after the merge = %v and %v, want 180 on the survivor", b, balanceOf(t, h, customerAcme))
	}
	if got := modtest.One[string](t, h.Harness, `SELECT amount::text FROM invoices.credit_merges`); got != "180.00" {
		t.Errorf("the merge's record = %s, want 180.00", got)
	}
	export, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).ExportCustomerData(context.Background(), customerNoTerms)
	if err != nil || !containsText(t, export, "Betalte for mye") {
		t.Errorf("the export = %v, want the movements' notes", err)
	}
	erased := eraseOf(t, h, disabledDeps(h), customerNoTerms)
	if !slices.Contains(erased, contracts.ErasedData{Kind: "invoices.customerCredits", Count: 2}) ||
		!slices.Contains(erased, contracts.ErasedData{Kind: "invoices.creditRefunds", Count: 1}) {
		t.Errorf("erased = %+v, want the two notes and the refund's", erased)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.customer_credits WHERE note <> ''`); n != 0 {
		t.Errorf("%d notes left, want none", n)
	}
	if b := balanceOf(t, h, customerNoTerms); b != 180 {
		t.Errorf("after the erase = %v, want the amounts kept", b)
	}
}

// The ledger's triggers: a movement is never deleted nor changed but for
// the merge's customer and the anonymisation's note; a negative one never
// takes the balance below zero; a reversal mirrors its movement, once; a
// refund changes only by its removal, the merge and the note; and a payment
// of source credit names a user and no bank line.
func TestCredit_TheTriggers(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := overpaid(t, h)
	id := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.customer_credits`)
	ctx := context.Background()
	exec := func(sql string, args ...any) error {
		_, err := h.Pool().Exec(ctx, sql, args...)
		return err
	}
	for what, sql := range map[string]string{
		"a delete":         `DELETE FROM invoices.customer_credits WHERE id = $1`,
		"an amount change": `UPDATE invoices.customer_credits SET amount = 1 WHERE id = $1`,
		"a note changed":   `UPDATE invoices.customer_credits SET note = 'x' WHERE id = $1`,
	} {
		if err := exec(sql, id); err == nil {
			t.Errorf("%s was let through", what)
		}
	}
	if err := exec(`UPDATE invoices.customer_credits SET note = '', customer_id = $2 WHERE id = $1`, id, customerAcme); err != nil {
		t.Errorf("the merge's and the erase's write: %v", err)
	}
	if err := exec(`INSERT INTO invoices.customer_credits (customer_id, currency, amount, kind, credit_day, invoice_id, payment_id,
		registered_by_user_id, registered_at) VALUES ($1, 'NOK', -500, 'applied', DATE '2026-09-12', $2, $3, gen_random_uuid(), now())`,
		customerAcme, inv.ID, inv.Payments[0].ID); err == nil {
		t.Error("a movement below the balance was let through")
	}
	reverse := `INSERT INTO invoices.customer_credits (customer_id, currency, amount, kind, credit_day, invoice_id, reverses_id,
		registered_by_user_id, registered_at) VALUES ($1, 'NOK', $2, 'reversed', DATE '2026-09-12', $3, $4, gen_random_uuid(), now())`
	if err := exec(reverse, customerAcme, 100, inv.ID, id); err == nil {
		t.Error("a reversal of the same sign was let through")
	}
	if err := exec(reverse, customerAcme, -100, inv.ID, id); err == nil {
		t.Error("a part of an overpayment reversed was let through")
	}
	if err := exec(reverse, customerAcme, -200, inv.ID, id); err != nil {
		t.Fatalf("the whole reversal: %v", err)
	}
	if err := exec(reverse, customerAcme, -200, inv.ID, id); err == nil {
		t.Error("a second reversal was let through")
	}
	if err := exec(`INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, source, registered_at)
		VALUES ($1, DATE '2026-09-12', 1, 'NOK', 'credit', now())`, inv.ID); err == nil {
		t.Error("a payment of source credit with no user was let through")
	}
	if got := modtest.One[string](t, h.Harness, `SELECT amount_nok::text FROM invoices.payments WHERE id = $1`, inv.Payments[0].ID); got != "1000.00" {
		t.Errorf("the payment's amount_nok = %s, want its amount", got)
	}
	if err := exec(`UPDATE invoices.payments SET amount_nok = 1 WHERE id = $1`, inv.Payments[0].ID); err == nil {
		t.Error("a payment's amount_nok was changed")
	}
}

// containsText reports whether v, as JSON, holds text.
func containsText(t *testing.T, v any, text string) bool {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return strings.Contains(string(raw), text)
}

// poolOfOneDeps is the harness's Deps on a pool of one connection of its
// own, closed with the test: a worker that held a connection while it asked
// for another would hang on it.
func poolOfOneDeps(t *testing.T, h *harness) module.Deps {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	t.Cleanup(one.Close)
	d := h.Deps()
	d.Pool = one
	return d
}

// The sales journal's credit (I16): a fact per movement and per merge, the
// movement's own NOK, a refund's fact with no invoice, the customer the
// movement belonged to when it was written; CreditsAt answers the balance at
// the end of a day in OpenAt's snapshot; a credit note's move takes the
// invoice out of OpenAt.
func TestSalesJournal_CreditFactsAndCreditsAt(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	ctx := context.Background()
	inv := overpaid(t, h)
	if res := payer(t, h).Do(http.MethodPost, refundsPath(customerAcme), refund(50)); res.Status != http.StatusOK {
		t.Fatalf("refund = %d %s", res.Status, res.Body)
	}
	paid := thousand(t, h)
	registered(t, h, paid.ID, pay(1000, "2026-09-12"))
	issued(t, h, creditUnits(t, h, paid.ID, 1).ID)
	merged(t, h, customerAcme, customerNoTerms)

	facts, err := salesJournal(h).Facts(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var credits []contracts.JournalFact
	for _, f := range facts {
		if f.Kind == contracts.FactCredit || f.Kind == contracts.FactCreditMerged {
			credits = append(credits, f)
		}
	}
	if len(credits) != 4 {
		t.Fatalf("credit facts = %+v, want the overpayment, the refund, the move and the merge", credits)
	}
	over, refunded, moved, merge := credits[0], credits[1], credits[2], credits[3]
	if over.Credit.Kind != contracts.CreditOverpayment || over.AmountNOK != "200.00" || over.Credit.Amount != "200.00" ||
		over.InvoiceID != inv.ID || over.CustomerID != customerAcme || over.Credit.PaymentID != inv.Payments[0].ID {
		t.Errorf("the overpayment's fact = %+v %+v", over, over.Credit)
	}
	if refunded.Credit.Kind != contracts.CreditRefunded || refunded.InvoiceID != 0 || refunded.AmountNOK != "50.00" ||
		refunded.Credit.Amount != "-50.00" || refunded.Credit.RefundBankAccount != "15032080119" || refunded.CustomerID != customerAcme {
		t.Errorf("the refund's fact = %+v %+v", refunded, refunded.Credit)
	}
	if moved.Credit.Kind != contracts.CreditNoteMoved || moved.InvoiceID != paid.ID || moved.AmountNOK != "100.00" {
		t.Errorf("the move's fact = %+v %+v", moved, moved.Credit)
	}
	if merge.Kind != contracts.FactCreditMerged || merge.CustomerID != customerNoTerms || merge.Credit.FromCustomerID != customerAcme ||
		merge.AmountNOK != "250.00" || merge.InvoiceID != 0 {
		t.Errorf("the merge's fact = %+v %+v", merge, merge.Credit)
	}
	for _, f := range facts {
		if f.Kind == contracts.FactPayment && f.SubjectID == inv.Payments[0].ID && f.AmountNOK != "1000.00" {
			t.Errorf("the payment's fact = %s, want its amount_nok", f.AmountNOK)
		}
	}

	page, err := salesJournal(h).CreditsAt(ctx, h.Now(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Credits) != 1 || page.Credits[0].CustomerID != customerNoTerms || page.Credits[0].BalanceNOK != "250.00" ||
		page.AsOfSeq != facts[len(facts)-1].Seq {
		t.Errorf("CreditsAt = %+v, want the survivor's 250 as of the last fact", page)
	}
	open, err := salesJournal(h).OpenAt(ctx, h.Now(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range open.Documents {
		if d.ID == paid.ID {
			t.Errorf("OpenAt = %+v, want the credited invoice out of it once its surplus moved", d)
		}
	}
}
