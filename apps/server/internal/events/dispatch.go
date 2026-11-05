package events

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

const (
	// DispatcherName is the dispatcher's worker name (orders design O3).
	DispatcherName = "events.dispatch"
	// dispatchInterval is how often the dispatcher polls.
	dispatchInterval = 5 * time.Second
	// dispatchBatch bounds the events fanned out and the deliveries claimed
	// per pass.
	dispatchBatch = 100
	// leaseDuration is how long a claim holds a delivery. A delivery whose
	// lease ran out — its dispatcher died mid-handle — is claimable again.
	leaseDuration = 2 * time.Minute
	// handleTimeout bounds one handler's run, well inside the lease that
	// protects it.
	handleTimeout = leaseDuration / 2
	// maxAttempts is how many failures make a delivery dead.
	maxAttempts = 8
	// lastErrorLimit is platform.event_deliveries.last_error's length.
	lastErrorLimit = 2000
	// retention is how long after its creation an event fanned out whose
	// deliveries are all done is kept.
	retention = 30 * 24 * time.Hour
	// pruneBatch bounds the events one pass deletes.
	pruneBatch = 1000
)

// backoff is the wait after the n-th failure (n from 1), the last step
// repeating (O3).
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour}

func backoffAfter(attempts int) time.Duration {
	return backoff[min(attempts, len(backoff))-1]
}

// Dispatcher is the outbox's one leased worker, events.dispatch (orders
// design O3). Each pass it fans every event not yet fanned out into one
// platform.event_deliveries row per handler subscribed now, marking the
// event fanned out in the same statement — a handler composed later gets no
// deliveries for events fanned out before it. It then claims due deliveries
// under a lease — in event id order per handler, aggregate and aggregate
// id, an earlier delivery not yet done, or an earlier event of the same
// aggregate not yet fanned out, holding a later one back — and handles each
// on its own transaction, marking it done on that transaction. A failure of
// the handler is recorded on a separate short transaction, with backoff,
// and the eighth makes the delivery dead until the operator retries or
// skips it (O4). Events fanned out and created more than 30 days ago whose
// deliveries are all done are pruned.
//
// It never holds one pooled connection while it asks for another: every
// statement and every transaction takes its connection, uses it and gives
// it back before the next is asked for, so it runs on a pool of one.
type Dispatcher struct {
	pool     *pgxpool.Pool
	handlers map[string]Handler
	logger   *slog.Logger
	clock    func() time.Time
}

var _ worker.Worker = (*Dispatcher)(nil)

