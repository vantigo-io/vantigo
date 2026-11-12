package integration_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	mfggen "github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Lots through Products, Inventory, Manufacturing and Orders together
// (manufacturing phase 2, layer M), against the real modules: a variant is
// made lot-tracked through the API now that the interim guard is gone and
// its tracking then held by its stock; a lot is received; a production
// consumes it — Inventory taking it FEFO under its own locks — and makes the
// order's output lot with its expiry from the variant's offset; a sales
// order delivers that output lot; a held lot is refused; and two productions
// racing a hold on their shared lot on a pool of four end in an answer each,
// never a failure.

const mfgBase = "/api/v1/manufacturing"

// manufacturingPermissions are the keys beyond deliveryPermissions.
var manufacturingPermissions = []string{
	"manufacturing:access", "manufacturing:boms-manage", "manufacturing:orders-manage", "manufacturing:shop-floor",
	"inventory:lots-manage",
}

// lotsInstallation is a stock installation with Manufacturing, a jam made
// of berries, both tracked in lots, the jam's lots expiring 30 days after
// production.
type lotsInstallation struct {
	stockInstallation
	jam, berry   int32
	berryProduct int32
	berrySKU     string
	center       int64
}

func newLotsInstallation(t *testing.T, opts ...modtest.Option) lotsInstallation {
	t.Helper()
	s := lotsInstallation{stockInstallation: newStockInstallationOf(t, []string{modManufacturing}, manufacturingPermissions, opts...)}
	standard := taxCategory(t, s.c, "Food", 0.15, 2)
	type product struct {
		Id       int32 `json:"id"`
		Variants []struct {
			Id       int32  `json:"id"`
			Sku      string `json:"sku"`
			Tracking string `json:"tracking"`
		} `json:"variants"`
	}
	var jam, berry product
	okJSON(t, s.c, http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Jam", "type": "Goods", "status": "Active", "taxCategoryId": standard,
		"variants": []map[string]any{{"sku": "JAM-1", "tracking": "lot", "expiryDays": 30}},
	}, &jam)
	okJSON(t, s.c, http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", jam.Id, jam.Variants[0].Id),
		map[string]any{"currency": "NOK", "amount": 49.0}, nil)
	okJSON(t, s.c, http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Berries", "type": "Goods", "status": "Active", "taxCategoryId": standard,
		"variants": []map[string]any{{"sku": "BER-1"}},
	}, &berry)
	// Tracking switched on through the variant's update: nothing holds it.
	var updated struct {
		Tracking string `json:"tracking"`
	}
	okJSON(t, s.c, http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", berry.Id, berry.Variants[0].Id),
		map[string]any{"sku": "BER-1", "unit": "kg", "tracking": "lot"}, &updated)
	if jam.Variants[0].Tracking != "lot" || updated.Tracking != "lot" {
		t.Fatalf("tracking: jam %s, berries %s; want lot", jam.Variants[0].Tracking, updated.Tracking)
	}
	s.jam, s.berry, s.berryProduct, s.berrySKU = jam.Variants[0].Id, berry.Variants[0].Id, berry.Id, "BER-1"

	var center mfggen.ManufacturingWorkCenter
	okJSON(t, s.c, http.MethodPost, mfgBase+"/work-centers", mfggen.ManufacturingWorkCenterInput{Code: "POT", Name: "Pot", SiteId: s.site}, &center)
	s.center = center.Id
	okJSON(t, s.c, http.MethodPost, mfgBase+"/boms", mfggen.ManufacturingBomInput{VariantId: s.jam, OutputQuantity: "1",
		Operations: []mfggen.ManufacturingBomOperationInput{{Key: "a", Sequence: 10, Name: "Boil", WorkCenterId: center.Id, MinutesPerCycle: "1"}},
		Lines:      []mfggen.ManufacturingBomLineInput{{VariantId: s.berry, Quantity: "2"}}}, nil)
	return s
}

// receiveLot receives qty of the variant into a lot numbered number.
func (s lotsInstallation) receiveLot(t *testing.T, variant int32, number string, qty float64) {
	t.Helper()
	okJSON(t, s.c, http.MethodPost, "/api/v1/inventory/movements/receipts", map[string]any{
		"clientRequestId": uuid.New(), "siteId": s.site, "toLocationId": s.loc,
		"lines": []map[string]any{{"variantId": variant, "quantity": qty, "lot": map[string]any{"number": number, "expiry": "2030-01-01"}}},
	}, nil)
}

