package staff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the staff-accounts worker (design S27, §4 "Accounts" and
// Locking; plan Task 4b): every poll it gives the people whose account flag
// is on the user the decision table (accounts_decide.go) says they should
// have — prepared, disabled, createBeforeDays before the start, enabled on
// the day, disabled the day after the last end — through identity's
// contracts.AccountProvisioning.
//
// It holds no cycle-wide lock and no lease connection (pre-flight B2; PR
// #124 is the counter-example). A cycle:
//
//  1. For every person with the flag on, not erased and with no link, whose
//     account the decision table says is due to be prepared, it writes a
//     preparing link under the person's lock, INSERT … ON CONFLICT DO
//     NOTHING, so two cycles write one.
//  2. It claims up to fifty due links, one at a time, each by a conditional
//     UPDATE … FOR UPDATE SKIP LOCKED that sets a lease (lease_id,
//     lease_until five minutes on) and commits at once: a link another
//     cycle holds is skipped, and two cycles share the work.
//  3. Per claim: the person's facts in a read-only snapshot, closed; the
//     link's user as identity reports them (Account, with the person's own
//     address); decide; each step called under a two-minute timeout with no
//     transaction open and no connection held; after each successful call
//     the user read again and decide run again on the fresh ref, up to three
//     calls, until it answers nothing or a report, or the ref is unchanged
//     (the header of accounts_decide.go).
//  4. The result under the person's lock, then the link WHERE lease_id is
//     the claim's — a link claimed since by another cycle is not written, and
//     a link gone (the person deleted) has the user a Prepare of this claim
//     made deactivated, outside any transaction, and logged. The flag is read
//     again under the lock: one turned off meanwhile is recorded on the
//     events (flagOffMeanwhile), and the next cycle claims the link no more.
//
// The worker writes only the link and the account events: never the person
// (their revision and updated_at, which a manager reads, stay as they are),
// never user_linked or user_unlinked. last_error and every event hold codes
// and states, never an address or a driver's words (pre-flight I5).
//
// A mode mismatch (identity's Mode answering contracts.ErrModeMismatch: this
// process's OIDC or SCIM settings differ from the API's) holds every step but
// deactivate, which reads no mode, so a leaver is disabled whatever the
// configuration says; the links it holds record mode_mismatch, from which the
// installation's attention item is derived (Task 4c).

const (
	// accountsWorkerName is what the runner logs this worker as.
	accountsWorkerName = "staff-accounts"
	// accountsBatch bounds one cycle at fifty claims; a backlog drains over
	// cycles.
	accountsBatch = 50
	// accountsLeaseFor is how long a claim holds its link from its last
	// renewal: the lease is renewed before each identity call, and before
	// each read of the user after a call — the first read rides on the
	// claim's fresh lease — (RenewAccountLease), each bounded by
	// accountsCallTimeout, so a
	// claim alive keeps its link however long its chain, and a crashed one
	// is taken over within five minutes. A renewal that finds the lease
	// lapsed or taken stops the chain.
	accountsLeaseFor = 5 * time.Minute
	// accountsCallTimeout bounds each identity call.
	accountsCallTimeout = 2 * time.Minute
	// accountsMaxCalls is the most calls one claim chains (plan I2).
	accountsMaxCalls = 3
	// accountsMaxAttempts is how many failed attempts are retried at each
	// poll before the link is retried daily.
	accountsMaxAttempts = 3
)

// The codes a call's refusal is recorded as, beside decide's own reasons
// (accounts_decide.go): the link's blocked_reason, or for a failure to
// retry its last_error.
const (
	reasonNotFound            = "not_found"
	reasonEmployeeRoleMissing = "employee_role_missing"
	reasonEmployeeRoleUnsafe  = "employee_role_unsafe"
	reasonEmailTaken          = "account_email_taken"
	reasonNumberTaken         = "employee_number_taken"
	reasonModeMismatch        = "mode_mismatch"
	// reasonInvalidActor is a bug of this module's (the call named no valid
	// actor): logged, and the link failed until it is fixed.
	reasonInvalidActor = "invalid_actor"
	// errorAccountsUnavailable is last_error for a call that could not be
	// made — identity unreachable, a timeout, any answer that is not one of
	// the contract's — retried.
	errorAccountsUnavailable = "accounts_unavailable"
)

// accountsRun is who a cycle acts for: the actor identity audits, the actor
// the account events name, whether HR asked for a fresh invitation (Run
// now, Task 4c), which only the first decide of a claim is given, and the
// instant after which a cycle run by hand begins no new claim (zero: none —
// the worker's own cycle).
type accountsRun struct {
	actor contracts.Actor
	who   actor
	fresh bool
	until time.Time
}

// accountsWorkerRun is the worker's own: the system actor in identity, the
// installation in the history.
var accountsWorkerRun = accountsRun{
	actor: contracts.Actor{System: contracts.SystemActorStaffAccounts},
	who:   actor{Kind: actorKindSystem, Display: systemActorDisplayName},
}

// accountsTally is what a cycle did: the links claimed, those whose result
// was written as done or refused, and those failed to retry.
type accountsTally struct {
	attempted, done, failed int
}

// AccountsWorker provisions the accounts the flag asks for. It implements
// worker.Worker.
type AccountsWorker struct {
	deps module.Deps
	srv  *server
}

var _ worker.Worker = (*AccountsWorker)(nil)

// NewAccountsWorker builds the worker over d.
func NewAccountsWorker(d module.Deps) *AccountsWorker {
	return &AccountsWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *AccountsWorker) Name() string { return accountsWorkerName }

// Interval is the poll (STAFF_WORKERS_POLL, default an hour).
func (w *AccountsWorker) Interval() time.Duration { return w.srv.workersPoll() }

// Run is the worker loop: a cycle, then the poll. A failure is logged and
// the loop goes on.
func (w *AccountsWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.srv.logger().Error("staff: the accounts cycle failed", "worker", accountsWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle, the worker as the actor.
func (w *AccountsWorker) RunCycle(ctx context.Context) error {
	_, err := w.srv.accountsCycle(ctx, accountsWorkerRun)
	return err
}

// workersPoll is the Staff workers' poll: STAFF_WORKERS_POLL, or an hour
// for Deps without a Config.
func (s *server) workersPoll() time.Duration {
	if s.deps.Config != nil && s.deps.Config.StaffWorkersPoll > 0 {
		return s.deps.Config.StaffWorkersPoll
	}
	return defaultWorkersPoll
}

// accountsCycle is one cycle for run. No provider is nothing to do. The mode
// is asked once, on no connection; identity unable to answer fails the
// cycle, which the next poll runs again.
func (s *server) accountsCycle(ctx context.Context, run accountsRun) (accountsTally, error) {
	var tally accountsTally
	if s.deps.Accounts == nil {
		s.logger().DebugContext(ctx, "staff: no module provides accounts; the accounts cycle does nothing", "worker", accountsWorkerName)
		return tally, nil
	}
	mode, err := withCallTimeout(ctx, s.accountsMode)
	mismatch := errors.Is(err, contracts.ErrModeMismatch)
	switch {
	case mismatch:
		mode = ""
		s.logger().WarnContext(ctx, "staff: this process's authority mode differs from the recorded one; only deactivations run",
			"worker", accountsWorkerName)
	case err != nil:
		return tally, fmt.Errorf("staff: read the authority mode: %w", err)
	}
	if err := s.prepareAccountLinks(ctx, mode); err != nil {
		return tally, err
	}
	zone, err := s.settingsZone(ctx)
	if err != nil {
		return tally, err
	}
	for tally.attempted < accountsBatch {
		now := s.deps.Clock()
		if !run.until.IsZero() && !now.Before(run.until) {
			break
		}
		lease := uuid.New()
		claim, err := store.New(s.deps.Pool).ClaimAccountLink(ctx, store.ClaimAccountLinkParams{
			LeaseID: lease, LeaseUntil: now.Add(accountsLeaseFor), Today: dayParam(civilToday(now, zone)), Now: now,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return tally, fmt.Errorf("staff: claim an account link: %w", err)
		}
		tally.attempted++
		if s.workAccountLink(ctx, run, claim, lease, mode, mismatch, now).ok() {
			tally.done++
		} else {
			tally.failed++
		}
	}
	return tally, nil
}

// settingsZone is the settings' business time zone, read on the pool: the
// claim judges a leaver's last end on the civil day there.
func (s *server) settingsZone(ctx context.Context) (*time.Location, error) {
	set, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, fmt.Errorf("staff: read the settings for the accounts cycle: %w", err)
	}
	return zoneOf(set), nil
}

// withCallTimeout runs one identity call under accountsCallTimeout.
func withCallTimeout[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, accountsCallTimeout)
	defer cancel()
	return call(ctx)
}

// accountFacts is what one claim reads of the person, in one snapshot: the
// account columns, the person's row, decide's view of them and the civil day
// in the settings zone.
type accountFacts struct {
	person           accountPerson
	row              store.GetPersonRow
	decide           accountPersonFacts
	today            time.Time
	zone             *time.Location
	createBeforeDays int
}

// readAccountFacts reads personID's facts at now in one read-only snapshot,
// closed before it answers; errPersonNotFound for no such person.
func (s *server) readAccountFacts(ctx context.Context, personID int64, now time.Time) (accountFacts, error) {
	var f accountFacts
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		set, err := settings(ctx, q)
		if err != nil {
			return err
		}
		f.zone = zoneOf(set)
		f.today = civilToday(now, f.zone)
		f.createBeforeDays = int(set.CreateBeforeDays)
		if f.person, err = readAccountPerson(ctx, q, personID); err != nil {
			return err
		}
		if f.row, err = q.GetPerson(ctx, personID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errPersonNotFound
			}
			return fmt.Errorf("staff: read person %d: %w", personID, err)
		}
		employments, err := accountEmployments(ctx, q, personID)
		if err != nil {
			return err
		}
		var from *time.Time
		if f.person.provisionFrom.Valid {
			from = &f.person.provisionFrom.Time
		}
		f.decide = accountPersonFacts{
			Erased: f.row.ErasedAt != nil, Flag: f.person.provisionAccount, ProvisionFrom: from,
			AddressPresent: accountAddress(f.person) != "", EmployeeNumber: f.row.EmployeeNumber, Employments: employments,
		}
		return nil
	})
	return f, err
}

