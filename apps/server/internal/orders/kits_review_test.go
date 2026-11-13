package orders_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// pairKit is a kit of one bolt and one coffee (manufacturing phase 2 M145).
func pairKit() *fakeKits {
	return &fakeKits{kits: map[int32][]contracts.KitComponent{
		varKit: {{VariantID: varBolt, PerKit: "1"}, {VariantID: varFood, PerKit: "1"}},
	}}
}

// seedKit puts the kit variant in the catalog at NOK 500.
func seedKit(h *harness) {
	h.catalog.seed(contracts.VariantEntry{ID: varKit, ProductID: varKit, ProductName: "Toolbox kit", SKU: "KIT-1", Unit: "pcs",
		ProductType: "Goods", ProductStatus: "Active", TaxCategoryID: catStandard})
	h.catalog.setPrice(varKit, "NOK", "500")
}

// cancelRemaining cancels what remains of o and answers the order.
func cancelRemaining(t *testing.T, c *modtest.Client, o gen.OrdersOrder) gen.OrdersOrder {
	t.Helper()
	res := c.Do(http.MethodPost, orderPath(o.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version})
	mustStatus(t, res, http.StatusOK, "cancel remaining")
	return orderOf(t, res)
}

// beyondKitsOf is the order's delivered_beyond_kits actions as "lineId:to".
func beyondKitsOf(t *testing.T, c *modtest.Client, id int64) []string {
	t.Helper()
	res := c.Do(http.MethodGet, orderPath(id, "changes"), nil)
	mustStatus(t, res, http.StatusOK, "changes")
	var list gen.OrdersChangeList
	res.JSON(&list)
	var out []string
	for _, e := range list.Items {
		for _, a := range e.Actions {
			if a.Kind == "delivered_beyond_kits" && a.LineId != nil && a.To != nil {
				out = append(out, fmt.Sprintf("%d:%s", *a.LineId, *a.To))
			}
		}
	}
	return out
}

// doneListed reports whether ListOrders' done filter keeps the order.
func doneListed(t *testing.T, c *modtest.Client, id int64) bool {
	t.Helper()
	res := c.Do(http.MethodGet, ordersPath+"?done=true", nil)
	mustStatus(t, res, http.StatusOK, "list the done")
	var page gen.OrdersOrderPage
	res.JSON(&page)
	for _, o := range page.Items {
		if o.Id == id {
			return o.Done
		}
	}
	return false
}

// A kit's children delivered unevenly, the rest cancelled and the whole kits
// invoiced: the children never count toward the order's invoicing — its kit
// does — so the order reaches invoiced and done, and the component delivered
// beyond the whole kits is logged with the excess (M145).
func TestKits_CancelRemainingWithUnevenChildren(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true, kits: pairKit()})
	seedKit(h)
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "50")
	h.stock.put(varFood, locA1, "50")

	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varKit, 10)))
	kit := lineOf(t, o, varKit)
	bolt, food := childrenOf(o, kit.Id)[0], childrenOf(o, kit.Id)[1]
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "create delivery")
	d := deliveryOf(t, res)
	res = c.Do(http.MethodPut, deliveryPath(d.Id), gen.OrdersDeliveryUpdate{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt,
		Lines: []gen.OrdersDeliveryLineInput{{OrderLineId: bolt.Id, Quantity: 5, FromLocationId: ptrLoc(locA1)}, {OrderLineId: food.Id, Quantity: 3, FromLocationId: ptrLoc(locA1)}}})
	mustStatus(t, res, http.StatusOK, "five bolts, three coffees")
	mustStatus(t, postDelivery(c, deliveryOf(t, res)), http.StatusOK, "post")
	o = getOrder(t, c, o.Id)
	if k := lineOf(t, o, varKit); k.QtyDelivered != 3 {
		t.Fatalf("kit delivered %v, want 3", k.QtyDelivered)
	}

	o = cancelRemaining(t, c, o)
	if k, b, f := lineOf(t, o, varKit), childrenOf(o, kit.Id)[0], childrenOf(o, kit.Id)[1]; k.QtyCancelled != 7 || b.QtyCancelled != 5 || f.QtyCancelled != 7 {
		t.Fatalf("cancelled kit %v bolt %v coffee %v, want 7, 5 and 7", k.QtyCancelled, b.QtyCancelled, f.QtyCancelled)
	}
	if got := beyondKitsOf(t, c, o.Id); len(got) != 1 || got[0] != fmt.Sprintf("%d:2.000", bolt.Id) {
		t.Errorf("delivered beyond kits %v, want the bolt's 2", got)
	}

	p := orders.Module().BillableOrders(h.Deps())
	if _, err := p.Allocate(context.Background(), contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: bolt.Id, Quantity: "1"}}}); err == nil {
		t.Error("a kit's child line was allocated on its own")
	} else {
		expectAllocationRefusal(t, err, contracts.AllocationLineNotInvoiceable, "allocate a child")
	}
	if err := mark(t, h, invoiceRef(h, 91, 9001), sourceOf(allocate(t, p, kit.Id, "3"))); err != nil {
		t.Fatal(err)
	}
	o = getOrder(t, c, o.Id)
	if o.InvoiceStatus != gen.OrdersOrderInvoiceStatusInvoiced || o.DeliveryStatus != gen.OrdersOrderDeliveryStatusDelivered || !o.Done {
		t.Errorf("order %s/%s done %v, want delivered, invoiced and done", o.DeliveryStatus, o.InvoiceStatus, o.Done)
	}
	if !doneListed(t, c, o.Id) {
		t.Error("ListOrders' done filter leaves the order out")
	}
	page, err := p.DueOrders(context.Background(), contracts.DueOrdersRequest{OrderIDs: []int64{o.Id}})
	if err != nil || len(page.Orders) != 0 {
		t.Errorf("due %+v, %v; want nothing left to invoice", page, err)
	}
}

