package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the owner half of phase 4 delivery A (owner and tags design
// D1, D4): PUT /customers/{id}/owner, GET /customers/assignable-users, the
// owner on every customer response, and the list's ownerId filter. It is
// shaped after contact_info_test.go, because the owner PUT is contact info's
// PUT with one column instead of three — the happy path, the validation
// refusals, the revision guard, the no-op rule, the generated timeline event
// with its actor, the permission gate.
//
// What is new here, and has no analogue in the module so far, is that the
// value being written belongs to ANOTHER module's data: identity's users,
// reached only through contracts.UserDirectory. The three states that
// directory can report — a name, a disabled account, no account at all — are
// each a test below, and each is arranged with harness_test.go's own fixtures
// against identity.users, because modtest composes the real identity module
// rather than a fake.

// putOwner PUTs /customers/{id}/owner with body and returns the raw response,
// leaving status assertions to the caller.
func putOwner(t *testing.T, c *modtest.Client, id int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/owner", id), body)
}

// assignableUserJSON decodes CustomerAssignableUser.
type assignableUserJSON struct {
	UserId      string `json:"userId"`
	DisplayName string `json:"displayName"`
}

// getAssignableUsers GETs /customers/assignable-users with the given query
// string (no leading '?') and decodes a 200.
func getAssignableUsers(t *testing.T, c *modtest.Client, query string) []assignableUserJSON {
	t.Helper()
	path := "/api/v1/customers/assignable-users"
	if query != "" {
		path += "?" + query
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s, want 200", path, r.Status, r.Body)
	}
	var users []assignableUserJSON
	r.JSON(&users)
	return users
}

// TestPutCustomersByIdOwner_SetsAndClears_ShowsInGetAndList pins the whole
// visible behaviour of an owner: the PUT answers the customer with the owner
// named from the DIRECTORY (never from anything stored on the customer), the
// detail read and the list row agree with it, and clearing it makes the field
// absent again rather than present-and-null.
func TestPutCustomersByIdOwner_SetsAndClears_ShowsInGetAndList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, callerID, "Kari Nordmann")
	created := createCustomer(t, c, "Owned Co")

	r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String()})
	if r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	var owned customerJSON
	r.JSON(&owned)
	if owned.Owner == nil || owned.Owner.UserId != callerID.String() ||
		owned.Owner.DisplayName != "Kari Nordmann" || !owned.Owner.Active {
		t.Fatalf("owner = %+v, want %s/Kari Nordmann/active", owned.Owner, callerID)
	}

	if got := fetchCustomerJSON(t, c, created.Id); got.Owner == nil || got.Owner.DisplayName != "Kari Nordmann" {
		t.Errorf("GET owner = %+v, want Kari Nordmann", got.Owner)
	}
	list := getList(t, c, "search="+url.QueryEscape("Owned Co"))
	if len(list.Data) != 1 || list.Data[0].Owner == nil || list.Data[0].Owner.DisplayName != "Kari Nordmann" {
		t.Errorf("list owner = %+v, want Kari Nordmann", list.Data)
	}

	r = putOwner(t, c, created.Id, map[string]any{"ownerUserId": nil})
	if r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", r.Status, r.Body)
	}
	var cleared customerJSON
	r.JSON(&cleared)
	if cleared.Owner != nil {
		t.Errorf("owner = %+v after clearing, want absent", cleared.Owner)
	}
	// An absent body field means the same as null, as every full-replace body
	// in this module does (customers foundation design D1).
	if r := putOwner(t, c, created.Id, map[string]any{}); r.Status != http.StatusOK {
		t.Fatalf("clear with an empty body: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestPutCustomersByIdOwner_RefusesAUserWhoCannotOwn pins both field errors
// design D1 names, with projects' own wording: a user id nobody holds, and a
// disabled account. Both are 400s keyed ownerUserId, not 404s — the customer
// exists and the caller may edit it, so what is wrong is the body.
func TestPutCustomersByIdOwner_RefusesAUserWhoCannotOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Refuser Co")

	missing := uuid.New()
	r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": missing.String()})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("unknown user: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("User %s does not exist", missing)
	if got := problem.Errors["ownerUserId"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[ownerUserId] = %v, want [%s]", got, want)
	}

	_, disabledID := h.SignInUser(t, "customers:view")
	disableUser(t, h, disabledID)
	r = putOwner(t, c, created.Id, map[string]any{"ownerUserId": disabledID.String()})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("disabled user: status %d body %s, want 400", r.Status, r.Body)
	}
	r.JSON(&problem)
	want = fmt.Sprintf("User %s is disabled and cannot own a customer", disabledID)
	if got := problem.Errors["ownerUserId"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[ownerUserId] = %v, want [%s]", got, want)
	}

	if got := fetchCustomerJSON(t, c, created.Id); got.Owner != nil || got.Revision != 1 {
		t.Errorf("after two refusals: owner = %+v revision = %d, want no owner and revision 1", got.Owner, got.Revision)
	}
}

