package products_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// testSkuCounter makes sku's output unique across a whole test binary run
// (sku is catalog-wide unique, products inventory §2 oddity 10), without
// the collision risk or unreadability of a random suffix.
var testSkuCounter atomic.Int64

// newHarness is one products installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against products.yaml through the package recorder.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(products.Module()),
	}, opts...)...)
}

// authenticatedClient signs in a caller holding every one of the module's
// ten permissions — .NET's ProductsApiFactory.CreateAuthenticatedClient,
// which the ported tests assume throughout (TS/ProductsEndpointsTests.cs
// and friends). The permission-gate tests that need a caller *without* some
// permission sign in separately with a narrower set (permissions_test.go).
func authenticatedClient(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t,
		"products:products-view", "products:products-manage",
		"products:variants-view", "products:variants-manage",
		"products:pricing-view", "products:pricing-manage",
		"products:categories-view", "products:categories-manage",
		"products:tax-categories-view", "products:tax-categories-manage")
}

// insertTaxCategory creates a tax category directly (categories/tax
// categories are Task 12's own operations, not yet implemented) and returns
// its id. rate is the fraction (e.g. 0.25 for 25%).
func insertTaxCategory(t *testing.T, h *modtest.Harness, name string, rate float64) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO products.tax_categories (name, kind, rate, created_at, updated_at)
		VALUES ($1, 'Standard', $2, $3, $3)
		RETURNING id`, name, rate, h.Now())
}

// insertCategory creates a product category directly and returns its id.
func insertCategory(t *testing.T, h *modtest.Harness, name string, parentID *int32) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO products.product_categories (name, parent_id) VALUES ($1, $2) RETURNING id`,
		name, parentID)
}

// sku returns a short, unique, all-caps SKU under the column's 64-character
// limit, matching .NET's ProductsEndpointsTests.Sku helper's shape.
func sku(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("TST-%s-%d", prefix, testSkuCounter.Add(1))
}
