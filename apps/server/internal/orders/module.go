// Package orders is the Orders module (nb Ordre): the quotes and sales orders
// the business makes for its customers, their PDFs and their sending, and
// later their invoicing, subscriptions, deliveries and shipping (orders
// design, docs/superpowers/specs/2026-11-05-orders-design.md). It reads the
// customer through contracts.CustomerDirectory, the catalog through
// contracts.ProductCatalog and the VAT codes and the seller through
// contracts.SalesTax, and requires the products, customers and invoices
// modules (O9).
package orders

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// The nine permission keys (§8). Every key is delegable but the two that
// administer the module, and only the carriers' key, whose holder sees the
// carrier credentials, is sensitive. Delivery A's operations ask for access,
// quotes-manage, sales-manage, sales-confirm and settings; the other four are
// declared now so a role can be built once, and are first asked for by the
// delivery named beside each.
const (
	permAccess              = "orders:access"
	permQuotesManage        = "orders:quotes-manage"
	permSalesManage         = "orders:sales-manage"
	permSalesConfirm        = "orders:sales-confirm"
	permSubscriptionsManage = "orders:subscriptions-manage" // delivery S
	permDeliveriesManage    = "orders:deliveries-manage"    // deliveries C, shipments D
	permCarriersManage      = "orders:carriers-manage"      // delivery D
	permAnalysisView        = "orders:analysis-view"        // delivery A2
	permSettings            = "orders:settings"
)

var permissions = []contracts.Permission{
	{
		Key: permAccess, Display: "Use Orders",
		Description: "See the Orders app and read quotes, orders, subscriptions and deliveries.",
		Category:    "Orders", Sensitive: false, Delegable: true,
	},
	{
		Key: permQuotesManage, Display: "Manage quotes",
		Description: "Create, edit, send, revise, accept, decline and cancel quotes.",
		Category:    "Orders", Sensitive: false, Delegable: true,
	},
	{
		Key: permSalesManage, Display: "Manage orders",
		Description: "Create and edit orders, hold and release them, cancel them and register what was delivered.",
		Category:    "Orders", Sensitive: false, Delegable: true,
	},
	{
		Key: permSalesConfirm, Display: "Confirm orders",
		Description: "Confirm orders, and lock and unlock them.",
		Category:    "Orders", Sensitive: false, Delegable: true,
	},
	{
		Key: permSubscriptionsManage, Display: "Manage subscriptions",
		Description: "Create, activate, change, pause and end subscriptions.",
		Category:    "Orders", Sensitive: false, Delegable: true,
	},
	{
		Key: permDeliveriesManage, Display: "Manage deliveries",
		Description: "Create, pick, post and cancel deliveries; create, book and cancel shipments and print their labels.",
		Category:    "Orders", Sensitive: false, Delegable: true,
	},
	{
		Key: permCarriersManage, Display: "Manage carriers",
		Description: "Connect carrier accounts and manage their keys under Integrations.",
		Category:    "Orders", Sensitive: true, Delegable: false,
	},
	{
		Key: permAnalysisView, Display: "View sales analysis",
		Description: "See the analysis of quotes and orders.",
		Category:    "Orders", Sensitive: false, Delegable: true,
	},
	{
		Key: permSettings, Display: "Manage Orders settings",
		Description: "Change the settings, the number series, the templates and the warnings, and override a warning's block.",
		Category:    "Orders", Sensitive: false, Delegable: false,
	},
}

// limits are the module's rate limits; none beyond the platform's.
var limits = map[string]ratelimit.Policy{}

// Module is the Orders module.
func Module() module.Module {
	return module.Module{
		Name:        "orders",
		Permissions: permissions,
		Mount:       mount,
		Workers:     workers,
	}
}

// mount builds the module's handler. It refuses to mount without the
// customer directory, the product catalog or the sales tax: config makes
// orders without customers, products or invoices impossible (O9), and a nil
// contract would fail every document.
func mount(d module.Deps) (http.Handler, error) {
	if d.Directory == nil {
		return nil, errors.New("orders: no customer directory; the orders module requires the customers module")
	}
	if d.Products == nil {
		return nil, errors.New("orders: no product catalog; the orders module requires the products module")
	}
	if d.SalesTax == nil {
		return nil, errors.New("orders: no sales tax; the orders module requires the invoices module")
	}
	if err := checkClosedSchemas(d.Doc); err != nil {
		return nil, err
	}
	srv, err := newServer(d)
	if err != nil {
		return nil, err
	}
	router := module.NewRouter(module.RouterOptions{
		Doc: d.Doc, Access: d.Access, Limiter: d.Limiter, Limits: limits, Catalog: d.Catalog,
	})
	strict := gen.NewStrictHandlerWithOptions(srv, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(apicommon.WriteDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(apicommon.WriteDecodeError),
		Middlewares:      []gen.MiddlewareFunc{keepRawBody},
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
