// Package customers is the customers module: customers, their legal
// identities, contacts, customer-contact associations, the customer timeline
// and the business-registry lookup, serving openapi/customers.yaml under
// /api/v1/customers/. It also owns the customer data every other module reads,
// which it publishes as the contracts.CustomerDirectory that Compose hands to
// them.
package customers

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog
// (AZ/CustomerPermissionCatalogContributor.cs:9-49, inventory §6). The .NET
// contributor never passed Delegable, so every key takes the descriptor's
// default of true; only Sensitive is ever overridden, and only view, create
// and update are not sensitive. customers:billing-manage (invoice-ready
// customer design D1, D4) is the one key with no .NET ancestor: writing a
// customer's billing profile — payment terms and delivery channel decide
// when and how money arrives — is its own sensitive permission, deliberately
// narrower than customers:update.
var permissions = []contracts.Permission{
	{Key: "customers:view", Display: "View customers", Description: "View customer names, identifiers, and a sanitized activity summary.", Category: "Customers", Sensitive: false, Delegable: true},
	{Key: "customers:create", Display: "Create customers", Description: "Create customers without legal identity data.", Category: "Customers", Sensitive: false, Delegable: true},
	{Key: "customers:update", Display: "Update customers", Description: "Update customer names and basic non-sensitive details.", Category: "Customers", Sensitive: false, Delegable: true},
	{Key: "customers:delete", Display: "Delete customers", Description: "Delete customers and their customer-owned records.", Category: "Customers", Sensitive: true, Delegable: true},
	{Key: "customers:legal-identity-view", Display: "View legal identities", Description: "View customer legal identity and registry attribution.", Category: "Legal identity", Sensitive: true, Delegable: true},
	{Key: "customers:legal-identity-manage", Display: "Manage legal identities", Description: "Add, replace, or remove customer legal identity data.", Category: "Legal identity", Sensitive: true, Delegable: true},
	{Key: "customers:contacts-view", Display: "View contacts", Description: "View contact names and contact details.", Category: "Contacts", Sensitive: true, Delegable: true},
	{Key: "customers:contacts-manage", Display: "Manage contacts", Description: "Create, update, and delete contacts.", Category: "Contacts", Sensitive: true, Delegable: true},
	{Key: "customers:associations-view", Display: "View customer associations", Description: "View links between customers and contacts.", Category: "Associations", Sensitive: true, Delegable: true},
	{Key: "customers:associations-manage", Display: "Manage customer associations", Description: "Create, update, and remove customer-contact links.", Category: "Associations", Sensitive: true, Delegable: true},
	{Key: "customers:timeline-view", Display: "View customer timeline", Description: "View customer timeline entries, notes, provenance, and revisions.", Category: "Timeline", Sensitive: true, Delegable: true},
	{Key: "customers:timeline-manage", Display: "Manage customer timeline", Description: "Create, update, and delete customer timeline entries.", Category: "Timeline", Sensitive: true, Delegable: true},
	{Key: "customers:lookup-view", Display: "Use registry lookup", Description: "Search the external business registry for legal identities.", Category: "Lookup", Sensitive: true, Delegable: true},
	{Key: "customers:billing-manage", Display: "Manage billing profiles", Description: "Set a customer's payment terms, invoice delivery and billing addresses for documents.", Category: "Billing", Sensitive: true, Delegable: true},
}

// limits maps each rate-limited operationId to its policy. It is empty and
// stays empty: no Customers endpoint calls RequireRateLimiting (inventory §6),
// unlike identity's nine authentication policies.
var limits = map[string]ratelimit.Policy{}

// Module is customers as a platform module: its contract mounted under
// /api/v1/customers/, its fourteen permissions in the composed catalog, and
// the customer directory it publishes to every other module.
func Module() module.Module {
	return module.Module{
		Name:        "customers",
		Permissions: permissions,
		Mount:       mount,
		Directory:   newDirectory,
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
