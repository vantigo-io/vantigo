package energy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the import worker (energy import design D6): the customers
// import worker's shape (internal/customers/import_worker.go) — lease,
// chunks, nudge, stop, resume, retention — copied rather than shared, since
// two copies are the evidence a shared runner would need (design D2). A cycle
// takes the oldest import asked for and either checks or runs it — the pass
// before the first row finds the in-file duplicate GSRNs and resolves every
// organisation number of the file through the customer directory, once, then
// the rows with no outcome are walked in chunks, each a transaction of its own
// (import.go's runImportRow) — or attaches its pending rows (runAttach). A run
// interrupted resumes at the first row without an outcome; a failure that is
// the run's rather than a row's is written on the import and retried next
// cycle.
//
// Every statement of a cycle runs on the lease's connection, the customers
// worker's rule. The one exception is the customer directory, read once per
// run before the first row and outside every row's transaction: it is the
// customers module's own read, on its own connection from the pool.

const (
	// importWorkerName is what the runner logs this worker as.
	importWorkerName = "energy-import"

	// importLeaseKey is the ASCII string "ENRGIMP1" read as a big-endian
	// 64-bit value — its own key, beside customers' CUSTIMP1 and the other
	// workers': one holding another's lease would silently stall it. The
	// supply periods' lock (lockSupplyPeriods) is the two-int32 overload, a
	// separate key space.
	importLeaseKey int64 = 0x454E5247494D5031

	// importChunkRows is how many rows run between two progress writes and
	// two reads of the stop flag.
	importChunkRows = 500

	// defaultImportPoll and defaultImportRetention are what a worker built
	// from a Deps with no Config falls back on — config.go's own defaults.
	defaultImportPoll      = 5 * time.Second
	defaultImportRetention = 720 * time.Hour
)

// importNudge wakes the worker in-process the moment a check, a run or an
// attach is asked for: a channel of one, so a nudge never blocks a handler.
var importNudge = make(chan struct{}, 1)

// importRunHookForTest, when set, runs once a run has started and before its
// first row, and an error it answers fails the run as a whole. Set only
// through export_test.go's SetImportRunHook; nil in production.
var importRunHookForTest func(ctx context.Context, importID int32) error

func nudgeImportWorker() {
	select {
	case importNudge <- struct{}{}:
	default:
	}
}

// ImportWorker checks, runs and attaches the queued imports. It implements
// worker.Worker.
type ImportWorker struct {
	deps module.Deps
	srv  *server
}

var _ worker.Worker = (*ImportWorker)(nil)

// NewImportWorker builds the worker over d.
func NewImportWorker(d module.Deps) *ImportWorker {
	return &ImportWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *ImportWorker) Name() string { return importWorkerName }

// Interval is the poll cadence between cycles (ENERGY_IMPORT_POLL, default
// five seconds); a nudge from a handler cuts it short.
func (w *ImportWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.EnergyImportPoll > 0 {
		return w.deps.Config.EnergyImportPoll
	}
	return defaultImportPoll
}

// retention is how long a finished or abandoned import is kept
// (ENERGY_IMPORT_RETENTION, default thirty days).
func (w *ImportWorker) retention() time.Duration {
	if w.deps.Config != nil && w.deps.Config.EnergyImportRetention > 0 {
		return w.deps.Config.EnergyImportRetention
	}
	return defaultImportRetention
}

// Run is the worker loop: a cycle, then the next on the tick or the nudge.
func (w *ImportWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("import cycle failed", "worker", importWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-importNudge:
		}
	}
}

