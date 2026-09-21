package customers_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is GET/POST /customers/{id}/addresses and
// PUT/DELETE /customers/{id}/addresses/{addressId} (invoice-ready customer
// design D1, D3): a customer's typed addresses. No .NET ancestor, since
// addresses are new to this port; shaped after contact_info_test.go and
// customer_type_test.go's coverage of this module's other sub-resource
// writes — one HTTP case per validation/business rule, the generated
// timeline events with their actor and payload, the permission gate, and
// list search/ordering. addresses_concurrency_test.go carries the
// lock-forced races.

type addressJSON struct {
	Id         int32     `json:"id"`
	Type       string    `json:"type"`
	Label      *string   `json:"label"`
	Line1      string    `json:"line1"`
	Line2      *string   `json:"line2"`
	PostalCode *string   `json:"postalCode"`
	City       *string   `json:"city"`
	Region     *string   `json:"region"`
	Country    string    `json:"country"`
	IsPrimary  bool      `json:"isPrimary"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type addressListJSON struct {
	Data []addressJSON `json:"data"`
}

func getAddresses(t *testing.T, c *modtest.Client, customerID int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/addresses", customerID), nil)
}

func postAddress(t *testing.T, c *modtest.Client, customerID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/addresses", customerID), body)
}

func putAddress(t *testing.T, c *modtest.Client, customerID, addressID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/addresses/%d", customerID, addressID), body)
}

func deleteAddress(t *testing.T, c *modtest.Client, customerID, addressID int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/addresses/%d", customerID, addressID), nil)
}

// createAddress posts body (expecting 201) and returns the decoded address.
func createAddress(t *testing.T, c *modtest.Client, customerID int32, body map[string]any) addressJSON {
	t.Helper()
	r := postAddress(t, c, customerID, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create address: status %d body %s, want 201", r.Status, r.Body)
	}
	var a addressJSON
	r.JSON(&a)
	return a
}

func listAddresses(t *testing.T, c *modtest.Client, customerID int32) addressListJSON {
	t.Helper()
	r := getAddresses(t, c, customerID)
	if r.Status != http.StatusOK {
		t.Fatalf("list addresses: status %d body %s, want 200", r.Status, r.Body)
	}
	var list addressListJSON
	r.JSON(&list)
	return list
}

// insertAddress inserts one address directly, bypassing the API — used only
// to seed bulk fixtures (the 50-address cap test) fast; every business-rule
// test goes through the HTTP handlers themselves.
func insertAddress(t *testing.T, h *modtest.Harness, customerID int32, addrType string, isPrimary bool) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO customers.customer_addresses (customer_id, type, line1, country, is_primary, created_at, updated_at)
		VALUES ($1, $2, 'Filler line', 'no', $3, $4, $4)
		RETURNING id`, customerID, addrType, isPrimary, h.Now())
}

func fullAddressBody(addrType string, isPrimary *bool) map[string]any {
	body := map[string]any{
		"type": addrType, "label": "HQ", "line1": "Storgata 1", "line2": "Suite 2",
		"postalCode": "0155", "city": "Oslo", "region": "Oslo", "country": "no",
	}
	if isPrimary != nil {
		body["isPrimary"] = *isPrimary
	}
	return body
}

func boolPtr(b bool) *bool { return &b }

// --- creation, first-of-type rule ------------------------------------------

func TestPostCustomersByIdAddresses_FirstOfType_IsPrimaryRegardlessOfRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "First Address Co")

	created := createAddress(t, c, customer.Id, fullAddressBody("postal", boolPtr(false)))
	if !created.IsPrimary {
		t.Errorf("IsPrimary = false, want true (first address of a type is always primary)")
	}
}

func TestPostCustomersByIdAddresses_IsPrimaryAbsent_DefaultsToFalse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Second Address Co")

	createAddress(t, c, customer.Id, fullAddressBody("postal", nil))
	second := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))
	if second.IsPrimary {
		t.Errorf("second address IsPrimary = true, want false (isPrimary absent means false)")
	}
}

