package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the customer's credit (invoices phase 6 design I8-I13, plan
// delivery B-1): a ledger of movements, invoices.customer_credits, whose sum
// per customer and currency is the customer's balance and never goes below
// zero. Money received above what was open is kept as credit by hand (the
// payment's keepRestAsCredit) or from the exception queue (keep-as-credit,
// bankqueue.go); a credit note's surplus moves off its invoice to the
// customer after the issue (moveCreditNoteSurplus, and the invoices.
// credit_moves worker for a move that did not run); credit is applied to an
// invoice as a payment of source credit, and refunded outside Vantigo. A
// removal writes a reversed movement, never an edit.
//
// The lock order (I17a as the plan's reading 1 amends it): the bank line,
// if any; the customer's credit lock (lockCustomerCredit), or two customers'
// in ascending id for a merge, which takes them before its documents; the
// invoices in descending id; settings and the counter. A path that writes
// credit reads the invoice's customer on the pool, takes that customer's
// lock, locks the invoice and compares: a merge that committed in between
// moved the invoice, so the transaction rolls back and runs once more with
// the new customer (withCustomerAndInvoice).

// The codes and titles of the credit's refusals.
const (
	codeCreditExceedsBalance = "credit_exceeds_balance"
	codeCreditExceedsOpen    = "credit_exceeds_open"
	codeRefundRemoved        = "refund_removed"

	cannotApplyCreditTitle = "The credit cannot be applied"
	cannotRefundTitle      = "The refund cannot be recorded"
	cannotRemoveRefund     = "The refund cannot be removed"
	invalidCreditTitle     = "Invalid credit"
	invalidRefundTitle     = "Invalid refund"
)

// The kinds of a movement (ck_customer_credits_kind).
const (
	creditOverpayment = "overpayment"
	creditNoteMove    = "credit_note"
	creditApplied     = "applied"
	creditRefunded    = "refunded"
	creditReversed    = "reversed"
)

// paymentSourceCredit is a payment's source when credit was applied (I11).
const paymentSourceCredit = "credit"

// warningCreditNotApplied is the issue's warning when useCredit could not
// apply the customer's credit after the issue committed (I11).
const warningCreditNotApplied = "credit_not_applied"

// creditAfterLocks, when a test sets it, runs inside every credit path's
// transaction right after its customer's lock and its invoice's, with the
// invoice's id (0 for a refund); an error rolls the path back. Nil in
// production.
var creditAfterLocks func(ctx context.Context, invoiceID int64) error

// creditMoveAfterIssue, when a test sets it, runs before the issue's
// follow-up moves a credit note's surplus; an error it answers stands for the
// follow-up's failure, which leaves the move to the invoices.credit_moves
// worker. Nil in production.
var creditMoveAfterIssue func(ctx context.Context, creditNoteID int64) error

// errCustomerMoved is a credit path that found the invoice's customer
// changed between its read on the pool and its lock: a merge moved it.
var errCustomerMoved = errors.New("invoices: the invoice's customer changed")

// errNoSuchRefund is a refund removal naming a refund that is not the
// customer's.
var errNoSuchRefund = errors.New("invoices: no such refund of the customer")

// creditBalance is the customer's credit in currency, read with q, which
// holds the customer's credit lock.
func creditBalance(ctx context.Context, q *store.Queries, customerID int32, currency string) (*big.Rat, error) {
	n, err := q.CreditBalance(ctx, store.CreditBalanceParams{CustomerID: customerID, Currency: currency})
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's credit in %s: %w", customerID, currency, err)
	}
	return ratFromNumeric(n)
}

// creditExceeds is credit_exceeds_balance for title, with the balance.
func creditExceeds(title string, balance *big.Rat, detail string) *gen.InvoicesConflictProblem {
	p := conflict(codeCreditExceedsBalance, title, detail)
	p.Balance = ptr(floatFromRat(balance, 2))
	return &p
}

