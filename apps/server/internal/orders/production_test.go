package orders_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// fakeProduction is Manufacturing's production read: what it makes for which
// line, and which variants have a BoM. It notes every call, so the lock rule
// holds it out of a transaction.
type fakeProduction struct {
	mu      sync.Mutex
	made    []contracts.LineProduction
	hasBom  map[int32]bool
	notMade []contracts.LineNotMade
}

func (f *fakeProduction) ForOrderLines(ctx context.Context, ids []int64) ([]contracts.LineProduction, error) {
	lockedContractCalls.note(ctx, "fake Production.ForOrderLines")
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.LineProduction
	for _, p := range f.made {
		for _, id := range ids {
			if p.LineID == id {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func (f *fakeProduction) HasBOM(ctx context.Context, ids []int32) (map[int32]bool, error) {
	lockedContractCalls.note(ctx, "fake Production.HasBOM")
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int32]bool{}
	for _, id := range ids {
		out[id] = f.hasBom[id]
	}
	return out, nil
}

func (f *fakeProduction) NotMade(ctx context.Context, _ int64) ([]contracts.LineNotMade, error) {
	lockedContractCalls.note(ctx, "fake Production.NotMade")
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notMade, nil
}

// SalesOrderLines answers an order's product lines naming a variant, with
// their open quantity, and nothing of a quote (manufacturing design M43).
func TestSalesOrderLines_AnswersAnOrdersGoodsLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, append([]string{"orders:quotes-manage"}, stockKeys...)...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "50")
	free := line(gen.OrdersLineInputKindProduct)
	free.Name, free.Quantity, free.UnitPrice, free.VatCodeId = str("Freight"), f64(1), f64(200), i32(codeStandard)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 8), free))
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))

	p := orders.Module().SalesOrderLines(h.Deps())
	got, err := p.OrderLines(context.Background(), o.Id)
	if err != nil || len(got) != 1 {
		t.Fatalf("OrderLines = %+v, %v", got, err)
	}
	l := got[0]
	if l.LineID != lineOf(t, o, varBolt).Id || l.OrderID != o.Id || l.OrderNumber != o.Number || l.CustomerID != custBusiness ||
		l.VariantID != varBolt || l.Quantity != "8" || l.SiteID == nil || *l.SiteID != siteOslo || l.OrderStatus != "confirmed" {
		t.Errorf("line = %+v", l)
	}
	byID, err := p.Lines(context.Background(), []int64{l.LineID, q.Lines[0].Id, 999999})
	if err != nil || len(byID) != 1 || byID[0].LineID != l.LineID {
		t.Errorf("Lines = %+v, %v", byID, err)
	}
}

