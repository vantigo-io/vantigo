package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The queue's races (invoices payments and reminders design D5, D18) on the
// race kit: a pool of two, every raw lock and probe on a connection of its
// own, a waiter proved by pg_blocking_pids, a lock's mode by the NOWAIT
// probes, every racing request under a deadline, and
// pg_stat_database.deadlocks unchanged.

// startAction posts action on line id with body on its own goroutine under
// a 30-second deadline.
func startAction(c *modtest.Client, id int64, action string, body any) <-chan raceRequest {
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(http.MethodPost, queueActionPath(id, action), body, modtest.Context(ctx))}
	}()
	return done
}

// TestBankQueue_ApplyRacesManualPayment: an apply over two invoices parked
// after its line's lock — holding the line FOR NO KEY UPDATE and no invoice
// — while a manual registration of the lower id's whole open amount goes
// through and commits. Released, the apply locks the invoices, finds the
// lower one settled, and is refused payment_exceeds_open for that
// allocation, rolled back whole: the higher invoice untouched, the line
// still an exception. No deadlock.
func TestBankQueue_ApplyRacesManualPayment(t *testing.T) {
	h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
	low, high := kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	c, manual := importer(t, h), payer(t, h)
	_, ids := camtLines(t, h, c, "RACE-1", noKidEntry(6, 1500, "To fakturaer", "TWO"))
	line := ids["TWO"]
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	hook, parked := parkEach()
	restore := invoices.SetQueueAfterLineLock(hook)
	defer restore()

	applyDone := startAction(c, line, "apply", applyBody(allocate(low, 1000), allocate(high, 500)))
	p := waitParked(t, "the apply", parked)
	if p.id != line {
		t.Fatalf("the parked action holds line %d, want %d", p.id, line)
	}
	if got := heldMode(t, probeConn, "invoices.bank_transactions", "id = $1", line); got != modeNoKeyUpdate {
		t.Errorf("the line is held %q, want FOR NO KEY UPDATE", got)
	}
	for _, inv := range []invoiceJSON{low, high} {
		if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", inv.ID); got != "" {
			t.Errorf("invoice %d is held %q while the apply is parked on its line, want free", inv.ID, got)
		}
	}
	finished(t, "the manual payment", startPayment(manual, low.ID, 1000), http.StatusOK)

	close(p.release)
	r := queueRefused(t, "the apply", finished(t, "the apply", applyDone, http.StatusConflict), "payment_exceeds_open")
	if r.InvoiceID == nil || *r.InvoiceID != low.ID || r.OpenAmount == nil || *r.OpenAmount != 0 {
		t.Errorf("the refusal = invoice %v open %v, want the lower invoice %d with 0 open", r.InvoiceID, money(r.OpenAmount), low.ID)
	}
	if got := livePaymentsOf(t, h, high.ID); len(got) != 0 {
		t.Errorf("the higher invoice's payments = %v, want none — the apply rolled back whole", got)
	}
	if got := livePaymentsOf(t, h, low.ID); !slices.Equal(got, []string{"1000.00 manual"}) {
		t.Errorf("the lower invoice's payments = %v, want the manual one alone", got)
	}
	if got := stateOf(t, h, line); got != "exception no_kid" || len(paymentsFrom(t, h, line)) != 0 {
		t.Errorf("the line = %s, want still an exception with nothing applied", got)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// TestBankQueue_ReversalRacesApply: a handle-reversal and an apply naming
// the same two invoices, each holding its own line, both waiting behind a
// raw lock on the higher invoice — the apply first, then the reversal — so
// neither has touched the lower one (each locks in descending id). Released,
// the apply takes the higher, then the lower, and commits; the reversal
// follows. Both finish with 200, never 40P01, and the deadlocks are
// unchanged. Were either to lock ascending, it would hold the lower while
// waiting on the higher, and the other, holding the higher, would deadlock
// on it.
func TestBankQueue_ReversalRacesApply(t *testing.T) {
	h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
	low, high := kidInvoice(t, h), kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "RACE-2",
		kidEntry(5, 100, *low.Kid, "", "PAY-LOW"),
		kidEntry(5, 100, *high.Kid, "", "PAY-HIGH"),
		reversalEntry(6, 200, "REV"),
		noKidEntry(6, 300, "To fakturaer", "APPLY"),
	)
	pLow, pHigh := paymentOf(t, h, ids["PAY-LOW"]), paymentOf(t, h, ids["PAY-HIGH"])
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	raw := holdRow(t, h, `SELECT 1 FROM invoices.invoices WHERE id = $1 FOR UPDATE`, high.ID)

	applyDone := startAction(c, ids["APPLY"], "apply", applyBody(allocate(low, 100), allocate(high, 200)))
	applyPID := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, applyPID); !slices.Equal(got, []uint32{raw.pid}) {
		t.Errorf("the apply waits on %v, want the raw lock %d", got, raw.pid)
	}
	reversalDone := startAction(c, ids["REV"], "handle-reversal", map[string]any{
		"removePayments": []map[string]any{{"invoiceId": low.ID, "paymentId": pLow}, {"invoiceId": high.ID, "paymentId": pHigh}},
	})
	reversalPID := newWaiter(t, probeConn, applyPID)
	if got := blockersOf(t, probeConn, reversalPID); len(got) == 0 || slices.ContainsFunc(got, func(pid uint32) bool { return pid != raw.pid && pid != applyPID }) {
		t.Errorf("the reversal waits on %v, want the higher invoice's holder or its first waiter", got)
	}
	for _, l := range []int64{ids["APPLY"], ids["REV"]} {
		if got := heldMode(t, probeConn, "invoices.bank_transactions", "id = $1", l); got != modeNoKeyUpdate {
			t.Errorf("line %d is held %q, want FOR NO KEY UPDATE", l, got)
		}
	}
	if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", low.ID); got != "" {
		t.Errorf("the lower invoice is held %q while both wait on the higher, want free — each locks in descending id", got)
	}

	raw.release(t)
	applied := finished(t, "the apply", applyDone, http.StatusOK)
	reversed := finished(t, "the reversal", reversalDone, http.StatusOK)
	var a, r queueLineJSON
	applied.JSON(&a)
	reversed.JSON(&r)
	if a.Status != "resolved" || r.Status != "resolved" {
		t.Errorf("the apply = %s, the reversal = %s; want both resolved", a.Status, r.Status)
	}
	if got := livePaymentsOf(t, h, low.ID); !slices.Equal(got, []string{"100.00 camt054"}) {
		t.Errorf("the lower invoice's payments = %v, want the apply's alone", got)
	}
	if got := livePaymentsOf(t, h, high.ID); !slices.Equal(got, []string{"200.00 camt054"}) {
		t.Errorf("the higher invoice's payments = %v, want the apply's alone", got)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// TestBankQueue_TwoAppliesOfOneLine: two applies of one line, both judged
// on the pool while it is an exception. A parks after the line's lock; B is
// proved waiting on that lock (pg_blocking_pids). Released, A applies and
// commits; B takes the lock, judges the line again — resolved now — and is
// refused bank_transaction_not_open, rolled back. One set of payments, no
// deadlock.
func TestBankQueue_TwoAppliesOfOneLine(t *testing.T) {
	h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "TWICE-APPLY", noKidEntry(6, 300, "Innbetaling", "ONE"))
	line := ids["ONE"]
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	hook, parked := parkEach()
	restore := invoices.SetQueueAfterLineLock(hook)
	defer restore()

	aDone := startAction(c, line, "apply", applyBody(allocate(inv, 300)))
	a := waitParked(t, "apply A", parked)
	aPID := idleInTransaction(t, probeConn)
	bDone := startAction(c, line, "apply", applyBody(allocate(inv, 300)))
	bPID := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, bPID); !slices.Equal(got, []uint32{aPID}) {
		t.Errorf("apply B waits on %v, want apply A %d", got, aPID)
	}
	close(a.release)
	finished(t, "apply A", aDone, http.StatusOK)
	b := waitParked(t, "apply B", parked)
	close(b.release)
	queueRefused(t, "apply B", finished(t, "apply B", bDone, http.StatusConflict), "bank_transaction_not_open")
	if got := paymentsFrom(t, h, line); !slices.Equal(got, []string{paid(inv, "300.00", "camt054", "2026-10-06", "")}) {
		t.Errorf("the line's payments = %v, want apply A's alone", got)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}
