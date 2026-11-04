package google

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the provisioner (Google Workspace design G17): the module's
// contracts.IdentityProvisioner, Staff's decisions executed in the
// organisation's directory as the admin to impersonate. Its status is read
// from the connection's rows with no network; its lifecycle is
// lifecycle_*.go, the user as Google has it directory.go, the licences
// licences.go, the groups groups.go and groups_traits.go, the photo
// photo.go. Every write checks the actor first, then takes its footing on
// the pool (open), makes its calls to Google on no transaction, and writes
// its rows and its event in short transactions after the calls. Staff's
// accounts worker reads each link's Account on its own cycle, so the module
// runs no worker of its own (G22).

// The provider's key — the identity_links.provider value Staff records and
// the account kind HR picks — and its display name.
const (
	providerKey  = "google"
	providerName = "Google Workspace"
)

// The scopes delivery B needs, each asked of the token endpoint as the admin
// to impersonate (G15's table).
const (
	scopeUsers          = scopePrefix + "admin.directory.user"
	scopeGroups         = scopePrefix + "admin.directory.group"
	scopeGroupsSettings = scopePrefix + "apps.groups.settings"
	scopeLicensing      = scopePrefix + "apps.licensing"
)

// accountScopes are the scopes without which the provisioner cannot do
// Staff's work: Provider answers failing, scopes_missing, while the last
// test was refused one of them.
var accountScopes = []string{scopeUsers, scopeGroups, scopeGroupsSettings, scopeLicensing}

// reasonScopesMissing: the last test was refused a scope the accounts need
// (Provider's failing reason).
const reasonScopesMissing = "scopes_missing"

// The lifecycle's event actions.
const (
	actionUserPrepared             = "google.user.prepared"
	actionUserActivated            = "google.user.activated"
	actionUserDeactivated          = "google.user.deactivated"
	actionUserUpdated              = "google.user.updated"
	actionUserDeleted              = "google.user.deleted"
	actionPassIssued               = "google.pass.issued"
	actionPassRevealed             = "google.pass.revealed"
	actionUserPhotoSet             = "google.user.photo_set"
	actionUserPhotoCleared         = "google.user.photo_cleared"
	actionAccountAdoptionConfirmed = "google.account.adoption_confirmed"
)

// The account states google.accounts and ExternalAccount.State name.
const (
	stateEnabled  = "enabled"
	stateDisabled = "disabled"
	stateDeleted  = "deleted"
)

// The refusal codes kept as an account's last_error; each is also the
// event's "refused:" code.
const (
	refusedPrivileged   = contracts.ReportedPrivileged
	refusedAmbiguous    = "ambiguous"
	refusedAddressTaken = "address_taken"
	refusedEnabled      = "enabled"
)

// The codes the provisioner reports beside the contract's
// (ExternalAccount.Reported).
const (
	// reportedAdoptionUnconfirmed: the user is one the module found holding
	// the employee number, and no holder of google:manage has confirmed its
	// adoption; nothing is written to it until one does.
	reportedAdoptionUnconfirmed = "adoption_unconfirmed"
	// reportedLeaverStepFailed: a leaver's step after the suspension failed;
	// the step's name follows the colon (remove_licences, remove_groups,
	// sign_out).
	reportedLeaverStepFailed = "leaver_step_failed"
	// reportedSignOutRefused: Google refused to sign the leaver out (U19);
	// the account is suspended all the same, which ends the sessions at
	// their next check.
	reportedSignOutRefused = "sign_out_refused"
	// reportedPassNotSetUsed: Activate wanted a first password for a user
	// someone has signed in to, and set none — a new password there would
	// be a way into an account in use; the suspension is lifted all the
	// same, and HR or an admin resets the password by hand if it is needed.
	reportedPassNotSetUsed = "pass_not_set_used"
)

// userIDPattern is a Google user id: digits (21 of them, in practice).
var userIDPattern = regexp.MustCompile(`^[0-9]{1,32}$`)

