package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is matching on KID (invoices payments and reminders design D4):
// a file's pending lines, after its rows committed, each classified on the
// pool by D4's seven steps and either queued with its reason or — a
// candidate for one invoice — matched in one READ COMMITTED transaction of
// its own: the line FOR NO KEY UPDATE, then the invoice FOR UPDATE (D18),
// and every figure read after both. A match registers a payment of up to
// the open amount and a charge payment of the rest, principal first, paid on
// the line's booking day and registered by the uploader or the caller of
// …/match; then, in the same transaction, a reminder fee the payment — by
// its ordering day, or its booking day without one — shows was claimed after
// a deadline that was in fact met is waived deadline_met. Matching reads no directory and calls nothing out of
// the module.

// The reasons a line is queued for (D4, D5; ck_bank_transactions_reason).
const (
	reasonReversal          = "reversal"
	reasonNegativeAmount    = "negative_amount"
	reasonPossibleDuplicate = "possible_duplicate"
	reasonVippsPayout       = "vipps_payout"
	reasonNoKid             = "no_kid"
	reasonKidInvalid        = "kid_invalid"
	reasonKidUnknown        = "kid_unknown"
	reasonAccountMismatch   = "account_mismatch"
	reasonInvoiceCredited   = "invoice_credited"
	reasonPaidBeforeIssue   = "paid_before_issue"
	reasonInvoiceSettled    = "invoice_settled"
	reasonExceedsOpen       = "exceeds_open"
)

// The outcomes of one line: matched, queued (with its reason), or nothing
// to do — the line was no longer pending when it was locked or queued.
const (
	outcomeMatched = "matched"
	outcomeQueued  = "queued"
	outcomeNone    = ""
)

// The events a match writes (ck_bank_transaction_events_event).
const (
	eventMatched = "matched"
	eventQueued  = "queued"
)

// waiverDeadlineMet is the reason of the waiver a match writes (D9).
const waiverDeadlineMet = "deadline_met"

// vippsPayout is a Vipps payout's bank text, "Utb. {recipient} Vippsnr
// {payout}" (R4 §4.9): never a customer's payment.
var vippsPayout = regexp.MustCompile(`(?i)\bvippsnr \d+`)

// matchAfterLineLock, when a test sets it (export_test.go's
// SetMatchAfterLineLock), runs inside a line's match right after the line is
// locked, before the invoice, with the line's id; an error rolls the line's
// match back and stops the file's matching. Nil in production.
var matchAfterLineLock func(ctx context.Context, bankTransactionID int64) error

// matchAfterInvoiceLock, when a test sets it (SetMatchAfterInvoiceLock),
// runs inside a line's match right after its invoice is locked, before any
// figure is read, with the line's id; an error rolls the line's match back
// and stops the file's matching. Nil in production.
var matchAfterInvoiceLock func(ctx context.Context, bankTransactionID int64) error

// matchCounts is what one run of matching over a file did: the lines it
// matched and queued, with their amounts, and done — every line it found
// pending that is no longer, those another run matched or queued meanwhile
// included. A line it did not reach is still pending.
type matchCounts struct {
	matched, exceptions, done       int
	matchedAmount, exceptionsAmount *big.Rat
}

