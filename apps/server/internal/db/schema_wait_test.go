package db_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func TestLatestMigration_IsTheHighestEmbeddedVersion(t *testing.T) {
	latest, err := db.LatestMigration()
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := testdb.Migrated(t)
	applied, err := db.SchemaVersion(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if latest <= 0 || applied != latest {
		t.Errorf("LatestMigration = %d, a migrated database is at %d", latest, applied)
	}
}

// KR4: an empty database (no goose_db_version yet) reads as version 0, and
// the wait holds until the migration lands, then passes.
func TestWaitForSchema_HoldsUntilTheMigrationLands(t *testing.T) {
	url := testdb.URL(t)
	ctx := context.Background()
	pool, err := db.Open(ctx, url, db.WithMaxConns(2))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if v, err := db.SchemaVersion(ctx, pool); err != nil || v != 0 {
		t.Fatalf("an unmigrated database: version %d, error %v; want 0, nil", v, err)
	}
	latest, err := db.LatestMigration()
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- db.WaitForSchema(ctx, db.SchemaWait{
			Read: func(ctx context.Context) (int64, error) { return db.SchemaVersion(ctx, pool) },
			Want: latest, Every: 20 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("passed before the migration: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := db.ApplyMigrations(ctx, url); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("WaitForSchema = %v, want nil once migrated", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("still waiting after the migration")
	}
}

// A schema newer than the binary's passes at once (an old pod on a newer
// schema, KR36), and a failing read keeps waiting rather than giving up.
func TestWaitForSchema_NewerPassesAndErrorsKeepWaiting(t *testing.T) {
	ctx := context.Background()
	if err := db.WaitForSchema(ctx, db.SchemaWait{
		Read: func(context.Context) (int64, error) { return 300, nil }, Want: 219,
	}); err != nil {
		t.Errorf("a newer schema: %v", err)
	}

	reads := 0
	err := db.WaitForSchema(ctx, db.SchemaWait{
		Read: func(context.Context) (int64, error) {
			reads++
			if reads < 3 {
				return 0, errors.New("connection refused")
			}
			return 219, nil
		},
		Want: 219, Every: time.Millisecond, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil || reads != 3 {
		t.Errorf("after two failed reads: %v, %d reads", err, reads)
	}
}

// The wait honours its context: a shutdown signal while waiting ends it.
func TestWaitForSchema_HonoursTheContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := db.WaitForSchema(ctx, db.SchemaWait{
		Read: func(context.Context) (int64, error) { return 1, nil }, Want: 2,
		Every: time.Hour, Logger: slog.New(slog.DiscardHandler),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitForSchema = %v, want the context's error", err)
	}
}
