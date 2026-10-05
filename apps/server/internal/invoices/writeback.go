package invoices

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the write-back (invoices work design D1, module-boundaries
// rule 10): the issue of an invoice that bills work stamps each source
// invoiced through its module's contracts.InvoicedWorkHolder, inside the
// issue's own transaction — after every check and the number, before the
// document is written — and a credit note that returns a line in full takes
// the stamp back the same way (D8). The holders run on the issue's pgx.Tx,
// once per kind, in contracts.InvoicedWorkOrder: Projects, then Expenses,
// then Time, after Invoices' own locks. What a decision under the lock needs
// from a directory — whom a source's project bills and how, the issuer's
// name — is read before the transaction (issueWork) and judged again under
// it.

// The issue's refusals about work (D1): the three a holder answers, as
// contracts names them, and the three the issue judges itself.
const (
	codeSourceNotInvoiceable  = "source_not_invoiceable"
	codeSourceChanged         = "source_changed"
	codeSourceAlreadyInvoiced = "source_already_invoiced"
	codeProjectsUnavailable   = "projects_unavailable"
	codeSourceCustomerChanged = "source_customer_changed"
	codeSourceNotSelectable   = "source_not_selectable"
)

// The billing types a project may carry, as contracts.ProjectEntry names them.
const (
	billingFixedPrice  = "fixed-price"
	billingNonBillable = "non-billable"
)

// holders is the composed holders by the kind each stamps.
type holders map[contracts.WorkSourceKind]contracts.InvoicedWorkHolder

// holders is deps.InvoicedWork by kind. Compose refuses two holders claiming
// one kind; this refuses it again rather than let one win.
func (s *server) holders() (holders, error) {
	out := holders{}
	for _, h := range s.deps.InvoicedWork {
		for _, k := range h.Kinds() {
			if _, twice := out[k]; twice {
				return nil, fmt.Errorf("invoices: two invoiced-work holders claim %s", k)
			}
			out[k] = h
		}
	}
	return out, nil
}

// workSources is rows as the holders take them, by kind, each kind's by id —
// the order every holder locks its rows in.
func workSources(rows []heldSource) map[contracts.WorkSourceKind][]contracts.WorkSource {
	out := map[contracts.WorkSourceKind][]contracts.WorkSource{}
	for _, r := range rows {
		expenseKind := ""
		if r.subkind != nil {
			expenseKind = *r.subkind
		}
		out[r.kind] = append(out[r.kind], contracts.WorkSource{
			Kind: r.kind, ID: r.id, Revision: r.revision, ProjectID: r.projectID, Currency: r.currency,
			Amount: r.amount.FloatString(8), ExpenseKind: expenseKind,
		})
	}
	for _, sources := range out {
		slices.SortFunc(sources, func(a, b contracts.WorkSource) int { return cmp.Compare(a.ID, b.ID) })
	}
	return out
}

// sourceRefusal is the issue's 409 code about the source r, on the first line
// holding it.
func sourceRefusal(code, detail string, r heldSource) *gen.InvoicesConflictProblem {
	p := cannotIssue(code, detail)
	p.LinePosition, p.SourceKind, p.SourceId = ptr(r.linePosition), ptr(string(r.kind)), ptr(r.id)
	return p
}

// markWork stamps rows invoiced with ref (D1): each kind's holder once, in
// contracts.InvoicedWorkOrder, on tx. A holder's *contracts.WorkSourceRefusal
// is the issue's refusal with its code, naming the first line holding the
// source and the source; anything else, a refusal code no holder may answer
// included, is a failure. Either rolls the whole issue back.
func (s *server) markWork(ctx context.Context, tx pgx.Tx, h holders, ref contracts.InvoiceRef, rows []heldSource) (*gen.InvoicesConflictProblem, error) {
	byKind := workSources(rows)
	for _, kind := range contracts.InvoicedWorkOrder {
		sources := byKind[kind]
		if len(sources) == 0 {
			continue
		}
		holder, ok := h[kind]
		if !ok {
			return nil, fmt.Errorf("invoices: no holder claims %s", kind)
		}
		err := markInvoiced(ctx, tx, kind, holder, ref, sources)
		var refusal *contracts.WorkSourceRefusal
		if errors.As(err, &refusal) {
			switch refusal.Code {
			case codeSourceNotInvoiceable, codeSourceChanged, codeSourceAlreadyInvoiced:
			default:
				return nil, fmt.Errorf("invoices: %s's holder answered an unknown refusal: %w", kind, err)
			}
			for _, r := range rows {
				if r.kind == refusal.Source.Kind && r.id == refusal.Source.ID {
					return sourceRefusal(refusal.Code, fmt.Sprintf("Line %d's %s %d cannot be invoiced: %s",
						r.linePosition, r.kind, r.id, refusal.Detail), r), nil
				}
			}
			return nil, fmt.Errorf("invoices: %s's holder refused a source the invoice does not hold: %w", kind, err)
		}
		if err != nil {
			return nil, fmt.Errorf("invoices: mark %s invoiced: %w", kind, err)
		}
	}
	return nil, nil
}

// releaseWork takes ref's stamp off rows (D8): each kind's holder once, in
// contracts.InvoicedWorkOrder, on tx. An invoiced source whose kind no holder
// claims is logged at error and skipped — a credit note is never blocked —
// and any error a holder answers is a failure that rolls the credit note
// back.
func (s *server) releaseWork(ctx context.Context, tx pgx.Tx, h holders, ref contracts.InvoiceRef, rows []heldSource) error {
	byKind := workSources(rows)
	for _, kind := range contracts.InvoicedWorkOrder {
		sources := byKind[kind]
		if len(sources) == 0 {
			continue
		}
		holder, ok := h[kind]
		if !ok {
			s.deps.Logger.ErrorContext(ctx, "invoices: no holder claims a released source's kind; its stamp stays",
				"kind", string(kind), "invoiceId", ref.ID, "sources", len(sources))
			continue
		}
		if err := releaseInvoiced(ctx, tx, kind, holder, ref, sources); err != nil {
			return fmt.Errorf("invoices: release %s: %w", kind, err)
		}
	}
	return nil
}

// issueWork is what an issue reads about its work before its transaction:
// for an invoice (D1) the held rows, the holders that stamp them, their
// projects and the issuer's name; for a credit note (D8, readReleaseWork)
// its original's rows, and the holders and the issuer's name when any of
// them is invoiced.
type issueWork struct {
	rows     []heldSource
	holders  holders
	projects map[int32]contracts.ProjectEntry
	display  string
}

// readIssueWork reads, on the pool, before the issue's transaction, what the
// write-back of draft's work needs, in order: the held rows; every kind
// claimed by a holder, else a composition bug — logged, and a failure; the
// project directory, else projects_unavailable; each held source's project
// still billing draft's customer, else source_customer_changed on the first
// line holding a source of it; the issuer's name. A draft holding no work
// reads nothing more.
func (s *server) readIssueWork(ctx context.Context, q *store.Queries, draft store.InvoicesInvoice) (issueWork, *gen.InvoicesConflictProblem, error) {
	rows, err := sourcesOf(ctx, q, draft.ID)
	if err != nil || len(rows) == 0 {
		return issueWork{rows: rows}, nil, err
	}
	h, err := s.holders()
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: the invoiced-work holders are composed wrong", "error", err.Error())
		return issueWork{}, nil, err
	}
	for _, r := range rows {
		if _, ok := h[r.kind]; !ok {
			s.deps.Logger.ErrorContext(ctx, "invoices: no holder claims a held source's kind; the invoice cannot be issued",
				"kind", string(r.kind), "invoiceId", draft.ID)
			return issueWork{}, nil, fmt.Errorf("invoices: no holder claims %s", r.kind)
		}
	}
	if s.deps.Projects == nil {
		return issueWork{}, cannotIssue(codeProjectsUnavailable,
			"The draft bills work, and with the projects module switched off its projects cannot be checked. Nothing was issued."), nil
	}
	var ids []int32
	for _, r := range rows {
		ids = append(ids, r.projectID)
	}
	slices.Sort(ids)
	entries, err := s.projectEntries(ctx, slices.Compact(ids))
	if err != nil {
		return issueWork{}, nil, fmt.Errorf("invoices: read the projects of document %d's work: %w", draft.ID, err)
	}
	projects := make(map[int32]contracts.ProjectEntry, len(entries))
	for _, e := range entries {
		projects[e.ID] = e
	}
	for _, r := range rows {
		p, ok := projects[r.projectID]
		if !ok || p.CustomerID == nil || *p.CustomerID != draft.CustomerID {
			return issueWork{}, sourceRefusal(codeSourceCustomerChanged, fmt.Sprintf(
				"Line %d bills work of project %d, which no longer bills this customer.", r.linePosition, r.projectID), r), nil
		}
	}
	display, err := s.issuerName(ctx)
	if err != nil {
		return issueWork{}, nil, err
	}
	return issueWork{rows: rows, holders: h, projects: projects, display: display}, nil, nil
}

