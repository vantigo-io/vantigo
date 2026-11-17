// Package purchasing is the Purchasing module (nb Innkjøp): the buy side of
// the chain Orders is the sell side of — supplier items, purchase orders with
// their PDF and mail, and later receipts, proposals and the supplier invoice
// matched to an order (purchasing design,
// docs/superpowers/specs/2026-11-17-purchasing-design.md). It reads the
// supplier through contracts.SupplierDirectory, the catalog through
// contracts.ProductCatalog, the sites through contracts.Stock and the company
// through contracts.SalesTax, and requires the accounting, inventory and
// products modules (P8).
package purchasing

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// The seven permission keys (§8). Every key is delegable but the settings',
// which administers the module; none is sensitive. Delivery A's operations
// ask for access, orders-manage, orders-send, supplier-items-manage and
// settings; receive and invoices-match are declared now so a role can be
// built once, and are first asked for by the delivery named beside each.
const (
	permAccess              = "purchasing:access"
	permOrdersManage        = "purchasing:orders-manage"
	permOrdersSend          = "purchasing:orders-send"
	permReceive             = "purchasing:receive" // delivery B
	permSupplierItemsManage = "purchasing:supplier-items-manage"
	permInvoicesMatch       = "purchasing:invoices-match" // delivery D
	permSettings            = "purchasing:settings"
)

var permissions = []contracts.Permission{
	{
		Key: permAccess, Display: "Use Purchasing",
		Description: "See the Purchasing app and read purchase orders and supplier items.",
		Category:    "Purchasing", Sensitive: false, Delegable: true,
	},
	{
		Key: permOrdersManage, Display: "Manage purchase orders",
		Description: "Write draft purchase orders, change sent ones, record the supplier's confirmation, and close and reopen them.",
		Category:    "Purchasing", Sensitive: false, Delegable: true,
	},
	{
		Key: permOrdersSend, Display: "Send purchase orders",
		Description: "Send purchase orders, mark them as sent, send them again and cancel them.",
		Category:    "Purchasing", Sensitive: false, Delegable: true,
	},
	{
		Key: permReceive, Display: "Receive goods (coming)",
		Description: "Not in use yet: receiving goods against purchase orders comes in a later version of Purchasing.",
		Category:    "Purchasing", Sensitive: false, Delegable: true,
	},
	{
		Key: permSupplierItemsManage, Display: "Manage supplier items",
		Description: "Write and import supplier items: what each supplier sells, at what price.",
		Category:    "Purchasing", Sensitive: false, Delegable: true,
	},
	{
		Key: permInvoicesMatch, Display: "Match supplier invoices (coming)",
		Description: "Not in use yet: matching supplier invoices to purchase orders comes in a later version of Purchasing.",
		Category:    "Purchasing", Sensitive: false, Delegable: true,
	},
	{
		Key: permSettings, Display: "Manage Purchasing settings",
		Description: "Change the settings: the order series, the default site, the mail channel, the order text and the delivery addresses.",
		Category:    "Purchasing", Sensitive: false, Delegable: false,
	},
}

// limits are the module's rate limits; none beyond the platform's.
var limits = map[string]ratelimit.Policy{}

// Module is the Purchasing module.
func Module() module.Module {
	return module.Module{
		Name:        "purchasing",
		Permissions: permissions,
		Mount:       mount,
		Workers:     workers,
		// The slots below are composed whatever MODULES says, each from
		// what a disabled module's Deps has too (rules 20, 27, 28).
		VariantReferences:    newVariantReferenceHolder,
		SupplierReferences:   newSupplierReferenceHolder,
		SupplierPersonalData: newSupplierPersonalData,
		// The open purchases (P4), resolved from the enabled modules only.
		PurchaseSupply: newPurchaseSupply,
	}
}

// mount builds the module's handler. It refuses to mount without the
// supplier directory, the product catalog or the stock: config makes
// purchasing without accounting, products or inventory impossible (P8), and
// a nil contract would fail every order.
func mount(d module.Deps) (http.Handler, error) {
	if d.SupplierDirectory == nil {
		return nil, errors.New("purchasing: no supplier directory; the purchasing module requires the accounting module")
	}
	if d.Products == nil {
		return nil, errors.New("purchasing: no product catalog; the purchasing module requires the products module")
	}
	if d.Stock == nil {
		return nil, errors.New("purchasing: no stock; the purchasing module requires the inventory module")
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
