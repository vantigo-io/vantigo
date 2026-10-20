package contracts

import (
	"context"
	"errors"
	"time"
)

// IdentityProvisioner is an external identity provider's accounts
// and groups as a module offers them to Staff: Entra today, another
// directory later. Staff is the authority — it decides what exists and when,
// from the register — and the provider executes against its directory and
// answers what it did. Every write names its Actor; every write is
// idempotent; every call runs on the provider's own pool and reaches the
// network, so a caller never makes one inside a transaction or holding a
// connection, as for AccountProvisioning (rule 11).
//
// It is the fifth many-provider slot (Entra design D6): a module fills it
// through module.Module.IdentityProvisioner, and Compose and Workers collect
// the enabled modules' providers into module.Deps.IdentityProvisioners,
// refusing two with one Key. Staff reaches each through accessors that
// refuse a call inside a transaction (contractscalls.go). The sentinels
// below are the provider's answers; anything else is a failure to retry.
type IdentityProvisioner interface {
	// Key is the provider's key — the identity_links.provider value,
	// "entra" — answered with no context and no network, so composition can
	// refuse two providers claiming one. Provider(ctx).Key answers the same.
	Key() string

	// Provider names the provider: its key (the identity_links.provider
	// value, "entra"), its display name, and its status — unconfigured
	// (no connection yet), connected, or failing (the last check failed:
	// a credential expired, consent withdrawn), with the reason code.
	Provider(ctx context.Context) (ProviderInfo, error)

	// Prepare finds the account holding spec.EmployeeNumber, or creates it
	// disabled with no licence and the mapped attributes. Idempotent: an
	// existing account is updated to the spec and answered.
	// ErrExternalAmbiguous (two accounts hold the number),
	// ErrExternalAddressTaken (the address is another account's),
	// ErrExternalSyncedOnPremises, ErrExternalPrivileged,
	// ErrExternalUnconfirmed (an account the provider did not create holds
	// the number: linked, nothing written, until its adoption is
	// confirmed), ErrProviderUnavailable.
	Prepare(ctx context.Context, actor Actor, spec ExternalSpec) (ExternalAccount, error)
	// Activate enables the account, writes the hire date, assigns the
	// licences of the groups the person is in (spec.Licences) and, when the
	// account had no pass yet in this activation, issues a Temporary Access
	// Pass the provider keeps sealed (RevealPass). Idempotent.
	// ErrExternalUnconfirmed: an adopted account not yet confirmed.
	Activate(ctx context.Context, actor Actor, externalID string, spec ExternalSpec) (ExternalAccount, error)
	// Deactivate disables the account, revokes its sessions, writes the
	// leave date, strips the direct licences, and removes it from the
	// groups the provider manages and, when the connection says so, every
	// other group it may. Idempotent; runs whatever Provider's status says,
	// as long as a credential works. ErrExternalUnconfirmed: an adopted
	// account not yet confirmed, left as it is.
	Deactivate(ctx context.Context, actor Actor, externalID string, spec ExternalSpec) (ExternalAccount, error)
	// Update writes the mapped attributes and the manager (the mover).
	// ErrExternalUnconfirmed: an adopted account not yet confirmed.
	Update(ctx context.Context, actor Actor, externalID string, spec ExternalSpec) (ExternalAccount, error)
	// Delete soft-deletes the account (restorable by an administrator for
	// thirty days). ErrExternalEnabled when it is not disabled;
	// ErrExternalUnconfirmed for an adopted account not yet confirmed.
	Delete(ctx context.Context, actor Actor, externalID string) error
	// Account reads the account, or (nil, nil) when the directory has none.
	Account(ctx context.Context, externalID string) (*ExternalAccount, error)
	// RevealPass answers the pass issued for the account once — the second
	// call, and a call after its lifetime, answer ErrPassGone — and audits it.
	RevealPass(ctx context.Context, actor Actor, externalID string) (Pass, error)

	// Groups (delivery D).
	//
	// GroupTraits declares the traits a group may carry for this provider.
	// It reads the provider's own state — the tenant's licences as its last
	// connection check cached them — and never the network, so it takes a
	// context and may fail; an error is the provider's state unreadable.
	GroupTraits(ctx context.Context) ([]GroupTrait, error)
	// EnsureGroup creates or updates the external group for the caller's
	// group id. Idempotent: the first call creates the group with
	// spec.OwnerExternalIDs and the provider's own identity as its owners;
	// later calls write the name, the description and the mutable traits,
	// and reconcile the owners. A value the provider refuses answers a
	// *GroupTraitError naming the trait (a caller tests it with errors.As,
	// keeps the field and the code, and calls again only when the values
	// changed); ErrGroupNotManageable for a group the provider found that it
	// may not write (dynamic membership, role-assignable, or another's);
	// ErrGroupUnconfirmed for a group the provider found and adopted and
	// nobody has confirmed yet, nothing written to it;
	// ErrProviderUnavailable. What the provider could not do yet and will
	// try again is ExternalGroup.Pending, never an error.
	EnsureGroup(ctx context.Context, actor Actor, spec GroupSpec) (ExternalGroup, error)
	// SetMembers reconciles the external group's members to exactly these
	// external ids: the missing added, the extra removed. Idempotent; a
	// member it could not change is in MembersResult.Failed with a code, and
	// the call answers no error for it. ErrGroupNotManageable for a group
	// EnsureGroup never made; ErrGroupUnconfirmed for an adopted group
	// nobody has confirmed yet, nothing written.
	SetMembers(ctx context.Context, actor Actor, groupID string, memberExternalIDs []string) (MembersResult, error)
	// DeleteGroup deletes the external group (restorable by an
	// administrator for thirty days where the directory allows), or retires
	// it where a delete could not be undone, or leaves a group the provider
	// did not create to its directory, forgetting it. Idempotent: a group
	// already gone, or never made, answers nil.
	DeleteGroup(ctx context.Context, actor Actor, groupID string) error
}

