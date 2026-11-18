package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
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
// credit serialise on it and each sees the other's commit. A registration
// that keeps the rest as credit, and a removal of a payment with credit, take
// the customer's credit lock first (invoices phase 6 design I17a,
// withCustomerAndInvoice).

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
// (the business day), the amount above 0 with at most two decimals and
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
// credit notes credit, what its live payments paid and what its live Vipps
// reservations hold (openAndReserved) — read with q, which holds the
// invoice's lock, or a plain read. The document's own figures (settle,
// document_state) do not see a reservation: it is not a payment (D2).
func openOf(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (*big.Rat, error) {
	open, _, err := openAndReserved(ctx, q, inv)
	return open, err
}

// vippsInProgress is the sentence a refusal adds when a live reservation is
// why less is open than the invoice's figures show (I4).
func vippsInProgress(reserved *big.Rat) string {
	return fmt.Sprintf("A Vipps payment of %s is being completed; try again once it is registered or released.", reserved.FloatString(2))
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
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	payment, errs, err := parsePayment(*req.Body, inv.IssueDate.Time, holidays.DayIn(s.deps.Clock(), loc))
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
	recovering := req.Body.Kind != nil && *req.Body.Kind == gen.InvoicesPaymentKindRecovered
	keep := req.Body.KeepRestAsCredit != nil && *req.Body.KeepRestAsCredit
	if keep && recovering {
		return gen.PostInvoicesByIdPayments400ApplicationProblemPlusJSONResponse(invalid(invalidPaymentTitle,
			fieldError("keepRestAsCredit", "Money recovered after a write-off is never kept as credit"))), nil
	}

	var refusal *gen.InvoicesConflictProblem
	register := func(ctx context.Context, txq *store.Queries, locked store.InvoicesInvoice) error {
		if paymentAfterLock != nil {
			paymentAfterLock(ctx, locked.ID)
		}
		if recovering {
			// Money after a write-off (R4): up to what is written off less
			// what is already recovered; the open amount stays as it is.
			writeOff, room, ok, err := recoveryRoom(ctx, txq, locked.ID)
			if err != nil {
				return err
			}
			switch {
			case !ok:
				refusal = ptr(conflict(codeNoWriteOff, cannotRegisterTitle,
					"No write-off of this invoice stands: register the money as an ordinary payment."))
				return errRefused
			case amt.Cmp(room) > 0:
				refusal = ptr(conflict(codeRecoveryExceedsWriteOff, cannotRegisterTitle,
					fmt.Sprintf("This payment is %s, and %s of the write-off is left to recover.", amt.FloatString(2), room.FloatString(2))))
				refusal.OpenAmount = ptr(floatFromRat(room, 2))
				return errRefused
			}
			payment.Kind, payment.WriteOffID = "recovered", &writeOff
		} else {
			payment.Kind = "payment"
		}
		open, reserved, err := openAndReserved(ctx, txq, locked)
		if err != nil {
			return err
		}
		switch {
		case recovering:
		case open.Sign() <= 0 && reserved.Sign() > 0:
			refusal = ptr(conflict(codeInvoiceSettled, cannotRegisterTitle,
				"Nothing else is open on this invoice. "+vippsInProgress(reserved)))
			return errRefused
		case open.Sign() <= 0:
			refusal = ptr(conflict(codeInvoiceSettled, cannotRegisterTitle,
				"Nothing is open on this invoice: its credit notes and payments cover its total."))
			return errRefused
		case amt.Cmp(open) > 0 && keep:
			// The rest is the customer's credit (I9): the payment takes what
			// is open, an overpayment beside it the rest.
			rest := new(big.Rat).Sub(amt, open)
			if payment.Amount, err = numericFromRat(open, 2); err != nil {
				return err
			}
			payment.InvoiceID, payment.Currency = locked.ID, locked.Currency
			payment.RegisteredAt, payment.RegisteredByUserID = s.deps.Clock(), ptr(callerID(ctx))
			if payment.AmountNok, err = amountNOKOf(locked, payment.Amount); err != nil {
				return err
			}
			registered, err := txq.InsertPayment(ctx, payment)
			if err != nil {
				return fmt.Errorf("invoices: register a payment against %d: %w", locked.ID, err)
			}
			nok, err := nokOfInvoice(locked, rest)
			if err != nil {
				return err
			}
			_, refusal, err = insertCredit(ctx, txq, store.InsertCustomerCreditParams{
				CustomerID: locked.CustomerID, Currency: locked.Currency, Kind: creditOverpayment, CreditDay: payment.PaidOn,
				Note: payment.Note, InvoiceID: &locked.ID, PaymentID: &registered.ID, RegisteredByUserID: payment.RegisteredByUserID,
				RegisteredAt: payment.RegisteredAt, AmountNok: nok,
			}, rest, cannotRegisterTitle)
			if refusal != nil {
				return errRefused
			}
			return err
		case amt.Cmp(open) > 0:
			detail := fmt.Sprintf("This payment is %s, and the invoice has %s open. An overpayment is not registered unless the rest is kept as credit.", amt.FloatString(2), open.FloatString(2))
			if reserved.Sign() > 0 {
				detail += " " + vippsInProgress(reserved)
			}
			refusal = ptr(conflict(codePaymentExceedsOpen, cannotRegisterTitle, detail))
			refusal.OpenAmount = ptr(floatFromRat(open, 2))
			return errRefused
		}
		payment.InvoiceID, payment.Currency = locked.ID, locked.Currency
		payment.RegisteredAt, payment.RegisteredByUserID = s.deps.Clock(), ptr(callerID(ctx))
		if payment.AmountNok, err = amountNOKOf(locked, payment.Amount); err != nil {
			return err
		}
		if _, err := txq.InsertPayment(ctx, payment); err != nil {
			return fmt.Errorf("invoices: register a payment against %d: %w", locked.ID, err)
		}
		return nil
	}
	if keep {
		// The customer's credit lock, then the invoice (I17a).
		err = s.withCustomerAndInvoice(ctx, inv.ID, register)
	} else {
		err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			// The invoice FOR UPDATE — the only row this takes — and every
			// figure after it, so a credit note's issue or another
			// registration that holds it has committed by the time they are
			// read.
			locked, err := txq.LockInvoice(ctx, inv.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return errDocumentGone
			}
			if err != nil {
				return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
			}
			return register(ctx, txq, locked)
		})
	}
	if refusal != nil {
		return gen.PostInvoicesByIdPayments409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdPayments404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, inv, nil, loc)
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
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}

	// A payment with credit — of source credit, or with an overpayment kept
	// beside it — or of an invoice whose credit note's surplus moved to the
	// customer writes credit: read on the pool, it takes the customer's
	// credit lock before the invoice (I17a). One found so only under the
	// invoice's lock runs again on that path.
	q := store.New(s.deps.Pool)
	withCredit, err := removalWritesCredit(ctx, q, req.Id, req.PaymentId)
	if err != nil {
		return nil, err
	}
	caller, now := callerID(ctx), s.deps.Clock()
	var refusal *gen.InvoicesConflictProblem
	var inv store.InvoicesInvoice
	remove := func(ctx context.Context, txq *store.Queries, locked store.InvoicesInvoice) error {
		inv = locked
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
		credits, err := txq.CreditsOfPayment(ctx, &payment.ID)
		if err != nil {
			return fmt.Errorf("invoices: read payment %d's credit: %w", payment.ID, err)
		}
		movedN, err := txq.MovedOf(ctx, inv.ID)
		if err != nil {
			return fmt.Errorf("invoices: read what moved off document %d: %w", inv.ID, err)
		}
		moved, err := ratFromNumeric(movedN)
		if err != nil {
			return err
		}
		if !withCredit && (len(credits) > 0 || payment.Source == paymentSourceCredit || moved.Sign() > 0) {
			return errCreditPathNeeded
		}
		n, err := txq.RemovePayment(ctx, store.RemovePaymentParams{
			RemovedAt: now, RemovedByUserID: caller, RemovalReason: reason, ID: payment.ID, InvoiceID: inv.ID,
		})
		if err != nil {
			return fmt.Errorf("invoices: remove payment %d of %d: %w", payment.ID, inv.ID, err)
		}
		if n != 1 {
			return fmt.Errorf("invoices: remove payment %d of %d: %d rows, want 1", payment.ID, inv.ID, n)
		}
		if !withCredit {
			return nil
		}
		day := holidays.DayIn(now, loc)
		for _, c := range credits {
			if _, refusal, err = reverseCredit(ctx, txq, c.InvoicesCustomerCredit, c.Reversed, nil, day, caller, now, cannotRemoveTitle); err != nil {
				return err
			}
			if refusal != nil {
				refusal.Detail = ptr(deref(refusal.Detail) + " The credit kept from this payment was used; remove what used it first.")
				return errRefused
			}
		}
		return returnMovedCredit(ctx, txq, inv, day, caller, now)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if withCredit {
			err = s.withCustomerAndInvoice(ctx, req.Id, remove)
		} else {
			err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
				// The invoice first, the payment after the lock: two
				// removals of one payment queue here, and the second reads
				// the first's removal.
				locked, err := txq.LockInvoice(ctx, req.Id)
				if errors.Is(err, pgx.ErrNoRows) {
					return errDocumentGone
				}
				if err != nil {
					return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
				}
				return remove(ctx, txq, locked)
			})
		}
		if !errors.Is(err, errCreditPathNeeded) {
			break
		}
		withCredit = true
	}
	if refusal != nil {
		return gen.PostInvoicesByIdPaymentsByPaymentIdRemove409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) || errors.Is(err, errNoSuchPayment) {
		return gen.PostInvoicesByIdPaymentsByPaymentIdRemove404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, inv, nil, loc)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdPaymentsByPaymentIdRemove200JSONResponse(resp), nil
}

