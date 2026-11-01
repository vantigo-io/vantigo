package contracts

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// MaxAssistantGrantTTL is the longest an assistant grant may live. A run lasts
// minutes; a longer grant would only outlive the work it was issued for.
const MaxAssistantGrantTTL = time.Hour

// ErrNoSessionRequest is AssistantGrants.Issue's refusal when the context
// carries no request whose cookie resolves to a live session: a grant is
// bound to the session of the person who sent the message, so there is
// nothing to bind one to for a worker, an AsUser read or a request that is
// itself made through a grant.
var ErrNoSessionRequest = errors.New("assistant grants: no session request in the context")

// ErrInvalidGrantTTL is Issue's refusal of a lifetime that is not positive or
// exceeds MaxAssistantGrantTTL.
var ErrInvalidGrantTTL = errors.New("assistant grants: the time to live must be positive and at most one hour")

// AssistantGrants is the assistant's door into identity (the Assistant design
// S3): the one place a grant — the assistant principal — is issued and ended.
// Identity provides it, a single-provider slot (Module.AssistantGrants →
// Deps.AssistantGrants), and the assistant module is its only caller.
//
// A grant carries no secret. It is a row bound to the session of the person
// who sent the message, the conversation and the run; the dispatcher puts its
// id on the in-process request's context (WithAssistantGrant), where no
// network input can reach, and Access.Check answers the session's own
// principal, with Principal.Via set, for as long as the grant is current and
// the session valid.
type AssistantGrants interface {
	// Issue binds a new grant to the session of the request in ctx — the
	// person's own message request, resolved through its session cookie —
	// for conversationID and runID, living ttl (positive, at most
	// MaxAssistantGrantTTL), and answers its id. It is ErrNoSessionRequest
	// without such a request.
	Issue(ctx context.Context, conversationID, runID uuid.UUID, ttl time.Duration) (uuid.UUID, error)
	// Revoke ends one grant at once. A grant already ended, or unknown, is not
	// an error.
	Revoke(ctx context.Context, grantID uuid.UUID) error
	// RevokeForUser ends every grant of a user at once.
	RevokeForUser(ctx context.Context, userID uuid.UUID) error
}

// assistantGrantContextKey is unexported so only this package can set or read
// the grant: a context value cannot be supplied by a network request.
type assistantGrantContextKey struct{}

// WithAssistantGrant attaches the id of an assistant grant to ctx. The
// operations dispatcher is its only caller.
func WithAssistantGrant(ctx context.Context, grantID uuid.UUID) context.Context {
	return context.WithValue(ctx, assistantGrantContextKey{}, grantID)
}

// AssistantGrantFrom retrieves the grant WithAssistantGrant attached to ctx.
func AssistantGrantFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(assistantGrantContextKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}
