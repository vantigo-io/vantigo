package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the payments (payments and delivery design D2): money received
// against an issued invoice, registered and — with a reason — removed, never
// edited or deleted. Both writes read no directory: the customer may be
// disabled, archived, merged or anonymised since, and the money arrived
// regardless. Each judges its fields before anything is read under a lock,
// then takes one lock — the invoice, FOR UPDATE — and reads every figure it
// decides on after it: a credit note's issue locks the same row (its
// original, the last lock of the issue's order), so a registration and a
// credit serialise on it and each sees the other's commit.

// The codes and titles of the payments' refusals.
const (
	codeCreditNoteNoPayments = "credit_note_no_payments"
	codeInvoiceSettled       = "invoice_settled"
	codePaymentExceedsOpen   = "payment_exceeds_open"
	codePaymentRemoved       = "payment_removed"

	cannotRegisterTitle = "The payment cannot be registered"
	cannotRemoveTitle   = "The payment cannot be removed"
	invalidPaymentTitle = "Invalid payment"
	invalidRemovalTitle = "Invalid payment removal"
)

// paymentAfterLock is called inside both payment writes' transactions right
// after the invoice is locked, so a race test can hold one write there while
// another waits on the lock. nil in production.
var paymentAfterLock func(ctx context.Context, invoiceID int64)

// errNoSuchPayment is a removal naming a payment that is not the invoice's.
var errNoSuchPayment = errors.New("invoices: no such payment on the invoice")

// parsePayment runs D2's field rules over a registration body, every failure
// collected: paidOn on or after the invoice's issue date and not after today
// (the Oslo business day), the amount above 0 with at most two decimals and
// within the document bound, the reference and the note within their columns.
// It answers the row less what the handler fills in — the invoice, the
// currency, who and when.
func parsePayment(req gen.InvoicesPaymentRequest, issueDate, today time.Time) (store.InsertPaymentParams, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	p := store.InsertPaymentParams{Reference: optionalText(req.Reference), Note: optionalText(req.Note)}
	paidOn := utcDay(req.PaidOn.Time)
	switch {
	case req.PaidOn.IsZero():
		// Absent from the body, it decodes as the zero day, which the floor
		// below would answer with a message about the issue date.
		add("paidOn", "A payment needs the day it was received")
	case paidOn.Before(issueDate):
		add("paidOn", "A payment is received on or after the invoice's issue date, "+issueDate.Format(time.DateOnly))
	case paidOn.After(today):
		add("paidOn", "A payment is received today at the latest, "+today.Format(time.DateOnly))
	}
	p.PaidOn = pgDate(paidOn)
	amt, msg := amount("An amount", req.Amount, 2, zero, true, maxGrossTotal)
	add("amount", msg)
	if amt != nil {
		// amount() has held it to two decimals within the bound, so this
		// cannot fail on the caller's input: a failure is the server's.
		n, err := numericFromRat(amt, 2)
		if err != nil {
			return store.InsertPaymentParams{}, nil, fmt.Errorf("invoices: a payment's amount as numeric: %w", err)
		}
		p.Amount = n
	}
	add("reference", maxLength("A reference", p.Reference, 100))
	add("note", maxLength("The note", p.Note, 500))
	return p, errs, nil
}