// errCreditPathNeeded is a removal that found, under the invoice's lock, a
// payment or an invoice whose removal writes credit, which it had not read
// on the pool: it runs again with the customer's credit lock first.
var errCreditPathNeeded = errors.New("invoices: the removal writes credit")

// removalWritesCredit reports, read on the pool, whether removing payment
// paymentID of invoice id writes credit (I9-I11): one of source credit, one
// with a movement naming it, or one of an invoice whose credit note's
// surplus moved to the customer.
func removalWritesCredit(ctx context.Context, q *store.Queries, id, paymentID int64) (bool, error) {
	payment, err := q.GetPayment(ctx, store.GetPaymentParams{ID: paymentID, InvoiceID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("invoices: read payment %d of %d: %w", paymentID, id, err)
	}
	if payment.Source == paymentSourceCredit {
		return true, nil
	}
	credits, err := q.CreditsOfPayment(ctx, &payment.ID)
	if err != nil {
		return false, fmt.Errorf("invoices: read payment %d's credit: %w", payment.ID, err)
	}
	if len(credits) > 0 {
		return true, nil
	}
	moved, err := q.MovedOf(ctx, id)
	if err != nil {
		return false, fmt.Errorf("invoices: read what moved off document %d: %w", id, err)
	}
	m, err := ratFromNumeric(moved)
	if err != nil {
		return false, err
	}
	return m.Sign() > 0, nil
}
