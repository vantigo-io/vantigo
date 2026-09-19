package projects

import (
	"math/big"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// These are economy_math.go's rules stated as arithmetic, not as requests:
// design §2 E8's one definition of "budget used", the thresholds the
// dashboard raises a project on, and the exactness both of them depend on.
// They are internal tests for the reason milestones_internal_test.go is one —
// every case here would otherwise cost a project, a time entry and an HTTP
// round trip to state — and they matter more than most, because the portfolio
// and the dashboard answer from these same functions.

// rat is a decimal as the exact rational its text spells. A test that wrote
// big.NewRat(1, 3) would be stating something the contract cannot carry;
// every number here is a decimal somebody could have typed.
func rat(t *testing.T, text string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		t.Fatalf("%q is not a decimal", text)
	}
	return r
}

// sums builds the three buckets from decimal texts: approved, submitted and
// draft hours, then the same three bill amounts. Total is the three added up,
// which is what a provider answers whenever nothing lands on a rounding
// boundary — these tests are about the comparison, not about the boundary.
func sums(t *testing.T, approvedHours, submittedHours, draftHours, approvedAmount, submittedAmount, draftAmount string) bucketSums {
	t.Helper()
	out := bucketSums{
		Approved:  bucketSum{Hours: rat(t, approvedHours), Amount: rat(t, approvedAmount)},
		Submitted: bucketSum{Hours: rat(t, submittedHours), Amount: rat(t, submittedAmount)},
		Draft:     bucketSum{Hours: rat(t, draftHours), Amount: rat(t, draftAmount)},
	}
	out.Total = bucketSum{Hours: new(big.Rat), Amount: new(big.Rat)}
	for _, bucket := range []bucketSum{out.Approved, out.Submitted, out.Draft} {
		out.Total.Hours.Add(out.Total.Hours, bucket.Hours)
		out.Total.Amount.Add(out.Total.Amount, bucket.Amount)
	}
	return out
}

// TestBudgetUsedPicksTheBasisInOrder pins design §2 E8's fall-through: the
// project's budget amount, then the fixed price of a *fixed-price* project,
// then the budget hours — and the two amount bases skipped entirely for a
// caller who may not see amounts, so no percentage of money ever reaches
// them.
func TestBudgetUsedPicksTheBasisInOrder(t *testing.T) {
	t.Parallel()

	// 10 h and 1 000.00 logged, so every basis below answers a different
	// percentage and a test can never pass on the wrong one by coincidence.
	logged := sums(t, "5", "3", "2", "500.00", "300.00", "200.00")

	cases := []struct {
		name    string
		basis   budgetBasis
		want    string
		percent float64
	}{
		{
			name: "the budget amount comes first",
			basis: budgetBasis{
				Amount: rat(t, "2000.00"), FixedPrice: rat(t, "4000.00"), Hours: rat(t, "40"),
				BillingType: billingFixedPrice, SeesAmounts: true,
			},
			want: basisAmount, percent: 50,
		},
		{
			name: "the fixed price comes second",
			basis: budgetBasis{
				FixedPrice: rat(t, "4000.00"), Hours: rat(t, "40"),
				BillingType: billingFixedPrice, SeesAmounts: true,
			},
			want: basisFixedPrice, percent: 25,
		},
		{
			name: "a fixed price on a project that does not bill one is no basis",
			basis: budgetBasis{
				FixedPrice: rat(t, "4000.00"), Hours: rat(t, "40"),
				BillingType: billingTimeAndMaterials, SeesAmounts: true,
			},
			want: basisHours, percent: 25,
		},
		{
			name:  "the budget hours come last",
			basis: budgetBasis{Hours: rat(t, "40"), BillingType: billingTimeAndMaterials, SeesAmounts: true},
			want:  basisHours, percent: 25,
		},
		{
			name: "a caller without financial rights only ever gets the hours",
			basis: budgetBasis{
				Amount: rat(t, "2000.00"), FixedPrice: rat(t, "4000.00"), Hours: rat(t, "40"),
				BillingType: billingFixedPrice, SeesAmounts: false,
			},
			want: basisHours, percent: 25,
		},
		{
			name: "a budget of nothing is no basis, it is a division by zero",
			basis: budgetBasis{
				Amount: rat(t, "0.00"), Hours: rat(t, "40"),
				BillingType: billingTimeAndMaterials, SeesAmounts: true,
			},
			want: basisHours, percent: 25,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := budgetUsed(tc.basis, logged)
			if !ok {
				t.Fatalf("budgetUsed reported no basis, want %s", tc.want)
			}
			if got.Basis != tc.want {
				t.Errorf("Basis = %q, want %q", got.Basis, tc.want)
			}
			if got.Percent != tc.percent {
				t.Errorf("Percent = %v, want %v", got.Percent, tc.percent)
			}
		})
	}
}

