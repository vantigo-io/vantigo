package main

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// TestWorkerModeDeps_CarryIdentitysUserDirectory proves worker mode — which
// never composes — hands every module's workers identity's user directory,
// built from the same Deps serve builds (moduleDeps) over the same module
// list (businessModules), and resolving a real user on the pool — and
// identity's account provisioning beside it, reading the pool's recorded mode.
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
	if seen.Accounts == nil {
		t.Fatal("worker-mode Deps.Accounts = nil, want identity's account provisioning")
	}
	if mode, err := seen.Accounts.Mode(ctx); err != nil || mode != contracts.AccountModeLocal {
		t.Errorf("Accounts.Mode = %q, %v, want local from the pool", mode, err)
	}
}

// TestWorkerMode_StaffAloneStartsTheAccountsWorker proves MODULES=staff
// alone hands the runner staff-accounts (accounts plan M10) — identity is
// always there to provide Accounts — and staff-competence (staff competence
// design S7); STAFF_ACCOUNTS_ENABLED=0 and STAFF_COMPETENCE_ENABLED=0 each
// keep their own out, the others still started.
func TestWorkerMode_StaffAloneStartsTheAccountsWorker(t *testing.T) {
	_, databaseURL := testdb.Migrated(t)
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, tc := range []struct {
		accounts, competence string
		want                 []string
	}{
		{want: []string{"staff-retention", "staff-accounts", "staff-competence"}},
		{accounts: "0", want: []string{"staff-retention", "staff-competence"}},
		{competence: "0", want: []string{"staff-retention", "staff-accounts"}},
	} {
		env := map[string]string{
			"DATABASE_URL": databaseURL,
			"APP_URL":      "http://localhost:8080",
			"APP_SECRET":   testAppSecret,
			"SMTP_HOST":    "smtp.example.invalid",
			"SMTP_FROM":    "noreply@example.invalid",
			"MODULES":      "staff",
		}
		if tc.accounts != "" {
			env["STAFF_ACCOUNTS_ENABLED"] = tc.accounts
		}
		if tc.competence != "" {
			env["STAFF_COMPETENCE_ENABLED"] = tc.competence
		}
		cfg, err := config.Load(env)
		if err != nil {
			t.Fatal(err)
		}
		deps, access, err := moduleDeps(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, w := range module.Workers(deps, businessModules(access)...) {
			names = append(names, w.Name())
		}
		if !slices.Equal(names, tc.want) {
			t.Errorf("STAFF_ACCOUNTS_ENABLED=%q, STAFF_COMPETENCE_ENABLED=%q: workers = %v, want %v", tc.accounts, tc.competence, names, tc.want)
		}
	}
}
