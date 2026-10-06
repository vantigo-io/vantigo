package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the exception queue (invoices payments and reminders design
// D5): the bank lines matching did not match, listed with what is applied
// from them, their suggestions and their events, and the seven things a
// person does about one — apply it to invoices and their charges, dismiss
// it, handle a reversal, confirm a duplicate or keep it as distinct, reopen
// it. Each action judges its body and the line on the pool, then runs one
// READ COMMITTED transaction whose first lock is the line, FOR NO KEY UPDATE
// (lockBankTransaction), its state judged again under it; the apply and the
// reversal then lock their invoices FOR UPDATE in descending id (D18), the
// others nothing more. Every action writes the line's event, by the caller
// at the request's one clock read. Nothing here reads a directory or calls
// out of the module.

// The codes and titles of the queue's refusals.
const (
	codeBankTransactionNotOpen       = "bank_transaction_not_open"
	codeBankTransactionNotApplicable = "bank_transaction_not_applicable"
	codeBankTransactionApplied       = "bank_transaction_applied"
	codeReversalPaymentRequired      = "reversal_payment_required"
	codeAllocationNotAnInvoice       = "allocation_not_an_invoice"
	codeAllocationExceedsTransaction = "allocation_exceeds_transaction"
	codePaidBeforeIssue              = "paid_before_issue"
	codeBankTransactionReversed      = "bank_transaction_reversed"

	cannotApplyTitle          = "The bank line cannot be applied"
	cannotDismissTitle        = "The bank line cannot be dismissed"
	cannotHandleReversalTitle = "The reversal cannot be handled"
	cannotConfirmTitle        = "The duplicate cannot be confirmed"
	cannotKeepTitle           = "The duplicate cannot be treated as distinct"
	cannotReopenTitle         = "The bank line cannot be reopened"
	invalidApplyTitle         = "Invalid allocation"
	invalidDismissTitle       = "Invalid dismissal"
	invalidReversalTitle      = "Invalid reversal"
	invalidConfirmTitle       = "Invalid confirmation"
)

// A line's statuses (ck_bank_transactions_status).
const (
	lineMatched   = "matched"
	lineException = "exception"
	lineResolved  = "resolved"
	lineDuplicate = "duplicate"
)

// The resolutions of a resolved line (ck_bank_transactions_resolution).
const (
	resolutionApplied            = "applied"
	resolutionNotCustomerPayment = "not_customer_payment"
	resolutionReversalHandled    = "reversal_handled"
	resolutionDuplicateConfirmed = "duplicate_confirmed"
)

// The queue's events (ck_bank_transaction_events_event), beside matching's
// matched and queued.
const (
	eventApplied            = "applied"
	eventDismissed          = "dismissed"
	eventReversalHandled    = "reversal_handled"
	eventReopened           = "reopened"
	eventDuplicateConfirmed = "duplicate_confirmed"
	eventTreatedAsDistinct  = "treated_as_distinct"
	// eventReversed is written on the line whose payment a reversal took
	// back: its money is never applied again.
	eventReversed = "reversed"
)

// reasonPaymentRemoved is a matched line's reason once it is reopened with
// its payments all removed (D5).
const reasonPaymentRemoved = "payment_removed"

// The suggestions' reasons (InvoicesBankTransactionSuggestion.why), in the
// order a suggestion is named by.
const (
	whyNumberInText     = "number_in_text"
	whyAmountEqualsOpen = "amount_equals_open"
	whyDebtorAccount    = "debtor_account"
)

// The bounds of the queue's bodies.
const (
	maxAllocations      = 20
	maxReversalPayments = 20
	maxQueueNote        = 500
)

// digitRun is a whole word of digits in a line's text, which may be an
// invoice's number (D5's number_in_text); 18 digits always fit an int64.
var digitRun = regexp.MustCompile(`\b\d{1,18}\b`)

// queueAfterLineLock, when a test sets it (export_test.go's
// SetQueueAfterLineLock), runs inside every queue action's transaction right
// after the line is locked, before anything else, with the line's id; an
// error rolls the action back. Nil in production.
var queueAfterLineLock func(ctx context.Context, bankTransactionID int64) error

// errNoSuchLine is an action on a line that does not exist.
var errNoSuchLine = errors.New("invoices: no such bank line")

// suggestible reports whether a line queued for reason, with no invoice its
// KID named, is given suggestions (D5): no KID, a KID that is not valid or
// names nothing, a matched line whose payments were removed, and a possible
// duplicate whose KID named no invoice.
func suggestible(reason string) bool {
	switch reason {
	case reasonNoKid, reasonKidInvalid, reasonKidUnknown, reasonPaymentRemoved, reasonPossibleDuplicate:
		return true
	}
	return false
}

// suggestionsFor is the issued invoices line may pay (D5), read on the pool
// and never registered by itself: those whose number is a whole word of
// its text (number_in_text), whose open amount equals its amount
// (amount_equals_open), and the open ones of the customers whose earlier
// payments from a bank line — one booked on or before it — came from its
// debtor account (debtor_account) —
// each invoice once, under the first reason in that order. The id answered
// is the one suggestion when there is exactly one, else nil: what a line
// queued keeps as its suggested invoice (plan reading 24).
func suggestionsFor(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction) ([]gen.InvoicesBankTransactionSuggestion, *int64, error) {
	type found struct {
		id, number int64
		customer   int32
		buyer      *string
		open       pgtype.Numeric
	}
	var all []found
	var whys []string
	add := func(why string, rows []found) {
		for _, r := range rows {
			all, whys = append(all, r), append(whys, why)
		}
	}
	var numbers []int64
	for _, tok := range digitRun.FindAllString(line.RemittanceText, 50) {
		if n, err := strconv.ParseInt(tok, 10, 64); err == nil && n > 0 && !slices.Contains(numbers, n) {
			numbers = append(numbers, n)
		}
	}
	if len(numbers) > 0 {
		rows, err := q.InvoicesByNumbers(ctx, numbers)
		if err != nil {
			return nil, nil, fmt.Errorf("invoices: suggest by the numbers of line %d's text: %w", line.ID, err)
		}
		fs := make([]found, 0, len(rows))
		for _, r := range rows {
			fs = append(fs, found{r.ID, r.Number, r.CustomerID, r.BuyerName, r.OpenAmount})
		}
		add(whyNumberInText, fs)
	}
	rows, err := q.InvoicesOpenEqual(ctx, line.Amount)
	if err != nil {
		return nil, nil, fmt.Errorf("invoices: suggest by line %d's amount: %w", line.ID, err)
	}
	fs := make([]found, 0, len(rows))
	for _, r := range rows {
		fs = append(fs, found{r.ID, r.Number, r.CustomerID, r.BuyerName, r.OpenAmount})
	}
	add(whyAmountEqualsOpen, fs)
	if line.DebtorAccount != "" {
		rows, err := q.InvoicesOfDebtorAccount(ctx, store.InvoicesOfDebtorAccountParams{
			DebtorAccount: line.DebtorAccount, LineID: line.ID, BookedOn: line.BookedOn,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("invoices: suggest by line %d's debtor account: %w", line.ID, err)
		}
		fs := make([]found, 0, len(rows))
		for _, r := range rows {
			fs = append(fs, found{r.ID, r.Number, r.CustomerID, r.BuyerName, r.OpenAmount})
		}
		add(whyDebtorAccount, fs)
	}
	out := []gen.InvoicesBankTransactionSuggestion{}
	seen := map[int64]bool{}
	for i, f := range all {
		if seen[f.id] {
			continue
		}
		seen[f.id] = true
		open, err := floatFromNumeric(f.open)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, gen.InvoicesBankTransactionSuggestion{
			InvoiceId: f.id, Number: f.number, CustomerId: f.customer, BuyerName: deref(f.buyer), OpenAmount: open,
			Why: gen.InvoicesBankTransactionSuggestionWhy(whys[i]),
		})
	}
	if len(out) == 1 {
		return out, &out[0].InvoiceId, nil
	}
	return out, nil, nil
}

// suggestionOnQueue is the suggested invoice a line queued for reason keeps
// (plan reading 24): the one it has; else the invoice its KID names; else,
// for a reason given suggestions, the one unambiguous suggestion; else none.
// Read on the pool before the action's transaction: it is a hint.
func suggestionOnQueue(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction, reason string) (*int64, error) {
	if line.SuggestedInvoiceID != nil {
		return line.SuggestedInvoiceID, nil
	}
	if line.Kid != nil {
		inv, _, err := invoiceOfKid(ctx, q, line)
		if err != nil {
			return nil, err
		}
		if inv != nil {
			return &inv.ID, nil
		}
	}
	if !suggestible(reason) {
		return nil, nil
	}
	_, one, err := suggestionsFor(ctx, q, line)
	return one, err
}

// appliedWire is one payment or charge payment of a line on the wire.
func appliedWire(r store.AppliedToTransactionsRow) (gen.InvoicesBankTransactionApplied, error) {
	amount, err := floatFromNumeric(r.Amount)
	if err != nil {
		return gen.InvoicesBankTransactionApplied{}, err
	}
	return gen.InvoicesBankTransactionApplied{
		Kind: gen.InvoicesBankTransactionAppliedKind(r.Kind), Id: r.ID, InvoiceId: r.InvoiceID, Number: r.Number,
		Amount: amount, Removed: r.Removed,
	}, nil
}

// owedBack reports whether a line dismissed after it was queued for reason
// is money owed back to the payer — a credited or settled invoice, or more
// than was open — and so stays unapplied (the coordinator's decision at Task
// 9's review): a refund is made outside Vantigo.
func owedBack(reason *string) bool {
	if reason == nil {
		return false
	}
	switch *reason {
	case reasonInvoiceCredited, reasonInvoiceSettled, reasonExceedsOpen:
		return true
	}
	return false
}

// unappliedOf is what of line its live payments and charge payments leave
// (D5): its amount less them, and 0 for what is not money waiting to be
// applied — a reversal, a negative line, a duplicate row, a line resolved
// otherwise than applied unless it was dismissed as money owed back, and a
// line whose payment a reversal took back (reversed) — ListBankTransactions'
// unapplied filter, word for word.
func unappliedOf(line store.InvoicesBankTransaction, applied []store.AppliedToTransactionsRow, reversed bool) (*big.Rat, error) {
	switch {
	case line.Direction != "credit", line.Negative, line.Status == lineDuplicate, reversed,
		line.Resolution != nil && *line.Resolution != resolutionApplied &&
			(*line.Resolution != resolutionNotCustomerPayment || !owedBack(line.Reason)):
		return new(big.Rat), nil
	}
	rest, err := ratFromNumeric(line.Amount)
	if err != nil {
		return nil, err
	}
	for _, a := range applied {
		if a.Removed {
			continue
		}
		amount, err := ratFromNumeric(a.Amount)
		if err != nil {
			return nil, err
		}
		rest.Sub(rest, amount)
	}
	return rest, nil
}

// bankTransactionsResponse answers lines on the wire with the queue's fields
// (D5): each with its file, what refers to it, its unapplied rest, the line a
// possible duplicate or duplicate may repeat, and its events — in a handful
// of statements, whatever the count. withSuggestions adds the suggestions of
// every exception they are for, a few statements a line, so only the queue's
// list and its actions ask for them; a file's detail does not.
func bankTransactionsResponse(ctx context.Context, q *store.Queries, lines []store.InvoicesBankTransaction, withSuggestions bool) ([]gen.InvoicesBankTransaction, error) {
	out := make([]gen.InvoicesBankTransaction, 0, len(lines))
	if len(lines) == 0 {
		return out, nil
	}
	ids, fileIDs, twinOf := make([]int64, 0, len(lines)), []int64{}, []int64{}
	for _, l := range lines {
		ids = append(ids, l.ID)
		if !slices.Contains(fileIDs, l.BankFileID) {
			fileIDs = append(fileIDs, l.BankFileID)
		}
		if l.Status == lineDuplicate || (l.Reason != nil && *l.Reason == reasonPossibleDuplicate) {
			twinOf = append(twinOf, l.ID)
		}
	}
	files, err := q.BankFileRefs(ctx, fileIDs)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the lines' bank files: %w", err)
	}
	fileByID := make(map[int64]gen.InvoicesBankTransactionFile, len(files))
	for _, f := range files {
		fileByID[f.ID] = gen.InvoicesBankTransactionFile{Id: f.ID, Format: f.Format, UploadedAt: f.UploadedAt}
	}
	twins := map[int64]store.InvoicesBankTransaction{}
	appliedIDs := slices.Clone(ids)
	if len(twinOf) > 0 {
		rows, err := q.TwinsOf(ctx, twinOf)
		if err != nil {
			return nil, fmt.Errorf("invoices: read the lines possible duplicates repeat: %w", err)
		}
		for _, r := range rows {
			twins[r.LineID] = r.InvoicesBankTransaction
			appliedIDs = append(appliedIDs, r.InvoicesBankTransaction.ID)
		}
	}
	appliedRows, err := q.AppliedToTransactions(ctx, appliedIDs)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what is applied from the lines: %w", err)
	}
	applied := map[int64][]store.AppliedToTransactionsRow{}
	for _, r := range appliedRows {
		applied[r.BankTransactionID] = append(applied[r.BankTransactionID], r)
	}
	eventRows, err := q.EventsOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the lines' events: %w", err)
	}
	events := map[int64][]gen.InvoicesBankTransactionEvent{}
	for _, e := range eventRows {
		events[e.BankTransactionID] = append(events[e.BankTransactionID], gen.InvoicesBankTransactionEvent{
			Id: e.ID, Event: gen.InvoicesBankTransactionEventEvent(e.Event), Reason: reasonWire(e.Reason),
			Note: e.Note, By: e.ByUserID, At: e.At,
		})
	}
	appliedWires := func(id int64) ([]gen.InvoicesBankTransactionApplied, error) {
		ws := []gen.InvoicesBankTransactionApplied{}
		for _, r := range applied[id] {
			w, err := appliedWire(r)
			if err != nil {
				return nil, err
			}
			ws = append(ws, w)
		}
		return ws, nil
	}
	for _, l := range lines {
		tx, err := bankTransactionResponse(l)
		if err != nil {
			return nil, err
		}
		tx.BankFile = fileByID[l.BankFileID]
		if tx.Applied, err = appliedWires(l.ID); err != nil {
			return nil, err
		}
		reversed := slices.ContainsFunc(events[l.ID], func(e gen.InvoicesBankTransactionEvent) bool { return e.Event == eventReversed })
		rest, err := unappliedOf(l, applied[l.ID], reversed)
		if err != nil {
			return nil, err
		}
		tx.UnappliedAmount = floatFromRat(rest, 2)
		if ev := events[l.ID]; ev != nil {
			tx.Events = ev
		}
		if twin, ok := twins[l.ID]; ok {
			amount, err := floatFromNumeric(twin.Amount)
			if err != nil {
				return nil, err
			}
			ws, err := appliedWires(twin.ID)
			if err != nil {
				return nil, err
			}
			tx.PossibleDuplicateOf = &gen.InvoicesBankTransactionTwin{
				Id: twin.ID, BankFileId: twin.BankFileID, LineRef: twin.LineRef, BookedOn: wireDate(twin.BookedOn.Time),
				Amount: amount, Kid: twin.Kid, Status: twin.Status, Applied: ws,
			}
		}
		if withSuggestions && l.Status == lineException && l.Reason != nil && suggestible(*l.Reason) &&
			(*l.Reason != reasonPossibleDuplicate || l.SuggestedInvoiceID == nil) {
			sugg, _, err := suggestionsFor(ctx, q, l)
			if err != nil {
				return nil, err
			}
			tx.Suggestions = &sugg
		}
		out = append(out, tx)
	}
	return out, nil
}

