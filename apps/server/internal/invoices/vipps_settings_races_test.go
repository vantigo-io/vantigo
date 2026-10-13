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

// startVippsPut stores body on its own goroutine under a 30-second deadline.
func startVippsPut(c *modtest.Client, body map[string]any) <-chan raceRequest {
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(http.MethodPut, vippsSettingsPath, body, modtest.Context(ctx))}
	}()
	return done
}

// TestVippsCredentials_TwoFirstPutsRace: two first PUTs, while an attempt
// is live. The first inserts the row and parks before its commit; the second
// finds no row to lock, and its insert waits on the first — proved by
// pg_blocking_pids. When the first commits, the second's insert does nothing
// and the second runs again on the locked path, where its other sales unit
// is a change judged against the live attempt: 409 payment_attempts_active,
// the first's sales unit kept. Without that, the second would write its
// sales unit over the first's, the attempts never counted.
func TestVippsCredentials_TwoFirstPutsRace(t *testing.T) {
	h := raceHarness(t)
	saveSeller(t, h, completeSeller(1))
	plantAttempt(t, h, "authorized")
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)

	parked, release := make(chan uint32, 1), make(chan struct{})
	var once sync.Once
	restore := invoices.SetVippsPutAfterWrite(func(ctx context.Context, tx pgx.Tx) {
		once.Do(func() {
			var pid uint32
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Errorf("read the first PUT's backend: %v", err)
			}
			parked <- pid
			<-release
		})
	})
	defer restore()

	manager := vippsManager(t, h)
	first := startVippsPut(manager, vippsKeys("111111", vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey))
	var firstPID uint32
	select {
	case firstPID = <-parked:
	case <-time.After(10 * time.Second):
		t.Fatal("the first PUT never wrote its row")
	}
	second := startVippsPut(manager, vippsKeys("222222", vippstest.ClientID, vippstest.ClientSecret, vippstest.SubscriptionKey))
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{firstPID}) {
		t.Fatalf("the second PUT waits on %v, want the first PUT's %d", got, firstPID)
	}
	close(release)

	finished(t, "the first PUT", first, http.StatusOK)
	res := finished(t, "the second PUT", second, http.StatusConflict)
	if code := problemOf(t, res).Code; code != "payment_attempts_active" {
		t.Errorf("the second PUT's code = %q, want payment_attempts_active", code)
	}
	if row := storedVipps(t, h); row.SettingsJson != `{"msn":"111111"}` {
		t.Errorf("the stored sales unit = %s, want the first PUT's", row.SettingsJson)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks went from %d to %d", before, after)
	}
}

// TestVippsVerify_OldKeysNeverClearANewerRefusal: a verify accepted for the
// keys it read clears a refusal only while those keys are still stored. A
// PUT of new keys while the verify is out, and a refusal recorded against
// them, survive the old keys' ok.
func TestVippsVerify_OldKeysNeverClearANewerRefusal(t *testing.T) {
	h := newHarness(t)
	putVipps(t, h, vippstestKeys())
	restore := invoices.SetVippsVerifyBeforeClear(func() {
		h.Advance(time.Second)
		// The same keys stored again are a new version all the same.
		putVipps(t, h, vippstestKeys())
		h.Exec(t, `UPDATE invoices.payment_provider_credentials SET rejected_at = $1`, h.Now())
	})
	defer restore()

	if got := verifyVipps(t, h); got != "ok" {
		t.Fatalf("verify = %q, want ok", got)
	}
	if storedVipps(t, h).RejectedAt == nil {
		t.Error("the old keys' ok cleared the refusal recorded against the new keys")
	}
	restore()
	// The new keys verified clear it.
	if got := verifyVipps(t, h); got != "ok" || storedVipps(t, h).RejectedAt != nil {
		t.Errorf("verify of the stored keys = %q, rejected %v; want ok and cleared", got, storedVipps(t, h).RejectedAt)
	}
}
