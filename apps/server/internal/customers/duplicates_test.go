package customers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is customers foundation design D6: a legal identity another
// customer already has is a conflict the caller can overrule
// (allowDuplicateIdentity). Not a port — no .NET test in customers
// inventory §7 exercises this, since the .NET Customers module never had a
// duplicate check at all (there was no unique index and no conflict
// response); this is new behaviour this port adds.

// conflictProblemJSON is CustomerConflictProblem's five conflict-specific
// fields (customers.go's problemDetailsJSON only decodes title/detail,
// which a duplicate conflict also carries, but this test file needs code
// and duplicates too).
type conflictProblemJSON struct {
	Title      *string                 `json:"title"`
	Detail     *string                 `json:"detail"`
	Code       *string                 `json:"code"`
	Status     *int32                  `json:"status"`
	Duplicates []conflictDuplicateJSON `json:"duplicates"`
}

type conflictDuplicateJSON struct {
	Id             int32  `json:"id"`
	CustomerNumber int64  `json:"customerNumber"`
	Name           string `json:"name"`
	Status         string `json:"status"`
}

// createCustomerWithIdentity posts a business customer named name holding
// (country, orgNumber) and returns the created id/customerNumber, failing
// the test on anything but 201.
func createCustomerWithIdentity(t *testing.T, c *modtest.Client, name, country, orgNumber string, opts ...map[string]any) createdCustomerJSON {
	t.Helper()
	body := map[string]any{
		"name": name,
		"identity": map[string]any{
			"country": country, "type": "business", "id": orgNumber, "name": name, "source": "manual",
		},
	}
	for _, o := range opts {
		for k, v := range o {
			body[k] = v
		}
	}
	r := c.Do(http.MethodPost, "/api/v1/customers", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create %q: status %d body %s, want 201", name, r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)
	return created
}

// TestPostCustomers_DuplicateLegalIdentity_ReturnsConflictNamingHolder pins
// the core of D6: a second customer created with an identity another
// customer already holds is refused, and the 409 names that first customer
// (id, customerNumber, name, status), never a bare "conflict, details
// withheld" response — the caller reached this handler holding
// legal-identity-manage, so naming the other customer leaks nothing it
// could not already look up.
func TestPostCustomers_DuplicateLegalIdentity_ReturnsConflictNamingHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	first := createCustomerWithIdentity(t, c, "Acme Holder", "no", "923609016")

	// Captured before the refused attempt: the duplicate check must abort
	// before NextCounterValue, and the whole write is one transaction, so
	// neither the counter nor the timeline should move at all.
	counterBefore := modtest.One[int64](t, h, `SELECT next_value FROM customers.counters WHERE counter_name = 'customer-number'`)
	timelineBefore := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme Copy",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme Copy AS", "source": "manual",
		},
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Duplicate legal identity" {
		t.Errorf("Title = %q, want %q", problemTitle(problem.Title), "Duplicate legal identity")
	}
	if problem.Code == nil || *problem.Code != "duplicate_legal_identity" {
		t.Errorf("Code = %v, want duplicate_legal_identity", problem.Code)
	}
	wantDetail := "Another customer already has this legal identity."
	if problemTitle(problem.Detail) != wantDetail {
		t.Errorf("Detail = %q, want %q", problemTitle(problem.Detail), wantDetail)
	}
	if len(problem.Duplicates) != 1 {
		t.Fatalf("duplicates = %d entries, want 1", len(problem.Duplicates))
	}
	got := problem.Duplicates[0]
	if got.Id != first.Id || got.CustomerNumber != first.CustomerNumber || got.Name != "Acme Holder" || got.Status != "active" {
		t.Errorf("duplicate = %+v, want id=%d customerNumber=%d name=Acme Holder status=active", got, first.Id, first.CustomerNumber)
	}

	// The refused create must not have burned the customer-number counter
	// or written a timeline event for the customer it never made.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Acme Copy'`); n != 0 {
		t.Errorf("customers named Acme Copy = %d, want 0 (refused create wrote nothing)", n)
	}
	if counterAfter := modtest.One[int64](t, h, `SELECT next_value FROM customers.counters WHERE counter_name = 'customer-number'`); counterAfter != counterBefore {
		t.Errorf("customer-number counter = %d, want unchanged %d (a refused create must not burn a number)", counterAfter, counterBefore)
	}
	if timelineAfter := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`); timelineAfter != timelineBefore {
		t.Errorf("timeline entries = %d, want unchanged %d (a refused create must write no event)", timelineAfter, timelineBefore)
	}
}

// TestPostCustomers_DuplicateLegalIdentity_WithAllowDuplicateIdentity_Succeeds
// proves the override: two departments of one company kept as separate
// customers is a legitimate use of allowDuplicateIdentity: true.
func TestPostCustomers_DuplicateLegalIdentity_WithAllowDuplicateIdentity_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	createCustomerWithIdentity(t, c, "Acme Holder", "no", "923609016")

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":                   "Acme Second Department",
		"allowDuplicateIdentity": true,
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE legal_id = '923609016'`); n != 2 {
		t.Errorf("customers holding 923609016 = %d, want 2", n)
	}
}

