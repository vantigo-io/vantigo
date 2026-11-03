package inventory_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestContents_Subtree is I29's contents: what lies on a location and below
// it, by full code then SKU, or on the location alone; a variant the catalog
// no longer knows named by its snapshot.
func TestContents_Subtree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	a := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	a1 := createLocation(t, c, a.Id, gen.InventoryLocationKindShelf, "1")
	receive(t, c, s.site.Id, a1.Id, receiptLine(varNut, 2), receiptLine(varBolt, 1))
	receive(t, c, s.site.Id, a.Id, receiptLine(varWasher, 1.25))
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 9))

	viewer, _ := signIn(t, h, stockViewer...)
	var contents gen.InventoryLocationContents
	res := viewer.Do(http.MethodGet, locationPath(a.Id)+"/contents", nil)
	mustStatus(t, res, http.StatusOK, "GET contents")
	res.JSON(&contents)
	got := []string{}
	for _, r := range contents.Rows {
		got = append(got, fmt.Sprintf("%s %s %v", r.Location.FullCode, r.Variant.Sku, r.OnHand))
	}
	if want := []string{"OSL-A WASH-8 1.25", "OSL-A-1 BOLT-M8 1", "OSL-A-1 NUT-M8 2"}; !slices.Equal(got, want) {
		t.Errorf("contents = %v, want %v", got, want)
	}
	res = viewer.Do(http.MethodGet, locationPath(a.Id)+"/contents?subtree=false", nil)
	mustStatus(t, res, http.StatusOK, "GET contents of A alone")
	res.JSON(&contents)
	if len(contents.Rows) != 1 || contents.Rows[0].Variant.Sku != "WASH-8" {
		t.Errorf("A alone = %+v", contents.Rows)
	}
	mustStatus(t, viewer.Do(http.MethodGet, locationPath(999999)+"/contents", nil), http.StatusNotFound, "contents of an unknown location")

	h.catalog.remove(varWasher)
	res = viewer.Do(http.MethodGet, locationPath(a.Id)+"/contents?subtree=false", nil)
	mustStatus(t, res, http.StatusOK, "GET contents with a variant gone")
	contents = gen.InventoryLocationContents{}
	res.JSON(&contents)
	if v := contents.Rows[0].Variant; v.Deleted == nil || !*v.Deleted || v.Sku != "WASH-8" || v.Unit != "kg" || v.ProductId != nil {
		t.Errorf("variant = %+v, want the snapshot, deleted", v)
	}

	// Locations alone shows no quantities.
	structure, _ := signIn(t, h, locationsViewer...)
	mustStatus(t, structure.Do(http.MethodGet, locationPath(a.Id)+"/contents", nil), http.StatusForbidden, "contents without stock-view")
	assertLedgerConsistent(t, h)
}

// TestProductStock is I27's Stock card: every goods variant of the product
// in the catalog's order, by site (by code) and location (by full code),
// with exact totals; a service product answers none, an unknown product 404.
func TestProductStock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varBolt, 0.1))
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 0.2))
	receive(t, c, s.elsewhre.Id, s.elsewhre.RootLocationId, receiptLine(varBolt, 5))

	var stock gen.InventoryProductStock
	res := c.Do(http.MethodGet, "/api/v1/inventory/products/10/stock", nil)
	mustStatus(t, res, http.StatusOK, "GET product stock")
	res.JSON(&stock)
	if stock.ProductId != 10 || len(stock.Variants) != 2 || stock.Variants[0].Variant.Id != varBolt || stock.Variants[1].Variant.Id != varNut {
		t.Fatalf("stock = %+v, want bolt then nut", stock)
	}
	bolt := stock.Variants[0]
	if bolt.TotalOnHand != 5.3 || len(bolt.Sites) != 2 || bolt.Sites[0].Site.Code != "BGO" || bolt.Sites[1].OnHand != 0.3 {
		t.Errorf("bolt = %+v, want 5.3 over BGO then OSL", bolt)
	}
	if rows := bolt.Sites[1].Rows; len(rows) != 2 || rows[0].Location.FullCode != "OSL-1" || rows[1].Location.FullCode != "OSL-2" {
		t.Errorf("OSL rows = %+v, want by full code", rows)
	}
	if nut := stock.Variants[1]; nut.TotalOnHand != 0 || len(nut.Sites) != 0 {
		t.Errorf("nut = %+v, want no stock", nut)
	}
	res = c.Do(http.MethodGet, "/api/v1/inventory/products/30/stock", nil)
	mustStatus(t, res, http.StatusOK, "GET a service's stock")
	res.JSON(&stock)
	if len(stock.Variants) != 0 {
		t.Errorf("service stock = %+v, want no variants", stock)
	}
	mustStatus(t, c.Do(http.MethodGet, "/api/v1/inventory/products/999/stock", nil), http.StatusNotFound, "GET an unknown product's stock")
	assertLedgerConsistent(t, h)
}

