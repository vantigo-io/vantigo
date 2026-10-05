package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The invoices work schema (00040_invoices_work.sql; invoices work design D2,
// D5, D7, D8, D9): the line sources held from the draft with their one
// transition, the credit note's releases, the timesheet rows, the deduction
// line and the document's project, and the work settings. Its own file, so
// the parallel phase-3 migrations rarely meet in schema_test.go.

// invoicesWorkVersion is 00040's goose version; Tasks 2–4's 00037–00039 sit
// below it, so the Down is checked by migrating to 39 whether or not they
// are in the tree.
const invoicesWorkVersion = 40

// invoicesWorkViolation reports whether err is SQLSTATE code raised by the
// constraint named constraint.
func invoicesWorkViolation(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}

// invoicesWorkObjects is what 00040 adds to the invoices schema, as found:
// the new tables' columns and the added ones (table.column:type:length or
// precision:nullable:default), its tables, its triggers
// (table:trigger:function), its constraints by name with their definitions
// (the CHECKs, the composite keys and the foreign keys — ck_lines_quantity
// stands both before and after, with its old definition after the Down), its
// function and its indexes as Postgres prints them.
func invoicesWorkObjects(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string][]string {
	t.Helper()
	collect := func(what, sql string) []string {
		t.Helper()
		rows, err := pool.Query(ctx, sql)
		if err != nil {
			t.Fatalf("query %s: %v", what, err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("collect %s: %v", what, err)
		}
		return got
	}
	return map[string][]string{
		"columns": collect("columns", `
			SELECT table_name || '.' || column_name || ':' || data_type || ':'
			    || coalesce(character_maximum_length::text, CASE WHEN data_type = 'numeric' THEN numeric_precision || ',' || numeric_scale END, '')
			    || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'invoices'
			  AND (table_name IN ('line_sources', 'line_releases', 'timesheet_rows')
			    OR (table_name = 'lines' AND column_name = 'deducts_invoice_id')
			    OR (table_name = 'invoices' AND column_name IN ('project_id', 'project_reference', 'timesheet'))
			    OR (table_name = 'settings' AND column_name IN ('work_vat_code_hours', 'work_vat_code_expenses',
			        'work_vat_code_milestones', 'timesheet_default', 'timesheet_person_label')))
			ORDER BY 1`),
		"tables": collect("tables", `
			SELECT table_name FROM information_schema.tables
			WHERE table_schema = 'invoices' AND table_name IN ('line_sources', 'line_releases', 'timesheet_rows')
			ORDER BY 1`),
		"triggers": collect("triggers", `
			SELECT c.relname || ':' || t.tgname || ':' || p.proname
			FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_proc p ON p.oid = t.tgfoid
			WHERE n.nspname = 'invoices' AND NOT t.tgisinternal AND c.relname IN ('line_sources', 'line_releases', 'timesheet_rows')
			ORDER BY 1`),
		"constraints": collect("constraints", `
			SELECT c.conrelid::regclass::text || ':' || c.conname || ':' || pg_get_constraintdef(c.oid)
			FROM pg_constraint c
			WHERE c.connamespace = 'invoices'::regnamespace AND c.contype IN ('c', 'f', 'u')
			  AND (c.conrelid::regclass::text IN ('invoices.line_sources', 'invoices.line_releases', 'invoices.timesheet_rows')
			    OR c.conname IN ('uq_lines_id_invoice', 'ck_lines_quantity', 'ck_lines_deduction_no_discount',
			        'ck_invoices_project', 'ck_settings_timesheet_person_label',
			        'settings_work_vat_code_hours_fkey', 'settings_work_vat_code_expenses_fkey',
			        'settings_work_vat_code_milestones_fkey'))
			ORDER BY 1`),
		"functions": collect("functions", `
			SELECT p.proname || ':' || p.provolatile::text || ':' || pg_catalog.format_type(p.prorettype, NULL)
			FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'invoices' AND p.proname = 'refuse_issued_line_source_change'
			ORDER BY 1`),
		"indexes": collect("indexes", `
			SELECT indexname || ':' || indexdef FROM pg_indexes
			WHERE schemaname = 'invoices'
			  AND (tablename IN ('line_sources', 'line_releases', 'timesheet_rows')
			    OR indexname IN ('uq_lines_id_invoice', 'ix_lines_deducts', 'ix_invoices_project'))
			ORDER BY 1`),
	}
}

// TestInvoicesWork_AppliesAndIsIdempotent proves 00040_invoices_work.sql
// applies, rolls back and re-applies cleanly, and pins every object it adds:
// line_sources with the exact amount numeric(22,8), the expense's subkind,
// the state and the floor — the partial unique index over held and invoiced
// (D2); line_releases with the credit note's invoice_id (D8); timesheet_rows
// with person_label varchar(200) (D5); the composite keys that tie a child
// row's invoice_id to its line's, cascading from the line; the document's
// project and timesheet flag (D9, D5); the deduction line's column and the
// relaxed quantity CHECK (D7); the work settings (D6, D5); and the three
// triggers. The Down removes all of it and restores the old CHECK.
func TestInvoicesWork_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, invoicesWorkVersion) // 00040_invoices_work.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	want := map[string][]string{
		"columns": {
			"invoices.project_id:integer::YES:",
			"invoices.project_reference:character varying:30:YES:",
			"invoices.timesheet:boolean::NO:false",
			"line_releases.credit_line_id:bigint::NO:",
			"line_releases.id:bigint::NO:",
			"line_releases.invoice_id:bigint::NO:",
			"line_releases.line_source_id:bigint::NO:",
			"line_sources.amount:numeric:22,8:NO:",
			"line_sources.currency:character:3:NO:",
			"line_sources.id:bigint::NO:",
			"line_sources.invoice_id:bigint::NO:",
			"line_sources.line_id:bigint::NO:",
			"line_sources.project_id:integer::NO:",
			"line_sources.quantity:numeric:12,3:NO:",
			"line_sources.source_date:date::NO:",
			"line_sources.source_id:bigint::NO:",
			"line_sources.source_kind:character varying:30:NO:",
			"line_sources.source_revision:integer::NO:",
			"line_sources.source_subkind:character varying:20:YES:",
			"line_sources.state:character varying:10:NO:'held'::character varying",
			"lines.deducts_invoice_id:bigint::YES:",
			"settings.timesheet_default:boolean::NO:false",
			"settings.timesheet_person_label:character varying:10:NO:'initials'::character varying",
			"settings.work_vat_code_expenses:integer::NO:1",
			"settings.work_vat_code_hours:integer::NO:1",
			"settings.work_vat_code_milestones:integer::NO:1",
			"timesheet_rows.description:character varying:200:NO:",
			"timesheet_rows.entry_date:date::NO:",
			"timesheet_rows.hours:numeric:5,2:NO:",
			"timesheet_rows.id:bigint::NO:",
			"timesheet_rows.invoice_id:bigint::NO:",
			"timesheet_rows.person_label:character varying:200:NO:",
			"timesheet_rows.position:integer::NO:",
			"timesheet_rows.source_id:bigint::NO:",
			"timesheet_rows.work_type:character varying:100:YES:",
		},
		"tables": {"line_releases", "line_sources", "timesheet_rows"},
		"triggers": {
			"line_releases:tr_line_releases_immutable:refuse_issued_child_change",
			"line_sources:tr_line_sources_immutable:refuse_issued_line_source_change",
			"timesheet_rows:tr_timesheet_rows_immutable:refuse_issued_child_change",
		},
		"constraints": {
			"invoices.invoices:ck_invoices_project:CHECK (((project_id IS NULL) = (project_reference IS NULL)))",
			"invoices.line_releases:fk_line_releases_line:FOREIGN KEY (credit_line_id, invoice_id) REFERENCES invoices.lines(id, invoice_id) ON DELETE CASCADE",
			"invoices.line_releases:line_releases_line_source_id_fkey:FOREIGN KEY (line_source_id) REFERENCES invoices.line_sources(id) ON DELETE RESTRICT",
			"invoices.line_sources:ck_line_sources_kind:CHECK (((source_kind)::text = ANY ((ARRAY['time.entry'::character varying, 'expenses.entry'::character varying, 'projects.milestone'::character varying])::text[])))",
			"invoices.line_sources:ck_line_sources_quantity:CHECK ((quantity > (0)::numeric))",
			"invoices.line_sources:ck_line_sources_state:CHECK (((state)::text = ANY ((ARRAY['held'::character varying, 'invoiced'::character varying, 'released'::character varying])::text[])))",
			"invoices.line_sources:ck_line_sources_subkind:CHECK ((((source_kind)::text = 'expenses.entry'::text) = (source_subkind IS NOT NULL)))",
			"invoices.line_sources:fk_line_sources_line:FOREIGN KEY (line_id, invoice_id) REFERENCES invoices.lines(id, invoice_id) ON DELETE CASCADE",
			"invoices.lines:ck_lines_deduction_no_discount:CHECK (((deducts_invoice_id IS NULL) OR (discount_percent = (0)::numeric)))",
			"invoices.lines:ck_lines_quantity:CHECK (((quantity > (0)::numeric) OR ((quantity < (0)::numeric) AND (deducts_invoice_id IS NOT NULL))))",
			"invoices.lines:uq_lines_id_invoice:UNIQUE (id, invoice_id)",
			"invoices.settings:ck_settings_timesheet_person_label:CHECK (((timesheet_person_label)::text = ANY ((ARRAY['initials'::character varying, 'number'::character varying, 'name'::character varying])::text[])))",
			"invoices.settings:settings_work_vat_code_expenses_fkey:FOREIGN KEY (work_vat_code_expenses) REFERENCES invoices.vat_codes(id) ON DELETE RESTRICT",
			"invoices.settings:settings_work_vat_code_hours_fkey:FOREIGN KEY (work_vat_code_hours) REFERENCES invoices.vat_codes(id) ON DELETE RESTRICT",
			"invoices.settings:settings_work_vat_code_milestones_fkey:FOREIGN KEY (work_vat_code_milestones) REFERENCES invoices.vat_codes(id) ON DELETE RESTRICT",
			"invoices.timesheet_rows:ck_timesheet_rows_hours:CHECK ((hours > (0)::numeric))",
			"invoices.timesheet_rows:ck_timesheet_rows_position:CHECK ((\"position\" >= 1))",
			"invoices.timesheet_rows:timesheet_rows_invoice_id_fkey:FOREIGN KEY (invoice_id) REFERENCES invoices.invoices(id) ON DELETE CASCADE",
		},
		"functions": {"refuse_issued_line_source_change:v:trigger"},
		"indexes": {
			"ix_invoices_project:CREATE INDEX ix_invoices_project ON invoices.invoices USING btree (project_id) WHERE (project_id IS NOT NULL)",
			"ix_line_releases_invoice:CREATE INDEX ix_line_releases_invoice ON invoices.line_releases USING btree (invoice_id)",
			"ix_line_sources_invoice:CREATE INDEX ix_line_sources_invoice ON invoices.line_sources USING btree (invoice_id)",
			"ix_line_sources_line:CREATE INDEX ix_line_sources_line ON invoices.line_sources USING btree (line_id)",
			"ix_lines_deducts:CREATE INDEX ix_lines_deducts ON invoices.lines USING btree (deducts_invoice_id) WHERE (deducts_invoice_id IS NOT NULL)",
			"line_releases_pkey:CREATE UNIQUE INDEX line_releases_pkey ON invoices.line_releases USING btree (id)",
			"line_sources_pkey:CREATE UNIQUE INDEX line_sources_pkey ON invoices.line_sources USING btree (id)",
			"timesheet_rows_pkey:CREATE UNIQUE INDEX timesheet_rows_pkey ON invoices.timesheet_rows USING btree (id)",
			"uq_lines_id_invoice:CREATE UNIQUE INDEX uq_lines_id_invoice ON invoices.lines USING btree (id, invoice_id)",
			"ux_line_releases_source:CREATE UNIQUE INDEX ux_line_releases_source ON invoices.line_releases USING btree (line_source_id)",
			"ux_line_sources_live:CREATE UNIQUE INDEX ux_line_sources_live ON invoices.line_sources USING btree (source_kind, source_id) WHERE ((state)::text = ANY ((ARRAY['held'::character varying, 'invoiced'::character varying])::text[]))",
			"ux_timesheet_rows_invoice_position:CREATE UNIQUE INDEX ux_timesheet_rows_invoice_position ON invoices.timesheet_rows USING btree (invoice_id, \"position\")",
		},
	}
	// After the Down: nothing of 00040's but ck_lines_quantity, as 00034 wrote it.
	wantDown := map[string][]string{
		"constraints": {"invoices.lines:ck_lines_quantity:CHECK ((quantity > (0)::numeric))"},
	}
	expect := func(when string, want map[string][]string) {
		t.Helper()
		got := invoicesWorkObjects(t, ctx, pool)
		for _, what := range []string{"columns", "tables", "triggers", "constraints", "functions", "indexes"} {
			if !equalStrings(got[what], want[what]) {
				t.Errorf("%s: %s = %q, want %q", when, what, got[what], want[what])
			}
		}
	}
	expect("after up", want)

	// The floor's predicate, read on its own: held and invoiced only.
	if def := indexDefinition(t, ctx, pool, "invoices", "ux_line_sources_live"); def != "CREATE UNIQUE INDEX ux_line_sources_live ON invoices.line_sources USING btree (source_kind, source_id) WHERE ((state)::text = ANY ((ARRAY['held'::character varying, 'invoiced'::character varying])::text[]))" {
		t.Errorf("ux_line_sources_live = %s, want unique over (source_kind, source_id) where state is held or invoiced", def)
	}

	migrateTo(t, url, invoicesWorkVersion-1)
	expect("after down", wantDown)

	migrateTo(t, url, invoicesWorkVersion)
	expect("after up again", want)
}

