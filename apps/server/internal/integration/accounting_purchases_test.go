package integration_test

import (
	"fmt"
	"math/big"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Expenses post themselves end to end (accounting design A20, R2): the real
// expenses and accounting modules on one database, the catch-up built as
// worker mode builds it. An expense approved through Expenses becomes a
// balanced voucher with its VAT deducted by the category's rule, its
// reimbursement pays employee_payables, and its unapproval turns the cost
// back; the books' employee_payables is what Expenses still owes.
func TestAccounting_ApprovedExpensesPostThemselves(t *testing.T) {
	names := []string{modCustomers, modInvoices, modExpenses, modAccounting}
	h, c, _, _ := installInvoices(t, names, []string{"accounting:read", "accounting:post", "accounting:close",
		"expenses:access", "expenses:approve", "expenses:manage"})
	okJSON(t, c, http.MethodPost, accountingBase+"/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "bimonthly", "language": "en"}, nil)
	var chart struct {
		Items []struct {
			ID     int32  `json:"id"`
			Number string `json:"number"`
		} `json:"items"`
	}
	okJSON(t, c, http.MethodGet, accountingBase+"/accounts", nil, &chart)
	accountID := map[string]int32{}
	for _, a := range chart.Items {
		accountID[a.Number] = a.ID
	}
	okJSON(t, c, http.MethodPost, accountingBase+"/posting-rules", map[string]any{"name": "Materials", "scope": "expense",
		"match": "expense_category", "expenseCategoryId": materialsCategory, "accountId": accountID["4005"], "vatDeductible": true}, nil)

	kept := record(t, c, map[string]any{"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-16",
		"description": "Kabel", "paidBy": "employee", "currency": "NOK", "grossAmount": 1250.00, "vatAmount": 250.00})
	undone := record(t, c, map[string]any{"kind": "outlay", "categoryId": materialsCategory, "entryDate": "2026-03-17",
		"description": "Feil", "paidBy": "company", "currency": "NOK", "grossAmount": 100.00})
	move(t, c, expensesSubmit, map[string]any{"entryIds": []int64{kept.Id, undone.Id}})
	move(t, c, expensesApprove, map[string]any{"entryIds": []int64{kept.Id, undone.Id}})
	move(t, c, "/api/v1/expenses/unapprove", map[string]any{"entryIds": []int64{undone.Id}})
	move(t, c, "/api/v1/expenses/reimbursed", map[string]any{"entryIds": []int64{kept.Id}, "date": "2026-03-31"})

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

	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	okJSON(t, c, http.MethodGet, accountingBase+"/vouchers?limit=100", nil, &list)
	got := map[string][]string{}
	for _, it := range list.Items {
		var v struct {
			accountingVoucher
			Source *struct {
				Module string `json:"module"`
				Kind   string `json:"kind"`
				ID     int64  `json:"id"`
			} `json:"source"`
		}
		okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/vouchers/%d", accountingBase, it.ID), nil, &v)
		if v.Source == nil || v.Source.Module != "expenses" {
			continue
		}
		sum := new(big.Rat)
		var lines []string
		for _, p := range v.Postings {
			a, _ := new(big.Rat).SetString(p.Amount)
			sum.Add(sum, a)
			lines = append(lines, p.AccountNumber+" "+p.Amount)
		}
		if sum.Sign() != 0 {
			t.Errorf("voucher %d does not balance: %v", v.ID, lines)
		}
		got[fmt.Sprintf("%s:%d", v.Source.Kind, v.Source.ID)] = lines
	}
	want := map[string][]string{
		fmt.Sprintf("entry.approved:%d", kept.Id):     {"4005 1000.00", "2710 250.00", "2910 -1250.00"},
		fmt.Sprintf("entry.reimbursed:%d", kept.Id):   {"2910 1250.00", "1920 -1250.00"},
		fmt.Sprintf("entry.approved:%d", undone.Id):   {"4005 100.00", "1920 -100.00"},
		fmt.Sprintf("entry.unapproved:%d", undone.Id): {"4005 -100.00", "1920 100.00"},
	}
	if len(got) != len(want) {
		t.Fatalf("expense vouchers = %v, want %v", got, want)
	}
	for k, w := range want {
		if fmt.Sprint(got[k]) != fmt.Sprint(w) {
			t.Errorf("%s = %v, want %v", k, got[k], w)
		}
	}
}
