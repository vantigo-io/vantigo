package customers_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file pins the legal-identity-manage gate customers inventory
// §1.1/§1.4 documents for postCustomers and putCustomersById: "create
// (+legal-identity-manage iff identity supplied)" and "update+view
// (+legal-identity-manage iff identity supplied)". Neither operation's
// x-vantigo-access names it — module.Router enforces only the flat
// create/update+view rule — since whether identity is required at all
// depends on the request body, which the router never inspects. None of
// these are ports: no .NET test in customers inventory §7's list exercises
// this permission directly (CustomersPermissionIntegrationTests.cs, which
// would, is not in Task 6's ported set), so these are added to close the
// privilege gap a fix round found in the initial port.

// TestCreateCustomer_WithIdentityWithoutManagePermission_Returns403 pins
// CreateCustomerEndpoint.cs:38-42's 403 gate.
func TestCreateCustomer_WithIdentityWithoutManagePermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:create") // no legal-identity-manage

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
	if r.Code() != "forbidden" {
		t.Errorf("Code() = %q, want \"forbidden\"", r.Code())
	}
}

// TestCreateCustomer_WithIdentityAndManagePermission_Succeeds proves the
// gate only blocks a caller who actually lacks the permission.
func TestCreateCustomer_WithIdentityAndManagePermission_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:create", "customers:legal-identity-manage")

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusCreated {
		t.Errorf("status %d body %s, want 201", r.Status, r.Body)
	}
}

// TestCreateCustomer_InvalidNamePlusUnauthorizedIdentity_Returns403NotBadRequest
// is the ordering test: the permission gate runs before any field
// validation (CreateCustomerEndpoint.cs:38-47), so a request that would
// also fail name validation still answers 403, never 400 — the gate's
// position in the handler is load-bearing, not incidental.
func TestCreateCustomer_InvalidNamePlusUnauthorizedIdentity_Returns403NotBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:create") // no legal-identity-manage

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "", // also invalid, but must never be reached
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403 (the permission gate must run before name validation)", r.Status, r.Body)
	}
}

// TestUpdateCustomer_WithIdentityWithoutManagePermission_Returns403 pins
// UpdateCustomerEndpoint.cs:34-38's 403 gate.
func TestUpdateCustomer_WithIdentityWithoutManagePermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	created := createCustomer(t, owner, "Gate Candidate")

	c := h.SignIn(t, "customers:update", "customers:view") // no legal-identity-manage
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Gate Candidate",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
	if r.Code() != "forbidden" {
		t.Errorf("Code() = %q, want \"forbidden\"", r.Code())
	}
}

// TestUpdateCustomer_WithIdentityAndManagePermission_Succeeds proves the
// gate only blocks a caller who actually lacks the permission.
func TestUpdateCustomer_WithIdentityAndManagePermission_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	created := createCustomer(t, owner, "Gate Success Candidate")

	c := h.SignIn(t, "customers:update", "customers:view", "customers:legal-identity-manage")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Gate Success Candidate",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestUpdateCustomer_InvalidNamePlusUnauthorizedIdentity_Returns403NotBadRequest
// is the ordering test for update: the gate runs before name/status
// validation too.
func TestUpdateCustomer_InvalidNamePlusUnauthorizedIdentity_Returns403NotBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	created := createCustomer(t, owner, "Gate Ordering Candidate")

	c := h.SignIn(t, "customers:update", "customers:view") // no legal-identity-manage
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "", // also invalid, but must never be reached
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403 (the permission gate must run before name validation)", r.Status, r.Body)
	}
}

// TestUpdateCustomer_UnauthorizedIdentityAgainstMissingCustomer_Returns403NotFound
// is the ordering test that proves the gate precedes even the 404 lookup:
// identity supplied without legal-identity-manage against a nonexistent
// customer id answers 403, not 404 — the permission gate wins over
// existence, unlike name/status validation which loses to it (inventory
// §1.4).
func TestUpdateCustomer_UnauthorizedIdentityAgainstMissingCustomer_Returns403NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:update", "customers:view") // no legal-identity-manage

	r := c.Do(http.MethodPut, "/api/v1/customers/999999", map[string]any{
		"name": "Ghost Corp",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Ghost AS", "source": "manual",
		},
	})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403 (the permission gate must run before the existence check)", r.Status, r.Body)
	}
}
