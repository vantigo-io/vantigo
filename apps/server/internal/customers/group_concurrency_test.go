package customers_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins PUT /customers/{id}/group's guarded UPDATE (SetCustomerGroup,
// queries/groups.sql) — owner_concurrency_test.go's twin, for the same reason
// and with the same gate: gateCustomerLock holds FOR UPDATE on the customer
// row, and the handler's own pre-check reads through an unlocked SELECT the gate
// does not hold up, so both PUTs see the same still-current revision and only
// the database's "AND (expected_revision IS NULL OR revision = expected_revision)"
// can decide a winner once the gate releases. Dropping that clause from
// SetCustomerGroup's WHERE lets both PUTs succeed (two 200s, the revision bumped
// twice, no 409 at all), which the "exactly one winner" assertion below catches.
//
// Not parallel: awaitLockWaiters counts lock waiters across the whole database.

// TestPutCustomersByIdGroup_ConcurrentUpdatesWithSameRevision_ExactlyOneWins
// races two moves of the same customer into two DIFFERENT groups, both claiming
// revision 1. Two different groups on purpose: the same group twice is answered
// by the no-op check before any write and nothing would race at all.
func TestPutCustomersByIdGroup_ConcurrentUpdatesWithSameRevision_ExactlyOneWins(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	created := createCustomer(t, c, "Group Race Co")

	release := gateCustomerLock(t, h, created.Id)

	moveTo := func(groupID string) func() *modtest.Response {
		return func() *modtest.Response {
			return putCustomerGroup(t, c, created.Id, map[string]any{"groupId": groupID, "revision": 1})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(moveTo(retail.Id), moveTo(key.Id))
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	var winners, losers int
	for _, r := range responses {
		switch r.Status {
		case http.StatusOK:
			winners++
		case http.StatusConflict:
			losers++
			var problem conflictProblemJSON
			r.JSON(&problem)
			// The database guard's fallback answers in the same words as the
			// pre-check, with no code: one conflict, one wording, whichever
			// branch produced it (customers foundation design D5).
			if problemTitle(problem.Title) != "Customer revision conflict" || problem.Code != nil {
				t.Errorf("loser = title %q code %v, want the revision conflict with no code",
					problemTitle(problem.Title), problem.Code)
			}
		default:
			t.Errorf("status %d body %s, want 200 or 409", r.Status, r.Body)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d, want exactly one of each", winners, losers)
	}
	if rev := h.Count(t, `SELECT revision FROM customers.customers WHERE id = $1`, created.Id); rev != 2 {
		t.Errorf("revision = %d, want 2: exactly one write landed", rev)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.group_changed"); n != 1 {
		t.Errorf("customer.group_changed events = %d, want 1: the loser wrote nothing", n)
	}
}
