package invoices_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The dispatch's races (invoices payments and reminders design D10, D18) on
// the race kit: a pool of two, every probe on a connection of its own, a
// waiter proved by pg_blocking_pids, a lock's mode by the NOWAIT probes,
// every racing request under a deadline, and pg_stat_database.deadlocks
// unchanged. The dispatch takes the invoice, then the letter; a hold, a
// hand-off and a match take the invoice first too, and a withdrawal the
// letter alone, so none can wait in a cycle with it.

// startRequest makes one request as c on its own goroutine under a
// 30-second deadline.
func startRequest(c *modtest.Client, method, path string, body any) <-chan raceRequest {
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(method, path, body, modtest.Context(ctx))}
	}()
	return done
}

// parkFirstLock installs the lock-order seam so the first lock of what
// noted — and only the first — parks until release is closed, reporting it
// on parked: a request held right after it took that lock, before anything
// else.
func parkFirstLock(t *testing.T, what string) (parked <-chan struct{}, release chan<- struct{}) {
	t.Helper()
	p, r := make(chan struct{}, 1), make(chan struct{})
	var first atomic.Bool
	restore := invoices.SetLockTaken(func(_ context.Context, w, _ string) {
		if w != what || first.Swap(true) {
			return
		}
		p <- struct{}{}
		select {
		case <-r:
		case <-time.After(30 * time.Second):
		}
	})
	t.Cleanup(restore)
	return p, r
}

// sendAfter holds every send until done is closed: the mail leaves only
// once the racing request has answered.
func sendAfter(m *letterMail, done <-chan struct{}) {
	m.holdWith(func(context.Context, mail.Outbound) {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
		}
	})
}

// letterWaivers is letter id's waivers as "kind reason".
func letterWaivers(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return texts(t, h, `SELECT kind || ' ' || reason FROM invoices.charge_waivers WHERE reminder_id = $1 ORDER BY id`, id)
}