// validUserID reports whether s is a Google user id.
func validUserID(s string) bool { return userIDPattern.MatchString(s) }

// provisioner is the module's contracts.IdentityProvisioner over the
// module's rows and keyring.
type provisioner struct {
	deps module.Deps
	keys *keyring
}

var _ contracts.IdentityProvisioner = (*provisioner)(nil)

// newProvisioner builds the module's provider over d, for Compose and
// Workers alike.
func newProvisioner(d module.Deps) contracts.IdentityProvisioner {
	return &provisioner{deps: d, keys: newKeyring(store.New(d.Pool), newGoogleClient(d))}
}

// now is the module's clock.
func (p *provisioner) now() time.Time { return p.keys.c.now() }

// Key is "google", with no context and no network.
func (p *provisioner) Key() string { return providerKey }

// Provider names the provider and its status, read from the connection's
// rows with no network: Staff's GET /meta asks it on every request.
func (p *provisioner) Provider(ctx context.Context) (contracts.ProviderInfo, error) {
	q := store.New(p.deps.Pool)
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return contracts.ProviderInfo{}, fmt.Errorf("google: read the connection: %w", err)
	}
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return contracts.ProviderInfo{}, fmt.Errorf("google: read the credentials: %w", err)
	}
	status, reason := providerStatus(conn, creds)
	return contracts.ProviderInfo{Key: providerKey, Name: providerName, Status: status, Reason: reason}, nil
}

// providerStatus is the connection as Staff reads it: unconfigured while no
// key or no admin is set, or while the delegation is pending
// (not_delegated); failing with A's reason when the last test failed, or
// scopes_missing while it was refused one of accountScopes; connected
// otherwise.
func providerStatus(conn store.GoogleConnection, creds []store.GoogleCredential) (string, string) {
	status, reason := statusOf(conn, creds)
	switch status {
	case statusUnconfigured:
		return contracts.ProviderUnconfigured, ""
	case statusPendingDelegation:
		return contracts.ProviderUnconfigured, reasonNotDelegated
	case statusFailing:
		return contracts.ProviderFailing, reason
	}
	if slices.ContainsFunc(accountScopes, func(s string) bool { return !slices.Contains(conn.ScopesGranted, s) }) {
		return contracts.ProviderFailing, reasonScopesMissing
	}
	return contracts.ProviderConnected, ""
}

// session is one call's footing: the queries on the pool, the connection's
// row, and the token sources of the four scopes, each as the admin to
// impersonate.
type session struct {
	q                                 *store.Queries
	conn                              store.GoogleConnection
	users, groups, settings, licences tokenSource
}

// open is a call's footing. connected is every write but Deactivate: it
// needs Provider's status connected and answers ErrProviderUnavailable
// otherwise. Deactivate and Account run whatever the status says, as long as
// a key fetches a token (the contract's Deactivate; Staff reads Account
// before it decides a deactivation).
func (p *provisioner) open(ctx context.Context, connected bool) (session, error) {
	q := store.New(p.deps.Pool)
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return session{}, fmt.Errorf("google: read the connection: %w", err)
	}
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return session{}, fmt.Errorf("google: read the credentials: %w", err)
	}
	if connected {
		if status, _ := providerStatus(conn, creds); status != contracts.ProviderConnected {
			return session{}, fmt.Errorf("google: the connection is %s: %w", status, contracts.ErrProviderUnavailable)
		}
	}
	if !configured(conn, creds) {
		return session{}, fmt.Errorf("%w: %w", contracts.ErrProviderUnavailable, errNotConfigured)
	}
	subject := *conn.AdminSubject
	return session{
		q: q, conn: conn,
		users:    p.keys.tokenSource([]string{scopeUsers}, subject),
		groups:   p.keys.tokenSource([]string{scopeGroups}, subject),
		settings: p.keys.tokenSource([]string{scopeGroupsSettings}, subject),
		licences: p.keys.tokenSource([]string{scopeLicensing}, subject),
	}, nil
}

