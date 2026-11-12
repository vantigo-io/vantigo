package inventory_test

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The trace (manufacturing phase 2 T, M87–M92): the walk over receipts,
// productions and deliveries both ways, with the multi-level productions,
// the lot reached twice and the unbuild that is never expanded; who received
// a lot, gated by Orders and by customers:view; the CSV and the PDF; and the
// block with its downstream list, walked again inside its transaction.

// The traced variants: flour (a component), dough (made of flour) and bread
// (made of dough and flour), all kept in lots.
const (
	varFlour int32 = 701
	varDough int32 = 702
	varBread int32 = 703
)

const (
	tracePath = "/api/v1/inventory/trace"
	// traceDelivery is the Orders delivery the bread was issued for.
	traceDelivery int64 = 71
	traceCustomer int32 = 42
)

// fakeDeliveryTrace is Orders' contracts.DeliveryTrace as its provider keeps
// it: refused without orders:access, else the posted deliveries asked.
type fakeDeliveryTrace struct {
	mu     sync.Mutex
	access contracts.Access
	calls  int
}

func (f *fakeDeliveryTrace) Deliveries(ctx context.Context, ids []int64) ([]contracts.TracedDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.access == nil || !contracts.HasPermission(ctx, f.access, "orders:access") {
		return nil, contracts.ErrForbidden
	}
	var out []contracts.TracedDelivery
	for _, id := range ids {
		if id == traceDelivery {
			out = append(out, contracts.TracedDelivery{DeliveryID: id, OrderID: 9, DeliveryNumber: 3001, OrderNumber: 2001, CustomerID: traceCustomer,
				PostedAt: testToday})
		}
	}
	return out, nil
}

// fakeCustomerNames is the customer directory's batch read, the one call
// the trace makes of it.
type fakeCustomerNames struct {
	contracts.CustomerDirectory
}

func (fakeCustomerNames) Customers(_ context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	var out []contracts.CustomerEntry
	for _, id := range ids {
		if id == traceCustomer {
			out = append(out, contracts.CustomerEntry{ID: id, Name: "Bakeriet AS"})
		}
	}
	return out, nil
}

// tracedLots is the lots of the traced graph.
type tracedLots struct {
	site                     ledgerSite
	flour, dough, bread, bun int64
	unbuild                  int64 // the unbuild's movement
}

