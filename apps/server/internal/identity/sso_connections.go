package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// ownerManagement is the rule SSOConnections checks its caller against: the
// connection's own writes' (PostIdentityOwnerUsersByIdSsoLink).
var ownerManagement = contracts.Rule{Kind: contracts.RulePolicy, Names: []string{"Owner", "OwnerManagement"}}

// ssoConnections is this module's contracts.SSOConnections (Entra phase 2,
// delivery B; plan reading 1): what connecting existing users to the
// workforce provider reads of identity. It holds the queries and Access, by
// which it gates every call itself (module-boundaries rule 12), and
// nothing it remembers between calls.
type ssoConnections struct {
	access *Access
	q      *store.Queries
}

var _ contracts.SSOConnections = (*ssoConnections)(nil)

// newSSOConnections is Module's SSOConnections: the constructor Compose
// calls with the dependencies it was given.
func newSSOConnections(a *Access, d module.Deps) contracts.SSOConnections {
	return &ssoConnections{access: a, q: store.New(d.Pool)}
}

// owner is the caller and the configured issuer for a read, or ok false
// when the read answers nothing: a context with no request, a caller the
// request does not admit under policy:Owner+OwnerManagement, or OIDC off.
// The issuer is the configured authority normalised, as an OIDC link and an
// announced link are stored under it.
func (c *ssoConnections) owner(ctx context.Context) (caller uuid.UUID, issuer string, ok bool, err error) {
	r, found := contracts.RequestFrom(ctx)
	if !found || r == nil {
		return uuid.Nil, "", false, nil
	}
	p, err := c.access.Check(r, ownerManagement)
	if errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden) {
		return uuid.Nil, "", false, nil
	}
	if err != nil {
		return uuid.Nil, "", false, fmt.Errorf("identity: SSO connections: %w", err)
	}
	settings, err := c.access.sso.get(ctx)
	if err != nil {
		return uuid.Nil, "", false, fmt.Errorf("identity: SSO connections: %w", err)
	}
	if settings.OIDC == nil {
		return uuid.Nil, "", false, nil
	}
	issuer, valid := normalizeIssuer(settings.OIDC.Authority)
	if !valid {
		return uuid.Nil, "", false, errors.New("identity: SSO connections: the OIDC authority is not a valid issuer")
	}
	return p.UserID, issuer, true, nil
}

// Unconnected lists the users not connected under the configured issuer,
// for an Owner (contracts.SSOConnections). An address identity made up for
// an account the provider created is answered as none.
func (c *ssoConnections) Unconnected(ctx context.Context) ([]contracts.ConnectCandidate, error) {
	caller, issuer, ok, err := c.owner(ctx)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := c.q.ListConnectCandidates(ctx, store.ListConnectCandidatesParams{Caller: caller, OidcIssuer: issuer})
	if err != nil {
		return nil, fmt.Errorf("identity: SSO connections: list the candidates: %w", err)
	}
	out := make([]contracts.ConnectCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, contracts.ConnectCandidate{
			UserID: row.ID, Email: deref(publicEmail(row.Email)), EmployeeNumber: deref(row.EmployeeNumber), Disabled: row.IsDisabled,
		})
	}
	return out, nil
}

// holderRank orders a key's holders: an announced link, then a SCIM
// mapping, then an announced account.
var holderRank = map[string]int{contracts.KeyHolderExpected: 0, contracts.KeyHolderMapped: 1, contracts.KeyHolderAccount: 2}

// KeyHolders answers who holds each of keys under the configured issuer,
// for an Owner (contracts.SSOConnections). Each key is normalised as
// identity stores it — the object id as SCIM's externalId, the principal
// name as emails are — and compared with the holders' stored keys.
func (c *ssoConnections) KeyHolders(ctx context.Context, keys []contracts.DirectoryKey) ([]contracts.KeyHolder, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	_, issuer, ok, err := c.owner(ctx)
	if err != nil || !ok {
		return nil, err
	}
	type normalised struct{ object, upn string }
	norm := make([]normalised, len(keys))
	objects, upns := []string{}, []string{}
	for i, k := range keys {
		if raw := strings.TrimSpace(k.ObjectID); raw != "" {
			norm[i].object = *normalizeExternalID(&raw)
			objects = append(objects, norm[i].object)
		}
		if strings.TrimSpace(k.UserPrincipalName) != "" {
			norm[i].upn = normalizeEmail(k.UserPrincipalName)
			upns = append(upns, norm[i].upn)
		}
	}
	if len(objects) == 0 && len(upns) == 0 {
		return nil, nil
	}
	rows, err := c.q.ListKeyHolders(ctx, store.ListKeyHoldersParams{Issuer: issuer, ObjectIds: objects, Upns: upns})
	if err != nil {
		return nil, fmt.Errorf("identity: SSO connections: list the key holders: %w", err)
	}
	var out []contracts.KeyHolder
	for i, k := range keys {
		var best *store.ListKeyHoldersRow
		for j := range rows {
			row := &rows[j]
			named := (norm[i].object != "" && deref(row.ObjectID) == norm[i].object) ||
				(norm[i].upn != "" && deref(row.UserPrincipalName) == norm[i].upn)
			if named && (best == nil || holderRank[row.Kind] < holderRank[best.Kind]) {
				best = row
			}
		}
		if best == nil {
			continue
		}
		h := contracts.KeyHolder{ObjectID: k.ObjectID, UserPrincipalName: k.UserPrincipalName, Kind: best.Kind}
		if best.Kind != contracts.KeyHolderAccount && best.UserID != nil {
			id := *best.UserID
			h.UserID = &id
		}
		out = append(out, h)
	}
	return out, nil
}
