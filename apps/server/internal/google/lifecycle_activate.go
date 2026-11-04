package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/directoryaddress"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is Activate and the password (Google Workspace design G17, Q4):
// the user's suspension lifted with a fresh password the person must change
// at their first sign-in, sealed under google/pass and shown once, and the
// licences of the person's groups assigned.

// passPurpose is the secrets box purpose a person's first password is
// sealed under.
const passPurpose = "google/pass"

// Activate lifts the user's suspension (contracts.IdentityProvisioner.
// Activate): suspended false with a fresh password and
// changePasswordAtNextLogin, the password sealed for pass_lifetime_days,
// then the licences brought to the spec's (reconcileLicences). A password is
// set when this call lifts the suspension and none waits unrevealed (or HR
// asked for a fresh one), and on HR's Run now (Flags.Fresh) for a user
// nobody has signed in as yet — never over an account someone uses: a new
// password there would be a way into it. A password Google's policy refuses
// is reported pass_not_allowed and the suspension lifted without it.
// Idempotent. An unconfirmed adoption is ErrExternalUnconfirmed, nothing
// written.
func (p *provisioner) Activate(ctx context.Context, actor contracts.Actor, externalID string, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !actor.Valid() {
		return contracts.ExternalAccount{}, contracts.ErrInvalidActor
	}
	s, u, row, err := p.openUser(ctx, externalID, spec, true)
	if err != nil {
		return contracts.ExternalAccount{}, unavailable(err)
	}
	acc, err := p.activate(ctx, s, actor, u, row, spec)
	return acc, unavailable(err)
}

func (p *provisioner) activate(ctx context.Context, s session, actor contracts.Actor, u directoryUser, row store.GoogleAccount, spec contracts.ExternalSpec) (contracts.ExternalAccount, error) {
	if !trusted(row) {
		return contracts.ExternalAccount{}, p.unconfirmed(ctx, s, actor, actionUserActivated, u, row)
	}
	if u.privileged() {
		return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserActivated, u.ID, privilegedRefusal())
	}
	wasSuspended := u.Suspended
	now := p.now()
	issue := (wasSuspended && (spec.Flags.Fresh || !livePass(row, now))) || (!wasSuspended && spec.Flags.Fresh && u.neverSignedIn())
	var reported, codes []string

	body := map[string]any{}
	if wasSuspended {
		body["suspended"] = false
	}
	var password string
	if issue {
		var err error
		if password, err = directoryaddress.ReadablePassword(); err != nil {
			return contracts.ExternalAccount{}, fmt.Errorf("google: make the password: %w", err)
		}
		body["password"] = password
		body["changePasswordAtNextLogin"] = true
	}
	if len(body) > 0 {
		err := p.keys.c.send(ctx, s.users, http.MethodPatch, userURL(u.ID), body, nil)
		if issue && isStatus(err, http.StatusBadRequest) {
			// The organisation's password policy refused it: the
			// suspension is lifted without it, and HR is told.
			delete(body, "password")
			delete(body, "changePasswordAtNextLogin")
			issue, password = false, ""
			reported = append(reported, contracts.ReportedPassNotAllowed)
			err = nil
			if len(body) > 0 {
				err = p.keys.c.send(ctx, s.users, http.MethodPatch, userURL(u.ID), body, nil)
			}
		}
		switch {
		case isStatus(err, http.StatusForbidden):
			return contracts.ExternalAccount{}, p.refused(ctx, s, actor, actionUserActivated, u.ID, privilegedRefusal())
		case err != nil:
			return contracts.ExternalAccount{}, err
		}
	}
	if wasSuspended {
		codes = append(codes, "enabled")
		if _, err := s.q.SetAccountState(ctx, store.SetAccountStateParams{
			State: stateEnabled, Reported: orEmpty(row.Reported), LastError: nil, Now: p.now(), UserID: u.ID,
		}); err != nil {
			return contracts.ExternalAccount{}, fmt.Errorf("google: record the account enabled: %w", err)
		}
		u.Suspended = false
	}
	if issue {
		if err := p.keepPass(ctx, s, actor, u.ID, password); err != nil {
			return contracts.ExternalAccount{}, err
		}
		codes = append(codes, "pass_issued")
	}
	licences, licenceCodes, err := p.reconcileLicences(ctx, s, u, row, spec.Licences)
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	reported = append(reported, licences...)
	codes = append(codes, licenceCodes...)

	reported = mergeCodes(row.Reported, activateJudges, reported)
	row, err = p.settle(ctx, s, actor, actionUserActivated, u, accountState(u, row), reported, append(codes, reported...))
	if err != nil {
		return contracts.ExternalAccount{}, err
	}
	return account(u, &row, p.now()), nil
}

