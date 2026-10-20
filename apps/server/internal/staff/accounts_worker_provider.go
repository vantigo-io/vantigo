package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the accounts worker's provider column (Entra design D7): for
// a person whose account kind is a provider's key, the chain works their
// provider link before the identity link, under the claim the cycle holds on
// the identity link (the provider link is never claimed itself) — at most
// three provider calls, the announcement (Expect) and its withdrawal
// (Unexpect) among them, each under the call timeout with the lease renewed
// first and no transaction open, then the result on the provider link
// (WriteProviderLinkResult, on the identity link's lease). The address a
// provider made for a person who had no work email is written to the person
// (work_email_set_by_provider): the one person write the worker makes.
//
// The provider's status is asked once per cycle (providerStatuses) and
// recorded on settings.provider_status when it changed, from which the
// installation's attention items are judged. A provider not connected
// holds every step but a deactivate, on both links.

// providerFacts is what a claim reads of a person's provider link in its
// snapshot: the provider's key (the person's kind), the link (nil: none),
// the external spec, the settings' delete_external_after_days and the
// person's legal hold.
type providerFacts struct {
	key             string
	link            *store.PersonProviderLinkRow
	spec            externalSpecFacts
	deleteAfterDays *int
	legalHold       bool
}

// providerKind is whether kind is a provider's key: neither local nor
// external.
func providerKind(kind string) bool {
	return linkOnly(kind) && kind != contracts.AccountKindExternal
}

// readProviderFacts reads the provider column's facts on q, the claim's
// snapshot.
func readProviderFacts(ctx context.Context, q *store.Queries, set store.StaffSetting, row store.GetPersonRow, key string, today time.Time) (*providerFacts, error) {
	pf := &providerFacts{key: key, legalHold: row.LegalHold}
	if set.DeleteExternalAfterDays != nil {
		days := int(*set.DeleteExternalAfterDays)
		pf.deleteAfterDays = &days
	}
	link, err := q.PersonProviderLink(ctx, store.PersonProviderLinkParams{PersonID: row.ID, Provider: key})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("staff: read person %d's %s link: %w", row.ID, key, err)
	default:
		pf.link = &link
	}
	if pf.spec, err = readExternalSpec(ctx, q, set, row, key, today); err != nil {
		return nil, err
	}
	return pf, nil
}

// providerStatusSeen is a provider's entry in settings.provider_status.
type providerStatusSeen struct {
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// The reasons a status carries that Staff makes itself: no enabled module
// offers the provider a person's kind names, or the provider could not be
// asked.
const (
	providerReasonMissing     = "provider_missing"
	providerReasonUnreachable = "unreachable"
)

// providerStatuses asks every provider in the slot for its info, and
// answers an unconfigured one for every provider key in extra no module
// offers; each is recorded on settings.provider_status when its status or
// reason changed. Every call is made on no connection; a provider that
// cannot answer is failing, unreachable.
func (s *server) providerStatuses(ctx context.Context, extra ...string) map[string]contracts.ProviderInfo {
	out := map[string]contracts.ProviderInfo{}
	for _, p := range s.deps.IdentityProvisioners {
		if p == nil {
			continue
		}
		key := p.Key()
		info, err := withCallTimeout(ctx, func(ctx context.Context) (contracts.ProviderInfo, error) { return s.providerInfo(ctx, p) })
		if err != nil {
			s.logger().WarnContext(ctx, "staff: an identity provider could not say its status", "provider", key, "error", err.Error())
			info = contracts.ProviderInfo{Key: key, Status: contracts.ProviderFailing, Reason: providerReasonUnreachable}
		}
		info.Key = key
		out[key] = info
	}
	for _, key := range extra {
		if _, ok := out[key]; !ok && providerKind(key) {
			out[key] = contracts.ProviderInfo{Key: key, Status: contracts.ProviderUnconfigured, Reason: providerReasonMissing}
		}
	}
	if err := s.recordProviderStatuses(ctx, out); err != nil {
		s.logger().ErrorContext(ctx, "staff: record the identity providers' status", "error", err.Error())
	}
	return out
}

// recordProviderStatuses writes each provider's status on the settings row
// when it differs from what is recorded.
func (s *server) recordProviderStatuses(ctx context.Context, infos map[string]contracts.ProviderInfo) error {
	if len(infos) == 0 {
		return nil
	}
	q := store.New(s.deps.Pool)
	set, err := q.GetSettings(ctx)
	if err != nil {
		return err
	}
	recorded := map[string]providerStatusSeen{}
	if len(set.ProviderStatus) > 0 {
		if err := json.Unmarshal(set.ProviderStatus, &recorded); err != nil {
			return fmt.Errorf("staff: read the recorded provider status: %w", err)
		}
	}
	now := s.deps.Clock()
	for key, info := range infos {
		if was, ok := recorded[key]; ok && was.Status == info.Status && was.Reason == info.Reason {
			continue
		}
		b, err := json.Marshal(providerStatusSeen{Status: info.Status, Reason: info.Reason, CheckedAt: now})
		if err != nil {
			return err
		}
		if err := q.SetProviderStatus(ctx, store.SetProviderStatusParams{Provider: key, Status: b}); err != nil {
			return fmt.Errorf("staff: record provider %s's status: %w", key, err)
		}
	}
	return nil
}

// providerKeysOfPeople is the provider kinds people with the flag on hold,
// which a cycle asks about though no module offers them.
func (s *server) providerKeysOfPeople(ctx context.Context) ([]string, error) {
	rows, err := store.New(s.deps.Pool).FlaggedAccountKinds(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: read the people's account kinds: %w", err)
	}
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.AccountKind)
	}
	return keys, nil
}

