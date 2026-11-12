package manufacturing_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Lot capture in Manufacturing (manufacturing phase 2 M80–M86), against the
// fake ledger's lots: the output lot by the order's pattern, serials one per
// unit, the consumed lots per component — named, or taken by Inventory with
// AutoLot — the proposals and their one re-proposal, the deadlock retried
// once, a replay answering the first production's lots, and the order's
// lots read.

// The lot-tracked fixtures: jam made of berries (lot-tracked) and sugar (not
// tracked); a serial pump of a body.
const (
	varJam   int32 = 201 // lot-tracked, expiry 30 days
	varBerry int32 = 202 // lot-tracked component
	varSugar int32 = 203 // untracked component
	varPump  int32 = 204 // serial-tracked
	varBody  int32 = 205 // untracked component of the pump
)

// lotHarness is a harness with the lot-tracked variants seeded.
func lotHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	jam := goods(varJam, "JAM-1", "Jam", "")
	jam.Tracking = contracts.TrackingLot
	thirty := int32(30)
	jam.ExpiryDays = &thirty
	berry := goods(varBerry, "BERRY-1", "Berries", "")
	berry.Tracking = contracts.TrackingLot
	pump := goods(varPump, "PUMP-1", "Pump", "")
	pump.Tracking = contracts.TrackingSerial
	for _, e := range []*contracts.VariantEntry{&jam, &berry, &pump} {
		if e.Tracking == "" {
			e.Tracking = contracts.TrackingNone
		}
	}
	sugar, body := goods(varSugar, "SUGAR-1", "Sugar", ""), goods(varBody, "BODY-1", "Pump body", "")
	sugar.Tracking, body.Tracking = contracts.TrackingNone, contracts.TrackingNone
	h.catalog.seed(jam, berry, sugar, pump, body)
	return h
}

// jamOrder is a confirmed order of qty jam from a BoM of two berries and one
// sugar per jar, with the lot pattern given ("" for the default).
func jamOrder(t *testing.T, c *modtest.Client, qty, pattern string) gen.ManufacturingOrder {
	t.Helper()
	pot := createWorkCenter(t, c, workCenterInput("POT", siteMain))
	in := bomInput(varJam, "1", line(varBerry, "2"), line(varSugar, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("a", 10, pot.Id, "1")}
	if pattern != "" {
		in.LotPattern = &pattern
	}
	b := createBom(t, c, in)
	want := pattern
	if want == "" {
		want = "{MO}"
	}
	if b.LotPattern != want {
		t.Fatalf("BoM lotPattern = %q, want %q", b.LotPattern, want)
	}
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varJam, qty))).Order
	if o.LotPattern != want {
		t.Fatalf("order lotPattern = %q, want %q", o.LotPattern, want)
	}
	return o
}

func productionOf(t *testing.T, res *modtest.Response) gen.ManufacturingProduction {
	t.Helper()
	var p gen.ManufacturingProduction
	res.JSON(&p)
	return p
}

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

