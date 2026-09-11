package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// accessGroupsPath is the access group collection: the Location of a
// created group.
const accessGroupsPath = "/api/v1/identity/access/groups"

// A group's source as identity.access_groups stores it.
const (
	groupSourceLocal = "local"
	groupSourceSCIM  = "scim"
)

// scimRejectionReason is why a SCIM group's content is refused, as .NET
// audited it (SV/AccessGroupManagementService.cs:333).
const scimRejectionReason = "scim_group_content_is_protocol_owned"

// The access group refusals, each the flat CodeMessageError .NET answered
// with (EA/IdentityControlPlaneEndpoints.cs, "EA/ICP" below;
// SV/AccessGroupManagementService.cs, "SV/AGM"). SV/AGM's validation
// exception answered 400 unless it named another status (:370-373).
var (
	groupInvalid            = refuseFlat(http.StatusBadRequest, "invalid_group", "A safe display name is required.")                                                                         // EA/ICP:58, :73
	groupExists             = refuseFlat(http.StatusConflict, "group_exists", "An access group with this display name already exists.")                                                      // EA/ICP:60, :78
	groupChanged            = refuseFlat(http.StatusConflict, "group_conflict", "The access group changed concurrently.")                                                                    // EA/ICP:77
	groupStampRequired      = refuseFlat(http.StatusBadRequest, "concurrency_required", "A concurrency stamp is required.")                                                                  // EA/ICP:76, :91, :101, :115
	groupUserNotFound       = refuseFlat(http.StatusBadRequest, "user_not_found", "The selected user does not exist.")                                                                       // SV/AGM:107-108
	groupRoleNotFound       = refuseFlat(http.StatusBadRequest, "role_not_found", "The selected role does not exist.")                                                                       // SV/AGM:175-176
	groupScimScopeConflict  = refuseFlat(http.StatusBadRequest, "scim_scope_conflict", "The SCIM group belongs to a different SCIM connection.")                                             // SV/AGM:311-312
	groupLocalScopeConflict = refuseFlat(http.StatusBadRequest, "scim_scope_conflict", "A local group cannot be scoped to a SCIM connection.")                                               // SV/AGM:314-316
	groupProtectedRole      = refuseFlat(http.StatusBadRequest, "protected_role", "Owner, system, and built-in roles cannot be mapped to access groups.")                                    // SV/AGM:180-186
	groupProtectedRoleKeys  = refuseFlat(http.StatusBadRequest, "protected_role", "Roles carrying authorization-management permissions cannot be mapped to access groups.")                  // SV/AGM:192-197
	groupScimManaged        = refuseFlat(http.StatusConflict, "scim_group_managed", "SCIM group content is managed by the SCIM protocol and cannot be changed through the Owner group API.") // SV/AGM:336-339

	// groupCreateConflict is the create handler's own catch of a lost race
	// (EA/ICP:66), which answers before the route group's filter can.
	groupCreateConflict = refuseFlat(http.StatusConflict, "group_conflict", "The access group conflicts with another change.")

	// groupConcurrencyConflict is the access group route group's filter
	// answer (EA/ICP:17-22): among others, to the stale stamp of a delete, a
	// member change or a role mapping, which .NET's service threw as a
	// DbUpdateConcurrencyException (SV/AGM:84-85, :103-104, :141-142,
	// :168-169, :231-232).
	groupConcurrencyConflict = refuseFlat(http.StatusConflict, "concurrency_conflict", "The resource changed concurrently.")
)

// scimRejection is a change to a SCIM group's content, which the Owner
// group API refuses (SV/AGM:61-62, :82-83, :100-101, :138-139): groupTx
// audits it and answers 409 scim_group_managed. userID is the member a
// member change named.
type scimRejection struct {
	action string
	group  store.IdentityAccessGroup
	userID *uuid.UUID
}

func (r scimRejection) Error() string { return "identity: " + r.action }

