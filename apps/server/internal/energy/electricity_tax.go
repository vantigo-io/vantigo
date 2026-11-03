package energy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// Why electricityTaxSuggestion suggests what it does (energy electricity-tax
// suggestion design): the API's reason, one sentence each in the UI.
const (
	taxReasonNotNorway          = "not_norway"
	taxReasonZoneHousehold      = "zone_household"
	taxReasonZonePublicAdmin    = "zone_public_administration"
	taxReasonZoneBusiness       = "zone_business"
	taxReasonHousehold          = "household"
	taxReasonIndustry           = "industry"
	taxReasonIndustryMayQualify = "industry_may_qualify"
	taxReasonOrdinary           = "ordinary"
	taxReasonNoIndustryCode     = "no_industry_code"
)

// electricityTaxHint is electricityTaxSuggestion's answer: class is empty when
// there is no suggestion.
type electricityTaxHint struct {
	class     string
	reason    string
	uncertain bool
}

// taxZoneRanges are the four-digit postal codes in tiltakssonen — Finnmark and
// the Troms municipalities Karlsøy, Kvænangen, Kåfjord, Lyngen, Nordreisa,
// Skjervøy and Storfjord (Stortingets vedtak om avgift på elektrisk kraft
// 2026 § 1 annet ledd b and § 3 a) — inclusive, ascending. Nord-Troms is the
// exact list Bring's postal register maps to those municipalities, not a
// prefix: 9049–9059, 9100–9128, 9131, 9141 and 9170–9178 (Svalbard) sit
// between zone codes and are not in it. Finnmark is every code from 9501 up,
// 9992–9999 included although Bring has assigned none of them yet, since all
// of 95xx–99xx is Finnmark. Research:
// docs/superpowers/research/2026-11-03-energy-electricity-tax-class.md.
var taxZoneRanges = [][2]int{
	{9046, 9046}, {9060, 9060}, {9062, 9062}, {9064, 9064}, {9068, 9069},
	{9130, 9130}, {9132, 9132}, {9134, 9138}, {9140, 9140}, {9142, 9149},
	{9151, 9159}, {9161, 9163}, {9169, 9169}, {9180, 9182}, {9184, 9187},
	{9189, 9190}, {9192, 9195}, {9197, 9197}, {9501, 9999},
}

// taxZoneStraddlingCode is the one zone postal code whose addresses also lie
// outside the zone: 9140 Rebbenes, Karlsøy's but with about four in ten of its
// addresses in Tromsø.
const taxZoneStraddlingCode = 9140

// inTaxZone reports whether postalCode, trimmed, is a four-digit code in
// taxZoneRanges, and the code read.
func inTaxZone(postalCode string) (bool, int) {
	code := strings.TrimSpace(postalCode)
	if len(code) != 4 {
		return false, 0
	}
	n := 0
	for _, r := range code {
		if r < '0' || r > '9' {
			return false, 0
		}
		n = n*10 + int(r-'0')
	}
	for _, rg := range taxZoneRanges {
		if n >= rg[0] && n <= rg[1] {
			return true, n
		}
	}
	return false, n
}

// industryDigits is an industry code's digits, dots and whitespace dropped
// ("24.420" → "24420"), the form the rule compares prefixes of.
func industryDigits(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, code)
}

// taxReducedIndustryCodes are the codes outside sections B and C that saf.
// § 3-12-4 lists: 38.21 materialgjenvinning (SN2025) and 38.32, its SN2007
// code.
var taxReducedIndustryCodes = []string{"3821", "3832"}

// taxMayQualifyIndustryCodes are the codes whose holders may qualify for a
// reduced rate or an exemption that the code alone cannot decide: power
// production's own use (35.11, saf. § 3-12-1 (3)), gas and biogas
// manufacture (35.2, § 3-12-8), district heating (35.30, § 3-12-5),
// greenhouses with electric heating (01.13, 01.19, 01.30, § 3-12-14),
// traction power for rail, trams and metro (49.1, 49.2, 49.31, 52.21,
// § 3-12-16) and work training of an industrial nature (88.99, § 3-12-4 d).
var taxMayQualifyIndustryCodes = []string{"3511", "352", "3530", "0113", "0119", "0130", "491", "492", "4931", "5221", "8899"}

// hasAnyPrefix reports whether digits starts with one of prefixes.
func hasAnyPrefix(digits string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(digits, p) {
			return true
		}
	}
	return false
}

// inSectionBOrC reports whether digits is in section B (divisions 05–09) or C
// (10–33) of the industry classification.
func inSectionBOrC(digits string) bool {
	if len(digits) < 2 || digits[0] < '0' || digits[0] > '9' || digits[1] < '0' || digits[1] > '9' {
		return false
	}
	division := int(digits[0]-'0')*10 + int(digits[1]-'0')
	return division >= 5 && division <= 33
}

