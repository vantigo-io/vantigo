package expenses

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is decision X4's flow: submit, approve, reject and unapprove, each
// of them a batch that is all or nothing.
//
// Every one of them follows the same shape, the one the sibling time module
// set: whatever another module has to answer — the caller's role on each
// project, and what each project bills — is read *before* any row is locked;
// the transaction locks the rows in id order (deadlock-free) and judges them as
// they stand under the lock, so two callers racing over one expense end with
// one move and one clean refusal; and the response is rendered *after* the
// transaction, where the directories may be asked again.
//
// Submit is the one that freezes (design §4): it prices each line one last time
// from the tables in force on the expense's own date and stores the result.
// Nothing recomputes those figures afterwards — not a rate an administrator
// adds, not a settings change, not an approval.
//
// **Two units move through all of it.** A standalone expense, and a whole
// travel claim; a claim's lines are never a unit of their own (unitOf in
// authorize.go). Every operation here takes both lists, all or nothing across
// the two, and answers a message per refused id under the list that named it.
//
// **The lock order of a mixed batch**, which nothing else may invent:
//
//  1. the named travel claims' own rows, in id order (LockClaims), and then
//  2. every expense row the batch is about — the claims' lines and the named
//     standalone expenses alike — in one ascending pass (LockBatchEntries).
//
// It is the module's order (a claim's row before any of its lines) with the
// standalone expenses folded into the same ascending pass, which is what makes
// two batches naming each other's claims unable to cross. See LockBatchEntries'
// own comment for the cycle that one statement closes.

// maxBatchIDs bounds every batch's ids. It is deliberately not a maxItems on
// either array in the contract: the cap is on the **distinct units across both
// lists**, so a longer array that names 500 units or fewer is accepted and a
// schema bound would refuse it. The schema's description states that rule, and
// batchUnits is what enforces it.
//
// No request-validation middleware sits in front of these handlers anyway, so
// the handler enforces its own contract: without a cap, LockEntries would
// row-lock and warmRoles would look up a project role for as many ids as the
// body's 1 MiB cap allows.
const maxBatchIDs = 500

// rejectionReasonMaxLength is the rejection_reason column's width.
const rejectionReasonMaxLength = 1000

// flowOutcome is what one batch answers: forbidden for a caller who could
// approve nothing at all, the field errors of a refused request, or the moved
// units — each list in the order its own ids were given.
type flowOutcome struct {
	forbidden bool
	errs      map[string][]string
	entries   []gen.ExpensesEntryResponse
	claims    []gen.ExpensesClaimListResponse
}

// response is the body every one of the six operations answers with.
func (o flowOutcome) response() gen.ExpensesFlowResponse {
	entries, claims := o.entries, o.claims
	if entries == nil {
		entries = []gen.ExpensesEntryResponse{}
	}
	if claims == nil {
		claims = []gen.ExpensesClaimListResponse{}
	}
	return gen.ExpensesFlowResponse{Entries: entries, Claims: claims}
}

// uniqueIDs is ids with duplicates dropped, in the order given.
func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// idsOf is an optional id list as a slice; neither list of a flow request is
// required on its own, because a batch may be of either unit or of both.
func idsOf(ids *[]int64) []int64 {
	if ids == nil {
		return nil
	}
	return *ids
}

// batchUnits is the ids of one flow request, judged against the rules every one
// of the batches shares: at least one id between the two lists, and at most
// maxBatchIDs **units** across both.
//
// The cap is counted on the units, after the duplicates in each list are
// collapsed, because that is what it is for: it bounds the rows the batch locks
// and the project roles warmRoles looks up, and an id given twice adds neither.
// It counts a travel claim as one — which it is to everything but the row
// locks, where a trip of two hundred lines is two hundred rows; the cap on a
// claim's lines is what bounds that, and the two together bound the batch.
//
// A refusal goes on each list that actually named something, so a client that
// sent both is told once rather than made to guess which half was too long.
func batchUnits(entryIDs, claimIDs *[]int64, errs map[string][]string) ([]int64, []int64, map[string][]string) {
	entries := uniqueIDs(idsOf(entryIDs))
	claims := uniqueIDs(idsOf(claimIDs))
	switch total := len(entries) + len(claims); {
	case total > maxBatchIDs:
		msg := fmt.Sprintf("At most %d expenses and travel claims may be given at once; %d were given",
			maxBatchIDs, total)
		if len(entries) > 0 {
			errs = withFieldError(errs, "entryIds", msg)
		}
		if len(claims) > 0 {
			errs = withFieldError(errs, "claimIds", msg)
		}
	case total == 0:
		errs = withFieldError(errs, "entryIds", "At least one expense or travel claim id is required")
	}
	return entries, claims, errs
}

// refusalsOf is the field errors of a batch that moved nothing, keyed so a
// client can tell which of its two lists an id came from. nil when both are
// empty, which is what "nothing refused" reads as.
func refusalsOf(entryRefusals, claimRefusals []string) map[string][]string {
	var errs map[string][]string
	if len(entryRefusals) > 0 {
		errs = map[string][]string{"entryIds": entryRefusals}
	}
	if len(claimRefusals) > 0 {
		if errs == nil {
			errs = map[string][]string{}
		}
		errs["claimIds"] = claimRefusals
	}
	return errs
}

// notFoundRefusal is what an id nobody may see reads as — byte for byte what an
// id that does not exist reads as, so a refusal tells a stranger nothing.
func notFoundRefusal(id int64) string { return fmt.Sprintf("Expense %d was not found", id) }

// claimLineRefusal is the per-id message every standalone flow operation
// answers for a line that belongs to a travel claim (decision X4): the unit
// that moves is the whole claim, so the operation is asked of the claim and
// never of one of its lines. imperative is what the caller should do instead,
// in the words of the operation they asked for.
//
// It comes after the visibility check and before every other refusal, so a
// stranger still learns nothing, and whoever may see the line is pointed
// straight at the thing they can actually act on.
func claimLineRefusal(id, claimID int64, imperative string) string {
	return fmt.Sprintf("Expense %d belongs to travel claim %d; %s", id, claimID, imperative)
}

