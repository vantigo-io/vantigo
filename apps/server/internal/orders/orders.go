package orders

// The sales order operations (orders design O18, O26, O28, O32), by file:
// this one reads an order — the list, one order, the change log — and
// deletes a draft, and holds what the others share: the version judge, the
// derived progress and the change log's writer. create.go creates an order
// through the one create command (ordercreate.go); edit.go saves one, a draft
// or a confirmed order (O26); lifecycle.go confirms, holds, releases, locks,
// unlocks and cancels; delivered.go marks delivery by hand (O32); ordersend.go
// prints, stores, sends and lists an order's confirmations.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The derived statuses of an order (O18).
const (
	progressNone      = "none"
	progressPartial   = "partial"
	progressDelivered = "delivered"
	progressInvoiced  = "invoiced"
)

// The typed actions of an order's change log (O26, O32).
const (
	actionLineAdded          = "line_added"
	actionLineRemoved        = "line_removed"
	actionQuantityChanged    = "quantity_changed"
	actionPriceChanged       = "price_changed"
	actionDiscountChanged    = "discount_changed"
	actionTextChanged        = "text_changed"
	actionDeliveryChanged    = "delivery_changed"
	actionContactChanged     = "contact_changed"
	actionReferenceChanged   = "reference_changed"
	actionCancelledRemaining = "cancelled_remaining"
	actionHeld               = "held"
	actionReleased           = "released"
	actionLocked             = "locked"
	actionUnlocked           = "unlocked"
	actionDeliveredMarked    = "delivered_marked"
	actionDeliveredUndone    = "delivered_undone"
	// A kit's component delivered beyond the whole kits a cancel of the
	// remainder left (manufacturing phase 2 M145): to is the excess.
	actionDeliveredBeyondKits = "delivered_beyond_kits"
	// actionWarningOverridden is a block warning overridden at confirm
	// (O33): from is the warning, to the reason.
	actionWarningOverridden = "warning_overridden"
	// actionDraftAllocationsReleased is the order page's Release draft
	// allocations (O36): to is how many were released.
	actionDraftAllocationsReleased = "draft_allocations_released"
	// actionPolicyChanged is a line's invoice policy changed (O48);
	// actionDeliveryPosted a delivery posted (O47): to is its number.
	actionPolicyChanged  = "policy_changed"
	actionDeliveryPosted = "delivery_posted"
)

// changeAction is one typed action of a change log entry: its kind, the line
// it is about when it is about one, and the values before and after as text.
type changeAction struct {
	Kind   string  `json:"kind"`
	LineID *int64  `json:"lineId,omitempty"`
	From   *string `json:"from,omitempty"`
	To     *string `json:"to,omitempty"`
}

// The kinds of actor a change log entry names.
const (
	actorUser   = "user"
	actorSystem = "system"
)

// logChange writes one change log entry on the caller's transaction, which
// holds the order's row (O26). The actor's id and display name are kept as
// they were when the change was made: a user's erasure leaves them (MB rule
// 19, orders design O29) — the log is the record of what was agreed, as
// Inventory's movements keep their poster — so no sweep against the user
// directory runs over this table, nor over the salesperson and the actors
// snapshotted on quotes and orders.
func logChange(ctx context.Context, txq *store.Queries, orderID int64, version int32, by actor, at time.Time, actions []changeAction) error {
	if len(actions) == 0 {
		return nil
	}
	body, err := json.Marshal(actions)
	if err != nil {
		return fmt.Errorf("orders: encode order %d's change: %w", orderID, err)
	}
	kind := actorUser
	if by.id == nil {
		kind = actorSystem
	}
	if _, err := txq.InsertOrderChange(ctx, store.InsertOrderChangeParams{
		OwnerID: orderID, Version: version, ActorKind: kind, ActorUserID: by.id, ActorDisplay: by.display, At: at, Actions: body,
	}); err != nil {
		return fmt.Errorf("orders: log order %d's change: %w", orderID, err)
	}
	return nil
}

// judgeVersion refuses a write made from a version of the order that is not
// its current one (O26), or from a read older than its last write
// (judgeFresh: the expectedUpdatedAt every write sends).
func judgeVersion(o store.OrdersOrder, version int32, expected time.Time) error {
	if o.Version != version {
		return versionConflict("order's version", o.Version, version)
	}
	return judgeFresh("order", o.UpdatedAt, expected)
}

// judgeConfirmed refuses a write that needs a confirmed order.
func judgeConfirmed(o store.OrdersOrder) error {
	if o.Status != orderConfirmed {
		return conflict(codeOrderNotConfirmed, "The order is not confirmed",
			fmt.Sprintf("Order %d is %s; only a confirmed order can take this.", o.Number, o.Status))
	}
	return nil
}

