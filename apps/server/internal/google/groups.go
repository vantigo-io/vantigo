package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the groups in the organisation (Google Workspace design
// G20): Staff's groups made, adopted and kept in step by EnsureGroup — the
// name, the description and the owners in the Directory with
// admin.directory.group, who sees the group in Groups Settings with
// apps.groups.settings —, their members reconciled by SetMembers, and
// DeleteGroup's delete. An owner is a member with the role OWNER: EnsureGroup
// keeps the owners, SetMembers the members, and neither touches the other's.
// A group the module found holding the address it would have made is
// untrusted until a holder of google:manage confirms it, as an adopted
// account is: nothing is written to it until then.

// The groups' event actions.
const (
	actionGroupCreated           = "google.group.created"
	actionGroupAdopted           = "google.group.adopted"
	actionGroupUpdated           = "google.group.updated"
	actionGroupMembersSet        = "google.group.members_set"
	actionGroupDeleted           = "google.group.deleted"
	actionGroupAdoptionConfirmed = "google.group.adoption_confirmed"
)

// The members' roles, and the member type SetMembers reconciles.
const (
	roleOwner  = "OWNER"
	roleMember = "MEMBER"
	typeUser   = "USER"
)

// The groups' codes.
const (
	// refusedNotManageable: the group's address is held by a group another
	// Staff group's row names, or by something that is not a group.
	refusedNotManageable = "group_not_manageable"
	// codeKeptNotOurs is DeleteGroup's code for a group the module did not
	// make: the organisation keeps it, the row is forgotten.
	codeKeptNotOurs = "kept_not_ours"
)

// The members' bounds.
const (
	maxMembersPages = 100
	membersPageSize = "200"
	maxGroupRef     = 64
)

// groupIDPattern is a Google group id.
var groupIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)

// groupURL is a group's URL in the Directory, by its id or address, with
// rest after it.
func groupURL(key string, rest ...string) string {
	return directoryBase + "/groups/" + url.PathEscape(key) + strings.Join(rest, "")
}

// directoryGroup is a group as the Directory answers it.
type directoryGroup struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// directoryMember is one member as members.list answers it.
type directoryMember struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Type string `json:"type"`
}

// groupWant is what EnsureGroup makes a group look like: Staff's group id,
// the address, the name and the description, the trait values and the
// owners' ids.
type groupWant struct {
	groupID, email, name, description string
	values                            groupValues
	owners                            []string
}

// wantGroup reads spec against the connection: the address the email trait
// names, else the group's key at the address domain. A value the traits
// refuse is a *contracts.GroupTraitError.
func wantGroup(spec contracts.GroupSpec, conn store.GoogleConnection) (groupWant, error) {
	w := groupWant{groupID: strings.TrimSpace(spec.GroupID)}
	if w.groupID == "" || len(w.groupID) > maxGroupRef {
		return w, errors.New("google: a group spec without a usable group id")
	}
	values, err := parseGroupValues(spec.Values, conn.Domains)
	if err != nil {
		return w, err
	}
	w.values = values
	w.email = values.email
	if w.email == "" {
		local, domain := groupLocal(spec.Key), addressDomain(conn)
		if local == "" || domain == "" {
			return w, traitError(traitEmail, contracts.TraitRequired)
		}
		w.email = local + "@" + domain
	}
	w.name = cut(strings.TrimSpace(spec.Name), maxGroupName)
	if w.name == "" {
		w.name = cut(strings.TrimSpace(spec.Key), maxGroupName)
	}
	w.description = cut(strings.TrimSpace(spec.Description), maxGroupDescription)
	for _, o := range spec.OwnerExternalIDs {
		if o = strings.TrimSpace(o); validUserID(o) && !slices.Contains(w.owners, o) {
			w.owners = append(w.owners, o)
		}
	}
	return w, nil
}

// trustedGroup reports whether the module may write to row's group: one it
// made, or one it adopted whose adoption a holder of google:manage
// confirmed.
func trustedGroup(row store.GoogleGroup) bool {
	return row.CreatedByUs || row.AdoptionConfirmedAt != nil
}

// groupRow is the row of Staff's group, false when the module keeps none.
func groupRow(ctx context.Context, q *store.Queries, staffGroupID string) (store.GoogleGroup, bool, error) {
	row, err := q.GetGroupByStaffID(ctx, staffGroupID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return store.GoogleGroup{}, false, nil
	case err != nil:
		return store.GoogleGroup{}, false, fmt.Errorf("google: read the group: %w", err)
	}
	return row, true, nil
}