// issuerName is the caller's name as the user directory knows it, "" for a
// user it does not know: the IssuedByDisplay of a stamp or a release, read
// before the issue's transaction (plan reading 20).
func (s *server) issuerName(ctx context.Context) (string, error) {
	user, err := s.userEntry(ctx, callerID(ctx))
	if err != nil {
		return "", fmt.Errorf("invoices: read the issuer: %w", err)
	}
	if user == nil {
		return "", nil
	}
	return user.DisplayName, nil
}

// sameIssueWork reports whether the draft's work under the issue's lock is
// the work read before it: the same sources at the same revisions and
// amounts, on the same lines.
func sameIssueWork(before, locked []heldSource) bool {
	if !sameHeldSet(before, locked) {
		return false
	}
	lines := make(map[sourceRef]int32, len(before))
	for _, h := range before {
		lines[h.ref()] = h.linePosition
	}
	for _, h := range locked {
		if lines[h.ref()] != h.linePosition {
			return false
		}
	}
	return true
}

// notSelectable is the wizard's rule applied again under the issue's lock
// from the projects read before it (plan reading 19): hours of a project now
// fixed-price or non-billable, and any work of one now non-billable, refuse
// the issue on the first line holding such a source.
func (w issueWork) notSelectable() *gen.InvoicesConflictProblem {
	for _, r := range w.rows {
		switch billing := w.projects[r.projectID].BillingType; {
		case billing == billingNonBillable,
			billing == billingFixedPrice && r.kind == contracts.WorkSourceHours:
			return sourceRefusal(codeSourceNotSelectable, fmt.Sprintf(
				"Line %d bills %s %d of project %d, which is now %s.", r.linePosition, r.kind, r.id, r.projectID, billing), r)
		}
	}
	return nil
}