// unavailable is err as the contract answers it: a token Google would not
// issue — no key, every key refused, the admin unknown, the scope not
// delegated — is ErrProviderUnavailable, nothing written; anything else is
// itself.
func unavailable(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, contracts.ErrProviderUnavailable):
		return err
	case errors.Is(err, errNotConfigured), errors.Is(err, errNoCredential), errors.Is(err, errCredentialInvalid),
		errors.Is(err, errSubjectInvalid), errors.Is(err, errNotDelegated):
		return fmt.Errorf("%w: %w", contracts.ErrProviderUnavailable, err)
	}
	return err
}

// refusedError is Google refusing a write in a way the contract names: the
// code kept on the account (last_error, and reported for privileged), and
// the sentinel the caller answers.
type refusedError struct {
	code     string
	report   bool
	sentinel error
}

func (e *refusedError) Error() string { return "google: refused " + e.code + ": " + e.sentinel.Error() }
func (e *refusedError) Unwrap() error { return e.sentinel }

// privilegedRefusal is a user holding an administrator role, or a 403 on a
// write to one user: the module does not write to an administrator.
func privilegedRefusal() *refusedError {
	return &refusedError{code: refusedPrivileged, report: true, sentinel: contracts.ErrExternalPrivileged}
}

// refused records a refusal of a write by actor on the user userID ("" when
// there is none yet) and answers its sentinel: the account's last_error and,
// for privileged, its reported codes, and the write's event with the code.
func (p *provisioner) refused(ctx context.Context, s session, actor contracts.Actor, action, userID string, r *refusedError) error {
	now := p.now()
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if userID != "" {
			row, err := q.GetAccount(ctx, userID)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return fmt.Errorf("google: read the account: %w", err)
			default:
				reported := slices.Clone(row.Reported)
				if r.report && !slices.Contains(reported, r.code) {
					reported = append(reported, r.code)
				}
				code := r.code
				if _, err := q.SetAccountState(ctx, store.SetAccountStateParams{
					State: row.State, Reported: orEmpty(reported), LastError: &code, Now: now, UserID: row.UserID,
				}); err != nil {
					return fmt.Errorf("google: record the refusal: %w", err)
				}
			}
		}
		return recordEvent(ctx, q, now, actor, action, userID, []string{"refused:" + r.code})
	})
	if err != nil {
		return err
	}
	return r.sentinel
}

// settle records what a write left the account as — its state and the
// codes it reports, its refusal cleared — and the write's event with codes,
// in one short transaction, and answers the row (as it would be, for a user
// the module keeps no row for).
func (p *provisioner) settle(ctx context.Context, s session, actor contracts.Actor, action string, u directoryUser, state string, reported, codes []string) (store.GoogleAccount, error) {
	now := p.now()
	var row store.GoogleAccount
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		row, err = q.SetAccountState(ctx, store.SetAccountStateParams{
			State: state, Reported: orEmpty(reported), LastError: nil, Now: now, UserID: u.ID,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			row = store.GoogleAccount{UserID: u.ID, PrimaryEmail: u.PrimaryEmail, State: state, Reported: orEmpty(reported)}
		case err != nil:
			return fmt.Errorf("google: record the account: %w", err)
		}
		return recordEvent(ctx, q, now, actor, action, u.ID, codes)
	})
	return row, err
}

// account is the answer for user u and its row: the state from Google's
// suspended (deleted when the row says so), the primary email, the password
// waiting on the row, and the codes the row reports.
func account(u directoryUser, row *store.GoogleAccount, now time.Time) contracts.ExternalAccount {
	out := contracts.ExternalAccount{
		ExternalID: u.ID, UserPrincipalName: u.PrimaryEmail, Email: u.PrimaryEmail,
		State: stateDisabled, Reported: []string{},
	}
	if !u.Suspended {
		out.State = stateEnabled
	}
	if row != nil {
		if row.State == stateDeleted {
			out.State = stateDeleted
		}
		out.HasPass = livePass(*row, now)
		out.Reported = slices.Clone(orEmpty(row.Reported))
	}
	return out
}

