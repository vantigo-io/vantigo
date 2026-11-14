package accounting_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Delivery C: expenses post themselves (A20, R2). A fake of Expenses' journal
// feeds the catch-up's third stream; each test reads the vouchers back to the
// øre.

// fakeExpenses is contracts.ExpenseJournal: the facts a test adds, in seq
// order, and the receipts of each expense.
type fakeExpenses struct {
	mu       sync.Mutex
	facts    []contracts.ExpenseFact
	receipts map[int64][]contracts.ExpenseAttachment
	bytes    map[int64][]byte
}

func (j *fakeExpenses) add(f contracts.ExpenseFact) contracts.ExpenseFact {
	j.mu.Lock()
	defer j.mu.Unlock()
	f.Seq = int64(len(j.facts)) + 1
	if f.UserID == uuid.Nil {
		f.UserID = uuid.MustParse("00000000-0000-0000-0000-000000000042")
	}
	j.facts = append(j.facts, f)
	return f
}

func (j *fakeExpenses) Facts(_ context.Context, after int64, limit int) ([]contracts.ExpenseFact, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []contracts.ExpenseFact{}
	for _, f := range j.facts {
		if f.Seq > after && len(out) < limit {
			out = append(out, f)
		}
	}
	return out, nil
}

func (j *fakeExpenses) Attachments(_ context.Context, entry int64) ([]contracts.ExpenseAttachment, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.receipts[entry], nil
}

func (j *fakeExpenses) OpenAttachment(_ context.Context, entry, id int64) (io.ReadCloser, contracts.ExpenseAttachment, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, a := range j.receipts[entry] {
		if a.ID == id {
			return io.NopCloser(strings.NewReader(string(j.bytes[id]))), a, nil
		}
	}
	return nil, contracts.ExpenseAttachment{}, contracts.ErrExpenseAttachmentNotFound
}

// expenseBooks is books with Expenses' journal composed.
func expenseBooks(t *testing.T) (*books, *fakeExpenses) {
	t.Helper()
	j := &fakeExpenses{receipts: map[int64][]contracts.ExpenseAttachment{}, bytes: map[int64][]byte{}}
	h := newHarness(t, modtest.WithExpenseJournal(j))
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	return &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}, j
}

// outlay is a standalone expense of 2026-03-10.
func outlay(id int64, paidBy, gross, vat string, category *int32) contracts.ExpenseLine {
	return contracts.ExpenseLine{EntryID: id, Kind: contracts.ExpenseKindOutlay, Date: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		CategoryID: category, CategoryName: "Materials", PaidBy: paidBy, Currency: "NOK", Gross: gross, VAT: vat}
}

// entryFact is a fact of a standalone expense.
func entryFact(occurrence string, line contracts.ExpenseLine) contracts.ExpenseFact {
	return contracts.ExpenseFact{Occurrence: occurrence, Subject: contracts.SubjectEntry, SubjectID: line.EntryID, Date: line.Date,
		Lines: []contracts.ExpenseLine{line}}
}

// expenseVoucher is the voucher a fact posted as, through the API.
func expenseVoucher(t *testing.T, b *books, f contracts.ExpenseFact) (gen.AccountingVoucher, bool) {
	t.Helper()
	vid := b.h.Count(t, `SELECT coalesce(max(id), 0)::int FROM accounting.vouchers
		WHERE source_module = 'expenses' AND source_kind = $1 AND source_id = $2 AND source_part = $3`,
		f.Subject+"."+f.Occurrence, f.SubjectID, fmt.Sprint(f.Seq))
	if vid == 0 {
		return gen.AccountingVoucher{}, false
	}
	return voucherByID(t, b, int64(vid)), true
}

func expectExpense(t *testing.T, b *books, f contracts.ExpenseFact, want ...string) gen.AccountingVoucher {
	t.Helper()
	v, ok := expenseVoucher(t, b, f)
	if !ok {
		t.Fatalf("no voucher for %s %d (seq %d)", f.Subject+"."+f.Occurrence, f.SubjectID, f.Seq)
	}
	expectPostings(t, fmt.Sprintf("%s.%s %d", f.Subject, f.Occurrence, f.SubjectID), v, want...)
	return v
}

