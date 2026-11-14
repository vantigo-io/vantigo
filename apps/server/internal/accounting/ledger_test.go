package accounting_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The opening balances (A9, R5): one voucher dated books_start, one open item
// per document, the difference on the opening account, Invoices' open
// invoices offered; reversed and entered again until the first lock.
func TestOpening(t *testing.T) {
	b := newBooks(t)
	b.h.journal.open = []contracts.OpenDocument{{ID: 77, Number: 1042, CustomerID: customerAcme,
		IssueDate: time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), DueDate: time.Date(2025, 12, 15, 0, 0, 0, 0, time.UTC), OpenNOK: "1250.00"}}
	res := b.c.Do(http.MethodGet, base+"/opening", nil)
	var o gen.AccountingOpening
	res.JSON(&o)
	if !o.CanEnter || o.Voucher != nil || len(o.OpenInvoices) != 1 || o.OpenInvoices[0].CustomerName != "Acme AS" || o.OpenInvoices[0].Amount != "1250.00" {
		t.Fatalf("opening = %+v", o)
	}
	if got := b.h.journal.asked[0].Format(time.DateOnly); got != "2025-12-31" {
		t.Errorf("OpenAt asked for %s, want the day before the books start", got)
	}
	body := map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1920"), "amount": "10000"},
		{"accountId": b.acc("1500"), "amount": "1250", "customerId": customerAcme, "documentNumber": "1042", "documentDate": "2025-12-01",
			"dueDate": "2025-12-15", "sourceDocumentId": 77},
		{"accountId": b.acc("1500"), "amount": "300", "customerId": customerBeta, "documentNumber": "A-7"},
		{"accountId": b.acc("2000"), "amount": "-30000"},
	}}
	res = b.c.Do(http.MethodPost, base+"/opening", body)
	if res.Status != http.StatusCreated {
		t.Fatalf("opening = %d %s", res.Status, res.Body)
	}
	var v gen.AccountingVoucher
	res.JSON(&v)
	if v.Kind != "opening" || v.VoucherDate.String() != "2026-01-01" || len(v.Postings) != 5 {
		t.Fatalf("opening voucher = %+v", v)
	}
	if last := v.Postings[4]; last.AccountNumber != "2060" || last.Amount != "18450.00" {
		t.Errorf("the difference = %+v", last)
	}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/opening", body), http.StatusConflict, "opening_exists")
	res = b.c.Do(http.MethodGet, base+"/open-items?kind=customer", nil)
	var items gen.AccountingOpenItemList
	res.JSON(&items)
	if len(items.Items) != 2 {
		t.Fatalf("open items = %+v", items)
	}
	acme := items.Items[0]
	if acme.DocumentNumber == nil || *acme.DocumentNumber != "1042" || acme.SourceDocumentId == nil || acme.Balance != "1250.00" ||
		acme.DueDate.String() != "2025-12-15" || *acme.CustomerName != "Acme AS" {
		t.Errorf("Acme's item = %+v", acme)
	}

	// Reverse and enter again: the opening is dated books_start either way.
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, v.Id), map[string]any{"date": "2026-05-05", "reason": "Wrong equity"})
	var r gen.AccountingVoucher
	res.JSON(&r)
	if res.Status != http.StatusCreated || r.VoucherDate.String() != "2026-01-01" {
		t.Fatalf("reverse the opening = %d %s", res.Status, res.Body)
	}
	res = b.c.Do(http.MethodGet, base+"/opening", nil)
	res.JSON(&o)
	if !o.CanEnter || o.Voucher != nil {
		t.Errorf("after the reversal = %+v", o)
	}
	res = b.c.Do(http.MethodPost, base+"/opening", body)
	res.JSON(&v)
	if res.Status != http.StatusCreated {
		t.Fatalf("enter again = %d %s", res.Status, res.Body)
	}
	if n := b.h.Count(t, "SELECT count(*) FROM accounting.open_items WHERE source_document_id = 77"); n != 1 {
		t.Errorf("%d items for document 77, want the one", n)
	}
	// After the first lock the opening is final.
	if got := b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-01-31"}).Status; got != http.StatusOK {
		t.Fatalf("lock = %d", got)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, v.Id), map[string]any{"date": "2026-05-05", "reason": "x"}),
		http.StatusConflict, "opening_locked")
	res = b.c.Do(http.MethodGet, base+"/opening", nil)
	res.JSON(&o)
	if o.CanEnter || !o.Locked || o.Voucher == nil || o.Voucher.Id != v.Id {
		t.Errorf("locked opening = %+v", o)
	}
}

