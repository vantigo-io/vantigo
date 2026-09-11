package identity

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, identity's contract
// operations. Each area implements its operations as methods in its own
// file; every operation is implemented, so the build itself proves the
// interface is complete.
type server struct {
	access *Access
	deps   module.Deps
	q      *store.Queries
	// bootstrapSecret is what POST /bootstrap accepts, resolved once when
	// the module mounts (resolveBootstrapSecret); "" accepts nothing.
	bootstrapSecret string
	// dummyPasswordHash is what a password is verified against when the
	// email names no account, or one without a password (checkPassword).
	dummyPasswordHash string
	// relyingParty verifies passkey ceremonies; nil while APP_URL's host
	// cannot be a WebAuthn RP ID, and then every ceremony is refused.
	relyingParty *webauthn.WebAuthn
	// maintenance is the 20 s cache GetIdentitySystemStatus reads the
	// system_settings singleton through; see system.go.
	maintenance maintenanceCache
	// oidc is the workforce OIDC relying party; nil while OIDC is off, and
	// then the OIDC operations answer 404 or a failure redirect.
	oidc *oidcRelyingParty
}

var _ gen.StrictServerInterface = (*server)(nil)

// dummyPassword is the password dummyPasswordHash hashes. Nothing ever
// signs in with it: checkPassword never counts a dummy verification as a
// match.
const dummyPassword = "vantigo-no-such-account"

// newServer builds identity's operations over a and d. It resolves the
// bootstrap secret, and computes the dummy password hash up front so that
// no request, the first unknown-email sign-in included, pays for it. It
// builds the passkey relying party once; an APP_URL host that cannot be an
// RP ID (an IP address) leaves passkeys unavailable, with a warning, rather
// than the installation unable to start.
func newServer(a *Access, d module.Deps) (*server, error) {
	dummy, err := hashPassword(dummyPassword)
	if err != nil {
		return nil, err
	}
	rp, err := newRelyingParty(d.Config)
	if err != nil {
		d.Logger.Warn("passkeys unavailable: APP_URL's host cannot be a WebAuthn relying party ID",
			"app_host", d.Config.AppHostname, "error", err.Error())
		rp = nil
	}
	oidcRP, err := newOIDCRelyingParty(d.Config, a.oidcHTTPClient, d.Clock)
	if err != nil {
		return nil, err
	}
	return &server{
		access:            a,
		deps:              d,
		q:                 store.New(d.Pool),
		bootstrapSecret:   resolveBootstrapSecret(d.Config, d.Logger),
		dummyPasswordHash: dummy,
		relyingParty:      rp,
		oidc:              oidcRP,
	}, nil
}

// requestKey and responseWriterKey are the context keys withRequest stores
// the request and its response writer under.
type (
	requestKey        struct{}
	responseWriterKey struct{}
)

// withRequest is the strict middleware that hands every operation its
// *http.Request and response writer. The generated strict handlers pass
// only a context: sign-in needs the client address, the user agent, the
// cookies and the trace id, and an OIDC step that ends in a server error
// still clears its cookie on the 500 (clearOnServerError).
func withRequest(f gen.StrictHandlerFunc, _ string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		ctx = context.WithValue(ctx, requestKey{}, r)
		ctx = context.WithValue(ctx, responseWriterKey{}, w)
		return f(ctx, w, r, request)
	}
}

// responseWriterFrom returns the response writer withRequest stored in ctx.
func responseWriterFrom(ctx context.Context) (http.ResponseWriter, bool) {
	w, ok := ctx.Value(responseWriterKey{}).(http.ResponseWriter)
	return w, ok
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
