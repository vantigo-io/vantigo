package main

// The demo workshop: a small furniture and metal workshop's goods, stock,
// bills of materials, manufacturing orders and sales orders, so Inventory,
// Orders and Manufacturing have something to show in a development stack.
//
// It comes in two parts. The catalog — the workshop's products and variants —
// and the roles' new permissions are written in the seed's own transaction,
// as the rest of the dataset is: seedWorkshopCatalog on a fresh seed,
// backfillDemoWorkshop on an installation seeded before the workshop existed.
// Everything with derived state — movements and balances, lots, reservations,
// MO and sales order numbers, work orders, schedules, costs, kit lines and
// make-to-order — is then made by seedDemoWorkshop through the modules' own
// HTTP API, composed in process and called as the demo personas, so the data
// is the data a person would have made in the interface. It runs once: a
// workshop site already in place skips it.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// demoWorkshopSiteCode is the workshop's site; its existence is what says the
// workshop was seeded.
const demoWorkshopSiteCode = "VERK"

// The personas the workshop is run by (demoStaffMembers): the owner sets up
// the site and sells, the planner runs manufacturing, the operator works on
// the shop floor.
const (
	demoPlannerEmail  = "magnus.bakken@vantigo.local"
	demoOperatorEmail = "kasper.myhre@vantigo.local"
)

// demoWorkshopProduct is one of the workshop's goods: one product with one
// variant.
type demoWorkshopProduct struct {
	key         string
	name        string
	description string
	sku         string
	unit        string
	tracking    string // none, lot or serial
	expiryDays  int
	cost        float64
	price       float64 // NOK excl. VAT; 0 for none
	category    int     // index into demoWorkshopCategories
}

var demoWorkshopCategories = []string{"Verkstedprodukter", "Råvarer & komponenter"}

var demoWorkshopProducts = []demoWorkshopProduct{
	{key: "workbench", name: "Arbeidsbenk Pro 160", sku: "VRK-ARB-160", unit: "pcs", tracking: "serial", cost: 3900, price: 8990,
		description: "Arbeidsbenk 160 × 70 cm med pulverlakkert stålramme og benkeplate i bjørk. Serienummerert."},
	{key: "shelf", name: "Lagerreol Industri 90", sku: "VRK-REOL-90", unit: "pcs", cost: 1800, price: 4490,
		description: "Sveiset lagerreol 90 × 45 × 200 cm med fem hyller. Lages på bestilling."},
	{key: "kit", name: "Arbeidsbenk startsett", sku: "VRK-SETT-START", unit: "pcs", cost: 4760, price: 11990,
		description: "Arbeidsbenk Pro 160 med verktøytavle og benkeskruestikk, levert som sett."},
	{key: "pegboard", name: "Verktøytavle perforert 120", sku: "VRK-TAVLE-120", unit: "pcs", cost: 320, price: 790,
		description: "Perforert verktøytavle i stål, 120 × 60 cm."},
	{key: "vise", name: "Benkeskruestikk 125 mm", sku: "VRK-SKRUESTIKK-125", unit: "pcs", cost: 540, price: 1290,
		description: "Benkeskruestikk med 125 mm kjeftbredde."},
	{key: "frame", name: "Benkeramme stål 160", sku: "VRK-RAMME-160", unit: "pcs", cost: 1400, category: 1,
		description: "Sveiset og lakkert stålramme til Arbeidsbenk Pro 160. Halvfabrikat."},
	{key: "top", name: "Benkeplate bjørk 160", sku: "VRK-PLATE-160", unit: "pcs", cost: 950, category: 1,
		description: "Benkeplate 160 × 70 cm i limt bjørkekryssfiner, oljet. Halvfabrikat."},
	{key: "tube", name: "Stålrør 40×40×2 mm", sku: "VRK-ROR-40", unit: "m", cost: 62, category: 1,
		description: "Kvadratisk stålrør S235, 40 × 40 × 2 mm, kjøpes i 6 m lengder."},
	{key: "screw", name: "Maskinskrue M8×20", sku: "VRK-SKRUE-M8", unit: "pcs", cost: 1.2, category: 1,
		description: "Maskinskrue M8 × 20 mm, elforsinket."},
	{key: "plywood", name: "Bjørkekryssfiner 18 mm", sku: "VRK-FINER-18", unit: "sheet", cost: 640, category: 1,
		description: "Bjørkekryssfiner 18 mm, plate 250 × 125 cm."},
	{key: "paint", name: "Pulverlakk RAL 7016", sku: "VRK-LAKK-7016", unit: "kg", tracking: "lot", expiryDays: 365, cost: 180, category: 1,
		description: "Pulverlakk antrasittgrå RAL 7016. Sporet per parti, med utløpsdato."},
	{key: "offcut", name: "Stålkapp (restmateriale)", sku: "VRK-KAPP", unit: "kg", cost: 4, category: 1,
		description: "Kapp av stålrør fra kapping, selges som skrapjern."},
}

// demoWorkshopProductCount is how many products the workshop adds to the
// catalog.
var demoWorkshopProductCount = len(demoWorkshopProducts)

// workshopRolePrefixes are the permission prefixes the workshop gave the demo
// roles; a backfill grants a role's keys under them.
var workshopRolePrefixes = []string{"manufacturing:", "inventory:", "orders:"}

