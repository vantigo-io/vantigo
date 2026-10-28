package calendar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the calendar-subscriptions worker (calendar feed design D7):
// every five minutes it fetches each subscription that is due — next_fetch_at
// null or past, the never-fetched first and then the longest due, at most
// two hundred a cycle, one at a time — through the same fetch a save and
// Refresh now use, and sets when each is due next. It takes no lease: each
// subscription is claimed in one short statement of its own
// (ClaimDueSubscription, next_fetch_at put subscriptionsClaimHold ahead)
// whose connection goes back to the pool before the fetch, so no connection
// is held while the owner's access is asked or the address fetched — on a
// small pool, a worker holding one while it asked for another starved every
// other and the readiness probe. The claim is what keeps two replicas from
// fetching the same row, and they share the rows due. A person who
// may no longer use Calendar — disabled, locked out, deactivated by the
// directory, or without calendar:access — is asked about once a cycle
// (Access.UserHolds), and their subscriptions are not fetched but put off by
// the poll, so a departed person's calendars are neither fetched for nobody
// nor read again every tick.

const (
	// subscriptionsWorkerName is what the runner logs this worker as.
	subscriptionsWorkerName = "calendar-subscriptions"

	// subscriptionsClaimHold is how long a cycle's claim on a subscription
	// holds: a fetch's budget and room for its record. The record sets
	// when the next fetch is due; a fetch cut short is due once it lapses.
	subscriptionsClaimHold = 5 * time.Minute

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
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.Error("calendar: the subscriptions cycle failed", "worker", subscriptionsWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle fetches the subscriptions due, one claim at a time, and answers
// how many it fetched, whatever each answered — at most
// subscriptionsBatch. A cancelled context stops it between two fetches.
func (w *SubscriptionsWorker) RunCycle(ctx context.Context) (int, error) {
	q := w.srv.queries()
	fetched, seen := 0, 0
	mayFetch := map[uuid.UUID]bool{}
	for ; seen < subscriptionsBatch && ctx.Err() == nil; seen++ {
		now := w.deps.Clock()
		d, err := q.ClaimDueSubscription(ctx, store.ClaimDueSubscriptionParams{
			Now: now, ClaimedUntil: now.Add(subscriptionsClaimHold),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return fetched, fmt.Errorf("calendar: claim a subscription due: %w", err)
		}
		holds, asked := mayFetch[d.UserID]
		if !asked {
			if holds, err = w.srv.accessUserHolds(ctx, d.UserID, "calendar:access"); err != nil {
				return fetched, fmt.Errorf("calendar: check a subscription's owner: %w", err)
			}
			mayFetch[d.UserID] = holds
		}
		if !holds {
			now := w.deps.Clock()
			if err := q.DeferSubscriptionFetch(ctx, store.DeferSubscriptionFetchParams{
				NextFetchAt: nextFetchAt(now, w.srv.subscriptionsPoll(), 0), ID: d.ID,
			}); err != nil {
				return fetched, fmt.Errorf("calendar: put off subscription %d: %w", d.ID, err)
			}
			w.deps.Logger.DebugContext(ctx, "calendar: a subscription's person may no longer use Calendar; it is put off",
				"worker", subscriptionsWorkerName, "subscription_id", d.ID)
			continue
		}
		t := fetchTarget{id: d.ID, url: d.Url, etag: d.Etag, lastModified: d.LastModified}
		if _, err := w.srv.refreshSubscription(ctx, q, t); err != nil && !errors.Is(err, errSubscriptionGone) {
			if ctx.Err() != nil {
				break
			}
			return fetched, err
		}
		fetched++
	}
	if fetched > 0 {
		w.deps.Logger.InfoContext(ctx, "calendar: subscriptions fetched", "worker", subscriptionsWorkerName, "fetched", fetched)
	}
	return fetched, nil
}
