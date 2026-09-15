package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// MigrationLockKey serialises every migrator of every version ("VANTIGO1" as
// a 64-bit value — the key the .NET MigrationLock used). It must never change:
// an old and a new binary mid-rollout would stop contending with each other,
// silently.
const MigrationLockKey int64 = 0x56414E5449474F31

//go:embed migrations/*.sql
var migrationsFS embed.FS

// errSessionLost is returned by singleSessionConnector once a connection has
// already been established through it and a second dial is attempted.
var errSessionLost = errors.New("db: the migration session was lost; refusing to continue without the advisory lock")

// singleSessionConnector wraps a database/sql driver.Connector so that at
// most one physical connection is ever opened through it, for the lifetime
// of the *sql.DB built on it.
//
// pg_advisory_lock is held by a session (one physical connection). Pinning
// the pool to a single connection (SetMaxOpenConns(1) etc.) makes the lock,
// the migrations and the unlock share that session in the common case — but
// database/sql may still decide a connection is bad and silently redial for
// the next statement. If that happens after pg_advisory_lock, PostgreSQL has
// already released the lock as the old socket closed, and the Go code would
// carry on believing it still held it, racing a second waiting migrator.
// singleSessionConnector makes that redial impossible instead of merely
// unlikely: once Connect has succeeded once, every later call fails loudly
// with errSessionLost rather than opening a second connection.
type singleSessionConnector struct {
	inner driver.Connector

	mu   sync.Mutex
	used bool
}

func newSingleSessionConnector(inner driver.Connector) *singleSessionConnector {
	return &singleSessionConnector{inner: inner}
}

// Connect delegates to the inner connector, but only marks the session used
// once it has actually succeeded — a failed attempt (PostgreSQL still coming
// up, say) does not consume it, so waitForDatabase's retries keep working.
func (c *singleSessionConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.mu.Lock()
	if c.used {
		c.mu.Unlock()
		return nil, errSessionLost
	}
	c.mu.Unlock()

	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.used = true
	c.mu.Unlock()
	return conn, nil
}

func (c *singleSessionConnector) Driver() driver.Driver {
	return c.inner.Driver()
}

// ApplyMigrations applies every pending migration under the advisory lock
// and returns an error rather than exiting, so both `migrate` and `api`
// (migrate, then serve) decide for themselves what a failure means.
//
// pg_advisory_lock is held by a session, so the lock, the migrations and the
// unlock must share one physical connection. singleSessionConnector, plus
// pinning the pool to a single connection, is what makes that guarantee
// hold even if database/sql would otherwise have silently reconnected.
func ApplyMigrations(ctx context.Context, databaseURL string) error {
	// Parsed as a pool config even though the migrator holds one plain
	// connection: the same DATABASE_URL feeds Open's pool, and operators size
	// that pool with pool_max_conns and friends on the URL. pgxpool consumes
	// those keys; pgx.ParseConfig would forward them to PostgreSQL as runtime
	// parameters, which rejects the connection outright.
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("db: parse connection string: %w", err)
	}
	connector := newSingleSessionConnector(stdlib.GetConnector(*cfg.ConnConfig))
	sqlDB := sql.OpenDB(connector)
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	// Never recycle the one connection we're allowed: singleSessionConnector
	// would refuse to redial, turning a routine recycle into a hard failure.
	sqlDB.SetConnMaxLifetime(0)
	sqlDB.SetConnMaxIdleTime(0)

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