// openOf is what is open on an issued invoice — gross less what its issued
// credit notes credit and what its live payments paid — read with q, which
// holds the invoice's lock, or a plain read.
func openOf(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (*big.Rat, error) {
	_, left, err := uncredited(ctx, q, inv)
	if err != nil {
		return nil, err
	}
	sum, err := q.LivePaymentsSum(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what document %d is paid: %w", inv.ID, err)
	}
	paid, err := ratFromNumeric(sum)
	if err != nil {
		return nil, err
	}
	return left.Sub(left, paid), nil
}

// PostInvoicesByIdPayments Register a payment
// (POST /api/v1/invoices/{id}/payments)
func (s *server) PostInvoicesByIdPayments(ctx context.Context, req gen.PostInvoicesByIdPaymentsRequestObject) (gen.PostInvoicesByIdPaymentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdPayments404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	// The kind first, as the credit endpoint decides it: a credit-note draft
	// told "it is a draft" would be issued and still take no payment.
	switch {
	case inv.Kind == kindCreditNote:
		return gen.PostInvoicesByIdPayments409ApplicationProblemPlusJSONResponse(conflict(codeCreditNoteNoPayments, cannotRegisterTitle,
			"A payment is registered against an invoice, never a credit note.")), nil
	case inv.Status != statusIssued:
		return gen.PostInvoicesByIdPayments409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceDraft, cannotRegisterTitle,
			"A draft is not a sales document: issue it before registering a payment against it.")), nil
	}
	payment, errs, err := parsePayment(*req.Body, inv.IssueDate.Time, businessDay(s.deps.Clock()))
	if err != nil {
		return nil, err
	}
	if errs != nil {
		return gen.PostInvoicesByIdPayments400ApplicationProblemPlusJSONResponse(invalid(invalidPaymentTitle, errs)), nil
	}
	amt, err := ratFromNumeric(payment.Amount)
	if err != nil {
		return nil, err
	}

	var refusal *gen.InvoicesConflictProblem
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		// The invoice FOR UPDATE — the only row this takes — and every
		// figure after it, so a credit note's issue or another registration
		// that holds it has committed by the time they are read.
		locked, err := txq.LockInvoice(ctx, inv.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
		}
		if paymentAfterLock != nil {
			paymentAfterLock(ctx, locked.ID)
		}
		open, err := openOf(ctx, txq, locked)
		if err != nil {
			return err
		}
		switch {
		case open.Sign() <= 0:
			refusal = ptr(conflict(codeInvoiceSettled, cannotRegisterTitle,
				"Nothing is open on this invoice: its credit notes and payments cover its total."))
			return errRefused
		case amt.Cmp(open) > 0:
			refusal = ptr(conflict(codePaymentExceedsOpen, cannotRegisterTitle, fmt.Sprintf(
				"This payment is %s, and the invoice has %s open. An overpayment is not registered.", amt.FloatString(2), open.FloatString(2))))
			refusal.OpenAmount = ptr(floatFromRat(open, 2))
			return errRefused
		}
		payment.InvoiceID, payment.Currency = locked.ID, locked.Currency
		payment.RegisteredAt, payment.RegisteredByUserID = s.deps.Clock(), ptr(callerID(ctx))
		if _, err := txq.InsertPayment(ctx, payment); err != nil {
			return fmt.Errorf("invoices: register a payment against %d: %w", locked.ID, err)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesByIdPayments409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdPayments404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, inv, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdPayments200JSONResponse(resp), nil
}

// PostInvoicesByIdPaymentsByPaymentIdRemove Remove a payment registration
// (POST /api/v1/invoices/{id}/payments/{paymentId}/remove)
func (s *server) PostInvoicesByIdPaymentsByPaymentIdRemove(ctx context.Context, req gen.PostInvoicesByIdPaymentsByPaymentIdRemoveRequestObject) (gen.PostInvoicesByIdPaymentsByPaymentIdRemoveResponseObject, error) {
	reason := strings.TrimSpace(req.Body.Reason)
	var errs map[string][]string
	if reason == "" {
		errs = fieldError("reason", "A removal needs a reason")
	} else if msg := maxLength("A reason", reason, 200); msg != "" {
		errs = fieldError("reason", msg)
	}
	if errs != nil {
		return gen.PostInvoicesByIdPaymentsByPaymentIdRemove400ApplicationProblemPlusJSONResponse(invalid(invalidRemovalTitle, errs)), nil
	}

	var refusal *gen.InvoicesConflictProblem
	var inv store.InvoicesInvoice
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		// The invoice first, the payment after the lock: two removals of one
		// payment queue here, and the second reads the first's removal.
		var err error
		inv, err = txq.LockInvoice(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		if paymentAfterLock != nil {
			paymentAfterLock(ctx, inv.ID)
		}
		payment, err := txq.GetPayment(ctx, store.GetPaymentParams{ID: req.PaymentId, InvoiceID: inv.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoSuchPayment
		}
		if err != nil {
			return fmt.Errorf("invoices: read payment %d of %d: %w", req.PaymentId, inv.ID, err)
		}
		if payment.RemovedAt != nil {
			refusal = ptr(conflict(codePaymentRemoved, cannotRemoveTitle,
				"This payment registration is already removed. A removal is never undone; register the payment again."))
			return errRefused
		}
		n, err := txq.RemovePayment(ctx, store.RemovePaymentParams{
			RemovedAt: s.deps.Clock(), RemovedByUserID: callerID(ctx), RemovalReason: reason, ID: payment.ID, InvoiceID: inv.ID,
		})
		if err != nil {
			return fmt.Errorf("invoices: remove payment %d of %d: %w", payment.ID, inv.ID, err)
		}
		if n != 1 {
			return fmt.Errorf("invoices: remove payment %d of %d: %d rows, want 1", payment.ID, inv.ID, n)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesByIdPaymentsByPaymentIdRemove409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) || errors.Is(err, errNoSuchPayment) {
		return gen.PostInvoicesByIdPaymentsByPaymentIdRemove404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, store.New(s.deps.Pool), inv, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdPaymentsByPaymentIdRemove200JSONResponse(resp), nil
}
