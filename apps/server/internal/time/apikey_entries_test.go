package timetracking_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestTimeEntries_AreNoAPIKeys is API access design K56: a time entry and a
// submitted week are the caller's own, and the contract has no field that
// names another person, so logging, changing and submitting time are never an
// API key's: 403 api_not_available, and nothing is written.
func TestTimeEntries_AreNoAPIKeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person, _ := signInAs(t, h, projectKraftVerket, roleMember)
	entry := createEntry(t, person, nil)
	key, integration := h.SignInAPIKey(t, modtest.APIKey{}, "time:access", "time:manage")

	for _, c := range []struct {
		name, method, path string
		body               any
	}{
		{"log time", http.MethodPost, entriesPath, entryBody(nil)},
		{"change an entry", http.MethodPut, entryPath(entry.Id), entryBody(map[string]any{"revision": entry.Revision})},
		{"submit a week", http.MethodPost, "/api/v1/time/weeks/" + workDay + "/submit", nil},
	} {
		if r := key.Do(c.method, c.path, c.body); r.Status != http.StatusForbidden || r.Code() != "api_not_available" {
			t.Errorf("%s: status %d body %s, want 403 api_not_available", c.name, r.Status, r.Body)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM time.entries WHERE user_id = $1`, integration); n != 0 {
		t.Errorf("the integration has %d time entries, want none", n)
	}
}
