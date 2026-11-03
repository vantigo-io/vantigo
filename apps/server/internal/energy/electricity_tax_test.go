package energy

import (
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// TestInTaxZone pins tiltakssonen's postal codes at their borders: the codes
// between zone codes that are not in it, the one that straddles the border,
// and Finnmark from 9501 up, unassigned codes included.
func TestInTaxZone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		postal string
		want   bool
	}{
		{"9046", true},   // Oteren, Storfjord
		{"9049", false},  // Balsfjord/Tromsø
		{"9059", false},  // Tromsø
		{"9060", true},   // Lyngseidet
		{"9061", false},  // unassigned, between zone codes
		{"9069", true},   // Lyngen
		{"9128", false},  // Tromsø
		{"9130", true},   // Karlsøy
		{"9131", false},  // Kårvik, Tromsø
		{"9140", true},   // Rebbenes, straddles Karlsøy/Tromsø
		{"9141", false},  // Melvika, Tromsø
		{"9142", true},   // Skibotn, Storfjord
		{"9170", false},  // Longyearbyen, Svalbard
		{"9178", false},  // Svalbard
		{"9186", true},   // Andsnes, Loppa
		{"9197", true},   // Ullsfjord
		{"9198", false},  // after Nord-Troms
		{"9500", false},  // before Finnmark
		{"9501", true},   // Alta
		{"9526", true},   // unassigned, but in Finnmark's run
		{"9991", true},   // Båtsfjord
		{"9992", true},   // unassigned, every code from 9501 up is Finnmark
		{"9999", true},   // the last four-digit code
		{"9008", false},  // Tromsø
		{"0150", false},  // Oslo
		{" 9501 ", true}, // trimmed
		{"95010", false}, // five digits
		{"950", false},   // three digits
		{"95O1", false},  // a letter
		{"", false},
	} {
		if got, _ := inTaxZone(tc.postal); got != tc.want {
			t.Errorf("inTaxZone(%q) = %v, want %v", tc.postal, got, tc.want)
		}
	}
}

