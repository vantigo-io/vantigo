package db_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// moduleSchemas are the PostgreSQL schemas owned by one module each. The
// platform schema is deliberately not one of these: internal/ratelimit
// reaches it from outside its own module, by design (Global Constraints).
var moduleSchemas = []string{"identity", "customers", "products", "energy", "communications"}

// schemaOwnedFile is one migration or query file, with the module that owns
// it and its full text.
type schemaOwnedFile struct {
	path  string
	owner string
	body  string
}

// migrationOwner returns the module a migration belongs to, from its
// NNNNN_<module>_<name>.sql file name (00001_platform_init.sql is "platform").
func migrationOwner(name string) string {
	parts := strings.SplitN(strings.TrimSuffix(name, ".sql"), "_", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// collectSchemaOwnedFiles reads every embedded migration and every
// internal/*/queries/*.sql query file, alongside the module that owns each.
func collectSchemaOwnedFiles(t *testing.T) []schemaOwnedFile {
	t.Helper()
	var files []schemaOwnedFile

	entries, err := fs.ReadDir(db.MigrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, e := range entries {
		body, err := fs.ReadFile(db.MigrationsFS, "migrations/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		files = append(files, schemaOwnedFile{
			path:  "migrations/" + e.Name(),
			owner: migrationOwner(e.Name()),
			body:  string(body),
		})
	}

	// internal/db/schema_test.go runs from internal/db, so
	// internal/*/queries/*.sql is reached via "../*/queries/*.sql".
	queryFiles, err := filepath.Glob("../*/queries/*.sql")
	if err != nil {
		t.Fatalf("glob queries: %v", err)
	}
	for _, path := range queryFiles {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// path == ../<module>/queries/<name>.sql: the owner is <module>.
		parts := strings.Split(filepath.ToSlash(path), "/")
		if len(parts) < 3 {
			t.Fatalf("unexpected query path shape: %s", path)
		}
		owner := parts[len(parts)-3]
		files = append(files, schemaOwnedFile{path: path, owner: owner, body: string(body)})
	}

	return files
}

// TestNoModuleReferencesAnotherModulesSchema is the cross-schema scan: no
// migration or query file owned by one module may reference another
// module's schema.
func TestNoModuleReferencesAnotherModulesSchema(t *testing.T) {
	files := collectSchemaOwnedFiles(t)
	if len(files) == 0 {
		t.Fatal("no schema-owned files found")
	}
	for _, schema := range moduleSchemas {
		needle := schema + "."
		for _, f := range files {
			if f.owner == schema {
				continue
			}
			if strings.Contains(f.body, needle) {
				t.Errorf("%s (owned by %q) references schema %q via %q", f.path, f.owner, schema, needle)
			}
		}
	}
}

// TestIdentityBaseline_AppliesAndSeedsBuiltInRoles proves
// 00002_identity_baseline.sql applies cleanly and that the three built-in
// roles it seeds exist with their fixed ids.
func TestIdentityBaseline_AppliesAndSeedsBuiltInRoles(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	want := map[string]string{
		"00000000-0000-4000-8000-000000000001": "SystemAdmin",
		"00000000-0000-4000-8000-000000000002": "Owner",
		"00000000-0000-4000-8000-000000000003": "User",
	}

	rows, err := pool.Query(ctx, "SELECT id::text, name FROM identity.roles WHERE is_built_in ORDER BY name")
	if err != nil {
		t.Fatalf("query built-in roles: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[id] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("got %d built-in roles %v, want %d %v", len(got), got, len(want), want)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("role %s = %q, want %q", id, got[id], name)
		}
	}
}
