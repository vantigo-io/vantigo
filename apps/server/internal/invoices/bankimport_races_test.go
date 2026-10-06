package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The bank import's races (invoices payments and reminders design D3 step 7,
// D18) on the race kit: a pool of two, every probe and raw lock on a
// connection of its own, a waiter proved by pg_blocking_pids, every racing
// request under a deadline, and pg_stat_database.deadlocks unchanged.

// raceRequest is one racing request's answer, delivered on a channel.
type raceRequest struct{ res *modtest.Response }

// startImport uploads data on its own goroutine under a 30-second deadline.
func startImport(t *testing.T, c *modtest.Client, data []byte) <-chan raceRequest {
	t.Helper()
	contentType, body := fileBody(t, "file", data)
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(http.MethodPost, bankFilesPath, nil, modtest.RawBody(contentType, body), modtest.Context(ctx))}
	}()
	return done
}

// finished waits for a racing request and fails the test unless it answered
// want.
func finished(t *testing.T, what string, done <-chan raceRequest, want int) *modtest.Response {
	t.Helper()
	select {
	case r := <-done:
		if r.res.Status != want {
			t.Errorf("%s = %d %s, want %d", what, r.res.Status, r.res.Body, want)
		}
		return r.res
	case <-time.After(40 * time.Second):
		t.Fatalf("%s never finished", what)
		return nil
	}
}

// parkFirstImport installs SetBankImportAfterInsert so the first import to
// reach it — after every insert, before its commit — parks until release is
// closed, reporting its file's id on parked; any later import passes.
func parkFirstImport(t *testing.T) (parked <-chan int64, release chan<- struct{}) {
	t.Helper()
	p, r := make(chan int64, 1), make(chan struct{})
	var first atomic.Bool
	restore := invoices.SetBankImportAfterInsert(func(_ context.Context, id int64) error {
		if first.Swap(true) {
			return nil
		}
		p <- id
		select {
		case <-r:
		case <-time.After(30 * time.Second):
		}
		return nil
	})
	t.Cleanup(restore)
	return p, r
}

// idleInTransaction is the one backend of the installation's database
// holding a transaction open and doing nothing: the parked import's.
func idleInTransaction(t *testing.T, probe *pgx.Conn) uint32 {
	t.Helper()
	rows, err := probe.Query(context.Background(), `
		SELECT pid FROM pg_stat_activity
		WHERE datname = current_database() AND state = 'idle in transaction'`)
	if err != nil {
		t.Fatalf("read the idle transactions: %v", err)
	}
	pids, err := pgx.CollectRows(rows, pgx.RowTo[uint32])
	if err != nil || len(pids) != 1 {
		t.Fatalf("the idle transactions = %v (%v), want the parked import's alone", pids, err)
	}
	return pids[0]
}

// eachSharedLineOnce asserts that, across files, every fingerprint is one
// live line, and those of shared lines one duplicate besides; it answers how
// many fingerprints had a duplicate.
func eachSharedLineOnce(t *testing.T, h *harness, files ...int64) int {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(), `
		SELECT count(*) FILTER (WHERE duplicate_of_id IS NULL), count(*) FILTER (WHERE duplicate_of_id IS NOT NULL)
		FROM invoices.bank_transactions WHERE bank_file_id = ANY($1) GROUP BY account, fingerprint`, files)
	if err != nil {
		t.Fatalf("read the lines: %v", err)
	}
	defer rows.Close()
	shared := 0
	for rows.Next() {
		var live, dup int
		if err := rows.Scan(&live, &dup); err != nil {
			t.Fatalf("read the lines: %v", err)
		}
		if live != 1 || dup > 1 {
			t.Errorf("a fingerprint has %d live lines and %d duplicates, want one live and at most one duplicate", live, dup)
		}
		shared += dup
	}
	return shared
}

