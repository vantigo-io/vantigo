package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is PUT /customers/{id}/contact-info (invoice-ready customer
// design D1, D2): a customer's own email, phone and website. Shaped after
// customer_type_test.go/legal_identity_test.go's coverage of the module's
// other revision-guarded sub-resource writes — the happy path, blank/absent
// both clearing to null, one HTTP case per validation rule (values_test.go
// carries the table-driven unit coverage of validateEmail/validatePhone/
// validateWebsite themselves), the revision guard, the no-op rule, the
// generated timeline event with its actor and before/after payload, create
// support, the permission gate, and list search.

// putContactInfo PUTs /customers/{id}/contact-info with body and returns the
// raw response, leaving status assertions to the caller.
func putContactInfo(t *testing.T, c *modtest.Client, id int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contact-info", id), body)
}

func TestPutCustomersByIdContactInfo_SetsAllThreeFields_ShowsInGetAndList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Contact Co")

	r := putContactInfo(t, c, created.Id, map[string]any{
		"email": "hello@contact.co", "phone": "+47 934 89 731", "website": "https://contact.co",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	wantSet := func(got contactInfoJSON) bool {
		return got.Email != nil && *got.Email == "hello@contact.co" &&
			got.Phone != nil && *got.Phone == "+47 934 89 731" &&
			got.Website != nil && *got.Website == "https://contact.co"
	}
	if !wantSet(updated.ContactInfo) {
		t.Errorf("response contactInfo = %+v, want all three set", updated.ContactInfo)
	}

	fetched := fetchCustomerJSON(t, c, created.Id)
	if !wantSet(fetched.ContactInfo) {
		t.Errorf("GET contactInfo = %+v, want all three set", fetched.ContactInfo)
	}

	list := getList(t, c, "search="+url.QueryEscape("Contact Co"))
	if len(list.Data) != 1 || !wantSet(list.Data[0].ContactInfo) {
		t.Errorf("list contactInfo = %+v, want all three set", list.Data)
	}
}

// TestPutCustomersByIdContactInfo_BlankAndAbsentFieldsClearToNull proves the
// controller ruling: blank/whitespace-only, explicit null and an absent key
// all mean the same thing — clear the field — and none of the three is a
// validation error.
func TestPutCustomersByIdContactInfo_BlankAndAbsentFieldsClearToNull(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Clearable Co")

	r := putContactInfo(t, c, created.Id, map[string]any{
		"email": "hello@clearable.co", "phone": "+47 934 89 731", "website": "https://clearable.co",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}

	r = putContactInfo(t, c, created.Id, map[string]any{"email": "   ", "phone": "", "website": nil})
	if r.Status != http.StatusOK {
		t.Fatalf("clear with blank/null: status %d body %s, want 200", r.Status, r.Body)
	}
	var cleared customerJSON
	r.JSON(&cleared)
	if cleared.ContactInfo.Email != nil || cleared.ContactInfo.Phone != nil || cleared.ContactInfo.Website != nil {
		t.Errorf("contactInfo = %+v, want all three cleared to null", cleared.ContactInfo)
	}

	// Re-set, then clear by omitting every field entirely: absent must mean
	// the same as null (a full replace, customers foundation design D1).
	r = putContactInfo(t, c, created.Id, map[string]any{"email": "hello@clearable.co"})
	if r.Status != http.StatusOK {
		t.Fatalf("re-set: status %d body %s, want 200", r.Status, r.Body)
	}
	r = putContactInfo(t, c, created.Id, map[string]any{})
	if r.Status != http.StatusOK {
		t.Fatalf("clear with an empty body: status %d body %s, want 200", r.Status, r.Body)
	}
	var afterAbsent customerJSON
	r.JSON(&afterAbsent)
	if afterAbsent.ContactInfo.Email != nil {
		t.Errorf("email = %v, want nil after an absent-field PUT (absent means clear)", afterAbsent.ContactInfo.Email)
	}
}

func TestPutCustomersByIdContactInfo_InvalidEmail_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Email Co")

	r := putContactInfo(t, c, created.Id, map[string]any{"email": "not-an-email"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An email address must look like name@example.com, but was 'not-an-email'"
	if msgs := problem.Errors["email"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[email] = %v, want [%q]", msgs, want)
	}
}

// TestPutCustomersByIdContactInfo_EmbeddedSpaceInEmail_ReturnsBadRequest is
// review fix round 1's HTTP-level case: an email with a space embedded in
// the local part passed validateEmail's original inline shape check and was
// stored unchanged (values_test.go carries the unit-level proof); this pins
// the whole request/response path answers 400 under "email" now that
// validateEmail shares hasValidEmailShape with validateEmailAddress.
func TestPutCustomersByIdContactInfo_EmbeddedSpaceInEmail_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Spaced Email Co")

	r := putContactInfo(t, c, created.Id, map[string]any{"email": "an ders@vantigo.io"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An email address must look like name@example.com, but was 'an ders@vantigo.io'"
	if msgs := problem.Errors["email"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[email] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdContactInfo_InvalidPhone_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Phone Co")

	r := putContactInfo(t, c, created.Id, map[string]any{"phone": "1234"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A phone number may only contain digits, spaces and + - ( ), and needs at least five digits, but was '1234'"
	if msgs := problem.Errors["phone"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[phone] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdContactInfo_InvalidWebsite_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Website Co")

	r := putContactInfo(t, c, created.Id, map[string]any{"website": "not a url"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A website must be an absolute http or https URL, but was 'not a url'"
	if msgs := problem.Errors["website"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[website] = %v, want [%q]", msgs, want)
	}
}

func TestPutCustomersByIdContactInfo_WithStaleRevision_ReturnsConflictAndLeavesRowUntouched(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Stale Rev Co")
	before := fetchCustomerJSON(t, c, created.Id)
	h.Advance(time.Second)

	r := putContactInfo(t, c, created.Id, map[string]any{"email": "hello@stale.co", "revision": 999})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Customer revision conflict" {
		t.Errorf("Title = %q, want %q", problemTitle(problem.Title), "Customer revision conflict")
	}
	// The exact wording (not just the status code) is what distinguishes the
	// Go-side pre-check (customerRevisionConflict(*body.Revision, ...), the
	// caller's own claimed 999) from the database guard's own fallback
	// (customerRevisionConflict(existing.Revision, ...), the row's revision
	// at read time) — the same pinning customers_concurrency_test.go's
	// PutCustomersById revision test makes.
	wantDetail := "The customer has been changed since revision 999 was read; it is now at revision 1."
	if problemTitle(problem.Detail) != wantDetail {
		t.Errorf("Detail = %q, want %q", problemTitle(problem.Detail), wantDetail)
	}
	if problem.Code != nil {
		t.Errorf("Code = %v, want nil: a revision conflict carries no code", problem.Code)
	}

	after := fetchCustomerJSON(t, c, created.Id)
	if after.ContactInfo.Email != nil {
		t.Errorf("email = %v, want unchanged nil", after.ContactInfo.Email)
	}
	if after.Revision != before.Revision {
		t.Errorf("revision = %d, want unchanged %d", after.Revision, before.Revision)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updatedAt changed from %s to %s on a refused update", before.UpdatedAt, after.UpdatedAt)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.contact_info_updated"); n != 0 {
		t.Errorf("customer.contact_info_updated events = %d, want 0", n)
	}
}

// TestPutCustomersByIdContactInfo_NoOp_DoesNotBumpRevisionOrRecordEvent
// resubmits exactly what is already stored: the no-op rule (customers
// foundation design D5) says this writes nothing at all, so only the first,
// real write's event exists afterward.
func TestPutCustomersByIdContactInfo_NoOp_DoesNotBumpRevisionOrRecordEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "NoOp Co")

	r := putContactInfo(t, c, created.Id, map[string]any{"email": "hello@noop.co", "revision": 1})
	if r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	before := fetchCustomerJSON(t, c, created.Id)
	h.Advance(time.Second)

	r = putContactInfo(t, c, created.Id, map[string]any{"email": "hello@noop.co", "revision": before.Revision})
	if r.Status != http.StatusOK {
		t.Fatalf("resubmit: status %d body %s, want 200", r.Status, r.Body)
	}
	after := fetchCustomerJSON(t, c, created.Id)
	if after.Revision != before.Revision {
		t.Errorf("revision = %d, want unchanged %d", after.Revision, before.Revision)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("updatedAt changed from %s to %s on a no-op PUT", before.UpdatedAt, after.UpdatedAt)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.contact_info_updated"); n != 1 {
		t.Errorf("customer.contact_info_updated events = %d, want 1 (only from the first, real write)", n)
	}
}

// TestPutCustomersByIdContactInfo_ChangesFields_BumpsRevisionAndRecordsEventWithActorAndPayload
// proves the real-write path end to end: exactly one revision bump, one
// customer.contact_info_updated event attributed to the signed-in caller
// (customers foundation design D1), with the changed field's before/after in
// the payload.
func TestPutCustomersByIdContactInfo_ChangesFields_BumpsRevisionAndRecordsEventWithActorAndPayload(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	created := createCustomer(t, c, "Changed Co")

	r := putContactInfo(t, c, created.Id, map[string]any{
		"email": "hello@changed.co", "phone": "+47 934 89 731",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated customerJSON
	r.JSON(&updated)
	if updated.Revision != 2 {
		t.Errorf("revision = %d, want 2", updated.Revision)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 2 {
		t.Errorf("persisted revision = %d, want 2", got.Revision)
	}

	if n := countTimelineEvents(t, h, created.Id, "customer.contact_info_updated"); n != 1 {
		t.Fatalf("customer.contact_info_updated events = %d, want 1", n)
	}
	event := fetchTimelineEvent(t, h, created.Id, "customer.contact_info_updated")
	before, _ := event.Payload["before"].(map[string]any)
	after, _ := event.Payload["after"].(map[string]any)
	if before == nil || before["email"] != nil {
		t.Errorf("before.email = %v, want nil", before["email"])
	}
	if after == nil || after["email"] != "hello@changed.co" {
		t.Errorf("after.email = %v, want hello@changed.co", after["email"])
	}
	if after["phone"] != "+47 934 89 731" {
		t.Errorf("after.phone = %v, want +47 934 89 731", after["phone"])
	}

	gotActor := modtest.One[string](t, h, `
		SELECT actor_user_id::text FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.contact_info_updated'`, created.Id)
	if gotActor != userID.String() {
		t.Errorf("actor_user_id = %s, want %s (the signed-in caller)", gotActor, userID)
	}
}

func TestPutCustomersByIdContactInfo_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := putContactInfo(t, c, 999999, map[string]any{"email": "hello@nowhere.co"})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestPutCustomersByIdContactInfo_WithoutUpdatePermission_ReturnsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "customers:view")

	r := putContactInfo(t, c, 1001, map[string]any{"email": "hello@forbidden.co"})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

func TestPostCustomers_WithContactInfo_CreatesIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Created With Contact Co",
		"contactInfo": map[string]any{
			"email": "hello@created.co", "phone": "+47 934 89 731", "website": "https://created.co",
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)

	got := fetchCustomerJSON(t, c, created.Id)
	if got.ContactInfo.Email == nil || *got.ContactInfo.Email != "hello@created.co" {
		t.Errorf("email = %v, want hello@created.co", got.ContactInfo.Email)
	}
	if got.ContactInfo.Phone == nil || *got.ContactInfo.Phone != "+47 934 89 731" {
		t.Errorf("phone = %v, want +47 934 89 731", got.ContactInfo.Phone)
	}
	if got.ContactInfo.Website == nil || *got.ContactInfo.Website != "https://created.co" {
		t.Errorf("website = %v, want https://created.co", got.ContactInfo.Website)
	}
}

func TestPostCustomers_WithInvalidContactInfoEmail_ReturnsBadRequestUnderContactInfoEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":        "Bad Contact Co",
		"contactInfo": map[string]any{"email": "not-an-email"},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "An email address must look like name@example.com, but was 'not-an-email'"
	if msgs := problem.Errors["contactInfo.email"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[contactInfo.email] = %v, want [%q]", msgs, want)
	}
}

// TestGetCustomers_Search_MatchesEmailAndPhoneWithDifferentSpacing is D2's
// own search addition: the customer's own email and, compacted the same way
// a legal id already is, its own phone — typed with different spacing than
// it is stored with.
func TestGetCustomers_Search_MatchesEmailAndPhoneWithDifferentSpacing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	target := createCustomer(t, c, "Findable Co")
	r := putContactInfo(t, c, target.Id, map[string]any{
		"email": "unique.findable@example.com", "phone": "+47 934 89 731",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("set contact info: status %d body %s, want 200", r.Status, r.Body)
	}
	createCustomer(t, c, "Control Co") // no contact info: never matches either search

	byEmail := getList(t, c, "search="+url.QueryEscape("unique.findable@example.com"))
	if !idsEqual(idsOf(byEmail), target.Id) {
		t.Errorf("search by email: ids = %v, want [%d]", idsOf(byEmail), target.Id)
	}

	bySameSpacing := getList(t, c, "search="+url.QueryEscape("+47 934 89 731"))
	byDifferentSpacing := getList(t, c, "search="+url.QueryEscape("+4793489731"))
	if !idsEqual(idsOf(bySameSpacing), target.Id) {
		t.Errorf("search by phone (same spacing as stored): ids = %v, want [%d]", idsOf(bySameSpacing), target.Id)
	}
	if !idsEqual(idsOf(byDifferentSpacing), target.Id) {
		t.Errorf("search by phone (different spacing than stored): ids = %v, want [%d]", idsOf(byDifferentSpacing), target.Id)
	}
}
