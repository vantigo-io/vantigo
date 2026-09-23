package customers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the vocabulary half of phase 4 delivery D (customer groups
// design D2): GET/POST /customers/groups and PUT/DELETE
// /customers/groups/{groupId}.
//
// It is the tag vocabulary's own test file (tags_test.go) with two deliberate
// differences, and each has a test here that would pass against the tags'
// version and must not: the second field is a PAYMENT TERM, so it is validated
// 0-365 and a PUT that omits it CLEARS the group's default; and a delete
// REFUSES a group with members (409 group_in_use) rather than detaching them,
// because a silent detach would change every member's effective payment term
// with no record on any customer.

// setCustomerGroup puts a customer in a group (or takes it out, with a nil
// groupID) through the column migration 00027 added. The vocabulary's own
// delete rule needs members before it can refuse a delete, and PUT
// /customers/{id}/group is the NEXT task's endpoint — calling it here would be
// a request no operation in customers.yaml matches, which the package's own
// contract recorder reports as an error on top of the 404. This is the fixture
// shortcut harness_test.go's setDisplayName/disableUser already take for
// identity's own columns.
// groupID is a group's id as the API answered it (a string) or nil to take the
// customer out of every group; the ::uuid cast is what lets an untyped nil and a
// string both reach a uuid column through the same statement.
func setCustomerGroup(t *testing.T, h *modtest.Harness, customerID int32, groupID any) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET group_id = $2::uuid WHERE id = $1`, customerID, groupID)
}

// groupSummaryJSON decodes CustomerGroupSummary.
type groupSummaryJSON struct {
	Id                      string `json:"id"`
	Name                    string `json:"name"`
	DefaultPaymentTermsDays *int32 `json:"defaultPaymentTermsDays"`
	CustomerCount           int32  `json:"customerCount"`
}

// listGroups GETs /customers/groups and decodes a 200.
func listGroups(t *testing.T, c *modtest.Client) []groupSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/customers/groups", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /api/v1/customers/groups: status %d body %s, want 200", r.Status, r.Body)
	}
	var groups []groupSummaryJSON
	r.JSON(&groups)
	return groups
}

// createGroup POSTs a group and fails the test on anything but 201.
func createGroup(t *testing.T, c *modtest.Client, body map[string]any) groupSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/customers/groups", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("POST /api/v1/customers/groups %v: status %d body %s, want 201", body, r.Status, r.Body)
	}
	var group groupSummaryJSON
	r.JSON(&group)
	return group
}

// TestCustomerGroups_CreateListUpdateDelete walks the vocabulary's whole life
// in one test, because each step asserts the state the previous one left: the
// list is name-ascending with a member count, a PUT is a full replace that
// keeps the id and the count, and a delete of an empty group is a 204 that is
// not idempotent.
func TestCustomerGroups_CreateListUpdateDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	if retail.CustomerCount != 0 {
		t.Errorf("a fresh group's customerCount = %d, want 0", retail.CustomerCount)
	}
	if retail.DefaultPaymentTermsDays == nil || *retail.DefaultPaymentTermsDays != 30 {
		t.Errorf("Retail's defaultPaymentTermsDays = %v, want 30", retail.DefaultPaymentTermsDays)
	}
	if key.DefaultPaymentTermsDays != nil {
		t.Errorf("a group created without a default has defaultPaymentTermsDays = %v, want it absent", key.DefaultPaymentTermsDays)
	}

	groups := listGroups(t, c)
	if len(groups) != 2 || groups[0].Name != "Key accounts" || groups[1].Name != "Retail" {
		t.Fatalf("groups = %+v, want Key accounts then Retail (name-ascending)", groups)
	}

	// A full replace of both fields: the new name AND a default that is gone
	// because the body did not name one (design D2 — the request says what the
	// group is, not what changed).
	r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{"name": "Retail chains"})
	if r.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", r.Status, r.Body)
	}
	var updated groupSummaryJSON
	r.JSON(&updated)
	if updated.Id != retail.Id || updated.Name != "Retail chains" {
		t.Errorf("updated = %+v, want the same id and the new name", updated)
	}
	if updated.DefaultPaymentTermsDays != nil {
		t.Errorf("updated.defaultPaymentTermsDays = %v, want it cleared: the PUT is a full replace",
			updated.DefaultPaymentTermsDays)
	}

	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+key.Id, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete an empty group: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+key.Id, nil); r.Status != http.StatusNotFound {
		t.Errorf("deleting it again: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+uuid.NewString(), map[string]any{"name": "Nobody"}); r.Status != http.StatusNotFound {
		t.Errorf("updating an unknown group: status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestCustomerGroups_NameIsUniqueIgnoringCaseAndNormalisation is the one thing
// a group vocabulary must not get wrong: 'Retail' and 'retail' are the same
// word, and so are a composed and a decomposed 'Café'. Both a create and an
// update answer 409 group_exists, and renaming a group to the name it already
// has is a plain 200 rather than a conflict with itself.
func TestCustomerGroups_NameIsUniqueIgnoringCaseAndNormalisation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	other := createGroup(t, c, map[string]any{"name": "Café"}) // composed é

	r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "retail"})
	if r.Status != http.StatusConflict {
		t.Fatalf("create 'retail': status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problem.Code == nil || *problem.Code != "group_exists" {
		t.Errorf("code = %v, want group_exists", problem.Code)
	}

	// Café is the same word decomposed: NFC-normalised before it is
	// compared, so the unique index on lower(name) sees the name it already
	// holds (values.go's validateGroupName, the tags' own rule).
	if r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "Café"}); r.Status != http.StatusConflict {
		t.Errorf("create a decomposed 'Café': status %d body %s, want 409", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+other.Id, map[string]any{"name": "RETAIL"}); r.Status != http.StatusConflict {
		t.Errorf("rename to 'RETAIL': status %d body %s, want 409", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{"name": "Retail"}); r.Status != http.StatusOK {
		t.Errorf("renaming a group to its own name: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestCustomerGroups_DefaultPaymentTermsIsValidated pins the field the billing
// profile's own rule validates (design D2): 0-365 inclusive, keyed
// defaultPaymentTermsDays rather than paymentTermsDays, and 0 is a legitimate
// value ("due on receipt") rather than a missing one.
func TestCustomerGroups_DefaultPaymentTermsIsValidated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	for _, days := range []int{-1, 366} {
		r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{
			"name": fmt.Sprintf("Group %d", days), "defaultPaymentTermsDays": days,
		})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("defaultPaymentTermsDays %d: status %d body %s, want 400", days, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		want := fmt.Sprintf("Payment terms must be between 0 and 365 days, but was %d", days)
		if msgs := problem.Errors["defaultPaymentTermsDays"]; len(msgs) != 1 || msgs[0] != want {
			t.Errorf("errors[defaultPaymentTermsDays] = %v, want [%q]", msgs, want)
		}
		if _, ok := problem.Errors["paymentTermsDays"]; ok {
			t.Errorf("errors = %v, want no key \"paymentTermsDays\": this request's field is defaultPaymentTermsDays", problem.Errors)
		}
	}

	onReceipt := createGroup(t, c, map[string]any{"name": "Cash", "defaultPaymentTermsDays": 0})
	if onReceipt.DefaultPaymentTermsDays == nil || *onReceipt.DefaultPaymentTermsDays != 0 {
		t.Errorf("defaultPaymentTermsDays = %v, want 0: due on receipt is a decision, not an absence",
			onReceipt.DefaultPaymentTermsDays)
	}

	// The name's own rule, the tags' word for word under its own noun.
	r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "   "})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("a blank name: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if msgs := problem.Errors["name"]; len(msgs) != 1 || msgs[0] != "A group name cannot be null or empty" {
		t.Errorf("errors[name] = %v, want the blank-name refusal", msgs)
	}
	long := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": strings.Repeat("g", 101)})
	if long.Status != http.StatusBadRequest {
		t.Errorf("a 101-character name: status %d body %s, want 400", long.Status, long.Body)
	}
}

// TestCustomerGroups_DeleteRefusesAGroupInUse is the difference from tags that
// matters most (design D2): a tag's delete cascades because a label going away
// says nothing about the customer, and a group's must not, because every member
// would silently change what it inherits. The count is in the detail so the
// refusal says what has to be moved.
func TestCustomerGroups_DeleteRefusesAGroupInUse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 14})
	first := createCustomer(t, c, "Grouped Co")
	second := createCustomer(t, c, "Grouped Too AS")
	for _, id := range []int32{first.Id, second.Id} {
		setCustomerGroup(t, h, id, retail.Id)
	}

	// The count the refusal is about is the count the vocabulary reports: both
	// the list and a PUT's answer carry it, and POST's 0-by-construction is the
	// only place it is not read from the rows.
	groups := listGroups(t, c)
	if len(groups) != 1 || groups[0].CustomerCount != 2 {
		t.Errorf("groups = %+v, want Retail with customerCount 2", groups)
	}
	put := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 14})
	if put.Status != http.StatusOK {
		t.Fatalf("PUT Retail: status %d body %s, want 200", put.Status, put.Body)
	}
	var updated groupSummaryJSON
	put.JSON(&updated)
	if updated.CustomerCount != 2 {
		t.Errorf("PUT's customerCount = %d, want 2: the answer is what the Manage groups modal keeps on screen", updated.CustomerCount)
	}

	r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+retail.Id, nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("delete a group in use: status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problem.Code == nil || *problem.Code != "group_in_use" {
		t.Fatalf("code = %v, want group_in_use", problem.Code)
	}
	if problem.Detail == nil || !strings.Contains(*problem.Detail, "2 customers") {
		t.Errorf("detail = %v, want it to name the two members", problem.Detail)
	}
	// Nothing was written: the group and both memberships stand.
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_groups WHERE id = $1`, retail.Id); n != 1 {
		t.Errorf("group rows = %d after the refusal, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE group_id = $1`, retail.Id); n != 2 {
		t.Errorf("members = %d after the refusal, want 2", n)
	}

	// Moved out one at a time: the singular is worth its own assertion, since a
	// refusal that says "1 customers" is the kind of thing nobody notices until
	// a customer does.
	setCustomerGroup(t, h, second.Id, nil)
	r = c.Do(http.MethodDelete, "/api/v1/customers/groups/"+retail.Id, nil)
	r.JSON(&problem)
	if r.Status != http.StatusConflict || problem.Detail == nil || !strings.Contains(*problem.Detail, "1 customer. Move it") {
		t.Errorf("one member left: status %d detail %v, want 409 naming one customer", r.Status, problem.Detail)
	}
	setCustomerGroup(t, h, first.Id, nil)
	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+retail.Id, nil); r.Status != http.StatusNoContent {
		t.Errorf("delete the now-empty group: status %d body %s, want 204", r.Status, r.Body)
	}
}

