package communications_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
)

// This file is the attachment-cleanup claim race, gated rather than hoped for.
// The claim is this worker's ONLY mutual exclusion: unlike retention it takes
// no advisory lease, and every replica runs every cycle (inventory §12.2,
// §14), so the conditional UPDATE in queries/cleanup.sql is all that stands
// between two workers and a double delete of the same object.
//
// The gate is the technique the package already uses (channels_concurrency_test.go's
// race/awaitLockWaiters helpers, reused rather than redeclared): a gate
// transaction takes `LOCK TABLE ... IN EXCLUSIVE MODE` before either worker
// starts. EXCLUSIVE is compatible with the ACCESS SHARE of each worker's
// candidate SELECT, so both workers really do see the same claimable record
// and both really do try to claim it — and it conflicts with the ROW
// EXCLUSIVE their claim UPDATE needs, so both park there. Only once
// awaitLockWaiters confirms both backends are genuinely blocked on a lock
// (pg_stat_activity, never a sleep — a sleep passes vacuously under load,
// which is exactly when a claim race bites) does the gate release. Postgres
// then serialises the two updates on the row and the conditional WHERE does
// the rest: the loser re-evaluates against the winner's committed row,
// matches nothing, and reports 0 rows.

// TestCleanupWorker_ConcurrentBatchesClaimEachRecordOnce is the teeth check:
// two workers, one due record, exactly one claim — and therefore exactly one
// object-store delete. A claim that was not atomic would show up here as two
// deletes of one key, or as two workers each reporting the record as claimed.
func TestCleanupWorker_ConcurrentBatchesClaimEachRecordOnce(t *testing.T) {
	store := newCleanupStore()
	h := newCleanupHarness(t, store)
	key := "cleanup/" + uuid.NewString()
	id := seedCleanupRecord(t, h, cleanupSeed{status: "pending", storageKey: key, nextAttemptIn: -time.Minute})
	store.put(t, key)

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.attachment_cleanup_records IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock attachment_cleanup_records: %v", err)
	}

	const n = 2
	claimed := make([]int, n)
	errs := make([]error, n)
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i := 0; i < n; i++ {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			// Each goroutine gets its own worker, as two replicas would.
			w := communications.NewCleanupWorker(h.Deps())
			ready.Done()
			<-begin
			claimed[i], errs[i] = w.RunBatch(ctx)
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
			t.Fatalf("worker %d: RunBatch: %v", i, err)
		}
	}
	if total := claimed[0] + claimed[1]; total != 1 {
		t.Errorf("records claimed across both workers = %d (%v), want exactly 1: the conditional update is the only lock this worker has",
			total, claimed)
	}
	if got := store.deleteCount(); got != 1 {
		t.Errorf("object deletes = %d, want exactly 1: a lost claim must not delete", got)
	}
	if store.has(key) {
		t.Error("the object survived: neither worker completed the record it claimed")
	}
	row := readCleanupRow(t, h, id)
	if row.status != "completed" {
		t.Errorf("record status = %q, want completed", row.status)
	}
	if row.leaseID != nil {
		t.Errorf("lease_id = %q, want cleared on completion", *row.leaseID)
	}
	if row.attempts != 0 {
		t.Errorf("attempts = %d, want 0: a lost claim must not count as a failed attempt", row.attempts)
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 0 {
		t.Errorf("advisory locks held = %d, want 0: cleanup takes no lease, unlike retention", n)
	}
}
