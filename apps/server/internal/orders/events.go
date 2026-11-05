package orders

// Orders' own handler of the domain-events outbox (orders design O2, O25; MB
// rule 23): orders.queue_confirmation receives
// orders.sales_order.confirmed.v1 and, when the settings send the
// confirmation on confirm, queues its mailing — nothing else. It runs on the
// delivery's own transaction, writes only orders.mailings, reads only
// Orders' own rows and no contract, and never leaves the process: the
// orders.mail worker sends the row. The mailing is unique per (order,
// version, triggering event), so a delivery retried after its commit was lost
// queues nothing twice, and the handler runs exactly once.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// queueConfirmationHandler is the handler's name.
const queueConfirmationHandler = "orders.queue_confirmation"

// triggerNamespace makes an outbox event's id the uuid a mailing's trigger
// column takes, the same uuid for the same event every time.
var triggerNamespace = uuid.MustParse("6f1d0a52-9b3e-4f0c-a1d4-0e6a7c1b5f21")

// triggerOf is the mailing trigger of an outbox event.
func triggerOf(eventID int64) uuid.UUID {
	return uuid.NewSHA1(triggerNamespace, []byte(queueConfirmationHandler+":"+strconv.FormatInt(eventID, 10)))
}

// eventHandlers are the module's handlers (module.Module.EventHandlers),
// built from what a disabled module's Deps has too.
func eventHandlers(d module.Deps) []events.Handler {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return []events.Handler{{
		Name:   queueConfirmationHandler,
		Events: []events.EventRef{{Name: contracts.SalesOrderConfirmed{}.EventName(), Version: 1}},
		Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
			return queueConfirmation(ctx, tx, ev, d.Clock, logger)
		},
	}}
}

// queueConfirmation queues the confirmation's mailing of a confirmed order at
// its version, to the order's contact's e-mail, else the customer's invoice
// e-mail, when the settings say so. An order with neither, one gone, and one
// no longer confirmed — cancelled before the delivery ran — is skipped.
func queueConfirmation(ctx context.Context, tx pgx.Tx, ev events.Delivered, clock func() time.Time, logger *slog.Logger) error {
	var e contracts.SalesOrderConfirmed
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return fmt.Errorf("orders: decode event %d: %w", ev.ID, err)
	}
	q := store.New(tx)
	settings, err := readSettings(ctx, q)
	if err != nil {
		return err
	}
	if !settings.confirmationOnConfirm {
		return nil
	}
	o, err := q.OrderByID(ctx, e.OrderID)
	if noRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("orders: read order %d: %w", e.OrderID, err)
	}
	// An order cancelled (or otherwise no longer confirmed) before the
	// delivery ran gets no confirmation; one already past the event's version
	// gets it, and the worker sends the version it is at then.
	if o.Status != orderConfirmed || o.Version < e.Version {
		logger.InfoContext(ctx, "orders: a confirmation was not queued: the order is no longer confirmed at the event's version",
			"order_id", o.ID, "status", o.Status, "version", o.Version, "event_version", e.Version)
		return nil
	}
	recipient := defaultRecipient(headerOfOrder(o))
	if recipient == nil {
		logger.InfoContext(ctx, "orders: a confirmation was not queued: the order has no contact e-mail and the customer no invoice e-mail",
			"order_id", o.ID)
		return nil
	}
	_, err = queueMailing(ctx, q, ownerOrder, o.ID, e.Version, nil, *recipient, triggerOf(ev.ID), clock())
	return err
}
