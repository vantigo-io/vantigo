package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// accessDelegationsPath is the delegation collection: the Location of a
// created delegation.
const accessDelegationsPath = "/api/v1/identity/access/delegations"

// The delegation refusals, each the flat CodeMessageError .NET answered
// with (EA/AuthorizationManagementEndpoints.cs, "EA/AMS").
var (
	invalidDelegation            = refuseFlat(http.StatusBadRequest, "invalid_delegation", "A delegation must target another user and have valid scope.")                // EA/AMS:726-728
	invalidDelegationTarget      = refuseFlat(http.StatusBadRequest, "invalid_delegation_target", "Owner accounts cannot be delegated.")                                 // EA/AMS:731-733
	invalidDelegationPermissions = refuseFlat(http.StatusBadRequest, "invalid_delegation_permissions", "Delegations may contain only registered delegable permissions.") // EA/AMS:734-735
	invalidDelegationRoles       = refuseFlat(http.StatusBadRequest, "invalid_delegation_roles", "Only custom roles may be stewarded.")                                  // EA/AMS:736-738
	delegationVersionRequired    = refuseFlat(http.StatusConflict, "delegation_conflict", "A delegation version is required.")                                           // EA/AMS:639

	// delegationChanged is both a stale or missing stamp (EA/AMS:642-643,
	// :693-694) and each delegation handler's own catch of a lost race
	// (:620-623, :675, :715): .NET answered both alike.
	delegationChanged = refuseFlat(http.StatusConflict, "delegation_conflict", "The delegation changed concurrently.")
)

// scope is one active, valid delegation's authority, .NET's
// AuthorizationScope (AZ/AuthorizationMutationService.cs:305-310, "AZ/AMS").
// A mutation a delegate makes must lie wholly within one scope, so disjoint
// scopes never combine into authority neither grants.
type scope struct {
	id             uuid.UUID
	canCreateRoles bool
	keys           []string    // the permission keys it may grant, ordinal
	stewarded      []uuid.UUID // the custom roles it stewards, by id
	assignable     []uuid.UUID // the stewarded roles whose every current key lies within it
}

func (sc scope) stewards(roleID uuid.UUID) bool { return slices.Contains(sc.stewarded, roleID) }

func (sc scope) assigns(roleID uuid.UUID) bool { return slices.Contains(sc.assignable, roleID) }

// within is KeysWithinScope (AZ/AMS:265-267): every key is in the catalog,
// may be delegated, and is one sc grants.
func (sc scope) within(catalog map[string]contracts.Permission, keys []string) bool {
	return !slices.ContainsFunc(keys, func(key string) bool {
		return !delegable(catalog, key) || !slices.Contains(sc.keys, key)
	})
}

// delegable reports whether key is in the catalog and may be delegated.
func delegable(catalog map[string]contracts.Permission, key string) bool {
	p, ok := catalog[key]
	return ok && p.Delegable
}

// findScope is the scope of delegation id among scopes, or nil.
func findScope(scopes []scope, id uuid.UUID) *scope {
	i := slices.IndexFunc(scopes, func(sc scope) bool { return sc.id == id })
	if i < 0 {
		return nil
	}
	return &scopes[i]
}

// loadScopes is ActiveScopesAsync's evaluation (AZ/AMS:168-236), and
// AuthorizationManagementHandler's (EA/AuthAuthorization.cs:127-173), of
// the delegations ids that are still active at now, each on its own. A
// delegation that stewards a role that no longer exists or is protected,
// or holds a key the catalog lacks or does not let be delegated, is
// dropped: it fails closed, granting nothing. A valid scope's assignable
// roles are the stewarded roles whose current keys all lie within it (a
// role without keys among them).
func loadScopes(ctx context.Context, q *store.Queries, catalog map[string]contracts.Permission, ids []uuid.UUID, now time.Time) ([]scope, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	delegations, err := q.DelegationScopes(ctx, store.DelegationScopesParams{Ids: ids, Now: now})
	if err != nil {
		return nil, fmt.Errorf("identity: delegation scopes: %w", err)
	}
	var roleIDs []uuid.UUID
	for _, d := range delegations {
		roleIDs = append(roleIDs, d.RoleIds...)
	}
	rows, err := q.ScopeRoles(ctx, roleIDs)
	if err != nil {
		return nil, fmt.Errorf("identity: delegation scopes: %w", err)
	}
	roles := make(map[uuid.UUID]store.ScopeRolesRow, len(rows))
	for _, r := range rows {
		roles[r.ID] = r
	}

	var scopes []scope
	for _, d := range delegations {
		dangling := slices.ContainsFunc(d.RoleIds, func(id uuid.UUID) bool {
			r, ok := roles[id]
			return !ok || r.IsSystem || r.IsBuiltIn
		})
		undelegable := slices.ContainsFunc(d.PermissionKeys, func(key string) bool { return !delegable(catalog, key) })
		if dangling || undelegable {
			continue
		}
		sc := scope{id: d.ID, canCreateRoles: d.CanCreateRoles, keys: nonNil(d.PermissionKeys), stewarded: nonNil(d.RoleIds), assignable: []uuid.UUID{}}
		for _, id := range sc.stewarded {
			if sc.within(catalog, roles[id].PermissionKeys) {
				sc.assignable = append(sc.assignable, id)
			}
		}
		scopes = append(scopes, sc)
	}
	return scopes, nil
}

