package planning_test

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// termsPath is the terms of allocation id.
func termsPath(id int64) string { return allocationPath(id) + "/terms" }

// putTerms sets id's terms to body, with If-Match only when opts carry it.
func putTerms(c *modtest.Client, id int64, body map[string]any, opts ...modtest.RequestOption) *modtest.Response {
	return c.Do(http.MethodPut, termsPath(id), body, opts...)
}

// termsSet decodes a 200 terms PUT, failing otherwise.
func termsSet(t *testing.T, res *modtest.Response) gen.PlanningAllocation {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("PUT terms = %d %s, want 200", res.Status, res.Body)
	}
	var out gen.PlanningAllocation
	res.JSON(&out)
	return out
}

// getAllocation reads id as c, failing unless it is 200.
func getAllocation(t *testing.T, c *modtest.Client, id int64) gen.PlanningAllocation {
	t.Helper()
	var out gen.PlanningAllocation
	getJSON(t, c, allocationPath(id), http.StatusOK, &out)
	return out
}

// seedTerms is one terms row a test plans against, written straight into
// planning.allocation_terms.
type seedTerm struct {
	minimumUntil    string
	handoverDays    *int16
	approval        bool
	nonCompeteUntil string
	customers       []int32
}

// seedTerms writes r as allocation id's terms.
func seedTerms(t *testing.T, h *harness, id int64, r seedTerm) {
	t.Helper()
	day := func(s string) any {
		if s == "" {
			return nil
		}
		return date(s)
	}
	customers := r.customers
	if customers == nil {
		customers = []int32{}
	}
	h.Exec(t, `INSERT INTO planning.allocation_terms
    (allocation_id, minimum_until, handover_days, replacement_needs_approval, non_compete_until, non_compete_customer_ids, created_at, updated_at)
VALUES ($1, $2::date, $3, $4, $5::date, $6, $7::timestamptz, $7::timestamptz)`,
		id, day(r.minimumUntil), r.handoverDays, r.approval, day(r.nonCompeteUntil), customers, h.Now())
}

// termsEvents is the kinds of the terms events about allocation id, in
// order.
func termsEvents(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return slices.DeleteFunc(eventKinds(t, h, id), func(k string) bool { return !strings.HasPrefix(k, "terms_") })
}