// A project with nothing to measure against has no percentage at all — not a
// percentage of zero, which would read as a project that has used nothing.
func TestBudgetUsedWithoutABasis(t *testing.T) {
	t.Parallel()

	logged := sums(t, "5", "3", "2", "500.00", "300.00", "200.00")
	cases := map[string]budgetBasis{
		"nothing set at all":                      {BillingType: billingTimeAndMaterials, SeesAmounts: true},
		"amounts the caller may not see":          {Amount: rat(t, "2000.00"), BillingType: billingTimeAndMaterials},
		"a fixed price the caller may not see":    {FixedPrice: rat(t, "4000.00"), BillingType: billingFixedPrice},
		"a budget of zero hours and nothing else": {Hours: rat(t, "0"), BillingType: billingTimeAndMaterials, SeesAmounts: true},
	}
	for name, basis := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got, ok := budgetUsed(basis, logged); ok {
				t.Errorf("budgetUsed = %+v, true; want no basis", got)
			}
		})
	}
}

// The approved bucket is measured against the same basis as the whole, and
// against the basis's own unit: an amount basis compares amounts, the hours
// basis compares hours. Getting the unit wrong is the mistake this test
// exists for — the two are deliberately different numbers here.
func TestBudgetUsedComparesTheBasisOwnUnit(t *testing.T) {
	t.Parallel()

	logged := sums(t, "5", "3", "2", "500.00", "300.00", "200.00")

	amount, ok := budgetUsed(budgetBasis{
		Amount: rat(t, "2000.00"), Hours: rat(t, "40"),
		BillingType: billingTimeAndMaterials, SeesAmounts: true,
	}, logged)
	if !ok || amount.Percent != 50 || amount.ApprovedPercent != 25 {
		t.Errorf("on the amount basis: %+v, want 50 %% used and 25 %% approved", amount)
	}

	hours, ok := budgetUsed(budgetBasis{
		Hours: rat(t, "40"), BillingType: billingTimeAndMaterials, SeesAmounts: true,
	}, logged)
	if !ok || hours.Percent != 25 || hours.ApprovedPercent != 12.5 {
		t.Errorf("on the hours basis: %+v, want 25 %% used and 12.5 %% approved", hours)
	}
}

// The percentage is rounded half up to one decimal, in exact decimal. Both
// cases here are halves float64 cannot hold: 0.35 is stored a hair below and
// so rounds *down* in binary, and 8.45 does the same — a float64
// implementation answers 0.3 and 8.4 and fails this test.
func TestBudgetUsedRoundsHalfUpToOneDecimal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		used   string
		budget string
		want   float64
	}{
		{"a half a float64 holds just below", "35.00", "10000.00", 0.4},
		{"another half a float64 holds just below", "845.00", "10000.00", 8.5},
		{"a half at the tenth, upwards", "1225.00", "10000.00", 12.3},
		{"a third of the budget, cut at one decimal", "1000.00", "3000.00", 33.3},
		{"two thirds of the budget, rounded up", "2000.00", "3000.00", 66.7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := budgetUsed(
				budgetBasis{Amount: rat(t, tc.budget), BillingType: billingTimeAndMaterials, SeesAmounts: true},
				sums(t, "0", "0", "0", tc.used, "0.00", "0.00"))
			if !ok {
				t.Fatal("budgetUsed reported no basis")
			}
			if got.Percent != tc.want {
				t.Errorf("Percent = %v, want %v", got.Percent, tc.want)
			}
		})
	}
}

