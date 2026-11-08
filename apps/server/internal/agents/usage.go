package agents

// The agent's usage and budget (virtual employees design V24): counts per agent
// per business day. When the day's tokens reach the agent's daily budget no new
// run starts, and the requests that wait are handed off.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// withTx runs fn on one transaction of this module's own rows.
func withTx(ctx context.Context, pool db.TxBeginner, fn func(q *store.Queries) error) error {
	return db.WithTx(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error { return fn(store.New(tx)) })
}

// day is the business day of t, as a date.
func (s *server) day(t time.Time) time.Time {
	if s.deps.Calendar != nil {
		if d, err := s.deps.Calendar.Day(context.Background(), t); err == nil {
			return d
		}
	}
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// countTokens adds a model turn's tokens to the agent's day.
func (s *server) countTokens(ctx context.Context, agentID uuid.UUID, in, out int64) {
	if in == 0 && out == 0 {
		return
	}
	if err := store.New(s.deps.Pool).AddUsage(context.WithoutCancel(ctx), store.AddUsageParams{
		AgentID: agentID, Day: s.day(s.deps.Clock()), InputTokens: in, OutputTokens: out,
	}); err != nil {
		s.warn("agents: count tokens", "agent", agentID, "err", err)
	}
}

// overBudget says the agent's tokens today have reached its daily budget.
func (s *server) overBudget(ctx context.Context, agent store.AgentsAgent) (bool, error) {
	if agent.DailyTokenBudget == nil {
		return false, nil
	}
	row, err := store.New(s.deps.Pool).GetUsageDay(ctx, store.GetUsageDayParams{AgentID: agent.ID, Day: s.day(s.deps.Clock())})
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return row.InputTokens+row.OutputTokens >= int64(*agent.DailyTokenBudget), nil
}

// handOffOverBudget hands off the requests that wait for an agent whose budget
// is reached: each request is deleted and the conversation handed off with the
// note "Daily budget reached" (V24). The note and the assignee are done by
// finishHandOffs.
func (s *server) handOffOverBudget(ctx context.Context) error {
	q := store.New(s.deps.Pool)
	agents, err := q.ListAgentsWithBudget(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range agents {
		agent, err := q.GetAgentByID(ctx, a.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if over, err := s.overBudget(ctx, agent); err != nil || !over {
			if err != nil {
				errs = append(errs, err)
			}
			continue
		}
		errs = append(errs, s.handOffWaiting(ctx, agent))
	}
	return errors.Join(errs...)
}

// handOffWaiting hands off every request that waits for the agent, in one
// transaction of this module's own rows.
func (s *server) handOffWaiting(ctx context.Context, agent store.AgentsAgent) error {
	return withTx(ctx, s.deps.Pool, func(q *store.Queries) error {
		reqs, err := q.ListUntakenRequestsOfAgent(ctx, agent.ID)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		for _, r := range reqs {
			if err := q.DeleteRequest(ctx, r.ID); err != nil {
				return err
			}
			if err := markHandedOff(ctx, q, agent.ID, r.RoleID, r.ConversationID, handBudget, "Daily budget reached.", now); err != nil {
				return err
			}
		}
		return nil
	})
}
