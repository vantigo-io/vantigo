package invoices

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the work a document's lines bill (invoices work design D2,
// D17): the line_sources rows held from the draft, carried by identity across
// every save — whose lines are deleted and inserted anew — and dropped by
// name; the sources block, answered from the document's own rows and never a
// live read; and the source modules' billable reads that judge a held source
// still fresh (a GET) or take its current facts (refreshSources). Those reads
// are contract calls, made on the pool and never inside withLockedTx.

// The warnings work adds to a document (D2). Never refusals.
const (
	// warningLineDiffersFromSources is a draft line whose net is not its
	// sources' amounts summed and rounded to øre: a write-down, a rounding.
	warningLineDiffersFromSources = "line_differs_from_sources"
	// warningSourcesReleased is a save that dropped work, named in
	// releasedSources.
	warningSourcesReleased = "sources_released"
	// warningSourceChanged and warningSourceNotInvoiceable are a held
	// source's freshness on GET: what the issue would refuse.
	warningSourceChanged        = "source_changed"
	warningSourceNotInvoiceable = "source_not_invoiceable"

	codeSourceHeldElsewhere = "source_held_elsewhere"
)

// The states of a line source (the column's own values).
const (
	sourceHeld     = "held"
	sourceInvoiced = "invoiced"
	sourceReleased = "released"
)

// The 400s a line's sources answer.
const (
	msgSourcesRequired = "A line of a draft that holds work names its sources: [] for none"
	msgSourceNotHeld   = "This work is not held by this draft; work is added through the uninvoiced view"
	msgSourceTwice     = "A source is named on one line, once"
	msgSourcesOnCreate = "Work is added to a draft through the uninvoiced view"
	msgSourcesOnCredit = "A credit note adds no work"
	msgSourceKind      = "A source's kind is time.entry, expenses.entry or projects.milestone, and its id is positive"
)

// sourceRef is one piece of work by identity.
type sourceRef struct {
	kind contracts.WorkSourceKind
	id   int64
}

func compareRefs(a, b sourceRef) int {
	return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.id, b.id))
}

// sourceRefs is refs on the wire, nil for none.
func sourceRefs(refs []sourceRef) *[]gen.InvoicesSourceRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]gen.InvoicesSourceRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, gen.InvoicesSourceRef{Kind: string(r.kind), Id: r.id})
	}
	return &out
}

// heldSource is one line_sources row as this module reasons about it: the
// snapshot the draft took from the source, and the line (by position) that
// bills it.
type heldSource struct {
	kind             contracts.WorkSourceKind
	id               int64
	revision         int32
	subkind          *string
	projectID        int32
	quantity, amount *big.Rat
	currency         string
	date             time.Time
	state            string
	linePosition     int32
}

func (h heldSource) ref() sourceRef { return sourceRef{kind: h.kind, id: h.id} }

// heldSources reads the rows of LineSourcesOf as exact decimals.
func heldSources(rows []store.LineSourcesOfRow) ([]heldSource, error) {
	out := make([]heldSource, 0, len(rows))
	for _, r := range rows {
		quantity, err := ratFromNumeric(r.Quantity)
		if err != nil {
			return nil, err
		}
		amount, err := ratFromNumeric(r.Amount)
		if err != nil {
			return nil, err
		}
		out = append(out, heldSource{
			kind: contracts.WorkSourceKind(r.SourceKind), id: r.SourceID, revision: r.SourceRevision, subkind: r.SourceSubkind,
			projectID: r.ProjectID, quantity: quantity, amount: amount, currency: r.Currency, date: r.SourceDate.Time,
			state: r.State, linePosition: r.LinePosition,
		})
	}
	return out, nil
}

// sourcesOf is a document's line sources, read on q.
func sourcesOf(ctx context.Context, q *store.Queries, invoiceID int64) ([]heldSource, error) {
	rows, err := q.LineSourcesOf(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's line sources: %w", invoiceID, err)
	}
	return heldSources(rows)
}

// parseLineSources is line i's sources as the request names them: nil when
// the field is absent, empty for [], and a 400 on lines[i].sources for a kind
// this module does not know or an id that is not one.
func parseLineSources(i int, l gen.InvoicesLineRequest) ([]sourceRef, map[string][]string) {
	if l.Sources == nil {
		return nil, nil
	}
	var errs map[string][]string
	out := make([]sourceRef, 0, len(*l.Sources))
	for _, s := range *l.Sources {
		kind := contracts.WorkSourceKind(s.Kind)
		if !slices.Contains(contracts.InvoicedWorkOrder, kind) || s.Id <= 0 {
			if errs == nil {
				errs = fieldError(fmt.Sprintf("lines[%d].sources", i), msgSourceKind)
			}
			continue
		}
		out = append(out, sourceRef{kind: kind, id: s.Id})
	}
	return out, errs
}

