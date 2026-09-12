package customers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports Integration/ContactsEndpointsTests.cs (customers inventory
// §7): all 24 tests. It also adds two ordering-pin tests that are not
// themselves .NET ports (no test in ContactsEndpointsTests.cs combines an
// invalid body with a missing target) but are required by customers
// inventory §1.4's ordering table: Attach validates connection fields before
// checking existence, Update-association checks existence before
// validating, the opposite order — see
// TestAttachContact_InvalidConnectionAgainstUnknownCustomer_Returns400 and
// TestUpdateCustomerContact_InvalidConnectionAgainstUnknownAssociation_Returns404
// below.

type contactJSON struct {
	Id         int32   `json:"id"`
	FirstName  string  `json:"firstName"`
	LastName   string  `json:"lastName"`
	MiddleName *string `json:"middleName"`
	Prefix     *string `json:"prefix"`
	Suffix     *string `json:"suffix"`
	Phone      *string `json:"phone"`
	Email      *string `json:"email"`
}

type contactCustomerReferenceJSON struct {
	Id             int32  `json:"id"`
	CustomerNumber int64  `json:"customerNumber"`
	Name           string `json:"name"`
}

type contactListItemJSON struct {
	Contact       contactJSON                   `json:"contact"`
	CustomerCount int32                         `json:"customerCount"`
	Customer      *contactCustomerReferenceJSON `json:"customer"`
}

type contactListJSON struct {
	Data []contactListItemJSON `json:"data"`
}

type customerContactJSON struct {
	Contact contactJSON `json:"contact"`
	Role    string      `json:"role"`
	Phone   *string     `json:"phone"`
	Email   *string     `json:"email"`
}

type customerContactListJSON struct {
	Data []customerContactJSON `json:"data"`
}

type contactCustomerJSON struct {
	Customer contactCustomerReferenceJSON `json:"customer"`
	Role     string                       `json:"role"`
	Phone    *string                      `json:"phone"`
	Email    *string                      `json:"email"`
}

type contactCustomerListJSON struct {
	Data []contactCustomerJSON `json:"data"`
}

// str dereferences p, or answers "" for a nil p — this test file's own copy,
// since the package's unexported deref belongs to package customers, not
// customers_test.
func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// createContact posts body to the contacts collection and returns the
// created contact, failing the test on anything but 201.
func createContact(t *testing.T, c *modtest.Client, body map[string]any) contactJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/customers/contacts", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create contact: status %d body %s, want 201", r.Status, r.Body)
	}
	var contact contactJSON
	r.JSON(&contact)
	return contact
}

