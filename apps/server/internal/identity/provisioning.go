package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Identity's contracts.AccountProvisioning (staff foundation design S25;
// staff accounts plan, Task 2b): the one way Staff writes identity's users.
// (account.go is the signed-in user's own account; this file is Staff's.)
//
// Every write names a Valid actor (ErrInvalidActor first, before anything
// is read or written) and runs in one serializable transaction of its own on
// identity's pool, retried from a fresh snapshot while it loses a race:
// the recorded authority mode is read (ErrModeMismatch when this process's
// configuration derives another; Deactivate alone reads none), then the owner lock is taken — any write
// may meet an Owner, and taking it always keeps one lock order — then the
// user's invitations FOR UPDATE, then the user FOR UPDATE, as acceptance
// orders them. Mail is sent only after the commit. Each change is audited
// with the actor: a user as actor_user_id, a system as a null actor with
// details.actor and no correlation id. The provider holds no state of its
// own — no cache, no memory of an earlier call — so any two instances, in an
// API process or a worker, answer the same.

// maxProvisionedInvitations is how many delivered invitations one pending
// spell gets before Activate stops issuing (InvitationExhausted).
const maxProvisionedInvitations = 3

// errInvalidSpec refuses an AccountSpec identity cannot store: a caller's
// bug (Staff validates the same rules), never retried into success.
var errInvalidSpec = errors.New("identity: the account spec is invalid")

// provisioning is the provider. access is identity's own, whose catalog —
// set at identity's mount, absent in a worker-only process — widens the
// denied keys (provisioningDeniedKeys) and whose stamp rotation ends a
// deactivated user's sessions.
type provisioning struct {
	access *Access
	deps   module.Deps
}

var _ contracts.AccountProvisioning = (*provisioning)(nil)

// newProvisioning is Module's Accounts over a and d.
func newProvisioning(a *Access, d module.Deps) contracts.AccountProvisioning {
	return &provisioning{access: a, deps: d}
}

// provisionedUser is a user as the provider judges them: GetProvisioningUser
// and LockProvisioningUser's row.
type provisionedUser = store.GetProvisioningUserRow

// cleanSpec is an AccountSpec as identity stores it.
type cleanSpec struct {
	email, displayName, number string
	language                   *string
}

// validSpec trims s and judges it: the employee number in Staff's alphabet
// (normalizeEmployeeNumber) and the language en, nb or none, always; the
// address (one '@', at most 256 characters) and the display name (1 to 200
// characters) only when full, which a local create and a local update use.
func validSpec(s contracts.AccountSpec, full bool) (cleanSpec, error) {
	out := cleanSpec{email: strings.TrimSpace(s.Email), displayName: strings.TrimSpace(s.DisplayName)}
	number, ok := normalizeEmployeeNumber(s.EmployeeNumber)
	if !ok {
		return out, fmt.Errorf("%w: the employee number", errInvalidSpec)
	}
	out.number = number
	switch lang := strings.TrimSpace(s.PreferredLanguage); lang {
	case "":
	case "en", "nb":
		out.language = &lang
	default:
		return out, fmt.Errorf("%w: the language", errInvalidSpec)
	}
	if !full {
		return out, nil
	}
	if !emailAddressValid(out.email) || len(out.email) > maxManagedEmailLength {
		return out, fmt.Errorf("%w: the address", errInvalidSpec)
	}
	if n := utf16Length(out.displayName); n == 0 || n > maxDisplayNameLength {
		return out, fmt.Errorf("%w: the display name", errInvalidSpec)
	}
	return out, nil
}

// auditActor is how actor is written to an audit row.
func auditActor(actor contracts.Actor) (*uuid.UUID, string) {
	if actor.UserID != nil {
		id := *actor.UserID
		return &id, ""
	}
	return nil, actor.System
}

