package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
)

// RoleEnv names the environment variable that makes Migrated hand out the
// runtime role's pool: TEST_DATABASE_ROLE=runtime. CI runs the packages whose
// work reaches partition upkeep with it set, so a query, a function or a
// worker that needs more than the runtime role holds — DDL above all — fails
// a test rather than an installation.
const RoleEnv = "TEST_DATABASE_ROLE"

// RuntimeRole is the least-privilege role the installation guide has the
// server connect as (docs admin/installation.md, Database roles), made on the
// test server once and shared by every test database on it: a role is the
// server's, not a database's. runtimePassword is its password; the server is
// a throwaway one, as the owner's own vantigo/vantigo is.
const (
	RuntimeRole     = "vantigo_test_runtime"
	runtimePassword = "vantigo_test_runtime"
)

// prepareRuntimeRole makes RuntimeRole when the server does not have it yet
// and, in the database at databaseURL, gives it what deploy/compose's init
// script gives vantigo_app before the migrations run: default privileges,
// attached to the owner, over the schemas, tables and sequences the owner
// creates afterwards. It calls a function as PUBLIC may, and one whose
// EXECUTE a migration revokes from PUBLIC it is granted by that migration
// (00220, 00221), as an installation's runtime role is. It owns nothing,
// cannot create a database or a role, and is granted CREATE on no schema.
//
// Parallel test binaries race to make the role; whichever loses sees either
// duplicate_object or, when both inserts reach pg_authid at once,
// unique_violation, and both mean the role is there.
func prepareRuntimeRole(ctx context.Context, admin *pgx.Conn, databaseURL string) error {
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DO $$
BEGIN
    CREATE ROLE %[1]s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT PASSWORD '%[2]s';
EXCEPTION WHEN duplicate_object OR unique_violation THEN
    NULL;
END
$$`, RuntimeRole, runtimePassword)); err != nil {
		return fmt.Errorf("make the runtime role: %w", err)
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to the template: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, stmt := range []string{
		"ALTER DEFAULT PRIVILEGES GRANT USAGE ON SCHEMAS TO " + RuntimeRole,
		"ALTER DEFAULT PRIVILEGES GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + RuntimeRole,
		"ALTER DEFAULT PRIVILEGES GRANT USAGE, SELECT ON SEQUENCES TO " + RuntimeRole,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("grant the runtime role its default privileges: %w", err)
		}
	}
	return nil
}

// RuntimeRequested reports whether TEST_DATABASE_ROLE=runtime is set, the
// switch that has Migrated, and every modtest harness, connect as
// RuntimeRole.
func RuntimeRequested() bool { return os.Getenv(RoleEnv) == "runtime" }

// MigratedAsOwner is Migrated as the owner whatever TEST_DATABASE_ROLE says,
// for a test whose subject is the schema itself and that alters it or makes
// a role on the way: a trigger switched off to reach a check behind it, say.
func MigratedAsOwner(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	return open(t, create(t, templateName(t)))
}

// MigratedAsRuntime is Migrated with the pool and the connection string of
// RuntimeRole in place of the owner's: the database is migrated by the
// owner, and the test then talks to it as the server does in an installation
// that follows the guide.
func MigratedAsRuntime(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	return open(t, runtimeURL(t, create(t, templateName(t))))
}

// MigratedAsRuntimeWithOwner is MigratedAsRuntime and, beside it, a small
// pool of the owner's on the same database, for the fixtures of a test whose
// subject runs as the runtime role: a fault injected with a trigger of the
// test's own is DDL the runtime role may not run.
func MigratedAsRuntimeWithOwner(t testing.TB) (runtime *pgxpool.Pool, runtimeDatabaseURL string, owner *pgxpool.Pool) {
	t.Helper()
	ownerURL := create(t, templateName(t))
	ownerPool, err := db.Open(context.Background(), ownerURL, db.WithMaxConns(4))
	if err != nil {
		t.Fatalf("testdb: open the owner's pool: %v", err)
	}
	t.Cleanup(ownerPool.Close)
	runtime, runtimeDatabaseURL = open(t, runtimeURL(t, ownerURL))
	return runtime, runtimeDatabaseURL, ownerPool
}

// runtimeURL is ownerURL with RuntimeRole's credentials.
func runtimeURL(t testing.TB, ownerURL string) string {
	t.Helper()
	parsed, err := url.Parse(ownerURL)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	parsed.User = url.UserPassword(RuntimeRole, runtimePassword)
	return parsed.String()
}
