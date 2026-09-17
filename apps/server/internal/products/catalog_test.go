package products_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// This file ports Task 3 of the projects module plan
// (.superpowers/sdd/2026-09-17-projects-module/task-3-brief.md):
// products.Module()'s contracts.ProductCatalog, the one sanctioned way
// another module reads a variant's name/unit/type/status and its effective
// price without importing this package or reading its schema.

// addPrice posts body to a variant's prices sub-resource and requires 201.
func addPrice(t *testing.T, c *modtest.Client, productID, variantID int32, body map[string]any) priceJSON {
	t.Helper()
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", productID, variantID), body)
	if r.Status != http.StatusCreated {
		t.Fatalf("add price: status %d body %s, want 201", r.Status, r.Body)
	}
	var created priceJSON
	r.JSON(&created)
	return created
}

// effectivePriceInCurrency fetches the product's variants (the same
// GET .../variants a client uses, backed by responses.go's variantResponse
// and pricing.go's getEffectivePrices) and returns the effective price entry
// in currency for the given variant, or nil if none.
func effectivePriceInCurrency(t *testing.T, c *modtest.Client, productID, variantID int32, currency string) *priceJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/%d/variants", productID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list variants: status %d body %s, want 200", r.Status, r.Body)
	}
	var variants []variantJSON
	r.JSON(&variants)
	for _, v := range variants {
		if v.Id != variantID {
			continue
		}
		for _, p := range v.EffectivePrices {
			if p.Currency == currency {
				price := p
				return &price
			}
		}
	}
	return nil
}

// TestCatalog_VariantResolvesProductFields proves Variant answers the
// product name, SKU, unit, product type and product status for a known
// variant — exactly the fields contracts.VariantEntry documents, joined
// from the variant's own product row.
func TestCatalog_VariantResolvesProductFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Catalog Widget", sku(t, "catalog-widget")))
	variant := created.Variants[0]

	catalog := products.Module().Products(h.Deps())
	got, err := catalog.Variant(context.Background(), variant.Id)
	if err != nil {
		t.Fatalf("Variant: %v", err)
	}
	want := contracts.VariantEntry{
		ID: variant.Id, ProductID: created.Id, ProductName: "Catalog Widget",
		SKU: variant.Sku, Unit: variant.Unit, ProductType: "Goods", ProductStatus: "Draft",
	}
	if got == nil || *got != want {
		t.Errorf("Variant = %+v, want %+v", got, want)
	}
}

// TestCatalog_VariantUnknownIDIsNilWithoutAnError proves a missing variant
// is (nil, nil), never an error: a caller tells "does not exist" from "the
// lookup failed" by checking err.
func TestCatalog_VariantUnknownIDIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	catalog := products.Module().Products(h.Deps())

	got, err := catalog.Variant(context.Background(), 999_999)
	if got != nil || err != nil {
		t.Errorf("Variant(unknown) = %+v, %v, want nil, nil", got, err)
	}
}

// TestCatalog_VariantsBatchSkipsUnknownIDs proves Variants resolves every id
// it knows and simply omits the ones it does not, rather than failing the
// whole lookup.
func TestCatalog_VariantsBatchSkipsUnknownIDs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	first := createProduct(t, c, newProductBody(taxCategoryID, "First Catalog", sku(t, "catalog-first")))
	second := createProduct(t, c, newProductBody(taxCategoryID, "Second Catalog", sku(t, "catalog-second")))

	catalog := products.Module().Products(h.Deps())
	got, err := catalog.Variants(context.Background(), []int32{first.Variants[0].Id, second.Variants[0].Id, 999_999})
	if err != nil {
		t.Fatalf("Variants: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Variants = %+v, want two entries", got)
	}
	byID := map[int32]contracts.VariantEntry{got[0].ID: got[0], got[1].ID: got[1]}
	if e, ok := byID[first.Variants[0].Id]; !ok || e.ProductName != "First Catalog" {
		t.Errorf("Variants missing or wrong entry for first: %+v", byID[first.Variants[0].Id])
	}
	if e, ok := byID[second.Variants[0].Id]; !ok || e.ProductName != "Second Catalog" {
		t.Errorf("Variants missing or wrong entry for second: %+v", byID[second.Variants[0].Id])
	}
}

// TestCatalog_VariantsEmptyInputIsEmptyOutput proves an empty slice of ids
// answers an empty result with no query error, rather than a malformed
// ANY($1) query against an empty array.
func TestCatalog_VariantsEmptyInputIsEmptyOutput(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	catalog := products.Module().Products(h.Deps())

	got, err := catalog.Variants(context.Background(), []int32{})
	if err != nil {
		t.Fatalf("Variants(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Variants(empty) = %+v, want empty", got)
	}
}

