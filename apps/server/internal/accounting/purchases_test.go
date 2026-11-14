package accounting_test

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Delivery C: purchases (A17–A19, R5, R8). Suppliers, supplier invoices from
// draft to voucher with every seeded purchase tax code, foreign currency,
// payments with their currency difference, credit notes, the duplicate
// guard, Reverse and copy, and the files a draft carries to its voucher.

// supplier creates a supplier and answers it.
func supplier(t *testing.T, b *books, body map[string]any) gen.AccountingSupplier {
	t.Helper()
	res := b.c.Do(http.MethodPost, base+"/suppliers", body)
	if res.Status != http.StatusCreated {
		t.Fatalf("create supplier = %d %s", res.Status, res.Body)
	}
	var s gen.AccountingSupplier
	res.JSON(&s)
	return s
}

// draft enters a supplier invoice and answers it.
func draft(t *testing.T, b *books, body map[string]any) gen.AccountingSupplierInvoice {
	t.Helper()
	res := b.c.Do(http.MethodPost, base+"/supplier-invoices", body)
	if res.Status != http.StatusCreated {
		t.Fatalf("create supplier invoice = %d %s", res.Status, res.Body)
	}
	var inv gen.AccountingSupplierInvoice
	res.JSON(&inv)
	return inv
}

// attach uploads a file to a draft.
func attach(t *testing.T, b *books, id int64, name string) *modtest.Response {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", name)
	_, _ = part.Write([]byte("%PDF-1.4 " + name))
	_ = w.Close()
	return b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/files", base, id), nil,
		modtest.RawBody(w.FormDataContentType(), body.Bytes()))
}

// postInvoice attaches a file and posts the draft, which must succeed.
func postInvoice(t *testing.T, b *books, id int64) gen.AccountingSupplierInvoice {
	t.Helper()
	if res := attach(t, b, id, "faktura.pdf"); res.Status != http.StatusCreated {
		t.Fatalf("attach = %d %s", res.Status, res.Body)
	}
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/post", base, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("post = %d %s", res.Status, res.Body)
	}
	var inv gen.AccountingSupplierInvoice
	res.JSON(&inv)
	return inv
}

// voucherByID is a voucher through the API.
func voucherByID(t *testing.T, b *books, id int64) gen.AccountingVoucher {
	t.Helper()
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d", base, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("voucher %d = %d %s", id, res.Status, res.Body)
	}
	var v gen.AccountingVoucher
	res.JSON(&v)
	return v
}