// spec is what the person's user should look like.
func (f accountFacts) spec() contracts.AccountSpec {
	return contracts.AccountSpec{
		Email: accountAddress(f.person), DisplayName: f.row.DisplayName,
		PreferredLanguage: f.row.PreferredLanguage, EmployeeNumber: f.row.EmployeeNumber,
	}
}

// specHash is last_spec_hash: SHA-256 over the address (only while it
// follows the person's, AccountRef.EmailFollows — once the first invitation
// went out identity's address is an Owner's, the owner's decision D1), the
// display name, the language and the employee number.
func (f accountFacts) specHash(follows bool) []byte {
	email := ""
	if follows {
		email = accountAddress(f.person)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{email, f.row.DisplayName, f.row.PreferredLanguage, f.row.EmployeeNumber}, "|")))
	return sum[:]
}

// nextMidnight is the first instant of the civil day after f.today in the
// settings zone: a link that succeeded is acted on once per civil day,
// however long a daylight-saving change makes the day.
func (f accountFacts) nextMidnight() time.Time {
	y, m, d := f.today.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, f.zone)
}

// prepareAccountLinks writes a preparing link for every person the decision
// table says is due to be prepared, each under the person's lock, the flag
// and the erasure judged again there.
func (s *server) prepareAccountLinks(ctx context.Context, mode string) error {
	ids, err := store.New(s.deps.Pool).AccountCandidates(ctx)
	if err != nil {
		return fmt.Errorf("staff: read the people due an account: %w", err)
	}
	for _, id := range ids {
		now := s.deps.Clock()
		f, err := s.readAccountFacts(ctx, id, now)
		if errors.Is(err, errPersonNotFound) {
			continue
		}
		if err != nil {
			s.logger().ErrorContext(ctx, "staff: read a person due an account", "worker", accountsWorkerName,
				"person_id", id, "error", err.Error())
			continue
		}
		if err := s.prepareAccountLink(ctx, id, f, mode, now); err != nil {
			s.logger().ErrorContext(ctx, "staff: write a preparing link", "worker", accountsWorkerName,
				"person_id", id, "error", err.Error())
		}
	}
	return nil
}