// invoicesWorkFixture is a migrated database and the statements the
// behaviour tests build their documents with.
type invoicesWorkFixture struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	url  string
	next int64 // the next document number to issue
}

func newInvoicesWorkFixture(t *testing.T) *invoicesWorkFixture {
	t.Helper()
	pool, url := testdb.Migrated(t)
	return &invoicesWorkFixture{t: t, ctx: context.Background(), pool: pool, url: url, next: 1}
}

// draft makes an invoice draft of customer 7, or a credit-note draft of
// credits when it is non-zero.
func (f *invoicesWorkFixture) draft(credits int64) int64 {
	f.t.Helper()
	kind := "invoice"
	var creditsID *int64
	if credits != 0 {
		kind, creditsID = "credit_note", &credits
	}
	var id int64
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO invoices.invoices (kind, customer_id, credits_invoice_id, created_by_user_id, created_at, updated_at)
		VALUES ($1, 7, $2, gen_random_uuid(), now(), now()) RETURNING id`, kind, creditsID).Scan(&id); err != nil {
		f.t.Fatalf("seed a %s draft: %v", kind, err)
	}
	return id
}

// line adds a line at position to invoiceID, crediting credits when it is
// non-zero.
func (f *invoicesWorkFixture) line(invoiceID int64, position int, credits int64) int64 {
	f.t.Helper()
	var creditsID *int64
	if credits != 0 {
		creditsID = &credits
	}
	var id int64
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, vat_code_id, credits_line_id, line_gross, line_allowance, line_net)
		VALUES ($1, $2, 'Konsulenttimer', 1, 1000, 1, $3, 1000, 0, 1000) RETURNING id`, invoiceID, position, creditsID).Scan(&id); err != nil {
		f.t.Fatalf("seed line %d of %d: %v", position, invoiceID, err)
	}
	return id
}

