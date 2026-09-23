package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports Integration/CustomersEndpointsTests.cs (customers
// inventory §7). Two of its 24 tests are out of Task 6's scope despite
// their names: UpdateCustomer_WithoutIdentity_RemovesExistingIdentity
// actually calls DELETE .../legal-identity, and
// UpdateCustomer_WithUnreadableIdentityPayload_ReturnsSanitizedProblemDetails
// calls PUT .../legal-identity — both Task 8 operations, deferred to that
// task's port. OpenApiDocument_ForV1_ContainsVersionedCustomerPaths is host
// infrastructure (a served OpenAPI document) with no Go equivalent and is
// dropped, as customers inventory §7 marks similar cases. That leaves 21
// ported here.
//
// A few assertions the .NET test also made through the dedicated
// GET/PUT .../legal-identity endpoints (Task 8) are instead made against
// the persisted row directly, with a fixture query, since Task 6's contract
// surface (SafeCustomerResponse's identity sub-object) only exposes
// country/type/id, never name/source.

// customerJSON decodes SafeCustomerResponse (Task 6's contract surface);
// legalIdentityJSON only ever appears as customerJSON.Identity.
// Revision decodes as a plain int32, not a pointer, even though
// SafeCustomerResponse's schema marks it optional (corpus compatibility,
// customers foundation design D5): every response this module's own
// handlers build always sets it, the same convention customer_type_test.go's
// typedCustomerJSON.Type already follows for that field. ContactInfo
// (invoice-ready customer design D2) decodes as a plain struct, not a
// pointer either, for the same reason: the server always sends it now.
type customerJSON struct {
	Id             int32             `json:"id"`
	CustomerNumber int64             `json:"customerNumber"`
	Name           string            `json:"name"`
	Status         string            `json:"status"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
	Revision       int32             `json:"revision"`
	Identity       *legalIdentityRef `json:"identity"`
	ContactInfo    contactInfoJSON   `json:"contactInfo"`
	Owner          *ownerJSON        `json:"owner"`
	Group          *groupRefJSON     `json:"group"`
	Tags           []tagJSON         `json:"tags"`
}

// ownerJSON decodes CustomerOwner, a pointer on customerJSON because it is
// genuinely absent for an unowned customer (owner and tags design D1) — unlike
// contactInfo, which the server always sends.
type ownerJSON struct {
	UserId      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
}

// groupRefJSON decodes CustomerGroupRef, a pointer on customerJSON for the same
// reason ownerJSON is one: it is genuinely absent for a customer in no group
// (customer groups design D3), never null.
type groupRefJSON struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// tagJSON decodes CustomerTag. A plain slice, not a pointer: the server always
// sends tags, empty array included, so a nil here means the field was missing
// and that is a failure worth seeing as one (owner and tags design D2).
type tagJSON struct {
	Id    string  `json:"id"`
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

// contactInfoJSON decodes CustomerContactInfo: each of the three fields
// nullable (invoice-ready customer design D2).
type contactInfoJSON struct {
	Email   *string `json:"email"`
	Phone   *string `json:"phone"`
	Website *string `json:"website"`
}

type legalIdentityRef struct {
	Country string `json:"country"`
	Type    string `json:"type"`
	Id      string `json:"id"`
}

type createdCustomerJSON struct {
	Id             int32 `json:"id"`
	CustomerNumber int64 `json:"customerNumber"`
}

type validationProblemJSON struct {
	Errors map[string][]string `json:"errors"`
}

type customerListJSON struct {
	Data       []customerJSON `json:"data"`
	Pagination struct {
		Page            int  `json:"page"`
		PageSize        int  `json:"pageSize"`
		TotalCount      int  `json:"totalCount"`
		TotalPages      int  `json:"totalPages"`
		HasNextPage     bool `json:"hasNextPage"`
		HasPreviousPage bool `json:"hasPreviousPage"`
	} `json:"pagination"`
}

// createCustomer posts name with no other fields and returns the created id.
func createCustomer(t *testing.T, c *modtest.Client, name string) createdCustomerJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": name})
	if r.Status != http.StatusCreated {
		t.Fatalf("create customer %q: status %d body %s", name, r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)
	return created
}

// legalRow is the persisted legal_* columns for the fixture assertions Task
// 6's contract cannot make directly (see the file doc comment).
type legalRow struct {
	Name   string
	Source string
}

func fetchLegalRow(t *testing.T, h *modtest.Harness, id int32) legalRow {
	t.Helper()
	var row legalRow
	err := h.Pool().QueryRow(t.Context(), `SELECT legal_name, legal_source FROM customers.customers WHERE id = $1`, id).Scan(&row.Name, &row.Source)
	if err != nil {
		t.Fatalf("fetchLegalRow(%d): %v", id, err)
	}
	return row
}

// Ported from Integration/CustomersEndpointsTests.cs.
// CreateCustomer_WithNameOnly_ReturnsCreatedWithLocation.
func TestCreateCustomer_WithNameOnly_ReturnsCreatedWithLocation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Wayne Enterprises"})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)
	if created.Id <= 0 {
		t.Errorf("Id = %d, want > 0", created.Id)
	}
	want := fmt.Sprintf("/api/v1/customers/%d", created.Id)
	if got := r.Header("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// CreateCustomer_WithLegalIdentity_PersistsIdentity. The .NET test also
// fetches GET .../legal-identity to check name/source; that endpoint is
// Task 8's, so name/source are checked against the row directly instead.
func TestCreateCustomer_WithLegalIdentity_PersistsIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme",
		"identity": map[string]any{
			"country": "NO", "type": "Business", "id": "923609016", "name": "Acme AS", "source": "brreg",
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)

	got := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	var customer customerJSON
	got.JSON(&customer)
	if customer.Name != "Acme" {
		t.Errorf("Name = %q, want Acme", customer.Name)
	}
	if customer.Identity == nil {
		t.Fatal("Identity = nil, want a value: the caller holds legal-identity-view")
	}
	if customer.Identity.Country != "no" || customer.Identity.Type != "business" || customer.Identity.Id != "923609016" {
		t.Errorf("Identity = %+v, want {no business 923609016}", customer.Identity)
	}
	if row := fetchLegalRow(t, h, created.Id); row.Name != "Acme AS" || row.Source != "brreg" {
		t.Errorf("persisted legal name/source = %+v, want {Acme AS brreg}", row)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// CreateCustomer_WithInvalidLegalId_ReturnsBadRequestWithFieldError.
func TestCreateCustomer_WithInvalidLegalId_ReturnsBadRequestWithFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	for _, legalID := range []string{"", "   "} {
		r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
			"name": "Acme",
			"identity": map[string]any{
				"country": "no", "type": "business", "id": legalID, "name": "Acme AS", "source": "manual",
			},
		})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("legalId %q: status %d body %s, want 400", legalID, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if _, ok := problem.Errors["identity.id"]; !ok {
			t.Errorf("legalId %q: errors = %v, want a key \"identity.id\"", legalID, problem.Errors)
		}
	}
}

// Not a port: customers foundation design D2's Norwegian organisation-number
// rule, exercised through PostCustomers's nested-identity error keying
// (errs["identity."+field], customers.go) the same way the blank/too-long
// case above exercises validateLegalID's generic rule. "123456789" passes
// that generic rule (nine digits, well under the length cap) but fails the
// mod-11 check digit only country "no" and type "business" together trigger.
func TestCreateCustomer_WithInvalidNorwegianOrgNumber_ReturnsBadRequestWithFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "123456789", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A Norwegian organisation number must be nine digits with a valid check digit, but was '123456789'"
	if got := problem.Errors["identity.id"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[\"identity.id\"] = %v, want [%q]", got, want)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// CreateCustomer_WithEmptyName_ReturnsBadRequestWithFieldError.
func TestCreateCustomer_WithEmptyName_ReturnsBadRequestWithFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": ""})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly one field", problem.Errors)
	}
	if _, ok := problem.Errors["name"]; !ok {
		t.Errorf("errors = %v, want a key \"name\"", problem.Errors)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// CreateCustomer_WithMultipleInvalidFields_ReportsAllErrorsAtOnce.
func TestCreateCustomer_WithMultipleInvalidFields_ReportsAllErrorsAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "",
		"identity": map[string]any{
			"country": "", "type": "business", "id": "  ", "name": "Acme AS", "source": "manual",
		},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := []string{"identity.country", "identity.id", "name"}
	got := make([]string, 0, len(problem.Errors))
	for k := range problem.Errors {
		got = append(got, k)
	}
	if !sameSet(got, want) {
		t.Errorf("error keys = %v, want %v", got, want)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// CreateCustomer_WithTooLongLegalName_ReturnsBadRequest.
func TestCreateCustomer_WithTooLongLegalName_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Acme",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": strings.Repeat("a", 256), "source": "manual",
		},
	})
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400", r.Status, r.Body)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// CreateCustomer_WithInvalidLegalSource_ReturnsBadRequestWithFieldError.
func TestCreateCustomer_WithInvalidLegalSource_ReturnsBadRequestWithFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	for _, source := range []string{"", "   ", "bogus", "BRREG!"} {
		r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
			"name": "Acme",
			"identity": map[string]any{
				"country": "no", "type": "business", "id": "923609016", "name": "Acme AS", "source": source,
			},
		})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("source %q: status %d body %s, want 400", source, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors) != 1 {
			t.Fatalf("source %q: errors = %v, want exactly one field", source, problem.Errors)
		}
		if _, ok := problem.Errors["identity.source"]; !ok {
			t.Errorf("source %q: errors = %v, want a key \"identity.source\"", source, problem.Errors)
		}
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// CreateCustomer_WithValidLegalSource_PersistsNormalizedSource. The .NET
// test fetches GET .../legal-identity (Task 8) to observe the normalized
// source; here the row is read directly instead.
func TestCreateCustomer_WithValidLegalSource_PersistsNormalizedSource(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	// Each iteration creates its own customer, so each needs its own valid
	// org number (customers foundation design D6) — the same identity twice
	// would now be a 409, and that is not what this test is about.
	for _, tc := range []struct{ source, orgNumber string }{
		{"brreg", "923609016"},
		{"Manual", "810000007"},
	} {
		source := tc.source
		r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
			"name": "Sourced",
			"identity": map[string]any{
				"country": "no", "type": "business", "id": tc.orgNumber, "name": "Sourced AS", "source": source,
			},
		})
		if r.Status != http.StatusCreated {
			t.Fatalf("source %q: status %d body %s, want 201", source, r.Status, r.Body)
		}
		var created createdCustomerJSON
		r.JSON(&created)
		if row := fetchLegalRow(t, h, created.Id); row.Source != strings.ToLower(source) {
			t.Errorf("source %q: persisted source = %q, want %q", source, row.Source, strings.ToLower(source))
		}
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomer_WhenCustomerDoesNotExist_ReturnsNotFound.
func TestGetCustomer_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers/999999", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestGetCustomer_WithoutLegalIdentityViewPermission_OmitsIdentity is not a
// port (customers inventory §7 has no dedicated permission-matrix class in
// Task 6's ported set — CustomersPermissionIntegrationTests.cs would cover
// it), but pins the same response-shaping behaviour §6 documents
// ("Business view exposed twice") that /stats already has coverage for
// (TestStats_OmitsIdentityFigures_WithoutLegalIdentityViewPermission):
// GetCustomerEndpoint.cs:51-52's includeIdentity re-check. A caller who can
// view the customer but not its legal identity must never see it, even
// though one exists.
func TestGetCustomer_WithoutLegalIdentityViewPermission_OmitsIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	create := owner.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Hidden Identity Co",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Hidden Identity AS", "source": "manual",
		},
	})
	var created createdCustomerJSON
	create.JSON(&created)

	viewer := h.SignIn(t, "customers:view") // no legal-identity-view
	r := viewer.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var customer customerJSON
	r.JSON(&customer)
	if customer.Identity != nil {
		t.Errorf("Identity = %+v, want nil without legal-identity-view, even though the customer has one", customer.Identity)
	}
}

// TestGetCustomers_WithoutLegalIdentityViewPermission_OmitsIdentity is the
// list-endpoint counterpart, GetCustomersEndpoint.cs:37-38/90-97's
// includeIdentity re-check.
func TestGetCustomers_WithoutLegalIdentityViewPermission_OmitsIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	owner.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Hidden Identity List Co",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Hidden Identity List AS", "source": "manual",
		},
	})

	viewer := h.SignIn(t, "customers:view") // no legal-identity-view
	r := viewer.Do(http.MethodGet, "/api/v1/customers?search="+url.QueryEscape("Hidden Identity List Co"), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list customerListJSON
	r.JSON(&list)
	if len(list.Data) != 1 {
		t.Fatalf("data = %+v, want exactly one entry", list.Data)
	}
	if list.Data[0].Identity != nil {
		t.Errorf("Identity = %+v, want nil without legal-identity-view, even though the customer has one", list.Data[0].Identity)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// UpdateCustomer_WithValidName_UpdatesName.
func TestUpdateCustomer_WithValidName_UpdatesName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Initech")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{"name": "Initrode"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Id != created.Id || updated.Name != "Initrode" {
		t.Errorf("updated = %+v, want id %d name Initrode", updated, created.Id)
	}

	fetched := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	var refetched customerJSON
	fetched.JSON(&refetched)
	// reflect.DeepEqual rather than ==: customerJSON carries the tags array
	// (owner and tags design D2), so it is no longer a comparable struct — the
	// assertion is still "the whole response, field for field".
	if !reflect.DeepEqual(refetched, updated) {
		t.Errorf("refetched = %+v, want the same as the update response %+v", refetched, updated)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// UpdateCustomer_WithIdentity_ReplacesIdentity. The .NET test's
// GET .../legal-identity assertions (Task 8) are replaced with a direct row
// check for name/source; country/type/id are still observable through
// SafeCustomerResponse.
func TestUpdateCustomer_WithIdentity_ReplacesIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Replaceable")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Replaceable",
		"identity": map[string]any{
			"country": "NO", "type": "Business", "id": "923609016", "name": "Replaceable AS", "source": "brreg",
		},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Identity == nil || updated.Identity.Country != "no" || updated.Identity.Type != "business" || updated.Identity.Id != "923609016" {
		t.Errorf("Identity = %+v, want {no business 923609016}", updated.Identity)
	}
	if row := fetchLegalRow(t, h, created.Id); row.Name != "Replaceable AS" || row.Source != "brreg" {
		t.Errorf("persisted legal name/source = %+v, want {Replaceable AS brreg}", row)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// UpdateCustomer_WithInvalidName_ReturnsBadRequestWithFieldError.
func TestUpdateCustomer_WithInvalidName_ReturnsBadRequestWithFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Update Validation Co")

	for _, name := range []string{"", "   "} {
		r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{"name": name})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("name %q: status %d body %s, want 400", name, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors) != 1 {
			t.Fatalf("name %q: errors = %v, want exactly one field", name, problem.Errors)
		}
		if _, ok := problem.Errors["name"]; !ok {
			t.Errorf("name %q: errors = %v, want a key \"name\"", name, problem.Errors)
		}
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// UpdateCustomer_WhenCustomerDoesNotExist_ReturnsNotFound.
func TestUpdateCustomer_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPut, "/api/v1/customers/999999", map[string]any{"name": "Ghost Corp"})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestUpdateCustomer_InvalidIdentityAgainstMissingCustomer_Returns404 is not
// a port: no .NET test in this class combines an invalid identity with a
// nonexistent customer id (the two candidates with "Identity" and
// "DoesNotExist" in their names each turn out, on inspection, to exercise
// the Task 8 legal-identity endpoints instead — see the file doc comment).
// It pins customers inventory §1.4's UpdateCustomer ordering directly: name/
// status win before the 404 check, but identity is only re-validated after
// it, so an invalid identity against a missing id answers 404, not 400.
func TestUpdateCustomer_InvalidIdentityAgainstMissingCustomer_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPut, "/api/v1/customers/999999", map[string]any{
		"name": "Ghost Corp",
		"identity": map[string]any{
			"country": "", "type": "business", "id": "923609016", "name": "Ghost AS", "source": "manual",
		},
	})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404 (existence wins over the deferred identity validation)", r.Status, r.Body)
	}
}

// TestUpdateCustomer_InvalidNameAgainstMissingCustomer_Returns400 is the
// other half of inventory §1.4's decisive pair, and is not a port either:
// name/status validation runs and wins BEFORE the existence check (unlike
// identity, re-validated only after it), so an invalid name against a
// missing id answers 400, not 404 — the mirror image of
// TestUpdateCustomer_InvalidIdentityAgainstMissingCustomer_Returns404 above.
// A handler that moved name/status validation to after the lookup would
// still pass every other ported test; only this one catches it.
func TestUpdateCustomer_InvalidNameAgainstMissingCustomer_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPut, "/api/v1/customers/999999", map[string]any{"name": ""})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (validation wins over the missing id)", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["name"]; !ok {
		t.Errorf("errors = %v, want a key \"name\"", problem.Errors)
	}
}

// TestUpdateCustomer_OmittedIdentityPreservesExisting pins
// UpdateCustomerEndpoint.cs:71-87's actual behaviour: request.Identity is
// only read when present; when it is omitted, customerIdentity keeps the
// value it was seeded with, customer.Identity, so the persisted identity is
// left untouched. This is not what the endpoint's own doc comment claims
// ("removed"), and is not a port: no .NET test exercises PUT-with-no-identity
// against a customer that already has one (the test named for that claim,
// UpdateCustomer_WithoutIdentity_RemovesExistingIdentity, calls
// DELETE .../legal-identity instead — a Task 8 operation, see the file doc
// comment above). The code, not the doc comment, is the port's ground truth.
func TestUpdateCustomer_OmittedIdentityPreservesExisting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Identity Persists")

	seed := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Identity Persists",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Persists AS", "source": "manual",
		},
	})
	if seed.Status != http.StatusOK {
		t.Fatalf("seed update: status %d body %s, want 200", seed.Status, seed.Body)
	}

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{"name": "Identity Persists Renamed"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Name != "Identity Persists Renamed" {
		t.Errorf("Name = %q, want Identity Persists Renamed", updated.Name)
	}
	if updated.Identity == nil || updated.Identity.Country != "no" || updated.Identity.Type != "business" || updated.Identity.Id != "923609016" {
		t.Errorf("Identity = %+v, want it preserved as {no business 923609016}", updated.Identity)
	}
	if row := fetchLegalRow(t, h, created.Id); row.Name != "Persists AS" || row.Source != "manual" {
		t.Errorf("persisted legal name/source = %+v, want them preserved as {Persists AS manual}", row)
	}
}

// TestUpdateCustomer_OmittedIdentityNeverRevalidatesAStoredInvalidOne pins
// customers foundation design D2's explicit carve-out: "Validation applies
// to writes only. Rows already stored are not re-validated, and a PUT that
// leaves the identity unchanged (identity omitted) never trips over an old
// value." The only way to get such a row under the new rule is to predate
// it — the API itself now refuses to create one — so this seeds it directly
// with SQL, the way legal_identity_test.go's file doc comment describes for
// pre-Task-8 rows.
func TestUpdateCustomer_OmittedIdentityNeverRevalidatesAStoredInvalidOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Legacy Invalid Identity Co")

	h.Exec(t, `UPDATE customers.customers SET legal_country = 'no', legal_id = '123456789', legal_name = 'Legacy AS', legal_source = 'manual', legal_type = 'business' WHERE id = $1`,
		created.Id)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{"name": "Legacy Invalid Identity Co Renamed"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (an omitted identity is never re-validated)", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Identity == nil || updated.Identity.Id != "123456789" {
		t.Errorf("Identity = %+v, want the stored invalid identity preserved untouched", updated.Identity)
	}
}

// TestCreateCustomer_CustomerNumberSurvivesADeletedHighNumberedCustomer pins
// the counters upsert (NextCounterValue, customers inventory §3/§4): the
// next customer_number always continues from the persisted counter row,
// never from max(customer_number) recomputed over the live table. The two
// agree as long as no row ever leaves the table; customers.counters exists
// specifically to survive that. The module itself only ever archives
// (DeleteCustomersById), never hard-deletes, so the row is removed directly
// here only to prove the point: a `coalesce(max(customer_number), 1000) + 1`
// allocator would reuse a number once its holder is gone, exactly as this
// test's fixture insertCustomer helper does (deliberately, for tests that
// don't care) — the real allocator must not.
func TestCreateCustomer_CustomerNumberSurvivesADeletedHighNumberedCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	a := createCustomer(t, c, "Counter A")
	b := createCustomer(t, c, "Counter B")
	if b.CustomerNumber != a.CustomerNumber+1 {
		t.Fatalf("CustomerNumber sequence = %d, %d, want consecutive", a.CustomerNumber, b.CustomerNumber)
	}

	h.Exec(t, `DELETE FROM customers.customers WHERE id = $1`, b.Id)

	cc := createCustomer(t, c, "Counter C")
	if cc.CustomerNumber != b.CustomerNumber+1 {
		t.Errorf("CustomerNumber = %d, want %d: it must continue the counter, not recompute max(customer_number) over the live table (which, with B gone, would go backward)",
			cc.CustomerNumber, b.CustomerNumber+1)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomers_ReturnsCreatedCustomers.
func TestGetCustomers_ReturnsCreatedCustomers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Stark Industries")

	r := c.Do(http.MethodGet, "/api/v1/customers?search="+url.QueryEscape("Stark Industries"), nil)
	var list customerListJSON
	r.JSON(&list)

	found := false
	for _, item := range list.Data {
		if item.Id == created.Id {
			found = true
			if item.Name != "Stark Industries" || item.Identity != nil {
				t.Errorf("item = %+v, want name Stark Industries and no identity", item)
			}
		}
	}
	if !found {
		t.Errorf("data = %+v, want an entry for id %d", list.Data, created.Id)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomers_RepresentsCustomersIdenticallyToGetCustomer.
func TestGetCustomers_RepresentsCustomersIdenticallyToGetCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Globex",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "912345629", "name": "Globex AS", "source": "manual",
		},
	})
	var created createdCustomerJSON
	r.JSON(&created)

	single := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	var one customerJSON
	single.JSON(&one)

	list := c.Do(http.MethodGet, "/api/v1/customers?search="+url.QueryEscape("Globex"), nil)
	var listed customerListJSON
	list.JSON(&listed)

	var match *customerJSON
	for i := range listed.Data {
		if listed.Data[i].Id == created.Id {
			match = &listed.Data[i]
		}
	}
	if match == nil {
		t.Fatalf("data = %+v, want an entry for id %d", listed.Data, created.Id)
	}
	if match.Identity == nil || one.Identity == nil {
		t.Fatalf("listed.Identity = %+v, single.Identity = %+v, want both non-nil", match.Identity, one.Identity)
	}
	if match.Id != one.Id || match.Name != one.Name || *match.Identity != *one.Identity {
		t.Errorf("listed = %+v, want the same as GetCustomer %+v", match, one)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomers_WithoutParameters_AppliesDefaultPagination.
func TestGetCustomers_WithoutParameters_AppliesDefaultPagination(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomer(t, c, "Default Paging Co")

	r := c.Do(http.MethodGet, "/api/v1/customers", nil)
	var list customerListJSON
	r.JSON(&list)

	if list.Pagination.Page != 1 {
		t.Errorf("Page = %d, want 1", list.Pagination.Page)
	}
	if list.Pagination.PageSize != 25 {
		t.Errorf("PageSize = %d, want 25", list.Pagination.PageSize)
	}
	if list.Pagination.TotalCount <= 0 {
		t.Errorf("TotalCount = %d, want > 0", list.Pagination.TotalCount)
	}
	if list.Pagination.HasPreviousPage {
		t.Error("HasPreviousPage = true, want false")
	}
	if len(list.Data) > 25 {
		t.Errorf("len(Data) = %d, want <= 25", len(list.Data))
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomers_Pagination_ReturnsCorrectSlicesAndMetadata.
func TestGetCustomers_Pagination_ReturnsCorrectSlicesAndMetadata(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	for i := 1; i <= 5; i++ {
		createCustomer(t, c, fmt.Sprintf("Paged Corp %d", i))
	}

	get := func(page int) customerListJSON {
		r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers?search=%s&pageSize=2&page=%d&sortBy=name", url.QueryEscape("Paged Corp"), page), nil)
		var list customerListJSON
		r.JSON(&list)
		return list
	}

	first := get(1)
	second := get(2)
	last := get(3)

	if first.Pagination.TotalCount != 5 {
		t.Errorf("TotalCount = %d, want 5", first.Pagination.TotalCount)
	}
	if first.Pagination.TotalPages != 3 {
		t.Errorf("TotalPages = %d, want 3", first.Pagination.TotalPages)
	}
	if !namesEqual(first.Data, "Paged Corp 1", "Paged Corp 2") {
		t.Errorf("first page names = %v, want [Paged Corp 1 Paged Corp 2]", names(first.Data))
	}
	if !first.Pagination.HasNextPage || first.Pagination.HasPreviousPage {
		t.Errorf("first page pagination = %+v, want HasNextPage true, HasPreviousPage false", first.Pagination)
	}
	if !namesEqual(second.Data, "Paged Corp 3", "Paged Corp 4") {
		t.Errorf("second page names = %v, want [Paged Corp 3 Paged Corp 4]", names(second.Data))
	}
	if !second.Pagination.HasNextPage || !second.Pagination.HasPreviousPage {
		t.Errorf("second page pagination = %+v, want both true", second.Pagination)
	}
	if !namesEqual(last.Data, "Paged Corp 5") {
		t.Errorf("last page names = %v, want [Paged Corp 5]", names(last.Data))
	}
	if last.Pagination.HasNextPage || !last.Pagination.HasPreviousPage {
		t.Errorf("last page pagination = %+v, want HasNextPage false, HasPreviousPage true", last.Pagination)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomers_SortByNameDescending_ReturnsCustomersInDescendingOrder.
func TestGetCustomers_SortByNameDescending_ReturnsCustomersInDescendingOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomer(t, c, "Sorted Alpha")
	createCustomer(t, c, "Sorted Beta")
	createCustomer(t, c, "Sorted Gamma")

	r := c.Do(http.MethodGet, "/api/v1/customers?search=Sorted&sortBy=name&sortDirection=desc", nil)
	var list customerListJSON
	r.JSON(&list)
	if !namesEqual(list.Data, "Sorted Gamma", "Sorted Beta", "Sorted Alpha") {
		t.Errorf("names = %v, want [Sorted Gamma Sorted Beta Sorted Alpha]", names(list.Data))
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomers_Search_MatchesLegalNameAndLegalIdCaseInsensitively — but its
// original assertions (all Assert.Empty) pinned .NET's inventory oddity #2:
// search never actually matched the legal name or legal id, despite the
// test's own name. Customers foundation design D4 gives search that reach,
// for a caller who holds legal-identity-view (authenticatedClient does), so
// this test now asserts a match where the ported one asserted an absence.
// The permission-gated absence (a caller *without* legal-identity-view)
// moves to customers_list_test.go, alongside the rest of D4's search cases.
func TestGetCustomers_Search_MatchesLegalNameAndLegalId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Searchable",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Umbrella Norge AS", "source": "manual",
		},
	})

	byLegalName := c.Do(http.MethodGet, "/api/v1/customers?search="+url.QueryEscape("umbrella norge"), nil)
	byLegalID := c.Do(http.MethodGet, "/api/v1/customers?search=923609016", nil)
	noMatch := c.Do(http.MethodGet, "/api/v1/customers?search=no-such-customer", nil)

	var byName, byID, none customerListJSON
	byLegalName.JSON(&byName)
	byLegalID.JSON(&byID)
	noMatch.JSON(&none)

	if !namesEqual(byName.Data, "Searchable") {
		t.Errorf("search by legal name: names = %v, want [Searchable]", names(byName.Data))
	}
	if !namesEqual(byID.Data, "Searchable") {
		t.Errorf("search by legal id: names = %v, want [Searchable]", names(byID.Data))
	}
	if len(none.Data) != 0 || none.Pagination.TotalCount != 0 {
		t.Errorf("search with no match: data = %+v totalCount = %d, want empty/0", none.Data, none.Pagination.TotalCount)
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomers_WithInvalidQueryParameters_ReturnsBadRequest.
func TestGetCustomers_WithInvalidQueryParameters_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	for _, qs := range []string{"page=0", "page=-1", "pageSize=0", "pageSize=101", "sortBy=bogus", "sortDirection=bogus"} {
		r := c.Do(http.MethodGet, "/api/v1/customers?"+qs, nil)
		if r.Status != http.StatusBadRequest {
			t.Errorf("query %q: status %d body %s, want 400", qs, r.Status, r.Body)
		}
	}
}

// Ported from Integration/CustomersEndpointsTests.cs.
// GetCustomers_WithUnknownApiVersion_DoesNotResolve.
func TestGetCustomers_WithUnknownApiVersion_DoesNotResolve(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	for _, path := range []string{"/api/v2/customers", "/api/v9/customers"} {
		r := c.Do(http.MethodGet, path, nil, modtest.SkipContract("an unversioned/off-contract path, on purpose"))
		if r.Status != http.StatusNotFound {
			t.Errorf("path %q: status %d body %s, want 404", path, r.Status, r.Body)
		}
	}
}

// sameSet reports whether got and want contain the same elements,
// irrespective of order or duplicates.
func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	set := make(map[string]bool, len(want))
	for _, w := range want {
		set[w] = true
	}
	for _, g := range got {
		if !set[g] {
			return false
		}
	}
	return true
}

func names(items []customerJSON) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Name
	}
	return out
}

func namesEqual(items []customerJSON, want ...string) bool {
	got := names(items)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
