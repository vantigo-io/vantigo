package orders

// Manual delivery marking (orders design O32): until delivery C, and for
// services and non-stock lines after it, what was delivered is registered by
// hand on a confirmed order. A marking adds to a line's delivered quantity,
// never beyond what is ordered less cancelled (over_delivery); an undo takes
// it back, never below 0 (undo_exceeds_delivered) and, on a line invoiced on
// delivery, never below what is invoiced (delivered_below_invoiced) nor below
// that and what a draft invoice holds (line_in_draft_invoice, 409). Every
// line of the request is judged at once. The derived delivery status is
// recomputed and delivered_marked or delivered_undone logged per line; the
// version is not bumped, since the confirmation prints nothing delivered. A
// marking releases what a line holds reserved beyond what is still open
// (O45); an undo reserves nothing again — Reserve now does.
//
// A kit (manufacturing phase 2 M145): a kit line delivered by delivery
// documents is delivered as its child lines, never by hand (line_invalid),
// and its children marked by hand bring it to the whole kits they make; a kit
// line marked by hand marks its children in step — each child's delivered is
// the kit's × per kit — and a child of such a kit is not marked on its own
// (line_invalid).

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
			kits := kitsOf(lines)
			var handKits []int
			// kitRows is, per kit delivered by documents, the request's first
			// row naming one of its components: where a refusal for the kit
			// is put.
			kitRows := map[int64]int{}
			var allocs lineAllocations
			if undo {
				rows, err := orderAllocations(ctx, txq, o.ID)
				if err != nil {
					return o, nil, err
				}
				allocs = allocationsByLine(rows)
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
				if kits[l.ID] && l.DeliveryMethod == methodDelivery {
					failures = append(failures, lineFailure{index: i, code: codeLineInvalid,
						detail: fmt.Sprintf("Line %d is a kit delivered by delivery documents: its components are delivered.", l.Position)})
					continue
				}
				if l.ParentLineID != nil && lines[index[*l.ParentLineID]].DeliveryMethod != methodDelivery {
					failures = append(failures, lineFailure{index: i, code: codeLineInvalid,
						detail: fmt.Sprintf("Line %d is a component of a kit: register the kit delivered.", l.Position)})
					continue
				}
				if kits[l.ID] {
					handKits = append(handKits, at)
				}
				if l.ParentLineID != nil {
					if _, seen := kitRows[*l.ParentLineID]; !seen {
						kitRows[*l.ParentLineID] = i
					}
				}
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
					if l.InvoicePolicy == policyOnDelivery && after.Cmp(ratOrZero(l.QtyInvoiced)) < 0 {
						failures = append(failures, lineFailure{index: i, code: codeDeliveredBelowInvoiced,
							detail: fmt.Sprintf("Line %d is invoiced on delivery; what is invoiced stays delivered.", l.Position)})
						continue
					}
					if billed := new(big.Rat).Add(ratOrZero(l.QtyInvoiced), allocs.drafted(l.ID)); l.InvoicePolicy == policyOnDelivery && after.Cmp(billed) < 0 {
						return o, nil, conflict(codeLineInDraftInvoice, "The line is in a draft invoice",
							fmt.Sprintf("Line %d is invoiced on delivery and %s of it is in a draft invoice; what is in the draft stays delivered until the draft is issued or deleted, or its allocations released.",
								l.Position, allocs.drafted(l.ID).FloatString(quantityPlaces)))
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
			for _, k := range handKits {
				actions = append(actions, markKitChildren(lines, lines[k], undo)...)
			}
			syncKits(lines)
			if undo {
				// A component's undo lowers the whole kits its kit counts
				// delivered. The kit carries the invoice, so it is held to
				// the rule a line of its own is: invoiced on delivery, what
				// is invoiced or in a draft invoice stays delivered.
				for _, l := range lines {
					row, touched := kitRows[l.ID]
					if !touched || l.InvoicePolicy != policyOnDelivery {
						continue
					}
					delivered := ratOrZero(l.QtyDelivered)
					if delivered.Cmp(ratOrZero(l.QtyInvoiced)) < 0 {
						failures = append(failures, lineFailure{index: row, code: codeDeliveredBelowInvoiced,
							detail: fmt.Sprintf("Line %d, the kit, is invoiced on delivery; the components of what is invoiced stay delivered.", l.Position)})
						continue
					}
					if billed := new(big.Rat).Add(ratOrZero(l.QtyInvoiced), allocs.drafted(l.ID)); delivered.Cmp(billed) < 0 {
						return o, nil, conflict(codeLineInDraftInvoice, "The line is in a draft invoice",
							fmt.Sprintf("Line %d, the kit, is invoiced on delivery and %s of it is in a draft invoice; its components stay delivered until the draft is issued or deleted, or its allocations released.",
								l.Position, allocs.drafted(l.ID).FloatString(quantityPlaces)))
					}
				}
				if len(failures) > 0 {
					return o, nil, refuseLines(invalidDeliveryTitle, failures)
				}
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

// markKitChildren brings a kit line marked delivered by hand's children in
// step with it, in lines (M145): each child's delivered is the kit's × per
// kit, half-up to three decimals, within 0 and its ordered less cancelled.
// It answers the children's delivered_marked (delivered_undone) actions.
func markKitChildren(lines []store.OrdersLine, kit store.OrdersLine, undo bool) []changeAction {
	kind := actionDeliveredMarked
	if undo {
		kind = actionDeliveredUndone
	}
	var out []changeAction
	for i := range lines {
		c := &lines[i]
		if c.ParentLineID == nil || *c.ParentLineID != kit.ID {
			continue
		}
		after := new(big.Rat).Mul(ratOrZero(kit.QtyDelivered), ratOrZero(c.PerKit))
		after.SetString(after.FloatString(quantityPlaces))
		if most := new(big.Rat).Sub(ratOrZero(c.Quantity), ratOrZero(c.QtyCancelled)); after.Cmp(most) > 0 {
			after = most
		}
		if after.Sign() < 0 {
			after = new(big.Rat)
		}
		before := ratOrZero(c.QtyDelivered)
		if after.Cmp(before) == 0 {
			continue
		}
		c.QtyDelivered = numeric(after, quantityPlaces)
		out = append(out, changeAction{Kind: kind, LineID: &c.ID,
			From: ptr(before.FloatString(quantityPlaces)), To: ptr(after.FloatString(quantityPlaces))})
	}
	return out
}
