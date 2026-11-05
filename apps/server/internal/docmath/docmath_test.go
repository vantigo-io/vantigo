package docmath_test

import (
	"math/big"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// Two codes at one (category, rate) and SAF-T code whose reasons differ: the
// summary row names the first line's reason, whichever that is.
func TestSummarize_TheFirstLinesReasonNamesTheRow(t *testing.T) {
	t.Parallel()
	a, b := "Grunn A", "Grunn B"
	line := func(reason *string) docmath.TaxedLine {
		return docmath.TaxedLine{Net: big.NewRat(100, 1), Category: "E", Rate: new(big.Rat), SAFT: "6", Reason: reason}
	}
	for _, c := range []struct {
		first, second *string
		want          string
	}{{&a, &b, a}, {&b, &a, b}} {
		rows, _, ambiguous := docmath.Summarize([]docmath.TaxedLine{line(c.first), line(c.second)}, big.NewRat(1, 1))
		if ambiguous || len(rows) != 1 || rows[0].Reason == nil || *rows[0].Reason != c.want {
			var reason any
			if len(rows) > 0 && rows[0].Reason != nil {
				reason = *rows[0].Reason
			}
			t.Errorf("%d rows, ambiguous %v, the first row's reason %v; want one row with %q", len(rows), ambiguous, reason, c.want)
		}
	}
}

// Every rounding is half away from zero, never half to even: 0.125 has an
// even digit before its 5, where the two rules part — banker's rounding would
// give 0.12.
func TestRound2_HalfAwayFromZero(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"0.125": "0.13", "-0.125": "-0.13", "2.505": "2.51", "0.135": "0.14", "0.124": "0.12"} {
		if got := docmath.Round2(rat(in)).FloatString(2); got != want {
			t.Errorf("Round2(%s) = %s, want %s", in, got, want)
		}
	}
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("not a decimal: " + s)
	}
	return r
}

// A line's discount is an allowance rounded on its own, and its net is gross
// minus allowance: 3 × 33.335 is 100.005 → 100.01, 12.5 % of that is 12.50125
// → 12.50, and the net 87.51 — where one rounded product would give 87.50.
func TestLine_GrossMinusAllowance(t *testing.T) {
	t.Parallel()
	got := docmath.Line(rat("3"), rat("33.335"), rat("12.5"))
	for name, c := range map[string]struct{ got, want string }{
		"gross":     {got.Gross.FloatString(2), "100.01"},
		"allowance": {got.Allowance.FloatString(2), "12.50"},
		"net":       {got.Net.FloatString(2), "87.51"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", name, c.got, c.want)
		}
	}
}