// write runs fn for actor in one serializable transaction, retried while it
// loses a race, with the mode and the clock, under the owner lock. An
// invalid actor is refused before the transaction begins.
func (p *provisioning) write(ctx context.Context, actor contracts.Actor, fn func(q *store.Queries, mode string, now time.Time) error) error {
	return p.writeIn(ctx, actor, true, fn)
}

// writeIn is write, reading the mode only when withMode: Deactivate reads
// none, so a leaver is disabled even by a process whose configuration
// disagrees with the recorded mode (fn is then given "").
func (p *provisioning) writeIn(ctx context.Context, actor contracts.Actor, withMode bool, fn func(q *store.Queries, mode string, now time.Time) error) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	return db.RetrySerializable(ctx, serializableAttempts, func() error {
		return db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
			q := store.New(tx)
			mode := ""
			if withMode {
				var err error
				if mode, err = authorityMode(ctx, q, p.deps.Config); err != nil {
					return err
				}
			}
			if err := lockOwners(ctx, q); err != nil {
				return err
			}
			return fn(q, mode, p.deps.Clock())
		})
	})
}

// lockUser takes the user's invitations' locks, then the user's own, and
// reads them: ErrUserGone when there is no such user.
func lockUser(ctx context.Context, q *store.Queries, id uuid.UUID) (provisionedUser, error) {
	if err := q.LockUserInvitations(ctx, &id); err != nil {
		return provisionedUser{}, err
	}
	u, err := q.LockProvisioningUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return provisionedUser{}, contracts.ErrUserGone
	}
	return provisionedUser(u), err
}

// scimEnabled is whether a directory's deactivation counts.
func (p *provisioning) scimEnabled() bool {
	return p.deps.Config != nil && p.deps.Config.SCIM != nil
}

// directoryInactive reports whether the directory has deactivated u: SCIM
// is configured, the directory reports them inactive, and they are no Owner
// — no session of theirs is accepted, whatever identity's record says. The
// ref reports it; the writes act on identity's record all the same.
func (p *provisioning) directoryInactive(u provisionedUser) bool {
	return p.scimEnabled() && u.ScimInactive && !u.IsOwner
}

// spellStart is when u's current invitation spell began.
func spellStart(u provisionedUser) time.Time {
	if u.InvitationsSince != nil {
		return *u.InvitationsSince
	}
	if u.PendingSince != nil {
		return *u.PendingSince
	}
	return time.Time{}
}

// invitationState is the state of a pending user's invitations of the
// current spell, newest first, at now. Issued counts those delivered — every
// one but a failed delivery's. The latest decides: acceptable is active; an
// Owner's revoke is revoked_by_owner; otherwise, three delivered is
// exhausted; a lapsed one is expired; anything else (none yet, a failed
// delivery, a revoke for another reason) is none, for Activate to issue.
func invitationState(invs []store.IdentityInvitation, now time.Time) contracts.Invitation {
	out := contracts.Invitation{State: contracts.InvitationNone}
	for _, inv := range invs {
		if deref(inv.RevokedReason) != revokedDeliveryFailed {
			out.Issued++
		}
	}
	if len(invs) == 0 {
		return out
	}
	latest := invs[0]
	switch {
	case latest.RevokedAt == nil && latest.AcceptedAt == nil && latest.ExpiresAt.After(now):
		out.State = contracts.InvitationActive
	case deref(latest.RevokedReason) == revokedByOwner:
		out.State = contracts.InvitationRevokedByOwner
	case out.Issued >= maxProvisionedInvitations:
		out.State = contracts.InvitationExhausted
	case latest.RevokedAt == nil && latest.AcceptedAt == nil:
		out.State = contracts.InvitationExpired
	}
	return out
}

