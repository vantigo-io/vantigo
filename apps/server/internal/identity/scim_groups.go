package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// SCIM Groups: access groups with source scim (SV/SCIM:362-537). SCIM
// writes only what upstream says of a member, is_upstream_present, on the
// member's SCIM-sourced row; a local include or exclude stays as it is, so
// the Owner's override always wins (UpsertScimMembership). A group's ETag is
// its version. The Owner's group API refuses a SCIM group's content
// (groups.go, 409 scim_group_managed) but may map roles to it.

// scimGroupFacts is ScimGroupAuditFacts (SV/SCIM:1208-1209), a Group
// event's before and after.
type scimGroupFacts struct {
	ConnectionID              uuid.UUID   `json:"ConnectionId"`
	ResourceID                uuid.UUID   `json:"ResourceId"`
	Active                    bool        `json:"Active"`
	UpstreamMemberResourceIDs []uuid.UUID `json:"UpstreamMemberResourceIds"`
}

// scimGroupFactsOf is CaptureGroupFactsAsync (SV/SCIM:999-1008).
func scimGroupFactsOf(ctx context.Context, q *store.Queries, g store.IdentityAccessGroup) (scimGroupFacts, error) {
	members, err := q.ScimGroupUpstreamMembers(ctx, g.ID)
	if err != nil {
		return scimGroupFacts{}, fmt.Errorf("identity: SCIM group facts: %w", err)
	}
	return scimGroupFacts{ConnectionID: scimConnectionID, ResourceID: g.ID, Active: g.IsActive, UpstreamMemberResourceIDs: nonNil(members)}, nil
}

// scimGroup is ReadGroupResourceAsync (SV/SCIM:612-638): the group with
// meta and its ETag, and, unless excluded, its members.
func (s *server) scimGroup(g store.IdentityAccessGroup, members []uuid.UUID, withMembers bool) gen.ScimGroup {
	out := gen.ScimGroup{
		Schemas:     []string{scimGroupSchema},
		Id:          g.ID.String(),
		ExternalId:  g.ExternalID,
		DisplayName: g.DisplayName,
		Active:      g.IsActive,
		Meta: gen.ScimMeta{
			ResourceType: "Group",
			Created:      g.CreatedAt.UTC(),
			LastModified: g.UpdatedAt.UTC(),
			Location:     s.scimGroupLocation(g.ID),
			Version:      g.Version.String(),
		},
	}
	if withMembers {
		list := make([]gen.ScimMember, 0, len(members))
		for _, id := range members {
			list = append(list, gen.ScimMember{Value: id.String(), Type: "User", Ref: s.scimUserLocation(id)})
		}
		out.Members = &list
	}
	return out
}

// scimGroupMembers is the members each of groups lists, its effective
// SCIM-sourced members by resource id (ScimGroupMembers), in one query.
func scimGroupMembers(ctx context.Context, q *store.Queries, groups ...uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	rows, err := q.ScimGroupMembers(ctx, groups)
	if err != nil {
		return nil, fmt.Errorf("identity: SCIM group members: %w", err)
	}
	out := make(map[uuid.UUID][]uuid.UUID, len(groups))
	for _, row := range rows {
		out[row.GroupID] = append(out[row.GroupID], row.ResourceID)
	}
	return out, nil
}

// scimGroupID is a Group's id as a path names it, any form Guid.TryParse
// reads (SV/SCIM:411).
func scimGroupID(id string) (uuid.UUID, bool) {
	return parseGUID(id)
}

