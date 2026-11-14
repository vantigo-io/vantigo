package accounting

// The catch-up (A12 as amended by R1, R7, R12): Invoices' sales journal is
// the source, read after the books' own cursor in two streams — the issued
// documents by number, the fact log by seq — and Invoices' events only make
// it prompt. A handler upserts a wake-up for its stream on the delivery's
// transaction and does nothing else (module-boundaries rule 23); the worker
// accounting.catchup, every 15 seconds:
//
//  1. claims a stream's lease in one short transaction, when a wake-up waits
//     or the last run is five minutes old, and takes the wake-up;
//  2. reads the journal after the cursor with no transaction open;
//  3. posts each document or fact in its own transaction, which also moves
//     the cursor under the lease — one that cannot be posted goes under Needs
//     attention (unposted) and the cursor still moves (A15), and one of an
//     invoice that has a row there waits behind it (R15); a transaction that
//     meets a deadlock — a person settling the same item — is tried once
//     more, and any other error stops the run for the next to take up;
//  4. releases the lease, saying how the run ended — leaving a wake-up
//     behind when it stopped at the lease's margin with more to read (R15),
//     so a backlog drains at the poll's cadence, not the sweep's.
//
// The source index (A4) makes a fact delivered twice, or posted by two
// workers, harmless: the second post is a Repeat. A late handler, a pruned
// event and a dead delivery all end the same way: the next sweep posts what
// is missing. Nothing runs before the books are set up (R12).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	// catchupWorkerName is the worker's name in the runner's logs.
	catchupWorkerName = "accounting.catchup"
	// catchupInterval is the poll cadence.
	catchupInterval = 15 * time.Second
	// catchupSweep is how old a stream's last run may be before it is read
	// with no wake-up.
	catchupSweep = 5 * time.Minute
	// catchupMargin is how much of the lease a run leaves unused, so its last
	// write lands while the lease still holds.
	catchupMargin = 30 * time.Second
	// catchupPage is the page a run reads at a time.
	catchupPage = 200
	// catchupReleaseTimeout bounds the release, made on a context the
	// worker's shutdown does not reach.
	catchupReleaseTimeout = 10 * time.Second

	// The streams.
	streamDocuments = "invoices.documents"
	streamFacts     = "invoices.facts"
	// streamExpenses is Expenses' fact log by seq (delivery C, R2).
	streamExpenses = "expenses.facts"
)

// catchupLease is the lease a claim takes. A test may shorten it.
var catchupLease = 2 * time.Minute

// catchupStreams are the streams in the order a run reads them: documents
// first, so an invoice's item exists before its payment's fact posts; then
// Expenses' facts, which no sales item waits for.
var catchupStreams = []string{streamDocuments, streamFacts, streamExpenses}

// errLeaseLost is a write that found the stream no longer under this claim.
var errLeaseLost = errors.New("accounting: the catch-up's lease was lost")

// enabled reports whether MODULES enables this module; a Deps with no Config
// cannot say and counts as enabled.
func enabled(d module.Deps) bool {
	return d.Config == nil || slices.Contains(d.Config.Modules, moduleName)
}

// The event handlers' names.
const (
	handlerDocumentIssued  = "accounting.catchup.document-issued"
	handlerPaymentReceived = "accounting.catchup.payment-received"
	handlerPaymentRemoved  = "accounting.catchup.payment-removed"
	// handlerExpenses wakes the expenses stream on every event of
	// Expenses' fact log (R2).
	handlerExpenses = "accounting.catchup.expenses"
)

// eventHandlers wake the catch-up for the stream an event belongs to (R7):
// one upsert into this module's own schema, on the delivery's transaction,
// and nothing else — no contract read, no other lock. With the module off, or
// the books not set up, they do nothing.
func eventHandlers(d module.Deps) []events.Handler {
	wake := func(stream string) func(ctx context.Context, tx pgx.Tx, _ events.Delivered) error {
		return func(ctx context.Context, tx pgx.Tx, _ events.Delivered) error {
			if !enabled(d) {
				return nil
			}
			q := store.New(tx)
			if _, err := q.GetSettings(ctx); noRows(err) {
				return nil
			} else if err != nil {
				return err
			}
			return q.RequestCatchUp(ctx, store.RequestCatchUpParams{Stream: stream, Now: clockOf(d)()})
		}
	}
	return []events.Handler{
		{Name: handlerDocumentIssued, Events: []events.EventRef{{Name: contracts.DocumentIssued{}.EventName(), Version: 1}},
			Handle: wake(streamDocuments)},
		{Name: handlerPaymentReceived, Events: []events.EventRef{{Name: contracts.PaymentReceived{}.EventName(), Version: 1}},
			Handle: wake(streamFacts)},
		{Name: handlerPaymentRemoved, Events: []events.EventRef{{Name: contracts.PaymentRemoved{}.EventName(), Version: 1}},
			Handle: wake(streamFacts)},
		{Name: handlerExpenses, Events: expenseEvents(), Handle: wake(streamExpenses)},
	}
}

