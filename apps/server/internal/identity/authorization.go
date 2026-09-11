package identity

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// accessRolesPath is the custom role collection: the Location of a created
// role.
const accessRolesPath = "/api/v1/identity/access/roles"

// The roles table's length CHECKs a role's trimmed name and description
// are held to (the display name shares maxDisplayNameLength).
const (
	maxRoleNameLength        = 256
	maxRoleDescriptionLength = 2000
)

// The /access/* refusals, each the flat CodeMessageError .NET's handlers
// answered with (EA/AuthorizationManagementEndpoints.cs, "EA/AMS" below;
// AZ/AuthorizationMutationService.cs, "AZ/AMS").
var (
	invalidRole            = refuseFlat(http.StatusBadRequest, "invalid_role", "Role metadata and registered permission keys are required.")    // EA/AMS:750-757
	invalidRoles           = refuseFlat(http.StatusBadRequest, "invalid_roles", "Only custom application roles may be assigned by this route.") // EA/AMS:378-379
	selfRoleChange         = refuseFlat(http.StatusBadRequest, "self_change", "A user cannot change their own roles.")                          // EA/AMS:359
	systemRoleChanged      = refuseFlat(http.StatusConflict, "system_role", "Protected system and built-in roles cannot be changed.")           // EA/AMS:218-219
	systemRoleDeleted      = refuseFlat(http.StatusConflict, "system_role", "Protected system and built-in roles cannot be deleted.")           // EA/AMS:301-302
	staleRole              = refuseFlat(http.StatusConflict, "role_conflict", "The role changed concurrently; refresh its version.")            // EA/AMS:220-222, :303-305
	staleUser              = refuseFlat(http.StatusConflict, "user_conflict", "The user changed concurrently; refresh its version.")            // EA/AMS:362-364
	roleExists             = refuseFlat(http.StatusConflict, "role_exists", "The normalized role name is already in use.")                      // EA/AMS:132-133, :243-244
	roleAssigned           = refuseFlat(http.StatusConflict, "role_assigned", "Assigned roles cannot be deleted.")                              // EA/AMS:317-318
	roleMapped             = refuseFlat(http.StatusForbidden, "role_mapped", "Roles mapped to access groups cannot be deleted; remove every mapping first.")
	roleGroupProtected     = refuseFlat(http.StatusForbidden, "role_group_protected_permission", "A role mapped to an access group cannot receive Owner or protected authorization-management permissions.")
	delegationNotAllowed   = refuseFlat(http.StatusForbidden, "delegation_not_allowed", "Owners cannot select a delegation.")                                                        // AZ/AMS:40-42, :117-118
	delegationRequired     = refuseFlat(http.StatusForbidden, "delegation_required", "A delegation must be selected.")                                                               // AZ/AMS:44-45, :125-126
	roleEditDenied         = refuseFlat(http.StatusForbidden, "role_edit_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be changed.")   // AZ/AMS:79-81
	roleDeleteDenied       = refuseFlat(http.StatusForbidden, "role_delete_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be deleted.") // AZ/AMS:99-102
	roleCreateDenied       = refuseFlat(http.StatusForbidden, "role_create_denied", "The selected delegation cannot create this role.")                                              // AZ/AMS:49-50
	roleBoundaryExceeded   = refuseFlat(http.StatusForbidden, "role_boundary_exceeded", "The role permissions exceed the selected delegation boundary.")                             // AZ/AMS:51-53
	assignmentTargetDenied = refuseFlat(http.StatusForbidden, "assignment_target_denied", "Delegates may assign only ordinary users.")                                               // AZ/AMS:121-123
	delegationInvalid      = refuseFlat(http.StatusForbidden, "delegation_invalid", "The selected delegation is not active and valid.")                                              // AZ/AMS:127-129
	assignmentDenied       = refuseFlat(http.StatusForbidden, "assignment_denied", "The requested role is outside the selected delegation scope.")                                   // AZ/AMS:138-139

	// Each handler's own catch of a lost race, AuthorizationConflict.IsExpected
	// (EA/AMS:192-195, :274-277, :324-327, :413-416), which answers before the
	// route group's filter can.
	roleCreateConflict = refuseFlat(http.StatusConflict, "role_conflict", "The role conflicts with a concurrent authorization change.")
	roleChangeConflict = refuseFlat(http.StatusConflict, "role_conflict", "The role changed concurrently.")
	userRolesConflict  = refuseFlat(http.StatusConflict, "user_conflict", "The user roles changed concurrently.")

	// authorizationConflict is the /access route group's filter answer
	// (EA/AMS:20-34), for a lost race nothing nearer handled.
	authorizationConflict = refuseFlat(http.StatusConflict, "authorization_conflict", "The authorization state changed concurrently.")
)

// errConcurrentChange is a conditional write that found its row's version
// changed since the transaction read it: .NET's DbUpdateConcurrencyException.
var errConcurrentChange = errors.New("identity: concurrent authorization change")

// isAuthorizationConflict is .NET's AuthorizationConflict.IsExpected
// (AZ/AuthorizationConflict.cs:9-22): a concurrent version change, a unique
// violation, a serialization failure or a deadlock.
func isAuthorizationConflict(err error) bool {
	return errors.Is(err, errConcurrentChange) || db.IsUniqueViolation(err, "") || db.IsSerializationConflict(err)
}

