package accounting_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// books is a set-up installation with its chart and tax codes at hand.
type books struct {
	h     *harness
	c     *modtest.Client
	chart map[string]gen.AccountingAccount
	codes map[string]gen.AccountingTaxCode
}

func newBooks(t *testing.T) *books {
	t.Helper()
	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	return &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
}

func (b *books) acc(number string) int32 { return b.chart[number].Id }
func (b *books) code(code string) int32  { return b.codes[code].Id }

// The manual journal's tax lines (A6), to the øre: net and gross, each rate,
// reverse charge and the zero-rate codes, with docmath's rounding — half
// away from zero.
func TestJournal_TaxLinesToTheOre(t *testing.T) {
	b := newBooks(t)
	type posting struct {
		account, amount string
	}
	for _, tc := range []struct {
		name, entry, code, account, amount string
		want                               []posting
	}{
		{"net 25 %", "net", "1", "6300", "1000.00", []posting{{"6300", "1000.00"}, {"2710", "250.00"}}},
		{"gross 25 %", "gross", "1", "6300", "1000.00", []posting{{"6300", "800.00"}, {"2710", "200.00"}}},
		{"gross 15 %", "gross", "11", "6300", "99.99", []posting{{"6300", "86.95"}, {"2711", "13.04"}}},
		{"net 11.11 %", "net", "12", "4005", "33.33", []posting{{"4005", "33.33"}, {"2713", "3.70"}}},
		{"net half an øre up", "net", "1", "6300", "0.02", []posting{{"6300", "0.02"}, {"2710", "0.01"}}},
		{"gross 12 %", "gross", "13", "7000", "0.05", []posting{{"7000", "0.04"}, {"2712", "0.01"}}},
		{"output credit net", "net", "3", "3000", "-1234.56", []posting{{"3000", "-1234.56"}, {"2700", "-308.64"}}},
		{"output credit gross", "gross", "31", "3010", "-115.00", []posting{{"3010", "-100.00"}, {"2701", "-15.00"}}},
		{"reverse charge, gross is net", "gross", "86", "6700", "400.00", []posting{{"6700", "400.00"}, {"2715", "100.00"}, {"2705", "-100.00"}}},
		{"reverse charge without deduction", "net", "87", "6700", "400.00", []posting{{"6700", "400.00"}, {"6700", "100.00"}, {"2705", "-100.00"}}},
		{"zero rate", "net", "5", "3100", "-500.00", []posting{{"3100", "-500.00"}}},
		{"outside the law", "net", "6", "3200", "-500.00", []posting{{"3200", "-500.00"}}},
		{"no VAT treatment", "gross", "0", "6300", "500.00", []posting{{"6300", "500.00"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := b.c.Do(http.MethodPost, base+"/vouchers/preview", map[string]any{"voucherDate": "2026-03-10", "description": tc.name,
				"entry": tc.entry, "lines": []line{{"accountId": b.acc(tc.account), "amount": tc.amount, "taxCodeId": b.code(tc.code)}}})
			if res.Status != http.StatusOK {
				t.Fatalf("preview = %d %s", res.Status, res.Body)
			}
			var p gen.AccountingJournalPreview
			res.JSON(&p)
			if len(p.Lines) != len(tc.want) {
				t.Fatalf("lines = %+v, want %v", p.Lines, tc.want)
			}
			for i, w := range tc.want {
				if p.Lines[i].AccountId != b.acc(w.account) || p.Lines[i].Amount != w.amount {
					t.Errorf("line %d = %d %s, want %s %s", i, p.Lines[i].AccountId, p.Lines[i].Amount, w.account, w.amount)
				}
				if p.Lines[i].TaxCodeId == nil || *p.Lines[i].TaxCodeId != b.code(tc.code) {
					t.Errorf("line %d carries no tax code", i)
				}
			}
			if p.Balanced {
				t.Errorf("one line cannot balance")
			}
		})
	}
}

// A journal posts its lines and their tax lines, balanced, numbered from 1 in
// its fiscal year, and reads back with names and links.
func TestJournal_PostsABalancedVoucher(t *testing.T) {
	b := newBooks(t)
	v := mustPost(t, b.c, "2026-03-10", "Rent for March",
		line{"accountId": b.acc("6340"), "amount": "1000.00", "taxCodeId": b.code("1"), "text": "Electricity"},
		line{"accountId": b.acc("1920"), "amount": "-1250.00"})
	if v.Display != "2026-000001" || v.Kind != "manual" || v.System || v.Amount != "1250.00" || len(v.Postings) != 3 {
		t.Fatalf("voucher = %+v", v)
	}
	tax := v.Postings[1]
	if tax.AccountNumber != "2710" || tax.Amount != "250.00" || tax.TaxRole == nil || *tax.TaxRole != "tax" || tax.TaxBase == nil || *tax.TaxBase != "1000.00" {
		t.Errorf("tax posting = %+v", tax)
	}
	if v.PostedByUserId == nil {
		t.Error("postedByUserId is empty")
	}
	second := mustPost(t, b.c, "2026-04-01", "Second", line{"accountId": b.acc("6300"), "amount": "10"}, line{"accountId": b.acc("1920"), "amount": "-10"})
	if second.Number != 2 {
		t.Errorf("second voucher number = %d", second.Number)
	}
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, v.Id), nil)
	var got gen.AccountingVoucher
	res.JSON(&got)
	if got.Postings[0].AccountName != b.chart["6340"].Name || got.Postings[0].Description != "Electricity" {
		t.Errorf("read back = %+v", got.Postings[0])
	}
	if b.h.Count(t, "SELECT count(*) FROM accounting.vouchers WHERE fiscal_year = 2026") != 2 {
		t.Error("two vouchers in 2026 expected")
	}
	// The list filters and searches.
	for _, q := range []string{"?q=Rent", "?q=2026-000001", "?q=Electricity", "?accountId=" + fmt.Sprint(b.acc("6340")), "?kind=manual&from=2026-03-01&to=2026-03-31"} {
		res := b.c.Do(http.MethodGet, base+"/vouchers"+q, nil)
		var list gen.AccountingVoucherList
		res.JSON(&list)
		if list.Total != 1 || list.Items[0].Id != v.Id {
			t.Errorf("list %s = %+v", q, list)
		}
	}
	res = b.c.Do(http.MethodGet, base+"/vouchers?limit=1", nil)
	var list gen.AccountingVoucherList
	res.JSON(&list)
	if list.Total != 2 || len(list.Items) != 1 || list.Items[0].Id != second.Id {
		t.Errorf("page = %+v", list)
	}
}

