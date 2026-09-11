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
// (23505). An empty constraint matches any unique violation; a non-empty one
// must match the violated constraint's name exactly.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}
