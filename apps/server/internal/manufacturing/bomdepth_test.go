package manufacturing_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// BoM depth (manufacturing phase 2 M120–M128): by-products with their cost
// share and lots, unbuilds that restore the chosen lot's consumption only,
// and the split and merge of orders under the schedule's locks.

const unbuildsPath = base + "/unbuilds"

func output(variant int32, qty, share string) gen.ManufacturingBomOutputInput {
	return gen.ManufacturingBomOutputInput{VariantId: variant, Quantity: qty, CostShare: &share}
}

func orderOutput(t *testing.T, o gen.ManufacturingOrder, variant int32) gen.ManufacturingOrderOutput {
	t.Helper()
	for _, x := range o.Outputs {
		if x.Variant.Id == variant {
			return x
		}
	}
	t.Fatalf("%s has no by-product of variant %d: %+v", o.Number, variant, o.Outputs)
	return gen.ManufacturingOrderOutput{}
}

func produce(t *testing.T, c *modtest.Client, o gen.ManufacturingOrder, in gen.ManufacturingProductionInput) gen.ManufacturingProduction {
	t.Helper()
	res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), in)
	mustStatus(t, res, http.StatusCreated, "production "+in.ClientRequestId)
	return productionOf(t, res)
}

func closeShort(t *testing.T, c *modtest.Client, id int64) gen.ManufacturingOrder {
	t.Helper()
	o := getOrder(t, c, id)
	no := false
	res := c.Do(http.MethodPost, orderPath(id, "close"), gen.ManufacturingCloseInput{Revision: o.Revision, Backorder: &no})
	mustStatus(t, res, http.StatusOK, "close "+o.Number)
	return getOrder(t, c, id)
}

// TestBomDepth_OutputRules is M120's refusals: a by-product that is the
// BoM's own variant or one of its components, shares over 100, and a cycle
// through a by-product (the graph's edge from a by-product to its BoM).
func TestBomDepth_OutputRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)

	in := bomInput(varBike, "1", line(varWheel, "2"))
	in.Outputs = &[]gen.ManufacturingBomOutputInput{output(varBike, "1", "0")}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "output_is_product", "the product as a by-product")
	in.Outputs = &[]gen.ManufacturingBomOutputInput{output(varWheel, "1", "0")}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "output_is_component", "a component as a by-product")
	in.Outputs = &[]gen.ManufacturingBomOutputInput{output(varPaint, "1", "60"), output(varTube, "1", "40.01")}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "cost_share_over", "shares over 100")
	in.Outputs = &[]gen.ManufacturingBomOutputInput{output(varService, "1", "0")}
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, in), http.StatusBadRequest, "variant_not_goods", "a service as a by-product")

	// A frame made with paint; a bicycle made of a frame that gives paint
	// off: the by-product is, through the frame, one of its components.
	createBom(t, c, bomInput(varFrame, "1", line(varPaint, "1")))
	cyc := bomInput(varBike, "1", line(varFrame, "1"))
	cyc.Outputs = &[]gen.ManufacturingBomOutputInput{output(varPaint, "0.5", "10")}
	p := expectRefusal(t, c.Do(http.MethodPost, bomsPath, cyc), http.StatusBadRequest, "bom_cycle", "a cycle through a by-product")
	if p.Path == nil || len(*p.Path) == 0 {
		t.Errorf("the cycle's path = %+v", p.Path)
	}

	// The other way round: a bicycle giving tubes off, then a frame made of
	// tubes that the bicycle consumes — the frame walks into the BoM that
	// makes its tube.
	ok := bomInput(varBike, "1", line(varWheel, "2"), line(varFrame, "1"))
	ok.Outputs = &[]gen.ManufacturingBomOutputInput{output(varTube, "2", "12.5"), output(varBolt, "1", "0")}
	b := createBom(t, c, ok)
	if len(b.Outputs) != 2 || b.Outputs[0].Variant.Sku != "TUBE-1" || b.Outputs[0].CostShare != "12.5" || b.Outputs[1].Quantity != "1" {
		t.Fatalf("outputs = %+v", b.Outputs)
	}
	frame := getBom(t, c, b.Id-1)
	up := updateOf(frame)
	up.Lines = append(up.Lines, line(varTube, "1"))
	expectRefusal(t, c.Do(http.MethodPut, bomPath(frame.Id), up), http.StatusBadRequest, "bom_cycle", "a frame consuming the bicycle's by-product")

	// An update that leaves the outputs out removes them.
	keep := updateOf(b)
	keep.Outputs = nil
	res := c.Do(http.MethodPut, bomPath(b.Id), keep)
	mustStatus(t, res, http.StatusOK, "an update without outputs")
	if got := bomOf(t, res); len(got.Outputs) != 0 {
		t.Errorf("outputs after an update without them = %+v", got.Outputs)
	}
}

