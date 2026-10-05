package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestProjectsInvoicedBy_AppliesAndIsIdempotent proves
// 00039_projects_invoiced_by.sql applies, rolls back and re-applies cleanly,
// and pins what the invoices work design (D1) rests on for a billing
// milestone: the invoice that invoiced it, as an opaque id and number — both
// or neither, and only on an invoiced milestone. A milestone marked invoiced
// by hand carries neither and stays allowed; Down removes both columns.
func TestProjectsInvoicedBy_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 39) // 00039_projects_invoiced_by.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := func() string {
		t.Helper()
		var got string
		if err := pool.QueryRow(ctx, `
			SELECT coalesce(string_agg(column_name || ':' || data_type || ':' || is_nullable, ','
			                ORDER BY column_name COLLATE "C"), '')
			FROM information_schema.columns
			WHERE table_schema = 'projects' AND table_name = 'billing_milestones'
			  AND column_name IN ('invoiced_invoice_id', 'invoiced_number')`).Scan(&got); err != nil {
			t.Fatalf("read the invoiced-by columns: %v", err)
		}
		return got
	}
	if got, want := columns(), "invoiced_invoice_id:bigint:YES,invoiced_number:bigint:YES"; got != want {
		t.Errorf("invoiced-by columns = %q, want %q", got, want)
	}

	var projectID int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects.projects (code, name, billing_type, currency, created_by_user_id, created_at, updated_at)
		VALUES ('INVBY1', 'Invoiced by', 'time-and-materials', 'NOK', $1, now(), now())
		RETURNING id`, uuid.New()).Scan(&projectID); err != nil {
		t.Fatalf("insert a project: %v", err)
	}
	insert := func(status string, invoiceID, number *int64) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO projects.billing_milestones (
			    project_id, name, amount, amount_currency, status, position,
			    invoiced_invoice_id, invoiced_number, created_by_user_id, created_at, updated_at)
			VALUES ($1, 'M', 100, 'NOK', $2, 1, $3, $4, $5, now(), now())`,
			projectID, status, invoiceID, number, uuid.New())
		return err
	}
	id, number := int64(7), int64(10042)

	if err := insert("invoiced", &id, &number); err != nil {
		t.Errorf("an invoiced milestone stamped by an invoice was refused: %v", err)
	}
	if err := insert("invoiced", nil, nil); err != nil {
		t.Errorf("a milestone invoiced by hand, without an invoice, was refused: %v", err)
	}
	if err := insert("invoiced", &id, nil); !checkViolationOf(err, "ck_billing_milestones_invoiced_by") {
		t.Errorf("an id without a number: %v, want ck_billing_milestones_invoiced_by", err)
	}
	if err := insert("invoiced", nil, &number); !checkViolationOf(err, "ck_billing_milestones_invoiced_by") {
		t.Errorf("a number without an id: %v, want ck_billing_milestones_invoiced_by", err)
	}
	for _, status := range []string{"planned", "ready", "cancelled"} {
		if err := insert(status, &id, &number); !checkViolationOf(err, "ck_billing_milestones_invoiced_by_status") {
			t.Errorf("a %s milestone carrying an invoice: %v, want ck_billing_milestones_invoiced_by_status", status, err)
		}
	}

	migrateTo(t, url, 38)
	if got := columns(); got != "" {
		t.Errorf("after Down the invoiced-by columns are still there: %q", got)
	}
}