// withSourceError adds msg to lines[i].sources once.
func withSourceError(errs map[string][]string, i int, msg string) map[string][]string {
	field := fmt.Sprintf("lines[%d].sources", i)
	if slices.Contains(errs[field], msg) {
		return errs
	}
	return withFieldError(errs, field, msg)
}

// carrySources is D2's carry: the rows held must reappear under the lines
// that now name them. When the draft holds work, every line names its
// sources (absent is a 400, [] carries none); every identity named is one the
// draft holds (a save never adds work) and is named once. rows are the held
// snapshots under their new lines' positions; released is what the draft
// held and no line names any more, by (kind, id).
func carrySources(held []heldSource, lines []draftLine) (rows []heldSource, released []sourceRef, errs map[string][]string) {
	byRef := make(map[sourceRef]heldSource, len(held))
	for _, h := range held {
		byRef[h.ref()] = h
	}
	named := map[sourceRef]bool{}
	for i, l := range lines {
		if !l.sourcesGiven {
			if len(held) > 0 {
				errs = withSourceError(errs, i, msgSourcesRequired)
			}
			continue
		}
		for _, r := range l.sources {
			h, ok := byRef[r]
			switch {
			case named[r]:
				errs = withSourceError(errs, i, msgSourceTwice)
			case !ok:
				errs = withSourceError(errs, i, msgSourceNotHeld)
			default:
				named[r] = true
				h.linePosition = int32(i + 1)
				rows = append(rows, h)
			}
		}
	}
	for _, h := range held {
		if !named[h.ref()] {
			released = append(released, h.ref())
		}
	}
	slices.SortFunc(released, compareRefs)
	return rows, released, errs
}

// errSourceHeldElsewhere is an insert of line sources refused by
// ux_line_sources_live: another document holds or has invoiced one of them.
var errSourceHeldElsewhere = errors.New("invoices: a source is held by another document")

// insertSources writes a draft's held rows under its new lines (lineIDs by
// position) in ONE statement, InsertLineSources, which orders them by (kind,
// id): two transactions holding overlapping sources wait on the index in the
// same order, so one fails with the unique violation — answered as
// errSourceHeldElsewhere — and neither deadlocks (D2).
func insertSources(ctx context.Context, txq *store.Queries, invoiceID int64, lineIDs []int64, rows []heldSource) error {
	if len(rows) == 0 {
		return nil
	}
	p := store.InsertLineSourcesParams{InvoiceID: invoiceID}
	for _, r := range rows {
		quantity, err := numericFromRat(r.quantity, 3)
		if err != nil {
			return err
		}
		amount, err := numericFromRat(r.amount, 8)
		if err != nil {
			return err
		}
		subkind := ""
		if r.subkind != nil {
			subkind = *r.subkind
		}
		p.LineIds = append(p.LineIds, lineIDs[r.linePosition-1])
		p.Kinds, p.Ids, p.Revisions = append(p.Kinds, string(r.kind)), append(p.Ids, r.id), append(p.Revisions, r.revision)
		p.Subkinds, p.ProjectIds = append(p.Subkinds, subkind), append(p.ProjectIds, r.projectID)
		p.Quantities, p.Amounts = append(p.Quantities, quantity), append(p.Amounts, amount)
		p.Currencies, p.Dates = append(p.Currencies, r.currency), append(p.Dates, pgDate(r.date))
	}
	err := txq.InsertLineSources(ctx, p)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "ux_line_sources_live" {
		return errSourceHeldElsewhere
	}
	if err != nil {
		return fmt.Errorf("invoices: hold draft %d's sources: %w", invoiceID, err)
	}
	return nil
}

