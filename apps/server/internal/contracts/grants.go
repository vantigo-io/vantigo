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

// AgentRun is what the operations dispatcher needs to know about the run an
// agent grant stands for: the agent and run the gate applies its levels for,
// and the customer scope (virtual employees design V9b) the dispatcher enforces
// on an operation that declares x-vantigo-customer-scope. It carries what the
// grant's Via carries, because identity records the grant and the dispatcher
// has no other way to read it before a call; the agents module sets both from
// the same values.
type AgentRun struct {
	AgentID uuid.UUID
	RunID   uuid.UUID
	// CustomerID is the run's customer, nil for a run with none: a scoped
	// operation is then refused.
	CustomerID *int32
}

type agentRunContextKey struct{}

// WithAgentRun attaches the run an agent grant stands for to ctx. Like
// WithGrant it is a context value and never a header, so no request can set it.
func WithAgentRun(ctx context.Context, run AgentRun) context.Context {
	return context.WithValue(ctx, agentRunContextKey{}, run)
}

// AgentRunFrom retrieves the run WithAgentRun attached to ctx.
func AgentRunFrom(ctx context.Context) (AgentRun, bool) {
	run, ok := ctx.Value(agentRunContextKey{}).(AgentRun)
	return run, ok
}
