package invoices_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
)

// lockSeen records what the lock-order seam reports, as "what key".
type lockSeen struct {
	mu   sync.Mutex
	seen []string
}

func (l *lockSeen) note(_ context.Context, what, key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, what+" "+key)
}

// take answers what was reported since the last take, and forgets it.
func (l *lockSeen) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := l.seen
	l.seen = nil
	return seen
}

// rawTx is a transaction on a connection of its own, rolled back at the end
// of the test unless it was committed.
func rawTx(t *testing.T, h *harness) (pgx.Tx, uint32) {
	t.Helper()
	conn := ownConn(t, h)
	tx, err := conn.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx, conn.PgConn().PID()
}

// plantID runs an INSERT … RETURNING id on the installation's pool.
func plantID(t *testing.T, h *harness, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := h.Pool().QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		t.Fatalf("plant %q: %v", sql, err)
	}
	return id
}

// idKey is an id as the seam reports it.
func idKey(id int64) string { return strconv.FormatInt(id, 10) }

// TestLocks_EachHelperReportsAndHolds takes every lock helper of
// inv/locks.go on a transaction of a connection of its own (D18): the seam
// sees what each locks and the keys in the order it took them; the rows are
// held in the helper's mode, proved by the NOWAIT probes of each mode; a
// helper waits for a row another transaction holds rather than skipping it;
// and Postgres breaks no deadlock.
func TestLocks_EachHelperReportsAndHolds(t *testing.T) {
	h := raceHarness(t)
	saveSeller(t, h, completeSeller(1))
	inv := issuedFor(t, h, customerAcme, line("Konsulenttimer", 1, 1000, vat25))
	draftA := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID
	draftB := createDraft(t, h, draftBody(customerAcme, line("B", 1, 100, vat25))).ID
	draftC := createDraft(t, h, draftBody(customerAcme, line("C", 1, 100, vat25))).ID

	file := plantID(t, h, `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
		VALUES ('ocr', repeat('a', 64), '00008080:1:00008080', 'bank-files/a.ocr', 400, ARRAY['15032080119'], 1, 0, '{}',
		    gen_random_uuid(), now()) RETURNING id`)
	bankLine := plantID(t, h, `
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, amount,
		    currency, fingerprint, ordinal)
		VALUES ($1, '1/1', 'ocr', '15032080119', 'credit', DATE '2026-10-01', 1250, 'NOK', repeat('b', 64), 1) RETURNING id`, file)
	run := plantID(t, h, `INSERT INTO invoices.reminder_runs (run_on, created_at, created_by_user_id, stale_import_acknowledged)
		VALUES (DATE '2026-11-02', now(), gen_random_uuid(), false) RETURNING id`)
	letter := plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id, status)
		VALUES ($1, $2, 1, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'awaiting_print') RETURNING id`, inv.ID, run)
	batch := plantID(t, h, `INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
		VALUES (DATE '2026-11-03', now(), gen_random_uuid()) RETURNING id`)
	h.Exec(t, `INSERT INTO invoices.customer_reminder_policies (customer_id, mode, updated_by_user_id, updated_at)
		VALUES (9001, 'none', gen_random_uuid(), now()), (9002, 'no_charges', gen_random_uuid(), now())`)

	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	seen := &lockSeen{}
	restore := invoices.SetLockTaken(seen.note)
	defer restore()
	ctx := context.Background()

	// The import's first statements on two accounts never seen: inserted,
	// then reported in account order.
	tx, _ := rawTx(t, h)
	if err := invoices.LockForTest(ctx, tx, "lockImportAccounts", "22222222222", "11111111111"); err != nil {
		t.Fatalf("lockImportAccounts on two new accounts: %v", err)
	}
	if got, want := seen.take(), []string{"account 11111111111", "account 22222222222"}; !slices.Equal(got, want) {
		t.Errorf("lockImportAccounts on two new accounts reported %v, want %v", got, want)
	}
	var formats string
	if err := tx.QueryRow(ctx, `SELECT string_agg(account || ':' || format, ',' ORDER BY account)
		FROM invoices.bank_import_accounts`).Scan(&formats); err != nil {
		t.Fatalf("read the accounts: %v", err)
	}
	if want := "11111111111:ocr,22222222222:ocr"; formats != want {
		t.Errorf("the accounts after lockImportAccounts = %s, want %s: the first import sets the format", formats, want)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the accounts: %v", err)
	}

	for _, c := range []struct {
		helper string
		keys   []string
		seen   []string
		table  string
		where  string
		rows   []any
		mode   string
	}{
		{"lockInvoice", []string{idKey(inv.ID)}, []string{"invoice " + idKey(inv.ID)},
			"invoices.invoices", "id = $1", []any{inv.ID}, "UPDATE"},
		{"lockInvoicesDescending", []string{idKey(draftA), idKey(draftC), idKey(draftB)}, []string{"invoice " + idKey(draftC), "invoice " + idKey(draftB), "invoice " + idKey(draftA)},
			"invoices.invoices", "id = $1", []any{draftA, draftB, draftC}, "UPDATE"},
		{"lockBankTransaction", []string{idKey(bankLine)}, []string{"bank_transaction " + idKey(bankLine)},
			"invoices.bank_transactions", "id = $1", []any{bankLine}, "NO KEY UPDATE"},
		{"lockImportAccounts", []string{"22222222222", "11111111111"}, []string{"account 11111111111", "account 22222222222"},
			"invoices.bank_import_accounts", "account = $1", []any{"11111111111", "22222222222"}, "SHARE"},
		{"lockImportAccount", []string{"11111111111"}, []string{"account 11111111111"},
			"invoices.bank_import_accounts", "account = $1", []any{"11111111111"}, "UPDATE"},
		{"lockReminder", []string{idKey(letter)}, []string{"reminder " + idKey(letter)},
			"invoices.reminders", "id = $1", []any{letter}, "NO KEY UPDATE"},
		{"lockPrintBatch", []string{idKey(batch)}, []string{"print_batch " + idKey(batch)},
			"invoices.reminder_print_batches", "id = $1", []any{batch}, "NO KEY UPDATE"},
		{"shareCustomerDocuments", []string{fmt.Sprint(customerAcme)},
			[]string{"document " + idKey(draftC), "document " + idKey(draftB), "document " + idKey(draftA), "document " + idKey(inv.ID)},
			"invoices.invoices", "id = $1", []any{inv.ID, draftA, draftB, draftC}, "SHARE"},
		{"lockPolicies", []string{"9002", "9001", "9003"}, []string{"policy 9001", "policy 9002"},
			"invoices.customer_reminder_policies", "customer_id = $1", []any{9001, 9002}, "UPDATE"},
	} {
		tx, _ := rawTx(t, h)
		if err := invoices.LockForTest(ctx, tx, c.helper, c.keys...); err != nil {
			t.Fatalf("%s(%v): %v", c.helper, c.keys, err)
		}
		if got := seen.take(); !slices.Equal(got, c.seen) {
			t.Errorf("%s(%v) reported %v, want %v", c.helper, c.keys, got, c.seen)
		}
		for _, row := range c.rows {
			if got := heldMode(t, probeConn, c.table, c.where, row); got != c.mode {
				t.Errorf("%s(%v): %s row %v held FOR %q, want FOR %s", c.helper, c.keys, c.table, row, got, c.mode)
			}
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("roll back %s: %v", c.helper, err)
		}
		for _, row := range c.rows {
			if got := heldMode(t, probeConn, c.table, c.where, row); got != "" {
				t.Errorf("%s rolled back: %s still held FOR %s", c.helper, c.table, got)
			}
		}
	}

	// A helper waits for a row another transaction holds: lockInvoice parks
	// behind a raw FOR UPDATE of the invoice and takes it once it is released.
	holder := holdRow(t, h, `SELECT id FROM invoices.invoices WHERE id = $1 FOR UPDATE`, inv.ID)
	waiting, waiterPID := rawTx(t, h)
	done := make(chan error, 1)
	go func() { done <- invoices.LockForTest(ctx, waiting, "lockInvoice", idKey(inv.ID)) }()
	if pid := newWaiter(t, probeConn); pid != waiterPID {
		t.Errorf("the backend waiting is %d, want the helper's %d", pid, waiterPID)
	}
	if got := blockersOf(t, probeConn, waiterPID); !slices.Equal(got, []uint32{holder.pid}) {
		t.Errorf("lockInvoice waits on %v, want the raw holder %d", got, holder.pid)
	}
	holder.release(t)
	if err := <-done; err != nil {
		t.Errorf("lockInvoice after the holder released: %v", err)
	}
	if got := seen.take(); !slices.Equal(got, []string{"invoice " + idKey(inv.ID)}) {
		t.Errorf("lockInvoice after its wait reported %v", got)
	}

	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s) while the helpers held their rows", after-before)
	}
}

// TestLocks_TheImportAccountsOrderNeverDeadlocks: two first imports naming
// the same two new accounts in opposite orders (D3 step 7, I18) — each the
// import's first statements on a raw transaction of its own — queue on the
// same row in the same order: the second waits on the first, and both
// finish, each account inserted once, never 40P01.
func TestLocks_TheImportAccountsOrderNeverDeadlocks(t *testing.T) {
	h := raceHarness(t)
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	ctx := context.Background()

	first, firstPID := rawTx(t, h)
	if err := invoices.LockForTest(ctx, first, "lockImportAccounts", "11111111111", "22222222222"); err != nil {
		t.Fatalf("the first import's accounts: %v", err)
	}
	second, secondPID := rawTx(t, h)
	done := make(chan error, 1)
	go func() { done <- invoices.LockForTest(ctx, second, "lockImportAccounts", "22222222222", "11111111111") }()
	if pid := newWaiter(t, probeConn); pid != secondPID {
		t.Errorf("the backend waiting is %d, want the second import's %d", pid, secondPID)
	}
	if got := blockersOf(t, probeConn, secondPID); !slices.Equal(got, []uint32{firstPID}) {
		t.Errorf("the second import waits on %v, want the first %d", got, firstPID)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("commit the first import: %v", err)
	}
	if err := <-done; err != nil {
		t.Errorf("the second import after the first committed: %v, want it to finish", err)
	}
	if err := second.Commit(ctx); err != nil {
		t.Fatalf("commit the second import: %v", err)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.bank_import_accounts WHERE account IN ('11111111111', '22222222222')`); n != 2 {
		t.Errorf("accounts = %d, want each inserted once", n)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s) between the two imports", after-before)
	}
}