// TestBankImport_TwoOverlappingImports: overlap-a is held after its inserts
// (D3 step 7), and overlap-b, started then, waits on it — on the
// fingerprint index, where the shared lines' uncommitted rows are — never
// the other way round; released, both are 201, each shared line is live once
// and a duplicate once, and Postgres broke no deadlock. In the first-import
// variant neither account exists yet and the two files name their two
// accounts in opposite orders: the second waits on the first's upserted
// account rows, taken in account order, with the same outcome. In the
// simultaneous variant both imports are let go at once onto three hundred
// shared lines in opposite file orders: the one insert ordered by
// fingerprint makes them wait on each other in one order only.
func TestBankImport_TwoOverlappingImports(t *testing.T) {
	t.Run("accounts known", func(t *testing.T) {
		h := raceHarness(t)
		toBankDay(t, h)
		c := importer(t, h)
		imported(t, c, bankfiletest.OCR("99", giro(olderAccount, 1, 100, "0010058", "9")))
		overlapRace(t, h, c, ocrFixture(t, "overlap-a.ocr"), ocrFixture(t, "overlap-b.ocr"), 2, olderAccount)
	})

	t.Run("first import", func(t *testing.T) {
		h := raceHarness(t)
		toBankDay(t, h)
		c := importer(t, h)
		shared := []bankfiletest.OCRPayment{giro(sellerAccount, 5, 125000, "0010017", "1"), giro(olderAccount, 5, 49950, "0010025", "2")}
		a := bankfiletest.OCR("1", shared[0], shared[1], giro(olderAccount, 6, 700, "0010033", "3"))
		b := bankfiletest.OCR("2", shared[1], shared[0], giro(sellerAccount, 6, 900, "0010041", "4"))
		if n := len(bankAccounts(t, h)); n != 0 {
			t.Fatalf("%d accounts before the race, want none", n)
		}
		overlapRace(t, h, c, a, b, 2)
		if got := bankAccounts(t, h); len(got) != 2 || got[0].Account != olderAccount || got[1].Account != sellerAccount {
			t.Errorf("accounts = %+v, want each inserted once", got)
		}
	})

	t.Run("simultaneous", func(t *testing.T) {
		h := raceHarness(t)
		toBankDay(t, h)
		c := importer(t, h)
		imported(t, c, bankfiletest.OCR("99", giro(olderAccount, 1, 100, "0010058", "9")))
		var shared []bankfiletest.OCRPayment
		for i := range 300 {
			shared = append(shared, giro(olderAccount, 1+i%6, int64(1000+i), fmt.Sprintf("%07d", 1000+i), fmt.Sprint(100+i)))
		}
		reversed := slices.Clone(shared)
		slices.Reverse(reversed)
		a := bankfiletest.OCR("1", shared...)
		b := bankfiletest.OCR("2", reversed...)

		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		gate := holdRow(t, h, `SELECT 1 FROM invoices.bank_import_accounts WHERE account = $1 FOR UPDATE`, olderAccount)
		doneA, doneB := startImport(t, c, a), startImport(t, c, b)
		first := newWaiter(t, probeConn)
		second := newWaiter(t, probeConn, first)
		for _, pid := range []uint32{first, second} {
			if got := blockersOf(t, probeConn, pid); !slices.Equal(got, []uint32{gate.pid}) {
				t.Errorf("an import waits on %v, want the gate %d", got, gate.pid)
			}
		}
		gate.release(t)
		var ra, rb importJSON
		finished(t, "the import in file order", doneA, http.StatusCreated).JSON(&ra)
		finished(t, "the import in reverse order", doneB, http.StatusCreated).JSON(&rb)
		if ra.Duplicates+rb.Duplicates != 300 {
			t.Errorf("duplicates = %d + %d, want 300 between them", ra.Duplicates, rb.Duplicates)
		}
		if n := eachSharedLineOnce(t, h, ra.File.ID, rb.File.ID); n != 300 {
			t.Errorf("%d shared lines have a duplicate, want 300", n)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s) between the two imports", after-before)
		}
	})
}

