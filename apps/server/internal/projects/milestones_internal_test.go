package projects

import "testing"

// TestPercentOfPrice pins design §3.2's percent arithmetic: a milestone's
// effective amount is the project's fixed price times the milestone's
// percent, in exact decimal, rounded half up to the two places the column
// stores. It is an internal test because the rule is a pure function and
// every one of these cases would otherwise cost a project, a milestone and
// an HTTP round trip to state — the same reason values_internal_test.go pins
// the task status enumeration here rather than through a request.
//
// The three cases the design names are the reason it cannot be float64
// arithmetic: 100 000.01 at 12.5 % is 12 500.00125, which must round *down*,
// and 999.99 at 50 % is 499.995, which must round *up* — a half cent that
// binary floating point lands a hair below.
func TestPercentOfPrice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		price   string
		percent string
		want    float64
	}{
		{"a third of a round price", "300000.00", "33.33", 99990.00},
		{"a half cent that rounds down", "100000.01", "12.50", 12500.00},
		{"a half cent that rounds up", "999.99", "50.00", 500.00},
		{"the whole price", "250000.00", "100.00", 250000.00},
		{"the smallest price and half of it", "0.01", "50.00", 0.01},
		{"the smallest percent", "1000.00", "0.01", 0.10},
		{"a price the column stores to the cent", "1234.56", "10.00", 123.46},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := percentOfPrice(tc.price, tc.percent)
			if !ok {
				t.Fatalf("percentOfPrice(%q, %q) could not read its operands", tc.price, tc.percent)
			}
			if got != tc.want {
				t.Errorf("percentOfPrice(%q, %q) = %v, want %v", tc.price, tc.percent, got, tc.want)
			}
		})
	}
}

// A text neither operand can be read from is reported rather than silently
// treated as zero: a milestone whose amount came back as 0.00 because the
// price could not be parsed would look like a milestone somebody planned at
// nothing.
func TestPercentOfPrice_Unreadable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ price, percent string }{
		{"", "50.00"},
		{"300000.00", ""},
		{"NaN", "50.00"},
	} {
		if _, ok := percentOfPrice(tc.price, tc.percent); ok {
			t.Errorf("percentOfPrice(%q, %q) reported success, want a refusal", tc.price, tc.percent)
		}
	}
}
