package invoices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-billing worker (energy consumption billing
// design D10; billing runs plan readings 4–7): every billing run's items are
// carried by it, one at a time, under a row lease — the reminder worker's
// shape (reminder_worker.go). A claim takes the oldest due queued item of a
// run queued or running, moves the run to running, and carries the item on a
// context bounded by the lease less a minute, under the run's creator as its
// principal (readings on the record 10), so every callerID the code path
// reads — the draft's creator, the issue's issuer, the holders' IssuedBy — is
// the creator's. The run does not judge the creator's permissions again: they
// were judged when it was made.
//
// An item, in order (plan reading 7): Energy off is skipped
// consumption_unavailable; Settle for the customer through the start of the
// day after the run's through, on the pool, the settlements a live document
// holds left out — this item's own documents of an earlier attempt among
// them, which are carried on instead; nothing left and nothing made before is
// skipped no_consumption_due. The rest, in the wizard's order, is packed
// point by point into documents of at most maxLines lines, each made by the
// wizard's own core (fromConsumption) with its settlementIds and recorded on
// the item at once, then — when the run issues — issued by the issue's own
// core (issueDraft) for today. A wizard refusal skips the item with its code,
// naming the metering point when it names a settlement; an issue refusal
// fails it; nothing after a refusal is tried, and nothing refused is retried.
// An error counts an attempt and puts the item back, due after one minute,
// then two; the third fails it billing_error. After each item the run is
// done once it has no queued item. Every call out of the module is made on
// the pool, never under a lock; every write of a claim names its lease.

const (
	// billingWorkerName is the worker's name in the runner's logs.
	billingWorkerName = "invoices-billing"
	// billingWorkerInterval is the poll cadence between cycles.
	billingWorkerInterval = 5 * time.Second
	// billingPerCycle is the most items one cycle carries.
	billingPerCycle = 5
	// billingDefaultLease is the lease a claim takes: an item settles, drafts
	// and issues several documents, each a PDF stored.
	billingDefaultLease = 10 * time.Minute
	// billingLeaseMargin is how much of the lease the item's context leaves
	// unused, so its outcome is written while the lease still holds.
	billingLeaseMargin = time.Minute
	// billingMarkTimeout bounds each write of a claim's outcome, made on a
	// context the worker's shutdown does not reach.
	billingMarkTimeout = 10 * time.Second
	// billingMaxAttempts is the attempts an item has: the last error fails
	// it.
	billingMaxAttempts = 3

	// reasonBillingError is the detail of an item that failed on errors: the
	// error itself is logged, never recorded.
	reasonBillingError = "The item could not be billed after three attempts; the server log names the error."
)

// billingLease is the lease a claim takes. A test may shorten it.
var billingLease = billingDefaultLease

// errBillingLeaseLost is a lease-checked write that found the item no
// longer under this claim: whatever happens to it next is another actor's.
var errBillingLeaseLost = errors.New("invoices: the billing run item is no longer under this claim")

// BillingWorker carries the billing runs' items. It implements
// worker.Worker; Module.Workers registers it always — a run is made only
// while Energy is composed, and an item judges Energy again.
type BillingWorker struct {
	srv *server
	// srvErr is newServer's failure, kept so the worker still starts and
	// every cycle says why it does nothing.
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*BillingWorker)(nil)

// NewBillingWorker builds the worker over worker mode's Deps: the pool, the
// clock, the directory, Energy's Consumption slot and the holders the issue
// stamps through, and the object store the issued PDFs go to.
func NewBillingWorker(d module.Deps) *BillingWorker {
	srv, err := newServer(d)
	return &BillingWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *BillingWorker) Name() string { return billingWorkerName }

// Interval is the poll cadence between cycles.
func (w *BillingWorker) Interval() time.Duration { return billingWorkerInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done. A
// failing cycle is logged and the loop goes on.
func (w *BillingWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(billingWorkerInterval)
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: a billing worker cycle failed", "worker", billingWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle carries at most billingPerCycle items, one at a time, and stops
// early when none is due.
func (w *BillingWorker) RunCycle(ctx context.Context) error {
	if err := w.sweep(ctx); err != nil {
		return err
	}
	for range billingPerCycle {
		if ctx.Err() != nil {
			return nil
		}
		processed, err := w.ProcessOne(ctx)
		if err != nil || !processed {
			return err
		}
	}
	return nil
}

// sweep ends what no claim will: a running run with no queued item left —
// its last item recorded, its FinishBillingRun lost to a crash or a failed
// write — is done, and a queued item of a cancelled run whose lease ran out
// — its claim never recorded an outcome — is cancelled (the review's
// findings). Without it the first would refuse every new run.
func (w *BillingWorker) sweep(ctx context.Context) error {
	if w.srvErr != nil {
		return w.srvErr
	}
	q, now := store.New(w.deps.Pool), w.now()
	if _, err := q.CancelAbandonedBillingRunItems(ctx, now); err != nil {
		return fmt.Errorf("invoices: cancel the abandoned items of cancelled billing runs: %w", err)
	}
	if _, err := q.FinishIdleBillingRuns(ctx, now); err != nil {
		return fmt.Errorf("invoices: finish the billing runs with nothing left: %w", err)
	}
	return nil
}

// ProcessOne claims at most one due item and carries it to its outcome,
// answering whether an item was claimed. An error carrying the item is
// recorded on it (an attempt counted) and logged, not answered: only a
// failure to claim or to record is.
func (w *BillingWorker) ProcessOne(ctx context.Context) (bool, error) {
	if w.srvErr != nil {
		return false, w.srvErr
	}
	now := w.now()
	lease := strings.ReplaceAll(uuid.NewString(), "-", "")
	q := store.New(w.deps.Pool)
	row, err := q.ClaimBillingRunItem(ctx, store.ClaimBillingRunItemParams{LeaseID: lease, LeaseUntil: now.Add(billingLease), Now: now})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("invoices: claim a billing run item: %w", err)
	}
	c := &billingClaim{w: w, s: w.srv, row: row, lease: lease}
	// One err for the whole chain: a step's failure, and the retry that
	// records it, is what ProcessOne answers.
	if err = q.StartBillingRun(ctx, store.StartBillingRunParams{Now: now, ID: row.RunID}); err != nil {
		err = c.retry(ctx, fmt.Errorf("invoices: start billing run %d: %w", row.RunID, err))
	} else if c.run, err = q.GetBillingRun(ctx, row.RunID); err != nil {
		err = c.retry(ctx, fmt.Errorf("invoices: read billing run %d: %w", row.RunID, err))
	} else {
		err = c.carry(ctx)
	}
	if err != nil {
		return true, err
	}
	return true, c.finishRun(ctx)
}

// billingClaim is one claimed item: the row as the claim saw it, its run and
// its lease.
type billingClaim struct {
	w     *BillingWorker
	s     *server
	row   store.InvoicesBillingRunItem
	run   store.InvoicesBillingRun
	lease string
}

// itemOutcome is how an item ended: its status, and on skipped or failed
// the refusal's code and detail.
type itemOutcome struct {
	status       string
	code, detail string
}

func skipped(code, detail string) itemOutcome {
	return itemOutcome{status: itemSkipped, code: code, detail: detail}
}

// carry is plan reading 7 for the claimed item, on a context bounded by the
// lease less its margin and carrying the run's creator as the principal;
// its outcome, or the error's attempt, is recorded on a context of its own.
func (c *billingClaim) carry(ctx context.Context) error {
	itemCtx, cancel := context.WithTimeout(ctx, max(billingLease-billingLeaseMargin, billingLeaseMargin))
	defer cancel()
	itemCtx = contracts.WithPrincipal(itemCtx, contracts.Principal{UserID: c.run.CreatedByUserID})
	out, err := c.billRecovered(itemCtx)
	switch {
	case ctx.Err() != nil && err != nil:
		// The worker is stopping: the item goes back uncounted, for the next
		// claim — a deploy is not an attempt.
		return c.release(ctx)
	case errors.Is(err, errBillingLeaseLost):
		c.w.logger().DebugContext(ctx, "invoices: a billing run item moved under the claim; nothing recorded",
			"worker", billingWorkerName, "item_id", c.row.ID, "run_id", c.row.RunID)
		return nil
	case err != nil:
		return c.retry(ctx, err)
	}
	return c.complete(ctx, out)
}

// billRecovered is bill with a panic turned into an error, so it counts an
// attempt as any error does: an item that panics every time fails after
// its last attempt instead of killing the worker and being claimed again
// after every restart.
func (c *billingClaim) billRecovered(ctx context.Context) (out itemOutcome, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = itemOutcome{}, fmt.Errorf("invoices: billing customer %d panicked: %v", c.row.CustomerID, r)
		}
	}()
	return c.bill(ctx)
}

