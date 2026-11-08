package agents

// The runner (virtual employees design V14): the API process polls the run queue
// every five seconds, claims due requests with FOR UPDATE SKIP LOCKED in a
// transaction that commits before the run starts, and carries at most
// AGENTS_CONCURRENCY runs at once, each a goroutine with a five-minute ceiling.
// It needs the operations dispatcher, which only the API process has, so it is a
// module.Runner (Module.APIRunners) and not a worker. The agents-runs worker
// (worker.go) does what needs no dispatcher: lapsed claims and stale requests.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

const (
	pollEvery       = 5 * time.Second
	claimRenewEvery = 30 * time.Second
	claimFor        = 2 * time.Minute
	runnerName      = "agents-runner"
)

// apiRunners builds the module's runner (module.Module.APIRunners). It exists
// only where the dispatcher and the grants do.
func apiRunners(d module.Deps) []module.Runner {
	if d.Operations == nil || d.AgentGrants == nil || d.AgentAccounts == nil {
		return nil
	}
	concurrency := 4
	if d.Config != nil && d.Config.AgentsConcurrency > 0 {
		concurrency = d.Config.AgentsConcurrency
	}
	return []module.Runner{&runner{s: newServer(d), replica: uuid.New(), sem: make(chan struct{}, concurrency)}}
}

type runner struct {
	s         *server
	replica   uuid.UUID
	sem       chan struct{}
	wg        sync.WaitGroup
	finishing atomic.Bool
}

func (r *runner) Name() string { return runnerName }

func (r *runner) Run(ctx context.Context) error {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		r.poll(ctx)
		select {
		case <-ctx.Done():
			r.wg.Wait()
			return nil
		case <-t.C:
		}
	}
}

// poll takes the work that is due: the hand-offs to finish, the requests of an
// agent over its budget, and as many runs as there are free places.
func (r *runner) poll(ctx context.Context) {
	if r.finishing.CompareAndSwap(false, true) {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer r.finishing.Store(false)
			if err := r.s.handOffOverBudget(ctx); err != nil && ctx.Err() == nil {
				r.s.warn("agents: hand off the requests over budget", "err", err)
			}
			if err := r.s.finishHandOffs(ctx); err != nil && ctx.Err() == nil {
				r.s.warn("agents: finish the hand-offs", "err", err)
			}
		}()
	}
	for ctx.Err() == nil {
		select {
		case r.sem <- struct{}{}:
		default:
			return // every place is taken
		}
		run, ok, err := r.s.claim(ctx, r.replica)
		if err != nil || !ok {
			<-r.sem
			if err != nil && ctx.Err() == nil {
				r.s.warn("agents: claim a run", "err", err)
			}
			return
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer func() { <-r.sem }()
			defer func() {
				if rec := recover(); rec != nil {
					r.s.warn("agents: a run panicked", "run", run.ID, "panic", fmt.Sprint(rec))
					r.s.finishRun(context.WithoutCancel(ctx), run, runFailed, "", "internal", "")
				}
			}()
			r.s.executeRun(ctx, run)
		}()
	}
}

// claim takes the oldest due request of an active agent and starts its run, in
// one transaction that is committed before the run does anything: no lock or
// connection is held while the run works.
func (s *server) claim(ctx context.Context, replica uuid.UUID) (store.AgentsRun, bool, error) {
	var run store.AgentsRun
	var claimed bool
	err := withTx(ctx, s.deps.Pool, func(q *store.Queries) error {
		now := s.deps.Clock()
		c, err := q.ClaimDueRequest(ctx, now)
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("agents: find a due request: %w", err)
		}
		id := uuid.New()
		if err := q.InsertRun(ctx, store.InsertRunParams{
			ID: id, AgentID: c.AgentID, RoleID: c.RoleID, ConversationID: c.ConversationID, Reason: c.Reason, Attempt: c.Attempt,
			ClaimedBy: &replica, ClaimedUntil: now.Add(claimFor), Now: now,
		}); err != nil {
			return fmt.Errorf("agents: start a run: %w", err)
		}
		if err := q.MarkRequestTaken(ctx, store.MarkRequestTakenParams{ID: c.ID, RunID: &id, Now: now}); err != nil {
			return err
		}
		if run, err = q.GetRun(ctx, id); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if uniqueViolation(err) {
		return run, false, nil // a run of this role and conversation is under way
	}
	return run, claimed, err
}
