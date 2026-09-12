package products_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// TestModule_ComposesAndDemandsAPermission proves products mounts through
// module.Compose without a router problem — newHarness fails the test on any
// Compose error, which is what catches an operation the generated server never
// registers or a permission the catalog is missing — and that the listing
// answers the access layer's 401 without a session, as the contract documents
// it.
func TestModule_ComposesAndDemandsAPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	r := h.Client(t).Do(http.MethodGet, "/api/v1/products", nil)
	if r.Status != http.StatusUnauthorized || r.Code() != "unauthenticated" {
		t.Errorf("status %d code %q body %s, want 401 unauthenticated", r.Status, r.Code(), r.Body)
	}
}

// TestModule_DeclaresItsPermissionCatalog pins the module's name and all ten
// permissions, field for field, against the .NET catalog
// (AZ/ProductsPermissionCatalog.cs, products inventory §5): every key is
// delegable and none is sensitive. It asserts an exact match — same length,
// same order, same fields — so a permission silently dropped, renamed, or
// added would fail here even though "the module has permissions" would still
// hold.
func TestModule_DeclaresItsPermissionCatalog(t *testing.T) {
	t.Parallel()
	m := products.Module()

	want := []contracts.Permission{
		{Key: "products:products-view", Display: "View products", Description: "View products and their details.", Category: "Products", Sensitive: false, Delegable: true},
		{Key: "products:products-manage", Display: "Manage products", Description: "Create, update, and archive products.", Category: "Products", Sensitive: false, Delegable: true},
		{Key: "products:variants-view", Display: "View variants", Description: "View product variants.", Category: "Variants", Sensitive: false, Delegable: true},
		{Key: "products:variants-manage", Display: "Manage variants", Description: "Create, update, and delete product variants.", Category: "Variants", Sensitive: false, Delegable: true},
		{Key: "products:pricing-view", Display: "View pricing", Description: "View product variant prices.", Category: "Pricing", Sensitive: false, Delegable: true},
		{Key: "products:pricing-manage", Display: "Manage pricing", Description: "Create, update, and delete product variant prices.", Category: "Pricing", Sensitive: false, Delegable: true},
		{Key: "products:categories-view", Display: "View categories", Description: "View product categories.", Category: "Categories", Sensitive: false, Delegable: true},
		{Key: "products:categories-manage", Display: "Manage categories", Description: "Create, update, and delete product categories.", Category: "Categories", Sensitive: false, Delegable: true},
		{Key: "products:tax-categories-view", Display: "View tax categories", Description: "View product tax categories.", Category: "Tax categories", Sensitive: false, Delegable: true},
		{Key: "products:tax-categories-manage", Display: "Manage tax categories", Description: "Create, update, and delete product tax categories.", Category: "Tax categories", Sensitive: false, Delegable: true},
	}
	if m.Name != "products" {
		t.Errorf("Name = %q, want products", m.Name)
	}
	if len(m.Permissions) != 10 {
		t.Fatalf("Permissions has %d entries, want exactly 10: %+v", len(m.Permissions), m.Permissions)
	}
	if !slices.Equal(m.Permissions, want) {
		t.Errorf("Permissions = %+v, want %+v", m.Permissions, want)
	}
	for _, p := range m.Permissions {
		if !p.Delegable {
			t.Errorf("permission %s: Delegable = false, want true (products inventory §5: every key is delegable)", p.Key)
		}
		if p.Sensitive {
			t.Errorf("permission %s: Sensitive = true, want false (products inventory §5: none is sensitive)", p.Key)
		}
	}
	if m.Directory != nil {
		t.Error("Module declares a customer directory, want nil: products publishes no contracts.CustomerDirectory")
	}
}

// TestModule_StubbedOperationAnswers501 proves every not-yet-implemented
// operation is still routed: a signed-in caller holding the operation's
// permission passes the access check and reaches the stub, which answers 501
// rather than a 404 from an unregistered route or a 500 from a nil handler.
// getProductsCategories is Task 12's (categories, tax categories and
// stats); products/variants/pricing (this task) are implemented and no
// longer answer 501, so the stub proof moves here.
func TestModule_StubbedOperationAnswers501(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Off-contract by design: a 501 is the platform's answer to a handler that
	// does not exist yet, and no operation documents one.
	r := h.SignIn(t, "products:categories-view").
		Do(http.MethodGet, "/api/v1/products/categories", nil, modtest.SkipContract("the operation is not implemented yet"))
	if r.Status != http.StatusNotImplemented {
		t.Errorf("status %d body %s, want 501", r.Status, r.Body)
	}
}
