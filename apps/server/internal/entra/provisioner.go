package entra

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The provider's key — the identity_links.provider value Staff records — and
// its display name.
const (
	providerKey  = "entra"
	providerName = "Microsoft Entra ID"
)

// The lifecycle's event actions (the plan's "Exact values").
const (
	actionUserPrepared    = "entra.user.prepared"
	actionUserActivated   = "entra.user.activated"
	actionUserDeactivated = "entra.user.deactivated"
	actionUserUpdated     = "entra.user.updated"
	actionUserDeleted     = "entra.user.deleted"
	actionPassIssued      = "entra.pass.issued"
	actionPassRevealed    = "entra.pass.revealed"
)

// The account states entra.accounts and ExternalAccount.State name.
const (
	stateEnabled  = "enabled"
	stateDisabled = "disabled"
	stateDeleted  = "deleted"
)

// The refusal codes kept as an account's last_error beside the reported
// privileged; each is also the event's "refused:" code.
const (
	refusedPrivileged   = contracts.ReportedPrivileged
	refusedSynced       = "synced_on_premises"
	refusedAmbiguous    = "ambiguous"
	refusedAddressTaken = "address_taken"
	refusedEnabled      = "enabled"
)

// provisioner is the module's contracts.IdentityProvisioner (Entra design D6,
// D13): Staff's decisions executed against the tenant through client. Its
// status is read from the connection's row (status.go); its lifecycle is
// lifecycle_*.go. Every write checks the actor first, then takes the
// connection and a credential on the pool (open), makes its Graph calls on
// no transaction, and writes its rows and its event in short transactions
// after the calls. The groups are delivery D's and answer
// ErrGroupNotManageable.
type provisioner struct {
	deps   module.Deps
	client *graphClient
}

var _ contracts.IdentityProvisioner = (*provisioner)(nil)

// newProvisioner builds the module's provider over d, for Compose and Workers
// alike.
func newProvisioner(d module.Deps) contracts.IdentityProvisioner {
	return &provisioner{deps: d, client: newGraphClient(d)}
}

// Key is "entra", with no context and no network.
func (p *provisioner) Key() string { return providerKey }

// Provider names the provider and its status, read from the connection's
// row and the secrets with no network (plan reading 2): Staff's GET /meta
// asks it on every request.
func (p *provisioner) Provider(ctx context.Context) (contracts.ProviderInfo, error) {
	return providerInfo(ctx, store.New(p.deps.Pool), p.client.now())
}

// session is one call's footing: the queries on the pool, the connection's
// row and the credential the calls to the tenant are made with.
type session struct {
	q    *store.Queries
	conn store.EntraConnection
	cred graphCredential
}

// open is a call's footing. connected is every write but Deactivate: it
// needs the connection connected (Provider's status), as Staff's own
// decisions do, and answers ErrProviderUnavailable otherwise. Deactivate and
// Account run on any working credential, whatever the status says (the
// contract's Deactivate; Staff reads Account before it decides a
// deactivation). No tenant or client, or no secret that fetches a token, is
// ErrProviderUnavailable either way; a tenant that did not answer is a
// failure to retry.
func (p *provisioner) open(ctx context.Context, connected bool) (session, error) {
	q := store.New(p.deps.Pool)
	if connected {
		info, err := providerInfo(ctx, q, p.client.now())
		if err != nil {
			return session{}, err
		}
		if info.Status != contracts.ProviderConnected {
			return session{}, fmt.Errorf("entra: the connection is %s: %w", info.Status, contracts.ErrProviderUnavailable)
		}
	}
	cred, err := credentialFor(ctx, q, p.client)
	switch {
	case errors.Is(err, errNotConfigured), errors.Is(err, errNoCredential):
		return session{}, fmt.Errorf("%w: %w", contracts.ErrProviderUnavailable, err)
	case err != nil:
		return session{}, err
	}
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return session{}, fmt.Errorf("entra: read the connection: %w", err)
	}
	return session{q: q, conn: conn, cred: cred}, nil
}