// markIssueWork is the write-back's step in an invoice's issue, under its
// lock, after every check and the number (D1): the draft's work read again —
// any difference from what was read before the transaction is a save slipped
// in between, invoice_changed; the projects' billing types judged
// (source_not_selectable); the holders called with ref; the rows moved from
// held to invoiced. A draft holding no work, then or now, writes nothing.
func (s *server) markIssueWork(ctx context.Context, tx pgx.Tx, txq *store.Queries, work issueWork, ref contracts.InvoiceRef) (*gen.InvoicesConflictProblem, error) {
	rows, err := sourcesOf(ctx, txq, ref.ID)
	if err != nil {
		return nil, err
	}
	if !sameIssueWork(work.rows, rows) {
		return cannotIssue(codeInvoiceChanged, "The work this invoice bills changed; try again."), nil
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if refusal := work.notSelectable(); refusal != nil {
		return refusal, nil
	}
	if refusal, err := s.markWork(ctx, tx, work.holders, ref, rows); refusal != nil || err != nil {
		return refusal, err
	}
	n, err := txq.MarkSourcesInvoiced(ctx, ref.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: mark document %d's sources invoiced: %w", ref.ID, err)
	}
	if n != int64(len(rows)) {
		return nil, fmt.Errorf("invoices: document %d held %d sources and %d moved to invoiced", ref.ID, len(rows), n)
	}
	return nil, nil
}