// reasonWire is a stored reason on the wire, nil for none.
func reasonWire(r *string) *gen.InvoicesBankTransactionReason {
	if r == nil {
		return nil
	}
	return ptr(gen.InvoicesBankTransactionReason(*r))
}

// oneLineResponse is line id as it now stands, with its suggestions — every
// action's answer.
func oneLineResponse(ctx context.Context, q *store.Queries, id int64) (gen.InvoicesBankTransaction, error) {
	line, err := q.GetBankTransaction(ctx, id)
	if err != nil {
		return gen.InvoicesBankTransaction{}, fmt.Errorf("invoices: read bank line %d: %w", id, err)
	}
	txs, err := bankTransactionsResponse(ctx, q, []store.InvoicesBankTransaction{line}, true)
	if err != nil {
		return gen.InvoicesBankTransaction{}, err
	}
	return txs[0], nil
}

// GetInvoicesBankTransactions List the bank lines and the exception queue
// (GET /api/v1/invoices/bank-transactions)
func (s *server) GetInvoicesBankTransactions(ctx context.Context, req gen.GetInvoicesBankTransactionsRequestObject) (gen.GetInvoicesBankTransactionsResponseObject, error) {
	p := req.Params
	errs := validatePageParams(p.Page, p.PageSize)
	if p.Status != nil && !p.Status.Valid() {
		errs = append(errs, "status is pending, matched, exception, resolved or duplicate.")
	}
	if p.Reason != nil && !p.Reason.Valid() {
		errs = append(errs, "reason is not one a line is queued for.")
	}
	if p.From != nil && p.To != nil && p.From.After(p.To.Time) {
		errs = append(errs, "from is on or before to.")
	}
	if len(errs) > 0 {
		return gen.GetInvoicesBankTransactions400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	filter := store.ListBankTransactionsParams{
		BankFileID: p.BankFileId, Unapplied: p.Unapplied != nil && *p.Unapplied,
		PageOffset: (page - 1) * pageSize, PageSize: pageSize,
	}
	if p.Status != nil {
		filter.Status = ptr(string(*p.Status))
	}
	if p.Reason != nil {
		filter.Reason = ptr(string(*p.Reason))
	}
	if p.From != nil {
		filter.FromOn = pgDate(p.From.Time)
	}
	if p.To != nil {
		filter.ToOn = pgDate(p.To.Time)
	}
	q := store.New(s.deps.Pool)
	total, err := q.CountBankTransactions(ctx, store.CountBankTransactionsParams{
		Status: filter.Status, Reason: filter.Reason, BankFileID: filter.BankFileID, FromOn: filter.FromOn, ToOn: filter.ToOn,
		Unapplied: filter.Unapplied,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: count the bank lines: %w", err)
	}
	rows, err := q.ListBankTransactions(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the bank lines: %w", err)
	}
	lines := make([]store.InvoicesBankTransaction, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, r.InvoicesBankTransaction)
	}
	data, err := bankTransactionsResponse(ctx, q, lines, true)
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesBankTransactions200JSONResponse(gen.PaginatedResponseOfInvoicesBankTransaction{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, total),
	}), nil
}