// TestVariantStock is one variant's stock, and the deleted fallback: a
// variant the catalog no longer knows answers its snapshot, one neither
// knows is 404.
func TestVariantStock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varNut, 4))
	path := func(id int32) string { return "/api/v1/inventory/variants/" + strconv.Itoa(int(id)) + "/stock" }

	var stock gen.InventoryVariantStock
	res := c.Do(http.MethodGet, path(varNut), nil)
	mustStatus(t, res, http.StatusOK, "GET variant stock")
	res.JSON(&stock)
	if stock.TotalOnHand != 4 || len(stock.Sites) != 1 || stock.Variant.ProductId == nil || *stock.Variant.ProductId != 10 {
		t.Errorf("stock = %+v", stock)
	}
	res = c.Do(http.MethodGet, path(varWasher), nil)
	mustStatus(t, res, http.StatusOK, "GET the stock of a variant with none")
	res.JSON(&stock)
	if stock.TotalOnHand != 0 || len(stock.Sites) != 0 {
		t.Errorf("washer = %+v, want none", stock)
	}
	h.catalog.remove(varNut)
	res = c.Do(http.MethodGet, path(varNut), nil)
	mustStatus(t, res, http.StatusOK, "GET a deleted variant's stock")
	stock = gen.InventoryVariantStock{}
	res.JSON(&stock)
	if stock.Variant.Deleted == nil || !*stock.Variant.Deleted || stock.Variant.Sku != "NUT-M8" {
		t.Errorf("variant = %+v, want the snapshot", stock.Variant)
	}
	mustStatus(t, c.Do(http.MethodGet, path(9999), nil), http.StatusNotFound, "GET an unknown variant's stock")
}

// TestMovements_ListAndGet is I30: newest first, the filters, the cursor,
// and one movement; and a movement naming a variant the catalog forgot.
func TestMovements_ListAndGet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	a := createLocation(t, c, s.site.RootLocationId, gen.InventoryLocationKindAisle, "A")
	a1 := createLocation(t, c, a.Id, gen.InventoryLocationKindShelf, "1")
	r1 := receive(t, c, s.site.Id, a1.Id, receiptLine(varBolt, 3))
	r2 := receive(t, c, s.elsewhre.Id, s.elsewhre.RootLocationId, receiptLine(varNut, 3))
	res := move(c, s.site.Id, gen.InventoryMoveLine{VariantId: varBolt, FromLocationId: a1.Id, ToLocationId: s.shelf1.Id, Quantity: 1})
	mustStatus(t, res, http.StatusCreated, "move")
	var mv gen.InventoryMovement
	res.JSON(&mv)
	res = adjust(c, s.site.Id, change(varWasher, s.shelf2.Id, 1))
	mustStatus(t, res, http.StatusCreated, "adjust")
	var adj gen.InventoryMovement
	res.JSON(&adj)

	list := func(query string) []int64 {
		t.Helper()
		var page gen.InventoryMovementPage
		res := c.Do(http.MethodGet, movementsPath+query, nil)
		mustStatus(t, res, http.StatusOK, "GET /movements"+query)
		res.JSON(&page)
		ids := []int64{}
		for _, m := range page.Movements {
			ids = append(ids, m.Id)
		}
		return ids
	}
	if got := list(""); !slices.Equal(got, []int64{adj.Id, mv.Id, r2.Id, r1.Id}) {
		t.Errorf("all = %v, want newest first", got)
	}
	if got := list("?kind=receipt"); !slices.Equal(got, []int64{r2.Id, r1.Id}) {
		t.Errorf("receipts = %v", got)
	}
	if got := list(fmt.Sprintf("?siteId=%d&variantId=%d", s.site.Id, varBolt)); !slices.Equal(got, []int64{mv.Id, r1.Id}) {
		t.Errorf("OSL's bolts = %v", got)
	}
	if got := list(fmt.Sprintf("?locationId=%d", a.Id)); !slices.Equal(got, []int64{mv.Id, r1.Id}) {
		t.Errorf("A's subtree = %v", got)
	}
	today := h.Now().UTC().Format("2006-01-02")
	if got := list("?from=" + today + "&to=" + today); len(got) != 4 {
		t.Errorf("today's = %v, want all four", got)
	}
	if got := list("?to=2000-01-01"); len(got) != 0 {
		t.Errorf("before 2000 = %v, want none", got)
	}

	// Paged by two.
	var page gen.InventoryMovementPage
	res = c.Do(http.MethodGet, movementsPath+"?limit=2", nil)
	mustStatus(t, res, http.StatusOK, "page 1")
	res.JSON(&page)
	if len(page.Movements) != 2 || page.NextCursor == nil {
		t.Fatalf("page 1 = %+v", page)
	}
	res = c.Do(http.MethodGet, movementsPath+"?limit=2&cursor="+url.QueryEscape(*page.NextCursor), nil)
	mustStatus(t, res, http.StatusOK, "page 2")
	page = gen.InventoryMovementPage{}
	res.JSON(&page)
	if len(page.Movements) != 2 || page.Movements[0].Id != r2.Id || page.NextCursor != nil {
		t.Errorf("page 2 = %+v, want the two receipts and no next", page)
	}
	if m := page.Movements[1]; m.LineCount != 1 || m.Kind != gen.Receipt || m.Site.Code != "OSL" || m.PostedBy.DisplayName == "" {
		t.Errorf("summary = %+v", m)
	}
	for _, bad := range []string{"?cursor=nonsense", "?limit=501", "?from=2026-02-02&to=2026-02-01", "?kind=count"} {
		mustStatus(t, c.Do(http.MethodGet, movementsPath+bad, nil), http.StatusBadRequest, "GET /movements"+bad)
	}

	res = c.Do(http.MethodGet, movementPath(mv.Id), nil)
	mustStatus(t, res, http.StatusOK, "GET /movements/{id}")
	var got gen.InventoryMovement
	res.JSON(&got)
	if got.Kind != gen.Move || len(got.Lines) != 1 || got.Lines[0].From.FullCode != "OSL-A-1" || got.Lines[0].To.FullCode != "OSL-1" {
		t.Errorf("move = %+v", got)
	}
	h.catalog.remove(varBolt)
	res = c.Do(http.MethodGet, movementPath(mv.Id), nil)
	mustStatus(t, res, http.StatusOK, "GET a movement of a forgotten variant")
	got = gen.InventoryMovement{}
	res.JSON(&got)
	if v := got.Lines[0].Variant; v.Deleted == nil || !*v.Deleted || v.Sku != "BOLT-M8" {
		t.Errorf("variant = %+v, want the line's snapshot", v)
	}
	mustStatus(t, c.Do(http.MethodGet, movementPath(999999), nil), http.StatusNotFound, "GET /movements/{unknown}")
}