// activeScopes is the scopes of userID's delegations active at now, read
// without locks: ActiveScopesAsync (AZ/AMS:168-236) for a user who is not
// an Owner. An Owner acts directly and holds no scope; every caller has
// already answered for an Owner before it asks.
func activeScopes(ctx context.Context, q *store.Queries, catalog map[string]contracts.Permission, userID uuid.UUID, now time.Time) ([]scope, error) {
	ids, err := q.ActiveDelegationIDs(ctx, store.ActiveDelegationIDsParams{GranteeUserID: userID, Now: now})
	if err != nil {
		return nil, fmt.Errorf("identity: active delegations: %w", err)
	}
	return loadScopes(ctx, q, catalog, ids, now)
}

// heldDelegations opens every authorization mutation made by a caller who
// is not an Owner: it holds each of the caller's active delegations FOR
// SHARE until the transaction ends (LockActiveDelegations), before any
// other lock the mutation takes, and returns their ids for scopesOf. So a
// revoke or update of a delegation waits for a mutation made under it to
// commit, and a mutation queued behind an uncommitted revoke or update
// reads the delegation as that left it. An Owner acts directly and holds
// none.
func (s *server) heldDelegations(ctx context.Context, q *store.Queries, p contracts.Principal, now time.Time) ([]uuid.UUID, error) {
	if isOwner(p) {
		return nil, nil
	}
	ids, err := q.LockActiveDelegations(ctx, store.LockActiveDelegationsParams{GranteeUserID: p.UserID, Now: now})
	if err != nil {
		return nil, fmt.Errorf("identity: hold delegations: %w", err)
	}
	return ids, nil
}

// scopesOf evaluates the delegations heldDelegations holds.
func (s *server) scopesOf(ctx context.Context, q *store.Queries, held []uuid.UUID, now time.Time) ([]scope, error) {
	return loadScopes(ctx, q, s.deps.Catalog, held, now)
}

// delegationRequest is a DelegationRequest as it is validated and stored.
type delegationRequest struct {
	grantee        uuid.UUID
	expiresAt      *time.Time
	canCreateRoles bool
	keys           []string
	roleIDs        []uuid.UUID
}

// validateDelegation is .NET's ValidateDelegation (EA/AMS:719-740), in its
// order, on the caller's transaction q: 400 invalid_delegation for no
// body, the caller as grantee, no key or role list, or an expiry not after
// now; the bare 404 for an unknown grantee; 400 invalid_delegation_target
// for an Owner grantee; 400 invalid_delegation_permissions for a key the
// catalog lacks or does not let be delegated; and 400
// invalid_delegation_roles unless every role id names a custom role, a
// repeated id counting as a missing one, as .NET counted them. An absent
// grantee is the nil uuid and an absent flag false, as .NET bound them.
// The roles are held FOR KEY SHARE (LockStewardableRoles), so a role
// deletion cannot slip in between this check and the commit and leave the
// delegation naming a role that no longer exists.
func (s *server) validateDelegation(ctx context.Context, q *store.Queries, body *gen.DelegationRequest, actor uuid.UUID, now time.Time) (delegationRequest, error) {
	if body == nil || body.PermissionKeys == nil || body.StewardedRoleIds == nil {
		return delegationRequest{}, invalidDelegation
	}
	d := delegationRequest{keys: *body.PermissionKeys, roleIDs: *body.StewardedRoleIds}
	if body.GranteeUserId != nil {
		d.grantee = *body.GranteeUserId
	}
	if body.CanCreateRoles != nil {
		d.canCreateRoles = *body.CanCreateRoles
	}
	if body.ExpiresAt != nil {
		expires := body.ExpiresAt.UTC().Truncate(time.Microsecond) // as timestamptz stores it
		d.expiresAt = &expires
	}
	if d.grantee == actor || (d.expiresAt != nil && !d.expiresAt.After(now)) {
		return delegationRequest{}, invalidDelegation
	}
	if _, err := q.GetUserByID(ctx, d.grantee); errors.Is(err, pgx.ErrNoRows) {
		return delegationRequest{}, notFound
	} else if err != nil {
		return delegationRequest{}, err
	}
	owner, err := q.IsOwner(ctx, d.grantee)
	if err != nil || owner {
		return delegationRequest{}, cmpOr(err, error(invalidDelegationTarget))
	}
	if slices.ContainsFunc(d.keys, func(key string) bool { return !delegable(s.deps.Catalog, key) }) {
		return delegationRequest{}, invalidDelegationPermissions
	}
	roles, err := q.LockStewardableRoles(ctx, d.roleIDs)
	if err != nil {
		return delegationRequest{}, err
	}
	if len(roles) != len(d.roleIDs) || slices.ContainsFunc(roles, func(r store.LockStewardableRolesRow) bool { return r.IsSystem || r.IsBuiltIn }) {
		return delegationRequest{}, invalidDelegationRoles
	}
	return d, nil
}