// providerResult is what a claim writes to the provider link, built as the
// chain goes: it starts as the link as read.
type providerResult struct {
	key          string
	id           int64 // the link's row, 0 while there is none
	claimedState string
	claimedCode  string
	externalID   *string
	state        string
	reason       *string
	lastError    *string
	attempts     int32
	syncedAt     *time.Time
	specHash     []byte
	upn, email   *string
	announcedAt  *time.Time
	passIssuedAt *time.Time
	codes        []string
	// failed is a call that could not be made, retried; soon is a link to
	// work again at the next poll (a deactivation that failed, the chain's
	// three calls spent with more to do).
	failed, soon bool
	// madeEmail is the address the provider answered, which the person is
	// given when they have no work email.
	madeEmail string
	events    []accountEvent
}

func newProviderResult(pf *providerFacts) *providerResult {
	r := &providerResult{key: pf.key, state: providerStatePreparing}
	if l := pf.link; l != nil {
		r.id, r.claimedState, r.claimedCode = l.ID, l.State, deref(l.BlockedReason)
		r.externalID, r.state, r.reason, r.lastError, r.attempts = l.ExternalID, l.State, l.BlockedReason, l.LastError, l.Attempts
		r.syncedAt, r.specHash, r.upn, r.email = l.LastSyncedAt, l.LastSpecHash, l.ExternalUpn, l.ExternalEmail
		r.announcedAt, r.passIssuedAt, r.codes = l.AnnouncedAt, l.PassIssuedAt, slices.Clone(l.ReportedCodes)
	}
	return r
}

// answered clears the failure, recording reason ("" for none).
func (r *providerResult) answered(reason string) {
	r.reason, r.lastError, r.attempts = nil, nil, 0
	if reason != "" {
		r.reason = &reason
	}
}

func (r *providerResult) changed() bool {
	return r.state != r.claimedState || deref(r.reason) != r.claimedCode
}

func (r *providerResult) event(kind string, step accountStepKind, code string) {
	r.events = append(r.events, accountEvent{kind: kind, payload: accountEventPayload{
		Step: string(step), State: r.state, Code: code, Codes: slices.Clone(r.codes),
	}})
}