// expectPostings fails unless the voucher has exactly want, in order.
func expectPostings(t *testing.T, what string, v gen.AccountingVoucher, want ...string) {
	t.Helper()
	if got := postingsOf(v); !slices.Equal(got, want) {
		t.Fatalf("%s posted\n  %s\nwant\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func (b *books) aSupplier(t *testing.T) gen.AccountingSupplier {
	t.Helper()
	return supplier(t, b, map[string]any{"name": "Rørlegger Hansen AS", "organisationNumber": "923 609 016",
		"bankAccount": "1234.56.78903", "paymentTermsDays": 30})
}

// Suppliers (A17): numbered gap-free from 1, the organisation number and the
// bank account checked by mod 11, the organisation number unique, changed
// under a revision, deleted only while the books do not name them, and
// exported whole.
func TestSuppliers(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	if s.Number != 1 || s.BankAccount == nil || *s.BankAccount != "12345678903" || *s.OrganisationNumber != "923609016" ||
		s.PaymentTermsDays != 30 || s.Currency != "NOK" || s.Country != "NO" || s.Balance != "0.00" {
		t.Fatalf("supplier = %+v", s)
	}
	second := supplier(t, b, map[string]any{"name": "Ola Nordmann", "isPerson": true})
	if second.Number != 2 || !second.IsPerson {
		t.Fatalf("second = %+v", second)
	}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "Again", "organisationNumber": "923609016"}),
		http.StatusConflict, "supplier_organisation_number_taken")
	expectProblem(t, b.c.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "Bad", "bankAccount": "12345678901"}),
		http.StatusBadRequest, "supplier_bank_account_invalid")
	if res := b.c.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "Bad org", "organisationNumber": "923609017"}); res.Status != http.StatusBadRequest {
		t.Fatalf("a bad organisation number = %d", res.Status)
	}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "Sales code", "defaultTaxCodeId": b.code("3")}),
		http.StatusBadRequest, "tax_code_not_purchase")

	res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/suppliers/%d", base, s.Id), map[string]any{"name": "Rørlegger Hansen AS",
		"organisationNumber": "923609016", "email": "post@hansen.no", "paymentTermsDays": 30, "bankAccount": "12345678903",
		"revision": s.Revision})
	if res.Status != http.StatusOK {
		t.Fatalf("update = %d %s", res.Status, res.Body)
	}
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/suppliers/%d", base, s.Id), map[string]any{"name": "Stale",
		"revision": s.Revision}), http.StatusConflict, "revision_conflict")

	// A supplier the books do not name is deleted; one with an invoice is
	// kept, and is made inactive instead.
	if res := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/suppliers/%d", base, second.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Status, res.Body)
	}
	inv := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "F-1", "invoiceDate": "2026-03-01",
		"total": "1250.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("1"), "amount": "1250.00"}}})
	if inv.DueDate == nil || inv.DueDate.String() != "2026-03-31" {
		t.Fatalf("due = %v, want the supplier's 30 days", inv.DueDate)
	}
	postInvoice(t, b, inv.Id)
	expectProblem(t, b.c.Do(http.MethodDelete, fmt.Sprintf("%s/suppliers/%d", base, s.Id), nil), http.StatusConflict, "supplier_has_postings")

	// The export carries the supplier, its invoice and every posting naming it.
	ex := b.h.SignIn(t, exporter...)
	res = ex.Do(http.MethodGet, fmt.Sprintf("%s/suppliers/%d/export", base, s.Id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("export = %d %s", res.Status, res.Body)
	}
	var export gen.AccountingSupplierExport
	res.JSON(&export)
	if len(export.Invoices) != 1 || len(export.Postings) != 1 || export.Postings[0].Amount != "-1250.00" ||
		export.Supplier.Balance != "-1250.00" || export.Supplier.LastVoucherDate == nil {
		t.Fatalf("export = %+v", export)
	}
	if res := b.h.SignIn(t, poster...).Do(http.MethodGet, fmt.Sprintf("%s/suppliers/%d/export", base, s.Id), nil); res.Status != http.StatusForbidden {
		t.Fatalf("export without accounting:export = %d", res.Status)
	}

	// A person supplier is kept five years after its last voucher's fiscal
	// year.
	person := supplier(t, b, map[string]any{"name": "Kari Enkeltperson", "isPerson": true})
	pinv := draft(t, b, map[string]any{"supplierId": person.Id, "kind": "invoice", "invoiceNumber": "1", "invoiceDate": "2026-04-01",
		"total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}})
	postInvoice(t, b, pinv.Id)
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/suppliers/%d", base, person.Id), nil)
	res.JSON(&person)
	if person.RetainedUntil == nil || person.RetainedUntil.String() != "2031-12-31" {
		t.Fatalf("retained until %v, want 2031-12-31", person.RetainedUntil)
	}
}

// Every seeded purchase tax code a supplier invoice takes (A5, A18): a line
// of 1 000.00 entered net, to the øre — the deductible input codes' VAT on
// their VAT account, the code of no VAT treatment as a base alone, reverse
// charge on both sides with the payable at the net (the calculated side
// tax_reverse), the codes without deduction putting the deductible side on
// the cost line, and an output code and the import codes refused.
func TestSupplierInvoices_EveryPurchaseCode(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	cases := []struct {
		code  string
		total string
		want  []string
	}{
		{"1", "1250.00", []string{"2400 -1250.00", "6300 1000.00 1 base", "2710 250.00 1 tax"}},
		{"11", "1150.00", []string{"2400 -1150.00", "6300 1000.00 11 base", "2711 150.00 11 tax"}},
		{"12", "1111.10", []string{"2400 -1111.10", "6300 1000.00 12 base", "2713 111.10 12 tax"}},
		{"13", "1120.00", []string{"2400 -1120.00", "6300 1000.00 13 base", "2712 120.00 13 tax"}},
		{"0", "1000.00", []string{"2400 -1000.00", "6300 1000.00 0 base"}},
		{"86", "1000.00", []string{"2400 -1000.00", "6300 1000.00 86 base", "2715 250.00 86 tax", "2705 -250.00 86 tax_reverse"}},
		{"87", "1000.00", []string{"2400 -1000.00", "6300 1000.00 87 base", "6300 250.00 87 tax", "2705 -250.00 87 tax_reverse"}},
		{"88", "1000.00", []string{"2400 -1000.00", "6300 1000.00 88 base", "2715 120.00 88 tax", "2705 -120.00 88 tax_reverse"}},
		{"89", "1000.00", []string{"2400 -1000.00", "6300 1000.00 89 base", "6300 120.00 89 tax", "2705 -120.00 89 tax_reverse"}},
		{"91", "1000.00", []string{"2400 -1000.00", "6300 1000.00 91 base", "2715 250.00 91 tax", "2705 -250.00 91 tax_reverse"}},
		{"92", "1000.00", []string{"2400 -1000.00", "6300 1000.00 92 base", "6300 250.00 92 tax", "2705 -250.00 92 tax_reverse"}},
	}
	for i, c := range cases {
		inv := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": fmt.Sprintf("K-%s", c.code),
			"invoiceDate": "2026-03-01", "total": c.total,
			"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code(c.code), "amount": "1000.00", "entry": "net"}}})
		if inv.LinesTotal != c.total {
			t.Fatalf("code %s: lines total %s, want %s", c.code, inv.LinesTotal, c.total)
		}
		posted := postInvoice(t, b, inv.Id)
		if posted.State != "posted" || posted.Voucher == nil || posted.Balance == nil || *posted.Balance != "-"+c.total {
			t.Fatalf("code %s: %+v", c.code, posted)
		}
		v := voucherByID(t, b, posted.Voucher.Id)
		expectPostings(t, "code "+c.code, v, c.want...)
		if v.Kind != "supplier_invoice" || v.Rule == nil || *v.Rule != "purchase.supplier_invoice" || len(v.Attachments) != 1 {
			t.Fatalf("code %s: voucher %+v", c.code, v)
		}
		_ = i
	}
	// Gross entry with a deductible code takes the VAT out of the amount.
	inv := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "G-1", "invoiceDate": "2026-03-01",
		"total": "99.99", "lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("1"), "amount": "99.99"}}})
	expectPostings(t, "gross", voucherByID(t, b, postInvoice(t, b, inv.Id).Voucher.Id),
		"2400 -99.99", "6300 79.99 1 base", "2710 20.00 1 tax")

	// An import's codes are a manual journal from the customs declaration
	// (R8, M4): refused on a supplier invoice.
	refusals := []struct{ code, want string }{{"3", "tax_code_not_purchase"}, {"31", "tax_code_not_purchase"}}
	for _, code := range []string{"14", "15", "20", "21", "22", "81", "82", "83", "84", "85"} {
		refusals = append(refusals, struct{ code, want string }{code, "tax_code_not_for_supplier_invoice"})
	}
	for _, refused := range refusals {
		expectProblem(t, b.c.Do(http.MethodPost, base+"/supplier-invoices", map[string]any{"supplierId": s.Id, "kind": "invoice",
			"invoiceNumber": "X-" + refused.code, "invoiceDate": "2026-03-01", "total": "1.00",
			"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code(refused.code), "amount": "1.00"}}}),
			http.StatusBadRequest, refused.want)
	}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/supplier-invoices", map[string]any{"supplierId": s.Id, "kind": "invoice",
		"invoiceNumber": "RC-gross", "invoiceDate": "2026-03-01", "total": "1.00",
		"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("86"), "amount": "1.00", "entry": "gross"}}}),
		http.StatusBadRequest, "reverse_charge_gross")
}

// A purchase in another currency (R8): the rate gives the NOK total; each
// posting carries the currency amount and the rate beside its NOK; a
// reverse-charge VAT is computed on the NOK base. Two part payments in NOK
// as the bank took them settle shares in the currency, the first at a loss
// and the last at a gain, and the item ends at zero in both: paid.
func TestSupplierInvoices_ForeignCurrencyAndPartPayments(t *testing.T) {
	b := newBooks(t)
	s := supplier(t, b, map[string]any{"name": "Cloud GmbH", "country": "DE", "currency": "EUR"})
	inv := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "INV-77", "invoiceDate": "2026-03-01",
		"exchangeRate": "11.5", "total": "100.00",
		"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("86"), "amount": "100.00", "entry": "net"}}})
	if inv.Currency != "EUR" || deref(inv.TotalNok) != "1150.00" || deref(inv.ExchangeRate) != "11.500000" {
		t.Fatalf("draft = %+v", inv)
	}
	posted := postInvoice(t, b, inv.Id)
	v := voucherByID(t, b, posted.Voucher.Id)
	expectPostings(t, "EUR invoice", v, "2400 -1150.00", "6300 1150.00 86 base", "2715 287.50 86 tax", "2705 -287.50 86 tax_reverse")
	if p := v.Postings[0]; p.Currency == nil || *p.Currency != "EUR" || *p.CurrencyAmount != "-100.00" || *p.ExchangeRate != "11.500000" {
		t.Fatalf("payable's currency = %+v", p)
	}
	if v.Postings[2].Currency != nil {
		t.Fatalf("a reverse-charge VAT line carries no currency: %+v", v.Postings[2])
	}

	pay := func(body map[string]any) gen.AccountingSupplierInvoice {
		t.Helper()
		res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, inv.Id), body)
		if res.Status != http.StatusCreated {
			t.Fatalf("pay = %d %s", res.Status, res.Body)
		}
		var out gen.AccountingSupplierInvoice
		res.JSON(&out)
		return out
	}
	after := pay(map[string]any{"date": "2026-03-20", "amountNok": "470.00", "amountCurrency": "40.00"})
	expectPostings(t, "first payment", voucherByID(t, b, after.Payments[0].Voucher.Id), "2400 460.00", "1920 -470.00", "8160 10.00")
	if *after.Balance != "-690.00" || *after.BalanceCurrency != "-60.00" || after.State != "posted" || after.Payments[0].CurrencyDifference != "10.00" {
		t.Fatalf("after the first = %+v", after)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, inv.Id),
		map[string]any{"date": "2026-03-21", "amountNok": "800.00", "amountCurrency": "60.01"}), http.StatusConflict, "payment_exceeds_balance")
	after = pay(map[string]any{"date": "2026-03-25", "amountNok": "680.00"})
	expectPostings(t, "last payment", voucherByID(t, b, after.Payments[1].Voucher.Id), "2400 690.00", "1920 -680.00", "8060 -10.00")
	if *after.Balance != "0.00" || *after.BalanceCurrency != "0.00" || after.State != "paid" {
		t.Fatalf("after the last = %+v", after)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, inv.Id),
		map[string]any{"date": "2026-03-26", "amountNok": "1.00"}), http.StatusConflict, "supplier_invoice_settled")

	// A payment removed posts its voucher reversed, and the invoice is owed
	// again; a second removal is refused.
	res := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/supplier-invoices/%d/payments/%d", base, inv.Id, after.Payments[1].Id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("remove = %d %s", res.Status, res.Body)
	}
	res.JSON(&after)
	if *after.Balance != "-690.00" || after.Payments[1].RemovalVoucher == nil {
		t.Fatalf("after the removal = %+v", after)
	}
	expectPostings(t, "removal", voucherByID(t, b, after.Payments[1].RemovalVoucher.Id), "2400 -690.00", "1920 680.00", "8060 10.00")
	expectProblem(t, b.c.Do(http.MethodDelete, fmt.Sprintf("%s/supplier-invoices/%d/payments/%d", base, inv.Id, after.Payments[1].Id), nil),
		http.StatusConflict, "payment_removed")

	// The NOK total may be given instead of the rate.
	byTotal := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "INV-78", "invoiceDate": "2026-03-01",
		"totalNok": "1234.56", "total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}})
	if deref(byTotal.ExchangeRate) != "12.345600" || deref(byTotal.TotalNok) != "1234.56" {
		t.Fatalf("by total = %+v", byTotal)
	}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/supplier-invoices", map[string]any{"supplierId": s.Id, "kind": "invoice",
		"invoiceNumber": "INV-79", "invoiceDate": "2026-03-01", "total": "1.00", "lines": []map[string]any{}}),
		http.StatusBadRequest, "exchange_rate_required")
}

