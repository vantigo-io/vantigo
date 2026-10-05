package projects

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

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

// TestMilestoneEffectiveAmountRat_HalfUpToCents pins the one figure invoicing
// reads and writes of a milestone (invoices work design D1, D3): the effective
// amount as an exact decimal, a percent rounded half up to cents by
// percentOfPrice's own rule — 33.33 % of 3 750.30 is 1 249.974 999, so
// 1 249.97, and 50 % of 999.99 is exactly 499.995, so 500.00 — and the same
// figure percentOfPrice answers. A frozen amount wins over the flat one, and
// a percent with no fixed price is errMilestoneUnpriced.
func TestMilestoneEffectiveAmountRat_HalfUpToCents(t *testing.T) {
	t.Parallel()
	numeric := func(text string) pgtype.Numeric {
		var n pgtype.Numeric
		if err := n.Scan(text); err != nil {
			t.Fatalf("numeric %q: %v", text, err)
		}
		return n
	}

	for _, tc := range []struct {
		name      string
		milestone store.ProjectsBillingMilestone
		price     string
		want      string
	}{
		{"a third of a price with cents", store.ProjectsBillingMilestone{Percent: numeric("33.33")}, "3750.30", "1249.97"},
		{"a half cent rounds up", store.ProjectsBillingMilestone{Percent: numeric("50.00")}, "999.99", "500.00"},
		{"a flat amount as entered", store.ProjectsBillingMilestone{Amount: numeric("1234.50")}, "", "1234.50"},
		{"the frozen amount over the flat one", store.ProjectsBillingMilestone{Amount: numeric("1234.50"), InvoicedAmount: numeric("1000.00")}, "", "1000.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := store.ProjectsProject{}
			if tc.price != "" {
				project.FixedPriceAmount = numeric(tc.price)
			}
			got, err := milestoneEffectiveAmountRat(tc.milestone, project)
			if err != nil {
				t.Fatalf("milestoneEffectiveAmountRat: %v", err)
			}
			if got.FloatString(2) != tc.want {
				t.Errorf("= %s, want %s", got.FloatString(2), tc.want)
			}
			if exact := got.FloatString(10); exact != tc.want+"00000000" {
				t.Errorf("= %s exactly, want %s to the cent and nothing past it", exact, tc.want)
			}
			if tc.price != "" {
				percent, _, _ := numericText(tc.milestone.Percent)
				float, _ := percentOfPrice(tc.price, percent)
				if f, _ := got.Float64(); f != float {
					t.Errorf("= %v, but percentOfPrice answers %v", f, float)
				}
			}
		})
	}

	if _, err := milestoneEffectiveAmountRat(store.ProjectsBillingMilestone{Percent: numeric("25.00")}, store.ProjectsProject{}); !errors.Is(err, errMilestoneUnpriced) {
		t.Errorf("a percent with no fixed price: %v, want errMilestoneUnpriced", err)
	}
}