// A kit of one with only one component delivered, the rest cancelled: the
// kit is cancelled whole and the order is done, the component delivered
// beyond the kits logged (M145).
func TestKits_CancelRemainingOfAKitOfOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true, kits: pairKit()})
	seedKit(h)
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "5")
	h.stock.put(varFood, locA1, "5")

	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varKit, 1)))
	kit := lineOf(t, o, varKit)
	bolt := childrenOf(o, kit.Id)[0]
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "create delivery")
	d := deliveryOf(t, res)
	res = c.Do(http.MethodPut, deliveryPath(d.Id), gen.OrdersDeliveryUpdate{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt,
		Lines: []gen.OrdersDeliveryLineInput{{OrderLineId: bolt.Id, Quantity: 1, FromLocationId: ptrLoc(locA1)}}})
	mustStatus(t, res, http.StatusOK, "the bolt alone")
	mustStatus(t, postDelivery(c, deliveryOf(t, res)), http.StatusOK, "post")

	o = cancelRemaining(t, c, getOrder(t, c, o.Id))
	if k := lineOf(t, o, varKit); k.QtyCancelled != 1 || k.QtyDelivered != 0 {
		t.Fatalf("kit cancelled %v delivered %v, want 1 and 0", k.QtyCancelled, k.QtyDelivered)
	}
	if !o.Done || o.DeliveryStatus != gen.OrdersOrderDeliveryStatusDelivered || !doneListed(t, c, o.Id) {
		t.Errorf("order %s done %v (listed %v), want delivered and done", o.DeliveryStatus, o.Done, doneListed(t, c, o.Id))
	}
	if got := beyondKitsOf(t, c, o.Id); len(got) != 1 || got[0] != fmt.Sprintf("%d:1.000", bolt.Id) {
		t.Errorf("delivered beyond kits %v, want the bolt's 1", got)
	}
}

func ptrLoc(id int64) *int64 { return &id }

// markLines posts a marking (or with undo, its undo) of the lines at the
// quantities given.
func markLines(c *modtest.Client, t *testing.T, o gen.OrdersOrder, undo bool, lines ...gen.OrdersDeliveredLine) *modtest.Response {
	t.Helper()
	path := "mark-delivered"
	if undo {
		path = "undo-delivered"
	}
	return c.Do(http.MethodPost, orderPath(o.Id, path), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version, Lines: lines})
}

// With Inventory keeping stock, a kit line is delivered by delivery
// documents as its children: Register delivered refuses it, line_invalid on
// its row, and a child marked by hand brings the kit to the whole kits made
// (M145).
func TestKits_RegisterDeliveredWithDeliveryDocuments(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true, kits: pairKit()})
	seedKit(h)
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "10")
	h.stock.put(varFood, locA1, "10")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varKit, 3)))
	kit := lineOf(t, o, varKit)
	bolt, food := childrenOf(o, kit.Id)[0], childrenOf(o, kit.Id)[1]

	p := expectRefusal(t, markLines(c, t, o, false, gen.OrdersDeliveredLine{LineId: bolt.Id, Quantity: 1}, gen.OrdersDeliveredLine{LineId: kit.Id, Quantity: 1}),
		http.StatusBadRequest, "line_invalid", "mark the kit")
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Index != 1 {
		t.Errorf("lines %+v, want the kit's row refused alone", p.Lines)
	}
	res := markLines(c, t, o, false, gen.OrdersDeliveredLine{LineId: bolt.Id, Quantity: 2}, gen.OrdersDeliveredLine{LineId: food.Id, Quantity: 1})
	mustStatus(t, res, http.StatusOK, "mark the children")
	if k := lineOf(t, orderOf(t, res), varKit); k.QtyDelivered != 1 {
		t.Errorf("kit delivered %v, want the 1 whole kit its children make", k.QtyDelivered)
	}
}