// TestTerms_FieldErrors is S14's field rules (plan reading 3): each fault a
// field error on its own field under "Invalid allocation", nothing written;
// then a whole body accepted, the reference trimmed and the customers
// collapsed, ascending and named.
func TestTerms_FieldErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	ids := make([]int32, 21)
	for i := range ids {
		ids[i] = int32(3001 + i)
	}
	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"a count of 0":                  {map[string]any{"noticeCount": 0, "noticeUnit": "days"}, "noticeCount"},
		"a count of 366":                {map[string]any{"noticeCount": 366, "noticeUnit": "days"}, "noticeCount"},
		"a count without a unit":        {map[string]any{"noticeCount": 3}, "noticeUnit"},
		"a unit without a count":        {map[string]any{"noticeUnit": "weeks"}, "noticeCount"},
		"a unit of years":               {map[string]any{"noticeCount": 3, "noticeUnit": "years"}, "noticeUnit"},
		"notice given with no period":   {map[string]any{"noticeGivenOn": "2026-10-01"}, "noticeGivenOn"},
		"a handover of -1":              {map[string]any{"handoverDays": -1}, "handoverDays"},
		"a handover of 91":              {map[string]any{"handoverDays": 91}, "handoverDays"},
		"customers with no date":        {map[string]any{"nonCompeteCustomerIds": []int32{3001}}, "nonCompeteUntil"},
		"a date with no customer":       {map[string]any{"nonCompeteUntil": "2027-06-30"}, "nonCompeteCustomerIds"},
		"a date with an empty list":     {map[string]any{"nonCompeteUntil": "2027-06-30", "nonCompeteCustomerIds": []int32{}}, "nonCompeteCustomerIds"},
		"21 customers":                  {map[string]any{"nonCompeteUntil": "2027-06-30", "nonCompeteCustomerIds": ids}, "nonCompeteCustomerIds"},
		"an unknown customer":           {map[string]any{"nonCompeteUntil": "2027-06-30", "nonCompeteCustomerIds": []int32{3001, 3999}}, "nonCompeteCustomerIds"},
		"a reference of 201":            {map[string]any{"reference": strings.Repeat("å", 201)}, "reference"},
		"a property the contract lacks": {map[string]any{"noticeCount": 3, "noticeUnit": "days", "noticeDays": 3}, "noticeDays"},
	} {
		p := problemOf(t, putTerms(c, id, tc.body), http.StatusBadRequest)
		if p.Title == nil || *p.Title != "Invalid allocation" || p.Errors == nil || len((*p.Errors)[tc.field]) == 0 {
			t.Errorf("%s = %+v, want a field error on %s", name, p, tc.field)
		}
		if name == "an unknown customer" && !strings.Contains((*p.Errors)[tc.field][0], "3999") {
			t.Errorf("%s = %v, want the id named", name, (*p.Errors)[tc.field])
		}
	}
	if got := getAllocation(t, c, id); got.Terms != nil {
		t.Fatalf("terms after refusals = %+v, want none", got.Terms)
	}

	set := termsSet(t, putTerms(c, id, map[string]any{
		"noticeCount": 3, "noticeUnit": "months", "noticeGivenOn": "2026-10-01", "minimumUntil": "2026-12-31",
		"handoverDays": 0, "nonCompeteUntil": "2027-06-30", "nonCompeteCustomerIds": []int32{3002, 3001, 3002},
		"reference": "  Avtale 17  ",
	}))
	terms := set.Terms
	if terms == nil || terms.Revision != 1 || terms.NoticeCount == nil || *terms.NoticeCount != 3 || terms.NoticeUnit == nil ||
		*terms.NoticeUnit != gen.Months || terms.Reference == nil || *terms.Reference != "Avtale 17" || terms.ReplacementNeedsApproval ||
		terms.HandoverDays == nil || *terms.HandoverDays != 0 {
		t.Fatalf("terms = %+v, want the body's, the reference trimmed, approval false by default", terms)
	}
	if got := terms.NonCompeteCustomers; len(got) != 2 || got[0].Id != customerKraftVerket || got[0].Name == nil ||
		*got[0].Name != "Kraft-Verket AS" || got[1].Id != customerHydro || got[1].Name == nil || *got[1].Name != "Hydro ASA" {
		t.Errorf("customers = %+v, want Kraft-Verket and Hydro once each, named", got)
	}
	// The release is the notice's end, 1 January, past the minimum term;
	// a handover of 0 days starts on the release day itself.
	if set.EarliestRelease.String() != "2027-01-01" || set.HandoverStart == nil || set.HandoverStart.String() != "2027-01-01" {
		t.Errorf("derived = %s, %v; want 2027-01-01 both", set.EarliestRelease, set.HandoverStart)
	}

	// An empty reference clears it; a customer the directory no longer
	// resolves is its id alone (rule 4).
	h.customers.set(3003, "Statnett SF")
	set = termsSet(t, putTerms(c, id, map[string]any{"reference": " ", "nonCompeteUntil": "2027-06-30", "nonCompeteCustomerIds": []int32{3003}}, ifMatch(1)))
	if set.Terms == nil || set.Terms.Reference != nil || set.Terms.NoticeCount != nil || set.Terms.Revision != 2 {
		t.Errorf("terms after a whole replace = %+v, want the left-out fields cleared at revision 2", set.Terms)
	}
	h.customers.mu.Lock()
	delete(h.customers.names, 3003)
	h.customers.mu.Unlock()
	if got := getAllocation(t, c, id).Terms; got == nil || len(got.NonCompeteCustomers) != 1 || got.NonCompeteCustomers[0].Id != 3003 ||
		got.NonCompeteCustomers[0].Name != nil {
		t.Errorf("an unresolved customer = %+v, want the id alone", got)
	}
}

