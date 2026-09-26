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