// electricityTaxSuggestion is the class the rules point to for a metering
// point (energy electricity-tax suggestion design), derived on every read and
// never stored: the stored class stays what settlements bill. industryCode is
// the point's current customer's, empty when it has no customer or the
// customer no code.
//
//   - Outside Norway there is no suggestion.
//   - In tiltakssonen (inTaxZone) a household is exempt and so is public
//     administration, read as division 84 — a heuristic, the vedtak says
//     "offentlig forvaltning" (§ 3 a); every other consumer is reduced (§ 1
//     annet ledd b). 9140 straddles the zone border and is uncertain.
//   - Elsewhere a household is ordinary; sections B and C and 38.21/38.32 are
//     reduced (saf. § 3-12-4) — for the power used in the production process,
//     which the UI says; a code that may qualify for something it cannot decide
//     is ordinary and uncertain, any other code ordinary, and no code ordinary
//     and uncertain.
//
// Process exemptions (saf. § 3-12-13: chemical reduction, electrolysis,
// metallurgy, mineralogy) are never suggested: they hang on the process, not
// the code.
func electricityTaxSuggestion(household bool, countryCode, postalCode, industryCode string) electricityTaxHint {
	if !strings.EqualFold(strings.TrimSpace(countryCode), "NO") {
		return electricityTaxHint{reason: taxReasonNotNorway}
	}
	digits := industryDigits(industryCode)
	if zone, code := inTaxZone(postalCode); zone {
		straddles := code == taxZoneStraddlingCode
		switch {
		case household:
			return electricityTaxHint{class: taxClassExempt, reason: taxReasonZoneHousehold, uncertain: straddles}
		case strings.HasPrefix(digits, "84"):
			return electricityTaxHint{class: taxClassExempt, reason: taxReasonZonePublicAdmin, uncertain: straddles}
		default:
			return electricityTaxHint{class: taxClassReduced, reason: taxReasonZoneBusiness, uncertain: straddles}
		}
	}
	switch {
	case household:
		return electricityTaxHint{class: taxClassOrdinary, reason: taxReasonHousehold}
	case digits == "":
		return electricityTaxHint{class: taxClassOrdinary, reason: taxReasonNoIndustryCode, uncertain: true}
	case inSectionBOrC(digits) || hasAnyPrefix(digits, taxReducedIndustryCodes):
		return electricityTaxHint{class: taxClassReduced, reason: taxReasonIndustry}
	case hasAnyPrefix(digits, taxMayQualifyIndustryCodes):
		return electricityTaxHint{class: taxClassOrdinary, reason: taxReasonIndustryMayQualify, uncertain: true}
	default:
		return electricityTaxHint{class: taxClassOrdinary, reason: taxReasonOrdinary}
	}
}

// taxCustomer is whose industry code a suggestion read: the customer's id, nil
// for none, and its code, empty for none.
type taxCustomer struct {
	id           *int32
	industryCode string
}

// electricityTaxSuggestionOf is the suggestion for row as the API sends it,
// read with customer's industry code and compared with the stored class.
func electricityTaxSuggestionOf(row store.EnergyMeteringPoint, customer taxCustomer) *gen.ElectricityTaxSuggestion {
	hint := electricityTaxSuggestion(row.Household, row.CountryCode, row.PostalCode, customer.industryCode)
	out := &gen.ElectricityTaxSuggestion{
		Reason:     hint.reason,
		Uncertain:  hint.uncertain,
		Matches:    hint.class == "" || hint.class == row.ElectricityTaxClass,
		CustomerId: customer.id,
	}
	if hint.class != "" {
		class := hint.class
		out.Class = &class
	}
	if customer.industryCode != "" {
		code := customer.industryCode
		out.IndustryCode = &code
	}
	return out
}

// currentCustomerID is a point's current customer, the one whose industry code
// its suggestion reads: the customer of its Active period covering now; failing
// that, of its latest non-cancelled period by start; nil when it has none.
// periods are ordered by start (ListSupplyPeriodsByMeteringPoint).
func currentCustomerID(periods []store.EnergySupplyPeriod, now time.Time) *int32 {
	var latest *int32
	for _, p := range periods {
		if p.Status == statusCancelled {
			continue
		}
		if p.Status == statusActive && !p.Start.After(now) && (p.End == nil || p.End.After(now)) {
			id := p.CustomerID
			return &id
		}
		id := p.CustomerID
		latest = &id
	}
	return latest
}

// taxCustomerOf reads the customer's industry code through the directory; a
// nil id, a nil directory (never in production) or a customer the directory
// does not know is a customer without a code.
func (s *server) taxCustomerOf(ctx context.Context, id *int32) (taxCustomer, error) {
	customer := taxCustomer{id: id}
	if id == nil || s.deps.Directory == nil {
		return customer, nil
	}
	entry, err := s.deps.Directory.Customer(ctx, *id)
	if err != nil {
		return taxCustomer{}, fmt.Errorf("energy: read the customer's industry code: %w", err)
	}
	if entry != nil {
		customer.industryCode = entry.IndustryCode
	}
	return customer, nil
}

// pointTaxCustomer is taxCustomerOf for a point's current customer.
func (s *server) pointTaxCustomer(ctx context.Context, q *store.Queries, meteringPointID int32) (taxCustomer, error) {
	periods, err := q.ListSupplyPeriodsByMeteringPoint(ctx, meteringPointID)
	if err != nil {
		return taxCustomer{}, fmt.Errorf("energy: list supply periods: %w", err)
	}
	return s.taxCustomerOf(ctx, currentCustomerID(periods, s.deps.Clock()))
}
