package agents

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// gateRecheck is how long the gate trusts the version it last read before it
// asks the database again: a change made on this replica takes effect at once
// (invalidate), one made on another within this window, at the next tool call
// after it.
const gateRecheck = time.Second

// agentTable is what the gate knows of one agent at one version: whether it may
// act, the levels the Owner gave it and the operations its roles' templates
// allow. It is immutable once built.
type agentTable struct {
	version int32
	acts    bool
	levels  map[string]string
	allowed map[string]bool
}

type gateEntry struct {
	table     *agentTable
	checkedAt time.Time
}

// gateCache keeps each agent's table, cached by the agent's version (V9: a
// change takes effect at the next tool call).
type gateCache struct {
	mu      sync.Mutex
	entries map[uuid.UUID]*gateEntry
}

// invalidate forgets the agent's table, so the next gate call reads the
// database.
func (c *gateCache) invalidate(agentID uuid.UUID) {
	c.mu.Lock()
	delete(c.entries, agentID)
	c.mu.Unlock()
}

// table is the agent's current table. It asks the database for the agent's
// version at most once per gateRecheck, and reads the levels and roles only when
// the version moved. An agent that is not there, or is deleted, does not act.
func (s *server) table(ctx context.Context, agentID uuid.UUID) (*agentTable, error) {
	c := &s.gates
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[agentID]
	if e != nil && time.Since(e.checkedAt) < gateRecheck {
		return e.table, nil
	}
	q := store.New(s.deps.Pool)
	row, err := q.GetAgentGate(ctx, agentID)
	if err != nil {
		if isNoRows(err) {
			return &agentTable{}, nil
		}
		return nil, err
	}
	if e != nil && e.table.version == row.Version && e.table.acts == (row.State != stateDeleted) {
		e.checkedAt = time.Now()
		return e.table, nil
	}
	table := &agentTable{version: row.Version, acts: row.State != stateDeleted, levels: map[string]string{}, allowed: map[string]bool{}}
	if table.acts {
		levels, err := q.ListAgentLevels(ctx, agentID)
		if err != nil {
			return nil, err
		}
		table.levels = levelsOf(levels)
		roles, err := q.ListRolesOfAgent(ctx, agentID)
		if err != nil {
			return nil, err
		}
		for _, r := range roles {
			if tpl, ok := templates[r.Template]; ok {
				for _, a := range tpl.Allowlist {
					table.allowed[a.ID] = true
				}
			}
		}
	}
	if c.entries == nil {
		c.entries = map[uuid.UUID]*gateEntry{}
	}
	c.entries[agentID] = &gateEntry{table: table, checkedAt: time.Now()}
	return table, nil
}

// gate is the dispatcher's gate for an agent's run (V9). It starts from the
// contract's mode and narrows it by three locks, and it fails closed:
//
//   - the operation must be on the allowlist of a template one of the agent's
//     roles has (V9b). The grant names the agent and the run, not the role, so
//     until runs record theirs the allowlist is the union of the agent's roles';
//   - none of the permissions it names may be in agentNever, whatever the level;
//   - each permission it names narrows the mode by the agent's level for it,
//     which is off until the Owner raises it: off makes it never, read turns
//     confirm into never, change leaves it.
//
// An operation with an access rule that names no permission (a policy or a
// session rule) is never, as are a run the dispatcher could not resolve and a
// table that cannot be read. The agent still needs the permission through its
// Identity roles, and the router still checks every call.
func (s *server) gate(ctx context.Context, info operations.Info) operations.Mode {
	run, ok := operations.AgentRunFrom(ctx)
	if !ok || info.Mode == operations.ModeNever || len(info.Permissions) == 0 {
		return operations.ModeNever
	}
	table, err := s.table(ctx, run.AgentID)
	if err != nil || !table.acts || !table.allowed[info.ID] {
		return operations.ModeNever
	}
	mode := info.Mode
	for _, key := range info.Permissions {
		if isNever(key) {
			return operations.ModeNever
		}
		switch table.levels[key] {
		case levelChange:
		case levelRead:
			if mode == operations.ModeConfirm {
				mode = operations.ModeNever
			}
		default:
			return operations.ModeNever
		}
	}
	return mode
}