// The statuses of ProviderInfo.Status.
const (
	// ProviderUnconfigured: the provider has no connection yet.
	ProviderUnconfigured = "unconfigured"
	// ProviderConnected: the provider's last check succeeded.
	ProviderConnected = "connected"
	// ProviderFailing: the provider's last check failed — a credential
	// expired, consent withdrawn — with the reason in ProviderInfo.Reason.
	ProviderFailing = "failing"
)

// ProviderInfo is a provider as Provider names it.
type ProviderInfo struct {
	// Key is the identity_links.provider value, "entra", the same as Key().
	Key string
	// Name is the provider's display name, "Microsoft Entra ID".
	Name string
	// Status is ProviderUnconfigured, ProviderConnected or ProviderFailing.
	Status string
	// Reason is a code — never an address or the directory's words — for
	// a failing status ("credential_invalid", "secret_expired"), or a fact
	// about a connected one ("secret_expiring"); "" when there is none.
	Reason string
}

// ExternalSpec is what the caller wants a person's external account to look
// like, from the register.
type ExternalSpec struct {
	EmployeeNumber, GivenName, FamilyName, DisplayName string
	WorkEmail                                          string     // "" lets the provider make the address
	PreferredLanguage                                  string     // "nb" | "en"
	WorkCountry                                        string     // ISO 3166-1 alpha-2, the usage location
	HireDate, LeaveDate                                *time.Time // civil days; the provider makes the instants
	// Fields are the mapped fields the installation turned on (D8), keyed by
	// the directory's attribute names — jobTitle, department, companyName,
	// employeeType, costCenter, division, officeLocation, streetAddress,
	// postalCode, city, country, businessPhone, mobilePhone — each with its
	// value, "" to clear it. A field the installation turned off is absent,
	// and the provider leaves it as it is.
	Fields            map[string]string
	ManagerExternalID string    // "" when the manager has no account here
	Licences          []string  // SKU part numbers from the person's groups (D10)
	Flags             SpecFlags // Fresh: HR's Run now (a new pass)
}

