package assistant

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// The module's two workers (the Assistant design S9):
//
//   - assistant-runs marks the runs still running after staleRunAge failed
//     (server_restarted), so an API restart never leaves a run running forever;
//   - assistant-retention deletes the conversations whose last activity is older
//     than the workspace's retention, whole, in chunks, and sweeps the
//     conversations and switches of people identity has erased (erasure.go).

const (
	staleRunAge       = 10 * time.Minute
	staleRunsPoll     = time.Minute
	retentionPoll     = 24 * time.Hour
	retentionChunk    = 200
	erasureChunk      = 200
	retentionLeaseKey = int64(0x4153535452455431) // "ASSTRET1"
)

// workers are the module's background workers.
func workers(d module.Deps) []worker.Worker {
	return []worker.Worker{&staleRunsWorker{deps: d}, &retentionWorker{deps: d}}
}

// staleRunsWorker fails the runs nothing is executing any more.
type staleRunsWorker struct{ deps module.Deps }

func (*staleRunsWorker) Name() string            { return "assistant-runs" }
func (*staleRunsWorker) Interval() time.Duration { return staleRunsPoll }

func (w *staleRunsWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(staleRunsPoll)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.Error("assistant stale-runs cycle failed", "worker", w.Name(), "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle fails the stale runs and answers how many it failed.
func (w *staleRunsWorker) RunCycle(ctx context.Context) (int, error) {
	now := w.deps.Clock()
	ids, err := store.New(w.deps.Pool).FailStaleRuns(ctx, store.FailStaleRunsParams{Now: now, Cutoff: now.Add(-staleRunAge)})
	if err != nil {
		return 0, fmt.Errorf("assistant: fail the stale runs: %w", err)
	}
	return len(ids), nil
}

// retentionWorker applies the retention and the erasure sweep.
type retentionWorker struct{ deps module.Deps }

func (*retentionWorker) Name() string            { return "assistant-retention" }
func (*retentionWorker) Interval() time.Duration { return retentionPoll }

func (w *retentionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(retentionPoll)
	defer ticker.Stop()
	for {
		if _, _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.Error("assistant retention cycle failed", "worker", w.Name(), "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under an advisory lease, so one replica does it: it
// deletes the expired conversations, then sweeps the erased people. It answers
// how many conversations retention deleted and how many people the sweep found
// erased; it deletes nothing and answers zeros when another replica holds the
// lease.
//
// The lease's session is a pooled connection held for the whole expiry, so the
// expiry runs its statements on that connection and never asks the pool for a
// second one: a worker that holds one connection while waiting for another
// deadlocks the pool once as many workers do it as the pool has connections
// (four on a four-core host), and readiness, which needs one too, fails with
// them. The sweep asks identity, whose queries take their own connections, so
// it runs after the lease's connection is back in the pool; it is idempotent,
// and only the replica that won the lease runs it.
func (w *retentionWorker) RunCycle(ctx context.Context) (expired, erased int, err error) {
	acquired, expired, err := w.expireUnderLease(ctx)
	if err != nil || !acquired {
		return expired, 0, err
	}
	erased, err = sweepErased(ctx, w.deps)
	return expired, erased, err
}

// expireUnderLease takes the lease on a connection of its own and, when it
// wins it, runs the expiry on that same connection.
func (w *retentionWorker) expireUnderLease(ctx context.Context) (acquired bool, expired int, err error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("assistant: acquire a connection for the retention lease: %w", err)
	}
	defer conn.Release()
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, retentionLeaseKey).Scan(&acquired); err != nil {
		return false, 0, fmt.Errorf("assistant: take the retention lease: %w", err)
	}
	if !acquired {
		return false, 0, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, uerr := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, retentionLeaseKey); uerr != nil {
			// A session that still holds the lease must not go back into the pool.
			_ = conn.Conn().Close(release)
		}
	}()
	expired, err = w.expire(ctx, store.New(conn))
	return true, expired, err
}

// expire deletes whole conversations by their last activity, a chunk at a time.
func (w *retentionWorker) expire(ctx context.Context, q *store.Queries) (int, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("assistant: read the retention: %w", err)
	}
	cutoff := w.deps.Clock().AddDate(0, -int(settings.RetentionMonths), 0)
	total := 0
	for {
		n, err := q.DeleteExpiredConversations(ctx, store.DeleteExpiredConversationsParams{Cutoff: cutoff, BatchSize: retentionChunk})
		if err != nil {
			return total, fmt.Errorf("assistant: delete the expired conversations: %w", err)
		}
		total += int(n)
		if n < retentionChunk || ctx.Err() != nil {
			return total, nil
		}
	}
}
