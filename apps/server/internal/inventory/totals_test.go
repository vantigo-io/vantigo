package inventory_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

const stockPath = "/api/v1/inventory/stock"

// TestStockTotals is I33's column: each variant's on hand across every
// location and site, summed exactly; a variant with none absent; at most 100
// ids.
func TestStockTotals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	bgoRoot := s.elsewhre.RootLocationId
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 0.1), receiptLine(varNut, 2))
	receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varBolt, 0.2))
	receive(t, c, s.elsewhre.Id, bgoRoot, receiptLine(varBolt, 1))

	viewer, _ := signIn(t, h, stockViewer...)
	res := viewer.Do(http.MethodGet, fmt.Sprintf("%s?variantIds=%d,%d,%d", stockPath, varBolt, varNut, varWasher), nil)
	mustStatus(t, res, http.StatusOK, "GET /stock")
	var out gen.InventoryStockTotals
	res.JSON(&out)
	if len(out.Totals) != 2 || out.Totals[0].VariantId != varBolt || out.Totals[0].OnHand != 1.3 || out.Totals[1].OnHand != 2 {
		t.Errorf("totals = %+v, want bolts 1.3 and nuts 2", out.Totals)
	}
	ids := ""
	for i := range 101 {
		if i > 0 {
			ids += ","
		}
		ids += fmt.Sprint(i + 1)
	}
	mustStatus(t, viewer.Do(http.MethodGet, stockPath+"?variantIds="+url.QueryEscape(ids), nil), http.StatusBadRequest, "101 ids")
	structure, _ := signIn(t, h, locationsViewer...)
	mustStatus(t, structure.Do(http.MethodGet, stockPath+"?variantIds=1", nil), http.StatusForbidden, "totals without stock-view")
}

// TestSites_Figures is the site list's fold-in: every caller sees each
// site's active locations; one holding stock-view its stock lines and last
// receipt too.
func TestSites_Figures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	s := newLedgerSite(t, h, c)
	receive(t, c, s.site.Id, s.shelf1.Id, receiptLine(varBolt, 1), receiptLine(varNut, 1))
	last := receive(t, c, s.site.Id, s.shelf2.Id, receiptLine(varBolt, 1))

	sitesOf := func(as []string) map[string]gen.InventorySite {
		t.Helper()
		who, _ := signIn(t, h, as...)
		res := who.Do(http.MethodGet, sitesPath, nil)
		mustStatus(t, res, http.StatusOK, "GET /sites")
		var list gen.InventorySiteList
		res.JSON(&list)
		out := map[string]gen.InventorySite{}
		for _, site := range list.Sites {
			out[site.Code] = site
		}
		return out
	}
	stock := sitesOf(stockViewer)
	osl, bgo := stock["OSL"], stock["BGO"]
	if osl.LocationCount != 2 || osl.StockLines == nil || *osl.StockLines != 3 || osl.LastReceiptAt == nil || !osl.LastReceiptAt.Equal(last.PostedAt) {
		t.Errorf("OSL = %+v, want 2 locations, 3 stock lines and the last receipt", osl)
	}
	if bgo.LocationCount != 0 || bgo.StockLines == nil || *bgo.StockLines != 0 || bgo.LastReceiptAt != nil {
		t.Errorf("BGO = %+v, want nothing", bgo)
	}
	structure := sitesOf(locationsViewer)["OSL"]
	if structure.LocationCount != 2 || structure.StockLines != nil || structure.LastReceiptAt != nil {
		t.Errorf("OSL to the structure alone = %+v, want the location count only", structure)
	}
	res := c.Do(http.MethodGet, sitePath(s.site.Id), nil)
	mustStatus(t, res, http.StatusOK, "GET /sites/{id}")
	var one gen.InventorySite
	res.JSON(&one)
	if one.LocationCount != 2 || one.StockLines == nil || *one.StockLines != 3 {
		t.Errorf("GET /sites/{id} = %+v", one)
	}
}

// TestSiteLocations_Search is the location list's q and truncated: the full
// code or the name containing the text, ignoring case.
func TestSiteLocations_Search(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, locationsManager...)
	site := createSite(t, c, "OSL")
	named := createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindArea, "A")
	name := "Kjølerom"
	mustStatus(t, putLocation(c, named, func(u *gen.InventoryLocationUpdate) { u.Name = &name }), http.StatusOK, "name A")
	createLocation(t, c, site.RootLocationId, gen.InventoryLocationKindArea, "B")
	createLocation(t, c, named.Id, gen.InventoryLocationKindShelf, "12")

	search := func(q string) gen.InventoryLocationList {
		t.Helper()
		res := c.Do(http.MethodGet, sitePath(site.Id)+"/locations?q="+url.QueryEscape(q), nil)
		mustStatus(t, res, http.StatusOK, "search "+q)
		var out gen.InventoryLocationList
		res.JSON(&out)
		return out
	}
	codes := func(l gen.InventoryLocationList) []string {
		out := []string{}
		for _, x := range l.Locations {
			out = append(out, x.FullCode)
		}
		return out
	}
	if got := codes(search("osl-a")); fmt.Sprint(got) != "[OSL-A OSL-A-12]" {
		t.Errorf("osl-a = %v", got)
	}
	if got := codes(search("KJØLE")); fmt.Sprint(got) != "[OSL-A]" {
		t.Errorf("KJØLE = %v", got)
	}
	if l := search("12"); fmt.Sprint(codes(l)) != "[OSL-A-12]" || l.Truncated {
		t.Errorf("12 = %+v", l)
	}
}