// seedWorkshopCatalog puts the workshop's goods in the catalog: one product
// with one variant each, a price for those sold. A SKU already in the catalog
// is left as it is.
func seedWorkshopCatalog(ctx context.Context, tx pgx.Tx, now time.Time) error {
	// The standard VAT category and the two categories, made when missing
	// and otherwise left as they are.
	if _, err := tx.Exec(ctx, `
		INSERT INTO products.tax_categories (name, kind, rate, created_at, updated_at)
		VALUES ('Standard MVA 25 %', 'vat', 0.2500, $1, $1)
		ON CONFLICT (name) DO NOTHING
	`, now); err != nil {
		return fmt.Errorf("ensure the VAT category: %w", err)
	}
	var vatID int32
	if err := tx.QueryRow(ctx, `SELECT id FROM products.tax_categories WHERE name = 'Standard MVA 25 %'`).Scan(&vatID); err != nil {
		return fmt.Errorf("find the VAT category: %w", err)
	}
	catIDs := make([]int32, len(demoWorkshopCategories))
	for i, name := range demoWorkshopCategories {
		if _, err := tx.Exec(ctx, `
			INSERT INTO products.product_categories (name) VALUES ($1)
			ON CONFLICT (parent_id, name) DO NOTHING
		`, name); err != nil {
			return fmt.Errorf("ensure category %q: %w", name, err)
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM products.product_categories WHERE parent_id IS NULL AND name = $1`, name).Scan(&catIDs[i]); err != nil {
			return fmt.Errorf("find category %q: %w", name, err)
		}
	}
	validFrom := now.AddDate(-1, 0, 0)
	for _, p := range demoWorkshopProducts {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM products.product_variants WHERE sku = $1)`, p.sku).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		var productID int32
		if err := tx.QueryRow(ctx, `
			INSERT INTO products.products (name, description, type, status, category_id, tax_category_id, created_at, updated_at)
			VALUES ($1, $2, 'Goods', 'Active', $3, $4, $5, $5)
			RETURNING id
		`, p.name, p.description, catIDs[p.category], vatID, now).Scan(&productID); err != nil {
			return fmt.Errorf("insert product %q: %w", p.name, err)
		}
		tracking := p.tracking
		if tracking == "" {
			tracking = "none"
		}
		var expiryDays *int
		if p.expiryDays > 0 {
			expiryDays = &p.expiryDays
		}
		var variantID int32
		if err := tx.QueryRow(ctx, `
			INSERT INTO products.product_variants (product_id, sku, unit, standard_cost, option_values, tracking, expiry_days, created_at, updated_at)
			VALUES ($1, $2, $3, $4, '{}'::jsonb, $5, $6, $7, $7)
			RETURNING id
		`, productID, p.sku, p.unit, p.cost, tracking, expiryDays, now).Scan(&variantID); err != nil {
			return fmt.Errorf("insert variant %s: %w", p.sku, err)
		}
		if p.price > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO products.product_prices (variant_id, currency, amount, valid_from)
				VALUES ($1, 'NOK', $2, $3)
			`, variantID, p.price, validFrom); err != nil {
				return fmt.Errorf("insert the price of %s: %w", p.sku, err)
			}
		}
	}
	return nil
}

// backfillDemoWorkshop brings an installation seeded before the workshop
// existed up to date, inside the seed's transaction: the workshop's two roles,
// the demo roles' Manufacturing, Inventory and Orders keys, the personas'
// extra roles, and the workshop's goods. seedDemoWorkshop does the rest once
// the transaction is committed.
func backfillDemoWorkshop(ctx context.Context, tx pgx.Tx, now time.Time) error {
	for _, r := range demoCustomRoles {
		inserted, err := tx.Exec(ctx, `
			INSERT INTO identity.roles (
				id, name, normalized_name, display_name, description,
				is_system, is_built_in, version, created_at, updated_at
			)
			VALUES ($1, $2, $3, $2, $4, false, false, $5, $6, $6)
			ON CONFLICT DO NOTHING
		`, r.id, r.name, strings.ToUpper(r.name), r.description, uuid.New(), now)
		if err != nil {
			return fmt.Errorf("ensure demo role %q: %w", r.name, err)
		}
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM identity.roles WHERE normalized_name = $1`, strings.ToUpper(r.name)).Scan(&roleID); err != nil {
			return fmt.Errorf("find demo role %q: %w", r.name, err)
		}
		for _, permKey := range r.permissions {
			// A role the backfill just made gets all its keys; an existing
			// one only those of the modules the workshop added.
			if inserted.RowsAffected() == 0 && !slices.ContainsFunc(workshopRolePrefixes, func(p string) bool { return strings.HasPrefix(permKey, p) }) {
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO identity.role_permissions (role_id, permission_key)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, roleID, permKey); err != nil {
				return fmt.Errorf("grant %s to demo role %q: %w", permKey, r.name, err)
			}
		}
	}
	for _, m := range demoStaffMembers {
		for _, name := range m.extraRoles {
			if _, err := tx.Exec(ctx, `
				INSERT INTO identity.user_roles (user_id, role_id)
				SELECT u.id, r.id FROM identity.users u, identity.roles r
				WHERE u.normalized_email = $1 AND r.normalized_name = $2
				ON CONFLICT DO NOTHING
			`, strings.ToUpper(m.email), strings.ToUpper(name)); err != nil {
				return fmt.Errorf("give %s the role %q: %w", m.email, name, err)
			}
		}
	}
	return seedWorkshopCatalog(ctx, tx, now)
}

