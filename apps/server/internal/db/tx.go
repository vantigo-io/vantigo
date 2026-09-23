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
	return isConstraintViolation(err, "23505", constraints)
}

// IsForeignKeyViolation reports whether err is a PostgreSQL
// foreign_key_violation (23503) against any of constraints, matched exactly as
// IsUniqueViolation matches its own: no constraints, or any element equal to
// "", matches any 23503; otherwise err's violated-constraint name must equal
// one of them.
//
// Naming the constraint matters more here than it does above, and for a
// different reason than the several-indexes-per-write one. A single INSERT
// commonly has several foreign keys pointing at DIFFERENT things (a link table
// references both sides of the link), they fail for unrelated reasons, and the
// answers do not interchange: a caller mapping "the tag you named is gone" to
// a field error on the request body would, with an unnamed check, map "the
// customer you are writing to is gone" to the same field error. Pass the one
// name whose absence the caller actually knows how to explain.
func IsForeignKeyViolation(err error, constraints ...string) bool {
	return isConstraintViolation(err, "23503", constraints)
}

// IsRestrictViolation reports whether err is a PostgreSQL restrict_violation
// (23001) against any of constraints, matched exactly as the two checks above
// match their own.
//
// It is not IsForeignKeyViolation under another name, and the difference is
// one a caller cannot see in its schema at a glance: on PostgreSQL 18+ a
// foreign key declared ON DELETE RESTRICT is checked immediately and reports
// 23001 when the referenced row is deleted, whereas the default NO ACTION (and
// an insert or update on the referencing side, whatever the action) reports
// 23503. Before PostgreSQL 18 the RESTRICT case reported 23503 as well. A
// caller that deletes the parent of a RESTRICT key — customers' DELETE
// /customers/groups/{groupId} against customers_group_id_fkey — therefore
// matches BOTH this and IsForeignKeyViolation, since an installation may bring
// its own PostgreSQL; matching 23503 alone there would compile, pass every test
// that never races, and on PostgreSQL 18 turn the one race the key exists to
// stop into a 500.
func IsRestrictViolation(err error, constraints ...string) bool {
	return isConstraintViolation(err, "23001", constraints)
}

// isConstraintViolation is the matching every exported check above does,
// shared rather than written three times: the rules are identical and only the
// SQLSTATE differs, so a change to how a name is matched must land in one
// place.
func isConstraintViolation(err error, code string, constraints []string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
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
