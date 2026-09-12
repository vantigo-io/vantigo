package products_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file continues Integration/ProductsEndpointsTests.cs's port
// (products inventory §6) for the variant sub-resource.

// Ported from Integration/ProductsEndpointsTests.cs.
// UpdateVariant_WithDuplicateSku_ReturnsConflict.
func TestUpdateVariant_WithDuplicateSku_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	first := createProduct(t, c, newProductBody(taxCategoryID, "First Variant", sku(t, "update-dup-first")))
	second := createProduct(t, c, newProductBody(taxCategoryID, "Second Variant", sku(t, "update-dup-second")))

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", second.Id, second.Variants[0].Id),
		map[string]any{"sku": *first.Sku})
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// UpdateVariant_SkuChangeOnDraft_IsAllowed. Also pins products inventory §7
// oddity 5: OptionValues keys come back camelCased ("Color" -> "color") even
// though the value's casing is preserved.
func TestUpdateVariant_SkuChangeOnDraft_IsAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Draft Sku Change", sku(t, "draft-sku")))
	variant := created.Variants[0]
	newSku := sku(t, "draft-sku-new")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, variant.Id), map[string]any{
		"sku": newSku, "optionValues": map[string]any{"Color": "Red"},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated variantJSON
	r.JSON(&updated)
	if updated.Sku != newSku {
		t.Errorf("Sku = %q, want %q", updated.Sku, newSku)
	}
	if got := updated.OptionValues["color"]; got != "Red" {
		t.Errorf(`OptionValues["color"] = %q, want "Red" (client sent key "Color", camelCased on the way out)`, got)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// UpdateVariant_SkuChangeOnActiveProduct_ReturnsConflict.
func TestUpdateVariant_SkuChangeOnActiveProduct_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Active Sku Change", sku(t, "active-sku"), "Active"))
	variant := created.Variants[0]

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, variant.Id),
		map[string]any{"sku": sku(t, "active-sku-new")})
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs. VariantCrud_RejectsDeletingLastVariant.
func TestVariantCrud_RejectsDeletingLastVariant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Variant CRUD", sku(t, "variant-crud")))
	first := created.Variants[0]

	lastDelete := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, first.Id), nil)
	if lastDelete.Status != http.StatusConflict {
		t.Fatalf("delete the only variant: status %d body %s, want 409", lastDelete.Status, lastDelete.Body)
	}

	add := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), map[string]any{
		"sku": sku(t, "variant-two"), "optionValues": map[string]any{"Color": "Blue"},
	})
	if add.Status != http.StatusCreated {
		t.Fatalf("add second variant: status %d body %s, want 201", add.Status, add.Body)
	}
	var second variantJSON
	add.JSON(&second)

	secondDelete := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
	if secondDelete.Status != http.StatusNoContent {
		t.Errorf("delete the second variant: status %d body %s, want 204", secondDelete.Status, secondDelete.Body)
	}

	firstDeleteAgain := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, first.Id), nil)
	if firstDeleteAgain.Status != http.StatusConflict {
		t.Errorf("delete the now-only variant: status %d body %s, want 409", firstDeleteAgain.Status, firstDeleteAgain.Body)
	}
}

// TestPostProductsByIdVariants_UnknownProduct_ReturnsNotFound pins
// AddProductVariantEndpoint.cs:40-43's 404.
func TestPostProductsByIdVariants_UnknownProduct_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/products/999999/variants", map[string]any{"sku": sku(t, "orphan")})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestPostProductsByIdVariants_DuplicateBarcode_ReturnsConflict pins
// AddProductVariantEndpoint.cs:51-53's exact detail text, distinct from the
// no-quoted-value message CreateProductEndpoint uses for the same conflict
// on a whole-product create.
func TestPostProductsByIdVariants_DuplicateBarcode_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	barcode := "4006381333931"

	created := createProduct(t, c, newProductBody(taxCategoryID, "Barcode Base", sku(t, "barcode-base")))
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), map[string]any{
		"sku": sku(t, "barcode-first"), "barcode": barcode,
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("first variant with barcode: status %d body %s, want 201", r.Status, r.Body)
	}

	second := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), map[string]any{
		"sku": sku(t, "barcode-second"), "barcode": barcode,
	})
	if second.Status != http.StatusConflict {
		t.Fatalf("second variant with the same barcode: status %d body %s, want 409", second.Status, second.Body)
	}
	var problem problemJSON
	second.JSON(&problem)
	want := fmt.Sprintf("A variant with barcode '%s' already exists.", barcode)
	if problem.Detail != want {
		t.Errorf("Detail = %q, want %q", problem.Detail, want)
	}
}

// TestPostProductsByIdVariants_InvalidBarcode_ReturnsFieldError pins the
// GTIN field-validation message text.
func TestPostProductsByIdVariants_InvalidBarcode_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Bad Barcode", sku(t, "bad-barcode")))

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), map[string]any{
		"sku": sku(t, "bad-barcode-2"), "barcode": "123",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "'barcode' must be a valid GTIN-8, GTIN-12, GTIN-13 or GTIN-14: digits only with a correct check digit."
	if got := problem.Errors["barcode"]; len(got) != 1 || got[0] != want {
		t.Errorf("Errors[barcode] = %v, want [%q]", got, want)
	}
}

func TestPutProductsByIdVariantsByVariantId_UnknownId_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Ghost Variant Target", sku(t, "ghost-variant")))

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/999999", created.Id), map[string]any{"sku": sku(t, "ghost")})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}
