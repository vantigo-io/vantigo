package staff

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// lockNotAvailable is the SQLSTATE a NOWAIT probe answers when another
// transaction holds the row.
const lockNotAvailable = "55P03"

// withPersonLock takes the person's row before fn runs and holds it until fn
// returns: a NOWAIT probe for the row from a connection of its own is refused
// while fn runs and granted after. The lock is FOR NO KEY UPDATE, so a
// foreign-key check naming the person — a KEY SHARE lock, which another
// person's terms or a department take — is not refused. fn's context is
// marked as inside a transaction, so the contract-call hook sees any call fn
// makes out of the module; an unknown person is errPersonNotFound, with fn
// never run.
func TestWithPersonLock_HoldsThePersonRowForTheTransaction(t *testing.T) {
	t.Parallel()
	pool, url := testdb.Migrated(t)
	ctx := context.Background()
	s := &server{deps: module.Deps{Pool: pool, Clock: time.Now}}
	var person int64
	if err := pool.QueryRow(ctx, `INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('1', 'Kari', 'Nordmann', 'Kari Nordmann', now(), now()) RETURNING id`).Scan(&person); err != nil {
		t.Fatalf("seed a person: %v", err)
	}
	probe, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect the probe: %v", err)
	}
	defer func() { _ = probe.Close(ctx) }()
	tryLock := func(strength string) error {
		tx, err := probe.Begin(ctx)
		if err != nil {
			t.Fatalf("begin the probe: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `SELECT id FROM staff.people WHERE id = $1 FOR `+strength+` NOWAIT`, person)
		return err
	}

	ran := false
	err = s.withPersonLock(ctx, person, func(ctx context.Context, _ pgx.Tx, _ *store.Queries) error {
		ran = true
		if !inOpenTx(ctx) || !inLockedTx(ctx) {
			t.Error("fn's context is not marked as inside a locked transaction")
		}
		var pgErr *pgconn.PgError
		if err := tryLock("NO KEY UPDATE"); !errors.As(err, &pgErr) || pgErr.Code != lockNotAvailable {
			t.Errorf("a second writer's probe while fn runs: %v, want %s", err, lockNotAvailable)
		}
		if err := tryLock("KEY SHARE"); err != nil {
			t.Errorf("a foreign-key check's probe while fn runs: %v, want it granted", err)
		}
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("withPersonLock = %v (ran %v), want fn run and nil", err, ran)
	}
	if err := tryLock("NO KEY UPDATE"); err != nil {
		t.Errorf("the probe after the transaction: %v, want the lock released", err)
	}

	err = s.withPersonLock(ctx, person+1000, func(context.Context, pgx.Tx, *store.Queries) error {
		t.Error("fn ran for an unknown person")
		return nil
	})
	if !errors.Is(err, errPersonNotFound) {
		t.Errorf("an unknown person = %v, want errPersonNotFound", err)
	}
}

// The manager-graph lock is a transaction advisory lock: held from the moment
// it is taken to the end of the transaction, and refused to anyone else
// meanwhile.
func TestLockManagerGraph_IsHeldUntilTheTransactionEnds(t *testing.T) {
	t.Parallel()
	pool, url := testdb.Migrated(t)
	ctx := context.Background()
	s := &server{deps: module.Deps{Pool: pool, Clock: time.Now}}
	probe, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect the probe: %v", err)
	}
	defer func() { _ = probe.Close(ctx) }()
	held := func() bool {
		var got bool
		if err := probe.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, managerGraphLock).Scan(&got); err != nil {
			t.Fatalf("probe the advisory lock: %v", err)
		}
		if got {
			if _, err := probe.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, managerGraphLock); err != nil {
				t.Fatalf("release the probe's lock: %v", err)
			}
		}
		return !got
	}

	if err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := lockManagerGraph(ctx, txq); err != nil {
			return err
		}
		if !held() {
			t.Error("the manager graph is free to another session while the transaction holds it")
		}
		return nil
	}); err != nil {
		t.Fatalf("withLockedTx: %v", err)
	}
	if held() {
		t.Error("the manager graph is still held after the transaction ended")
	}
}