// ref is u as the contract reports them at now, read on q. A pending user
// reads pending, also while disabled (DisabledBy says by whom); a
// directory-deactivated non-Owner under SCIM reads disabled by the
// directory. EmailDiffers compares want with identity's address as identity
// compares addresses, and is false for a placeholder address and for no want.
func (p *provisioning) ref(ctx context.Context, q *store.Queries, u provisionedUser, want string, now time.Time) (contracts.AccountRef, error) {
	ref := contracts.AccountRef{
		UserID:         u.ID,
		Managed:        u.ManagedBy != nil,
		Privileged:     u.Privileged,
		EmployeeNumber: deref(u.EmployeeNumber),
		HasCredential:  u.HasPassword || u.HasPasskey || u.HasOidcLink,
		Invitation:     contracts.Invitation{State: contracts.InvitationNone},
	}
	switch {
	case p.directoryInactive(u):
		ref.State, ref.DisabledBy = contracts.AccountStateDisabled, contracts.DisabledByDirectory
	case u.PendingSince != nil:
		ref.State = contracts.AccountStatePending
		if u.IsDisabled {
			ref.DisabledBy = deref(u.DisabledBy)
		}
	case u.IsDisabled:
		ref.State, ref.DisabledBy = contracts.AccountStateDisabled, deref(u.DisabledBy)
	default:
		ref.State = contracts.AccountStateActive
	}
	if want = strings.TrimSpace(want); want != "" && publicEmail(u.Email) != nil {
		ref.EmailDiffers = normalizeEmail(want) != u.NormalizedEmail
	}
	if u.PendingSince != nil {
		invs, err := q.ListUserInvitationsSince(ctx, store.ListUserInvitationsSinceParams{UserID: &u.ID, Since: spellStart(u)})
		if err != nil {
			return contracts.AccountRef{}, err
		}
		ref.Invitation = invitationState(invs, now)
	}
	return ref, nil
}

// reread is the user id names as the contract reports them, on q.
func (p *provisioning) reread(ctx context.Context, q *store.Queries, id uuid.UUID, now time.Time) (contracts.AccountRef, error) {
	u, err := q.GetProvisioningUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.AccountRef{}, contracts.ErrUserGone
	}
	if err != nil {
		return contracts.AccountRef{}, err
	}
	return p.ref(ctx, q, provisionedUser(u), "", now)
}

// Mode answers the recorded authority mode (authorityMode).
func (p *provisioning) Mode(ctx context.Context) (string, error) {
	return authorityMode(ctx, store.New(p.deps.Pool), p.deps.Config)
}

// replacedInvitation is one invitation a provider revoked replaced, as its
// audit row names it.
type replacedInvitation struct {
	ID   uuid.UUID `json:"id"`
	Role string    `json:"role"`
}

