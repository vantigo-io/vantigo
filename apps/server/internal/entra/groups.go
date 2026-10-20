package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the groups in the tenant (Entra design D14, research §3.3,
// plan task 4): Staff's groups made, adopted and kept in step by
// EnsureGroup, their members reconciled by SetMembers, and DeleteGroup's soft
// delete. Every group the module makes names the application's own service
// principal as an owner — a group made app-only without one cannot be
// modified — and the service principal is never removed. A member or an
// owner is only ever removed by its reference (/$ref): without it Graph
// deletes the user itself. As the user lifecycle does, every write checks the
// actor, takes its footing with open, makes its Graph calls on no
// transaction and records its row and its event after them; an event names
// the group by its object id, never a user. A group the module found and
// adopted is untrusted until a holder of entra:manage confirms it, as an
// adopted account is (lifecycle_user.go's trusted): until then nothing is
// written to it.

// The groups' event actions (the plan's "Exact values").
const (
	actionGroupCreated     = "entra.group.created"
	actionGroupAdopted     = "entra.group.adopted"
	actionGroupUpdated     = "entra.group.updated"
	actionGroupMembersSet  = "entra.group.members_set"
	actionGroupTeamCreated = "entra.group.team_created"
	actionGroupDeleted     = "entra.group.deleted"
	actionGroupRetired     = "entra.group.retired"
	// actionGroupAdoptionConfirmed audits POST /groups/{objectId}/confirm.
	actionGroupAdoptionConfirmed = "entra.group.adoption_confirmed"
)

// The states entra.groups.state names.
const (
	groupStatePresent = "present"
	groupStateDeleted = "deleted"
	// groupStateRetired is a security group the module made and Staff let
	// go: emptied of its people and kept, never deleted (Graph deletes a
	// security group for good).
	groupStateRetired = "retired"
)

const (
	// refusedNotManageable is the refusal code of a group the module may not
	// write: dynamic, role-assignable, mail-enabled by Exchange alone, or
	// another Staff group's.
	refusedNotManageable = "group_not_manageable"
	// refusedGroupUnconfirmed is the refusal code of a write to a group the
	// module adopted and nobody has confirmed.
	refusedGroupUnconfirmed = "adoption_unconfirmed"
	// codeKeptNotOurs is DeleteGroup's code for a group the module did not
	// make: the tenant keeps it, the row is marked deleted.
	codeKeptNotOurs = "kept_not_ours"
	// codeSecurityKept is the retired event's code for a security group
	// emptied and kept rather than deleted.
	codeSecurityKept = "security_kept"
	// groupArchivedSuffix is what a retired security group's name ends
	// with, once.
	groupArchivedSuffix = " (archived)"
	// codeTeamPending is the event code of a team not made yet: the group is
	// too young, or Teams has not seen it.
	codeTeamPending = "team_pending"
	// maxGroupRef is the longest Staff group id and key entra.groups keeps.
	maxGroupRef = 60
	// createRelationships is how many owners a group's create may name, the
	// service principal among them; the rest are added by $ref after.
	createRelationships = 20
	// membersChunk is how many members one PATCH adds and one $batch
	// removes.
	membersChunk = 20
	// membersPageSize is the members list's page, and maxMembersPages how
	// many pages SetMembers reads at most.
	membersPageSize = 999
	maxMembersPages = 100
	// maxOwnersPages bounds the owners list's pages.
	maxOwnersPages = 10
	// teamMinAge is how old a group must be before a team is made from it,
	// teamTries how many times a 404 is tried, teamRetryDelay apart.
	teamMinAge     = 15 * time.Minute
	teamTries      = 3
	teamRetryDelay = 10 * time.Second
	// teamTemplate is the template a team is made from a group with.
	teamTemplate = "https://graph.microsoft.com/v1.0/teamsTemplates('standard')"
	// groupSelect is what the module reads of a group.
	groupSelect = "id,displayName,description,mailNickname,visibility,groupTypes,mailEnabled,securityEnabled," +
		"membershipRule,isAssignableToRole,createdDateTime"
	// odataUser is a directory object that is a user.
	odataUser = "#microsoft.graph.user"
)

// errNoSuchGroup: the id is not a group the tenant holds.
var errNoSuchGroup = errors.New("entra: the tenant holds no such group")

// groupPath is /groups/{id}, and more after it.
func groupPath(id uuid.UUID, rest ...string) string {
	return "/groups/" + id.String() + strings.Join(rest, "")
}

// membersRefPath is the path that removes user from group's members: the
// reference, ending in the literal /$ref — never the member itself, which
// Graph would delete (research §3.3's trap). Both are parsed ids, so
// nothing else can reach the path.
func membersRefPath(group, user uuid.UUID) string {
	return groupPath(group, "/members/", user.String(), "/$ref")
}

// ownersRefPath is the path that removes owner from group's owners, by its
// reference.
func ownersRefPath(group, owner uuid.UUID) string {
	return groupPath(group, "/owners/", owner.String(), "/$ref")
}

// userRef and servicePrincipalRef are the references a group's owners and
// members are bound by.
func userRef(id uuid.UUID) string { return graphAPIBase + userPath(id) }

func servicePrincipalRef(id uuid.UUID) string {
	return graphAPIBase + "/servicePrincipals/" + id.String()
}

// manageable reports whether the module may write g: not dynamic, not
// role-assignable, and not a list or a mail-enabled security group that
// Exchange keeps.
func (g graphGroup) manageable() bool {
	role := g.IsAssignableToRole != nil && *g.IsAssignableToRole
	exchange := !slices.Contains(g.GroupTypes, "Unified") && g.MailEnabled != nil && *g.MailEnabled
	return !g.dynamic() && !role && !exchange
}

// dynamic reports whether g's members are the directory's rule.
func (g graphGroup) dynamic() bool {
	return (g.MembershipRule != nil && *g.MembershipRule != "") || slices.Contains(g.GroupTypes, "DynamicMembership")
}

// kind is g's type as the type trait names it.
func (g graphGroup) kind() string {
	if slices.Contains(g.GroupTypes, "Unified") {
		return groupTypeMicrosoft365
	}
	return groupTypeSecurity
}

// groupWant is what a GroupSpec asks of the tenant: Staff's id and key, the
// name and the description bounded as Graph keeps them, the trait values,
// the mail nickname to make the group with, and the person owners.
type groupWant struct {
	groupID, key, name, description string
	values                          groupValues
	nickname                        string
	owners                          []uuid.UUID
}

// wantGroup reads spec. A spec without a group id or a key is a fault, a
// plain error; a value the traits refuse is a *contracts.GroupTraitError
// (parseGroupValues), answered with the rest read.
func wantGroup(spec contracts.GroupSpec) (groupWant, error) {
	w := groupWant{groupID: strings.TrimSpace(spec.GroupID), key: strings.TrimSpace(spec.Key)}
	if w.groupID == "" || len(w.groupID) > maxGroupRef || w.key == "" || len(w.key) > maxGroupRef {
		return w, errors.New("entra: a group spec needs a group id and a key of at most 60 characters")
	}
	w.name = cut(strings.TrimSpace(spec.Name), maxGroupDisplayName)
	if w.name == "" {
		w.name = w.key
	}
	for _, raw := range spec.OwnerExternalIDs {
		if id, ok := objectID(raw); ok && !slices.Contains(w.owners, id) {
			w.owners = append(w.owners, id)
		}
	}
	values, err := parseGroupValues(spec.Values)
	w.values = values
	w.description = values.description
	if w.description == "" {
		w.description = cut(strings.TrimSpace(spec.Description), maxGroupDescription)
	}
	w.nickname = values.nickname
	if w.nickname == "" {
		w.nickname = mailNicknameOf(w.key)
	}
	if err == nil && w.nickname == "" {
		err = traitError(traitMailNickname, contracts.TraitInvalid)
	}
	return w, err
}

// groupRow is the row of Staff's group id and whether it names a group
// present in the tenant; a deleted one is answered, not present.
func groupRow(ctx context.Context, q *store.Queries, groupID string) (store.EntraGroup, bool, error) {
	row, err := q.GetGroupByGroupID(ctx, groupID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.EntraGroup{}, false, nil
	case err != nil:
		return store.EntraGroup{}, false, fmt.Errorf("entra: read the group: %w", err)
	}
	return row, row.State == groupStatePresent, nil
}

// trustedGroup reports whether the module may write to row's group: one it
// made, or an adoption a holder of entra:manage confirmed.
func trustedGroup(row store.EntraGroup) bool {
	return row.CreatedByUs || row.AdoptionConfirmedAt != nil
}

// servicePrincipal is the application's service principal in the tenant:
// the oid claim of the token s was opened with, written to the connection
// when the row lacks it or names another (plan reading 2); the row's when
// the token named none.
func (p *provisioner) servicePrincipal(ctx context.Context, s session) (uuid.UUID, error) {
	if s.cred.oid != nil {
		if s.conn.ServicePrincipalID == nil || *s.conn.ServicePrincipalID != *s.cred.oid {
			if err := s.q.SetServicePrincipal(ctx, *s.cred.oid); err != nil {
				return uuid.Nil, fmt.Errorf("entra: record the service principal: %w", err)
			}
		}
		return *s.cred.oid, nil
	}
	if s.conn.ServicePrincipalID != nil {
		return *s.conn.ServicePrincipalID, nil
	}
	return uuid.Nil, errors.New("entra: the token names no service principal, which a group's owner must be")
}

// groupRefused records a refusal of a write to a group by actor — the
// event action with refused:<code> — and answers answer; a record that
// could not be written is the answer instead.
func (p *provisioner) groupRefused(ctx context.Context, s session, actor contracts.Actor, action string, objectID *uuid.UUID, code string, answer error) error {
	if err := recordEvent(ctx, s.q, p.client.now(), actor, action, objectID, "", []string{"refused:" + code}); err != nil {
		return err
	}
	return answer
}

// EnsureGroup makes or keeps the group of Staff's group in the tenant
// (contracts.IdentityProvisioner.EnsureGroup, design D14):
//
//   - The module's row for the group: the group read, its type and nickname
//     unchanged (trait_immutable otherwise), the name, the description and
//     the visibility that differ PATCHed, the owners reconciled — the
//     missing added, the people Staff no longer names removed, the service
//     principal never. A group gone from the tenant is made anew.
//   - No row: a group holding the mail nickname is adopted when the module
//     may write it — not dynamic, role-assignable, Exchange's or another
//     Staff group's, which is ErrGroupNotManageable — untrusted: its row is
//     recorded and nothing written to the tenant, ErrGroupUnconfirmed,
//     until a holder of entra:manage confirms it (POST
//     /groups/{objectId}/confirm); the call after the confirmation makes
//     the owners Staff's and the service principal (the group's own
//     removed) and writes Staff's name, description and visibility. None is created with the
//     service principal and the first nineteen owners, the rest by $ref.
//   - An adopted group nobody has confirmed: ErrGroupUnconfirmed, nothing
//     written and no event — the refusal was recorded once, on the
//     adoption, and Staff's worker calls at every poll.
//   - team: on a Microsoft 365 group fifteen minutes old with a person
//     among its owners, PUT /team, three tries ten seconds apart on a 404;
//     younger, or still 404, the answer's Pending names it; without a person
//     owner, trait_needs_owner.
//
// A trait value the declaration refuses is a *contracts.GroupTraitError,
// nothing written to the tenant.
func (p *provisioner) EnsureGroup(ctx context.Context, actor contracts.Actor, spec contracts.GroupSpec) (contracts.ExternalGroup, error) {
	if !actor.Valid() {
		return contracts.ExternalGroup{}, contracts.ErrInvalidActor
	}
	w, wantErr := wantGroup(spec)
	var traitErr *contracts.GroupTraitError
	if wantErr != nil && !errors.As(wantErr, &traitErr) {
		return contracts.ExternalGroup{}, wantErr
	}
	s, err := p.open(ctx, true)
	if err != nil {
		return contracts.ExternalGroup{}, err
	}
	row, present, err := groupRow(ctx, s.q, w.groupID)
	if err != nil {
		return contracts.ExternalGroup{}, err
	}
	if present && !trustedGroup(row) {
		return contracts.ExternalGroup{}, contracts.ErrGroupUnconfirmed
	}
	if traitErr != nil {
		action, id := actionGroupCreated, (*uuid.UUID)(nil)
		if present {
			action, id = actionGroupUpdated, &row.ObjectID
		}
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, action, id, traitErr.Code, wantErr)
	}
	sp, err := p.servicePrincipal(ctx, s)
	if err != nil {
		return contracts.ExternalGroup{}, err
	}
	if present {
		return p.updateGroup(ctx, s, actor, sp, row, w)
	}
	return p.placeGroup(ctx, s, actor, sp, w)
}