// TestProductionLots_OrderLotAcrossDays is M80 with the default {MO}: two
// productions on different days add to one lot, the second's defaulted
// production date never compared (no lot_dates_differ); the berries are
// taken FEFO by Inventory (AutoLot) when sent without lots; the draft
// proposes them and offers every lot; the order's lots read sums them.
func TestProductionLots_OrderLotAcrossDays(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, planner...)
	now := h.Now()
	late := h.stock.receiveLot(varBerry, "B-LATE", locMainA1, "10", now.Add(-48*time.Hour), day(2027, 6, 1))
	early := h.stock.receiveLot(varBerry, "B-EARLY", locMainA1, "3", now.Add(-24*time.Hour), day(2027, 1, 1))
	h.stock.put(varSugar, locMainA1, "100")
	o := jamOrder(t, c, "4", "")
	berry := component(t, o, varBerry)

	res := c.Do(http.MethodGet, orderPath(o.Id, "production-draft")+"?quantity=2", nil)
	mustStatus(t, res, http.StatusOK, "draft")
	var draft gen.ManufacturingProductionDraft
	res.JSON(&draft)
	if draft.Output.Tracking != gen.Lot || draft.Output.LotNumber == nil || *draft.Output.LotNumber != o.Number || draft.Output.Expiry == nil {
		t.Errorf("draft output = %+v", draft.Output)
	}
	for _, l := range draft.Lines {
		switch l.ComponentId {
		case *berry.Id:
			if l.Tracking != gen.Lot || len(l.Lots) != 2 || l.Lots[0].LotId != early || l.Lots[0].Quantity != "3" ||
				l.Lots[1].LotId != late || l.Lots[1].Quantity != "1" || l.Short != "0" || len(l.LotChoices) != 2 {
				t.Errorf("berry draft line = %+v", l)
			}
		default:
			if l.Tracking != gen.None || len(l.Lots) != 0 || len(l.LotChoices) != 0 {
				t.Errorf("sugar draft line = %+v", l)
			}
		}
	}

	post := gen.ManufacturingProductionInput{ClientRequestId: "first", Quantity: "2",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *berry.Id, Consumed: str("4")}}}
	res = c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusCreated, "the first production")
	first := productionOf(t, res)
	if len(first.OutputLots) != 1 || first.OutputLots[0].LotNumber != o.Number || first.OutputLots[0].Quantity != "2" {
		t.Fatalf("first output lots = %+v", first.OutputLots)
	}
	lots := first.Lines[0].Lots
	if len(lots) != 2 || lots[0].LotId != early || lots[0].Consumed != "3" || lots[1].LotId != late || lots[1].Consumed != "1" {
		t.Errorf("first line lots = %+v", lots)
	}
	if ps := h.stock.productions(); !ps[0].Consume[0].AutoLot || ps[0].Produce[0].LotID != first.OutputLots[0].LotId {
		t.Errorf("ledger lines = %+v", ps[0])
	}

	h.Advance(24 * time.Hour)
	c = h.SignIn(t, planner...) // the session does not outlive a day
	post.ClientRequestId = "second"
	res = c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusCreated, "the second production, a day later")
	second := productionOf(t, res)
	if len(second.OutputLots) != 1 || second.OutputLots[0].LotId != first.OutputLots[0].LotId {
		t.Errorf("second output lots = %+v, want the first's lot", second.OutputLots)
	}
	// A stated date that differs from the lot's is the 400 of the output lot.
	differ := post
	differ.ClientRequestId = "differ"
	differ.OutputLot = &gen.ManufacturingOutputLotInput{Expiry: &openapi_types.Date{Time: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}
	r := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), differ), http.StatusBadRequest, "lot_dates_differ", "another expiry")
	if r.Errors == nil || (*r.Errors)["outputLot"] == nil {
		t.Errorf("another expiry's errors = %+v", r.Errors)
	}
	calls := h.stock.ensureCalls()
	if len(calls) != 3 || calls[0][0].ProductionDate.Equal(calls[1][0].ProductionDate) || calls[1][0].Expiry != nil {
		t.Errorf("EnsureLots calls = %+v", calls)
	}
	if got := h.stock.lotAt(first.OutputLots[0].LotId, o.OutputLocationId); got != "4" {
		t.Errorf("the order's lot holds %s, want 4", got)
	}

	res = c.Do(http.MethodGet, orderPath(o.Id, "lots"), nil)
	mustStatus(t, res, http.StatusOK, "the order's lots")
	var read gen.ManufacturingOrderLots
	res.JSON(&read)
	if len(read.Outputs) != 1 || read.Outputs[0].Quantity != "4" || read.Outputs[0].Productions != 2 {
		t.Errorf("outputs = %+v", read.Outputs)
	}
	if len(read.Components) != 1 || read.Components[0].ComponentId != *berry.Id || len(read.Components[0].Lots) != 2 ||
		read.Components[0].Lots[0].Consumed != "3" || read.Components[0].Lots[1].Consumed != "5" {
		t.Errorf("components = %+v", read.Components)
	}
	mustStatus(t, c.Do(http.MethodGet, orderPath(999999, "lots"), nil), http.StatusNotFound, "an unknown order's lots")
	var payload string
	if err := h.Pool().QueryRow(t.Context(), `SELECT payload::text FROM manufacturing.order_events WHERE order_id = $1 AND kind = 'production_posted' ORDER BY id LIMIT 1`, o.Id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if want := `"lotRows": 2`; !strings.Contains(payload, want) || !strings.Contains(payload, `"outputLots": [`) {
		t.Errorf("event payload = %s", payload)
	}
}

