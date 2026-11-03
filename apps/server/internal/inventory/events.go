package inventory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The record (design I31): every structural write — a site or a location
// created, changed, re-parented, archived, reactivated or deleted — is one
// row in inventory.events, with the site it touches and the actor
// snapshotted as Planning keeps it. Movements are their own record and are
// not repeated here.

// The entity kinds an event is about.
const (
	entitySite     = "site"
	entityLocation = "location"
)

const (
	actorKindUser          = "user"
	actorKindSystem        = "system"
	unknownUserDisplay     = "Unknown user"
	systemActorDisplayName = "System"
)

// actor is who an event is attributed to: the three values every event
// carries (actor_kind, actor_display, actor_user_id), resolved once per
// request — Planning's shape (internal/planning/events.go), copied because no
// module imports another.
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
		return actor{}, fmt.Errorf("inventory: resolve the actor: %w", err)
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

// event is one row of the record before it is written: what it is about,
// the site it touches, and what it says.
type event struct {
	EntityKind string
	EntityID   int64
	SiteID     *int64
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
		return fmt.Errorf("inventory: encode a %s event: %w", e.Kind, err)
	}
	display := who.Display
	if err := txq.InsertEvent(ctx, store.InsertEventParams{
		EntityKind: e.EntityKind, EntityID: e.EntityID, SiteID: e.SiteID,
		Kind: e.Kind, Payload: body, ActorKind: who.Kind, ActorUserID: who.UserID, ActorDisplay: &display,
		OccurredAt: s.deps.Clock(),
	}); err != nil {
		return fmt.Errorf("inventory: record a %s event: %w", e.Kind, err)
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

// The kinds of a site's events (I31): created carries the fields; changed
// the fields a replace changed; archived and reactivated the status change
// alone; deleted the code the site had.
const (
	eventSiteCreated     = "site_created"
	eventSiteChanged     = "site_changed"
	eventSiteArchived    = "site_archived"
	eventSiteReactivated = "site_reactivated"
	eventSiteDeleted     = "site_deleted"
)

// The kinds of a location's events (I31, I21): re-parented names the parent
// before and after beside the full code, so the location page can offer a
// label reprint; bulk_created is one event on the parent of a bulk create,
// naming every full code it made.
const (
	eventLocationCreated     = "location_created"
	eventLocationChanged     = "location_changed"
	eventLocationReparented  = "location_reparented"
	eventLocationArchived    = "location_archived"
	eventLocationReactivated = "location_reactivated"
	eventLocationDeleted     = "location_deleted"
	eventLocationsBulk       = "locations_bulk_created"
)