// matchFile runs D4 over file fileID's pending lines, in the order they were
// stored, caller registering what is matched, now the request's one clock
// read. Each line is its own transaction, so a line matched stays matched
// whatever happens to the next. Matching stops early at the first error — a
// seam's, the database's — or when ctx ends: the line in hand rolls back and
// stays pending with the rest, the stop is logged at warn with the file and
// the line, and the counts say what was done; POST …/match finishes the
// rest. The error answered is why it stopped, nil when it ran to the end.
func (s *server) matchFile(ctx context.Context, fileID int64, caller uuid.UUID, now time.Time) (matchCounts, error) {
	counts := matchCounts{matchedAmount: new(big.Rat), exceptionsAmount: new(big.Rat)}
	q := store.New(s.deps.Pool)
	stopped := func(lineID int64, err error) (matchCounts, error) {
		s.deps.Logger.WarnContext(ctx, "invoices: matching stopped early; the rest of the file stays pending",
			"bankFileId", fileID, "bankTransactionId", lineID, "error", err.Error())
		return counts, err
	}
	lines, err := q.PendingTransactionsOfFile(ctx, fileID)
	if err != nil {
		return stopped(0, fmt.Errorf("invoices: read bank file %d's pending lines: %w", fileID, err))
	}
	accounts := map[string]store.InvoicesBankImportAccount{}
	for _, line := range lines {
		if err := ctx.Err(); err != nil {
			return stopped(line.ID, err)
		}
		acct, ok := accounts[line.Account]
		if !ok {
			if acct, err = q.ImportAccount(ctx, line.Account); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return stopped(line.ID, fmt.Errorf("invoices: read account %s: %w", line.Account, err))
			}
			accounts[line.Account] = acct
		}
		reason, invoiceID, err := s.classify(ctx, q, line, acct)
		if err != nil {
			return stopped(line.ID, err)
		}
		var outcome string
		if reason != "" {
			// A line whose KID named no invoice keeps the one unambiguous
			// suggestion, when there is one (D5, plan reading 24).
			if invoiceID == 0 && suggestible(reason) {
				_, one, err := suggestionsFor(ctx, q, line)
				if err != nil {
					return stopped(line.ID, err)
				}
				if one != nil {
					invoiceID = *one
				}
			}
			outcome, err = s.queueOnPool(ctx, line, reason, invoiceID, caller, now)
		} else {
			outcome, err = s.matchLine(ctx, line.ID, invoiceID, caller, now)
		}
		if err != nil {
			return stopped(line.ID, err)
		}
		amount, err := ratFromNumeric(line.Amount)
		if err != nil {
			return stopped(line.ID, err)
		}
		switch outcome {
		case outcomeMatched:
			counts.matched++
			counts.matchedAmount.Add(counts.matchedAmount, amount)
		case outcomeQueued:
			counts.exceptions++
			counts.exceptionsAmount.Add(counts.exceptionsAmount, amount)
		}
		counts.done++
	}
	return counts, nil
}

// classify is D4's steps 1–7 on the pool, in order: the reason the line is
// queued for, or "" and the invoice it is a candidate for. invoiceID is also
// the invoice the KID named when the line is queued possible_duplicate or
// account_mismatch — the suggestion it is queued with — and 0 when the KID
// named none (matchFile then keeps the one unambiguous suggestion, D5). acct is the line's account (its zero value when it has no
// row).
func (s *server) classify(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction, acct store.InvoicesBankImportAccount) (reason string, invoiceID int64, err error) {
	// 1. A reversal or a negative line is never a payment.
	switch {
	case line.Direction == "debit":
		return reasonReversal, 0, nil
	case line.Negative:
		return reasonNegativeAmount, 0, nil
	}
	// 2. What the other format may already have registered: a line of the
	// account's new format booked on or before its cutover, or the soft key.
	// Held back with the invoice its KID names, when it names one, as the
	// suggestion — as a possible duplicate seen under the lock is.
	dup := acct.PreviousFormat != nil && line.Format == acct.Format && acct.CutoverThrough.Valid &&
		!line.BookedOn.Time.After(acct.CutoverThrough.Time)
	if !dup && line.Kid != nil {
		if dup, err = softKeyRegistered(ctx, q, line); err != nil {
			return "", 0, err
		}
	}
	if dup {
		inv, _, err := invoiceOfKid(ctx, q, line)
		if err != nil || inv == nil {
			return reasonPossibleDuplicate, 0, err
		}
		return reasonPossibleDuplicate, inv.ID, nil
	}
	// 3. A Vipps payout carries no KID: the invoices were settled at capture.
	if line.Kid == nil && vippsPayout.MatchString(line.RemittanceText) {
		return reasonVippsPayout, 0, nil
	}
	// 4. No KID.
	if line.Kid == nil {
		return reasonNoKid, 0, nil
	}
	// 5 and 6. A KID that is not valid, or that no issued invoice carries.
	inv, reason, err := invoiceOfKid(ctx, q, line)
	if err != nil || inv == nil {
		return reason, 0, err
	}
	// 7. Paid into another account than the invoice printed.
	if inv.SellerBankAccount == nil || *inv.SellerBankAccount != line.Account {
		return reasonAccountMismatch, inv.ID, nil
	}
	return "", inv.ID, nil
}