// TestProductionLots_PatternPerProduction is M80's pattern: L{YYMMDD}-{N}
// makes a lot per production, counted per prefix; a pattern that renders no
// lot number is refused on the BoM's save; a number sent is used as it is;
// an untracked product refuses an output lot.
func TestProductionLots_PatternPerProduction(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, planner...)
	h.stock.receiveLot(varBerry, "B-1", locMainA1, "100", h.Now(), nil)
	h.stock.put(varSugar, locMainA1, "100")

	bad := bomInput(varJam, "1", line(varBerry, "2"))
	bad.LotPattern = str("{MO} {SKU}")
	expectRefusal(t, c.Do(http.MethodPost, bomsPath, bad), http.StatusBadRequest, "lot_pattern_invalid", "a pattern with a space")

	o := jamOrder(t, c, "10", "L{YYMMDD}-{N}")
	prefix := "L" + h.Now().UTC().Format("060102") + "-"
	post := gen.ManufacturingProductionInput{ClientRequestId: "p1", Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{}}
	var numbers []string
	for _, id := range []string{"p1", "p2"} {
		post.ClientRequestId = id
		res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
		mustStatus(t, res, http.StatusCreated, "production "+id)
		numbers = append(numbers, productionOf(t, res).OutputLots[0].LotNumber)
	}
	if numbers[0] != prefix+"1" || numbers[1] != prefix+"2" {
		t.Errorf("lot numbers = %v, want %s1 and %s2", numbers, prefix, prefix)
	}
	// A number typed in the counter's shape names its own lot; the counter
	// skips it rather than join that lot (M57).
	post.ClientRequestId = "typed"
	post.OutputLot = &gen.ManufacturingOutputLotInput{Number: str(prefix + "3")}
	res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusCreated, "a typed number ahead of the counter")
	typed := productionOf(t, res).OutputLots[0]
	post.ClientRequestId, post.OutputLot = "p3", nil
	res = c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusCreated, "the counter past a typed number")
	if got := productionOf(t, res).OutputLots[0]; got.LotNumber != prefix+"4" || got.LotId == typed.LotId {
		t.Errorf("the counted lot after a typed %s3 = %+v, want a new %s4", prefix, got, prefix)
	}
	post.ClientRequestId = "named"
	post.OutputLot = &gen.ManufacturingOutputLotInput{Number: str("BATCH-7"), SupplierLot: str("S-1")}
	res = c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusCreated, "a named lot")
	if got := productionOf(t, res).OutputLots[0].LotNumber; got != "BATCH-7" {
		t.Errorf("named lot = %s", got)
	}

	// An untracked product takes no output lot.
	c2 := h.SignIn(t, planner...)
	bikeBom(t, c2)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	bike := confirmOrder(t, c2, createOrder(t, c2, orderInput(varBike, "1"))).Order
	untracked := gen.ManufacturingProductionInput{ClientRequestId: "bike", Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{},
		OutputLot: &gen.ManufacturingOutputLotInput{Number: str("X")}}
	expectRefusal(t, c2.Do(http.MethodPost, orderPath(bike.Id, "productions"), untracked), http.StatusBadRequest, "lot_not_allowed", "a bike's lot")
}

