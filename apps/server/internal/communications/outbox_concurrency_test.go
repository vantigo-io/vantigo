package communications_test

import (
	"context"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/communications"
)

// This file is the outbox claim race, gated rather than hoped for. The module
// has already produced several concurrency defects that every sequential test
// passed straight through, so the claim — which is the outbox's ONLY mutual
// exclusion, there being no advisory lock here (design D8, inventory §13.2's
// "the conditional update is the lock") — gets a test that cannot pass
// vacuously.
//
// The gate is the technique channels_concurrency_test.go and
// suppressions_test.go already use in this package, and this file reuses their
// race/awaitLockWaiters helpers rather than redeclaring them: a gate
// transaction takes `LOCK TABLE ... IN EXCLUSIVE MODE` before either worker
// starts. EXCLUSIVE is compatible with the ACCESS SHARE of each worker's
// candidate SELECT, so both workers really do see the same claimable job and
// both really do decide to claim it — but it conflicts with the ROW EXCLUSIVE
// their claim UPDATE needs, so both queue there. Only once
// awaitLockWaiters confirms both backends are genuinely blocked on a lock
// (pg_stat_activity, never a sleep — a sleep passes vacuously under load,
// which is exactly when a claim race actually bites) does the gate release.
// Postgres then serialises the two updates on the row, and the conditional
// WHERE clause does the rest: the loser's UPDATE re-evaluates against the
// winner's committed row, matches nothing, and reports 0 rows.

// TestOutboxWorker_ConcurrentClaimYieldsExactlyOneSend is the teeth check:
// two workers, one job, exactly one claim — and therefore exactly one send,
// one completion and one attempts increment. A claim that was not atomic would
// show up here as two sends (the duplicate the whole lease design exists to
// bound) or as attempts = 2 on a single delivery.
func TestOutboxWorker_ConcurrentClaimYieldsExactlyOneSend(t *testing.T) {
	f := &fakeSMTP{}
	h := newOutboxHarness(t, f)
	fx := seedOutboxJob(t, h)

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.outbox_jobs IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock outbox_jobs: %v", err)
	}

	const n = 2
	processed := make([]bool, n)
	errs := make([]error, n)
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i := 0; i < n; i++ {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			// Each goroutine gets its own worker, as two replicas would.
			w := communications.NewOutboxWorker(h.Deps())
			ready.Done()
			<-begin
			processed[i], errs[i] = w.ProcessOne(ctx)
		}()
	}
	ready.Wait()
	close(begin)

	finished := make(chan struct{})
	go func() {
		done.Wait()
		close(finished)
	}()
	awaitLockWaiters(t, h, n, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	<-finished

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: ProcessOne: %v", i, err)
		}
	}
	claims := 0
	for _, ok := range processed {
		if ok {
			claims++
		}
	}
	if claims != 1 {
		t.Errorf("claims = %d, want exactly 1: the conditional update is the only lock the outbox has", claims)
	}
	if got := len(f.sends()); got != 1 {
		t.Errorf("sends = %d, want exactly 1: a lost claim must not send", got)
	}

	job := readOutboxJob(t, h, fx.messageID)
	if job.status != "completed" {
		t.Errorf("job status = %q, want completed", job.status)
	}
	if job.attempts != 1 {
		t.Errorf("attempts = %d, want 1: only the winning claim increments", job.attempts)
	}
	deliveries := readDeliveries(t, h, fx.messageID)
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}
	if deliveries[0].attempts != 1 {
		t.Errorf("delivery attempts = %d, want 1", deliveries[0].attempts)
	}
	if deliveries[0].status != "relay_accepted" {
		t.Errorf("delivery status = %q, want relay_accepted", deliveries[0].status)
	}
	if n, _ := countEvents(t, h, fx.messageID, "relay_accepted"); n != 1 {
		t.Errorf("relay_accepted events = %d, want exactly 1", n)
	}
}