func TestJournal_RefusesWhatTheLedgerDoesNotAllow(t *testing.T) {
	b := newBooks(t)
	bank := b.acc("1920")
	cases := []struct {
		name   string
		lines  []line
		date   string
		status int
		code   string
	}{
		{"unbalanced", []line{{"accountId": b.acc("6300"), "amount": "100"}, {"accountId": bank, "amount": "-99.99"}}, "2026-02-01", 400, "voucher_unbalanced"},
		{"no lines", []line{}, "2026-02-01", 400, "voucher_empty"},
		{"a zero amount", []line{{"accountId": bank, "amount": "0"}}, "2026-02-01", 400, "amount_invalid"},
		{"three decimals", []line{{"accountId": bank, "amount": "1.001"}, {"accountId": bank, "amount": "-1.001"}}, "2026-02-01", 400, "amount_invalid"},
		{"unknown account", []line{{"accountId": 9, "amount": "1"}, {"accountId": bank, "amount": "-1"}}, "2026-02-01", 400, "account_unknown"},
		{"receivables without a customer", []line{{"accountId": b.acc("1500"), "amount": "1"}, {"accountId": bank, "amount": "-1"}}, "2026-02-01", 400, "party_required"},
		{"payables with a customer", []line{{"accountId": b.acc("2400"), "amount": "1", "customerId": customerAcme, "supplierId": 7}, {"accountId": bank, "amount": "-1"}}, "2026-02-01", 400, "party_not_allowed"},
		{"an open item off the control accounts", []line{{"accountId": b.acc("6300"), "amount": "1", "openItemId": 1}, {"accountId": bank, "amount": "-1"}}, "2026-02-01", 400, "open_item_unknown"},
		{"unknown tax code", []line{{"accountId": b.acc("6300"), "amount": "1", "taxCodeId": 1}, {"accountId": bank, "amount": "-1"}}, "2026-02-01", 400, "tax_code_unknown"},
		{"before the books", []line{{"accountId": b.acc("6300"), "amount": "1"}, {"accountId": bank, "amount": "-1"}}, "2025-12-31", 400, "date_before_books_start"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectProblem(t, journal(b.c, tc.date, tc.name, tc.lines...), tc.status, tc.code)
		})
	}
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vouchers"); n != 0 {
		t.Errorf("%d vouchers after refusals", n)
	}
	// An inactive account takes no posting.
	a := b.chart["6995"]
	res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/accounts/%d", base, a.Id), map[string]any{"number": a.Number, "name": a.Name,
		"kind": a.Kind, "defaultTaxCodeId": a.DefaultTaxCodeId, "active": false, "revision": a.Revision})
	if res.Status != http.StatusOK {
		t.Fatalf("deactivate = %d %s", res.Status, res.Body)
	}
	expectProblem(t, journal(b.c, "2026-02-01", "x", line{"accountId": a.Id, "amount": "1"}, line{"accountId": bank, "amount": "-1"}), 400, "account_inactive")
	// The preview reports the difference instead of refusing it.
	res = b.c.Do(http.MethodPost, base+"/vouchers/preview", map[string]any{"voucherDate": "2026-02-01", "description": "x",
		"lines": []line{{"accountId": bank, "amount": "10"}, {"accountId": b.acc("3000"), "amount": "-8", "taxCodeId": b.code("3")}}})
	var p gen.AccountingJournalPreview
	res.JSON(&p)
	if res.Status != http.StatusOK || p.Difference != "0.00" || !p.Balanced || p.Debit != "10.00" || p.Credit != "10.00" {
		t.Errorf("preview = %d %+v", res.Status, p)
	}
}

