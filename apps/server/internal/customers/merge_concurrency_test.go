package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file forces a merge's two races with a lock gate, the way
// contacts_concurrency_test.go does (race, awaitLockWaiters and
// addresses_concurrency_test.go's gateCustomerLock are reused, not declared
// again): a transaction holds a customer or a contact row, both racing
// requests queue behind it — confirmed through pg_stat_activity, never a sleep
// — and only then is the gate released.

// TestPostCustomersByIdMerge_TwoMergesOfOnePairRace_OneWinsOneIsAlreadyMerged:
// both merges queue on the lower id's lock (ascending order, design D3); the
// winner absorbs, and the other — reading the rows only once it holds both
// locks — finds the duplicate merged away and answers merge_already_merged. One
// merge event, never two, and never a 500.
func TestPostCustomersByIdMerge_TwoMergesOfOnePairRace_OneWinsOneIsAlreadyMerged(t *testing.T) {
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	release := gateCustomerLock(t, h, min(survivor, absorbed))

	merge := func() *modtest.Response {
		return postMerge(t, c, survivor, map[string]any{"sourceId": absorbed})
	}
	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(merge, merge)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	var won, refused int
	for _, r := range responses {
		switch r.Status {
		case http.StatusOK:
			won++
		case http.StatusConflict:
			refusedWith(t, r, "merge_already_merged")
			refused++
		default:
			t.Errorf("status %d body %s, want 200 or 409", r.Status, r.Body)
		}
	}
	if won != 1 || refused != 1 {
		t.Errorf("won %d, refused %d; want exactly one of each", won, refused)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE event_type = 'customer.merged'`); n != 1 {
		t.Errorf("customer.merged entries = %d, want 1", n)
	}
}

// TestPostCustomersByIdMerge_RacesTheDeleteOfAContactItMoves is the cycle
// mergeWriteAttempts exists for: a merge holds both customers and, moving the
// duplicate's association, wants a key-share on the contact; DELETE
// /customers/contacts/{id} holds that contact and wants the duplicate's row.
// PostgreSQL kills one; the retry runs it again from a fresh snapshot, so both
// requests succeed whichever order the database picked, and the contact ends
// up nowhere — never a 500, never an association left pointing at the
// duplicate.
//
// The gate is on the CONTACT row, not a customer's, and that is what makes
// this the merge's test: PostgreSQL kills whichever side of a cycle began its
// last wait first. Gated on the contact, the delete usually takes it when the
// gate lifts; the merge, already holding both customers, queues for its
// key-share first, and the delete only then queues for the duplicate — so the
// merge is the one killed, and only mergeWriteAttempts saves it. Gated on the
// duplicate's row instead, the delete would be the one that waited first, and
// the test would prove contactRoleWriteAttempts again.
func TestPostCustomersByIdMerge_RacesTheDeleteOfAContactItMoves(t *testing.T) {
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	contact := createContact(t, c, map[string]any{"firstName": "Race", "lastName": "Merger"}).Id
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": contact, "roles": []any{map[string]any{"role": "billing"}}})
	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.contacts WHERE id = $1 FOR UPDATE`, contact); err != nil {
		t.Fatalf("gate: lock the contact row: %v", err)
	}

	merge := func() *modtest.Response {
		return postMerge(t, c, survivor, map[string]any{"sourceId": absorbed})
	}
	del := func() *modtest.Response {
		return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", contact), nil)
	}
	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(merge, del)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	if r := responses[0]; r.Status != http.StatusOK {
		t.Errorf("merge: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := responses[1]; r.Status != http.StatusNoContent {
		t.Errorf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.contacts WHERE id = $1`, contact); n != 0 {
		t.Error("the contact survived its delete")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE contact_id = $1`, contact); n != 0 {
		t.Errorf("%d associations of the deleted contact remain", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND merged_into_customer_id = $2`, absorbed, survivor); n != 1 {
		t.Error("the duplicate is not marked merged")
	}
}

// inBackground runs fn on its own goroutine: done carries its response, and
// finished closes once it has one, for awaitLockWaiters to watch.
func inBackground(fn func() *modtest.Response) (done <-chan *modtest.Response, finished <-chan struct{}) {
	out := make(chan *modtest.Response, 1)
	closed := make(chan struct{})
	go func() {
		out <- fn()
		close(closed)
	}()
	return out, closed
}

// TestPostCustomersByIdMerge_AnEntryLandingBetweenItsMovesKeepsItsRevisions is
// MoveTimelineRevisions' own guarantee, underneath the locks: a revision follows
// its entry, never the absorbed customer's id. Every writer of an entry takes
// the customer's lock now and so cannot land between the merge's entry move and
// its revision move — so the test plays the writer that would: the gate holds
// the duplicate's one entry, the merge's entry move waits on it with its
// snapshot taken, and an entry with its revision is inserted (the POST's own
// statement, without the POST's lock) and committed before the gate lifts. The
// entry is not in the entry move's snapshot and stays; its revision is in the
// revision move's, and must stay with it.
func TestPostCustomersByIdMerge_AnEntryLandingBetweenItsMovesKeepsItsRevisions(t *testing.T) {
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.customers_timeline_entries WHERE customer_id = $1 FOR UPDATE`, absorbed); err != nil {
		t.Fatalf("gate: lock the duplicate's entries: %v", err)
	}

	done, finished := inBackground(func() *modtest.Response {
		return postMerge(t, c, survivor, map[string]any{"sourceId": absorbed})
	})
	awaitLockWaiters(t, h, 1, finished)
	late, err := store.New(h.Pool()).InsertManualTimelineEntry(ctx, store.InsertManualTimelineEntryParams{
		CustomerID: absorbed, EventType: "note", OccurredOn: pgtype.Date{Time: h.Now(), Valid: true},
		Summary: "Midt i", Note: "Midt i", ActorKind: "unattributed", ActorDisplay: "Unattributed", Now: h.Now(),
	})
	if err != nil {
		t.Fatalf("the late entry: %v", err)
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	if r := <-done; r.Status != http.StatusOK {
		t.Fatalf("merge: status %d body %s, want 200", r.Status, r.Body)
	}

	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions r
	                     JOIN customers.customers_timeline_entries e ON e.id = r.customer_timeline_entry_id
	                     WHERE r.customer_id <> e.customer_id`); n != 0 {
		t.Errorf("%d revisions disagree with their entry's customer", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1 AND customer_id = $2`, late.ID, absorbed); n != 1 {
		t.Errorf("the late entry's revision is not with its entry on the duplicate (%d rows there)", n)
	}
}

// queuedBehindAMerge runs a merge of absorbed into survivor and then write,
// both held at a gate on the duplicate's row, the merge queued first — so the
// write takes the lock only once the merge has committed — and answers both.
func queuedBehindAMerge(t *testing.T, h *modtest.Harness, c *modtest.Client, survivor, absorbed int32, write func() *modtest.Response) (merge, queued *modtest.Response) {
	t.Helper()
	release := gateCustomerLock(t, h, absorbed)
	merged, mergeFinished := inBackground(func() *modtest.Response {
		return postMerge(t, c, survivor, map[string]any{"sourceId": absorbed})
	})
	awaitLockWaiters(t, h, 1, mergeFinished)
	wrote, writeFinished := inBackground(write)
	awaitLockWaiters(t, h, 2, writeFinished)
	release()
	return <-merged, <-wrote
}

// TestPostCustomersByIdTimeline_QueuedBehindAMergeIsRefused: the entry POST
// takes the customer's lock like every other customer-scoped write, so it
// waits for a merge of the customer instead of landing inside it, and then
// finds the customer merged away — customer_merged, and no entry left behind
// on a customer nobody reads any more.
func TestPostCustomersByIdTimeline_QueuedBehindAMergeIsRefused(t *testing.T) {
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id

	merge, queued := queuedBehindAMerge(t, h, c, survivor, absorbed, func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", absorbed),
			map[string]any{"eventType": "note", "occurredOn": day(h, 0), "note": "Sent"})
	})

	if merge.Status != http.StatusOK {
		t.Fatalf("merge: status %d body %s, want 200", merge.Status, merge.Body)
	}
	refusedWith(t, queued, "customer_merged")
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND provenance = 'manual'`, absorbed); n != 0 {
		t.Errorf("%d manual entries landed on the merged-away customer", n)
	}
}

// TestPostCustomersByIdContacts_QueuedBehindAMergeIsRefused is the attach the
// merge design's testing section names: it queues on the duplicate's lock, as
// the merge does, and cannot cycle with it — and once the merge has committed
// it finds the customer merged away and attaches nothing (customer_merged),
// rather than an association on a customer whose page hides it.
func TestPostCustomersByIdContacts_QueuedBehindAMergeIsRefused(t *testing.T) {
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	contact := createContact(t, c, map[string]any{"firstName": "Sen", "lastName": "Kommer"}).Id

	merge, queued := queuedBehindAMerge(t, h, c, survivor, absorbed, func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", absorbed),
			map[string]any{"contactId": contact, "roles": []any{map[string]any{"role": "billing"}}})
	})

	if merge.Status != http.StatusOK {
		t.Fatalf("merge: status %d body %s, want 200", merge.Status, merge.Body)
	}
	refusedWith(t, queued, "customer_merged")
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE contact_id = $1`, contact); n != 0 {
		t.Errorf("the contact has %d associations, want none", n)
	}
}