// lineJudge decides whether an action takes a line as it stands: a refusal,
// or nil. Each action's judge runs on the pool, then again under the line's
// lock.
type lineJudge func(line store.InvoicesBankTransaction) *gen.InvoicesConflictProblem

// queueAction is the shape of every action (D5): the line read on the pool —
// errNoSuchLine when it does not exist — and judged; then one transaction,
// the line locked FOR NO KEY UPDATE first, the seam, the line judged again,
// then act, which locks what else it needs and writes. A refusal, from the
// judge or from act, rolls the transaction back and is answered; an error
// is the server's.
func (s *server) queueAction(ctx context.Context, id int64, judge lineJudge,
	act func(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error),
) (*gen.InvoicesConflictProblem, error) {
	line, err := store.New(s.deps.Pool).GetBankTransaction(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoSuchLine
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read bank line %d: %w", id, err)
	}
	if refusal := judge(line); refusal != nil {
		return refusal, nil
	}
	var refusal *gen.InvoicesConflictProblem
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockBankTransaction(ctx, txq, id)
		if err != nil {
			return fmt.Errorf("invoices: lock bank line %d: %w", id, err)
		}
		if hook := queueAfterLineLock; hook != nil {
			if err := hook(ctx, id); err != nil {
				return err
			}
		}
		if refusal = judge(locked); refusal != nil {
			return errRefused
		}
		if refusal, err = act(ctx, txq, locked); err != nil {
			return err
		}
		if refusal != nil {
			return errRefused
		}
		return nil
	})
	if refusal != nil {
		return refusal, nil
	}
	return nil, err
}