// TestLookup is I28: a LOC token, a full code ignoring case, then a SKU or a
// barcode — each kind by its own key, nothing to a caller with neither.
func TestLookup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	lookup := func(c *modtest.Client, q string) []string {
		t.Helper()
		var out gen.InventoryLookupResult
		res := c.Do(http.MethodGet, lookupPath+"?q="+url.QueryEscape(q), nil)
		mustStatus(t, res, http.StatusOK, "GET /lookup?q="+q)
		res.JSON(&out)
		got := []string{}
		for _, m := range out.Matches {
			if m.Location != nil {
				got = append(got, "location "+m.Location.FullCode)
			}
			if m.Variant != nil {
				got = append(got, "variant "+m.Variant.Sku)
			}
		}
		return got
	}
	token := "loc" + strconv.FormatInt(s.shelf2.Id, 10)
	if got := lookup(c, token); !slices.Equal(got, []string{"location OSL-2"}) {
		t.Errorf("%s = %v", token, got)
	}
	if got := lookup(c, " osl-1 "); !slices.Equal(got, []string{"location OSL-1"}) {
		t.Errorf("osl-1 = %v", got)
	}
	if got := lookup(c, "bolt-m8"); !slices.Equal(got, []string{"variant BOLT-M8"}) {
		t.Errorf("bolt-m8 = %v", got)
	}
	if got := lookup(c, "7040000000017"); !slices.Equal(got, []string{"variant BOLT-M8"}) {
		t.Errorf("the barcode = %v", got)
	}
	if got := lookup(c, "nothing"); len(got) != 0 {
		t.Errorf("nothing = %v", got)
	}
	structure, _ := signIn(t, h, locationsViewer...)
	if got := lookup(structure, "BOLT-M8"); len(got) != 0 {
		t.Errorf("a variant to locations-view alone = %v, want none", got)
	}
	if got := lookup(structure, "OSL-1"); len(got) != 1 {
		t.Errorf("a location to locations-view = %v", got)
	}
	none, _ := signIn(t, h)
	if got := lookup(none, "OSL-1"); len(got) != 0 {
		t.Errorf("a location to no key = %v, want none", got)
	}
	mustStatus(t, c.Do(http.MethodGet, lookupPath+"?q=%20", nil), http.StatusBadRequest, "GET /lookup with a blank q")
}

