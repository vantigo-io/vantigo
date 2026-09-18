package projects_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// PUT /api/v1/projects/{id}/status: D14's "any transition is allowed,
// including reopening", and its other half — a status change is its own
// operation so it gets its own timeline entry.

// changeStatus puts one status and returns the response, so a case can
// assert on the status code before decoding.
func changeStatus(t *testing.T, c *modtest.Client, id int32, status string) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/projects/%d/status", id), map[string]any{"status": status})
}

// setStatus changes a project's status and fails the test unless it was
// accepted, for a test whose subject is something else.
func setStatus(t *testing.T, c *modtest.Client, id int32, status string) projectJSON {
	t.Helper()
	r := changeStatus(t, c, id, status)
	if r.Status != http.StatusOK {
		t.Fatalf("set status %q: status %d body %s, want 200", status, r.Status, r.Body)
	}
	var project projectJSON
	r.JSON(&project)
	return project
}

// D14: the five statuses, every one of them reachable from the one before —
// the walk ends on 'completed' → 'active', which is reopening a finished
// project and is deliberately allowed.
func TestPutProjectsByIdStatus_EveryValueIsReachable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "ST1000"})

	if project.Status != "planned" {
		t.Fatalf("Status = %q, want the column default %q", project.Status, "planned")
	}
	walk := []string{"active", "on-hold", "completed", "cancelled", "planned", "completed", "active"}
	revision := project.Revision
	for _, want := range walk {
		updated := setStatus(t, c, project.Id, want)
		if updated.Status != want {
			t.Fatalf("Status = %q, want %q", updated.Status, want)
		}
		if updated.Revision != revision+1 {
			t.Fatalf("Revision = %d, want %d: a status change bumps the revision", updated.Revision, revision+1)
		}
		revision = updated.Revision
	}

	entries := h.Count(t, `SELECT count(*) FROM projects.timeline_entries WHERE project_id = $1 AND event_type = 'status-changed'`, project.Id)
	if entries != len(walk) {
		t.Errorf("status-changed entries = %d, want %d, one per change", entries, len(walk))
	}
	raw := modtest.One[string](t, h, `SELECT payload::text FROM projects.timeline_entries
	                                  WHERE project_id = $1 AND event_type = 'status-changed'
	                                  ORDER BY id LIMIT 1`, project.Id)
	var payload map[string]string
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if payload["old"] != "planned" || payload["new"] != "active" {
		t.Errorf("first status-changed payload = %s, want old 'planned' and new 'active'", raw)
	}
}

func TestPutProjectsByIdStatus_ValueOutsideTheSet_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "STBAD1000"})

	for _, status := range []string{"", "archived", "Active"} {
		r := changeStatus(t, c, project.Id, status)
		if r.Status != http.StatusBadRequest {
			t.Fatalf("status %q: status %d body %s, want 400", status, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if len(problem.Errors["status"]) == 0 {
			t.Errorf("status %q: Errors = %v, want the failure on the 'status' field", status, problem.Errors)
		}
	}
}

// Setting the status a project already has changes nothing: it answers the
// project, writes no timeline entry, and leaves the revision alone — a
// no-op must not look like a change to whoever reads the timeline.
func TestPutProjectsByIdStatus_SameStatus_ChangesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "STSAME1000"})

	unchanged := setStatus(t, c, project.Id, "planned")
	if unchanged.Status != "planned" {
		t.Errorf("Status = %q, want %q", unchanged.Status, "planned")
	}
	if unchanged.Revision != project.Revision {
		t.Errorf("Revision = %d, want %d unchanged: nothing happened", unchanged.Revision, project.Revision)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.timeline_entries WHERE project_id = $1 AND event_type = 'status-changed'`, project.Id); n != 0 {
		t.Errorf("status-changed entries = %d, want 0", n)
	}
}

// Changing the status is managing the project (design §5), so a member who
// can see it may not change it.
func TestPutProjectsByIdStatus_Member_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "STMEM1000"})
	member, memberID := signIn(t, h)
	addRole(t, h, project.Id, memberID, "member")

	if r := changeStatus(t, member, project.Id, "active"); r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

// D7: an outsider cannot tell the project from one that does not exist, on
// a write as much as on a read.
func TestPutProjectsByIdStatus_Outsider_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "STOUT1000"})
	outsider, _ := signIn(t, h)

	existing := changeStatus(t, outsider, project.Id, "active")
	if existing.Status != http.StatusNotFound {
		t.Fatalf("existing project: status %d body %s, want 404", existing.Status, existing.Body)
	}
	if unknown := changeStatus(t, outsider, 999999, "active"); unknown.Status != http.StatusNotFound {
		t.Errorf("unknown id: status %d body %s, want 404", unknown.Status, unknown.Body)
	}
}