// lineEvent records what an action did to line, by and at.
func lineEvent(ctx context.Context, txq *store.Queries, lineID int64, event string, reason *string, note string, by uuid.UUID, at time.Time) error {
	if err := txq.InsertBankTransactionEvent(ctx, store.InsertBankTransactionEventParams{
		BankTransactionID: lineID, Event: event, Reason: reason, Note: note, ByUserID: by, At: at,
	}); err != nil {
		return fmt.Errorf("invoices: record line %d %s: %w", lineID, event, err)
	}
	return nil
}

// resolveLine resolves line, which the caller holds, as resolution with
// reason, by and at, the note kept, from the statuses the action takes, and
// records event.
func resolveLine(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction, resolution, reason, event, note string,
	from []string, by uuid.UUID, at time.Time,
) error {
	n, err := txq.ResolveTransaction(ctx, store.ResolveTransactionParams{
		Resolution: resolution, Reason: reason, ResolvedByUserID: by, ResolvedAt: at, ResolutionNote: note,
		ID: line.ID, FromStatuses: from,
	})
	if err != nil {
		return fmt.Errorf("invoices: resolve line %d %s: %w", line.ID, resolution, err)
	}
	if n != 1 {
		return fmt.Errorf("invoices: resolve line %d %s: %d rows, want 1", line.ID, resolution, n)
	}
	return lineEvent(ctx, txq, line.ID, event, &reason, note, by, at)
}

// notOpen is bank_transaction_not_open for title.
func notOpen(title string) *gen.InvoicesConflictProblem {
	return ptr(conflict(codeBankTransactionNotOpen, title,
		"This bank line is not in the queue as this action needs it: it was matched, resolved or kept as a duplicate, or someone dealt with it meanwhile. Reopen it first if it must be dealt with again."))
}

// notApplicable is bank_transaction_not_applicable for title, detail saying
// why.
func notApplicable(title, detail string) *gen.InvoicesConflictProblem {
	return ptr(conflict(codeBankTransactionNotApplicable, title, detail))
}

// queueNote is an optional note of at most 500 characters, trimmed, with
// its message on note.
func queueNote(v *string) (string, map[string][]string) {
	note := optionalText(v)
	if msg := maxLength("The note", note, maxQueueNote); msg != "" {
		return note, fieldError("note", msg)
	}
	return note, nil
}

// lineRefusal answers the 404 or the refusal of an action; ok is false when
// it answered neither and the action went through.
func lineRefusal[T any](refusal *gen.InvoicesConflictProblem, err error, notFound func() T, conflicted func(gen.InvoicesConflictProblem) T) (T, bool, error) {
	var zeroT T
	switch {
	case refusal != nil:
		return conflicted(*refusal), true, nil
	case errors.Is(err, errNoSuchLine), errors.Is(err, errNoSuchPayment):
		return notFound(), true, nil
	case err != nil:
		return zeroT, true, err
	}
	return zeroT, false, nil
}

// allocation is one part of an apply, parsed: an invoice, the principal and
// the charges it pays, each exact and as numeric(14,2).
type allocation struct {
	invoiceID         int64
	amount, charges   *big.Rat
	amountN, chargesN pgtype.Numeric
}

// parseApply runs D5's field rules over an apply's body, every failure
// collected: 1 to 20 allocations, each invoice once, amount and
// chargesAmount 0 or more with two decimals within the document bound and
// their sum above 0, the note at most 500 characters.
func parseApply(body gen.InvoicesBankTransactionApplyRequest) ([]allocation, string, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	note, noteErrs := queueNote(body.Note)
	for f, ms := range noteErrs {
		for _, m := range ms {
			add(f, m)
		}
	}
	switch n := len(body.Allocations); {
	case n == 0:
		add("allocations", "An apply needs at least one allocation")
	case n > maxAllocations:
		add("allocations", fmt.Sprintf("An apply has at most %d allocations", maxAllocations))
	}
	allocs := make([]allocation, 0, len(body.Allocations))
	seen := map[int64]bool{}
	for i, a := range body.Allocations {
		field := func(name string) string { return fmt.Sprintf("allocations[%d].%s", i, name) }
		if seen[a.InvoiceId] {
			add(field("invoiceId"), "Each invoice is allocated once")
		}
		seen[a.InvoiceId] = true
		amt, msg := amount("An amount", a.Amount, 2, zero, false, maxGrossTotal)
		add(field("amount"), msg)
		charges, cmsg := new(big.Rat), ""
		if a.ChargesAmount != nil {
			charges, cmsg = amount("A charges amount", *a.ChargesAmount, 2, zero, false, maxGrossTotal)
			add(field("chargesAmount"), cmsg)
		}
		if amt == nil || charges == nil {
			continue
		}
		if amt.Sign() == 0 && charges.Sign() == 0 {
			add(field("amount"), "An allocation pays something: amount and chargesAmount are not both 0")
			continue
		}
		// amount() has held both to two decimals within the bound, so these
		// cannot fail on the caller's input: a failure is the server's.
		amountN, err := numericFromRat(amt, 2)
		if err != nil {
			return nil, "", nil, fmt.Errorf("invoices: an allocation's amount as numeric: %w", err)
		}
		chargesN, err := numericFromRat(charges, 2)
		if err != nil {
			return nil, "", nil, fmt.Errorf("invoices: an allocation's charges as numeric: %w", err)
		}
		allocs = append(allocs, allocation{invoiceID: a.InvoiceId, amount: amt, charges: charges, amountN: amountN, chargesN: chargesN})
	}
	return allocs, note, errs, nil
}

