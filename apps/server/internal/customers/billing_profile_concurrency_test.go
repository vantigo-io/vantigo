package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins PUT .../billing-profile's guarded UPDATE (final review fix
// wave, finding I2: the design's own concurrency test for this sub-resource's
// pgx.ErrNoRows → re-read → 409 branch, queries/customers.sql's
// UpdateCustomerBillingProfile) — nothing else in this package exercises it.
// Modelled on customers_concurrency_test.go's
// TestPutCustomersById_ConcurrentUpdatesWithSameRevision_ExactlyOneWins:
// gateCustomerLock (addresses_concurrency_test.go) forces both PUTs' Go-side
// pre-check (body.Revision != existing.Revision) to see the same,
// still-current revision, so only the database's own row locking behind
// "AND (expected_revision IS NULL OR revision = expected_revision)" decides
// a winner once the gate releases. Dropping that clause from
// UpdateCustomerBillingProfile's WHERE would let both PUTs succeed (two
// 200s, revision bumped twice, no 409 at all), which this test's "exactly
// one winner" assertion catches.
func TestPutCustomersByIdBillingProfile_ConcurrentUpdatesWithSameRevision_ExactlyOneWins(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Billing Race Co")

	release := gateCustomerLock(t, h, created.Id)

	updateWith := func(currency string) func() *modtest.Response {
		return func() *modtest.Response {
			return putBillingProfile(t, c, created.Id, map[string]any{"currency": currency, "revision": 1})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(updateWith("NOK"), updateWith("SEK"))
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

	if got := fetchBillingProfile(t, c, created.Id); got.Revision != 2 {
		t.Errorf("final revision = %d, want 2 (bumped exactly once)", got.Revision)
	}
}

// The rest of this file pins GET/PUT .../billing-profile's one snapshot of
// the row and its group (billingProfileSnapshot, billing_profile.go; customer
// groups design D4): the billing row and the group are two unlocked reads, and the revision
// the response carries must describe the group it shows. The gate is
// group_concurrency_test.go's: only the second read touches
// customers.customer_groups (CustomerGroupMembership's LEFT JOIN), so a
// transaction holding ACCESS EXCLUSIVE on that table lets the first read
// through and queues the second, and whatever the gate does to the customer
// before committing lands exactly between the two.
//
// Not parallel either: awaitLockWaiters counts lock waiters across the whole
// database.

// gateGroupTable opens the gate: a transaction holding ACCESS EXCLUSIVE on
// customers.customer_groups. The caller runs its concurrent write through the
// returned transaction, then commits it to release the queued read.
func gateGroupTable(t *testing.T, h *modtest.Harness) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE customers.customer_groups IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock customer_groups: %v", err)
	}
	return gate
}

// betweenTheReads runs request in the background, waits until it queues on
// the gate, runs sql through the gate, commits it and returns the answer.
func betweenTheReads(t *testing.T, h *modtest.Harness, gate pgx.Tx, request func() *modtest.Response, sql string, args ...any) *modtest.Response {
	t.Helper()
	ctx := context.Background()
	done := make(chan *modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- request()
		close(finished)
	}()
	awaitLockWaiters(t, h, 1, finished)
	if _, err := gate.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("gate: %s: %v", sql, err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	return <-done
}

const moveCustomerSQL = `UPDATE customers.customers SET group_id = $1, revision = revision + 1 WHERE id = $2`

// groupedCustomer is a customer in Retail (30 days) at revision 2, and a
// second group, Key accounts (60 days), for the gate to move it into.
func groupedCustomer(t *testing.T, c *modtest.Client, name string) (id int32, retailID, keyID string) {
	t.Helper()
	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	key := createGroup(t, c, map[string]any{"name": "Key accounts", "defaultPaymentTermsDays": 60})
	customer := createCustomer(t, c, name)
	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id, "revision": 1}); r.Status != http.StatusOK {
		t.Fatalf("set the starting group: status %d body %s", r.Status, r.Body)
	}
	return customer.Id, retail.Id, key.Id
}