// A line on receivables lands on the customer's unallocated item unless it
// names one (R5); a reversal (A7) turns every sign, names the original, and
// only once; a system voucher is not a person's to reverse.
func TestReversal(t *testing.T) {
	b := newBooks(t)
	v := mustPost(t, b.c, "2026-02-10", "Opening debt", line{"accountId": b.acc("1500"), "amount": "500", "customerId": customerAcme},
		line{"accountId": b.acc("3000"), "amount": "-500"})
	if v.Postings[0].OpenItemId == nil || v.Postings[0].CustomerName == nil || *v.Postings[0].CustomerName != "Acme AS" {
		t.Fatalf("receivables posting = %+v", v.Postings[0])
	}
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, v.Id), map[string]any{"date": "2026-02-11", "reason": "Wrong customer"})
	if res.Status != http.StatusCreated {
		t.Fatalf("reverse = %d %s", res.Status, res.Body)
	}
	var r gen.AccountingVoucher
	res.JSON(&r)
	if r.Reverses == nil || r.Reverses.Id != v.Id || r.Postings[0].Amount != "-500.00" || r.Postings[1].Amount != "500.00" ||
		r.ReversalReason == nil || *r.Postings[0].OpenItemId != *v.Postings[0].OpenItemId || !strings.HasPrefix(r.Description, "Tilbakeføring av 2026-000001") {
		t.Fatalf("reversal = %+v", r)
	}
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, v.Id), nil)
	var orig gen.AccountingVoucher
	res.JSON(&orig)
	if orig.ReversedBy == nil || orig.ReversedBy.Id != r.Id || orig.ReversedBy.Display != "2026-000002" {
		t.Errorf("original's link = %+v", orig.ReversedBy)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, v.Id), map[string]any{"date": "2026-02-11", "reason": "again"}),
		http.StatusConflict, "voucher_already_reversed")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, r.Id), map[string]any{"date": "2026-02-11", "reason": "again"}),
		http.StatusConflict, "voucher_already_reversed")
	if got := b.c.Do(http.MethodPost, base+"/vouchers/99999/reverse", map[string]any{"date": "2026-02-11", "reason": "x"}).Status; got != http.StatusNotFound {
		t.Errorf("unknown voucher = %d", got)
	}
	// A voucher the system posted for a source.
	sys := postSystem(t, b.h, b.acc("1920"), b.acc("3000"), "2026-02-12")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, sys), map[string]any{"date": "2026-02-12", "reason": "x"}),
		http.StatusConflict, "voucher_owned_by_source")
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, sys), nil)
	var sv gen.AccountingVoucher
	res.JSON(&sv)
	if !sv.System || sv.Source == nil || sv.Source.Module != "invoices" || sv.Rule == nil {
		t.Errorf("system voucher = %+v", sv)
	}
}

