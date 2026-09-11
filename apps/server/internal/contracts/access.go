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
