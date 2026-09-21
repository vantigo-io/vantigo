package customers_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins PUT .../contact-info's guarded UPDATE (final review fix
// wave, finding I2: the design's own concurrency test for this sub-resource's
// pgx.ErrNoRows → re-read → 409 branch, queries/customers.sql's
// UpdateCustomerContactInfo) — nothing else in this package exercises it.
// Modelled on customers_concurrency_test.go's
// TestPutCustomersById_ConcurrentUpdatesWithSameRevision_ExactlyOneWins and
// billing_profile_concurrency_test.go's own twin: gateCustomerLock
// (addresses_concurrency_test.go) forces both PUTs' Go-side pre-check
// (body.Revision != existing.Revision) to see the same, still-current
// revision, so only the database's own row locking behind
// "AND (expected_revision IS NULL OR revision = expected_revision)" decides
// a winner once the gate releases. Dropping that clause from
// UpdateCustomerContactInfo's WHERE would let both PUTs succeed (two 200s,
// revision bumped twice, no 409 at all), which this test's "exactly one
// winner" assertion catches.
func TestPutCustomersByIdContactInfo_ConcurrentUpdatesWithSameRevision_ExactlyOneWins(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Contact Info Race Co")

	release := gateCustomerLock(t, h, created.Id)

	updateWith := func(email string) func() *modtest.Response {
		return func() *modtest.Response {
			return putContactInfo(t, c, created.Id, map[string]any{"email": email, "revision": 1})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(updateWith("writer-a@race.example.test"), updateWith("writer-b@race.example.test"))
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
			if problemTitle(problem.Title) != "Customer revision conflict" {
				t.Errorf("loser Title = %q, want %q", problemTitle(problem.Title), "Customer revision conflict")
			}
		default:
			t.Errorf("status %d body %s, want 200 or 409", r.Status, r.Body)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d, want exactly one of each", winners, losers)
	}

	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 2 {
		t.Errorf("final revision = %d, want 2 (bumped exactly once)", got.Revision)
	}
}
