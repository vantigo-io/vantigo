package orders_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// Delivery C (orders design O45–O48): the site, reservations at confirm and
// on edits, deliveries with their pick list and packing slip, the back order
// and invoicing on delivery — against a fake Inventory.

// stockKeys are the keys of a caller who sells, confirms, delivers and keeps
// the settings.
var stockKeys = []string{"orders:access", "orders:sales-manage", "orders:sales-confirm", "orders:deliveries-manage", "orders:settings"}

const deliveriesPath = "/api/v1/orders/deliveries"

func deliveryPath(id int64, rest ...string) string {
	p := fmt.Sprintf("%s/%d", deliveriesPath, id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

func deliveryOf(t *testing.T, res *modtest.Response) gen.OrdersDelivery {
	t.Helper()
	var d gen.OrdersDelivery
	res.JSON(&d)
	return d
}

// orderSource is an order as the ledger names it.
func orderSource(id int64) contracts.StockSource {
	return contracts.StockSource{Module: "orders", Kind: "order", ID: id}
}

// setDefaultSite saves the settings with site as the default.
func setDefaultSite(t *testing.T, c *modtest.Client, site int64) {
	t.Helper()
	res := c.Do(http.MethodGet, settingsPath, nil)
	mustStatus(t, res, http.StatusOK, "settings")
	var st gen.OrdersSettings
	res.JSON(&st)
	mustStatus(t, c.Do(http.MethodPut, settingsPath, gen.OrdersSettingsInput{
		Revision: st.Revision, QuoteSeriesStart: st.QuoteSeriesStart, OrderSeriesStart: st.OrderSeriesStart,
		QuoteValidityDays: st.QuoteValidityDays, DefaultPaymentTermsDays: st.DefaultPaymentTermsDays,
		ConfirmationOnConfirm: st.ConfirmationOnConfirm, CombineByDefault: st.CombineByDefault, MailTexts: st.MailTexts,
		DefaultSiteId: &site,
	}), http.StatusOK, "save the default site")
}

// confirmWith confirms o with reserve and answers the response.
func confirmWith(t *testing.T, c *modtest.Client, o gen.OrdersOrder, reserve bool) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version, Reserve: &reserve,
	})
}

// getOrder reads an order.
func getOrder(t *testing.T, c *modtest.Client, id int64) gen.OrdersOrder {
	t.Helper()
	res := c.Do(http.MethodGet, orderPath(id), nil)
	mustStatus(t, res, http.StatusOK, "get order")
	return orderOf(t, res)
}

// reservedOn is a line's qtyReserved as the order answers it.
func reservedOn(t *testing.T, o gen.OrdersOrder, variant int32) float64 {
	t.Helper()
	l := lineOf(t, o, variant)
	if l.QtyReserved == nil {
		t.Fatalf("line of %d answers no qtyReserved", variant)
	}
	return *l.QtyReserved
}

