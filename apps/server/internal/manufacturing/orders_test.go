package manufacturing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const ordersPath = base + "/orders"

func orderPath(id int64, rest ...string) string { return pathOf(ordersPath, id, rest...) }

// planner may create and run orders; operator only registers production.
var (
	planner  = []string{"manufacturing:access", "manufacturing:boms-manage", "manufacturing:orders-manage"}
	operator = []string{"manufacturing:access", "manufacturing:shop-floor"}
)

func orderOf(t *testing.T, res *modtest.Response) gen.ManufacturingOrder {
	t.Helper()
	var o gen.ManufacturingOrder
	res.JSON(&o)
	return o
}

func createOrder(t *testing.T, c *modtest.Client, in gen.ManufacturingOrderInput) gen.ManufacturingOrder {
	t.Helper()
	res := c.Do(http.MethodPost, ordersPath, in)
	mustStatus(t, res, http.StatusCreated, "create order")
	return orderOf(t, res)
}

func getOrder(t *testing.T, c *modtest.Client, id int64) gen.ManufacturingOrder {
	t.Helper()
	res := c.Do(http.MethodGet, orderPath(id), nil)
	mustStatus(t, res, http.StatusOK, "get order")
	return orderOf(t, res)
}

func confirmOrder(t *testing.T, c *modtest.Client, o gen.ManufacturingOrder) gen.ManufacturingOrderWithShortLines {
	t.Helper()
	res := c.Do(http.MethodPost, orderPath(o.Id, "confirm"), revisionInput(o.Revision))
	mustStatus(t, res, http.StatusOK, "confirm "+o.Number)
	var out gen.ManufacturingOrderWithShortLines
	res.JSON(&out)
	return out
}

// bikeBom is the fixture: a bicycle of two wheels and three bolts per
// output of 1, the bolts with 10 % scrap, made at the main site's SAW.
func bikeBom(t *testing.T, c *modtest.Client) gen.ManufacturingBom {
	t.Helper()
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varBike, "1", line(varWheel, "2"), line(varBolt, "3"))
	in.Lines[1].ScrapPercent = str("10")
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "5")}
	in.Lines[0].OperationKey = str("a")
	return createBom(t, c, in)
}

func orderInput(variant int32, qty string) gen.ManufacturingOrderInput {
	return gen.ManufacturingOrderInput{VariantId: variant, Quantity: qty}
}

func component(t *testing.T, o gen.ManufacturingOrder, variant int32) gen.ManufacturingOrderComponent {
	t.Helper()
	for _, c := range o.Components {
		if c.Variant.Id == variant {
			return c
		}
	}
	t.Fatalf("%s has no line of variant %d: %+v", o.Number, variant, o.Components)
	return gen.ManufacturingOrderComponent{}
}

// TestOrder_CreateDefaultsAndRefusals is M17, M17a and M17b's create: the
// default BoM, the site of its work centers, the site's root as the output
// location, MO-<n> from the counter, a draft following its BoM, and the
// create's refusals.
func TestOrder_CreateDefaultsAndRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	b := bikeBom(t, c)

	due := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	in := orderInput(varBike, "2")
	in.DueDate = dateOf(due)
	in.Note = str(" Rush ")
	o := createOrder(t, c, in)
	if o.Number != "MO-1" || o.BomId != b.Id || o.SiteId != siteMain || o.OutputLocationId != locMainRoot || o.ComponentLocationId != nil ||
		o.Status != gen.ManufacturingOrderStatusDraft || o.Priority != gen.ManufacturingPriorityNormal || o.Note != "Rush" ||
		o.Origin.Kind != gen.ManufacturingOriginManual || o.Quantity != "2" || o.Produced != "0" || o.Revision != 1 || o.ReservationStatus != nil {
		t.Fatalf("created = %+v", o)
	}
	// A draft follows its BoM: 3 × 2 / 1 × 1.1 = 6.6 bolts, no id yet.
	if bolt := component(t, o, varBolt); bolt.Id != nil || bolt.Required != "6.6" || bolt.Reserved != "0" {
		t.Errorf("draft bolt line = %+v", bolt)
	}
	if second := createOrder(t, c, orderInput(varBike, "1")); second.Number != "MO-2" {
		t.Errorf("second number = %s", second.Number)
	}

	expectRefusal(t, c.Do(http.MethodPost, ordersPath, orderInput(varWheel, "1")), http.StatusBadRequest, "bom_required", "a variant with no BoM")
	other := orderInput(varBike, "1")
	other.SiteId = &[]int64{siteBranch}[0]
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, other), http.StatusBadRequest, "work_center_other_site", "another site than the operations")
	archived := orderInput(varBike, "1")
	archived.ComponentLocationId = &[]int64{locMainOld}[0]
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, archived), http.StatusBadRequest, "location_unknown", "an archived location")
	foreign := orderInput(varBike, "1")
	foreign.OutputLocationId = &[]int64{locBranchRoot}[0]
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, foreign), http.StatusBadRequest, "location_unknown", "a location of another site")
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, orderInput(varBike, "1.0001")), http.StatusBadRequest, "quantity_invalid", "four decimals")
	mustStatus(t, h.SignIn(t, reader...).Do(http.MethodPost, ordersPath, orderInput(varBike, "1")), http.StatusForbidden, "create as a reader")
	mustStatus(t, c.Do(http.MethodGet, orderPath(999999), nil), http.StatusNotFound, "get unknown")

	var locations gen.ManufacturingLocationList
	res := c.Do(http.MethodGet, fmt.Sprintf("%s/sites/%d/locations", base, siteMain), nil)
	mustStatus(t, res, http.StatusOK, "site locations")
	res.JSON(&locations)
	if len(locations.Items) != 4 || !locations.Items[0].Root || locations.Items[3].Active {
		t.Errorf("site locations = %+v", locations.Items)
	}
	mustStatus(t, c.Do(http.MethodGet, base+"/people?q=", nil), http.StatusOK, "people")
}

func dateOf(t time.Time) *openapi_types.Date {
	d := openapi_types.Date{Time: t}
	return &d
}

