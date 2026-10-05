package expenses

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"sync/atomic"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is module-boundaries rule 10 on this module's side (invoices work
// design D1): the invoices issue stamps the expense lines it invoiced, inside
// its own transaction, and the credit note that returns a line in full takes
// the stamp back. It is the third track of invoiced.go's: the line is invoiced
// exactly as a manual mark makes it — invoiced_at, by whom — and also carries
// the invoice's id and number, which is what tells the two apart. A line the
// invoices module stamped is not the manual door's to undo (invoiced.go).
//
// The holder keeps rule 8's rules and rule 10's: it runs on the caller's
// transaction and never on a pool, reads no directory and no clock (every
// timestamp is ref.IssuedAt), and locks in this module's own order — the
// claims of the claim lines by id, then every line by id (claims.go) — which
// is Expenses' place in contracts.InvoicedWorkOrder. The period lock does not
// apply, as it does not to the manual mark.

// invoicedWorkHolder is this module's contracts.InvoicedWorkHolder. It holds
// the logger and nothing else, so a disabled module's Deps build it too.
type invoicedWorkHolder struct {
	logger *slog.Logger
}

var _ contracts.InvoicedWorkHolder = (*invoicedWorkHolder)(nil)

// newInvoicedWorkHolder is Module's InvoicedWork.
func newInvoicedWorkHolder(d module.Deps) contracts.InvoicedWorkHolder {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &invoicedWorkHolder{logger: logger}
}

// invoicedWorkAfterLock is the tests' seam: called right after the holder's
// locks, in both directions, with the context the holder runs under — so a
// test can see it marked as this module's locked transaction. Nil outside the
// tests that set it (export_test.go).
var invoicedWorkAfterLock atomic.Pointer[func(ctx context.Context)]

// Kinds is the one kind this holder stamps.
func (h *invoicedWorkHolder) Kinds() []contracts.WorkSourceKind {
	return []contracts.WorkSourceKind{contracts.WorkSourceExpense}
}

// lockedLines is what the holder's locks answer: every named line that still
// exists, by id, and the claims it locked first.
type lockedLines struct {
	lines map[int64]store.ExpensesEntry
	// readClaim is the claim each line belonged to when it was read before
	// the locks; claims is the claims locked, by id.
	readClaim map[int64]*int64
	claims    map[int64]store.ExpensesClaim
}

// lock takes the module's locks for ids, in the module's order: the claims of
// the claim lines by id (LockClaims), then exactly the named lines by id
// (LockBatchEntries with no claim ids, so no other line of a claim is held).
// ctx comes back marked as this module's locked transaction.
func (h *invoicedWorkHolder) lock(ctx context.Context, txq *store.Queries, ids []int64) (context.Context, lockedLines, error) {
	ctx = context.WithValue(ctx, lockedTxKey{}, true)
	read, err := txq.EntryClaims(ctx, ids)
	if err != nil {
		return ctx, lockedLines{}, fmt.Errorf("expenses: read the invoiced lines' claims: %w", err)
	}
	locked := lockedLines{
		lines:     make(map[int64]store.ExpensesEntry, len(ids)),
		readClaim: make(map[int64]*int64, len(read)),
		claims:    map[int64]store.ExpensesClaim{},
	}
	var claimIDs []int64
	for _, r := range read {
		locked.readClaim[r.ID] = r.ClaimID
		if r.ClaimID != nil && !slices.Contains(claimIDs, *r.ClaimID) {
			claimIDs = append(claimIDs, *r.ClaimID)
		}
	}
	if len(claimIDs) > 0 {
		slices.Sort(claimIDs)
		claims, err := txq.LockClaims(ctx, claimIDs)
		if err != nil {
			return ctx, lockedLines{}, fmt.Errorf("expenses: lock the invoiced lines' claims: %w", err)
		}
		for _, c := range claims {
			locked.claims[c.ID] = c
		}
	}
	rows, err := txq.LockBatchEntries(ctx, store.LockBatchEntriesParams{ClaimIds: []int64{}, EntryIds: ids})
	if err != nil {
		return ctx, lockedLines{}, fmt.Errorf("expenses: lock the invoiced lines: %w", err)
	}
	for _, r := range rows {
		locked.lines[r.ID] = r
	}
	if hook := invoicedWorkAfterLock.Load(); hook != nil {
		(*hook)(ctx)
	}
	return ctx, locked, nil
}

