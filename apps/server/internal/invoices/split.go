package invoices

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the automatic split and the small difference (invoices phase
// 6 design I14, I15, as the plan's readings 10 to 12 amend them).
//
// The split: before matching queues a line no_kid, kid_unknown or
// exceeds_open, it reads what the line's remittance names (referencesOf) on
// the pool. When that is two or more issued NOK invoices of one customer,
// none written off or credited in full, each with something open, no Vipps
// payment in progress on any, paid into the account each printed and booked
// on or after each was issued, whose open principals add up to the line's
// amount exactly, one transaction takes the line FOR NO KEY UPDATE and the
// invoices FOR UPDATE in descending id (D18), judges all of it again under
// the locks — and the soft key — and registers one payment per invoice of
// what it has open, the oldest due first, as a match does: by the file's
// uploader or the caller of …/match, by no one for the bank feed
// (ck_payments_origin). The line is matched with an auto_split event.
// Charges are never paid by a split. Any doubt queues the line with the
// reason it had, as before.
//
// The small difference: after a payment is registered — by hand, by a match
// or by an apply that takes its whole line — roundOffResidual writes a
// rounding write-off of what is left open when that is above zero and at
// most the tolerance, and the payment covered at least 95 % of what was open
// before it, in the same transaction, under the invoice's lock. Never after
// a Vipps capture, a recovery, credit applied or a split, which pays in full.
// Removing or reversing a payment of the invoice undoes it
// (undoAutoRoundings).

// eventAutoSplit is a line matching applied across several invoices.
const eventAutoSplit = "auto_split"

// splitBeforeLocks, when a test sets it (export_test.go's
// SetSplitBeforeLocks), runs after the pool's reading found a split and
// before its transaction takes a lock, with the line's id, so a test can
// change what the locks will find; an error stops the split. Nil in
// production.
var splitBeforeLocks func(ctx context.Context, bankTransactionID int64) error

// splittable reports whether a line queued for reason is first tried as a
// split (I14).
func splittable(reason string) bool {
	switch reason {
	case reasonNoKid, reasonKidUnknown, reasonExceedsOpen:
		return true
	}
	return false
}

// seriesOf is this installation's invoice numbers, read on the pool.
func seriesOf(ctx context.Context, q *store.Queries) (invoiceSeries, error) {
	row, err := q.GetSettings(ctx)
	if err != nil {
		return invoiceSeries{}, fmt.Errorf("invoices: read the settings: %w", err)
	}
	next, _, err := nextNumber(ctx, q, row.SeriesStart)
	if err != nil {
		return invoiceSeries{}, err
	}
	return invoiceSeries{first: row.SeriesStart, next: next}, nil
}

// splitCandidates is the issued invoices line's remittance names, read with
// q: each KID's invoice when the KID is exactly the one it carries, and each
// invoice number's, every invoice once, by id.
func splitCandidates(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction) ([]int64, error) {
	series, err := seriesOf(ctx, q)
	if err != nil {
		return nil, err
	}
	refs := referencesOf(line, series)
	var ids []int64
	for _, k := range refs.kids {
		probe := line
		probe.Kid = &k
		inv, _, err := invoiceOfKid(ctx, q, probe)
		if err != nil {
			return nil, err
		}
		if inv != nil && inv.Kind == kindInvoice && !slices.Contains(ids, inv.ID) {
			ids = append(ids, inv.ID)
		}
	}
	if len(refs.numbers) > 0 {
		// One more than a split takes: more invoices named is no split.
		invs, err := q.IssuedInvoicesByNumbers(ctx, refs.numbers)
		if err != nil {
			return nil, fmt.Errorf("invoices: read the invoices line %d names: %w", line.ID, err)
		}
		for _, inv := range invs {
			if !slices.Contains(ids, inv.ID) {
				ids = append(ids, inv.ID)
			}
		}
	}
	slices.Sort(ids)
	if len(ids) > maxSplitInvoices {
		// More invoices named than a split takes: the line is queued, with
		// them among its suggestions.
		return nil, nil
	}
	return ids, nil
}

// maxSplitInvoices is the most invoices one automatic split pays; a line
// naming more is queued (IssuedInvoicesByNumbers reads one more).
const maxSplitInvoices = 20

// undoAutoRoundings undoes every standing automatic rounding write-off of
// invoice invoiceID, which txq holds FOR UPDATE, by and at, with the reason
// "payment removed": the payment whose residual it rounded off is gone
// (I15), whether a person removed it or a reversal took it back, and what it
// left open is open again.
func undoAutoRoundings(ctx context.Context, txq *store.Queries, invoiceID int64, by uuid.UUID, at time.Time) error {
	if _, err := txq.UndoAutoWriteOffs(ctx, store.UndoAutoWriteOffsParams{
		UndoneAt: at, UndoneByUserID: by, InvoiceID: invoiceID,
	}); err != nil {
		return fmt.Errorf("invoices: undo the automatic rounding of %d: %w", invoiceID, err)
	}
	return nil
}

// splitShare is one invoice of a split and what it has open.
type splitShare struct {
	inv  store.InvoicesInvoice
	open *big.Rat
}

// judgeSplit is I14's rule for line against invs, read with q — on the pool
// to decide whether to try, under the locks to decide: the shares, oldest
// due first, or nil for any doubt. invs are what splitCandidates named; a
// line naming fewer than two is no split.
func judgeSplit(ctx context.Context, q *store.Queries, line store.InvoicesBankTransaction, invs []store.InvoicesInvoice) ([]splitShare, error) {
	if len(invs) < 2 {
		return nil, nil
	}
	if line.Kid != nil {
		if dup, err := softKeyRegistered(ctx, q, line); err != nil || dup {
			return nil, err
		}
	}
	amount, err := ratFromNumeric(line.Amount)
	if err != nil {
		return nil, err
	}
	shares := make([]splitShare, 0, len(invs))
	sum := new(big.Rat)
	for _, inv := range invs {
		if inv.Kind != kindInvoice || inv.Status != statusIssued || inv.Currency != "NOK" ||
			inv.CustomerID != invs[0].CustomerID ||
			inv.SellerBankAccount == nil || *inv.SellerBankAccount != line.Account ||
			line.BookedOn.Time.Before(inv.IssueDate.Time) {
			return nil, nil
		}
		credited, _, err := uncredited(ctx, q, inv)
		if err != nil {
			return nil, err
		}
		gross, err := ratFromNumeric(inv.GrossTotal)
		if err != nil {
			return nil, err
		}
		if credited.Sign() > 0 && credited.Cmp(gross) >= 0 {
			return nil, nil
		}
		if writtenOff, err := writtenOffOf(ctx, q, inv.ID); err != nil || writtenOff {
			return nil, err
		}
		open, reserved, err := openAndReserved(ctx, q, inv)
		if err != nil {
			return nil, err
		}
		if open.Sign() <= 0 || reserved.Sign() > 0 {
			return nil, nil
		}
		shares = append(shares, splitShare{inv: inv, open: open})
		sum.Add(sum, open)
	}
	if sum.Cmp(amount) != 0 {
		return nil, nil
	}
	slices.SortFunc(shares, func(a, b splitShare) int {
		return cmp.Or(compareDates(a.inv.DueDate, b.inv.DueDate), cmp.Compare(deref64(a.inv.Number), deref64(b.inv.Number)))
	})
	return shares, nil
}

// compareDates orders two due dates, one without a date last.
func compareDates(a, b pgtype.Date) int {
	switch {
	case a.Valid && b.Valid:
		return a.Time.Compare(b.Time)
	case a.Valid:
		return -1
	case b.Valid:
		return 1
	}
	return 0
}

// trySplit is I14 for a line classification or the match would queue for
// reason, with suggested its suggestion (0 for none): the split, when the
// pool's reading says one may be made and the locks confirm it — matched —
// else outcomeNone and nothing written, for the caller to queue it as
// before. A line no longer pending under its lock is done: outcomeDone.
func (s *server) trySplit(ctx context.Context, line store.InvoicesBankTransaction, caller *uuid.UUID, now time.Time, loc *time.Location) (string, error) {
	q := store.New(s.deps.Pool)
	ids, err := splitCandidates(ctx, q, line)
	if err != nil || len(ids) < 2 {
		return outcomeNone, err
	}
	invs := make([]store.InvoicesInvoice, 0, len(ids))
	for _, id := range ids {
		inv, err := q.GetInvoice(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return outcomeNone, nil
		}
		if err != nil {
			return outcomeNone, fmt.Errorf("invoices: read document %d: %w", id, err)
		}
		invs = append(invs, inv)
	}
	if shares, err := judgeSplit(ctx, q, line, invs); err != nil || shares == nil {
		return outcomeNone, err
	}
	if hook := splitBeforeLocks; hook != nil {
		if err := hook(ctx, line.ID); err != nil {
			return outcomeNone, err
		}
	}
	outcome := outcomeNone
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockBankTransaction(ctx, txq, line.ID)
		if err != nil {
			return fmt.Errorf("invoices: lock line %d: %w", line.ID, err)
		}
		if locked.Status != "pending" {
			outcome = outcomeDone
			return nil
		}
		invs, err := lockInvoicesDescending(ctx, txq, ids)
		if err != nil {
			return err
		}
		if len(invs) != len(ids) {
			return nil
		}
		shares, err := judgeSplit(ctx, txq, locked, invs)
		if err != nil || shares == nil {
			return err
		}
		reference := deref(locked.Kid)
		for _, sh := range shares {
			n, err := numericFromRat(sh.open, 2)
			if err != nil {
				return err
			}
			nok, err := amountNOKOf(sh.inv, n)
			if err != nil {
				return err
			}
			if _, err := txq.InsertImportedPayment(ctx, store.InsertImportedPaymentParams{
				InvoiceID: sh.inv.ID, PaidOn: locked.BookedOn, Amount: n, AmountNok: nok, Currency: sh.inv.Currency,
				Source: paymentSourceOf(locked.Format), BankTransactionID: locked.ID, Reference: reference,
				RegisteredByUserID: caller, RegisteredAt: now,
			}); err != nil {
				return fmt.Errorf("invoices: register line %d's payment against %d: %w", locked.ID, sh.inv.ID, err)
			}
			in, err := s.ruleInputLocked(ctx, txq, sh.inv, holidays.DayIn(now, loc), loc, 0)
			if err != nil {
				return err
			}
			if err := waiveDeadlineMet(ctx, txq, sh.inv.ID, in, locked, caller, now); err != nil {
				return err
			}
		}
		n, err := txq.MarkMatched(ctx, locked.ID)
		if err != nil {
			return fmt.Errorf("invoices: mark line %d matched: %w", locked.ID, err)
		}
		if n != 1 {
			return fmt.Errorf("invoices: mark line %d matched: %d rows, want 1", locked.ID, n)
		}
		if err := txq.InsertBankTransactionEvent(ctx, store.InsertBankTransactionEventParams{
			BankTransactionID: locked.ID, Event: eventAutoSplit, ByUserID: caller, At: now,
		}); err != nil {
			return fmt.Errorf("invoices: record line %d split: %w", locked.ID, err)
		}
		outcome = outcomeMatched
		return nil
	})
	if err != nil {
		return outcomeNone, err
	}
	return outcome, nil
}

