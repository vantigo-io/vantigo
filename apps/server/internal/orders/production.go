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

// newSalesOrderLines builds contracts.SalesOrderLines over d's pool (M43).
func newSalesOrderLines(d module.Deps) contracts.SalesOrderLines {
	return salesOrderLines{pool: d.Pool}
}

// salesOrderLines is contracts.SalesOrderLines: consumer-gated (M43a), so
// it authorises nothing, and it names the customer by id only.
type salesOrderLines struct{ pool *pgxpool.Pool }

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
// for them and whether it can (M45), and to every line its produced, ready to
// reserve mark (M48a), after the order's transaction. Production is
// provider-gated (M46a): it answers nothing to a caller without
// manufacturing:access, and Orders checks nothing itself.
func (s *server) withProduction(ctx context.Context, out *gen.OrdersOrder) error {
	marks, err := store.New(s.deps.Pool).ReadyLinesOfOrder(ctx, out.Id)
	if err != nil {
		return fmt.Errorf("orders: read the produced lines: %w", err)
	}
	ready := map[int64]store.OrdersLineProductionReady{}
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
	for i := range out.Lines {
		l := &out.Lines[i]
		if m, ok := ready[l.Id]; ok {
			l.ReadyToReserve = &gen.OrdersLineReady{ManufacturingOrderId: m.ManufacturingOrderID, SiteId: m.SiteID,
				Produced: wire(ratOrZero(m.Produced), quantityPlaces), OtherSite: out.SiteId == nil || *out.SiteId != m.SiteID}
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
