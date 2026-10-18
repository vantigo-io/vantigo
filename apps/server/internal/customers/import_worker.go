package customers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the import worker (customers import wizard design D5, D6):
// the one that checks and runs the files people upload. A cycle takes the
// oldest import asked for, resolves what every row shares — the uploader as
// actor, the vocabularies, the rows that repeat a legal identity — and walks
// the rows with no outcome in chunks, each row a transaction of its own
// (import.go's runImportRow), writing progress after every chunk and reading
// whether a stop was asked for. A run interrupted — the process gone, the
// context cancelled — resumes at the first row without an outcome next
// cycle, and in the real run a row's outcome commits with the row, so nothing
// is written twice. A failure that is the run's rather than a row's is
// written on the import and retried next cycle; it is never silently dropped.
//
// Everything a cycle does runs on the lease's connection, the anonymisation
// worker's rule (PR #124): a worker holding one connection and asking the
// pool for another deadlocked the api at startup when the pool was small.

const (
	// importWorkerName is what the runner logs this worker as, in the
	// <module>-<worker> spelling of the three beside it.
	importWorkerName = "customers-import"

	// importLeaseKey is the ASCII string "CUSTIMP1" read as a big-endian
	// 64-bit value — its own key, not the other workers': one holding
	// another's lease would silently stall it.
	importLeaseKey int64 = 0x43555354494D5031

	// importChunkRows is how many rows run between two progress writes and
	// two reads of the stop flag: a few seconds of rows, so a stop lands
	// within them and a progress bar moves.
	importChunkRows = 500

	// defaultImportPoll and defaultImportRetention are what a worker built
	// from a Deps with no Config falls back on — config.go's own defaults.
	defaultImportPoll      = 5 * time.Second
	defaultImportRetention = 720 * time.Hour
)

// importNudge wakes the worker in-process the moment a check or a run is
// asked for (design D6): a channel of one, so a nudge never blocks a handler
// and a hundred nudges are one. In a split deployment the handlers' process
// has no worker to wake, and the poll is the bound.
var importNudge = make(chan struct{}, 1)

// importRunHookForTest, when set, runs once a run has started and before its
// first row, and an error it answers fails the run as a whole: the one point
// a test can make the worker meet a failure that is the run's rather than a
// row's. Set only through export_test.go's SetImportRunHook, by a test that
// does not run in parallel; nil in production.
var importRunHookForTest func(ctx context.Context, importID int32) error

func nudgeImportWorker() {
	select {
	case importNudge <- struct{}{}:
	default:
	}
}

// ImportWorker checks and runs the queued imports. It implements worker.Worker.
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

// Interval is the poll cadence between cycles (CUSTOMERS_IMPORT_POLL, default
// five seconds); a nudge from a handler cuts it short.
func (w *ImportWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersImportPoll > 0 {
		return w.deps.Config.CustomersImportPoll
	}
	return defaultImportPoll
}

// retention is how long a finished or abandoned import is kept
// (CUSTOMERS_IMPORT_RETENTION, default thirty days).
func (w *ImportWorker) retention() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersImportRetention > 0 {
		return w.deps.Config.CustomersImportRetention
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
// failing as a whole is written on it and logged, and the cycle ends there —
// the next cycle takes it up again, and a permanent failure is seen on the
// page rather than spun on.
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
				return fmt.Errorf("customers: read the import queue: %w", err)
			}
			if err := w.runImport(ctx, conn, imp); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				message := err.Error()
				if failErr := q.FailImport(ctx, store.FailImportParams{ID: imp.ID, Error: &message, UpdatedAt: w.now()}); failErr != nil {
					return fmt.Errorf("customers: record an import's failure: %w", failErr)
				}
				w.logger().Error("customers: an import failed and is tried again next cycle",
					"worker", importWorkerName, "importId", imp.ID, "error", message)
				return nil
			}
		}
		swept, err := q.SweepImports(ctx, w.now().Add(-w.retention()))
		if err != nil {
			return fmt.Errorf("customers: sweep old imports: %w", err)
		}
		if swept > 0 {
			w.logger().Info("old imports swept", "worker", importWorkerName, "imports", swept)
		}
		return nil
	})
}