// TestProductionLots_Serials is M81: one produce line of 1 per unit,
// numbered {MO}-0001 upwards across productions; a first counted up; a list
// one per unit; a part of a unit refused serial_quantity.
func TestProductionLots_Serials(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, planner...)
	h.stock.put(varBody, locMainA1, "100")
	createBom(t, c, bomInput(varPump, "1", line(varBody, "1")))
	o := confirmOrder(t, c, createOrder(t, c, orderInput(varPump, "10"))).Order
	post := func(id, qty string, serials *gen.ManufacturingOutputSerialsInput) *modtest.Response {
		return c.Do(http.MethodPost, orderPath(o.Id, "productions"), gen.ManufacturingProductionInput{ClientRequestId: id, Quantity: qty,
			Lines: []gen.ManufacturingProductionLineInput{}, OutputSerials: serials})
	}
	numbersOf := func(res *modtest.Response) []string {
		var out []string
		for _, l := range productionOf(t, res).OutputLots {
			if !l.Serial || l.Quantity != "1" {
				t.Errorf("serial output = %+v", l)
			}
			out = append(out, l.LotNumber)
		}
		return out
	}
	res := post("a", "3", nil)
	mustStatus(t, res, http.StatusCreated, "three pumps")
	if got := numbersOf(res); len(got) != 3 || got[0] != o.Number+"-0001" || got[2] != o.Number+"-0003" {
		t.Errorf("first serials = %v", got)
	}
	if ps := h.stock.productions(); len(ps[0].Produce) != 3 || ps[0].Produce[0].Quantity != "1.000" {
		t.Errorf("produce lines = %+v", ps[0].Produce)
	}
	res = post("b", "2", nil)
	mustStatus(t, res, http.StatusCreated, "two more")
	if got := numbersOf(res); len(got) != 2 || got[0] != o.Number+"-0004" {
		t.Errorf("continued serials = %v", got)
	}
	res = post("c", "2", &gen.ManufacturingOutputSerialsInput{First: str("SN0099")})
	mustStatus(t, res, http.StatusCreated, "from SN0099")
	if got := numbersOf(res); len(got) != 2 || got[0] != "SN0099" || got[1] != "SN0100" {
		t.Errorf("counted serials = %v", got)
	}
	res = post("d", "1", &gen.ManufacturingOutputSerialsInput{Numbers: &[]string{"X-1"}})
	mustStatus(t, res, http.StatusCreated, "a listed serial")
	expectRefusal(t, post("e", "1.5", nil), http.StatusBadRequest, "serial_quantity", "half a pump")
	expectRefusal(t, post("f", "2", &gen.ManufacturingOutputSerialsInput{Numbers: &[]string{"Y-1"}}), http.StatusBadRequest, "serial_quantity", "one number for two")
	r := expectRefusal(t, post("g", "1", &gen.ManufacturingOutputSerialsInput{Numbers: &[]string{}}), http.StatusBadRequest, "", "an empty list")
	if r.Errors == nil || (*r.Errors)["outputSerials.numbers"] == nil {
		t.Errorf("an empty list's errors = %+v", r.Errors)
	}
	// A serial is a new lot: one that exists is lot_exists at its field.
	r = expectRefusal(t, post("h", "1", &gen.ManufacturingOutputSerialsInput{Numbers: &[]string{o.Number + "-0002"}}), http.StatusBadRequest, "lot_exists",
		"a listed serial made before")
	if r.Errors == nil || (*r.Errors)["outputSerials.numbers[0]"] == nil {
		t.Errorf("a listed serial's errors = %+v", r.Errors)
	}
	r = expectRefusal(t, post("i", "2", &gen.ManufacturingOutputSerialsInput{First: str("SN0100")}), http.StatusBadRequest, "lot_exists", "a first counted into one made")
	if r.Errors == nil || (*r.Errors)["outputSerials.first"] == nil {
		t.Errorf("a counted serial's errors = %+v", r.Errors)
	}

	// The default continues after the highest {MO}-NNNN serial, not after the
	// count: a serial typed in that shape is never handed out again.
	other := confirmOrder(t, c, createOrder(t, c, orderInput(varPump, "10"))).Order
	postOther := func(id string, serials *gen.ManufacturingOutputSerialsInput) *modtest.Response {
		return c.Do(http.MethodPost, orderPath(other.Id, "productions"), gen.ManufacturingProductionInput{ClientRequestId: id, Quantity: "1",
			Lines: []gen.ManufacturingProductionLineInput{}, OutputSerials: serials})
	}
	mustStatus(t, postOther("typed", &gen.ManufacturingOutputSerialsInput{Numbers: &[]string{other.Number + "-0002"}}), http.StatusCreated, "a typed 0002")
	res = c.Do(http.MethodGet, orderPath(other.Id, "production-draft"), nil)
	mustStatus(t, res, http.StatusOK, "the draft after a typed serial")
	var draft gen.ManufacturingProductionDraft
	res.JSON(&draft)
	if draft.Output.SerialFirst == nil || *draft.Output.SerialFirst != other.Number+"-0003" {
		t.Errorf("the draft's first serial = %v, want %s-0003", draft.Output.SerialFirst, other.Number)
	}
	res = postOther("default", nil)
	mustStatus(t, res, http.StatusCreated, "the default after a typed 0002")
	if got := numbersOf(res); len(got) != 1 || got[0] != other.Number+"-0003" {
		t.Errorf("the default serial = %v, want %s-0003", got, other.Number)
	}
}

