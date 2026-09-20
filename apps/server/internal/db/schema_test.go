package db_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// moduleSchemas are the PostgreSQL schemas owned by one module each. The
// platform schema is deliberately not one of these: internal/ratelimit
// reaches it from outside its own module, by design (Global Constraints).
var moduleSchemas = []string{"identity", "customers", "products", "energy", "communications", "projects", "time", "expenses"}

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

// schemaReference matches schema as SQL qualifies a name with it: the whole
// word, then a dot, then the start of an identifier — in any case, with the
// schema or the name double-quoted or not, and with whitespace around the
// dot, since Postgres accepts every one of those spellings. The word boundary
// and the identifier keep an English word that happens to name a schema from
// counting ("SELECT runtime.x"); comments, where "time" ends sentences and
// prefixes Go types, are stripped before it runs (sqlComments).
func schemaReference(schema string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(^|[^a-z0-9_])"?` + regexp.QuoteMeta(schema) + `"?\s*\.\s*"?[a-z_]`)
}

// sqlComments matches SQL's two comment forms: a -- line comment up to the
// end of its line, and a /* */ block comment across lines. Nested block
// comments are not modelled; none of the scanned files uses one.
var sqlComments = regexp.MustCompile(`(?s)--[^\n]*|/\*.*?\*/`)

// referencesSchema reports whether body — a migration or query file —
// qualifies a name with schema anywhere outside a comment. A comment is
// replaced by a space rather than removed, so it cannot join the words on
// either side of it into a reference.
func referencesSchema(schema, body string) bool {
	return schemaReference(schema).MatchString(sqlComments.ReplaceAllString(body, " "))
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
		for _, f := range files {
			if f.owner == schema {
				continue
			}
			if referencesSchema(schema, f.body) {
				match := schemaReference(schema).FindString(sqlComments.ReplaceAllString(f.body, " "))
				t.Errorf("%s (owned by %q) references schema %q via %q", f.path, f.owner, schema, match)
			}
		}
	}
}

// TestSchemaReference_MatchesQualifiedNamesOnly pins what the cross-schema
// scan counts as a reference, so tightening it for an English-word schema
// name never quietly stops it catching a real one.
func TestSchemaReference_MatchesQualifiedNamesOnly(t *testing.T) {
	for _, tc := range []struct {
		schema, text string
		want         bool
	}{
		{"time", "SELECT * FROM time.entries", true},
		{"time", "(time.settings)", true},
		{"time", "time.entries at the start of a file", true},
		{"customers", "JOIN customers.customers c ON", true},
		{"time", "-- must not be queued a second time.", false},
		{"time", "-- so sqlc maps the Go parameter to time.Time", false},
		{"time", "-- the start time.\n-- next line", false},
		{"time", "SELECT runtime.x", false},
		{"time", `SELECT * FROM time."entries"`, true},
		{"time", "SELECT * FROM time.Entries", true},
		{"time", `SELECT * FROM "time".entries`, true},
		{"time", "SELECT * FROM TIME.entries", true},
		{"time", "SELECT * FROM time . entries", true},
		{"time", "SELECT 1 /* time.Time */", false},
		{"time", "SELECT 1 /* a\nsecond time.\n */ FROM x", false},
		{"time", "SELECT 1 -- a second time.\nFROM x", false},
	} {
		if got := referencesSchema(tc.schema, tc.text); got != tc.want {
			t.Errorf("referencesSchema(%q, %q) = %v, want %v", tc.schema, tc.text, got, tc.want)
		}
	}
}

// sqlcSchemaFiles returns the migration file names a module's sqlc.yaml
// declares under its `schema:` list, in order.
func sqlcSchemaFiles(t *testing.T, module string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", module, "sqlc.yaml"))
	if err != nil {
		t.Fatalf("read %s/sqlc.yaml: %v", module, err)
	}
	var files []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "- ../db/migrations/"); ok {
			files = append(files, rest)
		}
	}
	return files
}

