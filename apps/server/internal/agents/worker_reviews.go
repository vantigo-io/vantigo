package agents

// The agents-reviews worker (virtual employees design V20, V22): an item that
// waits too long expires, after a day, or for a Messenger reply 15 minutes before
// the window closes, and an expired item hands its conversation off. The worker
// has no dispatcher, so it records the hand-off and the API process's runner
// finishes it (the note, the assignee), as it does for every hand-off made
// without one.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	reviewsWorkerName     = "agents-reviews"
	reviewsWorkerInterval = time.Minute
	reviewLifetime        = 24 * time.Hour
	// expiryBatch is how many items one pass expires.
	expiryBatch = 100
)

type reviewsWorker struct{ s *server }

func (w *reviewsWorker) Name() string            { return reviewsWorkerName }
func (w *reviewsWorker) Interval() time.Duration { return reviewsWorkerInterval }

func (w *reviewsWorker) Run(ctx context.Context) error {
	t := time.NewTicker(reviewsWorkerInterval)
	defer t.Stop()
	for {
		if err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.s.warn("agents: the reviews worker failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// cycle expires the items that lapsed and hands their conversations off.
func (w *reviewsWorker) cycle(ctx context.Context) error {
	return withTx(ctx, w.s.deps.Pool, func(q *store.Queries) error {
		now := w.s.deps.Clock()
		lapsed, err := q.ListExpiredItems(ctx, store.ListExpiredItemsParams{Now: now, N: expiryBatch})
		if err != nil {
			return fmt.Errorf("agents: find the items that lapsed: %w", err)
		}
		for _, it := range lapsed {
			if err := q.SetItemState(ctx, store.SetItemStateParams{ID: it.ID, State: itemExpired, Now: now}); err != nil {
				return fmt.Errorf("agents: expire an item: %w", err)
			}
			if err := markHandedOff(ctx, q, it.AgentID, it.RoleID, it.ConversationID, handExpired, expirySummary(it), now); err != nil {
				return err
			}
		}
		return nil
	})
}

// expirySummary is the hand-off note of an item that lapsed: what waited, and
// for a reply that was running out of its Messenger window, that.
func expirySummary(it store.AgentsReviewItem) string {
	what := "a change"
	var wrote string
	if it.Kind == kindReply {
		what = "a reply"
		var in storedReply
		if json.Unmarshal(it.Item, &in) == nil && in.Text != "" {
			wrote = " The agent wrote: " + in.Text
		}
	}
	if it.ExpiresAt.Before(it.CreatedAt.Add(reviewLifetime)) {
		return "The reply window was about to close, so " + what + " that waited for review expired." + wrote
	}
	return "Nobody reviewed " + what + " in a day, so it expired." + wrote
}

var _ worker.Worker = (*reviewsWorker)(nil)
