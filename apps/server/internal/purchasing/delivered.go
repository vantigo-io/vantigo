package purchasing

// The delivered mark (P16, §8): a text line or a service line is never
// received into stock; a holder of purchasing:receive marks it delivered,
// or takes the mark off again. The order's state follows from its lines as
// after a receipt.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// MarkPurchasingOrderLineDelivered Mark an order line delivered
// (POST /api/v1/purchasing/orders/{orderId}/lines/{lineId}/delivered)
func (s *server) MarkPurchasingOrderLineDelivered(ctx context.Context, req gen.MarkPurchasingOrderLineDeliveredRequestObject) (gen.MarkPurchasingOrderLineDeliveredResponseObject, error) {
	resp := answers[gen.MarkPurchasingOrderLineDeliveredResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.MarkPurchasingOrderLineDeliveredResponseObject {
			return gen.MarkPurchasingOrderLineDelivered400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.MarkPurchasingOrderLineDeliveredResponseObject {
			return gen.MarkPurchasingOrderLineDelivered404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.MarkPurchasingOrderLineDeliveredResponseObject {
			return gen.MarkPurchasingOrderLineDelivered409ApplicationProblemPlusJSONResponse(p)
		},
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	b := *req.Body
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		o, err := lockedOrder(ctx, txq, req.OrderId, b.ExpectedUpdatedAt)
		if err != nil {
			return err
		}
		// Taking the mark off a received order opens it again; anything
		// else needs it open.
		if !isOpen(o.Status) && (b.Delivered || o.Status != statusReceived) {
			return judgeSent(o)
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
		if target.Kind == kindVariant && target.ProductType != nil && *target.ProductType == typeGoods {
			return conflict(codeLineNotDeliverable, "A goods line is received",
				fmt.Sprintf("Line %d is goods: it is received into stock, not marked delivered.", target.ID))
		}
		if target.ClosedAt != nil {
			return conflict(codeLineClosed, "The line is closed", fmt.Sprintf("Line %d is closed.", target.ID))
		}
		now := s.deps.Clock()
		action := "line_undelivered"
		if b.Delivered {
			action = "line_delivered"
		}
		if (target.DeliveredAt != nil) == b.Delivered {
			return nil // as asked already
		}
		deliveredAt := &now
		if !b.Delivered {
			deliveredAt = nil
		}
		if err := txq.SetLineDelivered(ctx, store.SetLineDeliveredParams{ID: target.ID, DeliveredAt: deliveredAt}); err != nil {
			return fmt.Errorf("purchasing: mark line %d delivered: %w", target.ID, err)
		}
		if _, err := setDerivedStatus(ctx, txq, o, now); err != nil {
			return err
		}
		return logChange(ctx, txq, o.ID, by, now, change{Action: action, LineID: &target.ID})
	})
	if err != nil {
		return answer(err, resp)
	}
	view, err := s.getOrderView(ctx, req.OrderId)
	if err != nil {
		return nil, err
	}
	return gen.MarkPurchasingOrderLineDelivered200JSONResponse(view), nil
}