// The sites are Inventory's, through the contract; the default site is
// judged against them and a new order takes it; a goods line on an order
// with a site is delivered by delivery documents and invoiced on delivery,
// a free-text line by hand and on order (O46, O48).
func TestSites_DefaultSiteAndPolicies(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)

	res := c.Do(http.MethodGet, "/api/v1/orders/sites", nil)
	mustStatus(t, res, http.StatusOK, "sites")
	var sites gen.OrdersSiteList
	res.JSON(&sites)
	if len(sites.Items) != 3 || sites.Items[0].Code != "BGO" {
		t.Fatalf("sites %+v", sites.Items)
	}
	var meta gen.OrdersMeta
	r := c.Do(http.MethodGet, "/api/v1/orders/meta", nil)
	r.JSON(&meta)
	if !meta.StockKept {
		t.Error("meta answers stock not kept with Inventory on")
	}

	// A store does not fulfil orders.
	res = c.Do(http.MethodGet, settingsPath, nil)
	var st gen.OrdersSettings
	res.JSON(&st)
	shop := siteStore
	expectRefusal(t, c.Do(http.MethodPut, settingsPath, gen.OrdersSettingsInput{
		Revision: st.Revision, QuoteSeriesStart: 1, OrderSeriesStart: 1, QuoteValidityDays: 30, DefaultPaymentTermsDays: 14,
		MailTexts: []gen.OrdersMailText{}, DefaultSiteId: &shop,
	}), http.StatusBadRequest, "site_not_fulfilling", "a store as the default site")
	setDefaultSite(t, c, siteOslo)

	free := line(gen.OrdersLineInputKindProduct)
	free.Name, free.Quantity, free.UnitPrice, free.VatCodeId = str("Freight"), f64(1), f64(200), i32(codeStandard)
	o := createOrder(t, c, custBusiness, productLine(varBolt, 3), free)
	if o.SiteId == nil || *o.SiteId != siteOslo {
		t.Fatalf("order site %v, want the default %d", o.SiteId, siteOslo)
	}
	bolt := lineOf(t, o, varBolt)
	if bolt.InvoicePolicy != gen.OrdersLineInvoicePolicyDelivery || bolt.DeliveryMethod != gen.OrdersLineDeliveryMethodDelivery {
		t.Errorf("goods line %s/%s, want delivery/delivery", bolt.InvoicePolicy, bolt.DeliveryMethod)
	}
	freight := o.Lines[1]
	if freight.InvoicePolicy != gen.OrdersLineInvoicePolicyOrder || freight.DeliveryMethod != gen.OrdersLineDeliveryMethodManual {
		t.Errorf("free-text line %s/%s, want order/manual", freight.InvoicePolicy, freight.DeliveryMethod)
	}

	// A draft without a site: its goods lines go back to the defaults of no
	// site; the request's own policy wins.
	lines := keep(o)
	lines[0].InvoicePolicy = ptrPolicy(gen.OrdersLineInputInvoicePolicyOrder)
	res = save(c, o, lines, func(b *gen.OrdersOrderUpdate) { b.SiteId = nil })
	mustStatus(t, res, http.StatusOK, "save without a site")
	o = orderOf(t, res)
	if o.SiteId != nil || lineOf(t, o, varBolt).DeliveryMethod != gen.OrdersLineDeliveryMethodManual ||
		lineOf(t, o, varBolt).InvoicePolicy != gen.OrdersLineInvoicePolicyOrder {
		t.Errorf("a draft saved without a site: %v %+v", o.SiteId, lineOf(t, o, varBolt))
	}
}

func ptrPolicy(p gen.OrdersLineInputInvoicePolicy) *gen.OrdersLineInputInvoicePolicy { return &p }

// Confirm reserves every goods line's open quantity at the site; a shortfall
// is refused naming the short line; Confirm without reserving confirms and
// logs it, and Reserve now reserves later (O46).
func TestConfirm_ReservesOrRefusesAndReserveNow(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "5")
	h.stock.put(varFood, locA1, "100")

	o := createOrder(t, c, custBusiness, productLine(varFood, 2), productLine(varBolt, 8))
	p := expectRefusal(t, confirmWith(t, c, o, true), http.StatusConflict, contracts.StockInsufficientAvailable, "a short confirm")
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Index != 1 {
		t.Fatalf("refusal lines %+v, want the bolt's (index 1)", p.Lines)
	}
	if got := getOrder(t, c, o.Id); got.Status != gen.OrdersOrderStatusDraft {
		t.Fatalf("a refused confirm confirmed: %s", got.Status)
	}

	res := confirmWith(t, c, o, false)
	mustStatus(t, res, http.StatusOK, "confirm without reserving")
	o = orderOf(t, res)
	if reservedOn(t, o, varBolt) != 0 || reservedOn(t, o, varFood) != 0 {
		t.Errorf("confirm without reserving reserved")
	}
	if log := changesOf(t, c, o.Id); !slices.Contains(log[len(log)-1], "confirmed_without_reserving") {
		t.Errorf("change log %v, want confirmed_without_reserving", log)
	}
	if bolt := lineOf(t, o, varBolt); bolt.Available == nil || *bolt.Available != 5 {
		t.Errorf("bolt available %v, want 5", bolt.Available)
	}

	reserve := func(o gen.OrdersOrder) *modtest.Response {
		return c.Do(http.MethodPost, orderPath(o.Id, "reserve"), gen.OrdersVersionInput{Version: o.Version, ExpectedUpdatedAt: orderAt(t, c, o.Id)})
	}
	expectRefusal(t, reserve(o), http.StatusConflict, contracts.StockInsufficientAvailable, "reserve now, still short")
	h.stock.put(varBolt, locA2, "3")
	res = reserve(o)
	mustStatus(t, res, http.StatusOK, "reserve now")
	o = orderOf(t, res)
	if reservedOn(t, o, varBolt) != 8 || reservedOn(t, o, varFood) != 2 {
		t.Errorf("reserved bolt %v food %v, want 8 and 2", reservedOn(t, o, varBolt), reservedOn(t, o, varFood))
	}
	if bolt := lineOf(t, o, varBolt); bolt.Available == nil || *bolt.Available != 0 {
		t.Errorf("bolt available %v after reserving all, want 0", bolt.Available)
	}

	// A second order competing for the bolts is refused.
	other := createOrder(t, c, custBusiness, productLine(varBolt, 1))
	expectRefusal(t, confirmWith(t, c, other, true), http.StatusConflict, contracts.StockInsufficientAvailable, "the last unit is taken")
}