// lockedRefusal is the per-id message for an expense inside a closed period.
func (c *caller) lockedRefusal(id int64) string {
	return fmt.Sprintf("Expense %d is dated before %s, the lock date",
		id, lockedBefore(c.Settings).Format(time.DateOnly))
}

// notFoundClaimRefusal is what a claim id nobody may see reads as — byte for
// byte what an id that does not exist reads as.
func notFoundClaimRefusal(id int64) string { return fmt.Sprintf("Travel claim %d was not found", id) }

// lockedClaimRefusal is the per-id message for a trip that departed inside a
// closed period. It names the departure, because that is the day the lock
// judges a claim on.
func (c *caller) lockedClaimRefusal(id int64) string {
	return fmt.Sprintf("Travel claim %d departed before %s, the lock date",
		id, lockedBefore(c.Settings).Format(time.DateOnly))
}

// lockedBatch is every row one batch works on, held in the module's own order:
// the named claims' rows first and then every expense row of the batch in one
// ascending pass (LockBatchEntries).
//
// entries holds the named standalone expenses *and* the named claims' lines,
// keyed by id, so an entryIds naming one of a claim's lines is found here and
// refused by id rather than read as an unknown one. lines is each named claim's
// own, in id order, which is what a submit freezes and what an unapprove judges.
type lockedBatch struct {
	entries map[int64]store.ExpensesEntry
	claims  map[int64]store.ExpensesClaim
	lines   map[int64][]store.ExpensesEntry
}

// lockBatch takes those locks. An id with no row is simply absent, which every
// refusal reads as "was not found".
func lockBatch(ctx context.Context, txq *store.Queries, entryIDs, claimIDs []int64) (lockedBatch, error) {
	b := lockedBatch{
		entries: map[int64]store.ExpensesEntry{},
		claims:  map[int64]store.ExpensesClaim{},
		lines:   map[int64][]store.ExpensesEntry{},
	}
	if len(claimIDs) > 0 {
		claims, err := txq.LockClaims(ctx, claimIDs)
		if err != nil {
			return lockedBatch{}, fmt.Errorf("expenses: lock the travel claims: %w", err)
		}
		for _, claim := range claims {
			b.claims[claim.ID] = claim
		}
	}
	rows, err := txq.LockBatchEntries(ctx, store.LockBatchEntriesParams{ClaimIds: claimIDs, EntryIds: entryIDs})
	if err != nil {
		return lockedBatch{}, fmt.Errorf("expenses: lock the expenses: %w", err)
	}
	for _, row := range rows {
		b.entries[row.ID] = row
		if row.ClaimID != nil {
			b.lines[*row.ClaimID] = append(b.lines[*row.ClaimID], row)
		}
	}
	return b, nil
}