// judgeApplicable is the apply's judgement of the line: an exception, and
// not a reversal or a negative line, which are never a payment.
func judgeApplicable(line store.InvoicesBankTransaction) *gen.InvoicesConflictProblem {
	switch {
	case line.Status != lineException:
		return notOpen(cannotApplyTitle)
	case line.Reason != nil && *line.Reason == reasonReversal:
		return notApplicable(cannotApplyTitle, "A reversal takes money back; it is handled, never applied to an invoice.")
	case line.Reason != nil && *line.Reason == reasonNegativeAmount, line.Negative:
		return notApplicable(cannotApplyTitle, "A negative line is not a payment; dismiss it with a note.")
	}
	return nil
}

// applyLocked applies line, which the caller holds FOR NO KEY UPDATE, to
// allocs (D5): the invoices FOR UPDATE in descending id, then per invoice,
// in that order, after its lock — allocation_not_an_invoice,
// payment_exceeds_open, charge_payment_exceeds_outstanding,
// paid_before_issue — and allocation_exceeds_transaction over the sum
// against what is left of the line. A refusal is answered before anything is
// written, so the caller's rollback leaves nothing. Then each allocation
// registers its payment and its charge payment as a match does, by caller at
// now, with the deadline-met waiver; the line resolved, applied, note kept,
// with its event.
func (s *server) applyLocked(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction, allocs []allocation, note string,
	caller uuid.UUID, now time.Time,
) (*gen.InvoicesConflictProblem, error) {
	ids := make([]int64, 0, len(allocs))
	for _, a := range allocs {
		ids = append(ids, a.invoiceID)
	}
	invs, err := lockInvoicesDescending(ctx, txq, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]store.InvoicesInvoice, len(invs))
	for _, inv := range invs {
		byID[inv.ID] = inv
	}
	ordered := slices.Clone(allocs)
	slices.SortFunc(ordered, func(a, b allocation) int { return compareInt64(b.invoiceID, a.invoiceID) })
	total := new(big.Rat)
	for _, a := range ordered {
		inv, ok := byID[a.invoiceID]
		if !ok || inv.Kind != kindInvoice || inv.Status != statusIssued {
			return ptr(conflict(codeAllocationNotAnInvoice, cannotApplyTitle, fmt.Sprintf(
				"Document %d is not an issued invoice: a bank line is applied to issued invoices only.", a.invoiceID))), nil
		}
		if a.amount.Sign() > 0 {
			open, err := openOf(ctx, txq, inv)
			if err != nil {
				return nil, err
			}
			if a.amount.Cmp(open) > 0 {
				p := conflict(codePaymentExceedsOpen, cannotApplyTitle, fmt.Sprintf(
					"The allocation to invoice %s is %s, and it has %s open. An overpayment is not registered; apply the rest elsewhere or leave it unapplied.",
					numberText(inv), a.amount.FloatString(2), open.FloatString(2)))
				p.InvoiceId, p.OpenAmount = ptr(inv.ID), ptr(floatFromRat(open, 2))
				return &p, nil
			}
		}
		if a.charges.Sign() > 0 {
			state, _, err := chargesOf(ctx, txq, inv.ID)
			if err != nil {
				return nil, err
			}
			if a.charges.Cmp(state.Outstanding) > 0 {
				p := conflict(codeChargePaymentExceedsOutstanding, cannotApplyTitle, fmt.Sprintf(
					"The allocation to invoice %s's charges is %s, and %s of them is outstanding. An overpayment is not registered.",
					numberText(inv), a.charges.FloatString(2), state.Outstanding.FloatString(2)))
				p.ChargesOutstanding = ptr(floatFromRat(state.Outstanding, 2))
				return &p, nil
			}
		}
		if line.BookedOn.Time.Before(inv.IssueDate.Time) {
			return ptr(conflict(codePaidBeforeIssue, cannotApplyTitle, fmt.Sprintf(
				"The bank booked this line on %s, before invoice %s was issued on %s: it cannot be a payment of it.",
				line.BookedOn.Time.Format(time.DateOnly), numberText(inv), inv.IssueDate.Time.Format(time.DateOnly)))), nil
		}
		total.Add(total, a.amount).Add(total, a.charges)
	}
	live, err := txq.LiveAppliedTo(ctx, line.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what is applied from line %d: %w", line.ID, err)
	}
	left, err := ratFromNumeric(line.Amount)
	if err != nil {
		return nil, err
	}
	applied, err := ratFromNumeric(live.Applied)
	if err != nil {
		return nil, err
	}
	left.Sub(left, applied)
	if total.Cmp(left) > 0 {
		return ptr(conflict(codeAllocationExceedsTransaction, cannotApplyTitle, fmt.Sprintf(
			"The allocations add up to %s, and %s of this bank line is left to apply.", total.FloatString(2), left.FloatString(2)))), nil
	}
	reference := ""
	if line.Kid != nil {
		reference = *line.Kid
	}
	for _, a := range ordered {
		inv := byID[a.invoiceID]
		// A bank line is NOK (ck_bank_transactions_currency); an invoice in
		// another currency cannot be paid by it, and no refusal says so yet:
		// fail closed, as a match does.
		if inv.Currency != "NOK" {
			return nil, fmt.Errorf("invoices: line %d is in NOK and invoice %d is in %s", line.ID, inv.ID, inv.Currency)
		}
		if a.amount.Sign() > 0 {
			if _, err := txq.InsertImportedPayment(ctx, store.InsertImportedPaymentParams{
				InvoiceID: inv.ID, PaidOn: line.BookedOn, Amount: a.amountN, Currency: inv.Currency, Source: line.Format,
				BankTransactionID: line.ID, Reference: reference, RegisteredByUserID: caller, RegisteredAt: now,
			}); err != nil {
				return nil, fmt.Errorf("invoices: apply line %d's payment to %d: %w", line.ID, inv.ID, err)
			}
		}
		if a.charges.Sign() > 0 {
			if _, err := insertChargePayment(ctx, txq, store.InsertChargePaymentParams{
				InvoiceID: inv.ID, PaidOn: line.BookedOn, Amount: a.chargesN, Currency: inv.Currency, Source: line.Format,
				BankTransactionID: &line.ID, Reference: reference, RegisteredByUserID: caller, RegisteredAt: now,
			}); err != nil {
				return nil, err
			}
		}
		in, err := s.ruleInputLocked(ctx, txq, inv, businessDay(now), 0)
		if err != nil {
			return nil, err
		}
		if err := waiveDeadlineMet(ctx, txq, inv.ID, in, line, caller, now); err != nil {
			return nil, err
		}
	}
	return nil, resolveLine(ctx, txq, line, resolutionApplied, deref(line.Reason), eventApplied, note, []string{lineException}, caller, now)
}