// TestBomDepth_ByproductsCostAndLots is M121 and M122: confirm snapshots the
// by-products with their expected quantities; a production makes each as a
// produce line of its own variant and its order_outputs id, at the order's
// output location, a lot-tracked one into the order's pattern rendered for
// it; close splits the actual by the shares, and a by-product that produced
// nothing has no cost per unit and leaves its share to the main product.
func TestBomDepth_ByproductsCostAndLots(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	pulp := goods(209, "PULP-1", "Pulp", "")
	pulp.Tracking = contracts.TrackingLot
	h.catalog.seed(pulp)
	c := h.SignIn(t, everything...)
	h.stock.put(varWheel, locMainA1, "100")
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varBike, "2", line(varWheel, "4"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "1")}
	in.Outputs = &[]gen.ManufacturingBomOutputInput{output(209, "1", "10"), output(varPaint, "3", "20")}
	createBom(t, c, in)
	draft := createOrder(t, c, orderInput(varBike, "4"))
	if x := orderOutput(t, draft, 209); x.Id != nil || x.Expected != "2" || x.Tracking != gen.Lot {
		t.Errorf("a draft's by-product = %+v", x)
	}
	o := confirmOrder(t, c, draft).Order
	pulpOut, paintOut := orderOutput(t, o, 209), orderOutput(t, o, varPaint)
	if pulpOut.Id == nil || pulpOut.Expected != "2" || paintOut.Expected != "6" || paintOut.CostShare != "20" {
		t.Fatalf("by-products = %+v", o.Outputs)
	}

	res := c.Do(http.MethodGet, orderPath(o.Id, "production-draft")+"?quantity=2", nil)
	mustStatus(t, res, http.StatusOK, "draft")
	var d gen.ManufacturingProductionDraft
	res.JSON(&d)
	if len(d.ByProducts) != 2 || d.ByProducts[0].Proposed != "1" || d.ByProducts[0].LotNumber == nil || *d.ByProducts[0].LotNumber != o.Number ||
		d.ByProducts[1].Proposed != "3" || d.ByProducts[1].LotNumber != nil {
		t.Errorf("draft by-products = %+v", d.ByProducts)
	}

	wheel := component(t, o, varWheel)
	post := gen.ManufacturingProductionInput{ClientRequestId: "p1", Quantity: "4",
		Lines:   []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Consumed: str("8")}},
		Outputs: &[]gen.ManufacturingProductionOutputInput{{OutputId: *pulpOut.Id, Quantity: "2"}, {OutputId: *paintOut.Id, Quantity: "0"}}}
	bad := post
	bad.ClientRequestId = "bad"
	bad.Outputs = &[]gen.ManufacturingProductionOutputInput{{OutputId: 999999, Quantity: "1"}}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), bad), http.StatusBadRequest, "", "a by-product of another order")
	// A by-product's bad lot number is refused at its own field.
	bad.Outputs = &[]gen.ManufacturingProductionOutputInput{{OutputId: *pulpOut.Id, Quantity: "1", Lot: &gen.ManufacturingOutputLotInput{Number: str("no spaces")}}}
	lp := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), bad), http.StatusBadRequest, "", "a by-product's bad lot number")
	if lp.Errors == nil || len((*lp.Errors)["outputs[0].lot.number"]) != 1 {
		t.Errorf("a by-product's bad lot number's errors = %+v", lp.Errors)
	}
	p := produce(t, c, o, post)
	if len(p.ByProducts) != 1 || p.ByProducts[0].OutputId != *pulpOut.Id || p.ByProducts[0].Quantity != "2" || p.ByProducts[0].LotNumber == nil ||
		*p.ByProducts[0].LotNumber != o.Number {
		t.Fatalf("production by-products = %+v", p.ByProducts)
	}
	// Two produce lines, keyed by (variant, line id): the bicycle under the
	// order's id, the pulp under its order_outputs id, both at the output
	// location; the ids may be equal, the variants tell them apart.
	posted := h.stock.productions()[0]
	if len(posted.Produce) != 2 {
		t.Fatalf("produce lines = %+v", posted.Produce)
	}
	main, by := posted.Produce[0], posted.Produce[1]
	if main.VariantID != varBike || main.LineID != o.Id || by.VariantID != 209 || by.LineID != *pulpOut.Id ||
		by.ToLocationID != o.OutputLocationId || by.LotID == 0 || by.Quantity != "2.000" {
		t.Errorf("produce lines = %+v", posted.Produce)
	}
	if main.LineID == by.LineID {
		t.Logf("the order's id and the by-product's are both %d", main.LineID)
	}
	if got := h.stock.lotAt(by.LotID, o.OutputLocationId); got != "2" {
		t.Errorf("the pulp's lot holds %s", got)
	}
	read := getOrder(t, c, o.Id)
	if orderOutput(t, read, 209).Produced != "2" || orderOutput(t, read, varPaint).Produced != "0" {
		t.Errorf("produced = %+v", read.Outputs)
	}
	var lots gen.ManufacturingOrderLots
	res = c.Do(http.MethodGet, orderPath(o.Id, "lots"), nil)
	mustStatus(t, res, http.StatusOK, "lots")
	res.JSON(&lots)
	if len(lots.ByProducts) != 1 || lots.ByProducts[0].LotId == nil || *lots.ByProducts[0].LotId != by.LotID {
		t.Errorf("the lots tab's by-products = %+v", lots.ByProducts)
	}

	// Close: 8 wheels × 150.50 = 1204.00. The pulp carries 10 %; the paint,
	// never produced, has no cost per unit and its 20 % stays with the bike.
	closed := closeShort(t, c, o.Id)
	cost := closed.Cost
	if cost == nil || cost.Actual == nil || cost.Actual.Total != "1204.00" || cost.MainActual == nil || *cost.MainActual != "1083.60" ||
		cost.CostPerUnit == nil || *cost.CostPerUnit != "270.90" {
		t.Fatalf("cost = %+v", cost)
	}
	if len(cost.ByProducts) != 2 {
		t.Fatalf("cost by-products = %+v", cost.ByProducts)
	}
	pc, ac := cost.ByProducts[0], cost.ByProducts[1]
	if pc.Actual == nil || *pc.Actual != "120.40" || pc.CostPerUnit == nil || *pc.CostPerUnit != "60.20" || pc.NotProduced {
		t.Errorf("pulp's cost = %+v", pc)
	}
	if !ac.NotProduced || ac.CostPerUnit != nil || ac.Actual == nil || *ac.Actual != "240.80" {
		t.Errorf("paint's cost = %+v", ac)
	}
	if pc.Estimate == nil {
		t.Errorf("pulp's estimate = %+v", pc)
	}
	res = c.Do(http.MethodGet, base+"/reports/production-cost?from=2026-09-01&to=2026-12-31", nil)
	mustStatus(t, res, http.StatusOK, "report")
	var report gen.ManufacturingProductionCostReport
	res.JSON(&report)
	if len(report.ByProducts) != 2 || report.ByProducts[0].Actual != "120.40" || !report.ByProducts[1].NotProduced ||
		len(report.Groups) != 1 || report.Groups[0].Totals.CostPerUnit == nil || *report.Groups[0].Totals.CostPerUnit != "270.90" {
		t.Errorf("report = %+v", report)
	}
	// The CSV carries the by-products as a section of their own.
	res = c.Do(http.MethodGet, base+"/reports/production-cost?from=2026-09-01&to=2026-12-31&format=csv", nil)
	mustStatus(t, res, http.StatusOK, "report as CSV")
	if body := string(res.Body); !strings.Contains(body, "byProductOrder;sku;product;costShare;produced;actual;costPerUnit;notProduced") ||
		!strings.Contains(body, o.Number+";PULP-1;Pulp;10;2;120,40;60,20;false") {
		t.Errorf("CSV = %s", body)
	}
	// What its consumption made was not the bicycle alone: the order is not
	// taken apart, the BoM may be.
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "by", OrderId: &o.Id, Quantity: "1"}),
		http.StatusConflict, "unbuild_has_byproducts", "an unbuild of an order that made by-products")
}

