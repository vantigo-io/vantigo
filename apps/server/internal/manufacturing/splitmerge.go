package manufacturing

// Split and merge of manufacturing orders (manufacturing phase 2 M124–M128).
// Both take orders that are drafts or confirmed with no production, no time
// log and no registered quantity (order_has_work), so nothing measured is lost when their lines and
// work orders are taken again for new quantities (M126 amends D1 M28a's
// "never recomputed").
//
// The locks (M127): withScheduledOrders takes the schedule's advisory lock of
// every work center of every order involved — group members included — in id
// order, before any row lock, because both unplan the orders; then the
// canonical order: LockVariants, the orders FOR UPDATE in id order, their work
// orders, then the ledger — release first, then the two-pass reserve of each
// order kept.

import (
	"cmp"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	splitTitle    = "The manufacturing order was not split"
	mergeTitle    = "The manufacturing orders were not merged"
	minSplitParts = 2
	maxSplitParts = 20
)

// orderHasWork is the 409 order_has_work of a split or a merge of an order
// with a production, a time log or a registered quantity.
func orderHasWork(number string) error {
	return conflict(codeOrderHasWork, "The manufacturing order has work",
		fmt.Sprintf("%s has a production, a time log or a registered quantity; it is neither split nor merged.", number))
}

// judgeWorkless refuses an order that is not a draft or confirmed, or has a
// production, a time log or a registered quantity (M124, M125).
func judgeWorkless(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder) error {
	number := orderNumber(o.Number, o.Suffix)
	switch o.Status {
	case orderDraft, orderConfirmed:
	case orderInProgress:
		return orderHasWork(number)
	default:
		return orderNotOpen(o.Status)
	}
	productions, err := txq.CountProductionsOfOrder(ctx, o.ID)
	if err != nil {
		return err
	}
	work, err := txq.CountWorkOfOrder(ctx, o.ID)
	if err != nil {
		return err
	}
	if productions > 0 || work > 0 {
		return orderHasWork(number)
	}
	return nil
}

// unplanOrder clears the order's plan in the write's transaction (M124): its
// open work orders lose their slots and planned dates, the order its window.
func unplanOrder(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, wos []store.ManufacturingWorkOrder, notes *notices, now time.Time) error {
	open := openWork(wos)
	if len(open) > 0 {
		if err := txq.DeleteSlotsOfWorkOrders(ctx, workOrderIDs(open)); err != nil {
			return err
		}
	}
	for _, w := range open {
		if _, err := txq.SetWorkOrderPlan(ctx, store.SetWorkOrderPlanParams{ID: w.ID}); err != nil {
			return err
		}
		notes.workOrder(w.ID, w.OrderID, w.WorkCenterID)
	}
	if err := notes.groupWork(ctx, txq, open...); err != nil {
		return err
	}
	if o.PlannedStart != nil || o.PlannedFinish != nil {
		if _, err := txq.SetOrderPlan(ctx, store.SetOrderPlanParams{ID: o.ID, Now: now}); err != nil {
			return err
		}
	}
	notes.orders[o.ID] = true
	notes.sites[o.SiteID] = true
	return nil
}

// splitParts is total split over the quantities in proportion to each, of
// their sum whole (§16): each part half-up to three decimals, the last the
// remainder, so the parts sum to total.
func splitParts(total *big.Rat, quantities []*big.Rat, whole *big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(quantities))
	left := new(big.Rat).Set(total)
	for i, q := range quantities {
		if i == len(quantities)-1 {
			out[i] = positive(left)
			break
		}
		part := new(big.Rat).Mul(total, q)
		part = roundPlaces(part.Quo(part, whole), quantityPlaces)
		if part.Cmp(left) > 0 {
			part = new(big.Rat).Set(left)
		}
		out[i] = part
		left.Sub(left, part)
	}
	return out
}

// expectedAt is a work order's expected minutes for an order quantity
// (M126), from its own figures.
func expectedAt(w store.ManufacturingWorkOrder, quantity *big.Rat, bom store.ManufacturingBom) int64 {
	output := ratOrZero(w.BomOutputQuantity)
	if output.Sign() <= 0 {
		output = ratOrZero(bom.OutputQuantity)
	}
	return expectedFor(ratOrZero(w.SetupMinutes), ratOrZero(w.CleanupMinutes), ratOrZero(w.MinutesPerCycle), w.Capacity, w.TimeEfficiency,
		quantity, output).Int64()
}

