package invoices

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// mandateClaimOf is the mandate rule (invoices phase 6 design I32), run
// before D4's step 5: a line whose KID is in the customer KIDs' space and is
// an AvtaleGiro mandate's resolves to that customer's accepted claim due on
// the booking day — or on a day a weekend or holiday moved the debit from —
// with the line's amount, and from the claim to its invoice. handled is
// false for any other KID, and for a mandate's KID that no claim fits —
// those take today's path, invoiceOfKid, which finds an invoice issued before
// the customer KIDs' space was reserved, or queues kid_unknown. Two claims
// that fit queue kid_unknown. The claim moves to paid when registerMatch
// registers the payment, in the match's transaction.
func mandateClaimOf(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction) (reason string, invoiceID int64, handled bool, err error) {
	if line.Kid == nil || !kid.IsCustomerKID(*line.Kid) {
		return "", 0, false, nil
	}
	if _, err := q.MandateByKID(ctx, *line.Kid); errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil
	} else if err != nil {
		return "", 0, true, fmt.Errorf("invoices: read the mandate of line %d: %w", line.ID, err)
	}
	claims, err := q.ClaimsForMatch(ctx, store.ClaimsForMatchParams{
		Kid: *line.Kid, Amount: line.Amount, DueDates: debitDueDates(line.BookedOn.Time),
	})
	if err != nil {
		return "", 0, true, fmt.Errorf("invoices: read the claims of line %d: %w", line.ID, err)
	}
	switch len(claims) {
	case 0:
		// No claim fits: an invoice issued before the customer KIDs' space
		// was reserved may carry this KID, so today's path decides.
		return "", 0, false, nil
	case 1:
		return "", claims[0].InvoiceID, true, nil
	default:
		return reasonKidUnknown, 0, true, nil
	}
}

// mandateHolds reports whether line's KID is in the customer KIDs' space and
// an AvtaleGiro mandate's. Such a line is an AvtaleGiro debit: it pays its
// claim's invoice or is queued for a person, and is never tried as an
// automatic split (I14), which would pay invoices the debit was not for and
// leave the claim unpaid.
func mandateHolds(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction) (bool, error) {
	if line.Kid == nil || !kid.IsCustomerKID(*line.Kid) {
		return false, nil
	}
	if _, err := q.MandateByKID(ctx, *line.Kid); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("invoices: read the mandate of line %d: %w", line.ID, err)
	}
	return true, nil
}

// payClaimOfDebit moves invoice invoiceID's claim on line's customer KID to
// paid — the debit came — and publishes invoices.avtalegiro.claim_changed,
// on the locked transaction ctx carries (I32, I33): a match, a queue apply
// and a keep-as-credit all pay it so. A line on any other KID pays none.
func payClaimOfDebit(ctx context.Context, txq *store.Queries, line store.InvoicesBankTransaction, invoiceID int64, now time.Time) error {
	if line.Kid == nil || !kid.IsCustomerKID(*line.Kid) {
		return nil
	}
	paid, err := txq.PayClaimOfInvoice(ctx, store.PayClaimOfInvoiceParams{Now: now, InvoiceID: invoiceID, Kid: *line.Kid})
	if err != nil {
		return fmt.Errorf("invoices: mark document %d's claim paid: %w", invoiceID, err)
	}
	if len(paid) == 0 {
		return nil
	}
	tx, err := lockedTxOf(ctx)
	if err != nil {
		return err
	}
	for _, c := range paid {
		if err := publishClaimChanged(ctx, tx, c, "paid", now); err != nil {
			return err
		}
	}
	return nil
}

// claimDebitOf reports whether line, queued against invoice invoiceID, is
// the AvtaleGiro debit of a claim of that invoice: a mandate's customer KID
// and a claim on it the debit would pay.
func claimDebitOf(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction, invoiceID int64) (bool, error) {
	if invoiceID == 0 {
		return false, nil
	}
	if held, err := mandateHolds(ctx, q, line); err != nil || !held {
		return false, err
	}
	ok, err := q.HasPayableClaim(ctx, store.HasPayableClaimParams{InvoiceID: invoiceID, Kid: *line.Kid})
	if err != nil {
		return false, fmt.Errorf("invoices: read document %d's claim: %w", invoiceID, err)
	}
	return ok, nil
}

// settleClaimDebit is matching's last step for a line it queued: when the
// line is the debit of a claim of invoiceID (claimDebitOf), what the invoice
// has open is applied and the rest kept as the customer's credit, as
// keep-as-credit does, by the file's uploader or the caller of …/match, and
// the claim is paid — outcomeMatched. A line keep-as-credit refuses, or one
// of the bank feed's sync, which keeps no credit without a person, stays
// queued for a person; its claim is paid all the same, so the worker never
// calls it unpaid. Any other line stays as it was queued.
func (s *server) settleClaimDebit(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction, invoiceID int64,
	caller *uuid.UUID, now time.Time, loc *time.Location,
) (string, error) {
	debit, err := claimDebitOf(ctx, q, line, invoiceID)
	if err != nil || !debit {
		return outcomeQueued, err
	}
	if caller != nil {
		refusal, err := s.queueAction(ctx, line.ID, judgeKeepable, func(ctx context.Context, txq *store.Queries, locked store.InvoicesBankTransaction) (*gen.InvoicesConflictProblem, error) {
			inv, err := txq.GetInvoice(ctx, invoiceID)
			if err != nil {
				return nil, fmt.Errorf("invoices: read document %d: %w", invoiceID, err)
			}
			if err := lockCustomerCredit(ctx, txq, inv.CustomerID); err != nil {
				return nil, err
			}
			return s.keepLocked(ctx, txq, locked, invoiceID, inv.CustomerID, "", *caller, now, loc, foreignKeep{})
		})
		switch {
		case err == nil && refusal == nil:
			return outcomeMatched, nil
		case err != nil && !errors.Is(err, errCustomerMoved):
			return outcomeQueued, err
		}
	}
	return outcomeQueued, s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		return payClaimOfDebit(ctx, txq, line, invoiceID, now)
	})
}

// debitDueDates are the due dates a debit booked on booked may be for: the
// day itself, and every day before it the bank moved to it — a weekend or a
// holiday whose next business day is booked.
func debitDueDates(booked time.Time) []pgtype.Date {
	day := utcDay(booked)
	out := []pgtype.Date{pgDate(day)}
	for back := 1; back <= 5; back++ {
		d := day.AddDate(0, 0, -back)
		if holidays.BusinessDay(d) || !holidays.NextBusinessDay(d).Equal(day) {
			break
		}
		out = append(out, pgDate(d))
	}
	return out
}
