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

// This file is the tags half of phase 4 delivery A (owner and tags design D2,
// D3, D4): the vocabulary's four operations, the customer's set replace, the
// tagId list filter, and the tags that ride on every customer response.
//
// The shape is communications' own tags (internal/communications/tags.go) with
// three deliberate differences, and each of them has a test here that would
// pass against communications' version and must not: the uniqueness is
// case-insensitive; a customer's tags are REPLACED as a set rather than linked
// and unlinked one at a time; and the list carries a customerCount, because
// design D3's delete confirmation has to say what it will affect.

// tagSummaryJSON decodes CustomerTagSummary.
type tagSummaryJSON struct {
	Id            string  `json:"id"`
	Name          string  `json:"name"`
	Color         *string `json:"color"`
	CustomerCount int32   `json:"customerCount"`
}

// listTags GETs /customers/tags and decodes a 200.
func listTags(t *testing.T, c *modtest.Client) []tagSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/customers/tags", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /api/v1/customers/tags: status %d body %s, want 200", r.Status, r.Body)
	}
	var tags []tagSummaryJSON
	r.JSON(&tags)
	return tags
}

// createTag POSTs a tag and fails the test on anything but 201.
func createTag(t *testing.T, c *modtest.Client, body map[string]any) tagSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/customers/tags", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("POST /api/v1/customers/tags %v: status %d body %s, want 201", body, r.Status, r.Body)
	}
	var tag tagSummaryJSON
	r.JSON(&tag)
	return tag
}

// putCustomerTags PUTs /customers/{id}/tags with the given ids.
func putCustomerTags(t *testing.T, c *modtest.Client, id int32, tagIDs []string) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/tags", id), map[string]any{"tagIds": tagIDs})
}

