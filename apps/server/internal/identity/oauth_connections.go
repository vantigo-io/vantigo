package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is an integration's connected apps (OAuth connections design
// C18, C25, A17, A20): the Owner's list and Revoke, and the revocations that
// follow from a change elsewhere — the integration archived, or the person
// who authorized a connection deleted, disabled or no longer an Owner.

var connectionNotFound = refuse(http.StatusNotFound, "not_found", "No such connection.", nil)

// GetIdentityIntegrationsByIdConnections lists an integration's connected
// apps: activated, unrevoked and unexpired, each active or inactive by its
// authorizer (A17).
// (GET /api/v1/identity/integrations/{id}/connections)
func (s *server) GetIdentityIntegrationsByIdConnections(ctx context.Context, req gen.GetIdentityIntegrationsByIdConnectionsRequestObject) (gen.GetIdentityIntegrationsByIdConnectionsResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	exists, err := s.q.IntegrationExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("identity: read the integration: %w", err)
	}
	if !exists {
		return integrationNotFound, nil
	}
	sso, err := s.access.sso.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: read whether SCIM is on: %w", err)
	}
	rows, err := s.q.ListIntegrationOAuthConnections(ctx, store.ListIntegrationOAuthConnectionsParams{
		IntegrationID: req.Id, Now: s.deps.Clock(), ScimEnabled: sso.SCIM != nil,
	})
	if err != nil {
		return nil, fmt.Errorf("identity: list the connections: %w", err)
	}
	out := gen.IntegrationConnectionList{Connections: make([]gen.IntegrationConnection, 0, len(rows))}
	for _, c := range rows {
		status, reason := gen.IntegrationConnectionStatus("active"), (*string)(nil)
		if !c.AuthorizerActive {
			status = "inactive"
			r := revokeReasonAuthorizerRemoved
			reason = &r
		}
		connected := c.ExpiresAt
		if c.ActivatedAt != nil {
			connected = *c.ActivatedAt
		}
		out.Connections = append(out.Connections, gen.IntegrationConnection{
			Id: c.ID, ClientId: c.ClientID, ClientKind: gen.IntegrationConnectionClientKind(c.ClientKind), ClientName: c.ClientName,
			ClientHost: c.ClientHost, Scopes: nonNil(c.Scopes), AuthorizedBy: c.AuthorizedBy, AuthorizedByName: c.AuthorizedByName,
			ConnectedAt: connected, ExpiresAt: c.ExpiresAt, LastUsedAt: c.LastUsedAt, LastUsedIp: c.LastUsedIp,
			Status: status, InactiveReason: reason,
		})
	}
	return gen.GetIdentityIntegrationsByIdConnections200JSONResponse(out), nil
}

// DeleteIdentityIntegrationsByIdConnectionsByConnectionId revokes a
// connection, audited connection.revoked (C18); one ended already is
// answered as it is.
// (DELETE /api/v1/identity/integrations/{id}/connections/{connectionId})
func (s *server) DeleteIdentityIntegrationsByIdConnectionsByConnectionId(ctx context.Context, req gen.DeleteIdentityIntegrationsByIdConnectionsByConnectionIdRequestObject) (gen.DeleteIdentityIntegrationsByIdConnectionsByConnectionIdResponseObject, error) {
	if !s.deps.Config.OAuthEnabled {
		return oauthNotFound, nil
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	err = s.integrationTx(ctx, req.Id, true, func(q *store.Queries, _ store.LockIntegrationRow, now time.Time) error {
		c, err := q.LockIntegrationOAuthConnection(ctx, store.LockIntegrationOAuthConnectionParams{ID: req.ConnectionId, IntegrationID: req.Id})
		if errors.Is(err, pgx.ErrNoRows) {
			return connectionNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: read the connection: %w", err)
		}
		if c.RevokedAt != nil {
			return nil
		}
		revoked, err := q.RevokeOAuthConnection(ctx, store.RevokeOAuthConnectionParams{ID: c.ID, Now: now, RevokedBy: &p.UserID, Reason: revokeReasonRevoked})
		if err != nil {
			return fmt.Errorf("identity: revoke the connection: %w", err)
		}
		return auditRevoked(ctx, q, r, revoked, &p.UserID, "", p.MFAVerified, now)
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityIntegrationsByIdConnectionsByConnectionIdResponseObject](err)
	}
	return gen.DeleteIdentityIntegrationsByIdConnectionsByConnectionId204Response{}, nil
}

// revokeIntegrationConnections ends every live connection of an integration
// being archived, on its transaction, each audited (A20).
func revokeIntegrationConnections(ctx context.Context, q *store.Queries, r *http.Request, integrationID uuid.UUID, p contracts.Principal, now time.Time) error {
	revoked, err := q.RevokeIntegrationOAuthConnections(ctx, store.RevokeIntegrationOAuthConnectionsParams{
		IntegrationID: integrationID, Now: now, RevokedBy: &p.UserID, Reason: revokeReasonIntegrationArchived,
	})
	if err != nil {
		return fmt.Errorf("identity: revoke the integration's connections: %w", err)
	}
	for _, c := range revoked {
		if err := auditRevoked(ctx, q, r, c, &p.UserID, "", p.MFAVerified, now); err != nil {
			return err
		}
	}
	return nil
}

// revokeAuthorizerConnections ends, on the caller's transaction, the live
// connections userID authorized (A17), audited connection.revoked with the
// reason authorizer_removed: all of them when unconditional (the person is
// deleted or disabled), else those whose authorizer no longer qualifies (the
// Owner role removed). actor and system say who made the change. The token
// check refuses such a connection whether or not this runs; the row is for
// the audit and the list.
func (a *Access) revokeAuthorizerConnections(ctx context.Context, q *store.Queries, r *http.Request, actor *uuid.UUID, system string, userID uuid.UUID, unconditional bool, now time.Time) error {
	scim := false
	if !unconditional {
		sso, err := a.sso.get(ctx)
		if err != nil {
			return fmt.Errorf("identity: read whether SCIM is on: %w", err)
		}
		scim = sso.SCIM != nil
	}
	revoked, err := q.RevokeAuthorizerOAuthConnections(ctx, store.RevokeAuthorizerOAuthConnectionsParams{
		Now: now, RevokedBy: actor, AuthorizedBy: &userID, Unconditional: unconditional, ScimEnabled: scim,
	})
	if err != nil {
		return fmt.Errorf("identity: revoke the person's connections: %w", err)
	}
	for _, c := range revoked {
		if err := auditRevoked(ctx, q, r, c, actor, system, false, now); err != nil {
			return err
		}
	}
	return nil
}

func (r refusal) VisitGetIdentityIntegrationsByIdConnectionsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityIntegrationsByIdConnectionsByConnectionIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
