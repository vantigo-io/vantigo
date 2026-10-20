package staff_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// placement is one employment as a group's rule reads it: its unit and days,
// and its one terms row's department, location, manager and employee type
// (each 0 or "" for none).
type placement struct {
	unit                       int64
	start, end                 string // end "" for open-ended
	department, location       int64
	manager                    int64
	employeeType, validFromRow string // validFromRow "" for the start
}

// seedUnit writes a business unit under a legal entity of its own.
func seedUnit(t *testing.T, h *harness, name string) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `
		WITH le AS (
			INSERT INTO staff.legal_entities (name, is_default, created_at, updated_at)
			VALUES ($1, false, $2, $2) RETURNING id
		)
		INSERT INTO staff.business_units (legal_entity_id, name, is_default, created_at, updated_at)
		SELECT id, $1, false, $2, $2 FROM le RETURNING id`, name, h.Now())
}

// seedPlacement writes an employment of personID as p says, and answers it.
func seedPlacement(t *testing.T, h *harness, personID int64, p placement) int64 {
	t.Helper()
	opt := func(id int64) *int64 {
		if id == 0 {
			return nil
		}
		return &id
	}
	var end, kind *string
	if p.end != "" {
		end = &p.end
	}
	if p.employeeType != "" {
		kind = &p.employeeType
	}
	from := p.start
	if p.validFromRow != "" {
		from = p.validFromRow
	}
	employment := modtest.One[int64](t, h.Harness, `
		INSERT INTO staff.employments (person_id, employment_number, relationship_type, business_unit_id, start_date, end_date, end_reason, created_at, updated_at)
		VALUES ($1, $2, 'ordinary', $3, $4::date, $5::date, CASE WHEN $5::date IS NULL THEN NULL ELSE 'contract_ended' END, $6, $6) RETURNING id`,
		personID, fmt.Sprintf("G-%d-%d", personID, people.Add(1)), p.unit, p.start, end, h.Now())
	h.Exec(t, `
		INSERT INTO staff.employment_terms (employment_id, valid_from, employment_type, appointment_form, fte_percent,
			full_time_hours_per_week, department_id, location_id, manager_person_id, employee_type, created_at, updated_at)
		VALUES ($1, $2::date, 'fast', 'fast', 100, 37.5, $3, $4, $5, $6, $7, $7)`,
		employment, from, opt(p.department), opt(p.location), opt(p.manager), kind, h.Now())
	return employment
}

