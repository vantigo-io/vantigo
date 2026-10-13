package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

// The races of an attempt's create (invoices payments and reminders design
// D16, D18; plan readings 8 and m15), on the race kit: a pool of two, every
// raw lock and probe on a connection of its own, the wait proved by
// pg_blocking_pids, every racing request under a deadline, no deadlock.

// startPayCreate asks for a payment on its own goroutine under a 30-second
// deadline.
func startPayCreate(c *modtest.Client, token string) <-chan raceRequest {
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(http.MethodPost, payAttemptsPath, map[string]any{"token": token, "acceptTerms": true}, modtest.Context(ctx))}
	}()
	return done
}

// payReason is a racing create's 409 reason, "" for none.
func payReason(t *testing.T, res *modtest.Response) string {
	t.Helper()
	var p payProblemJSON
	res.JSON(&p)
	if p.Reason == nil {
		return ""
	}
	return *p.Reason
}

// parkOnce installs park as the first call of a seam taking a transaction:
// it reports that transaction's backend and waits for release.
func parkOnce(t *testing.T) (hook func(ctx context.Context, tx pgx.Tx), parked <-chan uint32, release closeOnce) {
	t.Helper()
	p, r := make(chan uint32, 1), make(chan struct{})
	var once, closed sync.Once
	// A test that fails before it releases the hook still lets it go, so the
	// parked request ends and the harness can close.
	t.Cleanup(func() { closed.Do(func() { close(r) }) })
	release = closeOnce{ch: r, once: &closed}
	return func(ctx context.Context, tx pgx.Tx) {
		once.Do(func() {
			var pid uint32
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Errorf("read the parked transaction's backend: %v", err)
			}
			p <- pid
			<-r
		})
	}, p, release
}

// closeOnce is a release channel closed once, by the test or its cleanup.
type closeOnce struct {
	ch   chan struct{}
	once *sync.Once
}

func (c closeOnce) Close() { c.once.Do(func() { close(c.ch) }) }

func parkedPID(t *testing.T, what string, parked <-chan uint32) uint32 {
	t.Helper()
	select {
	case pid := <-parked:
		return pid
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never parked", what)
		return 0
	}
}

// TestPayLink_RevokeRacesAttemptCreate (m15): a revoke holding the invoice
// — its lock, then the link revoked, uncommitted — while a create that
// judged the link live on the pool waits on the invoice. When the revoke
// commits, the create re-reads the link under its own lock and answers 409
// revoked; nothing is inserted and Vipps is never asked.
func TestPayLink_RevokeRacesAttemptCreate(t *testing.T) {
	h, _ := payReady(t, modtest.WithPoolMaxConns(2))
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)

	revoke := holdRow(t, h, `SELECT 1 FROM invoices.invoices WHERE id = $1 FOR UPDATE`, inv.ID)
	if _, err := revoke.tx.Exec(context.Background(),
		`UPDATE invoices.pay_links SET revoked_at = $2 WHERE invoice_id = $1 AND revoked_at IS NULL`, inv.ID, h.Now()); err != nil {
		t.Fatal(err)
	}
	create := startPayCreate(h.Client(t), token)
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{revoke.pid}) {
		t.Fatalf("the create waits on %v, want the revoke's %d", got, revoke.pid)
	}
	revoke.release(t)

	res := finished(t, "the create", create, http.StatusConflict)
	if got := payReason(t, res); got != "revoked" {
		t.Errorf("the create's reason = %q, want revoked", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID); n != 0 {
		t.Errorf("%d attempts inserted, want none", n)
	}
	if n := len(vippsCreates(t, h)); n != 0 {
		t.Errorf("%d creates sent to Vipps, want none", n)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks went from %d to %d", before, after)
	}
}

// TestPaymentSettings_PayLinksOffRacesCreate (m15): a create waits on the
// invoice, held by another transaction, while a payment settings PUT turning
// pay links off commits — it locks only its own row. The create then
// re-judges availability under the invoice's lock and answers 409
// unavailable.
func TestPaymentSettings_PayLinksOffRacesCreate(t *testing.T) {
	h, _ := payReady(t, modtest.WithPoolMaxConns(2))
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)

	holder := holdRow(t, h, `SELECT 1 FROM invoices.invoices WHERE id = $1 FOR UPDATE`, inv.ID)
	create := startPayCreate(h.Client(t), token)
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{holder.pid}) {
		t.Fatalf("the create waits on %v, want the holder's %d", got, holder.pid)
	}
	payLinksOff(t, h)
	holder.release(t)

	res := finished(t, "the create", create, http.StatusConflict)
	if got := payReason(t, res); got != "unavailable" {
		t.Errorf("the create's reason = %q, want unavailable", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID); n != 0 {
		t.Errorf("%d attempts inserted, want none", n)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks went from %d to %d", before, after)
	}
}