// TestOrder_ConfirmSnapshotScrapAndShortLine is M18's rounding up, M20's
// confirm with a short line left unreserved and named, M20a's two passes and
// Reserve now, M21a's availability on the page and the list's reservation
// status.
func TestOrder_ConfirmSnapshotScrapAndShortLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	saw := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varBike, "3", line(varWheel, "2"), line(varBolt, "1"))
	in.Lines[1].ScrapPercent = str("10")
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, saw.Id, "5")}
	createBom(t, c, in)
	h.stock.put(varWheel, locMainA1, "10")
	h.stock.put(varBolt, locMainA1, "0.5")

	o := createOrder(t, c, orderInput(varBike, "2"))
	out := confirmOrder(t, c, o)
	got := out.Order
	wheel, bolt := component(t, got, varWheel), component(t, got, varBolt)
	// 1 × 2 / 3 × 1.1 = 0.7333… rounds UP to 0.734; 2 × 2 / 3 = 1.333… to 1.334.
	if bolt.Required != "0.734" || wheel.Required != "1.334" {
		t.Fatalf("required wheel %s bolt %s, want 1.334 and 0.734", wheel.Required, bolt.Required)
	}
	if got.Status != gen.ManufacturingOrderStatusConfirmed || got.ConfirmedAt == nil {
		t.Errorf("status = %s", got.Status)
	}
	if len(out.ShortLines) != 1 || out.ShortLines[0].ComponentId != *bolt.Id || out.ShortLines[0].Missing != "0.734" {
		t.Fatalf("short lines = %+v", out.ShortLines)
	}
	if wheel.Reserved != "1.334" || h.stock.reservedFor(got.Id, *wheel.Id) != "1.334" || bolt.Reserved != "0" {
		t.Errorf("reserved wheel %s bolt %s", wheel.Reserved, bolt.Reserved)
	}
	// The bolt line: not reserved, 0.5 available covers part of it.
	if bolt.Available != "0.5" || bolt.Missing != "0.234" || bolt.Covered || !wheel.Covered ||
		got.Availability != gen.ManufacturingAvailabilityPartlyAvailable || *got.ReservationStatus != gen.ManufacturingReservationStatusPartlyReserved {
		t.Errorf("availability = %s, bolt %+v, reservation %v", got.Availability, bolt, got.ReservationStatus)
	}
	// A later BoM edit never reaches a confirmed order (M17).
	expectRefusal(t, c.Do(http.MethodPost, orderPath(got.Id, "confirm"), revisionInput(got.Revision)), http.StatusConflict, "order_not_draft", "confirm twice")

	var page gen.ManufacturingOrderPage
	c.Do(http.MethodGet, ordersPath+"?reservation=partly_reserved", nil).JSON(&page)
	if page.Total != 1 || page.Items[0].Id != got.Id {
		t.Errorf("partly reserved list = %+v", page)
	}

	h.stock.put(varBolt, locMainA2, "1")
	res := c.Do(http.MethodPost, orderPath(got.Id, "reserve"), revisionInput(got.Revision))
	mustStatus(t, res, http.StatusOK, "reserve now")
	var again gen.ManufacturingOrderWithShortLines
	res.JSON(&again)
	if len(again.ShortLines) != 0 || component(t, again.Order, varBolt).Reserved != "0.734" ||
		again.Order.Availability != gen.ManufacturingAvailabilityReady || *again.Order.ReservationStatus != gen.ManufacturingReservationStatusReserved {
		t.Errorf("after reserve now = %+v", again)
	}
	c.Do(http.MethodGet, ordersPath+"?reservation=reserved", nil).JSON(&page)
	if page.Total != 1 {
		t.Errorf("reserved list = %+v", page)
	}
}

// TestOrder_UpdateRules is M19's editing: a draft changes everything, a
// confirmed order only its dates, priority, responsible, note and locations.
func TestOrder_UpdateRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, me := h.SignInUser(t, planner...)
	b := bikeBom(t, c)
	o := createOrder(t, c, orderInput(varBike, "2"))
	upd := gen.ManufacturingOrderUpdate{Revision: o.Revision, BomId: b.Id, Quantity: "5", SiteId: siteMain, Priority: gen.ManufacturingPriorityUrgent,
		ResponsibleUserId: &me, ComponentLocationId: &[]int64{locMainA1}[0]}
	res := c.Do(http.MethodPut, orderPath(o.Id), upd)
	mustStatus(t, res, http.StatusOK, "update a draft")
	o = orderOf(t, res)
	if o.Quantity != "5" || o.Priority != gen.ManufacturingPriorityUrgent || o.Responsible == nil || o.Responsible.Id != me ||
		*o.ComponentLocationId != locMainA1 || o.OutputLocationId != locMainRoot || o.Revision != 2 {
		t.Fatalf("updated = %+v", o)
	}
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), upd), http.StatusConflict, "revision_conflict", "a stale revision")

	o = confirmOrder(t, c, o).Order
	upd.Revision, upd.Quantity = o.Revision, "6"
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), upd), http.StatusConflict, "order_not_draft", "a confirmed order's quantity")
	upd.Quantity, upd.Note = "5", str("Second shift")
	upd.DueDate = dateOf(time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC))
	res = c.Do(http.MethodPut, orderPath(o.Id), upd)
	mustStatus(t, res, http.StatusOK, "update a confirmed order's dates and note")
	if o = orderOf(t, res); o.Note != "Second shift" || o.DueDate == nil {
		t.Errorf("confirmed update = %+v", o)
	}
}

