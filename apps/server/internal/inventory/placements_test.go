package inventory_test

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The placement tests' keys: setting placements needs stock-adjust with both
// views (I63); the list reads with both views.
var placer = []string{"inventory:locations-view", "inventory:stock-view", "inventory:stock-adjust", "inventory:locations-manage", "inventory:transfers-manage", "inventory:receipts-manage"}

func placementPath(variant int32, site int64) string {
	return fmt.Sprintf("%s/%d/placements/%d", variantsPath, variant, site)
}

// createOnly is the If-None-Match: * a create sends.
var createOnly = modtest.Header("If-None-Match", "*")

// putPlacement writes a placement under the given precondition.
func putPlacement(c *modtest.Client, variant int32, site int64, in gen.InventoryPlacementInput, opts ...modtest.RequestOption) *modtest.Response {
	return c.Do(http.MethodPut, placementPath(variant, site), in, opts...)
}

// placementAnswer decodes a placement.
func placementAnswer(t *testing.T, res *modtest.Response, want int, what string) gen.InventoryPlacement {
	t.Helper()
	mustStatus(t, res, want, what)
	var p gen.InventoryPlacement
	res.JSON(&p)
	return p
}

// setPlacement creates a placement and answers it.
func setPlacement(t *testing.T, c *modtest.Client, variant int32, site int64, in gen.InventoryPlacementInput) gen.InventoryPlacement {
	t.Helper()
	return placementAnswer(t, putPlacement(c, variant, site, in, createOnly), http.StatusCreated, fmt.Sprintf("create the placement of %d at %d", variant, site))
}

// reorderAt is a placement of a reorder point and an optional maximum.
func reorderAt(reorderPoint float64, maximum ...float64) gen.InventoryPlacementInput {
	in := gen.InventoryPlacementInput{ReorderPoint: &reorderPoint}
	if len(maximum) > 0 {
		in.Maximum = &maximum[0]
	}
	return in
}