// TestBomDepth_ByproductCostWithNothingProduced is M122's edge: an order
// that closes with no good output has no cost per unit anywhere, and every
// share is a part not produced.
func TestBomDepth_ByproductCostWithNothingProduced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	h.stock.put(varWheel, locMainA1, "100")
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varBike, "1", line(varWheel, "2"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "1")}
	in.Outputs = &[]gen.ManufacturingBomOutputInput{output(varPaint, "1", "50")}
	createBom(t, c, in)
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order
	wheel := component(t, o, varWheel)
	produce(t, c, o, gen.ManufacturingProductionInput{ClientRequestId: "scrap", Quantity: "0",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Scrap: str("2")}}})
	cost := closeShort(t, c, o.Id).Cost
	if cost == nil || cost.Actual.Total != "301.00" || cost.CostPerUnit != nil || *cost.MainActual != "301.00" ||
		len(cost.ByProducts) != 1 || !cost.ByProducts[0].NotProduced || cost.ByProducts[0].CostPerUnit != nil {
		t.Errorf("cost = %+v", cost)
	}
}

// unbuildFloor is the lot fixtures with jam made a lot per production
// (L{YYMMDD}-{N}) from two berry lots, one per production, done.
type unbuildFloor struct {
	h            *harness
	c            *modtest.Client
	o            gen.ManufacturingOrder
	j1, j2       int64 // the jam lots, one per production
	b1, b2       int64 // the berry lots, one per production
	berry, sugar gen.ManufacturingOrderComponent
}

func newUnbuildFloor(t *testing.T) unbuildFloor {
	t.Helper()
	h := lotHarness(t)
	c := h.SignIn(t, everything...)
	f := unbuildFloor{h: h, c: c}
	f.b1 = h.stock.receiveLot(varBerry, "B-1", locMainA1, "10", h.Now(), nil)
	f.b2 = h.stock.receiveLot(varBerry, "B-2", locMainA1, "10", h.Now(), nil)
	h.stock.put(varSugar, locMainA1, "100")
	f.o = jamOrder(t, c, "4", "L{YYMMDD}-{N}")
	f.berry, f.sugar = component(t, f.o, varBerry), component(t, f.o, varSugar)
	for i, lot := range []int64{f.b1, f.b2} {
		p := produce(t, c, f.o, gen.ManufacturingProductionInput{ClientRequestId: fmt.Sprintf("p%d", i), Quantity: "2",
			Lines: []gen.ManufacturingProductionLineInput{
				{ComponentId: *f.berry.Id, Consumed: str("4"), Lots: &[]gen.ManufacturingProductionLotInput{{LotId: lot, LocationId: locMainA1, Consumed: str("4")}}},
				{ComponentId: *f.sugar.Id, Consumed: str("2")},
			}})
		if i == 0 {
			f.j1 = p.OutputLots[0].LotId
		} else {
			f.j2 = p.OutputLots[0].LotId
		}
	}
	if f.j1 == f.j2 {
		t.Fatalf("one jam lot for two productions")
	}
	f.o = closeShort(t, c, f.o.Id)
	return f
}

func unbuildOf(t *testing.T, res *modtest.Response) gen.ManufacturingUnbuild {
	t.Helper()
	var u gen.ManufacturingUnbuild
	res.JSON(&u)
	return u
}

