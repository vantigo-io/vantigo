package orders

// Manual delivery marking (orders design O32): until delivery C, and for
// services and non-stock lines after it, what was delivered is registered by
// hand on a confirmed order. A marking adds to a line's delivered quantity,
// never beyond what is ordered less cancelled (over_delivery); an undo takes
// it back, never below 0 (undo_exceeds_delivered) and, on a line invoiced on
// delivery, never below what is invoiced (delivered_below_invoiced). Every
// line of the request is judged at once. The derived delivery status is
// recomputed and delivered_marked or delivered_undone logged per line; the
// version is not bumped, since the confirmation prints nothing delivered. A
// marking releases what a line holds reserved beyond what is still open
// (O45); an undo reserves nothing again — Reserve now does.

import (
	"context"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// markDelivered is a marking (undo false) or its undo.
func (s *server) markDelivered(ctx context.Context, id int64, body gen.OrdersDeliveredInput, undo bool) (gen.OrdersOrder, error) {
	if f := s.unknownProperties(ctx, deliveredInputSchema); f.failed() {
		return gen.OrdersOrder{}, refuseInvalid(invalidDeliveryTitle, f)
	}
	if len(body.Lines) == 0 || len(body.Lines) > maxLines {
		return gen.OrdersOrder{}, refuseField(invalidDeliveryTitle, "lines", "", "One to 500 lines")
	}
	// The variants are locked (MB rule 21): an undo opens a line again, and
	// an open line holds its variant against a delete (O30).
	return s.changeOrder(ctx, id, body.Version, body.ExpectedUpdatedAt, true,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.OrdersOrder, lines []store.OrdersLine) (store.OrdersOrder, []changeAction, error) {
			if err := judgeConfirmed(o); err != nil {
				return o, nil, err
			}
			if o.OnHold {
				return o, nil, conflict(codeOrderOnHold, "The order is on hold",
					fmt.Sprintf("Order %d is on hold; release it before registering delivery.", o.Number))
			}
			index := map[int64]int{}
			for i, l := range lines {
				index[l.ID] = i
			}
			var failures []lineFailure
			var actions []changeAction
			for i, in := range body.Lines {
				at, ok := index[in.LineId]
				if !ok || lines[at].Kind != kindProduct || !lines[at].Quantity.Valid {
					failures = append(failures, lineFailure{index: i, code: codeLineNotFound,
						detail: fmt.Sprintf("Line %d is not a product line of this order.", in.LineId)})
					continue
				}
				qty, msg := parseQuantity(in.Quantity)
				if msg != "" {
					failures = append(failures, lineFailure{index: i, code: codeQuantityInvalid, detail: msg})
					continue
				}
				l := lines[at]
				before := ratOrZero(l.QtyDelivered)
				after := new(big.Rat)
				switch {
				case !undo:
					after.Add(before, qty)
					limit := new(big.Rat).Sub(ratOf(l.Quantity), ratOrZero(l.QtyCancelled))
					if after.Cmp(limit) > 0 {
						failures = append(failures, lineFailure{index: i, code: codeOverDelivery,
							detail: fmt.Sprintf("Line %d has %s left to deliver.", l.Position, openQuantity(limit, before, new(big.Rat)).FloatString(quantityPlaces))})
						continue
					}
				default:
					after.Sub(before, qty)
					if after.Sign() < 0 {
						failures = append(failures, lineFailure{index: i, code: codeUndoExceedsDelivered,
							detail: fmt.Sprintf("Line %d has %s delivered.", l.Position, before.FloatString(quantityPlaces))})
						continue
					}
					if l.InvoicePolicy == "delivery" && after.Cmp(ratOrZero(l.QtyInvoiced)) < 0 {
						failures = append(failures, lineFailure{index: i, code: codeDeliveredBelowInvoiced,
							detail: fmt.Sprintf("Line %d is invoiced on delivery; what is invoiced stays delivered.", l.Position)})
						continue
					}
				}
				lines[at].QtyDelivered = numeric(after, quantityPlaces)
				kind := actionDeliveredMarked
				if undo {
					kind = actionDeliveredUndone
				}
				lineID := l.ID
				actions = append(actions, changeAction{Kind: kind, LineID: &lineID,
					From: ptr(before.FloatString(quantityPlaces)), To: ptr(after.FloatString(quantityPlaces))})
			}
			if len(failures) > 0 {
				return o, nil, refuseLines(invalidDeliveryTitle, failures)
			}
			for _, l := range lines {
				if err := txq.SetLineBuckets(ctx, store.SetLineBucketsParams{
					ID: l.ID, QtyCancelled: l.QtyCancelled, QtyDelivered: l.QtyDelivered, QtyInvoiced: l.QtyInvoiced,
				}); err != nil {
					return o, nil, fmt.Errorf("orders: mark line %d delivered: %w", l.ID, err)
				}
			}
			if !undo {
				// What was marked delivered is no longer held for the line
				// (O45): its reservation beyond what is still open is
				// released. An undo reserves nothing again.
				if err := s.releaseBeyondOpen(ctx, tx, o.ID, lines); err != nil {
					return o, nil, err
				}
			}
			row, err := s.bumpProgress(ctx, tx, txq, o, lines, o.Version)
			return row, actions, err
		})
}

// MarkSalesOrderDelivered Register delivered quantities
// (POST /api/v1/orders/orders/{id}/mark-delivered)
func (s *server) MarkSalesOrderDelivered(ctx context.Context, req gen.MarkSalesOrderDeliveredRequestObject) (gen.MarkSalesOrderDeliveredResponseObject, error) {
	out, err := s.markDelivered(ctx, req.Id, *req.Body, false)
	if err != nil {
		return answer(err, orderAnswers(
			func(p gen.OrdersProblem) gen.MarkSalesOrderDeliveredResponseObject {
				return gen.MarkSalesOrderDelivered400ApplicationProblemPlusJSONResponse(p)
			},
			func() gen.MarkSalesOrderDeliveredResponseObject { return gen.MarkSalesOrderDelivered404Response{} },
			func(p gen.OrdersProblem) gen.MarkSalesOrderDeliveredResponseObject {
				return gen.MarkSalesOrderDelivered409ApplicationProblemPlusJSONResponse(p)
			}))
	}
	return gen.MarkSalesOrderDelivered200JSONResponse(out), nil
}

// UndoSalesOrderDelivered Undo delivered quantities
// (POST /api/v1/orders/orders/{id}/undo-delivered)
func (s *server) UndoSalesOrderDelivered(ctx context.Context, req gen.UndoSalesOrderDeliveredRequestObject) (gen.UndoSalesOrderDeliveredResponseObject, error) {
	out, err := s.markDelivered(ctx, req.Id, *req.Body, true)
	if err != nil {
		return answer(err, orderAnswers(
			func(p gen.OrdersProblem) gen.UndoSalesOrderDeliveredResponseObject {
				return gen.UndoSalesOrderDelivered400ApplicationProblemPlusJSONResponse(p)
			},
			func() gen.UndoSalesOrderDeliveredResponseObject { return gen.UndoSalesOrderDelivered404Response{} },
			func(p gen.OrdersProblem) gen.UndoSalesOrderDeliveredResponseObject {
				return gen.UndoSalesOrderDelivered409ApplicationProblemPlusJSONResponse(p)
			}))
	}
	return gen.UndoSalesOrderDelivered200JSONResponse(out), nil
}