// release puts the claimed item back, due at once and uncounted, on a
// context the worker's shutdown does not reach.
func (c *billingClaim) release(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingMarkTimeout)
	defer cancel()
	n, err := store.New(c.w.deps.Pool).ReleaseBillingRunItem(ctx, store.ReleaseBillingRunItemParams{
		Now: c.w.now(), ID: c.row.ID, LeaseID: c.lease,
	})
	return c.done(ctx, "release", n, err)
}

// bill is the item's steps up to its outcome.
func (c *billingClaim) bill(ctx context.Context) (itemOutcome, error) {
	s, customer := c.s, c.row.CustomerID
	// (1) Energy off.
	if s.deps.Consumption == nil {
		p := consumptionUnavailable()
		return skipped(codeConsumptionUnavailable, *p.Detail), nil
	}
	// (2) The settlements through the run's date, on the pool, less what a
	// live document holds — this item's documents of an earlier attempt
	// among them, carried on below.
	through := utcDay(c.run.Through.Time)
	page, err := s.settle(ctx, contracts.SettleRequest{CustomerIDs: []int32{customer}, Through: throughInstant(through)})
	if err != nil {
		return itemOutcome{}, fmt.Errorf("invoices: settle customer %d's consumption: %w", customer, err)
	}
	if page.More {
		return skipped(codeTooManySources, fmt.Sprintf(
			"Energy has more than %d settlements to invoice for this customer; invoice them from the customer's Consumption section.",
			contracts.MaxBillableRows)), nil
	}
	q := store.New(s.deps.Pool)
	held, err := settlementHolds(ctx, q, page.Settlements)
	if err != nil {
		return itemOutcome{}, err
	}
	gsrns := make(map[int64]string, len(page.Settlements))
	var left []contracts.Settlement
	for _, st := range page.Settlements {
		gsrns[st.ID] = st.GSRN
		if _, ok := held[st.ID]; !ok {
			left = append(left, st)
		}
	}
	earlier, err := c.earlierDocuments(ctx, q)
	if err != nil {
		return itemOutcome{}, err
	}
	if len(left) == 0 && len(earlier) == 0 {
		return skipped(codeNoConsumptionDue, fmt.Sprintf(
			"No consumption of this customer's is left to invoice through %s.", through.Format(time.DateOnly))), nil
	}

	// (4) An earlier attempt's drafts are issued first when the run issues.
	if c.run.Issue {
		for _, d := range earlier {
			if d.Status != statusDraft {
				continue
			}
			if out, refused, err := c.issue(ctx, d.ID); refused || err != nil {
				return out, err
			}
		}
	}
	// (3) The split, then (4) each document drafted, recorded and issued.
	sortSettlements(left)
	for _, ids := range packDocuments(left) {
		out, err := s.fromConsumption(ctx, gen.InvoicesFromConsumptionRequest{
			CustomerId: customer, Through: ptr(wireDate(through)), SettlementIds: &ids,
		})
		switch {
		case err != nil:
			return itemOutcome{}, err
		case out.notFound:
			return itemOutcome{}, fmt.Errorf("invoices: the wizard found no target for customer %d's run item", customer)
		case out.invalid != nil:
			return skipped(codeInvalidRequest, fieldErrorsDetail(out.invalid)), nil
		case out.refusal != nil:
			return skipped(problemCode(*out.refusal), refusalDetail(*out.refusal, gsrns)), nil
		}
		if err := c.addInvoice(ctx, out.doc.ID); err != nil {
			return itemOutcome{}, err
		}
		if c.run.Issue {
			if out, refused, err := c.issue(ctx, out.doc.ID); refused || err != nil {
				return out, err
			}
		}
	}
	if c.run.Issue {
		return itemOutcome{status: itemIssued}, nil
	}
	return itemOutcome{status: itemDrafted}, nil
}

