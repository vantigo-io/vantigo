package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The races of a Vipps capture (invoices payments and reminders design D16,
// D18) on the race kit: a pool of two, every raw lock and probe on a
// connection of its own, a wait proved by pg_blocking_pids, every racing
// side under a deadline, both finishing with the named outcome, no
// deadlock. The capture decision and the registration take the attempt,
// then its invoice; a manual payment, a bank match and a credit note's
// issue take the invoice — they serialise on it.

// startClaim runs one payments worker claim on its own goroutine under a
// 30-second deadline.
func startClaim(h *harness) <-chan error {
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ok, err := invoices.NewPaymentWorker(h.Deps()).ProcessOne(ctx)
		if err == nil && !ok {
			err = context.Canceled
		}
		done <- err
	}()
	return done
}

// claimDone waits for a racing claim, failing unless it claimed and ended
// without an error.
func claimDone(t *testing.T, what string, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("%s: %v", what, err)
		}
	case <-time.After(40 * time.Second):
		t.Fatalf("%s never finished", what)
	}
}

// startPaymentToday registers amount against invoice id, paid on the fixed
// clock's day, on its own goroutine under a 30-second deadline.
func startPaymentToday(c *modtest.Client, id int64, amount float64) <-chan raceRequest {
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(http.MethodPost, paymentsPath(id), pay(amount, "2026-09-12"), modtest.Context(ctx))}
	}()
	return done
}

// TestVippsCapture_RacesManualPayment: the decision held after the
// attempt's lock while a manual registration of the whole open amount takes
// the invoice and commits — the decision then sees nothing open and
// cancels. Reversed, a reservation decided first refuses the manual payment
// with the Vipps sentence, and a registration held after the invoice's lock
// makes the manual one wait and then refuses it as settled. One payment
// either way, no deadlock.
func TestVippsCapture_RacesManualPayment(t *testing.T) {
	t.Run("the manual payment first", func(t *testing.T) {
		h := workerReady(t, modtest.WithPoolMaxConns(2))
		inv := issuedAcme(t, h)
		ref := plantReservation(t, h, inv.ID, "authorized", "2500")
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked, release := parkOnce(t)
		defer invoices.SetCaptureAfterAttemptLock(hook)()

		decision := startClaim(h)
		pid := parkedPID(t, "the decision", parked)
		if got := heldMode(t, probeConn, "invoices.payment_attempts", "reference = $1", ref); got != modeNoKeyUpdate {
			t.Errorf("the attempt is held %q, want FOR NO KEY UPDATE", got)
		}
		if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", inv.ID); got != "" {
			t.Errorf("the invoice is held %q before the decision locks it", got)
		}
		manual := startPaymentToday(payer(t, h), inv.ID, 2500)
		finished(t, "the manual payment", manual, http.StatusOK)
		release.Close()
		claimDone(t, "the decision", decision)
		if after := blockersOf(t, probeConn, pid); len(after) != 0 {
			t.Errorf("the decision's backend still waits on %v", after)
		}

		if a := attemptOf(t, h, ref); a.State != "cancelling" || a.Reserved != nil {
			t.Errorf("the decision = %s reserved %s, want cancelling: nothing was open", a.State, text(a.Reserved))
		}
		if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"2500.00 manual"}) {
			t.Errorf("the payments = %v, want the manual one alone", got)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})

	t.Run("the capture first", func(t *testing.T) {
		h := workerReady(t, modtest.WithPoolMaxConns(2))
		inv, ref := approvedAttempt(t, h)
		step(t, h) // authorized
		step(t, h) // the decision: 2500.00 reserved
		p := refusedAs(t, "a manual payment over the reservation", payer(t, h).Do(http.MethodPost, paymentsPath(inv.ID), pay(2500, "2026-09-12")), "invoice_settled")
		if !strings.Contains(p.Detail, "A Vipps payment of 2500.00 is being completed") {
			t.Errorf("the refusal's detail = %q, want the Vipps payment named", p.Detail)
		}
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked, release := parkOnce(t)
		defer invoices.SetRegisterAfterInvoiceLock(hook)()

		capture := startClaim(h)
		pid := parkedPID(t, "the registration", parked)
		if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", inv.ID); got != modeUpdate {
			t.Errorf("the invoice is held %q, want FOR UPDATE", got)
		}
		if got := heldMode(t, probeConn, "invoices.payment_attempts", "reference = $1", ref); got != modeNoKeyUpdate {
			t.Errorf("the attempt is held %q, want FOR NO KEY UPDATE", got)
		}
		manual := startPaymentToday(payer(t, h), inv.ID, 2500)
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{pid}) {
			t.Errorf("the manual payment waits on %v, want the registration %d", got, pid)
		}
		release.Close()
		claimDone(t, "the capture", capture)
		refusedAs(t, "the manual payment", finished(t, "the manual payment", manual, http.StatusConflict), "invoice_settled")
		if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"2500.00 vipps"}) {
			t.Errorf("the payments = %v, want the Vipps one alone", got)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})
}

