package planning_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// TestProjectPlan_Weeks is S11: the project, its rows in the window, and per
// week its confirmed and tentative hours over each person's capacity — a
// row on another project not counted — and no demand without an open
// request.
func TestProjectPlan_Weeks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	kari := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-27", load: 50})
	ola := seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: week38, to: "2026-09-20", unit: "hours_per_week", load: 10, status: "tentative"})
	seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: week38, to: "2026-09-27", load: 50})

	var plan gen.PlanningProjectPlan
	getJSON(t, c, "/api/v1/planning/projects/2001/plan?from=2026-09-14&to=2026-09-27", http.StatusOK, &plan)
	if plan.Project.Code != "KVEM1000" || plan.Project.Customer == nil {
		t.Errorf("project = %+v, want KVEM1000 with its customer", plan.Project)
	}
	if ids := allocationIDs(plan.Allocations); len(ids) != 2 || ids[0] != kari || ids[1] != ola {
		t.Errorf("allocations = %v, want Kari's and Ola's rows on it", ids)
	}
	want := []gen.PlanningProjectPlanWeek{
		{Monday: plan.Weeks[0].Monday, Booked: 18.75, Tentative: 10},
		{Monday: plan.Weeks[1].Monday, Booked: 18.75, Tentative: 0},
	}
	if len(plan.Weeks) != 2 || plan.Weeks[0] != want[0] || plan.Weeks[1] != want[1] || plan.Weeks[0].Monday.String() != week38 {
		t.Errorf("weeks = %+v, want %+v", plan.Weeks, want)
	}

	var def gen.PlanningProjectPlan
	getJSON(t, c, "/api/v1/planning/projects/2001/plan", http.StatusOK, &def)
	if len(def.Weeks) != 12 || def.Weeks[0].Monday.String() != thisMonday {
		t.Errorf("the default window = %d weeks from %v, want this week and the eleven after it", len(def.Weeks), def.Weeks[0].Monday)
	}
	getJSON(t, c, "/api/v1/planning/projects/2999/plan", http.StatusNotFound, nil)
	getJSON(t, h.SignIn(t, "planning:access"), "/api/v1/planning/projects/2001/plan", http.StatusForbidden, nil)
}

// TestProjectPlan_DemandAndRequests is S11's demand and requests over
// three weeks from Monday 5 October: an open request of 20 hours a week
// from Wednesday the 7th to Friday the 23rd weighs 3/5 of its hours in its
// first week (plan reading 5), and one of 10 hours over Monday and Tuesday
// the 12th and 13th 2/5 of its; a filled request is booked through its row
// and a withdrawn one is no demand. The plan lists the open and filled
// requests touching the window, by from date — not the withdrawn one, not
// one past the window, not another project's.
func TestProjectPlan_DemandAndRequests(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	row := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-23", unit: "hours_per_week", load: 10})
	filled := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-23", hours: 10, filledBy: row})
	wide := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-07", to: "2026-10-23", hours: 20})
	short := seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-12", to: "2026-10-13", hours: 10})
	seedRequest(t, h, seedReq{project: projectActive, from: "2026-10-05", to: "2026-10-23", hours: 30, status: "withdrawn"})
	seedRequest(t, h, seedReq{project: projectActive, from: "2026-11-02", to: "2026-11-27", hours: 30})
	seedRequest(t, h, seedReq{project: projectInternal, from: "2026-10-05", to: "2026-10-23", hours: 30})

	var plan gen.PlanningProjectPlan
	getJSON(t, c, "/api/v1/planning/projects/2001/plan?from=2026-10-05&to=2026-10-25", http.StatusOK, &plan)
	var demand []float64
	for _, w := range plan.Weeks {
		demand = append(demand, w.Demand)
	}
	if want := []float64{12, 24, 20}; !slices.Equal(demand, want) {
		t.Errorf("demand = %v, want %v", demand, want)
	}
	var ids []int64
	for _, r := range plan.Requests {
		ids = append(ids, r.Id)
	}
	if want := []int64{filled, wide, short}; !slices.Equal(ids, want) {
		t.Errorf("requests = %v, want %v", ids, want)
	}
	if r := plan.Requests[0]; r.FilledBy == nil || r.FilledBy.AllocationId != row || r.Project.Code == nil || *r.Project.Code != "KVEM1000" {
		t.Errorf("the filled request = %+v, want filled by row %d on KVEM1000", r, row)
	}

	// A window starting on the Thursday counts that week's days from it, as
	// its booked does: the wide request's Thursday and Friday.
	getJSON(t, c, "/api/v1/planning/projects/2001/plan?from=2026-10-08&to=2026-10-11", http.StatusOK, &plan)
	if len(plan.Weeks) != 1 || plan.Weeks[0].Demand != 8 {
		t.Errorf("the week from Thursday = %+v, want a demand of 8", plan.Weeks)
	}
}

// TestProjectPlan_DemandSkipsPublicHolidays is plan reading 5 on a public
// holiday: over the week of Christmas 2026, Friday the 25th is no business
// day, so a request of 20 hours a week asks 16 that week — exactly what an
// hours row of 20 books over the same days.
func TestProjectPlan_DemandSkipsPublicHolidays(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	seedRequest(t, h, seedReq{project: projectActive, from: "2026-12-21", to: "2026-12-25", hours: 20})
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-12-21", to: "2026-12-25", unit: "hours_per_week", load: 20})

	var plan gen.PlanningProjectPlan
	getJSON(t, c, "/api/v1/planning/projects/2001/plan?from=2026-12-21&to=2026-12-27", http.StatusOK, &plan)
	if len(plan.Weeks) != 1 || plan.Weeks[0].Demand != 16 || plan.Weeks[0].Booked != 16 {
		t.Errorf("the week of Christmas = %+v, want a demand of 16 and 16 booked", plan.Weeks)
	}
}