// overBudget is decided on the exact ratio, never on the rounded percent: a
// project at 100.04 % prints 100.0 and is over budget all the same, and one
// at 99.96 % prints 100.0 and is not.
func TestOverBudgetIsDecidedOnTheExactRatio(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		used    string
		percent float64
		over    bool
	}{
		{"a hair over prints a round hundred", "10004.00", 100, true},
		{"exactly the budget is not over it", "10000.00", 100, false},
		{"a hair under prints a round hundred too", "9996.00", 100, false},
		{"a cent over", "10000.01", 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := budgetUsed(
				budgetBasis{Amount: rat(t, "10000.00"), BillingType: billingTimeAndMaterials, SeesAmounts: true},
				sums(t, "0", "0", "0", tc.used, "0.00", "0.00"))
			if !ok {
				t.Fatal("budgetUsed reported no basis")
			}
			if got.Percent != tc.percent {
				t.Errorf("Percent = %v, want %v", got.Percent, tc.percent)
			}
			if got.OverBudget != tc.over {
				t.Errorf("OverBudget = %v, want %v (the exact ratio is %s)", got.OverBudget, tc.over, got.Ratio)
			}
		})
	}
}

// The three bands the dashboard raises a project on: nothing below 80 %, a
// warning from 80 % through 100 % inclusive, exceeded past it. Decided on the
// exact ratio, which is why 100.04 % — printed as 100.0 — is exceeded.
func TestBudgetLevelThresholds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		used string
		want string
	}{
		{"7999.00", budgetLevelNone},
		{"8000.00", budgetLevelWarning},
		{"9000.00", budgetLevelWarning},
		{"10000.00", budgetLevelWarning},
		{"10001.00", budgetLevelExceeded},
		{"10004.00", budgetLevelExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.used, func(t *testing.T) {
			t.Parallel()
			use, ok := budgetUsed(
				budgetBasis{Amount: rat(t, "10000.00"), BillingType: billingTimeAndMaterials, SeesAmounts: true},
				sums(t, "0", "0", "0", tc.used, "0.00", "0.00"))
			if !ok {
				t.Fatal("budgetUsed reported no basis")
			}
			if got := budgetLevel(use); got != tc.want {
				t.Errorf("budgetLevel(%s%%) = %q, want %q", tc.used, got, tc.want)
			}
		})
	}
	if got := budgetLevel(budgetUse{}); got != budgetLevelNone {
		t.Errorf("budgetLevel of a project with no basis = %q, want none", got)
	}
}

// A billing line has no fixed price of its own: its own budget amount when
// the caller may see amounts, then its budget hours, then nothing.
func TestLineBudgetUsed(t *testing.T) {
	t.Parallel()

	logged := sums(t, "5", "3", "2", "500.00", "300.00", "200.00")

	if got, ok := lineBudgetUsed(rat(t, "2000.00"), rat(t, "40"), true, logged); !ok ||
		got.Basis != basisAmount || got.Percent != 50 {
		t.Errorf("with amounts: %+v, %v; want the amount basis at 50 %%", got, ok)
	}
	if got, ok := lineBudgetUsed(rat(t, "2000.00"), rat(t, "40"), false, logged); !ok ||
		got.Basis != basisHours || got.Percent != 25 {
		t.Errorf("without amounts: %+v, %v; want the hours basis at 25 %%", got, ok)
	}
	if got, ok := lineBudgetUsed(nil, nil, true, logged); ok {
		t.Errorf("with no budget at all: %+v, true; want no basis", got)
	}
}

