package manufacturing

import (
	"context"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The live topics of the manufacturing orders (M25; live design L6, K-D).
// Events carry the order's id only: a screen that hears one refetches through
// its ordinary request, which checks the person's permission.
const (
	// liveOrders is the list's topic, keyless: order.changed with the id.
	liveOrders = "manufacturing.orders"
	// liveOrder is one order's page, keyed by its id.
	liveOrder = "manufacturing.order"

	eventOrderChanged = "order.changed"
)

// liveTopics is the module's LiveTopics, authorised by the rule the GETs
// apply: manufacturing:access. An order's key must be an id; one that names
// no order is still answered alike, so a subscription is no existence
// oracle.
func liveTopics(module.Deps) []live.Topic {
	return []live.Topic{
		{Kind: liveOrders, Authorize: func(ctx context.Context, key string) error {
			if key != "" || !live.HasPermission(ctx, permAccess) {
				return live.ErrForbidden
			}
			return nil
		}},
		{Kind: liveOrder, Authorize: func(ctx context.Context, key string) error {
			if id, err := strconv.ParseInt(key, 10, 64); err != nil || id < 1 || strconv.FormatInt(id, 10) != key ||
				!live.HasPermission(ctx, permAccess) {
				return live.ErrForbidden
			}
			return nil
		}},
	}
}

// notifyOrder tells the list and the order's page that order id changed,
// once per order on q, the transaction that wrote: heard only if it commits.
func notifyOrder(ctx context.Context, q live.Execer, id int64) error {
	key := strconv.FormatInt(id, 10)
	if err := live.Notify(ctx, q, liveOrders, eventOrderChanged, key); err != nil {
		return err
	}
	return live.Notify(ctx, q, liveOrder+":"+key, eventOrderChanged, key)
}