// TestCustomerTags_CreateListRenameDelete walks the vocabulary's whole life in
// one test, because each step's assertion is about the state the previous one
// left: the list is name-ascending, the count is the customers carrying the
// tag, a rename keeps both the id and the count, and a delete takes the links
// with it (the table's own cascade) without touching the customers.
func TestCustomerTags_CreateListRenameDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	vip := createTag(t, c, map[string]any{"name": "VIP", "color": "grape"})
	prospect := createTag(t, c, map[string]any{"name": "Prospect"})
	if vip.CustomerCount != 0 {
		t.Errorf("a fresh tag's customerCount = %d, want 0", vip.CustomerCount)
	}
	if prospect.Color != nil {
		t.Errorf("a tag created without a colour has color = %v, want null", prospect.Color)
	}

	customer := createCustomer(t, c, "Tagged Co")
	if r := putCustomerTags(t, c, customer.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag the customer: status %d body %s, want 200", r.Status, r.Body)
	}

	tags := listTags(t, c)
	if len(tags) != 2 || tags[0].Name != "Prospect" || tags[1].Name != "VIP" {
		t.Fatalf("tags = %+v, want Prospect then VIP (name-ascending)", tags)
	}
	if tags[1].CustomerCount != 1 || tags[0].CustomerCount != 0 {
		t.Errorf("customerCounts = %d/%d, want 0 for Prospect and 1 for VIP", tags[0].CustomerCount, tags[1].CustomerCount)
	}

	r := c.Do(http.MethodPut, "/api/v1/customers/tags/"+vip.Id, map[string]any{"name": "Key account", "color": "teal"})
	if r.Status != http.StatusOK {
		t.Fatalf("rename: status %d body %s, want 200", r.Status, r.Body)
	}
	var renamed tagSummaryJSON
	r.JSON(&renamed)
	if renamed.Id != vip.Id || renamed.Name != "Key account" || renamed.Color == nil || *renamed.Color != "teal" || renamed.CustomerCount != 1 {
		t.Errorf("renamed = %+v, want the same id, the new name and colour, and the count kept", renamed)
	}
	// A rename records nothing on the customers carrying the tag (design D2:
	// the tag is the vocabulary, not the customer).
	if n := countTimelineEvents(t, h, customer.Id, "customer.tags_changed"); n != 1 {
		t.Errorf("customer.tags_changed events = %d after a rename, want 1 (only the set replace)", n)
	}

	if r := c.Do(http.MethodDelete, "/api/v1/customers/tags/"+vip.Id, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, customer.Id); len(got.Tags) != 0 {
		t.Errorf("customer tags = %+v after deleting the tag, want none — the cascade", got.Tags)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/customers/tags/"+vip.Id, nil); r.Status != http.StatusNotFound {
		t.Errorf("deleting it again: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/customers/tags/"+uuid.New().String(), map[string]any{"name": "Ghost"}); r.Status != http.StatusNotFound {
		t.Errorf("renaming a tag that does not exist: status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestCustomerTags_DuplicateNameIsAConflictIgnoringCase is the one difference
// from communications worth its own test: a tag is a vocabulary word, so 'VIP'
// and 'vip' are the same word. Both the create and the rename must say so, and
// both with code tag_exists, so a UI can offer "you already have that tag"
// rather than a bare 409.
func TestCustomerTags_DuplicateNameIsAConflictIgnoringCase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createTag(t, c, map[string]any{"name": "VIP"})
	other := createTag(t, c, map[string]any{"name": "Prospect"})

	for _, name := range []string{"VIP", "vip", "  ViP  "} {
		r := c.Do(http.MethodPost, "/api/v1/customers/tags", map[string]any{"name": name})
		if r.Status != http.StatusConflict {
			t.Fatalf("create %q: status %d body %s, want 409", name, r.Status, r.Body)
		}
		var conflict conflictProblemJSON
		r.JSON(&conflict)
		if conflict.Code == nil || *conflict.Code != "tag_exists" {
			t.Errorf("create %q conflict code = %v, want tag_exists", name, conflict.Code)
		}
	}

	r := c.Do(http.MethodPut, "/api/v1/customers/tags/"+other.Id, map[string]any{"name": "vip"})
	if r.Status != http.StatusConflict {
		t.Fatalf("rename onto an existing name: status %d body %s, want 409", r.Status, r.Body)
	}
	// Renaming a tag to the name it already has is not a conflict with itself.
	if r := c.Do(http.MethodPut, "/api/v1/customers/tags/"+other.Id, map[string]any{"name": "Prospect"}); r.Status != http.StatusOK {
		t.Errorf("renaming a tag to its own name: status %d body %s, want 200", r.Status, r.Body)
	}
	// And the same rule with teeth: fixing the CASE of a tag's own name. The
	// unique index compares lower(name), so 'Prospect' → 'PROSPECT' collides with
	// the very row being updated — which the index itself excludes and a
	// check-then-update implementation (a SELECT for an existing lower(name)
	// without "AND id <> the row") does not. The same-name case above passes for
	// such an implementation too whenever it compares exactly; this one cannot.
	r = c.Do(http.MethodPut, "/api/v1/customers/tags/"+other.Id, map[string]any{"name": "PROSPECT"})
	if r.Status != http.StatusOK {
		t.Fatalf("a case-only rename: status %d body %s, want 200", r.Status, r.Body)
	}
	var recased tagSummaryJSON
	r.JSON(&recased)
	if recased.Id != other.Id || recased.Name != "PROSPECT" {
		t.Errorf("recased = %+v, want the same id carrying the new casing", recased)
	}
	if got := listTags(t, c); len(got) != 2 {
		t.Errorf("tags = %+v, want the original two", got)
	}
}

// TestCustomerTags_DuplicateNameIsAConflictAcrossUnicodeForms is the second
// half of "a tag is a vocabulary word" (final fix wave M2): 'Café' typed with a
// precomposed é and 'Café' typed with an e plus a combining acute are the same
// word to every reader and two different byte strings to lower(name), so
// without normalisation an installation ends up with two Café chips nobody can
// tell apart and a filter that splits its customers between them. The server
// normalises to NFC before it validates, which is why the second create is the
// same 409 a repeated 'vip' gets.
func TestCustomerTags_DuplicateNameIsAConflictAcrossUnicodeForms(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	const composed = "Café"    // é as one code point
	const decomposed = "Café" // e + combining acute
	created := createTag(t, c, map[string]any{"name": composed})

	r := c.Do(http.MethodPost, "/api/v1/customers/tags", map[string]any{"name": decomposed})
	if r.Status != http.StatusConflict {
		t.Fatalf("create the decomposed form: status %d body %s, want 409", r.Status, r.Body)
	}
	var conflict conflictProblemJSON
	r.JSON(&conflict)
	if conflict.Code == nil || *conflict.Code != "tag_exists" {
		t.Errorf("conflict code = %v, want tag_exists", conflict.Code)
	}

	// The stored name is the composed form whichever form was sent: a tag
	// created from the decomposed one reads back as the same bytes this one
	// did, so the UI never has to compare strings two ways.
	other := createTag(t, c, map[string]any{"name": "Façade"})
	r = c.Do(http.MethodPut, "/api/v1/customers/tags/"+other.Id, map[string]any{"name": "Façade"})
	if r.Status != http.StatusOK {
		t.Fatalf("rename to the decomposed form of its own name: status %d body %s, want 200", r.Status, r.Body)
	}
	var renamed tagSummaryJSON
	r.JSON(&renamed)
	if renamed.Name != "Façade" {
		t.Errorf("renamed name = %q, want the composed form %q", renamed.Name, "Façade")
	}
	if got := listTags(t, c); len(got) != 2 || got[0].Id != created.Id || got[0].Name != composed {
		t.Errorf("tags = %+v, want the two originals with %q stored composed", got, composed)
	}
}

// TestCustomerTags_RefusesABadNameOrColour pins the two validation rules over
// HTTP, keyed by the request's own field names (values_test.go carries the
// table-driven coverage of the rules themselves).
func TestCustomerTags_RefusesABadNameOrColour(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers/tags", map[string]any{"name": "  ", "color": "#ff0000"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if got := problem.Errors["name"]; len(got) != 1 || got[0] != "A tag name cannot be null or empty" {
		t.Errorf("errors[name] = %v, want the blank-name message", got)
	}
	if got := problem.Errors["color"]; len(got) != 1 {
		t.Fatalf("errors[color] = %v, want one message", got)
	}
	// Both fields are reported together, never short-circuited on the first
	// failure — the module's all-errors-at-once convention.
	if len(problem.Errors) != 2 {
		t.Errorf("errors = %v, want exactly name and color", problem.Errors)
	}
	if len(listTags(t, c)) != 0 {
		t.Error("a refused create left a tag behind")
	}
}

// TestPutCustomersByIdTags_ReplacesTheSet pins what a set replace means: the
// answer is the customer's tags name-ascending, a second call with a different
// set replaces rather than adds, an empty array clears, and the customer row is
// untouched throughout — no revision bump, no updatedAt move (design D2: tags
// are off the row).
func TestPutCustomersByIdTags_ReplacesTheSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	vip := createTag(t, c, map[string]any{"name": "VIP", "color": "grape"})
	prospect := createTag(t, c, map[string]any{"name": "Prospect"})
	churned := createTag(t, c, map[string]any{"name": "Churned"})
	customer := createCustomer(t, c, "Replace Co")
	before := fetchCustomerJSON(t, c, customer.Id)
	// Advanced so the updatedAt half of the final assertion can fail: with a
	// frozen clock a write to the customer row would stamp the instant it
	// already carries (contact_info_test.go:192 does the same).
	h.Advance(time.Second)

	r := putCustomerTags(t, c, customer.Id, []string{vip.Id, prospect.Id})
	if r.Status != http.StatusOK {
		t.Fatalf("first replace: status %d body %s, want 200", r.Status, r.Body)
	}
	var answered struct {
		Tags []tagJSON `json:"tags"`
	}
	r.JSON(&answered)
	if len(answered.Tags) != 2 || answered.Tags[0].Name != "Prospect" || answered.Tags[1].Name != "VIP" {
		t.Fatalf("tags = %+v, want Prospect then VIP", answered.Tags)
	}
	if answered.Tags[1].Color == nil || *answered.Tags[1].Color != "grape" {
		t.Errorf("VIP color = %v, want grape", answered.Tags[1].Color)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{churned.Id}); r.Status != http.StatusOK {
		t.Fatalf("second replace: status %d body %s, want 200", r.Status, r.Body)
	}
	got := fetchCustomerJSON(t, c, customer.Id)
	if len(got.Tags) != 1 || got.Tags[0].Name != "Churned" {
		t.Errorf("tags = %+v after replacing, want only Churned — a replace is not an add", got.Tags)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{}); r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", r.Status, r.Body)
	}
	cleared := fetchCustomerJSON(t, c, customer.Id)
	if cleared.Tags == nil || len(cleared.Tags) != 0 {
		t.Errorf("tags = %+v after clearing, want an empty array (never null)", cleared.Tags)
	}
	if cleared.Revision != before.Revision || !cleared.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("three set replaces moved revision %d→%d / updatedAt %v→%v, want neither: tags are off the row",
			before.Revision, cleared.Revision, before.UpdatedAt, cleared.UpdatedAt)
	}
}