// TestPostCustomers_DuplicateLegalIdentity_ArchivedHolderStillConflicts
// proves an archived holder still conflicts (customers foundation design
// D6: "the right move is usually to restore it"), reported with status
// "archived" — never silently ignored because it is no longer active.
func TestPostCustomers_DuplicateLegalIdentity_ArchivedHolderStillConflicts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	first := createCustomerWithIdentity(t, c, "Retired Co", "no", "923609016")
	del := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", first.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s, want 204", del.Status, del.Body)
	}

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Retired Co Reborn",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Retired Co AS", "source": "manual",
		},
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if len(problem.Duplicates) != 1 || problem.Duplicates[0].Status != "archived" {
		t.Errorf("duplicates = %+v, want one entry with status archived", problem.Duplicates)
	}
}

// TestPostCustomers_SameIdDifferentCountry_NoConflict proves the check is
// keyed on (country, id) together, not id alone: the same id string held by
// a Norwegian business is no conflict at all for a Swedish one.
func TestPostCustomers_SameIdDifferentCountry_NoConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	createCustomerWithIdentity(t, c, "Norwegian Co", "no", "923609016")

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Swedish Co",
		"identity": map[string]any{
			"country": "se", "type": "business", "id": "923609016", "name": "Swedish Co AB", "source": "manual",
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201 (different country, no conflict)", r.Status, r.Body)
	}
}

// TestPutCustomersById_ResendingOwnUnchangedIdentity_Succeeds proves "an
// identity that is unchanged by the request is never checked" — including
// when another customer legitimately holds the very same identity through
// an earlier allowDuplicateIdentity override: resubmitting your own
// identity untouched must not suddenly 409 against that other holder just
// because some other field (here, the name) changed.
func TestPutCustomersById_ResendingOwnUnchangedIdentity_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	first := createCustomerWithIdentity(t, c, "Acme Original", "no", "923609016")
	createCustomerWithIdentity(t, c, "Acme Department Two", "no", "923609016", map[string]any{"allowDuplicateIdentity": true})

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", first.Id), map[string]any{
		"name":     "Acme Original Renamed",
		"revision": 1,
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme Original AS", "source": "manual",
		},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (identity unchanged, never checked)", r.Status, r.Body)
	}
}

// TestPutCustomersById_ChangingIdentityToATakenOne_ReturnsConflict proves
// the check also runs on update, not just create: a PUT that changes the
// identity to one another customer already holds is refused exactly like a
// create would be.
func TestPutCustomersById_ChangingIdentityToATakenOne_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	holder := createCustomerWithIdentity(t, c, "Taken Co", "no", "923609016")
	mover := createCustomerWithIdentity(t, c, "Mover Co", "no", "974760673")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", mover.Id), map[string]any{
		"name":     "Mover Co",
		"revision": 1,
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Mover Co AS", "source": "manual",
		},
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if len(problem.Duplicates) != 1 || problem.Duplicates[0].Id != holder.Id {
		t.Errorf("duplicates = %+v, want one entry naming customer %d", problem.Duplicates, holder.Id)
	}

	// The stale-looking revision guard was never triggered by this refusal —
	// the row must be exactly as it was, still at revision 1.
	after := fetchCustomerJSON(t, c, mover.Id)
	if after.Revision != 1 {
		t.Errorf("revision = %d, want unchanged 1", after.Revision)
	}
}

