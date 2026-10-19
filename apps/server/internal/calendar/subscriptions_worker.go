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

// This file is the calendar-subscriptions worker (calendar feed design D7):
// every five minutes it fetches each subscription that is due — next_fetch_at
// null or past, the never-fetched first and then the longest due, at most
// two hundred a cycle, one at a time — through the same fetch a save and
// Refresh now use, and sets when each is due next. The cancellations
// worker's shape: one cycle under an advisory lease taken on one pooled
// connection, every query on that connection and never on the pool;
// another replica holding the lease means this one skips the cycle. The due
// list is read in a short transaction of its own and committed before the
// first fetch, so no row lock is held for the minutes a cycle may take; the
// lease is what keeps two replicas from fetching the same row.

const (
	// subscriptionsWorkerName is what the runner logs this worker as.
	subscriptionsWorkerName = "calendar-subscriptions"

	// subscriptionsLeaseKey is the ASCII string "CALSUBS1" read as a
	// big-endian 64-bit value: this worker's own advisory lease.
	subscriptionsLeaseKey int64 = 0x43414C5355425331

	// subscriptionsTick is how often the worker wakes — a constant, not a
	// knob: the poll (CALENDAR_SUBSCRIPTIONS_POLL) is how often each
	// subscription is fetched, and this tick is the most one is late.
	subscriptionsTick = 5 * time.Minute

	// subscriptionsBatch bounds one cycle; a backlog drains over cycles.
	subscriptionsBatch = 200
)

// SubscriptionsWorker fetches the subscriptions that are due. It implements
// worker.Worker.
type SubscriptionsWorker struct {
	deps module.Deps
	// srv is the module's own operations, built as mount builds them: the
	// worker fetches through refreshSubscription, the same fetch and the
	// same three outcomes a click takes, with the same Deps.HTTPTransport.
	srv *server
}

var _ worker.Worker = (*SubscriptionsWorker)(nil)

// NewSubscriptionsWorker builds the worker over d.
func NewSubscriptionsWorker(d module.Deps) *SubscriptionsWorker {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = time.Now
	}
	return &SubscriptionsWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *SubscriptionsWorker) Name() string { return subscriptionsWorkerName }

// Interval is the tick, five minutes.
func (w *SubscriptionsWorker) Interval() time.Duration { return subscriptionsTick }

// Run is the worker loop: a cycle, then the tick. A failure is logged and
// the loop goes on — the runner never restarts a worker.
func (w *SubscriptionsWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.Error("calendar: the subscriptions cycle failed", "worker", subscriptionsWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the lease: it reports whether it ran — false
// when another holder has the lease — and how many subscriptions it
// fetched, whatever each answered. A cancelled context stops it between two
// fetches. The unlock runs on a context stripped of cancellation, so a
// cancelled cycle still gives the lease back; an unlock that fails closes
// the connection, which ends the session and the lock with it.
func (w *SubscriptionsWorker) RunCycle(ctx context.Context) (bool, int, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("calendar: acquire a connection for the subscriptions lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, subscriptionsLeaseKey).Scan(&acquired); err != nil {
		return false, 0, fmt.Errorf("calendar: take the subscriptions lease: %w", err)
	}
	if !acquired {
		w.deps.Logger.DebugContext(ctx, "calendar: the subscriptions lease is held elsewhere; skipping this cycle", "worker", subscriptionsWorkerName)
		return false, 0, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, subscriptionsLeaseKey); err != nil {
			w.deps.Logger.ErrorContext(release, "calendar: releasing the subscriptions lease failed; discarding the connection",
				"worker", subscriptionsWorkerName, "error", err.Error())
			_ = conn.Conn().Close(release)
		}
	}()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return true, 0, fmt.Errorf("calendar: begin the subscriptions due list: %w", err)
	}
	due, err := store.New(tx).ListSubscriptionsDue(ctx, store.ListSubscriptionsDueParams{
		Now: w.deps.Clock(), RowLimit: subscriptionsBatch,
	})
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return true, 0, fmt.Errorf("calendar: list the subscriptions due: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, 0, fmt.Errorf("calendar: commit the subscriptions due list: %w", err)
	}

	q := store.New(conn)
	fetched := 0
	for _, d := range due {
		if ctx.Err() != nil {
			break
		}
		t := fetchTarget{id: d.ID, url: d.Url, etag: d.Etag, lastModified: d.LastModified}
		if _, err := w.srv.refreshSubscription(ctx, q, t); err != nil {
			if ctx.Err() != nil {
				break
			}
			return true, fetched, err
		}
		fetched++
	}
	if fetched > 0 {
		w.deps.Logger.InfoContext(ctx, "calendar: subscriptions fetched", "worker", subscriptionsWorkerName, "fetched", fetched)
	}
	return true, fetched, nil
}