// judgeUnlocked refuses an edit of a locked order (O18).
func judgeUnlocked(o store.OrdersOrder) error {
	if o.Locked {
		return conflict(codeOrderLocked, "The order is locked",
			fmt.Sprintf("Order %d is locked; unlock it to change it.", o.Number))
	}
	return nil
}

// lockOrder reads an order under its row lock on the caller's transaction,
// after any variants' locks (MB rule 21); notFound when there is none.
func lockOrder(ctx context.Context, txq *store.Queries, id int64) (store.OrdersOrder, error) {
	o, err := txq.LockOrder(ctx, id)
	if noRows(err) {
		return o, notFound()
	}
	if err != nil {
		return o, fmt.Errorf("orders: lock order %d: %w", id, err)
	}
	return o, nil
}

// readOrder reads an order and its lines on q; notFound when there is none.
func readOrder(ctx context.Context, q *store.Queries, id int64) (store.OrdersOrder, []store.OrdersLine, error) {
	o, err := q.OrderByID(ctx, id)
	if noRows(err) {
		return o, nil, notFound()
	}
	if err != nil {
		return o, nil, fmt.Errorf("orders: read order %d: %w", id, err)
	}
	lines, err := linesOf(ctx, q, ownerOrder, id)
	return o, lines, err
}

// getOrderView reads an order and answers it, in one snapshot.
func (s *server) getOrderView(ctx context.Context, id int64) (gen.OrdersOrder, error) {
	var out gen.OrdersOrder
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		o, lines, err := readOrder(ctx, q, id)
		if err != nil {
			return err
		}
		warnings, err := documentWarnings(ctx, q, o.CustomerID, lines)
		if err != nil {
			return err
		}
		allocations, err := orderAllocations(ctx, q, id)
		if err != nil {
			return err
		}
		out = orderView(o, lines, warnings, allocations)
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, s.withStockFigures(ctx, &out)
}

// openQuantity is what remains of a line to deliver or invoice: ordered less
// cancelled less done, never below 0.
func openQuantity(ordered, cancelled, done *big.Rat) *big.Rat {
	open := new(big.Rat).Sub(ordered, cancelled)
	open.Sub(open, done)
	if open.Sign() < 0 {
		return new(big.Rat)
	}
	return open
}

// progressOf is an order's derived delivery and invoice status (O18): none
// while nothing is delivered (invoiced), delivered (invoiced) when every
// product line's delivered (invoiced) and cancelled cover what was ordered,
// partial between. A kit's child line counts toward the delivery status
// only: it is invoiced as its kit, which counts for it (manufacturing phase 2
// M145).
func progressOf(lines []store.OrdersLine) (delivery, invoice string) {
	status := func(done func(store.OrdersLine) *big.Rat, full string, children bool) string {
		any, all, products := false, true, 0
		for _, l := range lines {
			if l.Kind != kindProduct || !l.Quantity.Valid || (!children && l.ParentLineID != nil) {
				continue
			}
			products++
			d := done(l)
			if d.Sign() > 0 {
				any = true
			}
			if openQuantity(ratOf(l.Quantity), ratOrZero(l.QtyCancelled), d).Sign() > 0 {
				all = false
			}
		}
		switch {
		case !any || products == 0:
			return progressNone
		case all:
			return full
		}
		return progressPartial
	}
	delivery = status(func(l store.OrdersLine) *big.Rat { return ratOrZero(l.QtyDelivered) }, progressDelivered, true)
	invoice = status(func(l store.OrdersLine) *big.Rat { return ratOrZero(l.QtyInvoiced) }, progressInvoiced, false)
	return delivery, invoice
}

// hasProgress reports whether anything of an order is delivered or invoiced.
func hasProgress(lines []store.OrdersLine) bool {
	for _, l := range lines {
		if ratOrZero(l.QtyDelivered).Sign() > 0 || ratOrZero(l.QtyInvoiced).Sign() > 0 {
			return true
		}
	}
	return false
}