// TestSqlcSchemaListsOnlyTheModulesOwnMigrations proves each module's sqlc
// config compiles against its own migrations and nothing else. Pointing sqlc
// at the whole migrations directory makes every module's generated store carry
// every other module's table types — a boundary hole neither depguard (an
// import check) nor the cross-schema text scan above (SQL files only) would
// catch, because the foreign type lives in the module's own package.
func TestSqlcSchemaListsOnlyTheModulesOwnMigrations(t *testing.T) {
	entries, err := fs.ReadDir(db.MigrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	owned := map[string][]string{}
	for _, e := range entries {
		owner := migrationOwner(e.Name())
		owned[owner] = append(owned[owner], e.Name())
	}
	for _, module := range moduleSchemas {
		want := owned[module]
		if len(want) == 0 {
			t.Errorf("%s: no migration named NNNNN_%s_*.sql", module, module)
			continue
		}
		got := sqlcSchemaFiles(t, module)
		if !equalStrings(got, want) {
			t.Errorf("%s/sqlc.yaml schema list = %v, want exactly its own migrations %v", module, got, want)
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

// applyUpDownUp applies every migration, rolls all the way back down to
// (and including) the migration numbered version — goose's version is a
// migration file's leading number, so 00003_customers_baseline.sql is
// version 3 — and applies everything again, proving that migration's down
// path is a clean inverse of its up path.
//
// It uses DownTo(version-1) rather than a single Down(), deliberately:
// Down() only ever rolls back the single most-recently-applied migration,
// so calling it once genuinely exercises version's own down script only
// while version happens to be the newest migration in the tree. The moment
// a later migration lands on top, a bare Down() would roll back that later
// migration instead, and version's down path would silently stop being
// tested — exactly the blind spot a caller adding a baseline in this style
// must not inherit. DownTo(version-1) rolls back every migration from the
// current head down through version, in descending order, so version's own
// down runs (and is proven to compose with whatever sits above it)
// regardless of how many migrations that is.
func applyUpDownUp(t *testing.T, databaseURL string, version int64) {
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
	if _, err := provider.DownTo(ctx, version-1); err != nil {
		t.Fatalf("down to version %d: %v", version-1, err)
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
	applyUpDownUp(t, url, 3) // 00003_customers_baseline.sql

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

// TestProductsBaseline_AppliesAndIsIdempotent proves
// 00004_products_baseline.sql applies, rolls back, and re-applies cleanly,
// and that the tenant drop landed exactly where the inventory says it
// should: sku unique alone, no tenant_id column anywhere in the schema.
func TestProductsBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 4) // 00004_products_baseline.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	wantTables := []string{
		"product_categories",
		"product_prices",
		"product_variants",
		"products",
		"tax_categories",
	}
	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'products' ORDER BY table_name`)
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

	if cols := indexColumns(t, ctx, pool, "products", "ux_product_variants_sku"); len(cols) != 1 || cols[0] != "sku" {
		t.Errorf("ux_product_variants_sku columns = %v, want [sku]", cols)
	}

	var tenantIDColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'products' AND column_name = 'tenant_id'`).Scan(&tenantIDColumns); err != nil {
		t.Fatalf("count tenant_id columns: %v", err)
	}
	if tenantIDColumns != 0 {
		t.Errorf("found %d tenant_id column(s) in schema products, want 0", tenantIDColumns)
	}
}

// TestProductCategories_RootSiblingNamesAreNullsNotDistinct proves the
// ux_product_categories_parent_id_name index is NULLS NOT DISTINCT, not a
// plain UNIQUE: two root categories (parent_id IS NULL) sharing a name must
// collide, exactly like two categories under the same non-null parent. A
// plain `UNIQUE (parent_id, name)` would let this insert through silently,
// since Postgres treats every NULL parent_id as distinct from every other —
// that is exactly the mutation this test exists to catch.
func TestProductCategories_RootSiblingNamesAreNullsNotDistinct(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `INSERT INTO products.product_categories (name, parent_id) VALUES ('Furniture', NULL)`); err != nil {
		t.Fatalf("insert first root category: %v", err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO products.product_categories (name, parent_id) VALUES ('Furniture', NULL)`)
	if !isUniqueViolation(err) {
		t.Fatalf("insert second root category named Furniture: err = %v, want a unique_violation", err)
	}

	// Sanity check the same rule still applies to two categories under a real
	// (non-null) parent, so the test is exercising NULLS NOT DISTINCT
	// specifically, not just uniqueness in general.
	var parentID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.product_categories (name, parent_id) VALUES ('Seating', NULL) RETURNING id`).Scan(&parentID); err != nil {
		t.Fatalf("insert parent category: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products.product_categories (name, parent_id) VALUES ('Chairs', $1)`, parentID); err != nil {
		t.Fatalf("insert first child category: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO products.product_categories (name, parent_id) VALUES ('Chairs', $1)`, parentID)
	if !isUniqueViolation(err) {
		t.Fatalf("insert second child category named Chairs: err = %v, want a unique_violation", err)
	}
}

// TestProductVariants_BarcodeUniqueIsPartial proves ux_product_variants_barcode
// is a partial unique index (WHERE barcode IS NOT NULL): any number of
// variants may leave barcode unset, but two variants sharing the same
// non-null barcode collide.
func TestProductVariants_BarcodeUniqueIsPartial(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	productID := insertTestProduct(t, ctx, pool)

	if _, err := pool.Exec(ctx, `INSERT INTO products.product_variants (product_id, sku, barcode, unit, created_at, updated_at) VALUES ($1, 'SKU-1', NULL, 'pcs', now(), now())`, productID); err != nil {
		t.Fatalf("insert first variant with a NULL barcode: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products.product_variants (product_id, sku, barcode, unit, created_at, updated_at) VALUES ($1, 'SKU-2', NULL, 'pcs', now(), now())`, productID); err != nil {
		t.Fatalf("insert second variant with a NULL barcode: %v, want no error (the unique index is partial)", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO products.product_variants (product_id, sku, barcode, unit, created_at, updated_at) VALUES ($1, 'SKU-3', '07012345678902', 'pcs', now(), now())`, productID); err != nil {
		t.Fatalf("insert first variant with a barcode: %v", err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO products.product_variants (product_id, sku, barcode, unit, created_at, updated_at) VALUES ($1, 'SKU-4', '07012345678902', 'pcs', now(), now())`, productID)
	if !isUniqueViolation(err) {
		t.Fatalf("insert second variant with the same barcode: err = %v, want a unique_violation", err)
	}
}

// TestProductPrices_HasNoExclusionConstraint proves product_prices carries no
// exclusion (or other overlap-guarding) constraint: this is deliberate
// (products inventory §3/§4/§7 oddity 6, and the migration's own comment),
// not an oversight, so two prices for the same variant and currency with
// overlapping validity windows must insert without error. If a later change
// ever adds DB-level overlap enforcement here, it must be a conscious
// decision that also updates this test, not a change this test lets through
// by accident.
func TestProductPrices_HasNoExclusionConstraint(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	productID := insertTestProduct(t, ctx, pool)
	var variantID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.product_variants (product_id, sku, unit, created_at, updated_at) VALUES ($1, 'SKU-1', 'pcs', now(), now()) RETURNING id`, productID).Scan(&variantID); err != nil {
		t.Fatalf("insert variant: %v", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO products.product_prices (variant_id, currency, amount, valid_from, valid_to) VALUES ($1, 'USD', 10.00, NULL, NULL)`, variantID); err != nil {
		t.Fatalf("insert base price: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products.product_prices (variant_id, currency, amount, valid_from, valid_to) VALUES ($1, 'USD', 8.00, NULL, NULL)`, variantID); err != nil {
		t.Fatalf("insert a second, fully overlapping open-ended price: %v, want no error (no exclusion constraint exists on this table)", err)
	}

	var exclusionConstraints int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid JOIN pg_namespace n ON n.oid = t.relnamespace WHERE n.nspname = 'products' AND t.relname = 'product_prices' AND c.contype = 'x'`).Scan(&exclusionConstraints); err != nil {
		t.Fatalf("count exclusion constraints: %v", err)
	}
	if exclusionConstraints != 0 {
		t.Errorf("found %d exclusion constraint(s) on products.product_prices, want 0", exclusionConstraints)
	}
}

// TestProductCategories_ParentIsRestrict proves product_categories.parent_id
// is a Restrict FK (products inventory §3): a category that is still a
// parent cannot be deleted. A Cascade FK here would silently delete an
// entire subtree instead.
//
// Postgres's literal RESTRICT keyword (unlike the FK default, NO ACTION)
// raises SQLSTATE 23001 (restrict_violation), not 23503
// (foreign_key_violation) — confirmed empirically writing this test. The
// products inventory's §4 discussion of "Restrict-FK races are unmapped to
// 409" talks about 23503; whichever later task wires the 409 mapping needs
// to know the actual code a literal SQL RESTRICT clause raises is 23001, so
// it maps the code Postgres really sends, not the one a EF-Core-shaped
// mental model predicts.
func TestProductCategories_ParentIsRestrict(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	var parentID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.product_categories (name, parent_id) VALUES ('Furniture', NULL) RETURNING id`).Scan(&parentID); err != nil {
		t.Fatalf("insert parent category: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products.product_categories (name, parent_id) VALUES ('Chairs', $1)`, parentID); err != nil {
		t.Fatalf("insert child category: %v", err)
	}

	_, err := pool.Exec(ctx, `DELETE FROM products.product_categories WHERE id = $1`, parentID)
	if !isRestrictViolation(err) {
		t.Fatalf("delete a category with a child: err = %v, want a restrict_violation", err)
	}
}

// TestProducts_CategoryAndTaxCategoryAreRestrict proves products.category_id
// and products.tax_category_id are both Restrict FKs (products inventory
// §3): a category or tax category still referenced by a product cannot be
// deleted. A Cascade FK on either would silently delete or orphan products
// instead. See TestProductCategories_ParentIsRestrict's comment on why the
// expected SQLSTATE is 23001, not 23503.
func TestProducts_CategoryAndTaxCategoryAreRestrict(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	var categoryID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.product_categories (name, parent_id) VALUES ('Furniture', NULL) RETURNING id`).Scan(&categoryID); err != nil {
		t.Fatalf("insert category: %v", err)
	}
	var taxCategoryID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.tax_categories (name, kind, rate, created_at, updated_at) VALUES ('Standard', 'Standard', 0.25, now(), now()) RETURNING id`).Scan(&taxCategoryID); err != nil {
		t.Fatalf("insert tax category: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO products.products (name, category_id, type, status, tax_category_id, created_at, updated_at) VALUES ('Chair', $1, 'Goods', 'Draft', $2, now(), now())`, categoryID, taxCategoryID); err != nil {
		t.Fatalf("insert product: %v", err)
	}

	_, err := pool.Exec(ctx, `DELETE FROM products.product_categories WHERE id = $1`, categoryID)
	if !isRestrictViolation(err) {
		t.Fatalf("delete a category referenced by a product: err = %v, want a restrict_violation", err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM products.tax_categories WHERE id = $1`, taxCategoryID)
	if !isRestrictViolation(err) {
		t.Fatalf("delete a tax category referenced by a product: err = %v, want a restrict_violation", err)
	}
}

// TestProductVariantsAndPrices_CascadeFromTheirParent proves
// product_variants.product_id and product_prices.variant_id are both
// Cascade FKs (products inventory §3): deleting a product deletes its
// variants, and deleting a variant deletes its prices. A Restrict FK on
// either would leave products (or variants) impossible to delete once they
// ever gained a variant (or price) — the opposite of category/tax-category's
// Restrict, and a real behavioral difference this test pins.
func TestProductVariantsAndPrices_CascadeFromTheirParent(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	productID := insertTestProduct(t, ctx, pool)
	var variantID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.product_variants (product_id, sku, unit, created_at, updated_at) VALUES ($1, 'SKU-1', 'pcs', now(), now()) RETURNING id`, productID).Scan(&variantID); err != nil {
		t.Fatalf("insert variant: %v", err)
	}
	var priceID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.product_prices (variant_id, currency, amount) VALUES ($1, 'USD', 10.00) RETURNING id`, variantID).Scan(&priceID); err != nil {
		t.Fatalf("insert price: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM products.products WHERE id = $1`, productID); err != nil {
		t.Fatalf("delete product: %v, want cascade to succeed", err)
	}

	var variants, prices int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM products.product_variants WHERE id = $1`, variantID).Scan(&variants); err != nil {
		t.Fatalf("count remaining variants: %v", err)
	}
	if variants != 0 {
		t.Errorf("variant %d survived deleting its product, want it cascade-deleted", variantID)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM products.product_prices WHERE id = $1`, priceID).Scan(&prices); err != nil {
		t.Fatalf("count remaining prices: %v", err)
	}
	if prices != 0 {
		t.Errorf("price %d survived deleting its product, want it cascade-deleted along with its variant", priceID)
	}
}

// TestEnergyBaseline_AppliesAndIsIdempotent proves
// 00005_energy_baseline.sql applies, rolls back, and re-applies cleanly —
// the extension and the partitioned table make this baseline's down path
// less trivial than the previous three (dropping the schema must also take
// the function, every pre-created monthly partition, and the exclusion
// constraint with it) — and that the tenant drop landed exactly where the
// inventory says it should: no tenant_id column anywhere in the schema, and
// consumption_intervals' primary key carries start alongside id (energy
// inventory §3, the partition-key requirement).
func TestEnergyBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 5) // 00005_energy_baseline.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// information_schema.tables lists every pre-created monthly partition as
	// its own table, so the base-table list is read from pg_class instead,
	// filtered to relations that are not themselves a partition
	// (relispartition = false catches consumption_intervals's own children
	// but keeps the partitioned parent itself, relkind 'p').
	wantTables := []string{
		"consumption_intervals",
		"metering_points",
		"meters",
		"supply_periods",
	}
	rows, err := pool.Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'energy' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		ORDER BY c.relname`)
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

	var extensions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_extension WHERE extname = 'btree_gist'`).Scan(&extensions); err != nil {
		t.Fatalf("count btree_gist extension: %v", err)
	}
	if extensions != 1 {
		t.Errorf("found %d btree_gist extension(s), want 1", extensions)
	}

	var exclusionConstraints int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = 'energy' AND t.relname = 'supply_periods' AND c.contype = 'x' AND c.conname = 'supply_periods_no_overlap'`).Scan(&exclusionConstraints); err != nil {
		t.Fatalf("count supply_periods_no_overlap: %v", err)
	}
	if exclusionConstraints != 1 {
		t.Errorf("found %d supply_periods_no_overlap exclusion constraint(s), want 1", exclusionConstraints)
	}

	if cols := indexColumns(t, ctx, pool, "energy", "ux_metering_points_gsrn"); len(cols) != 1 || cols[0] != "gsrn" {
		t.Errorf("ux_metering_points_gsrn columns = %v, want [gsrn]", cols)
	}
	if cols := indexColumns(t, ctx, pool, "energy", "ux_meters_metering_point_id_active"); len(cols) != 1 || cols[0] != "metering_point_id" {
		t.Errorf("ux_meters_metering_point_id_active columns = %v, want [metering_point_id]", cols)
	}
	if cols := primaryKeyColumns(t, ctx, pool, "energy", "consumption_intervals"); !equalStrings(cols, []string{"id", "start"}) {
		t.Errorf("consumption_intervals primary key columns = %v, want [id start] (Postgres requires the partition key in every unique/PK index)", cols)
	}

	var tenantIDColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'energy' AND column_name = 'tenant_id'`).Scan(&tenantIDColumns); err != nil {
		t.Fatalf("count tenant_id columns: %v", err)
	}
	if tenantIDColumns != 0 {
		t.Errorf("found %d tenant_id column(s) in schema energy, want 0", tenantIDColumns)
	}
}

// insertTestMeteringPoint inserts one metering point and returns its id. It
// exists only to satisfy meters/supply_periods/consumption_intervals' NOT
// NULL metering_point_id FK for tests that need a real parent row.
func insertTestMeteringPoint(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int32 {
	t.Helper()
	var id int32
	if err := pool.QueryRow(ctx, `
		INSERT INTO energy.metering_points
			(gsrn, street_address, postal_code, city, country_code, price_area, connection_status, created_at, updated_at)
		VALUES
			('123456789012345678', 'Storgata 1', '0001', 'Oslo', 'NO', 'NO1', 'New', now(), now())
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert metering point: %v", err)
	}
	return id
}

// TestSupplyPeriods_ExclusionConstraintRejectsOverlappingNonCancelledPeriods
// proves the GiST exclusion constraint's overlap half: two non-Cancelled
// supply periods on the same metering point with overlapping [start, end)
// ranges cannot both exist (energy inventory §3.2, §5 — this is what
// actually decides concurrent-create races, not the application's own
// pre-checks, which this schema-level test deliberately bypasses).
func TestSupplyPeriods_ExclusionConstraintRejectsOverlappingNonCancelledPeriods(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	meteringPointID := insertTestMeteringPoint(t, ctx, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, "end", status)
		VALUES ($1, 1001, '2026-01-01T00:00:00Z', '2026-02-01T00:00:00Z', 'Active')`, meteringPointID); err != nil {
		t.Fatalf("insert first active period: %v", err)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, "end", status)
		VALUES ($1, 1002, '2026-01-15T00:00:00Z', '2026-03-01T00:00:00Z', 'Ended')`, meteringPointID)
	if !isExclusionViolation(err) {
		t.Fatalf("insert overlapping Ended period: err = %v, want an exclusion_violation", err)
	}
}

// TestSupplyPeriods_ExclusionConstraintAllowsOverlappingCancelledPeriods
// proves the constraint's partial WHERE (status <> 'Cancelled') half: two
// overlapping periods on the same metering point are admitted as soon as
// one of them is Cancelled, since a Cancelled row never participates in the
// exclusion index at all (energy inventory §3.2). Together with the
// previous test, this pins both halves of the WHERE clause — losing either
// half (dropping the clause entirely, or inverting it) would flip one of
// these two tests from pass to fail.
func TestSupplyPeriods_ExclusionConstraintAllowsOverlappingCancelledPeriods(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	meteringPointID := insertTestMeteringPoint(t, ctx, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, "end", status)
		VALUES ($1, 1001, '2026-01-01T00:00:00Z', '2026-02-01T00:00:00Z', 'Cancelled')`, meteringPointID); err != nil {
		t.Fatalf("insert cancelled period: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, "end", status)
		VALUES ($1, 1002, '2026-01-15T00:00:00Z', '2026-03-01T00:00:00Z', 'Active')`, meteringPointID); err != nil {
		t.Fatalf("insert overlapping active period against a cancelled one: err = %v, want no error", err)
	}
}

// TestSupplyPeriods_ExclusionConstraintAdmitsAdjacentNonCancelledPeriods
// proves the constraint's half-open '[)' boundary (energy inventory §3.2,
// §2.3's Overlaps predicate; .NET asserts this directly in
// SupplyPeriodTests.cs:8-12, Adjacent_periods_do_not_overlap): a period
// ending exactly when the next one starts does not overlap it, so both may
// exist even though both are non-Cancelled — this is exactly the shape a
// supply-period switch produces (the new period starts precisely where the
// ended one stops, so a strict '[]' boundary would reject every legitimate
// switch). Neither of the two tests above probes the boundary itself —
// mutating '[)' to '[]' leaves both green — so this is the one that must
// flip: the shared instant 2026-02-01T00:00:00Z, inclusive on the first
// period's end and inclusive on the second period's start, only collides
// under '[]'.
func TestSupplyPeriods_ExclusionConstraintAdmitsAdjacentNonCancelledPeriods(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	meteringPointID := insertTestMeteringPoint(t, ctx, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, "end", status)
		VALUES ($1, 1001, '2026-01-01T00:00:00Z', '2026-02-01T00:00:00Z', 'Ended')`, meteringPointID); err != nil {
		t.Fatalf("insert first period: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.supply_periods (metering_point_id, customer_id, start, "end", status)
		VALUES ($1, 1002, '2026-02-01T00:00:00Z', '2026-03-01T00:00:00Z', 'Active')`, meteringPointID); err != nil {
		t.Fatalf("insert second period starting exactly when the first ends: %v, want no error (half-open '[)' boundary — SupplyPeriodTests.cs's Adjacent_periods_do_not_overlap)", err)
	}
}

// TestMeters_ActiveMeterUniqueIsPartial proves ux_meters_metering_point_id_active
// is a partial unique index (WHERE removed_at IS NULL): at most one live
// (removed_at IS NULL) meter may exist per metering point, but any number of
// removed ones may — and once the live meter is itself removed, a new live
// meter may be installed (energy inventory §2.2/§3).
func TestMeters_ActiveMeterUniqueIsPartial(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	meteringPointID := insertTestMeteringPoint(t, ctx, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.meters (metering_point_id, meter_number, installed_at, removed_at)
		VALUES ($1, 'M-1', '2026-01-01T00:00:00Z', NULL)`, meteringPointID); err != nil {
		t.Fatalf("insert first live meter: %v", err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO energy.meters (metering_point_id, meter_number, installed_at, removed_at)
		VALUES ($1, 'M-2', '2026-02-01T00:00:00Z', NULL)`, meteringPointID)
	if !isUniqueViolation(err) {
		t.Fatalf("insert second live meter: err = %v, want a unique_violation", err)
	}

	// Any number of removed meters may coexist: the partiality of the index
	// is what makes this legal, unlike a plain UNIQUE (metering_point_id).
	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.meters (metering_point_id, meter_number, installed_at, removed_at)
		VALUES ($1, 'M-3', '2025-01-01T00:00:00Z', '2025-06-01T00:00:00Z')`, meteringPointID); err != nil {
		t.Fatalf("insert first removed meter: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.meters (metering_point_id, meter_number, installed_at, removed_at)
		VALUES ($1, 'M-4', '2025-06-01T00:00:00Z', '2025-12-01T00:00:00Z')`, meteringPointID); err != nil {
		t.Fatalf("insert second removed meter: %v, want no error (the unique index is partial)", err)
	}

	// Removing the live meter frees the slot for a new one.
	if _, err := pool.Exec(ctx, `UPDATE energy.meters SET removed_at = '2026-03-01T00:00:00Z' WHERE metering_point_id = $1 AND removed_at IS NULL`, meteringPointID); err != nil {
		t.Fatalf("remove the live meter: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.meters (metering_point_id, meter_number, installed_at, removed_at)
		VALUES ($1, 'M-5', '2026-03-01T00:00:00Z', NULL)`, meteringPointID); err != nil {
		t.Fatalf("insert a new live meter after removing the old one: %v, want no error", err)
	}
}

// TestConsumptionIntervals_EnsurePartitionCreatesMonthlyPartition proves
// energy.ensure_consumption_partition (energy inventory §3.3, ported
// verbatim from the .NET migration) creates a real, correctly-bounded
// monthly partition on demand, and that a row whose start falls in that
// month can then be written. 2031-03 is chosen because it falls well
// outside the ±12 month window the migration itself pre-creates around the
// real clock (energy inventory §3.3), so this test can only pass if the
// function itself works, not because the partition already existed.
func TestConsumptionIntervals_EnsurePartitionCreatesMonthlyPartition(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `SELECT energy.ensure_consumption_partition('2031-03-10'::date)`); err != nil {
		t.Fatalf("ensure_consumption_partition: %v", err)
	}
	// Idempotent: a second call for the same month must not error (the
	// function's own CREATE TABLE IF NOT EXISTS).
	if _, err := pool.Exec(ctx, `SELECT energy.ensure_consumption_partition('2031-03-25'::date)`); err != nil {
		t.Fatalf("ensure_consumption_partition, second call for the same month: %v", err)
	}

	var bound string
	err := pool.QueryRow(ctx, `
		SELECT pg_get_expr(c.relpartbound, c.oid)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'energy' AND c.relname = 'consumption_intervals_2031_03'`).Scan(&bound)
	if err != nil {
		t.Fatalf("find partition consumption_intervals_2031_03: %v (want a partition table with this exact name — YYYY_MM of the truncated month)", err)
	}
	if !strings.Contains(bound, "2031-03-01") || !strings.Contains(bound, "2031-04-01") {
		t.Errorf("consumption_intervals_2031_03 bound = %q, want it to span [2031-03-01, 2031-04-01)", bound)
	}

	meteringPointID := insertTestMeteringPoint(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO energy.consumption_intervals (metering_point_id, start, "end", quantity_kwh, quality, source, received_at)
		VALUES ($1, '2031-03-10T00:00:00Z', '2031-03-10T01:00:00Z', 1.5, 'Measured', 'Elhub', now())`, meteringPointID); err != nil {
		t.Fatalf("insert a consumption interval into the newly created partition: %v", err)
	}

	var rowsInPartition int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM energy.consumption_intervals_2031_03`).Scan(&rowsInPartition); err != nil {
		t.Fatalf("count rows in partition: %v", err)
	}
	if rowsInPartition != 1 {
		t.Errorf("consumption_intervals_2031_03 has %d row(s), want 1 (the written interval should have routed into this partition)", rowsInPartition)
	}
}

// TestCommunicationsBaseline_AppliesAndIsIdempotent proves
// 00006_communications_baseline.sql applies, rolls back, and re-applies
// cleanly, and that the tenant drop landed exactly where the inventory says
// it should: exactly the 19 tables the keep/drop verdict leaves (the
// inventory's 21 entities minus inbound_receipts and inbound_email_jobs,
// communications inventory §7, §9) and no tenant_id column anywhere in the
// schema.
func TestCommunicationsBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 6) // 00006_communications_baseline.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	wantTables := []string{
		"ai_interactions",
		"attachment_cleanup_records",
		"attachment_uploads",
		"channel_credentials",
		"channels",
		"conversation_customer_candidates",
		"conversation_messages",
		"conversation_participants",
		"conversation_read_states",
		"conversation_tags",
		"conversations",
		"idempotency_records",
		"message_attachments",
		"message_deliveries",
		"message_events",
		"outbox_jobs",
		"participants",
		"suppressions",
		"tags",
	}
	rows, err := pool.Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'communications' AND c.relkind = 'r'
		ORDER BY c.relname`)
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
	if !equalStrings(gotTables, wantTables) {
		t.Fatalf("tables = %v, want %v", gotTables, wantTables)
	}

	var tenantIDColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'communications' AND column_name = 'tenant_id'`).Scan(&tenantIDColumns); err != nil {
		t.Fatalf("count tenant_id columns: %v", err)
	}
	if tenantIDColumns != 0 {
		t.Errorf("found %d tenant_id column(s) in schema communications, want 0", tenantIDColumns)
	}
}

// insertTestChannel inserts one minimal SMTP channel and returns its id.
// The address is derived from the id so repeat calls within one test never
// collide with ux_channels_type_address.
func insertTestChannel(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.channels (id, type, address, created_at)
		VALUES ($1, 'email', $2, now())`, id, id.String()+"@example.com"); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	return id
}

// insertTestConversation inserts one minimal conversation on channelID and
// returns its id.
func insertTestConversation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, channelID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.conversations (id, channel_id, last_activity_at, created_at)
		VALUES ($1, $2, now(), now())`, id, channelID); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	return id
}