// updateGroup brings the group row names to w.
func (p *provisioner) updateGroup(ctx context.Context, s session, actor contracts.Actor, sp uuid.UUID, row store.EntraGroup, w groupWant) (contracts.ExternalGroup, error) {
	oid := row.ObjectID
	switch {
	case w.values.kind != row.Type:
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupUpdated, &oid, contracts.TraitImmutable,
			traitError(traitType, contracts.TraitImmutable))
	case w.values.nickname != "" && w.values.nickname != row.MailNickname:
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupUpdated, &oid, contracts.TraitImmutable,
			traitError(traitMailNickname, contracts.TraitImmutable))
	}
	g, err := p.getGroup(ctx, s, oid)
	switch {
	case errors.Is(err, errNoSuchGroup):
		// Deleted in the tenant by somebody else: the row says so, and the
		// group is made anew.
		if _, err := s.q.SetGroupState(ctx, store.SetGroupStateParams{State: groupStateDeleted, Now: p.client.now(), ObjectID: oid}); err != nil {
			return contracts.ExternalGroup{}, fmt.Errorf("entra: record the group gone: %w", err)
		}
		return p.placeGroup(ctx, s, actor, sp, w)
	case err != nil:
		return contracts.ExternalGroup{}, err
	case !g.manageable():
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupUpdated, &oid, refusedNotManageable, contracts.ErrGroupNotManageable)
	}
	w.nickname = row.MailNickname
	codes, persons, err := p.bringGroup(ctx, s, sp, oid, g, w, true)
	if err != nil {
		return contracts.ExternalGroup{}, p.settleAfterFailure(ctx, s, actor, actionGroupUpdated, w, oid, codes, err)
	}
	return p.finishGroup(ctx, s, actor, actionGroupUpdated, oid, createdAt(g, p.client.now()), row.TeamCreatedAt != nil, w, persons, codes)
}