// A credit note (A18) posts with its signs turned onto the open item of the
// invoice it credits, which is then owed the rest.
func TestSupplierInvoices_CreditNote(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	inv := postInvoice(t, b, draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "F-10",
		"invoiceDate": "2026-03-01", "total": "1250.00",
		"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("1"), "amount": "1250.00"}}}).Id)
	cn := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "credit_note", "invoiceNumber": "K-10", "invoiceDate": "2026-03-05",
		"total": "250.00", "creditsInvoiceId": inv.Id,
		"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("1"), "amount": "250.00"}}})
	cn = postInvoice(t, b, cn.Id)
	v := voucherByID(t, b, cn.Voucher.Id)
	expectPostings(t, "credit note", v, "2400 250.00", "6300 -200.00 1 base", "2710 -50.00 1 tax")
	if v.Kind != "supplier_credit_note" || *v.Postings[0].OpenItemId != *inv.OpenItemId {
		t.Fatalf("credit note voucher = %+v", v)
	}
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil)
	res.JSON(&inv)
	if *inv.Balance != "-1000.00" {
		t.Fatalf("invoice balance = %s", *inv.Balance)
	}
	// A credit note names a posted invoice of the same supplier.
	other := supplier(t, b, map[string]any{"name": "Other AS"})
	if res := b.c.Do(http.MethodPost, base+"/supplier-invoices", map[string]any{"supplierId": other.Id, "kind": "credit_note",
		"invoiceNumber": "K-11", "invoiceDate": "2026-03-05", "total": "1.00", "creditsInvoiceId": inv.Id,
		"lines": []map[string]any{}}); res.Status != http.StatusBadRequest {
		t.Fatalf("another supplier's invoice credited = %d", res.Status)
	}
}

