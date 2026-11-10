package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/operations"
	ordgen "github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/shipping/bring/bringtest"
)

// "Where is my parcel?" (virtual employees design V9b): an agent's run for one
// customer reads that customer's shipments and their tracking, and a shipment,
// an order's shipments or a delivery of another customer is refused, with its
// body dropped, against the real Orders contract and router.
func TestAgentScope_ShipmentReadsHoldOnlyTheRunsCustomer(t *testing.T) {
	t.Parallel()
	bring := bringtest.New(t)
	s := newStockInstallation(t, modtest.WithEnv("SHIPPING_BRING_BASE_URL", bring.URL()), modtest.WithTransport(bring.Transport()))
	shipper := s.h.SignIn(t, append(append([]string{}, deliveryPermissions...), "orders:carriers-manage")...)
	s.receive(t, s.shelf, 4)
	var account ordgen.OrdersCarrierAccount
	okJSON(t, shipper, http.MethodPost, "/api/v1/orders/carrier-accounts", ordgen.OrdersCarrierAccountInput{
		Carrier: ordgen.OrdersCarrierAccountInputCarrierBring, Name: "Bring", Test: true, CustomerNumbers: []string{bringtest.CustomerParcels},
		Credentials: &ordgen.OrdersCarrierCredentials{Uid: bringtest.UID, Key: bringtest.Key}, DefaultService: strPtr("5000"),
	}, &account)
	okJSON(t, shipper, http.MethodPut, "/api/v1/orders/shipping/site-defaults", ordgen.OrdersSiteCarrierDefaultsInput{
		Items: []ordgen.OrdersSiteCarrierDefaultInput{{SiteId: s.site, AccountId: &account.Id}},
	}, nil)

	// shipped is a booked shipment of a one-shelf order of the customer.
	shipped := func(customer int32) (order ordgen.OrdersOrder, d ordgen.OrdersDelivery, sh ordgen.OrdersShipment) {
		sc := s
		sc.customer = customer
		draft := sc.draftOrder(t, orderLine(s.shelf, 1, 0))
		if res := sc.confirm(sc.c, draft, true); res.Status != http.StatusOK {
			t.Fatalf("confirm = %d %s", res.Status, res.Body)
		}
		d = deliveryFrom(t, sc.newDelivery(draft.Id), http.StatusCreated, "delivery")
		d = deliveryFrom(t, sc.post(d), http.StatusOK, "post")
		okJSON(t, shipper, http.MethodPost, fmt.Sprintf("%s/%d/shipments", ordersDeliveriesPath, d.Id), ordgen.OrdersShipmentInput{}, &sh)
		okJSON(t, shipper, http.MethodPost, fmt.Sprintf("/api/v1/orders/shipments/%d/book", sh.Id),
			ordgen.OrdersVersionInput{Version: sh.Version, ExpectedUpdatedAt: sh.UpdatedAt}, &sh)
		return sc.order(t, draft.Id), d, sh
	}
	own := s.customer
	other := invoiceNewBusiness(t, s.c, "Elv Sør", "Elv Sør AS", "974760673").Id
	ownOrder, ownDelivery, ownShipment := shipped(own)
	otherOrder, otherDelivery, otherShipment := shipped(other)

	ops := s.h.Operations(t)
	ops.SetGate(contracts.GrantAgent, func(_ context.Context, info operations.Info) operations.Mode { return info.Mode })
	agent, _ := s.h.AgentContext(t, &own, "orders:access")

	// The run's customer's parcel, and its tracking link, are readable; the
	// customer is filled in on the lists.
	res, err := ops.Call(agent, operations.CallRequest{OperationID: "listOrderShipments", PathParams: map[string]any{"id": ownOrder.Id}})
	if err != nil || res.Status != http.StatusOK || !strings.Contains(string(res.Body), fmt.Sprintf(`"id":%d,`, ownShipment.Id)) || !strings.Contains(string(res.Body), `"trackingUrl"`) {
		t.Errorf("listOrderShipments of the run's customer: %v %d %s", err, res.Status, res.Body)
	}
	for _, req := range []operations.CallRequest{
		{OperationID: "getShipment", PathParams: map[string]any{"id": ownShipment.Id}},
		{OperationID: "getDelivery", PathParams: map[string]any{"id": ownDelivery.Id}},
		{OperationID: "listDeliveryShipments", PathParams: map[string]any{"id": ownDelivery.Id}},
	} {
		if res, err := ops.Call(agent, req); err != nil || res.Status != http.StatusOK {
			t.Errorf("%s of the run's customer: %v %d %s", req.OperationID, err, res.Status, res.Body)
		}
	}
	res, err = ops.Call(agent, operations.CallRequest{OperationID: "listDeliveries"})
	var deliveries struct {
		Items []struct {
			Id         int64 `json:"id"`
			CustomerId int32 `json:"customerId"`
		} `json:"items"`
	}
	if err != nil || res.Status != http.StatusOK || json.Unmarshal(res.Body, &deliveries) != nil ||
		len(deliveries.Items) != 1 || deliveries.Items[0].Id != ownDelivery.Id || deliveries.Items[0].CustomerId != own {
		t.Errorf("listDeliveries holds %s, want only delivery %d", res.Body, ownDelivery.Id)
	}

	// The other customer's: refused by the dispatcher, the body dropped. An id
	// the agent guesses is refused after the handler answered, by the resource;
	// a list asked for another customer, before it.
	for _, req := range []operations.CallRequest{
		{OperationID: "getShipment", PathParams: map[string]any{"id": otherShipment.Id}},
		{OperationID: "getDelivery", PathParams: map[string]any{"id": otherDelivery.Id}},
		{OperationID: "listOrderShipments", PathParams: map[string]any{"id": otherOrder.Id}, Query: map[string]any{"customerId": other}},
		{OperationID: "listDeliveryShipments", PathParams: map[string]any{"id": otherDelivery.Id}, Query: map[string]any{"customerId": other}},
		{OperationID: "listDeliveries", Query: map[string]any{"customerId": other}},
	} {
		res, err := ops.Call(agent, req)
		if !errors.Is(err, operations.ErrCustomerScope) || len(res.Body) != 0 {
			t.Errorf("%s for the other customer = %+v, %v, want customer_scope and no body", req.OperationID, res, err)
		}
	}

	// A list by the other customer's order id, with the run's customer filled
	// in, finds no such order: the handler answers 404, not that customer's parcels.
	for _, req := range []operations.CallRequest{
		{OperationID: "listOrderShipments", PathParams: map[string]any{"id": otherOrder.Id}},
		{OperationID: "listDeliveryShipments", PathParams: map[string]any{"id": otherDelivery.Id}},
	} {
		res, err := ops.Call(agent, req)
		if err != nil || res.Status != http.StatusNotFound || len(res.Body) != 0 {
			t.Errorf("%s by the other customer's id = %+v, %v, want a 404 with no body", req.OperationID, res, err)
		}
	}
}
