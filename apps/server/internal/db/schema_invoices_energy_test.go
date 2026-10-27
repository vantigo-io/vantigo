package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// invoicesEnergyVersion is 00078_invoices_energy.sql (energy consumption
// billing design D7-D9): a settlement as a line's source, correction lines,
// eight-decimal unit prices, the energy VAT codes and the north's exemption.
const invoicesEnergyVersion = 78

// TestInvoicesEnergy_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins what the wizard rests on: a
// settlement held with no project, a subkind and its snapshot, over several
// lines, while a work kind keeps its project, its single line and no
// snapshot; the kind held to a shape; a correction line negative, an
// ordinary one not; a unit price of eight decimals; the settings' two codes
// and the seeded code 10.
func TestInvoicesEnergy_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, invoicesEnergyVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	f := &invoicesWorkFixture{t: t, ctx: ctx, pool: pool, url: url, next: 1}

	var energy, exempt int32
	if err := pool.QueryRow(ctx, `SELECT energy_vat_code, energy_vat_code_exempt FROM invoices.settings`).Scan(&energy, &exempt); err != nil {
		t.Fatalf("read the settings: %v", err)
	}
	if energy != 1 || exempt != 10 {
		t.Errorf("energy VAT codes = %d, %d; want 1 and 10", energy, exempt)
	}
	var category, reason string
	if err := pool.QueryRow(ctx, `
		SELECT c.ehf_category || ' ' || r.rate_percent::text, c.exemption_reason
		FROM invoices.vat_codes c JOIN invoices.vat_code_rates r ON r.vat_code_id = c.id
		WHERE c.id = 10 AND c.code = '5N'`).Scan(&category, &reason); err != nil {
		t.Fatalf("read code 10: %v", err)
	}
	if category != "Z 0.00" || reason != "Elektrisk kraft til husholdningsbruk i Nord-Norge, mval. § 6-6" {
		t.Errorf("code 10 = %q %q, want Z 0.00 with the § 6-6 reason", category, reason)
	}

	doc := f.draft(0)
	first, second := f.line(doc, 1, 0), f.line(doc, 2, 0)
	const settlement = `
		INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
		    project_id, quantity, amount, currency, source_date, line_count,
		    point_gsrn, point_address, price_area, contract_name, period_from, estimated)
		VALUES ($1, $2, $3, $4, 1, $5, $6, $7, 1250.40, 'NOK', DATE '2026-09-30', $8,
		    $9::varchar, CASE WHEN $9 IS NOT NULL THEN 'Storgata 1, 9008 Tromsø' END, CASE WHEN $9 IS NOT NULL THEN 'NO4' END,
		    CASE WHEN $9 IS NOT NULL THEN 'Spot' END, CASE WHEN $9 IS NOT NULL THEN DATE '2026-09-01' END,
		    CASE WHEN $9 IS NOT NULL THEN false END)`
	gsrn := "707057500012345678"
	if err := f.exec(settlement, first, doc, "energy.settlement", 501, "regular", nil, "1500.250", 2, gsrn); err != nil {
		t.Errorf("a settlement over two lines: %v, want it held", err)
	}
	if err := f.exec(settlement, second, doc, "energy.settlement", 502, "correction", nil, "-12.5", 1, gsrn); err != nil {
		t.Errorf("a correction of negative kWh: %v, want it held", err)
	}
	for _, c := range []struct {
		name, kind string
		subkind    any
		project    any
		quantity   string
		count      int
		gsrn       any
		constraint string
	}{
		{"a kind out of shape", "Energy.Settlement", "regular", nil, "1", 1, gsrn, "ck_line_sources_kind"},
		{"a settlement with a project", "energy.settlement", "regular", 42, "1", 1, nil, "ck_line_sources_project"},
		{"a work kind without one", "time.entry", "regular", nil, "1", 1, gsrn, "ck_line_sources_project"},
		{"a settlement without a subkind", "energy.settlement", nil, nil, "1", 1, gsrn, "ck_line_sources_subkind"},
		{"a settlement without its point", "energy.settlement", "regular", nil, "1", 1, nil, "ck_line_sources_point"},
		{"work with a point", "time.entry", nil, 42, "1", 1, gsrn, "ck_line_sources_point"},
		{"work over two lines", "time.entry", nil, 42, "1", 2, nil, "ck_line_sources_line_count"},
		{"work of no quantity", "time.entry", nil, 42, "0", 1, nil, "ck_line_sources_quantity"},
		{"a settlement over no line", "energy.settlement", "regular", nil, "1", 0, gsrn, "ck_line_sources_line_count"},
	} {
		if err := f.exec(settlement, first, doc, c.kind, 900, c.subkind, c.project, c.quantity, c.count, c.gsrn); !checkViolationOf(err, c.constraint) {
			t.Errorf("%s: %v, want a check violation from %s", c.name, err, c.constraint)
		}
	}
	if _, err := f.hold(first, doc, "time.entry", 77, "held"); err != nil {
		t.Errorf("an hour as before: %v, want it held", err)
	}

	const line = `
		INSERT INTO invoices.lines (invoice_id, position, description, quantity, unit, unit_price, discount_percent, vat_code_id,
		    correction, line_gross, line_allowance, line_net)
		VALUES ($1, $2, 'Korreksjon Spot NO4', $3, 'stk', $4, $5, 10, $6, -12.34, 0, -12.34)`
	if err := f.exec(line, doc, 3, "-1", "12.34", "0", true); err != nil {
		t.Errorf("a negative correction line: %v, want it allowed", err)
	}
	if err := f.exec(line, doc, 4, "-1", "12.34", "0", false); !checkViolationOf(err, "ck_lines_quantity") {
		t.Errorf("a negative ordinary line: %v, want ck_lines_quantity", err)
	}
	if err := f.exec(line, doc, 4, "-1", "12.34", "5", true); !checkViolationOf(err, "ck_lines_correction") {
		t.Errorf("a correction with a discount: %v, want ck_lines_correction", err)
	}
	if err := f.exec(line, doc, 5, "1500.25", "0.83345678", "0", false); err != nil {
		t.Fatalf("an eight-decimal price: %v", err)
	}
	var price string
	if err := pool.QueryRow(ctx, `SELECT unit_price::text FROM invoices.lines WHERE invoice_id = $1 AND position = 5`, doc).Scan(&price); err != nil {
		t.Fatalf("read the price: %v", err)
	}
	if price != "0.83345678" {
		t.Errorf("unit price = %s, want 0.83345678 as written", price)
	}

	// The Down refuses while a settlement is a source or a price has more than
	// four decimals; with neither, it restores 00040's shape.
	if err := f.exec(`DELETE FROM invoices.invoices WHERE id = $1`, doc); err != nil {
		t.Fatalf("delete the draft: %v", err)
	}
	migrateTo(t, url, invoicesEnergyVersion-1)
	var nullable string
	if err := pool.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = 'invoices' AND table_name = 'line_sources' AND column_name = 'project_id'`).Scan(&nullable); err != nil {
		t.Fatalf("read project_id: %v", err)
	}
	if nullable != "NO" {
		t.Errorf("after the Down, line_sources.project_id nullable = %s, want NO", nullable)
	}
	if n := f.count(`SELECT count(*) FROM invoices.vat_codes WHERE id = 10`); n != 0 {
		t.Errorf("after the Down, code 10 rows = %d, want 0", n)
	}
	migrateTo(t, url, invoicesEnergyVersion)
}
