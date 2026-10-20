package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the groups (Entra design D9, plan task 3a): HR's groups,
// listed — HR names the members — or derived — a rule over the register
// yields them, materialised daily (groups_members.go, groups_rule.go) — each
// with a key the providers keep it by, set on create and never changed. A
// group is listed, added, read, replaced, archived and, while it never
// reached a provider, deleted; an archived one is read-only. Every operation
// is staff:manage's, which the router checks (x-vantigo-access), and GET
// /me/groups the caller's own. Like the structure's writes, a group's are
// not audited: the person events carry the membership and the provider's
// trail the directory.

// The refusal codes of the groups (delivery D), each a 409.
const (
	codeGroupSynced   = "group_synced"
	codeGroupDerived  = "group_derived"
	codeGroupArchived = "group_archived"
)

// invalidGroupTitle is the title of a group's validation problems.
const invalidGroupTitle = "Invalid group"

// The group's bounds (plan task 3a, exact values).
const (
	groupNameMaxLength        = 120
	groupDescriptionMaxLength = 500
)

// groupKeyPattern is a group's key: 1 to 60 lower-case letters, digits or
// hyphens, starting with a letter or a digit.
var groupKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)

// groupUniqueKeyIndex is the index a key another group holds trips.
const groupUniqueKeyIndex = "ux_groups_key"

// groupArchived is the 409 an archived group answers a write.
func groupArchived() gen.StaffProblem {
	return conflict(codeGroupArchived, "Group archived", "The group is archived and read-only.")
}

// parsedGroup is one validated group body. Rule is nil for a listed group.
type parsedGroup struct {
	Key         string
	Name        string
	Description *string
	Kind        string
	Rule        *gen.StaffGroupRule
}

// ruleNeedsID is whether a rule of kind names an id: a department, a
// business unit, a location or a person.
func ruleNeedsID(kind gen.StaffGroupRuleKind) bool {
	return kind == gen.Department || kind == gen.BusinessUnit || kind == gen.Location || kind == gen.ReportsTo
}

// parseGroup runs a group body's static rules. current is the group a
// change replaces, nil on a create: the key is required and checked on a
// create, and on a change left out or the group's own.
func parseGroup(body gen.StaffGroupRequest, current *store.StaffGroup) (parsedGroup, fieldErrors) {
	var f fieldErrors
	var out parsedGroup
	var msg string
	switch {
	case current != nil:
		out.Key = current.Key
		if body.Key != nil && *body.Key != current.Key {
			f.add("key", "The key cannot change")
		}
	case body.Key == nil || *body.Key == "":
		f.add("key", "A key is required")
	case !groupKeyPattern.MatchString(*body.Key):
		f.add("key", "A key is 1 to 60 lower-case letters, digits or hyphens, starting with a letter or a digit")
	default:
		out.Key = *body.Key
	}
	out.Name, msg = requiredText("A name", body.Name, groupNameMaxLength)
	f.add("name", msg)
	out.Description, msg = optionalText("A description", body.Description, groupDescriptionMaxLength)
	f.add("description", msg)
	switch body.Kind {
	case gen.Listed:
		if body.Rule != nil {
			f.add("rule", "A listed group has no rule; its members are named")
		}
	case gen.Derived:
		if body.Rule == nil {
			f.add("rule", "A derived group needs a rule")
			break
		}
		rule, rf := parseGroupRule(*body.Rule)
		for field, msgs := range rf.errs {
			for _, m := range msgs {
				f.add(field, m)
			}
		}
		out.Rule = &rule
	default:
		f.add("kind", "A kind is listed or derived")
	}
	out.Kind = string(body.Kind)
	return out, f
}

