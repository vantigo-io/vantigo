package expenses

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the travel claim: the container a trip's expenses sit in
// (design §3.6, decision X4). Its lines are ordinary rows of expenses.entries
// carrying its id; what makes them lines rather than expenses of their own is
// that everything status-shaped about them is read off the claim — see unitOf
// in authorize.go, which is the one function that decides it.
//
// **The lock order**, which every write in this module keeps and which nothing
// else may invent:
//
//	1. the claim's own row (LockClaim), and then
//	2. its lines, in id order (LockClaimLines, or the one line a write is about).
//
// A write on the claim and a write on one of its lines therefore start at the
// same row, so they queue rather than deadlock; and a batch over several lines
// takes them in id order, as every other batch in this module does. Nothing
// inside any of these transactions calls another module or the object store
// (the rule of docs/expenses.md, checked across the whole test suite): what a
// decision under the lock needs from a neighbour — the new project, its billing
// lines — is read before the transaction opens, and what the response needs is
// resolved after it commits.

// lockEntryUnit takes the locks a write on one expense needs, in that order:
// the travel claim it belongs to first, when it has one, and then the expense
// itself. It answers the locked row, the unit it belongs to and whether both
// are still there — a claim that has gone takes its lines with it, so either
// missing is the same "it is gone" the caller answers 404 for.
//
// claimID comes from the row the handler already read, outside the transaction.
// It can never go stale: an expense moves neither into a claim nor out of one,
// which is exactly why the contract refuses a claimId that does not match.
// It answers the claim itself as well as the unit, because a per diem day's
// rate, its currency and the dates it may fall on are all the *claim's* and a
// write that derived them before the lock may have been overtaken. A caller
// that re-derives anything from the claim must use this copy and not the one it
// read outside the transaction.
func lockEntryUnit(ctx context.Context, txq *store.Queries, id int64, claimID *int64) (
	store.ExpensesEntry, entryUnit, *store.ExpensesClaim, bool, error,
) {
	var claim *store.ExpensesClaim
	if claimID != nil {
		locked, err := txq.LockClaim(ctx, *claimID)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ExpensesEntry{}, entryUnit{}, nil, false, nil
		}
		if err != nil {
			return store.ExpensesEntry{}, entryUnit{}, nil, false,
				fmt.Errorf("expenses: lock a travel claim: %w", err)
		}
		claim = &locked
	}
	row, err := txq.LockEntry(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ExpensesEntry{}, entryUnit{}, nil, false, nil
	}
	if err != nil {
		return store.ExpensesEntry{}, entryUnit{}, nil, false, fmt.Errorf("expenses: lock an expense: %w", err)
	}
	return row, unitOf(row, claim), claim, true, nil
}

// The messages a claimId carries. An id the caller may not see reads exactly as
// one that does not exist, so a stranger cannot probe which claims there are —
// the same rule cannotBookOnProject keeps for projects.
func noSuchClaim(id int64) string { return fmt.Sprintf("No travel claim has id %d", id) }

func claimNotYours(id int64) string {
	return fmt.Sprintf("Travel claim %d is not yours to change", id)
}

// visibleClaim loads one claim and the caller's access to it, answering
// found=false both for an unknown id and for a claim the caller may not see, so
// the two can never be told apart. owesAnything is what its lines come to,
// which only the capabilities need; a caller that has not read them passes
// false and gets the safe answer.
func (s *server) visibleClaim(ctx context.Context, q *store.Queries, c *caller, id int64, owesAnything bool) (
	store.ExpensesClaim, claimAccess, bool, error,
) {
	claim, err := q.GetClaim(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ExpensesClaim{}, claimAccess{}, false, nil
	}
	if err != nil {
		return store.ExpensesClaim{}, claimAccess{}, false, fmt.Errorf("expenses: get a travel claim: %w", err)
	}
	role, err := c.roleOf(ctx, s, claim.ProjectID)
	if err != nil {
		return store.ExpensesClaim{}, claimAccess{}, false, err
	}
	a := c.claimAccessFor(claim, role, owesAnything)
	if !a.CanSee {
		return store.ExpensesClaim{}, claimAccess{}, false, nil
	}
	return claim, a, true, nil
}

// claimFigures is what one claim's lines come to, read through the one query
// that answers it for a whole page of claims at once (ClaimTotals), so a list
// and a single read can never disagree.
type claimFigures struct {
	Lines    int32
	Totals   []gen.ExpensesCurrencyTotal
	Billable []gen.ExpensesCurrencyAmount
	// Owes is whether the claim owes its owner anything at all, which is what
	// decides whether a payroll run can cover it.
	Owes bool
}