// A component's undo lowers the whole kits its kit counts delivered, and the
// kit carries the invoice: what a draft invoice holds of the kit keeps its
// components delivered, as it keeps a line of its own (M145).
func TestKits_UndoingAComponentHoldsTheKitToItsDraftInvoice(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true, kits: pairKit()})
	seedKit(h)
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "10")
	h.stock.put(varFood, locA1, "10")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varKit, 3)))
	kit := lineOf(t, o, varKit)
	bolt, food := childrenOf(o, kit.Id)[0], childrenOf(o, kit.Id)[1]
	res := markLines(c, t, o, false, gen.OrdersDeliveredLine{LineId: bolt.Id, Quantity: 2}, gen.OrdersDeliveredLine{LineId: food.Id, Quantity: 2})
	mustStatus(t, res, http.StatusOK, "mark the children")
	o = orderOf(t, res)
	if k := lineOf(t, o, varKit); k.QtyDelivered != 2 {
		t.Fatalf("kit delivered %v, want 2", k.QtyDelivered)
	}
	allocate(t, orders.Module().BillableOrders(h.Deps()), kit.Id, "2")

	o = getOrder(t, c, o.Id)
	expectRefusal(t, markLines(c, t, o, true, gen.OrdersDeliveredLine{LineId: food.Id, Quantity: 1}),
		http.StatusConflict, "line_in_draft_invoice", "undo a component of a kit in a draft invoice")
	if k := lineOf(t, getOrder(t, c, o.Id), varKit); k.QtyDelivered != 2 {
		t.Errorf("kit delivered %v after the refused undo, want 2", k.QtyDelivered)
	}
}

// Without a site the order's lines are marked by hand: marking the kit marks
// its children in step, the kit's × per kit, an undo takes them back with
// it, and a child is not marked on its own (M145).
func TestKits_RegisterDeliveredByHand(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{kits: &fakeKits{kits: map[int32][]contracts.KitComponent{
		varKit: {{VariantID: varBolt, PerKit: "4"}, {VariantID: varFood, PerKit: "1.5"}},
	}}})
	seedKit(h)
	c := h.SignIn(t, boss...)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varKit, 3)))
	kit := lineOf(t, o, varKit)
	bolt := childrenOf(o, kit.Id)[0]

	expectRefusal(t, markLines(c, t, o, false, gen.OrdersDeliveredLine{LineId: bolt.Id, Quantity: 1}), http.StatusBadRequest, "line_invalid", "mark a child")
	res := markLines(c, t, o, false, gen.OrdersDeliveredLine{LineId: kit.Id, Quantity: 2})
	mustStatus(t, res, http.StatusOK, "mark the kit")
	o = orderOf(t, res)
	if b, f := childrenOf(o, kit.Id)[0], childrenOf(o, kit.Id)[1]; lineOf(t, o, varKit).QtyDelivered != 2 || b.QtyDelivered != 8 || f.QtyDelivered != 3 {
		t.Fatalf("delivered kit %v bolt %v coffee %v, want 2, 8 and 3", lineOf(t, o, varKit).QtyDelivered, b.QtyDelivered, f.QtyDelivered)
	}
	res = markLines(c, t, o, true, gen.OrdersDeliveredLine{LineId: kit.Id, Quantity: 1})
	mustStatus(t, res, http.StatusOK, "undo one kit")
	o = orderOf(t, res)
	if b, f := childrenOf(o, kit.Id)[0], childrenOf(o, kit.Id)[1]; b.QtyDelivered != 4 || f.QtyDelivered != 1.5 || o.DeliveryStatus != gen.OrdersOrderDeliveryStatusPartial {
		t.Errorf("after the undo bolt %v coffee %v, order %s; want 4, 1.5 and partial", b.QtyDelivered, f.QtyDelivered, o.DeliveryStatus)
	}
}

// refusedRows is a refusal's lines as "index:code", in order.
func refusedRows(p gen.OrdersProblem) []string {
	var out []string
	if p.Lines != nil {
		for _, l := range *p.Lines {
			out = append(out, fmt.Sprintf("%d:%s", l.Index, l.Code))
		}
	}
	return out
}