// writeDelegationChildren gives delegation id d's keys and stewarded roles,
// each once.
func writeDelegationChildren(ctx context.Context, q *store.Queries, id uuid.UUID, d delegationRequest) error {
	if err := q.InsertDelegationPermissions(ctx, store.InsertDelegationPermissionsParams{DelegationID: id, Keys: d.keys}); err != nil {
		return err
	}
	return q.InsertDelegationRoles(ctx, store.InsertDelegationRolesParams{DelegationID: id, RoleIds: d.roleIDs})
}

// delegationAudit is a delegation's state in the delegation audit events,
// with the property names .NET's serializer wrote: its anonymous after
// objects (EA/AMS:602-611, :660-670, :702-712) and
// DelegationAuthorizationSnapshot, which CaptureDelegationAsync filled
// (AZ/AuthorizationAuditWriter.cs:100-127, :147-155), share one shape.
type delegationAudit struct {
	ID               uuid.UUID   `json:"Id"`
	GranteeUserID    uuid.UUID   `json:"GranteeUserId"`
	CreatedByUserID  uuid.UUID   `json:"CreatedByUserId"`
	ExpiresAt        *time.Time  `json:"ExpiresAt"`
	RevokedAt        *time.Time  `json:"RevokedAt"`
	CanCreateRoles   bool        `json:"CanCreateRoles"`
	PermissionKeys   []string    `json:"PermissionKeys"`
	StewardedRoleIDs []uuid.UUID `json:"StewardedRoleIds"`
}

// delegationCreatedBefore is delegation.created's before (EA/AMS:602).
type delegationCreatedBefore struct {
	Delegation *delegationAudit `json:"Delegation"`
}

// captureDelegation is CaptureDelegationAsync (AZ/AuthorizationAuditWriter.cs:100-127):
// d as stored, with its keys (ordinal) and stewarded role ids.
func captureDelegation(ctx context.Context, q *store.Queries, d store.IdentityAuthorizationDelegation) (delegationAudit, error) {
	children, err := q.DelegationChildren(ctx, d.ID)
	if err != nil {
		return delegationAudit{}, err
	}
	return delegationAudit{
		ID:               d.ID,
		GranteeUserID:    d.GranteeUserID,
		CreatedByUserID:  d.CreatedByUserID,
		ExpiresAt:        utc(d.ExpiresAt),
		RevokedAt:        utc(d.RevokedAt),
		CanCreateRoles:   d.CanCreateRoles,
		PermissionKeys:   nonNil(children.PermissionKeys),
		StewardedRoleIDs: nonNil(children.StewardedRoleIds),
	}, nil
}

// utc is t in UTC, or nil.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// lockDelegation reads delegation id and holds its row on q: the bare 404
// when there is none.
func lockDelegation(ctx context.Context, q *store.Queries, id uuid.UUID) (store.IdentityAuthorizationDelegation, error) {
	d, err := q.LockDelegation(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, notFound
	}
	return d, err
}