// TestGetBillingProfile_AMoveBetweenTheTwoReadsAnswersOneSnapshot: the GET
// read revision 2 (Retail), the gate moves the customer to Key accounts at
// revision 3, and the membership read sees 3. Without the re-read the GET
// answers revision 2 beside Key accounts, a pair no row ever had; with it, 3
// beside Key accounts. A GET has no revision to conflict with, so the answer is
// the consistent pair, never a 409.
func TestGetBillingProfile_AMoveBetweenTheTwoReadsAnswersOneSnapshot(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	id, _, keyID := groupedCustomer(t, c, "Snapshot Get Co")

	gate := gateGroupTable(t, h)
	r := betweenTheReads(t, h, gate, func() *modtest.Response { return getBillingProfile(t, c, id) }, moveCustomerSQL, keyID, id)

	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got billingProfileJSON
	r.JSON(&got)
	if got.GroupDefault == nil || got.GroupDefault.Group.Id != keyID || got.Revision != 3 {
		t.Errorf("revision %d beside groupDefault %+v, want revision 3 beside Key accounts: one snapshot", got.Revision, got.GroupDefault)
	}
}

// TestPutBillingProfile_AMoveBetweenTheTwoReadsIsAConflict: the same window
// under a PUT that claims revision 2 and resubmits the current (empty)
// profile. Without the re-read the no-op branch answers 200 at revision 2
// beside Key accounts; with it, the re-read row is at revision 3 and the
// claimed 2 is the revision conflict, as if the move had landed first.
func TestPutBillingProfile_AMoveBetweenTheTwoReadsIsAConflict(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	id, _, keyID := groupedCustomer(t, c, "Snapshot Put Co")

	gate := gateGroupTable(t, h)
	r := betweenTheReads(t, h, gate, func() *modtest.Response {
		return putBillingProfile(t, c, id, map[string]any{"revision": 2})
	}, moveCustomerSQL, keyID, id)

	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409: the snapshot's revision is 3, the body claimed 2", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Customer revision conflict" || problem.Code != nil {
		t.Errorf("conflict = title %q code %v, want the revision conflict with no code", problemTitle(problem.Title), problem.Code)
	}
}

// TestPutBillingProfile_AnUnguardedWriteAfterAMoveAnswersTheNewGroup: a PUT
// without a revision writes unguarded, so a move can land between its snapshot
// and its UPDATE. The gate here is the customer ROW (gateCustomerLock's shape):
// the reads pass it, the UPDATE queues on it, the gate moves the customer to Key
// accounts (revision 3) and commits, and the PUT's write lands at revision 4.
// Without the post-write re-read the response pairs revision 4 with the Retail
// the snapshot saw.
func TestPutBillingProfile_AnUnguardedWriteAfterAMoveAnswersTheNewGroup(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	id, _, keyID := groupedCustomer(t, c, "Unguarded Put Co")

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.customers WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("gate: lock the customer row: %v", err)
	}
	r := betweenTheReads(t, h, gate, func() *modtest.Response {
		return putBillingProfile(t, c, id, map[string]any{"paymentTermsDays": 14})
	}, moveCustomerSQL, keyID, id)

	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var got billingProfileJSON
	r.JSON(&got)
	if got.Revision != 4 || got.GroupDefault == nil || got.GroupDefault.Group.Id != keyID {
		t.Errorf("revision %d beside groupDefault %+v, want revision 4 beside Key accounts", got.Revision, got.GroupDefault)
	}
}

// TestBillingProfile_ACustomerDeletedBetweenTheTwoReadsIs404 pins both
// handlers' 404 for the delete race: the billing row is read, the gate deletes
// the customer, and the membership read finds no row — pgx.ErrNoRows through
// billingProfileSnapshot's wrap, answered as the 404 the first read would
// have given, never a 500.
func TestBillingProfile_ACustomerDeletedBetweenTheTwoReadsIs404(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			h := newHarness(t)
			c := authenticatedClient(t, h)
			customer := createCustomer(t, c, fmt.Sprintf("Vanishing %s Co", method))

			gate := gateGroupTable(t, h)
			r := betweenTheReads(t, h, gate, func() *modtest.Response {
				if method == http.MethodGet {
					return getBillingProfile(t, c, customer.Id)
				}
				return putBillingProfile(t, c, customer.Id, map[string]any{"paymentTermsDays": 14})
			}, `DELETE FROM customers.customers WHERE id = $1`, customer.Id)

			if r.Status != http.StatusNotFound {
				t.Errorf("status %d body %s, want 404", r.Status, r.Body)
			}
		})
	}
}
