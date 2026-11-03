package inventory_test

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The paths the tests call.
const (
	metaPath        = "/api/v1/inventory/meta"
	sitesPath       = "/api/v1/inventory/sites"
	locationsPath   = "/api/v1/inventory/locations"
	bulkPath        = "/api/v1/inventory/locations/bulk"
	lookupPath      = "/api/v1/inventory/lookup"
	variantsPath    = "/api/v1/inventory/variants"
	movementsPath   = "/api/v1/inventory/movements"
	adjustmentsPath = "/api/v1/inventory/movements/adjustments"
	movesPath       = "/api/v1/inventory/movements/moves"
	receiptsPath    = "/api/v1/inventory/movements/receipts"
	statsPath       = "/api/v1/inventory/stats"
	attentionPath   = "/api/v1/inventory/stats/attention"
)

func sitePath(id int64) string     { return sitesPath + "/" + strconv.FormatInt(id, 10) }
func locationPath(id int64) string { return locationsPath + "/" + strconv.FormatInt(id, 10) }
func movementPath(id int64) string { return movementsPath + "/" + strconv.FormatInt(id, 10) }
func ifMatch(revision int32) modtest.RequestOption {
	return modtest.Header("If-Match", strconv.Itoa(int(revision)))
}

// The variants most tests keep stock of: two goods variants of one product,
// one of another, a service and a discontinued product's.
const (
	varBolt    int32 = 101
	varNut     int32 = 102
	varWasher  int32 = 201
	varService int32 = 301
	varOld     int32 = 401
	varDraft   int32 = 501
)

// seedCatalog seeds the fake catalog with the variants above.
func seedCatalog(h *harness) {
	barcode := "7040000000017"
	bolt := goods(varBolt, 10, "BOLT-M8", "Bolt M8", "pcs")
	bolt.Barcode = &barcode
	service := goods(varService, 30, "INSTALL", "Installation", "h")
	service.ProductType = "Service"
	old := goods(varOld, 40, "OLD-1", "Old thing", "pcs")
	old.ProductStatus = "Discontinued"
	draft := goods(varDraft, 50, "NEW-1", "New thing", "pcs")
	draft.ProductStatus = "Draft"
	h.catalog.seed(bolt, goods(varNut, 10, "NUT-M8", "Nut M8", "pcs"), goods(varWasher, 20, "WASH-8", "Washer 8", "kg"), service, old, draft)
}

// mustStatus fails the test unless res answered want.
func mustStatus(t *testing.T, res *modtest.Response, want int, what string) {
	t.Helper()
	if res.Status != want {
		t.Fatalf("%s = %d %s, want %d", what, res.Status, res.Body, want)
	}
}

// problemOf decodes a problem body.
func problemOf(t *testing.T, res *modtest.Response) gen.InventoryProblem {
	t.Helper()
	var p gen.InventoryProblem
	res.JSON(&p)
	return p
}

// expectRefusal fails the test unless res is a problem with status and code.
func expectRefusal(t *testing.T, res *modtest.Response, status int, code, what string) gen.InventoryProblem {
	t.Helper()
	if res.Status != status {
		t.Fatalf("%s = %d %s, want %d %s", what, res.Status, res.Body, status, code)
	}
	p := problemOf(t, res)
	if p.Code == nil || *p.Code != code {
		t.Fatalf("%s answered code %v (%s), want %s", what, p.Code, res.Body, code)
	}
	return p
}

// lineCodes is a posting refusal's lines as index:code.
func lineCodes(p gen.InventoryProblem) []string {
	if p.Lines == nil {
		return nil
	}
	out := make([]string, 0, len(*p.Lines))
	for _, l := range *p.Lines {
		out = append(out, fmt.Sprintf("%d:%s", l.Index, l.Code))
	}
	return out
}