// parseGroupRule runs a rule's static rules and answers it as it is stored:
// an id only on the kinds that name one, a value only on employeeType,
// includeBelow only on department and only when true.
func parseGroupRule(in gen.StaffGroupRule) (gen.StaffGroupRule, fieldErrors) {
	var f fieldErrors
	out := gen.StaffGroupRule{Kind: in.Kind}
	switch in.Kind {
	case gen.Employed, gen.Department, gen.BusinessUnit, gen.Location, gen.EmployeeType, gen.ReportsTo:
	default:
		f.add("rule.kind", "A rule's kind is employed, department, businessUnit, location, employeeType or reportsTo")
		return out, f
	}
	switch {
	case ruleNeedsID(in.Kind) && in.Id == nil:
		f.add("rule.id", fmt.Sprintf("A %s rule names an id", in.Kind))
	case !ruleNeedsID(in.Kind) && in.Id != nil:
		f.add("rule.id", fmt.Sprintf("A %s rule names no id", in.Kind))
	default:
		out.Id = in.Id
	}
	switch {
	case in.Kind == gen.EmployeeType && (in.Value == nil || !slices.Contains(employeeTypes, *in.Value)):
		f.add("rule.value", "An employeeType rule names an employee type: employee, contractor, consultant or intern")
	case in.Kind != gen.EmployeeType && in.Value != nil:
		f.add("rule.value", fmt.Sprintf("A %s rule names no value", in.Kind))
	default:
		out.Value = in.Value
	}
	if in.IncludeBelow != nil && *in.IncludeBelow {
		if in.Kind != gen.Department {
			f.add("rule.includeBelow", "includeBelow is for a department rule alone")
		} else {
			out.IncludeBelow = in.IncludeBelow
		}
	}
	return out, f
}

// checkGroupRuleTarget is the rule's reference, judged in the caller's
// transaction: the department, business unit, location or person it names
// exists — archived or not.
func checkGroupRuleTarget(ctx context.Context, txq *store.Queries, rule *gen.StaffGroupRule) error {
	if rule == nil || rule.Id == nil {
		return nil
	}
	id := *rule.Id
	var exists bool
	var what string
	switch rule.Kind {
	case gen.Department:
		what = "department"
		rows, err := txq.DepartmentsByID(ctx, []int64{id})
		if err != nil {
			return fmt.Errorf("staff: read department %d: %w", id, err)
		}
		exists = len(rows) > 0
	case gen.BusinessUnit:
		what = "business unit"
		rows, err := txq.ListBusinessUnits(ctx)
		if err != nil {
			return fmt.Errorf("staff: read the business units: %w", err)
		}
		exists = slices.ContainsFunc(rows, func(r store.StaffBusinessUnit) bool { return r.ID == id })
	case gen.Location:
		what = "location"
		rows, err := txq.ListLocations(ctx)
		if err != nil {
			return fmt.Errorf("staff: read the locations: %w", err)
		}
		exists = slices.ContainsFunc(rows, func(r store.StaffLocation) bool { return r.ID == id })
	case gen.ReportsTo:
		what = "person"
		var err error
		exists, err = txq.PersonExists(ctx, id)
		if err != nil {
			return fmt.Errorf("staff: read person %d: %w", id, err)
		}
	}
	if !exists {
		return refuseInvalid(invalidGroupTitle, fieldErrors{errs: fieldError("rule.id", fmt.Sprintf("No %s has id %d", what, id))})
	}
	return nil
}

// encodeRule is a rule as the column stores it, nil for none.
func encodeRule(rule *gen.StaffGroupRule) ([]byte, error) {
	if rule == nil {
		return nil, nil
	}
	b, err := json.Marshal(rule)
	if err != nil {
		return nil, fmt.Errorf("staff: encode a group's rule: %w", err)
	}
	return b, nil
}

// groupKeyTaken is the field error a key another group holds answers.
func groupKeyTaken() error {
	return refuseInvalid(invalidGroupTitle, fieldErrors{errs: fieldError("key", "Another group already has this key")})
}

