package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A run's item and a payment race on the invoice (D10, D18), both ways, on a
// pool of two.
//
// The item first: parked on SetRunItemAfterLock it holds the invoice FOR
// UPDATE — proved by the NOWAIT probes — and a registration of the whole
// open amount waits on it (pg_blocking_pids names the run); released, the
// run writes its letter and the payment then registers: the letter was made
// for an invoice open when it was judged.
//
// The payment first: parked on SetPaymentAfterLock it holds the invoice, and
// the run's item waits on it; released, the payment settles the invoice and
// the item, judging again under its lock with every figure read after it, is
// skipped action_changed — no letter for a settled invoice. Judging under
// the lock with the pool pre-pass's figures makes that letter.
//
// Never 40P01, and pg_stat_database.deadlocks unchanged. Not parallel: the
// seams are the package's.
func TestReminderRun_RacesPayment(t *testing.T) {
	h := raceHarness(t)
	remindersOn(t, h, "")
	runFirst := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	paymentFirst := deliveredOn(t, h, 2, customerAcme, "2026-08-03")
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	noDeadlock := func(t *testing.T) {
		t.Helper()
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("deadlocks = %d, was %d: Postgres broke a deadlock", after, before)
		}
	}

	t.Run("the item first", func(t *testing.T) {
		t.Cleanup(func() { noDeadlock(t) })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		parked, release := make(chan struct{}), make(chan struct{})
		defer invoices.SetRunItemAfterLock(func(ctx context.Context, invoiceID int64) error {
			close(parked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})()
		ran := make(chan *modtest.Response, 1)
		runner := payer(t, h)
		go func() { ran <- run(runner, runBody(yes, item(runFirst, "reminder")), modtest.Context(ctx)) }()
		select {
		case <-parked:
		case <-ctx.Done():
			t.Fatal("the run never reached the seam")
		}
		item := idleInTransaction(t, probeConn)
		if mode := heldMode(t, probeConn, "invoices.invoices", "id = $1", runFirst); mode != modeUpdate {
			t.Errorf("the run's item holds the invoice %q, want FOR UPDATE", mode)
		}
		paid := make(chan *modtest.Response, 1)
		client := payer(t, h)
		go func() {
			paid <- client.Do(http.MethodPost, paymentsPath(runFirst), pay(1000, "2026-09-12"), modtest.Context(ctx))
		}()
		payment := newWaiter(t, probeConn)
		if got, want := blockersOf(t, probeConn, payment), []uint32{item}; !slices.Equal(got, want) {
			t.Errorf("pg_blocking_pids(the payment) = %v, want the run's item %v", got, want)
		}
		close(release)
		r := made(t, "the run", <-ran)
		if len(r.Created) != 1 || len(r.Skipped) != 0 {
			t.Errorf("the run = %+v, want the letter made", r)
		}
		if res := <-paid; res.Status != http.StatusOK {
			t.Errorf("the payment = %d %s, want 200 after the run", res.Status, res.Body)
		}
	})

	t.Run("the payment first", func(t *testing.T) {
		t.Cleanup(func() { noDeadlock(t) })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		parked, release := make(chan struct{}), make(chan struct{})
		defer invoices.SetPaymentAfterLock(func(ctx context.Context, invoiceID int64) {
			close(parked)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})()
		paid := make(chan *modtest.Response, 1)
		client := payer(t, h)
		go func() {
			paid <- client.Do(http.MethodPost, paymentsPath(paymentFirst), pay(1000, "2026-09-12"), modtest.Context(ctx))
		}()
		select {
		case <-parked:
		case <-ctx.Done():
			t.Fatal("the payment never reached the seam")
		}
		payment := idleInTransaction(t, probeConn)
		ran := make(chan *modtest.Response, 1)
		runner := payer(t, h)
		go func() { ran <- run(runner, runBody(yes, item(paymentFirst, "reminder")), modtest.Context(ctx)) }()
		item := newWaiter(t, probeConn)
		if got, want := blockersOf(t, probeConn, item), []uint32{payment}; !slices.Equal(got, want) {
			t.Errorf("pg_blocking_pids(the run's item) = %v, want the payment %v", got, want)
		}
		close(release)
		if res := <-paid; res.Status != http.StatusOK {
			t.Errorf("the payment = %d %s, want 200", res.Status, res.Body)
		}
		r := made(t, "the run", <-ran)
		if want := []runSkipJSON{{paymentFirst, "action_changed"}}; len(r.Created) != 0 || !slices.Equal(r.Skipped, want) {
			t.Errorf("the run = created %+v, skipped %+v; want none, and %+v — the invoice was settled", r.Created, r.Skipped, want)
		}
	})

	if n := letterRows(t, h, paymentFirst); n != 0 {
		t.Errorf("letters of the settled invoice = %d, want none", n)
	}
	if n := letterRows(t, h, runFirst); n != 1 {
		t.Errorf("letters of the invoice the run judged first = %d, want one", n)
	}
}

// Two runs over one invoice serialise on it (D10): the first, parked after
// its lock, holds the invoice; the second's item waits on it
// (pg_blocking_pids names the first); released, the first writes its
// letter, and the second, judging after the lock, sees it in flight —
// letter_pending — and is skipped action_changed. One letter;
// uq_reminders_invoice_sequence is never reached; never 40P01.
func TestReminderRun_TwoRuns(t *testing.T) {
	h := raceHarness(t)
	remindersOn(t, h, "")
	id := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var calls atomic.Int32
	parked, release := make(chan struct{}), make(chan struct{})
	defer invoices.SetRunItemAfterLock(func(ctx context.Context, invoiceID int64) error {
		if calls.Add(1) > 1 {
			return nil
		}
		close(parked)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})()

	first := make(chan *modtest.Response, 1)
	c1 := payer(t, h)
	go func() { first <- run(c1, runBody(yes, item(id, "reminder")), modtest.Context(ctx)) }()
	select {
	case <-parked:
	case <-ctx.Done():
		t.Fatal("the first run never reached the seam")
	}
	holder := idleInTransaction(t, probeConn)
	second := make(chan *modtest.Response, 1)
	c2 := payer(t, h)
	go func() { second <- run(c2, runBody(yes, item(id, "reminder")), modtest.Context(ctx)) }()
	waiter := newWaiter(t, probeConn)
	if got, want := blockersOf(t, probeConn, waiter), []uint32{holder}; !slices.Equal(got, want) {
		t.Errorf("pg_blocking_pids(the second run) = %v, want the first %v", got, want)
	}
	close(release)
	if r := made(t, "the first run", <-first); len(r.Created) != 1 {
		t.Errorf("the first run = %+v, want the letter", r)
	}
	r := made(t, "the second run", <-second)
	if want := []runSkipJSON{{id, "action_changed"}}; len(r.Created) != 0 || !slices.Equal(r.Skipped, want) {
		t.Errorf("the second run = created %+v, skipped %+v; want %+v", r.Created, r.Skipped, want)
	}
	if n := letterRows(t, h, id); n != 1 {
		t.Errorf("letters = %d, want one", n)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks = %d, was %d: Postgres broke a deadlock", after, before)
	}
}
