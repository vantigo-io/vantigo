package energy_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the import worker (energy import design D6): the customers
// import worker's tests on energy's path — the chunks and the progress, the
// stop under way, the resume at the first row without an outcome, a
// whole-run failure written and retried, the lease, the sweep, and the nudge.

// manyPoints is a file of n points that create, numbered.
func manyPoints(n int) []byte {
	rows := make([][]string, 0, n+1)
	rows = append(rows, []string{"gsrn", "meterNumber", "streetAddress", "postalCode", "city", "priceArea"})
	for i := 1; i <= n; i++ {
		rows = append(rows, []string{fmt.Sprintf("7070575993%08d", i), fmt.Sprintf("M%d", i), "Storgata 1", "0150", "Oslo", "NO1"})
	}
	return fileOf(rows...)
}

var manyPointsMapping = mappingByFields([]string{"gsrn", "meterNumber", "streetAddress", "postalCode", "city", "priceArea"}, "create",
	"gsrn", "meterNumber", "streetAddress", "postalCode", "city", "priceArea")

// flagEnergyStopUnderWay marks an import as taken up by the worker and asked
// to stop, so the stop is there when the first chunk ends.
func flagEnergyStopUnderWay(t *testing.T, h *modtest.Harness, id int32) {
	t.Helper()
	h.Exec(t, `UPDATE energy.imports SET started_at = now(), stop_requested = true WHERE id = $1`, id)
}

func TestEnergyImportWorker_WalksTheRowsInChunks(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	done := importEnergyNow(t, h, c, manyPoints(1100), manyPointsMapping)
	if done.Processed != 1100 || done.Created != 1100 || done.Failed != 0 || done.StartedAt == nil || done.FinishedAt == nil {
		t.Errorf("done = %+v, want every row created", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.import_rows WHERE import_id = $1 AND outcome = 'created' AND metering_point_id IS NOT NULL`, done.ID); n != 1100 {
		t.Errorf("%d rows carry their point, want 1100", n)
	}
}

func TestEnergyImportWorker_AStopUnderWay(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	// A check stopped goes back to draft with its results cleared.
	imp := uploadEnergyImport(t, c, manyPoints(600))
	energyImportOf(t, putEnergyMapping(c, imp.ID, manyPointsMapping), http.StatusOK)
	energyImportOf(t, postEnergyCheck(c, imp.ID), http.StatusOK)
	flagEnergyStopUnderWay(t, h, imp.ID)
	runEnergyImportWorker(t, h)
	if stopped := getEnergyImport(t, c, imp.ID); stopped.State != "draft" || stopped.Processed != 0 || stopped.StopRequested {
		t.Errorf("a stopped check = %+v, want draft with nothing kept", stopped)
	}
	// A run stopped finishes as done with the first chunk kept.
	checked := checkEnergyImport(t, h, c, manyPoints(600), manyPointsMapping)
	energyImportOf(t, postEnergyRun(c, checked.ID, nil), http.StatusOK)
	flagEnergyStopUnderWay(t, h, checked.ID)
	runEnergyImportWorker(t, h)
	if done := getEnergyImport(t, c, checked.ID); done.State != "done" || done.Processed != 500 || done.Created != 500 {
		t.Errorf("a stopped run = %+v, want done with 500 of 600", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points`); n != 500 {
		t.Errorf("%d points after a stopped run, want 500", n)
	}
	// A stop on a done import is refused.
	if code := energyConflictCode(t, c.Do(http.MethodPost, energyImportPath(checked.ID, "/stop"), nil)); code != "import_finished" {
		t.Errorf("stop on done: %q", code)
	}
}

func TestEnergyImportWorker_AStopUnderWayDuringAnAttach(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	rows := [][]string{pointHeader}
	for i := 1; i <= 600; i++ {
		rows = append(rows, pointRow(fmt.Sprintf("7070575994%08d", i), orgA, ""))
	}
	done := importEnergyNow(t, h, c, fileOf(rows...), fullMapping("create"))
	if done.Pending != 600 {
		t.Fatalf("done = %+v, want 600 pending", done)
	}
	energyImportOf(t, postEnergyAttach(c, done.ID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), 0), http.StatusOK)
	flagEnergyStopUnderWay(t, h, done.ID)
	runEnergyImportWorker(t, h)
	after := getEnergyImport(t, c, done.ID)
	if after.State != "done" || after.Attached != 500 || after.Pending != 100 || after.StopRequested {
		t.Errorf("a stopped attach = %+v, want done with 500 attached and 100 pending", after)
	}
}

