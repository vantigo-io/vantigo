package planning

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The record (design S23, §3.6): every write is one row in planning.events —
// the entity it is about, the person and the project it touches, the changed
// fields before and after, and the actor snapshotted as Staff keeps it. The
// person's name is never written; the actor's is, as it read when they wrote.

// The entity kinds an event is about: an allocation (delivery A), a request
// (B); a wish arrives with C.
const (
	entityAllocation = "allocation"
	entityRequest    = "request"
)

const (
	actorKindUser          = "user"
	actorKindSystem        = "system"
	unknownUserDisplay     = "Unknown user"
	systemActorDisplayName = "System"
)

// actor is who an event is attributed to: the three values every event
// carries (actor_kind, actor_display, actor_user_id), resolved once per
// request — Staff's shape (internal/staff/events.go), copied because no module
// imports another.
type actor struct {
	Kind    string
	Display string
	UserID  *uuid.UUID
}

// actorFor resolves the request's principal into the actor its events are
// attributed to. It reads the user directory, so it is called before the
// transaction opens (contractscalls.go refuses it inside one). A user the
// directory no longer knows is still the actor, as "Unknown user", so the
// record exists although the account is gone; a lookup that fails fails the
// write, since history that guesses at who wrote it is worse than a 500.
func (s *server) actorFor(ctx context.Context) (actor, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.SCIM || p.UserID == uuid.Nil {
		return actor{Kind: actorKindSystem, Display: systemActorDisplayName}, nil
	}
	user, err := s.usersUser(ctx, p.UserID)
	if err != nil {
		return actor{}, fmt.Errorf("planning: resolve the actor: %w", err)
	}
	id := p.UserID
	if user == nil {
		return actor{Kind: actorKindUser, Display: unknownUserDisplay, UserID: &id}, nil
	}
	return actor{Kind: actorKindUser, Display: user.DisplayName, UserID: &id}, nil
}

// fieldChange is one field's before and after in a payload. Either is absent
// when the field was empty on that side.
type fieldChange struct {
	From any `json:"from,omitempty"`
	To   any `json:"to,omitempty"`
}

// changes is a payload's fields, by the contract's field names.
type changes map[string]fieldChange

// event is one row of the record before it is written: what it is about and
// what it says. PersonID and ProjectID are nil for an event that touches
// neither.
type event struct {
	EntityKind string
	EntityID   int64
	PersonID   *int64
	ProjectID  *int32
	Kind       string
	Payload    any
}

// recordEvent appends e inside the caller's transaction, so the write and its
// record commit or roll back together, attributed to who as read before the
// transaction (actorFor).
func (s *server) recordEvent(ctx context.Context, txq *store.Queries, who actor, e event) error {
	payload := e.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("planning: encode a %s event: %w", e.Kind, err)
	}
	display := who.Display
	if err := txq.InsertEvent(ctx, store.InsertEventParams{
		EntityKind: e.EntityKind, EntityID: e.EntityID, PersonID: e.PersonID, ProjectID: e.ProjectID,
		Kind: e.Kind, Payload: body, ActorKind: who.Kind, ActorUserID: who.UserID, ActorDisplay: &display,
		OccurredAt: s.deps.Clock(),
	}); err != nil {
		return fmt.Errorf("planning: record a %s event: %w", e.Kind, err)
	}
	return nil
}

// recordChanges records e with c as its fields; nothing
// when c is empty, since a write that changed nothing has nothing to say.
func (s *server) recordChanges(ctx context.Context, txq *store.Queries, who actor, e event, c changes) error {
	if len(c) == 0 {
		return nil
	}
	e.Payload = map[string]any{"fields": c}
	return s.recordEvent(ctx, txq, who, e)
}

// The kinds of an allocation's events (S23): one per write, a status change
// recorded on its own beside the fields a replace changed.
const (
	eventAllocationCreated     = "allocation_created"
	eventAllocationChanged     = "allocation_changed"
	eventAllocationConfirmed   = "allocation_confirmed"
	eventAllocationUnconfirmed = "allocation_unconfirmed"
	eventAllocationDeleted     = "allocation_deleted"
)

// The kinds of a request's events (S23, S16): created and changed carry the
// fields; filled names the allocation and its person, reopened the deleted
// allocation, withdrawn the status's before and after.
const (
	eventRequestCreated   = "request_created"
	eventRequestChanged   = "request_changed"
	eventRequestFilled    = "request_filled"
	eventRequestReopened  = "request_reopened"
	eventRequestWithdrawn = "request_withdrawn"
)
