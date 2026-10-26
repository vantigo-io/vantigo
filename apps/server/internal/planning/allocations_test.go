package planning_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const allocationsPath = "/api/v1/planning/allocations"

func allocationPath(id int64) string { return allocationsPath + "/" + strconv.FormatInt(id, 10) }

// booking is an allocation's body: Kari at 50 % on the active project over
// the four weeks from Monday 5 October 2026, with what fields replace.
func booking(fields map[string]any) map[string]any {
	body := map[string]any{
		"personId": personKari, "projectId": projectActive,
		"fromDate": "2026-10-05", "toDate": "2026-10-30",
		"loadUnit": "percent", "load": 50,
	}
	maps.Copy(body, fields)
	for k, v := range body {
		if v == nil {
			delete(body, k)
		}
	}
	return body
}

// create books body and answers the written row, failing unless it is 201.
func create(t *testing.T, c *modtest.Client, body map[string]any) gen.PlanningAllocationWritten {
	t.Helper()
	res := c.Do(http.MethodPost, allocationsPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /allocations %v = %d %s, want 201", body, res.Status, res.Body)
	}
	var out gen.PlanningAllocationWritten
	res.JSON(&out)
	return out
}

// put replaces id at revision with body, answering the response.
func put(c *modtest.Client, id int64, revision int32, body map[string]any) *modtest.Response {
	return c.Do(http.MethodPut, allocationPath(id), body, ifMatch(revision))
}

// written decodes a 200 write, failing otherwise.
func written(t *testing.T, res *modtest.Response) gen.PlanningAllocationWritten {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("write = %d %s, want 200", res.Status, res.Body)
	}
	var out gen.PlanningAllocationWritten
	res.JSON(&out)
	return out
}

// problemOf decodes a refusal, failing unless its status is want.
func problemOf(t *testing.T, res *modtest.Response, want int) gen.PlanningProblem {
	t.Helper()
	if res.Status != want {
		t.Fatalf("status = %d %s, want %d", res.Status, res.Body, want)
	}
	var p gen.PlanningProblem
	res.JSON(&p)
	return p
}

// wantCode fails unless p carries code.
func wantCode(t *testing.T, p gen.PlanningProblem, code string) {
	t.Helper()
	if p.Code == nil || *p.Code != code {
		t.Errorf("problem = %+v, want code %s", p, code)
	}
}

// warningCodes is a write's warnings by code.
func warningCodes(w gen.PlanningAllocationWritten) map[string]gen.PlanningWarning {
	out := map[string]gen.PlanningWarning{}
	for _, warning := range w.Warnings {
		out[string(warning.Code)] = warning
	}
	return out
}

// eventKinds is the kinds of the events recorded about allocation id, in order.
func eventKinds(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return modtest.One[[]string](t, h.Harness,
		`SELECT coalesce(array_agg(kind ORDER BY id), '{}') FROM planning.events WHERE entity_kind = 'allocation' AND entity_id = $1`, id)
}

// eventPayload is the payload of the last event of kind about id.
func eventPayload(t *testing.T, h *harness, id int64, kind string) map[string]map[string]map[string]any {
	t.Helper()
	raw := modtest.One[string](t, h.Harness,
		`SELECT payload::text FROM planning.events WHERE entity_id = $1 AND kind = $2 ORDER BY id DESC LIMIT 1`, id, kind)
	var out map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	return out
}

// planner is a client holding planning:view and planning:manage.
func planner(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	c, _ := signIn(t, h, "planning:view", "planning:manage")
	return c
}

// linkUser gives personID a user of their own, whom the notices go to.
func linkUser(t *testing.T, h *harness, personID int64) uuid.UUID {
	t.Helper()
	_, user := signIn(t, h)
	h.workforce.link(user, personID)
	return user
}

// noticesTo is the notices sent to user.
func noticesTo(h *harness, user uuid.UUID) []contracts.Notice {
	var out []contracts.Notice
	for _, n := range h.Notices() {
		if n.UserID == user {
			out = append(out, n.Notice)
		}
	}
	return out
}