// liveElsewhere is the 409 source_held_elsewhere for rows when another live
// document holds or has invoiced one of them, nil when none does: the first
// such document — the oldest row — as heldBy, and the source it holds (D2).
// The wizard reads it on its own transaction, under the draft's lock, before
// it holds anything; a save's index violation reads it on the pool after the
// refused transaction rolled back.
func liveElsewhere(ctx context.Context, q *store.Queries, invoiceID int64, rows []heldSource) (*gen.InvoicesConflictProblem, error) {
	p := store.LiveSourcesElsewhereParams{InvoiceID: invoiceID}
	for _, r := range rows {
		p.Kinds, p.Ids = append(p.Kinds, string(r.kind)), append(p.Ids, r.id)
	}
	found, err := q.LiveSourcesElsewhere(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("invoices: read where document %d's sources are held: %w", invoiceID, err)
	}
	if len(found) == 0 {
		return nil, nil
	}
	f := found[0]
	return heldByProblem(f.SourceKind, f.SourceID, gen.InvoicesWorkHeldBy{InvoiceId: f.InvoiceID, Number: f.Number, Status: f.Status}), nil
}

// heldByProblem is source_held_elsewhere naming the source and the document
// that has it.
func heldByProblem(kind string, id int64, by gen.InvoicesWorkHeldBy) *gen.InvoicesConflictProblem {
	holder := fmt.Sprintf("draft %d", by.InvoiceId)
	if by.Number != nil {
		holder = fmt.Sprintf("invoice %d", *by.Number)
	}
	p := conflict(codeSourceHeldElsewhere, "The work is held elsewhere", fmt.Sprintf("%s %d is held by %s.", kind, id, holder))
	p.HeldBy, p.SourceKind, p.SourceId = &by, &kind, &id
	return &p
}

// heldElsewhere is the 409 source_held_elsewhere for rows, naming the first
// other live document that holds one of them — read on q after the refused
// transaction rolled back. When the holder has let go meanwhile, the answer
// names neither.
func heldElsewhere(ctx context.Context, q *store.Queries, invoiceID int64, rows []heldSource) (gen.InvoicesConflictProblem, error) {
	p, err := liveElsewhere(ctx, q, invoiceID, rows)
	if err != nil {
		return gen.InvoicesConflictProblem{}, err
	}
	if p == nil {
		return conflict(codeSourceHeldElsewhere, "The work is held elsewhere", "Some of this work is held by another document."), nil
	}
	return *p, nil
}

// sameHeldSet reports whether two reads of a draft's sources hold the same
// work at the same revisions and amounts — what a refresh read before the
// save's transaction must still find under its lock (plan reading 32). It
// compares kind, id, revision and amount only, never the line: a refresh
// re-places the work on the request's lines anyway. The issue's own check
// of the set it read before its lock must add the line position, since the
// issue stamps the work onto the lines as they stand.
func sameHeldSet(a, b []heldSource) bool {
	if len(a) != len(b) {
		return false
	}
	byRef := make(map[sourceRef]heldSource, len(a))
	for _, h := range a {
		byRef[h.ref()] = h
	}
	for _, h := range b {
		o, ok := byRef[h.ref()]
		if !ok || o.revision != h.revision || o.amount.Cmp(h.amount) != 0 {
			return false
		}
	}
	return true
}

// lineDiffers is D2's difference: a line whose net is not its sources'
// amounts summed and rounded to øre.
func lineDiffers(net *big.Rat, rows []heldSource) bool {
	sum := new(big.Rat)
	for _, r := range rows {
		sum.Add(sum, r.amount)
	}
	return net.Cmp(round2(sum)) != 0
}

// withSources answers a document's sources on resp from its own rows (D2,
// D17): each line's sources and, on a draft, its line_differs_from_sources;
// the document's count by state. A document that bills no work is left as
// it was — no block, no line field.
func withSources(held []heldSource, draft bool, lines []store.InvoicesLine, resp *gen.InvoicesInvoiceResponse) error {
	if len(held) == 0 {
		return nil
	}
	byLine := map[int32][]heldSource{}
	block := gen.InvoicesSourcesBlock{Count: int32(len(held))}
	for _, h := range held {
		byLine[h.linePosition] = append(byLine[h.linePosition], h)
		switch h.state {
		case sourceHeld:
			block.Held++
		case sourceInvoiced:
			block.Invoiced++
		case sourceReleased:
			block.Released++
		}
	}
	resp.Sources = &block
	differs := false
	for i, l := range lines {
		rows := byLine[l.Position]
		sources := make([]gen.InvoicesLineSource, 0, len(rows))
		for _, r := range rows {
			sources = append(sources, gen.InvoicesLineSource{
				Kind: string(r.kind), Id: r.id, ProjectId: r.projectID, Date: wireDate(r.date),
				Quantity: floatFromRat(r.quantity, 3), Amount: floatFromRat(r.amount, 8), State: r.state,
			})
		}
		warnings := []string{}
		if draft && len(rows) > 0 {
			net, err := ratFromNumeric(l.LineNet)
			if err != nil {
				return err
			}
			if lineDiffers(net, rows) {
				warnings = append(warnings, warningLineDiffersFromSources)
				differs = true
			}
		}
		resp.Lines[i].Sources, resp.Lines[i].Warnings = &sources, &warnings
	}
	if differs {
		resp.Warnings = append(resp.Warnings, warningLineDiffersFromSources)
	}
	return nil
}