// jamOrder is a confirmed manufacturing order of qty jam.
func (s lotsInstallation) jamOrder(t *testing.T, qty string) mfggen.ManufacturingOrder {
	t.Helper()
	var o mfggen.ManufacturingOrder
	okJSON(t, s.c, http.MethodPost, mfgBase+"/orders", mfggen.ManufacturingOrderInput{VariantId: s.jam, Quantity: qty}, &o)
	var confirmed mfggen.ManufacturingOrderWithShortLines
	okJSON(t, s.c, http.MethodPost, fmt.Sprintf("%s/orders/%d/confirm", mfgBase, o.Id), mfggen.ManufacturingRevisionInput{Revision: o.Revision}, &confirmed)
	return confirmed.Order
}

// produce posts a production of qty jam consuming berries as body says.
func (s lotsInstallation) produce(o mfggen.ManufacturingOrder, in mfggen.ManufacturingProductionInput) *modtest.Response {
	return s.c.Do(http.MethodPost, fmt.Sprintf("%s/orders/%d/productions", mfgBase, o.Id), in)
}

func TestManufacturingLots_ReceiveProduceDeliver(t *testing.T) {
	t.Parallel()
	s := newLotsInstallation(t)
	s.receiveLot(t, s.berry, "BER-A", 10)

	// The berries now hold stock: their tracking is held.
	r := s.c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", s.berryProduct, s.berry),
		map[string]any{"sku": s.berrySKU, "unit": "kg", "tracking": "none"})
	if r.Status != http.StatusConflict {
		t.Fatalf("untracking held berries = %d %s, want 409", r.Status, r.Body)
	}

	o := s.jamOrder(t, "3")
	var draft mfggen.ManufacturingProductionDraft
	okJSON(t, s.c, http.MethodGet, fmt.Sprintf("%s/orders/%d/production-draft", mfgBase, o.Id), nil, &draft)
	if draft.Output.Tracking != "lot" || draft.Output.LotNumber == nil || *draft.Output.LotNumber != o.Number || draft.Output.Expiry == nil ||
		!draft.Output.Expiry.Equal(draft.Output.ProductionDate.AddDate(0, 0, 30)) {
		t.Errorf("draft output = %+v", draft.Output)
	}
	if l := draft.Lines[0]; len(l.Lots) != 1 || l.Lots[0].LotNumber != "BER-A" || l.Lots[0].Quantity != "6" || len(l.LotChoices) != 1 {
		t.Errorf("draft line = %+v", l)
	}
	berryLot := draft.Lines[0].LotChoices[0].LotId

	res := s.produce(o, mfggen.ManufacturingProductionInput{ClientRequestId: "jam-1", Quantity: "3",
		Lines: []mfggen.ManufacturingProductionLineInput{{ComponentId: *o.Components[0].Id, Consumed: strPtr("6")}}})
	if res.Status != http.StatusCreated {
		t.Fatalf("the production = %d %s", res.Status, res.Body)
	}
	var p mfggen.ManufacturingProduction
	res.JSON(&p)
	if len(p.OutputLots) != 1 || p.OutputLots[0].LotNumber != o.Number || p.OutputLots[0].Quantity != "3" {
		t.Fatalf("output lots = %+v", p.OutputLots)
	}
	if l := p.Lines[0].Lots; len(l) != 1 || l[0].LotId != berryLot || l[0].Consumed != "6" {
		t.Errorf("consumed lots = %+v", l)
	}
	jamLot := p.OutputLots[0].LotId
	expiry := modtest.One[string](t, s.h, `SELECT expiry::text FROM inventory.lots WHERE id = $1`, jamLot)
	if want := draft.Output.ProductionDate.AddDate(0, 0, 30).Format("2006-01-02"); expiry != want {
		t.Errorf("the jam lot's expiry %s, want %s", expiry, want)
	}
	if left := modtest.One[string](t, s.h, `SELECT COALESCE(sum(on_hand), 0)::text FROM inventory.balances WHERE lot_id = $1`, berryLot); left != "4.000" {
		t.Errorf("berries left in BER-A = %s", left)
	}

	// A sales order delivers the jam from the lot the production made.
	sale := s.draftOrder(t, orderLine(s.jam, 2, 0))
	if res := s.confirm(s.c, sale, true); res.Status != http.StatusOK {
		t.Fatalf("confirm the sale = %d %s", res.Status, res.Body)
	}
	d := deliveryFrom(t, s.newDelivery(sale.Id), http.StatusCreated, "the jam's delivery")
	if l := d.Lines[0]; !l.Tracked || len(l.Lots) != 1 || l.Lots[0].LotId != jamLot || l.Lots[0].Quantity != 2 {
		t.Fatalf("delivery lots = %+v", d.Lines[0].Lots)
	}
	deliveryFrom(t, s.post(d), http.StatusOK, "post the jam's delivery")
	if left := modtest.One[string](t, s.h, `SELECT COALESCE(sum(on_hand), 0)::text FROM inventory.balances WHERE lot_id = $1`, jamLot); left != "1.000" {
		t.Errorf("jam left in %s = %s", o.Number, left)
	}
	var lots mfggen.ManufacturingOrderLots
	okJSON(t, s.c, http.MethodGet, fmt.Sprintf("%s/orders/%d/lots", mfgBase, o.Id), nil, &lots)
	if len(lots.Outputs) != 1 || lots.Outputs[0].LotId != jamLot || len(lots.Components) != 1 || lots.Components[0].Lots[0].LotId != berryLot {
		t.Errorf("the order's lots = %+v", lots)
	}

	// A quarantined lot is refused when named.
	okJSON(t, s.c, http.MethodPost, fmt.Sprintf("/api/v1/inventory/lots/%d/holds", berryLot), map[string]any{"kind": "quarantine", "cause": "inspection"}, nil)
	o2 := s.jamOrder(t, "1")
	res = s.produce(o2, mfggen.ManufacturingProductionInput{ClientRequestId: "held", Quantity: "1",
		Lines: []mfggen.ManufacturingProductionLineInput{{ComponentId: *o2.Components[0].Id, Consumed: strPtr("2"),
			Lots: &[]mfggen.ManufacturingProductionLotInput{{LotId: berryLot, LocationId: s.loc, Consumed: strPtr("2")}}}}})
	if res.Status != http.StatusConflict || problemCode(res) != "lot_blocked" {
		t.Errorf("a quarantined lot = %d %s, want 409 lot_blocked", res.Status, res.Body)
	}
}

