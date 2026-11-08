package contracts

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// UserKindPerson and UserKindAgent are UserEntry.Kind: a person signs in; an
// agent is an AI user that never does (virtual employees design V5).
const (
	UserKindPerson = "person"
	UserKindAgent  = "agent"
)

// AgentAddressDomain is the domain of every agent user's address,
// agent-<uuid>@agents.invalid. The .invalid top-level domain is reserved by
// RFC 2606 and never routes.
const AgentAddressDomain = "agents.invalid"

// AgentAccounts is the agents module's door into identity (virtual employees
// design V5): the one place a user of kind agent is made and changed. Identity
// provides it, a single-provider slot (Module.AgentAccounts →
// Deps.AgentAccounts), and the agents module is its only caller.
//
// An agent user has no password, no second factor and no session, and
// identity refuses it sign-in, a password reset, an invitation, an identity
// provider link, a SCIM mapping, the Owner and SystemAdmin roles, a delegation
// and the stewardship of a role. Its roles are given in identity's own roles
// screen, like a person's. It is never deleted, so an audit's references stay
// true: deleting an agent disables its user.
type AgentAccounts interface {
	// Create makes a user of kind agent named displayName (1 to 200
	// characters) with the address agent-<id>@agents.invalid, and answers its
	// id. It never sets pending_since, which would stop the user acting.
	Create(ctx context.Context, displayName string) (uuid.UUID, error)
	// Rename changes an agent's display name. It is ErrNotAgent for a user that
	// is not one.
	Rename(ctx context.Context, userID uuid.UUID, displayName string) error
	// SetAvatar sets an agent's own picture; contentType is "image/jpeg" or
	// "image/png". It is ErrNotAgent for a user that is not an agent.
	SetAvatar(ctx context.Context, userID uuid.UUID, contentType string, data []byte) error
	// Disable stops an agent acting and ends every grant it holds. A user that
	// is disabled already is not an error. It is ErrNotAgent for a user that is
	// not an agent.
	Disable(ctx context.Context, userID uuid.UUID) error
	// Enable lets a disabled agent act again. It is ErrNotAgent for a user that
	// is not an agent.
	Enable(ctx context.Context, userID uuid.UUID) error
}

// ErrNotAgent is AgentAccounts' and AgentGrants' refusal of a user that does
// not exist or is not of kind agent.
var ErrNotAgent = errors.New("agent accounts: the user is not an agent")

// ErrInvalidAgentName is AgentAccounts' refusal of a display name that is
// empty or longer than 200 characters.
var ErrInvalidAgentName = errors.New("agent accounts: the display name must be 1 to 200 characters")

// ErrInvalidAgentAvatar is SetAvatar's refusal of a picture that is not JPEG
// or PNG, or is empty.
var ErrInvalidAgentAvatar = errors.New("agent accounts: the picture must be a JPEG or PNG")

// MaxAgentGrantTTL is how long an agent grant lives: twice a run's ceiling
// (virtual employees design V14). It is never renewed; a reviewer's approval
// gets a new grant.
const MaxAgentGrantTTL = 10 * time.Minute

// IssueAgentGrant says whom an agent grant is for and what it is for.
type IssueAgentGrant struct {
	// AgentUserID is the agent's user, of kind agent and active.
	AgentUserID uuid.UUID
	// AgentID and RunID are the agents module's ids of the agent and the run,
	// which Principal.Via carries so a module's audit can say which run acted.
	AgentID uuid.UUID
	RunID   uuid.UUID
	// CustomerID is the run's customer scope (design V9b), nil for a run with
	// no customer.
	CustomerID *int32
	// ApprovedBy is the reviewer whose approval this grant carries (design
	// V20), nil for a run's own.
	ApprovedBy *uuid.UUID
}

// AgentGrants is the agents module's door into identity for the agent
// principal (virtual employees design V6): the one place an agent grant is
// issued and ended. Identity provides it, a single-provider slot
// (Module.AgentGrants → Deps.AgentGrants), and the agents module is its only
// caller.
//
// A grant carries no secret. It is a row for one run of one agent; the
// dispatcher puts its id on the in-process request's context (WithGrant), where
// no network input can reach, and Access.Check answers the agent's own
// principal, with Principal.Via set, for as long as the grant is current and
// the agent is active. Unlike AssistantGrants.Issue, Issue needs no session.
type AgentGrants interface {
	// Issue makes a grant living MaxAgentGrantTTL and answers its id. It is
	// ErrNotAgent for a user that is not an agent, and ErrAgentInactive for one
	// that is disabled.
	Issue(ctx context.Context, g IssueAgentGrant) (uuid.UUID, error)
	// Revoke ends one grant at once. A grant already ended, or unknown, is not
	// an error.
	Revoke(ctx context.Context, grantID uuid.UUID) error
	// RevokeForUser ends every grant of an agent's user at once.
	RevokeForUser(ctx context.Context, userID uuid.UUID) error
}

// ErrAgentInactive is AgentGrants.Issue's refusal of an agent whose user is
// disabled.
var ErrAgentInactive = errors.New("agent grants: the agent is not active")