// An order of a kit of three and another line, X: a refusal names a line by
// its row in the request — the editor holds the kit alone — so a refusal on
// X carries index 1 and one on a kit's child its kit's index 0, at confirm
// and at a save (M145).
func TestKits_RefusalsNameTheRequestRow(t *testing.T) {
	t.Parallel()
	const varShelf int32 = 302
	h := newHarness(t, harnessOptions{stock: true, kits: &fakeKits{kits: map[int32][]contracts.KitComponent{
		varKit: {{VariantID: varBolt, PerKit: "1"}, {VariantID: varLot, PerKit: "1"}, {VariantID: varShelf, PerKit: "1"}},
	}}})
	seedKit(h)
	seedTracked(h)
	h.catalog.seed(contracts.VariantEntry{ID: varShelf, ProductID: varShelf, ProductName: "Shelf", SKU: "SHELF-1", Unit: "pcs",
		ProductType: "Goods", ProductStatus: "Active", TaxCategoryID: catStandard})
	h.catalog.setPrice(varShelf, "NOK", "50")
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "10")
	h.stock.putLot(varLot, 1, "A1", locA1, "10", day(h, 40), h.Now().AddDate(0, -1, 0))

	o := createOrder(t, c, custBusiness, productLine(varKit, 2), productLine(varFood, 3))
	if len(o.Lines) != 5 {
		t.Fatalf("lines %+v, want the kit, its three children and X", o.Lines)
	}
	// The shelf, the kit's third child, and X are short.
	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version}),
		http.StatusConflict, "insufficient_available", "confirm short")
	if got := refusedRows(p); len(got) != 2 || got[0] != "0:insufficient_available" || got[1] != "1:insufficient_available" {
		t.Fatalf("confirm refused %v, want the kit's row 0 and X's row 1", got)
	}

	h.stock.put(varShelf, locA1, "10")
	h.stock.put(varFood, locA1, "10")
	o = confirmOrder(t, c, getOrder(t, c, o.Id))
	kit := lineOf(t, o, varKit)
	shelf, food := childrenOf(o, kit.Id)[2], lineOf(t, o, varFood)
	res := markLines(c, t, o, false, gen.OrdersDeliveredLine{LineId: shelf.Id, Quantity: 2}, gen.OrdersDeliveredLine{LineId: food.Id, Quantity: 2})
	mustStatus(t, res, http.StatusOK, "deliver the shelves and X")
	o = orderOf(t, res)
	kitID, foodID := kit.Id, food.Id
	p = expectRefusal(t, save(c, o, []gen.OrdersLineInput{
		{Id: &kitID, Kind: gen.OrdersLineInputKindProduct, VariantId: ptrVariant(varKit), Quantity: f64(1)},
		{Id: &foodID, Kind: gen.OrdersLineInputKindProduct, VariantId: ptrVariant(varFood), Quantity: f64(1)},
	}, nil), http.StatusBadRequest, "quantity_below_progress", "save below progress")
	if got := refusedRows(p); len(got) != 2 || got[0] != "0:quantity_below_progress" || got[1] != "1:quantity_below_progress" {
		t.Errorf("save refused %v, want the kit's row 0 and X's row 1", got)
	}
}

func ptrVariant(id int32) *int32 { return &id }

// A kit KitStructure answers with no component is refused on its row, never
// sold as an ordinary line; a variant it does not answer is no kit (M145).
func TestKits_EmptyKitRefused(t *testing.T) {
	t.Parallel()
	kits := &fakeKits{kits: map[int32][]contracts.KitComponent{varKit: {}}}
	h := newHarness(t, harnessOptions{kits: kits})
	seedKit(h)
	c := h.SignIn(t, boss...)
	p := expectRefusal(t, c.Do(http.MethodPost, ordersPath, gen.OrdersOrderInput{ClientRequestId: uuid.New(), CustomerId: custBusiness,
		Lines: &[]gen.OrdersLineInput{productLine(varBolt, 1), productLine(varKit, 1)}}), http.StatusBadRequest, "line_invalid", "an empty kit")
	if got := refusedRows(p); len(got) != 1 || got[0] != "1:line_invalid" {
		t.Errorf("refused %v, want the kit's row 1", got)
	}
	delete(kits.kits, varKit)
	if o := createOrder(t, c, custBusiness, productLine(varKit, 1)); len(o.Lines) != 1 || o.Lines[0].ParentLineId != nil {
		t.Errorf("lines %+v, want the variant as an ordinary line", o.Lines)
	}
}