// hold writes a line source of kind and sourceID on lineID under invoiceID,
// in state, and answers its id or the refusal.
func (f *invoicesWorkFixture) hold(lineID, invoiceID int64, kind string, sourceID int64, state string) (int64, error) {
	var subkind *string
	if kind == "expenses.entry" {
		s := "expense"
		subkind = &s
	}
	var id int64
	err := f.pool.QueryRow(f.ctx, `
		INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
		    project_id, quantity, amount, currency, state, source_date)
		VALUES ($1, $2, $3, $4, 1, $5, 42, 1.25, 188.43228125, 'NOK', $6, DATE '2026-10-01') RETURNING id`,
		lineID, invoiceID, kind, sourceID, subkind, state).Scan(&id)
	return id, err
}

// mustHold is hold that must succeed, in state held.
func (f *invoicesWorkFixture) mustHold(lineID, invoiceID int64, kind string, sourceID int64) int64 {
	f.t.Helper()
	id, err := f.hold(lineID, invoiceID, kind, sourceID, "held")
	if err != nil {
		f.t.Fatalf("hold %s %d on line %d: %v", kind, sourceID, lineID, err)
	}
	return id
}

// exec runs sql and answers its error.
func (f *invoicesWorkFixture) exec(sql string, args ...any) error {
	_, err := f.pool.Exec(f.ctx, sql, args...)
	return err
}

// issue moves invoiceID from draft to issued with the next number, as the
// issue's own update does: the number, the dates and the two names.
func (f *invoicesWorkFixture) issue(invoiceID int64) {
	f.t.Helper()
	if err := f.exec(`
		UPDATE invoices.invoices SET status = 'issued', number = $2, issue_date = DATE '2026-10-05',
		    due_date = CASE WHEN kind = 'invoice' THEN DATE '2026-10-19' END, exchange_rate_date = DATE '2026-10-05',
		    seller_legal_name = 'Selger AS', buyer_name = 'Kunde AS', issued_at = now()
		WHERE id = $1`, invoiceID, f.next); err != nil {
		f.t.Fatalf("issue %d: %v", invoiceID, err)
	}
	f.next++
}

// count answers a count(*) query.
func (f *invoicesWorkFixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, sql, args...).Scan(&n); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

const (
	invoicesWorkImmutable  = "invoices: issued document is immutable"
	invoicesWorkHeld       = "invoices: a line source is written held"
	invoicesWorkStateOnly  = "invoices: a line source changes only its state"
	invoicesWorkHeldDelete = "invoices: a line source is deleted only while held"
	invoicesWorkSetState   = `UPDATE invoices.line_sources SET state = $2 WHERE id = $1`
	invoicesWorkSetRevAndS = `UPDATE invoices.line_sources SET state = $2, source_revision = source_revision + 1 WHERE id = $1`
)

