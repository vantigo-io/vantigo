package purchasing

// An order's ends (P18, A16): Close ends a sent order with its remainder —
// every open line closed, purchasing.order.closed.v1 published —, Reopen
// undoes it, Close line ends one line's remainder and closes the order (or
// makes it received) when nothing is left open, and Cancel ends a sent order
// with nothing received, mailing the supplier its cancellation when the
// order was mailed.

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// lockedOrder locks an order and judges it fresh; unknown is 404.
func lockedOrder(ctx context.Context, txq *store.Queries, id int64, expected time.Time) (store.PurchasingOrder, error) {
	o, err := txq.LockOrder(ctx, id)
	if noRows(err) {
		return o, notFound("purchase order")
	}
	if err != nil {
		return o, fmt.Errorf("purchasing: lock order %d: %w", id, err)
	}
	return o, judgeFresh(o.UpdatedAt, expected)
}

// ClosePurchasingOrder Close a purchase order
// (POST /api/v1/purchasing/orders/{orderId}/close)
func (s *server) ClosePurchasingOrder(ctx context.Context, req gen.ClosePurchasingOrderRequestObject) (gen.ClosePurchasingOrderResponseObject, error) {
	resp := answers[gen.ClosePurchasingOrderResponseObject]{
		notFound: func(p gen.PurchasingProblem) gen.ClosePurchasingOrderResponseObject {
			return gen.ClosePurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.ClosePurchasingOrderResponseObject {
			return gen.ClosePurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		o, err := lockedOrder(ctx, txq, req.OrderId, req.Body.ExpectedUpdatedAt)
		if err != nil {
			return err
		}
		if err := judgeSent(o); err != nil {
			return err
		}
		now := s.deps.Clock()
		if _, err := txq.CloseOpenLines(ctx, store.CloseOpenLinesParams{OrderID: o.ID, ClosedAt: &now}); err != nil {
			return fmt.Errorf("purchasing: close order %d's lines: %w", o.ID, err)
		}
		if _, err := txq.SetOrderState(ctx, store.SetOrderStateParams{ID: o.ID, Status: statusClosed, ClosedAt: &now, Now: now}); err != nil {
			return fmt.Errorf("purchasing: close order %d: %w", o.ID, err)
		}
		if err := logChange(ctx, txq, o.ID, by, now, change{Action: "closed"}); err != nil {
			return err
		}
		return publishClosed(ctx, tx, o, now)
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.getOrderView(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	return gen.ClosePurchasingOrder200JSONResponse(view), nil
}

// ReopenPurchasingOrder Reopen a purchase order
// (POST /api/v1/purchasing/orders/{orderId}/reopen)
func (s *server) ReopenPurchasingOrder(ctx context.Context, req gen.ReopenPurchasingOrderRequestObject) (gen.ReopenPurchasingOrderResponseObject, error) {
	resp := answers[gen.ReopenPurchasingOrderResponseObject]{
		notFound: func(p gen.PurchasingProblem) gen.ReopenPurchasingOrderResponseObject {
			return gen.ReopenPurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.ReopenPurchasingOrderResponseObject {
			return gen.ReopenPurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		o, err := lockedOrder(ctx, txq, req.OrderId, req.Body.ExpectedUpdatedAt)
		if err != nil {
			return err
		}
		if o.Status != statusClosed || o.ClosedAt == nil {
			return conflict(codeOrderNotClosed, "The order is not closed", fmt.Sprintf("The order is %s; only a closed order is reopened.", o.Status))
		}
		if _, err := txq.ReopenLines(ctx, store.ReopenLinesParams{OrderID: o.ID, ClosedAt: *o.ClosedAt}); err != nil {
			return fmt.Errorf("purchasing: reopen order %d's lines: %w", o.ID, err)
		}
		lines, err := txq.OrderLines(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
		}
		status := statusSent
		for _, l := range lines {
			if ratOrZero(l.Received).Sign() > 0 {
				status = statusPartial
				break
			}
		}
		now := s.deps.Clock()
		if _, err := txq.SetOrderState(ctx, store.SetOrderStateParams{ID: o.ID, Status: status, Now: now}); err != nil {
			return fmt.Errorf("purchasing: reopen order %d: %w", o.ID, err)
		}
		return logChange(ctx, txq, o.ID, by, now, change{Action: "reopened"})
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.getOrderView(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	return gen.ReopenPurchasingOrder200JSONResponse(view), nil
}

// ClosePurchasingOrderLine Close an order line
// (POST /api/v1/purchasing/orders/{orderId}/lines/{lineId}/close)
//
// When no line stays open the order ends: received when every line was
// received in full or marked delivered, else closed with what was left.
func (s *server) ClosePurchasingOrderLine(ctx context.Context, req gen.ClosePurchasingOrderLineRequestObject) (gen.ClosePurchasingOrderLineResponseObject, error) {
	resp := answers[gen.ClosePurchasingOrderLineResponseObject]{
		notFound: func(p gen.PurchasingProblem) gen.ClosePurchasingOrderLineResponseObject {
			return gen.ClosePurchasingOrderLine404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.ClosePurchasingOrderLineResponseObject {
			return gen.ClosePurchasingOrderLine409ApplicationProblemPlusJSONResponse(p)
		},
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		o, err := lockedOrder(ctx, txq, req.OrderId, req.Body.ExpectedUpdatedAt)
		if err != nil {
			return err
		}
		if err := judgeSent(o); err != nil {
			return err
		}
		lines, err := txq.OrderLines(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
		}
		var target *store.PurchasingOrderLine
		for i := range lines {
			if lines[i].ID == req.LineId {
				target = &lines[i]
			}
		}
		if target == nil {
			return notFound("line of the order")
		}
		if target.ClosedAt != nil {
			return conflict(codeLineClosed, "The line is closed", fmt.Sprintf("Line %d is closed already.", target.ID))
		}
		now := s.deps.Clock()
		if err := txq.CloseLine(ctx, store.CloseLineParams{ID: target.ID, ClosedAt: &now}); err != nil {
			return fmt.Errorf("purchasing: close line %d: %w", target.ID, err)
		}
		target.ClosedAt = &now
		if err := logChange(ctx, txq, o.ID, by, now, change{Action: "line_closed", LineID: &target.ID}); err != nil {
			return err
		}
		open, leftOver := false, false
		for _, l := range lines {
			remainder := ratOrZero(l.Quantity).Cmp(ratOrZero(l.Received)) > 0 && l.DeliveredAt == nil
			if l.ClosedAt == nil && remainder {
				open = true
			}
			if l.ClosedAt != nil && remainder {
				leftOver = true
			}
		}
		if open {
			return txq.TouchOrder(ctx, store.TouchOrderParams{ID: o.ID, Now: now})
		}
		if !leftOver {
			_, err := txq.SetOrderState(ctx, store.SetOrderStateParams{ID: o.ID, Status: statusReceived, Now: now})
			return err
		}
		if _, err := txq.SetOrderState(ctx, store.SetOrderStateParams{ID: o.ID, Status: statusClosed, ClosedAt: &now, Now: now}); err != nil {
			return fmt.Errorf("purchasing: close order %d: %w", o.ID, err)
		}
		if err := logChange(ctx, txq, o.ID, by, now, change{Action: "closed"}); err != nil {
			return err
		}
		return publishClosed(ctx, tx, o, now)
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.getOrderView(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	return gen.ClosePurchasingOrderLine200JSONResponse(view), nil
}

// CancelPurchasingOrder Cancel a purchase order
// (POST /api/v1/purchasing/orders/{orderId}/cancel)
func (s *server) CancelPurchasingOrder(ctx context.Context, req gen.CancelPurchasingOrderRequestObject) (gen.CancelPurchasingOrderResponseObject, error) {
	resp := answers[gen.CancelPurchasingOrderResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.CancelPurchasingOrderResponseObject {
			return gen.CancelPurchasingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.CancelPurchasingOrderResponseObject {
			return gen.CancelPurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.CancelPurchasingOrderResponseObject {
			return gen.CancelPurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.CancelPurchasingOrderResponseObject {
			return gen.CancelPurchasingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	var f fieldErrors
	m := readMailRequest(&f, b.Recipient, nil, nil, b.Message)
	if f.failed() {
		return gen.CancelPurchasingOrder400ApplicationProblemPlusJSONResponse(invalid(invalidSendTitle, f)), nil
	}
	r, err := readOrder(ctx, s.store(), req.OrderId)
	if err != nil {
		return answer(err, resp)
	}
	o := r.order
	if err := judgeCancellable(o, r.lines); err != nil {
		return answer(err, resp)
	}
	if err := judgeFresh(o.UpdatedAt, b.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	notify := deref(o.SendMethod) == sendMail && (b.NotifySupplier == nil || *b.NotifySupplier)
	var channel uuid.UUID
	if notify {
		var ok bool
		if channel, ok, err = s.sendingChannel(ctx); err != nil {
			return nil, err
		}
		if !ok {
			return answer(sendingDisabled(), resp)
		}
		if m.recipient, err = s.recipientFor(ctx, o, m.recipient); err != nil {
			return answer(err, resp)
		}
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var seller contracts.SellerProfile
	if notify {
		if seller, err = s.seller(ctx); err != nil {
			return nil, err
		}
	}
	var queued store.PurchasingMailing
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockedOrder(ctx, txq, o.ID, b.ExpectedUpdatedAt)
		if err != nil {
			return err
		}
		lines, err := txq.OrderLines(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
		}
		if err := judgeCancellable(locked, lines); err != nil {
			return err
		}
		now := s.deps.Clock()
		cancelled, err := txq.SetOrderState(ctx, store.SetOrderStateParams{ID: o.ID, Status: statusCancelled, CancelledAt: &now, Now: now})
		if err != nil {
			return fmt.Errorf("purchasing: cancel order %d: %w", o.ID, err)
		}
		if err := logChange(ctx, txq, o.ID, by, now, change{Action: "cancelled"}); err != nil {
			return err
		}
		if notify {
			queued, err = queueMailing(ctx, txq, cancelled, docCancellation, cancelled.Version, m, channel, seller, by, now)
			return err
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	var out gen.PurchasingSendResult
	if notify {
		mailing, sent, err := s.mailQueued(ctx, queued)
		if err != nil {
			return nil, err
		}
		if !sent {
			return answer(mailFailed(), resp)
		}
		out.Mailing = ptr(mailingOut(mailing))
	}
	if out.Order, err = s.getOrderView(ctx, o.ID); err != nil {
		return nil, err
	}
	return gen.CancelPurchasingOrder200JSONResponse(out), nil
}

// judgeCancellable refuses a cancel of an order that is not sent (a draft is
// deleted instead) or that has anything received (A16).
func judgeCancellable(o store.PurchasingOrder, lines []store.PurchasingOrderLine) error {
	if !isOpen(o.Status) {
		return conflict(codeOrderNotSent, "The order is not sent",
			fmt.Sprintf("The order is %s; a draft is deleted, and only a sent order is cancelled.", o.Status))
	}
	for _, l := range lines {
		if ratOrZero(l.Received).Sign() > 0 || l.DeliveredAt != nil {
			return conflict(codeOrderHasReceipts, "Something is received",
				"Part of the order is received; close it instead of cancelling it.")
		}
	}
	return nil
}
