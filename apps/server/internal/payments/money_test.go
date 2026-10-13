package payments_test

import (
	"math"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/payments"
)

// Money is minor units exactly: what goes in as øre comes out as øre, and its
// words are integer arithmetic — no float ever rounds an amount, at the
// edges of the provider's range or at int64's.
func TestMoney_MinorExact(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		minor int64
		want  string
	}{
		{0, "0.00 NOK"},
		{1, "0.01 NOK"},
		{99, "0.99 NOK"},
		{100, "1.00 NOK"},
		{151, "1.51 NOK"},
		{65_000_000, "650000.00 NOK"},
		{65_000_001, "650000.01 NOK"},
		{-5, "-0.05 NOK"},
		{-12345, "-123.45 NOK"},
		{math.MaxInt64, "92233720368547758.07 NOK"},
		{math.MinInt64, "-92233720368547758.08 NOK"},
	} {
		m := payments.NOK(c.minor)
		if m.Currency != "NOK" || m.Minor != c.minor {
			t.Errorf("NOK(%d) = %+v, want NOK and the same øre", c.minor, m)
		}
		if got := m.String(); got != c.want {
			t.Errorf("NOK(%d).String() = %q, want %q", c.minor, got, c.want)
		}
	}
}
