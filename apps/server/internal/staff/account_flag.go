package staff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the account flag (design S22, delivery C): whether Staff
// provides a person with a user, from when and at which of their addresses —
// three columns of staff.people, 00042's — read with the link it drives and
// what identity reports of the link's user. The flag, the link and the
// account events are the links group's (shaping.go): the person, staff:manage
// and staff:personal-data read them; a manager, an Approver and a colleague
// read none of it and get the person's 404. identity is asked — the
// authority mode, the user — before any snapshot opens and never under the
// person's lock (contractscalls.go). The accounts worker, which acts on the
// flag, is Task 4's.

// The account events (plan Global Constraints, "The account events").
// account_flag_updated is HR's change of the flag, {fields: {provisionAccount,
// provisionFrom, accountEmailKind: {from, to}}}, class C; the others are the
// accounts worker's (Task 4), {step, state, code?, invitation?,
// flagOffMeanwhile?}, class C, codes and states only — no address, no name,
// no user id beyond the link's own.
const (
	eventAccountFlagUpdated = "account_flag_updated"
	eventAccountPrepared    = "account_prepared"
	eventAccountActivated   = "account_activated"
	eventAccountDeactivated = "account_deactivated"
	eventAccountUpdated     = "account_updated"
	eventAccountBlocked     = "account_blocked"
	eventAccountFailed      = "account_failed"
	eventAccountMismatched  = "account_mismatched"
)

// accountEventKinds are the account events, listed only to the readers of
// the links group (seesEvents and WithLinks): to anyone else they are left
// out of the history and its count, so a manager does not learn a person is
// given a user, or why not.
var accountEventKinds = []string{
	eventAccountFlagUpdated, eventAccountPrepared, eventAccountActivated, eventAccountDeactivated,
	eventAccountUpdated, eventAccountBlocked, eventAccountFailed, eventAccountMismatched,
}

// The account email kinds (spec §3.1, account_email_kind): which of the
// person's addresses identity is given. A null column reads work (plan
// reading 10: 00042 gives it no default).
const (
	accountEmailWork    = "work"
	accountEmailPrivate = "private"
)

// linkStateLinked is HR's self-service link's state. Every other state —
// preparing, pending, active, disabled, failed, blocked — is the accounts
// worker's, whose link the self-service endpoint leaves alone (link_managed).
const linkStateLinked = "linked"

// accountRequestSchema is the contract's name for the flag's replace, closed
// as the person's is (strictbody.go).
const accountRequestSchema = "StaffPersonAccountRequest"

// accountPerson is what the account needs of a person: the flag, the two
// addresses it may name and the flag's own revision (00051's
// account_revision — never the person's, which a manager reads).
type accountPerson = store.GetPersonAccountRow

// accountEmailKindOf is p's address choice, null read as work.
func accountEmailKindOf(p accountPerson) string {
	if p.AccountEmailKind != nil && *p.AccountEmailKind == accountEmailPrivate {
		return accountEmailPrivate
	}
	return accountEmailWork
}

// accountAddress is the address p's account would be given — the work or
// the private email by the choice — and "" when the person has none. It is
// the only address Staff ever hands identity, or asks identity to compare
// (accountsAccount): never one a caller supplied.
func accountAddress(p accountPerson) string {
	address := p.WorkEmail
	if accountEmailKindOf(p) == accountEmailPrivate {
		address = p.PrivateEmail
	}
	if address == nil {
		return ""
	}
	return *address
}

// readAccountPerson is the person's account columns on q, errPersonNotFound
// when there is no such person.
func readAccountPerson(ctx context.Context, q *store.Queries, id int64) (accountPerson, error) {
	row, err := q.GetPersonAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountPerson{}, errPersonNotFound
	}
	if err != nil {
		return accountPerson{}, fmt.Errorf("staff: read person %d's account flag: %w", id, err)
	}
	return row, nil
}

// readAccountLink is the person's link with the worker's columns on q, nil
// when they have none.
func readAccountLink(ctx context.Context, q *store.Queries, personID int64) (*store.PersonAccountLinkRow, error) {
	row, err := q.PersonAccountLink(ctx, personID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's link: %w", personID, err)
	}
	return &row, nil
}

