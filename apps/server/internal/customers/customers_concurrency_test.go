package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the customer row's optimistic-concurrency token (customers
// foundation design D5): revision starts at 1, every one of the row's five
// writes (PUT /customers/{id}, PUT .../type, archive, and the legal-identity
// PUT/DELETE) advances it by exactly one, an update naming a stale revision
// is refused with a 409 rather than applied, and a no-op PUT — one that
// changes nothing at all — bumps nothing, the same as it always has. The
// database-level guard behind the 409 (UpdateCustomer/SetCustomerType's
// WHERE ... AND (expected_revision IS NULL OR revision = expected_revision))
// is pinned by the concurrency test at the bottom, modelled on
// timeline_concurrency_test.go: forced to overlap at the database with a
// lock gate, not a sleep-and-hope race, since only that proves the guard is
// the database's own row locking and not something that merely got lucky
// with Go's goroutine scheduler.

func TestGetCustomer_ShowsRevisionOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Revision Co")
	got := fetchCustomerJSON(t, c, created.Id)
	if got.Revision != 1 {
		t.Fatalf("revision = %d, want 1", got.Revision)
	}
}

// TestGetCustomers_ListIncludesRevision proves GetCustomers's row →
// response mapping (customers.go's fromListRow/safeCustomerResponse) carries
// revision too, not just GetCustomer's single-row path (customers
// foundation design D4's one ListCustomers query, D5's revision).
func TestGetCustomers_ListIncludesRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Listed Co")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Listed Co Renamed", "revision": 1,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}

	r = c.Do(http.MethodGet, "/api/v1/customers?search=Listed", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list customerListJSON
	r.JSON(&list)
	if len(list.Data) != 1 {
		t.Fatalf("data = %d entries, want 1", len(list.Data))
	}
	if list.Data[0].Revision != 2 {
		t.Errorf("list row revision = %d, want 2", list.Data[0].Revision)
	}
}

func TestPutCustomersById_WithCorrectRevision_SucceedsAndBumpsRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Acme Renamed", "revision": 1,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Revision != 2 {
		t.Errorf("response revision = %d, want 2", updated.Revision)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 2 {
		t.Errorf("persisted revision = %d, want 2", got.Revision)
	}
}

// TestPutCustomersById_WithStaleRevision_ReturnsConflictAndLeavesRowUntouched
// proves the 409 and, more importantly, that nothing about the row moved: a
// mutation that only checked the revision but still executed the write
// (or one that swallowed the Go-side pre-check entirely and relied on the
// database guard alone) would still leave every one of these assertions
// green if it forgot even one of them, so all four are checked together —
// and the exact wording (not just the status code) is what distinguishes
// the Go-side pre-check from the database guard's own fallback wording,
// exactly as timeline_concurrency_test.go's message assertions do.
func TestPutCustomersById_WithStaleRevision_ReturnsConflictAndLeavesRowUntouched(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	before := fetchCustomerJSON(t, c, created.Id)
	h.Advance(time.Second)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Acme Renamed", "revision": 999,
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Customer revision conflict" {
		t.Errorf("Title = %q, want %q", problemTitle(problem.Title), "Customer revision conflict")
	}
	wantDetail := "The customer has been changed since revision 999 was read; it is now at revision 1."
	if problemTitle(problem.Detail) != wantDetail {
		t.Errorf("Detail = %q, want %q", problemTitle(problem.Detail), wantDetail)
	}

	after := fetchCustomerJSON(t, c, created.Id)
	if after.Name != before.Name {
		t.Errorf("name = %q, want unchanged %q", after.Name, before.Name)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updatedAt changed from %s to %s on a refused update", before.UpdatedAt, after.UpdatedAt)
	}
	if after.Revision != before.Revision {
		t.Errorf("revision = %d, want unchanged %d", after.Revision, before.Revision)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = 'customer.updated'`, created.Id); n != 0 {
		t.Errorf("customer.updated events = %d, want 0", n)
	}
}

// TestPutCustomersById_WithoutRevision_Succeeds proves the recorded exchange
// corpus (openapi/testdata/exchanges/customers.jsonl, frozen — customers
// foundation design D5) still validates: its PUT /customers/{id} exchanges
// predate the revision field, so a caller that never sends one must still
// go through exactly as before.
func TestPutCustomersById_WithoutRevision_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{"name": "Acme Renamed"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Name != "Acme Renamed" {
		t.Errorf("name = %q, want %q", updated.Name, "Acme Renamed")
	}
	if updated.Revision != 2 {
		t.Errorf("revision = %d, want 2", updated.Revision)
	}
}

// TestPutCustomersById_NoOp_DoesNotBumpRevision is the no-op rule (customers
// foundation design D5): a PUT that changes neither the name, status nor
// identity writes nothing at all, so revision (like updated_at already did)
// stays exactly where it was.
func TestPutCustomersById_NoOp_DoesNotBumpRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	before := fetchCustomerJSON(t, c, created.Id)
	h.Advance(time.Second)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Acme", "revision": 1,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	after := fetchCustomerJSON(t, c, created.Id)
	if after.Revision != before.Revision {
		t.Errorf("revision = %d, want unchanged %d", after.Revision, before.Revision)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updatedAt changed from %s to %s on a no-op PUT", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestPutCustomersByIdType_WithCorrectRevision_SucceedsAndBumpsRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{
		"type": "person", "revision": 1,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Revision != 2 {
		t.Errorf("revision = %d, want 2", updated.Revision)
	}
}

func TestPutCustomersByIdType_WithStaleRevision_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{
		"type": "person", "revision": 999,
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Customer revision conflict" {
		t.Errorf("Title = %q, want %q", problemTitle(problem.Title), "Customer revision conflict")
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 1 {
		t.Errorf("persisted revision = %d, want unchanged 1", got.Revision)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = 'customer.type_changed'`, created.Id); n != 0 {
		t.Errorf("customer.type_changed events = %d, want 0", n)
	}
}

