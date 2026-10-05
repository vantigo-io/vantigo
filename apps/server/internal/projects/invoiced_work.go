package projects

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// invoicedWorkHolder is this module's contracts.InvoicedWorkHolder
// (module-boundaries rule 10, invoices work design D1): the invoices issue
// stamps the billing milestones an invoice bills with the invoice's id and
// number inside its own transaction, and the credit note that returns a
// milestone's line takes the stamp back. It keeps the contract's rules: its
// SQL is its own (queries/invoiced.sql), on the caller's tx; it reads no
// directory and takes no clock — every timestamp is ref.IssuedAt and the
// timeline's actor is ref's issuer, named by Invoices before its transaction —
// and it runs whether or not the module is enabled, needing only the logger.
type invoicedWorkHolder struct {
	logger *slog.Logger
}

var _ contracts.InvoicedWorkHolder = (*invoicedWorkHolder)(nil)

// newInvoicedWorkHolder is Module's InvoicedWork.
func newInvoicedWorkHolder(d module.Deps) contracts.InvoicedWorkHolder {
	return &invoicedWorkHolder{logger: d.Logger}
}

// invoicedWorkAfterLock is the tests' seam (SetInvoicedWorkAfterLock): called
// by both directions right after their locks are taken, with the context the
// holder runs under. Nil in production.
var invoicedWorkAfterLock func(ctx context.Context)

// Kinds is the one kind this module stamps.
func (h *invoicedWorkHolder) Kinds() []contracts.WorkSourceKind {
	return []contracts.WorkSourceKind{contracts.WorkSourceMilestone}
}

// lockedMilestones is what both directions hold before deciding anything: the
// sources' projects and their milestones, each under its row lock.
type lockedMilestones struct {
	projects   map[int32]store.ProjectsProject
	milestones map[int64]store.ProjectsBillingMilestone
}

// lock takes the locks both directions take, in the module's own order
// (milestones.go): each distinct project the sources name by id ascending
// (LockProject, FOR NO KEY UPDATE), then the milestones by id ascending
// (LockMilestonesByIDs). The ctx it answers is marked inLockedTx, so the
// module's contract-call hook catches a directory read made under the locks.
// A project that is gone holds no milestone (the key has no ON DELETE, and a
// project is never deleted), so it is simply not in the answer.
func (h *invoicedWorkHolder) lock(ctx context.Context, txq *store.Queries, sources []contracts.WorkSource) (context.Context, lockedMilestones, error) {
	ctx = context.WithValue(ctx, lockedTxKey{}, true)
	locked := lockedMilestones{
		projects:   map[int32]store.ProjectsProject{},
		milestones: map[int64]store.ProjectsBillingMilestone{},
	}
	var projectIDs []int32
	var ids []int64
	for _, s := range sources {
		if s.Kind != contracts.WorkSourceMilestone {
			return ctx, locked, fmt.Errorf("projects: invoiced work: a %q source handed to the milestone holder", s.Kind)
		}
		projectIDs = append(projectIDs, s.ProjectID)
		ids = append(ids, s.ID)
	}
	slices.Sort(projectIDs)
	for _, id := range slices.Compact(projectIDs) {
		project, err := lockProject(ctx, txq, id)
		if errors.Is(err, errProjectVanished) {
			continue
		}
		if err != nil {
			return ctx, locked, err
		}
		locked.projects[id] = project
	}
	rows, err := txq.LockMilestonesByIDs(ctx, milestoneIDs(ids))
	if err != nil {
		return ctx, locked, fmt.Errorf("projects: invoiced work: lock the milestones: %w", err)
	}
	for _, m := range rows {
		locked.milestones[int64(m.ID)] = m
	}
	if invoicedWorkAfterLock != nil {
		invoicedWorkAfterLock(ctx)
	}
	return ctx, locked, nil
}

// byID is sources in id order, the order both directions judge and write in.
func byID(sources []contracts.WorkSource) []contracts.WorkSource {
	sorted := slices.Clone(sources)
	slices.SortFunc(sorted, func(a, b contracts.WorkSource) int { return cmp.Compare(a.ID, b.ID) })
	return sorted
}

// issuer is ref's issuer as the timeline records an actor: a user the
// directory did not know reads as unknownUser, as on every other entry.
func issuer(ref contracts.InvoiceRef) actor {
	display := ref.IssuedByDisplay
	if display == "" {
		display = unknownUser
	}
	return actor{UserID: ref.IssuedBy, Display: display}
}

