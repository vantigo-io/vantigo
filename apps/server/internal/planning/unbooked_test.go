package planning_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// personPer is a fourth person some read tests add: full time all along, in
// no department.
const personPer = int64(1004)

// setUnbooked sets the unbooked list's horizon and threshold (S20).
func setUnbooked(t *testing.T, h *harness, horizonWeeks, thresholdPercent int) {
	t.Helper()
	h.Exec(t, `UPDATE planning.settings SET unbooked_horizon_weeks = $1, unbooked_threshold_percent = $2`, horizonWeeks, thresholdPercent)
}

// TestUnbooked_List is S13 and amendment 9 over a four-week horizon from
// today: Ola, with no confirmed row (his tentative one weighs nothing), is
// free now and first; then by roll-off Per, whose row ends on the 18th, and
// Kari, whose ends on the 25th; a lower threshold leaves Kari's 25 % out,
// and a person away throughout the horizon is away, not unbooked.
func TestUnbooked_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	setUnbooked(t, h, 4, 50)
	h.workforce.addPerson(personPer, "Per Hansen", "A-1004", date("2026-01-01"), date("2029-12-31"), 37.5)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: week38, to: "2026-09-25", load: 50})
	seedAllocation(t, h, seedRow{person: personPer, project: projectActive, from: week38, to: "2026-09-18", load: 50})
	seedAllocation(t, h, seedRow{person: personOla, project: projectPlanned, from: week38, to: "2026-10-30", load: 100, status: "tentative"})

	var list gen.PlanningUnbooked
	getJSON(t, c, "/api/v1/planning/unbooked", http.StatusOK, &list)
	if list.Horizon.From.String() != "2026-09-12" || list.Horizon.To.String() != "2026-10-10" {
		t.Errorf("horizon = %+v, want today and the four weeks after it", list.Horizon)
	}
	if len(list.People) != 3 {
		t.Fatalf("people = %+v, want Ola, Per and Kari", list.People)
	}
	ola, per, kari := list.People[0], list.People[1], list.People[2]
	if ola.Person.Id != personOla || ola.RollOff != nil || ola.LoadPercent != 0 || ola.FreeHours != 120 {
		t.Errorf("first = %+v, want Ola free now: no roll-off, 0 %%, 120 hours", ola)
	}
	if per.Person.Id != personPer || per.RollOff == nil || per.RollOff.String() != "2026-09-18" || per.LoadPercent != 12.5 {
		t.Errorf("second = %+v, want Per rolling off on the 18th at 12.5 %%", per)
	}
	if kari.Person.Id != personKari || kari.RollOff == nil || kari.RollOff.String() != "2026-09-25" || kari.LoadPercent != 25 || kari.FreeHours != 112.5 {
		t.Errorf("third = %+v, want Kari rolling off on the 25th at 25 %% with 112.5 hours free", kari)
	}

	setUnbooked(t, h, 4, 20)
	h.workforce.setAway(personOla, contracts.DateSpan{From: date("2026-09-12"), To: date("2026-10-10")})
	getJSON(t, c, "/api/v1/planning/unbooked", http.StatusOK, &list)
	if len(list.People) != 1 || list.People[0].Person.Id != personPer {
		t.Errorf("under 20 %% with Ola away throughout = %+v, want Per alone", list.People)
	}
	getJSON(t, h.SignIn(t, "planning:access"), "/api/v1/planning/unbooked", http.StatusForbidden, nil)
}