// retimeWorkOrders recomputes the order's work orders' expected minutes for
// its new quantity (M126) and fixes its estimate again (M39).
func retimeWorkOrders(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, bom store.ManufacturingBom, notes *notices) error {
	wos, err := txq.WorkOrdersOfOrder(ctx, o.ID)
	if err != nil {
		return err
	}
	for _, w := range wos {
		if w.Status == workOrderDone || w.Status == workOrderCancelled {
			continue
		}
		if err := txq.SetWorkOrderExpected(ctx, store.SetWorkOrderExpectedParams{ID: w.ID, ExpectedMinutes: expectedAt(w, ratOrZero(o.Quantity), bom)}); err != nil {
			return err
		}
		notes.workOrder(w.ID, w.OrderID, w.WorkCenterID)
	}
	return fixEstimate(ctx, txq, o.ID)
}

// fixEstimate fixes the order's estimate from its lines and work orders as
// they are now (M39).
func fixEstimate(ctx context.Context, txq *store.Queries, orderID int64) error {
	components, err := txq.ListOrderComponents(ctx, orderID)
	if err != nil {
		return err
	}
	wos, err := txq.WorkOrdersOfOrder(ctx, orderID)
	if err != nil {
		return err
	}
	return txq.SetOrderEstimate(ctx, store.SetOrderEstimateParams{ID: orderID, Estimate: marshalCost(storedOf(estimateOf(components, wos)))})
}

// copyWorkOrders copies the order's work orders onto a split part (M124):
// their figures, graph and checks as the confirm snapshotted them, the
// expected minutes for the part's quantity.
func copyWorkOrders(ctx context.Context, txq *store.Queries, from store.ManufacturingOrder, to store.ManufacturingOrder, wos []store.ManufacturingWorkOrder,
	bom store.ManufacturingBom, notes *notices) error {
	ids := map[int64]int64{}
	var made []store.ManufacturingWorkOrder
	for _, w := range wos {
		if w.Status == workOrderCancelled {
			continue
		}
		n, err := txq.InsertWorkOrder(ctx, store.InsertWorkOrderParams{
			OrderID: to.ID, OperationID: w.OperationID, Sequence: w.Sequence, Name: w.Name, WorkCenterID: w.WorkCenterID,
			WorkCenterGroupID: w.WorkCenterGroupID, SetupMinutes: w.SetupMinutes, CleanupMinutes: w.CleanupMinutes, MinutesPerCycle: w.MinutesPerCycle,
			Capacity: w.Capacity, TimeEfficiency: w.TimeEfficiency, MachineRate: w.MachineRate, LabourRate: w.LabourRate, Instructions: w.Instructions,
			ExpectedMinutes: expectedAt(w, ratOrZero(to.Quantity), bom), Status: w.Status, SendAheadQuantity: w.SendAheadQuantity,
			BomOutputQuantity: w.BomOutputQuantity, OwnSetup: w.OwnSetup,
		})
		if err != nil {
			return err
		}
		ids[w.ID] = n.ID
		made = append(made, n)
		if err := txq.CopyWorkOrderChecks(ctx, store.CopyWorkOrderChecksParams{ToWorkOrderID: n.ID, FromWorkOrderID: w.ID}); err != nil {
			return err
		}
		notes.workOrder(n.ID, n.OrderID, n.WorkCenterID)
	}
	edges, err := txq.DependenciesOfOrders(ctx, []int64{from.ID})
	if err != nil {
		return err
	}
	for _, e := range edges {
		a, okA := ids[e.WorkOrderID]
		b, okB := ids[e.DependsOnID]
		if !okA || !okB {
			continue
		}
		if err := txq.InsertWorkOrderDependency(ctx, store.InsertWorkOrderDependencyParams{WorkOrderID: a, DependsOnID: b}); err != nil {
			return err
		}
	}
	// The group topics of the part's own work orders (M119).
	return notes.groupWork(ctx, txq, made...)
}

