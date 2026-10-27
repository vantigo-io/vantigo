package energy

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.InvoicedWorkHolder (energy consumption
// billing design D6, energy settlements plan reading 11, module-boundaries
// rule 10): the invoices issue stamps the settlements an invoice bills as
// invoiced, inside its own transaction, and the issue of the credit note that
// returns a settlement's line moves it to released, the stamp kept as
// history. A released period is forgotten by the live index, so the next
// Settle drafts it afresh.
//
// The holder runs on the caller's transaction and touches nothing else: no
// pool, no directory, no clock. Every timestamp it writes is ref.IssuedAt,
// and the recomputation it judges a draft by runs on the transaction with
// ref.IssuedAt as its now. The recomputation's own reads (intervals, prices,
// tariffs) take no locks: an interval written after the stamp is a
// correction's business.

// invoicedWorkAfterLock is called by both directions right after the
// settlements are locked, so a test can see the holder's locks held. nil in
// production.
var invoicedWorkAfterLock func(ctx context.Context)

// invoicedWorkHolder is this module's contracts.InvoicedWorkHolder. It holds
// a logger and nothing else: its constructor runs for a module that is
// switched off too, and every row it writes arrives through the caller's tx.
type invoicedWorkHolder struct {
	logger *slog.Logger
}

var _ contracts.InvoicedWorkHolder = (*invoicedWorkHolder)(nil)

// newInvoicedWorkHolder is Module's InvoicedWork. It keeps d.Logger only.
func newInvoicedWorkHolder(d module.Deps) contracts.InvoicedWorkHolder {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &invoicedWorkHolder{logger: logger}
}

// Kinds is the one kind this module holds: a settlement.
func (h *invoicedWorkHolder) Kinds() []contracts.WorkSourceKind {
	return []contracts.WorkSourceKind{contracts.WorkSourceSettlement}
}

// MarkInvoiced locks the sources' settlements by id ascending, judges each as
// it stands under the lock — missing, then already invoiced, then released or
// blocked, then no longer a period of its supply period (cancelled, or its
// start no longer a boundary: source_not_invoiceable), then recomputed over
// its period as the supply period now holds it (a blocker, or any figure
// other than the stored row's, its period's end among them), then the
// source's revision, currency and amount — and stamps them all with ref, or
// none: the first refusal answers and nothing is written.
func (h *invoicedWorkHolder) MarkInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	ids, err := sourceSettlementIDs(sources)
	if err != nil || len(ids) == 0 {
		return err
	}
	q := store.New(tx)
	byID, err := lockSourceSettlements(ctx, q, ids)
	if err != nil {
		return err
	}
	sorted := slices.Clone(sources)
	slices.SortFunc(sorted, func(a, b contracts.WorkSource) int { return compareInt64(a.ID, b.ID) })
	for _, source := range sorted {
		refusal, err := markSettlementRefusal(ctx, q, ref, source, byID)
		if err != nil {
			return err
		}
		if refusal != nil {
			return refusal
		}
	}
	params := store.StampSettlementsInvoicedParams{
		InvoiceID: &ref.ID, InvoiceNumber: &ref.Number, IssuedAt: ref.IssuedAt,
		InvoiceDate: pgtype.Date{Time: ref.IssueDate, Valid: true}, Ids: ids,
	}
	if ref.IssuedBy != uuid.Nil {
		by := ref.IssuedBy
		params.InvoicedByUserID = &by
	}
	stamped, err := q.StampSettlementsInvoiced(ctx, params)
	if err != nil {
		return fmt.Errorf("energy: stamp settlements invoiced by invoice %d: %w", ref.ID, err)
	}
	if len(stamped) != len(ids) {
		return fmt.Errorf("energy: stamped %d of %d settlements invoiced by invoice %d", len(stamped), len(ids), ref.ID)
	}
	return nil
}

// ReleaseInvoiced locks the sources' settlements exactly as MarkInvoiced
// does, before it writes, and moves every one carrying ref's stamp to
// released at ref.IssuedAt, the stamp kept. A source that does not carry the
// stamp — gone, released already, or stamped by another invoice — is left as
// it is and named in a warning: a credit note is never blocked.
func (h *invoicedWorkHolder) ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	ids, err := sourceSettlementIDs(sources)
	if err != nil || len(ids) == 0 {
		return err
	}
	q := store.New(tx)
	byID, err := lockSourceSettlements(ctx, q, ids)
	if err != nil {
		return err
	}
	release := make([]int64, 0, len(ids))
	for _, id := range ids {
		row, ok := byID[id]
		if ok && row.State == contracts.SettlementInvoiced && row.InvoicedInvoiceID != nil && *row.InvoicedInvoiceID == ref.ID {
			release = append(release, id)
			continue
		}
		var stampedBy any
		var state string
		if ok {
			state = row.State
			if row.InvoicedInvoiceID != nil {
				stampedBy = *row.InvoicedInvoiceID
			}
		}
		h.logger.WarnContext(ctx, "energy: a credit note releases a settlement that does not carry its invoice's stamp; left as it is",
			"settlement_id", id, "found", ok, "state", state, "invoice_id", ref.ID, "invoice_number", ref.Number,
			"stamped_by_invoice_id", stampedBy)
	}
	if len(release) == 0 {
		return nil
	}
	released, err := q.ReleaseSettlementsInvoiced(ctx, store.ReleaseSettlementsInvoicedParams{
		ReleasedAt: ref.IssuedAt, Ids: release, InvoiceID: &ref.ID,
	})
	if err != nil {
		return fmt.Errorf("energy: release settlements invoiced by invoice %d: %w", ref.ID, err)
	}
	if len(released) != len(release) {
		return fmt.Errorf("energy: released %d of %d settlements invoiced by invoice %d", len(released), len(release), ref.ID)
	}
	return nil
}