// effectiveProvisionFrom is the day the account is enabled (spec reading 14):
// the stored date, else the first day of the earliest employment that has
// not ended by today, else none.
func effectiveProvisionFrom(ctx context.Context, q *store.Queries, p accountPerson, today time.Time) (*openapi_types.Date, error) {
	if p.ProvisionFrom.Valid {
		return dateOf(p.ProvisionFrom), nil
	}
	start, err := q.EarliestOpenEmploymentStart(ctx, store.EarliestOpenEmploymentStartParams{
		PersonID: p.ID, Today: pgtype.Date{Time: today, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's earliest employment: %w", p.ID, err)
	}
	return dateOf(start), nil
}

// readsAccount is whether the caller reads personID's account: the links
// group's audience — the person themselves, staff:manage and
// staff:personal-data. None of the three needs the manager graph, so the
// question is answered before anything is read.
func (c *callerContext) readsAccount(personID int64) bool {
	return c.Manage || c.PersonalData || (c.Person != nil && *c.Person == personID)
}

// accountMode is the authority mode for a response: nil when no module
// provides accounts, when this process's configuration derives another mode
// than the recorded one (contracts.ErrModeMismatch), and when identity could
// not answer — which is logged, so a fault there never takes a read of
// Staff's own data down with it.
func (s *server) accountMode(ctx context.Context) *gen.StaffAccountMode {
	mode, err := s.accountsMode(ctx)
	switch {
	case errors.Is(err, errAccountsUnavailable), errors.Is(err, contracts.ErrModeMismatch):
		return nil
	case err != nil:
		s.logger().WarnContext(ctx, "staff: read the authority mode", slog.String("error", err.Error()))
		return nil
	}
	out := gen.StaffAccountMode(mode)
	return &out
}

// accountView is personID's account as the contract answers it, at today.
// identity is asked first, on no snapshot — the mode, and the link's user as
// identity reports them — then the person, the link and the employments are
// read in one snapshot, and the linked user is named by the directory after
// it. What identity reported is used only when the snapshot's link still
// names the user it was asked about.
func (s *server) accountView(ctx context.Context, personID int64, today time.Time) (gen.StaffPersonAccount, error) {
	mode := s.accountMode(ctx)
	before, err := readAccountLink(ctx, store.New(s.deps.Pool), personID)
	if err != nil {
		return gen.StaffPersonAccount{}, err
	}
	var (
		asked uuid.UUID
		ref   *contracts.AccountRef
	)
	if before != nil && before.ExternalID != nil {
		if id, err := uuid.Parse(*before.ExternalID); err == nil {
			asked = id
			// The view reports no address difference, so identity is asked to
			// compare none.
			ref, err = s.accountsAccount(ctx, id, nil)
			if err != nil {
				if !errors.Is(err, errAccountsUnavailable) {
					s.logger().WarnContext(ctx, "staff: read a linked account", slog.Int64("person_id", personID), slog.String("error", err.Error()))
				}
				ref = nil
			}
		}
	}

	var (
		p         accountPerson
		link      *store.PersonAccountLinkRow
		effective *openapi_types.Date
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if p, err = readAccountPerson(ctx, q, personID); err != nil {
			return err
		}
		if link, err = readAccountLink(ctx, q, personID); err != nil {
			return err
		}
		effective, err = effectiveProvisionFrom(ctx, q, p, today)
		return err
	})
	if err != nil {
		return gen.StaffPersonAccount{}, err
	}

	kind := gen.StaffAccountEmailKind(accountEmailKindOf(p))
	out := gen.StaffPersonAccount{
		ProvisionAccount:       p.ProvisionAccount,
		ProvisionFrom:          dateOf(p.ProvisionFrom),
		ProvisionFromEffective: effective,
		AccountEmailKind:       kind,
		Address:                gen.StaffPersonAccountAddress{Kind: kind, Present: accountAddress(p) != ""},
		Mode:                   mode,
		Revision:               p.AccountRevision,
	}
	if link == nil {
		return out, nil
	}
	attempts := link.Attempts
	out.Link = &gen.StaffAccountLink{
		State:         link.State,
		Managed:       link.Managed,
		Reason:        link.BlockedReason,
		Attempts:      attempts,
		NextAttemptAt: link.NextAttemptAt,
		LastSyncedAt:  link.LastSyncedAt,
	}
	if link.ExternalID != nil {
		if out.Link.User, err = s.linkedUser(ctx, link.ExternalID); err != nil {
			return gen.StaffPersonAccount{}, err
		}
		if ref != nil && ref.UserID == asked && *link.ExternalID == asked.String() && ref.State == contracts.AccountStatePending {
			out.Link.Invitation = &gen.StaffAccountInvitation{
				State:  gen.StaffAccountInvitationState(ref.Invitation.State),
				Issued: int32(ref.Invitation.Issued),
			}
		}
	}
	return out, nil
}

// GetStaffPeopleByIdAccount Get a person's account flag
// (GET /api/v1/staff/people/{id}/account)
func (s *server) GetStaffPeopleByIdAccount(ctx context.Context, req gen.GetStaffPeopleByIdAccountRequestObject) (gen.GetStaffPeopleByIdAccountResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if !caller.readsAccount(req.Id) {
		return gen.GetStaffPeopleByIdAccount404Response{}, nil
	}
	out, err := s.accountView(ctx, req.Id, caller.Today)
	if errors.Is(err, errPersonNotFound) {
		return gen.GetStaffPeopleByIdAccount404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdAccount200JSONResponse(out), nil
}

// parsedAccountFlag is a validated replace of the flag: the kind always
// work or private, so a null and an explicit work are one value and a
// replace between them changes nothing.
type parsedAccountFlag struct {
	provision bool
	from      pgtype.Date
	kind      string
}

// parseAccountFlag judges a replace of the flag: the address choice work,
// private or none (work).
func parseAccountFlag(body gen.StaffPersonAccountRequest) (parsedAccountFlag, fieldErrors) {
	var f fieldErrors
	out := parsedAccountFlag{provision: body.ProvisionAccount, kind: accountEmailWork}
	if body.ProvisionFrom != nil {
		out.from = pgtype.Date{Time: body.ProvisionFrom.Time, Valid: true}
	}
	if body.AccountEmailKind != nil {
		switch k := string(*body.AccountEmailKind); k {
		case accountEmailWork, accountEmailPrivate:
			out.kind = k
		default:
			f.add("accountEmailKind", "The account's address is work or private")
		}
	}
	return out, f
}

// accountFlagChanges is the fields a replace changes, by the contract's
// names; empty when it changes nothing.
func accountFlagChanges(current accountPerson, next parsedAccountFlag) changes {
	out := changes{}
	if current.ProvisionAccount != next.provision {
		out["provisionAccount"] = fieldChange{From: current.ProvisionAccount, To: next.provision}
	}
	day := func(d pgtype.Date) any {
		if !d.Valid {
			return nil
		}
		return d.Time.Format(time.DateOnly)
	}
	if from, to := day(current.ProvisionFrom), day(next.from); from != to {
		out["provisionFrom"] = fieldChange{From: from, To: to}
	}
	if from := accountEmailKindOf(current); from != next.kind {
		out["accountEmailKind"] = fieldChange{From: from, To: next.kind}
	}
	return out
}

// invalidAccountFlagTitle is the title of a refused replace of the flag.
const invalidAccountFlagTitle = "The account flag was not changed"

// PutStaffPeopleByIdAccount Change a person's account flag
// (PUT /api/v1/staff/people/{id}/account)
//
// A full replace of the flag's three fields, guarded by the flag's own
// revision — no account write moves the person's — and written under the
// person's lock; an erased person is person_erased.
// A replace that changes nothing writes nothing. Nothing is asked of
// identity here but the response's mode and link, after the commit: acting
// on the flag is the accounts worker's.
func (s *server) PutStaffPeopleByIdAccount(ctx context.Context, req gen.PutStaffPeopleByIdAccountRequestObject) (gen.PutStaffPeopleByIdAccountResponseObject, error) {
	var body gen.StaffPersonAccountRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffPeopleByIdAccount400ApplicationProblemPlusJSONResponse(invalid(invalidAccountFlagTitle, badIfMatch())), nil
	}
	if f := s.unknownProperties(ctx, accountRequestSchema); f.failed() {
		return gen.PutStaffPeopleByIdAccount400ApplicationProblemPlusJSONResponse(invalid(invalidAccountFlagTitle, f)), nil
	}
	parsed, f := parseAccountFlag(body)
	if f.failed() {
		return gen.PutStaffPeopleByIdAccount400ApplicationProblemPlusJSONResponse(invalid(invalidAccountFlagTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := readAccountPerson(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if err := checkRevision("account flag", current.AccountRevision, revision); err != nil {
			return err
		}
		c := accountFlagChanges(current, parsed)
		if len(c) == 0 {
			return nil
		}
		kind := parsed.kind
		if _, err := txq.UpdatePersonAccount(ctx, store.UpdatePersonAccountParams{
			ID: req.Id, ProvisionAccount: parsed.provision, ProvisionFrom: parsed.from, AccountEmailKind: &kind,
		}); err != nil {
			return fmt.Errorf("staff: change person %d's account flag: %w", req.Id, err)
		}
		return s.recordChanges(ctx, txq, req.Id, eventAccountFlagUpdated, who, c)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleByIdAccount404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffPeopleByIdAccount404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffPeopleByIdAccount409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffPeopleByIdAccount400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.accountView(ctx, req.Id, caller.Today)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffPeopleByIdAccount200JSONResponse(out), nil
}