// SpecFlags adjusts one call made with an ExternalSpec.
type SpecFlags struct {
	// Fresh is HR's Run now: Activate issues a new pass even when one not
	// yet revealed exists. Never set by a worker.
	Fresh bool
}

// ExternalAccount is an account in the provider's directory as the provider
// answers it.
type ExternalAccount struct {
	ExternalID        string // the directory's immutable id
	UserPrincipalName string
	Email             string   // the address the provider made or found
	State             string   // "disabled" | "enabled" | "deleted"
	Created           bool     // true only on the Prepare that created it
	HasPass           bool     // a pass exists and was not yet revealed
	Reported          []string // facts the provider could not act on: "hire_date_not_written", "licence_count", "privileged", …
}

// The codes a provider reports in ExternalAccount.Reported: facts about the
// external account it could not act on, which the caller records on its
// link and raises as attention items. A code never carries an address or
// the directory's words.
const (
	// ReportedHireDateNotWritten: the directory refused the hire date.
	ReportedHireDateNotWritten = "hire_date_not_written"
	// ReportedPrivileged: the account holds an administrative role in the
	// directory, which the provider does not write to.
	ReportedPrivileged = "privileged"
	// ReportedPassNotAllowed: the directory's policy does not allow the
	// provider to issue a pass for the account.
	ReportedPassNotAllowed = "pass_not_allowed"
	// ReportedLicenceUnavailable: a licence could not be assigned, none
	// being left. A provider appends ":" and the SKU's part number
	// ("licence_unavailable:ENTERPRISEPACK").
	ReportedLicenceUnavailable = "licence_unavailable"
	// ReportedManagerNotFound: the manager the spec names has no account in
	// the directory.
	ReportedManagerNotFound = "manager_not_found"
)

// Pass is a Temporary Access Pass as RevealPass answers it, once.
type Pass struct {
	Value     string
	ExpiresAt time.Time
}

// GroupTrait is one trait a group may carry for a provider (Entra design
// D10): Staff renders it as a form field from this description alone and
// stores its value; the provider validates it.
type GroupTrait struct {
	Key         string            // "type", "team", "licences"
	Type        string            // "bool" | "string" | "choice" | "list"
	Label       map[string]string // "en", "nb"
	Description map[string]string
	Choices     []TraitChoice // for "choice" and "list": { Value, Label }
	DocURL      string        // the vendor's page
	Default     any
	Required    bool
	MaxLength   int // "string": the longest value admitted, in characters; 0 for no bound
}

// TraitChoice is one value a "choice" or "list" trait admits.
type TraitChoice struct {
	Value string
	Label map[string]string // "en", "nb"
}

// GroupSpec is what the caller wants an external group to look like: the
// caller's group id, its key, name and description, the trait values Staff
// stored for the provider, and the external ids of its owners.
type GroupSpec struct {
	GroupID, Key, Name, Description string
	Values                          map[string]any
	OwnerExternalIDs                []string
}

// ExternalGroup is a group in the provider's directory as EnsureGroup
// answers it.
type ExternalGroup struct {
	ExternalID string
	Created    bool // true only on the EnsureGroup that created it
	// Pending names the traits the provider could not act on yet and will
	// on a later call ("team" on a group too young for one) — never an
	// error. A caller that sees it calls EnsureGroup again next cycle.
	Pending []string
}

// The codes of GroupTraitError.Code.
const (
	// TraitRequired: a required trait has no value.
	TraitRequired = "trait_required"
	// TraitInvalid: the value is not one the trait admits — a choice not
	// among its choices, a string too long, a value another trait forbids.
	TraitInvalid = "trait_invalid"
	// TraitImmutable: the trait cannot change once the group exists, and
	// the value differs from the one it was made with.
	TraitImmutable = "trait_immutable"
	// TraitNeedsOwner: the trait needs the group to have a person as its
	// owner, and it has none.
	TraitNeedsOwner = "trait_needs_owner"
)

