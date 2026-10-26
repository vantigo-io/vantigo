package energy

import "strings"

// The VAT treatment a metering point's consumption is supplied under (energy
// consumption billing design D4, D8): a hint, not a VAT code — Invoices maps
// it to its settings' codes when it builds the document (delivery D).
const (
	vatStandard             = "standard"
	vatExemptHouseholdNorth = "exempt_household_north"
)

// vatTreatment derives the point's VAT treatment, never stored, so a wrong
// postal code shows on the point's page instead of hiding in a column:
// electricity for household use in Nordland, Troms and Finnmark is exempt
// (mval. § 6-6, mvaf § 6-6-1), which the module reads as household set, the
// country NO and a four-digit postal code from 8000 to 9999. Anything else is
// standard.
func vatTreatment(household bool, countryCode, postalCode string) string {
	if !household || !strings.EqualFold(strings.TrimSpace(countryCode), "NO") {
		return vatStandard
	}
	code := strings.TrimSpace(postalCode)
	if len(code) != 4 {
		return vatStandard
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return vatStandard
		}
	}
	if code >= "8000" && code <= "9999" {
		return vatExemptHouseholdNorth
	}
	return vatStandard
}
