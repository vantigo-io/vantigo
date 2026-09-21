package customers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the customer-row lock every address write takes first
// (invoice-ready customer design D3's controller ruling; queries/addresses.sql's
// LockCustomer): two address writes against the same customer serialize
// through that one FOR NO KEY UPDATE lock, so the partial unique index
// ux_customer_addresses_primary (00018_customers_addresses.sql) is never
// even at risk of a transient double-primary or a missing one — this file
// forces the interleaving with a lock gate exactly as
// customers_concurrency_test.go and contacts_concurrency_test.go do (a
// separate transaction takes FOR UPDATE on the customer row first, both
// racing requests queue behind it, confirmed via pg_stat_activity, never a
// sleep-and-hope race), rather than merely trusting Go's goroutine
// scheduler to interleave two sequential calls unluckily enough to expose a
// missing lock. race and awaitLockWaiters are contacts_concurrency_test.go's,
// reused here rather than declared a third time in this package.

// gateCustomerLock opens a transaction that holds FOR UPDATE on the
// customer row, the same lock mode customers_concurrency_test.go's own gate
// uses: it conflicts with LockCustomer's FOR NO KEY UPDATE (Postgres's row
// lock conflict table — FOR UPDATE conflicts with every other row lock
// mode), so every address write against customerID queues behind it until
// release is called.
func gateCustomerLock(t *testing.T, h *modtest.Harness, customerID int32) (release func()) {
	t.Helper()
	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.customers WHERE id = $1 FOR UPDATE`, customerID); err != nil {
		t.Fatalf("gate: lock the customer row: %v", err)
	}
	return func() {
		if err := gate.Commit(ctx); err != nil {
			t.Fatalf("gate: release: %v", err)
		}
	}
}

// TestPutCustomersByIdAddressesByAddressId_ConcurrentMakePrimary_ExactlyOneWinner
// pins the controller ruling: two different, non-primary addresses of the
// same type, both PUT with isPrimary:true at once, forced to overlap at the
// database by the customer-row lock gate. Both requests are serialized
// through that one lock — neither is rejected — so both must answer 200;
// only the database's own transaction ordering decides which address ends
// up primary, and exactly one must, whichever it is. A mutation dropping
// LockCustomer's FOR NO KEY UPDATE (or reordering demote-before-promote)
// would let both PUTs' inner queries interleave and either violate
// ux_customer_addresses_primary (surfacing as a 500 from an unhandled
// unique-violation, since addresses.go's demote/promote never expects
// one) or leave two rows marked primary at once, which this test's
// "exactly one primary" assertion catches either way.
func TestPutCustomersByIdAddressesByAddressId_ConcurrentMakePrimary_ExactlyOneWinner(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Concurrent Make Primary Co")
	createAddress(t, c, customer.Id, fullAddressBody("delivery", nil)) // forced primary, first of type
	x := createAddress(t, c, customer.Id, fullAddressBody("delivery", nil))
	y := createAddress(t, c, customer.Id, fullAddressBody("delivery", nil))

	release := gateCustomerLock(t, h, customer.Id)

	makePrimary := func(addressID int32) func() *modtest.Response {
		return func() *modtest.Response {
			return putAddress(t, c, customer.Id, addressID, fullAddressBody("delivery", boolPtr(true)))
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(makePrimary(x.Id), makePrimary(y.Id))
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

	list := listAddresses(t, c, customer.Id)
	var primaries int
	for _, a := range list.Data {
		if a.IsPrimary {
			primaries++
		}
	}
	if len(list.Data) != 3 {
		t.Fatalf("len(data) = %d, want 3", len(list.Data))
	}
	if primaries != 1 {
		t.Errorf("primaries = %d, want exactly 1", primaries)
	}
}

// TestDeleteThePrimaryAddress_RacesAddFirstOfType pins the same lock across
// the two other address writes: DELETE of the type's only (and therefore
// primary) address racing a POST of a new address of that same type. Two
// valid orderings exist, both serialized through the customer lock, never
// interleaved:
//
//   - the delete wins first: the type is briefly empty, so the POST's
//     "first address of a type is always primary" rule (D3) makes the new
//     address primary regardless of what it asked for; or
//   - the POST wins first: the original address is still primary and still
//     present, so the new address joins as a plain (non-primary) member,
//     then the delete removes the original and promotes the new one (the
//     only remaining member of the type) in its place.
//
// Either way, exactly one address survives and it is primary — never two
// primaries, and never an address present with none of them primary.
func TestDeleteThePrimaryAddress_RacesAddFirstOfType(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Delete Races Add Co")
	original := createAddress(t, c, customer.Id, fullAddressBody("visiting", nil)) // the type's only, forced primary

	release := gateCustomerLock(t, h, customer.Id)

	del := func() *modtest.Response { return deleteAddress(t, c, customer.Id, original.Id) }
	add := func() *modtest.Response { return postAddress(t, c, customer.Id, fullAddressBody("visiting", nil)) }

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(del, add)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done
	delResp, addResp := responses[0], responses[1]

	if delResp.Status != http.StatusNoContent {
		t.Errorf("delete: status %d body %s, want 204", delResp.Status, delResp.Body)
	}
	if addResp.Status != http.StatusCreated {
		t.Errorf("add: status %d body %s, want 201", addResp.Status, addResp.Body)
	}

	list := listAddresses(t, c, customer.Id)
	if len(list.Data) != 1 {
		t.Fatalf("len(data) = %d, want exactly 1 (the original deleted, one new one added)", len(list.Data))
	}
	if !list.Data[0].IsPrimary {
		t.Errorf("the one remaining address is not primary, want it to be (never zero primaries with addresses present)")
	}
}