// splitAnswers are the split's typed refusals.
func splitAnswers() answers[gen.SplitManufacturingOrderResponseObject] {
	return answers[gen.SplitManufacturingOrderResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.SplitManufacturingOrderResponseObject {
			return gen.SplitManufacturingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.SplitManufacturingOrderResponseObject { return gen.SplitManufacturingOrder404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.SplitManufacturingOrderResponseObject {
			return gen.SplitManufacturingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.SplitManufacturingOrderResponseObject {
			return gen.SplitManufacturingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// SplitManufacturingOrder splits an order into quantities summing to its own
// (M124): the order keeps the first; each other part is a new order numbered
// as a backorder of its root, split from it, with its BoM, origin, due date
// and locations. Every one is unplanned; a confirmed order's reservations are
// released, its lines, by-products and work orders taken for each part's
// quantity, and every part reserves again.
func (s *server) SplitManufacturingOrder(ctx context.Context, req gen.SplitManufacturingOrderRequestObject) (gen.SplitManufacturingOrderResponseObject, error) {
	in := req.Body
	f := s.unknownProperties(ctx, splitInputSchema)
	if n := len(in.Quantities); n < minSplitParts || n > maxSplitParts {
		f.add("quantities", "2–20 quantities")
	}
	quantities := make([]*big.Rat, 0, len(in.Quantities))
	sum := new(big.Rat)
	for i, q := range in.Quantities {
		v, msg := parsePositive(q, quantityPlaces, maxQuantity)
		f.addCoded(fmt.Sprintf("quantities[%d]", i), codeQuantityInvalid, msg)
		if v != nil {
			quantities = append(quantities, v)
			sum.Add(sum, v)
		}
	}
	if f.failed() {
		return answer(refuseInvalid(splitTitle, f), splitAnswers())
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
	var partIDs []int64
	var short []shortLine
	err = s.withScheduledOrders(ctx, ordersCenters, func(context.Context, *store.Queries) ([]int64, error) { return []int64{req.Id}, nil }, nil,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
			partIDs, short = nil, nil
			o := orders[req.Id].order
			if err := checkRevision(o, in.Revision); err != nil {
				return err
			}
			if err := judgeWorkless(ctx, txq, o); err != nil {
				return err
			}
			if sum.Cmp(ratOrZero(o.Quantity)) != 0 {
				return refuseField(splitTitle, "quantities", codeSplitSum,
					fmt.Sprintf("The quantities sum to %s; the order's is %s.", text(sum, quantityPlaces), numericText(o.Quantity, quantityPlaces)))
			}
			wos, err := txq.WorkOrdersOfOrder(ctx, o.ID)
			if err != nil {
				return err
			}
			if !centersHeld(ctx, wos) {
				return errRelock
			}
			if _, err := txq.LockWorkOrders(ctx, workOrderIDs(wos)); err != nil {
				return err
			}
			bom, err := txq.GetBom(ctx, o.BomID)
			if err != nil {
				return err
			}
			notes := newNotices()
			now := s.deps.Clock()
			if err := unplanOrder(ctx, txq, o, wos, notes, now); err != nil {
				return err
			}
			confirmed := o.Status == orderConfirmed
			if confirmed {
				if err := s.releaseAll(ctx, tx, txq, o.ID); err != nil {
					return err
				}
			}
			components, err := txq.ListOrderComponents(ctx, o.ID)
			if err != nil {
				return err
			}
			outputs, err := txq.OutputsOfOrders(ctx, []int64{o.ID})
			if err != nil {
				return err
			}
			whole := ratOrZero(o.Quantity)
			lineParts := make([][]*big.Rat, len(components))
			for i, c := range components {
				lineParts[i] = splitParts(ratOrZero(c.Required), quantities, whole)
			}
			outputParts := make([][]*big.Rat, len(outputs))
			for i, x := range outputs {
				outputParts[i] = splitParts(ratOrZero(x.Expected), quantities, whole)
			}
			kept, err := txq.SetOrderQuantityAndDue(ctx, store.SetOrderQuantityAndDueParams{ID: o.ID, Quantity: numeric(quantities[0], quantityPlaces),
				DueDate: o.DueDate, Now: now})
			if err != nil {
				return err
			}
			if confirmed {
				for i, c := range components {
					if err := txq.SetComponentRequiredOnly(ctx, store.SetComponentRequiredOnlyParams{ID: c.ID, Required: numeric(lineParts[i][0], quantityPlaces)}); err != nil {
						return err
					}
				}
				for i, x := range outputs {
					if err := txq.SetOutputExpected(ctx, store.SetOutputExpectedParams{ID: x.ID, Expected: numeric(outputParts[i][0], quantityPlaces)}); err != nil {
						return err
					}
				}
				if err := retimeWorkOrders(ctx, txq, kept, bom, notes); err != nil {
					return err
				}
			}
			var made []map[string]any
			for p := 1; p < len(quantities); p++ {
				suffix, err := txq.NextBackorderSuffix(ctx, o.Number)
				if err != nil {
					return err
				}
				part, err := txq.InsertOrder(ctx, store.InsertOrderParams{
					Number: o.Number, Suffix: suffix, VariantID: o.VariantID, BomID: o.BomID, Quantity: numeric(quantities[p], quantityPlaces),
					SiteID: o.SiteID, ComponentLocationID: o.ComponentLocationID, OutputLocationID: o.OutputLocationID, DueDate: o.DueDate,
					RequestedStart: o.RequestedStart, Priority: o.Priority, ResponsibleUserID: o.ResponsibleUserID, Note: o.Note,
					OriginKind: o.OriginKind, OriginOrderID: o.OriginOrderID, OriginLineID: o.OriginLineID, OriginReference: o.OriginReference,
					OriginCancelled: o.OriginCancelled, ParentOrderID: o.ParentOrderID, SplitFromID: &o.ID, Now: now,
				})
				if err != nil {
					return err
				}
				if err := txq.SetOrderLotPattern(ctx, store.SetOrderLotPatternParams{ID: part.ID, LotPattern: o.LotPattern}); err != nil {
					return err
				}
				if confirmed {
					for i, c := range components {
						if _, err := txq.InsertOrderComponent(ctx, store.InsertOrderComponentParams{
							OrderID: part.ID, VariantID: c.VariantID, Sku: c.Sku, ProductName: c.ProductName, Unit: c.Unit,
							Required: numeric(lineParts[i][p], quantityPlaces), OperationID: c.OperationID, StandardCost: c.StandardCost,
							SortOrder: c.SortOrder, Added: c.Added, Note: c.Note,
						}); err != nil {
							return err
						}
					}
					for i, x := range outputs {
						if _, err := txq.InsertOrderOutput(ctx, store.InsertOrderOutputParams{
							OrderID: part.ID, VariantID: x.VariantID, Sku: x.Sku, ProductName: x.ProductName, Unit: x.Unit, Quantity: x.Quantity,
							CostShare: x.CostShare, Expected: numeric(outputParts[i][p], quantityPlaces), SortOrder: x.SortOrder,
						}); err != nil {
							return err
						}
					}
					if err := copyWorkOrders(ctx, txq, o, part, wos, bom, notes); err != nil {
						return err
					}
					if err := fixEstimate(ctx, txq, part.ID); err != nil {
						return err
					}
					if part, err = txq.SetOrderStatus(ctx, store.SetOrderStatusParams{ID: part.ID, Status: orderConfirmed, ConfirmedAt: o.ConfirmedAt, Now: now}); err != nil {
						return err
					}
				}
				number := orderNumber(part.Number, part.Suffix)
				if err := event(ctx, txq, part.ID, gen.ManufacturingOrderEventKindSplitFrom, who, now, map[string]any{
					"orderId": o.ID, "number": orderNumber(o.Number, o.Suffix), "quantity": text(quantities[p], quantityPlaces),
				}); err != nil {
					return err
				}
				made = append(made, map[string]any{"orderId": part.ID, "number": number, "quantity": text(quantities[p], quantityPlaces)})
				partIDs = append(partIDs, part.ID)
				notes.orders[part.ID] = true
			}
			if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindSplit, who, now, map[string]any{
				"from": numericText(o.Quantity, quantityPlaces), "kept": text(quantities[0], quantityPlaces), "parts": made,
			}); err != nil {
				return err
			}
			// The ledger last (M127): every part reserves again, the order first.
			if confirmed {
				for _, id := range append([]int64{o.ID}, partIDs...) {
					row, err := txq.GetOrder(ctx, id)
					if err != nil {
						return err
					}
					more, err := s.reserve(ctx, tx, txq, row, today, nil)
					if err != nil {
						return err
					}
					if err := reservedEvent(ctx, txq, id, who, s, more); err != nil {
						return err
					}
					short = append(short, more...)
				}
			}
			return notes.send(ctx, tx)
		})
	if err != nil {
		return answer(err, splitAnswers())
	}
	out := gen.SplitManufacturingOrder200JSONResponse{Parts: []gen.ManufacturingOrderLink{}, ShortLines: shortLinesOf(short, s.has(ctx, permCostsView))}
	if out.Order, err = s.orderAnswer(ctx, req.Id); err != nil {
		return answer(err, splitAnswers())
	}
	parts, err := store.New(s.deps.Pool).OrdersByIDs(ctx, partIDs)
	if err != nil {
		return nil, err
	}
	for _, p := range parts {
		out.Parts = append(out.Parts, linkOf(p))
	}
	return out, nil
}

// sameOrigin reports whether two orders serve the same origin (M125): both
// by hand, both from a reorder, or both made for the same sales order line or
// the same parent.
func sameOrigin(a, b store.ManufacturingOrder) bool {
	if a.OriginKind != b.OriginKind {
		return false
	}
	switch a.OriginKind {
	case originSalesOrder:
		return a.OriginLineID != nil && b.OriginLineID != nil && *a.OriginLineID == *b.OriginLineID
	case originParent:
		return a.ParentOrderID != nil && b.ParentOrderID != nil && *a.ParentOrderID == *b.ParentOrderID
	}
	return true
}

// mergeMismatch is the 400 merge_mismatch naming why.
func mergeMismatch(detail string) error {
	p := problem(http.StatusBadRequest, codeMergeMismatch, mergeTitle, detail)
	p.Errors = &map[string][]string{"orders": {detail}}
	return refuse(p)
}

// mergeAnswers are the merge's typed refusals.
func mergeAnswers() answers[gen.MergeManufacturingOrdersResponseObject] {
	return answers[gen.MergeManufacturingOrdersResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.MergeManufacturingOrdersResponseObject {
			return gen.MergeManufacturingOrders400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.MergeManufacturingOrdersResponseObject { return gen.MergeManufacturingOrders404Response{} },
		conflict: func(p gen.ManufacturingProblem) gen.MergeManufacturingOrdersResponseObject {
			return gen.MergeManufacturingOrders409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.MergeManufacturingOrdersResponseObject {
			return gen.MergeManufacturingOrders503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// lineKey is how a merge matches a merged order's line to the survivor's: the
// variant and the operation of a BoM line, the n-th such line by sort order
// to the n-th; an added line matches none.
type lineKey struct {
	variant   int32
	operation int64
}

func keyOf(c store.ManufacturingOrderComponent) lineKey {
	op := int64(0)
	if c.OperationID != nil {
		op = *c.OperationID
	}
	return lineKey{c.VariantID, op}
}

// MergeManufacturingOrders merges orders (M125): of the same variant, BoM
// and site, all drafts or all confirmed, with no production and no time log,
// and the same origin. The one with the lowest number survives with the
// summed quantity and the earliest due date; the others are cancelled naming
// it. Every one is unplanned; confirmed ones release their reservations, the
// survivor takes the others' lines and by-products, its work orders' expected
// minutes are recomputed and it reserves again.
func (s *server) MergeManufacturingOrders(ctx context.Context, req gen.MergeManufacturingOrdersRequestObject) (gen.MergeManufacturingOrdersResponseObject, error) {
	in := req.Body
	f := s.unknownProperties(ctx, mergeInputSchema)
	if n := len(in.Orders); n < minSplitParts || n > maxSplitParts {
		f.add("orders", "2–20 orders")
	}
	revisions := map[int64]int32{}
	ids := make([]int64, 0, len(in.Orders))
	for i, o := range in.Orders {
		if _, ok := revisions[o.Id]; ok {
			f.add(fmt.Sprintf("orders[%d].id", i), "Named twice")
		}
		revisions[o.Id] = o.Revision
		ids = append(ids, o.Id)
	}
	if f.failed() {
		return answer(refuseInvalid(mergeTitle, f), mergeAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	today, err := s.stockDay(ctx)
	if err != nil {
		return nil, err
	}
	var survivorID int64
	var mergedIDs []int64
	var short []shortLine
	err = s.withScheduledOrders(ctx, ordersCenters, func(context.Context, *store.Queries) ([]int64, error) { return ids, nil }, nil,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, orders map[int64]*lockedOrder) error {
			mergedIDs, short = nil, nil
			list := make([]store.ManufacturingOrder, 0, len(orders))
			for _, lo := range orders {
				list = append(list, lo.order)
			}
			slices.SortFunc(list, func(a, b store.ManufacturingOrder) int {
				return cmp.Or(cmp.Compare(a.Number, b.Number), cmp.Compare(a.Suffix, b.Suffix), cmp.Compare(a.ID, b.ID))
			})
			for _, o := range list {
				if err := checkRevision(o, revisions[o.ID]); err != nil {
					return err
				}
				if err := judgeWorkless(ctx, txq, o); err != nil {
					return err
				}
			}
			survivor := list[0]
			for _, o := range list[1:] {
				number := orderNumber(o.Number, o.Suffix)
				switch {
				case o.VariantID != survivor.VariantID, o.BomID != survivor.BomID, o.SiteID != survivor.SiteID:
					return mergeMismatch(fmt.Sprintf("%s makes another variant, by another bill of materials or at another site.", number))
				case o.Status != survivor.Status:
					return mergeMismatch(fmt.Sprintf("%s is %s and %s is %s: merge drafts with drafts and confirmed orders with confirmed ones.",
						orderNumber(survivor.Number, survivor.Suffix), survivor.Status, number, o.Status))
				case !sameOrigin(o, survivor):
					return mergeMismatch(fmt.Sprintf("%s serves another origin; orders of different origins are not merged, so neither promise is lost.", number))
				}
			}
			var all []store.ManufacturingWorkOrder
			byOrder := map[int64][]store.ManufacturingWorkOrder{}
			for _, o := range list {
				wos, err := txq.WorkOrdersOfOrder(ctx, o.ID)
				if err != nil {
					return err
				}
				byOrder[o.ID] = wos
				all = append(all, wos...)
			}
			if !centersHeld(ctx, all) {
				return errRelock
			}
			if _, err := txq.LockWorkOrders(ctx, workOrderIDs(all)); err != nil {
				return err
			}
			bom, err := txq.GetBom(ctx, survivor.BomID)
			if err != nil {
				return err
			}
			notes := newNotices()
			now := s.deps.Clock()
			total := new(big.Rat)
			due := survivor.DueDate
			for _, o := range list {
				if err := unplanOrder(ctx, txq, o, byOrder[o.ID], notes, now); err != nil {
					return err
				}
				total.Add(total, ratOrZero(o.Quantity))
				if o.DueDate.Valid && (!due.Valid || o.DueDate.Time.Before(due.Time)) {
					due = pgtype.Date{Time: o.DueDate.Time, Valid: true}
				}
			}
			confirmed := survivor.Status == orderConfirmed
			if confirmed {
				for _, o := range list {
					if err := s.releaseAll(ctx, tx, txq, o.ID); err != nil {
						return err
					}
				}
			}
			kept, err := txq.SetOrderQuantityAndDue(ctx, store.SetOrderQuantityAndDueParams{ID: survivor.ID, Quantity: numeric(total, quantityPlaces),
				DueDate: due, Now: now})
			if err != nil {
				return err
			}
			if confirmed {
				if err := s.mergeLines(ctx, txq, survivor.ID, list[1:]); err != nil {
					return err
				}
				if err := retimeWorkOrders(ctx, txq, kept, bom, notes); err != nil {
					return err
				}
			}
			var merged []map[string]any
			for _, o := range list[1:] {
				if err := endOrderWork(ctx, txq, o.ID, now, notes); err != nil {
					return err
				}
				if _, err := txq.SetOrderMergedInto(ctx, store.SetOrderMergedIntoParams{ID: o.ID, MergedIntoID: &survivor.ID, Now: now}); err != nil {
					return err
				}
				if err := event(ctx, txq, o.ID, gen.ManufacturingOrderEventKindMergedInto, who, now, map[string]any{
					"orderId": survivor.ID, "number": orderNumber(survivor.Number, survivor.Suffix),
				}); err != nil {
					return err
				}
				merged = append(merged, map[string]any{"orderId": o.ID, "number": orderNumber(o.Number, o.Suffix), "quantity": numericText(o.Quantity, quantityPlaces)})
				mergedIDs = append(mergedIDs, o.ID)
				notes.orders[o.ID] = true
			}
			if err := event(ctx, txq, survivor.ID, gen.ManufacturingOrderEventKindMerged, who, now, map[string]any{
				"orders": merged, "quantity": text(total, quantityPlaces),
			}); err != nil {
				return err
			}
			survivorID = survivor.ID
			if confirmed {
				if short, err = s.reserve(ctx, tx, txq, kept, today, nil); err != nil {
					return err
				}
				if err := reservedEvent(ctx, txq, survivor.ID, who, s, short); err != nil {
					return err
				}
			}
			return notes.send(ctx, tx)
		})
	if err != nil {
		return answer(err, mergeAnswers())
	}
	out := gen.MergeManufacturingOrders200JSONResponse{Merged: []gen.ManufacturingOrderLink{}, ShortLines: shortLinesOf(short, s.has(ctx, permCostsView))}
	if out.Order, err = s.orderAnswer(ctx, survivorID); err != nil {
		return answer(err, mergeAnswers())
	}
	rows, err := store.New(s.deps.Pool).OrdersByIDs(ctx, mergedIDs)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out.Merged = append(out.Merged, linkOf(r))
	}
	return out, nil
}

// mergeLines adds the merged orders' lines and by-products to the survivor's
// (M125): the n-th BoM line of a variant and operation, by sort order, to the
// survivor's n-th of the same; an added line, or one the survivor has not, as
// a line of its own.
func (s *server) mergeLines(ctx context.Context, txq *store.Queries, survivorID int64, others []store.ManufacturingOrder) error {
	mine, err := txq.ListOrderComponents(ctx, survivorID)
	if err != nil {
		return err
	}
	byKey := map[lineKey][]int64{}
	for _, c := range mine {
		if !c.Added {
			byKey[keyOf(c)] = append(byKey[keyOf(c)], c.ID)
		}
	}
	outs, err := txq.OutputsOfOrders(ctx, []int64{survivorID})
	if err != nil {
		return err
	}
	byVariant := map[int32]store.ManufacturingOrderOutput{}
	for _, x := range outs {
		byVariant[x.VariantID] = x
	}
	for _, o := range others {
		lines, err := txq.ListOrderComponents(ctx, o.ID)
		if err != nil {
			return err
		}
		nth := map[lineKey]int{}
		for _, c := range lines {
			if !c.Added {
				k := keyOf(c)
				n := nth[k]
				nth[k]++
				if n < len(byKey[k]) {
					if err := txq.AddComponentRequired(ctx, store.AddComponentRequiredParams{ID: byKey[k][n], Required: c.Required}); err != nil {
						return err
					}
					continue
				}
			}
			sort, err := txq.NextComponentSortOrder(ctx, survivorID)
			if err != nil {
				return err
			}
			n, err := txq.InsertOrderComponent(ctx, store.InsertOrderComponentParams{
				OrderID: survivorID, VariantID: c.VariantID, Sku: c.Sku, ProductName: c.ProductName, Unit: c.Unit, Required: c.Required,
				OperationID: c.OperationID, StandardCost: c.StandardCost, SortOrder: sort, Added: c.Added, Note: c.Note,
			})
			if err != nil {
				return err
			}
			if !c.Added {
				byKey[keyOf(c)] = append(byKey[keyOf(c)], n.ID)
			}
		}
		theirs, err := txq.OutputsOfOrders(ctx, []int64{o.ID})
		if err != nil {
			return err
		}
		for _, x := range theirs {
			if m, ok := byVariant[x.VariantID]; ok {
				sum := new(big.Rat).Add(ratOrZero(m.Expected), ratOrZero(x.Expected))
				m.Expected = numeric(sum, quantityPlaces)
				byVariant[x.VariantID] = m
				if err := txq.SetOutputExpected(ctx, store.SetOutputExpectedParams{ID: m.ID, Expected: m.Expected}); err != nil {
					return err
				}
				continue
			}
			n, err := txq.InsertOrderOutput(ctx, store.InsertOrderOutputParams{
				OrderID: survivorID, VariantID: x.VariantID, Sku: x.Sku, ProductName: x.ProductName, Unit: x.Unit, Quantity: x.Quantity,
				CostShare: x.CostShare, Expected: x.Expected, SortOrder: x.SortOrder,
			})
			if err != nil {
				return err
			}
			byVariant[x.VariantID] = n
		}
	}
	return nil
}
