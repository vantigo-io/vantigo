package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// The exception queue's credit (invoices phase 6 design I9): a line queued
// exceeds_open or invoice_settled may have its surplus kept as the invoice's
// customer's credit — what the invoice has open applied as a payment, the
// rest an overpayment movement naming the line — and a reversal may take
// such an overpayment back. Both take the line first, then the customer's
// credit lock, then the invoices in descending id (I17a).

// eventKeptAsCredit is a line whose surplus a person kept as credit.
const eventKeptAsCredit = "kept_as_credit"

const (
	cannotKeepAsCreditTitle = "The bank line cannot be kept as credit"
	invalidKeepTitle        = "Invalid keep as credit"
)

// judgeKeepable is keep-as-credit's judgement of a line: an exception
// queued exceeds_open or invoice_settled, money above what an invoice had
// open.
func judgeKeepable(line store.InvoicesBankTransaction) *gen.InvoicesConflictProblem {
	switch {
	case line.Status != lineException:
		return notOpen(cannotKeepAsCreditTitle)
	case line.Reason == nil || (*line.Reason != reasonExceedsOpen && *line.Reason != reasonInvoiceSettled):
		return notApplicable(cannotKeepAsCreditTitle,
			"Only a line queued as more than its invoice had open, or for a settled invoice, is kept as credit: apply it or dismiss it instead.")
	}
	return nil
}

