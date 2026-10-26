package planning_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// timelinePath is personID's timeline.
func timelinePath(personID int64) string {
	return "/api/v1/planning/people/" + strconv.FormatInt(personID, 10) + "/timeline"
}

// TestTimeline_Planner is S12 to planning:view and planning:manage: the
// employment, the days away, every row with its note, and the two flags —
// beyond_employment on the row running past Kari's last day, dated on it,
// and certificate_expires on the row her certificate expires inside, dated
// on its validTo.
func TestTimeline_Planner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view", "planning:manage")
	h.workforce.endEmployment(personKari, date("2026-10-31"))
	h.workforce.setAway(personKari, contracts.DateSpan{From: date("2026-09-28"), To: date("2026-10-02")})
	certificateEnd := date("2026-10-09")
	h.workforce.setHeld(personKari,
		contracts.HeldCompetence{CompetenceID: 7001, Key: "hms-kort", Kind: "certificate", Verified: true, ValidFrom: date("2025-01-01"), ValidTo: &certificateEnd},
		contracts.HeldCompetence{CompetenceID: 7002, Key: "go", Kind: "skill", ValidFrom: date("2025-01-01"), ValidTo: &certificateEnd})
	early := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-16", load: 50, note: "Fase 1"})
	late := seedAllocation(t, h, seedRow{person: personKari, project: projectPlanned, from: "2026-10-19", to: "2026-11-15", load: 100, status: "tentative"})

	var tl gen.PlanningTimeline
	getJSON(t, c, timelinePath(personKari), http.StatusOK, &tl)
	if tl.Person.Id != personKari || len(tl.Employed) != 1 || tl.Employed[0].From.String() != "2026-09-12" || tl.Employed[0].To.String() != "2026-10-31" {
		t.Errorf("person and employment = %+v, %+v; want Kari employed from today to 31 October", tl.Person, tl.Employed)
	}
	if len(tl.Away) != 1 || tl.Away[0].From.String() != "2026-09-28" {
		t.Errorf("away = %+v, want her week away", tl.Away)
	}
	if ids := allocationIDs(tl.Allocations); len(ids) != 2 || ids[0] != early || ids[1] != late {
		t.Errorf("allocations = %v, want both rows, tentative included", ids)
	}
	if tl.Allocations[0].Note == nil || *tl.Allocations[0].Note != "Fase 1" {
		t.Errorf("the note = %v, want it for planning:manage", tl.Allocations[0].Note)
	}
	flags := map[gen.PlanningTimelineFlagCode]gen.PlanningTimelineFlag{}
	for _, f := range tl.Flags {
		flags[f.Code] = f
	}
	if len(tl.Flags) != 2 {
		t.Fatalf("flags = %+v, want one of each", tl.Flags)
	}
	if f := flags[gen.PlanningTimelineFlagCodeBeyondEmployment]; f.AllocationId != late || f.Date == nil || f.Date.String() != "2026-10-31" {
		t.Errorf("beyond_employment = %+v, want the late row, dated on the employment's end", f)
	}
	if f := flags[gen.PlanningTimelineFlagCodeCertificateExpires]; f.AllocationId != early || f.Date == nil || f.Date.String() != "2026-10-09" {
		t.Errorf("certificate_expires = %+v, want the early row, dated on validTo", f)
	}

	getJSON(t, c, timelinePath(1999), http.StatusNotFound, nil)
	getJSON(t, c, timelinePath(personKari)+"?from=2026-09-14&to=2028-09-12", http.StatusBadRequest, nil)
}

// TestTimeline_Self is amendment 2: a caller holding planning:access alone
// reads their own person's confirmed rows and nothing else — no tentative
// row, no note, empty employment, days away and flags — and another
// person's timeline is the 404 an unknown id gets.
func TestTimeline_Self(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h)
	h.workforce.link(user, personKari)
	h.workforce.endEmployment(personKari, date("2026-10-31"))
	confirmed := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-11-15", load: 50, note: "Hemmelig"})
	seedAllocation(t, h, seedRow{person: personKari, project: projectPlanned, from: "2026-10-19", to: "2026-11-15", load: 50, status: "tentative"})

	var tl gen.PlanningTimeline
	getJSON(t, c, timelinePath(personKari), http.StatusOK, &tl)
	if tl.Person.Id != personKari || tl.Person.DisplayName != "Kari Nordmann" {
		t.Errorf("person = %+v, want Kari", tl.Person)
	}
	if len(tl.Employed) != 0 || len(tl.Away) != 0 || len(tl.Flags) != 0 {
		t.Errorf("employed, away, flags = %+v, %+v, %+v; want all empty", tl.Employed, tl.Away, tl.Flags)
	}
	if len(tl.Allocations) != 1 || tl.Allocations[0].Id != confirmed || tl.Allocations[0].Note != nil {
		t.Errorf("allocations = %+v, want the confirmed row without its note", tl.Allocations)
	}
	getJSON(t, c, timelinePath(personOla), http.StatusNotFound, nil)

	stranger, _ := signIn(t, h)
	getJSON(t, stranger, timelinePath(personKari), http.StatusNotFound, nil)
}

// TestTimeline_ViewWithoutStaff is a planning:view holder Staff answers
// nobody to — without staff:view: their own timeline is still theirs, in the
// self shape of amendment 2, and anyone else's is the bare 404.
func TestTimeline_ViewWithoutStaff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, "planning:view")
	h.workforce.link(user, personKari)
	confirmed := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-11-15", load: 50})
	seedAllocation(t, h, seedRow{person: personKari, project: projectPlanned, from: "2026-10-19", to: "2026-11-15", load: 50, status: "tentative"})
	h.workforce.setVisible(false)

	var tl gen.PlanningTimeline
	getJSON(t, c, timelinePath(personKari), http.StatusOK, &tl)
	if tl.Person.Id != personKari || len(tl.Employed) != 0 || len(tl.Away) != 0 || len(tl.Flags) != 0 {
		t.Errorf("timeline = %+v, want Kari with no employment, days away or flags", tl)
	}
	if len(tl.Allocations) != 1 || tl.Allocations[0].Id != confirmed {
		t.Errorf("allocations = %+v, want the confirmed row alone", tl.Allocations)
	}
	getJSON(t, c, timelinePath(personOla), http.StatusNotFound, nil)
}
