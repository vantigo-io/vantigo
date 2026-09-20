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

// maxBatchIDs bounds every batch's ids, matching the contract's maxItems. No
// request-validation middleware sits in front of these handlers, so the handler
// enforces its own contract: without a cap, LockEntries would row-lock and
// warmRoles would look up a project role for as many ids as the body's 1 MiB
// cap allows.
const maxBatchIDs = 500

// rejectionReasonMaxLength is the rejection_reason column's width.
const rejectionReasonMaxLength = 1000

// flowOutcome is what one batch answers: forbidden for a caller who could
// approve nothing at all, the field errors of a refused request, or the moved
// expenses in the order their ids were given.
type flowOutcome struct {
	forbidden bool
	errs      map[string][]string
	entries   []gen.ExpensesEntryResponse
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

// batchIDs is the ids of one flow request, judged against the rules every one
// of the batches shares: at least one, at most maxBatchIDs, and no claim —
// travel claims are a later delivery's unit of approval and the field exists in
// the contract only so a client naming one is told so rather than ignored.
//
// The cap is counted on the **expenses**, after the duplicates are collapsed,
// because that is what it is for: it bounds the rows LockEntries locks and the
// project roles warmRoles looks up, and an id given twice adds neither. It is
// also the rule the rest of the module states — one id given twice is one
// expense — so counting the array instead would make a body of 501 ids naming
// three expenses a refusal nobody could explain.
func batchIDs(entryIDs []int64, claimIDs *[]int64, errs map[string][]string) ([]int64, map[string][]string) {
	if claimIDs != nil && len(*claimIDs) > 0 {
		errs = withFieldError(errs, "claimIds",
			"Travel claims arrive in a later delivery; they cannot be submitted or approved yet")
	}
	ids := uniqueIDs(entryIDs)
	if len(ids) > maxBatchIDs {
		errs = withFieldError(errs, "entryIds",
			fmt.Sprintf("At most %d expenses may be given at once; %d were given", maxBatchIDs, len(ids)))
	}
	if len(ids) == 0 {
		errs = withFieldError(errs, "entryIds", "At least one expense id is required")
	}
	return ids, errs
}

// notFoundRefusal is what an id nobody may see reads as — byte for byte what an
// id that does not exist reads as, so a refusal tells a stranger nothing.
func notFoundRefusal(id int64) string { return fmt.Sprintf("Expense %d was not found", id) }

// lockedRefusal is the per-id message for an expense inside a closed period.
func (c *caller) lockedRefusal(id int64) string {
	return fmt.Sprintf("Expense %d is dated before %s, the lock date",
		id, lockedBefore(c.Settings).Format(time.DateOnly))
}

// lockedRows is the expenses of one batch, locked and keyed by id; an id with
// no row is simply absent, which every refusal reads as "was not found".
func lockedRows(ctx context.Context, txq *store.Queries, ids []int64) (map[int64]store.ExpensesEntry, error) {
	rows, err := txq.LockEntries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("expenses: lock the expenses: %w", err)
	}
	byID := make(map[int64]store.ExpensesEntry, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	return byID, nil
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

// warmBatch reads everything another module has to answer about one batch,
// before any row is locked: the caller's role on every project the expenses are
// on, and each of those projects as the directory has it — a submit reprices
// what the customer is billed, and only the project says whether it bills at
// all. Without the projects module there is nothing to read and nothing to
// judge (decision X2).
func (s *server) warmBatch(ctx context.Context, c *caller, rows []store.ExpensesEntry) (map[int32]contracts.ProjectEntry, error) {
	projectIDs := make([]*int32, 0, len(rows))
	for _, row := range rows {
		projectIDs = append(projectIDs, row.ProjectID)
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
// Drafts and rejected lines become submitted, all or nothing. Each must be the
// caller's own — or anyone's, for expenses:manage — and not dated before the
// period lock. This is where design §4's freezing happens: each line is priced
// one last time and the result stored.
func (s *server) PostExpensesSubmit(ctx context.Context, req gen.PostExpensesSubmitRequestObject) (gen.PostExpensesSubmitResponseObject, error) {
	body := gen.ExpensesFlowRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	ids, errs := batchIDs(body.EntryIds, body.ClaimIds, nil)
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
	projects, err := s.warmBatch(ctx, c, before)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var refusals []string
	var moved []store.ExpensesEntry
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		byID, err := lockedRows(ctx, txq, ids)
		if err != nil {
			return err
		}
		frozen := make(map[int64]frozenEntry, len(ids))
		for _, id := range ids {
			row, ok := byID[id]
			if !ok {
				refusals = append(refusals, notFoundRefusal(id))
				continue
			}
			if msg := c.submitRefusal(id, row); msg != "" {
				refusals = append(refusals, msg)
				continue
			}
			f, msg, err := s.freeze(ctx, txq, c, row, projects)
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
		if len(refusals) > 0 {
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
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case len(refusals) > 0:
		return gen.PostExpensesSubmit400ApplicationProblemPlusJSONResponse(
			invalidSubmission(map[string][]string{"entryIds": refusals})), nil
	}

	entries, err := s.entryResponses(ctx, c, orderedByIDs(ids, moved))
	if err != nil {
		return nil, err
	}
	return gen.PostExpensesSubmit200JSONResponse(entries), nil
}

// submitRefusal is why the expense id may not be submitted by c, "" when it
// may. It runs inside the locked transaction on the row as it stands there, so
// it reads c's roles only from the cache warmBatch filled.
//
// The checks come in the order that tells the caller least: an expense they may
// not see is the unknown id's "was not found", one they see but do not own says
// only that, and only then are its status and its date judged.
func (c *caller) submitRefusal(id int64, row store.ExpensesEntry) string {
	a := c.accessFor(row, c.cachedRole(row.ProjectID))
	switch {
	case !a.CanSee:
		return notFoundRefusal(id)
	case !a.IsWriter:
		return fmt.Sprintf("Expense %d is not yours", id)
	case !slices.Contains(editableStatuses, row.Status):
		return fmt.Sprintf("Expense %d is %s, and only a draft or a rejected expense can be submitted", id, row.Status)
	case !c.mayWritePast(row.EntryDate.Time):
		return c.lockedRefusal(id)
	}
	return ""
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
	projects map[int32]contracts.ProjectEntry,
) (frozenEntry, string, error) {
	f := frozenEntry{
		Rate: row.Rate, PassengerRate: row.PassengerRate, Gross: row.GrossAmount,
		Billable: row.Billable, Markup: row.MarkupPercent,
		BillRate: row.BillRatePerKm, BillAmount: row.BillAmount,
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

	// orManage is whether expenses:manage may make this move as well as an
	// approver; manageOnly is whether expenses:manage is the *only* one who
	// may, which is what the payroll track is — approving an expense is not
	// paying for it.
	orManage   bool
	manageOnly bool

	// also is the refusals only this move has, judged after the status and
	// before the period lock, on the row as it stands under the lock.
	also func(id int64, row store.ExpensesEntry) string

	apply func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error)
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
	a := c.accessFor(row, c.cachedRole(row.ProjectID))
	mayMove := a.IsApprover || (d.orManage && c.Manage)
	if d.manageOnly {
		mayMove = c.Manage
	}
	switch {
	case !a.CanSee:
		return notFoundRefusal(id)
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

// decide runs d over the request's ids. errs is what the body's own fields
// already failed (reject's reason); it is reported before any expense is
// judged.
func (s *server) decide(ctx context.Context, d decision, body gen.ExpensesFlowRequest,
	errs map[string][]string,
) (flowOutcome, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return flowOutcome{}, err
	}
	// A caller who could approve nothing at all is refused the whole request,
	// as the access layer would, rather than told about each id (decision
	// X10). A manage-only move has no such gate to apply: the router has
	// already required expenses:manage of it.
	if !d.manageOnly {
		approves, err := c.approvesAnything(ctx, s, d.orManage)
		if err != nil {
			return flowOutcome{}, err
		}
		if !approves {
			return flowOutcome{forbidden: true}, nil
		}
	}

	ids, errs := batchIDs(body.EntryIds, body.ClaimIds, errs)
	if len(errs) > 0 {
		return flowOutcome{errs: errs}, nil
	}
	before, err := q.GetEntries(ctx, ids)
	if err != nil {
		return flowOutcome{}, fmt.Errorf("expenses: read the expenses: %w", err)
	}
	projectIDs := make([]*int32, 0, len(before))
	for _, row := range before {
		projectIDs = append(projectIDs, row.ProjectID)
	}
	if err := c.warmRoles(ctx, s, projectIDs); err != nil {
		return flowOutcome{}, err
	}

	var refusals []string
	var moved []store.ExpensesEntry
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		byID, err := lockedRows(ctx, txq, ids)
		if err != nil {
			return err
		}
		for _, id := range ids {
			row, ok := byID[id]
			if !ok {
				refusals = append(refusals, notFoundRefusal(id))
				continue
			}
			if msg := c.decisionRefusal(d, id, row); msg != "" {
				refusals = append(refusals, msg)
			}
		}
		if len(refusals) > 0 {
			return nil
		}
		moved, err = d.apply(ctx, txq, ids)
		if err == nil && len(moved) != len(ids) {
			// Every row is locked and was judged movable; the update's own
			// guard refusing one means the two disagree — a bug, not a race,
			// and never a 200 with a blank expense in it.
			err = fmt.Errorf("expenses: the %s moved %d of %d judged expenses", d.verb, len(moved), len(ids))
		}
		return err
	})
	if err != nil {
		return flowOutcome{}, err
	}
	if len(refusals) > 0 {
		return flowOutcome{errs: map[string][]string{"entryIds": refusals}}, nil
	}

	entries, err := s.entryResponses(ctx, c, orderedByIDs(ids, moved))
	if err != nil {
		return flowOutcome{}, err
	}
	return flowOutcome{entries: entries}, nil
}

// PostExpensesApprove Approve expenses
// (POST /api/v1/expenses/approve)
//
// Submitted expenses, each one the caller approves for — expenses:approve for
// anything, the manager role for a project's own — become approved, recording
// who decided and when. The frozen figures are not touched.
func (s *server) PostExpensesApprove(ctx context.Context, req gen.PostExpensesApproveRequestObject) (gen.PostExpensesApproveResponseObject, error) {
	body := gen.ExpensesFlowRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	decider, now := callerID(ctx), s.deps.Clock()
	out, err := s.decide(ctx, decision{
		from: statusSubmitted, verb: "approved",
		apply: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error) {
			rows, err := txq.ApproveEntries(ctx, store.ApproveEntriesParams{Ids: ids, DecidedBy: decider, Now: now})
			if err != nil {
				return nil, fmt.Errorf("expenses: approve expenses: %w", err)
			}
			return rows, nil
		},
	}, body, nil)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesApprove403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesApprove400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostExpensesApprove200JSONResponse(out.entries), nil
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
// Under exactly the rules of an approval, submitted expenses become rejected
// with a reason their owner sees. A rejected expense is theirs again — to edit,
// to delete, or to submit as it stands, which prices it once more.
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
		from: statusSubmitted, verb: "rejected",
		apply: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error) {
			rows, err := txq.RejectEntries(ctx, store.RejectEntriesParams{
				Ids: ids, DecidedBy: decider, Reason: reason, Now: now,
			})
			if err != nil {
				return nil, fmt.Errorf("expenses: reject expenses: %w", err)
			}
			return rows, nil
		},
	}, gen.ExpensesFlowRequest{EntryIds: body.EntryIds, ClaimIds: body.ClaimIds}, errs)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesReject403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesReject400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostExpensesReject200JSONResponse(out.entries), nil
}