// TestOrder_ComponentLines is M18 and M18a: an extra line added to a
// confirmed order is reserved; a requirement is never lowered below what is
// consumed and scrapped; lowering it releases the excess.
func TestOrder_ComponentLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	h.stock.put(varTube, locMainA1, "3")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "2"))).Order

	res := c.Do(http.MethodPost, orderPath(o.Id, "components"), gen.ManufacturingComponentInput{Revision: o.Revision, VariantId: varTube, Required: "2"})
	mustStatus(t, res, http.StatusOK, "add a component")
	var out gen.ManufacturingOrderWithShortLines
	res.JSON(&out)
	tube := component(t, out.Order, varTube)
	if !tube.Added || tube.Reserved != "2" || len(out.ShortLines) != 0 {
		t.Fatalf("added tube = %+v, short %+v", tube, out.ShortLines)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "components"), gen.ManufacturingComponentInput{Revision: out.Order.Revision,
		VariantId: varService, Required: "1"}), http.StatusBadRequest, "variant_not_goods", "a service")

	post := gen.ManufacturingProductionInput{ClientRequestId: "p1", Quantity: "0",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *tube.Id, Consumed: str("1.5")}}}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "consume tubes")
	o = getOrder(t, c, o.Id)
	path := fmt.Sprintf("%s/components/%d", orderPath(o.Id), *tube.Id)
	expectRefusal(t, c.Do(http.MethodPut, path, gen.ManufacturingComponentUpdate{Revision: o.Revision, Required: "1"}),
		http.StatusBadRequest, "required_below_used", "below what was used")
	res = c.Do(http.MethodPut, path, gen.ManufacturingComponentUpdate{Revision: o.Revision, Required: "1.6"})
	mustStatus(t, res, http.StatusOK, "lower the requirement")
	if got := h.stock.reservedFor(o.Id, *tube.Id); got != "0.1" {
		t.Errorf("tube reservation after lowering = %s, want 0.1", got)
	}
	mustStatus(t, c.Do(http.MethodPut, fmt.Sprintf("%s/components/999999", orderPath(o.Id)), gen.ManufacturingComponentUpdate{Revision: o.Revision, Required: "1"}),
		http.StatusNotFound, "an unknown line")
}

