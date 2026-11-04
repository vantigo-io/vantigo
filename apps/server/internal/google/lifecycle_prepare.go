package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/directoryaddress"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is Prepare and Update (Google Workspace design G17, G18): the
// user found by the employee number and brought to the spec, or created
// suspended with an address by the shared rule and a password nobody sees;
// and the mover's changed attributes and manager.

// errNoDomain: the connection names no address domain and the organisation
// has no primary domain yet, so no address can be made.
var errNoDomain = errors.New("google: no address domain: set one in the account rules, or test the connection")

// Prepare finds the user holding the spec's number as their organization
// external id, or creates it suspended (contracts.IdentityProvisioner.Prepare).
//
//   - Two users hold the number: ErrExternalAmbiguous.
//   - One: linked and brought to the spec; an administrator is
//     ErrExternalPrivileged; one the module did not make is adopted
//     untrusted — linked, adoption_unconfirmed reported and nothing written —
//     and answered ErrExternalUnconfirmed until a holder of google:manage
//     confirms it.
//   - None (and the module's own row names no user the organisation still
//     holds — the search may lag a create): created, Created true.
func (p *provisioner) Prepare(ctx context.Context, actor contracts.Actor, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !actor.Valid() {
		return contracts.ExternalAccount{}, contracts.ErrInvalidActor
	}
	number := strings.TrimSpace(spec.EmployeeNumber)
	if number == "" {
		return contracts.ExternalAccount{}, errors.New("google: a spec without an employee number")
	}
	s, err := p.open(ctx, true)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	acc, err := p.prepare(ctx, s, actor, spec, number)
	return acc, unavailable(err)
}

