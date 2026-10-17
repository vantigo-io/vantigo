package contracts

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

// The authority modes AccountProvisioning.Mode answers.
const (
	// AccountModeLocal: identity holds the credentials, so a provider creates
	// users, enables them and invites them to choose a password.
	AccountModeLocal = "local"
	// AccountModeFederated: an identity provider (OIDC or SCIM) owns the
	// accounts, so a provider only finds, re-enables what it disabled and
	// disables — it never creates or invites.
	AccountModeFederated = "federated"
)

// The states of AccountRef.State.
const (
	// AccountStatePending is a Staff-managed user who has not yet chosen a
	// credential through their invitation.
	AccountStatePending = "pending"
	// AccountStateActive is a user who can sign in.
	AccountStateActive = "active"
	// AccountStateDisabled is a disabled user, or a directory-deactivated
	// non-Owner while SCIM is configured.
	AccountStateDisabled = "disabled"
)

// The values of AccountRef.DisabledBy beside "" (not disabled, or disabled
// before identity recorded who did it).
const (
	// DisabledByOwner: an Owner disabled the user. A provider never undoes
	// it (ErrBlockedByOwner).
	DisabledByOwner = "owner"
	// DisabledByStaff: a provider disabled the user for Staff, and may
	// re-enable them.
	DisabledByStaff = "staff"
)

// The states of Invitation.State.
const (
	// InvitationNone: the user has no invitation, or needs none.
	InvitationNone = "none"
	// InvitationActive: an invitation that can still be accepted.
	InvitationActive = "active"
	// InvitationExpired: the latest invitation lapsed unanswered; the next
	// Activate issues another while fewer than three were delivered in this
	// pending spell.
	InvitationExpired = "expired"
	// InvitationRevokedByOwner: an Owner revoked the latest invitation. Only
	// ActivateOpts.FreshInvitation issues another.
	InvitationRevokedByOwner = "revoked_by_owner"
	// InvitationExhausted: three invitations were delivered in this pending
	// spell and none was accepted. Only ActivateOpts.FreshInvitation issues
	// another.
	InvitationExhausted = "exhausted"
)

// SystemActorStaffAccounts is the Actor.System of the staff-accounts worker.
const SystemActorStaffAccounts = "system:staff-accounts"

// Actor is who a provisioning call acts for, written to identity's audit:
// exactly one of UserID (a person who pressed a button: identity writes them
// as the audit row's actor) and System (a background worker, such as
// SystemActorStaffAccounts: identity writes a null actor with
// details.actor = System and no correlation id).
type Actor struct {
	UserID *uuid.UUID
	System string
}

// AccountSpec is what the caller wants a managed user to look like. It names
// no roles: identity assigns the roles an Owner named for Staff
// (ProvisioningRoles), never roles the caller chooses — staff:manage must not
// be able to mint an account holding a role it names.
type AccountSpec struct {
	// Email is the address the caller chose for the account. Identity writes
	// it only while the user is pending (no credential yet); afterwards the
	// address is the Owner's to change in identity.
	Email string
	// DisplayName is the name identity shows for the user.
	DisplayName string
	// PreferredLanguage is "en" or "nb".
	PreferredLanguage string
	// EmployeeNumber correlates the user with the caller's record and is kept
	// on the user. See SameEmployeeNumber for how two numbers compare.
	EmployeeNumber string
}

// Invitation is the state of a pending user's invitation to choose a
// credential.
type Invitation struct {
	// State is one of InvitationNone, InvitationActive, InvitationExpired,
	// InvitationRevokedByOwner or InvitationExhausted.
	State string
	// Issued counts the invitations delivered in the user's current pending
	// spell — a failed delivery is not counted, and ActivateOpts.FreshInvitation
	// starts a new spell.
	Issued int
}