// TestBomDepth_UnbuildRestoresOnlyTheChosenLotsProductions is M123 (§23 #8):
// unbuilding one of a done order's lots consumes that lot and restores only
// what the production that made it consumed — its berry lot, proportionally,
// and the untracked sugar — never the other production's berries; its limit
// is what the lot received less what was unbuilt; the post is one Produce
// with Unbuild true; the order counts it unbuilt; replays answer the first.
func TestBomDepth_UnbuildRestoresOnlyTheChosenLotsProductions(t *testing.T) {
	t.Parallel()
	f := newUnbuildFloor(t)
	c, o := f.c, f.o
	in := gen.ManufacturingUnbuildInput{ClientRequestId: "u1", OrderId: &o.Id, Quantity: "1", LotId: &f.j1, Note: str("A dented jar")}
	res := c.Do(http.MethodPost, unbuildsPath, in)
	mustStatus(t, res, http.StatusCreated, "unbuild J1")
	u := unbuildOf(t, res)
	if u.Number != "UB-1" || u.OrderNumber == nil || *u.OrderNumber != o.Number || u.LotNumber == nil || u.MovementId == nil || len(u.Lines) != 2 {
		t.Fatalf("unbuild = %+v", u)
	}
	berry, sugar := u.Lines[0], u.Lines[1]
	if berry.Variant.Id != varBerry || berry.Quantity != "2" || berry.LotId == nil || *berry.LotId != f.b1 || berry.LotNumber == nil || *berry.LotNumber != "B-1" {
		t.Errorf("berry restored = %+v", berry)
	}
	if sugar.Variant.Id != varSugar || sugar.Quantity != "1" || sugar.LotId != nil {
		t.Errorf("sugar restored = %+v", sugar)
	}
	ps := f.h.stock.productions()
	last := ps[len(ps)-1]
	if !last.Unbuild || last.Document.Kind != "unbuild" || last.Document.ID != u.Id || len(last.Consume) != 1 ||
		last.Consume[0].LotID != f.j1 || last.Consume[0].VariantID != varJam || len(last.Produce) != 2 || last.Produce[0].LotID != f.b1 {
		t.Errorf("the unbuild's posting = %+v", last)
	}
	if got := f.h.stock.lotAt(f.b2, locMainA1); got != "6" {
		t.Errorf("B-2 holds %s after an unbuild of J1, want 6 (untouched)", got)
	}

	// The replay answers the first; the same id with another body is refused.
	res = c.Do(http.MethodPost, unbuildsPath, in)
	mustStatus(t, res, http.StatusOK, "the replay")
	if unbuildOf(t, res).Id != u.Id {
		t.Errorf("the replay answered another unbuild")
	}
	other := in
	other.Quantity = "0.5"
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, other), http.StatusConflict, "client_request_reused", "a reused request id")

	// J1 received 2; 1 is unbuilt, so 1.5 more is too much.
	more := in
	more.ClientRequestId, more.Quantity = "u2", "1.5"
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, more), http.StatusBadRequest, "unbuild_too_much", "more than the lot has left")
	more.Quantity = "0.5"
	res = c.Do(http.MethodPost, unbuildsPath, more)
	mustStatus(t, res, http.StatusCreated, "half a jar more")
	if l := unbuildOf(t, res).Lines; len(l) != 2 || l[0].Quantity != "1" || l[1].Quantity != "0.5" {
		t.Errorf("half a jar restores %+v", l)
	}

	// The lot is the order's, and named.
	none := gen.ManufacturingUnbuildInput{ClientRequestId: "u3", OrderId: &o.Id, Quantity: "1"}
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, none), http.StatusBadRequest, "lot_required", "a tracked product without its lot")
	none.LotId = &f.b1
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, none), http.StatusBadRequest, "lot_not_of_order", "a lot the order did not make")

	read := getOrder(t, c, o.Id)
	if read.Unbuilt != "1.5" {
		t.Errorf("unbuilt = %s", read.Unbuilt)
	}
	var lots gen.ManufacturingOrderLots
	res = c.Do(http.MethodGet, orderPath(o.Id, "lots"), nil)
	mustStatus(t, res, http.StatusOK, "lots")
	res.JSON(&lots)
	if lots.Tracking != gen.Lot {
		t.Errorf("the jam's tracking = %s", lots.Tracking)
	}
	for _, l := range lots.Outputs {
		want := "0"
		if l.LotId == f.j1 {
			want = "1.5"
		}
		if l.Unbuilt != want {
			t.Errorf("lot %d unbuilt %s, want %s", l.LotId, l.Unbuilt, want)
		}
	}
	var list gen.ManufacturingUnbuildList
	res = c.Do(http.MethodGet, fmt.Sprintf("%s?orderId=%d", unbuildsPath, o.Id), nil)
	mustStatus(t, res, http.StatusOK, "list")
	res.JSON(&list)
	if list.Total != 2 || len(list.Items) != 2 || list.Items[0].Number != "UB-2" {
		t.Errorf("list = %+v", list)
	}
	res = c.Do(http.MethodGet, pathOf(unbuildsPath, u.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	if got := unbuildOf(t, res); got.Note != "A dented jar" || len(got.Lines) != 2 {
		t.Errorf("get = %+v", got)
	}
	mustStatus(t, c.Do(http.MethodGet, pathOf(unbuildsPath, 999999), nil), http.StatusNotFound, "an unknown unbuild")
	// The product is snapshotted as posted; a lot that was not held
	// quarantined nothing, so there is nothing to release.
	if got := modtest.One[string](t, f.h.Harness, `SELECT sku || ' ' || product_name || ' ' || unit FROM manufacturing.unbuilds WHERE id = $1`, u.Id); got != "JAM-1 Jam pcs" {
		t.Errorf("the unbuild's product snapshot = %q", got)
	}
	if u.Quarantined || u.ReleasedAt != nil {
		t.Errorf("an unbuild of an available lot = %+v", u)
	}
	expectRefusal(t, f.h.SignIn(t, quality...).Do(http.MethodPost, pathOf(unbuildsPath, u.Id)+"/release", nil), http.StatusConflict,
		"unbuild_not_quarantined", "a release of an unbuild that quarantined nothing")
	var kinds []string
	rows, err := f.h.Pool().Query(context.Background(), `SELECT kind FROM manufacturing.order_events WHERE order_id = $1 AND kind = 'unbuilt'`, o.Id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	rows.Close()
	if len(kinds) != 2 {
		t.Errorf("unbuilt events = %v", kinds)
	}

	// Only a done order is unbuilt; only orders-manage may.
	open := confirmOrder(t, c, createOrder(t, c, orderInput(varJam, "1"))).Order
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "u4", OrderId: &open.Id, Quantity: "1", LotId: &f.j2}),
		http.StatusConflict, "order_not_open", "an open order")
	op := f.h.SignIn(t, operator...)
	mustStatus(t, op.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "u5", OrderId: &o.Id, Quantity: "1", LotId: &f.j2}),
		http.StatusForbidden, "an operator's unbuild")
}

// TestBomDepth_UnbuildOfAHeldLot is M123's held lots: taking a blocked lot
// apart is allowed — the consume line of an unbuild may take it.
func TestBomDepth_UnbuildOfAHeldLot(t *testing.T) {
	t.Parallel()
	f := newUnbuildFloor(t)
	f.h.stock.hold(f.j2, contracts.StockSource{Module: "inventory", Kind: "recall", ID: 1}, "block")
	res := f.c.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "held", OrderId: &f.o.Id, Quantity: "2", LotId: &f.j2})
	mustStatus(t, res, http.StatusCreated, "unbuild a blocked lot")
	u := unbuildOf(t, res)
	if len(u.Lines) != 2 || u.Lines[0].LotId == nil || *u.Lines[0].LotId != f.b2 || u.Lines[0].Quantity != "4" || !u.Quarantined || u.ReleasedAt != nil {
		t.Fatalf("unbuild of the blocked lot = %+v", u)
	}
	// The berries came back quarantined by the unbuild; a quality manager
	// releases its holds, and only its own.
	if got := f.h.stock.lotStatus(f.b2); got != contracts.LotQuarantine {
		t.Fatalf("the restored lot is %s", got)
	}
	release := pathOf(unbuildsPath, u.Id) + "/release"
	mustStatus(t, f.c.Do(http.MethodPost, release, nil), http.StatusForbidden, "a release without quality-manage")
	mustStatus(t, f.h.SignIn(t, quality...).Do(http.MethodPost, pathOf(unbuildsPath, 999999)+"/release", nil), http.StatusNotFound, "an unknown unbuild")
	q := f.h.SignIn(t, quality...)
	res = q.Do(http.MethodPost, release, nil)
	mustStatus(t, res, http.StatusOK, "release")
	if got := unbuildOf(t, res); got.ReleasedAt == nil || got.ReleasedByDisplay == nil {
		t.Errorf("released = %+v", got)
	}
	if got := f.h.stock.lotStatus(f.b2); got != contracts.LotAvailable {
		t.Errorf("the restored lot is %s after the release", got)
	}
	if got := f.h.stock.lotStatus(f.j2); got != contracts.LotBlocked {
		t.Errorf("the recalled lot is %s; the recall's block is not the unbuild's", got)
	}
	mustStatus(t, q.Do(http.MethodPost, release, nil), http.StatusOK, "a release done already")
}

