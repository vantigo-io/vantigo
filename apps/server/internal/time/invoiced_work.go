package timetracking

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the module's contracts.InvoicedWorkHolder (invoices work
// design D1, module-boundaries rule 10): the invoices issue stamps the
// entries an invoice bills as invoiced, inside its own transaction, and the
// issue of the credit note that returns an entry's line takes the stamp
// back. It is the one writer of the invoiced state, and the release is the
// one way out of it: invoiced → approved exists nowhere else, and the manual
// approval operations still refuse an invoiced entry (approval.go).
//
// The holder runs on the caller's transaction and touches nothing else: no
// pool, no directory, no clock. Every timestamp it writes is ref.IssuedAt.
// The period lock applies to neither direction — a stamp is not an edit of
// the hours, and the lock is how a month is closed before it is invoiced.

// invoicedWorkAfterLock is called by both directions right after the entries
// are locked, so a test can see the holder's context and its locks. nil in
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

// Kinds is the one kind this module holds: a time entry.
func (h *invoicedWorkHolder) Kinds() []contracts.WorkSourceKind {
	return []contracts.WorkSourceKind{contracts.WorkSourceHours}
}

// MarkInvoiced locks the sources' entries in id order (LockEntries, the order
// every batch of this module keeps), judges each as it stands under the lock
// — already invoiced, then no longer invoiceable, then changed since the
// draft read it — and stamps them all with ref, or none: the first refusal
// answers and nothing is written.
func (h *invoicedWorkHolder) MarkInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	ids, err := sourceEntryIDs(sources)
	if err != nil || len(ids) == 0 {
		return err
	}
	ctx = context.WithValue(ctx, lockedTxKey{}, true)
	txq := store.New(tx)
	byID, err := lockSourceEntries(ctx, txq, ids)
	if err != nil {
		return err
	}
	for _, source := range sortedSources(sources) {
		refusal, err := markRefusal(source, byID)
		if err != nil {
			return err
		}
		if refusal != nil {
			return refusal
		}
	}
	stamped, err := txq.StampEntriesInvoiced(ctx, store.StampEntriesInvoicedParams{
		IssuedAt:      ref.IssuedAt,
		InvoiceID:     ref.ID,
		InvoiceNumber: ref.Number,
		Ids:           ids,
	})
	if err != nil {
		return fmt.Errorf("time: stamp entries invoiced by invoice %d: %w", ref.ID, err)
	}
	if len(stamped) != len(ids) {
		return fmt.Errorf("time: stamped %d of %d entries invoiced by invoice %d", len(stamped), len(ids), ref.ID)
	}
	return nil
}

// ReleaseInvoiced locks the sources' entries exactly as MarkInvoiced does,
// before it writes, and moves every one carrying ref's stamp back to
// approved, the approval stamps kept. A source that does not carry the stamp
// — gone, or stamped by another invoice or by hand — is left as it is and
// named in a warning: a credit note is never blocked.
func (h *invoicedWorkHolder) ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	ids, err := sourceEntryIDs(sources)
	if err != nil || len(ids) == 0 {
		return err
	}
	ctx = context.WithValue(ctx, lockedTxKey{}, true)
	txq := store.New(tx)
	byID, err := lockSourceEntries(ctx, txq, ids)
	if err != nil {
		return err
	}
	release := make([]int64, 0, len(ids))
	for _, id := range ids {
		row, ok := byID[id]
		if ok && row.InvoicedInvoiceID != nil && *row.InvoicedInvoiceID == ref.ID {
			release = append(release, id)
			continue
		}
		var stampedBy any
		if ok && row.InvoicedInvoiceID != nil {
			stampedBy = *row.InvoicedInvoiceID
		}
		h.logger.WarnContext(ctx, "time: a credit note releases an entry that does not carry its invoice's stamp; left as it is",
			"entry_id", id, "found", ok, "invoice_id", ref.ID, "invoice_number", ref.Number, "stamped_by_invoice_id", stampedBy)
	}
	if len(release) == 0 {
		return nil
	}
	released, err := txq.ReleaseEntriesInvoiced(ctx, store.ReleaseEntriesInvoicedParams{
		ReleasedAt: ref.IssuedAt,
		Ids:        release,
		InvoiceID:  ref.ID,
	})
	if err != nil {
		return fmt.Errorf("time: release entries invoiced by invoice %d: %w", ref.ID, err)
	}
	if len(released) != len(release) {
		return fmt.Errorf("time: released %d of %d entries invoiced by invoice %d", len(released), len(release), ref.ID)
	}
	return nil
}