// groupDay is a date-only string as the day the module judges on.
func groupDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The rule per kind on one small register: the people with an employment in
// force today, none erased, each judged on their earliest in force — not
// one who has not started, has left, is erased, or whose only terms row is
// to come.
func TestEvaluateRule_PerKind(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	day := groupDay(t, today(t, h))
	past := day.AddDate(-1, 0, 0).Format(time.DateOnly)
	yesterday := day.AddDate(0, 0, -1).Format(time.DateOnly)
	tomorrow := day.AddDate(0, 0, 1).Format(time.DateOnly)

	north, south := seedUnit(t, h, "North"), seedUnit(t, h, "South")
	boss := seedPerson(t, h, "Boss", "Person")
	top := seedDepartment(t, h, "TOP", boss, 0)
	child := seedDepartment(t, h, "CHILD", 0, top)
	grandchild := seedDepartment(t, h, "GRAND", 0, child)
	other := seedDepartment(t, h, "OTHER", 0, 0)
	h.Exec(t, `UPDATE staff.departments SET archived_at = $2 WHERE id = $1`, child, h.Now())
	oslo := modtest.One[int64](t, h.Harness, `INSERT INTO staff.locations (name, created_at, updated_at) VALUES ('Oslo', $1, $1) RETURNING id`, h.Now())

	seedPlacement(t, h, boss, placement{unit: north, start: past, department: top, location: oslo, employeeType: "employee"})
	direct := seedPerson(t, h, "Direct", "Report")
	seedPlacement(t, h, direct, placement{unit: north, start: past, department: child, manager: boss, employeeType: "contractor"})
	viaDept := seedPerson(t, h, "Via", "Department")
	seedPlacement(t, h, viaDept, placement{unit: south, start: past, department: top, location: oslo})
	deep := seedPerson(t, h, "Deep", "Down")
	seedPlacement(t, h, deep, placement{unit: south, start: past, department: grandchild, employeeType: "intern"})
	elsewhere := seedPerson(t, h, "Else", "Where")
	seedPlacement(t, h, elsewhere, placement{unit: south, start: past, department: other, employeeType: "intern"})
	// Two employments: the earliest in force (north, other) is the one judged.
	twice := seedPerson(t, h, "Two", "Jobs")
	seedPlacement(t, h, twice, placement{unit: north, start: past, department: other})
	seedPlacement(t, h, twice, placement{unit: south, start: yesterday, department: top})

	notYet := seedPerson(t, h, "Not", "Yet")
	seedPlacement(t, h, notYet, placement{unit: north, start: tomorrow, department: top})
	left := seedPerson(t, h, "Has", "Left")
	seedPlacement(t, h, left, placement{unit: north, start: past, end: yesterday, department: top})
	endsToday := seedPerson(t, h, "Ends", "Today")
	seedPlacement(t, h, endsToday, placement{unit: north, start: past, end: day.Format(time.DateOnly), department: other})
	erased := seedPerson(t, h, "Is", "Erased")
	seedPlacement(t, h, erased, placement{unit: north, start: past, department: top})
	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, erased, h.Now())
	termsLater := seedPerson(t, h, "Terms", "Later")
	seedPlacement(t, h, termsLater, placement{unit: north, start: past, validFromRow: tomorrow, department: top})

	id := func(v int64) *int64 { return &v }
	str := func(v string) *string { return &v }
	yes := true
	cases := []struct {
		name string
		rule gen.StaffGroupRule
		want []int64
	}{
		{"employed", gen.StaffGroupRule{Kind: gen.Employed}, []int64{boss, direct, viaDept, deep, elsewhere, twice, endsToday, termsLater}},
		{"businessUnit", gen.StaffGroupRule{Kind: gen.BusinessUnit, Id: id(north)}, []int64{boss, direct, twice, endsToday, termsLater}},
		{"department", gen.StaffGroupRule{Kind: gen.Department, Id: id(top)}, []int64{boss, viaDept}},
		{"department below", gen.StaffGroupRule{Kind: gen.Department, Id: id(top), IncludeBelow: &yes}, []int64{boss, direct, viaDept, deep}},
		{"an archived department", gen.StaffGroupRule{Kind: gen.Department, Id: id(child)}, []int64{direct}},
		{"location", gen.StaffGroupRule{Kind: gen.Location, Id: id(oslo)}, []int64{boss, viaDept}},
		{"employeeType", gen.StaffGroupRule{Kind: gen.EmployeeType, Value: str("intern")}, []int64{deep, elsewhere}},
		{"reportsTo", gen.StaffGroupRule{Kind: gen.ReportsTo, Id: id(boss)}, []int64{direct, viaDept, deep}},
		{"reportsTo nobody's", gen.StaffGroupRule{Kind: gen.ReportsTo, Id: id(deep)}, []int64{}},
	}
	for _, c := range cases {
		got, err := staff.EvaluateRule(ctx, h.Deps(), c.rule, day)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if want := sortedIDs(c.want...); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", c.name, got, want)
		}
	}
}

