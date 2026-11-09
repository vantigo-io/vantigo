package inventory_test

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const placementsImportPath = "/api/v1/inventory/imports/placements"

// importPlacements posts a placements file, a commit when commit is true.
func importPlacements(c *modtest.Client, commit bool, file string) *modtest.Response {
	path := placementsImportPath
	if commit {
		path += "?dryRun=false"
	}
	return c.Do(http.MethodPost, path, nil, modtest.RawBody("text/csv", []byte(file)))
}

// placementImportResult decodes the import's answer.
func placementImportResult(t *testing.T, res *modtest.Response, want int, what string) gen.InventoryPlacementImportResult {
	t.Helper()
	mustStatus(t, res, want, what)
	var out gen.InventoryPlacementImportResult
	res.JSON(&out)
	return out
}

func placementOutcomes(r gen.InventoryPlacementImportResult) []string {
	out := make([]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		out = append(out, fmt.Sprintf("%d:%s", row.Row, row.Outcome))
	}
	return out
}

// TestPlacementImport is I61: a dry run answers each row's outcome and
// writes nothing; a commit with a failing row writes nothing; a good file
// writes last-wins, bumping revisions, an empty cell clearing its field and
// a row clearing all three removing the placement; the SKUs resolved in one
// LookupSKUs; the same file again writes nothing (idempotent by the file).
func TestPlacementImport(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	archived := createLocation(t, c, s.osl.RootLocationId, gen.InventoryLocationKindShelf, "9")
	mustStatus(t, putLocation(c, archived, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive OSL-9")
	trd := createSite(t, c, "TRD")
	mustStatus(t, c.Do(http.MethodPut, sitePath(trd.Id), gen.InventorySiteUpdate{Code: "TRD", Name: trd.Name, Kind: trd.Kind, Status: gen.Archived}, ifMatch(trd.Revision)),
		http.StatusOK, "archive TRD")
	setPlacement(t, c, varWasher, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &s.shelf2.Id, ReorderPoint: qty(1)})

	bad := "SKU;Site;Primary_Location;Reorder_Point;Maximum\n" +
		"bolt-m8;osl;osl-1;5;20\n" + // 2 ok
		"BOLT-M8;NOPE;;1;\n" + // 3 site_not_found
		"BOLT-M8;TRD;;1;\n" + // 4 site_archived
		"NOPE;OSL;;1;\n" + // 5 variant_not_found
		"INSTALL;OSL;;1;\n" + // 6 variant_not_stockable
		"BOLT-M8;OSL;BGO-B1;1;\n" + // 7 placement_location_invalid (another site)
		"BOLT-M8;OSL;OSL-9;1;\n" + // 8 placement_location_invalid (archived)
		"BOLT-M8;OSL;;x;\n" + // 9 quantity_invalid
		"BOLT-M8;OSL;;;0\n" + // 10 quantity_invalid (a maximum of 0)
		"BOLT-M8;OSL;;10;5\n" // 11 maximum_below_reorder_point
	before := h.catalog.lookupCalls("LookupSKUs")
	r := placementImportResult(t, importPlacements(c, false, bad), http.StatusOK, "dry run")
	want := []string{"2:ok", "3:site_not_found", "4:site_archived", "5:variant_not_found", "6:variant_not_stockable",
		"7:placement_location_invalid", "8:placement_location_invalid", "9:quantity_invalid", "10:quantity_invalid", "11:maximum_below_reorder_point"}
	if got := placementOutcomes(r); r.Ok || !slices.Equal(got, want) || r.Set != nil {
		t.Errorf("dry run = %v (ok %v), want %v", got, r.Ok, want)
	}
	if n := h.catalog.lookupCalls("LookupSKUs") - before; n != 1 {
		t.Errorf("LookupSKUs called %d times, want once", n)
	}
	placementImportResult(t, importPlacements(c, true, bad), http.StatusOK, "a commit with failing rows")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements`); n != 1 {
		t.Fatalf("%d placements after a refused commit, want the one there", n)
	}

	mustStatus(t, importPlacements(c, false, "sku,site,quantity\nBOLT-M8,OSL,1\n"), http.StatusBadRequest, "a wrong header")

	good := "sku,site,primary_location,reorder_point,maximum\n" +
		"BOLT-M8,OSL,OSL-1,5,20\n" +
		"NUT-M8,OSL,,\"2,5\",\n" +
		"BOLT-M8,OSL,OSL-2,6,\n" + // last wins over row 2
		"WASH-8,OSL,,,\n" // clears all three: removes the washer's placement
	dry := placementImportResult(t, importPlacements(c, false, good), http.StatusOK, "dry run of a good file")
	if !dry.Ok || h.Count(t, `SELECT count(*) FROM inventory.placements`) != 1 {
		t.Fatalf("dry run = %+v, want ok and nothing written", dry)
	}
	done := placementImportResult(t, importPlacements(c, true, good), http.StatusCreated, "commit")
	if done.Set == nil || *done.Set != 2 || done.Cleared == nil || *done.Cleared != 1 {
		t.Errorf("commit = set %v cleared %v, want 2 and 1", done.Set, done.Cleared)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT primary_location_id::text || '|' || reorder_point::text || '|' || coalesce(maximum::text, '-')
		FROM inventory.placements WHERE variant_id = $1`, varBolt); got != strconv.FormatInt(s.shelf2.Id, 10)+"|6.000|-" {
		t.Errorf("bolt = %q, want the last row's", got)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT reorder_point::text FROM inventory.placements WHERE variant_id = $1 AND primary_location_id IS NULL`, varNut); got != "2.500" {
		t.Errorf("nut's reorder point = %q, want 2.500 from a decimal comma", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1`, varWasher); n != 0 {
		t.Errorf("the washer's placement was not removed")
	}

	// The same file again writes nothing.
	again := placementImportResult(t, importPlacements(c, true, good), http.StatusOK, "the same file again")
	if !again.Ok {
		t.Errorf("replay = %+v", again)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE revision > 1`); n != 0 {
		t.Errorf("%d placements' revisions bumped by the replay", n)
	}
	if n := countOf(eventKinds(t, h), "placements_imported"); n != 1 {
		t.Errorf("placements_imported events = %d, want 1", n)
	}

	// A new file changing a placement bumps its revision; If-Match on the
	// old revision is then stale.
	placementImportResult(t, importPlacements(c, true, "sku;site;primary_location;reorder_point;maximum\nNUT-M8;OSL;;3;\n"), http.StatusCreated, "a second file")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1 AND revision = 2`, varNut); n != 1 {
		t.Errorf("the nut's revision did not move on")
	}

	// Without stock-adjust.
	viewer, _ := signIn(t, h, stockViewer...)
	mustStatus(t, importPlacements(viewer, false, good), http.StatusForbidden, "without stock-adjust")
}