// TestTerms_IfMatch is plan reading 2's four cases and the header's form:
// no header on a row with no terms sets them; a header on such a row, no
// header on a row with terms, and a stale revision are each
// revision_conflict; a header carrying no revision is a 400. An unknown
// allocation is the bare 404; terms may be set on a tentative row (reading
// 8).
func TestTerms_IfMatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	body := map[string]any{"minimumUntil": "2026-11-30"}

	wantCode(t, problemOf(t, putTerms(c, id, body, ifMatch(1)), http.StatusConflict), "revision_conflict")
	if set := termsSet(t, putTerms(c, id, body)); set.Terms == nil || set.Terms.Revision != 1 {
		t.Fatalf("terms = %+v, want revision 1", set.Terms)
	}
	wantCode(t, problemOf(t, putTerms(c, id, body), http.StatusConflict), "revision_conflict")
	wantCode(t, problemOf(t, putTerms(c, id, body, ifMatch(2)), http.StatusConflict), "revision_conflict")
	if set := termsSet(t, putTerms(c, id, map[string]any{"minimumUntil": "2026-12-31"}, modtest.Header("If-Match", `W/"1"`))); set.Terms.Revision != 2 {
		t.Errorf("revision = %d, want 2", set.Terms.Revision)
	}
	p := problemOf(t, putTerms(c, id, body, modtest.Header("If-Match", "abc")), http.StatusBadRequest)
	if p.Errors == nil || len((*p.Errors)["If-Match"]) == 0 {
		t.Errorf("a header with no revision = %+v, want a field error on If-Match", p)
	}
	if res := putTerms(c, 999999, body); res.Status != http.StatusNotFound {
		t.Errorf("PUT terms of an unknown allocation = %d, want 404", res.Status)
	}
	if res := c.Do(http.MethodDelete, termsPath(999999), nil); res.Status != http.StatusNotFound {
		t.Errorf("DELETE terms of an unknown allocation = %d, want 404", res.Status)
	}

	tentative := seedAllocation(t, h, seedRow{person: personOla, project: projectPlanned, from: "2026-10-05", to: "2026-10-30", load: 50, status: "tentative"})
	if set := termsSet(t, putTerms(c, tentative, body)); set.Status != gen.Tentative || set.EarliestRelease.String() != "2026-11-30" {
		t.Errorf("terms on a tentative row = %+v, want set, the release derived", set)
	}
}

// TestTerms_ProjectClosed is plan reading 2: setting terms is a change of
// the row, refused on a completed project; clearing them is allowed there,
// as deleting the row is.
func TestTerms_ProjectClosed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectCompleted, from: "2026-08-03", to: "2026-08-28", load: 50})
	wantCode(t, problemOf(t, putTerms(c, id, map[string]any{"minimumUntil": "2026-11-30"}), http.StatusConflict), "project_closed")

	seedTerms(t, h, id, seedTerm{minimumUntil: "2026-11-30"})
	if res := c.Do(http.MethodDelete, termsPath(id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE terms on a closed project = %d %s, want 204", res.Status, res.Body)
	}
	if got := getAllocation(t, c, id); got.Terms != nil || got.EarliestRelease.String() != "2026-08-28" {
		t.Errorf("after the clear = %+v, want no terms and the row's end", got)
	}
}

// TestTerms_Audiences is S14's visibility: the terms whole to
// planning:manage, the derived dates alone to planning:view — on the list,
// the row, the board and the plan — and no write for planning:view.
func TestTerms_Audiences(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	viewer, _ := signIn(t, h, "planning:view")
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	termsSet(t, putTerms(c, id, map[string]any{"minimumUntil": "2026-11-27", "handoverDays": 5, "reference": "Avtale"}))

	check := func(who string, a gen.PlanningAllocation, withTerms bool) {
		t.Helper()
		if a.EarliestRelease.String() != "2026-11-27" || a.HandoverStart == nil || a.HandoverStart.String() != "2026-11-22" {
			t.Errorf("%s: derived = %s, %v; want 2026-11-27 and 2026-11-22", who, a.EarliestRelease, a.HandoverStart)
		}
		if withTerms != (a.Terms != nil) {
			t.Errorf("%s: terms = %+v, want present %v", who, a.Terms, withTerms)
		}
	}
	check("manage's row", getAllocation(t, c, id), true)
	check("view's row", getAllocation(t, viewer, id), false)
	for name, client := range map[string]*modtest.Client{"manage": c, "view": viewer} {
		var list gen.PlanningAllocationList
		getJSON(t, client, allocationsPath+"?personId=1001", http.StatusOK, &list)
		check(name+"'s list", list.Allocations[0], name == "manage")
		var board gen.PlanningBoard
		getJSON(t, client, "/api/v1/planning/board?from=2026-10-05&to=2026-11-01", http.StatusOK, &board)
		check(name+"'s board", board.Allocations[0], name == "manage")
		var plan gen.PlanningProjectPlan
		getJSON(t, client, "/api/v1/planning/projects/2001/plan?from=2026-10-05&to=2026-11-01", http.StatusOK, &plan)
		check(name+"'s plan", plan.Allocations[0], name == "manage")
	}
	if res := putTerms(viewer, id, map[string]any{}, ifMatch(1)); res.Status != http.StatusForbidden {
		t.Errorf("PUT terms with view = %d, want 403", res.Status)
	}
	if res := viewer.Do(http.MethodDelete, termsPath(id), nil); res.Status != http.StatusForbidden {
		t.Errorf("DELETE terms with view = %d, want 403", res.Status)
	}
}