// groupResponse is a group as the contract answers it, with its providers'
// states; members and owners are set by the one group's answers alone.
func groupResponse(g store.GetGroupRow, providers []gen.StaffGroupProviderState) gen.StaffGroup {
	if providers == nil {
		providers = []gen.StaffGroupProviderState{}
	}
	out := gen.StaffGroup{
		Id: g.ID, Key: g.Key, Name: g.Name, Description: g.Description, Kind: gen.StaffGroupKind(g.Kind),
		ArchivedAt: g.ArchivedAt, MemberCount: g.MemberCount, OwnerCount: g.OwnerCount, Providers: providers,
		Revision: g.Revision, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
	if len(g.Rule) > 0 {
		var rule gen.StaffGroupRule
		if err := json.Unmarshal(g.Rule, &rule); err == nil {
			out.Rule = &rule
		}
	}
	return out
}

// readGroup is one group with its providers' states, members and owners,
// read on q; notFound for no such group.
func readGroup(ctx context.Context, q *store.Queries, id int64) (gen.StaffGroup, error) {
	g, err := q.GetGroup(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.StaffGroup{}, notFound()
	} else if err != nil {
		return gen.StaffGroup{}, fmt.Errorf("staff: read group %d: %w", id, err)
	}
	traits, err := q.ListGroupTraits(ctx, id)
	if err != nil {
		return gen.StaffGroup{}, fmt.Errorf("staff: read group %d's sections: %w", id, err)
	}
	providers := make([]gen.StaffGroupProviderState, 0, len(traits))
	for _, t := range traits {
		providers = append(providers, gen.StaffGroupProviderState{Key: t.Provider, Enabled: t.Enabled, State: gen.StaffGroupSyncState(t.State)})
	}
	memberRows, err := q.ListGroupMembers(ctx, id)
	if err != nil {
		return gen.StaffGroup{}, fmt.Errorf("staff: read group %d's members: %w", id, err)
	}
	ownerRows, err := q.ListGroupOwners(ctx, id)
	if err != nil {
		return gen.StaffGroup{}, fmt.Errorf("staff: read group %d's owners: %w", id, err)
	}
	members := mapRows(memberRows, func(r store.ListGroupMembersRow) gen.StaffGroupMember {
		return gen.StaffGroupMember{
			PersonId: r.PersonID, DisplayName: r.DisplayName, EmployeeNumber: r.EmployeeNumber,
			Source: gen.StaffGroupKind(r.Source), Since: openapi_types.Date{Time: civil(r.Since)},
		}
	})
	owners := mapRows(ownerRows, func(r store.ListGroupOwnersRow) gen.StaffGroupOwner {
		return gen.StaffGroupOwner{PersonId: r.PersonID, DisplayName: r.DisplayName, EmployeeNumber: r.EmployeeNumber}
	})
	out := groupResponse(g, providers)
	out.Members, out.Owners = &members, &owners
	return out, nil
}

// GetStaffGroups List groups
// (GET /api/v1/staff/groups)
func (s *server) GetStaffGroups(ctx context.Context, _ gen.GetStaffGroupsRequestObject) (gen.GetStaffGroupsResponseObject, error) {
	var (
		rows   []store.ListGroupsRow
		states []store.ListGroupTraitStatesRow
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if rows, err = q.ListGroups(ctx); err != nil {
			return fmt.Errorf("staff: list the groups: %w", err)
		}
		if states, err = q.ListGroupTraitStates(ctx); err != nil {
			return fmt.Errorf("staff: list the groups' sections: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	byGroup := map[int64][]gen.StaffGroupProviderState{}
	for _, t := range states {
		byGroup[t.GroupID] = append(byGroup[t.GroupID], gen.StaffGroupProviderState{Key: t.Provider, Enabled: t.Enabled, State: gen.StaffGroupSyncState(t.State)})
	}
	out := make([]gen.StaffGroup, 0, len(rows))
	for _, r := range rows {
		out = append(out, groupResponse(store.GetGroupRow(r), byGroup[r.ID]))
	}
	return gen.GetStaffGroups200JSONResponse(out), nil
}

// PostStaffGroups Add a group
// (POST /api/v1/staff/groups)
func (s *server) PostStaffGroups(ctx context.Context, req gen.PostStaffGroupsRequestObject) (gen.PostStaffGroupsResponseObject, error) {
	var body gen.StaffGroupRequest
	if req.Body != nil {
		body = *req.Body
	}
	parsed, f := parseGroup(body, nil)
	if f.failed() {
		return gen.PostStaffGroups400ApplicationProblemPlusJSONResponse(invalid(invalidGroupTitle, f)), nil
	}
	rule, err := encodeRule(parsed.Rule)
	if err != nil {
		return nil, err
	}
	var out gen.StaffGroup
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := checkGroupRuleTarget(ctx, txq, parsed.Rule); err != nil {
			return err
		}
		if _, err := txq.GetGroupByKey(ctx, parsed.Key); err == nil {
			return groupKeyTaken()
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("staff: read the group with key %q: %w", parsed.Key, err)
		}
		created, err := txq.InsertGroup(ctx, store.InsertGroupParams{
			Key: parsed.Key, Name: parsed.Name, Description: parsed.Description, Kind: parsed.Kind, Rule: rule, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, groupUniqueKeyIndex) {
			return groupKeyTaken()
		} else if err != nil {
			return fmt.Errorf("staff: insert a group: %w", err)
		}
		out, err = readGroup(ctx, txq, created.ID)
		return err
	})
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffGroups400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: add a group: %w", err)
	}
	return gen.PostStaffGroups201JSONResponse(out), nil
}

// GetStaffGroupsById Get a group
// (GET /api/v1/staff/groups/{id})
func (s *server) GetStaffGroupsById(ctx context.Context, req gen.GetStaffGroupsByIdRequestObject) (gen.GetStaffGroupsByIdResponseObject, error) {
	var out gen.StaffGroup
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		out, err = readGroup(ctx, q, req.Id)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffGroupsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffGroupsById200JSONResponse(out), nil
}

// PutStaffGroupsById Change a group
// (PUT /api/v1/staff/groups/{id})
//
// A change of the name or the description makes every provider's section
// due, so the worker carries it at its next cycle; a change of the rule
// reaches the members at the next materialisation.
func (s *server) PutStaffGroupsById(ctx context.Context, req gen.PutStaffGroupsByIdRequestObject) (gen.PutStaffGroupsByIdResponseObject, error) {
	var body gen.StaffGroupRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffGroupsById400ApplicationProblemPlusJSONResponse(invalid(invalidGroupTitle, badIfMatch())), nil
	}
	var out gen.StaffGroup
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		g, err := lockWritableGroup(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if err := checkRevision("group", g.Revision, revision); err != nil {
			return err
		}
		parsed, f := parseGroup(body, &g)
		if f.failed() {
			return refuseInvalid(invalidGroupTitle, f)
		}
		if err := checkGroupRuleTarget(ctx, txq, parsed.Rule); err != nil {
			return err
		}
		rule, err := encodeRule(parsed.Rule)
		if err != nil {
			return err
		}
		saved, err := txq.UpdateGroup(ctx, store.UpdateGroupParams{
			ID: g.ID, Name: parsed.Name, Description: parsed.Description, Kind: parsed.Kind, Rule: rule, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: update group %d: %w", g.ID, err)
		}
		if saved.Name != g.Name || !sameText(saved.Description, g.Description) {
			if err := txq.MakeGroupTraitsDue(ctx, store.MakeGroupTraitsDueParams{GroupID: g.ID, Now: s.deps.Clock()}); err != nil {
				return fmt.Errorf("staff: make group %d's sections due: %w", g.ID, err)
			}
		}
		out, err = readGroup(ctx, txq, g.ID)
		return err
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffGroupsById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffGroupsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffGroupsById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: change group %d: %w", req.Id, err)
	}
	return gen.PutStaffGroupsById200JSONResponse(out), nil
}

// sameText reports whether two optional texts are the same.
func sameText(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// PostStaffGroupsByIdArchive Archive a group
// (POST /api/v1/staff/groups/{id}/archive)
//
// The first archive stamps the group and its archiver and makes every
// provider's section due, so the worker retires it; archiving it again
// changes nothing.
func (s *server) PostStaffGroupsByIdArchive(ctx context.Context, req gen.PostStaffGroupsByIdArchiveRequestObject) (gen.PostStaffGroupsByIdArchiveResponseObject, error) {
	by := callerID(ctx)
	var out gen.StaffGroup
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		g, err := txq.LockGroup(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock group %d: %w", req.Id, err)
		}
		if g.ArchivedAt == nil {
			params := store.ArchiveGroupParams{ID: g.ID, Now: s.deps.Clock()}
			if by != uuid.Nil {
				params.ArchivedByUserID = &by
			}
			if _, err := txq.ArchiveGroup(ctx, params); err != nil {
				return fmt.Errorf("staff: archive group %d: %w", g.ID, err)
			}
			if err := txq.MakeGroupTraitsDue(ctx, store.MakeGroupTraitsDueParams{GroupID: g.ID, Now: s.deps.Clock()}); err != nil {
				return fmt.Errorf("staff: make group %d's sections due: %w", g.ID, err)
			}
		}
		out, err = readGroup(ctx, txq, g.ID)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.PostStaffGroupsByIdArchive404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: archive group %d: %w", req.Id, err)
	}
	return gen.PostStaffGroupsByIdArchive200JSONResponse(out), nil
}

// DeleteStaffGroupsById Delete a group
// (DELETE /api/v1/staff/groups/{id})
//
// Only a group no provider ever made or found: one that did is group_synced,
// archived instead. Its members leave it, each a group_left event by the
// caller, so a person's history does not keep a join with no end.
func (s *server) DeleteStaffGroupsById(ctx context.Context, req gen.DeleteStaffGroupsByIdRequestObject) (gen.DeleteStaffGroupsByIdResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := advisoryLock(ctx, txq, groupLock(req.Id)); err != nil {
			return err
		}
		g, err := txq.LockGroup(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock group %d: %w", req.Id, err)
		}
		synced, err := txq.CountGroupTraitsSynced(ctx, g.ID)
		if err != nil {
			return fmt.Errorf("staff: count group %d's synced sections: %w", g.ID, err)
		}
		if synced > 0 {
			return refuseConflict(conflict(codeGroupSynced, "Group in a provider's directory",
				"A provider has made or found the group in its directory, so it is not deleted; archive it instead."))
		}
		members, err := txq.ListGroupMembers(ctx, g.ID)
		if err != nil {
			return fmt.Errorf("staff: read group %d's members: %w", g.ID, err)
		}
		for _, m := range members {
			id := m.PersonID
			payload := groupEvent{GroupID: g.ID, GroupName: g.Name, GroupKey: g.Key, Source: m.Source}
			if err := s.recordEvent(ctx, txq, &id, eventGroupLeft, who, payload, groupEventClasses); err != nil {
				return err
			}
		}
		if _, err := txq.DeleteGroup(ctx, g.ID); err != nil {
			return fmt.Errorf("staff: delete group %d: %w", g.ID, err)
		}
		return nil
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.DeleteStaffGroupsById404Response{}, nil
		}
		return gen.DeleteStaffGroupsById409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: delete group %d: %w", req.Id, err)
	}
	return gen.DeleteStaffGroupsById204Response{}, nil
}

