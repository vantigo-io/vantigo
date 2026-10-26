package planning_test

import (
	"net/http"
	"strconv"
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

// TestEvents_WishesToManageAlone is S23's audience: a wish's events are
// planning:manage's — a caller with planning:view alone never reads one,
// whatever the filter.
func TestEvents_WishesToManageAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	actor := uuid.New()
	seedEvent(t, h, 1001, personKari, projectActive, "allocation_created", actor, 0)
	h.Exec(t, `INSERT INTO planning.events (entity_kind, entity_id, person_id, project_id, kind, payload, actor_kind, actor_user_id, actor_display, occurred_at)
VALUES ('wish', 1001, $1, $2, 'wish_created', '{"kind":"off","projectId":2001,"fromDate":"2026-10-01"}', 'user', $3, 'Kari Nordmann', $4)`,
		personKari, projectActive, actor, h.Now().Add(time.Hour))

	viewer := h.SignIn(t, "planning:access", "planning:view")
	for _, query := range []string{"", "?personId=1001", "?projectId=2001", "?allocationId=1001"} {
		var list gen.PlanningEventList
		getJSON(t, viewer, "/api/v1/planning/events"+query, http.StatusOK, &list)
		for _, e := range list.Events {
			if e.EntityKind == gen.Wish {
				t.Errorf("planning:view reads a wish's event with %q: %+v", query, e)
			}
		}
	}
	var list gen.PlanningEventList
	getJSON(t, h.SignIn(t, "planning:access", "planning:view", "planning:manage"), "/api/v1/planning/events?personId=1001", http.StatusOK, &list)
	if len(list.Events) != 2 || list.Events[0].EntityKind != gen.Wish || list.Events[0].Kind != gen.WishCreated {
		t.Errorf("to manage = %+v, want the wish's event first", list.Events)
	}
}

// TestEvents_TermsToManageAlone is amendment 89: the terms' events are
// planning:manage's as a wish's are — a caller with planning:view alone
// never reads terms_set or terms_cleared, whatever the filter, while a
// planner reads both with the fields before and after.
func TestEvents_TermsToManageAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager := planner(t, h)
	id := seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-10-05", to: "2026-10-30", load: 50})
	termsSet(t, putTerms(manager, id, map[string]any{"reference": "A-1", "nonCompeteUntil": "2027-06-30", "nonCompeteCustomerIds": []int32{customerKraftVerket}}))
	if res := manager.Do(http.MethodDelete, termsPath(id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE terms = %d %s", res.Status, res.Body)
	}

	viewer := h.SignIn(t, "planning:access", "planning:view")
	for _, query := range []string{"", "?personId=1001", "?projectId=2001", "?allocationId=" + strconv.FormatInt(id, 10)} {
		var list gen.PlanningEventList
		getJSON(t, viewer, "/api/v1/planning/events"+query, http.StatusOK, &list)
		for _, e := range list.Events {
			if e.Kind == gen.TermsSet || e.Kind == gen.TermsCleared {
				t.Errorf("planning:view reads a terms event with %q: %+v", query, e)
			}
		}
	}

	var list gen.PlanningEventList
	getJSON(t, manager, "/api/v1/planning/events?allocationId="+strconv.FormatInt(id, 10), http.StatusOK, &list)
	if len(list.Events) != 2 || list.Events[0].Kind != gen.TermsCleared || list.Events[1].Kind != gen.TermsSet {
		t.Fatalf("to manage = %+v, want terms_cleared, then terms_set", list.Events)
	}
	for _, e := range list.Events {
		fields, ok := e.Payload["fields"].(map[string]any)
		if !ok || fields["reference"] == nil || fields["nonCompeteCustomerIds"] == nil {
			t.Errorf("%s's payload = %v, want the reference and the customer ids", e.Kind, e.Payload)
		}
	}
}
