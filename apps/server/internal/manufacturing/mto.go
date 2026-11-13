package manufacturing

// Make-to-order (manufacturing design M43–M48, M43a, M44a, M44b, M46a): the
// handlers on Orders' events, the manufacturing.mto-worker that makes the
// orders, contracts.Production, and the sales order line the new-order form
// is prefilled from.
//
// The handlers keep rule 23: each writes only this module's schema on the
// delivery's transaction and reads no contract. manufacturing.mto queues one
// request per confirmed sales order; manufacturing.mto-cancelled flags the
// orders made for a cancelled one. The worker does the rest, outside any
// handler: it reads the order's lines through contracts.SalesOrderLines on
// the pool, judges each line against the BoM and Inventory's sites on the
// pool, and only then opens one transaction per sales order — the
// make-to-order lease, LockVariants, the orders created and confirmed, each
// reserving through ReserveForProduction — and marks the request done. It
// never holds a connection while it reads a contract, so it runs on a pool of
// one.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The origins an order names besides manual (M17): a sales order line (E)
// and a parent order (M47).
const (
	originSalesOrder = "sales_order"
	originParent     = "parent"
)

const (
	// mtoHandler queues a confirmed sales order's request (M44a).
	mtoHandler = "manufacturing.mto"
	// mtoCancelledHandler flags the orders made for a cancelled sales order
	// (M44).
	mtoCancelledHandler = "manufacturing.mto-cancelled"

	// mtoWorkerName is what the runner logs the worker as.
	mtoWorkerName = "manufacturing.mto-worker"
	// mtoLeaseKey is the ASCII string "MFGMTO01" read as a big-endian 64-bit
	// value: the make-to-order lease, an advisory lock every transaction that
	// makes orders for a sales order takes first (LockMto), so two replicas'
	// workers never make one request's orders at once. It is a transaction's
	// lock, never a session's, so the worker holds no connection between two
	// of its steps.
	mtoLeaseKey int64 = 0x4D46474D544F3031

	mtoPoll        = 15 * time.Second
	mtoBatch       = 20
	mtoMaxAttempts = 8
)

// The request's states and the reasons a line is left out (M44, M44a).
const (
	mtoPending   = "pending"
	mtoDone      = "done"
	mtoSkipped   = "skipped"
	mtoFailed    = "failed"
	mtoCancelled = "cancelled"

	reasonOrdersOff       = "orders_unavailable"
	reasonOrderUnknown    = "order_unknown"
	reasonOrderCancelled  = "order_cancelled"
	reasonNoBom           = "no_bom"
	reasonNotMakeToOrder  = "not_make_to_order"
	reasonNothingOpen     = "nothing_open"
	reasonHasOrder        = "has_order"
	reasonNoSite          = "no_site"
	reasonVariantNotGoods = "variant_not_goods"
)

// systemActor is who the worker and the handlers write as.
var systemActor = actor{display: "System"}

// salesOrderReference is the origin's display reference of a sales order.
func salesOrderReference(number int64) string { return "SO-" + strconv.FormatInt(number, 10) }

// eventHandlers are the module's handlers of the outbox (module.Module.
// EventHandlers), built from what a disabled module's Deps has too.
func eventHandlers(d module.Deps) []events.Handler {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	return []events.Handler{
		{
			Name:   mtoHandler,
			Events: []events.EventRef{{Name: contracts.SalesOrderConfirmed{}.EventName(), Version: 1}},
			Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
				return queueMto(ctx, tx, ev, d, clock)
			},
		},
		{
			Name:   mtoCancelledHandler,
			Events: []events.EventRef{{Name: contracts.SalesOrderCancelled{}.EventName(), Version: 1}},
			Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
				return flagOriginCancelled(ctx, tx, ev, d, clock)
			},
		},
	}
}

// enabled reports whether MODULES enables this module; a Deps with no
// Config cannot say and counts as enabled.
func enabled(d module.Deps) bool {
	return d.Config == nil || slices.Contains(d.Config.Modules, moduleName)
}

// queueMto inserts the sales order's request, once (M44a): a redelivery, or
// a second confirm, adds nothing. With the module off it does nothing.
func queueMto(ctx context.Context, tx pgx.Tx, ev events.Delivered, d module.Deps, clock func() time.Time) error {
	if !enabled(d) {
		return nil
	}
	var e contracts.SalesOrderConfirmed
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return fmt.Errorf("manufacturing: decode event %d: %w", ev.ID, err)
	}
	return store.New(tx).InsertMtoRequest(ctx, store.InsertMtoRequestParams{SalesOrderID: e.OrderID, EventID: ev.ID, Now: clock()})
}