// TestPutCustomersByIdOwner_AnOwnerDisabledAfterwardsKeepsTheCustomer is
// design D1's explicit ruling, and the reason the check above is on the
// WRITE alone: an account disabled later is reported inactive, and the
// customer still has an owner. Nothing revokes it behind the caller's back.
func TestPutCustomersByIdOwner_AnOwnerDisabledAfterwardsKeepsTheCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	_, ownerID := h.SignInUser(t, "customers:view")
	setDisplayName(t, h, ownerID, "Ola Nordmann")
	created := createCustomer(t, c, "Inactive Owner Co")

	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": ownerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	disableUser(t, h, ownerID)

	got := fetchCustomerJSON(t, c, created.Id)
	if got.Owner == nil || got.Owner.DisplayName != "Ola Nordmann" || got.Owner.Active {
		t.Errorf("owner = %+v, want Ola Nordmann with active false", got.Owner)
	}
}

// TestGetCustomer_AnOwnerTheDirectoryForgotIsUnknownUser pins the actorFor
// precedent (design D1): an owner whose account is gone is still an owner, and
// it reads as Unknown user with active false — not as a 500, and not as an
// unowned customer.
func TestGetCustomer_AnOwnerTheDirectoryForgotIsUnknownUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	_, ownerID := h.SignInUser(t, "customers:view")
	created := createCustomer(t, c, "Forgotten Owner Co")
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": ownerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	forgetUser(t, h, ownerID)

	got := fetchCustomerJSON(t, c, created.Id)
	if got.Owner == nil || got.Owner.UserId != ownerID.String() ||
		got.Owner.DisplayName != "Unknown user" || got.Owner.Active {
		t.Errorf("owner = %+v, want %s/Unknown user/inactive", got.Owner, ownerID)
	}
}

// TestPutCustomersByIdOwner_StaleRevisionIsAConflictAndWritesNothing pins the
// revision guard ahead of the no-op check, exactly as contact info's own PUT
// orders them: resubmitting with a stale revision is a conflict, not a free
// pass.
func TestPutCustomersByIdOwner_StaleRevisionIsAConflictAndWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	created := createCustomer(t, c, "Stale Owner Co")
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String(), "revision": 1}); r.Status != http.StatusOK {
		t.Fatalf("first set: status %d body %s, want 200", r.Status, r.Body)
	}

	r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": nil, "revision": 1})
	if r.Status != http.StatusConflict {
		t.Fatalf("stale: status %d body %s, want 409", r.Status, r.Body)
	}
	var conflict conflictProblemJSON
	r.JSON(&conflict)
	// problemTitle (timeline_test.go:65) dereferences the nullable title, which
	// is how contact_info_test.go's own stale-revision case reads it: the exact
	// wording, not just the 409, is what separates the Go-side pre-check from
	// the database guard's fallback.
	if problemTitle(conflict.Title) != "Customer revision conflict" || conflict.Code != nil {
		t.Errorf("conflict = title %q code %v, want the revision conflict with no code",
			problemTitle(conflict.Title), conflict.Code)
	}
	got := fetchCustomerJSON(t, c, created.Id)
	if got.Owner == nil || got.Revision != 2 {
		t.Errorf("after the conflict: owner = %+v revision = %d, want the owner kept at revision 2", got.Owner, got.Revision)
	}
}