// NewDispatcher is the dispatcher over pool for handlers, whose names the
// caller has already checked are unique (module.EventHandlers does). A nil
// clock is time.Now.
func NewDispatcher(pool *pgxpool.Pool, handlers []Handler, logger *slog.Logger, clock func() time.Time) *Dispatcher {
	byName := make(map[string]Handler, len(handlers))
	for _, h := range handlers {
		byName[h.Name] = h
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if clock == nil {
		clock = time.Now
	}
	return &Dispatcher{pool: pool, handlers: byName, logger: logger, clock: clock}
}

// Name identifies the worker in the runner's logs.
func (d *Dispatcher) Name() string { return DispatcherName }

// Interval is the poll cadence.
func (d *Dispatcher) Interval() time.Duration { return dispatchInterval }

// Run passes until ctx is done. A failed pass is logged and the loop goes on.
func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(dispatchInterval)
	defer ticker.Stop()
	for {
		if err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
			d.logger.Error("events dispatch failed", "worker", DispatcherName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce is one pass: fan out, then claim and handle until nothing due is
// left, then prune.
func (d *Dispatcher) RunOnce(ctx context.Context) error {
	if err := d.fanOut(ctx); err != nil {
		return err
	}
	if err := d.warnUnknown(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		claimed, err := d.claim(ctx)
		if err != nil {
			return err
		}
		if len(claimed) == 0 {
			break
		}
		for _, c := range claimed {
			if err := d.handle(ctx, c); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.prune(ctx)
}

// subscriptions are the handlers' (handler, name, version) triples as three
// parallel arrays, for unnest.
func (d *Dispatcher) subscriptions() (handlers, names []string, versions []int32) {
	for _, h := range d.handlers {
		for _, ref := range h.Events {
			handlers = append(handlers, h.Name)
			names = append(names, ref.Name)
			versions = append(versions, int32(ref.Version))
		}
	}
	return handlers, names, versions
}

func (d *Dispatcher) handlerNames() []string {
	names := make([]string, 0, len(d.handlers))
	for name := range d.handlers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// fanOutSQL fans out up to $5 events not yet fanned out, the lowest ids
// first: a pending delivery for every handler subscribed to the event's name
// and version, and the event marked fanned out, in one statement, so either
// both commit or neither does. An event no handler takes gets no delivery
// and is marked all the same. A handler composed later gets nothing for an
// event already marked: there is no backfill.
//
// FOR UPDATE SKIP LOCKED lets two dispatchers fan out at once, each its own
// events. One may so commit a later event's deliveries before the other
// commits an earlier event's of the same aggregate; the claim then holds the
// later one back while the earlier event is not yet fanned out (claimSQL).
const fanOutSQL = `WITH batch AS (
    SELECT e.id, e.name, e.version, e.aggregate, e.aggregate_id FROM platform.events e
    WHERE e.fanned_out_at IS NULL
    ORDER BY e.id
    LIMIT $5
    FOR UPDATE SKIP LOCKED
), marked AS (
    UPDATE platform.events e SET fanned_out_at = $4
    FROM batch b WHERE e.id = b.id
    RETURNING e.id
), delivered AS (
    INSERT INTO platform.event_deliveries
        (event_id, handler, aggregate, aggregate_id, state, attempts, next_attempt_at, created_at, updated_at)
    SELECT b.id, s.handler, b.aggregate, b.aggregate_id, 'pending', 0, $4, $4, $4
    FROM batch b
    JOIN unnest($1::text[], $2::text[], $3::int[]) AS s(handler, name, version)
      ON s.name = b.name AND s.version = b.version
    ON CONFLICT (event_id, handler) DO NOTHING
)
SELECT count(*) FROM marked`

// fanOut fans out batches until one comes back short. It runs with no
// handler composed too, so the events of such a time are marked, and pruned,
// rather than delivered to a handler composed later.
func (d *Dispatcher) fanOut(ctx context.Context) error {
	handlers, names, versions := d.subscriptions()
	now := d.clock()
	for ctx.Err() == nil {
		var marked int
		if err := d.pool.QueryRow(ctx, fanOutSQL, handlers, names, versions, now, dispatchBatch).Scan(&marked); err != nil {
			return fmt.Errorf("events: fan out: %w", err)
		}
		if marked < dispatchBatch {
			return nil
		}
	}
	return ctx.Err()
}

// unknownSQL names the handlers of undone deliveries that no handler of the
// composition answers to: a handler removed from the code. Their rows stay
// as they are, for the operator to skip.
const unknownSQL = `SELECT DISTINCT handler FROM platform.event_deliveries
WHERE state IN ('pending', 'failed') AND NOT (handler = ANY($1::text[]))
ORDER BY handler`

func (d *Dispatcher) warnUnknown(ctx context.Context) error {
	rows, err := d.pool.Query(ctx, unknownSQL, d.handlerNames())
	if err != nil {
		return fmt.Errorf("events: read unknown handlers: %w", err)
	}
	unknown, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("events: read unknown handlers: %w", err)
	}
	if len(unknown) > 0 {
		d.logger.Warn("events deliveries wait for handlers that are not composed",
			"worker", DispatcherName, "handlers", unknown)
	}
	return nil
}

// claimSQL leases up to $2 due deliveries of composed handlers: pending or
// failed, due, not leased, with no earlier delivery for the same handler,
// aggregate and aggregate id that is not done — a failed or dead one holds
// the later ones back (O3) — and no earlier event of the same aggregate and
// aggregate id not yet fanned out, whose deliveries another dispatcher may
// be about to commit (fanOutSQL). SKIP LOCKED lets two dispatchers claim at
// once without one waiting on the other's rows; the lease keeps them apart
// afterwards.
const claimSQL = `WITH due AS (
    SELECT d.id FROM platform.event_deliveries d
    WHERE d.state IN ('pending', 'failed')
      AND d.next_attempt_at <= $1
      AND (d.lease_until IS NULL OR d.lease_until <= $1)
      AND d.handler = ANY($5::text[])
      AND NOT EXISTS (
          SELECT 1 FROM platform.event_deliveries p
          WHERE p.handler = d.handler AND p.aggregate = d.aggregate AND p.aggregate_id = d.aggregate_id
            AND p.event_id < d.event_id AND p.state <> 'done')
      AND NOT EXISTS (
          SELECT 1 FROM platform.events e
          WHERE e.fanned_out_at IS NULL AND e.id < d.event_id
            AND e.aggregate = d.aggregate AND e.aggregate_id = d.aggregate_id)
    ORDER BY d.event_id, d.id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
UPDATE platform.event_deliveries d
SET lease_id = $3, lease_until = $4, updated_at = $1
FROM due WHERE d.id = due.id
RETURNING d.id, d.handler, d.attempts`

type claimed struct {
	id       int64
	handler  string
	attempts int
	leaseID  string
}

func (d *Dispatcher) claim(ctx context.Context) ([]claimed, error) {
	now := d.clock()
	leaseID := uuid.NewString()
	rows, err := d.pool.Query(ctx, claimSQL, now, dispatchBatch, leaseID, now.Add(leaseDuration), d.handlerNames())
	if err != nil {
		return nil, fmt.Errorf("events: claim deliveries: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (claimed, error) {
		c := claimed{leaseID: leaseID}
		err := row.Scan(&c.id, &c.handler, &c.attempts)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("events: claim deliveries: %w", err)
	}
	slices.SortFunc(out, func(a, b claimed) int { return cmp.Compare(a.id, b.id) })
	return out, nil
}

// leasedSQL renews a claimed delivery's lease from now, as its handling
// starts — a delivery late in a batch is not left with what remains of the
// claim's lease — and reads its event under the delivery's row lock, only
// while the claim's lease still holds it.
const leasedSQL = `UPDATE platform.event_deliveries d SET lease_until = $3
FROM platform.events e
WHERE e.id = d.event_id AND d.id = $1 AND d.lease_id = $2 AND d.state IN ('pending', 'failed')
RETURNING e.id, e.name, e.version, e.module, e.aggregate_id, e.payload, e.occurred_at`

const doneSQL = `UPDATE platform.event_deliveries
SET state = 'done', completed_at = $2, lease_id = NULL, lease_until = NULL, updated_at = $2
WHERE id = $1`

// errLeaseLost is a delivery whose lease another claim took — or the
// operator reset — before its transaction began. It is no failure.
var errLeaseLost = errors.New("events: the lease was lost")

// handle runs one claimed delivery on its own transaction and marks it done
// there. A handler error rolls that back; the failure is then recorded on a
// separate statement, after the transaction gave its connection back. It
// returns an error only for the database failing, never for the handler.
//
// Only a failure after the lease was re-checked is an attempt: the handler's
// error, panic or timeout, or the database failing under it (its commit,
// say). Failing to begin or to re-read the delivery — the database down —
// counts nothing: the pass ends with the error, and the delivery is claimed
// again once its lease runs out.
func (d *Dispatcher) handle(ctx context.Context, c claimed) error {
	h := d.handlers[c.handler]
	leased := false
	handleErr := db.WithTx(ctx, d.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var (
			ev      Delivered
			version int16
		)
		err := tx.QueryRow(ctx, leasedSQL, c.id, c.leaseID, d.clock().Add(leaseDuration)).
			Scan(&ev.ID, &ev.Name, &version, &ev.Module, &ev.AggregateID, &ev.Payload, &ev.OccurredAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return errLeaseLost
		}
		if err != nil {
			return fmt.Errorf("events: read delivery %d: %w", c.id, err)
		}
		leased = true
		ev.Version = int(version)
		if err := runHandler(ctx, h, tx, ev); err != nil {
			return handlerError{err}
		}
		if _, err := tx.Exec(ctx, doneSQL, c.id, d.clock()); err != nil {
			return fmt.Errorf("events: mark delivery %d done: %w", c.id, err)
		}
		return nil
	})
	var failed handlerError
	switch {
	case handleErr == nil, errors.Is(handleErr, errLeaseLost):
		return nil
	case ctx.Err() != nil:
		// Shutting down is no attempt: the lease runs out and the delivery
		// is claimed again.
		return ctx.Err()
	case errors.As(handleErr, &failed):
		return d.fail(ctx, c, failed.err)
	case !leased:
		return handleErr
	default:
		// The database failed under the handler — its commit, say. That is
		// an attempt too: a handler whose commit always fails must die, not
		// loop.
		return d.fail(ctx, c, handleErr)
	}
}

// handlerError marks an error as the handler's own.
type handlerError struct{ err error }

func (e handlerError) Error() string { return e.err.Error() }

// runHandler calls h within handleTimeout, a panic becoming an error.
func runHandler(ctx context.Context, h Handler, tx pgx.Tx, ev Delivered) (err error) {
	ctx, cancel := context.WithTimeout(ctx, handleTimeout)
	defer cancel()
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("handler panicked: %v", rec)
		}
	}()
	return h.Handle(ctx, tx, ev)
}

const failSQL = `UPDATE platform.event_deliveries
SET attempts = $3, state = $4, next_attempt_at = $5, last_error = $6,
    lease_id = NULL, lease_until = NULL, updated_at = $7
WHERE id = $1 AND lease_id = $2`

// fail records one failed attempt, with backoff, or the delivery's death on
// the eighth — only while the claim's lease still holds it.
func (d *Dispatcher) fail(ctx context.Context, c claimed, cause error) error {
	now := d.clock()
	attempts := c.attempts + 1
	state := "failed"
	if attempts >= maxAttempts {
		state = "dead"
	}
	msg := truncate(cause.Error(), lastErrorLimit)
	tag, err := d.pool.Exec(ctx, failSQL, c.id, c.leaseID, attempts, state, now.Add(backoffAfter(attempts)), msg, now)
	if err != nil {
		return fmt.Errorf("events: record delivery %d's failure: %w", c.id, err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if state == "dead" {
		d.logger.Error("events delivery is dead",
			"worker", DispatcherName, "delivery", c.id, "handler", c.handler, "attempts", attempts, "error", msg)
	} else {
		d.logger.Warn("events delivery failed",
			"worker", DispatcherName, "delivery", c.id, "handler", c.handler, "attempts", attempts, "error", msg)
	}
	return nil
}

// truncate cuts s to at most n characters, on a rune boundary.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// pruneSQL deletes events created before the cutoff and fanned out none of
// whose deliveries is undone — every one done, or none at all; their
// deliveries cascade. It keys on created_at, the outbox's own clock, not the
// publisher's occurred_at, and never deletes an event not yet fanned out.
const pruneSQL = `DELETE FROM platform.events
WHERE id IN (
    SELECT e.id FROM platform.events e
    WHERE e.created_at < $1
      AND e.fanned_out_at IS NOT NULL
      AND NOT EXISTS (
          SELECT 1 FROM platform.event_deliveries d WHERE d.event_id = e.id AND d.state <> 'done')
    ORDER BY e.created_at
    LIMIT $2)`

func (d *Dispatcher) prune(ctx context.Context) error {
	if _, err := d.pool.Exec(ctx, pruneSQL, d.clock().Add(-retention), pruneBatch); err != nil {
		return fmt.Errorf("events: prune: %w", err)
	}
	return nil
}