// TestProductionLots_ConsumedRows is M82's named lots: one consume line per
// row, the rows summing to the line (picks_mismatch otherwise), a held lot
// refused lot_blocked naming the lot, an expired one refused lot_expired
// unless chosen by a holder of inventory:expired-override.
func TestProductionLots_ConsumedRows(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, planner...)
	now := h.Now()
	a := h.stock.receiveLot(varBerry, "B-A", locMainA1, "5", now, nil)
	b := h.stock.receiveLot(varBerry, "B-B", locMainA2, "5", now, nil)
	old := h.stock.receiveLot(varBerry, "B-OLD", locMainA1, "5", now, day(2020, 1, 1))
	h.stock.put(varSugar, locMainA1, "100")
	o := jamOrder(t, c, "10", "")
	berry := component(t, o, varBerry)
	rows := func(r ...gen.ManufacturingProductionLotInput) *[]gen.ManufacturingProductionLotInput { return &r }
	in := func(id, consumed, scrap string, lots *[]gen.ManufacturingProductionLotInput) gen.ManufacturingProductionInput {
		return gen.ManufacturingProductionInput{ClientRequestId: id, Quantity: "1", AcceptDeviation: &[]bool{true}[0],
			Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *berry.Id, Consumed: str(consumed), Scrap: str(scrap), Lots: lots}}}
	}

	res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), in("rows", "3", "1", rows(
		gen.ManufacturingProductionLotInput{LotId: a, LocationId: locMainA1, Consumed: str("2")},
		gen.ManufacturingProductionLotInput{LotId: b, LocationId: locMainA2, Consumed: str("1"), Scrapped: str("1")})))
	mustStatus(t, res, http.StatusCreated, "named lots")
	p := productionOf(t, res)
	if l := p.Lines[0].Lots; len(l) != 2 || l[0].LotNumber != "B-A" || l[0].Consumed != "2" || l[1].LotNumber != "B-B" || l[1].Scrapped != "1" {
		t.Errorf("line lots = %+v", l)
	}
	if h.stock.lotAt(a, locMainA1) != "3" || h.stock.lotAt(b, locMainA2) != "3" {
		t.Errorf("lots left: A %s, B %s", h.stock.lotAt(a, locMainA1), h.stock.lotAt(b, locMainA2))
	}

	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), in("mismatch", "3", "0", rows(
		gen.ManufacturingProductionLotInput{LotId: a, LocationId: locMainA1, Consumed: str("2")}))), http.StatusBadRequest, "picks_mismatch", "rows short")

	h.stock.setLotStatus(a, contracts.LotQuarantine)
	r := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), in("held", "1", "0", rows(
		gen.ManufacturingProductionLotInput{LotId: a, LocationId: locMainA1, Consumed: str("1")}))), http.StatusConflict, "lot_blocked", "a held lot")
	if r.Lines == nil || (*r.Lines)[0].LotId == nil || *(*r.Lines)[0].LotId != a || (*r.Lines)[0].ComponentId == nil {
		t.Errorf("refusal lines = %+v", r.Lines)
	}

	expired := in("expired", "1", "0", rows(gen.ManufacturingProductionLotInput{LotId: old, LocationId: locMainA1, Consumed: str("1"), AllowExpired: &[]bool{true}[0]}))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), expired), http.StatusConflict, "lot_expired", "an expired lot without the override")
	overrider := h.SignIn(t, append([]string{"inventory:expired-override"}, planner...)...)
	mustStatus(t, overrider.Do(http.MethodPost, orderPath(o.Id, "productions"), expired), http.StatusCreated, "an expired lot with the override")
}

