package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestFollowUpDone_ConcurrentTicks_BothSucceedAndOnlyOneRevisionIsWritten is
// the guard that replaces expectedRevision on the two done paths (follow-ups
// design D1). Two ticks of the same follow-up are forced to genuinely overlap
// at the database — a gate transaction holds the entry row before either
// request starts, so both requests' Go-side "already done?" check sees the same
// still-open row and neither is short-circuited by it — and only the guarded
// UPDATE's own `follow_up_done_at IS NULL` can decide.
//
// What the assertions pin, and what each catches:
//   - both answer 200. A tick that lost a race has still achieved what it asked
//     for; answering 409 (or 404) there would be telling the caller a follow-up
//     they can see is done is not done.
//   - exactly ONE new revision row. Dropping `follow_up_done_at IS NULL` from
//     the UPDATE's WHERE would let both write, producing two revision rows and
//     current_revision 3 — or, more likely, a 23505 on
//     ux_customers_timeline_entries_revisions_entry_revision as both claim
//     revision 2, which is the backstop firing where the guard should have.
//   - current_revision is exactly 2.
//
// race and awaitLockWaiters are contacts_concurrency_test.go's, already shared
// by timeline_concurrency_test.go in this package.
func TestFollowUpDone_ConcurrentTicks_BothSucceedAndOnlyOneRevisionIsWritten(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Tick Race Co")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "ring back", map[string]any{"dueOn": day(h, 1)})

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.customers_timeline_entries WHERE id = $1 FOR UPDATE`, entry.Id); err != nil {
		t.Fatalf("gate: lock the entry row: %v", err)
	}

	tick := func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/follow-up/done", customer.Id, entry.Id), nil)
	}
	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(tick, tick)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	for i, r := range responses {
		if r.Status != http.StatusOK {
			t.Errorf("tick %d: status %d body %s, want 200 (a tick that lost the race still got what it asked for)", i, r.Status, r.Body)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 2 {
		t.Errorf("revision rows = %d, want 2 (the create's, and exactly one tick's)", n)
	}
	if rev := h.Count(t, `SELECT current_revision FROM customers.customers_timeline_entries WHERE id = $1`, entry.Id); rev != 2 {
		t.Errorf("current_revision = %d, want 2 (bumped exactly once)", rev)
	}
}