// TestBomDepth_UnbuildCountsOutputScrap is M123's divisor: a production's
// consumption made its good output and its output scrap, so a unit taken
// apart restores what one of them consumed.
func TestBomDepth_UnbuildCountsOutputScrap(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, everything...)
	b1 := h.stock.receiveLot(varBerry, "B-1", locMainA1, "10", h.Now(), nil)
	h.stock.put(varSugar, locMainA1, "100")
	o := jamOrder(t, c, "4", "")
	berry, sugar := component(t, o, varBerry), component(t, o, varSugar)
	p := produce(t, c, o, gen.ManufacturingProductionInput{ClientRequestId: "p", Quantity: "2", OutputScrap: str("2"),
		Lines: []gen.ManufacturingProductionLineInput{
			{ComponentId: *berry.Id, Consumed: str("8"), Lots: &[]gen.ManufacturingProductionLotInput{{LotId: b1, LocationId: locMainA1, Consumed: str("8")}}},
			{ComponentId: *sugar.Id, Consumed: str("4")},
		}})
	o = closeShort(t, c, o.Id)
	lot := p.OutputLots[0].LotId
	res := c.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "u", OrderId: &o.Id, Quantity: "1", LotId: &lot})
	mustStatus(t, res, http.StatusCreated, "unbuild a jar")
	if u := unbuildOf(t, res); len(u.Lines) != 2 || u.Lines[0].Quantity != "2" || u.Lines[1].Quantity != "1" {
		t.Errorf("a jar of a production with output scrap restores %+v, want 2 berries and 1 sugar", u.Lines)
	}
}

// TestBomDepth_PartialUnbuildOfSerials is M123's serial rule: an unbuild from
// an order that would restore a fraction of a serial component is refused
// naming it; whole units restore each serial whole.
func TestBomDepth_PartialUnbuildOfSerials(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, everything...)
	p1 := h.stock.receiveLot(varPump, "P-1", locMainA1, "1", h.Now(), nil)
	p2 := h.stock.receiveLot(varPump, "P-2", locMainA1, "1", h.Now(), nil)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varBike, "1", line(varPump, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "1")}
	createBom(t, c, in)
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "2"))).Order
	pump := component(t, o, varPump)
	produce(t, c, o, gen.ManufacturingProductionInput{ClientRequestId: "p", Quantity: "2",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *pump.Id, Consumed: str("2"), Lots: &[]gen.ManufacturingProductionLotInput{
			{LotId: p1, LocationId: locMainA1, Consumed: str("1")}, {LotId: p2, LocationId: locMainA1, Consumed: str("1")}}}}})
	o = closeShort(t, c, o.Id)
	p := expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "half", OrderId: &o.Id, Quantity: "1"}),
		http.StatusBadRequest, "serial_quantity", "half of each serial")
	if p.Errors == nil || len((*p.Errors)["quantity"]) != 1 || !strings.Contains((*p.Errors)["quantity"][0], "PUMP-1") {
		t.Errorf("the refusal does not name the component: %+v", p.Errors)
	}
	res := c.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "whole", OrderId: &o.Id, Quantity: "2"})
	mustStatus(t, res, http.StatusCreated, "both bicycles")
	if u := unbuildOf(t, res); len(u.Lines) != 2 || u.Lines[0].Quantity != "1" || u.Lines[1].Quantity != "1" {
		t.Errorf("whole units restore %+v", u.Lines)
	}
}

// TestBomDepth_FreeStandingUnbuild is M123 without an order: the BoM's lines
// without scrap for the quantity; a tracked component needs its lot named,
// an untracked one none.
func TestBomDepth_FreeStandingUnbuild(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, everything...)
	b := bikeBom(t, c)
	h.stock.put(varBike, locMainRoot, "5")
	res := c.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "bike", BomId: &b.Id, Quantity: "2"})
	mustStatus(t, res, http.StatusCreated, "a free-standing unbuild")
	u := unbuildOf(t, res)
	if u.OrderId != nil || u.SiteId != siteMain || u.FromLocationId != locMainRoot || len(u.Lines) != 2 ||
		u.Lines[0].Quantity != "4" || u.Lines[1].Quantity != "6" {
		t.Fatalf("unbuild = %+v", u)
	}
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, gen.ManufacturingUnbuildInput{ClientRequestId: "both", BomId: &b.Id, OrderId: &b.Id, Quantity: "1"}),
		http.StatusBadRequest, "", "an order and a BoM")

	jam := createBom(t, c, bomInput(varJam, "1", line(varBerry, "2"), line(varSugar, "1")))
	lot := h.stock.receiveLot(varJam, "J-9", locMainRoot, "3", h.Now(), nil)
	main := siteMain
	in := gen.ManufacturingUnbuildInput{ClientRequestId: "jam", BomId: &jam.Id, Quantity: "1", LotId: &lot, SiteId: &main}
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, in), http.StatusBadRequest, "lot_required", "a tracked component without its lot")
	in.Components = &[]gen.ManufacturingUnbuildComponentInput{{VariantId: varBerry, Lot: &gen.ManufacturingOutputLotInput{Number: str("R-1")}},
		{VariantId: varSugar, LotId: &lot}}
	expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, in), http.StatusBadRequest, "lot_not_allowed", "a lot for an untracked component")
	in.Components = &[]gen.ManufacturingUnbuildComponentInput{{VariantId: varBerry, Lot: &gen.ManufacturingOutputLotInput{Number: str("R-1")}},
		{VariantId: varPump, Lot: &gen.ManufacturingOutputLotInput{Number: str("X-1")}}}
	np := expectRefusal(t, c.Do(http.MethodPost, unbuildsPath, in), http.StatusBadRequest, "", "a component the BoM does not have")
	if np.Errors == nil || len((*np.Errors)["components[1].variantId"]) != 1 {
		t.Errorf("a component the BoM does not have: errors %+v", np.Errors)
	}
	in.Components = &[]gen.ManufacturingUnbuildComponentInput{{VariantId: varBerry, Lot: &gen.ManufacturingOutputLotInput{Number: str("R-1")}}}
	res = c.Do(http.MethodPost, unbuildsPath, in)
	mustStatus(t, res, http.StatusCreated, "a free-standing unbuild into a new lot")
	u = unbuildOf(t, res)
	if len(u.Lines) != 2 || u.Lines[0].LotNumber == nil || *u.Lines[0].LotNumber != "R-1" || u.Lines[0].Quantity != "2" || u.LotNumber == nil || *u.LotNumber != "J-9" {
		t.Errorf("unbuild = %+v", u)
	}
}

