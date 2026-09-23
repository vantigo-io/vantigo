package customers_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins PUT /customers/{id}/owner's guarded UPDATE (Task 3 review
// finding: the pgx.ErrNoRows → re-read → 409 branch of PutCustomersByIdOwner,
// queries/customers.sql's UpdateCustomerOwner) — nothing else in this package
// reaches it, since every other owner test is single-threaded and its 409 comes
// from the Go-side pre-check instead. It is
// contact_info_concurrency_test.go's twin, for the same reason and with the same
// gate: gateCustomerLock (addresses_concurrency_test.go) holds FOR UPDATE on the
// customer row, and GetCustomer is an unlocked SELECT that the gate does not
// hold up, so both PUTs' pre-check (body.Revision != existing.Revision) reads
// the same, still-current revision and only the database's own
// "AND (expected_revision IS NULL OR revision = expected_revision)" can decide a
// winner once the gate releases. Dropping that clause from UpdateCustomerOwner's
// WHERE lets both PUTs succeed (two 200s, the revision bumped twice, no 409 at
// all), which the "exactly one winner" assertion below catches.
//
// Not parallel: awaitLockWaiters counts lock waiters across the whole database.

// TestPutCustomersByIdOwner_ConcurrentUpdatesWithSameRevision_ExactlyOneWins
// races two assignments of the same customer to two DIFFERENT owners, both
// claiming revision 1. Two different owners on purpose: the same owner twice is
// answered by the no-op check before any write (design D5) and nothing would
// race at all.
func TestPutCustomersByIdOwner_ConcurrentUpdatesWithSameRevision_ExactlyOneWins(t *testing.T) {
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	_, otherID := h.SignInUser(t, "customers:view")
	created := createCustomer(t, c, "Owner Race Co")

	release := gateCustomerLock(t, h, created.Id)

	assignTo := func(userID uuid.UUID) func() *modtest.Response {
		return func() *modtest.Response {
			return putOwner(t, c, created.Id, map[string]any{"ownerUserId": userID.String(), "revision": 1})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(assignTo(callerID), assignTo(otherID))
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
			// The database guard's fallback must answer in the same words as the
			// pre-check, with no code: one conflict, one wording, whichever of the
			// two branches produced it (customers foundation design D5).
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

	got := fetchCustomerJSON(t, c, created.Id)
	if got.Revision != 2 {
		t.Errorf("final revision = %d, want 2 (bumped exactly once)", got.Revision)
	}
	// The surviving owner is one of the two, never a mixture and never cleared:
	// the loser wrote nothing.
	if got.Owner == nil || (got.Owner.UserId != callerID.String() && got.Owner.UserId != otherID.String()) {
		t.Errorf("owner = %+v, want one of %s / %s", got.Owner, callerID, otherID)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.owner_changed"); n != 1 {
		t.Errorf("customer.owner_changed events = %d, want 1 (only the winner recorded one)", n)
	}
}
