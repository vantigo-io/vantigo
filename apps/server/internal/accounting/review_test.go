package accounting_test

// The rules delivery A's review added, through the API: the opening entered
// once at a time and read page by page from Invoices, its reversal under
// accounting:close, the result's two lines and the sides of the balance
// sheet, the reports' cap, the open items' pages, the accounts' kinds and the
// roles', and the lock's and the books' limits.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Two openings entered at once: one is posted, the other sees it (H1). Both
// requests are held at the voucher counter, which every post takes, so
// without the settings row taken FOR UPDATE both would have passed the check
// for a standing opening before either posted.
func TestOpening_TwoAtOnceEnterOne(t *testing.T) {
	b := newBooks(t)
	ctx := t.Context()
	b.h.Exec(t, "INSERT INTO accounting.counters (counter_name, next_value) VALUES ('voucher:2026', 1)")
	hold, err := b.h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, "SELECT 1 FROM accounting.counters WHERE counter_name = 'voucher:2026' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"lines": []map[string]any{{"accountId": b.acc("1920"), "amount": "100"}, {"accountId": b.acc("2000"), "amount": "-100"}}}
	statuses := make(chan int, 2)
	for range 2 {
		go func() { statuses <- b.c.Do(http.MethodPost, base+"/opening", body).Status }()
	}
	deadline := time.Now().Add(10 * time.Second)
	for b.h.Count(t, "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'") < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the two openings never both waited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	got := map[int]int{}
	for range 2 {
		got[<-statuses]++
	}
	if got[http.StatusCreated] != 1 || got[http.StatusConflict] != 1 {
		t.Fatalf("statuses = %v, want one 201 and one 409", got)
	}
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vouchers WHERE kind = 'opening'"); n != 1 {
		t.Errorf("%d opening vouchers", n)
	}
}

// The opening reads Invoices' open invoices page by page; each item made for
// an invoice keeps its page's AsOfSeq, and the settings the lowest (L3, R14).
// A line naming an invoice must be one open at the day, its customer's and
// its amount (M6).
func TestOpening_ReadsEveryPageAndKeepsEachPagesSeq(t *testing.T) {
	b := newBooks(t)
	for n := range contracts.MaxJournalPage + 1 {
		b.h.journal.open = append(b.h.journal.open, contracts.OpenDocument{ID: int64(5000 + n), Number: int64(1 + n),
			CustomerID: customerAcme, OpenNOK: "10.00"})
	}
	b.h.journal.open[contracts.MaxJournalPage].CustomerID = customerBeta
	b.h.journal.seqs = []int64{40, 35, 38}
	res := b.c.Do(http.MethodGet, base+"/opening", nil)
	var o gen.AccountingOpening
	res.JSON(&o)
	if len(o.OpenInvoices) != contracts.MaxJournalPage+1 {
		t.Fatalf("%d open invoices offered, want both pages'", len(o.OpenInvoices))
	}
	b.h.journal.calls = 0
	first, last := int64(5000), int64(5000+contracts.MaxJournalPage)
	refused := func(lines ...map[string]any) {
		t.Helper()
		lines = append(lines, map[string]any{"accountId": b.acc("2000"), "amount": "-10"})
		expectProblem(t, b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": lines}), http.StatusBadRequest, "open_item_unknown")
	}
	refused(map[string]any{"accountId": b.acc("1500"), "amount": "10", "customerId": customerAcme, "sourceDocumentId": 4999})
	refused(map[string]any{"accountId": b.acc("1500"), "amount": "10", "customerId": customerBeta, "sourceDocumentId": first})
	refused(map[string]any{"accountId": b.acc("1500"), "amount": "11", "customerId": customerAcme, "sourceDocumentId": first})
	b.h.journal.calls = 0
	res = b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1500"), "amount": "10", "customerId": customerAcme, "documentNumber": "1", "sourceDocumentId": first},
		{"accountId": b.acc("1500"), "amount": "10", "customerId": customerBeta, "documentNumber": "501", "sourceDocumentId": last},
		{"accountId": b.acc("1500"), "amount": "5", "customerId": customerBeta, "documentNumber": "Old"},
		{"accountId": b.acc("2000"), "amount": "-25"},
	}})
	if res.Status != http.StatusCreated {
		t.Fatalf("opening = %d %s", res.Status, res.Body)
	}
	for doc, want := range map[int64]int{first: 40, last: 35} {
		if n := b.h.Count(t, "SELECT opening_as_of_seq FROM accounting.open_items WHERE source_document_id = $1", doc); n != want {
			t.Errorf("document %d's item opening_as_of_seq = %d, want %d", doc, n, want)
		}
	}
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.open_items WHERE document_number = 'Old' AND opening_as_of_seq IS NULL"); n != 1 {
		t.Error("an item of no invoice carries a seq")
	}
	if n := b.h.Count(t, "SELECT opening_as_of_seq FROM accounting.settings"); n != 35 {
		t.Errorf("settings' opening_as_of_seq = %d, want the lowest page's, 35", n)
	}
}