// TestCustomerGroups_VocabularyWritesRecordNoTimelineEvent is design D2's other
// half: the group is the vocabulary, not the customer. Renaming a group — or
// moving its default, which changes what every member inherits — records
// nothing at all on a member's timeline, the tags' rule restated for a field
// that carries more weight than a colour.
//
// The membership is set through the column rather than through PUT
// /customers/{id}/group (setCustomerGroup above), which is what the next task
// adds. So this test says exactly what it checks and no more: a vocabulary
// write records NOTHING — not "nothing beyond the membership's own event",
// which is group_membership_test.go's to prove once that endpoint exists. The
// customer's timeline holds its one customer.created entry before and after.
func TestCustomerGroups_VocabularyWritesRecordNoTimelineEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	customer := createCustomer(t, c, "Quiet Co")
	setCustomerGroup(t, h, customer.Id, retail.Id)
	before := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, customer.Id)

	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{
		"name": "Retail chains", "defaultPaymentTermsDays": 60,
	}); r.Status != http.StatusOK {
		t.Fatalf("rename and re-default: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); n != 0 {
		t.Errorf("customer.group_changed events = %d after a vocabulary write, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, customer.Id); n != before {
		t.Errorf("timeline entries = %d, want %d: a vocabulary write records nothing at all", n, before)
	}

	// And the delete of an empty group is as quiet: the group the customer is
	// in cannot be deleted at all (the test above), so this is a second group.
	spare := createGroup(t, c, map[string]any{"name": "Spare"})
	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+spare.Id, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the empty group: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, customer.Id); n != before {
		t.Errorf("timeline entries = %d after a delete elsewhere in the vocabulary, want %d", n, before)
	}
}