// insertTestConversationMessage inserts one minimal outbound message on
// conversationID and returns its id.
func insertTestConversationMessage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, conversationID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.conversation_messages (id, conversation_id, direction, occurred_at, created_at)
		VALUES ($1, $2, 'outbound', now(), now())`, id, conversationID); err != nil {
		t.Fatalf("insert conversation message: %v", err)
	}
	return id
}

// TestCommunicationsBaseline_ConversationsFeedIndexIsFullyDescending pins
// spec D6 and dispatch correction 1: the .NET index is
// (tenant_id ASC, status DESC, last_activity_at DESC) with a *positional*
// descending-flag array keyed by column position, not name. Dropping the
// leading tenant_id column means the remaining flags must shift left too —
// the natural-looking mistranslation "(status, last_activity_at DESC)"
// (status left ascending) is wrong. This reads the direction bits straight
// from pg_index (not the migration's DDL text), so it fails if either
// column's DESC flag is ever dropped or the two columns are reordered.
func TestCommunicationsBaseline_ConversationsFeedIndexIsFullyDescending(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	cols := indexColumns(t, ctx, pool, "communications", "ix_conversations_status_last_activity_at")
	if !equalStrings(cols, []string{"status", "last_activity_at"}) {
		t.Fatalf("ix_conversations_status_last_activity_at columns = %v, want [status last_activity_at]", cols)
	}
	directions := indexColumnDirections(t, ctx, pool, "communications", "ix_conversations_status_last_activity_at")
	if len(directions) != 2 || !directions[0] || !directions[1] {
		t.Errorf("ix_conversations_status_last_activity_at descending flags = %v, want [true true] (both status and last_activity_at descending, per D6)", directions)
	}
}

// TestCommunicationsBaseline_MessageEventsDeliveryIdIsRestrict pins
// dispatch correction 2 and inventory §10 item 8, §12.1, §19.1 item 6:
// message_events.delivery_id → message_deliveries is the only RESTRICT FK
// between the two tables retention batch-deletes together, and it dictates
// delete order — message_events before message_deliveries. A delivery
// still referenced by an event cannot be deleted; porting this FK as
// Cascade would silently corrupt the event log during retention, and NO
// ACTION (the FK default) would raise a different SQLSTATE than a literal
// RESTRICT does (see isRestrictViolation's comment) — either mistake trips
// this test.
func TestCommunicationsBaseline_MessageEventsDeliveryIdIsRestrict(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	channelID := insertTestChannel(t, ctx, pool)
	conversationID := insertTestConversation(t, ctx, pool, channelID)
	messageID := insertTestConversationMessage(t, ctx, pool, conversationID)

	deliveryID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.message_deliveries (id, message_id, recipient_address, recipient_type, attempts, created_at)
		VALUES ($1, $2, 'customer@example.com', 'to', 0, now())`, deliveryID, messageID); err != nil {
		t.Fatalf("insert message delivery: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.message_events (id, message_id, delivery_id, event_type, occurred_at)
		VALUES ($1, $2, $3, 'accepted', now())`, uuid.New(), messageID, deliveryID); err != nil {
		t.Fatalf("insert message event: %v", err)
	}

	_, err := pool.Exec(ctx, `DELETE FROM communications.message_deliveries WHERE id = $1`, deliveryID)
	if !isRestrictViolation(err) {
		t.Fatalf("delete a delivery referenced by an event: err = %v, want a restrict_violation", err)
	}
}

// TestCommunicationsBaseline_ChannelRestrictsProtectHistory pins the
// schema's other two RESTRICT FKs, which the brief undercounted (dispatch
// correction 2): conversations.channel_id and participants.channel_id →
// channels are also RESTRICT, so a channel with any conversation or
// participant history cannot be deleted — channels are deactivated via
// is_active = false instead (inventory §10 item 9).
func TestCommunicationsBaseline_ChannelRestrictsProtectHistory(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	channelWithConversation := insertTestChannel(t, ctx, pool)
	insertTestConversation(t, ctx, pool, channelWithConversation)
	_, err := pool.Exec(ctx, `DELETE FROM communications.channels WHERE id = $1`, channelWithConversation)
	if !isRestrictViolation(err) {
		t.Fatalf("delete a channel with a conversation: err = %v, want a restrict_violation", err)
	}

	channelWithParticipant := insertTestChannel(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.participants (id, channel_id, address, created_at)
		VALUES ($1, $2, 'someone@example.com', now())`, uuid.New(), channelWithParticipant); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM communications.channels WHERE id = $1`, channelWithParticipant)
	if !isRestrictViolation(err) {
		t.Fatalf("delete a channel with a participant: err = %v, want a restrict_violation", err)
	}
}

// TestCommunicationsBaseline_MessageDirectionAcceptsDotNetsFullDomain pins
// the CHECK on conversation_messages.direction — new in this port; .NET has
// no CHECK here at all (inventory §10 item 7) — against .NET's own
// Direction domain: {inbound, outbound, internal_note}, all three accepted,
// anything else rejected.
//
// Task 7 fix round 2 correction (design doc §1.1's correction): an earlier
// version of this test (then named
// TestCommunicationsBaseline_MessageDirectionRejectsInbound) asserted the
// opposite of its last assertion — that "inbound" must be *rejected*,
// because this port's own writers (the composer's reply and note paths)
// never produce it. That reasoning was wrong: narrowing the CHECK to this
// port's current writers, rather than matching .NET's declared domain, made
// an inbound row impossible to insert even from a test fixture, which in
// turn made the real recipient-resolution query
// (GetLatestInboundParticipantAddress) permanently unexercisable on its
// success branch — no fixture could ever drive it there, so a test seam
// added to compensate only relocated the blindness. This port still never
// writes "inbound" in production (design doc §1.1: the inbound worker was
// the only writer and it is out of scope, so recipients_missing still fires
// for every conversation this port's own API can create) — but a fixture
// now can, which is exactly what conversations_reply_test.go's own fixtures
// rely on.
func TestCommunicationsBaseline_MessageDirectionAcceptsDotNetsFullDomain(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	channelID := insertTestChannel(t, ctx, pool)
	conversationID := insertTestConversation(t, ctx, pool, channelID)

	for _, direction := range []string{"outbound", "internal_note", "inbound"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO communications.conversation_messages (id, conversation_id, direction, occurred_at, created_at)
			VALUES ($1, $2, $3, now(), now())`, uuid.New(), conversationID, direction); err != nil {
			t.Errorf("insert a %q message: %v, want no error", direction, err)
		}
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO communications.conversation_messages (id, conversation_id, direction, occurred_at, created_at)
		VALUES ($1, $2, 'bogus', now(), now())`, uuid.New(), conversationID)
	if !isCheckViolation(err) {
		t.Fatalf("insert a bogus-direction message: err = %v, want a check_violation", err)
	}
}

// TestCommunicationsBaseline_DDLDefaultsMatchDotNetInitialisers pins spec
// D4 (and D2's attachment_uploads.scan_status ruling): every column whose
// only .NET default was a C# property initialiser with no DDL counterpart
// gets a real DDL default here, so a Go insert that omits the column gets
// the value every real write path produces instead of a NOT NULL violation
// (inventory §10 item 10, §19.1 item 4).
func TestCommunicationsBaseline_DDLDefaultsMatchDotNetInitialisers(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	channelID := insertTestChannel(t, ctx, pool)

	var conversationStatus string
	if err := pool.QueryRow(ctx, `
		INSERT INTO communications.conversations (id, channel_id, last_activity_at, created_at)
		VALUES ($1, $2, now(), now()) RETURNING status`, uuid.New(), channelID).Scan(&conversationStatus); err != nil {
		t.Fatalf("insert conversation without status: %v", err)
	}
	if conversationStatus != "open" {
		t.Errorf("conversations.status default = %q, want %q", conversationStatus, "open")
	}

	conversationID := insertTestConversation(t, ctx, pool, channelID)
	messageID := insertTestConversationMessage(t, ctx, pool, conversationID)

	var deliveryStatus string
	if err := pool.QueryRow(ctx, `
		INSERT INTO communications.message_deliveries (id, message_id, recipient_address, recipient_type, attempts, created_at)
		VALUES ($1, $2, 'customer@example.com', 'to', 0, now()) RETURNING status`, uuid.New(), messageID).Scan(&deliveryStatus); err != nil {
		t.Fatalf("insert message delivery without status: %v", err)
	}
	if deliveryStatus != "queued" {
		t.Errorf("message_deliveries.status default = %q, want %q", deliveryStatus, "queued")
	}

	var outboxStatus string
	if err := pool.QueryRow(ctx, `
		INSERT INTO communications.outbox_jobs (id, message_id, attempts, next_attempt_at, created_at)
		VALUES ($1, $2, 0, now(), now()) RETURNING status`, uuid.New(), messageID).Scan(&outboxStatus); err != nil {
		t.Fatalf("insert outbox job without status: %v", err)
	}
	if outboxStatus != "pending" {
		t.Errorf("outbox_jobs.status default = %q, want %q", outboxStatus, "pending")
	}

	var cleanupStatus string
	if err := pool.QueryRow(ctx, `
		INSERT INTO communications.attachment_cleanup_records (id, storage_key, attempts, next_attempt_at, created_at)
		VALUES ($1, 'attachments/x', 0, now(), now()) RETURNING status`, uuid.New()).Scan(&cleanupStatus); err != nil {
		t.Fatalf("insert attachment cleanup record without status: %v", err)
	}
	if cleanupStatus != "pending" {
		t.Errorf("attachment_cleanup_records.status default = %q, want %q", cleanupStatus, "pending")
	}

	participantID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.participants (id, channel_id, address, created_at)
		VALUES ($1, $2, 'someone@example.com', now())`, participantID, channelID); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	var role string
	if err := pool.QueryRow(ctx, `
		INSERT INTO communications.conversation_participants (conversation_id, participant_id)
		VALUES ($1, $2) RETURNING role`, conversationID, participantID).Scan(&role); err != nil {
		t.Fatalf("insert conversation participant without role: %v", err)
	}
	if role != "participant" {
		t.Errorf("conversation_participants.role default = %q, want %q", role, "participant")
	}

	var uploadScanStatus string
	if err := pool.QueryRow(ctx, `
		INSERT INTO communications.attachment_uploads (id, conversation_id, uploaded_by_user_id, file_name, content_type, size_bytes, content_hash, storage_key, is_inline, idempotency_key, expires_at, created_at)
		VALUES ($1, $2, $3, 'invoice.pdf', 'application/pdf', 1024, 'deadbeef', 'attachments/y', false, 'idem-key-1', now() + interval '24 hours', now())
		RETURNING scan_status`, uuid.New(), conversationID, uuid.New()).Scan(&uploadScanStatus); err != nil {
		t.Fatalf("insert attachment upload without scan_status: %v", err)
	}
	if uploadScanStatus != "clean" {
		t.Errorf("attachment_uploads.scan_status default = %q, want %q (D2: nothing to scan post-port, so uploads are born ready)", uploadScanStatus, "clean")
	}

	var attachmentScanStatus string
	if err := pool.QueryRow(ctx, `
		INSERT INTO communications.message_attachments (id, message_id, file_name, content_type, size_bytes, content_hash, storage_key, is_inline, created_at)
		VALUES ($1, $2, 'invoice.pdf', 'application/pdf', 1024, 'deadbeef', 'attachments/z', false, now())
		RETURNING scan_status`, uuid.New(), messageID).Scan(&attachmentScanStatus); err != nil {
		t.Fatalf("insert message attachment without scan_status: %v", err)
	}
	if attachmentScanStatus != "clean" {
		t.Errorf("message_attachments.scan_status default = %q, want %q (kept consistent with attachment_uploads: the composer's reply promotion is the only surviving writer, and it always writes \"clean\")", attachmentScanStatus, "clean")
	}
}