// account records what the provider answered of the account: its id, state,
// UPN, address and the facts it reported (with Staff's own code for a
// manager it could not name), and the pass — issued when HasPass turned true
// or a fresh one was asked for, cleared when there is none.
func (r *providerResult) account(acc contracts.ExternalAccount, fresh bool, managerMissing bool, now time.Time) {
	if r.externalID == nil || *r.externalID != acc.ExternalID {
		// Another account than the link named: its announcement is not this one's.
		r.announcedAt, r.passIssuedAt = nil, nil
	}
	id := acc.ExternalID
	r.externalID = &id
	switch acc.State {
	case providerStateEnabled, providerStateDisabled, providerStateDeleted:
		r.state = acc.State
	}
	if acc.UserPrincipalName != "" {
		upn := acc.UserPrincipalName
		r.upn = &upn
	}
	if acc.Email != "" {
		email := acc.Email
		r.email = &email
		r.madeEmail = email
	}
	switch {
	case !acc.HasPass:
		r.passIssuedAt = nil
	case r.passIssuedAt == nil || fresh:
		r.passIssuedAt = &now
	}
	r.codes = reportedCodes(acc.Reported, managerMissing)
}

// reportedCodes is the link's reported_codes: the provider's facts as it
// reported them, and manager_not_in_entra once when the manager has no
// external account, without repeats.
func reportedCodes(reported []string, managerMissing bool) []string {
	codes := make([]string, 0, len(reported)+1)
	for _, c := range reported {
		if c != "" && !slices.Contains(codes, c) {
			codes = append(codes, c)
		}
	}
	if managerMissing && !slices.Contains(codes, codeManagerNotInEntra) {
		codes = append(codes, codeManagerNotInEntra)
	}
	return codes
}

// providerCallCode maps a provider call's error to the code it is recorded
// as and the link state it leaves ("" to keep the link's), answer false for
// a failure to retry. The announcement's refusals are identity's
// (accountCallCode).
func providerCallCode(err error) (code, state string, answer bool) {
	for _, m := range []struct {
		err         error
		code, state string
	}{
		{contracts.ErrExternalAmbiguous, reasonExternalAmbiguous, providerStateBlocked},
		{contracts.ErrExternalAddressTaken, reasonExternalAddressTaken, providerStateBlocked},
		{contracts.ErrExternalSyncedOnPremises, reasonExternalSyncedOnPremises, providerStateBlocked},
		{contracts.ErrExternalPrivileged, reasonExternalPrivileged, providerStateBlocked},
		{contracts.ErrExternalEnabled, reasonExternalEnabled, ""},
		{contracts.ErrProviderUnavailable, reasonProviderUnavailable, ""},
		{errProviderUnavailable, reasonProviderUnavailable, ""},
		{contracts.ErrInvalidActor, reasonInvalidActor, providerStateFailed},
	} {
		if errors.Is(err, m.err) {
			return m.code, m.state, true
		}
	}
	return accountCallCode(err)
}

// providerEventOf is the event a successful provider step is recorded as.
func providerEventOf(step accountStepKind) string {
	switch step {
	case accountStepPrepare:
		return eventAccountExternalPrepared
	case accountStepAnnounce:
		return eventAccountAnnounced
	case accountStepActivate:
		return eventAccountExternalActivated
	case accountStepDeactivate:
		return eventAccountExternalDeactivated
	case accountStepDelete:
		return eventAccountExternalDeleted
	default:
		return eventAccountExternalUpdated
	}
}

// providerReportEvent is the event a report or a refusal is recorded as.
func providerReportEvent(state string) string {
	if state == providerStateFailed {
		return eventAccountExternalFailed
	}
	return eventAccountExternalBlocked
}

// providerOutcome is how the provider column ended for the identity column:
// whether the provider is not connected (the identity link then runs nothing
// but a deactivate), whether a call could not be made, and the code of a
// provider's refusal.
type providerOutcome struct {
	unavailable bool
	failed      bool
	refusal     string
	err         error
}

// providerIdentity is what the provider column knows of the identity link:
// whether its user has arrived (named, and not a pending, credential-less
// one), the pending user an announcement replaces, and whether the link
// names any user (an announcement nothing fulfilled is withdrawn on a
// deactivate or a delete only then).
type providerIdentity struct {
	arrived bool
	replace *uuid.UUID
	linked  bool
}