// placeGroup is the group of a Staff group the module keeps no row for:
// the one holding the nickname adopted, or a new one.
func (p *provisioner) placeGroup(ctx context.Context, s session, actor contracts.Actor, sp uuid.UUID, w groupWant) (contracts.ExternalGroup, error) {
	hits, err := p.findGroups(ctx, s, w.nickname)
	if err != nil {
		return contracts.ExternalGroup{}, err
	}
	switch len(hits) {
	case 0:
		return p.createGroup(ctx, s, actor, sp, w)
	case 1:
		return p.adoptGroup(ctx, s, actor, sp, w, hits[0])
	}
	// Two groups hold the nickname (a security group's need not be unique):
	// the module cannot tell which is Staff's.
	return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupAdopted, nil, refusedNotManageable, contracts.ErrGroupNotManageable)
}

// adoptGroup takes hit, the one group holding the nickname, as the Staff
// group's: refused when the module may not write it or another Staff group
// holds it, trait_immutable when its type is not the one asked; otherwise
// recorded created_by_us false and answered ErrGroupUnconfirmed with nothing
// written to the tenant — the event adopted with refused:adoption_unconfirmed
// — until a holder of entra:manage confirms it. A group the module made
// itself and retired, found again, is its own still: the missing owners
// added (none removed) and Staff's name, description and visibility
// written.
func (p *provisioner) adoptGroup(ctx context.Context, s session, actor contracts.Actor, sp uuid.UUID, w groupWant, hit graphGroup) (contracts.ExternalGroup, error) {
	oid, ok := objectID(hit.ID)
	if !ok {
		return contracts.ExternalGroup{}, errors.New("entra: Graph answered a group without an id")
	}
	rows, err := s.q.ListGroups(ctx)
	if err != nil {
		return contracts.ExternalGroup{}, fmt.Errorf("entra: read the groups: %w", err)
	}
	switch {
	case groupTaken(rows, w, oid, true) || !hit.manageable():
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupAdopted, &oid, refusedNotManageable, contracts.ErrGroupNotManageable)
	case hit.kind() != w.values.kind:
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupAdopted, &oid, contracts.TraitImmutable,
			traitError(traitType, contracts.TraitImmutable))
	}
	row, err := p.insertGroup(ctx, s, w, oid, hit.kind(), false)
	if errors.Is(err, contracts.ErrGroupNotManageable) {
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupAdopted, &oid, refusedNotManageable, err)
	} else if err != nil {
		return contracts.ExternalGroup{}, err
	}
	if !trustedGroup(row) {
		if err := recordEvent(ctx, s.q, p.client.now(), actor, actionGroupAdopted, &oid, "",
			[]string{"type:" + hit.kind(), "refused:" + refusedGroupUnconfirmed}); err != nil {
			return contracts.ExternalGroup{}, err
		}
		return contracts.ExternalGroup{}, contracts.ErrGroupUnconfirmed
	}
	codes, persons, err := p.bringGroup(ctx, s, sp, oid, hit, w, false)
	codes = append([]string{"type:" + hit.kind()}, codes...)
	if err != nil {
		return contracts.ExternalGroup{}, p.settleAfterFailure(ctx, s, actor, actionGroupAdopted, w, oid, codes, err)
	}
	return p.finishGroup(ctx, s, actor, actionGroupAdopted, oid, createdAt(hit, p.client.now()), false, w, persons, codes)
}