// PostExpensesUnapprove Unapprove expenses
// (POST /api/v1/expenses/unapprove)
//
// Approved expenses become fresh drafts — by whoever could have approved them,
// or by expenses:manage — with the decision and the submission stamp cleared,
// so the owner sees something to change and send again. Never one that has been
// reimbursed or invoiced: the reimbursement track and the invoicing track each
// have an undo of their own.
func (s *server) PostExpensesUnapprove(ctx context.Context, req gen.PostExpensesUnapproveRequestObject) (gen.PostExpensesUnapproveResponseObject, error) {
	body := gen.ExpensesFlowRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	now := s.deps.Clock()
	out, err := s.decide(ctx, decision{
		from: statusApproved, verb: "unapproved", orManage: true, also: paidOutRefusal,
		apply: func(ctx context.Context, txq *store.Queries, ids []int64) ([]store.ExpensesEntry, error) {
			rows, err := txq.UnapproveEntries(ctx, store.UnapproveEntriesParams{Ids: ids, Now: now})
			if err != nil {
				return nil, fmt.Errorf("expenses: unapprove expenses: %w", err)
			}
			return rows, nil
		},
	}, body, nil)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostExpensesUnapprove403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostExpensesUnapprove400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostExpensesUnapprove200JSONResponse(out.entries), nil
}