// Settlements (R5): a receipt, a payment or a refund against an item, never
// more than its balance, and never on an item that is Invoices' document.
func TestSettlements(t *testing.T) {
	b := newBooks(t)
	b.h.journal.open = []contracts.OpenDocument{{ID: 1001, Number: 1001, CustomerID: customerBeta, OpenNOK: "500.00"}}
	res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1500"), "amount": "1000", "customerId": customerAcme, "documentNumber": "A-1", "dueDate": "2026-01-20"},
		{"accountId": b.acc("1500"), "amount": "-200", "customerId": customerBeta, "documentNumber": "CN-3"},
		{"accountId": b.acc("1500"), "amount": "500", "customerId": customerBeta, "documentNumber": "1001", "sourceDocumentId": 1001},
	}})
	if res.Status != http.StatusCreated {
		t.Fatalf("opening = %d %s", res.Status, res.Body)
	}
	items := map[string]gen.AccountingOpenItem{}
	res = b.c.Do(http.MethodGet, base+"/open-items", nil)
	var list gen.AccountingOpenItemList
	res.JSON(&list)
	for _, it := range list.Items {
		items[*it.DocumentNumber] = it
	}
	settle := func(item gen.AccountingOpenItem, body map[string]any) *gen.AccountingVoucher {
		res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/open-items/%d/settlements", base, item.Id), body)
		if res.Status != http.StatusCreated {
			return nil
		}
		var v gen.AccountingVoucher
		res.JSON(&v)
		return &v
	}
	v := settle(items["A-1"], map[string]any{"date": "2026-02-01", "amount": "400"})
	if v == nil || v.Kind != "customer_payment" || v.Postings[0].AccountNumber != "1500" || v.Postings[0].Amount != "-400.00" ||
		v.Postings[1].AccountNumber != "1920" || v.Postings[1].Amount != "400.00" {
		t.Fatalf("receipt = %+v", v)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/open-items/%d/settlements", base, items["A-1"].Id),
		map[string]any{"date": "2026-02-01", "amount": "600.01"}), http.StatusConflict, "settlement_exceeds_balance")
	refund := settle(items["CN-3"], map[string]any{"date": "2026-02-02", "amount": "200", "bankAccountId": b.acc("1900"), "text": "Refund"})
	if refund == nil || refund.Postings[0].Amount != "200.00" || refund.Postings[1].AccountNumber != "1900" || refund.Postings[1].Amount != "-200.00" {
		t.Fatalf("refund = %+v", refund)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/open-items/%d/settlements", base, items["CN-3"].Id),
		map[string]any{"date": "2026-02-02", "amount": "1"}), http.StatusConflict, "settlement_settled")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/open-items/%d/settlements", base, items["1001"].Id),
		map[string]any{"date": "2026-02-02", "amount": "1"}), http.StatusConflict, "settlement_source_document")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/open-items/%d/settlements", base, items["A-1"].Id),
		map[string]any{"date": "2026-02-02", "amount": "1", "bankAccountId": b.acc("3000")}), 400, "bank_account_invalid")
	res = b.c.Do(http.MethodGet, base+"/open-items?open=false&customerId="+fmt.Sprint(customerBeta), nil)
	res.JSON(&list)
	if len(list.Items) != 2 {
		t.Errorf("Beta's items, settled too = %+v", list.Items)
	}
	res = b.c.Do(http.MethodGet, base+"/open-items?customerId="+fmt.Sprint(customerBeta), nil)
	res.JSON(&list)
	if len(list.Items) != 1 || *list.Items[0].DocumentNumber != "1001" {
		t.Errorf("Beta's open items = %+v", list.Items)
	}
}

// reportOf is a report's rows by a key cell.
func reportOf(t *testing.T, b *books, path string) gen.AccountingReport {
	t.Helper()
	res := b.c.Do(http.MethodGet, base+"/reports/"+path, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("report %s = %d %s", path, res.Status, res.Body)
	}
	var r gen.AccountingReport
	res.JSON(&r)
	return r
}

func rowWith(r gen.AccountingReport, key, value string) *gen.AccountingReportRow {
	for i := range r.Rows {
		if r.Rows[i].Cells[key] == value {
			return &r.Rows[i]
		}
	}
	return nil
}

