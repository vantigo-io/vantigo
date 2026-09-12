package products_test

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports Integration/CategoriesEndpointsTests.cs (products
// inventory §6), plus this task's own additions: an explicit proof that a
// delete blocked by a Restrict foreign key answers the documented 409
// rather than .NET's unmapped 500 (dispatch requirement), and a pin for an
// undocumented .NET oddity found by reading source directly — every
// category response except the list endpoint's always reports
// productCount:0.

type categoryJSON struct {
	Id           int32  `json:"id"`
	Name         string `json:"name"`
	ParentId     *int32 `json:"parentId"`
	ProductCount int32  `json:"productCount"`
}

var testCategoryCounter atomic.Int64

// categoryName is CategoriesEndpointsTests.Name: a short, unique category
// name, matching sku()'s counter-over-random-suffix preference
// (harness_test.go).
func categoryName(prefix string) string {
	return fmt.Sprintf("Category %s %d", prefix, testCategoryCounter.Add(1))
}

// createCategory posts a category and requires 201, returning the decoded
// category. parentID nil omits parentId from the body entirely, matching
// CategoriesEndpointsTests.CreateAsync's default.
func createCategory(t *testing.T, c *modtest.Client, name string, parentID *int32) categoryJSON {
	t.Helper()
	body := map[string]any{"name": name}
	if parentID != nil {
		body["parentId"] = *parentID
	}
	r := c.Do(http.MethodPost, "/api/v1/products/categories", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create category: status %d body %s", r.Status, r.Body)
	}
	var created categoryJSON
	r.JSON(&created)
	return created
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// CreateCategory_AsRootAndSubcategory_ReturnsCreatedWithLocation.
func TestCreateCategory_AsRootAndSubcategory_ReturnsCreatedWithLocation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	root := createCategory(t, c, categoryName("root"), nil)
	if root.Id <= 0 {
		t.Fatalf("Id = %d, want > 0", root.Id)
	}
	if root.ParentId != nil {
		t.Errorf("ParentId = %v, want nil", root.ParentId)
	}

	r := c.Do(http.MethodPost, "/api/v1/products/categories", map[string]any{"name": categoryName("child"), "parentId": root.Id})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var child categoryJSON
	r.JSON(&child)
	if child.ParentId == nil || *child.ParentId != root.Id {
		t.Errorf("ParentId = %v, want %d", child.ParentId, root.Id)
	}

	wantLocation := fmt.Sprintf("/api/v1/products/categories/%d", child.Id)
	if got := r.Header("Location"); got != wantLocation {
		t.Errorf("Location = %q, want %q", got, wantLocation)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// GetCategories_ReturnsFlatListIncludingCreated.
func TestGetCategories_ReturnsFlatListIncludingCreated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	root := createCategory(t, c, categoryName("list-root"), nil)
	child := createCategory(t, c, categoryName("list-child"), &root.Id)

	r := c.Do(http.MethodGet, "/api/v1/products/categories", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var categories []categoryJSON
	r.JSON(&categories)

	var foundRoot, foundChild bool
	for _, cat := range categories {
		if cat.Id == root.Id && cat.ParentId == nil {
			foundRoot = true
		}
		if cat.Id == child.Id && cat.ParentId != nil && *cat.ParentId == root.Id {
			foundChild = true
		}
	}
	if !foundRoot {
		t.Error("root category not found in the flat list with a nil parentId")
	}
	if !foundChild {
		t.Error("child category not found in the flat list with parentId = root.Id")
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// GetCategories_ReportsDirectProductCounts.
func TestGetCategories_ReportsDirectProductCounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	root := createCategory(t, c, categoryName("count-root"), nil)
	child := createCategory(t, c, categoryName("count-child"), &root.Id)

	// Two products directly on the child, none on the root: the counts are
	// direct assignments only, subtree totals are a client concern.
	for _, name := range []string{"Counted One", "Counted Two"} {
		body := newProductBody(taxCategoryID, name, sku(t, "cnt"))
		body["categoryId"] = child.Id
		createProduct(t, c, body)
	}

	r := c.Do(http.MethodGet, "/api/v1/products/categories", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var categories []categoryJSON
	r.JSON(&categories)

	var childCount, rootCount int32 = -1, -1
	for _, cat := range categories {
		if cat.Id == child.Id {
			childCount = cat.ProductCount
		}
		if cat.Id == root.Id {
			rootCount = cat.ProductCount
		}
	}
	if childCount != 2 {
		t.Errorf("child ProductCount = %d, want 2", childCount)
	}
	if rootCount != 0 {
		t.Errorf("root ProductCount = %d, want 0 (direct assignment only)", rootCount)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// CreateCategory_WithMissingName_ReturnsFieldError.
func TestCreateCategory_WithMissingName_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/products/categories", map[string]any{"name": "  "})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["name"]; !ok {
		t.Errorf("errors = %v, want a key \"name\"", problem.Errors)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// CreateCategory_WithUnknownParent_ReturnsFieldError.
func TestCreateCategory_WithUnknownParent_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/products/categories", map[string]any{"name": categoryName("orphan"), "parentId": 999999})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["parentId"]; !ok {
		t.Errorf("errors = %v, want a key \"parentId\"", problem.Errors)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// CreateCategory_WithDuplicateSiblingName_ReturnsConflict.
func TestCreateCategory_WithDuplicateSiblingName_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	name := categoryName("dup")

	first := c.Do(http.MethodPost, "/api/v1/products/categories", map[string]any{"name": name})
	if first.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", first.Status, first.Body)
	}
	second := c.Do(http.MethodPost, "/api/v1/products/categories", map[string]any{"name": name})
	if second.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", second.Status, second.Body)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// UpdateCategory_RenamesAndReparents.
func TestUpdateCategory_RenamesAndReparents(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	root := createCategory(t, c, categoryName("move-root"), nil)
	other := createCategory(t, c, categoryName("move-other"), nil)
	child := createCategory(t, c, categoryName("move-child"), &root.Id)

	newName := categoryName("moved")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/categories/%d", child.Id),
		map[string]any{"name": newName, "parentId": other.Id})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated categoryJSON
	r.JSON(&updated)
	if updated.Name != newName {
		t.Errorf("Name = %q, want %q", updated.Name, newName)
	}
	if updated.ParentId == nil || *updated.ParentId != other.Id {
		t.Errorf("ParentId = %v, want %d", updated.ParentId, other.Id)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// UpdateCategory_MovingUnderDescendant_ReturnsConflict.
func TestUpdateCategory_MovingUnderDescendant_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	root := createCategory(t, c, categoryName("cycle-root"), nil)
	child := createCategory(t, c, categoryName("cycle-child"), &root.Id)
	grandchild := createCategory(t, c, categoryName("cycle-grand"), &child.Id)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/categories/%d", root.Id),
		map[string]any{"name": root.Name, "parentId": grandchild.Id})
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// UpdateCategory_UnderItself_ReturnsFieldError.
func TestUpdateCategory_UnderItself_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	category := createCategory(t, c, categoryName("self"), nil)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/categories/%d", category.Id),
		map[string]any{"name": category.Name, "parentId": category.Id})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["parentId"]; !ok {
		t.Errorf("errors = %v, want a key \"parentId\"", problem.Errors)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// DeleteCategory_WithChildren_ReturnsConflict. This is also, unavoidably,
// this task's proof that a delete blocked by a Restrict foreign key answers
// the documented 409 rather than .NET's 500: categories.go's
// DeleteProductsCategoriesById has no app-level pre-check ahead of the
// delete, so a blocked delete always round-trips through Postgres's real
// restrict_violation (23001) — see the dedicated test below for an
// explicit, unambiguous assertion of that.
func TestDeleteCategory_WithChildren_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	root := createCategory(t, c, categoryName("del-root"), nil)
	createCategory(t, c, categoryName("del-child"), &root.Id)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/categories/%d", root.Id), nil)
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// DeleteCategory_WithAssignedProducts_ReturnsConflict.
func TestDeleteCategory_WithAssignedProducts_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	category := createCategory(t, c, categoryName("del-assigned"), nil)
	body := newProductBody(taxCategoryID, "Categorised Product", sku(t, "del-assigned"))
	body["categoryId"] = category.Id
	createProduct(t, c, body)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/categories/%d", category.Id), nil)
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// DeleteCategory_WithoutChildrenOrProducts_ReturnsNoContent.
func TestDeleteCategory_WithoutChildrenOrProducts_ReturnsNoContent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	category := createCategory(t, c, categoryName("del-empty"), nil)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/categories/%d", category.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Errorf("status %d body %s, want 204", r.Status, r.Body)
	}

	fetch := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/categories/%d", category.Id), nil)
	if fetch.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", fetch.Status, fetch.Body)
	}
}

// Ported from Integration/CategoriesEndpointsTests.cs.
// DeleteCategory_UnknownId_ReturnsNotFound.
func TestDeleteCategory_UnknownId_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodDelete, "/api/v1/products/categories/999999", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestDeleteCategory_WithBothChildrenAndProducts_ReportsSubcategoriesFirst
// is not a port — neither DeleteCategory_WithChildren_ReturnsConflict nor
// DeleteCategory_WithAssignedProducts_ReturnsConflict above ever combines
// both blockers, so neither .NET's own test suite nor a direct port of it
// would catch a mutation that swapped
// DeleteProductsCategoriesById's diagnostic order (categories.go): .NET
// checks "has subcategories" strictly before "has products"
// (DeleteCategoryEndpoint.cs:27-41), so a category blocked by both must
// report subcategories, never products.
func TestDeleteCategory_WithBothChildrenAndProducts_ReportsSubcategoriesFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	category := createCategory(t, c, categoryName("del-both"), nil)
	createCategory(t, c, categoryName("del-both-child"), &category.Id)
	body := newProductBody(taxCategoryID, "Del Both Product", sku(t, "del-both"))
	body["categoryId"] = category.Id
	createProduct(t, c, body)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/categories/%d", category.Id), nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Category has subcategories" {
		t.Errorf("Title = %q, want %q (subcategories must be reported before products)", problem.Title, "Category has subcategories")
	}
}

// TestDeleteProductsCategoriesById_RestrictedDeleteAnswers409NotNETs500 is
// this task's dispatch-required proof, distinct from (though mechanically
// identical to) the ported DeleteCategory_WithChildren_ReturnsConflict
// above: a category still referenced by a subcategory answers the
// documented 409, not .NET's unmapped 500 (products inventory §4/§7
// oddity 7, corrected 2026-09-12 — VantigoExceptionHandler.cs:83-98's
// IsConstraintConflict special-cases only 23505/23P01, so a real Postgres
// restrict_violation falls through to a 500 there). This handler has no
// app-level pre-check ahead of the delete (categories.go's doc comment), so
// this genuinely exercises the real SQLSTATE mapping through the whole
// HTTP stack, and asserts the specific title the mapping path produces.
func TestDeleteProductsCategoriesById_RestrictedDeleteAnswers409NotNETs500(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	parent := createCategory(t, c, categoryName("restrict-parent"), nil)
	createCategory(t, c, categoryName("restrict-child"), &parent.Id)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/categories/%d", parent.Id), nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409 (the Restrict-backed delete must answer the documented 409, never a 500)",
			r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Category has subcategories" {
		t.Errorf("Title = %q, want %q", problem.Title, "Category has subcategories")
	}
}

// TestCategoryResponses_ProductCountIsZeroExceptOnTheList pins an oddity
// found by reading .NET source directly (not documented in the products
// inventory, the same way Task 11 verified its Location-header questions
// against source rather than the inventory's summary):
// CategoryResponse.FromDomain(category, productCount = 0) defaults to 0,
// and CreateCategoryEndpoint, GetCategoryEndpoint and UpdateCategoryEndpoint
// all call it with no second argument — a category with a real assigned
// product still reports productCount:0 from POST, GET .../{id} and PUT.
// Only GetCategoriesEndpoint (the list) ever computes and passes a real
// count.
func TestCategoryResponses_ProductCountIsZeroExceptOnTheList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	category := createCategory(t, c, categoryName("count-oddity"), nil)
	if category.ProductCount != 0 {
		t.Errorf("POST ProductCount = %d, want 0 (FromDomain's default, not a real count)", category.ProductCount)
	}

	body := newProductBody(taxCategoryID, "Count Oddity Product", sku(t, "count-oddity"))
	body["categoryId"] = category.Id
	createProduct(t, c, body)

	get := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/categories/%d", category.Id), nil)
	var fetched categoryJSON
	get.JSON(&fetched)
	if fetched.ProductCount != 0 {
		t.Errorf("GET ProductCount = %d, want 0 (GetCategoryEndpoint never computes a real count)", fetched.ProductCount)
	}

	put := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/categories/%d", category.Id), map[string]any{"name": category.Name})
	var updated categoryJSON
	put.JSON(&updated)
	if updated.ProductCount != 0 {
		t.Errorf("PUT ProductCount = %d, want 0 (UpdateCategoryEndpoint never computes a real count)", updated.ProductCount)
	}

	list := c.Do(http.MethodGet, "/api/v1/products/categories", nil)
	var categories []categoryJSON
	list.JSON(&categories)
	listCount := int32(-1)
	for _, cat := range categories {
		if cat.Id == category.Id {
			listCount = cat.ProductCount
		}
	}
	if listCount != 1 {
		t.Errorf("list ProductCount = %d, want 1 (only GetCategoriesEndpoint computes a real count)", listCount)
	}
}
