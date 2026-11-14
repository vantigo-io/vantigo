package integration_test

import (
	"fmt"
	"math/big"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Sales post themselves end to end (accounting design A12–A16): the real
// customers, invoices and accounting modules on one database, the catch-up
// worker built as worker mode builds it. An invoice issued through Invoices
// becomes a balanced voucher, its payment and a write-off follow, and the
// ledger's receivables are Invoices' own open total.

const accountingBase = "/api/v1/accounting"

// accountingVoucher is a voucher as the story reads it.
type accountingVoucher struct {
	ID               int64  `json:"id"`
	Kind             string `json:"kind"`
	SourceDocumentID *int64 `json:"sourceDocumentId"`
	Postings         []struct {
		AccountNumber string `json:"accountNumber"`
		Amount        string `json:"amount"`
	} `json:"postings"`
}

func TestAccounting_IssuedInvoicesPostAndTheLedgerAgreesWithInvoices(t *testing.T) {
	names := []string{modCustomers, modInvoices, modAccounting}
	h, c, _, _ := installInvoices(t, names, []string{"accounting:read", "accounting:post", "accounting:close"})
	okJSON(t, c, http.MethodPost, accountingBase+"/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "bimonthly", "language": "en"}, nil)
	customer := invoiceNewBusiness(t, c, "Fjord Bygg", "Fjord Bygg AS", "923609016")
	paid := issueDoc(t, c, newDraft(t, c, customer.Id).ID)
	okJSON(t, c, http.MethodPost, invoiceAt(paid.ID)+"/payments", map[string]any{"amount": 5000, "paidOn": invoiceToday}, nil)
	lost := issueDoc(t, c, newDraft(t, c, customer.Id).ID)
	okJSON(t, c, http.MethodPost, invoiceAt(lost.ID)+"/write-off", map[string]any{"writtenOffOn": invoiceToday, "reason": "loss", "vatCorrection": true}, nil)
	open := issueDoc(t, c, newDraft(t, c, customer.Id).ID)

	mods := make([]module.Module, 0, len(names))
	for _, n := range names {
		mods = append(mods, moduleNamed(t, n))
	}
	var worker *accounting.CatchUpWorker
	for _, w := range module.Workers(h.Deps(), mods...) {
		if cw, ok := w.(*accounting.CatchUpWorker); ok {
			worker = cw
		}
	}
	if worker == nil {
		t.Fatal("module.Workers built no catch-up")
	}
	if err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	vouchersOf := func(doc int64) []accountingVoucher {
		var list struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
		}
		okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/vouchers?sourceDocumentId=%d", accountingBase, doc), nil, &list)
		var out []accountingVoucher
		for _, it := range list.Items {
			var v accountingVoucher
			okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/vouchers/%d", accountingBase, it.ID), nil, &v)
			sum := new(big.Rat)
			for _, p := range v.Postings {
				a, _ := new(big.Rat).SetString(p.Amount)
				sum.Add(sum, a)
			}
			if sum.Sign() != 0 {
				t.Errorf("voucher %d does not balance: %v", v.ID, v.Postings)
			}
			out = append(out, v)
		}
		return out
	}
	kinds := func(vs []accountingVoucher) map[string]bool {
		out := map[string]bool{}
		for _, v := range vs {
			out[v.Kind] = true
		}
		return out
	}
	if k := kinds(vouchersOf(paid.ID)); !k["sales_invoice"] || !k["customer_payment"] || len(k) != 2 {
		t.Errorf("the paid invoice's vouchers = %v", k)
	}
	if k := kinds(vouchersOf(lost.ID)); !k["sales_invoice"] || !k["write_off"] {
		t.Errorf("the written-off invoice's vouchers = %v", k)
	}
	for _, v := range vouchersOf(open.ID) {
		if v.Postings[0].AccountNumber != "1500" || v.Postings[0].Amount != "12500.00" {
			t.Errorf("the open invoice's receivable = %+v, want 12 500.00 on 1500", v.Postings[0])
		}
	}

	// The ledger's receivables are what Invoices says is open.
	var report struct {
		Rows []struct {
			Cells map[string]string `json:"cells"`
		} `json:"rows"`
	}
	okJSON(t, c, http.MethodGet, accountingBase+"/reports/trial_balance?from=2026-01-01&to=2026-12-31", nil, &report)
	ledger := ""
	for _, r := range report.Rows {
		if r.Cells["account"] == "1500" {
			ledger = r.Cells["closing"]
		}
	}
	invoicesOpen := new(big.Rat)
	for _, id := range []int64{paid.ID, lost.ID, open.ID} {
		var doc struct {
			OpenAmount float64 `json:"openAmount"`
		}
		okJSON(t, c, http.MethodGet, invoiceAt(id), nil, &doc)
		invoicesOpen.Add(invoicesOpen, new(big.Rat).SetFloat64(doc.OpenAmount))
	}
	if want := invoicesOpen.FloatString(2); ledger != want {
		t.Errorf("receivables in the ledger = %s, Invoices' open total = %s", ledger, want)
	}
}