// expenseRule adds an expense rule for a category.
func expenseRule(t *testing.T, b *books, category int32, account string, deductible bool) gen.AccountingPostingRule {
	t.Helper()
	res := b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": fmt.Sprintf("Category %d", category),
		"scope": "expense", "match": "expense_category", "expenseCategoryId": category, "accountId": b.acc(account),
		"vatDeductible": deductible})
	if res.Status != http.StatusCreated {
		t.Fatalf("expense rule = %d %s", res.Status, res.Body)
	}
	var r gen.AccountingPostingRule
	res.JSON(&r)
	return r
}

// unposted is the rows under Needs attention.
func unposted(t *testing.T, b *books, open bool) []gen.AccountingUnposted {
	t.Helper()
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/unposted?open=%v", base, open), nil)
	var list gen.AccountingUnpostedList
	res.JSON(&list)
	return list.Items
}

// An employee's outlay (R2): with a deductible rule the VAT that matches 25 %
// is deducted with code 1; with none it posts gross on 7700 and says so,
// without blocking; a company-paid outlay credits the bank; reimbursed, the
// employee is paid what the approval owed; undone, the reimbursement turns
// back.
func TestExpenses_OutlaysVATAndReimbursement(t *testing.T) {
	b, j := expenseBooks(t)
	materials, meals := int32(1001), int32(1006)
	rule := expenseRule(t, b, materials, "6300", true)
	expenseRule(t, b, meals, "7350", false)

	deducted := j.add(entryFact(contracts.ExpenseApproved, outlay(11, "employee", "1250.00", "250.00", &materials)))
	unmapped := j.add(entryFact(contracts.ExpenseApproved, outlay(12, "employee", "1250.00", "250.00", nil)))
	company := j.add(entryFact(contracts.ExpenseApproved, outlay(13, "company", "500.00", "", &materials)))
	entertainment := j.add(entryFact(contracts.ExpenseApproved, outlay(14, "employee", "625.00", "125.00", &meals)))
	catchUp(t, b.h)

	v := expectExpense(t, b, deducted, "6300 1000.00 1 base", "2710 250.00 1 tax", "2910 -1250.00")
	if v.Kind != "expense" || *v.Rule != "expense.approved" || v.Postings[0].PostingRuleId == nil || *v.Postings[0].PostingRuleId != rule.Id {
		t.Fatalf("voucher = %+v", v)
	}
	expectExpense(t, b, unmapped, "7700 1250.00", "2910 -1250.00")
	expectExpense(t, b, company, "6300 500.00", "1920 -500.00")
	expectExpense(t, b, entertainment, "7350 625.00", "2910 -625.00")

	// The unmapped one deducted nothing and says so; it never blocks.
	rows := unposted(t, b, true)
	if len(rows) != 0 {
		t.Fatalf("open rows = %+v, want none: the unmapped category falls back to 7700 without deduction", rows)
	}

	reimbursed := j.add(contracts.ExpenseFact{Occurrence: contracts.ExpenseReimbursed, Subject: contracts.SubjectEntry, SubjectID: 11,
		Date: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), ReimbursementReference: "Lønn mars", Lines: deducted.Lines})
	catchUp(t, b.h)
	rv := expectExpense(t, b, reimbursed, "2910 1250.00", "1920 -1250.00")
	if rv.Kind != "reimbursement" || rv.VoucherDate.String() != "2026-03-31" {
		t.Fatalf("reimbursement = %+v", rv)
	}
	undone := j.add(contracts.ExpenseFact{Occurrence: contracts.ExpenseReimbursementUndone, Subject: contracts.SubjectEntry, SubjectID: 11,
		Date: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), UndoesSeq: reimbursed.Seq, Lines: deducted.Lines})
	catchUp(t, b.h)
	uv := expectExpense(t, b, undone, "2910 -1250.00", "1920 1250.00")
	if uv.Reverses == nil || uv.Reverses.Id != rv.Id {
		t.Fatalf("undo = %+v", uv)
	}
	if b.h.Count(t, "SELECT count(*) FROM accounting.unposted WHERE posted_at IS NULL AND resolved_at IS NULL") != 0 {
		t.Fatal("something waits")
	}
}

