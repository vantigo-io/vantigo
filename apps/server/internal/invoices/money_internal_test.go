package invoices

import (
	"math/big"
	"testing"
)

// Two codes at one (category, rate) and SAF-T code whose reasons differ: the
// summary row names the first line's reason, whichever that is.
func TestSummarize_TheFirstLinesReasonNamesTheRow(t *testing.T) {
	t.Parallel()
	a, b := "Grunn A", "Grunn B"
	line := func(reason *string) taxedLine {
		return taxedLine{net: big.NewRat(100, 1), category: "E", rate: new(big.Rat), safT: "6", reason: reason}
	}
	for _, c := range []struct {
		first, second *string
		want          string
	}{{&a, &b, a}, {&b, &a, b}} {
		rows, _, ambiguous := summarize([]taxedLine{line(c.first), line(c.second)}, big.NewRat(1, 1))
		if ambiguous || len(rows) != 1 || rows[0].reason == nil || *rows[0].reason != c.want {
			var reason any
			if len(rows) > 0 && rows[0].reason != nil {
				reason = *rows[0].reason
			}
			t.Errorf("%d rows, ambiguous %v, the first row's reason %v; want one row with %q", len(rows), ambiguous, reason, c.want)
		}
	}
}

// Every rounding is half away from zero, never half to even (D5): 0.125 has an
// even digit before its 5, where the two rules part — banker's rounding would
// give 0.12.
func TestRound2_HalfAwayFromZero(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"0.125": "0.13", "-0.125": "-0.13", "2.505": "2.51", "0.135": "0.14", "0.124": "0.12"} {
		if got := round2(mustRat(in)).FloatString(2); got != want {
			t.Errorf("round2(%s) = %s, want %s", in, got, want)
		}
	}
}
