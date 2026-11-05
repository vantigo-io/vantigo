package invoices

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices.allocation_reconcile worker (orders design O36,
// as amended for delivery B's review): this module owns the cleanup of the
// order allocations it took. Every allocation Orders' Allocate answered is in
// the ledger (invoices.order_allocations_taken) from the moment it was
// taken; every ten minutes the worker gives back to Orders each one taken
// over an hour ago that no live line source holds — a draft deleted, or a
// line removed, whose release after the commit failed; a customer's
// anonymisation, which deletes the customer's drafts inside its own
// transaction; a wizard or a billing run item that ended between Allocate
// and its drafts — and marks it released. An allocation a draft still holds
// is never touched, however old: a long-lived draft keeps its allocations
// until it is issued or deleted.
//
// A cycle holds no connection while it calls Orders, so it is safe on a pool
// of one: it claims its rows in one statement (ClaimUnheldAllocations), each
// until claimed_until — the per-row lease that keeps two replicas off the
// same rows — then calls ReleaseAllocations on the pool, then marks the rows
// released in another statement. A call that fails leaves the rows to be
// claimed again once their claim has run out. Orders' release is idempotent:
// one invoiced or released already is left as it is.

const (
	allocationReconcileWorkerName = "invoices.allocation_reconcile"
	allocationReconcileInterval   = 10 * time.Minute
	// allocationReconcileAge is how long an allocation is left to the step
	// that took it: the wizard and a billing run item hold theirs within a
	// request, so one an hour old and held by nothing is never going to be.
	allocationReconcileAge = time.Hour
	// allocationReconcileClaim is how long a cycle's claim on its rows runs:
	// past it, a replica that died mid-cycle leaves them to the next.
	allocationReconcileClaim = 5 * time.Minute
	// allocationReconcileBatch bounds a cycle's claim: one Orders call.
	allocationReconcileBatch = contracts.MaxBillableRows
	// allocationReconcileKeep is how long a released row is kept before it
	// is pruned: nothing reads it again.
	allocationReconcileKeep = 30 * 24 * time.Hour
)

// AllocationReconcileWorker gives back the order allocations this module
// took and no draft holds. It implements worker.Worker; Module.Workers
// registers it always — with Orders off, a cycle does nothing.
type AllocationReconcileWorker struct {
	srv    *server
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*AllocationReconcileWorker)(nil)

// NewAllocationReconcileWorker builds the worker over worker mode's Deps:
// the pool, the clock and Orders' BillableOrders slot.
func NewAllocationReconcileWorker(d module.Deps) *AllocationReconcileWorker {
	srv, err := newServer(d)
	return &AllocationReconcileWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *AllocationReconcileWorker) Name() string { return allocationReconcileWorkerName }

// Interval is the cadence between cycles.
func (w *AllocationReconcileWorker) Interval() time.Duration { return allocationReconcileInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done. A
// failing cycle is logged and the loop goes on.
func (w *AllocationReconcileWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(allocationReconcileInterval)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: an allocation reconcile cycle failed", "worker", allocationReconcileWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle claims the allocations due to be given back, at most one batch,
// releases them in Orders, marks them released, and prunes the rows released
// long ago. It answers how many it released. With Orders off it does
// nothing: there is no one to give them back to.
func (w *AllocationReconcileWorker) RunCycle(ctx context.Context) (int, error) {
	if w.srvErr != nil {
		return 0, w.srvErr
	}
	if w.deps.BillableOrders == nil {
		return 0, nil
	}
	q := store.New(w.deps.Pool)
	now := w.now()
	until := now.Add(allocationReconcileClaim)
	ids, err := q.ClaimUnheldAllocations(ctx, store.ClaimUnheldAllocationsParams{
		ClaimedUntil: &until, Cutoff: now.Add(-allocationReconcileAge), Now: &now, RowLimit: allocationReconcileBatch,
	})
	if err != nil {
		return 0, fmt.Errorf("invoices: claim the order allocations held by nothing: %w", err)
	}
	slices.Sort(ids)
	if len(ids) > 0 {
		if err := w.srv.releaseAllocations(ctx, ids); err != nil {
			return 0, fmt.Errorf("invoices: release %d order allocations held by nothing: %w", len(ids), err)
		}
		released := w.now()
		if err := q.MarkAllocationsReleased(ctx, store.MarkAllocationsReleasedParams{ReleasedAt: &released, Ids: ids}); err != nil {
			return 0, fmt.Errorf("invoices: mark %d order allocations released: %w", len(ids), err)
		}
		w.logger().InfoContext(ctx, "invoices: released order allocations no draft holds",
			"worker", allocationReconcileWorkerName, "count", len(ids))
	}
	cutoff := now.Add(-allocationReconcileKeep)
	if err := q.PruneReleasedAllocations(ctx, &cutoff); err != nil {
		return len(ids), fmt.Errorf("invoices: prune the order allocations released: %w", err)
	}
	return len(ids), nil
}

func (w *AllocationReconcileWorker) now() time.Time { return w.deps.Clock().UTC() }

func (w *AllocationReconcileWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