// accountState is the state u and its row stand at: deleted when the row
// says so, else as Google's suspended says.
func accountState(u directoryUser, row store.GoogleAccount) string {
	switch {
	case row.State == stateDeleted:
		return stateDeleted
	case u.Suspended:
		return stateDisabled
	}
	return stateEnabled
}

// livePass reports whether row holds a password not yet revealed whose
// lifetime is ahead of now.
func livePass(row store.GoogleAccount, now time.Time) bool {
	return row.PassSealed != nil && row.PassExpiresAt != nil && row.PassExpiresAt.After(now)
}

// trusted reports whether the module may write to row's user: one it
// created, or one it found whose adoption a holder of google:manage
// confirmed.
func trusted(row store.GoogleAccount) bool {
	return row.CreatedByUs || row.AdoptionConfirmedAt != nil
}

// unconfirmed refuses a write to u, an adopted user nobody has confirmed,
// with contracts.ErrExternalUnconfirmed: nothing written to Google; the row
// keeps the state Google answers and adoption_unconfirmed among its codes;
// the write's event carries codes and refused:adoption_unconfirmed.
func (p *provisioner) unconfirmed(ctx context.Context, s session, actor contracts.Actor, action string, u directoryUser, row store.GoogleAccount, codes ...string) error {
	reported := mergeCodes(row.Reported, func(c string) bool { return c == reportedAdoptionUnconfirmed }, []string{reportedAdoptionUnconfirmed})
	now := p.now()
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		_, err := q.SetAccountState(ctx, store.SetAccountStateParams{
			State: accountState(u, row), Reported: orEmpty(reported), LastError: row.LastError, Now: now, UserID: u.ID,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("google: record the account: %w", err)
		}
		return recordEvent(ctx, q, now, actor, action, u.ID, append(codes, "refused:"+reportedAdoptionUnconfirmed))
	})
	if err != nil {
		return err
	}
	return contracts.ErrExternalUnconfirmed
}

// codeSuffix is what may follow a reported code's colon (Staff's bound).
var codeSuffix = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// mergeCodes is old without the codes evaluated says this call judged, with
// fresh added: a call replaces what it knows about and keeps the rest.
func mergeCodes(old []string, evaluated func(string) bool, fresh []string) []string {
	out := []string{}
	for _, c := range old {
		if !evaluated(c) && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, c := range fresh {
		if c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// updateJudges are the codes Prepare's update path and Update judge.
func updateJudges(c string) bool {
	return c == contracts.ReportedManagerNotFound || c == contracts.ReportedPrivileged
}

// licenceJudges are the codes a reconcile of the licences judges.
func licenceJudges(c string) bool {
	return c == contracts.ReportedLicenceUnavailable || strings.HasPrefix(c, contracts.ReportedLicenceUnavailable+":")
}

// activateJudges are the codes Activate judges: the password, the licences,
// the role, and a leaver's steps a rehire leaves behind.
func activateJudges(c string) bool {
	return c == contracts.ReportedPassNotAllowed || c == reportedPassNotSetUsed || c == contracts.ReportedPrivileged || licenceJudges(c) ||
		c == reportedSignOutRefused || strings.HasPrefix(c, reportedLeaverStepFailed+":")
}

// leaverJudges are the codes Deactivate judges: its own steps'.
func leaverJudges(c string) bool {
	return c == reportedSignOutRefused || strings.HasPrefix(c, reportedLeaverStepFailed+":")
}

// photoJudges are the codes SetPhoto and ClearPhoto judge.
func photoJudges(c string) bool { return c == contracts.ReportedPhotoNotWritten }

// changedCodes are the names an update wrote, as event codes.
func changedCodes(changed []string) []string {
	out := make([]string, 0, len(changed))
	for _, c := range changed {
		out = append(out, "changed:"+c)
	}
	return out
}
