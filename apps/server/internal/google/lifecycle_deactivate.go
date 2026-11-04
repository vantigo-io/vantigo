package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the leaver, the delete and the read (Google Workspace design
// G17, G19): the user suspended first, then the leaver's steps the account
// rules turn on; the delete of a suspended user, which Staff's own
// deleteExternalAfterDays asks for (P1); and Account, which reads and writes
// nothing of the module's.

// The leaver's steps after the suspension, by the name a failure is
// reported with (leaver_step_failed:<name>), in their order.
const (
	stepRemoveLicences = "remove_licences"
	stepRemoveGroups   = "remove_groups"
	stepSignOut        = "sign_out"
)

// leaver_remove_groups' values.
const (
	leaverGroupsAll     = "all"
	leaverGroupsManaged = "managed"
)

// maxUserGroupPages bounds the pages of a leaver's groups read.
const maxUserGroupPages = 20

// Deactivate suspends the user and runs the leaver's steps
// (contracts.IdentityProvisioner.Deactivate), whatever the connection's
// status, on any working key: suspended true, always and first, the password
// waiting cleared, then in order as the account rules turn them on — the
// licences removed (Google bills a suspended user's, U16), the groups left
// (all, or the module's own), and the sessions signed out (U19: a refusal is
// reported sign_out_refused and the rest goes on). A step that fails leaves
// the rest to the next call and is reported leaver_step_failed:<step>; the
// user is answered suspended all the same. An unconfirmed adoption is
// ErrExternalUnconfirmed, nothing written.
func (p *provisioner) Deactivate(ctx context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !actor.Valid() {
		return contracts.ExternalAccount{}, contracts.ErrInvalidActor
	}
	s, u, row, err := p.openUser(ctx, externalID, spec, false)
	if err != nil {
		return contracts.ExternalAccount{}, unavailable(err)
	}
	acc, err := p.deactivate(ctx, s, actor, u, row)
	return acc, unavailable(err)
}

func (p *provisioner) deactivate(ctx context.Context, s session, actor contracts.Actor, u directoryUser, row store.GoogleAccount) (contracts.ExternalAccount, error) {
	if !trusted(row) {
		return contracts.ExternalAccount{}, p.unconfirmed(ctx, s, actor, actionUserDeactivated, u, row)
	}
	if u.privileged() {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserDeactivated, u.ID, privilegedRefusal())
	}
	var done []string
	if !u.Suspended {
		err := p.keys.c.send(ctx, s.users, http.MethodPatch, userURL(u.ID), map[string]any{"suspended": true}, nil)
		switch {
		case isStatus(err, http.StatusForbidden):
			return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserDeactivated, u.ID, privilegedRefusal())
		case err != nil:
			return contracts.ExternalAccount{}, err
		}
		done = append(done, "suspended")
		u.Suspended = true
	}
	// Suspended, the password waiting and the person's own connection are of
	// no use to anyone: cleared before any other step can fail.
	now := p.now()
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.SetAccountState(ctx, store.SetAccountStateParams{State: accountState(u, row), Reported: orEmpty(row.Reported), Now: now, UserID: u.ID}); err != nil &&
			!errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("google: record the account suspended: %w", err)
		}
		if err := q.ClearAccountPass(ctx, store.ClearAccountPassParams{Now: now, UserID: u.ID}); err != nil {
			return fmt.Errorf("google: clear the password: %w", err)
		}
		// The leaver's own Google connection closes with its token forgotten
		// (G19, G28): their calendar copies stay in the suspended account.
		return closePersonal(ctx, q, now, actor, u.ID, nil, true, personalClosedLeaver)
	})
	if err != nil {
		return contracts.ExternalAccount{}, err
	}

	steps := []struct {
		name string
		on   bool
		run  func() (string, error)
	}{
		{stepRemoveLicences, s.conn.LeaverRemoveLicences, func() (string, error) { return p.removeAllLicences(ctx, s, u, row) }},
		{stepRemoveGroups, s.conn.LeaverRemoveGroups == leaverGroupsAll || s.conn.LeaverRemoveGroups == leaverGroupsManaged, func() (string, error) {
			return p.leaveGroups(ctx, s, u, s.conn.LeaverRemoveGroups == leaverGroupsManaged)
		}},
		{stepSignOut, s.conn.LeaverSignOut, func() (string, error) { return p.signOut(ctx, s, u) }},
	}
	var reported []string
	for _, step := range steps {
		if !step.on {
			continue
		}
		code, err := step.run()
		switch {
		case err != nil && ctx.Err() != nil:
			return contracts.ExternalAccount{}, ctx.Err()
		case err != nil:
			p.keys.c.logger.WarnContext(ctx, "google: a leaver's step failed; the rest wait for the next call",
				"step", step.name, "user_id", u.ID, "error", err.Error())
			reported = append(reported, reportedLeaverStepFailed+":"+step.name)
		case code == reportedSignOutRefused:
			reported = append(reported, code)
			continue
		case code != "":
			done = append(done, code)
			continue
		default:
			continue
		}
		break
	}
	reported = mergeCodes(row.Reported, leaverJudges, reported)
	row, err = p.settle(ctx, s, actor, actionUserDeactivated, u, accountState(u, row), reported, append(done, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	return account(u, &row, p.now()), nil
}

// signOut signs the user out of every session (users.signOut). A refusal
// (4xx) is reported sign_out_refused, not a failure: the suspension is
// what keeps the leaver out (U19).
func (p *provisioner) signOut(ctx context.Context, s session, u directoryUser) (string, error) {
	err := p.keys.c.send(ctx, s.users, http.MethodPost, userURL(u.ID, "/signOut"), nil, nil)
	switch {
	case err == nil:
		return "signed_out", nil
	case isRefusal(err):
		return reportedSignOutRefused, nil
	}
	return "", err
}

// leaveGroups takes the user out of every group groups.list(userKey) names
// — when managed, of those the module made alone. A membership already gone
// (404) is done; another refusal fails the step, for the next call to try
// again.
func (p *provisioner) leaveGroups(ctx context.Context, s session, u directoryUser, managed bool) (string, error) {
	var ours []string
	if managed {
		ids, err := s.q.ListGroupIDs(ctx)
		if err != nil {
			return "", fmt.Errorf("google: read the module's groups: %w", err)
		}
		if len(ids) == 0 {
			return "", nil
		}
		ours = ids
	}
	var groups []string
	token := ""
	for range maxUserGroupPages {
		v := url.Values{"userKey": {u.ID}, "maxResults": {"200"}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page struct {
			Groups []struct {
				ID string `json:"id"`
			} `json:"groups"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := p.keys.c.get(ctx, s.groups, directoryBase+"/groups?"+v.Encode(), &page); err != nil {
			return "", err
		}
		for _, g := range page.Groups {
			if g.ID != "" && (!managed || slices.Contains(ours, g.ID)) {
				groups = append(groups, g.ID)
			}
		}
		if token = page.NextPageToken; token == "" {
			break
		}
	}
	refused := 0
	for _, gid := range groups {
		err := p.keys.c.send(ctx, s.groups, http.MethodDelete, groupURL(gid, "/members/"+url.PathEscape(u.ID)), nil, nil)
		switch {
		case err == nil, isStatus(err, http.StatusNotFound):
		case isRefusal(err):
			refused++
		default:
			return "", err
		}
	}
	if refused > 0 {
		return "", fmt.Errorf("google: %d of the leaver's group removals were refused", refused)
	}
	if len(groups) == 0 {
		return "", nil
	}
	return "groups_left", nil
}

// Delete deletes a suspended user (contracts.IdentityProvisioner.Delete):
// ErrExternalEnabled while Google says it is not suspended; Google keeps it
// restorable for 20 days. The row is marked deleted and kept for the
// events, its password cleared and the person's own connection closed with
// its token forgotten. A user the organisation no longer holds is deleted
// already. An unconfirmed adoption is ErrExternalUnconfirmed,
// nothing written.
func (p *provisioner) Delete(ctx context.Context, actor contracts.Actor, externalID string) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	s, u, row, err := p.openUser(ctx, externalID, contracts.ExternalSpec{}, true)
	switch {
	case errors.Is(err, errNoSuchUser):
		return unavailable(p.markDeleted(ctx, actor, externalID))
	case err != nil:
		return unavailable(err)
	}
	return unavailable(p.delete(ctx, s, actor, u, row))
}

func (p *provisioner) delete(ctx context.Context, s session, actor contracts.Actor, u directoryUser, row store.GoogleAccount) error {
	switch {
	case !trusted(row):
		return p.unconfirmed(ctx, s, actor, actionUserDeleted, u, row)
	case u.privileged():
		return p.refused(ctx, s, actor, actionUserDeleted, u.ID, privilegedRefusal())
	case !u.Suspended:
		return p.refused(ctx, s, actor, actionUserDeleted, u.ID, &refusedError{code: refusedEnabled, sentinel: contracts.ErrExternalEnabled})
	}
	err := p.keys.c.send(ctx, s.users, http.MethodDelete, userURL(u.ID), nil, nil)
	switch {
	case isStatus(err, http.StatusForbidden):
		return p.refused(ctx, s, actor, actionUserDeleted, u.ID, privilegedRefusal())
	case err != nil && !isStatus(err, http.StatusNotFound):
		return err
	}
	if err := s.q.ClearAccountPass(ctx, store.ClearAccountPassParams{Now: p.now(), UserID: u.ID}); err != nil {
		return fmt.Errorf("google: clear the password: %w", err)
	}
	if err := closePersonalIn(ctx, p.deps.Pool, s.q, p.now(), actor, u.ID, nil, true, personalClosedDeleted); err != nil {
		return err
	}
	_, err = p.settle(ctx, s, actor, actionUserDeleted, u, stateDeleted, row.Reported, nil)
	return err
}

// markDeleted records as deleted the row of a user the organisation answers
// 404 for, once: a row deleted already, or none, is left as it is, and so is
// an unconfirmed adoption, which the module never owned.
func (p *provisioner) markDeleted(ctx context.Context, actor contracts.Actor, externalID string) error {
	if !validUserID(externalID) {
		return nil
	}
	q := store.New(p.deps.Pool)
	row, err := q.GetAccount(ctx, externalID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("google: read the account: %w", err)
	case row.State == stateDeleted, !trusted(row):
		return nil
	}
	if err := q.ClearAccountPass(ctx, store.ClearAccountPassParams{Now: p.now(), UserID: row.UserID}); err != nil {
		return fmt.Errorf("google: clear the password: %w", err)
	}
	if err := closePersonalIn(ctx, p.deps.Pool, q, p.now(), actor, row.UserID, nil, true, personalClosedDeleted); err != nil {
		return err
	}
	_, err = p.settle(ctx, session{q: q}, actor, actionUserDeleted, directoryUser{ID: row.UserID, PrimaryEmail: row.PrimaryEmail}, stateDeleted, row.Reported, []string{"gone"})
	return err
}

// Account reads the user (contracts.IdentityProvisioner.Account), on any
// working key whatever the status — Staff reads it before it decides a
// deactivation —, or (nil, nil) when the organisation holds none by that
// id. The row is read, never written.
func (p *provisioner) Account(ctx context.Context, externalID string) (*contracts.ExternalAccount, error) {
	if !validUserID(externalID) {
		return nil, nil
	}
	s, err := p.open(ctx, false)
	if err != nil {
		return nil, err
	}
	u, err := p.getUser(ctx, s, externalID)
	switch {
	case errors.Is(err, errNoSuchUser):
		return nil, nil
	case err != nil:
		return nil, unavailable(err)
	}
	var row *store.GoogleAccount
	switch r, err := s.q.GetAccount(ctx, externalID); {
	case err == nil:
		row = &r
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("google: read the account: %w", err)
	}
	out := account(u, row, p.now())
	return &out, nil
}
