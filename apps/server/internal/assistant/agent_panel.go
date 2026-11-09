package assistant

// Talking to a virtual employee in the panel (virtual employees design V31). The
// conversation is the person's own, and so is every run: it runs under the
// person's assistant grant, so the router judges each call as the person (the
// access rule and the handler's row rules), and the Owner's levels for the
// Assistant (S16) apply as they always do. What the agent adds is the second
// half of the double check and its voice: contracts.AgentColleagues says what the
// agent itself may do, and colleagueOps lets through only what both may, so the
// person sees only what they and the agent may both see. The agent's persona
// applies and Knowledge is read through the operations; its modes do not: every
// change is the Assistant's confirmation card, decided by the person.
//
// The panel has no customer and no conversation of the agent's, so the agent is
// never offered an operation that is about a conversation or returns a customer's
// data, and the module's own tools of an agent (reply, note, start) are not in
// the panel at all.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// permAgentsView is what a person needs to talk to an agent: the narrowest
// permission that already lets them see the agents, their personas and roles.
const permAgentsView = "agents:view"

// mayTalkToAgents says the installation has agents and the caller may see them.
// The caller is a request, or a run of the person's (the AsUser mark); any
// failure reads as no.
func (s *server) mayTalkToAgents(ctx context.Context) bool {
	return s.deps.AgentColleagues != nil && contracts.HasPermission(ctx, s.deps.Access, permAgentsView)
}

// agentNames is the names of the agents the conversations are with, by agent id.
// A failure to read leaves them out: the conversation is shown without a name
// rather than not shown.
func (s *server) agentNames(ctx context.Context, ids ...*uuid.UUID) map[uuid.UUID]string {
	var asked []uuid.UUID
	for _, id := range ids {
		if id != nil {
			asked = append(asked, *id)
		}
	}
	if len(asked) == 0 || s.deps.AgentColleagues == nil {
		return nil
	}
	names, err := s.deps.AgentColleagues.Names(ctx, asked)
	if err != nil {
		s.deps.Logger.Warn("assistant: read the names of agents", "error", err)
		return nil
	}
	return names
}

// agentRef is the agent a conversation is with, nil for the Assistant's own.
func agentRef(id *uuid.UUID, names map[uuid.UUID]string) *gen.AssistantAgentRef {
	if id == nil {
		return nil
	}
	return &gen.AssistantAgentRef{Id: *id, Name: names[*id]}
}

// ListAssistantAgents (GET /api/v1/assistant/agents)
func (s *server) ListAssistantAgents(ctx context.Context, _ gen.ListAssistantAgentsRequestObject) (gen.ListAssistantAgentsResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.ListAssistantAgentsResponseObject](r, err)
	}
	out := gen.AssistantAgentList{Items: []gen.AssistantAgentRef{}}
	if !s.mayTalkToAgents(ctx) {
		return gen.ListAssistantAgents200JSONResponse(out), nil
	}
	agents, err := s.deps.AgentColleagues.Agents(ctx)
	if err != nil {
		return nil, fmt.Errorf("assistant: list the agents: %w", err)
	}
	for _, a := range agents {
		out.Items = append(out.Items, gen.AssistantAgentRef{Id: a.ID, Name: a.Name})
	}
	return gen.ListAssistantAgents200JSONResponse(out), nil
}

// panelAgent is the agent a run talks as: who it is and what it may do itself.
type panelAgent struct {
	agent contracts.ColleagueAgent
	ops   map[string]contracts.ColleagueOperation
}