// TestSearchVariants is I22's picker: goods only, by SKU, name or barcode,
// within the limit.
func TestSearchVariants(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, stockViewer...)
	seedCatalog(h)
	var out gen.InventoryVariantList
	res := c.Do(http.MethodGet, variantsPath+"?q=m8", nil)
	mustStatus(t, res, http.StatusOK, "GET /variants?q=m8")
	res.JSON(&out)
	if len(out.Variants) != 2 || out.Variants[0].Sku != "BOLT-M8" || out.Variants[0].Barcode == nil {
		t.Errorf("variants = %+v", out.Variants)
	}
	res = c.Do(http.MethodGet, variantsPath+"?q=install", nil)
	mustStatus(t, res, http.StatusOK, "GET /variants?q=install")
	res.JSON(&out)
	if len(out.Variants) != 0 {
		t.Errorf("a service = %+v, want none", out.Variants)
	}
	res = c.Do(http.MethodGet, variantsPath+"?q=t&limit=1", nil)
	mustStatus(t, res, http.StatusOK, "GET /variants?limit=1")
	res.JSON(&out)
	if len(out.Variants) != 1 {
		t.Errorf("limit 1 = %+v", out.Variants)
	}
	mustStatus(t, c.Do(http.MethodGet, variantsPath+"?q=t&limit=51", nil), http.StatusBadRequest, "GET /variants?limit=51")
	mustStatus(t, c.Do(http.MethodGet, variantsPath+"?q=%20", nil), http.StatusBadRequest, "GET /variants?q=blank")
}

// TestStats is the dashboard card's figures and its attention items, none
// in delivery A.
func TestStats(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 1), receiptLine(varNut, 1))
	receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varBolt, 1))
	mustStatus(t, adjust(c, s.site.Id, change(varBolt, s.shelf2.Id, -1)), http.StatusCreated, "adjust")

	var stats gen.InventoryStats
	res := c.Do(http.MethodGet, statsPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /stats")
	res.JSON(&stats)
	if stats.Sites != 2 || stats.Locations != 2 || stats.StockLines != 2 || stats.ReceiptsThisWeek != 2 || stats.OpenCounts != 0 {
		t.Errorf("stats = %+v", stats)
	}
	var items []gen.InventoryStatsAttentionItem
	res = c.Do(http.MethodGet, attentionPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /stats/attention")
	res.JSON(&items)
	if items == nil || len(items) != 0 {
		t.Errorf("attention = %v, want an empty array", items)
	}
}

// TestVariantHeld is I23's holder: what a variant holds, summed on the
// caller's transaction, nil when nothing.
func TestVariantHeld(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 30.5))
	receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varBolt, 3.5))
	holder := inventory.Module().VariantReferences(h.Deps())

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	held, err := holder.VariantHeld(ctx, tx, varBolt)
	if err != nil || held == nil || held.Quantity != "34" || held.Locations != 2 {
		t.Errorf("VariantHeld(bolt) = %+v, %v; want 34 at 2 locations", held, err)
	}
	if held, err := holder.VariantHeld(ctx, tx, varNut); err != nil || held != nil {
		t.Errorf("VariantHeld(nut) = %+v, %v; want nil", held, err)
	}
}

// TestPosting_SameKeyConcurrently is I14 under a race: two postings of one
// clientRequestId at once — the second waits at the key, then answers the
// first movement.
func TestPosting_SameKeyConcurrently(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(8))
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	body := gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: s.shelf1.Id, Lines: []gen.InventoryReceiptLine{receiptLine(varBolt, 2)}}
	var wg sync.WaitGroup
	results := make([]gen.InventoryMovement, 4)
	statuses := make([]int, 4)
	for i := range 4 {
		wg.Go(func() {
			res := c.Do(http.MethodPost, receiptsPath, body)
			statuses[i] = res.Status
			if res.Status < 300 {
				res.JSON(&results[i])
			}
		})
	}
	wg.Wait()
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusOK, http.StatusOK, http.StatusCreated}) {
		t.Errorf("statuses = %v, want one 201 and three 200", statuses)
	}
	for _, r := range results[1:] {
		if r.Id != results[0].Id {
			t.Errorf("movements %d and %d, want one", r.Id, results[0].Id)
		}
	}
	if got := onHand(t, h, varBolt, s.shelf1.Id); got != "2.000" {
		t.Errorf("bolts = %q, want 2", got)
	}
	assertLedgerConsistent(t, h)
}
