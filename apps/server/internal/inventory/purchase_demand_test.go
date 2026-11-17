package inventory_test

import (
	"context"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestPurchaseDemand_LowStockBeforePackaging is purchasing design P31 and
// A4: Inventory's purchase demand is the low-stock list's rows — what is on
// order counted, one site or every one — each its need rounded to the
// placement's multiple and never to the variant's packaging, now, computed
// when it was read.
func TestPurchaseDemand_LowStockBeforePackaging(t *testing.T) {
	t.Parallel()
	supply := modtest.NewFakePurchaseSupply()
	h := newHarness(t, modtest.WithPurchaseSupply(supply))
	c, _ := signIn(t, h, placer...)
	s := newTransferSites(t, h, c) // OSL: 20 bolts on 1, 10 nuts on 2
	h.catalog.setPackagings(varBolt, contracts.PackagingEntry{ID: 9, Name: "Box", Kind: "case", Quantity: "10", SortOrder: 1})
	nut := reorderAt(12)
	five := 5.0
	nut.Multiple = &five
	setPlacement(t, c, varNut, s.osl.Id, nut)           // 10 ≤ 12: 3 needed, 5 by the multiple
	setPlacement(t, c, varBolt, s.bgo.Id, reorderAt(3)) // 0 + 2 on order ≤ 3: 2 needed, not a box of 10
	setPlacement(t, c, varBolt, s.osl.Id, reorderAt(5)) // 20 > 5: not low
	supply.Set(contracts.OpenPurchase{VariantID: varBolt, SiteID: s.bgo.Id, Quantity: "2"})

	before := h.Now()
	demand := inventory.PurchaseDemandOf(h.Deps())
	got, err := demand.Demand(context.Background(), nil)
	if err != nil {
		t.Fatalf("Demand: %v", err)
	}
	want := map[[2]int64]string{{int64(varBolt), s.bgo.Id}: "2", {int64(varNut), s.osl.Id}: "5"}
	if len(got) != len(want) {
		t.Fatalf("demand = %+v, want %v", got, want)
	}
	for _, n := range got {
		if q, ok := want[[2]int64{int64(n.VariantID), n.SiteID}]; !ok || q != n.Quantity {
			t.Errorf("need %+v, want %v", n, want)
		}
		if n.Source != "low_stock" || n.NeededOn != nil || n.ComputedAt.Before(before) || n.ComputedAt.After(h.Now().Add(time.Second)) {
			t.Errorf("need %+v: want low_stock, needed now, computed when read", n)
		}
	}

	bgo, err := demand.Demand(context.Background(), &s.bgo.Id)
	if err != nil || len(bgo) != 1 || bgo[0].VariantID != varBolt {
		t.Errorf("Demand at BGO = %+v, %v; want the bolt alone", bgo, err)
	}

}
