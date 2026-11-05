package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	ordgen "github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/shipping/bring/bringtest"
)

// Shipping (orders design O49–O53, delivery D), end to end against the real
// modules and the real Bring adapter over HTTP to bringtest: an order with a
// shipping line is confirmed, delivered from the shelf and shipped; the
// booking stores the label and counts the shipping line delivered; tracking
// reports it delivered; and the invoice made from the order carries the
// shipping line.
func TestOrdersShipping_EndToEnd(t *testing.T) {
	t.Parallel()
	bring := bringtest.New(t)
	s := newStockInstallation(t, modtest.WithEnv("SHIPPING_BRING_BASE_URL", bring.URL()), modtest.WithTransport(bring.Transport()))
	shipper := s.h.SignIn(t, append(append([]string{}, deliveryPermissions...), "orders:carriers-manage")...)
	s.receive(t, s.shelf, 4)

	// The carrier account under Integrations, the site's default.
	var account ordgen.OrdersCarrierAccount
	okJSON(t, shipper, http.MethodPost, "/api/v1/orders/carrier-accounts", ordgen.OrdersCarrierAccountInput{
		Carrier: ordgen.OrdersCarrierAccountInputCarrierBring, Name: "Bring", Test: true, CustomerNumbers: []string{bringtest.CustomerParcels},
		Credentials: &ordgen.OrdersCarrierCredentials{Uid: bringtest.UID, Key: bringtest.Key}, DefaultService: strPtr("5000"),
	}, &account)
	okJSON(t, shipper, http.MethodPut, "/api/v1/orders/shipping/site-defaults", ordgen.OrdersSiteCarrierDefaultsInput{
		Items: []ordgen.OrdersSiteCarrierDefaultInput{{SiteId: s.site, AccountId: &account.Id}},
	}, nil)

	// An order of two shelves and its freight, the freight's price estimated.
	freight := ordgen.OrdersLineInput{Kind: ordgen.OrdersLineInputKindShipping, Name: strPtr("Frakt"), UnitPrice: f64Ptr(149)}
	draft := s.draftOrder(t, orderLine(s.shelf, 2, 0), freight)
	var estimate ordgen.OrdersShippingEstimate
	okJSON(t, shipper, http.MethodPost, "/api/v1/orders/shipping-estimate", ordgen.OrdersShippingEstimateInput{OrderId: &draft.Id}, &estimate)
	if estimate.AccountId != account.Id || estimate.Service != "5000" || estimate.CarrierPrice <= 0 {
		t.Fatalf("estimate %+v", estimate)
	}
	if res := s.confirm(s.c, draft, true); res.Status != http.StatusOK {
		t.Fatalf("confirm = %d %s", res.Status, res.Body)
	}
	d := deliveryFrom(t, s.newDelivery(draft.Id), http.StatusCreated, "delivery")
	d = deliveryFrom(t, s.post(d), http.StatusOK, "post")

	// Ship it.
	var sh ordgen.OrdersShipment
	okJSON(t, shipper, http.MethodPost, fmt.Sprintf("%s/%d/shipments", ordersDeliveriesPath, d.Id), ordgen.OrdersShipmentInput{}, &sh)
	okJSON(t, shipper, http.MethodPost, fmt.Sprintf("/api/v1/orders/shipments/%d/book", sh.Id),
		ordgen.OrdersVersionInput{Version: sh.Version, ExpectedUpdatedAt: sh.UpdatedAt}, &sh)
	if sh.Status != ordgen.OrdersShipmentStatusBooked || sh.ConsignmentNumber == nil || !sh.Label || len(bring.Consignments()) != 1 {
		t.Fatalf("booked %+v, %d bookings", sh, len(bring.Consignments()))
	}
	label := shipper.Do(http.MethodGet, fmt.Sprintf("/api/v1/orders/shipments/%d/label", sh.Id), nil)
	if label.Status != http.StatusOK || !strings.HasPrefix(string(label.Body), "%PDF") {
		t.Fatalf("label = %d", label.Status)
	}
	order := s.order(t, draft.Id)
	var shippingLine ordgen.OrdersLine
	for _, l := range order.Lines {
		if l.Kind == ordgen.OrdersLineKindShipping {
			shippingLine = l
		}
	}
	if shippingLine.QtyDelivered != 1 || shippingLine.InvoicePolicy != ordgen.OrdersLineInvoicePolicyOrder {
		t.Fatalf("shipping line %+v, want delivered on booking and invoiced on order", shippingLine)
	}

	// Tracking reports it delivered.
	bring.SetEvents(*sh.ConsignmentNumber, bringtest.Event{Status: "DELIVERED", Description: "Delivered", At: s.h.Now().Add(time.Hour)})
	s.h.Advance(31 * time.Minute)
	// The composed worker's first cycle runs at once.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := false
	for _, w := range s.h.Workers(s.h.Deps()) {
		if w.Name() == "orders.shipment_tracking" {
			started = true
			go func() { _ = w.Run(ctx) }()
		}
	}
	if !started {
		t.Fatal("no orders.shipment_tracking worker is composed")
	}
	for sh.Status != ordgen.OrdersShipmentStatusDelivered && ctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
		okJSON(t, shipper, http.MethodGet, fmt.Sprintf("/api/v1/orders/shipments/%d", sh.Id), nil, &sh)
	}
	cancel()
	if sh.Status != ordgen.OrdersShipmentStatusDelivered {
		t.Fatalf("after tracking %s", sh.Status)
	}

	// The invoice carries the goods and the freight.
	drafts := s.fromOrders(t, false, whole(order)...)
	inv := s.issue(t, drafts[0].InvoiceId)
	found := false
	for _, l := range inv.Lines {
		found = found || (l.Quantity == 1 && l.UnitPrice == 149 && l.LineNet == 149)
	}
	if !found || len(inv.Lines) != 2 {
		t.Fatalf("invoice lines %+v, want the shelves and the freight", inv.Lines)
	}
}

func strPtr(v string) *string   { return &v }
func f64Ptr(v float64) *float64 { return &v }
