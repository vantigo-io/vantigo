package manufacturing

// An order's stock and lifecycle (manufacturing design M17–M20, M23, M18a,
// M20a, M23a): the snapshot at confirm, the two-pass reserve through
// StockLedger.ReserveForProduction, Manufacturing's copy of what each line
// holds, the component lines' add and edit, close with its backorder, and
// cancel. Every write runs under withOrder's lock order (M26a): the ledger is
// called last, on the same transaction. Reserve, ReserveForProduction and
// Release write nothing when they refuse, so the second pass runs in the
// same transaction (M2a).

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// shortLine is a component line left unreserved, with what it still needs.
type shortLine struct {
	component store.ManufacturingOrderComponent
	missing   *big.Rat
}

// ledger is the stock ledger, or the 503 when it is not composed.
func (s *server) ledger() (contracts.StockLedger, error) {
	if s.deps.StockLedger == nil {
		return nil, errDependencyUnavailable
	}
	return s.deps.StockLedger, nil
}

// stockRefusal is Inventory's refusal as this module's 409, with its code
// and each failing line named by its component line (Rounding and codes:
// Inventory's refusals are passed through). A line is a component line's
// only when both its variant and its line id are that line's: a production's
// produce line carries the order's id as its line, which may equal a
// component line's id, and names no component.
func stockRefusal(r *contracts.StockRefusal, components []store.ManufacturingOrderComponent) error {
	p := problem(http.StatusConflict, r.Code, "Inventory refused", r.Detail+" Nothing was changed.")
	if len(r.Lines) > 0 {
		lines := make([]gen.ManufacturingProblemLine, 0, len(r.Lines))
		for _, l := range r.Lines {
			index := slices.IndexFunc(components, func(c store.ManufacturingOrderComponent) bool {
				return c.ID == l.LineID && c.VariantID == l.VariantID
			})
			line := gen.ManufacturingProblemLine{Index: int32(max(index, 0)), Code: l.Code, Detail: l.Detail}
			if index >= 0 {
				id := l.LineID
				line.ComponentId = &id
			}
			lines = append(lines, line)
		}
		p.Lines = &lines
	}
	return refuse(p)
}

// heldByLine is what each of the order's lines holds reserved, from the
// ledger (M20a's held), on tx.
func heldByLine(ctx context.Context, ledger contracts.StockLedger, tx pgx.Tx, id int64) (map[int64]*big.Rat, error) {
	reservations, err := ledger.ReservationsFor(ctx, tx, orderSource(id))
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read order %d's reservations: %w", id, err)
	}
	out := map[int64]*big.Rat{}
	for _, r := range reservations {
		q, ok := new(big.Rat).SetString(r.Quantity)
		if !ok {
			return nil, fmt.Errorf("manufacturing: reservation of line %d is not a decimal: %q", r.LineID, r.Quantity)
		}
		if out[r.LineID] == nil {
			out[r.LineID] = new(big.Rat)
		}
		out[r.LineID].Add(out[r.LineID], q)
	}
	return out, nil
}