// loadPanelAgent reads the agent a conversation is with. It answers
// contracts.ErrColleagueUnavailable for an agent that is paused or deleted.
func (s *server) loadPanelAgent(ctx context.Context, agentID uuid.UUID) (*panelAgent, error) {
	agent, err := s.deps.AgentColleagues.Agent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	ops, err := s.deps.AgentColleagues.Operations(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return &panelAgent{agent: agent, ops: ops}, nil
}

// panelAgentOf is the agent of a conversation for a run, or nil for the
// Assistant's own. A conversation whose agent can no longer be talked to, or
// whose person lost agents:view, gets an agent that may do nothing: its tools
// refuse every operation, so a run that goes on (a cancelled card's last line)
// can reach nothing.
func (s *server) panelAgentOf(ctx context.Context, agentID *uuid.UUID) *panelAgent {
	if agentID == nil {
		return nil
	}
	if s.mayTalkToAgents(ctx) {
		if p, err := s.loadPanelAgent(ctx, *agentID); err == nil {
			return p
		} else if !errors.Is(err, contracts.ErrColleagueUnavailable) {
			s.deps.Logger.Warn("assistant: read the agent of a conversation", "error", err)
		}
	}
	return &panelAgent{}
}

// unavailableAgent is the refusal of a message to an agent that cannot be talked
// to now.
func unavailableAgent() refusal {
	return refuse(http.StatusConflict, codeAgentUnavailable, "This virtual employee is not available now")
}

// colleagueOps is the dispatcher as an agent's panel run sees it: the
// operations the person may use that the agent may use too, and no others. It
// narrows the person's own list, never widens it, and refuses a call the agent
// may not make whatever the person may; the router has judged the person already
// and judges the call again.
type colleagueOps struct {
	inner agentloop.Operations
	agent *panelAgent
}

// allowed says the agent may use the operation as it is offered to the person:
// a change needs the agent's level for it.
func (c colleagueOps) allowed(info operations.Info) bool {
	op, ok := c.agent.ops[info.ID]
	return ok && (info.Mode != operations.ModeConfirm || op.Change)
}

func (c colleagueOps) narrow(list []operations.Info) []operations.Info {
	out := make([]operations.Info, 0, len(list))
	for _, info := range list {
		if c.allowed(info) {
			out = append(out, info)
		}
	}
	return out
}

func (c colleagueOps) List(ctx context.Context, userID uuid.UUID) ([]operations.Info, error) {
	list, err := c.inner.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	return c.narrow(list), nil
}

// Search asks for the most the dispatcher gives and keeps the first limit the
// agent may use: the agent's narrower list must not leave the model with fewer
// answers than there are.
func (c colleagueOps) Search(ctx context.Context, userID uuid.UUID, query string, limit int) ([]operations.Info, error) {
	found, err := c.inner.Search(ctx, userID, query, 50)
	if err != nil {
		return nil, err
	}
	found = c.narrow(found)
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	return found, nil
}

// Describe answers only for an operation the agent may use at all, as List does;
// a change the agent may not make is described as one it may not use.
func (c colleagueOps) Describe(id string) (json.RawMessage, error) {
	if _, ok := c.agent.ops[id]; !ok {
		return nil, &operations.Error{Code: operations.CodeUnknownOperation, Message: "there is no operation " + id + " that the virtual employee may use"}
	}
	return c.inner.Describe(id)
}

// Call makes the call only if the agent may. A call the model could not have
// found through List is refused here too, and the confirmed call of a card is
// made only if the agent may still change with it.
func (c colleagueOps) Call(ctx context.Context, req operations.CallRequest) (operations.CallResult, error) {
	op, ok := c.agent.ops[req.OperationID]
	if !ok || (req.Confirmed && !op.Change) {
		return operations.CallResult{}, &operations.Error{Code: operations.CodeNever, Message: "the virtual employee may not use " + req.OperationID}
	}
	return c.inner.Call(ctx, req)
}

// agentMayStillChange is nil when the agent may still make the change a card
// waits on, and the refusal of the confirmation when it may not.
func (s *server) agentMayStillChange(ctx context.Context, agentID uuid.UUID, operationID string) *refusal {
	if !s.mayTalkToAgents(ctx) {
		r := refuse(http.StatusForbidden, codeAgentUnavailable, "You cannot talk to virtual employees")
		return &r
	}
	ops, err := s.deps.AgentColleagues.Operations(ctx, agentID)
	if err != nil && !errors.Is(err, contracts.ErrColleagueUnavailable) {
		s.deps.Logger.Warn("assistant: read what an agent may do", "error", err)
	}
	if err != nil {
		r := refuse(http.StatusForbidden, codeAgentUnavailable, "This virtual employee is not available now")
		return &r
	}
	if op, ok := ops[operationID]; !ok || !op.Change {
		r := refuse(http.StatusForbidden, codeNotAllowed, "The virtual employee may no longer make this change")
		return &r
	}
	return nil
}
