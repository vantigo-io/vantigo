package customers

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own
// file.
type server struct {
	deps  module.Deps
	brreg *brregClient
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d. The Brreg client is built
// once, here, from config and d.HTTPTransport/d.HTTPBackoff (nil in
// production, a fake/zero-delay in every test — brreg.go, customers
// inventory §5).
func newServer(d module.Deps) *server {
	return &server{deps: d, brreg: newBrregClient(d.Config.BrregBaseURL, d.Config.BrregTimeout, d.HTTPTransport, d.HTTPBackoff)}
}

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

// deref returns *s, or "" for a nil s.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