// MarkInvoiced judges every source under the locks and, only when none is
// refused, stamps each with ref and records its milestone-invoiced entry.
func (h *invoicedWorkHolder) MarkInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	if len(sources) == 0 {
		return nil
	}
	txq := store.New(tx)
	ctx, locked, err := h.lock(ctx, txq, sources)
	if err != nil {
		return err
	}

	sorted := byID(sources)
	frozen := make([]*big.Rat, len(sorted))
	for i, s := range sorted {
		amount, err := judgeMilestone(s, locked)
		if err != nil {
			return err
		}
		frozen[i] = amount
	}

	by := issuer(ref)
	for i, s := range sorted {
		amount, err := numericFromRat(frozen[i])
		if err != nil {
			return err
		}
		stamped, err := txq.StampMilestoneInvoicedByInvoice(ctx, store.StampMilestoneInvoicedByInvoiceParams{
			ID:             int32(s.ID),
			IssuedAt:       ref.IssuedAt,
			IssuedBy:       &ref.IssuedBy,
			IssueDate:      pgtype.Date{Time: ref.IssueDate, Valid: true},
			InvoicedAmount: amount,
			InvoiceID:      &ref.ID,
			InvoiceNumber:  &ref.Number,
		})
		if err != nil {
			return fmt.Errorf("projects: invoiced work: stamp milestone %d: %w", s.ID, err)
		}
		if err := recordMilestoneEventWith(ctx, txq, ref.IssuedAt, eventMilestoneInvoiced, stamped,
			map[string]any{"invoiceNumber": ref.Number}, by); err != nil {
			return err
		}
	}
	return nil
}

// judgeMilestone is one source against its locked row, in the contract's
// order: already invoiced first, so a milestone marked by hand is named for
// what it is; then no longer invoiceable — not ready, or nothing to price it
// by; then changed since the draft read it — its revision (a reorder moves
// none), its project, its currency, or its effective amount by value. It
// answers the amount to freeze.
func judgeMilestone(s contracts.WorkSource, locked lockedMilestones) (*big.Rat, error) {
	refuse := func(code, detail string) (*big.Rat, error) {
		return nil, &contracts.WorkSourceRefusal{Source: s, Code: code, Detail: detail}
	}
	m, ok := locked.milestones[s.ID]
	if !ok {
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Billing milestone %d does not exist", s.ID))
	}
	if m.Status == milestoneStatusInvoiced {
		if m.InvoicedNumber != nil {
			return refuse(contracts.SourceAlreadyInvoiced,
				fmt.Sprintf("Billing milestone '%s' is already invoiced on invoice %d", m.Name, *m.InvoicedNumber))
		}
		return refuse(contracts.SourceAlreadyInvoiced,
			fmt.Sprintf("Billing milestone '%s' is already marked invoiced", m.Name))
	}
	if m.Status != milestoneStatusReady {
		return refuse(contracts.SourceNotInvoiceable,
			fmt.Sprintf("Billing milestone '%s' is %s, not ready to invoice", m.Name, m.Status))
	}
	project, ok := locked.projects[m.ProjectID]
	if !ok {
		// The source named another project than the milestone's own, so the
		// milestone's project was not locked: the draft's read is not this row.
		return refuse(contracts.SourceChanged,
			fmt.Sprintf("Billing milestone '%s' belongs to project %d, not %d", m.Name, m.ProjectID, s.ProjectID))
	}
	amount, err := milestoneEffectiveAmountRat(m, project)
	if errors.Is(err, errMilestoneUnpriced) {
		return refuse(contracts.SourceNotInvoiceable,
			fmt.Sprintf("Billing milestone '%s' is a percent of a fixed price its project no longer has", m.Name))
	}
	if err != nil {
		return nil, err
	}
	if m.Revision != s.Revision {
		return refuse(contracts.SourceChanged,
			fmt.Sprintf("Billing milestone '%s' has revision %d; the draft has revision %d", m.Name, m.Revision, s.Revision))
	}
	if m.ProjectID != s.ProjectID {
		return refuse(contracts.SourceChanged,
			fmt.Sprintf("Billing milestone '%s' belongs to project %d, not %d", m.Name, m.ProjectID, s.ProjectID))
	}
	currency := ""
	if c := milestoneCurrency(m, project); c != nil {
		currency = *c
	}
	if currency != s.Currency {
		return refuse(contracts.SourceChanged,
			fmt.Sprintf("Billing milestone '%s' is in %q; the draft has %q", m.Name, currency, s.Currency))
	}
	drafted, ok := new(big.Rat).SetString(s.Amount)
	if !ok {
		return nil, fmt.Errorf("projects: invoiced work: milestone %d: the draft's amount %q is not a decimal", s.ID, s.Amount)
	}
	if amount.Cmp(drafted) != 0 {
		return refuse(contracts.SourceChanged,
			fmt.Sprintf("Billing milestone '%s' is now %s; the draft has %s", m.Name, amount.FloatString(2), s.Amount))
	}
	return amount, nil
}

