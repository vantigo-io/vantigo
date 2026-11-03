package products_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
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
	if got == nil || !reflect.DeepEqual(*got, want) {
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

// TestCatalog_VariantsOfProduct_AnswersEveryVariantByID pins
// VariantsOfProduct (inventory design I22): every variant of the product, in
// id order, and nothing of another product.
func TestCatalog_VariantsOfProduct_AnswersEveryVariantByID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, map[string]any{
		"name": "Many Variants", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "many-a")}, {"sku": sku(t, "many-b")}},
	})
	createProduct(t, c, newProductBody(taxCategoryID, "Bystander", sku(t, "bystander")))

	catalog := products.Module().Products(h.Deps())
	got, err := catalog.VariantsOfProduct(context.Background(), created.Id)
	if err != nil {
		t.Fatalf("VariantsOfProduct: %v", err)
	}
	if len(got) != 2 || got[0].ID >= got[1].ID {
		t.Fatalf("VariantsOfProduct = %+v, want the product's two variants by id", got)
	}
	for _, e := range got {
		if e.ProductID != created.Id || e.ProductName != "Many Variants" {
			t.Errorf("entry %+v belongs to another product", e)
		}
	}

	none, err := catalog.VariantsOfProduct(context.Background(), 999_999)
	if err != nil || len(none) != 0 {
		t.Errorf("VariantsOfProduct(unknown) = %+v, %v, want empty without an error", none, err)
	}
}

// TestCatalog_LookupCode_MatchesSkuIgnoringCaseAndBarcodeExactly pins
// LookupCode: a SKU in any case, a barcode exactly, a part of either
// nothing — and none is an empty answer, not an error.
func TestCatalog_LookupCode_MatchesSkuIgnoringCaseAndBarcodeExactly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	barcode := newValidGTIN13(t)
	created := createProduct(t, c, map[string]any{
		"name": "Scannable", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "scan"), "barcode": barcode}},
	})
	variant := created.Variants[0]
	catalog := products.Module().Products(h.Deps())

	for _, code := range []string{variant.Sku, strings.ToLower(variant.Sku), "  " + variant.Sku + " ", barcode} {
		got, err := catalog.LookupCode(context.Background(), code)
		if err != nil {
			t.Fatalf("LookupCode(%q): %v", code, err)
		}
		if len(got) != 1 || got[0].Variant.ID != variant.Id || got[0].Packaging != nil {
			t.Errorf("LookupCode(%q) = %+v, want the one variant without a packaging", code, got)
			continue
		}
		if got[0].Variant.Barcode == nil || *got[0].Variant.Barcode != barcode {
			t.Errorf("LookupCode(%q).Barcode = %v, want %s", code, got[0].Variant.Barcode, barcode)
		}
	}
	for _, code := range []string{barcode[:12], variant.Sku[:len(variant.Sku)-1], ""} {
		got, err := catalog.LookupCode(context.Background(), code)
		if err != nil || len(got) != 0 {
			t.Errorf("LookupCode(%q) = %+v, %v, want empty without an error", code, got, err)
		}
	}
}

// TestCatalog_SearchVariants_GoodsOnlyBySkuWithinTheLimit pins
// SearchVariants: SKU, product name and barcode, ignoring case; goods only;
// ordered by SKU; at most limit; and LIKE's wildcards in q match literally.
func TestCatalog_SearchVariants_GoodsOnlyBySkuWithinTheLimit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	barcode := newValidGTIN13(t)
	goods := createProduct(t, c, map[string]any{
		"name": "Brass Hinge", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{
			{"sku": "HINGE-B-" + sku(t, "x")},
			{"sku": "HINGE-A-" + sku(t, "x"), "barcode": barcode},
		},
	})
	service := newProductBody(taxCategoryID, "Hinge fitting", "HINGE-SERVICE-"+sku(t, "x"))
	service["type"] = "Service"
	createProduct(t, c, service)
	createProduct(t, c, newProductBody(taxCategoryID, "100% wool", sku(t, "wool")))
	createProduct(t, c, newProductBody(taxCategoryID, "1000 wool", sku(t, "wool")))
	catalog := products.Module().Products(h.Deps())
	ctx := context.Background()

	byName, err := catalog.SearchVariants(ctx, "brass hinge", 50)
	if err != nil {
		t.Fatalf("SearchVariants(name): %v", err)
	}
	if len(byName) != 2 || byName[0].SKU >= byName[1].SKU || byName[0].ProductID != goods.Id {
		t.Errorf("SearchVariants(name) = %+v, want both Brass Hinge variants by SKU", byName)
	}

	bySku, err := catalog.SearchVariants(ctx, "hinge-", 50)
	if err != nil {
		t.Fatalf("SearchVariants(sku): %v", err)
	}
	if len(bySku) != 2 {
		t.Errorf("SearchVariants(sku) = %+v, want the two goods variants, never the service's", bySku)
	}

	limited, err := catalog.SearchVariants(ctx, "hinge", 1)
	if err != nil || len(limited) != 1 || !strings.HasPrefix(limited[0].SKU, "HINGE-A-") {
		t.Errorf("SearchVariants(limit 1) = %+v, %v, want the first by SKU only", limited, err)
	}

	byBarcode, err := catalog.SearchVariants(ctx, barcode[2:9], 50)
	if err != nil || len(byBarcode) != 1 || byBarcode[0].Barcode == nil || *byBarcode[0].Barcode != barcode {
		t.Errorf("SearchVariants(barcode part) = %+v, %v, want the barcoded variant", byBarcode, err)
	}

	literal, err := catalog.SearchVariants(ctx, "100%", 50)
	if err != nil || len(literal) != 1 || literal[0].ProductName != "100% wool" {
		t.Errorf("SearchVariants(100%%) = %+v, %v, want only 100%% wool: %% must match literally", literal, err)
	}
}

