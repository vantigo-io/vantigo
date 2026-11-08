package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// levelRank orders the levels: off, read, change.
func levelRank(level string) int {
	switch level {
	case levelChange:
		return 2
	case levelRead:
		return 1
	}
	return 0
}

// levelsOf is the agent's levels by permission key; a key with no row is off.
func levelsOf(rows []store.AgentsPermissionLevel) map[string]string {
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.PermissionKey] = r.Level
	}
	return out
}

// needsOf are the levels the agent's roles need, the highest of each key, in the
// order the templates list them. A permission the catalog lacks, because its
// module is not enabled, is left out: nothing that names it can be called.
func (s *server) needsOf(roles []store.AgentsRole) []need {
	var out []need
	for _, r := range roles {
		tpl, ok := templates[r.Template]
		if !ok {
			continue
		}
		for _, n := range tpl.Needs {
			if _, inCatalog := s.deps.Catalog[n.Permission]; !inCatalog {
				continue
			}
			i := slices.IndexFunc(out, func(o need) bool { return o.Permission == n.Permission })
			switch {
			case i < 0:
				out = append(out, n)
			case levelRank(n.Level) > levelRank(out[i].Level):
				out[i].Level = n.Level
			}
		}
	}
	return out
}

// GetAgentSetup (GET /api/v1/agents/agents/{agentId}/setup)
//
// What the agent's roles need (V12) against what the Owner gave it: each needed
// permission with its level, and whether the agent's Identity roles hold the
// permission at all, which a level cannot give.
func (s *server) GetAgentSetup(ctx context.Context, req gen.GetAgentSetupRequestObject) (gen.GetAgentSetupResponseObject, error) {
	row, err := s.loadAgent(ctx, req.AgentId)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return notFound(), nil
	}
	q := store.New(s.deps.Pool)
	roles, err := q.ListRolesOfAgent(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the roles: %w", err)
	}
	levelRows, err := q.ListAgentLevels(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the levels: %w", err)
	}
	levels := levelsOf(levelRows)

	out := gen.AgentSetup{HasRoles: len(roles) > 0, Items: []gen.AgentSetupItem{}}
	ready := out.HasRoles
	for _, n := range s.needsOf(roles) {
		holders, err := s.deps.Users.PermissionHolders(ctx, n.Permission, contracts.IncludeAgents())
		if err != nil {
			return nil, fmt.Errorf("agents: read who holds %s: %w", n.Permission, err)
		}
		held := slices.ContainsFunc(holders, func(h contracts.Holder) bool { return h.ID == row.UserID })
		level := levels[n.Permission]
		if level == "" {
			level = levelOff
		}
		locked := isNever(n.Permission)
		satisfied := !locked && levelRank(level) >= levelRank(n.Level)
		ready = ready && satisfied && held
		out.Items = append(out.Items, gen.AgentSetupItem{
			PermissionKey: n.Permission, Display: s.deps.Catalog[n.Permission].Display, Needed: gen.AgentSetupItemNeeded(n.Level),
			Level: gen.AgentSetupItemLevel(level), Satisfied: satisfied, HeldByRoles: held, Locked: locked,
		})
	}
	out.Ready = ready && len(out.Items) > 0
	return gen.GetAgentSetup200JSONResponse(out), nil
}

// PutAgentPermission (PUT /api/v1/agents/agents/{agentId}/permissions/{key})
//
// Every permission of an agent is off until a row says otherwise (V9), so off
// deletes the row. Lowering a level is anyone's with agents:manage; raising it
// is the Owner's alone. A permission in the agentNever list takes no level.
func (s *server) PutAgentPermission(ctx context.Context, req gen.PutAgentPermissionRequestObject) (gen.PutAgentPermissionResponseObject, error) {
	if _, known := s.deps.Catalog[req.Key]; !known {
		return notFound(), nil
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	level := string(req.Body.Level)
	if !oneOf(level, levelOff, levelRead, levelChange) {
		return invalid("The level is not valid", map[string][]string{"level": {"level must be off, read or change"}}), nil
	}
	if isNever(req.Key) {
		return refuse(409, codeLevelLocked, "No level can raise this permission for an agent"), nil
	}

	actor := callerID(ctx)
	owner := s.isOwner(ctx)
	var version int32
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		agent, err := q.LockAgent(ctx, req.AgentId)
		if err != nil {
			return err
		}
		before := levelOff
		if row, err := q.GetAgentLevel(ctx, store.GetAgentLevelParams{AgentID: agent.ID, PermissionKey: req.Key}); err == nil {
			before = row.Level
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("agents: read the level: %w", err)
		}
		if levelRank(level) > levelRank(before) && !owner {
			return errOwnerRequired
		}
		if level == levelOff {
			err = q.DeleteAgentLevel(ctx, store.DeleteAgentLevelParams{AgentID: agent.ID, PermissionKey: req.Key})
		} else {
			err = q.UpsertAgentLevel(ctx, store.UpsertAgentLevelParams{AgentID: agent.ID, PermissionKey: req.Key, Level: level, Now: s.deps.Clock(), UpdatedBy: actor})
		}
		if err != nil {
			return fmt.Errorf("agents: change the level: %w", err)
		}
		if err := q.BumpAgentVersion(ctx, store.BumpAgentVersionParams{ID: agent.ID, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("agents: move the agent's version on: %w", err)
		}
		version = agent.Version + 1
		return s.audit(ctx, q, actor, "permission.changed", agent.ID.String()+" "+req.Key, map[string]any{"level": before}, map[string]any{"level": level})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return notFound(), nil
	case errors.Is(err, errOwnerRequired):
		return ownerRequired(), nil
	case err != nil:
		return nil, err
	}
	s.gates.invalidate(req.AgentId)
	return gen.PutAgentPermission200JSONResponse(gen.AgentPermission{PermissionKey: req.Key, Level: gen.AgentPermissionLevel(level), Version: version}), nil
}