// prepareAccountLink writes personID's preparing link when the decision
// table, on f (read at now), says the account is due to be prepared and the
// person has no link: under the person's lock, the flag and the erasure
// judged again there. A person gone or erased meanwhile is nothing to do.
func (s *server) prepareAccountLink(ctx context.Context, id int64, f accountFacts, mode string, now time.Time) error {
	step := decide(accountDecision{Person: f.decide, Mode: mode, CreateBeforeDays: f.createBeforeDays, Today: f.today})
	if step.Kind != accountStepPrepare {
		return nil
	}
	err := s.withPersonLock(ctx, id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		p, err := readAccountPerson(ctx, txq, id)
		if err != nil || !p.provisionAccount {
			return err
		}
		if err := txq.InsertPreparingLink(ctx, store.InsertPreparingLinkParams{PersonID: id, Now: now}); err != nil {
			return fmt.Errorf("staff: write person %d's preparing link: %w", id, err)
		}
		return nil
	})
	if _, refused := asRefusal(err); refused || errors.Is(err, errPersonNotFound) {
		return nil
	}
	return err
}

// accountEventPayload is an account event's payload (plan Global
// Constraints, "The account events"): the step, the link's state after it,
// the code of a refusal or a failure, a pending user's invitation after an
// activation, and whether the flag was turned off meanwhile — codes and
// states only.
type accountEventPayload struct {
	Step             string                  `json:"step"`
	State            string                  `json:"state"`
	Code             string                  `json:"code,omitempty"`
	Invitation       *accountEventInvitation `json:"invitation,omitempty"`
	FlagOffMeanwhile bool                    `json:"flagOffMeanwhile,omitempty"`
}

type accountEventInvitation struct {
	State  string `json:"state"`
	Issued int    `json:"issued"`
}

// accountEventClasses is every account event's payload_classes: class C,
// as the link's columns are, passed explicitly — the payload names no field
// of fieldClasses.
var accountEventClasses = []string{"C"}

type accountEvent struct {
	kind    string
	payload accountEventPayload
}

// accountResult is what a claim writes to its link, built as the claim
// goes: it starts as the claimed row.
type accountResult struct {
	claim      store.ClaimAccountLinkRow
	externalID *string
	state      string
	managed    bool
	reason     *string
	lastError  *string
	attempts   int32
	syncedAt   *time.Time
	specHash   []byte
	synced     *string
	// current is the user the link names as far as this claim knows: the
	// claimed row's, then the one RecordPreparedUser wrote. The result is
	// written only onto a link still naming it.
	current *string
	// failed is a call that could not be made, retried; soon is a link to
	// claim again at the next poll rather than the next civil day (held by
	// a mode mismatch, the chain's three calls spent with more to do, a
	// re-read that failed after a call, or a deactivation that failed — a
	// leaver loses access as soon as identity answers).
	failed, soon bool
	// created is the user this claim's Prepare created
	// (AccountRef.Created): deactivated when the result finds the link gone.
	// A user Prepare only found is never deactivated for it.
	created *uuid.UUID
	// disableRequested is the link's disable_requested_at: the disable
	// Disable now asked for, kept until identity confirms it (review M1).
	disableRequested *time.Time
	events           []accountEvent
}

func newAccountResult(claim store.ClaimAccountLinkRow) *accountResult {
	return &accountResult{
		claim: claim, externalID: claim.ExternalID, state: claim.State, managed: claim.Managed,
		reason: claim.BlockedReason, lastError: claim.LastError, attempts: claim.Attempts,
		syncedAt: claim.LastSyncedAt, specHash: claim.LastSpecHash, synced: claim.SyncedEmployeeNumber,
		current: claim.ExternalID, disableRequested: claim.DisableRequestedAt,
	}
}