// The lock (A8, R10): forward to a month end, back only with a reason, the
// history kept; a manual voucher on or before it is refused.
func TestLock(t *testing.T) {
	b := newBooks(t)
	put := func(body map[string]any) *modtest.Response { return b.c.Do(http.MethodPut, base+"/lock", body) }
	expectProblem(t, put(map[string]any{"lockedThrough": "2026-02-15"}), 400, "lock_not_month_end")
	expectProblem(t, put(map[string]any{"lockedThrough": "2025-12-31"}), 400, "lock_before_books_start")
	res := put(map[string]any{"lockedThrough": "2026-03-31"})
	if res.Status != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Status, res.Body)
	}
	expectProblem(t, journal(b.c, "2026-03-31", "late", line{"accountId": b.acc("6300"), "amount": "1"}, line{"accountId": b.acc("1920"), "amount": "-1"}),
		http.StatusConflict, "period_locked")
	mustPost(t, b.c, "2026-04-01", "open", line{"accountId": b.acc("6300"), "amount": "1"}, line{"accountId": b.acc("1920"), "amount": "-1"})
	expectProblem(t, put(map[string]any{"lockedThrough": "2026-02-28"}), 400, "lock_reason_required")
	res = put(map[string]any{"lockedThrough": "2026-02-28", "reason": "Locked March too early"})
	var lock gen.AccountingLock
	res.JSON(&lock)
	if res.Status != http.StatusOK || lock.LockedThrough == nil || lock.LockedThrough.String() != "2026-02-28" || len(lock.Events) != 2 ||
		lock.Events[0].Reason == nil || lock.Events[0].LockedFrom.String() != "2026-03-31" {
		t.Fatalf("lock back = %d %+v", res.Status, lock)
	}
	res = b.c.Do(http.MethodGet, base+"/lock", nil)
	res.JSON(&lock)
	if len(lock.Events) != 2 || lock.BooksStart.String() != "2026-01-01" {
		t.Errorf("lock = %+v", lock)
	}
	// A system voucher dated in the locked period is booked on the first open
	// day, keeping its document date.
	id := postSystem(t, b.h, b.acc("1920"), b.acc("3000"), "2026-02-10")
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, id), nil)
	var v gen.AccountingVoucher
	res.JSON(&v)
	if v.VoucherDate.String() != "2026-03-01" || v.DocumentDate.String() != "2026-02-10" {
		t.Errorf("moved voucher = %s / %s", v.VoucherDate, v.DocumentDate)
	}
	// Lifting it needs the reason too.
	res = put(map[string]any{"lockedThrough": nil, "reason": "Reopened"})
	res.JSON(&lock)
	if lock.LockedThrough != nil {
		t.Errorf("unlocked = %+v", lock)
	}
}

// Attachments (A4): kept by name, type, size and hash, served back as they
// were, never removed.
func TestAttachments(t *testing.T) {
	b := newBooks(t)
	v := mustPost(t, b.c, "2026-02-01", "Receipt", line{"accountId": b.acc("6300"), "amount": "1"}, line{"accountId": b.acc("1920"), "amount": "-1"})
	upload := func(name string, content []byte) *modtest.Response {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, _ := w.CreateFormFile("file", name)
		_, _ = part.Write(content)
		_ = w.Close()
		return b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/attachments", base, v.Id), nil, modtest.RawBody(w.FormDataContentType(), body.Bytes()))
	}
	res := upload("kvittering.pdf", []byte("%PDF-1.4 a receipt"))
	if res.Status != http.StatusCreated {
		t.Fatalf("upload = %d %s", res.Status, res.Body)
	}
	var a gen.AccountingAttachment
	res.JSON(&a)
	if a.FileName != "kvittering.pdf" || a.SizeBytes != 18 || len(a.Sha256) != 64 || a.ContentType != "application/pdf" {
		t.Errorf("attachment = %+v", a)
	}
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d/attachments/%d", base, v.Id, a.Id), nil)
	if res.Status != http.StatusOK || string(res.Body) != "%PDF-1.4 a receipt" || !strings.Contains(res.Header("Content-Disposition"), "kvittering.pdf") {
		t.Errorf("download = %d %q %q", res.Status, res.Body, res.Header("Content-Disposition"))
	}
	if got := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d/attachments/%d", base, v.Id+1, a.Id), nil).Status; got != http.StatusNotFound {
		t.Errorf("another voucher's attachment = %d", got)
	}
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, v.Id), nil)
	var got gen.AccountingVoucher
	res.JSON(&got)
	if len(got.Attachments) != 1 {
		t.Errorf("attachments = %+v", got.Attachments)
	}
	expectProblem(t, upload("big.bin", bytes.Repeat([]byte("x"), 20<<20+1)), 400, "attachment_too_large")
	if _, err := b.h.Pool().Exec(t.Context(), "DELETE FROM accounting.attachments"); err == nil {
		t.Error("an attachment was deleted")
	}
}

// Concurrent posts take gap-free numbers, one fiscal year's series each.
func TestJournal_ConcurrentPostsAreNumberedWithoutGaps(t *testing.T) {
	b := newBooks(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := journal(b.c, "2026-05-0"+fmt.Sprint(1+i%5), "concurrent", line{"accountId": b.acc("6300"), "amount": "1"}, line{"accountId": b.acc("1920"), "amount": "-1"})
			if res.Status != http.StatusCreated {
				t.Errorf("post = %d %s", res.Status, res.Body)
			}
		}()
	}
	wg.Wait()
	if n := b.h.Count(t, "SELECT count(DISTINCT number) FROM accounting.vouchers WHERE number BETWEEN 1 AND 8"); n != 8 {
		t.Errorf("%d distinct numbers 1..8", n)
	}
}