// invoiceOfKid is D4's steps 5 and 6 for a line with a KID: the issued
// invoice it names, or nil and why not — kid_invalid when kid.Parse fails or
// the check character verifies under neither MOD10 nor MOD11, kid_unknown
// when the body exceeds int64 or no issued document of that number carries
// exactly this KID under the algorithm it was issued with (a credit note
// carries none). A line without a KID names none.
func invoiceOfKid(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction) (*store.InvoicesInvoice, string, error) {
	if line.Kid == nil {
		return nil, reasonNoKid, nil
	}
	body, number, fits, ok := kid.Parse(*line.Kid)
	if !ok {
		return nil, reasonKidInvalid, nil
	}
	check := (*line.Kid)[len(*line.Kid)-1]
	if check != kid.CheckMod10(body) && check != kid.CheckMod11(body) {
		return nil, reasonKidInvalid, nil
	}
	if !fits {
		return nil, reasonKidUnknown, nil
	}
	inv, err := q.InvoiceForKid(ctx, &number)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, reasonKidUnknown, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("invoices: read the document numbered %d: %w", number, err)
	}
	if inv.Kid == nil || *inv.Kid != *line.Kid || inv.KidAlgorithm == nil || !kid.Verify(*inv.Kid, *inv.KidAlgorithm, number) {
		return nil, reasonKidUnknown, nil
	}
	return &inv, "", nil
}

// softKeyRegistered reports whether a line of another file has the same
// account, booking day, amount and KID as line and a live payment or charge
// payment (D4 step 2).
func softKeyRegistered(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction) (bool, error) {
	dup, err := q.SoftKeyRegistered(ctx, store.SoftKeyRegisteredParams{
		Account: line.Account, BookedOn: line.BookedOn, Amount: line.Amount, Kid: *line.Kid, BankFileID: line.BankFileID,
	})
	if err != nil {
		return false, fmt.Errorf("invoices: read the soft key of line %d: %w", line.ID, err)
	}
	return dup, nil
}

// queueOnPool queues a line classification refused, with its queued event,
// in one short transaction that locks nothing first: the UPDATE is only from
// pending, so a line another run matched or queued since the pool read is
// left as it is — outcomeNone, no event.
func (s *server) queueOnPool(ctx context.Context, line store.InvoicesBankTransaction, reason string, invoiceID int64, caller uuid.UUID, now time.Time) (string, error) {
	outcome := outcomeNone
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		outcome, err = queueLine(ctx, txq, line.ID, reason, invoiceID, caller, now)
		return err
	})
	return outcome, err
}

// queueLine makes line lineID an exception with reason — only from pending —
// and writes its queued event, by and at; suggested is the invoice the KID
// named, 0 for none. It answers outcomeQueued, or outcomeNone when the line
// was no longer pending.
func queueLine(ctx context.Context, txq *store.Queries, lineID int64, reason string, suggested int64, by uuid.UUID, at time.Time) (string, error) {
	var hint *int64
	if suggested != 0 {
		hint = &suggested
	}
	n, err := txq.QueueTransaction(ctx, store.QueueTransactionParams{Reason: reason, SuggestedInvoiceID: hint, ID: lineID})
	if err != nil {
		return outcomeNone, fmt.Errorf("invoices: queue line %d: %w", lineID, err)
	}
	if n == 0 {
		return outcomeNone, nil
	}
	if err := txq.InsertBankTransactionEvent(ctx, store.InsertBankTransactionEventParams{
		BankTransactionID: lineID, Event: eventQueued, Reason: &reason, ByUserID: by, At: at,
	}); err != nil {
		return outcomeNone, fmt.Errorf("invoices: record line %d queued: %w", lineID, err)
	}
	return outcomeQueued, nil
}

// matchLine matches line lineID to invoice invoiceID in one transaction (D4
// under the lock): the line FOR NO KEY UPDATE — still pending, else nothing
// to do — then the invoice FOR UPDATE, then every figure after them: the
// soft key again first, the open amount (openOf) and the charges
// outstanding (chargesOf); then D4's table in order. A queued outcome writes
// the reason and its event in the same transaction; a match registers the
// payment and the charge payment, marks the line matched with its event,
// and waives what the payment shows was claimed after a met deadline.
func (s *server) matchLine(ctx context.Context, lineID, invoiceID int64, caller uuid.UUID, now time.Time) (outcome string, err error) {
	outcome = outcomeNone
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		line, err := lockBankTransaction(ctx, txq, lineID)
		if err != nil {
			return fmt.Errorf("invoices: lock line %d: %w", lineID, err)
		}
		if line.Status != "pending" {
			return nil
		}
		if hook := matchAfterLineLock; hook != nil {
			if err := hook(ctx, line.ID); err != nil {
				return err
			}
		}
		inv, err := lockInvoice(ctx, txq, invoiceID)
		if err != nil {
			return fmt.Errorf("invoices: lock document %d for line %d: %w", invoiceID, line.ID, err)
		}
		if hook := matchAfterInvoiceLock; hook != nil {
			if err := hook(ctx, line.ID); err != nil {
				return err
			}
		}
		reason, principal, charges, err := judgeMatch(ctx, txq, line, inv)
		if err != nil {
			return err
		}
		if reason != "" {
			outcome, err = queueLine(ctx, txq, line.ID, reason, inv.ID, caller, now)
			return err
		}
		if err := registerMatch(ctx, txq, line, inv, principal, charges, caller, now); err != nil {
			return err
		}
		// Every matched line: a payment meets a deadline by its ordering
		// day where the bank gives one, else by its booking day (reading 32).
		in, err := s.ruleInputLocked(ctx, txq, inv, businessDay(now), 0)
		if err != nil {
			return err
		}
		if err := waiveDeadlineMet(ctx, txq, inv.ID, in, line, caller, now); err != nil {
			return err
		}
		outcome = outcomeMatched
		return nil
	})
	if err != nil {
		return outcomeNone, err
	}
	return outcome, nil
}