// RunCycle is one cycle under the advisory lease, reporting whether it ran:
// every queued import in turn, oldest first, then the sweep. One import
// failing as a whole is written on it and logged, and the cycle ends there.
func (w *ImportWorker) RunCycle(ctx context.Context) (bool, error) {
	return w.underLease(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		q := store.New(conn)
		for {
			if ctx.Err() != nil {
				return nil
			}
			imp, err := q.NextQueuedImport(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			if err != nil {
				return fmt.Errorf("energy: read the import queue: %w", err)
			}
			if err := w.runImport(ctx, conn, imp); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				message := err.Error()
				if failErr := q.FailImport(ctx, store.FailImportParams{ID: imp.ID, Error: &message, UpdatedAt: w.now()}); failErr != nil {
					return fmt.Errorf("energy: record an import's failure: %w", failErr)
				}
				w.logger().Error("energy: an import failed and is tried again next cycle",
					"worker", importWorkerName, "importId", imp.ID, "error", message)
				return nil
			}
		}
		swept, err := q.SweepImports(ctx, w.now().Add(-w.retention()))
		if err != nil {
			return fmt.Errorf("energy: sweep old imports: %w", err)
		}
		if swept > 0 {
			w.logger().Info("old imports swept", "worker", importWorkerName, "imports", swept)
		}
		return nil
	})
}

// importCounts are an import's counts as the worker keeps them while it walks.
type importCounts struct {
	processed, created, updated, failed, skipped, switched, pending, attached int32
}

func countsOf(c store.ImportRowCountsRow) importCounts {
	return importCounts{
		processed: int32(c.Processed), created: int32(c.Created), updated: int32(c.Updated), failed: int32(c.Failed),
		skipped: int32(c.Skipped), switched: int32(c.Switched), pending: int32(c.Pending), attached: int32(c.Attached),
	}
}

func (w *ImportWorker) writeProgress(ctx context.Context, q *store.Queries, id int32, c importCounts) error {
	if err := q.UpdateImportProgress(ctx, store.UpdateImportProgressParams{
		ID: id, Processed: c.processed, Created: c.created, Updated: c.updated, Failed: c.failed, Skipped: c.skipped,
		Switched: c.switched, Pending: c.pending, Attached: c.attached, UpdatedAt: w.now(),
	}); err != nil {
		return fmt.Errorf("energy: write import %d's progress: %w", id, err)
	}
	return nil
}

