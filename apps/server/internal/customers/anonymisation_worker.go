package customers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the anonymisation worker (customers GDPR design D4): once a
// cycle it takes every private person whose scheduled day has come — archived,
// not yet anonymised — and runs each one's anonymisation (anonymisation.go) in
// a transaction of its own. It decides nothing: the day is a person's choice,
// made through PUT /customers/{id}/anonymisation, and the worker only keeps it.
//
// One customer failing — another module's eraser, a deadlock it kept losing —
// rolls that customer back and is logged, and the cycle moves on to the next:
// a cycle that stopped at the first failure would hold every later customer
// hostage to one module's bad day. The failed one is due again next cycle.

const (
	// anonymisationWorkerName is what the runner logs this worker as, in the
	// <module>-<worker> spelling of the two beside it.
	anonymisationWorkerName = "customers-anonymisation"

	// anonymisationLeaseKey is the ASCII string "CUSTANO1" read as a big-endian
	// 64-bit value — its own key, not the registry workers': they do unrelated
	// work, and one holding another's lease would silently stall it.
	anonymisationLeaseKey int64 = 0x43555354414E4F31

	// anonymisationBatch bounds one cycle at fifty customers (design D4). Each
	// is a transaction holding its row and every module's part; fifty keeps a
	// cycle short, and a backlog drains a day at a time.
	anonymisationBatch = 50

	// defaultAnonymisationPoll is what a worker built from a Deps with no
	// Config falls back on — config.go's own default for
	// CUSTOMERS_ANONYMISATION_POLL.
	defaultAnonymisationPoll = 24 * time.Hour
)

// AnonymisationWorker anonymises the private persons whose scheduled day has
// come. It implements worker.Worker.
type AnonymisationWorker struct {
	deps module.Deps
	srv  *server
}

var _ worker.Worker = (*AnonymisationWorker)(nil)

// NewAnonymisationWorker builds the worker over d. d.CustomerPersonalData is
// what module.Workers collected from every module given — in worker mode
// nothing composes, so that is the only place it comes from.
func NewAnonymisationWorker(d module.Deps) *AnonymisationWorker {
	return &AnonymisationWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *AnonymisationWorker) Name() string { return anonymisationWorkerName }

// Interval is the poll cadence between cycles (CUSTOMERS_ANONYMISATION_POLL,
// default 24 hours).
func (w *AnonymisationWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersAnonymisationPoll > 0 {
		return w.deps.Config.CustomersAnonymisationPoll
	}
	return defaultAnonymisationPoll
}

// Run is the worker loop, the registry workers' shape.
func (w *AnonymisationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			// Only the lease or the batch's SELECT fails a cycle, and neither
			// takes anything personal as an argument; one customer's failure is
			// logged, by id, inside the cycle.
			w.logger().Error("anonymisation cycle failed", "worker", anonymisationWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the advisory lease, reporting whether it ran:
// the due customers, at most anonymisationBatch, oldest day first, each in its
// own transaction.
func (w *AnonymisationWorker) RunCycle(ctx context.Context) (bool, error) {
	return w.underLease(ctx, func(ctx context.Context) error {
		today := pgtype.Date{Time: civilDate(w.now()), Valid: true}
		due, err := store.New(w.deps.Pool).DueAnonymisations(ctx, store.DueAnonymisationsParams{Today: today, RowLimit: anonymisationBatch})
		if err != nil {
			return fmt.Errorf("customers: select the customers due for anonymisation: %w", err)
		}
		anonymised, failed := 0, 0
		for _, id := range due {
			if ctx.Err() != nil {
				return nil
			}
			done, err := w.srv.anonymiseCustomer(ctx, id)
			if err != nil {
				if ctx.Err() == nil {
					w.logger().Error("customers: anonymising a customer failed; it is tried again next cycle",
						"worker", anonymisationWorkerName, "customerId", id, "error", err.Error())
				}
				failed++
				continue
			}
			if done {
				anonymised++
			}
		}
		w.logger().Info("anonymisation cycle finished", "worker", anonymisationWorkerName, "anonymised", anonymised, "failed", failed)
		return nil
	})
}

// underLease is the registry workers' lease (peppol_recheck_worker.go, and
// communications/retention.go for why the unlock runs on a context stripped of
// cancellation and why a failed unlock discards the connection), on this
// worker's own key.
func (w *AnonymisationWorker) underLease(ctx context.Context, action func(context.Context) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("customers: acquire a connection for the anonymisation lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, anonymisationLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("customers: take the anonymisation lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the customers anonymisation lease is held by another replica; skipping this cycle",
			"worker", anonymisationWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, anonymisationLeaseKey); err != nil {
			w.logger().Error("releasing the anonymisation lease failed; discarding the connection",
				"worker", anonymisationWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx)
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers.
func (w *AnonymisationWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *AnonymisationWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
