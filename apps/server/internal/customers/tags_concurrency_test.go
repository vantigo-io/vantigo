package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the two races PUT /customers/{id}/tags lives with.
//
// The first is the one it cannot resolve by reading first (Task 4 review
// finding): the tag ids are resolved on the pool,
// deliberately without a lock on the vocabulary (owner and tags design D2), so
// a tag deleted in the window between that resolve and the insert inside the
// transaction raises a foreign-key violation (23503 on
// customer_tags_tag_id_fkey) on a set the resolve had just called valid.
// Unmapped, that is a 500 for something the caller can act on, and the pre-read
// exists precisely so this is a field error on tagIds.
//
// The race is forced rather than hoped for, the technique of every other
// *_concurrency_test.go here: a gate transaction holds FOR UPDATE on the tag
// row, which conflicts with the FOR KEY SHARE the insert's foreign-key check
// takes, so the request queues there (confirmed through pg_stat_activity, never
// a sleep) — and the gate then deletes the tag and commits, which is the exact
// interleaving no amount of reading ahead can prevent. Not parallel, because
// awaitLockWaiters counts lock waiters across the whole database.
//
// The second is the set replace against itself (final fix wave C1): the replace
// is a DELETE followed by an INSERT, and under READ COMMITTED two of them on one
// customer are not last-wins at all unless something serializes them — the
// second transaction's DELETE waits on the first's row locks, resumes with a
// statement snapshot that predates the first's INSERT, deletes nothing, and
// trips the primary key on the ids the two sets share. That is a 500 for a
// contract (the yaml's PutCustomerTagsRequest, docs/customers.md, design D2)
// that promises last-wins, which is why the transaction's first statement is
// LockCustomer's FOR NO KEY UPDATE on the customer row. The lock is what
// TestPutCustomersByIdTags_ConcurrentReplacesSerialize_LastWins below pins.

