package staff

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is Run now and Disable now (design S27, plan Task 4c): the
// accounts worker's cycle by hand — for everyone, or for one person with a
// fresh invitation — and the owner's answer 2, the flag turned off with the
// user disabled at once. Each runs by the worker's own code and disciplines
// (accounts_worker.go): no cycle-wide lock — a link is claimed alone, by a
// lease, in a statement that commits at once, so a run and the worker's
// ticker share the work — every identity call on no connection under the
// two-minute timeout, the result written under the person's lock on the
// claim's lease and only onto the user the claim knew. The caller is the
// actor identity audits and the account events name.

// accountsRunBudget bounds POST /accounts/run: no claim is begun after it,
// on the module's clock; a claim begun finishes under its calls' own bounds,
// so the request answers within the budget and one claim's chain.
const accountsRunBudget = 60 * time.Second

// The account runs' rate limits (privacy review L2), keyed by client address
// as every platform policy is (objects.go's upload): each run calls identity
// and may mail, so neither is left unbounded. A run of every account is a
// cycle of up to fifty claims, thirty an hour; a person's Run now and Disable
// now share one bound of a hundred and twenty an hour.
const (
	accountsRunsPerHour   = 30
	accountActionsPerHour = 120
)

var (
	policyAccountsRun = ratelimit.Policy{
		Name:    "StaffAccountsRun",
		Limit:   accountsRunsPerHour,
		Window:  time.Hour,
		Message: "Too many account runs. Please wait a little before running the accounts again.",
	}
	policyAccountAction = ratelimit.Policy{
		Name:    "StaffAccountAction",
		Limit:   accountActionsPerHour,
		Window:  time.Hour,
		Message: "Too many account runs and disables. Please wait a little before trying again.",
	}
)

// Disable now's refusals (delivery C): a user Staff does not manage, and an
// Owner, a SystemAdmin or the last active Owner — an Owner's to disable.
const (
	codeAccountNotManaged = "account_not_managed"
	codeAccountPrivileged = "account_privileged"
	codeAccountMismatch   = "account_mismatch"
)

// callerRun is the run a request makes: the caller as identity's actor and
// as the events' actor (resolved before any connection is held).
func (s *server) callerRun(ctx context.Context) (accountsRun, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return accountsRun{}, err
	}
	return accountsRun{actor: contracts.Actor{UserID: who.UserID}, who: who}, nil
}

// runMode is the authority mode for a run, asked on no connection: whether
// only deactivations may run (a mismatch), or the 503 when there is no
// provider or identity could not answer.
func (s *server) runMode(ctx context.Context) (mode string, mismatch bool, unavailable bool) {
	mode, err := withCallTimeout(ctx, s.accountsMode)
	switch {
	case errors.Is(err, contracts.ErrModeMismatch):
		return "", true, false
	case errors.Is(err, errAccountsUnavailable):
		return "", false, true
	case err != nil:
		s.logger().WarnContext(ctx, "staff: read the authority mode for a run", "error", err.Error())
		return "", false, true
	}
	return mode, false, false
}