// A confirmed order's edits move its reservations: a quantity up reserves
// more (refused when short), down releases, a removed line releases; manual
// marking releases what it marked; cancel remaining and cancel release all
// (O26, O32, O45, O46).
func TestEdits_AdjustReservations(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "20")
	h.stock.put(varFood, locA1, "20")

	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 5), productLine(varFood, 4)))
	src := orderSource(o.Id)
	bolt, food := lineOf(t, o, varBolt), lineOf(t, o, varFood)
	if h.stock.reservedOf(src, bolt.Id) != "5" || h.stock.reservedOf(src, food.Id) != "4" {
		t.Fatalf("confirm reserved %s and %s, want 5 and 4", h.stock.reservedOf(src, bolt.Id), h.stock.reservedOf(src, food.Id))
	}

	// Up beyond what is available.
	expectRefusal(t, save(c, o, keepAt(o, varBolt, 30), nil), http.StatusConflict, contracts.StockInsufficientAvailable, "up beyond available")
	res := save(c, o, keepAt(o, varBolt, 9), nil)
	mustStatus(t, res, http.StatusOK, "up")
	o = orderOf(t, res)
	if h.stock.reservedOf(src, bolt.Id) != "9" {
		t.Errorf("bolt reserved %s, want 9", h.stock.reservedOf(src, bolt.Id))
	}
	res = save(c, o, keepAt(o, varBolt, 2), nil)
	mustStatus(t, res, http.StatusOK, "down")
	o = orderOf(t, res)
	if h.stock.reservedOf(src, bolt.Id) != "2" {
		t.Errorf("bolt reserved %s, want 2", h.stock.reservedOf(src, bolt.Id))
	}
	// The food line removed; a new bolt line reserves its own.
	lines := []gen.OrdersLineInput{{Id: &bolt.Id, Kind: gen.OrdersLineInputKindProduct, VariantId: bolt.VariantId, Quantity: f64(2)}}
	lines = append(lines, productLine(varFood, 3))
	res = save(c, o, lines, nil)
	mustStatus(t, res, http.StatusOK, "remove and add")
	o = orderOf(t, res)
	if h.stock.reservedOf(src, food.Id) != "0" {
		t.Errorf("the removed line still holds %s", h.stock.reservedOf(src, food.Id))
	}
	newFood := lineOf(t, o, varFood)
	if h.stock.reservedOf(src, newFood.Id) != "3" {
		t.Errorf("the new line holds %s, want 3", h.stock.reservedOf(src, newFood.Id))
	}

	// Manual marking releases what it marked; its undo reserves nothing.
	o = deliver(t, c, o, newFood.Id, 1)
	if h.stock.reservedOf(src, newFood.Id) != "2" {
		t.Errorf("after marking 1 the line holds %s, want 2", h.stock.reservedOf(src, newFood.Id))
	}
	res = c.Do(http.MethodPost, orderPath(o.Id, "undo-delivered"), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, o.Id),
		Version: o.Version, Lines: []gen.OrdersDeliveredLine{{LineId: newFood.Id, Quantity: 1}}})
	mustStatus(t, res, http.StatusOK, "undo")
	o = orderOf(t, res)
	if h.stock.reservedOf(src, newFood.Id) != "2" {
		t.Errorf("undo changed the reservation to %s", h.stock.reservedOf(src, newFood.Id))
	}

	// Cancel remaining releases everything.
	o = deliver(t, c, o, newFood.Id, 1)
	res = c.Do(http.MethodPost, orderPath(o.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version})
	mustStatus(t, res, http.StatusOK, "cancel remaining")
	if h.stock.reservedOf(src, newFood.Id) != "0" || h.stock.reservedOf(src, bolt.Id) != "0" {
		t.Error("cancel remaining left reservations")
	}

	// A plain cancel releases too.
	other := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 4)))
	if h.stock.reservedOf(orderSource(other.Id), other.Lines[0].Id) != "4" {
		t.Fatal("the second order reserved nothing")
	}
	mustStatus(t, c.Do(http.MethodPost, orderPath(other.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, other.Id), Version: other.Version}),
		http.StatusOK, "cancel")
	if h.stock.reservedOf(orderSource(other.Id), other.Lines[0].Id) != "0" {
		t.Error("cancel left the reservation")
	}
}

