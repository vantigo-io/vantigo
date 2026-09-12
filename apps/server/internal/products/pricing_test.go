package products

// Ported from Domain/Products/ProductPricingTests.cs.

import (
	"math"
	"testing"
	"time"
)

var pricingNow = time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)

// TestNumericFromFloat_RejectsUnstorableValues pins that
// pgtype.Numeric.Scan's error is propagated rather than discarded
// (values.go). Discarding it left the zero pgtype.Numeric — an invalid
// value, which writes as SQL NULL — so an infinity would have become a
// silent NULL in a numeric column instead of failing. No request can reach
// this today, because every value arrives through encoding/json, which
// refuses to decode either an infinity or a NaN; that is exactly why only a
// direct test can hold the behaviour.
//
// NaN is deliberately not treated as unstorable: Postgres's numeric type has
// its own NaN and Scan accepts "NaN", so NaN was never at risk of becoming a
// silent NULL, and rejecting it here would invent a rule neither Postgres nor
// .NET has. Measured, not assumed — of the three non-finite float64s only the
// two infinities fail to scan.
func TestNumericFromFloat_RejectsUnstorableValues(t *testing.T) {
	t.Parallel()
	for _, v := range []float64{math.Inf(1), math.Inf(-1)} {
		if _, err := numericFromFloat(v); err == nil {
			t.Errorf("numericFromFloat(%v) returned no error, want one rather than a silent SQL NULL", v)
		}
	}

	n, err := numericFromFloat(math.NaN())
	if err != nil {
		t.Fatalf("numericFromFloat(NaN): %v, want no error (Postgres numeric has its own NaN)", err)
	}
	if !n.Valid || !n.NaN {
		t.Errorf("numericFromFloat(NaN) = %+v, want a Valid NaN rather than the invalid SQL NULL zero value", n)
	}
}

// TestNumericFromFloat_AcceptsFiniteValues is the other direction, so the
// check above cannot be satisfied by rejecting everything.
func TestNumericFromFloat_AcceptsFiniteValues(t *testing.T) {
	t.Parallel()
	for _, v := range []float64{0, 19.99, -1, 1234567.89} {
		n, err := numericFromFloat(v)
		if err != nil {
			t.Errorf("numericFromFloat(%v): %v", v, err)
			continue
		}
		if !n.Valid {
			t.Errorf("numericFromFloat(%v) is not Valid, want a storable value", v)
		}
	}
}

// TestNumericFromFloatPtr_NilIsNullNotAnError keeps the optional-field
// contract: nil is the column's own NULL and never an error, while a
// non-finite pointee is still an error.
func TestNumericFromFloatPtr_NilIsNullNotAnError(t *testing.T) {
	t.Parallel()
	n, err := numericFromFloatPtr(nil)
	if err != nil {
		t.Fatalf("numericFromFloatPtr(nil): %v, want no error", err)
	}
	if n.Valid {
		t.Error("numericFromFloatPtr(nil) is Valid, want the invalid (SQL NULL) zero value")
	}

	inf := math.Inf(1)
	if _, err := numericFromFloatPtr(&inf); err == nil {
		t.Error("numericFromFloatPtr(+Inf) returned no error, want one")
	}
}

// TestNumericsFromVariant_PropagatesTheFirstFailure covers the helper the
// three variant writers share: one error for the whole params literal, and
// an unset optional stays the invalid (SQL NULL) zero value.
func TestNumericsFromVariant_PropagatesTheFirstFailure(t *testing.T) {
	t.Parallel()
	inf := math.Inf(1)
	if _, err := numericsFromVariant(parsedVariant{StandardCost: &inf}); err == nil {
		t.Error("numericsFromVariant with a non-finite standardCost returned no error, want one")
	}

	weight := 12.5
	nums, err := numericsFromVariant(parsedVariant{WeightKg: &weight})
	if err != nil {
		t.Fatalf("numericsFromVariant: %v", err)
	}
	if !nums.WeightKg.Valid {
		t.Error("WeightKg is not Valid, want the converted value")
	}
	if nums.StandardCost.Valid {
		t.Error("StandardCost is Valid, want the invalid (SQL NULL) zero value for an unset optional")
	}
}

func basePrice(currency string, amount float64) productPrice {
	return productPrice{Currency: currency, Amount: amount}
}

func campaignPrice(currency string, amount float64, from, to time.Time) productPrice {
	return productPrice{Currency: currency, Amount: amount, ValidFrom: &from, ValidTo: &to}
}

func TestEffectivePrice_WithOnlyBasePrice_ReturnsBasePrice(t *testing.T) {
	t.Parallel()
	prices := []productPrice{basePrice("NOK", 599)}

	effective, ok := getEffectivePrice(prices, "NOK", pricingNow)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if effective.Amount != 599 {
		t.Errorf("Amount = %v, want 599", effective.Amount)
	}
}

func TestEffectivePrice_WithActiveCampaign_PrefersCampaignOverBase(t *testing.T) {
	t.Parallel()
	prices := []productPrice{
		basePrice("NOK", 599),
		campaignPrice("NOK", 499, pricingNow.AddDate(0, 0, -1), pricingNow.AddDate(0, 0, 1)),
	}

	effective, ok := getEffectivePrice(prices, "NOK", pricingNow)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if effective.Amount != 499 {
		t.Errorf("Amount = %v, want 499", effective.Amount)
	}
}

