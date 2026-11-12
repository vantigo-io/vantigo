package inventory_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// Inspection on receipt (manufacturing phase 2 M100): the variant's rule,
// set only on a tracked variant; a receipt's new lot and a lot it adds to
// each held for inspection from the receipt; the hold released on the lot
// page as one of Inventory's own; the queue's count.

func rulesPath(variant int32) string {
	return fmt.Sprintf("/api/v1/inventory/variants/%d/rules", variant)
}

// TestVariantRules_InspectionOnReceipt is M100 end to end.
func TestVariantRules_InspectionOnReceipt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, everything...)
	c, _ := signIn(t, h, lotManager...)
	adjuster, _ := signIn(t, h, stockAdjuster...)
	s := seedLots(t, h, admin)

	res := c.Do(http.MethodGet, rulesPath(varLot), nil)
	mustStatus(t, res, http.StatusOK, "rules of a variant with none")
	var rules gen.InventoryVariantRules
	res.JSON(&rules)
	if rules.InspectOnReceipt || rules.Revision != 0 || rules.UpdatedAt != nil {
		t.Fatalf("no rules = %+v", rules)
	}
	if res := c.Do(http.MethodPut, rulesPath(varLot), gen.InventoryVariantRulesInput{InspectOnReceipt: true}); res.Status != http.StatusForbidden {
		t.Errorf("a put without stock-adjust = %d", res.Status)
	}
	expectRefusal(t, adjuster.Do(http.MethodPut, rulesPath(varBolt), gen.InventoryVariantRulesInput{InspectOnReceipt: true}),
		http.StatusBadRequest, "inspection_requires_tracking", "an untracked variant")
	res = adjuster.Do(http.MethodPut, rulesPath(varLot), gen.InventoryVariantRulesInput{InspectOnReceipt: true})
	mustStatus(t, res, http.StatusOK, "inspect on receipt")
	res.JSON(&rules)
	if !rules.InspectOnReceipt || rules.Revision != 1 || rules.UpdatedBy == nil {
		t.Fatalf("rules = %+v", rules)
	}
	expectRefusal(t, adjuster.Do(http.MethodPut, rulesPath(varLot), gen.InventoryVariantRulesInput{InspectOnReceipt: true}),
		http.StatusConflict, "revision_conflict", "a stale revision")
	mustStatus(t, adjuster.Do(http.MethodPut, rulesPath(9999), gen.InventoryVariantRulesInput{InspectOnReceipt: false}), http.StatusNotFound, "an unknown variant")

	// A receipt makes the lot under an inspection hold, and says so.
	first := receive(t, admin, s.site.Id, s.shelf1.Id, lotLine(varLot, 5, gen.InventoryNewLot{Number: ptr("INS-1")}))
	lot := lotID(t, h, varLot, "INS-1")
	if first.InspectionLots == nil || !slices.Equal(*first.InspectionLots, []int64{lot}) {
		t.Fatalf("the receipt's inspection lots = %+v", first.InspectionLots)
	}
	l := getLot(t, c, lot)
	if l.Status != gen.InventoryLotStatusQuarantine || len(l.Holds) != 1 || l.Holds[0].Cause != gen.InventoryLotHoldCauseInspection ||
		!l.Holds[0].Inventorys {
		t.Fatalf("the lot after the receipt = %+v", l)
	}
	if p := listLots(t, c, "?status=quarantine&holdCause=inspection"); len(p.Lots) != 1 {
		t.Errorf("the inspection queue = %+v", p.Lots)
	}
	var stats gen.InventoryStats
	admin.Do(http.MethodGet, statsPath, nil).JSON(&stats)
	if stats.LotsAwaitingInspection == nil || *stats.LotsAwaitingInspection != 1 {
		t.Errorf("stats = %+v", stats.LotsAwaitingInspection)
	}

	// Released on the lot page, the lot is available; a second receipt into
	// it puts a new hold from that receipt.
	release := fmt.Sprintf("%s/holds/%d/release", lotPath(lot), l.Holds[0].Id)
	mustStatus(t, c.Do(http.MethodPost, release, gen.InventoryLotHoldRelease{Note: ptr("Inspected, fine")}), http.StatusOK, "release the inspection hold")
	if l = getLot(t, c, lot); l.Status != gen.InventoryLotStatusAvailable {
		t.Fatalf("after the release = %s", l.Status)
	}
	second := receive(t, admin, s.site.Id, s.shelf1.Id, gen.InventoryReceiptLine{VariantId: varLot, Quantity: qty(3), LotId: &lot})
	if second.InspectionLots == nil || len(*second.InspectionLots) != 1 {
		t.Fatalf("the second receipt's inspection lots = %+v", second.InspectionLots)
	}
	l = getLot(t, c, lot)
	active := 0
	for _, hold := range l.Holds {
		if hold.ReleasedAt == nil {
			active++
			if hold.Source.Id != fmt.Sprint(second.Id) {
				t.Errorf("the new hold's source = %+v", hold.Source)
			}
		}
	}
	if l.Status != gen.InventoryLotStatusQuarantine || active != 1 || len(l.Holds) != 2 {
		t.Errorf("after the second receipt = %s, holds %+v", l.Status, l.Holds)
	}
	assertLedgerConsistent(t, h)
}
