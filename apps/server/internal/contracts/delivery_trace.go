package contracts

import (
	"context"
	"time"
)

// MaxTracedDeliveries is the most deliveries one DeliveryTrace.Deliveries
// read names (manufacturing phase 2 M90).
const MaxTracedDeliveries = 1000

// DeliveryTrace is Orders' read of its posted deliveries for Inventory's
// trace (manufacturing phase 2 M90): what a recall needs to know of who
// received a lot. It is a single-provider slot, nil when Orders is off.
//
// It is provider-gated (module-boundaries rule 12): Orders checks
// orders:access against the principal in ctx and refuses with ErrForbidden a
// caller without it and a context with no principal at all. The refusal is
// explicit, not an empty answer, so the consumer never mistakes "no
// deliveries" for "not allowed to see them". Inventory, its consumer, calls
// it with the request's context, on the pool and never inside a
// transaction, and shows the delivery as its movement alone on ErrForbidden.
// The rules a provider keeps:
//
//   - It reads on the pool, in one snapshot per call.
//   - It answers the posted deliveries among deliveryIDs, by id; an id that
//     is not a posted delivery is absent, never an error.
//   - It carries no personal data: the customer's id, never their name.
//     Names come from CustomerDirectory, which the consumer gates on
//     customers:view itself.
//   - It refuses more than MaxTracedDeliveries ids as a failure.
type DeliveryTrace interface {
	// Deliveries is the posted deliveries named by deliveryIDs.
	Deliveries(ctx context.Context, deliveryIDs []int64) ([]TracedDelivery, error)
}

// TracedDelivery is one posted delivery as DeliveryTrace answers it: its
// number and its order's, the order's customer and when it was posted.
type TracedDelivery struct {
	DeliveryID     int64
	OrderID        int64
	DeliveryNumber int64
	OrderNumber    int64
	CustomerID     int32
	PostedAt       time.Time
}
