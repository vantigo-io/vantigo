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
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// permissions is the module's permission catalog
// (AZ/EnergyPermissionCatalogContributor.cs, energy inventory §6). Every
// entry shares one category, "Energy" — unlike products and customers, the
// .NET contributor never splits energy's permissions across categories —
// and every key is explicitly Delegable=true, Sensitive=false (the .NET
// default, but set on every descriptor there rather than omitted).
// energy:import (energy import design D5) is the one exception to
// Sensitive=false and the one key with no .NET ancestor: one import writes
// thousands of metering points and supply periods under one name — and it is
// only the door: the mapping is still checked against the keys each write
// needs by hand.
// The entries are written in the alphabetical-by-key order
// TestModule_DeclaresItsPermissionCatalog pins (ported from
// EnergyPermissionCatalogTests.cs:14-23's stable-ordering assertion):
// "metering-points-*" sorts before "meters-*" because 'i' < 's' at the
// first differing character ("meter" + "i" vs "meter" + "s"), which is easy
// to get backwards by ear.
var permissions = []contracts.Permission{
	{Key: "energy:consumption-manage", Display: "Manage energy consumption", Description: "Add and replace manual energy consumption intervals.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:consumption-view", Display: "View energy consumption", Description: "View energy consumption intervals and aggregates.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:import", Display: "Import energy data", Description: "Import metering points and consumption from a file; each thing written needs the permission it needs by hand.", Category: "Energy", Sensitive: true, Delegable: true},
	{Key: "energy:metering-points-manage", Display: "Manage energy metering points", Description: "Create and update energy metering points.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:metering-points-view", Display: "View energy metering points", Description: "View energy metering point details and listings.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:meters-manage", Display: "Manage energy meters", Description: "Replace meters installed at energy metering points.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:meters-view", Display: "View energy meters", Description: "View energy meter history for metering points.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:supply-periods-manage", Display: "Manage energy supply periods", Description: "Create, switch, end, and cancel energy supply periods.", Category: "Energy", Sensitive: false, Delegable: true},
	{Key: "energy:supply-periods-view", Display: "View energy supply periods", Description: "View energy supply periods for metering points.", Category: "Energy", Sensitive: false, Delegable: true},
}

// limits maps each rate-limited operationId to its policy. No Energy endpoint
// called RequireRateLimiting (energy inventory §6); the import's upload (energy
// import design D6) takes the customers import's limit, since it stores what
// it is sent.
var limits = map[string]ratelimit.Policy{
	"postEnergyImports": policyImportUpload,
}

// bodyLimits raises the router's request-body cap for the import's upload
// (imports.go); every other operation keeps the platform default (1 MiB).
var bodyLimits = map[string]int64{
	"postEnergyImports": maxImportUploadRequestBytes,
}

// Module is energy as a platform module: its contract mounted under
// /api/v1/energy/, its nine permissions in the composed catalog and its import
// worker. Energy
// only ever reads another module's data through contracts.CustomerDirectory
// (internal/contracts) and never provides one of its own — energy inventory
// §1.2 calls it "a leaf module for cross-module purposes" — so Directory is
// left nil, the same as products. The one thing it does provide is the
// contracts.CustomerReferenceHolder a customer merge re-points its supply
// periods through (customers merge design D1) — a write the customers module
// drives, not a read anyone makes.
func Module() module.Module {
	return module.Module{
		Name:                 "energy",
		Permissions:          permissions,
		Mount:                mount,
		CustomerReferences:   newCustomerReferenceHolder,
		CustomerPersonalData: newCustomerPersonalData,
		Workers:              workers,
	}
}

// workers is this module's background work: the import worker (energy import
// design D6), which checks, runs and attaches the metering-point files people
// upload. It is registered only when its switch is on, as the customers
// import worker is: off means this process never hands it to the runner, and
// an import queues and waits. TestModule_ContributesItsWorkers asserts the set.
func workers(d module.Deps) []worker.Worker {
	if d.Config == nil || !d.Config.EnergyImportEnabled {
		return nil
	}
	return []worker.Worker{NewImportWorker(d)}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. Every body is capped at the router's default except the
// import's upload (bodyLimits). It fails when the router reports a problem: an
// operation never registered, a rule that does not parse, or a permission
// missing from the catalog.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:        d.Doc,
		Access:     d.Access,
		Limiter:    d.Limiter,
		Limits:     limits,
		Catalog:    d.Catalog,
		BodyLimits: bodyLimits,
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
