package invoices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the invoices-ehf-events worker (EHF and KID design D9): the
// provider's event queue drained under an advisory lease, one replica at a
// time (the customers module's shape). Each event is applied to the row it
// names — by its provider reference, or by its idempotency key when the row
// never learned the reference — idempotently and without a row lease: the
// UPDATE only moves a row that is still queued, submitted or unconfirmed, so
// an event for a row the probe already delivered, a duplicate, or an event
// for no row of ours changes nothing. Every event read is acknowledged, which
// is why an installation must have the provider account to itself (reading
// 17): a shared account would lose another system's events.

const (
	// ehfEventsWorkerName is what the runner logs this worker as.
	ehfEventsWorkerName = "invoices-ehf-events"
	// ehfEventsInterval is the poll cadence between drains (D9).
	ehfEventsInterval = 30 * time.Second
	// ehfEventsLeaseKey is the ASCII string "INVEHF1" read as a big-endian
	// integer: this worker's own advisory lock key.
	ehfEventsLeaseKey int64 = 0x494E5645484631
	// ehfEventsPerCycle bounds one drain: a queue that never empties — an
	// acknowledgement the provider keeps ignoring — must not hold the lease
	// for ever.
	ehfEventsPerCycle = 500

	// ehfEventNote is the resolution note of an unconfirmed row an event
	// resolved: resolved_by_user_id stays NULL.
	ehfEventNote = "Resolved by the provider's event."
	// reasonEventFailed is last_error when a failure event names no reason.
	reasonEventFailed = "The access point reported that the document could not be delivered."
)

// EhfEventsWorker drains the access point's event queue. It implements
// worker.Worker; Module.Workers registers it only when INVOICES_EHF_ENABLED
// is on.
type EhfEventsWorker struct {
	srv    *server
	srvErr error
	deps   module.Deps
}

var _ worker.Worker = (*EhfEventsWorker)(nil)

// NewEhfEventsWorker builds the worker over worker mode's Deps.
func NewEhfEventsWorker(d module.Deps) *EhfEventsWorker {
	srv, err := newServer(d)
	return &EhfEventsWorker{srv: srv, srvErr: err, deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *EhfEventsWorker) Name() string { return ehfEventsWorkerName }

// Interval is the poll cadence between drains.
func (w *EhfEventsWorker) Interval() time.Duration { return ehfEventsInterval }

// Run drains the queue, sleeps the interval, and repeats until ctx is done.
func (w *EhfEventsWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(ehfEventsInterval)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("invoices: an EHF events cycle failed", "worker", ehfEventsWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one drain under the advisory lease, answering whether this
// replica held it. Nothing is asked of the provider unless a row awaits an
// event. A NextEvent or AckEvent failure ends the cycle; the next one reads
// the same event again.
func (w *EhfEventsWorker) RunCycle(ctx context.Context) (bool, error) {
	if w.srvErr != nil {
		return false, w.srvErr
	}
	return w.underLease(ctx, func(ctx context.Context) error {
		q := store.New(w.deps.Pool)
		awaiting, err := q.AnyAwaitingEvents(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read whether a transmission awaits an event: %w", err)
		}
		if !awaiting {
			return nil
		}
		ap, err := w.srv.accessPoint(ctx)
		if err != nil {
			w.logger().ErrorContext(ctx, "invoices: the access point cannot be opened; the event queue is not drained",
				"worker", ehfEventsWorkerName, "error", err.Error())
			w.flagRejected(ctx)
			return nil
		}
		for range ehfEventsPerCycle {
			if ctx.Err() != nil {
				return nil
			}
			callCtx, cancel := callContext(ctx)
			event, ok, err := ap.NextEvent(callCtx)
			cancel()
			if err != nil {
				if errors.Is(err, accesspoint.ErrUnauthorized) {
					w.logger().ErrorContext(ctx, "invoices: the access point refused the key; the event queue is not drained",
						"worker", ehfEventsWorkerName)
					w.flagRejected(ctx)
					return nil
				}
				return fmt.Errorf("invoices: read the access point's next event: %w", err)
			}
			w.clearRejected(ctx)
			if !ok {
				return nil
			}
			if err := w.apply(ctx, q, event); err != nil {
				// Not acknowledged: the event is read again next cycle.
				return err
			}
			callCtx, cancel = callContext(ctx)
			err = ap.AckEvent(callCtx, event.ID)
			cancel()
			if err != nil {
				return fmt.Errorf("invoices: acknowledge event %s: %w", event.ID, err)
			}
		}
		return nil
	})
}

// apply moves the row an event names, if it is still in flight.
func (w *EhfEventsWorker) apply(ctx context.Context, q *store.Queries, e accesspoint.Event) error {
	ref := string(e.SubmissionRef)
	var n int64
	var err error
	switch e.State {
	case accesspoint.StateDelivered:
		n, err = q.ApplyEventDelivered(ctx, store.ApplyEventDeliveredParams{
			Now: w.now(), ProviderRef: &ref, MachineNote: ptr(ehfEventNote), IdempotencyKey: e.IdempotencyKey,
		})
	case accesspoint.StateFailed:
		reason := redactReason(e.Reason)
		if reason == "" {
			reason = reasonEventFailed
		}
		n, err = q.ApplyEventFailed(ctx, store.ApplyEventFailedParams{
			Now: w.now(), LastError: &reason, ProviderRef: &ref, MachineNote: ptr(ehfEventNote), IdempotencyKey: e.IdempotencyKey,
		})
	default:
		// submitted, or a state short of an outcome: nothing to record.
		return nil
	}
	if err != nil {
		return fmt.Errorf("invoices: apply event %s: %w", e.ID, err)
	}
	if n == 0 {
		w.logger().InfoContext(ctx, "invoices: an access point event matched no transmission in flight",
			"worker", ehfEventsWorkerName, "event", e.ID, "provider_ref", ref, "state", string(e.State))
		return nil
	}
	w.logger().InfoContext(ctx, "invoices: a transmission moved by an access point event",
		"worker", ehfEventsWorkerName, "event", e.ID, "provider_ref", ref, "state", string(e.State))
	return nil
}

// underLease runs action holding the advisory lock on its own connection,
// the customers re-check worker's shape: the unlock runs on a context
// stripped of cancellation, and a failed unlock discards the connection.
func (w *EhfEventsWorker) underLease(ctx context.Context, action func(context.Context) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("invoices: acquire a connection for the EHF events lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, ehfEventsLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("invoices: take the EHF events lease: %w", err)
	}
	if !acquired {
		w.logger().DebugContext(ctx, "invoices: the EHF events lease is held by another replica; skipping this cycle",
			"worker", ehfEventsWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, ehfEventsLeaseKey); err != nil {
			w.logger().Error("invoices: releasing the EHF events lease failed; discarding the connection",
				"worker", ehfEventsWorkerName, "error", err.Error())
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx)
}

func (w *EhfEventsWorker) flagRejected(ctx context.Context) {
	if err := store.New(w.deps.Pool).MarkAccessPointRejected(ctx, w.now()); err != nil {
		w.logger().ErrorContext(ctx, "invoices: flag the access point key as rejected", "worker", ehfEventsWorkerName, "error", err.Error())
	}
}

func (w *EhfEventsWorker) clearRejected(ctx context.Context) {
	if err := store.New(w.deps.Pool).ClearAccessPointRejected(ctx); err != nil {
		w.logger().WarnContext(ctx, "invoices: clear the access point key's refusal", "worker", ehfEventsWorkerName, "error", err.Error())
	}
}

func (w *EhfEventsWorker) now() time.Time { return w.deps.Clock().UTC() }

func (w *EhfEventsWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
