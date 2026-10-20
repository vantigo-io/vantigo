package entra

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the module's own trail (Entra design D13): entra.events, one
// row per write the module made to its connection or in the tenant — who
// acted (a user or a system, exactly one), what, which user by object id and
// the UPN the module made or found, and codes. Never an address beyond the
// UPN, a secret, a token or a pass, and never the directory's words. It is
// append-only: nothing here updates or deletes a row.

// The event actions of the connection's writes. The lifecycle's
// (entra.user.*, entra.pass.*) are the provisioner's.
const (
	actionConnectionChanged = "entra.connection.changed"
	actionConnectionTested  = "entra.connection.tested"
	actionSettingsChanged   = "entra.settings.changed"
	actionCredentialAdded   = "entra.credential.added"
	actionCredentialRevoked = "entra.credential.revoked"
)

// The events list's bounds.
const (
	defaultEventsLimit = 50
	maxEventsLimit     = 200
)

// recordEvent appends one event at now by actor, which must name exactly one
// user or system (contracts.ErrInvalidActor otherwise). objectID and upn are
// the user the write concerned, nil and "" on a connection's event; codes
// are what it reported, codes only.
func recordEvent(ctx context.Context, q *store.Queries, now time.Time, actor contracts.Actor, action string, objectID *uuid.UUID, upn string, codes []string) error {
	if !actor.Valid() {
		return fmt.Errorf("entra: record %s: %w", action, contracts.ErrInvalidActor)
	}
	p := store.InsertEventParams{At: now, ActorUserID: actor.UserID, Action: action, ObjectID: objectID, Codes: codes}
	if actor.UserID == nil {
		system := actor.System
		p.ActorSystem = &system
	}
	if upn != "" {
		p.Upn = &upn
	}
	if p.Codes == nil {
		p.Codes = []string{}
	}
	if err := q.InsertEvent(ctx, p); err != nil {
		return fmt.Errorf("entra: record %s: %w", action, err)
	}
	return nil
}

// requestActor is the signed-in caller of one request as an actor. Every
// operation of the module is authenticated by the router, so a request
// without a user is a fault, not a caller.
func requestActor(ctx context.Context) (contracts.Actor, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil {
		return contracts.Actor{}, fmt.Errorf("entra: a write without a signed-in user: %w", contracts.ErrInvalidActor)
	}
	id := p.UserID
	return contracts.Actor{UserID: &id}, nil
}

// GetEntraEvents answers the newest events, limit of them (50 when absent,
// 1 to 200). (GET /api/v1/entra/events)
func (s *server) GetEntraEvents(ctx context.Context, req gen.GetEntraEventsRequestObject) (gen.GetEntraEventsResponseObject, error) {
	limit := int32(defaultEventsLimit)
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if limit < 1 || limit > maxEventsLimit {
		var f fieldErrors
		f.add("limit", fmt.Sprintf("The limit is 1 to %d.", maxEventsLimit))
		return gen.GetEntraEvents400ApplicationProblemPlusJSONResponse(invalid("Invalid limit", f)), nil
	}
	rows, err := s.q.ListEvents(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("entra: list the events: %w", err)
	}
	out := make(gen.GetEntraEvents200JSONResponse, 0, len(rows))
	for _, e := range rows {
		ev := gen.EntraEvent{Id: e.ID, At: e.At, Action: e.Action, Codes: e.Codes, Upn: e.Upn}
		switch {
		case e.ActorUserID != nil:
			ev.Actor = e.ActorUserID.String()
		case e.ActorSystem != nil:
			ev.Actor = *e.ActorSystem
		}
		if e.ObjectID != nil {
			id := openapi_types.UUID(*e.ObjectID)
			ev.ObjectId = &id
		}
		if ev.Codes == nil {
			ev.Codes = []string{}
		}
		out = append(out, ev)
	}
	return out, nil
}

// GetEntraAccounts answers every account the module keeps, by UPN — never
// the pass, only whether one waits. (GET /api/v1/entra/accounts)
func (s *server) GetEntraAccounts(ctx context.Context, _ gen.GetEntraAccountsRequestObject) (gen.GetEntraAccountsResponseObject, error) {
	rows, err := s.q.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: list the accounts: %w", err)
	}
	now := s.deps.Clock()
	out := make(gen.GetEntraAccounts200JSONResponse, 0, len(rows))
	for _, a := range rows {
		reported := a.Reported
		if reported == nil {
			reported = []string{}
		}
		out = append(out, gen.EntraAccount{
			ObjectId:          openapi_types.UUID(a.ObjectID),
			EmployeeNumber:    a.EmployeeNumber,
			UserPrincipalName: a.UserPrincipalName,
			State:             gen.EntraAccountState(a.State),
			CreatedByUs:       a.CreatedByUs,
			HasPass:           a.PassSealed != nil && a.PassExpiresAt != nil && a.PassExpiresAt.After(now),
			Reported:          reported,
			LastSyncedAt:      a.LastSyncedAt,
			CreatedAt:         a.CreatedAt,
		})
	}
	return out, nil
}
