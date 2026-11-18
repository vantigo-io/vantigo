package invoices

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
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
