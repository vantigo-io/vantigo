package customers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the membership half of phase 4 delivery D (customer groups
// design D3): PUT /customers/{id}/group, the group on every customer response,
// and the list's groupId filter.
//
// It is owner_test.go's matrix with the value in this module's own table
// instead of identity's directory: 404 for the customer, 409 for a stale
// revision ahead of the no-op check, a no-op that writes nothing at all, a
// field error for a group that does not exist, and one event per real change
// with the names snapshotted into it.

// putCustomerGroup PUTs /customers/{id}/group with the given body. The
// vocabulary's own tests (groups_test.go) deliberately do NOT use it — they set
// the column directly, so they stay independent of the membership endpoint —
// but every test of the membership and of what it feeds does: this file's,
// group_concurrency_test.go's and the billing profile's alike.
func putCustomerGroup(t *testing.T, c *modtest.Client, id int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/group", id), body)
}

// TestPutCustomerGroup_SetsMovesAndClears walks the membership's whole life,
// because each step asserts the state the previous one left — and every
// response is a SafeCustomerResponse, so the group on it is asserted from the
// answer rather than from a follow-up GET.
func TestPutCustomerGroup_SetsMovesAndClears(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	customer := createCustomer(t, c, "Grouped Co")

	// A customer belongs to no group until somebody says so, and the field is
	// ABSENT rather than null on the wire (design D3).
	if got := fetchCustomerJSON(t, c, customer.Id); got.Group != nil {
		t.Errorf("a fresh customer's group = %+v, want it absent", got.Group)
	}

	r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id, "revision": 1})
	if r.Status != http.StatusOK {
		t.Fatalf("set the group: status %d body %s, want 200", r.Status, r.Body)
	}
	var answered customerJSON
	r.JSON(&answered)
	if answered.Group == nil || answered.Group.Id != retail.Id || answered.Group.Name != "Retail" {
		t.Fatalf("answered group = %+v, want Retail: the PUT answers the whole customer", answered.Group)
	}
	if answered.Revision != 2 {
		t.Errorf("revision = %d, want 2: the group is a column on the row", answered.Revision)
	}

	// Moved, not added: one group at a time is the whole point of a column.
	moved := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": key.Id})
	if moved.Status != http.StatusOK {
		t.Fatalf("move the customer: status %d body %s, want 200", moved.Status, moved.Body)
	}
	if got := fetchCustomerJSON(t, c, customer.Id); got.Group == nil || got.Group.Name != "Key accounts" {
		t.Errorf("group after the move = %+v, want Key accounts", got.Group)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND group_id = $2`, customer.Id, key.Id); n != 1 {
		t.Errorf("group_id rows = %d, want 1: the column holds exactly one group", n)
	}

	// Cleared: a real change like any other — it bumps the revision and records
	// an event, and the field goes back to being absent.
	cleared := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": nil})
	if cleared.Status != http.StatusOK {
		t.Fatalf("clear the group: status %d body %s, want 200", cleared.Status, cleared.Body)
	}
	// A fresh value, not answered again: encoding/json leaves a field the body
	// omits exactly as it was, so decoding into the struct that still holds
	// Retail would report Retail for a response that carries no group at all.
	var afterClear customerJSON
	cleared.JSON(&afterClear)
	if afterClear.Group != nil {
		t.Errorf("group after clearing = %+v, want it absent", afterClear.Group)
	}
	if afterClear.Revision != 4 {
		t.Errorf("revision = %d, want 4: set, move, clear are three writes", afterClear.Revision)
	}
	// An omitted groupId means the same as a null one — oapi-codegen collapses
	// the two into a nil *uuid.UUID, and clearing an already-cleared group is
	// the no-op below rather than an error.
	events := countTimelineEvents(t, h, customer.Id, "customer.group_changed")
	empty := putCustomerGroup(t, c, customer.Id, map[string]any{})
	if empty.Status != http.StatusOK {
		t.Fatalf("an empty body: status %d body %s, want 200", empty.Status, empty.Body)
	}
	var afterEmpty customerJSON
	empty.JSON(&afterEmpty)
	if afterEmpty.Revision != 4 || afterEmpty.Group != nil {
		t.Errorf("an empty body answered revision %d group %+v, want 4 and no group: it is the no-op",
			afterEmpty.Revision, afterEmpty.Group)
	}
	if got := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); got != events {
		t.Errorf("customer.group_changed events = %d after an empty body, want %d", got, events)
	}
}

// TestPutCustomerGroup_TheOwnersMatrix is owner_test.go's own matrix, case for
// case: the four refusals, and the no-op that writes nothing at all.
func TestPutCustomerGroup_TheOwnersMatrix(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	customer := createCustomer(t, c, "Matrix Co")

	// (1) The customer's own 404, ahead of everything.
	if r := putCustomerGroup(t, c, 999_999, map[string]any{"groupId": retail.Id}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown customer: status %d body %s, want 404", r.Status, r.Body)
	}

	// (2) A stale revision is a 409 BEFORE the no-op check, so resubmitting the
	// current group with a stale revision is still a conflict rather than a free
	// pass (customers foundation design D5).
	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("set the group: status %d body %s", r.Status, r.Body)
	}
	stale := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id, "revision": 1})
	if stale.Status != http.StatusConflict {
		t.Fatalf("a stale revision on a no-op: status %d body %s, want 409", stale.Status, stale.Body)
	}
	var problem conflictProblemJSON
	stale.JSON(&problem)
	if problemTitle(problem.Title) != "Customer revision conflict" || problem.Code != nil {
		t.Errorf("conflict = title %q code %v, want the revision conflict with no code", problemTitle(problem.Title), problem.Code)
	}

	// (3) The no-op: the same group, at the current revision, writes nothing —
	// no revision bump, no updated_at move, no event.
	before := fetchCustomerJSON(t, c, customer.Id)
	events := countTimelineEvents(t, h, customer.Id, "customer.group_changed")
	noop := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id, "revision": before.Revision})
	if noop.Status != http.StatusOK {
		t.Fatalf("the no-op: status %d body %s, want 200", noop.Status, noop.Body)
	}
	var answered customerJSON
	noop.JSON(&answered)
	if answered.Revision != before.Revision || !answered.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("no-op answered revision %d updatedAt %s, want %d/%s unchanged",
			answered.Revision, answered.UpdatedAt, before.Revision, before.UpdatedAt)
	}
	if answered.Group == nil || answered.Group.Id != retail.Id {
		t.Errorf("no-op answered group = %+v, want Retail: a no-op still answers the customer", answered.Group)
	}
	if got := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); got != events {
		t.Errorf("customer.group_changed events = %d after a no-op, want %d", got, events)
	}
	// Clearing a customer that is in no group is the nil/nil half of the same
	// rule, and it is the case an implementation that only compares dereferenced
	// ids gets wrong.
	unassigned := createCustomer(t, c, "Never Grouped AS")
	nilNoop := putCustomerGroup(t, c, unassigned.Id, map[string]any{"groupId": nil})
	if nilNoop.Status != http.StatusOK {
		t.Fatalf("clearing an ungrouped customer: status %d body %s, want 200", nilNoop.Status, nilNoop.Body)
	}
	var ungrouped customerJSON
	nilNoop.JSON(&ungrouped)
	if ungrouped.Revision != 1 || ungrouped.Group != nil {
		t.Errorf("clearing an ungrouped customer answered revision %d group %+v, want 1 and no group: nothing was written",
			ungrouped.Revision, ungrouped.Group)
	}
	if n := countTimelineEvents(t, h, unassigned.Id, "customer.group_changed"); n != 0 {
		t.Errorf("customer.group_changed events = %d, want 0", n)
	}

	// (4) A group that does not exist is a field error on groupId, not a 404:
	// the customer exists and the caller may edit it, so what is wrong is the
	// body they sent (ownerNotFound's own reasoning).
	missing := uuid.New()
	bad := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": missing.String()})
	if bad.Status != http.StatusBadRequest {
		t.Fatalf("an unknown group: status %d body %s, want 400", bad.Status, bad.Body)
	}
	var invalid validationProblemJSON
	bad.JSON(&invalid)
	want := fmt.Sprintf("Customer group %s does not exist", missing)
	if msgs := invalid.Errors["groupId"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[groupId] = %v, want [%q]", msgs, want)
	}
}

// TestPutCustomerGroup_RecordsTheEventWithSnapshottedNames pins
// customer.group_changed's summary, payload and payload version for all three
// shapes of the change, and the reason the names are IN the payload: renaming a
// group afterwards must not rewrite what the timeline says happened.
func TestPutCustomerGroup_RecordsTheEventWithSnapshottedNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	customer := createCustomer(t, c, "Event Co")

	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s", r.Status, r.Body)
	}
	assigned := fetchTimelineEvent(t, h, customer.Id, "customer.group_changed")
	if assigned.Summary != "Moved to group Retail" {
		t.Errorf("summary = %q, want %q", assigned.Summary, "Moved to group Retail")
	}
	if assigned.PayloadVersion != 1 {
		t.Errorf("payloadVersion = %d, want 1", assigned.PayloadVersion)
	}
	if assigned.Payload["before"] != nil {
		t.Errorf("before = %v, want null: the customer was in no group", assigned.Payload["before"])
	}
	after, ok := assigned.Payload["after"].(map[string]any)
	if !ok || after["groupId"] != retail.Id || after["name"] != "Retail" {
		t.Errorf("after = %v, want the group's id and its name at the time", assigned.Payload["after"])
	}
	// JSON numbers decode as float64 into map[string]any.
	if assigned.Payload["customerId"] != float64(customer.Id) {
		t.Errorf("customerId = %v, want %d", assigned.Payload["customerId"], customer.Id)
	}

	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": key.Id}); r.Status != http.StatusOK {
		t.Fatalf("move: status %d body %s", r.Status, r.Body)
	}
	// The vocabulary is renamed AFTER the move: the entries above must still say
	// what they said, which is the whole reason the name is snapshotted.
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{"name": "Retail chains"}); r.Status != http.StatusOK {
		t.Fatalf("rename: status %d body %s", r.Status, r.Body)
	}
	moved := fetchTimelineEvent(t, h, customer.Id, "customer.group_changed")
	if moved.Summary != "Moved from Retail to Key accounts" {
		t.Errorf("summary = %q, want %q — the name at the time, not today's", moved.Summary, "Moved from Retail to Key accounts")
	}
	movedBefore, ok := moved.Payload["before"].(map[string]any)
	if !ok || movedBefore["groupId"] != retail.Id || movedBefore["name"] != "Retail" {
		t.Errorf("before = %v, want Retail's id and the name it had when the move happened", moved.Payload["before"])
	}
	movedAfter, ok := moved.Payload["after"].(map[string]any)
	if !ok || movedAfter["groupId"] != key.Id || movedAfter["name"] != "Key accounts" {
		t.Errorf("after = %v, want Key accounts' id and name", moved.Payload["after"])
	}
	if moved.Payload["customerId"] != float64(customer.Id) {
		t.Errorf("customerId = %v, want %d", moved.Payload["customerId"], customer.Id)
	}

	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": nil}); r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s", r.Status, r.Body)
	}
	removed := fetchTimelineEvent(t, h, customer.Id, "customer.group_changed")
	if removed.Summary != "Removed from group Key accounts" {
		t.Errorf("summary = %q, want %q", removed.Summary, "Removed from group Key accounts")
	}
	if removed.Payload["after"] != nil {
		t.Errorf("after = %v, want null: the customer is in no group", removed.Payload["after"])
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); n != 3 {
		t.Errorf("customer.group_changed events = %d, want 3 (set, move, clear)", n)
	}
}

// TestPutCustomerGroup_OnlyTheSubResourceSetsTheGroup is the owner's rule
// restated (design D3): POST /customers and PUT /customers/{id} do not learn a
// groupId. A body that names one is a body with an unknown key — no schema in
// customers.yaml sets additionalProperties: false — so it is ignored, and the
// customer ends up in no group.
func TestPutCustomerGroup_OnlyTheSubResourceSetsTheGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Sneaky Co", "groupId": retail.Id})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s, want 201", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)
	if got := fetchCustomerJSON(t, c, created.Id); got.Group != nil {
		t.Errorf("group after a create that named one = %+v, want it absent", got.Group)
	}
	if u := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Sneaky Co", "groupId": retail.Id,
	}); u.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", u.Status, u.Body)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Group != nil {
		t.Errorf("group after an update that named one = %+v, want it absent", got.Group)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND group_id IS NULL`, created.Id); n != 1 {
		t.Errorf("group_id is set on the row, want NULL: only the sub-resource writes it")
	}
}