// seedTrace builds the graph every trace test reads:
//
//	flour F1 (received from Mølle AS) ──P1──▶ dough D1 ──P2──▶ bread B1 ──▶ delivery 71
//	   │                                     │  (with F1 again)
//	   └──────────────────P2─────────────────┘──P3──▶ bun B2 ──unbuild──▶ D1
//
// P1 also consumes untracked bolts; the unbuild takes B2 apart into D1.
func seedTrace(t *testing.T, h *harness, c *modtest.Client) tracedLots {
	t.Helper()
	s := newLedgerSite(t, h, c)
	tracked := func(id, product int32, sku, name string) contracts.VariantEntry {
		v := goods(id, product, sku, name, "kg")
		v.Tracking = contracts.TrackingLot
		return v
	}
	bread := tracked(varBread, 73, "BREAD-1", "Bread")
	gtin := "4006381333931"
	bread.Barcode = &gtin
	h.catalog.seed(tracked(varFlour, 71, "FLOUR-1", "Flour"), tracked(varDough, 72, "DOUGH-1", "Dough"), bread)
	receipt := gen.InventoryReceiptInput{ClientRequestId: uuid.New(), SiteId: s.site.Id, ToLocationId: s.shelf1.Id,
		SupplierName: ptr("Mølle AS"), SupplierReference: ptr("PO-1"),
		Lines: []gen.InventoryReceiptLine{lotLine(varFlour, 10, gen.InventoryNewLot{Number: ptr("F1")}), receiptLine(varBolt, 10)}}
	mustStatus(t, c.Do(http.MethodPost, receiptsPath, receipt), http.StatusCreated, "the flour's receipt")
	out := tracedLots{site: s, flour: lotID(t, h, varFlour, "F1")}
	l := ledgerOf(h)
	mo := contracts.StockSource{Module: "manufacturing", Kind: "order", ID: 5}
	ensure := func(variant int32, number string) int64 {
		var ids []int64
		if err := inTx(t, h, func(tx pgx.Tx) error {
			var err error
			ids, err = l.EnsureLots(inCallerTx(t.Context()), tx, mo, testToday, []contracts.NewLot{{VariantID: variant, Number: number, ProductionDate: testToday}})
			return err
		}); err != nil {
			t.Fatalf("EnsureLots %s: %v", number, err)
		}
		return ids[0]
	}
	produce := func(id int64, unbuild bool, consume []contracts.ConsumeLine, made []contracts.ProduceLine) int64 {
		kind := "production"
		if unbuild {
			kind = "unbuild"
		}
		var posted contracts.StockPosted
		if err := inTx(t, h, func(tx pgx.Tx) error {
			var err error
			posted, err = l.Produce(inCallerTx(t.Context()), tx, mo, contracts.StockProduction{
				Document: contracts.StockSource{Module: "manufacturing", Kind: kind, ID: id}, SiteID: s.site.Id, Today: testToday, Unbuild: unbuild,
				Consume: consume, Produce: made})
			return err
		}); err != nil {
			t.Fatalf("Produce %s %d: %v", kind, id, err)
		}
		return posted.MovementID
	}
	shelf1, shelf2 := s.shelf1.Id, s.shelf2.Id
	out.dough = ensure(varDough, "D1")
	produce(1, false, []contracts.ConsumeLine{
		{VariantID: varFlour, LineID: 1, FromLocationID: shelf1, Quantity: "4", LotID: out.flour},
		{VariantID: varBolt, LineID: 2, FromLocationID: shelf1, Quantity: "1"},
	}, []contracts.ProduceLine{{VariantID: varDough, LineID: 1, ToLocationID: shelf2, Quantity: "4", LotID: out.dough}})
	out.bread = ensure(varBread, "B1")
	produce(2, false, []contracts.ConsumeLine{
		{VariantID: varDough, LineID: 1, FromLocationID: shelf2, Quantity: "3", LotID: out.dough},
		{VariantID: varFlour, LineID: 2, FromLocationID: shelf1, Quantity: "1", LotID: out.flour},
	}, []contracts.ProduceLine{{VariantID: varBread, LineID: 1, ToLocationID: shelf2, Quantity: "3", LotID: out.bread}})
	out.bun = ensure(varBread, "B2")
	produce(3, false, []contracts.ConsumeLine{{VariantID: varDough, LineID: 1, FromLocationID: shelf2, Quantity: "1", LotID: out.dough}},
		[]contracts.ProduceLine{{VariantID: varBread, LineID: 1, ToLocationID: shelf2, Quantity: "1", LotID: out.bun}})
	out.unbuild = produce(4, true, []contracts.ConsumeLine{{VariantID: varBread, LineID: 1, FromLocationID: shelf2, Quantity: "1", LotID: out.bun}},
		[]contracts.ProduceLine{{VariantID: varDough, LineID: 1, ToLocationID: shelf2, Quantity: "1", LotID: out.dough}})
	if _, err := issueOf(t, h, traceDelivery, contracts.StockIssue{SiteID: s.site.Id, Today: testToday,
		Lines: []contracts.IssueLine{{VariantID: varBread, LineID: 1, FromLocationID: shelf2, Quantity: "2", LotID: out.bread}}}); err != nil {
		t.Fatalf("the delivery's issue: %v", err)
	}
	return out
}

// getTrace reads a trace, failing unless it answers 200.
func getTrace(t *testing.T, c *modtest.Client, query string) gen.InventoryTrace {
	t.Helper()
	res := c.Do(http.MethodGet, tracePath+"?"+query, nil)
	mustStatus(t, res, http.StatusOK, "the trace "+query)
	var out gen.InventoryTrace
	res.JSON(&out)
	return out
}