// TestTerms_DerivedDates is S15 under each combination, read back through
// GET /allocations/{id} for a row running 5 to 30 October 2026: the latest
// of the end, the minimum term and the notice's end, the notice counted in
// each unit (months by Staff's month rule), given before or after the end,
// and the handover counted back from the release.
func TestTerms_DerivedDates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	if got := getAllocation(t, c, id); got.EarliestRelease.String() != "2026-10-30" || got.HandoverStart != nil || got.Terms != nil {
		t.Fatalf("with no terms = %+v, want the end and no handover", got)
	}
	for _, tc := range []struct {
		name     string
		body     map[string]any
		release  string
		handover string
	}{
		{"a minimum after the end", map[string]any{"minimumUntil": "2026-12-31"}, "2026-12-31", ""},
		{"a minimum before the end", map[string]any{"minimumUntil": "2026-10-01"}, "2026-10-30", ""},
		{"14 days given after", map[string]any{"noticeCount": 14, "noticeUnit": "days", "noticeGivenOn": "2026-10-20"}, "2026-11-03", ""},
		{"2 weeks given early", map[string]any{"noticeCount": 2, "noticeUnit": "weeks", "noticeGivenOn": "2026-10-01"}, "2026-10-30", ""},
		{"3 weeks given after", map[string]any{"noticeCount": 3, "noticeUnit": "weeks", "noticeGivenOn": "2026-10-26"}, "2026-11-16", ""},
		{"3 months to a short month", map[string]any{"noticeCount": 3, "noticeUnit": "months", "noticeGivenOn": "2026-11-30"}, "2027-02-28", ""},
		{"notice not given", map[string]any{"noticeCount": 3, "noticeUnit": "months"}, "2026-10-30", ""},
		{"the notice past the minimum", map[string]any{"minimumUntil": "2026-11-15", "noticeCount": 1, "noticeUnit": "months", "noticeGivenOn": "2026-10-20"}, "2026-11-20", ""},
		{"a handover of ten days", map[string]any{"minimumUntil": "2026-12-31", "handoverDays": 10}, "2026-12-31", "2026-12-21"},
		{"a handover with no other term", map[string]any{"handoverDays": 5}, "2026-10-30", "2026-10-25"},
	} {
		if res := c.Do(http.MethodDelete, termsPath(id), nil); res.Status != http.StatusNoContent {
			t.Fatalf("DELETE terms = %d %s", res.Status, res.Body)
		}
		termsSet(t, putTerms(c, id, tc.body))
		got := getAllocation(t, c, id)
		handover := ""
		if got.HandoverStart != nil {
			handover = got.HandoverStart.String()
		}
		if got.EarliestRelease.String() != tc.release || handover != tc.handover {
			t.Errorf("%s: release %s, handover %q; want %s, %q", tc.name, got.EarliestRelease, handover, tc.release, tc.handover)
		}
	}
}

