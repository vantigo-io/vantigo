package orders_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The rules delivery C's review settled: a line whose item changes moves its
// reservation to the new variant, cancel remaining keeps what is still open,
// an undo keeps what a draft invoice holds, an edit with Inventory off keeps
// the reservations, a second draft takes no reservation another holds, a
// post locks the variants it issues and an erasure waits on packing slips.

// A confirmed line's item changed releases the old variant's reservation and
// reserves the line's whole open quantity of the new one; refused when the
// new variant is short (O26, O46).
func TestEdits_ItemChangeMovesTheReservation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "10")
	h.stock.put(varFood, locA1, "10")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 5)))
	src := orderSource(o.Id)
	id := o.Lines[0].Id
	if h.stock.reservedOf(src, id) != "5" || h.stock.variantHeld(src, id) != varBolt {
		t.Fatalf("confirm reserved %s of %d", h.stock.reservedOf(src, id), h.stock.variantHeld(src, id))
	}
	itemOf := func(variant int32) []gen.OrdersLineInput {
		return []gen.OrdersLineInput{{Id: &id, Kind: gen.OrdersLineInputKindProduct, VariantId: i32(variant), Quantity: f64(5)}}
	}
	res := save(c, o, itemOf(varFood), nil)
	mustStatus(t, res, http.StatusOK, "change the item")
	o = orderOf(t, res)
	if h.stock.reservedOf(src, id) != "5" || h.stock.variantHeld(src, id) != varFood {
		t.Errorf("after the change the line holds %s of %d, want 5 of the coffee", h.stock.reservedOf(src, id), h.stock.variantHeld(src, id))
	}
	if q, _ := h.stock.Available(context.Background(), []int32{varBolt}, ptrI64(siteOslo)); q[0].Quantity != "10" {
		t.Errorf("bolt available %s, want all 10 back", q[0].Quantity)
	}
	// Back to the bolt, now short: refused with Inventory's code.
	h.stock.put(varBolt, locA1, "2")
	expectRefusal(t, save(c, o, itemOf(varBolt), nil), http.StatusConflict, contracts.StockInsufficientAvailable, "an item change short of stock")
}

// Cancel remaining releases only what is no longer open and takes out of
// the draft deliveries only the lines with nothing left to deliver (O18,
// O46, O47).
func TestCancelRemaining_KeepsWhatIsStillOpen(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "20")
	h.stock.put(varFood, locA1, "20")
	bolt := productLine(varBolt, 10)
	bolt.InvoicePolicy = ptrPolicy(gen.OrdersLineInputInvoicePolicyOrder)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, bolt, productLine(varFood, 4)))
	src := orderSource(o.Id)
	boltID, foodID := lineOf(t, o, varBolt).Id, lineOf(t, o, varFood).Id
	o = deliver(t, c, o, boltID, 2)
	// 6 of the bolts invoiced: 4 more than delivered stay to deliver.
	p := orders.Module().BillableOrders(h.Deps())
	if err := mark(t, h, invoiceRef(h, 91, 9001), sourceOf(allocate(t, p, boltID, "6"))); err != nil {
		t.Fatal(err)
	}
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "a draft delivery")
	d := deliveryOf(t, res)
	if len(d.Lines) != 2 {
		t.Fatalf("draft lines %+v, want the bolt and the coffee", d.Lines)
	}
	o = getOrder(t, c, o.Id)
	res = c.Do(http.MethodPost, orderPath(o.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version})
	mustStatus(t, res, http.StatusOK, "cancel remaining")
	if got := h.stock.reservedOf(src, boltID); got != "4" {
		t.Errorf("the bolt holds %s, want the 4 still open", got)
	}
	if got := h.stock.reservedOf(src, foodID); got != "0" {
		t.Errorf("the coffee holds %s, want none", got)
	}
	res = c.Do(http.MethodGet, deliveryPath(d.Id), nil)
	mustStatus(t, res, http.StatusOK, "the draft")
	after := deliveryOf(t, res)
	if after.Status != gen.OrdersDeliveryStatusDraft || len(after.Lines) != 1 || after.Lines[0].OrderLineId != boltID || after.Version <= d.Version {
		t.Errorf("draft after cancel remaining %s v%d %+v, want a draft of the bolt alone, its version bumped", after.Status, after.Version, after.Lines)
	}
}

