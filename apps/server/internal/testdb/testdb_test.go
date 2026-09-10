package testdb

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func databaseExists(t *testing.T, name string) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, baseURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		t.Fatalf("query pg_database: %v", err)
	}
	return exists
}

func TestURL_GivesEachTestItsOwnDatabaseAndDropsIt(t *testing.T) {
	var first, second string
	t.Run("create", func(t *testing.T) {
		first, second = URL(t), URL(t)
		if first == second {
			t.Fatal("two calls returned the same database")
		}
		for _, raw := range []string{first, second} {
			conn, err := pgx.Connect(context.Background(), raw)
			if err != nil {
				t.Fatalf("connect to %s: %v", raw, err)
			}
			_ = conn.Close(context.Background())
		}
	})

	for _, raw := range []string{first, second} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if name := strings.TrimPrefix(u.Path, "/"); databaseExists(t, name) {
			t.Errorf("database %s survived its test", name)
		}
	}
}

func TestMigrated_AppliesTheSchema(t *testing.T) {
	pool, _ := Migrated(t)
	var present bool
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('platform.rate_limit') IS NOT NULL").Scan(&present); err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Error("platform.rate_limit is missing after Migrated")
	}
}

func TestBaseURL_DefaultsToTheComposeServer(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") != "" {
		t.Skip("TEST_DATABASE_URL overrides the default")
	}
	if baseURL() != defaultURL {
		t.Errorf("baseURL = %q", baseURL())
	}
}