// TestPlacement_PutAndDelete is I54 and I61: If-None-Match * creates and a
// second create is revision_conflict; If-Match changes; neither or both is
// a 400; the field rules and their codes; the site and the variant; DELETE
// with If-Match; the events and the SKU snapshot.
func TestPlacement_PutAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)

	in := gen.InventoryPlacementInput{PrimaryLocationId: &s.shelf1.Id, ReorderPoint: qty(5), Maximum: qty(20)}
	p := setPlacement(t, c, varBolt, s.osl.Id, in)
	if p.Revision != 1 || p.PrimaryLocationId == nil || *p.PrimaryLocationId != s.shelf1.Id || p.Low {
		t.Errorf("created = %+v, want revision 1 at OSL-1, not low (20 on hand)", p)
	}
	if sku := modtest.One[string](t, h.Harness, `SELECT sku || '|' || product_name FROM inventory.placements WHERE variant_id = $1`, varBolt); sku != "BOLT-M8|Bolt M8" {
		t.Errorf("snapshot = %q, want the catalog's SKU and name", sku)
	}
	expectRefusal(t, putPlacement(c, varBolt, s.osl.Id, in, createOnly), http.StatusConflict, "revision_conflict", "a second create")

	// The preconditions.
	mustStatus(t, putPlacement(c, varBolt, s.osl.Id, in), http.StatusBadRequest, "neither header")
	mustStatus(t, putPlacement(c, varBolt, s.osl.Id, in, createOnly, ifMatch(1)), http.StatusBadRequest, "both headers")
	mustStatus(t, putPlacement(c, varBolt, s.osl.Id, in, modtest.Header("If-None-Match", "\"1\"")), http.StatusBadRequest, "If-None-Match not *")
	mustStatus(t, putPlacement(c, varNut, s.osl.Id, in, ifMatch(1)), http.StatusNotFound, "If-Match on no placement")
	expectRefusal(t, putPlacement(c, varBolt, s.osl.Id, in, ifMatch(7)), http.StatusConflict, "revision_conflict", "a stale If-Match")

	changed := placementAnswer(t, putPlacement(c, varBolt, s.osl.Id, reorderAt(25), ifMatch(p.Revision)), http.StatusOK, "change")
	if changed.Revision != 2 || changed.PrimaryLocationId != nil || changed.Maximum != nil || !changed.Low {
		t.Errorf("changed = %+v, want revision 2, no primary or maximum, low (20 ≤ 25)", changed)
	}

	// The field rules.
	expectRefusal(t, putPlacement(c, varNut, s.osl.Id, gen.InventoryPlacementInput{}, createOnly), http.StatusBadRequest, "placement_empty", "three nulls")
	expectRefusal(t, putPlacement(c, varNut, s.osl.Id, reorderAt(10, 5), createOnly), http.StatusBadRequest, "maximum_below_reorder_point", "a maximum below")
	expectRefusal(t, putPlacement(c, varNut, s.osl.Id, reorderAt(-1), createOnly), http.StatusBadRequest, "quantity_invalid", "a negative reorder point")
	expectRefusal(t, putPlacement(c, varNut, s.osl.Id, gen.InventoryPlacementInput{Maximum: qty(0)}, createOnly), http.StatusBadRequest, "quantity_invalid", "a maximum of 0")
	expectRefusal(t, putPlacement(c, varNut, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &s.bgoShelf.Id}, createOnly),
		http.StatusBadRequest, "placement_location_invalid", "another site's location")
	archived := createLocation(t, c, s.osl.RootLocationId, gen.InventoryLocationKindShelf, "9")
	mustStatus(t, putLocation(c, archived, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive OSL-9")
	expectRefusal(t, putPlacement(c, varNut, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &archived.Id}, createOnly),
		http.StatusBadRequest, "placement_location_invalid", "an archived location")
	expectRefusal(t, putPlacement(c, 9999, s.osl.Id, reorderAt(1), createOnly), http.StatusBadRequest, "variant_not_found", "an unknown variant")
	expectRefusal(t, putPlacement(c, varService, s.osl.Id, reorderAt(1), createOnly), http.StatusBadRequest, "variant_not_stockable", "a service")
	mustStatus(t, putPlacement(c, varNut, 99999, reorderAt(1), createOnly), http.StatusNotFound, "an unknown site")
	empty := createSite(t, c, "TRD")
	mustStatus(t, c.Do(http.MethodPut, sitePath(empty.Id), gen.InventorySiteUpdate{Code: "TRD", Name: empty.Name, Kind: empty.Kind, Status: gen.Archived}, ifMatch(empty.Revision)),
		http.StatusOK, "archive TRD")
	expectRefusal(t, putPlacement(c, varNut, empty.Id, reorderAt(1), createOnly), http.StatusBadRequest, "site_archived", "an archived site")

	// Without stock-adjust.
	viewer, _ := signIn(t, h, stockViewer...)
	mustStatus(t, putPlacement(viewer, varNut, s.osl.Id, reorderAt(1), createOnly), http.StatusForbidden, "without stock-adjust")

	// DELETE.
	mustStatus(t, c.Do(http.MethodDelete, placementPath(varBolt, s.osl.Id), nil, modtest.Header("If-Match", "x")), http.StatusBadRequest, "a bad If-Match")
	expectRefusal(t, c.Do(http.MethodDelete, placementPath(varBolt, s.osl.Id), nil, ifMatch(1)), http.StatusConflict, "revision_conflict", "a stale delete")
	mustStatus(t, c.Do(http.MethodDelete, placementPath(varBolt, s.osl.Id), nil, ifMatch(2)), http.StatusNoContent, "delete")
	mustStatus(t, c.Do(http.MethodDelete, placementPath(varBolt, s.osl.Id), nil, ifMatch(2)), http.StatusNotFound, "delete again")

	kinds := eventKinds(t, h)
	if n := countOf(kinds, "placement_set"); n != 2 {
		t.Errorf("placement_set events = %d, want 2 (create, change): %v", n, kinds)
	}
	if n := countOf(kinds, "placement_cleared"); n != 1 {
		t.Errorf("placement_cleared events = %d, want 1 (the delete): %v", n, kinds)
	}
}

func countOf(values []string, want string) int {
	n := 0
	for _, v := range values {
		if v == want {
			n++
		}
	}
	return n
}