// withReleased names on a save's answer the work the save dropped.
func withReleased(released []sourceRef, resp *gen.InvoicesInvoiceResponse) {
	if len(released) == 0 {
		return
	}
	resp.ReleasedSources = sourceRefs(released)
	resp.Warnings = append(resp.Warnings, warningSourcesReleased)
}

// The billable reads' rows as the snapshot a line source takes (D2, D3): an
// hour's quantity is its hours and its amount the exact one Time answers; a
// mileage line's its kilometres and its bill amount; an outlay's, a supplier
// invoice's and a milestone's 1 and their amount. A milestone's date is the
// Oslo business day it became ready (plan reading 13).

func hourSource(h contracts.BillableHour) (heldSource, error) {
	amount, err := billableAmount(h.Amount)
	if err != nil {
		return heldSource{}, err
	}
	return heldSource{
		kind: contracts.WorkSourceHours, id: h.ID, revision: h.Revision, projectID: h.ProjectID,
		quantity: big.NewRat(h.HoursHundredths, 100), amount: amount, currency: h.Currency, date: utcDay(h.Date),
	}, nil
}

func expenseSource(e contracts.BillableExpense) (heldSource, error) {
	amount, err := billableAmount(e.BillAmount)
	if err != nil {
		return heldSource{}, err
	}
	quantity := big.NewRat(1, 1)
	if e.Kind == "mileage" && e.DistanceKm != nil {
		if quantity, err = billableAmount(*e.DistanceKm); err != nil {
			return heldSource{}, err
		}
	}
	return heldSource{
		kind: contracts.WorkSourceExpense, id: e.ID, revision: e.Revision, subkind: ptr(e.Kind), projectID: e.ProjectID,
		quantity: quantity, amount: amount, currency: e.Currency, date: utcDay(e.Date),
	}, nil
}

func milestoneSource(m contracts.BillableMilestone) (heldSource, error) {
	amount, err := billableAmount(m.Amount)
	if err != nil {
		return heldSource{}, err
	}
	return heldSource{
		kind: contracts.WorkSourceMilestone, id: m.ID, revision: m.Revision, projectID: m.ProjectID,
		quantity: big.NewRat(1, 1), amount: amount, currency: m.Currency, date: businessDay(m.ReadyAt),
	}, nil
}

// billableAmount reads a contract's decimal text exactly.
func billableAmount(text string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, fmt.Errorf("invoices: a billable read answered %q, not a decimal", text)
	}
	return r, nil
}

// billableNow reads the held sources by id through the composed billable
// reads, on the pool — never under a lock — and answers each one still
// billable as the snapshot it would take now, and which kinds were read: a
// kind whose module is off has no provider and is not judged (plan reading
// 11). Each read names at most contracts.MaxBillableRows ids, the bound a
// save's body keeps.
func (s *server) billableNow(ctx context.Context, held []heldSource) (map[sourceRef]heldSource, map[contracts.WorkSourceKind]bool, error) {
	ids := map[contracts.WorkSourceKind][]int64{}
	for _, h := range held {
		ids[h.kind] = append(ids[h.kind], h.id)
	}
	now := map[sourceRef]heldSource{}
	read := map[contracts.WorkSourceKind]bool{}
	add := func(h heldSource, err error) error {
		if err != nil {
			return err
		}
		now[h.ref()] = h
		return nil
	}
	if want := ids[contracts.WorkSourceMilestone]; len(want) > 0 && s.deps.BillableMilestones != nil {
		page, err := s.billableMilestones(ctx, contracts.BillableRequest{IDs: want})
		if err != nil {
			return nil, nil, fmt.Errorf("invoices: read the billable milestones: %w", err)
		}
		for _, m := range page.Milestones {
			if err := add(milestoneSource(m)); err != nil {
				return nil, nil, err
			}
		}
		read[contracts.WorkSourceMilestone] = true
	}
	if want := ids[contracts.WorkSourceExpense]; len(want) > 0 && s.deps.BillableExpenses != nil {
		page, err := s.billableExpenses(ctx, contracts.BillableRequest{IDs: want})
		if err != nil {
			return nil, nil, fmt.Errorf("invoices: read the billable expenses: %w", err)
		}
		for _, e := range page.Expenses {
			if err := add(expenseSource(e)); err != nil {
				return nil, nil, err
			}
		}
		read[contracts.WorkSourceExpense] = true
	}
	if want := ids[contracts.WorkSourceHours]; len(want) > 0 && s.deps.BillableHours != nil {
		page, err := s.billableHours(ctx, contracts.BillableRequest{IDs: want})
		if err != nil {
			return nil, nil, fmt.Errorf("invoices: read the billable hours: %w", err)
		}
		for _, h := range page.Hours {
			if err := add(hourSource(h)); err != nil {
				return nil, nil, err
			}
		}
		read[contracts.WorkSourceHours] = true
	}
	return now, read, nil
}