// TestReminderDispatch_RacesHold: a hold and a dispatch of the invoice's
// letter, which claims the fee 38. The dispatch held after its invoice lock
// (FOR UPDATE, the letter not yet locked): the hold waits on the invoice;
// the dispatch writes the facts and commits; the hold then finds the letter
// being sent and leaves it, named in lettersLeft (plan reading 45); the
// letter is mailed and marked sent under the hold, so its fee is waived
// claimed_in_error in that transaction (Task 11's review) — and a barring
// lift afterwards finds it waived and does not waive it twice. Reversed — the
// hold held after its invoice lock — the dispatch's claim waits on the
// invoice, the hold withdraws the letter on_hold (it is claimed, but carries
// no facts: not being sent), and the dispatch's re-read finds it withdrawn
// and mails nothing.
func TestReminderDispatch_RacesHold(t *testing.T) {
	t.Run("the dispatch first", func(t *testing.T) {
		h, mails := workerHarness(t, "", modtest.WithPoolMaxConns(2))
		id, letter := dueLetter(t, h, 1)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked := parkEach()
		defer invoices.SetDispatchAfterLock(hook)()
		holdAnswered := make(chan struct{})
		sendAfter(mails, holdAnswered)

		done := claimed(invoices.NewReminderWorker(h.Deps()))
		p := waitParked(t, "the dispatch", parked)
		dispatchPID := idleInTransaction(t, probeConn)
		if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", id); got != modeUpdate {
			t.Errorf("the invoice is held %q, want FOR UPDATE", got)
		}
		if got := heldMode(t, probeConn, "invoices.reminders", "id = $1", letter); got != "" {
			t.Errorf("the letter is held %q before the dispatch locks it, want free", got)
		}
		holdDone := startRequest(payer(t, h), http.MethodPost, holdPath(id), holdNote("Kunden bestrider timene"))
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{dispatchPID}) {
			t.Errorf("the hold waits on %v, want the dispatch %d", got, dispatchPID)
		}
		close(p.release)
		held := answered(t, "the hold", finished(t, "the hold", holdDone, http.StatusOK))
		close(holdAnswered)
		finishedClaim(t, done)
		if len(held.LettersLeft) != 1 || held.LettersLeft[0].ReminderID != letter || held.LettersLeft[0].Status != "queued" {
			t.Errorf("lettersLeft = %+v, want the letter being sent", held.LettersLeft)
		}
		r := letterOf(t, h, letter)
		if r.Status != "sent" || amountText(r.Fee) != "38.00" || len(mails.delivered()) != 1 {
			t.Fatalf("the letter = %s fee %s after %d mails, want sent with its fee", r.Status, amountText(r.Fee), len(mails.delivered()))
		}
		if got := letterWaivers(t, h, letter); !slices.Equal(got, []string{"fee claimed_in_error"}) {
			t.Errorf("the letter's waivers = %v, want its fee waived claimed_in_error", got)
		}
		answered(t, "the barring lift", payer(t, h).Do(http.MethodPost, liftPath(id), liftBody(false, "Innsigelsen var begrunnet")))
		if got := letterWaivers(t, h, letter); !slices.Equal(got, []string{"fee claimed_in_error"}) {
			t.Errorf("after the barring lift the waivers = %v, want the one fee waiver", got)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})

	t.Run("the hold first", func(t *testing.T) {
		h, mails := workerHarness(t, "", modtest.WithPoolMaxConns(2))
		id, letter := dueLetter(t, h, 1)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		parked, release := parkFirstLock(t, "invoice")

		holdDone := startRequest(payer(t, h), http.MethodPost, holdPath(id), holdNote("Kunden bestrider timene"))
		waitFor(t, "the hold's invoice lock", parked)
		holdPID := idleInTransaction(t, probeConn)
		done := claimed(invoices.NewReminderWorker(h.Deps()))
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{holdPID}) {
			t.Errorf("the dispatch waits on %v, want the hold %d", got, holdPID)
		}
		close(release)
		held := answered(t, "the hold", finished(t, "the hold", holdDone, http.StatusOK))
		finishedClaim(t, done)
		if len(held.LettersLeft) != 0 {
			t.Errorf("lettersLeft = %+v, want none", held.LettersLeft)
		}
		if s := letterStateOf(t, h, letter); s.status != "withdrawn" || s.reason != "on_hold" || s.byUser || len(mails.tried()) != 0 {
			t.Errorf("the letter = %+v after %d sends, want withdrawn on_hold by no one, nothing mailed", s, len(mails.tried()))
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})
}

