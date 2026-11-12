package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	invgen "github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	mfggen "github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
)

// Deviations through Manufacturing and Inventory together (manufacturing
// phase 2, layer Q, M99 and M64), against the real ledger: a quarantining
// deviation opened before any output holds nothing, then the order's first
// production makes its lot under the deviation's hold, in the production's
// transaction; the lot page refuses to release that hold (it is
// Manufacturing's); a recall's block on the same lot survives the
// deviation's close as use_as_is, which releases only its own hold.
func TestManufacturingQuality_DeviationHoldsAndRecall(t *testing.T) {
	t.Parallel()
	s := newLotsInstallation(t)
	s.receiveLot(t, s.berry, "BER-Q", 10)
	o := s.jamOrder(t, "3")
	quarantine := true

	var d mfggen.ManufacturingQualityDeviation
	okJSON(t, s.c, http.MethodPost, mfgBase+"/deviations", mfggen.ManufacturingQualityDeviationInput{
		Title: "First piece too light", OrderId: &o.Id, Quarantine: &quarantine}, &d)
	if len(d.Lots) != 0 || !d.Quarantine {
		t.Fatalf("deviation = %+v", d)
	}
	res := s.produce(o, mfggen.ManufacturingProductionInput{ClientRequestId: "q-1", Quantity: "2",
		Lines: []mfggen.ManufacturingProductionLineInput{{ComponentId: *o.Components[0].Id, Consumed: strPtr("4")}}})
	if res.Status != http.StatusCreated {
		t.Fatalf("the production = %d %s", res.Status, res.Body)
	}
	var p mfggen.ManufacturingProduction
	res.JSON(&p)
	lot := p.OutputLots[0].LotId
	lotPath := fmt.Sprintf("/api/v1/inventory/lots/%d", lot)
	var page invgen.InventoryLot
	okJSON(t, s.c, http.MethodGet, lotPath, nil, &page)
	if page.Status != invgen.InventoryLotStatusQuarantine || len(page.Holds) != 1 || page.Holds[0].Cause != invgen.InventoryLotHoldCauseDeviation ||
		page.Holds[0].Inventorys || page.Holds[0].Source.Module != "manufacturing" || page.Holds[0].Source.Id != fmt.Sprint(d.Id) {
		t.Fatalf("the output lot = %+v", page)
	}
	deviationHold := page.Holds[0].Id
	if r := s.c.Do(http.MethodPost, fmt.Sprintf("%s/holds/%d/release", lotPath, deviationHold), invgen.InventoryLotHoldRelease{}); r.Status != http.StatusConflict {
		t.Errorf("the lot page releasing a deviation's hold = %d %s", r.Status, r.Body)
	}

	// A recall blocks the lot from the lot page.
	okJSON(t, s.c, http.MethodPost, lotPath+"/holds", invgen.InventoryLotHoldInput{Kind: invgen.InventoryLotHoldInputKindBlock,
		Cause: invgen.InventoryLotHoldInputCauseRecall}, &page)
	okJSON(t, s.c, http.MethodGet, fmt.Sprintf("%s/deviations/%d", mfgBase, d.Id), nil, &d)
	okJSON(t, s.c, http.MethodPost, fmt.Sprintf("%s/deviations/%d/close", mfgBase, d.Id), mfggen.ManufacturingDeviationCloseInput{
		Disposition: mfggen.UseAsIs, ClosingNote: "Within tolerance after re-weighing.", Revision: d.Revision}, &d)
	okJSON(t, s.c, http.MethodGet, lotPath, nil, &page)
	active := map[invgen.InventoryLotHoldCause]bool{}
	for _, h := range page.Holds {
		if h.ReleasedAt == nil {
			active[h.Cause] = true
		}
	}
	if page.Status != invgen.InventoryLotStatusBlocked || len(active) != 1 || !active[invgen.InventoryLotHoldCauseRecall] {
		t.Errorf("after the close: %s, active %v", page.Status, active)
	}

	// A later production of the order holds nothing: the deviation is closed.
	res = s.produce(o, mfggen.ManufacturingProductionInput{ClientRequestId: "q-2", Quantity: "1",
		Lines: []mfggen.ManufacturingProductionLineInput{{ComponentId: *o.Components[0].Id, Consumed: strPtr("2")}}})
	if res.Status != http.StatusCreated {
		t.Fatalf("the second production = %d %s", res.Status, res.Body)
	}
	okJSON(t, s.c, http.MethodGet, fmt.Sprintf("%s/deviations/%d", mfgBase, d.Id), nil, &d)
	if len(d.Lots) != 1 || !d.HoldsReleased {
		t.Errorf("the closed deviation = %+v", d)
	}
}
