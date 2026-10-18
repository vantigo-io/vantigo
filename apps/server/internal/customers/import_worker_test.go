package customers_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the import worker (customers import wizard design D5, D6):
// the chunks and the progress, the stop under way in each run, the resume at
// the first row without an outcome, a whole-run failure written and retried,
// the lease, the sweep, and the nudge that starts a check without a tick.

// manyRows is a file of n rows that create, names numbered.
func manyRows(n int) []byte {
	rows := make([][]string, 0, n+1)
	rows = append(rows, []string{"name", "email"})
	for i := 1; i <= n; i++ {
		rows = append(rows, []string{fmt.Sprintf("Customer %04d AS", i), fmt.Sprintf("c%d@example.test", i)})
	}
	return csvFileOf(rows...)
}

// flagStopUnderWay marks an import as taken up by the worker and asked to
// stop — what the stop endpoint does once the worker has started, written
// here directly so the stop is already there when the first chunk ends.
func flagStopUnderWay(t *testing.T, h *modtest.Harness, id int32) {
	t.Helper()
	h.Exec(t, `UPDATE customers.imports SET started_at = now(), stop_requested = true WHERE id = $1`, id)
}

func TestImportWorker_WalksTheRowsInChunks_AndReportsWhatItDid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	// More than two chunks of five hundred.
	done := importNow(t, c, h, manyRows(1200), mappingOf("create", "", "name", "email"))
	if done.Processed != 1200 || done.Created != 1200 || done.Failed != 0 || done.StartedAt == nil || done.FinishedAt == nil || done.RequestedAt == nil {
		t.Errorf("done = %+v, want every row created with the three times set", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 1200 {
		t.Errorf("%d customers, want 1200", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1 AND outcome = 'created' AND customer_id IS NOT NULL`, done.ID); n != 1200 {
		t.Errorf("%d rows carry their customer, want 1200", n)
	}
}

func TestImportWorker_AStopUnderWay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	mapping := mappingOf("create", "", "name", "email")

	// A check stopped goes back to draft with its results cleared.
	imp := uploadImport(t, c, manyRows(700))
	importOf(t, putMapping(t, c, imp.ID, mapping), http.StatusOK)
	importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	flagStopUnderWay(t, h, imp.ID)
	runImportWorker(t, h)
	stopped := getImport(t, c, imp.ID)
	if stopped.State != "draft" || stopped.Processed != 0 || stopped.Created != 0 || stopped.StopRequested || stopped.FinishedAt != nil {
		t.Errorf("a stopped check = %+v, want draft with nothing kept", stopped)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1 AND outcome IS NOT NULL`, imp.ID); n != 0 {
		t.Errorf("%d rows keep a result after the check was stopped", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers after a check, want none", n)
	}

	// A run stopped finishes as done with the first chunk kept and the rest
	// never attempted — processed short of rows says so.
	checked := checkNow(t, c, h, manyRows(700), mapping)
	importOf(t, postRun(t, c, checked.ID), http.StatusOK)
	flagStopUnderWay(t, h, checked.ID)
	runImportWorker(t, h)
	done := getImport(t, c, checked.ID)
	if done.State != "done" || done.Processed != 500 || done.Created != 500 || done.Rows != 700 || done.StopRequested || done.FinishedAt == nil {
		t.Errorf("a stopped run = %+v, want done with 500 of 700 processed", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 500 {
		t.Errorf("%d customers after a stopped run, want the 500 of the first chunk", n)
	}
	// Done is done: another cycle changes nothing.
	runImportWorker(t, h)
	if again := getImport(t, c, checked.ID); again.Processed != 500 || h.Count(t, `SELECT count(*) FROM customers.customers`) != 500 {
		t.Errorf("a later cycle touched a done import: %+v", again)
	}
}

func TestImportWorker_ResumesAtTheFirstRowWithoutAnOutcome(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	checked := checkNow(t, c, h, manyRows(10), mappingOf("create", "", "name", "email"))
	importOf(t, postRun(t, c, checked.ID), http.StatusOK)
	// As if a run had committed four rows and died before the next: their
	// outcomes are on the rows, and nothing else is.
	h.Exec(t, `UPDATE customers.import_rows SET outcome = 'created', customer_id = 0 WHERE import_id = $1 AND row_no <= 4`, checked.ID)
	h.Exec(t, `UPDATE customers.imports SET started_at = now() WHERE id = $1`, checked.ID)
	runImportWorker(t, h)
	done := getImport(t, c, checked.ID)
	if done.State != "done" || done.Processed != 10 || done.Created != 10 {
		t.Errorf("resumed = %+v, want done with every row counted", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 6 {
		t.Errorf("%d customers created by the resumed run, want the six rows that had no outcome", n)
	}
}

// TestImportWorker_AnImportStoppedAsItIsTakenUpIsNotRun: a stop that sends
// an unstarted check back to draft between the worker's read of the queue and
// its taking the import up leaves nothing to take up. The worker used to
// start it anyway and finish a check nobody wanted any more.
func TestImportWorker_AnImportStoppedAsItIsTakenUpIsNotRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	imp := uploadImport(t, c, manyRows(3))
	importOf(t, putMapping(t, c, imp.ID, mappingOf("create", "", "name", "email")), http.StatusOK)
	importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The stop endpoint's own statement, not yet committed when the worker
	// reads the queue.
	if _, err := tx.Exec(ctx, `UPDATE customers.imports SET state = 'draft', requested_at = NULL WHERE id = $1 AND started_at IS NULL`, imp.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	cycle := make(chan error, 1)
	go func() {
		_, err := customers.NewImportWorker(h.Deps()).RunCycle(context.Background())
		cycle <- err
	}()
	waitForALockWait(t, h)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-cycle; err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if after := getImport(t, c, imp.ID); after.State != "draft" || after.StartedAt != nil || after.Processed != 0 {
		t.Errorf("after = %+v, want draft and never started", after)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1 AND outcome IS NOT NULL`, imp.ID); n != 0 {
		t.Errorf("%d rows carry an outcome, want none", n)
	}
}

// Not parallel: it sets the package's run hook.
func TestImportWorker_AWholeRunFailureIsWrittenAndRetried(t *testing.T) {
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	imp := uploadImport(t, c, manyRows(3))
	importOf(t, putMapping(t, c, imp.ID, mappingOf("create", "", "name", "email")), http.StatusOK)
	importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	// The run fails once, as a whole — the database gone, say — then not.
	attempts := 0
	defer customers.SetImportRunHook(func(context.Context, int32) error {
		attempts++
		if attempts == 1 {
			return errors.New("the database went away")
		}
		return nil
	})()
	if ran, err := customers.NewImportWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want the cycle to run and keep the failure to the import", ran, err)
	}
	failed := getImport(t, c, imp.ID)
	if failed.State != "checking" || failed.Error != "the database went away" || failed.StartedAt == nil {
		t.Errorf("after the failure = %+v, want still checking with the error on it", failed)
	}
	// The next cycle takes it up again and finishes it, the error gone.
	runImportWorker(t, h)
	if mended := getImport(t, c, imp.ID); mended.State != "checked" || mended.Error != "" || mended.Created != 3 || attempts != 2 {
		t.Errorf("after the retry = %+v (%d attempts), want checked with no error", mended, attempts)
	}
}

func TestImportWorker_TheLeaseIsOneReplicaAtATime(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	conn, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var held bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, customers.ImportLeaseKeyForTest).Scan(&held); err != nil || !held {
		t.Fatalf("take the lease: %v %v", held, err)
	}
	w := customers.NewImportWorker(h.Deps())
	if ran, err := w.RunCycle(ctx); err != nil || ran {
		t.Errorf("RunCycle with the lease held = %v, %v; want false, nil", ran, err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, customers.ImportLeaseKeyForTest); err != nil {
		t.Fatal(err)
	}
	if ran, err := w.RunCycle(ctx); err != nil || !ran {
		t.Errorf("RunCycle after release = %v, %v; want true, nil", ran, err)
	}
}

func TestImportWorker_SweepsImportsPastRetention_AndLeavesARunningOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("CUSTOMERS_IMPORT_RETENTION", "24h"))
	c, _ := uploadClient(t, h)
	old := importNow(t, c, h, manyRows(1), mappingOf("create", "", "name", "email"))
	draft := uploadImport(t, c, manyRows(1))
	running := uploadImport(t, c, manyRows(1))
	importOf(t, putMapping(t, c, running.ID, mappingOf("create", "", "name", "email")), http.StatusOK)
	importOf(t, postCheck(t, c, running.ID), http.StatusOK)
	// Everything is now older than a day but the draft, uploaded "tomorrow".
	h.Exec(t, `UPDATE customers.imports SET updated_at = updated_at - interval '2 days' WHERE id <> $1`, draft.ID)
	// The running one is checked by this very cycle, then the sweep runs: it
	// is fresh again (finished just now) and stays; the old done one goes.
	runImportWorker(t, h)
	if r := c.Do(http.MethodGet, importPath(old.ID, ""), nil); r.Status != http.StatusNotFound {
		t.Errorf("the old import: status %d, want swept", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.import_rows WHERE import_id = $1`, old.ID); n != 0 {
		t.Errorf("%d rows left of the swept import", n)
	}
	for name, id := range map[string]int32{"the draft": draft.ID, "the one just checked": running.ID} {
		if r := c.Do(http.MethodGet, importPath(id, ""), nil); r.Status != http.StatusOK {
			t.Errorf("%s: status %d, want kept", name, r.Status)
		}
	}
	// The customer the swept import made is not touched.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 1 {
		t.Errorf("%d customers after the sweep, want the one imported", n)
	}
}

func TestImportWorker_ANudgeStartsACheckWithoutATick(t *testing.T) {
	t.Parallel()
	// An hour between ticks: only the nudge can make this pass in time.
	h := newHarness(t, modtest.WithEnv("CUSTOMERS_IMPORT_POLL", "1h"))
	c, _ := uploadClient(t, h)
	w := customers.NewImportWorker(h.Deps())
	if w.Interval() != time.Hour {
		t.Fatalf("Interval = %v, want the configured hour", w.Interval())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- w.Run(ctx) }()

	imp := uploadImport(t, c, manyRows(2))
	importOf(t, putMapping(t, c, imp.ID, mappingOf("create", "", "name", "email")), http.StatusOK)
	// The first cycle of Run may have run before the check was asked for; the
	// check's own nudge is what wakes it now.
	importOf(t, postCheck(t, c, imp.ID), http.StatusOK)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := getImport(t, c, imp.ID); got.State == "checked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the check was not taken up within ten seconds: %+v", getImport(t, c, imp.ID))
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-finished; err != nil {
		t.Errorf("Run = %v, want nil once cancelled", err)
	}
}

func TestImportWorker_TheOneEventActorIsTheUploader_EvenWithoutADirectory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, uploader := uploadClient(t, h)
	// The worker is built from the harness's Deps, which — like worker mode's
	// — carry no user directory. The name travels on the import.
	done := importNow(t, c, h, csvFileOf([]string{"name"}, []string{"Named AS"}), mappingOf("create", "", "name"))
	id := customerIDByName(t, h, "Named AS")
	for _, e := range timelineOf(t, c, id) {
		if deref(e.ActorDisplay) != uploader {
			t.Errorf("event %s actor = %q, want %q", e.EventType, deref(e.ActorDisplay), uploader)
		}
	}
	if done.CreatedByName != uploader {
		t.Errorf("createdByName = %q", done.CreatedByName)
	}
}
