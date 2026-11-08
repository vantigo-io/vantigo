package contracts

import (
	"context"

	"github.com/google/uuid"
)

// The kinds of grant a request's context can carry: the principal of an
// in-process call made for the assistant (AssistantGrants) or for an agent
// (AgentGrants).
const (
	GrantAssistant = "assistant"
	GrantAgent     = "agent"
)

// GrantRef names one grant: its kind, which decides the table Access.Check
// reads it from, and its id.
type GrantRef struct {
	Kind string
	ID   uuid.UUID
}

// grantContextKey is unexported so only this package can set or read the
// grant: a context value cannot be supplied by a network request.
type grantContextKey struct{}

// WithGrant attaches the grant ref names to ctx. The operations dispatcher is
// its only caller, for either kind.
func WithGrant(ctx context.Context, ref GrantRef) context.Context {
	return context.WithValue(ctx, grantContextKey{}, ref)
}

// GrantFrom retrieves the grant WithGrant attached to ctx. A nil id is no
// grant.
func GrantFrom(ctx context.Context) (GrantRef, bool) {
	ref, ok := ctx.Value(grantContextKey{}).(GrantRef)
	return ref, ok && ref.ID != uuid.Nil
}
