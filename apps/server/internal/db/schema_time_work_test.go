package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestTimeInvoicedBy_AppliesAndIsIdempotent proves 00037_time_invoiced_by.sql
// applies, rolls back and re-applies cleanly, and pins what the invoices work
// design's D1 rests on in the time schema: the invoice's id and number on an
// entry, both or neither, and only on an invoiced entry with its invoiced_at —
// while an entry marked invoiced by hand, with no invoice, stays allowed. The
// Down takes both columns away again.
func TestTimeInvoicedBy_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 37) // 00037_time_invoiced_by.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	if got := timeInvoicedByColumns(t, ctx, url); got != "invoiced_invoice_id:bigint:YES,invoiced_number:bigint:YES" {
		t.Errorf("invoiced-by columns = %q, want both bigint and nullable", got)
	}

	insert := func(status string, invoicedAt, invoiceID, number any) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO time.entries (user_id, project_id, entry_date, hours, billable, rate_source, status,
			                          invoiced_at, invoiced_invoice_id, invoiced_number, created_at, updated_at)
			VALUES ($1, 1001, DATE '2026-09-14', 2.00, true, 'none', $2, $3::timestamptz, $4::bigint, $5::bigint, now(), now())`,
			uuid.New(), status, invoicedAt, invoiceID, number)
		return err
	}
	const at = "2026-10-05T10:00:00Z"

	if err := insert("invoiced", at, 7001, 10001); err != nil {
		t.Errorf("an entry invoiced by an invoice was refused: %v", err)
	}
	if err := insert("invoiced", at, nil, nil); err != nil {
		t.Errorf("an entry marked invoiced by hand, with no invoice, was refused: %v", err)
	}
	if err := insert("approved", nil, nil, nil); err != nil {
		t.Errorf("an approved entry was refused: %v", err)
	}
	for _, c := range []struct {
		name               string
		status             string
		invoicedAt, id, no any
		constraint         string
	}{
		{"an id without a number", "invoiced", at, 7001, nil, "ck_entries_invoiced_by"},
		{"a number without an id", "invoiced", at, nil, 10001, "ck_entries_invoiced_by"},
		{"an invoice on an approved entry", "approved", at, 7001, 10001, "ck_entries_invoiced_by_status"},
		{"an invoice on an invoiced entry without invoiced_at", "invoiced", nil, 7001, 10001, "ck_entries_invoiced_by_status"},
	} {
		if err := insert(c.status, c.invoicedAt, c.id, c.no); !checkViolationOf(err, c.constraint) {
			t.Errorf("%s: err = %v, want a violation of %s", c.name, err, c.constraint)
		}
	}

	migrateTo(t, url, 36)
	if got := timeInvoicedByColumns(t, ctx, url); got != "MISSING" {
		t.Errorf("after the Down, invoiced-by columns = %q, want none", got)
	}
}

// timeInvoicedByColumns is time.entries' two invoiced-by columns as
// "name:type:nullable", or "MISSING".
func timeInvoicedByColumns(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	var columns string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(string_agg(column_name || ':' || data_type || ':' || is_nullable, ',' ORDER BY column_name COLLATE "C"), 'MISSING')
		FROM information_schema.columns
		WHERE table_schema = 'time' AND table_name = 'entries'
		  AND column_name IN ('invoiced_invoice_id', 'invoiced_number')`).Scan(&columns); err != nil {
		t.Fatalf("read the invoiced-by columns: %v", err)
	}
	return columns
}