// claimFiguresOf reads the totals of every claim in one go. The arithmetic is
// the database's sum of exact numerics, not a float's, and the per-currency
// shape is the one the approval queue and the reimbursement list already use:
// nothing is ever converted, so a trip with a hotel in euros and mileage in
// kroner answers two lines and never a sum that is in neither.
func (s *server) claimFiguresOf(ctx context.Context, q *store.Queries, ids []int64) (map[int64]claimFigures, error) {
	out := map[int64]claimFigures{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.ClaimTotals(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("expenses: read the travel claims' totals: %w", err)
	}
	for _, row := range rows {
		if row.ClaimID == nil {
			continue
		}
		gross, err := floatFromNumeric(row.Gross)
		if err != nil {
			return nil, err
		}
		owed, err := floatFromNumeric(row.OwedToEmployee)
		if err != nil {
			return nil, err
		}
		billable, err := floatFromNumeric(row.BillAmount)
		if err != nil {
			return nil, err
		}
		f := out[*row.ClaimID]
		f.Lines += int32(row.LineCount)
		f.Totals = append(f.Totals, gen.ExpensesCurrencyTotal{
			Currency: row.Currency, Gross: gross, OwedToEmployee: owed,
		})
		f.Billable = append(f.Billable, gen.ExpensesCurrencyAmount{
			Currency: row.Currency, Amount: billable,
		})
		f.Owes = f.Owes || owed > 0
		out[*row.ClaimID] = f
	}
	return out, nil
}

// totalsOf is a claim's per-currency figures, always a list and never null: a
// claim with no lines carries an empty array, so a client never has to tell
// "none" from "not answered".
func (f claimFigures) totalsOf() []gen.ExpensesCurrencyTotal {
	if f.Totals == nil {
		return []gen.ExpensesCurrencyTotal{}
	}
	return f.Totals
}

func (f claimFigures) billableOf() []gen.ExpensesCurrencyAmount {
	if f.Billable == nil {
		return []gen.ExpensesCurrencyAmount{}
	}
	return f.Billable
}

// claimCapabilities renders one claim's capabilities. The three flow ones and
// the two payroll ones are answered truthfully although the operations they
// describe arrive with the claim flow: a capability that lied would be worse
// than one that is simply false, and the doors need no second rule when they
// open.
func claimCapabilities(a claimAccess) gen.ExpensesClaimCapabilities {
	return gen.ExpensesClaimCapabilities{
		CanEdit:           a.CanEdit,
		CanDelete:         a.CanDelete,
		CanSubmit:         a.CanSubmit,
		CanApprove:        a.CanApprove,
		CanUnapprove:      a.CanUnapprove,
		CanMarkReimbursed: a.CanMarkReimbursed,
		CanUndoReimbursed: a.CanUndoReimbursed,
	}
}

// claimHeader is everything a claim answers apart from its lines, rendered
// once for both shapes so the list and the single read cannot drift.
type claimHeader struct {
	Abroad         bool
	AbroadCurrency *string
	AbroadDayRate  *float64
	Decision       *gen.ExpensesEntryDecision
	Destination    *string
	Owner          gen.ExpensesUserRef
	Project        *gen.ExpensesEntryProject
	Reimbursement  *gen.ExpensesEntryReimbursement
}

func claimHeaderOf(claim store.ExpensesClaim, a claimAccess, names entryNames) (claimHeader, error) {
	rate, err := floatPtrFromNumeric(claim.AbroadDayRate)
	if err != nil {
		return claimHeader{}, err
	}
	unit := claimUnit(claim)
	h := claimHeader{
		Abroad:         claim.Abroad,
		AbroadCurrency: claim.AbroadCurrency,
		AbroadDayRate:  rate,
		Decision:       decisionResponse(unit, names),
		Destination:    claim.Destination,
		Owner:          userRef(claim.UserID, names),
		// The payroll reference is the clerk's record of their own run, so it
		// goes to the person it paid and to whoever reads everybody's expenses
		// — and not to a project manager, exactly as on a single expense.
		Reimbursement: reimbursementResponse(unit, a.IsOwner || claimHeaderSeesEveryone(a), names),
	}
	// A project the directory no longer lists — or an installation with no
	// projects module at all — leaves the stored id where it is and simply
	// shows nothing for it (decision X2).
	if claim.ProjectID != nil {
		if p, ok := names.projects[*claim.ProjectID]; ok {
			h.Project = &gen.ExpensesEntryProject{Id: p.ID, Code: p.Code, Name: p.Name}
		}
	}
	return h, nil
}

// claimHeaderSeesEveryone is the half of the payroll-reference rule that is
// about permissions rather than ownership. It is carried on the access rather
// than recomputed, so the claim and its lines answer the same thing.
func claimHeaderSeesEveryone(a claimAccess) bool { return a.SeesPayrollReference }

// claimResponse is one claim with its lines, through exactly the renderer a
// single expense goes through — there is one entry renderer in this module and
// a claim does not get a second.
func claimResponse(claim store.ExpensesClaim, a claimAccess, names entryNames, f claimFigures,
	lines []gen.ExpensesEntryResponse,
) (gen.ExpensesClaimResponse, error) {
	h, err := claimHeaderOf(claim, a, names)
	if err != nil {
		return gen.ExpensesClaimResponse{}, err
	}
	resp := gen.ExpensesClaimResponse{
		Id:             claim.ID,
		Purpose:        claim.Purpose,
		Destination:    h.Destination,
		Abroad:         h.Abroad,
		AbroadDayRate:  h.AbroadDayRate,
		AbroadCurrency: h.AbroadCurrency,
		DepartureAt:    claim.DepartureAt,
		ReturnAt:       claim.ReturnAt,
		Project:        h.Project,
		Owner:          h.Owner,
		Status:         claim.Status,
		SubmittedAt:    claim.SubmittedAt,
		Decision:       h.Decision,
		Reimbursement:  h.Reimbursement,
		Lines:          lines,
		Totals:         f.totalsOf(),
		Revision:       claim.Revision,
		CreatedAt:      claim.CreatedAt,
		UpdatedAt:      claim.UpdatedAt,
		Capabilities:   claimCapabilities(a),
	}
	// What the trip bills its customer goes to whoever may see the project's
	// money on a line of it, and to nobody else — the same rule, one level up.
	if a.CanSeeFinancials {
		resp.BillableTotals = ptrTo(f.billableOf())
	}
	return resp, nil
}

// claimListResponse is one claim in a list: the same header, with how many
// lines it holds in place of the lines themselves.
func claimListResponse(claim store.ExpensesClaim, a claimAccess, names entryNames, f claimFigures) (
	gen.ExpensesClaimListResponse, error,
) {
	h, err := claimHeaderOf(claim, a, names)
	if err != nil {
		return gen.ExpensesClaimListResponse{}, err
	}
	return gen.ExpensesClaimListResponse{
		Id:             claim.ID,
		Purpose:        claim.Purpose,
		Destination:    h.Destination,
		Abroad:         h.Abroad,
		AbroadDayRate:  h.AbroadDayRate,
		AbroadCurrency: h.AbroadCurrency,
		DepartureAt:    claim.DepartureAt,
		ReturnAt:       claim.ReturnAt,
		Project:        h.Project,
		Owner:          h.Owner,
		Status:         claim.Status,
		SubmittedAt:    claim.SubmittedAt,
		Decision:       h.Decision,
		Reimbursement:  h.Reimbursement,
		LineCount:      f.Lines,
		Totals:         f.totalsOf(),
		Revision:       claim.Revision,
		CreatedAt:      claim.CreatedAt,
		UpdatedAt:      claim.UpdatedAt,
		Capabilities:   claimCapabilities(a),
	}, nil
}

// claimResponseFor renders the single claim a create, a read or a replace
// answers with: its lines read in the order it shows them, their names and the
// claim's own resolved in one pass, and every line rendered by the entry
// renderer with the capabilities it would answer on its own.
func (s *server) claimResponseFor(ctx context.Context, q *store.Queries, c *caller, claim store.ExpensesClaim) (
	gen.ExpensesClaimResponse, error,
) {
	rows, err := q.ListClaimLines(ctx, &claim.ID)
	if err != nil {
		return gen.ExpensesClaimResponse{}, fmt.Errorf("expenses: read a travel claim's expenses: %w", err)
	}
	names, err := s.namesFor(ctx, rows, claim)
	if err != nil {
		return gen.ExpensesClaimResponse{}, err
	}
	figures, err := s.claimFiguresOf(ctx, q, []int64{claim.ID})
	if err != nil {
		return gen.ExpensesClaimResponse{}, err
	}
	lines, err := s.entryResponsesWith(ctx, c, rows, names)
	if err != nil {
		return gen.ExpensesClaimResponse{}, err
	}
	role, err := c.roleOf(ctx, s, claim.ProjectID)
	if err != nil {
		return gen.ExpensesClaimResponse{}, err
	}
	return claimResponse(claim, c.claimAccessFor(claim, role, figures[claim.ID].Owes), names, figures[claim.ID], lines)
}

// GetExpensesClaims List travel claims
// (GET /api/v1/expenses/claims)
//
// Visibility is a predicate of the query itself — the rule claimAccessFor
// applies to one claim: everything for expenses:view-all, expenses:approve and
// expenses:manage, the caller's own, and everything on the projects the caller
// manages. So the total is the number of claims the caller may see, every page
// is full but the last, and the list never holds a claim its own read would
// answer 404 for.
func (s *server) GetExpensesClaims(ctx context.Context, req gen.GetExpensesClaimsRequestObject) (gen.GetExpensesClaimsResponseObject, error) {
	p := req.Params
	msgs := validatePageParams(p.Page, p.PageSize)
	if value := filterValue(p.Status); value != nil && !slices.Contains(entryStatuses, *value) {
		msgs = append(msgs, fmt.Sprintf("'status' must be one of %s, but was '%s'.",
			strings.Join(entryStatuses, ", "), *value))
	}
	if len(msgs) > 0 {
		return gen.GetExpensesClaims400ApplicationProblemPlusJSONResponse(invalidQuery(msgs)), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	managed := []int32{}
	if !c.seesEveryone() {
		if managed, err = c.managedProjects(ctx, s); err != nil {
			return nil, err
		}
	}

	filter := store.CountClaimsParams{
		SeeAll:            c.seesEveryone(),
		CallerID:          c.UserID,
		ManagedProjectIds: managed,
		UserID:            p.UserId,
		Status:            filterValue(p.Status),
		FromDate:          optionalDate(p.From),
		ToDate:            optionalDate(p.To),
		Reimbursed:        p.Reimbursed,
	}
	total, err := q.CountClaims(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("expenses: count travel claims: %w", err)
	}
	claims, err := q.ListClaims(ctx, store.ListClaimsParams{
		SeeAll:            filter.SeeAll,
		CallerID:          filter.CallerID,
		ManagedProjectIds: filter.ManagedProjectIds,
		UserID:            filter.UserID,
		Status:            filter.Status,
		FromDate:          filter.FromDate,
		ToDate:            filter.ToDate,
		Reimbursed:        filter.Reimbursed,
		PageSize:          pageSize,
		PageOffset:        (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list travel claims: %w", err)
	}

	ids := make([]int64, 0, len(claims))
	for _, claim := range claims {
		ids = append(ids, claim.ID)
	}
	figures, err := s.claimFiguresOf(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	names, err := s.namesFor(ctx, nil, claims...)
	if err != nil {
		return nil, err
	}
	data := make([]gen.ExpensesClaimListResponse, 0, len(claims))
	for _, claim := range claims {
		role, err := c.roleOf(ctx, s, claim.ProjectID)
		if err != nil {
			return nil, err
		}
		resp, err := claimListResponse(claim, c.claimAccessFor(claim, role, figures[claim.ID].Owes), names, figures[claim.ID])
		if err != nil {
			return nil, err
		}
		data = append(data, resp)
	}
	return gen.GetExpensesClaims200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// GetExpensesClaimsById Get a travel claim by id
// (GET /api/v1/expenses/claims/{id})
func (s *server) GetExpensesClaimsById(ctx context.Context, req gen.GetExpensesClaimsByIdRequestObject) (gen.GetExpensesClaimsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	claim, _, found, err := s.visibleClaim(ctx, q, c, req.Id, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.GetExpensesClaimsById404Response{}, nil
	}
	resp, err := s.claimResponseFor(ctx, q, c, claim)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesClaimsById200JSONResponse(resp), nil
}

// checkClaimProject is decision X9's rule over the claim's project: the
// *owner* — the person the trip concerns, not whoever is recording it — may
// book on it, judged exactly as booking an expense on one is. A project the
// claim is *keeping* is not judged again (the grandfathering of design §8), so
// a project that has since been completed does not strand a trip already
// recorded; a changed one is judged in full.
//
// It also answers whether the kept project is one the directory can no longer
// resolve at all. That is not a refusal either — the stored id stays (decision
// X2) — but nothing can be re-pointed against a project nobody can read, so the
// save carries the column through instead.
func (s *server) checkClaimProject(ctx context.Context, ownerID uuid.UUID, projectID *int32,
	current *store.ExpensesClaim, add func(field, msg string),
) (*contracts.ProjectEntry, bool, error) {
	if projectID == nil || !s.projectsAvailable() {
		return nil, false, nil
	}
	kept := current != nil && current.ProjectID != nil && *current.ProjectID == *projectID
	if !kept {
		allowed, err := s.projectsCanLogTime(ctx, *projectID, ownerID)
		if err != nil {
			return nil, false, fmt.Errorf("expenses: check the claim's owner may book on the project: %w", err)
		}
		if !allowed {
			add("projectId", cannotBookOnProject)
			return nil, false, nil
		}
	}
	project, err := s.projectsProject(ctx, *projectID)
	if err != nil {
		return nil, false, fmt.Errorf("expenses: look up the project: %w", err)
	}
	if project == nil {
		if kept {
			return nil, true, nil
		}
		add("projectId", cannotBookOnProject)
	}
	return project, false, nil
}

// PostExpensesClaims Record a travel claim
// (POST /api/v1/expenses/claims)
//
// The claim is a draft with no lines, owned by the caller or by the person
// userId names. Nothing about it contends with another row, so it is one insert
// rather than a locked transaction.
func (s *server) PostExpensesClaims(ctx context.Context, req gen.PostExpensesClaimsRequestObject) (gen.PostExpensesClaimsResponseObject, error) {
	body := gen.ExpensesClaimRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}

	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	parsed, parseErrs := parseClaim(claimBodyOfCreate(body), s.projectsAvailable())
	for field, messages := range parseErrs {
		for _, msg := range messages {
			add(field, msg)
		}
	}
	owner, err := s.resolveOwner(ctx, c, body.UserId, nil, add)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return gen.PostExpensesClaims400ApplicationProblemPlusJSONResponse(invalidClaim(errs)), nil
	}
	if _, _, err := s.checkClaimProject(ctx, owner, parsed.ProjectID, nil, add); err != nil {
		return nil, err
	}
	// The period lock is judged on the day the trip departed — the day every
	// one of its lines will be judged on too, so a claim can never be recorded
	// into a period its expenses could not be.
	if !c.mayWritePast(utcDay(parsed.DepartureAt)) {
		add("departureAt", claimLockedMessage(*lockedBefore(c.Settings)))
	}
	if len(errs) > 0 {
		return gen.PostExpensesClaims400ApplicationProblemPlusJSONResponse(invalidClaim(errs)), nil
	}

	dayRate, err := numericFromRatPtr(parsed.AbroadDayRate, moneyPlaces)
	if err != nil {
		return nil, err
	}
	created, err := q.InsertClaim(ctx, store.InsertClaimParams{
		UserID:          owner,
		CreatedByUserID: c.UserID,
		Purpose:         parsed.Purpose,
		Destination:     parsed.Destination,
		Abroad:          parsed.Abroad,
		AbroadDayRate:   dayRate,
		AbroadCurrency:  parsed.AbroadCurrency,
		DepartureAt:     parsed.DepartureAt,
		ReturnAt:        parsed.ReturnAt,
		ProjectID:       parsed.ProjectID,
		Now:             s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: record a travel claim: %w", err)
	}
	resp, err := s.claimResponseFor(ctx, q, c, created)
	if err != nil {
		return nil, err
	}
	return gen.PostExpensesClaims201JSONResponse(resp), nil
}

// claimLockedMessage is the departureAt message when a trip departed before the
// period lock (design §4).
func claimLockedMessage(lock time.Time) string {
	return fmt.Sprintf("Travel claims departing before %s are locked", lock.Format(time.DateOnly))
}

// PutExpensesClaimsById Change a travel claim
// (PUT /api/v1/expenses/claims/{id})
//
// A full replace of the header, guarded by the revision the claim was read at.
// The refusals come in the order that tells the caller least about what they
// may not touch: a claim they may not see is the unknown id's 404, one they may
// see but not change the access layer's 403, and only then is the body judged.
//
// Changing the project re-points every line in the same transaction, under the
// lock order this file documents: the claim's row, then its lines in id order.
// The new project's billing lines are read before the transaction opens,
// because nothing inside one may call another module.
func (s *server) PutExpensesClaimsById(ctx context.Context, req gen.PutExpensesClaimsByIdRequestObject) (gen.PutExpensesClaimsByIdResponseObject, error) {
	body := gen.ExpensesClaimUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	current, a, found, err := s.visibleClaim(ctx, q, c, req.Id, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PutExpensesClaimsById404Response{}, nil
	}
	if !a.IsWriter {
		return gen.PutExpensesClaimsById403JSONResponse(forbidden()), nil
	}
	if field, msg := claimStateRefusal(c, current); msg != "" {
		return gen.PutExpensesClaimsById400ApplicationProblemPlusJSONResponse(
			invalidClaim(fieldError(field, msg))), nil
	}

	p, plan, errs, err := s.prepareClaimUpdate(ctx, q, c, current, body)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return gen.PutExpensesClaimsById400ApplicationProblemPlusJSONResponse(invalidClaim(errs)), nil
	}

	dayRate, err := numericFromRatPtr(p.AbroadDayRate, moneyPlaces)
	if err != nil {
		return nil, err
	}
	var (
		updated   store.ExpensesClaim
		gone      bool
		staleErrs map[string][]string
		conflict  *int32
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, err := txq.LockClaim(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("expenses: lock a travel claim: %w", err)
		}
		// Judged again on the row as it stands under the lock: a submit that
		// committed since is the state refusal above, arrived a moment later.
		if field, msg := claimStateRefusal(c, locked); msg != "" {
			staleErrs = fieldError(field, msg)
			return nil
		}
		if locked.Revision != body.Revision {
			conflict = &locked.Revision
			return nil
		}
		projectID := p.ProjectID
		if plan.carry {
			// Decision X2: an installation that no longer has the projects
			// module — or a project the directory can no longer resolve —
			// leaves what was booked exactly as it was booked, off the row this
			// transaction holds rather than the one the handler read.
			projectID = locked.ProjectID
		}
		// What this edit does to the claim's own lines. The trip's window says
		// which dates a per diem day may fall on and the abroad triple says what
		// one is worth, so an edit to either has to answer for the days already
		// recorded — and the claim's row is held, so they cannot move underneath.
		after := claimAfterEdit(locked, p, dayRate)
		repriced, err := claimPricingChanged(locked, after)
		if err != nil {
			return err
		}
		if plan.repoint || claimWindowMoved(locked, after) || repriced {
			lines, err := txq.LockClaimLines(ctx, &locked.ID)
			if err != nil {
				return fmt.Errorf("expenses: lock a travel claim's expenses: %w", err)
			}
			if plan.repoint {
				if msg := invoicedLineRefusal(lines); msg != "" {
					staleErrs = fieldError("projectId", msg)
					return nil
				}
			}
			// The window first: a day outside the new trip is refused rather
			// than repriced, so a narrowing edit never silently reprices a day
			// it is about to strand.
			if staleErrs = perDiemStrandedByWindow(lines, after); staleErrs != nil {
				return nil
			}
			if repriced {
				staleErrs, err = repriceClaimPerDiem(ctx, txq, lines, locked, after,
					c.Settings.DefaultCurrency, s.deps.Clock())
				if err != nil {
					return err
				}
				if staleErrs != nil {
					return nil
				}
			}
			if plan.repoint {
				for _, line := range lines {
					params, err := repointParams(line, projectID, plan, s.deps.Clock())
					if err != nil {
						return err
					}
					if err := txq.SetClaimLineProject(ctx, params); err != nil {
						return fmt.Errorf("expenses: re-point a travel claim's expense: %w", err)
					}
				}
			}
		}
		updated, err = txq.UpdateClaim(ctx, store.UpdateClaimParams{
			ID:             req.Id,
			Revision:       body.Revision,
			AnyOwner:       c.Manage,
			UserID:         c.UserID,
			Purpose:        p.Purpose,
			Destination:    p.Destination,
			Abroad:         p.Abroad,
			AbroadDayRate:  dayRate,
			AbroadCurrency: p.AbroadCurrency,
			DepartureAt:    p.DepartureAt,
			ReturnAt:       p.ReturnAt,
			ProjectID:      projectID,
			Now:            s.deps.Clock(),
		})
		return err
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("expenses: change a travel claim: %w", err)
	case gone:
		return gen.PutExpensesClaimsById404Response{}, nil
	case staleErrs != nil:
		return gen.PutExpensesClaimsById400ApplicationProblemPlusJSONResponse(
			invalidClaim(staleErrs)), nil
	case conflict != nil:
		return gen.PutExpensesClaimsById409ApplicationProblemPlusJSONResponse(
			revisionConflict(*conflict, body.Revision)), nil
	}

	resp, err := s.claimResponseFor(ctx, q, c, updated)
	if err != nil {
		return nil, err
	}
	return gen.PutExpensesClaimsById200JSONResponse(resp), nil
}

// DeleteExpensesClaimsById Delete a travel claim
// (DELETE /api/v1/expenses/claims/{id})
//
// Delete is for what is still editable: a draft or a rejected claim, its
// owner's or expenses:manage's, and not departing before the period lock —
// exactly capabilities.canDelete.
//
// Its lines go with it (fk_entries_claim_id cascades) and their receipt rows
// with them. The object keys are read under the claim's own row lock, in the
// transaction that deletes it, because an upload takes that same lock first:
// read outside it, a receipt committing between the read and the delete would
// have its row cascaded away and its object left behind. The objects themselves
// are removed once the delete has committed, and never before.
func (s *server) DeleteExpensesClaimsById(ctx context.Context, req gen.DeleteExpensesClaimsByIdRequestObject) (gen.DeleteExpensesClaimsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	current, a, found, err := s.visibleClaim(ctx, q, c, req.Id, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.DeleteExpensesClaimsById404Response{}, nil
	}
	if !a.IsWriter {
		return gen.DeleteExpensesClaimsById403JSONResponse(forbidden()), nil
	}
	if field, msg := claimStateRefusal(c, current); msg != "" {
		return gen.DeleteExpensesClaimsById400ApplicationProblemPlusJSONResponse(
			invalidClaim(fieldError(field, msg))), nil
	}

	var (
		keys       []string
		deleted    int64
		gone       bool
		staleField string
		staleMsg   string
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, err := txq.LockClaim(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			// A concurrent delete won; it is gone, which is the unknown id.
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("expenses: lock a travel claim: %w", err)
		}
		if staleField, staleMsg = claimStateRefusal(c, locked); staleMsg != "" {
			return nil
		}
		if keys, err = txq.ListAttachmentKeysForClaim(ctx, &req.Id); err != nil {
			return fmt.Errorf("expenses: read a travel claim's receipt keys: %w", err)
		}
		if deleted, err = txq.DeleteClaim(ctx, store.DeleteClaimParams{
			ID: req.Id, AnyOwner: c.Manage, UserID: c.UserID,
		}); err != nil {
			return fmt.Errorf("expenses: delete a travel claim: %w", err)
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case gone:
		return gen.DeleteExpensesClaimsById404Response{}, nil
	case staleMsg != "":
		return gen.DeleteExpensesClaimsById400ApplicationProblemPlusJSONResponse(
			invalidClaim(fieldError(staleField, staleMsg))), nil
	case deleted == 0:
		// Unreachable: the row was locked, the caller is its writer and its
		// state passed under the lock, which is every guard DeleteClaim
		// applies. Answered rather than asserted, because a future guard added
		// to the query should not become a silent 204.
		return gen.DeleteExpensesClaimsById403JSONResponse(forbidden()), nil
	}
	for _, key := range keys {
		// The claim is what these belonged to, so that is what the sweep logs
		// if the store refuses one: an entry id here would send an operator to
		// an expense that is not the one the receipt was on.
		s.removeReceiptObject(ctx, logKeyClaimID, req.Id, key)
	}
	return gen.DeleteExpensesClaimsById204Response{}, nil
}

// resolveClaimLine is the whole of "this expense is a line of that claim",
// asked before any lock is taken: which claim the save is about, and whether
// this caller may put an expense in it right now.
//
// A create names the claim; a replace may name only the one the line already
// carries, because an expense moves neither into a claim nor out of one — the
// owner, the project and the whole flow would change under it, and there is no
// answer to "what is this now" that is not simply a different line.
func (s *server) resolveClaimLine(ctx context.Context, q *store.Queries, c *caller, body entryBody,
	current *store.ExpensesEntry, add func(field, msg string),
) (*store.ExpensesClaim, error) {
	id := body.ClaimID
	if current != nil {
		switch {
		case current.ClaimID == nil && id != nil:
			add("claimId", "This expense is not part of a travel claim, and cannot be moved into one")
			return nil, nil
		case current.ClaimID != nil && id != nil && *id != *current.ClaimID:
			add("claimId", fmt.Sprintf("This expense belongs to travel claim %d and cannot be moved to another",
				*current.ClaimID))
			return nil, nil
		}
		// An absent claimId on a replace keeps the line where it is, which is
		// the only place it can be.
		id = current.ClaimID
	}
	if id == nil {
		return nil, nil
	}

	claim, err := q.GetClaim(ctx, *id)
	if errors.Is(err, pgx.ErrNoRows) {
		add("claimId", noSuchClaim(*id))
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("expenses: get a travel claim: %w", err)
	}
	role, err := c.roleOf(ctx, s, claim.ProjectID)
	if err != nil {
		return nil, err
	}
	a := c.claimAccessFor(claim, role, false)
	switch {
	case !a.CanSee:
		// A claim the caller cannot see reads exactly as one that is not
		// there, so naming ids tells a stranger nothing.
		add("claimId", noSuchClaim(*id))
		return nil, nil
	case !a.IsWriter:
		add("claimId", claimNotYours(*id))
		return nil, nil
	}
	if _, msg := entryStateRefusal(c, claimUnit(claim)); msg != "" {
		add("claimId", msg)
		return nil, nil
	}
	return &claim, nil
}

// claimLineRules is what belonging to a claim decides about the line itself
// (Global Constraints): its owner and its project are the claim's, and naming
// another of either is a mistake worth reporting rather than something to
// override in silence.
func claimLineRules(p *parsedEntry, userID *openapi_types.UUID, claim store.ExpensesClaim,
	add func(field, msg string),
) {
	if userID != nil && *userID != claim.UserID {
		add("userId", "An expense inside a travel claim belongs to whoever the claim does")
	}
	switch {
	case p.ProjectID == nil:
		// The claim's project is the line's, carried through even in an
		// installation with no projects module: the stored id is data, not a
		// fact this module deletes (decision X2).
		p.ProjectID = claim.ProjectID
	case claim.ProjectID == nil || *p.ProjectID != *claim.ProjectID:
		add("projectId", "An expense inside a travel claim is booked on the claim's own project")
	}
	if p.ProjectID == nil {
		// The two fields parseProjectFields let through for a line, because it
		// could not yet know the claim's project.
		if p.LineID != nil {
			add("billingLineId", "A billing line needs the project it belongs to, and this travel claim is on none")
		}
		if p.Billable {
			add("billable", "Only an expense on a project can be billed on to a customer")
		}
	}
	// A per diem day is a day *of the trip*, so its date has to be one: the
	// departure day and the return day included, both taken as the UTC calendar
	// days of the claim's two instants — the one derivation of a claim's dates
	// this module makes (see perdiem.go's header). That a claim holds at most
	// one day per date is decided under the claim's own row lock instead, where
	// two saves racing for the same day can be told apart.
	if p.Kind == kindPerDiem {
		from, to := claimDays(claim)
		if p.Date.Before(from) || p.Date.After(to) {
			add("entryDate", perDiemOutsideTrip(from, to))
		}
	}
}

// perDiemDayRefusal is the one-per-date rule, decided under the claim's own row
// lock (the module's lock order, this file's header) so two days of the same
// date added at once cannot both take it. excludeID is the line being replaced,
// 0 on a create.
func perDiemDayRefusal(ctx context.Context, txq *store.Queries, p prepared, excludeID int64) (string, error) {
	if p.Parsed.Kind != kindPerDiem || p.Claim == nil {
		return "", nil
	}
	count, err := txq.CountClaimPerDiemOnDate(ctx, store.CountClaimPerDiemOnDateParams{
		ClaimID: &p.Claim.ID, EntryDate: pgDate(p.Parsed.Date), ExcludeID: excludeID,
	})
	if err != nil {
		return "", fmt.Errorf("expenses: count a travel claim's per diem days: %w", err)
	}
	if count == 0 {
		return "", nil
	}
	return perDiemDayTaken(p.Parsed.Date), nil
}

// claimChangedUnderSave reports whether the claim moved, between the read a
// save was judged against and the row lock it then took, in a way that makes
// what the save is about to write wrong.
//
// A line's columns are **derived** from its claim: its owner and its project
// (and the billing line, the billable flag and the figures judged against that
// project), and — for a per diem day — the day rate, the currency and the trip
// window it was priced and dated against. Those are all read before the
// transaction, because judging a project means asking the project directory and
// nothing inside a locked transaction may. So the claim has to be compared
// again once it is held.
//
// It matters most for the project. The list's visibility predicate reads a
// line's own denormalised `user_id` and `project_id` *because* they always
// equal the claim's; a line inserted with the claim's previous project would be
// visible to that project's managers and invisible to the new one's, while the
// claim's own read showed it to neither. The sibling path is already safe by
// another route: `PUT /entries/{id}` is guarded by the line's revision, which a
// re-point bumps, so a racing edit gets a 409.
//
// It refuses rather than re-deriving. Re-deriving would need the project
// directory inside the transaction, which this module forbids; and the window
// and the day rate would each need their own re-judgement under the lock for a
// race two writers on one trip have to lose anyway. The caller is told to read
// the claim again, which is what they would have to do regardless.
func claimChangedUnderSave(judged, locked store.ExpensesClaim) bool {
	return locked.UserID != judged.UserID ||
		!sameProject(locked.ProjectID, judged.ProjectID) ||
		locked.Abroad != judged.Abroad ||
		!sameDayRate(locked.AbroadDayRate, judged.AbroadDayRate) ||
		derefString(locked.AbroadCurrency) != derefString(judged.AbroadCurrency) ||
		!locked.DepartureAt.Equal(judged.DepartureAt) ||
		!locked.ReturnAt.Equal(judged.ReturnAt)
}

// claimChangedMessage is what that refusal says. It is a 400 on claimId rather
// than a 409: a create carries no revision of the claim to conflict with, and
// what refuses is a fact about the claim the caller can act on — the module's
// one rule for that code.
const claimChangedMessage = "This travel claim changed while the expense was being recorded; read it again and retry"

// sameDayRate reports whether two optional day rates are the same figure,
// comparing the decimals the columns hold rather than their representations.
func sameDayRate(a, b pgtype.Numeric) bool {
	if a.Valid != b.Valid {
		return false
	}
	if !a.Valid {
		return true
	}
	left, err := ratFromNumeric(a)
	if err != nil {
		return false
	}
	right, err := ratFromNumeric(b)
	if err != nil {
		return false
	}
	return left.Cmp(right) == 0
}

// claimLineCapRefusal is the cap of Global Constraints, decided under the
// claim's own row lock so two lines racing for the last slot cannot both take
// it.
func claimLineCapRefusal(count int64) string {
	if count < maxClaimLines {
		return ""
	}
	return fmt.Sprintf("A travel claim holds at most %d expenses", maxClaimLines)
}

// invoicedLineRefusal is the one thing a project re-point cannot do: move a
// line that has already been billed to a customer. What went out on an invoice
// keeps the project it went out under, so the claim's project has to stay too —
// whether the replace is *changing* the project or taking it off altogether.
// Either would leave the line saying it belongs to one project while the
// invoice that went out says another, and would clear the billing line it was
// invoiced against on the way.
//
// It is only ever called on a re-point, so it does not ask what the new project
// is: any re-point at all is refused while a line of the claim is invoiced.
// This is what the standalone path already does by another route — an invoiced
// entry is approved, therefore not editable, therefore its project cannot be
// changed at all.
func invoicedLineRefusal(lines []store.ExpensesEntry) string {
	for _, line := range lines {
		if line.InvoicedAt != nil {
			return fmt.Sprintf(
				"Expense %d has been invoiced, so this claim keeps the project it was invoiced under", line.ID)
		}
	}
	return ""
}

// repointPlan is what a replace decided about the claim's project before it
// took any lock: whether the lines have to follow it, whether the column is
// carried through untouched instead, and — for the lines that follow — which
// billing lines the new project actually has and whether it bills at all.
type repointPlan struct {
	repoint bool
	carry   bool
	// lines is the new project's billing line ids, read before the transaction
	// because nothing inside one may call another module. Empty for a claim
	// whose project is being cleared.
	lines map[int32]bool
	// billsNothing is decision X7 for the new project: a project that bills
	// nothing bills nothing here either, so every line on it stops being
	// billable and its figures go with the flag.
	billsNothing bool
}

// prepareClaimUpdate runs every rule a replace is held to, in the order that
// asks nothing of another module twice, and works out what the lines will need.
// Nothing here takes a lock.
func (s *server) prepareClaimUpdate(ctx context.Context, q *store.Queries, c *caller,
	current store.ExpensesClaim, body gen.ExpensesClaimUpdateRequest,
) (parsedClaim, repointPlan, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	parsed, parseErrs := parseClaim(claimBodyOfUpdate(body), s.projectsAvailable())
	for field, messages := range parseErrs {
		for _, msg := range messages {
			add(field, msg)
		}
	}
	// Revisions start at 1, and an absent one decodes as 0: refused on the
	// field rather than answered with a conflict against a revision nobody
	// read.
	if body.Revision < 1 {
		add("revision", "The revision the travel claim was read at is required")
	}
	if len(errs) > 0 {
		return parsedClaim{}, repointPlan{}, errs, nil
	}

	project, lost, err := s.checkClaimProject(ctx, current.UserID, parsed.ProjectID, &current, add)
	if err != nil {
		return parsedClaim{}, repointPlan{}, nil, err
	}
	// The lock is judged on the day the trip *has* and on the day it is being
	// given, so a locked claim can be moved neither into nor out of the lock.
	if !c.mayWritePast(utcDay(parsed.DepartureAt)) {
		add("departureAt", claimLockedMessage(*lockedBefore(c.Settings)))
	}
	if len(errs) > 0 {
		return parsedClaim{}, repointPlan{}, errs, nil
	}

	plan := repointPlan{carry: !s.projectsAvailable() || lost, lines: map[int32]bool{}}
	if plan.carry {
		return parsed, plan, nil, nil
	}
	plan.repoint = !sameProject(current.ProjectID, parsed.ProjectID)
	if !plan.repoint {
		return parsed, plan, nil, nil
	}
	if project != nil {
		plan.billsNothing = project.BillingType == billingNonBillable
		lines, err := s.projectsBillingLines(ctx, project.ID)
		if err != nil {
			return parsedClaim{}, repointPlan{}, nil, fmt.Errorf("expenses: resolve the project's billing lines: %w", err)
		}
		for _, line := range lines {
			plan.lines[line.ID] = true
		}
	}
	// Refused here as well as under the lock, so the caller hears about it
	// before anything is held.
	existing, err := q.ListClaimLines(ctx, &current.ID)
	if err != nil {
		return parsedClaim{}, repointPlan{}, nil, fmt.Errorf("expenses: read a travel claim's expenses: %w", err)
	}
	if msg := invoicedLineRefusal(existing); msg != "" {
		add("projectId", msg)
	}
	return parsed, plan, errs, nil
}

// sameProject reports whether two optional project ids are the same link.
func sameProject(a, b *int32) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return *a == *b
}

// repointParams is what one line is written with when the claim's project
// moves. A line keeps the billing line it carries only when the new project has
// it, and keeps being billable only when there is a project that bills at all —
// otherwise every billing figure goes with the flag, because a figure that
// bills nobody is a number that follows nothing.
func repointParams(line store.ExpensesEntry, projectID *int32, plan repointPlan, now time.Time) (
	store.SetClaimLineProjectParams, error,
) {
	params := store.SetClaimLineProjectParams{ID: line.ID, ProjectID: projectID, Now: now}
	billable := line.Billable && projectID != nil && !plan.billsNothing
	if line.BillingLineID != nil && projectID != nil && plan.lines[*line.BillingLineID] {
		params.BillingLineID = line.BillingLineID
	}
	if !billable {
		return params, nil
	}
	params.Billable = true
	params.MarkupPercent = line.MarkupPercent
	params.BillRatePerKm = line.BillRatePerKm
	params.BillAmount = line.BillAmount
	return params, nil
}
