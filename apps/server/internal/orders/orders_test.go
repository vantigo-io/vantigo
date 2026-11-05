package orders_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// The permission sets of the order tests (§8).
var (
	seller = []string{"orders:access", "orders:sales-manage"}
	boss   = []string{"orders:access", "orders:sales-manage", "orders:sales-confirm"}
)

const ordersPath = "/api/v1/orders/orders"

func orderPath(id int64, rest ...string) string {
	p := fmt.Sprintf("%s/%d", ordersPath, id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

func orderOf(t *testing.T, res *modtest.Response) gen.OrdersOrder {
	t.Helper()
	var o gen.OrdersOrder
	res.JSON(&o)
	return o
}

// createOrder creates a draft order for customer with lines and answers it.
func createOrder(t *testing.T, c *modtest.Client, customer int32, lines ...gen.OrdersLineInput) gen.OrdersOrder {
	t.Helper()
	if lines == nil {
		lines = []gen.OrdersLineInput{}
	}
	res := c.Do(http.MethodPost, ordersPath, gen.OrdersOrderInput{ClientRequestId: uuid.New(), CustomerId: customer, Lines: &lines})
	mustStatus(t, res, http.StatusCreated, "create order")
	return orderOf(t, res)
}

// confirmOrder confirms a draft and answers it.
func confirmOrder(t *testing.T, c *modtest.Client, o gen.OrdersOrder) gen.OrdersOrder {
	t.Helper()
	res := c.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version})
	mustStatus(t, res, http.StatusOK, "confirm")
	return orderOf(t, res)
}

// keep is an order's lines as a save keeps them, at their quantities.
func keep(o gen.OrdersOrder) []gen.OrdersLineInput {
	out := make([]gen.OrdersLineInput, 0, len(o.Lines))
	for _, l := range o.Lines {
		id := l.Id
		out = append(out, gen.OrdersLineInput{Id: &id, Kind: gen.OrdersLineInputKind(l.Kind), VariantId: l.VariantId, Quantity: l.Quantity})
	}
	return out
}

// save puts o back with lines.
func save(c *modtest.Client, o gen.OrdersOrder, lines []gen.OrdersLineInput, edit func(*gen.OrdersOrderUpdate)) *modtest.Response {
	body := gen.OrdersOrderUpdate{Version: o.Version, ExpectedUpdatedAt: o.UpdatedAt, CustomerId: o.Customer.CustomerId, Lines: lines}
	if edit != nil {
		edit(&body)
	}
	return c.Do(http.MethodPut, orderPath(o.Id), body)
}

// lineOf is the order's line of a variant.
func lineOf(t *testing.T, o gen.OrdersOrder, variant int32) gen.OrdersLine {
	t.Helper()
	for _, l := range o.Lines {
		if l.VariantId != nil && *l.VariantId == variant {
			return l
		}
	}
	t.Fatalf("order %d has no line of variant %d", o.Id, variant)
	return gen.OrdersLine{}
}

// deliver marks qty of the order's line delivered and answers the order.
func deliver(t *testing.T, c *modtest.Client, o gen.OrdersOrder, lineID int64, qty float64) gen.OrdersOrder {
	t.Helper()
	res := c.Do(http.MethodPost, orderPath(o.Id, "mark-delivered"), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, o.Id),
		Version: o.Version, Lines: []gen.OrdersDeliveredLine{{LineId: lineID, Quantity: qty}},
	})
	mustStatus(t, res, http.StatusOK, "mark delivered")
	return orderOf(t, res)
}

// changesOf is an order's change log's action kinds, entry by entry.
func changesOf(t *testing.T, c *modtest.Client, id int64) [][]string {
	t.Helper()
	res := c.Do(http.MethodGet, orderPath(id, "changes"), nil)
	mustStatus(t, res, http.StatusOK, "changes")
	var list gen.OrdersChangeList
	res.JSON(&list)
	var out [][]string
	for _, e := range list.Items {
		var kinds []string
		for _, a := range e.Actions {
			kinds = append(kinds, a.Kind)
		}
		out = append(out, kinds)
	}
	return out
}

