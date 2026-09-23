package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the one race PUT /customers/{id}/tags cannot resolve by
// reading first (Task 4 review finding): the tag ids are resolved on the pool,
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