// GetIdentityAccessDelegations lists every delegation for an Owner, by id
// descending, with its keys and stewarded roles (EA/AMS:556-571).
func (s *server) GetIdentityAccessDelegations(ctx context.Context, _ gen.GetIdentityAccessDelegationsRequestObject) (gen.GetIdentityAccessDelegationsResponseObject, error) {
	rows, err := s.q.ListDelegations(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list delegations: %w", err)
	}
	out := make(gen.GetIdentityAccessDelegations200JSONResponse, len(rows))
	for i, d := range rows {
		out[i] = gen.DelegationResponse{
			Id:               d.ID,
			GranteeUserId:    d.GranteeUserID,
			ExpiresAt:        utc(d.ExpiresAt),
			RevokedAt:        utc(d.RevokedAt),
			Version:          d.Version.String(),
			CanCreateRoles:   d.CanCreateRoles,
			PermissionKeys:   nonNil(d.PermissionKeys),
			StewardedRoleIds: nonNil(d.StewardedRoleIds),
		}
	}
	return out, nil
}

// PostIdentityAccessDelegations lets an Owner delegate part of
// authorization management to another user (EA/AMS:573-624). In one
// transaction under the owner lock, so the grantee cannot become an Owner
// between the check and the commit: validateDelegation's refusals, then
// the delegation, its keys and stewarded roles each once, and
// delegation.created audited. 201 with its id, version and Location.
func (s *server) PostIdentityAccessDelegations(ctx context.Context, req gen.PostIdentityAccessDelegationsRequestObject) (gen.PostIdentityAccessDelegationsResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	id, version := uuid.New(), uuid.New()
	err = s.accessTx(ctx, delegationChanged, func(q *store.Queries) error {
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		now := s.deps.Clock()
		d, err := s.validateDelegation(ctx, q, req.Body, p.UserID, now)
		if err != nil {
			return err
		}
		if err := q.InsertDelegation(ctx, store.InsertDelegationParams{
			ID:              id,
			GranteeUserID:   d.grantee,
			CreatedByUserID: p.UserID,
			CanCreateRoles:  d.canCreateRoles,
			ExpiresAt:       d.expiresAt,
			Version:         version,
			Now:             now,
		}); err != nil {
			return err
		}
		if err := writeDelegationChildren(ctx, q, id, d); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:      &p.UserID,
			targetUser: &d.grantee,
			action:     "delegation.created",
			before:     delegationCreatedBefore{},
			after: delegationAudit{
				ID: id, GranteeUserID: d.grantee, CreatedByUserID: p.UserID, ExpiresAt: d.expiresAt,
				CanCreateRoles: d.canCreateRoles, PermissionKeys: d.keys, StewardedRoleIDs: d.roleIDs,
			},
			mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityAccessDelegationsResponseObject](err)
	}
	return delegationCreated{
		location: s.deps.Config.BasePath + accessDelegationsPath + "/" + id.String(),
		body:     gen.PostIdentityAccessDelegations201JSONResponse{Id: id, Version: version.String()},
	}, nil
}