// TestGetCustomers_GroupIdFilter pins the filter in BOTH queries: the count and
// the rows have to agree, which is the one thing a hand-kept duplicate WHERE
// clause can get wrong.
func TestGetCustomers_GroupIdFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	inGroup := createCustomer(t, c, "Filter In Group AS")
	ungrouped := createCustomer(t, c, "Filter No Group AS")
	if r := putCustomerGroup(t, c, inGroup.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("set the group: status %d body %s", r.Status, r.Body)
	}

	byGroup := getList(t, c, "groupId="+retail.Id+"&pageSize=100")
	if len(byGroup.Data) != 1 || byGroup.Data[0].Id != inGroup.Id {
		t.Fatalf("groupId=<uuid> rows = %+v, want only the member", byGroup.Data)
	}
	if byGroup.Pagination.TotalCount != 1 {
		t.Errorf("totalCount = %d, want 1: CountCustomers and ListCustomers must apply the same WHERE",
			byGroup.Pagination.TotalCount)
	}

	// The harness is per test, so the ungrouped customer is the only customer in
	// no group. Asserting that it is FOUND, rather than only that nothing found
	// has a group, is what stops a 'none' branch that matches nothing at all
	// from passing.
	none := getList(t, c, "groupId=none&pageSize=100")
	if none.Pagination.TotalCount != 1 || len(none.Data) != 1 || none.Data[0].Id != ungrouped.Id {
		t.Errorf("groupId=none: totalCount %d, rows %+v, want exactly the ungrouped customer",
			none.Pagination.TotalCount, none.Data)
	}
	for _, row := range none.Data {
		if row.Group != nil {
			t.Errorf("groupId=none returned %q with group %+v", row.Name, row.Group)
		}
		if row.Id == inGroup.Id {
			t.Errorf("groupId=none returned the member")
		}
	}

	// An unknown uuid matches nothing — a filter, not an error (design D3).
	unknown := getList(t, c, "groupId="+uuid.NewString())
	if unknown.Pagination.TotalCount != 0 || len(unknown.Data) != 0 {
		t.Errorf("an unknown groupId: %d rows, totalCount %d, want none", len(unknown.Data), unknown.Pagination.TotalCount)
	}

	// The shape check is the module's own query-parameter wording, with the
	// trailing period every message in validateGetCustomersParams has.
	bad := c.Do(http.MethodGet, "/api/v1/customers?groupId=nonsense", nil)
	if bad.Status != http.StatusBadRequest {
		t.Fatalf("groupId=nonsense: status %d body %s, want 400", bad.Status, bad.Body)
	}
	var problem problemDetailsJSON
	bad.JSON(&problem)
	if want := "'groupId' must be a group id or 'none', but was 'nonsense'."; problem.Detail == nil || *problem.Detail != want {
		t.Errorf("detail = %v, want %q", problem.Detail, want)
	}
}