// groupSnapshot is .NET's Snapshot of a group (SV/AGM:352-362): the before
// of every group event. No group is bound to a SCIM connection id here
// (roleMappingMutation), so ScimConnectionId is always null.
type groupSnapshot struct {
	ID               uuid.UUID  `json:"Id"`
	DisplayName      string     `json:"DisplayName"`
	Source           string     `json:"Source"`
	IsActive         bool       `json:"IsActive"`
	ScimConnectionID *uuid.UUID `json:"ScimConnectionId"`
	CreatedAt        time.Time  `json:"CreatedAt"`
	UpdatedAt        time.Time  `json:"UpdatedAt"`
	ConcurrencyStamp string     `json:"ConcurrencyStamp"`
}

// groupEntity is the AccessGroup entity as .NET's serializer wrote it:
// access_group.created's and access_group.updated's after (SV/AGM:47, :73).
// Its properties are in their declaration order
// (Database/Accounts/AccessGroup.cs), and Source is the enum's number, 0
// for Local and 1 for Scim, as JsonSerializer's defaults write an enum.
type groupEntity struct {
	ID               uuid.UUID  `json:"Id"`
	ScimConnectionID *uuid.UUID `json:"ScimConnectionId"`
	DisplayName      string     `json:"DisplayName"`
	Source           int        `json:"Source"`
	ExternalID       *string    `json:"ExternalId"`
	IsActive         bool       `json:"IsActive"`
	CreatedAt        time.Time  `json:"CreatedAt"`
	UpdatedAt        time.Time  `json:"UpdatedAt"`
	ConcurrencyStamp string     `json:"ConcurrencyStamp"`
}

// groupMemberChange is a member change's after (SV/AGM:129, :153).
type groupMemberChange struct {
	Group  groupSnapshot `json:"Group"`
	UserID uuid.UUID     `json:"UserId"`
}

// groupRoleChange is a role mapping change's after (SV/AGM:215, :244).
type groupRoleChange struct {
	Group  groupSnapshot `json:"Group"`
	RoleID uuid.UUID     `json:"RoleId"`
}

// groupRejected is a refused SCIM group change's after (SV/AGM:327-334).
type groupRejected struct {
	GroupID          uuid.UUID  `json:"GroupId"`
	ScimConnectionID *uuid.UUID `json:"ScimConnectionId"`
	UserID           *uuid.UUID `json:"UserId"`
	Rejected         bool       `json:"Rejected"`
	Reason           string     `json:"Reason"`
}

// sourceName is a group source as .NET's AccessGroupSource.ToString()
// named it, in responses and snapshots.
func sourceName(source string) string {
	if source == groupSourceSCIM {
		return "Scim"
	}
	return "Local"
}

func snapshotOf(g store.IdentityAccessGroup) groupSnapshot {
	return groupSnapshot{
		ID:               g.ID,
		DisplayName:      g.DisplayName,
		Source:           sourceName(g.Source),
		IsActive:         g.IsActive,
		CreatedAt:        g.CreatedAt.UTC(),
		UpdatedAt:        g.UpdatedAt.UTC(),
		ConcurrencyStamp: g.Version.String(),
	}
}

func entityOf(g store.IdentityAccessGroup) groupEntity {
	source := 0
	if g.Source == groupSourceSCIM {
		source = 1
	}
	return groupEntity{
		ID:               g.ID,
		DisplayName:      g.DisplayName,
		Source:           source,
		ExternalID:       g.ExternalID,
		IsActive:         g.IsActive,
		CreatedAt:        g.CreatedAt.UTC(),
		UpdatedAt:        g.UpdatedAt.UTC(),
		ConcurrencyStamp: g.Version.String(),
	}
}

// normalizeGroupName is .NET's Normalize (EA/ICP:130-135): a display name
// that is not blank, trimmed, at most 200 characters (UTF-16) and free of
// control characters. ok is false for any other.
func normalizeGroupName(value *string) (name string, ok bool) {
	if blank(value) {
		return "", false
	}
	name = strings.TrimSpace(*value)
	if utf16Length(name) > maxDisplayNameLength || strings.ContainsFunc(name, unicode.IsControl) {
		return "", false
	}
	return name, true
}

