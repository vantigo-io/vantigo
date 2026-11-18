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

// This file is the write-off (accounting design A16 as amended by R4): an
// issued invoice's whole open amount given up on a day with a reason, undone
// with a reason, never edited or deleted, and the money that arrives after it
// — a payment of kind recovered (payments.go), allowed up to the amount
// written off. A standing write-off settles the principal as a payment does
// (invoices.settled_amount, migration 00182), so a written-off invoice is not
// open, not overdue and never reminded. Each write is a fact in the sales
// journal a ledger posts from; nothing here knows of a ledger. They are under
// invoices:payments, "what the company says it is owed", as registering and
// removing a payment are.
//
// Both writes take the one lock the payments take — the invoice, FOR UPDATE
// — and read every figure after it.

// The codes and titles of the write-off's refusals.
const (
	codeVippsInProgress         = "vipps_in_progress"
	codeNoWriteOff              = "no_write_off"
	codeWriteOffRecovered       = "write_off_recovered"
	codeRecoveryExceedsWriteOff = "recovery_exceeds_written_off"
	// codeInvoiceWrittenOff refuses a credit note of an invoice a write-off
	// stands on, its draft's creation and save as its issue (accounting
	// design R15): the customer would be shown a refund of money never paid,
	// and a ledger would take the VAT back twice.
	codeInvoiceWrittenOff = "invoice_written_off"

	cannotWriteOffTitle  = "The invoice cannot be written off"
	cannotUndoTitle      = "The write-off cannot be undone"
	invalidWriteOffTitle = "Invalid write-off"
)

// The write-off's reasons on the wire and in the table.
const (
	writeOffLoss     = "loss"
	writeOffRounding = "rounding"
	writeOffOther    = "other"
)

// A write-off's sources (ck_write_offs_source): a person's, or the rounding
// write-off a payment leaves by itself (invoices phase 6 design I15).
const (
	writeOffManual = "manual"
	writeOffAuto   = "auto"
)

// PostInvoicesByIdWriteOff Write off an invoice's open amount
// (POST /api/v1/invoices/{id}/write-off)
func (s *server) PostInvoicesByIdWriteOff(ctx context.Context, req gen.PostInvoicesByIdWriteOffRequestObject) (gen.PostInvoicesByIdWriteOffResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdWriteOff404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	switch {
	case inv.Kind == kindCreditNote:
		return gen.PostInvoicesByIdWriteOff409ApplicationProblemPlusJSONResponse(conflict(codeCreditNoteNoPayments, cannotWriteOffTitle,
			"A credit note is never written off: write off the invoice it credits.")), nil
	case inv.Status != statusIssued:
		return gen.PostInvoicesByIdWriteOff409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceDraft, cannotWriteOffTitle,
			"A draft is not a sales document: there is nothing to write off.")), nil
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	b := *req.Body
	var errs map[string][]string
	day := utcDay(b.WrittenOffOn.Time)
	today := holidays.DayIn(s.deps.Clock(), loc)
	switch {
	case b.WrittenOffOn.IsZero():
		errs = withFieldError(errs, "writtenOffOn", "A write-off needs its day")
	case day.Before(inv.IssueDate.Time):
		errs = withFieldError(errs, "writtenOffOn", "A write-off is made on or after the invoice's issue date, "+inv.IssueDate.Time.Format(time.DateOnly))
	case day.After(today):
		errs = withFieldError(errs, "writtenOffOn", "A write-off is made today at the latest, "+today.Format(time.DateOnly))
	}
	reason := string(b.Reason)
	switch reason {
	case writeOffLoss, writeOffRounding, writeOffOther:
	default:
		errs = withFieldError(errs, "reason", "loss, rounding or other")
	}
	vatCorrection := b.VatCorrection != nil && *b.VatCorrection
	if vatCorrection && reason != writeOffLoss {
		errs = withFieldError(errs, "vatCorrection", "Only a loss takes its VAT back (mval. § 4-7)")
	}
	if errs != nil {
		return gen.PostInvoicesByIdWriteOff400ApplicationProblemPlusJSONResponse(invalid(invalidWriteOffTitle, errs)), nil
	}

	var refusal *gen.InvoicesConflictProblem
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := txq.LockInvoice(ctx, inv.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
		}
		open, reserved, err := openAndReserved(ctx, txq, locked)
		if err != nil {
			return err
		}
		switch {
		case reserved.Sign() > 0:
			refusal = ptr(conflict(codeVippsInProgress, cannotWriteOffTitle, vippsInProgress(reserved)))
			return errRefused
		case open.Sign() <= 0:
			refusal = ptr(conflict(codeInvoiceSettled, cannotWriteOffTitle,
				"Nothing is open on this invoice: its credit notes, payments and any write-off cover its total."))
			return errRefused
		}
		amount, err := numericFromRat(open, 2)
		if err != nil {
			return err
		}
		nok, err := amountNOKOf(locked, amount)
		if err != nil {
			return err
		}
		if _, err := txq.InsertWriteOff(ctx, store.InsertWriteOffParams{
			InvoiceID: locked.ID, WrittenOffOn: pgDate(day), Amount: amount, AmountNok: nok, Currency: locked.Currency, Reason: reason,
			VatCorrection: vatCorrection, Source: writeOffManual, RegisteredByUserID: ptr(callerID(ctx)), RegisteredAt: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("invoices: write off %d: %w", locked.ID, err)
		}
		return nil
	})
	if refusal != nil {
		return gen.PostInvoicesByIdWriteOff409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdWriteOff404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, inv, nil, loc)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdWriteOff200JSONResponse(resp), nil
}

