package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func TestWithTx_CommitsOnNilError(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO platform.rate_limit (key, window_start, hits) VALUES ($1, now(), 1)", "tx-commit")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	var hits int
	if err := pool.QueryRow(ctx, "SELECT hits FROM platform.rate_limit WHERE key = $1", "tx-commit").Scan(&hits); err != nil {
		t.Fatalf("row not visible after commit: %v", err)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}

func TestWithTx_RollsBackOnError(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	wantErr := errors.New("boom")

	err := db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO platform.rate_limit (key, window_start, hits) VALUES ($1, now(), 1)", "tx-rollback"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM platform.rate_limit WHERE key = $1", "tx-rollback").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("row is visible after a rollback, want none")
	}
}

// A panic in fn rolls the transaction back, releases its connection, and
// reaches WithTx's caller unchanged.
func TestWithTx_RollsBackAndRepanicsWhenFnPanics(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	const boom = "boom"

	func() {
		defer func() {
			if v := recover(); v != boom {
				t.Errorf("recovered %v, want fn's own panic value %q", v, boom)
			}
		}()
		_ = db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "INSERT INTO platform.rate_limit (key, window_start, hits) VALUES ($1, $2, 1)", "tx-panic", time.Now()); err != nil {
				return err
			}
			panic(boom)
		})
		t.Error("WithTx returned, want fn's panic to propagate")
	}()

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM platform.rate_limit WHERE key = $1", "tx-panic").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("the panicking transaction's row is visible, want it rolled back")
	}
	if n := pool.Stat().AcquiredConns(); n != 0 {
		t.Errorf("%d connections still acquired after the panic, want the transaction's released", n)
	}
}

