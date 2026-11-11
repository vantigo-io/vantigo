package manufacturing_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// fakeSalesOrderLines is contracts.SalesOrderLines over the lines a test
// seeds. It notes every call, so the lock rule holds it out of a
// transaction.
type fakeSalesOrderLines struct {
	mu    sync.Mutex
	lines []contracts.SalesOrderLine
}

func (f *fakeSalesOrderLines) seed(lines ...contracts.SalesOrderLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append(f.lines, lines...)
}

func (f *fakeSalesOrderLines) Lines(ctx context.Context, ids []int64) ([]contracts.SalesOrderLine, error) {
	lockedContractCalls.note(ctx, "fake SalesOrderLines.Lines")
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.SalesOrderLine
	for _, l := range f.lines {
		if slices.Contains(ids, l.LineID) {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeSalesOrderLines) OrderLines(ctx context.Context, id int64) ([]contracts.SalesOrderLine, error) {
	lockedContractCalls.note(ctx, "fake SalesOrderLines.OrderLines")
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []contracts.SalesOrderLine
	for _, l := range f.lines {
		if l.OrderID == id {
			out = append(out, l)
		}
	}
	return out, nil
}

// customerDirectory names the customers of the sales orders.
type customerDirectory struct {
	contracts.CustomerDirectory
}

func (customerDirectory) Customer(ctx context.Context, id int32) (*contracts.CustomerEntry, error) {
	lockedContractCalls.note(ctx, "fake Directory.Customer")
	return &contracts.CustomerEntry{ID: id, Name: "Fjord AS"}, nil
}

const salesOrder int64 = 77

// mtoHarness is a harness with Orders' lines and the customer directory
// faked.
func mtoHarness(t *testing.T, opts ...modtest.Option) (*harness, *fakeSalesOrderLines) {
	t.Helper()
	lines := &fakeSalesOrderLines{}
	h := newHarness(t, append([]modtest.Option{modtest.WithSalesOrderLines(lines), modtest.WithDirectory(customerDirectory{})}, opts...)...)
	return h, lines
}

// mtoBikeBom is bikeBom made to order.
func mtoBikeBom(t *testing.T, c *modtest.Client) gen.ManufacturingBom {
	t.Helper()
	b := bikeBom(t, c)
	up := updateOf(b)
	yes := true
	up.MakeToOrder = &yes
	res := c.Do(http.MethodPut, bomPath(b.Id), up)
	mustStatus(t, res, http.StatusOK, "make the bicycle to order")
	return bomOf(t, res)
}

// publish puts events on the outbox as module and runs the module's
// handlers over them once.
func publish(t *testing.T, h *harness, module string, evs ...contracts.Event) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, ev := range evs {
		if err := events.Publish(ctx, tx, module, ev, h.Now()); err != nil {
			t.Fatalf("publish %s: %v", ev.EventName(), err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	handlers := manufacturing.Module().EventHandlers(h.Deps())
	if err := events.NewDispatcher(h.Pool(), handlers, h.Deps().Logger, h.Now).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
}

func confirmed(id int64) contracts.SalesOrderConfirmed {
	return contracts.SalesOrderConfirmed{OrderID: id, Number: 1042, Version: 1, CustomerID: 5, Currency: "NOK", NetTotal: "1.00", ConfirmedAt: time.Now()}
}

type mtoRequest struct {
	status  string
	reasons map[int64]string
}

func requestOf(t *testing.T, h *harness, salesOrderID int64) mtoRequest {
	t.Helper()
	var r mtoRequest
	var body []byte
	if err := h.Pool().QueryRow(context.Background(), `SELECT status, reasons FROM manufacturing.mto_requests WHERE sales_order_id = $1`,
		salesOrderID).Scan(&r.status, &body); err != nil {
		t.Fatalf("read the request: %v", err)
	}
	var reasons []struct {
		LineID int64  `json:"lineId"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(body, &reasons); err != nil {
		t.Fatalf("reasons: %v", err)
	}
	r.reasons = map[int64]string{}
	for _, x := range reasons {
		r.reasons[x.LineID] = x.Code
	}
	return r
}

func runMto(t *testing.T, h *harness) int {
	t.Helper()
	n, err := manufacturing.MtoCycle(context.Background(), h.Deps())
	if err != nil {
		t.Fatalf("MtoCycle: %v", err)
	}
	return n
}

// TestMto_HandlerQueuesOnceAndWorkerMakesOnce is M44 and M44a: the handler
// queues one request per sales order however often the event is delivered;
// the worker makes one confirmed order per make-to-order line — shortage
// never stops it — skips the others with their reasons, and makes nothing
// twice.
func TestMto_HandlerQueuesOnceAndWorkerMakesOnce(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	mtoBikeBom(t, c)
	createBom(t, c, bomInput(varFrame, "1", line(varPaint, "1")))
	delivery := time.Date(2026, 12, 10, 0, 0, 0, 0, time.UTC)
	site := siteBranch
	lines.seed(
		contracts.SalesOrderLine{LineID: 501, OrderID: salesOrder, OrderNumber: 1042, CustomerID: 5, VariantID: varBike, Quantity: "2",
			DeliveryDate: &delivery, SiteID: &site, OrderStatus: "confirmed"},
		contracts.SalesOrderLine{LineID: 502, OrderID: salesOrder, OrderNumber: 1042, CustomerID: 5, VariantID: varWheel, Quantity: "1", OrderStatus: "confirmed"},
		contracts.SalesOrderLine{LineID: 503, OrderID: salesOrder, OrderNumber: 1042, CustomerID: 5, VariantID: varFrame, Quantity: "1", OrderStatus: "confirmed"},
	)

	publish(t, h, "orders", confirmed(salesOrder), confirmed(salesOrder))
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.mto_requests`); n != 1 {
		t.Fatalf("requests after a redelivery = %d, want 1", n)
	}
	if n := runMto(t, h); n != 1 {
		t.Fatalf("finished = %d", n)
	}
	if n := runMto(t, h); n != 0 {
		t.Fatalf("a second cycle finished %d", n)
	}
	r := requestOf(t, h, salesOrder)
	if r.status != "done" || r.reasons[502] != "no_bom" || r.reasons[503] != "not_make_to_order" || r.reasons[501] != "" {
		t.Fatalf("request = %+v", r)
	}
	var page gen.ManufacturingOrderPage
	c.Do(http.MethodGet, ordersPath+"?origin=sales_order", nil).JSON(&page)
	if len(page.Items) != 1 {
		t.Fatalf("orders made = %+v", page.Items)
	}
	o := getOrder(t, c, page.Items[0].Id)
	// The BoM's work centers stand at the main site, whatever the sales
	// order names (M44a); nothing is on hand, so it is confirmed short.
	if o.Status != gen.ManufacturingOrderStatusConfirmed || o.Quantity != "2" || o.SiteId != siteMain ||
		o.Origin.Kind != gen.SalesOrder || *o.Origin.Reference != "SO-1042" || *o.Origin.LineId != 501 || *o.Origin.OrderId != salesOrder ||
		o.DueDate == nil || !o.DueDate.Equal(delivery) || o.ReservationStatus == nil || *o.ReservationStatus != gen.ManufacturingReservationStatusNotReserved {
		t.Fatalf("made = %+v", o)
	}
	if o.Origin.CustomerName != nil {
		t.Errorf("the customer was named to a caller without orders:access: %v", *o.Origin.CustomerName)
	}

	// A second confirm of the order (a new event) finds its request and adds
	// nothing; the line keeps its one order.
	publish(t, h, "orders", confirmed(salesOrder))
	runMto(t, h)
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.orders WHERE origin_kind = 'sales_order'`); n != 1 {
		t.Errorf("orders after a second confirm = %d", n)
	}
}

// TestMto_NoSiteSkipped is M44a's edge: a BoM with no operations takes the
// sales order's site; with none on the order either the line is skipped.
func TestMto_NoSiteSkipped(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	in := bomInput(varFrame, "1", line(varPaint, "1"))
	yes := true
	in.MakeToOrder = &yes
	createBom(t, c, in)
	site := siteBranch
	lines.seed(
		contracts.SalesOrderLine{LineID: 601, OrderID: 80, OrderNumber: 9, VariantID: varFrame, Quantity: "1", OrderStatus: "confirmed"},
		contracts.SalesOrderLine{LineID: 701, OrderID: 81, OrderNumber: 10, VariantID: varFrame, Quantity: "3", SiteID: &site, OrderStatus: "confirmed"},
	)
	publish(t, h, "orders", confirmed(80), confirmed(81))
	runMto(t, h)
	if r := requestOf(t, h, 80); r.status != "skipped" || r.reasons[601] != "no_site" {
		t.Errorf("no site = %+v", r)
	}
	if r := requestOf(t, h, 81); r.status != "done" {
		t.Errorf("with a site = %+v", r)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.orders WHERE site_id = $1 AND origin_line_id = 701`, siteBranch); n != 1 {
		t.Errorf("orders at the sales order's site = %d", n)
	}
}

// TestMto_WorkerOnAPoolOfOne is the worker's connection rule: it reads the
// contract and judges the lines holding no connection, so a pool of one
// finishes the request.
func TestMto_WorkerOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	mtoBikeBom(t, c)
	lines.seed(contracts.SalesOrderLine{LineID: 801, OrderID: 90, OrderNumber: 1, VariantID: varBike, Quantity: "1", OrderStatus: "confirmed"})
	publish(t, h, "orders", confirmed(90))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	if n, err := manufacturing.MtoCycle(ctx, h.DepsOn(one)); err != nil || n != 1 {
		t.Fatalf("MtoCycle on a pool of one = %d, %v", n, err)
	}
	if r := requestOf(t, h, 90); r.status != "done" {
		t.Errorf("request = %+v", r)
	}
}

// TestMto_CancelledOriginFlagged is M44's cancel: the orders made for the
// sales order are flagged, not cancelled, and say so on their timeline; the
// customer is named to a caller who also holds orders:access.
func TestMto_CancelledOriginFlagged(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	mtoBikeBom(t, c)
	lines.seed(contracts.SalesOrderLine{LineID: 901, OrderID: 91, OrderNumber: 5, CustomerID: 5, VariantID: varBike, Quantity: "1", OrderStatus: "confirmed"})
	publish(t, h, "orders", confirmed(91))
	runMto(t, h)
	publish(t, h, "orders", contracts.SalesOrderCancelled{OrderID: 91, Number: 5, CustomerID: 5, CancelledAt: time.Now()},
		contracts.SalesOrderCancelled{OrderID: 91, Number: 5, CustomerID: 5, CancelledAt: time.Now()})

	var page gen.ManufacturingOrderPage
	c.Do(http.MethodGet, ordersPath+"?origin=sales_order", nil).JSON(&page)
	if len(page.Items) != 1 || !page.Items[0].Origin.Cancelled || page.Items[0].Status != gen.ManufacturingOrderStatusConfirmed {
		t.Fatalf("list = %+v", page.Items)
	}
	var timeline gen.ManufacturingOrderEventList
	c.Do(http.MethodGet, orderPath(page.Items[0].Id, "events"), nil).JSON(&timeline)
	flagged := 0
	for _, e := range timeline.Items {
		if e.Kind == gen.ManufacturingOrderEventKindOriginCancelled {
			flagged++
		}
	}
	if flagged != 1 {
		t.Errorf("origin_cancelled events = %d, want 1", flagged)
	}
	both := h.SignIn(t, append([]string{"orders:access"}, planner...)...)
	if o := getOrder(t, both, page.Items[0].Id); o.Origin.CustomerName == nil || *o.Origin.CustomerName != "Fjord AS" {
		t.Errorf("customer to orders:access = %v", o.Origin.CustomerName)
	}
}

// TestMto_ManualOrderForALine is M45 and M44b: the sales order line read for
// the form, an order created for it, a second one refused, and a line of
// another variant refused.
func TestMto_ManualOrderForALine(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	b := bikeBom(t, c)
	lines.seed(contracts.SalesOrderLine{LineID: 1001, OrderID: 92, OrderNumber: 6, VariantID: varBike, Quantity: "4", OrderStatus: "confirmed"})

	var l gen.ManufacturingSalesOrderLine
	res := c.Do(http.MethodGet, base+"/sales-order-lines/1001", nil)
	mustStatus(t, res, http.StatusOK, "read the line")
	res.JSON(&l)
	if l.Reference != "SO-6" || l.Quantity != "4" || l.BomId == nil || *l.BomId != b.Id || len(l.Orders) != 0 {
		t.Fatalf("line = %+v", l)
	}
	mustStatus(t, c.Do(http.MethodGet, base+"/sales-order-lines/999", nil), http.StatusNotFound, "an unknown line")

	in := orderInput(varBike, "4")
	id := int64(1001)
	in.SalesOrderLineId = &id
	o := createOrder(t, c, in)
	if o.Origin.Kind != gen.SalesOrder || *o.Origin.Reference != "SO-6" {
		t.Fatalf("origin = %+v", o.Origin)
	}
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, in), http.StatusConflict, "sales_order_line_has_order", "a second order for the line")
	wrong := orderInput(varFrame, "1")
	createBom(t, c, bomInput(varFrame, "1", line(varPaint, "1")))
	wrong.SalesOrderLineId = &id
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, wrong), http.StatusBadRequest, "sales_order_line_unknown", "a line of another variant")

	// A cancelled order frees the line (M44b).
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), revisionInput(o.Revision)), http.StatusOK, "cancel")
	createOrder(t, c, in)
}