// TestProductionLots_ProposedAgainOnce is M65a: lots the draft proposed and
// the person did not change, refused lot_blocked, are proposed again once
// from the stock then and posted; chosen lots are not; a re-proposal that
// finds nothing answers the first refusal.
func TestProductionLots_ProposedAgainOnce(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, planner...)
	now := h.Now()
	first := h.stock.receiveLot(varBerry, "B-FIRST", locMainA1, "4", now, day(2027, 1, 1))
	next := h.stock.receiveLot(varBerry, "B-NEXT", locMainA1, "4", now, day(2027, 2, 1))
	h.stock.put(varSugar, locMainA1, "100")
	o := jamOrder(t, c, "10", "")
	berry := component(t, o, varBerry)
	proposed := func(id string, chosen bool) gen.ManufacturingProductionInput {
		return gen.ManufacturingProductionInput{ClientRequestId: id, Quantity: "1",
			Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *berry.Id, Consumed: str("2"), LotsProposed: &[]bool{!chosen}[0],
				Lots: &[]gen.ManufacturingProductionLotInput{{LotId: first, LocationId: locMainA1, Consumed: str("2")}}}}}
	}
	h.stock.setLotStatus(first, contracts.LotBlocked)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), proposed("chosen", true)), http.StatusConflict, "lot_blocked", "a chosen lot")
	res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), proposed("again", false))
	mustStatus(t, res, http.StatusCreated, "a proposal made again")
	if l := productionOf(t, res).Lines[0].Lots; len(l) != 1 || l[0].LotId != next || l[0].Consumed != "2" {
		t.Errorf("lots after the re-proposal = %+v", l)
	}
	h.stock.setLotStatus(next, contracts.LotBlocked)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), proposed("twice", false)), http.StatusConflict, "lot_blocked",
		"a re-proposal that finds nothing")
}