// TestVippsCredentials_SalesUnitChangeRacesCreate (reading 8): a create
// parked after it shared the credentials under the invoice's lock; a PUT
// changing the sales unit waits on the row, and once the create commits its
// attempt counts it and answers 409 payment_attempts_active — no attempt is
// ever stranded under the old sales unit. Reversed, the create waits on the
// PUT's lock and, the credentials replaced since its provider was built,
// answers a retryable 503 with nothing inserted; its retry inserts under the
// new sales unit.
func TestVippsCredentials_SalesUnitChangeRacesCreate(t *testing.T) {
	t.Run("the create first", func(t *testing.T) {
		h, _ := payReady(t, modtest.WithPoolMaxConns(2))
		inv := issuedAcme(t, h)
		token := payToken(t, h, inv.ID)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked, release := parkOnce(t)
		defer invoices.SetAttemptAfterInvoiceLock(hook)()

		create := startPayCreate(h.Client(t), token)
		createPID := parkedPID(t, "the create", parked)
		put := startVippsPut(vippsManager(t, h), vippsKeys("654321", vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey))
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{createPID}) {
			t.Fatalf("the PUT waits on %v, want the create's %d", got, createPID)
		}
		release.Close()

		finished(t, "the create", create, http.StatusCreated)
		res := finished(t, "the PUT", put, http.StatusConflict)
		if code := problemOf(t, res).Code; code != "payment_attempts_active" {
			t.Errorf("the PUT's code = %q, want payment_attempts_active", code)
		}
		if msn := modtest.One[string](t, h.Harness, `SELECT msn FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID); msn != vippstest.MSN {
			t.Errorf("the attempt's sales unit = %s, want %s", msn, vippstest.MSN)
		}
		if row := storedVipps(t, h); row.SettingsJson != `{"msn":"`+vippstest.MSN+`"}` {
			t.Errorf("the stored sales unit = %s, want the old one kept", row.SettingsJson)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("deadlocks went from %d to %d", before, after)
		}
	})
	t.Run("the PUT first", func(t *testing.T) {
		h, _ := payReady(t, modtest.WithPoolMaxConns(2))
		inv := issuedAcme(t, h)
		token := payToken(t, h, inv.ID)
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		hook, parked, release := parkOnce(t)
		defer invoices.SetVippsPutAfterWrite(hook)()

		h.Advance(time.Second)
		put := startVippsPut(vippsManager(t, h), vippsKeys("654321", vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey))
		putPID := parkedPID(t, "the PUT", parked)
		create := startPayCreate(h.Client(t), token)
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{putPID}) {
			t.Fatalf("the create waits on %v, want the PUT's %d", got, putPID)
		}
		release.Close()

		finished(t, "the PUT", put, http.StatusOK)
		res := finished(t, "the create", create, http.StatusServiceUnavailable)
		if code := problemOf(t, res).Code; code != "payments_unavailable" {
			t.Errorf("the create's code = %q, want payments_unavailable", code)
		}
		if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID); n != 0 {
			t.Errorf("%d attempts inserted under the old keys, want none", n)
		}
		// The retry is made under the new sales unit (which vippstest,
		// knowing one sales unit, refuses: 502, the attempt failed).
		startPay(h.Client(t), token)
		if msn := modtest.One[string](t, h.Harness, `SELECT msn FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID); msn != "654321" {
			t.Errorf("the retry's sales unit = %s, want 654321", msn)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("deadlocks went from %d to %d", before, after)
		}
	})
}

// TestAttempts_CredentialsVersionMismatch: the provider is built on the
// pool from the credentials as they were; a PUT that replaced them — a new
// client secret of the same sales unit, allowed while attempts are live — commits while the
// create waits on the row. The create compares the version it shares under
// the invoice's lock with the provider's and refuses with a retryable 503
// payments_unavailable: nothing inserted, nothing sent with the old keys. A
// retry then pays.
func TestAttempts_CredentialsVersionMismatch(t *testing.T) {
	h, _ := payReady(t, modtest.WithPoolMaxConns(2))
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)
	probeConn := ownConn(t, h)
	hook, parked, release := parkOnce(t)
	defer invoices.SetVippsPutAfterWrite(hook)()

	h.Advance(time.Second)
	put := startVippsPut(vippsManager(t, h), vippsKeys(vippstest.MSN, "", vippstest.ClientSecret, ""))
	putPID := parkedPID(t, "the PUT", parked)
	create := startPayCreate(h.Client(t), token)
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{putPID}) {
		t.Fatalf("the create waits on %v, want the PUT's %d", got, putPID)
	}
	release.Close()

	finished(t, "the PUT", put, http.StatusOK)
	res := finished(t, "the create", create, http.StatusServiceUnavailable)
	if p := problemOf(t, res); p.Code != "payments_unavailable" {
		t.Errorf("the create's code = %q, want payments_unavailable", p.Code)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID); n != 0 {
		t.Errorf("%d attempts inserted, want none", n)
	}
	if n := len(vippsCreates(t, h)); n != 0 {
		t.Errorf("%d creates sent to Vipps, want none", n)
	}
	startedPay(t, h.Client(t), token)
}

// TestAttempts_RejectedUnderTheShare (L1): a refusal of the keys recorded
// while the create waits for the credentials row — after it judged
// availability under the invoice's lock — is seen in the row it shares: a
// 503 payments_unavailable, nothing inserted, nothing sent.
func TestAttempts_RejectedUnderTheShare(t *testing.T) {
	h, _ := payReady(t, modtest.WithPoolMaxConns(2))
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)
	probeConn := ownConn(t, h)
	refusal := holdRow(t, h, `UPDATE invoices.payment_provider_credentials SET rejected_at = $1`, h.Now())
	create := startPayCreate(h.Client(t), token)
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{refusal.pid}) {
		t.Fatalf("the create waits on %v, want the refusal's %d", got, refusal.pid)
	}
	refusal.release(t)
	res := finished(t, "the create", create, http.StatusServiceUnavailable)
	if code := problemOf(t, res).Code; code != "payments_unavailable" {
		t.Errorf("the create's code = %q, want payments_unavailable", code)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID); n != 0 {
		t.Errorf("%d attempts inserted, want none", n)
	}
	if n := len(vippsCreates(t, h)); n != 0 {
		t.Errorf("%d creates sent to Vipps, want none", n)
	}
}