// accessTx runs fn in one READ COMMITTED transaction, retried while it is
// chosen as a deadlock victim. A deadlock on the last attempt, a unique
// violation, or a concurrent version change is answered with conflict, the
// operation's own catch; a refusal fn returns rolls the transaction back
// and comes back as it is.
//
// .NET ran the role mutations SERIALIZABLE. Here they run READ COMMITTED
// under the role's lock and row locks instead: a serializable transaction
// takes its snapshot at its first statement, the lock call, before it
// waits, so every check after the lock would read what was true before the
// lock's holder committed. Under READ COMMITTED each check after the lock
// sees what the holder did, whatever the holder's isolation.
func (s *server) accessTx(ctx context.Context, conflict refusal, fn func(q *store.Queries) error) error {
	err := db.RetrySerializable(ctx, serializableAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			return fn(store.New(tx))
		})
	})
	if isAuthorizationConflict(err) {
		return conflict
	}
	return err
}

// accessConflictFilter is the endpoint filter of .NET's two /access route
// groups: a lost race that escapes an operation's own handling (a
// concurrent version change, a unique violation, a serialization failure or
// a deadlock) is answered 409 with the flat body, not a 500. Roles, users
// and delegations answer authorization_conflict (EA/AMS:20-34); access
// groups answer concurrency_conflict (EA/IdentityControlPlaneEndpoints.cs:17-22),
// whose filter caught the same errors. It is a strict middleware, so it
// sees exactly what the operation returned. /access/me was mapped outside
// both groups (EA/AMS:55-56).
func accessConflictFilter(f gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
	if operationID == "GetIdentityAccessMe" || !strings.Contains(operationID, "IdentityAccess") {
		return f
	}
	answer := authorizationConflict
	if strings.Contains(operationID, "IdentityAccessGroups") {
		answer = groupConcurrencyConflict
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		response, err := f(ctx, w, r, request)
		if err != nil && isAuthorizationConflict(err) {
			return nil, answer.write(w) // written here: no response object, so the generated handler writes nothing more
		}
		return response, err
	}
}

