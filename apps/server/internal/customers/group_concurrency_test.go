package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

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

// gateGroupLock is gateTagLock (tags_concurrency_test.go) for a group: a
// separate transaction holds FOR UPDATE on one customer_groups row, which
// conflicts with the FOR KEY SHARE that SetCustomerGroup's foreign-key check
// takes on the group it points the customer at. The handler's own resolve
// (GetCustomerGroup) is a plain SELECT the lock does not hold up, so the
// request passes it and then queues at the write. deleteAndRelease deletes the
// group under the lock and commits, which is design D2's "only an empty group
// can be deleted" satisfied, since the queued move has not committed. The
// foreign-key check then finds no row.
func gateGroupLock(t *testing.T, h *modtest.Harness, groupID uuid.UUID) (deleteAndRelease func()) {
	t.Helper()
	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.customer_groups WHERE id = $1 FOR UPDATE`, groupID); err != nil {
		t.Fatalf("gate: lock the group row: %v", err)
	}
	return func() {
		if _, err := gate.Exec(ctx, `DELETE FROM customers.customer_groups WHERE id = $1`, groupID); err != nil {
			t.Fatalf("gate: delete the group: %v", err)
		}
		if err := gate.Commit(ctx); err != nil {
			t.Fatalf("gate: release: %v", err)
		}
	}
}

// TestPutCustomersByIdGroup_AGroupDeletedMidWriteIsAFieldError pins the late
// foreign-key branch of PutCustomersByIdGroup. It is
// TestPutCustomersByIdTags_ATagDeletedMidWriteIsAFieldError's twin: the group
// exists when it is resolved and is gone by the time the UPDATE's foreign-key
// check runs, and the answer is the same 400 on groupId the resolve would have
// given. The customer starts IN another group on purpose, because only the
// target has to be empty for the delete to succeed. Dropping the
// IsForeignKeyViolation branch turns this into a 500, which the status
// assertion catches.
func TestPutCustomersByIdGroup_AGroupDeletedMidWriteIsAFieldError(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	vanishing := createGroup(t, c, map[string]any{"name": "Vanishing"})
	customer := createCustomer(t, c, "Vanishing Group Co")
	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("set the starting group: status %d body %s", r.Status, r.Body)
	}

	deleteAndRelease := gateGroupLock(t, h, uuid.MustParse(vanishing.Id))

	done := make(chan *modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": vanishing.Id, "revision": 2})
		close(finished)
	}()
	awaitLockWaiters(t, h, 1, finished)
	deleteAndRelease()
	r := <-done

	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (not a 500 from the foreign-key violation)", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("Customer group %s does not exist", vanishing.Id)
	if got := problem.Errors["groupId"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[groupId] = %v, want [%s]", got, want)
	}
	// The transaction rolled back whole: the customer is still in Retail, at
	// the revision it had, with no event for a move that never happened.
	got := fetchCustomerJSON(t, c, customer.Id)
	if got.Group == nil || got.Group.Id != retail.Id || got.Revision != 2 {
		t.Errorf("after the refusal: group %+v revision %d, want Retail at revision 2", got.Group, got.Revision)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); n != 1 {
		t.Errorf("customer.group_changed events = %d, want 1 (only the starting move)", n)
	}
}

// moveBetweenTheTwoReads holds open the window between PutCustomersByIdGroup's
// two unlocked reads, GetCustomer and CustomerGroupMembership, with no seam.
// Only the second read touches customers.customer_groups (its LEFT JOIN), so a
// gate holding ACCESS EXCLUSIVE on that table lets GetCustomer through and
// queues the membership read. While the read is queued, the gate moves the
// customer into moveTo with a raw UPDATE, which bumps the revision and records
// no event, and commits. The request's first pair of reads then disagrees by
// exactly one revision. It returns the request's response.
func moveBetweenTheTwoReads(t *testing.T, h *modtest.Harness, c *modtest.Client, customerID int32, body map[string]any, moveTo string) *modtest.Response {
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

	done := make(chan *modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- putCustomerGroup(t, c, customerID, body)
		close(finished)
	}()
	awaitLockWaiters(t, h, 1, finished)
	if _, err := gate.Exec(ctx, `UPDATE customers.customers SET group_id = $1, revision = revision + 1 WHERE id = $2`, moveTo, customerID); err != nil {
		t.Fatalf("gate: move the customer: %v", err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	return <-done
}

// TestPutCustomersByIdGroup_AMoveBetweenTheTwoReads pins what
// PutCustomersByIdGroup does when its two reads see different revisions, in
// both of the cases the handler tells apart.
//
// With a revision, the row has moved past the caller's revision: the revision
// conflict. The gate moves the customer into the very group the request names,
// because that is the case a missing cross-check gets wrong. The no-op check
// would answer 200 with revision 2 beside Key accounts, a group revision 2
// never had.
//
// Without a revision, the caller asked for the change unconditionally, so the
// handler reads again and proceeds with the consistent pair. The gate moves the
// customer into a THIRD group so the request still has a real change to make.
// The event's before must then name the group the retry read, not the one the
// first read saw.
//
// Not parallel, and the subtests are not either: awaitLockWaiters counts
// across the database.
func TestPutCustomersByIdGroup_AMoveBetweenTheTwoReads(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	other := createGroup(t, c, map[string]any{"name": "Wholesale"})

	t.Run("with a revision it is the revision conflict", func(t *testing.T) {
		customer := createCustomer(t, c, "Between Reads Co")
		if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id, "revision": 1}); r.Status != http.StatusOK {
			t.Fatalf("set the starting group: status %d body %s", r.Status, r.Body)
		}

		r := moveBetweenTheTwoReads(t, h, c, customer.Id, map[string]any{"groupId": key.Id, "revision": 2}, key.Id)

		if r.Status != http.StatusConflict {
			t.Fatalf("status %d body %s, want 409: the two reads saw different revisions", r.Status, r.Body)
		}
		var problem conflictProblemJSON
		r.JSON(&problem)
		if problemTitle(problem.Title) != "Customer revision conflict" || problem.Code != nil {
			t.Errorf("conflict = title %q code %v, want the revision conflict with no code", problemTitle(problem.Title), problem.Code)
		}
		if n := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); n != 1 {
			t.Errorf("customer.group_changed events = %d, want 1: the request wrote nothing", n)
		}
	})

	t.Run("without a revision it reads again and applies", func(t *testing.T) {
		customer := createCustomer(t, c, "Between Reads Unconditional Co")
		if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
			t.Fatalf("set the starting group: status %d body %s", r.Status, r.Body)
		}

		// Revision 2 in Retail, then the gate's move makes it revision 3 in
		// Wholesale, and the request's own write makes it revision 4 in Key
		// accounts.
		r := moveBetweenTheTwoReads(t, h, c, customer.Id, map[string]any{"groupId": key.Id}, other.Id)

		if r.Status != http.StatusOK {
			t.Fatalf("status %d body %s, want 200: an omitted revision applies unconditionally", r.Status, r.Body)
		}
		var answered customerJSON
		r.JSON(&answered)
		if answered.Group == nil || answered.Group.Id != key.Id || answered.Revision != 4 {
			t.Errorf("answered group %+v revision %d, want Key accounts at revision 4", answered.Group, answered.Revision)
		}
		if rev := h.Count(t, `SELECT revision FROM customers.customers WHERE id = $1 AND group_id = $2`, customer.Id, key.Id); rev != 4 {
			t.Errorf("stored revision in Key accounts = %d, want 4", rev)
		}
		if n := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); n != 2 {
			t.Errorf("customer.group_changed events = %d, want 2: the starting move and this request's", n)
		}
		moved := fetchTimelineEvent(t, h, customer.Id, "customer.group_changed")
		if moved.Summary != "Moved from Wholesale to Key accounts" {
			t.Errorf("summary = %q, want %q: before is the group the consistent read saw", moved.Summary, "Moved from Wholesale to Key accounts")
		}
	})
}