// compareInt64 orders a before b by value.
func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// numberText is an issued document's number as a person reads it, its id
// without one.
func numberText(inv store.InvoicesInvoice) string {
	if inv.Number != nil {
		return strconv.FormatInt(*inv.Number, 10)
	}
	return "id " + strconv.FormatInt(inv.ID, 10)
}

// PostInvoicesBankTransactionsByIdApply Apply a bank line to invoices
// (POST /api/v1/invoices/bank-transactions/{id}/apply)
func (s *server) PostInvoicesBankTransactionsByIdApply(ctx context.Context, req gen.PostInvoicesBankTransactionsByIdApplyRequestObject) (gen.PostInvoicesBankTransactionsByIdApplyResponseObject, error) {
	allocs, note, errs, err := parseApply(*req.Body)
	if err != nil {
		return nil, err
	}
	if errs != nil {
		return gen.PostInvoicesBankTransactionsByIdApply400ApplicationProblemPlusJSONResponse(invalid(invalidApplyTitle, errs)), nil
	}
	caller, now := callerID(ctx), s.deps.Clock()
	refusal, err := s.queueAction(ctx, req.Id, judgeApplicable, func(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error) {
		return s.applyLocked(ctx, txq, line, allocs, note, caller, now)
	})
	if resp, done, err := lineRefusal(refusal, err,
		func() gen.PostInvoicesBankTransactionsByIdApplyResponseObject {
			return gen.PostInvoicesBankTransactionsByIdApply404Response{}
		},
		func(p gen.InvoicesConflictProblem) gen.PostInvoicesBankTransactionsByIdApplyResponseObject {
			return gen.PostInvoicesBankTransactionsByIdApply409ApplicationProblemPlusJSONResponse(p)
		}); done {
		return resp, err
	}
	tx, err := oneLineResponse(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankTransactionsByIdApply200JSONResponse(tx), nil
}

// PostInvoicesBankTransactionsByIdDismiss Dismiss a bank line as not a
// customer payment (POST /api/v1/invoices/bank-transactions/{id}/dismiss)
func (s *server) PostInvoicesBankTransactionsByIdDismiss(ctx context.Context, req gen.PostInvoicesBankTransactionsByIdDismissRequestObject) (gen.PostInvoicesBankTransactionsByIdDismissResponseObject, error) {
	note := strings.TrimSpace(req.Body.Note)
	if note == "" {
		return gen.PostInvoicesBankTransactionsByIdDismiss400ApplicationProblemPlusJSONResponse(invalid(invalidDismissTitle,
			fieldError("note", "A dismissal needs a note saying why"))), nil
	}
	if msg := maxLength("The note", note, maxQueueNote); msg != "" {
		return gen.PostInvoicesBankTransactionsByIdDismiss400ApplicationProblemPlusJSONResponse(invalid(invalidDismissTitle, fieldError("note", msg))), nil
	}
	judge := func(line store.InvoicesBankTransaction) *gen.InvoicesConflictProblem {
		switch {
		case line.Status != lineException:
			return notOpen(cannotDismissTitle)
		case line.Reason != nil && *line.Reason == reasonReversal:
			return notApplicable(cannotDismissTitle, "A reversal takes money back: handle it, removing the payment it reverses or saying why none is.")
		}
		return nil
	}
	caller, now := callerID(ctx), s.deps.Clock()
	refusal, err := s.queueAction(ctx, req.Id, judge, func(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error) {
		return nil, resolveLine(ctx, txq, line, resolutionNotCustomerPayment, deref(line.Reason), eventDismissed, note, []string{lineException}, caller, now)
	})
	if resp, done, err := lineRefusal(refusal, err,
		func() gen.PostInvoicesBankTransactionsByIdDismissResponseObject {
			return gen.PostInvoicesBankTransactionsByIdDismiss404Response{}
		},
		func(p gen.InvoicesConflictProblem) gen.PostInvoicesBankTransactionsByIdDismissResponseObject {
			return gen.PostInvoicesBankTransactionsByIdDismiss409ApplicationProblemPlusJSONResponse(p)
		}); done {
		return resp, err
	}
	tx, err := oneLineResponse(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankTransactionsByIdDismiss200JSONResponse(tx), nil
}

// PostInvoicesBankTransactionsByIdHandleReversal Handle a reversed bank line
// (POST /api/v1/invoices/bank-transactions/{id}/handle-reversal)
func (s *server) PostInvoicesBankTransactionsByIdHandleReversal(ctx context.Context, req gen.PostInvoicesBankTransactionsByIdHandleReversalRequestObject) (gen.PostInvoicesBankTransactionsByIdHandleReversalResponseObject, error) {
	note, errs := queueNote(req.Body.Note)
	var named []gen.InvoicesBankTransactionReversalPayment
	if req.Body.RemovePayments != nil {
		named = *req.Body.RemovePayments
	}
	noPayment := req.Body.NoPayment != nil && *req.Body.NoPayment
	if len(named) > maxReversalPayments {
		errs = withFieldError(errs, "removePayments", fmt.Sprintf("A reversal removes at most %d payments", maxReversalPayments))
	}
	seen := map[int64]bool{}
	for i, p := range named {
		if seen[p.PaymentId] {
			errs = withFieldError(errs, fmt.Sprintf("removePayments[%d].paymentId", i), "Each payment is named once")
		}
		seen[p.PaymentId] = true
	}
	if noPayment && len(named) > 0 {
		errs = withFieldError(errs, "noPayment", "Name the payments the reversal takes back, or say none is — not both")
	}
	if errs != nil {
		return gen.PostInvoicesBankTransactionsByIdHandleReversal400ApplicationProblemPlusJSONResponse(invalid(invalidReversalTitle, errs)), nil
	}
	judge := func(line store.InvoicesBankTransaction) *gen.InvoicesConflictProblem {
		switch {
		case line.Status != lineException:
			return notOpen(cannotHandleReversalTitle)
		case line.Reason == nil || *line.Reason != reasonReversal:
			return notApplicable(cannotHandleReversalTitle, "This bank line is not a reversal: apply it or dismiss it instead.")
		case len(named) == 0 && (!noPayment || note == ""):
			return ptr(conflict(codeReversalPaymentRequired, cannotHandleReversalTitle,
				"Name the payment the bank took back, or set noPayment and say in the note why no payment is removed."))
		}
		return nil
	}
	caller, now := callerID(ctx), s.deps.Clock()
	refusal, err := s.queueAction(ctx, req.Id, judge, func(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error) {
		ids := make([]int64, 0, len(named))
		for _, p := range named {
			if !slices.Contains(ids, p.InvoiceId) {
				ids = append(ids, p.InvoiceId)
			}
		}
		if len(ids) > 0 {
			if _, err := lockInvoicesDescending(ctx, txq, ids); err != nil {
				return nil, err
			}
		}
		ordered := slices.Clone(named)
		slices.SortFunc(ordered, func(a, b gen.InvoicesBankTransactionReversalPayment) int {
			if c := compareInt64(b.InvoiceId, a.InvoiceId); c != 0 {
				return c
			}
			return compareInt64(a.PaymentId, b.PaymentId)
		})
		reason := "Reversed by the bank: line " + line.LineRef
		var reversedLines []int64
		for _, p := range ordered {
			payment, err := txq.GetPayment(ctx, store.GetPaymentParams{ID: p.PaymentId, InvoiceID: p.InvoiceId})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, errNoSuchPayment
			}
			if err != nil {
				return nil, fmt.Errorf("invoices: read payment %d of %d: %w", p.PaymentId, p.InvoiceId, err)
			}
			if payment.RemovedAt != nil {
				return ptr(conflict(codePaymentRemoved, cannotHandleReversalTitle, fmt.Sprintf(
					"Payment %d is already removed. A removal is never undone; name the payment the bank took back.", payment.ID))), nil
			}
			n, err := txq.RemovePayment(ctx, store.RemovePaymentParams{
				RemovedAt: now, RemovedByUserID: caller, RemovalReason: reason, ID: payment.ID, InvoiceID: payment.InvoiceID,
			})
			if err != nil {
				return nil, fmt.Errorf("invoices: remove payment %d of %d: %w", payment.ID, payment.InvoiceID, err)
			}
			if n != 1 {
				return nil, fmt.Errorf("invoices: remove payment %d of %d: %d rows, want 1", payment.ID, payment.InvoiceID, n)
			}
			if payment.BankTransactionID != nil && !slices.Contains(reversedLines, *payment.BankTransactionID) {
				reversedLines = append(reversedLines, *payment.BankTransactionID)
			}
		}
		// The line each payment came from is marked reversed, so its money
		// is never applied again (no unapplied rest, never reopened). The
		// event's foreign key takes FOR KEY SHARE on that line, which its
		// FOR NO KEY UPDATE lets through: no wait, no new lock order.
		for _, id := range reversedLines {
			if err := lineEvent(ctx, txq, id, eventReversed, nil, reason, caller, now); err != nil {
				return nil, err
			}
		}
		return nil, resolveLine(ctx, txq, line, resolutionReversalHandled, reasonReversal, eventReversalHandled, note, []string{lineException}, caller, now)
	})
	if resp, done, err := lineRefusal(refusal, err,
		func() gen.PostInvoicesBankTransactionsByIdHandleReversalResponseObject {
			return gen.PostInvoicesBankTransactionsByIdHandleReversal404Response{}
		},
		func(p gen.InvoicesConflictProblem) gen.PostInvoicesBankTransactionsByIdHandleReversalResponseObject {
			return gen.PostInvoicesBankTransactionsByIdHandleReversal409ApplicationProblemPlusJSONResponse(p)
		}); done {
		return resp, err
	}
	tx, err := oneLineResponse(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankTransactionsByIdHandleReversal200JSONResponse(tx), nil
}

// PostInvoicesBankTransactionsByIdConfirmDuplicate Confirm a bank line a
// duplicate (POST /api/v1/invoices/bank-transactions/{id}/confirm-duplicate)
func (s *server) PostInvoicesBankTransactionsByIdConfirmDuplicate(ctx context.Context, req gen.PostInvoicesBankTransactionsByIdConfirmDuplicateRequestObject) (gen.PostInvoicesBankTransactionsByIdConfirmDuplicateResponseObject, error) {
	note, errs := queueNote(req.Body.Note)
	if errs != nil {
		return gen.PostInvoicesBankTransactionsByIdConfirmDuplicate400ApplicationProblemPlusJSONResponse(invalid(invalidConfirmTitle, errs)), nil
	}
	judge := func(line store.InvoicesBankTransaction) *gen.InvoicesConflictProblem {
		switch {
		case line.Status == lineDuplicate:
			return nil
		case line.Status != lineException:
			return notOpen(cannotConfirmTitle)
		case line.Reason == nil || *line.Reason != reasonPossibleDuplicate:
			return notApplicable(cannotConfirmTitle, "This bank line was not queued as a possible duplicate: apply it or dismiss it instead.")
		}
		return nil
	}
	caller, now := callerID(ctx), s.deps.Clock()
	refusal, err := s.queueAction(ctx, req.Id, judge, func(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error) {
		return nil, resolveLine(ctx, txq, line, resolutionDuplicateConfirmed, reasonPossibleDuplicate, eventDuplicateConfirmed, note,
			[]string{lineException, lineDuplicate}, caller, now)
	})
	if resp, done, err := lineRefusal(refusal, err,
		func() gen.PostInvoicesBankTransactionsByIdConfirmDuplicateResponseObject {
			return gen.PostInvoicesBankTransactionsByIdConfirmDuplicate404Response{}
		},
		func(p gen.InvoicesConflictProblem) gen.PostInvoicesBankTransactionsByIdConfirmDuplicateResponseObject {
			return gen.PostInvoicesBankTransactionsByIdConfirmDuplicate409ApplicationProblemPlusJSONResponse(p)
		}); done {
		return resp, err
	}
	tx, err := oneLineResponse(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankTransactionsByIdConfirmDuplicate200JSONResponse(tx), nil
}

// PostInvoicesBankTransactionsByIdTreatAsDistinct Treat a duplicate bank
// line as distinct (POST /api/v1/invoices/bank-transactions/{id}/treat-as-distinct)
func (s *server) PostInvoicesBankTransactionsByIdTreatAsDistinct(ctx context.Context, req gen.PostInvoicesBankTransactionsByIdTreatAsDistinctRequestObject) (gen.PostInvoicesBankTransactionsByIdTreatAsDistinctResponseObject, error) {
	judge := func(line store.InvoicesBankTransaction) *gen.InvoicesConflictProblem {
		switch line.Status {
		case lineDuplicate:
			return nil
		case lineException:
			return notApplicable(cannotKeepTitle, "This bank line is already in the queue: apply it, or confirm it a duplicate.")
		}
		return notOpen(cannotKeepTitle)
	}
	q := store.New(s.deps.Pool)
	var suggested *int64
	if line, err := q.GetBankTransaction(ctx, req.Id); err == nil && judge(line) == nil {
		if suggested, err = suggestionOnQueue(ctx, q, line, reasonPossibleDuplicate); err != nil {
			return nil, err
		}
	}
	caller, now := callerID(ctx), s.deps.Clock()
	refusal, err := s.queueAction(ctx, req.Id, judge, func(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error) {
		n, err := txq.TreatAsDistinct(ctx, store.TreatAsDistinctParams{SuggestedInvoiceID: suggested, ID: line.ID})
		if err != nil {
			return nil, fmt.Errorf("invoices: treat line %d as distinct: %w", line.ID, err)
		}
		if n != 1 {
			return nil, fmt.Errorf("invoices: treat line %d as distinct: %d rows, want 1", line.ID, n)
		}
		reason := reasonPossibleDuplicate
		return nil, lineEvent(ctx, txq, line.ID, eventTreatedAsDistinct, &reason, "", caller, now)
	})
	if resp, done, err := lineRefusal(refusal, err,
		func() gen.PostInvoicesBankTransactionsByIdTreatAsDistinctResponseObject {
			return gen.PostInvoicesBankTransactionsByIdTreatAsDistinct404Response{}
		},
		func(p gen.InvoicesConflictProblem) gen.PostInvoicesBankTransactionsByIdTreatAsDistinctResponseObject {
			return gen.PostInvoicesBankTransactionsByIdTreatAsDistinct409ApplicationProblemPlusJSONResponse(p)
		}); done {
		return resp, err
	}
	tx, err := oneLineResponse(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankTransactionsByIdTreatAsDistinct200JSONResponse(tx), nil
}

// reopenReason is the reason a line reopens with (D5): a resolved line its
// own, a matched one payment_removed.
func reopenReason(line store.InvoicesBankTransaction) string {
	if line.Status == lineResolved && line.Reason != nil {
		return *line.Reason
	}
	return reasonPaymentRemoved
}

// PostInvoicesBankTransactionsByIdReopen Reopen a bank line
// (POST /api/v1/invoices/bank-transactions/{id}/reopen)
func (s *server) PostInvoicesBankTransactionsByIdReopen(ctx context.Context, req gen.PostInvoicesBankTransactionsByIdReopenRequestObject) (gen.PostInvoicesBankTransactionsByIdReopenResponseObject, error) {
	judge := func(line store.InvoicesBankTransaction) *gen.InvoicesConflictProblem {
		if line.Status != lineResolved && line.Status != lineMatched {
			return notApplicable(cannotReopenTitle, "This bank line is not resolved or matched: it is already in the queue, or not yet matched.")
		}
		return nil
	}
	q := store.New(s.deps.Pool)
	var suggested *int64
	if line, err := q.GetBankTransaction(ctx, req.Id); err == nil && judge(line) == nil {
		if suggested, err = suggestionOnQueue(ctx, q, line, reopenReason(line)); err != nil {
			return nil, err
		}
	}
	caller, now := callerID(ctx), s.deps.Clock()
	refusal, err := s.queueAction(ctx, req.Id, judge, func(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error) {
		live, err := txq.LiveAppliedTo(ctx, line.ID)
		if err != nil {
			return nil, fmt.Errorf("invoices: read what is applied from line %d: %w", line.ID, err)
		}
		if live.Live > 0 {
			return ptr(conflict(codeBankTransactionApplied, cannotReopenTitle, fmt.Sprintf(
				"%d payment(s) or charge payment(s) registered from this bank line still stand. Remove them first; then reopen it.", live.Live))), nil
		}
		reversed, err := txq.LineReversed(ctx, line.ID)
		if err != nil {
			return nil, fmt.Errorf("invoices: read whether line %d was reversed: %w", line.ID, err)
		}
		if reversed {
			return ptr(conflict(codeBankTransactionReversed, cannotReopenTitle,
				"The bank reversed a payment of this line: its money went back, so it is never applied again.")), nil
		}
		reason := reopenReason(line)
		n, err := txq.ReopenTransaction(ctx, store.ReopenTransactionParams{Reason: reason, SuggestedInvoiceID: suggested, ID: line.ID})
		if err != nil {
			return nil, fmt.Errorf("invoices: reopen line %d: %w", line.ID, err)
		}
		if n != 1 {
			return nil, fmt.Errorf("invoices: reopen line %d: %d rows, want 1", line.ID, n)
		}
		return nil, lineEvent(ctx, txq, line.ID, eventReopened, &reason, "", caller, now)
	})
	if resp, done, err := lineRefusal(refusal, err,
		func() gen.PostInvoicesBankTransactionsByIdReopenResponseObject {
			return gen.PostInvoicesBankTransactionsByIdReopen404Response{}
		},
		func(p gen.InvoicesConflictProblem) gen.PostInvoicesBankTransactionsByIdReopenResponseObject {
			return gen.PostInvoicesBankTransactionsByIdReopen409ApplicationProblemPlusJSONResponse(p)
		}); done {
		return resp, err
	}
	tx, err := oneLineResponse(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankTransactionsByIdReopen200JSONResponse(tx), nil
}
