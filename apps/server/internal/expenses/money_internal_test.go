package expenses

import (
	"math/big"
	"testing"
)

// The arithmetic of design §4, tested where it is written rather than through
// the API: every one of these is a pure function over exact decimals, and the
// cases that matter are the ones a float would get wrong.

// rat is an exact decimal from its text — how a stored numeric reaches these
// functions, never through a float.
func rat(t *testing.T, text string) *big.Rat {
	t.Helper()
	v, ok := new(big.Rat).SetString(text)
	if !ok {
		t.Fatalf("%q is not a decimal", text)
	}
	return v
}

func TestNetOf_IsGrossLessVat(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		gross, vat, want string
	}{
		"no VAT at all":    {"1250.00", "", "1250.00"},
		"VAT of a quarter": {"1250.00", "250.00", "1000.00"},
		"VAT of the lot":   {"1250.00", "1250.00", "0.00"},
		"odd øre":          {"100.03", "20.01", "80.02"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var vat *big.Rat
			if tc.vat != "" {
				vat = rat(t, tc.vat)
			}
			if got := netOf(rat(t, tc.gross), vat); got.Cmp(rat(t, tc.want)) != 0 {
				t.Errorf("netOf(%s, %s) = %s, want %s", tc.gross, tc.vat, got.FloatString(2), tc.want)
			}
		})
	}
}

func TestMileageAmount_IsKilometresTimesTheRatesRoundedOnce(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		km, rate, passengerRate string
		passengers              int
		want                    string
	}{
		"a round trip":        {"120.0", "5.30", "1.00", 0, "636.00"},
		"with two passengers": {"12.3", "5.30", "1.00", 2, "89.79"},
		"no passenger rate":   {"12.3", "5.30", "", 0, "65.19"},
		"rounded half up":     {"0.5", "5.25", "", 0, "2.63"},
		// 8.025 + 0.525: rounding each half first would answer 8.56.
		"rounded once, at the end": {"1.5", "5.35", "0.35", 1, "8.55"},
		"three passengers":         {"3.3", "5.30", "1.00", 3, "27.39"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var passengerRate *big.Rat
			if tc.passengerRate != "" {
				passengerRate = rat(t, tc.passengerRate)
			}
			got := mileageAmount(rat(t, tc.km), rat(t, tc.rate), passengerRate, tc.passengers)
			if got.Cmp(rat(t, tc.want)) != 0 {
				t.Errorf("mileageAmount(%s, %s, %s, %d) = %s, want %s",
					tc.km, tc.rate, tc.passengerRate, tc.passengers, got.FloatString(4), tc.want)
			}
		})
	}
}

func TestOutlayBillAmount_IsNetPlusTheMarkup(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		net, markup, want string
	}{
		"no markup":       {"1000.00", "0", "1000.00"},
		"fifteen percent": {"1000.00", "15", "1150.00"},
		"an odd net":      {"333.33", "15", "383.33"},
		"rounded half up": {"1.05", "10", "1.16"},
		"a big markup":    {"100.00", "1000", "1100.00"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := outlayBillAmount(rat(t, tc.net), rat(t, tc.markup))
			if got.Cmp(rat(t, tc.want)) != 0 {
				t.Errorf("outlayBillAmount(%s, %s) = %s, want %s", tc.net, tc.markup, got.FloatString(4), tc.want)
			}
		})
	}
}

func TestMileageBillAmount_IsKilometresTimesTheCustomerRate(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		km, rate, want string
	}{
		"a round trip":    {"120.0", "7.50", "900.00"},
		"rounded half up": {"1.5", "7.55", "11.33"},
		"an odd distance": {"12.3", "9.00", "110.70"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := mileageBillAmount(rat(t, tc.km), rat(t, tc.rate))
			if got.Cmp(rat(t, tc.want)) != 0 {
				t.Errorf("mileageBillAmount(%s, %s) = %s, want %s", tc.km, tc.rate, got.FloatString(4), tc.want)
			}
		})
	}
}

// Half-up is away from zero on the half, so 2.625 is 2.63 and not 2.62 — the
// rule the whole module's money follows, written down once.
func TestRoundHalfUp_RoundsTheHalfAwayFromZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"2.625", "2.63"},
		{"2.624", "2.62"},
		{"2.635", "2.64"},
		{"0.005", "0.01"},
		{"0.004", "0.00"},
		{"1", "1.00"},
	} {
		if got := decimalText(roundHalfUp(rat(t, tc.in), moneyPlaces), moneyPlaces); got != tc.want {
			t.Errorf("roundHalfUp(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}