// Reversing the opening needs accounting:close as entering it does (M1).
func TestOpening_ReversalNeedsClose(t *testing.T) {
	b := newBooks(t)
	res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1920"), "amount": "100"}, {"accountId": b.acc("2000"), "amount": "-100"}}})
	var v gen.AccountingVoucher
	res.JSON(&v)
	path := fmt.Sprintf("%s/vouchers/%d/reverse", base, v.Id)
	reverse := map[string]any{"date": "2026-02-01", "reason": "Wrong"}
	if got := b.h.SignIn(t, poster...).Do(http.MethodPost, path, reverse).Status; got != http.StatusForbidden {
		t.Fatalf("a poster reversed the opening: %d", got)
	}
	if got := b.c.Do(http.MethodPost, path, reverse).Status; got != http.StatusCreated {
		t.Fatalf("the accountant's reversal = %d", got)
	}
}

// The opening is final from the first lock, even once the lock is lifted (L2).
func TestOpening_FinalAfterALiftedLock(t *testing.T) {
	b := newBooks(t)
	res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1920"), "amount": "100"}, {"accountId": b.acc("2000"), "amount": "-100"}}})
	var v gen.AccountingVoucher
	res.JSON(&v)
	if got := b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-01-31"}).Status; got != http.StatusOK {
		t.Fatalf("lock = %d", got)
	}
	if got := b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": nil, "reason": "Opened again"}).Status; got != http.StatusOK {
		t.Fatalf("lift = %d", got)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, v.Id), map[string]any{"date": "2026-02-01", "reason": "x"}),
		http.StatusConflict, "opening_locked")
	var o gen.AccountingOpening
	b.c.Do(http.MethodGet, base+"/opening", nil).JSON(&o)
	if !o.Locked || o.CanEnter {
		t.Errorf("opening = %+v", o)
	}
}