// TestProduction_PrefillDeviationAndReplay is M22 and M22a: the prefill on
// quantity + outputScrap, the consumption warning and acceptDeviation, the
// totals and the state, output scrap kept out of produced, and the
// clientRequestId's replay and reuse.
func TestProduction_PrefillDeviationAndReplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "10"))).Order
	wheel, bolt := component(t, o, varWheel), component(t, o, varBolt) // 20 and 33

	var draft gen.ManufacturingProductionDraft
	res := c.Do(http.MethodGet, orderPath(o.Id, "production-draft")+"?quantity=3&outputScrap=1", nil)
	mustStatus(t, res, http.StatusOK, "prefill")
	res.JSON(&draft)
	// 20 × 4 / 10 = 8; 33 × 4 / 10 = 13.2.
	if draft.Quantity != "3" || draft.OutputScrap != "1" || draft.ToLocationId != locMainRoot || len(draft.Lines) != 2 ||
		draft.Lines[0].Proposed != "8" || draft.Lines[1].Proposed != "13.2" {
		t.Fatalf("prefill = %+v", draft)
	}
	c.Do(http.MethodGet, orderPath(o.Id, "production-draft"), nil).JSON(&draft)
	if draft.Quantity != "10" || draft.Lines[0].Proposed != "20" {
		t.Errorf("default prefill = %+v", draft)
	}
	expectRefusal(t, c.Do(http.MethodGet, orderPath(o.Id, "production-draft")+"?quantity=-1", nil), http.StatusBadRequest, "quantity_invalid", "a negative prefill")

	post := gen.ManufacturingProductionInput{ClientRequestId: "run-1", Quantity: "3", OutputScrap: str("1"),
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Consumed: str("8.8")}, {ComponentId: *bolt.Id, Consumed: str("15"), Scrap: str("1")}}}
	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusConflict, "consumption_differs", "15 bolts for 13.2")
	// 8.8 is 10 % off 8 — not more — so only the bolts deviate.
	if p.Deviations == nil || len(*p.Deviations) != 1 || (*p.Deviations)[0].ComponentId != *bolt.Id || (*p.Deviations)[0].Expected != "13.2" {
		t.Fatalf("deviations = %+v", p.Deviations)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.productions`); n != 0 {
		t.Fatalf("a warned production stored %d rows", n)
	}

	post.AcceptDeviation = &[]bool{true}[0]
	res = c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusCreated, "post with the deviation accepted")
	var first gen.ManufacturingProduction
	res.JSON(&first)
	if first.OrderId != o.Id || first.OrderNumber != o.Number || first.Quantity != "3" || first.OutputScrap != "1" || first.MovementId == nil ||
		len(first.Lines) != 2 || first.PostedByDisplay == "" {
		t.Fatalf("production = %+v", first)
	}
	o = getOrder(t, c, o.Id)
	wheel, bolt = component(t, o, varWheel), component(t, o, varBolt)
	if o.Status != gen.ManufacturingOrderStatusInProgress || o.Produced != "3" || o.OutputScrap != "1" ||
		wheel.Consumed != "8.8" || bolt.Consumed != "15" || bolt.Scrapped != "1" || bolt.Reserved != "17" || wheel.Reserved != "11.2" {
		t.Fatalf("after the production = %+v", o)
	}
	if h.stock.held(varBike, locMainRoot) != "3" || h.stock.held(varBolt, locMainA1) != "84" {
		t.Errorf("stock after = bikes %s bolts %s", h.stock.held(varBike, locMainRoot), h.stock.held(varBolt, locMainA1))
	}
	if ps := h.stock.productions(); len(ps) != 1 || ps[0].Document != (contracts.StockSource{Module: "manufacturing", Kind: "production", ID: first.Id}) {
		t.Errorf("posted = %+v", ps)
	}
	// The business day and the override travel as data, read on the pool
	// before the transaction (manufacturing phase 2 M62, rule 24); the
	// lines name no lot before layer M.
	if ps := h.stock.productions(); len(ps) == 1 && (ps[0].Today.Format(time.DateOnly) != h.Now().Format(time.DateOnly) || ps[0].ExpiredOverride ||
		ps[0].Consume[0].LotID != 0 || ps[0].Produce[0].LotID != 0) {
		t.Errorf("posted today %s override %v, want %s and false", ps[0].Today, ps[0].ExpiredOverride, h.Now().Format(time.DateOnly))
	}

	// The same request again is a replay: 200 with the first production,
	// nothing posted twice, whatever its spelling or acceptDeviation.
	post.Quantity, post.AcceptDeviation = "3.000", nil
	res = c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusOK, "a replay")
	var replayed gen.ManufacturingProduction
	res.JSON(&replayed)
	if replayed.Id != first.Id || len(h.stock.productions()) != 1 || getOrder(t, c, o.Id).Produced != "3" {
		t.Errorf("replay = %+v", replayed)
	}
	post.Quantity = "4"
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusConflict, "client_request_reused", "another body")

	res = c.Do(http.MethodGet, fmt.Sprintf("%s/productions/%d", base, first.Id), nil)
	mustStatus(t, res, http.StatusOK, "get the production")
	mustStatus(t, c.Do(http.MethodGet, fmt.Sprintf("%s/productions/999999", base), nil), http.StatusNotFound, "an unknown production")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), gen.ManufacturingProductionInput{ClientRequestId: "zero", Quantity: "0",
		Lines: []gen.ManufacturingProductionLineInput{}}), http.StatusBadRequest, "quantity_invalid", "nothing to register")
}

// TestProduction_LocationSplit is M22a's locations: a line's own location,
// else the order's component location, else the primary location first and
// then the others by full code; insufficient_stock only when the site's total
// falls short, with nothing committed.
func TestProduction_LocationSplit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "4")
	h.stock.put(varWheel, locMainA2, "3")
	h.stock.setPrimary(varWheel, locMainA2)
	h.stock.put(varBolt, locMainA1, "50")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "2"))).Order
	wheel := component(t, o, varWheel)

	post := gen.ManufacturingProductionInput{ClientRequestId: "split", Quantity: "1",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Consumed: str("2"), Scrap: str("3")}}}
	post.AcceptDeviation = &[]bool{true}[0]
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "a split production")
	ps := h.stock.productions()
	if len(ps) != 1 || len(ps[0].Consume) != 2 || ps[0].Consume[0].FromLocationID != locMainA2 || ps[0].Consume[0].Quantity != "3.000" ||
		ps[0].Consume[1].FromLocationID != locMainA1 || ps[0].Consume[1].Quantity != "2.000" {
		t.Fatalf("consume lines = %+v", ps[0].Consume)
	}

	post = gen.ManufacturingProductionInput{ClientRequestId: "short", Quantity: "0", AcceptDeviation: post.AcceptDeviation,
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Consumed: str("3")}}}
	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusConflict, "insufficient_stock", "2 at the site")
	if p.Lines == nil || (*p.Lines)[0].ComponentId == nil || *(*p.Lines)[0].ComponentId != *wheel.Id {
		t.Errorf("refusal lines = %+v", p.Lines)
	}
	// From an explicit location the ledger judges the location itself.
	post.ClientRequestId = "explicit"
	post.Lines[0].FromLocationId = &[]int64{locMainA1}[0]
	post.Lines[0].Consumed = str("1")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "from a named location")
	if last := h.stock.productions()[1]; len(last.Consume) != 1 || last.Consume[0].FromLocationID != locMainA1 {
		t.Errorf("explicit consume = %+v", last.Consume)
	}
	post.ClientRequestId = "archived"
	post.Lines[0].FromLocationId = &[]int64{locMainOld}[0]
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusBadRequest, "location_unknown", "an archived location")
}

// TestProduction_RefusalLeavesNothingCommitted is M2a: after a refusal from
// Produce — which may already have written on the transaction — nothing of
// the production is committed.
func TestProduction_RefusalLeavesNothingCommitted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order
	h.stock.onProduce(func(ctx context.Context, tx pgx.Tx, _ contracts.StockProduction) error {
		if _, err := tx.Exec(ctx, `UPDATE manufacturing.orders SET note = 'claimed by the engine' WHERE id = $1`, o.Id); err != nil {
			return err
		}
		return &contracts.StockRefusal{Code: contracts.StockSiteArchived, Detail: "The site was archived."}
	})
	post := gen.ManufacturingProductionInput{ClientRequestId: "refused", Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{}}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusConflict, "site_archived", "Inventory refuses")
	got := getOrder(t, c, o.Id)
	if got.Note != "" || got.Status != gen.ManufacturingOrderStatusConfirmed || got.Produced != "0" || got.Revision != o.Revision {
		t.Errorf("after the refusal = %+v", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.productions`); n != 0 {
		t.Errorf("%d productions committed", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.order_events WHERE kind = 'production_posted'`); n != 0 {
		t.Errorf("%d production events committed", n)
	}
}

// TestProduction_Permissions is M33's either-or: shop-floor or
// orders-manage, shop_floor_required for a reader; and order_not_open on a
// draft.
func TestProduction_Permissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	draft := createOrder(t, c, orderInput(varBike, "1"))
	post := gen.ManufacturingProductionInput{ClientRequestId: "draft", Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{}}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "productions"), post), http.StatusConflict, "order_not_open", "a draft")
	expectRefusal(t, c.Do(http.MethodGet, orderPath(draft.Id, "production-draft"), nil), http.StatusConflict, "order_not_open", "a draft's prefill")
	o := confirmOrder(t, c, draft).Order
	post.ClientRequestId = "reader"
	expectRefusal(t, h.SignIn(t, reader...).Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusForbidden, "shop_floor_required", "a reader")
	post.ClientRequestId = "operator"
	mustStatus(t, h.SignIn(t, operator...).Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "an operator")
	mustStatus(t, h.SignIn(t, operator...).Do(http.MethodPost, orderPath(o.Id, "cancel"), revisionInput(o.Revision)), http.StatusForbidden, "an operator's cancel")
}

// TestOrder_CloseBackordersAndNumbering is M23 and M17b: quantity_short
// without an answer, a backorder for the rest numbered from the root and
// confirmed at once, a backorder of a backorder taking the root's next
// suffix, closing short, and an overrun.
func TestOrder_CloseBackordersAndNumbering(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "200")
	produce := func(o gen.ManufacturingOrder, id, qty string) {
		t.Helper()
		post := gen.ManufacturingProductionInput{ClientRequestId: id, Quantity: qty, Lines: []gen.ManufacturingProductionLineInput{}}
		mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "produce "+qty+" on "+o.Number)
	}
	closeOrder := func(o gen.ManufacturingOrder, backorder *bool) gen.ManufacturingCloseResult {
		t.Helper()
		o = getOrder(t, c, o.Id)
		res := c.Do(http.MethodPost, orderPath(o.Id, "close"), gen.ManufacturingCloseInput{Revision: o.Revision, Backorder: backorder})
		mustStatus(t, res, http.StatusOK, "close "+o.Number)
		var out gen.ManufacturingCloseResult
		res.JSON(&out)
		return out
	}
	yes, no := true, false

	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "10"))).Order
	wheel := component(t, o, varWheel)
	produce(o, "a", "4")
	o = getOrder(t, c, o.Id)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "close"), gen.ManufacturingCloseInput{Revision: o.Revision}),
		http.StatusConflict, "quantity_short", "close short without an answer")

	out := closeOrder(o, &yes)
	b := out.Backorder
	if out.Order.Status != gen.ManufacturingOrderStatusDone || out.Order.ClosedAt == nil || b == nil || b.Number != "MO-1-2" ||
		b.Quantity != "6" || b.Status != gen.ManufacturingOrderStatusConfirmed || b.BackorderOf == nil || b.BackorderOf.Id != o.Id ||
		len(out.Order.Backorders) != 1 || out.Order.Backorders[0].Number != "MO-1-2" {
		t.Fatalf("close with backorder = %+v / %+v", out.Order, b)
	}
	if h.stock.reservedFor(o.Id, *wheel.Id) != "0" || component(t, *b, varWheel).Reserved != "12" {
		t.Errorf("the original still holds %s; the backorder reserved %s", h.stock.reservedFor(o.Id, *wheel.Id), component(t, *b, varWheel).Reserved)
	}

	produce(*b, "b", "1")
	out = closeOrder(*b, &yes)
	if out.Backorder == nil || out.Backorder.Number != "MO-1-3" || out.Backorder.Quantity != "5" || out.Backorder.BackorderOf.Id != b.Id {
		t.Fatalf("backorder of a backorder = %+v", out.Backorder)
	}
	third := *out.Backorder
	produce(third, "c", "2")
	if out = closeOrder(third, &no); out.Backorder != nil || out.Order.Status != gen.ManufacturingOrderStatusDone {
		t.Errorf("close short = %+v", out)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(third.Id, "close"), gen.ManufacturingCloseInput{Revision: out.Order.Revision, Backorder: &no}),
		http.StatusConflict, "order_not_open", "close twice")

	// Producing more than the quantity is allowed and shows as an overrun.
	over := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order
	produce(over, "d", "1.5")
	if got := closeOrder(over, nil); got.Order.Overrun != "0.5" || got.Order.Number != "MO-2" {
		t.Errorf("overrun = %+v", got.Order)
	}

	var events gen.ManufacturingOrderEventList
	res := c.Do(http.MethodGet, orderPath(o.Id, "events"), nil)
	mustStatus(t, res, http.StatusOK, "events")
	res.JSON(&events)
	var kinds []gen.ManufacturingOrderEventKind
	for _, e := range events.Items {
		kinds = append(kinds, e.Kind)
	}
	want := []gen.ManufacturingOrderEventKind{"created", "confirmed", "reserved", "production_posted", "closed", "backorder_created"}
	if fmt.Sprint(kinds) != fmt.Sprint(want) || events.Items[0].ActorDisplay == "" {
		t.Errorf("timeline = %v, want %v", kinds, want)
	}
	mustStatus(t, c.Do(http.MethodGet, orderPath(999999, "events"), nil), http.StatusNotFound, "events of an unknown order")
}

// TestOrder_Cancel is M19's cancel: refused once something was produced or
// consumed; otherwise every reservation released and the number kept.
func TestOrder_Cancel(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")

	produced := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "2"))).Order
	wheel := component(t, produced, varWheel)
	post := gen.ManufacturingProductionInput{ClientRequestId: "scrap", Quantity: "0",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Scrap: str("1")}}}
	mustStatus(t, c.Do(http.MethodPost, orderPath(produced.Id, "productions"), post), http.StatusCreated, "a scrap of components")
	produced = getOrder(t, c, produced.Id)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(produced.Id, "cancel"), revisionInput(produced.Revision)),
		http.StatusConflict, "order_has_production", "cancel after production")

	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "2"))).Order
	ow := component(t, o, varWheel)
	if h.stock.reservedFor(o.Id, *ow.Id) != "4" {
		t.Fatalf("reserved before cancel = %s", h.stock.reservedFor(o.Id, *ow.Id))
	}
	res := c.Do(http.MethodPost, orderPath(o.Id, "cancel"), revisionInput(o.Revision))
	mustStatus(t, res, http.StatusOK, "cancel")
	if got := orderOf(t, res); got.Status != gen.ManufacturingOrderStatusCancelled || got.Number != "MO-2" || h.stock.reservedFor(o.Id, *ow.Id) != "0" {
		t.Errorf("cancelled = %+v, reserved %s", got, h.stock.reservedFor(o.Id, *ow.Id))
	}
	got := getOrder(t, c, o.Id)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), revisionInput(got.Revision)), http.StatusConflict, "order_not_open", "cancel twice")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "reserve"), revisionInput(got.Revision)), http.StatusConflict, "order_not_open", "reserve a cancelled order")
	draft := createOrder(t, c, orderInput(varBike, "1"))
	mustStatus(t, c.Do(http.MethodPost, orderPath(draft.Id, "cancel"), revisionInput(draft.Revision)), http.StatusOK, "cancel a draft")
}

// TestProduction_ConcurrentOnPoolOfFour is §11's two productions at once:
// the order row is locked after LockVariants, both post, and the totals add
// up, on a pool of four connections.
func TestProduction_ConcurrentOnPoolOfFour(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "10"))).Order
	wheel := component(t, o, varWheel)

	var wg sync.WaitGroup
	statuses := make([]int, 4)
	for i := range statuses {
		wg.Go(func() {
			post := gen.ManufacturingProductionInput{ClientRequestId: "run-" + strconv.Itoa(i), Quantity: "1",
				Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Consumed: str("2")}}}
			statuses[i] = h.SignIn(t, planner...).Do(http.MethodPost, orderPath(o.Id, "productions"), post).Status
		})
	}
	wg.Wait()
	for i, s := range statuses {
		if s != http.StatusCreated {
			t.Errorf("production %d answered %d", i, s)
		}
	}
	got := getOrder(t, c, o.Id)
	if got.Produced != "4" || component(t, got, varWheel).Consumed != "8" || component(t, got, varWheel).Reserved != "12" {
		t.Errorf("after four productions = %+v", got)
	}
}

// TestOrder_ListFilters is M24: the open statuses by default, the status,
// variant, late and search filters, the sorts and the page.
func TestOrder_ListFilters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	createBom(t, c, bomInput(varFrame, "1", line(varTube, "1")))
	past := orderInput(varBike, "1")
	past.DueDate = dateOf(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	late := createOrder(t, c, past)
	urgent := orderInput(varFrame, "1")
	urgent.Priority = &[]gen.ManufacturingPriority{gen.ManufacturingPriorityUrgent}[0]
	frame := createOrder(t, c, urgent)
	gone := createOrder(t, c, orderInput(varBike, "1"))
	mustStatus(t, c.Do(http.MethodPost, orderPath(gone.Id, "cancel"), revisionInput(gone.Revision)), http.StatusOK, "cancel")

	list := func(query string) gen.ManufacturingOrderPage {
		t.Helper()
		var p gen.ManufacturingOrderPage
		res := c.Do(http.MethodGet, ordersPath+query, nil)
		mustStatus(t, res, http.StatusOK, "list "+query)
		res.JSON(&p)
		return p
	}
	numbers := func(p gen.ManufacturingOrderPage) string {
		var out []string
		for _, i := range p.Items {
			out = append(out, i.Number)
		}
		return fmt.Sprint(out)
	}
	if p := list(""); p.Total != 2 || numbers(p) != "[MO-1 MO-2]" || !p.Items[0].Late {
		t.Errorf("default list = %s %+v", numbers(p), p.Items)
	}
	if p := list("?status=cancelled"); numbers(p) != "[MO-3]" {
		t.Errorf("cancelled = %s", numbers(p))
	}
	if p := list("?late=true"); numbers(p) != "[MO-1]" || p.Items[0].Id != late.Id {
		t.Errorf("late = %s", numbers(p))
	}
	if p := list(fmt.Sprintf("?variantId=%d", varFrame)); numbers(p) != "[MO-2]" || p.Items[0].Id != frame.Id {
		t.Errorf("by variant = %s", numbers(p))
	}
	if p := list("?sort=priority"); numbers(p) != "[MO-2 MO-1]" {
		t.Errorf("by priority = %s", numbers(p))
	}
	if p := list("?sort=number&status=draft&status=cancelled"); numbers(p) != "[MO-3 MO-2 MO-1]" {
		t.Errorf("by number = %s", numbers(p))
	}
	if p := list("?q=mo-2"); numbers(p) != "[MO-2]" {
		t.Errorf("search mo-2 = %s", numbers(p))
	}
	if p := list("?q=frame"); numbers(p) != "[MO-2]" {
		t.Errorf("search frame = %s", numbers(p))
	}
	if p := list("?pageSize=1&page=2"); p.Total != 2 || numbers(p) != "[MO-2]" {
		t.Errorf("page 2 = %s total %d", numbers(p), p.Total)
	}
	var stats gen.ManufacturingStats
	c.Do(http.MethodGet, base+"/stats", nil).JSON(&stats)
	if stats.OrdersLate != 1 || stats.OrdersInProgress != 0 {
		t.Errorf("stats = %+v", stats)
	}
}

// liveEvent is one notification on live.Channel.
type liveEvent struct {
	T  string `json:"t"`
	K  string `json:"k"`
	ID string `json:"id"`
}

// TestOrder_LiveNotifies is M25: a write is heard on the list's topic and
// the order's own, once each, on the writing transaction.
func TestOrder_LiveNotifies(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := pgx.ConnectConfig(ctx, h.Pool().Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+live.Channel); err != nil {
		t.Fatal(err)
	}
	events := make(chan liveEvent, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return
			}
			var e liveEvent
			if json.Unmarshal([]byte(n.Payload), &e) == nil && e.T != "" {
				events <- e
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close(context.Background())
	})
	o := createOrder(t, c, orderInput(varBike, "1"))
	id := strconv.FormatInt(o.Id, 10)
	want := map[liveEvent]bool{{T: "manufacturing.orders", K: "order.changed", ID: id}: true, {T: "manufacturing.order:" + id, K: "order.changed", ID: id}: true}
	deadline := time.After(10 * time.Second)
	for len(want) > 0 {
		select {
		case e := <-events:
			delete(want, e)
		case <-deadline:
			t.Fatalf("live events missing: %v", want)
		}
	}
}

// TestOrder_ConfirmRelocksWhenAVariantIsNotLocked is M26a step 3 at the
// snapshot: a BoM line whose variant LockVariants did not answer never
// becomes a line without its snapshot. The write starts again from the lock,
// and when the lock keeps not answering it the confirm is 409
// revision_conflict, with nothing stored.
func TestOrder_ConfirmRelocksWhenAVariantIsNotLocked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	o := createOrder(t, c, orderInput(varBike, "1"))
	h.catalog.lockAnswer(varBolt, nil)
	before := len(h.catalog.lockCalls())
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "confirm"), revisionInput(o.Revision)), http.StatusConflict, "revision_conflict",
		"a component the lock does not answer")
	if n := len(h.catalog.lockCalls()) - before; n != 4 {
		t.Errorf("the confirm locked %d times, want 4 attempts", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.order_components WHERE order_id = $1`, o.Id); n != 0 {
		t.Fatalf("%d component lines stored by a refused confirm", n)
	}
	if got := getOrder(t, c, o.Id); got.Status != gen.ManufacturingOrderStatusDraft || got.Revision != o.Revision {
		t.Fatalf("after the refusal = %+v", got)
	}

	bolt := goods(varBolt, "BOLT-1", "Bolt", "0.25")
	h.catalog.lockAnswer(varBolt, &bolt)
	confirmOrder(t, c, o)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.order_components WHERE order_id = $1 AND sku <> '' AND unit <> ''`, o.Id); n != 2 {
		t.Errorf("%d snapshotted lines, want 2", n)
	}
}

// TestOrder_ConfirmHoldsTheBomForShare is M26a step 3 against a BoM edit in
// flight: confirm takes the draft's BoM row FOR SHARE before it re-reads the
// lines, so it waits for the edit, then sees the line it changed, locks its
// variant and snapshots it — never the line it did not lock.
func TestOrder_ConfirmHoldsTheBomForShare(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	b := bikeBom(t, c)
	o := createOrder(t, c, orderInput(varBike, "1"))
	ctx := context.Background()
	edit, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = edit.Rollback(ctx) })
	// A BoM edit holds the row FOR UPDATE, as lockBom does, and moves the
	// bolt line to the tube.
	if _, err := edit.Exec(ctx, `SELECT 1 FROM manufacturing.boms WHERE id = $1 FOR UPDATE`, b.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := edit.Exec(ctx, `UPDATE manufacturing.bom_lines SET variant_id = $1 WHERE bom_id = $2 AND variant_id = $3`, varTube, b.Id, varBolt); err != nil {
		t.Fatal(err)
	}
	done := make(chan *modtest.Response, 1)
	go func() { done <- c.Do(http.MethodPost, orderPath(o.Id, "confirm"), revisionInput(o.Revision)) }()
	deadline := time.Now().Add(10 * time.Second)
	for h.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'
		AND query LIKE '%manufacturing.boms%FOR SHARE%'`) == 0 {
		select {
		case res := <-done:
			t.Fatalf("confirm did not wait for the BoM edit: %d %s", res.Status, res.Body)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("confirm never waited on the BoM's row")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := edit.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	res := <-done
	mustStatus(t, res, http.StatusOK, "confirm after the BoM edit")
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.order_components WHERE order_id = $1 AND variant_id = $2 AND sku = 'TUBE-1'`, o.Id, varTube); n != 1 {
		t.Errorf("tube lines = %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.order_components WHERE order_id = $1 AND (variant_id = $2 OR sku = '')`, o.Id, varBolt); n != 0 {
		t.Errorf("%d stale or unsnapshotted lines", n)
	}
}

