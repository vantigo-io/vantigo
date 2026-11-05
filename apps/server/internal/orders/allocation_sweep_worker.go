package orders

// The orders.allocation_sweep worker (orders design O36): once a day every
// draft allocation untouched for 30 days — Energy's horizon for a draft
// settlement — is released, a backstop for a ReleaseAllocations that never
// arrived after a draft invoice was deleted. If that draft still exists,
// issuing it later is refused (source_not_invoiceable) and the user drafts
// again.
//
// One replica runs a cycle: it takes an advisory lease on a pooled
// connection of its own and runs its one statement on that connection, so it
// never asks the pool for a second one while it holds one — safe on a pool
// of one. The statement locks only the allocations it releases: an issue
// stamping one holds it FOR UPDATE, so the sweep waits and then skips it,
// no longer a draft.

import (
	"context"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

const (
	allocationSweepWorkerName = "orders.allocation_sweep"
	allocationSweepInterval   = 24 * time.Hour
	allocationSweepLeaseKey   = int64(0x4f52445253575031) // "ORDRSWP1"
	allocationSweepDeadline   = 5 * time.Minute
	// allocationDraftHorizon is how long a draft allocation is kept untouched.
	allocationDraftHorizon = 30 * 24 * time.Hour
)

// allocationSweepWorker releases the draft allocations past the horizon.
type allocationSweepWorker struct{ s *server }

func (w *allocationSweepWorker) Name() string            { return allocationSweepWorkerName }
func (w *allocationSweepWorker) Interval() time.Duration { return allocationSweepInterval }

func (w *allocationSweepWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(allocationSweepInterval)
	defer ticker.Stop()
	for {
		if _, _, err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.s.deps.Logger.ErrorContext(ctx, "orders: the allocation sweep failed", "worker", allocationSweepWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// cycle is one pass under the lease. It answers whether it won the lease and
// how many allocations it released.
func (w *allocationSweepWorker) cycle(ctx context.Context) (bool, int, error) {
	s := w.s
	now := s.deps.Clock()
	ctx, cancel := context.WithTimeout(ctx, allocationSweepDeadline)
	defer cancel()
	conn, err := s.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("orders: acquire a connection for the allocation sweep's lease: %w", err)
	}
	defer conn.Release()
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, allocationSweepLeaseKey).Scan(&acquired); err != nil {
		return false, 0, fmt.Errorf("orders: take the allocation sweep's lease: %w", err)
	}
	if !acquired {
		return false, 0, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, allocationSweepLeaseKey); err != nil {
			// A session that still holds the lease must not go back into the pool.
			_ = conn.Conn().Close(release)
		}
	}()
	released, err := store.New(conn).SweepDraftAllocations(ctx, store.SweepDraftAllocationsParams{
		Now: &now, Cutoff: now.Add(-allocationDraftHorizon),
	})
	if err != nil {
		return true, 0, fmt.Errorf("orders: release the stale draft allocations: %w", err)
	}
	if len(released) > 0 {
		s.deps.Logger.InfoContext(ctx, "orders: released stale draft allocations", "worker", allocationSweepWorkerName, "count", len(released))
	}
	return true, len(released), nil
}
