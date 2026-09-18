package timetracking_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const settingsPath = "/api/v1/time/settings"

// getSettings reads the time settings as a bare object, whose subject is
// whether lockedBefore is there at all, and fails the test unless it
// answered 200.
func getSettings(t *testing.T, c *modtest.Client) map[string]any {
	t.Helper()
	r := c.Do(http.MethodGet, settingsPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get settings: status %d body %s, want 200", r.Status, r.Body)
	}
	var settings map[string]any
	r.JSON(&settings)
	return settings
}

// putSettings replaces the time settings with body and fails the test unless
// it answered 200, answering them as they now stand.
func putSettings(t *testing.T, c *modtest.Client, body map[string]any) map[string]any {
	t.Helper()
	r := c.Do(http.MethodPut, settingsPath, body)
	if r.Status != http.StatusOK {
		t.Fatalf("put settings %v: status %d body %s, want 200", body, r.Status, r.Body)
	}
	var settings map[string]any
	r.JSON(&settings)
	return settings
}

func TestTimeSettings_TheLock_RoundTripsAndHoldsBackEntries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "time:manage")
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)

	if got := getSettings(t, member); len(got) != 0 {
		t.Errorf("no lock: settings = %v, want an empty object", got)
	}
	if got := putSettings(t, admin, map[string]any{"lockedBefore": "2026-09-10"}); got["lockedBefore"] != "2026-09-10" {
		t.Errorf("put: settings = %v, want the lock", got)
	}
	if got := getSettings(t, member); got["lockedBefore"] != "2026-09-10" {
		t.Errorf("a member reads settings = %v, want the lock", got)
	}
	errs := fieldErrors(t, member, entryBody(map[string]any{"entryDate": "2026-09-09"}))
	if len(errs["entryDate"]) != 1 {
		t.Errorf("an entry before the lock: errors = %v, want one on entryDate", errs)
	}
	createEntry(t, member, map[string]any{"entryDate": "2026-09-10"})

	// A new date moves the lock; null lifts it, and so does leaving it out.
	putSettings(t, admin, map[string]any{"lockedBefore": "2026-09-01"})
	createEntry(t, member, map[string]any{"entryDate": "2026-09-09"})
	if got := putSettings(t, admin, map[string]any{"lockedBefore": nil}); len(got) != 0 {
		t.Errorf("null: settings = %v, want the lock gone", got)
	}
	createEntry(t, member, map[string]any{"entryDate": "2026-08-01"})
	putSettings(t, admin, map[string]any{"lockedBefore": "2026-09-01"})
	if got := putSettings(t, admin, map[string]any{}); len(got) != 0 {
		t.Errorf("absent: settings = %v, want the lock gone", got)
	}
	if got := getSettings(t, member); len(got) != 0 {
		t.Errorf("after lifting: settings = %v, want an empty object", got)
	}
}

func TestPutTimeSettings_WithoutTimeManage_IsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, perms := range [][]string{nil, {"time:approve", "time:view-all"}} {
		c, _ := signIn(t, h, perms...)
		if r := c.Do(http.MethodPut, settingsPath, map[string]any{"lockedBefore": "2026-09-10"}); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", perms, r.Status)
		}
	}
	if got := getSettings(t, h.SignIn(t, "time:access")); len(got) != 0 {
		t.Errorf("settings = %v, want no lock set", got)
	}
}