// AccountRef is a user as a provider reports it to the caller.
type AccountRef struct {
	UserID uuid.UUID
	// State is AccountStatePending, AccountStateActive or AccountStateDisabled.
	State string
	// DisabledBy is "", DisabledByOwner or DisabledByStaff.
	DisabledBy string
	// Managed reports whether Staff manages the user; a provider writes only
	// to a managed user (ErrAccountNotManaged).
	Managed bool
	// EmployeeNumber is the number identity holds for the user ("" when
	// none). A caller acts on a user only when it is the same number as its
	// own record's (SameEmployeeNumber): a link to a user with another number
	// is someone else's account.
	EmployeeNumber string
	// Email is the user's current address in identity. It exists so the
	// caller can tell whether it differs from the address it would set (a
	// non-pending user's address is not written, so a difference is reported
	// to an Owner rather than corrected). It is for that comparison alone:
	// never put on the caller's wire, in a log, an event or an error.
	Email string
	// HasCredential reports whether the user can sign in by themselves: a
	// password, a passkey or an OIDC link.
	HasCredential bool
	// Invitation is the user's invitation state; InvitationNone for a user
	// who is not pending.
	Invitation Invitation
}

// ActivateOpts adjusts one Activate call.
type ActivateOpts struct {
	// FreshInvitation starts a new pending spell — the delivered count reset
	// and an Owner's revoke overridden — so a pending user gets an invitation
	// even when the last was revoked by an Owner or three went unanswered. Set
	// only by an explicit request of a person (Staff's Run now), never by a
	// worker.
	FreshInvitation bool
}

// RoleRef is a role as ProvisioningRoles names it.
type RoleRef struct {
	ID   uuid.UUID
	Name string
}

// The errors a provider answers. A caller tests them with errors.Is; anything
// else is a failure to retry ("the call could not be made"), never an answer.
var (
	// ErrAccountNotManaged: the user is not managed by Staff, so a provider
	// writes nothing to it.
	ErrAccountNotManaged = errors.New("contracts: the account is not managed by Staff")
	// ErrAccountPrivileged: the user is an Owner or a SystemAdmin, whom an
	// Owner handles by hand.
	ErrAccountPrivileged = errors.New("contracts: the account is an Owner's or a SystemAdmin's")
	// ErrBlockedByOwner: an Owner disabled the user, and a provider does not
	// undo it.
	ErrBlockedByOwner = errors.New("contracts: the account was disabled by an Owner")
	// ErrAccountEmailTaken: another user, not the managed one with this
	// employee number, holds the address.
	ErrAccountEmailTaken = errors.New("contracts: another account holds the address")
	// ErrEmployeeRoleMissing: no Owner has named the roles Staff may give, so
	// a local Activate cannot give the user any.
	ErrEmployeeRoleMissing = errors.New("contracts: no employee role is named for Staff")
	// ErrAccountNotFound: in federated mode, no managed user holds the
	// employee number — the identity provider has not created one yet.
	ErrAccountNotFound = errors.New("contracts: no account holds the employee number")
	// ErrUserGone: the user the caller names no longer exists.
	ErrUserGone = errors.New("contracts: the user no longer exists")
	// ErrModeMismatch: this process derives another authority mode than the
	// one identity's API process recorded (their OIDC or SCIM settings
	// differ), so a provider acts on neither.
	ErrModeMismatch = errors.New("contracts: this process's authority mode differs from the recorded one")
	// ErrLastOwner: deactivating the user would leave no active Owner.
	ErrLastOwner = errors.New("contracts: the account is the last active Owner")
)