// A deductible category whose VAT matches no rate posts gross and is listed
// as vat_not_deducted, an informational row: it posted, it is not counted with
// the rows that wait, and it is acknowledged rather than retried. The match
// is within one øre per hundred kroner, at least one øre.
func TestExpenses_VATInferenceBoundaries(t *testing.T) {
	b, j := expenseBooks(t)
	cat := int32(1001)
	expenseRule(t, b, cat, "6300", true)
	cases := []struct {
		gross, vat string
		want       []string
	}{
		{"125010.00", "25010.00", []string{"6300 100000.00 1 base", "2710 25010.00 1 tax", "2910 -125010.00"}},
		{"125010.02", "25010.02", []string{"6300 125010.02", "2910 -125010.02"}},
		{"100.01", "20.01", []string{"6300 80.00 1 base", "2710 20.01 1 tax", "2910 -100.01"}},
		{"100.02", "20.02", []string{"6300 100.02", "2910 -100.02"}},
		{"115.00", "15.00", []string{"6300 100.00 11 base", "2711 15.00 11 tax", "2910 -115.00"}},
		{"112.00", "12.00", []string{"6300 100.00 13 base", "2712 12.00 13 tax", "2910 -112.00"}},
		{"111.11", "11.11", []string{"6300 100.00 12 base", "2713 11.11 12 tax", "2910 -111.11"}},
	}
	var facts []contracts.ExpenseFact
	for i, c := range cases {
		facts = append(facts, j.add(entryFact(contracts.ExpenseApproved, outlay(int64(100+i), "employee", c.gross, c.vat, &cat))))
	}
	catchUp(t, b.h)
	for i, c := range cases {
		expectExpense(t, b, facts[i], c.want...)
	}
	rows := unposted(t, b, true)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the two not deducted", rows)
	}
	for _, r := range rows {
		if r.Reason != "vat_not_deducted" || !r.Informational || r.PostedVoucher == nil {
			t.Fatalf("row = %+v", r)
		}
	}
	var meta gen.AccountingMeta
	b.c.Do(http.MethodGet, base+"/meta", nil).JSON(&meta)
	if meta.Unposted != 0 || meta.Informational != 2 || !meta.ExpensesEnabled {
		t.Fatalf("meta = %+v", meta)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/retry", base, rows[0].Id), nil), http.StatusConflict,
		"unposted_already_posted")
	if res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/acknowledge", base, rows[0].Id), nil); res.Status != http.StatusOK {
		t.Fatalf("acknowledge = %d %s", res.Status, res.Body)
	}
	if len(unposted(t, b, true)) != 1 {
		t.Fatal("an acknowledged row is still listed")
	}
}