// groupRefused records a refusal of a group write — the event with the
// code — and answers answer.
func (p *provisioner) groupRefused(ctx context.Context, s session, actor contracts.Actor, action, groupID, code string, answer error) error {
	if err := recordEvent(ctx, s.q, p.now(), actor, action, groupID, []string{"refused:" + code}); err != nil {
		return err
	}
	return answer
}

// EnsureGroup creates or updates the Google group of Staff's group
// (contracts.IdentityProvisioner.EnsureGroup, G20):
//
//   - a group the module keeps: the address the traits name must be the one
//     it was made with (trait_immutable otherwise); the name and the
//     description written when they differ; the visibility; the owners.
//     One Google no longer holds is forgotten and placed again.
//   - none kept: the group holding the address is adopted, untrusted —
//     ErrGroupUnconfirmed, nothing written — until a holder of google:manage
//     confirms it; with none, groups.insert, then the visibility and the
//     owners.
//
// A trait value the declaration refuses is a *contracts.GroupTraitError,
// nothing written to Google.
func (p *provisioner) EnsureGroup(ctx context.Context, actor contracts.Actor, spec contracts.GroupSpec) (contracts.ExternalGroup, error) {
	if !actor.Valid() {
		return contracts.ExternalGroup{}, contracts.ErrInvalidActor
	}
	s, err := p.open(ctx, true)
	if err != nil {
		return contracts.ExternalGroup{}, err
	}
	g, err := p.ensureGroup(ctx, s, actor, spec)
	return g, unavailable(err)
}

func (p *provisioner) ensureGroup(ctx context.Context, s session, actor contracts.Actor, spec contracts.GroupSpec) (contracts.ExternalGroup, error) {
	w, wantErr := wantGroup(spec, s.conn)
	var traitErr *contracts.GroupTraitError
	if wantErr != nil && !errors.As(wantErr, &traitErr) {
		return contracts.ExternalGroup{}, wantErr
	}
	row, present, err := groupRow(ctx, s.q, strings.TrimSpace(spec.GroupID))
	if err != nil {
		return contracts.ExternalGroup{}, err
	}
	if present && !trustedGroup(row) {
		return contracts.ExternalGroup{}, contracts.ErrGroupUnconfirmed
	}
	if traitErr != nil {
		action, id := actionGroupCreated, ""
		if present {
			action, id = actionGroupUpdated, row.GroupID
		}
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, action, id, traitErr.Code, wantErr)
	}
	if present {
		if _, named := spec.Values[traitEmail]; named && w.values.email != "" && !strings.EqualFold(w.values.email, row.Email) {
			return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupUpdated, row.GroupID, contracts.TraitImmutable,
				traitError(traitEmail, contracts.TraitImmutable))
		}
		var g directoryGroup
		err := p.keys.c.get(ctx, s.groups, groupURL(row.GroupID), &g)
		switch {
		case errors.Is(err, errNotFound):
			// Deleted at Google by somebody else: forgotten, and made anew.
			if err := s.q.DeleteGroup(ctx, row.GroupID); err != nil {
				return contracts.ExternalGroup{}, fmt.Errorf("google: forget the group gone: %w", err)
			}
			w.email = row.Email
			return p.placeGroup(ctx, s, actor, w)
		case err != nil:
			return contracts.ExternalGroup{}, err
		}
		w.email = row.Email
		codes, err := p.bringGroup(ctx, s, g, w)
		if err != nil {
			return contracts.ExternalGroup{}, err
		}
		return contracts.ExternalGroup{ExternalID: row.GroupID}, p.settleGroup(ctx, s, actor, actionGroupUpdated, row.GroupID, codes)
	}
	return p.placeGroup(ctx, s, actor, w)
}

