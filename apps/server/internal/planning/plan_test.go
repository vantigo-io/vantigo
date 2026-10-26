package planning_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// TestProjectPlan_Weeks is S11: the project, its rows in the window, and per
// week its confirmed and tentative hours over each person's capacity — a
// row on another project not counted — with demand 0 until delivery B.
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