// orderedByIDs is rows in the order their ids were given, which is the order a
// batch answers in; an update returns them in none of its own.
func orderedByIDs(ids []int64, rows []store.ExpensesEntry) []store.ExpensesEntry {
	byID := make(map[int64]store.ExpensesEntry, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	out := make([]store.ExpensesEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

// orderedClaims is claims in the order their ids were given, which is the order
// a batch answers in; an update returns them in none of its own.
func orderedClaims(ids []int64, rows []store.ExpensesClaim) []store.ExpensesClaim {
	byID := make(map[int64]store.ExpensesClaim, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	out := make([]store.ExpensesClaim, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

// warmBatch reads everything another module has to answer about one batch,
// before any row is locked: the caller's role on every project the expenses are
// on, and each of those projects as the directory has it — a submit reprices
// what the customer is billed, and only the project says whether it bills at
// all. Without the projects module there is nothing to read and nothing to
// judge (decision X2).
func (s *server) warmBatch(ctx context.Context, c *caller, rows []store.ExpensesEntry,
	claims []store.ExpensesClaim,
) (map[int32]contracts.ProjectEntry, error) {
	projectIDs := make([]*int32, 0, len(rows)+len(claims))
	for _, row := range rows {
		projectIDs = append(projectIDs, row.ProjectID)
	}
	// A claim's project is every one of its lines' (docs/expenses.md, "The lock
	// order inside a claim"), so warming the claim's warms the lines the freeze
	// has not read yet — they are only loaded once the rows are locked, where no
	// directory call may be made.
	for _, claim := range claims {
		projectIDs = append(projectIDs, claim.ProjectID)
	}
	if err := c.warmRoles(ctx, s, projectIDs); err != nil {
		return nil, err
	}
	projects := map[int32]contracts.ProjectEntry{}
	if !s.projectsAvailable() {
		return projects, nil
	}
	var ids []int32
	for _, id := range projectIDs {
		if id != nil && !slices.Contains(ids, *id) {
			ids = append(ids, *id)
		}
	}
	if len(ids) == 0 {
		return projects, nil
	}
	found, err := s.projectsProjects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("expenses: resolve the expenses' projects: %w", err)
	}
	for _, p := range found {
		projects[p.ID] = p
	}
	return projects, nil
}

// PostExpensesSubmit Submit expenses
// (POST /api/v1/expenses/submit)
//
// Drafts and rejected units become submitted, all or nothing — standalone
// expenses, travel claims, or both in one batch. Each must be the caller's own
// (or anyone's, for expenses:manage) and not dated before the period lock: an
// expense on its own date, a claim on the day it departed. This is where design
// §4's freezing happens: every line is priced one last time and the result
// stored, a claim's lines all in the transaction that submits it.
func (s *server) PostExpensesSubmit(ctx context.Context, req gen.PostExpensesSubmitRequestObject) (gen.PostExpensesSubmitResponseObject, error) {
	body := gen.ExpensesFlowRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	ids, claimIDs, errs := batchUnits(body.EntryIds, body.ClaimIds, nil)
	if len(errs) > 0 {
		return gen.PostExpensesSubmit400ApplicationProblemPlusJSONResponse(invalidSubmission(errs)), nil
	}

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	before, err := q.GetEntries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("expenses: read the expenses: %w", err)
	}
	claimsBefore, err := q.GetClaims(ctx, claimIDs)
	if err != nil {
		return nil, fmt.Errorf("expenses: read the travel claims: %w", err)
	}
	projects, err := s.warmBatch(ctx, c, before, claimsBefore)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var refusals, claimRefusals []string
	var moved []store.ExpensesEntry
	var movedClaims []store.ExpensesClaim
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		b, err := lockBatch(ctx, txq, ids, claimIDs)
		if err != nil {
			return err
		}
		frozen := make(map[int64]frozenEntry, len(ids))
		for _, id := range ids {
			row, ok := b.entries[id]
			if !ok {
				refusals = append(refusals, notFoundRefusal(id))
				continue
			}
			if msg := c.submitRefusal(id, row); msg != "" {
				refusals = append(refusals, msg)
				continue
			}
			f, msg, err := s.freeze(ctx, txq, c, row, nil, projects)
			if err != nil {
				return err
			}
			if msg == "" {
				msg, err = receiptRefusal(ctx, txq, c, row)
				if err != nil {
					return err
				}
			}
			if msg != "" {
				refusals = append(refusals, msg)
				continue
			}
			frozen[id] = f
		}
		frozenClaims := make(map[int64][]frozenLine, len(claimIDs))
		for _, id := range claimIDs {
			claim, ok := b.claims[id]
			if !ok {
				claimRefusals = append(claimRefusals, notFoundClaimRefusal(id))
				continue
			}
			if msg := c.submitClaimRefusal(id, claim); msg != "" {
				claimRefusals = append(claimRefusals, msg)
				continue
			}
			lines, msg, err := s.freezeClaim(ctx, txq, c, claim, b.lines[id], projects)
			if err != nil {
				return err
			}
			if msg != "" {
				claimRefusals = append(claimRefusals, msg)
				continue
			}
			frozenClaims[id] = lines
		}
		if len(refusals) > 0 || len(claimRefusals) > 0 {
			return nil
		}
		for _, id := range ids {
			f := frozen[id]
			row, err := txq.SubmitEntry(ctx, store.SubmitEntryParams{
				ID: id, Rate: f.Rate, PassengerRate: f.PassengerRate, GrossAmount: f.Gross,
				Billable: f.Billable, MarkupPercent: f.Markup, BillRatePerKm: f.BillRate,
				BillAmount: f.BillAmount, Now: now,
			})
			if err != nil {
				return fmt.Errorf("expenses: submit an expense: %w", err)
			}
			moved = append(moved, row)
		}
		for _, id := range claimIDs {
			for _, line := range frozenClaims[id] {
				if err := txq.FreezeClaimLine(ctx, store.FreezeClaimLineParams{
					ID: line.ID, ClaimID: &id, Currency: line.Currency,
					Rate: line.Rate, PassengerRate: line.PassengerRate, GrossAmount: line.Gross,
					MealBreakfastPercent: line.Breakfast, MealLunchPercent: line.Lunch,
					MealDinnerPercent: line.Dinner,
					Billable:          line.Billable, MarkupPercent: line.Markup,
					BillRatePerKm: line.BillRate, BillAmount: line.BillAmount, Now: now,
				}); err != nil {
					return fmt.Errorf("expenses: freeze a travel claim's expense: %w", err)
				}
			}
			row, err := txq.SubmitClaim(ctx, store.SubmitClaimParams{ID: id, Now: now})
			if err != nil {
				return fmt.Errorf("expenses: submit a travel claim: %w", err)
			}
			movedClaims = append(movedClaims, row)
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case len(refusals) > 0 || len(claimRefusals) > 0:
		return gen.PostExpensesSubmit400ApplicationProblemPlusJSONResponse(
			invalidSubmission(refusalsOf(refusals, claimRefusals))), nil
	}

	out, err := s.movedResponse(ctx, c, ids, moved, claimIDs, movedClaims)
	if err != nil {
		return nil, err
	}
	return gen.PostExpensesSubmit200JSONResponse(out.response()), nil
}

// movedResponse renders what a batch moved, after the transaction has committed
// — which is where the directories may be asked again.
func (s *server) movedResponse(ctx context.Context, c *caller, entryIDs []int64, moved []store.ExpensesEntry,
	claimIDs []int64, movedClaims []store.ExpensesClaim,
) (flowOutcome, error) {
	entries, err := s.entryResponses(ctx, c, orderedByIDs(entryIDs, moved))
	if err != nil {
		return flowOutcome{}, err
	}
	claims, err := s.claimListResponses(ctx, c, orderedClaims(claimIDs, movedClaims))
	if err != nil {
		return flowOutcome{}, err
	}
	return flowOutcome{entries: entries, claims: claims}, nil
}

// submitRefusal is why the expense id may not be submitted by c, "" when it
// may. It runs inside the locked transaction on the row as it stands there, so
// it reads c's roles only from the cache warmBatch filled.
//
// The checks come in the order that tells the caller least: an expense they may
// not see is the unknown id's "was not found", one they see but do not own says
// only that, and only then are its status and its date judged.
func (c *caller) submitRefusal(id int64, row store.ExpensesEntry) string {
	unit := unitOf(row, nil, c.zone())
	a := c.accessFor(row, unit, c.cachedRole(row.ProjectID))
	switch {
	case !a.CanSee:
		return notFoundRefusal(id)
	case row.ClaimID != nil:
		return claimLineRefusal(id, *row.ClaimID, "submit the claim")
	case !a.IsWriter:
		return fmt.Sprintf("Expense %d is not yours", id)
	case !slices.Contains(editableStatuses, row.Status):
		return fmt.Sprintf("Expense %d is %s, and only a draft or a rejected expense can be submitted", id, row.Status)
	case !c.mayWritePast(row.EntryDate.Time):
		return c.lockedRefusal(id)
	}
	return ""
}

// submitClaimRefusal is submitRefusal for the other unit: why this travel claim
// may not be submitted by c, judged on the row as it stands under its own lock.
// Whether it holds anything to submit is freezeClaim's, because that needs the
// lines.
func (c *caller) submitClaimRefusal(id int64, claim store.ExpensesClaim) string {
	unit := claimUnit(claim, c.zone())
	a := c.claimAccessFor(claim, c.cachedRole(claim.ProjectID), claimFigures{})
	switch {
	case !a.CanSee:
		return notFoundClaimRefusal(id)
	case !a.IsWriter:
		return fmt.Sprintf("Travel claim %d is not yours", id)
	case !unit.editable():
		return fmt.Sprintf("Travel claim %d is %s, and only a draft or a rejected travel claim can be submitted",
			id, claim.Status)
	case !c.mayWritePast(unit.Date):
		return c.lockedClaimRefusal(id)
	}
	return ""
}

// frozenLine is one of a claim's lines and the figures its submit writes on it.
type frozenLine struct {
	ID int64
	frozenEntry
}

// freezeClaim prices every line of one travel claim for the last time, in the
// transaction that is about to submit it. It answers the lines to write, or the
// one per-id message that refuses the **whole** claim: a trip is submitted
// entire or not at all, so a line the tables cannot price stops the submit
// rather than going forward at a stale figure.
//
// Three things are judged here rather than in submitClaimRefusal, because all
// three need the lines and the lines are only read once the rows are locked:
// that the trip holds anything at all, that no per diem day has been left
// outside it, and the receipt rule, which is per employee-paid outlay line.
func (s *server) freezeClaim(ctx context.Context, txq *store.Queries, c *caller, claim store.ExpensesClaim,
	lines []store.ExpensesEntry, projects map[int32]contracts.ProjectEntry,
) ([]frozenLine, string, error) {
	if len(lines) == 0 {
		return nil, fmt.Sprintf("Travel claim %d holds no expenses, so there is nothing to submit", claim.ID), nil
	}
	// The trip's window, re-judged. Every door that records a per diem day
	// checks it, and one thing can still move a day outside its own trip
	// afterwards: an administrator changing the installation's business time
	// zone, which moves every trip's days and sweeps nothing. The submit is the
	// last place to notice, and noticing costs nothing here.
	if msg := perDiemStrandedOnSubmit(claim, lines, c.zone()); msg != "" {
		return nil, msg, nil
	}
	out := make([]frozenLine, 0, len(lines))
	for _, line := range lines {
		f, msg, err := s.freeze(ctx, txq, c, line, &claim, projects)
		if err != nil {
			return nil, "", err
		}
		if msg == "" {
			if msg, err = receiptRefusal(ctx, txq, c, line); err != nil {
				return nil, "", err
			}
		}
		if msg != "" {
			return nil, fmt.Sprintf("Travel claim %d cannot be submitted: %s", claim.ID, msg), nil
		}
		out = append(out, frozenLine{ID: line.ID, frozenEntry: f})
	}
	return out, "", nil
}

// perDiemStrandedOnSubmit is the refusal when a trip holds a per diem day that
// its own window no longer covers. It names every such date, so one round trip
// tells the caller exactly which days to remove.
func perDiemStrandedOnSubmit(claim store.ExpensesClaim, lines []store.ExpensesEntry, loc *time.Location) string {
	from, to := claimDays(claim, loc)
	var stranded []string
	for _, line := range lines {
		if line.Kind != kindPerDiem {
			continue
		}
		if date := line.EntryDate.Time; date.Before(from) || date.After(to) {
			stranded = append(stranded, date.Format(time.DateOnly))
		}
	}
	if len(stranded) == 0 {
		return ""
	}
	return fmt.Sprintf("Travel claim %d holds a per diem day on %s, which the trip it covers (%s to %s) no longer does; remove %s first",
		claim.ID, strings.Join(stranded, ", "),
		from.Format(time.DateOnly), to.Format(time.DateOnly), theDay(len(stranded)))
}

// frozenEntry is what a submit writes onto one expense: the figures it will
// carry from then on, already in the columns' own shape.
type frozenEntry struct {
	Rate          pgtype.Numeric
	PassengerRate pgtype.Numeric
	Gross         pgtype.Numeric
	Billable      bool
	Markup        pgtype.Numeric
	BillRate      pgtype.Numeric
	BillAmount    pgtype.Numeric

	// The four a per diem day is priced from as well as at: the currency the
	// day is paid in and the three percentages that were deducted from it. They
	// are carried unchanged for every other kind — what somebody typed is not
	// something a freeze recomputes — and only FreezeClaimLine writes them,
	// because a per diem day exists only inside a travel claim.
	Currency                 string
	Breakfast, Lunch, Dinner pgtype.Numeric
}

// freeze prices one expense for the last time (design §4). It answers the
// figures to store, or a per-id refusal when the tables cannot price the line
// at all. It runs inside the locked transaction and reads only this module's
// own tables: the projects it needs were read before it (warmBatch).
//
// Everything the caller could have set is left exactly as it is — the gross of
// an outlay is what somebody typed, not something to recompute. What follows a
// table follows it one last time: the mileage rate, the passenger supplement
// and the amount they make, and what the customer is billed.
//
// The billing figures are *carried through untouched* when nothing can judge
// them: an installation with no projects module, or a project the directory no
// longer resolves (decision X2 — the stored ids stay). Otherwise they follow
// exactly the rule a save follows: a figure already on the line is kept, and
// only a line carrying none falls back to the server's own.
func (s *server) freeze(ctx context.Context, txq *store.Queries, c *caller, row store.ExpensesEntry,
	claim *store.ExpensesClaim, projects map[int32]contracts.ProjectEntry,
) (frozenEntry, string, error) {
	f := frozenEntry{
		Rate: row.Rate, PassengerRate: row.PassengerRate, Gross: row.GrossAmount,
		Billable: row.Billable, Markup: row.MarkupPercent,
		BillRate: row.BillRatePerKm, BillAmount: row.BillAmount,
		Currency:  row.Currency,
		Breakfast: row.MealBreakfastPercent, Lunch: row.MealLunchPercent, Dinner: row.MealDinnerPercent,
	}
	date := row.EntryDate.Time
	gross, err := ratFromNumeric(row.GrossAmount)
	if err != nil {
		return frozenEntry{}, "", err
	}
	vat, err := ratPtrFromNumeric(row.VatAmount)
	if err != nil {
		return frozenEntry{}, "", err
	}
	km, err := ratPtrFromNumeric(row.DistanceKm)
	if err != nil {
		return frozenEntry{}, "", err
	}

	if row.Kind == kindMileage {
		rate, err := rateFor(ctx, txq, rateKindMileage, date)
		if errors.Is(err, errNoRate) {
			return frozenEntry{}, fmt.Sprintf("Expense %d has no mileage rate in force on %s",
				row.ID, date.Format(time.DateOnly)), nil
		}
		if err != nil {
			return frozenEntry{}, "", err
		}
		var passengerRate *big.Rat
		if row.Passengers > 0 {
			supplement, err := rateFor(ctx, txq, rateKindMileagePassenger, date)
			if errors.Is(err, errNoRate) {
				return frozenEntry{}, fmt.Sprintf("Expense %d has no passenger rate in force on %s",
					row.ID, date.Format(time.DateOnly)), nil
			}
			if err != nil {
				return frozenEntry{}, "", err
			}
			passengerRate = supplement.Value
		}
		if km == nil {
			return frozenEntry{}, "", fmt.Errorf("expenses: mileage expense %d carries no distance", row.ID)
		}
		gross = mileageAmount(km, rate.Value, passengerRate, int(row.Passengers))
		if overflowsMoney(gross) {
			return frozenEntry{}, fmt.Sprintf("Expense %d works out to more than %s at the rate in force on %s",
				row.ID, formatNumber(maxMoney), date.Format(time.DateOnly)), nil
		}
		if f.Rate, err = numericFromRat(rate.Value, moneyPlaces); err != nil {
			return frozenEntry{}, "", err
		}
		if f.PassengerRate, err = numericFromRatPtr(passengerRate, moneyPlaces); err != nil {
			return frozenEntry{}, "", err
		}
		if f.Gross, err = numericFromRat(gross, moneyPlaces); err != nil {
			return frozenEntry{}, "", err
		}
	}

	if row.Kind == kindPerDiem {
		// A per diem day is priced from the *claim* as much as from the table —
		// the day rate abroad, the currency — so it is frozen against the claim
		// this transaction holds, never one read before the lock. It is the one
		// recompute a draft save makes (perdiem.go), run once more and written.
		if claim == nil || row.PerDiemType == nil {
			return frozenEntry{}, "", fmt.Errorf("expenses: per diem expense %d has no travel claim to price it", row.ID)
		}
		_, meals, err := perDiemStored(row)
		if err != nil {
			return frozenEntry{}, "", err
		}
		rates, missing, err := perDiemFiguresFor(ctx, txq, *claim, *row.PerDiemType, date, meals)
		if err != nil {
			return frozenEntry{}, "", err
		}
		if missing.DayRate {
			return frozenEntry{}, fmt.Sprintf("Expense %d cannot be priced: %s",
				row.ID, perDiemNoDayRate(*claim, *row.PerDiemType, date)), nil
		}
		if missing.Breakfast || missing.Lunch || missing.Dinner {
			return frozenEntry{}, fmt.Sprintf("Expense %d cannot be priced: %s on %s",
				row.ID, perDiemNoMealRate, date.Format(time.DateOnly)), nil
		}
		amount, err := perDiemAmount(rates, meals)
		if err != nil {
			return frozenEntry{}, "", err
		}
		f.Currency = perDiemCurrency(*claim, c.Settings.DefaultCurrency)
		for _, conv := range []struct {
			value *big.Rat
			into  *pgtype.Numeric
		}{
			{rates.DayRate, &f.Rate},
			{rates.Breakfast, &f.Breakfast},
			{rates.Lunch, &f.Lunch},
			{rates.Dinner, &f.Dinner},
			{amount, &f.Gross},
		} {
			if *conv.into, err = numericFromRatPtr(conv.value, moneyPlaces); err != nil {
				return frozenEntry{}, "", err
			}
		}
		// A per diem day is never billed on to a customer, so nothing below
		// applies to it and the billing columns stay as they are — false and
		// empty, by every door that can write them.
		return f, "", nil
	}

	var project *contracts.ProjectEntry
	if row.ProjectID != nil {
		found, known := projects[*row.ProjectID]
		if !known {
			// Nothing here can judge what the customer is billed — the project
			// is gone from the directory — so nothing here writes it: the
			// stored columns go through as they are (decision X2).
			return f, "", nil
		}
		project = &found
	}
	if !s.projectsAvailable() {
		return f, "", nil
	}

	in, err := billingInputFor(row, project)
	if err != nil {
		return frozenEntry{}, "", err
	}
	in.Gross, in.Vat, in.Km = gross, vat, km
	in.Billable = row.Billable
	if in.DefaultMarkup, err = ratFromNumeric(c.Settings.DefaultMarkupPercent); err != nil {
		return frozenEntry{}, "", err
	}
	figures, err := resolveBilling(ctx, txq, in)
	if err != nil {
		return frozenEntry{}, "", err
	}
	if overflowsMoney(figures.BillAmount) {
		return frozenEntry{}, fmt.Sprintf("Expense %d bills more than %s, which is more than an expense can hold",
			row.ID, formatNumber(maxMoney)), nil
	}
	f.Billable = figures.Billable
	if f.Markup, f.BillRate, f.BillAmount, err = billingColumns(figures); err != nil {
		return frozenEntry{}, "", err
	}
	return f, "", nil
}

// receiptRefusal is design §4's receipt rule, judged under the expense's own
// row lock: when the settings name a threshold, an employee-paid outlay whose
// gross *exceeds* it cannot be submitted without a receipt. The word is the
// spec's, so the boundary is strict — a gross equal to the threshold needs
// none, and a threshold of zero therefore asks for one on every employee-paid
// outlay. The company's own outlays are exempt, and so is mileage, which takes
// no receipt at all.
//
// The count is read on the same transaction that holds the row, so a receipt
// deleted a moment ago cannot let a line through.
func receiptRefusal(ctx context.Context, txq *store.Queries, c *caller, row store.ExpensesEntry) (string, error) {
	if !c.Settings.ReceiptRequiredOver.Valid || row.Kind != kindOutlay {
		return "", nil
	}
	if row.PaidBy == nil || *row.PaidBy != paidByEmployee {
		return "", nil
	}
	threshold, err := ratFromNumeric(c.Settings.ReceiptRequiredOver)
	if err != nil {
		return "", err
	}
	gross, err := ratFromNumeric(row.GrossAmount)
	if err != nil {
		return "", err
	}
	if gross.Cmp(threshold) <= 0 {
		return "", nil
	}
	count, err := txq.CountAttachmentsForEntry(ctx, row.ID)
	if err != nil {
		return "", fmt.Errorf("expenses: count an expense's receipts: %w", err)
	}
	if count > 0 {
		return "", nil
	}
	return fmt.Sprintf("Expense %d needs a receipt: an outlay the employee paid for more than %s cannot be submitted without one",
		row.ID, decimalText(threshold, moneyPlaces)), nil
}

// decision is one move a batch makes over expenses that are already approved
// or on their way there: the status it moves them from, what a refusal calls
// the move, who may make it, the refusals only this move has, and the update
// that makes it on rows already locked and judged.
//
// Every batch in this module is one of these — the three an approver makes,
// and the two a payroll run makes (reimbursements.go) — so the mechanics
// (locking in id order, judging under the lock, all or nothing, rendering
// after the commit) exist once.
type decision struct {
	from string
	verb string

	// claimImperative is what a caller who named one of a travel claim's lines
	// should do instead — "approve the claim", "mark the claim reimbursed" —
	// in the words of this move. Every batch has one, because every batch is
	// about a unit and a line is never one.
	claimImperative string

	// orManage is whether expenses:manage may make this move as well as an
	// approver; manageOnly is whether expenses:manage is the *only* one who
	// may, which is what the payroll track is — approving an expense is not
	// paying for it.
	orManage   bool
	manageOnly bool

	// also is the refusals only this move has, judged after the status and
	// before the period lock, on the row as it stands under the lock.
	also func(id int64, row store.ExpensesEntry) string

	// claimAlso is also for the other unit, given the claim's own locked lines
	// — which is what the two refusals a trip has are about: whether its money
	// has moved, and whether one of its lines has been invoiced.
	claimAlso func(id int64, claim store.ExpensesClaim, lines []store.ExpensesEntry) string

	apply       func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error)
	applyClaims func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesClaim, error)
}

// article is "an" before a status that begins with a vowel and "a" before the
// rest, so a refusal reads as a sentence.
func article(status string) string {
	if strings.ContainsRune("aeiou", rune(status[0])) {
		return "an"
	}
	return "a"
}

// decisionRefusal is why c may not move the expense id through d, "" when they
// may. It runs inside the locked transaction on the row as it stands there and
// reads c's roles only from the cache warmBatch filled.
func (c *caller) decisionRefusal(d decision, id int64, row store.ExpensesEntry) string {
	unit := unitOf(row, nil, c.zone())
	a := c.accessFor(row, unit, c.cachedRole(row.ProjectID))
	mayMove := a.IsApprover || (d.orManage && c.Manage)
	if d.manageOnly {
		mayMove = c.Manage
	}
	switch {
	case !a.CanSee:
		return notFoundRefusal(id)
	case row.ClaimID != nil:
		return claimLineRefusal(id, *row.ClaimID, d.claimImperative)
	case !mayMove:
		// Deliberately no more than a 404 would tell them: that it exists and
		// is not theirs, and nothing about whose it is or what it is on.
		return fmt.Sprintf("Expense %d is not yours to approve", id)
	case row.Status != d.from:
		return fmt.Sprintf("Expense %d is %s, and only %s %s expense can be %s",
			id, row.Status, article(d.from), d.from, d.verb)
	}
	if d.also != nil {
		if msg := d.also(id, row); msg != "" {
			return msg
		}
	}
	// The period lock holds back what the employee submitted and what was
	// approved. It does not hold back the payroll track, which runs after a
	// period closes — and could not, because only expenses:manage marks a
	// reimbursement and the lock never held expenses:manage back anyway.
	if !c.mayWritePast(row.EntryDate.Time) {
		return c.lockedRefusal(id)
	}
	return ""
}

// claimDecisionRefusal is decisionRefusal for the other unit: why c may not
// move this travel claim through d. It is the same shape and the same order —
// a claim they may not see reads as the unknown id, one they see but may not
// decide says only that, and only then are its status, the move's own refusals
// and the period lock judged — on the claim's own departure day.
func (c *caller) claimDecisionRefusal(d decision, id int64, claim store.ExpensesClaim,
	lines []store.ExpensesEntry,
) string {
	unit := claimUnit(claim, c.zone())
	a := c.claimAccessFor(claim, c.cachedRole(claim.ProjectID), claimFigures{})
	mayMove := a.IsApprover || (d.orManage && c.Manage)
	if d.manageOnly {
		mayMove = c.Manage
	}
	switch {
	case !a.CanSee:
		return notFoundClaimRefusal(id)
	case !mayMove:
		return fmt.Sprintf("Travel claim %d is not yours to approve", id)
	case claim.Status != d.from:
		return fmt.Sprintf("Travel claim %d is %s, and only %s %s travel claim can be %s",
			id, claim.Status, article(d.from), d.from, d.verb)
	}
	if d.claimAlso != nil {
		if msg := d.claimAlso(id, claim, lines); msg != "" {
			return msg
		}
	}
	if !c.mayWritePast(unit.Date) {
		return c.lockedClaimRefusal(id)
	}
	return ""
}

// paidOutRefusal is unapprove's own pair of refusals: an expense whose money
// has already moved, in either direction, is not something to send back to its
// owner as a draft. Undoing those is each track's own operation.
func paidOutRefusal(id int64, row store.ExpensesEntry) string {
	switch {
	case row.ReimbursedAt != nil:
		return fmt.Sprintf("Expense %d has been reimbursed", id)
	case row.InvoicedAt != nil:
		return fmt.Sprintf("Expense %d has been invoiced", id)
	}
	return ""
}

// claimPaidOutRefusal is paidOutRefusal one level up. The reimbursement is the
// claim's own, so it reads off the claim; the invoicing is each line's, so it
// reads the lines — a trip with one invoiced line cannot go back to being a
// draft its owner may rewrite, and the message names the line so whoever has to
// undo the invoice knows which one it is.
func claimPaidOutRefusal(id int64, claim store.ExpensesClaim, lines []store.ExpensesEntry) string {
	if claim.ReimbursedAt != nil {
		return fmt.Sprintf("Travel claim %d has been reimbursed", id)
	}
	for _, line := range lines {
		if line.InvoicedAt != nil {
			return fmt.Sprintf("Travel claim %d holds expense %d, which has been invoiced", id, line.ID)
		}
	}
	return ""
}

// decide runs d over the request's ids, of both units. errs is what the body's
// own fields already failed (reject's reason); it is reported before any unit is
// judged.
func (s *server) decide(ctx context.Context, d decision, entryIDs, claimIDs *[]int64,
	errs map[string][]string,
) (flowOutcome, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return flowOutcome{}, err
	}
	// A caller who could approve nothing at all is refused the whole request,
	// as the access layer would, rather than told about each id (decision
	// X10). It is the same gate for both units: whoever approves no expense
	// anywhere approves no trip either, because a trip is approved by the very
	// rights an expense is. A manage-only move has no such gate to apply: the
	// router has already required expenses:manage of it.
	if !d.manageOnly {
		approves, err := c.approvesAnything(ctx, s, d.orManage)
		if err != nil {
			return flowOutcome{}, err
		}
		if !approves {
			return flowOutcome{forbidden: true}, nil
		}
	}

	ids, claims, errs := batchUnits(entryIDs, claimIDs, errs)
	if len(errs) > 0 {
		return flowOutcome{errs: errs}, nil
	}
	before, err := q.GetEntries(ctx, ids)
	if err != nil {
		return flowOutcome{}, fmt.Errorf("expenses: read the expenses: %w", err)
	}
	claimsBefore, err := q.GetClaims(ctx, claims)
	if err != nil {
		return flowOutcome{}, fmt.Errorf("expenses: read the travel claims: %w", err)
	}
	if _, err := s.warmBatch(ctx, c, before, claimsBefore); err != nil {
		return flowOutcome{}, err
	}

	var refusals, claimRefusals []string
	var moved []store.ExpensesEntry
	var movedClaims []store.ExpensesClaim
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		b, err := lockBatch(ctx, txq, ids, claims)
		if err != nil {
			return err
		}
		for _, id := range ids {
			row, ok := b.entries[id]
			if !ok {
				refusals = append(refusals, notFoundRefusal(id))
				continue
			}
			if msg := c.decisionRefusal(d, id, row); msg != "" {
				refusals = append(refusals, msg)
			}
		}
		for _, id := range claims {
			claim, ok := b.claims[id]
			if !ok {
				claimRefusals = append(claimRefusals, notFoundClaimRefusal(id))
				continue
			}
			if msg := c.claimDecisionRefusal(d, id, claim, b.lines[id]); msg != "" {
				claimRefusals = append(claimRefusals, msg)
			}
		}
		if len(refusals) > 0 || len(claimRefusals) > 0 {
			return nil
		}
		if len(ids) > 0 {
			if moved, err = d.apply(ctx, txq, ids); err != nil {
				return err
			}
			if len(moved) != len(ids) {
				// Every row is locked and was judged movable; the update's own
				// guard refusing one means the two disagree — a bug, not a race,
				// and never a 200 with a blank expense in it.
				return fmt.Errorf("expenses: the %s moved %d of %d judged expenses", d.verb, len(moved), len(ids))
			}
		}
		if len(claims) > 0 {
			if movedClaims, err = d.applyClaims(ctx, txq, claims); err != nil {
				return err
			}
			if len(movedClaims) != len(claims) {
				return fmt.Errorf("expenses: the %s moved %d of %d judged travel claims",
					d.verb, len(movedClaims), len(claims))
			}
		}
		return nil
	})
	if err != nil {
		return flowOutcome{}, err
	}
	if len(refusals) > 0 || len(claimRefusals) > 0 {
		return flowOutcome{errs: refusalsOf(refusals, claimRefusals)}, nil
	}
	return s.movedResponse(ctx, c, ids, moved, claims, movedClaims)
}