// The duplicate guard (A18), draft deletion, a file needed and lines that sum
// to the total for Post, the files carried to the voucher, and Reverse and
// copy: the reversal line for line, the invoice reversed, a new draft with
// the same lines and files — which the guard lets through.
func TestSupplierInvoices_LifeOfADraft(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	body := map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "F-20", "invoiceDate": "2026-03-01",
		"total": "1250.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("1"), "amount": "1000.00",
			"entry": "net", "text": "Rør"}}}
	inv := draft(t, b, body)
	body["invoiceNumber"] = " f-20 "
	expectProblem(t, b.c.Do(http.MethodPost, base+"/supplier-invoices", body), http.StatusConflict, "duplicate_supplier_invoice")
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices?supplierId=%d&supplierNumber=F-20", base, s.Id), nil)
	var list gen.AccountingSupplierInvoiceList
	res.JSON(&list)
	if list.Total != 1 {
		t.Fatalf("duplicate lookup = %+v", list)
	}

	// Post needs a file, and lines that make the total.
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/post", base, inv.Id), nil),
		http.StatusConflict, "supplier_invoice_file_required")
	if res := attach(t, b, inv.Id, "a.pdf"); res.Status != http.StatusCreated {
		t.Fatalf("attach = %d", res.Status)
	}
	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), map[string]any{"supplierId": s.Id, "kind": "invoice",
		"invoiceNumber": "F-20", "invoiceDate": "2026-03-01", "total": "1300.00", "revision": inv.Revision,
		"lines": body["lines"]})
	if res.Status != http.StatusOK {
		t.Fatalf("update = %d %s", res.Status, res.Body)
	}
	res.JSON(&inv)
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/post", base, inv.Id), nil),
		http.StatusBadRequest, "supplier_invoice_total_mismatch")

	// A draft is deleted with its files, and its object leaves the store.
	extra := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "F-21", "invoiceDate": "2026-03-01",
		"total": "1.00", "lines": []map[string]any{}})
	attach(t, b, extra.Id, "extra.pdf")
	objects := len(b.h.objects.data)
	if res := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/supplier-invoices/%d", base, extra.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("delete draft = %d %s", res.Status, res.Body)
	}
	if len(b.h.objects.data) != objects-1 {
		t.Fatalf("objects %d after deleting the draft, want %d", len(b.h.objects.data), objects-1)
	}

	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), map[string]any{"supplierId": s.Id, "kind": "invoice",
		"invoiceNumber": "F-20", "invoiceDate": "2026-03-01", "total": "1250.00", "revision": inv.Revision,
		"lines": body["lines"]})
	res.JSON(&inv)
	if res := attach(t, b, inv.Id, "b.pdf"); res.Status != http.StatusCreated {
		t.Fatalf("second attach = %d", res.Status)
	}
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/post", base, inv.Id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("post = %d %s", res.Status, res.Body)
	}
	res.JSON(&inv)
	v := voucherByID(t, b, inv.Voucher.Id)
	if len(v.Attachments) != 2 || v.Attachments[0].FileName != "a.pdf" || v.Attachments[1].FileName != "b.pdf" {
		t.Fatalf("voucher attachments = %+v", v.Attachments)
	}
	expectProblem(t, b.c.Do(http.MethodDelete, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil),
		http.StatusConflict, "supplier_invoice_not_draft")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, v.Id),
		map[string]any{"date": "2026-03-10", "reason": "x"}), http.StatusConflict, "voucher_owned_by_source")

	// Reverse and copy.
	res = b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/reverse-and-copy", base, inv.Id),
		map[string]any{"date": "2026-03-10", "reason": "Wrong account"})
	if res.Status != http.StatusCreated {
		t.Fatalf("reverse and copy = %d %s", res.Status, res.Body)
	}
	var copied gen.AccountingSupplierInvoice
	res.JSON(&copied)
	if copied.State != "draft" || copied.CopiedFromId == nil || *copied.CopiedFromId != inv.Id || len(copied.Lines) != 1 ||
		copied.Lines[0].Amount != "1000.00" || copied.Lines[0].Text != "Rør" || len(copied.Files) != 2 || copied.InvoiceNumber != "F-20" {
		t.Fatalf("copy = %+v", copied)
	}
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil)
	res.JSON(&inv)
	if inv.State != "reversed" || inv.ReversalVoucher == nil {
		t.Fatalf("original = %+v", inv)
	}
	rv := voucherByID(t, b, inv.ReversalVoucher.Id)
	expectPostings(t, "reversal", rv, "2400 1250.00", "6300 -1000.00 1 base", "2710 -250.00 1 tax")
	if rv.Reverses == nil || rv.Reverses.Id != v.Id || *rv.ReversalReason != "Wrong account" {
		t.Fatalf("reversal = %+v", rv)
	}
	// The copy's files are the same objects: deleting the copy keeps them,
	// since the voucher still names them.
	if res := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/supplier-invoices/%d", base, copied.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("delete copy = %d", res.Status)
	}
	for _, a := range v.Attachments {
		if res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/vouchers/%d/attachments/%d", base, v.Id, a.Id), nil); res.Status != http.StatusOK {
			t.Fatalf("attachment %d after the copy's deletion = %d", a.Id, res.Status)
		}
	}
}