// createGroup makes the group (research §3.3): the type's flags, Staff's
// name and description, the visibility, the nickname, and as owners the
// service principal and the first nineteen people — the rest added by $ref
// after. An owner the tenant does not hold refuses the whole create, which
// is then made with the service principal alone and the people added one by
// one. The create is made once (noRetry): when its answer is lost, the group
// it may have made all the same is read back by its nickname.
func (p *provisioner) createGroup(ctx context.Context, s session, actor contracts.Actor, sp uuid.UUID, w groupWant) (contracts.ExternalGroup, error) {
	body := map[string]any{"displayName": w.name, "mailNickname": w.nickname}
	if w.values.kind == groupTypeMicrosoft365 {
		body["mailEnabled"], body["securityEnabled"], body["groupTypes"] = true, true, []string{"Unified"}
		if w.values.visibility != "" {
			body["visibility"] = graphVisibility(w.values.visibility)
		}
	} else {
		body["mailEnabled"], body["securityEnabled"], body["groupTypes"] = false, true, []string{}
	}
	if w.description != "" {
		body["description"] = w.description
	}
	bound := w.owners[:min(len(w.owners), createRelationships-1)]
	create := func(people []uuid.UUID) (graphGroup, error) {
		refs := []string{servicePrincipalRef(sp)}
		for _, id := range people {
			refs = append(refs, userRef(id))
		}
		body["owners@odata.bind"] = refs
		var made graphGroup
		err := p.client.post(ctx, s.cred, "/groups", body, &made, noRetry)
		return made, err
	}
	made, err := create(bound)
	if (isStatus(err, http.StatusBadRequest) || isStatus(err, http.StatusNotFound)) && len(bound) > 0 {
		bound = nil
		made, err = create(nil)
	}
	switch {
	case err == nil:
	case errors.Is(err, errTenantUnreachable) && ctx.Err() == nil:
		hits, readErr := p.findGroups(ctx, s, w.nickname)
		if readErr != nil || len(hits) != 1 || !hits[0].manageable() || hits[0].kind() != w.values.kind {
			return contracts.ExternalGroup{}, err
		}
		made, bound = hits[0], nil
	default:
		return contracts.ExternalGroup{}, err
	}
	oid, ok := objectID(made.ID)
	if !ok {
		return contracts.ExternalGroup{}, errors.New("entra: Graph created a group without an id")
	}
	if _, err := p.insertGroup(ctx, s, w, oid, w.values.kind, true); err != nil {
		return contracts.ExternalGroup{}, err
	}
	codes := []string{"type:" + w.values.kind}
	persons := len(bound)
	if len(bound) < len(w.owners) {
		added, _, n, err := p.reconcileOwners(ctx, s, sp, oid, w.owners, false)
		if err != nil {
			return contracts.ExternalGroup{}, p.settleAfterFailure(ctx, s, actor, actionGroupCreated, w, oid, codes, err)
		}
		persons = n
		if added > 0 {
			codes = append(codes, "owners_added:"+strconv.Itoa(added))
		}
	}
	codes = append(codes, "owners:"+strconv.Itoa(persons))
	out, err := p.finishGroup(ctx, s, actor, actionGroupCreated, oid, createdAt(made, p.client.now()), false, w, persons, codes)
	out.Created = err == nil
	return out, err
}

// createdAt is when g was made, now when Graph did not say.
func createdAt(g graphGroup, now time.Time) time.Time {
	if g.CreatedDateTime != nil {
		return *g.CreatedDateTime
	}
	return now
}

// graphVisibility is a visibility as Graph spells it.
func graphVisibility(v string) string {
	if v == visibilityPublic {
		return "Public"
	}
	return "Private"
}

// bringGroup writes to g what w asks that differs — the name, the
// description, the visibility of a Microsoft 365 group — and reconciles its
// owners: the missing added, and when remove the people w does not name
// removed. It answers the event's codes, the person owners the group has
// after, and the codes of what it wrote before a failure.
func (p *provisioner) bringGroup(ctx context.Context, s session, sp, oid uuid.UUID, g graphGroup, w groupWant, remove bool) ([]string, int, error) {
	patch := map[string]any{}
	if g.DisplayName != w.name {
		patch["displayName"] = w.name
	}
	if deref(g.Description) != w.description {
		patch["description"] = nullable(w.description)
	}
	if w.values.kind == groupTypeMicrosoft365 && w.values.visibility != "" && !strings.EqualFold(deref(g.Visibility), w.values.visibility) {
		patch["visibility"] = graphVisibility(w.values.visibility)
	}
	var codes []string
	if len(patch) > 0 {
		if err := p.client.patch(ctx, s.cred, groupPath(oid), patch); err != nil {
			return nil, 0, err
		}
		for _, k := range []string{"displayName", "description", "visibility"} {
			if _, ok := patch[k]; ok {
				codes = append(codes, "changed:"+k)
			}
		}
	}
	added, removed, persons, err := p.reconcileOwners(ctx, s, sp, oid, w.owners, remove)
	if added > 0 {
		codes = append(codes, "owners_added:"+strconv.Itoa(added))
	}
	if removed > 0 {
		codes = append(codes, "owners_removed:"+strconv.Itoa(removed))
	}
	return codes, persons, err
}

// directoryObject is an owner or a member as Graph lists it.
type directoryObject struct {
	ODataType string `json:"@odata.type"`
	ID        string `json:"id"`
}