// keepPass seals the password Google accepted under google/pass with its
// expiry, now plus the connection's pass_lifetime_days, until RevealPass.
// It is never logged, and never written anywhere but the sealed column.
func (p *provisioner) keepPass(ctx context.Context, s session, actor contracts.Actor, userID, password string) error {
	if p.deps.Secrets == nil {
		return errors.New("google: no secrets box to seal the password with")
	}
	sealed, err := p.deps.Secrets.Seal(passPurpose, []byte(password))
	if err != nil {
		return fmt.Errorf("google: seal the password: %w", err)
	}
	now := p.now()
	expires := now.Add(time.Duration(s.conn.PassLifetimeDays) * 24 * time.Hour)
	return db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.SetAccountPass(ctx, store.SetAccountPassParams{PassSealed: sealed, PassExpiresAt: &expires, Now: now, UserID: userID}); err != nil {
			return fmt.Errorf("google: keep the password: %w", err)
		}
		return recordEvent(ctx, q, now, actor, actionPassIssued, userID, []string{"expires_at:" + expires.UTC().Format(time.RFC3339)})
	})
}

// RevealPass answers the user's first password once (contracts.
// IdentityProvisioner.RevealPass): taken and cleared in one statement, so two
// reveals cannot both have it; opened and answered with its expiry; audited
// google.pass.revealed with the actor and the user's id, never the password.
// ErrPassGone when none waits, or when its lifetime has ended — cleared all
// the same, with every other expired one. It reads the module's own rows
// alone: no call to Google.
func (p *provisioner) RevealPass(ctx context.Context, actor contracts.Actor, externalID string) (contracts.Pass, error) {
	if !actor.Valid() {
		return contracts.Pass{}, contracts.ErrInvalidActor
	}
	if !validUserID(externalID) {
		return contracts.Pass{}, contracts.ErrPassGone
	}
	if p.deps.Secrets == nil {
		return contracts.Pass{}, errors.New("google: no secrets box to open the password with")
	}
	now := p.now()
	var (
		pass    contracts.Pass
		expired bool
	)
	if err := store.New(p.deps.Pool).ClearExpiredPasses(ctx, now); err != nil {
		return contracts.Pass{}, fmt.Errorf("google: forget the expired passwords: %w", err)
	}
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		taken, err := q.TakeAccountPass(ctx, store.TakeAccountPassParams{Now: now, UserID: externalID})
		if errors.Is(err, pgx.ErrNoRows) {
			return contracts.ErrPassGone
		} else if err != nil {
			return fmt.Errorf("google: take the password: %w", err)
		}
		if taken.TakenExpiresAt == nil || !taken.TakenExpiresAt.After(now) {
			expired = true
			return nil
		}
		value, err := p.deps.Secrets.Open(passPurpose, taken.TakenSealed)
		if err != nil {
			return fmt.Errorf("google: open the password: %w", err)
		}
		if err := recordEvent(ctx, q, now, actor, actionPassRevealed, externalID,
			[]string{"expires_at:" + taken.TakenExpiresAt.UTC().Format(time.RFC3339)}); err != nil {
			return err
		}
		pass = contracts.Pass{Value: string(value), ExpiresAt: *taken.TakenExpiresAt}
		return nil
	})
	switch {
	case errors.Is(err, contracts.ErrPassGone):
		return contracts.Pass{}, contracts.ErrPassGone
	case err != nil:
		return contracts.Pass{}, err
	case expired:
		return contracts.Pass{}, contracts.ErrPassGone
	}
	return pass, nil
}