// mismatched records a user the link may not name — another link names
// them — as a mismatch: the link as claimed, its worker columns too, blocked.
func (r *accountResult) mismatched() {
	c := r.claim
	r.externalID, r.current, r.managed = c.ExternalID, c.ExternalID, c.Managed
	r.synced, r.syncedAt, r.specHash = c.SyncedEmployeeNumber, c.LastSyncedAt, c.LastSpecHash
	r.created = nil
	r.state = linkStateBlocked
	r.answered(reasonMismatch)
	r.events = nil
	// Recorded once: a link already blocked on the mismatch, claimed again
	// on its schedule, records nothing new (review LOW 1).
	if c.State != linkStateBlocked || deref(c.BlockedReason) != reasonMismatch {
		r.event(eventAccountMismatched, string(accountStepPrepare), reasonMismatch)
	}
}

// deactivationDue is whether the person's facts alone, the user unread, say
// a Deactivate may be due: not erased, an employment, and every employment
// ended — or, the flag on, between employments before the later one is due
// (with the flag off only a leaver's account is deactivated, D2).
func deactivationDue(f accountFacts) bool {
	p := f.decide
	if p.Erased || len(p.Employments) == 0 {
		return false
	}
	phase, between := accountPhaseOf(p, decideDay(f.today), f.createBeforeDays)
	return phase == phaseEnded || (p.Flag && between && phase != phaseDue)
}

// stateOfRef is the link state ref's user reads as, current the link's: a
// pending user Staff disabled after an enable stays disabled to the link,
// though identity still reports them pending.
func stateOfRef(ref contracts.AccountRef, current string) string {
	switch ref.State {
	case contracts.AccountStateActive:
		return linkStateActive
	case contracts.AccountStateDisabled:
		return linkStateDisabled
	}
	if ref.DisabledBy != "" && current == linkStateDisabled {
		return linkStateDisabled
	}
	return linkStatePending
}

// answered clears the failure: whatever was asked was answered.
func (r *accountResult) answered(reason string) {
	r.reason, r.lastError, r.attempts = nil, nil, 0
	if reason != "" {
		r.reason = &reason
	}
}

// reported records a non-call step: nothing to do, or a report.
func (r *accountResult) reported(step accountStep, ref *contracts.AccountRef) {
	if ref != nil {
		r.managed = ref.Managed
	}
	switch {
	case step.State != "":
		r.state = step.State
	case step.Kind == accountStepNone && ref != nil:
		r.state = stateOfRef(*ref, r.state)
	}
	r.answered(step.Reason)
	if step.Kind == accountStepReport && r.changed() {
		r.event(reportEventKind(step.Reason, r.state), string(step.Kind), step.Reason)
	}
}

// changed is whether the state or the reason differs from the claimed row's.
func (r *accountResult) changed() bool {
	return r.state != r.claim.State || deref(r.reason) != deref(r.claim.BlockedReason)
}

// reportEventKind is the event a report or a refusal is recorded as.
func reportEventKind(reason, state string) string {
	switch {
	case reason == reasonMismatch:
		return eventAccountMismatched
	case state == linkStateFailed:
		return eventAccountFailed
	default:
		return eventAccountBlocked
	}
}

func (r *accountResult) event(kind, step, code string) {
	r.events = append(r.events, accountEvent{kind: kind, payload: accountEventPayload{Step: step, State: r.state, Code: code}})
}

// held records a step held by the mode mismatch: the state kept, the reason
// mode_mismatch, claimed again at the next poll.
func (r *accountResult) held(step accountStep) {
	r.answered(reasonModeMismatch)
	r.soon = true
	if r.changed() {
		r.event(eventAccountBlocked, string(step.Kind), reasonModeMismatch)
	}
}

// succeeded records a call identity carried out, at now; before is the
// user as the call was decided on (nil for a Prepare).
func (r *accountResult) succeeded(step accountStep, before *contracts.AccountRef, got contracts.AccountRef, f accountFacts, now time.Time) {
	id := got.UserID.String()
	if step.Kind == accountStepPrepare && got.Created {
		r.created = &got.UserID
	}
	prior := r.state
	r.externalID = &id
	r.managed = got.Managed
	r.syncedAt = &now
	r.answered("")
	if step.Kind == accountStepDeactivate {
		r.state = linkStateDisabled
	} else {
		r.state = stateOfRef(got, r.state)
	}
	payload := accountEventPayload{Step: string(step.Kind), State: r.state}
	var kind string
	switch step.Kind {
	case accountStepPrepare:
		kind = eventAccountPrepared
		number := f.row.EmployeeNumber
		r.synced = &number
	case accountStepActivate:
		kind = eventAccountActivated
		if got.State == contracts.AccountStatePending {
			payload.Invitation = &accountEventInvitation{State: got.Invitation.State, Issued: got.Invitation.Issued}
		}
	case accountStepDeactivate:
		// A user the directory keeps inactive is deactivated every run,
		// changing nothing after the first: recorded only when the link's
		// state moved or the user changed.
		unchanged := before != nil && *before == got
		if prior == linkStateDisabled && unchanged {
			return
		}
		kind = eventAccountDeactivated
	case accountStepUpdate:
		kind = eventAccountUpdated
		number := f.row.EmployeeNumber
		r.synced = &number
		r.specHash = f.specHash(got.EmailFollows)
	}
	r.events = append(r.events, accountEvent{kind: kind, payload: payload})
}