// TestAllocations_DefaultStatusByProject is S3: a row on a planned project
// is tentative unless the planner says otherwise, on any other confirmed;
// a confirmed one is stamped and its person told (amendment 5), a tentative
// one not; each is one allocation_created event naming its status.
func TestAllocations_DefaultStatusByProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	kari := linkUser(t, h, personKari)

	bid := create(t, c, booking(map[string]any{"projectId": projectPlanned}))
	if bid.Allocation.Status != gen.Tentative || bid.Allocation.ConfirmedAt != nil {
		t.Errorf("on a planned project = %+v, want tentative and not stamped", bid.Allocation)
	}
	if len(noticesTo(h, kari)) != 0 {
		t.Errorf("a tentative row mailed the person: %+v", h.Notices())
	}

	active := create(t, c, booking(nil))
	a := active.Allocation
	if a.Status != gen.Confirmed || a.ConfirmedAt == nil || a.Revision != 1 {
		t.Errorf("on an active project = %+v, want confirmed, stamped, revision 1", a)
	}
	if a.Person.DisplayName == nil || *a.Person.DisplayName != "Kari Nordmann" || a.Person.EmployeeNumber == nil || *a.Person.EmployeeNumber != "A-1001" {
		t.Errorf("person = %+v, want Kari named", a.Person)
	}
	if a.Project.Code == nil || *a.Project.Code != "KVEM1000" || a.Project.Status == nil || *a.Project.Status != gen.Active {
		t.Errorf("project = %+v, want KVEM1000, active", a.Project)
	}
	if len(active.Warnings) != 0 {
		t.Errorf("warnings = %+v, want none", active.Warnings)
	}
	if got := noticesTo(h, kari); len(got) != 1 {
		t.Fatalf("notices to Kari = %+v, want one", got)
	}

	if planned := create(t, c, booking(map[string]any{"projectId": projectPlanned, "status": "confirmed"})); planned.Allocation.Status != gen.Confirmed {
		t.Errorf("confirmed on a planned project = %s, want confirmed as asked", planned.Allocation.Status)
	}

	if kinds := eventKinds(t, h, a.Id); len(kinds) != 1 || kinds[0] != "allocation_created" {
		t.Errorf("events = %v, want allocation_created", kinds)
	}
	fields := eventPayload(t, h, a.Id, "allocation_created")["fields"]
	if fields["status"]["to"] != "confirmed" || fields["personId"]["to"] != float64(personKari) ||
		fields["fromDate"]["to"] != "2026-10-05" || fields["load"]["to"] != float64(50) || fields["loadUnit"]["to"] != "percent" {
		t.Errorf("allocation_created payload = %+v", fields)
	}
	if actor := modtest.One[string](t, h.Harness, `SELECT actor_kind FROM planning.events WHERE entity_id = $1`, a.Id); actor != "user" {
		t.Errorf("actor_kind = %s, want user", actor)
	}
}

// TestAllocations_Loads is S2: a percent and an hours row stored as typed,
// two decimals kept exactly.
func TestAllocations_Loads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)

	percent := create(t, c, booking(map[string]any{"load": 37.25}))
	if percent.Allocation.LoadUnit != gen.Percent || percent.Allocation.Load != 37.25 {
		t.Errorf("percent row = %s %v, want percent 37.25", percent.Allocation.LoadUnit, percent.Allocation.Load)
	}
	hours := create(t, c, booking(map[string]any{"personId": personOla, "loadUnit": "hours_per_week", "load": 22.5}))
	if hours.Allocation.LoadUnit != gen.HoursPerWeek || hours.Allocation.Load != 22.5 {
		t.Errorf("hours row = %s %v, want hours_per_week 22.5", hours.Allocation.LoadUnit, hours.Allocation.Load)
	}
	if stored := modtest.One[string](t, h.Harness, `SELECT load::text FROM planning.allocations WHERE id = $1`, percent.Allocation.Id); stored != "37.25" {
		t.Errorf("stored load = %s, want 37.25", stored)
	}
}