// expenseEvents is every event Expenses' fact log publishes, at version 1.
func expenseEvents() []events.EventRef {
	out := make([]events.EventRef, 0, len(contracts.ExpenseEventNames))
	for _, name := range contracts.ExpenseEventNames {
		out = append(out, events.EventRef{Name: name, Version: 1})
	}
	return out
}

// clockOf is Deps' clock, time.Now without one.
func clockOf(d module.Deps) func() time.Time {
	if d.Clock != nil {
		return d.Clock
	}
	return time.Now
}

// workers is the module's background work: the catch-up.
func workers(d module.Deps) []worker.Worker { return []worker.Worker{NewCatchUpWorker(d)} }

// CatchUpWorker posts Invoices' sales journal into the books. It implements
// worker.Worker.
type CatchUpWorker struct {
	deps module.Deps
}

var _ worker.Worker = (*CatchUpWorker)(nil)

// NewCatchUpWorker builds the worker over worker mode's Deps: the pool, the
// clock and Invoices' sales journal, which module.Workers resolves.
func NewCatchUpWorker(d module.Deps) *CatchUpWorker { return &CatchUpWorker{deps: d} }

// Name identifies this worker in the runner's logs.
func (w *CatchUpWorker) Name() string { return catchupWorkerName }

// Interval is the poll cadence.
func (w *CatchUpWorker) Interval() time.Duration { return catchupInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done. A
// failing cycle is logged and the loop goes on.
func (w *CatchUpWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(catchupInterval)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger().ErrorContext(ctx, "accounting: the catch-up failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *CatchUpWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}

// RunOnce runs every stream that is due once: nothing before the books are
// set up, and no stream whose journal is not composed — Invoices' two
// without Invoices, Expenses' without Expenses.
func (w *CatchUpWorker) RunOnce(ctx context.Context) error {
	if w.deps.Pool == nil || (w.deps.SalesJournal == nil && w.deps.ExpenseJournal == nil) {
		return nil
	}
	if _, err := store.New(w.deps.Pool).GetSettings(ctx); noRows(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := ensureDefaultRules(ctx, w.deps.Pool, clockOf(w.deps)()); err != nil {
		return err
	}
	var errs []error
	for _, stream := range catchupStreams {
		if !composed(w.deps, stream) {
			continue
		}
		if err := w.runStream(ctx, stream); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", stream, err))
		}
	}
	return errors.Join(errs...)
}

// composed reports whether a stream's journal is in Deps.
func composed(d module.Deps, stream string) bool {
	if stream == streamExpenses {
		return d.ExpenseJournal != nil
	}
	return d.SalesJournal != nil
}

// runStream claims a stream, reads it after its cursor and posts what it
// reads, then releases it.
func (w *CatchUpWorker) runStream(ctx context.Context, stream string) error {
	now := clockOf(w.deps)
	lease := uuid.New()
	until := now().Add(catchupLease)
	var position int64
	claimed := false
	err := db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		at := now()
		if err := q.EnsureCursor(ctx, store.EnsureCursorParams{Stream: stream, Now: at}); err != nil {
			return err
		}
		var err error
		position, err = q.ClaimCursor(ctx, store.ClaimCursorParams{Stream: stream, LeaseID: &lease, LeaseUntil: &until,
			Now: at, SweepBefore: ptr(at.Add(-catchupSweep))})
		if noRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		claimed = true
		return q.TakeRequest(ctx, stream)
	})
	if err != nil || !claimed {
		return err
	}
	more, runErr := w.read(ctx, stream, lease, until, position)
	// The release, on a context the worker's shutdown does not reach, so a
	// claim stopped early does not hold the stream for its lease.
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), catchupReleaseTimeout)
	defer cancel()
	var lastError *string
	if runErr != nil {
		lastError = ptr(truncate(runErr.Error(), 500))
	}
	q := store.New(w.deps.Pool)
	if more && runErr == nil {
		// A backlog asks for itself again (R15), before the lease is let go.
		if err := q.RequestCatchUp(releaseCtx, store.RequestCatchUpParams{Stream: stream, Now: now()}); err != nil {
			runErr = err
		}
	}
	if err := q.ReleaseCursor(releaseCtx, store.ReleaseCursorParams{Stream: stream, LeaseID: &lease,
		Now: ptr(now()), LastError: lastError}); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

