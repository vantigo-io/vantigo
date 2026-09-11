// Package db owns PostgreSQL access: the request-traffic pool, the embedded
// goose migrations, and the advisory-lock-guarded runner that applies them.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Option adjusts the pool Open builds, after the connection string has been
// parsed and before the pool is created.
type Option func(*pgxpool.Config)

// WithMaxConns fixes the pool's size. Without it pgxpool takes max(4,
// NumCPU), which ties the size to the host: tests that hold a lock while
// several requests queue behind it starve on a small CI runner and run
// comfortably on a large workstation.
func WithMaxConns(n int32) Option {
	return func(cfg *pgxpool.Config) { cfg.MaxConns = n }
}

// Open returns a connection pool for request traffic. It pings before
// returning, so a wrong DATABASE_URL fails startup instead of the first
// request.
func Open(ctx context.Context, databaseURL string, opts ...Option) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: parse connection string: %w", err)
	}
	// Query spans join the request's trace. With no tracer provider installed
	// they go to the global no-op and cost next to nothing. otelpgx defaults
	// to the bare operation name (e.g. "SELECT"); WithQuerySpanNamePrefix
	// prefixes it with "query " per the OTel DB span-naming convention.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithQuerySpanNamePrefix())
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}
	if err := waitForDatabase(ctx, pool.Ping); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// connectAttempts and retryDelay bound how long startup waits for a PostgreSQL
// that is still coming up (a compose stack starting both at once): six
// attempts with a growing pause, about 30 s in total.
const connectAttempts = 6

var retryDelay = func(attempt int) time.Duration { return time.Duration(attempt) * 2 * time.Second }

// pingTimeout bounds one attempt. Without it a black-holed host makes every
// attempt wait out the kernel's connect timeout (minutes), not seconds.
var pingTimeout = 5 * time.Second

func waitForDatabase(ctx context.Context, ping func(context.Context) error) error {
	var err error
	for attempt := 1; attempt <= connectAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		err = ping(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		if attempt == connectAttempts {
			break
		}
		slog.WarnContext(ctx, "PostgreSQL is not reachable yet; retrying", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay(attempt)):
		}
	}
	return fmt.Errorf("db: PostgreSQL unreachable after %d attempts: %w", connectAttempts, err)
}