// keepLocked keeps line's surplus as credit (I9), the line held FOR NO KEY
// UPDATE and the credit lock of customer — invoiceID's customer as read in
// the transaction before it — held: the invoices the line paid and
// invoiceID FOR UPDATE in descending id; the apply's rules for the invoice;
// what is left of the line, read under its lock; what the invoice has open
// applied as a payment, as the apply registers one, and the rest an
// overpayment movement of the customer naming the line; the line resolved,
// applied, with a kept_as_credit event.
func (s *server) keepLocked(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction, invoiceID int64, customer int32,
	note string, caller uuid.UUID, now time.Time, loc *time.Location,
) (*gen.InvoicesConflictProblem, error) {
	if refusal, err := duplicateOfReversed(ctx, txq, line, cannotKeepAsCreditTitle); refusal != nil || err != nil {
		return refusal, err
	}
	invs, note, err := lockForNote(ctx, txq, line.ID, []int64{invoiceID}, note)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(invs, func(inv store.InvoicesInvoice) bool { return inv.ID == invoiceID })
	if i < 0 || invs[i].Kind != kindInvoice || invs[i].Status != statusIssued {
		return ptr(conflict(codeAllocationNotAnInvoice, cannotKeepAsCreditTitle, fmt.Sprintf(
			"Document %d is not an issued invoice: a bank line's surplus is kept as the credit of an issued invoice's customer.", invoiceID))), nil
	}
	inv := invs[i]
	if inv.CustomerID != customer {
		return nil, errCustomerMoved
	}
	if hook := creditAfterLocks; hook != nil {
		if err := hook(ctx, inv.ID); err != nil {
			return nil, err
		}
	}
	if line.BookedOn.Time.Before(inv.IssueDate.Time) {
		return ptr(conflict(codePaidBeforeIssue, cannotKeepAsCreditTitle, fmt.Sprintf(
			"The bank booked this line on %s, before invoice %s was issued on %s: it cannot be a payment of it.",
			line.BookedOn.Time.Format(time.DateOnly), numberText(inv), inv.IssueDate.Time.Format(time.DateOnly)))), nil
	}
	if inv.Currency != "NOK" {
		return nil, fmt.Errorf("invoices: line %d is in NOK and invoice %d is in %s", line.ID, inv.ID, inv.Currency)
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
	if left.Sign() <= 0 {
		return ptr(conflict(codeAllocationExceedsTransaction, cannotKeepAsCreditTitle,
			"Nothing of this bank line is left to keep: what is applied from it takes all of it.")), nil
	}
	open, err := openOf(ctx, txq, inv)
	if err != nil {
		return nil, err
	}
	pay := new(big.Rat)
	if open.Sign() > 0 {
		pay.Set(open)
	}
	if pay.Cmp(left) >= 0 {
		return notApplicable(cannotKeepAsCreditTitle, fmt.Sprintf(
			"Invoice %s has %s open, which takes the whole %s left of this line: apply it instead.",
			numberText(inv), open.FloatString(2), left.FloatString(2))), nil
	}
	rest := new(big.Rat).Sub(left, pay)
	reference := deref(line.Kid)
	if pay.Sign() > 0 {
		n, err := numericFromRat(pay, 2)
		if err != nil {
			return nil, err
		}
		nokN, err := amountNOKOf(inv, n)
		if err != nil {
			return nil, err
		}
		if _, err := txq.InsertImportedPayment(ctx, store.InsertImportedPaymentParams{
			InvoiceID: inv.ID, PaidOn: line.BookedOn, Amount: n, AmountNok: nokN, Currency: inv.Currency, Source: paymentSourceOf(line.Format),
			BankTransactionID: line.ID, Reference: reference, RegisteredByUserID: &caller, RegisteredAt: now,
		}); err != nil {
			return nil, fmt.Errorf("invoices: apply line %d's payment to %d: %w", line.ID, inv.ID, err)
		}
		in, err := s.ruleInputLocked(ctx, txq, inv, holidays.DayIn(now, loc), loc, 0)
		if err != nil {
			return nil, err
		}
		if err := waiveDeadlineMet(ctx, txq, inv.ID, in, line, &caller, now); err != nil {
			return nil, err
		}
	}
	if _, refusal, err := insertCredit(ctx, txq, store.InsertCustomerCreditParams{
		CustomerID: inv.CustomerID, Currency: inv.Currency, Kind: creditOverpayment, CreditDay: line.BookedOn, Note: note,
		InvoiceID: &inv.ID, BankTransactionID: &line.ID, RegisteredByUserID: &caller, RegisteredAt: now,
	}, rest, cannotKeepAsCreditTitle); refusal != nil || err != nil {
		return refusal, err
	}
	return nil, resolveLine(ctx, txq, line, resolutionApplied, deref(line.Reason), eventKeptAsCredit, note, []string{lineException}, caller, now)
}

// PostInvoicesBankTransactionsByIdKeepAsCredit Keep a bank line's surplus
// as the customer's credit (POST /api/v1/invoices/bank-transactions/{id}/keep-as-credit)
func (s *server) PostInvoicesBankTransactionsByIdKeepAsCredit(ctx context.Context, req gen.PostInvoicesBankTransactionsByIdKeepAsCreditRequestObject) (gen.PostInvoicesBankTransactionsByIdKeepAsCreditResponseObject, error) {
	note, errs := queueNote(req.Body.Note)
	if errs != nil {
		return gen.PostInvoicesBankTransactionsByIdKeepAsCredit400ApplicationProblemPlusJSONResponse(invalid(invalidKeepTitle, errs)), nil
	}
	q := store.New(s.deps.Pool)
	invoiceID := req.Body.InvoiceId
	if invoiceID == nil {
		// The line's suggestion, read on the pool: a hint, judged again
		// under the locks.
		line, err := q.GetBankTransaction(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PostInvoicesBankTransactionsByIdKeepAsCredit404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("invoices: read bank line %d: %w", req.Id, err)
		}
		if refusal := judgeKeepable(line); refusal != nil {
			return gen.PostInvoicesBankTransactionsByIdKeepAsCredit409ApplicationProblemPlusJSONResponse(*refusal), nil
		}
		if invoiceID, err = suggestionOnQueue(ctx, q, line, deref(line.Reason)); err != nil {
			return nil, err
		}
		if invoiceID == nil {
			return gen.PostInvoicesBankTransactionsByIdKeepAsCredit400ApplicationProblemPlusJSONResponse(invalid(invalidKeepTitle,
				fieldError("invoiceId", "Name the invoice the line paid: it suggests none"))), nil
		}
	}
	caller, now := callerID(ctx), s.deps.Clock()
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	var refusal *gen.InvoicesConflictProblem
	for attempt := 0; attempt < 2; attempt++ {
		refusal, err = s.queueAction(ctx, req.Id, judgeKeepable, func(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error) {
			// The invoice's customer, read in the transaction without a
			// lock, then its credit lock: the second of I17a's order.
			inv, err := txq.GetInvoice(ctx, *invoiceID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ptr(conflict(codeAllocationNotAnInvoice, cannotKeepAsCreditTitle, fmt.Sprintf(
					"Document %d does not exist: a bank line's surplus is kept as the credit of an issued invoice's customer.", *invoiceID))), nil
			}
			if err != nil {
				return nil, fmt.Errorf("invoices: read document %d: %w", *invoiceID, err)
			}
			if err := lockCustomerCredit(ctx, txq, inv.CustomerID); err != nil {
				return nil, err
			}
			return s.keepLocked(ctx, txq, line, *invoiceID, inv.CustomerID, note, caller, now, loc)
		})
		if !errors.Is(err, errCustomerMoved) {
			break
		}
	}
	if resp, done, err := lineRefusal(refusal, err,
		func() gen.PostInvoicesBankTransactionsByIdKeepAsCreditResponseObject {
			return gen.PostInvoicesBankTransactionsByIdKeepAsCredit404Response{}
		},
		func(p gen.InvoicesConflictProblem) gen.PostInvoicesBankTransactionsByIdKeepAsCreditResponseObject {
			return gen.PostInvoicesBankTransactionsByIdKeepAsCredit409ApplicationProblemPlusJSONResponse(p)
		}); done {
		return resp, err
	}
	tx, err := oneLineResponse(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesBankTransactionsByIdKeepAsCredit200JSONResponse(tx), nil
}

// reversalCredits is a reversal's overpayments to take back (I9), read in
// its transaction after the line's lock and before any other: each an
// overpayment kept from a bank line — errNoSuchPayment otherwise — with
// what reversals took of it, and their customers in ascending id, whose
// credit locks the reversal takes next.
func reversalCredits(ctx context.Context, txq *store.Queries, ids []int64) ([]store.GetCustomerCreditRow, []int32, error) {
	rows := make([]store.GetCustomerCreditRow, 0, len(ids))
	var customers []int32
	for _, id := range ids {
		r, err := txq.GetCustomerCredit(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errNoSuchPayment
		}
		if err != nil {
			return nil, nil, fmt.Errorf("invoices: read credit movement %d: %w", id, err)
		}
		if r.InvoicesCustomerCredit.Kind != creditOverpayment || r.InvoicesCustomerCredit.BankTransactionID == nil {
			return nil, nil, errNoSuchPayment
		}
		rows = append(rows, r)
		if !slices.Contains(customers, r.InvoicesCustomerCredit.CustomerID) {
			customers = append(customers, r.InvoicesCustomerCredit.CustomerID)
		}
	}
	slices.Sort(customers)
	return rows, customers, nil
}
