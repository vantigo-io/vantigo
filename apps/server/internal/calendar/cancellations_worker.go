package calendar

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the calendar-cancellations worker (calendar invitations
// design D6): it erases a cancelled appointment once its last day has passed
// — a single one's end, a bounded series' last occurrence's day, and an
// unbounded series ninety days after it was cancelled — its attendees and
// exceptions with it. The RSVP page for an erased attendee's token is then
// the 404 of a token nobody holds. Staff's shape, with nothing else to do:
// one cycle a poll under an advisory lease taken on one pooled connection,
// every query on that connection and never on the pool, so a cycle never
// waits for a second connection while it holds the first; another replica
// holding the lease means this one skips the cycle.

const (
	// cancellationsWorkerName is what the runner logs this worker as.
	cancellationsWorkerName = "calendar-cancellations"

	// cancellationsLeaseKey is the ASCII string "CALCANC1" read as a
	// big-endian 64-bit value: this worker's own advisory lease, apart from
	// every other module's.
	cancellationsLeaseKey int64 = 0x43414C43414E4331

	// cancellationsBatch bounds one cycle; a backlog drains over cycles.
	cancellationsBatch = 200

	// unboundedCancelledFor is how long a cancelled series that never ends
	// is kept after it was cancelled.
	unboundedCancelledFor = 90 * 24 * time.Hour

	// defaultWorkersPoll is the poll of a worker built from Deps without a
	// Config — config.go's own default for CALENDAR_WORKERS_POLL.
	defaultWorkersPoll = time.Hour
)

// workers is the module's background workers: calendar-cancellations unless
// CALENDAR_CANCELLATIONS_ENABLED=0, and calendar-subscriptions unless
// CALENDAR_SUBSCRIPTIONS_ENABLED=0. A Deps with no Config cannot say, and
// gets none.
func workers(d module.Deps) []worker.Worker {
	if d.Config == nil {
		return nil
	}
	var out []worker.Worker
	if d.Config.CalendarCancellationsEnabled {
		out = append(out, NewCancellationsWorker(d))
	}
	if d.Config.CalendarSubscriptionsEnabled {
		out = append(out, NewSubscriptionsWorker(d))
	}
	return out
}

// CancellationsWorker erases the cancelled appointments that are due. It
// implements worker.Worker.
type CancellationsWorker struct {
	deps module.Deps
}

var _ worker.Worker = (*CancellationsWorker)(nil)

// NewCancellationsWorker builds the worker over d.
func NewCancellationsWorker(d module.Deps) *CancellationsWorker {
	return &CancellationsWorker{deps: d}
}

// Name identifies this worker in the runner's logs.
func (w *CancellationsWorker) Name() string { return cancellationsWorkerName }

// Interval is the poll (CALENDAR_WORKERS_POLL, default an hour).
func (w *CancellationsWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CalendarWorkersPoll > 0 {
		return w.deps.Config.CalendarWorkersPoll
	}
	return defaultWorkersPoll
}

// Run is the worker loop: a cycle, then the poll. A failure is logged and the
// loop goes on — the runner never restarts a worker.
func (w *CancellationsWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("calendar: the cancellations cycle failed", "worker", cancellationsWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the lease: it reports whether it ran — false
// when another holder has the lease — and how many appointments it erased.
// The unlock runs on a context stripped of cancellation, so a cancelled cycle
// still gives the lease back; an unlock that fails closes the connection,
// which ends the session and the lock with it.
func (w *CancellationsWorker) RunCycle(ctx context.Context) (bool, int64, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("calendar: acquire a connection for the cancellations lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, cancellationsLeaseKey).Scan(&acquired); err != nil {
		return false, 0, fmt.Errorf("calendar: take the cancellations lease: %w", err)
	}
	if !acquired {
		w.logger().DebugContext(ctx, "calendar: the cancellations lease is held elsewhere; skipping this cycle", "worker", cancellationsWorkerName)
		return false, 0, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, cancellationsLeaseKey); err != nil {
			w.logger().ErrorContext(release, "calendar: releasing the cancellations lease failed; discarding the connection",
				"worker", cancellationsWorkerName, "error", err.Error())
			_ = conn.Conn().Close(release)
		}
	}()

	q := store.New(conn)
	now := w.now()
	ids, err := q.ListCancelledDue(ctx, store.ListCancelledDueParams{
		Now: now, UnboundedCancelledBefore: now.Add(-unboundedCancelledFor), RowLimit: cancellationsBatch,
	})
	if err != nil {
		return true, 0, fmt.Errorf("calendar: list the cancelled appointments due: %w", err)
	}
	if len(ids) == 0 {
		return true, 0, nil
	}
	n, err := q.DeleteCancelled(ctx, ids)
	if err != nil {
		return true, 0, fmt.Errorf("calendar: erase %d cancelled appointments: %w", len(ids), err)
	}
	w.logger().InfoContext(ctx, "calendar: cancelled appointments erased", "worker", cancellationsWorkerName, "erased", n)
	return true, n, nil
}

// now is the installation's clock, the wall clock when a bare Deps has none.
func (w *CancellationsWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock()
	}
	return time.Now()
}

// logger is the module's logger, or the process's when a bare Deps has none.
func (w *CancellationsWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