// roleMutationLockKey is .NET's RoleMutationLockKey
// (AZ/AMS:27-31): the first eight bytes of SHA-256 over the role id as
// Guid.ToByteArray() lays it out, read big-endian
// (BinaryPrimitives.ReadInt64BigEndian). ToByteArray writes the first three
// fields little-endian, so they are reversed here from the RFC 4122 order a
// uuid.UUID holds, and the key is the one .NET took for the same role.
func roleMutationLockKey(id uuid.UUID) int64 {
	b := id
	slices.Reverse(b[0:4])
	slices.Reverse(b[4:6])
	slices.Reverse(b[6:8])
	sum := sha256.Sum256(b[:])
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// lockRole takes roleID's transaction advisory lock on q
// (AZ/AMS:20-25). Editing a role, deleting it, and mapping it to an access
// group or unmapping it (roleMappingMutation) take it, so a role's
// protected-permission check and a group mapping never interleave.
//
// Lock order, for a transaction that takes more than one of these: the
// owner lock (ownerMutationLock) first; then delegation rows, by id (the
// caller's FOR SHARE, heldDelegations; the one an Owner updates or revokes
// FOR UPDATE); then role locks in ascending key order; then access_groups
// rows; then users rows; then roles rows. The one exception is a user's
// deletion, whose cascade takes the user's delegation rows after their
// users row; PostgreSQL breaks the deadlock that can meet with that user's
// own delegated mutation, and the loser is retried.
func lockRole(ctx context.Context, q *store.Queries, roleID uuid.UUID) error {
	return q.AcquireRoleMutationLock(ctx, roleMutationLockKey(roleID))
}

// isOwner reports whether the caller holds Owner, as read with this
// request's session (.NET's IsOwnerAsync read it from the database,
// AZ/AMS:150-152).
func isOwner(p contracts.Principal) bool {
	return slices.Contains(p.Roles, RoleOwner)
}

// The guards below are AuthorizationMutationService's (AZ/AMS:33-148,
// :249-260). An Owner acts directly and may not name a delegation. Anyone
// else passed AuthorizationManagement on an active delegation and acts
// within one of their scopes at a time (scopesOf), never within the union
// of several, so disjoint scopes never combine. Each guard runs in the
// mutation's transaction, after heldDelegations, at the point .NET ran it.

// authorizeRoleCreate is AuthorizeRoleCreateAsync (AZ/AMS:33-54): an Owner
// creates any role and may not name a delegation (403
// delegation_not_allowed). Anyone else must name a delegation (403
// delegation_required) that is one of their scopes and may create roles
// (403 role_create_denied), and every key must lie within it (403
// role_boundary_exceeded). It returns the scope the role is created under,
// nil for an Owner.
func (s *server) authorizeRoleCreate(ctx context.Context, q *store.Queries, p contracts.Principal, held []uuid.UUID, delegationID *uuid.UUID, keys []string, now time.Time) (*scope, error) {
	if isOwner(p) {
		if delegationID != nil {
			return nil, delegationNotAllowed
		}
		return nil, nil
	}
	if delegationID == nil {
		return nil, delegationRequired
	}
	scopes, err := s.scopesOf(ctx, q, held, now)
	if err != nil {
		return nil, err
	}
	sc := findScope(scopes, *delegationID)
	if sc == nil || !sc.canCreateRoles {
		return nil, roleCreateDenied
	}
	if !sc.within(s.deps.Catalog, keys) {
		return nil, roleBoundaryExceeded
	}
	return sc, nil
}

// authorizeRoleEdit is AuthorizeRoleEditAsync's actor check (AZ/AMS:76-82):
// an Owner edits any custom role. Anyone else edits only a role one scope
// of theirs stewards and within which every key lies, among the keys the
// role has and those it is to get, so an edit neither grants nor strips a
// key outside that one scope. 403 role_edit_denied. .NET's edit named no
// delegation, and neither does this one.
func (s *server) authorizeRoleEdit(ctx context.Context, q *store.Queries, p contracts.Principal, held []uuid.UUID, roleID uuid.UUID, keys []string, now time.Time) error {
	if isOwner(p) {
		return nil
	}
	ok, err := s.stewardedWithin(ctx, q, held, roleID, keys, now)
	if err != nil || !ok {
		return cmpOr(err, error(roleEditDenied))
	}
	return nil
}

// authorizeRoleDelete is AuthorizeRoleDeleteAsync's actor check
// (AZ/AMS:94-102): as authorizeRoleEdit, over the keys the role has. 403
// role_delete_denied.
func (s *server) authorizeRoleDelete(ctx context.Context, q *store.Queries, p contracts.Principal, held []uuid.UUID, roleID uuid.UUID, now time.Time) error {
	if isOwner(p) {
		return nil
	}
	keys, err := q.GetRolePermissionKeys(ctx, roleID)
	if err != nil {
		return err
	}
	ok, err := s.stewardedWithin(ctx, q, held, roleID, keys, now)
	if err != nil || !ok {
		return cmpOr(err, error(roleDeleteDenied))
	}
	return nil
}

// stewardedWithin reports whether one of the scopes held stewards roleID
// and has every key within it.
func (s *server) stewardedWithin(ctx context.Context, q *store.Queries, held []uuid.UUID, roleID uuid.UUID, keys []string, now time.Time) (bool, error) {
	scopes, err := s.scopesOf(ctx, q, held, now)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(scopes, func(sc scope) bool { return sc.stewards(roleID) && sc.within(s.deps.Catalog, keys) }), nil
}

// authorizeAssignment is AuthorizeAssignmentAsync (AZ/AMS:105-148), in its
// order. It returns the custom roles the caller may take away from the
// user. An Owner may not name a delegation (403 delegation_not_allowed),
// and takes away every custom role the request leaves out. Anyone else may
// not target an Owner or a delegate (403 assignment_target_denied), must
// name a delegation (403 delegation_required) that is one of their scopes
// (403 delegation_invalid), and may add only roles that scope can assign
// (403 assignment_denied); of the roles the request leaves out, only those
// that scope can assign are taken away, and the rest stay. The caller is
// never the user (self_change answers first, EA/AMS:359, which leaves
// AZ/AMS:113-114 unreachable), and every requested role is already known to
// be custom (invalid_roles, EA/AMS:378-379, before AZ/AMS:131-134's
// system_role_denied could).
func (s *server) authorizeAssignment(ctx context.Context, q *store.Queries, p contracts.Principal, held []uuid.UUID, delegationID *uuid.UUID,
	target uuid.UUID, targetOwner bool, existingCustom, requested []uuid.UUID, now time.Time,
) ([]uuid.UUID, error) {
	leftOut := slices.DeleteFunc(slices.Clone(existingCustom), func(id uuid.UUID) bool { return slices.Contains(requested, id) })
	if isOwner(p) {
		if delegationID != nil {
			return nil, delegationNotAllowed
		}
		return leftOut, nil
	}
	if targetOwner {
		return nil, assignmentTargetDenied
	}
	targetScopes, err := activeScopes(ctx, q, s.deps.Catalog, target, now)
	if err != nil {
		return nil, err
	}
	if len(targetScopes) > 0 {
		return nil, assignmentTargetDenied
	}
	if delegationID == nil {
		return nil, delegationRequired
	}
	scopes, err := s.scopesOf(ctx, q, held, now)
	if err != nil {
		return nil, err
	}
	sc := findScope(scopes, *delegationID)
	if sc == nil {
		return nil, delegationInvalid
	}
	if slices.ContainsFunc(requested, func(id uuid.UUID) bool { return !slices.Contains(existingCustom, id) && !sc.assigns(id) }) {
		return nil, assignmentDenied
	}
	return slices.DeleteFunc(leftOut, func(id uuid.UUID) bool { return !sc.assigns(id) }), nil
}

// canInspectTarget is CanInspectTargetAsync (AZ/AMS:249-260): nobody
// inspects their own access through the directory, and an Owner inspects
// anyone else. Anyone else inspects no Owner and no delegate, and
// otherwise a user whose every custom role one scope of theirs can assign
// (a user without custom roles, when they hold any scope).
func (s *server) canInspectTarget(ctx context.Context, p contracts.Principal, target uuid.UUID) (bool, error) {
	if p.UserID == target {
		return false, nil
	}
	if isOwner(p) {
		return true, nil
	}
	now := s.deps.Clock()
	owner, err := s.q.IsOwner(ctx, target)
	if err != nil || owner {
		return false, err
	}
	targetScopes, err := activeScopes(ctx, s.q, s.deps.Catalog, target, now)
	if err != nil || len(targetScopes) > 0 {
		return false, err
	}
	scopes, err := activeScopes(ctx, s.q, s.deps.Catalog, p.UserID, now)
	if err != nil {
		return false, err
	}
	roles, err := s.q.ListUserRoleAssignments(ctx, target)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(scopes, func(sc scope) bool {
		return !slices.ContainsFunc(roles, func(r store.ListUserRoleAssignmentsRow) bool {
			return !r.IsSystem && !r.IsBuiltIn && !sc.assigns(r.ID)
		})
	}), nil
}

// managementScope is /access/me's canManageAuthorization and
// administrationScope for a user (EA/AMS:433-458, AZ/AMS:157-158): an Owner
// manages authorization, with no delegation scopes. Anyone else manages it
// while they hold a scope, and their administration scope lists each, with
// its keys and roles as loadScopes reads them (ordinal, and by id); without
// one they do not, and have no administration scope.
func managementScope(owner bool, scopes []scope) (bool, *gen.AdministrationScopeResponse) {
	if owner {
		return true, &gen.AdministrationScopeResponse{IsOwner: true, DelegationScopes: []gen.DelegationScopeResponse{}}
	}
	if len(scopes) == 0 {
		return false, nil
	}
	out := make([]gen.DelegationScopeResponse, len(scopes))
	for i, sc := range scopes {
		out[i] = gen.DelegationScopeResponse{
			Id:                      sc.id,
			CanCreateRoles:          sc.canCreateRoles,
			GrantablePermissionKeys: sc.keys,
			StewardedRoleIds:        sc.stewarded,
			AssignableRoleIds:       sc.assignable,
		}
	}
	return true, &gen.AdministrationScopeResponse{IsOwner: false, DelegationScopes: out}
}

// protectedPermission reports whether key is in the catalog and may not be
// delegated (AZ/AMS:269-270).
func (s *server) protectedPermission(key string) bool {
	p, ok := s.deps.Catalog[key]
	return ok && !p.Delegable
}

// guardGroupMappedRole is .NET's protection of a role an access group maps
// (AZ/AMS:63-74, rechecked at EA/AMS:233-240): such a role may not be named
// Owner nor carry a non-delegable key, among the keys it has and the keys
// it is to get, so no group hands out Owner-level or authorization
// management power. 403 role_group_protected_permission.
func (s *server) guardGroupMappedRole(ctx context.Context, q *store.Queries, roleID uuid.UUID, name string, keys []string) error {
	mapped, err := q.RoleIsMapped(ctx, roleID)
	if err != nil || !mapped {
		return err
	}
	if name == RoleOwner || slices.ContainsFunc(keys, s.protectedPermission) {
		return roleGroupProtected
	}
	return nil
}

// validRole is .NET's ValidateRoleRequest (EA/AMS:750-757): a name, display
// name and description that are not blank, and a permission-key list whose
// every key is in the catalog. The trimmed values are also held to their
// columns' lengths, where .NET failed at the database.
func (s *server) validRole(name, displayName, description *string, keys *[]string) bool {
	if blank(name) || blank(displayName) || blank(description) || keys == nil {
		return false
	}
	if utf16Length(strings.TrimSpace(*name)) > maxRoleNameLength ||
		utf16Length(strings.TrimSpace(*displayName)) > maxDisplayNameLength ||
		utf16Length(strings.TrimSpace(*description)) > maxRoleDescriptionLength {
		return false
	}
	for _, key := range *keys {
		if _, ok := s.deps.Catalog[key]; !ok {
			return false
		}
	}
	return true
}

// normalizeRoleName is a role name as its uniqueness is decided: the
// lookup normalizer's upper case (EA/AMS:131, :242).
func normalizeRoleName(name string) string {
	return strings.ToUpper(name)
}

// roleAudit is a role's state in the role audit events, with the property
// names .NET's serializer wrote (EA/AMS:174-180, :241, :255-261).
type roleAudit struct {
	Name        string   `json:"Name"`
	DisplayName string   `json:"DisplayName"`
	Description string   `json:"Description"`
	Permissions []string `json:"Permissions"`
}

// roleCreatedBefore is role.created's before (EA/AMS:164-173): the new
// role's id, and nothing else yet.
type roleCreatedBefore struct {
	RoleID        uuid.UUID  `json:"RoleId"`
	Name          *string    `json:"Name"`
	DisplayName   *string    `json:"DisplayName"`
	Description   *string    `json:"Description"`
	IsSystem      bool       `json:"IsSystem"`
	IsBuiltIn     bool       `json:"IsBuiltIn"`
	StewardUserID *uuid.UUID `json:"StewardUserId"`
	Permissions   []string   `json:"Permissions"`
}

// roleDeletedBefore is role.deleted's before (EA/AMS:320-321).
type roleDeletedBefore struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
}