// replaceInvitationsTo revokes, replaced, the open invitations to the
// normalized address an account for it replaces, and returns them for the
// audit. An Owner-role invitation still acceptable at now is an Owner's to
// accept, so the address is refused ErrAccountEmailTaken and nothing is
// revoked: staff:manage may not take the address an Owner invited an Owner
// to.
func replaceInvitationsTo(ctx context.Context, q *store.Queries, normalized string, now time.Time) ([]replacedInvitation, error) {
	open, err := q.ListOpenInvitationsForEmail(ctx, normalized)
	if err != nil {
		return nil, err
	}
	out := make([]replacedInvitation, 0, len(open))
	for _, inv := range open {
		if inv.Role == RoleOwner && inv.ExpiresAt.After(now) {
			return nil, contracts.ErrAccountEmailTaken
		}
		out = append(out, replacedInvitation{ID: inv.ID, Role: inv.Role})
	}
	if err := q.RevokeActiveInvitationsForEmail(ctx, store.RevokeActiveInvitationsForEmailParams{
		NormalizedEmail: normalized, Now: now, RevokedReason: revokedReplaced,
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// staffPreparedAudit is user.staff-prepared's after beside the user's
// snapshot: the employee number the user was created for, and the
// invitations to the address the account replaced.
type staffPreparedAudit struct {
	userSnapshot
	EmployeeNumber      string               `json:"employeeNumber"`
	ReplacedInvitations []replacedInvitation `json:"replacedInvitations"`
}

// Prepare finds the managed user holding spec's employee number, by its key,
// or in local mode creates one. A user holding the number who is not
// Staff-managed is ErrEmployeeNumberTaken; a managed Owner or SystemAdmin is
// ErrAccountPrivileged; a found user is answered as they are (Update
// changes them). In federated mode nobody holding the number is
// ErrAccountNotFound. In local mode another user holding the address,
// compared normalized, is ErrAccountEmailTaken; otherwise the user is
// created Staff-managed, pending, disabled by Staff, without a credential or
// a confirmed email, with the built-in User role (plan reading 5); the open
// invitations to the address are revoked replaced, as an Owner's create does,
// and named in the audit, user.staff-prepared — but an open Owner-role
// invitation to it refuses the address (replaceInvitationsTo); the ref it
// answers for a user it created is Created, for one it found not. A concurrent writer of
// the number or the address, seen only by the unique index, is answered as
// the same refusal.
func (p *provisioning) Prepare(ctx context.Context, actor contracts.Actor, spec contracts.AccountSpec) (contracts.AccountRef, error) {
	var ref contracts.AccountRef
	err := p.write(ctx, actor, func(q *store.Queries, mode string, now time.Time) error {
		s, err := validSpec(spec, mode == contracts.AccountModeLocal)
		if err != nil {
			return err
		}
		holder, err := q.GetUserIDByEmployeeNumber(ctx, contracts.NormalizeEmployeeNumber(s.number))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		default:
			u, err := lockUser(ctx, q, holder)
			if err != nil {
				return err
			}
			if u.ManagedBy == nil {
				return contracts.ErrEmployeeNumberTaken
			}
			if u.Privileged {
				return contracts.ErrAccountPrivileged
			}
			ref, err = p.ref(ctx, q, u, "", now)
			return err
		}
		if mode != contracts.AccountModeLocal {
			return contracts.ErrAccountNotFound
		}
		if _, taken, err := emailTaken(ctx, q, s.email); err != nil || taken {
			return cmpOr(err, contracts.ErrAccountEmailTaken)
		}
		replaced, err := replaceInvitationsTo(ctx, q, normalizeEmail(s.email), now)
		if err != nil {
			return err
		}
		id := uuid.New()
		if err := q.InsertProvisionedUser(ctx, store.InsertProvisionedUserParams{
			ID: id, Email: s.email, NormalizedEmail: normalizeEmail(s.email), DisplayName: s.displayName,
			PreferredLanguage: s.language, Now: now, Version: uuid.New(),
		}); err != nil {
			if db.IsUniqueViolation(err, "ux_users_normalized_email") {
				return contracts.ErrAccountEmailTaken
			}
			return err
		}
		if err := setStaffManaged(ctx, q, id, &s.number, now); err != nil {
			if db.IsUniqueViolation(err, "ux_users_employee_number") {
				return contracts.ErrEmployeeNumberTaken
			}
			return err
		}
		if _, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: id, RoleID: RoleUserID}); err != nil {
			return err
		}
		after, err := captureUser(ctx, q, id)
		if err != nil {
			return err
		}
		by, system := auditActor(actor)
		if err := writeAudit(ctx, q, nil, now, auditEvent{
			actor: by, system: system, targetUser: &id, action: "user.staff-prepared",
			before: bootstrapAuditState{Roles: []string{}, PermissionKeys: []string{}},
			after:  staffPreparedAudit{userSnapshot: after, EmployeeNumber: s.number, ReplacedInvitations: replaced},
		}); err != nil {
			return err
		}
		ref, err = p.reread(ctx, q, id, now)
		ref.Created = true
		return err
	})
	if err != nil {
		return contracts.AccountRef{}, err
	}
	return ref, nil
}