// staleness judges one held source against what its module answers now, by
// the holders' own facts (D1, D2): Time's and Projects' revision, project,
// currency and amount; Expenses' bill amount, project, currency and kind —
// never an expense's revision, which a reimbursement moves without touching
// what is billed. Amounts by value. "" when it is fresh.
func staleness(h heldSource, now heldSource, found bool) string {
	switch {
	case !found:
		return warningSourceNotInvoiceable
	case h.projectID != now.projectID, h.currency != now.currency, h.amount.Cmp(now.amount) != 0:
		return warningSourceChanged
	case h.kind == contracts.WorkSourceExpense:
		if h.subkind == nil || now.subkind == nil || *h.subkind != *now.subkind {
			return warningSourceChanged
		}
	case h.revision != now.revision:
		return warningSourceChanged
	}
	return ""
}

// freshness is D2's GET-time judgment of a draft's held work: per source,
// source_changed or source_not_invoiceable, nothing for a fresh source or one
// whose kind's module is off.
func (s *server) freshness(ctx context.Context, held []heldSource) (map[sourceRef]string, error) {
	now, read, err := s.billableNow(ctx, held)
	if err != nil {
		return nil, err
	}
	out := map[sourceRef]string{}
	for _, h := range held {
		if !read[h.kind] {
			continue
		}
		n, found := now[h.ref()]
		if w := staleness(h, n, found); w != "" {
			out[h.ref()] = w
		}
	}
	return out, nil
}

// withFreshness adds freshness's verdicts to resp: each on the line that
// bills the source, once per line, and each code once on the document.
func withFreshness(held []heldSource, verdicts map[sourceRef]string, resp *gen.InvoicesInvoiceResponse) {
	byPosition := map[int32]int{}
	for i, l := range resp.Lines {
		byPosition[l.Position] = i
	}
	for _, h := range held {
		w, ok := verdicts[h.ref()]
		if !ok {
			continue
		}
		if i, ok := byPosition[h.linePosition]; ok && resp.Lines[i].Warnings != nil && !slices.Contains(*resp.Lines[i].Warnings, w) {
			*resp.Lines[i].Warnings = append(*resp.Lines[i].Warnings, w)
		}
		if !slices.Contains(resp.Warnings, w) {
			resp.Warnings = append(resp.Warnings, w)
		}
	}
}

// sourcesRefresh is what refreshSources read before the save's transaction:
// the draft's held work, and what its modules answer for it now.
type sourcesRefresh struct {
	read []heldSource
	now  map[sourceRef]heldSource
	kind map[contracts.WorkSourceKind]bool
}

// apply takes the current facts of each carried row whose module answered
// for it — the revision, the project, the quantity, the amount, the date and
// an expense's kind — and drops each one its module no longer answers (no
// longer invoiceable) or answers in another currency: a source that moved
// from the draft's currency would otherwise be taken as fresh and stamped
// into a document in another currency without a word. A kind whose module is off is carried as
// it stood; the issue fails closed on it.
func (r *sourcesRefresh) apply(rows []heldSource) (kept []heldSource, dropped []sourceRef) {
	for _, h := range rows {
		if !r.kind[h.kind] {
			kept = append(kept, h)
			continue
		}
		n, ok := r.now[h.ref()]
		if !ok || n.currency != h.currency {
			dropped = append(dropped, h.ref())
			continue
		}
		n.state, n.linePosition = h.state, h.linePosition
		kept = append(kept, n)
	}
	return kept, dropped
}

