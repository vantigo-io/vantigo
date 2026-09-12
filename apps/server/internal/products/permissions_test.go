package products_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file pins the conditional pricing-permission gate products inventory
// §1.1/§7 oddity 1 and this task's dispatch correct: when a create-product
// or create-variant payload carries pricing data (a non-null standardCost
// or a non-empty prices array on any variant), the handler additionally
// requires BOTH products:pricing-view AND products:pricing-manage — not
// pricing-manage alone, the error an earlier misreading of the same
// Global-Constraints clause shipped as a privilege gap in this project
// before. None of these are ports: no .NET test in products inventory §6's
// list exercises this permission directly the way
// AggregateProductEndpointsRequireNestedVariantAndPricingPermissions does
// through .NET's own role/user HTTP plumbing — modtest.Harness.SignIn
// reaches the same assertions directly.
//
// putProductsByIdVariantsByVariantId's pricing-manage requirement, by
// contrast, is unconditional and already declared in the contract's
// x-vantigo-access (verified against openapi/products.yaml:
// "permission:products:pricing-manage+products:pricing-view+products:variants-manage+products:variants-view"),
// so it is enforced by module.Router before the handler ever runs — no
// handler-side check exists for it, and none should.

// withPricing is a single-variant, pricing-bearing create-product body.
func withPricingBody(taxCategoryID int32, name, sku string) map[string]any {
	return map[string]any{
		"name": name, "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku, "standardCost": 42}},
	}
}

func TestCreateProduct_WithPricingData_WithoutPricingManagePermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	// Holds pricing-view but not pricing-manage: the correction this task
	// exists to make — pricing-view alone must not be enough.
	c := h.SignIn(t, "products:products-manage", "products:variants-manage", "products:variants-view",
		"products:pricing-view", "products:categories-view", "products:tax-categories-view")

	r := c.Do(http.MethodPost, "/api/v1/products", withPricingBody(taxCategoryID, "Denied by manage", sku(t, "gate-manage")))
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
	if r.Code() != "forbidden" {
		t.Errorf("Code() = %q, want \"forbidden\"", r.Code())
	}
}

// pricing-view is already part of postProducts's static x-vantigo-access
// (products inventory §1.1 line 45), so a caller lacking it is refused by
// module.Router itself before this handler ever runs — this test still
// pins the end-to-end outcome, even though the handler's own conditional
// re-check of pricing-view (server.go's hasPermission) can never actually
// be the thing that denies anyone in practice.
func TestCreateProduct_WithPricingData_WithoutPricingViewPermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	// Holds pricing-manage but not pricing-view.
	c := h.SignIn(t, "products:products-manage", "products:variants-manage", "products:variants-view",
		"products:pricing-manage", "products:categories-view", "products:tax-categories-view")

	r := c.Do(http.MethodPost, "/api/v1/products", withPricingBody(taxCategoryID, "Denied by view", sku(t, "gate-view")))
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

func TestCreateProduct_WithPricingData_WithNeitherPricingPermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	c := h.SignIn(t, "products:products-manage", "products:variants-manage", "products:variants-view",
		"products:categories-view", "products:tax-categories-view")

	r := c.Do(http.MethodPost, "/api/v1/products", withPricingBody(taxCategoryID, "Denied by neither", sku(t, "gate-neither")))
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

func TestCreateProduct_WithPricingData_WithBothPricingPermissions_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	c := h.SignIn(t, "products:products-manage", "products:variants-manage", "products:variants-view",
		"products:pricing-view", "products:pricing-manage", "products:categories-view", "products:tax-categories-view")

	r := c.Do(http.MethodPost, "/api/v1/products", withPricingBody(taxCategoryID, "Allowed", sku(t, "gate-both")))
	if r.Status != http.StatusCreated {
		t.Errorf("status %d body %s, want 201", r.Status, r.Body)
	}
}

// TestCreateProduct_WithoutPricingData_DoesNotRequirePricingManage proves
// the gate is genuinely conditional: postProducts's own x-vantigo-access
// already requires pricing-view unconditionally (products inventory §1.1
// line 45), so the only permission truly invisible to the router is
// pricing-manage — a caller holding every statically-required permission
// but not pricing-manage may still create a product whose variant carries
// no standardCost and no prices.
func TestCreateProduct_WithoutPricingData_DoesNotRequirePricingManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	c := h.SignIn(t, "products:products-manage", "products:variants-manage", "products:variants-view",
		"products:pricing-view", "products:categories-view", "products:tax-categories-view")

	r := c.Do(http.MethodPost, "/api/v1/products", newProductBody(taxCategoryID, "No Pricing Needed", sku(t, "gate-none")))
	if r.Status != http.StatusCreated {
		t.Errorf("status %d body %s, want 201", r.Status, r.Body)
	}
}