// insertCredit writes one movement, with q holding the customer's credit
// lock (I8): a negative one that would take the balance below zero is
// credit_exceeds_balance for title, written nothing. amount is the signed
// amount; amount_nok is the trigger's for NOK.
func insertCredit(ctx context.Context, q *store.Queries, row store.InsertCustomerCreditParams, amount *big.Rat, title string) (store.InvoicesCustomerCredit, *gen.InvoicesConflictProblem, error) {
	if amount.Sign() < 0 {
		balance, err := creditBalance(ctx, q, row.CustomerID, row.Currency)
		if err != nil {
			return store.InvoicesCustomerCredit{}, nil, err
		}
		if new(big.Rat).Add(balance, amount).Sign() < 0 {
			return store.InvoicesCustomerCredit{}, creditExceeds(title, balance, fmt.Sprintf(
				"The customer has %s %s in credit, and this takes %s of it.", row.Currency, balance.FloatString(2),
				new(big.Rat).Neg(amount).FloatString(2))), nil
		}
	}
	n, err := numericFromRat(amount, 2)
	if err != nil {
		return store.InvoicesCustomerCredit{}, nil, err
	}
	row.Amount = n
	written, err := q.InsertCustomerCredit(ctx, row)
	if err != nil {
		return store.InvoicesCustomerCredit{}, nil, fmt.Errorf("invoices: write customer %d's %s credit: %w", row.CustomerID, row.Kind, err)
	}
	return written, nil, nil
}

// reverseCredit writes the reversal of movement c, of which reversed (≤ 0
// for a positive movement, ≥ 0 for a negative one) is already reversed: all
// that is left of it, or at most limit of it when limit is not nil (a credit
// note's surplus, which goes back in parts). A reversal that would take the
// balance below zero — an overpayment that was used — is
// credit_exceeds_balance for title. It answers how much it reversed, as the
// reversal's amount.
func reverseCredit(ctx context.Context, q *store.Queries, c store.InvoicesCustomerCredit, reversed pgtype.Numeric, limit *big.Rat,
	day time.Time, by uuid.UUID, at time.Time, title string,
) (*big.Rat, *gen.InvoicesConflictProblem, error) {
	amount, err := ratFromNumeric(c.Amount)
	if err != nil {
		return nil, nil, err
	}
	done, err := ratFromNumeric(reversed)
	if err != nil {
		return nil, nil, err
	}
	back := new(big.Rat).Neg(new(big.Rat).Add(amount, done))
	if back.Sign() == 0 {
		return back, nil, nil
	}
	if limit != nil && new(big.Rat).Abs(back).Cmp(limit) > 0 {
		back = new(big.Rat).Set(limit)
		if amount.Sign() > 0 {
			back.Neg(back)
		}
	}
	if _, refusal, err := insertCredit(ctx, q, store.InsertCustomerCreditParams{
		CustomerID: c.CustomerID, Currency: c.Currency, Kind: creditReversed, CreditDay: pgDate(day),
		InvoiceID: c.InvoiceID, ReversesID: &c.ID, RegisteredByUserID: &by, RegisteredAt: at,
		AmountNok: nokOfCredit(c, back),
	}, back, title); refusal != nil || err != nil {
		return nil, refusal, err
	}
	return back, nil, nil
}

// nokOfCredit is the NOK a part of movement c is worth: NULL for NOK, which
// the trigger fills; else the movement's own rate, amount_nok over amount.
func nokOfCredit(c store.InvoicesCustomerCredit, part *big.Rat) pgtype.Numeric {
	if c.Currency == "NOK" {
		return pgtype.Numeric{}
	}
	amount, err := ratFromNumeric(c.Amount)
	if err != nil || amount.Sign() == 0 {
		return pgtype.Numeric{}
	}
	nok, err := ratFromNumeric(c.AmountNok)
	if err != nil {
		return pgtype.Numeric{}
	}
	n, err := numericFromRat(docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(part, nok), amount)), 2)
	if err != nil {
		return pgtype.Numeric{}
	}
	return n
}

// nokOfInvoice is the NOK an amount in an invoice's currency is worth at its
// exchange rate: NULL for NOK, which the trigger fills (I13).
func nokOfInvoice(inv store.InvoicesInvoice, amount *big.Rat) (pgtype.Numeric, error) {
	if inv.Currency == "NOK" {
		return pgtype.Numeric{}, nil
	}
	rate, err := ratFromNumeric(inv.ExchangeRate)
	if err != nil {
		return pgtype.Numeric{}, err
	}
	return numericFromRat(docmath.Round2(new(big.Rat).Mul(amount, rate)), 2)
}