// identityForProvider reads ref (nil: no user, or none read) as the
// provider column needs it; known false is a user who could not be read,
// taken as arrived so nothing is announced over them.
func identityForProvider(user *uuid.UUID, ref *contracts.AccountRef, known bool) providerIdentity {
	if user == nil {
		return providerIdentity{}
	}
	if !known || ref == nil {
		return providerIdentity{arrived: true, linked: true}
	}
	if ref.Managed && ref.State == contracts.AccountStatePending && !ref.HasCredential {
		id := ref.UserID
		return providerIdentity{replace: &id, linked: true}
	}
	return providerIdentity{arrived: true, linked: true}
}

// workProviderLink works the person's provider link on the claim r holds
// (lease), for run, and writes its result: decideProvider, each call under
// the timeout with the lease renewed first, at most accountsMaxCalls
// provider calls. disable is HR's Disable now (or the worker finishing it).
func (s *server) workProviderLink(ctx context.Context, run accountsRun, claim store.ClaimAccountLinkRow, lease uuid.UUID, f accountFacts, ident providerIdentity, disable bool, now time.Time) providerOutcome {
	pf := f.provider
	log := s.logger().With("worker", accountsWorkerName, "person_id", claim.PersonID, "provider", pf.key)
	p, _ := s.provisioner(pf.key)
	info, ok := run.providers[pf.key]
	if !ok || p == nil {
		info = contracts.ProviderInfo{Key: pf.key, Status: contracts.ProviderUnconfigured, Reason: providerReasonMissing}
	}
	out := providerOutcome{unavailable: info.Status != contracts.ProviderConnected}
	r := newProviderResult(pf)
	spec := pf.spec.spec

	if p == nil {
		// No enabled module offers the provider: nothing can be read or
		// called, and the link says so.
		if pf.link != nil && f.decide.Flag && !f.decide.Erased {
			r.reported(accountStep{Kind: accountStepReport, Reason: reasonProviderUnavailable})
		}
		out.err = s.writeProviderResult(ctx, run, claim, lease, r, f, now)
		return out
	}
	// The account as the provider has it: read on no connection.
	var acc *contracts.ExternalAccount
	if l := pf.link; l != nil && l.ExternalID != nil && l.State != providerStateDeleted {
		got, err := withCallTimeout(ctx, func(ctx context.Context) (*contracts.ExternalAccount, error) {
			return s.providerAccount(ctx, p, *l.ExternalID)
		})
		if err != nil {
			if code, _, answer := providerCallCode(err); answer && code == reasonProviderUnavailable {
				out.unavailable = true
			} else {
				log.WarnContext(ctx, "staff: read an external account", "error", err.Error())
			}
			r.failCall(accountStepNone, err)
			if deactivationDue(f) || disable {
				r.soon = true
			}
			out.failed = r.failed
			out.err = s.writeProviderResult(ctx, run, claim, lease, r, f, now)
			return out
		}
		acc = got
		if acc != nil {
			r.codes = reportedCodes(acc.Reported, pf.spec.managerMissing)
		}
	} else if pf.link != nil {
		r.codes = reportedCodes(pf.link.ReportedCodes, pf.spec.managerMissing)
	}

	linkFacts := func() *providerLinkFacts {
		if r.id == 0 && r.externalID == nil {
			return nil
		}
		return &providerLinkFacts{
			State: r.state, ExternalID: r.externalID, Announced: r.announcedAt != nil,
			EnabledToday: r.claimedState == providerStateEnabled && r.syncedAt != nil && decideDay(civilToday(*r.syncedAt, f.zone)).Equal(decideDay(f.today)),
		}
	}
	in := providerDecision{
		Person: f.decide, Status: info.Status, Link: linkFacts(), Account: acc,
		SpecChanged: !slices.Equal(externalSpecHash(spec), r.specHash), Arrived: ident.arrived,
		DeleteAfterDays: pf.deleteAfterDays, LegalHold: pf.legalHold, DisableRequested: disable,
		CreateBeforeDays: f.createBeforeDays, Today: f.today, Fresh: run.fresh,
	}
	for calls := 0; ; calls++ {
		step := decideProvider(in)
		if !step.isCall() && step.Kind != accountStepAnnounce && step.Kind != accountStepDelete {
			r.reported(step)
			break
		}
		if calls >= accountsMaxCalls {
			r.soon = true
			break
		}
		if !s.renewAccountLease(ctx, claim.ID, lease) {
			log.InfoContext(ctx, "staff: an account claim's lease lapsed; the provider chain stops", "step", string(step.Kind))
			return out
		}
		if step.Kind == accountStepPrepare && r.id == 0 {
			id, err := s.insertProviderLink(ctx, claim.PersonID, pf.key, now)
			if err != nil || id == 0 {
				if err != nil {
					log.ErrorContext(ctx, "staff: write a preparing provider link", "error", err.Error())
				}
				out.err = err
				return out
			}
			r.id = id
		}
		called := spec
		called.Flags.Fresh = step.Fresh
		got, extra, err := s.callProviderStep(ctx, run, p, step, r, called, f, ident)
		calls += extra
		if err != nil {
			code, _, answer := providerCallCode(err)
			if !answer {
				log.WarnContext(ctx, "staff: a provider call failed", "step", string(step.Kind), "error", err.Error())
			} else if code == reasonInvalidActor {
				log.ErrorContext(ctx, "staff: a provider call named no valid actor", "step", string(step.Kind))
			}
			if answer && code != reasonProviderUnavailable && step.Kind != accountStepAnnounce {
				out.refusal = code
			}
			if code == reasonProviderUnavailable {
				out.unavailable = true
			}
			r.failCall(step.Kind, err)
			if step.Kind == accountStepDeactivate {
				r.soon = true
			}
			break
		}
		r.succeeded(step, got, pf.spec.managerMissing, spec, now)
		if got != nil {
			acc = got
		}
		in.Link, in.Account, in.Fresh = linkFacts(), acc, false
		in.SpecChanged = !slices.Equal(externalSpecHash(spec), r.specHash)
	}
	out.failed = r.failed
	if err := s.writeProviderResult(ctx, run, claim, lease, r, f, now); err != nil {
		out.err = err
	}
	return out
}