// An order is created through the create command (O28) as a draft with the
// next number, the origin channel manual, the customer snapshotted and the
// catalog priced; a repeated clientRequestId answers it with 200 (O27), the
// same id for another customer is refused; an optional line and a disabled
// customer are refused.
func TestCreateOrder_DraftRepeatAndRefusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, seller...)
	id := uuid.New()
	lines := []gen.OrdersLineInput{productLine(varBolt, 3)}
	body := gen.OrdersOrderInput{ClientRequestId: id, CustomerId: custBusiness, Lines: &lines}
	first := c.Do(http.MethodPost, ordersPath, body)
	mustStatus(t, first, http.StatusCreated, "create")
	o := orderOf(t, first)
	if o.Number != 1 || o.Status != gen.OrdersOrderStatusDraft || o.Version != 1 || o.Origin.Channel != "manual" ||
		o.Customer.Name != "Acme AS" || o.Totals.Net != 300 || o.SourceQuoteId != nil {
		t.Errorf("order %+v; want draft 1 at version 1, manual, Acme AS, net 300", o)
	}
	if o.DefaultRecipient == nil || *o.DefaultRecipient != "faktura@acme.example" || o.Customer.InvoiceEmail == nil {
		t.Errorf("default recipient %v, invoice e-mail %v; want the invoice e-mail with no contact", o.DefaultRecipient, o.Customer.InvoiceEmail)
	}
	again := c.Do(http.MethodPost, ordersPath, body)
	mustStatus(t, again, http.StatusOK, "repeat")
	if orderOf(t, again).Id != o.Id {
		t.Error("the repeat made a second order")
	}
	body.CustomerId = custPerson
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, body), http.StatusConflict, "client_request_reused", "reuse")

	optional := productLine(varBolt, 1)
	optional.Optional = ptrBool(true)
	bad := []gen.OrdersLineInput{optional}
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, gen.OrdersOrderInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Lines: &bad}),
		http.StatusBadRequest, "optional_not_allowed", "optional line")
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, gen.OrdersOrderInput{ClientRequestId: uuid.New(), CustomerId: custDisabled}),
		http.StatusBadRequest, "customer_disabled", "disabled customer")
	mustStatus(t, h.SignIn(t, reader...).Do(http.MethodPost, ordersPath, gen.OrdersOrderInput{ClientRequestId: uuid.New(), CustomerId: custBusiness}),
		http.StatusForbidden, "create without sales-manage")
	if n := h.Count(t, `SELECT count(*) FROM orders.orders`); n != 1 {
		t.Errorf("%d orders, want 1", n)
	}
}

func ptrBool(v bool) *bool { return &v }