// roleDeletedAfter is role.deleted's after: .NET handed its serializer the
// string "{}", so the column holds that JSON string, quotes included
// (EA/AMS:321).
const roleDeletedAfter = "{}"

// GetIdentityAccessCatalog lists the composed permission catalog in key
// order (ordinal), as .NET's PermissionCatalog kept it
// (packages/contracts/Vantigo.Contracts/Authorization/PermissionCatalog.cs:95-97;
// EA/AMS:59). A key's module is its prefix.
func (s *server) GetIdentityAccessCatalog(context.Context, gen.GetIdentityAccessCatalogRequestObject) (gen.GetIdentityAccessCatalogResponseObject, error) {
	keys := slices.Sorted(maps.Keys(s.deps.Catalog))
	out := make(gen.GetIdentityAccessCatalog200JSONResponse, len(keys))
	for i, key := range keys {
		p := s.deps.Catalog[key]
		module, _, _ := strings.Cut(key, ":")
		out[i] = gen.PermissionDescriptor{
			Key:         key,
			DisplayName: p.Display,
			Description: p.Description,
			Module:      module,
			Category:    p.Category,
			Sensitive:   p.Sensitive,
			Delegable:   p.Delegable,
		}
	}
	return out, nil
}

// GetIdentityAccessRoles lists every role but SystemAdmin, by name, with
// its permission keys (EA/AMS:61-104).
func (s *server) GetIdentityAccessRoles(ctx context.Context, _ gen.GetIdentityAccessRolesRequestObject) (gen.GetIdentityAccessRolesResponseObject, error) {
	rows, err := s.q.ListManageableRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list roles: %w", err)
	}
	out := make(gen.GetIdentityAccessRoles200JSONResponse, len(rows))
	for i, r := range rows {
		out[i] = gen.RoleResponse{
			Id:             r.ID,
			Name:           &r.Name,
			NormalizedName: &r.NormalizedName,
			DisplayName:    r.DisplayName,
			Description:    r.Description,
			IsSystem:       r.IsSystem,
			IsBuiltIn:      r.IsBuiltIn,
			StewardUserId:  r.StewardUserID,
			Version:        r.Version.String(),
			Permissions:    nonNil(r.Permissions),
		}
	}
	return out, nil
}

