package inventory_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestPlacementImport_ConcurrentWithArchive is the commit's lock order (I61):
// two imports naming the same placements in opposite orders and an archive
// clearing a primary among them, at once, on a pool of four — every one
// answers, none deadlocks into a 500.
func TestPlacementImport_ConcurrentWithArchive(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	rows := []string{
		"BOLT-M8;OSL;OSL-1;%d;", "NUT-M8;OSL;;%d;", "WASH-8;OSL;;%d;", "BOLT-M8;BGO;;%d;", "NUT-M8;BGO;BGO-B1;%d;", "WASH-8;BGO;;%d;",
	}
	file := func(round int, reverse bool) string {
		lines := slices.Clone(rows)
		if reverse {
			slices.Reverse(lines)
		}
		var b strings.Builder
		b.WriteString("sku;site;primary_location;reorder_point;maximum\n")
		for i, l := range lines {
			fmt.Fprintf(&b, l+"\n", round*10+i)
		}
		return b.String()
	}
	for round := 1; round <= 4; round++ {
		bin := createLocation(t, c, s.osl.RootLocationId, gen.InventoryLocationKindBin, fmt.Sprintf("X%d", round))
		h.Exec(t, `INSERT INTO inventory.placements (variant_id, site_id, primary_location_id, reorder_point, sku, product_name, updated_at)
			VALUES ($1, $2, $3, 1, 'WASH-8', 'Washer 8', now())
			ON CONFLICT (variant_id, site_id) DO UPDATE SET primary_location_id = excluded.primary_location_id`, varWasher, s.osl.Id, bin.Id)
		statuses := make([]int, 3)
		var wg sync.WaitGroup
		wg.Go(func() { statuses[0] = importPlacements(c, true, file(round, false)).Status })
		wg.Go(func() { statuses[1] = importPlacements(c, true, file(round, true)).Status })
		wg.Go(func() {
			statuses[2] = putLocation(c, bin, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }).Status
		})
		wg.Wait()
		if statuses[0] != http.StatusCreated || statuses[1] != http.StatusCreated || statuses[2] != http.StatusOK {
			t.Fatalf("round %d answered %v, want 201, 201, 200", round, statuses)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements`); n != 6 {
		t.Errorf("%d placements, want the six the files name", n)
	}
}

// TestLowStock_CSVRenameMidExport is I58's snapshot refresh against the
// CSV's pages: a rename that sorts a row past the cursor does not export it
// twice, and the snapshot is refreshed once the file is written. Not
// parallel: it shrinks the export's page for its own run.
func TestLowStock_CSVRenameMidExport(t *testing.T) {
	old := inventory.SetLowStockExportPage(1)
	t.Cleanup(func() { inventory.SetLowStockExportPage(old) })
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	setPlacement(t, c, varBolt, s.osl.Id, reorderAt(30))
	setPlacement(t, c, varNut, s.osl.Id, reorderAt(30))
	setPlacement(t, c, varWasher, s.osl.Id, reorderAt(30))
	h.catalog.seed(goods(varBolt, 10, "ZZ-BOLT", "Bolt M8", "pcs")) // read on page 1, sorts last once refreshed

	res := c.Do(http.MethodGet, "/api/v1/inventory/low-stock.csv", nil)
	mustStatus(t, res, http.StatusOK, "GET /low-stock.csv")
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(string(res.Body), "\ufeff"), "\r\n"), "\r\n")[1:]
	var skus []string
	for _, l := range lines {
		skus = append(skus, strings.Split(l, ";")[1])
	}
	if !slices.Equal(skus, []string{"ZZ-BOLT", "NUT-M8", "WASH-8"}) {
		t.Errorf("exported %v, want each row once, in the order the export began with", skus)
	}
	if sku := modtest.One[string](t, h.Harness, `SELECT sku FROM inventory.placements WHERE variant_id = $1`, varBolt); sku != "ZZ-BOLT" {
		t.Errorf("snapshot = %q, want it refreshed after the export", sku)
	}
}

// TestPlacementImport_SiteGone is a site deleted between the rows' reading
// and the commit: a 400, nothing written, never a foreign key's 500. Not
// parallel: it installs the commit's hook.
func TestPlacementImport_SiteGone(t *testing.T) {
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	trd := createSite(t, c, "TRD")
	inventory.SetPlacementsResolvedHook(func() {
		h.Exec(t, `DELETE FROM inventory.locations WHERE site_id = $1`, trd.Id)
		h.Exec(t, `DELETE FROM inventory.sites WHERE id = $1`, trd.Id)
	})
	t.Cleanup(func() { inventory.SetPlacementsResolvedHook(nil) })
	res := importPlacements(c, true, "sku;site;primary_location;reorder_point;maximum\nBOLT-M8;OSL;;1;\nBOLT-M8;TRD;;1;\n")
	expectRefusal(t, res, http.StatusBadRequest, "site_archived", "a site deleted before the commit")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE site_id = $1`, s.osl.Id); n != 0 {
		t.Errorf("%d placements written by a refused commit", n)
	}
}

