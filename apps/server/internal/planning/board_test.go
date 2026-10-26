package planning_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// The harness clock starts on Saturday 12 September 2026 (modtest.Start), so
// "this week" is the week of Monday 7 September and the board's default
// window runs to Sunday 29 November.
const (
	thisMonday = "2026-09-07"
	week38     = "2026-09-14"
	week39     = "2026-09-21"
)

// seedRow is one allocation a read test plans against, written straight
// into planning.allocations: the reads do not depend on the write lane.
type seedRow struct {
	person   int64
	project  int32
	from, to string
	unit     string // "percent" (the default) or "hours_per_week"
	load     float64
	status   string // "confirmed" (the default) or "tentative"
	note     string
}

// seedAllocation writes r and answers its id.
func seedAllocation(t *testing.T, h *harness, r seedRow) int64 {
	t.Helper()
	if r.unit == "" {
		r.unit = "percent"
	}
	if r.status == "" {
		r.status = "confirmed"
	}
	var note *string
	if r.note != "" {
		note = &r.note
	}
	return modtest.One[int64](t, h.Harness, `INSERT INTO planning.allocations
    (person_id, project_id, from_date, to_date, load_unit, load, status, note, confirmed_at, created_at, updated_at)
VALUES ($1, $2, $3::date, $4::date, $5, $6, $7::varchar, $8,
    CASE WHEN $7::varchar = 'confirmed' THEN $9::timestamptz END, $9::timestamptz, $9::timestamptz)
RETURNING id`, r.person, r.project, date(r.from), date(r.to), r.unit, r.load, r.status, note, h.Now())
}

// getJSON is a GET that must answer want, decoded into v when v is not nil.
func getJSON(t *testing.T, c *modtest.Client, path string, want int, v any) *modtest.Response {
	t.Helper()
	res := c.Do(http.MethodGet, path, nil)
	if res.Status != want {
		t.Fatalf("GET %s = %d %s, want %d", path, res.Status, res.Body, want)
	}
	if v != nil {
		res.JSON(v)
	}
	return res
}

// rowOf is the board row of personID, failing the test when there is none.
func rowOf(t *testing.T, b gen.PlanningBoard, personID int64) gen.PlanningBoardRow {
	t.Helper()
	for _, r := range b.Rows {
		if r.Person != nil && r.Person.Id == personID && r.Project == nil {
			return r
		}
	}
	t.Fatalf("no row for person %d in %+v", personID, b.Rows)
	return gen.PlanningBoardRow{}
}

// cellOf is the row's cell of the week of monday.
func cellOf(t *testing.T, r gen.PlanningBoardRow, monday string) gen.PlanningWeekFigures {
	t.Helper()
	for _, c := range r.Cells {
		if c.Monday.String() == monday {
			return c
		}
	}
	t.Fatalf("no cell for %s in %+v", monday, r.Cells)
	return gen.PlanningWeekFigures{}
}

// allocationIDs is the ids of allocations, in order.
func allocationIDs(allocations []gen.PlanningAllocation) []int64 {
	ids := make([]int64, len(allocations))
	for i, a := range allocations {
		ids[i] = a.Id
	}
	return ids
}

const boardPath = "/api/v1/planning/board"

