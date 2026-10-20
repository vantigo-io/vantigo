package staff

import "time"

// The provider link's names (Entra design D7): the reasons, states, event
// kinds and problem codes the accounts worker, Run now, Disable now, the
// Account tab and the pass share. The provider link is a staff.identity_links
// row whose provider is a provider's key ("entra") beside the person's
// identity link; it reuses the row's state, external_id, blocked_reason,
// last_spec_hash and attempts, and adds external_upn, external_email,
// announced_at, pass_issued_at and reported_codes (00060).

// linkProviderIdentity is the identity link's provider: identity's own
// users, the link the accounts worker claims.
const linkProviderIdentity = "identity"

// The provider link's states.
const (
	providerStatePreparing = "preparing"
	providerStateEnabled   = "enabled"
	providerStateDisabled  = "disabled"
	providerStateDeleted   = "deleted"
	providerStateFailed    = "failed"
	providerStateBlocked   = "blocked"
)

// The provider link's reasons (its blocked_reason): a provider's sentinel as
// a code, never an address or the directory's words. user_gone and
// invalid_actor are the identity link's names, reasonUserGone and
// reasonInvalidActor, reused.
const (
	reasonProviderUnavailable      = "provider_unavailable"
	reasonExternalAmbiguous        = "external_ambiguous"
	reasonExternalAddressTaken     = "external_address_taken"
	reasonExternalSyncedOnPremises = "external_synced_on_premises"
	reasonExternalPrivileged       = "external_privileged"
	reasonExternalEnabled          = "external_enabled"
)

// codeManagerNotInEntra is the code Staff itself records on a provider link's
// reported_codes, beside the provider's own: the person's effective manager
// has no external account to name as theirs.
const codeManagerNotInEntra = "manager_not_in_entra"

// The provider link's events, and the two person events it makes. Each
// carries codes only, never an address or a pass.
const (
	eventAccountAnnounced           = "account_announced"
	eventAccountExternalPrepared    = "account_external_prepared"
	eventAccountExternalActivated   = "account_external_activated"
	eventAccountExternalDeactivated = "account_external_deactivated"
	eventAccountExternalUpdated     = "account_external_updated"
	eventAccountExternalDeleted     = "account_external_deleted"
	eventAccountExternalBlocked     = "account_external_blocked"
	eventAccountExternalFailed      = "account_external_failed"
	eventWorkEmailSetByProvider     = "work_email_set_by_provider"
	eventPassRevealed               = "pass_revealed"
)

// The problem codes of the kind, the provider and the pass.
const (
	// codeAccountKindLocked (409): a change of kind away from a provider's
	// while the person's link under it names an account the directory has
	// not deleted or reported gone (kindLocked).
	codeAccountKindLocked = "account_kind_locked"
	// codeProviderMissing (400, a field error on accountKind or
	// defaultAccountKind): no enabled module offers that provider.
	codeProviderMissing = "provider_missing"
	// codePassGone (409): the pass was shown already, has expired, or none
	// was issued.
	codePassGone = "pass_gone"
	// codeProviderRefused (409): the provider refused Run now or Disable
	// now; the detail carries its code.
	codeProviderRefused = "provider_refused"
)

// The bounds of the provider's attention items.
const (
	// passUnrevealedAfter: a pass issued and not shown this long raises
	// passUnrevealed (B's bound until C's pass lifetime is known).
	passUnrevealedAfter = 24 * time.Hour
	// accountNotArrivedAfter: an account announced this long ago that no
	// user has fulfilled raises accountNotArrived (D4).
	accountNotArrivedAfter = 90 * 24 * time.Hour
)