// PostIdentityAccessRoles creates a custom role (EA/AMS:106-196), in .NET's
// order: 400 invalid_role; then, in one transaction, the caller's
// authority (authorizeRoleCreate), and 409 role_exists for a normalized
// name already in use. The role is created with the caller as steward and
// each key once; a delegate's joins the roles their delegation stewards
// (EA/AMS:156-162); and role.created is audited. 201 with the role, its
// version and the keys as sent, and its Location.
func (s *server) PostIdentityAccessRoles(ctx context.Context, req gen.PostIdentityAccessRolesRequestObject) (gen.PostIdentityAccessRolesResponseObject, error) {
	var body gen.RoleUpsertRequest
	if req.Body != nil {
		body = *req.Body
	}
	if !s.validRole(body.Name, body.DisplayName, body.Description, body.PermissionKeys) {
		return invalidRole, nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(*body.Name)
	displayName, description := strings.TrimSpace(*body.DisplayName), strings.TrimSpace(*body.Description)
	keys := *body.PermissionKeys
	id, version := uuid.New(), uuid.New()
	err = s.accessTx(ctx, roleCreateConflict, func(q *store.Queries) error {
		now := s.deps.Clock()
		delegations, err := s.heldDelegations(ctx, q, p, now)
		if err != nil {
			return err
		}
		sc, err := s.authorizeRoleCreate(ctx, q, p, delegations, body.DelegationId, keys, now)
		if err != nil {
			return err
		}
		taken, err := q.RoleNameTaken(ctx, store.RoleNameTakenParams{NormalizedName: normalizeRoleName(name), ID: uuid.Nil})
		if err != nil || taken {
			return cmpOr(err, error(roleExists))
		}
		if err := q.InsertRole(ctx, store.InsertRoleParams{
			ID:             id,
			Name:           name,
			NormalizedName: normalizeRoleName(name),
			DisplayName:    displayName,
			Description:    description,
			StewardUserID:  &p.UserID,
			Version:        version,
			Now:            now,
		}); err != nil {
			return err
		}
		if err := q.InsertRolePermissions(ctx, store.InsertRolePermissionsParams{RoleID: id, Keys: keys}); err != nil {
			return err
		}
		if sc != nil {
			if err := q.InsertDelegationRoles(ctx, store.InsertDelegationRolesParams{DelegationID: sc.id, RoleIds: []uuid.UUID{id}}); err != nil {
				return err
			}
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:      &p.UserID,
			targetRole: &id,
			action:     "role.created",
			before:     roleCreatedBefore{RoleID: id, Permissions: []string{}},
			after:      roleAudit{Name: name, DisplayName: displayName, Description: description, Permissions: keys},
			mfa:        p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityAccessRolesResponseObject](err)
	}
	return roleCreated{
		location: s.deps.Config.BasePath + accessRolesPath + "/" + id.String(),
		body:     gen.PostIdentityAccessRoles201JSONResponse{Id: id, Name: &name, Version: version.String(), Permissions: keys},
	}, nil
}

// PutIdentityAccessRolesById replaces a custom role's name, display name,
// description and whole permission set (updateRole).
func (s *server) PutIdentityAccessRolesById(ctx context.Context, req gen.PutIdentityAccessRolesByIdRequestObject) (gen.PutIdentityAccessRolesByIdResponseObject, error) {
	var body gen.RoleUpsertRequest
	if req.Body != nil {
		body = *req.Body
	}
	role, err := s.updateRole(ctx, req.Id, body.Name, body.DisplayName, body.Description, body.PermissionKeys, body.ConcurrencyStamp)
	if err != nil {
		return refusalOr[gen.PutIdentityAccessRolesByIdResponseObject](err)
	}
	return gen.PutIdentityAccessRolesById200JSONResponse(role), nil
}

// PutIdentityAccessRolesByIdPermissions is the same replacement, as .NET
// routed it to UpdateRole (EA/AMS:331-344).
func (s *server) PutIdentityAccessRolesByIdPermissions(ctx context.Context, req gen.PutIdentityAccessRolesByIdPermissionsRequestObject) (gen.PutIdentityAccessRolesByIdPermissionsResponseObject, error) {
	var body gen.PermissionReplacementRequest
	if req.Body != nil {
		body = *req.Body
	}
	role, err := s.updateRole(ctx, req.Id, body.Name, body.DisplayName, body.Description, body.PermissionKeys, body.ConcurrencyStamp)
	if err != nil {
		return refusalOr[gen.PutIdentityAccessRolesByIdPermissionsResponseObject](err)
	}
	return gen.PutIdentityAccessRolesByIdPermissions200JSONResponse(role), nil
}

// updateRole is .NET's UpdateRole (EA/AMS:198-279), in its order: 400
// invalid_role; then, in one transaction under the role's lock, 404 for an unknown role, 409 system_role for a built-in one, 409
// role_conflict unless the stamp is the role's version, 403
// role_group_protected_permission, the caller's authority, and 409
// role_exists for a normalized name another role has. The role then gets
// its new metadata, version and permission set, and role.updated is
// audited. Holders' sessions are untouched: .NET rotated no user's
// security stamp here, and every request reads permissions afresh.
func (s *server) updateRole(ctx context.Context, id uuid.UUID, name, displayName, description *string, keys *[]string, stamp *string) (gen.RoleUpdateResponse, error) {
	if !s.validRole(name, displayName, description, keys) {
		return gen.RoleUpdateResponse{}, invalidRole
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return gen.RoleUpdateResponse{}, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return gen.RoleUpdateResponse{}, err
	}

	newName := strings.TrimSpace(*name)
	newDisplayName, newDescription := strings.TrimSpace(*displayName), strings.TrimSpace(*description)
	requested := *keys
	version := uuid.New()
	err = s.accessTx(ctx, roleChangeConflict, func(q *store.Queries) error {
		now := s.deps.Clock()
		delegations, err := s.heldDelegations(ctx, q, p, now)
		if err != nil {
			return err
		}
		if err := lockRole(ctx, q, id); err != nil {
			return err
		}
		role, err := q.LockRoleForEdit(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return err
		}
		if role.IsSystem || role.IsBuiltIn {
			return systemRoleChanged
		}
		if blank(stamp) || *stamp != role.Version.String() {
			return staleRole
		}
		current, err := q.GetRolePermissionKeys(ctx, id)
		if err != nil {
			return err
		}
		if err := s.guardGroupMappedRole(ctx, q, id, newName, slices.Concat(current, requested)); err != nil {
			return err
		}
		if err := s.authorizeRoleEdit(ctx, q, p, delegations, id, slices.Concat(current, requested), now); err != nil {
			return err
		}
		taken, err := q.RoleNameTaken(ctx, store.RoleNameTakenParams{NormalizedName: normalizeRoleName(newName), ID: id})
		if err != nil || taken {
			return cmpOr(err, error(roleExists))
		}

		changed, err := q.UpdateRole(ctx, store.UpdateRoleParams{
			ID:              id,
			Name:            newName,
			NormalizedName:  normalizeRoleName(newName),
			DisplayName:     newDisplayName,
			Description:     newDescription,
			Version:         version,
			Now:             now,
			ExpectedVersion: role.Version,
		})
		if err != nil {
			return err
		}
		if changed == 0 {
			return errConcurrentChange
		}
		if err := q.DeleteRolePermissions(ctx, id); err != nil {
			return err
		}
		if err := q.InsertRolePermissions(ctx, store.InsertRolePermissionsParams{RoleID: id, Keys: requested}); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:      &p.UserID,
			targetRole: &id,
			action:     "role.updated",
			before:     roleAudit{Name: role.Name, DisplayName: role.DisplayName, Description: role.Description, Permissions: nonNil(current)},
			after:      roleAudit{Name: newName, DisplayName: newDisplayName, Description: newDescription, Permissions: requested},
			mfa:        p.MFAVerified,
		})
	})
	if err != nil {
		return gen.RoleUpdateResponse{}, err
	}
	return gen.RoleUpdateResponse{
		Id:          id,
		Name:        &newName,
		DisplayName: newDisplayName,
		Description: newDescription,
		Version:     version.String(),
		Permissions: requested,
	}, nil
}

