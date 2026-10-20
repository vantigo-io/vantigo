package staff_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const (
	groupsPath   = "/api/v1/staff/groups"
	meGroupsPath = "/api/v1/staff/me/groups"
)

func groupPath(id int64) string        { return rowPath(groupsPath, id) }
func groupMembersPath(id int64) string { return groupPath(id) + "/members" }
func groupOwnersPath(id int64) string  { return groupPath(id) + "/owners" }
func groupTraitsPath(id int64) string  { return groupPath(id) + "/traits" }
func groupTraitPath(id int64, provider string) string {
	return groupTraitsPath(id) + "/" + provider
}

// listedGroup is the body of a listed group under key.
func listedGroup(key string) map[string]any {
	return map[string]any{"key": key, "name": "Group " + key, "kind": "listed"}
}

// derivedGroup is the body of a derived group under key with rule.
func derivedGroup(key string, rule map[string]any) map[string]any {
	return map[string]any{"key": key, "name": "Group " + key, "kind": "derived", "rule": rule}
}

// createGroup adds a group and fails the test unless it answered 201.
func createGroup(t *testing.T, c *modtest.Client, body map[string]any) gen.StaffGroup {
	t.Helper()
	var out gen.StaffGroup
	do(t, c, http.MethodPost, groupsPath, body, http.StatusCreated, &out)
	return out
}

// getGroup reads one group and fails the test unless it answered 200.
func getGroup(t *testing.T, c *modtest.Client, id int64) gen.StaffGroup {
	t.Helper()
	var out gen.StaffGroup
	do(t, c, http.MethodGet, groupPath(id), nil, http.StatusOK, &out)
	return out
}

// memberIDs is a group's members' person ids, in the answer's order.
func memberIDs(g gen.StaffGroup) []int64 {
	var out []int64
	if g.Members != nil {
		for _, m := range *g.Members {
			out = append(out, m.PersonId)
		}
	}
	return out
}