// runImport is one import's check, run or attach, from where it stands to its
// end or its stop.
func (w *ImportWorker) runImport(ctx context.Context, conn *pgxpool.Conn, imp store.EnergyImport) error {
	q := store.New(conn)
	mapping, err := importMappingFromJSON(imp.Mapping)
	if err != nil {
		return err
	}
	if mapping == nil {
		return errors.New("energy: the import has no mapping")
	}
	l := layoutFor(*mapping, len(imp.Header))
	if err := q.StartImport(ctx, store.StartImportParams{ID: imp.ID, StartedAt: w.now()}); err != nil {
		return fmt.Errorf("energy: start import %d: %w", imp.ID, err)
	}
	if importRunHookForTest != nil {
		if err := importRunHookForTest(ctx, imp.ID); err != nil {
			return err
		}
	}
	if imp.State == string(gen.Attaching) {
		return w.runAttach(ctx, conn, imp)
	}
	dryRun := imp.State == string(gen.Checking)

	duplicates, legalIDs, err := importKeyPass(ctx, q, imp.ID, l)
	if err != nil {
		return err
	}
	customers, err := w.resolveCustomers(ctx, legalIDs)
	if err != nil {
		return err
	}
	rowCounts, err := q.ImportRowCounts(ctx, imp.ID)
	if err != nil {
		return fmt.Errorf("energy: count import %d's rows: %w", imp.ID, err)
	}
	counts := countsOf(rowCounts)
	// The real run switches no more points than the person confirmed (design
	// D4): each switch spends one in its own row's transaction, so a resumed
	// run starts from what is left. The check counts and does not bound.
	budget := switchBudget{}
	if !dryRun {
		budget = switchBudget{bounded: true, left: int(imp.ConfirmedSwitches), importID: imp.ID}
	}
	if err := w.writeProgress(ctx, q, imp.ID, counts); err != nil {
		return err
	}

	for {
		rows, err := q.ImportRowsChunk(ctx, store.ImportRowsChunkParams{ImportID: imp.ID, Limit: importChunkRows})
		if err != nil {
			return fmt.Errorf("energy: read import %d's next rows: %w", imp.ID, err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			rec := fileimport.Record{Row: int(row.RowNo), Cells: row.Cells}
			var result importRowResult
			switch first, dup := duplicates[row.RowNo]; {
			case l.isTotalsLine(rec):
				result = importRowResult{Outcome: string(gen.Skipped), Errs: []importError{{Row: rec.Row,
					Message: "A totals line: no GSRN, and at most one other cell filled in"}}}
			case dup:
				p, _ := l.plan(rec, l.creates())
				result = importRowResult{Outcome: string(gen.Failed), Gsrn: &p.Gsrn, Errs: []importError{{Row: rec.Row, Field: fieldGsrn,
					Message: fmt.Sprintf("Row %d of this file already has this GSRN; a file lists a metering point once", first)}}}
			default:
				p, planErrs := l.plan(rec, l.creates())
				if result, err = w.srv.runImportRow(ctx, conn, imp.ID, l, p, planErrs, customers, &budget, dryRun); err != nil {
					return err
				}
			}
			if dryRun || result.Outcome == string(gen.Failed) || result.Outcome == string(gen.Skipped) {
				if err := writeRowOutcome(ctx, q, imp.ID, row.RowNo, l, result); err != nil {
					return err
				}
			}
			counts.processed++
			switch result.Outcome {
			case string(gen.Created):
				counts.created++
			case string(gen.Updated):
				counts.updated++
			case string(gen.Skipped):
				counts.skipped++
			default:
				counts.failed++
			}
			if result.Link != nil {
				switch *result.Link {
				case linkSwitched:
					counts.switched++
				case linkPending:
					counts.pending++
				}
			}
		}
		if err := w.writeProgress(ctx, q, imp.ID, counts); err != nil {
			return err
		}
		stop, err := q.ImportStopRequested(ctx, imp.ID)
		if err != nil {
			return fmt.Errorf("energy: read import %d's stop flag: %w", imp.ID, err)
		}
		if stop {
			return w.finish(ctx, q, imp, dryRun, true)
		}
	}
	return w.finish(ctx, q, imp, dryRun, false)
}

// writeRowOutcome writes a row's result outside its transaction: a dry run's
// (its transaction rolled back), a failed row's and a skipped one's.
func writeRowOutcome(ctx context.Context, q *store.Queries, importID, rowNo int32, l importLayout, result importRowResult) error {
	errsJSON, err := importRowErrorsJSON(l, result.Errs)
	if err != nil {
		return err
	}
	outcome := result.Outcome
	if err := q.WriteImportRowOutcome(ctx, store.WriteImportRowOutcomeParams{
		ImportID: importID, RowNo: rowNo, Outcome: &outcome, MeteringPointID: result.PointID, Gsrn: result.Gsrn,
		CustomerID: result.CustomerID, Link: result.Link, SwitchFrom: result.SwitchFrom, SupplyStart: result.SupplyStart, Errors: errsJSON,
	}); err != nil {
		return fmt.Errorf("energy: write import %d row %d's outcome: %w", importID, rowNo, err)
	}
	return nil
}

// importRowErrorsJSON is a row's errors as import_rows.errors holds them: the
// contract's own shape, NULL for none.
func importRowErrorsJSON(l importLayout, errs []importError) ([]byte, error) {
	if len(errs) == 0 {
		return nil, nil
	}
	out := make([]gen.EnergyImportRowError, 0, len(errs))
	for _, e := range errs {
		out = append(out, l.wire(e))
	}
	return json.Marshal(out)
}

// importRowErrorsFromJSON reads them back; an empty list for a row with none.
func importRowErrorsFromJSON(data []byte) ([]gen.EnergyImportRowError, error) {
	out := []gen.EnergyImportRowError{}
	if len(data) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("energy: read an import row's errors: %w", err)
	}
	return out, nil
}

