package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestExpensesInvoicedBy_AppliesAndIsIdempotent proves
// 00038_expenses_invoiced_by.sql applies, rolls back and re-applies cleanly,
// and pins what the invoices work design (D1, D3) rests on in the expenses
// schema: the two opaque columns the invoices issue stamps, both or neither,
// only beside invoiced_at and never beside a hand-typed reference — a line
// marked by hand is still allowed without them — and expenses.ready_to_invoice,
// the one "ready to invoice" rule, over its whole truth table. The Down removes
// the function and both columns.
func TestExpensesInvoicedBy_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 38) // 00038_expenses_invoiced_by.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var columns string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(string_agg(column_name || ':' || data_type || ':' || is_nullable, ','
		       ORDER BY column_name COLLATE "C"), 'MISSING')
		FROM information_schema.columns
		WHERE table_schema = 'expenses' AND table_name = 'entries'
		  AND column_name IN ('invoiced_invoice_id', 'invoiced_number')`).Scan(&columns); err != nil {
		t.Fatalf("read the invoiced-by columns: %v", err)
	}
	if want := "invoiced_invoice_id:bigint:YES,invoiced_number:bigint:YES"; columns != want {
		t.Errorf("invoiced-by columns = %q, want %q", columns, want)
	}

	insert := func(invoicedAt, reference, invoiceID, number string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO expenses.entries (user_id, created_by_user_id, kind, entry_date, description,
			    currency, gross_amount, paid_by, status, billable, bill_amount, project_id,
			    invoiced_at, invoice_reference, invoiced_invoice_id, invoiced_number, created_at, updated_at)
			VALUES ($1, $1, 'outlay', DATE '2026-03-10', 'Stillas', 'NOK', 1000.00, 'employee', 'approved',
			    true, 1100.00, 1001, `+invoicedAt+`, `+reference+`, `+invoiceID+`, `+number+`, now(), now())`,
			uuid.New())
		return err
	}
	for _, c := range []struct {
		name                                     string
		invoicedAt, reference, invoiceID, number string
		refusedBy                                string
	}{
		{"an id without a number", "now()", "NULL", "7", "NULL", "ck_entries_invoiced_by"},
		{"a number without an id", "now()", "NULL", "NULL", "10042", "ck_entries_invoiced_by"},
		{"a stamp without invoiced_at", "NULL", "NULL", "7", "10042", "ck_entries_invoiced_by_stamp"},
		{"a stamp beside a hand-typed reference", "now()", "'F-1'", "7", "10042", "ck_entries_invoiced_by_stamp"},
	} {
		if err := insert(c.invoicedAt, c.reference, c.invoiceID, c.number); !checkViolationOf(err, c.refusedBy) {
			t.Errorf("%s: %v, want a check violation from %s", c.name, err, c.refusedBy)
		}
	}
	for _, c := range []struct {
		name                                     string
		invoicedAt, reference, invoiceID, number string
	}{
		{"a line marked by hand with a reference", "now()", "'F-1'", "NULL", "NULL"},
		{"a line marked by hand without one", "now()", "NULL", "NULL", "NULL"},
		{"a line the invoices issue stamped", "now()", "NULL", "7", "10042"},
		{"a line nobody invoiced", "NULL", "NULL", "NULL", "NULL"},
	} {
		if err := insert(c.invoicedAt, c.reference, c.invoiceID, c.number); err != nil {
			t.Errorf("%s: %v, want it allowed", c.name, err)
		}
	}

	var volatility, returns string
	var parallel string
	if err := pool.QueryRow(ctx, `
		SELECT p.provolatile::text, p.proparallel::text, pg_catalog.format_type(p.prorettype, NULL)
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'expenses' AND p.proname = 'ready_to_invoice'`).Scan(&volatility, &parallel, &returns); err != nil {
		t.Fatalf("read expenses.ready_to_invoice: %v", err)
	}
	if volatility != "i" || parallel != "s" || returns != "boolean" {
		t.Errorf("expenses.ready_to_invoice is volatility %q, parallel %q, returning %q; want IMMUTABLE (i), PARALLEL SAFE (s), boolean",
			volatility, parallel, returns)
	}

	// The truth table: true exactly for an approved, billable, priced line of
	// any kind but a per diem day. Invoiced or not is no argument.
	priced := "100.00"
	for _, status := range []string{"draft", "submitted", "approved", "rejected"} {
		for _, billable := range []bool{false, true} {
			for _, kind := range []string{"outlay", "mileage", "per_diem", "supplier_invoice"} {
				for _, amount := range []*string{nil, &priced} {
					want := status == "approved" && billable && kind != "per_diem" && amount != nil
					var got *bool
					if err := pool.QueryRow(ctx, `SELECT expenses.ready_to_invoice($1, $2, $3, $4::numeric)`,
						status, billable, kind, amount).Scan(&got); err != nil {
						t.Fatalf("expenses.ready_to_invoice: %v", err)
					}
					if got == nil || *got != want {
						t.Errorf("expenses.ready_to_invoice(%s, %v, %s, priced %v) = %v, want %v",
							status, billable, kind, amount != nil, got, want)
					}
				}
			}
		}
	}

	migrateTo(t, url, 37)
	var functions, left int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		        WHERE n.nspname = 'expenses' AND p.proname = 'ready_to_invoice'),
		       (SELECT count(*) FROM information_schema.columns
		        WHERE table_schema = 'expenses' AND table_name = 'entries'
		          AND column_name IN ('invoiced_invoice_id', 'invoiced_number'))`).Scan(&functions, &left); err != nil {
		t.Fatalf("read what the rollback left: %v", err)
	}
	if functions != 0 || left != 0 {
		t.Errorf("after the rollback %d ready_to_invoice functions and %d invoiced-by columns remain, want none", functions, left)
	}
}