// callProviderStep makes step's call for run under the call timeout: the
// provider's, or identity's announcement. A deactivate or a delete while the
// identity link names no user withdraws the announcement too (Unexpect, one
// more call: extra). It answers the account the provider answered (nil for
// the announcement and the delete).
func (s *server) callProviderStep(ctx context.Context, run accountsRun, p contracts.IdentityProvisioner, step accountStep, r *providerResult, spec contracts.ExternalSpec, f accountFacts, ident providerIdentity) (*contracts.ExternalAccount, int, error) {
	externalID := deref(r.externalID)
	answer := func(acc contracts.ExternalAccount, err error) (*contracts.ExternalAccount, int, error) {
		if err != nil {
			return nil, 0, err
		}
		return &acc, 0, nil
	}
	switch step.Kind {
	case accountStepPrepare:
		return answer(withCallTimeout(ctx, func(ctx context.Context) (contracts.ExternalAccount, error) {
			return s.providerPrepare(ctx, p, run.actor, spec)
		}))
	case accountStepAnnounce:
		_, err := withCallTimeout(ctx, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, s.accountsExpect(ctx, run.actor, contracts.ExpectedAccount{
				EmployeeNumber: f.row.EmployeeNumber, DisplayName: f.row.DisplayName,
				PreferredLanguage: f.row.PreferredLanguage, ObjectID: externalID,
				UserPrincipalName: deref(r.upn), ReplaceUserID: ident.replace,
			})
		})
		return nil, 0, err
	case accountStepActivate:
		return answer(withCallTimeout(ctx, func(ctx context.Context) (contracts.ExternalAccount, error) {
			return s.providerActivate(ctx, p, run.actor, externalID, spec)
		}))
	case accountStepUpdate:
		return answer(withCallTimeout(ctx, func(ctx context.Context) (contracts.ExternalAccount, error) {
			return s.providerUpdate(ctx, p, run.actor, externalID, spec)
		}))
	}
	// A deactivate or a delete: the account ends, and an announcement no
	// user fulfilled is withdrawn.
	var acc *contracts.ExternalAccount
	if step.Kind == accountStepDeactivate {
		got, err := withCallTimeout(ctx, func(ctx context.Context) (contracts.ExternalAccount, error) {
			return s.providerDeactivate(ctx, p, run.actor, externalID, spec)
		})
		if err != nil {
			return nil, 0, err
		}
		acc = &got
	} else {
		if _, err := withCallTimeout(ctx, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, s.providerDelete(ctx, p, run.actor, externalID)
		}); err != nil {
			return nil, 0, err
		}
	}
	if r.announcedAt == nil || ident.linked {
		return acc, 0, nil
	}
	if _, err := withCallTimeout(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.accountsUnexpect(ctx, run.actor, f.row.EmployeeNumber)
	}); err != nil {
		// The account ended; the announcement is withdrawn by the next try.
		s.logger().WarnContext(ctx, "staff: withdraw an announcement", "worker", accountsWorkerName,
			"person_id", f.row.ID, "error", err.Error())
		return acc, 1, nil
	}
	r.announcedAt = nil
	return acc, 1, nil
}

