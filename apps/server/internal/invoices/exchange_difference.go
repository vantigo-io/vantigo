package invoices

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the exchange difference (invoices phase 6 design I24; plan
// C8 and reading 5): the three rules that keep an invoice's NOK receivable
// (Σ BaseNOK + Σ VATNOK, I21) matching what is open in its currency.
//
//   - A movement that leaves the invoice partly open clears
//     round(amount × the document's rate) of the receivable.
//   - The movement that brings the currency open amount to zero clears the
//     NOK receivable still open, so no phantom øre is left on it.
//   - A payment's exchange difference is what arrived in NOK less what it
//     cleared: positive a gain (8060), negative a loss (8160). A write-off or
//     a credit row moves no money: its amount_nok is the NOK it cleared.
//
// For NOK every figure is the amount and every difference 0.

// clearedNOK is the NOK receivable a movement of amount clears on an
// invoice at rate, given what stays open in its currency after it and the
// NOK receivable open before it.
func clearedNOK(rate, amount, openAfter, openNOKBefore *big.Rat) *big.Rat {
	if openAfter.Sign() <= 0 {
		return new(big.Rat).Set(openNOKBefore)
	}
	// A partial clear never takes more than is open in NOK, so open_nok()
	// never goes below zero mid-life however the rows rounded.
	cleared := docmath.Round2(new(big.Rat).Mul(amount, rate))
	if openNOKBefore.Sign() >= 0 && cleared.Cmp(openNOKBefore) > 0 {
		return new(big.Rat).Set(openNOKBefore)
	}
	return cleared
}

// nokShare is part of a whole's NOK: round(totalNOK × part / whole), the
// whole NOK when part is the whole.
func nokShare(totalNOK, part, whole *big.Rat) *big.Rat {
	if whole.Sign() == 0 || part.Cmp(whole) == 0 {
		return new(big.Rat).Set(totalNOK)
	}
	return docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(totalNOK, part), whole))
}

// openNOKOf is invoices.open_nok() of an invoice, read on q — under its lock
// when q holds one, as every movement reads it.
func openNOKOf(ctx context.Context, q *store.Queries, invoiceID int64) (*big.Rat, error) {
	n, err := q.OpenNOK(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the NOK open on %d: %w", invoiceID, err)
	}
	return ratFromNumeric(n)
}

// movementNOK is what a movement of amount on locked clears in NOK
// (clearedNOK), read under its lock: open is its currency open amount
// before the movement. For NOK it is the amount itself.
func movementNOK(ctx context.Context, q *store.Queries, locked store.InvoicesInvoice, amount, open *big.Rat) (*big.Rat, error) {
	if locked.Currency == "NOK" {
		return new(big.Rat).Set(amount), nil
	}
	rate, err := ratFromNumeric(locked.ExchangeRate)
	if err != nil {
		return nil, err
	}
	openNOK, err := openNOKOf(ctx, q, locked.ID)
	if err != nil {
		return nil, err
	}
	return clearedNOK(rate, amount, new(big.Rat).Sub(open, amount), openNOK), nil
}

// paymentNOK is a payment's amount_nok and exchange difference: arrived is
// what came in NOK, nil for NOK (the amount itself), and cleared what the
// payment cleared of the receivable (movementNOK).
func paymentNOK(arrived, cleared *big.Rat) (amountNOK, difference pgtype.Numeric, err error) {
	if arrived == nil {
		arrived = cleared
	}
	if amountNOK, err = numericFromRat(arrived, 2); err != nil {
		return
	}
	difference, err = numericFromRat(new(big.Rat).Sub(arrived, cleared), 2)
	return
}

// closingDifference writes the NOK rest an invoice in another currency is
// left with when a credit note issued on day brings it to zero open in its
// currency (review M1), under the issue's lock on the original. Nothing when
// something is still open, or the NOK is at zero too.
func closingDifference(ctx context.Context, q *store.Queries, originalID, creditNoteID int64, day, now time.Time) error {
	original, err := q.GetInvoice(ctx, originalID)
	if err != nil {
		return fmt.Errorf("invoices: read the original %d: %w", originalID, err)
	}
	open, err := openOf(ctx, q, original)
	if err != nil || open.Sign() != 0 {
		return err
	}
	rest, err := openNOKOf(ctx, q, originalID)
	if err != nil || rest.Sign() == 0 {
		return err
	}
	n, err := numericFromRat(rest, 2)
	if err != nil {
		return err
	}
	if err := q.InsertExchangeDifference(ctx, store.InsertExchangeDifferenceParams{
		InvoiceID: originalID, DocumentID: creditNoteID, DifferenceDay: pgDate(day), AmountNok: n, RegisteredAt: now,
	}); err != nil {
		return fmt.Errorf("invoices: record the NOK rest of %d: %w", originalID, err)
	}
	return nil
}

// recoveryNOK is what a recovered payment of amount clears of its invoice's
// standing write-off in NOK (review M2): the write-off's NOK times what all
// live recoveries will have recovered over its amount, less what the earlier
// ones cleared — so the recovery that recovers the whole write-off clears
// exactly its NOK, and a ledger's VAT given back never exceeds what the
// write-off took. Read under the invoice's lock.
func recoveryNOK(ctx context.Context, q *store.Queries, invoiceID int64, amount *big.Rat) (*big.Rat, error) {
	wo, err := q.LiveWriteOff(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the write-off of %d: %w", invoiceID, err)
	}
	total, err := ratFromNumeric(wo.Amount)
	if err != nil {
		return nil, err
	}
	totalNOK, err := ratFromNumeric(wo.AmountNok)
	if err != nil {
		return nil, err
	}
	prior, err := q.LiveRecoveriesNOK(ctx, &wo.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the recoveries of write-off %d: %w", wo.ID, err)
	}
	before, err := ratFromNumeric(prior.Amount)
	if err != nil {
		return nil, err
	}
	clearedBefore, err := ratFromNumeric(prior.ClearedNok)
	if err != nil {
		return nil, err
	}
	after := new(big.Rat).Add(before, amount)
	worth := totalNOK
	if after.Cmp(total) < 0 && total.Sign() != 0 {
		worth = docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(totalNOK, after), total))
	}
	return new(big.Rat).Sub(worth, clearedBefore), nil
}
