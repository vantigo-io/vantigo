package products_test

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports Integration/TaxCategoriesEndpointsTests.cs (products
// inventory §6, 4 methods), plus this task's own tests for behavior the
// .NET class never covers at all (no duplicate-name test exists there for
// tax categories, unlike categories' own CreateCategory_WithDuplicateSiblingName_ReturnsConflict)
// and an explicit, unambiguous proof of the RESTRICT-FK-blocked-delete
// divergence mirroring categories_test.go's.

type taxCategoryJSON struct {
	Id   int32   `json:"id"`
	Name string  `json:"name"`
	Kind string  `json:"kind"`
	Rate float64 `json:"rate"`
}

var testTaxCategoryCounter atomic.Int64

func taxCategoryName(prefix string) string {
	return fmt.Sprintf("Tax %s %d", prefix, testTaxCategoryCounter.Add(1))
}

func createTaxCategory(t *testing.T, c *modtest.Client, name, kind string, rate float64) taxCategoryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/products/tax-categories", map[string]any{"name": name, "kind": kind, "rate": rate})
	if r.Status != http.StatusCreated {
		t.Fatalf("create tax category: status %d body %s", r.Status, r.Body)
	}
	var created taxCategoryJSON
	r.JSON(&created)
	return created
}

// Ported from Integration/TaxCategoriesEndpointsTests.cs.
// TaxCategoryCrud_CreatesListsUpdatesAndDeletes.
func TestTaxCategoryCrud_CreatesListsUpdatesAndDeletes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	name := taxCategoryName("crud")

	created := createTaxCategory(t, c, name, "Reduced", 0.15)
	if created.Kind != "Reduced" {
		t.Errorf("Kind = %q, want Reduced", created.Kind)
	}
	if created.Rate != 0.15 {
		t.Errorf("Rate = %v, want 0.15", created.Rate)
	}

	list := c.Do(http.MethodGet, "/api/v1/products/tax-categories", nil)
	if list.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", list.Status, list.Body)
	}
	var categories []taxCategoryJSON
	list.JSON(&categories)
	found := false
	for _, cat := range categories {
		if cat.Id == created.Id {
			found = true
		}
	}
	if !found {
		t.Error("created tax category not found in the list")
	}

	update := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/tax-categories/%d", created.Id),
		map[string]any{"name": name, "kind": "Zero", "rate": 0})
	if update.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", update.Status, update.Body)
	}
	var updated taxCategoryJSON
	update.JSON(&updated)
	if updated.Kind != "Zero" {
		t.Errorf("Kind = %q, want Zero", updated.Kind)
	}

	del := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/tax-categories/%d", created.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Errorf("status %d body %s, want 204", del.Status, del.Body)
	}
}

// TestPostProductsTaxCategories_ReturnsLocationHeader is not a port — no
// .NET test in TaxCategoriesEndpointsTests.cs checks the Location header —
// but pins the contract question this task answered by reading
// CreateTaxCategoryEndpoint.cs directly (:35-38's CreatedAtRoute), the same
// way categories_test.go's
// TestCreateCategory_AsRootAndSubcategory_ReturnsCreatedWithLocation does
// for categories.
func TestPostProductsTaxCategories_ReturnsLocationHeader(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/products/tax-categories",
		map[string]any{"name": taxCategoryName("location"), "kind": "Standard", "rate": 0.25})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created taxCategoryJSON
	r.JSON(&created)

	wantLocation := fmt.Sprintf("/api/v1/products/tax-categories/%d", created.Id)
	if got := r.Header("Location"); got != wantLocation {
		t.Errorf("Location = %q, want %q", got, wantLocation)
	}
}

// Ported from Integration/TaxCategoriesEndpointsTests.cs.
// DeleteTaxCategory_InUse_ReturnsConflict.
func TestDeleteTaxCategory_InUse_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	category := createTaxCategory(t, c, taxCategoryName("in-use"), "Standard", 0.25)
	createProduct(t, c, newProductBody(category.Id, "Tax product", sku(t, "tax-in-use")))

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/tax-categories/%d", category.Id), nil)
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// Ported from Integration/TaxCategoriesEndpointsTests.cs.
// CreateProduct_WithUnknownTaxCategory_ReturnsFieldError.
func TestCreateProduct_WithUnknownTaxCategory_ReturnsFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/products", newProductBody(999999, "Unknown tax category", sku(t, "tax-unknown")))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["taxCategoryId"]; !ok {
		t.Errorf("errors = %v, want a key \"taxCategoryId\"", problem.Errors)
	}
}

