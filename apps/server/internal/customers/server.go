package customers

import (
	"context"
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own file;
// unimplemented.go holds the stubs of every area not yet built, so the build
// itself proves the interface is complete.
type server struct {
	deps  module.Deps
	brreg *brregClient
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d. The Brreg client is built
// once, here, from config and d.HTTPTransport (nil in production, a fake in
// every test — brreg.go, customers inventory §5).
func newServer(d module.Deps) *server {
	return &server{deps: d, brreg: newBrregClient(d.Config.BrregBaseURL, d.Config.BrregTimeout, d.HTTPTransport)}
}

// requestKey is the context key withRequest stores the underlying
// *http.Request under. The generated strict handlers only pass a context to
// a business method, but some of this module's handlers need a permission
// check beyond what the router's own x-vantigo-access rule covers —
// whether the caller may see legal-identity data (inventory §6, "Business
// view exposed twice"), and whether it may write it
// (legal-identity-manage, inventory §1.1/§1.4) — which means calling
// deps.Access.Check a second time, and Check needs the request. Mirrors
// internal/identity/server.go's withRequest/requestFrom, which depguard
// forbids importing directly.
type requestKey struct{}

var errNoRequest = errors.New("customers: no request in the handler context")

// withRequest is the strict middleware that hands every operation its
// *http.Request via requestFrom.
func withRequest(f gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		return f(context.WithValue(ctx, requestKey{}, r), w, r, request)
	}
}

// requestFrom returns the request withRequest stored in ctx.
func requestFrom(ctx context.Context) (*http.Request, error) {
	r, ok := ctx.Value(requestKey{}).(*http.Request)
	if !ok {
		return nil, errNoRequest
	}
	return r, nil
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
// the one additional Access.Check a handler makes that can itself answer
// 403, the same shape as Task 11's conditional pricing permission.
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
	r, err := requestFrom(ctx)
	if err != nil {
		return false
	}
	_, err = s.deps.Access.Check(r, contracts.Rule{Kind: contracts.RulePermission, Names: []string{key}})
	return err == nil
}

// ptr returns a pointer to a copy of v, for the optional fields of a
// generated response type.
func ptr[T any](v T) *T { return &v }

// deref returns *s, or "" for a nil s.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
