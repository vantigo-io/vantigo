package expenses

import (
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// The money rendering of contracts.ProjectExpenses, without a database.
//
// Two of these cannot be reached through the tables at all. Every amount this
// module stores is numeric(12,2), so a group sum PostgreSQL hands back is
// already exact at two decimals and no seeded row can put a figure on a
// rounding boundary — but the contract's rule is that each published figure
// is rounded once from the unrounded sum, and Total exists precisely so a
// consumer never adds three rounded buckets. A column's scale is not
// something a contract should depend on, so the rule is proved here, where
// the groups can carry whatever text the query's ::text cast could ever
// produce.

func TestExpenseAmountText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		amount *big.Rat
		want   string
	}{
		{"nothing at all", new(big.Rat), "0.00"},
		{"a whole number", big.NewRat(1800, 1), "1800.00"},
		{"two decimals exactly", big.NewRat(123450, 100), "1234.50"},
		{"below one", big.NewRat(1, 100), "0.01"},
		{"below one cent", big.NewRat(1, 1000), "0.00"},
		// Half away from zero, not to even: 0.005 is a cent, and so is 0.015
		// — banker's rounding would make the second 0.02 and the first 0.00.
		{"a half rounds away from zero", big.NewRat(5, 1000), "0.01"},
		{"a half above an even cent", big.NewRat(15, 1000), "0.02"},
		{"just under a half", big.NewRat(4999, 1000000), "0.00"},
		{"a third", big.NewRat(1, 3), "0.33"},
		{"two thirds", big.NewRat(2, 3), "0.67"},
		// Negative: no expense is negative today, but a consumer subtracting
		// one figure from another would be the first to find out otherwise,
		// and "half up" and "half away from zero" only differ here.
		{"a negative half rounds away from zero", big.NewRat(-5, 1000), "-0.01"},
		{"a negative amount", big.NewRat(-123456, 100), "-1234.56"},
		// Past float64's exact integers, where a float-based path would
		// already have started lying.
		{"beyond 2^53", new(big.Rat).SetFrac(big.NewInt(900719925474099301), big.NewInt(100)), "9007199254740993.01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := expenseAmountText(c.amount); got != c.want {
				t.Errorf("expenseAmountText(%s) = %q, want %q", c.amount.RatString(), got, c.want)
			}
		})
	}
}

// A sum PostgreSQL cannot have produced is reported, not silently read as
// nothing: a zero where an amount belongs is the one answer this contract
// must never invent.
func TestExactDecimalRefusesWhatIsNotADecimal(t *testing.T) {
	t.Parallel()
	if _, err := exactDecimal("NaN"); err == nil {
		t.Error("exactDecimal(\"NaN\"): want an error")
	}
	got, err := exactDecimal("1234.5600")
	if err != nil {
		t.Fatalf("exactDecimal: %v", err)
	}
	if got.Cmp(big.NewRat(12345600, 10000)) != 0 {
		t.Errorf("exactDecimal(\"1234.5600\") = %s, want 1234.56", got.RatString())
	}
}

// A group whose sum is not a decimal fails the whole read rather than
// contributing nothing: an error means "could not be read", never "there are
// none".
func TestProjectExpenseSumRefusesAnUnreadableGroup(t *testing.T) {
	t.Parallel()
	sum := &projectExpenseSum{currencies: map[string]*currencyExpenseSum{}}
	err := sum.add(store.ProjectExpenseGroupsRow{
		ProjectID: 1001, Currency: "NOK", Bucket: statusApproved, LineCount: 1,
		CostAmount: "not a number", BillAmount: "0", ReadyAmount: "0", InvoicedAmount: "0",
	})
	if err == nil {
		t.Fatal("add: want an error when a group's sum is not a decimal")
	}
}

// Total is rounded once from the unrounded whole, which is not the same
// number as adding the three published bucket amounts: each of those was
// rounded on its own first. Three buckets of 0.005 publish "0.01" apiece — a
// consumer adding them would report 0.03 for expenses worth 0.02.
func TestProjectExpenseTotalIsRoundedOnceAndNotTheSumOfTheBuckets(t *testing.T) {
	t.Parallel()
	sum := &projectExpenseSum{currencies: map[string]*currencyExpenseSum{}}
	for _, bucket := range []string{statusApproved, statusSubmitted, "draft"} {
		if err := sum.add(store.ProjectExpenseGroupsRow{
			ProjectID: 1001, Currency: "NOK", Bucket: bucket, LineCount: 1,
			CostAmount: "0.005", BillAmount: "0.005", ReadyAmount: "0", InvoicedAmount: "0",
			LastEntryDate: pgtype.Date{Valid: false},
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	totals := sum.totals()
	if len(totals.Currencies) != 1 {
		t.Fatalf("currencies = %v, want one", totals.Currencies)
	}
	nok := totals.Currencies[0]
	for name, bucket := range map[string]struct{ cost, bill string }{
		"approved":  {nok.Approved.CostAmount, nok.Approved.BillAmount},
		"submitted": {nok.Submitted.CostAmount, nok.Submitted.BillAmount},
		"draft":     {nok.Draft.CostAmount, nok.Draft.BillAmount},
	} {
		if bucket.cost != "0.01" || bucket.bill != "0.01" {
			t.Errorf("%s = %q cost and %q bill, want \"0.01\" apiece", name, bucket.cost, bucket.bill)
		}
	}
	// 3 × 0.005 is 0.015, which rounds half away from zero to 0.02 — while
	// the three published buckets add up to 0.03.
	if nok.Total.CostAmount != "0.02" || nok.Total.BillAmount != "0.02" {
		t.Errorf("total = %q cost and %q bill, want \"0.02\" apiece: rounded once from the unrounded whole, not the sum of three rounded buckets",
			nok.Total.CostAmount, nok.Total.BillAmount)
	}
	if nok.Total.Count != 3 {
		t.Errorf("total count = %d, want 3", nok.Total.Count)
	}
}

// A project whose groups carry no date at all reports none rather than the
// zero time formatted as a date. Nothing recorded means the project is
// absent, so this is only reachable from here — and it is what keeps a
// missing date from rendering as "0001-01-01".
func TestProjectExpenseTotalsWithoutADateReportNone(t *testing.T) {
	t.Parallel()
	sum := &projectExpenseSum{currencies: map[string]*currencyExpenseSum{}}
	if err := sum.add(store.ProjectExpenseGroupsRow{
		ProjectID: 1001, Currency: "NOK", Bucket: "draft", LineCount: 1,
		CostAmount: "0", BillAmount: "0", ReadyAmount: "0", InvoicedAmount: "0",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if date := sum.totals().LastEntryDate; date != nil {
		t.Errorf("last entry date = %q, want none", *date)
	}
}