// TestTerms_Events is S23's record of the terms: terms_set with the fields
// set, then only those a replace changed, before and after; nothing for a
// replace that changes nothing, which leaves the revision too; terms_cleared
// with the fields before; nothing for clearing a row with no terms; and a
// first PUT of an empty body recorded as terms_set with no fields.
func TestTerms_Events(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	body := map[string]any{"noticeCount": 3, "noticeUnit": "months", "replacementNeedsApproval": true,
		"nonCompeteUntil": "2027-06-30", "nonCompeteCustomerIds": []int32{3001}, "reference": "A-1"}
	termsSet(t, putTerms(c, id, body))
	fields := eventPayload(t, h, id, "terms_set")["fields"]
	if len(fields) != 6 || fields["noticeCount"]["to"] != 3.0 || fields["noticeUnit"]["to"] != "months" ||
		fields["replacementNeedsApproval"]["to"] != true || fields["nonCompeteUntil"]["to"] != "2027-06-30" ||
		fields["reference"]["to"] != "A-1" || fields["noticeCount"]["from"] != nil {
		t.Errorf("terms_set = %v, want the six fields set", fields)
	}
	if ids, ok := fields["nonCompeteCustomerIds"]["to"].([]any); !ok || len(ids) != 1 || ids[0] != 3001.0 {
		t.Errorf("nonCompeteCustomerIds = %v, want the list [3001]", fields["nonCompeteCustomerIds"])
	}

	body["reference"] = "A-2"
	if set := termsSet(t, putTerms(c, id, body, ifMatch(1))); set.Terms.Revision != 2 {
		t.Fatalf("revision = %d, want 2", set.Terms.Revision)
	}
	fields = eventPayload(t, h, id, "terms_set")["fields"]
	if len(fields) != 1 || fields["reference"]["from"] != "A-1" || fields["reference"]["to"] != "A-2" {
		t.Errorf("the replace's terms_set = %v, want the reference alone", fields)
	}
	if set := termsSet(t, putTerms(c, id, body, ifMatch(2))); set.Terms.Revision != 2 {
		t.Errorf("an unchanged replace moved the revision to %d", set.Terms.Revision)
	}
	if got := termsEvents(t, h, id); !slices.Equal(got, []string{"terms_set", "terms_set"}) {
		t.Errorf("events = %v, want two terms_set", got)
	}

	if res := c.Do(http.MethodDelete, termsPath(id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE terms = %d %s", res.Status, res.Body)
	}
	fields = eventPayload(t, h, id, "terms_cleared")["fields"]
	if len(fields) != 6 || fields["reference"]["from"] != "A-2" || fields["reference"]["to"] != nil || fields["noticeUnit"]["from"] != "months" {
		t.Errorf("terms_cleared = %v, want the six fields before", fields)
	}
	if res := c.Do(http.MethodDelete, termsPath(id), nil); res.Status != http.StatusNoContent {
		t.Errorf("DELETE of no terms = %d, want 204", res.Status)
	}
	if got := termsEvents(t, h, id); !slices.Equal(got, []string{"terms_set", "terms_set", "terms_cleared"}) {
		t.Errorf("events = %v, want nothing for clearing no terms", got)
	}

	if set := termsSet(t, putTerms(c, id, map[string]any{})); set.Terms == nil || set.Terms.Revision != 1 {
		t.Fatalf("an empty first PUT = %+v, want terms at revision 1", set.Terms)
	}
	if got := termsEvents(t, h, id); !slices.Equal(got, []string{"terms_set", "terms_set", "terms_cleared", "terms_set"}) {
		t.Errorf("events = %v, want terms_set for the empty insert", got)
	}
	if fields := eventPayload(t, h, id, "terms_set")["fields"]; fields == nil || len(fields) != 0 {
		t.Errorf("the empty insert's fields = %v, want {}", fields)
	}
	termsSet(t, putTerms(c, id, map[string]any{}, ifMatch(1)))
	if got := termsEvents(t, h, id); len(got) != 4 {
		t.Errorf("events = %v, want nothing for an unchanged replace of empty terms", got)
	}
}

// TestTerms_CustomersUnnamed is rule 4 on the terms' answers: a customer
// directory that fails after the terms are written is logged, and the read
// answers each customer of the non-compete as its id alone rather than
// failing.
func TestTerms_CustomersUnnamed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	termsSet(t, putTerms(c, id, map[string]any{"nonCompeteUntil": "2027-06-30", "nonCompeteCustomerIds": []int32{customerKraftVerket}}))

	h.customers.fail(errors.New("customers are down"))
	got := getAllocation(t, c, id)
	if got.Terms == nil || len(got.Terms.NonCompeteCustomers) != 1 || got.Terms.NonCompeteCustomers[0].Id != customerKraftVerket ||
		got.Terms.NonCompeteCustomers[0].Name != nil {
		t.Errorf("terms = %+v, want the customer as its id alone", got.Terms)
	}
}

// holdPersonLock takes person's write lock on a connection of its own, as a
// concurrent write would hold it, and answers its release.
func holdPersonLock(t *testing.T, h *harness, person int64) func() {
	t.Helper()
	holder, err := h.Pool().Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire a connection: %v", err)
	}
	lock := "planning-person-" + strconv.FormatInt(person, 10)
	if _, err := holder.Exec(t.Context(), `SELECT pg_advisory_lock(hashtext($1))`, lock); err != nil {
		t.Fatalf("hold %s: %v", lock, err)
	}
	return func() {
		if _, err := holder.Exec(t.Context(), `SELECT pg_advisory_unlock(hashtext($1))`, lock); err != nil {
			t.Fatalf("release %s: %v", lock, err)
		}
		holder.Release()
	}
}