// earlierDocuments is what still exists of the documents this item made on
// an earlier attempt, in the order it made them.
func (c *billingClaim) earlierDocuments(ctx context.Context, q *store.Queries) ([]store.BillingRunDocumentsRow, error) {
	if len(c.row.InvoiceIds) == 0 {
		return nil, nil
	}
	rows, err := q.BillingRunDocuments(ctx, c.row.InvoiceIds)
	if err != nil {
		return nil, fmt.Errorf("invoices: read billing run item %d's documents: %w", c.row.ID, err)
	}
	byID := make(map[int64]store.BillingRunDocumentsRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	var out []store.BillingRunDocumentsRow
	for _, id := range c.row.InvoiceIds {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// issue is the issue's core on one of the item's drafts, for today. refused
// says the issue refused it — the item fails with the refusal's code — and a
// draft deleted since is passed over.
func (c *billingClaim) issue(ctx context.Context, id int64) (itemOutcome, bool, error) {
	out, err := c.s.issueDraft(ctx, id, nil)
	switch {
	case err != nil:
		return itemOutcome{}, false, err
	case out.unavailable != nil:
		return itemOutcome{status: itemFailed, code: problemCode(*out.unavailable), detail: problemDetail(*out.unavailable)}, true, nil
	case out.refusal != nil:
		return itemOutcome{status: itemFailed, code: problemCode(*out.refusal), detail: problemDetail(*out.refusal)}, true, nil
	}
	return itemOutcome{}, false, nil
}

// packDocuments is the split (D7, D10, plan reading 7.3): settlements in the
// wizard's order, grouped by metering point and packed, point by point, into
// documents of at most maxLines lines, a settlement counted as the most lines
// energyLines makes of it. A point whose settlements alone exceed the limit
// is a document of its own, which the wizard refuses too_many_lines.
func packDocuments(sorted []contracts.Settlement) [][]int64 {
	var docs [][]int64
	var current []int64
	lines := 0
	for i := 0; i < len(sorted); {
		j, point := i, 0
		for ; j < len(sorted) && sorted[j].GSRN == sorted[i].GSRN; j++ {
			point += max(1, len(sorted[j].Lines))
		}
		if len(current) > 0 && lines+point > maxLines {
			docs, current, lines = append(docs, current), nil, 0
		}
		for _, st := range sorted[i:j] {
			current = append(current, st.ID)
		}
		lines += point
		i = j
	}
	if len(current) > 0 {
		docs = append(docs, current)
	}
	return docs
}

func problemCode(p gen.InvoicesConflictProblem) string {
	if p.Code != nil {
		return *p.Code
	}
	// A refusal without a code is a revision conflict, which the run's own
	// drafts never meet; it is still recorded as refused.
	return codeInvalidRequest
}

func problemDetail(p gen.InvoicesConflictProblem) string {
	if p.Detail != nil {
		return *p.Detail
	}
	return ""
}

// refusalDetail is a wizard refusal's detail, prefixed with the metering
// point when it names a settlement the item settled.
func refusalDetail(p gen.InvoicesConflictProblem, gsrns map[int64]string) string {
	detail := problemDetail(p)
	if p.SourceId != nil {
		if gsrn, ok := gsrns[*p.SourceId]; ok {
			return fmt.Sprintf("Metering point %s: %s", gsrn, detail)
		}
	}
	return detail
}

// fieldErrorsDetail is a wizard 400's field errors as one sentence, by
// field.
func fieldErrorsDetail(errs map[string][]string) string {
	var parts []string
	for _, field := range slices.Sorted(maps.Keys(errs)) {
		parts = append(parts, field+": "+strings.Join(errs[field], "; "))
	}
	return strings.Join(parts, ". ")
}

// addInvoice records a document the item made, at once, lease-checked.
func (c *billingClaim) addInvoice(ctx context.Context, invoiceID int64) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingMarkTimeout)
	defer cancel()
	n, err := store.New(c.w.deps.Pool).AddBillingRunItemInvoice(wctx, store.AddBillingRunItemInvoiceParams{
		InvoiceID: invoiceID, ID: c.row.ID, LeaseID: c.lease,
	})
	if err != nil {
		return fmt.Errorf("invoices: record document %d on billing run item %d: %w", invoiceID, c.row.ID, err)
	}
	if n == 0 {
		return errBillingLeaseLost
	}
	return nil
}

// complete records the item's outcome.
func (c *billingClaim) complete(ctx context.Context, out itemOutcome) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingMarkTimeout)
	defer cancel()
	n, err := store.New(c.w.deps.Pool).CompleteBillingRunItem(ctx, store.CompleteBillingRunItemParams{
		Status: out.status, Code: nonEmpty(out.code), Detail: nonEmpty(out.detail), Now: c.w.now(), ID: c.row.ID, LeaseID: c.lease,
	})
	return c.done(ctx, "complete", n, err)
}