// TestBoard_Figures is S10 over S6: Kari at 37.5 hours under a 50 % row books
// 18.75 and is half loaded; Ola's tentative row is summed apart, not
// booked; a day away makes the week away (amendment 10) with its available
// hours less; the default window is this week and the eleven after it.
func TestBoard_Figures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	kari := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-27", load: 50})
	ola := seedAllocation(t, h, seedRow{person: personOla, project: projectPlanned, from: week38, to: "2026-09-20", load: 100, status: "tentative"})
	h.workforce.setAway(personKari, contracts.DateSpan{From: date("2026-09-16"), To: date("2026-09-16")})

	var b gen.PlanningBoard
	getJSON(t, c, boardPath, http.StatusOK, &b)
	if b.From.String() != thisMonday || b.To.String() != "2026-11-29" || len(b.Weeks) != 12 || b.Weeks[0].Monday.String() != thisMonday {
		t.Errorf("window = %s..%s, %d weeks; want this week and the eleven after it", b.From, b.To, len(b.Weeks))
	}
	if len(b.Rows) != 2 {
		t.Fatalf("rows = %+v, want Kari and Ola (Siri starts in December)", b.Rows)
	}
	k := cellOf(t, rowOf(t, b, personKari), week38)
	if k.Capacity != 37.5 || k.Booked != 18.75 || k.Available != 30 || k.Free != 11.25 || k.LoadPercent == nil || *k.LoadPercent != 50 || !k.Away {
		t.Errorf("Kari's week 38 = %+v, want 37.5 capacity, 18.75 booked, 30 available, 11.25 free, 50 %%, away", k)
	}
	if k39 := cellOf(t, rowOf(t, b, personKari), week39); k39.Away || k39.Available != 37.5 || k39.Booked != 18.75 {
		t.Errorf("Kari's week 39 = %+v, want not away, 18.75 booked of 37.5", k39)
	}
	o := cellOf(t, rowOf(t, b, personOla), week38)
	if o.Booked != 0 || o.Tentative != 30 || o.Free != 30 || o.LoadPercent == nil || *o.LoadPercent != 0 {
		t.Errorf("Ola's week 38 = %+v, want the tentative 30 hours apart from booked", o)
	}
	if ids := allocationIDs(b.Allocations); len(ids) != 2 || !containsID(ids, kari) || !containsID(ids, ola) {
		t.Errorf("allocations = %v, want both rows", ids)
	}
	for _, a := range b.Allocations {
		if a.Person.DisplayName == nil || a.Project.Code == nil {
			t.Errorf("allocation %d = %+v, want the person and the project named", a.Id, a)
		}
	}
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// TestBoard_Switches is includeTentative and includePlanned (S10): without
// tentative rows Ola's week sums nothing and lists nothing; without planned
// projects Kari's confirmed row on bid 2002 drops out of her figures and the
// bars, her row on the active project stays.
func TestBoard_Switches(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	active := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-20", load: 50})
	bid := seedAllocation(t, h, seedRow{person: personKari, project: projectPlanned, from: week38, to: "2026-09-20", unit: "hours_per_week", load: 10})
	tentative := seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: week38, to: "2026-09-20", load: 100, status: "tentative"})

	var b gen.PlanningBoard
	getJSON(t, c, boardPath+"?includeTentative=false", http.StatusOK, &b)
	if o := cellOf(t, rowOf(t, b, personOla), week38); o.Tentative != 0 {
		t.Errorf("Ola without tentative rows = %+v, want 0 tentative", o)
	}
	if containsID(allocationIDs(b.Allocations), tentative) {
		t.Errorf("allocations without tentative rows = %v, want the tentative row left out", allocationIDs(b.Allocations))
	}
	if k := cellOf(t, rowOf(t, b, personKari), week38); k.Booked != 28.75 {
		t.Errorf("Kari with the bid = %+v, want 18.75 + 10 booked", k)
	}

	getJSON(t, c, boardPath+"?includePlanned=false", http.StatusOK, &b)
	if k := cellOf(t, rowOf(t, b, personKari), week38); k.Booked != 18.75 {
		t.Errorf("Kari without planned projects = %+v, want 18.75 booked", k)
	}
	ids := allocationIDs(b.Allocations)
	if containsID(ids, bid) || !containsID(ids, active) || !containsID(ids, tentative) {
		t.Errorf("allocations without planned projects = %v, want the bid's row alone left out", ids)
	}
}

// TestBoard_GroupByProject is groupBy=project: a row per project by code
// with the sums of its people's rows of it, each followed by its people.
func TestBoard_GroupByProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-20", load: 50})
	seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: week38, to: "2026-09-20", unit: "hours_per_week", load: 10})
	seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: week38, to: "2026-09-20", load: 20})

	var b gen.PlanningBoard
	getJSON(t, c, boardPath+"?groupBy=project", http.StatusOK, &b)
	// INTERN sorts before KVEM1000.
	if len(b.Rows) != 5 {
		t.Fatalf("rows = %d, want INTERN with Kari, KVEM1000 with Kari and Ola", len(b.Rows))
	}
	intern, kvem := b.Rows[0], b.Rows[2]
	if intern.Project == nil || intern.Project.Code != "INTERN" || intern.Person != nil || b.Rows[1].Person == nil || b.Rows[1].Person.Id != personKari {
		t.Errorf("first rows = %+v, %+v; want INTERN then Kari under it", intern, b.Rows[1])
	}
	if kvem.Project == nil || kvem.Project.Code != "KVEM1000" || kvem.Project.Customer == nil || kvem.Project.Customer.Name == nil || *kvem.Project.Customer.Name != "Kraft-Verket AS" {
		t.Errorf("KVEM1000's row = %+v, want it with its customer named", kvem.Project)
	}
	if w := cellOf(t, kvem, week38); w.Booked != 28.75 || w.Capacity != 67.5 {
		t.Errorf("KVEM1000's week 38 = %+v, want 18.75 + 10 booked over 37.5 + 30", w)
	}
	if w := cellOf(t, b.Rows[3], week38); b.Rows[3].Person.Id != personKari || w.Booked != 18.75 {
		t.Errorf("Kari under KVEM1000 = %+v, want her 18.75 on it alone", w)
	}
}

