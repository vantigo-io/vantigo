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
	// Receivables on the ledger are what Invoices has open, less the
	// customers' credit, which is on receivables too (invoices phase 6 design
	// I16).
	expect("receivables less Invoices' open amount and credit", `
		SELECT ((SELECT coalesce(sum(p.amount), 0) FROM accounting.postings p
		         JOIN accounting.accounts a ON a.id = p.account_id WHERE a.system_role = 'receivables')
		      - (SELECT coalesce(sum(i.gross_total - invoices.settled_amount(i.id)), 0) FROM invoices.invoices i
		         WHERE i.kind = 'invoice' AND i.status = 'issued')
		      + (SELECT coalesce(sum(amount_nok), 0) FROM invoices.customer_credits))::text`, "0.00")
	expect("the demo customer's credit", `SELECT coalesce(sum(amount), 0)::text FROM invoices.customer_credits`, "300.00")
	// The small difference rounded off by itself, posted to 7790 as a
	// manual one is, and the bank-feed line split across two invoices
	// (invoices phase 6 design I14, I15).
	expect("the automatic rounding write-off", `SELECT string_agg(amount || ' ' || reason, ',') FROM invoices.write_offs
		WHERE source = 'auto' AND registered_by_user_id IS NULL`, "2.00 rounding")
	expect("the rounding posting", `SELECT coalesce(sum(p.amount), 0)::text FROM accounting.postings p
		JOIN accounting.accounts a ON a.id = p.account_id WHERE a.system_role = 'rounding'`, "2.00")
	expect("the split line's payments", `SELECT count(*)::text FROM invoices.payments p
		JOIN invoices.bank_transaction_events e ON e.bank_transaction_id = p.bank_transaction_id AND e.event = 'auto_split'`, "2")
	// Payables on the ledger are the supplier ledger, and something is owed.
	expect("payables less the supplier ledger", `
		SELECT ((SELECT coalesce(sum(p.amount), 0) FROM accounting.postings p
		         JOIN accounting.accounts a ON a.id = p.account_id WHERE a.system_role = 'payables')
		      - (SELECT coalesce(sum(p.amount), 0) FROM accounting.postings p
		         JOIN accounting.open_items oi ON oi.id = p.open_item_id WHERE oi.kind = 'supplier'))::text`, "0.00")
	// The books' own and the match demo's paint invoice, owed.
	expect("supplier invoices left owed", `
		SELECT count(*)::text FROM accounting.open_items oi WHERE oi.kind = 'supplier'
		AND (SELECT sum(amount) FROM accounting.postings WHERE open_item_id = oi.id) <> 0`, "3")
	expect("supplier invoices by state", `
		SELECT string_agg(state || '=' || n, ',' ORDER BY state) FROM (
			SELECT state, count(*) AS n FROM accounting.supplier_invoices GROUP BY state) s`, "draft=2,posted=15")
	// The EUR invoice paid in NOK leaves nothing open in NOK, with a gain
	// and a loss beside the supplier payments' two (invoices phase 6 design
	// I24).
	expect("the EUR invoice's NOK left open", `SELECT string_agg(invoices.open_nok(id)::text, ',') FROM invoices.invoices WHERE currency = 'EUR'`, "0.00")
	expect("a currency difference", `
		SELECT count(*)::text FROM accounting.postings p JOIN accounting.accounts a ON a.id = p.account_id
		WHERE a.system_role IN ('currency_gain', 'currency_loss')`, "4")
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

	// Purchasing (delivery A): the workshop's suppliers and items, and an
	// order in every state, the steel tube the workshop is low on on order.
	if !strings.Contains(stdout, "purchasing demo seeded") {
		t.Errorf("expected the purchasing demo's log line, got: %s", stdout)
	}
	expect("the purchase orders' states", `SELECT string_agg(status, ',' ORDER BY status) FROM purchasing.orders`,
		"cancelled,closed,draft,partially_received,received,sent")
	expect("the supplier items", `SELECT count(*)::text FROM purchasing.supplier_items`, "6")
	expect("the confirmed orders", `SELECT count(*)::text FROM purchasing.orders WHERE supplier_reference IS NOT NULL`, "1")
	expect("the closed lines", `SELECT count(*)::text FROM purchasing.order_lines WHERE closed_at IS NOT NULL`, "2")
	expect("the stored PDFs", `SELECT count(*)::text FROM purchasing.documents`, "5")
	expect("the tube on order at the workshop", `
		SELECT sum(l.quantity - l.received)::text FROM purchasing.order_lines l JOIN purchasing.orders o ON o.id = l.order_id
		JOIN products.product_variants v ON v.id = l.variant_id JOIN inventory.sites s ON s.id = o.site_id
		WHERE v.sku = 'VRK-ROR-40' AND s.code = 'VERK' AND o.status IN ('sent', 'partially_received') AND l.closed_at IS NULL`, "60.000")
	// Receipts (delivery B): the tube in part, the paint into a lot, the
	// plywood received and undone — each posted through Inventory's ledger.
	if !strings.Contains(stdout, "purchasing receipts demo seeded") {
		t.Errorf("expected the receipts demo's log line, got: %s", stdout)
	}
	expect("the receipts", `SELECT count(*) || '/' || count(undone_at) FROM purchasing.receipts`, "3/1")
	expect("the receipts' movements", `SELECT count(*)::text FROM inventory.movements WHERE source_module = 'purchasing' AND kind IN ('receipt', 'receipt_reversal')`, "4")
	expect("the paint's lot", `SELECT l.number || '/' || (l.expiry IS NOT NULL)::text FROM inventory.lots l
		JOIN purchasing.receipt_lines rl ON rl.lot_number = l.number`, "LAKK-7016-2611/true")
	// The invoice match (delivery D): the paint invoice registered from its
	// order and posted, the steel invoice typed in Accounting and matched
	// at a dearer metre, a draft.
	if !strings.Contains(stdout, "purchasing match demo seeded") {
		t.Errorf("expected the match demo's log line, got: %s", stdout)
	}
	expect("the matched invoices", `SELECT string_agg(si.supplier_number || '=' || si.state || '/' || m.state, ',' ORDER BY si.supplier_number)
		FROM purchasing.invoice_matches m JOIN accounting.supplier_invoices si ON si.id = m.supplier_invoice_id`,
		"JPC-F-88412=posted/posted,NS-F-30502=draft/provisional")
	expect("the paint's reference", `SELECT (si.reference = o.number::text)::text FROM accounting.supplier_invoices si
		JOIN purchasing.invoice_registrations r ON r.supplier_invoice_id = si.id JOIN purchasing.orders o ON o.id = r.order_id`, "true")
	expect("the invoiced quantities", `SELECT string_agg(l.sku || '=' || l.invoiced, ',' ORDER BY l.sku) FROM purchasing.order_lines l WHERE l.invoiced > 0`,
		"VRK-LAKK-7016=40.000,VRK-ROR-40=120.000")

	counts := func() string {
		return value(`SELECT (SELECT count(*) FROM accounting.vouchers) || '/' || (SELECT count(*) FROM accounting.postings)
			|| '/' || (SELECT count(*) FROM accounting.statement_lines) || '/' || (SELECT count(*) FROM accounting.suppliers)
			|| '/' || (SELECT count(*) FROM purchasing.orders) || '/' || (SELECT count(*) FROM purchasing.supplier_items)
			|| '/' || (SELECT count(*) FROM purchasing.receipts) || '/' || (SELECT count(*) FROM purchasing.invoice_matches)
			|| '/' || (SELECT count(*) FROM accounting.supplier_invoices)`)
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

// The purchasing demo judges each part on its own: a seed without an object
// store makes the suppliers, the items and the draft, and a later seed with
// one adds the sent, confirmed, closed and cancelled orders, reusing the
// suppliers and the items.
func TestRun_SeedPurchasingAddsTheSentOrdersOnALaterRun(t *testing.T) {
	databaseURL := testdb.URL(t)
	setEnv(t, "APP_ENV", "development", "DATABASE_URL", databaseURL, "APP_URL", "http://localhost:8080")
	was := seedClock
	seedClock = func() time.Time { return time.Date(2026, time.November, 14, 9, 30, 0, 0, time.UTC) }
	t.Cleanup(func() { seedClock = was })
	if code, stdout, stderr := runCapture("seed"); code != 0 {
		t.Fatalf("seed without a store failed with exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	value := func(query string) string {
		t.Helper()
		var out string
		if err := conn.QueryRow(ctx, query).Scan(&out); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return out
	}
	states := `SELECT coalesce(string_agg(status, ',' ORDER BY status), '') FROM purchasing.orders`
	parts := `SELECT (SELECT count(*) FROM accounting.suppliers WHERE name IN ('Norsk Stål AS', 'Jotun Powder Coatings AS', 'Verktøygrossisten AS'))
		|| '/' || (SELECT count(*) FROM purchasing.supplier_items)`
	if got := value(states); got != "draft" {
		t.Fatalf("without a store the orders are %q, want the draft only", got)
	}
	if got := value(parts); got != "3/6" {
		t.Fatalf("without a store suppliers/items = %s, want 3/6", got)
	}
	setEnv(t, "APP_ENV", "development", "DATABASE_URL", databaseURL, "APP_URL", "http://localhost:8080",
		"STORAGE_PROVIDER", "fs", "STORAGE_FS_ROOT", t.TempDir())
	if code, stdout, stderr := runCapture("seed"); code != 0 {
		t.Fatalf("seed with a store failed with exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if got := value(states); got != "cancelled,closed,draft,partially_received,received,sent" {
		t.Errorf("with a store the orders are %q", got)
	}
	if got := value(parts); got != "3/6" {
		t.Errorf("a rerun made suppliers/items %s, want 3/6", got)
	}
	if got := value(`SELECT count(*)::text FROM purchasing.documents`); got != "5" {
		t.Errorf("stored PDFs = %s, want 5", got)
	}
}