// reconcileOwners brings oid's owners to the service principal and people:
// the missing added by $ref — a person the tenant does not hold skipped —
// and, when remove, the users among the owners people does not name
// removed by their reference. The service principal is never removed, nor
// an owner that is not a user. It answers how many it added and removed,
// and how many users own the group after.
func (p *provisioner) reconcileOwners(ctx context.Context, s session, sp, oid uuid.UUID, people []uuid.UUID, remove bool) (added, removed, persons int, err error) {
	current, err := p.listDirectory(ctx, s, groupPath(oid, "/owners?", query(url.Values{"$select": {"id"}})), maxOwnersPages)
	if err != nil {
		return 0, 0, 0, err
	}
	users := map[uuid.UUID]bool{}
	hasSP := false
	for _, o := range current {
		id, ok := objectID(o.ID)
		switch {
		case !ok:
		case id == sp:
			hasSP = true
		case o.ODataType == odataUser:
			users[id] = true
		}
	}
	addRef := func(ref string) error {
		err := p.client.post(ctx, s.cred, groupPath(oid, "/owners/$ref"), map[string]string{"@odata.id": ref}, nil, noWords)
		if g, ok := asGraphError(err); ok && g.Status == http.StatusBadRequest && g.Exists {
			return nil
		}
		return err
	}
	if !hasSP {
		if err := addRef(servicePrincipalRef(sp)); err != nil {
			return added, removed, len(users), err
		}
		added++
	}
	for _, id := range people {
		if id == sp || users[id] {
			continue
		}
		switch err := addRef(userRef(id)); {
		case err == nil:
			users[id] = true
			added++
		case isStatus(err, http.StatusNotFound):
			// Not a user the tenant holds: it cannot own the group.
		default:
			return added, removed, len(users), err
		}
	}
	if remove {
		var extra []uuid.UUID
		for id := range users {
			if !slices.Contains(people, id) {
				extra = append(extra, id)
			}
		}
		slices.SortFunc(extra, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		paths := make([]string, len(extra))
		for i, id := range extra {
			paths[i] = ownersRefPath(oid, id)
		}
		statuses, err := p.batchDelete(ctx, s, paths)
		for i, st := range statuses {
			if st == http.StatusNoContent || st == http.StatusOK || st == http.StatusNotFound {
				delete(users, extra[i])
				removed++
			}
		}
		if err != nil {
			return added, removed, len(users), err
		}
	}
	return added, removed, len(users), nil
}

// finishGroup makes the team w asks for when it may, then records the row
// and the event of action with codes — and, when a team was made,
// team_created — and answers the group: Pending names the team not made yet.
// A team refused for want of a person owner is answered as the trait's
// error after the record; so is a failure of the team's call.
func (p *provisioner) finishGroup(ctx context.Context, s session, actor contracts.Actor, action string, oid uuid.UUID, created time.Time, hasTeam bool, w groupWant, persons int, codes []string) (contracts.ExternalGroup, error) {
	out := contracts.ExternalGroup{ExternalID: oid.String()}
	teamMade := false
	var teamErr error
	if w.values.team && !hasTeam && w.values.kind == groupTypeMicrosoft365 {
		switch {
		case p.client.now().Sub(created) < teamMinAge:
			out.Pending = []string{traitTeam}
			codes = append(codes, codeTeamPending)
		case persons == 0:
			teamErr = traitError(traitTeam, contracts.TraitNeedsOwner)
			codes = append(codes, "refused:"+contracts.TraitNeedsOwner)
		default:
			made, err := p.makeTeam(ctx, s, oid)
			switch {
			case err != nil:
				teamErr = err
			case made:
				teamMade = true
			default:
				out.Pending = []string{traitTeam}
				codes = append(codes, codeTeamPending)
			}
		}
	}
	if err := p.settleGroup(ctx, s, actor, action, w, oid, teamMade, codes); err != nil {
		return contracts.ExternalGroup{}, err
	}
	if teamErr != nil {
		return contracts.ExternalGroup{}, teamErr
	}
	return out, nil
}

// makeTeam makes a team from the group (research §3.3): PUT /team with the
// standard template, a 404 tried again teamRetryDelay later up to
// teamTries times, a 409 — a team made already — taken as made. It answers
// false when the last try was still 404: Teams has not seen the group yet.
func (p *provisioner) makeTeam(ctx context.Context, s session, oid uuid.UUID) (bool, error) {
	body := map[string]string{"template@odata.bind": teamTemplate}
	for try := 1; ; try++ {
		err := p.client.put(ctx, s.cred, groupPath(oid, "/team"), body)
		switch {
		case err == nil, isStatus(err, http.StatusConflict):
			return true, nil
		case !isStatus(err, http.StatusNotFound):
			return false, err
		case try >= teamTries:
			return false, nil
		}
		if err := p.client.pause(ctx, try, teamRetryDelay); err != nil {
			return false, err
		}
	}
}

// groupTaken reports whether rows hold oid, or — when adopting — a present
// group holding w's nickname, for another Staff group than w's.
func groupTaken(rows []store.EntraGroup, w groupWant, oid uuid.UUID, adopting bool) bool {
	return slices.ContainsFunc(rows, func(r store.EntraGroup) bool {
		return r.GroupID != w.groupID && (r.ObjectID == oid ||
			(adopting && r.State == groupStatePresent && strings.EqualFold(r.MailNickname, w.nickname)))
	})
}

// insertGroup records the group EnsureGroup made or adopted, at once, so a
// failure from here on finds it next time — under the connection's share
// lock, as a user's create is (recordCreated): a change of the tenant or the
// client since the session was opened records nothing and answers
// ErrProviderUnavailable. A deleted or retired group's row for the same
// Staff group gives way, unless it is this group's, found again. A group
// another Staff group's row holds — read again inside the transaction, so
// two Staff groups racing for one object both see it — answers
// ErrGroupNotManageable and records nothing. It answers the row.
func (p *provisioner) insertGroup(ctx context.Context, s session, w groupWant, oid uuid.UUID, kind string, createdByUs bool) (store.EntraGroup, error) {
	now := p.client.now()
	var row store.EntraGroup
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		conn, err := q.LockConnectionShare(ctx)
		if err != nil {
			return fmt.Errorf("entra: lock the connection: %w", err)
		}
		if conn.TenantID == nil || *conn.TenantID != s.cred.tenantID || conn.ClientID == nil || *conn.ClientID != s.cred.clientID {
			return errConnectionMoved
		}
		rows, err := q.ListGroups(ctx)
		if err != nil {
			return fmt.Errorf("entra: read the groups: %w", err)
		}
		if groupTaken(rows, w, oid, !createdByUs) {
			return contracts.ErrGroupNotManageable
		}
		if err := q.ForgetDeletedGroup(ctx, store.ForgetDeletedGroupParams{GroupID: w.groupID, ObjectID: oid}); err != nil {
			return fmt.Errorf("entra: forget the deleted group: %w", err)
		}
		row, err = q.InsertGroup(ctx, store.InsertGroupParams{
			ObjectID: oid, GroupID: w.groupID, Key: w.key, DisplayName: w.name, MailNickname: w.nickname,
			Type: kind, CreatedByUs: createdByUs, Now: now,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Another Staff group's row holds the object (InsertGroup's
			// WHERE): it is not re-pointed.
			return contracts.ErrGroupNotManageable
		case err != nil:
			return fmt.Errorf("entra: record the group: %w", err)
		}
		return nil
	})
	if errors.Is(err, errConnectionMoved) {
		p.client.logger.WarnContext(ctx, "entra: the connection changed while a group was made; the group is left unrecorded",
			"object_id", oid.String())
		return row, fmt.Errorf("%w: %w", contracts.ErrProviderUnavailable, err)
	}
	return row, err
}