// failedCall records a call's error: one of the contract's refusals as the
// link's blocked_reason, anything else a failure retried.
func (r *accountResult) failedCall(step accountStep, err error) {
	code, state, answer := accountCallCode(err)
	if !answer {
		r.failed = true
		r.attempts = r.claim.Attempts + 1
		code := errorAccountsUnavailable
		first := r.lastError == nil
		r.lastError = &code
		if step.Kind == accountStepDeactivate {
			r.soon = true
		}
		if first {
			r.event(eventAccountFailed, string(step.Kind), code)
		}
		return
	}
	if state != "" {
		r.state = state
	}
	r.answered(code)
	if r.changed() {
		r.event(reportEventKind(code, r.state), string(step.Kind), code)
	}
}

// accountCallCode maps a call's error to the code it is recorded as and the
// link state it leaves ("" to keep the link's), answer false for a failure
// to retry.
func accountCallCode(err error) (code, state string, answer bool) {
	for _, m := range []struct {
		err         error
		code, state string
	}{
		{contracts.ErrAccountPrivileged, reasonPrivileged, linkStateBlocked},
		{contracts.ErrLastOwner, reasonPrivileged, linkStateBlocked},
		{contracts.ErrBlockedByOwner, reasonBlockedByOwner, linkStateBlocked},
		{contracts.ErrAccountNotFound, reasonNotFound, linkStateBlocked},
		{contracts.ErrUserGone, reasonUserGone, linkStateFailed},
		{contracts.ErrAccountNotManaged, reasonNotManaged, ""},
		{contracts.ErrEmployeeRoleMissing, reasonEmployeeRoleMissing, linkStateBlocked},
		{contracts.ErrEmployeeRoleUnsafe, reasonEmployeeRoleUnsafe, linkStateBlocked},
		{contracts.ErrAccountEmailTaken, reasonEmailTaken, linkStateBlocked},
		{contracts.ErrEmployeeNumberTaken, reasonNumberTaken, linkStateBlocked},
		{contracts.ErrModeMismatch, reasonModeMismatch, ""},
		{contracts.ErrInvalidActor, reasonInvalidActor, linkStateFailed},
	} {
		if errors.Is(err, m.err) {
			return m.code, m.state, true
		}
	}
	return "", "", false
}

// isCall is whether step is one identity carries out.
func (step accountStep) isCall() bool {
	switch step.Kind {
	case accountStepPrepare, accountStepActivate, accountStepDeactivate, accountStepUpdate:
		return true
	}
	return false
}

// readAccountRef is the link's user as identity reports them, compared with
// the person's own address, under the call timeout.
func (s *server) readAccountRef(ctx context.Context, id uuid.UUID, f accountFacts) (*contracts.AccountRef, error) {
	return withCallTimeout(ctx, func(ctx context.Context) (*contracts.AccountRef, error) {
		return s.accountsAccount(ctx, id, &f.person)
	})
}

// callAccountStep makes step's call for run, under the call timeout.
func (s *server) callAccountStep(ctx context.Context, run accountsRun, step accountStep, user *uuid.UUID, f accountFacts) (contracts.AccountRef, error) {
	return withCallTimeout(ctx, func(ctx context.Context) (contracts.AccountRef, error) {
		switch step.Kind {
		case accountStepPrepare:
			return s.accountsPrepare(ctx, run.actor, f.spec())
		case accountStepActivate:
			return s.accountsActivate(ctx, run.actor, *user, contracts.ActivateOpts{FreshInvitation: step.Fresh})
		case accountStepDeactivate:
			return s.accountsDeactivate(ctx, run.actor, *user)
		default:
			return s.accountsUpdate(ctx, run.actor, *user, f.spec())
		}
	})
}

// accountWorked is how one claim ended: whether a call could not be made
// (failed, retried), whether its facts or its result could not be read or
// written (err), and whether identity sent the person a new invitation.
type accountWorked struct {
	failed         bool
	err            error
	invitationSent bool
}

// ok is whether the claim ended without a failure to retry.
func (w accountWorked) ok() bool { return !w.failed && w.err == nil }

// invitationSent is whether a pending user's invitation became one that can
// be accepted between before (nil: no user yet) and after: identity never
// re-sends an open invitation, so an open one before is none sent.
func invitationSent(before, after *contracts.AccountRef) bool {
	if after == nil || after.State != contracts.AccountStatePending || after.Invitation.State != contracts.InvitationActive {
		return false
	}
	return before == nil || before.Invitation.State != contracts.InvitationActive
}

