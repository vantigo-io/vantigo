package customers

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own
// file.
type server struct {
	deps         module.Deps
	brreg        *brregClient
	peppolLookup func(ctx context.Context, participant string) (peppol.Result, error)
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d. The Brreg client is built
// once, here, from config and d.HTTPTransport/d.HTTPBackoff (nil in
// production, a fake/zero-delay in every test — brreg.go, customers
// inventory §5).
//
// s.peppolLookup is resolved once here too (peppol lookup design D3, D5):
// nil whenever Config.PeppolLookupEnabled is false — the seam is never even
// read in that case, so a test that sets both a fake Deps.PeppolLookup and
// PEPPOL_LOOKUP_ENABLED=0 can assert the fake was never called — otherwise
// d.PeppolLookup itself when a test harness set one (modtest.WithPeppolLookup),
// or, in production, a real *peppol.Client's Lookup built from
// Config.PeppolSMLZone/PeppolDNSServer/PeppolTimeout and d.HTTPTransport,
// the same seam Brreg's own client dials through.
func newServer(d module.Deps) *server {
	var lookup func(ctx context.Context, participant string) (peppol.Result, error)
	if d.Config.PeppolLookupEnabled {
		lookup = d.PeppolLookup
		if lookup == nil {
			lookup = peppol.NewClient(peppol.Options{
				Zone:          d.Config.PeppolSMLZone,
				DNSServers:    peppolDNSServers(d.Config.PeppolDNSServer),
				Timeout:       d.Config.PeppolTimeout,
				HTTPTransport: d.HTTPTransport,
			}).Lookup
		}
	}
	return &server{
		deps:         d,
		brreg:        newBrregClient(d.Config.BrregBaseURL, d.Config.BrregTimeout, d.HTTPTransport, d.HTTPBackoff),
		peppolLookup: lookup,
	}
}

// peppolDNSServers is peppol.Options.DNSServers from Config.PeppolDNSServer:
// nil (meaning the server's own name servers) when it is unset, else the
// one configured resolver.
func peppolDNSServers(server string) []string {
	if server == "" {
		return nil
	}
	return []string{server}
}

// customersView is the permission that admits the module's read operations
// (module.Router enforces it on GetCustomers/GetCustomer). The handlers only
// ever ask about it themselves for the duplicate-identity conflict body
// (duplicates.go): none of the three write paths that can raise that conflict
// requires customers:view, so whether the 409 may name the other customer has
// to be asked separately. Like legalIdentityView this is response-shaping, not
// a gate — it never answers 403.
const customersView = "customers:view"

// legalIdentityView is the permission that decides whether a customer
// response's legal-identity sub-object (or the stats endpoint's
// identity-derived figures) is included. It is never what admits the
// request itself — that is customers:view, enforced by module.Router before
// the handler runs — so this is a response-shaping check, not a second
// access gate, and never answers 403 (inventory §6).
const legalIdentityView = "customers:legal-identity-view"

// legalIdentityManage is the permission PostCustomers/PutCustomersById
// additionally require when the request body carries an identity
// (CreateCustomerEndpoint.cs:38-42, UpdateCustomerEndpoint.cs:34-38,
// inventory §1.1/§1.4). Unlike legalIdentityView, this one does gate the
// request: module.Router's x-vantigo-access for both operations is the flat
// create (or update+view), since whether identity is required at all
// depends on the request body, which the router never inspects — so this is
// one of exactly two handler-side Access.Check gates that can themselves
// answer 403. The other is Task 11's conditional pricing permission
// (internal/products/server.go's hasPermission); the two have the same
// shape — a permission conditional on payload content the router cannot
// see — and the plan's Global Constraints names both, so neither comment
// should claim to be the only one.
const legalIdentityManage = "customers:legal-identity-manage"

// billingManage is the permission the CSV import asks for itself (customers
// import/export design D3): a file carrying the billing profile's columns is
// refused whole without it, because the importer may never write more than
// its sender could by hand, and the billing profile's own PUT wants this key.
// module.Router enforces it everywhere else, from x-vantigo-access.
const billingManage = "customers:billing-manage"

// contactsView and associationsView are the two permissions GetCustomers
// checks together (customers foundation design D4) to decide whether
// search may reach into a linked contact's name and email: both are
// required, the same "never an oracle for data the response would
// withhold" reasoning as legalIdentityView, because a caller who cannot
// list a customer's contacts or associations (Task 7's own gates) must not
// be able to discover them by searching for one instead.
const (
	contactsView     = "customers:contacts-view"
	associationsView = "customers:associations-view"
)

// timelineView is the permission that decides whether /stats/attention's
// follow-up items are included (follow-ups design D2). Like legalIdentityView
// it is response-shaping, never a gate — that endpoint admits a caller on
// customers:view and never answers 403 here — and it exists for the same
// reason: a follow-up item names a timeline entry, customers:timeline-view is
// sensitive (module.go), and the dashboard must not be a way around the
// timeline's own door. Every operation that ANSWERS timeline data is gated on
// this key by module.Router instead, from x-vantigo-access.
const timelineView = "customers:timeline-view"

// hasPermission reports whether the signed-in caller holds key, evaluated
// the same way module.Router evaluates x-vantigo-access. Any failure,
// infrastructure errors included, reads as false. hasPermission itself never
// writes a response either way — it only answers the question — but its two
// callers use "false" for opposite purposes: legalIdentityView's
// response-shaping callers treat it as "omit the data", never a request
// failure; legalIdentityManage's gating callers (PostCustomers,
// PutCustomersById) treat it as "deny", which does answer 403.
func (s *server) hasPermission(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// hasPermissions is hasPermission for an AND-gate of several keys, asked as one
// access check rather than one per key (contracts.HasPermissions). GetCustomers
// is its caller: contactsView and associationsView are only ever wanted
// together, and a check is a session lookup plus a permission query, so asking
// for both at once halves what the list endpoint pays for the answer.
func (s *server) hasPermissions(ctx context.Context, keys ...string) bool {
	return contracts.HasPermissions(ctx, s.deps.Access, keys...)
}

// deref returns *s, or "" for a nil s.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
