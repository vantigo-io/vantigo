package customers

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// The permissions the agent-memory section needs in the agents module: seeing the
// notes needs the first (besides customers:view, the operation's access), and
// deleting one, which the agents API decides, the second. They are asked here and
// not in the operation's access because a deployment without the agents module has
// neither in its catalog. The page hides the delete the caller lacks.
const (
	agentsView   = "agents:view"
	agentsManage = "agents:manage"
)

// GetCustomersByIdAgentMemory (GET /api/v1/customers/{id}/agent-memory)
//
// What the virtual employees remember about the customer (virtual employees
// design V27), read through contracts.AgentMemory. The contract performs no
// authorization, so it is decided here: a caller without agents:view is answered
// the page as it was, an empty list, and the contract is not asked. A deployment
// without the agents module (Deps.AgentMemory nil) has nothing remembered and
// answers an empty list too.
func (s *server) GetCustomersByIdAgentMemory(ctx context.Context, req gen.GetCustomersByIdAgentMemoryRequestObject) (gen.GetCustomersByIdAgentMemoryResponseObject, error) {
	if _, err := store.New(s.deps.Pool).GetCustomer(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersByIdAgentMemory404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	out := gen.CustomerAgentMemory{Agents: []gen.CustomerAgentMemoryEntry{}}
	if s.deps.AgentMemory == nil || !s.hasPermission(ctx, agentsView) {
		return gen.GetCustomersByIdAgentMemory200JSONResponse(out), nil
	}
	out.CanDelete = s.hasPermission(ctx, agentsManage)
	entries, err := s.deps.AgentMemory.AboutCustomer(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: read what the agents remember: %w", err)
	}
	for _, e := range entries {
		notes := make([]gen.CustomerAgentMemoryNote, 0, len(e.Notes))
		for _, n := range e.Notes {
			notes = append(notes, gen.CustomerAgentMemoryNote{Id: n.ID, Text: n.Text, CreatedAt: n.CreatedAt})
		}
		out.Agents = append(out.Agents, gen.CustomerAgentMemoryEntry{AgentId: e.AgentID, AgentName: e.AgentName, Full: e.Full, Notes: notes})
	}
	return gen.GetCustomersByIdAgentMemory200JSONResponse(out), nil
}