// Every figure is read in exact decimal, the provider's own Total included —
// and the total is read rather than derived: the contract computes it from the
// unrounded whole, so it can legitimately differ from the three published
// buckets added up, and taking the sum instead would quietly report the number
// the contract exists to avoid.
func TestBucketSumsReadTheProvidersTotal(t *testing.T) {
	t.Parallel()

	got, err := bucketSumsOf(contracts.ActualsTotals{
		Approved:  contracts.ActualsBucket{HoursHundredths: 10, BillAmount: "0.10", CostAmount: "0.05"},
		Submitted: contracts.ActualsBucket{HoursHundredths: 20, BillAmount: "0.20", CostAmount: "0.05"},
		Draft:     contracts.ActualsBucket{HoursHundredths: 5, BillAmount: "0.00", CostAmount: "0.05"},
		Total:     contracts.ActualsBucket{HoursHundredths: 35, BillAmount: "0.29", CostAmount: "0.14"},
	})
	if err != nil {
		t.Fatalf("bucketSumsOf: %v", err)
	}
	if amount := decimalNumber(got.totalAmount()); amount != 0.29 {
		t.Errorf("total bill amount = %v, want the provider's 0.29 rather than 0.3 from adding the buckets", amount)
	}
	if hours := decimalNumber(got.totalHours()); hours != 0.35 {
		t.Errorf("total hours = %v, want 0.35", hours)
	}
	if got.Approved.Amount == nil || got.Approved.Amount.Cmp(big.NewRat(1, 10)) != 0 {
		t.Errorf("the approved bucket's amount is %s, want exactly 1/10", got.Approved.Amount)
	}
}

// costSumsOf is the same three buckets read from the cost side, so a margin
// is never accidentally computed from the bill amounts twice.
func TestCostSumsReadTheCostAmounts(t *testing.T) {
	t.Parallel()

	got, err := costSumsOf(contracts.ActualsTotals{
		Approved:  contracts.ActualsBucket{HoursHundredths: 100, BillAmount: "900.00", CostAmount: "400.00"},
		Submitted: contracts.ActualsBucket{HoursHundredths: 100, BillAmount: "900.00", CostAmount: "0.00"},
		Draft:     contracts.ActualsBucket{HoursHundredths: 0, BillAmount: "0.00", CostAmount: "0.00"},
		Total:     contracts.ActualsBucket{HoursHundredths: 200, BillAmount: "1800.00", CostAmount: "400.00"},
	})
	if err != nil {
		t.Fatalf("costSumsOf: %v", err)
	}
	if total := decimalNumber(got.totalAmount()); total != 400 {
		t.Errorf("total cost = %v, want 400", total)
	}
}

// An amount a provider spelled in a text nothing can read is an error, never
// a zero: a margin computed from a number that was silently dropped is worse
// than no answer.
func TestExactAmountRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	if got, err := exactAmount(""); err != nil || got.Sign() != 0 {
		t.Errorf("exactAmount(\"\") = %v, %v; want zero and no error", got, err)
	}
	if _, err := exactAmount("not a number"); err == nil {
		t.Error("exactAmount accepted a text that is not a decimal")
	}
	if _, err := bucketSumsOf(contracts.ActualsTotals{
		Approved: contracts.ActualsBucket{BillAmount: "oops"},
	}); err == nil {
		t.Error("bucketSumsOf accepted a bucket whose amount is not a decimal")
	}
}

// Hours arrive as int64 hundredths and stay exact: 125 is 1.25 h, and a
// quarter of an hour is a quarter, not 0.2499999999999999.
func TestHoursAreExact(t *testing.T) {
	t.Parallel()

	if got := decimalNumber(exactHours(125)); got != 1.25 {
		t.Errorf("125 hundredths = %v h, want 1.25", got)
	}
	if got := decimalNumber(exactHours(0)); got != 0 {
		t.Errorf("0 hundredths = %v h, want 0", got)
	}
	if got := exactHours(25); got.Cmp(big.NewRat(1, 4)) != 0 {
		t.Errorf("exactHours(25) = %s, want exactly 1/4", got)
	}
}

// What is left of a budget in hours never goes negative: a line past its
// budget has nothing left and says the rest through overBudget.
func TestRemainingHoursClampsAtZero(t *testing.T) {
	t.Parallel()

	if got := remainingHours(rat(t, "40"), rat(t, "12.25")); got != 27.75 {
		t.Errorf("remainingHours = %v, want 27.75", got)
	}
	if got := remainingHours(rat(t, "40"), rat(t, "41.5")); got != 0 {
		t.Errorf("remainingHours past the budget = %v, want 0", got)
	}
	if got := remainingHours(rat(t, "40"), rat(t, "40")); got != 0 {
		t.Errorf("remainingHours at the budget = %v, want 0", got)
	}
}