// attachContact attaches contactID to customerID with role, failing the test
// on anything but 200.
func attachContact(t *testing.T, c *modtest.Client, customerID, contactID int32, role string) {
	t.Helper()
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customerID), map[string]any{
		"contactId": contactID, "role": role,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("attach contact %d to customer %d: status %d body %s, want 200", contactID, customerID, r.Status, r.Body)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// CreateContact_WithAllFields_ReturnsCreatedContactAndLocation.
func TestCreateContact_WithAllFields_ReturnsCreatedContactAndLocation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers/contacts", map[string]any{
		"firstName": "Anders", "lastName": "Refsdal", "middleName": "Bernhard",
		"prefix": "Dr.", "suffix": "PhD", "phone": "+47 934 89 731", "email": "Anders@Refsdal.NO",
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var contact contactJSON
	r.JSON(&contact)
	if contact.Id <= 0 {
		t.Errorf("Id = %d, want > 0", contact.Id)
	}
	if contact.FirstName != "Anders" || contact.LastName != "Refsdal" {
		t.Errorf("FirstName/LastName = %q/%q, want Anders/Refsdal", contact.FirstName, contact.LastName)
	}
	if str(contact.MiddleName) != "Bernhard" || str(contact.Prefix) != "Dr." || str(contact.Suffix) != "PhD" {
		t.Errorf("MiddleName/Prefix/Suffix = %q/%q/%q, want Bernhard/Dr./PhD", str(contact.MiddleName), str(contact.Prefix), str(contact.Suffix))
	}
	if str(contact.Phone) != "+47 934 89 731" {
		t.Errorf("Phone = %q, want \"+47 934 89 731\"", str(contact.Phone))
	}
	if str(contact.Email) != "anders@refsdal.no" {
		t.Errorf("Email = %q, want anders@refsdal.no", str(contact.Email))
	}
	want := fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id)
	if got := r.Header("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// CreateContact_WithNamesOnly_LeavesOptionalFieldsNull.
func TestCreateContact_WithNamesOnly_LeavesOptionalFieldsNull(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	contact := createContact(t, c, map[string]any{"firstName": "Kari", "lastName": "Nordmann"})

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id), nil)
	var fetched contactJSON
	r.JSON(&fetched)
	if fetched.FirstName != "Kari" {
		t.Errorf("FirstName = %q, want Kari", fetched.FirstName)
	}
	if fetched.MiddleName != nil || fetched.Phone != nil || fetched.Email != nil {
		t.Errorf("MiddleName/Phone/Email = %v/%v/%v, want all nil", fetched.MiddleName, fetched.Phone, fetched.Email)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// CreateContact_WithMultipleInvalidFields_ReportsAllErrorsAtOnce.
func TestCreateContact_WithMultipleInvalidFields_ReportsAllErrorsAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers/contacts", map[string]any{
		"firstName": "", "lastName": "  ", "phone": "not a number", "email": "not-an-email",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := []string{"email", "firstName", "lastName", "phone"}
	got := make([]string, 0, len(problem.Errors))
	for k := range problem.Errors {
		got = append(got, k)
	}
	if !sameSet(got, want) {
		t.Errorf("error keys = %v, want %v", got, want)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// UpdateContact_ReplacesAllFieldsAndClearsBlankOptionals.
func TestUpdateContact_ReplacesAllFieldsAndClearsBlankOptionals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Ola", "lastName": "Nordmann", "phone": "+47 22 86 44 00"})

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id), map[string]any{
		"firstName": "Ola", "lastName": "Nordmann-Hansen", "email": "ola@nordmann.no",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated contactJSON
	r.JSON(&updated)
	if updated.LastName != "Nordmann-Hansen" {
		t.Errorf("LastName = %q, want Nordmann-Hansen", updated.LastName)
	}
	if str(updated.Email) != "ola@nordmann.no" {
		t.Errorf("Email = %q, want ola@nordmann.no", str(updated.Email))
	}
	if updated.Phone != nil {
		t.Errorf("Phone = %v, want nil (cleared by omission)", updated.Phone)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// UpdateContact_WhenContactDoesNotExist_ReturnsNotFound.
func TestUpdateContact_WhenContactDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPut, "/api/v1/customers/contacts/999999", map[string]any{"firstName": "Ghost", "lastName": "Contact"})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// DeleteContact_RemovesContactAndItsAssociations.
func TestDeleteContact_RemovesContactAndItsAssociations(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Delete", "lastName": "Me"})
	customer := createCustomer(t, c, "Delete Contact Co")
	attachContact(t, c, customer.Id, contact.Id, "CEO")

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}
	if got := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id), nil); got.Status != http.StatusNotFound {
		t.Errorf("get after delete: status %d, want 404", got.Status)
	}

	list := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), nil)
	var customerContacts customerContactListJSON
	list.JSON(&customerContacts)
	if len(customerContacts.Data) != 0 {
		t.Errorf("customer contacts = %v, want empty", customerContacts.Data)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// DeleteContact_WhenContactDoesNotExist_ReturnsNotFound.
func TestDeleteContact_WhenContactDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodDelete, "/api/v1/customers/contacts/999999", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetContacts_Search_MatchesNamePartsPhoneAndEmail.
func TestGetContacts_Search_MatchesNamePartsPhoneAndEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createContact(t, c, map[string]any{
		"firstName": "Searchable", "lastName": "Contactsen", "middleName": "Findme",
		"phone": "+47 99 88 77 66", "email": "searchable@contactsen.no",
	})

	search := func(q string) contactListJSON {
		r := c.Do(http.MethodGet, "/api/v1/customers/contacts?search="+q, nil)
		var list contactListJSON
		r.JSON(&list)
		return list
	}

	byFirstName := search("searchable")
	byMiddleName := search("findme")
	byPhone := search("99%2088%2077")
	byEmail := search("searchable%40contactsen")
	noMatch := search("no-such-contact")

	assertSingleByFirstName := func(list contactListJSON, label string) {
		t.Helper()
		count := 0
		for _, item := range list.Data {
			if item.Contact.FirstName == "Searchable" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("%s: matches = %d, want exactly 1", label, count)
		}
	}
	assertSingleByFirstName(byFirstName, "byFirstName")
	assertSingleByFirstName(byMiddleName, "byMiddleName")
	assertSingleByFirstName(byPhone, "byPhone")
	assertSingleByFirstName(byEmail, "byEmail")
	if len(noMatch.Data) != 0 {
		t.Errorf("noMatch.Data = %v, want empty", noMatch.Data)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetContacts_Search_MatchesFullNamesAcrossNameParts.
func TestGetContacts_Search_MatchesFullNamesAcrossNameParts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createContact(t, c, map[string]any{"firstName": "Fullname", "lastName": "Matchsen", "middleName": "Bernhard"})

	search := func(q string) contactListJSON {
		r := c.Do(http.MethodGet, "/api/v1/customers/contacts?search="+q, nil)
		var list contactListJSON
		r.JSON(&list)
		return list
	}
	matchesFullname := func(list contactListJSON) int {
		n := 0
		for _, item := range list.Data {
			if item.Contact.FirstName == "Fullname" {
				n++
			}
		}
		return n
	}

	if n := matchesFullname(search("fullname%20matchsen")); n != 1 {
		t.Errorf("byFullName matches = %d, want 1", n)
	}
	if n := matchesFullname(search("matchsen%20fullname")); n != 1 {
		t.Errorf("byReversedOrder matches = %d, want 1", n)
	}
	if n := matchesFullname(search("fullname%20bernhard")); n != 1 {
		t.Errorf("byFirstAndMiddle matches = %d, want 1", n)
	}
	if n := matchesFullname(search("full%20match")); n != 1 {
		t.Errorf("byPartialTerms matches = %d, want 1", n)
	}
	if list := search("fullname%20nomatch"); len(list.Data) != 0 {
		t.Errorf("withWrongTerm.Data = %v, want empty", list.Data)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetContacts_DefaultSort_OrdersByFirstNameThenLastName.
func TestGetContacts_DefaultSort_OrdersByFirstNameThenLastName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createContact(t, c, map[string]any{"firstName": "Sorttest Bravo", "lastName": "Alpha"})
	createContact(t, c, map[string]any{"firstName": "Sorttest Alpha", "lastName": "Bravo"})
	createContact(t, c, map[string]any{"firstName": "Sorttest Alpha", "lastName": "Alpha"})

	r := c.Do(http.MethodGet, "/api/v1/customers/contacts?search=Sorttest", nil)
	var list contactListJSON
	r.JSON(&list)

	type pair struct{ first, last string }
	want := []pair{{"Sorttest Alpha", "Alpha"}, {"Sorttest Alpha", "Bravo"}, {"Sorttest Bravo", "Alpha"}}
	if len(list.Data) != len(want) {
		t.Fatalf("Data = %v, want %d items", list.Data, len(want))
	}
	for i, item := range list.Data {
		if item.Contact.FirstName != want[i].first || item.Contact.LastName != want[i].last {
			t.Errorf("item %d = %q/%q, want %q/%q", i, item.Contact.FirstName, item.Contact.LastName, want[i].first, want[i].last)
		}
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetContacts_ReturnsCustomerCountAndSingleCustomerName.
func TestGetContacts_ReturnsCustomerCountAndSingleCustomerName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contactAlone := createContact(t, c, map[string]any{"firstName": "Countless", "lastName": "Solo"})
	contactSingle := createContact(t, c, map[string]any{"firstName": "Countless", "lastName": "Single"})
	contactDouble := createContact(t, c, map[string]any{"firstName": "Countless", "lastName": "Double"})

	firstCustomer := createCustomer(t, c, "Countless First AS")
	secondCustomer := createCustomer(t, c, "Countless Second AS")

	attachContact(t, c, firstCustomer.Id, contactSingle.Id, "CEO")
	attachContact(t, c, firstCustomer.Id, contactDouble.Id, "CTO")
	attachContact(t, c, secondCustomer.Id, contactDouble.Id, "Custodian")

	r := c.Do(http.MethodGet, "/api/v1/customers/contacts?search=Countless", nil)
	var list contactListJSON
	r.JSON(&list)

	find := func(id int32) contactListItemJSON {
		t.Helper()
		for _, item := range list.Data {
			if item.Contact.Id == id {
				return item
			}
		}
		t.Fatalf("no item for contact %d in %v", id, list.Data)
		return contactListItemJSON{}
	}

	alone := find(contactAlone.Id)
	if alone.CustomerCount != 0 || alone.Customer != nil {
		t.Errorf("alone: CustomerCount/Customer = %d/%v, want 0/nil", alone.CustomerCount, alone.Customer)
	}

	single := find(contactSingle.Id)
	if single.CustomerCount != 1 || single.Customer == nil || single.Customer.Name != "Countless First AS" || single.Customer.Id != firstCustomer.Id {
		t.Errorf("single: CustomerCount/Customer = %d/%+v, want 1/{%d Countless First AS}", single.CustomerCount, single.Customer, firstCustomer.Id)
	}

	double := find(contactDouble.Id)
	if double.CustomerCount != 2 || double.Customer != nil {
		t.Errorf("double: CustomerCount/Customer = %d/%v, want 2/nil", double.CustomerCount, double.Customer)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// AttachContact_ReturnsAssociationWithContact.
func TestAttachContact_ReturnsAssociationWithContact(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Attach", "lastName": "Mensen", "email": "attach@mensen.no"})
	customer := createCustomer(t, c, "Attach Co")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
		"contactId": contact.Id, "role": "CEO", "email": "attach@attachco.no",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var association customerContactJSON
	r.JSON(&association)
	if association.Contact.Id != contact.Id {
		t.Errorf("Contact.Id = %d, want %d", association.Contact.Id, contact.Id)
	}
	if association.Role != "CEO" {
		t.Errorf("Role = %q, want CEO", association.Role)
	}
	if str(association.Email) != "attach@attachco.no" {
		t.Errorf("Email = %q, want attach@attachco.no", str(association.Email))
	}
	if association.Phone != nil {
		t.Errorf("Phone = %v, want nil", association.Phone)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// AttachContact_Twice_ReturnsConflict.
func TestAttachContact_Twice_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Conflict", "lastName": "Hansen"})
	customer := createCustomer(t, c, "Conflict Co")
	attachContact(t, c, customer.Id, contact.Id, "CEO")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
		"contactId": contact.Id, "role": "CTO",
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	// The 409's body is client-visible and byte-exact from
	// AttachCustomerContactEndpoint: a status-only assertion let a rewrite of
	// either string through unnoticed.
	var problem problemDetailsJSON
	r.JSON(&problem)
	if got := problemTitle(problem.Title); got != "Contact already associated" {
		t.Errorf("Title = %q, want %q", got, "Contact already associated")
	}
	wantDetail := fmt.Sprintf("Contact %d is already associated with customer %d.", contact.Id, customer.Id)
	if got := problemTitle(problem.Detail); got != wantDetail {
		t.Errorf("Detail = %q, want %q", got, wantDetail)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// AttachContact_WithUnknownCustomerOrContact_ReturnsNotFound.
func TestAttachContact_WithUnknownCustomerOrContact_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Known", "lastName": "Contactsen"})
	knownCustomer := createCustomer(t, c, "Known Co")

	cases := []struct {
		name                  string
		customerID, contactID int32
	}{
		{"unknown customer", 999999, contact.Id},
		{"unknown contact", knownCustomer.Id, 999999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", tc.customerID), map[string]any{
				"contactId": tc.contactID, "role": "CEO",
			})
			if r.Status != http.StatusNotFound {
				t.Errorf("status %d body %s, want 404", r.Status, r.Body)
			}
		})
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// AttachContact_WithInvalidConnection_ReportsFieldErrors.
func TestAttachContact_WithInvalidConnection_ReportsFieldErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Invalid", "lastName": "Connection"})
	customer := createCustomer(t, c, "Invalid Connection Co")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
		"contactId": contact.Id, "role": "", "email": "not-an-email",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := []string{"email", "role"}
	got := make([]string, 0, len(problem.Errors))
	for k := range problem.Errors {
		got = append(got, k)
	}
	if !sameSet(got, want) {
		t.Errorf("error keys = %v, want %v", got, want)
	}
}

// TestPutContact_InvalidBodyAgainstMissingContact_Returns400 pins
// UpdateContactEndpoint.cs:15-38's order, which contacts.go states as
// inventory §1.4's "CreateContact / UpdateContact: validate first, then (for
// update) look up": a body that is both invalid and aimed at a missing
// contact id answers 400, never 404. Swapping the two steps fails only this
// test — every other contact test sends either a valid body or a real id.
func TestPutContact_InvalidBodyAgainstMissingContact_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPut, "/api/v1/customers/contacts/999999", map[string]any{
		"firstName": "", "lastName": "",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (validation must run before the lookup)", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["firstName"]; !ok {
		t.Errorf("errors = %v, want a key \"firstName\"", problem.Errors)
	}
}

// TestAttachContact_InvalidConnectionAgainstUnknownCustomer_Returns400 is not
// itself a .NET port — no test in ContactsEndpointsTests.cs combines an
// invalid body with a missing target — but pins the ordering customers
// inventory §1.4 specifies for AttachCustomerContactEndpoint: connection
// field validation (400) runs before the customer/contact existence check
// (404). If the order were swapped, this request would answer 404 instead.
func TestAttachContact_InvalidConnectionAgainstUnknownCustomer_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers/999999/contacts", map[string]any{
		"contactId": 999998, "role": "", // also nonexistent, but validation must win
	})
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400 (validation must run before the existence check)", r.Status, r.Body)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetCustomerContacts_ReturnsAssociationsSortedByContactName.
func TestGetCustomerContacts_ReturnsAssociationsSortedByContactName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Listing Co")
	second := createContact(t, c, map[string]any{"firstName": "Bravo", "lastName": "Listing"})
	first := createContact(t, c, map[string]any{"firstName": "Alpha", "lastName": "Listing", "phone": "+47 11 22 33 44"})

	attachContact(t, c, customer.Id, second.Id, "CTO")
	attachContact(t, c, customer.Id, first.Id, "CEO")

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), nil)
	var list customerContactListJSON
	r.JSON(&list)
	if len(list.Data) != 2 {
		t.Fatalf("Data = %v, want 2 items", list.Data)
	}
	if list.Data[0].Contact.FirstName != "Alpha" || list.Data[1].Contact.FirstName != "Bravo" {
		t.Errorf("order = %q, %q, want Alpha, Bravo", list.Data[0].Contact.FirstName, list.Data[1].Contact.FirstName)
	}
	if list.Data[0].Role != "CEO" {
		t.Errorf("Data[0].Role = %q, want CEO", list.Data[0].Role)
	}
	if str(list.Data[0].Contact.Phone) != "+47 11 22 33 44" {
		t.Errorf("Data[0].Contact.Phone = %q, want \"+47 11 22 33 44\"", str(list.Data[0].Contact.Phone))
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetCustomerContacts_WhenCustomerDoesNotExist_ReturnsNotFound.
func TestGetCustomerContacts_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers/999999/contacts", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// UpdateCustomerContact_ReplacesConnectionFields.
func TestUpdateCustomerContact_ReplacesConnectionFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Update", "lastName": "Connectionsen"})
	customer := createCustomer(t, c, "Update Connection Co")
	attachContact(t, c, customer.Id, contact.Id, "CEO")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contact.Id), map[string]any{
		"role": "Chairman", "phone": "+47 55 66 77 88",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerContactJSON
	r.JSON(&updated)
	if updated.Role != "Chairman" {
		t.Errorf("Role = %q, want Chairman", updated.Role)
	}
	if str(updated.Phone) != "+47 55 66 77 88" {
		t.Errorf("Phone = %q, want \"+47 55 66 77 88\"", str(updated.Phone))
	}
	if updated.Email != nil {
		t.Errorf("Email = %v, want nil", updated.Email)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// UpdateCustomerContact_WhenAssociationDoesNotExist_ReturnsNotFound.
func TestUpdateCustomerContact_WhenAssociationDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "No Association Co")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/999999", customer.Id), map[string]any{"role": "CEO"})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestUpdateCustomerContact_InvalidConnectionAgainstUnknownAssociation_Returns404
// is not itself a .NET port — no test in ContactsEndpointsTests.cs combines
// an invalid body with a missing association — but pins the ordering
// customers inventory §1.4 specifies for UpdateCustomerContactEndpoint: the
// association lookup (404) runs before field validation (400), the opposite
// order from Attach above. If the order were swapped, this request would
// answer 400 instead.
func TestUpdateCustomerContact_InvalidConnectionAgainstUnknownAssociation_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Ordering Co")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/999999", customer.Id), map[string]any{
		"role": "", // also invalid, but existence must win
	})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404 (the existence check must run before validation)", r.Status, r.Body)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// DetachContact_RemovesAssociationButKeepsContact.
