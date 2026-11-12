// Package inventory is the Inventory module (nb Lager): the stock of the
// catalog's goods in warehouses and stores, each site a tree of locations, and
// the ledger of movements that changes it (inventory design,
// docs/superpowers/specs/2026-10-27-inventory-design.md, decisions I1–I34).
// It reads and locks Products' variants through contracts.ProductCatalog and
// requires the products module.
package inventory

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// The nine permission keys (§5; the phase 2 design's I63 adds
// transfers-manage, manufacturing phase 2's M68 expired-override and
// lots-manage). None is sensitive and all are delegable.
// Every stock read and every write also needs both views, because they show
// location codes and their forms pick locations; so a stock role is
// locations-view + stock-view (+ the write keys), and locations-view alone
// gives the structure without quantities.
const (
	permLocationsView   = "inventory:locations-view"
	permLocationsManage = "inventory:locations-manage"
	permStockView       = "inventory:stock-view"
	permStockAdjust     = "inventory:stock-adjust"
	permReceiptsManage  = "inventory:receipts-manage"
	permCountsManage    = "inventory:counts-manage"
	permTransfersManage = "inventory:transfers-manage"
	// permExpiredOverride is manufacturing phase 2's M68: issuing or
	// consuming an expired lot the person chose. Inventory never checks it
	// inside a ledger call; the caller reads it on the pool (M62).
	permExpiredOverride = "inventory:expired-override"
	// permLotsManage is manufacturing phase 2's M68: editing a lot, its
	// files, and putting and releasing Inventory's own holds on it.
	permLotsManage = "inventory:lots-manage"
)

var permissions = []contracts.Permission{
	{
		Key: permLocationsView, Display: "View locations",
		Description: "See the sites and their locations, and find a location by its code, without quantities.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
	{
		Key: permLocationsManage, Display: "Manage locations",
		Description: "Create, change, archive and delete sites and locations, one by one or in bulk.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
	{
		Key: permStockView, Display: "View stock",
		Description: "See what lies where: the quantities on hand, a product's Stock card, a location's contents and the movements.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
	{
		Key: permStockAdjust, Display: "Adjust stock",
		Description: "Post adjustments and moves of stock between the locations of a site.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
	{
		Key: permReceiptsManage, Display: "Receive goods",
		Description: "Post goods receipts, putting delivered goods on the locations of a site.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
	{
		Key: permCountsManage, Display: "Count stock",
		Description: "Open, count, post and cancel stock counts.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
	{
		Key: permTransfersManage, Display: "Transfer stock",
		Description: "Create, send, receive and cancel transfers of stock between sites, and close a transfer's difference.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
	{
		Key: permExpiredOverride, Display: "Use expired lots",
		Description: "Issue or consume a lot past its expiry date, when the lot is chosen by hand on a delivery or a production.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
	{
		Key: permLotsManage, Display: "Manage lots",
		Description: "Change a lot's dates, supplier lot and note, add and remove its files, and put and release its holds on the lot page.",
		Category:    "Inventory", Sensitive: false, Delegable: true,
	},
}

// limits are the module's rate limits; none beyond the platform's.
var limits = map[string]ratelimit.Policy{}

// Module is the Inventory module.
func Module() module.Module {
	return module.Module{
		Name:              "inventory",
		Permissions:       permissions,
		Mount:             mount,
		VariantReferences: newVariantHolder,
		Stock:             newStock,
		StockLedger:       newLedger,
		// LiveTopics is the lots list's and a lot's page (manufacturing phase
		// 2 §17, live.go); Workers the deletion ledger of the lots' files
		// (M54, lot_attachments.go).
		LiveTopics: liveTopics,
		Workers:    workers,
	}
}

// mount builds the module's handler. It refuses to mount without the
// product catalog: config makes "inventory without products" impossible, and
// a nil catalog would fail every posting.
func mount(d module.Deps) (http.Handler, error) {
	if d.Products == nil {
		return nil, errors.New("inventory: no product catalog; the inventory module requires the products module")
	}
	if err := checkClosedSchemas(d.Doc); err != nil {
		return nil, err
	}
	router := module.NewRouter(module.RouterOptions{
		Doc: d.Doc, Access: d.Access, Limiter: d.Limiter, Limits: limits, Catalog: d.Catalog,
		BodyLimits: map[string]int64{
			"importInventoryOpeningBalances": maxImportRequestBytes, "importInventoryPlacements": maxImportRequestBytes,
			"postInventoryLotAttachment": maxLotAttachmentRequestBytes,
		},
	})
	strict := gen.NewStrictHandlerWithOptions(newServer(d), nil, gen.StrictHTTPServerOptions{
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
