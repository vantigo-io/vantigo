package timetracking

import (
	"math/big"
	"testing"
)

// The money rendering of contracts.ProjectActuals, without a database: the
// rounding rule the whole contract rests on is worth pinning where it can be
// read, and the negative and very large cases no seeded entry can reach are
// only reachable here.

func TestAmountText(t *testing.T) {
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
		// Half away from zero, not to even: 0.005 is a cent, and so is
		// 0.015 — banker's rounding would make the second 0.02 and the
		// first 0.00.
		{"a half rounds away from zero", big.NewRat(5, 1000), "0.01"},
		{"a half above an even cent", big.NewRat(15, 1000), "0.02"},
		{"just under a half", big.NewRat(4999, 1000000), "0.00"},
		{"a third", big.NewRat(1, 3), "0.33"},
		{"two thirds", big.NewRat(2, 3), "0.67"},
		// Negative: no rate is negative today, but a consumer subtracting
		// one figure from another would be the first to find out otherwise.
		{"a negative half rounds away from zero", big.NewRat(-5, 1000), "-0.01"},
		{"a negative amount", big.NewRat(-123456, 100), "-1234.56"},
		{"a negative below one", big.NewRat(-1, 100), "-0.01"},
		// Past float64's exact integers, where a float-based path would
		// already have started lying.
		{"beyond 2^53", new(big.Rat).SetFrac(big.NewInt(900719925474099301), big.NewInt(100)), "9007199254740993.01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := amountText(c.amount); got != c.want {
				t.Errorf("amountText(%s) = %q, want %q", c.amount.RatString(), got, c.want)
			}
		})
	}
}

func TestHundredthsText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hundredths int64
		want       string
	}{
		{0, "0.00"},
		{1, "0.01"},
		{9, "0.09"},
		{10, "0.10"},
		{99, "0.99"},
		{100, "1.00"},
		{101, "1.01"},
		{123456, "1234.56"},
		{-1, "-0.01"},
		{-100, "-1.00"},
		{-123456, "-1234.56"},
	}
	for _, c := range cases {
		if got := hundredthsText(big.NewInt(c.hundredths)); got != c.want {
			t.Errorf("hundredthsText(%d) = %q, want %q", c.hundredths, got, c.want)
		}
	}
}

// A sum PostgreSQL cannot have produced is reported, not silently read as
// nothing: a zero where an amount belongs is the one answer this contract
// must never invent.
func TestExactAmountRefusesWhatIsNotADecimal(t *testing.T) {
	t.Parallel()
	if _, err := exactAmount("NaN"); err == nil {
		t.Error("exactAmount(\"NaN\"): want an error")
	}
	got, err := exactAmount("1234.5600")
	if err != nil {
		t.Fatalf("exactAmount: %v", err)
	}
	if got.Cmp(big.NewRat(12345600, 10000)) != 0 {
		t.Errorf("exactAmount(\"1234.5600\") = %s, want 1234.56", got.RatString())
	}
}