func (p *provisioner) prepare(ctx context.Context, s session, actor contracts.Actor, spec contracts.ExternalSpec, number string) (contracts.ExternalAccount, error) {
	var row *store.GoogleAccount
	switch r, err := s.q.GetAccountByNumber(ctx, number); {
	case err == nil:
		row = &r
	case !errors.Is(err, pgx.ErrNoRows):
		return contracts.ExternalAccount{}, fmt.Errorf("google: read the account: %w", err)
	}
	found, err := p.findByNumber(ctx, s, number)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	if len(found) > 1 {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, "",
			&refusedError{code: refusedAmbiguous, sentinel: contracts.ErrExternalAmbiguous})
	}
	var user *directoryUser
	switch {
	case len(found) == 1:
		user = &found[0]
	case row != nil && row.State != stateDeleted:
		// The search may lag a create: the user the row names may be one
		// this module created a moment ago.
		u, err := p.getUser(ctx, s, row.UserID)
		switch {
		case err == nil:
			user = &u
		case !errors.Is(err, errNoSuchUser):
			return contracts.ExternalAccount{}, err
		}
	}
	if row != nil && (user == nil || row.UserID != user.ID) {
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
// deleted user's, or one the organisation answers 404 for. A user still
// held is ErrExternalAmbiguous: two users would answer to one number.
func (p *provisioner) release(ctx context.Context, s session, actor contracts.Actor, row store.GoogleAccount) error {
	if row.State != stateDeleted {
		_, err := p.getUser(ctx, s, row.UserID)
		switch {
		case err == nil:
			return p.refused(ctx, s, actor, actionUserPrepared, row.UserID,
				&refusedError{code: refusedAmbiguous, sentinel: contracts.ErrExternalAmbiguous})
		case !errors.Is(err, errNoSuchUser):
			return err
		}
	}
	if err := s.q.DeleteAccount(ctx, row.UserID); err != nil {
		return fmt.Errorf("google: forget the old account: %w", err)
	}
	return nil
}

// link records the user Prepare found and brings it to the spec.
func (p *provisioner) link(ctx context.Context, s session, actor contracts.Actor, spec contracts.ExternalSpec, number string, u directoryUser) (contracts.ExternalAccount, error) {
	if !validUserID(u.ID) {
		return contracts.ExternalAccount{}, errors.New("google: the Directory answered a user without an id")
	}
	before, err := s.q.GetAccount(ctx, u.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return contracts.ExternalAccount{}, fmt.Errorf("google: read the account: %w", err)
	}
	state := stateEnabled
	if u.Suspended {
		state = stateDisabled
	}
	row, err := s.q.UpsertAccount(ctx, store.UpsertAccountParams{
		UserID: u.ID, EmployeeNumber: number, PrimaryEmail: u.PrimaryEmail, State: state,
		CreatedByUs: false, Reported: orEmpty(before.Reported), LastError: before.LastError, Now: p.now(),
	})
	if err != nil {
		return contracts.ExternalAccount{}, fmt.Errorf("google: record the account: %w", err)
	}
	if u.privileged() {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, u.ID, privilegedRefusal())
	}
	if !trusted(row) {
		return contracts.ExternalAccount{}, p.unconfirmed(ctx, s, actor, actionUserPrepared, u, row, "found")
	}
	reported, changed, err := p.bringToSpec(ctx, s, u, spec)
	var r *refusedError
	if errors.As(err, &r) {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, u.ID, r)
	} else if err != nil {
		return contracts.ExternalAccount{}, err
	}
	reported = mergeCodes(row.Reported, updateJudges, reported)
	codes := append([]string{"found"}, changedCodes(changed)...)
	row, err = p.settle(ctx, s, actor, actionUserPrepared, u, state, reported, append(codes, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	return account(u, &row, p.now()), nil
}

// addressDomain is the domain new addresses are made at: the account
// rules' (one of the verified domains), else the primary domain.
func addressDomain(conn store.GoogleConnection) string {
	if d := strings.ToLower(strings.TrimSpace(deref(conn.AddressDomain))); d != "" {
		return d
	}
	return strings.ToLower(strings.TrimSpace(deref(conn.PrimaryDomain)))
}

// create makes the person's user suspended: the shared address rule's
// primary email (the next suffix on a pattern's conflict,
// ErrExternalAddressTaken on the work email's or past the last suffix), the
// mapped attributes, the employee number as the organization external id, a
// random password nobody sees that must be changed at the first sign-in,
// and the organisational unit. The create is made once (noRetry): when its
// answer is lost, the user it may have made all the same is read back
// (madeAnyway) and taken as this call's.
func (p *provisioner) create(ctx context.Context, s session, actor contracts.Actor, spec contracts.ExternalSpec, number string) (contracts.ExternalAccount, error) {
	addr, err := directoryaddress.Mailbox.For(spec, s.conn.AddressPattern, addressDomain(s.conn), s.conn.Domains)
	if errors.Is(err, directoryaddress.ErrNoDomain) {
		return contracts.ExternalAccount{}, errNoDomain
	} else if err != nil {
		return contracts.ExternalAccount{}, err
	}
	manager, managerCode, err := p.managerEmail(ctx, s, spec.ManagerExternalID)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	pr := wantedProfile(spec)
	var created directoryUser
	for n := 1; ; n++ {
		email, _, ok := addr.Attempt(n)
		if !ok {
			return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, "",
				&refusedError{code: refusedAddressTaken, sentinel: contracts.ErrExternalAddressTaken})
		}
		password, err := directoryaddress.TemporaryPassword()
		if err != nil {
			return contracts.ExternalAccount{}, fmt.Errorf("google: make the create's password: %w", err)
		}
		body := map[string]any{
			"primaryEmail":              email,
			"password":                  password,
			"changePasswordAtNextLogin": true,
			"suspended":                 true,
			"orgUnitPath":               s.conn.OrgUnitPath,
			"externalIds":               []map[string]any{{"type": externalIDOrganization, "value": number}},
		}
		pr.createBody(body, manager)
		err = p.keys.c.send(ctx, s.users, http.MethodPost, directoryBase+"/users", body, &created, noRetry())
		switch {
		case err == nil:
		case errors.Is(err, errGoogleUnreachable) && ctx.Err() == nil:
			made, ok, readErr := p.madeAnyway(ctx, s, number, email)
			if readErr != nil || !ok {
				return contracts.ExternalAccount{}, err
			}
			created = made
		case isStatus(err, http.StatusConflict):
			if addr.FromEmail {
				return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserPrepared, "",
					&refusedError{code: refusedAddressTaken, sentinel: contracts.ErrExternalAddressTaken})
			}
			continue
		case isStatus(err, http.StatusForbidden):
			return contracts.ExternalAccount{}, fmt.Errorf("%w: the admin may not create users: %w", contracts.ErrProviderUnavailable, err)
		default:
			return contracts.ExternalAccount{}, err
		}
		break
	}
	if !validUserID(created.ID) {
		return contracts.ExternalAccount{}, errors.New("google: the Directory created a user without an id")
	}
	if created.PrimaryEmail == "" {
		created.PrimaryEmail, _, _ = addr.Attempt(1)
	}
	if _, err := s.q.UpsertAccount(ctx, store.UpsertAccountParams{
		UserID: created.ID, EmployeeNumber: number, PrimaryEmail: created.PrimaryEmail, State: stateDisabled,
		CreatedByUs: true, Reported: []string{}, Now: p.now(),
	}); err != nil {
		return contracts.ExternalAccount{}, fmt.Errorf("google: record the account: %w", err)
	}
	var reported []string
	if managerCode != "" {
		reported = append(reported, managerCode)
	}
	row, err := p.settle(ctx, s, actor, actionUserPrepared, created, stateDisabled, reported, append([]string{"created"}, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	created.Suspended = true
	out := account(created, &row, p.now())
	out.Created = true
	return out, nil
}

// madeAnyway reads back the user a create whose answer was lost may have
// made: the one user the number's search finds that holds email — the
// address this create asked for — suspended, as the create made it.
func (p *provisioner) madeAnyway(ctx context.Context, s session, number, email string) (directoryUser, bool, error) {
	found, err := p.findByNumber(ctx, s, number)
	if err != nil {
		return directoryUser{}, false, err
	}
	if len(found) != 1 || !strings.EqualFold(found[0].PrimaryEmail, email) || !found[0].Suspended {
		return directoryUser{}, false, nil
	}
	return found[0], true, nil
}

// Update writes the attributes of the spec that differ and the manager —
// never the external id or the primary email (contracts.IdentityProvisioner.
// Update) — and, on an active user, brings the licences the module assigned
// to the spec's. An unconfirmed adoption is ErrExternalUnconfirmed, nothing
// written.
func (p *provisioner) Update(ctx context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !actor.Valid() {
		return contracts.ExternalAccount{}, contracts.ErrInvalidActor
	}
	s, u, row, err := p.openUser(ctx, externalID, spec, true)
	if err != nil {
		return contracts.ExternalAccount{}, unavailable(err)
	}
	acc, err := p.update(ctx, s, actor, u, row, spec)
	return acc, unavailable(err)
}

func (p *provisioner) update(ctx context.Context, s session, actor contracts.Actor, u directoryUser, row store.GoogleAccount, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !trusted(row) {
		return contracts.ExternalAccount{}, p.unconfirmed(ctx, s, actor, actionUserUpdated, u, row)
	}
	if u.privileged() {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserUpdated, u.ID, privilegedRefusal())
	}
	reported, changed, err := p.bringToSpec(ctx, s, u, spec)
	var r *refusedError
	if errors.As(err, &r) {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserUpdated, u.ID, r)
	} else if err != nil {
		return contracts.ExternalAccount{}, err
	}
	codes := changedCodes(changed)
	judges := updateJudges
	if !u.Suspended {
		licences, licenceCodes, err := p.reconcileLicences(ctx, s, u, row, spec.Licences)
		if err != nil {
			return contracts.ExternalAccount{}, err
		}
		reported = append(reported, licences...)
		codes = append(codes, licenceCodes...)
		judges = func(c string) bool { return updateJudges(c) || licenceJudges(c) }
	}
	reported = mergeCodes(row.Reported, judges, reported)
	row, err = p.settle(ctx, s, actor, actionUserUpdated, u, accountState(u, row), reported, append(codes, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	return account(u, &row, p.now()), nil
}

// openUser is a write's footing on one user: the session (connected, or any
// working key), the user as Google has it — errNoSuchUser (wrapped) when
// the organisation holds none by that id — and its row, made when the
// module had none.
func (p *provisioner) openUser(ctx context.Context, externalID string, spec contracts.ExternalSpec, connected bool) (session, directoryUser, store.GoogleAccount, error) {
	id := strings.TrimSpace(externalID)
	if !validUserID(id) {
		return session{}, directoryUser{}, store.GoogleAccount{}, errNoSuchUser
	}
	s, err := p.open(ctx, connected)
	if err != nil {
		return session{}, directoryUser{}, store.GoogleAccount{}, err
	}
	u, err := p.getUser(ctx, s, id)
	if err != nil {
		return session{}, directoryUser{}, store.GoogleAccount{}, err
	}
	row, err := p.ensureRow(ctx, s, u, strings.TrimSpace(spec.EmployeeNumber))
	if err != nil {
		return session{}, directoryUser{}, store.GoogleAccount{}, err
	}
	return s, u, row, nil
}
