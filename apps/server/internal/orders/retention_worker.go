package orders

// The orders.retention worker (orders design O54): once a day it works
// through the erasures EraseCustomerData recorded (orders.erasures) and
// anonymises each erased person's confirmed orders whose retention has run —
// kept under bokføringsloven § 13 until 3 years and 6 months after the end of
// the financial year (the calendar year, in the business time zone) in which
// the order was cancelled, or was done (confirmed, delivered and invoiced;
// the order's last change stands for when it became done). Done is
// confirmed with every product line's cancelled and invoiced covering what
// was ordered (isDone), so an order whose remainder was cancelled whole is
// done too. It then deletes the stored PDFs of the person's anonymised quotes
// and orders — they print the person — and marks an erasure with nothing
// left to wait for complete.
//
// One replica runs a cycle: the cycle takes an advisory lease on a pooled
// connection of its own and runs every statement on that connection, so it
// never asks the pool for a second one while it holds one — safe on a pool of
// one. The business zone is read before the lease is taken.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

const (
	retentionWorkerName   = "orders.retention"
	retentionInterval     = 24 * time.Hour
	retentionBatch        = 100
	retentionLeaseKey     = int64(0x4f52445252455431) // "ORDRRET1"
	retentionStepDeadline = 5 * time.Minute
)

// retentionCutoff is the moment before which an order's anchor — its
// cancellation, or its last change once done — has had its retention run out
// by today: the start of the calendar year after the last financial year
// whose 3 years and 6 months have passed.
func retentionCutoff(now time.Time, zone *time.Location) time.Time {
	local := now.In(zone)
	last := local.Year() - 5
	if !local.Before(time.Date(local.Year(), time.July, 1, 0, 0, 0, 0, zone)) {
		last = local.Year() - 4
	}
	return time.Date(last+1, time.January, 1, 0, 0, 0, 0, zone)
}

// retentionWorker anonymises the kept orders of erased people.
type retentionWorker struct{ s *server }

func (w *retentionWorker) Name() string            { return retentionWorkerName }
func (w *retentionWorker) Interval() time.Duration { return retentionInterval }

func (w *retentionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	for {
		if _, _, err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.s.deps.Logger.ErrorContext(ctx, "orders: the retention cycle failed", "worker", retentionWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// cycle is one pass under the lease. It answers whether it won the lease and
// how many orders it anonymised.
func (w *retentionWorker) cycle(ctx context.Context) (bool, int64, error) {
	s := w.s
	zone, err := s.businessZone(ctx)
	if err != nil {
		return false, 0, err
	}
	now := s.deps.Clock()
	cutoff := retentionCutoff(now, zone)

	ctx, cancel := context.WithTimeout(ctx, retentionStepDeadline)
	defer cancel()
	conn, err := s.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("orders: acquire a connection for the retention lease: %w", err)
	}
	defer conn.Release()
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, retentionLeaseKey).Scan(&acquired); err != nil {
		return false, 0, fmt.Errorf("orders: take the retention lease: %w", err)
	}
	if !acquired {
		return false, 0, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, retentionLeaseKey); err != nil {
			// A session that still holds the lease must not go back into the pool.
			_ = conn.Conn().Close(release)
		}
	}()
	customers, err := store.New(conn).OpenErasures(ctx, retentionBatch)
	if err != nil {
		return true, 0, fmt.Errorf("orders: read the open erasures: %w", err)
	}
	var total int64
	for _, id := range customers {
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			q := store.New(tx)
			if err := q.LockCustomerOrders(ctx, id); err != nil {
				return fmt.Errorf("orders: lock customer %d's orders: %w", id, err)
			}
			n, err := q.AnonymiseCustomerOrders(ctx, store.AnonymiseCustomerOrdersParams{CustomerID: id, Now: &now, Cutoff: &cutoff})
			if err != nil {
				return fmt.Errorf("orders: anonymise customer %d's kept orders: %w", id, err)
			}
			if err := q.LockCustomerSubscriptions(ctx, id); err != nil {
				return fmt.Errorf("orders: lock customer %d's subscriptions: %w", id, err)
			}
			if _, err := q.AnonymiseCustomerSubscriptions(ctx, store.AnonymiseCustomerSubscriptionsParams{CustomerID: id, Now: &now, Cutoff: &cutoff}); err != nil {
				return fmt.Errorf("orders: anonymise customer %d's kept subscriptions: %w", id, err)
			}
			if _, err := q.BlankCustomerMailings(ctx, id); err != nil {
				return fmt.Errorf("orders: blank customer %d's sends: %w", id, err)
			}
			total += n
			return nil
		})
		if err != nil {
			return true, total, err
		}
		if err := w.deleteErasedDocuments(ctx, conn, id); err != nil {
			return true, total, err
		}
		if _, err := store.New(conn).CompleteErasure(ctx, store.CompleteErasureParams{CustomerID: id, Now: &now}); err != nil {
			return true, total, fmt.Errorf("orders: complete customer %d's erasure: %w", id, err)
		}
	}
	return true, total, nil
}

// deleteErasedDocuments deletes the stored PDFs of a customer's anonymised
// quotes and orders (O29, O54): each object from the store first, outside
// any transaction, then its row — its sends unlinked, and kept — so a cycle
// cut short between the two deletes the object again, which succeeds, next
// time.
func (w *retentionWorker) deleteErasedDocuments(ctx context.Context, conn *pgxpool.Conn, customerID int32) error {
	docs, err := store.New(conn).ErasedDocuments(ctx, customerID)
	if err != nil {
		return fmt.Errorf("orders: read customer %d's erased PDFs: %w", customerID, err)
	}
	for _, d := range docs {
		if err := w.s.objectDelete(ctx, d.StorageKey); err != nil {
			return fmt.Errorf("orders: delete stored PDF %d: %w", d.ID, err)
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			q := store.New(tx)
			if err := q.DetachDocumentMailings(ctx, &d.ID); err != nil {
				return fmt.Errorf("orders: unlink stored PDF %d's sends: %w", d.ID, err)
			}
			if _, err := q.DeleteDocumentRow(ctx, d.ID); err != nil {
				return fmt.Errorf("orders: delete stored PDF %d's row: %w", d.ID, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
