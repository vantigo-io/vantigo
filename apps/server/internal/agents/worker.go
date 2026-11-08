package agents

// The agents-runs worker (virtual employees design V14): what needs no
// dispatcher. A run whose claim lapsed (its replica went away) is re-queued once
// and handed off the second time; a request that waited more than a day is handed
// off. The note and the assignee of a hand-off are the API process's
// (finishHandOffs), so this worker only records them.

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	runsWorkerName     = "agents-runs"
	runsWorkerInterval = 30 * time.Second
)

// workers are the module's workers (module.Module.Workers).
func workers(d module.Deps) []worker.Worker {
	s := newServer(d)
	return []worker.Worker{&runsWorker{s: s}, &reviewsWorker{s: s}, &retentionWorker{s: s}}
}

type runsWorker struct{ s *server }

func (w *runsWorker) Name() string            { return runsWorkerName }
func (w *runsWorker) Interval() time.Duration { return runsWorkerInterval }

func (w *runsWorker) Run(ctx context.Context) error {
	t := time.NewTicker(runsWorkerInterval)
	defer t.Stop()
	for {
		if err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.s.warn("agents: the runs worker failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// cycle does one pass: the lapsed runs, then the stale requests.
func (w *runsWorker) cycle(ctx context.Context) error {
	return withTx(ctx, w.s.deps.Pool, func(q *store.Queries) error {
		now := w.s.deps.Clock()
		lapsed, err := q.LockLapsedRuns(ctx, store.LockLapsedRunsParams{Now: now, N: 50})
		if err != nil {
			return fmt.Errorf("agents: find the lapsed runs: %w", err)
		}
		for _, run := range lapsed {
			if run.Attempt < 2 {
				code := handLapsed
				if err := q.StopRun(ctx, store.StopRunParams{ID: run.ID, State: runStopped, ErrorCode: &code, Now: now}); err != nil {
					return err
				}
				if err := q.InsertRetryRequest(ctx, store.InsertRetryRequestParams{RoleID: run.RoleID, ConversationID: run.ConversationID, Attempt: run.Attempt + 1, Now: now}); err != nil {
					return err
				}
				continue
			}
			summary := "The agent's run was lost twice, so the conversation is handed to a colleague."
			if err := q.StopRun(ctx, store.StopRunParams{ID: run.ID, State: runHandedOff, ErrorCode: ptr(handLapsed), Summary: &summary, Now: now}); err != nil {
				return err
			}
			if err := writeMarker(ctx, q, run.RoleID, run.ConversationID, handLapsed, now); err != nil {
				return err
			}
		}
		stale, err := q.ListStaleRequests(ctx, store.ListStaleRequestsParams{Before: now.Add(-staleAfter), N: 50})
		if err != nil {
			return fmt.Errorf("agents: find the stale requests: %w", err)
		}
		for _, r := range stale {
			if err := q.DeleteRequest(ctx, r.ID); err != nil {
				return err
			}
			if err := markHandedOff(ctx, q, r.AgentID, r.RoleID, r.ConversationID, handStale, "The request waited more than a day without being answered.", now); err != nil {
				return err
			}
		}
		return nil
	})
}
