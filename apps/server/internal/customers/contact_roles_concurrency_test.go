package customers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the customer-row lock every role write takes first (typed
// contact roles design D2; queries/addresses.sql's LockCustomer): two
// association writes against the same customer serialize through that one
// FOR NO KEY UPDATE lock, so ux_customer_contact_roles_primary (migration
// 00025) is never even at risk of a transient double primary or a missing one.
// It forces the interleaving with the lock gate the addresses' own concurrency
// file uses — gateCustomerLock, race and awaitLockWaiters are all declared
// elsewhere in this package and reused here rather than written a fourth time —
// rather than trusting Go's scheduler to interleave two sequential calls
// unluckily enough.
//
// -race cannot catch what this pins: a database row lock is not a Go data race.
// Run with -count=10 or more to exercise the timing.

// TestPutAssociation_ConcurrentPrimaryTrue_ExactlyOneWinner is design D2's own
// test case ("a forced race of two primary:true writers — one wins, the other
// demotes it, never two primaries"). Both requests are serialized through the
// customer lock, so neither is rejected and both must answer 200; only the
// database's transaction ordering decides which contact ends up the primary
// billing contact, and exactly one must.
//
// A mutation dropping LockCustomer from the update handler lets both writes'
// inner queries interleave, and the result is either a 500 from an unhandled
// unique violation (applyRoles' demote/insert never expects one) or two rows
// marked primary at once — which the "exactly one primary" assertion catches
// either way.
func TestPutAssociation_ConcurrentPrimaryTrue_ExactlyOneWinner(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Concurrent Primary Co")
	first := createContact(t, c, map[string]any{"firstName": "Racer", "lastName": "Onesen"})
	second := createContact(t, c, map[string]any{"firstName": "Racer", "lastName": "Twosen"})
	third := createContact(t, c, map[string]any{"firstName": "Incumbent", "lastName": "Threesen"})

	// third holds billing first, so it is the primary both racers try to take.
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": third.Id, "title": "A", "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": first.Id, "title": "B", "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": second.Id, "title": "C", "roles": []any{map[string]any{"role": "billing"}}})

	release := gateCustomerLock(t, h, customer.Id)

	makePrimary := func(contactID int32, title string) func() *modtest.Response {
		return func() *modtest.Response {
			return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contactID), map[string]any{
				"title": title, "roles": []any{map[string]any{"role": "billing", "primary": true}},
			})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(makePrimary(first.Id, "B"), makePrimary(second.Id, "C"))
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

	if n := primaryCountOf(t, h, customer.Id, "billing"); n != 1 {
		t.Errorf("primary billing holders = %d, want exactly 1", n)
	}
	winner := primaryHolderOf(t, h, customer.Id, "billing")
	if winner != first.Id && winner != second.Id {
		t.Errorf("primary billing holder = %d, want one of the two racers (%d or %d) — the incumbent must have been demoted", winner, first.Id, second.Id)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1 AND role = 'billing'`, customer.Id); n != 3 {
		t.Errorf("billing role rows = %d, want 3 (a race changes who is primary, never who holds the role)", n)
	}
}

// TestDetachTheOnlyHolder_RacesAttachingANewOne pins the same lock across the
// two other role writes: detaching the role's only (and therefore primary)
// holder while a new contact is attached with that role. Two orderings exist,
// both serialized, never interleaved — the detach first, so the new contact is
// the role's first holder and primary whatever it asked; or the attach first,
// so it joins as a plain member and the detach then promotes it as the only
// remaining one. Either way exactly one contact holds billing and it is
// primary: never two primaries, and never a holder with no primary among them.
func TestDetachTheOnlyHolder_RacesAttachingANewOne(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Detach Races Attach Co")
	leaving := createContact(t, c, map[string]any{"firstName": "Leaving", "lastName": "Racersen"})
	joining := createContact(t, c, map[string]any{"firstName": "Joining", "lastName": "Racersen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": leaving.Id, "title": "A", "roles": []any{map[string]any{"role": "billing"}}})

	release := gateCustomerLock(t, h, customer.Id)

	detach := func() *modtest.Response {
		return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, leaving.Id), nil)
	}
	attach := func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
			"contactId": joining.Id, "title": "B", "roles": []any{map[string]any{"role": "billing"}},
		})
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(detach, attach)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	if responses[0].Status != http.StatusNoContent {
		t.Errorf("detach: status %d body %s, want 204", responses[0].Status, responses[0].Body)
	}
	if responses[1].Status != http.StatusOK {
		t.Errorf("attach: status %d body %s, want 200", responses[1].Status, responses[1].Body)
	}

	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1 AND role = 'billing'`, customer.Id); n != 1 {
		t.Fatalf("billing role rows = %d, want exactly 1 (the leaver's gone, the joiner's there)", n)
	}
	if n := primaryCountOf(t, h, customer.Id, "billing"); n != 1 {
		t.Errorf("primary billing holders = %d, want exactly 1 (never zero with a holder present)", n)
	}
	if got := primaryHolderOf(t, h, customer.Id, "billing"); got != joining.Id {
		t.Errorf("primary billing holder = %d, want %d", got, joining.Id)
	}
}

// TestAttachAndDeleteContact_CrossedLockOrders forces the one lock cycle in this
// module: an attach takes the CUSTOMER row's lock and then the CONTACT row's
// (contacts.go's PostCustomersByIdContacts), while deleting a contact takes the
// contact row's lock first — it has to, that is the ported lock's purpose — and
// only then the row of every customer it must promote a new primary for. Two
// opposite orders, so the two can cycle, and PostgreSQL breaks a cycle by
// killing one side with 40P01.
//
// The gate is what makes the interleaving reachable, and it only is when the
// two requests want the SAME customer row: the delete queues for the rows of
// the customers the contact is ALREADY attached to, so the attach has to name
// one of those, which is why the fixture below attaches the contact to the
// gated customer first and the attach is then a second attach of a contact
// that is already there. (A fixture that attached the contact somewhere else
// and aimed the attach at a fresh customer never forms the cycle at all: the
// delete locks only the other customer, finishes without ever queueing behind
// the gate, and the attach is the single waiter — measured, not assumed.)
//
// So: while a third transaction holds the customer row, the delete gets the
// contact lock and then queues for the customer, and the attach queues for the
// customer too. Releasing the gate admits one of them:
//
//   - the delete wins the customer lock: it already holds the contact lock, so
//     it finishes, and the attach then finds no contact and answers 404; or
//   - the attach wins the customer lock: it now wants the contact lock the
//     delete holds, while the delete wants the customer lock the attach holds —
//     a cycle. One of the two is killed with 40P01, db.RetrySerializable
//     (contactRoleWriteAttempts) runs the loser again from a fresh snapshot,
//     and it 404s (the delete got there first) or 409s (the attach did, and the
//     association it is asked to create is the one that is already there) on the
//     second attempt.
//
// Which branch a run takes is the database's choice, so this asserts what is
// true of both: the delete always ends up done, the attach answers 404 or 409,
// and **neither ever answers 500**. That last one is the retry's whole
// observable effect. Measured, the cycle branch is the usual one and the DELETE
// is the side PostgreSQL kills — the attach queues for the customer row first
// and is granted it first, so the delete is the transaction whose lock request
// closes the cycle: take db.RetrySerializable off DeleteCustomersContactsById
// and it answers a 500 from `fmt.Errorf("customers: delete contact: %w", err)`
// in most runs, which is exactly what this test catches. Run it with -count=20:
// the cycle branch is likely, not certain.
func TestAttachAndDeleteContact_CrossedLockOrders(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Crossed Locks Co")
	target := createContact(t, c, map[string]any{"firstName": "Crossed", "lastName": "Locksen"})
	// Attached to the gated customer, so the delete really does queue behind
	// the gate for the very row the attach wants, and attached elsewhere too,
	// so the delete walks more than one customer row in ListAssociationsForContact's
	// ascending-id order rather than taking a single-lock short path.
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": target.Id, "title": "A",
		"roles": []any{map[string]any{"role": "billing"}}})
	other := createCustomer(t, c, "Crossed Other Co")
	attachWithRoles(t, c, other.Id, map[string]any{"contactId": target.Id, "title": "A",
		"roles": []any{map[string]any{"role": "billing"}}})

	release := gateCustomerLock(t, h, customer.Id)

	attach := func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
			"contactId": target.Id, "title": "B", "roles": []any{map[string]any{"role": "project"}},
		})
	}
	del := func() *modtest.Response {
		return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", target.Id), nil)
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(attach, del)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done
	attachResp, deleteResp := responses[0], responses[1]

	if deleteResp.Status != http.StatusNoContent {
		t.Errorf("delete: status %d body %s, want 204", deleteResp.Status, deleteResp.Body)
	}
	if attachResp.Status != http.StatusNotFound && attachResp.Status != http.StatusConflict {
		t.Errorf("attach: status %d body %s, want 404 or 409 — never a 500, which is what an unretried 40P01 looks like", attachResp.Status, attachResp.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.contacts WHERE id = $1`, target.Id); n != 0 {
		t.Errorf("contact rows left = %d, want 0 (the delete always wins eventually)", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE contact_id = $1`, target.Id); n != 0 {
		t.Errorf("orphaned role rows = %d, want 0", n)
	}
}

// TestPartialIndexIsTheBackstop proves the database's own last word is really
// there (design D2): a second primary row for one (customer, role), inserted
// behind the handlers' backs, is refused by
// ux_customer_contact_roles_primary. Nothing in the module can reach this
// state — that is the point — so the only way to pin the index is to try it
// directly, exactly as the addresses' invariant is documented to rely on it.
func TestPartialIndexIsTheBackstop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Backstop Co")
	one := createContact(t, c, map[string]any{"firstName": "One", "lastName": "Backstopsen"})
	two := createContact(t, c, map[string]any{"firstName": "Two", "lastName": "Backstopsen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": one.Id, "title": "A", "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": two.Id, "title": "B", "roles": []any{map[string]any{"role": "billing"}}})

	_, err := h.Pool().Exec(t.Context(), `UPDATE customers.customer_contact_roles SET is_primary = true
	                                      WHERE customer_id = $1 AND contact_id = $2 AND role = 'billing'`, customer.Id, two.Id)
	if err == nil {
		t.Fatal("a second primary billing holder was accepted, want ux_customer_contact_roles_primary to refuse it")
	}
}
