package invoices_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The races of the customer's credit (invoices phase 6 design I17a, as the
// plan's reading 1 amends it): every path that writes credit takes the bank
// line, then the customer, then the invoices, and the merge and the erase
// take the customer before the documents — so each pair below serialises on
// the customer's lock and neither deadlocks. One side is held on the seam
// right after its locks, the other is started and seen waiting on a lock,
// the first is released, and both end as the order says. A test here never
// runs in parallel: the seam is the package's.

// holdCreditPath installs the seam so the first credit path that locks
// invoice id (0 for a refund) starts other on its own goroutine and waits
// until a session waits on a lock before it goes on. It answers the
// function that removes the seam, and whether the seam fired.
func holdCreditPath(t *testing.T, h *harness, id int64, other func()) (func(), *atomic.Bool) {
	t.Helper()
	var fired atomic.Bool
	restore := invoices.SetCreditAfterLocks(func(_ context.Context, invoiceID int64) error {
		if invoiceID != id || fired.Swap(true) {
			return nil
		}
		go other()
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the second path never waited: %v", err)
		}
		return nil
	})
	return restore, &fired
}

// within waits for done, failing after 30 seconds.
func within(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s never finished", what)
	}
}

// Apply-credit against a refund of the same customer: the refund waits on
// the customer's lock and is judged on what the credit applied left.
func TestCreditRace_ApplyAgainstARefund(t *testing.T) {
	h := readyToIssue(t)
	overpaid(t, h)
	next := thousand(t, h)
	c := payer(t, h)
	var refundRes *modtest.Response
	done := make(chan struct{})
	restore, fired := holdCreditPath(t, h, next.ID, func() {
		defer close(done)
		refundRes = c.Do(http.MethodPost, refundsPath(customerAcme), refund(100))
	})
	defer restore()
	if res := c.Do(http.MethodPost, applyCreditPath(next.ID), map[string]any{"amount": 150}); res.Status != http.StatusOK {
		t.Fatalf("apply = %d %s", res.Status, res.Body)
	}
	if !fired.Load() {
		t.Fatal("the seam never fired")
	}
	within(t, "the refund", done)
	refusedCredit(t, "the refund behind the apply", refundRes, "credit_exceeds_balance")
	if b := balanceOf(t, h, customerAcme); b != 50 {
		t.Errorf("the credit = %v, want 50", b)
	}
}

// startMerge runs the merge slot from → into on its own goroutine, in a
// transaction it commits.
func startMerge(h *harness, from, into int32, done chan<- error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tx, err := h.Pool().Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := invoices.Module().CustomerReferences(disabledDeps(h)).RepointCustomer(ctx, tx, from, into); err != nil {
			done <- err
			return
		}
		done <- tx.Commit(ctx)
	}()
}

// Keep-as-credit against a merge of the line's customer: the merge waits on
// the customer's lock, then moves the credit just kept.
func TestCreditRace_KeepAsCreditAgainstAMerge(t *testing.T) {
	h, _ := matchHarness(t)
	over := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "RACE-KEEP", kidEntry(6, 1300, *over.Kid, "", "OVER"))
	mergeDone := make(chan error, 1)
	restore, fired := holdCreditPath(t, h, over.ID, func() { startMerge(h, customerAcme, customerNoTerms, mergeDone) })
	defer restore()
	acted(t, c, ids["OVER"], "keep-as-credit", map[string]any{})
	if !fired.Load() {
		t.Fatal("the seam never fired")
	}
	select {
	case err := <-mergeDone:
		if err != nil {
			t.Fatalf("the merge: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the merge never finished")
	}
	if a, b := balanceOf(t, h, customerAcme), balanceOf(t, h, customerNoTerms); a != 0 || b != 300 {
		t.Errorf("after both = %v and %v, want the 300 moved to the survivor", a, b)
	}
}

// A merge against apply-credit — the deadlock the plan's reading 1 removes:
// the merge holds both customers' locks and the documents; apply-credit
// waits on the customer's lock, never holding the invoice, and once the
// merge commits it reads the invoice's new customer and runs again with it.
func TestCreditRace_AMergeAgainstApplyCredit(t *testing.T) {
	h := readyToIssue(t)
	overpaid(t, h)
	next := thousand(t, h)
	c := payer(t, h)
	conn := ownConn(t, h)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := invoices.Module().CustomerReferences(disabledDeps(h)).RepointCustomer(ctx, tx, customerAcme, customerNoTerms); err != nil {
		t.Fatalf("the merge: %v", err)
	}
	var res *modtest.Response
	done := make(chan struct{})
	go func() {
		defer close(done)
		res = c.Do(http.MethodPost, applyCreditPath(next.ID), map[string]any{"amount": 150})
	}()
	if err := awaitLockWaiter(h); err != nil {
		t.Fatalf("apply-credit never waited: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the merge: %v", err)
	}
	within(t, "apply-credit", done)
	if res.Status != http.StatusOK {
		t.Fatalf("apply-credit behind the merge = %d %s, want 200", res.Status, res.Body)
	}
	if b := balanceOf(t, h, customerNoTerms); b != 50 {
		t.Errorf("the survivor's credit = %v, want 50", b)
	}
	var deadlocks int64
	if err := h.Pool().QueryRow(ctx, `SELECT coalesce(deadlocks, 0) FROM pg_stat_database WHERE datname = current_database()`).Scan(&deadlocks); err != nil || deadlocks != 0 {
		t.Errorf("deadlocks = %d (%v), want none", deadlocks, err)
	}
}