// flagOriginCancelled flags the orders made for a cancelled sales order
// (M44): it never cancels them, because the goods may already be half made.
// Each flagged order's timeline says so, and its screens hear of it.
//
// It takes the make-to-order lease first, as the worker's transaction does,
// and with the module on marks the sales order's request cancelled — inserts
// it cancelled when this cancel is handled before the confirm — so the
// worker, which reads the request again under the lease, makes nothing for
// it; orders the worker committed before are flagged here, under the lease,
// after their commit.
func flagOriginCancelled(ctx context.Context, tx pgx.Tx, ev events.Delivered, d module.Deps, clock func() time.Time) error {
	var e contracts.SalesOrderCancelled
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return fmt.Errorf("manufacturing: decode event %d: %w", ev.ID, err)
	}
	q := store.New(tx)
	at := clock()
	if err := q.LockMto(ctx, mtoLeaseKey); err != nil {
		return err
	}
	if enabled(d) {
		if err := q.CancelMtoRequest(ctx, store.CancelMtoRequestParams{SalesOrderID: e.OrderID, EventID: ev.ID, Now: at}); err != nil {
			return err
		}
	}
	ids, err := q.FlagOriginCancelled(ctx, store.FlagOriginCancelledParams{SalesOrderID: &e.OrderID, Now: at})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := event(ctx, q, id, gen.ManufacturingOrderEventKindOriginCancelled, systemActor, at,
			map[string]any{"reference": salesOrderReference(e.Number)}); err != nil {
			return err
		}
		if err := notifyOrder(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

// mtoWorker makes the orders the queued requests ask for (M44a).
type mtoWorker struct{ s *server }

func (*mtoWorker) Name() string            { return mtoWorkerName }
func (*mtoWorker) Interval() time.Duration { return mtoPoll }

func (w *mtoWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(mtoPoll)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("manufacturing: the make-to-order cycle failed", "worker", mtoWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *mtoWorker) logger() *slog.Logger { return w.s.logger() }

// logger is the Deps' logger, the default one when the Deps carry none.
func (s *server) logger() *slog.Logger {
	if s.deps.Logger != nil {
		return s.deps.Logger
	}
	return slog.Default()
}

// RunCycle handles the due requests, oldest first, and answers how many it
// finished. A request that fails is counted and retried after a backoff; it
// never stops the others.
func (w *mtoWorker) RunCycle(ctx context.Context) (int, error) {
	s := w.s
	due, err := store.New(s.deps.Pool).DueMtoRequests(ctx, store.DueMtoRequestsParams{Now: s.deps.Clock(), RowLimit: mtoBatch})
	if err != nil {
		return 0, fmt.Errorf("manufacturing: list the make-to-order requests: %w", err)
	}
	finished := 0
	for _, r := range due {
		if ctx.Err() != nil {
			break
		}
		if err := w.handle(ctx, r); err != nil {
			w.logger().WarnContext(ctx, "manufacturing: a make-to-order request failed", "worker", mtoWorkerName,
				"request", r.ID, "salesOrder", r.SalesOrderID, "error", err.Error())
			if ferr := w.fail(ctx, r, err); ferr != nil {
				return finished, ferr
			}
			continue
		}
		finished++
	}
	return finished, nil
}

// fail counts a failed attempt and backs off: a minute doubled per attempt,
// at most an hour.
func (w *mtoWorker) fail(ctx context.Context, r store.ManufacturingMtoRequest, cause error) error {
	backoff := time.Minute << min(r.Attempts, 6)
	msg := cause.Error()
	return store.New(w.s.deps.Pool).FailMtoRequest(ctx, store.FailMtoRequestParams{
		ID: r.ID, LastError: &msg, NextAttemptAt: w.s.deps.Clock().Add(min(backoff, time.Hour)), MaxAttempts: mtoMaxAttempts,
	})
}

// mtoReason is one line left out of a request, and why.
type mtoReason struct {
	LineID int64  `json:"lineId,omitempty"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// mtoLine is a line the worker will make an order for, judged before the
// transaction.
type mtoLine struct {
	line   contracts.SalesOrderLine
	bom    store.ManufacturingBom
	fields orderFields
}

// handle makes one request's orders, or finishes it skipped.
func (w *mtoWorker) handle(ctx context.Context, r store.ManufacturingMtoRequest) error {
	s := w.s
	if s.deps.SalesOrderLines == nil {
		return w.finish(ctx, r.ID, mtoSkipped, []mtoReason{{Code: reasonOrdersOff, Detail: "Orders is not enabled."}})
	}
	lines, err := s.salesOrderLines(ctx, r.SalesOrderID)
	if err != nil {
		return err
	}
	switch {
	case len(lines) == 0:
		return w.finish(ctx, r.ID, mtoSkipped, []mtoReason{{Code: reasonOrderUnknown, Detail: "The sales order has no goods lines, or no longer exists."}})
	case lines[0].OrderStatus == "cancelled":
		return w.finish(ctx, r.ID, mtoSkipped, []mtoReason{{Code: reasonOrderCancelled, Detail: "The sales order was cancelled."}})
	}
	plan, reasons, err := s.planMto(ctx, lines)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		return w.finish(ctx, r.ID, mtoSkipped, reasons)
	}
	return s.makeMto(ctx, r.ID, plan, reasons)
}

// finish records a request's outcome on the pool.
func (w *mtoWorker) finish(ctx context.Context, id int64, status string, reasons []mtoReason) error {
	_, err := finishMto(ctx, store.New(w.s.deps.Pool), id, status, reasons, w.s.deps.Clock())
	return err
}

func finishMto(ctx context.Context, q *store.Queries, id int64, status string, reasons []mtoReason, at time.Time) (int64, error) {
	if reasons == nil {
		reasons = []mtoReason{}
	}
	body, err := json.Marshal(reasons)
	if err != nil {
		return 0, err
	}
	return q.FinishMtoRequest(ctx, store.FinishMtoRequestParams{ID: id, Status: status, Reasons: body, Now: &at})
}

// planMto judges each line before any transaction (M44, M44a): a goods line
// whose variant's default active BoM makes to order, with something open,
// and no live order yet; made at the BoM's work centers' site, else the
// sales order's — a line with neither is skipped no_site. Everything read
// here is read again under the lock.
func (s *server) planMto(ctx context.Context, lines []contracts.SalesOrderLine) ([]mtoLine, []mtoReason, error) {
	pool := store.New(s.deps.Pool)
	variantIDs := make([]int32, 0, len(lines))
	lineIDs := make([]int64, 0, len(lines))
	for _, l := range lines {
		variantIDs = append(variantIDs, l.VariantID)
		lineIDs = append(lineIDs, l.LineID)
	}
	boms, err := pool.DefaultBomsOfVariants(ctx, variantIDs)
	if err != nil {
		return nil, nil, err
	}
	byVariant := map[int32]store.ManufacturingBom{}
	for _, b := range boms {
		byVariant[b.VariantID] = b
	}
	live, err := pool.LinesWithLiveOrder(ctx, lineIDs)
	if err != nil {
		return nil, nil, err
	}
	var plan []mtoLine
	var reasons []mtoReason
	for _, l := range lines {
		b, ok := byVariant[l.VariantID]
		quantity, _ := new(big.Rat).SetString(l.Quantity)
		switch {
		case !ok:
			reasons = append(reasons, mtoReason{l.LineID, reasonNoBom, "The variant has no active bill of materials."})
			continue
		case !b.MakeToOrder:
			reasons = append(reasons, mtoReason{l.LineID, reasonNotMakeToOrder, "The variant's default bill of materials is not made to order."})
			continue
		case quantity == nil || quantity.Sign() <= 0:
			reasons = append(reasons, mtoReason{l.LineID, reasonNothingOpen, "Nothing of the line is open."})
			continue
		case slices.Contains(live, l.LineID):
			reasons = append(reasons, mtoReason{l.LineID, reasonHasOrder, "A manufacturing order is already made for the line."})
			continue
		}
		operations, err := bomSite(ctx, pool, b.ID)
		if err != nil {
			return nil, nil, err
		}
		if operations == nil && l.SiteID == nil {
			reasons = append(reasons, mtoReason{l.LineID, reasonNoSite, "The bill of materials has no operations and the sales order names no site."})
			continue
		}
		v := orderFields{quantity: roundPlaces(quantity, quantityPlaces), priority: priorityNormal}
		if l.DeliveryDate != nil {
			v.dueDate = dateValue(*l.DeliveryDate)
		}
		if operations == nil {
			v.siteID = l.SiteID
		}
		if _, err := s.judgePlace(ctx, b.ID, &v); err != nil {
			if _, p, ok := answerOf(err); ok {
				reasons = append(reasons, mtoReason{l.LineID, deref(p.Code), deref(p.Detail)})
				continue
			}
			return nil, nil, err
		}
		plan = append(plan, mtoLine{line: l, bom: b, fields: v})
	}
	return plan, reasons, nil
}

// errMtoTaken is a request another worker finished while this one judged it.
var errMtoTaken = errors.New("manufacturing: the make-to-order request is no longer pending")

// makeMto makes the planned lines' orders in one transaction under the
// make-to-order lease (M44a): LockVariants on every product and component,
// the request read again, then per line, on a savepoint, the order created
// and confirmed — the snapshot, the work orders and the reserve, where
// shortage never refuses (M20a) — and the request finished. A line Inventory
// or the catalog refuses is rolled back to its savepoint and recorded.
func (s *server) makeMto(ctx context.Context, requestID int64, plan []mtoLine, reasons []mtoReason) error {
	// The day the ledger judges a lot's expiry on, read on the pool before
	// the transaction (rule 24).
	today, err := s.stockDay(ctx)
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		var ids []int32
		for _, p := range plan {
			components, err := bomVariants(ctx, store.New(s.deps.Pool), p.bom.ID)
			if err != nil {
				return err
			}
			ids = append(ids, lockedVariants(p.line.VariantID, components)...)
		}
		ids = slices.Compact(slices.Sorted(slices.Values(ids)))
		err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			if err := txq.LockMto(ctx, mtoLeaseKey); err != nil {
				return err
			}
			locked, err := s.lockVariants(ctx, tx, ids)
			if err != nil {
				return err
			}
			r, err := txq.GetMtoRequest(ctx, requestID)
			if err != nil {
				return err
			}
			if r.Status != mtoPending {
				return errMtoTaken
			}
			lineIDs := make([]int64, 0, len(plan))
			for _, p := range plan {
				lineIDs = append(lineIDs, p.line.LineID)
			}
			live, err := txq.LinesWithLiveOrder(ctx, lineIDs)
			if err != nil {
				return err
			}
			notes := newNotices()
			made := 0
			out := slices.Clone(reasons)
			for _, p := range plan {
				reason, err := s.makeMtoLine(ctx, tx, txq, p, ids, locked, live, today, notes)
				if err != nil {
					return err
				}
				if reason != nil {
					out = append(out, *reason)
					continue
				}
				made++
			}
			status := mtoDone
			if made == 0 {
				status = mtoSkipped
			}
			if _, err := finishMto(ctx, txq, requestID, status, out, s.deps.Clock()); err != nil {
				return err
			}
			return notes.send(ctx, tx)
		})
		switch {
		case errors.Is(err, errRelock) && attempt < maxLockAttempts:
			continue
		case errors.Is(err, errRelock):
			return relockExhausted()
		case errors.Is(err, errMtoTaken):
			return nil
		}
		return err
	}
}

// makeMtoLine makes one line's order on a savepoint of tx, answering why it
// did not when a rule of this module or Inventory refused it.
func (s *server) makeMtoLine(ctx context.Context, tx pgx.Tx, txq *store.Queries, p mtoLine, ids []int32,
	locked map[int32]contracts.VariantEntry, live []int64, today time.Time, notes *notices) (*mtoReason, error) {
	l := p.line
	if slices.Contains(live, l.LineID) {
		return &mtoReason{l.LineID, reasonHasOrder, "A manufacturing order is already made for the line."}, nil
	}
	if e, ok := locked[l.VariantID]; !ok || e.ProductType != goodsType {
		return &mtoReason{l.LineID, reasonVariantNotGoods, "The line's variant is not goods."}, nil
	}
	bom, err := txq.GetBom(ctx, p.bom.ID)
	if err != nil {
		return nil, err
	}
	if bom.Status != statusActive || !bom.MakeToOrder {
		return &mtoReason{l.LineID, reasonNotMakeToOrder, "The bill of materials changed and is no longer made to order."}, nil
	}
	now, err := bomVariants(ctx, txq, bom.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range now {
		if !slices.Contains(ids, c) {
			return nil, errRelock
		}
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sp.Rollback(context.WithoutCancel(ctx)) }()
	spq := store.New(sp)
	reference := salesOrderReference(l.OrderNumber)
	o, err := s.insertOrder(ctx, spq, p.bom.ID, l.VariantID, p.fields, originSalesOrder, &l.OrderID, &l.LineID, &reference, nil, systemActor)
	if err != nil {
		// An order made for the line by hand since the read under the lease
		// (the new-order form takes no lease): ux_orders_origin_line refuses
		// this one, and the line is left out, the savepoint rolled back.
		if _, prob, ok := answerOf(err); ok {
			return &mtoReason{l.LineID, deref(prob.Code), deref(prob.Detail)}, nil
		}
		return nil, err
	}
	if _, err := s.confirmOrder(ctx, sp, spq, o, locked, systemActor, today, notes); err != nil {
		if _, prob, ok := answerOf(err); ok {
			return &mtoReason{l.LineID, deref(prob.Code), deref(prob.Detail)}, nil
		}
		return nil, err
	}
	if err := sp.Commit(ctx); err != nil {
		return nil, err
	}
	notes.orders[o.ID] = true
	return nil, nil
}

// insertOrder inserts a draft numbered MO-<n> with its created event: the
// one insert of a new order, by hand, for a sales order line or under a
// parent.
func (s *server) insertOrder(ctx context.Context, txq *store.Queries, bomID int64, variantID int32, v orderFields, origin string,
	originOrderID, originLineID *int64, reference *string, parentID *int64, who actor) (store.ManufacturingOrder, error) {
	number, err := txq.NextOrderNumber(ctx)
	if err != nil {
		return store.ManufacturingOrder{}, err
	}
	at := s.deps.Clock()
	o, err := txq.InsertOrder(ctx, store.InsertOrderParams{
		Number: number, VariantID: variantID, BomID: bomID, Quantity: numeric(v.quantity, quantityPlaces),
		SiteID: *v.siteID, ComponentLocationID: v.componentLocationID, OutputLocationID: *v.outputLocationID,
		DueDate: v.dueDate, RequestedStart: v.requestedStart,
		Priority: v.priority, ResponsibleUserID: v.responsible, Note: v.note, OriginKind: origin,
		OriginOrderID: originOrderID, OriginLineID: originLineID, OriginReference: reference, OriginCancelled: false,
		ParentOrderID: parentID, Now: at,
	})
	if err != nil {
		if isOriginTaken(err) {
			return o, conflict(codeSalesOrderLineHasOrder, "The sales order line already has an order",
				"A manufacturing order is already made for this sales order line. Open it, or cancel it first.")
		}
		return o, err
	}
	payload := map[string]any{"number": orderNumber(o.Number, o.Suffix), "quantity": text(v.quantity, quantityPlaces)}
	if reference != nil {
		payload["origin"] = *reference
	}
	if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindCreated, who, at, payload); err != nil {
		return o, err
	}
	return o, nil
}

// isOriginTaken reports the unique index of one live order per sales order
// line (M44b).
func isOriginTaken(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "ux_orders_origin_line"
}

// newProduction builds contracts.Production over d (M46, M46a).
func newProduction(d module.Deps) contracts.Production { return production{deps: d} }

// production is contracts.Production: provider-gated on
// manufacturing:access from ctx, answering nothing without it.
type production struct{ deps module.Deps }

func (p production) allowed(ctx context.Context) bool {
	return p.deps.Access != nil && contracts.HasPermission(ctx, p.deps.Access, permAccess)
}

// ForOrderLines is every order made for the lines, the backorders too.
func (p production) ForOrderLines(ctx context.Context, lineIDs []int64) ([]contracts.LineProduction, error) {
	if len(lineIDs) > contracts.MaxProductionLines {
		return nil, fmt.Errorf("manufacturing: a production read names %d lines, more than %d", len(lineIDs), contracts.MaxProductionLines)
	}
	if len(lineIDs) == 0 || !p.allowed(ctx) {
		return nil, nil
	}
	rows, err := store.New(p.deps.Pool).OrdersForLines(ctx, lineIDs)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the orders made for sales order lines: %w", err)
	}
	out := make([]contracts.LineProduction, 0, len(rows))
	for _, o := range rows {
		if o.OriginLineID == nil {
			continue
		}
		out = append(out, contracts.LineProduction{
			LineID: *o.OriginLineID, OrderID: o.ID, Number: orderNumber(o.Number, o.Suffix), Status: o.Status,
			Quantity: numericText(o.Quantity, quantityPlaces), Produced: numericText(o.Produced, quantityPlaces),
			SiteID: o.SiteID, PlannedFinish: o.PlannedFinish, DueDate: dateOf(o.DueDate),
		})
	}
	return out, nil
}

// HasBOM is whether each variant has a default active BoM.
func (p production) HasBOM(ctx context.Context, variantIDs []int32) (map[int32]bool, error) {
	if len(variantIDs) > contracts.MaxProductionLines {
		return nil, fmt.Errorf("manufacturing: a production read names %d variants, more than %d", len(variantIDs), contracts.MaxProductionLines)
	}
	out := map[int32]bool{}
	if len(variantIDs) == 0 || !p.allowed(ctx) {
		return out, nil
	}
	rows, err := store.New(p.deps.Pool).DefaultActiveBomsOfVariants(ctx, variantIDs)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the default BoMs: %w", err)
	}
	for _, r := range rows {
		out[r.VariantID] = true
	}
	return out, nil
}

// NotMade is why make-to-order made no order for lines of one sales order
// (M44a): the lines its request left out, but those that were never this
// module's to make — no BoM, or one not made to order — and those that have
// an order; the whole sales order when the request failed for good. Pending,
// cancelled and request-level skips answer none.
func (p production) NotMade(ctx context.Context, salesOrderID int64) ([]contracts.LineNotMade, error) {
	if !p.allowed(ctx) {
		return nil, nil
	}
	r, err := store.New(p.deps.Pool).MtoRequestOfSalesOrder(ctx, salesOrderID)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read sales order %d's make-to-order request: %w", salesOrderID, err)
	}
	switch r.Status {
	case mtoFailed:
		return []contracts.LineNotMade{{Code: contracts.NotMadeFailed,
			Detail: "Make-to-order failed for the sales order after its last attempt."}}, nil
	case mtoDone, mtoSkipped:
	default:
		return nil, nil
	}
	var reasons []mtoReason
	if err := json.Unmarshal(r.Reasons, &reasons); err != nil {
		return nil, fmt.Errorf("manufacturing: read request %d's reasons: %w", r.ID, err)
	}
	var out []contracts.LineNotMade
	for _, x := range reasons {
		if x.LineID == 0 || x.Code == reasonNoBom || x.Code == reasonNotMakeToOrder || x.Code == reasonHasOrder {
			continue
		}
		out = append(out, contracts.LineNotMade{LineID: x.LineID, Code: x.Code, Detail: x.Detail})
	}
	return out, nil
}

// publishDone publishes manufacturing.order.done for an order made for a
// sales order line, on the closing transaction, while it holds the order's
// row (M48, rule 23).
func publishDone(ctx context.Context, tx pgx.Tx, o store.ManufacturingOrder, at time.Time) error {
	if o.OriginKind != originSalesOrder || o.OriginOrderID == nil || o.OriginLineID == nil {
		return nil
	}
	return events.Publish(ctx, tx, moduleName, contracts.ManufacturingOrderDone{
		ManufacturingOrderID: o.ID, SalesOrderID: *o.OriginOrderID, SalesOrderLineID: *o.OriginLineID,
		SiteID: o.SiteID, Produced: numericText(o.Produced, quantityPlaces), ClosedAt: at,
	}, at)
}

// GetManufacturingSalesOrderLine is the sales order line the new-order form
// is prefilled from (M45): read through SalesOrderLines, which the route's
// orders-manage has already allowed (M43a), with the variant's default BoM
// and the orders already made for it.
func (s *server) GetManufacturingSalesOrderLine(ctx context.Context, req gen.GetManufacturingSalesOrderLineRequestObject) (gen.GetManufacturingSalesOrderLineResponseObject, error) {
	answers := answers[gen.GetManufacturingSalesOrderLineResponseObject]{
		notFound: func() gen.GetManufacturingSalesOrderLineResponseObject {
			return gen.GetManufacturingSalesOrderLine404Response{}
		},
		unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingSalesOrderLineResponseObject {
			return gen.GetManufacturingSalesOrderLine503ApplicationProblemPlusJSONResponse(p)
		},
	}
	l, err := s.salesOrderLine(ctx, req.Id)
	if err != nil {
		return answer(err, answers)
	}
	if l == nil {
		return gen.GetManufacturingSalesOrderLine404Response{}, nil
	}
	pool := store.New(s.deps.Pool)
	boms, err := pool.DefaultActiveBomsOfVariants(ctx, []int32{l.VariantID})
	if err != nil {
		return nil, err
	}
	made, err := pool.OrdersForLines(ctx, []int64{l.LineID})
	if err != nil {
		return nil, err
	}
	entries, err := s.catalogVariants(ctx, []int32{l.VariantID})
	if err != nil {
		return answer(err, answers)
	}
	out := gen.ManufacturingSalesOrderLine{
		LineId: l.LineID, SalesOrderId: l.OrderID, Reference: salesOrderReference(l.OrderNumber),
		Variant: refOf(entries, l.VariantID, s.has(ctx, permCostsView)), Quantity: l.Quantity, SiteId: l.SiteID,
		Orders: make([]gen.ManufacturingOrderLink, 0, len(made)),
	}
	if l.DeliveryDate != nil {
		out.DueDate = &openapi_types.Date{Time: *l.DeliveryDate}
	}
	if len(boms) > 0 {
		out.BomId = &boms[0].ID
	}
	for _, o := range made {
		out.Orders = append(out.Orders, linkOf(o))
	}
	return gen.GetManufacturingSalesOrderLine200JSONResponse(out), nil
}

// judgeSalesOrderLine is a new order's sales order line (M45): a goods line
// of a sales order that is not cancelled, of the order's variant
// (sales_order_line_unknown otherwise), read through SalesOrderLines before
// the transaction.
func (s *server) judgeSalesOrderLine(ctx context.Context, lineID int64, variantID int32) (contracts.SalesOrderLine, error) {
	l, err := s.salesOrderLine(ctx, lineID)
	if err != nil {
		return contracts.SalesOrderLine{}, err
	}
	if l == nil || l.OrderStatus == "cancelled" || l.VariantID != variantID {
		return contracts.SalesOrderLine{}, refuseField(orderTitle, "salesOrderLineId", codeSalesOrderLineUnknown,
			"Not a goods line of a sales order that is not cancelled, of this variant")
	}
	return *l, nil
}

// childrenAnswers are the sub-assembly create's typed refusals.
func childrenAnswers() answers[gen.CreateManufacturingChildOrdersResponseObject] {
	return answers[gen.CreateManufacturingChildOrdersResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CreateManufacturingChildOrdersResponseObject {
			return gen.CreateManufacturingChildOrders400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.CreateManufacturingChildOrdersResponseObject {
			return gen.CreateManufacturingChildOrders404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.CreateManufacturingChildOrdersResponseObject {
			return gen.CreateManufacturingChildOrders409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CreateManufacturingChildOrdersResponseObject {
			return gen.CreateManufacturingChildOrders503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// childPlan is one sub-assembly order to make, judged before the
// transaction: missing is the component line's missing quantity (M21a), of
// which what the parent's open sub-assembly orders still make is taken off
// again under the parent's lock.
type childPlan struct {
	field       string
	componentID int64
	variantID   int32
	bomID       int64
	missing     *big.Rat
	fields      orderFields
}

// CreateManufacturingChildOrders makes one confirmed child order per chosen
// component line (M47): its variant's default active BoM, the line's missing
// quantity (M21a) less what the parent's open sub-assembly orders of the
// variant still make — more than 0, nothing_missing otherwise —, origin
// parent, at the BoM's work centers' site, else the parent's, due the
// parent's planned start, else its requested start (M37). The parent's lock
// order holds (M26a): LockVariants on the parent's and every child's
// variants, the parent FOR UPDATE, then the open children read again, the
// children made and confirmed, reserving last.
func (s *server) CreateManufacturingChildOrders(ctx context.Context, req gen.CreateManufacturingChildOrdersRequestObject) (gen.CreateManufacturingChildOrdersResponseObject, error) {
	in := req.Body
	r, err := s.readOrder(ctx, req.Id)
	if err != nil {
		return answer(err, childrenAnswers())
	}
	parent := r.order
	if !isOpen(parent.Status) {
		return answer(orderNotOpen(parent.Status), childrenAnswers())
	}
	if err := checkRevision(parent, in.Revision); err != nil {
		return answer(err, childrenAnswers())
	}
	due, err := s.childDueDate(ctx, parent, r.workOrders)
	if err != nil {
		return answer(err, childrenAnswers())
	}
	missing, boms, err := s.childMissing(ctx, parent, r.components)
	if err != nil {
		return answer(err, childrenAnswers())
	}
	pool := store.New(s.deps.Pool)
	open, err := openChildren(ctx, pool, parent.ID)
	if err != nil {
		return nil, err
	}
	var plans []childPlan
	var f fieldErrors
	for i, id := range slices.Compact(slices.Sorted(slices.Values(in.ComponentIds))) {
		field := fmt.Sprintf("componentIds[%d]", i)
		index := slices.IndexFunc(r.components, func(c store.ManufacturingOrderComponent) bool { return c.ID == id })
		if index < 0 {
			f.addCoded(field, codeComponentUnknown, "Not a component line of the order")
			continue
		}
		c := r.components[index]
		bomID, ok := boms[c.VariantID]
		if !ok {
			f.addCoded(field, codeBomRequired, "The component has no active bill of materials")
			continue
		}
		short := missing[index]
		if new(big.Rat).Sub(short, openOf(open, c.VariantID)).Sign() <= 0 {
			f.addCoded(field, codeNothingMissing, "The component misses nothing its open sub-assembly orders do not make")
			continue
		}
		v := orderFields{priority: parent.Priority, dueDate: due}
		operations, err := bomSite(ctx, pool, bomID)
		if err != nil {
			return nil, err
		}
		if operations == nil {
			site := parent.SiteID
			v.siteID = &site
		}
		v.quantity = new(big.Rat).Sub(short, openOf(open, c.VariantID))
		if _, err := s.judgePlace(ctx, bomID, &v); err != nil {
			return answer(err, childrenAnswers())
		}
		plans = append(plans, childPlan{field: field, componentID: id, variantID: c.VariantID, bomID: bomID, missing: short, fields: v})
	}
	if f.failed() {
		return answer(refuseInvalid("The sub-assembly orders were not made", f), childrenAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	// The day the ledger judges a lot's expiry on, read on the pool (rule 24).
	today, err := s.stockDay(ctx)
	if err != nil {
		return nil, err
	}
	var made []store.ManufacturingOrder
	err = s.withOrder(ctx, req.Id, func(ctx context.Context, q *store.Queries, _ store.ManufacturingOrder) ([]int32, error) {
		var ids []int32
		for _, p := range plans {
			components, err := bomVariants(ctx, q, p.bomID)
			if err != nil {
				return nil, err
			}
			ids = append(ids, p.variantID)
			ids = append(ids, components...)
		}
		return ids, nil
	}, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		if err := checkRevision(o, in.Revision); err != nil {
			return err
		}
		if !isOpen(o.Status) {
			return orderNotOpen(o.Status)
		}
		// The open children again, under the parent's lock every
		// sub-assembly create takes: two creates at once never both make
		// what the line misses.
		open, err := openChildren(ctx, txq, o.ID)
		if err != nil {
			return err
		}
		var f fieldErrors
		for _, p := range plans {
			if new(big.Rat).Sub(p.missing, openOf(open, p.variantID)).Sign() <= 0 {
				f.addCoded(p.field, codeNothingMissing, "The component misses nothing its open sub-assembly orders do not make")
			}
		}
		if f.failed() {
			return refuseInvalid("The sub-assembly orders were not made", f)
		}
		notes := newNotices()
		made = made[:0]
		for _, p := range plans {
			b, err := txq.GetBom(ctx, p.bomID)
			if err != nil {
				return err
			}
			if b.Status != statusActive {
				return notActive()
			}
			fields := p.fields
			fields.quantity = new(big.Rat).Sub(p.missing, openOf(open, p.variantID))
			child, err := s.insertOrder(ctx, txq, p.bomID, p.variantID, fields, originParent, &o.ID, nil, nil, &o.ID, who)
			if err != nil {
				return err
			}
			if _, err := s.confirmOrder(ctx, tx, txq, child, lo.locked, who, today, notes); err != nil {
				return err
			}
			notes.orders[child.ID] = true
			made = append(made, child)
		}
		numbers := make([]string, 0, len(made))
		for _, c := range made {
			numbers = append(numbers, orderNumber(c.Number, c.Suffix))
		}
		if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindChildrenCreated, who, s.deps.Clock(), map[string]any{"children": numbers}); err != nil {
			return err
		}
		if _, err := txq.TouchOrder(ctx, store.TouchOrderParams{ID: o.ID, Now: s.deps.Clock()}); err != nil {
			return err
		}
		notes.orders[o.ID] = true
		return notes.send(ctx, tx)
	})
	if err != nil {
		return answer(err, childrenAnswers())
	}
	out := gen.CreateManufacturingChildOrders200JSONResponse{Children: make([]gen.ManufacturingOrderLink, 0, len(made))}
	for _, c := range made {
		fresh, err := pool.GetOrder(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, linkOf(fresh))
	}
	if out.Order, err = s.orderAnswer(ctx, req.Id); err != nil {
		return answer(err, childrenAnswers())
	}
	return out, nil
}

// childMissing is what each of the parent's component lines misses at the
// parent's site (M21a), by line, and each component variant's default
// active BoM, read before the transaction: the site's available stock
// through contracts.Stock, the BoMs on the pool.
func (s *server) childMissing(ctx context.Context, parent store.ManufacturingOrder, components []store.ManufacturingOrderComponent) ([]*big.Rat, map[int32]int64, error) {
	ids := make([]int32, 0, len(components))
	for _, c := range components {
		ids = append(ids, c.VariantID)
	}
	var available map[int32]contracts.StockQuantity
	if len(ids) > 0 {
		site := parent.SiteID
		var err error
		if available, err = s.stockAvailable(ctx, slices.Compact(slices.Sorted(slices.Values(ids))), &site); err != nil {
			return nil, nil, err
		}
	}
	boms, err := s.componentBoms(ctx, components)
	if err != nil {
		return nil, nil, err
	}
	return coverageOf(components, available).missing, boms, nil
}

// openChildren is, per variant, what the parent's sub-assembly orders not
// done or cancelled still make (M47).
func openChildren(ctx context.Context, q *store.Queries, parentID int64) (map[int32]*big.Rat, error) {
	rows, err := q.OpenChildrenOfOrder(ctx, &parentID)
	if err != nil {
		return nil, err
	}
	out := map[int32]*big.Rat{}
	for _, r := range rows {
		out[r.VariantID] = ratOrZero(r.Remaining)
	}
	return out, nil
}

// openOf is what open children of variant still make, 0 for none.
func openOf(open map[int32]*big.Rat, variant int32) *big.Rat {
	if r, ok := open[variant]; ok {
		return r
	}
	return new(big.Rat)
}

// childDueDate is M37: the parent's planned start, as a day of the business
// zone, else its requested start, else the requested start planning would
// take.
func (s *server) childDueDate(ctx context.Context, parent store.ManufacturingOrder, workOrders []store.ManufacturingWorkOrder) (pgtype.Date, error) {
	switch {
	case parent.PlannedStart != nil:
		zone, err := s.businessZone(ctx)
		if err != nil {
			return pgtype.Date{}, err
		}
		local := parent.PlannedStart.In(zone)
		return dateValue(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)), nil
	case parent.RequestedStart.Valid:
		return parent.RequestedStart, nil
	}
	today, _, err := s.today(ctx)
	if err != nil {
		return pgtype.Date{}, err
	}
	return dateValue(defaultRequestedStart(dateOf(parent.DueDate), expectedOf(openWork(workOrders)), today)), nil
}

// withOrigin adds to an order's answer what its origin needs read out of
// the module, after any transaction (M43a): the sales order's customer, to a
// caller who also holds orders:access. A read that fails is logged and the
// name left out.
func (s *server) withOrigin(ctx context.Context, out *gen.ManufacturingOrder) {
	if out.Origin.Kind != gen.ManufacturingOriginSalesOrder || out.Origin.LineId == nil || s.deps.SalesOrderLines == nil ||
		s.deps.Directory == nil || !s.has(ctx, "orders:access") {
		return
	}
	l, err := s.salesOrderLine(ctx, *out.Origin.LineId)
	var name string
	if err == nil && l != nil {
		name, err = s.customerName(ctx, l.CustomerID)
	}
	// The customer's name is a courtesy of the answer: a read that fails
	// leaves it out, logged, rather than failing the order's answer.
	if err != nil {
		s.logger().WarnContext(ctx, "manufacturing: the sales order's customer could not be read", "order", out.Id, "error", err.Error())
		return
	}
	if name != "" {
		out.Origin.CustomerName = &name
	}
}

// deref is *p, "" for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