// TestMto_DonePublishedForSalesOrderOrigin is M48: closing an order made
// for a sales order line publishes manufacturing.order.done on the outbox;
// a manual one publishes nothing.
func TestMto_DonePublishedForSalesOrderOrigin(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	bikeBom(t, c)
	h.stock.put(varWheel, locMainA1, "100")
	h.stock.put(varBolt, locMainA1, "100")
	lines.seed(contracts.SalesOrderLine{LineID: 1101, OrderID: 93, OrderNumber: 7, VariantID: varBike, Quantity: "1", OrderStatus: "confirmed"})

	in := orderInput(varBike, "1")
	id := int64(1101)
	in.SalesOrderLineId = &id
	made := confirmOrder(t, c, createOrder(t, c, in)).Order
	manual := confirmOrder(t, c, createOrder(t, c, orderInput(varBike, "1"))).Order
	for _, o := range []gen.ManufacturingOrder{made, manual} {
		post := gen.ManufacturingProductionInput{ClientRequestId: "all-" + o.Number, Quantity: "1", Lines: []gen.ManufacturingProductionLineInput{}}
		mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "productions"), post), http.StatusCreated, "produce "+o.Number)
		o = getOrder(t, c, o.Id)
		mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "close"), gen.ManufacturingCloseInput{Revision: o.Revision}), http.StatusOK, "close "+o.Number)
	}
	var payload []byte
	if n := h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'manufacturing.order.done'`); n != 1 {
		t.Fatalf("done events = %d, want 1", n)
	}
	if err := h.Pool().QueryRow(context.Background(), `SELECT payload FROM platform.events WHERE name = 'manufacturing.order.done'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var ev contracts.ManufacturingOrderDone
	if err := json.Unmarshal(payload, &ev); err != nil || ev.ManufacturingOrderID != made.Id || ev.SalesOrderLineID != 1101 ||
		ev.SalesOrderID != 93 || ev.Produced != "1" || ev.SiteID != siteMain {
		t.Errorf("done = %+v, %v", ev, err)
	}
}