// finish closes a check or a run: a check stopped goes back to draft with its
// results cleared; a check finished is checked; a run, finished or stopped, is
// done. The counts are recounted from the rows, the one source of truth.
func (w *ImportWorker) finish(ctx context.Context, q *store.Queries, imp store.EnergyImport, dryRun, stopped bool) error {
	now := w.now()
	if dryRun && stopped {
		if err := q.ClearImportRowResults(ctx, imp.ID); err != nil {
			return fmt.Errorf("energy: clear import %d's results: %w", imp.ID, err)
		}
		if err := q.FinishImport(ctx, store.FinishImportParams{ID: imp.ID, State: string(gen.Draft), UpdatedAt: now}); err != nil {
			return fmt.Errorf("energy: finish import %d: %w", imp.ID, err)
		}
		w.logger().Info("import check stopped", "worker", importWorkerName, "importId", imp.ID)
		return nil
	}
	state := gen.Checked
	if !dryRun {
		state = gen.Done
	}
	return w.close(ctx, q, imp, state, now, stopped)
}

// close writes an import's final state with its counts recounted from the
// rows.
func (w *ImportWorker) close(ctx context.Context, q *store.Queries, imp store.EnergyImport, state gen.EnergyImportState, finishedAt time.Time, stopped bool) error {
	rowCounts, err := q.ImportRowCounts(ctx, imp.ID)
	if err != nil {
		return fmt.Errorf("energy: count import %d's rows: %w", imp.ID, err)
	}
	c := countsOf(rowCounts)
	if err := q.FinishImport(ctx, store.FinishImportParams{
		ID: imp.ID, State: string(state), Processed: c.processed, Created: c.created, Updated: c.updated, Failed: c.failed,
		Skipped: c.skipped, Switched: c.switched, Pending: c.pending, Attached: c.attached, FinishedAt: &finishedAt, UpdatedAt: w.now(),
	}); err != nil {
		return fmt.Errorf("energy: finish import %d: %w", imp.ID, err)
	}
	w.logger().Info("import finished", "worker", importWorkerName, "importId", imp.ID, "state", state, "stopped", stopped,
		"rows", imp.RowCount, "processed", c.processed, "created", c.created, "updated", c.updated, "failed", c.failed,
		"skipped", c.skipped, "switched", c.switched, "pending", c.pending, "attached", c.attached)
	return nil
}

// importKeyPass is the pass before the first row (design D3, D4), over every
// row of the file whatever its outcome, so a run that resumes still sees the
// first: of the rows that carry one GSRN, every row but the first, keyed to
// the first's number — refused on the second, in the check and the run alike
// — and every distinct valid organisation number the file names, to resolve
// once.
func importKeyPass(ctx context.Context, q *store.Queries, importID int32, l importLayout) (map[int32]int32, []string, error) {
	gsrnColumn, legalIDColumn, fixedLegalID := l.keyColumnsOf()
	cells, err := q.ImportRowKeyCells(ctx, store.ImportRowKeyCellsParams{ImportID: importID, GsrnColumn: gsrnColumn, LegalIDColumn: legalIDColumn})
	if err != nil {
		return nil, nil, fmt.Errorf("energy: read import %d's keys: %w", importID, err)
	}
	first := map[string]int32{}
	duplicates := map[int32]int32{}
	seenID := map[string]bool{}
	var legalIDs []string
	addID := func(raw string) {
		if id := stripWhitespace(raw); validNorwegianOrgNumber(id) && !seenID[id] {
			seenID[id] = true
			legalIDs = append(legalIDs, id)
		}
	}
	if l.mapped(fieldCustomerLegalID) && legalIDColumn == 0 {
		addID(fixedLegalID)
	}
	for _, c := range cells {
		if legalIDColumn != 0 {
			addID(c.LegalID)
		}
		g := strings.TrimSpace(c.Gsrn)
		if g == "" {
			continue
		}
		if row, seen := first[g]; seen {
			duplicates[c.RowNo] = row
			continue
		}
		first[g] = c.RowNo
	}
	return duplicates, legalIDs, nil
}