// splitFloor is a bicycle made at SAW with a paint by-product and stock
// enough, on a pool of poolSize connections (0 for the default).
func splitFloor(t *testing.T, poolSize int32) (*harness, *modtest.Client, gen.ManufacturingWorkCenter) {
	t.Helper()
	opts := []modtest.Option{}
	if poolSize > 0 {
		opts = append(opts, modtest.WithPoolMaxConns(poolSize))
	}
	h := newHarness(t, opts...)
	c := h.SignIn(t, everything...)
	h.stock.put(varWheel, locMainA1, "1000")
	h.stock.put(varBolt, locMainA1, "1000")
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varBike, "1", line(varWheel, "2"), line(varBolt, "3"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "6")}
	in.Outputs = &[]gen.ManufacturingBomOutputInput{output(varPaint, "1", "5")}
	createBom(t, c, in)
	return h, c, saw
}

func splitOf(t *testing.T, res *modtest.Response) gen.ManufacturingOrderSplitResult {
	t.Helper()
	var out gen.ManufacturingOrderSplitResult
	res.JSON(&out)
	return out
}

// TestBomDepth_Split is M124, M126 and M128: a planned confirmed order split
// into 4 and 6 is unplanned, keeps 4 and its lines and work orders scaled
// (expected minutes recomputed), and a new part MO-n-2 split from it carries
// 6 with its own lines, by-products and work orders, reserved again; a split
// that does not sum, and one of an order with a time log, are refused; a
// draft splits into drafts.
func TestBomDepth_Split(t *testing.T) {
	t.Parallel()
	h, c, _ := splitFloor(t, 0)
	o, wos := confirmedBike(t, c, "10")
	if wos[0].ExpectedMinutes != 60 {
		t.Fatalf("expected = %d", wos[0].ExpectedMinutes)
	}
	o = planOrder(t, c, o)
	if o.PlannedStart == nil {
		t.Fatal("not planned")
	}
	wheel := component(t, o, varWheel)
	if h.stock.reservedFor(o.Id, *wheel.Id) != "20" {
		t.Fatalf("reserved before = %s", h.stock.reservedFor(o.Id, *wheel.Id))
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: o.Revision, Quantities: []string{"4", "5"}}),
		http.StatusBadRequest, "split_sum", "parts not summing")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: o.Revision, Quantities: []string{"10"}}),
		http.StatusBadRequest, "", "one part")

	res := c.Do(http.MethodPost, orderPath(o.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: o.Revision, Quantities: []string{"4", "6"}})
	mustStatus(t, res, http.StatusOK, "split")
	out := splitOf(t, res)
	kept := out.Order
	if kept.Quantity != "4" || kept.PlannedStart != nil || len(out.Parts) != 1 || len(kept.SplitParts) != 1 || component(t, kept, varWheel).Required != "8" {
		t.Fatalf("kept = %+v, parts %+v", kept, out.Parts)
	}
	if w := workOrdersOf(t, c, kept.Id); w[0].ExpectedMinutes != 24 || w[0].PlannedStart != nil {
		t.Errorf("kept work order = %+v", w[0])
	}
	if h.stock.reservedFor(o.Id, *wheel.Id) != "8" {
		t.Errorf("kept reserves %s wheels, want 8", h.stock.reservedFor(o.Id, *wheel.Id))
	}
	part := getOrder(t, c, out.Parts[0].Id)
	if part.Number != o.Number+"-2" || part.SplitFromId == nil || *part.SplitFromId != o.Id || part.SplitFrom == nil || part.Status != gen.ManufacturingOrderStatusConfirmed ||
		part.Quantity != "6" || part.PlannedStart != nil || component(t, part, varWheel).Required != "12" || orderOutput(t, part, varPaint).Expected != "6" {
		t.Fatalf("part = %+v", part)
	}
	pw := workOrdersOf(t, c, part.Id)
	if len(pw) != 1 || pw[0].ExpectedMinutes != 36 || pw[0].Status != gen.ManufacturingWorkOrderStatusReady {
		t.Errorf("part's work orders = %+v", pw)
	}
	if got := h.stock.reservedFor(part.Id, *component(t, part, varWheel).Id); got != "12" {
		t.Errorf("the part reserves %s wheels, want 12", got)
	}
	if orderOutput(t, kept, varPaint).Expected != "4" {
		t.Errorf("kept by-product = %+v", kept.Outputs)
	}
	// The original is not cancelled while its part is open.
	kept = getOrder(t, c, kept.Id)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(kept.Id, "cancel"), revisionInput(kept.Revision)), http.StatusConflict, "order_has_open_parts",
		"a cancel of a split original with an open part")

	// A draft splits into drafts; an order with a time log does not split.
	draft := createOrder(t, c, orderInput(varBike, "3"))
	res = c.Do(http.MethodPost, orderPath(draft.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: draft.Revision, Quantities: []string{"1", "1", "1"}})
	mustStatus(t, res, http.StatusOK, "split a draft")
	if d := splitOf(t, res); len(d.Parts) != 2 || d.Parts[0].Status != gen.ManufacturingOrderStatusDraft || d.Order.Quantity != "1" {
		t.Errorf("draft split = %+v", d.Parts)
	}
	act(t, c, pw[0].Id, "start", nil)
	part = getOrder(t, c, part.Id)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(part.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: part.Revision, Quantities: []string{"3", "3"}}),
		http.StatusConflict, "order_has_work", "a split of an order with a time log")
}