func TestPostCustomersByIdAddresses_IsPrimaryTrue_DemotesThePreviousPrimary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Demote Co")

	first := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))
	second := createAddress(t, c, customer.Id, fullAddressBody("postal", boolPtr(true)))
	if !second.IsPrimary {
		t.Fatalf("second address IsPrimary = false, want true")
	}

	list := listAddresses(t, c, customer.Id)
	var primaries int
	for _, a := range list.Data {
		if a.Id == first.Id && a.IsPrimary {
			t.Errorf("first address is still primary after a second was made primary")
		}
		if a.IsPrimary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Errorf("primaries = %d, want exactly 1", primaries)
	}
}

func TestPostCustomersByIdAddresses_ReturnsCreatedWithLocation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Location Co")

	r := postAddress(t, c, customer.Id, fullAddressBody("invoice", nil))
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created addressJSON
	r.JSON(&created)
	want := fmt.Sprintf("/api/v1/customers/%d/addresses/%d", customer.Id, created.Id)
	if got := r.Header("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

func TestPostCustomersByIdAddresses_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := postAddress(t, c, 999999, fullAddressBody("postal", nil))
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestPostCustomersByIdAddresses_WithoutUpdatePermission_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:view")

	r := postAddress(t, c, 1001, fullAddressBody("postal", nil))
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

// --- validation --------------------------------------------------------

func TestPostCustomersByIdAddresses_InvalidType_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Bad Type Co")

	body := fullAddressBody("billing", nil)
	r := postAddress(t, c, customer.Id, body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An address type must be one of 'postal', 'invoice', 'delivery' or 'visiting', but was 'billing'"
	if msgs := problem.Errors["type"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[type] = %v, want [%q]", msgs, want)
	}
}

func TestPostCustomersByIdAddresses_BlankLine1_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Blank Line1 Co")

	body := fullAddressBody("postal", nil)
	body["line1"] = "   "
	r := postAddress(t, c, customer.Id, body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An address's first line cannot be null or empty"
	if msgs := problem.Errors["line1"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[line1] = %v, want [%q]", msgs, want)
	}
}

func TestPostCustomersByIdAddresses_InvalidCountry_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Bad Country Co")

	body := fullAddressBody("postal", nil)
	body["country"] = "xx"
	r := postAddress(t, c, customer.Id, body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A country code must be an ISO 3166-1 alpha-2 code, but was 'xx'"
	if msgs := problem.Errors["country"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[country] = %v, want [%q]", msgs, want)
	}
}

func TestPostCustomersByIdAddresses_NorwegianAddressWithoutPostalCodeOrCity_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "No Postal Co")

	r := postAddress(t, c, customer.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "country": "no"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if msgs := problem.Errors["postalCode"]; len(msgs) != 1 || msgs[0] != "A Norwegian address needs a four-digit postal code" {
		t.Errorf("errors[postalCode] = %v, want the four-digit message", msgs)
	}
	if msgs := problem.Errors["city"]; len(msgs) != 1 || msgs[0] != "A Norwegian address needs a city" {
		t.Errorf("errors[city] = %v, want the needs-a-city message", msgs)
	}
}

func TestPostCustomersByIdAddresses_NonNorwegianAddress_PostalCodeAndCityStayOptional(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Optional Postal Co")

	r := postAddress(t, c, customer.Id, map[string]any{"type": "postal", "line1": "1 Main St", "country": "us"})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
}

func TestPostCustomersByIdAddresses_LabelTooLong_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Long Label Co")

	body := fullAddressBody("postal", nil)
	long := make([]byte, 101)
	for i := range long {
		long[i] = 'a'
	}
	body["label"] = string(long)
	r := postAddress(t, c, customer.Id, body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A label cannot be longer than 100 characters, the given value was 101 characters"
	if msgs := problem.Errors["label"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[label] = %v, want [%q]", msgs, want)
	}
}