// resolveCustomers is every organisation number of the file looked up through
// the customer directory in one round trip, before the first row and outside
// every row's transaction (design D4). With no directory composed — the
// customers module off — every row naming a number is refused.
func (w *ImportWorker) resolveCustomers(ctx context.Context, legalIDs []string) (importCustomers, error) {
	if w.deps.Directory == nil {
		return importCustomers{off: true}, nil
	}
	if len(legalIDs) == 0 {
		return importCustomers{holders: map[string][]contracts.CustomerEntry{}}, nil
	}
	holders, err := w.deps.Directory.CustomersByLegalID(ctx, importLegalCountry, legalIDs)
	if err != nil {
		return importCustomers{}, fmt.Errorf("energy: resolve the file's organisation numbers: %w", err)
	}
	return importCustomers{holders: holders}, nil
}

// runAttach is the attach (design D6): every pending row not yet tried by this
// attach, in chunks, each in its own transaction under the point's
// supply-period lock — the point's active period read again now, and the
// row's customer assigned from the attach's start, or switched to there
// within the switches the person confirmed, or found to have it already
// (same). A row that cannot be attached stays pending with its error, so the
// attach can be asked for again. The customers the rows name are read once,
// before the first row.
func (w *ImportWorker) runAttach(ctx context.Context, conn *pgxpool.Conn, imp store.EnergyImport) error {
	q := store.New(conn)
	if imp.AttachStart == nil {
		return errors.New("energy: the import has no attach start")
	}
	start := imp.AttachStart.UTC()
	entries, err := w.attachCustomers(ctx, q, imp.ID)
	if err != nil {
		return err
	}
	rowCounts, err := q.ImportRowCounts(ctx, imp.ID)
	if err != nil {
		return fmt.Errorf("energy: count import %d's rows: %w", imp.ID, err)
	}
	counts := countsOf(rowCounts)
	budget := switchBudget{bounded: true, left: int(imp.ConfirmedSwitches), importID: imp.ID}
	for {
		rows, err := q.PendingRowsChunk(ctx, store.PendingRowsChunkParams{ImportID: imp.ID, Limit: importChunkRows})
		if err != nil {
			return fmt.Errorf("energy: read import %d's next pending rows: %w", imp.ID, err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			link, switchFrom, refusal, err := w.attachRow(ctx, conn, imp.ID, row, start, entries, &budget)
			if err != nil {
				return err
			}
			if refusal != "" {
				errsJSON, err := json.Marshal([]gen.EnergyImportRowError{{Message: refusal}})
				if err != nil {
					return err
				}
				pending := linkPending
				if err := q.WriteImportRowAttach(ctx, store.WriteImportRowAttachParams{
					ImportID: imp.ID, RowNo: row.RowNo, Link: &pending, SwitchFrom: switchFrom, Errors: errsJSON,
				}); err != nil {
					return fmt.Errorf("energy: write import %d row %d's attach: %w", imp.ID, row.RowNo, err)
				}
				continue
			}
			counts.pending--
			if link == linkAttached {
				counts.attached++
			}
		}
		if err := w.writeProgress(ctx, q, imp.ID, counts); err != nil {
			return err
		}
		stop, err := q.ImportStopRequested(ctx, imp.ID)
		if err != nil {
			return fmt.Errorf("energy: read import %d's stop flag: %w", imp.ID, err)
		}
		if stop {
			return w.close(ctx, q, imp, gen.Done, w.now(), true)
		}
	}
	return w.close(ctx, q, imp, gen.Done, w.now(), false)
}

// attachCustomers is the customers the attach's untried rows name, read once
// through the directory before the first row; nil with no directory composed.
func (w *ImportWorker) attachCustomers(ctx context.Context, q *store.Queries, importID int32) (map[int32]contracts.CustomerEntry, error) {
	if w.deps.Directory == nil {
		return nil, nil
	}
	ids, err := q.PendingRowCustomerIDs(ctx, importID)
	if err != nil {
		return nil, fmt.Errorf("energy: read import %d's pending customers: %w", importID, err)
	}
	entries, err := w.deps.Directory.Customers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("energy: resolve import %d's pending customers: %w", importID, err)
	}
	byID := make(map[int32]contracts.CustomerEntry, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
	}
	return byID, nil
}