// TestLowStock_CSV is I60: the list's filter as an attachment, the columns
// in order, decimals with a comma.
func TestLowStock_CSV(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	setPlacement(t, c, varNut, s.osl.Id, reorderAt(12.5, 30))
	setPlacement(t, c, varBolt, s.bgo.Id, reorderAt(3))
	res := c.Do(http.MethodGet, "/api/v1/inventory/low-stock.csv?siteId="+strconv.FormatInt(s.osl.Id, 10), nil)
	mustStatus(t, res, http.StatusOK, "GET /low-stock.csv")
	if cd := res.Header("Content-Disposition"); !strings.Contains(cd, "low-stock-") || !strings.HasPrefix(res.Header("Content-Type"), "text/csv") {
		t.Errorf("headers = %q, %q", res.Header("Content-Disposition"), res.Header("Content-Type"))
	}
	lines := strings.Split(strings.TrimPrefix(string(res.Body), "\ufeff"), "\r\n")
	if len(lines) != 3 || lines[0] != "site;sku;name;barcode;available;incoming;reorder_point;maximum;needed;suggested;packaging;packaging_count" ||
		lines[1] != "OSL;NUT-M8;Nut M8;;10;0;12,5;30;20;20;;" {
		t.Errorf("csv = %q", lines)
	}
}