// TestPlacement_Events is what the record says of placements (I64): the
// import writes placement_set and placement_cleared for every placement it
// changes beside its own placements_imported, whose rows are the file's,
// and a replay answers what the first commit wrote; a primary cleared by an
// archive names the location it had, in fields as every placement event
// does.
func TestPlacement_Events(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	setPlacement(t, c, varWasher, s.osl.Id, gen.InventoryPlacementInput{ReorderPoint: qty(1)})
	setPlacement(t, c, varNut, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &s.shelf2.Id, ReorderPoint: qty(2)})
	before := eventKinds(t, h)

	file := "sku;site;primary_location;reorder_point;maximum\n" +
		"BOLT-M8;OSL;OSL-1;5;\n" + // new: set
		"NUT-M8;OSL;OSL-2;2;\n" + // unchanged: no event
		"WASH-8;OSL;;;\n" + // cleared
		"BOLT-M8;OSL;OSL-1;6;\n" // last wins: one write
	first := placementImportResult(t, importPlacements(c, true, file), http.StatusCreated, "commit")
	after := eventKinds(t, h)
	if d := countOf(after, "placement_set") - countOf(before, "placement_set"); d != 1 {
		t.Errorf("placement_set events written by the import = %d, want 1 (the bolt)", d)
	}
	if d := countOf(after, "placement_cleared") - countOf(before, "placement_cleared"); d != 1 {
		t.Errorf("placement_cleared events written by the import = %d, want 1 (the washer)", d)
	}
	if rows := modtest.One[string](t, h.Harness, `SELECT payload->>'rows' || '|' || (payload->>'writes') FROM inventory.events WHERE kind = 'placements_imported'`); rows != "4|3" {
		t.Errorf("the import event's rows|writes = %q, want 4|3", rows)
	}
	again := placementImportResult(t, importPlacements(c, true, file), http.StatusOK, "replay")
	if *again.Set != *first.Set || *again.Cleared != *first.Cleared || *first.Set != 2 || *first.Cleared != 1 {
		t.Errorf("replay set %d cleared %d, first %d %d; want the first commit's 2 and 1", *again.Set, *again.Cleared, *first.Set, *first.Cleared)
	}

	// An archive clearing the nut's primary names it.
	empty := createLocation(t, c, s.osl.RootLocationId, gen.InventoryLocationKindShelf, "7")
	h.Exec(t, `UPDATE inventory.placements SET primary_location_id = $1 WHERE variant_id = $2`, empty.Id, varNut)
	mustStatus(t, putLocation(c, empty, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive OSL-7")
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE kind = 'placement_cleared'
		AND (payload->'fields'->'primaryLocationId'->>'from')::bigint = $1`, empty.Id); n != 1 {
		t.Errorf("no placement_cleared event naming the primary location it cleared")
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE kind LIKE 'placement_%' AND jsonb_typeof(payload->'fields') <> 'object'`); n != 0 {
		t.Errorf("%d placement events whose fields are not the changes", n)
	}
}

// TestLowStock_CSVGuard is I60's file against a spreadsheet: a text cell
// that would run as a formula is guarded, a negative number is not.
func TestLowStock_CSVGuard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c) // 10 nuts at OSL
	h.catalog.seed(goods(601, 60, "=SUM", "Formula", "pcs"))
	setPlacement(t, c, 601, s.osl.Id, reorderAt(1))
	setPlacement(t, c, varNut, s.osl.Id, reorderAt(1))
	h.Exec(t, `INSERT INTO inventory.reservations (variant_id, site_id, quantity, source_module, source_kind, source_id, source_line_id, created_at, updated_at)
		VALUES ($1, $2, 15, 'orders', 'order', '1', '1', now(), now())`, varNut, s.osl.Id)
	res := c.Do(http.MethodGet, "/api/v1/inventory/low-stock.csv", nil)
	mustStatus(t, res, http.StatusOK, "GET /low-stock.csv")
	body := string(res.Body)
	if !strings.Contains(body, "\r\nOSL;'=SUM;Formula;;0;") {
		t.Errorf("the formula SKU is not guarded:\n%s", body)
	}
	if !strings.Contains(body, "\r\nOSL;NUT-M8;Nut M8;;-5;0;1;;7;7;;") {
		t.Errorf("a negative available is not written as a number:\n%s", body)
	}
}