// TestOrder_ConfirmedOrderKeepsItsPlace is M19 after confirm: the BoM moved
// to another site's work centers, or the order's site archived, a confirmed
// order still takes its dates, note and locations, judged against its own
// site's locations; its site never changes.
func TestOrder_ConfirmedOrderKeepsItsPlace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	b := bikeBom(t, c)
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order

	branch := createWorkCenter(t, c, workCenterInput("BR", siteBranch))
	u := updateOf(getBom(t, c, b.Id))
	u.Operations[0].WorkCenterId = branch.Id
	mustStatus(t, c.Do(http.MethodPut, bomPath(b.Id), u), http.StatusOK, "move the BoM to the branch")
	atMain := orderInput(varBike, "1")
	atMain.SiteId = &[]int64{siteMain}[0]
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, atMain), http.StatusBadRequest, "work_center_other_site", "a new order at the old site")

	upd := gen.ManufacturingOrderUpdate{Revision: o.Revision, BomId: b.Id, Quantity: "1", SiteId: siteMain, Priority: gen.ManufacturingPriorityNormal,
		Note: str("After the move"), ComponentLocationId: &[]int64{locMainA1}[0]}
	res := c.Do(http.MethodPut, orderPath(o.Id), upd)
	mustStatus(t, res, http.StatusOK, "edit a confirmed order after its BoM moved")
	o = orderOf(t, res)
	if o.Note != "After the move" || o.ComponentLocationId == nil || *o.ComponentLocationId != locMainA1 || o.OutputLocationId != locMainRoot {
		t.Fatalf("after the edit = %+v", o)
	}

	h.stock.archiveSite(siteMain)
	upd.Revision, upd.Note = o.Revision, str("After the archive")
	mustStatus(t, c.Do(http.MethodPut, orderPath(o.Id), upd), http.StatusOK, "edit a confirmed order at an archived site")
	o = getOrder(t, c, o.Id)
	upd.Revision = o.Revision
	upd.ComponentLocationId = &[]int64{locBranchRoot}[0]
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), upd), http.StatusBadRequest, "location_unknown", "another site's location")
	upd.ComponentLocationId, upd.SiteId = nil, siteBranch
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), upd), http.StatusConflict, "order_not_draft", "a confirmed order's site")
}