// A draft left with no line by cancel remaining is cancelled.
func TestCancelRemaining_CancelsAnEmptiedDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varFood, locA1, "20")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varFood, 4)))
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "a draft delivery")
	d := deliveryOf(t, res)
	res = c.Do(http.MethodPost, orderPath(o.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version})
	mustStatus(t, res, http.StatusOK, "cancel remaining")
	res = c.Do(http.MethodGet, deliveryPath(d.Id), nil)
	mustStatus(t, res, http.StatusOK, "the draft")
	if got := deliveryOf(t, res); got.Status != gen.OrdersDeliveryStatusCancelled {
		t.Errorf("the emptied draft is %s, want cancelled", got.Status)
	}
}

// On a line invoiced on delivery, an undo goes no lower than what is
// invoiced (delivered_below_invoiced) and what a draft invoice holds
// (line_in_draft_invoice) (O32, O48).
func TestUndoDelivered_KeepsWhatADraftInvoiceHolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "10")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10)))
	id := o.Lines[0].Id
	o = deliver(t, c, o, id, 5)
	p := orders.Module().BillableOrders(h.Deps())
	a := allocate(t, p, id, "3")
	undo := func(qty float64) *modtest.Response {
		o := getOrder(t, c, o.Id)
		return c.Do(http.MethodPost, orderPath(o.Id, "undo-delivered"), gen.OrdersDeliveredInput{ExpectedUpdatedAt: o.UpdatedAt,
			Version: o.Version, Lines: []gen.OrdersDeliveredLine{{LineId: id, Quantity: qty}}})
	}
	expectRefusal(t, undo(3), http.StatusConflict, "line_in_draft_invoice", "an undo into a draft invoice")
	if err := mark(t, h, invoiceRef(h, 92, 9002), sourceOf(a)); err != nil {
		t.Fatal(err)
	}
	expectRefusal(t, undo(3), http.StatusBadRequest, "delivered_below_invoiced", "an undo below invoiced")
	mustStatus(t, undo(2), http.StatusOK, "an undo down to what is invoiced")
}

// An edit of a confirmed order with Inventory off keeps a line's delivery
// method and its reservation: only a smaller open quantity releases, and
// nothing is reserved — so the order is as Inventory left it when it is
// switched on again (O45).
func TestInventoryOff_EditKeepsReservations(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stockOff: true})
	c := h.SignIn(t, stockKeys...)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 5)))
	id := o.Lines[0].Id
	src := orderSource(o.Id)
	// The order as Inventory on left it: at a site, its line delivered by
	// delivery documents and holding its open quantity.
	h.Exec(t, `UPDATE orders.orders SET site_id = $2 WHERE id = $1`, o.Id, siteOslo)
	h.Exec(t, `UPDATE orders.lines SET delivery_method = 'delivery', invoice_policy = 'delivery' WHERE id = $1`, id)
	h.stock.put(varBolt, locA1, "10")
	if err := h.stock.Reserve(context.Background(), nil, src, siteOslo, []contracts.StockLine{{VariantID: varBolt, LineID: id, Quantity: "5"}}); err != nil {
		t.Fatal(err)
	}
	at := func(qty float64) gen.OrdersOrder {
		t.Helper()
		o := getOrder(t, c, o.Id)
		res := save(c, o, []gen.OrdersLineInput{{Id: &id, Kind: gen.OrdersLineInputKindProduct, VariantId: i32(varBolt), Quantity: f64(qty)}}, nil)
		mustStatus(t, res, http.StatusOK, "save with Inventory off")
		return orderOf(t, res)
	}
	o = at(5)
	if l := o.Lines[0]; l.DeliveryMethod != gen.OrdersLineDeliveryMethodDelivery || l.InvoicePolicy != gen.OrdersLineInvoicePolicyDelivery {
		t.Errorf("line %s/%s after an edit with Inventory off, want delivery/delivery kept", l.DeliveryMethod, l.InvoicePolicy)
	}
	if got := h.stock.reservedOf(src, id); got != "5" {
		t.Errorf("an edit with Inventory off left %s reserved, want 5", got)
	}
	at(4)
	if got := h.stock.reservedOf(src, id); got != "4" {
		t.Errorf("down to 4 left %s reserved, want 4", got)
	}
	o = at(6)
	if got := h.stock.reservedOf(src, id); got != "4" {
		t.Errorf("up to 6 with Inventory off left %s reserved, want 4: nothing reserved", got)
	}
	if l := o.Lines[0]; l.DeliveryMethod != gen.OrdersLineDeliveryMethodDelivery {
		t.Errorf("line delivered %s, want delivery kept for when Inventory is on again", l.DeliveryMethod)
	}
}

