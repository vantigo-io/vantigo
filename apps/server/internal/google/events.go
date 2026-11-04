package google

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the module's own trail (Google Workspace design G16):
// google.events, one row per write the module made to its connection or at
// Google — who acted (a user or a system, exactly one), what, which Google id
// it concerned, and codes. Never an address, a key, a secret or a token, and
// never Google's words. It is append-only: nothing here updates or deletes a
// row.

// The event actions.
const (
	actionConnectionChanged  = "google.connection.changed"
	actionConnectionTested   = "google.connection.tested"
	actionOAuthClientChanged = "google.oauth_client.changed"
	actionCredentialAdded    = "google.credential.added"
	actionCredentialRevoked  = "google.credential.revoked"
	actionCredentialRefused  = "google.credential.refused"
)

// systemActor is the module's own system actor: what the module does with no
// person behind it — a key Google refused on a call nobody clicked.
const systemActor = "system:google"

// validActor reports whether a names exactly one actor: one
// contracts.Actor.Valid accepts, or the module's own systemActor.
func validActor(a contracts.Actor) bool {
	return a.Valid() || (a.UserID == nil && a.System == systemActor)
}

// The events list's bounds.
const (
	defaultEventsLimit = 50
	maxEventsLimit     = 200
)

// recordEvent appends one event at now by actor, which must name exactly one
// user or system (contracts.ErrInvalidActor otherwise). subject is the
// Google id the write concerned, "" on a connection's event; codes are what
// it reported, codes only.
func recordEvent(ctx context.Context, q *store.Queries, now time.Time, actor contracts.Actor, action, subject string, codes []string) error {
	if !validActor(actor) {
		return fmt.Errorf("google: record %s: %w", action, contracts.ErrInvalidActor)
	}
	p := store.InsertEventParams{At: now, ActorUserID: actor.UserID, Action: action, Codes: codes}
	if actor.UserID == nil {
		system := actor.System
		p.ActorSystem = &system
	}
	if subject != "" {
		p.SubjectID = &subject
	}
	if p.Codes == nil {
		p.Codes = []string{}
	}
	if err := q.InsertEvent(ctx, p); err != nil {
		return fmt.Errorf("google: record %s: %w", action, err)
	}
	return nil
}

// requestActor is the signed-in caller of one request as an actor. Every
// operation of the module is authenticated by the router, so a request
// without a user is a fault, not a caller.
func requestActor(ctx context.Context) (contracts.Actor, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return contracts.Actor{}, fmt.Errorf("google: a write without a signed-in user: %w", contracts.ErrInvalidActor)
	}
	id := p.UserID
	return contracts.Actor{UserID: &id}, nil
}

// GetGoogleEvents answers the newest events, limit of them (50 when absent,
// 1 to 200). (GET /api/v1/google/events)
func (s *server) GetGoogleEvents(ctx context.Context, req gen.GetGoogleEventsRequestObject) (gen.GetGoogleEventsResponseObject, error) {
	limit := int32(defaultEventsLimit)
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if limit < 1 || limit > maxEventsLimit {
		var f fieldErrors
		f.add("limit", fmt.Sprintf("The limit is 1 to %d.", maxEventsLimit))
		return gen.GetGoogleEvents400ApplicationProblemPlusJSONResponse(invalid("Invalid limit", f)), nil
	}
	rows, err := s.q.ListEvents(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("google: list the events: %w", err)
	}
	out := make(gen.GetGoogleEvents200JSONResponse, 0, len(rows))
	for _, e := range rows {
		ev := gen.GoogleEvent{Id: e.ID, At: e.At, Action: e.Action, Codes: e.Codes, SubjectId: e.SubjectID}
		switch {
		case e.ActorUserID != nil:
			ev.Actor = e.ActorUserID.String()
		case e.ActorSystem != nil:
			ev.Actor = *e.ActorSystem
		}
		if ev.Codes == nil {
			ev.Codes = []string{}
		}
		out = append(out, ev)
	}
	return out, nil
}

// The person's own Google connection's actions (delivery D, G25–G28): the
// switch, and each move of a person's row, its subject the Google id.
const (
	actionPersonalSwitched        = "google.personal.switched"
	actionPersonalConnected       = "google.personal.connected"
	actionPersonalDisconnected    = "google.personal.disconnected"
	actionPersonalReconnectNeeded = "google.personal.reconnect_needed"
	actionPersonalClosed          = "google.personal.closed"
	actionPersonalDeleted         = "google.personal.deleted"
)

// Mail channels' action (delivery F, G33): the switch.
const actionMailChannelsSwitched = "google.mail_channels.switched"