// The balance sheet shows the result not yet closed as two lines, the
// earlier years' and this one's (M2), and balances with accounts outside
// every group, each on its kind's side (M3).
func TestBalanceSheet_TwoResultLinesAndEveryAccount(t *testing.T) {
	b := newBooks(t)
	mustPost(t, b.c, "2026-03-01", "Rent", line{"accountId": b.acc("6300"), "amount": "800"}, line{"accountId": b.acc("1920"), "amount": "-800"})
	mustPost(t, b.c, "2027-02-01", "Sale", line{"accountId": b.acc("1920"), "amount": "500"}, line{"accountId": b.acc("3000"), "amount": "-500"})
	// Two accounts no group of their kind takes, as an import could bring.
	b.h.Exec(t, `INSERT INTO accounting.accounts (number, name, kind, created_at, updated_at) VALUES
		('9100', 'Odd asset', 'asset', now(), now()), ('2999', 'Odd income', 'income', now(), now())`)
	ids := map[string]int32{}
	for _, n := range []string{"9100", "2999"} {
		ids[n] = int32(b.h.Count(t, "SELECT id FROM accounting.accounts WHERE number = $1", n))
	}
	mustPost(t, b.c, "2027-03-01", "Odd", line{"accountId": ids["9100"], "amount": "70"}, line{"accountId": ids["2999"], "amount": "-70"})

	bs := reportOf(t, b, "balance_sheet?from=2027-01-01&to=2027-06-30")
	earlier := rowWith(bs, "name", "Tidligere års resultat, ikke avsluttet")
	year := rowWith(bs, "name", "Årets resultat, ikke avsluttet")
	if earlier == nil || year == nil || earlier.Cells["amount"] != "-800.00" || year.Cells["amount"] != "570.00" {
		t.Fatalf("result lines = %+v / %+v in %+v", earlier, year, bs.Rows)
	}
	assets, claims := rowWith(bs, "name", "Sum eiendeler"), rowWith(bs, "name", "Sum egenkapital og gjeld")
	if assets == nil || claims == nil || assets.Cells["amount"] != claims.Cells["amount"] || assets.Cells["amount"] != "-230.00" {
		t.Errorf("assets %+v, claims %+v", assets, claims)
	}
	if row := rowWith(bs, "account", "9100"); row == nil || row.Cells["amount"] != "70.00" {
		t.Errorf("the odd asset = %+v", row)
	}
	if row := rowWith(bs, "account", "2999"); row != nil {
		t.Errorf("an income account on the balance sheet: %+v", row)
	}
	pl := reportOf(t, b, "profit_and_loss?from=2027-01-01&to=2027-12-31")
	if row := rowWith(pl, "account", "2999"); row == nil || row.Cells["amount"] != "70.00" {
		t.Errorf("the odd income in the P&L = %+v", pl.Rows)
	}
	if row := rowWith(pl, "name", "Resultat"); row == nil || row.Cells["amount"] != "570.00" {
		t.Errorf("P&L result = %+v", row)
	}
}

// An account's kind is its class's (M3).
func TestAccounts_KindFollowsTheClass(t *testing.T) {
	b := newBooks(t)
	for _, tc := range []struct {
		number, kind string
		ok           bool
	}{
		{"1600", "expense", false}, {"1601", "asset", true}, {"2001", "liability", false}, {"2101", "liability", true},
		{"3901", "expense", false}, {"6301", "expense", true}, {"8401", "income", true}, {"8402", "expense", true},
		{"9001", "expense", false},
	} {
		res := b.c.Do(http.MethodPost, base+"/accounts", map[string]any{"number": tc.number, "name": "Test", "kind": tc.kind})
		if tc.ok && res.Status != http.StatusCreated {
			t.Errorf("%s %s = %d %s", tc.number, tc.kind, res.Status, res.Body)
		}
		if !tc.ok {
			expectProblem(t, res, http.StatusBadRequest, "account_kind_mismatch")
		}
	}
}

// A role moves only to an account of its kind, and receivables and payables
// never off an account with postings (M7).
func TestRoles_KindAndPostings(t *testing.T) {
	b := newBooks(t)
	move := func(role, number string) *modtest.Response {
		return b.c.Do(http.MethodPut, base+"/roles/"+role, map[string]any{"accountId": b.acc(number)})
	}
	expectProblem(t, move("bank", "6300"), http.StatusBadRequest, "account_kind_mismatch")
	expectProblem(t, move("payables", "1530"), http.StatusBadRequest, "account_kind_mismatch")
	if got := move("receivables", "1530").Status; got != http.StatusOK {
		t.Fatalf("receivables to an asset account without postings = %d", got)
	}
	if got := move("receivables", "1500").Status; got != http.StatusOK {
		t.Fatalf("receivables back = %d", got)
	}
	mustPost(t, b.c, "2026-02-01", "Sale", line{"accountId": b.acc("1500"), "amount": "100", "customerId": customerAcme},
		line{"accountId": b.acc("3000"), "amount": "-100"})
	expectProblem(t, move("receivables", "1530"), http.StatusConflict, "account_has_postings")
	if got := move("bank", "1950").Status; got != http.StatusOK {
		t.Errorf("bank to another asset account = %d", got)
	}
}