// GroupTraitError is EnsureGroup's refusal of one trait value: the trait's
// key and a code (TraitRequired, TraitInvalid, TraitImmutable,
// TraitNeedsOwner), never the directory's words. It does not go away until
// the values change, so a caller keeps it beside the field and does not
// retry.
type GroupTraitError struct {
	Field, Code string
}

func (e *GroupTraitError) Error() string {
	return "contracts: group trait " + e.Field + ": " + e.Code
}

// MembersResult is what SetMembers did: the members it added and removed,
// and those it could not change, each with a code.
type MembersResult struct {
	Added, Removed int
	Failed         []MemberFailure
}

// MemberFailure is one member SetMembers could not change: its external id
// and a code, never the directory's words — one of MemberNotFound,
// MemberDynamic, MemberRefused, MemberOther.
type MemberFailure struct {
	ID, Code string
}

// The codes of MemberFailure.Code.
const (
	// MemberNotFound: the directory has no account with the id.
	MemberNotFound = "not_found"
	// MemberDynamic: the group's membership is the directory's rule, not a
	// list the provider may write.
	MemberDynamic = "dynamic"
	// MemberRefused: the directory refused the change.
	MemberRefused = "refused"
	// MemberOther: the change failed for another reason.
	MemberOther = "other"
)

// The errors a provider answers. A caller tests them with errors.Is; anything
// else is a failure to retry ("the call could not be made"), never an answer.
var (
	// ErrExternalAmbiguous: two accounts in the directory hold the employee
	// number, so the provider cannot tell which is the person's.
	ErrExternalAmbiguous = errors.New("contracts: two external accounts hold the employee number")
	// ErrExternalAddressTaken: the address the spec names is another
	// account's in the directory.
	ErrExternalAddressTaken = errors.New("contracts: another external account holds the address")
	// ErrExternalSyncedOnPremises: the account is synchronised from an
	// on-premises directory, whose attributes the provider cannot write.
	ErrExternalSyncedOnPremises = errors.New("contracts: the external account is synchronised from on-premises")
	// ErrExternalPrivileged: the account holds an administrative role in
	// the directory, which the provider does not write to.
	ErrExternalPrivileged = errors.New("contracts: the external account is privileged")
	// ErrExternalUnconfirmed: the provider found an account it did not
	// create holding the employee number, and adopted it untrusted: a holder
	// of the provider's permission must confirm the adoption before the
	// provider writes to it. Prepare links the account and answers this all
	// the same; every write to it answers this, nothing written, until the
	// confirmation.
	ErrExternalUnconfirmed = errors.New("contracts: the external account was adopted and is not yet confirmed")
	// ErrExternalEnabled: Delete was asked of an account that is not
	// disabled.
	ErrExternalEnabled = errors.New("contracts: the external account is enabled")
	// ErrPassGone: the pass was revealed already, has expired, or was never
	// issued.
	ErrPassGone = errors.New("contracts: the pass is gone")
	// ErrProviderUnavailable: the provider has no working connection —
	// unconfigured, or its credential refused — so it wrote nothing.
	ErrProviderUnavailable = errors.New("contracts: the identity provider is unavailable")
	// ErrGroupNotManageable: the external group is not one the provider may
	// write (another's, or of a kind it does not manage).
	ErrGroupNotManageable = errors.New("contracts: the external group is not manageable")
	// ErrGroupUnconfirmed: the provider found a group it did not create
	// and adopted it untrusted; a holder of the provider's permission must
	// confirm the adoption before the provider writes to it. EnsureGroup
	// adopting the group answers this, and so does every later write to it,
	// nothing written, until the confirmation. Not a failure: the caller
	// asks again later.
	ErrGroupUnconfirmed = errors.New("contracts: the external group was adopted and is not yet confirmed")
)