// TestOrder_ResponsibleMustBeActive refuses a disabled user as a new
// responsible, while one disabled after being named stays through an edit.
func TestOrder_ResponsibleMustBeActive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	_, named := h.SignInUser(t, reader...)
	_, disabled := h.SignInUser(t, reader...)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)

	in := orderInput(varBike, "1")
	in.ResponsibleUserId = &disabled
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, in), http.StatusBadRequest, "", "a disabled responsible")
	in.ResponsibleUserId = &named
	o := createOrder(t, c, in)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, named)
	b := o.BomId
	upd := gen.ManufacturingOrderUpdate{Revision: o.Revision, BomId: b, Quantity: "1", SiteId: o.SiteId, Priority: o.Priority,
		ResponsibleUserId: &named, Note: str("Still theirs")}
	res := c.Do(http.MethodPut, orderPath(o.Id), upd)
	mustStatus(t, res, http.StatusOK, "keep a responsible disabled since")
	upd.Revision = orderOf(t, res).Revision
	upd.ResponsibleUserId = &disabled
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), upd), http.StatusBadRequest, "", "change to a disabled responsible")
}

// TestProduction_ConcurrentSplitReadsTheLocationsAgain is M22a's split under
// two productions at once: both split over the locations they read before
// their transactions; the second's is stale once the first has taken from
// the primary location, so Inventory refuses it, and it reads the locations
// again and splits anew, posting since the site's total suffices.
func TestProduction_ConcurrentSplitReadsTheLocationsAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "4")
	h.stock.put(varWheel, locMainA2, "4")
	h.stock.setPrimary(varWheel, locMainA2)
	h.stock.put(varBolt, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "4"))).Order
	wheel := component(t, o, varWheel)

	// The first Produce waits until both posts have read the locations, so
	// both split over the same reading.
	bothRead := make(chan struct{})
	var reads atomic.Int32
	h.stock.onLocations(func() {
		if reads.Add(1) == 2 {
			close(bothRead)
		}
	})
	var first sync.Once
	h.stock.onProduce(func(context.Context, pgx.Tx, contracts.StockProduction) error {
		first.Do(func() {
			select {
			case <-bothRead:
			case <-time.After(10 * time.Second):
			}
		})
		return nil
	})
	accept := true
	var wg sync.WaitGroup
	statuses := make([]*modtest.Response, 2)
	for i := range statuses {
		wg.Go(func() {
			post := gen.ManufacturingProductionInput{ClientRequestId: "split-" + strconv.Itoa(i), Quantity: "1", AcceptDeviation: &accept,
				Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *wheel.Id, Consumed: str("3")}}}
			statuses[i] = h.SignIn(t, planner...).Do(http.MethodPost, orderPath(o.Id, "productions"), post)
		})
	}
	wg.Wait()
	for i, res := range statuses {
		mustStatus(t, res, http.StatusCreated, "production "+strconv.Itoa(i))
	}
	if got := reads.Load(); got != 3 {
		t.Errorf("Stock.Locations read %d times, want 3: one post read again", got)
	}
	ps := h.stock.productions()
	if len(ps) != 2 || len(ps[1].Consume) != 2 || ps[1].Consume[0].FromLocationID != locMainA2 || ps[1].Consume[0].Quantity != "1.000" ||
		ps[1].Consume[1].FromLocationID != locMainA1 || ps[1].Consume[1].Quantity != "2.000" {
		t.Fatalf("productions = %+v", ps)
	}
	if h.stock.held(varWheel, locMainA2) != "0" || h.stock.held(varWheel, locMainA1) != "2" {
		t.Errorf("wheels left: A1 %s, A2 %s", h.stock.held(varWheel, locMainA1), h.stock.held(varWheel, locMainA2))
	}
	if got := component(t, getOrder(t, c, o.Id), varWheel); got.Consumed != "6" {
		t.Errorf("wheels consumed = %s, want 6", got.Consumed)
	}
}