// overlapRace holds the import of a after its inserts, proves it holds the
// known accounts FOR SHARE, starts b, proves b waits on a alone, releases a,
// and checks both finished with shared lines
// each live once and a duplicate once.
func overlapRace(t *testing.T, h *harness, c *modtest.Client, a, b []byte, shared int, known ...string) {
	t.Helper()
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	parked, release := parkFirstImport(t)

	doneA := startImport(t, c, a)
	var fileA int64
	select {
	case fileA = <-parked:
	case <-time.After(20 * time.Second):
		t.Fatal("the first import never reached its seam")
	}
	pidA := idleInTransaction(t, probeConn)
	// The parked import holds its accounts FOR SHARE — those already
	// committed; one it inserted is not yet visible to a probe.
	for _, account := range known {
		if mode := heldMode(t, probeConn, "invoices.bank_import_accounts", "account = $1", account); mode != modeShare {
			t.Errorf("the parked import holds account %s %q, want FOR SHARE", account, mode)
		}
	}
	doneB := startImport(t, c, b)
	pidB := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, pidB); !slices.Equal(got, []uint32{pidA}) {
		t.Errorf("the second import waits on %v, want the first %d", got, pidA)
	}
	close(release)

	var ra, rb importJSON
	finished(t, "the first import", doneA, http.StatusCreated).JSON(&ra)
	finished(t, "the second import", doneB, http.StatusCreated).JSON(&rb)
	if ra.File.ID != fileA || ra.Duplicates != 0 || rb.Duplicates != shared {
		t.Errorf("the first = file %d with %d duplicates, the second %d duplicates; want %d with none, and %d", ra.File.ID, ra.Duplicates, rb.Duplicates, fileA, shared)
	}
	if n := eachSharedLineOnce(t, h, ra.File.ID, rb.File.ID); n != shared {
		t.Errorf("%d shared lines have a duplicate, want %d", n, shared)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s) between the two imports", after-before)
	}
}