// judgeMatch is D4's table for line against inv, read under both locks: the
// reason it is queued for, or the principal and the charges it pays (either
// may be zero, never both).
func judgeMatch(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction, inv store.InvoicesInvoice) (reason string, principal, charges *big.Rat, err error) {
	// The same payment registered from another file meanwhile — the other
	// notification's import, committed while this one waited.
	if dup, err := softKeyRegistered(ctx, txq, line); err != nil {
		return "", nil, nil, err
	} else if dup {
		return reasonPossibleDuplicate, nil, nil, nil
	}
	credited, _, err := uncredited(ctx, txq, inv)
	if err != nil {
		return "", nil, nil, err
	}
	gross, err := ratFromNumeric(inv.GrossTotal)
	if err != nil {
		return "", nil, nil, err
	}
	open, err := openOf(ctx, txq, inv)
	if err != nil {
		return "", nil, nil, err
	}
	state, _, err := chargesOf(ctx, txq, inv.ID)
	if err != nil {
		return "", nil, nil, err
	}
	outstanding := state.Outstanding
	amount, err := ratFromNumeric(line.Amount)
	if err != nil {
		return "", nil, nil, err
	}
	zero := new(big.Rat)
	switch {
	case credited.Sign() > 0 && credited.Cmp(gross) >= 0:
		return reasonInvoiceCredited, nil, nil, nil
	case line.BookedOn.Time.Before(inv.IssueDate.Time):
		return reasonPaidBeforeIssue, nil, nil, nil
	case open.Sign() > 0 && amount.Cmp(open) <= 0:
		return "", amount, zero, nil
	case open.Sign() > 0 && amount.Cmp(new(big.Rat).Add(open, outstanding)) <= 0:
		return "", open, new(big.Rat).Sub(amount, open), nil
	case open.Sign() <= 0 && amount.Cmp(outstanding) <= 0:
		return "", zero, amount, nil
	case open.Sign() <= 0:
		return reasonInvoiceSettled, nil, nil, nil
	}
	return reasonExceedsOpen, nil, nil, nil
}

// registerMatch writes what a match registers (D4): a payment of principal
// and a charge payment of charges, each only when above zero — source the
// file's format, the line, paid on its booking day, its reference the KID,
// by caller at now — and the line matched with its event.
func registerMatch(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction, inv store.InvoicesInvoice, principal, charges *big.Rat, caller uuid.UUID, now time.Time) error {
	// Fail closed: a bank line is NOK (ck_bank_transactions_currency), and
	// its amount was judged against the invoice's figures as if they were
	// too. An invoice in another currency cannot be paid by it, and no
	// reason of the queue says so yet: the error rolls the line back, leaves
	// it pending, and stops matching with a warning naming it.
	if inv.Currency != "NOK" {
		return fmt.Errorf("invoices: line %d is in NOK and its KID names document %d in %s", line.ID, inv.ID, inv.Currency)
	}
	reference := ""
	if line.Kid != nil {
		reference = *line.Kid
	}
	if principal.Sign() > 0 {
		n, err := numericFromRat(principal, 2)
		if err != nil {
			return fmt.Errorf("invoices: line %d's principal as numeric: %w", line.ID, err)
		}
		if _, err := txq.InsertImportedPayment(ctx, store.InsertImportedPaymentParams{
			InvoiceID: inv.ID, PaidOn: line.BookedOn, Amount: n, Currency: inv.Currency, Source: line.Format,
			BankTransactionID: line.ID, Reference: reference, RegisteredByUserID: caller, RegisteredAt: now,
		}); err != nil {
			return fmt.Errorf("invoices: register line %d's payment against %d: %w", line.ID, inv.ID, err)
		}
	}
	if charges.Sign() > 0 {
		n, err := numericFromRat(charges, 2)
		if err != nil {
			return fmt.Errorf("invoices: line %d's charges as numeric: %w", line.ID, err)
		}
		if _, err := insertChargePayment(ctx, txq, store.InsertChargePaymentParams{
			InvoiceID: inv.ID, PaidOn: line.BookedOn, Amount: n, Currency: inv.Currency, Source: line.Format,
			BankTransactionID: &line.ID, Reference: reference, RegisteredByUserID: caller, RegisteredAt: now,
		}); err != nil {
			return err
		}
	}
	n, err := txq.MarkMatched(ctx, line.ID)
	if err != nil {
		return fmt.Errorf("invoices: mark line %d matched: %w", line.ID, err)
	}
	if n != 1 {
		return fmt.Errorf("invoices: mark line %d matched: %d rows, want 1", line.ID, n)
	}
	if err := txq.InsertBankTransactionEvent(ctx, store.InsertBankTransactionEventParams{
		BankTransactionID: line.ID, Event: eventMatched, ByUserID: caller, At: now,
	}); err != nil {
		return fmt.Errorf("invoices: record line %d matched: %w", line.ID, err)
	}
	return nil
}