// Approve, unapprove, approve (R2): three vouchers — the cost, its reversal
// line for line, the cost again — each with the fact's seq as its source
// part; a fact delivered twice is still one voucher.
func TestExpenses_ApproveUnapproveApprove(t *testing.T) {
	b, j := expenseBooks(t)
	cat := int32(1001)
	expenseRule(t, b, cat, "6300", true)
	first := j.add(entryFact(contracts.ExpenseApproved, outlay(21, "employee", "125.00", "25.00", &cat)))
	undo := entryFact(contracts.ExpenseUnapproved, outlay(21, "employee", "125.00", "25.00", &cat))
	undo.UndoesSeq = first.Seq
	undo = j.add(undo)
	again := j.add(entryFact(contracts.ExpenseApproved, outlay(21, "employee", "250.00", "50.00", &cat)))
	catchUp(t, b.h)
	cost := expectExpense(t, b, first, "6300 100.00 1 base", "2710 25.00 1 tax", "2910 -125.00")
	rev := expectExpense(t, b, undo, "6300 -100.00 1 base", "2710 -25.00 1 tax", "2910 125.00")
	if rev.Reverses == nil || rev.Reverses.Id != cost.Id || *rev.Rule != "expense.unapproved" {
		t.Fatalf("reversal = %+v", rev)
	}
	expectExpense(t, b, again, "6300 200.00 1 base", "2710 50.00 1 tax", "2910 -250.00")
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vouchers WHERE source_module = 'expenses'"); n != 3 {
		t.Fatalf("%d vouchers, want 3", n)
	}
	// The cursor put back delivers every fact again: nothing new is posted.
	b.h.Exec(t, "UPDATE accounting.cursors SET position = 0 WHERE stream = 'expenses.facts'")
	catchUp(t, b.h)
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vouchers WHERE source_module = 'expenses'"); n != 3 {
		t.Fatalf("%d vouchers after a redelivery, want 3", n)
	}
}

// A travel claim (R2): one fact for its every line — mileage and per diem
// owed the employee with no VAT, whatever the line says — and its
// reimbursement pays the sum.
func TestExpenses_ClaimsMileageAndPerDiem(t *testing.T) {
	b, j := expenseBooks(t)
	d := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	project := int32(77)
	claim := j.add(contracts.ExpenseFact{Occurrence: contracts.ExpenseApproved, Subject: contracts.SubjectClaim, SubjectID: 5, Date: d,
		Lines: []contracts.ExpenseLine{
			{EntryID: 31, Kind: contracts.ExpenseKindMileage, Date: d, Currency: "NOK", Gross: "530.00", VAT: "106.00", ProjectID: &project},
			{EntryID: 32, Kind: contracts.ExpenseKindPerDiem, Date: d, Currency: "NOK", Gross: "736.00"},
		}})
	catchUp(t, b.h)
	v := expectExpense(t, b, claim, "7700 530.00", "2910 -530.00", "7700 736.00", "2910 -736.00")
	if v.Postings[0].ProjectId == nil || *v.Postings[0].ProjectId != project {
		t.Fatalf("the project is not the posting's: %+v", v.Postings[0])
	}
	paid := j.add(contracts.ExpenseFact{Occurrence: contracts.ExpenseReimbursed, Subject: contracts.SubjectClaim, SubjectID: 5,
		Date: d.AddDate(0, 0, 10), Lines: claim.Lines})
	catchUp(t, b.h)
	expectExpense(t, b, paid, "2910 1266.00", "1920 -1266.00")
}

// An expense in another currency (R2) waits for its amount in NOK, and what
// follows it waits behind it; entered, it posts with the amount, and so does
// its reimbursement from the cost's own payable.
func TestExpenses_AnotherCurrencyWaitsForItsNOK(t *testing.T) {
	b, j := expenseBooks(t)
	cat := int32(1001)
	expenseRule(t, b, cat, "6300", true)
	line := outlay(41, "employee", "80.00", "16.00", &cat)
	line.Currency = "EUR"
	approved := j.add(entryFact(contracts.ExpenseApproved, line))
	paid := j.add(contracts.ExpenseFact{Occurrence: contracts.ExpenseReimbursed, Subject: contracts.SubjectEntry, SubjectID: 41,
		Date: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC), Lines: []contracts.ExpenseLine{line}})
	catchUp(t, b.h)
	rows := unposted(t, b, true)
	if len(rows) != 2 || rows[0].Reason != "amount_in_nok_needed" || rows[1].Reason != "waiting_on" || *rows[1].WaitingOn != rows[0].Id {
		t.Fatalf("rows = %+v", rows)
	}
	if len(rows[0].ExpenseLines) != 1 || rows[0].ExpenseLines[0].Currency != "EUR" || rows[0].ExpenseLines[0].Gross != "80.00" {
		t.Fatalf("expense lines = %+v", rows[0].ExpenseLines)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/nok-amounts", base, rows[1].Id),
		map[string]any{"amounts": []map[string]any{{"entryId": 41, "amountNok": "920.00"}}}), http.StatusConflict, "unposted_wrong_reason")
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/nok-amounts", base, rows[0].Id),
		map[string]any{"amounts": []map[string]any{{"entryId": 41, "amountNok": "920.00"}}})
	if res.Status != http.StatusOK {
		t.Fatalf("enter NOK = %d %s", res.Status, res.Body)
	}
	expectExpense(t, b, approved, "6300 920.00", "2910 -920.00")
	expectExpense(t, b, paid, "2910 920.00", "1920 -920.00")
	if len(unposted(t, b, true)) != 0 {
		t.Fatal("rows still open")
	}
}

