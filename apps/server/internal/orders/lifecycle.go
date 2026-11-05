package orders

// An order's life (orders design O18): confirm, hold and release, lock and
// unlock, cancel and cancel what remains. Every write locks the order's lines'
// variants first where it touches its lines (MB rule 21), then the order's
// row, judges it again under the lock, and publishes its event only while it
// holds the row (O3).
//
// The version (O26) is the confirmation's: a draft stays at 1, and an edit
// that changes a confirmed order or a cancel of what remains bumps it, so a
// confirmation sent at 2 or later prints as revised. A hold, a release, a
// lock, an unlock and a delivery marking change no printed word and are
// logged at the version they found.

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

func init() {
	confirmOrderTx = confirmTx
	afterOrderConfirmed = func(ctx context.Context, s *server, order store.OrdersOrder) {
		if _, _, err := s.ensureConfirmation(context.WithoutCancel(ctx), order.ID); err != nil {
			// The confirmation is still printed and stored by the first
			// download or send of this version (ensureConfirmation).
			s.deps.Logger.WarnContext(ctx, "orders: a confirmation's PDF could not be stored", "order_id", order.ID, "error", err.Error())
		}
	}
}

// customerConfirmable is the refusal of a confirm whose customer may take no
// order (customer_disabled): gone, merged away, disabled or archived.
func customerConfirmable(p *contracts.CustomerBillingProfile) error {
	var detail string
	switch {
	case p == nil:
		detail = "The customer no longer exists."
	case p.MergedInto != nil:
		detail = fmt.Sprintf("The customer was merged into customer %d; change the order's customer first.", *p.MergedInto)
	case p.Archived || p.Status != "active":
		detail = "The customer is disabled or archived."
	default:
		return nil
	}
	return refuse(problem(http.StatusBadRequest, codeCustomerDisabled, invalidConfirmTitle, detail))
}

// confirmTx is confirmOrderTx (ordercreate.go): the judge and the write of a
// confirm on the caller's transaction, which holds the order's row and its
// lines' variants.
func confirmTx(ctx context.Context, s *server, tx pgx.Tx, txq *store.Queries, order store.OrdersOrder, lines []store.OrdersLine,
	by actor, customer *contracts.CustomerBillingProfile) (store.OrdersOrder, error) {
	if order.Status != orderDraft {
		return order, conflict(codeOrderNotDraft, "The order is not a draft",
			fmt.Sprintf("Order %d is %s; only a draft is confirmed.", order.Number, order.Status))
	}
	if err := customerConfirmable(customer); err != nil {
		return order, err
	}
	products := 0
	for _, l := range lines {
		if l.Kind == kindProduct {
			products++
		}
	}
	if products == 0 {
		return order, refuse(problem(http.StatusBadRequest, codeOrderEmpty, invalidConfirmTitle, "The order has no product lines."))
	}
	if failures := sendable(lines); len(failures) > 0 {
		return order, refuseLines(invalidConfirmTitle, failures)
	}
	now := s.deps.Clock()
	row, err := txq.ConfirmOrderRow(ctx, store.ConfirmOrderRowParams{ID: order.ID, Now: &now, ByUserID: by.id, ByDisplay: by.display})
	if err != nil {
		return order, fmt.Errorf("orders: confirm order %d: %w", order.ID, err)
	}
	if err := events.Publish(ctx, tx, moduleName, contracts.SalesOrderConfirmed{
		OrderID: row.ID, Number: row.Number, Version: row.Version, CustomerID: row.CustomerID, Currency: row.Currency,
		NetTotal: ratOrZero(row.NetTotal).FloatString(amountPlaces), ConfirmedAt: now,
	}, now); err != nil {
		return row, err
	}
	return row, nil
}

// pricedLinesOf are stored lines as lockLines takes them.
func pricedLinesOf(lines []store.OrdersLine) []pricedLine {
	out := make([]pricedLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, pricedLineOf(l))
	}
	return out
}

