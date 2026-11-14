package accounting

// The ledger's rules held by the database and the engine, by tests that try
// to break them (accounting design §10): an unbalanced or empty voucher, an
// update or a delete, a post into a locked period, a duplicate source, a
// failed post's number, and a lock move racing a post (R10).

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// ledger is a set-up ledger on its own database: books from 2026-01-01, the
// default chart.
type ledger struct {
	pool  *pgxpool.Pool
	chart map[string]int32
}

func newLedger(t *testing.T) *ledger {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.InsertSettings(ctx, store.InsertSettingsParams{BooksStart: start, FiscalYearStartMonth: 1, VatTerm: "bimonthly",
			ChartLanguage: "en", Now: time.Now(), UserID: uuid.New()}); err != nil {
			return err
		}
		return seedBooks(ctx, q, "en", start, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	l := &ledger{pool: pool, chart: map[string]int32{}}
	rows, err := store.New(pool).ListAccounts(ctx, store.ListAccountsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		l.chart[r.Number] = r.ID
	}
	return l
}

func d(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func rat(s string) *big.Rat {
	r, _ := new(big.Rat).SetString(s)
	return r
}

// simple is a two-line voucher of amount, debit then credit.
func (l *ledger) simple(date, amount string, source *Source) Voucher {
	by := uuid.New()
	v := Voucher{Kind: "manual", Date: d(date), Description: "Test", Source: source,
		Lines: []Line{{AccountID: l.chart["6300"], Amount: rat(amount)}, {AccountID: l.chart["1920"], Amount: neg(rat(amount))}}}
	if source == nil {
		v.PostedBy = &by
	} else {
		v.Kind = "sales_invoice"
	}
	return v
}

func (l *ledger) post(t *testing.T, v Voucher) (Posted, error) {
	t.Helper()
	var p Posted
	err := db.WithTx(context.Background(), l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		p, err = Post(context.Background(), tx, v)
		return err
	})
	return p, err
}

func codeOf(err error) string {
	var r *refusal
	if errors.As(err, &r) && r.problem.Code != nil {
		return *r.problem.Code
	}
	return ""
}

// rawVoucher inserts a voucher row as SQL would, around the engine.
func rawVoucher(ctx context.Context, tx pgx.Tx, number int, date string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO accounting.vouchers (fiscal_year, number, voucher_date, document_date, description, kind, posted_at)
		VALUES (2026, $1, $2, $2, 'raw', 'manual', now()) RETURNING id`, number, d(date)).Scan(&id)
	return id, err
}

func rawPosting(ctx context.Context, tx pgx.Tx, voucher int64, line int, account int32, amount string) error {
	_, err := tx.Exec(ctx, `INSERT INTO accounting.postings (voucher_id, line_no, account_id, amount) VALUES ($1, $2, $3, $4::numeric)`,
		voucher, line, account, amount)
	return err
}

func TestDatabase_RefusesAnUnbalancedVoucherAtCommit(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	err := db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		id, err := rawVoucher(ctx, tx, 900, "2026-02-01")
		if err != nil {
			return err
		}
		if err := rawPosting(ctx, tx, id, 1, l.chart["6300"], "100"); err != nil {
			return err
		}
		return rawPosting(ctx, tx, id, 2, l.chart["1920"], "-99.99")
	})
	if err == nil || !strings.Contains(err.Error(), "not zero") {
		t.Fatalf("an unbalanced voucher committed: %v", err)
	}
	err = db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := rawVoucher(ctx, tx, 901, "2026-02-01")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "no postings") {
		t.Fatalf("an empty voucher committed: %v", err)
	}
	err = db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		id, err := rawVoucher(ctx, tx, 902, "2026-02-01")
		if err != nil {
			return err
		}
		return rawPosting(ctx, tx, id, 1, l.chart["6300"], "0")
	})
	if err == nil {
		t.Fatal("a zero posting was stored")
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.vouchers"); n != 0 {
		t.Fatalf("%d vouchers left behind", n)
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDatabase_NeverUpdatesOrDeletesAVoucher(t *testing.T) {
	l := newLedger(t)
	p, err := l.post(t, l.simple("2026-02-01", "100", nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"UPDATE accounting.vouchers SET description = 'changed' WHERE id = $1",
		"DELETE FROM accounting.vouchers WHERE id = $1",
		"UPDATE accounting.postings SET amount = 99 WHERE voucher_id = $1",
		"DELETE FROM accounting.postings WHERE voucher_id = $1",
	} {
		if _, err := l.pool.Exec(context.Background(), sql, p.ID); err == nil || !strings.Contains(err.Error(), "never changed") {
			t.Errorf("%s: %v", sql, err)
		}
	}
}

func TestDatabase_RefusesAPostingIntoALockedPeriod(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	if _, err := l.pool.Exec(ctx, "UPDATE accounting.settings SET locked_through = '2026-03-31'"); err != nil {
		t.Fatal(err)
	}
	err := db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := rawVoucher(ctx, tx, 900, "2026-03-31")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("a raw voucher in the locked period: %v", err)
	}
	err = db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := rawVoucher(ctx, tx, 901, "2025-12-31")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "before the books start") {
		t.Fatalf("a raw voucher before the books: %v", err)
	}
	// The engine refuses a person's voucher and moves a system one.
	if _, err := l.post(t, l.simple("2026-03-15", "1", nil)); codeOf(err) != codePeriodLocked {
		t.Errorf("manual into the lock: %v", err)
	}
	p, err := l.post(t, l.simple("2026-03-15", "1", &Source{Module: "invoices", Kind: "document", ID: 1}))
	if err != nil || !p.Moved || !p.VoucherDate.Equal(d("2026-04-01")) {
		t.Errorf("system into the lock = %+v, %v", p, err)
	}
	p, err = l.post(t, l.simple("2025-06-01", "1", &Source{Module: "invoices", Kind: "payment", ID: 2}))
	if err != nil || !p.VoucherDate.Equal(d("2026-04-01")) {
		t.Errorf("system before the books, after the lock = %+v, %v", p, err)
	}
}

func TestPost_ASourceIsPostedOnce(t *testing.T) {
	l := newLedger(t)
	src := &Source{Module: "invoices", Kind: "document", ID: 42}
	first, err := l.post(t, l.simple("2026-02-01", "100", src))
	if err != nil || first.Repeat {
		t.Fatalf("first = %+v %v", first, err)
	}
	again, err := l.post(t, l.simple("2026-02-02", "999", src))
	if err != nil || !again.Repeat || again.ID != first.ID {
		t.Fatalf("again = %+v %v", again, err)
	}
	other, err := l.post(t, l.simple("2026-02-01", "100", &Source{Module: "invoices", Kind: "document", ID: 42, Part: "2"}))
	if err != nil || other.ID == first.ID {
		t.Fatalf("another part = %+v %v", other, err)
	}
	// Around the engine, the index refuses the duplicate.
	ctx := context.Background()
	_, err = l.pool.Exec(ctx, `INSERT INTO accounting.vouchers (fiscal_year, number, voucher_date, document_date, description, kind,
		source_module, source_kind, source_id, posted_at) VALUES (2026, 999, '2026-02-01', '2026-02-01', 'dup', 'sales_invoice',
		'invoices', 'document', 42, now())`)
	if err == nil || !strings.Contains(err.Error(), "ux_vouchers_source") {
		t.Fatalf("a duplicate source: %v", err)
	}
}

// A failed post gives its number back: the counter row rolls back with it.
func TestPost_NumbersAreGapFreeUnderAFailedPost(t *testing.T) {
	l := newLedger(t)
	failed := errors.New("the caller failed after the post")
	err := db.WithTx(context.Background(), l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		p, err := Post(context.Background(), tx, l.simple("2026-02-01", "100", nil))
		if err != nil {
			return err
		}
		if p.Number != 1 {
			t.Errorf("first number %d", p.Number)
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if _, err := l.post(t, l.simple("2026-02-01", "100.001", nil)); err == nil {
		t.Fatal("an amount the column cannot hold balanced")
	}
	p, err := l.post(t, l.simple("2026-02-01", "100", nil))
	if err != nil || p.Number != 1 || p.FiscalYear != 2026 {
		t.Fatalf("after the failures = %+v %v", p, err)
	}
	p, err = l.post(t, l.simple("2027-01-05", "100", nil))
	if err != nil || p.Number != 1 || p.FiscalYear != 2027 {
		t.Fatalf("a new year's series = %+v %v", p, err)
	}
}

// Zero lines are left out (R3), and a voucher of nothing but zeros posts
// nothing; a control line with no item lands on the party's unallocated item.
func TestPost_ZeroLinesAndUnallocatedItems(t *testing.T) {
	l := newLedger(t)
	cust := int32(7)
	v := Voucher{Kind: "sales_invoice", Date: d("2026-02-01"), Description: "Free line", Source: &Source{Module: "invoices", Kind: "document", ID: 5},
		Lines: []Line{
			{Role: "receivables", Amount: rat("125"), CustomerID: &cust},
			{AccountID: l.chart["3000"], Amount: rat("-100")},
			{AccountID: l.chart["3100"], Amount: rat("0")},
			{AccountID: l.chart["2700"], Amount: rat("-25")},
		}}
	p, err := l.post(t, v)
	if err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.postings WHERE voucher_id = $1", p.ID); n != 3 {
		t.Errorf("%d postings, want the zero line left out", n)
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.open_items WHERE unallocated AND customer_id = 7"); n != 1 {
		t.Errorf("%d unallocated items", n)
	}
	v.Source.ID = 6
	if _, err := l.post(t, v); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.open_items WHERE customer_id = 7"); n != 1 {
		t.Errorf("%d items for the customer, want the one unallocated", n)
	}
	zero := Voucher{Kind: "sales_invoice", Date: d("2026-02-01"), Description: "Nothing", Source: &Source{Module: "invoices", Kind: "document", ID: 9},
		Lines: []Line{{AccountID: l.chart["3000"], Amount: rat("0")}, {Role: "receivables", Amount: rat("0"), CustomerID: &cust}}}
	if _, err := l.post(t, zero); codeOf(err) != codeVoucherEmpty {
		t.Errorf("an all-zero voucher: %v", err)
	}
	// A new item for a source document, found again by the next posting.
	doc := int64(1234)
	sale := Voucher{Kind: "sales_invoice", Date: d("2026-02-02"), Description: "Invoice 1234", Source: &Source{Module: "invoices", Kind: "document", ID: doc},
		Lines: []Line{{Role: "receivables", Amount: rat("50"), CustomerID: &cust, SourceDocumentID: &doc, Item: &OpenItemSpec{DocumentNumber: "1234"}},
			{AccountID: l.chart["3000"], Amount: rat("-50")}}}
	if _, err := l.post(t, sale); err != nil {
		t.Fatal(err)
	}
	pay := Voucher{Kind: "customer_payment", Date: d("2026-02-03"), Description: "Paid", Source: &Source{Module: "invoices", Kind: "payment", ID: 1},
		Lines: []Line{{Role: "bank", Amount: rat("50")}, {Role: "receivables", Amount: rat("-50"), CustomerID: &cust, SourceDocumentID: &doc}}}
	if _, err := l.post(t, pay); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, l.pool, `SELECT count(*) FROM accounting.open_items o WHERE source_document_id = 1234
		AND (SELECT sum(amount) FROM accounting.postings WHERE open_item_id = o.id) = 0`); n != 1 {
		t.Errorf("the document's item is not settled by its payment")
	}
}

// A lock move waits for a post in flight, and a post waits for a lock move
// (R10): the post that waited sees the new lock and is refused.
func TestLock_AMoveAndAPostDoNotPassEachOther(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	lockTx, err := l.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockTx.Rollback(ctx) }()
	if _, err := store.New(lockTx).GetSettingsForUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, "UPDATE accounting.settings SET locked_through = '2026-02-28'"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.post(t, l.simple("2026-02-15", "10", nil))
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the post did not wait for the lock move: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; codeOf(err) != codePeriodLocked {
		t.Fatalf("the waiting post = %v, want period_locked", err)
	}

	// And the other way: a post holds the settings FOR SHARE until it commits.
	postTx, err := l.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = postTx.Rollback(ctx) }()
	if _, err := Post(ctx, postTx, l.simple("2026-03-15", "10", nil)); err != nil {
		t.Fatal(err)
	}
	moved := make(chan error, 1)
	go func() {
		moved <- db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := store.New(tx).GetSettingsForUpdate(ctx); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "UPDATE accounting.settings SET locked_through = '2026-03-31'")
			return err
		})
	}()
	select {
	case err := <-moved:
		t.Fatalf("the lock moved past a post in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := postTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-moved; err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.vouchers WHERE voucher_date = '2026-03-15'"); n != 1 {
		t.Errorf("the post in flight is lost")
	}
}

// The contract's AccountingProblem names every refusal code.
func TestProblemDescription_NamesEveryCode(t *testing.T) {
	doc, err := openapi.Load(context.Background(), moduleName)
	if err != nil {
		t.Fatal(err)
	}
	desc := doc.Components.Schemas["AccountingProblem"].Value.Description
	for _, code := range refusalCodes {
		if !strings.Contains(desc, code+" (") {
			t.Errorf("AccountingProblem does not name %s", code)
		}
	}
}

// The default chart names only codes of the list, and every role once.
func TestDefaultChart_IsTheListsAndHoldsEveryRole(t *testing.T) {
	list, err := groupingsFor(2026)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{}
	numbers := map[string]bool{}
	for _, a := range defaultChart {
		code := a.code
		if code == "" {
			code = a.number
		}
		if _, ok := list.byCode[code]; !ok {
			t.Errorf("%s names %s, not in the list", a.number, code)
		}
		if (a.nb == "") != (a.code == "") {
			t.Errorf("%s: an account of its own needs both names and a code, and only it", a.number)
		}
		if numbers[a.number] {
			t.Errorf("%s twice", a.number)
		}
		numbers[a.number] = true
		if a.role != "" {
			roles[a.role]++
		}
	}
	for _, r := range systemRoles {
		if roles[r] != 1 {
			t.Errorf("role %s held %d times", r, roles[r])
		}
	}
	for _, c := range standardTaxCodes {
		for _, n := range []string{c.vatAccount, c.counterAccount} {
			if n != "" && !numbers[n] {
				t.Errorf("tax code %s posts to %s, not in the chart", c.code, n)
			}
		}
	}
}

// taxOf rounds half away from zero, both ways.
func TestTaxOf(t *testing.T) {
	for _, tc := range []struct {
		amount, rate string
		gross        bool
		base, tax    string
	}{
		{"0.02", "25", false, "0.02", "0.01"},
		{"-0.02", "25", false, "-0.02", "-0.01"},
		{"1000", "25", true, "800.00", "200.00"},
		{"-99.99", "15", true, "-86.95", "-13.04"},
		{"100", "11.11", false, "100.00", "11.11"},
	} {
		base, tax := taxOf(rat(tc.amount), rat(tc.rate), tc.gross)
		if money(base) != tc.base || money(tax) != tc.tax {
			t.Errorf("taxOf(%s, %s, %v) = %s, %s; want %s, %s", tc.amount, tc.rate, tc.gross, money(base), money(tax), tc.base, tc.tax)
		}
	}
}

// A posting is added only to the voucher its own transaction has just
// inserted (H2): never to a committed one, in the lock or out of it, and the
// rule holds when the voucher was inserted inside a savepoint — released, it
// counts; rolled back, the voucher before it is the open one again.
func TestDatabase_PostingsJoinOnlyTheVoucherJustInserted(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	p, err := l.post(t, l.simple("2026-02-01", "100", nil))
	if err != nil {
		t.Fatal(err)
	}
	addPair := func(tx pgx.Tx, voucher int64, first int) error {
		if err := rawPosting(ctx, tx, voucher, first, l.chart["6300"], "5"); err != nil {
			return err
		}
		return rawPosting(ctx, tx, voucher, first+1, l.chart["1920"], "-5")
	}
	// A balanced pair on the committed voucher.
	err = db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error { return addPair(tx, p.ID, 3) })
	if err == nil || !strings.Contains(err.Error(), "not inserted by this transaction") {
		t.Fatalf("a pair added to a committed voucher: %v", err)
	}
	// The same once its period is locked.
	if _, err := l.pool.Exec(ctx, "UPDATE accounting.settings SET locked_through = '2026-02-28'"); err != nil {
		t.Fatal(err)
	}
	err = db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error { return addPair(tx, p.ID, 3) })
	if err == nil || !strings.Contains(err.Error(), "not inserted by this transaction") {
		t.Fatalf("a pair added to a voucher in the locked period: %v", err)
	}
	// Another voucher inserted first does not open the committed one either.
	err = db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		id, err := rawVoucher(ctx, tx, 900, "2026-03-02")
		if err != nil {
			return err
		}
		if err := addPair(tx, id, 1); err != nil {
			return err
		}
		return addPair(tx, p.ID, 3)
	})
	if err == nil || !strings.Contains(err.Error(), "not inserted by this transaction") {
		t.Fatalf("a pair added to a committed voucher beside a new one: %v", err)
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.postings WHERE voucher_id = $1", p.ID); n != 2 {
		t.Fatalf("the committed voucher has %d postings, want its 2", n)
	}

	// Inside savepoints: a voucher inserted in a released savepoint takes its
	// postings after it; one in a rolled-back savepoint leaves the voucher
	// before it open.
	var inSavepoint, kept int64
	err = db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if inSavepoint, err = rawVoucher(ctx, sp, 901, "2026-03-03"); err != nil {
			return err
		}
		var differs bool
		if err := sp.QueryRow(ctx, `SELECT xmin::text <> pg_current_xact_id()::text FROM accounting.vouchers WHERE id = $1`,
			inSavepoint).Scan(&differs); err != nil {
			return err
		}
		if !differs {
			t.Error("the savepoint's row carries the top transaction's xid; the test no longer shows why xmin is not used")
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		if err := addPair(tx, inSavepoint, 1); err != nil {
			return err
		}
		if kept, err = rawVoucher(ctx, tx, 902, "2026-03-03"); err != nil {
			return err
		}
		if err := addPair(tx, kept, 1); err != nil {
			return err
		}
		sp, err = tx.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := rawVoucher(ctx, sp, 903, "2026-03-03"); err != nil {
			return err
		}
		if err := sp.Rollback(ctx); err != nil {
			return err
		}
		return addPair(tx, kept, 3)
	})
	if err != nil {
		t.Fatalf("postings after savepoints: %v", err)
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.postings WHERE voucher_id = ANY($1)", []int64{inSavepoint, kept}); n != 6 {
		t.Errorf("%d postings on the savepoints' vouchers, want 6", n)
	}
	// The engine inside a caller's savepoint.
	err = db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := Post(ctx, sp, l.simple("2026-03-04", "7", nil)); err != nil {
			return err
		}
		return sp.Commit(ctx)
	})
	if err != nil {
		t.Fatalf("the engine inside a savepoint: %v", err)
	}
	// Nothing carries to the connection's next transaction.
	var marker *string
	if err := l.pool.QueryRow(ctx, "SELECT nullif(current_setting('accounting.open_voucher', true), '')").Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if marker != nil {
		t.Errorf("the marker outlived its transaction: %q", *marker)
	}
}

// TRUNCATE fires no row trigger; each append-only table refuses it (H2).
func TestDatabase_NeverTruncatesTheBooks(t *testing.T) {
	l := newLedger(t)
	if _, err := l.post(t, l.simple("2026-02-01", "100", nil)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"vouchers", "postings", "attachments", "lock_events"} {
		_, err := l.pool.Exec(context.Background(), "TRUNCATE accounting."+table+" CASCADE")
		if err == nil || !strings.Contains(err.Error(), "never changed or deleted") {
			t.Errorf("TRUNCATE %s: %v", table, err)
		}
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.postings"); n != 2 {
		t.Errorf("%d postings left, want 2", n)
	}
}

// An existing item found by its source document is the line's party's (M6).
func TestPost_ASourceDocumentsItemIsItsPartys(t *testing.T) {
	l := newLedger(t)
	acme, beta, doc := int32(7), int32(8), int64(4321)
	sale := Voucher{Kind: "sales_invoice", Date: d("2026-02-02"), Description: "Invoice", Source: &Source{Module: "invoices", Kind: "document", ID: doc},
		Lines: []Line{{Role: "receivables", Amount: rat("50"), CustomerID: &acme, SourceDocumentID: &doc},
			{AccountID: l.chart["3000"], Amount: rat("-50")}}}
	if _, err := l.post(t, sale); err != nil {
		t.Fatal(err)
	}
	pay := Voucher{Kind: "customer_payment", Date: d("2026-02-03"), Description: "Paid", Source: &Source{Module: "invoices", Kind: "payment", ID: 1},
		Lines: []Line{{Role: "bank", Amount: rat("50")}, {Role: "receivables", Amount: rat("-50"), CustomerID: &beta, SourceDocumentID: &doc}}}
	if _, err := l.post(t, pay); codeOf(err) != codeOpenItemUnknown {
		t.Fatalf("another customer's document item: %v", err)
	}
}

// Every post takes the items it names FOR UPDATE (M5): one waits while
// another transaction holds an item, though the posting's foreign key alone
// would not.
func TestPost_TakesTheItemsItNames(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	cust := int32(7)
	open := Voucher{Kind: "manual", Date: d("2026-02-01"), Description: "Opening item", PostedBy: ptr(uuid.New()),
		Lines: []Line{{Role: "receivables", Amount: rat("100"), CustomerID: &cust, Item: &OpenItemSpec{DocumentNumber: "A-1"}},
			{AccountID: l.chart["3000"], Amount: rat("-100")}}}
	if _, err := l.post(t, open); err != nil {
		t.Fatal(err)
	}
	var item int64
	if err := l.pool.QueryRow(ctx, "SELECT id FROM accounting.open_items WHERE document_number = 'A-1'").Scan(&item); err != nil {
		t.Fatal(err)
	}
	hold, err := l.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, "SELECT 1 FROM accounting.open_items WHERE id = $1 FOR NO KEY UPDATE", item); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.post(t, Voucher{Kind: "manual", Date: d("2026-02-05"), Description: "Paid", PostedBy: ptr(uuid.New()),
			Lines: []Line{{Role: "bank", Amount: rat("100")}, {Role: "receivables", Amount: rat("-100"), CustomerID: &cust, OpenItemID: &item}}})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the post did not wait for the item: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A person's voucher names no item that stands for a document in Invoices
// (M5); a reversal of a system voucher, which turns back such a line, may.
func TestPost_APersonsVoucherNamesNoInvoicesItem(t *testing.T) {
	l := newLedger(t)
	cust, doc := int32(7), int64(99)
	sale := Voucher{Kind: "sales_invoice", Date: d("2026-02-02"), Description: "Invoice", Source: &Source{Module: "invoices", Kind: "document", ID: doc},
		Lines: []Line{{Role: "receivables", Amount: rat("50"), CustomerID: &cust, SourceDocumentID: &doc},
			{AccountID: l.chart["3000"], Amount: rat("-50")}}}
	posted, err := l.post(t, sale)
	if err != nil {
		t.Fatal(err)
	}
	var item int64
	if err := l.pool.QueryRow(context.Background(), "SELECT id FROM accounting.open_items WHERE source_document_id = 99").Scan(&item); err != nil {
		t.Fatal(err)
	}
	manual := Voucher{Kind: "manual", Date: d("2026-02-05"), Description: "Paid by hand", PostedBy: ptr(uuid.New()),
		Lines: []Line{{Role: "bank", Amount: rat("50")}, {Role: "receivables", Amount: rat("-50"), CustomerID: &cust, OpenItemID: &item}}}
	if _, err := l.post(t, manual); codeOf(err) != codeOpenItemSourceDocument {
		t.Fatalf("a manual line on Invoices' item: %v", err)
	}
	err = db.WithTx(context.Background(), l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rev, err := reversal(context.Background(), store.New(tx), posted.ID, d("2026-02-06"), "test", "en", nil)
		if err != nil {
			return err
		}
		rev.Source = &Source{Module: "invoices", Kind: "credit_note", ID: 1}
		_, err = Post(context.Background(), tx, rev)
		return err
	})
	if err != nil {
		t.Fatalf("the source's reversal: %v", err)
	}
}

// A reversal posts on an account made inactive since (M8).
func TestPost_AReversalPostsOnAnInactiveAccount(t *testing.T) {
	l := newLedger(t)
	p, err := l.post(t, l.simple("2026-02-01", "100", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.pool.Exec(context.Background(), "UPDATE accounting.accounts SET active = false WHERE number = '6300'"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.post(t, l.simple("2026-02-02", "1", nil)); codeOf(err) != codeAccountInactive {
		t.Fatalf("a new voucher on the inactive account: %v", err)
	}
	err = db.WithTx(context.Background(), l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rev, err := reversal(context.Background(), store.New(tx), p.ID, d("2026-02-03"), "wrong", "en", ptr(uuid.New()))
		if err != nil {
			return err
		}
		_, err = Post(context.Background(), tx, rev)
		return err
	})
	if err != nil {
		t.Fatalf("the reversal on the inactive account: %v", err)
	}
}

// Two transactions posting one source at once: the second waits on the index,
// then answers the first's voucher as a Repeat, and its own transaction goes
// on (L4).
func TestPost_ASourcePostedAtOnceIsARepeat(t *testing.T) {
	l := newLedger(t)
	ctx := context.Background()
	src := &Source{Module: "invoices", Kind: "document", ID: 77}
	first, err := l.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(ctx) }()
	p1, err := Post(ctx, first, l.simple("2026-02-01", "100", src))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Posted, 1)
	go func() {
		var p Posted
		err := db.WithTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			if p, err = Post(ctx, tx, l.simple("2026-02-01", "100", src)); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "SELECT 1")
			return err
		})
		if err != nil {
			t.Errorf("the second post: %v", err)
		}
		done <- p
	}()
	time.Sleep(300 * time.Millisecond)
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	p2 := <-done
	if !p2.Repeat || p2.ID != p1.ID {
		t.Fatalf("the second post = %+v, want a repeat of %d", p2, p1.ID)
	}
	if n := countRows(t, l.pool, "SELECT count(*) FROM accounting.vouchers"); n != 1 {
		t.Errorf("%d vouchers", n)
	}
	if n := countRows(t, l.pool, "SELECT next_value FROM accounting.counters WHERE counter_name = 'voucher:2026'"); n != 2 {
		t.Errorf("the counter is at %d: the repeat took a number", n)
	}
}

// Every system role sits on an account of its kind in the default chart (M7).
func TestDefaultChart_RolesSitOnTheirKind(t *testing.T) {
	for _, a := range defaultChart {
		if a.role != "" && roleKinds[a.role] != kindOf(a.number) {
			t.Errorf("role %s on %s (%s), want %s", a.role, a.number, kindOf(a.number), roleKinds[a.role])
		}
	}
	for _, r := range systemRoles {
		if roleKinds[r] == "" {
			t.Errorf("role %s has no kind", r)
		}
	}
}
