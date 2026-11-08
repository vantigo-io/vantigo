package invoices

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The live topic of the billing runs screens (live design K40). Events carry
// the run's id only: a screen that hears one refetches through its ordinary
// request, which checks the person's permission.
const (
	// liveBillingRuns is the runs list's and a run's page's topic, keyless:
	// run.changed with the run's id, or without one after a bulk change.
	liveBillingRuns = "invoices.billing-runs"

	eventRunChanged = "run.changed"

	// billingProgressEvery is the least time between two progress
	// notifications of one run (K37): a state change always notifies, an
	// item's outcome at most this often.
	billingProgressEvery = time.Second
)

// liveTopics is the module's LiveTopics. The topic is authorised by the rule
// both GETs of the runs apply: invoices:access.
func liveTopics(module.Deps) []live.Topic {
	return []live.Topic{
		{Kind: liveBillingRuns, Authorize: func(ctx context.Context, key string) error {
			if key != "" || !live.HasPermission(ctx, "invoices:access") {
				return live.ErrForbidden
			}
			return nil
		}},
	}
}

// notifyRun tells the billing runs screens that run id changed. It runs on q,
// the transaction that wrote (live design L6), so it is heard only if that
// commits.
func notifyRun(ctx context.Context, q live.Execer, id int64) error {
	return live.Notify(ctx, q, liveBillingRuns, eventRunChanged, strconv.FormatInt(id, 10))
}

// notifyRunsChanged tells the billing runs screens that runs changed in bulk
// (the worker's sweeps, which do not name the runs): one event without an id,
// never one per run (L6).
func notifyRunsChanged(ctx context.Context, q live.Execer) error {
	return live.Notify(ctx, q, liveBillingRuns, eventRunChanged, "")
}

// runNotifier is the worker's memory of when it last told each run's screen
// something, so an item's progress notifies at most once a second per run
// (K37). A notification suppressed inside the window is sent when it ends, so
// the last progress is never left unheard. Entries are dropped when the run
// finishes and when they are older than the window.
type runNotifier struct {
	mu   sync.Mutex
	runs map[int64]*runThrottle
}

type runThrottle struct {
	last  time.Time
	timer *time.Timer
}

// throttle calls send now, or, inside the run's window, once when it ends.
func (n *runNotifier) throttle(id int64, send func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.runs == nil {
		n.runs = map[int64]*runThrottle{}
	}
	now := time.Now()
	for other, t := range n.runs {
		if other != id && t.timer == nil && now.Sub(t.last) >= billingProgressEvery {
			delete(n.runs, other)
		}
	}
	t := n.runs[id]
	if t == nil {
		n.runs[id] = &runThrottle{last: now}
		send()
		return
	}
	if since := now.Sub(t.last); since >= billingProgressEvery {
		t.last = now
		send()
		return
	}
	if t.timer == nil {
		t.timer = time.AfterFunc(billingProgressEvery-now.Sub(t.last), func() {
			n.mu.Lock()
			if cur := n.runs[id]; cur == t {
				t.timer, t.last = nil, time.Now()
			} else {
				n.mu.Unlock()
				return
			}
			n.mu.Unlock()
			send()
		})
	}
}

// forget drops run id and a trailing event still waiting: it is finished or
// cancelled, and its final state is notified by the caller.
func (n *runNotifier) forget(id int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if t := n.runs[id]; t != nil && t.timer != nil {
		t.timer.Stop()
	}
	delete(n.runs, id)
}

// notifyRun is the worker's state change: always sent, on the pool, with a
// failure logged rather than failing the item — the write is made, and the
// screens' fallback poll repairs a lost event.
func (w *BillingWorker) notifyRun(ctx context.Context, id int64) {
	if err := notifyRun(ctx, w.deps.Pool, id); err != nil {
		w.logger().WarnContext(ctx, "invoices: notify a billing run", "worker", billingWorkerName, "run_id", id, "error", err)
	}
}

// notifyRunProgress is notifyRun for an item's outcome, at most once a second
// per run.
func (w *BillingWorker) notifyRunProgress(ctx context.Context, id int64) {
	ctx = context.WithoutCancel(ctx)
	w.notified.throttle(id, func() { w.notifyRun(ctx, id) })
}

// notifyRunsSwept is notifyRunsChanged after a sweep that changed something.
func (w *BillingWorker) notifyRunsSwept(ctx context.Context) {
	if err := notifyRunsChanged(ctx, w.deps.Pool); err != nil {
		w.logger().WarnContext(ctx, "invoices: notify swept billing runs", "worker", billingWorkerName, "error", err)
	}
}