// AccountProvisioning is the one sanctioned way a module writes identity's
// users: an in-process port over identity's accounts through which Staff
// prepares, enables, disables and updates the account of a person it
// registers, without importing the identity package (barred by depguard) or
// writing its PostgreSQL schema (barred by internal/db/schema_test.go).
// identity implements it; Compose and Workers (worker mode never composes)
// wire it into every module's Deps as Accounts. It is nil when no module
// provides one, which a caller answers as "accounts are unavailable".
//
// It is a write direction, staff → identity, unlike every other contract,
// which reads. Four rules hold:
//
//   - The lock rule. A caller never calls it inside one of its own
//     transactions or read-only snapshots, and holds no connection or lease
//     connection across a call: each method runs on identity's own pool, in
//     identity's own transactions, and mails after its own commit, so a
//     caller holding locks while it waits would hold them across identity's
//     work and could deadlock a small pool.
//   - The actor. Every write names who it acts for (Actor), and identity
//     audits each change with it.
//   - The roles are identity's. AccountSpec carries none; Activate assigns
//     the set an Owner named (ProvisioningRoles), only when the user was
//     pending or disabled by Staff, so an Owner's later removal of a role is
//     not undone.
//   - The employee number decides whose account it is. Prepare finds a
//     managed user by number; a caller acts on a user only when the number
//     identity reports is the same as its record's, by SameEmployeeNumber.
//
// Every write is idempotent: called again with the same arguments it changes
// nothing more and answers the same AccountRef.
type AccountProvisioning interface {
	// Mode answers AccountModeLocal or AccountModeFederated — the mode
	// identity's API process recorded — or ErrModeMismatch when this
	// process's own configuration derives another.
	Mode(ctx context.Context) (string, error)
	// Prepare finds the managed user holding spec.EmployeeNumber or, in local
	// mode only, creates one: Staff-managed, disabled by Staff, pending, with
	// no credential. ErrAccountEmailTaken when another user holds the
	// address, ErrAccountPrivileged when the user found is an Owner or a
	// SystemAdmin, ErrAccountNotFound in federated mode when none is found.
	Prepare(ctx context.Context, actor Actor, spec AccountSpec) (AccountRef, error)
	// Activate enables a managed user. In local mode it assigns the
	// provisioning roles (ErrEmployeeRoleMissing when there are none) and
	// invites a pending user who has no credential and no acceptable
	// invitation, delivering after its commit; in federated mode it only
	// re-enables a user Staff disabled. ErrAccountNotManaged,
	// ErrAccountPrivileged, ErrBlockedByOwner, ErrUserGone.
	Activate(ctx context.Context, actor Actor, userID uuid.UUID, opts ActivateOpts) (AccountRef, error)
	// Deactivate disables a managed user (DisabledByStaff unless already
	// disabled), ends their sessions and revokes their acceptable
	// invitations. ErrAccountNotManaged, ErrAccountPrivileged, ErrLastOwner,
	// ErrUserGone.
	Deactivate(ctx context.Context, actor Actor, userID uuid.UUID) (AccountRef, error)
	// Update writes the display name, language and employee number of a
	// managed user, and the email only while the user is pending; in
	// federated mode the language and the employee number only.
	// ErrAccountNotManaged, ErrAccountEmailTaken, ErrUserGone.
	Update(ctx context.Context, actor Actor, userID uuid.UUID, spec AccountSpec) (AccountRef, error)
	// Account reports a user, or (nil, nil) when no user has the id.
	Account(ctx context.Context, userID uuid.UUID) (*AccountRef, error)
	// ProvisioningRoles lists the roles an Owner named for Staff to give,
	// ordered by name; empty when none is named.
	ProvisioningRoles(ctx context.Context) ([]RoleRef, error)
}

// NormalizeEmployeeNumber is the one key two employee numbers are compared
// by: trimmed of surrounding white space, NFC-normalised (two spellings of
// one character, a precomposed Å and A with a combining ring, are one), then
// upper-cased by Go's own case mapping. Identity stores it beside the number
// and its unique index is on it, as normalized_email is for addresses, so no
// database collation's upper() — which differs between the C locale and
// en_US, and from Go — ever decides which two numbers are one.
func NormalizeEmployeeNumber(s string) string {
	return strings.ToUpper(norm.NFC.String(strings.TrimSpace(s)))
}

// SameEmployeeNumber reports whether a and b are the same employee number:
// the same NormalizeEmployeeNumber key, identity's. An empty number is the
// same as no other, not even another empty one.
func SameEmployeeNumber(a, b string) bool {
	keyA := NormalizeEmployeeNumber(a)
	return keyA != "" && keyA == NormalizeEmployeeNumber(b)
}
