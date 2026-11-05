package orders

// The orders.shipment_tracking worker (orders design O50): every 30 minutes
// it polls the carriers for the shipments booked or in transit in the last
// 60 days — an exception is a flag beside those — stores the events it has
// not seen in orders.shipment_events, moves the status, sets or clears the
// exception flag, and publishes orders.shipment.delivered.v1 on delivery.
// Bring is polled: its Event Cast webhooks are unsigned and retry little.
//
// A cycle claims a batch at a time (ClaimTrackableShipments: a SKIP LOCKED
// pick that moves last_tracked_at to now — the lease — so two workers never
// poll one shipment, and one that dies leaves it due at the next interval),
// asks each account's carrier once for its shipments in the batch, outside
// any transaction, and records each shipment on a short transaction of its
// own. Every statement takes a pooled connection and returns it before the
// next is taken, and no carrier call is made while one is held: the worker is
// safe on a pool of one.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
	"github.com/vantigo-io/vantigo/server/internal/shipping"
)

const (
	trackingWorkerName = "orders.shipment_tracking"
	// trackingInterval is the poll cadence, and how long a polled shipment
	// waits before it is polled again.
	trackingInterval = 30 * time.Minute
	// trackingWindow is how far back a booking is still polled.
	trackingWindow = 60 * 24 * time.Hour
	// trackingBatch bounds one claim; trackingBatches one cycle.
	trackingBatch   = 50
	trackingBatches = 20
)

// trackingWorker polls the carriers for booked shipments.
type trackingWorker struct{ s *server }

func (w *trackingWorker) Name() string            { return trackingWorkerName }
func (w *trackingWorker) Interval() time.Duration { return trackingInterval }

func (w *trackingWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(trackingInterval)
	defer ticker.Stop()
	for {
		if _, err := w.cycle(ctx); err != nil && ctx.Err() == nil {
			w.s.deps.Logger.ErrorContext(ctx, "orders: the shipment tracking failed", "worker", trackingWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// cycle polls what is due, a batch at a time, and answers how many
// shipments it polled.
func (w *trackingWorker) cycle(ctx context.Context) (int, error) {
	s := w.s
	polled := 0
	for range trackingBatches {
		if ctx.Err() != nil {
			return polled, nil
		}
		now := s.deps.Clock()
		claimed, err := store.New(s.deps.Pool).ClaimTrackableShipments(ctx, store.ClaimTrackableShipmentsParams{
			Now: &now, Since: ptr(now.Add(-trackingWindow)), Due: ptr(now.Add(-trackingInterval + time.Minute)), BatchSize: trackingBatch,
		})
		if err != nil {
			return polled, fmt.Errorf("orders: claim the shipments to track: %w", err)
		}
		if len(claimed) == 0 {
			return polled, nil
		}
		byAccount := map[int64][]store.OrdersShipment{}
		var order []int64
		for _, sh := range claimed {
			if _, ok := byAccount[sh.CarrierAccountID]; !ok {
				order = append(order, sh.CarrierAccountID)
			}
			byAccount[sh.CarrierAccountID] = append(byAccount[sh.CarrierAccountID], sh)
		}
		for _, id := range order {
			w.trackAccount(ctx, id, byAccount[id])
		}
		polled += len(claimed)
		if len(claimed) < trackingBatch {
			return polled, nil
		}
	}
	return polled, nil
}

// trackAccount asks one account's carrier for its shipments' tracking and
// records each one; a carrier that cannot be called or does not answer is
// logged, and its shipments are polled again at the next interval.
func (w *trackingWorker) trackAccount(ctx context.Context, accountID int64, shipments []store.OrdersShipment) {
	s := w.s
	account, err := store.New(s.deps.Pool).GetCarrierAccount(ctx, accountID)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: a carrier account to track could not be read", "account_id", accountID, "error", err.Error())
		return
	}
	c, err := s.carrierOf(account)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: a carrier account cannot be tracked", "account_id", accountID, "error", err.Error())
		return
	}
	refs := make([]string, 0, len(shipments))
	for _, sh := range shipments {
		refs = append(refs, deref(sh.ConsignmentNumber))
	}
	statuses, err := c.Track(ctx, refs)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: the carrier did not answer the tracking", "account_id", accountID, "error", err.Error())
	}
	byConsignment := map[string]shipping.TrackingStatus{}
	for _, st := range statuses {
		byConsignment[st.ConsignmentNumber] = st
		byConsignment[st.Reference] = st
	}
	for _, sh := range shipments {
		st, ok := byConsignment[deref(sh.ConsignmentNumber)]
		if !ok {
			continue
		}
		if err := w.record(ctx, sh, st); err != nil {
			s.deps.Logger.WarnContext(ctx, "orders: a shipment's tracking could not be recorded", "shipment_id", sh.ID, "error", err.Error())
		}
	}
}

// folded is the shipment status and exception flag a tracking status moves
// a booked or in-transit shipment to. Ready for pickup is in transit; a
// return or a deviation is an exception beside the status it had.
func folded(current string, st shipping.State) (string, bool) {
	switch st {
	case shipping.StateDelivered:
		return shipmentDelivered, false
	case shipping.StateInTransit, shipping.StateReadyForPickup:
		return shipmentInTransit, false
	case shipping.StateException, shipping.StateReturned:
		return current, true
	}
	return current, false
}

// record stores a shipment's new events and moves it, on one short
// transaction, publishing orders.shipment.delivered.v1 when it is delivered.
func (w *trackingWorker) record(ctx context.Context, sh store.OrdersShipment, st shipping.TrackingStatus) error {
	s := w.s
	return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		cur, err := txq.LockShipment(ctx, sh.ID)
		if err != nil {
			return fmt.Errorf("orders: lock shipment %d: %w", sh.ID, err)
		}
		if cur.Status != shipmentBooked && cur.Status != shipmentInTransit {
			return nil
		}
		for _, e := range st.Events {
			if _, err := txq.InsertShipmentEvent(ctx, store.InsertShipmentEventParams{
				ShipmentID: cur.ID, Code: truncate(e.Code, 64), Text: truncate(e.Text, 500), Place: truncate(e.Place, 200),
				OccurredAt: e.At, FoldedState: string(e.State),
			}); err != nil {
				return fmt.Errorf("orders: record shipment %d's event: %w", cur.ID, err)
			}
		}
		status, exception := folded(cur.Status, st.State)
		if status == cur.Status && exception == cur.Exception {
			return nil
		}
		now := s.deps.Clock()
		moved, err := txq.SetShipmentTracked(ctx, store.SetShipmentTrackedParams{ID: cur.ID, Status: status, Exception: exception, Now: now})
		if err != nil {
			return fmt.Errorf("orders: move shipment %d: %w", cur.ID, err)
		}
		if moved.Status != shipmentDelivered || cur.Status == shipmentDelivered {
			return nil
		}
		d, err := txq.GetDelivery(ctx, cur.DeliveryID)
		if err != nil {
			return fmt.Errorf("orders: read delivery %d: %w", cur.DeliveryID, err)
		}
		at := now
		if n := len(st.Events); n > 0 {
			at = st.Events[n-1].At
		}
		return events.Publish(ctx, tx, moduleName, contracts.ShipmentDelivered{
			ShipmentID: cur.ID, DeliveryID: d.ID, OrderID: d.OrderID, ConsignmentNumber: deref(cur.ConsignmentNumber), DeliveredAt: at,
		}, now)
	})
}
