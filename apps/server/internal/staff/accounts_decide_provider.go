package staff

import (
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the decision table's third column (Entra design D7): what
// the accounts worker does next to a person's provider link — their
// account in an external directory, for a person whose account kind is a
// provider's key ("entra"). decideProvider is pure as decide is (its purity
// test in accounts_decide_provider_internal_test.go): it reads no database,
// no clock and calls no provider; the worker fills its input from the
// snapshot it closed, from the provider's Account and Provider asked on no
// connection, and from the identity link's user as identity reported them.
//
// How the worker uses it: provider first, identity second, so the
// announcement precedes the arrival. For one claim (on the identity link)
// it runs decideProvider; when the step is a call (prepare, announce,
// activate, deactivate, update, delete) it makes it, and decides again on
// the account the call answered, at most three provider calls — Expect, the
// announcement, among them — then the identity chain's own three
// (accounts_decide.go). A Deactivate or a Delete while the identity link
// names no user withdraws the announcement (Unexpect) in the same call's
// turn. The result lands on the provider link (WriteProviderLinkResult) on
// the claim's lease.

// The provider column's own steps, beside decide's.
const (
	// accountStepAnnounce: AccountProvisioning.Expect — identity told the
	// external account the provider prepared, so it adopts the user the
	// provider's SCIM push or the person's first sign-in creates.
	accountStepAnnounce accountStepKind = "announce"
	// accountStepDelete: IdentityProvisioner.Delete — a leaver's disabled
	// account soft-deleted after delete_external_after_days.
	accountStepDelete accountStepKind = "delete"
)

// providerLinkFacts is the person's provider link: its state, the external
// account it names (ExternalID nil until a Prepare answered), whether the
// account was announced to identity, and whether the worker left it enabled
// today (EnabledToday: an account found disabled after that was disabled in
// the directory today, and is not re-enabled before tomorrow).
type providerLinkFacts struct {
	State        string
	ExternalID   *string
	Announced    bool
	EnabledToday bool
}

// providerDecision is decideProvider's whole input. Status is the provider's
// (ProviderInfo.Status); Link nil is a person with no provider link; Account
// is what the provider answered for the link's account (nil with an
// ExternalID: the directory has none). SpecChanged is the external spec
// hashed as the worker hashes it differing from the link's last_spec_hash;
// Arrived is whether the identity link names a user who is not a pending,
// credential-less one (the announced account has arrived, or the person
// signed in). DeleteAfterDays is delete_external_after_days (nil: never),
// LegalHold the person's, DisableRequested HR's Disable now, Fresh HR's Run
// now (a new pass).
type providerDecision struct {
	Person           accountPersonFacts
	Status           string
	Link             *providerLinkFacts
	Account          *contracts.ExternalAccount
	SpecChanged      bool
	Arrived          bool
	DeleteAfterDays  *int
	LegalHold        bool
	DisableRequested bool
	CreateBeforeDays int
	Today            time.Time
	Fresh            bool
}

// decideProvider is D7's column for the provider link, first match wins:
//
//  1. HR's Disable now: deactivate an account that is enabled, else nothing.
//  2. Erased: nothing (the erase of the external account is delivery E's).
//  3. The link names an account the directory no longer has: report failed
//     user_gone, never re-created (S reading 18).
//  4. No employment: with the flag on and a link, report no_employment;
//     otherwise nothing.
//  5. Every employment ended (T > E, none later), or with the flag on
//     between employments before the later one is due: deactivate an
//     enabled account — whatever the provider's status, since Deactivate
//     runs as long as a credential works; after delete_external_after_days
//     from the last end, no legal hold and the account disabled, delete it
//     (a leaver only, never between employments); otherwise nothing.
//  6. The flag off: nothing.
//  7. The provider not connected (unconfigured or failing): report
//     provider_unavailable on a link there is, the link's state kept.
//  8. Before the window (T < F − createBeforeDays): nothing.
//  9. No account (no link, a link deleted, or one no Prepare answered):
//     prepare.
//  10. The account not announced and nothing arrived yet: announce.
//  11. In the window: update an enabled account whose spec changed, else
//     nothing.
//  12. Due (T ≥ F): a disabled account is activated (a rehire too, with a
//     new pass), unless the worker left it enabled today; an enabled one is
//     activated again with a fresh pass on Run now, updated when its spec
//     changed, else nothing.
func decideProvider(in providerDecision) accountStep {
	none := accountStep{Kind: accountStepNone}
	p := in.Person
	link := in.Link
	acc := in.Account
	deleted := link != nil && link.State == providerStateDeleted
	hasAccount := link != nil && link.ExternalID != nil && !deleted && acc != nil && acc.State != providerStateDeleted
	enabled := hasAccount && acc.State == providerStateEnabled
	if in.DisableRequested {
		if enabled {
			return accountStep{Kind: accountStepDeactivate}
		}
		return none
	}
	if p.Erased {
		return none
	}
	if link != nil && link.ExternalID != nil && !deleted && acc == nil {
		return accountStep{Kind: accountStepReport, Reason: reasonUserGone, State: providerStateFailed}
	}
	if len(p.Employments) == 0 {
		if p.Flag && link != nil {
			return accountStep{Kind: accountStepReport, Reason: reasonNoEmployment}
		}
		return none
	}
	today := decideDay(in.Today)
	phase, between := accountPhaseOf(p, today, in.CreateBeforeDays)
	if phase == phaseEnded || (p.Flag && between && phase != phaseDue) {
		switch {
		case enabled:
			return accountStep{Kind: accountStepDeactivate}
		case phase == phaseEnded && hasAccount && acc.State == providerStateDisabled &&
			providerDeleteDue(p.Employments, today, in.DeleteAfterDays) && !in.LegalHold:
			return accountStep{Kind: accountStepDelete}
		}
		if phase == phaseEnded || phase == phaseBefore {
			return none
		}
	}
	if !p.Flag {
		return none
	}
	if in.Status != contracts.ProviderConnected {
		if link == nil {
			return none
		}
		return accountStep{Kind: accountStepReport, Reason: reasonProviderUnavailable}
	}
	if phase == phaseBefore {
		return none
	}
	if !hasAccount {
		return accountStep{Kind: accountStepPrepare}
	}
	if !link.Announced && !in.Arrived {
		return accountStep{Kind: accountStepAnnounce}
	}
	if phase == phaseWindow {
		if enabled && in.SpecChanged {
			return accountStep{Kind: accountStepUpdate}
		}
		return none
	}
	switch {
	case !enabled && link.EnabledToday:
		return none
	case !enabled, in.Fresh:
		return accountStep{Kind: accountStepActivate, Fresh: in.Fresh}
	case in.SpecChanged:
		return accountStep{Kind: accountStepUpdate}
	default:
		return none
	}
}

// providerDeleteDue is whether delete_external_after_days (nil: never) have
// passed since the last end of employments, every one of which has ended.
func providerDeleteDue(employments []decideEmployment, today time.Time, days *int) bool {
	if days == nil {
		return false
	}
	_, _, ended, lastEnd, _ := accountSpan(employments, today)
	return ended && !today.Before(lastEnd.AddDate(0, 0, *days))
}