// Activate enables a managed user who is not privileged. An Owner's disable
// (or a disable no one recorded as Staff's) is ErrBlockedByOwner. A user
// Staff disabled is re-enabled even while the directory reports them
// inactive: identity's own record is Staff's to restore, and the directory's
// deactivation still stops their sessions while SCIM is configured.
//
// In local mode, the call that enables a user — one disabled by Staff,
// a prepared user among them — gives them the roles Staff may give, read FOR
// SHARE, as PUT /owner/provisioning-roles reads them, so none is edited
// before this commits: none named is ErrEmployeeRoleMissing, and one now
// carrying a denied key (provisioningDeniedKeys) is ErrEmployeeRoleUnsafe;
// then the user is enabled, with disabled_by cleared and the stamp rotated.
// A pending user stays pending, unable to sign in until they accept their
// invitation. A user already enabled is given nothing, so an Owner's removal
// of a role — before or after the acceptance — is not undone. A pending user
// who could complete an invitation (canComplete's conditions: no password,
// passkey or OIDC link) is then issued one naming them, unless the latest of
// their spell is open, revoked by an Owner or the third delivered
// (invitationState), and unless one naming them, of any spell, was made in
// the last ten minutes (invitationInterval). opts.FreshInvitation never
// re-sends an open invitation: otherwise, outside those ten minutes, it
// starts a new spell, so an Owner's revoke and the count are behind it;
// within them it is an ordinary call. The invitation is mailed after the commit; a failed delivery revokes
// it delivery_failed, uncounted, and fails the call.
//
// In federated mode, a user disabled by Staff is re-enabled; nothing else is
// given, issued or changed.
//
// A change is audited user.staff-activated with the user's snapshot before
// and after.
func (p *provisioning) Activate(ctx context.Context, actor contracts.Actor, userID uuid.UUID, opts contracts.ActivateOpts) (contracts.AccountRef, error) {
	var issued *store.IdentityInvitation
	var token string
	err := p.write(ctx, actor, func(q *store.Queries, mode string, now time.Time) error {
		issued, token = nil, ""
		u, err := lockUser(ctx, q, userID)
		if err != nil {
			return err
		}
		if u.ManagedBy == nil {
			return contracts.ErrAccountNotManaged
		}
		if u.Privileged {
			return contracts.ErrAccountPrivileged
		}
		if u.IsDisabled && deref(u.DisabledBy) != disabledByStaffValue {
			return contracts.ErrBlockedByOwner
		}
		local := mode == contracts.AccountModeLocal
		before, err := captureUser(ctx, q, userID)
		if err != nil {
			return err
		}
		changed := false
		if local && u.IsDisabled {
			roles, err := q.LockProvisioningRoleIDs(ctx)
			if err != nil {
				return err
			}
			if len(roles) == 0 {
				return contracts.ErrEmployeeRoleMissing
			}
			if err := p.deniedRole(ctx, q, roles); err != nil {
				return err
			}
			for _, role := range roles {
				n, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: role})
				if err != nil {
					return err
				}
				changed = changed || n > 0
			}
		}
		if u.IsDisabled {
			if err := q.SetUserDisabled(ctx, store.SetUserDisabledParams{ID: userID, IsDisabled: false, Version: uuid.New(), Now: now}); err != nil {
				return err
			}
			if err := p.access.rotateSecurityStamp(ctx, q, userID, uuid.Nil); err != nil {
				return err
			}
			changed = true
		} else if changed {
			if err := q.RotateUserVersion(ctx, store.RotateUserVersionParams{ID: userID, Version: uuid.New(), Now: now}); err != nil {
				return err
			}
		}
		if changed {
			after, err := captureUser(ctx, q, userID)
			if err != nil {
				return err
			}
			by, system := auditActor(actor)
			if err := writeAudit(ctx, q, nil, now, auditEvent{
				actor: by, system: system, targetUser: &userID, action: "user.staff-activated", before: before, after: after,
			}); err != nil {
				return err
			}
		}
		if !local || u.PendingSince == nil || u.HasPassword || u.HasPasskey || u.HasOidcLink {
			return nil
		}
		invs, err := q.ListUserInvitationsSince(ctx, store.ListUserInvitationsSinceParams{UserID: &userID, Since: spellStart(u)})
		if err != nil {
			return err
		}
		state := invitationState(invs, now).State
		if opts.FreshInvitation && state != contracts.InvitationActive {
			fresh, err := noRecentInvitation(ctx, q, userID, now)
			if err != nil {
				return err
			}
			if fresh {
				if err := q.SetInvitationsSince(ctx, store.SetInvitationsSinceParams{ID: userID, Now: now}); err != nil {
					return err
				}
				state = contracts.InvitationNone
			}
		}
		switch state {
		case contracts.InvitationNone, contracts.InvitationExpired:
		default:
			return nil
		}
		if quiet, err := noRecentInvitation(ctx, q, userID, now); err != nil || !quiet {
			return err
		}
		displayName := u.DisplayName
		inv, tok, err := issueInvitation(ctx, q, p.deps.Config.InvitationLifetime, store.InsertInvitationParams{
			Email: u.Email, NormalizedEmail: u.NormalizedEmail, Role: RoleUser, DisplayName: &displayName,
			InvitedByUserID: actor.UserID, UserID: &userID,
		}, now)
		if err != nil {
			return err
		}
		issued, token = &inv, tok
		return nil
	})
	if err != nil {
		return contracts.AccountRef{}, err
	}
	if issued != nil {
		if err := deliverInvitation(ctx, store.New(p.deps.Pool), p.deps, *issued, token); err != nil {
			return contracts.AccountRef{}, err
		}
	}
	return p.reread(ctx, store.New(p.deps.Pool), userID, p.deps.Clock())
}