// groupResponse is .NET's ToResponse (EA/ICP:124-128): a group with its
// effective members and its mapped roles, each by id.
func groupResponse(r store.ListAccessGroupsRow) gen.AccessGroupResponse {
	return gen.AccessGroupResponse{
		Id:               r.ID,
		DisplayName:      r.DisplayName,
		Source:           sourceName(r.Source),
		ScimConnectionId: nil,
		IsActive:         r.IsActive,
		CreatedAt:        r.CreatedAt.UTC(),
		UpdatedAt:        r.UpdatedAt.UTC(),
		ConcurrencyStamp: r.Version.String(),
		MemberUserIds:    nonNil(r.MemberUserIds),
		RoleIds:          nonNil(r.RoleIds),
	}
}

// readGroup is group id as a response shows it, read on q.
func readGroup(ctx context.Context, q *store.Queries, id uuid.UUID) (gen.AccessGroupResponse, error) {
	row, err := q.GetAccessGroup(ctx, id)
	if err != nil {
		return gen.AccessGroupResponse{}, fmt.Errorf("identity: read access group: %w", err)
	}
	return groupResponse(store.ListAccessGroupsRow(row)), nil
}

// lockGroup holds group id's row (LockAccessGroup): the bare 404 when there
// is no such group.
func lockGroup(ctx context.Context, q *store.Queries, id uuid.UUID) (store.IdentityAccessGroup, error) {
	g, err := q.LockAccessGroup(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, notFound
	}
	return g, err
}

// touchGroup gives g, whose members or role mappings changed, a new version
// at now, and returns g as it now is.
func touchGroup(ctx context.Context, q *store.Queries, g store.IdentityAccessGroup, now time.Time) (store.IdentityAccessGroup, error) {
	next := g
	next.Version, next.UpdatedAt = uuid.New(), now
	changed, err := q.TouchAccessGroup(ctx, store.TouchAccessGroupParams{Version: next.Version, Now: now, ID: g.ID, ExpectedVersion: g.Version})
	if err != nil {
		return g, err
	}
	if changed == 0 {
		return g, errConcurrentChange
	}
	return next, nil
}

// groupTx runs fn in one READ COMMITTED transaction at now, retried while
// it is chosen as a deadlock victim, as accessTx does. What fn returns
// comes back as it is, lost races included, for the route group's filter
// to answer (accessConflictFilter), with one exception. A scimRejection
// is audited in a transaction of its own, once fn's has rolled back, and
// answered 409 scim_group_managed: .NET committed the rejected row in its
// own transaction before it refused (SV/AGM:320-340), so the row stays
// although the change does not happen.
//
// now is the clock at microseconds, the precision the columns keep, so the
// times an audit row records are the ones stored. The rejection's audit
// row records the now of the attempt that was rejected, and is written
// with the same READ COMMITTED transaction and deadlock retry as the
// mutation.
func (s *server) groupTx(ctx context.Context, p contracts.Principal, r *http.Request, fn func(q *store.Queries, now time.Time) error) error {
	var now time.Time
	err := db.RetrySerializable(ctx, serializableAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			now = s.deps.Clock().UTC().Truncate(time.Microsecond)
			return fn(store.New(tx), now)
		})
	})
	var rejected scimRejection
	if !errors.As(err, &rejected) {
		return err
	}
	if err := db.RetrySerializable(ctx, serializableAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
			return writeAudit(ctx, store.New(tx), r, now, auditEvent{
				actor:  &p.UserID,
				action: rejected.action,
				before: snapshotOf(rejected.group),
				after:  groupRejected{GroupID: rejected.group.ID, UserID: rejected.userID, Rejected: true, Reason: scimRejectionReason},
				mfa:    p.MFAVerified,
			})
		})
	}); err != nil {
		return err
	}
	return groupScimManaged
}