// TestTerms_RowMovedUnderTheLock is amendment 68's retry: a terms PUT or
// DELETE that waits on the person's lock while the row is given to another
// person reads the row again, takes the new person's lock and is written,
// its event filed under the person the row is on.
func TestTerms_RowMovedUnderTheLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	moveTo := func(person int64) {
		h.Exec(t, `UPDATE planning.allocations SET person_id = $1, revision = revision + 1 WHERE id = $2`, person, id)
	}
	eventPerson := func(kind string) int64 {
		return modtest.One[int64](t, h.Harness,
			`SELECT person_id FROM planning.events WHERE entity_kind = 'allocation' AND entity_id = $1 AND kind = $2 ORDER BY id DESC LIMIT 1`, id, kind)
	}
	waitFor := func(name string, release func(), move func(), call func() *modtest.Response) *modtest.Response {
		t.Helper()
		done := make(chan *modtest.Response, 1)
		go func() { done <- call() }()
		select {
		case res := <-done:
			t.Errorf("%s = %d while the person was locked, want it to wait", name, res.Status)
		case <-time.After(300 * time.Millisecond):
		}
		move()
		release()
		return <-done
	}

	release := holdPersonLock(t, h, personKari)
	res := waitFor("PUT terms", release, func() { moveTo(personOla) }, func() *modtest.Response {
		return putTerms(c, id, map[string]any{"reference": "A-1"})
	})
	if set := termsSet(t, res); set.Terms == nil || set.Terms.Reference == nil || *set.Terms.Reference != "A-1" || set.Person.Id != personOla {
		t.Errorf("the PUT after the move = %+v, want the terms on Ola's row", set)
	}
	if p := eventPerson("terms_set"); p != personOla {
		t.Errorf("terms_set is filed under %d, want Ola", p)
	}

	release = holdPersonLock(t, h, personOla)
	res = waitFor("DELETE terms", release, func() { moveTo(personKari) }, func() *modtest.Response {
		return c.Do(http.MethodDelete, termsPath(id), nil)
	})
	if res.Status != http.StatusNoContent {
		t.Fatalf("the DELETE after the move = %d %s, want 204", res.Status, res.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.allocation_terms WHERE allocation_id = $1`, id); n != 0 {
		t.Errorf("the terms are still stored after the DELETE")
	}
	if p := eventPerson("terms_cleared"); p != personKari {
		t.Errorf("terms_cleared is filed under %d, want Kari", p)
	}
}

// bindKari gives Kari a confirmed row on the internal project through
// September and October whose terms bar Kraft-Verket until the end of June
// 2027, and answers its id.
func bindKari(t *testing.T, h *harness, status string) int64 {
	t.Helper()
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: "2026-09-01", to: "2026-10-31", load: 20, status: status})
	seedTerms(t, h, id, seedTerm{nonCompeteUntil: "2027-06-30", customers: []int32{customerKraftVerket}})
	return id
}

// allocationCount is how many rows person has.
func allocationCount(t *testing.T, h *harness, person int64) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT count(*) FROM planning.allocations WHERE person_id = $1`, person)
}

