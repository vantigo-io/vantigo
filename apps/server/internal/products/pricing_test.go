package products

// Ported from Domain/Products/ProductPricingTests.cs.

import (
	"testing"
	"time"
)

var pricingNow = time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)

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