// read is one claimed run: page after page from position, each item posted
// in its own transaction, until a short page or the lease's end. more is true
// when it stopped at the lease's margin with items still to post.
func (w *CatchUpWorker) read(ctx context.Context, stream string, lease uuid.UUID, until time.Time, position int64) (more bool, err error) {
	now := clockOf(w.deps)
	for {
		items, err := readAny(ctx, w.deps, stream, position, catchupPage)
		if err != nil {
			return false, err
		}
		for _, it := range items {
			if !now().Before(until.Add(-catchupMargin)) {
				return true, nil
			}
			post := func(tx pgx.Tx) error {
				if _, err := postSource(ctx, tx, it, now()); err != nil {
					return err
				}
				n, err := store.New(tx).AdvanceCursor(ctx, store.AdvanceCursorParams{Stream: stream, LeaseID: &lease,
					Position: it.position, Now: now()})
				if err != nil {
					return err
				}
				if n != 1 {
					return errLeaseLost
				}
				return nil
			}
			err := db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, post)
			if deadlocked(err) {
				err = db.WithTx(ctx, w.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, post)
			}
			if err != nil {
				return false, err
			}
			position = it.position
		}
		if len(items) < catchupPage {
			return false, nil
		}
	}
}

// deadlocked reports whether err is a deadlock Postgres broke (40P01): the
// item's transaction met a person's settlement of the same open item, and is
// tried once more.
func deadlocked(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// sourceItem is one item of a stream: an issued document, or a fact with the
// invoice it is about and that invoice's credit notes when a write-off's VAT
// needs them, or a fact of Expenses' log with the amounts in NOK a person
// entered for its lines in another currency.
type sourceItem struct {
	stream   string
	position int64
	doc      *contracts.JournalDocument
	fact     *contracts.JournalFact
	invoice  *contracts.JournalDocument
	credits  []contracts.JournalDocument
	expense  *contracts.ExpenseFact
	nok      map[int64]*big.Rat
	// nokBy is who entered the NOK amounts (M3): the voucher posted with them
	// is that person's.
	nokBy *uuid.UUID
}

// readAny reads up to limit items of any stream after position, from the
// journal Deps composes for it.
func readAny(ctx context.Context, d module.Deps, stream string, position int64, limit int) ([]sourceItem, error) {
	if stream == streamExpenses {
		return readExpenseStream(ctx, d.ExpenseJournal, position, limit)
	}
	return readStream(ctx, d.SalesJournal, stream, position, limit)
}

// invoiceID is the invoice the item is about: a document's own id, or for a
// credit note the invoice it credits; a fact's invoice.
func (it sourceItem) invoiceID() int64 {
	switch {
	case it.doc != nil && it.doc.Kind == contracts.JournalCreditNote && it.doc.CreditsID != 0:
		return it.doc.CreditsID
	case it.doc != nil:
		return it.doc.ID
	case it.fact != nil:
		return it.fact.InvoiceID
	}
	return 0
}

// readStream reads up to limit items of a stream after position, with no
// transaction open: the documents by number, or the facts by seq with the
// invoice of each write-off or recovery whose VAT is corrected, and its credit
// notes.
func readStream(ctx context.Context, journal contracts.SalesJournal, stream string, position int64, limit int) ([]sourceItem, error) {
	var out []sourceItem
	switch stream {
	case streamDocuments:
		docs, err := journal.Documents(ctx, position, limit)
		if err != nil {
			return nil, err
		}
		for i := range docs {
			out = append(out, sourceItem{stream: stream, position: docs[i].Number, doc: &docs[i]})
		}
	case streamFacts:
		facts, err := journal.Facts(ctx, position, limit)
		if err != nil {
			return nil, err
		}
		for i := range facts {
			it := sourceItem{stream: stream, position: facts[i].Seq, fact: &facts[i]}
			if facts[i].WriteOff != nil && facts[i].WriteOff.VATCorrection {
				if it.invoice, err = journal.Document(ctx, facts[i].InvoiceID); err != nil {
					return nil, err
				}
				if it.invoice != nil {
					if it.credits, err = journal.CreditNotesOf(ctx, facts[i].InvoiceID); err != nil {
						return nil, err
					}
				}
			}
			out = append(out, it)
		}
	default:
		return nil, fmt.Errorf("accounting: no stream %q", stream)
	}
	return out, nil
}

// outcome is what posting a source item came to.
type outcome int

const (
	outcomeSkipped outcome = iota
	outcomePosted
	outcomeUnposted
)

// postSource posts one item on tx (A13–A15, R15): its voucher through the
// engine, with its source key; nothing when it is outside the books or all
// zeros; a row under Needs attention when it cannot be posted, its reason the
// builder's or the engine's refusal's code, or waiting_on when its invoice
// has an earlier row there. Any other error rolls the item back, and the
// stream stops there.
func postSource(ctx context.Context, tx pgx.Tx, it sourceItem, now time.Time) (outcome, error) {
	if it.expense != nil {
		return postExpense(ctx, tx, it, now)
	}
	q := store.New(tx)
	books, err := readSalesBooks(ctx, q)
	if err != nil {
		return outcomeSkipped, err
	}
	var v *Voucher
	switch {
	case it.doc != nil:
		v, err = documentVoucher(books, *it.doc)
	case it.fact != nil:
		in, ferr := factInBooks(ctx, q, books, *it.fact)
		if ferr != nil {
			return outcomeSkipped, ferr
		}
		if in {
			inputs, ferr := readFactInputs(ctx, q, it)
			if ferr != nil {
				return outcomeSkipped, ferr
			}
			v, err = factVoucher(books, *it.fact, inputs)
		}
	}
	var why *unpostable
	if !errors.As(err, &why) && err != nil {
		return outcomeSkipped, err
	}
	if v == nil && why == nil {
		return outcomeSkipped, nil
	}
	// What waits, waits in order (R15): an item of an invoice with an
	// earlier row under Needs attention is held behind it — unless it is in
	// the books already, which Post answers as a repeat.
	src := itemSource(it)
	_, posted, err := postedSource(ctx, q, src)
	if err != nil {
		return outcomeSkipped, err
	}
	if id := it.invoiceID(); id != 0 && !posted {
		blocker, err := q.UnpostedBlocker(ctx, store.UnpostedBlockerParams{InvoiceID: &id, SourceModule: src.Module,
			SourceKind: src.Kind, SourceID: src.ID, SourcePart: src.Part})
		switch {
		case noRows(err):
		case err != nil:
			return outcomeSkipped, err
		default:
			detail := fmt.Sprintf("Waits for the %s of invoice %s, which is under Needs attention: fix and retry that first.",
				blocker.SourceKind, deref(blocker.DocumentNumber))
			return outcomeUnposted, recordUnposted(ctx, q, it, reasonWaitingOn, detail, &blocker.ID, now)
		}
	}
	if why != nil {
		return outcomeUnposted, recordUnposted(ctx, q, it, why.reason, why.detail, nil, now)
	}
	v.At = now
	voucher, err := Post(ctx, tx, *v)
	var refused *refusal
	if errors.As(err, &refused) {
		return outcomeUnposted, recordUnposted(ctx, q, it, refusalReason(refused), refusalDetail(refused), nil, now)
	}
	if err != nil {
		return outcomeSkipped, err
	}
	if err := q.MarkUnpostedPosted(ctx, store.MarkUnpostedPostedParams{VoucherID: &voucher.ID, Now: &now,
		SourceModule: src.Module, SourceKind: src.Kind, SourceID: src.ID, SourcePart: src.Part}); err != nil {
		return outcomeSkipped, err
	}
	return outcomePosted, nil
}

// readFactInputs reads what a fact's voucher is built from in the books (R15):
// for a recovery the write-off's voucher, for an undoing of a write-off or a
// removal of a recovery the voucher it turns back; with the invoice and its
// credit notes the journal answered.
func readFactInputs(ctx context.Context, q *store.Queries, it sourceItem) (factInputs, error) {
	f := it.fact
	in := factInputs{invoice: it.invoice, credits: it.credits}
	var err error
	switch f.Kind {
	case contracts.FactWriteOffUndone:
		in.original, err = readStoredVoucher(ctx, q, contracts.FactWriteOff, f.SubjectID)
	case contracts.FactRecoveredPaymentRemoved:
		in.original, err = readStoredVoucher(ctx, q, contracts.FactRecoveredPayment, f.SubjectID)
	case contracts.FactRecoveredPayment:
		if f.WriteOff != nil {
			in.writeOff, err = readStoredVoucher(ctx, q, contracts.FactWriteOff, f.WriteOff.ID)
		}
	}
	return in, err
}

// readStoredVoucher is the voucher Invoices' source of that kind and id posted
// as, its postings as lines; nil when it has none.
func readStoredVoucher(ctx context.Context, q *store.Queries, kind string, id int64) (*storedVoucher, error) {
	rows, err := q.SourcePostings(ctx, store.SourcePostingsParams{SourceModule: ptr(sourceInvoices), SourceKind: &kind, SourceID: &id})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	out := &storedVoucher{id: rows[0].VoucherID}
	for _, p := range rows {
		l := Line{AccountID: p.AccountID, Amount: mustRat(p.Amount), TaxCodeID: p.TaxCodeID, CustomerID: p.CustomerID,
			SupplierID: p.SupplierID, OpenItemID: p.OpenItemID, ProjectID: p.ProjectID, Text: p.Description,
			TaxSpecification: deref(p.TaxSpecification), PostingRuleID: p.PostingRuleID, PostingRuleVersion: p.PostingRuleVersion}
		if p.TaxRole != nil {
			l.TaxRole = *p.TaxRole
		}
		if p.TaxBase != "" {
			l.TaxBase = mustRat(p.TaxBase)
		}
		out.lines = append(out.lines, l)
	}
	return out, nil
}

// itemSource is an item's source key.
func itemSource(it sourceItem) *Source {
	switch {
	case it.doc != nil:
		return documentSource(*it.doc)
	case it.expense != nil:
		return expenseSource(*it.expense)
	}
	return factSource(*it.fact)
}

// recordUnposted writes the item under Needs attention, or its new reason;
// waitingOn is the row it waits behind, with reason waiting_on.
func recordUnposted(ctx context.Context, q *store.Queries, it sourceItem, reason, detail string, waitingOn *int64, now time.Time) error {
	return q.RecordUnposted(ctx, unpostedParams(it, reason, detail, waitingOn, now))
}

// recordUnpostedFor records a row that names a supplier invoice: the one an
// unapproved expense was made into (L1).
func recordUnpostedFor(ctx context.Context, q *store.Queries, it sourceItem, reason, detail string, invoice *int64, now time.Time) error {
	p := unpostedParams(it, reason, detail, nil, now)
	p.SupplierInvoiceID = invoice
	return q.RecordUnposted(ctx, p)
}

// unpostedParams is the row of an item that did not post.
func unpostedParams(it sourceItem, reason, detail string, waitingOn *int64, now time.Time) store.RecordUnpostedParams {
	src := itemSource(it)
	p := store.RecordUnpostedParams{Stream: it.stream, Position: it.position, SourceModule: src.Module, SourceKind: src.Kind,
		SourceID: src.ID, SourcePart: src.Part, Reason: truncate(reason, 40), Detail: truncate(detail, 500), Now: now,
		WaitingOn: waitingOn}
	if id := it.invoiceID(); id != 0 {
		p.InvoiceID = &id
	}
	switch {
	case it.doc != nil:
		d := it.doc
		p.FactDate, p.DocumentID, p.CustomerID = &d.IssueDate, &d.ID, &d.CustomerID
		p.DocumentNumber, p.Amount = ptr(fmt.Sprint(d.Number)), ptr(d.Gross)
	case it.fact != nil:
		f := it.fact
		p.FactDate, p.DocumentID, p.CustomerID = &f.Date, &f.InvoiceID, &f.CustomerID
		p.DocumentNumber, p.Amount = ptr(fmt.Sprint(f.InvoiceNumber)), ptr(f.AmountNOK)
	case it.expense != nil:
		f := it.expense
		p.FactDate, p.OrderKey = &f.Date, ptr(expenseOrderKey(*f))
		if total, ok := expenseNOKTotal(*f); ok {
			p.Amount = ptr(total)
		}
	}
	if p.Amount != nil {
		if _, ok := parseAmount(*p.Amount); !ok {
			p.Amount = nil
		}
	}
	return p
}

// refusalReason is a refusal's code, "refused" without one.
func refusalReason(r *refusal) string {
	if r.problem.Code != nil {
		return *r.problem.Code
	}
	return "refused"
}

// refusalDetail is what a refusal says: its detail, or its field errors.
func refusalDetail(r *refusal) string {
	if r.problem.Detail != nil {
		return *r.problem.Detail
	}
	out := ""
	if r.problem.Title != nil {
		out = *r.problem.Title
	}
	if r.problem.Errors != nil {
		keys := make([]string, 0, len(*r.problem.Errors))
		for k := range *r.problem.Errors {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			for _, m := range (*r.problem.Errors)[k] {
				out += " " + k + ": " + m
			}
		}
	}
	return out
}

// truncate is s cut to n runes.
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