// shape is a trace's nodes as "key kind lot quantity" lines, with the marks
// a reader sees: seen, a delivery and the depth limit.
func shape(tr gen.InventoryTrace) []string {
	out := make([]string, 0, len(tr.Nodes))
	for _, n := range tr.Nodes {
		line := n.Key + " " + string(n.Kind)
		if n.Lot != nil {
			line += " " + n.Lot.Number
		}
		switch {
		case n.Edge != nil:
			line += " " + strconv.FormatFloat(n.Edge.Consumed, 'f', -1, 64) + "→" + strconv.FormatFloat(n.Edge.Produced, 'f', -1, 64)
		case n.Quantity != nil:
			line += " " + strconv.FormatFloat(*n.Quantity, 'f', -1, 64)
		}
		if n.SeenAt != nil {
			line += " seen@" + *n.SeenAt
		}
		if n.DepthLimit {
			line += " limit"
		}
		if n.Delivery != nil {
			line += " delivery " + strconv.FormatInt(n.Delivery.DeliveryNumber, 10)
			if n.Delivery.CustomerName != nil {
				line += " " + *n.Delivery.CustomerName
			}
		}
		out = append(out, line)
	}
	return out
}

func expectShape(t *testing.T, what string, tr gen.InventoryTrace, want ...string) {
	t.Helper()
	if got := shape(tr); !slices.Equal(got, want) {
		t.Errorf("%s:\n got %s\nwant %s", what, strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}

// newTraceHarness is a harness with Orders' delivery trace and the
// customer directory beside Inventory.
func newTraceHarness(t *testing.T) (*harness, *fakeDeliveryTrace) {
	t.Helper()
	trace := &fakeDeliveryTrace{}
	h := newHarness(t, modtest.WithDeliveryTrace(trace), modtest.WithDirectory(fakeCustomerNames{}))
	trace.access = h.Deps().Access
	return h, trace
}

// TestTrace_BothWays is a three-level graph walked both ways: usage from the
// flour through the dough to the bread and its delivery, the flour reached
// again through the bread's own production shown once and referenced after;
// origin from the bread back to the receipt, with the untracked components
// when asked; and the unbuild an end on both lots, never expanded.
func TestTrace_BothWays(t *testing.T) {
	t.Parallel()
	h, _ := newTraceHarness(t)
	c, _ := signIn(t, h, everything...)
	lots := seedTrace(t, h, c)
	viewer, _ := signIn(t, h, append(slices.Clone(stockViewer), "orders:access", permCustomersView)...)

	usage := getTrace(t, viewer, "lotId="+strconv.FormatInt(lots.flour, 10))
	if usage.Direction != gen.Usage || usage.Components != gen.Tracked || usage.MaxDepth != 10 || usage.Deliveries != gen.Shown || !usage.CustomerNames {
		t.Errorf("usage = %+v", usage)
	}
	expectShape(t, "usage from the flour", usage,
		"0 lot F1",
		"0.0 lot D1 4→4",
		"0.0.0 lot B1 3→3",
		"0.0.0.0 issue 2 delivery 3001 Bakeriet AS",
		"0.0.1 lot B2 1→1",
		"0.0.1.0 unbuild 1",
		"0.1 lot B1 1→3 seen@0.0.0",
	)
	root := usage.Nodes[0].Lot
	if root.QuantityIn != 10 || root.OnHand != 5 || usage.Nodes[1].Lot.QuantityIn != 5 || usage.Nodes[1].Lot.OnHand != 1 {
		t.Errorf("quantities: flour in %v on hand %v, dough in %v on hand %v", root.QuantityIn, root.OnHand, usage.Nodes[1].Lot.QuantityIn, usage.Nodes[1].Lot.OnHand)
	}
	if e := usage.Nodes[1].Edge; e.Source == nil || e.Source.Kind != "production" || e.Source.Id != "1" || e.Source.LineId == nil || *e.Source.LineId != "5" {
		t.Errorf("the dough's edge = %+v, want production 1 of order 5", e)
	}
	if m := usage.Nodes[5].Movement; m == nil || m.Id != lots.unbuild || m.Kind != gen.Unbuild {
		t.Errorf("the unbuild end = %+v", usage.Nodes[5])
	}

	// The unbuild restored the dough from the bun: the dough's origin shows
	// it as an end and does not go into the bun.
	origin := getTrace(t, viewer, "lotId="+strconv.FormatInt(lots.dough, 10)+"&direction=origin")
	expectShape(t, "origin of the dough", origin,
		"0 lot D1",
		"0.0 lot F1 4→4",
		"0.0.0 receipt 10",
		"0.1 unbuild 1",
	)
	if m := origin.Nodes[2].Movement; m.SupplierName == nil || *m.SupplierName != "Mølle AS" || m.SupplierReference == nil || *m.SupplierReference != "PO-1" {
		t.Errorf("the receipt = %+v", m)
	}
	all := getTrace(t, viewer, "lotId="+strconv.FormatInt(lots.bread, 10)+"&direction=origin&components=all")
	expectShape(t, "origin of the bread, with the components", all,
		"0 lot B1",
		"0.0 lot D1 3→3",
		"0.0.0 lot F1 4→4",
		"0.0.0.0 receipt 10",
		"0.0.1 component 1→4",
		"0.0.2 unbuild 1",
		"0.1 lot F1 1→3 seen@0.0.0",
	)
	if n := all.Nodes[4]; n.Variant.Sku != "BOLT-M8" || n.Lot != nil {
		t.Errorf("the component = %+v", n)
	}
	// The bun's usage ends at its unbuild: the restored dough is not its.
	expectShape(t, "usage of the bun", getTrace(t, viewer, "lotId="+strconv.FormatInt(lots.bun, 10)),
		"0 lot B2",
		"0.0 unbuild 1",
	)

	// One level: the dough is shown and not expanded.
	expectShape(t, "usage at depth 1", getTrace(t, viewer, "lotId="+strconv.FormatInt(lots.flour, 10)+"&maxDepth=1"),
		"0 lot F1",
		"0.0 lot D1 4→4 limit",
		"0.1 lot B1 1→3 limit",
	)
	res := viewer.Do(http.MethodGet, tracePath+"?lotId="+strconv.FormatInt(lots.flour, 10)+"&maxDepth=11", nil)
	mustStatus(t, res, http.StatusBadRequest, "a depth of 11")
	mustStatus(t, viewer.Do(http.MethodGet, tracePath+"?lotId=999999", nil), http.StatusNotFound, "an unknown lot")
	locations, _ := signIn(t, h, locationsViewer...)
	mustStatus(t, locations.Do(http.MethodGet, tracePath+"?lotId="+strconv.FormatInt(lots.flour, 10), nil), http.StatusForbidden, "without stock-view")
}

// TestTrace_WhoReceivedIt is M90's gates: without orders:access the delivery
// read is refused and the trace shows the movement alone; with it, the
// delivery and its order; the customer's name only with customers:view; and
// without Orders at all the trace says so.
func TestTrace_WhoReceivedIt(t *testing.T) {
	t.Parallel()
	h, trace := newTraceHarness(t)
	c, _ := signIn(t, h, everything...)
	lots := seedTrace(t, h, c)
	query := "lotId=" + strconv.FormatInt(lots.bread, 10)

	plain, _ := signIn(t, h, stockViewer...)
	tr := getTrace(t, plain, query)
	if tr.Deliveries != gen.Hidden || tr.CustomerNames || tr.Nodes[1].Delivery != nil || tr.Nodes[1].Movement.Source.Id != "71" {
		t.Errorf("without orders:access = %+v / %+v", tr, tr.Nodes[1])
	}
	if trace.calls == 0 {
		t.Error("the delivery trace was not asked: the refusal must come from Orders")
	}
	ordersOnly, _ := signIn(t, h, append(slices.Clone(stockViewer), "orders:access")...)
	tr = getTrace(t, ordersOnly, query)
	if d := tr.Nodes[1].Delivery; tr.Deliveries != gen.Shown || tr.CustomerNames || d == nil || d.OrderNumber != 2001 || d.CustomerId != traceCustomer || d.CustomerName != nil {
		t.Errorf("with orders:access alone = %+v / %+v", tr, d)
	}
	names, _ := signIn(t, h, append(slices.Clone(stockViewer), "orders:access", permCustomersView)...)
	if tr = getTrace(t, names, query); !tr.CustomerNames || tr.Nodes[1].Delivery.CustomerName == nil {
		t.Errorf("with customers:view = %+v", tr.Nodes[1].Delivery)
	}

	// The CSV says the same, by the same gates.
	csv := func(c *modtest.Client) string {
		res := c.Do(http.MethodGet, "/api/v1/inventory/trace.csv?"+query, nil)
		mustStatus(t, res, http.StatusOK, "the trace's CSV")
		if !strings.HasPrefix(res.Header("Content-Disposition"), `attachment; filename="trace-B1-usage.csv"`) {
			t.Errorf("Content-Disposition = %q", res.Header("Content-Disposition"))
		}
		return strings.TrimPrefix(string(res.Body), "\ufeff")
	}
	wantRows := func(c *modtest.Client, rows ...string) {
		t.Helper()
		got := strings.Split(strings.TrimSuffix(csv(c), "\r\n"), "\r\n")
		want := append([]string{"key;depth;kind;lot;sku;product;quantity;unit;date;document;party;note"}, rows...)
		if !slices.Equal(got, want) {
			t.Errorf("CSV:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
		}
	}
	day := h.Now().Format("2006-01-02")
	wantRows(plain,
		"0;0;lot;B1;BREAD-1;Bread;3;kg;"+day+";;;",
		"0.0;1;issue;B1;BREAD-1;Bread;2;kg;"+day+";Delivery, movement "+strconv.FormatInt(tr.Nodes[1].Movement.Id, 10)+";;")
	wantRows(ordersOnly,
		"0;0;lot;B1;BREAD-1;Bread;3;kg;"+day+";;;",
		"0.0;1;issue;B1;BREAD-1;Bread;2;kg;"+day+";Delivery 3001, order 2001;Customer #42;")
	wantRows(names,
		"0;0;lot;B1;BREAD-1;Bread;3;kg;"+day+";;;",
		"0.0;1;issue;B1;BREAD-1;Bread;2;kg;"+day+";Delivery 3001, order 2001;Bakeriet AS;")

	// The PDF prints the same rows, with who made it and the direction.
	var header []string
	var rows [][]string
	var mu sync.Mutex
	inventory.SetTracePDFHook(func(h []string, r [][]string) {
		mu.Lock()
		defer mu.Unlock()
		header, rows = h, r
	})
	t.Cleanup(func() { inventory.SetTracePDFHook(nil) })
	res := names.Do(http.MethodGet, "/api/v1/inventory/trace.pdf?"+query+"&direction=origin", nil)
	mustStatus(t, res, http.StatusOK, "the trace's PDF")
	if !strings.HasPrefix(string(res.Body), "%PDF") || res.Header("Content-Disposition") != `attachment; filename="trace-B1-origin.pdf"` {
		t.Errorf("the PDF: %q, %q", res.Body[:min(8, len(res.Body))], res.Header("Content-Disposition"))
	}
	mu.Lock()
	if len(header) != 5 || len(rows) != 6 || rows[0][0] != "B1" || !strings.HasPrefix(rows[1][0], "   D1") || rows[3][0] != "         Mottak / Receipt" ||
		rows[3][4] != "Receipt, movement "+strconv.FormatInt(modtest.One[int64](t, h.Harness, `SELECT min(id) FROM inventory.movements WHERE kind = 'receipt'`), 10)+", ref PO-1 · Mølle AS" ||
		!strings.Contains(rows[5][0], "Already shown at 0.0.0") {
		t.Errorf("the PDF's rows = %q", rows)
	}
	mu.Unlock()

	// Without Orders composed, the trace says the deliveries are not known.
	bare := newHarness(t)
	bc, _ := signIn(t, bare, everything...)
	bareLots := seedTrace(t, bare, bc)
	if tr := getTrace(t, bc, "lotId="+strconv.FormatInt(bareLots.bread, 10)); tr.Deliveries != gen.Unavailable || tr.Nodes[1].Delivery != nil {
		t.Errorf("without Orders = %+v", tr)
	}
}

// TestTrace_Bound is the 2,000-node bound (M89): a lot that went into more
// productions than the bound allows is trace_too_large, and so is its
// block, which then blocks nothing.
func TestTrace_Bound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, append(slices.Clone(everything), "inventory:lots-manage")...)
	lots := seedTrace(t, h, c)
	// 2,000 bread lots made of the flour, written straight into the ledger's
	// tables: one production movement consuming the flour and producing each.
	h.Exec(t, `
		WITH made AS (
			INSERT INTO inventory.lots (variant_id, number, serial, received_at, sku, product_name, created_at, updated_at)
			SELECT $1::integer, 'X' || g, false, now(), 'BREAD-1', 'Bread', now(), now() FROM generate_series(1, 2000) g
			RETURNING id
		), mov AS (
			INSERT INTO inventory.movements (client_request_id, request_hash, site_id, kind, posted_at)
			VALUES (gen_random_uuid(), '\x00', $2, 'production', now()) RETURNING id
		)
		INSERT INTO inventory.movement_lines (movement_id, line_no, variant_id, sku, product_name, unit, from_location_id, to_location_id, quantity, lot_id)
		SELECT mov.id, 1, $3::integer, 'FLOUR-1', 'Flour', 'kg', $4::bigint, NULL::bigint, 0.001, $5::bigint FROM mov
		UNION ALL
		SELECT mov.id, (1 + row_number() OVER (ORDER BY made.id))::integer, $1::integer, 'BREAD-1', 'Bread', 'kg', NULL::bigint, $4::bigint, 1, made.id FROM mov, made`,
		varBread, lots.site.site.Id, varFlour, lots.site.shelf1.Id, lots.flour)
	expectRefusal(t, c.Do(http.MethodGet, tracePath+"?lotId="+strconv.FormatInt(lots.flour, 10), nil), http.StatusBadRequest, "trace_too_large", "a trace of more than 2,000 nodes")
	expectRefusal(t, c.Do(http.MethodPost, lotPath(lots.flour)+"/block", gen.InventoryLotBlockInput{Cause: gen.InventoryLotBlockInputCauseRecall}),
		http.StatusBadRequest, "trace_too_large", "a block beyond the bound")
	if n := h.Count(t, `SELECT count(*) FROM inventory.lot_holds`); n != 0 {
		t.Errorf("a refused block left %d holds", n)
	}
	// The lot alone may still be blocked.
	res := c.Do(http.MethodPost, lotPath(lots.flour)+"/block", gen.InventoryLotBlockInput{Cause: gen.InventoryLotBlockInputCauseRecall, Downstream: ptr(false)})
	mustStatus(t, res, http.StatusOK, "the lot alone")
}

// TestTrace_BlockDownstream is M92: the dialog's summary is the usage trace
// read on the pool; a production after it makes another lot of the flour;
// the block walks again inside its transaction and blocks that one too —
// every lot still holding stock, none across the unbuild — each with its
// own event; the release of the flour's block with its downstream list
// releases every one, and a hold of another source on one of them stays.
func TestTrace_BlockDownstream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, append(slices.Clone(everything), "inventory:lots-manage")...)
	lots := seedTrace(t, h, c)
	summary := getTrace(t, c, "lotId="+strconv.FormatInt(lots.flour, 10))
	if len(summary.Nodes) != 7 {
		t.Fatalf("the summary = %v", shape(summary))
	}
	// A production after the summary: more bread of the flour.
	l := ledgerOf(h)
	mo := contracts.StockSource{Module: "manufacturing", Kind: "order", ID: 6}
	var late int64
	if err := inTx(t, h, func(tx pgx.Tx) error {
		ids, err := l.EnsureLots(inCallerTx(t.Context()), tx, mo, testToday, []contracts.NewLot{{VariantID: varBread, Number: "B3", ProductionDate: testToday}})
		if err != nil {
			return err
		}
		late = ids[0]
		_, err = l.Produce(inCallerTx(t.Context()), tx, mo, contracts.StockProduction{
			Document: contracts.StockSource{Module: "manufacturing", Kind: "production", ID: 9}, SiteID: lots.site.site.Id, Today: testToday,
			Consume: []contracts.ConsumeLine{{VariantID: varFlour, LineID: 1, FromLocationID: lots.site.shelf1.Id, Quantity: "1", LotID: lots.flour}},
			Produce: []contracts.ProduceLine{{VariantID: varBread, LineID: 1, ToLocationID: lots.site.shelf2.Id, Quantity: "1", LotID: late}}})
		return err
	}); err != nil {
		t.Fatalf("the late production: %v", err)
	}
	// Another source holds the dough already.
	deviation := contracts.StockSource{Module: "manufacturing", Kind: "deviation", ID: 1}
	if err := inTx(t, h, func(tx pgx.Tx) error {
		return l.HoldLots(inCallerTx(t.Context()), tx, deviation, []contracts.LotHold{{LotID: lots.dough, Kind: "quarantine", Cause: "deviation"}})
	}); err != nil {
		t.Fatal(err)
	}

	res := c.Do(http.MethodPost, lotPath(lots.flour)+"/block", gen.InventoryLotBlockInput{Cause: gen.InventoryLotBlockInputCauseRecall, Note: ptr("Salmonella")})
	mustStatus(t, res, http.StatusOK, "the block")
	var out gen.InventoryLotBlock
	res.JSON(&out)
	var blocked []string
	for _, b := range out.Blocked {
		blocked = append(blocked, b.Number+":"+strconv.FormatBool(b.Root))
	}
	// The bun was taken apart and holds nothing: it is not blocked; the
	// late bread is.
	if want := []string{"F1:true", "D1:false", "B1:false", "B3:false"}; !slices.Equal(blocked, want) {
		t.Errorf("blocked = %v, want %v", blocked, want)
	}
	if out.Lot.Status != gen.InventoryLotStatusBlocked {
		t.Errorf("the flour's status = %s", out.Lot.Status)
	}
	statuses := func() string {
		return modtest.One[string](t, h.Harness, `SELECT string_agg(number || '=' || status, ',' ORDER BY id) FROM inventory.lots WHERE variant_id = ANY($1)`,
			[]int32{varFlour, varDough, varBread})
	}
	if got := statuses(); got != "F1=blocked,D1=blocked,B1=blocked,B2=available,B3=blocked" {
		t.Errorf("statuses = %s", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.events WHERE kind = 'lot_held' AND payload->>'source' = $1`, "inventory/lot/"+strconv.FormatInt(lots.flour, 10)); n != 4 {
		t.Errorf("%d held events naming the flour, want 4", n)
	}
	// Asked again, it blocks nothing twice.
	mustStatus(t, c.Do(http.MethodPost, lotPath(lots.flour)+"/block", gen.InventoryLotBlockInput{Cause: gen.InventoryLotBlockInputCauseRecall}), http.StatusOK, "the block again")
	if n := h.Count(t, `SELECT count(*) FROM inventory.lot_holds WHERE released_at IS NULL AND kind = 'block'`); n != 4 {
		t.Errorf("%d active blocks after the second block, want 4", n)
	}
	// A blocked bread cannot be delivered.
	if _, err := issueOf(t, h, 72, contracts.StockIssue{SiteID: lots.site.site.Id, Today: testToday,
		Lines: []contracts.IssueLine{{VariantID: varBread, LineID: 1, FromLocationID: lots.site.shelf2.Id, Quantity: "1", LotID: late}}}); refusalCode(err) != contracts.StockLotBlocked {
		t.Errorf("issuing the late bread = %v, want lot_blocked", err)
	}

	// Release the flour's block with its downstream list: every block of
	// the flour's lot page goes; the deviation's quarantine stays.
	page := getLot(t, c, lots.flour)
	var holdID int64
	for _, hd := range page.Holds {
		if hd.ReleasedAt == nil && hd.Kind == gen.InventoryLotHoldKindBlock {
			holdID = hd.Id
		}
	}
	res = c.Do(http.MethodPost, lotPath(lots.flour)+"/holds/"+strconv.FormatInt(holdID, 10)+"/release",
		gen.InventoryLotHoldRelease{Note: ptr("Cleared by the lab"), Downstream: ptr(true)})
	mustStatus(t, res, http.StatusOK, "the release with the downstream list")
	if got := statuses(); got != "F1=available,D1=quarantine,B1=available,B2=available,B3=available" {
		t.Errorf("statuses after the release = %s", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.lot_holds WHERE release_note = 'Cleared by the lab'`); n != 4 {
		t.Errorf("%d holds released with the note, want 4", n)
	}

	// Blocking the bun does not cross its unbuild into the dough.
	res = c.Do(http.MethodPost, lotPath(lots.bun)+"/block", gen.InventoryLotBlockInput{Cause: gen.InventoryLotBlockInputCauseComplaint})
	mustStatus(t, res, http.StatusOK, "the bun's block")
	res.JSON(&out)
	if len(out.Blocked) != 1 || out.Blocked[0].Id != lots.bun {
		t.Errorf("the bun's block = %+v, want the bun alone", out.Blocked)
	}
	// Releasing a downstream lot's block alone, on its own page, is the lot
	// page's too, and leaves the root's.
	mustStatus(t, c.Do(http.MethodPost, lotPath(lots.flour)+"/block", gen.InventoryLotBlockInput{Cause: gen.InventoryLotBlockInputCauseRecall}), http.StatusOK, "the block once more")
	bread := getLot(t, c, lots.bread)
	for _, hd := range bread.Holds {
		if hd.ReleasedAt == nil && hd.Kind == gen.InventoryLotHoldKindBlock {
			mustStatus(t, c.Do(http.MethodPost, lotPath(lots.bread)+"/holds/"+strconv.FormatInt(hd.Id, 10)+"/release", gen.InventoryLotHoldRelease{}),
				http.StatusOK, "the bread's block")
		}
	}
	if got := statuses(); got != "F1=blocked,D1=blocked,B1=available,B2=blocked,B3=blocked" {
		t.Errorf("statuses after the bread's own release = %s", got)
	}
	viewer, _ := signIn(t, h, stockViewer...)
	mustStatus(t, viewer.Do(http.MethodPost, lotPath(lots.flour)+"/block", gen.InventoryLotBlockInput{Cause: gen.InventoryLotBlockInputCauseRecall}),
		http.StatusForbidden, "a block without lots-manage")
	mustStatus(t, c.Do(http.MethodPost, lotPath(lots.flour)+"/block", map[string]any{"cause": "recall", "cascade": true}), http.StatusBadRequest, "an unknown property")
	mustStatus(t, c.Do(http.MethodPost, lotPath(999999)+"/block", gen.InventoryLotBlockInput{Cause: gen.InventoryLotBlockInputCauseRecall}),
		http.StatusNotFound, "an unknown lot")
	assertLedgerConsistent(t, h)
}

// TestTrace_GS1Labels prints the lots' GS1-128 labels: the bread, whose
// variant has a GTIN, and the flour, which falls back to the internal label.
func TestTrace_GS1Labels(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, everything...)
	lots := seedTrace(t, h, c)
	res := c.Do(http.MethodGet, lotsPath+"/labels.pdf?format=gs1&ids="+strconv.FormatInt(lots.bread, 10)+","+strconv.FormatInt(lots.flour, 10), nil)
	mustStatus(t, res, http.StatusOK, "GS1 labels")
	if !strings.HasPrefix(string(res.Body), "%PDF") || res.Header("Content-Disposition") != `attachment; filename="gs1-lot-labels-l7160.pdf"` {
		t.Errorf("GS1 labels: %q", res.Header("Content-Disposition"))
	}
	mustStatus(t, c.Do(http.MethodGet, lotsPath+"/labels.pdf?format=zpl&ids="+strconv.FormatInt(lots.bread, 10), nil), http.StatusBadRequest, "an unknown format")
}

// permCustomersView is Customers' read, which names the customers on a
// trace.
const permCustomersView = "customers:view"
