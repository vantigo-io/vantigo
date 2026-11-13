package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	invgen "github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	mfggen "github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The trace on the real modules (manufacturing phase 2, layer T): berries
// received in a lot, made into jam, the jam delivered by a sales order; the
// berries' usage trace reaches the delivery through Orders'
// contracts.DeliveryTrace — with its order and customer for a caller holding
// orders:access, the customer's name only with customers:view, and the
// movement alone without orders:access; and the berries' block reaches the
// jam still in stock, which the next delivery may then not take.
func TestManufacturingLots_TraceAndBlock(t *testing.T) {
	t.Parallel()
	s := newLotsInstallation(t)
	s.receiveLot(t, s.berry, "BER-T", 10)
	o := s.jamOrder(t, "3")
	res := s.produce(o, mfggen.ManufacturingProductionInput{ClientRequestId: "jam-t", Quantity: "3",
		Lines: []mfggen.ManufacturingProductionLineInput{{ComponentId: *o.Components[0].Id, Consumed: strPtr("6")}}})
	if res.Status != http.StatusCreated {
		t.Fatalf("the production = %d %s", res.Status, res.Body)
	}
	var p mfggen.ManufacturingProduction
	res.JSON(&p)
	jamLot, berryLot := p.OutputLots[0].LotId, p.Lines[0].Lots[0].LotId
	sale := s.draftOrder(t, orderLine(s.jam, 2, 0))
	if res := s.confirm(s.c, sale, true); res.Status != http.StatusOK {
		t.Fatalf("confirm the sale = %d %s", res.Status, res.Body)
	}
	d := deliveryFrom(t, s.newDelivery(sale.Id), http.StatusCreated, "the jam's delivery")
	d = deliveryFrom(t, s.post(d), http.StatusOK, "post the jam's delivery")

	trace := func(keys ...string) invgen.InventoryTrace {
		t.Helper()
		var tr invgen.InventoryTrace
		okJSON(t, s.h.SignIn(t, keys...), http.MethodGet, fmt.Sprintf("/api/v1/inventory/trace?lotId=%d", berryLot), nil, &tr)
		return tr
	}
	views := []string{"inventory:locations-view", "inventory:stock-view"}
	full := trace(append(views, "orders:access", "customers:view")...)
	if len(full.Nodes) != 3 || full.Nodes[1].Lot == nil || full.Nodes[1].Lot.Id != jamLot || full.Nodes[2].Kind != invgen.TraceIssue {
		t.Fatalf("the berries' trace = %+v", full.Nodes)
	}
	if dl := full.Nodes[2].Delivery; full.Deliveries != invgen.Shown || dl == nil || dl.DeliveryId != d.Id || dl.OrderNumber != sale.Number ||
		dl.CustomerId != s.customer || dl.CustomerName == nil || *dl.CustomerName == "" {
		t.Errorf("the delivery with names = %+v (%s)", dl, full.Deliveries)
	}
	if e := full.Nodes[1].Edge; e == nil || e.Source == nil || e.Source.LineId == nil || *e.Source.LineId != fmt.Sprint(o.Id) {
		t.Errorf("the jam's edge = %+v, want the production of order %d", e, o.Id)
	}
	if tr := trace(append(views, "orders:access")...); tr.CustomerNames || tr.Nodes[2].Delivery == nil || tr.Nodes[2].Delivery.CustomerName != nil {
		t.Errorf("without customers:view = %+v", tr.Nodes[2].Delivery)
	}
	if tr := trace(views...); tr.Deliveries != invgen.Hidden || tr.Nodes[2].Delivery != nil {
		t.Errorf("without orders:access = %s %+v", tr.Deliveries, tr.Nodes[2].Delivery)
	}

	var blocked invgen.InventoryLotBlock
	okJSON(t, s.c, http.MethodPost, fmt.Sprintf("/api/v1/inventory/lots/%d/block", berryLot), map[string]any{"cause": "recall"}, &blocked)
	if len(blocked.Blocked) != 2 || blocked.Blocked[0].Id != berryLot || blocked.Blocked[1].Id != jamLot {
		t.Fatalf("blocked = %+v, want the berries and the jam", blocked.Blocked)
	}
	// The jam left is blocked: it is not available, and a new delivery
	// has nothing to take.
	sale2 := s.draftOrder(t, orderLine(s.jam, 1, 0))
	if res := s.confirm(s.c, sale2, false); res.Status != http.StatusOK {
		t.Fatalf("confirm the second sale = %d %s", res.Status, res.Body)
	}
	if res := s.newDelivery(sale2.Id); res.Status != http.StatusConflict || problemCode(res) != "nothing_to_deliver" {
		t.Errorf("a delivery of the blocked jam = %d %s, want 409 nothing_to_deliver", res.Status, res.Body)
	}
}

