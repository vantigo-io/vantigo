package orders_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// TestDeliveryTrace is contracts.DeliveryTrace (manufacturing phase 2 M90):
// provider-gated on orders:access, refusing with ErrForbidden a caller
// without it and a context with no principal; a posted delivery answered
// with its order's number and customer, a draft or an unknown id absent;
// and the meta's canTraceLots, which the delivery page's lot links follow
// (M94).
func TestDeliveryTrace(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "6")
	// delivered posts a delivery of a new order of n bolts when post is
	// set, and leaves it a draft otherwise.
	delivered := func(n float64, post bool) (gen.OrdersOrder, gen.OrdersDelivery) {
		o := createOrder(t, c, custBusiness, productLine(varBolt, n))
		mustStatus(t, confirmWith(t, c, o, false), http.StatusOK, "confirm")
		o = getOrder(t, c, o.Id)
		res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
		mustStatus(t, res, http.StatusCreated, "create delivery")
		d := deliveryOf(t, res)
		if post {
			res = c.Do(http.MethodPost, deliveryPath(d.Id, "post"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt})
			mustStatus(t, res, http.StatusOK, "post")
			d = deliveryOf(t, res)
		}
		return o, d
	}
	o, d := delivered(4, true)
	_, draft := delivered(1, false)

	trace := orders.Module().DeliveryTrace(h.Deps())
	ids := []int64{d.Id, 999999}
	if _, err := trace.Deliveries(context.Background(), ids); !errors.Is(err, contracts.ErrForbidden) {
		t.Errorf("without a principal = %v, want ErrForbidden", err)
	}
	_, outsiderID := h.SignInUser(t, "inventory:stock-view")
	if got, err := trace.Deliveries(contracts.AsUser(context.Background(), outsiderID), ids); !errors.Is(err, contracts.ErrForbidden) || got != nil {
		t.Errorf("without orders:access = %+v, %v; want ErrForbidden", got, err)
	}
	_, readerID := h.SignInUser(t, "orders:access")
	reader := contracts.AsUser(context.Background(), readerID)
	got, err := trace.Deliveries(reader, ids)
	if err != nil || len(got) != 1 || got[0].DeliveryID != d.Id || got[0].DeliveryNumber != d.Number || got[0].OrderID != o.Id ||
		got[0].OrderNumber != o.Number || got[0].CustomerID != custBusiness || d.PostedAt == nil || !got[0].PostedAt.Equal(*d.PostedAt) {
		t.Errorf("to a reader = %+v, %v", got, err)
	}
	// A draft delivery is no record of who received anything.
	if got, err := trace.Deliveries(reader, []int64{draft.Id}); err != nil || len(got) != 0 {
		t.Errorf("a draft = %+v, %v; want none", got, err)
	}
	tooMany := make([]int64, contracts.MaxTracedDeliveries+1)
	if _, err := trace.Deliveries(reader, tooMany); err == nil || errors.Is(err, contracts.ErrForbidden) {
		t.Errorf("more than %d ids = %v, want a failure", contracts.MaxTracedDeliveries, err)
	}

	// The meta's canTraceLots: Inventory keeps stock and the caller holds
	// both of its views.
	meta := func(keys ...string) gen.OrdersMeta {
		var m gen.OrdersMeta
		res := h.SignIn(t, keys...).Do(http.MethodGet, "/api/v1/orders/meta", nil)
		mustStatus(t, res, http.StatusOK, "the meta")
		res.JSON(&m)
		return m
	}
	if meta("orders:access", "inventory:stock-view").CanTraceLots {
		t.Error("canTraceLots without locations-view")
	}
	if !meta("orders:access", "inventory:stock-view", "inventory:locations-view").CanTraceLots {
		t.Error("canTraceLots false for a holder of both views")
	}
	bare := newHarness(t, harnessOptions{})
	var m gen.OrdersMeta
	bare.SignIn(t, "orders:access", "inventory:stock-view", "inventory:locations-view").Do(http.MethodGet, "/api/v1/orders/meta", nil).JSON(&m)
	if m.CanTraceLots {
		t.Error("canTraceLots without Inventory")
	}
}