// TestAllocations_FieldErrors is §4.7's field errors on a create, each on its
// own field, period_too_long coded, nothing written by any.
func TestAllocations_FieldErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)

	cases := []struct {
		name   string
		fields map[string]any
		field  string
		code   string
	}{
		{"no person", map[string]any{"personId": nil}, "personId", ""},
		{"an unknown person", map[string]any{"personId": 1999}, "personId", ""},
		{"no project", map[string]any{"projectId": nil}, "projectId", ""},
		{"an unknown project", map[string]any{"projectId": projectUnknown}, "projectId", ""},
		{"to before from", map[string]any{"toDate": "2026-10-04"}, "toDate", ""},
		{"a period too long", map[string]any{"toDate": "2029-10-06"}, "toDate", "period_too_long"},
		{"a period wholly past the bound", map[string]any{"fromDate": "2026-01-05", "toDate": "2026-01-30"}, "fromDate", "window_out_of_bounds"},
		{"a load unit", map[string]any{"loadUnit": "days"}, "loadUnit", ""},
		{"no load", map[string]any{"load": 0}, "load", ""},
		{"a percent over 100", map[string]any{"load": 100.01}, "load", ""},
		{"hours over 80", map[string]any{"loadUnit": "hours_per_week", "load": 80.5}, "load", ""},
		{"three decimals", map[string]any{"load": 12.345}, "load", ""},
		{"a status", map[string]any{"status": "maybe"}, "status", ""},
		{"a long title", map[string]any{"title": strings.Repeat("x", 101)}, "title", ""},
		{"a long note", map[string]any{"note": strings.Repeat("x", 501)}, "note", ""},
		{"a property not taken", map[string]any{"requestId": 7}, "requestId", ""},
	}
	for _, tc := range cases {
		res := c.Do(http.MethodPost, allocationsPath, booking(tc.fields))
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", tc.name, res.Status, res.Body)
			continue
		}
		var p gen.PlanningProblem
		res.JSON(&p)
		if p.Errors == nil || len((*p.Errors)[tc.field]) == 0 {
			t.Errorf("%s: no field error on %s: %s", tc.name, tc.field, res.Body)
		}
		if tc.code != "" {
			wantCode(t, p, tc.code)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.allocations`); n != 0 {
		t.Errorf("%d rows written by refusals, want none", n)
	}
	if res := c.Do(http.MethodPost, allocationsPath, booking(map[string]any{"title": strings.Repeat("x", 100), "toDate": "2029-10-05"})); res.Status != http.StatusCreated {
		t.Errorf("a title of 100 over 1096 days = %d %s, want 201", res.Status, res.Body)
	}
}

// TestAllocations_OutsideEmployment is S4: a day of the period before Siri
// starts is the 409 outside_employment; from her first day it is written.
func TestAllocations_OutsideEmployment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)

	res := c.Do(http.MethodPost, allocationsPath, booking(map[string]any{"personId": personSiri, "fromDate": "2026-11-30", "toDate": "2026-12-11"}))
	wantCode(t, problemOf(t, res, http.StatusConflict), "outside_employment")
	create(t, c, booking(map[string]any{"personId": personSiri, "fromDate": "2026-12-01", "toDate": "2026-12-11"}))
}

// TestAllocations_ProjectClosed is S4 and amendment 6: a closed project takes
// no new row, no change and no confirm — but a deletion.
func TestAllocations_ProjectClosed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)

	res := c.Do(http.MethodPost, allocationsPath, booking(map[string]any{"projectId": projectCompleted}))
	wantCode(t, problemOf(t, res, http.StatusConflict), "project_closed")

	row := create(t, c, booking(nil)).Allocation
	res = put(c, row.Id, row.Revision, booking(map[string]any{"projectId": projectCompleted, "status": "confirmed"}))
	wantCode(t, problemOf(t, res, http.StatusConflict), "project_closed")

	draft := create(t, c, booking(map[string]any{"projectId": projectPlanned})).Allocation
	closed, _ := h.projects.Project(t.Context(), projectPlanned)
	closed.Status = "cancelled"
	h.projects.setProject(*closed)
	wantCode(t, problemOf(t, c.Do(http.MethodPost, allocationPath(draft.Id)+"/confirm", nil), http.StatusConflict), "project_closed")
	res = put(c, draft.Id, draft.Revision, booking(map[string]any{"projectId": projectPlanned, "status": "tentative", "title": "x"}))
	wantCode(t, problemOf(t, res, http.StatusConflict), "project_closed")

	if res := c.Do(http.MethodDelete, allocationPath(draft.Id), nil); res.Status != http.StatusNoContent {
		t.Errorf("DELETE on a cancelled project = %d %s, want 204", res.Status, res.Body)
	}
}

// TestAllocations_OutsideProjectDates is rule 6's warning: a period past the
// project's end is written with outside_project_dates; a project without
// dates warns of nothing.
func TestAllocations_OutsideProjectDates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)

	late := create(t, c, booking(map[string]any{"fromDate": "2027-06-28", "toDate": "2027-07-09"}))
	if w, ok := warningCodes(late)["outside_project_dates"]; !ok || w.Detail == nil {
		t.Errorf("warnings = %+v, want outside_project_dates with the project's dates", late.Warnings)
	}
	internal := create(t, c, booking(map[string]any{"projectId": projectInternal, "fromDate": "2027-06-28", "toDate": "2027-07-09"}))
	if len(internal.Warnings) != 0 {
		t.Errorf("a project without dates warned: %+v", internal.Warnings)
	}
}

// TestAllocations_Overbooking is S7: past capacity a write warns overbooked
// naming the week under warn; under refuse a write that grows the excess is
// the 409 and writes nothing, one that shrinks it is written with the
// warning, so the plan can be mended a row at a time.
func TestAllocations_Overbooking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	week := map[string]any{"fromDate": "2026-10-05", "toDate": "2026-10-09", "load": 60}

	create(t, c, booking(week))
	second := create(t, c, booking(week))
	w, ok := warningCodes(second)["overbooked"]
	if !ok || w.Weeks == nil || len(*w.Weeks) != 1 || (*w.Weeks)[0].String() != "2026-10-05" {
		t.Fatalf("warnings = %+v, want overbooked in the week of 2026-10-05", second.Warnings)
	}
	if tentative := create(t, c, booking(map[string]any{"fromDate": "2026-10-05", "toDate": "2026-10-09", "load": 60, "status": "tentative"})); len(tentative.Warnings) != 0 {
		t.Errorf("a tentative row warned: %+v — tentative rows weigh nothing (S3)", tentative.Warnings)
	}

	if res := c.Do(http.MethodPut, settingsPath, map[string]any{"overbooking": "refuse"}, ifMatch(1)); res.Status != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s", res.Status, res.Body)
	}
	before := h.Count(t, `SELECT count(*) FROM planning.allocations`)
	res := c.Do(http.MethodPost, allocationsPath, booking(map[string]any{"fromDate": "2026-10-05", "toDate": "2026-10-09", "load": 10}))
	p := problemOf(t, res, http.StatusConflict)
	wantCode(t, p, "overbooked")
	if p.Detail == nil || !strings.Contains(*p.Detail, "2026-10-05") {
		t.Errorf("detail = %v, want the week named", p.Detail)
	}
	if after := h.Count(t, `SELECT count(*) FROM planning.allocations`); after != before {
		t.Errorf("rows = %d after the refusal, want %d", after, before)
	}

	a := second.Allocation
	res = put(c, a.Id, a.Revision, booking(map[string]any{"fromDate": "2026-10-05", "toDate": "2026-10-09", "load": 50, "status": "confirmed"}))
	shrunk := written(t, res)
	if _, ok := warningCodes(shrunk)["overbooked"]; !ok {
		t.Errorf("a shrinking excess = %+v, want written with overbooked", shrunk.Warnings)
	}
	res = put(c, a.Id, shrunk.Allocation.Revision, booking(map[string]any{"fromDate": "2026-10-05", "toDate": "2026-10-09", "load": 70, "status": "confirmed"}))
	wantCode(t, problemOf(t, res, http.StatusConflict), "overbooked")
}

// TestAllocations_OverbookingConcurrent is S23's person lock under refuse:
// two planners booking Kari at 60 % into one week at once are judged one
// after the other, so exactly one is written and the other is overbooked.
func TestAllocations_OverbookingConcurrent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	if res := c.Do(http.MethodPut, settingsPath, map[string]any{"overbooking": "refuse"}, ifMatch(1)); res.Status != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s", res.Status, res.Body)
	}
	body := booking(map[string]any{"fromDate": "2026-10-05", "toDate": "2026-10-09", "load": 60, "status": "confirmed"})
	responses := make([]*modtest.Response, 2)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Go(func() { responses[i] = c.Do(http.MethodPost, allocationsPath, body) })
	}
	wg.Wait()

	created, refused := 0, 0
	for _, res := range responses {
		switch res.Status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			refused++
			wantCode(t, problemOf(t, res, http.StatusConflict), "overbooked")
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("concurrent bookings = %d created, %d refused (%d, %d); want one of each", created, refused, responses[0].Status, responses[1].Status)
	}
}

// TestAllocations_BookedWhileAway is S7's other warning: a week whose booked
// exceeds what is left once the days away are taken, but not capacity.
func TestAllocations_BookedWhileAway(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	h.workforce.setAway(personKari, contracts.DateSpan{From: date("2026-10-07"), To: date("2026-10-09")})

	out := create(t, c, booking(map[string]any{"fromDate": "2026-10-05", "toDate": "2026-10-16", "load": 100}))
	codes := warningCodes(out)
	w, ok := codes["booked_while_away"]
	if !ok || w.Weeks == nil || len(*w.Weeks) != 1 || (*w.Weeks)[0].String() != "2026-10-05" {
		t.Errorf("warnings = %+v, want booked_while_away in the week of 2026-10-05 alone", out.Warnings)
	}
	if _, over := codes["overbooked"]; over {
		t.Errorf("warnings = %+v, want no overbooked: being away is not over capacity", out.Warnings)
	}
}

// TestAllocations_PutRevision is rule 8: PUT takes If-Match — one that is no
// revision is a 400, a stale one the 409 revision_conflict — and a field
// left out is cleared.
func TestAllocations_PutRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	row := create(t, c, booking(map[string]any{"title": "Utvikler", "note": "Ønsker backend"})).Allocation

	if res := c.Do(http.MethodPut, allocationPath(row.Id), booking(map[string]any{"status": "confirmed"}), modtest.Header("If-Match", "abc")); res.Status != http.StatusBadRequest {
		t.Errorf("an If-Match that is no revision = %d, want 400", res.Status)
	}
	wantCode(t, problemOf(t, put(c, row.Id, row.Revision+1, booking(map[string]any{"status": "confirmed"})), http.StatusConflict), "revision_conflict")
	if res := put(c, row.Id, row.Revision, booking(nil)); res.Status != http.StatusBadRequest {
		t.Errorf("a PUT without status = %d, want 400: the replace is whole", res.Status)
	}
	if res := put(c, 999999, 1, booking(map[string]any{"status": "confirmed"})); res.Status != http.StatusNotFound {
		t.Errorf("PUT of an unknown id = %d, want 404", res.Status)
	}

	out := written(t, put(c, row.Id, row.Revision, booking(map[string]any{"status": "confirmed", "load": 40})))
	if out.Allocation.Revision != row.Revision+1 || out.Allocation.Title != nil || out.Allocation.Note != nil || out.Allocation.Load != 40 {
		t.Errorf("replaced = %+v, want revision 2, title and note cleared, load 40", out.Allocation)
	}
	wantCode(t, problemOf(t, put(c, row.Id, row.Revision, booking(map[string]any{"status": "confirmed"})), http.StatusConflict), "revision_conflict")

	fields := eventPayload(t, h, row.Id, "allocation_changed")["fields"]
	if _, ok := fields["note"]; !ok || len(fields["note"]) != 0 {
		t.Errorf("the note's change = %+v, want named with neither text", fields["note"])
	}
	if fields["title"]["from"] != "Utvikler" || fields["load"]["from"] != float64(50) || fields["load"]["to"] != float64(40) {
		t.Errorf("allocation_changed payload = %+v", fields)
	}
	if _, ok := fields["status"]; ok {
		t.Errorf("payload = %+v: the status did not change", fields)
	}

	same := written(t, put(c, row.Id, out.Allocation.Revision, booking(map[string]any{"status": "confirmed", "load": 40})))
	if same.Allocation.Revision != out.Allocation.Revision {
		t.Errorf("a replace that changes nothing moved the revision to %d", same.Allocation.Revision)
	}
}

// TestAllocations_PutMovesPersonAndProject is S23's move: a confirmed row
// moved to another person and project under both projects' and persons'
// locks; the one it left is told it is removed, the one it reached that they
// are planned.
func TestAllocations_PutMovesPersonAndProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	kari, ola := linkUser(t, h, personKari), linkUser(t, h, personOla)
	row := create(t, c, booking(nil)).Allocation

	out := written(t, put(c, row.Id, row.Revision, booking(map[string]any{"personId": personOla, "projectId": projectInternal, "status": "confirmed"})))
	if out.Allocation.Person.Id != personOla || out.Allocation.Project.Id != projectInternal {
		t.Errorf("moved = %+v, want Ola on the internal project", out.Allocation)
	}
	fields := eventPayload(t, h, row.Id, "allocation_changed")["fields"]
	if fields["personId"]["from"] != float64(personKari) || fields["personId"]["to"] != float64(personOla) ||
		fields["projectId"]["from"] != float64(projectActive) || fields["projectId"]["to"] != float64(projectInternal) {
		t.Errorf("allocation_changed payload = %+v", fields)
	}
	if got := noticesTo(h, kari); len(got) != 2 || !strings.Contains(got[1].Texts["en"].Body, "no longer planned on KVEM1000") {
		t.Errorf("notices to Kari = %+v, want planned then removed", got)
	}
	if got := noticesTo(h, ola); len(got) != 1 || !strings.Contains(got[0].Texts["en"].Body, "You are planned on INTERN Internt arbeid") {
		t.Errorf("notices to Ola = %+v, want planned on INTERN", got)
	}
}

// TestAllocations_StatusFlips is S3 and amendment 5 through PUT: set
// confirmed is allocation_confirmed and a "planned" mail, set tentative
// allocation_unconfirmed and a "no longer confirmed" mail, a confirmed row's
// load changed a "changed" mail; a tentative row's change and a confirmed
// row's title tell nobody.
func TestAllocations_StatusFlips(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	kari := linkUser(t, h, personKari)
	row := create(t, c, booking(map[string]any{"projectId": projectPlanned})).Allocation
	row = written(t, put(c, row.Id, row.Revision, booking(map[string]any{"projectId": projectPlanned, "status": "tentative", "load": 40}))).Allocation
	if len(noticesTo(h, kari)) != 0 {
		t.Fatalf("a tentative row's change mailed: %+v", h.Notices())
	}

	row = written(t, put(c, row.Id, row.Revision, booking(map[string]any{"projectId": projectPlanned, "status": "confirmed", "load": 40}))).Allocation
	if row.Status != gen.Confirmed || row.ConfirmedAt == nil {
		t.Errorf("set confirmed = %+v", row)
	}
	row = written(t, put(c, row.Id, row.Revision, booking(map[string]any{"projectId": projectPlanned, "status": "confirmed", "load": 40, "title": "Arkitekt"}))).Allocation
	row = written(t, put(c, row.Id, row.Revision, booking(map[string]any{"projectId": projectPlanned, "status": "confirmed", "load": 30, "title": "Arkitekt"}))).Allocation
	row = written(t, put(c, row.Id, row.Revision, booking(map[string]any{"projectId": projectPlanned, "status": "tentative", "load": 30, "title": "Arkitekt"}))).Allocation
	if row.Status != gen.Tentative || row.ConfirmedAt != nil {
		t.Errorf("set tentative = %+v, want tentative and the stamp cleared", row)
	}

	want := []string{"allocation_created", "allocation_changed", "allocation_confirmed", "allocation_changed", "allocation_changed", "allocation_unconfirmed"}
	if kinds := eventKinds(t, h, row.Id); strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", kinds, want)
	}
	if fields := eventPayload(t, h, row.Id, "allocation_unconfirmed")["fields"]; fields["status"]["from"] != "confirmed" || fields["status"]["to"] != "tentative" {
		t.Errorf("allocation_unconfirmed payload = %+v", fields)
	}
	got := noticesTo(h, kari)
	if len(got) != 3 {
		t.Fatalf("notices = %+v, want planned, changed, no longer confirmed", got)
	}
	for i, want := range []string{"You are planned on", "Your planning has changed", "is no longer confirmed"} {
		if !strings.Contains(got[i].Texts["en"].Body, want) {
			t.Errorf("notice %d = %q, want %q", i, got[i].Texts["en"].Body, want)
		}
	}
}

// TestAllocations_Confirm is S3 and amendment 6: confirm stamps a tentative
// row, records allocation_confirmed and tells the person; on a confirmed
// row it answers the row as it is with no event and no mail.
func TestAllocations_Confirm(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	kari := linkUser(t, h, personKari)
	row := create(t, c, booking(map[string]any{"projectId": projectPlanned})).Allocation

	first := written(t, c.Do(http.MethodPost, allocationPath(row.Id)+"/confirm", nil))
	if first.Allocation.Status != gen.Confirmed || first.Allocation.ConfirmedAt == nil || first.Allocation.Revision != 2 {
		t.Errorf("confirmed = %+v", first.Allocation)
	}
	again := written(t, c.Do(http.MethodPost, allocationPath(row.Id)+"/confirm", nil))
	if again.Allocation.Revision != 2 || len(again.Warnings) != 0 {
		t.Errorf("confirmed again = %+v, want the row as it is", again)
	}
	if kinds := eventKinds(t, h, row.Id); strings.Join(kinds, ",") != "allocation_created,allocation_confirmed" {
		t.Errorf("events = %v, want created then confirmed once", kinds)
	}
	if got := noticesTo(h, kari); len(got) != 1 {
		t.Errorf("notices = %+v, want one", got)
	}
	if res := c.Do(http.MethodPost, allocationPath(999999)+"/confirm", nil); res.Status != http.StatusNotFound {
		t.Errorf("confirm of an unknown id = %d, want 404", res.Status)
	}

	// Under refuse, confirming into an excess is the 409.
	create(t, c, booking(map[string]any{"load": 100}))
	draft := create(t, c, booking(map[string]any{"projectId": projectPlanned, "load": 10})).Allocation
	if res := c.Do(http.MethodPut, settingsPath, map[string]any{"overbooking": "refuse"}, ifMatch(1)); res.Status != http.StatusOK {
		t.Fatalf("PUT /settings = %d", res.Status)
	}
	wantCode(t, problemOf(t, c.Do(http.MethodPost, allocationPath(draft.Id)+"/confirm", nil), http.StatusConflict), "overbooked")
}

// TestAllocations_Delete is DELETE: 204 and then 404, the row's fields in
// allocation_deleted, and the person told when it was confirmed.
func TestAllocations_Delete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	kari := linkUser(t, h, personKari)
	row := create(t, c, booking(map[string]any{"title": "Utvikler"})).Allocation
	draft := create(t, c, booking(map[string]any{"projectId": projectPlanned})).Allocation

	for _, id := range []int64{row.Id, draft.Id} {
		if res := c.Do(http.MethodDelete, allocationPath(id), nil); res.Status != http.StatusNoContent {
			t.Fatalf("DELETE = %d %s, want 204", res.Status, res.Body)
		}
	}
	if res := c.Do(http.MethodDelete, allocationPath(row.Id), nil); res.Status != http.StatusNotFound {
		t.Errorf("DELETE again = %d, want 404", res.Status)
	}
	if res := c.Do(http.MethodGet, allocationPath(row.Id), nil); res.Status != http.StatusNotFound {
		t.Errorf("GET after DELETE = %d, want 404", res.Status)
	}
	fields := eventPayload(t, h, row.Id, "allocation_deleted")["fields"]
	if fields["title"]["from"] != "Utvikler" || fields["status"]["from"] != "confirmed" || fields["projectId"]["from"] != float64(projectActive) {
		t.Errorf("allocation_deleted payload = %+v", fields)
	}
	got := noticesTo(h, kari)
	if len(got) != 2 || !strings.Contains(got[1].Texts["en"].Subject, "is removed") {
		t.Errorf("notices = %+v, want planned and removed for the confirmed row alone", got)
	}
}

// TestAllocations_StaffRefusesTheWindow is a write whose window Staff refuses
// as outside its bound — its today a day on from Planning's, at midnight:
// a create, a delete of a confirmed row and a confirm each answer the 400
// window_out_of_bounds, not a fault, and nothing is written.
func TestAllocations_StaffRefusesTheWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	row := create(t, c, booking(nil)).Allocation
	draft := create(t, c, booking(map[string]any{"projectId": projectPlanned})).Allocation
	h.workforce.setRefuseWindow(true)

	for _, res := range []*modtest.Response{
		c.Do(http.MethodPost, allocationsPath, booking(nil)),
		c.Do(http.MethodDelete, allocationPath(row.Id), nil),
		c.Do(http.MethodPost, allocationPath(draft.Id)+"/confirm", nil),
	} {
		wantCode(t, problemOf(t, res, http.StatusBadRequest), "window_out_of_bounds")
	}
	if got := h.Count(t, `SELECT count(*) FROM planning.allocations WHERE status = 'confirmed'`); got != 1 {
		t.Errorf("confirmed rows = %d after the refusals, want the one", got)
	}
}

// TestAllocations_Mail is S19's mail: both languages, the project's code and
// name, the dates and the load, the link to My planning — and never the note;
// a person with no user is not mailed and the write succeeds.
func TestAllocations_Mail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	kari := linkUser(t, h, personKari)

	create(t, c, booking(map[string]any{"note": "Hemmelig notat", "loadUnit": "hours_per_week", "load": 22.5}))
	got := noticesTo(h, kari)
	if len(got) != 1 {
		t.Fatalf("notices = %+v, want one", got)
	}
	en, nb := got[0].Texts["en"], got[0].Texts["nb"]
	if en.Subject != "You are planned on KVEM1000 Kraft-Verket modernisering" ||
		!strings.HasPrefix(en.Body, "You are planned on KVEM1000 Kraft-Verket modernisering from 5 October 2026 to 30 October 2026 (22.5 hours a week).") ||
		!strings.Contains(en.Body, "Open My planning: http://modules.example.com/planning/me") {
		t.Errorf("en = %+v", en)
	}
	if nb.Subject != "Du er planlagt på KVEM1000 Kraft-Verket modernisering" ||
		!strings.HasPrefix(nb.Body, "Du er planlagt på KVEM1000 Kraft-Verket modernisering fra 5. oktober 2026 til 30. oktober 2026 (22,5 timer i uka).") ||
		!strings.Contains(nb.Body, "Åpne Min bemanning: http://modules.example.com/planning/me") {
		t.Errorf("nb = %+v", nb)
	}
	for _, text := range []contracts.NoticeText{en, nb} {
		if strings.Contains(text.Subject+text.Body, "Hemmelig") {
			t.Errorf("the note leaked into the mail: %+v", text)
		}
	}

	before := len(h.Notices())
	create(t, c, booking(map[string]any{"personId": personOla}))
	if after := len(h.Notices()); after != before {
		t.Errorf("a person with no user was mailed: %+v", h.Notices()[before:])
	}
}

// TestAllocations_Reads is GET /allocations and GET /allocations/{id}: the
// filters, the order, the note to planning:manage alone, and a person Staff
// no longer answers as an id with displayName null.
func TestAllocations_Reads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	viewer, _ := signIn(t, h, "planning:view")

	late := create(t, c, booking(map[string]any{"fromDate": "2026-11-02", "toDate": "2026-11-27", "note": "Notat"})).Allocation
	early := create(t, c, booking(map[string]any{"personId": personOla, "projectId": projectPlanned})).Allocation
	h.Exec(t, `INSERT INTO planning.allocations (person_id, project_id, from_date, to_date, load_unit, load, status, created_at, updated_at)
		VALUES (1999, $1, '2026-12-07', '2026-12-11', 'percent', 20, 'confirmed', now(), now())`, projectUnknown)

	var list gen.PlanningAllocationList
	res := viewer.Do(http.MethodGet, allocationsPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /allocations = %d %s", res.Status, res.Body)
	}
	res.JSON(&list)
	if len(list.Allocations) != 3 || list.Allocations[0].Id != early.Id || list.Allocations[1].Id != late.Id {
		t.Fatalf("list = %+v, want three by from date", list.Allocations)
	}
	for _, a := range list.Allocations {
		if a.Note != nil {
			t.Errorf("planning:view read the note of %d", a.Id)
		}
	}
	erased := list.Allocations[2]
	if erased.Person.Id != 1999 || erased.Person.DisplayName != nil || erased.Person.EmployeeNumber != nil || erased.Project.Code != nil {
		t.Errorf("an unknown person and project = %+v, want ids alone", erased)
	}
	if !strings.Contains(string(res.Body), `"displayName":null`) {
		t.Errorf("body %s: want displayName null for the unknown person", res.Body)
	}

	for query, want := range map[string]int{
		"?personId=1002": 1, "?projectId=2001": 1, "?status=tentative": 1,
		"?from=2026-11-01&to=2026-11-30": 1, "?from=2026-12-01": 1,
	} {
		var got gen.PlanningAllocationList
		viewer.Do(http.MethodGet, allocationsPath+query, nil).JSON(&got)
		if len(got.Allocations) != want {
			t.Errorf("GET /allocations%s = %d rows, want %d", query, len(got.Allocations), want)
		}
	}
	if res := viewer.Do(http.MethodGet, allocationsPath+"?from=2026-11-30&to=2026-11-01", nil); res.Status != http.StatusBadRequest {
		t.Errorf("to before from = %d, want 400", res.Status)
	}

	var one gen.PlanningAllocation
	res = c.Do(http.MethodGet, allocationPath(late.Id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /allocations/{id} = %d %s", res.Status, res.Body)
	}
	res.JSON(&one)
	if one.Note == nil || *one.Note != "Notat" || one.Person.DisplayName == nil {
		t.Errorf("to planning:manage = %+v, want the note and the name", one)
	}
	var seen gen.PlanningAllocation
	viewer.Do(http.MethodGet, allocationPath(late.Id), nil).JSON(&seen)
	if seen.Note != nil {
		t.Errorf("planning:view read the note: %+v", seen)
	}
	if res := viewer.Do(http.MethodGet, allocationPath(999999), nil); res.Status != http.StatusNotFound {
		t.Errorf("GET of an unknown id = %d, want 404", res.Status)
	}
}

// TestAllocations_Permissions is the contract's access rules: planning:view
// reads but never writes, planning:access alone reads no allocation.
func TestAllocations_Permissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	row := create(t, planner(t, h), booking(nil)).Allocation
	viewer, _ := signIn(t, h, "planning:view")
	employee, _ := signIn(t, h)

	for name, res := range map[string]*modtest.Response{
		"POST with view":    viewer.Do(http.MethodPost, allocationsPath, booking(nil)),
		"PUT with view":     put(viewer, row.Id, row.Revision, booking(map[string]any{"status": "confirmed"})),
		"DELETE with view":  viewer.Do(http.MethodDelete, allocationPath(row.Id), nil),
		"confirm with view": viewer.Do(http.MethodPost, allocationPath(row.Id)+"/confirm", nil),
		"list with access":  employee.Do(http.MethodGet, allocationsPath, nil),
		"get with access":   employee.Do(http.MethodGet, allocationPath(row.Id), nil),
	} {
		if res.Status != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, res.Status)
		}
	}
}