// orderAnswers are the typed refusals most order writes share.
func orderAnswers[R any](bad func(gen.OrdersProblem) R, nf func() R, cf func(gen.OrdersProblem) R) answers[R] {
	return answers[R]{badRequest: bad, notFound: nf, conflict: cf}
}

// ConfirmSalesOrder Confirm an order
// (POST /api/v1/orders/orders/{id}/confirm)
//
// The customer's billing profile is read before the transaction, which locks
// the lines' variants first, then the order, and confirms it (confirmTx);
// the confirmation's PDF is stored after the commit (O24).
func (s *server) ConfirmSalesOrder(ctx context.Context, req gen.ConfirmSalesOrderRequestObject) (gen.ConfirmSalesOrderResponseObject, error) {
	resp := orderAnswers(
		func(p gen.OrdersProblem) gen.ConfirmSalesOrderResponseObject {
			return gen.ConfirmSalesOrder400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.ConfirmSalesOrderResponseObject { return gen.ConfirmSalesOrder404Response{} },
		func(p gen.OrdersProblem) gen.ConfirmSalesOrderResponseObject {
			return gen.ConfirmSalesOrder409ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, versionInputSchema); f.failed() {
		return gen.ConfirmSalesOrder400ApplicationProblemPlusJSONResponse(invalid(invalidOrderTitle, f)), nil
	}
	cur, lines, err := readOrder(ctx, store.New(s.deps.Pool), req.Id)
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeVersion(cur, req.Body.Version); err != nil {
		return answer(err, resp)
	}
	customer, err := s.billingProfile(ctx, cur.CustomerID)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var confirmed store.OrdersOrder
	var out gen.OrdersOrder
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidConfirmTitle, pricedLinesOf(lines)); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if err := judgeVersion(o, req.Body.Version); err != nil {
			return err
		}
		locked, err := linesOf(ctx, txq, ownerOrder, o.ID)
		if err != nil {
			return err
		}
		if confirmed, err = confirmTx(ctx, s, tx, txq, o, locked, by, customer); err != nil {
			return err
		}
		out = orderView(confirmed, locked)
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	afterOrderConfirmed(ctx, s, confirmed)
	return gen.ConfirmSalesOrder200JSONResponse(out), nil
}

// changeOrder locks an order and its lines on one transaction, judges it at
// version, and writes it with write, which answers the order as it now is
// and the actions to log; the entry is logged at the order's version after
// the write. It answers the order as the API does.
func (s *server) changeOrder(ctx context.Context, id int64, version int32, lockVariants bool,
	write func(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.OrdersOrder, lines []store.OrdersLine) (store.OrdersOrder, []changeAction, error)) (gen.OrdersOrder, error) {
	by, err := s.actorFor(ctx)
	if err != nil {
		return gen.OrdersOrder{}, err
	}
	var before []store.OrdersLine
	if lockVariants {
		if _, before, err = readOrder(ctx, store.New(s.deps.Pool), id); err != nil {
			return gen.OrdersOrder{}, err
		}
	}
	var out gen.OrdersOrder
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if err := s.lockLines(ctx, tx, invalidOrderTitle, pricedLinesOf(before)); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, id)
		if err != nil {
			return err
		}
		if err := judgeVersion(o, version); err != nil {
			return err
		}
		lines, err := linesOf(ctx, txq, ownerOrder, id)
		if err != nil {
			return err
		}
		row, actions, err := write(ctx, tx, txq, o, lines)
		if err != nil {
			return err
		}
		if err := logChange(ctx, txq, row.ID, row.Version, by, s.deps.Clock(), actions); err != nil {
			return err
		}
		if lines, err = linesOf(ctx, txq, ownerOrder, id); err != nil {
			return err
		}
		out = orderView(row, lines)
		return nil
	})
	return out, err
}