// workAccountLink works one claimed link and writes its result.
func (s *server) workAccountLink(ctx context.Context, run accountsRun, claim store.ClaimAccountLinkRow, lease uuid.UUID, mode string, mismatch bool, now time.Time) accountWorked {
	log := s.logger().With("worker", accountsWorkerName, "person_id", claim.PersonID)
	r := newAccountResult(claim)
	f, err := s.readAccountFacts(ctx, claim.PersonID, now)
	if errors.Is(err, errPersonNotFound) {
		return accountWorked{}
	}
	if err != nil {
		log.ErrorContext(ctx, "staff: read a person's account facts", "error", err.Error())
		s.releaseAccountLease(ctx, claim.ID, lease)
		return accountWorked{err: err}
	}
	var user *uuid.UUID
	if claim.ExternalID != nil {
		id, err := uuid.Parse(*claim.ExternalID)
		if err != nil {
			log.ErrorContext(ctx, "staff: a link names no user id identity could know")
			r.answered(reasonUserGone)
			r.state = linkStateFailed
			r.disableRequested = nil
			return accountWorked{failed: r.failed, err: s.writeAccountResult(ctx, run, r, lease, f, now)}
		}
		user = &id
	}
	// A disable Disable now asked for, the flag still off or the person
	// erased: the table's "flag off, nothing" does not hold for it — the user
	// is read first, and deactivated unless identity reports them disabled
	// already (re-check MEDIUM-1), at every poll while it fails, as a
	// leaver's is (review M1). The flag on again, for a person not erased, is
	// HR's wish for the account: the request lapses.
	if r.disableRequested != nil {
		if !f.decide.Flag || f.decide.Erased {
			if user == nil {
				r.disableRequested = nil
				return accountWorked{err: s.writeAccountResult(ctx, run, r, lease, f, now)}
			}
			worked, _ := s.finishRequestedDisable(ctx, run, r, lease, *user, f, now)
			return worked
		}
		r.disableRequested = nil
	}
	var ref *contracts.AccountRef
	if user != nil {
		if ref, err = s.readAccountRef(ctx, *user, f); err != nil {
			log.WarnContext(ctx, "staff: read a linked account", "error", err.Error())
			r.failedCall(accountStep{Kind: accountStepNone}, err)
			// A leaver's read is retried at every poll, as their Deactivate is
			// (reading 25): judged on the facts alone, since the user could not
			// be read (review MEDIUM 1).
			if claim.State != linkStateDisabled && deactivationDue(f) {
				r.soon = true
			}
			return accountWorked{failed: r.failed, err: s.writeAccountResult(ctx, run, r, lease, f, now)}
		}
	}
	initial := ref
	link := accountLinkFacts{
		State: claim.State, ExternalID: claim.ExternalID, Managed: claim.Managed,
		SpecChanged:          ref != nil && !bytes.Equal(f.specHash(ref.EmailFollows), claim.LastSpecHash),
		SyncedEmployeeNumber: deref(claim.SyncedEmployeeNumber),
	}
	in := accountDecision{Person: f.decide, Link: &link, Ref: ref, Mode: mode, CreateBeforeDays: f.createBeforeDays, Today: f.today, Fresh: run.fresh}
	for calls := 0; ; calls++ {
		step := decide(in)
		if !step.isCall() {
			r.reported(step, ref)
			break
		}
		if mismatch && step.Kind != accountStepDeactivate {
			log.InfoContext(ctx, "staff: an account step held by the authority mode mismatch", "step", string(step.Kind))
			r.held(step)
			break
		}
		if calls == accountsMaxCalls {
			r.reported(accountStep{Kind: accountStepNone}, ref)
			r.soon = true
			break
		}
		if !s.renewAccountLease(ctx, claim.ID, lease) {
			log.InfoContext(ctx, "staff: an account claim's lease lapsed; the chain stops", "step", string(step.Kind))
			break
		}
		got, err := s.callAccountStep(ctx, run, step, user, f)
		if err != nil {
			if errors.Is(err, contracts.ErrInvalidActor) {
				log.ErrorContext(ctx, "staff: an account call named no valid actor", "step", string(step.Kind))
			} else if _, _, answer := accountCallCode(err); !answer {
				log.WarnContext(ctx, "staff: an account call failed", "step", string(step.Kind), "error", err.Error())
			}
			r.failedCall(step, err)
			break
		}
		r.succeeded(step, ref, got, f, now)
		if user == nil {
			// The user Prepare answered is recorded on the link before
			// anything more is done with them (review HIGH 1): a user another
			// link names is a mismatch, and nothing is called for them.
			if !s.recordPreparedUser(ctx, r, lease, got, f, now) {
				break
			}
			id := got.UserID
			user = &id
		}
		if !s.renewAccountLease(ctx, claim.ID, lease) {
			log.InfoContext(ctx, "staff: an account claim's lease lapsed; the chain stops", "step", string(step.Kind))
			break
		}
		fresh, err := s.readAccountRef(ctx, *user, f)
		if err != nil {
			// The call stood; the next poll reads the user again.
			log.WarnContext(ctx, "staff: read a linked account after a call", "error", err.Error())
			r.soon = true
			break
		}
		unchanged := ref != nil && fresh != nil && *ref == *fresh
		ref = fresh
		link.ExternalID, link.State = r.externalID, r.state
		if ref != nil {
			link.Managed = ref.Managed
		}
		if step.Kind == accountStepPrepare || step.Kind == accountStepUpdate {
			link.SyncedEmployeeNumber = f.row.EmployeeNumber
		}
		// The spec as the link now records it: after an Update the one it
		// wrote, so the re-run asks for no second; after a Prepare the user
		// is compared with it for the first time.
		link.SpecChanged = step.Kind != accountStepUpdate && ref != nil &&
			!bytes.Equal(f.specHash(ref.EmailFollows), r.specHash)
		in.Link, in.Ref, in.Fresh = &link, ref, false
		if unchanged {
			if next := decide(in); !next.isCall() {
				r.reported(next, ref)
			} else {
				r.reported(accountStep{Kind: accountStepNone}, ref)
			}
			break
		}
	}
	err = s.writeAccountResult(ctx, run, r, lease, f, now)
	return accountWorked{failed: r.failed, err: err, invitationSent: err == nil && invitationSent(initial, ref)}
}

