package docmath_test

import (
	"math/big"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// A document in EUR at 11.7234 with two VAT rates and a negative deduction
// line (invoices phase 6 design I21): each row's lines in NOK sum to its
// BaseNOK, the remainder on the row's largest line, and the receivable in
// NOK is Σ BaseNOK + Σ VATNOK — here 3263.10, where round(gross × rate)
// would be 3263.09.
func TestNetsNOK_TheRemainderStaysInItsRow(t *testing.T) {
	t.Parallel()
	high, low := big.NewRat(25, 1), big.NewRat(15, 1)
	var lines []docmath.TaxedLine
	for i, n := range []string{"5.84", "77.89", "8.12", "-6.92", "51.54", "98.18"} {
		r := high
		if i >= 4 {
			r = low
		}
		lines = append(lines, docmath.TaxedLine{Net: rat(n), Category: "S", Rate: r, SAFT: "3"})
	}
	rate := rat("11.7234")
	rows, totals, _ := docmath.Summarize(lines, rate)
	nets := docmath.NetsNOK(lines, rows, rate)
	want := []string{"68.46", "913.15", "95.19", "-81.13", "604.22", "1151.01"}
	for i, n := range nets {
		if n.FloatString(2) != want[i] {
			t.Errorf("line %d in NOK = %s, want %s", i, n.FloatString(2), want[i])
		}
	}
	for _, r := range rows {
		sum := new(big.Rat)
		for i, l := range lines {
			if l.Rate.Cmp(r.Rate) == 0 {
				sum.Add(sum, nets[i])
			}
		}
		if sum.Cmp(r.BaseNOK) != 0 {
			t.Errorf("the %s %% row's lines sum to %s, its BaseNOK is %s", r.Rate.FloatString(0), sum.FloatString(2), r.BaseNOK.FloatString(2))
		}
	}
	if got := totals.GrossNOK.FloatString(2); got != "3263.10" {
		t.Errorf("GrossNOK = %s, want 3263.10", got)
	}
	if got := docmath.Round2(new(big.Rat).Mul(totals.Gross, rate)).FloatString(2); got != "3263.09" {
		t.Errorf("round(gross × rate) = %s; the case no longer shows the two differ", got)
	}
}

// For NOK every NOK figure is its currency figure.
func TestNOK_IsTheIdentity(t *testing.T) {
	t.Parallel()
	one := big.NewRat(1, 1)
	for seed := int64(1); seed < 200; seed++ {
		var lines []docmath.TaxedLine
		for i := int64(0); i < 1+seed%5; i++ {
			net := big.NewRat((seed*7919+i*104729)%999983-200000, 100)
			lines = append(lines, docmath.TaxedLine{Net: net, Category: "S", Rate: big.NewRat(15+10*(i%2), 1), SAFT: "3"})
		}
		rows, totals, _ := docmath.Summarize(lines, one)
		for i, n := range docmath.NetsNOK(lines, rows, one) {
			if n.Cmp(lines[i].Net) != 0 {
				t.Fatalf("seed %d: line %d in NOK %s, net %s", seed, i, n.FloatString(2), lines[i].Net.FloatString(2))
			}
		}
		if totals.GrossNOK.Cmp(totals.Gross) != 0 || totals.BaseNOK.Cmp(totals.Net) != 0 || totals.VATNOK.Cmp(totals.VAT) != 0 {
			t.Fatalf("seed %d: NOK totals %v differ from the document's", seed, totals)
		}
	}
}
