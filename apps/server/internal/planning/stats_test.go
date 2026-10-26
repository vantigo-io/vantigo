package planning_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const statsPath = "/api/v1/planning/stats"

// TestStats_ThisWeek is S22's figures: this week Kari is half booked and
// Ola tentatively full, over the two's 67.5 hours; one person planned; the
// unbooked list's count; no open requests before delivery B. A caller to
// whom Staff answers nobody sees no load at all.
func TestStats_ThisWeek(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: thisMonday, to: "2026-09-13", load: 50})
	seedAllocation(t, h, seedRow{person: personOla, project: projectPlanned, from: thisMonday, to: "2026-09-13", load: 100, status: "tentative"})

	var stats gen.PlanningStats
	getJSON(t, c, statsPath, http.StatusOK, &stats)
	if stats.PeoplePlannedThisWeek != 1 || stats.OpenRequests != 0 || stats.Unbooked != 2 {
		t.Errorf("stats = %+v, want one planned, two unbooked over the horizon ahead, no requests", stats)
	}
	if stats.LoadPercentThisWeek == nil || *stats.LoadPercentThisWeek != 27.78 ||
		stats.TentativeLoadPercentThisWeek == nil || *stats.TentativeLoadPercentThisWeek != 44.44 {
		t.Errorf("load = %v, tentative %v; want 18.75 and 30 of 67.5 hours", stats.LoadPercentThisWeek, stats.TentativeLoadPercentThisWeek)
	}

	h.workforce.setVisible(false)
	stats = gen.PlanningStats{}
	getJSON(t, c, statsPath, http.StatusOK, &stats)
	if stats.PeoplePlannedThisWeek != 0 || stats.Unbooked != 0 || stats.LoadPercentThisWeek != nil || stats.TentativeLoadPercentThisWeek != nil {
		t.Errorf("stats with nobody visible = %+v, want nothing", stats)
	}
	getJSON(t, h.SignIn(t, "planning:access"), statsPath, http.StatusForbidden, nil)
}