// invitationInterval is the least time between two invitations naming one
// user: Activate issues none, and FreshInvitation starts no spell, while one
// was made in the last ten minutes — so neither Run now pressed again and
// again nor an address changed back and forth sends more than one invitation
// per ten minutes.
const invitationInterval = 10 * time.Minute

// noRecentInvitation reports whether no invitation naming the user was made
// within invitationInterval of now.
func noRecentInvitation(ctx context.Context, q *store.Queries, userID uuid.UUID, now time.Time) (bool, error) {
	all, err := q.ListUserInvitationsSince(ctx, store.ListUserInvitationsSinceParams{UserID: &userID, Since: time.Time{}})
	if err != nil {
		return false, err
	}
	for _, inv := range all {
		if inv.CreatedAt.Add(invitationInterval).After(now) {
			return false, nil
		}
	}
	return true, nil
}

// deniedRole refuses roles when one carries a denied key, naming them.
func (p *provisioning) deniedRole(ctx context.Context, q *store.Queries, roles []uuid.UUID) error {
	var catalog map[string]contracts.Permission
	if p.access != nil {
		catalog = p.access.catalog
	}
	row, err := q.FirstDeniedRolePermission(ctx, store.FirstDeniedRolePermissionParams{RoleIds: roles, Keys: provisioningDeniedKeys(catalog)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: the role %s carries %s", contracts.ErrEmployeeRoleUnsafe, row.Name, row.PermissionKey)
}

// Deactivate disables a managed user for Staff. The user must be managed
// (ErrAccountNotManaged), not the last active Owner (ErrLastOwner), not an
// Owner or a SystemAdmin (ErrAccountPrivileged). An enabled user is disabled
// with disabled_by staff, their sessions and reset links ended, audited
// user.staff-deactivated — whatever the directory says, since a leaver must
// lose access whatever the identity provider does (SCIM never writes
// is_disabled, and its deactivation counts only while SCIM is configured);
// an already disabled one keeps who disabled them. Deactivate reads no
// authority mode, so it disables even where this process's configuration
// disagrees with the recorded mode.
// Every invitation naming the user that could still be accepted is revoked
// deactivated. Again, it changes nothing.
func (p *provisioning) Deactivate(ctx context.Context, actor contracts.Actor, userID uuid.UUID) (contracts.AccountRef, error) {
	var ref contracts.AccountRef
	err := p.writeIn(ctx, actor, false, func(q *store.Queries, _ string, now time.Time) error {
		u, err := lockUser(ctx, q, userID)
		if err != nil {
			return err
		}
		if u.ManagedBy == nil {
			return contracts.ErrAccountNotManaged
		}
		last, err := q.IsLastActiveOwner(ctx, store.IsLastActiveOwnerParams{UserID: userID, Now: now})
		if err != nil {
			return err
		}
		if last {
			return contracts.ErrLastOwner
		}
		if u.Privileged {
			return contracts.ErrAccountPrivileged
		}
		if !u.IsDisabled {
			before, err := captureUser(ctx, q, userID)
			if err != nil {
				return err
			}
			staff := disabledByStaffValue
			if err := q.SetUserDisabled(ctx, store.SetUserDisabledParams{ID: userID, IsDisabled: true, DisabledBy: &staff, Version: uuid.New(), Now: now}); err != nil {
				return err
			}
			if err := p.access.rotateSecurityStamp(ctx, q, userID, uuid.Nil); err != nil {
				return err
			}
			after := before
			after.IsDisabled = true
			by, system := auditActor(actor)
			if err := writeAudit(ctx, q, nil, now, auditEvent{
				actor: by, system: system, targetUser: &userID, action: "user.staff-deactivated", before: before, after: after,
			}); err != nil {
				return err
			}
		}
		if err := q.RevokeUserInvitations(ctx, store.RevokeUserInvitationsParams{UserID: &userID, Now: now, RevokedReason: revokedDeactivated}); err != nil {
			return err
		}
		ref, err = p.reread(ctx, q, userID, now)
		return err
	})
	if err != nil {
		return contracts.AccountRef{}, err
	}
	return ref, nil
}

// staffUpdatedAudit is user.staff-updated's before and after: what Update
// may change, the address only as whether it changed.
type staffUpdatedAudit struct {
	DisplayName       string  `json:"displayName"`
	PreferredLanguage *string `json:"preferredLanguage"`
	EmployeeNumber    *string `json:"employeeNumber"`
	EmailChanged      bool    `json:"emailChanged"`
	// ReplacedInvitations, on the after only, are the invitations to the new
	// address the change revoked replaced.
	ReplacedInvitations []replacedInvitation `json:"replacedInvitations,omitempty"`
}

// Update writes a managed user (ErrAccountNotManaged) to spec. In local mode
// the display name, the language and the employee number, and — only while
// the user is pending (the owner's answer 1) — the address: another user
// holding it is ErrAccountEmailTaken; the new address revokes the user's
// acceptable invitations email_changed and an Owner's unnamed ones to the new
// address replaced (named in the audit; an open Owner-role one refuses the
// address, replaceInvitationsTo), and starts a new invitation spell (the next
// Activate invites at the new address). A managed Owner or SystemAdmin is
// ErrAccountPrivileged. In federated mode the language and the
// employee number only. A number another user holds, by its key, is
// ErrEmployeeNumberTaken. A change is audited user.staff-updated; the same
// values again change nothing.
func (p *provisioning) Update(ctx context.Context, actor contracts.Actor, userID uuid.UUID, spec contracts.AccountSpec) (contracts.AccountRef, error) {
	var ref contracts.AccountRef
	err := p.write(ctx, actor, func(q *store.Queries, mode string, now time.Time) error {
		local := mode == contracts.AccountModeLocal
		s, err := validSpec(spec, local)
		if err != nil {
			return err
		}
		u, err := lockUser(ctx, q, userID)
		if err != nil {
			return err
		}
		if u.ManagedBy == nil {
			return contracts.ErrAccountNotManaged
		}
		if u.Privileged {
			return contracts.ErrAccountPrivileged
		}
		next := store.UpdateProvisionedUserParams{
			ID: userID, DisplayName: u.DisplayName, PreferredLanguage: s.language, Email: u.Email,
			NormalizedEmail: u.NormalizedEmail, InvitationsSince: u.InvitationsSince, Version: uuid.New(), Now: now,
		}
		emailChanged := false
		var replaced []replacedInvitation
		if local {
			next.DisplayName = s.displayName
			if u.PendingSince != nil && normalizeEmail(s.email) != u.NormalizedEmail {
				if _, taken, err := emailTaken(ctx, q, s.email); err != nil || taken {
					return cmpOr(err, contracts.ErrAccountEmailTaken)
				}
				next.Email, next.NormalizedEmail, next.InvitationsSince = s.email, normalizeEmail(s.email), &now
				emailChanged = true
				if err := q.RevokeUserInvitations(ctx, store.RevokeUserInvitationsParams{UserID: &userID, Now: now, RevokedReason: revokedEmailChanged}); err != nil {
					return err
				}
				if replaced, err = replaceInvitationsTo(ctx, q, next.NormalizedEmail, now); err != nil {
					return err
				}
			}
		}
		fields := emailChanged || next.DisplayName != u.DisplayName || deref(next.PreferredLanguage) != deref(u.PreferredLanguage)
		if fields {
			if err := q.UpdateProvisionedUser(ctx, next); err != nil {
				if db.IsUniqueViolation(err, "ux_users_normalized_email") {
					return contracts.ErrAccountEmailTaken
				}
				return err
			}
		}
		renumbered := deref(u.EmployeeNumber) != s.number
		if renumbered {
			holder, err := q.GetUserIDByEmployeeNumber(ctx, contracts.NormalizeEmployeeNumber(s.number))
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return err
			case holder != userID:
				return contracts.ErrEmployeeNumberTaken
			}
			if err := setStaffManaged(ctx, q, userID, &s.number, now); err != nil {
				if db.IsUniqueViolation(err, "ux_users_employee_number") {
					return contracts.ErrEmployeeNumberTaken
				}
				return err
			}
		}
		if fields || renumbered {
			by, system := auditActor(actor)
			if err := writeAudit(ctx, q, nil, now, auditEvent{
				actor: by, system: system, targetUser: &userID, action: "user.staff-updated",
				before: staffUpdatedAudit{DisplayName: u.DisplayName, PreferredLanguage: u.PreferredLanguage, EmployeeNumber: u.EmployeeNumber},
				after: staffUpdatedAudit{
					DisplayName: next.DisplayName, PreferredLanguage: next.PreferredLanguage, EmployeeNumber: &s.number,
					EmailChanged: emailChanged, ReplacedInvitations: replaced,
				},
			}); err != nil {
				return err
			}
		}
		ref, err = p.reread(ctx, q, userID, now)
		return err
	})
	if err != nil {
		return contracts.AccountRef{}, err
	}
	return ref, nil
}

// Account reports the user id names, or nil for none, in one read-only
// snapshot so the user and their invitations agree. wantEmail is compared,
// never returned (AccountRef.EmailDiffers).
func (p *provisioning) Account(ctx context.Context, userID uuid.UUID, wantEmail string) (*contracts.AccountRef, error) {
	var out *contracts.AccountRef
	err := db.WithTx(ctx, p.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := store.New(tx)
		u, err := q.GetProvisioningUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ref, err := p.ref(ctx, q, provisionedUser(u), wantEmail, p.deps.Clock())
		out = &ref
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("identity: account: %w", err)
	}
	return out, nil
}

// ProvisioningRoles lists the roles Staff may give, by name.
func (p *provisioning) ProvisioningRoles(ctx context.Context) ([]contracts.RoleRef, error) {
	rows, err := store.New(p.deps.Pool).ListProvisioningRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: provisioning roles: %w", err)
	}
	out := make([]contracts.RoleRef, len(rows))
	for i, row := range rows {
		out[i] = contracts.RoleRef{ID: row.ID, Name: row.Name}
	}
	return out, nil
}