// TestMto_ChildOrders is M47 and M37: one confirmed child per chosen
// component with a default BoM and something missing, origin parent, due
// the parent's requested start when it is unplanned; a line missing
// nothing and a bought one are refused.
func TestMto_ChildOrders(t *testing.T) {
	t.Parallel()
	h, _ := mtoHarness(t)
	c := h.SignIn(t, planner...)
	createBom(t, c, bomInput(varFrame, "1", line(varPaint, "2")))
	in := bomInput(varBike, "1", line(varFrame, "1"), line(varWheel, "2"))
	createBom(t, c, in)
	h.stock.setAvailable(siteMain, varFrame, "1")

	site := siteMain
	parent := createOrder(t, c, gen.ManufacturingOrderInput{VariantId: varBike, Quantity: "3", SiteId: &site,
		RequestedStart: &openapi_types.Date{Time: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}})
	parent = confirmOrder(t, c, parent).Order
	frame, wheel := component(t, parent, varFrame), component(t, parent, varWheel)
	if frame.DefaultBomId == nil || wheel.DefaultBomId != nil {
		t.Fatalf("default BoMs: frame %v, wheel %v", frame.DefaultBomId, wheel.DefaultBomId)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(parent.Id, "children"),
		gen.ManufacturingChildrenInput{Revision: parent.Revision, ComponentIds: []int64{*wheel.Id}}), http.StatusBadRequest, "bom_required", "a bought component")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(parent.Id, "children"),
		gen.ManufacturingChildrenInput{Revision: parent.Revision, ComponentIds: []int64{424242}}), http.StatusBadRequest, "component_unknown", "not a line")

	res := c.Do(http.MethodPost, orderPath(parent.Id, "children"), gen.ManufacturingChildrenInput{Revision: parent.Revision, ComponentIds: []int64{*frame.Id}})
	mustStatus(t, res, http.StatusOK, "create the sub-assembly orders")
	var out gen.ManufacturingChildrenCreated
	res.JSON(&out)
	if len(out.Children) != 1 || len(out.Order.Children) != 1 || out.Children[0].Status != gen.ManufacturingOrderStatusConfirmed {
		t.Fatalf("children = %+v / %+v", out.Children, out.Order.Children)
	}
	child := getOrder(t, c, out.Children[0].Id)
	// 3 frames required, 1 available: 2 missing.
	if child.Quantity != "2" || child.Origin.Kind != gen.Parent || child.Parent == nil || child.Parent.Id != parent.Id ||
		child.DueDate == nil || !child.DueDate.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("child = %+v", child)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(parent.Id, "children"),
		gen.ManufacturingChildrenInput{Revision: parent.Revision, ComponentIds: []int64{*frame.Id}}), http.StatusConflict, "revision_conflict", "a stale revision")
	h.stock.setAvailable(siteMain, varFrame, "10")
	parent = getOrder(t, c, parent.Id)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(parent.Id, "children"),
		gen.ManufacturingChildrenInput{Revision: parent.Revision, ComponentIds: []int64{*frame.Id}}), http.StatusBadRequest, "nothing_missing", "nothing missing")
}

