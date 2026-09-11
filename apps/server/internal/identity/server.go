package identity

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, identity's contract
// operations. Each area implements its operations as methods in its own
// file, and unimplemented.go stubs the rest, so the build proves the
// interface is complete.
type server struct {
	access *Access
	deps   module.Deps
	q      *store.Queries
	// bootstrapSecret is what POST /bootstrap accepts, resolved once when
	// the module mounts (resolveBootstrapSecret); "" accepts nothing.
	bootstrapSecret string
}

var _ gen.StrictServerInterface = (*server)(nil)

func newServer(a *Access, d module.Deps, bootstrapSecret string) *server {
	return &server{access: a, deps: d, q: store.New(d.Pool), bootstrapSecret: bootstrapSecret}
}

// requestKey is the context key withRequest stores the request under.
type requestKey struct{}

// withRequest is the strict middleware that hands every operation its
// *http.Request. The generated strict handlers pass only a context, and
// sign-in needs the client address, the user agent, the cookies and the
// trace id.
func withRequest(f gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		return f(context.WithValue(ctx, requestKey{}, r), w, r, request)
	}
}

var errNoRequest = errors.New("identity: no request in the handler context")

// requestFrom returns the request withRequest stored in ctx.
func requestFrom(ctx context.Context) (*http.Request, error) {
	r, ok := ctx.Value(requestKey{}).(*http.Request)
	if !ok {
		return nil, errNoRequest
	}
	return r, nil
}

var errNoCaller = errors.New("identity: no signed-in caller in the handler context")

// callerFrom returns the signed-in caller the router's access check admitted.
// Only operations whose rule demands a session may call it; there the
// check guarantees one, so its absence is a server error.
func callerFrom(ctx context.Context) (contracts.Principal, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.UserID == uuid.Nil || p.SessionID == uuid.Nil {
		return contracts.Principal{}, errNoCaller
	}
	return p, nil
}