// createSite creates a warehouse coded code.
func createSite(t *testing.T, c *modtest.Client, code string) gen.InventorySite {
	t.Helper()
	res := c.Do(http.MethodPost, sitesPath, gen.InventorySiteInput{Code: code, Name: "Site " + code, Kind: gen.Warehouse})
	mustStatus(t, res, http.StatusCreated, "POST /sites "+code)
	var s gen.InventorySite
	res.JSON(&s)
	return s
}

// createLocation creates a location of kind under parent.
func createLocation(t *testing.T, c *modtest.Client, parent int64, kind gen.InventoryLocationKind, code string) gen.InventoryLocation {
	t.Helper()
	res := c.Do(http.MethodPost, locationsPath, gen.InventoryLocationInput{ParentId: parent, Kind: kind, Code: code})
	mustStatus(t, res, http.StatusCreated, "POST /locations "+code)
	var l gen.InventoryLocation
	res.JSON(&l)
	return l
}

// getLocation reads a location.
func getLocation(t *testing.T, c *modtest.Client, id int64) gen.InventoryLocationDetail {
	t.Helper()
	res := c.Do(http.MethodGet, locationPath(id), nil)
	mustStatus(t, res, http.StatusOK, "GET /locations/{id}")
	var d gen.InventoryLocationDetail
	res.JSON(&d)
	return d
}

// receiptLine is a receipt line of quantity of variant to the receipt's
// default location.
func receiptLine(variant int32, quantity float64) gen.InventoryReceiptLine {
	return gen.InventoryReceiptLine{VariantId: variant, Quantity: quantity}
}

// receive posts a receipt to site's location to and answers the movement.
func receive(t *testing.T, c *modtest.Client, site, to int64, lines ...gen.InventoryReceiptLine) gen.InventoryMovement {
	t.Helper()
	res := c.Do(http.MethodPost, receiptsPath, gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: site, ToLocationId: to, Lines: lines})
	mustStatus(t, res, http.StatusCreated, "POST /movements/receipts")
	var m gen.InventoryMovement
	res.JSON(&m)
	return m
}

// onHand is what lies of variant on location, as the balance's text; "" when
// there is no balance.
func onHand(t *testing.T, h *harness, variant int32, location int64) string {
	t.Helper()
	if h.Count(t, `SELECT count(*) FROM inventory.balances WHERE variant_id = $1 AND location_id = $2`, variant, location) == 0 {
		return ""
	}
	return modtest.One[string](t, h.Harness, `SELECT on_hand::text FROM inventory.balances WHERE variant_id = $1 AND location_id = $2`, variant, location)
}

// assertLedgerConsistent is the rebuild check (design §10): every balance
// equals the sum of the movement lines into its location less those out of
// it, no pair the lines leave non-zero lacks a balance, and no balance is at
// or below zero.
func assertLedgerConsistent(t *testing.T, h *harness) {
	t.Helper()
	const sums = `
WITH flows AS (
    SELECT variant_id, to_location_id AS location_id, quantity AS q FROM inventory.movement_lines WHERE to_location_id IS NOT NULL
    UNION ALL
    SELECT variant_id, from_location_id, -quantity FROM inventory.movement_lines WHERE from_location_id IS NOT NULL
), sums AS (
    SELECT variant_id, location_id, sum(q) AS q FROM flows GROUP BY variant_id, location_id HAVING sum(q) <> 0
)`
	if n := h.Count(t, sums+`
SELECT count(*) FROM sums s FULL JOIN inventory.balances b ON b.variant_id = s.variant_id AND b.location_id = s.location_id
WHERE s.q IS DISTINCT FROM b.on_hand`); n != 0 {
		t.Errorf("%d balance(s) differ from the sum of the movement lines", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.balances WHERE on_hand <= 0`); n != 0 {
		t.Errorf("%d balance(s) at or below zero", n)
	}
}

// eventKinds is the record's kinds, in order.
func eventKinds(t *testing.T, h *harness) []string {
	t.Helper()
	rows, err := h.Pool().Query(t.Context(), `SELECT kind FROM inventory.events ORDER BY id`)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		out = append(out, k)
	}
	return out
}

// ptr is a pointer to v.
func ptr[T any](v T) *T { return &v }
