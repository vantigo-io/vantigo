package db_test

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// invoicesOrderSourcesVersion is 00122_invoices_order_sources.sql (orders
// design O38, O40): an order line's allocation as a line's source, and the
// billing run's orders source.
const invoicesOrderSourcesVersion = 122

// TestInvoicesOrderSources_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what the From orders
// wizard rests on: an allocation held with or without its order's project,
// with a subkind of its own or none, a subscription period's first day and
// no metering point; the work and settlement rows held to 00078's shape as
// before; a billing run's source, and combine for orders only. The Down
// refuses while an allocation is a source.
func TestInvoicesOrderSources_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, invoicesOrderSourcesVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	f := &invoicesWorkFixture{t: t, ctx: ctx, pool: pool, url: url, next: 1}

	doc := f.draft(0)
	first := f.line(doc, 1, 0)
	const source = `
		INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
		    project_id, quantity, amount, currency, source_date, period_from, point_gsrn)
		VALUES ($1, $2, $3, $4, 1, $5, $6, 2, 2160.00, 'NOK', DATE '2026-09-30', $7::date, $8)`
	for _, c := range []struct {
		name    string
		id      int64
		subkind any
		project any
		from    any
	}{
		{"an order line's part without a project", 7001, "order_line", nil, nil},
		{"an order line's part with its order's project", 7002, "order_line", 42, nil},
		{"a subscription period", 7003, "subscription_period", nil, "2026-09-01"},
		{"an allocation without a subkind", 7004, nil, nil, nil},
	} {
		if err := f.exec(source, first, doc, "orders.allocation", c.id, c.subkind, c.project, c.from, nil); err != nil {
			t.Errorf("%s: %v, want it held", c.name, err)
		}
	}
	gsrn := "707057500012345678"
	for _, c := range []struct {
		name, kind string
		subkind    any
		project    any
		gsrn       any
		constraint string
	}{
		{"an allocation of another subkind", "orders.allocation", "regular", nil, nil, "ck_line_sources_subkind"},
		{"an allocation with a metering point", "orders.allocation", "order_line", nil, gsrn, "ck_line_sources_point"},
		{"a settlement without its point, as before", "energy.settlement", "regular", nil, nil, "ck_line_sources_point"},
		{"work with a point, as before", "time.entry", nil, 42, gsrn, "ck_line_sources_point"},
	} {
		if err := f.exec(source, first, doc, c.kind, 900, c.subkind, c.project, nil, c.gsrn); !checkViolationOf(err, c.constraint) {
			t.Errorf("%s: %v, want a check violation from %s", c.name, err, c.constraint)
		}
	}
	if _, err := f.hold(first, doc, "time.entry", 77, "held"); err != nil {
		t.Errorf("an hour as before: %v, want it held", err)
	}

	const run = `
		INSERT INTO invoices.billing_runs (through, issue, source, combine, customers, created_by_user_id, created_at, status, finished_at)
		VALUES (DATE '2026-09-30', false, $1, $2, 1, gen_random_uuid(), now(), 'done', now())`
	if err := f.exec(run, "orders", true); err != nil {
		t.Errorf("a combined run of orders: %v, want it written", err)
	}
	if err := f.exec(run, "consumption", true); !checkViolationOf(err, "ck_billing_runs_combine") {
		t.Errorf("a combined run of consumption: %v, want ck_billing_runs_combine", err)
	}
	if err := f.exec(run, "hours", false); !checkViolationOf(err, "ck_billing_runs_source") {
		t.Errorf("a run of another source: %v, want ck_billing_runs_source", err)
	}
	var source0 string
	if err := pool.QueryRow(ctx, `SELECT column_default FROM information_schema.columns
		WHERE table_schema = 'invoices' AND table_name = 'billing_runs' AND column_name = 'source'`).Scan(&source0); err != nil {
		t.Fatalf("read the source's default: %v", err)
	}
	if source0 != "'consumption'::character varying" {
		t.Errorf("source default = %s, want consumption", source0)
	}

	// The Down refuses while an allocation is a source or a run invoices
	// orders; with neither, it restores 00078's shape.
	if err := migrateDown(t, url, invoicesOrderSourcesVersion-1); err == nil {
		t.Error("the Down with an allocation held: nil, want it refused")
	}
	if err := f.exec(`DELETE FROM invoices.invoices WHERE id = $1`, doc); err != nil {
		t.Fatalf("delete the draft: %v", err)
	}
	if err := f.exec(`DELETE FROM invoices.billing_runs`); err != nil {
		t.Fatalf("delete the runs: %v", err)
	}
	migrateTo(t, url, invoicesOrderSourcesVersion-1)
	if n := f.count(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'invoices' AND table_name = 'billing_runs' AND column_name IN ('source', 'combine')`); n != 0 {
		t.Errorf("after the Down, billing_runs' new columns = %d, want 0", n)
	}
	migrateTo(t, url, invoicesOrderSourcesVersion)
}

// migrateDown is migrateTo's way down to version, answering the Down's
// error instead of failing the test.
func migrateDown(t *testing.T, databaseURL string, version int64) error {
	t.Helper()
	defer holdMigrator()()
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}
	sqlDB := sql.OpenDB(stdlib.GetConnector(*cfg))
	defer func() { _ = sqlDB.Close() }()
	dir, err := fs.Sub(db.MigrationsFS, "migrations")
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	_, err = provider.DownTo(context.Background(), version)
	return err
}
