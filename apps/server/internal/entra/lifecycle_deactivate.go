package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the leaver, the delete and the read (Entra design D13): the
// user disabled first, then the leaver's steps the connection turns on; the
// soft delete of a disabled user; and Account, which reads and writes
// nothing of the module's.

// The leaver's steps after the disable, by the name a failure is reported
// with (leaver_step_failed:<name>), in their order.
const (
	stepRevokeSessions = "revoke_sessions"
	stepLeaveDate      = "leave_date"
	stepStripLicences  = "strip_licences"
	stepRemoveGroups   = "remove_groups"
)

// maxMemberOfPages bounds the pages of memberOf one leaver reads.
const maxMemberOfPages = 50

// leaver_remove_groups' values: "all" removes a leaver from every group
// removable admits, "managed" from the module's own groups alone, "none"
// from none.
const (
	leaverGroupsAll     = "all"
	leaverGroupsManaged = "managed"
)

// Deactivate disables the user and runs the leaver's steps
// (contracts.IdentityProvisioner.Deactivate), whatever the connection's
// status, on any working credential: accountEnabled false, always and first
// — what continuous access evaluation reacts to —, the pass cleared, then in
// order as the connection turns them on: revokeSignInSessions, the end day's
// 23:00 as employeeLeaveDateTime, every direct licence removed, and the
// groups (leaver_remove_groups). A step that fails leaves the rest to the
// next call and is reported leaver_step_failed:<step>; the user is answered
// disabled all the same. An unconfirmed adoption is ErrExternalUnconfirmed,
// nothing written: the module never owned it, and a leaver's safety is not
// its to give until a holder of entra:manage confirms it.
func (p *provisioner) Deactivate(ctx context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !actor.Valid() {
		return contracts.ExternalAccount{}, contracts.ErrInvalidActor
	}
	s, u, id, row, err := p.openUser(ctx, externalID, spec, false)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	if u.synced() {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserDeactivated, &id, u.UserPrincipalName, syncedRefusal())
	}
	if !trusted(row) {
		return contracts.ExternalAccount{}, p.unconfirmed(ctx, s, actor, actionUserDeactivated, id, u, row)
	}
	var done []string
	if u.AccountEnabled {
		err := p.client.patch(ctx, s.cred, userPath(id), map[string]any{"accountEnabled": false})
		switch {
		case isStatus(err, http.StatusForbidden):
			return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserDeactivated, &id, u.UserPrincipalName, privilegedRefusal())
		case err != nil:
			return contracts.ExternalAccount{}, err
		}
		done = append(done, "disabled")
		u.AccountEnabled = false
		if err := p.sensitiveSucceeded(ctx, s); err != nil {
			return contracts.ExternalAccount{}, err
		}
	}
	// Disabled, the pass and the Outlook connection's token are of no use
	// to anyone: cleared before any other step can fail.
	now := p.client.now()
	err = db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		state := stateDisabled
		if row.State == stateDeleted {
			state = stateDeleted
		}
		if _, err := q.SetAccountState(ctx, store.SetAccountStateParams{State: state, Reported: orEmpty(row.Reported), Now: now, ObjectID: id}); err != nil {
			return fmt.Errorf("entra: record the account disabled: %w", err)
		}
		if err := q.ClearAccountPass(ctx, store.ClearAccountPassParams{Now: now, ObjectID: id}); err != nil {
			return fmt.Errorf("entra: clear the pass: %w", err)
		}
		// The leaver's Outlook connection closes with its token forgotten
		// (Outlook connection design M7): the steps below end the token
		// before any clean-up could use it, and none of them may keep it.
		return closeOutlook(ctx, q, now, actor, id, nil, true, outlookClosedLeaver)
	})
	if err != nil {
		return contracts.ExternalAccount{}, err
	}

	steps := []struct {
		name string
		on   bool
		run  func() (string, error)
	}{
		{stepRevokeSessions, s.conn.LeaverRevokeSessions, func() (string, error) {
			return "sessions_revoked", p.client.post(ctx, s.cred, userPath(id, "/revokeSignInSessions"), nil, nil)
		}},
		{stepLeaveDate, s.conn.LeaverWriteLeaveDate && spec.LeaveDate != nil, func() (string, error) {
			return p.writeLeaveDate(ctx, s, id, u, *spec.LeaveDate)
		}},
		{stepStripLicences, s.conn.LeaverStripLicences, func() (string, error) {
			return p.stripLicences(ctx, s, id, u)
		}},
		{stepRemoveGroups, s.conn.LeaverRemoveGroups == leaverGroupsAll || s.conn.LeaverRemoveGroups == leaverGroupsManaged, func() (string, error) {
			return p.removeGroups(ctx, s, id, s.conn.LeaverRemoveGroups == leaverGroupsManaged)
		}},
	}
	var reported []string
	for _, step := range steps {
		if !step.on {
			continue
		}
		code, err := step.run()
		if err != nil {
			if ctx.Err() != nil {
				return contracts.ExternalAccount{}, ctx.Err()
			}
			p.client.logger.WarnContext(ctx, "entra: a leaver's step failed; the rest wait for the next call",
				"step", step.name, "object_id", id.String(), "error", err.Error())
			reported = append(reported, reportedLeaverStepFailed+":"+step.name)
			break
		}
		if code != "" {
			done = append(done, code)
		}
	}
	state := stateDisabled
	if row.State == stateDeleted {
		state = stateDeleted
	}
	row, err = p.settle(ctx, s, actor, actionUserDeactivated, id, u.UserPrincipalName, state, reported, append(done, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	return account(u, &row, p.client.now()), nil
}

// writeLeaveDate writes the end day's 23:00 in the business zone, unless
// the user holds it already.
func (p *provisioner) writeLeaveDate(ctx context.Context, s session, id uuid.UUID, u graphUser, day time.Time) (string, error) {
	at, err := p.instant(ctx, day, leaveHour)
	if err != nil {
		return "", err
	}
	if u.EmployeeLeaveDateTime != nil && u.EmployeeLeaveDateTime.Equal(at) {
		return "", nil
	}
	if err := p.client.patch(ctx, s.cred, userPath(id), map[string]any{"employeeLeaveDateTime": at.UTC().Format(time.RFC3339)}); err != nil {
		return "", err
	}
	return "leave_date_written", nil
}

// stripLicences removes every licence assigned directly — assignedByGroup
// null; a group's licence is the tenant's (design reading 9) — as the
// connection's leaver_strip_licences asks, and forgets the licences the
// module had assigned.
func (p *provisioner) stripLicences(ctx context.Context, s session, id uuid.UUID, u graphUser) (string, error) {
	var direct []string
	for _, st := range u.LicenseAssignmentStates {
		if st.AssignedByGroup == nil && st.SkuID != "" && !slices.Contains(direct, st.SkuID) {
			direct = append(direct, st.SkuID)
		}
	}
	code := ""
	if len(direct) > 0 {
		if err := p.client.post(ctx, s.cred, userPath(id, "/assignLicense"), map[string]any{"addLicenses": []any{}, "removeLicenses": direct}, nil); err != nil {
			return "", err
		}
		code = "licences_stripped"
	}
	if err := s.q.SetAccountLicences(ctx, store.SetAccountLicencesParams{LicencesAssigned: []string{}, Now: p.client.now(), ObjectID: id}); err != nil {
		return "", fmt.Errorf("entra: forget the module's licences: %w", err)
	}
	return code, nil
}

// graphGroup is a group as memberOf lists it, and as the groups' calls read
// it (groupSelect).
type graphGroup struct {
	ODataType          string     `json:"@odata.type"`
	ID                 string     `json:"id"`
	DisplayName        string     `json:"displayName"`
	Description        *string    `json:"description"`
	MailNickname       string     `json:"mailNickname"`
	Visibility         *string    `json:"visibility"`
	GroupTypes         []string   `json:"groupTypes"`
	MembershipRule     *string    `json:"membershipRule"`
	IsAssignableToRole *bool      `json:"isAssignableToRole"`
	MailEnabled        *bool      `json:"mailEnabled"`
	SecurityEnabled    *bool      `json:"securityEnabled"`
	CreatedDateTime    *time.Time `json:"createdDateTime"`
}

// removable reports whether the leaver's "all" removes the user from g: a
// group (not a role or an administrative unit), not dynamic, not
// role-assignable, and not a distribution list Exchange keeps — the groups
// Lifecycle Workflows' "remove from all groups" skips too.
func (g graphGroup) removable() bool {
	switch {
	case g.ODataType != "#microsoft.graph.group":
		return false
	case (g.MembershipRule != nil && *g.MembershipRule != "") || slices.Contains(g.GroupTypes, "DynamicMembership"):
		return false
	case g.IsAssignableToRole != nil && *g.IsAssignableToRole:
		return false
	case g.MailEnabled != nil && *g.MailEnabled && (g.SecurityEnabled == nil || !*g.SecurityEnabled) && !slices.Contains(g.GroupTypes, "Unified"):
		return false
	}
	return true
}

// removeGroups removes the user from every group memberOf lists that
// removable admits — when managed, from those of them the module keeps
// present in entra.groups alone — by $ref (membersRefPath), never the
// member's object itself, inside $batch requests (batchDelete) so the
// user's id never stands in a request line the client logs. A member
// already gone (404) is done; another answer fails the step, for the next
// call to try again.
func (p *provisioner) removeGroups(ctx context.Context, s session, id uuid.UUID, managed bool) (string, error) {
	var ours []uuid.UUID
	if managed {
		ids, err := s.q.ListGroupObjectIDs(ctx)
		if err != nil {
			return "", fmt.Errorf("entra: read the module's groups: %w", err)
		}
		if len(ids) == 0 {
			return "", nil
		}
		ours = ids
	}
	path := userPath(id, "/memberOf?", query(url.Values{"$select": {"id,groupTypes,membershipRule,isAssignableToRole,mailEnabled,securityEnabled"}}))
	var groups []graphGroup
	for page := 0; path != "" && page < maxMemberOfPages; page++ {
		var answer struct {
			Value    []graphGroup `json:"value"`
			NextLink string       `json:"@odata.nextLink"`
		}
		if err := p.client.get(ctx, s.cred, path, &answer); err != nil {
			return "", err
		}
		groups = append(groups, answer.Value...)
		path = ""
		if rest, ok := strings.CutPrefix(answer.NextLink, graphAPIBase); ok {
			path = rest
		}
	}
	var paths []string
	for _, g := range groups {
		gid, ok := objectID(g.ID)
		if !ok || !g.removable() || (managed && !slices.Contains(ours, gid)) {
			continue
		}
		paths = append(paths, membersRefPath(gid, id))
	}
	if len(paths) == 0 {
		return "", nil
	}
	statuses, err := p.batchDelete(ctx, s, paths)
	if err != nil {
		return "", err
	}
	refused := 0
	for _, st := range statuses {
		if st != http.StatusNoContent && st != http.StatusOK && st != http.StatusNotFound {
			refused++
		}
	}
	if refused > 0 {
		return "", fmt.Errorf("entra: %d of the leaver's group removals were refused", refused)
	}
	return "groups_removed", nil
}

// Delete soft-deletes a disabled user (contracts.IdentityProvisioner.Delete):
// ErrExternalEnabled while Graph says it is enabled; the row marked deleted
// and kept for the events, its Outlook connection closed with its token
// forgotten. A user the tenant no longer holds is deleted
// already. An unconfirmed adoption is ErrExternalUnconfirmed, nothing
// written.
func (p *provisioner) Delete(ctx context.Context, actor contracts.Actor, externalID string) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	s, u, id, row, err := p.openUser(ctx, externalID, contracts.ExternalSpec{}, true)
	switch {
	case errors.Is(err, errNoSuchUser):
		// openUser answers no id with its error; the row is the external
		// id's.
		id, _ = objectID(externalID)
		return p.markDeleted(ctx, actor, id)
	case err != nil:
		return err
	}
	switch {
	case !trusted(row):
		return p.unconfirmed(ctx, s, actor, actionUserDeleted, id, u, row)
	case u.AccountEnabled:
		return p.refused(ctx, s, actor, actionUserDeleted, &id, u.UserPrincipalName, &refusedError{code: refusedEnabled, sentinel: contracts.ErrExternalEnabled})
	case u.synced():
		return p.refused(ctx, s, actor, actionUserDeleted, &id, u.UserPrincipalName, syncedRefusal())
	}
	err = p.client.del(ctx, s.cred, userPath(id))
	switch {
	case isStatus(err, http.StatusForbidden):
		return p.refused(ctx, s, actor, actionUserDeleted, &id, u.UserPrincipalName, privilegedRefusal())
	case err != nil && !isStatus(err, http.StatusNotFound):
		return err
	}
	if err := p.sensitiveSucceeded(ctx, s); err != nil {
		return err
	}
	if err := s.q.ClearAccountPass(ctx, store.ClearAccountPassParams{Now: p.client.now(), ObjectID: id}); err != nil {
		return fmt.Errorf("entra: clear the pass: %w", err)
	}
	if err := closeOutlookIn(ctx, p.deps.Pool, s.q, p.client.now(), actor, id, nil, true, outlookClosedDeleted); err != nil {
		return err
	}
	_, err = p.settle(ctx, s, actor, actionUserDeleted, id, u.UserPrincipalName, stateDeleted, row.Reported, nil)
	return err
}

