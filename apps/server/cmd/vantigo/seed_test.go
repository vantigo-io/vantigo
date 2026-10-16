package main

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func TestDemoCustomRoles_AllPermissionsExistInModuleCatalog(t *testing.T) {
	known := make(map[string]bool)
	for _, mod := range businessModules(nil) {
		for _, p := range mod.Permissions {
			known[p.Key] = true
		}
	}

	for _, role := range demoCustomRoles {
		for _, key := range role.permissions {
			if !known[key] {
				t.Errorf("demo role %q references unknown permission key %q", role.name, key)
			}
		}
	}
}

func TestRun_SeedPopulatesDemoDatasetAndIsIdempotent(t *testing.T) {
	databaseURL := testdb.URL(t)
	setEnv(t,
		"APP_ENV", "development",
		"DATABASE_URL", databaseURL,
		"APP_URL", "http://localhost:8080",
	)

	code, stdout, stderr := runCapture("seed")
	if code != 0 {
		t.Fatalf("first seed failed with exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "development demo seed applied") {
		t.Errorf("expected seed applied log in stdout, got: %s", stdout)
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	for _, check := range []struct {
		name  string
		query string
		want  int
	}{
		{"identity.users", "SELECT count(*) FROM identity.users", demoUserCount},
		{"staff.people", "SELECT count(*) FROM staff.people", demoUserCount},
		{"customers.customers", "SELECT count(*) FROM customers.customers", demoCustomerCount},
		{"customers.contacts", "SELECT count(*) FROM customers.contacts", demoCustomerCount * demoContactsPerCustomer},
		{"products.products", "SELECT count(*) FROM products.products", demoProductCount},
		{"projects.projects", "SELECT count(*) FROM projects.projects", demoProjectCount},
		{"invoices.invoices", "SELECT count(*) FROM invoices.invoices", 30},
	} {
		var got int
		if err := conn.QueryRow(ctx, check.query).Scan(&got); err != nil {
			t.Fatalf("%s query failed: %v", check.name, err)
		}
		if got != check.want {
			t.Errorf("%s count = %d, want %d", check.name, got, check.want)
		}
	}

	// Second run must be a no-op and preserve exact counts.
	code2, stdout2, stderr2 := runCapture("seed")
	if code2 != 0 {
		t.Fatalf("second seed failed with exit %d\nstdout: %s\nstderr: %s", code2, stdout2, stderr2)
	}
	if !strings.Contains(stdout2, "development seed already applied; skipping") {
		t.Errorf("expected idempotent skip message in stdout, got: %s", stdout2)
	}
}
