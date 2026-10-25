package staff_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Who reads what the person wants (staff competence design §5's Wishes
// group, plan reading 16): the person, their manager, HR and
// staff:personal-data read every list whole, each row with its Seen stamp;
// a colleague, an Approver and a caller who does not see the person get the
// 404 an unknown id gets, on every list and on the Seen; nothing of the
// lists reaches a colleague through the history. The Seen stamp on a
// competence row is the detail group's: the manager reads it, a colleague
// never does.
func TestWishes_NoLeaksPerAudience(t *testing.T) {
	t.Parallel()
	w := newCompetenceWorldA(t)
	row := declare(t, w.kariC, w.kari.Id, map[string]any{"competenceId": w.fse})
	putInterests(t, w.kariC, w.kari.Id, interestsOf(w.fse))
	putPreferences(t, w.kariC, w.kari.Id, pref("travel", map[string]any{"extent": "regional"}, 1))
	putAvailability(t, w.kariC, w.kari.Id, map[string]any{"kind": "available", "weekday": 1})
	markSeen(t, w.bossC, w.kari.Id)

	lists := map[string]struct {
		path string
		keys []string
	}{
		"interests":    {interestsPath(w.kari.Id), []string{"competenceId", "competence", "held", "seenAt"}},
		"preferences":  {preferencesPath(w.kari.Id), []string{"kind", "value", "weight", "seenAt"}},
		"declarations": {availabilityPath(w.kari.Id), []string{"kind", "weekday", "seenAt"}},
	}
	for name, c := range map[string]*modtest.Client{
		"Kari": w.kariC, "her manager": w.bossC, "HR": w.hr, "staff:personal-data": w.pd,
	} {
		for list, l := range lists {
			r := c.Do(http.MethodGet, l.path, nil)
			if r.Status != http.StatusOK {
				t.Errorf("%s reads Kari's %s: %d, want 200", name, list, r.Status)
				continue
			}
			rows, _ := raw(t, r)[list].([]any)
			if len(rows) != 1 {
				t.Errorf("%s reads %s %v, want Kari's one row", name, list, rows)
				continue
			}
			for _, key := range l.keys {
				if _, ok := rows[0].(map[string]any)[key]; !ok {
					t.Errorf("%s does not read %s on Kari's %s: %v", name, key, list, rows[0])
				}
			}
		}
	}
	for name, c := range map[string]*modtest.Client{"a colleague": w.viewer, "an Approver": w.approver, "a stranger": w.nobody} {
		for list, l := range lists {
			if r := c.Do(http.MethodGet, l.path, nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
				t.Errorf("%s reads Kari's %s: %d %s, want the bare 404", name, list, r.Status, r.Body)
			}
		}
		if r := c.Do(http.MethodPost, wishesSeenPath(w.kari.Id), nil); r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("%s marks Kari's wishes seen: %d %s, want the bare 404", name, r.Status, r.Body)
		}
	}

	seenAt := func(c *modtest.Client) (bool, int) {
		rows, status := rawCompetences(t, c, w.kari.Id)
		for _, r := range rows {
			if r["id"] == float64(row.Id) {
				_, ok := r["seenAt"]
				return ok, status
			}
		}
		return false, status
	}
	for name, c := range map[string]*modtest.Client{"Kari": w.kariC, "her manager": w.bossC, "an Approver": w.approver, "HR": w.hr} {
		if ok, status := seenAt(c); !ok {
			t.Errorf("%s does not read the competence row's seenAt (status %d)", name, status)
		}
	}
	if ok, status := seenAt(w.viewer); status != http.StatusOK || ok {
		t.Errorf("a colleague reads the competence row's seenAt: %v (status %d), want it absent", ok, status)
	}

	// The history is the person's, their manager's and HR's: a colleague
	// reads none of it.
	if r := w.viewer.Do(http.MethodGet, eventsPath(w.kari.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a colleague reads Kari's history: %d, want 404", r.Status)
	}
}