// TestBomDepth_SplitTakesTheScheduleLock is M127: a split of a planned order
// waits for its work center's schedule lock.
func TestBomDepth_SplitTakesTheScheduleLock(t *testing.T) {
	t.Parallel()
	h, c, saw := splitFloor(t, 4)
	o, _ := confirmedBike(t, c, "10")
	o = planOrder(t, c, o)
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(19783, $1::int)", saw.Id); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- c.Do(http.MethodPost, orderPath(o.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: o.Revision, Quantities: []string{"5", "5"}}).Status
	}()
	select {
	case st := <-done:
		t.Fatalf("the split answered %d while SAW's lock was held", st)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if st := <-done; st != http.StatusOK {
		t.Fatalf("the split answered %d", st)
	}
}

func mergeOf(t *testing.T, res *modtest.Response) gen.ManufacturingOrderMergeResult {
	t.Helper()
	var out gen.ManufacturingOrderMergeResult
	res.JSON(&out)
	return out
}

// TestBomDepth_Merge is M125: two confirmed planned orders merge into the one
// with the lowest number, with the summed quantity and the earliest due date;
// the other is cancelled naming it; the survivor is unplanned, its lines and
// work orders taken for the sum and reserved again, the other's reservations
// released. A draft with a confirmed order, and mixed origins, are refused.
func TestBomDepth_Merge(t *testing.T) {
	t.Parallel()
	h, c, _ := splitFloor(t, 0)
	early := openapi_types.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	a, _ := confirmedBike(t, c, "3")
	bIn := orderInput(varBike, "2")
	bIn.DueDate = &early
	b := confirmOrder(t, c, createOrder(t, c, bIn)).Order
	a = planOrder(t, c, a)
	bWheel := component(t, b, varWheel)
	res := c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
		{Id: b.Id, Revision: b.Revision}, {Id: a.Id, Revision: a.Revision}}})
	mustStatus(t, res, http.StatusOK, "merge")
	out := mergeOf(t, res)
	s := out.Order
	if s.Id != a.Id || s.Quantity != "5" || s.DueDate == nil || !s.DueDate.Equal(early.Time) || s.PlannedStart != nil ||
		component(t, s, varWheel).Required != "10" || len(out.Merged) != 1 || out.Merged[0].Id != b.Id || len(s.MergedFrom) != 1 ||
		orderOutput(t, s, varPaint).Expected != "5" {
		t.Fatalf("survivor = %+v", s)
	}
	if w := workOrdersOf(t, c, s.Id); w[0].ExpectedMinutes != 30 {
		t.Errorf("survivor's expected = %d", w[0].ExpectedMinutes)
	}
	gone := getOrder(t, c, b.Id)
	if gone.Status != gen.ManufacturingOrderStatusCancelled || gone.MergedIntoId == nil || *gone.MergedIntoId != a.Id || gone.MergedInto == nil {
		t.Errorf("merged = %+v", gone)
	}
	if got := h.stock.reservedFor(b.Id, *bWheel.Id); got != "0" {
		t.Errorf("the merged order still reserves %s", got)
	}
	if got := h.stock.reservedFor(a.Id, *component(t, s, varWheel).Id); got != "10" {
		t.Errorf("the survivor reserves %s, want 10", got)
	}

	d := createOrder(t, c, orderInput(varBike, "1"))
	s = getOrder(t, c, s.Id)
	expectRefusal(t, c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
		{Id: s.Id, Revision: s.Revision}, {Id: d.Id, Revision: d.Revision}}}), http.StatusBadRequest, "merge_mismatch", "a draft with a confirmed order")
	expectRefusal(t, c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
		{Id: s.Id, Revision: s.Revision}, {Id: gone.Id, Revision: gone.Revision}}}), http.StatusConflict, "order_not_open", "a cancelled order")
}

// TestBomDepth_MtoSplitAndMixedOrigins is M124's amended index and M125's
// origins: a make-to-order order split leaves the line one live order and
// its parts its chain, listed for the line; merging it with a manual order
// is merge_mismatch; its own parts merge back.
func TestBomDepth_MtoSplitAndMixedOrigins(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, everything...)
	h.stock.put(varWheel, locMainA1, "1000")
	h.stock.put(varBolt, locMainA1, "1000")
	bikeBom(t, c)
	lines.seed(contracts.SalesOrderLine{LineID: 1001, OrderID: 92, OrderNumber: 6, VariantID: varBike, Quantity: "4", OrderStatus: "confirmed"})
	in := orderInput(varBike, "4")
	line := int64(1001)
	in.SalesOrderLineId = &line
	mto := confirmOrder(t, c, createOrder(t, c, in)).Order
	res := c.Do(http.MethodPost, orderPath(mto.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: mto.Revision, Quantities: []string{"1", "3"}})
	mustStatus(t, res, http.StatusOK, "split a make-to-order order")
	part := getOrder(t, c, splitOf(t, res).Parts[0].Id)
	if part.Origin.Kind != gen.ManufacturingOriginSalesOrder || part.Origin.LineId == nil || *part.Origin.LineId != line {
		t.Fatalf("part origin = %+v", part.Origin)
	}
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, in), http.StatusConflict, "sales_order_line_has_order", "another order for the line")
	var l gen.ManufacturingSalesOrderLine
	res = c.Do(http.MethodGet, base+"/sales-order-lines/1001", nil)
	mustStatus(t, res, http.StatusOK, "the line")
	res.JSON(&l)
	if len(l.Orders) != 2 {
		t.Errorf("the line's orders = %+v", l.Orders)
	}
	manual := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order
	mto = getOrder(t, c, mto.Id)
	expectRefusal(t, c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
		{Id: mto.Id, Revision: mto.Revision}, {Id: manual.Id, Revision: manual.Revision}}}), http.StatusBadRequest, "merge_mismatch", "mixed origins")
	res = c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
		{Id: part.Id, Revision: part.Revision}, {Id: mto.Id, Revision: mto.Revision}}})
	mustStatus(t, res, http.StatusOK, "merge the parts back")
	if s := mergeOf(t, res).Order; s.Id != mto.Id || s.Quantity != "4" {
		t.Errorf("merged back = %+v", s)
	}
}

