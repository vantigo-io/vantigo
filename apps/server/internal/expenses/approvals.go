package expenses

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the approver's own two reads and one write: the queue of what is
// waiting for them, and decision X8's rate override on a single submitted
// mileage line or per diem day.

// GetExpensesApprovals Get the approval queue
// (GET /api/v1/expenses/approvals)
//
// The submitted **units** the caller may approve — a standalone expense, or a
// whole travel claim — grouped per person, the person who has been waiting
// longest first, paged **by person in SQL**: the database takes the page of
// people and hands back only their units, rather than every waiting one for Go
// to page afterwards.
//
// A claim is one row of the group's claims, with the figures an approver
// decides on; its lines are never listed among the entries, because a line is
// not something anybody can approve on its own. The trip's own page holds them.
//
// A caller who approves nothing at all gets the access layer's 403, as the
// batch decisions do: there is no queue for them to be shown an empty page of.
func (s *server) GetExpensesApprovals(ctx context.Context, req gen.GetExpensesApprovalsRequestObject) (gen.GetExpensesApprovalsResponseObject, error) {
	p := req.Params
	if msgs := validatePageParams(p.Page, p.PageSize); len(msgs) > 0 {
		return gen.GetExpensesApprovals400ApplicationProblemPlusJSONResponse(invalidQuery(msgs)), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	scope, err := s.approvalScopeFor(ctx, c)
	if err != nil {
		return nil, err
	}
	if !scope.approvesAny() {
		return gen.GetExpensesApprovals403JSONResponse(forbidden()), nil
	}

	total, err := q.CountApprovalGroups(ctx, store.CountApprovalGroupsParams{
		SeeAll: scope.seeAll, ManagedProjectIds: scope.managed, LockedBefore: scope.lock,
		TimeZone: c.Settings.TimeZone,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: count the approval queue's groups: %w", err)
	}
	userIDs, err := q.ListApprovalGroups(ctx, store.ListApprovalGroupsParams{
		SeeAll: scope.seeAll, ManagedProjectIds: scope.managed, LockedBefore: scope.lock,
		TimeZone: c.Settings.TimeZone,
		PageSize: pageSize, PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: page the approval queue: %w", err)
	}
	rows, err := q.ListApprovalGroupEntries(ctx, store.ListApprovalGroupEntriesParams{
		SeeAll: scope.seeAll, ManagedProjectIds: scope.managed, LockedBefore: scope.lock,
		UserIds: userIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list the approval queue: %w", err)
	}
	claims, err := q.ListApprovalGroupClaims(ctx, store.ListApprovalGroupClaimsParams{
		SeeAll: scope.seeAll, ManagedProjectIds: scope.managed, LockedBefore: scope.lock,
		TimeZone: c.Settings.TimeZone, UserIds: userIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: list the travel claims awaiting approval: %w", err)
	}
	// One renderer: an expense in the queue is shaped exactly as a single read
	// of it would be for this caller, billing object, capabilities and all.
	entries, err := s.entryResponses(ctx, c, rows)
	if err != nil {
		return nil, err
	}
	units, err := s.claimUnitsOf(ctx, q, c, claims)
	if err != nil {
		return nil, err
	}
	data, err := approvalGroups(userIDs, rows, entries, units)
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesApprovals200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}

// approvalGroups gathers one page's units into one group per person, in the
// queue's own order: the user ids arrive from SQL already sorted by who has
// waited longest, so Go re-derives nothing and the page and the order can never
// disagree. The names come off the rendered units, so the queue asks identity
// for nothing the shaping has not already asked for.
func approvalGroups(userIDs []uuid.UUID, rows []store.ExpensesEntry,
	entries []gen.ExpensesEntryResponse, units []claimUnitResponse,
) ([]gen.ExpensesApprovalGroup, error) {
	type group struct {
		user    gen.ExpensesUserRef
		entries []gen.ExpensesEntryResponse
		claims  []gen.ExpensesClaimSummary
		totals  map[string]*currencyTotal
		missing int32
		ridden  int32
	}
	byUser := make(map[uuid.UUID]*group, len(userIDs))
	for _, id := range userIDs {
		byUser[id] = &group{totals: map[string]*currencyTotal{}}
	}
	for i, row := range rows {
		g, ok := byUser[row.UserID]
		if !ok {
			continue
		}
		g.user = gen.ExpensesUserRef{
			UserId:      entries[i].Owner.UserId,
			DisplayName: entries[i].Owner.DisplayName,
			Active:      entries[i].Owner.Active,
		}
		g.entries = append(g.entries, entries[i])
		if err := addToTotals(g.totals, row); err != nil {
			return nil, err
		}
		// A receipt is what an approver checks an outlay against; mileage and a
		// per diem day take none and are never counted as missing one.
		if row.Kind == kindOutlay && entries[i].AttachmentCount == 0 {
			g.missing++
		}
		if row.RateOverriddenByUserID != nil {
			g.ridden++
		}
	}
	for _, unit := range units {
		g, ok := byUser[unit.owner.UserId]
		if !ok {
			continue
		}
		g.user = unit.owner
		g.claims = append(g.claims, unit.summary)
		addClaimToTotals(g.totals, unit.figures, false)
		// A trip's own two counts roll up into its owner's: the card above the
		// group says how much of this person's work an approver has to look at,
		// whether it is loose or gathered into a trip.
		g.missing += unit.summary.ReceiptsMissing
		g.ridden += unit.summary.OverriddenRates
	}

	data := make([]gen.ExpensesApprovalGroup, 0, len(userIDs))
	for _, id := range userIDs {
		g := byUser[id]
		if g.user.UserId == uuid.Nil {
			g.user = gen.ExpensesUserRef{UserId: id, DisplayName: unknownUser}
		}
		data = append(data, gen.ExpensesApprovalGroup{
			User: g.user, Entries: entriesOrEmpty(g.entries), Claims: claimsOrEmpty(g.claims),
			Totals: currencyTotals(g.totals), ReceiptsMissing: g.missing, OverriddenRates: g.ridden,
		})
	}
	return data, nil
}

// currencyTotal is one currency's running sum inside a group — the approval
// queue's, and the reimbursement list's — kept as exact decimals so a group of
// a hundred expenses adds up the way a person would add them (design §4:
// nothing is ever converted, and nothing rounds twice).
type currencyTotal struct {
	gross *big.Rat
	owed  *big.Rat
}

// currencySum is one currency's figures for one travel claim, as exact
// decimals: what its lines come to, and what of that its owner is owed back.
type currencySum struct {
	Currency    string
	Gross, Owed *big.Rat
}

// addClaimToTotals folds one travel claim's own per-currency figures into a
// group's, so a person's total is their loose expenses and their trips
// together. The claim's figures are already the database's sum of exact
// numerics (ClaimTotals), so this adds the two sums rather than re-adding the
// lines — one rounding, as everywhere else here — and it adds the *decimals*
// rather than the floats the response is written in.
//
// owedOnly is what keeps the two halves of a group meaning the same thing. The
// reimbursement list holds only what owes its owner something: its standalone
// half never carries a company-paid outlay at all, so a group's gross there is
// "the gross of what is owed". A trip's own gross counts every line it holds,
// company-paid outlays included, so folding that in would make one figure mean
// two things in one object. What owes is what the trip contributes there, and
// owed_to_employee is exactly the sum of the gross of the lines that owe. The
// approval queue holds whole units whatever they owe, so it adds the whole
// gross.
func addClaimToTotals(totals map[string]*currencyTotal, f claimFigures, owedOnly bool) {
	for _, line := range f.Sums {
		t, ok := totals[line.Currency]
		if !ok {
			t = &currencyTotal{gross: new(big.Rat), owed: new(big.Rat)}
			totals[line.Currency] = t
		}
		gross := line.Gross
		if owedOnly {
			gross = line.Owed
		}
		t.gross.Add(t.gross, gross)
		t.owed.Add(t.owed, line.Owed)
	}
}

// addToTotals folds one expense into its currency's total, starting that total
// when it is the first of its currency.
func addToTotals(totals map[string]*currencyTotal, row store.ExpensesEntry) error {
	t, ok := totals[row.Currency]
	if !ok {
		t = &currencyTotal{gross: new(big.Rat), owed: new(big.Rat)}
		totals[row.Currency] = t
	}
	gross, err := ratFromNumeric(row.GrossAmount)
	if err != nil {
		return err
	}
	t.gross.Add(t.gross, gross)
	t.owed.Add(t.owed, owedToEmployee(row, gross))
	return nil
}

// currencyTotals renders a group's totals, by currency code, so two reads of
// one queue never disagree about the order.
func currencyTotals(totals map[string]*currencyTotal) []gen.ExpensesCurrencyTotal {
	out := make([]gen.ExpensesCurrencyTotal, 0, len(totals))
	for currency, t := range totals {
		out = append(out, gen.ExpensesCurrencyTotal{
			Currency: currency, Gross: floatOfRat(t.gross), OwedToEmployee: floatOfRat(t.owed),
		})
	}
	slices.SortFunc(out, func(a, b gen.ExpensesCurrencyTotal) int {
		return strings.Compare(a.Currency, b.Currency)
	})
	return out
}

// rateOverrideRefusal is why an expense's rate cannot be overridden right now —
// because of what it is, not who is asking. The status and the lock are the
// *unit's* (authorize.go), so a line inside a travel claim is open to an
// approver's correction exactly while its claim is submitted.
func rateOverrideRefusal(c *caller, row store.ExpensesEntry, unit entryUnit) (string, string) {
	switch {
	case !c.mayWritePast(unit.Date):
		return "entryDate", lockedBeforeMessage(*lockedBefore(c.Settings))
	case row.Kind != kindMileage && row.Kind != kindPerDiem:
		return "kind", "Only a mileage line or a per diem day carries a rate to override"
	case unit.Status != statusSubmitted:
		return "status", fmt.Sprintf("Only a submitted expense's rate can be overridden; this one is %s", unit.Status)
	}
	return "", ""
}

// PutExpensesEntriesByIdRate Override a rate
// (PUT /api/v1/expenses/entries/{id}/rate)
//
// Decision X8's last sentence. Who may: whoever approves this expense — its
// project's manager, or expenses:approve — or expenses:manage. Not its owner as
// such; but an owner who holds one of those may, because self-approval is
// allowed and there is no reason to special-case them out of it.
//
// It reprices the line's own amount and nothing else: what the customer is
// billed comes from a rate of the customer's, which this does not touch. On a
// per diem day the day rate is what moves, and the amount is worked out again
// from it and the meal percentages the day was saved with — the line's own
// record, never the table as it stands today.
func (s *server) PutExpensesEntriesByIdRate(ctx context.Context, req gen.PutExpensesEntriesByIdRateRequestObject) (gen.PutExpensesEntriesByIdRateResponseObject, error) {
	body := gen.ExpensesRateOverrideRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	row, unit, a, found, err := s.visibleEntry(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PutExpensesEntriesByIdRate404Response{}, nil
	}
	if !a.IsApprover && !c.Manage {
		return gen.PutExpensesEntriesByIdRate403JSONResponse(forbidden()), nil
	}
	if field, msg := rateOverrideRefusal(c, row, unit); msg != "" {
		return gen.PutExpensesEntriesByIdRate400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError(field, msg))), nil
	}

	rate, passengerRate, errs, err := parseRateOverride(body, row)
	if err != nil {
		return nil, err
	}
	// Whether *this* request replaced the supplement, which is what decides
	// whether the line records what the table had said about it.
	passengerOverridden := body.PassengerRate != nil
	if len(errs) > 0 {
		return gen.PutExpensesEntriesByIdRate400ApplicationProblemPlusJSONResponse(invalidEntry(errs)), nil
	}

	var (
		updated  store.ExpensesEntry
		gone     bool
		conflict *int32
		stale    [2]string
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, lockedUnit, _, found, err := lockEntryUnit(ctx, txq, c, req.Id, row.ClaimID)
		if err != nil {
			return err
		}
		if !found {
			gone = true
			return nil
		}
		// Judged again on the row as it stands under the lock: a decision that
		// committed since is the state refusal above, arrived a moment later.
		if field, msg := rateOverrideRefusal(c, locked, lockedUnit); msg != "" {
			stale = [2]string{field, msg}
			return nil
		}
		if locked.Revision != body.Revision {
			conflict = &locked.Revision
			return nil
		}
		amount, err := overriddenAmount(locked, rate, passengerRate)
		if err != nil {
			return err
		}
		gross, err := numericFromRat(amount, moneyPlaces)
		if err != nil {
			return err
		}
		rateColumn, err := numericFromRat(rate, moneyPlaces)
		if err != nil {
			return err
		}
		passengerColumn, err := numericFromRatPtr(passengerRate, moneyPlaces)
		if err != nil {
			return err
		}
		updated, err = txq.OverrideEntryRate(ctx, store.OverrideEntryRateParams{
			ID: req.Id, Revision: body.Revision, Rate: rateColumn, PassengerRate: passengerColumn,
			GrossAmount: gross, OverriddenBy: c.UserID, PassengerOverridden: passengerOverridden,
			Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("expenses: override an expense's rate: %w", err)
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case gone:
		return gen.PutExpensesEntriesByIdRate404Response{}, nil
	case stale[1] != "":
		return gen.PutExpensesEntriesByIdRate400ApplicationProblemPlusJSONResponse(
			invalidEntry(fieldError(stale[0], stale[1]))), nil
	case conflict != nil:
		return gen.PutExpensesEntriesByIdRate409ApplicationProblemPlusJSONResponse(
			revisionConflict(*conflict, body.Revision)), nil
	}

	resp, err := s.entryResponseFor(ctx, c, updated)
	if err != nil {
		return nil, err
	}
	return gen.PutExpensesEntriesByIdRate200JSONResponse(resp), nil
}

// overriddenAmount is what a line comes to at a rate somebody replaced: the
// kilometres at the new rate per kilometre, or — for a per diem day — the new
// day rate less exactly the meal percentages the day was **saved** with. The
// deductions are the line's own record rather than the table's of today, so a
// correction to the day rate can never silently drop a breakfast somebody else
// paid for, nor pick up a percentage an administrator has changed since.
func overriddenAmount(row store.ExpensesEntry, rate, passengerRate *big.Rat) (*big.Rat, error) {
	if row.Kind == kindPerDiem {
		rates, meals, err := perDiemStored(row)
		if err != nil {
			return nil, err
		}
		rates.DayRate = rate
		return perDiemAmount(rates, meals)
	}
	km, err := ratPtrFromNumeric(row.DistanceKm)
	if err != nil {
		return nil, err
	}
	if km == nil {
		return nil, fmt.Errorf("expenses: mileage expense %d carries no distance", row.ID)
	}
	return mileageAmount(km, rate, passengerRate, int(row.Passengers)), nil
}

// parseRateOverride runs the override's own rules over its body and answers the
// two rates as exact decimals. A passenger supplement is only meaningful on a
// mileage line that carries passengers, and one left out keeps what the line
// was frozen with, so an approver correcting the rate alone does not lose it —
// and the line then records nothing about a supplement nobody touched. A per
// diem day carries no supplement at all, so naming one on one is refused rather
// than stored and never used.
func parseRateOverride(body gen.ExpensesRateOverrideRequest, row store.ExpensesEntry) (*big.Rat, *big.Rat, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) { errs = withFieldError(errs, field, msg) }

	var rate *big.Rat
	if msg := validateAboveZero("A rate", body.Rate, maxRateValue); msg != "" {
		add("rate", msg)
	} else {
		rate = ratFromFloat(body.Rate)
	}
	passengerRate, err := ratPtrFromNumeric(row.PassengerRate)
	if err != nil {
		return nil, nil, nil, err
	}
	if body.PassengerRate != nil {
		switch {
		case row.Kind == kindPerDiem:
			add("passengerRate", perDiemNoPassengers)
		case row.Passengers == 0:
			add("passengerRate", "This line carries no passengers, so it takes no passenger supplement")
		case validateDecimal("A passenger rate", *body.PassengerRate, 0, maxRateValue) != "":
			add("passengerRate", validateDecimal("A passenger rate", *body.PassengerRate, 0, maxRateValue))
		default:
			passengerRate = ratFromFloat(*body.PassengerRate)
		}
	}
	if body.Revision < 1 {
		add("revision", "The revision the expense was read at is required")
	}
	if len(errs) > 0 {
		return nil, nil, errs, nil
	}

	amount, err := overriddenAmount(row, rate, passengerRate)
	if err != nil {
		return nil, nil, nil, err
	}
	if overflowsMoney(amount) {
		add("rate", amountTooBig)
		return nil, nil, errs, nil
	}
	return rate, passengerRate, nil, nil
}
