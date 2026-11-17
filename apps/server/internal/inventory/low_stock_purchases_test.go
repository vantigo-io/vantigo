package inventory_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestLowStock_OnOrder is purchasing design P4 and A3: Purchasing's open
// purchases count as incoming beside the transfers in transit, read before
// the low-stock statement and joined in by it, so the list, its CSV, the
// dashboard's count and the stock card agree, each showing the two parts.
func TestLowStock_OnOrder(t *testing.T) {
	t.Parallel()
	supply := modtest.NewFakePurchaseSupply()
	h := newHarness(t, modtest.WithPurchaseSupply(supply))
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c) // OSL: 20 bolts on 1, 10 nuts on 2
	setPlacement(t, c, varNut, s.osl.Id, reorderAt(12))
	setPlacement(t, c, varBolt, s.bgo.Id, reorderAt(3))
	supply.Set(
		contracts.OpenPurchase{VariantID: varNut, SiteID: s.osl.Id, Quantity: "5"},  // 15 > 12: no longer low
		contracts.OpenPurchase{VariantID: varBolt, SiteID: s.bgo.Id, Quantity: "2"}, // 2 ≤ 3: still low
		contracts.OpenPurchase{VariantID: varWasher, SiteID: s.bgo.Id, Quantity: "1"},
	)
	page := lowStock(t, c, "")
	if got := lowSKUs(page); !slices.Equal(got, []string{"BGO:BOLT-M8"}) {
		t.Fatalf("low stock = %v, want the bolt at BGO alone", got)
	}
	if r := page.Rows[0]; r.OnOrder != 2 || r.InTransit != 0 || r.Incoming != 2 || r.Needed != 2 {
		t.Errorf("the bolt at BGO = %+v, want 2 on order, 2 needed to one above the point", r)
	}

	res := c.Do(http.MethodGet, "/api/v1/inventory/low-stock.csv", nil)
	mustStatus(t, res, http.StatusOK, "GET /low-stock.csv")
	lines := strings.Split(strings.TrimPrefix(string(res.Body), "\ufeff"), "\r\n")
	if len(lines) != 3 || lines[1] != "BGO;BOLT-M8;Bolt M8;7040000000017;0;2;0;2;3;;2;2;;" {
		t.Errorf("csv = %q", lines)
	}

	var items []gen.InventoryStatsAttentionItem
	res = c.Do(http.MethodGet, attentionPath, nil)
	mustStatus(t, res, http.StatusOK, "GET /stats/attention")
	res.JSON(&items)
	if len(items) != 1 || items[0].EntityId != strconv.FormatInt(s.bgo.Id, 10) || items[0].Count == nil || *items[0].Count != 1 {
		t.Errorf("attention = %+v, want BGO's one", items)
	}

	// The stock card: a site with only an open purchase has a row.
	res = c.Do(http.MethodGet, "/api/v1/inventory/variants/"+strconv.Itoa(int(varBolt))+"/stock", nil)
	mustStatus(t, res, http.StatusOK, "the bolt's stock")
	var stock gen.InventoryVariantStock
	res.JSON(&stock)
	var bgo *gen.InventorySiteStock
	for i := range stock.Sites {
		if stock.Sites[i].Site.Id == s.bgo.Id {
			bgo = &stock.Sites[i]
		}
	}
	if bgo == nil || bgo.OnOrder != 2 || bgo.InTransit != 0 || bgo.Incoming != 2 || bgo.Placement == nil || !bgo.Placement.Low {
		t.Errorf("the bolt at BGO on the stock card = %+v", bgo)
	}
}