// TestHandoff_RacesReminderDispatch: a hand-off and a dispatch of the
// invoice's letter, as the hold. The dispatch first: the hand-off waits on
// the invoice, then leaves the letter being sent, named in lettersLeft; the
// letter is mailed and marked sent, its fee standing — a hand-off bars no
// charge. The hand-off first: the dispatch waits on the invoice and finds
// the letter withdrawn handed_off.
func TestHandoff_RacesReminderDispatch(t *testing.T) {
	t.Run("the dispatch first", func(t *testing.T) {
		h, mails := workerHarness(t, "", modtest.WithPoolMaxConns(2))
		id, letter := dueLetter(t, h, 1)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked := parkEach()
		defer invoices.SetDispatchAfterLock(hook)()
		handedOff := make(chan struct{})
		sendAfter(mails, handedOff)

		done := claimed(invoices.NewReminderWorker(h.Deps()))
		p := waitParked(t, "the dispatch", parked)
		dispatchPID := idleInTransaction(t, probeConn)
		handoffDone := startRequest(payer(t, h), http.MethodPost, handoffPath(id), handoffBody("2026-09-12", "Inkasso AS"))
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{dispatchPID}) {
			t.Errorf("the hand-off waits on %v, want the dispatch %d", got, dispatchPID)
		}
		close(p.release)
		got := answered(t, "the hand-off", finished(t, "the hand-off", handoffDone, http.StatusOK))
		close(handedOff)
		finishedClaim(t, done)
		if len(got.LettersLeft) != 1 || got.LettersLeft[0].ReminderID != letter {
			t.Errorf("lettersLeft = %+v, want the letter being sent", got.LettersLeft)
		}
		r := letterOf(t, h, letter)
		if r.Status != "sent" || len(mails.delivered()) != 1 || len(letterWaivers(t, h, letter)) != 0 {
			t.Errorf("the letter = %s after %d mails, waivers %v; want sent, its fee standing", r.Status, len(mails.delivered()),
				letterWaivers(t, h, letter))
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})

	t.Run("the hand-off first", func(t *testing.T) {
		h, mails := workerHarness(t, "", modtest.WithPoolMaxConns(2))
		id, letter := dueLetter(t, h, 1)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		parked, release := parkFirstLock(t, "invoice")

		handoffDone := startRequest(payer(t, h), http.MethodPost, handoffPath(id), handoffBody("2026-09-12", "Inkasso AS"))
		waitFor(t, "the hand-off's invoice lock", parked)
		handoffPID := idleInTransaction(t, probeConn)
		done := claimed(invoices.NewReminderWorker(h.Deps()))
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{handoffPID}) {
			t.Errorf("the dispatch waits on %v, want the hand-off %d", got, handoffPID)
		}
		close(release)
		answered(t, "the hand-off", finished(t, "the hand-off", handoffDone, http.StatusOK))
		finishedClaim(t, done)
		if s := letterStateOf(t, h, letter); s.status != "withdrawn" || s.reason != "handed_off" || len(mails.tried()) != 0 {
			t.Errorf("the letter = %+v after %d sends, want withdrawn handed_off, nothing mailed", s, len(mails.tried()))
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})
}

// importRace is matchHarness's installation, sending e-mail through m,
// reminders on, on a pool of two, on Tuesday 27 October 2026: its invoice of
// 1 000 issued 12 September with a KID and the account it printed
// (olderAccount), due 12 October, has a reminder queued (E + 14 is 26
// October), and the file pays it in full to that account with that KID on
// the 26th.
func importRace(t *testing.T) (*harness, *letterMail, invoiceJSON, int64, []byte) {
	t.Helper()
	m := &letterMail{}
	h, inv := matchHarness(t, modtest.WithPoolMaxConns(2), modtest.WithSMTPSend(m.send), modtest.WithEnv("MAIL_DRIVER", "smtp"),
		modtest.WithEnv("SMTP_HOST", "smtp.example.invalid"), modtest.WithEnv("SMTP_FROM", "faktura@example.invalid"))
	remindersOn(t, h, "")
	moveClockTo(t, h, time.Date(2026, time.October, 27, 8, 0, 0, 0, time.UTC))
	letter := queueLetter(t, h, inv.ID, 1, "reminder", false, "nb")
	return h, m, inv, letter, bankfiletest.OCR("1", kidPay(olderAccount, 26, 1000, *inv.Kid, "1"))
}