// isCheckViolation reports whether err is Postgres SQL state 23514
// (check_violation), what a CHECK constraint raises.
func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23514"
}

// isExclusionViolation reports whether err is Postgres SQL state 23P01
// (exclusion_violation), what a GiST EXCLUDE USING constraint raises.
func isExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23P01"
}

// isRestrictViolation reports whether err is Postgres SQL state 23001
// (restrict_violation), what a literal `ON DELETE RESTRICT` FK raises —
// distinct from 23503 (foreign_key_violation), which is what the FK default
// (NO ACTION) raises instead.
func isRestrictViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23001"
}

// insertTestProduct inserts one tax category, one product referencing it, and
// returns the product's id. It exists only to satisfy products' NOT NULL/
// Restrict tax_category_id FK for tests that need a real product row.
func insertTestProduct(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int32 {
	t.Helper()
	var taxCategoryID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.tax_categories (name, kind, rate, created_at, updated_at) VALUES ('Standard', 'Standard', 0.25, now(), now()) RETURNING id`).Scan(&taxCategoryID); err != nil {
		t.Fatalf("insert tax category: %v", err)
	}
	var productID int32
	if err := pool.QueryRow(ctx, `INSERT INTO products.products (name, type, status, tax_category_id, created_at, updated_at) VALUES ('Test product', 'Goods', 'Draft', $1, now(), now()) RETURNING id`, taxCategoryID).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	return productID
}

// TestTimeBaseline_AppliesAndIsIdempotent proves 00010_time_baseline.sql
// applies, rolls back and re-applies cleanly — its schema is named after a
// SQL keyword, so the unquoted `time.` qualifier is itself under test here —
// and that a person's rate card is unique per (user, valid_from).
func TestTimeBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 10) // 00010_time_baseline.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'time' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	gotTables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect tables: %v", err)
	}
	if want := []string{"entries", "person_rates", "settings", "week_submissions"}; !equalStrings(gotTables, want) {
		t.Errorf("tables = %v, want %v", gotTables, want)
	}

	if cols := indexColumns(t, ctx, pool, "time", "ux_person_rates_user_id_valid_from"); !equalStrings(cols, []string{"user_id", "valid_from"}) {
		t.Errorf("ux_person_rates_user_id_valid_from columns = %v, want [user_id valid_from]", cols)
	}
	if cols := primaryKeyColumns(t, ctx, pool, "time", "week_submissions"); !equalStrings(cols, []string{"user_id", "week_start"}) {
		t.Errorf("week_submissions primary key columns = %v, want [user_id week_start]", cols)
	}
}

// TestProjectsMilestones_AppliesAndIsIdempotent proves
// 00011_projects_milestones.sql applies, rolls back and re-applies cleanly:
// projects.billing_milestones exists with its two indexes (design §3.2's
// manual order and the partial one over the two open statuses), and
// projects.billing_lines gained its own budget columns (§3.1).
func TestProjectsMilestones_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 11) // 00011_projects_milestones.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var hasTable bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'projects' AND table_name = 'billing_milestones')`).Scan(&hasTable); err != nil {
		t.Fatalf("check billing_milestones exists: %v", err)
	}
	if !hasTable {
		t.Fatal("projects.billing_milestones does not exist")
	}

	if cols := indexColumns(t, ctx, pool, "projects", "ix_billing_milestones_project_id_position"); !equalStrings(cols, []string{"project_id", "position"}) {
		t.Errorf("ix_billing_milestones_project_id_position columns = %v, want [project_id position]", cols)
	}
	if cols := indexColumns(t, ctx, pool, "projects", "ix_billing_milestones_project_id_planned_date_open"); !equalStrings(cols, []string{"project_id", "planned_date"}) {
		t.Errorf("ix_billing_milestones_project_id_planned_date_open columns = %v, want [project_id planned_date]", cols)
	}
	var predicate string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_expr(i.indpred, i.indrelid)
		FROM pg_index i
		JOIN pg_class ic ON ic.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = ic.relnamespace
		WHERE n.nspname = 'projects' AND ic.relname = 'ix_billing_milestones_project_id_planned_date_open'`).Scan(&predicate); err != nil {
		t.Fatalf("query partial index predicate: %v", err)
	}
	if want := "((status)::text = ANY ((ARRAY['planned'::character varying, 'ready'::character varying])::text[]))"; predicate != want {
		t.Errorf("partial index predicate = %q, want %q", predicate, want)
	}

	for _, col := range []string{"budget_hours", "budget_amount"} {
		var hasColumn bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'projects' AND table_name = 'billing_lines' AND column_name = $1)`, col).Scan(&hasColumn); err != nil {
			t.Fatalf("check billing_lines.%s exists: %v", col, err)
		}
		if !hasColumn {
			t.Errorf("projects.billing_lines has no %s column", col)
		}
	}

	// A flat amount remembers the currency it was entered in (design §3.2): a
	// cancelled milestone is exempt from the project's currency guard and can
	// outlive a currency change, so the column is what keeps its number
	// meaning what it meant. char(3), like the project's own currency.
	var dataType, maxLength string
	if err := pool.QueryRow(ctx, `
		SELECT data_type, coalesce(character_maximum_length::text, '')
		FROM information_schema.columns
		WHERE table_schema = 'projects' AND table_name = 'billing_milestones'
		  AND column_name = 'amount_currency'`).Scan(&dataType, &maxLength); err != nil {
		t.Fatalf("check billing_milestones.amount_currency: %v", err)
	}
	if dataType != "character" || maxLength != "3" {
		t.Errorf("amount_currency is %s(%s), want character(3)", dataType, maxLength)
	}
}

