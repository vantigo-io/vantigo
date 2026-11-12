package inventory

import (
	"context"
	"slices"
	"strconv"

	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Inventory's live topics (manufacturing phase 2 §17; live design L6, K-D):
// its first two, the lots list and a lot's page. Events carry the lot's id
// only: a screen that hears one refetches through its ordinary request, which
// checks the person's permission.
const (
	// liveLots is the lots list's topic, keyless: lot.changed, once per
	// transaction, with no id.
	liveLots = "inventory.lots"
	// liveLot is one lot's page, keyed by its id: its holds, files and stock.
	liveLot = "inventory.lot"

	eventLiveLotChanged = "lot.changed"
)

// liveTopics is the module's LiveTopics, authorised by the rule the lot
// reads apply: inventory:locations-view and inventory:stock-view. A lot's key
// must be an id; one that names no lot is answered alike, so a subscription
// is no existence oracle.
func liveTopics(module.Deps) []live.Topic {
	return []live.Topic{
		{Kind: liveLots, Authorize: func(ctx context.Context, key string) error {
			if key != "" || !live.HasPermission(ctx, permLocationsView, permStockView) {
				return live.ErrForbidden
			}
			return nil
		}},
		{Kind: liveLot, Authorize: func(ctx context.Context, key string) error {
			if id, err := strconv.ParseInt(key, 10, 64); err != nil || id < 1 || strconv.FormatInt(id, 10) != key ||
				!live.HasPermission(ctx, permLocationsView, permStockView) {
				return live.ErrForbidden
			}
			return nil
		}},
	}
}

// notifyLots tells the lots list once and each lot's page that the lots
// changed, in one statement on q — the transaction that wrote — so a listener
// hears it only if it commits (the K-D rules), and a posting of 500 serials
// is one round trip, not a thousand. The list's event carries no id: it
// refetches whatever changed. Postings call it for the lots their lines
// touch, whichever module's transaction they run on: a notification is a
// statement on that transaction, not a call out.
func notifyLots(ctx context.Context, q live.Execer, ids []int64) error {
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	events := make([]live.Event, 0, len(ids)+1)
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if len(events) == 0 {
			events = append(events, live.Event{Topic: liveLots, Kind: eventLiveLotChanged})
		}
		key := strconv.FormatInt(id, 10)
		events = append(events, live.Event{Topic: liveLot + ":" + key, Kind: eventLiveLotChanged, ID: key})
	}
	return live.NotifyAll(ctx, q, events)
}