// A confirmed order's site moves its reservations; once it has a delivery
// the site no longer changes (O46).
func TestSiteChange_MovesReservations(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "10")
	h.stock.put(varBolt, locB1, "10")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 4)))
	bolt := o.Lines[0]
	bergen := siteBergen
	res := save(c, o, keep(o), func(b *gen.OrdersOrderUpdate) { b.SiteId = &bergen })
	mustStatus(t, res, http.StatusOK, "move to Bergen")
	o = orderOf(t, res)
	src := orderSource(o.Id)
	if h.stock.reservedAt(src, bolt.Id, siteOslo) != "0" || h.stock.reservedAt(src, bolt.Id, siteBergen) != "4" {
		t.Errorf("reservations Oslo %s Bergen %s, want 0 and 4", h.stock.reservedAt(src, bolt.Id, siteOslo), h.stock.reservedAt(src, bolt.Id, siteBergen))
	}
	mustStatus(t, c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id}), http.StatusCreated, "a delivery")
	oslo := siteOslo
	expectRefusal(t, save(c, getOrder(t, c, o.Id), keep(o), func(b *gen.OrdersOrderUpdate) { b.SiteId = &oslo }),
		http.StatusConflict, "order_has_deliveries", "a site change with a delivery")
}

// A delivery proposes what is open and available, is picked, prints a pick
// list, and is posted: Inventory issues it, the order line's delivered
// grows and the reservation is consumed; what remains is the back order the
// To deliver filter shows, delivered by a second delivery. An issue beyond
// what the location holds is refused; a held order takes no delivery; a
// posted delivery is immutable (O47).
func TestDelivery_PartialPostBackOrderAndRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "6")
	h.stock.put(varFood, locA2, "10")

	o := createOrder(t, c, custBusiness, productLine(varBolt, 10), productLine(varFood, 2))
	mustStatus(t, confirmWith(t, c, o, false), http.StatusOK, "confirm without reserving")
	o = getOrder(t, c, o.Id)
	bolt, food := lineOf(t, o, varBolt), lineOf(t, o, varFood)

	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "create delivery")
	d := deliveryOf(t, res)
	if d.Status != gen.OrdersDeliveryStatusDraft || len(d.Lines) != 2 || d.Lines[0].Quantity != 6 || d.Lines[1].Quantity != 2 {
		t.Fatalf("delivery %+v, want 6 bolts (what is available) and 2 coffees", d.Lines)
	}
	if d.Lines[0].FromLocationId == nil || *d.Lines[0].FromLocationId != locA1 || len(d.Lines[0].Locations) != 1 {
		t.Errorf("bolt picked from %v with locations %+v", d.Lines[0].FromLocationId, d.Lines[0].Locations)
	}

	// Picking: over what is open, and a location not holding the item.
	expectRefusal(t, c.Do(http.MethodPut, deliveryPath(d.Id), gen.OrdersDeliveryUpdate{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt,
		Lines: []gen.OrdersDeliveryLineInput{{OrderLineId: bolt.Id, Quantity: 11}}}), http.StatusBadRequest, "over_delivery", "over delivery")
	expectRefusal(t, c.Do(http.MethodPut, deliveryPath(d.Id), gen.OrdersDeliveryUpdate{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt,
		Lines: []gen.OrdersDeliveryLineInput{{OrderLineId: bolt.Id, Quantity: 1, FromLocationId: ptrI64(locA2)}}}), http.StatusBadRequest,
		contracts.StockLocationUnknown, "a location without the item")
	res = c.Do(http.MethodPut, deliveryPath(d.Id), gen.OrdersDeliveryUpdate{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt,
		Lines: []gen.OrdersDeliveryLineInput{{OrderLineId: bolt.Id, Quantity: 4, FromLocationId: ptrI64(locA1)}, {OrderLineId: food.Id, Quantity: 2, FromLocationId: ptrI64(locA2)}}})
	mustStatus(t, res, http.StatusOK, "pick")
	d = deliveryOf(t, res)

	res = c.Do(http.MethodGet, deliveryPath(d.Id, "pick-list"), nil)
	mustStatus(t, res, http.StatusOK, "pick list")
	if m, ok := pdfModels.find("Plukkliste", fmt.Sprintf("Leveranse %d – Ordre %d", d.Number, o.Number), func(m orders.PDFModel) bool {
		return len(m.Rows) == 2 && m.Rows[0][0] == "OSL-A-01"
	}); !ok ||
		len(m.Rows) != 2 || m.Rows[0][0] != "OSL-A-01" || m.Rows[1][0] != "OSL-A-02" {
		t.Errorf("pick list %+v, %v; want the lines by location", m.Rows, ok)
	}

	// Held: no post.
	held := getOrder(t, c, o.Id)
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "hold"), gen.OrdersHoldInput{ExpectedUpdatedAt: held.UpdatedAt, Version: held.Version}), http.StatusOK, "hold")
	expectRefusal(t, c.Do(http.MethodPost, deliveryPath(d.Id, "post"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt}),
		http.StatusConflict, "order_on_hold", "post on hold")
	expectRefusal(t, c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id}), http.StatusConflict, "order_on_hold", "create on hold")
	held = getOrder(t, c, o.Id)
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "release"), gen.OrdersVersionInput{ExpectedUpdatedAt: held.UpdatedAt, Version: held.Version}), http.StatusOK, "release")

	// Stock taken meanwhile: the issue is refused, naming the line.
	h.stock.put(varBolt, locA1, "3")
	p := expectRefusal(t, c.Do(http.MethodPost, deliveryPath(d.Id, "post"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt}),
		http.StatusConflict, contracts.StockInsufficientStock, "post beyond on hand")
	if p.Lines == nil || (*p.Lines)[0].Index != 0 {
		t.Errorf("refusal lines %+v", p.Lines)
	}
	h.stock.put(varBolt, locA1, "6")
	res = c.Do(http.MethodPost, deliveryPath(d.Id, "post"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "post")
	d = deliveryOf(t, res)
	if d.Status != gen.OrdersDeliveryStatusPosted || d.MovementId == nil || !d.PackingSlipStored {
		t.Fatalf("posted %+v", d)
	}
	if h.stock.held(varBolt, locA1) != "2" || h.stock.held(varFood, locA2) != "8" {
		t.Errorf("on hand bolt %s coffee %s, want 2 and 8", h.stock.held(varBolt, locA1), h.stock.held(varFood, locA2))
	}
	if n := eventsNamed(t, h, "orders.delivery.posted", o.Id); n != 1 {
		t.Errorf("%d delivery.posted events, want 1", n)
	}
	o = getOrder(t, c, o.Id)
	if lineOf(t, o, varBolt).QtyDelivered != 4 || lineOf(t, o, varFood).QtyDelivered != 2 || o.DeliveryStatus != gen.OrdersOrderDeliveryStatusPartial {
		t.Errorf("after the post: bolt %v coffee %v status %s", lineOf(t, o, varBolt).QtyDelivered, lineOf(t, o, varFood).QtyDelivered, o.DeliveryStatus)
	}
	res = c.Do(http.MethodGet, deliveryPath(d.Id, "packing-slip"), nil)
	mustStatus(t, res, http.StatusOK, "packing slip")
	if m, ok := pdfModels.find("Pakkseddel", fmt.Sprintf("Leveranse %d – Ordre %d", d.Number, o.Number), func(m orders.PDFModel) bool {
		return len(m.Rows) == 2 && m.Rows[0][2] == "4"
	}); !ok ||
		len(m.Rows) != 2 || m.Rows[0][2] != "4" || m.Rows[0][3] != "6" {
		t.Errorf("packing slip %+v, %v; want 4 delivered and 6 on back order", m.Rows, ok)
	}

	// Posted is immutable.
	expectRefusal(t, c.Do(http.MethodPost, deliveryPath(d.Id, "cancel"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt}),
		http.StatusConflict, "delivery_not_draft", "cancel a posted delivery")

	// The back order: the To deliver filter, then a second delivery.
	res = c.Do(http.MethodGet, ordersPath+"?toDeliver=true", nil)
	var page gen.OrdersOrderPage
	res.JSON(&page)
	if page.Total != 1 || page.Items[0].Id != o.Id {
		t.Errorf("to deliver %+v", page.Items)
	}
	h.stock.put(varBolt, locA1, "10")
	res = c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "the second delivery")
	second := deliveryOf(t, res)
	if len(second.Lines) != 1 || second.Lines[0].Quantity != 6 {
		t.Fatalf("second delivery %+v, want the 6 left", second.Lines)
	}
	// A third draft finds nothing open; cancel the second and it is open again.
	expectRefusal(t, c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id}), http.StatusConflict, "nothing_to_deliver", "all in a draft")
	res = c.Do(http.MethodPost, deliveryPath(second.Id, "cancel"), gen.OrdersVersionInput{Version: second.Version, ExpectedUpdatedAt: second.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "cancel the draft")
	res = c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "the third delivery")
	third := deliveryOf(t, res)
	res = c.Do(http.MethodGet, deliveryPath(third.Id), nil)
	mustStatus(t, res, http.StatusOK, "get delivery")
	mustStatus(t, c.Do(http.MethodPost, deliveryPath(third.Id, "post"), gen.OrdersVersionInput{Version: third.Version, ExpectedUpdatedAt: third.UpdatedAt}),
		http.StatusOK, "post the rest")
	o = getOrder(t, c, o.Id)
	if o.DeliveryStatus != gen.OrdersOrderDeliveryStatusDelivered {
		t.Errorf("delivery status %s, want delivered", o.DeliveryStatus)
	}
	res = c.Do(http.MethodGet, deliveriesPath+fmt.Sprintf("?orderId=%d&status=posted", o.Id), nil)
	var list gen.OrdersDeliveryPage
	res.JSON(&list)
	if list.Total != 2 {
		t.Errorf("%d posted deliveries, want 2", list.Total)
	}
}

