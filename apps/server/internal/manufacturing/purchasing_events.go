package manufacturing

// Purchasing's events (purchasing design A4): a purchase order sent, a
// receipt posted and a receipt undone each change what is incoming at the
// order's site, so the handler queues a replenishment run for that site —
// one, when none is pending that covers it — and the suggestions are made
// again from the new figures. It keeps rule 23: it writes only this module's
// schema on the delivery's transaction, reads no contract, and does nothing
// with the module off. The run itself reads PurchaseSupply on the pool.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// purchasingHandler queues a site's replenishment run on Purchasing's events.
const purchasingHandler = "manufacturing.purchasing-replenish"

// purchasingEvents are the events the handler hears, each carrying the
// order's site as siteId.
var purchasingEvents = []events.EventRef{
	{Name: contracts.PurchaseOrderSent{}.EventName(), Version: 1},
	{Name: contracts.PurchaseReceiptPosted{}.EventName(), Version: 1},
	{Name: contracts.PurchaseReceiptUndone{}.EventName(), Version: 1},
}

// queueSiteRun reads the event's site and queues its run.
func queueSiteRun(ctx context.Context, tx pgx.Tx, ev events.Delivered, d module.Deps, clock func() time.Time) error {
	if !enabled(d) {
		return nil
	}
	var e struct {
		SiteID int64 `json:"siteId"`
	}
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return fmt.Errorf("manufacturing: decode event %d: %w", ev.ID, err)
	}
	if e.SiteID <= 0 {
		return nil
	}
	q := store.New(tx)
	if err := q.LockRunRequests(ctx, runRequestsKey); err != nil {
		return err
	}
	if _, err := q.QueueSiteRun(ctx, store.QueueSiteRunParams{SiteID: e.SiteID, Now: clock()}); err != nil {
		return fmt.Errorf("manufacturing: queue site %d's replenishment run: %w", e.SiteID, err)
	}
	return nil
}
