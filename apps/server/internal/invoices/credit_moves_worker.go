package invoices

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices.credit_moves worker (invoices phase 6 design
// I10, plan reading 8): a credit note's surplus moves to the customer's
// credit in a follow-up transaction right after its issue, and a follow-up
// that failed — the process stopped, a lock timed out — has nothing to retry
// it but this. Every five minutes it reads the issued credit notes whose
// original reads below zero open and that have moved nothing
// (PendingCreditNoteMoves), and runs the issue's own follow-up for each
// (moveCreditNoteSurplus), one transaction per credit note, under the
// credit's lock order: the original's customer, then the original. It holds
// no connection across moves — the read is one statement on the pool, each
// move its own transaction — so it is safe on a pool of one. A second
// replica moving the same credit note at once waits on the customer's lock
// and then writes nothing: one move per credit note
// (ux_customer_credits_credit_note).

const (
	creditMovesWorkerName = "invoices.credit_moves"
	creditMovesInterval   = 5 * time.Minute
	// creditMovesBatch bounds a cycle: the rest waits for the next.
	creditMovesBatch = 200
)

// CreditMovesWorker moves the credit notes' surplus the issue's follow-up
// left. It implements worker.Worker; Module.Workers registers it always.
type CreditMovesWorker struct {
	srv    *server
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*CreditMovesWorker)(nil)

// NewCreditMovesWorker builds the worker over worker mode's Deps.
func NewCreditMovesWorker(d module.Deps) *CreditMovesWorker {
	srv, err := newServer(d)
	return &CreditMovesWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *CreditMovesWorker) Name() string { return creditMovesWorkerName }

// Interval is the cadence between cycles.
func (w *CreditMovesWorker) Interval() time.Duration { return creditMovesInterval }

// Run runs a cycle, sleeps the interval, and repeats until ctx is done. A
// failing cycle is logged and the loop goes on.
func (w *CreditMovesWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(creditMovesInterval)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: a credit moves cycle failed", "worker", creditMovesWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle moves the surplus of every credit note left pending, at most one
// batch, and answers how many it ran. A move that fails is logged and the
// cycle goes on with the next; the cycle's error is the first failure's.
func (w *CreditMovesWorker) RunCycle(ctx context.Context) (int, error) {
	if w.srvErr != nil {
		return 0, w.srvErr
	}
	ids, err := store.New(w.deps.Pool).PendingCreditNoteMoves(ctx, creditMovesBatch)
	if err != nil {
		return 0, fmt.Errorf("invoices: read the credit notes whose surplus has not moved: %w", err)
	}
	var first error
	for _, id := range ids {
		if err := w.srv.moveCreditNoteSurplus(ctx, id); err != nil {
			w.logger().WarnContext(ctx, "invoices: a credit note's surplus did not move", "worker", creditMovesWorkerName,
				"credit_note_id", id, "error", err.Error())
			if first == nil {
				first = err
			}
		}
	}
	return len(ids), first
}

func (w *CreditMovesWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
