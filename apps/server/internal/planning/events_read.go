package planning

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The record read (design S23): the events of the writes, filtered by
// person, project or allocation, newest first. Each carries the actor as
// snapshotted when they wrote, so the read asks nobody outside the module.

// maxEvents is the most events one read answers.
const maxEvents = 500

// ListPlanningEvents List events
// (GET /api/v1/planning/events)
//
// planning:view.
func (s *server) ListPlanningEvents(ctx context.Context, req gen.ListPlanningEventsRequestObject) (gen.ListPlanningEventsResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListEvents(ctx, store.ListEventsParams{
		PersonID: req.Params.PersonId, ProjectID: req.Params.ProjectId, AllocationID: req.Params.AllocationId, MaxEvents: maxEvents,
	})
	if err != nil {
		return nil, fmt.Errorf("planning: read the events: %w", err)
	}
	out := gen.PlanningEventList{Events: make([]gen.PlanningEvent, 0, len(rows))}
	for _, e := range rows {
		view, err := eventView(e)
		if err != nil {
			return nil, err
		}
		out.Events = append(out.Events, view)
	}
	return gen.ListPlanningEvents200JSONResponse(out), nil
}

// eventView is one event as the contract answers it.
func eventView(e store.PlanningEvent) (gen.PlanningEvent, error) {
	payload := map[string]any{}
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return gen.PlanningEvent{}, fmt.Errorf("planning: decode event %d: %w", e.ID, err)
		}
	}
	actor := gen.PlanningActor{Kind: gen.PlanningActorKind(e.ActorKind), UserId: e.ActorUserID}
	if e.ActorDisplay != nil {
		actor.DisplayName = *e.ActorDisplay
	}
	return gen.PlanningEvent{
		Id: e.ID, EntityKind: gen.PlanningEventEntityKind(e.EntityKind), EntityId: e.EntityID,
		Kind: gen.PlanningEventKind(e.Kind), Payload: payload, Actor: actor, OccurredAt: e.OccurredAt,
		PersonId: e.PersonID, ProjectId: e.ProjectID,
	}, nil
}