func TestDetachContact_RemovesAssociationButKeepsContact(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Detach", "lastName": "Mensen"})
	customer := createCustomer(t, c, "Detach Co")
	attachContact(t, c, customer.Id, contact.Id, "CEO")

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contact.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}

	list := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), nil)
	var customerContacts customerContactListJSON
	list.JSON(&customerContacts)
	if len(customerContacts.Data) != 0 {
		t.Errorf("customer contacts = %v, want empty", customerContacts.Data)
	}

	stillThere := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id), nil)
	if stillThere.Status != http.StatusOK {
		t.Errorf("get contact after detach: status %d, want 200 (the contact itself is kept)", stillThere.Status)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// DetachContact_WhenAssociationDoesNotExist_ReturnsNotFound.
func TestDetachContact_WhenAssociationDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Nothing To Detach Co")

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/contacts/999999", customer.Id), nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetContactCustomers_ReturnsAssociationsSortedByCustomerName.
func TestGetContactCustomers_ReturnsAssociationsSortedByCustomerName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Manysided", "lastName": "Kontaktsen"})
	bravoCustomer := createCustomer(t, c, "Manysided Bravo AS")
	alphaCustomer := createCustomer(t, c, "Manysided Alpha AS")

	attachContact(t, c, bravoCustomer.Id, contact.Id, "CTO")
	attachResponse := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", alphaCustomer.Id), map[string]any{
		"contactId": contact.Id, "role": "CEO", "phone": "+47 99 00 11 22",
	})
	if attachResponse.Status != http.StatusOK {
		t.Fatalf("attach: status %d body %s, want 200", attachResponse.Status, attachResponse.Body)
	}

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/contacts/%d/customers", contact.Id), nil)
	var list contactCustomerListJSON
	r.JSON(&list)
	if len(list.Data) != 2 {
		t.Fatalf("Data = %v, want 2 items", list.Data)
	}
	if list.Data[0].Customer.Name != "Manysided Alpha AS" || list.Data[1].Customer.Name != "Manysided Bravo AS" {
		t.Errorf("order = %q, %q, want Manysided Alpha AS, Manysided Bravo AS", list.Data[0].Customer.Name, list.Data[1].Customer.Name)
	}
	if list.Data[0].Role != "CEO" {
		t.Errorf("Data[0].Role = %q, want CEO", list.Data[0].Role)
	}
	if str(list.Data[0].Phone) != "+47 99 00 11 22" {
		t.Errorf("Data[0].Phone = %q, want \"+47 99 00 11 22\"", str(list.Data[0].Phone))
	}
	if list.Data[0].Email != nil {
		t.Errorf("Data[0].Email = %v, want nil", list.Data[0].Email)
	}
	if list.Data[0].Customer.Id != alphaCustomer.Id {
		t.Errorf("Data[0].Customer.Id = %d, want %d", list.Data[0].Customer.Id, alphaCustomer.Id)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetContactCustomers_WithoutAssociations_ReturnsEmptyList.
func TestGetContactCustomers_WithoutAssociations_ReturnsEmptyList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Lonely", "lastName": "Kontaktsen"})

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/contacts/%d/customers", contact.Id), nil)
	var list contactCustomerListJSON
	r.JSON(&list)
	if len(list.Data) != 0 {
		t.Errorf("Data = %v, want empty", list.Data)
	}
}