// TestPlacement_CreateRace is I61's If-None-Match race: creates at once, one
// wins and every other is revision_conflict, never a 500 or a second row.
func TestPlacement_CreateRace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	const racers = 6
	statuses := make([]int, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			statuses[i] = putPlacement(c, varNut, s.osl.Id, reorderAt(float64(i+1)), createOnly).Status
		})
	}
	wg.Wait()
	slices.Sort(statuses)
	want := []int{http.StatusCreated}
	for range racers - 1 {
		want = append(want, http.StatusConflict)
	}
	if !slices.Equal(statuses, want) {
		t.Errorf("statuses = %v, want one 201 and the rest 409", statuses)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements`); n != 1 {
		t.Errorf("%d placements, want 1", n)
	}
}

// TestPlacement_Defaults is I55's defaults on the reads: the variant's stock
// puts the primary location's row first, marked, with the placement and
// low on the site's row — a site with only a placement listed too; I53's
// batch marks the primary and carries the placement; the variant search
// with siteId answers primaryLocationId.
func TestPlacement_Defaults(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	receive(t, c, s.osl.Id, s.shelf2.Id, receiptLine(varBolt, 3))
	setPlacement(t, c, varBolt, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &s.shelf2.Id, ReorderPoint: qty(30)})
	setPlacement(t, c, varBolt, s.bgo.Id, reorderAt(2))
	trd := createSite(t, c, "TRD") // a placement at an archived site is not listed
	setPlacement(t, c, varBolt, trd.Id, reorderAt(2))
	mustStatus(t, c.Do(http.MethodPut, sitePath(trd.Id), gen.InventorySiteUpdate{Code: "TRD", Name: trd.Name, Kind: trd.Kind, Status: gen.Archived}, ifMatch(trd.Revision)),
		http.StatusOK, "archive TRD")

	res := c.Do(http.MethodGet, variantsPath+"/"+strconv.Itoa(int(varBolt))+"/stock", nil)
	mustStatus(t, res, http.StatusOK, "GET variant stock")
	var vs gen.InventoryVariantStock
	res.JSON(&vs)
	if len(vs.Sites) != 2 {
		t.Fatalf("sites = %+v, want OSL and BGO (an active site with a placement alone), not the archived TRD", vs.Sites)
	}
	bgo, osl := vs.Sites[0], vs.Sites[1]
	if bgo.Placement == nil || !bgo.Placement.Low || len(bgo.Rows) != 0 {
		t.Errorf("BGO = %+v, want no rows and a low placement (0 ≤ 2)", bgo)
	}
	if len(osl.Rows) != 2 || osl.Rows[0].Location.Id != s.shelf2.Id || osl.Rows[0].Primary == nil || !*osl.Rows[0].Primary ||
		osl.Rows[1].Primary == nil || *osl.Rows[1].Primary {
		t.Errorf("OSL rows = %+v, want OSL-2 first and marked primary", osl.Rows)
	}
	if osl.Placement == nil || !osl.Placement.Low || osl.Placement.Revision != 1 {
		t.Errorf("OSL placement = %+v, want low (23 ≤ 30)", osl.Placement)
	}

	res = c.Do(http.MethodGet, "/api/v1/inventory/products/10/stock", nil)
	mustStatus(t, res, http.StatusOK, "GET product stock")
	var ps gen.InventoryProductStock
	res.JSON(&ps)
	if len(ps.Variants) == 0 || ps.Variants[0].Variant.Id != varBolt || len(ps.Variants[0].Sites) != 2 || ps.Variants[0].Sites[0].Placement == nil {
		t.Errorf("product stock = %+v, want the bolt at BGO (a placement alone) and OSL", ps.Variants)
	}

	res = c.Do(http.MethodGet, fmt.Sprintf("%s/stock/sites?variantIds=%d&siteIds=%d", "/api/v1/inventory", varBolt, s.osl.Id), nil)
	mustStatus(t, res, http.StatusOK, "GET /stock/sites")
	var at gen.InventoryStockAtSites
	res.JSON(&at)
	if len(at.Stock) != 1 || at.Stock[0].Placement == nil || len(at.Stock[0].Locations) != 2 {
		t.Fatalf("stock at sites = %+v", at.Stock)
	}
	for _, l := range at.Stock[0].Locations {
		if l.Primary != (l.Location.Id == s.shelf2.Id) {
			t.Errorf("location %s primary = %v", l.Location.FullCode, l.Primary)
		}
	}

	var list gen.InventoryVariantList
	res = c.Do(http.MethodGet, variantsPath+"?q=M8&siteId="+strconv.FormatInt(s.osl.Id, 10), nil)
	mustStatus(t, res, http.StatusOK, "search with siteId")
	res.JSON(&list)
	for _, v := range list.Variants {
		want := v.Id == varBolt
		if (v.PrimaryLocationId != nil) != want || (want && *v.PrimaryLocationId != s.shelf2.Id) {
			t.Errorf("variant %s primaryLocationId = %v", v.Sku, v.PrimaryLocationId)
		}
	}
	res = c.Do(http.MethodGet, variantsPath+"?q=M8", nil)
	list = gen.InventoryVariantList{}
	res.JSON(&list)
	for _, v := range list.Variants {
		if v.PrimaryLocationId != nil {
			t.Errorf("a search without siteId answered primaryLocationId for %s", v.Sku)
		}
	}
}

// TestPlacement_LocationArchivedOrDeleted is I55 and §9 edge case 11: a
// primary location re-parented within the site is kept; archiving it clears
// it — a placement with nothing else set is removed, another keeps its
// reorder point — and deleting one does the same before its row goes, each
// with a placement_cleared event; archiving the site clears every primary.
func TestPlacement_LocationArchivedOrDeleted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	aisle := createLocation(t, c, s.osl.RootLocationId, gen.InventoryLocationKindAisle, "A")
	bin := createLocation(t, c, aisle.Id, gen.InventoryLocationKindBin, "B")
	setPlacement(t, c, varBolt, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &bin.Id})
	setPlacement(t, c, varNut, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &bin.Id, ReorderPoint: qty(4)})

	// §9 11: re-parented within the site, kept.
	mustStatus(t, putLocation(c, getLocation(t, c, bin.Id).Location,
		func(u *gen.InventoryLocationUpdate) { u.ParentId = &s.osl.RootLocationId }), http.StatusOK, "re-parent the bin")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE primary_location_id = $1`, bin.Id); n != 2 {
		t.Fatalf("%d placements at the re-parented bin, want 2", n)
	}

	// Archiving it clears it.
	mustStatus(t, putLocation(c, getLocation(t, c, bin.Id).Location,
		func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived }), http.StatusOK, "archive the bin")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1`, varBolt); n != 0 {
		t.Errorf("the primary-only placement is still there")
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1 AND primary_location_id IS NULL AND reorder_point = 4 AND revision = 2`, varNut); n != 1 {
		t.Errorf("the nut's placement did not keep its reorder point with its primary cleared")
	}
	if n := countOf(eventKinds(t, h), "placement_cleared"); n != 2 {
		t.Errorf("placement_cleared events = %d, want 2", n)
	}

	// Deleting a location clears it before its row goes.
	fresh := createLocation(t, c, aisle.Id, gen.InventoryLocationKindBin, "C")
	setPlacement(t, c, varWasher, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &fresh.Id})
	mustStatus(t, c.Do(http.MethodDelete, locationPath(aisle.Id), nil), http.StatusNoContent, "delete the aisle")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1`, varWasher); n != 0 {
		t.Errorf("the placement at a deleted location is still there")
	}
	if n := countOf(eventKinds(t, h), "placement_cleared"); n != 3 {
		t.Errorf("placement_cleared events = %d, want 3", n)
	}

	// Archiving the site clears every primary in it, keeping reorder points.
	b := s.bgo
	setPlacement(t, c, varBolt, b.Id, gen.InventoryPlacementInput{PrimaryLocationId: &s.bgoShelf.Id, ReorderPoint: qty(1)})
	mustStatus(t, c.Do(http.MethodPut, sitePath(b.Id), gen.InventorySiteUpdate{Code: b.Code, Name: b.Name, Kind: b.Kind, Status: gen.Archived}, ifMatch(b.Revision)),
		http.StatusOK, "archive BGO")
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE site_id = $1 AND primary_location_id IS NULL AND reorder_point = 1`, b.Id); n != 1 {
		t.Errorf("archiving the site did not clear the primary")
	}
}