// retry counts an attempt that ended on err and puts the item back, due
// after a minute, then two — or ends it cancelled when its run was cancelled
// meanwhile (RetryBillingRunItem); the last attempt fails it billing_error.
// The error is logged, never recorded.
func (c *billingClaim) retry(ctx context.Context, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingMarkTimeout)
	defer cancel()
	q, now, attempt := store.New(c.w.deps.Pool), c.w.now(), c.row.Attempts+1
	if attempt >= billingMaxAttempts {
		c.w.logger().ErrorContext(ctx, "invoices: a billing run item failed after its last attempt",
			"worker", billingWorkerName, "item_id", c.row.ID, "run_id", c.row.RunID, "attempt", attempt, "error", cause.Error())
		n, err := q.FailBillingRunItem(ctx, store.FailBillingRunItemParams{
			Code: codeBillingError, Detail: reasonBillingError, Now: now, ID: c.row.ID, LeaseID: c.lease,
		})
		return c.done(ctx, "fail", n, err)
	}
	c.w.logger().WarnContext(ctx, "invoices: a billing run item failed; it is tried again",
		"worker", billingWorkerName, "item_id", c.row.ID, "run_id", c.row.RunID, "attempt", attempt, "error", cause.Error())
	n, err := q.RetryBillingRunItem(ctx, store.RetryBillingRunItemParams{
		NextAttemptAt: now.Add(time.Duration(attempt) * time.Minute), Now: now, ID: c.row.ID, LeaseID: c.lease,
	})
	return c.done(ctx, "reschedule", n, err)
}

// finishRun marks the run done once it has no queued item.
func (c *billingClaim) finishRun(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), billingMarkTimeout)
	defer cancel()
	if _, err := store.New(c.w.deps.Pool).FinishBillingRun(ctx, store.FinishBillingRunParams{Now: c.w.now(), ID: c.row.RunID}); err != nil {
		return fmt.Errorf("invoices: finish billing run %d: %w", c.row.RunID, err)
	}
	return nil
}

// done reports a lease-checked write: 0 rows is another actor's move — a
// newer claim after the lease ran out — and only worth a debug line.
func (c *billingClaim) done(ctx context.Context, what string, n int64, err error) error {
	if err != nil {
		return fmt.Errorf("invoices: %s billing run item %d: %w", what, c.row.ID, err)
	}
	if n == 0 {
		c.w.logger().DebugContext(ctx, "invoices: a billing run item moved under the claim; nothing recorded",
			"worker", billingWorkerName, "item_id", c.row.ID, "run_id", c.row.RunID, "step", what)
	}
	return nil
}

func (w *BillingWorker) now() time.Time { return w.deps.Clock().UTC() }

func (w *BillingWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
