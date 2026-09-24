package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

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