// A second draft of a line takes from the line's reservation only what the
// first draft does not, then what is free at the site (O47).
func TestCreateDelivery_SecondDraftTakesNoReservationTwice(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "8")
	o := createOrder(t, c, custBusiness, productLine(varBolt, 10))
	mustStatus(t, confirmWith(t, c, o, false), http.StatusOK, "confirm without reserving")
	id := getOrder(t, c, o.Id).Lines[0].Id
	if err := h.stock.Reserve(context.Background(), nil, orderSource(o.Id), siteOslo, []contracts.StockLine{{VariantID: varBolt, LineID: id, Quantity: "6"}}); err != nil {
		t.Fatal(err)
	}
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "the first draft")
	first := deliveryOf(t, res)
	res = c.Do(http.MethodPut, deliveryPath(first.Id), gen.OrdersDeliveryUpdate{Version: first.Version, ExpectedUpdatedAt: first.UpdatedAt,
		Lines: []gen.OrdersDeliveryLineInput{{OrderLineId: id, Quantity: 6}}})
	mustStatus(t, res, http.StatusOK, "the first draft takes the reservation")
	res = c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "the second draft")
	if second := deliveryOf(t, res); len(second.Lines) != 1 || second.Lines[0].Quantity != 2 {
		t.Errorf("second draft %+v, want the 2 free at the site", second.Lines)
	}
}

// A post locks the variants its lines issue, also one the order's lines no
// longer deliver by delivery documents (MB rule 21).
func TestPostDelivery_LocksTheVariantsItIssues(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "8")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2)))
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "a draft")
	d := deliveryOf(t, res)
	h.Exec(t, `UPDATE orders.lines SET delivery_method = 'manual' WHERE id = $1`, o.Lines[0].Id)
	before := len(h.catalog.lockCalls())
	mustStatus(t, c.Do(http.MethodPost, deliveryPath(d.Id, "post"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt}),
		http.StatusOK, "post")
	var locked []int32
	for _, ids := range h.catalog.lockCalls()[before:] {
		locked = append(locked, ids...)
	}
	if !slices.Contains(locked, varBolt) {
		t.Errorf("the post locked %v, want the bolt it issues", locked)
	}
}

// An erasure is complete only once no packing slip of the customer's
// deliveries is left (O29, O47).
func TestCompleteErasure_WaitsOnPackingSlips(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "8")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2)))
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "a draft")
	d := deliveryOf(t, res)
	h.Exec(t, `UPDATE orders.orders SET anonymised_at = now() WHERE id = $1`, o.Id)
	// The order's own PDFs gone, as the retention sweep deletes them.
	h.Exec(t, `UPDATE orders.mailings SET document_id = NULL WHERE owner_kind = 'order' AND owner_id = $1`, o.Id)
	h.Exec(t, `DELETE FROM orders.documents WHERE owner_kind = 'order' AND owner_id = $1`, o.Id)
	h.Exec(t, `INSERT INTO orders.erasures (customer_id, requested_at) VALUES ($1, now())`, custBusiness)
	h.Exec(t, `INSERT INTO orders.documents (owner_kind, owner_id, kind, version, storage_key, sha256, size_bytes, created_at)
		VALUES ('delivery', $1, 'packing_slip', 1, 'orders/test/slip.pdf', repeat('0', 64), 1, now())`, d.Id)
	q := store.New(h.Pool())
	ctx := context.Background()
	now := h.Now()
	if n, err := q.CompleteErasure(ctx, store.CompleteErasureParams{CustomerID: custBusiness, Now: &now}); err != nil || n != 0 {
		t.Fatalf("complete with a packing slip left: %d, %v; want nothing", n, err)
	}
	h.Exec(t, `DELETE FROM orders.documents WHERE owner_kind = 'delivery' AND owner_id = $1`, d.Id)
	if n, err := q.CompleteErasure(ctx, store.CompleteErasureParams{CustomerID: custBusiness, Now: &now}); err != nil || n != 1 {
		t.Fatalf("complete once the slip is gone: %d, %v; want 1", n, err)
	}
}