// TestLowStock_Attention is I62's lowStock: one item per active site with
// low placements, the count, the site as entity, for both views only.
func TestLowStock_Attention(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	setPlacement(t, c, varNut, s.osl.Id, reorderAt(12))
	setPlacement(t, c, varBolt, s.bgo.Id, reorderAt(3))
	setPlacement(t, c, varNut, s.bgo.Id, reorderAt(3))
	setPlacement(t, c, varBolt, s.osl.Id, reorderAt(3)) // 20 there: not low
	attention := func(c *modtest.Client) []gen.InventoryStatsAttentionItem {
		t.Helper()
		var items []gen.InventoryStatsAttentionItem
		res := c.Do(http.MethodGet, attentionPath, nil)
		mustStatus(t, res, http.StatusOK, "GET /stats/attention")
		res.JSON(&items)
		return items
	}
	items := attention(c)
	if len(items) != 2 || items[0].Type != gen.LowStock || items[0].EntityId != strconv.FormatInt(s.bgo.Id, 10) || items[0].Count == nil || *items[0].Count != 2 ||
		items[1].Count == nil || *items[1].Count != 1 || items[1].Title != "1 item at or below its reorder point at Site OSL" {
		t.Fatalf("attention = %+v, want BGO's 2 then OSL's 1", items)
	}
	if items[0].Title != "2 items at or below their reorder point at Site BGO" {
		t.Errorf("title = %q, want the site named", items[0].Title)
	}
	if !items[0].OccurredAt.Equal(h.Now()) && items[0].OccurredAt.Sub(h.Now()).Abs() > time.Second {
		t.Errorf("occurredAt = %v, want the read's time %v", items[0].OccurredAt, h.Now())
	}
	viewer, _ := signIn(t, h, locationsViewer...)
	if items := attention(viewer); len(items) != 0 {
		t.Errorf("attention without stock-view = %+v", items)
	}
}

// TestLowStock_Scale is the statement's cost on a seeded tenant (I58):
// 10,000 placements across three sites, every one low, the first page and a
// filtered one read well within a request's time — the statement walks the
// placements with a reorder point, not the catalog.
func TestLowStock_Scale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	var siteIDs []int64
	for _, code := range []string{"OSL", "BGO", "TRD"} {
		siteIDs = append(siteIDs, createSite(t, c, code).Id)
	}
	const perSite = 3334
	for i := range perSite {
		h.catalog.seed(goods(int32(10000+i), int32(10000+i), fmt.Sprintf("SKU-%05d", i), fmt.Sprintf("Item %d", i), "pcs"))
	}
	for _, site := range siteIDs {
		h.Exec(t, `INSERT INTO inventory.placements (variant_id, site_id, reorder_point, sku, product_name, updated_at)
			SELECT 10000 + g, $1, 5, 'SKU-' || lpad(g::text, 5, '0'), 'Item ' || g, now() FROM generate_series(0, $2 - 1) g`, site, perSite)
	}
	h.Exec(t, `ANALYZE inventory.placements`)
	start := time.Now()
	page := lowStock(t, c, "")
	if len(page.Rows) != 50 || page.Next == nil || page.Rows[0].Variant.Sku != "SKU-00000" {
		t.Fatalf("first page = %d rows, next %v", len(page.Rows), page.Next)
	}
	next := lowStock(t, c, "?cursor="+*page.Next)
	if next.Rows[0].Site.Code != page.Rows[0].Site.Code || next.Rows[0].Variant.Sku != "SKU-00050" {
		t.Errorf("second page starts at %s:%s", next.Rows[0].Site.Code, next.Rows[0].Variant.Sku)
	}
	lowStock(t, c, "?siteId="+strconv.FormatInt(siteIDs[2], 10)+"&q=item+33")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("three reads of the list over 10,000 placements took %v", elapsed)
	}
	var plan string
	rows, err := h.Pool().Query(t.Context(), `EXPLAIN SELECT p.variant_id FROM inventory.placements p
		WHERE p.reorder_point IS NOT NULL AND p.site_id = $1 ORDER BY p.sku, p.variant_id LIMIT 51`, siteIDs[0])
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan += line + "\n"
	}
	rows.Close()
	if !strings.Contains(plan, "ix_placements_site_sku") {
		t.Errorf("a site's placements by SKU are not read through the partial index:\n%s", plan)
	}
}