// TestBoard_Filters is the department, competence and project filters
// (S10): Siri, who has no department, is left out by one; the competence
// keeps the people holding it on the later of from and today (amendment 33)
// — Kari, certified from today, is kept on a board that begins last Monday;
// the project keeps its rows and the people on it.
func TestBoard_Filters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	h.workforce.setHeld(personOla, contracts.HeldCompetence{CompetenceID: 7001, Key: "go", Kind: "skill", ValidFrom: date("2025-01-01")})
	h.workforce.setHeld(personKari, contracts.HeldCompetence{CompetenceID: 7001, Key: "go", Kind: "skill", ValidFrom: date("2026-09-12")})
	onActive := seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: week38, to: "2026-09-20", load: 50})
	seedAllocation(t, h, seedRow{person: personOla, project: projectInternal, from: week38, to: "2026-09-20", load: 50})

	december := boardPath + "?from=2026-11-30&to=2026-12-13"
	var b gen.PlanningBoard
	getJSON(t, c, december, http.StatusOK, &b)
	if len(b.Rows) != 3 {
		t.Errorf("December = %d rows, want Kari, Ola and Siri", len(b.Rows))
	}
	getJSON(t, c, december+"&departmentId=4001", http.StatusOK, &b)
	if len(b.Rows) != 2 || b.Rows[0].Person.Department == nil || b.Rows[0].Person.Department.Name != "Leveranse" {
		t.Errorf("December in Leveranse = %+v, want Kari and Ola with their department", b.Rows)
	}

	getJSON(t, c, boardPath+"?competenceId=7001", http.StatusOK, &b)
	if len(b.Rows) != 2 || b.Rows[0].Person.Id != personKari || b.Rows[1].Person.Id != personOla {
		t.Errorf("holding competence 7001 = %+v, want Kari, held from today, and Ola", b.Rows)
	}

	getJSON(t, c, boardPath+"?projectId=2001", http.StatusOK, &b)
	if len(b.Rows) != 1 || b.Rows[0].Person.Id != personOla || len(b.Allocations) != 1 || b.Allocations[0].Id != onActive {
		t.Errorf("project 2001 = %d rows, %v; want Ola and her row on it", len(b.Rows), allocationIDs(b.Allocations))
	}
	if w := cellOf(t, b.Rows[0], week38); w.Booked != 15 {
		t.Errorf("Ola on project 2001 = %+v, want only that row's 15 hours", w)
	}
}

// TestBoard_Window is the window's refusals: to before from, more than 53
// weeks, and a window Staff refuses as outside its bound, which is
// window_out_of_bounds on the side that is out (amendment 8).
func TestBoard_Window(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")

	var p gen.PlanningProblem
	getJSON(t, c, boardPath+"?from=2026-10-05&to=2026-10-04", http.StatusBadRequest, &p)
	if p.Errors == nil || len((*p.Errors)["to"]) != 1 || p.Code != nil {
		t.Errorf("to before from = %+v, want a field error on to", p)
	}
	getJSON(t, c, boardPath+"?from=2026-09-07&to=2027-09-13", http.StatusBadRequest, &p)
	if p.Errors == nil || len((*p.Errors)["to"]) != 1 {
		t.Errorf("54 weeks = %+v, want a field error on to", p)
	}
	getJSON(t, c, boardPath+"?from=2026-09-07&to=2027-09-12", http.StatusOK, nil)

	h.workforce.setRefuseWindow(true)
	p = gen.PlanningProblem{}
	getJSON(t, c, boardPath+"?from=2026-01-05&to=2026-02-01", http.StatusBadRequest, &p)
	if p.Code == nil || *p.Code != "window_out_of_bounds" || p.Errors == nil || len((*p.Errors)["from"]) != 1 {
		t.Errorf("a window Staff refuses = %+v, want window_out_of_bounds on from", p)
	}
}

// TestBoard_Audiences is §5: planning:access alone is the router's 403; the
// note is planning:manage's.
func TestBoard_Audiences(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-20", load: 50, note: "Kunden vil ha Kari"})

	getJSON(t, h.SignIn(t, "planning:access"), boardPath, http.StatusForbidden, nil)

	var b gen.PlanningBoard
	getJSON(t, h.SignIn(t, "planning:access", "planning:view"), boardPath, http.StatusOK, &b)
	if len(b.Allocations) != 1 || b.Allocations[0].Note != nil {
		t.Errorf("planning:view's allocations = %+v, want the row without its note", b.Allocations)
	}
	getJSON(t, h.SignIn(t, "planning:access", "planning:view", "planning:manage"), boardPath, http.StatusOK, &b)
	if len(b.Allocations) != 1 || b.Allocations[0].Note == nil || *b.Allocations[0].Note != "Kunden vil ha Kari" {
		t.Errorf("planning:manage's allocations = %+v, want the note", b.Allocations)
	}
}