// claimHeld reports whether row's claim is the one read before the locks and
// is locked: a line that moved between the read and the lock — which no door
// of this module can do — would otherwise be judged under a claim the holder
// does not hold.
func (l lockedLines) claimHeld(row store.ExpensesEntry) bool {
	read, known := l.readClaim[row.ID]
	switch {
	case !known:
		return false
	case row.ClaimID == nil:
		return read == nil
	case read == nil || *read != *row.ClaimID:
		return false
	}
	_, held := l.claims[*row.ClaimID]
	return held
}

// unitStatus is the status the line is judged by: its claim's for a claim's
// line, its own otherwise (unitOf in authorize.go).
func (l lockedLines) unitStatus(row store.ExpensesEntry) string {
	if row.ClaimID != nil {
		if c, ok := l.claims[*row.ClaimID]; ok {
			return c.Status
		}
	}
	return row.Status
}

// sourceIDs checks sources are this holder's kind, none named twice, and
// answers their ids in ascending order.
func sourceIDs(sources []contracts.WorkSource) ([]int64, map[int64]contracts.WorkSource, error) {
	ids := make([]int64, 0, len(sources))
	byID := make(map[int64]contracts.WorkSource, len(sources))
	for _, s := range sources {
		if s.Kind != contracts.WorkSourceExpense {
			return nil, nil, fmt.Errorf("expenses: the invoiced work holder was handed a %q source", s.Kind)
		}
		if _, twice := byID[s.ID]; twice {
			return nil, nil, fmt.Errorf("expenses: the invoiced work holder was handed expense %d twice", s.ID)
		}
		byID[s.ID] = s
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	return ids, byID, nil
}

// MarkInvoiced judges every source under the locks, in id order, and stamps
// them all or none: the first refusal answers, and nothing is written.
func (h *invoicedWorkHolder) MarkInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	ids, byID, err := sourceIDs(sources)
	if err != nil || len(ids) == 0 {
		return err
	}
	txq := store.New(tx)
	ctx, locked, err := h.lock(ctx, txq, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if refusal, err := judgeInvoicedSource(byID[id], locked); refusal != nil || err != nil {
			if err != nil {
				return err
			}
			return refusal
		}
	}
	stamped, err := txq.StampEntriesInvoicedByInvoice(ctx, store.StampEntriesInvoicedByInvoiceParams{
		IssuedAt: ref.IssuedAt, IssuedBy: ref.IssuedBy, InvoiceID: ref.ID, InvoiceNumber: ref.Number, Ids: ids,
	})
	if err != nil {
		return fmt.Errorf("expenses: stamp the invoiced lines: %w", err)
	}
	if len(stamped) != len(ids) {
		return fmt.Errorf("expenses: stamped %d of the %d invoiced lines, each already judged under its lock", len(stamped), len(ids))
	}
	return nil
}

