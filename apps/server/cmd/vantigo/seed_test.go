package main

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

func TestEmbeddedDemoAvatars_AllPersonasDecodeAs256x256JPEG(t *testing.T) {
	seen := make(map[string]int, len(demoStaffMembers))
	for i := range demoStaffMembers {
		data, err := demoAvatarBytes(i)
		if err != nil {
			t.Fatalf("demoAvatarBytes(%d) failed: %v", i, err)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("persona %d invalid JPEG: %v", i, err)
		}
		if cfg.Width != 256 || cfg.Height != 256 {
			t.Errorf("persona %d dimensions = %dx%d, want 256x256", i, cfg.Width, cfg.Height)
		}
		if prev, dup := seen[string(data)]; dup {
			t.Errorf("persona %d has identical JPEG bytes as persona %d", i, prev)
		}
		seen[string(data)] = i
	}
}

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
		// The staff, and Kari of the Nordlys demo brand (seedDemoBrands), who has no person in Staff.
		{"identity.users", "SELECT count(*) FROM identity.users WHERE kind = 'person'", demoUserCount + 1},
		// The default brand and the two demo brands, with a rule each; Kari matches both.
		{"identity.brands", "SELECT count(*) FROM identity.brands WHERE state = 'published'", 3},
		{"identity.brand_rules", "SELECT count(*) FROM identity.brand_rules", 2},
		// Jul on the default brand and Pride on Nordlys, both disabled.
		{"identity.brand_campaigns", "SELECT count(*) FROM identity.brand_campaigns WHERE NOT enabled AND yearly", 2},
		{"identity.brand_rules group members", `SELECT count(*) FROM identity.brand_rules r
			JOIN identity.access_group_memberships m ON m.group_id::text = r.target_ref WHERE r.kind = 'access_group'`, 2},
		// The web shop's integration, its Webshop role and one key (API access
		// delivery A); the key itself is never asserted, only printed.
		{"identity.integrations", "SELECT count(*) FROM identity.integrations WHERE name = 'Webshop sync' AND archived_at IS NULL", 1},
		{"identity.users integration", "SELECT count(*) FROM identity.users WHERE kind = 'integration' AND display_name = 'Webshop sync'", 1},
		{"identity.user_roles Webshop", `SELECT count(*) FROM identity.user_roles ur JOIN identity.roles r ON r.id = ur.role_id
			JOIN identity.integrations i ON i.user_id = ur.user_id WHERE r.name = 'Webshop'`, 1},
		{"identity.role_permissions Webshop", `SELECT count(*) FROM identity.role_permissions rp JOIN identity.roles r ON r.id = rp.role_id
			WHERE r.name = 'Webshop' AND rp.permission_key IN ('customers:view', 'customers:create', 'customers:update',
			'products:products-view', 'products:pricing-view', 'orders:access')`, 6},
		{"identity.api_keys", "SELECT count(*) FROM identity.api_keys WHERE permissions IS NULL AND NOT read_only", 1},
		// Its day of request log rows (delivery B), seeded once.
		{"identity.api_requests", "SELECT count(*) FROM identity.api_requests", 210},
		{"identity.profile_avatars", "SELECT count(*) FROM identity.profile_avatars WHERE content_type = 'image/jpeg'", demoUserCount},
		{"staff.people", "SELECT count(*) FROM staff.people", demoUserCount},
		// The companies, and the person the eFaktura demo bills (invoices phase 6 D10).
		{"customers.customers", "SELECT count(*) FROM customers.customers", demoCustomerCount + 1},
		// A default brand on three customers and two groups (brands delivery E1).
		{"customers.customers brand", "SELECT count(*) FROM customers.customers WHERE brand_id IS NOT NULL", 3},
		{"customers.customer_groups brand", "SELECT count(*) FROM customers.customer_groups WHERE brand_id IS NOT NULL", 2},
		{"customers.contacts", "SELECT count(*) FROM customers.contacts", demoCustomerCount * demoContactsPerCustomer},
		{"products.products", "SELECT count(*) FROM products.products", demoProductCount + demoWorkshopProductCount},
		{"projects.projects", "SELECT count(*) FROM projects.projects", demoProjectCount},
		// Those of main and one in EUR (seedInvoicesCurrency).
		{"invoices.invoices", "SELECT count(*) FROM invoices.invoices", 32},
		{"invoices.consumer_lookups found", "SELECT count(*) FROM invoices.consumer_lookups WHERE status = 'found' AND auto_accept", 1},
		{"invoices.avtalegiro_mandates unresolved", "SELECT count(*) FROM invoices.avtalegiro_mandates WHERE customer_id IS NULL", 1},
		{"invoices.avtalegiro_claims accepted", "SELECT count(*) FROM invoices.avtalegiro_claims WHERE status = 'accepted'", 1},
		{"invoices.transmissions eFaktura delivered", "SELECT count(*) FROM invoices.transmissions WHERE channel = 'efaktura' AND status = 'delivered' AND evidence_object_key IS NOT NULL", 1},
		// Send all's finished batch: one sent, one awaiting print, one failed (phase 6 plan E8).
		{"invoices.send_batches finished", "SELECT count(*) FROM invoices.send_batches WHERE finished_at IS NOT NULL", 1},
		{"invoices.send_jobs", "SELECT count(*) FROM invoices.send_jobs WHERE state IN ('sent', 'awaiting_print', 'failed')", 3},
		{"inventory.sites", "SELECT count(*) FROM inventory.sites", 1},
		{"inventory.lots", "SELECT count(*) FROM inventory.lots", 5},
		{"manufacturing.work_centers", "SELECT count(*) FROM manufacturing.work_centers", 4},
		{"manufacturing.boms", "SELECT count(*) FROM manufacturing.boms", 5},
		{"manufacturing.orders", "SELECT count(*) FROM manufacturing.orders", 6},
		{"manufacturing.orders done", "SELECT count(*) FROM manufacturing.orders WHERE status = 'done'", 1},
		{"manufacturing.orders in progress", "SELECT count(*) FROM manufacturing.orders WHERE status = 'in_progress'", 1},
		{"manufacturing.orders draft", "SELECT count(*) FROM manufacturing.orders WHERE status = 'draft'", 1},
		{"manufacturing.orders made to order", "SELECT count(*) FROM manufacturing.orders WHERE origin_kind = 'sales_order' AND status = 'confirmed'", 1},
		{"manufacturing.time_logs open", "SELECT count(*) FROM manufacturing.time_logs WHERE ended_at IS NULL", 1},
		// The tube and the frame, and the screws, the plywood and the paint the
		// purchase proposals' demo places (purchasing delivery C).
		{"manufacturing.suggestions open", "SELECT count(*) FROM manufacturing.suggestions WHERE status = 'open'", 5},
		{"orders.orders", "SELECT count(*) FROM orders.orders", 3},
		{"orders.lines kit children", "SELECT count(*) FROM orders.lines WHERE parent_line_id IS NOT NULL", 3},
		{"identity.sessions", "SELECT count(*) FROM identity.sessions", 0},
	} {
		var got int
		if err := conn.QueryRow(ctx, check.query).Scan(&got); err != nil {
			t.Fatalf("%s query failed: %v", check.name, err)
		}
		if got != check.want {
			t.Errorf("%s count = %d, want %d", check.name, got, check.want)
		}
	}

	assertDemoWorkshopReads(t, ctx, conn, databaseURL)
	assertDemoProposals(t, ctx, databaseURL)

	// Second run must be a no-op and preserve exact counts.
	code2, stdout2, stderr2 := runCapture("seed")
	if code2 != 0 {
		t.Fatalf("second seed failed with exit %d\nstdout: %s\nstderr: %s", code2, stdout2, stderr2)
	}
	if !strings.Contains(stdout2, "development seed already applied; skipping") {
		t.Errorf("expected idempotent skip message in stdout, got: %s", stdout2)
	}
	if !strings.Contains(stdout2, "demo workshop already seeded; skipping") {
		t.Errorf("expected the workshop's skip message in stdout, got: %s", stdout2)
	}
	if !strings.Contains(stdout2, "purchase proposals demo: already seeded; skipping") {
		t.Errorf("expected the proposals' skip message in stdout, got: %s", stdout2)
	}
	// The key is printed by the first run alone, and no second key is made.
	if !strings.Contains(stdout, "vtg_") || strings.Contains(stdout2, "vtg_") {
		t.Error("the integration's key is printed by the first run only")
	}
	var keys int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM identity.api_keys`).Scan(&keys); err != nil || keys != 1 {
		t.Errorf("api keys after a rerun = %d, %v, want 1", keys, err)
	}
}

// assertDemoWorkshopReads reads the demo workshop back through the modules'
// own API — the manufacturing orders and their work orders, the BoMs, the
// shop floor, the schedule, the stock and the sales orders — and checks the
// ledger: every balance is the sum of its movements, and no site has more
// reserved of a variant than it holds.
func assertDemoWorkshopReads(t *testing.T, ctx context.Context, conn *pgx.Conn, databaseURL string) {
	t.Helper()
	cfg, err := config.FromOS()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	deps, access, err := moduleDeps(cfg, pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	handler, _, err := module.ComposeAPI(deps, businessModules(access)...)
	if err != nil {
		t.Fatal(err)
	}
	w := &workshop{ctx: ctx, pool: pool, handler: handler, variants: map[string]int32{}}
	defer w.signOut()
	if err := w.load(); err != nil {
		t.Fatal(err)
	}
	var siteID int64
	if err := conn.QueryRow(ctx, `SELECT id FROM inventory.sites WHERE code = $1`, demoWorkshopSiteCode).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	read := func(who *persona, path string) obj {
		t.Helper()
		var out any
		if err := w.call(who, http.MethodGet, path, nil, &out); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if o, ok := out.(obj); ok {
			return o
		}
		return obj{"items": out}
	}

	read(w.planner, "/api/v1/manufacturing/orders")
	var lateID int64
	if err := conn.QueryRow(ctx, `
		SELECT o.id FROM manufacturing.orders o JOIN products.product_variants v ON v.id = o.variant_id
		WHERE v.sku = 'VRK-PLATE-160' AND o.status = 'confirmed'
	`).Scan(&lateID); err != nil {
		t.Fatal(err)
	}
	for _, id := range queryIDs(t, conn, ctx, `SELECT id FROM manufacturing.orders ORDER BY id`) {
		o := read(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d", id))
		if str(o, "status") == "" {
			t.Errorf("manufacturing order %d answered no status: %v", id, o)
		}
		read(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d/work-orders", id))
		read(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d/events", id))
		if id == lateID && o["late"] != true {
			t.Errorf("the top order %s is not late: %v", str(o, "number"), o["late"])
		}
	}
	for _, id := range queryIDs(t, conn, ctx, `SELECT id FROM manufacturing.boms ORDER BY id`) {
		read(w.planner, fmt.Sprintf("/api/v1/manufacturing/boms/%d", id))
	}
	read(w.planner, "/api/v1/manufacturing/boms")
	for _, id := range queryIDs(t, conn, ctx, `SELECT id FROM manufacturing.work_centers ORDER BY id`) {
		read(w.operator, fmt.Sprintf("/api/v1/manufacturing/work-centers/%d/queue", id))
	}
	mine := read(w.operator, "/api/v1/manufacturing/shop-floor/mine")
	if mine["openWorkOrder"] == nil {
		t.Errorf("the operator has no work order running on the shop floor: %v", mine)
	}
	read(w.planner, fmt.Sprintf("/api/v1/manufacturing/schedule?siteId=%d", siteID))
	read(w.planner, fmt.Sprintf("/api/v1/manufacturing/replenishment/suggestions?siteId=%d", siteID))
	read(w.owner, fmt.Sprintf("/api/v1/inventory/sites/%d", siteID))
	for _, id := range queryIDs(t, conn, ctx, `SELECT id FROM inventory.locations ORDER BY id`) {
		read(w.owner, fmt.Sprintf("/api/v1/inventory/locations/%d/contents", id))
	}
	read(w.owner, "/api/v1/inventory/lots")
	read(w.owner, "/api/v1/inventory/low-stock")
	for _, id := range w.variants {
		read(w.owner, fmt.Sprintf("/api/v1/inventory/variants/%d/stock", id))
	}
	for _, id := range queryIDs(t, conn, ctx, `SELECT id FROM orders.orders ORDER BY id`) {
		read(w.owner, fmt.Sprintf("/api/v1/orders/orders/%d", id))
	}

	// The ledger: each balance is its movements' sum, and no balance lacks
	// its movements.
	var mismatches int
	if err := conn.QueryRow(ctx, `
		WITH moved AS (
			SELECT variant_id, location_id, lot_id, sum(q) AS q FROM (
				SELECT variant_id, to_location_id AS location_id, lot_id, quantity AS q FROM inventory.movement_lines WHERE to_location_id IS NOT NULL
				UNION ALL
				SELECT variant_id, from_location_id, lot_id, -quantity FROM inventory.movement_lines WHERE from_location_id IS NOT NULL
			) l GROUP BY 1, 2, 3
		)
		SELECT count(*) FROM moved m
		FULL JOIN inventory.balances b
			ON b.variant_id = m.variant_id AND b.location_id = m.location_id AND b.lot_id IS NOT DISTINCT FROM m.lot_id
		WHERE COALESCE(m.q, 0) <> COALESCE(b.on_hand, 0)
	`).Scan(&mismatches); err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 {
		t.Errorf("%d balances differ from the sum of their movements", mismatches)
	}
	var overReserved int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT r.variant_id, r.site_id, sum(r.quantity) AS reserved FROM inventory.reservations r GROUP BY 1, 2
		) r
		WHERE r.reserved > COALESCE((
			SELECT sum(b.on_hand) FROM inventory.balances b JOIN inventory.locations l ON l.id = b.location_id
			WHERE b.variant_id = r.variant_id AND l.site_id = r.site_id), 0)
	`).Scan(&overReserved); err != nil {
		t.Fatal(err)
	}
	if overReserved != 0 {
		t.Errorf("%d variants are reserved beyond what their site holds", overReserved)
	}
}

// queryIDs is the query's single bigint column.
func queryIDs(t *testing.T, conn *pgx.Conn, ctx context.Context, query string) []int64 {
	t.Helper()
	rows, err := conn.Query(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatal(err)
	}
	return out
}
