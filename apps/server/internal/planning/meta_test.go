package planning_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const metaPath = "/api/v1/planning/meta"

// TestMeta_Planner is GET /meta to a planner: both roles, the person Staff
// answers for them, the workforce visible, both load units and the settings.
func TestMeta_Planner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, "planning:view", "planning:manage")
	h.workforce.link(user, personKari)

	res := c.Do(http.MethodGet, metaPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /meta = %d %s, want 200", res.Status, res.Body)
	}
	var meta gen.PlanningMeta
	res.JSON(&meta)
	if !meta.CanView || !meta.CanManage || !meta.HasPerson || !meta.WorkforceVisible {
		t.Errorf("meta = %+v, want a planner with a person and the workforce visible", meta)
	}
	if !slices.Equal(meta.LoadUnits, []gen.PlanningLoadUnit{gen.Percent, gen.HoursPerWeek}) {
		t.Errorf("loadUnits = %v, want percent and hours_per_week", meta.LoadUnits)
	}
	if meta.Settings == nil || meta.Settings.Overbooking != gen.Warn || meta.Settings.Revision != 1 {
		t.Errorf("settings = %+v, want the seeded settings for planning:manage", meta.Settings)
	}
}

// TestMeta_AccessAlone is GET /meta to an employee: no role, no settings, and
// no person when Staff answers none for them — and the workforce is not asked
// about at all, since they may not see the plan.
func TestMeta_AccessAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)

	var meta gen.PlanningMeta
	res := c.Do(http.MethodGet, metaPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /meta = %d %s, want 200", res.Status, res.Body)
	}
	res.JSON(&meta)
	if meta.CanView || meta.CanManage || meta.HasPerson || meta.WorkforceVisible || meta.Settings != nil {
		t.Errorf("meta = %+v, want nothing but the load units", meta)
	}
}

// TestMeta_PlannerWithoutStaffView is reading 14: a caller who may see the
// plan but to whom Staff answers nobody — no staff:view — is told so, so an
// empty board is never mistaken for an empty plan. Self answers under its own
// rule all the same.
func TestMeta_PlannerWithoutStaffView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, "planning:view")
	h.workforce.link(user, personOla)
	h.workforce.setVisible(false)

	var meta gen.PlanningMeta
	res := c.Do(http.MethodGet, metaPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /meta = %d %s, want 200", res.Status, res.Body)
	}
	res.JSON(&meta)
	if !meta.CanView || meta.CanManage || !meta.HasPerson || meta.WorkforceVisible || meta.Settings != nil {
		t.Errorf("meta = %+v, want planning:view with a person and the workforce not visible", meta)
	}
}

// TestMeta_NeedsAccess is the router's rule: every operation needs
// planning:access.
func TestMeta_NeedsAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if res := h.SignIn(t, "planning:view").Do(http.MethodGet, metaPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /meta without planning:access = %d, want 403", res.Status)
	}
}
