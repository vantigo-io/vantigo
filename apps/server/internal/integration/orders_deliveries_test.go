package integration_test

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	ordgen "github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// Orders and Inventory together (orders design O45–O48, delivery C), end to
// end against the real modules: an order reserves at confirm through
// Inventory's own StockLedger on the order's transaction, two orders compete
// for the last units and one is refused, a delivery issues the stock and
// consumes the reservation, a back order is delivered by a second delivery,
// an issue beyond what the shelf holds is refused, a cancel releases, and an
// order line invoiced on delivery offers Invoices only what was delivered.

const ordersDeliveriesPath = "/api/v1/orders/deliveries"

// deliveryPermissions are orders' and inventory's keys beyond
// orderPermissions.
var deliveryPermissions = append(append([]string{}, orderPermissions...),
	"orders:deliveries-manage", "orders:settings",
	"inventory:locations-view", "inventory:locations-manage", "inventory:stock-view", "inventory:stock-adjust",
	"inventory:receipts-manage",
)

// stockInstallation is ordersInstallation with Inventory: one warehouse
// with one shelf (loc), and the warehouse Orders' default site.
type stockInstallation struct {
	ordersInstallation
	site, loc int64
}

func newStockInstallation(t *testing.T, opts ...modtest.Option) stockInstallation {
	t.Helper()
	return newStockInstallationOf(t, nil, nil, opts...)
}