// TestProduction_ProduceLineRefusalNamesNoComponent is the refusal's
// attribution: a line of Inventory's refusal names a component line only
// when both its variant and its line id are that line's, so a refused
// produce line — whose line id is the order's — names none.
func TestProduction_ProduceLineRefusalNamesNoComponent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order
	wheel := component(t, o, varWheel)
	h.stock.onProduce(func(context.Context, pgx.Tx, contracts.StockProduction) error {
		line := contracts.StockLineRefusal{Code: contracts.StockVariantNotStocked, VariantID: varBike, LineID: *wheel.Id, Detail: "Not stocked."}
		return &contracts.StockRefusal{Code: line.Code, VariantID: line.VariantID, LineID: line.LineID, Detail: line.Detail,
			Lines: []contracts.StockLineRefusal{line}}
	})
	post := gen.ManufacturingProductionInput{ClientRequestId: "produce", Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{}}
	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusConflict, "variant_not_stocked", "the output refused")
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].ComponentId != nil {
		t.Errorf("refusal lines = %+v", p.Lines)
	}
}

// TestOrder_BackorderOfAnArchivedBom is close's exception: a backorder keeps
// its original's BoM even when it was archived since, and is confirmed at
// once, while a draft on it can no longer be confirmed.
func TestOrder_BackorderOfAnArchivedBom(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	b := bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "2"))).Order
	draft := createOrder(t, c, orderInput(varBike, "1"))
	post := gen.ManufacturingProductionInput{ClientRequestId: "half", Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{}}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "produce half")
	b = getBom(t, c, b.Id)
	mustStatus(t, c.Do(http.MethodPost, bomPath(b.Id, "archive"), revisionInput(b.Revision)), http.StatusOK, "archive the BoM")

	o = getOrder(t, c, o.Id)
	yes := true
	res := c.Do(http.MethodPost, orderPath(o.Id, "close"), gen.ManufacturingCloseInput{Revision: o.Revision, Backorder: &yes})
	mustStatus(t, res, http.StatusOK, "close with a backorder of an archived BoM")
	var out gen.ManufacturingCloseResult
	res.JSON(&out)
	if out.Backorder == nil || out.Backorder.BomId != b.Id || out.Backorder.Status != gen.ManufacturingOrderStatusConfirmed {
		t.Fatalf("backorder = %+v", out.Backorder)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "confirm"), revisionInput(draft.Revision)), http.StatusConflict, "bom_not_active",
		"confirm a draft of the archived BoM")
}

