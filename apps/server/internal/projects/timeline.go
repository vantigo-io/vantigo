package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is the write half of the project timeline (design §3). Every
// state change writes its entry inside the same transaction as the change,
// so a project that exists always has the entry that created it, and a
// rolled-back change leaves no trace of having been attempted. The read
// endpoint is Task 5's.

// The timeline's event types. Only project-created is written so far; the
// rest arrive with the operations that cause them.
const eventProjectCreated = "project-created"

// recordEvent inserts one generated timeline entry. The actor's display name
// is stored, not looked up on read: a timeline is a record of what happened
// and who did it at the time, which a later rename or deletion must not
// rewrite.
func recordEvent(ctx context.Context, q *store.Queries, now time.Time, projectID int32, eventType string, payload any, by actor) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("projects: encode timeline payload: %w", err)
	}
	return q.InsertTimelineEntry(ctx, store.InsertTimelineEntryParams{
		ProjectID:    projectID,
		EventType:    eventType,
		Payload:      encoded,
		ActorUserID:  &by.UserID,
		ActorDisplay: by.Display,
		Now:          now,
	})
}

// recordProjectCreated is the entry every project opens its timeline with.
// Its payload carries the code and name the project was created under, so a
// later rename (D1) is legible against what it replaced — and no amounts,
// which would need the same financial shaping the project itself does (D12).
func recordProjectCreated(ctx context.Context, q *store.Queries, now time.Time, projectID int32, code, name string, by actor) error {
	payload := map[string]any{"code": code, "name": name}
	return recordEvent(ctx, q, now, projectID, eventProjectCreated, payload, by)
}