// The journal and an account's specification are refused past the cap,
// never cut short (M4); the open items come in pages with their count.
func TestReports_TooLargeAndOpenItemPages(t *testing.T) {
	b := newBooks(t)
	defer accounting.SetMaxReportLines(3)()
	mustPost(t, b.c, "2026-02-01", "One", line{"accountId": b.acc("6300"), "amount": "1"}, line{"accountId": b.acc("1920"), "amount": "-1"})
	if r := reportOf(t, b, "journal?from=2026-01-01&to=2026-12-31"); len(r.Rows) != 3 {
		t.Fatalf("journal at the cap = %+v", r.Rows)
	}
	mustPost(t, b.c, "2026-02-02", "Two", line{"accountId": b.acc("6300"), "amount": "1"}, line{"accountId": b.acc("1920"), "amount": "-1"})
	expectProblem(t, b.c.Do(http.MethodGet, base+"/reports/journal?from=2026-01-01&to=2026-12-31", nil), http.StatusBadRequest, "report_too_large")
	mustPost(t, b.c, "2026-02-03", "Three", line{"accountId": b.acc("6300"), "amount": "1"}, line{"accountId": b.acc("1920"), "amount": "-1"})
	mustPost(t, b.c, "2026-02-04", "Four", line{"accountId": b.acc("6300"), "amount": "1"}, line{"accountId": b.acc("1920"), "amount": "-1"})
	expectProblem(t, b.c.Do(http.MethodGet, fmt.Sprintf("%s/reports/account?from=2026-01-01&to=2026-12-31&accountId=%d", base, b.acc("6300")), nil),
		http.StatusBadRequest, "report_too_large")

	for _, customer := range []int32{customerAcme, customerBeta, 503} {
		mustPost(t, b.c, "2026-03-01", "Item", line{"accountId": b.acc("1500"), "amount": "10", "customerId": customer},
			line{"accountId": b.acc("3000"), "amount": "-10"})
	}
	var page gen.AccountingOpenItemList
	b.c.Do(http.MethodGet, base+"/open-items?limit=2", nil).JSON(&page)
	if len(page.Items) != 2 || page.Total != 3 {
		t.Errorf("first page = %d items of %d", len(page.Items), page.Total)
	}
	b.c.Do(http.MethodGet, base+"/open-items?limit=2&offset=2", nil).JSON(&page)
	if len(page.Items) != 1 || page.Total != 3 {
		t.Errorf("second page = %d items of %d", len(page.Items), page.Total)
	}
}

// The lock reaches at most this month's end (L1); the books start on
// 2022-01-01 or later, the seeded rates holding from then (the rates
// decision).
func TestLimits_TheLockAndTheBooksStart(t *testing.T) {
	b := newBooks(t)
	// The harness's day is 2026-09-12.
	expectProblem(t, b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-10-31"}), http.StatusBadRequest, "lock_in_future")
	if got := b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-09-30"}).Status; got != http.StatusOK {
		t.Errorf("lock through this month = %d", got)
	}

	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	expectProblem(t, c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2021-12-31", "vatTerm": "bimonthly", "language": "en"}),
		http.StatusBadRequest, "books_start_too_early")
	if res := c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2022-01-01", "vatTerm": "bimonthly", "language": "en"}); res.Status != http.StatusCreated {
		t.Fatalf("setup from 2022 = %d %s", res.Status, res.Body)
	}
	if n := h.Count(t, "SELECT count(*) FROM accounting.tax_code_rates WHERE valid_from <> '2022-01-01'"); n != 0 {
		t.Errorf("%d rates not from 2022-01-01", n)
	}
	mustPost(t, c, "2022-03-01", "Old purchase", line{"accountId": accounts(t, c)["6300"].Id, "amount": "100", "taxCodeId": taxCodes(t, c)["1"].Id},
		line{"accountId": accounts(t, c)["1920"].Id, "amount": "-125"})
}