// TestVippsCapture_TwoAttemptsBothAuthorized: two authorized attempts of one
// invoice — one of 1000.00 made while 1500.00 was paid, one of 2500.00 after
// that payment's removal — both wait on the invoice, held by another
// transaction; released, the decisions serialise and the second counts the
// first's reservation: together they reserve the 2500.00 open, never more.
func TestVippsCapture_TwoAttemptsBothAuthorized(t *testing.T) {
	h := workerReady(t, modtest.WithPoolMaxConns(2))
	inv := issuedAcme(t, h)
	small := plantReservation(t, h, inv.ID, "authorized", "1000")
	big := plantLiveAttempt2(t, h, inv.ID, "2500")
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)

	holder := holdRow(t, h, `SELECT 1 FROM invoices.invoices WHERE id = $1 FOR UPDATE`, inv.ID)
	first, second := startClaim(h), startClaim(h)
	w1 := newWaiter(t, probeConn)
	w2 := newWaiter(t, probeConn, w1)
	// The first waits on the holder; the second on the holder or behind
	// the first in the invoice's queue.
	if got := blockersOf(t, probeConn, w1); !slices.Equal(got, []uint32{holder.pid}) {
		t.Errorf("the first decision waits on %v, want the holder %d", got, holder.pid)
	}
	if got := blockersOf(t, probeConn, w2); len(got) == 0 || slices.ContainsFunc(got, func(pid uint32) bool { return pid != holder.pid && pid != w1 }) {
		t.Errorf("the second decision waits on %v, want the holder %d or the first %d", got, holder.pid, w1)
	}
	holder.release(t)
	claimDone(t, "the first decision", first)
	claimDone(t, "the second decision", second)

	a, b := attemptOf(t, h, small), attemptOf(t, h, big)
	switch {
	case a.State == "capturing" && b.State == "capturing":
		if text(a.Reserved) != "1000.00" || text(b.Reserved) != "1500.00" {
			t.Errorf("reserved %s and %s, want 1000.00 and the rest, 1500.00", text(a.Reserved), text(b.Reserved))
		}
	case b.State == "capturing" && a.State == "cancelling":
		if text(b.Reserved) != "2500.00" {
			t.Errorf("the larger reserved %s, want 2500.00", text(b.Reserved))
		}
	default:
		t.Errorf("the decisions = %s %s and %s %s, want 2500.00 reserved in all", a.State, text(a.Reserved), b.State, text(b.Reserved))
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// plantLiveAttempt2 is a pay-link attempt of invoice id authorized for
// amount and due now, its reference answered.
func plantLiveAttempt2(t *testing.T, h *harness, id int64, amount string) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `
		INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, pay_link_id, return_nonce_sha256,
		    amount, cancel_key, state, created_at, expires_at, authorized_amount, authorized_at, next_poll_at)
		SELECT $1, 'vipps', '123456', 'pay_link', id, repeat('0', 64), $2::numeric, gen_random_uuid(), 'authorized',
		    $3::timestamptz, $3::timestamptz + interval '10 minutes', $2::numeric, $3::timestamptz, $3::timestamptz
		FROM invoices.pay_links WHERE invoice_id = $1 AND revoked_at IS NULL
		RETURNING reference`, id, amount, h.Now())
}

