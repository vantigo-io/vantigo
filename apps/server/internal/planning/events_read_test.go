package planning_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// seedEvent writes one event of an allocation, kind, at the harness clock
// moved by offset.
func seedEvent(t *testing.T, h *harness, allocationID, personID int64, projectID int32, kind string, actor uuid.UUID, offset time.Duration) {
	t.Helper()
	h.Exec(t, `INSERT INTO planning.events (entity_kind, entity_id, person_id, project_id, kind, payload, actor_kind, actor_user_id, actor_display, occurred_at)
VALUES ('allocation', $1, $2, $3, $4, '{"fields":{"load":{"from":50,"to":80}}}', 'user', $5, 'Pia Planlegger', $6)`,
		allocationID, personID, projectID, kind, actor, h.Now().Add(offset))
}

// TestEvents_Listing is S23's read: newest first, filtered by person,
// project or allocation, each with its actor as snapshotted and its payload.
func TestEvents_Listing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, "planning:access", "planning:view")
	actor := uuid.New()
	seedEvent(t, h, 1001, personKari, projectActive, "allocation_created", actor, 0)
	seedEvent(t, h, 1001, personKari, projectActive, "allocation_changed", actor, time.Hour)
	seedEvent(t, h, 1002, personOla, projectInternal, "allocation_created", actor, 2*time.Hour)

	var list gen.PlanningEventList
	getJSON(t, c, "/api/v1/planning/events", http.StatusOK, &list)
	if len(list.Events) != 3 || list.Events[0].EntityId != 1002 {
		t.Fatalf("events = %+v, want all three, newest first", list.Events)
	}
	getJSON(t, c, "/api/v1/planning/events?personId=1001", http.StatusOK, &list)
	if len(list.Events) != 2 || list.Events[0].Kind != gen.AllocationChanged || list.Events[1].Kind != gen.AllocationCreated {
		t.Fatalf("Kari's events = %+v, want the change, then the creation", list.Events)
	}
	e := list.Events[0]
	if e.Actor.DisplayName != "Pia Planlegger" || e.Actor.Kind != gen.User || e.Actor.UserId == nil || *e.Actor.UserId != actor ||
		e.ProjectId == nil || *e.ProjectId != projectActive || e.Payload["fields"] == nil {
		t.Errorf("event = %+v, want the actor, the project and the payload", e)
	}
	getJSON(t, c, "/api/v1/planning/events?projectId=2004", http.StatusOK, &list)
	if len(list.Events) != 1 || list.Events[0].EntityId != 1002 {
		t.Errorf("INTERN's events = %+v, want Ola's", list.Events)
	}
	getJSON(t, c, "/api/v1/planning/events?allocationId=1001", http.StatusOK, &list)
	if len(list.Events) != 2 {
		t.Errorf("allocation 1001's events = %+v, want two", list.Events)
	}
	getJSON(t, h.SignIn(t, "planning:access"), "/api/v1/planning/events", http.StatusForbidden, nil)
}