// TestNonCompete_Writes is S17.3 on the writes: a confirmed row of Kari on a
// Kraft-Verket project is the 409 non_compete naming the binding row and the
// date — on a POST, a PUT, a fill (the request left open, nothing written)
// and a confirm — while a tentative one is written with the warning.
func TestNonCompete_Writes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	binding := bindKari(t, h, "confirmed")

	p := problemOf(t, c.Do(http.MethodPost, allocationsPath, booking(map[string]any{"status": "confirmed"})), http.StatusConflict)
	wantCode(t, p, "non_compete")
	if p.Detail == nil || !strings.Contains(*p.Detail, "2027-06-30") || !strings.Contains(*p.Detail, strconv.FormatInt(binding, 10)) {
		t.Errorf("detail = %v, want the binding row and the date", p.Detail)
	}
	if n := allocationCount(t, h, personKari); n != 1 {
		t.Errorf("Kari has %d rows after the refusal, want the binding one", n)
	}

	tentative := create(t, c, booking(map[string]any{"status": "tentative"}))
	w, ok := warningCodes(tentative)["non_compete"]
	if !ok || w.AllocationId == nil || *w.AllocationId != binding || w.Date == nil || w.Date.String() != "2027-06-30" {
		t.Errorf("warnings = %+v, want non_compete naming %d until 2027-06-30", tentative.Warnings, binding)
	}
	wantCode(t, problemOf(t, c.Do(http.MethodPost, allocationPath(tentative.Allocation.Id)+"/confirm", nil), http.StatusConflict), "non_compete")

	hydro := create(t, c, booking(map[string]any{"projectId": projectHydro, "status": "confirmed"})).Allocation
	moved := booking(map[string]any{"projectId": projectActive, "status": "confirmed"})
	wantCode(t, problemOf(t, put(c, hydro.Id, hydro.Revision, moved), http.StatusConflict), "non_compete")
	warned := written(t, put(c, hydro.Id, hydro.Revision, booking(map[string]any{"projectId": projectActive, "status": "tentative"})))
	if _, ok := warningCodes(warned)["non_compete"]; !ok {
		t.Errorf("a PUT leaving the row tentative = %+v, want the warning", warned.Warnings)
	}

	request := seedRequest(t, h, seedReq{project: projectActive, from: "2026-11-02", to: "2026-11-27", hours: 10})
	wantCode(t, problemOf(t, c.Do(http.MethodPost, requestPath(request)+"/fill", map[string]any{"personId": personKari, "status": "confirmed"}), http.StatusConflict), "non_compete")
	if r := getRequest(t, c, request); r.Status != gen.Open || r.FilledBy != nil {
		t.Errorf("the request after a refused fill = %+v, want open", r)
	}
	if n := modtest.One[int64](t, h.Harness, `SELECT count(*) FROM planning.allocations WHERE request_id = $1`, request); n != 0 {
		t.Errorf("%d rows name the request after a refused fill", n)
	}
	filled := fillRequest(t, c, request, map[string]any{"personId": personKari, "status": "tentative"})
	if _, ok := warningCodes(gen.PlanningAllocationWritten{Warnings: filled.Warnings})["non_compete"]; !ok {
		t.Errorf("a tentative fill = %+v, want the warning", filled.Warnings)
	}
}

// TestNonCompete_PutPlaces is amendment 90: a PUT of a confirmed row is
// refused non_compete only when it places the row — another project of the
// barred customer, confirmed from tentative, or a first day moved earlier
// past the until date — while any other PUT of a confirmed row already on
// the barred customer is written with the warning.
func TestNonCompete_PutPlaces(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	binding := bindKari(t, h, "confirmed")
	row := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-11-09", to: "2026-11-27", load: 20})
	body := func(fields map[string]any) map[string]any {
		out := map[string]any{"fromDate": "2026-11-09", "toDate": "2026-11-27", "load": 20, "status": "confirmed"}
		maps.Copy(out, fields)
		return booking(out)
	}
	warnsOn := func(name string, w gen.PlanningAllocationWritten) {
		t.Helper()
		got, ok := warningCodes(w)["non_compete"]
		if !ok || got.AllocationId == nil || *got.AllocationId != binding {
			t.Errorf("%s: warnings = %+v, want non_compete naming %d", name, w.Warnings, binding)
		}
	}

	retitled := written(t, put(c, row, getAllocation(t, c, row).Revision, body(map[string]any{"title": "Renamed"})))
	warnsOn("a title changed", retitled)
	wantCode(t, problemOf(t, put(c, row, retitled.Allocation.Revision, body(map[string]any{"title": "Renamed", "projectId": projectPlanned})), http.StatusConflict), "non_compete")
	later := written(t, put(c, row, retitled.Allocation.Revision, body(map[string]any{"title": "Renamed", "fromDate": "2026-11-16"})))
	warnsOn("the first day moved later", later)

	// A non-compete ending between the new and the old first day newly holds.
	h.Exec(t, `UPDATE planning.allocation_terms SET non_compete_until = '2026-11-12' WHERE allocation_id = $1`, binding)
	wantCode(t, problemOf(t, put(c, row, later.Allocation.Revision, body(map[string]any{"title": "Renamed", "fromDate": "2026-11-09"})), http.StatusConflict), "non_compete")
	if got := getAllocation(t, c, row); got.FromDate.String() != "2026-11-16" || got.Project.Id != projectActive {
		t.Errorf("the row after the refusals = %+v, want it where it was", got)
	}
	h.Exec(t, `UPDATE planning.allocation_terms SET non_compete_until = '2027-06-30' WHERE allocation_id = $1`, binding)

	tentative := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-12-07", to: "2026-12-18", load: 20, status: "tentative"})
	wantCode(t, problemOf(t, put(c, tentative, getAllocation(t, c, tentative).Revision,
		body(map[string]any{"fromDate": "2026-12-07", "toDate": "2026-12-18"})), http.StatusConflict), "non_compete")
}