// ListSalesOrders List orders
// (GET /api/v1/orders/orders)
func (s *server) ListSalesOrders(ctx context.Context, req gen.ListSalesOrdersRequestObject) (gen.ListSalesOrdersResponseObject, error) {
	p := req.Params
	page, size := int32(1), int32(50)
	if p.Page != nil {
		page = *p.Page
	}
	if p.PageSize != nil {
		size = *p.PageSize
	}
	if page < 1 || size < 1 || size > 200 {
		var f fieldErrors
		f.add("page", "page is from 1, and pageSize 1 to 200")
		return gen.ListSalesOrders400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	statuses, deliveries, invoices := []string{}, []string{}, []string{}
	if p.Status != nil {
		for _, st := range *p.Status {
			statuses = append(statuses, string(st))
		}
	}
	if p.DeliveryStatus != nil {
		for _, st := range *p.DeliveryStatus {
			deliveries = append(deliveries, string(st))
		}
	}
	if p.InvoiceStatus != nil {
		for _, st := range *p.InvoiceStatus {
			invoices = append(invoices, string(st))
		}
	}
	search := ""
	if p.Q != nil {
		search = strings.TrimSpace(*p.Q)
	}
	open := p.Open != nil && *p.Open
	toDeliver := p.ToDeliver != nil && *p.ToDeliver
	out := gen.OrdersOrderPage{Items: []gen.OrdersOrderSummary{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.ListOrders(ctx, store.ListOrdersParams{
			CustomerID: p.CustomerId, Statuses: statuses, DeliveryStatuses: deliveries, InvoiceStatuses: invoices,
			SalespersonID: p.SalespersonId, Open: open, Done: p.Done, ToDeliver: toDeliver, Q: search, PageSize: size, PageOffset: (page - 1) * size,
		})
		if err != nil {
			return fmt.Errorf("orders: list orders: %w", err)
		}
		total, err := q.CountOrders(ctx, store.CountOrdersParams{
			CustomerID: p.CustomerId, Statuses: statuses, DeliveryStatuses: deliveries, InvoiceStatuses: invoices,
			SalespersonID: p.SalespersonId, Open: open, Done: p.Done, ToDeliver: toDeliver, Q: search,
		})
		if err != nil {
			return fmt.Errorf("orders: count orders: %w", err)
		}
		out.Total = total
		for _, row := range rows {
			r := row.OrdersOrder
			out.Items = append(out.Items, gen.OrdersOrderSummary{
				Id: r.ID, Number: r.Number, Status: gen.OrdersOrderSummaryStatus(r.Status), OnHold: r.OnHold, Locked: r.Locked,
				DeliveryStatus: gen.OrdersOrderSummaryDeliveryStatus(r.DeliveryStatus),
				InvoiceStatus:  gen.OrdersOrderSummaryInvoiceStatus(r.InvoiceStatus),
				Done:           row.Done, CustomerId: r.CustomerID, CustomerName: r.CustomerName, Currency: r.Currency,
				Net: wire(ratOrZero(r.NetTotal), amountPlaces), Gross: wire(ratOrZero(r.GrossTotal), amountPlaces),
				Salesperson: personOut(r.SalespersonID, r.SalespersonDisplay), DeliveryDate: dateOut(r.DeliveryDate),
				UpdatedAt: r.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.ListSalesOrders200JSONResponse(out), nil
}

// isDone reports whether an order is done (O18): confirmed, with every
// product line satisfied — what is cancelled and invoiced covers what was
// ordered, and so do cancelled and the larger of delivered and invoiced. An
// order whose remainder was cancelled whole is done; one delivered and not
// invoiced waits for its invoice. A kit's child line is invoiced as its kit
// (manufacturing phase 2 M145): cancelled and the larger of delivered and
// invoiced cover it, its kit answering for the invoice. It is shown, never
// stored; ListOrders and the retention sweep (AnonymiseCustomerOrders) carry
// the same rule in SQL.
func isDone(o store.OrdersOrder, lines []store.OrdersLine) bool {
	if o.Status != orderConfirmed {
		return false
	}
	products := 0
	for _, l := range lines {
		if l.Kind != kindProduct || !l.Quantity.Valid {
			continue
		}
		products++
		ordered, cancelled := ratOf(l.Quantity), ratOrZero(l.QtyCancelled)
		delivered, invoiced := ratOrZero(l.QtyDelivered), ratOrZero(l.QtyInvoiced)
		done := delivered
		if invoiced.Cmp(done) > 0 {
			done = invoiced
		}
		if (l.ParentLineID == nil && new(big.Rat).Add(cancelled, invoiced).Cmp(ordered) < 0) || new(big.Rat).Add(cancelled, done).Cmp(ordered) < 0 {
			return false
		}
	}
	return products > 0
}

// GetSalesOrder Get an order
// (GET /api/v1/orders/orders/{id})
func (s *server) GetSalesOrder(ctx context.Context, req gen.GetSalesOrderRequestObject) (gen.GetSalesOrderResponseObject, error) {
	out, err := s.getOrderView(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetSalesOrderResponseObject]{notFound: func() gen.GetSalesOrderResponseObject { return gen.GetSalesOrder404Response{} }})
	}
	return gen.GetSalesOrder200JSONResponse(out), nil
}

// DeleteSalesOrder Delete a draft order
// (DELETE /api/v1/orders/orders/{id})
//
// Only a draft is deleted (O54) — a confirmed or cancelled order is a record
// and is kept; its number is left a gap (O12). expectedUpdatedAt is the order
// as the client read it (version_conflict). A draft accepted from a quote puts
// that quote back to sent, on the same transaction, so it can be accepted
// again: nothing is logged on the quote but its updated_at, and nothing is
// published. The quote's variants are locked first (MB rule 21) — a quote sent
// again holds them against a delete (O30) — then the quote, then the order:
// quotes before orders, the erase's order.
func (s *server) DeleteSalesOrder(ctx context.Context, req gen.DeleteSalesOrderRequestObject) (gen.DeleteSalesOrderResponseObject, error) {
	resp := answers[gen.DeleteSalesOrderResponseObject]{
		notFound: func() gen.DeleteSalesOrderResponseObject { return gen.DeleteSalesOrder404Response{} },
		conflict: func(p gen.OrdersProblem) gen.DeleteSalesOrderResponseObject {
			return gen.DeleteSalesOrder409ApplicationProblemPlusJSONResponse(p)
		},
	}
	q := store.New(s.deps.Pool)
	cur, err := q.OrderByID(ctx, req.Id)
	if noRows(err) {
		return gen.DeleteSalesOrder404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read order %d: %w", req.Id, err)
	}
	quoteID := sourceQuoteID(cur)
	var variants []int32
	if quoteID != nil {
		lines, err := linesOf(ctx, q, ownerQuote, *quoteID)
		if err != nil {
			return nil, err
		}
		variants = variantIDsOf(pricedLinesOf(lines))
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if len(variants) > 0 {
			if _, err := s.lockVariants(ctx, tx, variants); err != nil {
				return err
			}
		}
		if quoteID != nil {
			if _, err := txq.LockQuote(ctx, *quoteID); err != nil && !noRows(err) {
				return fmt.Errorf("orders: lock quote %d: %w", *quoteID, err)
			}
		}
		o, err := lockOrder(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if o.Status != orderDraft {
			return conflict(codeOrderNotDraft, "The order cannot be deleted",
				fmt.Sprintf("Order %d is %s; only a draft can be deleted. Cancel it instead.", o.Number, o.Status))
		}
		if err := judgeFresh("order", o.UpdatedAt, req.Params.ExpectedUpdatedAt); err != nil {
			return err
		}
		if err := txq.DeleteLinesOf(ctx, store.DeleteLinesOfParams{OwnerKind: ownerOrder, OwnerID: o.ID}); err != nil {
			return fmt.Errorf("orders: delete order %d's lines: %w", o.ID, err)
		}
		if _, err := txq.DeleteOrderRow(ctx, o.ID); err != nil {
			return fmt.Errorf("orders: delete order %d: %w", o.ID, err)
		}
		if quoteID != nil {
			// Only a quote still accepted into this very order goes back.
			_, err := txq.RestoreAcceptedQuote(ctx, store.RestoreAcceptedQuoteParams{ID: *quoteID, OrderID: o.ID, Now: s.deps.Clock()})
			if err != nil && !noRows(err) {
				return fmt.Errorf("orders: put quote %d back to sent: %w", *quoteID, err)
			}
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.DeleteSalesOrder204Response{}, nil
}

// ListSalesOrderChanges List an order's changes
// (GET /api/v1/orders/orders/{id}/changes)
func (s *server) ListSalesOrderChanges(ctx context.Context, req gen.ListSalesOrderChangesRequestObject) (gen.ListSalesOrderChangesResponseObject, error) {
	out := gen.OrdersChangeList{Items: []gen.OrdersChange{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.OrderByID(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("orders: read order %d: %w", req.Id, err)
		}
		rows, err := q.OrderChangesOf(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("orders: read order %d's changes: %w", req.Id, err)
		}
		for _, r := range rows {
			var actions []changeAction
			if err := json.Unmarshal(r.Actions, &actions); err != nil {
				return fmt.Errorf("orders: decode change %d: %w", r.ID, err)
			}
			items := make([]gen.OrdersChangeAction, 0, len(actions))
			for _, a := range actions {
				items = append(items, gen.OrdersChangeAction{Kind: a.Kind, LineId: a.LineID, From: a.From, To: a.To})
			}
			who := personOut(r.ActorUserID, r.ActorDisplay)
			if who == nil {
				who = &gen.OrdersPerson{DisplayName: ""}
			}
			out.Items = append(out.Items, gen.OrdersChange{Id: r.ID, Version: r.Version, Actor: *who, At: r.At, Actions: items})
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.ListSalesOrderChangesResponseObject]{
			notFound: func() gen.ListSalesOrderChangesResponseObject { return gen.ListSalesOrderChanges404Response{} },
		})
	}
	return gen.ListSalesOrderChanges200JSONResponse(out), nil
}