// TestPutCustomersByIdOwner_NoOpWritesNothing pins the no-op rule (customers
// foundation design D5): re-sending the owner the customer already has bumps
// no revision, moves no updated_at and records no event. Both directions are
// here, because "clear an already-unowned customer" is the case an
// implementation that only compares non-nil values gets wrong.
func TestPutCustomersByIdOwner_NoOpWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	created := createCustomer(t, c, "Idempotent Owner Co")

	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": nil}); r.Status != http.StatusOK {
		t.Fatalf("clear an unowned customer: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 1 {
		t.Errorf("revision = %d after clearing an unowned customer, want 1", got.Revision)
	}

	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	before := fetchCustomerJSON(t, c, created.Id)
	// The harness clock is frozen, so without this the updatedAt half of the
	// assertion below could not fail even if the no-op DID write: the write
	// would stamp the same instant it already carries. contact_info_test.go
	// advances it for exactly this reason (:192).
	h.Advance(time.Second)
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("re-set: status %d body %s, want 200", r.Status, r.Body)
	}
	after := fetchCustomerJSON(t, c, created.Id)
	if after.Revision != before.Revision || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("re-setting the same owner moved revision %d→%d / updatedAt %v→%v, want neither",
			before.Revision, after.Revision, before.UpdatedAt, after.UpdatedAt)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.owner_changed"); n != 1 {
		t.Errorf("customer.owner_changed events = %d, want 1 (the no-op records nothing)", n)
	}
}

// TestPutCustomersByIdOwner_RecordsTheEventWithBothNamesAndTheActor pins
// customer.owner_changed's payload (design D1): before and after each carry
// the user id AND the display name resolved at write time, so the timeline
// still reads correctly after the account is renamed or removed — and the
// entry is attributed to whoever made the change, not to the new owner.
func TestPutCustomersByIdOwner_RecordsTheEventWithBothNamesAndTheActor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, callerID, "Kari Nordmann")
	_, secondID := h.SignInUser(t, "customers:view")
	setDisplayName(t, h, secondID, "Ola Nordmann")
	created := createCustomer(t, c, "Event Owner Co")

	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("first set: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": secondID.String()}); r.Status != http.StatusOK {
		t.Fatalf("reassign: status %d body %s, want 200", r.Status, r.Body)
	}

	if n := countTimelineEvents(t, h, created.Id, "customer.owner_changed"); n != 2 {
		t.Fatalf("customer.owner_changed events = %d, want 2", n)
	}
	event := fetchTimelineEvent(t, h, created.Id, "customer.owner_changed")
	before, _ := event.Payload["before"].(map[string]any)
	after, _ := event.Payload["after"].(map[string]any)
	if before == nil || before["displayName"] != "Kari Nordmann" || before["userId"] != callerID.String() {
		t.Errorf("before = %v, want Kari Nordmann / %s", before, callerID)
	}
	if after == nil || after["displayName"] != "Ola Nordmann" || after["userId"] != secondID.String() {
		t.Errorf("after = %v, want Ola Nordmann / %s", after, secondID)
	}
	if event.Summary != "Customer owner changed: Kari Nordmann → Ola Nordmann" {
		t.Errorf("summary = %q, want the two names", event.Summary)
	}

	gotActor := modtest.One[string](t, h, `
		SELECT actor_user_id::text FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.owner_changed' ORDER BY id DESC LIMIT 1`, created.Id)
	if gotActor != callerID.String() {
		t.Errorf("actor_user_id = %s, want %s (whoever reassigned, not the new owner)", gotActor, callerID)
	}

	// Clearing it records a third event whose after is null, so the timeline
	// says who stopped owning the customer rather than going quiet.
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": nil}); r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", r.Status, r.Body)
	}
	cleared := fetchTimelineEvent(t, h, created.Id, "customer.owner_changed")
	if cleared.Payload["after"] != nil {
		t.Errorf("after = %v on the clearing event, want null", cleared.Payload["after"])
	}
	if cleared.Summary != "Customer owner changed: Ola Nordmann → nobody" {
		t.Errorf("summary = %q, want the clearing wording", cleared.Summary)
	}
}