// seedDemoWorkshop makes the workshop through the modules' API: the site and
// its stock, the work centers and bills of materials, the manufacturing
// orders in their several states, and the sales orders. It does nothing when
// the workshop's site exists, or when MODULES leaves out Inventory, Orders,
// Manufacturing or Invoices (whose VAT codes a sales order is confirmed
// with). Its calls are not one transaction: a failure part-way leaves the
// workshop half made, and the database has to be reset to seed it again.
func seedDemoWorkshop(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) error {
	for _, m := range []string{"inventory", "orders", "manufacturing", "invoices"} {
		if !slices.Contains(cfg.Modules, m) {
			logger.Info("demo workshop not seeded: MODULES leaves out " + m)
			return nil
		}
	}
	var seeded bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM inventory.sites WHERE code = $1)`, demoWorkshopSiteCode).Scan(&seeded); err != nil {
		return fmt.Errorf("check the demo workshop: %w", err)
	}
	if seeded {
		logger.Info("demo workshop already seeded; skipping")
		return nil
	}

	deps, access, err := moduleDeps(cfg, pool, logger)
	if err != nil {
		return err
	}
	mods := businessModules(access)
	handler, _, err := module.ComposeAPI(deps, mods...)
	if err != nil {
		return fmt.Errorf("compose the API: %w", err)
	}

	w := &workshop{ctx: ctx, pool: pool, handler: handler, variants: map[string]int32{}}
	defer w.signOut()
	if err := w.load(); err != nil {
		return err
	}
	steps := []struct {
		name string
		run  func() error
	}{
		{"inventory", w.seedInventory},
		{"work centers", w.seedWorkCenters},
		{"bills of materials", w.seedBoms},
		{"manufacturing orders", w.seedOrders},
		{"sales orders", w.seedSalesOrders},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			return fmt.Errorf("seed the workshop's %s (the workshop is half made; reset the development database to seed it again): %w", s.name, err)
		}
	}
	// The make-to-order flow and the replenishment run as the workers would
	// do them: the outbox delivers the sales order's confirmation to
	// Manufacturing's handler, the make-to-order worker makes and confirms
	// the order, and the replenishment worker runs the run requested above.
	if err := w.runWorkers(deps, mods, logger); err != nil {
		return err
	}
	logger.Info("demo workshop seeded", "site", demoWorkshopSiteCode,
		"manufacturing_orders", strings.Join(w.orderNumbers, ", "), "sales_orders", strings.Join(w.salesOrderNumbers, ", "))
	return nil
}

// workshop is seedDemoWorkshop's state: the composed API, the personas'
// sessions and the ids the steps hand each other.
type workshop struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	handler http.Handler

	owner, planner, operator *persona
	customers                []int32
	today                    time.Time // the business zone's date
	zone                     *time.Location

	variants  map[string]int32 // product key → variant id
	siteID    int64
	locations map[string]int64 // RAA, HALV, FERDIG, MOTTAK
	centers   map[string]int64
	groupID   int64
	boms      map[string]int64

	orderNumbers      []string
	salesOrderNumbers []string
}

// persona is one demo user and the session the seed calls the API with.
type persona struct {
	id        uuid.UUID
	sessionID uuid.UUID
	token     string
}

func (w *workshop) load() error {
	ctx := w.ctx
	for _, p := range demoWorkshopProducts {
		var id int32
		if err := w.pool.QueryRow(ctx, `SELECT id FROM products.product_variants WHERE sku = $1`, p.sku).Scan(&id); err != nil {
			return fmt.Errorf("find variant %s: %w", p.sku, err)
		}
		w.variants[p.key] = id
	}

	rows, err := w.pool.Query(ctx,
		`SELECT id FROM customers.customers WHERE customer_number >= $1 ORDER BY customer_number LIMIT 3`, demoFirstCustomerNumber)
	if err != nil {
		return err
	}
	w.customers, err = pgx.CollectRows(rows, pgx.RowTo[int32])
	if err != nil {
		return err
	}
	if len(w.customers) < 3 {
		return errors.New("the demo workshop needs three demo customers")
	}

	var zoneName string
	if err := w.pool.QueryRow(ctx, `SELECT business_time_zone FROM identity.settings LIMIT 1`).Scan(&zoneName); err != nil {
		zoneName = demoCalendarZone
	}
	if w.zone, err = time.LoadLocation(zoneName); err != nil {
		return err
	}
	local := time.Now().In(w.zone)
	w.today = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)

	var ownerID uuid.UUID
	err = w.pool.QueryRow(ctx, `SELECT id FROM identity.users WHERE normalized_email = $1`, strings.ToUpper(demoOwnerEmail)).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = w.pool.QueryRow(ctx, `
			SELECT u.id FROM identity.users u JOIN identity.user_roles ur ON ur.user_id = u.id
			WHERE ur.role_id = $1 AND NOT u.is_disabled ORDER BY u.created_at LIMIT 1
		`, identity.RoleOwnerID).Scan(&ownerID)
	}
	if err != nil {
		return fmt.Errorf("find the owner: %w", err)
	}
	if w.owner, err = w.signIn(ownerID); err != nil {
		return err
	}
	for _, p := range []struct {
		email string
		into  **persona
	}{{demoPlannerEmail, &w.planner}, {demoOperatorEmail, &w.operator}} {
		var id uuid.UUID
		if err := w.pool.QueryRow(ctx, `SELECT id FROM identity.users WHERE normalized_email = $1`, strings.ToUpper(p.email)).Scan(&id); err != nil {
			return fmt.Errorf("find demo user %s: %w", p.email, err)
		}
		if *p.into, err = w.signIn(id); err != nil {
			return err
		}
	}
	return nil
}

// signIn plants a session for the user, as identity's sign-in would; signOut
// deletes it again, so the seed leaves no session behind.
func (w *workshop) signIn(userID uuid.UUID) (*persona, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	p := &persona{id: userID, sessionID: uuid.New(), token: base64.RawURLEncoding.EncodeToString(raw)}
	now := time.Now()
	if _, err := w.pool.Exec(w.ctx, `
		INSERT INTO identity.sessions (id, user_id, token_hash, persistent, mfa_verified_at, created_at, last_seen_at, user_agent)
		VALUES ($1, $2, $3, false, $4, $4, $4, 'vantigo seed')
	`, p.sessionID, userID, hash[:], now); err != nil {
		return nil, fmt.Errorf("plant a session: %w", err)
	}
	return p, nil
}

func (w *workshop) signOut() {
	for _, p := range []*persona{w.owner, w.planner, w.operator} {
		if p != nil {
			_, _ = w.pool.Exec(context.WithoutCancel(w.ctx), `DELETE FROM identity.sessions WHERE id = $1`, p.sessionID)
		}
	}
}

// obj is a JSON object as the API answers it.
type obj = map[string]any

// call sends one request to the composed API as who, with the headers given
// as name, value pairs, and decodes the answer into out (when not nil).
// Anything but a 2xx is an error naming the answer.
func (w *workshop) call(who *persona, method, path string, body any, out any, headers ...string) error {
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(w.ctx, method, path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	req.RemoteAddr = "127.0.0.1:1"
	req.AddCookie(&http.Cookie{Name: "vantigo.session", Value: who.token})
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, req)
	if rec.Code < 200 || rec.Code > 299 {
		return fmt.Errorf("%s %s answered %d: %s", method, path, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			return fmt.Errorf("%s %s: decode the answer: %w", method, path, err)
		}
	}
	return nil
}

// post and put are call with an object answer.
func (w *workshop) post(who *persona, path string, body any) (obj, error) {
	var out obj
	return out, w.call(who, http.MethodPost, path, body, &out)
}

func (w *workshop) put(who *persona, path string, body any, headers ...string) (obj, error) {
	var out obj
	return out, w.call(who, http.MethodPut, path, body, &out, headers...)
}

func (w *workshop) get(who *persona, path string) (obj, error) {
	var out obj
	return out, w.call(who, http.MethodGet, path, nil, &out)
}

// id reads an integer field of an answer.
func id(o obj, key string) int64 {
	switch v := o[key].(type) {
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

// list reads an array-of-objects field of an answer.
func list(o obj, key string) []obj {
	raw, _ := o[key].([]any)
	out := make([]obj, 0, len(raw))
	for _, x := range raw {
		if m, ok := x.(obj); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(o obj, key string) string {
	s, _ := o[key].(string)
	return s
}

// day is the business date days from today, as the API takes a date.
func (w *workshop) day(days int) string { return w.today.AddDate(0, 0, days).Format(time.DateOnly) }

// workdayBefore is the n-th Monday-to-Friday day before today (n ≥ 1).
func (w *workshop) workdayBefore(n int) time.Time {
	d := w.today
	for n > 0 {
		d = d.AddDate(0, 0, -1)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			n--
		}
	}
	return d
}

// seedInventory makes the workshop's site with its locations, receives the
// opening stock — the paint in three lots, one of them held for inspection —
// and sets the reorder points replenishment suggests from.
func (w *workshop) seedInventory() error {
	site, err := w.post(w.owner, "/api/v1/inventory/sites", obj{
		"code": demoWorkshopSiteCode, "name": "Verkstedet Lillestrøm", "kind": "warehouse", "fulfilsOrders": true,
		"address": obj{"line1": "Industriveien 12", "postalCode": "2007", "city": "Kjeller", "country": "NO"},
	})
	if err != nil {
		return err
	}
	w.siteID = id(site, "id")
	root := id(site, "rootLocationId")
	w.locations = map[string]int64{}
	for i, l := range []struct{ code, kind, name string }{
		{"MOTTAK", "dock", "Varemottak"},
		{"RAA", "zone", "Råvarelager"},
		{"HALV", "zone", "Produksjon og halvfabrikat"},
		{"FERDIG", "zone", "Ferdigvarelager"},
	} {
		loc, err := w.post(w.owner, "/api/v1/inventory/locations", obj{"parentId": root, "kind": l.kind, "code": l.code, "name": l.name, "sortOrder": i + 1})
		if err != nil {
			return err
		}
		w.locations[l.code] = id(loc, "id")
	}

	// Orders' sales orders are fulfilled from the workshop by default.
	settings, err := w.get(w.owner, "/api/v1/orders/settings")
	if err != nil {
		return err
	}
	settings["defaultSiteId"] = w.siteID
	if _, err := w.put(w.owner, "/api/v1/orders/settings", settingsBody(settings)); err != nil {
		return err
	}

	v := w.variants
	if _, err := w.post(w.owner, "/api/v1/inventory/movements/receipts", obj{
		"clientRequestId": uuid.NewString(), "siteId": w.siteID, "toLocationId": w.locations["RAA"],
		"supplierName": "Norsk Stål AS", "supplierReference": "PO-24117", "note": "Inngående beholdning",
		"lines": []obj{
			{"variantId": v["tube"], "quantity": 240},
			{"variantId": v["screw"], "quantity": 2000},
			{"variantId": v["plywood"], "quantity": 30},
		},
	}); err != nil {
		return err
	}
	if _, err := w.post(w.owner, "/api/v1/inventory/movements/receipts", obj{
		"clientRequestId": uuid.NewString(), "siteId": w.siteID, "toLocationId": w.locations["RAA"],
		"supplierName": "Jotun Powder Coatings", "supplierReference": "ORD-88412", "note": "Pulverlakk, tre partier",
		"lines": []obj{
			{"variantId": v["paint"], "quantity": 25, "lot": obj{"number": "P2511-A", "supplierLot": "JPC-511-0042", "productionDate": w.day(-20), "expiry": w.day(200)}},
			{"variantId": v["paint"], "quantity": 8, "lot": obj{"number": "P2509-B", "supplierLot": "JPC-509-0017", "productionDate": w.day(-300), "expiry": w.day(21)}},
			{"variantId": v["paint"], "quantity": 15, "lot": obj{"number": "P2511-C", "supplierLot": "JPC-511-0051", "productionDate": w.day(-6), "expiry": w.day(230)}},
		},
	}); err != nil {
		return err
	}
	if _, err := w.post(w.owner, "/api/v1/inventory/movements/receipts", obj{
		"clientRequestId": uuid.NewString(), "siteId": w.siteID, "toLocationId": w.locations["HALV"],
		"note": "Inngående beholdning, halvfabrikat",
		"lines": []obj{
			{"variantId": v["frame"], "quantity": 3},
			{"variantId": v["top"], "quantity": 4},
		},
	}); err != nil {
		return err
	}
	if _, err := w.post(w.owner, "/api/v1/inventory/movements/receipts", obj{
		"clientRequestId": uuid.NewString(), "siteId": w.siteID, "toLocationId": w.locations["FERDIG"],
		"supplierName": "Verktøygrossisten AS", "supplierReference": "VG-5521",
		"lines": []obj{
			{"variantId": v["pegboard"], "quantity": 12},
			{"variantId": v["vise"], "quantity": 6},
		},
	}); err != nil {
		return err
	}

	// The newest paint lot waits for its incoming inspection.
	lots, err := w.get(w.owner, "/api/v1/inventory/lots?q=P2511-C")
	if err != nil {
		return err
	}
	var heldLot int64
	for _, l := range list(lots, "lots") {
		if str(l, "number") == "P2511-C" {
			heldLot = id(l, "id")
		}
	}
	if heldLot == 0 {
		return errors.New("the paint lot P2511-C was not found after its receipt")
	}
	if _, err := w.post(w.owner, fmt.Sprintf("/api/v1/inventory/lots/%d/holds", heldLot), obj{
		"kind": "quarantine", "cause": "inspection", "note": "Venter på mottakskontroll av kulørprøve.",
	}); err != nil {
		return err
	}

	for _, p := range []struct {
		key                   string
		location              string
		reorderPoint, maximum float64
	}{
		{"tube", "RAA", 250, 480},
		{"frame", "HALV", 4, 10},
	} {
		if _, err := w.put(w.owner, fmt.Sprintf("/api/v1/inventory/variants/%d/placements/%d", v[p.key], w.siteID), obj{
			"primaryLocationId": w.locations[p.location], "reorderPoint": p.reorderPoint, "maximum": p.maximum,
		}, "If-None-Match", "*"); err != nil {
			return err
		}
	}
	return nil
}

// settingsBody is Orders' settings as their PUT takes them, from their GET.
func settingsBody(s obj) obj {
	body := obj{}
	for _, k := range []string{
		"revision", "quoteSeriesStart", "orderSeriesStart", "quoteValidityDays", "defaultPaymentTermsDays",
		"confirmationOnConfirm", "combineByDefault", "mailTexts", "defaultSiteId", "freightMarkupPercent",
		"freightVariantId", "sendChannelId", "shippingNoticeOnBook", "subscriptionFinalPeriodDefault",
	} {
		if v, ok := s[k]; ok && v != nil {
			body[k] = v
		}
	}
	return body
}

// weekdays is a work center's pattern: 07:00–15:00 Monday to Friday.
func weekdays() obj {
	day := []obj{{"start": "07:00", "end": "15:00"}}
	return obj{"monday": day, "tuesday": day, "wednesday": day, "thursday": day, "friday": day, "saturday": []obj{}, "sunday": []obj{}}
}

// seedWorkCenters makes the workshop's four work centers and the finishing
// group of the two benches an operation may run at.
func (w *workshop) seedWorkCenters() error {
	w.centers = map[string]int64{}
	for _, c := range []struct {
		code, name, setup, machine, labour string
		capacity                           int
	}{
		{"KAPP", "Kappsag", "15", "180", "650", 1},
		{"SVEIS", "Sveisecelle", "20", "240", "720", 1},
		{"MONT", "Monteringsbenk", "10", "", "620", 2},
		{"LAKK", "Lakkbu", "30", "320", "650", 1},
	} {
		body := obj{"code": c.code, "name": c.name, "siteId": w.siteID, "capacity": c.capacity, "setupMinutes": c.setup,
			"labourCostPerHour": c.labour, "pattern": weekdays()}
		if c.machine != "" {
			body["machineCostPerHour"] = c.machine
		}
		center, err := w.post(w.planner, "/api/v1/manufacturing/work-centers", body)
		if err != nil {
			return err
		}
		w.centers[c.code] = id(center, "id")
	}
	group, err := w.post(w.planner, "/api/v1/manufacturing/work-center-groups", obj{
		"code": "ETTERARB", "name": "Etterarbeid", "siteId": w.siteID, "memberIds": []int64{w.centers["MONT"], w.centers["SVEIS"]},
	})
	if err != nil {
		return err
	}
	w.groupID = id(group, "id")
	return nil
}

// seedBoms makes the bills of materials: the frame (with its offcuts as a
// by-product) and the top, the workbench made of them — two levels, with a
// parallel branch and a quality check — the make-to-order shelf unit, and
// the starter kit.
func (w *workshop) seedBoms() error {
	v, c := w.variants, w.centers
	w.boms = map[string]int64{}
	for _, b := range []struct {
		key  string
		body obj
	}{
		{"frame", obj{
			"variantId": v["frame"], "code": "RAMME-160", "outputQuantity": "1", "isDefault": true, "lotPattern": "{MO}",
			"note": "Kappes, sveises og pulverlakkeres.",
			"operations": []obj{
				{"key": "kapp", "sequence": 10, "name": "Kapping av rør", "workCenterId": c["KAPP"], "minutesPerCycle": "12",
					"instructions": "Kapp 4 × 700 mm, 4 × 1500 mm og 4 × 800 mm. Avgrad endene."},
				{"key": "sveis", "sequence": 20, "name": "Sveising av ramme", "workCenterId": c["SVEIS"], "minutesPerCycle": "35", "dependsOn": []string{"kapp"},
					"instructions": "Hefte i jigg, kontroller diagonalene, sveis ut."},
				{"key": "lakk", "sequence": 30, "name": "Pulverlakkering", "workCenterId": c["LAKK"], "minutesPerCycle": "18", "dependsOn": []string{"sveis"}},
			},
			"lines": []obj{
				{"variantId": v["tube"], "quantity": "12", "scrapPercent": "3", "operationKey": "kapp", "sortOrder": 1},
				{"variantId": v["paint"], "quantity": "0.8", "operationKey": "lakk", "sortOrder": 2},
			},
			"outputs": []obj{{"variantId": v["offcut"], "quantity": "0.6", "costShare": "1"}},
		}},
		{"top", obj{
			"variantId": v["top"], "code": "PLATE-160", "outputQuantity": "1", "isDefault": true,
			"operations": []obj{
				{"key": "kapp", "sequence": 10, "name": "Kapping av plate", "workCenterId": c["KAPP"], "minutesPerCycle": "8"},
				{"key": "olje", "sequence": 20, "name": "Sliping og oljing", "workCenterId": c["LAKK"], "minutesPerCycle": "20", "dependsOn": []string{"kapp"}},
			},
			"lines": []obj{
				{"variantId": v["plywood"], "quantity": "0.5", "operationKey": "kapp", "sortOrder": 1},
			},
		}},
		{"workbench", obj{
			"variantId": v["workbench"], "code": "ARB-160", "outputQuantity": "1", "isDefault": true, "leadTimeDays": 3,
			"note": "Ramme og plate klargjøres parallelt før montering.",
			"operations": []obj{
				{"key": "ramme", "sequence": 10, "name": "Klargjøring av ramme", "workCenterGroupId": w.groupID, "minutesPerCycle": "10",
					"instructions": "Kontroller lakken, monter regulerbare føtter."},
				{"key": "plate", "sequence": 20, "name": "Kantlisting av plate", "workCenterId": c["MONT"], "minutesPerCycle": "15"},
				{"key": "mont", "sequence": 30, "name": "Sluttmontering", "workCenterId": c["MONT"], "minutesPerCycle": "25", "dependsOn": []string{"ramme", "plate"},
					"instructions": "Skru platen fast med 8 skruer, kontroller planhet.",
					"checks": []obj{
						{"kind": "measure", "title": "Planhet benkeplate", "frequency": "each_work_order", "nominal": "0", "max": "1.5", "unit": "mm",
							"instructions": "Mål med rettholt over diagonalen."},
						{"kind": "pass_fail", "title": "Benken står stødig uten vipping", "frequency": "each_work_order"},
					}},
			},
			"lines": []obj{
				{"variantId": v["frame"], "quantity": "1", "operationKey": "ramme", "sortOrder": 1},
				{"variantId": v["top"], "quantity": "1", "operationKey": "plate", "sortOrder": 2},
				{"variantId": v["screw"], "quantity": "8", "operationKey": "mont", "sortOrder": 3},
			},
		}},
		{"shelf", obj{
			"variantId": v["shelf"], "code": "REOL-90", "outputQuantity": "1", "isDefault": true, "makeToOrder": true, "leadTimeDays": 5,
			"note": "Lages kun på kundeordre.",
			"operations": []obj{
				{"key": "kapp", "sequence": 10, "name": "Kapping", "workCenterId": c["KAPP"], "minutesPerCycle": "20"},
				{"key": "sveis", "sequence": 20, "name": "Sveising av gavler", "workCenterId": c["SVEIS"], "minutesPerCycle": "40", "dependsOn": []string{"kapp"}},
				{"key": "lakk", "sequence": 30, "name": "Pulverlakkering", "workCenterId": c["LAKK"], "minutesPerCycle": "25", "dependsOn": []string{"sveis"}},
				{"key": "mont", "sequence": 40, "name": "Montering av hyller", "workCenterId": c["MONT"], "minutesPerCycle": "15", "dependsOn": []string{"lakk"}},
			},
			"lines": []obj{
				{"variantId": v["tube"], "quantity": "16", "scrapPercent": "3", "operationKey": "kapp", "sortOrder": 1},
				{"variantId": v["plywood"], "quantity": "1", "operationKey": "mont", "sortOrder": 2},
				{"variantId": v["screw"], "quantity": "20", "operationKey": "mont", "sortOrder": 3},
				{"variantId": v["paint"], "quantity": "1.1", "operationKey": "lakk", "sortOrder": 4},
			},
		}},
		{"kit", obj{
			"variantId": v["kit"], "code": "SETT-START", "outputQuantity": "1", "isDefault": true, "kind": "kit",
			"note":       "Plukkes og leveres som sett.",
			"operations": []obj{},
			"lines": []obj{
				{"variantId": v["workbench"], "quantity": "1", "sortOrder": 1},
				{"variantId": v["pegboard"], "quantity": "1", "sortOrder": 2},
				{"variantId": v["vise"], "quantity": "1", "sortOrder": 3},
			},
		}},
	} {
		bom, err := w.post(w.planner, "/api/v1/manufacturing/boms", b.body)
		if err != nil {
			return err
		}
		w.boms[b.key] = id(bom, "id")
	}
	return nil
}

// newOrder creates a draft manufacturing order and answers it.
func (w *workshop) newOrder(body obj) (obj, error) {
	body["siteId"] = w.siteID
	body["responsibleUserId"] = w.planner.id
	o, err := w.post(w.planner, "/api/v1/manufacturing/orders", body)
	if err != nil {
		return nil, err
	}
	w.orderNumbers = append(w.orderNumbers, str(o, "number"))
	return o, nil
}

// confirm confirms a draft (snapshotting it and reserving its components) and
// answers the order.
func (w *workshop) confirm(o obj) (obj, error) {
	answer, err := w.post(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d/confirm", id(o, "id")), obj{"revision": id(o, "revision")})
	if err != nil {
		return nil, err
	}
	if inner, ok := answer["order"].(obj); ok {
		return inner, nil
	}
	return answer, nil
}

// plan puts a confirmed order's work orders on the schedule.
func (w *workshop) plan(o obj) (obj, error) {
	return w.post(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d/plan", id(o, "id")), obj{"revision": id(o, "revision")})
}

// workOrders lists an order's work orders, by sequence.
func (w *workshop) workOrders(orderID int64) ([]obj, error) {
	out, err := w.get(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d/work-orders", orderID))
	if err != nil {
		return nil, err
	}
	return list(out, "items"), nil
}

// seedOrders makes the manufacturing orders: a workbench order done, with
// its time, checks and production; a frame order in progress, its first
// work order started on the shop floor; a confirmed workbench order short of
// frames and tops; a late top order; and a draft for next month.
func (w *workshop) seedOrders() error {
	v, loc := w.variants, w.locations

	// Done: two workbenches made from the opening stock of frames and tops.
	done, err := w.newOrder(obj{"variantId": v["workbench"], "quantity": "2", "dueDate": w.workdayBefore(1).Format(time.DateOnly), "requestedStart": w.workdayBefore(5).Format(time.DateOnly),
		"componentLocationId": loc["HALV"], "outputLocationId": loc["FERDIG"], "note": "Lagerordre, uke 1."})
	if err != nil {
		return err
	}
	if done, err = w.confirm(done); err != nil {
		return err
	}
	if err := w.workThrough(done); err != nil {
		return err
	}

	// In progress: four frames, the cutting started by the operator.
	frames, err := w.newOrder(obj{"variantId": v["frame"], "quantity": "4", "dueDate": w.day(6),
		"componentLocationId": loc["RAA"], "outputLocationId": loc["HALV"]})
	if err != nil {
		return err
	}
	if frames, err = w.confirm(frames); err != nil {
		return err
	}
	if _, err := w.plan(frames); err != nil {
		return err
	}
	wos, err := w.workOrders(id(frames, "id"))
	if err != nil {
		return err
	}
	if len(wos) == 0 {
		return errors.New("the frame order has no work orders")
	}
	if _, err := w.post(w.operator, fmt.Sprintf("/api/v1/manufacturing/work-orders/%d/start", id(wos[0], "id")), obj{}); err != nil {
		return err
	}

	// Confirmed and planned: three workbenches, short of frames and tops
	// until the frame order and the next top order are done.
	benches, err := w.newOrder(obj{"variantId": v["workbench"], "quantity": "3", "dueDate": w.day(12),
		"componentLocationId": loc["HALV"], "outputLocationId": loc["FERDIG"], "note": "Til lager før vårsesongen."})
	if err != nil {
		return err
	}
	if benches, err = w.confirm(benches); err != nil {
		return err
	}
	if _, err := w.plan(benches); err != nil {
		return err
	}

	// Late: four tops that were due two days ago.
	tops, err := w.newOrder(obj{"variantId": v["top"], "quantity": "4", "dueDate": w.day(-2), "priority": "urgent",
		"componentLocationId": loc["RAA"], "outputLocationId": loc["HALV"]})
	if err != nil {
		return err
	}
	if tops, err = w.confirm(tops); err != nil {
		return err
	}
	if _, err := w.plan(tops); err != nil {
		return err
	}

	// Draft: five workbenches for next month.
	if _, err := w.newOrder(obj{"variantId": v["workbench"], "quantity": "5", "dueDate": w.day(35), "requestedStart": w.day(24),
		"componentLocationId": loc["HALV"], "outputLocationId": loc["FERDIG"], "note": "Utkast – avventer salgsprognose."}); err != nil {
		return err
	}

	// A replenishment run, for the worker to make its suggestions from.
	if _, err := w.post(w.planner, "/api/v1/manufacturing/replenishment/runs", obj{"siteId": w.siteID}); err != nil {
		return err
	}
	return nil
}

// workThrough works a confirmed order from start to close as the shop floor
// would have over the last working days, one work order a day: each work
// order's time logged for the operator, its checks recorded, finished in
// turn; then the production posted — serial numbers out — and the order
// closed.
func (w *workshop) workThrough(o obj) error {
	orderID := id(o, "id")
	wos, err := w.workOrders(orderID)
	if err != nil {
		return err
	}
	slices.SortFunc(wos, func(a, b obj) int { return int(id(a, "sequence") - id(b, "sequence")) })
	for i, wo := range wos {
		woID := id(wo, "id")
		d := w.workdayBefore(len(wos) + 1 - i)
		from := time.Date(d.Year(), d.Month(), d.Day(), 8, 0, 0, 0, w.zone)
		minutes := id(wo, "expectedMinutes")
		if minutes <= 0 {
			minutes = 30
		}
		if _, err := w.post(w.planner, fmt.Sprintf("/api/v1/manufacturing/work-orders/%d/time-logs", woID), obj{
			"userId": w.operator.id, "kind": "productive", "startedAt": from.Format(time.RFC3339),
			"endedAt": from.Add(time.Duration(minutes+5) * time.Minute).Format(time.RFC3339), "note": "Ført etter arbeidsseddelen.",
		}); err != nil {
			return err
		}
		for _, check := range list(wo, "checks") {
			result := obj{"result": "pass"}
			switch str(check, "kind") {
			case "measure":
				result["value"] = "0.6"
			case "instruction":
				result["result"] = "done"
			}
			if _, err := w.post(w.operator, fmt.Sprintf("/api/v1/manufacturing/work-orders/%d/checks/%d/result", woID, id(check, "id")), result); err != nil {
				return err
			}
		}
		if _, err := w.post(w.operator, fmt.Sprintf("/api/v1/manufacturing/work-orders/%d/finish", woID), obj{}); err != nil {
			return err
		}
	}

	draft, err := w.get(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d/production-draft", orderID))
	if err != nil {
		return err
	}
	var lines []obj
	for _, l := range list(draft, "lines") {
		line := obj{"componentId": id(l, "componentId")}
		// The sub-assemblies wait in production; the rest is taken from the
		// raw materials store.
		if variant, ok := l["variant"].(obj); ok {
			from := w.locations["RAA"]
			if vid := id(variant, "id"); vid == int64(w.variants["frame"]) || vid == int64(w.variants["top"]) {
				from = w.locations["HALV"]
			}
			line["fromLocationId"] = from
		}
		if p, ok := l["proposed"].(string); ok {
			line["consumed"] = p
		} else if r, ok := l["required"].(string); ok {
			line["consumed"] = r
		}
		lines = append(lines, line)
	}
	body := obj{"clientRequestId": uuid.NewString(), "quantity": str(draft, "quantity"), "lines": lines}
	if out, ok := draft["output"].(obj); ok && str(out, "tracking") == "serial" {
		body["outputSerials"] = obj{"first": "AB-1001"}
	}
	if _, err := w.post(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d/productions", orderID), body); err != nil {
		return err
	}
	current, err := w.get(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d", orderID))
	if err != nil {
		return err
	}
	_, err = w.post(w.planner, fmt.Sprintf("/api/v1/manufacturing/orders/%d/close", orderID), obj{"revision": id(current, "revision")})
	return err
}

// seedSalesOrders makes the sales orders: a confirmed one for two shelf units
// (made to order), a confirmed one for the starter kit with its stock
// reserved, and a draft.
func (w *workshop) seedSalesOrders() error {
	if err := w.mapStandardVat(); err != nil {
		return err
	}
	v := w.variants
	for _, so := range []struct {
		customer  int32
		reference string
		confirm   bool
		reserve   bool
		lines     []obj
	}{
		// Made to order: nothing to reserve until Manufacturing has made it.
		{w.customers[0], "Lagerreoler til nytt lager", true, false, []obj{
			{"kind": "product", "variantId": v["shelf"], "quantity": 2},
		}},
		{w.customers[1], "Verksted, avdeling vest", true, true, []obj{
			{"kind": "product", "variantId": v["kit"], "quantity": 1},
			{"kind": "product", "variantId": v["pegboard"], "quantity": 2},
		}},
		{w.customers[2], "Tilbud arbeidsbenker", false, false, []obj{
			{"kind": "product", "variantId": v["workbench"], "quantity": 1},
			{"kind": "product", "variantId": v["vise"], "quantity": 2},
		}},
	} {
		order, err := w.post(w.owner, "/api/v1/orders/orders", obj{
			"clientRequestId": uuid.NewString(), "customerId": so.customer, "siteId": w.siteID,
			"yourReference": so.reference, "deliveryDate": w.day(14), "lines": so.lines,
		})
		if err != nil {
			return err
		}
		w.salesOrderNumbers = append(w.salesOrderNumbers, fmt.Sprint(order["number"]))
		if !so.confirm {
			continue
		}
		if _, err := w.post(w.owner, fmt.Sprintf("/api/v1/orders/orders/%d/confirm", id(order, "id")), obj{
			"version": id(order, "version"), "expectedUpdatedAt": str(order, "updatedAt"), "reserve": so.reserve,
		}); err != nil {
			return err
		}
	}
	return nil
}

// mapStandardVat maps the catalog's standard tax category to the outgoing
// 25 % VAT code, as an Owner does in Invoices' settings before the first
// sale, unless it is mapped already: a sales order line of an unmapped
// category cannot be confirmed.
func (w *workshop) mapStandardVat() error {
	var taxCategoryID, vatCodeID int32
	if err := w.pool.QueryRow(w.ctx, `SELECT id FROM products.tax_categories WHERE name = 'Standard MVA 25 %'`).Scan(&taxCategoryID); err != nil {
		return fmt.Errorf("find the standard tax category: %w", err)
	}
	var mapped bool
	if err := w.pool.QueryRow(w.ctx, `SELECT EXISTS (SELECT 1 FROM invoices.tax_category_vat_codes WHERE tax_category_id = $1)`, taxCategoryID).Scan(&mapped); err != nil {
		return err
	}
	if mapped {
		return nil
	}
	if err := w.pool.QueryRow(w.ctx, `SELECT id FROM invoices.vat_codes WHERE code = '3'`).Scan(&vatCodeID); err != nil {
		return fmt.Errorf("find VAT code 3: %w", err)
	}
	_, err := w.put(w.owner, fmt.Sprintf("/api/v1/invoices/settings/tax-category-vat-codes/%d", taxCategoryID), obj{"vatCodeId": vatCodeID})
	return err
}

// runWorkers runs, once each, the outbox's delivery and the two
// Manufacturing workers the seed's writes queued work for.
func (w *workshop) runWorkers(deps module.Deps, mods []module.Module, logger *slog.Logger) error {
	handlers, err := module.EventHandlers(deps, mods...)
	if err != nil {
		return err
	}
	if err := events.NewDispatcher(w.pool, handlers, logger, time.Now).RunOnce(w.ctx); err != nil {
		return fmt.Errorf("deliver the outbox: %w", err)
	}
	type cycler interface {
		RunCycle(context.Context) (int, error)
	}
	for _, wk := range module.Workers(deps, mods...) {
		if wk.Name() != "manufacturing.mto-worker" && wk.Name() != "manufacturing.replenishment" {
			continue
		}
		c, ok := wk.(cycler)
		if !ok {
			continue
		}
		if _, err := c.RunCycle(w.ctx); err != nil {
			return fmt.Errorf("run %s: %w", wk.Name(), err)
		}
	}
	return nil
}
