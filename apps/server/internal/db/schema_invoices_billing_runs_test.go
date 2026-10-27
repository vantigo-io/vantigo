package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// invoicesBillingRunsVersion is 00079_invoices_billing_runs.sql (energy
// consumption billing design D10): billing runs and their items.
const invoicesBillingRunsVersion = 79

// TestInvoicesBillingRuns_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what the run and its
// worker rest on: one run queued or running at a time, a finished run with
// its time, one item per customer of a run, a code exactly on a skipped or
// failed item, a finished item with its time, and the items going with
// their run.
func TestInvoicesBillingRuns_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, invoicesBillingRunsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) error {
		_, err := pool.Exec(ctx, sql, args...)
		return err
	}
	const run = `
		INSERT INTO invoices.billing_runs (through, issue, status, customers, created_by_user_id, created_at, finished_at)
		VALUES (DATE '2026-09-30', true, $1, 2, gen_random_uuid(), now(), $2)
		RETURNING id`
	var first int64
	if err := pool.QueryRow(ctx, run, "queued", nil).Scan(&first); err != nil {
		t.Fatalf("a queued run: %v", err)
	}
	if err := exec(run, "running", nil); !uniqueViolationOf(err, "ux_billing_runs_active") {
		t.Errorf("a second run while one is queued: %v, want ux_billing_runs_active", err)
	}
	if err := exec(run, "done", "2026-10-01T10:00:00Z"); err != nil {
		t.Errorf("a done run beside the queued one: %v, want it allowed", err)
	}
	for _, c := range []struct {
		name, status string
		finished     any
		constraint   string
	}{
		{"an unknown status", "paused", nil, "ck_billing_runs_status"},
		{"a done run without its time", "done", nil, "ck_billing_runs_finished"},
		{"a cancelled run without its time", "cancelled", nil, "ck_billing_runs_finished"},
	} {
		if err := exec(run, c.status, c.finished); !checkViolationOf(err, c.constraint) {
			t.Errorf("%s: %v, want a check violation from %s", c.name, err, c.constraint)
		}
	}

	const item = `
		INSERT INTO invoices.billing_run_items (run_id, customer_id, status, code, finished_at, next_attempt_at)
		VALUES ($1, $2, $3, $4, $5, now())`
	if err := exec(item, first, 1001, "queued", nil, nil); err != nil {
		t.Fatalf("a queued item: %v", err)
	}
	if err := exec(item, first, 1001, "queued", nil, nil); !uniqueViolationOf(err, "ux_billing_run_items_customer") {
		t.Errorf("a customer twice in a run: %v, want ux_billing_run_items_customer", err)
	}
	if err := exec(item, first, 1002, "skipped", "settlement_blocked", "2026-10-01T10:00:00Z"); err != nil {
		t.Errorf("a skipped item with its code: %v, want it allowed", err)
	}
	for _, c := range []struct {
		name, status string
		code, at     any
		constraint   string
	}{
		{"an unknown status", "paused", nil, "2026-10-01T10:00:00Z", "ck_billing_run_items_status"},
		{"a skipped item without a code", "skipped", nil, "2026-10-01T10:00:00Z", "ck_billing_run_items_code"},
		{"a drafted item with a code", "drafted", "settlement_blocked", "2026-10-01T10:00:00Z", "ck_billing_run_items_code"},
		{"an issued item without its time", "issued", nil, nil, "ck_billing_run_items_finished"},
		{"a queued item with a time", "queued", nil, "2026-10-01T10:00:00Z", "ck_billing_run_items_finished"},
	} {
		if err := exec(item, first, 2000, c.status, c.code, c.at); !checkViolationOf(err, c.constraint) {
			t.Errorf("%s: %v, want a check violation from %s", c.name, err, c.constraint)
		}
	}
	var ids []int64
	if err := pool.QueryRow(ctx, `SELECT invoice_ids FROM invoices.billing_run_items WHERE run_id = $1 AND customer_id = 1001`, first).Scan(&ids); err != nil {
		t.Fatalf("read the item's documents: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("a new item's invoice_ids = %v, want none", ids)
	}

	if err := exec(`DELETE FROM invoices.billing_runs WHERE id = $1`, first); err != nil {
		t.Fatalf("delete the run: %v", err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices.billing_run_items WHERE run_id = $1`, first).Scan(&left); err != nil {
		t.Fatalf("count the items: %v", err)
	}
	if left != 0 {
		t.Errorf("after the run was deleted, %d of its items are left, want none", left)
	}
}