// Ported from Integration/ContactsEndpointsTests.cs.
// GetContactCustomers_WhenContactDoesNotExist_ReturnsNotFound.
func TestGetContactCustomers_WhenContactDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers/contacts/999999/customers", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// This section is not a port: it pins the four contact-association
// generated-timeline-event payloads (contacts_timeline.go, ported from
// SV/CustomerTimelineRecorder.cs:87-124) and the relationship-updated no-op
// suppression (UpdateCustomerContactEndpoint.cs:43-49, customers inventory
// §2.4), added in the Task 7 fix round because no test anywhere read
// payload_json back for these four events, and none forced change
// detection false. Task 9 ports only customer-scoped generated-event
// assertions, so nothing downstream would ever have covered these either.

// timelineEventRow is one customers_timeline_entries row's summary,
// payload_version and decoded payload_json, read back directly since none
// of this task's contract surface exposes them (the timeline read endpoints
// are Task 9's).
type timelineEventRow struct {
	Summary        string
	PayloadVersion int32
	Payload        map[string]any
}

// fetchTimelineEvent reads the most recent timeline entry of eventType
// recorded against customerID, failing the test if none exists.
func fetchTimelineEvent(t *testing.T, h *modtest.Harness, customerID int32, eventType string) timelineEventRow {
	t.Helper()
	var row timelineEventRow
	var payloadJSON []byte
	err := h.Pool().QueryRow(t.Context(), `
		SELECT summary, payload_version, payload_json
		FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = $2
		ORDER BY id DESC LIMIT 1`, customerID, eventType).Scan(&row.Summary, &row.PayloadVersion, &payloadJSON)
	if err != nil {
		t.Fatalf("fetchTimelineEvent(%d, %q): %v", customerID, eventType, err)
	}
	if err := json.Unmarshal(payloadJSON, &row.Payload); err != nil {
		t.Fatalf("fetchTimelineEvent(%d, %q): decode payload: %v", customerID, eventType, err)
	}
	return row
}