func TestRetrySerializable_RetriesThenSucceeds(t *testing.T) {
	var calls int
	err := db.RetrySerializable(context.Background(), 5, func() error {
		calls++
		if calls <= 2 {
			return &pgconn.PgError{Code: "40001"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RetrySerializable: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (two failures then a success)", calls)
	}
}

func TestRetrySerializable_RetriesOnDeadlockToo(t *testing.T) {
	var calls int
	err := db.RetrySerializable(context.Background(), 5, func() error {
		calls++
		if calls == 1 {
			return &pgconn.PgError{Code: "40P01"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RetrySerializable: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestRetrySerializable_GivesUpAfterAttempts(t *testing.T) {
	var calls int
	wantErr := &pgconn.PgError{Code: "40001"}
	err := db.RetrySerializable(context.Background(), 3, func() error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (== attempts)", calls)
	}
}

func TestRetrySerializable_DoesNotRetryOtherErrors(t *testing.T) {
	var calls int
	wantErr := errors.New("boom")
	err := db.RetrySerializable(context.Background(), 5, func() error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-serialization errors are not retried)", calls)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	insert := func(id, normalizedEmail string) error {
		_, err := pool.Exec(ctx, `INSERT INTO identity.users
			(id, email, normalized_email, display_name, version, created_at, updated_at)
			VALUES ($1, $2, $3, 'Test User', gen_random_uuid(), now(), now())`,
			id, normalizedEmail, normalizedEmail)
		return err
	}

	if err := insert("00000000-0000-4000-8000-000000000101", "DUP@EXAMPLE.COM"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := insert("00000000-0000-4000-8000-000000000102", "DUP@EXAMPLE.COM")
	if err == nil {
		t.Fatal("expected a unique violation on the second insert")
	}

	if !db.IsUniqueViolation(err, "ux_users_normalized_email") {
		t.Errorf("IsUniqueViolation(err, %q) = false, want true", "ux_users_normalized_email")
	}
	if !db.IsUniqueViolation(err, "") {
		t.Error(`IsUniqueViolation(err, "") = false, want true (empty constraint matches any 23505)`)
	}
	if db.IsUniqueViolation(err, "some_other_constraint") {
		t.Error("IsUniqueViolation matched an unrelated constraint name")
	}
	if db.IsUniqueViolation(errors.New("boom"), "") {
		t.Error("IsUniqueViolation matched a non-pgconn error")
	}

	// Variadic form: a caller whose write is guarded by more than one
	// unique index (e.g. communications.channels' (type, address) and its
	// partial (type, is_default)) needs to match any one of several names
	// in a single call, not just the first it remembered to check.
	if !db.IsUniqueViolation(err, "some_other_constraint", "ux_users_normalized_email") {
		t.Error("IsUniqueViolation(err, wrong, right) = false, want true (matches the second name)")
	}
	if db.IsUniqueViolation(err, "some_other_constraint", "yet_another_constraint") {
		t.Error("IsUniqueViolation matched when none of several names were right")
	}
	if !db.IsUniqueViolation(err) {
		t.Error("IsUniqueViolation(err) with no names = false, want true (no constraints given matches any 23505, same as \"\")")
	}
}

// TestIsForeignKeyViolation pins the matching rules against a synthetic
// pgconn.PgError rather than a provoked violation, which is the whole of what
// this helper does: which SQLSTATE it accepts, which constraint names it
// matches, and that it looks through a wrapped error. A real 23503 raised by a
// real race is exercised where it matters — internal/customers'
// TestPutCustomersByIdTags_ATagDeletedMidWriteIsAFieldError, which deletes a
// tag out from under an in-flight insert — and reproducing one here would only
// couple this package's tests to another module's schema.
func TestIsForeignKeyViolation(t *testing.T) {
	// Wrapped, because every caller sees this error through at least one
	// fmt.Errorf("%w") on its way out of a query helper.
	err := fmt.Errorf("insert links: %w", &pgconn.PgError{Code: "23503", ConstraintName: "customer_tags_tag_id_fkey"})

	if !db.IsForeignKeyViolation(err, "customer_tags_tag_id_fkey") {
		t.Errorf("IsForeignKeyViolation(err, %q) = false, want true", "customer_tags_tag_id_fkey")
	}
	if !db.IsForeignKeyViolation(err, "") {
		t.Error(`IsForeignKeyViolation(err, "") = false, want true (empty constraint matches any 23503)`)
	}
	if !db.IsForeignKeyViolation(err) {
		t.Error("IsForeignKeyViolation(err) with no names = false, want true (no constraints given matches any 23503)")
	}
	if !db.IsForeignKeyViolation(err, "customer_tags_customer_id_fkey", "customer_tags_tag_id_fkey") {
		t.Error("IsForeignKeyViolation(err, wrong, right) = false, want true (matches the second name)")
	}
	// The distinction this helper exists to make: one insert into
	// customers.customer_tags has two foreign keys, and "the tag is gone" is a
	// field error on the body while "the customer is gone" is not.
	if db.IsForeignKeyViolation(err, "customer_tags_customer_id_fkey") {
		t.Error("IsForeignKeyViolation matched an unrelated constraint name")
	}
	if db.IsForeignKeyViolation(fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505", ConstraintName: "customer_tags_tag_id_fkey"})) {
		t.Error("IsForeignKeyViolation matched a unique violation (23505), want 23503 only")
	}
	if db.IsForeignKeyViolation(errors.New("boom")) {
		t.Error("IsForeignKeyViolation matched a non-pgconn error")
	}
}

// TestIsRestrictViolation pins what separates this helper from
// IsForeignKeyViolation: it accepts 23001 and only 23001, because the two
// SQLSTATEs come from the same foreign key depending on its ON DELETE action,
// and a caller that matched the wrong one would never see its own race. The
// real 23001 is provoked where it matters, in internal/customers' group delete.
func TestIsRestrictViolation(t *testing.T) {
	err := fmt.Errorf("delete group: %w", &pgconn.PgError{Code: "23001", ConstraintName: "customers_group_id_fkey"})

	if !db.IsRestrictViolation(err, "customers_group_id_fkey") {
		t.Errorf("IsRestrictViolation(err, %q) = false, want true", "customers_group_id_fkey")
	}
	if !db.IsRestrictViolation(err) {
		t.Error("IsRestrictViolation(err) with no names = false, want true (no constraints given matches any 23001)")
	}
	if db.IsRestrictViolation(err, "customer_tags_tag_id_fkey") {
		t.Error("IsRestrictViolation matched an unrelated constraint name")
	}
	if db.IsRestrictViolation(fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23503", ConstraintName: "customers_group_id_fkey"})) {
		t.Error("IsRestrictViolation matched a foreign_key_violation (23503), want 23001 only")
	}
	if db.IsForeignKeyViolation(err, "customers_group_id_fkey") {
		t.Error("IsForeignKeyViolation matched a restrict_violation (23001): the two must stay distinct")
	}
	if db.IsRestrictViolation(errors.New("boom")) {
		t.Error("IsRestrictViolation matched a non-pgconn error")
	}
}