// newStockInstallationOf is newStockInstallation with more modules composed
// after Orders, and more permissions for its caller.
func newStockInstallationOf(t *testing.T, more, perms []string, opts ...modtest.Option) stockInstallation {
	t.Helper()
	names := append([]string{modCustomers, modProducts, modInventory, modInvoices, modOrders}, more...)
	h, c, _, _ := installInvoices(t, names, append(slices.Clone(deliveryPermissions), perms...), opts...)
	standard := taxCategory(t, c, "Standard", 0.25, 1)
	food := taxCategory(t, c, "Næringsmidler", 0.15, 2)
	o := ordersInstallation{
		h: h, c: c,
		customer: invoiceNewBusiness(t, c, "Fjord Nord", "Fjord Nord AS", "923609016").Id,
		shelf:    pricedVariant(t, c, standard, "Hylle", "HYL-1", 1249.50),
		coffee:   pricedVariant(t, c, food, "Kaffe", "KAF-1", 89.90),
		screw:    pricedVariant(t, c, standard, "Skrue", "SKR-1", 3.35),
	}
	var site struct {
		Id             int64 `json:"id"`
		RootLocationId int64 `json:"rootLocationId"`
		FulfilsOrders  bool  `json:"fulfilsOrders"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/sites", map[string]any{"code": "OSL", "name": "Oslo", "kind": "warehouse"}, &site)
	if !site.FulfilsOrders {
		t.Fatal("a new warehouse does not fulfil orders")
	}
	var shelf struct {
		Id int64 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/locations", map[string]any{"parentId": site.RootLocationId, "kind": "shelf", "code": "A1"}, &shelf)
	var st ordgen.OrdersSettings
	okJSON(t, c, http.MethodGet, "/api/v1/orders/settings", nil, &st)
	okJSON(t, c, http.MethodPut, "/api/v1/orders/settings", ordgen.OrdersSettingsInput{
		Revision: st.Revision, QuoteSeriesStart: st.QuoteSeriesStart, OrderSeriesStart: st.OrderSeriesStart,
		QuoteValidityDays: st.QuoteValidityDays, DefaultPaymentTermsDays: st.DefaultPaymentTermsDays,
		ConfirmationOnConfirm: st.ConfirmationOnConfirm, CombineByDefault: st.CombineByDefault, MailTexts: st.MailTexts,
		DefaultSiteId: &site.Id,
	}, nil)
	return stockInstallation{ordersInstallation: o, site: site.Id, loc: shelf.Id}
}

// receive posts a receipt of qty of variant to the shelf.
func (s stockInstallation) receive(t *testing.T, variant int32, qty float64) {
	t.Helper()
	okJSON(t, s.c, http.MethodPost, "/api/v1/inventory/movements/receipts", map[string]any{
		"clientRequestId": uuid.New(), "siteId": s.site, "toLocationId": s.loc,
		"lines": []map[string]any{{"variantId": variant, "quantity": qty}},
	}, nil)
}

// setOnHand corrects what the shelf holds of variant to qty: Inventory's own
// correction, which ignores reservations.
func (s stockInstallation) setOnHand(t *testing.T, variant int32, qty float64) {
	t.Helper()
	okJSON(t, s.c, http.MethodPost, "/api/v1/inventory/movements/adjustments", map[string]any{
		"clientRequestId": uuid.New(), "siteId": s.site, "reason": "correction",
		"lines": []map[string]any{{"variantId": variant, "locationId": s.loc, "mode": "set", "quantity": qty}},
	}, nil)
}

// draftOrder creates a draft order of lines.
func (s stockInstallation) draftOrder(t *testing.T, lines ...ordgen.OrdersLineInput) ordgen.OrdersOrder {
	t.Helper()
	var o ordgen.OrdersOrder
	okJSON(t, s.c, http.MethodPost, ordersOrdersPath, ordgen.OrdersOrderInput{ClientRequestId: uuid.New(), CustomerId: s.customer, Lines: &lines}, &o)
	return o
}

// confirm confirms a draft, reserving or not, and answers the response.
func (s stockInstallation) confirm(c *modtest.Client, o ordgen.OrdersOrder, reserve bool) *modtest.Response {
	return c.Do(http.MethodPost, fmt.Sprintf("%s/%d/confirm", ordersOrdersPath, o.Id),
		ordgen.OrdersConfirmInput{ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version, Reserve: &reserve})
}

// newDelivery creates a delivery of the order and answers the response.
func (s stockInstallation) newDelivery(order int64) *modtest.Response {
	return s.c.Do(http.MethodPost, ordersDeliveriesPath, ordgen.OrdersDeliveryInput{OrderId: order})
}

// post posts a delivery and answers the response.
func (s stockInstallation) post(d ordgen.OrdersDelivery) *modtest.Response {
	return s.c.Do(http.MethodPost, fmt.Sprintf("%s/%d/post", ordersDeliveriesPath, d.Id),
		ordgen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt})
}

// deliveryFrom decodes a 201 or 200 delivery.
func deliveryFrom(t *testing.T, res *modtest.Response, want int, what string) ordgen.OrdersDelivery {
	t.Helper()
	if res.Status != want {
		t.Fatalf("%s = %d %s, want %d", what, res.Status, res.Body, want)
	}
	var d ordgen.OrdersDelivery
	res.JSON(&d)
	return d
}

func TestOrdersDeliveries_EndToEnd(t *testing.T) {
	t.Parallel()
	s := newStockInstallation(t)
	s.receive(t, s.shelf, 5)

	// A reserves 3 of the 5 at confirm.
	a := s.draftOrder(t, orderLine(s.shelf, 3, 0))
	if a.SiteId == nil || *a.SiteId != s.site || a.Lines[0].InvoicePolicy != ordgen.OrdersLineInvoicePolicyDelivery {
		t.Fatalf("order A site %v policy %s; want the default site and on delivery", a.SiteId, a.Lines[0].InvoicePolicy)
	}
	res := s.confirm(s.c, a, true)
	if res.Status != http.StatusOK {
		t.Fatalf("confirm A = %d %s", res.Status, res.Body)
	}
	a = s.order(t, a.Id)
	if l := a.Lines[0]; l.QtyReserved == nil || *l.QtyReserved != 3 || l.Available == nil || *l.Available != 2 {
		t.Fatalf("A's line reserved %v available %v, want 3 and 2", l.QtyReserved, l.Available)
	}

	// B and C compete for the last 2: one reserves, the other is refused.
	b := s.draftOrder(t, orderLine(s.shelf, 2, 0))
	c := s.draftOrder(t, orderLine(s.shelf, 2, 0))
	var wg sync.WaitGroup
	results := make([]*modtest.Response, 2)
	for i, o := range []ordgen.OrdersOrder{b, c} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.confirm(s.c, o, true)
		}()
	}
	wg.Wait()
	won, lost := b, c
	switch {
	case results[0].Status == http.StatusOK && results[1].Status == http.StatusConflict:
	case results[1].Status == http.StatusOK && results[0].Status == http.StatusConflict:
		won, lost = c, b
		results[0], results[1] = results[1], results[0]
	default:
		t.Fatalf("the two confirms = %d and %d, want one 200 and one 409", results[0].Status, results[1].Status)
	}
	if code := problemCode(results[1]); code != "insufficient_available" {
		t.Fatalf("the loser's refusal %q, want insufficient_available", code)
	}
	// The loser is confirmed without reserving.
	lost = s.order(t, lost.Id)
	if res := s.confirm(s.c, lost, false); res.Status != http.StatusOK {
		t.Fatalf("confirm without reserving = %d %s", res.Status, res.Body)
	}

	// A is delivered from the shelf: the issue consumes its reservation.
	d := deliveryFrom(t, s.newDelivery(a.Id), http.StatusCreated, "A's delivery")
	if len(d.Lines) != 1 || d.Lines[0].Quantity != 3 || d.Lines[0].FromLocationId == nil || *d.Lines[0].FromLocationId != s.loc {
		t.Fatalf("A's delivery %+v", d.Lines)
	}
	for _, doc := range []string{"pick-list", "packing-slip"} {
		r := s.c.Do(http.MethodGet, fmt.Sprintf("%s/%d/%s", ordersDeliveriesPath, d.Id, doc), nil)
		if r.Status != http.StatusOK || r.Header("Content-Type") != "application/pdf" {
			t.Errorf("%s = %d %s", doc, r.Status, r.Header("Content-Type"))
		}
	}
	d = deliveryFrom(t, s.post(d), http.StatusOK, "post A's delivery")
	a = s.order(t, a.Id)
	if l := a.Lines[0]; l.QtyDelivered != 3 || *l.QtyReserved != 0 || a.DeliveryStatus != ordgen.OrdersOrderDeliveryStatusDelivered {
		t.Fatalf("A after the post: delivered %v reserved %v status %s", l.QtyDelivered, *l.QtyReserved, a.DeliveryStatus)
	}
	if r := s.c.Do(http.MethodGet, fmt.Sprintf("%s/%d/packing-slip", ordersDeliveriesPath, d.Id), nil); r.Status != http.StatusOK {
		t.Errorf("the stored packing slip = %d", r.Status)
	}
	if n := modtest.One[int](t, s.h, `SELECT count(*)::int FROM inventory.movements WHERE kind = 'issue' AND source_kind = 'delivery' AND source_id = $1`, fmt.Sprint(d.Id)); n != 1 {
		t.Errorf("%d issue movements for the delivery, want 1", n)
	}

	// The loser, unreserved, finds nothing free: the winner holds the 2 left.
	if r := s.newDelivery(lost.Id); r.Status != http.StatusConflict || problemCode(r) != "nothing_to_deliver" {
		t.Fatalf("the loser's delivery = %d %s, want nothing_to_deliver", r.Status, r.Body)
	}
	// One more arrives: a partial delivery, and the rest stays as back order.
	s.receive(t, s.shelf, 1)
	first := deliveryFrom(t, s.newDelivery(lost.Id), http.StatusCreated, "the loser's first delivery")
	if first.Lines[0].Quantity != 1 {
		t.Fatalf("the first delivery proposes %v, want the 1 free", first.Lines[0].Quantity)
	}
	deliveryFrom(t, s.post(first), http.StatusOK, "post the first delivery")
	var back ordgen.OrdersOrderPage
	okJSON(t, s.c, http.MethodGet, ordersOrdersPath+"?toDeliver=true", nil, &back)
	if back.Total != 2 {
		t.Errorf("%d orders to deliver, want the winner's and the loser's back order", back.Total)
	}

	// Invoicing on delivery: only the 1 delivered is offered, then invoiced.
	if due := s.toInvoice(t, lost.Id); due[lost.Lines[0].Id] != 1 {
		t.Fatalf("to invoice %v, want the 1 delivered", due)
	}
	drafts := s.fromOrders(t, false, pickLine(lost.Id, lost.Lines[0].Id, 1))
	s.issue(t, drafts[0].InvoiceId)
	if l := s.order(t, lost.Id).Lines[0]; l.QtyInvoiced != 1 {
		t.Errorf("invoiced %v, want 1", l.QtyInvoiced)
	}

	s.receive(t, s.shelf, 1)
	second := deliveryFrom(t, s.newDelivery(lost.Id), http.StatusCreated, "the loser's second delivery")
	deliveryFrom(t, s.post(second), http.StatusOK, "post the second delivery")
	if got := s.order(t, lost.Id); got.DeliveryStatus != ordgen.OrdersOrderDeliveryStatusDelivered {
		t.Errorf("the loser after its back order: %s", got.DeliveryStatus)
	}

	// The winner's delivery: the shelf corrected below what it takes, so
	// the issue is refused; corrected back, it posts.
	wd := deliveryFrom(t, s.newDelivery(won.Id), http.StatusCreated, "the winner's delivery")
	s.setOnHand(t, s.shelf, 1)
	if r := s.post(wd); r.Status != http.StatusConflict || problemCode(r) != "insufficient_stock" {
		t.Fatalf("post beyond what the shelf holds = %d %s, want insufficient_stock", r.Status, r.Body)
	}
	s.setOnHand(t, s.shelf, 2)
	deliveryFrom(t, s.post(wd), http.StatusOK, "post the winner's delivery")

	// A cancel releases its reservation.
	s.receive(t, s.shelf, 2)
	e := s.draftOrder(t, orderLine(s.shelf, 2, 0))
	if r := s.confirm(s.c, e, true); r.Status != http.StatusOK {
		t.Fatalf("confirm E = %d %s", r.Status, r.Body)
	}
	e = s.order(t, e.Id)
	okJSON(t, s.c, http.MethodPost, fmt.Sprintf("%s/%d/cancel", ordersOrdersPath, e.Id),
		ordgen.OrdersVersionInput{ExpectedUpdatedAt: e.UpdatedAt, Version: e.Version}, nil)
	if n := modtest.One[int](t, s.h, `SELECT count(*)::int FROM inventory.reservations WHERE source_kind = 'order' AND source_id = $1`, fmt.Sprint(e.Id)); n != 0 {
		t.Errorf("%d reservations left after the cancel", n)
	}
}
