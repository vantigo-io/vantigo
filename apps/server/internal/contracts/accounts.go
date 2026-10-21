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

// The account kinds: who holds a person's account authority (Entra design
// D5), which Staff records per person and passes on every call that creates,
// enables or updates (AccountSpec.Kind, ActivateOpts.Kind), so the person's
// kind — not the installation's mode alone — decides what identity creates,
// gives and invites.
const (
	// AccountKindLocal: identity holds the credentials. Prepare creates the
	// user, Activate enables, invites and gives the provisioning roles.
	AccountKindLocal = "local"
	// AccountKindExternal: an identity provider owns the account and an
	// Owner adopts the user it creates. Prepare only finds, Activate only
	// re-enables a user Staff disabled, Update writes the language and the
	// number alone — the federated mode's rules, for one person.
	AccountKindExternal = "external"
	// AccountKindEntra: an external provider in the IdentityProvisioner slot
	// (keyed "entra") holds the account, which Staff has it create and
	// announces (Expect), so no Owner adopts it. Link-only as
	// AccountKindExternal, except that Activate gives the provisioning
	// roles once, on the first activation of the adopted user.
	AccountKindEntra = "entra"
)

// ValidAccountKind reports whether s is one of the three account kinds,
// exactly as spelled.
func ValidAccountKind(s string) bool {
	switch s {
	case AccountKindLocal, AccountKindExternal, AccountKindEntra:
		return true
	default:
		return false
	}
}

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
	// DisabledByDirectory: SCIM is configured and the directory deactivated
	// the user, who is not an Owner — identity's record may still say enabled,
	// but no session of theirs is accepted. A provider still writes
	// identity's record of such a user: Deactivate disables them for Staff
	// (a leaver loses access whatever the directory later says), and Activate
	// re-enables a user Staff disabled, whom the directory keeps out while it
	// reports them inactive.
	DisabledByDirectory = "directory"
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

// SystemActorStaffRetention is the Actor.System of the staff-retention worker.
const SystemActorStaffRetention = "system:staff-retention"

// Actor is who a provisioning call acts for, written to identity's audit:
// exactly one of UserID (a person who pressed a button: identity writes them
// as the audit row's actor) and System (a background worker, such as
// SystemActorStaffAccounts or SystemActorStaffRetention: identity writes a null
// actor with details.actor = System and no correlation id).
type Actor struct {
	UserID *uuid.UUID
	System string
}

// Valid reports whether a names exactly one actor: a user (not the nil
// UUID), or a known system (SystemActorStaffAccounts or
// SystemActorStaffRetention). A provider refuses an invalid actor with
// ErrInvalidActor before any write, so every change it audits has someone to
// name.
func (a Actor) Valid() bool {
	user := a.UserID != nil && *a.UserID != uuid.Nil
	switch {
	case user && a.System == "":
		return true
	case a.UserID == nil && (a.System == SystemActorStaffAccounts || a.System == SystemActorStaffRetention):
		return true
	default:
		return false
	}
}

// AccountSpec is what the caller wants a managed user to look like. It names
// no roles: identity assigns the roles an Owner named for Staff
// (ProvisioningRoles), never roles the caller chooses — staff:manage must not
// be able to mint an account holding a role it names.
type AccountSpec struct {
	// Email is the address the caller chose for the account. Identity writes
	// it only while the address follows the caller's record
	// (AccountRef.EmailFollows: the user is pending and no invitation naming
	// them has been delivered); afterwards the address is identity's, which an
	// Owner changes.
	Email string
	// DisplayName is the name identity shows for the user.
	DisplayName string
	// PreferredLanguage is "en" or "nb".
	PreferredLanguage string
	// EmployeeNumber correlates the user with the caller's record and is kept
	// on the user. See SameEmployeeNumber for how two numbers compare.
	EmployeeNumber string
	// Kind is the person's account kind (AccountKindLocal,
	// AccountKindExternal or AccountKindEntra), which decides whether
	// Prepare may create the user and what Update writes. "" reads the mode
	// — AccountKindLocal when Mode answers AccountModeLocal, else
	// AccountKindExternal — as a caller of the previous release, which
	// passes none, expects.
	Kind string
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
	// DisabledBy is "", DisabledByOwner, DisabledByStaff or DisabledByDirectory.
	DisabledBy string
	// Managed reports whether Staff manages the user; a provider writes only
	// to a managed user (ErrAccountNotManaged).
	Managed bool
	// EmployeeNumber is the number identity holds for the user ("" when
	// none). A caller acts on a user only when it is the same number as its
	// own record's (SameEmployeeNumber): a link to a user with another number
	// is someone else's account.
	EmployeeNumber string
	// EmailDiffers reports whether the user's address in identity differs
	// from the one the caller passed to Account, compared as identity
	// compares addresses (normalised, so a difference of case alone is none).
	// It is false when identity holds a placeholder no mail reaches, and when
	// the caller passed none. Update writes the address only while
	// EmailFollows, so any other difference is the caller's to report to an
	// Owner. The identity address itself never crosses into the caller.
	EmailDiffers bool
	// EmailFollows reports whether the user's address still follows the
	// caller's record: the user is pending and no invitation naming them has
	// been delivered (one whose delivery failed is none; one issued and not
	// yet sent counts as delivered). Once the first invitation went out, the
	// address is the one the person was invited at, and Update keeps it.
	EmailFollows bool
	// Privileged reports whether the user is an Owner or a SystemAdmin, whom
	// an Owner handles by hand: a provider refuses every write to them
	// (ErrAccountPrivileged), managed or not — a managed user promoted to
	// Owner is unmanaged by the promotion and reads Privileged too.
	Privileged bool
	// HasCredential reports whether the user can sign in by themselves: a
	// password, a passkey or an OIDC link.
	HasCredential bool
	// Invitation is the user's invitation state; InvitationNone for a user
	// who is not pending.
	Invitation Invitation
	// Created reports that the call answering the ref created the user: true
	// only on the Prepare that made them, false on every other answer (a
	// Prepare that found the user, any other write, Account). A caller that
	// must undo its own creation — its record gone while Prepare ran —
	// deactivates only a user it created, never one it found.
	Created bool
}