// writeAccountResult writes r under the person's lock, then the link on the
// claim's lease, re-reading the flag (plan M9). A link gone has the user a
// Prepare of this claim made deactivated, outside the transaction; a link
// claimed since is left to its new holder. A user another link already names
// is recorded as a mismatch instead. It answers the error the result could
// not be written with, logged.
func (s *server) writeAccountResult(ctx context.Context, run accountsRun, r *accountResult, lease uuid.UUID, f accountFacts, now time.Time) error {
	log := s.logger().With("worker", accountsWorkerName, "person_id", r.claim.PersonID)
	next := f.nextMidnight()
	switch {
	case r.failed && r.attempts < accountsMaxAttempts, r.soon:
		next = now.Add(s.workersPoll())
	}
	gone := false
	write := func() error {
		return s.withPersonLock(ctx, r.claim.PersonID, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			p, err := readAccountPerson(ctx, txq, r.claim.PersonID)
			if err != nil {
				return err
			}
			flagOff := f.decide.Flag && !p.provisionAccount
			n, err := txq.WriteAccountResult(ctx, store.WriteAccountResultParams{
				ID: r.claim.ID, LeaseID: lease, ExternalID: r.externalID, State: r.state, Managed: r.managed,
				BlockedReason: r.reason, LastError: r.lastError, Attempts: r.attempts, NextAttemptAt: next,
				LastSyncedAt: r.syncedAt, LastSpecHash: r.specHash, SyncedEmployeeNumber: r.synced, Now: now,
				ClaimedExternalID: r.current, DisableRequestedAt: r.disableRequested,
			})
			if err != nil {
				return err
			}
			if n == 0 {
				held, err := txq.AccountLinkClaim(ctx, r.claim.ID)
				switch {
				case errors.Is(err, pgx.ErrNoRows):
					gone = true
				case err != nil:
					return err
				case deref(held.ExternalID) != deref(r.current):
					log.InfoContext(ctx, "staff: an account link names another user than its claim knew (HR relinked it); the result is not written")
				default:
					log.InfoContext(ctx, "staff: an account link was claimed by another cycle meanwhile; its result is not written")
				}
				return nil
			}
			for _, e := range r.events {
				e.payload.FlagOffMeanwhile = flagOff
				if err := s.recordEvent(ctx, txq, &r.claim.PersonID, e.kind, run.who, e.payload, accountEventClasses); err != nil {
					return err
				}
			}
			return nil
		})
	}
	err := write()
	if db.IsUniqueViolation(err, "ux_identity_links_provider_external") {
		// The user is another person's link's already: not this person's.
		r.mismatched()
		err = write()
	}
	if errors.Is(err, errPersonNotFound) {
		gone, err = true, nil
	}
	if err != nil {
		log.ErrorContext(ctx, "staff: write an account result", "error", err.Error())
		return fmt.Errorf("staff: write person %d's account result: %w", r.claim.PersonID, err)
	}
	if gone && r.created != nil {
		if _, err := withCallTimeout(ctx, func(ctx context.Context) (contracts.AccountRef, error) {
			return s.accountsDeactivate(ctx, run.actor, *r.created)
		}); err != nil {
			log.ErrorContext(ctx, "staff: the person's link was gone after Prepare, and the user it prepared could not be deactivated",
				slog.String("user_id", r.created.String()), slog.String("error", err.Error()))
		} else {
			log.WarnContext(ctx, "staff: the person's link was gone after Prepare; the user it prepared was deactivated",
				slog.String("user_id", r.created.String()))
		}
	}
	return nil
}

// deref is *s, "" for nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// renewAccountLease moves the claim's lease on before an identity call or
// read, answering false when it lapsed or was taken, or the link is gone.
func (s *server) renewAccountLease(ctx context.Context, id int64, lease uuid.UUID) bool {
	now := s.deps.Clock()
	n, err := store.New(s.deps.Pool).RenewAccountLease(ctx, store.RenewAccountLeaseParams{
		ID: id, LeaseID: lease, LeaseUntil: now.Add(accountsLeaseFor), Now: now,
	})
	if err != nil {
		s.logger().ErrorContext(ctx, "staff: renew an account claim's lease", "worker", accountsWorkerName, "error", err.Error())
		return false
	}
	return n == 1
}

// releaseAccountLease ends a claim that writes no result (review L3), so the
// link is free at once rather than when the lease lapses.
func (s *server) releaseAccountLease(ctx context.Context, id int64, lease uuid.UUID) {
	if err := store.New(s.deps.Pool).ReleaseAccountLease(ctx, store.ReleaseAccountLeaseParams{ID: id, LeaseID: lease}); err != nil {
		s.logger().ErrorContext(ctx, "staff: release an account claim's lease", "worker", accountsWorkerName, "error", err.Error())
	}
}

// errAccountMismatch is finishRequestedDisable's answer for a user who
// carries another employee number than the person's: someone else's account,
// to which nothing is done.
var errAccountMismatch = errors.New("staff: the link's user carries another employee number")