// PostInvoicesByIdWriteOffUndo Undo an invoice's write-off
// (POST /api/v1/invoices/{id}/write-off/undo)
func (s *server) PostInvoicesByIdWriteOffUndo(ctx context.Context, req gen.PostInvoicesByIdWriteOffUndoRequestObject) (gen.PostInvoicesByIdWriteOffUndoResponseObject, error) {
	reason := strings.TrimSpace(req.Body.Reason)
	var errs map[string][]string
	if reason == "" {
		errs = fieldError("reason", "An undo needs a reason")
	} else if msg := maxLength("A reason", reason, 200); msg != "" {
		errs = fieldError("reason", msg)
	}
	if errs != nil {
		return gen.PostInvoicesByIdWriteOffUndo400ApplicationProblemPlusJSONResponse(invalid(invalidRemovalTitle, errs)), nil
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	// An invoice whose credit note's surplus moved to the customer gets it
	// back when the undo reopens it (plan reading 8): read on the pool, that
	// takes the customer's credit lock before the invoice (I17a), and one
	// found so only under the invoice's lock runs again on that path.
	q := store.New(s.deps.Pool)
	movedN, err := q.MovedOf(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what moved off document %d: %w", req.Id, err)
	}
	moved, err := ratFromNumeric(movedN)
	if err != nil {
		return nil, err
	}
	withCredit := moved.Sign() > 0
	caller, now := callerID(ctx), s.deps.Clock()
	var refusal *gen.InvoicesConflictProblem
	var inv store.InvoicesInvoice
	undo := func(ctx context.Context, txq *store.Queries, locked store.InvoicesInvoice) error {
		inv = locked
		if !withCredit {
			movedN, err := txq.MovedOf(ctx, inv.ID)
			if err != nil {
				return fmt.Errorf("invoices: read what moved off document %d: %w", inv.ID, err)
			}
			if m, err := ratFromNumeric(movedN); err != nil {
				return err
			} else if m.Sign() > 0 {
				return errCreditPathNeeded
			}
		}
		live, err := txq.LiveWriteOff(ctx, inv.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			refusal = ptr(conflict(codeNoWriteOff, cannotUndoTitle, "No write-off of this invoice stands."))
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: read the write-off of %d: %w", inv.ID, err)
		}
		recovered, err := txq.LiveRecoveries(ctx, live.ID)
		if err != nil {
			return fmt.Errorf("invoices: read the recoveries of write-off %d: %w", live.ID, err)
		}
		if r, err := ratFromNumeric(recovered); err != nil {
			return err
		} else if r.Sign() > 0 {
			refusal = ptr(conflict(codeWriteOffRecovered, cannotUndoTitle,
				"Money was recovered after this write-off. Remove the recovered payments first, then undo the write-off and register them as payments."))
			return errRefused
		}
		n, err := txq.UndoWriteOff(ctx, store.UndoWriteOffParams{UndoneAt: now, UndoneByUserID: caller,
			UndoReason: reason, ID: live.ID, InvoiceID: inv.ID})
		if err != nil {
			return fmt.Errorf("invoices: undo write-off %d of %d: %w", live.ID, inv.ID, err)
		}
		if n != 1 {
			return fmt.Errorf("invoices: undo write-off %d of %d: %d rows, want 1", live.ID, inv.ID, n)
		}
		if !withCredit {
			return nil
		}
		return returnMovedCredit(ctx, txq, inv, holidays.DayIn(now, loc), caller, now)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if withCredit {
			err = s.withCustomerAndInvoice(ctx, req.Id, undo)
		} else {
			err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
				locked, err := txq.LockInvoice(ctx, req.Id)
				if errors.Is(err, pgx.ErrNoRows) {
					return errDocumentGone
				}
				if err != nil {
					return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
				}
				return undo(ctx, txq, locked)
			})
		}
		if !errors.Is(err, errCreditPathNeeded) {
			break
		}
		withCredit = true
	}
	if refusal != nil {
		return gen.PostInvoicesByIdWriteOffUndo409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdWriteOffUndo404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, store.New(s.deps.Pool), inv, nil, loc)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdWriteOffUndo200JSONResponse(resp), nil
}

