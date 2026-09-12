package products_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file ports Integration/ProductsEndpointsTests.cs (products inventory
// §6). oddities_test.go carries the three .NET oddities this task's
// dispatch calls out by name (ignored PUT variants, the wrong price
// Location header, camelCased option-value keys) with their own dedicated
// pins; permissions_test.go carries the conditional pricing-permission
// gate. A few tests here (list, single get, archive, list-variants,
// update-price) have no direct .NET counterpart in the excerpt above and
// exist to give getProducts/getProduct/deleteProductsById/
// getProductsByIdVariants/putProductsByIdVariantsByVariantIdPricesByPriceId
// their own successful, contract-validated exchange (RequireCoverage,
// main_test.go).

// Ported from Integration/ProductsEndpointsTests.cs.
// CreateProduct_WithRequiredVariant_ReturnsFlattenedSingleVariant.
func TestCreateProduct_WithRequiredVariant_ReturnsFlattenedSingleVariant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	r := c.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "Nordlys Lantern", sku(t, "create")))
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created productJSON
	r.JSON(&created)

	if created.Id <= 0 {
		t.Errorf("Id = %d, want > 0", created.Id)
	}
	if created.Status != "Draft" {
		t.Errorf("Status = %q, want Draft", created.Status)
	}
	if created.Sku == nil || *created.Sku != created.Variants[0].Sku {
		t.Errorf("Sku = %v, want %q (the sole variant's sku)", created.Sku, created.Variants[0].Sku)
	}
	if created.Unit == nil || *created.Unit != "pcs" {
		t.Errorf("Unit = %v, want pcs", created.Unit)
	}
	want := fmt.Sprintf("/api/v1/products/%d", created.Id)
	if got := r.Header("Location"); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// CreateProduct_WithoutVariants_ReturnsValidationError.
func TestCreateProduct_WithoutVariants_ReturnsValidationError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Missing Variant", "type": "Goods", "taxCategoryId": taxCategoryID,
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["variants"]; !ok {
		t.Errorf("Errors = %v, want a 'variants' key", problem.Errors)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// CreateProduct_WithPrices_ResolvesEffectivePricesOnVariantAndFlattenedResponse.
func TestCreateProduct_WithPrices_ResolvesEffectivePricesOnVariantAndFlattenedResponse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	now := h.Now()

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Campaign Product", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{
			{
				"sku": sku(t, "campaign"),
				"prices": []map[string]any{
					{"currency": "NOK", "amount": 599},
					{"currency": "SEK", "amount": 649},
					{"currency": "NOK", "amount": 499, "validFrom": now.AddDate(0, 0, -1), "validTo": now.AddDate(0, 0, 1)},
				},
			},
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created productJSON
	r.JSON(&created)

	if len(created.EffectivePrices) != 2 {
		t.Fatalf("len(EffectivePrices) = %d, want 2", len(created.EffectivePrices))
	}
	var nok float64
	for _, p := range created.EffectivePrices {
		if p.Currency == "NOK" {
			nok = p.Amount
		}
	}
	if nok != 499 {
		t.Errorf("NOK effective amount = %v, want 499", nok)
	}
	if len(created.Variants) != 1 || len(created.Variants[0].EffectivePrices) != len(created.EffectivePrices) {
		t.Errorf("the flattened top-level effectivePrices must equal the sole variant's own")
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// CreateProduct_WithDuplicateSku_ReturnsConflict.
func TestCreateProduct_WithDuplicateSku_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	dupSku := sku(t, "dup")

	first := c.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "First", dupSku))
	if first.Status != http.StatusCreated {
		t.Fatalf("first create: status %d body %s, want 201", first.Status, first.Body)
	}
	second := c.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "Second", dupSku))
	if second.Status != http.StatusConflict {
		t.Errorf("second create: status %d body %s, want 409", second.Status, second.Body)
	}
}

// TestCreateProduct_WithDuplicateSku_WithinRequest_QuotesTheRepeatedSku pins
// the exact message text when two variants of the *same* create request
// share a SKU: CreateProductEndpoint.cs:46-47's GroupBy finds the repeated
// key itself, so the quoted value is that key.
func TestCreateProduct_WithDuplicateSku_WithinRequest_QuotesTheRepeatedSku(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	repeated := sku(t, "within-request-dup")

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Within Request Duplicate", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": repeated}, {"sku": repeated}},
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Duplicate SKU" {
		t.Errorf("Title = %q, want %q", problem.Title, "Duplicate SKU")
	}
	want := fmt.Sprintf("A variant with SKU '%s' already exists.", repeated)
	if problem.Detail != want {
		t.Errorf("Detail = %q, want %q", problem.Detail, want)
	}
}