// ReleaseInvoiced takes ref's stamp off every source that still carries it,
// under the same locks in the same order as MarkInvoiced. It never refuses: a
// credit note must not be blocked. The milestone always goes back to ready;
// a percent one whose fixed price is gone becomes an amount milestone at what
// was invoiced, as the manual undo converts it (milestoneMoveRefusal); and
// where the manual undo would have refused — a project since left without a
// currency, or moved to another — it is released anyway and a warning says
// so. A source that no longer carries ref's stamp, or whose project is not
// the one the source names, is left as it is with a warning: a stamp never
// moves without Invoices, so that is a repair, not a flow.
func (h *invoicedWorkHolder) ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	if len(sources) == 0 {
		return nil
	}
	txq := store.New(tx)
	ctx, locked, err := h.lock(ctx, txq, sources)
	if err != nil {
		return err
	}
	undo, _ := milestoneMoveFor(milestoneStatusInvoiced, milestoneStatusReady)
	by := issuer(ref)
	for _, s := range byID(sources) {
		m, ok := locked.milestones[s.ID]
		if !ok || m.InvoicedInvoiceID == nil || *m.InvoicedInvoiceID != ref.ID {
			h.logger.WarnContext(ctx, "projects: a billing milestone released by a credit note no longer carries the invoice's stamp; left as it is",
				"milestone_id", s.ID, "invoice_id", ref.ID, "invoice_number", ref.Number)
			continue
		}
		project, ok := locked.projects[m.ProjectID]
		if !ok {
			h.logger.WarnContext(ctx, "projects: a billing milestone released by a credit note belongs to another project than its source names; left as it is",
				"milestone_id", s.ID, "project_id", m.ProjectID, "source_project_id", s.ProjectID, "invoice_id", ref.ID)
			continue
		}
		msg, convert := milestoneMoveRefusal(m, project, milestoneStatusReady, undo)
		if msg != "" {
			h.logger.WarnContext(ctx, "projects: a billing milestone was released by a credit note although the manual undo would refuse it",
				"milestone_id", m.ID, "project_id", m.ProjectID, "invoice_id", ref.ID, "refusal", msg)
		}
		params := store.ReleaseMilestoneInvoicedByInvoiceParams{
			ID:             m.ID,
			Amount:         m.Amount,
			AmountCurrency: m.AmountCurrency,
			Percent:        m.Percent,
			ReleasedAt:     ref.IssuedAt,
			InvoiceID:      &ref.ID,
		}
		if convert {
			params.Amount, params.Percent, params.AmountCurrency = m.InvoicedAmount, pgtype.Numeric{}, project.Currency
		}
		released, err := txq.ReleaseMilestoneInvoicedByInvoice(ctx, params)
		if err != nil {
			return fmt.Errorf("projects: invoiced work: release milestone %d: %w", m.ID, err)
		}
		extra := map[string]any{"invoiceNumber": ref.Number}
		if convert {
			extra["convertedToAmount"] = true
		}
		if err := recordMilestoneEventWith(ctx, txq, ref.IssuedAt, eventMilestoneInvoiceUndone, released, extra, by); err != nil {
			return err
		}
	}
	return nil
}

// numericFromRat stores an exact amount as the numeric column takes it, by its
// two-decimal text — never through a float64.
func numericFromRat(r *big.Rat) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(r.FloatString(2)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("projects: %s is not a storable decimal: %w", r.FloatString(2), err)
	}
	return n, nil
}
