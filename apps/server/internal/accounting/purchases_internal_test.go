package accounting

import (
	"math/big"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// rate is a code's rate as the store answers it.
func rate(t *testing.T, text string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(text); err != nil {
		t.Fatal(err)
	}
	return n
}

// A purchase in another currency (R8): each posting is its currency amount
// at the rate, rounded, and what rounding leaves against the NOK total goes
// on the largest base line, so the voucher balances on the invoice's own
// total; every posting but a reverse-charge VAT carries its currency amount.
func TestPurchaseVoucherLines_ForeignRoundingOnTheLargestLine(t *testing.T) {
	vat := int32(2710)
	code := &store.TaxCodesForPostingRow{ID: 1, Code: "1", Direction: "input", Active: true, VatAccountID: &vat,
		RatePercent: rate(t, "25.00")}
	var lines []purchaseLine
	for _, amount := range []string{"0.33", "0.33", "10.00"} {
		in := purchaseLineIn{AccountID: 6300, TaxCodeID: &code.ID, Amount: mustRat(amount)}
		l, rule, _ := computePurchaseLine(in, code)
		if rule != "" {
			t.Fatal(rule)
		}
		lines = append(lines, l)
	}
	// Each line's VAT: 0.08, 0.08, 2.50; the total 13.32 EUR at 11.1111 is
	// 148.00 NOK, the postings converted one by one 148.01: the øre too many
	// comes off the largest base line.
	d := purchaseDoc{ID: 1, Number: "X", Supplier: 1001, Currency: "EUR", Rate: mustRat("11.1111"), Total: mustRat("13.32"),
		TotalNOK: mustRat("148.00"), DocumentDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Lines: lines}
	got := purchaseVoucherLines(d)
	sum := new(big.Rat)
	var amounts []string
	for _, l := range got {
		sum.Add(sum, l.Amount)
		amounts = append(amounts, money(l.Amount))
	}
	want := []string{"-148.00", "3.67", "0.89", "3.67", "0.89", "111.10", "27.78"}
	if sum.Sign() != 0 || len(amounts) != len(want) {
		t.Fatalf("lines %v sum to %s", amounts, money(sum))
	}
	for i := range want {
		if amounts[i] != want[i] {
			t.Fatalf("lines = %v, want %v", amounts, want)
		}
	}
	if got[0].Currency != "EUR" || money(got[0].CurrencyAmount) != "-13.32" || money(got[5].CurrencyAmount) != "10.00" {
		t.Fatalf("currency amounts: %+v", got)
	}
}

// The payment's lines (A19, R8): owed or a credit, the share settled at the
// invoice's rate against what the bank took, the difference on loss or
// gain.
func TestPaymentLines(t *testing.T) {
	bank := Line{Role: roleBank}
	sum := func(lines []Line) string {
		s := new(big.Rat)
		for _, l := range lines {
			s.Add(s, l.Amount)
		}
		return money(s)
	}
	owed := paymentLines(true, mustRat("460.00"), mustRat("470.00"), mustRat("40.00"), true, "EUR", mustRat("11.5"), 1, 2, bank, "")
	if sum(owed) != "0.00" || len(owed) != 3 || owed[2].Role != roleCurrencyLoss || money(owed[2].Amount) != "10.00" {
		t.Fatalf("owed = %+v", owed)
	}
	refund := paymentLines(false, mustRat("460.00"), mustRat("470.00"), mustRat("40.00"), true, "EUR", mustRat("11.5"), 1, 2, bank, "")
	if sum(refund) != "0.00" || refund[2].Role != roleCurrencyGain || money(refund[0].Amount) != "-460.00" ||
		money(refund[0].CurrencyAmount) != "-40.00" {
		t.Fatalf("refund = %+v", refund)
	}
	nok := paymentLines(true, mustRat("100.00"), mustRat("100.00"), mustRat("100.00"), false, "NOK", mustRat("1"), 1, 2, bank, "")
	if len(nok) != 2 || nok[0].Currency != "" {
		t.Fatalf("NOK = %+v", nok)
	}
}

// The VAT inference (R2): the nearest current input rate within one øre per
// hundred kroner of net, and at least one øre; nothing without VAT or net.
func TestInferredCode(t *testing.T) {
	rates := []store.InputRatesRow{{ID: 1, Code: "1", RatePercent: "25.00"}, {ID: 11, Code: "11", RatePercent: "15.00"},
		{ID: 12, Code: "12", RatePercent: "11.11"}, {ID: 13, Code: "13", RatePercent: "12.00"}}
	for _, c := range []struct{ gross, vat, want string }{
		{"125.00", "25.00", "1"}, {"125.01", "25.01", "1"}, {"125.02", "25.02", ""}, {"100.00", "0.00", ""},
		{"115.00", "15.00", "11"}, {"111.11", "11.11", "12"}, {"112.00", "12.00", "13"}, {"10.00", "10.00", ""},
	} {
		got := inferredCode(rates, mustRat(c.gross), mustRat(c.vat))
		code := ""
		if got != nil {
			code = got.Code
		}
		if code != c.want {
			t.Errorf("gross %s vat %s: code %q, want %q", c.gross, c.vat, code, c.want)
		}
	}
}

// What a claim's line is owed (contracts.ExpenseLine.OwesEmployee), as
// Expenses' own expenses.owes_employee says.
func TestOwesEmployee(t *testing.T) {
	for _, c := range []struct {
		kind, paidBy string
		want         bool
	}{
		{contracts.ExpenseKindOutlay, contracts.PaidByEmployee, true},
		{contracts.ExpenseKindOutlay, contracts.PaidByCompany, false},
		{contracts.ExpenseKindMileage, "", true},
		{contracts.ExpenseKindPerDiem, "", true},
		{contracts.ExpenseKindSupplierInvoice, contracts.PaidByCompany, false},
	} {
		if got := (contracts.ExpenseLine{Kind: c.kind, PaidBy: c.paidBy}).OwesEmployee(); got != c.want {
			t.Errorf("%s/%s = %v", c.kind, c.paidBy, got)
		}
	}
}
