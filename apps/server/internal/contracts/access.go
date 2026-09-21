package contracts

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

// Principal is the authenticated caller of one request.
type Principal struct {
	UserID      uuid.UUID
	SessionID   uuid.UUID
	Roles       []string
	MFAVerified bool
	SCIM        bool // authenticated by the SCIM bearer token; no user
}

// principalContextKey is unexported so only this package can set or read the
// context value.
type principalContextKey struct{}

// WithPrincipal attaches p to ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFrom retrieves the Principal WithPrincipal attached to ctx.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}

// requestContextKey and responseWriterContextKey are unexported so only
// this package can set or read the context values.
type (
	requestContextKey        struct{}
	responseWriterContextKey struct{}
)

// WithRequest attaches r and its response writer to ctx. The router does
// this for every operation it dispatches, so a generated strict handler —
// which is handed only a context — can still reach the request it is
// answering: the client address, the cookies, the trace id, or a second
// Access.Check on a permission the router could not evaluate statically.
func WithRequest(ctx context.Context, w http.ResponseWriter, r *http.Request) context.Context {
	ctx = context.WithValue(ctx, requestContextKey{}, r)
	return context.WithValue(ctx, responseWriterContextKey{}, w)
}

// RequestFrom retrieves the request WithRequest attached to ctx.
func RequestFrom(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(requestContextKey{}).(*http.Request)
	return r, ok
}

// ResponseWriterFrom retrieves the response writer WithRequest attached to
// ctx.
func ResponseWriterFrom(ctx context.Context) (http.ResponseWriter, bool) {
	w, ok := ctx.Value(responseWriterContextKey{}).(http.ResponseWriter)
	return w, ok
}

// HasPermission reports whether the caller of the request in ctx holds key,
// evaluated by a the way the router evaluates an operation's
// x-vantigo-access permission rule. Any failure — no request in ctx,
// infrastructure errors included — reads as false, so a gate built on it
// fails closed. It is for the handler-side checks the router cannot make:
// a permission conditional on request-body content.
func HasPermission(ctx context.Context, a Access, key string) bool {
	return HasPermissions(ctx, a, key)
}

// HasPermissions is HasPermission for several keys at once: it reports whether
// the caller holds every one of them. The keys travel in a single
// Rule{Kind: RulePermission, Names: keys}, which Access evaluates as an AND
// (identity/access.go's permitted refuses on the first key the caller does not
// hold) — one Check, not one per key, because a Check is a session lookup plus
// a permission query, and a handler gating on two permissions together should
// pay for one round of that rather than two.
//
// It fails closed exactly as HasPermission does. Naming no key at all is false
// too, and without asking Access: an empty Names list satisfies the AND
// vacuously, which is never the gate a caller passing no keys meant to open.
func HasPermissions(ctx context.Context, a Access, keys ...string) bool {
	if len(keys) == 0 {
		return false
	}
	r, ok := RequestFrom(ctx)
	if !ok {
		return false
	}
	_, err := a.Check(r, Rule{Kind: RulePermission, Names: keys})
	return err == nil
}

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
)

// Access authenticates a request and evaluates an access rule. Identity
// implements it; every module's router uses it.
type Access interface {
	// Check returns the caller if rule is satisfied. For RuleAnonymous it
	// never fails on credentials: it returns the caller when a valid session
	// exists, else a zero Principal. It returns ErrUnauthenticated or
	// ErrForbidden (possibly wrapped) when the rule is not met, and any other
	// error for infrastructure failures.
	Check(r *http.Request, rule Rule) (Principal, error)
	// Reject writes the response for ErrUnauthenticated/ErrForbidden under rule.
	Reject(w http.ResponseWriter, r *http.Request, rule Rule, err error)
}