// TestCreateProduct_WithDuplicateSku_AgainstExistingCatalog_QuotesTheFirstVariantRegardless
// pins products inventory §6's CreateProductEndpoint oddity verbatim
// (CreateProductEndpoint.cs:51-54): when there is no within-request
// duplicate but the catalog already has one of the batch's SKUs, the
// message always quotes variants[0].Sku — even when, as here, it is the
// *second* variant (index 1) that actually collides. This is a faithful
// port of .NET's own quirk, not a bug to "improve" in Go: a mutation that
// quoted the actually-conflicting SKU instead would flip this assertion.
func TestCreateProduct_WithDuplicateSku_AgainstExistingCatalog_QuotesTheFirstVariantRegardless(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	existingSku := sku(t, "already-exists")
	existing := createProduct(t, c, newProductBody(taxCategoryID, "Pre-existing", existingSku))
	if existing.Id <= 0 {
		t.Fatalf("failed to seed the pre-existing SKU")
	}

	firstSku := sku(t, "innocent-first")
	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Batch With A Catalog Collision", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": firstSku}, {"sku": existingSku}},
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("A variant with SKU '%s' already exists.", firstSku)
	if problem.Detail != want {
		t.Errorf("Detail = %q, want %q (variants[0].Sku, not the SKU that actually conflicts)", problem.Detail, want)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// CreateProduct_WithConflictingBasePrices_ReturnsFieldError.
func TestCreateProduct_WithConflictingBasePrices_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Conflicting", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{
			{
				"sku": sku(t, "conflict"),
				"prices": []map[string]any{
					{"currency": "NOK", "amount": 599},
					{"currency": "NOK", "amount": 649},
				},
			},
		},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["variants[0].prices[1]"]; !ok {
		t.Errorf("Errors = %v, want a 'variants[0].prices[1]' key", problem.Errors)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// UpdateProduct_ChangesOnlySharedFields.
func TestUpdateProduct_ChangesOnlySharedFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Original", sku(t, "update")))

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": "Renamed", "type": "Service", "taxCategoryId": taxCategoryID, "description": "Updated",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated productJSON
	r.JSON(&updated)

	if updated.Name != "Renamed" {
		t.Errorf("Name = %q, want Renamed", updated.Name)
	}
	if updated.Type != "Service" {
		t.Errorf("Type = %q, want Service", updated.Type)
	}
	if updated.TaxCategory.Rate != 0.25 {
		t.Errorf("TaxCategory.Rate = %v, want 0.25", updated.TaxCategory.Rate)
	}
	if updated.Sku == nil || created.Sku == nil || *updated.Sku != *created.Sku {
		t.Errorf("Sku changed to %v, want unchanged %v", updated.Sku, created.Sku)
	}
}

// Ported from Integration/ProductsEndpointsTests.cs.
// GetProduct_MultiVariantDoesNotFlattenSellableFields.
func TestGetProduct_MultiVariantDoesNotFlattenSellableFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Colour Assortment", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{
			{"sku": sku(t, "red"), "optionValues": map[string]any{"Color": "Red"}},
			{"sku": sku(t, "blue"), "optionValues": map[string]any{"Color": "Blue"}},
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created productJSON
	r.JSON(&created)

	if created.Sku != nil {
		t.Errorf("Sku = %v, want nil (multi-variant product)", *created.Sku)
	}
	if created.Unit != nil {
		t.Errorf("Unit = %v, want nil", *created.Unit)
	}
	if len(created.EffectivePrices) != 0 {
		t.Errorf("EffectivePrices = %v, want empty", created.EffectivePrices)
	}
	if len(created.Variants) != 2 {
		t.Fatalf("len(Variants) = %d, want 2", len(created.Variants))
	}
	var red string
	for _, v := range created.Variants {
		if v.OptionValues["color"] == "Red" {
			red = v.OptionValues["color"]
		}
	}
	if red != "Red" {
		t.Errorf("no variant carried optionValues[\"color\"] == \"Red\"")
	}
}

// Ported from Integration/ProductsEndpointsTests.cs. GetProduct_UnknownId_ReturnsNotFound.
func TestGetProduct_UnknownId_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/products/999999", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestGetProduct_ReturnsCreatedProduct gives getProduct its own successful
// exchange (main_test.go's RequireCoverage): no .NET test above happens to
// call the single-get endpoint on a product it just created without also
// covering something else, so this one is added rather than folded in.
func TestGetProduct_ReturnsCreatedProduct(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Fetchable", sku(t, "get")))

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/%d", created.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var fetched productJSON
	r.JSON(&fetched)
	if fetched.Id != created.Id {
		t.Errorf("Id = %d, want %d", fetched.Id, created.Id)
	}
}