// reported records a non-call step: nothing, or a report.
func (r *providerResult) reported(step accountStep) {
	if step.State != "" {
		r.state = step.State
	}
	if step.Kind == accountStepNone {
		// Nothing to do: whatever was asked was answered, and a reason the
		// link carried (a refusal now gone, provider_unavailable) lifts.
		r.answered("")
		return
	}
	r.answered(step.Reason)
	if r.id != 0 && r.changed() {
		r.event(providerReportEvent(r.state), step.Kind, step.Reason)
	}
}

// succeeded records a call the provider (or identity, for the announcement)
// carried out, at now.
func (r *providerResult) succeeded(step accountStep, got *contracts.ExternalAccount, managerMissing bool, spec contracts.ExternalSpec, now time.Time) {
	r.answered("")
	r.syncedAt = &now
	switch step.Kind {
	case accountStepAnnounce:
		r.announcedAt = &now
	case accountStepDelete:
		r.state = providerStateDeleted
		r.passIssuedAt = nil
	default:
		if got != nil {
			r.account(*got, step.Fresh, managerMissing, now)
		}
		r.specHash = externalSpecHash(spec)
		if step.Kind == accountStepDeactivate {
			r.state = providerStateDisabled
			r.passIssuedAt = nil
		}
	}
	r.event(providerEventOf(step.Kind), step.Kind, "")
}

// failCall records a call's error: a provider's refusal as the link's
// blocked_reason, anything else a failure retried.
func (r *providerResult) failCall(step accountStepKind, err error) {
	code, state, answer := providerCallCode(err)
	if !answer {
		first := r.lastError == nil
		r.failed = true
		r.attempts++
		failure := errorAccountsUnavailable
		r.lastError = &failure
		if first && r.id != 0 {
			r.event(eventAccountExternalFailed, step, failure)
		}
		return
	}
	if state != "" {
		r.state = state
	}
	r.answered(code)
	if r.id != 0 && r.changed() {
		r.event(providerReportEvent(r.state), step, code)
	}
}