// TestReminderDispatch_RacesImport: a bank file paying the invoice in full
// and a dispatch of its letter. The dispatch first, held after its invoice
// lock: the match — its line FOR NO KEY UPDATE, then the invoice — waits on
// the invoice, then registers the payment; the letter, its facts written
// before the payment, is mailed and sent. The match first, held after its
// invoice lock: the dispatch waits on the invoice, then judges it settled
// and withdraws the letter settled, mailing nothing.
func TestReminderDispatch_RacesImport(t *testing.T) {
	t.Run("the dispatch first", func(t *testing.T) {
		h, mails, inv, letter, file := importRace(t)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked := parkEach()
		defer invoices.SetDispatchAfterLock(hook)()

		done := claimed(invoices.NewReminderWorker(h.Deps()))
		p := waitParked(t, "the dispatch", parked)
		dispatchPID := idleInTransaction(t, probeConn)
		importDone := startImport(t, importer(t, h), file)
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{dispatchPID}) {
			t.Errorf("the match waits on %v, want the dispatch %d", got, dispatchPID)
		}
		close(p.release)
		var r importJSON
		finished(t, "the import", importDone, http.StatusCreated).JSON(&r)
		finishedClaim(t, done)
		if s := letterOf(t, h, letter); s.Status != "sent" || amountText(s.PrincipalOpen) != "1000.00" || len(mails.delivered()) != 1 {
			t.Errorf("the letter = %s principal %s after %d mails, want sent as judged before the payment",
				s.Status, amountText(s.PrincipalOpen), len(mails.delivered()))
		}
		if got := livePaymentsOf(t, h, inv.ID); r.Matched != 1 || !slices.Equal(got, []string{"1000.00 ocr"}) {
			t.Errorf("the import matched %d, the payments %v; want the payment registered", r.Matched, got)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})

	t.Run("the match first", func(t *testing.T) {
		h, mails, inv, letter, file := importRace(t)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked := parkEach()
		defer invoices.SetMatchAfterInvoiceLock(hook)()

		importDone := startImport(t, importer(t, h), file)
		p := waitParked(t, "the match", parked)
		matchPID := idleInTransaction(t, probeConn)
		done := claimed(invoices.NewReminderWorker(h.Deps()))
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{matchPID}) {
			t.Errorf("the dispatch waits on %v, want the match %d", got, matchPID)
		}
		close(p.release)
		var r importJSON
		finished(t, "the import", importDone, http.StatusCreated).JSON(&r)
		finishedClaim(t, done)
		if s := letterStateOf(t, h, letter); s.status != "withdrawn" || s.reason != "settled" || s.byUser || len(mails.tried()) != 0 {
			t.Errorf("the letter = %+v after %d sends, want withdrawn settled by no one, nothing mailed", s, len(mails.tried()))
		}
		if got := livePaymentsOf(t, h, inv.ID); r.Matched != 1 || !slices.Equal(got, []string{"1000.00 ocr"}) {
			t.Errorf("the import matched %d, the payments %v; want the payment registered", r.Matched, got)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})
}

