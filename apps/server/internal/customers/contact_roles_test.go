package customers_test

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is typed contact roles' own tests (typed contact roles design
// D1-D4): the title-or-role rule, the vocabulary, and the one-primary-per-role
// invariant that is addresses_test.go's invariant one table over. The
// concurrency half — a forced race of two primary:true writers — is
// contact_roles_concurrency_test.go, beside the other lock-gate files.
//
// The wire shapes are declared here rather than reusing contacts_test.go's
// customerContactJSON, because that type is deliberately the SHAPE THE CORPUS
// RECORDED (contact, role, phone, email) and one test below asserts that a
// corpus-shaped request still answers exactly it. Widening it would erase the
// distinction this delivery is built on.

type contactRoleJSON struct {
	Role    string `json:"role"`
	Primary bool   `json:"primary"`
}

type roledContactJSON struct {
	Contact contactJSON       `json:"contact"`
	Role    string            `json:"role"`
	Title   *string           `json:"title"`
	Roles   []contactRoleJSON `json:"roles"`
	Phone   *string           `json:"phone"`
	Email   *string           `json:"email"`
}

type roledContactListJSON struct {
	Data []roledContactJSON `json:"data"`
}

type roledCustomerJSON struct {
	Customer contactCustomerReferenceJSON `json:"customer"`
	Role     string                       `json:"role"`
	Title    *string                      `json:"title"`
	Roles    []contactRoleJSON            `json:"roles"`
}

type roledCustomerListJSON struct {
	Data []roledCustomerJSON `json:"data"`
}

// attachWithRoles posts an attach body and returns the answered association,
// failing the test on anything but 200.
func attachWithRoles(t *testing.T, c *modtest.Client, customerID int32, body map[string]any) roledContactJSON {
	t.Helper()
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customerID), body)
	if r.Status != http.StatusOK {
		t.Fatalf("attach %v: status %d body %s, want 200", body, r.Status, r.Body)
	}
	var out roledContactJSON
	r.JSON(&out)
	return out
}

// putAssociation is the update, answering the response whatever the status, so
// a test can assert a refusal's body as easily as a success's.
func putAssociation(t *testing.T, c *modtest.Client, customerID, contactID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customerID, contactID), body)
}

// listCustomerContacts is GET /customers/{id}/contacts, in the role-aware
// shape.
func listCustomerContacts(t *testing.T, c *modtest.Client, customerID int32) roledContactListJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", customerID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list contacts: status %d body %s, want 200", r.Status, r.Body)
	}
	var out roledContactListJSON
	r.JSON(&out)
	return out
}