// Every report against one small fixed ledger (A11).
func TestReports(t *testing.T) {
	b := newBooks(t)
	// Opening: 30 000 share capital in the bank.
	res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1920"), "amount": "30000"}, {"accountId": b.acc("2000"), "amount": "-30000"}}})
	if res.Status != http.StatusCreated {
		t.Fatalf("opening = %d %s", res.Status, res.Body)
	}
	// February: a sale on credit with VAT; March: rent paid; a payment.
	mustPost(t, b.c, "2026-02-10", "Sale to Acme", line{"accountId": b.acc("1500"), "amount": "1250", "customerId": customerAcme},
		line{"accountId": b.acc("3000"), "amount": "-1000", "taxCodeId": b.code("3"), "projectId": 9})
	mustPost(t, b.c, "2026-03-01", "Rent", line{"accountId": b.acc("6300"), "amount": "800"}, line{"accountId": b.acc("1920"), "amount": "-800"})
	mustPost(t, b.c, "2026-03-20", "Acme pays", line{"accountId": b.acc("1920"), "amount": "250"},
		line{"accountId": b.acc("1500"), "amount": "-250", "customerId": customerAcme})

	journal := reportOf(t, b, "journal?from=2026-01-01&to=2026-12-31")
	if journal.Title != "Bokføringsspesifikasjon" || len(journal.Rows) != 10 {
		t.Fatalf("journal = %d rows: %+v", len(journal.Rows), journal.Rows)
	}
	if total := journal.Rows[len(journal.Rows)-1]; total.Style != "total" || total.Cells["debit"] != "32300.00" || total.Cells["credit"] != "32300.00" {
		t.Errorf("journal total = %+v", total.Cells)
	}
	if r := reportOf(t, b, "journal?from=2026-01-01&to=2026-12-31&projectId=9"); len(r.Rows) != 3 {
		t.Errorf("journal on project 9 = %+v", r.Rows)
	}

	bank := reportOf(t, b, fmt.Sprintf("account?from=2026-03-01&to=2026-03-31&accountId=%d", b.acc("1920")))
	if bank.Rows[0].Cells["balance"] != "30000.00" || bank.Rows[len(bank.Rows)-1].Cells["balance"] != "29450.00" || len(bank.Rows) != 4 {
		t.Errorf("bank in March = %+v", bank.Rows)
	}
	expectProblem(t, b.c.Do(http.MethodGet, base+"/reports/account?from=2026-01-01&to=2026-12-31", nil), 400, "report_account_required")
	expectProblem(t, b.c.Do(http.MethodGet, base+"/reports/journal?from=2026-12-31&to=2026-01-01", nil), 400, "period_invalid")

	customers := reportOf(t, b, "customers?from=2026-01-01&to=2026-04-30")
	if row := rowWith(customers, "party", fmt.Sprintf("%d Acme AS", customerAcme)); row == nil || row.Style != "heading" {
		t.Errorf("customers report = %+v", customers.Rows)
	}
	if total := customers.Rows[len(customers.Rows)-1]; total.Cells["balance"] != "1000.00" || total.Cells["notDue"] != "1000.00" {
		t.Errorf("customers total = %+v", total.Cells)
	}
	if r := reportOf(t, b, "suppliers?from=2026-01-01&to=2026-04-30"); len(r.Rows) != 1 {
		t.Errorf("suppliers = %+v", r.Rows)
	}

	trial := reportOf(t, b, "trial_balance?from=2026-03-01&to=2026-03-31&compareFrom=2026-02-01&compareTo=2026-02-28")
	if row := rowWith(trial, "account", "1920"); row == nil || row.Cells["opening"] != "30000.00" || row.Cells["debit"] != "250.00" ||
		row.Cells["credit"] != "800.00" || row.Cells["closing"] != "29450.00" || row.Cells["compare"] != "30000.00" {
		t.Errorf("trial balance 1920 = %+v", row)
	}
	if total := trial.Rows[len(trial.Rows)-1]; total.Cells["opening"] != "0.00" || total.Cells["closing"] != "0.00" {
		t.Errorf("trial balance total = %+v", total.Cells)
	}

	pl := reportOf(t, b, "profit_and_loss?from=2026-01-01&to=2026-12-31&compareFrom=2025-01-01&compareTo=2025-12-31")
	if row := rowWith(pl, "account", "3000"); row == nil || row.Cells["amount"] != "1000.00" || row.Cells["compare"] != "0.00" {
		t.Errorf("P&L 3000 = %+v", row)
	}
	if row := rowWith(pl, "name", "Resultat"); row == nil || row.Cells["amount"] != "200.00" {
		t.Errorf("P&L result = %+v", pl.Rows)
	}
	if row := rowWith(pl, "name", "Driftsresultat"); row == nil || row.Cells["amount"] != "200.00" {
		t.Errorf("P&L operating result = %+v", row)
	}

	bs := reportOf(t, b, "balance_sheet?from=2026-01-01&to=2026-12-31")
	assets := rowWith(bs, "name", "Sum eiendeler")
	claims := rowWith(bs, "name", "Sum egenkapital og gjeld")
	result := rowWith(bs, "name", "Årets resultat, ikke avsluttet")
	if assets == nil || claims == nil || result == nil || assets.Cells["amount"] != "30450.00" || claims.Cells["amount"] != "30450.00" ||
		result.Cells["amount"] != "200.00" {
		t.Errorf("balance sheet = %+v", bs.Rows)
	}
	if bs.ProducedAt.IsZero() {
		t.Error("no produced-at stamp")
	}

	// The CSV: a byte order mark, semicolons, the decimal comma.
	res = b.h.SignIn(t, exporter...).Do(http.MethodGet, base+"/reports/trial_balance/csv?from=2026-03-01&to=2026-03-31", nil)
	if res.Status != http.StatusOK || !strings.HasPrefix(string(res.Body), "\ufeffSaldobalanse") || !strings.Contains(string(res.Body), "1920;") ||
		!strings.Contains(string(res.Body), "29450,00") || !strings.Contains(res.Header("Content-Disposition"), "trial-balance-2026-03-01-2026-03-31.csv") {
		t.Errorf("CSV = %d %q %q", res.Status, res.Body, res.Header("Content-Disposition"))
	}
}