// settleGroup records what a write left the group as — its key, name and
// nickname, the team when one was made — and the write's event with codes,
// and team_created after it, in one short transaction.
func (p *provisioner) settleGroup(ctx context.Context, s session, actor contracts.Actor, action string, w groupWant, oid uuid.UUID, teamMade bool, codes []string) error {
	now := p.client.now()
	return db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.UpdateGroup(ctx, store.UpdateGroupParams{
			Key: w.key, DisplayName: w.name, MailNickname: w.nickname, Now: now, ObjectID: oid,
		}); err != nil {
			return fmt.Errorf("entra: record the group: %w", err)
		}
		if teamMade {
			if err := q.SetGroupTeam(ctx, store.SetGroupTeamParams{Now: now, ObjectID: oid}); err != nil {
				return fmt.Errorf("entra: record the team: %w", err)
			}
		}
		if err := recordEvent(ctx, q, now, actor, action, &oid, "", codes); err != nil {
			return err
		}
		if teamMade {
			return recordEvent(ctx, q, now, actor, actionGroupTeamCreated, &oid, "", nil)
		}
		return nil
	})
}

// settleAfterFailure records the event of what a write did before it
// failed, when it did anything, and answers the failure.
func (p *provisioner) settleAfterFailure(ctx context.Context, s session, actor contracts.Actor, action string, w groupWant, oid uuid.UUID, codes []string, failure error) error {
	if len(codes) == 0 || ctx.Err() != nil {
		return failure
	}
	codes = append(codes, "failed")
	if err := recordEvent(ctx, s.q, p.client.now(), actor, action, &oid, "", codes); err != nil {
		return errors.Join(failure, err)
	}
	return failure
}

// getGroup reads the group oid; errNoSuchGroup (wrapped) on a 404.
func (p *provisioner) getGroup(ctx context.Context, s session, oid uuid.UUID) (graphGroup, error) {
	var g graphGroup
	err := p.client.get(ctx, s.cred, groupPath(oid, "?", query(url.Values{"$select": {groupSelect}})), &g)
	if isStatus(err, http.StatusNotFound) {
		return graphGroup{}, fmt.Errorf("%w: %w", errNoSuchGroup, err)
	}
	return g, err
}

// findGroups lists the groups holding nickname: the filter with the quote
// doubled, as OData escapes it, and the value encoded.
func (p *provisioner) findGroups(ctx context.Context, s session, nickname string) ([]graphGroup, error) {
	filter := "mailNickname eq '" + strings.ReplaceAll(nickname, "'", "''") + "'"
	var page struct {
		Value []graphGroup `json:"value"`
	}
	err := p.client.get(ctx, s.cred, "/groups?"+query(url.Values{"$filter": {filter}, "$select": {groupSelect}}), &page)
	return page.Value, err
}

// listDirectory reads every page of the directory objects path lists, at
// most maxPages of them; more is a failure, never a partial list.
func (p *provisioner) listDirectory(ctx context.Context, s session, path string, maxPages int) ([]directoryObject, error) {
	var out []directoryObject
	for page := 0; path != ""; page++ {
		if page == maxPages {
			return nil, fmt.Errorf("entra: a list longer than %d pages", maxPages)
		}
		var answer struct {
			Value    []directoryObject `json:"value"`
			NextLink string            `json:"@odata.nextLink"`
		}
		if err := p.client.get(ctx, s.cred, path, &answer); err != nil {
			return nil, err
		}
		out = append(out, answer.Value...)
		path = ""
		if answer.NextLink != "" {
			rest, ok := strings.CutPrefix(answer.NextLink, graphAPIBase)
			if !ok {
				return nil, fmt.Errorf("%w: a next page off Graph", errTenantUnreachable)
			}
			path = rest
		}
	}
	return out, nil
}

// batchRequest is one request of a $batch.
type batchRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	URL    string `json:"url"`
}

// batchDelete deletes paths in $batch requests of at most membersChunk and
// answers each one's status, in order — 0 for one the batch did not answer.
// The paths are the module's own (membersRefPath, ownersRefPath), so a user
// id never stands in a request line the client logs. On a failure it
// answers the statuses of the batches made before it.
func (p *provisioner) batchDelete(ctx context.Context, s session, paths []string) ([]int, error) {
	statuses := make([]int, 0, len(paths))
	for chunk := range slices.Chunk(paths, membersChunk) {
		requests := make([]batchRequest, len(chunk))
		for i, path := range chunk {
			requests[i] = batchRequest{ID: strconv.Itoa(i + 1), Method: http.MethodDelete, URL: path}
		}
		var answer struct {
			Responses []struct {
				ID     string `json:"id"`
				Status int    `json:"status"`
			} `json:"responses"`
		}
		if err := p.client.post(ctx, s.cred, "/$batch", map[string]any{"requests": requests}, &answer, noWords); err != nil {
			return statuses, err
		}
		got := map[string]int{}
		for _, r := range answer.Responses {
			got[r.ID] = r.Status
		}
		for i := range chunk {
			statuses = append(statuses, got[strconv.Itoa(i+1)])
		}
	}
	return statuses, nil
}