// roundOffResidual is I15 for inv, which txq holds FOR UPDATE, right after a
// payment of paid, paid on day, was registered: when what is left open of
// its principal is above zero and, in NOK at the document's rate, at most
// the tolerance — read in this transaction, so a change applies from the
// next payment on — and the payment covered at least 95 % of what was open
// just before it (plan reading 14: a test transfer of a krone, or an
// instalment, never closes an invoice), a rounding write-off of it, source
// auto, by no one, on day, at now. Nothing while a Vipps payment is in
// progress on it, nothing with the tolerance at 0.
func roundOffResidual(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice, paid *big.Rat, day pgtype.Date, now time.Time) error {
	settings, err := txq.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("invoices: read the settings: %w", err)
	}
	tolerance, err := ratFromNumeric(settings.Tolerance)
	if err != nil || tolerance.Sign() <= 0 {
		return err
	}
	open, reserved, err := openAndReserved(ctx, txq, inv)
	if err != nil {
		return err
	}
	if open.Sign() <= 0 || reserved.Sign() > 0 {
		return nil
	}
	// paid ≥ 95 % of (open + paid), in whole numbers: 100·paid ≥ 95·before.
	before := new(big.Rat).Add(open, paid)
	if new(big.Rat).Mul(paid, big.NewRat(100, 1)).Cmp(new(big.Rat).Mul(before, big.NewRat(95, 1))) < 0 {
		return nil
	}
	amount, err := numericFromRat(open, 2)
	if err != nil {
		return err
	}
	nok, err := amountNOKOf(inv, amount)
	if err != nil {
		return err
	}
	residual, err := ratFromNumeric(nok)
	if err != nil {
		return err
	}
	if residual.Cmp(tolerance) > 0 {
		return nil
	}
	if _, err := txq.InsertWriteOff(ctx, store.InsertWriteOffParams{
		InvoiceID: inv.ID, WrittenOffOn: day, Amount: amount, AmountNok: nok, Currency: inv.Currency,
		Reason: writeOffRounding, Source: writeOffAuto, RegisteredAt: now,
	}); err != nil {
		return fmt.Errorf("invoices: round off what is left of %d: %w", inv.ID, err)
	}
	return nil
}