// TestGetProducts_ListsCreatedProducts gives getProducts its own successful
// exchange and pins basic pagination shape.
func TestGetProducts_ListsCreatedProducts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Listed Product", sku(t, "list")))

	r := c.Do(http.MethodGet, "/api/v1/products?pageSize=100", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list productListJSON
	r.JSON(&list)
	if list.Pagination.TotalCount < 1 {
		t.Errorf("TotalCount = %d, want >= 1", list.Pagination.TotalCount)
	}
	found := false
	for _, p := range list.Data {
		if p.Id == created.Id {
			found = true
		}
	}
	if !found {
		t.Errorf("the created product (id %d) is missing from the list", created.Id)
	}
}

// TestGetProducts_InvalidPageSize_ReturnsProblemDetail pins products
// inventory §7 oddity 8: this list endpoint's 400 is a single joined
// ProblemDetails.Detail sentence, not a field-keyed HttpValidationProblemDetails
// map — unlike every mutating endpoint.
func TestGetProducts_InvalidPageSize_ReturnsProblemDetail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/products?pageSize=0", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	want := "'pageSize' must be between 1 and 100, but was 0."
	if problem.Detail != want {
		t.Errorf("Detail = %q, want %q", problem.Detail, want)
	}
}

// TestDeleteProductsById_ArchivesAndIsIdempotent gives deleteProductsById
// its own successful exchange and pins the archive-not-delete/idempotent
// behaviour (ArchiveProductEndpoint.cs:28-32).
func TestDeleteProductsById_ArchivesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Archivable", sku(t, "archive")))

	first := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d", created.Id), nil)
	if first.Status != http.StatusNoContent {
		t.Fatalf("first delete: status %d body %s, want 204", first.Status, first.Body)
	}

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/%d", created.Id), nil)
	var fetched productJSON
	r.JSON(&fetched)
	if fetched.Status != "Discontinued" {
		t.Errorf("Status = %q, want Discontinued (archived, not deleted)", fetched.Status)
	}

	second := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d", created.Id), nil)
	if second.Status != http.StatusNoContent {
		t.Errorf("second delete: status %d body %s, want 204 (idempotent)", second.Status, second.Body)
	}
}

func TestDeleteProductsById_UnknownId_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodDelete, "/api/v1/products/999999", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestGetProductsByIdVariants_ListsProductVariants gives
// getProductsByIdVariants its own successful exchange.
func TestGetProductsByIdVariants_ListsProductVariants(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Variant Listing", sku(t, "varlist")))

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var variants []variantJSON
	r.JSON(&variants)
	if len(variants) != 1 {
		t.Fatalf("len(variants) = %d, want 1", len(variants))
	}
	if variants[0].Id != created.Variants[0].Id {
		t.Errorf("Id = %d, want %d", variants[0].Id, created.Variants[0].Id)
	}
}

// TestGetProducts_FiltersByCategoryIncludingDescendants exercises
// selfAndDescendantCategoryIDs (products.go, ported from
// ProductCategoryHierarchy.GetSelfAndDescendantIds): filtering by a root
// category must also include its subcategories' products, and must
// exclude a product in neither.
func TestGetProducts_FiltersByCategoryIncludingDescendants(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	root := insertCategory(t, h, "Furniture", nil)
	child := insertCategory(t, h, "Chairs", &root)

	rootBody := newProductBody(taxCategoryID, "Table", sku(t, "cat-root"))
	rootBody["categoryId"] = root
	inRoot := createProduct(t, c, rootBody)

	childBody := newProductBody(taxCategoryID, "Chair", sku(t, "cat-child"))
	childBody["categoryId"] = child
	inChild := createProduct(t, c, childBody)

	uncategorized := createProduct(t, c, newProductBody(taxCategoryID, "Loose Item", sku(t, "cat-none")))

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products?categoryId=%d&pageSize=100", root), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list productListJSON
	r.JSON(&list)

	ids := map[int32]bool{}
	for _, p := range list.Data {
		ids[p.Id] = true
	}
	if !ids[inRoot.Id] {
		t.Errorf("the root-category product (id %d) is missing from the categoryId=%d filter", inRoot.Id, root)
	}
	if !ids[inChild.Id] {
		t.Errorf("the child-category product (id %d) is missing from the categoryId=%d filter (descendants must be included)", inChild.Id, root)
	}
	if ids[uncategorized.Id] {
		t.Errorf("the uncategorized product (id %d) must not appear under categoryId=%d", uncategorized.Id, root)
	}
}

// TestGetProducts_CategoryIdAndUncategorized_ReturnsProblemDetail pins
// GetProductsEndpoint.Validate's mutual-exclusivity check (:120-123).
func TestGetProducts_CategoryIdAndUncategorized_ReturnsProblemDetail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/products?categoryId=1&uncategorized=true", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	want := "'categoryId' and 'uncategorized' cannot be combined."
	if problem.Detail != want {
		t.Errorf("Detail = %q, want %q", problem.Detail, want)
	}
}

func TestGetProductsByIdVariants_UnknownProduct_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/products/999999/variants", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}