// TestPutCustomersById_StaleRevisionWithDuplicateIdentity_ReturnsRevisionConflict
// pins the controller ruling's order on PUT /customers/{id}: revision is
// checked before the identity is even looked at, so a request that is both
// stale and would-be duplicate answers the revision conflict, never the
// duplicate one — the caller must re-read before anything about the body,
// identity included, is worth judging.
func TestPutCustomersById_StaleRevisionWithDuplicateIdentity_ReturnsRevisionConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	createCustomerWithIdentity(t, c, "Taken Co", "no", "923609016")
	mover := createCustomerWithIdentity(t, c, "Mover Co", "no", "974760673")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", mover.Id), map[string]any{
		"name":     "Mover Co",
		"revision": 999,
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Mover Co AS", "source": "manual",
		},
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Customer revision conflict" {
		t.Errorf("Title = %q, want %q (the revision conflict, not the duplicate-identity one)", problemTitle(problem.Title), "Customer revision conflict")
	}
	if problem.Code != nil {
		t.Errorf("Code = %v, want nil (a revision conflict carries no code)", problem.Code)
	}
	if len(problem.Duplicates) != 0 {
		t.Errorf("duplicates = %+v, want none (a revision conflict names no one)", problem.Duplicates)
	}
}

// TestPutCustomersById_ChangingIdentityToATakenOne_WithAllowDuplicateIdentity_Succeeds
// proves the override applies to PutCustomersById itself, not just
// PostCustomers and the dedicated legal-identity PUT: the same three write
// paths D6 names all read allowDuplicateIdentity from their own request.
func TestPutCustomersById_ChangingIdentityToATakenOne_WithAllowDuplicateIdentity_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	createCustomerWithIdentity(t, c, "Taken Co", "no", "923609016")
	mover := createCustomerWithIdentity(t, c, "Mover Co", "no", "974760673")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", mover.Id), map[string]any{
		"name":                   "Mover Co",
		"revision":               1,
		"allowDuplicateIdentity": true,
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Mover Co AS", "source": "manual",
		},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE legal_id = '923609016'`); n != 2 {
		t.Errorf("customers holding 923609016 = %d, want 2", n)
	}
}

// TestPutCustomersByIdLegalIdentity_DuplicateLegalIdentity_ReturnsConflict
// and its override sibling below prove the same rule holds on the third
// write path, the dedicated legal-identity PUT (LegalIdentityEndpoints.Upsert).
func TestPutCustomersByIdLegalIdentity_DuplicateLegalIdentity_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	holder := createCustomerWithIdentity(t, c, "Held Co", "no", "923609016")
	mover := createCustomerWithIdentity(t, c, "Mover Co", "no", "974760673")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", mover.Id), map[string]any{
		"country": "no", "type": "business", "id": "923609016", "name": "Mover Co AS", "source": "manual",
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if len(problem.Duplicates) != 1 || problem.Duplicates[0].Id != holder.Id {
		t.Errorf("duplicates = %+v, want one entry naming customer %d", problem.Duplicates, holder.Id)
	}
}

func TestPutCustomersByIdLegalIdentity_DuplicateLegalIdentity_WithAllowDuplicateIdentity_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	createCustomerWithIdentity(t, c, "Held Co", "no", "923609016")
	mover := createCustomerWithIdentity(t, c, "Mover Co", "no", "974760673")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", mover.Id), map[string]any{
		"country": "no", "type": "business", "id": "923609016", "name": "Mover Co AS", "source": "manual",
		"allowDuplicateIdentity": true,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE legal_id = '923609016'`); n != 2 {
		t.Errorf("customers holding 923609016 = %d, want 2", n)
	}
}

// TestPostCustomers_DuplicateIdentityWithoutManagePermission_Returns403NotConflict
// proves the controller ruling's ordering: the legal-identity-manage 403
// gate wins over the 409 — a caller who cannot even attach an identity must
// never learn, from the shape of the refusal, whether the identity they
// tried collides with anything.
func TestPostCustomers_DuplicateIdentityWithoutManagePermission_Returns403NotConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := authenticatedClient(t, h)
	createCustomerWithIdentity(t, admin, "Acme Holder", "no", "923609016")

	c := h.SignIn(t, "customers:create") // no legal-identity-manage
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme Copy",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme Copy AS", "source": "manual",
		},
	})
	if r.Status != http.StatusForbidden {
		t.Fatalf("status %d body %s, want 403", r.Status, r.Body)
	}
	if r.Code() != "forbidden" {
		t.Errorf("Code() = %q, want \"forbidden\"", r.Code())
	}
}
