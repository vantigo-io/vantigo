package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is Prepare and Update (Entra design D13): the user found by the
// employee id and brought to the spec, or created disabled with a legal UPN
// and a password nobody sees; and the mover's changed properties and
// manager.

// Prepare finds the user whose employeeId is the spec's number, or creates
// it disabled (contracts.IdentityProvisioner.Prepare).
//
//   - Two users hold the number: ErrExternalAmbiguous.
//   - One: linked (created_by_us false, unless the module made it) and
//     brought to the spec by the update path; one synchronised from
//     on-premises is ErrExternalSyncedOnPremises.
//   - None (and the module's own row names no user the tenant still holds —
//     the filter may lag a create by seconds): created, Created true.
func (p *provisioner) Prepare(ctx context.Context, actor contracts.Actor, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !actor.Valid() {
		return contracts.ExternalAccount{}, contracts.ErrInvalidActor
	}
	number := strings.TrimSpace(spec.EmployeeNumber)
	if number == "" {
		return contracts.ExternalAccount{}, errors.New("entra: a spec without an employee number")
	}
	s, err := p.open(ctx, true)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	var row *store.EntraAccount
	switch r, err := s.q.GetAccountByNumber(ctx, number); {
	case err == nil:
		row = &r
	case !errors.Is(err, pgx.ErrNoRows):
		return contracts.ExternalAccount{}, fmt.Errorf("entra: read the account: %w", err)
	}

	found, err := p.findByNumber(ctx, s, number)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	if len(found) > 1 {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, nil, "",
			&refusedError{code: refusedAmbiguous, sentinel: contracts.ErrExternalAmbiguous})
	}
	var user *graphUser
	switch {
	case len(found) == 1:
		id, ok := objectID(found[0].ID)
		if !ok {
			return contracts.ExternalAccount{}, fmt.Errorf("entra: Graph answered a user without an id")
		}
		u, err := p.getUser(ctx, s, id)
		if err != nil {
			return contracts.ExternalAccount{}, err
		}
		user = &u
	case row != nil && row.State != stateDeleted:
		// The filter is eventually consistent: the user the row names may be
		// one this module created a moment ago.
		u, err := p.getUser(ctx, s, row.ObjectID)
		switch {
		case err == nil:
			user = &u
		case !errors.Is(err, errNoSuchUser):
			return contracts.ExternalAccount{}, err
		}
	}

	if row != nil && (user == nil || row.ObjectID.String() != strings.ToLower(user.ID)) {
		// The number's row names another user: one deleted (a rehire after a
		// delete is a new user), or one the tenant no longer holds, is
		// forgotten; one it still holds under another number is ambiguous.
		if err := p.release(ctx, s, actor, *row); err != nil {
			return contracts.ExternalAccount{}, err
		}
	}
	if user != nil {
		return p.link(ctx, s, actor, spec, number, *user)
	}
	return p.create(ctx, s, actor, spec, number)
}

// release forgets row, whose number another user is about to hold: a
// deleted user's, or one the tenant answers 404 for. A user still held is
// ErrExternalAmbiguous: two users would answer to one number.
func (p *provisioner) release(ctx context.Context, s session, actor contracts.Actor, row store.EntraAccount) error {
	if row.State != stateDeleted {
		_, err := p.getUser(ctx, s, row.ObjectID)
		switch {
		case err == nil:
			return p.refused(ctx, s, actor, actionUserPrepared, &row.ObjectID, row.UserPrincipalName,
				&refusedError{code: refusedAmbiguous, sentinel: contracts.ErrExternalAmbiguous})
		case !errors.Is(err, errNoSuchUser):
			return err
		}
	}
	if err := s.q.DeleteAccount(ctx, row.ObjectID); err != nil {
		return fmt.Errorf("entra: forget the old account: %w", err)
	}
	return nil
}