// TestCatalog_ListPrice_BaseOnly proves a variant with only an open-ended
// base price answers that price for its currency.
func TestCatalog_ListPrice_BaseOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Base Only", sku(t, "catalog-base-only")))
	variant := created.Variants[0]
	addPrice(t, c, created.Id, variant.Id, map[string]any{"currency": "NOK", "amount": 599})

	catalog := products.Module().Products(h.Deps())
	got, err := catalog.ListPrice(context.Background(), variant.Id, "NOK", h.Now())
	if err != nil {
		t.Fatalf("ListPrice: %v", err)
	}
	if got == nil || *got != (contracts.Money{Amount: 599, Currency: "NOK"}) {
		t.Errorf("ListPrice = %+v, want {599 NOK}", got)
	}
}

// TestCatalog_ListPrice_CampaignBeatsBase proves a bounded campaign price
// valid at the resolution moment is preferred over the open-ended base
// price, and that the answer equals what the module's own variant response
// (GET .../variants, backed by pricing.go's getEffectivePrices) reports for
// the same moment — ListPrice must reuse that resolution, not restate it.
func TestCatalog_ListPrice_CampaignBeatsBase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Campaign Beats Base", sku(t, "catalog-campaign")))
	variant := created.Variants[0]
	now := h.Now()
	addPrice(t, c, created.Id, variant.Id, map[string]any{"currency": "NOK", "amount": 599})
	addPrice(t, c, created.Id, variant.Id, map[string]any{
		"currency": "NOK", "amount": 499, "validFrom": now.AddDate(0, 0, -1), "validTo": now.AddDate(0, 0, 1),
	})

	catalog := products.Module().Products(h.Deps())
	got, err := catalog.ListPrice(context.Background(), variant.Id, "NOK", now)
	if err != nil {
		t.Fatalf("ListPrice: %v", err)
	}
	if got == nil || *got != (contracts.Money{Amount: 499, Currency: "NOK"}) {
		t.Errorf("ListPrice = %+v, want {499 NOK} (the campaign price)", got)
	}

	oracle := effectivePriceInCurrency(t, c, created.Id, variant.Id, "NOK")
	if oracle == nil || got.Amount != oracle.Amount || got.Currency != oracle.Currency {
		t.Errorf("ListPrice = %+v, disagrees with the module's own variant response %+v", got, oracle)
	}
}

// TestCatalog_ListPrice_ExpiredCampaignFallsBackToBase proves a campaign
// price whose window has already closed no longer beats the base price.
func TestCatalog_ListPrice_ExpiredCampaignFallsBackToBase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Expired Campaign", sku(t, "catalog-expired")))
	variant := created.Variants[0]
	now := h.Now()
	addPrice(t, c, created.Id, variant.Id, map[string]any{"currency": "NOK", "amount": 599})
	addPrice(t, c, created.Id, variant.Id, map[string]any{
		"currency": "NOK", "amount": 499, "validFrom": now.AddDate(0, 0, -10), "validTo": now.AddDate(0, 0, -5),
	})

	catalog := products.Module().Products(h.Deps())
	got, err := catalog.ListPrice(context.Background(), variant.Id, "NOK", now)
	if err != nil {
		t.Fatalf("ListPrice: %v", err)
	}
	if got == nil || *got != (contracts.Money{Amount: 599, Currency: "NOK"}) {
		t.Errorf("ListPrice = %+v, want {599 NOK} (the expired campaign no longer applies)", got)
	}
}

// TestCatalog_ListPrice_OtherCurrencyIsNilWithoutAnError proves a currency
// the variant has no price in is (nil, nil), not an error.
func TestCatalog_ListPrice_OtherCurrencyIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Only NOK", sku(t, "catalog-only-nok")))
	variant := created.Variants[0]
	addPrice(t, c, created.Id, variant.Id, map[string]any{"currency": "NOK", "amount": 599})

	catalog := products.Module().Products(h.Deps())
	got, err := catalog.ListPrice(context.Background(), variant.Id, "EUR", h.Now())
	if got != nil || err != nil {
		t.Errorf("ListPrice(EUR) = %+v, %v, want nil, nil", got, err)
	}
}

// TestCatalog_ListPrice_CurrencyIsCaseInsensitive proves a lowercase
// currency argument still matches a price stored uppercased.
func TestCatalog_ListPrice_CurrencyIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Case Insensitive", sku(t, "catalog-case")))
	variant := created.Variants[0]
	addPrice(t, c, created.Id, variant.Id, map[string]any{"currency": "NOK", "amount": 599})

	catalog := products.Module().Products(h.Deps())
	got, err := catalog.ListPrice(context.Background(), variant.Id, "nok", h.Now())
	if err != nil {
		t.Fatalf("ListPrice: %v", err)
	}
	if got == nil || *got != (contracts.Money{Amount: 599, Currency: "NOK"}) {
		t.Errorf("ListPrice(nok) = %+v, want {599 NOK}", got)
	}
}