// PostStaffAccountsRun Run the accounts now
// (POST /api/v1/staff/accounts/run)
//
// One cycle, the caller as the actor, under accountsRunBudget: what it
// attempted, did and failed, and the due links it left for the worker.
func (s *server) PostStaffAccountsRun(ctx context.Context, _ gen.PostStaffAccountsRunRequestObject) (gen.PostStaffAccountsRunResponseObject, error) {
	run, err := s.callerRun(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.Accounts == nil {
		return gen.PostStaffAccountsRun503ApplicationProblemPlusJSONResponse(accountsUnavailable()), nil
	}
	if _, _, unavailable := s.runMode(ctx); unavailable {
		return gen.PostStaffAccountsRun503ApplicationProblemPlusJSONResponse(accountsUnavailable()), nil
	}
	// The cycle goes on if the client goes away (review M3): a claim cut
	// mid-call would record a false failure and leave its lease live.
	work := context.WithoutCancel(ctx)
	run.until = s.deps.Clock().Add(accountsRunBudget)
	tally, err := s.accountsCycle(work, run)
	if err != nil {
		return nil, err
	}
	zone, err := s.settingsZone(work)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	remaining, err := store.New(s.deps.Pool).CountDueAccountLinks(work, store.CountDueAccountLinksParams{
		Today: dayParam(civilToday(now, zone)), Now: now,
	})
	if err != nil {
		return nil, fmt.Errorf("staff: count the due account links: %w", err)
	}
	return gen.PostStaffAccountsRun200JSONResponse{
		Attempted: int32(tally.attempted), Done: int32(tally.done), Failed: int32(tally.failed), Remaining: int32(remaining),
	}, nil
}

// claimPersonLink leases personID's link at now whatever its schedule,
// answering it and its lease; busy when a live lease holds it, and neither
// when there is no link.
func (s *server) claimPersonLink(ctx context.Context, personID int64, now time.Time) (claim *store.ClaimAccountLinkRow, lease uuid.UUID, busy bool, err error) {
	lease = uuid.New()
	q := store.New(s.deps.Pool)
	row, err := q.ClaimPersonAccountLink(ctx, store.ClaimPersonAccountLinkParams{
		LeaseID: lease, LeaseUntil: now.Add(accountsLeaseFor), PersonID: personID, Now: now,
	})
	if err == nil {
		c := store.ClaimAccountLinkRow(row)
		return &c, lease, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, lease, false, fmt.Errorf("staff: claim person %d's link: %w", personID, err)
	}
	link, err := readAccountLink(ctx, q, personID)
	if err != nil {
		return nil, lease, false, err
	}
	return nil, lease, link != nil, nil
}

// PostStaffPeopleByIdAccountRun Run a person's account now
// (POST /api/v1/staff/people/{id}/account/run)
//
// The account kinds seeded first, as the cycle seeds them: while they are
// not, the person's kind is not known and nothing is run (503,
// accountKindsUnseeded). Then the person's preparing link written when due,
// then their link claimed whatever its next attempt and worked with a fresh
// invitation on the first step — and a fresh pass only on the provider's
// activation that enables the account (decideProvider). A flag that is off
// runs nothing.
func (s *server) PostStaffPeopleByIdAccountRun(ctx context.Context, req gen.PostStaffPeopleByIdAccountRunRequestObject) (gen.PostStaffPeopleByIdAccountRunResponseObject, error) {
	run, err := s.callerRun(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.Accounts == nil {
		return gen.PostStaffPeopleByIdAccountRun503ApplicationProblemPlusJSONResponse(accountsUnavailable()), nil
	}
	if !s.seedAccountKinds(ctx) {
		return gen.PostStaffPeopleByIdAccountRun503ApplicationProblemPlusJSONResponse(accountKindsUnseeded()), nil
	}
	run.fresh = true
	_, mismatch, unavailable := s.runMode(ctx)
	if unavailable {
		return gen.PostStaffPeopleByIdAccountRun503ApplicationProblemPlusJSONResponse(accountsUnavailable()), nil
	}
	// The run goes on if the client goes away (review M3).
	ctx = context.WithoutCancel(ctx)
	now := s.deps.Clock()
	f, err := s.readAccountFacts(ctx, req.Id, now)
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdAccountRun404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if f.decide.Erased {
		r, _ := asRefusal(personErased())
		return gen.PostStaffPeopleByIdAccountRun409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	sent := false
	if f.decide.Flag {
		if f.provider != nil {
			run.providers = s.providerStatuses(ctx, f.provider.key)
		}
		if err := s.prepareAccountLink(ctx, req.Id, f, now); err != nil {
			return nil, err
		}
		claim, lease, busy, err := s.claimPersonLink(ctx, req.Id, now)
		switch {
		case err != nil:
			return nil, err
		case busy:
			r, _ := asRefusal(accountsBusy())
			return gen.PostStaffPeopleByIdAccountRun409ApplicationProblemPlusJSONResponse(r.problem), nil
		case claim != nil:
			worked := s.workAccountLink(ctx, run, *claim, lease, mismatch, now)
			if worked.err != nil {
				return nil, worked.err
			}
			if worked.providerRefusal != "" {
				return gen.PostStaffPeopleByIdAccountRun409ApplicationProblemPlusJSONResponse(providerRefused(worked.providerRefusal)), nil
			}
			if worked.failed {
				return gen.PostStaffPeopleByIdAccountRun503ApplicationProblemPlusJSONResponse(accountsUnavailable()), nil
			}
			sent = worked.invitationSent
		}
	}
	out, err := s.accountView(ctx, req.Id, civilToday(s.deps.Clock(), f.zone))
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdAccountRun404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdAccountRun200JSONResponse{InvitationSent: sent, Account: out}, nil
}

// PostStaffPeopleByIdAccountDisable Disable a person's account now
// (POST /api/v1/staff/people/{id}/account/disable)
//
// The owner's answer 2. First, under the person's lock: the flag written
// off (account_flag_updated, the flag's own revision — never the person's)
// and committed; a link the worker holds right now refuses accounts_busy
// there, before anything is written. Then the link claimed, its user
// deactivated with the caller as the actor through the worker's guards, and
// the result written. An erased person may be disabled: their access ends
// too.
func (s *server) PostStaffPeopleByIdAccountDisable(ctx context.Context, req gen.PostStaffPeopleByIdAccountDisableRequestObject) (gen.PostStaffPeopleByIdAccountDisableResponseObject, error) {
	run, err := s.callerRun(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var user *string
	// handLink is HR's own self-service link to a user Staff does not manage
	// (Task 6 review): the worker never acts on it, and identity would only
	// refuse it, so the flag is turned off as asked and nothing else — no
	// disable recorded, nothing called; HR unlinks it, or an Owner disables
	// the user in identity.
	handLink := false
	// external is whether the person's provider link names an account in the
	// directory not deleted (Entra design D7) — the link under their kind, or
	// one they still have under a provider their kind no longer names (review
	// M3): Disable now deactivates it first, then the identity link's user.
	external := false
	err = s.withPersonLock(ctx, req.Id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := readAccountPerson(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		key, err := personProviderKey(ctx, txq, req.Id, current.accountKind)
		if err != nil {
			return err
		}
		if key != "" {
			pl, err := txq.PersonProviderLink(ctx, store.PersonProviderLinkParams{PersonID: req.Id, Provider: key})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return fmt.Errorf("staff: read person %d's %s link: %w", req.Id, key, err)
			default:
				external = pl.ExternalID != nil && pl.State != providerStateDeleted
			}
		}
		link, err := txq.LockPersonAccountLink(ctx, req.Id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("staff: lock person %d's link: %w", req.Id, err)
		case link.LeaseUntil != nil && link.LeaseUntil.After(now):
			return accountsBusy()
		case link.State == linkStateLinked && !link.Managed:
			handLink = true
		default:
			user = link.ExternalID
		}
		// The disable asked for, recorded with the flag (review M1): what
		// does not reach identity or the provider below is the worker's to
		// finish.
		if user != nil || external {
			if err := txq.RequestAccountDisable(ctx, store.RequestAccountDisableParams{PersonID: req.Id, Now: now}); err != nil {
				return fmt.Errorf("staff: record person %d's disable: %w", req.Id, err)
			}
		}
		if !current.provisionAccount {
			return nil
		}
		kind := accountEmailKindOf(current)
		if _, err := txq.UpdatePersonAccount(ctx, store.UpdatePersonAccountParams{
			ID: req.Id, ProvisionAccount: false, ProvisionFrom: current.provisionFrom, AccountEmailKind: &kind,
		}); err != nil {
			return fmt.Errorf("staff: turn person %d's account flag off: %w", req.Id, err)
		}
		return s.recordChanges(ctx, txq, req.Id, eventAccountFlagUpdated, run.who,
			changes{"provisionAccount": fieldChange{From: true, To: false}})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdAccountDisable404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	// Committed: the rest goes on if the client goes away (review M1).
	ctx = context.WithoutCancel(ctx)
	if hook, ok := disableCommittedHooks.Load(disableHookKey{pool: s.deps.Pool, person: req.Id}); ok {
		hook.(func())()
	}
	if handLink && !external {
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(conflict(codeAccountNotManaged, "Not managed by Staff",
			"The account flag is off, but the person is linked by hand to a user Staff does not manage, so Staff does not disable them. Unlink the person, or an Owner disables the user in identity.")), nil
	}
	if user != nil || external {
		if resp, err := s.disableLinkedUser(ctx, run, req.Id, now); resp != nil || err != nil {
			return resp, err
		}
	}
	f, err := s.readAccountFacts(ctx, req.Id, s.deps.Clock())
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdAccountDisable404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.accountView(ctx, req.Id, f.today)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdAccountDisable200JSONResponse(out), nil
}

// disableLinkedUser claims personID's link and deactivates its user for
// run: the lease renewed before the call, the result written on the lease
// and only onto the user claimed. It answers a response only for a refusal
// or a failure; nil, nil when the user is disabled (or gone: nothing left
// to disable).
func (s *server) disableLinkedUser(ctx context.Context, run accountsRun, personID int64, now time.Time) (gen.PostStaffPeopleByIdAccountDisableResponseObject, error) {
	claim, lease, busy, err := s.claimPersonLink(ctx, personID, now)
	switch {
	case err != nil:
		return nil, err
	case busy:
		r, _ := asRefusal(accountsBusy())
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(r.problem), nil
	case claim == nil:
		return nil, nil
	}
	f, err := s.readAccountFacts(ctx, personID, now)
	if err != nil {
		s.releaseAccountLease(ctx, claim.ID, lease)
		if errors.Is(err, errPersonNotFound) {
			return gen.PostStaffPeopleByIdAccountDisable404Response{}, nil
		}
		return nil, err
	}
	if claim.ExternalID == nil && f.provider == nil {
		s.releaseAccountLease(ctx, claim.ID, lease)
		return nil, nil
	}
	r := newAccountResult(*claim)
	// The provider's account first (Entra design D7), whatever identity
	// then answers.
	var provider providerOutcome
	if f.provider != nil {
		run.providers = s.providerStatuses(ctx, f.provider.key)
		var user *uuid.UUID
		if claim.ExternalID != nil {
			if id, err := uuid.Parse(*claim.ExternalID); err == nil {
				user = &id
			}
		}
		provider = s.workProviderLink(ctx, run, *claim, lease, f, identityForProvider(user, nil, false), true, now)
		if provider.err != nil {
			s.releaseAccountLease(ctx, claim.ID, lease)
			return nil, provider.err
		}
		// A disable the directory did not confirm stays asked for, whatever
		// identity answers, and the worker finishes it at its next poll.
		r.keepDisable = provider.disablePending || provider.failed
		r.soon = r.soon || provider.soon
	}
	var refusal error
	if claim.ExternalID == nil {
		if !r.keepDisable {
			r.disableRequested = nil
		}
		if err := s.writeAccountResult(ctx, run, r, lease, f, now); err != nil {
			return nil, err
		}
		return providerDisableAnswer(provider), nil
	}
	id, perr := uuid.Parse(*claim.ExternalID)
	if perr != nil {
		r.answered(reasonUserGone)
		r.state = linkStateFailed
		r.disableRequested = nil
		if err := s.writeAccountResult(ctx, run, r, lease, f, now); err != nil {
			return nil, err
		}
	} else {
		worked, callErr := s.finishRequestedDisable(ctx, run, r, lease, id, f, now)
		if errors.Is(callErr, errAccountLeaseLost) {
			b, _ := asRefusal(accountsBusy())
			return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(b.problem), nil
		}
		if worked.err != nil {
			return nil, worked.err
		}
		refusal = callErr
	}
	switch {
	case refusal == nil:
		return providerDisableAnswer(provider), nil
	case r.failed:
		problem := accountsUnavailable()
		detail := "Identity could not be reached. The account flag is off; the user is not yet disabled. Try again."
		problem.Detail = &detail
		return gen.PostStaffPeopleByIdAccountDisable503ApplicationProblemPlusJSONResponse(problem), nil
	case errors.Is(refusal, contracts.ErrAccountNotManaged):
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(conflict(codeAccountNotManaged, "Not managed by Staff",
			"The account flag is off, but the user is not managed by Staff, so Staff does not disable them. An Owner disables them in identity.")), nil
	case errors.Is(refusal, contracts.ErrAccountPrivileged), errors.Is(refusal, contracts.ErrLastOwner):
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(conflict(codeAccountPrivileged, "An Owner's account",
			"The account flag is off, but the user is an Owner or a SystemAdmin, whom an Owner disables in identity.")), nil
	case errors.Is(refusal, contracts.ErrUserGone):
		return providerDisableAnswer(provider), nil
	case errors.Is(refusal, errAccountMismatch):
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(conflict(codeAccountMismatch, "Someone else's account",
			"The account flag is off, but the user the link names carries another employee number, so Staff does nothing to them. Put the person's employee number right; the disable then runs.")), nil
	default:
		return nil, fmt.Errorf("staff: Disable now for person %d: %w", personID, refusal)
	}
}

// providerDisableAnswer is Disable now's answer for the provider's part:
// nil when its account is disabled (or there was none), provider_refused
// with the code of a refusal, accounts_unavailable for a disable the
// directory did not confirm otherwise — it could not be reached, said it is
// unavailable, or no module offers it. The flag is off either way, the
// disable stays asked for, and the worker finishes it.
func providerDisableAnswer(p providerOutcome) gen.PostStaffPeopleByIdAccountDisableResponseObject {
	switch {
	case p.refusal != "":
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(providerRefused(p.refusal))
	case p.failed, p.disablePending:
		problem := accountsUnavailable()
		detail := "The external directory could not be reached. The account flag is off; the external account is not yet disabled, and the accounts worker finishes the disable when the directory answers."
		problem.Detail = &detail
		return gen.PostStaffPeopleByIdAccountDisable503ApplicationProblemPlusJSONResponse(problem)
	}
	return nil
}

// providerRefused is the 409 for a provider's refusal of Run now or Disable
// now: the code in the detail, never the provider's words.
func providerRefused(code string) gen.StaffProblem {
	return conflict(codeProviderRefused, "The identity provider refused",
		"The identity provider refused the account change: "+code+".")
}

// disableCommittedHooks is what a test runs when Disable now has committed
// the flag off and not yet claimed the link (export_test.go), keyed by the
// installation's pool and the person — every test's database numbers its
// people alike, so a person id alone is another test's person too; empty in
// production.
var disableCommittedHooks sync.Map

// disableHookKey is a disableCommittedHooks key: one installation's person.
type disableHookKey struct {
	pool   *pgxpool.Pool
	person int64
}