// TestCatalog_LockVariants_AnswersAndHoldsOnTheCallersTransaction pins
// LockVariants: it answers the entries in id order on the caller's tx,
// skips unknown ids, and holds FOR SHARE until that tx ends — a FOR UPDATE
// on the same variant from elsewhere cannot get its lock meanwhile.
func TestCatalog_LockVariants_AnswersAndHoldsOnTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	first := createProduct(t, c, newProductBody(taxCategoryID, "Locked One", sku(t, "lock-one")))
	second := createProduct(t, c, newProductBody(taxCategoryID, "Locked Two", sku(t, "lock-two")))
	ids := []int32{second.Variants[0].Id, first.Variants[0].Id, 999_999}

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	catalog := products.Module().Products(h.Deps())
	got, err := catalog.LockVariants(ctx, tx, ids)
	if err != nil {
		t.Fatalf("LockVariants: %v", err)
	}
	if len(got) != 2 || got[0].ID != first.Variants[0].Id || got[1].ID != second.Variants[0].Id {
		t.Fatalf("LockVariants = %+v, want both known variants in id order", got)
	}
	if got[0].ProductType != "Goods" || got[0].ProductName != "Locked One" {
		t.Errorf("LockVariants[0] = %+v, want the product's fields", got[0])
	}

	other, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("other: begin: %v", err)
	}
	defer func() { _ = other.Rollback(ctx) }()
	var locked int
	err = other.QueryRow(ctx, `SELECT count(*) FROM (SELECT id FROM products.product_variants WHERE id = $1 FOR UPDATE SKIP LOCKED) v`, first.Variants[0].Id).Scan(&locked)
	if err != nil || locked != 0 {
		t.Errorf("FOR UPDATE beside LockVariants got %d rows (%v), want the row locked", locked, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	err = other.QueryRow(ctx, `SELECT count(*) FROM (SELECT id FROM products.product_variants WHERE id = $1 FOR UPDATE SKIP LOCKED) v`, first.Variants[0].Id).Scan(&locked)
	if err != nil || locked != 1 {
		t.Errorf("FOR UPDATE after the commit got %d rows (%v), want the row free", locked, err)
	}

	empty, err := catalog.LockVariants(ctx, nil, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("LockVariants(no ids) = %+v, %v, want empty without touching tx", empty, err)
	}
}

// TestCatalog_LookupSKUs_KeyedAsWrittenIgnoringCaseWithPackagings pins
// LookupSKUs: each SKU keyed exactly as the caller wrote it, matched ignoring
// case; a missing or blank SKU absent; packagings filled like every read; and
// more SKUs than one query carries answered across the chunks.
func TestCatalog_LookupSKUs_KeyedAsWrittenIgnoringCaseWithPackagings(t *testing.T) {
	t.Parallel()
	h, c, product := packagingFixture(t)
	variant := product.Variants[0]
	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 15})
	taxCategoryID := insertTaxCategory(t, h, "Reduced rate", 0.15)
	other := createProduct(t, c, newProductBody(taxCategoryID, "Nails", sku(t, "nail"))).Variants[0]
	catalog := products.Module().Products(h.Deps())

	lower, missing := strings.ToLower(variant.Sku), sku(t, "missing")
	upper := " " + strings.ToUpper(other.Sku) + " "
	// Over a thousand unknown SKUs between the known ones put them in
	// different chunks.
	skus := []string{lower, variant.Sku, missing, "", "  "}
	for i := range 1500 {
		skus = append(skus, fmt.Sprintf("%s-%d", missing, i))
	}
	skus = append(skus, upper)
	got, err := catalog.LookupSKUs(context.Background(), skus)
	if err != nil {
		t.Fatalf("LookupSKUs: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("LookupSKUs answered %d keys, want 3: %+v", len(got), got)
	}
	for _, key := range []string{lower, variant.Sku} {
		entries := got[key]
		if len(entries) != 1 || entries[0].ID != variant.Id || entries[0].SKU != variant.Sku {
			t.Errorf("LookupSKUs[%q] = %+v, want the variant", key, entries)
			continue
		}
		if p := entries[0].Packagings; len(p) != 1 || p[0].ID != box.Id || p[0].Quantity != "15.000" {
			t.Errorf("LookupSKUs[%q].Packagings = %+v, want the box", key, p)
		}
	}
	if entries := got[upper]; len(entries) != 1 || entries[0].ID != other.Id {
		t.Errorf("LookupSKUs[the last chunk's SKU] = %+v, want the nails", entries)
	}
	for _, key := range []string{missing, "", "  "} {
		if entries, ok := got[key]; ok {
			t.Errorf("LookupSKUs[%q] = %+v, want absent", key, entries)
		}
	}
	if none, err := catalog.LookupSKUs(context.Background(), nil); err != nil || len(none) != 0 {
		t.Errorf("LookupSKUs(nil) = %+v, %v, want empty without an error", none, err)
	}
}
