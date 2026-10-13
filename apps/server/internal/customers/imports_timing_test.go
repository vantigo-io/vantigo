package customers_test

import (
	"os"
	"testing"
	"time"
)

// TestImportWorker_Timing measures a real run of a large file on this machine
// (customers import wizard design D6's "what it takes"): twenty thousand
// creates, each with contact info, through the worker. It is opt-in
// (VANTIGO_IMPORT_TIMING=1), because CI's race-detected run says nothing
// about the import a person waits on, and it prints what it measured rather
// than asserting a figure that depends on the machine.
func TestImportWorker_Timing(t *testing.T) {
	if os.Getenv("VANTIGO_IMPORT_TIMING") != "1" {
		t.Skip("set VANTIGO_IMPORT_TIMING=1 to measure a run of 20000 rows")
	}
	h := newHarness(t)
	c, _ := uploadClient(t, h)
	const rows = 20_000
	started := time.Now()
	checked := checkNow(t, c, h, manyRows(rows), mappingOf("create", "", "name", "email"))
	checkTook := time.Since(started)
	started = time.Now()
	importOf(t, postRun(t, c, checked.ID), 200)
	runImportWorker(t, h)
	runTook := time.Since(started)
	done := getImport(t, c, checked.ID)
	if done.Created != rows {
		t.Fatalf("done = %+v", done)
	}
	t.Logf("%d rows: the check took %s (%.1f ms a row), the run %s (%.1f ms a row)", rows,
		checkTook.Round(time.Millisecond), float64(checkTook.Milliseconds())/rows,
		runTook.Round(time.Millisecond), float64(runTook.Milliseconds())/rows)
}