// insertProviderLink writes the person's preparing provider link under
// their lock and answers its id; 0 when the person is gone, erased or their
// flag off meanwhile.
func (s *server) insertProviderLink(ctx context.Context, personID int64, key string, now time.Time) (int64, error) {
	var id int64
	err := s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		p, err := readAccountPerson(ctx, txq, personID)
		if err != nil || !p.provisionAccount {
			return err
		}
		if err := txq.InsertPreparingProviderLink(ctx, store.InsertPreparingProviderLinkParams{PersonID: personID, Provider: key, Now: now}); err != nil {
			return fmt.Errorf("staff: write person %d's preparing %s link: %w", personID, key, err)
		}
		link, err := txq.PersonProviderLink(ctx, store.PersonProviderLinkParams{PersonID: personID, Provider: key})
		if err != nil {
			return fmt.Errorf("staff: read person %d's %s link: %w", personID, key, err)
		}
		id = link.ID
		return nil
	})
	if _, refused := asRefusal(err); refused || errors.Is(err, errPersonNotFound) {
		return 0, nil
	}
	return id, err
}

// writeProviderResult writes r onto the provider link under the person's
// lock, on the claim's lease (held on the identity link), with its events;
// then, in a transaction of its own, the address the provider made to a
// person who has no work email. A link never written (no row) is nothing.
func (s *server) writeProviderResult(ctx context.Context, run accountsRun, claim store.ClaimAccountLinkRow, lease uuid.UUID, r *providerResult, f accountFacts, now time.Time) error {
	if r.id == 0 {
		return nil
	}
	log := s.logger().With("worker", accountsWorkerName, "person_id", claim.PersonID, "provider", r.key)
	next := f.nextMidnight()
	if (r.failed && r.attempts < accountsMaxAttempts) || r.soon {
		next = now.Add(s.workersPoll())
	}
	codes := r.codes
	if codes == nil {
		codes = []string{}
	}
	written := false
	err := s.withPersonLock(ctx, claim.PersonID, allowErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		n, err := txq.WriteProviderLinkResult(ctx, store.WriteProviderLinkResultParams{
			ID: r.id, LeaseID: lease, ExternalID: r.externalID, State: r.state, BlockedReason: r.reason,
			LastError: r.lastError, Attempts: r.attempts, NextAttemptAt: &next, LastSyncedAt: r.syncedAt,
			LastSpecHash: r.specHash, ExternalUpn: r.upn, ExternalEmail: r.email, AnnouncedAt: r.announcedAt,
			PassIssuedAt: r.passIssuedAt, ReportedCodes: codes, Now: now,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			log.InfoContext(ctx, "staff: the claim was lost before the provider link's result was written; it is not written")
			return nil
		}
		written = true
		for _, e := range r.events {
			if err := s.recordEvent(ctx, txq, &claim.PersonID, e.kind, run.who, e.payload, accountEventClasses); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errPersonNotFound) {
		return nil
	}
	if err != nil {
		log.ErrorContext(ctx, "staff: write a provider link's result", "error", err.Error())
		return fmt.Errorf("staff: write person %d's %s link result: %w", claim.PersonID, r.key, err)
	}
	if written && r.madeEmail != "" && deref(f.row.WorkEmail) == "" {
		s.setWorkEmailFromProvider(ctx, run, claim.PersonID, r, now)
	}
	return nil
}

// setWorkEmailFromProvider writes the address the provider made to the
// person, under their lock, when they still have no work email and nobody
// else holds it: work_email_set_by_provider, naming no address. A failure is
// logged, and tried again when the link next answers the address.
func (s *server) setWorkEmailFromProvider(ctx context.Context, run accountsRun, personID int64, r *providerResult, now time.Time) {
	err := s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		n, err := txq.SetWorkEmailFromProvider(ctx, store.SetWorkEmailFromProviderParams{
			WorkEmail: strings.TrimSpace(r.madeEmail), PersonID: personID, Now: now,
		})
		if err != nil || n == 0 {
			return err
		}
		return s.recordEvent(ctx, txq, &personID, eventWorkEmailSetByProvider, run.who,
			accountEventPayload{Step: "provider", State: r.state}, accountEventClasses)
	})
	if _, refused := asRefusal(err); refused || errors.Is(err, errPersonNotFound) {
		return
	}
	if err != nil {
		s.logger().ErrorContext(ctx, "staff: write the address the provider made", "worker", accountsWorkerName,
			"person_id", personID, "error", err.Error())
	}
}