// TestReminderDispatch_RacesWithdraw: a person's withdrawal and a dispatch
// of one letter. The dispatch held right after it locked the letter (FOR NO
// KEY UPDATE, the invoice FOR UPDATE before it): the withdrawal — the letter
// alone — waits on the letter; the dispatch writes the facts and commits,
// and the withdrawal then finds the letter being sent and is refused
// reminder_not_withdrawable (plan reading 45). Sent, it is refused again;
// when instead the attempt fails, which clears the lease and the facts, it
// is withdrawn.
func TestReminderDispatch_RacesWithdraw(t *testing.T) {
	for _, c := range []struct {
		name     string
		mailFail bool
	}{{"the letter sent", false}, {"the attempt failed", true}} {
		t.Run(c.name, func(t *testing.T) {
			h, mails := workerHarness(t, "", modtest.WithPoolMaxConns(2))
			id, letter := dueLetter(t, h, 1)
			probeConn := ownConn(t, h)
			before := deadlocks(t, probeConn)
			parked, release := parkFirstLock(t, "reminder")
			withdrawn := make(chan struct{})
			sendAfter(mails, withdrawn)
			if c.mailFail {
				mails.failWith(errSMTPRefused)
			}
			body := map[string]any{"reason": "Kunden har betalt kontant"}

			done := claimed(invoices.NewReminderWorker(h.Deps()))
			waitFor(t, "the dispatch's letter lock", parked)
			dispatchPID := idleInTransaction(t, probeConn)
			if got := heldMode(t, probeConn, "invoices.reminders", "id = $1", letter); got != modeNoKeyUpdate {
				t.Errorf("the letter is held %q, want FOR NO KEY UPDATE", got)
			}
			if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", id); got != modeUpdate {
				t.Errorf("the invoice is held %q, want FOR UPDATE", got)
			}
			withdrawDone := startRequest(payer(t, h), http.MethodPost, letterPath(letter, "withdraw"), body)
			waiter := newWaiter(t, probeConn)
			if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{dispatchPID}) {
				t.Errorf("the withdrawal waits on %v, want the dispatch %d", got, dispatchPID)
			}
			close(release)
			refusedLetter(t, "the withdrawal while the letter is being sent",
				finished(t, "the withdrawal", withdrawDone, http.StatusConflict), "reminder_not_withdrawable")
			close(withdrawn)
			finishedClaim(t, done)
			after := payer(t, h).Do(http.MethodPost, letterPath(letter, "withdraw"), body)
			if c.mailFail {
				if got := letterAnswer(t, "the withdrawal after the failed attempt", after); got.Status != "withdrawn" {
					t.Errorf("the letter = %s, want withdrawn", got.Status)
				}
			} else {
				refusedLetter(t, "the withdrawal of the sent letter", after, "reminder_not_withdrawable")
				if r := letterOf(t, h, letter); r.Status != "sent" || len(mails.delivered()) != 1 {
					t.Errorf("the letter = %s after %d mails, want sent", r.Status, len(mails.delivered()))
				}
			}
			if n := deadlocks(t, probeConn); n != before {
				t.Errorf("Postgres broke %d deadlock(s)", n-before)
			}
		})
	}
}

// TestReminderWorker_RacesTwoClaims: two workers and one letter. While one
// claim's pick holds the queued letter's row FOR UPDATE — a raw transaction
// standing in for the claim mid-statement — a second worker skips it at once
// (SKIP LOCKED): it claims nothing and never waits. While the first claim,
// its lease taken and committed, is parked inside its dispatch holding the
// invoice FOR UPDATE, the second finds the lease live and claims nothing,
// again without waiting. The letter is mailed once.
func TestReminderWorker_RacesTwoClaims(t *testing.T) {
	h, mails := workerHarness(t, "", modtest.WithPoolMaxConns(2))
	id, letter := dueLetter(t, h, 1)
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	w1, w2 := invoices.NewReminderWorker(h.Deps()), invoices.NewReminderWorker(h.Deps())
	second := func(what string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if processed, err := w2.ProcessOne(ctx); err != nil || processed {
			t.Fatalf("the second worker %s: claimed %v, error %v; want nothing claimed at once", what, processed, err)
		}
	}

	pick := holdRow(t, h, `SELECT 1 FROM invoices.reminders WHERE id = $1 FOR UPDATE`, letter)
	if got := heldMode(t, probeConn, "invoices.reminders", "id = $1", letter); got != modeUpdate {
		t.Fatalf("the letter is held %q, want FOR UPDATE", got)
	}
	second("beside a claim's pick")
	pick.release(t)

	hook, parked := parkEach()
	defer invoices.SetDispatchAfterLock(hook)()
	done := claimed(w1)
	p := waitParked(t, "the first claim", parked)
	if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", id); got != modeUpdate {
		t.Errorf("the invoice is held %q, want FOR UPDATE", got)
	}
	if got := heldMode(t, probeConn, "invoices.reminders", "id = $1", letter); got != "" {
		t.Errorf("the letter is held %q under the first claim's lease, want free", got)
	}
	second("under the first claim's lease")
	close(p.release)
	finishedClaim(t, done)
	if r := letterOf(t, h, letter); r.Status != "sent" || len(mails.delivered()) != 1 {
		t.Errorf("the letter = %s after %d mails, want sent once", r.Status, len(mails.delivered()))
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// errSMTPRefused is a mail server's refusal.
var errSMTPRefused = errors.New("554 rejected")