// TestPutCustomersByIdTags_UnknownIdIsAFieldError pins that an id no tag holds
// is a 400 on tagIds — not a 500 from a foreign-key violation, and not a
// silently shorter set. Duplicates in the request are tolerated, because a
// set is a set.
func TestPutCustomersByIdTags_UnknownIdIsAFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	customer := createCustomer(t, c, "Unknown Tag Co")

	missing := uuid.New()
	r := putCustomerTags(t, c, customer.Id, []string{vip.Id, missing.String()})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("Tag %s does not exist", missing)
	if got := problem.Errors["tagIds"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[tagIds] = %v, want [%s]", got, want)
	}
	if got := fetchCustomerJSON(t, c, customer.Id); len(got.Tags) != 0 {
		t.Errorf("tags = %+v after the refusal, want none written", got.Tags)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{vip.Id, vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("duplicate ids: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, customer.Id); len(got.Tags) != 1 {
		t.Errorf("tags = %+v for a request naming one tag twice, want one", got.Tags)
	}

	if r := putCustomerTags(t, c, 999999, []string{vip.Id}); r.Status != http.StatusNotFound {
		t.Errorf("a customer that does not exist: status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestPutCustomersByIdTags_RecordsTheEventOnlyWhenTheSetChanged pins design
// D2's event: added and removed, by id and by the name at the time, and
// nothing at all when the request names the set the customer already has —
// including the empty-to-empty case, which an implementation comparing only
// non-empty sets gets wrong.
func TestPutCustomersByIdTags_RecordsTheEventOnlyWhenTheSetChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	prospect := createTag(t, c, map[string]any{"name": "Prospect"})
	customer := createCustomer(t, c, "Event Tags Co")

	if r := putCustomerTags(t, c, customer.Id, []string{}); r.Status != http.StatusOK {
		t.Fatalf("clear an untagged customer: status %d body %s, want 200", r.Status, r.Body)
	}
	// The absence of an event is the observable half of the no-op rule. The
	// other half — that the handler also skipped its actor lookup — is not
	// observable here: Deps.Users is identity's real directory, not a fake with
	// a counter, so nothing in the harness can count a call to it. The event
	// count is what this test can assert, and the handler's own comment carries
	// the rest.
	if n := countTimelineEvents(t, h, customer.Id, "customer.tags_changed"); n != 0 {
		t.Fatalf("events = %d after replacing an empty set with an empty set, want 0", n)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{prospect.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag: status %d body %s, want 200", r.Status, r.Body)
	}
	// The harness clock is frozen; advancing it means a second event, if one
	// were wrongly written, would be distinguishable rather than landing on the
	// same instant as the first.
	h.Advance(time.Second)
	if r := putCustomerTags(t, c, customer.Id, []string{prospect.Id}); r.Status != http.StatusOK {
		t.Fatalf("re-send the same set: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.tags_changed"); n != 1 {
		t.Fatalf("events = %d, want 1 (the unchanged re-send records nothing)", n)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("swap: status %d body %s, want 200", r.Status, r.Body)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.tags_changed")
	added, _ := event.Payload["added"].([]any)
	removed, _ := event.Payload["removed"].([]any)
	if len(added) != 1 || len(removed) != 1 {
		t.Fatalf("added/removed = %v/%v, want one each", added, removed)
	}
	if first, _ := added[0].(map[string]any); first["name"] != "VIP" || first["tagId"] != vip.Id {
		t.Errorf("added[0] = %v, want VIP / %s", added[0], vip.Id)
	}
	if first, _ := removed[0].(map[string]any); first["name"] != "Prospect" || first["tagId"] != prospect.Id {
		t.Errorf("removed[0] = %v, want Prospect / %s", removed[0], prospect.Id)
	}
	if event.Summary != "Customer tags changed: added VIP; removed Prospect" {
		t.Errorf("summary = %q, want both halves named", event.Summary)
	}

	gotActor := modtest.One[string](t, h, `
		SELECT actor_user_id::text FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.tags_changed' ORDER BY id DESC LIMIT 1`, customer.Id)
	if gotActor != callerID.String() {
		t.Errorf("actor_user_id = %s, want %s (the signed-in caller)", gotActor, callerID)
	}
}

// TestGetCustomers_TagIdFilter pins design D2's single-tag filter on the list
// and the count together, plus the refusal of a value that is not a tag id.
func TestGetCustomers_TagIdFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	prospect := createTag(t, c, map[string]any{"name": "Prospect"})

	tagged := createCustomer(t, c, "Tagfilter Yes Co")
	both := createCustomer(t, c, "Tagfilter Both Co")
	createCustomer(t, c, "Tagfilter No Co")
	if r := putCustomerTags(t, c, tagged.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag one: status %d body %s", r.Status, r.Body)
	}
	if r := putCustomerTags(t, c, both.Id, []string{vip.Id, prospect.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag both: status %d body %s", r.Status, r.Body)
	}

	list := getList(t, c, "tagId="+vip.Id+"&search="+url.QueryEscape("Tagfilter "))
	if len(list.Data) != 2 || list.Pagination.TotalCount != 2 {
		t.Errorf("tagId=VIP = %d rows / totalCount %d, want 2 and 2", len(list.Data), list.Pagination.TotalCount)
	}
	list = getList(t, c, "tagId="+prospect.Id+"&search="+url.QueryEscape("Tagfilter "))
	// Fatal rather than an error, so the assertion below can index this row
	// unconditionally: guarding it with "if the page is what I expected" is how an
	// assertion quietly stops being one the day the page changes.
	if len(list.Data) != 1 || list.Data[0].Name != "Tagfilter Both Co" {
		t.Fatalf("tagId=Prospect = %+v, want only Tagfilter Both Co", list.Data)
	}
	// The tags ride on the list row itself, so one request answers both "which
	// customers" and "what else are they tagged with".
	if len(list.Data[0].Tags) != 2 {
		t.Errorf("list row tags = %+v, want both tags on the row", list.Data[0].Tags)
	}

	r := c.Do(http.MethodGet, "/api/v1/customers?tagId=notauuid", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("tagId=notauuid: status %d body %s, want 400", r.Status, r.Body)
	}
	if want := "'tagId' must be a tag id, but was 'notauuid'."; !strings.Contains(string(r.Body), want) {
		t.Errorf("body = %s, want it to contain %q", r.Body, want)
	}
}

// TestTagPermissions pins design D4: reading the vocabulary needs
// customers:view, everything that writes needs customers:update, and there is
// no new permission key for either.
func TestTagPermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	writer := authenticatedClient(t, h)
	tag := createTag(t, writer, map[string]any{"name": "VIP"})
	customer := createCustomer(t, writer, "Permission Tags Co")

	viewer := h.SignIn(t, "customers:view")
	if got := listTags(t, viewer); len(got) != 1 {
		t.Errorf("a view-only caller listed %d tags, want 1", len(got))
	}
	// body is `any` rather than map[string]any so the DELETE case can leave it
	// unset: a nil map still travels as a JSON body ("null"), and the contract
	// recorder rejects a body on an operation that declares none.
	for _, call := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/customers/tags", body: map[string]any{"name": "Nope"}},
		{name: "rename", method: http.MethodPut, path: "/api/v1/customers/tags/" + tag.Id, body: map[string]any{"name": "Nope"}},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/customers/tags/" + tag.Id},
		{name: "set replace", method: http.MethodPut, path: fmt.Sprintf("/api/v1/customers/%d/tags", customer.Id), body: map[string]any{"tagIds": []string{tag.Id}}},
	} {
		if r := viewer.Do(call.method, call.path, call.body); r.Status != http.StatusForbidden {
			t.Errorf("%s as a view-only caller: status %d body %s, want 403", call.name, r.Status, r.Body)
		}
	}
	// And the customer's own tags reach a view-only caller, ungated.
	if r := putCustomerTags(t, writer, customer.Id, []string{tag.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag: status %d body %s", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, viewer, customer.Id); len(got.Tags) != 1 || got.Tags[0].Name != "VIP" {
		t.Errorf("a view-only caller saw tags = %+v, want [VIP]", got.Tags)
	}
}