// DeleteIdentityAccessRolesById deletes a custom role (EA/AMS:281-329), in
// .NET's order, in one transaction under the role's lock: 404
// for an unknown role, 409 system_role for a built-in one, 409
// role_conflict unless the stamp is the role's version, 403 role_mapped
// while an access group maps it, the caller's authority, and 409
// role_assigned while anyone holds it. .NET checked the mapping again once
// it held the lock (:308-316); here the lock is held from the start, so the
// one check is already the one after it. role.deleted is audited. 204.
func (s *server) DeleteIdentityAccessRolesById(ctx context.Context, req gen.DeleteIdentityAccessRolesByIdRequestObject) (gen.DeleteIdentityAccessRolesByIdResponseObject, error) {
	var stamp *string
	if req.Body != nil {
		stamp = req.Body.ConcurrencyStamp
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	err = s.accessTx(ctx, roleChangeConflict, func(q *store.Queries) error {
		now := s.deps.Clock()
		delegations, err := s.heldDelegations(ctx, q, p, now)
		if err != nil {
			return err
		}
		if err := lockRole(ctx, q, req.Id); err != nil {
			return err
		}
		role, err := q.LockRole(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return err
		}
		if role.IsSystem || role.IsBuiltIn {
			return systemRoleDeleted
		}
		if blank(stamp) || *stamp != role.Version.String() {
			return staleRole
		}
		mapped, err := q.RoleIsMapped(ctx, req.Id)
		if err != nil || mapped {
			return cmpOr(err, error(roleMapped))
		}
		if err := s.authorizeRoleDelete(ctx, q, p, delegations, req.Id, now); err != nil {
			return err
		}
		assigned, err := q.RoleIsAssigned(ctx, req.Id)
		if err != nil || assigned {
			return cmpOr(err, error(roleAssigned))
		}
		if err := q.DeleteRole(ctx, req.Id); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:      &p.UserID,
			targetRole: &req.Id,
			action:     "role.deleted",
			before:     roleDeletedBefore{Name: role.Name, Description: role.Description},
			after:      roleDeletedAfter,
			mfa:        p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityAccessRolesByIdResponseObject](err)
	}
	return gen.DeleteIdentityAccessRolesById204Response{}, nil
}

// PutIdentityAccessUsersByIdRoles replaces another user's custom roles
// (EA/AMS:346-420), in .NET's order: 400 self_change for the caller's own
// id; then, in one transaction holding the user's row, 404 for an unknown
// user, 409 user_conflict unless the stamp is the user's version, 400
// invalid_roles unless every id names a custom role, and the caller's
// authority (authorizeAssignment). The custom roles the request leaves out
// are taken away, unless a delegate cannot assign them under the
// delegation named, those it names are given, and the user's built-in roles
// stay. The user gets a new
// version, and, as .NET rotated the user's security stamp (:397), every
// session of the user ends and every password-reset link they hold is
// spent. user.roles-replaced is audited. 200 with the roles the user holds
// and the new version.
func (s *server) PutIdentityAccessUsersByIdRoles(ctx context.Context, req gen.PutIdentityAccessUsersByIdRolesRequestObject) (gen.PutIdentityAccessUsersByIdRolesResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserID == req.Id {
		return selfRoleChange, nil
	}
	var body gen.RoleAssignmentRequest
	if req.Body != nil {
		body = *req.Body
	}
	var requested []uuid.UUID
	if body.RoleIds != nil {
		for _, id := range *body.RoleIds {
			if !slices.Contains(requested, id) {
				requested = append(requested, id)
			}
		}
	}

	version := uuid.New()
	var held []uuid.UUID
	err = s.accessTx(ctx, userRolesConflict, func(q *store.Queries) error {
		now := s.deps.Clock()
		delegations, err := s.heldDelegations(ctx, q, p, now)
		if err != nil {
			return err
		}
		current, err := q.LockUserVersion(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return err
		}
		if blank(body.ConcurrencyStamp) || *body.ConcurrencyStamp != current.String() {
			return staleUser
		}
		existing, err := q.ListUserRoleAssignments(ctx, req.Id)
		if err != nil {
			return err
		}
		roles, err := q.AssignUserRoleLocks(ctx, requested)
		if err != nil {
			return err
		}
		if len(roles) != len(requested) || slices.ContainsFunc(roles, func(r store.AssignUserRoleLocksRow) bool { return r.IsSystem || r.IsBuiltIn }) {
			return invalidRoles
		}
		var existingCustom []uuid.UUID
		targetOwner := false
		for _, e := range existing {
			targetOwner = targetOwner || e.Name == RoleOwner
			if !e.IsSystem && !e.IsBuiltIn {
				existingCustom = append(existingCustom, e.ID)
			}
		}
		removable, err := s.authorizeAssignment(ctx, q, p, delegations, body.DelegationId, req.Id, targetOwner, existingCustom, requested, now)
		if err != nil {
			return err
		}

		before, err := captureUser(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if len(removable) > 0 {
			if err := q.RemoveUserRoles(ctx, store.RemoveUserRolesParams{UserID: req.Id, RoleIds: removable}); err != nil {
				return err
			}
		}
		for _, roleID := range requested {
			if _, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: req.Id, RoleID: roleID}); err != nil {
				return err
			}
		}
		if err := q.RotateUserVersion(ctx, store.RotateUserVersionParams{ID: req.Id, Version: version, Now: now}); err != nil {
			return err
		}
		if err := s.access.rotateSecurityStamp(ctx, q, req.Id, uuid.Nil); err != nil {
			return err
		}
		after, err := captureUser(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor:      &p.UserID,
			targetUser: &req.Id,
			action:     "user.roles-replaced",
			before:     before,
			after:      after,
			mfa:        p.MFAVerified,
		}); err != nil {
			return err
		}
		assignments, err := q.ListUserRoleAssignments(ctx, req.Id)
		if err != nil {
			return err
		}
		held = assignmentIDs(orderAssignments(assignments))
		return nil
	})
	if err != nil {
		return refusalOr[gen.PutIdentityAccessUsersByIdRolesResponseObject](err)
	}
	v := version.String()
	return gen.PutIdentityAccessUsersByIdRoles200JSONResponse{UserId: req.Id, RoleIds: held, Version: &v}, nil
}

