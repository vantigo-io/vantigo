package products_test

import (
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// This file ports Integration/ProductCatalogFieldsTests.cs (products
// inventory §6), the last of the products/variants tests this task owed but
// had not yet ported.

// newValidGTIN13 is NewGtin13(): a random, structurally valid GTIN-13 with a
// correct check digit (alternating 3-1 weights from the right).
func newValidGTIN13(t *testing.T) string {
	t.Helper()
	digits := make([]int, 13)
	for i := 0; i < 12; i++ {
		digits[i] = rand.Intn(10) //nolint:gosec // test fixture, not a security-sensitive value
	}
	sum := 0
	for i := 0; i < 12; i++ {
		weight := 1
		if (12-i)%2 == 1 {
			weight = 3
		}
		sum += digits[i] * weight
	}
	digits[12] = (10 - sum%10) % 10
	var b strings.Builder
	for _, d := range digits {
		b.WriteByte(byte('0' + d))
	}
	return b.String()
}

// Ported from Integration/ProductCatalogFieldsTests.cs.
// CreateProduct_WithCatalogFields_ReturnsThemOnVariantAndFlattenedResponse.
func TestCreateProduct_WithCatalogFields_ReturnsThemOnVariantAndFlattenedResponse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	categoryID := insertCategory(t, h, "Goods category", nil)
	barcode := newValidGTIN13(t)

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Catalog Product", "type": "Goods", "taxCategoryId": taxCategoryID,
		"description": "A richly described product.", "categoryId": categoryID,
		"variants": []map[string]any{
			{"sku": sku(t, "catalog"), "barcode": barcode, "weightKg": 1.25, "lengthCm": 30.5, "widthCm": 20, "heightCm": 10},
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created productJSON
	r.JSON(&created)

	if created.Description == nil || *created.Description != "A richly described product." {
		t.Errorf("Description = %v, want %q", created.Description, "A richly described product.")
	}
	if created.Category == nil || created.Category.Id != categoryID {
		t.Errorf("Category = %v, want Id %d", created.Category, categoryID)
	}
	if created.Barcode == nil || *created.Barcode != barcode {
		t.Errorf("Barcode = %v, want %q", created.Barcode, barcode)
	}
	if created.WeightKg == nil || *created.WeightKg != 1.25 {
		t.Errorf("WeightKg = %v, want 1.25", created.WeightKg)
	}
	if created.Variants[0].LengthCm == nil || *created.Variants[0].LengthCm != 30.5 {
		t.Errorf("Variants[0].LengthCm = %v, want 30.5", created.Variants[0].LengthCm)
	}
}

// Ported from Integration/ProductCatalogFieldsTests.cs. UpdateVariant_SetsAndClearsCatalogFields.
func TestUpdateVariant_SetsAndClearsCatalogFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Catalog Update", sku(t, "cat-upd")))
	variant := created.Variants[0]
	barcode := newValidGTIN13(t)

	set := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, variant.Id), map[string]any{
		"sku": variant.Sku, "barcode": barcode, "weightKg": 2.5,
	})
	if set.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", set.Status, set.Body)
	}
	var updated variantJSON
	set.JSON(&updated)
	if updated.Barcode == nil || *updated.Barcode != barcode {
		t.Errorf("Barcode = %v, want %q", updated.Barcode, barcode)
	}
	if updated.WeightKg == nil || *updated.WeightKg != 2.5 {
		t.Errorf("WeightKg = %v, want 2.5", updated.WeightKg)
	}

	clear := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, variant.Id), map[string]any{"sku": variant.Sku})
	if clear.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", clear.Status, clear.Body)
	}
	var cleared variantJSON
	clear.JSON(&cleared)
	if cleared.Barcode != nil {
		t.Errorf("Barcode = %v, want nil (omitted from the PUT body must clear it)", *cleared.Barcode)
	}
	if cleared.WeightKg != nil {
		t.Errorf("WeightKg = %v, want nil", *cleared.WeightKg)
	}
}

// Ported from Integration/ProductCatalogFieldsTests.cs. CreateProduct_WithInvalidGtin_ReturnsFieldError.
func TestCreateProduct_WithInvalidGtin_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Bad Barcode", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "bad-gtin"), "barcode": "4006381333932"}},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["variants[0].barcode"]; !ok {
		t.Errorf("Errors = %v, want a 'variants[0].barcode' key", problem.Errors)
	}
}