// rolesOf is one association's roles as the database holds them, for the
// assertions the contract cannot make (which row is primary, and since when).
func rolesOf(t *testing.T, h *modtest.Harness, customerID, contactID int32) []contactRoleJSON {
	t.Helper()
	rows, err := h.Pool().Query(t.Context(), `
		SELECT role, is_primary FROM customers.customer_contact_roles
		WHERE customer_id = $1 AND contact_id = $2
		ORDER BY CASE role WHEN 'billing' THEN 0 WHEN 'project' THEN 1 WHEN 'decision_maker' THEN 2 ELSE 3 END, role`,
		customerID, contactID)
	if err != nil {
		t.Fatalf("rolesOf(%d, %d): %v", customerID, contactID, err)
	}
	defer rows.Close()
	var out []contactRoleJSON
	for rows.Next() {
		var r contactRoleJSON
		if err := rows.Scan(&r.Role, &r.Primary); err != nil {
			t.Fatalf("rolesOf: scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// primaryHolderOf is the contact that holds role for customerID, or 0 when
// nobody does.
func primaryHolderOf(t *testing.T, h *modtest.Harness, customerID int32, role string) int32 {
	t.Helper()
	return int32(h.Count(t, `SELECT coalesce(max(contact_id), 0) FROM customers.customer_contact_roles
	                         WHERE customer_id = $1 AND role = $2 AND is_primary`, customerID, role))
}

// primaryCountOf is how many contacts hold role as primary — the invariant's
// own number, which must never be anything but 0 or 1.
func primaryCountOf(t *testing.T, h *modtest.Harness, customerID int32, role string) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles
	                   WHERE customer_id = $1 AND role = $2 AND is_primary`, customerID, role)
}

func TestAttachContact_FirstHolderOfARoleIsPrimaryWhateverItAsked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "First Holder Co")
	contact := createContact(t, c, map[string]any{"firstName": "First", "lastName": "Holdersen"})

	got := attachWithRoles(t, c, customer.Id, map[string]any{
		"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "billing", "primary": false}},
	})

	want := []contactRoleJSON{{Role: "billing", Primary: true}}
	if !reflect.DeepEqual(got.Roles, want) {
		t.Errorf("roles = %+v, want %+v (the first holder is primary whatever the request says)", got.Roles, want)
	}
	if !reflect.DeepEqual(rolesOf(t, h, customer.Id, contact.Id), want) {
		t.Errorf("stored roles = %+v, want %+v", rolesOf(t, h, customer.Id, contact.Id), want)
	}
}

func TestAttachContact_PrimaryTrueDemotesTheCurrentHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Demote On Attach Co")
	first := createContact(t, c, map[string]any{"firstName": "Incumbent", "lastName": "Personsen"})
	second := createContact(t, c, map[string]any{"firstName": "Usurper", "lastName": "Personsen"})

	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": first.Id, "title": "CFO",
		"roles": []any{map[string]any{"role": "billing"}}})
	got := attachWithRoles(t, c, customer.Id, map[string]any{"contactId": second.Id, "title": "Controller",
		"roles": []any{map[string]any{"role": "billing", "primary": true}}})

	if !reflect.DeepEqual(got.Roles, []contactRoleJSON{{Role: "billing", Primary: true}}) {
		t.Errorf("the new contact's roles = %+v, want billing primary", got.Roles)
	}
	if !reflect.DeepEqual(rolesOf(t, h, customer.Id, first.Id), []contactRoleJSON{{Role: "billing", Primary: false}}) {
		t.Errorf("the incumbent's roles = %+v, want billing not primary (it must have been demoted)", rolesOf(t, h, customer.Id, first.Id))
	}
	if n := primaryCountOf(t, h, customer.Id, "billing"); n != 1 {
		t.Errorf("primary billing holders = %d, want exactly 1", n)
	}
}

func TestUpdateCustomerContact_ClearingTheOnlyHoldersPrimaryIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Refused Clear Co")
	contact := createContact(t, c, map[string]any{"firstName": "Sole", "lastName": "Holdersen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "billing"}}})

	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "billing", "primary": false}}})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A contact that is the only or primary holder of the 'billing' role stays primary; make another contact primary instead"
	if msgs := problem.Errors["roles"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[roles] = %v, want [%q]", msgs, want)
	}
	if !reflect.DeepEqual(rolesOf(t, h, customer.Id, contact.Id), []contactRoleJSON{{Role: "billing", Primary: true}}) {
		t.Errorf("stored roles = %+v, want billing still primary (a refusal writes nothing)", rolesOf(t, h, customer.Id, contact.Id))
	}
}

// TestUpdateCustomerContact_OmittedPrimaryKeepsTheFlagInAReplace is the
// omitted-flag half of design D2's three-valued primary: a replace that adds a
// role and says nothing about the flag of one the contact already holds as
// primary must keep that flag, not demote it and not be refused. This is the
// rule that makes "the complete set of roles" a writable field at all — a
// client rebuilding the set from a checkbox group has no business having to
// echo every primary flag back to avoid demoting somebody.
func TestUpdateCustomerContact_OmittedPrimaryKeepsTheFlagInAReplace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Omitted Flag Co")
	contact := createContact(t, c, map[string]any{"firstName": "Omitted", "lastName": "Flagsen"})
	other := createContact(t, c, map[string]any{"firstName": "Other", "lastName": "Flagsen"})
	// contact takes project first, so it is project's primary; other holds
	// project too, so contact is not its ONLY holder — which is the case a
	// "the only holder stays primary" reading would let through and this one
	// must not.
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "project"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": other.Id, "title": "CTO",
		"roles": []any{map[string]any{"role": "project"}}})

	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project"}, map[string]any{"role": "billing"}}})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (an omitted primary asks for nothing and cannot be refused)", r.Status, r.Body)
	}
	var updated roledContactJSON
	r.JSON(&updated)
	want := []contactRoleJSON{{Role: "billing", Primary: true}, {Role: "project", Primary: true}}
	if !reflect.DeepEqual(updated.Roles, want) {
		t.Errorf("roles = %+v, want %+v (project's flag kept, billing primary as its first holder)", updated.Roles, want)
	}
	if !reflect.DeepEqual(rolesOf(t, h, customer.Id, contact.Id), want) {
		t.Errorf("stored roles = %+v, want %+v", rolesOf(t, h, customer.Id, contact.Id), want)
	}
	if got := primaryHolderOf(t, h, customer.Id, "project"); got != contact.Id {
		t.Errorf("primary project holder = %d, want %d (unmoved)", got, contact.Id)
	}
}

// TestUpdateCustomerContact_ExplicitPrimaryFalseInAReplaceIsRefused is the other
// half: saying `primary: false` OUT LOUD about a role this contact is the
// primary holder of is the refusal (design D2), whether or not anything else in
// the request changed. The two tests together are what pins the pointer: drop it
// and make the flag a plain bool, and exactly one of them must break.
func TestUpdateCustomerContact_ExplicitPrimaryFalseInAReplaceIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Explicit False Co")
	contact := createContact(t, c, map[string]any{"firstName": "Explicit", "lastName": "Falsesen"})
	other := createContact(t, c, map[string]any{"firstName": "Other", "lastName": "Falsesen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "project"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": other.Id, "title": "CTO",
		"roles": []any{map[string]any{"role": "project"}}})

	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project", "primary": false}, map[string]any{"role": "billing"}}})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A contact that is the only or primary holder of the 'project' role stays primary; make another contact primary instead"
	if msgs := problem.Errors["roles"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[roles] = %v, want [%q]", msgs, want)
	}
	// A refusal writes nothing at all — billing was never added.
	if got := rolesOf(t, h, customer.Id, contact.Id); !reflect.DeepEqual(got, []contactRoleJSON{{Role: "project", Primary: true}}) {
		t.Errorf("stored roles = %+v, want project primary and nothing else", got)
	}
}

func TestUpdateCustomerContact_LosingAPrimaryRolePromotesTheLongestStandingHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Promote Longest Co")
	leaving := createContact(t, c, map[string]any{"firstName": "Leaving", "lastName": "Personsen"})
	lowerID := createContact(t, c, map[string]any{"firstName": "LowerId", "lastName": "Personsen"})
	higherID := createContact(t, c, map[string]any{"firstName": "HigherId", "lastName": "Personsen"})

	// created_at and contact_id are made to DISAGREE, which is the whole point:
	// contacts get ascending ids in creation order, so attaching higherID
	// before lowerID — with h.Advance moving the clock in between — makes
	// higherID the longest-standing of the two while lowerID has the smaller
	// id. An implementation that promoted by id alone would pick lowerID, and
	// only this disagreement can tell the two rules apart.
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": leaving.Id, "title": "A",
		"roles": []any{map[string]any{"role": "billing"}}})
	h.Advance(time.Hour)
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": higherID.Id, "title": "B",
		"roles": []any{map[string]any{"role": "billing"}}})
	h.Advance(time.Hour)
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": lowerID.Id, "title": "C",
		"roles": []any{map[string]any{"role": "billing"}}})

	// leaving — billing's primary, as its first holder — drops the role.
	if r := putAssociation(t, c, customer.Id, leaving.Id, map[string]any{"title": "A", "roles": []any{}}); r.Status != http.StatusOK {
		t.Fatalf("drop billing: status %d body %s, want 200", r.Status, r.Body)
	}

	if got := primaryHolderOf(t, h, customer.Id, "billing"); got != higherID.Id {
		t.Errorf("primary billing holder = %d, want %d (the longest-standing remaining holder, by created_at — not %d, which merely has the smaller id)", got, higherID.Id, lowerID.Id)
	}
	if n := primaryCountOf(t, h, customer.Id, "billing"); n != 1 {
		t.Errorf("primary billing holders = %d, want exactly 1", n)
	}
}

func TestUpdateCustomerContact_OmittedRolesAreUnchangedAndEmptyClearsThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Omitted Roles Co")
	contact := createContact(t, c, map[string]any{"firstName": "Omitted", "lastName": "Rolesen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "billing"}, map[string]any{"role": "project"}}})

	// No `roles` key at all: the set is left alone (design D3).
	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CTO"})
	if r.Status != http.StatusOK {
		t.Fatalf("omit roles: status %d body %s, want 200", r.Status, r.Body)
	}
	var updated roledContactJSON
	r.JSON(&updated)
	want := []contactRoleJSON{{Role: "billing", Primary: true}, {Role: "project", Primary: true}}
	if !reflect.DeepEqual(updated.Roles, want) {
		t.Errorf("roles after omitting them = %+v, want %+v", updated.Roles, want)
	}
	if updated.Role != "CTO" || updated.Title == nil || *updated.Title != "CTO" {
		t.Errorf("role = %q, title = %v, want both \"CTO\"", updated.Role, updated.Title)
	}

	// An empty array clears them — and is allowed only because a title remains.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CTO", "roles": []any{}}); r.Status != http.StatusOK {
		t.Fatalf("clear roles: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := rolesOf(t, h, customer.Id, contact.Id); len(got) != 0 {
		t.Errorf("stored roles after []= %+v, want none", got)
	}
}

func TestAssociationRequests_RefuseAnUnknownRoleADuplicateAndAnEmptyRelationship(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Refusals Co")
	contact := createContact(t, c, map[string]any{"firstName": "Refused", "lastName": "Bodysen"})

	cases := []struct {
		name  string
		body  map[string]any
		field string
		want  string
	}{
		{
			name:  "an unknown role",
			body:  map[string]any{"contactId": contact.Id, "title": "CEO", "roles": []any{map[string]any{"role": "technical"}}},
			field: "roles",
			want:  "A contact role must be one of 'billing', 'project' or 'decision_maker', but was 'technical'",
		},
		{
			name:  "a role given twice",
			body:  map[string]any{"contactId": contact.Id, "title": "CEO", "roles": []any{map[string]any{"role": "billing"}, map[string]any{"role": "billing", "primary": true}}},
			field: "roles",
			want:  "A contact role can only be given once, but 'billing' was given more than once",
		},
		{
			name:  "neither a title nor a role",
			body:  map[string]any{"contactId": contact.Id, "roles": []any{}},
			field: "title",
			want:  "A contact needs a title or at least one role",
		},
		{
			name:  "no title, no role and no roles key either",
			body:  map[string]any{"contactId": contact.Id},
			field: "title",
			want:  "A contact needs a title or at least one role",
		},
	}
	for _, tc := range cases {
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), tc.body)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", tc.name, r.Status, r.Body)
			continue
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if msgs := problem.Errors[tc.field]; len(msgs) != 1 || msgs[0] != tc.want {
			t.Errorf("%s: errors[%s] = %v, want [%q]", tc.name, tc.field, msgs, tc.want)
		}
	}

	// Nothing was attached by any of them.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE customer_id = $1`, customer.Id); n != 0 {
		t.Errorf("associations = %d, want 0 (every case above is a refusal)", n)
	}
}

func TestAssociationRequests_TheCorpusShapeStillWorksAndTitleWins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Corpus Shape Co")
	corpusContact := createContact(t, c, map[string]any{"firstName": "Corpus", "lastName": "Shapesen"})
	bothContact := createContact(t, c, map[string]any{"firstName": "Both", "lastName": "Fieldsen"})

	// Literally the body the frozen corpus records (openapi/testdata/exchanges/
	// customers.jsonl): contactId and role, nothing else.
	got := attachWithRoles(t, c, customer.Id, map[string]any{"contactId": corpusContact.Id, "role": "CEO"})
	if got.Role != "CEO" || got.Title == nil || *got.Title != "CEO" {
		t.Errorf("role = %q, title = %v, want both \"CEO\" (role is an alias of title)", got.Role, got.Title)
	}
	if len(got.Roles) != 0 {
		t.Errorf("roles = %+v, want an empty array: a corpus-shaped request gives no typed roles", got.Roles)
	}

	// Both fields: title wins (design D1).
	both := attachWithRoles(t, c, customer.Id, map[string]any{"contactId": bothContact.Id, "role": "ignored", "title": "CTO"})
	if both.Role != "CTO" || both.Title == nil || *both.Title != "CTO" {
		t.Errorf("role = %q, title = %v, want both \"CTO\"", both.Role, both.Title)
	}

	// And on the list, in the shape the corpus recorded: the same four keys,
	// with the two new ones beside them.
	list := listCustomerContacts(t, c, customer.Id)
	if len(list.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(list.Data))
	}
	for _, item := range list.Data {
		if item.Roles == nil {
			t.Errorf("contact %d: roles is null, want an empty array (the server always answers it)", item.Contact.Id)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1`, customer.Id); n != 0 {
		t.Errorf("role rows = %d, want 0: a corpus-shaped request creates none", n)
	}
}

func TestGetContactCustomers_CarriesTheTitleAndTheRolesPerCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Manysided", "lastName": "Personsen"})
	alpha := createCustomer(t, c, "Alpha Roles AS")
	beta := createCustomer(t, c, "Beta Roles AS")
	attachWithRoles(t, c, alpha.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "decision_maker"}, map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, beta.Id, map[string]any{"contactId": contact.Id, "roles": []any{map[string]any{"role": "project"}}})

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/contacts/%d/customers", contact.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list roledCustomerListJSON
	r.JSON(&list)
	if len(list.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(list.Data))
	}
	// Sorted by customer name: Alpha first.
	if got := list.Data[0].Roles; !reflect.DeepEqual(got, []contactRoleJSON{{Role: "billing", Primary: true}, {Role: "decision_maker", Primary: true}}) {
		t.Errorf("Alpha's roles = %+v, want billing then decision_maker, both primary (the fixed order, not the request's)", got)
	}
	if list.Data[0].Role != "CEO" {
		t.Errorf("Alpha's role = %q, want \"CEO\"", list.Data[0].Role)
	}
	if list.Data[1].Role != "" || list.Data[1].Title != nil {
		t.Errorf("Beta's role = %q and title = %v, want \"\" and null (an association with roles and no title)", list.Data[1].Role, list.Data[1].Title)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE contact_id = $1`, contact.Id); n != 3 {
		t.Errorf("role rows for the contact = %d, want 3 (two at Alpha, one at Beta)", n)
	}
}

func TestDetachContact_PromotesAndRecordsThePromotionWithTheActingUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, userID, "Promoting Person")
	customer := createCustomer(t, c, "Detach Promotes Co")
	leaving := createContact(t, c, map[string]any{"firstName": "Leaving", "lastName": "Detachsen"})
	staying := createContact(t, c, map[string]any{"firstName": "Staying", "lastName": "Detachsen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": leaving.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": staying.Id, "title": "CFO",
		"roles": []any{map[string]any{"role": "billing"}}})

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, leaving.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("detach: status %d body %s, want 204", r.Status, r.Body)
	}

	if got := primaryHolderOf(t, h, customer.Id, "billing"); got != staying.Id {
		t.Errorf("primary billing holder = %d, want %d", got, staying.Id)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary := fmt.Sprintf("Now the primary billing contact: Staying Detachsen (#%d)", staying.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", event.Summary, wantSummary)
	}
	actorDisplay := modtest.One[string](t, h, `SELECT actor_display FROM customers.customers_timeline_entries
	                                           WHERE customer_id = $1 AND event_type = 'customer.contact_relationship_updated'
	                                           ORDER BY id DESC LIMIT 1`, customer.Id)
	if actorDisplay != userDisplayName(t, h, userID) {
		t.Errorf("actor_display = %q, want the caller who caused the promotion (%q)", actorDisplay, userDisplayName(t, h, userID))
	}
}