// syncReserved writes the ledger's reservations into Manufacturing's copy on
// each line (M20a), under the order's lock.
func syncReserved(ctx context.Context, ledger contracts.StockLedger, tx pgx.Tx, txq *store.Queries, id int64) error {
	held, err := heldByLine(ctx, ledger, tx, id)
	if err != nil {
		return err
	}
	components, err := txq.ListOrderComponents(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range components {
		h := held[c.ID]
		if h == nil {
			h = new(big.Rat)
		}
		if h.Cmp(ratOrZero(c.Reserved)) != 0 {
			if err := txq.SetComponentReserved(ctx, store.SetComponentReservedParams{ID: c.ID, Reserved: numeric(roundPlaces(h, quantityPlaces), quantityPlaces)}); err != nil {
				return err
			}
		}
	}
	return nil
}

// reserve is M20a's two-pass reserve of the order's lines (only those of
// only, when given): each line's required − consumed − scrapped − held, all
// lines in one all-or-nothing ReserveForProduction; when that is refused
// with insufficient_available, again without the lines it named, until the
// rest goes through. Any other refusal is Inventory's 409. It answers the
// lines left short.
func (s *server) reserve(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.ManufacturingOrder, today time.Time, only map[int64]bool) ([]shortLine, error) {
	ledger, err := s.ledger()
	if err != nil {
		return nil, err
	}
	components, err := txq.ListOrderComponents(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	held, err := heldByLine(ctx, ledger, tx, o.ID)
	if err != nil {
		return nil, err
	}
	need := map[int64]*big.Rat{}
	var pending []contracts.StockLine
	for _, c := range components {
		if only != nil && !only[c.ID] {
			continue
		}
		want := remainingOf(c)
		if h := held[c.ID]; h != nil {
			want.Sub(want, h)
		}
		if want.Sign() <= 0 {
			continue
		}
		need[c.ID] = want
		pending = append(pending, contracts.StockLine{VariantID: c.VariantID, LineID: c.ID, Quantity: want.FloatString(quantityPlaces)})
	}
	refused := map[int64]bool{}
	for len(pending) > 0 {
		err := ledger.ReserveForProduction(ctx, tx, orderSource(o.ID), o.SiteID, today, pending)
		if err == nil {
			break
		}
		var r *contracts.StockRefusal
		if !errors.As(err, &r) {
			return nil, fmt.Errorf("manufacturing: reserve order %d: %w", o.ID, err)
		}
		named := map[int64]bool{}
		for _, l := range r.Lines {
			named[l.LineID] = true
		}
		if r.LineID != 0 {
			named[r.LineID] = true
		}
		if r.Code != contracts.StockInsufficientAvailable || len(named) == 0 {
			return nil, stockRefusal(r, components)
		}
		rest := pending[:0:0]
		for _, l := range pending {
			if named[l.LineID] {
				refused[l.LineID] = true
				continue
			}
			rest = append(rest, l)
		}
		if len(rest) == len(pending) {
			return nil, stockRefusal(r, components)
		}
		pending = rest
	}
	if err := syncReserved(ctx, ledger, tx, txq, o.ID); err != nil {
		return nil, err
	}
	var short []shortLine
	for _, c := range components {
		if refused[c.ID] {
			short = append(short, shortLine{component: c, missing: need[c.ID]})
		}
	}
	return short, nil
}

// releaseAll gives back every reservation of the order and clears the
// copies (M20: close and cancel).
func (s *server) releaseAll(ctx context.Context, tx pgx.Tx, txq *store.Queries, id int64) error {
	ledger, err := s.ledger()
	if err != nil {
		return err
	}
	if err := ledger.Release(ctx, tx, orderSource(id), nil); err != nil {
		var r *contracts.StockRefusal
		if errors.As(err, &r) {
			return stockRefusal(r, nil)
		}
		return fmt.Errorf("manufacturing: release order %d: %w", id, err)
	}
	return txq.ClearOrderReserved(ctx, id)
}

// snapshot copies the BoM's lines onto the order as its component lines
// (M17, M18): required rounded up, the SKU, name and unit, and the standard
// cost at confirm (M23a), all from what LockVariants answered. A line whose
// variant was not locked — the BoM changed after the lock, or the catalog no
// longer answers the variant — is errRelock, never a line without its
// snapshot.
func snapshot(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, locked map[int32]contracts.VariantEntry) (int, error) {
	bom, err := txq.GetBom(ctx, o.BomID)
	if err != nil {
		return 0, err
	}
	lines, err := txq.LinesOfBoms(ctx, []int64{o.BomID})
	if err != nil {
		return 0, err
	}
	for _, l := range lines {
		if _, ok := locked[l.VariantID]; !ok {
			return 0, errRelock
		}
	}
	for _, l := range lines {
		e := locked[l.VariantID]
		required := requiredOf(ratOrZero(l.Quantity), ratOrZero(l.ScrapPercent), ratOrZero(o.Quantity), ratOrZero(bom.OutputQuantity))
		if _, err := txq.InsertOrderComponent(ctx, store.InsertOrderComponentParams{
			OrderID: o.ID, VariantID: l.VariantID, Sku: e.SKU, ProductName: e.ProductName, Unit: e.Unit,
			Required: numeric(required, quantityPlaces), OperationID: l.OperationID, StandardCost: snapshotCost(e),
			SortOrder: l.SortOrder, Note: l.Note,
		}); err != nil {
			return 0, err
		}
	}
	if err := snapshotOutputs(ctx, txq, o, bom, locked); err != nil {
		return 0, err
	}
	return len(lines), nil
}

// expectedOutput is a by-product's expected quantity on an order (manufacturing
// phase 2 M121): quantity × the order's quantity ÷ the BoM's output quantity,
// half-up to three decimals.
func expectedOutput(quantity, orderQuantity, outputQuantity *big.Rat) *big.Rat {
	if outputQuantity.Sign() <= 0 {
		return new(big.Rat)
	}
	v := new(big.Rat).Mul(quantity, orderQuantity)
	return roundPlaces(v.Quo(v, outputQuantity), quantityPlaces)
}

// snapshotOutputs copies the BoM's by-products onto the order at confirm
// (M121), each with its expected quantity, from what LockVariants answered; a
// by-product whose variant was not locked is errRelock.
func snapshotOutputs(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, bom store.ManufacturingBom, locked map[int32]contracts.VariantEntry) error {
	outputs, err := txq.OutputsOfBoms(ctx, []int64{bom.ID})
	if err != nil {
		return err
	}
	for _, x := range outputs {
		e, ok := locked[x.VariantID]
		if !ok {
			return errRelock
		}
		if _, err := txq.InsertOrderOutput(ctx, store.InsertOrderOutputParams{
			OrderID: o.ID, VariantID: x.VariantID, Sku: e.SKU, ProductName: e.ProductName, Unit: e.Unit, Quantity: x.Quantity,
			CostShare: x.CostShare, Expected: numeric(expectedOutput(ratOrZero(x.Quantity), ratOrZero(o.Quantity), ratOrZero(bom.OutputQuantity)), quantityPlaces),
			SortOrder: x.SortOrder,
		}); err != nil {
			return err
		}
	}
	return nil
}

// snapshotCost is the catalog's standard cost as a column's value; none is
// NULL (M4).
func snapshotCost(e contracts.VariantEntry) pgtype.Numeric {
	if e.StandardCost == "" {
		return pgtype.Numeric{}
	}
	r, ok := new(big.Rat).SetString(e.StandardCost)
	if !ok {
		return pgtype.Numeric{}
	}
	return numeric(roundPlaces(r, amountPlaces), amountPlaces)
}

// confirmOrder snapshots and reserves a locked draft (M17, M20), stamping
// the timeline: the confirm itself and the reserve's outcome. The snapshot
// also creates the work orders (M27), whose work centers' queues are noted
// on notes; the reserve, the ledger, comes after them (M26a).
func (s *server) confirmOrder(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.ManufacturingOrder, locked map[int32]contracts.VariantEntry, who actor, today time.Time, notes *notices) ([]shortLine, error) {
	n, err := snapshot(ctx, txq, o, locked)
	if err != nil {
		return nil, err
	}
	bom, err := txq.GetBom(ctx, o.BomID)
	if err != nil {
		return nil, err
	}
	// The lot pattern is the BoM's at confirm (manufacturing phase 2 M80).
	if err := txq.SetOrderLotPattern(ctx, store.SetOrderLotPatternParams{ID: o.ID, LotPattern: bom.LotPattern}); err != nil {
		return nil, err
	}
	o.LotPattern = bom.LotPattern
	workOrders, err := snapshotWorkOrders(ctx, txq, o, bom)
	if err != nil {
		return nil, err
	}
	for _, w := range workOrders {
		notes.workOrder(w.ID, w.OrderID, w.WorkCenterID)
	}
	if err := notes.groupWork(ctx, txq, workOrders...); err != nil {
		return nil, err
	}
	components, err := txq.ListOrderComponents(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	// The estimate is fixed now, from the snapshots just made (M39): the
	// standard costs on the lines and the rates on the work orders.
	if err := txq.SetOrderEstimate(ctx, store.SetOrderEstimateParams{ID: o.ID, Estimate: marshalCost(storedOf(estimateOf(components, workOrders)))}); err != nil {
		return nil, err
	}
	at := s.deps.Clock()
	o, err = txq.SetOrderStatus(ctx, store.SetOrderStatusParams{ID: o.ID, Status: orderConfirmed, ConfirmedAt: &at, Now: at})
	if err != nil {
		return nil, err
	}
	if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindConfirmed, who, at, map[string]any{"lines": n, "workOrders": len(workOrders)}); err != nil {
		return nil, err
	}
	short, err := s.reserve(ctx, tx, txq, o, today, nil)
	if err != nil {
		return nil, err
	}
	return short, reservedEvent(ctx, txq, o.ID, who, s, short)
}

// reservedEvent stamps a reserve's outcome on the timeline.
func reservedEvent(ctx context.Context, txq *store.Queries, id int64, who actor, s *server, short []shortLine) error {
	skus := make([]string, 0, len(short))
	for _, l := range short {
		skus = append(skus, l.component.Sku)
	}
	return event(ctx, txq, id, gen.ManufacturingOrderEventKindReserved, who, s.deps.Clock(), map[string]any{"shortLines": len(short), "short": skus})
}

// shortLinesOf is the short lines as an answer.
func shortLinesOf(short []shortLine, costs bool) []gen.ManufacturingShortLine {
	out := make([]gen.ManufacturingShortLine, 0, len(short))
	for _, l := range short {
		out = append(out, gen.ManufacturingShortLine{ComponentId: l.component.ID, Variant: componentRef(nil, l.component, costs),
			Missing: text(l.missing, quantityPlaces)})
	}
	return out
}

// withShortLines is the order read after the write, with its short lines.
func (s *server) withShortLines(ctx context.Context, id int64, short []shortLine) (gen.ManufacturingOrderWithShortLines, error) {
	o, err := s.orderAnswer(ctx, id)
	if err != nil {
		return gen.ManufacturingOrderWithShortLines{}, err
	}
	return gen.ManufacturingOrderWithShortLines{Order: o, ShortLines: shortLinesOf(short, s.has(ctx, permCostsView))}, nil
}

// confirmAnswers are confirm's typed refusals.
func confirmAnswers() answers[gen.ConfirmManufacturingOrderResponseObject] {
	return answers[gen.ConfirmManufacturingOrderResponseObject]{
		notFound: func() gen.ConfirmManufacturingOrderResponseObject { return gen.ConfirmManufacturingOrder404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.ConfirmManufacturingOrderResponseObject {
			return gen.ConfirmManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ConfirmManufacturingOrderResponseObject {
			return gen.ConfirmManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// ConfirmManufacturingOrder snapshots a draft's BoM and reserves its lines
// (M17, M20, M20a). A short line never refuses it: it is left unreserved and
// named.
func (s *server) ConfirmManufacturingOrder(ctx context.Context, req gen.ConfirmManufacturingOrderRequestObject) (gen.ConfirmManufacturingOrderResponseObject, error) {
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	// The day the ledger judges a lot's expiry on, read on the pool (rule 24).
	today, err := s.stockDay(ctx)
	if err != nil {
		return nil, err
	}
	var short []shortLine
	err = s.withOrder(ctx, req.Id, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		if err := checkRevision(o, req.Body.Revision); err != nil {
			return err
		}
		if o.Status != orderDraft {
			return orderNotDraft(o.Status, "confirming is done")
		}
		bom, err := txq.GetBom(ctx, o.BomID)
		if err != nil {
			return err
		}
		if bom.Status != statusActive {
			return notActive()
		}
		notes := newNotices()
		if short, err = s.confirmOrder(ctx, tx, txq, o, lo.locked, who, today, notes); err != nil {
			return err
		}
		notes.orders[o.ID] = true
		return notes.send(ctx, tx)
	})
	if err != nil {
		return answer(err, confirmAnswers())
	}
	out, err := s.withShortLines(ctx, req.Id, short)
	if err != nil {
		return answer(err, confirmAnswers())
	}
	return gen.ConfirmManufacturingOrder200JSONResponse(out), nil
}

// reserveAnswers are reserve now's typed refusals.
func reserveAnswers() answers[gen.ReserveManufacturingOrderResponseObject] {
	return answers[gen.ReserveManufacturingOrderResponseObject]{
		notFound: func() gen.ReserveManufacturingOrderResponseObject { return gen.ReserveManufacturingOrder404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.ReserveManufacturingOrderResponseObject {
			return gen.ReserveManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ReserveManufacturingOrderResponseObject {
			return gen.ReserveManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// ReserveManufacturingOrder is Reserve now (M20, M20a): every line's
// unreserved requirement again, in two passes.
func (s *server) ReserveManufacturingOrder(ctx context.Context, req gen.ReserveManufacturingOrderRequestObject) (gen.ReserveManufacturingOrderResponseObject, error) {
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	// The day the ledger judges a lot's expiry on, read on the pool (rule 24).
	today, err := s.stockDay(ctx)
	if err != nil {
		return nil, err
	}
	var short []shortLine
	err = s.withOrder(ctx, req.Id, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		if err := checkRevision(o, req.Body.Revision); err != nil {
			return err
		}
		if !isOpen(o.Status) {
			return orderNotOpen(o.Status)
		}
		if short, err = s.reserve(ctx, tx, txq, o, today, nil); err != nil {
			return err
		}
		if _, err := txq.TouchOrder(ctx, store.TouchOrderParams{ID: o.ID, Now: s.deps.Clock()}); err != nil {
			return err
		}
		if err := reservedEvent(ctx, txq, o.ID, who, s, short); err != nil {
			return err
		}
		return notifyOrder(ctx, tx, o.ID)
	})
	if err != nil {
		return answer(err, reserveAnswers())
	}
	out, err := s.withShortLines(ctx, req.Id, short)
	if err != nil {
		return answer(err, reserveAnswers())
	}
	return gen.ReserveManufacturingOrder200JSONResponse(out), nil
}

// judgeLineNote is a component line's note.
func judgeLineNote(f *fieldErrors, note *string) string {
	if note == nil {
		return ""
	}
	n := strings.TrimSpace(*note)
	if utf8.RuneCountInString(n) > maxLineNote {
		f.add("note", "At most 2000 characters")
	}
	return n
}

// addComponentAnswers are the add's typed refusals.
func addComponentAnswers() answers[gen.AddManufacturingOrderComponentResponseObject] {
	return answers[gen.AddManufacturingOrderComponentResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.AddManufacturingOrderComponentResponseObject {
			return gen.AddManufacturingOrderComponent400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.AddManufacturingOrderComponentResponseObject {
			return gen.AddManufacturingOrderComponent404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.AddManufacturingOrderComponentResponseObject {
			return gen.AddManufacturingOrderComponent409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.AddManufacturingOrderComponentResponseObject {
			return gen.AddManufacturingOrderComponent503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// AddManufacturingOrderComponent adds an extra component to a confirmed or
// in-progress order and reserves it (M18, M20).
func (s *server) AddManufacturingOrderComponent(ctx context.Context, req gen.AddManufacturingOrderComponentRequestObject) (gen.AddManufacturingOrderComponentResponseObject, error) {
	in := req.Body
	f := s.unknownProperties(ctx, componentInputSchema)
	required, msg := parsePositive(in.Required, quantityPlaces, maxQuantity)
	f.addCoded("required", codeQuantityInvalid, msg)
	note := judgeLineNote(&f, in.Note)
	if f.failed() {
		return answer(refuseInvalid(orderTitle, f), addComponentAnswers())
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
	var short []shortLine
	err = s.withOrder(ctx, req.Id, func(context.Context, *store.Queries, store.ManufacturingOrder) ([]int32, error) {
		return []int32{in.VariantId}, nil
	}, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		if err := checkRevision(o, in.Revision); err != nil {
			return err
		}
		if !isOpen(o.Status) {
			return orderNotOpen(o.Status)
		}
		e, ok := lo.locked[in.VariantId]
		switch {
		case !ok:
			return refuseField(orderTitle, "variantId", codeVariantNotFound, "No such variant in the catalog")
		case e.ProductType != goodsType:
			return refuseField(orderTitle, "variantId", codeVariantNotGoods, e.SKU+" is not goods; only goods are consumed")
		}
		if in.OperationId != nil {
			ops, err := txq.OperationsOfBoms(ctx, []int64{o.BomID})
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(ops, func(op store.OperationsOfBomsRow) bool { return op.ID == *in.OperationId }) {
				return refuseField(orderTitle, "operationId", "", "Not an operation of the order's bill of materials")
			}
		}
		sort, err := txq.NextComponentSortOrder(ctx, o.ID)
		if err != nil {
			return err
		}
		c, err := txq.InsertOrderComponent(ctx, store.InsertOrderComponentParams{
			OrderID: o.ID, VariantID: in.VariantId, Sku: e.SKU, ProductName: e.ProductName, Unit: e.Unit,
			Required: numeric(required, quantityPlaces), OperationID: in.OperationId, StandardCost: snapshotCost(e),
			SortOrder: sort, Added: true, Note: note,
		})
		if err != nil {
			return err
		}
		if short, err = s.reserve(ctx, tx, txq, o, today, map[int64]bool{c.ID: true}); err != nil {
			return err
		}
		at := s.deps.Clock()
		if _, err := txq.TouchOrder(ctx, store.TouchOrderParams{ID: o.ID, Now: at}); err != nil {
			return err
		}
		if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindLineAdded, who, at, map[string]any{
			"componentId": c.ID, "sku": c.Sku, "required": text(required, quantityPlaces), "reserved": len(short) == 0,
		}); err != nil {
			return err
		}
		return notifyOrder(ctx, tx, o.ID)
	})
	if err != nil {
		return answer(err, addComponentAnswers())
	}
	out, err := s.withShortLines(ctx, req.Id, short)
	if err != nil {
		return answer(err, addComponentAnswers())
	}
	return gen.AddManufacturingOrderComponent200JSONResponse(out), nil
}

// updateComponentAnswers are the edit's typed refusals.
func updateComponentAnswers() answers[gen.UpdateManufacturingOrderComponentResponseObject] {
	return answers[gen.UpdateManufacturingOrderComponentResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.UpdateManufacturingOrderComponentResponseObject {
			return gen.UpdateManufacturingOrderComponent400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateManufacturingOrderComponentResponseObject {
			return gen.UpdateManufacturingOrderComponent404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.UpdateManufacturingOrderComponentResponseObject {
			return gen.UpdateManufacturingOrderComponent409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.UpdateManufacturingOrderComponentResponseObject {
			return gen.UpdateManufacturingOrderComponent503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// UpdateManufacturingOrderComponent changes a line's requirement (M18,
// M18a): never below what is consumed and scrapped (required_below_used);
// lowering it releases what the line holds beyond what remains, raising it
// reserves the rest (M20).
func (s *server) UpdateManufacturingOrderComponent(ctx context.Context, req gen.UpdateManufacturingOrderComponentRequestObject) (gen.UpdateManufacturingOrderComponentResponseObject, error) {
	in := req.Body
	f := s.unknownProperties(ctx, componentUpdateSchema)
	required, msg := parseDecimal(in.Required, quantityPlaces, maxQuantity)
	f.addCoded("required", codeQuantityInvalid, msg)
	note := judgeLineNote(&f, in.Note)
	if f.failed() {
		return answer(refuseInvalid(orderTitle, f), updateComponentAnswers())
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
	var short []shortLine
	err = s.withOrder(ctx, req.Id, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		components, err := txq.ListOrderComponents(ctx, o.ID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(components, func(c store.ManufacturingOrderComponent) bool { return c.ID == req.ComponentId })
		if i < 0 {
			return notFound()
		}
		c := components[i]
		if err := checkRevision(o, in.Revision); err != nil {
			return err
		}
		if !isOpen(o.Status) {
			return orderNotOpen(o.Status)
		}
		used := new(big.Rat).Add(ratOrZero(c.Consumed), ratOrZero(c.Scrapped))
		if required.Cmp(used) < 0 {
			return refuseField(orderTitle, "required", codeRequiredBelowUsed,
				fmt.Sprintf("Must be at least %s, what is consumed and scrapped", text(used, quantityPlaces)))
		}
		if in.Note == nil {
			note = c.Note
		}
		if err := txq.SetComponentRequired(ctx, store.SetComponentRequiredParams{ID: c.ID, Required: numeric(required, quantityPlaces), Note: note}); err != nil {
			return err
		}
		ledger, err := s.ledger()
		if err != nil {
			return err
		}
		held, err := heldByLine(ctx, ledger, tx, o.ID)
		if err != nil {
			return err
		}
		remaining := new(big.Rat).Sub(required, used)
		if h := held[c.ID]; h != nil && h.Cmp(remaining) > 0 {
			excess := new(big.Rat).Sub(h, remaining)
			if err := ledger.Release(ctx, tx, orderSource(o.ID), []contracts.StockRelease{{LineID: c.ID, Quantity: excess.FloatString(quantityPlaces)}}); err != nil {
				var r *contracts.StockRefusal
				if errors.As(err, &r) {
					return stockRefusal(r, components)
				}
				return err
			}
		}
		if short, err = s.reserve(ctx, tx, txq, o, today, map[int64]bool{c.ID: true}); err != nil {
			return err
		}
		at := s.deps.Clock()
		if _, err := txq.TouchOrder(ctx, store.TouchOrderParams{ID: o.ID, Now: at}); err != nil {
			return err
		}
		if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindLineChanged, who, at, map[string]any{
			"componentId": c.ID, "sku": c.Sku, "from": numericText(c.Required, quantityPlaces), "to": text(required, quantityPlaces),
		}); err != nil {
			return err
		}
		return notifyOrder(ctx, tx, o.ID)
	})
	if err != nil {
		return answer(err, updateComponentAnswers())
	}
	out, err := s.withShortLines(ctx, req.Id, short)
	if err != nil {
		return answer(err, updateComponentAnswers())
	}
	return gen.UpdateManufacturingOrderComponent200JSONResponse(out), nil
}

// closeAnswers are close's typed refusals.
func closeAnswers() answers[gen.CloseManufacturingOrderResponseObject] {
	return answers[gen.CloseManufacturingOrderResponseObject]{
		notFound: func() gen.CloseManufacturingOrderResponseObject { return gen.CloseManufacturingOrder404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.CloseManufacturingOrderResponseObject {
			return gen.CloseManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CloseManufacturingOrderResponseObject {
			return gen.CloseManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// CloseManufacturingOrder finishes an order (M23, M23a, M29a): it ends the
// open logs of its work orders and cancels those not done, releases what
// remains reserved and marks it done. Short of its quantity it asks for the
// backorder (quantity_short): true makes one for the rest, numbered from the
// root (M17b), with the same BoM, origin, place and dates, confirmed at once
// because the original was — so it reserves again; false closes short. The
// backorder's confirm is the one that does not refuse an archived BoM: it
// keeps its original's, whatever happened to it since. An order with good
// output closes only when every check of its work orders not cancelled has a
// result (closeGate, checks_incomplete).
func (s *server) CloseManufacturingOrder(ctx context.Context, req gen.CloseManufacturingOrderRequestObject) (gen.CloseManufacturingOrderResponseObject, error) {
	in := req.Body
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	// The day the ledger judges a lot's expiry on, read on the pool (rule 24).
	today, err := s.stockDay(ctx)
	if err != nil {
		return nil, err
	}
	wantsBackorder := in.Backorder != nil && *in.Backorder
	var backorderID int64
	var short []shortLine
	err = s.withOrder(ctx, req.Id, func(ctx context.Context, q *store.Queries, o store.ManufacturingOrder) ([]int32, error) {
		if !wantsBackorder {
			return nil, nil
		}
		return bomVariants(ctx, q, o.BomID)
	}, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		if err := checkRevision(o, in.Revision); err != nil {
			return err
		}
		if !isOpen(o.Status) {
			return orderNotOpen(o.Status)
		}
		if err := closeGate(ctx, txq, o); err != nil {
			return err
		}
		rest := new(big.Rat).Sub(ratOrZero(o.Quantity), ratOrZero(o.Produced))
		if rest.Sign() > 0 && in.Backorder == nil {
			return conflict(codeQuantityShort, "Less than the quantity was produced",
				fmt.Sprintf("%s of %s were produced. Say whether to create a backorder for the rest (backorder true) or to close short (false).",
					numericText(o.Produced, quantityPlaces), numericText(o.Quantity, quantityPlaces)))
		}
		notes := newNotices()
		at := s.deps.Clock()
		if err := endOrderWork(ctx, txq, o.ID, at, notes); err != nil {
			return err
		}
		if err := s.fixActual(ctx, txq, o, at); err != nil {
			return err
		}
		if err := s.releaseAll(ctx, tx, txq, o.ID); err != nil {
			return err
		}
		closed, err := txq.SetOrderStatus(ctx, store.SetOrderStatusParams{ID: o.ID, Status: orderDone, ClosedAt: &at, Now: at})
		if err != nil {
			return err
		}
		// Made for a sales order line, its close tells Orders, on this
		// transaction, while it holds the order's row (M48).
		if err := publishDone(ctx, tx, closed, at); err != nil {
			return err
		}
		if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindClosed, who, at, map[string]any{
			"produced": numericText(o.Produced, quantityPlaces), "quantity": numericText(o.Quantity, quantityPlaces),
			"outputScrap": numericText(o.OutputScrap, quantityPlaces), "short": rest.Sign() > 0,
		}); err != nil {
			return err
		}
		if rest.Sign() > 0 && wantsBackorder {
			b, err := s.createBackorder(ctx, txq, o, rest, at, who)
			if err != nil {
				return err
			}
			backorderID = b.ID
			if short, err = s.confirmOrder(ctx, tx, txq, b, lo.locked, who, today, notes); err != nil {
				return err
			}
			notes.orders[b.ID] = true
		}
		notes.orders[o.ID] = true
		return notes.send(ctx, tx)
	})
	if err != nil {
		return answer(err, closeAnswers())
	}
	out := gen.CloseManufacturingOrder200JSONResponse{ShortLines: shortLinesOf(short, s.has(ctx, permCostsView))}
	if out.Order, err = s.orderAnswer(ctx, req.Id); err != nil {
		return answer(err, closeAnswers())
	}
	if backorderID != 0 {
		b, err := s.orderAnswer(ctx, backorderID)
		if err != nil {
			return answer(err, closeAnswers())
		}
		out.Backorder = &b
	}
	return out, nil
}

// fixActual fixes the order's actual cost at close (M41), after its open
// logs were ended at at: the parts, rounded once, and the cost per unit of
// the good output.
func (s *server) fixActual(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, at time.Time) error {
	components, err := txq.ListOrderComponents(ctx, o.ID)
	if err != nil {
		return err
	}
	wos, err := txq.WorkOrdersOfOrder(ctx, o.ID)
	if err != nil {
		return err
	}
	logs, err := txq.LogsOfWorkOrders(ctx, workOrderIDs(wos))
	if err != nil {
		return err
	}
	outputs, err := txq.OutputsOfOrders(ctx, []int64{o.ID})
	if err != nil {
		return err
	}
	actual, _ := actualOf(components, wos, logs, at)
	stored := storedOf(actual)
	// The main product carries what its by-products do not (manufacturing
	// phase 2 M122); the exact total is split, each part rounded once.
	main, parts := splitCost(actual.total(), outputs)
	stored.CostPerUnit = costPerUnitOf(main, ratOrZero(o.Produced))
	if len(outputs) > 0 {
		mainText := amountText(main)
		stored.MainTotal, stored.ByProducts = &mainText, parts
	}
	return txq.SetOrderActual(ctx, store.SetOrderActualParams{ID: o.ID, Actual: marshalCost(stored)})
}

// createBackorder inserts the backorder of o for rest (M23, M17b): the next
// suffix of the root's number, o's BoM, place, due date, priority,
// responsible, note and origin — a cancelled sales order's flag too (M44) —,
// linked as its backorder. Its plan is not carried: its work orders are new,
// and wait to be planned (M35).
func (s *server) createBackorder(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, rest *big.Rat, at time.Time, who actor) (store.ManufacturingOrder, error) {
	suffix, err := txq.NextBackorderSuffix(ctx, o.Number)
	if err != nil {
		return store.ManufacturingOrder{}, err
	}
	b, err := txq.InsertOrder(ctx, store.InsertOrderParams{
		Number: o.Number, Suffix: suffix, VariantID: o.VariantID, BomID: o.BomID, Quantity: numeric(rest, quantityPlaces),
		SiteID: o.SiteID, ComponentLocationID: o.ComponentLocationID, OutputLocationID: o.OutputLocationID, DueDate: o.DueDate,
		Priority: o.Priority, ResponsibleUserID: o.ResponsibleUserID,
		Note: o.Note, OriginKind: o.OriginKind, OriginOrderID: o.OriginOrderID, OriginLineID: o.OriginLineID,
		OriginReference: o.OriginReference, OriginCancelled: o.OriginCancelled, ParentOrderID: o.ParentOrderID, BackorderOfID: &o.ID, Now: at,
	})
	if err != nil {
		return b, err
	}
	number := orderNumber(b.Number, b.Suffix)
	if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindBackorderCreated, who, at, map[string]any{
		"backorderId": b.ID, "number": number, "quantity": text(rest, quantityPlaces),
	}); err != nil {
		return b, err
	}
	return b, event(ctx, txq, b.ID, gen.ManufacturingOrderEventKindCreated, who, at, map[string]any{
		"number": number, "quantity": text(rest, quantityPlaces), "backorderOf": orderNumber(o.Number, o.Suffix),
	})
}

// cancelAnswers are cancel's typed refusals.
func cancelAnswers() answers[gen.CancelManufacturingOrderResponseObject] {
	return answers[gen.CancelManufacturingOrderResponseObject]{
		notFound: func() gen.CancelManufacturingOrderResponseObject { return gen.CancelManufacturingOrder404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.CancelManufacturingOrderResponseObject {
			return gen.CancelManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CancelManufacturingOrderResponseObject {
			return gen.CancelManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// CancelManufacturingOrder cancels an order while nothing has been produced
// or consumed (order_has_production otherwise), ending the open logs of its
// work orders and cancelling them (M29a), and releasing every reservation
// (M19). It keeps its number.
func (s *server) CancelManufacturingOrder(ctx context.Context, req gen.CancelManufacturingOrderRequestObject) (gen.CancelManufacturingOrderResponseObject, error) {
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withOrder(ctx, req.Id, nil, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, lo *lockedOrder) error {
		o := lo.order
		if err := checkRevision(o, req.Body.Revision); err != nil {
			return err
		}
		if o.Status == orderDone || o.Status == orderCancelled {
			return orderNotOpen(o.Status)
		}
		productions, err := txq.CountProductionsOfOrder(ctx, o.ID)
		if err != nil {
			return err
		}
		components, err := txq.ListOrderComponents(ctx, o.ID)
		if err != nil {
			return err
		}
		used := ratOrZero(o.Produced).Sign() > 0 || ratOrZero(o.OutputScrap).Sign() > 0 || productions > 0 ||
			slices.ContainsFunc(components, func(c store.ManufacturingOrderComponent) bool {
				return ratOrZero(c.Consumed).Sign() > 0 || ratOrZero(c.Scrapped).Sign() > 0
			})
		if used {
			return conflict(codeOrderHasProduction, "The manufacturing order has production",
				"Something was produced or consumed on this order, so it cannot be cancelled. Close it instead.")
		}
		notes := newNotices()
		at := s.deps.Clock()
		if err := endOrderWork(ctx, txq, o.ID, at, notes); err != nil {
			return err
		}
		if o.Status != orderDraft {
			if err := s.releaseAll(ctx, tx, txq, o.ID); err != nil {
				return err
			}
		}
		if _, err := txq.SetOrderStatus(ctx, store.SetOrderStatusParams{ID: o.ID, Status: orderCancelled, ClosedAt: &at, Now: at}); err != nil {
			return err
		}
		if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindCancelled, who, at, nil); err != nil {
			return err
		}
		notes.orders[o.ID] = true
		return notes.send(ctx, tx)
	})
	if err != nil {
		return answer(err, cancelAnswers())
	}
	out, err := s.orderAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, cancelAnswers())
	}
	return gen.CancelManufacturingOrder200JSONResponse(out), nil
}
