package manufacturing

import (
	"context"
	"maps"
	"slices"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The live topics of the manufacturing orders and the shop floor (M25, M32;
// live design L6, K-D).
// Events carry the order's id only: a screen that hears one refetches through
// its ordinary request, which checks the person's permission.
const (
	// liveOrders is the list's topic, keyless: order.changed with the id.
	liveOrders = "manufacturing.orders"
	// liveOrder is one order's page, keyed by its id.
	liveOrder = "manufacturing.order"

	eventOrderChanged = "order.changed"

	// liveWorkCenter is a work center's shop-floor page, keyed by its id:
	// its queue and its blocking (M32).
	liveWorkCenter = "manufacturing.work-center"
	// liveWorkOrder is a work order's view, keyed by its id, where the
	// operators on it announce their presence (M31, M32).
	liveWorkOrder = "manufacturing.work-order"

	eventWorkCenterChanged = "work-center.changed"
	eventWorkOrderChanged  = "work-order.changed"

	// liveSchedule is the production schedule of one site, keyed by the
	// site's id (M36): plan, plan all, unplan and reschedule tell it.
	liveSchedule         = "manufacturing.schedule"
	eventScheduleChanged = "schedule.changed"
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
		{Kind: liveOrder, Authorize: authorizeID},
		{Kind: liveWorkCenter, Authorize: authorizeID},
		{Kind: liveWorkOrder, Presence: true, Authorize: authorizeID},
		{Kind: liveSchedule, Authorize: authorizeID},
		{Kind: liveSuggestions, Authorize: authorizeID},
		{Kind: liveDeviations, Authorize: func(ctx context.Context, key string) error {
			if key != "" || !live.HasPermission(ctx, permAccess) {
				return live.ErrForbidden
			}
			return nil
		}},
	}
}

// authorizeID admits a keyed topic whose key is an id, for a holder of
// manufacturing:access.
func authorizeID(ctx context.Context, key string) error {
	if id, err := strconv.ParseInt(key, 10, 64); err != nil || id < 1 || strconv.FormatInt(id, 10) != key ||
		!live.HasPermission(ctx, permAccess) {
		return live.ErrForbidden
	}
	return nil
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

// notices is what one write tells, each order, work center and work order
// once (the K-D rules): collected while the write runs and sent on its
// transaction at the end, so a listener hears it only if it commits.
type notices struct {
	orders, workCenters, workOrders, sites map[int64]bool
}

func newNotices() *notices {
	return &notices{orders: map[int64]bool{}, workCenters: map[int64]bool{}, workOrders: map[int64]bool{}, sites: map[int64]bool{}}
}

// workOrder notes a work order that changed, with its order and its work
// center.
func (n *notices) workOrder(id, orderID, workCenterID int64) {
	n.workOrders[id] = true
	n.orders[orderID] = true
	n.workCenters[workCenterID] = true
}

// send notifies every aggregate noted, in id order, on q.
func (n *notices) send(ctx context.Context, q live.Execer) error {
	for _, id := range slices.Sorted(maps.Keys(n.orders)) {
		if err := notifyOrder(ctx, q, id); err != nil {
			return err
		}
	}
	for _, id := range slices.Sorted(maps.Keys(n.workCenters)) {
		key := strconv.FormatInt(id, 10)
		if err := live.Notify(ctx, q, liveWorkCenter+":"+key, eventWorkCenterChanged, key); err != nil {
			return err
		}
	}
	for _, id := range slices.Sorted(maps.Keys(n.workOrders)) {
		key := strconv.FormatInt(id, 10)
		if err := live.Notify(ctx, q, liveWorkOrder+":"+key, eventWorkOrderChanged, key); err != nil {
			return err
		}
	}
	for _, id := range slices.Sorted(maps.Keys(n.sites)) {
		key := strconv.FormatInt(id, 10)
		if err := live.Notify(ctx, q, liveSchedule+":"+key, eventScheduleChanged, key); err != nil {
			return err
		}
	}
	return nil
}
