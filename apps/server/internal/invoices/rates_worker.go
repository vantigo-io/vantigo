package invoices

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-rates worker (invoices phase 6 design I18; plan
// C2 and reading 11): every 15 minutes it reads the currencies turned on and
// fetches from Norges Bank those not fetched since the latest business day's
// 16:30 in the business time zone — before it, nothing is due for the day.
// Each currency's fetch row is claimed under a five-minute lease, one
// statement on the pool (FOR NO KEY UPDATE SKIP LOCKED); the fetch runs with
// no transaction open and no connection held; a short transaction per
// currency then stores the rates and ends the claim. So it never holds a
// connection while it waits on Norges Bank, and runs on a pool of one.

const (
	ratesWorkerName = "invoices-rates"
	ratesWorkerTick = 15 * time.Minute
	// rateFetchDefaultLease is longer than one request's whole budget.
	rateFetchDefaultLease = 5 * time.Minute
)

// rateFetchLease is the lease a claim takes. A test shortens it.
var rateFetchLease = rateFetchDefaultLease

// newLeaseID is a claim's lease, a uuid without its hyphens.
func newLeaseID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

// RatesWorker fetches Norges Bank's rates of the currencies turned on. It
// implements worker.Worker; Module.Workers registers it only while
// INVOICES_RATES_ENABLED is on.
type RatesWorker struct {
	srv    *server
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*RatesWorker)(nil)

// NewRatesWorker builds the worker over worker mode's Deps.
func NewRatesWorker(d module.Deps) *RatesWorker {
	srv, err := newServer(d)
	return &RatesWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *RatesWorker) Name() string { return ratesWorkerName }

// Interval is the cadence between cycles.
func (w *RatesWorker) Interval() time.Duration { return ratesWorkerTick }

// Run runs a cycle, sleeps the tick, and repeats until ctx is done. A failing
// cycle is logged and the loop goes on.
func (w *RatesWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(ratesWorkerTick)
	defer ticker.Stop()
	for {
		if err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: an exchange rates cycle failed", "worker", ratesWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle fetches every currency due once. Norges Bank's failure is the
// currency's last_error, not the cycle's; the error answered is the worker's
// own.
func (w *RatesWorker) RunCycle(ctx context.Context) error {
	if w.srvErr != nil {
		return w.srvErr
	}
	loc, err := w.srv.businessZone(ctx)
	if err != nil {
		return err
	}
	enabled, err := enabledCurrencies(ctx, store.New(w.deps.Pool))
	if err != nil || len(enabled) == 0 {
		return err
	}
	return w.srv.ratesCycle(ctx, enabled, w.deps.Clock().In(loc), false)
}

func (w *RatesWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
