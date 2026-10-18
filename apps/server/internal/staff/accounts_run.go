package staff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
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

// Disable now's refusals (delivery C): a user Staff does not manage, and an
// Owner, a SystemAdmin or the last active Owner — an Owner's to disable.
const (
	codeAccountNotManaged = "account_not_managed"
	codeAccountPrivileged = "account_privileged"
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
	run.until = s.deps.Clock().Add(accountsRunBudget)
	tally, err := s.accountsCycle(ctx, run)
	if err != nil {
		return nil, err
	}
	remaining, err := store.New(s.deps.Pool).CountDueAccountLinks(ctx, s.deps.Clock())
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
// The person's preparing link written when due, then their link claimed
// whatever its next attempt and worked with a fresh invitation on the first
// step. A flag that is off runs nothing.
func (s *server) PostStaffPeopleByIdAccountRun(ctx context.Context, req gen.PostStaffPeopleByIdAccountRunRequestObject) (gen.PostStaffPeopleByIdAccountRunResponseObject, error) {
	run, err := s.callerRun(ctx)
	if err != nil {
		return nil, err
	}
	run.fresh = true
	mode, mismatch, unavailable := s.runMode(ctx)
	if unavailable {
		return gen.PostStaffPeopleByIdAccountRun503ApplicationProblemPlusJSONResponse(accountsUnavailable()), nil
	}
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
		if err := s.prepareAccountLink(ctx, req.Id, f, mode, now); err != nil {
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
			worked := s.workAccountLink(ctx, run, *claim, lease, mode, mismatch, now)
			if worked.err != nil {
				return nil, worked.err
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
	err = s.withPersonLock(ctx, req.Id, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := readAccountPerson(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		link, err := txq.LockPersonAccountLink(ctx, req.Id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("staff: lock person %d's link: %w", req.Id, err)
		case link.LeaseUntil != nil && link.LeaseUntil.After(now):
			return accountsBusy()
		default:
			user = link.ExternalID
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
	if user != nil {
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
	log := s.logger().With("person_id", personID)
	claim, lease, busy, err := s.claimPersonLink(ctx, personID, now)
	switch {
	case err != nil:
		return nil, err
	case busy:
		r, _ := asRefusal(accountsBusy())
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(r.problem), nil
	case claim == nil || claim.ExternalID == nil:
		return nil, nil
	}
	f, err := s.readAccountFacts(ctx, personID, now)
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdAccountDisable404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	r := newAccountResult(*claim)
	step := accountStep{Kind: accountStepDeactivate}
	id, perr := uuid.Parse(*claim.ExternalID)
	var refusal error
	switch {
	case perr != nil:
		r.answered(reasonUserGone)
		r.state = linkStateFailed
	case !s.renewAccountLease(ctx, claim.ID, lease):
		r, _ := asRefusal(accountsBusy())
		return gen.PostStaffPeopleByIdAccountDisable409ApplicationProblemPlusJSONResponse(r.problem), nil
	default:
		got, err := s.callAccountStep(ctx, run, step, &id, f)
		if err != nil {
			if _, _, answer := accountCallCode(err); !answer {
				log.WarnContext(ctx, "staff: Disable now's call failed", "error", err.Error())
			}
			r.failedCall(step, err)
			refusal = err
		} else {
			r.succeeded(step, nil, got, f, now)
		}
	}
	if err := s.writeAccountResult(ctx, run, r, lease, f, now); err != nil {
		return nil, err
	}
	switch {
	case refusal == nil:
		return nil, nil
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
		return nil, nil
	default:
		return nil, fmt.Errorf("staff: Disable now for person %d: %w", personID, refusal)
	}
}
