package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the module's own trail (Entra design D13): entra.events, one
// row per write the module made to its connection or in the tenant — who
// acted (a user or a system, exactly one), what, which user by object id and
// the UPN the module made or found, and codes. Never an address beyond the
// UPN, a secret, a token or a pass, and never the directory's words. It is
// append-only: nothing here updates or deletes a row.

// The event actions of the connection's writes and of an adoption's
// confirmation. The lifecycle's (entra.user.*, entra.pass.*) are the
// provisioner's.
const (
	actionConnectionChanged = "entra.connection.changed"
	actionConnectionTested  = "entra.connection.tested"
	actionSettingsChanged   = "entra.settings.changed"
	actionCredentialAdded   = "entra.credential.added"
	actionCredentialRevoked = "entra.credential.revoked"
	actionAdoptionConfirmed = "entra.account.adoption_confirmed"
)

// The Outlook connection's event actions (Outlook connection design M3, M5,
// M7): a person connected, disconnected; a connection Microsoft refused,
// waiting for its person, its code the reason (expired, sign_in_again, mfa,
// blocked, consent_missing); a connection closed for another reason, its
// code saying which (leaver, deleted, disabled, switch_off, repointed: the
// person's account changed in Staff); and a closing connection deleted by
// entra-outlook-sweep. Never a token, a code from Microsoft's words, or an
// address beyond the account's UPN.
const (
	actionOutlookConnected    = "entra.outlook.connected"
	actionOutlookDisconnected = "entra.outlook.disconnected"
	actionOutlookReconnect    = "entra.outlook.reconnect_needed"
	actionOutlookClosed       = "entra.outlook.closed"
	actionOutlookDeleted      = "entra.outlook.deleted"
)

// outlookSystemActor is the module's own system actor: what the Outlook
// connection does with no person behind the request — a refresh Microsoft
// refused, a connection closed on use or by the sweep, a spent one
// deleted. Identity's audit never sees it, so it stays Entra's, beside
// Staff's two that contracts.Actor knows.
const outlookSystemActor = "system:entra-outlook"

// outlookActor is outlookSystemActor as an actor.
var outlookActor = contracts.Actor{System: outlookSystemActor}

// validActor reports whether a names exactly one actor: one
// contracts.Actor.Valid accepts, or the module's own outlookSystemActor.
func validActor(a contracts.Actor) bool {
	return a.Valid() || (a.UserID == nil && a.System == outlookSystemActor)
}

// The events list's bounds.
const (
	defaultEventsLimit = 50
	maxEventsLimit     = 200
)

// recordEvent appends one event at now by actor, which must name exactly one
// user or system — validActor's (contracts.ErrInvalidActor otherwise). objectID and upn are
// the user the write concerned, nil and "" on a connection's event; codes
// are what it reported, codes only.
func recordEvent(ctx context.Context, q *store.Queries, now time.Time, actor contracts.Actor, action string, objectID *uuid.UUID, upn string, codes []string) error {
	if !validActor(actor) {
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
		out = append(out, accountBody(a, now))
	}
	return out, nil
}

// accountBody is an account's row as the contract answers it at now.
func accountBody(a store.EntraAccount, now time.Time) gen.EntraAccount {
	return gen.EntraAccount{
		ObjectId:          openapi_types.UUID(a.ObjectID),
		EmployeeNumber:    a.EmployeeNumber,
		UserPrincipalName: a.UserPrincipalName,
		State:             gen.EntraAccountState(a.State),
		CreatedByUs:       a.CreatedByUs,
		AdoptionConfirmed: a.AdoptionConfirmedAt != nil,
		HasPass:           livePass(a, now),
		Reported:          orEmpty(a.Reported),
		LastSyncedAt:      a.LastSyncedAt,
		CreatedAt:         a.CreatedAt,
	}
}

// PostEntraAccountsByObjectIdConfirm confirms the adoption of a user the
// module found rather than created (plan reading 8): from now on the
// lifecycle writes to it as to one it created, and the next Prepare or
// Update brings it to the person's spec. The confirmation and the actor are
// kept on the row and audited entra.account.adoption_confirmed. 404 for an
// account the module does not keep; 409 adoption_confirmed for one
// confirmed already or one the module created, which needs none.
// (POST /api/v1/entra/accounts/{objectId}/confirm)
func (s *server) PostEntraAccountsByObjectIdConfirm(ctx context.Context, req gen.PostEntraAccountsByObjectIdConfirmRequestObject) (gen.PostEntraAccountsByObjectIdConfirmResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	id := uuid.UUID(req.ObjectId)
	now := s.deps.Clock()
	var (
		row   store.EntraAccount
		found bool
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		row, err = q.ConfirmAdoption(ctx, store.ConfirmAdoptionParams{Now: now, ConfirmedByUserID: actor.UserID, ObjectID: id})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := q.GetAccount(ctx, id); errors.Is(err, pgx.ErrNoRows) {
				return nil
			} else if err != nil {
				return fmt.Errorf("entra: read the account: %w", err)
			}
			found = true
			return refusal{adoptionConfirmed()}
		case err != nil:
			return fmt.Errorf("entra: confirm the adoption: %w", err)
		}
		found = true
		return recordEvent(ctx, q, now, actor, actionAdoptionConfirmed, &id, row.UserPrincipalName, nil)
	})
	var r refusal
	switch {
	case errors.As(err, &r) && r.status() == http.StatusConflict:
		return gen.PostEntraAccountsByObjectIdConfirm409ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	case !found:
		return gen.PostEntraAccountsByObjectIdConfirm404Response{}, nil
	}
	return gen.PostEntraAccountsByObjectIdConfirm200JSONResponse(accountBody(row, now)), nil
}
