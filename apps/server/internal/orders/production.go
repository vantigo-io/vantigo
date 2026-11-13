package orders

// Orders' side of make-to-order (manufacturing design M43, M43a, M45, M46a,
// M48, M48a): contracts.SalesOrderLines, the read Manufacturing makes its
// orders from; the orders.mto-reserve handler, which marks a line produced,
// ready to reserve when an order made for it closes; and the production an
// order's answer shows per goods line, read through contracts.Production.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// mtoReserveHandler marks a line produced, ready to reserve (M48a).
const mtoReserveHandler = "orders.mto-reserve"

// newSalesOrderLines builds contracts.SalesOrderLines over d's pool (M43),
// with the stock ledger Unreserved reads what each line holds reserved by.
func newSalesOrderLines(d module.Deps) contracts.SalesOrderLines {
	return salesOrderLines{pool: d.Pool, ledger: d.StockLedger}
}

// salesOrderLines is contracts.SalesOrderLines: consumer-gated (M43a), so
// it authorises nothing, and it names the customer by id only.
type salesOrderLines struct {
	pool   *pgxpool.Pool
	ledger contracts.StockLedger
}

// Unreserved is what the open goods lines of confirmed sales orders at the
// site still need beyond what they hold reserved there (manufacturing
// phase 2 M107a): the lines and their orders' reservations read in one
// read-only snapshot — what a line holds is Inventory's to say
// (ReservationsForSources, lock-free, on this transaction, the one connection
// it holds, one round trip per MaxStockSources orders) — and only a line with
// more open than reserved answered.
func (p salesOrderLines) Unreserved(ctx context.Context, variantIDs []int32, siteID int64) ([]contracts.UnreservedDemand, error) {
	if len(variantIDs) > contracts.MaxStockPlacements {
		return nil, fmt.Errorf("orders: an unreserved demand read names %d variants, more than %d", len(variantIDs), contracts.MaxStockPlacements)
	}
	if len(variantIDs) == 0 {
		return nil, nil
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("orders: begin the unreserved demand read: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	rows, err := store.New(tx).OpenGoodsLinesAt(ctx, store.OpenGoodsLinesAtParams{VariantIds: variantIDs, SiteID: siteID})
	if err != nil {
		return nil, fmt.Errorf("orders: read the open lines at site %d: %w", siteID, err)
	}
	held := map[int64]*big.Rat{}
	if p.ledger != nil {
		var sources []contracts.StockSource
		for _, r := range rows {
			if n := len(sources); n == 0 || sources[n-1].ID != r.OrderID {
				sources = append(sources, orderStock(r.OrderID))
			}
		}
		for batch := range slices.Chunk(sources, contracts.MaxStockSources) {
			reservations, err := p.ledger.ReservationsForSources(ctx, tx, batch)
			if err != nil {
				return nil, fmt.Errorf("orders: read %d sales orders' reservations: %w", len(batch), err)
			}
			for _, res := range reservations {
				q, ok := new(big.Rat).SetString(res.Quantity)
				if !ok || res.SiteID != siteID {
					continue
				}
				if held[res.LineID] == nil {
					held[res.LineID] = new(big.Rat)
				}
				held[res.LineID].Add(held[res.LineID], q)
			}
		}
	}
	out := make([]contracts.UnreservedDemand, 0, len(rows))
	for _, r := range rows {
		open := ratOrZero(r.OpenQuantity)
		if h := held[r.ID]; h != nil {
			open.Sub(open, h)
		}
		if open.Sign() <= 0 {
			continue
		}
		out = append(out, contracts.UnreservedDemand{OrderID: r.OrderID, LineID: r.ID, VariantID: r.VariantID,
			Quantity: decimalText(open, quantityPlaces)})
	}
	return out, nil
}

// Lines is the sales order lines named by ids.
func (p salesOrderLines) Lines(ctx context.Context, ids []int64) ([]contracts.SalesOrderLine, error) {
	if len(ids) > contracts.MaxProductionLines {
		return nil, fmt.Errorf("orders: a sales order lines read names %d lines, more than %d", len(ids), contracts.MaxProductionLines)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := store.New(p.pool).SalesOrderLinesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read sales order lines: %w", err)
	}
	out := make([]contracts.SalesOrderLine, 0, len(rows))
	for _, r := range rows {
		out = append(out, salesOrderLineOf(r.ID, r.OrderID, r.Number, r.CustomerID, r.VariantID, r.OpenQuantity, r.DeliveryDate, r.SiteID, r.Status))
	}
	return out, nil
}

// OrderLines is every goods line of one sales order.
func (p salesOrderLines) OrderLines(ctx context.Context, orderID int64) ([]contracts.SalesOrderLine, error) {
	rows, err := store.New(p.pool).SalesOrderLinesOfOrder(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: read sales order %d's lines: %w", orderID, err)
	}
	out := make([]contracts.SalesOrderLine, 0, len(rows))
	for _, r := range rows {
		out = append(out, salesOrderLineOf(r.ID, r.OrderID, r.Number, r.CustomerID, r.VariantID, r.OpenQuantity, r.DeliveryDate, r.SiteID, r.Status))
	}
	return out, nil
}

func salesOrderLineOf(id, orderID, number int64, customerID, variantID int32, open pgtype.Numeric, delivery pgtype.Date, siteID *int64, status string) contracts.SalesOrderLine {
	l := contracts.SalesOrderLine{LineID: id, OrderID: orderID, OrderNumber: number, CustomerID: customerID, VariantID: variantID,
		Quantity: decimalText(ratOrZero(open), quantityPlaces), SiteID: siteID, OrderStatus: status}
	if delivery.Valid {
		d := delivery.Time
		l.DeliveryDate = &d
	}
	return l
}

// decimalText is v as exact decimal text with at most places decimals and
// no trailing zeros, as the contracts carry quantities.
func decimalText(v *big.Rat, places int) string {
	s := v.FloatString(places)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// mtoReserve is orders.mto-reserve's handler (M48a): on
// manufacturing.order.done it marks the sales order line produced, ready to
// reserve, in this module's own schema and nothing else (rule 23); the order
// page then offers Reserve now on it. A line that is no longer an order's is
// skipped.
func mtoReserve(ctx context.Context, tx pgx.Tx, ev events.Delivered, clock func() time.Time, logger *slog.Logger) error {
	var e contracts.ManufacturingOrderDone
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return fmt.Errorf("orders: decode event %d: %w", ev.ID, err)
	}
	q := store.New(tx)
	orderID, err := q.OrderOfLine(ctx, e.SalesOrderLineID)
	if noRows(err) {
		logger.DebugContext(ctx, "orders: produced goods for a line that is no longer an order's", "event", ev.ID, "line", e.SalesOrderLineID)
		return nil
	}
	if err != nil {
		return err
	}
	produced, ok := new(big.Rat).SetString(e.Produced)
	if !ok {
		return fmt.Errorf("orders: event %d's produced %q is not a decimal", ev.ID, e.Produced)
	}
	if produced.Sign() <= 0 {
		return nil
	}
	return q.MarkLineReady(ctx, store.MarkLineReadyParams{
		LineID: e.SalesOrderLineID, OrderID: orderID, ManufacturingOrderID: e.ManufacturingOrderID, SiteID: e.SiteID,
		Produced: numeric(produced, quantityPlaces), Now: clock(),
	})
}

// withProduction adds to an order's goods lines what Manufacturing makes
// for them and whether it can (M45), why make-to-order made nothing for a
// line that has no manufacturing order (M44a), and to every line its
// produced, ready to reserve mark (M48a), after the order's transaction.
// Production is provider-gated (M46a): it answers nothing to a caller
// without manufacturing:access, and Orders checks nothing itself but the
// mark's manufacturing order id, which it names to such a caller only.
func (s *server) withProduction(ctx context.Context, out *gen.OrdersOrder) error {
	marks, err := store.New(s.deps.Pool).ReadyLinesOfOrder(ctx, out.Id)
	if err != nil {
		return fmt.Errorf("orders: read the produced lines: %w", err)
	}
	ready := map[int64]store.ReadyLinesOfOrderRow{}
	for _, m := range marks {
		ready[m.LineID] = m
	}
	var lineIDs []int64
	var variantIDs []int32
	for _, l := range out.Lines {
		if l.Kind == gen.OrdersLineKindProduct && l.VariantId != nil {
			lineIDs = append(lineIDs, l.Id)
			if !slices.Contains(variantIDs, *l.VariantId) {
				variantIDs = append(variantIDs, *l.VariantId)
			}
		}
	}
	made, hasBom, err := s.productionOf(ctx, lineIDs, variantIDs)
	if err != nil {
		return err
	}
	notMade, err := s.notMadeOf(ctx, out.Id, made)
	if err != nil {
		return err
	}
	seesManufacturing := s.has(ctx, "manufacturing:access")
	for i := range out.Lines {
		l := &out.Lines[i]
		if m, ok := ready[l.Id]; ok {
			l.ReadyToReserve = &gen.OrdersLineReady{SiteId: m.SiteID,
				Produced: wire(ratOrZero(m.Produced), quantityPlaces), OtherSite: out.SiteId == nil || *out.SiteId != m.SiteID}
			if seesManufacturing {
				id := m.ManufacturingOrderID
				l.ReadyToReserve.ManufacturingOrderId = &id
			}
		}
		if made == nil || l.Kind != gen.OrdersLineKindProduct || l.VariantId == nil {
			continue
		}
		has := hasBom[*l.VariantId]
		l.HasBom = &has
		lines := []gen.OrdersLineProduction{}
		for _, p := range made {
			if p.LineID != l.Id {
				continue
			}
			lp := gen.OrdersLineProduction{ManufacturingOrderId: p.OrderID, Number: p.Number, Status: p.Status, SiteId: p.SiteID,
				PlannedFinish: p.PlannedFinish, Quantity: wireText(p.Quantity), Produced: wireText(p.Produced)}
			if p.DueDate != nil {
				lp.DueDate = &openapi_types.Date{Time: *p.DueDate}
			}
			lines = append(lines, lp)
		}
		l.Production = &lines
		if len(lines) == 0 {
			l.NotMade = notMadeFor(notMade, l.Id, has)
		}
	}
	return nil
}

// notMadeOf is why make-to-order made no order for the sales order's lines,
// read only when the caller sees Manufacturing's production (made non-nil).
func (s *server) notMadeOf(ctx context.Context, orderID int64, made []contracts.LineProduction) ([]contracts.LineNotMade, error) {
	if made == nil {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Production.NotMade"); err != nil {
		return nil, err
	}
	notMade, err := s.deps.Production.NotMade(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("orders: read why Manufacturing made nothing for the lines: %w", err)
	}
	return notMade, nil
}

// notMadeFor is the reason a line with no manufacturing order shows: its
// own, else — for a line whose variant has a BoM — the whole order's, when
// the request failed for good.
func notMadeFor(notMade []contracts.LineNotMade, lineID int64, hasBom bool) *gen.OrdersLineNotMade {
	for _, n := range notMade {
		if n.LineID == lineID {
			return &gen.OrdersLineNotMade{Code: n.Code, Detail: n.Detail}
		}
	}
	for _, n := range notMade {
		if n.LineID == 0 && hasBom {
			return &gen.OrdersLineNotMade{Code: n.Code, Detail: n.Detail}
		}
	}
	return nil
}

// wireText is exact decimal text as the API's number.
func wireText(v string) float64 {
	r, ok := new(big.Rat).SetString(v)
	if !ok {
		return 0
	}
	return wire(r, quantityPlaces)
}

// productionOf is Manufacturing's read of the lines and the variants, on
// the pool with the request's context, never inside a transaction (M46a).
// made is nil when Manufacturing is off or the caller may not see it: the
// answer then carries no production at all.
func (s *server) productionOf(ctx context.Context, lineIDs []int64, variantIDs []int32) ([]contracts.LineProduction, map[int32]bool, error) {
	if s.deps.Production == nil || len(lineIDs) == 0 {
		return nil, nil, nil
	}
	if err := noteContractCall(ctx, "Production.HasBOM"); err != nil {
		return nil, nil, err
	}
	hasBom, err := s.deps.Production.HasBOM(ctx, variantIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("orders: read which variants Manufacturing makes: %w", err)
	}
	made, err := s.deps.Production.ForOrderLines(ctx, lineIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("orders: read what Manufacturing makes for the lines: %w", err)
	}
	// The provider answers nothing at all to a caller without
	// manufacturing:access: then the lines carry nothing either.
	if len(made) == 0 && !slices.ContainsFunc(variantIDs, func(id int32) bool { return hasBom[id] }) {
		return nil, nil, nil
	}
	if made == nil {
		made = []contracts.LineProduction{}
	}
	return made, hasBom, nil
}
