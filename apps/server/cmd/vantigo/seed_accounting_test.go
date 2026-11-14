package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func TestOnePagePDF_IsAPDF(t *testing.T) {
	pdf := onePagePDF([]string{"Kvadratur Eiendom AS", "Å betale: NOK 45 000,00 (husleie)"})
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatalf("not a PDF: %q", pdf)
	}
	if !bytes.Contains(pdf, []byte("\\(husleie\\)")) {
		t.Errorf("parentheses are not escaped: %q", pdf)
	}
}

// TestRun_SeedBooksTheDemo runs the seed with an object store on a fixed day
// and holds the books it writes to what the demo promises.
func TestRun_SeedBooksTheDemo(t *testing.T) {
	databaseURL := testdb.URL(t)
	setEnv(t,
		"APP_ENV", "development",
		"DATABASE_URL", databaseURL,
		"APP_URL", "http://localhost:8080",
		"STORAGE_PROVIDER", "fs",
		"STORAGE_FS_ROOT", t.TempDir(),
	)
	was := seedClock
	seedClock = func() time.Time { return time.Date(2026, time.November, 14, 9, 30, 0, 0, time.UTC) }
	t.Cleanup(func() { seedClock = was })

	code, stdout, stderr := runCapture("seed")
	if code != 0 {
		t.Fatalf("seed failed with exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "accounting demo books applied") {
		t.Fatalf("expected the books' log line, got: %s", stdout)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	value := func(query string, args ...any) string {
		t.Helper()
		var out string
		if err := conn.QueryRow(ctx, query, args...).Scan(&out); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return out
	}
	expect := func(what, query, want string) {
		t.Helper()
		if got := value(query); got != want {
			t.Errorf("%s = %s, want %s", what, got, want)
		}
	}

	expect("the books' start", `SELECT books_start::text FROM accounting.settings`, "2026-01-01")
	// The workshop's seed ran in the same seed as the books.
	expect("the workshop's manufacturing orders", `SELECT count(*)::text FROM manufacturing.orders`, "6")
	expect("the trial balance", `SELECT coalesce(sum(amount), 0)::text FROM accounting.postings`, "0.00")
	// Receivables on the ledger are what Invoices has open.
	expect("receivables less Invoices' open amount", `
		SELECT ((SELECT coalesce(sum(p.amount), 0) FROM accounting.postings p
		         JOIN accounting.accounts a ON a.id = p.account_id WHERE a.system_role = 'receivables')
		      - (SELECT coalesce(sum(i.gross_total - invoices.settled_amount(i.id)), 0) FROM invoices.invoices i
		         WHERE i.kind = 'invoice' AND i.status = 'issued'))::text`, "0.00")
	// Payables on the ledger are the supplier ledger, and something is owed.
	expect("payables less the supplier ledger", `
		SELECT ((SELECT coalesce(sum(p.amount), 0) FROM accounting.postings p
		         JOIN accounting.accounts a ON a.id = p.account_id WHERE a.system_role = 'payables')
		      - (SELECT coalesce(sum(p.amount), 0) FROM accounting.postings p
		         JOIN accounting.open_items oi ON oi.id = p.open_item_id WHERE oi.kind = 'supplier'))::text`, "0.00")
	expect("supplier invoices left owed", `
		SELECT count(*)::text FROM accounting.open_items oi WHERE oi.kind = 'supplier'
		AND (SELECT sum(amount) FROM accounting.postings WHERE open_item_id = oi.id) <> 0`, "2")
	expect("supplier invoices by state", `
		SELECT string_agg(state || '=' || n, ',' ORDER BY state) FROM (
			SELECT state, count(*) AS n FROM accounting.supplier_invoices GROUP BY state) s`, "draft=1,posted=14")
	expect("a currency difference", `
		SELECT count(*)::text FROM accounting.postings p JOIN accounting.accounts a ON a.id = p.account_id
		WHERE a.system_role IN ('currency_gain', 'currency_loss')`, "2")
	// Terms 1–5 of 2026 have ended: closed, all but the fifth filed, the
	// lock at the fifth's end.
	expect("VAT terms closed", `SELECT count(*)::text FROM accounting.vat_terms WHERE state = 'closed'`, "5")
	expect("VAT terms filed", `SELECT count(*)::text FROM accounting.vat_terms WHERE filed_at IS NOT NULL`, "4")
	expect("a filed term's stored closing", `
		SELECT count(*)::text FROM accounting.vat_terms t JOIN accounting.vat_term_closings c ON c.id = t.filed_closing_id
		WHERE c.figures IS NOT NULL AND c.return_xml <> ''`, "4")
	expect("the lock", `SELECT locked_through::text FROM accounting.settings`, "2026-10-31")
	// The open term has activity.
	expect("vouchers in the open term", `
		SELECT (count(*) > 0)::text FROM accounting.vouchers WHERE voucher_date >= '2026-11-01'`, "true")
	// The statement: both matched and open lines, the VAT and a bank voucher.
	expect("matched lines", `SELECT (count(*) > 0)::text FROM accounting.statement_lines WHERE state = 'matched'`, "true")
	expect("open lines", `SELECT (count(*) > 1)::text FROM accounting.statement_lines WHERE state = 'open'`, "true")
	expect("the VAT payment matched", `
		SELECT count(*)::text FROM accounting.statement_lines WHERE state = 'matched' AND text LIKE 'Skatteetaten%'`, "1")
	// Every open line but the card purchase has a suggestion to try.
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := conn.Query(ctx, `SELECT id, kind FROM accounting.statement_lines WHERE state = 'open' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type openLine struct {
		ID   int64
		Kind string
	}
	open, err := pgx.CollectRows(rows, pgx.RowToStructByPos[openLine])
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range open {
		keys, err := accounting.BankSuggestionKeys(ctx, module.Deps{Pool: pool}, l.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (len(keys) == 0) != (l.Kind == "card") {
			t.Errorf("open line %d (%s) has %d suggestions", l.ID, l.Kind, len(keys))
		}
	}
	// Needs attention holds the one expense in euros, waiting for its NOK.
	expect("needs attention", `
		SELECT string_agg(reason, ',') FROM accounting.unposted
		WHERE posted_at IS NULL AND resolved_at IS NULL AND NOT informational`, "amount_in_nok_needed")
	// Every approved expense posted, and every reimbursement.
	expect("expense vouchers", `SELECT string_agg(kind || '=' || n, ',' ORDER BY kind) FROM (
		SELECT kind, count(*) AS n FROM accounting.vouchers WHERE kind IN ('expense', 'reimbursement') GROUP BY kind) k`,
		"expense=24,reimbursement=10")
	// The write-off posted with its VAT correction, and its recovery.
	expect("the loss's VAT taken back", `
		SELECT (count(*) > 0)::text FROM accounting.postings WHERE tax_specification = 'tapPåKrav'`, "true")

	counts := func() string {
		return value(`SELECT (SELECT count(*) FROM accounting.vouchers) || '/' || (SELECT count(*) FROM accounting.postings)
			|| '/' || (SELECT count(*) FROM accounting.statement_lines) || '/' || (SELECT count(*) FROM accounting.suppliers)`)
	}
	before := counts()
	code2, stdout2, stderr2 := runCapture("seed")
	if code2 != 0 {
		t.Fatalf("second seed failed with exit %d\nstdout: %s\nstderr: %s", code2, stdout2, stderr2)
	}
	if after := counts(); after != before {
		t.Errorf("a second seed changed the books: %s, then %s", before, after)
	}
}