// TestProductionLots_ReproposalShort is M65a against M82's sum: a
// re-proposal the stock covers only in part answers the first refusal, and
// posts nothing — never a production that takes less than it records.
func TestProductionLots_ReproposalShort(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, planner...)
	now := h.Now()
	first := h.stock.receiveLot(varBerry, "B-FIRST", locMainA1, "4", now, day(2027, 1, 1))
	next := h.stock.receiveLot(varBerry, "B-NEXT", locMainA1, "1", now, day(2027, 2, 1))
	h.stock.put(varSugar, locMainA1, "100")
	o := jamOrder(t, c, "10", "")
	berry := component(t, o, varBerry)
	h.stock.setLotStatus(first, contracts.LotBlocked)
	post := gen.ManufacturingProductionInput{ClientRequestId: "short", Quantity: "1",
		Lines: []gen.ManufacturingProductionLineInput{{ComponentId: *berry.Id, Consumed: str("2"), LotsProposed: &[]bool{true}[0],
			Lots: &[]gen.ManufacturingProductionLotInput{{LotId: first, LocationId: locMainA1, Consumed: str("2")}}}}}
	r := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusConflict, "lot_blocked", "a re-proposal covering 1 of 2")
	if r.Lines == nil || (*r.Lines)[0].LotId == nil || *(*r.Lines)[0].LotId != first {
		t.Errorf("refusal lines = %+v", r.Lines)
	}
	if got := h.stock.lotAt(next, locMainA1); got != "1" {
		t.Errorf("B-NEXT holds %s after a refused production, want 1", got)
	}
	if n := len(h.stock.productions()); n != 0 {
		t.Errorf("%d productions posted to the ledger", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.productions WHERE client_request_id = 'short'`); n != 0 {
		t.Errorf("%d productions committed", n)
	}
}

// TestProduction_DeadlockRetriedOnce is M86's retry: a production chosen as
// a deadlock's victim runs again once and posts; a second deadlock is 409
// revision_conflict, with nothing committed.
func TestProduction_DeadlockRetriedOnce(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, planner...)
	h.stock.receiveLot(varBerry, "B-1", locMainA1, "100", h.Now(), nil)
	h.stock.put(varSugar, locMainA1, "100")
	o := jamOrder(t, c, "10", "")
	var calls, deadlocks atomic.Int32
	deadlocks.Store(1)
	h.stock.onProduce(func(context.Context, pgx.Tx, contracts.StockProduction) error {
		calls.Add(1)
		if deadlocks.Add(-1) >= 0 {
			return &pgconn.PgError{Code: "40P01", Message: "deadlock detected"}
		}
		return nil
	})
	post := gen.ManufacturingProductionInput{ClientRequestId: "once", Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{}}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "a production after one deadlock")
	if calls.Load() != 2 {
		t.Errorf("Produce called %d times, want 2", calls.Load())
	}
	deadlocks.Store(2)
	post.ClientRequestId = "twice"
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusConflict, "revision_conflict", "two deadlocks")
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.productions WHERE client_request_id = 'twice'`); n != 0 {
		t.Errorf("%d productions committed after two deadlocks", n)
	}
}

// TestProductionLots_Replay is M22a's replay with lots: the same
// clientRequestId and body answers 200 with the first production's lots, and
// no lot is ensured again.
func TestProductionLots_Replay(t *testing.T) {
	t.Parallel()
	h := lotHarness(t)
	c := h.SignIn(t, planner...)
	h.stock.receiveLot(varBerry, "B-1", locMainA1, "100", h.Now(), nil)
	h.stock.put(varSugar, locMainA1, "100")
	o := jamOrder(t, c, "10", "")
	berry := component(t, o, varBerry)
	post := gen.ManufacturingProductionInput{ClientRequestId: "replayed", Quantity: "1",
		OutputLot: &gen.ManufacturingOutputLotInput{Expiry: nil},
		Lines:     []gen.ManufacturingProductionLineInput{{ComponentId: *berry.Id, Consumed: str("2")}}}
	res := c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusCreated, "the first post")
	first := productionOf(t, res)
	res = c.Do(http.MethodPost, orderPath(o.Id, "productions"), post)
	mustStatus(t, res, http.StatusOK, "the replay")
	again := productionOf(t, res)
	if again.Id != first.Id || len(again.OutputLots) != 1 || again.OutputLots[0].LotId != first.OutputLots[0].LotId ||
		len(again.Lines[0].Lots) != 1 || again.Lines[0].Lots[0].LotId != first.Lines[0].Lots[0].LotId {
		t.Errorf("replay = %+v, first = %+v", again, first)
	}
	if n := len(h.stock.ensureCalls()); n != 1 {
		t.Errorf("EnsureLots called %d times, want once", n)
	}
	post.Quantity = "2"
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusConflict, "client_request_reused", "another body")
}
