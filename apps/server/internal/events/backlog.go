package events

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// backlogSQL is the outbox's backlog (Kubernetes readiness KR21): every
// delivery not done, by state, and the events not yet fanned out as the
// state "unfanned", each with its count and the creation time of its oldest
// row. Both reads are on an index (ix_event_deliveries_state_next_attempt_at,
// ix_events_not_fanned_out).
const backlogSQL = `SELECT state, count(*), min(created_at) FROM platform.event_deliveries
WHERE state IN ('pending', 'failed', 'dead') GROUP BY state
UNION ALL
SELECT 'unfanned', count(*), min(created_at) FROM platform.events WHERE fanned_out_at IS NULL`

// observeBacklog registers the backlog gauges, vantigo.events.backlog (rows)
// and vantigo.events.backlog.age (seconds since the oldest was created),
// each by state, read from pool whenever the meter collects; it returns the
// func that unregisters them. With no meter provider installed nothing ever
// collects, so the query never runs. Every worker process runs a
// dispatcher, so two worker replicas report the same backlog: read it as a
// maximum, not a sum.
func observeBacklog(pool *pgxpool.Pool, clock func() time.Time) (func(), error) {
	meter := otel.Meter("github.com/vantigo-io/vantigo/server/internal/events")
	rows, err := meter.Int64ObservableGauge("vantigo.events.backlog",
		metric.WithDescription("Outbox rows not yet delivered: deliveries by state, and events not yet fanned out."),
		metric.WithUnit("{row}"))
	if err != nil {
		return nil, fmt.Errorf("events: backlog gauge: %w", err)
	}
	age, err := meter.Float64ObservableGauge("vantigo.events.backlog.age",
		metric.WithDescription("Age of the oldest outbox row not yet delivered, by state."),
		metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("events: backlog age gauge: %w", err)
	}
	reg, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		res, err := pool.Query(ctx, backlogSQL)
		if err != nil {
			return fmt.Errorf("events: read the backlog: %w", err)
		}
		defer res.Close()
		now := clock()
		for res.Next() {
			var state string
			var n int64
			var oldest *time.Time
			if err := res.Scan(&state, &n, &oldest); err != nil {
				return fmt.Errorf("events: read the backlog: %w", err)
			}
			attrs := metric.WithAttributes(attribute.String("state", state))
			o.ObserveInt64(rows, n, attrs)
			var seconds float64
			if oldest != nil {
				seconds = max(now.Sub(*oldest).Seconds(), 0)
			}
			o.ObserveFloat64(age, seconds, attrs)
		}
		return res.Err()
	}, rows, age)
	if err != nil {
		return nil, fmt.Errorf("events: backlog callback: %w", err)
	}
	return func() { _ = reg.Unregister() }, nil
}