// orders.mto-reserve marks the line produced, ready to reserve, once per
// manufacturing order — a backorder chain's orders summed, one order's event
// handled twice counted once —, says when the goods were made at another
// site (M48a), and names the manufacturing order only to a caller holding
// manufacturing:access.
func TestMtoReserve_MarksTheLineReady(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "50")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 8)))
	bolt := lineOf(t, o, varBolt)

	done := func(mo, site int64, produced string) {
		t.Helper()
		ctx := context.Background()
		tx, err := h.Pool().Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ev := contracts.ManufacturingOrderDone{ManufacturingOrderID: mo, SalesOrderID: o.Id, SalesOrderLineID: bolt.Id, SiteID: site,
			Produced: produced, ClosedAt: time.Now()}
		if err := events.Publish(ctx, tx, "manufacturing", ev, h.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := events.NewDispatcher(h.Pool(), orders.Module().EventHandlers(h.Deps()), h.Deps().Logger, h.Now).RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	both := h.SignIn(t, append([]string{"manufacturing:access"}, stockKeys...)...)
	done(1001, siteOslo, "5")
	r := lineOf(t, getOrder(t, c, o.Id), varBolt).ReadyToReserve
	if r == nil || r.ManufacturingOrderId != nil || r.Produced != 5 || r.OtherSite {
		t.Fatalf("ready = %+v", r)
	}
	if r := lineOf(t, getOrder(t, both, o.Id), varBolt).ReadyToReserve; r == nil || r.ManufacturingOrderId == nil || *r.ManufacturingOrderId != 1001 {
		t.Fatalf("ready to manufacturing:access = %+v", r)
	}
	// The backorder of 1001 closes at another site; then 1001's event is
	// handled again, as a republished event would be.
	done(1002, siteBergen, "3")
	done(1001, siteOslo, "5")
	r = lineOf(t, getOrder(t, both, o.Id), varBolt).ReadyToReserve
	if r == nil || r.ManufacturingOrderId == nil || *r.ManufacturingOrderId != 1002 || r.Produced != 8 || !r.OtherSite {
		t.Fatalf("ready over the backorder chain = %+v", r)
	}
}

// An order's goods lines show what Manufacturing makes for them when it
// answers, and nothing when it is off (M45, M46a).
func TestOrder_ShowsProduction(t *testing.T) {
	t.Parallel()
	prod := &fakeProduction{hasBom: map[int32]bool{varBolt: true}}
	h := newHarness(t, harnessOptions{stock: true, production: prod})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "50")
	h.stock.put(varFood, locA1, "50")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2), productLine(varFood, 1)))
	bolt := lineOf(t, o, varBolt)
	finish := time.Date(2026, 12, 3, 12, 0, 0, 0, time.UTC)
	prod.mu.Lock()
	prod.made = []contracts.LineProduction{{LineID: bolt.Id, OrderID: 7, Number: "MO-7", Status: "confirmed", Quantity: "2", Produced: "0.5",
		SiteID: siteOslo, PlannedFinish: &finish}}
	prod.mu.Unlock()

	res := c.Do(http.MethodGet, orderPath(o.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	got := orderOf(t, res)
	b, f := lineOf(t, got, varBolt), lineOf(t, got, varFood)
	if b.HasBom == nil || !*b.HasBom || b.Production == nil || len(*b.Production) != 1 || (*b.Production)[0].Number != "MO-7" ||
		(*b.Production)[0].Produced != 0.5 {
		t.Errorf("bolt = %+v %+v", b.HasBom, b.Production)
	}
	if f.HasBom == nil || *f.HasBom || f.Production == nil || len(*f.Production) != 0 {
		t.Errorf("food = %+v %+v", f.HasBom, f.Production)
	}
	if b.NotMade != nil || f.NotMade != nil {
		t.Errorf("not made with no reasons = %+v %+v", b.NotMade, f.NotMade)
	}

	// Why make-to-order made nothing (M44a): a line's own reason, and the
	// whole order's failure on a line with a BoM and no manufacturing
	// order; never on a line that has one.
	food := lineOf(t, o, varFood)
	prod.mu.Lock()
	prod.notMade = []contracts.LineNotMade{{LineID: food.Id, Code: "no_site", Detail: "No site."}, {Code: contracts.NotMadeFailed, Detail: "Failed."}}
	prod.mu.Unlock()
	got = getOrder(t, c, o.Id)
	if b, f := lineOf(t, got, varBolt), lineOf(t, got, varFood); b.NotMade != nil || f.NotMade == nil || f.NotMade.Code != "no_site" {
		t.Errorf("not made = %+v %+v", b.NotMade, f.NotMade)
	}
	prod.mu.Lock()
	prod.made = nil
	prod.mu.Unlock()
	if b := lineOf(t, getOrder(t, c, o.Id), varBolt); b.NotMade == nil || b.NotMade.Code != contracts.NotMadeFailed {
		t.Errorf("a failed request on a line with a BoM = %+v", b.NotMade)
	}

	off := newHarness(t, harnessOptions{stock: true})
	oc := off.SignIn(t, stockKeys...)
	setDefaultSite(t, oc, siteOslo)
	off.stock.put(varBolt, locA1, "50")
	plain := createOrder(t, oc, custBusiness, productLine(varBolt, 2))
	if l := lineOf(t, plain, varBolt); l.HasBom != nil || l.Production != nil {
		t.Errorf("with Manufacturing off = %+v %+v", l.HasBom, l.Production)
	}
}

// TestSalesOrderLines_Unreserved is manufacturing phase 2 M107a: per open
// goods line of a confirmed sales order at the site, the open quantity less
// what it holds reserved — a line confirmed without reserving counted
// whole, a reserved one not at all, a draft's never, and only the variants
// asked.
func TestSalesOrderLines_Unreserved(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "5")
	h.stock.put(varFood, locA1, "100")

	reserved := createOrder(t, c, custBusiness, productLine(varFood, 2))
	mustStatus(t, confirmWith(t, c, reserved, true), http.StatusOK, "confirm reserving")
	open := createOrder(t, c, custBusiness, productLine(varFood, 3), productLine(varBolt, 8))
	mustStatus(t, confirmWith(t, c, open, false), http.StatusOK, "confirm without reserving")
	createOrder(t, c, custBusiness, productLine(varFood, 1)) // a draft

	p := orders.Module().SalesOrderLines(h.Deps())
	got, err := p.Unreserved(t.Context(), []int32{varFood}, siteOslo)
	if err != nil {
		t.Fatalf("Unreserved: %v", err)
	}
	if len(got) != 1 || got[0].OrderID != open.Id || got[0].VariantID != varFood || got[0].Quantity != "3" {
		t.Errorf("Unreserved = %+v, want the unreserved order's food line of 3", got)
	}
	if got, _ := p.Unreserved(t.Context(), []int32{varFood}, siteOslo+100); len(got) != 0 {
		t.Errorf("another site = %+v", got)
	}
}