// SetMembers reconciles the members of Staff's group to the users
// memberExternalIDs name (contracts.IdentityProvisioner.SetMembers, design
// D14): the current members read in pages of 999, the missing added twenty
// to a PATCH — a refused PATCH tried again one by one by $ref, so the one
// member at fault is named —, the users the list does not name removed by
// their reference in $batch requests of twenty, each answer read on its
// own. A member that is not a user (a nested group, a device) is left
// alone. ErrGroupNotManageable for a group the module never made, or one
// that became role-assignable or Exchange's; ErrGroupUnconfirmed for an
// adopted group nobody has confirmed, nothing written and no event (the
// refusal was recorded once, on the adoption); a group that became
// dynamic changes nothing, each member to change failed dynamic. Every failure is
// a code with the member's id, never Graph's words, and no user id is
// logged.
func (p *provisioner) SetMembers(ctx context.Context, actor contracts.Actor, groupID string, memberExternalIDs []string) (contracts.MembersResult, error) {
	result := contracts.MembersResult{Failed: []contracts.MemberFailure{}}
	if !actor.Valid() {
		return result, contracts.ErrInvalidActor
	}
	s, err := p.open(ctx, true)
	if err != nil {
		return result, err
	}
	row, present, err := groupRow(ctx, s.q, strings.TrimSpace(groupID))
	if err != nil {
		return result, err
	}
	if !present {
		return result, contracts.ErrGroupNotManageable
	}
	oid := row.ObjectID
	if !trustedGroup(row) {
		return result, contracts.ErrGroupUnconfirmed
	}
	// wanted keeps the order the ids came in, each once; wantedSet answers
	// "is it wanted" at once, for groups of tens of thousands.
	var wanted []uuid.UUID
	wantedSet := map[uuid.UUID]struct{}{}
	for _, raw := range memberExternalIDs {
		id, ok := objectID(raw)
		if !ok {
			result.Failed = append(result.Failed, contracts.MemberFailure{ID: raw, Code: contracts.MemberNotFound})
			continue
		}
		if _, dup := wantedSet[id]; !dup {
			wantedSet[id] = struct{}{}
			wanted = append(wanted, id)
		}
	}
	g, err := p.getGroup(ctx, s, oid)
	switch {
	case errors.Is(err, errNoSuchGroup):
		if _, err := s.q.SetGroupState(ctx, store.SetGroupStateParams{State: groupStateDeleted, Now: p.client.now(), ObjectID: oid}); err != nil {
			return result, fmt.Errorf("entra: record the group gone: %w", err)
		}
		return result, contracts.ErrGroupNotManageable
	case err != nil:
		return result, err
	case g.dynamic():
	case !g.manageable():
		return result, p.groupRefused(ctx, s, actor, actionGroupMembersSet, &oid, refusedNotManageable, contracts.ErrGroupNotManageable)
	}
	current, err := p.listDirectory(ctx, s, groupPath(oid, "/members?", query(url.Values{
		"$select": {"id"}, "$top": {strconv.Itoa(membersPageSize)},
	})), maxMembersPages)
	if err != nil {
		return result, err
	}
	have := map[uuid.UUID]bool{}
	for _, m := range current {
		if id, ok := objectID(m.ID); ok && m.ODataType == odataUser {
			have[id] = true
		}
	}
	var add, remove []uuid.UUID
	for _, id := range wanted {
		if !have[id] {
			add = append(add, id)
		}
	}
	for id := range have {
		if _, ok := wantedSet[id]; !ok {
			remove = append(remove, id)
		}
	}
	slices.SortFunc(remove, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if g.dynamic() {
		for _, id := range slices.Concat(add, remove) {
			result.Failed = append(result.Failed, contracts.MemberFailure{ID: id.String(), Code: contracts.MemberDynamic})
		}
		return result, p.settleMembers(ctx, s, actor, oid, result, nil)
	}
	err = p.addMembers(ctx, s, oid, add, &result)
	if err == nil {
		err = p.removeMembers(ctx, s, oid, remove, &result)
	}
	return result, p.settleMembers(ctx, s, actor, oid, result, err)
}

// addMembers adds ids to oid's members, twenty to a PATCH. A PATCH refused
// (400 — one already a member, say — or 404) is tried again one by one by
// $ref: a member already there is no failure, one the tenant does not hold
// is not_found, another refusal refused or other. A failure to ask is the
// answer.
func (p *provisioner) addMembers(ctx context.Context, s session, oid uuid.UUID, ids []uuid.UUID, result *contracts.MembersResult) error {
	for chunk := range slices.Chunk(ids, membersChunk) {
		refs := make([]string, len(chunk))
		for i, id := range chunk {
			refs[i] = userRef(id)
		}
		err := p.client.patch(ctx, s.cred, groupPath(oid), map[string]any{"members@odata.bind": refs}, noWords)
		switch {
		case err == nil:
			result.Added += len(chunk)
			continue
		case !isStatus(err, http.StatusBadRequest) && !isStatus(err, http.StatusNotFound):
			return err
		}
		for _, id := range chunk {
			err := p.client.post(ctx, s.cred, groupPath(oid, "/members/$ref"), map[string]string{"@odata.id": userRef(id)}, nil, noWords)
			code := ""
			g, refused := asGraphError(err)
			switch {
			case err == nil:
				result.Added++
			case refused && g.Status == http.StatusBadRequest && g.Exists:
			case refused && g.Status == http.StatusNotFound:
				code = contracts.MemberNotFound
			case refused && (g.Status == http.StatusBadRequest || g.Status == http.StatusForbidden):
				code = contracts.MemberRefused
			case refused && g.Status < http.StatusInternalServerError:
				code = contracts.MemberOther
			default:
				return err
			}
			if code != "" {
				result.Failed = append(result.Failed, contracts.MemberFailure{ID: id.String(), Code: code})
			}
		}
	}
	return nil
}

// removeMembers removes ids from oid's members by their reference
// (membersRefPath) in $batch requests of twenty: 204 removed, 404 removed
// already, 400 or 403 refused, any other answer — a throttled one among
// them — other, for the next call to try again.
func (p *provisioner) removeMembers(ctx context.Context, s session, oid uuid.UUID, ids []uuid.UUID, result *contracts.MembersResult) error {
	paths := make([]string, len(ids))
	for i, id := range ids {
		paths[i] = membersRefPath(oid, id)
	}
	statuses, err := p.batchDelete(ctx, s, paths)
	for i, st := range statuses {
		switch st {
		case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
			result.Removed++
		case http.StatusBadRequest, http.StatusForbidden:
			result.Failed = append(result.Failed, contracts.MemberFailure{ID: ids[i].String(), Code: contracts.MemberRefused})
		default:
			result.Failed = append(result.Failed, contracts.MemberFailure{ID: ids[i].String(), Code: contracts.MemberOther})
		}
	}
	return err
}

// settleMembers records SetMembers' outcome — last_members_at when it
// finished, and the event with the counts whenever it changed or failed
// anything — and answers failure, the call's own error.
func (p *provisioner) settleMembers(ctx context.Context, s session, actor contracts.Actor, oid uuid.UUID, result contracts.MembersResult, failure error) error {
	if failure != nil && (ctx.Err() != nil || result.Added+result.Removed+len(result.Failed) == 0) {
		return failure
	}
	codes := []string{
		"added:" + strconv.Itoa(result.Added), "removed:" + strconv.Itoa(result.Removed), "failed:" + strconv.Itoa(len(result.Failed)),
	}
	if failure != nil {
		codes = append(codes, "failed")
	}
	if len(result.Failed) > 0 {
		p.client.logger.DebugContext(ctx, "entra: members the tenant did not change", "object_id", oid.String(), "count", len(result.Failed))
	}
	now := p.client.now()
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if failure == nil {
			if err := q.SetGroupMembersAt(ctx, store.SetGroupMembersAtParams{Now: now, ObjectID: oid}); err != nil {
				return fmt.Errorf("entra: record the members: %w", err)
			}
		}
		return recordEvent(ctx, q, now, actor, actionGroupMembersSet, &oid, "", codes)
	})
	return errors.Join(failure, err)
}

