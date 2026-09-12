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
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	return &server{deps: d}
}

// requestKey is the context key withRequest stores the underlying
// *http.Request under. The generated strict handlers only pass a context to
// a business method, but a couple of this module's handlers need a second,
// narrower permission check the router's own x-vantigo-access rule does not
// cover — whether the caller may see legal-identity data (inventory §6,
// "Business view exposed twice") — which means calling deps.Access.Check a
// second time, and Check needs the request. Mirrors
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

// hasPermission reports whether the signed-in caller holds key, evaluated
// the same way module.Router evaluates x-vantigo-access. Any failure,
// infrastructure errors included, reads as false: a response-shaping check
// must never turn into a request failure.
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