// TestMto_ProductionGated is M46a: the provider answers what is made for
// the lines to a caller holding manufacturing:access, and nothing to one
// without it or to a context with no request.
func TestMto_ProductionGated(t *testing.T) {
	t.Parallel()
	h, lines := mtoHarness(t)
	c := h.SignIn(t, planner...)
	b := bikeBom(t, c)
	lines.seed(contracts.SalesOrderLine{LineID: 1201, OrderID: 94, OrderNumber: 8, VariantID: varBike, Quantity: "1", OrderStatus: "confirmed"})
	in := orderInput(varBike, "1")
	id := int64(1201)
	in.SalesOrderLineId = &id
	createOrder(t, c, in)

	p := manufacturing.Module().Production(h.Deps())
	if got, err := p.ForOrderLines(context.Background(), []int64{1201}); err != nil || len(got) != 0 {
		t.Errorf("without a request = %+v, %v", got, err)
	}
	if got, err := p.HasBOM(context.Background(), []int32{varBike}); err != nil || got[varBike] {
		t.Errorf("HasBOM without a request = %+v, %v", got, err)
	}
	_, readerID := h.SignInUser(t, "manufacturing:access")
	reader := contracts.AsUser(context.Background(), readerID)
	got, err := p.ForOrderLines(reader, []int64{1201})
	if err != nil || len(got) != 1 || got[0].Number != "MO-1" || got[0].Status != "draft" || got[0].SiteID != siteMain {
		t.Errorf("to a reader = %+v, %v", got, err)
	}
	if has, err := p.HasBOM(reader, []int32{varBike, varWheel}); err != nil || !has[varBike] || has[varWheel] {
		t.Errorf("HasBOM to a reader = %+v, %v (BoM %d)", has, err, b.Id)
	}
	_, otherID := h.SignInUser(t, "orders:access")
	if got, err := p.ForOrderLines(contracts.AsUser(context.Background(), otherID), []int64{1201}); err != nil || len(got) != 0 {
		t.Errorf("to a caller without manufacturing:access = %+v, %v", got, err)
	}
}
