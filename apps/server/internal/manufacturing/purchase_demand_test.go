package manufacturing_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
)

// TestPurchaseDemand_BuySuggestions is purchasing design P31 and A4:
// Manufacturing's purchase demand is its open buy suggestions as the run
// stored them — never a make suggestion — each needed by its need-by day and
// computed when its run started; a later run's rows replace the earlier's.
func TestPurchaseDemand_BuySuggestions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.setPlacements(siteMain,
		contracts.StockPlacement{VariantID: varBike, ReorderPoint: "10", OnHand: "0", Reserved: "0", Incoming: "0"},
		contracts.StockPlacement{VariantID: varWheel, ReorderPoint: "10", Multiple: "4", OnHand: "5", Reserved: "0", Incoming: "0"},
	)
	started := h.Now()
	runReplenishment(t, h)
	demand := manufacturing.PurchaseDemandOf(h.Deps())
	got, err := demand.Demand(context.Background(), nil)
	if err != nil {
		t.Fatalf("Demand: %v", err)
	}
	// The wheel: 11 − 5 = 6, by the multiple 8; the bike is made, not bought.
	if len(got) != 1 || got[0].VariantID != varWheel || got[0].SiteID != siteMain || got[0].Quantity != "8" ||
		got[0].Source != "replenishment" || got[0].NeededOn == nil || got[0].ComputedAt.Before(started.Add(-1)) {
		t.Fatalf("demand = %+v, want the wheel's 8, needed by its day, computed at the run", got)
	}
	run := got[0].ComputedAt

	other := siteMain + 1
	if rows, err := demand.Demand(context.Background(), &other); err != nil || len(rows) != 0 {
		t.Errorf("Demand at another site = %+v, %v", rows, err)
	}

	h.Advance(time.Hour)
	h.stock.setPlacements(siteMain, contracts.StockPlacement{VariantID: varWheel, ReorderPoint: "10", OnHand: "9", Reserved: "0", Incoming: "0"})
	mustStatus(t, c.Do(http.MethodPost, runsPath, gen.ManufacturingReplenishmentRunInput{}), http.StatusAccepted, "run now")
	runReplenishment(t, h)
	main := siteMain
	got, err = demand.Demand(context.Background(), &main)
	if err != nil || len(got) != 1 || got[0].Quantity != "2" || !got[0].ComputedAt.After(run) {
		t.Errorf("after a second run = %+v, %v; want the wheel's 2 of the later run", got, err)
	}
}