// placeGroup is the group of a Staff group the module keeps no row for: the
// one holding the address adopted, or a new one.
func (p *provisioner) placeGroup(ctx context.Context, s session, actor contracts.Actor, w groupWant) (contracts.ExternalGroup, error) {
	var g directoryGroup
	err := p.keys.c.get(ctx, s.groups, groupURL(w.email), &g, shownAs("/admin/directory/v1/groups/{group}"))
	switch {
	case err == nil:
		return p.adoptGroup(ctx, s, actor, w, g)
	case !errors.Is(err, errNotFound):
		return contracts.ExternalGroup{}, err
	}
	var created directoryGroup
	err = p.keys.c.send(ctx, s.groups, http.MethodPost, directoryBase+"/groups",
		map[string]any{"email": w.email, "name": w.name, "description": w.description}, &created, noRetry())
	switch {
	case isStatus(err, http.StatusConflict):
		// The address is a user's or an alias's, or a group made meanwhile.
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupCreated, "", refusedNotManageable, contracts.ErrGroupNotManageable)
	case err != nil:
		return contracts.ExternalGroup{}, err
	case !groupIDPattern.MatchString(created.ID):
		return contracts.ExternalGroup{}, errors.New("google: the Directory created a group without an id")
	}
	if _, err := s.q.InsertGroup(ctx, store.InsertGroupParams{
		GroupID: created.ID, StaffGroupID: w.groupID, Email: strings.ToLower(created.Email), CreatedByUs: true, Now: p.now(),
	}); err != nil {
		return contracts.ExternalGroup{}, fmt.Errorf("google: record the group: %w", err)
	}
	if created.Email == "" {
		created.Email = w.email
	}
	codes, err := p.bringGroup(ctx, s, created, w)
	if err != nil {
		return contracts.ExternalGroup{}, err
	}
	return contracts.ExternalGroup{ExternalID: created.ID, Created: true}, p.settleGroup(ctx, s, actor, actionGroupCreated, created.ID, codes)
}

// adoptGroup takes g, the group holding the address, as the Staff group's:
// refused when another Staff group's row names it; otherwise recorded
// created_by_us false and answered ErrGroupUnconfirmed with nothing written
// to Google — the event adopted with refused:adoption_unconfirmed — until a
// holder of google:manage confirms it.
func (p *provisioner) adoptGroup(ctx context.Context, s session, actor contracts.Actor, w groupWant, g directoryGroup) (contracts.ExternalGroup, error) {
	if !groupIDPattern.MatchString(g.ID) {
		return contracts.ExternalGroup{}, errors.New("google: the Directory answered a group without an id")
	}
	switch other, err := s.q.GetGroup(ctx, g.ID); {
	case err == nil && other.StaffGroupID != w.groupID:
		return contracts.ExternalGroup{}, p.groupRefused(ctx, s, actor, actionGroupAdopted, g.ID, refusedNotManageable, contracts.ErrGroupNotManageable)
	case err == nil:
	case !errors.Is(err, pgx.ErrNoRows):
		return contracts.ExternalGroup{}, fmt.Errorf("google: read the group: %w", err)
	default:
		if _, err := s.q.InsertGroup(ctx, store.InsertGroupParams{
			GroupID: g.ID, StaffGroupID: w.groupID, Email: strings.ToLower(g.Email), CreatedByUs: false, Now: p.now(),
		}); err != nil {
			return contracts.ExternalGroup{}, fmt.Errorf("google: record the group: %w", err)
		}
	}
	if err := recordEvent(ctx, s.q, p.now(), actor, actionGroupAdopted, g.ID, []string{"refused:" + reportedAdoptionUnconfirmed}); err != nil {
		return contracts.ExternalGroup{}, err
	}
	return contracts.ExternalGroup{}, contracts.ErrGroupUnconfirmed
}

// bringGroup writes w to g: the name and the description when they differ,
// the visibility (Groups Settings), and the owners. It answers the event's
// codes.
func (p *provisioner) bringGroup(ctx context.Context, s session, g directoryGroup, w groupWant) ([]string, error) {
	var codes []string
	body := map[string]any{}
	if g.Name != w.name {
		body["name"] = w.name
	}
	if g.Description != w.description {
		body["description"] = w.description
	}
	if len(body) > 0 {
		if err := p.keys.c.send(ctx, s.groups, http.MethodPatch, groupURL(g.ID), body, nil); err != nil {
			return nil, err
		}
		codes = append(codes, "changed:name")
	}
	settings := map[string]any{
		"whoCanViewGroup":      "ALL_IN_DOMAIN_CAN_VIEW",
		"whoCanViewMembership": "ALL_IN_DOMAIN_CAN_VIEW",
		"showInGroupDirectory": "true",
	}
	if w.values.visibility == visibilityMembers {
		settings = map[string]any{
			"whoCanViewGroup":      "ALL_MEMBERS_CAN_VIEW",
			"whoCanViewMembership": "ALL_MEMBERS_CAN_VIEW",
			"showInGroupDirectory": "false",
		}
	}
	email := g.Email
	if email == "" {
		email = w.email
	}
	if err := p.keys.c.send(ctx, s.settings, http.MethodPatch, groupsSettingsBase+"/"+url.PathEscape(email)+"?alt=json", settings, nil,
		shownAs("/groups/v1/groups/{group}")); err != nil {
		return nil, err
	}
	codes = append(codes, "visibility:"+w.values.visibility)
	added, demoted, err := p.reconcileOwners(ctx, s, g.ID, w.owners)
	if err != nil {
		return nil, err
	}
	if added > 0 {
		codes = append(codes, "owners_added:"+strconv.Itoa(added))
	}
	if demoted > 0 {
		codes = append(codes, "owners_removed:"+strconv.Itoa(demoted))
	}
	return codes, nil
}