// refusedError is the tenant refusing a write in a way the contract names:
// the code kept on the account (last_error, and reported for privileged),
// whether it was a sensitive action (the streak, plan reading 4), and the
// sentinel the caller answers.
type refusedError struct {
	code      string
	sensitive bool
	report    bool
	sentinel  error
}

func (e *refusedError) Error() string { return "entra: refused " + e.code + ": " + e.sentinel.Error() }
func (e *refusedError) Unwrap() error { return e.sentinel }

// privilegedRefusal is a 403 on a sensitive action: the user holds a role
// the application may not write to, or the application lacks the User
// Administrator role — the 403 cannot tell which (plan reading 4).
func privilegedRefusal() *refusedError {
	return &refusedError{code: refusedPrivileged, sensitive: true, report: true, sentinel: contracts.ErrExternalPrivileged}
}

// syncedRefusal is a user synchronised from on-premises Active Directory.
func syncedRefusal() *refusedError {
	return &refusedError{code: refusedSynced, sentinel: contracts.ErrExternalSyncedOnPremises}
}

// asRefused is err's *refusedError, when it is one.
func asRefused(err error) (*refusedError, bool) {
	var r *refusedError
	ok := errors.As(err, &r)
	return r, ok
}

// refused records a refusal of a write by actor on the user objectID (nil
// when there is none yet) and answers its sentinel: the account's
// last_error and, for privileged, its reported codes; the streak one longer
// for a sensitive action; and the write's event with the code. A record
// that could not be written is the answer instead, a failure to retry.
func (p *provisioner) refused(ctx context.Context, s session, actor contracts.Actor, action string, objectID *uuid.UUID, upn string, r *refusedError) error {
	now := p.client.now()
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if objectID != nil {
			row, err := q.GetAccount(ctx, *objectID)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return fmt.Errorf("entra: read the account: %w", err)
			default:
				reported := slices.Clone(row.Reported)
				if r.report && !slices.Contains(reported, r.code) {
					reported = append(reported, r.code)
				}
				code := r.code
				if _, err := q.SetAccountState(ctx, store.SetAccountStateParams{
					State: row.State, Reported: orEmpty(reported), LastError: &code, Now: now, ObjectID: row.ObjectID,
				}); err != nil {
					return fmt.Errorf("entra: record the refusal: %w", err)
				}
			}
		}
		if r.sensitive {
			if _, err := q.AddSensitiveDenied(ctx); err != nil {
				return fmt.Errorf("entra: count the sensitive refusal: %w", err)
			}
		}
		return recordEvent(ctx, q, now, actor, action, objectID, upn, []string{"refused:" + r.code})
	})
	if err != nil {
		return err
	}
	return r.sentinel
}

// sensitiveSucceeded ends the streak: a sensitive action the tenant allowed
// shows the application holds its role.
func (p *provisioner) sensitiveSucceeded(ctx context.Context, s session) error {
	if err := s.q.ResetSensitiveDenied(ctx); err != nil {
		return fmt.Errorf("entra: end the sensitive streak: %w", err)
	}
	return nil
}

// settle records what a write left the account as — its state and the
// codes it reports, its refusal cleared — and the write's event with codes,
// in one short transaction, and answers the row (as it would be, for a user
// the module keeps no row for).
func (p *provisioner) settle(ctx context.Context, s session, actor contracts.Actor, action string, objectID uuid.UUID, upn, state string, reported, codes []string) (store.EntraAccount, error) {
	now := p.client.now()
	var row store.EntraAccount
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		row, err = q.SetAccountState(ctx, store.SetAccountStateParams{
			State: state, Reported: orEmpty(reported), LastError: nil, Now: now, ObjectID: objectID,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// A user the module keeps no row for: the event alone.
			row = store.EntraAccount{ObjectID: objectID, UserPrincipalName: upn, State: state, Reported: orEmpty(reported)}
		case err != nil:
			return fmt.Errorf("entra: record the account: %w", err)
		}
		return recordEvent(ctx, q, now, actor, action, &objectID, upn, codes)
	})
	return row, err
}

