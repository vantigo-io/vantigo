package customers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is POST /customers/{id}/merge (customers merge design D2, D3): the
// refusal ladder, everything that moves and everything that stays, the two
// events, and the other modules' holders — faked here, because depguard keeps
// projects, energy and communications out of this package even in a test, and
// each of them proves its own SQL in its own package. merge_concurrency_test.go
// carries the lock-forced races.

// fakeReferenceHolder stands in for another module's
// contracts.CustomerReferenceHolder. It records every call, answers the kinds
// it was given or fails, and — through during — can look into the merge's own
// transaction while it is open.
type fakeReferenceHolder struct {
	answer []contracts.RepointedReferences
	err    error
	during func(ctx context.Context, tx pgx.Tx, from, into int32) error

	mu    sync.Mutex
	calls [][2]int32
}

func (f *fakeReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	f.mu.Lock()
	f.calls = append(f.calls, [2]int32{from, into})
	f.mu.Unlock()
	if f.during != nil {
		if err := f.during(ctx, tx, from, into); err != nil {
			return nil, err
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.answer, nil
}

func (f *fakeReferenceHolder) callsSoFar() [][2]int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type mergeMoveJSON struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

type mergeResultJSON struct {
	Customer customerJSON    `json:"customer"`
	Moved    []mergeMoveJSON `json:"moved"`
}

type mergeConflictJSON struct {
	Title  string  `json:"title"`
	Code   *string `json:"code"`
	Detail string  `json:"detail"`
}

// mergedEventPayloadJSON decodes customer.merged's payload (design D3).
type mergedEventPayloadJSON struct {
	CustomerId int32 `json:"customerId"`
	Absorbed   struct {
		Id             int32  `json:"id"`
		CustomerNumber int64  `json:"customerNumber"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		Status         string `json:"status"`
		Identity       *struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		} `json:"identity"`
		ContactInfo    contactInfoJSON    `json:"contactInfo"`
		BillingProfile billingProfileJSON `json:"billingProfile"`
		OwnerUserId    *string            `json:"ownerUserId"`
		GroupId        *string            `json:"groupId"`
	} `json:"absorbed"`
	Moved []mergeMoveJSON `json:"moved"`
}

// mergedAwayPayloadJSON decodes customer.merged_away's payload.
type mergedAwayPayloadJSON struct {
	CustomerId int32                        `json:"customerId"`
	Into       contactCustomerReferenceJSON `json:"into"`
}

// mergeClient signs in a caller who may merge and do everything the fixtures
// need: every customer key but lookup, customers:merge included.
func mergeClient(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "customers:view", "customers:create", "customers:update", "customers:delete", "customers:merge",
		"customers:legal-identity-view", "customers:legal-identity-manage",
		"customers:contacts-view", "customers:contacts-manage",
		"customers:associations-view", "customers:associations-manage",
		"customers:timeline-view", "customers:timeline-manage", "customers:billing-manage")
}

func postMerge(t *testing.T, c *modtest.Client, into int32, body map[string]any, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/merge", into), body, opts...)
}

// mergeOK merges source into into and fails the test on anything but 200.
func mergeOK(t *testing.T, c *modtest.Client, into, source int32) mergeResultJSON {
	t.Helper()
	r := postMerge(t, c, into, map[string]any{"sourceId": source})
	if r.Status != http.StatusOK {
		t.Fatalf("merge %d into %d: status %d body %s, want 200", source, into, r.Status, r.Body)
	}
	var result mergeResultJSON
	r.JSON(&result)
	return result
}

// refusedWith asserts a 409 carrying code and answers its body.
func refusedWith(t *testing.T, r *modtest.Response, code string) mergeConflictJSON {
	t.Helper()
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409 %s", r.Status, r.Body, code)
	}
	var problem mergeConflictJSON
	r.JSON(&problem)
	if problem.Code == nil || *problem.Code != code {
		t.Errorf("code = %q, want %q (body %s)", str(problem.Code), code, r.Body)
	}
	return problem
}

func movedCount(result mergeResultJSON, kind string) int64 {
	for _, m := range result.Moved {
		if m.Kind == kind {
			return m.Count
		}
	}
	return -1
}

// timelineOf reads a customer's first timeline page, failing on anything but 200.
func timelineOf(t *testing.T, c *modtest.Client, customerID int32) []timelineEntryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline", customerID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("timeline of %d: status %d body %s, want 200", customerID, r.Status, r.Body)
	}
	var list timelineListJSON
	r.JSON(&list)
	return list.Data
}

func entriesOfType(entries []timelineEntryJSON, eventType string) []timelineEntryJSON {
	var out []timelineEntryJSON
	for _, e := range entries {
		if e.EventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

func tagNamesOf(tags []tagJSON) []string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return names
}

// insertRegistryAndPeppol gives a customer a registry record and a stored
// Peppol answer directly: the merge's subject is which of them survive, not
// how a refresh or a lookup writes them.
func insertRegistryAndPeppol(t *testing.T, h *modtest.Harness, customerID int32) {
	t.Helper()
	h.Exec(t, `INSERT INTO customers.customer_registry_records
	               (customer_id, organisation_number, name, vat_registered, bankrupt, under_liquidation, under_forced_liquidation, fetched_at)
	           VALUES ($1, '923609016', 'ACME', true, false, false, false, $2)`, customerID, h.Now())
	h.Exec(t, `INSERT INTO customers.customer_peppol_lookups
	               (customer_id, participant_id, status, can_receive_invoice, can_receive_credit_note, checked_at)
	           VALUES ($1, '0192:923609016', 'registered', true, true, $2)`, customerID, h.Now())
}

// TestPostCustomersByIdMerge_RefusesInTheDesignsOrder walks design D2's ladder
// on one installation: 404 for either customer, then merge_self,
// merge_type_mismatch, merge_into_archived, merge_already_merged, then the
// survivor's stale revision — and the order is pinned where two refusals
// apply at once. A refused merge calls no holder: the one holder call is the
// merge that went through.
func TestPostCustomersByIdMerge_RefusesInTheDesignsOrder(t *testing.T) {
	t.Parallel()
	holder := &fakeReferenceHolder{}
	h := newHarness(t, modtest.WithCustomerReferenceHolders(holder))
	c := mergeClient(t, h)
	acme := createCustomer(t, c, "Acme AS")
	duplicate := createCustomer(t, c, "Acme Norge AS")
	person := createCustomerOfType(t, c, "Kari Nordmann", "person")
	archived := createCustomer(t, c, "Gamle Acme AS")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", archived.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	elsewhere := createCustomer(t, c, "Acme Holding AS")

	if r := postMerge(t, c, 999999, map[string]any{"sourceId": duplicate.Id}); r.Status != http.StatusNotFound {
		t.Errorf("unknown survivor: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := postMerge(t, c, acme.Id, map[string]any{"sourceId": 999999}); r.Status != http.StatusNotFound {
		t.Errorf("unknown source: status %d body %s, want 404", r.Status, r.Body)
	}
	r := postMerge(t, c, acme.Id, map[string]any{"sourceId": 0})
	var invalid validationProblemJSON
	if r.Status != http.StatusBadRequest {
		t.Fatalf("sourceId 0: status %d body %s, want 400", r.Status, r.Body)
	}
	if r.JSON(&invalid); len(invalid.Errors["sourceId"]) != 1 {
		t.Errorf("sourceId 0: errors = %v, want one for sourceId", invalid.Errors)
	}

	refusedWith(t, postMerge(t, c, acme.Id, map[string]any{"sourceId": acme.Id}), "merge_self")
	mismatch := refusedWith(t, postMerge(t, c, acme.Id, map[string]any{"sourceId": person.Id}), "merge_type_mismatch")
	if !strings.Contains(mismatch.Detail, "Kari Nordmann is a private person") {
		t.Errorf("type mismatch detail = %q, want it to say what the source is", mismatch.Detail)
	}
	refusedWith(t, postMerge(t, c, archived.Id, map[string]any{"sourceId": duplicate.Id}), "merge_into_archived")
	// Both apply: the type check comes first.
	refusedWith(t, postMerge(t, c, archived.Id, map[string]any{"sourceId": person.Id}), "merge_type_mismatch")

	stale := postMerge(t, c, acme.Id, map[string]any{"sourceId": duplicate.Id, "revision": 99})
	var conflict mergeConflictJSON
	if stale.Status != http.StatusConflict {
		t.Fatalf("stale revision: status %d body %s, want 409", stale.Status, stale.Body)
	}
	if stale.JSON(&conflict); conflict.Title != "Customer revision conflict" || conflict.Code != nil {
		t.Errorf("stale revision: body %s, want the revision conflict without a code", stale.Body)
	}
	if calls := holder.callsSoFar(); len(calls) != 0 {
		t.Errorf("refused merges called the holder %v", calls)
	}

	mergeOK(t, c, acme.Id, duplicate.Id)
	already := refusedWith(t, postMerge(t, c, elsewhere.Id, map[string]any{"sourceId": duplicate.Id}), "merge_already_merged")
	if want := fmt.Sprintf("#%d Acme AS", acme.CustomerNumber); !strings.Contains(already.Detail, want) {
		t.Errorf("already-merged detail = %q, want it to name %s", already.Detail, want)
	}
	if calls := holder.callsSoFar(); !slices.Equal(calls, [][2]int32{{duplicate.Id, acme.Id}}) {
		t.Errorf("holder calls = %v, want the one merge that went through", calls)
	}
}

// The operation wants customers:merge and customers:view together (design D2):
// delete and update, which archive and edit a customer, are not enough, and
// merge alone cannot read what it answers.
func TestPostCustomersByIdMerge_WantsMergeAndView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := mergeClient(t, h)
	survivor := createCustomer(t, admin, "Acme AS")
	absorbed := createCustomer(t, admin, "Acme Norge AS")

	for _, keys := range [][]string{{"customers:view", "customers:update", "customers:delete"}, {"customers:merge"}} {
		r := postMerge(t, h.SignIn(t, keys...), survivor.Id, map[string]any{"sourceId": absorbed.Id})
		if r.Status != http.StatusForbidden || r.Code() != "forbidden" {
			t.Errorf("%v: status %d code %q, want 403 forbidden", keys, r.Status, r.Code())
		}
	}
	if r := postMerge(t, h.SignIn(t, "customers:merge", "customers:view"), survivor.Id, map[string]any{"sourceId": absorbed.Id}); r.Status != http.StatusOK {
		t.Errorf("merge + view: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestPostCustomersByIdMerge_MovesContactsWithTheirRolesAndPrimaries is D3's
// contact rule: every association moves; a contact linked to both keeps the
// survivor's association and title; roles are unioned; the survivor's primary
// stays, the absorbed customer's primary becomes the survivor's for a role it
// had nobody in, and every other primary flag goes — so every role ends with
// exactly one primary.
func TestPostCustomersByIdMerge_MovesContactsWithTheirRolesAndPrimaries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	billing := createContact(t, c, map[string]any{"firstName": "Bea", "lastName": "Billing"}).Id
	shared := createContact(t, c, map[string]any{"firstName": "Sam", "lastName": "Shared"}).Id
	deciding := createContact(t, c, map[string]any{"firstName": "Dag", "lastName": "Decider"}).Id
	second := createContact(t, c, map[string]any{"firstName": "Siri", "lastName": "Second"}).Id

	// The survivor: Bea its primary billing contact, Sam its project contact, titled CEO.
	attachWithRoles(t, c, survivor, map[string]any{"contactId": billing, "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, survivor, map[string]any{"contactId": shared, "title": "CEO", "roles": []any{map[string]any{"role": "project"}}})
	// The duplicate: Sam again, as ITS primary billing contact under another
	// title; Dag its only decision maker; Siri a second billing holder.
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": shared, "title": "Daglig leder", "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": deciding, "roles": []any{map[string]any{"role": "decision_maker"}}})
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": second, "roles": []any{map[string]any{"role": "billing"}}})

	result := mergeOK(t, c, survivor, absorbed)

	if got := movedCount(result, "customers.contacts"); got != 3 {
		t.Errorf("customers.contacts = %d, want the duplicate's 3 associations, Sam's included", got)
	}
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", survivor), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("survivor's contacts: status %d body %s", r.Status, r.Body)
	}
	var list roledContactListJSON
	r.JSON(&list)
	got := map[int32]roledContactJSON{}
	for _, a := range list.Data {
		got[a.Contact.Id] = a
	}
	want := map[int32][]contactRoleJSON{
		billing:  {{Role: "billing", Primary: true}},
		shared:   {{Role: "billing", Primary: false}, {Role: "project", Primary: true}},
		deciding: {{Role: "decision_maker", Primary: true}},
		second:   {{Role: "billing", Primary: false}},
	}
	if len(got) != len(want) {
		t.Fatalf("survivor has %d contacts, want %d: %+v", len(got), len(want), list.Data)
	}
	for id, roles := range want {
		if !slices.Equal(got[id].Roles, roles) {
			t.Errorf("contact %d roles = %+v, want %+v", id, got[id].Roles, roles)
		}
	}
	if str(got[shared].Title) != "CEO" {
		t.Errorf("the shared contact's title = %q, want the survivor's CEO", str(got[shared].Title))
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE customer_id = $1`, absorbed); n != 0 {
		t.Errorf("the duplicate still has %d associations", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1`, absorbed); n != 0 {
		t.Errorf("the duplicate still has %d role rows", n)
	}
}

// TestPostCustomersByIdMerge_MovesTheRestAndKeepsTheSurvivorsOwnRow is the rest
// of D3's table in one merge: addresses (primary-per-type demoted, labels
// kept), the timeline (entries, revisions and follow-ups, payloads untouched),
// tags unioned, the duplicate's registry record and Peppol answer gone, the
// survivor's own row untouched, the duplicate archived with the marker and
// still readable, both revisions bumped, the directory's MergedInto, and the
// two events with no status change beside them.
func TestPostCustomersByIdMerge_MovesTheRestAndKeepsTheSurvivorsOwnRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	ctx := context.Background()
	survivor := createCustomer(t, c, "Acme AS")
	absorbed := createCustomer(t, c, "Acme Norge AS")
	setLegalIdentity(t, h, survivor.Id, "no", "923609016", "ACME AS")
	setLegalIdentity(t, h, absorbed.Id, "no", "923609016", "ACME NORGE AS")
	for id, body := range map[int32]map[string]any{
		survivor.Id: {"email": "post@acme.no"},
		absorbed.Id: {"email": "post@acmenorge.no", "phone": "+47 22 33 44 55"},
	} {
		if r := putContactInfo(t, c, id, body); r.Status != http.StatusOK {
			t.Fatalf("contact info of %d: status %d body %s", id, r.Status, r.Body)
		}
	}
	if r := putBillingProfile(t, c, absorbed.Id, map[string]any{"currency": "EUR", "paymentTermsDays": 30}); r.Status != http.StatusOK {
		t.Fatalf("billing profile: status %d body %s", r.Status, r.Body)
	}
	keptInvoice := createAddress(t, c, survivor.Id, fullAddressBody("invoice", nil))
	oldInvoiceBody := fullAddressBody("invoice", nil)
	oldInvoiceBody["label"] = "Gammelt hovedkontor"
	demotedInvoice := createAddress(t, c, absorbed.Id, oldInvoiceBody)
	movedPostal := createAddress(t, c, absorbed.Id, fullAddressBody("postal", nil))
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	nordic := createTag(t, c, map[string]any{"name": "Nordic"})
	for id, tags := range map[int32][]string{survivor.Id: {vip.Id}, absorbed.Id: {vip.Id, nordic.Id}} {
		if r := putCustomerTags(t, c, id, tags); r.Status != http.StatusOK {
			t.Fatalf("tags of %d: status %d body %s", id, r.Status, r.Body)
		}
	}
	entry := createWithFollowUp(t, c, absorbed.Id, day(h, 0), "Ring dem", map[string]any{"dueOn": day(h, 7)})
	putEntryWithFollowUp(t, c, absorbed.Id, entry.Id, entry.CurrentRevision, map[string]any{"dueOn": day(h, 14)})
	insertRegistryAndPeppol(t, h, survivor.Id)
	insertRegistryAndPeppol(t, h, absorbed.Id)
	survivorBefore := fetchCustomerJSON(t, c, survivor.Id)
	absorbedBefore := fetchCustomerJSON(t, c, absorbed.Id)

	result := mergeOK(t, c, survivor.Id, absorbed.Id)

	// The survivor's own row: every field its own, one revision on.
	after := result.Customer
	if after.Name != "Acme AS" || str(after.ContactInfo.Email) != "post@acme.no" || after.ContactInfo.Phone != nil {
		t.Errorf("survivor name/contact info = %q/%+v, want its own, untouched", after.Name, after.ContactInfo)
	}
	if after.Identity == nil || after.Identity.Id != "923609016" {
		t.Errorf("survivor identity = %+v, want its own", after.Identity)
	}
	if after.Revision != survivorBefore.Revision+1 || after.MergedInto != nil {
		t.Errorf("survivor revision %d (was %d), mergedInto %+v; want one on and no marker", after.Revision, survivorBefore.Revision, after.MergedInto)
	}
	if names := tagNamesOf(after.Tags); !slices.Equal(names, []string{"Nordic", "VIP"}) {
		t.Errorf("survivor tags = %v, want the union [Nordic VIP]", names)
	}
	if profile := fetchBillingProfile(t, c, survivor.Id); profile.Currency != nil || profile.PaymentTermsDays != nil {
		t.Errorf("survivor billing profile = %+v, want its own (empty), nothing filled in", profile)
	}

	// Addresses: the survivor's invoice primary stays, the duplicate's is
	// demoted with its label, and the duplicate's postal primary is the
	// survivor's now — it had none.
	addresses := map[int32]addressJSON{}
	for _, a := range listAddresses(t, c, survivor.Id).Data {
		addresses[a.Id] = a
	}
	if len(addresses) != 3 || !addresses[keptInvoice.Id].IsPrimary || addresses[demotedInvoice.Id].IsPrimary ||
		str(addresses[demotedInvoice.Id].Label) != "Gammelt hovedkontor" || !addresses[movedPostal.Id].IsPrimary {
		t.Errorf("survivor addresses = %+v", addresses)
	}
	if n := len(listAddresses(t, c, absorbed.Id).Data); n != 0 {
		t.Errorf("the duplicate still has %d addresses", n)
	}

	// The timeline: the entry and both its revisions under the survivor, its
	// follow-up with it, and the payloads still naming who they happened to.
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/revisions", survivor.Id, entry.Id), nil)
	var revisions timelineRevisionListJSON
	if r.Status != http.StatusOK {
		t.Fatalf("revisions under the survivor: status %d body %s", r.Status, r.Body)
	}
	if r.JSON(&revisions); len(revisions.Data) != 2 {
		t.Errorf("revisions = %d, want 2", len(revisions.Data))
	}
	if r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", absorbed.Id, entry.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("the entry under the duplicate: status %d, want 404", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions r
	                     JOIN customers.customers_timeline_entries e ON e.id = r.customer_timeline_entry_id
	                     WHERE r.customer_id <> e.customer_id`); n != 0 {
		t.Errorf("%d revisions disagree with their entry's customer", n)
	}
	var followUp *followUpRowJSON
	// The follow-up has no assignee, and the list's own default is assignee=me:
	// ask for the unassigned ones, on the survivor.
	for _, row := range listFollowUps(t, c, url.Values{"assignee": {"none"}, "customerId": {strconv.Itoa(int(survivor.Id))}}).Data {
		if row.EntryId == entry.Id {
			followUp = &row
		}
	}
	if followUp == nil || followUp.CustomerId != survivor.Id || followUp.CustomerName != "Acme AS" {
		t.Errorf("the follow-up = %+v, want it on the survivor", followUp)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries
	                     WHERE customer_id = $1 AND (payload_json->>'customerId')::int = $2`, survivor.Id, absorbed.Id); n == 0 {
		t.Error("no moved entry still names the duplicate in its payload: payloads were rewritten")
	}

	// The registry record and the Peppol answer: the survivor's kept, the duplicate's gone.
	for _, table := range []string{"customer_registry_records", "customer_peppol_lookups"} {
		query := fmt.Sprintf(`SELECT count(*) FROM customers.%s WHERE customer_id = $1`, table)
		if h.Count(t, query, survivor.Id) != 1 || h.Count(t, query, absorbed.Id) != 0 {
			t.Errorf("%s: want the survivor's row kept and the duplicate's deleted", table)
		}
	}

	// The duplicate: archived, marked, one revision on, its own details still there.
	gone := fetchCustomerJSON(t, c, absorbed.Id)
	if gone.Status != "archived" || gone.MergedInto == nil || gone.MergedInto.Id != survivor.Id ||
		gone.MergedInto.CustomerNumber != survivor.CustomerNumber || gone.MergedInto.Name != "Acme AS" {
		t.Errorf("the duplicate = status %q mergedInto %+v, want archived and pointing at the survivor", gone.Status, gone.MergedInto)
	}
	if gone.Revision != absorbedBefore.Revision+1 || gone.Name != "Acme Norge AS" || str(gone.ContactInfo.Email) != "post@acmenorge.no" {
		t.Errorf("the duplicate = revision %d (was %d) name %q email %q", gone.Revision, absorbedBefore.Revision, gone.Name, str(gone.ContactInfo.Email))
	}
	if e, err := newDirectory(t, h).Customer(ctx, absorbed.Id); err != nil || e == nil || !e.Archived || e.MergedInto == nil || *e.MergedInto != survivor.Id {
		t.Errorf("directory Customer(duplicate) = %+v, %v; want archived with MergedInto the survivor", e, err)
	}
	if e, err := newDirectory(t, h).Customer(ctx, survivor.Id); err != nil || e == nil || e.MergedInto != nil {
		t.Errorf("directory Customer(survivor) = %+v, %v; want no marker", e, err)
	}

	// The events: customer.merged on the survivor, merged_away on the
	// duplicate, and no status change beside them.
	merged := entriesOfType(timelineOf(t, c, survivor.Id), "customer.merged")
	if len(merged) != 1 {
		t.Fatalf("customer.merged entries = %d, want 1", len(merged))
	}
	if want := fmt.Sprintf("Absorbed #%d Acme Norge AS: ", absorbed.CustomerNumber); !strings.HasPrefix(str(merged[0].Summary), want) {
		t.Errorf("summary = %q, want it to start %q", str(merged[0].Summary), want)
	}
	var payload mergedEventPayloadJSON
	if err := json.Unmarshal(merged[0].Payload, &payload); err != nil {
		t.Fatalf("decode customer.merged payload %s: %v", merged[0].Payload, err)
	}
	a := payload.Absorbed
	if payload.CustomerId != survivor.Id || a.Id != absorbed.Id || a.CustomerNumber != absorbed.CustomerNumber ||
		a.Name != "Acme Norge AS" || a.Type != "business" || a.Status != "active" ||
		a.Identity == nil || a.Identity.Name != "ACME NORGE AS" || str(a.ContactInfo.Phone) != "+47 22 33 44 55" ||
		str(a.BillingProfile.Currency) != "EUR" || a.BillingProfile.PaymentTermsDays == nil || *a.BillingProfile.PaymentTermsDays != 30 ||
		a.OwnerUserId != nil || a.GroupId != nil {
		t.Errorf("customer.merged payload = %s", merged[0].Payload)
	}
	if !slices.Equal(payload.Moved, result.Moved) {
		t.Errorf("payload moved = %+v, want the answer's %+v", payload.Moved, result.Moved)
	}
	absorbedTimeline := timelineOf(t, c, absorbed.Id)
	away := entriesOfType(absorbedTimeline, "customer.merged_away")
	if len(away) != 1 || str(away[0].Summary) != fmt.Sprintf("Merged into #%d Acme AS", survivor.CustomerNumber) {
		t.Fatalf("customer.merged_away = %+v, want one naming the survivor", away)
	}
	var awayPayload mergedAwayPayloadJSON
	if err := json.Unmarshal(away[0].Payload, &awayPayload); err != nil || awayPayload.CustomerId != absorbed.Id || awayPayload.Into.Id != survivor.Id {
		t.Errorf("customer.merged_away payload = %s (%v)", away[0].Payload, err)
	}
	if n := len(entriesOfType(absorbedTimeline, "customer.status_changed")); n != 0 {
		t.Errorf("the duplicate has %d status_changed entries, want none beside merged_away", n)
	}
}

// TestPostCustomersByIdMerge_CallsEveryHolderInsideItsTransaction is D1: each
// holder is called once, in Compose order, with (absorbed, survivor) and the
// merge's own transaction — in which this module's moves are already visible,
// uncommitted — and what each reports is in the answer after this module's
// four kinds, and in the summary.
func TestPostCustomersByIdMerge_CallsEveryHolderInsideItsTransaction(t *testing.T) {
	t.Parallel()
	var survivorAddressesSeen, absorbedAddressesSeen, survivorAddressesCommitted int
	var h *modtest.Harness
	first := &fakeReferenceHolder{
		answer: []contracts.RepointedReferences{{Kind: "projects.projects", Count: 2}},
		during: func(ctx context.Context, tx pgx.Tx, from, into int32) error {
			const q = `SELECT count(*) FROM customers.customer_addresses WHERE customer_id = $1`
			if err := tx.QueryRow(ctx, q, into).Scan(&survivorAddressesSeen); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, q, from).Scan(&absorbedAddressesSeen); err != nil {
				return err
			}
			// And outside it, on the pool, the move is not there yet: this is
			// the merge's own transaction, not one after it committed.
			return h.Pool().QueryRow(ctx, q, into).Scan(&survivorAddressesCommitted)
		},
	}
	second := &fakeReferenceHolder{answer: []contracts.RepointedReferences{
		{Kind: "energy.supplyPeriods", Count: 0}, {Kind: "somewhere.else", Count: 1},
	}}
	h = newHarness(t, modtest.WithCustomerReferenceHolders(first, second))
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS")
	absorbed := createCustomer(t, c, "Acme Norge AS")
	createAddress(t, c, absorbed.Id, fullAddressBody("postal", nil))

	result := mergeOK(t, c, survivor.Id, absorbed.Id)

	for name, holder := range map[string]*fakeReferenceHolder{"first": first, "second": second} {
		if calls := holder.callsSoFar(); !slices.Equal(calls, [][2]int32{{absorbed.Id, survivor.Id}}) {
			t.Errorf("%s holder calls = %v, want one (absorbed, survivor)", name, calls)
		}
	}
	if survivorAddressesSeen != 1 || absorbedAddressesSeen != 0 {
		t.Errorf("inside the transaction the holder saw %d/%d addresses on survivor/duplicate, want the move made: 1/0",
			survivorAddressesSeen, absorbedAddressesSeen)
	}
	if survivorAddressesCommitted != 0 {
		t.Errorf("outside the transaction the survivor already had %d addresses while the holder ran, want 0: the holder was not inside the merge's transaction",
			survivorAddressesCommitted)
	}
	want := []mergeMoveJSON{
		{"customers.contacts", 0}, {"customers.addresses", 1}, {"customers.timelineEntries", 2}, {"customers.tags", 0},
		{"projects.projects", 2}, {"energy.supplyPeriods", 0}, {"somewhere.else", 1},
	}
	if !slices.Equal(result.Moved, want) {
		t.Errorf("moved = %+v, want %+v", result.Moved, want)
	}
	merged := entriesOfType(timelineOf(t, c, survivor.Id), "customer.merged")
	wantSummary := fmt.Sprintf("Absorbed #%d Acme Norge AS: 1 address, 2 timeline entries, 2 projects, 1 × somewhere.else", absorbed.CustomerNumber)
	if len(merged) != 1 || str(merged[0].Summary) != wantSummary {
		t.Errorf("customer.merged = %+v, want summary %q", merged, wantSummary)
	}
}

// A holder's error rolls everything back (design D1): nothing moved, no
// marker, no revision bump, no event — the merge is one transaction, and the
// holder's half cannot commit without this module's or the reverse. The error
// is not a deadlock, so it is not retried either.
func TestPostCustomersByIdMerge_AHolderErrorRollsEverythingBack(t *testing.T) {
	t.Parallel()
	failing := &fakeReferenceHolder{err: errors.New("the other module is down")}
	h := newHarness(t, modtest.WithCustomerReferenceHolders(failing))
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	contact := createContact(t, c, map[string]any{"firstName": "Bea", "lastName": "Billing"}).Id
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": contact, "roles": []any{map[string]any{"role": "billing"}}})
	createAddress(t, c, absorbed, fullAddressBody("postal", nil))
	tag := createTag(t, c, map[string]any{"name": "VIP"})
	if r := putCustomerTags(t, c, absorbed, []string{tag.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	const revisions = `SELECT revision FROM customers.customers WHERE id = $1`
	survivorRevision, absorbedRevision := modtest.One[int32](t, h, revisions, survivor), modtest.One[int32](t, h, revisions, absorbed)
	entries := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, absorbed)

	r := postMerge(t, c, survivor, map[string]any{"sourceId": absorbed},
		modtest.SkipContract("a holder's failure is an infrastructure 500, deliberately off-contract"))

	if r.Status != http.StatusInternalServerError {
		t.Fatalf("status %d body %s, want 500", r.Status, r.Body)
	}
	if calls := failing.callsSoFar(); len(calls) != 1 {
		t.Errorf("holder calls = %v, want exactly one: a plain error is not retried", calls)
	}
	for table, want := range map[string]int{"customers_contacts": 1, "customer_contact_roles": 1, "customer_addresses": 1, "customer_tags": 1} {
		if got := h.Count(t, fmt.Sprintf(`SELECT count(*) FROM customers.%s WHERE customer_id = $1`, table), absorbed); got != want {
			t.Errorf("%s rows on the duplicate = %d, want %d: the move was not rolled back", table, got, want)
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, absorbed); got != entries {
		t.Errorf("the duplicate's timeline entries = %d, want %d", got, entries)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND status = 'active' AND merged_into_customer_id IS NULL`, absorbed); n != 1 {
		t.Error("the duplicate was archived or marked although the merge failed")
	}
	if modtest.One[int32](t, h, revisions, survivor) != survivorRevision || modtest.One[int32](t, h, revisions, absorbed) != absorbedRevision {
		t.Error("a revision moved although the merge failed")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE event_type IN ('customer.merged', 'customer.merged_away')`); n != 0 {
		t.Errorf("%d merge events were written by a failed merge", n)
	}
}

// An archived customer may be absorbed (design D2) — the usual case, the
// duplicate was archived when noticed. It stays archived, gains the marker,
// and its timeline gains merged_away but no second status change.
func TestPostCustomersByIdMerge_AbsorbsAnArchivedDuplicate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", absorbed), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}

	mergeOK(t, c, survivor, absorbed)

	gone := fetchCustomerJSON(t, c, absorbed)
	if gone.Status != "archived" || gone.MergedInto == nil || gone.MergedInto.Id != survivor {
		t.Errorf("the duplicate = %q %+v, want archived and marked", gone.Status, gone.MergedInto)
	}
	timeline := timelineOf(t, c, absorbed)
	if n := len(entriesOfType(timeline, "customer.status_changed")); n != 0 {
		t.Errorf("the duplicate's own status_changed stayed behind %d times; it moves with the rest of its timeline", n)
	}
	if n := len(entriesOfType(timeline, "customer.merged_away")); n != 1 {
		t.Errorf("merged_away entries = %d, want 1", n)
	}
}