func TestEffectivePrice_WithExpiredCampaign_FallsBackToBase(t *testing.T) {
	t.Parallel()
	prices := []productPrice{
		basePrice("NOK", 599),
		campaignPrice("NOK", 499, pricingNow.AddDate(0, 0, -10), pricingNow.AddDate(0, 0, -5)),
	}

	effective, ok := getEffectivePrice(prices, "NOK", pricingNow)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if effective.Amount != 599 {
		t.Errorf("Amount = %v, want 599", effective.Amount)
	}
}

func TestEffectivePrice_WithFutureCampaign_FallsBackToBase(t *testing.T) {
	t.Parallel()
	prices := []productPrice{
		basePrice("NOK", 599),
		campaignPrice("NOK", 499, pricingNow.AddDate(0, 0, 5), pricingNow.AddDate(0, 0, 10)),
	}

	effective, ok := getEffectivePrice(prices, "NOK", pricingNow)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if effective.Amount != 599 {
		t.Errorf("Amount = %v, want 599", effective.Amount)
	}
}

func TestEffectivePrice_WithNoPrices_ReturnsNull(t *testing.T) {
	t.Parallel()
	if _, ok := getEffectivePrice(nil, "NOK", pricingNow); ok {
		t.Error("ok = true, want false")
	}
}

func TestEffectivePrice_WithOtherCurrencyOnly_ReturnsNull(t *testing.T) {
	t.Parallel()
	if _, ok := getEffectivePrice([]productPrice{basePrice("SEK", 649)}, "NOK", pricingNow); ok {
		t.Error("ok = true, want false")
	}
}

func TestEffectivePrices_ReturnsOnePricePerCurrency(t *testing.T) {
	t.Parallel()
	prices := []productPrice{
		basePrice("NOK", 599),
		basePrice("SEK", 649),
		campaignPrice("NOK", 499, pricingNow.AddDate(0, 0, -1), pricingNow.AddDate(0, 0, 1)),
	}

	effective := getEffectivePrices(prices, pricingNow)
	if len(effective) != 2 {
		t.Fatalf("len = %d, want 2", len(effective))
	}
	var nok, sek float64
	for _, p := range effective {
		switch p.Currency {
		case "NOK":
			nok = p.Amount
		case "SEK":
			sek = p.Amount
		}
	}
	if nok != 499 {
		t.Errorf("NOK = %v, want 499", nok)
	}
	if sek != 649 {
		t.Errorf("SEK = %v, want 649", sek)
	}
}

func TestEffectivePrice_WithOverlappingCampaigns_PrefersLatestStart(t *testing.T) {
	t.Parallel()
	prices := []productPrice{
		campaignPrice("NOK", 549, pricingNow.AddDate(0, 0, -10), pricingNow.AddDate(0, 0, 10)),
		campaignPrice("NOK", 449, pricingNow.AddDate(0, 0, -1), pricingNow.AddDate(0, 0, 1)),
	}

	effective, ok := getEffectivePrice(prices, "NOK", pricingNow)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if effective.Amount != 449 {
		t.Errorf("Amount = %v, want 449", effective.Amount)
	}
}

func TestConflicts_TwoBasePricesSameCurrency_Conflict(t *testing.T) {
	t.Parallel()
	if !pricesConflict(basePrice("NOK", 599), basePrice("NOK", 649)) {
		t.Error("Conflicts = false, want true")
	}
}

func TestConflicts_BasePricesInDifferentCurrencies_DoNotConflict(t *testing.T) {
	t.Parallel()
	if pricesConflict(basePrice("NOK", 599), basePrice("SEK", 649)) {
		t.Error("Conflicts = true, want false")
	}
}

func TestConflicts_CampaignOverBase_DoesNotConflict(t *testing.T) {
	t.Parallel()
	if pricesConflict(campaignPrice("NOK", 499, pricingNow.AddDate(0, 0, -1), pricingNow.AddDate(0, 0, 1)), basePrice("NOK", 599)) {
		t.Error("Conflicts = true, want false")
	}
}

func TestConflicts_OverlappingCampaignsSameCurrency_Conflict(t *testing.T) {
	t.Parallel()
	a := campaignPrice("NOK", 499, pricingNow.AddDate(0, 0, -1), pricingNow.AddDate(0, 0, 5))
	b := campaignPrice("NOK", 449, pricingNow.AddDate(0, 0, 2), pricingNow.AddDate(0, 0, 10))
	if !pricesConflict(a, b) {
		t.Error("Conflicts = false, want true")
	}
}

func TestConflicts_DisjointCampaignsSameCurrency_DoNotConflict(t *testing.T) {
	t.Parallel()
	a := campaignPrice("NOK", 499, pricingNow.AddDate(0, 0, -10), pricingNow.AddDate(0, 0, -5))
	b := campaignPrice("NOK", 449, pricingNow.AddDate(0, 0, 5), pricingNow.AddDate(0, 0, 10))
	if pricesConflict(a, b) {
		t.Error("Conflicts = true, want false")
	}
}