// DeleteGroup lets go of the group of Staff's group
// (contracts.IdentityProvisioner.DeleteGroup), its row kept:
//
//   - a group the module did not make — adopted, confirmed or not — is the
//     tenant's: no call, the row marked deleted, the event deleted with
//     kept_not_ours;
//   - a Microsoft 365 group the module made is soft-deleted, restorable
//     from the portal for thirty days, the row marked deleted;
//   - a security group the module made is never deleted — Graph deletes a
//     security group for good — but emptied: every user member and every
//     person owner removed by reference (the service principal stays),
//     renamed "… (archived)" once, and kept, the row marked retired with
//     the event retired, security_kept (retireGroup).
//
// A group never made, deleted or retired already answers nil with no call;
// one the tenant no longer holds is marked deleted. Staff renames a group
// it keeps whole through EnsureGroup (plan reading 5).
func (p *provisioner) DeleteGroup(ctx context.Context, actor contracts.Actor, groupID string) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	row, present, err := groupRow(ctx, store.New(p.deps.Pool), strings.TrimSpace(groupID))
	if err != nil || !present {
		return err
	}
	oid := row.ObjectID
	if !row.CreatedByUs {
		return p.settleGroupGone(ctx, store.New(p.deps.Pool), actor, oid, groupStateDeleted, actionGroupDeleted, []string{codeKeptNotOurs})
	}
	s, err := p.open(ctx, true)
	if err != nil {
		return err
	}
	if row.Type == groupTypeSecurity {
		return p.retireGroup(ctx, s, actor, oid)
	}
	var codes []string
	switch err := p.client.del(ctx, s.cred, groupPath(oid)); {
	case isStatus(err, http.StatusNotFound):
		codes = []string{"gone"}
	case err != nil:
		return err
	}
	return p.settleGroupGone(ctx, s.q, actor, oid, groupStateDeleted, actionGroupDeleted, codes)
}

// retireGroup empties and keeps a security group the module made
// (DeleteGroup): its user members and its person owners removed by their
// reference in $batch requests, the service principal left an owner, the
// name suffixed " (archived)" once; the row retired. A member or an owner
// the tenant would not remove fails the call, the row as it was, for the
// next call to try again; one the tenant no longer holds is marked deleted.
func (p *provisioner) retireGroup(ctx context.Context, s session, actor contracts.Actor, oid uuid.UUID) error {
	g, err := p.getGroup(ctx, s, oid)
	switch {
	case errors.Is(err, errNoSuchGroup):
		return p.settleGroupGone(ctx, s.q, actor, oid, groupStateDeleted, actionGroupDeleted, []string{"gone"})
	case err != nil:
		return err
	case !g.manageable():
		return p.groupRefused(ctx, s, actor, actionGroupRetired, &oid, refusedNotManageable, contracts.ErrGroupNotManageable)
	}
	sp, err := p.servicePrincipal(ctx, s)
	if err != nil {
		return err
	}
	current, err := p.listDirectory(ctx, s, groupPath(oid, "/members?", query(url.Values{
		"$select": {"id"}, "$top": {strconv.Itoa(membersPageSize)},
	})), maxMembersPages)
	if err != nil {
		return err
	}
	var users []uuid.UUID
	for _, m := range current {
		if id, ok := objectID(m.ID); ok && m.ODataType == odataUser {
			users = append(users, id)
		}
	}
	slices.SortFunc(users, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	var result contracts.MembersResult
	if err := p.removeMembers(ctx, s, oid, users, &result); err != nil {
		return err
	}
	if n := len(result.Failed); n > 0 {
		return fmt.Errorf("entra: %d of a retiring group's members could not be removed", n)
	}
	codes := []string{codeSecurityKept, "removed:" + strconv.Itoa(result.Removed)}
	_, removed, persons, err := p.reconcileOwners(ctx, s, sp, oid, nil, true)
	if err != nil {
		return err
	}
	if persons > 0 {
		return fmt.Errorf("entra: %d of a retiring group's owners could not be removed", persons)
	}
	codes = append(codes, "owners_removed:"+strconv.Itoa(removed))
	name := g.DisplayName
	if !strings.HasSuffix(name, groupArchivedSuffix) {
		name = cut(name, maxGroupDisplayName-len(groupArchivedSuffix)) + groupArchivedSuffix
		if err := p.client.patch(ctx, s.cred, groupPath(oid), map[string]any{"displayName": name}); err != nil {
			return err
		}
		codes = append(codes, "changed:displayName")
	}
	now := p.client.now()
	return db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.GetGroup(ctx, oid)
		if err != nil {
			return fmt.Errorf("entra: read the group: %w", err)
		}
		if _, err := q.UpdateGroup(ctx, store.UpdateGroupParams{
			Key: row.Key, DisplayName: name, MailNickname: row.MailNickname, Now: now, ObjectID: oid,
		}); err != nil {
			return fmt.Errorf("entra: record the group's name: %w", err)
		}
		if _, err := q.SetGroupState(ctx, store.SetGroupStateParams{State: groupStateRetired, Now: now, ObjectID: oid}); err != nil {
			return fmt.Errorf("entra: record the group retired: %w", err)
		}
		return recordEvent(ctx, q, now, actor, actionGroupRetired, &oid, "", codes)
	})
}

// settleGroupGone records oid's group as state with the event action and
// codes, in one transaction on q's pool.
func (p *provisioner) settleGroupGone(ctx context.Context, q *store.Queries, actor contracts.Actor, oid uuid.UUID, state, action string, codes []string) error {
	now := p.client.now()
	return db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := q.WithTx(tx)
		if _, err := q.SetGroupState(ctx, store.SetGroupStateParams{State: state, Now: now, ObjectID: oid}); err != nil {
			return fmt.Errorf("entra: record the group %s: %w", state, err)
		}
		return recordEvent(ctx, q, now, actor, action, &oid, "", codes)
	})
}