// personGroups is the groups a person is in, archived ones aside, as the
// Account tab answers them (StaffPersonAccount.groups).
func personGroups(ctx context.Context, q *store.Queries, personID int64) ([]gen.StaffPersonGroup, error) {
	rows, err := q.GroupsOfPerson(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's groups: %w", personID, err)
	}
	return mapRows(rows, func(r store.GroupsOfPersonRow) gen.StaffPersonGroup {
		key, source := r.Key, gen.StaffGroupKind(r.Source)
		since := openapi_types.Date{Time: civil(r.Since)}
		return gen.StaffPersonGroup{Id: r.ID, Name: r.Name, Key: &key, Source: &source, Since: &since}
	}), nil
}

// GetStaffMeGroups List my own groups
// (GET /api/v1/staff/me/groups)
func (s *server) GetStaffMeGroups(ctx context.Context, _ gen.GetStaffMeGroupsRequestObject) (gen.GetStaffMeGroupsResponseObject, error) {
	q := store.New(s.deps.Pool)
	caller, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMeGroups404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	rows, err := q.GroupNamesOfPerson(ctx, *caller.Person)
	if err != nil {
		return nil, fmt.Errorf("staff: read the caller's groups: %w", err)
	}
	return gen.GetStaffMeGroups200JSONResponse(mapRows(rows, func(r store.GroupNamesOfPersonRow) gen.StaffPersonGroup {
		return gen.StaffPersonGroup{Id: r.ID, Name: r.Name}
	})), nil
}