// TestManufacturingLots_UnbuildIsAReverseEdge is manufacturing phase 2 M123
// and M88 on the real modules: jam made from berries, its order closed, the
// jam's lot blocked by a recall, then taken apart by an unbuild. The unbuild
// is posted as a movement of kind unbuild; the berries it restored go back
// into their own lot under a quarantine hold from the unbuild; the jam's usage
// trace ends at the unbuild and never walks into the berries, the berries'
// origin is still their receipt, and the berries' usage does not reach back
// through the unbuild.
func TestManufacturingLots_UnbuildIsAReverseEdge(t *testing.T) {
	t.Parallel()
	s := newLotsInstallation(t)
	s.receiveLot(t, s.berry, "BER-U", 10)
	o := s.jamOrder(t, "3")
	res := s.produce(o, mfggen.ManufacturingProductionInput{ClientRequestId: "jam-u", Quantity: "3",
		Lines: []mfggen.ManufacturingProductionLineInput{{ComponentId: *o.Components[0].Id, Consumed: strPtr("6")}}})
	if res.Status != http.StatusCreated {
		t.Fatalf("the production = %d %s", res.Status, res.Body)
	}
	var p mfggen.ManufacturingProduction
	res.JSON(&p)
	jamLot, berryLot := p.OutputLots[0].LotId, p.Lines[0].Lots[0].LotId
	var now mfggen.ManufacturingOrder
	okJSON(t, s.c, http.MethodGet, fmt.Sprintf("%s/orders/%d", mfgBase, o.Id), nil, &now)
	var closed mfggen.ManufacturingCloseResult
	okJSON(t, s.c, http.MethodPost, fmt.Sprintf("%s/orders/%d/close", mfgBase, o.Id), mfggen.ManufacturingCloseInput{Revision: now.Revision}, &closed)
	okJSON(t, s.c, http.MethodPost, fmt.Sprintf("/api/v1/inventory/lots/%d/holds", jamLot), map[string]any{"kind": "block", "cause": "recall"}, nil)

	var u mfggen.ManufacturingUnbuild
	okJSON(t, s.c, http.MethodPost, mfgBase+"/unbuilds", mfggen.ManufacturingUnbuildInput{ClientRequestId: "ub-1", OrderId: &o.Id, Quantity: "1", LotId: &jamLot}, &u)
	if u.MovementId == nil || len(u.Lines) != 1 || u.Lines[0].LotId == nil || *u.Lines[0].LotId != berryLot || u.Lines[0].Quantity != "2" {
		t.Fatalf("unbuild = %+v", u)
	}
	if kind := modtest.One[string](t, s.h, `SELECT kind FROM inventory.movements WHERE id = $1`, *u.MovementId); kind != "unbuild" {
		t.Errorf("the movement's kind = %s", kind)
	}
	if got := modtest.One[string](t, s.h, `SELECT status FROM inventory.lots WHERE id = $1`, berryLot); got != "quarantine" {
		t.Errorf("the restored berries are %s, want quarantine", got)
	}
	if got := modtest.One[string](t, s.h, `SELECT COALESCE(sum(on_hand), 0)::text FROM inventory.balances WHERE lot_id = $1`, berryLot); got != "6.000" {
		t.Errorf("berries in their lot after the unbuild = %s", got)
	}

	trace := func(lot int64, direction string) invgen.InventoryTrace {
		t.Helper()
		var tr invgen.InventoryTrace
		okJSON(t, s.c, http.MethodGet, fmt.Sprintf("/api/v1/inventory/trace?lotId=%d&direction=%s", lot, direction), nil, &tr)
		return tr
	}
	usage := trace(jamLot, "usage")
	for _, n := range usage.Nodes {
		if n.Lot != nil && n.Lot.Id == berryLot {
			t.Errorf("the jam's usage walks into the berries: %+v", usage.Nodes)
		}
	}
	unbuilds := 0
	for _, n := range usage.Nodes {
		if n.Kind == invgen.TraceUnbuild {
			unbuilds++
		}
	}
	if unbuilds != 1 {
		t.Errorf("the jam's usage ends at %d unbuilds: %+v", unbuilds, usage.Nodes)
	}
	origin := trace(berryLot, "origin")
	for _, n := range origin.Nodes {
		if n.Lot != nil && n.Lot.Id == jamLot {
			t.Errorf("the berries' origin walks into the jam: %+v", origin.Nodes)
		}
	}
	berryUsage := trace(berryLot, "usage")
	seen := 0
	for _, n := range berryUsage.Nodes {
		if n.Lot != nil && n.Lot.Id == jamLot {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the berries' usage shows the jam %d times, want once (the production, not the unbuild back): %+v", seen, berryUsage.Nodes)
	}
}