// sourceSettlementIDs is the sources' settlement ids ascending. A source of
// another kind, or one named twice, is the caller's bug, never a refusal.
func sourceSettlementIDs(sources []contracts.WorkSource) ([]int64, error) {
	ids := make([]int64, 0, len(sources))
	seen := make(map[int64]bool, len(sources))
	for _, s := range sources {
		if s.Kind != contracts.WorkSourceSettlement {
			return nil, fmt.Errorf("energy: the invoiced-work holder was handed a %q source; it holds %q only", s.Kind, contracts.WorkSourceSettlement)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("energy: the invoiced-work holder was handed settlement %d twice", s.ID)
		}
		seen[s.ID] = true
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	return ids, nil
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// lockSourceSettlements locks ids ascending (LockSettlementsByIDs) and calls
// the test seam with the locks held.
func lockSourceSettlements(ctx context.Context, q *store.Queries, ids []int64) (map[int64]store.EnergySettlement, error) {
	rows, err := q.LockSettlementsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("energy: lock the invoiced settlements: %w", err)
	}
	if invoicedWorkAfterLock != nil {
		invoicedWorkAfterLock(ctx)
	}
	byID := make(map[int64]store.EnergySettlement, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	return byID, nil
}

// markSettlementRefusal judges one source against its locked row, in plan
// reading 11's order.
func markSettlementRefusal(ctx context.Context, q *store.Queries, ref contracts.InvoiceRef, source contracts.WorkSource,
	byID map[int64]store.EnergySettlement) (*contracts.WorkSourceRefusal, error) {
	refuse := func(code, detail string) *contracts.WorkSourceRefusal {
		return &contracts.WorkSourceRefusal{Source: source, Code: code, Detail: detail}
	}
	row, ok := byID[source.ID]
	switch {
	case !ok:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Settlement %d was not found", source.ID)), nil
	case row.State == contracts.SettlementInvoiced:
		return refuse(contracts.SourceAlreadyInvoiced, fmt.Sprintf("Settlement %d is already invoiced", source.ID)), nil
	case row.State != contracts.SettlementDraft:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Settlement %d is %s", source.ID, row.State)), nil
	case row.Blocked != nil:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Settlement %d is blocked: %s", source.ID, *row.Blocked)), nil
	}
	stored, err := readSettlements(ctx, q, []store.EnergySettlement{row})
	if err != nil {
		return nil, err
	}
	subjects, err := loadSettlementSubjects(ctx, q, []int32{row.SupplyPeriodID})
	if err != nil {
		return nil, err
	}
	subj := subjects[row.SupplyPeriodID]
	if _, ok, err := currentPeriodOf(subj, row.PeriodStart, ref.IssuedAt); err != nil {
		return nil, err
	} else if !ok {
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Settlement %d's period is no longer a period of its supply period", source.ID)), nil
	}
	recomputed, present, err := recomputeDraft(ctx, q, stored[0], subj, ref.IssuedAt)
	if err != nil {
		return nil, err
	}
	switch {
	case !present:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Settlement %d has nothing left to correct", source.ID)), nil
	case recomputed.Blocked != "":
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Settlement %d is now blocked: %s", source.ID, recomputed.Blocked)), nil
	case !sameFigures(recomputed, stored[0]):
		return refuse(contracts.SourceChanged, fmt.Sprintf("Settlement %d's figures changed since it was read", source.ID)), nil
	case row.Revision != source.Revision:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Settlement %d is at revision %d, not %d", source.ID, row.Revision, source.Revision)), nil
	case stored[0].Currency != source.Currency:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Settlement %d bills in %q, not %q", source.ID, stored[0].Currency, source.Currency)), nil
	}
	want, ok := new(big.Rat).SetString(source.Amount)
	if !ok {
		return nil, fmt.Errorf("energy: settlement %d's source amount %q is not a decimal", source.ID, source.Amount)
	}
	if stored[0].Amount.Cmp(want) != 0 {
		return refuse(contracts.SourceChanged, fmt.Sprintf("Settlement %d bills %s, not %s", source.ID, ratText(stored[0].Amount, amountPlaces), source.Amount)), nil
	}
	return nil, nil
}