// TestCustomerGroups_ReadingNeedsViewAndWritingNeedsUpdate pins the permission
// ruling (design D2): no new key. A caller with customers:view alone reads the
// vocabulary — a group's name and default are installation policy, and a
// member's INHERITED term is a policy fact rather than a negotiated one — and
// every write needs customers:update, exactly as the tag vocabulary's do.
func TestCustomerGroups_ReadingNeedsViewAndWritingNeedsUpdate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := authenticatedClient(t, h)
	group := createGroup(t, admin, map[string]any{"name": "Public sector", "defaultPaymentTermsDays": 45})

	reader, _ := h.SignInUser(t, "customers:view")
	if r := reader.Do(http.MethodGet, "/api/v1/customers/groups", nil); r.Status != http.StatusOK {
		t.Errorf("a view-only caller reading the vocabulary: status %d body %s, want 200", r.Status, r.Body)
	}
	for _, call := range []struct {
		method, path string
		// any rather than map[string]any: the DELETE row's nil must be an
		// untyped nil, or the client sends a JSON null body to an operation
		// that declares none and the contract recorder reports it.
		body any
	}{
		{http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "Sneaky"}},
		{http.MethodPut, "/api/v1/customers/groups/" + group.Id, map[string]any{"name": "Sneaky"}},
		{http.MethodDelete, "/api/v1/customers/groups/" + group.Id, nil},
	} {
		if r := reader.Do(call.method, call.path, call.body); r.Status != http.StatusForbidden {
			t.Errorf("%s %s as a view-only caller: status %d body %s, want 403", call.method, call.path, r.Status, r.Body)
		}
	}
}