// PostExpensesApprove Approve expenses
// (POST /api/v1/expenses/approve)
//
// Submitted units, each one the caller approves for — expenses:approve for
// anything, the manager role for a project's own — become approved, recording
// who decided and when. The frozen figures are not touched.
func (s *server) PostExpensesApprove(ctx context.Context, req gen.PostExpensesApproveRequestObject) (gen.PostExpensesApproveResponseObject, error) {
	body := gen.ExpensesFlowRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	decider, now := callerID(ctx), s.deps.Clock()
	out, err := s.decide(ctx, decision{
		from: statusSubmitted, verb: "approved", claimImperative: "approve the claim",
		apply: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error) {
			rows, err := txq.ApproveEntries(ctx, store.ApproveEntriesParams{Ids: ids, DecidedBy: decider, Now: now})
			if err != nil {
				return nil, fmt.Errorf("expenses: approve expenses: %w", err)
			}
			return rows, nil
		},
		applyClaims: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesClaim, error) {
			rows, err := txq.ApproveClaims(ctx, store.ApproveClaimsParams{Ids: ids, DecidedBy: decider, Now: now})
			if err != nil {
				return nil, fmt.Errorf("expenses: approve travel claims: %w", err)
			}
			return rows, nil
		},
	}, body.EntryIds, body.ClaimIds, nil)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesApprove403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesApprove400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostExpensesApprove200JSONResponse(out.response()), nil
}

