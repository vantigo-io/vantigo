package products

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/products/gen"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own file;
// unimplemented.go holds the stubs of every area not yet built, so the build
// itself proves the interface is complete.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	return &server{deps: d}
}

// pricingManage and pricingView are the two permissions the conditional
// gate additionally requires (see hasPermission's doc comment).
const (
	pricingView   = "products:pricing-view"
	pricingManage = "products:pricing-manage"
)

// hasPermission reports whether the signed-in caller holds key, evaluated
// the same way module.Router evaluates x-vantigo-access. Any failure,
// infrastructure errors included, reads as false. This is one of the
// project's two sanctioned handler-side permission gates (Global
// Constraints, "Contract-driven access": handlers never re-check what the
// router already checked, with exactly two exceptions, both a permission
// conditional on request-body content the router cannot see). The other is
// customers' legal-identity gate (internal/customers/server.go's
// legalIdentityManage), which has the same shape and fails closed the same
// way; neither is "the one". postProducts and postProductsByIdVariants
// additionally require pricing-view AND pricing-manage when the submitted payload
// carries pricing data (a non-null standardCost or a non-empty prices
// array on any variant), because whether pricing is involved at all depends
// on the request body, which module.Router's static x-vantigo-access can
// never see (products inventory §7 oddity 1,
// CreateProductEndpoint.cs:30-37, AddProductVariantEndpoint.cs:24-31).
// putProductsByIdVariantsByVariantId's pricing-manage requirement, by
// contrast, is unconditional and is already declared in the contract's
// x-vantigo-access, so it needs no handler-side check at all.
func (s *server) hasPermission(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}