// The materialisation writes a derived group's members as the difference
// from its rule today — the joiners from today, the leavers removed — each
// an event on the person by the system, and writes nothing when nothing
// changed. An archived group, and a listed one, are left as they stand.
func TestMaterialiseGroups_TheDifferenceAndItsEvents(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	hr, _ := signIn(t, h, "staff:manage")
	day := groupDay(t, today(t, h))
	past := day.AddDate(-1, 0, 0).Format(time.DateOnly)
	unit := seedUnit(t, h, "Unit")

	stays := seedPerson(t, h, "Stays", "On")
	seedPlacement(t, h, stays, placement{unit: unit, start: past})
	leaves := seedPerson(t, h, "Leaves", "Soon")
	leaving := seedPlacement(t, h, leaves, placement{unit: unit, start: past})

	g := createGroup(t, hr, derivedGroup("everyone", map[string]any{"kind": "employed"}))
	frozen := createGroup(t, hr, derivedGroup("frozen", map[string]any{"kind": "employed"}))
	do(t, hr, http.MethodPost, archivePath(groupsPath, frozen.Id), nil, http.StatusOK, nil)
	listed := createGroup(t, hr, listedGroup("hand-picked"))

	if err := staff.MaterialiseGroups(ctx, h.Deps(), day); err != nil {
		t.Fatal(err)
	}
	got := getGroup(t, hr, g.Id)
	if !slices.Equal(sortedIDs(memberIDs(got)...), sortedIDs(stays, leaves)) {
		t.Fatalf("members = %v, want %d and %d", memberIDs(got), stays, leaves)
	}
	for _, m := range *got.Members {
		if m.Source != gen.Derived || m.Since.Format(time.DateOnly) != day.Format(time.DateOnly) {
			t.Errorf("member %+v, want derived since today", m)
		}
	}
	if getGroup(t, hr, frozen.Id).MemberCount != 0 || getGroup(t, hr, listed.Id).MemberCount != 0 {
		t.Errorf("an archived or a listed group was materialised")
	}
	if e := groupEvents(t, h, stays); e != "group_joined:everyone:derived" {
		t.Errorf("events = %q, want one join", e)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'group_joined' AND actor_kind = 'system'`, stays); n != 1 {
		t.Errorf("the join is not the system's")
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'group_joined' AND payload_classes = '{C}' AND (payload->>'groupId')::bigint = $2 AND payload->>'groupName' = 'Group everyone'`, stays, g.Id); n != 1 {
		t.Errorf("the join's payload or classes are not the group's")
	}

	// Nothing changed: nothing written.
	if err := staff.MaterialiseGroups(ctx, h.Deps(), day); err != nil {
		t.Fatal(err)
	}
	if e := groupEvents(t, h, stays); e != "group_joined:everyone:derived" {
		t.Errorf("a second run: events = %q, want the one join", e)
	}

	// One leaves: the next day's run removes them.
	h.Exec(t, `UPDATE staff.employments SET end_date = $2::date, end_reason = 'contract_ended' WHERE id = $1`, leaving, day.Format(time.DateOnly))
	next := day.AddDate(0, 0, 1)
	if err := staff.MaterialiseGroups(ctx, h.Deps(), next); err != nil {
		t.Fatal(err)
	}
	if ids := memberIDs(getGroup(t, hr, g.Id)); !slices.Equal(ids, []int64{stays}) {
		t.Errorf("members = %v, want %d alone", ids, stays)
	}
	if e := groupEvents(t, h, leaves); e != "group_joined:everyone:derived,group_left:everyone:derived" {
		t.Errorf("events = %q, want the join and the leave", e)
	}
	if e := groupEvents(t, h, stays); e != "group_joined:everyone:derived" {
		t.Errorf("the one who stayed: events = %q, want the one join", e)
	}
}