// withCustomerAndInvoice runs fn in one locked transaction that holds the
// customer's credit lock and then invoice id FOR UPDATE, in I17a's order:
// the invoice's customer is read on the pool, its lock taken, the invoice
// locked and its customer compared. When a merge moved the invoice in
// between, the transaction rolls back and runs once more with the new
// customer; a second move is an error. errDocumentGone when the document
// does not exist.
func (s *server) withCustomerAndInvoice(ctx context.Context, id int64,
	fn func(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice) error,
) error {
	q := store.New(s.deps.Pool)
	for attempt := 0; ; attempt++ {
		read, err := q.GetInvoice(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: read document %d: %w", id, err)
		}
		err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			if err := lockCustomerCredit(ctx, txq, read.CustomerID); err != nil {
				return err
			}
			locked, err := lockInvoice(ctx, txq, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return errDocumentGone
			}
			if err != nil {
				return fmt.Errorf("invoices: lock document %d: %w", id, err)
			}
			if locked.CustomerID != read.CustomerID {
				return errCustomerMoved
			}
			if hook := creditAfterLocks; hook != nil {
				if err := hook(ctx, id); err != nil {
					return err
				}
			}
			return fn(ctx, txq, locked)
		})
		if errors.Is(err, errCustomerMoved) && attempt == 0 {
			continue
		}
		if errors.Is(err, errCustomerMoved) {
			return fmt.Errorf("invoices: document %d moved customer twice while its credit was written: %w", id, err)
		}
		return err
	}
}

// returnMovedCredit is the removal's and the undo's (plan reading 8): when
// they leave inv — locked, with its customer's credit lock held — open by
// more than 0 while a credit note's surplus has moved to the customer, the
// surplus comes back as far as the customer's credit and what is open reach,
// newest move first, so the invoice never duns what the customer holds as
// credit. Every reversal is by, the person removing or undoing.
func returnMovedCredit(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice, day time.Time, by uuid.UUID, at time.Time) error {
	movedN, err := txq.MovedOf(ctx, inv.ID)
	if err != nil {
		return fmt.Errorf("invoices: read what moved off document %d: %w", inv.ID, err)
	}
	moved, err := ratFromNumeric(movedN)
	if err != nil || moved.Sign() <= 0 {
		return err
	}
	open, reserved, err := openAndReserved(ctx, txq, inv)
	if err != nil {
		return err
	}
	open.Add(open, reserved)
	if open.Sign() <= 0 {
		return nil
	}
	balance, err := creditBalance(ctx, txq, inv.CustomerID, inv.Currency)
	if err != nil {
		return err
	}
	take := moved
	for _, r := range []*big.Rat{open, balance} {
		if r.Cmp(take) < 0 {
			take = r
		}
	}
	if take.Sign() <= 0 {
		return nil
	}
	moves, err := txq.CreditNoteMovesOf(ctx, &inv.ID)
	if err != nil {
		return fmt.Errorf("invoices: read the moves of document %d: %w", inv.ID, err)
	}
	left := new(big.Rat).Set(take)
	for _, m := range moves {
		if left.Sign() <= 0 {
			break
		}
		back, refusal, err := reverseCredit(ctx, txq, m.InvoicesCustomerCredit, m.Reversed, left, day, by, at, cannotRemoveTitle)
		if err != nil {
			return err
		}
		if refusal != nil {
			// The balance was read under the lock, so this cannot be the
			// caller's: it is the server's.
			return fmt.Errorf("invoices: return moved credit of document %d: %s", inv.ID, deref(refusal.Detail))
		}
		left.Sub(left, new(big.Rat).Abs(back))
	}
	return nil
}