// TestGetCustomers_OwnerIdFilter pins design D1's three accepted values
// against one another on one dataset, plus the refusal. 'me' is resolved from
// the SESSION — the request never names the caller — which is why the caller
// owns one of these customers and asserts it finds exactly that one.
func TestGetCustomers_OwnerIdFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	_, otherID := h.SignInUser(t, "customers:view")

	mine := createCustomer(t, c, "Filter Mine Co")
	theirs := createCustomer(t, c, "Filter Theirs Co")
	createCustomer(t, c, "Filter Nobody Co")
	if r := putOwner(t, c, mine.Id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("own mine: status %d body %s", r.Status, r.Body)
	}
	if r := putOwner(t, c, theirs.Id, map[string]any{"ownerUserId": otherID.String()}); r.Status != http.StatusOK {
		t.Fatalf("own theirs: status %d body %s", r.Status, r.Body)
	}

	names := func(query string) []string {
		list := getList(t, c, query+"&search="+url.QueryEscape("Filter "))
		out := make([]string, 0, len(list.Data))
		for _, row := range list.Data {
			out = append(out, row.Name)
		}
		return out
	}
	if got := names("ownerId=me"); len(got) != 1 || got[0] != "Filter Mine Co" {
		t.Errorf("ownerId=me = %v, want [Filter Mine Co]", got)
	}
	if got := names("ownerId=" + otherID.String()); len(got) != 1 || got[0] != "Filter Theirs Co" {
		t.Errorf("ownerId=<other> = %v, want [Filter Theirs Co]", got)
	}
	if got := names("ownerId=none"); len(got) != 1 || got[0] != "Filter Nobody Co" {
		t.Errorf("ownerId=none = %v, want [Filter Nobody Co]", got)
	}
	// The count must agree with the page: the two list queries share their
	// WHERE clause by hand, and a filter added to one and not the other is
	// exactly the drift their comments warn about.
	if list := getList(t, c, "ownerId=me&search="+url.QueryEscape("Filter ")); list.Pagination.TotalCount != 1 {
		t.Errorf("totalCount = %d for ownerId=me, want 1", list.Pagination.TotalCount)
	}

	r := c.Do(http.MethodGet, "/api/v1/customers?ownerId=someone", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("ownerId=someone: status %d body %s, want 400", r.Status, r.Body)
	}
	if want := "'ownerId' must be a user id, 'me' or 'none', but was 'someone'."; !strings.Contains(string(r.Body), want) {
		t.Errorf("body = %s, want it to contain %q", r.Body, want)
	}
}

// TestGetCustomersAssignableUsers pins design D1's picker endpoint: the
// directory's active users only, narrowed by query, capped at 20, and a limit
// outside 1-20 refused in this module's query-parameter wording.
func TestGetCustomersAssignableUsers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, callerID, "Searchable Kari")
	_, activeID := h.SignInUser(t, "customers:view")
	setDisplayName(t, h, activeID, "Searchable Ola")
	_, disabledID := h.SignInUser(t, "customers:view")
	setDisplayName(t, h, disabledID, "Searchable Nils")
	disableUser(t, h, disabledID)

	found := getAssignableUsers(t, c, "query="+url.QueryEscape("Searchable"))
	names := make([]string, 0, len(found))
	for _, u := range found {
		names = append(names, u.DisplayName)
	}
	if len(names) != 2 || names[0] != "Searchable Kari" || names[1] != "Searchable Ola" {
		t.Errorf("assignable users = %v, want [Searchable Kari Searchable Ola] — active only, display-name order", names)
	}

	if got := getAssignableUsers(t, c, "query="+url.QueryEscape("Searchable")+"&limit=1"); len(got) != 1 {
		t.Errorf("limit=1 answered %d users, want 1", len(got))
	}

	for _, limit := range []string{"0", "21"} {
		r := c.Do(http.MethodGet, "/api/v1/customers/assignable-users?limit="+limit, nil)
		if r.Status != http.StatusBadRequest {
			t.Errorf("limit=%s: status %d body %s, want 400", limit, r.Status, r.Body)
		}
		if want := fmt.Sprintf("'limit' must be between 1 and 20, but was %s.", limit); !strings.Contains(string(r.Body), want) {
			t.Errorf("limit=%s body = %s, want it to contain %q", limit, r.Body, want)
		}
	}
}

