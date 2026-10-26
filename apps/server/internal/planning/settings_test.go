package planning_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const settingsPath = "/api/v1/planning/settings"

func ifMatch(revision int32) modtest.RequestOption {
	return modtest.Header("If-Match", strconv.Itoa(int(revision)))
}

// TestSettings_DefaultsAndAChange is S20: the seeded defaults, a change of
// one field keeping the others, the revision moving on, and the change read
// back.
func TestSettings_DefaultsAndAChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, user := signIn(t, h, "planning:manage")

	var s gen.PlanningSettings
	res := c.Do(http.MethodGet, settingsPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /settings = %d %s", res.Status, res.Body)
	}
	res.JSON(&s)
	want := gen.PlanningSettings{Overbooking: gen.Warn, DefaultLoadUnit: gen.Percent, UnbookedThresholdPercent: 50,
		UnbookedHorizonWeeks: 6, RequestLeadDays: 14, EndingLeadWeeks: 4, Revision: 1}
	s.UpdatedAt = want.UpdatedAt
	if s != want {
		t.Errorf("defaults = %+v, want %+v", s, want)
	}

	res = c.Do(http.MethodPut, settingsPath, map[string]any{"overbooking": "refuse", "unbookedHorizonWeeks": 8}, ifMatch(1))
	if res.Status != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s", res.Status, res.Body)
	}
	var changed gen.PlanningSettings
	res.JSON(&changed)
	if changed.Overbooking != gen.Refuse || changed.UnbookedHorizonWeeks != 8 || changed.Revision != 2 ||
		changed.DefaultLoadUnit != gen.Percent || changed.UnbookedThresholdPercent != 50 || changed.EndingLeadWeeks != 4 {
		t.Errorf("changed = %+v, want refuse and 8 weeks at revision 2, the rest kept", changed)
	}
	if by := modtest.One[string](t, h.Harness, `SELECT updated_by_user_id::text FROM planning.settings WHERE id = 1`); by != user.String() {
		t.Errorf("updated_by_user_id = %s, want the planner %s", by, user)
	}

	var read gen.PlanningSettings
	c.Do(http.MethodGet, settingsPath, nil).JSON(&read)
	if read != changed {
		t.Errorf("read back = %+v, want %+v", read, changed)
	}
}

// TestSettings_Refusals is the change's refusals: a stale revision is the 409
// revision_conflict, an If-Match that is no revision a 400, every value out of
// its bounds a field error on its own field, and a property the contract does
// not name a field error naming it — nothing written by any of them.
func TestSettings_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "planning:manage")

	res := c.Do(http.MethodPut, settingsPath, map[string]any{"overbooking": "refuse"}, ifMatch(7))
	var p gen.PlanningProblem
	if res.Status != http.StatusConflict {
		t.Fatalf("a stale revision = %d %s, want 409", res.Status, res.Body)
	}
	res.JSON(&p)
	if p.Code == nil || *p.Code != "revision_conflict" {
		t.Errorf("a stale revision's problem = %+v, want revision_conflict", p)
	}

	if res := c.Do(http.MethodPut, settingsPath, map[string]any{}, modtest.Header("If-Match", "abc")); res.Status != http.StatusBadRequest {
		t.Errorf("an If-Match that is no revision = %d, want 400", res.Status)
	}

	res = c.Do(http.MethodPut, settingsPath, map[string]any{
		"overbooking": "maybe", "defaultLoadUnit": "days", "unbookedThresholdPercent": 0, "unbookedHorizonWeeks": 53,
		"requestLeadDays": 366, "endingLeadWeeks": 27, "digest": true,
	}, ifMatch(1))
	if res.Status != http.StatusBadRequest {
		t.Fatalf("values out of bounds = %d %s, want 400", res.Status, res.Body)
	}
	p = gen.PlanningProblem{}
	res.JSON(&p)
	for _, field := range []string{"overbooking", "defaultLoadUnit", "unbookedThresholdPercent", "unbookedHorizonWeeks", "requestLeadDays", "endingLeadWeeks", "digest"} {
		if p.Errors == nil || len((*p.Errors)[field]) == 0 {
			t.Errorf("no field error on %s: %s", field, res.Body)
		}
	}
	if revision := modtest.One[int32](t, h.Harness, `SELECT revision FROM planning.settings WHERE id = 1`); revision != 1 {
		t.Errorf("revision = %d after refusals, want 1: nothing written", revision)
	}
}

// TestSettings_NeedManage is the contract's access rule: the settings are
// planning:manage's, not planning:view's.
func TestSettings_NeedManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "planning:view")
	if res := c.Do(http.MethodGet, settingsPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /settings with planning:view = %d, want 403", res.Status)
	}
	if res := c.Do(http.MethodPut, settingsPath, map[string]any{}, ifMatch(1)); res.Status != http.StatusForbidden {
		t.Errorf("PUT /settings with planning:view = %d, want 403", res.Status)
	}
}
