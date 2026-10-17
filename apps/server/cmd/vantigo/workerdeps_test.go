package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// TestWorkerModeDeps_CarryIdentitysUserDirectory proves worker mode — which
// never composes — hands every module's workers identity's user directory,
// built from the same Deps serve builds (moduleDeps) over the same module
// list (businessModules), and resolving a real user on the pool. Accounts
// stays nil until identity provides it.
func TestWorkerModeDeps_CarryIdentitysUserDirectory(t *testing.T) {
	_, databaseURL := testdb.Migrated(t)
	cfg, err := config.Load(map[string]string{
		"DATABASE_URL": databaseURL,
		"APP_URL":      "http://localhost:8080",
		"APP_SECRET":   testAppSecret,
		"SMTP_HOST":    "smtp.example.invalid",
		"SMTP_FROM":    "noreply@example.invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
		VALUES ($1, 'kari@example.test', 'KARI@EXAMPLE.TEST', 'Kari Nordmann', $2, $3, $3)`, id, uuid.New(), time.Now()); err != nil {
		t.Fatal(err)
	}

	deps, access, err := moduleDeps(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var seen module.Deps
	probe := module.Module{Name: "identity", Workers: func(d module.Deps) []worker.Worker {
		seen = d
		return nil
	}}
	module.Workers(deps, append(businessModules(access), probe)...)

	if seen.Users == nil {
		t.Fatal("worker-mode Deps.Users = nil, want identity's user directory")
	}
	user, err := seen.Users.User(ctx, id)
	if err != nil || user == nil || user.DisplayName != "Kari Nordmann" {
		t.Errorf("Users.User = %+v, %v, want Kari Nordmann from the pool", user, err)
	}
	if seen.Accounts != nil {
		t.Errorf("worker-mode Deps.Accounts = %v, want nil until identity provides account provisioning", seen.Accounts)
	}
}
