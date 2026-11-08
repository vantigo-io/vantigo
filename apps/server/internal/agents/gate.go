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
	// byRole is each role's own allowlist, its template's: a run is narrowed to
	// its role's (V9b), and allowed, their union, serves a grant whose run is
	// not known.
	byRole map[uuid.UUID]map[string]bool
}

// gateEntry is one agent's table with its own lock, so a read of the database
// for one agent holds up that agent's calls only, and concurrent calls for it
// share one read.
type gateEntry struct {
	mu        sync.Mutex
	table     *agentTable
	checkedAt time.Time
}

// gateCache keeps each agent's table, cached by the agent's version (V9: a
// change takes effect at the next tool call).
type gateCache struct {
	mu      sync.Mutex // the map only, never held across a read
	entries map[uuid.UUID]*gateEntry
}

// entry is the agent's entry, made when there is none.
func (c *gateCache) entry(agentID uuid.UUID) *gateEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[uuid.UUID]*gateEntry{}
	}
	e := c.entries[agentID]
	if e == nil {
		e = &gateEntry{}
		c.entries[agentID] = e
	}
	return e
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
	e := s.gates.entry(agentID)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.table != nil && time.Since(e.checkedAt) < gateRecheck {
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
	acts := row.State != stateDeleted
	if e.table != nil && e.table.version == row.Version && e.table.acts == acts {
		e.checkedAt = time.Now()
		return e.table, nil
	}
	table := &agentTable{version: row.Version, acts: acts, levels: map[string]string{}, allowed: map[string]bool{}, byRole: map[uuid.UUID]map[string]bool{}}
	if acts {
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
				table.byRole[r.ID] = map[string]bool{}
				for _, a := range tpl.Allowlist {
					table.allowed[a.ID] = true
					table.byRole[r.ID][a.ID] = true
				}
			}
		}
	}
	e.table, e.checkedAt = table, time.Now()
	return table, nil
}

// allowedFor is the operations the run may call: its role's template's, when the
// run is one the module made (V9b). A grant whose run is not known gets the union
// of the agent's roles'.
func (s *server) allowedFor(ctx context.Context, table *agentTable, runID uuid.UUID) map[string]bool {
	if roleID, ok := s.runRole(ctx, runID); ok {
		if allowed, ok := table.byRole[roleID]; ok {
			return allowed
		}
		return nil // the run's role is gone
	}
	return table.allowed
}

// runRole is the role of a run, cached for good: a run never changes its role.
func (s *server) runRole(ctx context.Context, runID uuid.UUID) (uuid.UUID, bool) {
	if v, ok := s.runRoles.Load(runID); ok {
		return v.(uuid.UUID), true
	}
	run, err := store.New(s.deps.Pool).GetRun(ctx, runID)
	if err != nil {
		return uuid.Nil, false
	}
	s.runRoles.Store(runID, run.RoleID)
	return run.RoleID, true
}

// gate is the dispatcher's gate for an agent's run (V9). It starts from the
// contract's mode and narrows it by three locks, and it fails closed:
//
//   - the operation must be on the allowlist of the template of the run's role
//     (V9b); for a grant whose run the module did not make, the union of the
//     templates of the agent's roles;
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
	if err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Warn("agents: the gate could not read the agent, so it refuses", "agent", run.AgentID, "operation", info.ID, "err", err)
		}
		return operations.ModeNever
	}
	if !table.acts || !s.allowedFor(ctx, table, run.RunID)[info.ID] {
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