// orderAssignments puts a user's roles, which ListUserRoleAssignments reads
// by name, in the order every response lists roles (orderRoles):
// SystemAdmin, Owner, User, then the rest by name.
func orderAssignments(rows []store.ListUserRoleAssignmentsRow) []store.ListUserRoleAssignmentsRow {
	slices.SortStableFunc(rows, func(a, b store.ListUserRoleAssignmentsRow) int {
		return cmp.Compare(roleRank(a.Name), roleRank(b.Name))
	})
	return rows
}

func assignmentIDs(rows []store.ListUserRoleAssignmentsRow) []uuid.UUID {
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

// accessProjection is .NET's BuildEffectiveAccess (EA/AMS:529-543): a
// user's direct roles and their ids in the same order, the distinct keys
// those roles carry ("*" for an Owner), and the user's version. The roles
// access groups grant are left out, as .NET left them out, although the
// live permission check counts them (EffectivePermissionForUser).
type accessProjection struct {
	roles       []*string
	roleIDs     []uuid.UUID
	permissions []string
	version     string
	owner       bool
}

// projectAccess reads userID's accessProjection on q: the bare 404 when
// there is no such user.
func projectAccess(ctx context.Context, q *store.Queries, userID uuid.UUID) (accessProjection, error) {
	u, err := q.GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return accessProjection{}, notFound
	}
	if err != nil {
		return accessProjection{}, fmt.Errorf("identity: effective access: %w", err)
	}
	rows, err := q.ListUserRoleAssignments(ctx, userID)
	if err != nil {
		return accessProjection{}, fmt.Errorf("identity: effective access: %w", err)
	}
	a := accessProjection{roles: make([]*string, 0, len(rows)), roleIDs: make([]uuid.UUID, 0, len(rows)), version: u.Version.String()}
	for _, row := range orderAssignments(rows) {
		a.roles = append(a.roles, &row.Name)
		a.roleIDs = append(a.roleIDs, row.ID)
		a.owner = a.owner || row.Name == RoleOwner
	}
	if a.owner {
		a.permissions = []string{"*"}
		return a, nil
	}
	keys, err := q.ListUserDirectPermissionKeys(ctx, userID)
	if err != nil {
		return accessProjection{}, fmt.Errorf("identity: effective access: %w", err)
	}
	a.permissions = nonNil(keys)
	return a, nil
}

