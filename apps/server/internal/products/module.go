// Package products is the products module: product categories, tax
// categories, products, their variants and each variant's prices, serving
// openapi/products.yaml under /api/v1/products/.
package products

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog
// (AZ/ProductsPermissionCatalog.cs, products inventory §5). The .NET
// contributor omits both Delegable and Sensitive on every descriptor, so
// every key takes PermissionDescriptor's defaults: Delegable=true,
// Sensitive=false — all ten permissions here are non-sensitive and
// delegable.
var permissions = []contracts.Permission{
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

// limits maps each rate-limited operationId to its policy. It is empty and
// stays empty: no Products endpoint calls RequireRateLimiting (products
// inventory §5), unlike identity's nine authentication policies.
var limits = map[string]ratelimit.Policy{}

// Module is products as a platform module: its contract mounted under
// /api/v1/products/ and its ten permissions in the composed catalog. Unlike
// customers, products publishes no contracts.CustomerDirectory — Directory
// is left nil.
func Module() module.Module {
	return module.Module{
		Name:        "products",
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when the router reports a problem: an operation never
// registered, a rule that does not parse, or a permission missing from the
// catalog.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  d.Access,
		Limiter: d.Limiter,
		Limits:  limits,
		Catalog: d.Catalog,
	})
	strict := gen.NewStrictHandlerWithOptions(newServer(d), nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(apicommon.WriteDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(apicommon.WriteDecodeError),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
