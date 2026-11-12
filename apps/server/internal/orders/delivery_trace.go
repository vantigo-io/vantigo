package orders

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// newDeliveryTrace builds contracts.DeliveryTrace over d (manufacturing
// phase 2 M90).
func newDeliveryTrace(d module.Deps) contracts.DeliveryTrace { return deliveryTrace{deps: d} }

// deliveryTrace is contracts.DeliveryTrace: provider-gated on orders:access
// from ctx (module-boundaries rule 12). Unlike Manufacturing's production
// read it refuses rather than answering nothing, so Inventory tells "no
// deliveries" from "not allowed to see them".
type deliveryTrace struct{ deps module.Deps }

// Deliveries is the posted deliveries among ids, with their order's number
// and customer id. A caller without orders:access, or a context with no
// principal, is refused with contracts.ErrForbidden.
func (p deliveryTrace) Deliveries(ctx context.Context, ids []int64) ([]contracts.TracedDelivery, error) {
	if len(ids) > contracts.MaxTracedDeliveries {
		return nil, fmt.Errorf("orders: a delivery trace names %d deliveries, more than %d", len(ids), contracts.MaxTracedDeliveries)
	}
	if p.deps.Access == nil || !contracts.HasPermission(ctx, p.deps.Access, permAccess) {
		return nil, contracts.ErrForbidden
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := store.New(p.deps.Pool).DeliveriesForTrace(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read the deliveries to trace: %w", err)
	}
	out := make([]contracts.TracedDelivery, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.TracedDelivery{DeliveryID: r.ID, OrderID: r.OrderID, DeliveryNumber: r.Number,
			OrderNumber: r.OrderNumber, CustomerID: r.CustomerID, PostedAt: r.PostedAt})
	}
	return out, nil
}