func TestPostCustomersByIdAddresses_AtCap_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Cap Co")

	insertAddress(t, h, customer.Id, "delivery", true)
	for i := 0; i < 49; i++ {
		insertAddress(t, h, customer.Id, "delivery", false)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_addresses WHERE customer_id = $1`, customer.Id); n != 50 {
		t.Fatalf("seeded %d addresses, want 50", n)
	}

	r := postAddress(t, c, customer.Id, fullAddressBody("visiting", nil))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A customer can have at most 50 addresses"
	if msgs := problem.Errors["addresses"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[addresses] = %v, want [%q]", msgs, want)
	}
}

// --- list ordering -------------------------------------------------------

// TestGetCustomersByIdAddresses_OrdersByTypeThenPrimaryThenId pins the
// controller ruling's exact ordering: type in the fixed order invoice,
// postal, delivery, visiting; primary first within a type; then id.
func TestGetCustomersByIdAddresses_OrdersByTypeThenPrimaryThenId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Ordering Co")

	visiting := createAddress(t, c, customer.Id, fullAddressBody("visiting", nil))
	postalFirst := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))
	postalSecond := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))
	invoice := createAddress(t, c, customer.Id, fullAddressBody("invoice", nil))
	// Make postalSecond primary so primary-first-within-type is exercised too.
	putR := putAddress(t, c, customer.Id, postalSecond.Id, fullAddressBody("postal", boolPtr(true)))
	if putR.Status != http.StatusOK {
		t.Fatalf("make postalSecond primary: status %d body %s, want 200", putR.Status, putR.Body)
	}

	list := listAddresses(t, c, customer.Id)
	if len(list.Data) != 4 {
		t.Fatalf("len(data) = %d, want 4", len(list.Data))
	}
	var gotIDs []int32
	for _, a := range list.Data {
		gotIDs = append(gotIDs, a.Id)
	}
	want := []int32{invoice.Id, postalSecond.Id, postalFirst.Id, visiting.Id}
	if len(gotIDs) != len(want) {
		t.Fatalf("ids = %v, want %v", gotIDs, want)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Errorf("ids = %v, want %v (invoice, then postal primary-first, then visiting)", gotIDs, want)
		}
	}
}

func TestGetCustomersByIdAddresses_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := getAddresses(t, c, 999999)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestGetCustomersByIdAddresses_WithOnlyViewPermission_Succeeds proves the
// D1 access split: reading addresses needs only customers:view, unlike
// writing them.
func TestGetCustomersByIdAddresses_WithOnlyViewPermission_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := authenticatedClient(t, h)
	customer := createCustomer(t, admin, "View Only Co")
	createAddress(t, admin, customer.Id, fullAddressBody("postal", nil))

	viewer := h.SignIn(t, "customers:view")
	r := getAddresses(t, viewer, customer.Id)
	if r.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestGetCustomersByIdAddresses_ArchivedCustomer_StillListsAddresses proves
// the controller ruling: archive blocks nothing in this module.
func TestGetCustomersByIdAddresses_ArchivedCustomer_StillListsAddresses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Archivable Address Co")
	createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	del := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", customer.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s, want 204", del.Status, del.Body)
	}

	list := listAddresses(t, c, customer.Id)
	if len(list.Data) != 1 {
		t.Errorf("len(data) = %d, want 1 (archive lists, not hides, addresses)", len(list.Data))
	}
}

// TestPutCustomersByIdAddressesByAddressId_ArchivedCustomer_StillEditable
// proves the write side of the same controller ruling.
func TestPutCustomersByIdAddressesByAddressId_ArchivedCustomer_StillEditable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Archivable Editable Co")
	address := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	del := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", customer.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s, want 204", del.Status, del.Body)
	}

	body := fullAddressBody("postal", boolPtr(true))
	body["line1"] = "Changed line 1"
	r := putAddress(t, c, customer.Id, address.Id, body)
	if r.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200", r.Status, r.Body)
	}
}

// --- PUT: same-type primary rules ---------------------------------------

func TestPutCustomersByIdAddressesByAddressId_FalseOnCurrentPrimary_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Keep Primary Co")
	address := createAddress(t, c, customer.Id, fullAddressBody("postal", nil)) // first of type: forced primary

	r := putAddress(t, c, customer.Id, address.Id, fullAddressBody("postal", boolPtr(false)))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An address that is the only or primary one of its type stays primary; make another one primary instead"
	if msgs := problem.Errors["isPrimary"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[isPrimary] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdAddressesByAddressId_MakePrimary_DemotesTheOtherOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Make Primary Co")
	first := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))
	second := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	r := putAddress(t, c, customer.Id, second.Id, fullAddressBody("postal", boolPtr(true)))
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated addressJSON
	r.JSON(&updated)
	if !updated.IsPrimary {
		t.Errorf("IsPrimary = false, want true")
	}

	list := listAddresses(t, c, customer.Id)
	primaries := 0
	for _, a := range list.Data {
		if a.IsPrimary {
			primaries++
		}
		if a.Id == first.Id && a.IsPrimary {
			t.Errorf("first address is still primary")
		}
	}
	if primaries != 1 {
		t.Errorf("primaries = %d, want exactly 1", primaries)
	}

	// Controller ruling: exactly one event, naming the address the request
	// was about, never a second one for the demoted address.
	if n := countTimelineEvents(t, h, customer.Id, "customer.address_updated"); n != 1 {
		t.Errorf("customer.address_updated events = %d, want 1", n)
	}
}

// --- PUT: type change ------------------------------------------------------

func TestPutCustomersByIdAddressesByAddressId_TypeChange_PromotesOldestRemainingInOldType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Type Change Co")
	primary := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))
	other := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	// Move the primary postal address to visiting (its own first, so forced
	// primary there too); the remaining postal address (other) must be
	// promoted since postal still has a member.
	body := fullAddressBody("visiting", nil)
	r := putAddress(t, c, customer.Id, primary.Id, body)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated addressJSON
	r.JSON(&updated)
	if updated.Type != "visiting" || !updated.IsPrimary {
		t.Errorf("updated = %+v, want type visiting, primary true (first of its new type)", updated)
	}

	list := listAddresses(t, c, customer.Id)
	var otherAfter *addressJSON
	for i := range list.Data {
		if list.Data[i].Id == other.Id {
			otherAfter = &list.Data[i]
		}
	}
	if otherAfter == nil || !otherAfter.IsPrimary {
		t.Errorf("other postal address = %+v, want promoted to primary", otherAfter)
	}
}

func TestPutCustomersByIdAddressesByAddressId_TypeChange_LeavesEmptyOldTypeWithNothingToPromote(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Empty Old Type Co")
	only := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	r := putAddress(t, c, customer.Id, only.Id, fullAddressBody("visiting", nil))
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (nothing left in postal to promote, and that must not error)", r.Status, r.Body)
	}
}

func TestPutCustomersByIdAddressesByAddressId_TypeChange_JoinsExistingTypeAsNonPrimaryByDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Join Existing Type Co")
	existingVisitingPrimary := createAddress(t, c, customer.Id, fullAddressBody("visiting", nil))
	moving := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	r := putAddress(t, c, customer.Id, moving.Id, fullAddressBody("visiting", nil)) // isPrimary absent -> false
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated addressJSON
	r.JSON(&updated)
	if updated.IsPrimary {
		t.Errorf("IsPrimary = true, want false (joining a type that already has a primary, isPrimary absent)")
	}

	list := listAddresses(t, c, customer.Id)
	for _, a := range list.Data {
		if a.Id == existingVisitingPrimary.Id && !a.IsPrimary {
			t.Errorf("existing visiting primary was demoted, want unchanged")
		}
	}
}

func TestPutCustomersByIdAddressesByAddressId_TypeChange_IsPrimaryTrue_DemotesNewTypesPrimary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Type Change Demote Co")
	existingVisitingPrimary := createAddress(t, c, customer.Id, fullAddressBody("visiting", nil))
	moving := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	r := putAddress(t, c, customer.Id, moving.Id, fullAddressBody("visiting", boolPtr(true)))
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}

	list := listAddresses(t, c, customer.Id)
	for _, a := range list.Data {
		if a.Id == existingVisitingPrimary.Id && a.IsPrimary {
			t.Errorf("existing visiting primary is still primary, want demoted")
		}
		if a.Id == moving.Id && !a.IsPrimary {
			t.Errorf("moved address is not primary, want true")
		}
	}
}

// --- PUT: full replace, not found, permissions --------------------------

func TestPutCustomersByIdAddressesByAddressId_ReplacesEveryField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Full Replace Co")
	address := createAddress(t, c, customer.Id, map[string]any{"type": "postal", "line1": "Old line", "country": "se"})

	r := putAddress(t, c, customer.Id, address.Id, fullAddressBody("postal", boolPtr(true)))
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated addressJSON
	r.JSON(&updated)
	if updated.Line1 != "Storgata 1" || updated.Label == nil || *updated.Label != "HQ" ||
		updated.PostalCode == nil || *updated.PostalCode != "0155" || updated.Country != "no" {
		t.Errorf("updated = %+v, want every field replaced", updated)
	}
}

func TestPutCustomersByIdAddressesByAddressId_WhenAddressDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Missing Address Co")

	r := putAddress(t, c, customer.Id, 999999, fullAddressBody("postal", nil))
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestPutCustomersByIdAddressesByAddressId_AddressBelongsToAnotherCustomer_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customerA := createCustomer(t, c, "Owner Co")
	customerB := createCustomer(t, c, "Other Co")
	address := createAddress(t, c, customerA.Id, fullAddressBody("postal", nil))

	r := putAddress(t, c, customerB.Id, address.Id, fullAddressBody("postal", nil))
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404 (an address id valid for a different customer)", r.Status, r.Body)
	}
}

func TestPutCustomersByIdAddressesByAddressId_WithoutUpdatePermission_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:view")

	r := putAddress(t, c, 1001, 1, fullAddressBody("postal", nil))
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

// --- DELETE ---------------------------------------------------------------

func TestDeleteCustomersByIdAddressesByAddressId_RemovesIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Delete Co")
	address := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	r := deleteAddress(t, c, customer.Id, address.Id)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}
	list := listAddresses(t, c, customer.Id)
	if len(list.Data) != 0 {
		t.Errorf("len(data) = %d, want 0", len(list.Data))
	}
}

func TestDeleteCustomersByIdAddressesByAddressId_DeletingThePrimary_PromotesTheOldestRemaining(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Promote On Delete Co")
	primary := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))
	other := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	r := deleteAddress(t, c, customer.Id, primary.Id)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}

	list := listAddresses(t, c, customer.Id)
	if len(list.Data) != 1 || list.Data[0].Id != other.Id || !list.Data[0].IsPrimary {
		t.Errorf("list = %+v, want the remaining address, promoted to primary", list.Data)
	}
}

func TestDeleteCustomersByIdAddressesByAddressId_LastOfItsType_LeavesNonePrimary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Delete Last Co")
	only := createAddress(t, c, customer.Id, fullAddressBody("postal", nil))

	r := deleteAddress(t, c, customer.Id, only.Id)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204 (deleting the last address of a type is never refused)", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_addresses WHERE customer_id = $1`, customer.Id); n != 0 {
		t.Errorf("remaining addresses = %d, want 0", n)
	}
}

