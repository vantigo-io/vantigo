package telemetry

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ObservePool reports pool's statistics whenever the meter collects
// (Kubernetes readiness KR23), so a DATABASE_POOL_SIZE that is too small shows
// as waits before it is an outage:
//
//   - vantigo.db.pool.connections (gauge, by state: acquired, idle,
//     constructing) and vantigo.db.pool.max (gauge);
//   - vantigo.db.pool.waits (counter): acquires that found no idle
//     connection and had to wait;
//   - vantigo.db.pool.wait_time (counter, seconds): the time those waits took.
//
// It returns the func that stops reporting. With no meter provider it costs
// nothing: nothing collects.
func ObservePool(pool *pgxpool.Pool) (func(), error) {
	meter := otel.Meter("github.com/vantigo-io/vantigo/server/internal/telemetry/pool")
	conns, err := meter.Int64ObservableGauge("vantigo.db.pool.connections",
		metric.WithDescription("Connections in the database pool, by state."), metric.WithUnit("{connection}"))
	if err != nil {
		return nil, fmt.Errorf("telemetry: pool gauge: %w", err)
	}
	maxConns, err := meter.Int64ObservableGauge("vantigo.db.pool.max",
		metric.WithDescription("The database pool's size (DATABASE_POOL_SIZE)."), metric.WithUnit("{connection}"))
	if err != nil {
		return nil, fmt.Errorf("telemetry: pool gauge: %w", err)
	}
	waits, err := meter.Int64ObservableCounter("vantigo.db.pool.waits",
		metric.WithDescription("Acquires that found no idle connection and waited for one."), metric.WithUnit("{acquire}"))
	if err != nil {
		return nil, fmt.Errorf("telemetry: pool counter: %w", err)
	}
	waitTime, err := meter.Float64ObservableCounter("vantigo.db.pool.wait_time",
		metric.WithDescription("Time spent waiting for a connection."), metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("telemetry: pool counter: %w", err)
	}
	reg, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := pool.Stat()
		o.ObserveInt64(conns, int64(s.AcquiredConns()), metric.WithAttributes(attribute.String("state", "acquired")))
		o.ObserveInt64(conns, int64(s.IdleConns()), metric.WithAttributes(attribute.String("state", "idle")))
		o.ObserveInt64(conns, int64(s.ConstructingConns()), metric.WithAttributes(attribute.String("state", "constructing")))
		o.ObserveInt64(maxConns, int64(s.MaxConns()))
		o.ObserveInt64(waits, s.EmptyAcquireCount())
		o.ObserveFloat64(waitTime, s.EmptyAcquireWaitTime().Seconds())
		return nil
	}, conns, maxConns, waits, waitTime)
	if err != nil {
		return nil, fmt.Errorf("telemetry: pool callback: %w", err)
	}
	return func() { _ = reg.Unregister() }, nil
}
