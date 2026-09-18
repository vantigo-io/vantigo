package projects_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// GET /api/v1/projects/{id}/timeline: the read half of design §3's record of
// what happened to a project. Anyone who can see the project can read it —
// the entries carry no amounts (D12), so there is nothing on it to shape.

type timelineEntryJSON struct {
	Id           int64          `json:"id"`
	EventType    string         `json:"eventType"`
	Payload      map[string]any `json:"payload"`
	ActorUserId  *uuid.UUID     `json:"actorUserId"`
	ActorDisplay string         `json:"actorDisplay"`
	OccurredAt   time.Time      `json:"occurredAt"`
}

type timelineListJSON struct {
	Data       []timelineEntryJSON `json:"data"`
	Pagination paginationJSON      `json:"pagination"`
}

// readTimeline reads one page of a project's timeline, failing the test
// unless it answered 200.
func readTimeline(t *testing.T, c *modtest.Client, id int32, query string) timelineListJSON {
	t.Helper()
	path := fmt.Sprintf("/api/v1/projects/%d/timeline", id)
	if query != "" {
		path += "?" + query
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("timeline %q: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var list timelineListJSON
	r.JSON(&list)
	return list
}

func timelineEventTypes(list timelineListJSON) []string {
	out := make([]string, 0, len(list.Data))
	for _, e := range list.Data {
		out = append(out, e.EventType)
	}
	return out
}

// Newest first, and paged: the project's own create entry is the last thing
// on the last page, whatever happened after it.
func TestGetProjectsByIdTimeline_NewestFirstAndPaged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TLR1000"})
	for _, status := range []string{"active", "on-hold", "completed"} {
		setStatus(t, c, project.Id, status)
	}

	all := readTimeline(t, c, project.Id, "")
	want := []string{"status-changed", "status-changed", "status-changed", "project-created"}
	if got := timelineEventTypes(all); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("event types = %v, want %v (newest first)", got, want)
	}
	if all.Pagination.TotalCount != 4 {
		t.Errorf("TotalCount = %d, want 4", all.Pagination.TotalCount)
	}

	newest := all.Data[0]
	if newest.ActorUserId == nil || *newest.ActorUserId != userID {
		t.Errorf("ActorUserId = %v, want the caller %s", newest.ActorUserId, userID)
	}
	if newest.ActorDisplay == "" || newest.ActorDisplay == "Unknown user" {
		t.Errorf("ActorDisplay = %q, want the name the user directory resolved", newest.ActorDisplay)
	}
	if newest.Payload["new"] != "completed" {
		t.Errorf("Payload = %v, want the newest status change", newest.Payload)
	}

	first := readTimeline(t, c, project.Id, "page=1&pageSize=3")
	if len(first.Data) != 3 || !first.Pagination.HasNextPage {
		t.Errorf("first page = %+v, want three entries and a next page", first)
	}
	last := readTimeline(t, c, project.Id, "page=2&pageSize=3")
	if got := timelineEventTypes(last); len(got) != 1 || got[0] != "project-created" {
		t.Errorf("last page event types = %v, want [project-created]", got)
	}
	if last.Pagination.HasNextPage || !last.Pagination.HasPreviousPage {
		t.Errorf("last page pagination = %+v, want a previous and no next", last.Pagination)
	}
}

// Design §7: the timeline is readable by anyone who sees the project, a
// viewer included — the weakest role there is.
func TestGetProjectsByIdTimeline_Viewer_MayRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "TLV1000"})
	viewer, viewerID := signIn(t, h)
	addRole(t, h, project.Id, viewerID, "viewer")

	list := readTimeline(t, viewer, project.Id, "")
	if got := timelineEventTypes(list); len(got) != 1 || got[0] != "project-created" {
		t.Errorf("event types = %v, want [project-created]", got)
	}
}

// D7: an outsider cannot tell a project's timeline from one that does not
// exist.
func TestGetProjectsByIdTimeline_Outsider_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	creator, _ := signIn(t, h, "projects:create")
	project := createProject(t, creator, map[string]any{"code": "TLO1000"})
	outsider, _ := signIn(t, h)

	existing := outsider.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/timeline", project.Id), nil)
	if existing.Status != http.StatusNotFound {
		t.Fatalf("existing project: status %d body %s, want 404", existing.Status, existing.Body)
	}
	unknown := outsider.Do(http.MethodGet, "/api/v1/projects/999999/timeline", nil)
	if unknown.Status != http.StatusNotFound {
		t.Fatalf("unknown id: status %d body %s, want 404", unknown.Status, unknown.Body)
	}
	if string(existing.Body) != string(unknown.Body) {
		t.Errorf("outsider's 404 body %q differs from an unknown id's %q", existing.Body, unknown.Body)
	}
}

func TestGetProjectsByIdTimeline_InvalidPaging_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := createProject(t, c, map[string]any{"code": "TLP1000"})

	for _, query := range []string{"page=0", "pageSize=101"} {
		r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/projects/%d/timeline?%s", project.Id, query), nil)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", query, r.Status, r.Body)
		}
	}
}