// PutIdentityAccessDelegationsById replaces a delegation's grantee,
// expiry, flag, keys and stewarded roles (EA/AMS:626-677), in .NET's
// order: 409 delegation_conflict "A delegation version is required."
// without a stamp; then, in one transaction under the owner lock and
// holding the delegation's row, the bare 404 for an unknown delegation,
// 409 delegation_conflict unless the stamp is its version, and
// validateDelegation's refusals. The delegation gets its new state and
// version, a revoked one staying revoked, and delegation.updated is
// audited. 200 with its id and new version. No user's sessions end: every
// request evaluates delegations afresh.
func (s *server) PutIdentityAccessDelegationsById(ctx context.Context, req gen.PutIdentityAccessDelegationsByIdRequestObject) (gen.PutIdentityAccessDelegationsByIdResponseObject, error) {
	if req.Body == nil || req.Body.ConcurrencyStamp == nil {
		return delegationVersionRequired, nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	version := uuid.New()
	err = s.accessTx(ctx, delegationChanged, func(q *store.Queries) error {
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		current, err := lockDelegation(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if *req.Body.ConcurrencyStamp != current.Version.String() {
			return delegationChanged
		}
		now := s.deps.Clock()
		d, err := s.validateDelegation(ctx, q, req.Body, p.UserID, now)
		if err != nil {
			return err
		}
		before, err := captureDelegation(ctx, q, current)
		if err != nil {
			return err
		}
		changed, err := q.UpdateDelegation(ctx, store.UpdateDelegationParams{
			GranteeUserID:   d.grantee,
			CanCreateRoles:  d.canCreateRoles,
			ExpiresAt:       d.expiresAt,
			Version:         version,
			Now:             now,
			ID:              req.Id,
			ExpectedVersion: current.Version,
		})
		if err != nil {
			return err
		}
		if changed == 0 {
			return errConcurrentChange
		}
		if err := q.DeleteDelegationPermissions(ctx, req.Id); err != nil {
			return err
		}
		if err := q.DeleteDelegationRoles(ctx, req.Id); err != nil {
			return err
		}
		if err := writeDelegationChildren(ctx, q, req.Id, d); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:      &p.UserID,
			targetUser: &d.grantee,
			action:     "delegation.updated",
			before:     before,
			after: delegationAudit{
				ID: req.Id, GranteeUserID: d.grantee, CreatedByUserID: current.CreatedByUserID, ExpiresAt: d.expiresAt,
				RevokedAt: utc(current.RevokedAt), CanCreateRoles: d.canCreateRoles, PermissionKeys: d.keys, StewardedRoleIDs: d.roleIDs,
			},
			mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PutIdentityAccessDelegationsByIdResponseObject](err)
	}
	return gen.PutIdentityAccessDelegationsById200JSONResponse{Id: req.Id, Version: version.String()}, nil
}

// PostIdentityAccessDelegationsByIdRevoke revokes a delegation
// (EA/AMS:679-717), in one transaction holding its row: the bare 404 for
// an unknown delegation, 409 delegation_conflict unless the stamp is its
// version, then revoked_at is now, even for one revoked before, as .NET
// set it, with a new version, and delegation.revoked is audited. The
// grantee's security stamp rotates, as the spec's revocation list has it
// (the grantee's sessions end and their reset links are spent), although
// .NET rotated only the delegation's own stamp there. The caller's session
// is kept only in the one case where the caller is the grantee: a delegate
// since made an Owner revoking their own delegation. 200 with its id, new
// version and revokedAt.
func (s *server) PostIdentityAccessDelegationsByIdRevoke(ctx context.Context, req gen.PostIdentityAccessDelegationsByIdRevokeRequestObject) (gen.PostIdentityAccessDelegationsByIdRevokeResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	version := uuid.New()
	var revokedAt time.Time
	err = s.accessTx(ctx, delegationChanged, func(q *store.Queries) error {
		current, err := lockDelegation(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if req.Body == nil || req.Body.ConcurrencyStamp == nil || *req.Body.ConcurrencyStamp != current.Version.String() {
			return delegationChanged
		}
		before, err := captureDelegation(ctx, q, current)
		if err != nil {
			return err
		}
		// At timestamptz's precision, so the answer and the audit row agree
		// with what is read back, as expiresAt is stored.
		revokedAt = s.deps.Clock().Truncate(time.Microsecond)
		changed, err := q.RevokeDelegation(ctx, store.RevokeDelegationParams{Now: revokedAt, Version: version, ID: req.Id, ExpectedVersion: current.Version})
		if err != nil {
			return err
		}
		if changed == 0 {
			return errConcurrentChange
		}
		keep := uuid.Nil
		if current.GranteeUserID == p.UserID {
			keep = p.SessionID
		}
		if err := s.access.rotateSecurityStamp(ctx, q, current.GranteeUserID, keep); err != nil {
			return err
		}
		after := before
		after.RevokedAt = utc(&revokedAt)
		return writeAudit(ctx, q, r, revokedAt, auditEvent{
			actor:      &p.UserID,
			targetUser: &current.GranteeUserID,
			action:     "delegation.revoked",
			before:     before,
			after:      after,
			mfa:        p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityAccessDelegationsByIdRevokeResponseObject](err)
	}
	return gen.PostIdentityAccessDelegationsByIdRevoke200JSONResponse{Id: req.Id, Version: version.String(), RevokedAt: utc(&revokedAt)}, nil
}

// delegationCreated is the delegation creation's 201: its Location, then
// the generated body.
type delegationCreated struct {
	location string
	body     gen.PostIdentityAccessDelegations201JSONResponse
}

func (d delegationCreated) VisitPostIdentityAccessDelegationsResponse(w http.ResponseWriter) error {
	w.Header().Set("Location", d.location)
	return d.body.VisitPostIdentityAccessDelegationsResponse(w)
}

// The refusals the delegation operations answer with.

func (r refusal) VisitPostIdentityAccessDelegationsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityAccessDelegationsByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccessDelegationsByIdRevokeResponse(w http.ResponseWriter) error {
	return r.write(w)
}