// callerAndRequest is the principal and request every group mutation
// audits with.
func callerAndRequest(ctx context.Context) (contracts.Principal, *http.Request, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return contracts.Principal{}, nil, err
	}
	r, err := requestFrom(ctx)
	return p, r, err
}

// GetIdentityAccessGroups lists every access group, SCIM groups included,
// with its effective members and mapped roles (EA/ICP:40-45), in one query
// however many groups there are (ListAccessGroups).
func (s *server) GetIdentityAccessGroups(ctx context.Context, _ gen.GetIdentityAccessGroupsRequestObject) (gen.GetIdentityAccessGroupsResponseObject, error) {
	rows, err := s.q.ListAccessGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list access groups: %w", err)
	}
	out := make(gen.GetIdentityAccessGroups200JSONResponse, len(rows))
	for i, r := range rows {
		out[i] = groupResponse(r)
	}
	return out, nil
}

// GetIdentityAccessGroupsById is one local group (EA/ICP:47-52): the bare
// 404 for an unknown group and for a SCIM group, which the list still
// shows.
func (s *server) GetIdentityAccessGroupsById(ctx context.Context, req gen.GetIdentityAccessGroupsByIdRequestObject) (gen.GetIdentityAccessGroupsByIdResponseObject, error) {
	row, err := s.q.GetAccessGroup(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Source != groupSourceLocal) {
		return gen.GetIdentityAccessGroupsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read access group: %w", err)
	}
	return gen.GetIdentityAccessGroupsById200JSONResponse(groupResponse(store.ListAccessGroupsRow(row))), nil
}

// PostIdentityAccessGroups creates a local group (EA/ICP:54-67,
// SV/AGM:28-50), in .NET's order: 400 invalid_group, then 409 group_exists
// for a display name another group has exactly. isActive is false when the
// request leaves it out, as .NET's AccessGroupRequest bound it.
// access_group.created is audited, and the answer is 201 with the group and
// its Location. A create that loses a race for its name is 409
// group_conflict, the handler's own catch.
func (s *server) PostIdentityAccessGroups(ctx context.Context, req gen.PostIdentityAccessGroupsRequestObject) (gen.PostIdentityAccessGroupsResponseObject, error) {
	var body gen.AccessGroupRequest
	if req.Body != nil {
		body = *req.Body
	}
	name, ok := normalizeGroupName(body.DisplayName)
	if !ok {
		return groupInvalid, nil
	}
	p, r, err := callerAndRequest(ctx)
	if err != nil {
		return nil, err
	}
	id, version := uuid.New(), uuid.New()
	active := body.IsActive != nil && *body.IsActive
	var created gen.AccessGroupResponse
	err = s.groupTx(ctx, p, r, func(q *store.Queries, now time.Time) error {
		taken, err := q.AccessGroupNameTaken(ctx, store.AccessGroupNameTakenParams{DisplayName: name, ID: uuid.Nil})
		if err != nil || taken {
			return cmpOr(err, error(groupExists))
		}
		if err := q.InsertAccessGroup(ctx, store.InsertAccessGroupParams{ID: id, DisplayName: name, IsActive: active, Version: version, Now: now}); err != nil {
			return err
		}
		g := store.IdentityAccessGroup{ID: id, DisplayName: name, Source: groupSourceLocal, IsActive: active, Version: version, CreatedAt: now, UpdatedAt: now}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: "access_group.created",
			before: struct{}{},
			after:  entityOf(g),
			mfa:    p.MFAVerified,
		}); err != nil {
			return err
		}
		created, err = readGroup(ctx, q, id)
		return err
	})
	if isAuthorizationConflict(err) {
		err = groupCreateConflict
	}
	if err != nil {
		return refusalOr[gen.PostIdentityAccessGroupsResponseObject](err)
	}
	return groupCreated{
		location: s.deps.Config.BasePath + accessGroupsPath + "/" + id.String(),
		body:     gen.PostIdentityAccessGroups201JSONResponse(created),
	}, nil
}

