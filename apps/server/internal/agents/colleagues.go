package agents

// Talking to an agent in the Assistant's panel (virtual employees design V31):
// the Assistant owns the conversation and runs it as the person, through the
// person's own session, so the router judges every call as the person. This
// file is the agent's half of the double check, handed over as
// contracts.AgentColleagues: who the agents are and what each may do itself.
// The panel has no customer, so an operation that returns a customer's data (it
// declares x-vantigo-customer-scope) is never offered, and what an agent writes
// to a customer (a reply, a new conversation) goes through the pipeline of its
// own work, so it is not in the panel. The conversations an agent works on are
// the person's to name: the person's own rights judge each, and the person
// confirms every change.

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

type agentColleagues struct{ s *server }

var _ contracts.AgentColleagues = agentColleagues{}

func newAgentColleagues(d module.Deps) contracts.AgentColleagues {
	return agentColleagues{s: newServer(d)}
}

func colleagueOf(a store.AgentsAgent) contracts.ColleagueAgent {
	return contracts.ColleagueAgent{ID: a.ID, UserID: a.UserID, Name: a.Name, Style: a.Style, Languages: a.Languages}
}

func (c agentColleagues) Agents(ctx context.Context) ([]contracts.ColleagueAgent, error) {
	rows, err := store.New(c.s.deps.Pool).ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("agents: list the agents to talk to: %w", err)
	}
	out := []contracts.ColleagueAgent{}
	for _, a := range rows {
		if a.State == stateActive {
			out = append(out, colleagueOf(a))
		}
	}
	return out, nil
}

func (c agentColleagues) Agent(ctx context.Context, agentID uuid.UUID) (contracts.ColleagueAgent, error) {
	a, err := store.New(c.s.deps.Pool).GetAgent(ctx, agentID)
	if isNoRows(err) || (err == nil && a.State != stateActive) {
		return contracts.ColleagueAgent{}, contracts.ErrColleagueUnavailable
	}
	if err != nil {
		return contracts.ColleagueAgent{}, fmt.Errorf("agents: read the agent to talk to: %w", err)
	}
	return colleagueOf(a), nil
}

func (c agentColleagues) Names(ctx context.Context, agentIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := store.New(c.s.deps.Pool).ListAgentNames(ctx, agentIDs)
	if err != nil {
		return nil, fmt.Errorf("agents: read the agents' names: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// Operations is the agent's side of the double check. It starts from the union
// of its roles' allowlists, as the gate does for a grant whose run is not known,
// and narrows each operation by what the gate narrows a run by: the operation
// must name permissions, none in agentNever, each at a level the Owner raised
// (read, or change for a change), and held by the agent through its Identity
// roles. An operation that returns a customer's data, or writes to a customer, is
// left out.
func (c agentColleagues) Operations(ctx context.Context, agentID uuid.UUID) (map[string]contracts.ColleagueOperation, error) {
	s := c.s
	q := store.New(s.deps.Pool)
	agent, err := q.GetAgent(ctx, agentID)
	if isNoRows(err) || (err == nil && agent.State != stateActive) {
		return nil, contracts.ErrColleagueUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("agents: read the agent: %w", err)
	}
	levelRows, err := q.ListAgentLevels(ctx, agent.ID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the levels: %w", err)
	}
	levels := levelsOf(levelRows)
	roles, err := q.ListRolesOfAgent(ctx, agent.ID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the roles: %w", err)
	}

	held := map[string]bool{} // by permission key: whether the agent's Identity roles give it
	holds := func(key string) (bool, error) {
		if ok, known := held[key]; known {
			return ok, nil
		}
		holders, err := s.deps.Users.PermissionHolders(ctx, key, contracts.IncludeAgents())
		if err != nil {
			return false, fmt.Errorf("agents: read who holds %s: %w", key, err)
		}
		ok := slices.ContainsFunc(holders, func(h contracts.Holder) bool { return h.ID == agent.UserID })
		held[key] = ok
		return ok, nil
	}

	out := map[string]contracts.ColleagueOperation{}
	for _, r := range roles {
		tpl, ok := templates[r.Template]
		if !ok {
			continue
		}
		for _, a := range tpl.Allowlist {
			if _, done := out[a.ID]; done || a.CustomerData || a.ID == opReply || a.ID == opStart {
				continue
			}
			info, ok := s.deps.Operations.Info(a.ID)
			if !ok || info.Mode == operations.ModeNever || len(info.Permissions) == 0 || info.CustomerScope != nil {
				continue
			}
			change, ok := true, true
			for _, key := range info.Permissions {
				if isNever(key) {
					ok = false
					break
				}
				switch levels[key] {
				case levelChange:
				case levelRead:
					change = false
				default:
					ok = false
				}
				if !ok {
					break
				}
				if h, err := holds(key); err != nil {
					return nil, err
				} else if !h {
					ok = false
					break
				}
			}
			if !ok || (info.Mode == operations.ModeConfirm && !change) {
				continue
			}
			out[a.ID] = contracts.ColleagueOperation{Change: change}
		}
	}
	return out, nil
}