func ptrI64(v int64) *int64 { return &v }

// On delivery: only what is delivered is offered to invoice, and an
// allocation never goes beyond it; the policy changes only while nothing is
// invoiced or in a draft invoice (O48).
func TestInvoiceOnDelivery(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "10")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10)))
	bolt := o.Lines[0]
	p := orders.Module().BillableOrders(h.Deps())
	ctx := context.Background()
	page, err := p.DueOrders(ctx, contracts.DueOrdersRequest{OrderIDs: []int64{o.Id}})
	if err != nil || len(page.Orders) != 0 {
		t.Fatalf("due %+v, %v; want nothing before delivery", page, err)
	}
	o = deliver(t, c, o, bolt.Id, 3)
	page, err = p.DueOrders(ctx, contracts.DueOrdersRequest{OrderIDs: []int64{o.Id}})
	if err != nil || len(page.Orders) != 1 || page.Orders[0].Lines[0].ToInvoice != "3.000" || page.Orders[0].Lines[0].InvoicePolicy != "delivery" {
		t.Fatalf("due %+v, %v; want 3 delivered to invoice", page, err)
	}
	_, err = p.Allocate(ctx, contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: bolt.Id, Quantity: "4"}}})
	expectAllocationRefusal(t, err, contracts.AllocationExceedsOpen, "beyond what is delivered")
	allocate(t, p, bolt.Id, "3")
	lines := keep(o)
	lines[0].InvoicePolicy = ptrPolicy(gen.OrdersLineInputInvoicePolicyOrder)
	expectRefusal(t, save(c, getOrder(t, c, o.Id), lines, nil), http.StatusConflict, "line_invoiced", "a policy change with a draft allocation")
}