// sourceEntryIDs is the sources' entry ids in id order. A source of another
// kind, or one named twice, is the caller's bug, never a refusal.
func sourceEntryIDs(sources []contracts.WorkSource) ([]int64, error) {
	ids := make([]int64, 0, len(sources))
	seen := make(map[int64]bool, len(sources))
	for _, s := range sources {
		if s.Kind != contracts.WorkSourceHours {
			return nil, fmt.Errorf("time: the invoiced-work holder was handed a %q source; it holds %q only", s.Kind, contracts.WorkSourceHours)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("time: the invoiced-work holder was handed entry %d twice", s.ID)
		}
		seen[s.ID] = true
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	return ids, nil
}

// sortedSources is sources in entry id order, so the first refusal is the
// same whatever order the caller gave them in.
func sortedSources(sources []contracts.WorkSource) []contracts.WorkSource {
	sorted := slices.Clone(sources)
	slices.SortFunc(sorted, func(a, b contracts.WorkSource) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	return sorted
}

// lockSourceEntries locks ids in id order and calls the test seam with the
// locks held.
func lockSourceEntries(ctx context.Context, txq *store.Queries, ids []int64) (map[int64]store.TimeEntry, error) {
	rows, err := txq.LockEntries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("time: lock the invoiced entries: %w", err)
	}
	if invoicedWorkAfterLock != nil {
		invoicedWorkAfterLock(ctx)
	}
	byID := make(map[int64]store.TimeEntry, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	return byID, nil
}

// markRefusal judges one source against its locked row, in the contract's
// order: already invoiced first, so an entry stamped by hand is named for
// what it is; then no longer invoiceable; then changed since the draft read
// it — revision, project, currency or amount, the amount by value.
func markRefusal(source contracts.WorkSource, byID map[int64]store.TimeEntry) (*contracts.WorkSourceRefusal, error) {
	refuse := func(code, detail string) *contracts.WorkSourceRefusal {
		return &contracts.WorkSourceRefusal{Source: source, Code: code, Detail: detail}
	}
	row, ok := byID[source.ID]
	switch {
	case !ok:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Entry %d was not found", source.ID)), nil
	case row.Status == statusInvoiced:
		return refuse(contracts.SourceAlreadyInvoiced, fmt.Sprintf("Entry %d is already invoiced", source.ID)), nil
	case row.Status != statusApproved:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Entry %d is %s, not approved", source.ID, row.Status)), nil
	case !row.Billable:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Entry %d is not billable", source.ID)), nil
	case !row.BillRate.Valid:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Entry %d has no bill rate", source.ID)), nil
	case row.Revision != source.Revision:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Entry %d is at revision %d, not %d", source.ID, row.Revision, source.Revision)), nil
	case row.ProjectID != source.ProjectID:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Entry %d is on project %d, not %d", source.ID, row.ProjectID, source.ProjectID)), nil
	case currencyOf(row) != source.Currency:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Entry %d bills in %q, not %q", source.ID, currencyOf(row), source.Currency)), nil
	}
	want, ok := new(big.Rat).SetString(source.Amount)
	if !ok {
		return nil, fmt.Errorf("time: entry %d's source amount %q is not a decimal", source.ID, source.Amount)
	}
	got, err := entryAmount(row)
	if err != nil {
		return nil, err
	}
	if got.Cmp(want) != 0 {
		return refuse(contracts.SourceChanged, fmt.Sprintf("Entry %d bills %s, not %s", source.ID, got.FloatString(8), source.Amount)), nil
	}
	return nil, nil
}

// currencyOf is the entry's bill currency, "" for none.
func currencyOf(row store.TimeEntry) string {
	if row.BillCurrency == nil {
		return ""
	}
	return *row.BillCurrency
}

// entryAmount is what the entry bills, exactly: hours × bill rate × the
// multiplier (100 % for ordinary hours), the figure BillableHours answers as
// text (queries/invoiced.sql), from the locked row's own numerics.
func entryAmount(row store.TimeEntry) (*big.Rat, error) {
	hours, err := ratFromNumeric(row.Hours)
	if err != nil {
		return nil, err
	}
	rate, err := ratFromNumeric(row.BillRate)
	if err != nil {
		return nil, err
	}
	percent := big.NewRat(100, 1)
	if row.BillMultiplierPercent.Valid {
		if percent, err = ratFromNumeric(row.BillMultiplierPercent); err != nil {
			return nil, err
		}
	}
	amount := new(big.Rat).Mul(hours, rate)
	amount.Mul(amount, percent)
	return amount.Quo(amount, big.NewRat(100, 1)), nil
}

// ratFromNumeric is a numeric column's exact value: its digits times ten to
// its exponent. A NULL, NaN or infinite value is no amount at all.
func ratFromNumeric(n pgtype.Numeric) (*big.Rat, error) {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return nil, fmt.Errorf("time: a stored decimal is not a finite number")
	}
	r := new(big.Rat).SetInt(n.Int)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs32(n.Exp))), nil)
	if n.Exp >= 0 {
		return r.Mul(r, new(big.Rat).SetInt(scale)), nil
	}
	return r.Quo(r, new(big.Rat).SetInt(scale)), nil
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
