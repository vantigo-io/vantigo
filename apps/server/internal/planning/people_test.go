package planning_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const peoplePath = "/api/v1/planning/people"

// TestPeople_Listing is the pickers' people (S8): by default today and the
// twelve weeks after it — which reaches Siri, December's starter —
// employment clipped to the window; q matches the name or the employee
// number; departmentId narrows; a refused window is window_out_of_bounds.
func TestPeople_Listing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")

	var list gen.PlanningPeopleList
	getJSON(t, c, peoplePath, http.StatusOK, &list)
	if len(list.People) != 3 || list.People[0].Id != personKari || list.People[1].Id != personOla || list.People[2].Id != personSiri {
		t.Fatalf("people = %+v, want Kari, Ola and Siri, who starts on 1 December", list.People)
	}
	if e := list.People[0].Employed; len(e) != 1 || e[0].From.String() != "2026-09-12" || e[0].To.String() != "2026-12-05" {
		t.Errorf("Kari's employment = %+v, want today and the twelve weeks after it", e)
	}
	if e := list.People[2].Employed; len(e) != 1 || e[0].From.String() != "2026-12-01" || list.People[2].Department != nil {
		t.Errorf("Siri = %+v, want her from her first day, in no department", list.People[2])
	}

	getJSON(t, c, peoplePath+"?to=2026-11-30", http.StatusOK, &list)
	if len(list.People) != 2 {
		t.Errorf("to 30 November = %+v, want Kari and Ola, not Siri", list.People)
	}
	getJSON(t, c, peoplePath+"?q=a-1002", http.StatusOK, &list)
	if len(list.People) != 1 || list.People[0].Id != personOla {
		t.Errorf("q=a-1002 = %+v, want Ola", list.People)
	}
	getJSON(t, c, peoplePath+"?departmentId=4001&q=nordmann", http.StatusOK, &list)
	if len(list.People) != 2 {
		t.Errorf("Leveranse's Nordmanns = %+v, want Kari and Ola", list.People)
	}

	h.workforce.setRefuseWindow(true)
	var p gen.PlanningProblem
	getJSON(t, c, peoplePath+"?from=2026-09-14&to=2031-01-01", http.StatusBadRequest, &p)
	if p.Code == nil || *p.Code != "window_out_of_bounds" || p.Errors == nil || len((*p.Errors)["to"]) != 1 {
		t.Errorf("a window Staff refuses = %+v, want window_out_of_bounds on to", p)
	}
	getJSON(t, h.SignIn(t, "planning:access"), peoplePath, http.StatusForbidden, nil)
}