// HoldSalesOrder Hold an order
// (POST /api/v1/orders/orders/{id}/hold)
func (s *server) HoldSalesOrder(ctx context.Context, req gen.HoldSalesOrderRequestObject) (gen.HoldSalesOrderResponseObject, error) {
	resp := orderAnswers(
		func(p gen.OrdersProblem) gen.HoldSalesOrderResponseObject {
			return gen.HoldSalesOrder400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.HoldSalesOrderResponseObject { return gen.HoldSalesOrder404Response{} },
		func(p gen.OrdersProblem) gen.HoldSalesOrderResponseObject {
			return gen.HoldSalesOrder409ApplicationProblemPlusJSONResponse(p)
		})
	f := s.unknownProperties(ctx, holdInputSchema)
	var reason *string
	if req.Body.Reason != nil {
		reason = textPtr(*req.Body.Reason)
		if reason != nil && utf8.RuneCountInString(*reason) > 200 {
			f.add("reason", "A reason is at most 200 characters")
		}
	}
	if f.failed() {
		return gen.HoldSalesOrder400ApplicationProblemPlusJSONResponse(invalid(invalidHoldTitle, f)), nil
	}
	out, err := s.changeOrder(ctx, req.Id, req.Body.Version, false,
		func(ctx context.Context, _ pgx.Tx, txq *store.Queries, o store.OrdersOrder, _ []store.OrdersLine) (store.OrdersOrder, []changeAction, error) {
			if err := judgeConfirmed(o); err != nil {
				return o, nil, err
			}
			row, err := txq.SetOrderHold(ctx, store.SetOrderHoldParams{ID: o.ID, OnHold: true, HoldReason: reason, Now: s.deps.Clock()})
			if err != nil {
				return o, nil, fmt.Errorf("orders: hold order %d: %w", o.ID, err)
			}
			return row, []changeAction{{Kind: actionHeld, From: o.HoldReason, To: reason}}, nil
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.HoldSalesOrder200JSONResponse(out), nil
}

// ReleaseSalesOrder Release a held order
// (POST /api/v1/orders/orders/{id}/release)
//
// An order not on hold is answered as it is, and nothing is logged.
func (s *server) ReleaseSalesOrder(ctx context.Context, req gen.ReleaseSalesOrderRequestObject) (gen.ReleaseSalesOrderResponseObject, error) {
	resp := orderAnswers(nil,
		func() gen.ReleaseSalesOrderResponseObject { return gen.ReleaseSalesOrder404Response{} },
		func(p gen.OrdersProblem) gen.ReleaseSalesOrderResponseObject {
			return gen.ReleaseSalesOrder409ApplicationProblemPlusJSONResponse(p)
		})
	out, err := s.changeOrder(ctx, req.Id, req.Body.Version, false,
		func(ctx context.Context, _ pgx.Tx, txq *store.Queries, o store.OrdersOrder, _ []store.OrdersLine) (store.OrdersOrder, []changeAction, error) {
			if !o.OnHold {
				return o, nil, nil
			}
			row, err := txq.SetOrderHold(ctx, store.SetOrderHoldParams{ID: o.ID, OnHold: false, Now: s.deps.Clock()})
			if err != nil {
				return o, nil, fmt.Errorf("orders: release order %d: %w", o.ID, err)
			}
			return row, []changeAction{{Kind: actionReleased, From: o.HoldReason}}, nil
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.ReleaseSalesOrder200JSONResponse(out), nil
}

// setLocked is lock and unlock (O18): a confirmed order only; one already so
// is answered as it is, and nothing is logged.
func (s *server) setLocked(ctx context.Context, id int64, version int32, locked bool) (gen.OrdersOrder, error) {
	return s.changeOrder(ctx, id, version, false,
		func(ctx context.Context, _ pgx.Tx, txq *store.Queries, o store.OrdersOrder, _ []store.OrdersLine) (store.OrdersOrder, []changeAction, error) {
			if err := judgeConfirmed(o); err != nil {
				return o, nil, err
			}
			if o.Locked == locked {
				return o, nil, nil
			}
			row, err := txq.SetOrderLocked(ctx, store.SetOrderLockedParams{ID: o.ID, Locked: locked, Now: s.deps.Clock()})
			if err != nil {
				return o, nil, fmt.Errorf("orders: lock order %d: %w", o.ID, err)
			}
			kind := actionLocked
			if !locked {
				kind = actionUnlocked
			}
			return row, []changeAction{{Kind: kind}}, nil
		})
}

// LockSalesOrder Lock an order
// (POST /api/v1/orders/orders/{id}/lock)
func (s *server) LockSalesOrder(ctx context.Context, req gen.LockSalesOrderRequestObject) (gen.LockSalesOrderResponseObject, error) {
	out, err := s.setLocked(ctx, req.Id, req.Body.Version, true)
	if err != nil {
		return answer(err, orderAnswers(nil,
			func() gen.LockSalesOrderResponseObject { return gen.LockSalesOrder404Response{} },
			func(p gen.OrdersProblem) gen.LockSalesOrderResponseObject {
				return gen.LockSalesOrder409ApplicationProblemPlusJSONResponse(p)
			}))
	}
	return gen.LockSalesOrder200JSONResponse(out), nil
}

// UnlockSalesOrder Unlock an order
// (POST /api/v1/orders/orders/{id}/unlock)
func (s *server) UnlockSalesOrder(ctx context.Context, req gen.UnlockSalesOrderRequestObject) (gen.UnlockSalesOrderResponseObject, error) {
	out, err := s.setLocked(ctx, req.Id, req.Body.Version, false)
	if err != nil {
		return answer(err, orderAnswers(nil,
			func() gen.UnlockSalesOrderResponseObject { return gen.UnlockSalesOrder404Response{} },
			func(p gen.OrdersProblem) gen.UnlockSalesOrderResponseObject {
				return gen.UnlockSalesOrder409ApplicationProblemPlusJSONResponse(p)
			}))
	}
	return gen.UnlockSalesOrder200JSONResponse(out), nil
}

// CancelSalesOrder Cancel an order
// (POST /api/v1/orders/orders/{id}/cancel)
//
// A draft or a confirmed order with nothing delivered or invoiced is
// cancelled (O18); after that the order has progress (order_has_progress) and
// Cancel remaining is the way. orders.sales_order.cancelled.v1 is published
// for an order that was confirmed — a draft was never on the customer's
// timeline.
func (s *server) CancelSalesOrder(ctx context.Context, req gen.CancelSalesOrderRequestObject) (gen.CancelSalesOrderResponseObject, error) {
	out, err := s.changeOrder(ctx, req.Id, req.Body.Version, true,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.OrdersOrder, lines []store.OrdersLine) (store.OrdersOrder, []changeAction, error) {
			if o.Status == orderCancelled {
				return o, nil, conflict(codeOrderNotConfirmed, "The order is cancelled",
					fmt.Sprintf("Order %d is cancelled already.", o.Number))
			}
			if hasProgress(lines) {
				return o, nil, conflict(codeOrderHasProgress, "The order has progress",
					fmt.Sprintf("Something of order %d is delivered or invoiced; cancel what remains instead.", o.Number))
			}
			now := s.deps.Clock()
			row, err := txq.CancelOrderRow(ctx, store.CancelOrderRowParams{ID: o.ID, Now: &now})
			if err != nil {
				return o, nil, fmt.Errorf("orders: cancel order %d: %w", o.ID, err)
			}
			if o.Status == orderConfirmed {
				if err := events.Publish(ctx, tx, moduleName, contracts.SalesOrderCancelled{
					OrderID: row.ID, Number: row.Number, CustomerID: row.CustomerID, CancelledAt: now,
				}, now); err != nil {
					return row, nil, err
				}
			}
			return row, nil, nil
		})
	if err != nil {
		return answer(err, orderAnswers(nil,
			func() gen.CancelSalesOrderResponseObject { return gen.CancelSalesOrder404Response{} },
			func(p gen.OrdersProblem) gen.CancelSalesOrderResponseObject {
				return gen.CancelSalesOrder409ApplicationProblemPlusJSONResponse(p)
			}))
	}
	return gen.CancelSalesOrder200JSONResponse(out), nil
}

// CancelSalesOrderRemaining Cancel what remains of an order
// (POST /api/v1/orders/orders/{id}/cancel-remaining)
//
// Each product line's cancelled quantity becomes ordered less the larger of
// delivered and invoiced (O18). When that changes a line, the version is
// bumped, the derived statuses recomputed, cancelled_remaining logged per line
// and orders.sales_order.changed.v1 published; otherwise nothing is written.
func (s *server) CancelSalesOrderRemaining(ctx context.Context, req gen.CancelSalesOrderRemainingRequestObject) (gen.CancelSalesOrderRemainingResponseObject, error) {
	out, err := s.changeOrder(ctx, req.Id, req.Body.Version, true,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.OrdersOrder, lines []store.OrdersLine) (store.OrdersOrder, []changeAction, error) {
			if err := judgeConfirmed(o); err != nil {
				return o, nil, err
			}
			if err := judgeUnlocked(o); err != nil {
				return o, nil, err
			}
			var actions []changeAction
			for i, l := range lines {
				if l.Kind != kindProduct || !l.Quantity.Valid {
					continue
				}
				done := ratOrZero(l.QtyDelivered)
				if inv := ratOrZero(l.QtyInvoiced); inv.Cmp(done) > 0 {
					done = inv
				}
				cancelled := openQuantity(ratOf(l.Quantity), done, new(big.Rat))
				old := ratOrZero(l.QtyCancelled)
				if cancelled.Cmp(old) == 0 {
					continue
				}
				if err := txq.SetLineBuckets(ctx, store.SetLineBucketsParams{
					ID: l.ID, QtyCancelled: numeric(cancelled, quantityPlaces), QtyDelivered: l.QtyDelivered, QtyInvoiced: l.QtyInvoiced,
				}); err != nil {
					return o, nil, fmt.Errorf("orders: cancel line %d's remainder: %w", l.ID, err)
				}
				lines[i].QtyCancelled = numeric(cancelled, quantityPlaces)
				actions = append(actions, changeAction{Kind: actionCancelledRemaining, LineID: &l.ID,
					From: ptr(old.FloatString(quantityPlaces)), To: ptr(cancelled.FloatString(quantityPlaces))})
			}
			if len(actions) == 0 {
				return o, nil, nil
			}
			row, err := s.bumpProgress(ctx, tx, txq, o, lines, o.Version+1)
			return row, actions, err
		})
	if err != nil {
		return answer(err, orderAnswers(nil,
			func() gen.CancelSalesOrderRemainingResponseObject { return gen.CancelSalesOrderRemaining404Response{} },
			func(p gen.OrdersProblem) gen.CancelSalesOrderRemainingResponseObject {
				return gen.CancelSalesOrderRemaining409ApplicationProblemPlusJSONResponse(p)
			}))
	}
	return gen.CancelSalesOrderRemaining200JSONResponse(out), nil
}

// bumpProgress stores an order's derived statuses from its lines at version;
// a version above the order's publishes orders.sales_order.changed.v1.
func (s *server) bumpProgress(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.OrdersOrder, lines []store.OrdersLine, version int32) (store.OrdersOrder, error) {
	delivery, invoice := progressOf(lines)
	now := s.deps.Clock()
	row, err := txq.SetOrderProgress(ctx, store.SetOrderProgressParams{
		ID: o.ID, DeliveryStatus: delivery, InvoiceStatus: invoice, Version: version, Now: now,
	})
	if err != nil {
		return o, fmt.Errorf("orders: store order %d's progress: %w", o.ID, err)
	}
	if version > o.Version {
		if err := publishChanged(ctx, tx, row, now); err != nil {
			return row, err
		}
	}
	return row, nil
}

// publishChanged publishes orders.sales_order.changed.v1 for an order at its
// version, on the transaction that holds its row.
func publishChanged(ctx context.Context, tx pgx.Tx, o store.OrdersOrder, at time.Time) error {
	return events.Publish(ctx, tx, moduleName, contracts.SalesOrderChanged{
		OrderID: o.ID, Number: o.Number, Version: o.Version, CustomerID: o.CustomerID, ChangedAt: at,
	}, at)
}