// Expenses' own supplier_invoice kind (R2) is not posted: it waits as
// enter_as_supplier_invoice, and Make a supplier invoice opens a draft with
// its supplier (made when there is none), amount, inferred code, date,
// project and receipts, and resolves the row. Unapproved before it posted,
// such a row is resolved with nothing to post.
func TestExpenses_TheSupplierInvoiceKind(t *testing.T) {
	b, j := expenseBooks(t)
	cat := int32(1001)
	expenseRule(t, b, cat, "6300", true)
	project := int32(9)
	due := time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC)
	si := contracts.ExpenseLine{EntryID: 51, Kind: contracts.ExpenseKindSupplierInvoice, Date: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		CategoryID: &cat, CategoryName: "Materials", PaidBy: "company", Currency: "NOK", Gross: "1250.00", VAT: "250.00",
		ProjectID: &project, Supplier: "Byggmakker AS", SupplierInvoiceNumber: "77001", SupplierDueDate: &due}
	j.receipts[51] = []contracts.ExpenseAttachment{{ID: 900, EntryID: 51, FileName: "faktura.pdf", ContentType: "application/pdf", SizeBytes: 9}}
	j.bytes[900] = []byte("%PDF-1.4 x")
	j.add(entryFact(contracts.ExpenseApproved, si))
	catchUp(t, b.h)
	rows := unposted(t, b, true)
	if len(rows) != 1 || rows[0].Reason != "enter_as_supplier_invoice" {
		t.Fatalf("rows = %+v", rows)
	}
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/supplier-invoice", base, rows[0].Id), nil)
	if res.Status != http.StatusCreated {
		t.Fatalf("make = %d %s", res.Status, res.Body)
	}
	var inv gen.AccountingSupplierInvoice
	res.JSON(&inv)
	if inv.State != "draft" || inv.SupplierName != "Byggmakker AS" || inv.InvoiceNumber != "77001" || inv.Total != "1250.00" ||
		inv.DueDate.String() != "2026-04-09" || len(inv.Lines) != 1 || inv.Lines[0].TaxCode == nil || *inv.Lines[0].TaxCode != "1" ||
		inv.Lines[0].AccountNumber != "6300" || *inv.Lines[0].ProjectId != 9 || len(inv.Files) != 1 || *inv.SourceExpenseEntryId != 51 {
		t.Fatalf("draft = %+v", inv)
	}
	if len(unposted(t, b, true)) != 0 {
		t.Fatal("the row is still open")
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/unposted/%d/supplier-invoice", base, rows[0].Id), nil),
		http.StatusConflict, "unposted_wrong_reason")
	expectPostings(t, "the draft posted", voucherByID(t, b, postInvoiceAsIs(t, b, inv.Id).Voucher.Id),
		"2400 -1250.00", "6300 1000.00 1 base", "2710 250.00 1 tax")

	// Unapproved before it was made into a draft: resolved, nothing posted.
	other := si
	other.EntryID, other.SupplierInvoiceNumber = 52, "77002"
	approved := j.add(entryFact(contracts.ExpenseApproved, other))
	undo := entryFact(contracts.ExpenseUnapproved, other)
	undo.UndoesSeq = approved.Seq
	j.add(undo)
	catchUp(t, b.h)
	if len(unposted(t, b, true)) != 0 || b.h.Count(t, "SELECT count(*) FROM accounting.vouchers WHERE source_module = 'expenses'") != 0 {
		t.Fatal("an unapproved supplier invoice kind left something")
	}
}

