package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the pessimistic row lock customers inventory §4 describes
// (AttachCustomerContactEndpoint.cs:42, DeleteContactEndpoint.cs:22; ported
// as GetContactForUpdate in contacts.go/queries/contacts.sql): a
// SELECT ... FOR UPDATE on the contacts row, serializing an attach against a
// concurrent delete of the same contact. No other test in this package
// depends on it — every other test here is single-threaded — so a Task 7
// review found that removing FOR UPDATE entirely changed nothing
// observable. This forces the interleaving with a lock gate, the same
// technique internal/identity's own row-lock tests use
// (invitations_test.go's TestInvitations_ConcurrentCreatesForOneEmailLeaveOneActive,
// twofactor_test.go's awaitLockWaiters): a separate transaction takes the
// lock first, both racing requests queue behind it (confirmed via
// pg_stat_activity, never a sleep-and-hope), and only then is the gate
// released.
//
// "Two concurrent writers against one association" (the fix round's
// phrasing) is realized here as attach vs. delete-the-underlying-contact,
// not two writers to one customers_contacts row directly: that is the actual
// race GetContactForUpdate's lock exists to serialize, per the .NET source's
// own comment and customers inventory oddity #10 (the lock targets the
// contacts row, not the association table, because the race it defends
// against is specifically attach-vs-contact-delete).
//
// -race cannot catch what this pins: a database row lock is not a Go data
// race. Run with -count=10 (or more) to exercise the timing; see the Task 7
// fix-round report for what was observed with the lock removed by hand.

// race runs fns at once, each released only when every one is ready, and
// returns their responses in the same order — internal/identity/users_test.go's
// race, duplicated here since it is unexported in a different package's
// _test.go file, not importable.
func race(fns ...func() *modtest.Response) []*modtest.Response {
	out := make([]*modtest.Response, len(fns))
	var ready, done sync.WaitGroup
	begin := make(chan struct{})
	for i, fn := range fns {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-begin
			out[i] = fn()
		}()
	}
	ready.Wait()
	close(begin)
	done.Wait()
	return out
}

// awaitLockWaiters waits until n backends on h's database are waiting on a
// lock, failing t if finished closes first or ten seconds pass —
// internal/identity/twofactor_test.go's awaitLockWaiters, duplicated for the
// same reason as race above.
func awaitLockWaiters(t *testing.T, h *modtest.Harness, n int, finished <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) < n {
		select {
		case <-finished:
			t.Fatalf("the requests answered without waiting on the gate's lock")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d requests ever waited on the gate's lock", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAttachContact_RacesDeleteContact_OnTheSameContactRow pins
// GetContactForUpdate's FOR UPDATE lock: a delete and an attach on the same
// contact, forced to overlap by a lock gate held on the contacts row before
// either request starts. Whichever request the lock admits first determines
// the only two valid outcomes:
//
//   - attach wins the lock first (200), commits the association, then delete
//     acquires the lock, observes that association, records its own
//     "removed" timeline event for it, and deletes the contact (204); or
//   - delete wins the lock first, deletes the contact outright (204, no
//     surviving association to record), then attach acquires the lock,
//     re-checks under it, finds no contact, and answers a clean 404 —
//     never a 500 from a foreign-key violation, which is what an attach
//     racing ahead of an uncoordinated delete would produce once the
//     delete's cascade removes the very row the attach just inserted an
//     association against.
//
// Both outcomes leave the contact and any association gone; only their
// order differs.
func TestAttachContact_RacesDeleteContact_OnTheSameContactRow(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Race", "lastName": "Rowsen"})
	customer := createCustomer(t, c, "Race Row Co")

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.contacts WHERE id = $1 FOR UPDATE`, contact.Id); err != nil {
		t.Fatalf("gate: lock the contact row: %v", err)
	}

	attach := func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
			"contactId": contact.Id, "title": "CEO",
		})
	}
	del := func() *modtest.Response {
		return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id), nil)
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(attach, del)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done
	attachResp, deleteResp := responses[0], responses[1]

	if deleteResp.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", deleteResp.Status, deleteResp.Body)
	}
	if attachResp.Status != http.StatusOK && attachResp.Status != http.StatusNotFound {
		t.Fatalf("attach: status %d body %s, want 200 or 404 (never anything else, and never a 500 from an unlocked race)", attachResp.Status, attachResp.Body)
	}

	if n := h.Count(t, `SELECT count(*) FROM customers.contacts WHERE id = $1`, contact.Id); n != 0 {
		t.Errorf("contact rows left = %d, want 0 (delete always wins eventually)", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE contact_id = $1`, contact.Id); n != 0 {
		t.Errorf("orphaned association rows = %d, want 0", n)
	}
	if attachResp.Status == http.StatusOK {
		if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = 'customer.contact_removed'`, customer.Id); n != 1 {
			t.Errorf("attach won the race but no customer.contact_removed event followed it; count = %d, want 1", n)
		}
	}
}