// PutIdentityAccessGroupsById renames a group and sets whether it is active
// (EA/ICP:69-85, SV/AGM:52-76), in .NET's order: 400 invalid_group; then,
// in one transaction holding the group's row, the bare 404, 400
// concurrency_required, 409 group_conflict unless the stamp is the group's
// version, 409 group_exists for a display name another group has, and a
// SCIM group's rejection (scimRejection). A missing isActive deactivates
// the group, as .NET bound it. access_group.updated is audited, and the
// answer is the group as it now is.
func (s *server) PutIdentityAccessGroupsById(ctx context.Context, req gen.PutIdentityAccessGroupsByIdRequestObject) (gen.PutIdentityAccessGroupsByIdResponseObject, error) {
	var body gen.AccessGroupRequest
	if req.Body != nil {
		body = *req.Body
	}
	name, ok := normalizeGroupName(body.DisplayName)
	if !ok {
		return groupInvalid, nil
	}
	p, r, err := callerAndRequest(ctx)
	if err != nil {
		return nil, err
	}
	active := body.IsActive != nil && *body.IsActive
	var updated gen.AccessGroupResponse
	err = s.groupTx(ctx, p, r, func(q *store.Queries, now time.Time) error {
		g, err := lockGroup(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if blank(body.ConcurrencyStamp) {
			return groupStampRequired
		}
		if *body.ConcurrencyStamp != g.Version.String() {
			return groupChanged
		}
		taken, err := q.AccessGroupNameTaken(ctx, store.AccessGroupNameTakenParams{DisplayName: name, ID: g.ID})
		if err != nil || taken {
			return cmpOr(err, error(groupExists))
		}
		if g.Source != groupSourceLocal {
			return scimRejection{action: "access_group.update_rejected", group: g}
		}
		next := g
		next.DisplayName, next.IsActive, next.Version, next.UpdatedAt = name, active, uuid.New(), now
		changed, err := q.UpdateAccessGroup(ctx, store.UpdateAccessGroupParams{
			DisplayName:     name,
			IsActive:        active,
			Version:         next.Version,
			Now:             now,
			ID:              g.ID,
			ExpectedVersion: g.Version,
		})
		if err != nil {
			return err
		}
		if changed == 0 {
			return errConcurrentChange
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: "access_group.updated",
			before: snapshotOf(g),
			after:  entityOf(next),
			mfa:    p.MFAVerified,
		}); err != nil {
			return err
		}
		updated, err = readGroup(ctx, q, g.ID)
		return err
	})
	if err != nil {
		return refusalOr[gen.PutIdentityAccessGroupsByIdResponseObject](err)
	}
	return gen.PutIdentityAccessGroupsById200JSONResponse(updated), nil
}