// attachRow attaches one pending row in its own transaction, its result
// written in the same one. A refusal is answered as its message, the row
// written as still pending by the caller; any other error is the run's.
func (w *ImportWorker) attachRow(ctx context.Context, conn *pgxpool.Conn, importID int32, row store.PendingRowsChunkRow, start time.Time,
	entries map[int32]contracts.CustomerEntry, budget *switchBudget) (string, *int32, string, error) {
	if row.CustomerID == nil || row.MeteringPointID == nil {
		return "", nil, "This row names no customer or no metering point to attach", nil
	}
	switch entry, ok := entries[*row.CustomerID]; {
	case w.deps.Directory == nil:
		return "", nil, customersModuleOffMessage, nil
	case !ok:
		return "", nil, fmt.Sprintf("Customer %d no longer exists", *row.CustomerID), nil
	case customerRefusal(entry) != "":
		return "", nil, customerRefusal(entry), nil
	}
	var link string
	var switchFrom *int32
	err := db.RetrySerializable(ctx, importRowAttempts, func() error {
		attempt := *budget
		link, switchFrom = "", nil
		return db.WithTx(ctx, conn, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			link, switchFrom, err = attachLink(ctx, tx, int(row.RowNo), *row.MeteringPointID, *row.CustomerID, start, &attempt)
			if err != nil {
				return err
			}
			var supplyStart *time.Time
			if link == linkAttached {
				supplyStart = &start
			}
			if err := store.New(tx).WriteImportRowAttach(ctx, store.WriteImportRowAttachParams{
				ImportID: importID, RowNo: row.RowNo, Link: &link, SwitchFrom: switchFrom, SupplyStart: supplyStart, Errors: nil,
			}); err != nil {
				return err
			}
			*budget = attempt
			return nil
		})
	})
	var refused *importRowRefusal
	switch {
	case errors.As(err, &refused):
		return "", nil, refused.errs[0].Message, nil
	case err != nil:
		return "", nil, "", err
	}
	return link, switchFrom, "", nil
}

// attachLink is one pending row's link at attach time (design D6), the
// point's active period read again under its lock: the row's customer's
// already, same; none, a period from start; another customer's, a switch at
// start within the budget.
func attachLink(ctx context.Context, tx pgx.Tx, row int, pointID, customerID int32, start time.Time, budget *switchBudget) (string, *int32, error) {
	if err := lockSupplyPeriods(ctx, tx, pointID); err != nil {
		return "", nil, err
	}
	active, err := store.New(tx).GetActiveOpenSupplyPeriod(ctx, pointID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := assignSupplyPeriod(ctx, tx, row, pointID, customerID, start); err != nil {
			return "", nil, err
		}
		return linkAttached, nil, nil
	case err != nil:
		return "", nil, err
	case active.CustomerID == customerID:
		return linkSame, nil, nil
	}
	from := active.CustomerID
	if err := switchSupplyPeriodFor(ctx, tx, row, pointID, from, customerID, start, budget); err != nil {
		return "", nil, err
	}
	return linkAttached, &from, nil
}

// underLease is the customers import worker's lease, on this worker's own
// key: one connection a cycle, the lease taken and released on it, and every
// statement of the cycle run on it.
func (w *ImportWorker) underLease(ctx context.Context, action func(context.Context, *pgxpool.Conn) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("energy: acquire a connection for the import lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, importLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("energy: take the import lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the energy import lease is held by another replica; skipping this cycle",
			"worker", importWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, importLeaseKey); err != nil {
			w.logger().Error("releasing the import lease failed; discarding the connection",
				"worker", importWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx, conn)
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers.
func (w *ImportWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *ImportWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