// TestOrder_DiscontinuedVariantWarns is §11's discontinued variant: a new
// order whose component is of a Discontinued product is allowed, and the
// create and confirm answers warn variant_discontinued; an order without one
// warns nothing.
func TestOrder_DiscontinuedVariantWarns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	createBom(t, c, bomInput(varFrame, "1", line(varPaint, "1")))
	bikeBom(t, c)
	paint := goods(varPaint, "PAINT-1", "Paint", "")
	paint.ProductStatus = "Discontinued"
	h.catalog.seed(paint)

	o := createOrder(t, c, orderInput(varFrame, "1"))
	if fmt.Sprint(o.Warnings) != "[variant_discontinued]" {
		t.Errorf("create warnings = %v", o.Warnings)
	}
	if out := confirmOrder(t, c, o); fmt.Sprint(out.Order.Warnings) != "[variant_discontinued]" {
		t.Errorf("confirm warnings = %v", out.Order.Warnings)
	}
	if bike := createOrder(t, c, orderInput(varBike, "1")); bike.Warnings == nil || len(bike.Warnings) != 0 {
		t.Errorf("a bicycle's warnings = %v", bike.Warnings)
	}
}

// TestOrder_ListPageBounds refuses a page past 100000, whose offset would
// overflow.
func TestOrder_ListPageBounds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	mustStatus(t, c.Do(http.MethodGet, ordersPath+"?page=100001&pageSize=200", nil), http.StatusBadRequest, "a page past the last")
	mustStatus(t, c.Do(http.MethodGet, ordersPath+"?page=100000&pageSize=200", nil), http.StatusOK, "the last page")
	var stats gen.ManufacturingStats
	c.Do(http.MethodGet, base+"/stats", nil).JSON(&stats)
	if stats.Today.IsZero() {
		t.Errorf("stats today = %v", stats.Today)
	}
}