// TestVippsCapture_RacesImport: a bank match holding the invoice makes the
// decision wait; matched for the whole amount, the decision then cancels.
// Reversed, a reservation decided first makes a match of more than the rest
// queue exceeds_open — never a registration over the reservation.
func TestVippsCapture_RacesImport(t *testing.T) {
	t.Run("the match first", func(t *testing.T) {
		h, _ := matchHarness(t, modtest.WithPoolMaxConns(2), modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL))
		putVipps(t, h, vippstestKeys())
		inv := kidInvoice(t, h) // 1000.00
		toMatchDay(h)
		ref := plantReservation(t, h, inv.ID, "authorized", "1000")
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked := parkEach()
		defer invoices.SetMatchAfterInvoiceLock(hook)()

		importDone := startImport(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1000, *inv.Kid, "1")))
		p := waitParked(t, "the match", parked)
		matchPID := idleInTransaction(t, probeConn)
		decision := startClaim(h)
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{matchPID}) {
			t.Errorf("the decision waits on %v, want the match %d", got, matchPID)
		}
		close(p.release)
		finished(t, "the import", importDone, http.StatusCreated)
		claimDone(t, "the decision", decision)
		if a := attemptOf(t, h, ref); a.State != "cancelling" {
			t.Errorf("the decision = %s, want cancelling", a.State)
		}
		if got := livePaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{"1000.00 ocr"}) {
			t.Errorf("the payments = %v, want the match's alone", got)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})

	t.Run("the reservation first", func(t *testing.T) {
		h, _ := matchHarness(t, modtest.WithPoolMaxConns(2), modtest.WithEnv("PUBLIC_BASE_URL", publicBaseURL))
		putVipps(t, h, vippstestKeys())
		inv := kidInvoice(t, h)
		toMatchDay(h)
		ref := plantReservation(t, h, inv.ID, "authorized", "400")
		step(t, h)
		if a := attemptOf(t, h, ref); a.State != "capturing" || text(a.Reserved) != "400.00" {
			t.Fatalf("the decision = %s reserved %s, want 400.00", a.State, text(a.Reserved))
		}
		var r importJSON
		finished(t, "the import", startImport(t, importer(t, h), bankfiletest.OCR("1", kidPay(sellerAccount, 6, 1000, *inv.Kid, "1"))),
			http.StatusCreated).JSON(&r)
		if got := stateOf(t, h, lineID(t, h, r.File.ID, "1")); got != "exception exceeds_open" {
			t.Errorf("the line = %s, want queued exceeds_open", got)
		}
		if got := livePaymentsOf(t, h, inv.ID); len(got) != 0 {
			t.Errorf("the payments = %v, want none", got)
		}
	})
}

// TestVippsCapture_RacesCreditNote: a credit note's issue holding the
// original makes the decision wait; it then reserves only what the credit
// left. A credit issued after the capture leaves a refund due.
func TestVippsCapture_RacesCreditNote(t *testing.T) {
	h := workerReady(t, modtest.WithPoolMaxConns(2))
	inv := issuedAcme(t, h) // 2500.00
	ref := plantReservation(t, h, inv.ID, "authorized", "2500")
	credit := creditUnits(t, h, inv.ID, 1) // 1250.00
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	parked, release := make(chan struct{}, 1), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	defer invoices.SetCreditIssueAfterOriginalLock(func(_ context.Context, id int64) {
		if id != inv.ID {
			return
		}
		parked <- struct{}{}
		select {
		case <-release:
		case <-time.After(30 * time.Second):
		}
	})()

	issue := make(chan raceRequest, 1)
	c := issuer(t, h)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		issue <- raceRequest{c.Do(http.MethodPost, issuePath(credit.ID), map[string]any{}, modtest.Context(ctx))}
	}()
	select {
	case <-parked:
	case <-time.After(10 * time.Second):
		t.Fatal("the credit note's issue never locked the original")
	}
	issuePID := idleInTransaction(t, probeConn)
	decision := startClaim(h)
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{issuePID}) {
		t.Errorf("the decision waits on %v, want the issue %d", got, issuePID)
	}
	close(release)
	finished(t, "the credit note's issue", issue, http.StatusOK)
	claimDone(t, "the decision", decision)
	if a := attemptOf(t, h, ref); a.State != "capturing" || text(a.Reserved) != "1250.00" {
		t.Errorf("the decision = %s reserved %s, want 1250.00, what the credit left", a.State, text(a.Reserved))
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}