// TestGetCustomersAssignableUsers_CapsAtTwenty seeds twenty-one matching users
// and asserts the answer is exactly twenty of them, in display-name order.
// Twenty-one, not "some": a cap asserted against a dataset smaller than the cap
// proves nothing, and `len(got) <= 20` would pass against an installation with
// three users and a broken cap.
func TestGetCustomersAssignableUsers_CapsAtTwenty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	for i := 1; i <= 21; i++ {
		seedNamedUser(t, h, fmt.Sprintf("Capped %02d", i))
	}

	found := getAssignableUsers(t, c, "query="+url.QueryEscape("Capped"))
	if len(found) != 20 {
		t.Fatalf("assignable users = %d, want exactly 20 of the 21 seeded", len(found))
	}
	// Display-name order, so the one left out is the last one alphabetically —
	// which is also what tells a cap apart from an arbitrary truncation.
	if found[0].DisplayName != "Capped 01" || found[19].DisplayName != "Capped 20" {
		t.Errorf("first/last = %q/%q, want Capped 01/Capped 20", found[0].DisplayName, found[19].DisplayName)
	}
}

// TestGetCustomers_OneOwnerOnTwoRowsResolvesBoth exercises decorate's
// de-duplication: the distinct-id pass means a page where one person owns every
// row asks the directory about them once. Nothing in the harness can count
// directory calls — Deps.Users is identity's real implementation, not a fake —
// so what is asserted is the behaviour the de-duplication must not break: both
// rows still name the owner. It is here so the branch is executed at all, and
// so a future refactor that indexes the owner map by row rather than by user id
// fails a test instead of only getting slower.
func TestGetCustomers_OneOwnerOnTwoRowsResolvesBoth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, callerID, "Kari Nordmann")
	first := createCustomer(t, c, "Shared Owner One")
	second := createCustomer(t, c, "Shared Owner Two")
	for _, id := range []int32{first.Id, second.Id} {
		if r := putOwner(t, c, id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
			t.Fatalf("own %d: status %d body %s", id, r.Status, r.Body)
		}
	}

	list := getList(t, c, "search="+url.QueryEscape("Shared Owner"))
	if len(list.Data) != 2 {
		t.Fatalf("list = %d rows, want 2", len(list.Data))
	}
	for _, row := range list.Data {
		if row.Owner == nil || row.Owner.DisplayName != "Kari Nordmann" {
			t.Errorf("%s owner = %+v, want Kari Nordmann", row.Name, row.Owner)
		}
	}
}

// TestOwnerPermissions pins design D4's answer: an owner is not sensitive
// data. Reading one needs nothing beyond customers:view, and writing one
// needs customers:update and no new key — the router enforces both from
// x-vantigo-access, and these two cases are what would fail if a narrower key
// were ever introduced without the catalog and the contract agreeing.
func TestOwnerPermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	writer, ownerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, ownerID, "Kari Nordmann")
	created := createCustomer(t, writer, "Permission Owner Co")
	if r := putOwner(t, writer, created.Id, map[string]any{"ownerUserId": ownerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}

	viewer := h.SignIn(t, "customers:view")
	if got := fetchCustomerJSON(t, viewer, created.Id); got.Owner == nil || got.Owner.DisplayName != "Kari Nordmann" {
		t.Errorf("a view-only caller saw owner = %+v, want Kari Nordmann", got.Owner)
	}
	if r := putOwner(t, viewer, created.Id, map[string]any{"ownerUserId": nil}); r.Status != http.StatusForbidden {
		t.Errorf("view-only PUT: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := viewer.Do(http.MethodGet, "/api/v1/customers/assignable-users", nil); r.Status != http.StatusForbidden {
		t.Errorf("view-only assignable-users: status %d body %s, want 403", r.Status, r.Body)
	}
}

// TestPutCustomersByIdOwner_WhenCustomerDoesNotExist_ReturnsNotFound
func TestPutCustomersByIdOwner_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)

	r := putOwner(t, c, 999999, map[string]any{"ownerUserId": callerID.String()})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}