// DeleteIdentityAccessGroupsById deletes a group with its memberships and
// role mappings (EA/ICP:87-94, SV/AGM:78-94), in .NET's order, in one
// transaction holding the group's row: the bare 404, 400
// concurrency_required, a SCIM group's rejection (scimRejection), and a
// stale stamp, which the route group's filter answers 409
// concurrency_conflict. access_group.deleted is audited. 204.
func (s *server) DeleteIdentityAccessGroupsById(ctx context.Context, req gen.DeleteIdentityAccessGroupsByIdRequestObject) (gen.DeleteIdentityAccessGroupsByIdResponseObject, error) {
	var stamp *string
	if req.Body != nil {
		stamp = req.Body.ConcurrencyStamp
	}
	p, r, err := callerAndRequest(ctx)
	if err != nil {
		return nil, err
	}
	err = s.groupTx(ctx, p, r, func(q *store.Queries, now time.Time) error {
		g, err := lockGroup(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if blank(stamp) {
			return groupStampRequired
		}
		if g.Source != groupSourceLocal {
			return scimRejection{action: "access_group.delete_rejected", group: g}
		}
		if *stamp != g.Version.String() {
			return errConcurrentChange
		}
		deleted, err := q.DeleteAccessGroup(ctx, store.DeleteAccessGroupParams{ID: g.ID, ExpectedVersion: g.Version})
		if err != nil {
			return err
		}
		if deleted == 0 {
			return errConcurrentChange
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: "access_group.deleted",
			before: snapshotOf(g),
			after:  struct{}{},
			mfa:    p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityAccessGroupsByIdResponseObject](err)
	}
	return gen.DeleteIdentityAccessGroupsById204Response{}, nil
}

// memberMutation is .NET's MemberMutation (EA/ICP:99-108) with
// AddMemberAsync and RemoveMemberAsync (SV/AGM:96-156), in their order: 400
// concurrency_required; then, in one transaction holding the group's row,
// the bare 404, a SCIM group's rejection (scimRejection), a stale stamp
// (the filter's 409 concurrency_conflict), and, to add, 400 user_not_found.
//
// Adding makes the user a local forced member, whatever their row said
// before: source local, not upstream-present, override force_member
// (SV/AGM:122-124). A membership counts when it is force_member, or when it
// has no override and upstream says the user is present; force_non_member
// excludes the user whatever upstream says (AZ/PermissionAuthorization.cs:80-93,
// SV/AGM:260-262). Removing deletes the row, override and all.
//
// The group gets a new version, the change is audited, and the answer is
// the group as it now is. No user's session ends: .NET rotated no security
// stamp for a membership (SV/AGM:96-156; its one SaveChanges interceptor
// reacts to user rows alone, SV/SessionStateCache.cs:46-75), and every
// permission check reads memberships afresh, so the change applies to the
// member's very next request.
func (s *server) memberMutation(ctx context.Context, groupID, userID uuid.UUID, body *gen.MutationRequest, add bool) (gen.AccessGroupResponse, error) {
	var stamp *string
	if body != nil {
		stamp = body.ConcurrencyStamp
	}
	if blank(stamp) {
		return gen.AccessGroupResponse{}, groupStampRequired
	}
	p, r, err := callerAndRequest(ctx)
	if err != nil {
		return gen.AccessGroupResponse{}, err
	}
	action, rejected := "access_group.member_removed", "access_group.member_remove_rejected"
	if add {
		action, rejected = "access_group.member_added", "access_group.member_add_rejected"
	}
	var out gen.AccessGroupResponse
	err = s.groupTx(ctx, p, r, func(q *store.Queries, now time.Time) error {
		g, err := lockGroup(ctx, q, groupID)
		if err != nil {
			return err
		}
		if g.Source != groupSourceLocal {
			return scimRejection{action: rejected, group: g, userID: &userID}
		}
		if *stamp != g.Version.String() {
			return errConcurrentChange
		}
		if add {
			if _, err := q.KeyShareMember(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
				return groupUserNotFound
			} else if err != nil {
				return err
			}
			if err := q.UpsertForcedMembership(ctx, store.UpsertForcedMembershipParams{GroupID: groupID, UserID: userID}); err != nil {
				return err
			}
		} else if err := q.DeleteMembership(ctx, store.DeleteMembershipParams{GroupID: groupID, UserID: userID}); err != nil {
			return err
		}
		next, err := touchGroup(ctx, q, g, now)
		if err != nil {
			return err
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: action,
			before: snapshotOf(g),
			after:  groupMemberChange{Group: snapshotOf(next), UserID: userID},
			mfa:    p.MFAVerified,
		}); err != nil {
			return err
		}
		out, err = readGroup(ctx, q, groupID)
		return err
	})
	return out, err
}

// roleMappingMutation is .NET's RoleMappingMutation (EA/ICP:113-122) with
// AddRoleMappingAsync and RemoveRoleMappingAsync (SV/AGM:158-247), in their
// order: 400 concurrency_required; then, in one transaction, the bare 404,
// 400 scim_scope_conflict (ValidateGroupScope, SV/AGM:305-318), a stale
// stamp (the filter's 409 concurrency_conflict), and, to map, 400
// role_not_found and 400 protected_role for Owner, a system or built-in
// role, or a role holding a key that may not be delegated.
//
// The transaction takes the role's lock (lockRole) first, then holds the
// group's row, and reads everything after both, at READ COMMITTED. It is
// the lock role edits and deletions take (AZ/AuthorizationMutationService.cs:20-31,
// SV/AGM:172, :235): a mapping queued behind an edit reads the keys the
// edit left, and an edit queued behind a mapping sees the mapping
// (guardGroupMappedRole), so no group ever maps a role that holds a
// non-delegable key such as identity:manage. A request maps one role, so it
// takes one role lock.
//
// .NET's single SCIM connection collapses to source scim here (spec
// *SCIM*): no group is bound to a connection id, and every group's
// scimConnectionId is null. A request that names a connection is refused,
// for a local group as .NET refused one, and for a SCIM group as naming
// another connection than the group's. A SCIM group's role mappings are the
// Owner's to change, as in .NET; a mapping takes its group's source.
//
// The group gets a new version, the change is audited, and the answer is
// the group as it now is. No user's session ends, as for a membership: .NET
// rotated no security stamp for a mapping (SV/AGM:158-247).
func (s *server) roleMappingMutation(ctx context.Context, groupID, roleID uuid.UUID, body *gen.MutationRequest, add bool) (gen.AccessGroupResponse, error) {
	var stamp *string
	var connection *uuid.UUID
	if body != nil {
		stamp, connection = body.ConcurrencyStamp, body.ScimConnectionId
	}
	if blank(stamp) {
		return gen.AccessGroupResponse{}, groupStampRequired
	}
	p, r, err := callerAndRequest(ctx)
	if err != nil {
		return gen.AccessGroupResponse{}, err
	}
	action := "access_group.role_unmapped"
	if add {
		action = "access_group.role_mapped"
	}
	var out gen.AccessGroupResponse
	err = s.groupTx(ctx, p, r, func(q *store.Queries, now time.Time) error {
		if err := lockRole(ctx, q, roleID); err != nil {
			return err
		}
		g, err := lockGroup(ctx, q, groupID)
		if err != nil {
			return err
		}
		if connection != nil && g.Source == groupSourceSCIM {
			return groupScimScopeConflict
		}
		if connection != nil {
			return groupLocalScopeConflict
		}
		if *stamp != g.Version.String() {
			return errConcurrentChange
		}
		if add {
			role, err := q.GetMappingRole(ctx, roleID)
			if errors.Is(err, pgx.ErrNoRows) {
				return groupRoleNotFound
			}
			if err != nil {
				return err
			}
			if role.IsSystem || role.IsBuiltIn || role.Name == RoleOwner {
				return groupProtectedRole
			}
			if slices.ContainsFunc(role.PermissionKeys, s.protectedPermission) {
				return groupProtectedRoleKeys
			}
			if err := q.InsertRoleMapping(ctx, store.InsertRoleMappingParams{GroupID: groupID, RoleID: roleID, Source: g.Source}); err != nil {
				return err
			}
		} else if err := q.DeleteRoleMapping(ctx, store.DeleteRoleMappingParams{GroupID: groupID, RoleID: roleID}); err != nil {
			return err
		}
		next, err := touchGroup(ctx, q, g, now)
		if err != nil {
			return err
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor:  &p.UserID,
			action: action,
			before: snapshotOf(g),
			after:  groupRoleChange{Group: snapshotOf(next), RoleID: roleID},
			mfa:    p.MFAVerified,
		}); err != nil {
			return err
		}
		out, err = readGroup(ctx, q, groupID)
		return err
	})
	return out, err
}