// GetIdentityAccessMe is the caller's own effective access
// (EA/AMS:422-459): their direct roles and permissions (accessProjection),
// their version, whether they manage authorization, and their
// administration scope (managementScope).
func (s *server) GetIdentityAccessMe(ctx context.Context, _ gen.GetIdentityAccessMeRequestObject) (gen.GetIdentityAccessMeResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	a, err := projectAccess(ctx, s.q, p.UserID)
	if err != nil {
		return refusalOr[gen.GetIdentityAccessMeResponseObject](err)
	}
	var scopes []scope
	if !a.owner {
		if scopes, err = activeScopes(ctx, s.q, s.deps.Catalog, p.UserID, s.deps.Clock()); err != nil {
			return nil, err
		}
	}
	canManage, administration := managementScope(a.owner, scopes)
	return gen.GetIdentityAccessMe200JSONResponse{
		UserId:                 p.UserID,
		Roles:                  a.roles,
		RoleIds:                a.roleIDs,
		Permissions:            a.permissions,
		Version:                &a.version,
		CanManageAuthorization: canManage,
		AdministrationScope:    administration,
	}, nil
}

// GetIdentityAccessUsersById is another user's effective access
// (EA/AMS:521-527): the bare 404 for the caller's own id, for a user the
// caller may not inspect (canInspectTarget), and for an unknown user.
func (s *server) GetIdentityAccessUsersById(ctx context.Context, req gen.GetIdentityAccessUsersByIdRequestObject) (gen.GetIdentityAccessUsersByIdResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	visible, err := s.canInspectTarget(ctx, p, req.Id)
	if err != nil {
		return nil, fmt.Errorf("identity: inspect access: %w", err)
	}
	if !visible {
		return gen.GetIdentityAccessUsersById404Response{}, nil
	}
	a, err := projectAccess(ctx, s.q, req.Id)
	if err != nil {
		return refusalOr[gen.GetIdentityAccessUsersByIdResponseObject](err)
	}
	return gen.GetIdentityAccessUsersById200JSONResponse{
		Id:          req.Id,
		Roles:       a.roles,
		RoleIds:     a.roleIDs,
		Permissions: a.permissions,
		Version:     &a.version,
	}, nil
}

// GetIdentityAccessUsers is the directory role assignment works from
// (EA/AMS:461-519): every user by display name, then id, with their state
// at now, their version and their direct roles by name. An Owner sees every
// user. A delegate's directory leaves out every Owner and everyone holding
// an active delegation, valid or not, themselves included (:494-503).
func (s *server) GetIdentityAccessUsers(ctx context.Context, _ gen.GetIdentityAccessUsersRequestObject) (gen.GetIdentityAccessUsersResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.q.ListAccessUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list access users: %w", err)
	}
	assignments, err := s.q.ListAllUserRoleAssignments(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list access users: %w", err)
	}
	now := s.deps.Clock()
	hidden := map[uuid.UUID]bool{}
	roles := map[uuid.UUID][]gen.AccessRoleSummary{}
	for _, a := range assignments {
		roles[a.UserID] = append(roles[a.UserID], gen.AccessRoleSummary{Id: a.RoleID, Name: &a.Name})
		hidden[a.UserID] = hidden[a.UserID] || (!isOwner(p) && a.Name == RoleOwner)
	}
	if !isOwner(p) {
		grantees, err := s.q.ActiveDelegationGrantees(ctx, now)
		if err != nil {
			return nil, fmt.Errorf("identity: list access users: %w", err)
		}
		for _, g := range grantees {
			hidden[g] = true
		}
	}
	out := make(gen.GetIdentityAccessUsers200JSONResponse, 0, len(users))
	for _, u := range users {
		if hidden[u.ID] {
			continue
		}
		version := u.Version.String()
		held := roles[u.ID]
		if held == nil {
			held = []gen.AccessRoleSummary{}
		}
		out = append(out, gen.AccessUserResponse{
			Id:          u.ID,
			DisplayName: u.DisplayName,
			Email:       &u.Email,
			IsDisabled:  u.IsDisabled,
			Active:      isActive(u.IsDisabled, u.LockoutEnd, now),
			Version:     &version,
			Roles:       held,
		})
	}
	return out, nil
}

// GetIdentityAccessAudit is the authorization audit trail: the latest 500
// events, newest first (EA/AMS:552-554).
func (s *server) GetIdentityAccessAudit(ctx context.Context, _ gen.GetIdentityAccessAuditRequestObject) (gen.GetIdentityAccessAuditResponseObject, error) {
	rows, err := s.q.ListAuditEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list audit events: %w", err)
	}
	out := make(gen.GetIdentityAccessAudit200JSONResponse, len(rows))
	for i, e := range rows {
		out[i] = gen.AuthorizationAuditEvent{
			Id:               e.ID,
			ActorUserId:      e.ActorUserID,
			TargetUserId:     e.TargetUserID,
			TargetRoleId:     e.TargetRoleID,
			Action:           e.Action,
			Details:          e.Details,
			BeforeJson:       e.BeforeJson,
			AfterJson:        e.AfterJson,
			CorrelationId:    e.CorrelationID,
			MfaAuthenticated: e.MfaAuthenticated,
			OccurredAt:       e.OccurredAt,
		}
	}
	return out, nil
}

// roleCreated is the role creation's 201: its Location, then the generated
// body.
type roleCreated struct {
	location string
	body     gen.PostIdentityAccessRoles201JSONResponse
}

func (r roleCreated) VisitPostIdentityAccessRolesResponse(w http.ResponseWriter) error {
	w.Header().Set("Location", r.location)
	return r.body.VisitPostIdentityAccessRolesResponse(w)
}

// The refusals authorization management answers with.

func (r refusal) VisitPostIdentityAccessRolesResponse(w http.ResponseWriter) error { return r.write(w) }

func (r refusal) VisitPutIdentityAccessRolesByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityAccessRolesByIdPermissionsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityAccessRolesByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityAccessUsersByIdRolesResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitGetIdentityAccessMeResponse(w http.ResponseWriter) error { return r.write(w) }

func (r refusal) VisitGetIdentityAccessUsersByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