// TestManufacturingLots_RaceOnSharedLot is M86 on the real ledger: two
// productions of two orders consume the same lot while the lot page puts a
// hold on it, on a pool of four. Whatever order they meet in, each answers —
// a production 201, or 409 lot_blocked when the hold came first — and the
// hold is put; a deadlock is retried, never answered as a failure.
func TestManufacturingLots_RaceOnSharedLot(t *testing.T) {
	t.Parallel()
	s := newLotsInstallation(t, modtest.WithPoolMaxConns(4))
	s.receiveLot(t, s.berry, "BER-R", 100)
	a, b := s.jamOrder(t, "2"), s.jamOrder(t, "2")
	var draft mfggen.ManufacturingProductionDraft
	okJSON(t, s.c, http.MethodGet, fmt.Sprintf("%s/orders/%d/production-draft", mfgBase, a.Id), nil, &draft)
	lot := draft.Lines[0].LotChoices[0].LotId
	in := func(o mfggen.ManufacturingOrder, id string) mfggen.ManufacturingProductionInput {
		return mfggen.ManufacturingProductionInput{ClientRequestId: id, Quantity: "2",
			Lines: []mfggen.ManufacturingProductionLineInput{{ComponentId: *o.Components[0].Id, Consumed: strPtr("4"),
				Lots: &[]mfggen.ManufacturingProductionLotInput{{LotId: lot, LocationId: s.loc, Consumed: strPtr("4")}}}}}
	}
	var wg sync.WaitGroup
	results := make([]*modtest.Response, 3)
	for i, run := range []func() *modtest.Response{
		func() *modtest.Response { return s.produce(a, in(a, "race-a")) },
		func() *modtest.Response { return s.produce(b, in(b, "race-b")) },
		func() *modtest.Response {
			return s.c.Do(http.MethodPost, fmt.Sprintf("/api/v1/inventory/lots/%d/holds", lot), map[string]any{"kind": "quarantine", "cause": "other"})
		},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = run()
		}()
	}
	wg.Wait()
	for i, r := range results[:2] {
		if r.Status != http.StatusCreated && (r.Status != http.StatusConflict || problemCode(r) != "lot_blocked") {
			t.Errorf("production %d = %d %s, want 201 or 409 lot_blocked", i, r.Status, r.Body)
		}
	}
	if r := results[2]; r.Status != http.StatusOK && r.Status != http.StatusCreated {
		t.Errorf("the hold = %d %s", r.Status, r.Body)
	}
}