// validateReason is reject's reason rule: required, at most 1000 characters
// once trimmed. It answers the trimmed reason and the message, "" when the rule
// holds.
func validateReason(raw string) (string, string) {
	reason := strings.TrimSpace(raw)
	if reason == "" {
		return "", "A reason is required to reject expenses"
	}
	if n := utf8.RuneCountInString(reason); n > rejectionReasonMaxLength {
		return "", fmt.Sprintf("A reason cannot be longer than %d characters, the given value was %d characters",
			rejectionReasonMaxLength, n)
	}
	return reason, ""
}

// PostExpensesReject Reject expenses
// (POST /api/v1/expenses/reject)
//
// Under exactly the rules of an approval, submitted units become rejected with
// a reason their owner sees. A rejected unit is theirs again — to edit, to
// delete, or to submit as it stands, which prices it once more.
func (s *server) PostExpensesReject(ctx context.Context, req gen.PostExpensesRejectRequestObject) (gen.PostExpensesRejectResponseObject, error) {
	body := gen.ExpensesRejectRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	reason, msg := validateReason(body.Reason)
	var errs map[string][]string
	if msg != "" {
		errs = fieldError("reason", msg)
	}
	decider, now := callerID(ctx), s.deps.Clock()
	out, err := s.decide(ctx, decision{
		from: statusSubmitted, verb: "rejected", claimImperative: "reject the claim",
		apply: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error) {
			rows, err := txq.RejectEntries(ctx, store.RejectEntriesParams{
				Ids: ids, DecidedBy: decider, Reason: reason, Now: now,
			})
			if err != nil {
				return nil, fmt.Errorf("expenses: reject expenses: %w", err)
			}
			return rows, nil
		},
		applyClaims: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesClaim, error) {
			rows, err := txq.RejectClaims(ctx, store.RejectClaimsParams{
				Ids: ids, DecidedBy: decider, Reason: reason, Now: now,
			})
			if err != nil {
				return nil, fmt.Errorf("expenses: reject travel claims: %w", err)
			}
			return rows, nil
		},
	}, body.EntryIds, body.ClaimIds, errs)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesReject403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesReject400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostExpensesReject200JSONResponse(out.response()), nil
}