// finishRequestedDisable is the step for a disable Disable now asked for —
// Disable now's own, and the worker's while the marker stands: the user as
// identity reports them first. A user gone, or disabled in identity's own
// record already (by an Owner meanwhile, say; a directory's deactivation is
// not that), clears the request with no call. A privileged or unmanaged
// user is identity's to refuse — Deactivate answers account_privileged or
// account_not_managed — and is judged before the number, as decide judges:
// identity holds no number for either, so the number check would take every
// one of them for someone else's account (re-check of 032ed2ca). A managed
// user carrying another employee number than the person's, or than the one
// last synced, is someone else's account (decide's rule): nothing is called, the link is
// blocked mismatch and the request kept, so the disable runs once HR has put
// the number right. Otherwise Deactivate (deactivateClaimed). It answers how
// the claim ended and the error the caller reports: the read's or the call's,
// or errAccountMismatch.
func (s *server) finishRequestedDisable(ctx context.Context, run accountsRun, r *accountResult, lease, user uuid.UUID, f accountFacts, now time.Time) (accountWorked, error) {
	ref, err := s.readAccountRef(ctx, user, f)
	var answer error
	switch {
	case err != nil:
		s.logger().WarnContext(ctx, "staff: read a linked account for a disable asked for", "worker", accountsWorkerName,
			"person_id", r.claim.PersonID, "error", err.Error())
		r.failedCall(accountStep{Kind: accountStepNone}, err)
		r.soon = true
		answer = err
	case ref == nil:
		r.answered(reasonUserGone)
		r.state = linkStateFailed
		r.disableRequested = nil
		answer = contracts.ErrUserGone
	case ref.State == contracts.AccountStateDisabled && ref.DisabledBy != contracts.DisabledByDirectory:
		r.managed = ref.Managed
		r.state = linkStateDisabled
		r.answered("")
		r.disableRequested = nil
	case ref.Managed && !ref.Privileged &&
		!contracts.SameEmployeeNumber(ref.EmployeeNumber, f.row.EmployeeNumber) &&
		!contracts.SameEmployeeNumber(ref.EmployeeNumber, deref(r.claim.SyncedEmployeeNumber)):
		r.reported(accountStep{Kind: accountStepReport, Reason: reasonMismatch, State: linkStateBlocked}, ref)
		answer = errAccountMismatch
	default:
		return s.deactivateClaimed(ctx, run, r, lease, user, f, now)
	}
	return accountWorked{failed: r.failed, err: s.writeAccountResult(ctx, run, r, lease, f, now)}, answer
}

// errAccountLeaseLost is deactivateClaimed's answer when the claim's lease
// lapsed or was taken before the call: nothing was called or written.
var errAccountLeaseLost = errors.New("staff: the account claim's lease was lost")

// deactivateClaimed deactivates user on the claim r was built from, for run
// — Disable now's call, and the worker's for a disable it asked for — the
// lease renewed first, and writes the result: a deactivation, or a user
// gone, clears the disable requested. A refusal (account_not_managed,
// account_privileged, the last Owner) keeps it, as the link's blocked_reason
// too, tried again on the daily schedule — so a user an Owner adopts again,
// or demotes, is disabled by the next try (re-check MEDIUM-1, final); a
// failure keeps it, retried at the next poll; invalid_actor keeps it. It answers the call's error — the refusal or
// failure the caller reports — and how the claim ended.
func (s *server) deactivateClaimed(ctx context.Context, run accountsRun, r *accountResult, lease, user uuid.UUID, f accountFacts, now time.Time) (accountWorked, error) {
	if !s.renewAccountLease(ctx, r.claim.ID, lease) {
		return accountWorked{}, errAccountLeaseLost
	}
	step := accountStep{Kind: accountStepDeactivate}
	got, err := s.callAccountStep(ctx, run, step, &user, f)
	if err != nil {
		if _, _, answer := accountCallCode(err); !answer {
			s.logger().WarnContext(ctx, "staff: a deactivation failed", "worker", accountsWorkerName,
				"person_id", r.claim.PersonID, "error", err.Error())
		}
		r.failedCall(step, err)
		if errors.Is(err, contracts.ErrUserGone) {
			r.disableRequested = nil
		}
	} else {
		r.succeeded(step, nil, got, f, now)
		r.disableRequested = nil
	}
	return accountWorked{failed: r.failed, err: s.writeAccountResult(ctx, run, r, lease, f, now)}, err
}

// recordPreparedUser writes the user a Prepare answered onto the claim's
// link — under the person's lock, then the link on the claim's lease — and
// answers whether the chain may go on with them: not when another link names
// the user (a mismatch, recorded), the lease is lost or the person gone, and
// not when the person was erased or their flag turned off while Prepare ran
// (review LOW 2) — the user is recorded then, and nothing more is done.
func (s *server) recordPreparedUser(ctx context.Context, r *accountResult, lease uuid.UUID, got contracts.AccountRef, f accountFacts, now time.Time) bool {
	id := got.UserID.String()
	recorded, stop := false, false
	err := s.withPersonLock(ctx, r.claim.PersonID, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		p, err := readAccountPerson(ctx, txq, r.claim.PersonID)
		if err != nil {
			return err
		}
		row, err := txq.GetPerson(ctx, r.claim.PersonID)
		if err != nil {
			return fmt.Errorf("staff: read person %d: %w", r.claim.PersonID, err)
		}
		stop = row.ErasedAt != nil || !p.provisionAccount
		n, err := txq.RecordPreparedUser(ctx, store.RecordPreparedUserParams{
			ID: r.claim.ID, LeaseID: lease, ExternalID: id, SyncedEmployeeNumber: f.row.EmployeeNumber, Now: now,
		})
		recorded = n == 1
		return err
	})
	switch {
	case db.IsUniqueViolation(err, "ux_identity_links_provider_external"):
		r.mismatched()
		return false
	case errors.Is(err, errPersonNotFound):
		return false
	case err != nil:
		s.logger().ErrorContext(ctx, "staff: record a prepared user on the link", "worker", accountsWorkerName,
			"person_id", r.claim.PersonID, "error", err.Error())
		r.soon = true
		return false
	case !recorded:
		return false
	}
	r.current = &id
	return !stop
}