// The document's project (invoices work design D9, plan reading 14): the one
// project all of a draft's held work belongs to, derived — never written by
// a request — and stored with the project's code as a snapshot, the
// reference the PDF and the EHF print from the document's own row. The code
// comes from the draft's stored reference while the project is unchanged,
// and otherwise from the project directory, read before the writer's
// transaction (a contract call is never made under its lock) into
// projectCodes; with Projects switched off or the project gone from the
// directory there is no code, and both columns are NULL
// (ck_invoices_project).

// projectCodes is the codes of entries by project id: what a writer read
// through ProjectDirectory before its transaction, for setDocumentProject.
func projectCodes(entries []contracts.ProjectEntry) map[int32]string {
	codes := make(map[int32]string, len(entries))
	for _, e := range entries {
		codes[e.ID] = e.Code
	}
	return codes
}

// oneProject is the project every row belongs to, false when they span two
// or there are none.
func oneProject(rows []heldSource) (int32, bool) {
	if len(rows) == 0 {
		return 0, false
	}
	for _, r := range rows[1:] {
		if r.projectID != rows[0].projectID {
			return 0, false
		}
	}
	return rows[0].projectID, true
}

// setDocumentProject derives D9's project from rows — every line source the
// draft holds once the writer's own writes are done, the work a save
// carries or, for the uninvoiced view's wizard, the target's carried work
// and the work it adds — and stores it on the locked draft doc, on txq,
// inside the writer's transaction; it answers doc as it then stands. The
// reference is doc's own stored one when the project is unchanged, else
// codes[project] (projectCodes of what the writer read before its
// transaction); a project codes does not know leaves both columns NULL. A
// project the draft already names, unchanged, writes nothing.
func setDocumentProject(ctx context.Context, txq *store.Queries, doc store.InvoicesInvoice, rows []heldSource, codes map[int32]string) (store.InvoicesInvoice, error) {
	var projectID *int32
	var reference *string
	if p, ok := oneProject(rows); ok {
		switch code, known := codes[p]; {
		case doc.ProjectID != nil && *doc.ProjectID == p && doc.ProjectReference != nil:
			projectID, reference = &p, doc.ProjectReference
		case known:
			projectID, reference = &p, &code
		}
	}
	if sameInt32(projectID, doc.ProjectID) && sameText(reference, doc.ProjectReference) {
		return doc, nil
	}
	set, err := txq.SetDocumentProject(ctx, store.SetDocumentProjectParams{ID: doc.ID, ProjectID: projectID, ProjectReference: reference})
	if err != nil {
		return store.InvoicesInvoice{}, fmt.Errorf("invoices: set draft %d's project: %w", doc.ID, err)
	}
	return set, nil
}

func sameInt32(a, b *int32) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// saveProjectCodes is what a save of current needs from the project
// directory for its derived project, read before the save's transaction: the
// code of the one project the work the request keeps belongs to — the held
// work its lines name, at a refresh's current project — when the draft does
// not already name that project. Nothing is read for a draft without work,
// work spanning two projects, a customer change (which drops every hold) or
// a project the draft names already; nor with Projects switched off. held is
// the draft's work as read on the pool. The save's lock judges the work
// again; a save slipped in between moves the revision and is refused there.
func (s *server) saveProjectCodes(ctx context.Context, current store.InvoicesInvoice, in draftInput, held []heldSource) (map[int32]string, error) {
	if s.deps.Projects == nil || in.customerID != current.CustomerID || len(held) == 0 {
		return nil, nil
	}
	byRef := make(map[sourceRef]heldSource, len(held))
	for _, h := range held {
		byRef[h.ref()] = h
	}
	var kept []heldSource
	for _, l := range in.lines {
		for _, r := range l.sources {
			if h, ok := byRef[r]; ok {
				kept = append(kept, h)
			}
		}
	}
	if in.refresh != nil {
		kept, _ = in.refresh.apply(kept)
	}
	p, ok := oneProject(kept)
	if !ok || (current.ProjectID != nil && *current.ProjectID == p) {
		return nil, nil
	}
	entries, err := s.projectEntries(ctx, []int32{p})
	if err != nil {
		return nil, fmt.Errorf("invoices: read project %d for draft %d: %w", p, current.ID, err)
	}
	return projectCodes(entries), nil
}
