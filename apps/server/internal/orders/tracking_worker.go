package orders

// The orders.shipment_tracking worker (orders design O50): every 30 minutes
// it polls the carriers for the shipments booked or in transit in the last
// 60 days — an exception is a flag beside those — stores the events it has
// not seen in orders.shipment_events, moves the status, sets or clears the
// exception flag, and publishes orders.shipment.delivered.v1 on delivery.
// Bring is polled: its Event Cast webhooks are unsigned and retry little.
//
// A cycle claims a batch at a time (ClaimTrackableShipments: a SKIP LOCKED
// pick, the least recently tracked first, that moves last_tracked_at to now —
// the lease — so two workers never poll one shipment, and one that dies
// leaves it due at the next interval), asks each account's carrier once for
// its shipments in the batch, outside any transaction, and records each
// shipment on a short transaction of its own. The carrier calls of a cycle
// run under trackingBudget, well inside the lease, and each reference under a
// deadline of the adapter's own; a shipment the carrier was never asked about
// when the budget ran out gets its last_tracked_at back, so it is first in
// the next cycle and a stuck tail rotates. Every statement takes a pooled connection and returns it before the
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
	// trackingBudget bounds a cycle's carrier calls: well inside the lease
	// (trackingInterval less a minute), so no claimed shipment comes due
	// again while it is still being asked about.
	trackingBudget = 10 * time.Minute
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
	return w.cycleWithin(ctx, trackingBudget)
}

// cycleWithin is a cycle whose carrier calls end after budget.
func (w *trackingWorker) cycleWithin(ctx context.Context, budget time.Duration) (int, error) {
	s := w.s
	calls, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	polled := 0
	for range trackingBatches {
		if calls.Err() != nil {
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
		byAccount := map[int64][]store.ClaimTrackableShipmentsRow{}
		var order []int64
		for _, sh := range claimed {
			if _, ok := byAccount[sh.CarrierAccountID]; !ok {
				order = append(order, sh.CarrierAccountID)
			}
			byAccount[sh.CarrierAccountID] = append(byAccount[sh.CarrierAccountID], sh)
		}
		for _, id := range order {
			w.trackAccount(ctx, calls, id, byAccount[id], now)
		}
		polled += len(claimed)
		if len(claimed) < trackingBatch {
			return polled, nil
		}
	}
	return polled, nil
}

// trackAccount asks one account's carrier, under calls, for its shipments'
// tracking and records each one on ctx; a carrier that cannot be called or
// does not answer is logged, and its shipments are polled again at the next
// interval — but one calls ended before it was asked about gets the
// last_tracked_at it had back (claimed is what the claim moved it to).
func (w *trackingWorker) trackAccount(ctx, calls context.Context, accountID int64, shipments []store.ClaimTrackableShipmentsRow,
	claimed time.Time) {
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
	statuses, err := c.Track(calls, refs)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "orders: the carrier did not answer the tracking", "account_id", accountID, "error", err.Error())
	}
	unasked := err != nil && calls.Err() != nil
	byConsignment := map[string]shipping.TrackingStatus{}
	for _, st := range statuses {
		byConsignment[st.ConsignmentNumber] = st
		byConsignment[st.Reference] = st
	}
	for _, sh := range shipments {
		st, ok := byConsignment[deref(sh.ConsignmentNumber)]
		if !ok {
			if unasked {
				w.putBack(ctx, sh, claimed)
			}
			continue
		}
		if err := w.record(ctx, sh, st); err != nil {
			s.deps.Logger.WarnContext(ctx, "orders: a shipment's tracking could not be recorded", "shipment_id", sh.ID, "error", err.Error())
		}
	}
}

// putBack gives a claimed shipment the carrier was never asked about its
// last_tracked_at back, so it is due — and first — again at once. It runs
// even when ctx has ended, briefly.
func (w *trackingWorker) putBack(ctx context.Context, sh store.ClaimTrackableShipmentsRow, claimed time.Time) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.New(w.s.deps.Pool).RestoreShipmentTracked(ctx, store.RestoreShipmentTrackedParams{
		ID: sh.ID, Previous: sh.PreviousTrackedAt, Claimed: &claimed,
	}); err != nil {
		w.s.deps.Logger.WarnContext(ctx, "orders: a shipment's tracking lease could not be put back", "shipment_id", sh.ID, "error", err.Error())
	}
}

// folded is the shipment status and exception flag a tracking status moves
// a booked or in-transit shipment to. Ready for pickup is in transit; a
// return or a deviation is an exception beside the status it had, and so is a
// cancel the carrier reports: the shipment is not cancelled here on the
// carrier's word — a person looks, and cancels it in Vantigo if it is so.
func folded(current string, st shipping.State) (string, bool) {
	switch st {
	case shipping.StateDelivered:
		return shipmentDelivered, false
	case shipping.StateInTransit, shipping.StateReadyForPickup:
		return shipmentInTransit, false
	case shipping.StateException, shipping.StateReturned, shipping.StateCancelled:
		return current, true
	}
	return current, false
}

// record stores a shipment's new events and moves it, on one short
// transaction, publishing orders.shipment.delivered.v1 when it is delivered.
func (w *trackingWorker) record(ctx context.Context, sh store.ClaimTrackableShipmentsRow, st shipping.TrackingStatus) error {
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
