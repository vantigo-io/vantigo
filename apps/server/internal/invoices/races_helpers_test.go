package invoices_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The race kit of the receivables (invoices payments and reminders design
// D18), phase 3's (internal/integration/work_races_test.go) copied beside the
// seams it drives. Every race runs on a pool of two; every raw lock-holding
// transaction, every NOWAIT probe and every pg_blocking_pids reader is on a
// connection of its own, outside the pool; a row lock is proved by NOWAIT
// probes of each mode and a wait by pg_blocking_pids — never by reading
// pg_locks — and pg_stat_database.deadlocks is read after each race. A test
// using the kit never runs in parallel: the seams it installs are the
// package's.

// raceHarness is an invoices installation on a pool of two connections.
func raceHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, modtest.WithPoolMaxConns(2))
}

// ownConn is a connection of its own on the installation's database, outside
// its pool: a raw lock-holder's, a probe's or a pg_blocking_pids reader's.
func ownConn(t *testing.T, h *harness) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), h.Deps().Config.DatabaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// rawLock is a transaction holding row locks on a connection of its own
// until it is released.
type rawLock struct {
	tx  pgx.Tx
	pid uint32
}

// holdRow begins a transaction on a connection of its own and runs lockSQL —
// a row lock — in it.
func holdRow(t *testing.T, h *harness, lockSQL string, args ...any) *rawLock {
	t.Helper()
	conn := ownConn(t, h)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the raw lock: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, lockSQL, args...); err != nil {
		t.Fatalf("the raw lock %q: %v", lockSQL, err)
	}
	return &rawLock{tx: tx, pid: conn.PgConn().PID()}
}

// release commits the raw transaction, handing its rows on.
func (l *rawLock) release(t *testing.T) {
	t.Helper()
	if err := l.tx.Commit(context.Background()); err != nil {
		t.Fatalf("release the raw lock: %v", err)
	}
}

// newWaiter waits until a backend of the installation's database other than
// those known waits on a lock, and answers its pid.
func newWaiter(t *testing.T, probe *pgx.Conn, known ...uint32) uint32 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := probe.Query(context.Background(), `
			SELECT pid FROM pg_stat_activity
			WHERE datname = current_database() AND cardinality(pg_blocking_pids(pid)) > 0
			ORDER BY pid`)
		if err != nil {
			t.Fatalf("read the waiting backends: %v", err)
		}
		pids, err := pgx.CollectRows(rows, pgx.RowTo[uint32])
		if err != nil {
			t.Fatalf("read the waiting backends: %v", err)
		}
		for _, pid := range pids {
			if !slices.Contains(known, pid) {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no backend besides %v came to wait on a lock within 10 s", known)
	return 0
}

// blockersOf is pg_blocking_pids(pid): who pid waits on.
func blockersOf(t *testing.T, probe *pgx.Conn, pid uint32) []uint32 {
	t.Helper()
	var pids []uint32
	if err := probe.QueryRow(context.Background(), `SELECT pg_blocking_pids($1)`, pid).Scan(&pids); err != nil {
		t.Fatalf("pg_blocking_pids(%d): %v", pid, err)
	}
	return pids
}

// deadlocks is how many deadlocks Postgres has detected in the installation's
// database — its cumulative statistics, which a backend reports within a
// second of going idle, so it is read after that settles: a deadlock the
// loser retried leaves no other trace.
func deadlocks(t *testing.T, probe *pgx.Conn) int64 {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	ctx := context.Background()
	if _, err := probe.Exec(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
		t.Fatalf("clear the statistics snapshot: %v", err)
	}
	var n int64
	if err := probe.QueryRow(ctx, `SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
		t.Fatalf("read the deadlocks: %v", err)
	}
	return n
}

// noWait runs a NOWAIT row lock in a transaction of its own on conn and rolls
// it back: nil when the row was free, the 55P03 error when another
// transaction holds it in a conflicting mode.
func noWait(conn *pgx.Conn, sql string, args ...any) error {
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, sql, args...)
	return err
}

// isLockNotAvailable reports whether err is SQLSTATE 55P03.
func isLockNotAvailable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}

// The four row-lock modes a probe takes, strongest first.
const (
	modeUpdate      = "UPDATE"
	modeNoKeyUpdate = "NO KEY UPDATE"
	modeShare       = "SHARE"
	modeKeyShare    = "KEY SHARE"
)

// probe takes FOR <mode> NOWAIT of the rows of table where matches, from conn
// in a transaction of its own, and rolls it back: nil when no other
// transaction holds them in a mode that conflicts with mode, the 55P03 error
// when one does. A FOR UPDATE lock answers 55P03 to every mode; FOR NO KEY
// UPDATE lets KEY SHARE through; FOR SHARE lets SHARE and KEY SHARE through;
// FOR KEY SHARE stops only UPDATE — so the probes, together, prove a lock's
// mode, not only that a row is locked (heldMode).
func probe(conn *pgx.Conn, mode, table, where string, args ...any) error {
	return noWait(conn, `SELECT 1 FROM `+table+` WHERE `+where+` FOR `+mode+` NOWAIT`, args...)
}

// heldMode is the strongest row-lock mode another transaction holds on the
// rows of table where matches, read with probe from the weakest up: "" when
// none is held. It fails the test on an error that is not 55P03.
func heldMode(t *testing.T, conn *pgx.Conn, table, where string, args ...any) string {
	t.Helper()
	for _, c := range []struct{ probe, held string }{
		{modeKeyShare, modeUpdate},
		{modeShare, modeNoKeyUpdate},
		{modeNoKeyUpdate, modeShare},
		{modeUpdate, modeKeyShare},
	} {
		err := probe(conn, c.probe, table, where, args...)
		if err == nil {
			continue
		}
		if !isLockNotAvailable(err) {
			t.Fatalf("probe FOR %s NOWAIT of %s where %s: %v", c.probe, table, where, err)
		}
		return c.held
	}
	return ""
}
