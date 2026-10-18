package staff

import (
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the accounts decision table (spec §4 "Accounts", plan Task
// 4a): what the accounts worker does next for one person, from facts it has
// already read. decide is pure — it reads no database, no clock and calls no
// provider (accounts_decide_internal_test.go's purity test) — so the worker
// (Task 4b) fills its input from a snapshot it has closed and from
// AccountProvisioning.Account, asked on no connection.
//
// How the worker uses it (Task 4b). For one claimed link: decide; when the
// step is a call (prepare, activate, deactivate, update), make it, then ask
// AccountProvisioning.Account(userID, the person's own address) — not the ref
// the call answered, whose EmailDiffers is always false since a write is
// handed no address to compare — and run decide again on that ref, the link
// now naming the user, and go on until the step is none or a report, at most
// three calls per claim (plan I2: Prepare, then Activate, then an Update in
// one claim). A step after which the ref is unchanged ends the chain too (an
// Activate within identity's ten minutes issues nothing; a Deactivate of a
// user the directory keeps inactive reads the same before and after), and
// Fresh is passed only to the first decide of a Run now. After a successful
// Prepare or Update the re-run's link facts carry the employee number it
// wrote as SyncedEmployeeNumber, and after an Update SpecChanged false; both
// are recorded on the link only on success, so a failed call leaves them as
// they were. A call's error is
// the worker's to record, not decide's: ErrAccountPrivileged (the user an
// Owner or a SystemAdmin — AccountRef carries no such mark, so only a call
// tells), ErrBlockedByOwner, ErrAccountNotFound (federated, not found),
// ErrUserGone, ErrAccountNotManaged, ErrEmployeeRoleMissing,
// ErrEmployeeRoleUnsafe, ErrAccountEmailTaken and ErrEmployeeNumberTaken each
// become the link's blocked_reason; anything else is a failure to retry. On
// ErrModeMismatch from Mode the worker still runs every deactivate step decide
// answers (deactivate reads no mode) and holds the others.

// accountStepKind is what decide answers the worker to do.
type accountStepKind string

const (
	// accountStepNone: nothing to call; the link records the ref's state.
	accountStepNone accountStepKind = "none"
	// accountStepPrepare: AccountProvisioning.Prepare — find the managed
	// user by the employee number or, in local mode, create them.
	accountStepPrepare accountStepKind = "prepare"
	// accountStepActivate: AccountProvisioning.Activate, with
	// ActivateOpts.FreshInvitation = accountStep.Fresh.
	accountStepActivate accountStepKind = "activate"
	// accountStepDeactivate: AccountProvisioning.Deactivate.
	accountStepDeactivate accountStepKind = "deactivate"
	// accountStepUpdate: AccountProvisioning.Update with the person's spec.
	accountStepUpdate accountStepKind = "update"
	// accountStepReport: nothing to call; the link records State (when
	// set) and Reason, from which the attention items are derived (Task 4c).
	accountStepReport accountStepKind = "report"
)

// The link's states the worker writes (the self-service link's is
// linkStateLinked, account_flag.go).
const (
	linkStatePreparing = "preparing"
	linkStatePending   = "pending"
	linkStateActive    = "active"
	linkStateDisabled  = "disabled"
	linkStateFailed    = "failed"
	linkStateBlocked   = "blocked"
)

// The reasons decide reports, written as the link's blocked_reason — codes,
// never an address or a driver's words (plan I5).
const (
	reasonNoEmployment         = "no_employment"
	reasonEmailMissing         = "account_email_missing"
	reasonBlockedByOwner       = "blocked_by_owner"
	reasonDisabledByOwner      = "disabled_by_owner"
	reasonUserGone             = "user_gone"
	reasonMismatch             = "mismatch"
	reasonModeChanged          = "mode_changed"
	reasonEmailDiffers         = "email_differs"
	reasonInvitationRevoked    = "invitation_revoked"
	reasonInvitationUnanswered = "invitation_unanswered"
	// reasonNotManaged: the worker's link names a user Staff no longer
	// manages (an Owner unmanaged or promoted them); the link keeps its state.
	reasonNotManaged = "account_not_managed"
	reasonPrivileged = "account_privileged"
)

// accountStep is decide's answer: one step. Fresh is the activate's
// FreshInvitation; Reason is the report's code, or on an update the report
// that rides with it (email_differs); State is the link state a report
// records, "" to leave the link's own.
type accountStep struct {
	Kind   accountStepKind
	Fresh  bool
	Reason string
	State  string
}

// decideEmployment is one employment's dates, civil; End nil is open.
type decideEmployment struct {
	Start time.Time
	End   *time.Time
}

// accountPersonFacts is what decide needs of the person: whether they are
// erased, the flag and its stored date (ProvisionFrom nil when HR set none),
// whether the address the flag names is present (accountAddress), the
// employee number and every employment.
type accountPersonFacts struct {
	Erased         bool
	Flag           bool
	ProvisionFrom  *time.Time
	AddressPresent bool
	EmployeeNumber string
	Employments    []decideEmployment
}

// accountLinkFacts is the person's link: its state, the user it names
// (ExternalID nil while preparing), whether the worker recorded the user
// managed, SpecChanged — the person's spec hashed as the worker hashes it
// (last_spec_hash) differs from the link's — and SyncedEmployeeNumber, the
// number the worker last wrote to identity for this link ("" when unknown),
// so a person HR renumbered is still known by the number identity holds.
type accountLinkFacts struct {
	State                string
	ExternalID           *string
	Managed              bool
	SpecChanged          bool
	SyncedEmployeeNumber string
}

// accountDecision is decide's whole input. Link nil is a person with no link;
// Ref is what Account answered for the link's user (nil: no user has the
// id). Mode is the authority mode, CreateBeforeDays the setting, Today the
// civil day in the settings zone (the caller's civilToday: daylight saving
// is the caller's), and Fresh whether HR asked for this run (Run now).
type accountDecision struct {
	Person           accountPersonFacts
	Link             *accountLinkFacts
	Ref              *contracts.AccountRef
	Mode             string
	CreateBeforeDays int
	Today            time.Time
	Fresh            bool
}

// accountPhase is where today falls against the person's employments and
// the effective provisionFrom F.
type accountPhase int

const (
	// phaseBefore: T < F − createBeforeDays.
	phaseBefore accountPhase = iota
	// phaseWindow: F − createBeforeDays ≤ T < F.
	phaseWindow
	// phaseDue: T ≥ F, and an employment not yet ended.
	phaseDue
	// phaseEnded: every employment ended before T, none later (T > E).
	phaseEnded
)

// decideDay is t's calendar date in t's own location, as midnight UTC, so a
// day handed as a local midnight is the same day.
func decideDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// accountSpan is the employments against today: whether one is current
// (started, its end today or later), one is later (starts after today), and
// the last end before today of the ended ones (ended false when none).
func accountSpan(employments []decideEmployment, today time.Time) (current, later, ended bool, lastEnd, earliestOpen time.Time) {
	for _, e := range employments {
		start := decideDay(e.Start)
		if e.End != nil && decideDay(*e.End).Before(today) {
			if end := decideDay(*e.End); !ended || end.After(lastEnd) {
				lastEnd = end
			}
			ended = true
			continue
		}
		if !start.After(today) {
			current = true
		} else {
			later = true
		}
		if earliestOpen.IsZero() || start.Before(earliestOpen) {
			earliestOpen = start
		}
	}
	return current, later, ended, lastEnd, earliestOpen
}

// accountFrom is the effective provisionFrom F the worker acts on, false
// when there is none: the stored date, else the first day of the earliest
// employment not ended by today (spec reading 14). Between employments — one
// ended before today, none current, a later one to come — the later start
// stands for F (spec: "as the row for F = S") unless the stored date falls
// after the last ended employment, so is the later one's own (an early
// provisionFrom honoured); a stored date from before then is the earlier
// employment's and is not read. With every employment ended (or none) the
// stored date is answered as stored.
func accountFrom(provisionFrom *time.Time, employments []decideEmployment, today time.Time) (time.Time, bool) {
	today = decideDay(today)
	current, later, ended, lastEnd, earliestOpen := accountSpan(employments, today)
	between := ended && !current && later
	if provisionFrom != nil {
		if stored := decideDay(*provisionFrom); !between || stored.After(lastEnd) {
			return stored, true
		}
	}
	if !current && !later {
		return time.Time{}, false
	}
	return earliestOpen, true
}

// accountPhaseOf judges today against the employments (at least one) and F
// (accountFrom), and reports whether the person is between employments.
func accountPhaseOf(p accountPersonFacts, today time.Time, createBeforeDays int) (accountPhase, bool) {
	current, later, ended, _, _ := accountSpan(p.Employments, today)
	if !current && !later {
		return phaseEnded, false
	}
	between := ended && !current
	from, _ := accountFrom(p.ProvisionFrom, p.Employments, today)
	switch {
	case !today.Before(from):
		return phaseDue, between
	case !today.Before(from.AddDate(0, 0, -createBeforeDays)):
		return phaseWindow, between
	default:
		return phaseBefore, between
	}
}

// accountEnabled is whether a Deactivate may still be needed for ref's user:
// an active user, a pending user no one disabled, and any user the directory
// deactivated — the ref hides identity's own record behind the directory's
// answer, so whether identity has disabled them is never inferred (not from
// the link's state either: an Owner may have re-enabled them since, reading
// 32). Deactivate disables identity's record whatever the directory says and
// is idempotent; the worker's chain ends on the unchanged ref.
func accountEnabled(ref *contracts.AccountRef) bool {
	switch {
	case ref.DisabledBy == contracts.DisabledByDirectory:
		return true
	case ref.State == contracts.AccountStateActive:
		return true
	case ref.State == contracts.AccountStatePending:
		return ref.DisabledBy == ""
	default:
		return false
	}
}

// decide is the accounts decision table (spec §4 "Accounts" with §9's
// account edge cases and the plan's Task 4a), first match wins:
//
//  1. Erased: nothing — a legal hold stops the erase only.
//  2. The flag off: nothing; a link is left as it is (HR disables
//     explicitly).
//  3. No employment at all: report no_employment.
//  4. A link naming a user: identity knows no such user — on HR's
//     self-service link nothing, on the worker's report failed user_gone,
//     never re-created (reading 18); the user not managed by Staff on HR's
//     self-service link: nothing, the link recorded linked (a user an Owner
//     has since adopted is managed, so acted on — plan reading 8); an Owner
//     or a SystemAdmin (AccountRef.Privileged): report blocked
//     account_privileged; the user not managed by Staff on the worker's
//     link: report account_not_managed, the link's state kept;
//     identity's employee number neither the person's nor the one last
//     synced to identity (contracts.SameEmployeeNumber): report blocked
//     mismatch and nothing else, not even at the end (B3).
//  5. Every employment ended (T > E, none later): deactivate a user who is
//     still enabled — the address need not be present — and a user the
//     directory deactivated every run, since identity disables its own
//     record whatever the directory says; otherwise nothing.
//  6. Between employments, before the later one is due: the same, and
//     before its window nothing else — no report either.
//  7. Federated mode and a pending user with no credential (created in
//     local mode): report mode_changed (reading 30), nothing else.
//  8. Before the window (T < F − createBeforeDays) with no user: nothing.
//  9. The address the flag names missing: report account_email_missing.
//  10. No user yet: prepare (federated: find).
//  11. The user's state (before T ≥ F the same, but never an activate): the
//     directory deactivated them, nothing (the directory decides); disabled
//     by an Owner (or by no one recorded), report blocked —
//     blocked_by_owner for a user still pending (the activation refused),
//     disabled_by_owner otherwise; disabled by Staff, activate (a rehire;
//     federated re-enables only); pending and disabled by Staff, activate
//     (local); pending and enabled with no credential, by the invitation —
//     active nothing, revoked by an Owner invitation_revoked and exhausted
//     invitation_unanswered unless the run is fresh, else activate.
//  12. The spec: a pending user whose address differs, update (local — the
//     address is written while pending); a non-pending user's differing
//     address, report email_differs (local; in federated mode the identity
//     provider owns it), riding with an update when the spec changed; the
//     spec changed, or identity holds the synced number of a person HR
//     renumbered, update.
func decide(in accountDecision) accountStep {
	none := accountStep{Kind: accountStepNone}
	p := in.Person
	if p.Erased || !p.Flag {
		return none
	}
	if len(p.Employments) == 0 {
		return accountStep{Kind: accountStepReport, Reason: reasonNoEmployment}
	}
	today := decideDay(in.Today)
	federated := in.Mode == contracts.AccountModeFederated
	hasUser := in.Link != nil && in.Link.ExternalID != nil
	ref := in.Ref
	if hasUser {
		switch {
		case ref == nil && in.Link.State == linkStateLinked:
			return none
		case ref == nil:
			return accountStep{Kind: accountStepReport, Reason: reasonUserGone, State: linkStateFailed}
		case !ref.Managed && in.Link.State == linkStateLinked:
			return accountStep{Kind: accountStepNone, State: linkStateLinked}
		case ref.Privileged:
			return accountStep{Kind: accountStepReport, Reason: reasonPrivileged, State: linkStateBlocked}
		case !ref.Managed:
			return accountStep{Kind: accountStepReport, Reason: reasonNotManaged}
		case !contracts.SameEmployeeNumber(ref.EmployeeNumber, p.EmployeeNumber) &&
			!contracts.SameEmployeeNumber(ref.EmployeeNumber, in.Link.SyncedEmployeeNumber):
			return accountStep{Kind: accountStepReport, Reason: reasonMismatch, State: linkStateBlocked}
		}
	}

	phase, between := accountPhaseOf(p, today, in.CreateBeforeDays)
	if phase == phaseEnded || (between && phase != phaseDue) {
		if hasUser && accountEnabled(ref) {
			return accountStep{Kind: accountStepDeactivate}
		}
		// Nothing more for a leaver, nor between employments before the
		// later one's window: nothing of it is due yet, so no report either.
		if phase == phaseEnded || phase == phaseBefore {
			return none
		}
	}
	pending := hasUser && ref.State == contracts.AccountStatePending && ref.DisabledBy != contracts.DisabledByDirectory
	if federated && pending && !ref.HasCredential {
		return accountStep{Kind: accountStepReport, Reason: reasonModeChanged, State: linkStatePending}
	}
	if phase == phaseBefore && !hasUser {
		return none
	}
	if !p.AddressPresent {
		return accountStep{Kind: accountStepReport, Reason: reasonEmailMissing}
	}
	if !hasUser {
		return accountStep{Kind: accountStepPrepare}
	}
	// Before T ≥ F nothing is activated; every report and the update still
	// apply.
	if step := decideDue(in, ref, pending); step.Kind != accountStepNone &&
		(phase == phaseDue || step.Kind != accountStepActivate) {
		return step
	}
	return decideSpec(in, ref, pending)
}

// decideDue is row 11 of decide: the user's state, as it stands once the
// account is due (decide drops an activate before then).
func decideDue(in accountDecision, ref *contracts.AccountRef, pending bool) accountStep {
	none := accountStep{Kind: accountStepNone}
	activate := accountStep{Kind: accountStepActivate, Fresh: in.Fresh}
	// An Owner's disable of a user still pending blocks the activation; of a
	// user who was enabled, it disables them. Judged on the ref, not the
	// link, so a link the report has made blocked keeps its reason.
	byOwner := func() accountStep {
		reason := reasonDisabledByOwner
		if pending {
			reason = reasonBlockedByOwner
		}
		return accountStep{Kind: accountStepReport, Reason: reason, State: linkStateBlocked}
	}
	switch {
	case ref.DisabledBy == contracts.DisabledByDirectory:
		return none
	case pending && in.Mode == contracts.AccountModeFederated:
		// Federated mode never activates a pending user (the identity
		// provider is the authority; reading 19), credential or not.
		return none
	case pending && ref.DisabledBy == contracts.DisabledByStaff:
		return activate
	case pending && ref.DisabledBy != "":
		return byOwner()
	case pending:
		if ref.HasCredential {
			return none
		}
		switch ref.Invitation.State {
		case contracts.InvitationActive:
			return none
		case contracts.InvitationRevokedByOwner:
			if !in.Fresh {
				return accountStep{Kind: accountStepReport, Reason: reasonInvitationRevoked, State: linkStatePending}
			}
		case contracts.InvitationExhausted:
			if !in.Fresh {
				return accountStep{Kind: accountStepReport, Reason: reasonInvitationUnanswered, State: linkStatePending}
			}
		}
		return activate
	case ref.State == contracts.AccountStateDisabled && ref.DisabledBy == contracts.DisabledByStaff:
		return activate
	case ref.State == contracts.AccountStateDisabled:
		return byOwner()
	default:
		return none
	}
}

// decideSpec is row 12 of decide: what the user's details need.
func decideSpec(in accountDecision, ref *contracts.AccountRef, pending bool) accountStep {
	federated := in.Mode == contracts.AccountModeFederated
	// A person HR renumbered: identity still holds the synced number, which
	// the update rewrites (the owner's answer 1: the number still syncs).
	changed := in.Link.SpecChanged || !contracts.SameEmployeeNumber(ref.EmployeeNumber, in.Person.EmployeeNumber)
	switch {
	case !federated && pending && ref.EmailDiffers:
		return accountStep{Kind: accountStepUpdate}
	case !federated && ref.EmailDiffers && changed:
		return accountStep{Kind: accountStepUpdate, Reason: reasonEmailDiffers}
	case !federated && ref.EmailDiffers:
		return accountStep{Kind: accountStepReport, Reason: reasonEmailDiffers}
	case changed:
		return accountStep{Kind: accountStepUpdate}
	default:
		return accountStep{Kind: accountStepNone}
	}
}