// TestNonCompete_BeforeOverbooking is amendment 66 (T3): under the refuse
// setting a confirmed booking that would both overbook Kari and break her
// non-compete is refused non_compete, and nothing is written.
func TestNonCompete_BeforeOverbooking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	if res := c.Do(http.MethodPut, settingsPath, map[string]any{"overbooking": "refuse"}, ifMatch(1)); res.Status != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s", res.Status, res.Body)
	}
	bindKari(t, h, "confirmed")
	seedAllocation(t, h, seedRow{person: personKari, project: projectHydro, from: "2026-11-02", to: "2026-11-06", load: 100})
	week := booking(map[string]any{"fromDate": "2026-11-02", "toDate": "2026-11-06", "load": 50, "status": "confirmed"})

	wantCode(t, problemOf(t, c.Do(http.MethodPost, allocationsPath, week), http.StatusConflict), "non_compete")
	if n := allocationCount(t, h, personKari); n != 2 {
		t.Errorf("Kari has %d rows after the refusal, want 2", n)
	}
	// The same week on a customer the non-compete does not name is the
	// overbooking's refusal: the excess alone.
	week["projectId"] = projectHydro
	wantCode(t, problemOf(t, c.Do(http.MethodPost, allocationsPath, week), http.StatusConflict), "overbooked")
}

// TestNonCompete_DoesNotFire is S17.3's limits: another customer, a project
// with no customer, another person, a non-compete expired before the row's
// first day, a tentative binding row, and the binding row's own PUT.
func TestNonCompete_DoesNotFire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	binding := bindKari(t, h, "confirmed")
	confirmed := map[string]any{"status": "confirmed", "load": 20}

	for name, body := range map[string]map[string]any{
		"another customer":     booking(map[string]any{"projectId": projectHydro, "status": "confirmed", "load": 20}),
		"no customer":          booking(map[string]any{"projectId": projectInternal, "status": "confirmed", "load": 20, "fromDate": "2026-11-02", "toDate": "2026-11-27"}),
		"another person":       booking(map[string]any{"personId": personOla, "status": "confirmed"}),
		"after the until date": booking(map[string]any{"status": "confirmed", "load": 20, "fromDate": "2027-07-05", "toDate": "2027-07-30"}),
	} {
		res := c.Do(http.MethodPost, allocationsPath, body)
		if res.Status != http.StatusCreated {
			t.Errorf("%s = %d %s, want 201", name, res.Status, res.Body)
			continue
		}
		var out gen.PlanningAllocationWritten
		res.JSON(&out)
		if _, ok := warningCodes(out)["non_compete"]; ok {
			t.Errorf("%s warned non_compete", name)
		}
	}

	// A non-compete that ended before the row starts binds nothing.
	h.Exec(t, `UPDATE planning.allocation_terms SET non_compete_until = '2026-10-02' WHERE allocation_id = $1`, binding)
	create(t, c, booking(confirmed))
	h.Exec(t, `UPDATE planning.allocation_terms SET non_compete_until = '2027-06-30' WHERE allocation_id = $1`, binding)

	// The binding row's own PUT is not judged against itself.
	own := getAllocation(t, c, binding)
	written(t, put(c, binding, own.Revision, map[string]any{
		"personId": personKari, "projectId": projectInternal, "fromDate": "2026-09-01", "toDate": "2026-10-31",
		"loadUnit": "percent", "load": 10, "status": "confirmed",
	}))

	// A tentative binding row binds nothing (reading 8).
	h.Exec(t, `UPDATE planning.allocations SET status = 'tentative', confirmed_at = NULL WHERE id = $1`, binding)
	create(t, c, booking(map[string]any{"status": "confirmed", "load": 20, "fromDate": "2026-11-02", "toDate": "2026-11-27"}))
}
