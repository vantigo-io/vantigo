package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The sender's throughput, fairness and housekeeping under contention
// (outgoing webhooks design W29, W30, S1, S5).

// TestWebhookSender_AFastReceiverGetsMoreThanTwoMessagesAPoll is the poll
// as idle cadence: a pass that leased all it could is followed by the next
// at once, so twenty messages to a fast receiver take well under the ten
// polls of two they would otherwise wait for.
func TestWebhookSender_AFastReceiverGetsMoreThanTwoMessagesAPoll(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, _ := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	for i := range 20 {
		h.insertDelivery(t, e.ID, int64(i))
	}
	s := h.webhookSender(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- s.Run(ctx) }()
	waitFor(t, func() bool {
		return h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE state = 'succeeded'`) == 20
	})
	elapsed := time.Since(started)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The poll is 2 seconds: twenty in under 3 is far more than one a
	// second to one endpoint.
	if elapsed >= 3*time.Second {
		t.Errorf("20 messages took %v", elapsed)
	}
}

// TestWebhookSender_TheCapCountsLeasesARivalCommittedWhileItHeldTheEndpoint
// is W29's interleaving, made deterministic: a rival holds the endpoint and
// leases three of its deliveries; a pass meanwhile skips the endpoint, and
// the pass after the rival commits leases only the one the cap of 4 leaves.
func TestWebhookSender_TheCapCountsLeasesARivalCommittedWhileItHeldTheEndpoint(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	rc.hold("hooks.example.com")
	for i := range 8 {
		h.insertDelivery(t, e.ID, int64(i))
		h.advance(time.Millisecond)
	}
	s := h.webhookSender(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rival, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rival.Rollback(ctx) }()
	if _, err := rival.Exec(ctx, `SELECT 1 FROM identity.webhook_endpoints WHERE id = $1 FOR UPDATE`, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rival.Exec(ctx, `UPDATE identity.webhook_deliveries SET lease_id = gen_random_uuid(), lease_until = $2
	    WHERE id IN (SELECT id FROM identity.webhook_deliveries WHERE endpoint_id = $1 ORDER BY created_at LIMIT 3)`,
		e.ID, h.now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RunPass(ctx); err != nil || n != 0 {
		t.Fatalf("a pass while the rival held the endpoint leased %d, %v", n, err)
	}
	if err := rival.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RunPass(ctx); err != nil || n != 1 {
		t.Fatalf("after the rival's three, a pass leased %d, %v, want the one the cap leaves", n, err)
	}
	if n, err := s.RunPass(ctx); err != nil || n != 0 {
		t.Fatalf("at the cap a pass leased %d, %v", n, err)
	}
	rc.release()
	s.Wait()
}

// TestWebhookSender_EndpointsThatHangNeverTakeEverySlot is S1: four endpoints
// that never answer hold at most 12 of the process's 16 slots between them,
// and a fifth endpoint is served while they hang, again and again.
func TestWebhookSender_EndpointsThatHangNeverTakeEverySlot(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	other := createIntegration(t, owner, "Second", h.insertRole(t, "Second", "orders:access")).Integration.ID
	hosts := []string{"hang-1.example.com", "hang-2.example.com", "hang-3.example.com", "hang-4.example.com"}
	for i, host := range hosts {
		into := integration
		if i >= 2 {
			into = other // 5 endpoints per integration at most
		}
		e, _ := createWebhook(t, owner, into, map[string]any{"url": "https://" + host + "/in"})
		rc.hold(host)
		for j := range 6 {
			h.insertDelivery(t, e.ID, int64(j))
		}
	}
	fast, _ := createWebhook(t, owner, integration, map[string]any{"url": "https://fast.example.com/in"})
	h.dropPings(t)
	for j := range 3 {
		h.insertDelivery(t, fast.ID, int64(j))
	}
	s := h.webhookSender(t)
	ctx := context.Background()
	fastDone := func(n int) func() bool {
		return func() bool {
			if _, err := s.RunPass(ctx); err != nil {
				t.Fatal(err)
			}
			return h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE endpoint_id = $1 AND state = 'succeeded'`, fast.ID) == n
		}
	}
	waitFor(t, fastDone(3))
	// Saturate: pass until no more is leased.
	for range 10 {
		if _, err := s.RunPass(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range hosts {
		if got := rc.mostHeld(host); got > 4 {
			t.Errorf("%s held %d at once", host, got)
		}
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE lease_until > $1`, h.now()); n > 12 {
		t.Errorf("%d leased to hanging endpoints, want at most 12 of 16", n)
	}
	// A later message to the fifth endpoint is still sent at once.
	h.insertDelivery(t, fast.ID, 99)
	waitFor(t, fastDone(4))
	rc.release()
	s.Wait()
}

// TestWebhookSender_ExpirySkipsARowAnotherTransactionHolds is the expiry's
// SKIP LOCKED: a delivery whose result is being written is left to it, and
// expired on the next sweep, without the sweep waiting for it.
func TestWebhookSender_ExpirySkipsARowAnotherTransactionHolds(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, _ := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	held := h.insertDelivery(t, e.ID, 1)
	h.exec(t, `UPDATE identity.webhook_deliveries SET created_at = $2, next_attempt_at = $3 WHERE id = $1`,
		held, h.now().Add(-4*24*time.Hour), h.now().Add(time.Hour))
	s := h.webhookSender(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM identity.webhook_deliveries WHERE id = $1 FOR UPDATE`, held); err != nil {
		t.Fatal(err)
	}
	if err := s.Housekeep(ctx); err != nil {
		t.Fatalf("a sweep waited on a held row: %v", err)
	}
	if d := h.delivery(t, held); d.State != "pending" {
		t.Errorf("a held row was expired: %+v", d)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Housekeep(ctx); err != nil {
		t.Fatal(err)
	}
	if d := h.delivery(t, held); d.State != "dead" || d.Reason != "expired" {
		t.Errorf("after the hold: %+v", d)
	}
}

// TestWebhookSender_ASecretThatDoesNotOpenIsAFailedAttempt: nothing is sent,
// and the attempt is recorded as failed (other), so the run backs off and
// the endpoint can be disabled, instead of the message being claimed again
// at every lease.
func TestWebhookSender_ASecretThatDoesNotOpenIsAFailedAttempt(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, rc := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{})
	h.dropPings(t)
	h.exec(t, `UPDATE identity.webhook_endpoints SET secret_ciphertext = '\xdeadbeef' WHERE id = $1`, e.ID)
	id := h.insertDelivery(t, e.ID, 1)
	at := h.now()
	pass(t, h.webhookSender(t))
	if d := h.delivery(t, id); d.State != "pending" || d.RunAttempts != 1 || d.LastError != "other" || d.LastStatus != nil ||
		d.NextAt == nil || !d.NextAt.Equal(at.Add(30*time.Second)) {
		t.Errorf("delivery %+v", d)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_attempts WHERE delivery_id = $1 AND error = 'other' AND status_code IS NULL`, id); n != 1 {
		t.Errorf("%d attempts recorded", n)
	}
	if n := len(rc.requests("")); n != 0 {
		t.Errorf("%d requests sent without a secret", n)
	}
	var failingSince *time.Time
	if err := h.pool.QueryRow(context.Background(), `SELECT failing_since FROM identity.webhook_endpoints WHERE id = $1`, e.ID).Scan(&failingSince); err != nil || failingSince == nil {
		t.Errorf("failing_since %v %v", failingSince, err)
	}
}

// TestWebhookFanOut_RecordsOneSkippedDeliveryADay is S5: an integration that
// lost a role gets one skipped row per endpoint and event a day, however many
// events it misses.
func TestWebhookFanOut_RecordsOneSkippedDeliveryADay(t *testing.T) {
	t.Parallel()
	h, owner, _, integration, _ := senderHarness(t)
	e, _ := createWebhook(t, owner, integration, map[string]any{"events": []string{confirmed, cancelled}})
	h.dropPings(t)
	h.exec(t, `DELETE FROM identity.role_permissions WHERE permission_key = 'orders:access'`)
	for i := range int64(3) {
		h.publish(t, contracts.SalesOrderConfirmed{OrderID: i + 1})
	}
	h.publish(t, contracts.SalesOrderCancelled{OrderID: 1})
	h.fanOut(t)
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE endpoint_id = $1 AND state = 'skipped'`, e.ID); n != 2 {
		t.Errorf("%d skipped rows, want one per event name", n)
	}
	h.advance(25 * time.Hour)
	h.publish(t, contracts.SalesOrderConfirmed{OrderID: 9})
	h.fanOut(t)
	if n := h.count(t, `SELECT count(*) FROM identity.webhook_deliveries WHERE endpoint_id = $1 AND state = 'skipped'`, e.ID); n != 3 {
		t.Errorf("%d skipped rows a day on, want three", n)
	}
}
