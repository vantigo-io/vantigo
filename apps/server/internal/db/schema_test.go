package db_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// TestProductsBaseline_AppliesAndIsIdempotent proves
// 00004_products_baseline.sql applies, rolls back, and re-applies cleanly,
// and that the tenant drop landed exactly where the inventory says it
// should: sku unique alone, no tenant_id column anywhere in the schema.
func TestProductsBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url)

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
	applyUpDownUp(t, url)

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
