package products_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file ports Integration/ProductsAuthorizationEndpointsTests.cs's
// permission matrix (products inventory §6). Two of that class's six facts
// already had Go homes and are marked where they live — the catalog-
// completeness assertion in module_test.go and the variant-delete
// requirement in permissions_test.go — and the four matrix facts are here.
//
// One adaptation runs through all four: .NET's `CreateAuthenticatedClient`
// returns the bootstrap **Owner**, whose permission check short-circuits to
// allow before any catalog lookup (identity inventory §1). modtest has no
// Owner, so the "can do everything" leg is a caller holding all ten of the
// module's permissions (harness_test.go's authenticatedClient). That is the
// same assertion for these tests' purpose — they are about which
// permissions gate which routes, not about the Owner short-circuit, which
// identity's own tests own. Every narrower leg is a genuine scoped role, as
// in .NET.

// Ported from Integration/ProductsAuthorizationEndpointsTests.cs.
// ProductsEndpoints_EnforceOwnerNarrowPermissionsViewOnlyMutationAndDisabledUsers.
func TestProductsEndpoints_EnforceFullNarrowViewOnlyAndDisabledCallers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	full := authenticatedClient(t, h)
	if r := full.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusOK {
		t.Errorf("full access GET: status %d body %s, want 200", r.Status, r.Body)
	}
	created := full.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "Full access product", sku(t, "authz-full")))
	if created.Status != http.StatusCreated {
		t.Errorf("full access POST: status %d body %s, want 201", created.Status, created.Body)
	}

	// A signed-in caller holding no permission at all: refused by the
	// router's own x-vantigo-access rule, with 403 rather than 401 — the
	// session is valid, the authority is not.
	none := h.SignIn(t)
	if r := none.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusForbidden {
		t.Errorf("no permissions GET: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := none.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "Denied product", sku(t, "authz-none"))); r.Status != http.StatusForbidden {
		t.Errorf("no permissions POST: status %d body %s, want 403", r.Status, r.Body)
	}

	// products-view alone is not enough for either: getProducts requires all
	// five view permissions and postProducts six keys (inventory §1.1).
	viewOnly := h.SignIn(t, "products:products-view")
	if r := viewOnly.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusForbidden {
		t.Errorf("view-only GET: status %d body %s, want 403 (getProducts needs all five view permissions)", r.Status, r.Body)
	}
	if r := viewOnly.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "View only product", sku(t, "authz-viewonly"))); r.Status != http.StatusForbidden {
		t.Errorf("view-only POST: status %d body %s, want 403", r.Status, r.Body)
	}

	// A disabled account answers 401, not 403: session lookup excludes a
	// disabled user's row, so the request never reaches a permission check.
	disabled := h.SignInDisabled(t, "products:products-view", "products:variants-view", "products:pricing-view",
		"products:categories-view", "products:tax-categories-view")
	if r := disabled.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusUnauthorized {
		t.Errorf("disabled user GET: status %d body %s, want 401", r.Status, r.Body)
	}
}