// markDeleted records as deleted the row of a user the tenant answers 404
// for, once, closing its Outlook connection with its token forgotten: a row
// deleted already, or none, is left as it is, and so is an unconfirmed
// adoption, which the module never owned.
func (p *provisioner) markDeleted(ctx context.Context, actor contracts.Actor, id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	q := store.New(p.deps.Pool)
	row, err := q.GetAccount(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("entra: read the account: %w", err)
	case row.State == stateDeleted, !trusted(row):
		return nil
	}
	if err := closeOutlookIn(ctx, p.deps.Pool, q, p.client.now(), actor, id, nil, true, outlookClosedDeleted); err != nil {
		return err
	}
	_, err = p.settle(ctx, session{q: q}, actor, actionUserDeleted, id, row.UserPrincipalName, stateDeleted, row.Reported, []string{"gone"})
	return err
}

// Account reads the user (contracts.IdentityProvisioner.Account), on any
// working credential whatever the status — Staff reads it before it decides
// a deactivation —, or (nil, nil) when the tenant holds none by that id.
// The row is read, never written: a user gone is Staff's user_gone (plan
// reading 6).
func (p *provisioner) Account(ctx context.Context, externalID string) (*contracts.ExternalAccount, error) {
	id, ok := objectID(externalID)
	if !ok {
		return nil, nil
	}
	s, err := p.open(ctx, false)
	if err != nil {
		return nil, err
	}
	u, err := p.getUser(ctx, s, id)
	switch {
	case errors.Is(err, errNoSuchUser):
		return nil, nil
	case err != nil:
		return nil, err
	}
	var row *store.EntraAccount
	switch r, err := s.q.GetAccount(ctx, id); {
	case err == nil:
		row = &r
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("entra: read the account: %w", err)
	}
	out := account(u, row, p.client.now())
	return &out, nil
}
