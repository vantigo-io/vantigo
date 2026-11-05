package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// timelineOrdersHandler is customers.timeline_orders (orders design O31):
// the four orders events, each written as one generated entry of the
// customer's timeline. It runs on the delivery's own transaction and writes
// only this module's schema, so an entry is written exactly once; the
// payloads carry ids, numbers and amounts only (O3), and so do the entries.
const timelineOrdersHandler = "customers.timeline_orders"

// producerOrders is the producer of an entry written from an orders event.
const producerOrders = "customers.orders"

// maxMergeHops bounds the merge pointer chain followed to a surviving
// customer; a merge never chains deeper in practice.
const maxMergeHops = 5

func eventHandlers(d module.Deps) []events.Handler {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return []events.Handler{{
		Name: timelineOrdersHandler,
		Events: []events.EventRef{
			{Name: contracts.QuoteSent{}.EventName(), Version: 1},
			{Name: contracts.QuoteAccepted{}.EventName(), Version: 1},
			{Name: contracts.SalesOrderConfirmed{}.EventName(), Version: 1},
			{Name: contracts.SalesOrderCancelled{}.EventName(), Version: 1},
		},
		Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
			return handleTimelineOrders(ctx, tx, ev, logger)
		},
	}}
}

// ordersEntry is what one event becomes on the timeline.
type ordersEntry struct {
	customerID int32
	at         time.Time
	eventType  string
	summary    string
	payload    map[string]any
}

func decodeOrdersEvent(ev events.Delivered) (ordersEntry, error) {
	switch ev.Name {
	case "orders.quote.sent":
		var e contracts.QuoteSent
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return ordersEntry{}, err
		}
		return ordersEntry{e.CustomerID, e.SentAt, "orders.quote_sent", fmt.Sprintf("Quote sent: %d revision %d", e.Number, e.Revision),
			map[string]any{"documentKind": "quote", "documentId": e.QuoteID, "number": e.Number, "revision": e.Revision,
				"currency": e.Currency, "netTotal": e.NetTotal, "link": fmt.Sprintf("/orders/quotes/%d", e.QuoteID)}}, nil
	case "orders.quote.accepted":
		var e contracts.QuoteAccepted
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return ordersEntry{}, err
		}
		return ordersEntry{e.CustomerID, e.AcceptedAt, "orders.quote_accepted", fmt.Sprintf("Quote accepted: %d revision %d", e.Number, e.Revision),
			map[string]any{"documentKind": "quote", "documentId": e.QuoteID, "number": e.Number, "revision": e.Revision,
				"acceptedIntoKind": e.AcceptedIntoKind, "acceptedIntoId": e.AcceptedIntoID, "link": fmt.Sprintf("/orders/quotes/%d", e.QuoteID)}}, nil
	case "orders.sales_order.confirmed":
		var e contracts.SalesOrderConfirmed
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return ordersEntry{}, err
		}
		return ordersEntry{e.CustomerID, e.ConfirmedAt, "orders.order_confirmed", fmt.Sprintf("Order confirmed: %d", e.Number),
			map[string]any{"documentKind": "order", "documentId": e.OrderID, "number": e.Number, "version": e.Version,
				"currency": e.Currency, "netTotal": e.NetTotal, "link": fmt.Sprintf("/orders/orders/%d", e.OrderID)}}, nil
	case "orders.sales_order.cancelled":
		var e contracts.SalesOrderCancelled
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return ordersEntry{}, err
		}
		return ordersEntry{e.CustomerID, e.CancelledAt, "orders.order_cancelled", fmt.Sprintf("Order cancelled: %d", e.Number),
			map[string]any{"documentKind": "order", "documentId": e.OrderID, "number": e.Number, "link": fmt.Sprintf("/orders/orders/%d", e.OrderID)}}, nil
	}
	return ordersEntry{}, fmt.Errorf("customers: %s takes no event %q", timelineOrdersHandler, ev.Name)
}

func handleTimelineOrders(ctx context.Context, tx pgx.Tx, ev events.Delivered, logger *slog.Logger) error {
	entry, err := decodeOrdersEvent(ev)
	if err != nil {
		return fmt.Errorf("customers: decode %s: %w", ev.Name, err)
	}
	customerID, ok, err := survivingCustomer(ctx, tx, entry.customerID)
	if err != nil {
		return err
	}
	if !ok {
		logger.DebugContext(ctx, "customers: timeline entry skipped, customer absent, merged into nothing or anonymised",
			"event", ev.Name, "eventId", ev.ID, "customerId", entry.customerID)
		return nil
	}
	at := entry.at
	if at.IsZero() {
		at = ev.OccurredAt
	}
	return recordGeneratedEventFrom(ctx, store.New(tx), customerID, at, producerOrders, entry.eventType, entry.summary, entry.payload, 1, "system", "System", nil)
}

// survivingCustomer follows id's merge pointer to the customer that holds
// the timeline now. ok is false when the customer is missing, anonymised, or
// the chain does not end.
func survivingCustomer(ctx context.Context, tx pgx.Tx, id int32) (int32, bool, error) {
	for range maxMergeHops {
		var (
			mergedInto *int32
			anonymised bool
		)
		err := tx.QueryRow(ctx, `SELECT merged_into_customer_id, anonymised_at IS NOT NULL FROM customers.customers WHERE id = $1`, id).Scan(&mergedInto, &anonymised)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		if err != nil {
			return 0, false, fmt.Errorf("customers: read customer %d for the timeline: %w", id, err)
		}
		if anonymised {
			return 0, false, nil
		}
		if mergedInto == nil {
			return id, true, nil
		}
		id = *mergedInto
	}
	return 0, false, nil
}