func TestDeleteCustomersByIdAddressesByAddressId_WhenAddressDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Delete Missing Co")

	r := deleteAddress(t, c, customer.Id, 999999)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestDeleteCustomersByIdAddressesByAddressId_AddressBelongsToAnotherCustomer_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customerA := createCustomer(t, c, "Owner Delete Co")
	customerB := createCustomer(t, c, "Other Delete Co")
	address := createAddress(t, c, customerA.Id, fullAddressBody("postal", nil))

	r := deleteAddress(t, c, customerB.Id, address.Id)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestDeleteCustomersByIdAddressesByAddressId_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := deleteAddress(t, c, 999999, 1)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestDeleteCustomersByIdAddressesByAddressId_WithoutUpdatePermission_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:view")

	r := deleteAddress(t, c, 1001, 1)
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

// --- generated timeline events ---------------------------------------------

func TestPostCustomersByIdAddresses_RecordsAddressAddedEventWithActorAndPayload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	customer := createCustomer(t, c, "Event Added Co")

	created := createAddress(t, c, customer.Id, fullAddressBody("invoice", nil))

	if n := countTimelineEvents(t, h, customer.Id, "customer.address_added"); n != 1 {
		t.Fatalf("customer.address_added events = %d, want 1", n)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.address_added")
	if got := int32(event.Payload["addressId"].(float64)); got != created.Id {
		t.Errorf("payload addressId = %d, want %d", got, created.Id)
	}
	if event.Payload["type"] != "invoice" {
		t.Errorf("payload type = %v, want invoice", event.Payload["type"])
	}
	if event.Payload["label"] != "HQ" {
		t.Errorf("payload label = %v, want HQ", event.Payload["label"])
	}
	wantDisplay := "Storgata 1, Suite 2, 0155 Oslo, NO"
	if event.Payload["display"] != wantDisplay {
		t.Errorf("payload display = %v, want %q", event.Payload["display"], wantDisplay)
	}

	gotActor := modtest.One[string](t, h, `
		SELECT actor_user_id::text FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.address_added'`, customer.Id)
	if gotActor != userID.String() {
		t.Errorf("actor_user_id = %s, want %s", gotActor, userID)
	}
}