// TestElectricityTaxSuggestion pins the rule of the energy electricity-tax
// suggestion design, branch by branch.
func TestElectricityTaxSuggestion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		household bool
		country   string
		postal    string
		industry  string
		want      electricityTaxHint
	}{
		{"a Swedish point", false, "SE", "9501", "24.420", electricityTaxHint{reason: taxReasonNotNorway}},
		{"no country", true, "", "9501", "", electricityTaxHint{reason: taxReasonNotNorway}},

		{"a household in Alta", true, "NO", "9501", "", electricityTaxHint{taxClassExempt, taxReasonZoneHousehold, false}},
		{"a municipality in Alta", false, "NO", "9501", "84.110", electricityTaxHint{taxClassExempt, taxReasonZonePublicAdmin, false}},
		{"a smelter in Alta", false, "NO", "9501", "24.420", electricityTaxHint{taxClassReduced, taxReasonZoneBusiness, false}},
		{"a shop in Lyngen", false, "NO", "9060", "47.110", electricityTaxHint{taxClassReduced, taxReasonZoneBusiness, false}},
		{"a business in the zone without a code", false, "NO", "9046", "", electricityTaxHint{taxClassReduced, taxReasonZoneBusiness, false}},
		{"a household in Rebbenes", true, "NO", "9140", "", electricityTaxHint{taxClassExempt, taxReasonZoneHousehold, true}},
		{"a business in Rebbenes", false, "NO", "9140", "47.110", electricityTaxHint{taxClassReduced, taxReasonZoneBusiness, true}},
		{"the country in lower case, the code padded", true, " no ", " 9501 ", "", electricityTaxHint{taxClassExempt, taxReasonZoneHousehold, false}},

		{"a household in Tromsø", true, "NO", "9008", "", electricityTaxHint{taxClassOrdinary, taxReasonHousehold, false}},
		{"a household in Kårvik", true, "NO", "9131", "", electricityTaxHint{taxClassOrdinary, taxReasonHousehold, false}},
		{"a household with a code", true, "NO", "0150", "24.420", electricityTaxHint{taxClassOrdinary, taxReasonHousehold, false}},
		{"a smelter in Oslo", false, "NO", "0150", "24.420", electricityTaxHint{taxClassReduced, taxReasonIndustry, false}},
		{"a code without dots", false, "NO", "0150", "24420", electricityTaxHint{taxClassReduced, taxReasonIndustry, false}},
		{"a non-breaking space in the code", false, "NO", "0150", "24.\u00a0420", electricityTaxHint{taxClassReduced, taxReasonIndustry, false}},
		{"a code of only a non-breaking space", false, "NO", "0150", "\u00a0", electricityTaxHint{taxClassOrdinary, taxReasonNoIndustryCode, true}},
		{"the first division of section B", false, "NO", "0150", "05.100", electricityTaxHint{taxClassReduced, taxReasonIndustry, false}},
		{"the last division of section C", false, "NO", "0150", "33.120", electricityTaxHint{taxClassReduced, taxReasonIndustry, false}},
		{"the division before section B", false, "NO", "0150", "03.110", electricityTaxHint{taxClassOrdinary, taxReasonOrdinary, false}},
		{"the division after section C", false, "NO", "0150", "34.000", electricityTaxHint{taxClassOrdinary, taxReasonOrdinary, false}},
		{"recycling, SN2025", false, "NO", "0150", "38.210", electricityTaxHint{taxClassReduced, taxReasonIndustry, false}},
		{"recycling, SN2007", false, "NO", "0150", "38.320", electricityTaxHint{taxClassReduced, taxReasonIndustry, false}},
		{"waste collection", false, "NO", "0150", "38.110", electricityTaxHint{taxClassOrdinary, taxReasonOrdinary, false}},
		{"district heating", false, "NO", "0150", "35.300", electricityTaxHint{taxClassOrdinary, taxReasonIndustryMayQualify, true}},
		{"power production", false, "NO", "0150", "35.111", electricityTaxHint{taxClassOrdinary, taxReasonIndustryMayQualify, true}},
		{"power distribution", false, "NO", "0150", "35.130", electricityTaxHint{taxClassOrdinary, taxReasonOrdinary, false}},
		{"biogas", false, "NO", "0150", "35.210", electricityTaxHint{taxClassOrdinary, taxReasonIndustryMayQualify, true}},
		{"a greenhouse", false, "NO", "0150", "01.130", electricityTaxHint{taxClassOrdinary, taxReasonIndustryMayQualify, true}},
		{"a railway", false, "NO", "0150", "49.100", electricityTaxHint{taxClassOrdinary, taxReasonIndustryMayQualify, true}},
		{"a tram", false, "NO", "0150", "49.310", electricityTaxHint{taxClassOrdinary, taxReasonIndustryMayQualify, true}},
		{"a bus company", false, "NO", "0150", "49.390", electricityTaxHint{taxClassOrdinary, taxReasonOrdinary, false}},
		{"rail infrastructure", false, "NO", "0150", "52.210", electricityTaxHint{taxClassOrdinary, taxReasonIndustryMayQualify, true}},
		{"work training", false, "NO", "0150", "88.993", electricityTaxHint{taxClassOrdinary, taxReasonIndustryMayQualify, true}},
		{"a data centre", false, "NO", "0150", "63.110", electricityTaxHint{taxClassOrdinary, taxReasonOrdinary, false}},
		{"a municipality in Oslo", false, "NO", "0150", "84.110", electricityTaxHint{taxClassOrdinary, taxReasonOrdinary, false}},
		{"no code", false, "NO", "0150", "", electricityTaxHint{taxClassOrdinary, taxReasonNoIndustryCode, true}},
		{"a blank code", false, "NO", "0150", " . ", electricityTaxHint{taxClassOrdinary, taxReasonNoIndustryCode, true}},
		{"Svalbard", false, "NO", "9170", "47.110", electricityTaxHint{taxClassOrdinary, taxReasonOrdinary, false}},
	} {
		if got := electricityTaxSuggestion(tc.household, tc.country, tc.postal, tc.industry); got != tc.want {
			t.Errorf("%s: electricityTaxSuggestion(%v, %q, %q, %q) = %+v, want %+v",
				tc.name, tc.household, tc.country, tc.postal, tc.industry, got, tc.want)
		}
	}
}

// TestCurrentCustomerID pins whose industry code a point's suggestion reads:
// the Active period covering now, else the latest non-cancelled by start,
// else none.
func TestCurrentCustomerID(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return now.AddDate(0, 0, d) }
	at := func(d int) *time.Time { v := day(d); return &v }
	period := func(customer int32, status string, start int, end *time.Time) store.EnergySupplyPeriod {
		return store.EnergySupplyPeriod{CustomerID: customer, Status: status, Start: day(start), End: end}
	}
	for _, tc := range []struct {
		name    string
		periods []store.EnergySupplyPeriod
		want    int32 // 0 for none
	}{
		{"no periods", nil, 0},
		{"one active period", []store.EnergySupplyPeriod{period(1, statusActive, -10, nil)}, 1},
		{"the active one before a later future one", []store.EnergySupplyPeriod{
			period(1, statusActive, -10, at(5)), period(2, statusActive, 5, nil),
		}, 1},
		{"an ended one, then nothing", []store.EnergySupplyPeriod{
			period(1, statusEnded, -30, at(-20)), period(2, statusEnded, -20, at(-1)),
		}, 2},
		{"a cancelled latest is skipped", []store.EnergySupplyPeriod{
			period(1, statusEnded, -30, at(-20)), period(2, statusCancelled, -20, nil),
		}, 1},
		{"a future period with none now", []store.EnergySupplyPeriod{
			period(1, statusEnded, -30, at(-20)), period(2, statusActive, 5, nil),
		}, 2},
		{"only cancelled", []store.EnergySupplyPeriod{period(1, statusCancelled, -10, nil)}, 0},
	} {
		got := currentCustomerID(tc.periods, now)
		switch {
		case tc.want == 0 && got != nil:
			t.Errorf("%s: currentCustomerID = %d, want none", tc.name, *got)
		case tc.want != 0 && (got == nil || *got != tc.want):
			t.Errorf("%s: currentCustomerID = %v, want %d", tc.name, got, tc.want)
		}
	}
}
