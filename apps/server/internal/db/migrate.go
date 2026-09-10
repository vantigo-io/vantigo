package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

// MigrationLockKey serialises every migrator of every version ("VANTIGO1" as
// a 64-bit value — the key the .NET MigrationLock used). It must never change:
// an old and a new binary mid-rollout would stop contending with each other,
// silently.
const MigrationLockKey int64 = 0x56414E5449474F31

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ApplyMigrations applies every pending migration under the advisory lock
// and returns an error rather than exiting, so both `migrate` and `api`
// (migrate, then serve) decide for themselves what a failure means.
//
// pg_advisory_lock is held by a session, so the lock, the migrations and the
// unlock must share one physical connection. database/sql is a pool; pinning
// it to a single connection is what makes every statement below run in the
// session that holds the lock.
func ApplyMigrations(ctx context.Context, databaseURL string) error {
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("db: open: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := waitForDatabase(ctx, sqlDB.PingContext); err != nil {
		return err
	}

	slog.InfoContext(ctx, "acquiring the migration advisory lock")
	if _, err := sqlDB.ExecContext(ctx, "SELECT pg_advisory_lock($1)", MigrationLockKey); err != nil {
		return fmt.Errorf("db: acquire the migration lock: %w", err)
	}
	defer func() {
		_, _ = sqlDB.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", MigrationLockKey)
	}()

	dir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("db: embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir)
	if err != nil {
		return fmt.Errorf("db: goose provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("db: apply migrations: %w", err)
	}
	for _, r := range results {
		slog.InfoContext(ctx, "applied migration", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	slog.InfoContext(ctx, "migrations are up to date", "applied", len(results))
	return nil
}