// account is the answer for user u and its row: the state from Graph's
// accountEnabled (deleted when the row says so), the address the module
// made or found (mail when Graph has one, else the UPN), the pass waiting
// on the row, and the codes the row reports.
func account(u graphUser, row *store.EntraAccount, now time.Time) contracts.ExternalAccount {
	out := contracts.ExternalAccount{
		ExternalID: u.ID, UserPrincipalName: u.UserPrincipalName, Email: u.UserPrincipalName,
		State: stateDisabled, Reported: []string{},
	}
	if u.Mail != nil && *u.Mail != "" {
		out.Email = *u.Mail
	}
	if u.AccountEnabled {
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

// livePass reports whether row holds a pass not yet revealed whose lifetime
// is ahead of now.
func livePass(row store.EntraAccount, now time.Time) bool {
	return row.PassSealed != nil && row.PassExpiresAt != nil && row.PassExpiresAt.After(now)
}

// GroupTraits declares the traits a group may carry for Entra (design D10):
// its type, which D's EnsureGroup creates and never changes, and its mail
// nickname. Staff renders the form from this alone.
func (p *provisioner) GroupTraits() []contracts.GroupTrait {
	return []contracts.GroupTrait{
		{
			Key:   "type",
			Type:  "choice",
			Label: map[string]string{"en": "Group type", "nb": "Gruppetype"},
			Description: map[string]string{
				"en": "A security group grants access to apps and resources; a Microsoft 365 group adds a shared mailbox, calendar and files, and can become a team.",
				"nb": "En sikkerhetsgruppe gir tilgang til apper og ressurser; en Microsoft 365-gruppe gir i tillegg felles postboks, kalender og filer, og kan bli et team.",
			},
			Choices: []contracts.TraitChoice{
				{Value: "security", Label: map[string]string{"en": "Security", "nb": "Sikkerhet"}},
				{Value: "microsoft365", Label: map[string]string{"en": "Microsoft 365", "nb": "Microsoft 365"}},
			},
			DocURL:   "https://learn.microsoft.com/en-us/entra/fundamentals/concept-learn-about-groups",
			Default:  "security",
			Required: true,
		},
		{
			Key:   "mailNickname",
			Type:  "string",
			Label: map[string]string{"en": "Mail nickname", "nb": "E-postalias"},
			Description: map[string]string{
				"en": "The group's alias in the tenant, at most 64 characters; made from the group's key when left empty.",
				"nb": "Gruppens alias i tenanten, høyst 64 tegn; lages av gruppens nøkkel når feltet er tomt.",
			},
			DocURL: "https://learn.microsoft.com/en-us/graph/api/resources/group?view=graph-rest-1.0",
		},
	}
}

// EnsureGroup answers ErrGroupNotManageable: the module manages no group in
// delivery C; delivery D (Entra design D14) creates and updates them.
func (p *provisioner) EnsureGroup(context.Context, contracts.Actor, contracts.GroupSpec) (contracts.ExternalGroup, error) {
	return contracts.ExternalGroup{}, contracts.ErrGroupNotManageable
}

// SetMembers answers ErrGroupNotManageable until delivery D (design D14).
func (p *provisioner) SetMembers(context.Context, contracts.Actor, string, []string) (contracts.MembersResult, error) {
	return contracts.MembersResult{}, contracts.ErrGroupNotManageable
}

// DeleteGroup answers ErrGroupNotManageable until delivery D (design D14).
func (p *provisioner) DeleteGroup(context.Context, contracts.Actor, string) error {
	return contracts.ErrGroupNotManageable
}