// One group whose rule cannot be read does not hold the others.
func TestMaterialiseGroups_OneFailureDoesNotHoldTheRest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	hr, _ := signIn(t, h, "staff:manage")
	day := groupDay(t, today(t, h))
	person := seedPerson(t, h, "Kari", "Nordmann")
	seedPlacement(t, h, person, placement{unit: seedUnit(t, h, "Unit"), start: day.AddDate(0, -1, 0).Format(time.DateOnly)})

	broken := createGroup(t, hr, derivedGroup("broken", map[string]any{"kind": "employed"}))
	h.Exec(t, `UPDATE staff.groups SET rule = '{"kind":"nonsense"}' WHERE id = $1`, broken.Id)
	fine := createGroup(t, hr, derivedGroup("fine", map[string]any{"kind": "employed"}))
	if err := staff.MaterialiseGroups(ctx, h.Deps(), day); err != nil {
		t.Fatal(err)
	}
	if ids := memberIDs(getGroup(t, hr, fine.Id)); !slices.Equal(ids, []int64{person}) {
		t.Errorf("the fine group's members = %v, want %d", ids, person)
	}
}

// A listed group's members are exactly the people HR names, from today,
// each who joins or leaves an event by the caller; a person who does not
// exist or is erased is a field error and nothing is written; a duplicate
// counts once; a derived group's members are its rule's.
func TestGroupMembers_ListedPut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	a, b, c := seedPerson(t, h, "A", "Person"), seedPerson(t, h, "B", "Person"), seedPerson(t, h, "C", "Person")
	g := createGroup(t, hr, listedGroup("listed"))

	var out gen.StaffGroup
	do(t, hr, http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": []int64{b, a, a}}, http.StatusOK, &out)
	if out.MemberCount != 2 || !slices.Equal(sortedIDs(memberIDs(out)...), sortedIDs(a, b)) {
		t.Fatalf("members = %v (count %d), want %d and %d", memberIDs(out), out.MemberCount, a, b)
	}
	for _, m := range *out.Members {
		if m.Source != gen.Listed || m.Since.Format(time.DateOnly) != today(t, h) || m.DisplayName == "" || m.EmployeeNumber == "" {
			t.Errorf("member %+v, want listed since today, named and numbered", m)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'group_joined' AND actor_kind = 'user'`, a); n != 1 {
		t.Errorf("the join is not the caller's")
	}

	do(t, hr, http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": []int64{b, c}}, http.StatusOK, &out)
	if !slices.Equal(sortedIDs(memberIDs(out)...), sortedIDs(b, c)) {
		t.Fatalf("members = %v, want %d and %d", memberIDs(out), b, c)
	}
	if e := groupEvents(t, h, a); e != "group_joined:listed:listed,group_left:listed:listed" {
		t.Errorf("a's events = %q", e)
	}
	if e := groupEvents(t, h, b); e != "group_joined:listed:listed" {
		t.Errorf("b's events = %q, want the one join", e)
	}

	h.Exec(t, `UPDATE staff.people SET erased_at = $2 WHERE id = $1`, a, h.Now())
	for _, ids := range [][]int64{{b, 999999}, {b, a}} {
		p := refused(t, hr.Do(http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": ids}), http.StatusBadRequest, "")
		if _, ok := p.Errors["personIds"]; !ok {
			t.Errorf("%v: errors %v, want one on personIds", ids, p.Errors)
		}
	}
	if ids := memberIDs(getGroup(t, hr, g.Id)); !slices.Equal(sortedIDs(ids...), sortedIDs(b, c)) {
		t.Errorf("a refused write changed the members: %v", ids)
	}

	do(t, hr, http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": []int64{}}, http.StatusOK, &out)
	if out.MemberCount != 0 {
		t.Errorf("emptied: %d members", out.MemberCount)
	}

	derived := createGroup(t, hr, derivedGroup("derived", map[string]any{"kind": "employed"}))
	refused(t, hr.Do(http.MethodPut, groupMembersPath(derived.Id), map[string]any{"personIds": []int64{b}}), http.StatusConflict, "group_derived")
	if r := hr.Do(http.MethodPut, groupMembersPath(999999), map[string]any{"personIds": []int64{}}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown group: status %d, want 404", r.Status)
	}
}

// A group's owners are replaced whole, with no event, on a listed or a
// derived group alike, and make every provider's section due.
func TestGroupOwners_Put(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	a, b := seedPerson(t, h, "A", "Owner"), seedPerson(t, h, "B", "Owner")
	g := createGroup(t, hr, derivedGroup("owned", map[string]any{"kind": "employed"}))
	h.Exec(t, `INSERT INTO staff.group_traits (group_id, provider, values, state, next_attempt_at) VALUES ($1, 'entra', '{}', 'synced', NULL)`, g.Id)

	var out gen.StaffGroup
	do(t, hr, http.MethodPut, groupOwnersPath(g.Id), map[string]any{"personIds": []int64{a, b, a}}, http.StatusOK, &out)
	if out.OwnerCount != 2 || out.Owners == nil || len(*out.Owners) != 2 {
		t.Fatalf("owners = %v, want two", out.Owners)
	}
	do(t, hr, http.MethodPut, groupOwnersPath(g.Id), map[string]any{"personIds": []int64{b}}, http.StatusOK, &out)
	if out.OwnerCount != 1 || (*out.Owners)[0].PersonId != b {
		t.Fatalf("owners = %v, want %d alone", out.Owners, b)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id IN ($1, $2)`, a, b); n != 0 {
		t.Errorf("an owners' write recorded %d events, want none", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.group_traits WHERE group_id = $1 AND next_attempt_at IS NOT NULL`, g.Id); n != 1 {
		t.Errorf("the section was not made due")
	}
	p := refused(t, hr.Do(http.MethodPut, groupOwnersPath(g.Id), map[string]any{"personIds": []int64{999999}}), http.StatusBadRequest, "")
	if _, ok := p.Errors["personIds"]; !ok {
		t.Errorf("an unknown owner: errors %v, want one on personIds", p.Errors)
	}
	if r := hr.Do(http.MethodPut, groupOwnersPath(999999), map[string]any{"personIds": []int64{}}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown group: status %d, want 404", r.Status)
	}
}

// A group event in the history names the group and the source to the links
// group — the person, staff:manage and staff:personal-data, who read the
// Account tab's groups — and is the bare kind to the manager and a
// colleague.
func TestGroupEvents_TheHistoryPerAudience(t *testing.T) {
	t.Parallel()
	w := newAbsenceWorld(t)
	g := createGroup(t, w.hr, with(listedGroup("history"), "name", "History"))
	do(t, w.hr, http.MethodPut, groupMembersPath(g.Id), map[string]any{"personIds": []int64{w.kari.Id}}, http.StatusOK, nil)

	joined := func(c *modtest.Client) (gen.StaffEvent, bool) {
		for _, e := range eventsAs(t, c, w.kari.Id) {
			if e.Kind == "group_joined" {
				return e, true
			}
		}
		return gen.StaffEvent{}, false
	}
	for name, c := range map[string]*modtest.Client{"the person": w.kariC, "staff:manage": w.hr, "staff:personal-data": w.pd} {
		e, ok := joined(c)
		if !ok {
			t.Fatalf("%s: no group_joined in the history", name)
		}
		if e.Payload["groupName"] != "History" || e.Payload["groupKey"] != "history" || e.Payload["source"] != "listed" ||
			e.Payload["groupId"] != float64(g.Id) {
			t.Errorf("%s reads %v, want the group and the source", name, e.Payload)
		}
	}
	// staff:view alone reads no one's history (its 404); the manager reads
	// Kari's, the group events as their bare kind.
	if r := w.viewer.Do(http.MethodGet, pagedPath(eventsPath(w.kari.Id), 1, 100), nil); r.Status != http.StatusNotFound && strings.Contains(string(r.Body), "History") {
		t.Errorf("a colleague reads the group: %s", r.Body)
	}
	for name, c := range map[string]*modtest.Client{"the manager": w.bossC} {
		e, ok := joined(c)
		if !ok {
			t.Fatalf("%s: no group_joined in the history, want the bare kind", name)
		}
		if len(e.Payload) != 0 {
			t.Errorf("%s reads %v, want nothing", name, e.Payload)
		}
	}
}
