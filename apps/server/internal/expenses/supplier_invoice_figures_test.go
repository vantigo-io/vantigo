package expenses_test

import (
	"testing"
)

// What a project's supplier invoices cost and bill, as a sub-figure of what
// all its expenses do (supplier invoices design D3): per currency, in the
// three buckets the unit's status puts a line in and their total, nil when a
// currency has none — while every existing figure keeps meaning everything.

func TestProjectExpenses_SupplierInvoicesAreASubFigureOfEveryBucket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, user := signIn(t, h)
	p := expensesProvider(t, h)

	for _, e := range []recordedExpense{
		{project: projectKraftVerket, gross: "100.00", paidBy: "employee", status: "draft"},
		{project: projectKraftVerket, kind: "supplier_invoice", gross: "1250.00", vat: "250.00", paidBy: "company",
			billable: true, billAmount: "1100.00", status: "approved"},
		{project: projectKraftVerket, kind: "supplier_invoice", gross: "500.00", paidBy: "company", status: "submitted"},
		// Rejected is back with its recorder, which is where a draft is.
		{project: projectKraftVerket, kind: "supplier_invoice", gross: "200.00", paidBy: "company", status: "rejected"},
		{project: projectKraftVerket, currency: "EUR", gross: "90.00", paidBy: "company", status: "approved"},
	} {
		recordExpense(t, h, user, e)
	}

	totals := projectExpensesOf(t, p, projectKraftVerket)
	nok := currencyOf(t, totals, "NOK")
	// Every existing figure is still everything.
	wantBucket(t, "NOK approved", nok.Approved, 1, "1000.00", "1100.00")
	wantBucket(t, "NOK submitted", nok.Submitted, 1, "500.00", "0.00")
	wantBucket(t, "NOK draft", nok.Draft, 2, "300.00", "0.00")
	wantBucket(t, "NOK total", nok.Total, 4, "1800.00", "1100.00")

	si := nok.SupplierInvoices
	if si == nil {
		t.Fatal("NOK carries no supplier invoice figure; three supplier invoices were recorded in it")
	}
	wantBucket(t, "NOK supplier invoices approved", si.Approved, 1, "1000.00", "1100.00")
	wantBucket(t, "NOK supplier invoices submitted", si.Submitted, 1, "500.00", "0.00")
	wantBucket(t, "NOK supplier invoices draft", si.Draft, 1, "200.00", "0.00")
	wantBucket(t, "NOK supplier invoices total", si.Total, 3, "1700.00", "1100.00")

	// A currency with none carries no sub-figure at all, rather than zeroes.
	if eur := currencyOf(t, totals, "EUR"); eur.SupplierInvoices != nil {
		t.Errorf("EUR supplier invoices = %+v, want nil: nothing in EUR is a supplier invoice", eur.SupplierInvoices)
	}
}

func TestExpensesProjectSummary_SupplierInvoicesHaveTheirOwnLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, financeID := financeReader(t, h)
	recordExpense(t, h, financeID, recordedExpense{project: projectKraftVerket, kind: "supplier_invoice",
		gross: "1250.00", vat: "250.00", paidBy: "company", status: "approved"})
	// A second invoice in another bucket, so the line's total is neither of
	// its buckets alone and rendering one in the other's place shows.
	recordExpense(t, h, financeID, recordedExpense{project: projectKraftVerket, kind: "supplier_invoice",
		gross: "500.00", paidBy: "company", status: "submitted"})
	recordExpense(t, h, financeID, recordedExpense{project: projectKraftVerket, gross: "100.00", paidBy: "employee"})
	recordExpense(t, h, financeID, recordedExpense{project: projectEuro, currency: "EUR", gross: "90.00", paidBy: "employee"})

	nok := summaryCurrency(t, getProjectSummary(t, finance, projectKraftVerket), "NOK")
	if nok.Total.Count != 3 || nok.Total.Cost != 1600 {
		t.Errorf("NOK total = %+v, want all three lines, 1600 cost: the totals are everything", nok.Total)
	}
	si := nok.SupplierInvoices
	if si == nil || si.Approved.Count != 1 || si.Approved.Cost != 1000 || si.Submitted.Count != 1 || si.Submitted.Cost != 500 ||
		si.Draft.Count != 0 || si.Total.Count != 2 || si.Total.Cost != 1500 {
		t.Errorf("NOK supplierInvoices = %+v, want the approved invoice costing 1000 and the submitted one 500, 1500 in all", si)
	}

	raw := rawProjectSummary(t, finance, projectEuro)
	currencies, _ := raw["currencies"].([]any)
	if len(currencies) != 1 {
		t.Fatalf("EUR project currencies = %v, want one", raw["currencies"])
	}
	if _, present := currencies[0].(map[string]any)["supplierInvoices"]; present {
		t.Errorf("a currency with no supplier invoice answers supplierInvoices: %v", currencies[0])
	}
}