// PostIdentityAccessGroupsByGroupIdMembersByUserId adds a forced member
// (memberMutation).
func (s *server) PostIdentityAccessGroupsByGroupIdMembersByUserId(ctx context.Context, req gen.PostIdentityAccessGroupsByGroupIdMembersByUserIdRequestObject) (gen.PostIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject, error) {
	g, err := s.memberMutation(ctx, req.GroupId, req.UserId, req.Body, true)
	if err != nil {
		return refusalOr[gen.PostIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject](err)
	}
	return gen.PostIdentityAccessGroupsByGroupIdMembersByUserId200JSONResponse(g), nil
}

// PutIdentityAccessGroupsByGroupIdMembersByUserId is the same addition, as
// .NET mapped both verbs to AddMember (EA/ICP:30-31).
func (s *server) PutIdentityAccessGroupsByGroupIdMembersByUserId(ctx context.Context, req gen.PutIdentityAccessGroupsByGroupIdMembersByUserIdRequestObject) (gen.PutIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject, error) {
	g, err := s.memberMutation(ctx, req.GroupId, req.UserId, req.Body, true)
	if err != nil {
		return refusalOr[gen.PutIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject](err)
	}
	return gen.PutIdentityAccessGroupsByGroupIdMembersByUserId200JSONResponse(g), nil
}