// A posted invoice dated in a locked period, a payment before the books, and
// an invoice with a payment standing are refused, as a person's acts.
func TestSupplierInvoices_DatesAndRefusals(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	inv := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "L-1", "invoiceDate": "2026-01-15",
		"total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}})
	if res := b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-01-31"}); res.Status != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Status, res.Body)
	}
	attach(t, b, inv.Id, "l.pdf")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/post", base, inv.Id), nil),
		http.StatusConflict, "period_locked")
	if b.h.Count(t, "SELECT count(*) FROM accounting.vouchers WHERE kind = 'supplier_invoice'") != 0 {
		t.Fatal("a refused post left a voucher")
	}
	ok := postInvoice(t, b, draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "L-2",
		"invoiceDate": "2026-02-15", "total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}}).Id)
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, ok.Id),
		map[string]any{"date": "2025-12-31", "amountNok": "10.00"}), http.StatusBadRequest, "date_before_books_start")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, ok.Id),
		map[string]any{"date": "2026-02-20", "amountNok": "10.00", "bankAccountId": b.acc("6300")}), http.StatusBadRequest, "bank_account_invalid")
	if res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, ok.Id),
		map[string]any{"date": "2026-02-20", "amountNok": "10.00", "bankAccountId": b.acc("1950")}); res.Status != http.StatusCreated {
		t.Fatalf("pay from 1950 = %d %s", res.Status, res.Body)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/reverse-and-copy", base, ok.Id),
		map[string]any{"date": "2026-02-21", "reason": "x"}), http.StatusConflict, "supplier_invoice_has_payments")
	// The item is the invoice's: the open-item settlement refuses it.
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/open-items/%d/settlements", base, *ok.OpenItemId),
		map[string]any{"date": "2026-02-21", "amount": "1.00"}), http.StatusConflict, "settlement_supplier_invoice")
}

// The supplier ledger is the payables account (A10): the suppliers report's
// total equals the balance of 2400, item by item, and names the suppliers
// and their invoices.
func TestSupplierInvoices_TheLedgerIsThePayablesAccount(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	other := supplier(t, b, map[string]any{"name": "Elektro AS"})
	for i, sup := range []gen.AccountingSupplier{s, other, s} {
		inv := postInvoice(t, b, draft(t, b, map[string]any{"supplierId": sup.Id, "kind": "invoice",
			"invoiceNumber": fmt.Sprintf("P-%d", i), "invoiceDate": "2026-03-01", "total": "125.00",
			"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("1"), "amount": "125.00"}}}).Id)
		if i == 2 {
			b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, inv.Id), map[string]any{"date": "2026-03-10",
				"amountNok": "25.00"})
		}
	}
	res := b.c.Do(http.MethodGet, base+"/reports/suppliers?from=2026-01-01&to=2026-12-31", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("report = %d %s", res.Status, res.Body)
	}
	var report gen.AccountingReport
	res.JSON(&report)
	last := report.Rows[len(report.Rows)-1]
	payables := b.h.Count(t, `SELECT (sum(p.amount) * 100)::int FROM accounting.postings p
		JOIN accounting.accounts a ON a.id = p.account_id WHERE a.system_role = 'payables'`)
	if last.Cells["balance"] != fmt.Sprintf("%.2f", float64(payables)/100) || last.Cells["balance"] != "-350.00" {
		t.Fatalf("ledger total %s, payables %d øre", last.Cells["balance"], payables)
	}
	named, linked := false, 0
	for _, r := range report.Rows {
		if r.Style == gen.Heading && r.Cells["party"] == "Elektro AS" {
			named = true
		}
		if r.SupplierInvoiceId != nil {
			linked++
		}
	}
	if !named || linked != 3 {
		t.Fatalf("named %v, linked %d rows: %+v", named, linked, report.Rows)
	}
}

// Permissions per endpoint (A1): reading needs accounting:read, every write
// accounting:post.
func TestPermissions_Purchases(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	r := b.h.SignIn(t, reader...)
	if res := r.Do(http.MethodGet, base+"/suppliers", nil); res.Status != http.StatusOK {
		t.Fatalf("list suppliers as a reader = %d", res.Status)
	}
	if res := r.Do(http.MethodGet, base+"/supplier-invoices", nil); res.Status != http.StatusOK {
		t.Fatalf("list invoices as a reader = %d", res.Status)
	}
	for _, w := range []struct{ method, path string }{
		{http.MethodPost, "/suppliers"},
		{http.MethodPut, fmt.Sprintf("/suppliers/%d", s.Id)},
		{http.MethodDelete, fmt.Sprintf("/suppliers/%d", s.Id)},
		{http.MethodGet, "/suppliers/lookup/brreg?q=hansen"},
		{http.MethodPost, "/supplier-invoices"},
		{http.MethodPost, "/supplier-invoices/1/post"},
		{http.MethodPost, "/supplier-invoices/1/payments"},
		{http.MethodPost, "/supplier-invoices/1/reverse-and-copy"},
		{http.MethodDelete, "/supplier-invoices/1"},
		{http.MethodPost, "/unposted/1/nok-amounts"},
		{http.MethodPost, "/unposted/1/supplier-invoice"},
		{http.MethodPost, "/unposted/1/acknowledge"},
	} {
		if res := r.Do(w.method, base+w.path, map[string]any{}, modtest.SkipContract("a refused write's body is not read")); res.Status != http.StatusForbidden {
			t.Errorf("%s %s as a reader = %d, want 403", w.method, w.path, res.Status)
		}
	}
	if res := b.h.SignIn(t).Do(http.MethodGet, base+"/suppliers", nil); res.Status != http.StatusForbidden {
		t.Fatalf("list suppliers with no permission = %d", res.Status)
	}
}

// brregTransport answers Brønnøysundregisteret's two reads: a name search
// and an entity by organisation number; fail makes every answer a 503.
type brregTransport struct{ fail bool }

func (b brregTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	answer := func(status int, body string) *http.Response {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)),
			Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r}
	}
	switch {
	case b.fail:
		return answer(http.StatusServiceUnavailable, `{}`), nil
	case strings.HasSuffix(r.URL.Path, "/enheter/923609016"):
		return answer(http.StatusOK, `{"organisasjonsnummer":"923609016","navn":"EQUINOR ASA","registrertIMvaregisteret":true,`+
			`"forretningsadresse":{"adresse":["Forusbeen 50"],"postnummer":"4035","poststed":"STAVANGER","landkode":"NO"}}`), nil
	case strings.HasSuffix(r.URL.Path, "/enheter/999999999"):
		return answer(http.StatusNotFound, `{}`), nil
	default:
		return answer(http.StatusOK, `{"_embedded":{"enheter":[{"organisasjonsnummer":"923609016","navn":"EQUINOR ASA"}]}}`), nil
	}
}