// lowStock reads a page of the list.
func lowStock(t *testing.T, c *modtest.Client, query string) gen.InventoryLowStockPage {
	t.Helper()
	res := c.Do(http.MethodGet, "/api/v1/inventory/low-stock"+query, nil)
	mustStatus(t, res, http.StatusOK, "GET /low-stock"+query)
	var p gen.InventoryLowStockPage
	res.JSON(&p)
	return p
}

// lowSKUs is a page's rows as site:sku.
func lowSKUs(p gen.InventoryLowStockPage) []string {
	out := make([]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		out = append(out, r.Site.Code+":"+r.Variant.Sku)
	}
	return out
}

// TestLowStock_List is I56–I59 and §9 edge cases 12–15 on the list: the
// order, the rule at =, incoming from a sent transfer and none from a
// cancelled one, the rounding past the maximum, a reorder point of 0, a
// draft open to the site, elsewhere, an archived site skipped, a variant
// gone from the catalog skipped and its placement left, q, siteId, and the
// snapshot refreshed after a rename.
func TestLowStock_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c) // OSL: 20 bolts on 1, 10 nuts on 2
	h.catalog.setPackagings(varNut, contracts.PackagingEntry{ID: 71, Name: "Eske 15", Kind: "case", Quantity: "15.000", SortOrder: 1})

	setPlacement(t, c, varBolt, s.osl.Id, reorderAt(20))    // low at = (20 ≤ 20)
	setPlacement(t, c, varNut, s.osl.Id, reorderAt(12, 30)) // 6 after the send ≤ 12: 24 needed, 2 cases of 15
	setPlacement(t, c, varDraft, s.osl.Id, reorderAt(0))    // §9 13: nothing there, 0 ≤ 0, the target 1
	setPlacement(t, c, varWasher, s.osl.Id, reorderAt(0))   // §9 13: 0.5 there, not low
	setPlacement(t, c, varBolt, s.bgo.Id, reorderAt(3))     // nothing at BGO
	setPlacement(t, c, varNut, s.bgo.Id, reorderAt(0))      // 4 incoming lift it off
	setPlacement(t, c, varOld, s.bgo.Id, reorderAt(1))      // removed from the catalog
	setPlacement(t, c, varWasher, s.bgo.Id, reorderAt(100)) // the washers' transfer cancelled
	receive(t, c, s.osl.Id, s.shelf2.Id, receiptLine(varWasher, 0.5))

	// A sent transfer of 4 nuts to BGO is incoming there; a cancelled one of
	// washers is not (§10).
	mustSend(t, c, createTransfer(t, c, s, line(varNut, 4)), s.shelf2.Id)
	cancelled := mustSend(t, c, createTransfer(t, c, s, line(varWasher, 0.5)), s.shelf2.Id)
	mustStatus(t, cancelTransfer(c, cancelled), http.StatusOK, "cancel the washers")
	// §9 14: a draft does not take the row off, and names itself.
	draft := createTransfer(t, c, s, line(varBolt, 2))
	h.catalog.remove(varOld) // §9 15

	page := lowStock(t, c, "")
	want := []string{"BGO:BOLT-M8", "BGO:WASH-8", "OSL:BOLT-M8", "OSL:NEW-1", "OSL:NUT-M8"}
	if got := lowSKUs(page); !slices.Equal(got, want) || page.Next != nil {
		t.Fatalf("low stock = %v (next %v), want %v", got, page.Next, want)
	}
	byKey := map[string]gen.InventoryLowStockRow{}
	for i, r := range page.Rows {
		byKey[want[i]] = r
	}
	if r := byKey["OSL:BOLT-M8"]; r.Needed != 1 || r.Suggested != 1 || r.Packaging != nil || r.Available != 20 || r.Maximum != nil {
		t.Errorf("OSL bolt = %+v, want low at =, needed 1 to one above", r)
	}
	if r := byKey["OSL:NUT-M8"]; r.OnHand != 6 || r.Needed != 24 || r.Suggested != 30 || r.Packaging == nil || r.Packaging.Count != 2 ||
		r.Packaging.Kind != "case" || r.Packaging.Quantity != 15 {
		t.Errorf("OSL nut = %+v, want 24 needed rounded up to 2 cases of 15, past the maximum (§9 12)", r)
	}
	if r := byKey["OSL:NEW-1"]; r.Needed != 1 || r.ReorderPoint != 0 {
		t.Errorf("OSL new = %+v, want reorder point 0 and needed 1 (§9 13)", r)
	}
	if r := byKey["BGO:BOLT-M8"]; !slices.Equal(r.OpenDrafts, []int64{draft.Number}) || r.Needed != 4 ||
		len(r.Elsewhere) != 1 || r.Elsewhere[0].SiteId != s.osl.Id || r.Elsewhere[0].Available != 20 {
		t.Errorf("BGO bolt = %+v, want draft %d open (§9 14) and OSL's 20 elsewhere", r, draft.Number)
	}
	if r := byKey["BGO:WASH-8"]; r.Incoming != 0 || r.Needed != 101 {
		t.Errorf("BGO washer = %+v, want nothing incoming (the transfer was cancelled)", r)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.placements WHERE variant_id = $1`, varOld); n != 1 {
		t.Errorf("the deleted variant's placement was not left (§9 15)")
	}

	// The filters.
	if got := lowSKUs(lowStock(t, c, "?siteId="+strconv.FormatInt(s.bgo.Id, 10))); !slices.Equal(got, want[:2]) {
		t.Errorf("siteId = %v", got)
	}
	if got := lowSKUs(lowStock(t, c, "?q=nut")); !slices.Equal(got, []string{"OSL:NUT-M8"}) {
		t.Errorf("q=nut = %v", got)
	}
	if got := lowSKUs(lowStock(t, c, "?q=washer")); !slices.Equal(got, []string{"BGO:WASH-8"}) {
		t.Errorf("q=washer (the name) = %v", got)
	}
	mustStatus(t, c.Do(http.MethodGet, "/api/v1/inventory/low-stock?cursor=nope", nil), http.StatusBadRequest, "a bad cursor")

	// A rename in Products is searchable from the read after one shows it.
	renamed := goods(varWasher, 20, "SKIVE-8", "Skive 8", "kg")
	h.catalog.seed(renamed)
	if got := lowSKUs(lowStock(t, c, "?q=skive")); len(got) != 0 {
		t.Errorf("q=skive before a read showed the rename = %v", got)
	}
	lowStock(t, c, "")
	if got := lowSKUs(lowStock(t, c, "?q=skive")); !slices.Equal(got, []string{"BGO:SKIVE-8"}) {
		t.Errorf("q=skive after = %v", got)
	}

	// An archived site is skipped.
	trd := createSite(t, c, "TRD")
	setPlacement(t, c, varBolt, trd.Id, reorderAt(5))
	if got := lowSKUs(lowStock(t, c, "?q=bolt")); !slices.Contains(got, "TRD:BOLT-M8") {
		t.Errorf("TRD not listed while active: %v", got)
	}
	mustStatus(t, c.Do(http.MethodPut, sitePath(trd.Id), gen.InventorySiteUpdate{Code: "TRD", Name: trd.Name, Kind: trd.Kind, Status: gen.Archived}, ifMatch(trd.Revision)),
		http.StatusOK, "archive TRD")
	if got := lowSKUs(lowStock(t, c, "?q=bolt")); slices.Contains(got, "TRD:BOLT-M8") {
		t.Errorf("an archived site listed: %v", got)
	}
}

// TestPlacement_StockLocationsPrimary is I55 through contracts.Stock: the
// locations holding a variant at the site keep their order by full code,
// the primary one marked, which Orders' pick list puts first.
func TestPlacement_StockLocationsPrimary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c)
	receive(t, c, s.osl.Id, s.shelf2.Id, receiptLine(varBolt, 1))
	setPlacement(t, c, varBolt, s.osl.Id, gen.InventoryPlacementInput{PrimaryLocationId: &s.shelf2.Id})
	locs, err := stockOf(h).Locations(t.Context(), []int32{varBolt, varNut}, s.osl.Id)
	if err != nil {
		t.Fatalf("Locations: %v", err)
	}
	var got []string
	for _, l := range locs {
		got = append(got, fmt.Sprintf("%d@%s:%v", l.VariantID, l.FullCode, l.Primary))
	}
	want := []string{
		fmt.Sprintf("%d@OSL-1:false", varBolt), fmt.Sprintf("%d@OSL-2:true", varBolt), fmt.Sprintf("%d@OSL-2:false", varNut),
	}
	if !slices.Equal(got, want) {
		t.Errorf("Locations = %v, want %v", got, want)
	}
}