// lockScimGroup holds the SCIM group id: 404 for an unknown one or a local
// group.
func lockScimGroup(ctx context.Context, q *store.Queries, id uuid.UUID) (store.IdentityAccessGroup, error) {
	g, err := q.LockAccessGroup(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && g.Source != groupSourceSCIM {
		return store.IdentityAccessGroup{}, scimNotFound
	}
	return g, err
}

// applyScimMembers is ApplyGroupMembersAsync (SV/SCIM:644-675): at most
// scimMaxMembers ids, every distinct one a known User's, and each member's
// row then records present (UpsertScimMembership), in user id order.
func applyScimMembers(ctx context.Context, q *store.Queries, groupID uuid.UUID, resourceIDs []string, present bool) error {
	if len(resourceIDs) > scimMaxMembers {
		return scimTooManyMembers
	}
	distinct := map[string]bool{}
	ids := []uuid.UUID{}
	for _, v := range resourceIDs {
		if distinct[v] {
			continue
		}
		distinct[v] = true
		if id, ok := scimUserID(v); ok {
			ids = append(ids, id)
		}
	}
	users, err := q.ScimUsersByResourceID(ctx, ids)
	if err != nil {
		return err
	}
	if len(users) != len(distinct) {
		return scimUnknownMember
	}
	for _, u := range users {
		if err := q.UpsertScimMembership(ctx, store.UpsertScimMembershipParams{GroupID: groupID, UserID: u.UserID, IsUpstreamPresent: present}); err != nil {
			return err
		}
	}
	return nil
}

// PostIdentityScimV2Groups is CreateGroupAsync (SV/SCIM:362-405): the
// resource is read; a display name any group has, or an externalId a SCIM
// group has, is 409 uniqueness. The group is created active as the
// resource says (default true), with the resource's externalId or a new
// GUID. More than scimMaxMembers members is 400 tooMany, and every member
// must be a known User; each is upstream-present. Audited
// scim.group.created; 201 with the group, its Location and its ETag.
//
// A display name over 200 characters is 400 invalidValue, as is an
// externalId over the column's 256: .NET validated neither against its
// columns (:731, :752), and its database refused them with a 500.
func (s *server) PostIdentityScimV2Groups(ctx context.Context, _ gen.PostIdentityScimV2GroupsRequestObject) (gen.PostIdentityScimV2GroupsResponseObject, error) {
	in, r, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	input, err := readScimGroup(in.body)
	if err != nil {
		return scimOr[gen.PostIdentityScimV2GroupsResponseObject](err)
	}
	if utf16Length(input.displayName) > maxDisplayNameLength {
		return scimInvalidValue("displayName is invalid."), nil
	}
	if input.externalID != nil && utf16Length(*input.externalID) > maxGroupExternalIDLength {
		return scimInvalidValue("externalId is invalid."), nil
	}
	conflicts, err := s.q.ScimGroupConflicts(ctx, store.ScimGroupConflictsParams{DisplayName: input.displayName, ExternalID: input.externalID})
	if err != nil {
		return nil, fmt.Errorf("identity: SCIM group conflicts: %w", err)
	}
	if conflicts {
		return scimGroupConflict, nil
	}
	var created store.IdentityAccessGroup
	var members map[uuid.UUID][]uuid.UUID
	err = s.scimTx(ctx, func(q *store.Queries, now time.Time) error {
		externalID := input.externalID
		if externalID == nil {
			externalID = ptr(uuid.NewString())
		}
		g := store.IdentityAccessGroup{
			ID:          uuid.New(),
			DisplayName: input.displayName,
			Source:      groupSourceSCIM,
			ExternalID:  externalID,
			IsActive:    input.active,
			Version:     uuid.New(),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := q.InsertScimGroup(ctx, store.InsertScimGroupParams{
			ID: g.ID, DisplayName: g.DisplayName, ExternalID: g.ExternalID, IsActive: g.IsActive, Version: g.Version, Now: now,
		}); err != nil {
			return err
		}
		if len(input.memberIDs) > scimMaxMembers {
			return scimTooManyMembers
		}
		if err := applyScimMembers(ctx, q, g.ID, input.memberIDs, true); err != nil {
			return err
		}
		facts, err := scimGroupFactsOf(ctx, q, g)
		if err != nil {
			return err
		}
		if err := writeScimAudit(ctx, q, r, now, "scim.group.created", nil,
			scimCreatedBefore{ScimConnectionID: scimConnectionID, ResourceID: g.ID.String()}, facts); err != nil {
			return err
		}
		created = g
		members, err = scimGroupMembers(ctx, q, g.ID)
		return err
	})
	if db.IsUniqueViolation(err, "") {
		err = scimGroupConflict
	}
	if err != nil {
		return scimOr[gen.PostIdentityScimV2GroupsResponseObject](err)
	}
	location := s.scimGroupLocation(created.ID)
	return gen.PostIdentityScimV2Groups201ApplicationScimPlusJSONResponse{
		Body:    s.scimGroup(created, members[created.ID], true),
		Headers: gen.PostIdentityScimV2Groups201ResponseHeaders{ETag: quotedETag(created.Version.String()), Location: &location},
	}, nil
}

// GetIdentityScimV2GroupsById is GetGroupAsync (SV/SCIM:407-416): the SCIM
// group with its ETag, its members unless excludedAttributes lists them,
// or 404.
func (s *server) GetIdentityScimV2GroupsById(ctx context.Context, req gen.GetIdentityScimV2GroupsByIdRequestObject) (gen.GetIdentityScimV2GroupsByIdResponseObject, error) {
	in, _, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	id, ok := scimGroupID(req.Id)
	if !ok {
		return scimNotFound, nil
	}
	g, err := s.q.GetScimGroup(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return scimNotFound, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read SCIM group: %w", err)
	}
	withMembers := !scimMembersExcluded(in.query)
	var members map[uuid.UUID][]uuid.UUID
	if withMembers {
		if members, err = scimGroupMembers(ctx, s.q, g.ID); err != nil {
			return nil, err
		}
	}
	return gen.GetIdentityScimV2GroupsById200ApplicationScimPlusJSONResponse{
		Body:    s.scimGroup(g, members[g.ID], withMembers),
		Headers: gen.GetIdentityScimV2GroupsById200ResponseHeaders{ETag: quotedETag(g.Version.String())},
	}, nil
}

// GetIdentityScimV2Groups is ListGroupsAsync (SV/SCIM:418-441): paging, then
// the filter on displayName and externalId, then a page of the SCIM groups
// it admits, by id, with their members unless excluded.
func (s *server) GetIdentityScimV2Groups(ctx context.Context, _ gen.GetIdentityScimV2GroupsRequestObject) (gen.GetIdentityScimV2GroupsResponseObject, error) {
	in, _, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	startIndex, count, err := scimPaging(in.query)
	if err != nil {
		return scimOr[gen.GetIdentityScimV2GroupsResponseObject](err)
	}
	raw, _ := queryValue(in.query, "filter")
	clauses, err := scimFilter(raw, "displayName", "externalId")
	if err != nil {
		return scimOr[gen.GetIdentityScimV2GroupsResponseObject](err)
	}
	displayNames, externalIDs := scimFilterValues(clauses, "displayName")
	total, err := s.q.CountScimGroups(ctx, store.CountScimGroupsParams{DisplayNames: displayNames, ExternalIds: externalIDs})
	if err != nil {
		return nil, fmt.Errorf("identity: count SCIM groups: %w", err)
	}
	groups, err := s.q.ListScimGroups(ctx, store.ListScimGroupsParams{DisplayNames: displayNames, ExternalIds: externalIDs, Skip: int64(startIndex - 1), Take: int64(count)})
	if err != nil {
		return nil, fmt.Errorf("identity: list SCIM groups: %w", err)
	}
	withMembers := !scimMembersExcluded(in.query)
	var members map[uuid.UUID][]uuid.UUID
	if withMembers && len(groups) > 0 {
		ids := make([]uuid.UUID, len(groups))
		for i, g := range groups {
			ids[i] = g.ID
		}
		if members, err = scimGroupMembers(ctx, s.q, ids...); err != nil {
			return nil, err
		}
	}
	resources := make([]gen.ScimGroup, 0, len(groups))
	for _, g := range groups {
		resources = append(resources, s.scimGroup(g, members[g.ID], withMembers))
	}
	return gen.GetIdentityScimV2Groups200ApplicationScimPlusJSONResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: int32(total),
		StartIndex:   int32(startIndex),
		ItemsPerPage: int32(len(resources)),
		Resources:    resources,
	}, nil
}

// PatchIdentityScimV2GroupsById is PatchGroupAsync (SV/SCIM:443-510), in
// .NET's order: 404 for an id that is no GUID, the PatchOp read, 404, 412
// for a stale If-Match and for a stale meta.version in the body, the
// operations validated (scimGroupActions), and 400 tooMany for more than
// scimMaxMembers member ids in all. Then each action in turn: the display
// name, the state, or members, where replace and remove-all first make the
// other (or every) SCIM-sourced member absent. The group gets a new version
// and is audited scim.group.patched; 200 with the group and its ETag.
func (s *server) PatchIdentityScimV2GroupsById(ctx context.Context, req gen.PatchIdentityScimV2GroupsByIdRequestObject) (gen.PatchIdentityScimV2GroupsByIdResponseObject, error) {
	in, r, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	id, ok := scimGroupID(req.Id)
	if !ok {
		return scimNotFound, nil
	}
	ops, err := readScimPatch(in.body)
	if err != nil {
		return scimOr[gen.PatchIdentityScimV2GroupsByIdResponseObject](err)
	}
	var patched store.IdentityAccessGroup
	var members map[uuid.UUID][]uuid.UUID
	err = s.scimTx(ctx, func(q *store.Queries, now time.Time) error {
		g, err := lockScimGroup(ctx, q, id)
		if err != nil {
			return err
		}
		if err := scimPrecondition(req.Params.IfMatch, g.Version.String()); err != nil {
			return err
		}
		if scimBodyVersionStale(in.body, g.Version.String()) {
			return scimStaleVersion
		}
		actions, err := scimGroupActions(ops)
		if err != nil {
			return err
		}
		total := 0
		for _, a := range actions {
			total += len(a.memberIDs)
		}
		if total > scimMaxMembers {
			return scimTooManyMembers
		}
		before, err := scimGroupFactsOf(ctx, q, g)
		if err != nil {
			return err
		}
		next := g
		for _, a := range actions {
			switch a.field {
			case "displayName":
				next.DisplayName = a.name
			case "active":
				next.IsActive = a.active
			default:
				if err := applyScimMemberAction(ctx, q, g.ID, a); err != nil {
					return err
				}
			}
		}
		next.Version, next.UpdatedAt = uuid.New(), now
		if err := q.UpdateScimGroup(ctx, store.UpdateScimGroupParams{
			DisplayName: next.DisplayName, IsActive: next.IsActive, Version: next.Version, Now: now, ID: g.ID,
		}); err != nil {
			return err
		}
		after, err := scimGroupFactsOf(ctx, q, next)
		if err != nil {
			return err
		}
		if err := writeScimAudit(ctx, q, r, now, "scim.group.patched", nil, before, after); err != nil {
			return err
		}
		patched = next
		members, err = scimGroupMembers(ctx, q, g.ID)
		return err
	})
	if db.IsUniqueViolation(err, "") {
		err = scimGroupConflict
	}
	if err != nil {
		return scimOr[gen.PatchIdentityScimV2GroupsByIdResponseObject](err)
	}
	return gen.PatchIdentityScimV2GroupsById200ApplicationScimPlusJSONResponse{
		Body:    s.scimGroup(patched, members[patched.ID], true),
		Headers: gen.PatchIdentityScimV2GroupsById200ResponseHeaders{ETag: quotedETag(patched.Version.String())},
	}, nil
}

// applyScimMemberAction applies one members action (SV/SCIM:469-491).
func applyScimMemberAction(ctx context.Context, q *store.Queries, groupID uuid.UUID, a scimGroupAction) error {
	if a.replace || a.removeAll {
		keep := []uuid.UUID{}
		if !a.removeAll {
			for _, v := range a.memberIDs {
				if id, ok := scimUserID(v); ok {
					keep = append(keep, id)
				}
			}
		}
		if err := q.MarkScimGroupMembershipsAbsent(ctx, store.MarkScimGroupMembershipsAbsentParams{GroupID: groupID, Keep: keep}); err != nil {
			return err
		}
	}
	if a.removeAll {
		return nil
	}
	return applyScimMembers(ctx, q, groupID, a.memberIDs, a.present)
}

// DeleteIdentityScimV2GroupsById is DeleteGroupAsync (SV/SCIM:512-537): 404,
// then 412 for a stale If-Match or X-SCIM-Meta-Version; the group becomes
// inactive with a new version, and upstream no longer lists any of its
// SCIM-sourced members. It stays, and a later GET shows active:false.
// Audited scim.group.deleted; 204 with the new ETag.
func (s *server) DeleteIdentityScimV2GroupsById(ctx context.Context, req gen.DeleteIdentityScimV2GroupsByIdRequestObject) (gen.DeleteIdentityScimV2GroupsByIdResponseObject, error) {
	_, r, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	id, ok := scimGroupID(req.Id)
	if !ok {
		return scimNotFound, nil
	}
	var deleted store.IdentityAccessGroup
	err = s.scimTx(ctx, func(q *store.Queries, now time.Time) error {
		g, err := lockScimGroup(ctx, q, id)
		if err != nil {
			return err
		}
		if err := scimPrecondition(req.Params.IfMatch, g.Version.String()); err != nil {
			return err
		}
		if v := req.Params.XSCIMMetaVersion; v != nil && *v != g.Version.String() {
			return scimStaleVersion
		}
		before, err := scimGroupFactsOf(ctx, q, g)
		if err != nil {
			return err
		}
		next := g
		next.IsActive, next.Version, next.UpdatedAt = false, uuid.New(), now
		if err := q.UpdateScimGroup(ctx, store.UpdateScimGroupParams{
			DisplayName: next.DisplayName, IsActive: false, Version: next.Version, Now: now, ID: g.ID,
		}); err != nil {
			return err
		}
		if err := q.MarkScimGroupMembershipsAbsent(ctx, store.MarkScimGroupMembershipsAbsentParams{GroupID: g.ID, Keep: []uuid.UUID{}}); err != nil {
			return err
		}
		after, err := scimGroupFactsOf(ctx, q, next)
		if err != nil {
			return err
		}
		deleted = next
		return writeScimAudit(ctx, q, r, now, "scim.group.deleted", nil, before, after)
	})
	if err != nil {
		return scimOr[gen.DeleteIdentityScimV2GroupsByIdResponseObject](err)
	}
	return gen.DeleteIdentityScimV2GroupsById204Response{Headers: gen.DeleteIdentityScimV2GroupsById204ResponseHeaders{ETag: quotedETag(deleted.Version.String())}}, nil
}