// writtenOffOf reports whether a write-off of the invoice stands, read with
// q, which holds the invoice's lock.
func writtenOffOf(ctx context.Context, q *store.Queries, invoiceID int64) (bool, error) {
	_, err := q.LiveWriteOff(ctx, invoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("invoices: read the write-off of %d: %w", invoiceID, err)
	}
	return true, nil
}

// lossWrittenOffOf reports whether a write-off that is no rounding stands on
// the invoice, read with q, which holds the invoice's lock: what refuses a
// bank line as invoice_written_off (plan reading 11). A rounding write-off
// is a small difference, not a loss: money after it is a late residual,
// which falls through to invoice_settled and a person keeps as credit (I15).
func lossWrittenOffOf(ctx context.Context, q *store.Queries, invoiceID int64) (bool, error) {
	live, err := q.LiveWriteOff(ctx, invoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("invoices: read the write-off of %d: %w", invoiceID, err)
	}
	return live.Reason != writeOffRounding, nil
}

// recoveryRoom is what a write-off has left to recover — its amount less its
// live recoveries — read with q, which holds the invoice's lock. ok is false
// when no write-off stands; rounding is true when the one standing is a
// rounding write-off, which money is never recovered against (plan reading
// 11: a recovery is for a loss or other).
func recoveryRoom(ctx context.Context, q *store.Queries, invoiceID int64) (writeOffID int64, room *big.Rat, rounding, ok bool, err error) {
	live, err := q.LiveWriteOff(ctx, invoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, false, false, nil
	}
	if err != nil {
		return 0, nil, false, false, fmt.Errorf("invoices: read the write-off of %d: %w", invoiceID, err)
	}
	amount, err := ratFromNumeric(live.Amount)
	if err != nil {
		return 0, nil, false, false, err
	}
	sum, err := q.LiveRecoveries(ctx, live.ID)
	if err != nil {
		return 0, nil, false, false, fmt.Errorf("invoices: read the recoveries of write-off %d: %w", live.ID, err)
	}
	recovered, err := ratFromNumeric(sum)
	if err != nil {
		return 0, nil, false, false, err
	}
	return live.ID, amount.Sub(amount, recovered), live.Reason == writeOffRounding, true, nil
}

// writeOffsResponse is every write-off of an invoice, undone ones included,
// with what each has recovered.
func writeOffsResponse(ctx context.Context, q *store.Queries, invoiceID int64) (*[]gen.InvoicesWriteOff, error) {
	rows, err := q.WriteOffsOf(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's write-offs: %w", invoiceID, err)
	}
	out := make([]gen.InvoicesWriteOff, 0, len(rows))
	for _, w := range rows {
		amount, err := ratFromNumeric(w.Amount)
		if err != nil {
			return nil, err
		}
		sum, err := q.LiveRecoveries(ctx, w.ID)
		if err != nil {
			return nil, fmt.Errorf("invoices: read the recoveries of write-off %d: %w", w.ID, err)
		}
		recovered, err := ratFromNumeric(sum)
		if err != nil {
			return nil, err
		}
		out = append(out, gen.InvoicesWriteOff{
			Id: w.ID, WrittenOffOn: wireDate(w.WrittenOffOn.Time), Amount: floatFromRat(amount, 2), Currency: w.Currency,
			Reason: gen.InvoicesWriteOffReason(w.Reason), VatCorrection: w.VatCorrection, RecoveredAmount: floatFromRat(recovered, 2),
			Source: gen.InvoicesWriteOffSource(w.Source), RegisteredAt: w.RegisteredAt, RegisteredByUserId: w.RegisteredByUserID,
			UndoneAt: w.UndoneAt, UndoneByUserId: w.UndoneByUserID, UndoReason: w.UndoReason,
		})
	}
	return &out, nil
}