// countTimelineEvents counts the timeline entries of eventType recorded
// against customerID.
func countTimelineEvents(t *testing.T, h *modtest.Harness, customerID int32, eventType string) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = $2`, customerID, eventType)
}

// TestAttachContact_RecordsTimelineEvent pins customer.contact_attached's
// summary, payload_version and full payload. It also pins the trap a Task 7
// review found while verifying this: .NET's RecordContactAttached passes
// "Contact attached" as AddContact's summary parameter, but AddContact's
// own eventType switch overrides it to "Contact linked" for this exact
// event type regardless — the summary this test asserts is what a caller
// actually sees, not what the recorder call site's argument might suggest.
func TestAttachContact_RecordsTimelineEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Timeline", "lastName": "Attachsen", "middleName": "Middleman"})
	customer := createCustomer(t, c, "Timeline Attach Co")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
		"contactId": contact.Id, "role": "CEO", "phone": "+47 11 22 33 44",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("attach: status %d body %s, want 200", r.Status, r.Body)
	}

	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_attached")
	wantSummary := fmt.Sprintf("Contact linked: Timeline Middleman Attachsen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", event.Summary, wantSummary)
	}
	if event.PayloadVersion != 1 {
		t.Errorf("PayloadVersion = %d, want 1", event.PayloadVersion)
	}
	wantPayload := map[string]any{
		"customerId":  float64(customer.Id),
		"contactId":   float64(contact.Id),
		"displayName": "Timeline Middleman Attachsen",
		"firstName":   "Timeline",
		"middleName":  "Middleman",
		"lastName":    "Attachsen",
		"role":        "CEO",
		"phone":       "+47 11 22 33 44",
		"email":       nil,
	}
	if !reflect.DeepEqual(event.Payload, wantPayload) {
		t.Errorf("Payload = %+v, want %+v", event.Payload, wantPayload)
	}
}

// TestUpdateCustomerContact_RecordsRelationshipUpdatedEvent pins
// customer.contact_relationship_updated's summary, payload_version and
// payload for an update that actually changes role/phone/email.
func TestUpdateCustomerContact_RecordsRelationshipUpdatedEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Timeline", "lastName": "Updatesen"})
	customer := createCustomer(t, c, "Timeline Update Co")
	attachContact(t, c, customer.Id, contact.Id, "CEO")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contact.Id), map[string]any{
		"role": "Chairman", "email": "chair@timeline.co",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", r.Status, r.Body)
	}

	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary := fmt.Sprintf("Contact relationship updated: Timeline Updatesen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", event.Summary, wantSummary)
	}
	if event.PayloadVersion != 1 {
		t.Errorf("PayloadVersion = %d, want 1", event.PayloadVersion)
	}
	wantPayload := map[string]any{
		"customerId":  float64(customer.Id),
		"contactId":   float64(contact.Id),
		"displayName": "Timeline Updatesen",
		"firstName":   "Timeline",
		"middleName":  nil,
		"lastName":    "Updatesen",
		"role":        "Chairman",
		"phone":       nil,
		"email":       "chair@timeline.co",
	}
	if !reflect.DeepEqual(event.Payload, wantPayload) {
		t.Errorf("Payload = %+v, want %+v", event.Payload, wantPayload)
	}
}

// TestUpdateCustomerContact_NoChange_RecordsNoEvent pins the no-op
// suppression UpdateCustomerContactEndpoint.cs:43-49 requires (customers
// inventory §2.4): resubmitting the same role/phone/email records nothing,
// unlike TestUpdateCustomerContact_RecordsRelationshipUpdatedEvent above.
func TestUpdateCustomerContact_NoChange_RecordsNoEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Timeline", "lastName": "Nochangesen"})
	customer := createCustomer(t, c, "Timeline NoChange Co")
	attachContact(t, c, customer.Id, contact.Id, "CEO")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contact.Id), map[string]any{
		"role": "CEO", // identical to the attach above: no phone, no email either time
	})
	if r.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", r.Status, r.Body)
	}

	if n := countTimelineEvents(t, h, customer.Id, "customer.contact_relationship_updated"); n != 0 {
		t.Errorf("relationship_updated events = %d, want 0 (resubmitting the same values must not record one)", n)
	}
}

// TestDetachContact_RecordsTimelineEvent pins customer.contact_detached's
// summary, payload_version and payload.
func TestDetachContact_RecordsTimelineEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Timeline", "lastName": "Detachsen"})
	customer := createCustomer(t, c, "Timeline Detach Co")
	attachContact(t, c, customer.Id, contact.Id, "CTO")

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contact.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("detach: status %d body %s, want 204", r.Status, r.Body)
	}

	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_detached")
	wantSummary := fmt.Sprintf("Contact unlinked: Timeline Detachsen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", event.Summary, wantSummary)
	}
	if event.PayloadVersion != 1 {
		t.Errorf("PayloadVersion = %d, want 1", event.PayloadVersion)
	}
	wantPayload := map[string]any{
		"customerId": float64(customer.Id), "contactId": float64(contact.Id),
		"displayName": "Timeline Detachsen", "firstName": "Timeline", "middleName": nil, "lastName": "Detachsen",
		"role": "CTO", "phone": nil, "email": nil,
	}
	if !reflect.DeepEqual(event.Payload, wantPayload) {
		t.Errorf("Payload = %+v, want %+v", event.Payload, wantPayload)
	}
}

// TestDeleteContact_RecordsRemovedTimelineEvent pins
// customer.contact_removed's summary, payload_version and payload, recorded
// by DeleteContact's cascade over each surviving association.
func TestDeleteContact_RecordsRemovedTimelineEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Timeline", "lastName": "Removesen"})
	customer := createCustomer(t, c, "Timeline Remove Co")
	attachContact(t, c, customer.Id, contact.Id, "Custodian")

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("delete contact: status %d body %s, want 204", r.Status, r.Body)
	}

	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_removed")
	wantSummary := fmt.Sprintf("Contact removed: Timeline Removesen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", event.Summary, wantSummary)
	}
	if event.PayloadVersion != 1 {
		t.Errorf("PayloadVersion = %d, want 1", event.PayloadVersion)
	}
	wantPayload := map[string]any{
		"customerId": float64(customer.Id), "contactId": float64(contact.Id),
		"displayName": "Timeline Removesen", "firstName": "Timeline", "middleName": nil, "lastName": "Removesen",
		"role": "Custodian", "phone": nil, "email": nil,
	}
	if !reflect.DeepEqual(event.Payload, wantPayload) {
		t.Errorf("Payload = %+v, want %+v", event.Payload, wantPayload)
	}
}
