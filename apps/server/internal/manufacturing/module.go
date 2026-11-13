// Package manufacturing is the Manufacturing module (nb Produksjon): making
// the catalog's goods from other goods — the work centers where the work is
// done, the bills of materials that say how to make a variant, and later the
// manufacturing orders, the shop floor and the production schedule
// (manufacturing design, docs/superpowers/specs/2026-11-11-manufacturing-design.md).
// It reads and locks the catalog's variants through contracts.ProductCatalog,
// reads Inventory's sites and stock through contracts.Stock and moves stock
// through contracts.StockLedger, and requires the products and inventory
// modules (M6).
package manufacturing

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// moduleName is the module's name, its schema and the module of every stock
// source it names.
const moduleName = "manufacturing"

// The five permission keys (§8). Every key is delegable but costs-view, the
// one sensitive key: its holder sees the cost rates and the standard costs
// (M38). Delivery A's operations ask for access, boms-manage and costs-view;
// delivery B's for orders-manage, and registering a production for
// shop-floor or orders-manage (M33); work orders ask for shop-floor from C.
const (
	permAccess       = "manufacturing:access"
	permBomsManage   = "manufacturing:boms-manage"
	permOrdersManage = "manufacturing:orders-manage"
	permShopFloor    = "manufacturing:shop-floor"
	permCostsView    = "manufacturing:costs-view"
	// permQualityManage closes deviations, which releases their own holds
	// (manufacturing phase 2 M102).
	permQualityManage = "manufacturing:quality-manage"
)

var permissions = []contracts.Permission{
	{
		Key: permAccess, Display: "Use Manufacturing",
		Description: "See the Manufacturing app and read work centers, bills of materials and manufacturing orders.",
		Category:    "Manufacturing", Sensitive: false, Delegable: true,
	},
	{
		Key: permBomsManage, Display: "Manage bills of materials and work centers",
		Description: "Create, change, archive and delete work centers and their closures, and create, change, archive and make default bills of materials.",
		Category:    "Manufacturing", Sensitive: false, Delegable: true,
	},
	{
		Key: permOrdersManage, Display: "Manage manufacturing orders",
		Description: "Create, confirm, change, cancel and close manufacturing orders and post their production.",
		Category:    "Manufacturing", Sensitive: false, Delegable: true,
	},
	{
		Key: permShopFloor, Display: "Work on the shop floor",
		Description: "Register production, and start, pause and finish work orders and log time at a work center.",
		Category:    "Manufacturing", Sensitive: false, Delegable: true,
	},
	{
		Key: permCostsView, Display: "See manufacturing costs",
		Description: "See and write the work centers' cost rates, the standard costs and the rolled-up cost of a bill of materials.",
		Category:    "Manufacturing", Sensitive: true, Delegable: false,
	},
	{
		Key: permQualityManage, Display: "Manage quality",
		Description: "Close deviations with their disposition, which releases the quarantine holds they put on lots, and release the holds of a deviation closed as scrap or return.",
		Category:    "Manufacturing", Sensitive: false, Delegable: true,
	},
}

// limits are the module's rate limits; none beyond the platform's.
var limits = map[string]ratelimit.Policy{}

// Module is the Manufacturing module.
func Module() module.Module {
	return module.Module{
		Name:        moduleName,
		Permissions: permissions,
		Mount:       mount,
		// Composed whatever MODULES says, from what a disabled module's Deps
		// has too (M16).
		VariantReferences: newVariantHolder,
		LiveTopics:        liveTopics,
		// The erasure sweep needs the pool and the user directory, which a
		// worker-mode Deps has (erasure.go); the make-to-order worker the
		// sales order lines (mto.go).
		Workers: workers,
		// The handlers of Orders' confirmed and cancelled sales orders
		// (M44a), composed whatever MODULES says; the first does nothing
		// while the module is off.
		EventHandlers: eventHandlers,
		// What is being made for sales order lines (M46a), resolved from the
		// enabled modules only.
		Production: newProduction,
		// Orders' ungated read of the kits' components (manufacturing phase
		// 2 M145), resolved from the enabled modules only.
		KitStructure: newKitStructure,
	}
}

// bodyLimits raises the router's request-body cap for a check's photo — the
// file at most 10 MiB (M97) and room for the multipart framing around it, the
// margin Inventory's lot files leave; every other operation keeps the
// platform default (1 MiB).
var bodyLimits = map[string]int64{
	"uploadManufacturingCheckPhoto": maxPhotoRequestBytes,
}

// mount builds the module's handler. It refuses to mount without the product
// catalog or Inventory's stock: config makes manufacturing without products
// or inventory impossible (M6), and a nil contract would fail every BoM and
// every work center.
func mount(d module.Deps) (http.Handler, error) {
	if d.Products == nil {
		return nil, errors.New("manufacturing: no product catalog; manufacturing requires the products module")
	}
	if d.Stock == nil {
		return nil, errors.New("manufacturing: no stock; manufacturing requires the inventory module")
	}
	if err := checkClosedSchemas(d.Doc); err != nil {
		return nil, err
	}
	router := module.NewRouter(module.RouterOptions{
		Doc: d.Doc, Access: d.Access, Limiter: d.Limiter, Limits: limits, Catalog: d.Catalog,
		BodyLimits: bodyLimits,
	})
	srv := newServer(d)
	// A station's operator is held to the station's work centers before any
	// handler runs (manufacturing phase 2 M131).
	strict := gen.NewStrictHandlerWithOptions(srv, []gen.StrictMiddlewareFunc{srv.stationScope}, gen.StrictHTTPServerOptions{
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