// TestPutCustomersByIdType_StaleRevision_IsAConflictEvenWhenResubmittingTheCurrentType
// proves the controller ruling's ordering: the revision guard runs before
// the "resubmitting the current type is a no-op" check, so a stale revision
// against an unchanged type is still refused, not silently accepted.
func TestPutCustomersByIdType_StaleRevision_IsAConflictEvenWhenResubmittingTheCurrentType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{
		"type": "business", "revision": 999,
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409 (a no-op request is still refused on a stale revision)", r.Status, r.Body)
	}
}

func TestPutCustomersByIdType_WithoutRevision_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{"type": "person"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Revision != 2 {
		t.Errorf("revision = %d, want 2", updated.Revision)
	}
}

func TestDeleteCustomersById_BumpsRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Archivable")
	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 2 {
		t.Errorf("revision = %d, want 2", got.Revision)
	}
}

func TestPutCustomersByIdLegalIdentity_BumpsRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), map[string]any{
		"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "brreg",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 2 {
		t.Errorf("revision = %d, want 2", got.Revision)
	}
}

func TestDeleteCustomersByIdLegalIdentity_BumpsRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "brreg",
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)

	r = c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 2 {
		t.Errorf("revision = %d, want 2", got.Revision)
	}
}

// TestPutCustomersById_ConcurrentUpdatesWithSameRevision_ExactlyOneWins pins
// UpdateCustomer's guarded WHERE clause (queries/customers.sql): two PUTs,
// both reading revision 1 before either writes (forced by a gate lock taken
// on the customer row before either request starts, so both requests'
// Go-side pre-check — body.Revision != existing.Revision — sees the same,
// still-current revision 1 and neither is rejected by it) — only the
// database's row-level locking decides a winner once the gate releases.
// Dropping "AND (expected_revision IS NULL OR revision = expected_revision)"
// from that UPDATE's WHERE clause would let both PUTs succeed (two 200s,
// revision incremented twice, no 409 at all), which this test's "exactly
// one winner" assertion catches.
func TestPutCustomersById_ConcurrentUpdatesWithSameRevision_ExactlyOneWins(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Race Co")

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.customers WHERE id = $1 FOR UPDATE`, created.Id); err != nil {
		t.Fatalf("gate: lock the customer row: %v", err)
	}

	updateWith := func(name string) func() *modtest.Response {
		return func() *modtest.Response {
			return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
				"name": name, "revision": 1,
			})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(updateWith("Race Co — writer A"), updateWith("Race Co — writer B"))
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	var winners, losers int
	for _, r := range responses {
		switch r.Status {
		case http.StatusOK:
			winners++
		case http.StatusConflict:
			losers++
			var problem problemDetailsJSON
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