// A credit note's issue, and its surplus's move, against a payment's
// removal that returns moved credit: the issue waits on the invoice the
// removal holds, and its move is judged on what the removal left.
func TestCreditRace_TheMoveAgainstAPaymentRemoval(t *testing.T) {
	h := readyToIssue(t)
	inv := thousand(t, h)
	paid := registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	issued(t, h, creditUnits(t, h, inv.ID, 3).ID)
	second := creditUnits(t, h, inv.ID, 1)
	var issueRes *modtest.Response
	done := make(chan struct{})
	ic := issuer(t, h)
	restore, fired := holdCreditPath(t, h, inv.ID, func() {
		defer close(done)
		issueRes = ic.Do(http.MethodPost, invoicesPath+"/"+idKey(second.ID)+"/issue", map[string]any{})
	})
	defer restore()
	if res := payer(t, h).Do(http.MethodPost, removalPath(inv.ID, paid.Payments[0].ID), map[string]any{"reason": "Feil"}); res.Status != http.StatusOK {
		t.Fatalf("the removal = %d %s", res.Status, res.Body)
	}
	if !fired.Load() {
		t.Fatal("the seam never fired")
	}
	within(t, "the issue", done)
	if issueRes.Status != http.StatusOK {
		t.Fatalf("the issue = %d %s", issueRes.Status, issueRes.Body)
	}
	if got := getInvoice(t, h, inv.ID); money(got.OpenAmount) != 600.0 || balanceOf(t, h, customerAcme) != 0 {
		t.Errorf("after both = open %v credit %v, want 600 open and no credit", money(got.OpenAmount), balanceOf(t, h, customerAcme))
	}
}

// The erase against a refund's note: the erase waits on the customer's
// lock the refund holds, then blanks the note the refund wrote.
func TestCreditRace_TheEraseAgainstARefundsNote(t *testing.T) {
	h := readyToIssue(t)
	overpaid(t, h)
	eraseDone := make(chan error, 1)
	restore, fired := holdCreditPath(t, h, 0, func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := pgx.BeginFunc(ctx, h.Pool(), func(tx pgx.Tx) error {
				_, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).EraseCustomerData(ctx, tx, customerAcme)
				return err
			})
			eraseDone <- err
		}()
	})
	defer restore()
	if res := payer(t, h).Do(http.MethodPost, refundsPath(customerAcme), refund(20)); res.Status != http.StatusOK {
		t.Fatalf("the refund = %d %s", res.Status, res.Body)
	}
	if !fired.Load() {
		t.Fatal("the seam never fired")
	}
	select {
	case err := <-eraseDone:
		if err != nil {
			t.Fatalf("the erase: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the erase never finished")
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.credit_refunds WHERE note <> ''`) +
		h.Count(t, `SELECT count(*) FROM invoices.customer_credits WHERE note <> ''`); n != 0 {
		t.Errorf("%d notes left after the erase, want none", n)
	}
}

// A reversal of a payment with credit kept beside it against a refund of
// that credit: the reversal takes the customer's lock before the invoice,
// the refund waits on it and is judged on what the reversal left — nothing.
func TestCreditRace_AReversalAgainstARefund(t *testing.T) {
	h, _ := matchHarness(t)
	kept := kidInvoice(t, h)
	body := pay(1500, "2026-09-12")
	body["keepRestAsCredit"] = true
	paid := registered(t, h, kept.ID, body)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "RACE-REV", reversalEntry(7, 1500, "REV"))
	var refundRes *modtest.Response
	done := make(chan struct{})
	restore, fired := holdCreditPath(t, h, kept.ID, func() {
		defer close(done)
		refundRes = c.Do(http.MethodPost, refundsPath(customerAcme), refund(100))
	})
	defer restore()
	acted(t, c, ids["REV"], "handle-reversal", reversalOf(kept.ID, paid.Payments[0].ID))
	if !fired.Load() {
		t.Fatal("the seam never fired")
	}
	within(t, "the refund", done)
	refusedCredit(t, "the refund behind the reversal", refundRes, "credit_exceeds_balance")
	if b := balanceOf(t, h, customerAcme); b != 0 {
		t.Errorf("the credit = %v, want 0", b)
	}
}
