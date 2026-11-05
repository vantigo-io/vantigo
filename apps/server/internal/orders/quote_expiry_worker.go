package orders

// The orders.quote_expiry worker (orders design O20): once a day every sent
// quote whose valid-until has passed — before today in the business time
// zone — moves to expired. It is one statement on the pool, so it holds no
// connection while asking for another (safe on a pool of one) and two
// replicas running it at once do the same thing once. It publishes no event:
// nothing consumes an expiry in this phase, and the customer's timeline shows
// what was sent and answered. An expired quote can still be accepted (O20).

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

const (
	quoteExpiryWorkerName = "orders.quote_expiry"
	quoteExpiryInterval   = 24 * time.Hour
)

// quoteExpiryWorker expires the sent quotes past their valid-until.
type quoteExpiryWorker struct{ s *server }

func (w *quoteExpiryWorker) Name() string            { return quoteExpiryWorkerName }
func (w *quoteExpiryWorker) Interval() time.Duration { return quoteExpiryInterval }

func (w *quoteExpiryWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(quoteExpiryInterval)
	defer ticker.Stop()
	for {
		if _, err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.s.deps.Logger.ErrorContext(ctx, "orders: the quote expiry failed", "worker", quoteExpiryWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// cycle expires what is due and answers how many quotes it expired.
func (w *quoteExpiryWorker) cycle(ctx context.Context) (int64, error) {
	s := w.s
	zone, err := s.businessZone(ctx)
	if err != nil {
		return 0, err
	}
	now := s.deps.Clock()
	n, err := store.New(s.deps.Pool).ExpireQuotes(ctx, store.ExpireQuotesParams{
		Now: now, Today: pgtype.Date{Time: dayOf(now, zone), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("orders: expire the sent quotes: %w", err)
	}
	return n, nil
}
