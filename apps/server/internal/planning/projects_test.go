package planning_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const projectsPath = "/api/v1/planning/projects"

// TestProjects_Listing is S9: by default the plannable statuses by code,
// each with its customer's name and its dates; a status list and q narrow
// it; an unknown status is a field error; planning:access alone is a 403.
func TestProjects_Listing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")

	var list gen.PlanningProjectList
	getJSON(t, c, projectsPath, http.StatusOK, &list)
	if len(list.Projects) != 4 || list.Projects[0].Code != "HYDRO100" || list.Projects[1].Code != "INTERN" ||
		list.Projects[2].Code != "KVEM1000" || list.Projects[3].Code != "KVEM2000" {
		t.Fatalf("projects = %+v, want HYDRO100, INTERN, KVEM1000, KVEM2000", list.Projects)
	}
	if hydro := list.Projects[0]; hydro.Customer == nil || hydro.Customer.Name == nil || *hydro.Customer.Name != "Hydro ASA" {
		t.Errorf("HYDRO100 = %+v, want the second customer named", hydro)
	}
	active := list.Projects[2]
	if active.Customer == nil || active.Customer.Name == nil || *active.Customer.Name != "Kraft-Verket AS" ||
		active.StartDate == nil || active.StartDate.String() != "2026-09-01" || active.EndDate == nil || active.Status != gen.Active {
		t.Errorf("KVEM1000 = %+v, want its customer named and its dates", active)
	}
	if internal := list.Projects[1]; internal.Customer != nil || internal.StartDate != nil {
		t.Errorf("INTERN = %+v, want no customer and no dates", internal)
	}

	getJSON(t, c, projectsPath+"?status=completed,%20active&q=kraft", http.StatusOK, &list)
	if len(list.Projects) != 1 || list.Projects[0].Id != projectActive {
		t.Errorf("active or completed matching kraft = %+v, want KVEM1000 alone (FERDIG01 is not Kraft)", list.Projects)
	}
	getJSON(t, c, projectsPath+"?q=fase", http.StatusOK, &list)
	if len(list.Projects) != 1 || list.Projects[0].Id != projectPlanned {
		t.Errorf("q=fase = %+v, want the bid", list.Projects)
	}

	var p gen.PlanningProblem
	getJSON(t, c, projectsPath+"?status=active,closed", http.StatusBadRequest, &p)
	if p.Errors == nil || len((*p.Errors)["status"]) != 1 {
		t.Errorf("an unknown status = %+v, want a field error on status", p)
	}
	getJSON(t, h.SignIn(t, "planning:access"), projectsPath, http.StatusForbidden, nil)
}
