package db_test

import (
	"context"
	"errors"
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
}