func TestDeleteContact_PromotesInEveryCustomerItWasPrimaryFor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	doomed := createContact(t, c, map[string]any{"firstName": "Doomed", "lastName": "Cascadesen"})
	survivor := createContact(t, c, map[string]any{"firstName": "Survivor", "lastName": "Cascadesen"})
	alpha := createCustomer(t, c, "Alpha Cascade AS")
	beta := createCustomer(t, c, "Beta Cascade AS")
	for _, customer := range []int32{alpha.Id, beta.Id} {
		attachWithRoles(t, c, customer, map[string]any{"contactId": doomed.Id, "title": "CEO",
			"roles": []any{map[string]any{"role": "billing"}}})
		attachWithRoles(t, c, customer, map[string]any{"contactId": survivor.Id, "title": "CFO",
			"roles": []any{map[string]any{"role": "billing"}}})
	}

	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", doomed.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete contact: status %d body %s, want 204", r.Status, r.Body)
	}

	for _, customer := range []int32{alpha.Id, beta.Id} {
		if got := primaryHolderOf(t, h, customer, "billing"); got != survivor.Id {
			t.Errorf("customer %d: primary billing holder = %d, want %d", customer, got, survivor.Id)
		}
		if n := primaryCountOf(t, h, customer, "billing"); n != 1 {
			t.Errorf("customer %d: primary billing holders = %d, want exactly 1", customer, n)
		}
		if n := countTimelineEvents(t, h, customer, "customer.contact_removed"); n != 1 {
			t.Errorf("customer %d: contact_removed events = %d, want 1", customer, n)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE contact_id = $1`, doomed.Id); n != 0 {
		t.Errorf("role rows left for the deleted contact = %d, want 0 (the cascade)", n)
	}
}

// TestUpdateCustomerContact_RecordsAnEventOnlyWhenSomethingMovedAndSaysWhat is
// also where the omitted-flag rule earns its keep. Two of its steps send a
// `roles` replace that lists a role the contact holds AS primary and says
// nothing about the flag — `[{"role":"project"}, {"role":"billing"}]` and then
// `[{"role":"project"}]`. Under a rule that read an omitted flag as false, both
// would be 400s refusing to demote the primary project contact, for requests
// that never asked to; because an omitted flag means "leave this one alone"
// (design D2, requestedRole), both are the plain role-set changes they look
// like. That is the case, not an incidental detail of the fixture.
func TestUpdateCustomerContact_RecordsAnEventOnlyWhenSomethingMovedAndSaysWhat(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Summary Co")
	contact := createContact(t, c, map[string]any{"firstName": "Summary", "lastName": "Personsen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "project"}}})

	// A resubmit of exactly what is stored: nothing at all.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project", "primary": true}}}); r.Status != http.StatusOK {
		t.Fatalf("resubmit: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.contact_relationship_updated"); n != 0 {
		t.Fatalf("events after a resubmit = %d, want 0", n)
	}

	// Adding a role the contact becomes primary for: the summary says so.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project"}, map[string]any{"role": "billing"}}}); r.Status != http.StatusOK {
		t.Fatalf("add billing: status %d body %s, want 200", r.Status, r.Body)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary := fmt.Sprintf("Now the primary billing contact: Summary Personsen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", event.Summary, wantSummary)
	}
	if got := event.Payload["roles"]; !reflect.DeepEqual(got, []any{
		map[string]any{"role": "billing", "primary": true},
		map[string]any{"role": "project", "primary": true},
	}) {
		t.Errorf("payload roles = %+v, want billing then project, both primary", got)
	}
	if got := event.Payload["title"]; got != "CEO" {
		t.Errorf("payload title = %v, want \"CEO\"", got)
	}

	// Only the title moving keeps the wording it has always had.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "Chairman"}); r.Status != http.StatusOK {
		t.Fatalf("retitle: status %d body %s, want 200", r.Status, r.Body)
	}
	event = fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary = fmt.Sprintf("Contact relationship updated: Summary Personsen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary after a retitle = %q, want %q", event.Summary, wantSummary)
	}

	// Dropping a role it is NOT primary for anywhere else reads as "Roles
	// updated" — no new primary, but the set moved. project is the only role
	// left after billing goes, and the contact is its primary already, so no
	// "Now the primary …" applies.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "Chairman",
		"roles": []any{map[string]any{"role": "project"}}}); r.Status != http.StatusOK {
		t.Fatalf("drop billing: status %d body %s, want 200", r.Status, r.Body)
	}
	event = fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary = fmt.Sprintf("Roles updated: Summary Personsen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary after dropping a role = %q, want %q", event.Summary, wantSummary)
	}
}

// TestUpdateCustomerContact_AReplaceKeepsARetainedRolesSeniority pins what
// DeleteContactRolesNotIn exists for (design D3): a set replace SUBTRACTS the
// roles the request dropped, it does not clear the set and re-insert it, so a
// role the association keeps keeps its created_at. That column is not cosmetic —
// it is what "the longest-standing holder" means, so a replace that reset it
// would silently reshuffle who inherits a primary flag later, which no
// assertion about the answered roles could notice.
func TestUpdateCustomerContact_AReplaceKeepsARetainedRolesSeniority(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Seniority Co")
	contact := createContact(t, c, map[string]any{"firstName": "Senior", "lastName": "Rolesen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "project"}}})

	createdAt := func(role string) time.Time {
		t.Helper()
		return modtest.One[time.Time](t, h, `SELECT created_at FROM customers.customer_contact_roles
		                                     WHERE customer_id = $1 AND contact_id = $2 AND role = $3`,
			customer.Id, contact.Id, role)
	}
	before := createdAt("project")

	// The clock moves, so a re-inserted row would be visibly younger rather
	// than accidentally identical.
	h.Advance(time.Hour)
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project"}, map[string]any{"role": "billing"}}}); r.Status != http.StatusOK {
		t.Fatalf("add billing: status %d body %s, want 200", r.Status, r.Body)
	}

	if after := createdAt("project"); !after.Equal(before) {
		t.Errorf("project's created_at = %s, want it unmoved at %s (a replace subtracts, it does not re-insert)", after, before)
	}
	// And the role the request added really did take the later clock, which is
	// what makes the assertion above a comparison and not a tautology.
	if added := createdAt("billing"); !added.After(before) {
		t.Errorf("billing's created_at = %s, want it later than project's %s", added, before)
	}
}

// TestUpdateCustomerContact_AnEmptyBodyClearsTheFieldsAndKeepsTheRoles pins the
// one corner where `roles` being the only "omitted = unchanged" field on this
// endpoint becomes visible: PUT is a REPLACE of the whole association, so a body
// that mentions neither title nor phone nor email clears all three, and it is
// only accepted at all because the roles the association keeps satisfy the
// title-or-role rule (design D1, D3). Before `role` stopped being required this
// request could not be written; now it can, and the answer is a stripped
// association rather than a 400 — deliberate, and easy to mistake for a bug the
// first time a client sends a partial body, which is why it has a test and a
// sentence in docs/customers.md.
func TestUpdateCustomerContact_AnEmptyBodyClearsTheFieldsAndKeepsTheRoles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Replace Semantics Co")
	contact := createContact(t, c, map[string]any{"firstName": "Replaced", "lastName": "Wholesen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"phone": "+47 900 00 001", "email": "ceo@replace.co",
		"roles": []any{map[string]any{"role": "project"}}})

	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{})
	if r.Status != http.StatusOK {
		t.Fatalf("empty-body update: status %d body %s, want 200 (the kept roles satisfy the title-or-role rule)", r.Status, r.Body)
	}
	var answered roledContactJSON
	r.JSON(&answered)
	if answered.Title != nil || answered.Phone != nil || answered.Email != nil {
		t.Errorf("answered title/phone/email = %v/%v/%v, want all three cleared", answered.Title, answered.Phone, answered.Email)
	}
	if answered.Role != "" {
		t.Errorf("answered role = %q, want \"\" (the deprecated alias answers the title or empty)", answered.Role)
	}
	if !reflect.DeepEqual(answered.Roles, []contactRoleJSON{{Role: "project", Primary: true}}) {
		t.Errorf("answered roles = %+v, want project/primary kept — roles is the only field an omission leaves alone", answered.Roles)
	}

	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts
	                    WHERE customer_id = $1 AND contact_id = $2
	                      AND title IS NULL AND phone IS NULL AND email IS NULL`, customer.Id, contact.Id); n != 1 {
		t.Errorf("stored rows with all three cleared = %d, want 1", n)
	}
	if got := rolesOf(t, h, customer.Id, contact.Id); !reflect.DeepEqual(got, []contactRoleJSON{{Role: "project", Primary: true}}) {
		t.Errorf("stored roles = %+v, want project/primary untouched", got)
	}

	// The clearing is a change, so it is recorded — with the cleared fields, not
	// the ones it replaced, and under the plain wording, because no role moved.
	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary := fmt.Sprintf("Contact relationship updated: Replaced Wholesen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", event.Summary, wantSummary)
	}
	wantPayload := map[string]any{
		"customerId":  float64(customer.Id),
		"contactId":   float64(contact.Id),
		"displayName": "Replaced Wholesen",
		"firstName":   "Replaced",
		"middleName":  nil,
		"lastName":    "Wholesen",
		"role":        "",
		"title":       nil,
		"roles":       []any{map[string]any{"role": "project", "primary": true}},
		"phone":       nil,
		"email":       nil,
	}
	if !reflect.DeepEqual(event.Payload, wantPayload) {
		t.Errorf("payload = %+v, want %+v", event.Payload, wantPayload)
	}
}