// moveCreditNoteSurplus is I10's follow-up for one credit note, in a
// transaction of its own after the issue's: its original's customer's lock,
// then the original FOR UPDATE; the original's open amount read under them,
// reservations aside; and when it is below zero, the credit_note movement of
// its negation, unattributed, dated the credit note's issue date. Nothing
// when the original is not below zero, and nothing the second time: one move
// per credit note (ux_customer_credits_credit_note).
func (s *server) moveCreditNoteSurplus(ctx context.Context, creditNoteID int64) error {
	q := store.New(s.deps.Pool)
	cn, err := q.GetInvoice(ctx, creditNoteID)
	if err != nil {
		return fmt.Errorf("invoices: read credit note %d: %w", creditNoteID, err)
	}
	if cn.Kind != kindCreditNote || cn.Status != statusIssued || cn.CreditsInvoiceID == nil {
		return nil
	}
	now := s.deps.Clock()
	err = s.withCustomerAndInvoice(ctx, *cn.CreditsInvoiceID, func(ctx context.Context, txq *store.Queries, original store.InvoicesInvoice) error {
		open, reserved, err := openAndReserved(ctx, txq, original)
		if err != nil {
			return err
		}
		open.Add(open, reserved)
		if open.Sign() >= 0 {
			return nil
		}
		surplus := new(big.Rat).Neg(open)
		n, err := numericFromRat(surplus, 2)
		if err != nil {
			return err
		}
		nok, err := nokOfInvoice(original, surplus)
		if err != nil {
			return err
		}
		_, err = txq.InsertCreditNoteMove(ctx, store.InsertCreditNoteMoveParams{
			CustomerID: original.CustomerID, Currency: original.Currency, Amount: n, AmountNok: nok,
			CreditDay: cn.IssueDate, InvoiceID: &original.ID, DocumentID: &cn.ID, RegisteredAt: now,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// Moved already: the follow-up ran before, or the worker did.
			return nil
		}
		if err != nil {
			return fmt.Errorf("invoices: move credit note %d's surplus: %w", cn.ID, err)
		}
		return nil
	})
	if errors.Is(err, errDocumentGone) {
		return nil
	}
	return err
}

// moveAfterIssue runs moveCreditNoteSurplus after a credit note's issue
// committed; a failure is logged at warn and never fails the issue — the
// invoices.credit_moves worker moves it later.
func (s *server) moveAfterIssue(ctx context.Context, doc store.InvoicesInvoice) {
	if doc.Kind != kindCreditNote {
		return
	}
	err := error(nil)
	if hook := creditMoveAfterIssue; hook != nil {
		err = hook(ctx, doc.ID)
	}
	if err == nil {
		err = s.moveCreditNoteSurplus(ctx, doc.ID)
	}
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: the credit note's surplus did not move to the customer's credit; the credit moves worker retries it",
			"credit_note_id", doc.ID, "error", err)
	}
}