func TestPutCustomersByIdAddressesByAddressId_RecordsAddressUpdatedEventWithBeforeAfter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Event Updated Co")
	address := createAddress(t, c, customer.Id, map[string]any{"type": "postal", "line1": "Old line", "country": "se"})

	r := putAddress(t, c, customer.Id, address.Id, fullAddressBody("postal", boolPtr(true)))
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}

	if n := countTimelineEvents(t, h, customer.Id, "customer.address_updated"); n != 1 {
		t.Fatalf("customer.address_updated events = %d, want 1", n)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.address_updated")
	before, _ := event.Payload["before"].(map[string]any)
	after, _ := event.Payload["after"].(map[string]any)
	if before == nil || before["line1"] != "Old line" {
		t.Errorf("before.line1 = %v, want \"Old line\"", before["line1"])
	}
	if after == nil || after["line1"] != "Storgata 1" {
		t.Errorf("after.line1 = %v, want \"Storgata 1\"", after["line1"])
	}
}

func TestDeleteCustomersByIdAddressesByAddressId_RecordsAddressRemovedEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Event Removed Co")
	address := createAddress(t, c, customer.Id, fullAddressBody("delivery", nil))

	r := deleteAddress(t, c, customer.Id, address.Id)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}

	if n := countTimelineEvents(t, h, customer.Id, "customer.address_removed"); n != 1 {
		t.Fatalf("customer.address_removed events = %d, want 1", n)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.address_removed")
	if got := int32(event.Payload["addressId"].(float64)); got != address.Id {
		t.Errorf("payload addressId = %d, want %d", got, address.Id)
	}
	if event.Payload["type"] != "delivery" {
		t.Errorf("payload type = %v, want delivery", event.Payload["type"])
	}
}