// postInvoiceAsIs posts a draft that has its files already.
func postInvoiceAsIs(t *testing.T, b *books, id int64) gen.AccountingSupplierInvoice {
	t.Helper()
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/post", base, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("post = %d %s", res.Status, res.Body)
	}
	var inv gen.AccountingSupplierInvoice
	res.JSON(&inv)
	return inv
}

// A fact dated before the books start is skipped, and the cursor moves past
// it; and with Expenses off (no journal) nothing breaks: the stream is not
// run, the meta says so, and the sales go on.
func TestExpenses_BeforeTheBooksAndExpensesOff(t *testing.T) {
	b, j := expenseBooks(t)
	early := outlay(61, "employee", "100.00", "", nil)
	early.Date = time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	f := entryFact(contracts.ExpenseApproved, early)
	j.add(f)
	catchUp(t, b.h)
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.vouchers"); n != 0 {
		t.Fatalf("%d vouchers, want none", n)
	}
	if p := position(t, b, "expenses.facts"); p != 1 {
		t.Fatalf("position = %d, want past the early fact", p)
	}

	off := newBooks(t)
	catchUp(t, off.h)
	var meta gen.AccountingMeta
	off.c.Do(http.MethodGet, base+"/meta", nil).JSON(&meta)
	if meta.ExpensesEnabled {
		t.Fatal("expenses enabled without a journal")
	}
	var status gen.AccountingCatchUp
	off.c.Do(http.MethodGet, base+"/catch-up", nil).JSON(&status)
	if len(status.Streams) != 3 || deref(status.Streams[2].Behind) != 0 {
		t.Fatalf("status = %+v", status)
	}
	if off.h.Count(t, "SELECT count(*) FROM accounting.cursors WHERE stream = 'expenses.facts'") != 0 {
		t.Fatal("the expenses stream ran without Expenses")
	}
}

// An expense rule (A14, R2) names an expense account; its deductibility is a
// version of its own.
func TestPostingRules_ExpenseScope(t *testing.T) {
	b, _ := expenseBooks(t)
	expectProblem(t, b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Wrong kind", "scope": "expense",
		"match": "expense_category", "expenseCategoryId": 1001, "accountId": b.acc("3000")}), http.StatusBadRequest, "account_kind_mismatch")
	if res := b.c.Do(http.MethodPost, base+"/posting-rules", map[string]any{"name": "Wrong match", "scope": "expense",
		"match": "tax_code", "taxCode": "1", "accountId": b.acc("6300")}); res.Status != http.StatusBadRequest {
		t.Fatalf("an expense rule on a tax code = %d", res.Status)
	}
	r := expenseRule(t, b, 1001, "6300", true)
	if r.Scope != "expense" || r.VatDeductible == nil || !*r.VatDeductible || r.Version != 1 || *r.ExpenseCategoryId != 1001 {
		t.Fatalf("rule = %+v", r)
	}
	res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/posting-rules/%d", base, r.Id), map[string]any{"name": r.Name,
		"accountId": r.AccountId, "revision": r.Revision, "vatDeductible": false})
	if res.Status != http.StatusOK {
		t.Fatalf("update = %d %s", res.Status, res.Body)
	}
	res.JSON(&r)
	if *r.VatDeductible || r.Version != 2 {
		t.Fatalf("after the change = %+v", r)
	}
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.posting_rule_versions WHERE rule_id = $1 AND vat_deductible = false", r.Id); n != 1 {
		t.Fatalf("%d versions keep the change", n)
	}
}
