package products_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file continues Integration/ProductsEndpointsTests.cs's port
// (products inventory §6) for the price sub-resource. The wrong Location
// header on price creation (oddity 3) has its own dedicated test in
// oddities_test.go.

// Ported from Integration/ProductsEndpointsTests.cs. PriceSubResource_IsScopedToVariant.
func TestPriceSubResource_IsScopedToVariant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Priced", sku(t, "prices")))
	variant := created.Variants[0]
	basePath := fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id)
	now := h.Now()

	add := c.Do(http.MethodPost, basePath, map[string]any{"currency": "nok", "amount": 599})
	if add.Status != http.StatusCreated {
		t.Fatalf("add base price: status %d body %s, want 201", add.Status, add.Body)
	}
	var price priceJSON
	add.JSON(&price)
	if price.Currency != "NOK" {
		t.Errorf("Currency = %q, want NOK (lowercase input uppercased)", price.Currency)
	}

	conflict := c.Do(http.MethodPost, basePath, map[string]any{"currency": "NOK", "amount": 649})
	if conflict.Status != http.StatusConflict {
		t.Errorf("second open-ended NOK price: status %d body %s, want 409", conflict.Status, conflict.Body)
	}

	campaign := c.Do(http.MethodPost, basePath, map[string]any{
		"currency": "NOK", "amount": 499, "validFrom": now.AddDate(0, 0, -1), "validTo": now.AddDate(0, 0, 1),
	})
	if campaign.Status != http.StatusCreated {
		t.Errorf("bounded campaign NOK price: status %d body %s, want 201 (doesn't conflict with the base price)", campaign.Status, campaign.Body)
	}

	list := c.Do(http.MethodGet, basePath, nil)
	var prices []priceJSON
	list.JSON(&prices)
	if len(prices) != 2 {
		t.Fatalf("len(prices) = %d, want 2 (the rejected 649 conflict never persisted)", len(prices))
	}

	del := c.Do(http.MethodDelete, fmt.Sprintf("%s/%d", basePath, price.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Errorf("delete price: status %d body %s, want 204", del.Status, del.Body)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// AddProductPrice_WithOverlappingOpenEndedPrice_ReturnsConflict.
func TestAddProductPrice_WithOverlappingOpenEndedPrice_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Overlapping Prices", sku(t, "overlap")))
	variant := created.Variants[0]
	basePath := fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id)

	first := c.Do(http.MethodPost, basePath, map[string]any{"currency": "NOK", "amount": 599})
	if first.Status != http.StatusCreated {
		t.Fatalf("first price: status %d body %s, want 201", first.Status, first.Body)
	}
	second := c.Do(http.MethodPost, basePath, map[string]any{"currency": "NOK", "amount": 649})
	if second.Status != http.StatusConflict {
		t.Errorf("second price: status %d body %s, want 409", second.Status, second.Body)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs. AddPrice_WithInvalidWindow_ReturnsFieldError.
func TestAddPrice_WithInvalidWindow_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Bad Window", sku(t, "window")))
	variant := created.Variants[0]
	now := h.Now()

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id), map[string]any{
		"currency": "NOK", "amount": 599, "validFrom": now, "validTo": now.AddDate(0, 0, -1),
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["validTo"]; !ok {
		t.Errorf("Errors = %v, want a 'validTo' key", problem.Errors)
	}
}

// TestPutPrice_UpdatesInPlace gives putProductsByIdVariantsByVariantIdPricesByPriceId
// its own successful exchange (main_test.go's RequireCoverage), and pins
// UpdateProductPriceEndpoint's in-place rewrite (UpdateProductPriceEndpoint.cs:54-58).
func TestPutPrice_UpdatesInPlace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Editable Price", sku(t, "price-edit")))
	variant := created.Variants[0]
	basePath := fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id)

	add := c.Do(http.MethodPost, basePath, map[string]any{"currency": "NOK", "amount": 100})
	var price priceJSON
	add.JSON(&price)

	r := c.Do(http.MethodPut, fmt.Sprintf("%s/%d", basePath, price.Id), map[string]any{"currency": "NOK", "amount": 150})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated priceJSON
	r.JSON(&updated)
	if updated.Amount != 150 {
		t.Errorf("Amount = %v, want 150", updated.Amount)
	}
	if updated.Id != price.Id {
		t.Errorf("Id = %d, want %d (in place, not a new row)", updated.Id, price.Id)
	}
}

// TestPutPrice_OverlappingExcludesItself pins UpdateProductPriceEndpoint's
// "excluding itself" Conflicts check (:45-46): re-submitting a price's own
// unchanged window must not conflict with itself.
func TestPutPrice_OverlappingExcludesItself(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Self Overlap", sku(t, "self-overlap")))
	variant := created.Variants[0]
	basePath := fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id)

	add := c.Do(http.MethodPost, basePath, map[string]any{"currency": "NOK", "amount": 100})
	var price priceJSON
	add.JSON(&price)

	r := c.Do(http.MethodPut, fmt.Sprintf("%s/%d", basePath, price.Id), map[string]any{"currency": "NOK", "amount": 100})
	if r.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200 (a price cannot conflict with itself)", r.Status, r.Body)
	}
}

func TestPutPrice_UnknownId_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Ghost Price Target", sku(t, "ghost-price")))
	variant := created.Variants[0]

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d/prices/999999", created.Id, variant.Id),
		map[string]any{"currency": "NOK", "amount": 100})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestGetProductsByIdVariantsByVariantIdPrices_UnknownVariant_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "No Such Variant", sku(t, "no-variant")))

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/%d/variants/999999/prices", created.Id), nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestPostPrice_InvalidBodyAgainstMissingVariant_Returns400 pins
// AddProductPriceEndpoint's order (prices.go's doc comment): field
// validation runs before the scoped variant lookup, so an invalid price
// aimed at a missing variant answers 400, never 404. No test posted a price
// to an unknown variant at all before this one.
func TestPostPrice_InvalidBodyAgainstMissingVariant_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Price Ordering", sku(t, "price-order")))

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants/999999/prices", created.Id),
		map[string]any{"currency": "NOK", "amount": -1})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (validation must run before the existence check)", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["amount"]; !ok {
		t.Errorf("errors = %v, want a key \"amount\"", problem.Errors)
	}
}

// TestPutPrice_InvalidBodyAgainstMissingPrice_Returns400 pins
// UpdateProductPriceEndpoint.cs:17-61's order: field validation before the
// scoped price lookup, so an invalid price aimed at a missing price id
// answers 400, never the 404 TestPutPrice_UnknownId_ReturnsNotFound pins for
// a valid body.
func TestPutPrice_InvalidBodyAgainstMissingPrice_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Price Put Ordering", sku(t, "price-put-order")))
	variant := created.Variants[0]

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d/prices/999999", created.Id, variant.Id),
		map[string]any{"currency": "NOK", "amount": -1})
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400 (validation must run before the existence check)", r.Status, r.Body)
	}
}