// A group is added at revision 1 with no providers, read alone with its
// members and owners and in the list without them, changed under If-Match —
// its key never — archived once, and refused every write while archived.
func TestGroups_Lifecycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	g := createGroup(t, hr, with(listedGroup("sales"), "description", "Everyone selling"))
	if g.Revision != 1 || g.Key != "sales" || g.Kind != gen.Listed || g.Rule != nil || len(g.Providers) != 0 {
		t.Fatalf("created %+v, want revision 1, key sales, listed, no rule, no providers", g)
	}
	if g.Members == nil || len(*g.Members) != 0 || g.Owners == nil || len(*g.Owners) != 0 {
		t.Errorf("created members %v owners %v, want both empty and present", g.Members, g.Owners)
	}

	p := refused(t, hr.Do(http.MethodPost, groupsPath, listedGroup("sales")), http.StatusBadRequest, "")
	if _, ok := p.Errors["key"]; !ok {
		t.Errorf("a key taken: errors %v, want one on key", p.Errors)
	}

	var list []gen.StaffGroup
	do(t, hr, http.MethodGet, groupsPath, nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].Id != g.Id {
		t.Fatalf("list = %+v, want the one group", list)
	}
	if body := hr.Do(http.MethodGet, groupsPath, nil); strings.Contains(string(body.Body), `"members"`) || strings.Contains(string(body.Body), `"owners"`) {
		t.Errorf("the list carries members or owners: %s", body.Body)
	}

	var changed gen.StaffGroup
	do(t, hr, http.MethodPut, groupPath(g.Id), map[string]any{"name": "Sales team", "kind": "listed"}, http.StatusOK, &changed, ifMatch(g.Revision))
	if changed.Revision != 2 || changed.Name != "Sales team" || changed.Key != "sales" || changed.Description != nil {
		t.Errorf("changed %+v, want revision 2, the new name, the key kept and the description cleared", changed)
	}
	refused(t, hr.Do(http.MethodPut, groupPath(g.Id), map[string]any{"name": "x", "kind": "listed"}, ifMatch(1)), http.StatusConflict, "revision_conflict")
	p = refused(t, hr.Do(http.MethodPut, groupPath(g.Id), map[string]any{"key": "marketing", "name": "x", "kind": "listed"}, ifMatch(2)), http.StatusBadRequest, "")
	if _, ok := p.Errors["key"]; !ok {
		t.Errorf("a key changed: errors %v, want one on key", p.Errors)
	}
	do(t, hr, http.MethodPut, groupPath(g.Id), map[string]any{"key": "sales", "name": "Sales", "kind": "listed"}, http.StatusOK, &changed, ifMatch(2))

	var archived gen.StaffGroup
	do(t, hr, http.MethodPost, archivePath(groupsPath, g.Id), nil, http.StatusOK, &archived)
	if archived.ArchivedAt == nil || archived.Revision != changed.Revision+1 {
		t.Fatalf("archived %+v, want archivedAt and the revision moved", archived)
	}
	var again gen.StaffGroup
	do(t, hr, http.MethodPost, archivePath(groupsPath, g.Id), nil, http.StatusOK, &again)
	if again.Revision != archived.Revision || !again.ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Errorf("archived again %+v, want nothing changed", again)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.groups WHERE id = $1 AND archived_by_user_id IS NOT NULL`, g.Id); n != 1 {
		t.Errorf("archived_by_user_id not stamped")
	}

	refused(t, hr.Do(http.MethodPut, groupPath(g.Id), map[string]any{"name": "x", "kind": "listed"}, ifMatch(archived.Revision)), http.StatusConflict, "group_archived")
	refused(t, hr.Do(http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": []int64{}}), http.StatusConflict, "group_archived")
	refused(t, hr.Do(http.MethodPut, groupOwnersPath(g.Id), map[string]any{"personIds": []int64{}}), http.StatusConflict, "group_archived")

	for _, path := range []string{groupPath(999999), archivePath(groupsPath, 999999)} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/archive") {
			method = http.MethodPost
		}
		if r := hr.Do(method, path, nil); r.Status != http.StatusNotFound {
			t.Errorf("%s %s: status %d, want 404", method, path, r.Status)
		}
	}
	if r := hr.Do(http.MethodPut, groupPath(999999), map[string]any{"name": "x", "kind": "listed"}, ifMatch(1)); r.Status != http.StatusNotFound {
		t.Errorf("PUT an unknown group: status %d, want 404", r.Status)
	}
}

// A group no provider ever reached is deleted, its members leaving it with
// an event each; one a provider made or found, one with a provider turned
// on, or one the worker is sending right now (a live lease, its first sync
// in flight) is group_synced, to archive (the review's LOW 5).
func TestGroups_Delete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	person := seedPerson(t, h, "Kari", "Nordmann")

	g := createGroup(t, hr, listedGroup("never-synced"))
	do(t, hr, http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": []int64{person}}, http.StatusOK, nil)
	// Turned on once and off again, never synced, its last lease lapsed.
	h.Exec(t, `INSERT INTO staff.group_traits (group_id, provider, values, state, enabled, disabled_at, lease_until)
		VALUES ($1, 'entra', '{}', 'pending', false, $2, $3)`, g.Id, h.Now(), h.Now().Add(-time.Minute))
	do(t, hr, http.MethodDelete, groupPath(g.Id), nil, http.StatusNoContent, nil)
	if n := h.Count(t, `SELECT count(*) FROM staff.groups WHERE id = $1`, g.Id); n != 0 {
		t.Errorf("the group is still there")
	}
	if got := groupEvents(t, h, person); got != "group_joined:never-synced:listed,group_left:never-synced:listed" {
		t.Errorf("events = %q, want the join and the leave", got)
	}
	if r := hr.Do(http.MethodDelete, groupPath(g.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("delete again: status %d, want 404", r.Status)
	}

	synced := createGroup(t, hr, listedGroup("synced"))
	h.Exec(t, `INSERT INTO staff.group_traits (group_id, provider, values, state, synced_at) VALUES ($1, 'entra', '{}', 'synced', $2)`, synced.Id, h.Now())
	refused(t, hr.Do(http.MethodDelete, groupPath(synced.Id), nil), http.StatusConflict, "group_synced")

	found := createGroup(t, hr, listedGroup("found"))
	h.Exec(t, `INSERT INTO staff.group_traits (group_id, provider, values, state, external_id) VALUES ($1, 'entra', '{}', 'failed', 'ext-1')`, found.Id)
	refused(t, hr.Do(http.MethodDelete, groupPath(found.Id), nil), http.StatusConflict, "group_synced")

	on := createGroup(t, hr, listedGroup("turned-on"))
	h.Exec(t, `INSERT INTO staff.group_traits (group_id, provider, values, state) VALUES ($1, 'entra', '{}', 'pending')`, on.Id)
	refused(t, hr.Do(http.MethodDelete, groupPath(on.Id), nil), http.StatusConflict, "group_synced")

	inFlight := createGroup(t, hr, listedGroup("in-flight"))
	h.Exec(t, `INSERT INTO staff.group_traits (group_id, provider, values, state, enabled, disabled_at, lease_id, lease_until)
		VALUES ($1, 'entra', '{}', 'pending', false, $2, gen_random_uuid(), $3)`, inFlight.Id, h.Now(), h.Now().Add(time.Minute))
	refused(t, hr.Do(http.MethodDelete, groupPath(inFlight.Id), nil), http.StatusConflict, "group_synced")
}

// A rule names what exists — a department, business unit, location or
// person, archived or not — and a derived group's rule comes back as it is
// stored.
func TestGroups_RuleReferences(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	dept := seedDepartment(t, h, "OPS", 0, 0)
	h.Exec(t, `UPDATE staff.departments SET archived_at = $2 WHERE id = $1`, dept, h.Now())
	person := seedPerson(t, h, "Ola", "Nordmann")
	unit := seedUnit(t, h, "Unit")
	location := modtest.One[int64](t, h.Harness, `INSERT INTO staff.locations (name, created_at, updated_at) VALUES ('Oslo', $1, $1) RETURNING id`, h.Now())

	g := createGroup(t, hr, derivedGroup("ops", map[string]any{"kind": "department", "id": dept, "includeBelow": true}))
	if g.Rule == nil || g.Rule.Kind != gen.Department || g.Rule.Id == nil || *g.Rule.Id != dept || g.Rule.IncludeBelow == nil || !*g.Rule.IncludeBelow {
		t.Errorf("rule = %+v, want department %d below", g.Rule, dept)
	}
	createGroup(t, hr, derivedGroup("unit", map[string]any{"kind": "businessUnit", "id": unit}))
	createGroup(t, hr, derivedGroup("loc", map[string]any{"kind": "location", "id": location}))
	createGroup(t, hr, derivedGroup("team", map[string]any{"kind": "reportsTo", "id": person}))
	createGroup(t, hr, derivedGroup("all", map[string]any{"kind": "employed"}))
	createGroup(t, hr, derivedGroup("interns", map[string]any{"kind": "employeeType", "value": "intern"}))

	for i, rule := range []map[string]any{
		{"kind": "department", "id": 999999},
		{"kind": "businessUnit", "id": 999999},
		{"kind": "location", "id": 999999},
		{"kind": "reportsTo", "id": 999999},
	} {
		p := refused(t, hr.Do(http.MethodPost, groupsPath, derivedGroup(fmt.Sprintf("bad-%d", i), rule)), http.StatusBadRequest, "")
		if _, ok := p.Errors["rule.id"]; !ok {
			t.Errorf("%v: errors %v, want one on rule.id", rule, p.Errors)
		}
	}
	p := refused(t, hr.Do(http.MethodPost, groupsPath, derivedGroup("bad-type", map[string]any{"kind": "employeeType", "value": "boss"})), http.StatusBadRequest, "")
	if _, ok := p.Errors["rule.value"]; !ok {
		t.Errorf("an unknown employee type: errors %v, want one on rule.value", p.Errors)
	}
	p = refused(t, hr.Do(http.MethodPost, groupsPath, derivedGroup("bad-below", map[string]any{"kind": "location", "id": location, "includeBelow": true})), http.StatusBadRequest, "")
	if _, ok := p.Errors["rule.includeBelow"]; !ok {
		t.Errorf("includeBelow on a location: errors %v, want one on rule.includeBelow", p.Errors)
	}
	p = refused(t, hr.Do(http.MethodPost, groupsPath, map[string]any{"key": "no-rule", "name": "x", "kind": "derived"}), http.StatusBadRequest, "")
	if _, ok := p.Errors["rule"]; !ok {
		t.Errorf("a derived group with no rule: errors %v, want one on rule", p.Errors)
	}

	// A change that makes the group listed drops the rule.
	var listed gen.StaffGroup
	do(t, hr, http.MethodPut, groupPath(g.Id), map[string]any{"name": "Ops", "kind": "listed"}, http.StatusOK, &listed, ifMatch(g.Revision))
	if listed.Kind != gen.Listed || listed.Rule != nil {
		t.Errorf("made listed: %+v, want listed with no rule", listed)
	}
}

// Every group operation is staff:manage's: staff:view alone — or nothing
// beyond staff:access — gets the uniform 403. My own groups are anyone's.
func TestGroups_ViewAloneIsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	g := createGroup(t, hr, listedGroup("forbidden"))
	viewer, _ := signIn(t, h, "staff:view", "staff:personal-data")
	for _, op := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, groupsPath, nil},
		{http.MethodPost, groupsPath, listedGroup("other")},
		{http.MethodGet, groupPath(g.Id), nil},
		{http.MethodPut, groupPath(g.Id), map[string]any{"name": "x", "kind": "listed"}},
		{http.MethodDelete, groupPath(g.Id), nil},
		{http.MethodPost, archivePath(groupsPath, g.Id), nil},
		{http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": []int64{}}},
		{http.MethodPut, groupOwnersPath(g.Id), map[string]any{"personIds": []int64{}}},
		{http.MethodGet, groupTraitsPath(g.Id), nil},
		{http.MethodPut, groupTraitPath(g.Id, "entra"), map[string]any{"enabled": false}},
	} {
		var opts []modtest.RequestOption
		if op.method == http.MethodPut && op.path == groupPath(g.Id) {
			opts = append(opts, ifMatch(1))
		}
		if r := viewer.Do(op.method, op.path, op.body, opts...); r.Status != http.StatusForbidden {
			t.Errorf("%s %s as staff:view: status %d, want 403", op.method, op.path, r.Status)
		}
	}
	refused(t, viewer.Do(http.MethodGet, meGroupsPath, nil), http.StatusNotFound, "no_person")
}

// My own groups are the groups my person is in, archived ones aside, by
// name only; the Account tab names them with how and since when.
func TestGroups_MineAndTheAccountTab(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	me, user := signIn(t, h)
	person := seedPerson(t, h, "Kari", "Nordmann")
	link(t, h, person, user)

	b := createGroup(t, hr, listedGroup("b-team"))
	a := createGroup(t, hr, with(listedGroup("a-team"), "name", "A team"))
	gone := createGroup(t, hr, listedGroup("gone"))
	for _, g := range []gen.StaffGroup{a, b, gone} {
		do(t, hr, http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": []int64{person}}, http.StatusOK, nil)
	}
	do(t, hr, http.MethodPost, archivePath(groupsPath, gone.Id), nil, http.StatusOK, nil)

	var mine []map[string]any
	do(t, me, http.MethodGet, meGroupsPath, nil, http.StatusOK, &mine)
	if len(mine) != 2 || mine[0]["name"] != "A team" || mine[1]["name"] != "Group b-team" {
		t.Fatalf("my groups = %v, want A team and Group b-team by name", mine)
	}
	for _, g := range mine {
		if len(g) != 2 {
			t.Errorf("my group %v carries more than its id and name", g)
		}
	}

	account := getAccount(t, hr, person)
	if account.Groups == nil || len(*account.Groups) != 2 {
		t.Fatalf("account groups = %v, want two", account.Groups)
	}
	first := (*account.Groups)[0]
	if first.Id != a.Id || first.Key == nil || *first.Key != "a-team" || first.Source == nil || *first.Source != gen.Listed ||
		first.Since == nil || first.Since.Format("2006-01-02") != today(t, h) {
		t.Errorf("account group = %+v, want a-team, listed, since today", first)
	}

	none := seedPerson(t, h, "Ola", "Nordmann")
	if g := getAccount(t, hr, none).Groups; g == nil || len(*g) != 0 {
		t.Errorf("a person in no group: groups %v, want []", g)
	}
}

// groupEvents is a person's group events, oldest first, each kind, the
// group's key and the source, joined by commas.
func groupEvents(t *testing.T, h *harness, personID int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `
		SELECT coalesce(string_agg(kind || ':' || (payload->>'groupKey') || ':' || (payload->>'source'), ',' ORDER BY id), '')
		FROM staff.events WHERE person_id = $1 AND kind IN ('group_joined', 'group_left')`, personID)
}

// sortedIDs is ids sorted, for a comparison that ignores order.
func sortedIDs(ids ...int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}
