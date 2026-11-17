package purchasing

// The module's events (purchasing design §7; module-boundaries rule 23):
// purchasing.order.sent.v1 from Send and Mark as sent (ordersend.go) and
// purchasing.order.closed.v1 from Close and a last Close line, each published
// on the change's own transaction while it holds the order's row lock, so the
// event commits with the change or not at all. A payload carries ids,
// numbers, dates, the currency and the net total — no name, address or note
// (contracts/events_purchasing.go). None has a consumer in this phase.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// publishClosed publishes purchasing.order.closed.v1 on tx, under the
// order's lock.
func publishClosed(ctx context.Context, tx pgx.Tx, o store.PurchasingOrder, at time.Time) error {
	ev := contracts.PurchaseOrderClosed{OrderID: o.ID, SupplierID: o.SupplierID, SiteID: o.SiteID, ClosedAt: at}
	if o.Number != nil {
		ev.Number = *o.Number
	}
	if err := events.Publish(ctx, tx, moduleName, ev, at); err != nil {
		return fmt.Errorf("purchasing: publish the close of order %d: %w", o.ID, err)
	}
	return nil
}