func TestEnergyImportWorker_ResumesAtTheFirstRowWithoutAnOutcome(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t)
	checked := checkEnergyImport(t, h, c, manyPoints(10), manyPointsMapping)
	energyImportOf(t, postEnergyRun(c, checked.ID, nil), http.StatusOK)
	// As if a run had committed four rows and died before the next.
	h.Exec(t, `UPDATE energy.import_rows SET outcome = 'created' WHERE import_id = $1 AND row_no <= 4`, checked.ID)
	h.Exec(t, `UPDATE energy.imports SET started_at = now() WHERE id = $1`, checked.ID)
	runEnergyImportWorker(t, h)
	if done := getEnergyImport(t, c, checked.ID); done.State != "done" || done.Processed != 10 || done.Created != 10 {
		t.Errorf("resumed = %+v, want done with every row counted", done)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points`); n != 6 {
		t.Errorf("%d points created by the resumed run, want 6", n)
	}
}

// Not parallel: it sets the package's run hook.
func TestEnergyImportWorker_AWholeRunFailureIsWrittenAndRetried(t *testing.T) {
	h, c, _ := importHarness(t)
	imp := uploadEnergyImport(t, c, manyPoints(3))
	energyImportOf(t, putEnergyMapping(c, imp.ID, manyPointsMapping), http.StatusOK)
	energyImportOf(t, postEnergyCheck(c, imp.ID), http.StatusOK)
	attempts := 0
	defer energy.SetImportRunHook(func(context.Context, int32) error {
		attempts++
		if attempts == 1 {
			return errors.New("the database went away")
		}
		return nil
	})()
	if ran, err := energy.NewImportWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want the cycle to run and keep the failure to the import", ran, err)
	}
	if failed := getEnergyImport(t, c, imp.ID); failed.State != "checking" || failed.Error != "the database went away" {
		t.Errorf("after the failure = %+v, want still checking with the error on it", failed)
	}
	runEnergyImportWorker(t, h)
	if mended := getEnergyImport(t, c, imp.ID); mended.State != "checked" || mended.Error != "" || mended.Created != 3 || attempts != 2 {
		t.Errorf("after the retry = %+v (%d attempts), want checked with no error", mended, attempts)
	}
}

func TestEnergyImportWorker_TheLeaseIsOneReplicaAtATime(t *testing.T) {
	t.Parallel()
	h, _, _ := importHarness(t)
	ctx := context.Background()
	conn, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var held bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, energy.ImportLeaseKeyForTest).Scan(&held); err != nil || !held {
		t.Fatalf("take the lease: %v %v", held, err)
	}
	w := energy.NewImportWorker(h.Deps())
	if ran, err := w.RunCycle(ctx); err != nil || ran {
		t.Errorf("RunCycle with the lease held = %v, %v; want false, nil", ran, err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, energy.ImportLeaseKeyForTest); err != nil {
		t.Fatal(err)
	}
	if ran, err := w.RunCycle(ctx); err != nil || !ran {
		t.Errorf("RunCycle after release = %v, %v; want true, nil", ran, err)
	}
}

func TestEnergyImportWorker_SweepsImportsPastRetention(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t, modtest.WithEnv("ENERGY_IMPORT_RETENTION", "24h"))
	old := importEnergyNow(t, h, c, manyPoints(1), manyPointsMapping)
	draft := uploadEnergyImport(t, c, manyPoints(1))
	h.Exec(t, `UPDATE energy.imports SET updated_at = updated_at - interval '2 days' WHERE id <> $1`, draft.ID)
	runEnergyImportWorker(t, h)
	if r := c.Do(http.MethodGet, energyImportPath(old.ID, ""), nil); r.Status != http.StatusNotFound {
		t.Errorf("the old import: status %d, want swept", r.Status)
	}
	if r := c.Do(http.MethodGet, energyImportPath(draft.ID, ""), nil); r.Status != http.StatusOK {
		t.Errorf("the fresh draft: status %d, want kept", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM energy.metering_points`); n != 1 {
		t.Errorf("%d points after the sweep, want the one imported", n)
	}
}

func TestEnergyImportWorker_ANudgeStartsACheckWithoutATick(t *testing.T) {
	t.Parallel()
	h, c, _ := importHarness(t, modtest.WithEnv("ENERGY_IMPORT_POLL", "1h"))
	w := energy.NewImportWorker(h.Deps())
	if w.Interval() != time.Hour {
		t.Fatalf("Interval = %v, want the configured hour", w.Interval())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- w.Run(ctx) }()
	imp := uploadEnergyImport(t, c, manyPoints(2))
	energyImportOf(t, putEnergyMapping(c, imp.ID, manyPointsMapping), http.StatusOK)
	energyImportOf(t, postEnergyCheck(c, imp.ID), http.StatusOK)
	deadline := time.Now().Add(10 * time.Second)
	for getEnergyImport(t, c, imp.ID).State != "checked" {
		if time.Now().After(deadline) {
			t.Fatalf("the check was not taken up within ten seconds: %+v", getEnergyImport(t, c, imp.ID))
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-finished; err != nil {
		t.Errorf("Run = %v, want nil once cancelled", err)
	}
}
