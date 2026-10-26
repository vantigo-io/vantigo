package energy

import "testing"

// TestVATTreatment pins the derivation of design D4: exempt only for a
// household in Norway whose postal code is four digits from 8000 to 9999.
func TestVATTreatment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		household bool
		country   string
		postal    string
		want      string
	}{
		{"a household in Tromsø", true, "NO", "9008", vatExemptHouseholdNorth},
		{"the first code of Nordland", true, "NO", "8000", vatExemptHouseholdNorth},
		{"the last code of Finnmark", true, "NO", "9999", vatExemptHouseholdNorth},
		{"the code before Nordland", true, "NO", "7999", vatStandard},
		{"a household in Oslo", true, "NO", "0150", vatStandard},
		{"a business in Tromsø", false, "NO", "9008", vatStandard},
		{"the country in lower case, the code padded", true, " no", " 8001 ", vatExemptHouseholdNorth},
		{"a Swedish household", true, "SE", "9008", vatStandard},
		{"five digits", true, "NO", "90080", vatStandard},
		{"three digits", true, "NO", "900", vatStandard},
		{"letters", true, "NO", "9OO8", vatStandard},
		{"no postal code", true, "NO", "", vatStandard},
	} {
		if got := vatTreatment(tc.household, tc.country, tc.postal); got != tc.want {
			t.Errorf("%s: vatTreatment(%v, %q, %q) = %q, want %q", tc.name, tc.household, tc.country, tc.postal, got, tc.want)
		}
	}
}
