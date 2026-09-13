package communications_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/communications"
)

// This file is the retention lease, gated rather than hoped for — the exact
// inverse of outbox_concurrency_test.go. The outbox has no lock at all (its
// conditional UPDATE is the lock, design D8/§4); retention is the one worker
// that takes the installation-wide advisory lease (inventory §11 D2, §14), so
// what needs a gated test here is that a second replica entering its cycle
// WHILE the first is mid-batch skips instead of processing the same rows.
//
// The gate is the package's usual technique (channels_concurrency_test.go's
// race/awaitLockWaiters helpers, reused rather than redeclared): a gate
// transaction takes `LOCK TABLE communications.conversation_messages IN
// EXCLUSIVE MODE` before either worker starts. EXCLUSIVE is compatible with
// the ACCESS SHARE of the batch's candidate SELECT, so the lease holder gets
// all the way into its delete transaction, and conflicts with the ROW
// EXCLUSIVE its `DELETE FROM conversation_messages` needs, so it parks there —
// genuinely blocked, visible in pg_stat_activity, never a sleep.
//
// That is what makes the assertion deterministic rather than timing-hopeful:
// once awaitLockWaiters confirms one backend is really blocked inside the
// batch, the only worker that can possibly return is the one that did NOT get
// the lease. If the lease were removed, both would block on the table lock and
// nothing would return until the gate released — which is why the read below
// is bounded and says so.

// TestRetentionWorker_ConcurrentCyclesRunTheBatchOnce is the teeth check: two
// workers, one queue, exactly one cycle. Without the lease both would process
// the same candidate batch concurrently — the double-processing the lease
// exists to prevent (inventory §14, "what prevents two workers processing the
// same queue: retention — the advisory lease").
func TestRetentionWorker_ConcurrentCyclesRunTheBatchOnce(t *testing.T) {
	h := newHarness(t)
	old := 400 * 24 * time.Hour
	first := seedRetentionMessage(t, h, retentionSeed{age: old, outboxStatus: "completed"})
	second := seedRetentionMessage(t, h, retentionSeed{age: old + time.Hour})

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.conversation_messages IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock conversation_messages: %v", err)
	}

	const n = 2
	type outcome struct {
		ran bool
		err error
	}
	results := make(chan outcome, n)
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i := 0; i < n; i++ {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			// Each goroutine gets its own worker, as two replicas would.
			w := communications.NewRetentionWorker(h.Deps())
			ready.Done()
			<-begin
			ran, err := w.RunCycle(ctx)
			results <- outcome{ran: ran, err: err}
		}()
	}
	ready.Wait()
	close(begin)

	finished := make(chan struct{})
	go func() {
		done.Wait()
		close(finished)
	}()
	awaitLockWaiters(t, h, 1, finished)

	// The lease holder is now genuinely blocked inside its batch transaction.
	// Whoever returns while that is true is the worker that was refused the
	// lease, and it must report that it did not run.
	var skipped outcome
	select {
	case skipped = <-results:
	case <-time.After(15 * time.Second):
		t.Fatal("no worker returned while the other was blocked mid-batch: both took the lease, so the advisory lease excludes nothing")
	}
	if skipped.err != nil {
		t.Fatalf("the skipping worker returned an error: %v", skipped.err)
	}
	if skipped.ran {
		t.Error("a second worker ran its cycle while the first held the lease mid-batch")
	}

	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	<-finished
	holder := <-results
	if holder.err != nil {
		t.Fatalf("the lease holder returned an error: %v", holder.err)
	}
	if !holder.ran {
		t.Error("neither worker ran the cycle")
	}

	if messageExists(t, h, first.messageID) || messageExists(t, h, second.messageID) {
		t.Error("the lease holder's cycle did not drain the queue")
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 0 {
		t.Errorf("advisory locks still held = %d, want 0: the lease is released even after a contended cycle", n)
	}
}
