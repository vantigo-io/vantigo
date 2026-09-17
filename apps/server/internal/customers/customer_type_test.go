package customers_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the customer type — business or person — that every
// customer carries independently of its legal identity: the default on
// create, the explicit choice on create, the guard that a legal identity
// must agree with it, PUT /customers/{id}'s refusal to touch it, and the
// dedicated PUT /customers/{id}/type operation that is the only way to
// change it afterwards.

type typedCustomerJSON struct {
	customerJSON
	Type string `json:"type"`
}

func getTypedCustomer(t *testing.T, c *modtest.Client, id int32) typedCustomerJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get customer %d: status %d body %s", id, r.Status, r.Body)
	}
	var customer typedCustomerJSON
	r.JSON(&customer)
	return customer
}

func TestCreateCustomer_WithoutType_DefaultsToBusiness(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Wayne Enterprises")
	if got := getTypedCustomer(t, c, created.Id); got.Type != "business" {
		t.Errorf("type = %q, want business", got.Type)
	}
}

func TestCreateCustomer_WithTypePerson_PersistsNormalizedType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Bruce Wayne", "type": " Person "})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)
	if got := getTypedCustomer(t, c, created.Id); got.Type != "person" {
		t.Errorf("type = %q, want person", got.Type)
	}
}

func TestCreateCustomer_WithInvalidType_ReturnsBadRequestWithFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme", "type": "spaceship"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A customer type must be one of 'business' or 'person', but was 'spaceship'"
	if msgs := problem.Errors["type"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[type] = %v, want [%q]", msgs, want)
	}
}

func TestCreateCustomer_WithIdentityOfTheOtherType_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Bruce Wayne",
		"type": "person",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Wayne Enterprises AS", "source": "brreg",
		},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A legal identity's type must match the customer type 'person', but was 'business'"
	if msgs := problem.Errors["identity.type"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[identity.type] = %v, want [%q]", msgs, want)
	}
}

func TestUpdateCustomer_IgnoresType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{"name": "Acme", "type": "person"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	if got := getTypedCustomer(t, c, created.Id); got.Type != "business" {
		t.Errorf("type = %q after PUT with type=person, want business (the type is only changed through PUT .../type)", got.Type)
	}
}

func TestUpdateCustomer_WithIdentityOfTheOtherType_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Acme",
		"identity": map[string]any{
			"country": "se", "type": "person", "id": "19770101-1234", "name": "Acme Person", "source": "manual",
		},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if msgs := problem.Errors["identity.type"]; len(msgs) != 1 {
		t.Errorf("errors[identity.type] = %v, want exactly one message", msgs)
	}
}

func TestPutCustomersByIdType_ChangesTypeAndRecordsEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	h.Advance(time.Second)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{"type": "Person"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated typedCustomerJSON
	r.JSON(&updated)
	if updated.Type != "person" {
		t.Errorf("type = %q, want person", updated.Type)
	}
	if got := getTypedCustomer(t, c, created.Id); got.Type != "person" {
		t.Errorf("persisted type = %q, want person", got.Type)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = 'customer.type_changed'`, created.Id); n != 1 {
		t.Errorf("customer.type_changed events = %d, want 1", n)
	}
}

func TestPutCustomersByIdType_SameType_IsANoOp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	before := getTypedCustomer(t, c, created.Id)
	h.Advance(time.Second)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{"type": "business"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	after := getTypedCustomer(t, c, created.Id)
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updatedAt changed from %s to %s on a same-type request", before.UpdatedAt, after.UpdatedAt)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = 'customer.type_changed'`, created.Id); n != 0 {
		t.Errorf("customer.type_changed events = %d, want 0", n)
	}
}

func TestPutCustomersByIdType_ClearsALegalIdentityOfTheOldType(t *testing.T) {
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
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)

	r = c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{"type": "person"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated typedCustomerJSON
	r.JSON(&updated)
	if updated.Type != "person" || updated.Identity != nil {
		t.Errorf("response = %+v, want type person and no identity", updated)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND legal_country IS NULL AND legal_id IS NULL AND legal_name IS NULL AND legal_source IS NULL AND legal_type IS NULL`, created.Id); n != 1 {
		t.Errorf("legal columns not all cleared for customer %d", created.Id)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = 'customer.updated'`, created.Id); n != 1 {
		t.Errorf("customer.updated events = %d, want 1 for the removed identity", n)
	}
}

func TestPutCustomersByIdType_WithInvalidType_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createCustomer(t, c, "Acme")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", created.Id), map[string]any{"type": ""})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if msgs := problem.Errors["type"]; len(msgs) != 1 || msgs[0] != "A customer type cannot be null or empty" {
		t.Errorf("errors[type] = %v, want the blank message", msgs)
	}
}

func TestPutCustomersByIdType_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPut, "/api/v1/customers/999999/type", map[string]any{"type": "person"})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestPutCustomersByIdType_WithoutUpdatePermission_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:view")

	r := c.Do(http.MethodPut, "/api/v1/customers/1001/type", map[string]any{"type": "person"})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

func TestStats_CountBusinessAndPersonByCustomerType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Business without identity"})
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Person without identity", "type": "person"})
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Person with identity", "type": "person",
		"identity": map[string]any{"country": "se", "type": "person", "id": "19770101-1234", "name": "Some Person", "source": "manual"},
	})

	r := c.Do(http.MethodGet, "/api/v1/customers/stats", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var stats statsJSON
	r.JSON(&stats)
	if stats.BusinessCount == nil || stats.PersonCount == nil || stats.MissingIdentityCount == nil {
		t.Fatalf("stats = %+v, want identity figures present", stats)
	}
	if *stats.BusinessCount != 1 || *stats.PersonCount != 2 || *stats.MissingIdentityCount != 2 {
		t.Errorf("stats = %+v, want BusinessCount=1 PersonCount=2 MissingIdentityCount=2", stats)
	}
}