// The Brønnøysund lookup (A17): by organisation number the entity with its
// address, by name a search, an unknown number nothing, and an unreachable
// registry a 502.
func TestSuppliers_BrregLookup(t *testing.T) {
	h := newHarness(t, modtest.WithTransport(brregTransport{}), modtest.WithBackoff(func(int) time.Duration { return 0 }))
	c := h.SignIn(t, poster...)
	setUp(t, h.SignIn(t, accountant...), "nb")
	var list gen.AccountingBrregList
	res := c.Do(http.MethodGet, base+"/suppliers/lookup/brreg?organisationNumber=923%20609%20016", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("lookup = %d %s", res.Status, res.Body)
	}
	res.JSON(&list)
	if len(list.Items) != 1 || list.Items[0].Name != "EQUINOR ASA" || deref(list.Items[0].PostalCode) != "4035" ||
		deref(list.Items[0].City) != "STAVANGER" || deref(list.Items[0].AddressLine) != "Forusbeen 50" || !deref(list.Items[0].VatRegistered) {
		t.Fatalf("entity = %+v", list.Items)
	}
	c.Do(http.MethodGet, base+"/suppliers/lookup/brreg?q=equinor", nil).JSON(&list)
	if len(list.Items) != 1 || list.Items[0].OrganisationNumber != "923609016" {
		t.Fatalf("search = %+v", list.Items)
	}
	c.Do(http.MethodGet, base+"/suppliers/lookup/brreg?organisationNumber=999999999", nil).JSON(&list)
	if len(list.Items) != 0 {
		t.Fatalf("unknown = %+v", list.Items)
	}
	if res := c.Do(http.MethodGet, base+"/suppliers/lookup/brreg?q=e", nil); res.Status != http.StatusBadRequest {
		t.Fatalf("a one-letter search = %d", res.Status)
	}
	down := newHarness(t, modtest.WithTransport(brregTransport{fail: true}), modtest.WithBackoff(func(int) time.Duration { return 0 }))
	setUp(t, down.SignIn(t, accountant...), "nb")
	expectProblem(t, down.SignIn(t, poster...).Do(http.MethodGet, base+"/suppliers/lookup/brreg?q=equinor", nil),
		http.StatusBadGateway, "brreg_unavailable")
}

// A draft's file is served back as it was, and removed from the draft.
func TestSupplierInvoices_Files(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	inv := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "D-1", "invoiceDate": "2026-03-01",
		"total": "1.00", "lines": []map[string]any{}})
	res := attach(t, b, inv.Id, "scan.pdf")
	var f gen.AccountingAttachment
	res.JSON(&f)
	got := b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d/files/%d", base, inv.Id, f.Id), nil)
	if got.Status != http.StatusOK || string(got.Body) != "%PDF-1.4 scan.pdf" {
		t.Fatalf("download = %d %q", got.Status, got.Body)
	}
	if res := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/supplier-invoices/%d/files/%d", base, inv.Id, f.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("remove = %d %s", res.Status, res.Body)
	}
	if res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d/files/%d", base, inv.Id, f.Id), nil); res.Status != http.StatusNotFound {
		t.Fatalf("download after removal = %d", res.Status)
	}
	if len(b.h.objects.data) != 0 {
		t.Fatalf("objects left: %d", len(b.h.objects.data))
	}
}