// waiveDeadlineMet is D4's last paragraph (D8, D9, plan reading 55): in is
// invoice invoiceID's rule input read under its lock after the match registered
// line's payment, so the payments ordered on or before a letter's deadline —
// this one included — are all in it; every sent letter whose reminder fee
// was claimed after an earlier letter's deadline those payments met
// (reminderrules.ReliedOnMetDeadline) has that fee waived deadline_met, by
// and at. A payment meets a deadline by its ordering day where the line has
// one (OCR's), else by its booking day, its paid_on (reminderrules.DeadlineMet
// falls back to it; reading 32), so a camt.054 payment booked within a
// deadline waives as an OCR one ordered within it does. The compensation is
// never waived here.
func waiveDeadlineMet(ctx context.Context, txq *store.Queries, invoiceID int64, in reminderrules.Input, line store.InvoicesBankTransaction, by uuid.UUID, at time.Time) error {
	ids := reminderrules.ReliedOnMetDeadline(in)
	if len(ids) == 0 {
		return nil
	}
	ws := make([]waiverRequest, 0, len(ids))
	for _, id := range ids {
		ws = append(ws, waiverRequest{reminderID: id, kind: reminderrules.WaiverFee})
	}
	problem, err := waiveCharges(ctx, txq, invoiceID, ws, waiverDeadlineMet,
		fmt.Sprintf("The payment of bank line %s met the deadline.", line.LineRef), by, at)
	if err != nil {
		return err
	}
	if problem != nil {
		return fmt.Errorf("invoices: waive the fees line %d's payment met the deadline of: %s", line.ID, deref(problem.Code))
	}
	return nil
}

// PostInvoicesBankFilesByIdMatch Match a bank file's pending lines
// (POST /api/v1/invoices/bank-files/{id}/match)
//
// matchFile over the file's pending lines with one clock read, the caller
// registering (D3, D4); 200 with the file as it now stands and what this
// request matched and queued.
func (s *server) PostInvoicesBankFilesByIdMatch(ctx context.Context, req gen.PostInvoicesBankFilesByIdMatchRequestObject) (gen.PostInvoicesBankFilesByIdMatchResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetBankFile(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesBankFilesByIdMatch404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("invoices: read bank file %d: %w", req.Id, err)
	}
	counts, _ := s.matchFile(ctx, req.Id, callerID(ctx), s.deps.Clock())
	row, err := q.GetBankFile(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("invoices: read bank file %d: %w", req.Id, err)
	}
	file, err := bankFileResponse(row.InvoicesBankFile, row.Pending, row.Exceptions, row.Matched)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankFilesByIdMatch200JSONResponse(gen.InvoicesBankImportResult{
		File: file, Transactions: file.Transactions, Duplicates: file.Duplicates, Pending: file.Pending,
		Matched: int32(counts.matched), MatchedAmount: floatFromRat(counts.matchedAmount, 2), //nolint:gosec // at most bankfile.MaxTransactions
		Exceptions: int32(counts.exceptions), ExceptionsAmount: floatFromRat(counts.exceptionsAmount, 2), //nolint:gosec // at most bankfile.MaxTransactions
		Ignored: file.IgnoredKinds,
	}), nil
}