// judgeInvoicedSource is design D1's order for one line: already invoiced
// first — so a line marked by hand is named for what it is — then no longer
// ready to invoice, then changed since the draft read it. Changed is the
// billing facts alone, never the revision: a payroll reimbursement bumps it
// and changes nothing billed. The amount is compared by value.
func judgeInvoicedSource(src contracts.WorkSource, locked lockedLines) (*contracts.WorkSourceRefusal, error) {
	refuse := func(code, detail string) *contracts.WorkSourceRefusal {
		return &contracts.WorkSourceRefusal{Source: src, Code: code, Detail: detail}
	}
	row, found := locked.lines[src.ID]
	switch {
	case !found:
		return refuse(contracts.SourceNotInvoiceable, "This expense no longer exists"), nil
	case row.InvoicedInvoiceID != nil:
		return refuse(contracts.SourceAlreadyInvoiced,
			fmt.Sprintf("This expense has already been invoiced on invoice %d", *row.InvoicedNumber)), nil
	case row.InvoicedAt != nil && row.InvoiceReference != nil:
		return refuse(contracts.SourceAlreadyInvoiced,
			fmt.Sprintf("This expense has already been marked invoiced, with the reference %q", *row.InvoiceReference)), nil
	case row.InvoicedAt != nil:
		return refuse(contracts.SourceAlreadyInvoiced, "This expense has already been marked invoiced"), nil
	}
	// A line whose claim is not the one locked cannot be judged by its unit's
	// status at all, so it is changed before it is anything else.
	if !locked.claimHeld(row) {
		return refuse(contracts.SourceChanged, "This expense moved to another travel claim while it was being invoiced"), nil
	}
	if _, msg := invoicedRefusal(row, entryUnit{Status: locked.unitStatus(row)}, invoicedMark{}); msg != "" {
		return refuse(contracts.SourceNotInvoiceable, msg), nil
	}
	want, ok := new(big.Rat).SetString(src.Amount)
	if !ok {
		return nil, fmt.Errorf("expenses: expense %d's invoiced amount %q is not a decimal", src.ID, src.Amount)
	}
	bill, err := ratFromNumeric(row.BillAmount)
	if err != nil {
		return nil, err
	}
	switch {
	case bill.Cmp(want) != 0:
		return refuse(contracts.SourceChanged,
			fmt.Sprintf("This expense now bills %s, not %s", decimalText(bill, moneyPlaces), src.Amount)), nil
	case row.ProjectID == nil || *row.ProjectID != src.ProjectID:
		return refuse(contracts.SourceChanged, "This expense is now booked on another project"), nil
	case row.Currency != src.Currency:
		return refuse(contracts.SourceChanged,
			fmt.Sprintf("This expense is now in %s, not %s", row.Currency, src.Currency)), nil
	case row.Kind != src.ExpenseKind:
		return refuse(contracts.SourceChanged,
			fmt.Sprintf("This expense is now a %s, not a %s", row.Kind, src.ExpenseKind)), nil
	}
	return nil, nil
}

// ReleaseInvoiced takes ref's stamp back off sources, under the same locks in
// the same order as MarkInvoiced, before it writes. It never refuses: a line
// that no longer carries ref's stamp is left as it is and logged, because a
// credit note must never be blocked by it.
func (h *invoicedWorkHolder) ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	ids, _, err := sourceIDs(sources)
	if err != nil || len(ids) == 0 {
		return err
	}
	txq := store.New(tx)
	ctx, locked, err := h.lock(ctx, txq, ids)
	if err != nil {
		return err
	}
	release := make([]int64, 0, len(ids))
	for _, id := range ids {
		row, found := locked.lines[id]
		if !found || row.InvoicedInvoiceID == nil || *row.InvoicedInvoiceID != ref.ID {
			h.logger.WarnContext(ctx, "expenses: a credit note releases an expense that does not carry its invoice's stamp; left as it is",
				slog.Int64("expense_id", id), slog.Int64("invoice_id", ref.ID), slog.Int64("invoice_number", ref.Number),
				slog.Bool("found", found))
			continue
		}
		if !locked.claimHeld(row) {
			h.logger.WarnContext(ctx, "expenses: a credit note releases an expense whose travel claim moved while it was being locked",
				slog.Int64("expense_id", id), slog.Int64("invoice_id", ref.ID))
		}
		release = append(release, id)
	}
	if len(release) == 0 {
		return nil
	}
	released, err := txq.ReleaseEntriesInvoicedByInvoice(ctx, store.ReleaseEntriesInvoicedByInvoiceParams{
		ReleasedAt: ref.IssuedAt, Ids: release, InvoiceID: ref.ID,
	})
	if err != nil {
		return fmt.Errorf("expenses: release the credited lines: %w", err)
	}
	if len(released) != len(release) {
		return fmt.Errorf("expenses: released %d of the %d credited lines, each found stamped under its lock", len(released), len(release))
	}
	return nil
}
