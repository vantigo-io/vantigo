package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The match's races (invoices payments and reminders design D4, D18) on the
// race kit: a pool of two, every probe on a connection of its own, a waiter
// proved by pg_blocking_pids, a lock's mode by the NOWAIT probes, every
// racing request under a deadline, and pg_stat_database.deadlocks unchanged.

// startPayment registers amount against invoice id on its own goroutine
// under a 30-second deadline.
func startPayment(c *modtest.Client, id int64, amount float64) <-chan raceRequest {
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(http.MethodPost, paymentsPath(id), pay(amount, "2026-10-07"), modtest.Context(ctx))}
	}()
	return done
}

// parkedAt is a request parked on a seam, and the channel that lets it go.
type parkedAt struct {
	id      int64
	release chan struct{}
}

// parkEach answers a hook that parks every call until its release is closed,
// reporting each on the channel it answers.
func parkEach() (func(context.Context, int64) error, <-chan parkedAt) {
	parked := make(chan parkedAt, 4)
	return func(_ context.Context, id int64) error {
		p := parkedAt{id: id, release: make(chan struct{})}
		parked <- p
		select {
		case <-p.release:
		case <-time.After(30 * time.Second):
		}
		return nil
	}, parked
}

// waitParked answers the next parked request, failing the test after 10 s.
func waitParked(t *testing.T, what string, parked <-chan parkedAt) parkedAt {
	t.Helper()
	select {
	case p := <-parked:
		return p
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never reached the seam", what)
		return parkedAt{}
	}
}

// TestBankImport_RacesManualPayment: an import's match and a manual
// registration of the whole open amount on one invoice (D4's race). The
// manual one held after its lock: the match waits on the invoice, then
// finds it settled and queues invoice_settled. Reversed — the match held
// after the invoice's lock, holding the line FOR NO KEY UPDATE and the
// invoice FOR UPDATE — the manual one waits on the invoice and is then
// refused invoice_settled. Never two payments, never a deadlock.
func TestBankImport_RacesManualPayment(t *testing.T) {
	t.Run("the manual payment first", func(t *testing.T) {
		h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
		inv := kidInvoice(t, h)
		toMatchDay(h)
		c, manual := importer(t, h), payer(t, h)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		parked, release := make(chan struct{}, 1), make(chan struct{})
		var first atomic.Bool
		restore := invoices.SetPaymentAfterLock(func(context.Context, int64) {
			if first.Swap(true) {
				return
			}
			parked <- struct{}{}
			select {
			case <-release:
			case <-time.After(30 * time.Second):
			}
		})
		defer restore()

		manualDone := startPayment(manual, inv.ID, 1000)
		select {
		case <-parked:
		case <-time.After(10 * time.Second):
			t.Fatal("the manual payment never took its lock")
		}
		manualPID := idleInTransaction(t, probeConn)
		importDone := startImport(t, c, bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1000, *inv.Kid, "1")))
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{manualPID}) {
			t.Errorf("the match waits on %v, want the manual payment %d", got, manualPID)
		}
		close(release)
		finished(t, "the manual payment", manualDone, http.StatusOK)
		var r importJSON
		finished(t, "the import", importDone, http.StatusCreated).JSON(&r)
		if got := stateOf(t, h, lineID(t, h, r.File.ID, "1")); got != "exception invoice_settled" || r.Exceptions != 1 || r.Matched != 0 {
			t.Errorf("the line = %s, the import %+v; want it queued invoice_settled", got, r)
		}
		if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 manual"}) {
			t.Errorf("the invoice's payments = %v, want the manual one alone", got)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})

	t.Run("the match first", func(t *testing.T) {
		h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
		inv := kidInvoice(t, h)
		toMatchDay(h)
		c, manual := importer(t, h), payer(t, h)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked := parkEach()
		restore := invoices.SetMatchAfterInvoiceLock(hook)
		defer restore()

		importDone := startImport(t, c, bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1000, *inv.Kid, "1")))
		p := waitParked(t, "the match", parked)
		matchPID := idleInTransaction(t, probeConn)
		if got := heldMode(t, probeConn, "invoices.bank_transactions", "id = $1", p.id); got != modeNoKeyUpdate {
			t.Errorf("the line is held %q, want FOR NO KEY UPDATE", got)
		}
		if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", inv.ID); got != modeUpdate {
			t.Errorf("the invoice is held %q, want FOR UPDATE", got)
		}
		manualDone := startPayment(manual, inv.ID, 1000)
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{matchPID}) {
			t.Errorf("the manual payment waits on %v, want the match %d", got, matchPID)
		}
		close(p.release)
		var r importJSON
		finished(t, "the import", importDone, http.StatusCreated).JSON(&r)
		if r.Matched != 1 {
			t.Errorf("the import = %+v, want its line matched", r)
		}
		refusedAs(t, "the manual payment", finished(t, "the manual payment", manualDone, http.StatusConflict), "invoice_settled")
		if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 ocr"}) {
			t.Errorf("the invoice's payments = %v, want the match's alone", got)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})
}

// TestBankImport_SamePaymentTwoFilesRace (D4 as amended, I2): two camt.054
// files of one payment for one account — an intraday and an end-of-day
// notification, with different MsgId and AcctSvcrRef, so neither the file's
// keys nor the fingerprint join them. Each import classifies its line on the
// pool while the other has registered nothing, and parks after its line's
// lock; the first, released, matches and commits; the second, released,
// reads the soft key again under the invoice's lock, sees the first's
// payment and queues possible_duplicate. One payment, no deadlock.
func TestBankImport_SamePaymentTwoFilesRace(t *testing.T) {
	h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	hook, parked := parkEach()
	restore := invoices.SetMatchAfterLineLock(hook)
	defer restore()

	intradayDone := startImport(t, c, camtFile("INTRADAY-0617", sellerAccount, camtPay(6, 400, *inv.Kid, "SVC-INTRADAY")))
	intraday := waitParked(t, "the intraday import", parked)
	endOfDayDone := startImport(t, c, camtFile("EOD-0617", sellerAccount, camtPay(6, 400, *inv.Kid, "SVC-EOD")))
	endOfDay := waitParked(t, "the end-of-day import", parked)
	for _, l := range []int64{intraday.id, endOfDay.id} {
		if got := heldMode(t, probeConn, "invoices.bank_transactions", "id = $1", l); got != modeNoKeyUpdate {
			t.Errorf("line %d is held %q, want FOR NO KEY UPDATE", l, got)
		}
	}
	if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", inv.ID); got != "" {
		t.Errorf("the invoice is held %q before either match goes on, want free", got)
	}

	close(intraday.release)
	var first, second importJSON
	finished(t, "the intraday import", intradayDone, http.StatusCreated).JSON(&first)
	close(endOfDay.release)
	finished(t, "the end-of-day import", endOfDayDone, http.StatusCreated).JSON(&second)
	if first.Matched != 1 || second.Exceptions != 1 || second.Matched != 0 {
		t.Errorf("the intraday import = %+v, the end-of-day one %+v; want the first matched and the second queued", first, second)
	}
	if got := stateOf(t, h, endOfDay.id); got != "exception possible_duplicate" {
		t.Errorf("the end-of-day line = %s, want possible_duplicate by the soft key under the lock", got)
	}
	if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"400.00 camt054"}) {
		t.Errorf("the invoice's payments = %v, want one", got)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}