// link records the user Prepare found and brings it to the spec.
func (p *provisioner) link(ctx context.Context, s session, actor contracts.Actor, spec contracts.ExternalSpec, number string, u graphUser) (contracts.ExternalAccount, error) {
	id, ok := objectID(u.ID)
	if !ok {
		return contracts.ExternalAccount{}, fmt.Errorf("entra: Graph answered a user without an id")
	}
	if u.synced() {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, &id, u.UserPrincipalName, syncedRefusal())
	}
	state := stateDisabled
	if u.AccountEnabled {
		state = stateEnabled
	}
	// The row first: kept as it was when the module has one (created_by_us
	// and the pass stay), its codes until the update path judges them.
	before, err := s.q.GetAccount(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return contracts.ExternalAccount{}, fmt.Errorf("entra: read the account: %w", err)
	}
	row, err := s.q.UpsertAccount(ctx, store.UpsertAccountParams{
		ObjectID: id, EmployeeNumber: number, UserPrincipalName: u.UserPrincipalName, State: state,
		CreatedByUs: false, Reported: orEmpty(before.Reported), LastError: before.LastError, Now: p.client.now(),
	})
	if err != nil {
		return contracts.ExternalAccount{}, fmt.Errorf("entra: record the account: %w", err)
	}
	reported, changed, err := p.bringToSpec(ctx, s, id, u, spec)
	if r, ok := asRefused(err); ok {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, &id, u.UserPrincipalName, r)
	} else if err != nil {
		return contracts.ExternalAccount{}, err
	}
	reported = mergeCodes(row.Reported, updateJudges, reported)
	codes := append([]string{"found"}, changedCodes(changed)...)
	row, err = p.settle(ctx, s, actor, actionUserPrepared, id, u.UserPrincipalName, state, reported, append(codes, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	return account(u, &row, p.client.now()), nil
}

// changedCodes are the names an update wrote, as event codes.
func changedCodes(changed []string) []string {
	out := make([]string, 0, len(changed))
	for _, c := range changed {
		out = append(out, "changed:"+c)
	}
	return out
}

// create makes the person's user disabled: the address rule's UPN (the next
// suffix on a pattern's conflict, ErrExternalAddressTaken on the work
// email's or past the last suffix), the mapped properties, the employee id,
// a password from crypto/rand never stored or logged that the pass replaces,
// then the manager.
func (p *provisioner) create(ctx context.Context, s session, actor contracts.Actor, spec contracts.ExternalSpec, number string) (contracts.ExternalAccount, error) {
	addr, err := addressFor(spec, s.conn.UpnPattern, s.conn.UpnDomain, s.conn.VerifiedDomains)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	attrs := wantedAttributes(spec, usageLocation(spec, s.conn))
	var created graphUser
	for n := 1; ; n++ {
		upn, nickname, ok := addr.attempt(n)
		if !ok {
			return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, nil, "",
				&refusedError{code: refusedAddressTaken, sentinel: contracts.ErrExternalAddressTaken})
		}
		password, err := temporaryPassword()
		if err != nil {
			return contracts.ExternalAccount{}, fmt.Errorf("entra: make the create's password: %w", err)
		}
		body := map[string]any{
			"accountEnabled":    false,
			"userPrincipalName": upn,
			"mailNickname":      nickname,
			"employeeId":        number,
			"passwordProfile":   map[string]any{"password": password, "forceChangePasswordNextSignIn": true},
		}
		attrs.createBody(body)
		if _, ok := body["displayName"]; !ok {
			body["displayName"] = nickname
		}
		err = p.client.post(ctx, s.cred, "/users", body, &created)
		switch {
		case err == nil:
		case isCode(err, "ObjectConflict"):
			if addr.fromEmail {
				return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, nil, upn,
					&refusedError{code: refusedAddressTaken, sentinel: contracts.ErrExternalAddressTaken})
			}
			continue
		case isStatus(err, http.StatusForbidden):
			return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, nil, upn, privilegedRefusal())
		default:
			return contracts.ExternalAccount{}, err
		}
		break
	}
	id, ok := objectID(created.ID)
	if !ok {
		return contracts.ExternalAccount{}, fmt.Errorf("entra: Graph created a user without an id")
	}
	if err := p.sensitiveSucceeded(ctx, s); err != nil {
		return contracts.ExternalAccount{}, err
	}
	// The row at once, so a failure from here on finds the user next time.
	if _, err := s.q.UpsertAccount(ctx, store.UpsertAccountParams{
		ObjectID: id, EmployeeNumber: number, UserPrincipalName: created.UserPrincipalName, State: stateDisabled,
		CreatedByUs: true, Reported: []string{}, Now: p.client.now(),
	}); err != nil {
		return contracts.ExternalAccount{}, fmt.Errorf("entra: record the account: %w", err)
	}
	var reported []string
	code, err := p.setManager(ctx, s, id, spec.ManagerExternalID)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	if code != "" {
		reported = append(reported, code)
	}
	row, err := p.settle(ctx, s, actor, actionUserPrepared, id, created.UserPrincipalName, stateDisabled, reported,
		append([]string{"created"}, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	created.AccountEnabled = false
	out := account(created, &row, p.client.now())
	out.Created = true
	return out, nil
}

// Update writes the properties of the spec that differ and the manager —
// never the employee id or the UPN (contracts.IdentityProvisioner.Update).
func (p *provisioner) Update(ctx context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !actor.Valid() {
		return contracts.ExternalAccount{}, contracts.ErrInvalidActor
	}
	s, u, id, row, err := p.openUser(ctx, externalID, spec, true)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	if u.synced() {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserUpdated, &id, u.UserPrincipalName, syncedRefusal())
	}
	reported, changed, err := p.bringToSpec(ctx, s, id, u, spec)
	if r, ok := asRefused(err); ok {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserUpdated, &id, u.UserPrincipalName, r)
	} else if err != nil {
		return contracts.ExternalAccount{}, err
	}
	reported = mergeCodes(row.Reported, updateJudges, reported)
	state := row.State
	if state != stateDeleted {
		state = stateDisabled
		if u.AccountEnabled {
			state = stateEnabled
		}
	}
	row, err = p.settle(ctx, s, actor, actionUserUpdated, id, u.UserPrincipalName, state, reported, append(changedCodes(changed), reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	return account(u, &row, p.client.now()), nil
}

// openUser is a write's footing on one user: the session (connected, or any
// working credential), the user as Graph has it — errNoSuchUser (wrapped)
// when the tenant holds none by that id — and its row, made when the
// module had none.
func (p *provisioner) openUser(ctx context.Context, externalID string, spec contracts.ExternalSpec, connected bool) (session, graphUser, uuid.UUID, store.EntraAccount, error) {
	id, ok := objectID(externalID)
	if !ok {
		return session{}, graphUser{}, uuid.Nil, store.EntraAccount{}, errNoSuchUser
	}
	s, err := p.open(ctx, connected)
	if err != nil {
		return session{}, graphUser{}, uuid.Nil, store.EntraAccount{}, err
	}
	u, err := p.getUser(ctx, s, id)
	if err != nil {
		return session{}, graphUser{}, uuid.Nil, store.EntraAccount{}, err
	}
	row, err := p.ensureRow(ctx, s, u, id, strings.TrimSpace(spec.EmployeeNumber))
	if err != nil {
		return session{}, graphUser{}, uuid.Nil, store.EntraAccount{}, err
	}
	return s, u, id, row, nil
}