// Inventory off: no site is offered, nothing is reserved, deliveries are
// unavailable and lines are marked by hand — but a reservation made while it
// was on is still released by a cancel (O45).
func TestInventoryOff_ReleasesButNeverReserves(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stockOff: true})
	c := h.SignIn(t, stockKeys...)
	res := c.Do(http.MethodGet, "/api/v1/orders/sites", nil)
	var sites gen.OrdersSiteList
	res.JSON(&sites)
	if len(sites.Items) != 0 {
		t.Errorf("sites %+v with Inventory off", sites.Items)
	}
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2)))
	if l := o.Lines[0]; l.DeliveryMethod != gen.OrdersLineDeliveryMethodManual || l.InvoicePolicy != gen.OrdersLineInvoicePolicyOrder {
		t.Errorf("line %s/%s with Inventory off, want manual/order", l.DeliveryMethod, l.InvoicePolicy)
	}
	expectRefusal(t, c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id}), http.StatusServiceUnavailable,
		"dependency_unavailable", "a delivery with Inventory off")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "reserve"), gen.OrdersVersionInput{Version: o.Version, ExpectedUpdatedAt: o.UpdatedAt}),
		http.StatusServiceUnavailable, "dependency_unavailable", "reserve now with Inventory off")
	// A reservation left from when Inventory was on.
	h.stock.put(varBolt, locA1, "5")
	if err := h.stock.Reserve(context.Background(), nil, orderSource(o.Id), siteOslo, []contracts.StockLine{{VariantID: varBolt, LineID: o.Lines[0].Id, Quantity: "2"}}); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version}),
		http.StatusOK, "cancel")
	if got := h.stock.reservedOf(orderSource(o.Id), o.Lines[0].Id); got != "0" {
		t.Errorf("cancel with Inventory off left %s reserved", got)
	}
}

// A draft delivery holds its variant against a delete, and the export
// carries the order's deliveries (O29, O30).
func TestDelivery_HoldersAndExport(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varFood, locA1, "5")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varFood, 2)))
	// Making a delivery needs orders:deliveries-manage (§8).
	mustStatus(t, h.SignIn(t, boss...).Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id}),
		http.StatusForbidden, "a delivery without orders:deliveries-manage")
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "delivery")
	holder := orders.Module().VariantReferences(h.Deps())
	var held *contracts.VariantHold
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		held, err = holder.VariantHeld(ctx, tx, varFood, contracts.VariantHoldDelete)
		return err
	})
	if held == nil || held.Documents != 2 {
		t.Errorf("hold %+v; want the order and its draft delivery", held)
	}
	data, err := orders.Module().CustomerPersonalData(h.Deps()).ExportCustomerData(context.Background(), custBusiness)
	body, _ := json.Marshal(data)
	if err != nil || !strings.Contains(string(body), `"deliveries":[{"number":`) {
		t.Errorf("export %s, %v; want the order's deliveries", body, err)
	}
}