// reverseAndCopy asks for Reverse and copy of an invoice.
func reverseAndCopy(b *books, id int64) *modtest.Response {
	return b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/reverse-and-copy", base, id),
		map[string]any{"date": "2026-03-20", "reason": "Wrong"})
}

// A credit note is held, at Post, to the invoice it credits as it stands then
// (the review's HIGH): in its currency, for no more than is left on it in
// NOK and in the currency, and only while it is posted. Reverse and copy of
// an invoice is refused while another voucher stands on its open item.
func TestSupplierInvoices_CreditNoteHeldToItsInvoice(t *testing.T) {
	b := newBooks(t)
	eur := supplier(t, b, map[string]any{"name": "Cloud GmbH", "country": "DE", "currency": "EUR"})
	inv := postInvoice(t, b, draft(t, b, map[string]any{"supplierId": eur.Id, "kind": "invoice", "invoiceNumber": "E-1",
		"invoiceDate": "2026-03-01", "exchangeRate": "11.5", "total": "100.00",
		"lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}}).Id)
	post := func(id int64) *modtest.Response {
		if res := attach(t, b, id, "k.pdf"); res.Status != http.StatusCreated {
			t.Fatalf("attach = %d", res.Status)
		}
		return b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/post", base, id), nil)
	}
	credit := func(number, cur, total string, extra map[string]any) gen.AccountingSupplierInvoice {
		body := map[string]any{"supplierId": eur.Id, "kind": "credit_note", "invoiceNumber": number, "invoiceDate": "2026-03-05",
			"currency": cur, "total": total, "creditsInvoiceId": inv.Id,
			"lines": []map[string]any{{"accountId": b.acc("6300"), "amount": total}}}
		for k, v := range extra {
			body[k] = v
		}
		return draft(t, b, body)
	}

	// A NOK credit note on a EUR invoice.
	expectProblem(t, post(credit("K-NOK", "NOK", "100.00", nil).Id), http.StatusConflict, "credit_note_currency_mismatch")
	// More than is left in the currency, although less in NOK.
	expectProblem(t, post(credit("K-EUR-1", "EUR", "101.00", map[string]any{"exchangeRate": "11.0"}).Id),
		http.StatusConflict, "credit_note_exceeds_balance")
	// More than is left in NOK.
	expectProblem(t, post(credit("K-EUR-2", "EUR", "90.00", map[string]any{"exchangeRate": "13.0"}).Id),
		http.StatusConflict, "credit_note_exceeds_balance")
	// What is left, and then nothing more.
	part := credit("K-EUR-3", "EUR", "60.00", map[string]any{"exchangeRate": "11.5"})
	if res := post(part.Id); res.Status != http.StatusOK {
		t.Fatalf("credit 60 = %d %s", res.Status, res.Body)
	}
	expectProblem(t, post(credit("K-EUR-4", "EUR", "40.01", map[string]any{"exchangeRate": "11.5"}).Id),
		http.StatusConflict, "credit_note_exceeds_balance")

	// The invoice is not reversed under its credit note; the credit note
	// itself is, and then the invoice.
	expectProblem(t, reverseAndCopy(b, inv.Id), http.StatusConflict, "supplier_invoice_item_in_use")
	if res := reverseAndCopy(b, part.Id); res.Status != http.StatusCreated {
		t.Fatalf("reverse the credit note = %d %s", res.Status, res.Body)
	}
	late := credit("K-EUR-5", "EUR", "10.00", map[string]any{"exchangeRate": "11.5"})
	if res := reverseAndCopy(b, inv.Id); res.Status != http.StatusCreated {
		t.Fatalf("reverse the invoice = %d %s", res.Status, res.Body)
	}
	// A credit note drafted while its invoice stood, posted after the
	// invoice was reversed.
	expectProblem(t, post(late.Id), http.StatusConflict, "credit_note_invoice_not_posted")

	// A payment made through a credit note's own item holds the credit note,
	// and with it, through the shared item, the invoice.
	nok := b.aSupplier(t)
	inv2 := postInvoice(t, b, draft(t, b, map[string]any{"supplierId": nok.Id, "kind": "invoice", "invoiceNumber": "N-1",
		"invoiceDate": "2026-03-01", "total": "500.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "500.00"}}}).Id)
	cn := postInvoice(t, b, draft(t, b, map[string]any{"supplierId": nok.Id, "kind": "credit_note", "invoiceNumber": "NK-1",
		"invoiceDate": "2026-03-02", "total": "100.00", "creditsInvoiceId": inv2.Id,
		"lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}}).Id)
	if res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, inv2.Id),
		map[string]any{"date": "2026-03-10", "amountNok": "50.00"}); res.Status != http.StatusCreated {
		t.Fatalf("pay = %d %s", res.Status, res.Body)
	}
	expectProblem(t, reverseAndCopy(b, cn.Id), http.StatusConflict, "supplier_invoice_item_in_use")
}

// A credit note and a payment on the same open item, posted at once, cannot
// both pass their balance checks: Post locks the credited invoice and its
// item as a payment does, so the second sees what the first left.
func TestSupplierInvoices_CreditNoteAndPaymentRace(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	for i := range 4 {
		inv := postInvoice(t, b, draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice",
			"invoiceNumber": fmt.Sprintf("R-%d", i), "invoiceDate": "2026-03-01", "total": "1000.00",
			"lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "1000.00"}}}).Id)
		cn := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "credit_note", "invoiceNumber": fmt.Sprintf("RK-%d", i),
			"invoiceDate": "2026-03-02", "total": "600.00", "creditsInvoiceId": inv.Id,
			"lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "600.00"}}})
		if res := attach(t, b, cn.Id, "k.pdf"); res.Status != http.StatusCreated {
			t.Fatalf("attach = %d", res.Status)
		}
		statuses := make(chan int, 2)
		go func() {
			statuses <- b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/post", base, cn.Id), nil).Status
		}()
		go func() {
			statuses <- b.c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/payments", base, inv.Id),
				map[string]any{"date": "2026-03-03", "amountNok": "600.00"}).Status
		}()
		got := []int{<-statuses, <-statuses}
		slices.Sort(got)
		if got[0] >= 300 || got[1] != http.StatusConflict {
			t.Fatalf("round %d: statuses %v, want one success and one 409", i, got)
		}
		res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil)
		res.JSON(&inv)
		if *inv.Balance != "-400.00" {
			t.Fatalf("round %d: balance %s, want -400.00", i, *inv.Balance)
		}
	}
}

// Both the rate and the NOK total (M1): they must agree to the øre, an øre
// per line of slack; and a rate of exactly 1 is a rate (L4).
func TestSupplierInvoices_RateAndNOKTotalAgree(t *testing.T) {
	b := newBooks(t)
	s := supplier(t, b, map[string]any{"name": "Cloud GmbH", "country": "DE", "currency": "EUR"})
	body := func(number, rate, nok string) map[string]any {
		return map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": number, "invoiceDate": "2026-03-01",
			"exchangeRate": rate, "totalNok": nok, "total": "100.00",
			"lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}}
	}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/supplier-invoices", body("M-1", "11.5", "1250.00")),
		http.StatusBadRequest, "exchange_rate_mismatch")
	expectProblem(t, b.c.Do(http.MethodPost, base+"/supplier-invoices", body("M-2", "11.5", "1150.02")),
		http.StatusBadRequest, "exchange_rate_mismatch")
	if inv := draft(t, b, body("M-3", "11.5", "1150.01")); deref(inv.TotalNok) != "1150.01" {
		t.Fatalf("within an øre = %+v", inv)
	}
	one := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "M-4", "invoiceDate": "2026-03-01",
		"exchangeRate": "1", "total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}})
	if posted := postInvoice(t, b, one.Id); posted.State != "posted" || *posted.Balance != "-100.00" {
		t.Fatalf("a rate of 1 = %+v", posted)
	}
}

// A person supplier is anonymised once the bookkeeping no longer keeps it
// (L5): refused before its retention date, and for a company; after it, the
// name is its number's, the contact and bank fields are cleared, the
// postings keep it, and it is not changed again. The organisation number is
// unique per country, and the search takes % and _ as themselves.
func TestSuppliers_AnonymiseAndUniqueness(t *testing.T) {
	b := newBooks(t)
	person := supplier(t, b, map[string]any{"name": "Kari 100%_Enkeltperson", "isPerson": true, "email": "kari@example.com",
		"bankAccount": "1234.56.78903", "addressLine": "Gata 1"})
	inv := postInvoice(t, b, draft(t, b, map[string]any{"supplierId": person.Id, "kind": "invoice", "invoiceNumber": "1",
		"invoiceDate": "2026-04-01", "total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}}).Id)
	anonymise := func(id int64) *modtest.Response {
		return b.c.Do(http.MethodPost, fmt.Sprintf("%s/suppliers/%d/anonymise", base, id), nil)
	}
	expectProblem(t, anonymise(person.Id), http.StatusConflict, "supplier_retained")
	company := b.aSupplier(t)
	if res := anonymise(company.Id); res.Status != http.StatusBadRequest {
		t.Fatalf("a company anonymised = %d", res.Status)
	}

	var list gen.AccountingSupplierList
	b.c.Do(http.MethodGet, base+"/suppliers?q=100%25_", nil).JSON(&list)
	if list.Total != 1 || list.Items[0].Id != person.Id {
		t.Fatalf("search for 100%%_ = %+v", list)
	}
	b.c.Do(http.MethodGet, base+"/suppliers?q=_", nil).JSON(&list)
	if list.Total != 1 {
		t.Fatalf("search for _ matched %d", list.Total)
	}

	b.h.Advance(6 * 366 * 24 * time.Hour)
	b.c = b.h.SignIn(t, accountant...)
	res := anonymise(person.Id)
	if res.Status != http.StatusOK {
		t.Fatalf("anonymise = %d %s", res.Status, res.Body)
	}
	var got gen.AccountingSupplier
	res.JSON(&got)
	if got.Name != fmt.Sprintf("Anonymised supplier %d", person.Number) || got.Email != nil || got.BankAccount != nil ||
		got.AddressLine != nil || got.AnonymisedAt == nil || got.Active || got.Balance != "-100.00" {
		t.Fatalf("anonymised = %+v", got)
	}
	if v := voucherByID(t, b, inv.Voucher.Id); v.Postings[0].SupplierId == nil || *v.Postings[0].SupplierId != person.Id {
		t.Fatalf("the voucher lost its supplier: %+v", v.Postings[0])
	}
	expectProblem(t, anonymise(person.Id), http.StatusConflict, "supplier_anonymised")
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/suppliers/%d", base, person.Id),
		map[string]any{"name": "Kari", "revision": got.Revision}), http.StatusConflict, "supplier_anonymised")

	// The same organisation number in another country is another supplier.
	supplier(t, b, map[string]any{"name": "Svensk AB", "country": "SE", "organisationNumber": *company.OrganisationNumber})
	expectProblem(t, b.c.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "Again AS",
		"organisationNumber": *company.OrganisationNumber}), http.StatusConflict, "supplier_organisation_number_taken")
}
