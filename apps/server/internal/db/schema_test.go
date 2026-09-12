package db_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

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

// applyUpDownUp applies every migration, rolls back the most recent one
// (00003_customers_baseline.sql, at the time this test was written), and
// applies it again, proving the customers baseline's down migration is a
// clean inverse of its up migration.
func applyUpDownUp(t *testing.T, databaseURL string) {
	t.Helper()
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}
	sqlDB := sql.OpenDB(stdlib.GetConnector(*cfg))
	defer func() { _ = sqlDB.Close() }()

	dir, err := fs.Sub(db.MigrationsFS, "migrations")
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// TestCustomersBaseline_AppliesAndIsIdempotent proves
// 00003_customers_baseline.sql applies, rolls back, and re-applies cleanly,
// and that the tenant drop landed exactly where the inventory says it
// should: customer_number unique alone, the revisions pair unique alone,
// the customers_contacts PK without tenant_id, and no tenant_id column
// anywhere in the schema.
func TestCustomersBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	wantTables := []string{
		"contacts",
		"counters",
		"customers",
		"customers_contacts",
		"customers_timeline_entries",
		"customers_timeline_entries_revisions",
	}
	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'customers' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	var gotTables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		gotTables = append(gotTables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(gotTables) != len(wantTables) {
		t.Fatalf("tables = %v, want %v", gotTables, wantTables)
	}
	for i, name := range wantTables {
		if gotTables[i] != name {
			t.Errorf("tables = %v, want %v", gotTables, wantTables)
			break
		}
	}

	if cols := indexColumns(t, ctx, pool, "customers", "ux_customers_customer_number"); len(cols) != 1 || cols[0] != "customer_number" {
		t.Errorf("ux_customers_customer_number columns = %v, want [customer_number]", cols)
	}

	if cols := indexColumns(t, ctx, pool, "customers", "ux_customers_timeline_entries_revisions_entry_revision"); !equalStrings(cols, []string{"customer_timeline_entry_id", "revision_number"}) {
		t.Errorf("ux_customers_timeline_entries_revisions_entry_revision columns = %v, want [customer_timeline_entry_id revision_number]", cols)
	}

	if cols := primaryKeyColumns(t, ctx, pool, "customers", "customers_contacts"); !equalStrings(cols, []string{"customer_id", "contact_id"}) {
		t.Errorf("customers_contacts primary key columns = %v, want [customer_id contact_id]", cols)
	}

	var tenantIDColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'customers' AND column_name = 'tenant_id'`).Scan(&tenantIDColumns); err != nil {
		t.Fatalf("count tenant_id columns: %v", err)
	}
	if tenantIDColumns != 0 {
		t.Errorf("found %d tenant_id column(s) in schema customers, want 0", tenantIDColumns)
	}
}

// indexColumns returns a named index's columns, in index order.
func indexColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, indexName string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_class ic ON ic.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = ic.relnamespace
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE n.nspname = $1 AND ic.relname = $2
		ORDER BY array_position(i.indkey, a.attnum)`, schema, indexName)
	if err != nil {
		t.Fatalf("query index columns for %s: %v", indexName, err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan index column: %v", err)
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return cols
}

// primaryKeyColumns returns a table's primary key columns, in key order.
func primaryKeyColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, table string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT a.attname
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY(c.conkey)
		WHERE c.contype = 'p' AND c.conrelid = ($1 || '.' || $2)::regclass
		ORDER BY array_position(c.conkey, a.attnum)`, schema, table)
	if err != nil {
		t.Fatalf("query primary key columns for %s.%s: %v", schema, table, err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan primary key column: %v", err)
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return cols
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