// TestInvoicesWork_TheLineSourceTrigger pins refuse_issued_line_source_change
// (D2, plan reading 8): a source is written held; under a draft its one
// change is held → invoiced, nothing else changed; under an issued parent
// every INSERT, DELETE and UPDATE is refused but invoiced → released, once;
// a row is deleted only while held, a draft's invoiced row included; and a
// cascade from deleting a draft passes, as 00034's children's do.
func TestInvoicesWork_TheLineSourceTrigger(t *testing.T) {
	f := newInvoicesWorkFixture(t)
	draft := f.draft(0)
	line := f.line(draft, 1, 0)

	if _, err := f.hold(line, draft, "time.entry", 1, "invoiced"); !refusedWith(err, invoicesWorkHeld) {
		t.Errorf("an insert as invoiced: %v, want %q", err, invoicesWorkHeld)
	}
	if _, err := f.hold(line, draft, "time.entry", 1, "released"); !refusedWith(err, invoicesWorkHeld) {
		t.Errorf("an insert as released: %v, want %q", err, invoicesWorkHeld)
	}
	first := f.mustHold(line, draft, "time.entry", 1)
	second := f.mustHold(line, draft, "expenses.entry", 2)
	dropped := f.mustHold(line, draft, "projects.milestone", 3)

	// Under the draft: held → released is refused, and so is a revision
	// changed with the state; held → invoiced alone passes, and nothing after
	// it but the issue.
	if err := f.exec(invoicesWorkSetState, first, "released"); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("held → released under a draft: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(invoicesWorkSetRevAndS, first, "invoiced"); !refusedWith(err, invoicesWorkStateOnly) {
		t.Errorf("a revision changed with the state: %v, want %q", err, invoicesWorkStateOnly)
	}
	if err := f.exec(`UPDATE invoices.line_sources SET amount = 1 WHERE id = $1`, first); !refusedWith(err, invoicesWorkStateOnly) {
		t.Errorf("an amount changed under a draft: %v, want %q", err, invoicesWorkStateOnly)
	}
	if err := f.exec(invoicesWorkSetState, first, "invoiced"); err != nil {
		t.Errorf("held → invoiced under a draft: %v, want it allowed", err)
	}
	if err := f.exec(invoicesWorkSetState, first, "held"); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("invoiced → held under a draft: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(invoicesWorkSetState, first, "released"); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("invoiced → released under a draft: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(invoicesWorkSetState, second, "invoiced"); err != nil {
		t.Errorf("held → invoiced under a draft: %v, want it allowed", err)
	}
	// A draft drops a hold by deleting it, and nothing but a hold: an
	// invoiced row stays, alone or through its line's cascade.
	if err := f.exec(`DELETE FROM invoices.line_sources WHERE id = $1`, dropped); err != nil {
		t.Errorf("deleting a held row under a draft: %v, want it allowed", err)
	}
	if err := f.exec(`DELETE FROM invoices.line_sources WHERE id = $1`, first); !refusedWith(err, invoicesWorkHeldDelete) {
		t.Errorf("deleting an invoiced row under a draft: %v, want %q", err, invoicesWorkHeldDelete)
	}
	if err := f.exec(`DELETE FROM invoices.lines WHERE id = $1`, line); !refusedWith(err, invoicesWorkHeldDelete) {
		t.Errorf("deleting a draft's line with an invoiced source: %v, want %q", err, invoicesWorkHeldDelete)
	}

	f.issue(draft)

	// Under the issued parent: no insert, no delete, no other change.
	if _, err := f.hold(line, draft, "time.entry", 4, "held"); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("an insert under an issued document: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(`DELETE FROM invoices.line_sources WHERE id = $1`, first); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("a delete under an issued document: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(invoicesWorkSetState, first, "held"); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("invoiced → held under an issued document: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(invoicesWorkSetState, first, "invoiced"); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("invoiced → invoiced under an issued document: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(invoicesWorkSetRevAndS, first, "released"); !refusedWith(err, invoicesWorkStateOnly) {
		t.Errorf("a revision changed with the release: %v, want %q", err, invoicesWorkStateOnly)
	}
	if err := f.exec(invoicesWorkSetState, first, "released"); err != nil {
		t.Errorf("invoiced → released under an issued document: %v, want it allowed", err)
	}
	// Once: a released row changes no more.
	for _, state := range []string{"released", "invoiced", "held"} {
		if err := f.exec(invoicesWorkSetState, first, state); !refusedWith(err, invoicesWorkImmutable) {
			t.Errorf("released → %s under an issued document: %v, want %q", state, err, invoicesWorkImmutable)
		}
	}
	if got := f.count(`SELECT count(*) FROM invoices.line_sources WHERE invoice_id = $1 AND state = 'invoiced'`, draft); got != 1 {
		t.Errorf("invoiced rows under the issued document = %d, want the one not released", got)
	}

	// A cascade from deleting a draft — and from deleting a draft's line —
	// passes: the parent is gone, or still a draft.
	other := f.draft(0)
	kept := f.line(other, 1, 0)
	removed := f.line(other, 2, 0)
	f.mustHold(kept, other, "time.entry", 10)
	f.mustHold(removed, other, "time.entry", 11)
	if err := f.exec(`DELETE FROM invoices.lines WHERE id = $1`, removed); err != nil {
		t.Errorf("deleting a draft's line: %v, want its sources to go with it", err)
	}
	if got := f.count(`SELECT count(*) FROM invoices.line_sources WHERE line_id = $1`, removed); got != 0 {
		t.Errorf("sources of a deleted line = %d, want none", got)
	}
	if err := f.exec(`DELETE FROM invoices.invoices WHERE id = $1`, other); err != nil {
		t.Errorf("deleting a draft with held sources: %v, want the cascade allowed", err)
	}
	if got := f.count(`SELECT count(*) FROM invoices.line_sources WHERE invoice_id = $1`, other); got != 0 {
		t.Errorf("sources of a deleted draft = %d, want none", got)
	}
}

// TestInvoicesWork_TheLiveIndex pins the floor (D2): a source is live on one
// row at most — held or invoiced — while a released row frees it to be
// held again; the kind is part of the identity.
func TestInvoicesWork_TheLiveIndex(t *testing.T) {
	f := newInvoicesWorkFixture(t)
	a := f.draft(0)
	lineA := f.line(a, 1, 0)
	b := f.draft(0)
	lineB := f.line(b, 1, 0)

	held := f.mustHold(lineA, a, "time.entry", 500)
	if _, err := f.hold(lineB, b, "time.entry", 500, "held"); !invoicesWorkViolation(err, "23505", "ux_line_sources_live") {
		t.Errorf("a second hold of a held source: %v, want a unique violation on ux_line_sources_live", err)
	}
	if _, err := f.hold(lineA, a, "time.entry", 500, "held"); !invoicesWorkViolation(err, "23505", "ux_line_sources_live") {
		t.Errorf("a second hold on the same draft: %v, want a unique violation on ux_line_sources_live", err)
	}
	if _, err := f.hold(lineB, b, "expenses.entry", 500, "held"); err != nil {
		t.Errorf("another kind's source of the same id: %v, want it allowed", err)
	}

	if err := f.exec(invoicesWorkSetState, held, "invoiced"); err != nil {
		t.Fatalf("mark invoiced: %v", err)
	}
	f.issue(a)
	if _, err := f.hold(lineB, b, "time.entry", 500, "held"); !invoicesWorkViolation(err, "23505", "ux_line_sources_live") {
		t.Errorf("a hold of an invoiced source: %v, want a unique violation on ux_line_sources_live", err)
	}
	if err := f.exec(invoicesWorkSetState, held, "released"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := f.hold(lineB, b, "time.entry", 500, "held"); err != nil {
		t.Errorf("a hold of a released source: %v, want it allowed beside the released row", err)
	}
	if got := f.count(`SELECT count(*) FROM invoices.line_sources WHERE source_kind = 'time.entry' AND source_id = 500`); got != 2 {
		t.Errorf("rows of time.entry 500 = %d, want the released one and the live one", got)
	}
}

// invoicesWorkSource is one source a hold names, in the order it is given.
type invoicesWorkSource struct {
	kind string
	id   int64
}

// invoicesWorkQuery is the statement named name in
// internal/invoices/queries/work.sql with its sqlc parameters (@name)
// rewritten to $1, $2, … in order of first appearance, as sqlc numbers them,
// and those names in that order. It is read as text because internal/db, a
// platform package, imports no module (depguard): this way the test runs the
// very statement sqlc compiles into the store, not a copy of it.
func invoicesWorkQuery(t *testing.T, name string) (string, []string) {
	t.Helper()
	// The test runs from internal/db, as collectSchemaOwnedFiles' glob does.
	body, err := os.ReadFile("../invoices/queries/work.sql")
	if err != nil {
		t.Fatalf("read work.sql: %v", err)
	}
	_, statement, found := strings.Cut(string(body), "-- name: "+name+" ")
	if !found {
		t.Fatalf("work.sql has no query %s", name)
	}
	statement, _, _ = strings.Cut(statement, "-- name: ")
	var lines []string
	for _, line := range strings.Split(statement, "\n")[1:] {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	var params []string
	sql := regexp.MustCompile(`@([a-z_]+)`).ReplaceAllStringFunc(strings.Join(lines, "\n"), func(m string) string {
		i := slices.Index(params, m[1:])
		if i < 0 {
			params = append(params, m[1:])
			i = len(params) - 1
		}
		return fmt.Sprintf("$%d", i+1)
	})
	return sql, params
}

// invoicesWorkHolds is InsertLineSources' arguments, by parameter name, for
// sources held on lineID of invoiceID, in the order given.
func invoicesWorkHolds(t *testing.T, invoiceID, lineID int64, sources []invoicesWorkSource) map[string]any {
	t.Helper()
	var quantity, amount pgtype.Numeric
	if err := quantity.Scan("1.25"); err != nil {
		t.Fatalf("quantity: %v", err)
	}
	if err := amount.Scan("188.43228125"); err != nil {
		t.Fatalf("amount: %v", err)
	}
	date := pgtype.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Valid: true}
	var (
		lineIDs, ids                []int64
		kinds, subkinds, currencies []string
		revisions, projectIDs       []int32
		quantities, amounts         []pgtype.Numeric
		dates                       []pgtype.Date
	)
	for _, s := range sources {
		subkind := ""
		if s.kind == "expenses.entry" {
			subkind = "expense"
		}
		lineIDs, kinds, ids = append(lineIDs, lineID), append(kinds, s.kind), append(ids, s.id)
		revisions, subkinds, projectIDs = append(revisions, 1), append(subkinds, subkind), append(projectIDs, 42)
		quantities, amounts = append(quantities, quantity), append(amounts, amount)
		currencies, dates = append(currencies, "NOK"), append(dates, date)
	}
	return map[string]any{
		"invoice_id": invoiceID, "line_ids": lineIDs, "kinds": kinds, "ids": ids, "revisions": revisions,
		"subkinds": subkinds, "project_ids": projectIDs, "quantities": quantities, "amounts": amounts,
		"currencies": currencies, "dates": dates,
	}
}

// TestInvoicesWork_OverlappingHoldsInOppositeOrderFailOnTheIndexNeverDeadlock
// pins D2's one-statement rule through the query itself (invoicesWorkQuery):
// two transactions,
// each on a connection of its own, hold the same 2 000 sources of two kinds
// given in opposite orders, through InsertLineSources at the same moment.
// Its ORDER BY (source_kind, source_id) makes both wait on
// ux_line_sources_live in one order, so the second fails with the unique
// violation once the first commits — never 40P01, a deadlock both would
// otherwise reach meeting in the middle. The store's subkind and the exact
// amount survive the round trip.
func TestInvoicesWork_OverlappingHoldsInOppositeOrderFailOnTheIndexNeverDeadlock(t *testing.T) {
	f := newInvoicesWorkFixture(t)
	var given []invoicesWorkSource
	for id := int64(1); id <= 1000; id++ {
		given = append(given, invoicesWorkSource{"time.entry", id}, invoicesWorkSource{"expenses.entry", id})
	}
	reversed := slices.Clone(given)
	slices.Reverse(reversed)

	insert, params := invoicesWorkQuery(t, "InsertLineSources")
	a, b := f.draft(0), f.draft(0)
	var holds [][]any
	for _, named := range []map[string]any{
		invoicesWorkHolds(t, a, f.line(a, 1, 0), given),
		invoicesWorkHolds(t, b, f.line(b, 1, 0), reversed),
	} {
		var args []any
		for _, p := range params {
			arg, ok := named[p]
			if !ok {
				t.Fatalf("InsertLineSources takes @%s, which the test does not give", p)
			}
			args = append(args, arg)
		}
		if len(args) != len(named) {
			t.Fatalf("InsertLineSources takes %v, the test gives %d", params, len(named))
		}
		holds = append(holds, args)
	}
	txs := make([]pgx.Tx, len(holds))
	for i := range holds {
		conn, err := pgx.Connect(f.ctx, f.url)
		if err != nil {
			t.Fatalf("connect %d: %v", i, err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })
		if _, err := conn.Exec(f.ctx, `SET statement_timeout = '60s'`); err != nil {
			t.Fatalf("statement timeout %d: %v", i, err)
		}
		if txs[i], err = conn.Begin(f.ctx); err != nil {
			t.Fatalf("begin %d: %v", i, err)
		}
	}

	start := make(chan struct{})
	errs := make([]error, len(holds))
	var wg sync.WaitGroup
	for i := range holds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := txs[i].Exec(f.ctx, insert, holds[i]...); err != nil {
				errs[i] = err
				_ = txs[i].Rollback(context.Background())
				return
			}
			errs[i] = txs[i].Commit(f.ctx)
		}()
	}
	close(start)
	wg.Wait()

	var unique, committed int
	for i, err := range errs {
		var pgErr *pgconn.PgError
		switch {
		case err == nil:
			committed++
		case errors.As(err, &pgErr) && pgErr.Code == "40P01":
			t.Errorf("transaction %d: %v, want never a deadlock", i, err)
		case invoicesWorkViolation(err, "23505", "ux_line_sources_live"):
			unique++
		default:
			t.Errorf("transaction %d: %v, want success or a unique violation on ux_line_sources_live", i, err)
		}
	}
	if committed != 1 || unique != 1 {
		t.Errorf("committed %d and refused %d on the index, want exactly one of each", committed, unique)
	}
	if got := f.count(`SELECT count(*) FROM invoices.line_sources`); got != len(given) {
		t.Errorf("rows = %d, want the %d of the transaction that committed", got, len(given))
	}
	if got := f.count(`
		SELECT count(*) FROM invoices.line_sources
		WHERE amount = 188.43228125 AND quantity = 1.25 AND state = 'held'
		  AND (source_kind = 'expenses.entry') = (source_subkind IS NOT DISTINCT FROM 'expense')
		  AND (source_kind = 'time.entry') = (source_subkind IS NULL)`); got != len(given) {
		t.Errorf("rows with the exact amount, held, with the expense's subkind only = %d, want %d", got, len(given))
	}
}

// TestInvoicesWork_LineReleasesAndTimesheetRowsAreFrozenWithTheirDocument
// pins refuse_issued_child_change on the two new children through their
// invoice_id (D8, D5): written, changed and deleted freely under a draft,
// refused every write once their document is issued, and gone with a
// deleted draft; a source is released once.
func TestInvoicesWork_LineReleasesAndTimesheetRowsAreFrozenWithTheirDocument(t *testing.T) {
	f := newInvoicesWorkFixture(t)

	// An issued invoice whose two lines' sources are invoiced, and a
	// credit-note draft of it whose line credits the first.
	invoice := f.draft(0)
	original := f.line(invoice, 1, 0)
	secondLine := f.line(invoice, 2, 0)
	source := f.mustHold(original, invoice, "time.entry", 1)
	secondSource := f.mustHold(secondLine, invoice, "time.entry", 2)
	for _, id := range []int64{source, secondSource} {
		if err := f.exec(invoicesWorkSetState, id, "invoiced"); err != nil {
			t.Fatalf("mark invoiced: %v", err)
		}
	}
	f.issue(invoice)
	credit := f.draft(invoice)
	creditLine := f.line(credit, 1, original)

	const insertRelease = `INSERT INTO invoices.line_releases (invoice_id, credit_line_id, line_source_id) VALUES ($1, $2, $3) RETURNING id`
	var release int64
	if err := f.pool.QueryRow(f.ctx, insertRelease, credit, creditLine, source).Scan(&release); err != nil {
		t.Fatalf("a release under a credit-note draft: %v, want it allowed", err)
	}
	if err := f.exec(insertRelease, credit, creditLine, source); !invoicesWorkViolation(err, "23505", "ux_line_releases_source") {
		t.Errorf("a second release of one source: %v, want a unique violation on ux_line_releases_source", err)
	}
	if err := f.exec(`UPDATE invoices.line_releases SET credit_line_id = credit_line_id WHERE id = $1`, release); err != nil {
		t.Errorf("an update under a credit-note draft: %v, want it allowed", err)
	}
	if err := f.exec(`DELETE FROM invoices.line_releases WHERE id = $1`, release); err != nil {
		t.Errorf("a delete under a credit-note draft: %v, want it allowed", err)
	}
	if err := f.pool.QueryRow(f.ctx, insertRelease, credit, creditLine, source).Scan(&release); err != nil {
		t.Fatalf("the release again: %v", err)
	}
	f.issue(credit)
	if err := f.exec(invoicesWorkSetState, source, "released"); err != nil {
		t.Errorf("the original's source released beside the issued credit note: %v", err)
	}
	if err := f.exec(insertRelease, credit, creditLine, secondSource); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("an insert under an issued credit note: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(`UPDATE invoices.line_releases SET credit_line_id = credit_line_id WHERE id = $1`, release); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("an update under an issued credit note: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(`DELETE FROM invoices.line_releases WHERE id = $1`, release); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("a delete under an issued credit note: %v, want %q", err, invoicesWorkImmutable)
	}

	// A credit-note draft's release goes with its line (the cascade), and
	// with the draft.
	draftCredit := f.draft(invoice)
	draftCreditLine := f.line(draftCredit, 1, secondLine)
	if err := f.exec(insertRelease, draftCredit, draftCreditLine, secondSource); err != nil {
		t.Fatalf("a release under a second credit-note draft: %v", err)
	}
	if err := f.exec(`DELETE FROM invoices.lines WHERE id = $1`, draftCreditLine); err != nil {
		t.Errorf("deleting a credit-note draft's line: %v, want its release to go with it", err)
	}
	if got := f.count(`SELECT count(*) FROM invoices.line_releases WHERE invoice_id = $1`, draftCredit); got != 0 {
		t.Errorf("releases of a deleted line = %d, want none", got)
	}
	draftCreditLine = f.line(draftCredit, 1, secondLine)
	if err := f.exec(insertRelease, draftCredit, draftCreditLine, secondSource); err != nil {
		t.Fatalf("the release again: %v", err)
	}
	if err := f.exec(`DELETE FROM invoices.invoices WHERE id = $1`, draftCredit); err != nil {
		t.Errorf("deleting a credit-note draft with a release: %v, want the cascade allowed", err)
	}
	if got := f.count(`SELECT count(*) FROM invoices.line_releases WHERE line_source_id = $1`, secondSource); got != 0 {
		t.Errorf("releases of a deleted draft = %d, want none", got)
	}

	// The timesheet rows: positions from 1, hours above 0, one row per
	// position; free under a draft, frozen once issued, gone with the draft.
	const insertRow = `
		INSERT INTO invoices.timesheet_rows (invoice_id, position, source_id, person_label, entry_date, hours, work_type, description)
		VALUES ($1, $2, $3, 'KN', DATE '2026-10-01', $4, NULL, 'Utvikling') RETURNING id`
	sheet := f.draft(0)
	var row int64
	if err := f.pool.QueryRow(f.ctx, insertRow, sheet, 1, 100, "7.5").Scan(&row); err != nil {
		t.Fatalf("a timesheet row under a draft: %v, want it allowed", err)
	}
	for _, c := range []struct {
		position   int
		hours      string
		code, name string
	}{
		{0, "1", "23514", "ck_timesheet_rows_position"},
		{2, "0", "23514", "ck_timesheet_rows_hours"},
		{1, "1", "23505", "ux_timesheet_rows_invoice_position"},
	} {
		if err := f.exec(insertRow, sheet, c.position, 101, c.hours); !invoicesWorkViolation(err, c.code, c.name) {
			t.Errorf("a row at %d with %s hours: %v, want %s on %s", c.position, c.hours, err, c.code, c.name)
		}
	}
	if err := f.exec(`UPDATE invoices.timesheet_rows SET person_label = 'Person 1' WHERE id = $1`, row); err != nil {
		t.Errorf("an update under a draft: %v, want it allowed", err)
	}
	f.issue(sheet)
	if err := f.exec(insertRow, sheet, 2, 102, "1"); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("an insert under an issued document: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(`UPDATE invoices.timesheet_rows SET person_label = 'KN2' WHERE id = $1`, row); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("an update under an issued document: %v, want %q", err, invoicesWorkImmutable)
	}
	if err := f.exec(`DELETE FROM invoices.timesheet_rows WHERE id = $1`, row); !refusedWith(err, invoicesWorkImmutable) {
		t.Errorf("a delete under an issued document: %v, want %q", err, invoicesWorkImmutable)
	}
	gone := f.draft(0)
	if err := f.exec(insertRow, gone, 1, 103, "2"); err != nil {
		t.Fatalf("a row on another draft: %v", err)
	}
	if err := f.exec(`DELETE FROM invoices.invoices WHERE id = $1`, gone); err != nil {
		t.Errorf("deleting a draft with timesheet rows: %v, want the cascade allowed", err)
	}
	if got := f.count(`SELECT count(*) FROM invoices.timesheet_rows WHERE invoice_id = $1`, gone); got != 0 {
		t.Errorf("rows of a deleted draft = %d, want none", got)
	}
}

// TestInvoicesWork_ThePruneWithNothingKeptDeletesEveryRow pins
// PruneTimesheetRows as the store runs it (invoicesWorkQuery): the hours
// kept stay and the rest go, and a nil kept — which pgx sends as NULL, the
// natural caller's "no hours held" — deletes every row of the draft, never
// none, and never another draft's.
func TestInvoicesWork_ThePruneWithNothingKeptDeletesEveryRow(t *testing.T) {
	f := newInvoicesWorkFixture(t)
	prune, params := invoicesWorkQuery(t, "PruneTimesheetRows")
	if !slices.Equal(params, []string{"invoice_id", "kept"}) {
		t.Fatalf("PruneTimesheetRows takes %v, want [invoice_id kept]", params)
	}
	const insertRow = `
		INSERT INTO invoices.timesheet_rows (invoice_id, position, source_id, person_label, entry_date, hours, description)
		VALUES ($1, $2, $3, 'KN', DATE '2026-10-01', 1, 'Utvikling')`
	sheet, other := f.draft(0), f.draft(0)
	for i, source := range []int64{100, 101, 102} {
		if err := f.exec(insertRow, sheet, i+1, source); err != nil {
			t.Fatalf("seed row %d: %v", source, err)
		}
	}
	if err := f.exec(insertRow, other, 1, 100); err != nil {
		t.Fatalf("seed another draft's row: %v", err)
	}
	rows := func(invoiceID int64) int {
		return f.count(`SELECT count(*) FROM invoices.timesheet_rows WHERE invoice_id = $1`, invoiceID)
	}

	tag, err := f.pool.Exec(f.ctx, prune, sheet, []int64{101})
	if err != nil || tag.RowsAffected() != 2 || rows(sheet) != 1 {
		t.Errorf("keeping 101: %v, %d deleted, %d left, want 2 deleted and 101 left", err, tag.RowsAffected(), rows(sheet))
	}
	var none []int64
	tag, err = f.pool.Exec(f.ctx, prune, sheet, none)
	if err != nil || tag.RowsAffected() != 1 || rows(sheet) != 0 {
		t.Errorf("keeping nothing (nil): %v, %d deleted, %d left, want every row deleted", err, tag.RowsAffected(), rows(sheet))
	}
	if got := rows(other); got != 1 {
		t.Errorf("another draft's rows = %d, want its one untouched", got)
	}
}

// TestInvoicesWork_TheCompositeKeysRefuseAMismatchedDocument pins plan
// reading 7: a line source's and a release's invoice_id is their line's,
// through (line_id, invoice_id) referencing uq_lines_id_invoice.
func TestInvoicesWork_TheCompositeKeysRefuseAMismatchedDocument(t *testing.T) {
	f := newInvoicesWorkFixture(t)
	a := f.draft(0)
	lineA := f.line(a, 1, 0)
	b := f.draft(0)

	if _, err := f.hold(lineA, b, "time.entry", 1, "held"); !invoicesWorkViolation(err, "23503", "fk_line_sources_line") {
		t.Errorf("a source naming another document's line: %v, want a foreign-key violation on fk_line_sources_line", err)
	}
	source := f.mustHold(lineA, a, "time.entry", 1)
	if err := f.exec(`UPDATE invoices.line_sources SET state = 'invoiced' WHERE id = $1`, source); err != nil {
		t.Fatalf("mark invoiced: %v", err)
	}
	f.issue(a)

	credit := f.draft(a)
	creditLine := f.line(credit, 1, lineA)
	otherCredit := f.draft(a)
	if err := f.exec(`INSERT INTO invoices.line_releases (invoice_id, credit_line_id, line_source_id) VALUES ($1, $2, $3)`,
		otherCredit, creditLine, source); !invoicesWorkViolation(err, "23503", "fk_line_releases_line") {
		t.Errorf("a release naming another document's line: %v, want a foreign-key violation on fk_line_releases_line", err)
	}
	if err := f.exec(`INSERT INTO invoices.line_releases (invoice_id, credit_line_id, line_source_id) VALUES ($1, $2, 999999)`,
		credit, creditLine); !invoicesWorkViolation(err, "23503", "line_releases_line_source_id_fkey") {
		t.Errorf("a release of no source: %v, want a foreign-key violation on line_releases_line_source_id_fkey", err)
	}
	if err := f.exec(`INSERT INTO invoices.line_releases (invoice_id, credit_line_id, line_source_id) VALUES ($1, $2, $3)`,
		credit, creditLine, source); err != nil {
		t.Errorf("a release on its own line: %v, want it allowed", err)
	}
}

// TestInvoicesWork_TheQuantityCheck pins D7's relaxed ck_lines_quantity and
// the deduction's no-discount rule: a negative quantity only on a line that
// deducts an invoice, never zero, and a deduction carries no discount.
func TestInvoicesWork_TheQuantityCheck(t *testing.T) {
	f := newInvoicesWorkFixture(t)
	akonto := f.draft(0)
	f.line(akonto, 1, 0)
	f.issue(akonto)
	settlement := f.draft(0)

	const insertLine = `
		INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit_price, discount_percent, vat_code_id,
		    deducts_invoice_id, line_gross, line_allowance, line_net)
		VALUES ($1, $2, 'Tidligere fakturert a konto, faktura 1', $3, 1000, $4, 1, $5, -1000, 0, -1000)`
	for _, c := range []struct {
		name       string
		quantity   string
		discount   string
		deducts    *int64
		constraint string
	}{
		{"a negative quantity without a deducted invoice", "-1", "0", nil, "ck_lines_quantity"},
		{"a zero quantity on a deduction", "0", "0", &akonto, "ck_lines_quantity"},
		{"a zero quantity", "0", "0", nil, "ck_lines_quantity"},
		{"a deduction with a discount", "-1", "10", &akonto, "ck_lines_deduction_no_discount"},
	} {
		if err := f.exec(insertLine, settlement, 1, c.quantity, c.discount, c.deducts); !checkViolationOf(err, c.constraint) {
			t.Errorf("%s: %v, want a check violation from %s", c.name, err, c.constraint)
		}
	}
	// No foreign key (D7): its row lock would cycle with the merge's
	// newest-first lock, so the module's own checks keep the reference.
	if got := f.count(`SELECT count(*) FROM pg_constraint WHERE conrelid = 'invoices.lines'::regclass AND contype = 'f'
		AND conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = 'invoices.lines'::regclass AND attname = 'deducts_invoice_id')]`); got != 0 {
		t.Errorf("foreign keys on lines.deducts_invoice_id = %d, want none", got)
	}
	if err := f.exec(insertLine, settlement, 1, "-1", "0", &akonto); err != nil {
		t.Errorf("a deduction of quantity -1: %v, want it allowed", err)
	}
	if err := f.exec(insertLine, settlement, 2, "-0.5", "0", &akonto); err != nil {
		t.Errorf("a deduction of quantity -0.5: %v, want it allowed (a credit note's partial credit of one)", err)
	}
	if err := f.exec(insertLine, settlement, 3, "2", "10", nil); err != nil {
		t.Errorf("an ordinary line with a discount: %v, want it allowed", err)
	}
	if got := f.count(`SELECT count(*) FROM invoices.lines WHERE deducts_invoice_id = $1`, akonto); got != 2 {
		t.Errorf("lines deducting the a-konto = %d, want 2", got)
	}
}

// TestInvoicesWork_TheSettingsDefaults pins the work settings (D6, D5): each
// kind's VAT code defaults to id 1 (the seeded 25 %) and names a code that
// exists; the timesheet is off and its person label is initials, one of
// three.
func TestInvoicesWork_TheSettingsDefaults(t *testing.T) {
	f := newInvoicesWorkFixture(t)
	var hours, expenses, milestones int32
	var timesheet bool
	var label string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT work_vat_code_hours, work_vat_code_expenses, work_vat_code_milestones, timesheet_default, timesheet_person_label
		FROM invoices.settings WHERE id = 1`).Scan(&hours, &expenses, &milestones, &timesheet, &label); err != nil {
		t.Fatalf("read the settings: %v", err)
	}
	if hours != 1 || expenses != 1 || milestones != 1 || timesheet || label != "initials" {
		t.Errorf("settings = (%d, %d, %d, %t, %q), want (1, 1, 1, false, \"initials\")", hours, expenses, milestones, timesheet, label)
	}
	for _, good := range []string{"initials", "number", "name"} {
		if err := f.exec(`UPDATE invoices.settings SET timesheet_person_label = $1`, good); err != nil {
			t.Errorf("person label %q: %v, want it allowed", good, err)
		}
	}
	if err := f.exec(`UPDATE invoices.settings SET timesheet_person_label = 'email'`); !checkViolationOf(err, "ck_settings_timesheet_person_label") {
		t.Errorf("person label email: %v, want a check violation from ck_settings_timesheet_person_label", err)
	}
	for column, constraint := range map[string]string{
		"work_vat_code_hours":      "settings_work_vat_code_hours_fkey",
		"work_vat_code_expenses":   "settings_work_vat_code_expenses_fkey",
		"work_vat_code_milestones": "settings_work_vat_code_milestones_fkey",
	} {
		if err := f.exec(`UPDATE invoices.settings SET ` + column + ` = 999999`); !invoicesWorkViolation(err, "23503", constraint) {
			t.Errorf("%s naming no code: %v, want a foreign-key violation on %s", column, err, constraint)
		}
		if err := f.exec(`UPDATE invoices.settings SET ` + column + ` = 9`); err != nil {
			t.Errorf("%s = 9: %v, want it allowed", column, err)
		}
	}
}

// TestInvoicesWork_TheDocumentsProjectAndTimesheetFreezeAtIssue pins D9's
// pair and D5's flag on the document: the project's id and reference are
// set together or not at all, and both, with the flag, are frozen at issue
// by 00034's refuse_issued_document_change without touching it.
func TestInvoicesWork_TheDocumentsProjectAndTimesheetFreezeAtIssue(t *testing.T) {
	f := newInvoicesWorkFixture(t)
	draft := f.draft(0)
	for _, bad := range []string{
		`UPDATE invoices.invoices SET project_id = 42 WHERE id = $1`,
		`UPDATE invoices.invoices SET project_reference = 'P-42' WHERE id = $1`,
	} {
		if err := f.exec(bad, draft); !checkViolationOf(err, "ck_invoices_project") {
			t.Errorf("%s: %v, want a check violation from ck_invoices_project", bad, err)
		}
	}
	if err := f.exec(`UPDATE invoices.invoices SET project_id = 42, project_reference = 'P-42', timesheet = true WHERE id = $1`, draft); err != nil {
		t.Errorf("the project and the flag on a draft: %v, want it allowed", err)
	}
	f.issue(draft)
	for _, frozen := range []string{
		`UPDATE invoices.invoices SET project_id = NULL, project_reference = NULL WHERE id = $1`,
		`UPDATE invoices.invoices SET project_id = 43, project_reference = 'P-43' WHERE id = $1`,
		`UPDATE invoices.invoices SET timesheet = false WHERE id = $1`,
	} {
		if err := f.exec(frozen, draft); !refusedWith(err, invoicesWorkImmutable) {
			t.Errorf("%s on an issued document: %v, want %q", frozen, err, invoicesWorkImmutable)
		}
	}
}
