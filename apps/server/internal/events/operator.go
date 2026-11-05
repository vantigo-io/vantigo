package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Operator is the operator's view of the outbox (orders design O4): the
// stuck deliveries, and their retry and skip. The management listener serves
// it; no tenant permission reaches it.
type Operator struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

// NewOperator is the operator's view over pool. A nil clock is time.Now.
func NewOperator(pool *pgxpool.Pool, clock func() time.Time) *Operator {
	if clock == nil {
		clock = time.Now
	}
	return &Operator{pool: pool, clock: clock}
}

// DeliveryRecord is one delivery as the operator sees it.
type DeliveryRecord struct {
	ID            int64
	EventID       int64
	Event         string
	Version       int
	Module        string
	AggregateID   string
	Handler       string
	State         string
	Attempts      int
	LastError     *string
	Note          *string
	NextAttemptAt time.Time
	OccurredAt    time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ListLimit is the most deliveries List returns.
const ListLimit = 200

var (
	// ErrDeliveryNotFound is a delivery id that names no delivery.
	ErrDeliveryNotFound = errors.New("events: no such delivery")
	// ErrDeliveryState is a retry of a delivery that is not failed or dead,
	// or a skip of one already done.
	ErrDeliveryState = errors.New("events: the delivery is not in a state that allows this")
)

const listSQL = `SELECT d.id, d.event_id, e.name, e.version, e.module, d.aggregate_id, d.handler, d.state,
       d.attempts, d.last_error, d.note, d.next_attempt_at, e.occurred_at, d.created_at, d.updated_at
FROM platform.event_deliveries d
JOIN platform.events e ON e.id = d.event_id
WHERE d.state = ANY($1::text[])
ORDER BY d.updated_at DESC, d.id DESC
LIMIT $2`

// List is the deliveries in state — "dead" or "failed", or both when state
// is empty — the latest changed first, at most ListLimit.
func (o *Operator) List(ctx context.Context, state string) ([]DeliveryRecord, error) {
	states := []string{"dead", "failed"}
	switch state {
	case "":
	case "dead", "failed":
		states = []string{state}
	default:
		return nil, fmt.Errorf("events: list deliveries in %q: %w", state, ErrDeliveryState)
	}
	rows, err := o.pool.Query(ctx, listSQL, states, ListLimit)
	if err != nil {
		return nil, fmt.Errorf("events: list deliveries: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DeliveryRecord, error) {
		var (
			r       DeliveryRecord
			version int16
		)
		err := row.Scan(&r.ID, &r.EventID, &r.Event, &version, &r.Module, &r.AggregateID, &r.Handler, &r.State,
			&r.Attempts, &r.LastError, &r.Note, &r.NextAttemptAt, &r.OccurredAt, &r.CreatedAt, &r.UpdatedAt)
		r.Version = int(version)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("events: list deliveries: %w", err)
	}
	return out, nil
}

const retrySQL = `UPDATE platform.event_deliveries
SET state = 'pending', next_attempt_at = $2, lease_id = NULL, lease_until = NULL, updated_at = $2
WHERE id = $1 AND state IN ('failed', 'dead')`

// Retry resets a failed or dead delivery to pending, due now. Its attempts
// are kept, so a dead one that fails again is dead again at once.
func (o *Operator) Retry(ctx context.Context, id int64) error {
	tag, err := o.pool.Exec(ctx, retrySQL, id, o.clock())
	if err != nil {
		return fmt.Errorf("events: retry delivery %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return o.missing(ctx, id)
	}
	return nil
}

const skipSQL = `UPDATE platform.event_deliveries
SET state = 'done', note = $2, completed_at = $3, lease_id = NULL, lease_until = NULL, updated_at = $3
WHERE id = $1 AND state <> 'done'`

// Skip marks a delivery that is not done as done without handling it,
// keeping note, which unblocks the later deliveries of its handler and
// aggregate.
func (o *Operator) Skip(ctx context.Context, id int64, note string) error {
	tag, err := o.pool.Exec(ctx, skipSQL, id, note, o.clock())
	if err != nil {
		return fmt.Errorf("events: skip delivery %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return o.missing(ctx, id)
	}
	return nil
}

// missing tells a delivery that is not there from one in the wrong state.
func (o *Operator) missing(ctx context.Context, id int64) error {
	var exists bool
	if err := o.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform.event_deliveries WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("events: read delivery %d: %w", id, err)
	}
	if exists {
		return ErrDeliveryState
	}
	return ErrDeliveryNotFound
}
