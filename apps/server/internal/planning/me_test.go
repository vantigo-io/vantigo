package planning_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const mePath = "/api/v1/planning/me"

// TestMe_ConfirmedFromToday is S19: the caller's person and their confirmed
// rows from today on — one ending today included, one that ended yesterday
// and a tentative one not — never with the note.
func TestMe_ConfirmedFromToday(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h)
	h.workforce.link(user, personKari)
	endsToday := seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: "2026-09-01", to: "2026-09-12", load: 20, note: "Intern"})
	ahead := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-10-30", load: 80})
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-08-03", to: "2026-09-11", load: 80})
	seedAllocation(t, h, seedRow{person: personKari, project: projectPlanned, from: "2026-11-02", to: "2026-11-30", load: 50, status: "tentative"})

	var me gen.PlanningMe
	getJSON(t, c, mePath, http.StatusOK, &me)
	if me.Person.Id != personKari || me.Person.DisplayName != "Kari Nordmann" {
		t.Errorf("person = %+v, want Kari", me.Person)
	}
	if ids := allocationIDs(me.Allocations); len(ids) != 2 || ids[0] != endsToday || ids[1] != ahead {
		t.Fatalf("allocations = %v, want the row ending today and the one ahead", ids)
	}
	for _, a := range me.Allocations {
		if a.Note != nil || a.Status != gen.Confirmed || a.Project.Code == nil || a.Person.DisplayName == nil {
			t.Errorf("allocation = %+v, want confirmed, named, without the note", a)
		}
	}
}

// TestMe_NoPerson is S19's no_person: a caller Staff answers no person for.
func TestMe_NoPerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var p gen.PlanningProblem
	getJSON(t, h.SignIn(t, "planning:access", "planning:manage"), mePath, http.StatusNotFound, &p)
	if p.Code == nil || *p.Code != "no_person" {
		t.Errorf("GET /me without a person = %+v, want no_person", p)
	}
}

// TestMe_Wishes is S21 on My planning: the caller's open wishes, oldest
// first, the project named — never a colleague's.
func TestMe_Wishes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h)
	h.workforce.link(user, personKari)
	newer := seedOpenWish(t, h, personKari, "new", nil, "2026-11-01", time.Hour)
	older := seedOpenWish(t, h, personKari, "off", ptr(projectActive), "2026-10-01", 0)
	seedOpenWish(t, h, personOla, "new", nil, "2026-10-01", 0)

	var me gen.PlanningMe
	getJSON(t, c, mePath, http.StatusOK, &me)
	if ids := wishIDs(me.Wishes); len(ids) != 2 || ids[0] != older || ids[1] != newer {
		t.Fatalf("wishes = %v, want Kari's two, oldest first", ids)
	}
	if w := me.Wishes[0]; w.Project == nil || w.Project.Code == nil || *w.Project.Code != "KVEM1000" ||
		w.Person.DisplayName == nil || *w.Person.DisplayName != "Kari Nordmann" {
		t.Errorf("the off wish = %+v, want the project and Kari named", w)
	}
}