// gateTagLock opens a transaction holding FOR UPDATE on one tag row and answers
// the function that deletes that tag and commits, releasing whatever queued
// behind it into a vocabulary that no longer holds the tag. It is
// gateCustomerLock (addresses_concurrency_test.go) aimed at customers.tags,
// with the delete folded into the release: the point here is not that the
// request waited, it is what it finds once it stops waiting.
func gateTagLock(t *testing.T, h *modtest.Harness, tagID uuid.UUID) (deleteAndRelease func()) {
	t.Helper()
	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.tags WHERE id = $1 FOR UPDATE`, tagID); err != nil {
		t.Fatalf("gate: lock the tag row: %v", err)
	}
	return func() {
		if _, err := gate.Exec(ctx, `DELETE FROM customers.tags WHERE id = $1`, tagID); err != nil {
			t.Fatalf("gate: delete the tag: %v", err)
		}
		if err := gate.Commit(ctx); err != nil {
			t.Fatalf("gate: release: %v", err)
		}
	}
}

// TestPutCustomersByIdTags_ATagDeletedMidWriteIsAFieldError pins the mapping:
// the request names a tag that exists when it is resolved and is gone by the
// time the insert's foreign-key check runs, and the answer is the same 400 on
// tagIds the resolve itself would have given — indistinguishable to a caller,
// who has no use for the difference between "already gone" and "gone a
// millisecond later". Dropping the IsForeignKeyViolation branch from
// PutCustomersByIdTags turns this into a 500, which the status assertion below
// catches.
func TestPutCustomersByIdTags_ATagDeletedMidWriteIsAFieldError(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	vanishing := createTag(t, c, map[string]any{"name": "Vanishing"})
	kept := createTag(t, c, map[string]any{"name": "Kept"})
	customer := createCustomer(t, c, "Vanishing Tag Co")

	deleteAndRelease := gateTagLock(t, h, uuid.MustParse(vanishing.Id))

	done := make(chan *modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- putCustomerTags(t, c, customer.Id, []string{kept.Id, vanishing.Id})
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
	// Only the tag that vanished is named: the other id in the same request
	// still resolves, so the message set is the resolve's own, not "the whole
	// set is invalid".
	want := fmt.Sprintf("Tag %s does not exist", vanishing.Id)
	if got := problem.Errors["tagIds"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[tagIds] = %v, want [%s]", got, want)
	}
	// The transaction rolled back whole: the surviving tag was not quietly
	// linked on the way to the refusal.
	if got := fetchCustomerJSON(t, c, customer.Id); len(got.Tags) != 0 {
		t.Errorf("tags = %+v after the refusal, want none written", got.Tags)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.tags_changed"); n != 0 {
		t.Errorf("customer.tags_changed events = %d, want 0 (nothing was written)", n)
	}
}

// TestPutCustomersByIdTags_ConcurrentReplacesSerialize_LastWins pins C1's
// serialization point: two replaces of one customer's set, overlapping (both
// keep Shared, each adds its own second tag), forced to overlap at the database
// by gateCustomerLock (addresses_concurrency_test.go) — whose FOR UPDATE on the
// customer row conflicts with the FOR NO KEY UPDATE the transaction now takes
// first, so both requests queue there rather than racing each other's DELETE and
// INSERT.
//
// Last-wins is asserted as last-wins and not merely as "one of the two": the
// writer the lock admitted second wrote its event second, so it carries the
// higher identity id, and the tag ITS request added is the tag the customer is
// left carrying beside Shared. Both requests answer 200 — neither is rejected,
// nothing is a conflict here — and each records exactly one
// customer.tags_changed.
//
// Removing LockCustomer from the transaction fails this test: the two replaces
// then interleave, the later one's DELETE sees a snapshot without the earlier
// one's rows, and its INSERT violates customer_tags_pkey on Shared — a 500, and
// before that awaitLockWaiters never sees two requests waiting on the gate at
// all, since without the lock nothing in the transaction touches the customer
// row.
func TestPutCustomersByIdTags_ConcurrentReplacesSerialize_LastWins(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	shared := createTag(t, c, map[string]any{"name": "Shared"})
	mine := createTag(t, c, map[string]any{"name": "Mine"})
	yours := createTag(t, c, map[string]any{"name": "Yours"})
	customer := createCustomer(t, c, "Concurrent Replace Co")
	// Seeded so the two sets actually overlap: it is the shared id that the
	// second writer's INSERT collides on when nothing serializes the two.
	if r := putCustomerTags(t, c, customer.Id, []string{shared.Id}); r.Status != http.StatusOK {
		t.Fatalf("seeding the shared tag: status %d body %s, want 200", r.Status, r.Body)
	}

	release := gateCustomerLock(t, h, customer.Id)

	replaceWith := func(other tagSummaryJSON) func() *modtest.Response {
		return func() *modtest.Response {
			return putCustomerTags(t, c, customer.Id, []string{shared.Id, other.Id})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(replaceWith(mine), replaceWith(yours))
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	for i, r := range responses {
		if r.Status != http.StatusOK {
			t.Errorf("response %d: status %d body %s, want 200", i, r.Status, r.Body)
		}
	}

	// One event per writer, plus the seed's own: nothing recorded twice and
	// nothing lost.
	if n := countTimelineEvents(t, h, customer.Id, "customer.tags_changed"); n != 3 {
		t.Errorf("customer.tags_changed events = %d, want 3 (the seed's plus one per writer)", n)
	}
	lastAdded := modtest.One[string](t, h, `
		SELECT payload_json->'added'->0->>'tagId'
		FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.tags_changed'
		ORDER BY id DESC LIMIT 1`, customer.Id)

	got := fetchCustomerJSON(t, c, customer.Id)
	have := make(map[string]bool, len(got.Tags))
	for _, tag := range got.Tags {
		have[tag.Id] = true
	}
	if len(have) != 2 || !have[shared.Id] || !have[lastAdded] {
		t.Errorf("tags = %+v, want exactly %s (shared) and %s (the tag the later writer added)",
			got.Tags, shared.Id, lastAdded)
	}
}
