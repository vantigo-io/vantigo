package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is customers foundation design D4's own tests: GetCustomers's
// search reaching into the customer number, the legal identity and linked
// contacts, its status/type filters, and its widened sortBy. Task 6's
// customers_test.go keeps the pre-D4 pagination/sort/name-search coverage
// (as amended for D4 by TestGetCustomers_Search_MatchesLegalNameAndLegalId);
// this file is everything D4 adds on top of it.

// getList performs a GET against /api/v1/customers with the given query
// string (no leading '?') and decodes a 200 response, failing the test on
// anything else.
func getList(t *testing.T, c *modtest.Client, query string) customerListJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/customers?"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /api/v1/customers?%s: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var list customerListJSON
	r.JSON(&list)
	return list
}

// createCustomerOfType posts name and type with no other fields.
func createCustomerOfType(t *testing.T, c *modtest.Client, name, customerType string) createdCustomerJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": name, "type": customerType})
	if r.Status != http.StatusCreated {
		t.Fatalf("create customer %q type %q: status %d body %s", name, customerType, r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)
	return created
}

// insertCustomerAt creates a customer with an explicit customer_number,
// created_at and updated_at — control insertCustomer (harness_test.go) does
// not give. A sortBy test needs it: the customer_number/created_at/updated_at
// values must disagree with the insertion (and so id) order, or a bug that
// quietly sorted by id regardless of sortBy would pass by coincidence.
func insertCustomerAt(t *testing.T, h *modtest.Harness, name string, number int64, createdAt, updatedAt time.Time) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO customers.customers (customer_number, name, status, created_at, updated_at)
		VALUES ($1, $2, 'active', $3, $4)
		RETURNING id`, number, name, createdAt, updatedAt)
}

// idsOf is list.Data's ids, in response order, for a sortBy assertion.
func idsOf(list customerListJSON) []int32 {
	ids := make([]int32, len(list.Data))
	for i, item := range list.Data {
		ids[i] = item.Id
	}
	return ids
}

func idsEqual(got []int32, want ...int32) bool {
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

// Ported from Integration/CustomersEndpointsTests.cs in spirit only —
// customer-number search is new in D4; .NET never had it.
func TestGetCustomers_Search_MatchesCustomerNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	now := h.Now()
	target := insertCustomerAt(t, h, "Numbered Co", 700123456, now, now)
	insertCustomerAt(t, h, "Other Numbered Co", 700999999, now, now) // control: number contains no "0012345"

	byFullNumber := getList(t, c, "search=700123456")
	bySubstring := getList(t, c, "search=0012345")

	if !idsEqual(idsOf(byFullNumber), target) {
		t.Errorf("search by full customer number: ids = %v, want [%d]", idsOf(byFullNumber), target)
	}
	if !idsEqual(idsOf(bySubstring), target) {
		t.Errorf("search by customer number substring: ids = %v, want [%d]", idsOf(bySubstring), target)
	}
}

// TestGetCustomers_Search_MatchesOrgNumberWithAndWithoutSpaces is D4's
// search_compact: a legal id is stored with no spaces (customers foundation
// design D2's normalisation), but a person types it grouped in threes, so
// both forms must find it.
func TestGetCustomers_Search_MatchesOrgNumberWithAndWithoutSpaces(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Spaced Org Co",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Spaced Org AS", "source": "manual",
		},
	})
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Control Org Co"}) // no identity: never matches an org number

	spaced := getList(t, c, "search="+url.QueryEscape("923 609 016"))
	compact := getList(t, c, "search=923609016")

	if !namesEqual(spaced.Data, "Spaced Org Co") {
		t.Errorf("search %q (with spaces): names = %v, want [Spaced Org Co]", "923 609 016", names(spaced.Data))
	}
	if !namesEqual(compact.Data, "Spaced Org Co") {
		t.Errorf("search %q (without spaces): names = %v, want [Spaced Org Co]", "923609016", names(compact.Data))
	}
}

// TestGetCustomers_Search_MatchesContactNameAndEmail exercises every D4
// contact-search field at once: last name, first+last name together, a
// contact's own canonical email and a customer-specific association email
// (which overrides the canonical one for that relationship). Each assertion
// wants exactly one row, so an unrelated control customer/contact that
// happens to be present would be caught the same way a false match would.
func TestGetCustomers_Search_MatchesContactNameAndEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	byLastName := insertCustomer(t, h, "Found By Last Name Co", "active")
	byFullName := insertCustomer(t, h, "Found By Full Name Co", "active")
	byCanonicalEmail := insertCustomer(t, h, "Found By Canonical Email Co", "active")
	byAssocEmail := insertCustomer(t, h, "Found By Association Email Co", "active")
	control := insertCustomer(t, h, "Not Found Co", "active")

	lastNameContact := insertContact(t, h, "Ada", "Lovelace", nil)
	associate(t, h, byLastName, lastNameContact, nil)

	fullNameContact := insertContact(t, h, "Grace", "Hopper", nil)
	associate(t, h, byFullName, fullNameContact, nil)

	canonicalEmailContact := insertContact(t, h, "Rene", "Descartes", ptr("rene@example.com"))
	associate(t, h, byCanonicalEmail, canonicalEmailContact, nil)

	// The association email overrides the contact's own canonical one for
	// this one relationship (contacts.sql), so searching it must match
	// through cc.email, not ct.email.
	assocEmailContact := insertContact(t, h, "Katherine", "Johnson", ptr("katherine@example.com"))
	associate(t, h, byAssocEmail, assocEmailContact, ptr("assoc-only@example.com"))

	controlContact := insertContact(t, h, "No", "Match", ptr("no-match@example.com"))
	associate(t, h, control, controlContact, nil)

	byLastNameResult := getList(t, c, "search="+url.QueryEscape("Lovelace"))
	byFullNameResult := getList(t, c, "search="+url.QueryEscape("Grace Hopper"))
	byCanonicalEmailResult := getList(t, c, "search="+url.QueryEscape("rene@example.com"))
	byAssocEmailResult := getList(t, c, "search="+url.QueryEscape("assoc-only@example.com"))

	if !idsEqual(idsOf(byLastNameResult), byLastName) {
		t.Errorf("search by contact last name: ids = %v, want [%d]", idsOf(byLastNameResult), byLastName)
	}
	if !idsEqual(idsOf(byFullNameResult), byFullName) {
		t.Errorf("search by contact full name: ids = %v, want [%d]", idsOf(byFullNameResult), byFullName)
	}
	if !idsEqual(idsOf(byCanonicalEmailResult), byCanonicalEmail) {
		t.Errorf("search by contact canonical email: ids = %v, want [%d]", idsOf(byCanonicalEmailResult), byCanonicalEmail)
	}
	if !idsEqual(idsOf(byAssocEmailResult), byAssocEmail) {
		t.Errorf("search by association email: ids = %v, want [%d]", idsOf(byAssocEmailResult), byAssocEmail)
	}
}

// TestGetCustomers_Search_WithoutLegalIdentityView_OrgNumberMatchesNothing
// is D4's own rule: search must never be an oracle for data the caller
// cannot see. The setup check with the full-permission caller proves the
// narrower caller's empty result below is the permission gate at work, not
// a query bug or a typo in the search term.
func TestGetCustomers_Search_WithoutLegalIdentityView_OrgNumberMatchesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	owner.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Gated Identity Co",
		"identity": map[string]any{
			"country": "no", "type": "business", "id": "923609016", "name": "Gated Identity AS", "source": "manual",
		},
	})

	setupCheck := getList(t, owner, "search=923609016")
	if !namesEqual(setupCheck.Data, "Gated Identity Co") {
		t.Fatalf("owner search (setup check): names = %v, want [Gated Identity Co]", names(setupCheck.Data))
	}

	viewer := h.SignIn(t, "customers:view") // no legal-identity-view
	gated := getList(t, viewer, "search=923609016")
	if len(gated.Data) != 0 {
		t.Errorf("search without legal-identity-view: data = %+v, want empty", gated.Data)
	}
}

// TestGetCustomers_Search_WithoutContactsView_ContactEmailMatchesNothing is
// the contact-search counterpart: search_contacts needs *both*
// contacts-view and associations-view, so a caller holding only one of the
// two — associations-view here, deliberately, not neither — still gets
// nothing.
func TestGetCustomers_Search_WithoutContactsView_ContactEmailMatchesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	customerID := insertCustomer(t, h, "Gated Contact Co", "active")
	contactID := insertContact(t, h, "Gina", "Gate", ptr("gina.gate@example.com"))
	associate(t, h, customerID, contactID, nil)

	setupCheck := getList(t, owner, "search="+url.QueryEscape("gina.gate@example.com"))
	if !namesEqual(setupCheck.Data, "Gated Contact Co") {
		t.Fatalf("owner search (setup check): names = %v, want [Gated Contact Co]", names(setupCheck.Data))
	}

	viewer := h.SignIn(t, "customers:view", "customers:associations-view") // no contacts-view
	gated := getList(t, viewer, "search="+url.QueryEscape("gina.gate@example.com"))
	if len(gated.Data) != 0 {
		t.Errorf("search without contacts-view: data = %+v, want empty", gated.Data)
	}
}

// TestGetCustomers_Search_TreatsLikeMetacharactersLiterally proves
// likeReplacer's escaping (customers.go) still holds now that search
// reaches more columns: each control row would match its sibling search
// term if % or _ were left as SQL wildcards instead of literal characters.
func TestGetCustomers_Search_TreatsLikeMetacharactersLiterally(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomer(t, c, "100% Match Co")
	createCustomer(t, c, "100ZZZZ Match Co") // control: matches "100% Match Co" as a wildcard pattern if % is not escaped
	createCustomer(t, c, "AB_CD Co")
	createCustomer(t, c, "ABXCD Co") // control: matches "AB_CD Co" as a wildcard pattern if _ is not escaped

	percent := getList(t, c, "search="+url.QueryEscape("100% Match Co"))
	underscore := getList(t, c, "search="+url.QueryEscape("AB_CD Co"))

	if !namesEqual(percent.Data, "100% Match Co") {
		t.Errorf("search containing %%: names = %v, want [100%% Match Co]", names(percent.Data))
	}
	if !namesEqual(underscore.Data, "AB_CD Co") {
		t.Errorf("search containing _: names = %v, want [AB_CD Co]", names(underscore.Data))
	}
}

// TestGetCustomers_FilterByStatus_ArchivedShowsOnlyArchived is D4's status
// filter: naming a status shows exactly that status, with no need for
// includeArchived.
func TestGetCustomers_FilterByStatus_ArchivedShowsOnlyArchived(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	insertCustomer(t, h, "Active Co", "active")
	insertCustomer(t, h, "Archived Co", "archived")

	list := getList(t, c, "status=archived")

	if !namesEqual(list.Data, "Archived Co") {
		t.Errorf("status=archived: names = %v, want [Archived Co]", names(list.Data))
	}
}

func TestGetCustomers_FilterByStatus_Disabled(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	insertCustomer(t, h, "Active Co", "active")
	insertCustomer(t, h, "Disabled Co", "disabled")
	insertCustomer(t, h, "Archived Co", "archived")

	list := getList(t, c, "status=disabled")

	if !namesEqual(list.Data, "Disabled Co") {
		t.Errorf("status=disabled: names = %v, want [Disabled Co]", names(list.Data))
	}
}

func TestGetCustomers_FilterByStatus_Bogus_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers?status=bogus", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	want := "'status' must be one of 'active', 'disabled' or 'archived', but was 'bogus'."
	if problemTitle(problem.Detail) != want {
		t.Errorf("Detail = %q, want %q", problemTitle(problem.Detail), want)
	}
}

func TestGetCustomers_FilterByType_Person(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomerOfType(t, c, "Business Customer", "business")
	createCustomerOfType(t, c, "Person Customer", "person")

	list := getList(t, c, "type=person")

	if !namesEqual(list.Data, "Person Customer") {
		t.Errorf("type=person: names = %v, want [Person Customer]", names(list.Data))
	}
}

func TestGetCustomers_FilterByType_Bogus_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers?type=bogus", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	want := "'type' must be one of 'business' or 'person', but was 'bogus'."
	if problemTitle(problem.Detail) != want {
		t.Errorf("Detail = %q, want %q", problemTitle(problem.Detail), want)
	}
}

// TestGetCustomers_Pagination_TotalCountAgreesWithFilters is the self-review
// checklist's own item: totalCount must reflect the filtered set, not every
// customer, even when the page itself is too small to show all of it.
func TestGetCustomers_Pagination_TotalCountAgreesWithFilters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	for i := 0; i < 3; i++ {
		insertCustomer(t, h, fmt.Sprintf("Archived Filtered %d", i), "archived")
	}
	insertCustomer(t, h, "Active Filtered", "active")

	list := getList(t, c, "status=archived&pageSize=1")

	if list.Pagination.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", list.Pagination.TotalCount)
	}
	if len(list.Data) != 1 {
		t.Errorf("len(Data) = %d, want 1 (pageSize=1)", len(list.Data))
	}
}

// The five sortBy tests below each insert rows whose customer_number (or
// created_at/updated_at) deliberately disagrees with insertion (and so id)
// order, so a bug that quietly sorted by id regardless of sortBy would fail
// loudly rather than pass by coincidence. name/createdAt/updatedAt (unlike
// id and customerNumber, both unique) also each carry a tie: two rows share
// the sort key's value, and only c.id — in the same direction as the
// primary sort — tells them apart.

func TestGetCustomers_SortBy_Id(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	now := h.Now()
	idA := insertCustomerAt(t, h, "Id Sort A", 810001, now, now)
	idB := insertCustomerAt(t, h, "Id Sort B", 810002, now, now)
	idC := insertCustomerAt(t, h, "Id Sort C", 810003, now, now)

	asc := getList(t, c, "sortBy=id&sortDirection=asc")
	desc := getList(t, c, "sortBy=id&sortDirection=desc")

	if !idsEqual(idsOf(asc), idA, idB, idC) {
		t.Errorf("sortBy=id asc: ids = %v, want [%d %d %d]", idsOf(asc), idA, idB, idC)
	}
	if !idsEqual(idsOf(desc), idC, idB, idA) {
		t.Errorf("sortBy=id desc: ids = %v, want [%d %d %d]", idsOf(desc), idC, idB, idA)
	}
}

func TestGetCustomers_SortBy_CustomerNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	now := h.Now()
	idA := insertCustomerAt(t, h, "Number Sort A", 830003, now, now)
	idB := insertCustomerAt(t, h, "Number Sort B", 830001, now, now)
	idC := insertCustomerAt(t, h, "Number Sort C", 830002, now, now)

	asc := getList(t, c, "sortBy=customerNumber&sortDirection=asc")
	desc := getList(t, c, "sortBy=customerNumber&sortDirection=desc")

	if !idsEqual(idsOf(asc), idB, idC, idA) {
		t.Errorf("sortBy=customerNumber asc: ids = %v, want [%d %d %d]", idsOf(asc), idB, idC, idA)
	}
	if !idsEqual(idsOf(desc), idA, idC, idB) {
		t.Errorf("sortBy=customerNumber desc: ids = %v, want [%d %d %d]", idsOf(desc), idA, idC, idB)
	}
}

func TestGetCustomers_SortBy_NameWithIdTiebreak(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	now := h.Now()
	idAlpha := insertCustomerAt(t, h, "Tie Alpha", 840001, now, now)
	idBeta1 := insertCustomerAt(t, h, "Tie Beta", 840002, now, now)
	idBeta2 := insertCustomerAt(t, h, "Tie Beta", 840003, now, now)
	idGamma := insertCustomerAt(t, h, "Tie Gamma", 840004, now, now)

	asc := getList(t, c, "sortBy=name&sortDirection=asc")
	desc := getList(t, c, "sortBy=name&sortDirection=desc")

	if !idsEqual(idsOf(asc), idAlpha, idBeta1, idBeta2, idGamma) {
		t.Errorf("sortBy=name asc: ids = %v, want [%d %d %d %d] (tied names broken by id ascending)",
			idsOf(asc), idAlpha, idBeta1, idBeta2, idGamma)
	}
	if !idsEqual(idsOf(desc), idGamma, idBeta2, idBeta1, idAlpha) {
		t.Errorf("sortBy=name desc: ids = %v, want [%d %d %d %d] (tied names broken by id descending)",
			idsOf(desc), idGamma, idBeta2, idBeta1, idAlpha)
	}
}

func TestGetCustomers_SortBy_CreatedAtWithIdTiebreak(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	now := h.Now()
	idEarly := insertCustomerAt(t, h, "Created Early", 850001, now, now)
	idTied1 := insertCustomerAt(t, h, "Created Tied 1", 850002, now.Add(time.Hour), now)
	idTied2 := insertCustomerAt(t, h, "Created Tied 2", 850003, now.Add(time.Hour), now)
	idLate := insertCustomerAt(t, h, "Created Late", 850004, now.Add(2*time.Hour), now)

	asc := getList(t, c, "sortBy=createdAt&sortDirection=asc")
	desc := getList(t, c, "sortBy=createdAt&sortDirection=desc")

	if !idsEqual(idsOf(asc), idEarly, idTied1, idTied2, idLate) {
		t.Errorf("sortBy=createdAt asc: ids = %v, want [%d %d %d %d] (tied createdAt broken by id ascending)",
			idsOf(asc), idEarly, idTied1, idTied2, idLate)
	}
	if !idsEqual(idsOf(desc), idLate, idTied2, idTied1, idEarly) {
		t.Errorf("sortBy=createdAt desc: ids = %v, want [%d %d %d %d] (tied createdAt broken by id descending)",
			idsOf(desc), idLate, idTied2, idTied1, idEarly)
	}
}

func TestGetCustomers_SortBy_UpdatedAtWithIdTiebreak(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	now := h.Now()
	idEarly := insertCustomerAt(t, h, "Updated Early", 860001, now, now)
	idTied1 := insertCustomerAt(t, h, "Updated Tied 1", 860002, now, now.Add(time.Hour))
	idTied2 := insertCustomerAt(t, h, "Updated Tied 2", 860003, now, now.Add(time.Hour))
	idLate := insertCustomerAt(t, h, "Updated Late", 860004, now, now.Add(2*time.Hour))

	asc := getList(t, c, "sortBy=updatedAt&sortDirection=asc")
	desc := getList(t, c, "sortBy=updatedAt&sortDirection=desc")

	if !idsEqual(idsOf(asc), idEarly, idTied1, idTied2, idLate) {
		t.Errorf("sortBy=updatedAt asc: ids = %v, want [%d %d %d %d] (tied updatedAt broken by id ascending)",
			idsOf(asc), idEarly, idTied1, idTied2, idLate)
	}
	if !idsEqual(idsOf(desc), idLate, idTied2, idTied1, idEarly) {
		t.Errorf("sortBy=updatedAt desc: ids = %v, want [%d %d %d %d] (tied updatedAt broken by id descending)",
			idsOf(desc), idLate, idTied2, idTied1, idEarly)
	}
}
