package communications_test

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// This file is the messenger sync's claim race and its lease, as
// mailbox_sync_concurrency_test.go and outbox_concurrency_test.go gate theirs:
// the conditional update is the only lock, so the race is made to happen — a gate
// transaction holds the table in EXCLUSIVE mode, both workers pick the same due
// channel and queue on the claim's update, and only once pg_stat_activity shows
// both blocked on a lock (never a sleep) does the gate release.

// TestMessengerSync_ConcurrentClaimsYieldExactlyOneCycle: two workers, one due
// channel, one claim — and therefore one read of the Pending events and each
// event imported once.
func TestMessengerSync_ConcurrentClaimsYieldExactlyOneCycle(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	// No notices: the channel is due from its creation, and a notice would queue
	// each worker on its own update before the claim's.
	f.fake.FailWith("Notified", errors.New("notices off"))
	f.text("4242", "m1", "One", 0)
	f.text("4242", "m2", "Two", 0)

	ctx := context.Background()
	gate, err := f.h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.messenger_sync IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock messenger_sync: %v", err)
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
			w := f.worker()
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
	awaitLockWaiters(t, f.h, n, finished)
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
		t.Errorf("claims = %d, want exactly 1: the conditional update is the only lock the sync has", claims)
	}
	if got := f.fake.CallCount("Pending"); got != 1 {
		t.Errorf("Pending calls = %d, want exactly 1: a lost claim reads nothing", got)
	}
	if got := f.messageCount(t); got != 2 {
		t.Errorf("messages = %d, want each of the two events once", got)
	}
	if got := f.h.Count(t, `SELECT count(*) FROM communications.messenger_sync WHERE channel_id = $1 AND lease_id IS NULL`, f.channel.Id); got != 1 {
		t.Error("the winner did not give the lease back")
	}
}

// TestMessengerSync_ALostLeaseWritesNothing: the row taken by another lease id
// while the page is read — the cycle's first renewal finds the lease gone, so it
// stops with no event imported, the cursor untouched and nothing written on the
// other holder's row.
func TestMessengerSync_ALostLeaseWritesNothing(t *testing.T) {
	t.Parallel()
	f := newChat(t)
	f.text("4242", "m1", "One", 0)
	f.text("4242", "m2", "Two", 0)
	f.fake.OnPending(func(context.Context, string) {
		f.h.Exec(t, `UPDATE communications.messenger_sync SET lease_id = 'another0another0another0another0' WHERE channel_id = $1`, f.channel.Id)
	})

	processed, err := f.worker().ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if got := f.messageCount(t); got != 0 {
		t.Errorf("messages = %d, want none: the cycle stopped at its first renewal", got)
	}
	if got := f.cursor(t); got != 0 {
		t.Errorf("cursor = %d, want 0", got)
	}
	if got := f.fake.CallCount("Ack"); got != 0 {
		t.Errorf("Ack calls = %d, want none", got)
	}
	row := leaseRow(t, f)
	if row != "another0another0another0another0/-/0" {
		t.Errorf("messenger_sync = %s, want the other holder's row untouched", row)
	}
}

func leaseRow(t *testing.T, f chatFixture) string {
	t.Helper()
	var out string
	err := f.h.Pool().QueryRow(context.Background(), `
		SELECT coalesce(lease_id, '') || '/' || coalesce(last_error, '-') || '/' || attempts
		FROM communications.messenger_sync WHERE channel_id = $1`, f.channel.Id).Scan(&out)
	if err != nil {
		t.Fatalf("read messenger_sync: %v", err)
	}
	return out
}