// Ported from Integration/TaxCategoriesEndpointsTests.cs.
// ProductResponse_EmbedsTaxCategoryWithRate.
func TestProductResponse_EmbedsTaxCategoryWithRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	category := createTaxCategory(t, c, taxCategoryName("embedded"), "Reduced", 0.15)
	created := createProduct(t, c, newProductBody(category.Id, "Embedded tax product", sku(t, "tax-embed")))

	if created.TaxCategory.Id != category.Id {
		t.Errorf("TaxCategory.Id = %d, want %d", created.TaxCategory.Id, category.Id)
	}
	if created.TaxCategory.Kind != "Reduced" {
		t.Errorf("TaxCategory.Kind = %q, want Reduced", created.TaxCategory.Kind)
	}
	if created.TaxCategory.Rate != 0.15 {
		t.Errorf("TaxCategory.Rate = %v, want 0.15", created.TaxCategory.Rate)
	}
}

// TestDeleteProductsTaxCategoriesById_RestrictedDeleteAnswers409NotNETs500
// mirrors categories_test.go's explicit proof, for the analogous
// tax_category_id Restrict FK: taxcategories.go's
// DeleteProductsTaxCategoriesById has no app-level pre-check ahead of the
// delete, so this genuinely exercises Postgres's real restrict_violation
// (23001) through the whole HTTP stack — not .NET's unmapped 500 (products
// inventory §4/§7 oddity 7, corrected 2026-09-12).
func TestDeleteProductsTaxCategoriesById_RestrictedDeleteAnswers409NotNETs500(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	category := createTaxCategory(t, c, taxCategoryName("restrict"), "Standard", 0.25)
	createProduct(t, c, newProductBody(category.Id, "Restrict Proof Product", sku(t, "tax-restrict")))

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/tax-categories/%d", category.Id), nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409 (the Restrict-backed delete must answer the documented 409, never a 500)",
			r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Tax category has products" {
		t.Errorf("Title = %q, want %q", problem.Title, "Tax category has products")
	}
}

// The tests below are not ports: TaxCategoriesEndpointsTests.cs never
// exercises a duplicate name, an out-of-range rate, an invalid kind, or an
// unknown id, unlike categories' own equivalent tests. They still need
// direct coverage, both for the coverage gate and because each is a
// one-line mutation (dropping the duplicate-name check, flipping a rate
// boundary, mistyping a kind string) that no ported test would catch.

// TestCreateTaxCategory_WithDuplicateName_ReturnsConflictWithExactMessage
// pins CreateTaxCategoryEndpoint.cs:22-29's byte-exact title/detail.
func TestCreateTaxCategory_WithDuplicateName_ReturnsConflictWithExactMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	name := taxCategoryName("dup")

	createTaxCategory(t, c, name, "Standard", 0.25)

	r := c.Do(http.MethodPost, "/api/v1/products/tax-categories", map[string]any{"name": name, "kind": "Reduced", "rate": 0.1})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Duplicate tax category name" {
		t.Errorf("Title = %q, want %q", problem.Title, "Duplicate tax category name")
	}
	wantDetail := fmt.Sprintf("A tax category named '%s' already exists.", name)
	if problem.Detail != wantDetail {
		t.Errorf("Detail = %q, want %q", problem.Detail, wantDetail)
	}
}

// TestUpdateTaxCategory_ToADuplicateName_ReturnsConflict pins
// UpdateTaxCategoryEndpoint.cs:30-38's duplicate check, excluding the
// category being renamed (a category may keep its own name unchanged
// without tripping the check on itself).
func TestUpdateTaxCategory_ToADuplicateName_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	first := createTaxCategory(t, c, taxCategoryName("update-dup-1"), "Standard", 0.25)
	second := createTaxCategory(t, c, taxCategoryName("update-dup-2"), "Reduced", 0.1)

	conflict := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/tax-categories/%d", second.Id),
		map[string]any{"name": first.Name, "kind": second.Kind, "rate": second.Rate})
	if conflict.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", conflict.Status, conflict.Body)
	}

	unchanged := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/tax-categories/%d", second.Id),
		map[string]any{"name": second.Name, "kind": second.Kind, "rate": second.Rate})
	if unchanged.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200 (a category renamed to its own current name must not conflict with itself)",
			unchanged.Status, unchanged.Body)
	}
}

