// Package energy is the energy module: metering points, the meters
// installed at them, supply periods, and consumption intervals, serving
// openapi/energy.yaml under /api/v1/energy/.
package energy

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog
// (AZ/EnergyPermissionCatalogContributor.cs, energy inventory §6). Every
// entry shares one category, "Energy" — unlike products and customers, the
// .NET contributor never splits energy's permissions across categories —
// and every key is explicitly Delegable=true, Sensitive=false (the .NET
// default, but set on every descriptor there rather than omitted).
// The entries are written in the alphabetical-by-key order
// TestModule_DeclaresItsPermissionCatalog pins (ported from
// EnergyPermissionCatalogTests.cs:14-23's stable-ordering assertion):
// "metering-points-*" sorts before "meters-*" because 'i' < 's' at the
// first differing character ("meter" + "i" vs "meter" + "s"), which is easy
// to get backwards by ear.
var permissions = []contracts.Permission{
	{Key: "energy:consumption-manage", Display: "Manage energy consumption", Description: "Add and replace manual energy consumption intervals.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:consumption-view", Display: "View energy consumption", Description: "View energy consumption intervals and aggregates.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:metering-points-manage", Display: "Manage energy metering points", Description: "Create and update energy metering points.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:metering-points-view", Display: "View energy metering points", Description: "View energy metering point details and listings.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:meters-manage", Display: "Manage energy meters", Description: "Replace meters installed at energy metering points.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:meters-view", Display: "View energy meters", Description: "View energy meter history for metering points.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:supply-periods-manage", Display: "Manage energy supply periods", Description: "Create, switch, end, and cancel energy supply periods.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:supply-periods-view", Display: "View energy supply periods", Description: "View energy supply periods for metering points.", Category: "Energy", Sensitive: false, Delegable: true},
}

// limits maps each rate-limited operationId to its policy. It is empty and
// stays empty: no Energy endpoint calls RequireRateLimiting (energy
// inventory §6), the same as products.
var limits = map[string]ratelimit.Policy{}

// Module is energy as a platform module: its contract mounted under
// /api/v1/energy/ and its eight permissions in the composed catalog. Energy
// only ever reads another module's data through contracts.CustomerDirectory
// (internal/contracts) and never provides one of its own — energy inventory
// §1.2 calls it "a leaf module for cross-module purposes" — so Directory is
// left nil, the same as products. The one thing it does provide is the
// contracts.CustomerReferenceHolder a customer merge re-points its supply
// periods through (customers merge design D1) — a write the customers module
// drives, not a read anyone makes.
func Module() module.Module {
	return module.Module{
		Name:               "energy",
		Permissions:        permissions,
		Mount:              mount,
		CustomerReferences: newCustomerReferenceHolder,
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