// DeleteIdentityAccessGroupsByGroupIdMembersByUserId removes a member's row
// (memberMutation).
func (s *server) DeleteIdentityAccessGroupsByGroupIdMembersByUserId(ctx context.Context, req gen.DeleteIdentityAccessGroupsByGroupIdMembersByUserIdRequestObject) (gen.DeleteIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject, error) {
	g, err := s.memberMutation(ctx, req.GroupId, req.UserId, req.Body, false)
	if err != nil {
		return refusalOr[gen.DeleteIdentityAccessGroupsByGroupIdMembersByUserIdResponseObject](err)
	}
	return gen.DeleteIdentityAccessGroupsByGroupIdMembersByUserId200JSONResponse(g), nil
}

// PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleId maps a role to a
// group (roleMappingMutation).
func (s *server) PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleId(ctx context.Context, req gen.PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdRequestObject) (gen.PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject, error) {
	g, err := s.roleMappingMutation(ctx, req.GroupId, req.RoleId, req.Body, true)
	if err != nil {
		return refusalOr[gen.PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject](err)
	}
	return gen.PostIdentityAccessGroupsByGroupIdRoleMappingsByRoleId200JSONResponse(g), nil
}

// PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleId is the same mapping,
// as .NET mapped both verbs to AddRoleMapping (EA/ICP:33-34).
func (s *server) PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleId(ctx context.Context, req gen.PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdRequestObject) (gen.PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject, error) {
	g, err := s.roleMappingMutation(ctx, req.GroupId, req.RoleId, req.Body, true)
	if err != nil {
		return refusalOr[gen.PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject](err)
	}
	return gen.PutIdentityAccessGroupsByGroupIdRoleMappingsByRoleId200JSONResponse(g), nil
}

// DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleId unmaps a role
// (roleMappingMutation).
func (s *server) DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleId(ctx context.Context, req gen.DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdRequestObject) (gen.DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject, error) {
	g, err := s.roleMappingMutation(ctx, req.GroupId, req.RoleId, req.Body, false)
	if err != nil {
		return refusalOr[gen.DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponseObject](err)
	}
	return gen.DeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleId200JSONResponse(g), nil
}

// groupCreated is the group creation's 201: its Location, then the
// generated body.
type groupCreated struct {
	location string
	body     gen.PostIdentityAccessGroups201JSONResponse
}

func (g groupCreated) VisitPostIdentityAccessGroupsResponse(w http.ResponseWriter) error {
	w.Header().Set("Location", g.location)
	return g.body.VisitPostIdentityAccessGroupsResponse(w)
}

// The refusals the access group operations answer with.

func (r refusal) VisitPostIdentityAccessGroupsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityAccessGroupsByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityAccessGroupsByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccessGroupsByGroupIdMembersByUserIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityAccessGroupsByGroupIdMembersByUserIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityAccessGroupsByGroupIdMembersByUserIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityAccessGroupsByGroupIdRoleMappingsByRoleIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}