// TestExpensesBaseline_AppliesAndIsIdempotent proves
// 00012_expenses_baseline.sql applies, rolls back and re-applies cleanly, with
// the five tables of design §3.1–3.5, the two unique indexes the module's
// refusals key on, and the seeds an installation starts with: the eight
// categories in order, the two state mileage rates, and the single settings
// row. Re-applying after a rollback re-seeds, which is what makes the down
// migration safe to use on a live database.
func TestExpensesBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 12) // 00012_expenses_baseline.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'expenses' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	gotTables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect tables: %v", err)
	}
	// claims is 00013's, and applyUpDownUp ends with every migration applied,
	// so it stands here beside the five this one creates.
	if want := []string{"attachments", "categories", "claims", "entries", "rates", "settings"}; !equalStrings(gotTables, want) {
		t.Errorf("tables = %v, want %v", gotTables, want)
	}

	if cols := indexColumns(t, ctx, pool, "expenses", "ux_rates_kind_valid_from"); !equalStrings(cols, []string{"kind", "valid_from"}) {
		t.Errorf("ux_rates_kind_valid_from columns = %v, want [kind valid_from]", cols)
	}

	// The two partial indexes the tracks after approval read through. Their
	// predicates are pinned as well as their columns: an index whose WHERE no
	// longer matches the queries' own is an index Postgres silently stops
	// using, and the first symptom is a sequential scan on every dashboard
	// paint. The payroll one is the only index that serves
	// StatsReimbursementsWaiting, which has no user predicate at all.
	for _, want := range []struct {
		name      string
		columns   []string
		predicate string
	}{
		{
			"ix_entries_reimbursement_waiting", []string{"user_id", "entry_date"},
			"WHERE (((status)::text = 'approved'::text) AND (reimbursed_at IS NULL))",
		},
		{
			"ix_entries_to_invoice", []string{"project_id", "entry_date"},
			"WHERE (((status)::text = 'approved'::text) AND billable AND (invoiced_at IS NULL))",
		},
	} {
		if cols := indexColumns(t, ctx, pool, "expenses", want.name); !equalStrings(cols, want.columns) {
			t.Errorf("%s columns = %v, want %v", want.name, cols, want.columns)
		}
		var def string
		if err := pool.QueryRow(ctx, `
			SELECT pg_get_indexdef(i.indexrelid)
			FROM pg_index i
			JOIN pg_class ic ON ic.oid = i.indexrelid
			JOIN pg_namespace n ON n.oid = ic.relnamespace
			WHERE n.nspname = 'expenses' AND ic.relname = $1`, want.name).Scan(&def); err != nil {
			t.Fatalf("query %s: %v", want.name, err)
		}
		if !strings.HasSuffix(def, want.predicate) {
			t.Errorf("%s = %q, want it to end in %q", want.name, def, want.predicate)
		}
	}

	// The category name is unique on lower(name), so the index is over an
	// expression rather than a column and reads back as one unnamed member.
	var nameIndex string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_indexdef(i.indexrelid)
		FROM pg_index i
		JOIN pg_class ic ON ic.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = ic.relnamespace
		WHERE n.nspname = 'expenses' AND ic.relname = 'ux_categories_name_lower'`).Scan(&nameIndex); err != nil {
		t.Fatalf("query ux_categories_name_lower: %v", err)
	}
	if !strings.Contains(nameIndex, "UNIQUE") || !strings.Contains(nameIndex, "lower((name)::text)") {
		t.Errorf("ux_categories_name_lower = %q, want a unique index over lower(name)", nameIndex)
	}

	seededRows, err := pool.Query(ctx, `SELECT name FROM expenses.categories ORDER BY position`)
	if err != nil {
		t.Fatalf("query categories: %v", err)
	}
	gotCategories, err := pgx.CollectRows(seededRows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect categories: %v", err)
	}
	wantCategories := []string{"Materials", "Subcontractor", "Equipment hire", "Travel", "Accommodation", "Meals", "Phone and internet", "Other"}
	if !equalStrings(gotCategories, wantCategories) {
		t.Errorf("seeded categories = %v, want %v", gotCategories, wantCategories)
	}

	var rateCount, settingsCount int
	// This migration's own seeds are the two mileage rates; 00013 adds the per
	// diem ones, which the test of that migration pins.
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM expenses.rates
		WHERE source = 'State rate' AND kind IN ('mileage', 'mileage_passenger')`).Scan(&rateCount); err != nil {
		t.Fatalf("count seeded rates: %v", err)
	}
	if rateCount != 2 {
		t.Errorf("seeded rates = %d, want the two state mileage rates", rateCount)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM expenses.settings`).Scan(&settingsCount); err != nil {
		t.Fatalf("count settings: %v", err)
	}
	if settingsCount != 1 {
		t.Errorf("settings rows = %d, want the single row", settingsCount)
	}
	// And "one row" is the table's own rule: a second row is refused by the
	// database, not merely never written by the module.
	if _, err := pool.Exec(ctx, `
		INSERT INTO expenses.settings (id, default_currency, default_markup_percent, updated_at)
		VALUES (2, 'NOK', 0, now())`); !isUniqueViolation(err) {
		t.Errorf("a second settings row: %v, want a unique violation from ux_settings_single_row", err)
	}
}

// TestExpensesClaims_AppliesAndIsIdempotent proves 00013_expenses_claims.sql
// applies, rolls back and re-applies cleanly, with the travel claim table of
// design §3.6, the four indexes it reads through, the cascade that takes a
// claim's lines with it, and the six per diem rates the state agreement sets.
func TestExpensesClaims_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, 13) // 00013_expenses_claims.sql

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'expenses' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	gotTables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect tables: %v", err)
	}
	if want := []string{"attachments", "categories", "claims", "entries", "rates", "settings"}; !equalStrings(gotTables, want) {
		t.Errorf("tables = %v, want %v", gotTables, want)
	}

	// The claim indexes, predicates included, mirroring 00012's for the
	// entries: a claim is a unit of approval and of payroll exactly as a
	// standalone line is, and the queries that page each of those tracks read
	// through these.
	for _, want := range []struct {
		name      string
		columns   []string
		predicate string
	}{
		{"ix_claims_user_id_departure_at", []string{"user_id", "departure_at"}, ""},
		{"ix_claims_project_id_departure_at", []string{"project_id", "departure_at"}, ""},
		{"ix_claims_submitted", []string{"status"}, "WHERE ((status)::text = 'submitted'::text)"},
		{
			"ix_claims_reimbursement_waiting", []string{"user_id", "departure_at"},
			"WHERE (((status)::text = 'approved'::text) AND (reimbursed_at IS NULL))",
		},
	} {
		if cols := indexColumns(t, ctx, pool, "expenses", want.name); !equalStrings(cols, want.columns) {
			t.Errorf("%s columns = %v, want %v", want.name, cols, want.columns)
		}
		var def string
		if err := pool.QueryRow(ctx, `
			SELECT pg_get_indexdef(i.indexrelid)
			FROM pg_index i
			JOIN pg_class ic ON ic.oid = i.indexrelid
			JOIN pg_namespace n ON n.oid = ic.relnamespace
			WHERE n.nspname = 'expenses' AND ic.relname = $1`, want.name).Scan(&def); err != nil {
			t.Fatalf("query %s: %v", want.name, err)
		}
		if want.predicate != "" && !strings.HasSuffix(def, want.predicate) {
			t.Errorf("%s = %q, want it to end in %q", want.name, def, want.predicate)
		}
		if want.predicate == "" && strings.Contains(def, " WHERE ") {
			t.Errorf("%s = %q, want no predicate", want.name, def)
		}
	}

	// A claim's lines are the claim's: deleting it takes them, and their
	// receipts follow through the cascade 00012 already gave attachments.
	var deleteRule string
	if err := pool.QueryRow(ctx, `
		SELECT confdeltype FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = 'expenses' AND t.relname = 'entries' AND c.contype = 'f'
		  AND c.conname = 'fk_entries_claim_id'`).Scan(&deleteRule); err != nil {
		t.Fatalf("query the claim foreign key: %v", err)
	}
	if deleteRule != "c" {
		t.Errorf("entries.claim_id delete rule = %q, want %q (ON DELETE CASCADE)", deleteRule, "c")
	}

	// The seeds of design §3.4 (delivery B), verified against the state's
	// agreement. per_diem_overnight_other is deliberately not among them.
	seeded, err := pool.Query(ctx, `
		SELECT kind, value::text, coalesce(currency, ''), valid_from::text
		FROM expenses.rates WHERE source = 'State rate' AND kind <> 'mileage' AND kind <> 'mileage_passenger'
		ORDER BY kind`)
	if err != nil {
		t.Fatalf("query the per diem seeds: %v", err)
	}
	type seededRate struct {
		Kind      string
		Value     string
		Currency  string
		ValidFrom string
	}
	got, err := pgx.CollectRows(seeded, pgx.RowToStructByPos[seededRate])
	if err != nil {
		t.Fatalf("collect the per diem seeds: %v", err)
	}
	want := []seededRate{
		{"meal_breakfast_percent", "20.00", "", "2026-01-01"},
		{"meal_dinner_percent", "50.00", "", "2026-01-01"},
		{"meal_lunch_percent", "30.00", "", "2026-01-01"},
		{"per_diem_6_12", "397.00", "NOK", "2026-01-01"},
		{"per_diem_over_12", "736.00", "NOK", "2026-01-01"},
		{"per_diem_overnight_hotel", "1012.00", "NOK", "2026-01-01"},
	}
	if len(got) != len(want) {
		t.Fatalf("seeded per diem rates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("seeded rate %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestExpensesClaims_TheDownMigrationKeepsACompanysOwnRates pins what the down
// migration's seed identity actually means. It deletes the rows it wrote by
// (kind, valid_from, source), so:
//
//   - a rate the company entered on a day of its own survives, whatever kind it
//     is — that is the case the identity exists for;
//   - a shipped row the company **edited in place but left labelled "State
//     rate"** goes with the rest, because ux_rates_kind_valid_from admits only
//     one row per kind and day and nothing else distinguishes it. Relabelling it
//     as their own is what spares it, which is the same signal
//     POST /rates/reset reads.
//
// The second half is a documented consequence rather than a wish, so it is
// pinned here: a later change to either the delete or the label would fail with
// the reason written down beside it.
func TestExpensesClaims_TheDownMigrationKeepsACompanysOwnRates(t *testing.T) {
	url := testdb.URL(t)
	ctx := context.Background()

	migrateTo(t, url, 13)
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// A company's own rate, on a day the product does not ship, and a shipped
	// row they have overwritten without relabelling.
	if _, err := pool.Exec(ctx, `
		INSERT INTO expenses.rates (kind, valid_from, value, currency, source, created_at, updated_at)
		VALUES ('per_diem_6_12', DATE '2026-02-01', 410.00, 'NOK', 'Vår egen sats', now(), now())`); err != nil {
		t.Fatalf("insert the company's own rate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE expenses.rates SET value = 500.00
		WHERE kind = 'per_diem_6_12' AND valid_from = DATE '2026-01-01'`); err != nil {
		t.Fatalf("edit the shipped rate: %v", err)
	}

	migrateTo(t, url, 12)

	var own string
	if err := pool.QueryRow(ctx, `
		SELECT value::text FROM expenses.rates
		WHERE kind = 'per_diem_6_12' AND valid_from = DATE '2026-02-01'`).Scan(&own); err != nil {
		t.Fatalf("the company's own rate did not survive the rollback: %v", err)
	}
	if own != "410.00" {
		t.Errorf("the company's own rate = %s, want 410.00 untouched", own)
	}
	var shipped int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM expenses.rates
		WHERE kind = 'per_diem_6_12' AND valid_from = DATE '2026-01-01'`).Scan(&shipped); err != nil {
		t.Fatalf("count the shipped row: %v", err)
	}
	if shipped != 0 {
		t.Errorf("the edited shipped row is still there, want the rollback to take it with the rest of its seeds")
	}
}

// migrateTo moves the database to exactly version, up or down.
func migrateTo(t *testing.T, databaseURL string, version int64) {
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
	if _, err := provider.UpTo(ctx, version); err != nil {
		t.Fatalf("up to version %d: %v", version, err)
	}
	if _, err := provider.DownTo(ctx, version); err != nil {
		t.Fatalf("down to version %d: %v", version, err)
	}
}

// expensesColumns is design §3.1, §3.2 and §3.6 written out: the entries,
// attachments and claims columns with their types and nullability. No later
// task of a delivery changes a migration already shipped, so this is where the
// shape the module builds on is pinned: a widened column or a dropped one
// fails here rather than in whichever query first misses it.
var expensesColumns = map[string][]expensesColumn{
	"entries": {
		{"id", "bigint", "NO"},
		{"user_id", "uuid", "NO"},
		{"created_by_user_id", "uuid", "NO"},
		{"claim_id", "bigint", "YES"},
		{"kind", "character varying", "NO"},
		{"entry_date", "date", "NO"},
		{"description", "character varying", "NO"},
		{"category_id", "integer", "YES"},
		{"supplier", "character varying", "YES"},
		{"paid_by", "character varying", "YES"},
		{"currency", "character", "NO"},
		{"gross_amount", "numeric", "NO"},
		{"vat_amount", "numeric", "YES"},
		{"distance_km", "numeric", "YES"},
		{"from_place", "character varying", "YES"},
		{"to_place", "character varying", "YES"},
		{"passengers", "smallint", "NO"},
		{"rate", "numeric", "YES"},
		{"passenger_rate", "numeric", "YES"},
		{"rate_overridden_by_user_id", "uuid", "YES"},
		{"rate_table_value", "numeric", "YES"},
		{"passenger_rate_table_value", "numeric", "YES"},
		{"project_id", "integer", "YES"},
		{"billing_line_id", "integer", "YES"},
		{"billable", "boolean", "NO"},
		{"markup_percent", "numeric", "YES"},
		{"bill_rate_per_km", "numeric", "YES"},
		{"bill_amount", "numeric", "YES"},
		{"status", "character varying", "NO"},
		{"submitted_at", "timestamp with time zone", "YES"},
		{"decided_at", "timestamp with time zone", "YES"},
		{"decided_by_user_id", "uuid", "YES"},
		{"rejection_reason", "character varying", "YES"},
		{"reimbursed_at", "timestamp with time zone", "YES"},
		{"reimbursed_by_user_id", "uuid", "YES"},
		{"reimbursement_reference", "character varying", "YES"},
		{"reimbursement_date", "date", "YES"},
		{"invoiced_at", "timestamp with time zone", "YES"},
		{"invoiced_by_user_id", "uuid", "YES"},
		{"invoice_reference", "character varying", "YES"},
		{"revision", "integer", "NO"},
		{"created_at", "timestamp with time zone", "NO"},
		{"updated_at", "timestamp with time zone", "NO"},
		// The per diem columns 00013 adds, at the end of the table because
		// that is where ALTER TABLE ... ADD COLUMN puts them. The three
		// percentages are the ones the line was priced with, frozen beside the
		// day rate they were taken off.
		{"per_diem_type", "character varying", "YES"},
		{"breakfast_covered", "boolean", "NO"},
		{"lunch_covered", "boolean", "NO"},
		{"dinner_covered", "boolean", "NO"},
		{"meal_breakfast_percent", "numeric", "YES"},
		{"meal_lunch_percent", "numeric", "YES"},
		{"meal_dinner_percent", "numeric", "YES"},
	},
	"claims": {
		{"id", "bigint", "NO"},
		{"user_id", "uuid", "NO"},
		{"created_by_user_id", "uuid", "NO"},
		{"purpose", "character varying", "NO"},
		{"destination", "character varying", "YES"},
		{"abroad", "boolean", "NO"},
		{"abroad_day_rate", "numeric", "YES"},
		{"abroad_currency", "character", "YES"},
		{"departure_at", "timestamp with time zone", "NO"},
		{"return_at", "timestamp with time zone", "NO"},
		{"project_id", "integer", "YES"},
		{"status", "character varying", "NO"},
		{"submitted_at", "timestamp with time zone", "YES"},
		{"decided_at", "timestamp with time zone", "YES"},
		{"decided_by_user_id", "uuid", "YES"},
		{"rejection_reason", "character varying", "YES"},
		{"reimbursed_at", "timestamp with time zone", "YES"},
		{"reimbursed_by_user_id", "uuid", "YES"},
		{"reimbursement_reference", "character varying", "YES"},
		{"reimbursement_date", "date", "YES"},
		{"revision", "integer", "NO"},
		{"created_at", "timestamp with time zone", "NO"},
		{"updated_at", "timestamp with time zone", "NO"},
	},
	"attachments": {
		{"id", "bigint", "NO"},
		{"entry_id", "bigint", "NO"},
		{"object_key", "text", "NO"},
		{"file_name", "character varying", "NO"},
		{"content_type", "character varying", "NO"},
		{"size_bytes", "bigint", "NO"},
		{"uploaded_by_user_id", "uuid", "NO"},
		{"created_at", "timestamp with time zone", "NO"},
	},
}

// expensesColumn is one row of information_schema.columns, in the order the
// query selects it.
type expensesColumn struct {
	Name     string
	DataType string
	Nullable string
}

// The columns of expenses.entries and expenses.attachments are exactly design
// §3.1 and §3.2, in order. The module's own suite would catch a column its
// queries name; this catches the ones no query names yet — the reimbursement,
// invoicing, rate-override and travel-claim columns that later deliveries
// depend on and that no later migration may add.
func TestExpensesBaseline_PinsTheEntryAndAttachmentColumns(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	for table, want := range expensesColumns {
		rows, err := pool.Query(ctx, `
			SELECT column_name, data_type, is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'expenses' AND table_name = $1
			ORDER BY ordinal_position`, table)
		if err != nil {
			t.Fatalf("query %s columns: %v", table, err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[expensesColumn])
		if err != nil {
			t.Fatalf("collect %s columns: %v", table, err)
		}
		if len(got) != len(want) {
			t.Errorf("expenses.%s has %d columns, want the %d of the design; got %v", table, len(got), len(want), got)
			continue
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("expenses.%s column %d = %+v, want %+v", table, i, got[i], w)
			}
		}
	}
}

// isUniqueViolation reports whether err is Postgres SQL state 23505
// (unique_violation).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
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

// indexColumnDirections returns a named index's columns' sort direction, in
// index order — true for descending. Read from pg_index's indoption (bit
// 0x1 is INDOPTION_DESC), not from the migration's DDL text, because a
// positional descending-flag array (communications inventory §7, §8 — the
// conversations feed index) can be transcribed with the right column names
// but the wrong flags, which indexColumns alone would not catch. indkey and
// indoption are joined by ordinal position rather than by subscripting
// either directly: both are int2vector, a type whose array lower bound is 0
// rather than Postgres's usual 1, and unnest ... WITH ORDINALITY sidesteps
// that off-by-one entirely instead of relying on it.
func indexColumnDirections(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, indexName string) []bool {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT (opt.direction_bit & 1) <> 0 AS is_desc
		FROM pg_index i
		JOIN pg_class ic ON ic.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = ic.relnamespace
		CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
		CROSS JOIN LATERAL unnest(i.indoption) WITH ORDINALITY AS opt(direction_bit, ord2)
		WHERE n.nspname = $1 AND ic.relname = $2 AND k.ord = opt.ord2
		ORDER BY k.ord`, schema, indexName)
	if err != nil {
		t.Fatalf("query index directions for %s: %v", indexName, err)
	}
	defer rows.Close()

	var desc []bool
	for rows.Next() {
		var isDesc bool
		if err := rows.Scan(&isDesc); err != nil {
			t.Fatalf("scan index direction: %v", err)
		}
		desc = append(desc, isDesc)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return desc
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
