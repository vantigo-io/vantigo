package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTx runs fn inside a transaction opened on pool with opts. It commits
// when fn returns nil and rolls back otherwise; a rollback after a
// successful commit is a no-op, so the deferred Rollback is unconditional.
func WithTx(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("db: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit transaction: %w", err)
	}
	return nil
}

// retryableSQLStates are the SQLSTATEs PostgreSQL uses for a transaction
// that lost a race it should be retried for: serialization_failure (a
// SERIALIZABLE transaction was aborted to preserve isolation) and
// deadlock_detected (it was chosen as the deadlock victim).
const (
	sqlStateSerializationFailure = "40001"
	sqlStateDeadlockDetected     = "40P01"
)

// RetrySerializable calls fn up to attempts times, retrying while it fails
// with a serialization failure (40001) or a deadlock (40P01), backing off
// 25ms × n between attempts (n is the attempt just made). Any other error
// is returned immediately without a retry. If every attempt fails, the last
// error is returned.
func RetrySerializable(ctx context.Context, attempts int, fn func() error) error {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if !IsSerializationConflict(err) || attempt == attempts {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond * time.Duration(attempt)):
		}
	}
	return err
}

// IsSerializationConflict reports whether err is a serialization failure
// (40001) or a deadlock (40P01): a transaction that lost a race, which
// RetrySerializable retries and a caller that does not retry may report as a
// conflict.
func IsSerializationConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == sqlStateSerializationFailure || pgErr.Code == sqlStateDeadlockDetected
}

// IsUniqueViolation reports whether err is a PostgreSQL unique_violation
// (23505) against any of constraints. No constraints, or any element of
// constraints equal to "", matches any unique violation regardless of name.
// Otherwise err's own violated-constraint name must exactly match one of
// them.
//
// This matches a partial unique index (`CREATE UNIQUE INDEX ... WHERE ...`)
// exactly as it matches a plain unique index or a named UNIQUE constraint —
// Postgres reports the index's own name as ConstraintName for a partial
// index's violation too, nothing index-kind-specific to detect. What a
// single-name call can miss is a write that is guarded by *more than one*
// unique index at once (communications.channels' (type, address) and its
// partial (type, is_default) — a caller checking only the first treats the
// second's violation as some unrelated failure and lets it escape through
// whatever generic error path the caller falls back to, which is usually
// the wrong error vocabulary for that caller's own module). Passing every
// name the write could legitimately collide on, in one call, is what this
// variadic form is for: `IsUniqueViolation(err, "ux_a", "ux_b")` rather than
// two single-name calls that only the first of which a careless caller
// might remember to make.
func IsUniqueViolation(err error, constraints ...string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	if len(constraints) == 0 {
		return true
	}
	for _, c := range constraints {
		if c == "" || pgErr.ConstraintName == c {
			return true
		}
	}
	return false
}