// TestBomDepth_ConcurrentSplitsMergesAndPlans is M127 under load, on a pool
// of four: splits, merges and plans on one work center at once all go
// through, and the board books no time twice.
func TestBomDepth_ConcurrentSplitsMergesAndPlans(t *testing.T) {
	t.Parallel()
	h, c, _ := splitFloor(t, 4)
	_ = h
	var orders []gen.ManufacturingOrder
	for range 8 {
		o, _ := confirmedBike(t, c, "4")
		orders = append(orders, planOrder(t, c, o))
	}
	var wg sync.WaitGroup
	statuses := make([]int, 6)
	wg.Go(func() {
		o := getOrder(t, c, orders[0].Id)
		statuses[0] = c.Do(http.MethodPost, orderPath(o.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: o.Revision, Quantities: []string{"1", "3"}}).Status
	})
	wg.Go(func() {
		o := getOrder(t, c, orders[1].Id)
		statuses[1] = c.Do(http.MethodPost, orderPath(o.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: o.Revision, Quantities: []string{"2", "2"}}).Status
	})
	wg.Go(func() {
		a, b := getOrder(t, c, orders[2].Id), getOrder(t, c, orders[3].Id)
		statuses[2] = c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
			{Id: a.Id, Revision: a.Revision}, {Id: b.Id, Revision: b.Revision}}}).Status
	})
	wg.Go(func() {
		a, b := getOrder(t, c, orders[4].Id), getOrder(t, c, orders[5].Id)
		statuses[3] = c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
			{Id: b.Id, Revision: b.Revision}, {Id: a.Id, Revision: a.Revision}}}).Status
	})
	for i, o := range orders[6:] {
		wg.Go(func() {
			x := getOrder(t, c, o.Id)
			statuses[4+i] = c.Do(http.MethodPost, orderPath(x.Id, "unplan"), revisionInput(x.Revision)).Status
		})
	}
	wg.Wait()
	for i, st := range statuses {
		if st != http.StatusOK {
			t.Fatalf("write %d answered %d", i, st)
		}
	}
	res := c.Do(http.MethodPost, base+"/schedule/plan-all", gen.ManufacturingPlanAllInput{SiteId: siteMain})
	mustStatus(t, res, http.StatusOK, "plan all")
	s := scheduleOf(t, c, fmt.Sprintf("siteId=%d&from=2026-09-14&days=14", siteMain))
	for _, row := range s.WorkCenters {
		for i, a := range row.Bars {
			for _, b := range row.Bars[i+1:] {
				if a.StartsAt.Before(b.EndsAt) && b.StartsAt.Before(a.EndsAt) {
					t.Errorf("%s: %s overlaps %s", row.WorkCenter.Code, a.OrderNumber, b.OrderNumber)
				}
			}
		}
	}
}

// TestBomDepth_SplitWithRegisteredQuantity is the review's D1: a registered
// quantity is work done, so an order with one is neither split nor merged.
func TestBomDepth_SplitWithRegisteredQuantity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	a := operation("a", 10, saw.Id, "1")
	a.SendAheadQuantity = str("2")
	bikeWith(t, c, a, operation("b", 20, saw.Id, "1"))
	o, wos := confirmedBike(t, c, "10")
	mustStatus(t, c.Do(http.MethodPost, workOrderPath(wos[0].Id, "quantity"), gen.ManufacturingQuantityInput{ClientRequestId: uuid.New(), Quantity: "2"}),
		http.StatusOK, "two through")
	o = getOrder(t, c, o.Id)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "split"), gen.ManufacturingOrderSplitInput{Revision: o.Revision, Quantities: []string{"5", "5"}}),
		http.StatusConflict, "order_has_work", "a split of an order with a registered quantity")
	other, _ := confirmedBike(t, c, "1")
	expectRefusal(t, c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
		{Id: o.Id, Revision: o.Revision}, {Id: other.Id, Revision: other.Revision}}}), http.StatusConflict, "order_has_work",
		"a merge of an order with a registered quantity")
}

// TestBomDepth_MergeMatchesLinesInOrder is M125's matching: two BoM lines of
// one variant and operation are matched the n-th to the n-th, by sort order.
func TestBomDepth_MergeMatchesLinesInOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, everything...)
	h.stock.put(varWheel, locMainA1, "1000")
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varBike, "1", line(varWheel, "2"), line(varWheel, "3"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "1")}
	createBom(t, c, in)
	a, _ := confirmedBike(t, c, "1")
	b, _ := confirmedBike(t, c, "1")
	// The list carries each order's revision, which a merge chosen from it
	// sends.
	var list gen.ManufacturingOrderPage
	res := c.Do(http.MethodGet, ordersPath, nil)
	mustStatus(t, res, http.StatusOK, "the list")
	res.JSON(&list)
	for _, o := range list.Items {
		if o.Id == a.Id && o.Revision != a.Revision {
			t.Errorf("the list's revision of %s = %d, want %d", o.Number, o.Revision, a.Revision)
		}
	}
	res = c.Do(http.MethodPost, ordersPath+"/merge", gen.ManufacturingOrderMergeInput{Orders: []gen.ManufacturingOrderMergeItem{
		{Id: a.Id, Revision: a.Revision}, {Id: b.Id, Revision: b.Revision}}})
	mustStatus(t, res, http.StatusOK, "merge")
	s := mergeOf(t, res).Order
	var required []string
	for _, l := range s.Components {
		required = append(required, l.Required)
	}
	if len(required) != 2 || required[0] != "4" || required[1] != "6" {
		t.Errorf("the survivor's wheel lines = %v, want [4 6]", required)
	}
}