// TestCreateProduct_PricingGateRunsBeforeFieldValidation pins the gate's
// position ahead of ProductRequest.Validate (CreateProductEndpoint.cs:24-43):
// a request that is both missing the pricing permission and would also
// fail name validation must answer 403, never 400.
func TestCreateProduct_PricingGateRunsBeforeFieldValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	c := h.SignIn(t, "products:products-manage", "products:variants-manage", "products:variants-view",
		"products:pricing-view", "products:categories-view", "products:tax-categories-view")

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "", // also invalid, but must never be reached
		"type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "gate-order"), "standardCost": 1}},
	})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403 (the permission gate must run before name validation)", r.Status, r.Body)
	}
}

// TestAddVariant_WithPricingData_WithoutPricingManagePermission_Returns403
// is postProductsByIdVariants' own conditional gate
// (AddProductVariantEndpoint.cs:24-31), the same two-permission shape as
// postProducts.
func TestAddVariant_WithPricingData_WithoutPricingManagePermission_Returns403(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, owner, newProductBody(taxCategoryID, "Variant Gate Target", sku(t, "vgate-target")))

	c := h.SignIn(t, "products:variants-manage", "products:variants-view", "products:pricing-view")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id),
		map[string]any{"sku": sku(t, "vgate-manage"), "standardCost": 10})
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403", r.Status, r.Body)
	}
}

func TestAddVariant_WithPricingData_WithBothPricingPermissions_Succeeds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, owner, newProductBody(taxCategoryID, "Variant Gate Allowed", sku(t, "vgate-allowed-base")))

	c := h.SignIn(t, "products:variants-manage", "products:variants-view", "products:pricing-view", "products:pricing-manage")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id),
		map[string]any{"sku": sku(t, "vgate-allowed"), "standardCost": 10})
	if r.Status != http.StatusCreated {
		t.Errorf("status %d body %s, want 201", r.Status, r.Body)
	}
}

// TestAddVariant_WithoutPricingData_DoesNotRequirePricingManage is the
// conditionality proof for the variant sub-resource, mirroring
// TestCreateProduct_WithoutPricingData_DoesNotRequirePricingManage:
// postProductsByIdVariants also requires pricing-view unconditionally
// (products inventory §1.1 line 50), so only pricing-manage is the
// genuinely conditional, router-invisible permission.
func TestAddVariant_WithoutPricingData_DoesNotRequirePricingManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, owner, newProductBody(taxCategoryID, "Variant No Pricing", sku(t, "vgate-nopricing-base")))

	c := h.SignIn(t, "products:variants-manage", "products:variants-view", "products:pricing-view")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), map[string]any{"sku": sku(t, "vgate-nopricing")})
	if r.Status != http.StatusCreated {
		t.Errorf("status %d body %s, want 201", r.Status, r.Body)
	}
}

// TestPutVariant_RequiresPricingManageUnconditionally proves
// putProductsByIdVariantsByVariantId's route-level (not handler-level)
// pricing-manage requirement: even a payload that carries no pricing field
// at all is refused for a caller who lacks pricing-manage — because the
// contract's own x-vantigo-access names it unconditionally (products
// inventory §1.1 line 51), enforced entirely by module.Router before this
// operation's handler runs.
func TestPutVariant_RequiresPricingManageUnconditionally(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, owner, newProductBody(taxCategoryID, "Unconditional Gate", sku(t, "put-gate")))
	variant := created.Variants[0]

	// Every permission putProductsByIdVariantsByVariantId's x-vantigo-access
	// names except pricing-manage.
	c := h.SignIn(t, "products:variants-manage", "products:variants-view", "products:pricing-view")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, variant.Id),
		map[string]any{"sku": variant.Sku}) // no pricing field in the body at all
	if r.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403 (pricing-manage is required unconditionally, not just when the body carries pricing data)", r.Status, r.Body)
	}
}

// Ported from Integration/ProductsAuthorizationEndpointsTests.cs.
// VariantDeleteRequiresPricingManageBecauseItCascadesPrices.
//
// TestDeleteVariant_RequiresPricingManageBecauseItCascadesPrices is the
// route-level analogue for deleteProductsByIdVariantsByVariantId (products
// inventory §1.1 line 52).
func TestDeleteVariant_RequiresPricingManageBecauseItCascadesPrices(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, owner, newProductBody(taxCategoryID, "Delete Cascade", sku(t, "delcascade-1")))
	addSecond := owner.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), map[string]any{"sku": sku(t, "delcascade-2")})
	var second variantJSON
	addSecond.JSON(&second)

	variantsManageOnly := h.SignIn(t, "products:variants-manage")
	denied := variantsManageOnly.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
	if denied.Status != http.StatusForbidden {
		t.Errorf("status %d body %s, want 403 (variants-manage alone is not enough)", denied.Status, denied.Body)
	}

	fullDelete := h.SignIn(t, "products:variants-manage", "products:pricing-manage")
	allowed := fullDelete.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
	if allowed.Status != http.StatusNoContent {
		t.Errorf("status %d body %s, want 204", allowed.Status, allowed.Body)
	}
}