// TestTaxCategoryValidation_RejectsBadFieldsWithExactMessages pins
// TaxCategoryRequest.Validate's three checks (TaxCategoryRequest.cs:12-36)
// byte-exact, including the rate boundary: 0 and 1 are both valid, but
// anything outside is rejected with the raw value echoed back.
func TestTaxCategoryValidation_RejectsBadFieldsWithExactMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	cases := []struct {
		name       string
		body       map[string]any
		field      string
		wantDetail string
	}{
		{
			name:       "missing name",
			body:       map[string]any{"name": "  ", "kind": "Standard", "rate": 0.1},
			field:      "name",
			wantDetail: "'name' is required.",
		},
		{
			name:       "invalid kind",
			body:       map[string]any{"name": taxCategoryName("badkind"), "kind": "Luxury", "rate": 0.1},
			field:      "kind",
			wantDetail: "'kind' must be one of 'Standard', 'Reduced', 'Zero' or 'Exempt', but was 'Luxury'.",
		},
		{
			name:       "rate below zero",
			body:       map[string]any{"name": taxCategoryName("negrate"), "kind": "Standard", "rate": -0.01},
			field:      "rate",
			wantDetail: "'rate' must be between 0 and 1, but was -0.01.",
		},
		{
			name:       "rate above one",
			body:       map[string]any{"name": taxCategoryName("bigrate"), "kind": "Standard", "rate": 1.01},
			field:      "rate",
			wantDetail: "'rate' must be between 0 and 1, but was 1.01.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Do(http.MethodPost, "/api/v1/products/tax-categories", tc.body)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			msgs, ok := problem.Errors[tc.field]
			if !ok || len(msgs) == 0 {
				t.Fatalf("errors = %v, want a key %q", problem.Errors, tc.field)
			}
			if msgs[0] != tc.wantDetail {
				t.Errorf("errors[%q][0] = %q, want %q", tc.field, msgs[0], tc.wantDetail)
			}
		})
	}
}

// TestTaxCategoryValidation_AcceptsRateBoundaries proves 0 and 1 are both
// valid (the check is `Rate is < 0 or > 1`, an inclusive range on both
// ends — a mutation that made either bound exclusive would fail this).
func TestTaxCategoryValidation_AcceptsRateBoundaries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	zero := createTaxCategory(t, c, taxCategoryName("rate-zero"), "Zero", 0)
	if zero.Rate != 0 {
		t.Errorf("Rate = %v, want 0", zero.Rate)
	}
	one := createTaxCategory(t, c, taxCategoryName("rate-one"), "Standard", 1)
	if one.Rate != 1 {
		t.Errorf("Rate = %v, want 1", one.Rate)
	}
}

// TestGetTaxCategory_ReturnsTheCreatedRow pins getTaxCategory's success
// path (GetTaxCategoryEndpoint.cs:9-23) — not itself a .NET port, since the
// class exercises the id-scoped GET only indirectly, through the create
// helper's own response.
func TestGetTaxCategory_ReturnsTheCreatedRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	created := createTaxCategory(t, c, taxCategoryName("get"), "Exempt", 0)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/tax-categories/%d", created.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var fetched taxCategoryJSON
	r.JSON(&fetched)
	if fetched != created {
		t.Errorf("fetched = %+v, want %+v", fetched, created)
	}
}

// TestTaxCategory_UnknownId_ReturnsNotFound pins the 404 shared by
// GetTaxCategory, PutProductsTaxCategoriesById and
// DeleteProductsTaxCategoriesById for an id that does not exist.
func TestTaxCategory_UnknownId_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	if r := c.Do(http.MethodGet, "/api/v1/products/tax-categories/999999", nil); r.Status != http.StatusNotFound {
		t.Errorf("GET status %d body %s, want 404", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/products/tax-categories/999999",
		map[string]any{"name": taxCategoryName("missing"), "kind": "Standard", "rate": 0.1}); r.Status != http.StatusNotFound {
		t.Errorf("PUT status %d body %s, want 404", r.Status, r.Body)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/products/tax-categories/999999", nil); r.Status != http.StatusNotFound {
		t.Errorf("DELETE status %d body %s, want 404", r.Status, r.Body)
	}
}