// TestPostCustomersByIdMerge_RacesTheDeleteOfATagItMoves is the second cycle
// mergeWriteAttempts exists for: the merge's tag union holds the duplicate's
// customer_tags rows and wants a key-share on the tag, while DELETE
// /customers/tags/{tagId} holds the tag and, through its cascade, wants those
// rows. The gate is on the TAG, and the delete queues for it first, so it takes
// the tag when the gate lifts and the merge is the side PostgreSQL kills — the
// contact race's reasoning. Retried, both succeed, and the tag ends up nowhere.
func TestPostCustomersByIdMerge_RacesTheDeleteOfATagItMoves(t *testing.T) {
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	tag := createTag(t, c, map[string]any{"name": "VIP"})
	if r := putCustomerTags(t, c, absorbed, []string{tag.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.tags WHERE id = $1 FOR UPDATE`, tag.Id); err != nil {
		t.Fatalf("gate: lock the tag row: %v", err)
	}

	deleted, deleteFinished := inBackground(func() *modtest.Response {
		return c.Do(http.MethodDelete, "/api/v1/customers/tags/"+tag.Id, nil)
	})
	awaitLockWaiters(t, h, 1, deleteFinished)
	merged, mergeFinished := inBackground(func() *modtest.Response {
		return postMerge(t, c, survivor, map[string]any{"sourceId": absorbed})
	})
	awaitLockWaiters(t, h, 2, mergeFinished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}

	if r := <-merged; r.Status != http.StatusOK {
		t.Errorf("merge: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := <-deleted; r.Status != http.StatusNoContent {
		t.Errorf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_tags WHERE tag_id = $1`, tag.Id); n != 0 {
		t.Errorf("%d links to the deleted tag remain", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND merged_into_customer_id = $2`, absorbed, survivor); n != 1 {
		t.Error("the duplicate is not marked merged")
	}
}
