package contracts

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// AgentColleagues is what the Assistant needs of the virtual employees to let a
// person talk to one in the panel (virtual employees design V31): a read-only,
// in-process port, so that the Assistant can offer the agents and narrow a
// conversation to one without importing the agents module (barred by depguard)
// or reading its schema (barred by internal/db/schema_test.go). Agents is its
// sole provider (Module.AgentColleagues); Compose wires it into every module's
// Deps before any Mount runs. It is nil when Agents is disabled, which the
// Assistant reads as "no agent to talk to".
//
// It performs no authorization of the person: it is consumer-gated. The
// Assistant asks for agents:view before it lists the agents or starts, sends or
// confirms in a conversation with one, and it judges every operation as the
// person besides (an assistant grant, the router's access rule and row rules).
// What the port adds is the agent's side of the double check: Operations says
// what the agent itself may do.
type AgentColleagues interface {
	// Agents answers the active agents, ordered by name.
	Agents(ctx context.Context) ([]ColleagueAgent, error)
	// Agent answers one active agent, or ErrColleagueUnavailable for one that is
	// unknown, paused or deleted.
	Agent(ctx context.Context, agentID uuid.UUID) (ColleagueAgent, error)
	// Names answers the name of each agent asked for, whatever its state, so a
	// conversation with an agent that has been paused still names it. An id that
	// is not an agent is left out.
	Names(ctx context.Context, agentIDs []uuid.UUID) (map[uuid.UUID]string, error)
	// Operations answers what the agent may do in the panel, by operation id:
	// the union of its roles' allowlists, less every operation that returns a
	// customer's data (a panel conversation has no customer) or writes to one (a
	// reply, a new conversation), less what the Owner has not raised the agent's
	// levels for or the agent's Identity roles do not give.
	// It is ErrColleagueUnavailable for an agent that is not active.
	Operations(ctx context.Context, agentID uuid.UUID) (map[string]ColleagueOperation, error)
}

// ColleagueAgent is an agent as the panel offers it.
type ColleagueAgent struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Name   string
	// Style is the agent's persona, how it writes; Languages is "nb", "en" or
	// "auto" (the person's language).
	Style     string
	Languages string
}

// ColleagueOperation is an operation the agent may use in the panel. Change says
// the agent may also make changes with it (its level for every permission the
// operation names is change); an operation whose contract mode is confirm is
// offered to the person only when Change is true.
type ColleagueOperation struct {
	Change bool
}

// ErrColleagueUnavailable is AgentColleagues' refusal of an agent that is
// unknown, paused or deleted.
var ErrColleagueUnavailable = errors.New("agent colleagues: the agent is not available")