// TestBankImport_SameFileTwiceRace: the same bytes uploaded twice at once —
// the second's pool read cannot see the first, held uncommitted after its
// inserts, so the second waits on the file's unique hash, and once the first
// commits it is the same 409 bank_file_duplicate naming it (D3 step 7.2),
// with nothing of its own written.
func TestBankImport_SameFileTwiceRace(t *testing.T) {
	h := raceHarness(t)
	toBankDay(t, h)
	c := importer(t, h)
	imported(t, c, bankfiletest.OCR("99", giro(sellerAccount, 1, 100, "0010058", "9")))
	file := bankfiletest.OCR("5", giro(sellerAccount, 6, 125000, "0010017", "1"))
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	parked, release := parkFirstImport(t)

	doneA := startImport(t, c, file)
	var fileA int64
	select {
	case fileA = <-parked:
	case <-time.After(20 * time.Second):
		t.Fatal("the first import never reached its seam")
	}
	pidA := idleInTransaction(t, probeConn)
	doneB := startImport(t, c, file)
	pidB := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, pidB); !slices.Equal(got, []uint32{pidA}) {
		t.Errorf("the second import waits on %v, want the first %d", got, pidA)
	}
	close(release)

	finished(t, "the first import", doneA, http.StatusCreated)
	var p duplicateJSON
	finished(t, "the second import", doneB, http.StatusConflict).JSON(&p)
	if p.Code != "bank_file_duplicate" || p.BankFileID == nil || *p.BankFileID != fileA {
		t.Errorf("the second import = %+v, want bank_file_duplicate naming file %d", p, fileA)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_files`); n != 2 {
		t.Errorf("files = %d, want the earlier one and the first import's", n)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// startFormatPut changes account's format to format on its own goroutine
// under a 30-second deadline.
func startFormatPut(c *modtest.Client, account, format string) <-chan raceRequest {
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- raceRequest{c.Do(http.MethodPut, bankAccountsPath+"/"+account+"/format", map[string]any{"format": format}, modtest.Context(ctx))}
	}()
	return done
}

// TestBankImport_FormatChangeRacesImport: an import and a format change of
// its account serialise on the account's row (D3, D18) — the import holds it
// FOR SHARE, the change FOR UPDATE. A change started while an import is
// parked after its inserts waits on it, and its cutover is the parked file's
// booking day, read after the lock; an import started while a writer holds
// the row waits, then judges the format the writer committed and refuses the
// file, writing nothing; and the change holds the row FOR UPDATE.
func TestBankImport_FormatChangeRacesImport(t *testing.T) {
	t.Run("a change waits for an import", func(t *testing.T) {
		h := raceHarness(t)
		toBankDay(t, h)
		c := importer(t, h)
		manager := h.SignIn(t, "invoices:access", "invoices:manage")
		imported(t, c, bankfiletest.OCR("1", giro(sellerAccount, 2, 100, "0010058", "9")))
		probeConn := ownConn(t, h)
		before := deadlocks(t, probeConn)
		parked, release := parkFirstImport(t)

		doneImport := startImport(t, c, bankfiletest.OCR("2", giro(sellerAccount, 6, 125000, "0010017", "1")))
		select {
		case <-parked:
		case <-time.After(20 * time.Second):
			t.Fatal("the import never reached its seam")
		}
		importPID := idleInTransaction(t, probeConn)
		donePut := startFormatPut(manager, sellerAccount, "camt054")
		putPID := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, putPID); !slices.Equal(got, []uint32{importPID}) {
			t.Errorf("the format change waits on %v, want the import %d", got, importPID)
		}
		close(release)

		finished(t, "the import", doneImport, http.StatusCreated)
		var changed bankAccountJSON
		finished(t, "the format change", donePut, http.StatusOK).JSON(&changed)
		if changed.CutoverThrough == nil || *changed.CutoverThrough != "2026-10-06" {
			t.Errorf("the cutover = %v, want 2026-10-06, the parked file's booking day", changed.CutoverThrough)
		}
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("Postgres broke %d deadlock(s)", after-before)
		}
	})

	t.Run("an import waits for a change", func(t *testing.T) {
		h := raceHarness(t)
		toBankDay(t, h)
		c := importer(t, h)
		imported(t, c, bankfiletest.OCR("1", giro(sellerAccount, 2, 100, "0010058", "9")))
		before := written(t, h)
		probeConn := ownConn(t, h)
		deadlocksBefore := deadlocks(t, probeConn)

		writer := holdRow(t, h, `UPDATE invoices.bank_import_accounts SET format = 'camt054' WHERE account = $1`, sellerAccount)
		done := startImport(t, c, bankfiletest.OCR("2", giro(sellerAccount, 6, 125000, "0010017", "1")))
		pid := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, pid); !slices.Equal(got, []uint32{writer.pid}) {
			t.Errorf("the import waits on %v, want the writer %d", got, writer.pid)
		}
		writer.release(t)

		var p duplicateJSON
		finished(t, "the import", done, http.StatusConflict).JSON(&p)
		if p.Code != "bank_import_format_mismatch" {
			t.Errorf("the import = %+v, want bank_import_format_mismatch", p)
		}
		if got := written(t, h); got != before {
			t.Errorf("files, lines, accounts = %v after the refusal, want %v", got, before)
		}
		if after := deadlocks(t, probeConn); after != deadlocksBefore {
			t.Errorf("Postgres broke %d deadlock(s)", after-deadlocksBefore)
		}
	})

	t.Run("a change holds the row FOR UPDATE", func(t *testing.T) {
		h := raceHarness(t)
		toBankDay(t, h)
		imported(t, importer(t, h), bankfiletest.OCR("1", giro(sellerAccount, 2, 100, "0010058", "9")))
		manager := h.SignIn(t, "invoices:access", "invoices:manage")
		probeConn := ownConn(t, h)

		locked, release := make(chan struct{}), make(chan struct{})
		var once atomic.Bool
		restore := invoices.SetLockTaken(func(_ context.Context, what, _ string) {
			if what != "account" || once.Swap(true) {
				return
			}
			close(locked)
			select {
			case <-release:
			case <-time.After(30 * time.Second):
			}
		})
		defer restore()

		done := startFormatPut(manager, sellerAccount, "camt054")
		select {
		case <-locked:
		case <-time.After(20 * time.Second):
			t.Fatal("the format change never took its lock")
		}
		if mode := heldMode(t, probeConn, "invoices.bank_import_accounts", "account = $1", sellerAccount); mode != modeUpdate {
			t.Errorf("the format change holds the account %q, want FOR UPDATE", mode)
		}
		close(release)
		finished(t, "the format change", done, http.StatusOK)
	})
}
