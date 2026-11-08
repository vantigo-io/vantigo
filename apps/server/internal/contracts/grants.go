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

// AgentRun is what an agent grant stands for, read from the grant itself
// (AgentGrants.Describe): the agent user, the agent and run, the run's customer
// scope (virtual employees design V9b) and the reviewer whose approval it
// carries. The operations dispatcher resolves it from the grant on the context,
// so the grant is the one source of the run's customer.
type AgentRun struct {
	UserID  uuid.UUID
	AgentID uuid.UUID
	RunID   uuid.UUID
	// CustomerID is the run's customer, nil for a run with none: a scoped
	// operation is then refused.
	CustomerID *int32
	ApprovedBy *uuid.UUID
}