// listMembers reads the group's members, every page up to maxMembersPages.
func (p *provisioner) listMembers(ctx context.Context, s session, gid string) ([]directoryMember, error) {
	var out []directoryMember
	token := ""
	for range maxMembersPages {
		v := url.Values{"maxResults": {membersPageSize}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page struct {
			Members       []directoryMember `json:"members"`
			NextPageToken string            `json:"nextPageToken"`
		}
		if err := p.keys.c.get(ctx, s.groups, groupURL(gid, "/members?"+v.Encode()), &page); err != nil {
			return nil, err
		}
		out = append(out, page.Members...)
		if token = page.NextPageToken; token == "" {
			break
		}
	}
	return out, nil
}

// reconcileOwners makes owners exactly the users with the role OWNER: one
// missing added as an owner (or a member raised to it), one no longer named
// lowered to a member — whom SetMembers then keeps or removes. An owner
// Google does not know is skipped.
func (p *provisioner) reconcileOwners(ctx context.Context, s session, gid string, owners []string) (added, demoted int, err error) {
	members, err := p.listMembers(ctx, s, gid)
	if err != nil {
		return 0, 0, err
	}
	role := map[string]string{}
	for _, m := range members {
		if m.Type == typeUser || m.Type == "" {
			role[m.ID] = m.Role
		}
	}
	for _, o := range owners {
		switch role[o] {
		case roleOwner:
			continue
		case "":
			err = p.keys.c.send(ctx, s.groups, http.MethodPost, groupURL(gid, "/members"), map[string]any{"id": o, "role": roleOwner}, nil)
		default:
			err = p.keys.c.send(ctx, s.groups, http.MethodPatch, groupURL(gid, "/members/"+url.PathEscape(o)), map[string]any{"role": roleOwner}, nil)
		}
		switch {
		case err == nil, isStatus(err, http.StatusConflict):
			added++
		case isRefusal(err):
		default:
			return added, demoted, err
		}
	}
	for id, r := range role {
		if r != roleOwner || slices.Contains(owners, id) {
			continue
		}
		err := p.keys.c.send(ctx, s.groups, http.MethodPatch, groupURL(gid, "/members/"+url.PathEscape(id)), map[string]any{"role": roleMember}, nil)
		switch {
		case err == nil:
			demoted++
		case isRefusal(err):
		default:
			return added, demoted, err
		}
	}
	return added, demoted, nil
}

// settleGroup records a group write's event and touches its row.
func (p *provisioner) settleGroup(ctx context.Context, s session, actor contracts.Actor, action, gid string, codes []string) error {
	now := p.now()
	return db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.TouchGroup(ctx, store.TouchGroupParams{Now: now, GroupID: gid}); err != nil {
			return fmt.Errorf("google: record the group: %w", err)
		}
		return recordEvent(ctx, q, now, actor, action, gid, codes)
	})
}

// SetMembers reconciles the members of Staff's group to the users
// memberExternalIDs name (contracts.IdentityProvisioner.SetMembers): the
// members read in pages, the missing added as members, the users the list
// does not name removed — never an owner, whom EnsureGroup keeps, and never
// a member that is not a user. ErrGroupNotManageable for a group EnsureGroup
// never made; ErrGroupUnconfirmed for an adopted group nobody has
// confirmed, nothing written. Every failure is a code with the member's id,
// never Google's words.
func (p *provisioner) SetMembers(ctx context.Context, actor contracts.Actor, groupID string, memberExternalIDs []string) (contracts.MembersResult, error) {
	result := contracts.MembersResult{Failed: []contracts.MemberFailure{}}
	if !actor.Valid() {
		return result, contracts.ErrInvalidActor
	}
	s, err := p.open(ctx, true)
	if err != nil {
		return result, err
	}
	err = p.setMembers(ctx, s, actor, groupID, memberExternalIDs, &result)
	return result, unavailable(err)
}