// PostExpensesUnapprove Unapprove expenses
// (POST /api/v1/expenses/unapprove)
//
// Approved units become fresh drafts — by whoever could have approved them, or
// by expenses:manage — with the decision and the submission stamp cleared, so
// the owner sees something to change and send again. A travel claim's lines
// lose the record of any rate an approver replaced with them, exactly as a
// standalone expense does. Never one that has been reimbursed or invoiced: the
// reimbursement track and the invoicing track each have an undo of their own.
func (s *server) PostExpensesUnapprove(ctx context.Context, req gen.PostExpensesUnapproveRequestObject) (gen.PostExpensesUnapproveResponseObject, error) {
	body := gen.ExpensesFlowRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	now := s.deps.Clock()
	out, err := s.decide(ctx, decision{
		from: statusApproved, verb: "unapproved", orManage: true,
		also: paidOutRefusal, claimAlso: claimPaidOutRefusal,
		claimImperative: "unapprove the claim",
		apply: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error) {
			rows, err := txq.UnapproveEntries(ctx, store.UnapproveEntriesParams{Ids: ids, Now: now})
			if err != nil {
				return nil, fmt.Errorf("expenses: unapprove expenses: %w", err)
			}
			return rows, nil
		},
		applyClaims: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesClaim, error) {
			// The audit first, while the claim is still approved — the two
			// statements are one move, in one transaction, under locks the
			// caller already holds.
			if err := txq.ClearClaimLineOverrides(ctx, store.ClearClaimLineOverridesParams{
				ClaimIds: ids, Now: now,
			}); err != nil {
				return nil, fmt.Errorf("expenses: clear a travel claim's rate overrides: %w", err)
			}
			rows, err := txq.UnapproveClaims(ctx, store.UnapproveClaimsParams{Ids: ids, Now: now})
			if err != nil {
				return nil, fmt.Errorf("expenses: unapprove travel claims: %w", err)
			}
			return rows, nil
		},
	}, body.EntryIds, body.ClaimIds, nil)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesUnapprove403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesUnapprove400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostExpensesUnapprove200JSONResponse(out.response()), nil
}