// applyCredit applies amount of the customer's credit to issued invoice id
// (I11), as by, paid on day: under the customer's lock and then the
// invoice's, the refusals judged against what is open and the balance, then
// a payment of source credit and its applied movement. A nil amount is the
// lesser of the balance and what is open (useCredit on issue); nothing to
// apply then writes nothing and answers no refusal.
func (s *server) applyCredit(ctx context.Context, id int64, amount *big.Rat, day time.Time, by uuid.UUID) (*gen.InvoicesConflictProblem, error) {
	var refusal *gen.InvoicesConflictProblem
	now := s.deps.Clock()
	err := s.withCustomerAndInvoice(ctx, id, func(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice) error {
		switch {
		case inv.Kind == kindCreditNote:
			refusal = ptr(conflict(codeCreditNoteNoPayments, cannotApplyCreditTitle, "Credit is applied to an invoice, never a credit note."))
			return errRefused
		case inv.Status != statusIssued:
			refusal = ptr(conflict(codeInvoiceDraft, cannotApplyCreditTitle, "A draft is not a sales document: issue it before applying credit to it."))
			return errRefused
		}
		open, err := openOf(ctx, txq, inv)
		if err != nil {
			return err
		}
		balance, err := creditBalance(ctx, txq, inv.CustomerID, inv.Currency)
		if err != nil {
			return err
		}
		use := amount
		if use == nil {
			use = balance
			if open.Cmp(use) < 0 {
				use = open
			}
			if use.Sign() <= 0 {
				return nil
			}
		}
		switch {
		case open.Sign() <= 0:
			refusal = ptr(conflict(codeInvoiceSettled, cannotApplyCreditTitle, "Nothing is open on this invoice."))
			return errRefused
		case use.Cmp(open) > 0:
			refusal = ptr(conflict(codeCreditExceedsOpen, cannotApplyCreditTitle, fmt.Sprintf(
				"This is %s, and the invoice has %s open.", use.FloatString(2), open.FloatString(2))))
			refusal.OpenAmount = ptr(floatFromRat(open, 2))
			return errRefused
		case use.Cmp(balance) > 0:
			refusal = creditExceeds(cannotApplyCreditTitle, balance, fmt.Sprintf(
				"The customer has %s %s in credit, and this applies %s.", inv.Currency, balance.FloatString(2), use.FloatString(2)))
			return errRefused
		}
		if day.Before(inv.IssueDate.Time) {
			day = inv.IssueDate.Time
		}
		n, err := numericFromRat(use, 2)
		if err != nil {
			return err
		}
		payment, err := txq.InsertCreditPayment(ctx, store.InsertCreditPaymentParams{
			InvoiceID: inv.ID, PaidOn: pgDate(day), Amount: n, Currency: inv.Currency, RegisteredByUserID: by, RegisteredAt: now,
		})
		if err != nil {
			return fmt.Errorf("invoices: apply credit to %d: %w", inv.ID, err)
		}
		nok, err := nokOfInvoice(inv, use)
		if err != nil {
			return err
		}
		if _, refusal, err = insertCredit(ctx, txq, store.InsertCustomerCreditParams{
			CustomerID: inv.CustomerID, Currency: inv.Currency, Kind: creditApplied, CreditDay: pgDate(day),
			InvoiceID: &inv.ID, PaymentID: &payment.ID, RegisteredByUserID: &by, RegisteredAt: now, AmountNok: nok,
		}, new(big.Rat).Neg(use), cannotApplyCreditTitle); err != nil {
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

// PostInvoicesByIdApplyCredit Apply the customer's credit to an invoice
// (POST /api/v1/invoices/{id}/apply-credit)
func (s *server) PostInvoicesByIdApplyCredit(ctx context.Context, req gen.PostInvoicesByIdApplyCreditRequestObject) (gen.PostInvoicesByIdApplyCreditResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdApplyCredit404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	today := holidays.DayIn(s.deps.Clock(), loc)
	var errs map[string][]string
	amt, msg := amount("An amount", req.Body.Amount, 2, zero, true, maxGrossTotal)
	if msg != "" {
		errs = withFieldError(errs, "amount", msg)
	}
	day := today
	if req.Body.PaidOn != nil {
		day = utcDay(req.Body.PaidOn.Time)
		switch {
		case inv.IssueDate.Valid && day.Before(inv.IssueDate.Time):
			errs = withFieldError(errs, "paidOn", "Credit is applied on or after the invoice's issue date, "+inv.IssueDate.Time.Format(time.DateOnly))
		case day.After(today):
			errs = withFieldError(errs, "paidOn", "Credit is applied today at the latest, "+today.Format(time.DateOnly))
		}
	}
	if errs != nil {
		return gen.PostInvoicesByIdApplyCredit400ApplicationProblemPlusJSONResponse(invalid(invalidCreditTitle, errs)), nil
	}
	refusal, err := s.applyCredit(ctx, req.Id, amt, day, callerID(ctx))
	if refusal != nil {
		return gen.PostInvoicesByIdApplyCredit409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdApplyCredit404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if inv, err = q.GetInvoice(ctx, req.Id); err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	resp, err := s.invoiceResponse(ctx, q, inv, nil, loc)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdApplyCredit200JSONResponse(resp), nil
}

// useCreditAfterIssue is the issue's useCredit (I11): after the issue
// committed, apply's transaction for the lesser of the customer's credit and
// the invoice's open amount, as the issuer, on the issue date. A refusal or
// a failure is the answer's warning credit_not_applied, never the issue's
// refusal.
func (s *server) useCreditAfterIssue(ctx context.Context, doc store.InvoicesInvoice) []string {
	if doc.Kind != kindInvoice {
		return nil
	}
	refusal, err := s.applyCredit(ctx, doc.ID, nil, doc.IssueDate.Time, callerID(ctx))
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: the customer's credit was not applied to the invoice issued", "invoice_id", doc.ID, "error", err)
	}
	if err != nil || refusal != nil {
		return []string{warningCreditNotApplied}
	}
	return nil
}

// parseRefund runs I12's field rules over a refund's body, every failure
// collected.
func parseRefund(body gen.InvoicesCreditRefundRequest, today time.Time) (store.InsertCreditRefundParams, *big.Rat, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	p := store.InsertCreditRefundParams{
		Currency: strings.ToUpper(strings.TrimSpace(body.Currency)), BankAccount: strings.TrimSpace(body.BankAccount),
		Reference: optionalText(body.Reference), Note: optionalText(body.Note),
	}
	day := utcDay(body.RefundedOn.Time)
	switch {
	case body.RefundedOn.IsZero():
		add("refundedOn", "A refund needs the day the money left")
	case day.After(today):
		add("refundedOn", "A refund is paid today at the latest, "+today.Format(time.DateOnly))
	}
	p.RefundedOn = pgDate(day)
	amt, msg := amount("An amount", body.Amount, 2, zero, true, maxGrossTotal)
	add("amount", msg)
	if amt != nil {
		n, err := numericFromRat(amt, 2)
		if err != nil {
			return p, nil, nil, fmt.Errorf("invoices: a refund's amount as numeric: %w", err)
		}
		p.Amount = n
	}
	if p.Currency != "NOK" {
		add("currency", "A refund is in NOK in this version")
	}
	if !validBankAccount(p.BankAccount) {
		add("bankAccount", "The account is 11 digits with a valid check digit")
	}
	add("reference", maxLength("A reference", p.Reference, 100))
	add("note", maxLength("The note", p.Note, 500))
	return p, amt, errs, nil
}

// PostInvoicesCustomersByCustomerIdCreditRefunds Record a refund of a
// customer's credit (POST /api/v1/invoices/customers/{customerId}/credit/refunds)
func (s *server) PostInvoicesCustomersByCustomerIdCreditRefunds(ctx context.Context, req gen.PostInvoicesCustomersByCustomerIdCreditRefundsRequestObject) (gen.PostInvoicesCustomersByCustomerIdCreditRefundsResponseObject, error) {
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	refund, amt, errs, err := parseRefund(*req.Body, holidays.DayIn(s.deps.Clock(), loc))
	if err != nil {
		return nil, err
	}
	if errs != nil {
		return gen.PostInvoicesCustomersByCustomerIdCreditRefunds400ApplicationProblemPlusJSONResponse(invalid(invalidRefundTitle, errs)), nil
	}
	caller, now := callerID(ctx), s.deps.Clock()
	refund.CustomerID, refund.RegisteredByUserID, refund.RegisteredAt = req.CustomerId, caller, now
	var refusal *gen.InvoicesConflictProblem
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		// The customer's lock alone: a refund names no invoice.
		if err := lockCustomerCredit(ctx, txq, req.CustomerId); err != nil {
			return err
		}
		if hook := creditAfterLocks; hook != nil {
			if err := hook(ctx, 0); err != nil {
				return err
			}
		}
		balance, err := creditBalance(ctx, txq, req.CustomerId, refund.Currency)
		if err != nil {
			return err
		}
		if amt.Cmp(balance) > 0 {
			refusal = creditExceeds(cannotRefundTitle, balance, fmt.Sprintf(
				"The customer has %s %s in credit, and the refund is %s.", refund.Currency, balance.FloatString(2), amt.FloatString(2)))
			return errRefused
		}
		row, err := txq.InsertCreditRefund(ctx, refund)
		if err != nil {
			return fmt.Errorf("invoices: record customer %d's refund: %w", req.CustomerId, err)
		}
		if _, refusal, err = insertCredit(ctx, txq, store.InsertCustomerCreditParams{
			CustomerID: req.CustomerId, Currency: refund.Currency, Kind: creditRefunded, CreditDay: refund.RefundedOn,
			Note: refund.Note, RefundID: &row.ID, RegisteredByUserID: &caller, RegisteredAt: now,
		}, new(big.Rat).Neg(amt), cannotRefundTitle); err != nil {
			return err
		}
		if refusal != nil {
			return errRefused
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesCustomersByCustomerIdCreditRefunds409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := customerCreditResponse(ctx, store.New(s.deps.Pool), req.CustomerId, 1, defaultPageSize)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesCustomersByCustomerIdCreditRefunds200JSONResponse(resp), nil
}

// PostInvoicesCustomersByCustomerIdCreditRefundsByRefundIdRemove Remove a
// refund of a customer's credit
// (POST /api/v1/invoices/customers/{customerId}/credit/refunds/{refundId}/remove)
func (s *server) PostInvoicesCustomersByCustomerIdCreditRefundsByRefundIdRemove(ctx context.Context, req gen.PostInvoicesCustomersByCustomerIdCreditRefundsByRefundIdRemoveRequestObject) (gen.PostInvoicesCustomersByCustomerIdCreditRefundsByRefundIdRemoveResponseObject, error) {
	reason := strings.TrimSpace(req.Body.Reason)
	var errs map[string][]string
	if reason == "" {
		errs = fieldError("reason", "A removal needs a reason")
	} else if msg := maxLength("A reason", reason, 200); msg != "" {
		errs = fieldError("reason", msg)
	}
	if errs != nil {
		return gen.PostInvoicesCustomersByCustomerIdCreditRefundsByRefundIdRemove400ApplicationProblemPlusJSONResponse(invalid(invalidRemovalTitle, errs)), nil
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	caller, now := callerID(ctx), s.deps.Clock()
	var refusal *gen.InvoicesConflictProblem
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := lockCustomerCredit(ctx, txq, req.CustomerId); err != nil {
			return err
		}
		if hook := creditAfterLocks; hook != nil {
			if err := hook(ctx, 0); err != nil {
				return err
			}
		}
		refund, err := txq.GetCreditRefund(ctx, store.GetCreditRefundParams{ID: req.RefundId, CustomerID: req.CustomerId})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoSuchRefund
		}
		if err != nil {
			return fmt.Errorf("invoices: read refund %d: %w", req.RefundId, err)
		}
		if refund.RemovedAt != nil {
			refusal = ptr(conflict(codeRefundRemoved, cannotRemoveRefund, "This refund is already removed. A removal is never undone; record the refund again."))
			return errRefused
		}
		if n, err := txq.RemoveCreditRefund(ctx, store.RemoveCreditRefundParams{
			RemovedAt: now, RemovedByUserID: caller, RemovalReason: reason, ID: refund.ID, CustomerID: req.CustomerId,
		}); err != nil {
			return fmt.Errorf("invoices: remove refund %d: %w", refund.ID, err)
		} else if n != 1 {
			return fmt.Errorf("invoices: remove refund %d: %d rows, want 1", refund.ID, n)
		}
		move, err := txq.RefundedMoveOf(ctx, &refund.ID)
		if err != nil {
			return fmt.Errorf("invoices: read refund %d's movement: %w", refund.ID, err)
		}
		_, refusal, err = reverseCredit(ctx, txq, move, pgtype.Numeric{Int: big.NewInt(0), Valid: true}, nil,
			holidays.DayIn(now, loc), caller, now, cannotRemoveRefund)
		if err != nil {
			return err
		}
		if refusal != nil {
			return errRefused
		}
		return nil
	})
	switch {
	case refusal != nil:
		return gen.PostInvoicesCustomersByCustomerIdCreditRefundsByRefundIdRemove409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, errNoSuchRefund):
		return gen.PostInvoicesCustomersByCustomerIdCreditRefundsByRefundIdRemove404Response{}, nil
	case err != nil:
		return nil, err
	}
	resp, err := customerCreditResponse(ctx, store.New(s.deps.Pool), req.CustomerId, 1, defaultPageSize)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesCustomersByCustomerIdCreditRefundsByRefundIdRemove200JSONResponse(resp), nil
}

// GetInvoicesCustomersByCustomerIdCredit Get a customer's credit
// (GET /api/v1/invoices/customers/{customerId}/credit)
func (s *server) GetInvoicesCustomersByCustomerIdCredit(ctx context.Context, req gen.GetInvoicesCustomersByCustomerIdCreditRequestObject) (gen.GetInvoicesCustomersByCustomerIdCreditResponseObject, error) {
	if errs := validatePageParams(req.Params.Page, req.Params.PageSize); len(errs) > 0 {
		return gen.GetInvoicesCustomersByCustomerIdCredit400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle,
			fieldError("page", strings.Join(errs, " ")))), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)
	var resp gen.InvoicesCustomerCredit
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		resp, err = customerCreditResponse(ctx, q, req.CustomerId, page, pageSize)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesCustomersByCustomerIdCredit200JSONResponse(resp), nil
}