func (p *provisioner) setMembers(ctx context.Context, s session, actor contracts.Actor, groupID string, ids []string, result *contracts.MembersResult) error {
	row, present, err := groupRow(ctx, s.q, strings.TrimSpace(groupID))
	switch {
	case err != nil:
		return err
	case !present:
		return contracts.ErrGroupNotManageable
	case !trustedGroup(row):
		return contracts.ErrGroupUnconfirmed
	}
	var wanted []string
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		switch {
		case !validUserID(id):
			result.Failed = append(result.Failed, contracts.MemberFailure{ID: raw, Code: contracts.MemberNotFound})
		case !slices.Contains(wanted, id):
			wanted = append(wanted, id)
		}
	}
	members, err := p.listMembers(ctx, s, row.GroupID)
	if errors.Is(err, errNotFound) {
		if err := s.q.DeleteGroup(ctx, row.GroupID); err != nil {
			return fmt.Errorf("google: forget the group gone: %w", err)
		}
		return contracts.ErrGroupNotManageable
	} else if err != nil {
		return err
	}
	have := map[string]string{}
	for _, m := range members {
		if m.Type == typeUser || m.Type == "" {
			have[m.ID] = m.Role
		}
	}
	var failure error
	for _, id := range wanted {
		if _, ok := have[id]; ok {
			continue
		}
		err := p.keys.c.send(ctx, s.groups, http.MethodPost, groupURL(row.GroupID, "/members"), map[string]any{"id": id, "role": roleMember}, nil)
		code := ""
		switch {
		case err == nil:
			result.Added++
		case isStatus(err, http.StatusConflict):
		case isStatus(err, http.StatusNotFound), isReason(err, "notFound"):
			code = contracts.MemberNotFound
		case isStatus(err, http.StatusBadRequest), isStatus(err, http.StatusForbidden):
			code = contracts.MemberRefused
		case isRefusal(err):
			code = contracts.MemberOther
		default:
			failure = err
		}
		if failure != nil {
			break
		}
		if code != "" {
			result.Failed = append(result.Failed, contracts.MemberFailure{ID: id, Code: code})
		}
	}
	if failure == nil {
		var remove []string
		for id, r := range have {
			if r != roleOwner && !slices.Contains(wanted, id) {
				remove = append(remove, id)
			}
		}
		slices.Sort(remove)
		for _, id := range remove {
			err := p.keys.c.send(ctx, s.groups, http.MethodDelete, groupURL(row.GroupID, "/members/"+url.PathEscape(id)), nil, nil)
			switch {
			case err == nil, isStatus(err, http.StatusNotFound):
				result.Removed++
			case isStatus(err, http.StatusBadRequest), isStatus(err, http.StatusForbidden):
				result.Failed = append(result.Failed, contracts.MemberFailure{ID: id, Code: contracts.MemberRefused})
			case isRefusal(err):
				result.Failed = append(result.Failed, contracts.MemberFailure{ID: id, Code: contracts.MemberOther})
			default:
				failure = err
			}
			if failure != nil {
				break
			}
		}
	}
	if failure != nil && (ctx.Err() != nil || result.Added+result.Removed+len(result.Failed) == 0) {
		return failure
	}
	codes := []string{
		"added:" + strconv.Itoa(result.Added), "removed:" + strconv.Itoa(result.Removed), "failed:" + strconv.Itoa(len(result.Failed)),
	}
	if failure != nil {
		codes = append(codes, "failed")
	}
	if err := p.settleGroup(ctx, s, actor, actionGroupMembersSet, row.GroupID, codes); err != nil {
		return errors.Join(failure, err)
	}
	return failure
}

// DeleteGroup lets go of the group of Staff's group
// (contracts.IdentityProvisioner.DeleteGroup): a group the module did not
// make — adopted, confirmed or not — is the organisation's, and is only
// forgotten (kept_not_ours); one it made is deleted (groups.delete), a
// group Google no longer holds forgotten. A group never made answers nil
// with no call.
func (p *provisioner) DeleteGroup(ctx context.Context, actor contracts.Actor, groupID string) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	q := store.New(p.deps.Pool)
	row, present, err := groupRow(ctx, q, strings.TrimSpace(groupID))
	if err != nil || !present {
		return err
	}
	var codes []string
	if !row.CreatedByUs {
		codes = []string{codeKeptNotOurs}
	} else {
		s, err := p.open(ctx, true)
		if err != nil {
			return err
		}
		switch err := p.keys.c.send(ctx, s.groups, http.MethodDelete, groupURL(row.GroupID), nil, nil); {
		case isStatus(err, http.StatusNotFound):
			codes = []string{"gone"}
		case err != nil:
			return unavailable(err)
		}
	}
	now := p.now()
	return db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		qt := q.WithTx(tx)
		if err := qt.DeleteGroup(ctx, row.GroupID); err != nil {
			return fmt.Errorf("google: forget the group: %w", err)
		}
		return recordEvent(ctx, qt, now, actor, actionGroupDeleted, row.GroupID, codes)
	})
}