// runImport is one import's check or run (design D5), from where it stands
// to its end or its stop.
func (w *ImportWorker) runImport(ctx context.Context, conn *pgxpool.Conn, imp store.CustomersImport) error {
	q := store.New(conn)
	mapping, err := importMappingFromJSON(imp.Mapping)
	if err != nil {
		return err
	}
	if mapping == nil {
		return errors.New("customers: the import has no mapping")
	}
	l := layoutFor(*mapping, len(imp.Header))
	dryRun := imp.State == string(gen.Checking)
	if err := q.StartImport(ctx, store.StartImportParams{ID: imp.ID, StartedAt: w.now()}); err != nil {
		return fmt.Errorf("customers: start import %d: %w", imp.ID, err)
	}
	if importRunHookForTest != nil {
		if err := importRunHookForTest(ctx, imp.ID); err != nil {
			return err
		}
	}
	act := importActor(imp)
	vocab, err := importVocabularyFor(ctx, q, l)
	if err != nil {
		return err
	}
	o := importOptions{act: act, vocab: vocab, nameHolders: l.has(csvGroupIdentity) && !l.allowDuplicateIdentity}
	duplicates, err := importDuplicateRows(ctx, q, imp.ID, l)
	if err != nil {
		return err
	}
	counts, err := q.ImportRowCounts(ctx, imp.ID)
	if err != nil {
		return fmt.Errorf("customers: count import %d's rows: %w", imp.ID, err)
	}
	processed, created, updated, failed, skipped := int32(counts.Processed), int32(counts.Created), int32(counts.Updated), int32(counts.Failed), int32(counts.Skipped)
	// The counts on the import are now this run's — a run just started shows
	// zeros, not the check's figures it carried until the worker took it up.
	if err := q.UpdateImportProgress(ctx, store.UpdateImportProgressParams{
		ID: imp.ID, Processed: processed, Created: created, Updated: updated, Failed: failed, Skipped: skipped, UpdatedAt: w.now(),
	}); err != nil {
		return fmt.Errorf("customers: write import %d's progress: %w", imp.ID, err)
	}

	// firstCells is the cells of the earlier rows the repeats are compared
	// with, read once each.
	firstCells := map[int32][]string{}

	for {
		rows, err := q.ImportRowsChunk(ctx, store.ImportRowsChunkParams{ImportID: imp.ID, Limit: importChunkRows})
		if err != nil {
			return fmt.Errorf("customers: read import %d's next rows: %w", imp.ID, err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			rec := fileimport.Record{Row: int(row.RowNo), Cells: row.Cells}
			p, planErrs := l.plan(rec, vocab, l.creates())
			var result importRowResult
			if first, dup := duplicates[row.RowNo]; dup {
				cells, ok := firstCells[first]
				if !ok {
					if cells, err = q.ImportRowCells(ctx, store.ImportRowCellsParams{ImportID: imp.ID, RowNo: first}); err != nil {
						return fmt.Errorf("customers: read import %d row %d: %w", imp.ID, first, err)
					}
					firstCells[first] = cells
				}
				if l.sameMappedCells(row.Cells, cells) {
					// The same customer listed again: the earlier row is it.
					result = importRowResult{Outcome: "skipped", Errs: []importError{{Row: rec.Row, Message: fmt.Sprintf("Same customer as row %d.", first)}}}
				} else if len(planErrs) == 0 {
					planErrs = []importError{{Row: rec.Row, Field: "legalId", Message: fmt.Sprintf(
						"Row %d of this file already has this legal identity; a file lists a customer once", first)}}
				}
			}
			if result.Outcome == "" {
				if result, err = w.srv.runImportRow(ctx, conn, imp.ID, l, p, planErrs, o, dryRun); err != nil {
					return err
				}
			}
			if dryRun || result.Outcome == "failed" || result.Outcome == "skipped" {
				var errsJSON []byte
				if len(result.Errs) > 0 {
					if errsJSON, err = importRowErrorsJSON(l, result.Errs); err != nil {
						return err
					}
				}
				outcome := result.Outcome
				if err := q.WriteImportRowOutcome(ctx, store.WriteImportRowOutcomeParams{
					ImportID: imp.ID, RowNo: row.RowNo, Outcome: &outcome, CustomerID: result.CustomerID, Errors: errsJSON,
				}); err != nil {
					return fmt.Errorf("customers: write import %d row %d's outcome: %w", imp.ID, row.RowNo, err)
				}
			}
			processed++
			switch result.Outcome {
			case "created":
				created++
			case "updated":
				updated++
			case "skipped":
				skipped++
			default:
				failed++
			}
		}
		if err := q.UpdateImportProgress(ctx, store.UpdateImportProgressParams{
			ID: imp.ID, Processed: processed, Created: created, Updated: updated, Failed: failed, Skipped: skipped, UpdatedAt: w.now(),
		}); err != nil {
			return fmt.Errorf("customers: write import %d's progress: %w", imp.ID, err)
		}
		stop, err := q.ImportStopRequested(ctx, imp.ID)
		if err != nil {
			return fmt.Errorf("customers: read import %d's stop flag: %w", imp.ID, err)
		}
		if stop {
			return w.finish(ctx, q, imp, dryRun, true)
		}
	}
	return w.finish(ctx, q, imp, dryRun, false)
}

// finish closes a run (design D5): a check stopped goes back to draft with its
// results cleared; a check finished is checked; a run, finished or stopped,
// is done. The counts are recounted from the rows, the one source of truth.
func (w *ImportWorker) finish(ctx context.Context, q *store.Queries, imp store.CustomersImport, dryRun, stopped bool) error {
	now := w.now()
	if dryRun && stopped {
		if err := q.ClearImportRowResults(ctx, imp.ID); err != nil {
			return fmt.Errorf("customers: clear import %d's results: %w", imp.ID, err)
		}
		if err := q.FinishImport(ctx, store.FinishImportParams{ID: imp.ID, State: string(gen.Draft), FinishedAt: nil, UpdatedAt: now}); err != nil {
			return fmt.Errorf("customers: finish import %d: %w", imp.ID, err)
		}
		w.logger().Info("import check stopped", "worker", importWorkerName, "importId", imp.ID)
		return nil
	}
	counts, err := q.ImportRowCounts(ctx, imp.ID)
	if err != nil {
		return fmt.Errorf("customers: count import %d's rows: %w", imp.ID, err)
	}
	state := gen.Checked
	if !dryRun {
		state = gen.Done
	}
	if err := q.FinishImport(ctx, store.FinishImportParams{
		ID: imp.ID, State: string(state), Processed: int32(counts.Processed), Created: int32(counts.Created),
		Updated: int32(counts.Updated), Failed: int32(counts.Failed), Skipped: int32(counts.Skipped), FinishedAt: ptrTime(now), UpdatedAt: now,
	}); err != nil {
		return fmt.Errorf("customers: finish import %d: %w", imp.ID, err)
	}
	w.logger().Info("import finished", "worker", importWorkerName, "importId", imp.ID, "state", state, "stopped", stopped,
		"rows", imp.RowCount, "processed", counts.Processed, "created", counts.Created, "updated", counts.Updated, "failed", counts.Failed, "skipped", counts.Skipped)
	return nil
}

// importDuplicateRows is the in-file duplicate pass (design D3, D5): of the
// rows that carry one legal identity — country and id as the duplicate
// guard compares them, the import's default country applied — every row
// but the first, keyed to the first's number. The walk then compares each
// with that first row: one whose mapped cells are all equal is the same
// customer listed again — a supplier's export repeats a customer on every
// metering point's row — and is skipped; one that differs is the row's error. Run over every row of the file
// whatever its outcome, so a run that resumes still sees the first. The
// database check cannot see it in a dry run, whose rows each roll back
// before the next one runs; this pass needs no database write, so the two
// runs agree. Nothing to do when no legal id is mapped.
func importDuplicateRows(ctx context.Context, q *store.Queries, importID int32, l importLayout) (map[int32]int32, error) {
	idColumn, countryColumn, fixedID, fixedCountry := l.identityColumnsOf()
	if idColumn == 0 && fixedID == "" {
		return nil, nil
	}
	cells, err := q.ImportRowIdentityCells(ctx, store.ImportRowIdentityCellsParams{ImportID: importID, IDColumn: idColumn, CountryColumn: countryColumn})
	if err != nil {
		return nil, fmt.Errorf("customers: read import %d's identities: %w", importID, err)
	}
	first := map[identityKey]int32{}
	duplicates := map[int32]int32{}
	for _, c := range cells {
		id, country := fixedID, fixedCountry
		if idColumn != 0 {
			id = c.LegalID
		}
		if countryColumn != 0 {
			country = c.LegalCountry
		}
		key, ok := identityKeyOf(country, id)
		if !ok {
			continue
		}
		if row, seen := first[key]; seen {
			duplicates[c.RowNo] = row
			continue
		}
		first[key] = c.RowNo
	}
	return duplicates, nil
}

// importActor is the actor every row's events carry: the uploader, from the
// id and the display name the upload resolved and kept on the import
// (design D1) — the worker has no user directory to ask, worker mode
// composing none, and would not ask one under a lock if it had. An import
// with no uploader (none is made today) takes the generated fallback.
func importActor(imp store.CustomersImport) actor {
	if imp.CreatedByUserID == nil {
		return generatedFallbackActor
	}
	display := unknownUserDisplay
	if imp.CreatedByName != nil && *imp.CreatedByName != "" {
		display = *imp.CreatedByName
	}
	return actor{Kind: "user", Display: display, UserID: imp.CreatedByUserID}
}

// underLease is the anonymisation worker's lease, on this worker's own key:
// one connection a cycle, the lease taken and released on it, and every
// statement of the cycle run on it.
func (w *ImportWorker) underLease(ctx context.Context, action func(context.Context, *pgxpool.Conn) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("customers: acquire a connection for the import lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, importLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("customers: take the import lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the customers import lease is held by another replica; skipping this cycle",
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

func ptrTime(t time.Time) *time.Time { return &t }