// Ported from Integration/ProductsAuthorizationEndpointsTests.cs.
// AggregateProductEndpointsRequireNestedVariantAndPricingPermissions.
func TestAggregateProductEndpoints_RequireNestedVariantAndPricingPermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	// The aggregate read and write both embed variants and pricing, so both
	// demand the nested view permissions even though the route is "products".
	productsOnly := h.SignIn(t, "products:products-view", "products:products-manage")
	if r := productsOnly.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusForbidden {
		t.Errorf("products-only GET: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := productsOnly.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "Products only", sku(t, "authz-agg-products"))); r.Status != http.StatusForbidden {
		t.Errorf("products-only POST: status %d body %s, want 403", r.Status, r.Body)
	}

	// Every key postProducts names, pricing-manage excluded: an unpriced
	// create succeeds and a priced one is refused by the handler's
	// conditional gate (inventory §7 oddity 1).
	withoutPricingManage := []string{
		"products:products-view", "products:products-manage",
		"products:variants-view", "products:variants-manage", "products:pricing-view",
		"products:categories-view", "products:tax-categories-view",
	}
	variantsOnly := h.SignIn(t, withoutPricingManage...)
	if r := variantsOnly.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusOK {
		t.Errorf("without pricing-manage GET: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := variantsOnly.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "Variants without pricing", sku(t, "authz-agg-unpriced"))); r.Status != http.StatusCreated {
		t.Errorf("without pricing-manage, unpriced POST: status %d body %s, want 201", r.Status, r.Body)
	}
	if r := variantsOnly.Do(http.MethodPost, "/api/v1/products", withPricingBody(taxCategoryID, "Pricing without permission", sku(t, "authz-agg-priced"))); r.Status != http.StatusForbidden {
		t.Errorf("without pricing-manage, priced POST: status %d body %s, want 403", r.Status, r.Body)
	}

	// .NET's third leg ("pricingViewOnly") grants exactly the same set as the
	// second — the two `CreateClientWithPermissionsAsync` calls are
	// character-for-character identical in the source — so it re-asserts the
	// priced-create refusal with a second, independently scoped role. Kept
	// rather than folded away, so the port covers what the original ran.
	pricingViewOnly := h.SignIn(t, withoutPricingManage...)
	if r := pricingViewOnly.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusOK {
		t.Errorf("pricing-view-only GET: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := pricingViewOnly.Do(http.MethodPost, "/api/v1/products", withPricingBody(taxCategoryID, "Pricing manage missing", sku(t, "authz-agg-priced2"))); r.Status != http.StatusForbidden {
		t.Errorf("pricing-view-only, priced POST: status %d body %s, want 403", r.Status, r.Body)
	}

	fullAggregate := h.SignIn(t, append(withoutPricingManage, "products:pricing-manage")...)
	if r := fullAggregate.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusOK {
		t.Errorf("full aggregate GET: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := fullAggregate.Do(http.MethodPost, "/api/v1/products", withPricingBody(taxCategoryID, "Full aggregate access", sku(t, "authz-agg-full"))); r.Status != http.StatusCreated {
		t.Errorf("full aggregate, priced POST: status %d body %s, want 201", r.Status, r.Body)
	}
}

// Ported from Integration/ProductsAuthorizationEndpointsTests.cs.
// ProductAggregateReadsRequireCategoryAndTaxCategoryViewPermissions.
func TestProductAggregateReads_RequireCategoryAndTaxCategoryViewPermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	withoutCategoryView := h.SignIn(t, "products:products-view", "products:variants-view",
		"products:pricing-view", "products:tax-categories-view")
	if r := withoutCategoryView.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusForbidden {
		t.Errorf("without categories-view: status %d body %s, want 403", r.Status, r.Body)
	}

	withoutTaxCategoryView := h.SignIn(t, "products:products-view", "products:variants-view",
		"products:pricing-view", "products:categories-view")
	if r := withoutTaxCategoryView.Do(http.MethodGet, "/api/v1/products", nil); r.Status != http.StatusForbidden {
		t.Errorf("without tax-categories-view: status %d body %s, want 403", r.Status, r.Body)
	}
}

// Ported from Integration/ProductsAuthorizationEndpointsTests.cs.
// CompositeMutationResponsesRequireMatchingViewPermissions.
//
// Each of these three creates returns the created resource, so the
// operation requires the matching -view permission alongside its -manage
// one: a caller who may write but not read is refused rather than handed a
// body it has no authority to see.
func TestCompositeMutationResponses_RequireMatchingViewPermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	categoryManageOnly := h.SignIn(t, "products:categories-manage")
	if r := categoryManageOnly.Do(http.MethodPost, "/api/v1/products/categories", map[string]any{"name": categoryName("denied")}); r.Status != http.StatusForbidden {
		t.Errorf("categories-manage only: status %d body %s, want 403", r.Status, r.Body)
	}

	taxCategoryManageOnly := h.SignIn(t, "products:tax-categories-manage")
	body := map[string]any{"name": "Denied tax category", "kind": "Standard", "rate": 0.25}
	if r := taxCategoryManageOnly.Do(http.MethodPost, "/api/v1/products/tax-categories", body); r.Status != http.StatusForbidden {
		t.Errorf("tax-categories-manage only: status %d body %s, want 403", r.Status, r.Body)
	}

	full := authenticatedClient(t, h)
	created := createProduct(t, full, newProductBody(taxCategoryID, "Composite mutation", sku(t, "authz-composite")))
	pricingManageOnly := h.SignIn(t, "products:pricing-manage")
	price := map[string]any{"currency": "NOK", "amount": 199}
	r := pricingManageOnly.Do(http.MethodPost,
		fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, created.Variants[0].Id), price)
	if r.Status != http.StatusForbidden {
		t.Errorf("pricing-manage only: status %d body %s, want 403 (the 201 body carries the price, so pricing-view is required too)", r.Status, r.Body)
	}
}
