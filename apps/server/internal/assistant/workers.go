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
//     (server_restarted), so an API restart never leaves a run running forever,
//     expires the proposed changes nobody decided in 24 hours (S12), expires the
//     bulk plans and the jobs waiting for Continue that nobody went on with in 24
//     hours (S13), and hands a bulk job whose claim lapsed to the API server in
//     its own process, if it has one;
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

// RunCycle fails the stale runs, expires the changes nobody decided within
// pendingTTL (and ends the runs that waited for them), and closes the changes
// whose confirmation was claimed and never recorded. It answers how many runs it
// ended. Each is one statement on the pool and none holds a connection while
// asking for another.
func (w *staleRunsWorker) RunCycle(ctx context.Context) (int, error) {
	now := w.deps.Clock()
	q := store.New(w.deps.Pool)
	ids, err := q.FailStaleRuns(ctx, store.FailStaleRunsParams{Now: now, Cutoff: now.Add(-staleRunAge)})
	if err != nil {
		return 0, fmt.Errorf("assistant: fail the stale runs: %w", err)
	}
	expired, err := q.ExpirePendingActions(ctx, now)
	if err != nil {
		return len(ids), fmt.Errorf("assistant: expire the pending actions: %w", err)
	}
	if _, err := q.InterruptClaimedActions(ctx, now.Add(-staleRunAge)); err != nil {
		return len(ids) + len(expired), fmt.Errorf("assistant: close the claimed actions: %w", err)
	}
	// The bulk plans nobody started and the jobs nobody went on with expire too.
	jobs, err := q.ExpireBulkJobs(ctx, store.ExpireBulkJobsParams{Now: now, Cutoff: now.Add(-bulkWaitTTL)})
	if err != nil {
		return len(ids) + len(expired), fmt.Errorf("assistant: expire the bulk jobs: %w", err)
	}
	// A job whose claim lapsed needs the dispatcher, which only a process that
	// serves the API has: when the workers run in it, the API server composed over
	// the same pool takes the job over. A worker-mode process has none, and the
	// next read of the job by the person's own replica does it.
	resumeLapsedFor(ctx, w.deps)
	return len(ids) + len(expired) + len(jobs), nil
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
	// The usage counts are kept as long as the conversations are.
	if err := q.DeleteOldUsage(ctx, cutoff); err != nil {
		return 0, fmt.Errorf("assistant: delete the old usage: %w", err)
	}
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