// ActivateOpts adjusts one Activate call.
type ActivateOpts struct {
	// FreshInvitation starts a new pending spell — the delivered count reset
	// and an Owner's revoke overridden — so a pending user gets an invitation
	// even when the last was revoked by an Owner or three went unanswered. It
	// never re-sends an invitation that can still be accepted, and starts a
	// spell only when no invitation naming the user was made in the last ten
	// minutes (within them the call is an ordinary one). Set only by an
	// explicit request of a person (Staff's Run now), never by a worker.
	FreshInvitation bool
	// Kind is the person's account kind, as AccountSpec.Kind: it decides
	// whether the call invites and gives the provisioning roles. "" reads
	// the mode, as there.
	Kind string
}

// ExpectedAccount is a Vantigo user the caller knows will arrive from the
// identity provider for a person it registers: Staff has had the provider
// create the external account, and announces it so that identity adopts the
// user the provider's SCIM push or the person's first sign-in creates, with
// no Owner in between. Matched by EmployeeNumber (the enterprise extension's
// employeeNumber), by UserPrincipalName (SCIM's userName), or by ObjectID
// (OIDC's oid; SCIM's externalId when the tenant maps it so).
type ExpectedAccount struct {
	EmployeeNumber    string
	DisplayName       string
	PreferredLanguage string
	Issuer            string // the OIDC issuer the user will sign in under
	ObjectID          string // the provider's immutable id of the user
	UserPrincipalName string
	// ReplaceUserID names a pending, credential-less managed user of the
	// same employee number (the person's kind changed from local): the
	// arrival completes that user instead of creating one.
	ReplaceUserID *uuid.UUID
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
	// ErrEmployeeNumberTaken: another user holds the employee number (by
	// NormalizeEmployeeNumber's key), so it cannot be given to this one.
	ErrEmployeeNumberTaken = errors.New("contracts: another account holds the employee number")
	// ErrInvalidActor: the call's Actor is not Valid. A provider answers it
	// before any write; it is the caller's bug, never retried.
	ErrInvalidActor = errors.New("contracts: the call names no valid actor")
	// ErrEmployeeRoleMissing: no Owner has named the roles Staff may give, so
	// a local Activate cannot give the user any.
	ErrEmployeeRoleMissing = errors.New("contracts: no employee role is named for Staff")
	// ErrEmployeeRoleUnsafe: a role an Owner named for Staff now carries a
	// permission that grants administration (identity:manage, any other
	// non-delegable key, staff:manage, staff:personal-data or staff:approve —
	// only possible by a write outside identity's own checks), so a local
	// Activate gives none of the set until an Owner takes it out.
	ErrEmployeeRoleUnsafe = errors.New("contracts: a role named for Staff grants administration")
	// ErrAccountNotFound: in link-only kinds (AccountKindExternal,
	// AccountKindEntra, or federated mode when the call names no kind), no
	// managed user holds the employee number — the identity provider has
	// not created one yet.
	ErrAccountNotFound = errors.New("contracts: no account holds the employee number")
	// ErrUserGone: the user the caller names no longer exists.
	ErrUserGone = errors.New("contracts: the user no longer exists")
	// ErrModeMismatch: this process derives another authority mode than the
	// one identity's API process recorded (their OIDC or SCIM settings
	// differ), so a provider prepares, activates and updates nothing — it
	// still deactivates, so a leaver never keeps access over it.
	//
	// No provider answers it any more: identity derives the mode from its
	// single sign-on row, which every process reads (Entra design D1). It
	// stays this release so a worker on the previous binary still reports
	// sanely during a rolling upgrade, and goes in the next.
	ErrModeMismatch = errors.New("contracts: this process's authority mode differs from the recorded one")
	// ErrLastOwner: deactivating the user would leave no active Owner.
	ErrLastOwner = errors.New("contracts: the account is the last active Owner")
	// ErrAccountEnabled: Erase was called for a user who is not disabled.
	ErrAccountEnabled = errors.New("contracts: the account is not disabled")
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
// which reads. Five rules hold:
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
//     the set an Owner named (ProvisioningRoles), only on the call that
//     enables a user Staff disabled (AccountKindLocal) or once, on the first
//     activation of an adopted user (AccountKindEntra), so an Owner's later
//     removal of a role is not undone.
//   - The kind travels on the call. The person's account kind
//     (AccountSpec.Kind, ActivateOpts.Kind) decides what identity creates,
//     gives and invites, never the installation's mode alone: a local person
//     in an installation whose single sign-on is on is still created and
//     invited, and an entra person still given the roles. A call naming no
//     kind reads the mode, as the previous release did.
//   - The employee number decides whose account it is. Prepare finds a
//     managed user by number; a caller acts on a user only when the number
//     identity reports is the same as its record's, by SameEmployeeNumber.
//
// Every write is idempotent: called again with the same arguments it changes
// nothing more and answers the same AccountRef — but for Created, which only
// the creating Prepare answers. Every write refuses an
// Actor that is not Valid with ErrInvalidActor before it writes anything.
type AccountProvisioning interface {
	// Mode answers AccountModeLocal or AccountModeFederated, the mode
	// identity's single sign-on settings derive: what a call naming no kind
	// reads its kind from, and what Staff seeds its people's kinds from
	// once. (A provider of the
	// previous release answered ErrModeMismatch when this process's own
	// configuration derived another, and every write but Deactivate then
	// did too; identity answers it no more.)
	Mode(ctx context.Context) (string, error)
	// Prepare finds the managed user holding spec.EmployeeNumber or, for
	// AccountKindLocal only, creates one: Staff-managed, disabled by Staff,
	// pending, with no credential. ErrAccountEmailTaken when another user
	// holds the address, ErrEmployeeNumberTaken when an unmanaged user holds
	// the number, ErrAccountPrivileged when the user found is an Owner or a
	// SystemAdmin, ErrAccountNotFound for a link-only kind when none is
	// found.
	Prepare(ctx context.Context, actor Actor, spec AccountSpec) (AccountRef, error)
	// Activate enables a managed user. For AccountKindLocal the call that
	// enables them assigns the provisioning roles (ErrEmployeeRoleMissing
	// when there are none), and it invites a pending user who has no
	// credential and no acceptable invitation — never within ten minutes of
	// the last invitation naming them — delivering after its commit; for
	// AccountKindExternal it only re-enables a user Staff disabled; for
	// AccountKindEntra it re-enables a user Staff disabled and, the first
	// time it is called for the user, assigns the provisioning roles — once,
	// enabled or not, never again. ErrAccountNotManaged,
	// ErrAccountPrivileged, ErrBlockedByOwner, ErrEmployeeRoleMissing,
	// ErrEmployeeRoleUnsafe, ErrUserGone.
	Activate(ctx context.Context, actor Actor, userID uuid.UUID, opts ActivateOpts) (AccountRef, error)
	// Deactivate disables a managed user (DisabledByStaff unless already
	// disabled), whatever a directory says, ends their sessions and revokes
	// their acceptable invitations. It reads no authority mode, so it never
	// answers ErrModeMismatch: a leaver is disabled even by a process whose
	// configuration disagrees. ErrAccountNotManaged, ErrAccountPrivileged,
	// ErrLastOwner, ErrUserGone.
	Deactivate(ctx context.Context, actor Actor, userID uuid.UUID) (AccountRef, error)
	// Update writes the display name, language and employee number of a
	// managed user, and the email only while it follows the caller's record
	// (AccountRef.EmailFollows: pending, no invitation delivered yet); for a
	// link-only kind the language and the employee number only.
	// ErrAccountNotManaged, ErrAccountEmailTaken, ErrEmployeeNumberTaken,
	// ErrUserGone.
	Update(ctx context.Context, actor Actor, userID uuid.UUID, spec AccountSpec) (AccountRef, error)
	// Account reports a user, or (nil, nil) when no user has the id.
	// wantEmail is the address the caller would set; the answer says only
	// whether identity's differs (AccountRef.EmailDiffers), never what it is.
	// A directory-deactivated non-Owner reads AccountStateDisabled with
	// DisabledByDirectory while SCIM is configured.
	Account(ctx context.Context, userID uuid.UUID, wantEmail string) (*AccountRef, error)
	// ProvisioningRoles lists the roles an Owner named for Staff to give,
	// ordered by name; empty when none is named.
	ProvisioningRoles(ctx context.Context) ([]RoleRef, error)
	// Expect records or replaces the announcement for spec.EmployeeNumber.
	// ErrEmployeeNumberTaken when an unmanaged user holds the number,
	// ErrAccountPrivileged when the managed one is an Owner.
	Expect(ctx context.Context, actor Actor, spec ExpectedAccount) error
	// Unexpect withdraws an announcement nobody fulfilled.
	Unexpect(ctx context.Context, actor Actor, employeeNumber string) error
	// Erase deletes a managed, disabled user whose person was erased (D16).
	// ErrUserGone, ErrAccountNotManaged, ErrAccountPrivileged, ErrLastOwner,
	// ErrAccountEnabled.
	Erase(ctx context.Context, actor Actor, userID uuid.UUID) error
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