// Ported from Integration/ProductCatalogFieldsTests.cs. CreateProduct_WithDuplicateBarcode_ReturnsConflict.
func TestCreateProduct_WithDuplicateBarcode_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	barcode := newValidGTIN13(t)

	first := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "First Barcode", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "bar-1"), "barcode": barcode}},
	})
	if first.Status != http.StatusCreated {
		t.Fatalf("first: status %d body %s, want 201", first.Status, first.Body)
	}
	second := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Second Barcode", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "bar-2"), "barcode": barcode}},
	})
	if second.Status != http.StatusConflict {
		t.Errorf("second: status %d body %s, want 409", second.Status, second.Body)
	}
}

// Ported from Integration/ProductCatalogFieldsTests.cs. UpdateVariant_WithBarcodeOfAnotherProduct_ReturnsConflict.
func TestUpdateVariant_WithBarcodeOfAnotherProduct_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	barcode := newValidGTIN13(t)

	owner := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Barcode Owner", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "bar-own"), "barcode": barcode}},
	})
	if owner.Status != http.StatusCreated {
		t.Fatalf("owner: status %d body %s, want 201", owner.Status, owner.Body)
	}
	thief := createProduct(t, c, newProductBody(taxCategoryID, "Barcode Thief", sku(t, "bar-thief")))

	update := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", thief.Id, thief.Variants[0].Id),
		map[string]any{"sku": *thief.Sku, "barcode": barcode})
	if update.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", update.Status, update.Body)
	}
}

// Ported from Integration/ProductCatalogFieldsTests.cs. CreateProduct_WithUnknownCategory_ReturnsFieldError.
func TestCreateProduct_WithUnknownCategory_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Orphan", "type": "Goods", "taxCategoryId": taxCategoryID, "categoryId": 999999,
		"variants": []map[string]any{{"sku": sku(t, "orphan")}},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["categoryId"]; !ok {
		t.Errorf("Errors = %v, want a 'categoryId' key", problem.Errors)
	}
}

// Ported from Integration/ProductCatalogFieldsTests.cs. GetProducts_SearchByExactBarcode_ReturnsOnlyThatProduct.
func TestGetProducts_SearchByExactBarcode_ReturnsOnlyThatProduct(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	barcode := newValidGTIN13(t)

	create := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Barcode Searchable", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "search-bar"), "barcode": barcode}},
	})
	if create.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", create.Status, create.Body)
	}
	var created productJSON
	create.JSON(&created)

	r := c.Do(http.MethodGet, "/api/v1/products?search="+barcode, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list productListJSON
	r.JSON(&list)
	if len(list.Data) != 1 || list.Data[0].Id != created.Id {
		t.Errorf("Data = %+v, want exactly the barcode-matching product (id %d)", list.Data, created.Id)
	}
}

// Ported from Integration/ProductCatalogFieldsTests.cs. GetProducts_SearchByVariantSku_ReturnsOnlyThatProduct.
func TestGetProducts_SearchByVariantSku_ReturnsOnlyThatProduct(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	variantSku := sku(t, "search-variant")
	created := createProduct(t, c, newProductBody(taxCategoryID, "SKU Searchable", variantSku))

	r := c.Do(http.MethodGet, "/api/v1/products?search="+variantSku, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list productListJSON
	r.JSON(&list)
	if len(list.Data) != 1 || list.Data[0].Id != created.Id {
		t.Errorf("Data = %+v, want exactly the sku-matching product (id %d)", list.Data, created.Id)
	}

	unrelated := c.Do(http.MethodGet, "/api/v1/products?search="+sku(t, "not-matching"), nil)
	var unrelatedList productListJSON
	unrelated.JSON(&unrelatedList)
	for _, p := range unrelatedList.Data {
		if p.Id == created.Id {
			t.Errorf("the sku-matching product (id %d) unexpectedly appears under an unrelated search", created.Id)
		}
	}
}

// Ported from Integration/ProductCatalogFieldsTests.cs. GetProducts_SearchMatchesDescription.
func TestGetProducts_SearchMatchesDescription(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	marker := "marker" + strconv.FormatInt(int64(testSkuCounter.Add(1)), 10)

	create := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Described Product", "type": "Goods", "taxCategoryId": taxCategoryID,
		"description": fmt.Sprintf("Contains the %s in the text.", marker),
		"variants":    []map[string]any{{"sku": sku(t, "search-desc")}},
	})
	if create.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", create.Status, create.Body)
	}
	var created productJSON
	create.JSON(&created)

	r := c.Do(http.MethodGet, "/api/v1/products?search="+marker, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list productListJSON
	r.JSON(&list)
	if len(list.Data) != 1 || list.Data[0].Id != created.Id {
		t.Errorf("Data = %+v, want exactly the description-matching product (id %d)", list.Data, created.Id)
	}
}