// The create command keeps one order per channel reference (O27): a channel
// handing in the same external id again gets the first order back.
func TestCreateCommand_IsIdempotentByChannelAndExternalID(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	ctx := context.Background()
	first, created, err := orders.CreateChannelOrder(ctx, h.Deps(), custBusiness, "webshop", "WS-1", varBolt, 2)
	if err != nil || !created {
		t.Fatalf("first create: %v, created %v", err, created)
	}
	again, created, err := orders.CreateChannelOrder(ctx, h.Deps(), custBusiness, "webshop", "WS-1", varBolt, 5)
	if err != nil || created || again != first {
		t.Fatalf("repeat: %d (created %v, %v); want %d, not created", again, created, err, first)
	}
	other, created, err := orders.CreateChannelOrder(ctx, h.Deps(), custBusiness, "pos", "WS-1", varBolt, 1)
	if err != nil || !created || other == first {
		t.Fatalf("another channel's reference: %d (created %v, %v); want a new order", other, created, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.orders`); n != 2 {
		t.Errorf("%d orders, want 2", n)
	}
}

// Confirm (O18) refuses an empty order, an unpriced line, an unmapped VAT
// code, a disabled customer and a stale version; it needs sales-confirm.
// Confirmed, the order freezes its snapshot (O14), stores the confirmation of
// version 1 (O24), publishes orders.sales_order.confirmed.v1 once and is no
// longer confirmable.
func TestConfirmOrder_RefusalsFreezeAndPublish(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, seller...)
	b := h.SignIn(t, boss...)

	empty := createOrder(t, c, custBusiness)
	expectRefusal(t, b.Do(http.MethodPost, orderPath(empty.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, b, empty.Id), Version: 1}), http.StatusBadRequest, "order_empty", "empty")
	unpriced := createOrder(t, c, custBusiness, productLine(varNoPrice, 1))
	expectRefusal(t, b.Do(http.MethodPost, orderPath(unpriced.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, b, unpriced.Id), Version: 1}), http.StatusBadRequest, "line_unpriced", "unpriced")
	unmapped := createOrder(t, c, custBusiness, productLine(varUnmapped, 1))
	expectRefusal(t, b.Do(http.MethodPost, orderPath(unmapped.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, b, unmapped.Id), Version: 1}), http.StatusBadRequest, "vat_code_unmapped", "unmapped")

	o := createOrder(t, c, custBusiness, productLine(varBolt, 2))
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1}), http.StatusForbidden, "confirm without the key")
	expectRefusal(t, b.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, b, o.Id), Version: 7}), http.StatusConflict, "version_conflict", "stale")
	h.dir.update(custBusiness, func(p *contracts.CustomerBillingProfile) { p.Status = "disabled" })
	expectRefusal(t, b.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, b, o.Id), Version: 1}), http.StatusBadRequest, "customer_disabled", "disabled")
	h.dir.update(custBusiness, func(p *contracts.CustomerBillingProfile) { p.Status = "active" })

	confirmed := confirmOrder(t, b, o)
	if confirmed.Status != gen.OrdersOrderStatusConfirmed || confirmed.Version != 1 || confirmed.ConfirmedAt == nil || confirmed.ConfirmedBy == nil {
		t.Errorf("confirmed %+v; want confirmed at version 1, by the caller", confirmed)
	}
	if n := eventsNamed(t, h, "orders.sales_order.confirmed", o.Id); n != 1 {
		t.Errorf("%d confirmed events, want 1", n)
	}
	var docs gen.OrdersStoredDocumentList
	res := b.Do(http.MethodGet, orderPath(o.Id, "documents"), nil)
	mustStatus(t, res, http.StatusOK, "documents")
	res.JSON(&docs)
	if len(docs.Items) != 1 || docs.Items[0].Version != 1 || docs.Items[0].Kind != gen.OrdersStoredDocumentKindConfirmation {
		t.Errorf("documents %+v; want version 1's confirmation", docs)
	}

	// The snapshot is frozen: a renamed customer is not followed.
	h.dir.update(custBusiness, func(p *contracts.CustomerBillingProfile) { p.Name = "Acme Holding AS" })
	res = c.Do(http.MethodGet, orderPath(o.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	if got := orderOf(t, res); got.Customer.Name != "Acme AS" {
		t.Errorf("the confirmed order's customer is %q, want the frozen Acme AS", got.Customer.Name)
	}
	expectRefusal(t, b.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, b, o.Id), Version: 1}), http.StatusConflict, "order_not_draft", "confirm again")
}

// A draft's save follows the customer (O14) and prices its catalog lines
// again when the day moved (O16); a manual price is kept. The version stays
// 1 while the order is a draft.
func TestSaveDraftOrder_FollowsTheCustomerAndReprices(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, seller...)
	free := line(gen.OrdersLineInputKindProduct)
	free.Name, free.Quantity, free.UnitPrice = str("Freight"), f64(1), f64(250)
	o := createOrder(t, c, custBusiness, productLine(varBolt, 1), free)

	h.dir.update(custBusiness, func(p *contracts.CustomerBillingProfile) { p.Name = "Acme Holding AS" })
	h.catalog.setPrice(varBolt, "NOK", "120.00")
	// The draft was last saved yesterday.
	h.Exec(t, `UPDATE orders.orders SET updated_at = updated_at - interval '1 day' WHERE id = $1`, o.Id)
	o.UpdatedAt = orderAt(t, c, o.Id)
	res := save(c, o, keep(o), func(b *gen.OrdersOrderUpdate) { b.ContactId = i32(contactKari) })
	mustStatus(t, res, http.StatusOK, "save")
	got := orderOf(t, res)
	if got.Customer.Name != "Acme Holding AS" || got.Version != 1 || got.Customer.Contact.Email == nil ||
		got.DefaultRecipient == nil || *got.DefaultRecipient != "kari@acme.example" {
		t.Errorf("saved %+v; want the new name, version 1 and the contact's e-mail as the recipient", got)
	}
	if l := lineOf(t, got, varBolt); l.UnitPrice == nil || *l.UnitPrice != 120 {
		t.Errorf("bolt price %v, want the new list price 120", l.UnitPrice)
	}
	if got.Lines[1].UnitPrice == nil || *got.Lines[1].UnitPrice != 250 {
		t.Errorf("freight %v, want the manual 250 kept", got.Lines[1].UnitPrice)
	}
	expectRefusal(t, save(c, got, keep(got), func(b *gen.OrdersOrderUpdate) { b.Version = 3 }), http.StatusConflict, "version_conflict", "stale")
}

// A confirmed order is editable in its open quantity (O26): a quantity may
// not go below its progress, a line with progress may not go, the frozen
// header may not change, a locked order refuses every edit; an allowed save
// logs one entry of typed actions, bumps the version and publishes
// orders.sales_order.changed.v1; a stale version is refused.
func TestEditConfirmedOrder_Rules(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10), productLine(varFood, 2)))
	bolt := lineOf(t, o, varBolt)
	o = deliver(t, c, o, bolt.Id, 4)

	lines := keep(o)
	lines[0].Quantity = f64(3)
	p := expectRefusal(t, save(c, o, lines, nil), http.StatusBadRequest, "quantity_below_progress", "below delivered")
	if p.Lines == nil || (*p.Lines)[0].Index != 0 {
		t.Errorf("lines %+v; want line 0 named", p.Lines)
	}
	expectRefusal(t, save(c, o, keep(o)[1:], nil), http.StatusConflict, "line_has_progress", "remove a delivered line")
	expectRefusal(t, save(c, o, keep(o), func(b *gen.OrdersOrderUpdate) { b.Currency = str("EUR") }), http.StatusBadRequest, "", "change the currency")

	lines = keep(o)
	lines[0].Quantity = f64(6)
	lines[1].UnitPrice = f64(30)
	lines = append(lines, productLine(varOld, 1))
	res := save(c, o, lines, func(b *gen.OrdersOrderUpdate) {
		b.YourReference = str("PO-78")
		b.DeliveryAddressId = i32(addrShop)
	})
	mustStatus(t, res, http.StatusOK, "edit")
	edited := orderOf(t, res)
	if edited.Version != 2 || edited.Status != gen.OrdersOrderStatusConfirmed || len(edited.Lines) != 3 || edited.YourReference != "PO-78" ||
		edited.Customer.DeliveryAddressId == nil || *edited.Customer.DeliveryAddressId != addrShop {
		t.Fatalf("edited %+v; want version 2 with three lines, the new reference and address", edited)
	}
	if l := lineOf(t, edited, varBolt); l.QtyDelivered != 4 || *l.Quantity != 6 || *l.UnitPrice != 100 {
		t.Errorf("bolt %+v; want 6 at the frozen 100 with 4 delivered kept", l)
	}
	if edited.DeliveryStatus != gen.OrdersOrderDeliveryStatusPartial {
		t.Errorf("delivery status %s, want partial", edited.DeliveryStatus)
	}
	changes := changesOf(t, c, o.Id)
	last := strings.Join(changes[len(changes)-1], ",")
	for _, want := range []string{"delivery_changed", "reference_changed", "quantity_changed", "price_changed", "line_added"} {
		if !strings.Contains(last, want) {
			t.Errorf("the edit's actions %s lack %s", last, want)
		}
	}
	if n := eventsNamed(t, h, "orders.sales_order.changed", o.Id); n != 1 {
		t.Errorf("%d changed events, want 1", n)
	}
	expectRefusal(t, save(c, o, keep(o), nil), http.StatusConflict, "version_conflict", "a second editor's stale save")

	res = c.Do(http.MethodPost, orderPath(o.Id, "lock"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: edited.Version})
	mustStatus(t, res, http.StatusOK, "lock")
	expectRefusal(t, save(c, edited, keep(edited), nil), http.StatusConflict, "order_locked", "edit a locked order")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "unlock"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: edited.Version}), http.StatusOK, "unlock")
	mustStatus(t, save(c, edited, keep(edited), nil), http.StatusOK, "a save that changes nothing")
	if n := eventsNamed(t, h, "orders.sales_order.changed", o.Id); n != 1 {
		t.Errorf("%d changed events after a save that changed nothing, want still 1", n)
	}
}

// Hold and release, lock and unlock (O18): a confirmed order only, each
// logged; lock needs sales-confirm; a held order is still editable.
func TestHoldReleaseLockUnlock(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	s := h.SignIn(t, seller...)
	draft := createOrder(t, c, custBusiness, productLine(varBolt, 1))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "hold"), gen.OrdersHoldInput{ExpectedUpdatedAt: orderAt(t, c, draft.Id), Version: 1}), http.StatusConflict, "order_not_confirmed", "hold a draft")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "lock"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, draft.Id), Version: 1}), http.StatusConflict, "order_not_confirmed", "lock a draft")

	o := confirmOrder(t, c, draft)
	res := s.Do(http.MethodPost, orderPath(o.Id, "hold"), gen.OrdersHoldInput{ExpectedUpdatedAt: orderAt(t, s, o.Id), Version: 1, Reason: str("Awaiting prepayment")})
	mustStatus(t, res, http.StatusOK, "hold")
	held := orderOf(t, res)
	if !held.OnHold || held.HoldReason == nil || *held.HoldReason != "Awaiting prepayment" || held.Version != 1 {
		t.Errorf("held %+v; want on hold with the reason, version 1", held)
	}
	expectRefusal(t, s.Do(http.MethodPost, orderPath(o.Id, "hold"), gen.OrdersHoldInput{ExpectedUpdatedAt: orderAt(t, s, o.Id), Version: 1, Reason: str(strings.Repeat("x", 201))}),
		http.StatusBadRequest, "", "a long reason")
	lines := keep(held)
	lines[0].Quantity = f64(2)
	mustStatus(t, save(s, held, lines, nil), http.StatusOK, "edit a held order")
	res = s.Do(http.MethodPost, orderPath(o.Id, "release"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, s, o.Id), Version: 2})
	mustStatus(t, res, http.StatusOK, "release")
	if got := orderOf(t, res); got.OnHold || got.HoldReason != nil {
		t.Errorf("released %+v; want off hold", got)
	}
	mustStatus(t, s.Do(http.MethodPost, orderPath(o.Id, "lock"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, s, o.Id), Version: 2}), http.StatusForbidden, "lock without the key")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "lock"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 2}), http.StatusOK, "lock")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "unlock"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 2}), http.StatusOK, "unlock")
	var kinds []string
	for _, e := range changesOf(t, c, o.Id) {
		kinds = append(kinds, e...)
	}
	if got := strings.Join(kinds, ","); got != "held,quantity_changed,released,locked,unlocked" {
		t.Errorf("change log %s; want held, quantity_changed, released, locked, unlocked", got)
	}
}

// Cancel (O18): a draft or a confirmed order with no progress; cancelled.v1
// for the confirmed one only; with progress it is refused and Cancel
// remaining cancels ordered less delivered on each line, bumps the version,
// logs cancelled_remaining and publishes changed. Only a draft is deleted.
func TestCancelAndCancelRemaining(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	draft := createOrder(t, c, custBusiness, productLine(varBolt, 1))
	res := c.Do(http.MethodPost, orderPath(draft.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, draft.Id), Version: 1})
	mustStatus(t, res, http.StatusOK, "cancel a draft")
	if orderOf(t, res).Status != gen.OrdersOrderStatusCancelled || eventsNamed(t, h, "orders.sales_order.cancelled", draft.Id) != 0 {
		t.Error("a cancelled draft: want cancelled and no event")
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, draft.Id), Version: 1}), http.StatusConflict, "order_not_confirmed", "cancel twice")

	plain := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 1)))
	mustStatus(t, c.Do(http.MethodPost, orderPath(plain.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, plain.Id), Version: 1}), http.StatusOK, "cancel a confirmed order")
	if n := eventsNamed(t, h, "orders.sales_order.cancelled", plain.Id); n != 1 {
		t.Errorf("%d cancelled events, want 1", n)
	}
	expectRefusal(t, c.Do(http.MethodDelete, withUpdatedAt(orderPath(plain.Id), orderAt(t, c, plain.Id)), nil), http.StatusConflict, "order_not_draft", "delete a cancelled order")

	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10), productLine(varFood, 3)))
	o = deliver(t, c, o, lineOf(t, o, varBolt).Id, 4)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version}), http.StatusConflict, "order_has_progress", "cancel with progress")
	res = c.Do(http.MethodPost, orderPath(o.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version})
	mustStatus(t, res, http.StatusOK, "cancel remaining")
	rest := orderOf(t, res)
	if rest.Version != 2 || lineOf(t, rest, varBolt).QtyCancelled != 6 || lineOf(t, rest, varFood).QtyCancelled != 3 ||
		rest.DeliveryStatus != gen.OrdersOrderDeliveryStatusDelivered {
		t.Errorf("after cancel remaining %+v; want version 2, 6 and 3 cancelled, delivered", rest)
	}
	if n := eventsNamed(t, h, "orders.sales_order.changed", o.Id); n != 1 {
		t.Errorf("%d changed events, want 1", n)
	}
	res = c.Do(http.MethodPost, orderPath(o.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 2})
	mustStatus(t, res, http.StatusOK, "cancel remaining again")
	if orderOf(t, res).Version != 2 {
		t.Error("a cancel remaining with nothing left bumped the version")
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, draft.Id), Version: 1}), http.StatusConflict, "order_not_confirmed", "cancel remaining of a cancelled order")

	gone := createOrder(t, c, custBusiness, productLine(varBolt, 1))
	mustStatus(t, c.Do(http.MethodDelete, withUpdatedAt(orderPath(gone.Id), orderAt(t, c, gone.Id)), nil), http.StatusNoContent, "delete a draft")
	mustStatus(t, c.Do(http.MethodGet, orderPath(gone.Id), nil), http.StatusNotFound, "get the deleted draft")
	if n := h.Count(t, `SELECT count(*) FROM orders.lines WHERE owner_kind = 'order' AND owner_id = $1`, gone.Id); n != 0 {
		t.Errorf("%d lines left of the deleted draft", n)
	}
}

// Manual delivery marking (O32): never beyond ordered less cancelled, an
// undo never below 0, a line not the order's refused, every failing line
// named at once; the delivery status follows and each marking is logged.
func TestMarkDelivered_AndUndo(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	draft := createOrder(t, c, custBusiness, productLine(varBolt, 10), productLine(varFood, 2))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "mark-delivered"), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, draft.Id),
		Version: 1, Lines: []gen.OrdersDeliveredLine{{LineId: draft.Lines[0].Id, Quantity: 1}},
	}), http.StatusConflict, "order_not_confirmed", "mark a draft")
	o := confirmOrder(t, c, draft)
	bolt, food := lineOf(t, o, varBolt), lineOf(t, o, varFood)

	p := expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "mark-delivered"), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, o.Id),
		Version: 1, Lines: []gen.OrdersDeliveredLine{{LineId: bolt.Id, Quantity: 11}, {LineId: 999, Quantity: 1}, {LineId: food.Id, Quantity: 0}},
	}), http.StatusBadRequest, "over_delivery", "over delivery")
	if p.Lines == nil || len(*p.Lines) != 3 || (*p.Lines)[1].Code != "line_not_found" || (*p.Lines)[2].Code != "quantity_invalid" {
		t.Errorf("lines %+v; want over_delivery, line_not_found and quantity_invalid", p.Lines)
	}
	o = deliver(t, c, o, bolt.Id, 10)
	if o.DeliveryStatus != gen.OrdersOrderDeliveryStatusPartial || o.Version != 1 {
		t.Errorf("after one line %s at version %d; want partial at 1", o.DeliveryStatus, o.Version)
	}
	o = deliver(t, c, o, food.Id, 2)
	if o.DeliveryStatus != gen.OrdersOrderDeliveryStatusDelivered || o.Done {
		t.Errorf("after both %s, done %v; want delivered and not done (nothing invoiced)", o.DeliveryStatus, o.Done)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "undo-delivered"), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, o.Id),
		Version: 1, Lines: []gen.OrdersDeliveredLine{{LineId: food.Id, Quantity: 3}},
	}), http.StatusBadRequest, "undo_exceeds_delivered", "undo below 0")
	res := c.Do(http.MethodPost, orderPath(o.Id, "undo-delivered"), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, o.Id),
		Version: 1, Lines: []gen.OrdersDeliveredLine{{LineId: food.Id, Quantity: 2}, {LineId: bolt.Id, Quantity: 10}},
	})
	mustStatus(t, res, http.StatusOK, "undo")
	if got := orderOf(t, res); got.DeliveryStatus != gen.OrdersOrderDeliveryStatusNone {
		t.Errorf("after the undo %s, want none", got.DeliveryStatus)
	}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "mark-delivered"), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, o.Id),
		Version: 4, Lines: []gen.OrdersDeliveredLine{{LineId: bolt.Id, Quantity: 1}},
	}), http.StatusConflict, "version_conflict", "stale")
	var kinds []string
	for _, e := range changesOf(t, c, o.Id) {
		kinds = append(kinds, strings.Join(e, "+"))
	}
	if got := strings.Join(kinds, ","); got != "delivered_marked,delivered_marked,delivered_undone+delivered_undone" {
		t.Errorf("change log %s", got)
	}
}

// Sending a confirmation (O25, O26) mails the stored PDF of the current
// version and logs it; after an edit the send is of version 2, printed and
// mailed as revised, and the earlier version stays downloadable. A draft is
// not sent, a stale version is refused, and without Communications sending
// is disabled.
func TestSendOrder_AndRevisedConfirmation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, boss...)
	draft := createOrder(t, c, custBusiness, productLine(varBolt, 2))
	expectRefusal(t, c.Do(http.MethodPost, orderPath(draft.Id, "send"), gen.OrdersSendInput{ExpectedUpdatedAt: orderAt(t, c, draft.Id), Recipient: "kari@acme.example", Version: i32(1)}),
		http.StatusConflict, "order_not_confirmed", "send a draft")
	pdf := c.Do(http.MethodGet, orderPath(draft.Id, "pdf"), nil)
	mustStatus(t, pdf, http.StatusOK, "a draft's PDF")
	if !bytes.HasPrefix(pdf.Body, []byte("%PDF")) {
		t.Error("the draft's PDF is no PDF")
	}
	o := confirmOrder(t, c, draft)
	res := c.Do(http.MethodPost, orderPath(o.Id, "send"), gen.OrdersSendInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Recipient: "kari@acme.example", Version: i32(1)})
	mustStatus(t, res, http.StatusOK, "send version 1")
	lines := keep(o)
	lines[0].Quantity = f64(3)
	res = save(c, o, lines, nil)
	mustStatus(t, res, http.StatusOK, "edit")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "send"), gen.OrdersSendInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Recipient: "kari@acme.example", Version: i32(1)}),
		http.StatusConflict, "version_conflict", "send a stale version")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "send"), gen.OrdersSendInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Recipient: "kari@acme.example", Version: i32(2)}),
		http.StatusOK, "send version 2")
	mails := h.mail.Sent()
	if len(mails) != 2 || strings.Contains(mails[0].Out.Subject, "Revidert") || !strings.Contains(mails[1].Out.Subject, "Revidert ordrebekreftelse") {
		t.Fatalf("subjects %v; want a confirmation, then a revised one", subjects(mails))
	}
	var docs gen.OrdersStoredDocumentList
	res = c.Do(http.MethodGet, orderPath(o.Id, "documents"), nil)
	mustStatus(t, res, http.StatusOK, "documents")
	res.JSON(&docs)
	if len(docs.Items) != 2 {
		t.Errorf("%d stored confirmations, want versions 1 and 2", len(docs.Items))
	}
	v1 := c.Do(http.MethodGet, orderPath(o.Id, "pdf")+"?version=1", nil)
	mustStatus(t, v1, http.StatusOK, "version 1's PDF")
	if !bytes.Equal(v1.Body, mails[0].Out.Attachments[0].Content) {
		t.Error("version 1's stored PDF is not the one mailed")
	}
	mustStatus(t, c.Do(http.MethodGet, orderPath(o.Id, "pdf")+"?version=9", nil), http.StatusNotFound, "a version never stored")
	mustStatus(t, c.Do(http.MethodGet, orderPath(o.Id, "proforma"), nil), http.StatusOK, "pro forma")
	var mailings gen.OrdersMailingList
	res = c.Do(http.MethodGet, orderPath(o.Id, "mailings"), nil)
	mustStatus(t, res, http.StatusOK, "mailings")
	res.JSON(&mailings)
	if len(mailings.Items) != 2 || mailings.Items[0].Version != 2 {
		t.Errorf("mailings %+v; want two, the newest version 2's", mailings)
	}

	off := newHarness(t, harnessOptions{})
	oc := off.SignIn(t, boss...)
	plain := confirmOrder(t, oc, createOrder(t, oc, custBusiness, productLine(varBolt, 1)))
	expectRefusal(t, oc.Do(http.MethodPost, orderPath(plain.Id, "send"), gen.OrdersSendInput{ExpectedUpdatedAt: orderAt(t, oc, plain.Id), Recipient: "kari@acme.example", Version: i32(1)}),
		http.StatusServiceUnavailable, "sending_disabled", "send without Communications")
}

func subjects(mails []modtest.FakeOutboundSend) []string {
	var out []string
	for _, m := range mails {
		out = append(out, m.Out.Subject)
	}
	return out
}

// The list filters by customer, status, delivery and invoice status, done and
// a search (O31a).
func TestListOrders_Filters(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	draft := createOrder(t, c, custBusiness, productLine(varBolt, 1))
	confirmed := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2)))
	confirmed = deliver(t, c, confirmed, confirmed.Lines[0].Id, 1)
	person := createOrder(t, c, custPerson, productLine(varBolt, 1))
	list := func(query string) []int64 {
		t.Helper()
		res := c.Do(http.MethodGet, ordersPath+query, nil)
		mustStatus(t, res, http.StatusOK, "list "+query)
		var page gen.OrdersOrderPage
		res.JSON(&page)
		var ids []int64
		for _, it := range page.Items {
			ids = append(ids, it.Id)
		}
		if int(page.Total) != len(ids) {
			t.Errorf("%s: total %d, %d items", query, page.Total, len(ids))
		}
		return ids
	}
	for _, tc := range []struct {
		query string
		want  []int64
	}{
		{"", []int64{person.Id, confirmed.Id, draft.Id}},
		{fmt.Sprintf("?customerId=%d", custBusiness), []int64{confirmed.Id, draft.Id}},
		{"?status=confirmed", []int64{confirmed.Id}},
		{"?deliveryStatus=partial", []int64{confirmed.Id}},
		{"?deliveryStatus=none&deliveryStatus=delivered", []int64{person.Id, draft.Id}},
		{"?invoiceStatus=none", []int64{person.Id, confirmed.Id, draft.Id}},
		{"?invoiceStatus=invoiced", nil},
		{"?done=true", nil},
		{"?done=false&open=true", []int64{person.Id, confirmed.Id, draft.Id}},
		{"?q=Ola", []int64{person.Id}},
	} {
		if got := list(tc.query); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%q = %v, want %v", tc.query, got, tc.want)
		}
	}
	expectRefusal(t, c.Do(http.MethodGet, ordersPath+"?pageSize=500", nil), http.StatusBadRequest, "", "a page too large")
}

// The customer summary (O31a) counts and sums per currency: the open quotes,
// what is ordered and not delivered at each line's open quantity, and what is
// delivered and not invoiced.
func TestCustomerSummary_PerCurrency(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	createQuote(t, c, custBusiness, productLine(varBolt, 1))
	sent := createQuote(t, c, custBusiness, productLine(varBolt, 2))
	markSent(t, c, sent)
	euro := []gen.OrdersLineInput{productLine(varBolt, 1)}
	res := c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Currency: str("EUR"), Lines: &euro})
	mustStatus(t, res, http.StatusCreated, "EUR quote")
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10)))
	deliver(t, c, o, o.Lines[0].Id, 4)
	createOrder(t, c, custBusiness, productLine(varBolt, 5)) // a draft is not ordered

	res = c.Do(http.MethodGet, fmt.Sprintf("/api/v1/orders/customers/%d/summary", custBusiness), nil)
	mustStatus(t, res, http.StatusOK, "summary")
	var sum gen.OrdersCustomerSummary
	res.JSON(&sum)
	if fmt.Sprint(sum.OpenQuotes) != "[{9.5 1 EUR} {300 2 NOK}]" {
		t.Errorf("open quotes %+v; want EUR 9.50 from one, NOK 300 from two", sum.OpenQuotes)
	}
	if fmt.Sprint(sum.OrderedNotDelivered) != "[{600 1 NOK}]" || fmt.Sprint(sum.DeliveredNotInvoiced) != "[{400 1 NOK}]" {
		t.Errorf("ordered %+v, delivered %+v; want NOK 600 to deliver and 400 delivered", sum.OrderedNotDelivered, sum.DeliveredNotInvoiced)
	}
	res = c.Do(http.MethodGet, fmt.Sprintf("/api/v1/orders/customers/%d/summary", custPerson), nil)
	mustStatus(t, res, http.StatusOK, "empty summary")
	res.JSON(&sum)
	if len(sum.OpenQuotes) != 0 || len(sum.OrderedNotDelivered) != 0 || len(sum.DeliveredNotInvoiced) != 0 {
		t.Errorf("summary %+v; want empty", sum)
	}
}

// What a salesperson reads under orders:access alone: the meta, the
// salespeople and the catalog search priced for a line.
func TestMetaSalespeopleAndVariants(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	r := h.SignIn(t, reader...)
	res := r.Do(http.MethodGet, "/api/v1/orders/meta", nil)
	mustStatus(t, res, http.StatusOK, "meta")
	var meta gen.OrdersMeta
	res.JSON(&meta)
	if meta.SendingAvailable || meta.QuoteValidityDays != 30 || meta.DefaultPaymentTermsDays != 14 || meta.BaseCurrency != "NOK" {
		t.Errorf("meta %+v; want no sending before a channel is chosen, 30 and 14 days, NOK", meta)
	}
	chooseChannel(t, h)
	res = r.Do(http.MethodGet, "/api/v1/orders/meta", nil)
	mustStatus(t, res, http.StatusOK, "meta")
	res.JSON(&meta)
	if !meta.SendingAvailable {
		t.Error("sending is not available with a channel chosen")
	}
	res = h.SignIn(t, settingsKey...).Do(http.MethodGet, settingsPath, nil)
	mustStatus(t, res, http.StatusOK, "settings")
	var st gen.OrdersSettings
	res.JSON(&st)
	if st.SendChannelState != gen.Ready || len(st.SendChannels) != 1 || st.SendChannels[0].Id != h.channel {
		t.Errorf("settings channels %+v state %s; want the one channel, ready", st.SendChannels, st.SendChannelState)
	}

	_, quoterID := h.SignInUser(t, quoter...)
	_, sellerID := h.SignInUser(t, seller...)
	res = r.Do(http.MethodGet, "/api/v1/orders/salespeople", nil)
	mustStatus(t, res, http.StatusOK, "salespeople")
	var people gen.OrdersSalespersonList
	res.JSON(&people)
	ids := map[uuid.UUID]bool{}
	for _, p := range people.Items {
		ids[p.Id] = true
	}
	if !ids[quoterID] || !ids[sellerID] || len(people.Items) != 2 {
		t.Errorf("salespeople %+v; want the quoter and the seller only", people.Items)
	}

	res = r.Do(http.MethodGet, "/api/v1/orders/variants?q=bolt&currency=EUR", nil)
	mustStatus(t, res, http.StatusOK, "variants")
	var found gen.OrdersVariantList
	res.JSON(&found)
	if len(found.Items) != 1 || found.Items[0].Price == nil || *found.Items[0].Price != 9.5 || found.Items[0].VatCodeId == nil ||
		*found.Items[0].VatCodeId != codeStandard || len(found.Items[0].Packagings) != 1 || found.Items[0].Currency != "EUR" {
		t.Errorf("variants %+v; want the bolt at EUR 9.50, code 3, its case", found.Items)
	}
	res = r.Do(http.MethodGet, "/api/v1/orders/variants?q=", nil)
	mustStatus(t, res, http.StatusOK, "every active variant")
	res.JSON(&found)
	for _, v := range found.Items {
		if v.Id == varOld {
			t.Error("a discontinued product's variant was answered")
		}
		if v.Id == varUnmapped && v.VatCodeId != nil {
			t.Error("an unmapped category answered a VAT code")
		}
		if v.Id == varNoPrice && v.Price != nil {
			t.Error("a variant with no list price answered one")
		}
	}
	expectRefusal(t, r.Do(http.MethodGet, "/api/v1/orders/variants?currency=EU", nil), http.StatusBadRequest, "", "a bad currency")
}