// defaultPageSize is the codebase's page size when none is asked for.
const defaultPageSize int32 = 25

// customerCreditResponse is the customer's credit on the wire: its balances,
// one page of its movements and every refund.
func customerCreditResponse(ctx context.Context, q *store.Queries, customerID int32, page, pageSize int32) (gen.InvoicesCustomerCredit, error) {
	out := gen.InvoicesCustomerCredit{CustomerId: customerID, Balances: []gen.InvoicesCreditBalance{},
		Movements: []gen.InvoicesCreditMovement{}, Refunds: []gen.InvoicesCreditRefund{}}
	balances, err := q.CreditBalances(ctx, customerID)
	if err != nil {
		return out, fmt.Errorf("invoices: read customer %d's credit: %w", customerID, err)
	}
	for _, b := range balances {
		v, err := floatFromNumeric(b.Balance)
		if err != nil {
			return out, err
		}
		out.Balances = append(out.Balances, gen.InvoicesCreditBalance{Currency: b.Currency, Balance: v})
	}
	total, err := q.CountCustomerCredits(ctx, customerID)
	if err != nil {
		return out, fmt.Errorf("invoices: count customer %d's credit movements: %w", customerID, err)
	}
	rows, err := q.CustomerCredits(ctx, store.CustomerCreditsParams{CustomerID: customerID, PageSize: pageSize, PageOffset: (page - 1) * pageSize})
	if err != nil {
		return out, fmt.Errorf("invoices: read customer %d's credit movements: %w", customerID, err)
	}
	for _, r := range rows {
		c := r.InvoicesCustomerCredit
		amount, err := floatFromNumeric(c.Amount)
		if err != nil {
			return out, err
		}
		reversed, err := floatFromNumeric(r.Reversed)
		if err != nil {
			return out, err
		}
		out.Movements = append(out.Movements, gen.InvoicesCreditMovement{
			Id: c.ID, Kind: gen.InvoicesCreditMovementKind(c.Kind), Currency: c.Currency, Amount: amount,
			CreditDay: wireDate(c.CreditDay.Time), Note: c.Note, InvoiceId: c.InvoiceID, InvoiceNumber: r.InvoiceNumber,
			PaymentId: c.PaymentID, BankTransactionId: c.BankTransactionID, LineRef: r.LineRef, DocumentId: c.DocumentID,
			DocumentNumber: r.DocumentNumber, RefundId: c.RefundID, ReversesId: c.ReversesID, Reversed: reversed,
			RegisteredByUserId: c.RegisteredByUserID, RegisteredAt: c.RegisteredAt,
		})
	}
	refunds, err := q.CreditRefundsOf(ctx, customerID)
	if err != nil {
		return out, fmt.Errorf("invoices: read customer %d's refunds: %w", customerID, err)
	}
	for _, r := range refunds {
		amount, err := floatFromNumeric(r.Amount)
		if err != nil {
			return out, err
		}
		out.Refunds = append(out.Refunds, gen.InvoicesCreditRefund{
			Id: r.ID, RefundedOn: wireDate(r.RefundedOn.Time), Amount: amount, Currency: r.Currency, BankAccount: r.BankAccount,
			Reference: r.Reference, Note: r.Note, RegisteredAt: r.RegisteredAt, RegisteredByUserId: r.RegisteredByUserID,
			RemovedAt: r.RemovedAt, RemovedByUserId: r.RemovedByUserID, RemovalReason: r.RemovalReason,
		})
	}
	out.Pagination = apicommon.Pagination(page, pageSize, total)
	return out, nil
}

// creditKey is a customer's credit lock as the lock-order seam reports it.
func creditKey(customerID int32) string { return strconv.FormatInt(int64(customerID), 10) }
