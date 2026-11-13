package manufacturing

// Deviations (manufacturing phase 2 M99; nb avvik), DEV-<n>: opened by a
// failed check in its result's transaction, or by a person from an order, a
// work order or a lot. A quarantining deviation puts a quarantine hold —
// cause deviation, source manufacturing/deviation/<id> — through Inventory's
// ledger on the same transaction, last (rule 24): on every lot its order has
// made so far, on its lot, and, while it is open, on every lot a production
// of its order makes or adds to (productions.go, step 7b). Closing it with
// use_as_is or rework releases its own holds only — another deviation's, a
// recall's or the lot page's stay; scrap and return keep them until release.
//
// The lock order (§15): LockVariants on the order's variants, or the lot's;
// the order FOR UPDATE; the deviation FOR UPDATE; the ledger last, which
// takes the lots FOR NO KEY UPDATE in id order.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// The bounds of a deviation (M99).
const (
	maxDeviationTitle       = 200
	maxDeviationDescription = 5000
	maxClosingNote          = 2000
	maxHoldNote             = 1000
	deviationTitle          = "The deviation was not saved"
	defaultDeviationLimit   = 50
	maxDeviationLimit       = 200
	maxDeviationExport      = 10000
	deviationOpen           = "open"
	deviationClosedStatus   = "closed"
	deviationSourceKind     = "deviation"
	holdKindQuarantine      = "quarantine"
	holdCauseDeviation      = "deviation"
)

// The live topic of the deviations list (§17), keyless: deviation.changed
// with the id.
const (
	liveDeviations        = "manufacturing.deviations"
	eventDeviationChanged = "deviation.changed"
)

// notifyDeviation tells the list that deviation id changed, on q.
func notifyDeviation(ctx context.Context, q live.Execer, id int64) error {
	return live.Notify(ctx, q, liveDeviations, eventDeviationChanged, strconv.FormatInt(id, 10))
}

// deviationNumber is DEV-<n>.
func deviationNumber(n int64) string { return "DEV-" + strconv.FormatInt(n, 10) }

// deviationSource is the deviation as the source of its holds.
func deviationSource(id int64) contracts.StockSource {
	return contracts.StockSource{Module: moduleName, Kind: deviationSourceKind, ID: id}
}

// deviationDraft is a deviation about to be opened.
type deviationDraft struct {
	title, description string
	orderID            *int64
	workOrderID        *int64
	checkResultID      *int64
	lotID              *int64
	lotNumber          *string
	variantID          *int32
	quarantine         bool
}

// heldLot is a lot a deviation holds, with its number.
type heldLot struct {
	id     int64
	number string
}

// openDeviation opens d on tx, after the caller's own rows: the number, the
// row, the order's event and, for a quarantining one, the holds on the lots
// its order made so far and on its lot — the ledger last.
func (s *server) openDeviation(ctx context.Context, tx pgx.Tx, txq *store.Queries, d deviationDraft, who actor, now time.Time) (store.ManufacturingDeviation, error) {
	number, err := txq.NextDeviationNumber(ctx)
	if err != nil {
		return store.ManufacturingDeviation{}, err
	}
	dev, err := txq.InsertDeviation(ctx, store.InsertDeviationParams{
		Number: number, Title: d.title, Description: d.description, OrderID: d.orderID, WorkOrderID: d.workOrderID,
		CheckResultID: d.checkResultID, LotID: d.lotID, LotNumber: d.lotNumber, VariantID: d.variantID, Quarantine: d.quarantine,
		OpenedByUserID: who.id, OpenedByDisplay: who.display, Now: now, StationID: stationOf(ctx),
	})
	if err != nil {
		return dev, err
	}
	if d.orderID != nil {
		if err := event(ctx, txq, *d.orderID, gen.ManufacturingOrderEventKindDeviationOpened, who, now, map[string]any{
			"deviationId": dev.ID, "number": deviationNumber(number), "quarantine": d.quarantine,
		}); err != nil {
			return dev, err
		}
		if err := notifyOrder(ctx, tx, *d.orderID); err != nil {
			return dev, err
		}
	}
	if d.quarantine {
		var lots []heldLot
		if d.orderID != nil {
			made, err := txq.OutputLotsOfOrder(ctx, *d.orderID)
			if err != nil {
				return dev, err
			}
			for _, m := range made {
				lots = append(lots, heldLot{id: m.LotID, number: m.LotNumber})
			}
		}
		if d.lotID != nil && !slices.ContainsFunc(lots, func(l heldLot) bool { return l.id == *d.lotID }) {
			lots = append(lots, heldLot{id: *d.lotID, number: deref(d.lotNumber)})
		}
		if err := s.holdDeviationLots(ctx, tx, txq, dev, lots, who, now); err != nil {
			return dev, err
		}
	}
	return dev, notifyDeviation(ctx, tx, dev.ID)
}

// holdDeviationLots puts dev's quarantine hold on lots through the ledger,
// on tx, and records each on the deviation. A lot held by it already is left
// as it is (HoldLots is idempotent per source and kind).
func (s *server) holdDeviationLots(ctx context.Context, tx pgx.Tx, txq *store.Queries, dev store.ManufacturingDeviation, lots []heldLot, who actor, now time.Time) error {
	if len(lots) == 0 {
		return nil
	}
	ledger, err := s.ledger()
	if err != nil {
		return err
	}
	note := truncate(deviationNumber(dev.Number)+": "+dev.Title, maxHoldNote)
	holds := make([]contracts.LotHold, 0, len(lots))
	for _, l := range lots {
		holds = append(holds, contracts.LotHold{LotID: l.id, Kind: holdKindQuarantine, Cause: holdCauseDeviation, Note: note,
			By: contracts.LotActor{UserID: who.id, Display: who.display}})
	}
	if err := ledger.HoldLots(ctx, tx, deviationSource(dev.ID), holds); err != nil {
		var r *contracts.StockRefusal
		if errors.As(err, &r) {
			return stockRefusal(r, nil)
		}
		return fmt.Errorf("manufacturing: hold the lots of deviation %d: %w", dev.ID, err)
	}
	for _, l := range lots {
		if err := txq.InsertDeviationLot(ctx, store.InsertDeviationLotParams{DeviationID: dev.ID, LotID: l.id, LotNumber: l.number, Now: now}); err != nil {
			return err
		}
	}
	return notifyDeviation(ctx, tx, dev.ID)
}

// holdNewOutput is a production's step 7b (M80, M86): every lot it made or
// added to is held from each open quarantining deviation of its order, on
// the production's transaction, after EnsureLots and before Produce.
func (s *server) holdNewOutput(ctx context.Context, tx pgx.Tx, txq *store.Queries, orderID int64, outs []outputLot, who actor, now time.Time) error {
	if len(outs) == 0 {
		return nil
	}
	devs, err := txq.OpenQuarantineDeviationsOfOrder(ctx, &orderID)
	if err != nil || len(devs) == 0 {
		return err
	}
	lots := make([]heldLot, 0, len(outs))
	for _, o := range outs {
		lots = append(lots, heldLot{id: o.lotID, number: o.number})
	}
	for _, d := range devs {
		if err := s.holdDeviationLots(ctx, tx, txq, d, lots, who, now); err != nil {
			return err
		}
	}
	return nil
}

// withDeviation runs fn on the deviation pre names under its lock order:
// through the order's (withOrder) when it has one, the lot's variant locked
// with it; else LockVariants on its variant alone. fn takes the deviation
// FOR UPDATE.
func (s *server) withDeviation(ctx context.Context, pre store.ManufacturingDeviation,
	fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error) error {
	if pre.OrderID != nil {
		var extra func(context.Context, *store.Queries, store.ManufacturingOrder) ([]int32, error)
		if pre.VariantID != nil {
			extra = func(context.Context, *store.Queries, store.ManufacturingOrder) ([]int32, error) {
				return []int32{*pre.VariantID}, nil
			}
		}
		return s.withOrder(ctx, *pre.OrderID, extra, func(ctx context.Context, tx pgx.Tx, txq *store.Queries, _ *lockedOrder) error {
			return fn(ctx, tx, txq)
		})
	}
	return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if pre.VariantID != nil {
			if _, err := s.lockVariants(ctx, tx, []int32{*pre.VariantID}); err != nil {
				return err
			}
		}
		return fn(ctx, tx, txq)
	})
}

// deviationsOf is the deviations as the contract answers them: their rows,
// read in one snapshot with their lots, orders and work orders, then the
// catalog after it.
func (s *server) deviationsOf(ctx context.Context, rows []store.ManufacturingDeviation) ([]gen.ManufacturingQualityDeviation, error) {
	out := make([]gen.ManufacturingQualityDeviation, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]int64, 0, len(rows))
	var orderIDs, workOrderIDs []int64
	for _, d := range rows {
		ids = append(ids, d.ID)
		if d.OrderID != nil {
			orderIDs = append(orderIDs, *d.OrderID)
		}
		if d.WorkOrderID != nil {
			workOrderIDs = append(workOrderIDs, *d.WorkOrderID)
		}
	}
	lots := map[int64][]gen.ManufacturingDeviationLot{}
	orders := map[int64]store.ManufacturingOrder{}
	names := map[int64]string{}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		held, err := q.LotsOfDeviations(ctx, ids)
		if err != nil {
			return err
		}
		for _, l := range held {
			lots[l.DeviationID] = append(lots[l.DeviationID], gen.ManufacturingDeviationLot{LotId: l.LotID, LotNumber: l.LotNumber, HeldAt: l.HeldAt})
		}
		os, err := q.OrdersByIDs(ctx, slices.Compact(slices.Sorted(slices.Values(orderIDs))))
		if err != nil {
			return err
		}
		for _, o := range os {
			orders[o.ID] = o
		}
		wos, err := q.WorkOrderNames(ctx, slices.Compact(slices.Sorted(slices.Values(workOrderIDs))))
		if err != nil {
			return err
		}
		for _, w := range wos {
			names[w.ID] = fmt.Sprintf("%d · %s", w.Sequence, w.Name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	variants := map[int32]bool{}
	for _, d := range rows {
		if v := deviationVariant(d, orders); v != nil {
			variants[*v] = true
		}
	}
	entries, err := s.catalogVariants(ctx, slices.Sorted(maps.Keys(variants)))
	if err != nil {
		return nil, err
	}
	costs := s.has(ctx, permCostsView)
	for _, d := range rows {
		item := gen.ManufacturingQualityDeviation{
			Id: d.ID, Number: deviationNumber(d.Number), Title: d.Title, Description: d.Description,
			Status: gen.ManufacturingQualityDeviationStatus(d.Status), Quarantine: d.Quarantine, HoldsReleased: d.HoldsReleased,
			WorkOrderId: d.WorkOrderID, CheckResultId: d.CheckResultID, LotId: d.LotID, LotNumber: d.LotNumber,
			Lots: lots[d.ID], OpenedBy: gen.ManufacturingDeviationActor{UserId: d.OpenedByUserID, DisplayName: d.OpenedByDisplay},
			OpenedAt: d.OpenedAt, ClosedAt: d.ClosedAt, ClosingNote: d.ClosingNote, Revision: d.Revision,
		}
		if item.Lots == nil {
			item.Lots = []gen.ManufacturingDeviationLot{}
		}
		if d.Disposition != nil {
			disposition := gen.ManufacturingDeviationDisposition(*d.Disposition)
			item.Disposition = &disposition
		}
		if d.ClosedByDisplay != nil {
			item.ClosedBy = &gen.ManufacturingDeviationActor{UserId: d.ClosedByUserID, DisplayName: *d.ClosedByDisplay}
		}
		if d.OrderID != nil {
			if o, ok := orders[*d.OrderID]; ok {
				link := linkOf(o)
				item.Order = &link
			}
		}
		if d.WorkOrderID != nil {
			if name, ok := names[*d.WorkOrderID]; ok {
				item.WorkOrderName = &name
			}
		}
		if v := deviationVariant(d, orders); v != nil {
			ref := refOf(entries, *v, costs)
			item.Variant = &ref
		}
		out = append(out, item)
	}
	return out, nil
}

// deviationVariant is the product a deviation is about: its lot's variant,
// else its order's.
func deviationVariant(d store.ManufacturingDeviation, orders map[int64]store.ManufacturingOrder) *int32 {
	if d.VariantID != nil {
		return d.VariantID
	}
	if d.OrderID != nil {
		if o, ok := orders[*d.OrderID]; ok {
			return &o.VariantID
		}
	}
	return nil
}

// deviationAnswer is one deviation, 404 when there is none.
func (s *server) deviationAnswer(ctx context.Context, id int64) (gen.ManufacturingQualityDeviation, error) {
	d, err := store.New(s.deps.Pool).GetDeviation(ctx, id)
	if noRows(err) {
		return gen.ManufacturingQualityDeviation{}, notFound()
	}
	if err != nil {
		return gen.ManufacturingQualityDeviation{}, err
	}
	out, err := s.deviationsOf(ctx, []store.ManufacturingDeviation{d})
	if err != nil {
		return gen.ManufacturingQualityDeviation{}, err
	}
	return out[0], nil
}

// GetManufacturingDeviation is one deviation (M99).
func (s *server) GetManufacturingDeviation(ctx context.Context, req gen.GetManufacturingDeviationRequestObject) (gen.GetManufacturingDeviationResponseObject, error) {
	out, err := s.deviationAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetManufacturingDeviationResponseObject]{
			notFound: func() gen.GetManufacturingDeviationResponseObject { return gen.GetManufacturingDeviation404Response{} },
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingDeviationResponseObject {
				return gen.GetManufacturingDeviation503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetManufacturingDeviation200JSONResponse(out), nil
}

// ListManufacturingDeviations is a page of deviations by the filters, newest
// first, or every match as CSV (M99).
func (s *server) ListManufacturingDeviations(ctx context.Context, req gen.ListManufacturingDeviationsRequestObject) (gen.ListManufacturingDeviationsResponseObject, error) {
	reply := answers[gen.ListManufacturingDeviationsResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.ListManufacturingDeviationsResponseObject {
			return gen.ListManufacturingDeviations400ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ListManufacturingDeviationsResponseObject {
			return gen.ListManufacturingDeviations503ApplicationProblemPlusJSONResponse(p)
		},
	}
	p := req.Params
	params := store.ListDeviationsParams{OrderID: p.OrderId, VariantID: p.VariantId, LotID: p.LotId, PageLimit: defaultDeviationLimit}
	if p.Status != nil {
		status := string(*p.Status)
		params.Status = &status
	}
	if p.From != nil && p.To != nil && p.From.After(p.To.Time) {
		return answer(refuseField("The deviations were not listed", "from", "", "Must not be after to"), reply)
	}
	if p.From != nil || p.To != nil {
		zone, err := s.businessZone(ctx)
		if err != nil {
			return answer(err, reply)
		}
		if p.From != nil {
			from := dayStart(civilDay(p.From.Time), zone)
			params.FromAt = &from
		}
		if p.To != nil {
			to := dayStart(civilDay(p.To.Time).AddDate(0, 0, 1), zone)
			params.ToAt = &to
		}
	}
	csv := p.Format != nil && *p.Format == gen.ListManufacturingDeviationsParamsFormatCsv
	switch {
	case csv:
		params.PageLimit = maxDeviationExport
	case p.Limit != nil:
		params.PageLimit = min(max(*p.Limit, 1), maxDeviationLimit)
	}
	if !csv && p.Offset != nil {
		params.PageOffset = max(*p.Offset, 0)
	}
	var rows []store.ManufacturingDeviation
	var total int32
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		list, err := q.ListDeviations(ctx, params)
		if err != nil {
			return err
		}
		for _, r := range list {
			rows = append(rows, r.ManufacturingDeviation)
		}
		total, err = q.CountDeviations(ctx, store.CountDeviationsParams{Status: params.Status, OrderID: params.OrderID, VariantID: params.VariantID,
			LotID: params.LotID, FromAt: params.FromAt, ToAt: params.ToAt})
		return err
	})
	if err != nil {
		return nil, err
	}
	items, err := s.deviationsOf(ctx, rows)
	if err != nil {
		return answer(err, reply)
	}
	if csv {
		return csvDownload{body: deviationsCSV(items), fileName: "manufacturing-deviations.csv"}, nil
	}
	return gen.ListManufacturingDeviations200JSONResponse{Items: items, Total: total}, nil
}

func (d csvDownload) VisitListManufacturingDeviationsResponse(w http.ResponseWriter) error {
	return d.write(w)
}

// deviationsCSV is the list as a file: one row per deviation.
func deviationsCSV(items []gen.ManufacturingQualityDeviation) []byte {
	header := []string{"Number", "Status", "Opened", "Opened by", "Title", "Order", "Work order", "SKU", "Product", "Lot", "Quarantine",
		"Disposition", "Closed", "Closed by", "Closing note"}
	rows := make([][]string, 0, len(items))
	for _, d := range items {
		row := []string{d.Number, string(d.Status), d.OpenedAt.UTC().Format(time.RFC3339), d.OpenedBy.DisplayName, d.Title}
		order, sku, product, disposition, closed, closedBy := "", "", "", "", "", ""
		if d.Order != nil {
			order = d.Order.Number
		}
		if d.Variant != nil {
			sku, product = d.Variant.Sku, d.Variant.ProductName
		}
		if d.Disposition != nil {
			disposition = string(*d.Disposition)
		}
		if d.ClosedAt != nil {
			closed = d.ClosedAt.UTC().Format(time.RFC3339)
		}
		if d.ClosedBy != nil {
			closedBy = d.ClosedBy.DisplayName
		}
		lot := deref(d.LotNumber)
		if lot == "" && len(d.Lots) > 0 {
			numbers := make([]string, 0, len(d.Lots))
			for _, l := range d.Lots {
				numbers = append(numbers, l.LotNumber)
			}
			lot = strings.Join(numbers, ", ")
		}
		quarantine := "no"
		if d.Quarantine {
			quarantine = "yes"
		}
		row = append(row, order, deref(d.WorkOrderName), sku, product, lot, quarantine, disposition, closed, closedBy, deref(d.ClosingNote))
		rows = append(rows, row)
	}
	return csvFile(header, rows)
}

// createDeviationAnswers are a hand-reported deviation's typed refusals.
func createDeviationAnswers() answers[gen.CreateManufacturingDeviationResponseObject] {
	return answers[gen.CreateManufacturingDeviationResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CreateManufacturingDeviationResponseObject {
			return gen.CreateManufacturingDeviation400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.ManufacturingProblem) gen.CreateManufacturingDeviationResponseObject {
			return gen.CreateManufacturingDeviation403ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.ManufacturingProblem) gen.CreateManufacturingDeviationResponseObject {
			return gen.CreateManufacturingDeviation409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CreateManufacturingDeviationResponseObject {
			return gen.CreateManufacturingDeviation503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// CreateManufacturingDeviation reports a deviation by hand (M99): from an
// order, a work order (whose order it takes) or a lot, read before the
// transaction; then under the lock order the deviation and, quarantining,
// its holds.
func (s *server) CreateManufacturingDeviation(ctx context.Context, req gen.CreateManufacturingDeviationRequestObject) (gen.CreateManufacturingDeviationResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Reporting a deviation"); err != nil {
		return answer(err, createDeviationAnswers())
	}
	in := *req.Body
	f := s.unknownProperties(ctx, deviationInputSchema)
	d := deviationDraft{title: strings.TrimSpace(in.Title), quarantine: in.Quarantine != nil && *in.Quarantine}
	if n := utf8.RuneCountInString(d.title); n < 1 || n > maxDeviationTitle {
		f.add("title", "1–200 characters")
	}
	if in.Description != nil {
		d.description = strings.TrimSpace(*in.Description)
		if utf8.RuneCountInString(d.description) > maxDeviationDescription {
			f.add("description", "At most 5000 characters")
		}
	}
	pool := store.New(s.deps.Pool)
	if in.WorkOrderId != nil {
		w, err := pool.GetWorkOrder(ctx, *in.WorkOrderId)
		switch {
		case noRows(err):
			f.add("workOrderId", "No work order has this id")
		case err != nil:
			return nil, err
		case in.OrderId != nil && *in.OrderId != w.OrderID:
			f.add("workOrderId", "The work order is not the order's")
		default:
			d.workOrderID, d.orderID = &w.ID, &w.OrderID
		}
	}
	if in.OrderId != nil && d.orderID == nil {
		if _, err := pool.GetOrder(ctx, *in.OrderId); noRows(err) {
			f.add("orderId", "No order has this id")
		} else if err != nil {
			return nil, err
		} else {
			d.orderID = in.OrderId
		}
	}
	if in.LotId != nil {
		// A lot is Inventory's: reporting on one needs what its lot page
		// does (M99).
		if !contracts.HasPermissions(ctx, s.deps.Access, permInventoryLocationsView, permInventoryStockView) {
			return answer(refuse(problem(http.StatusForbidden, codeLotsViewRequired, "The lot is not yours to see",
				"Reporting a deviation on a lot needs inventory:locations-view and inventory:stock-view.")), createDeviationAnswers())
		}
		refs, err := s.lotRefs(ctx, []int64{*in.LotId})
		if err != nil {
			return answer(err, createDeviationAnswers())
		}
		if len(refs) != 1 {
			f.add("lotId", "No lot has this id")
		} else {
			d.lotID, d.lotNumber, d.variantID = &refs[0].ID, &refs[0].Number, &refs[0].VariantID
		}
	}
	if f.failed() {
		return answer(refuseInvalid(deviationTitle, f), createDeviationAnswers())
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	var created store.ManufacturingDeviation
	open := func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		var err error
		created, err = s.openDeviation(ctx, tx, txq, d, who, s.deps.Clock())
		return err
	}
	pre := store.ManufacturingDeviation{OrderID: d.orderID, VariantID: d.variantID}
	err = s.withDeviation(ctx, pre, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if d.orderID != nil {
			o, err := txq.GetOrder(ctx, *d.orderID)
			if err != nil {
				return err
			}
			if o.Status == orderDraft {
				return refuseField(deviationTitle, "orderId", "", "A draft order has no work to deviate from; confirm it first")
			}
			// With both, the lot is one the order made or consumed.
			if d.lotID != nil {
				ofOrder, err := txq.LotOfOrder(ctx, store.LotOfOrderParams{OrderID: o.ID, LotID: *d.lotID})
				if err != nil {
					return err
				}
				if !ofOrder {
					return refuseField(deviationTitle, "lotId", codeLotNotOfOrder, "The lot is not one the order made or consumed")
				}
			}
		}
		return open(ctx, tx, txq)
	})
	if err != nil {
		return answer(err, createDeviationAnswers())
	}
	out, err := s.deviationAnswer(ctx, created.ID)
	if err != nil {
		return answer(err, createDeviationAnswers())
	}
	return gen.CreateManufacturingDeviation201JSONResponse(out), nil
}

// closeDeviationAnswers are a close's and a release's typed refusals.
func closeDeviationAnswers() answers[gen.CloseManufacturingDeviationResponseObject] {
	return answers[gen.CloseManufacturingDeviationResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.CloseManufacturingDeviationResponseObject {
			return gen.CloseManufacturingDeviation400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.CloseManufacturingDeviationResponseObject {
			return gen.CloseManufacturingDeviation404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.CloseManufacturingDeviationResponseObject {
			return gen.CloseManufacturingDeviation409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.CloseManufacturingDeviationResponseObject {
			return gen.CloseManufacturingDeviation503ApplicationProblemPlusJSONResponse(p)
		},
	}
}

// deviationIsClosed is the 409 deviation_closed.
func deviationIsClosed(detail string) error {
	return conflict(codeDeviationClosed, "The deviation is closed", detail)
}

// CloseManufacturingDeviation closes the deviation with its disposition and
// note (M99): use_as_is and rework release its own holds through the ledger,
// last; scrap and return keep them.
func (s *server) CloseManufacturingDeviation(ctx context.Context, req gen.CloseManufacturingDeviationRequestObject) (gen.CloseManufacturingDeviationResponseObject, error) {
	in := *req.Body
	f := s.unknownProperties(ctx, deviationCloseInputSchema)
	disposition := string(in.Disposition)
	if !slices.Contains([]string{"use_as_is", "rework", "scrap", "return"}, disposition) {
		f.add("disposition", "The disposition is use_as_is, rework, scrap or return")
	}
	note := strings.TrimSpace(in.ClosingNote)
	if n := utf8.RuneCountInString(note); n < 1 || n > maxClosingNote {
		f.add("closingNote", "1–2000 characters")
	}
	if f.failed() {
		return answer(refuseInvalid("The deviation was not closed", f), closeDeviationAnswers())
	}
	pre, err := store.New(s.deps.Pool).GetDeviation(ctx, req.Id)
	if noRows(err) {
		return gen.CloseManufacturingDeviation404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	release := disposition == "use_as_is" || disposition == "rework"
	err = s.withDeviation(ctx, pre, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		d, err := txq.GetDeviationForUpdate(ctx, req.Id)
		if err != nil {
			return err
		}
		if d.Status != deviationOpen {
			return deviationIsClosed("The deviation was closed already; nothing was changed.")
		}
		if d.Revision != in.Revision {
			return revisionConflict("deviation", d.Revision, in.Revision)
		}
		now := s.deps.Clock()
		if _, err := txq.CloseDeviation(ctx, store.CloseDeviationParams{ID: d.ID, Disposition: &disposition, ClosingNote: &note,
			ClosedByUserID: who.id, ClosedByDisplay: &who.display, Now: &now, HoldsReleased: release}); err != nil {
			return err
		}
		if d.OrderID != nil {
			if err := event(ctx, txq, *d.OrderID, gen.ManufacturingOrderEventKindDeviationClosed, who, now, map[string]any{
				"deviationId": d.ID, "number": deviationNumber(d.Number), "disposition": disposition,
			}); err != nil {
				return err
			}
			if err := notifyOrder(ctx, tx, *d.OrderID); err != nil {
				return err
			}
		}
		if release {
			if err := s.releaseDeviationHolds(ctx, tx, d.ID, who); err != nil {
				return err
			}
		}
		return notifyDeviation(ctx, tx, d.ID)
	})
	if err != nil {
		return answer(err, closeDeviationAnswers())
	}
	out, err := s.deviationAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, closeDeviationAnswers())
	}
	return gen.CloseManufacturingDeviation200JSONResponse(out), nil
}

// releaseDeviationHolds releases every hold the deviation put, through the
// ledger, last: holds of any other source stay (M64).
func (s *server) releaseDeviationHolds(ctx context.Context, tx pgx.Tx, id int64, who actor) error {
	ledger, err := s.ledger()
	if err != nil {
		return err
	}
	if err := ledger.ReleaseLotHolds(ctx, tx, deviationSource(id), nil, contracts.LotActor{UserID: who.id, Display: who.display}); err != nil {
		var r *contracts.StockRefusal
		if errors.As(err, &r) {
			return stockRefusal(r, nil)
		}
		return fmt.Errorf("manufacturing: release the holds of deviation %d: %w", id, err)
	}
	return nil
}

// ReleaseManufacturingDeviationHolds releases the holds a closed deviation
// kept, closed as scrap or return (M99). A release done already answers the
// deviation as it is.
func (s *server) ReleaseManufacturingDeviationHolds(ctx context.Context, req gen.ReleaseManufacturingDeviationHoldsRequestObject) (gen.ReleaseManufacturingDeviationHoldsResponseObject, error) {
	reply := answers[gen.ReleaseManufacturingDeviationHoldsResponseObject]{
		badRequest: func(p gen.ManufacturingProblem) gen.ReleaseManufacturingDeviationHoldsResponseObject {
			return gen.ReleaseManufacturingDeviationHolds400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.ReleaseManufacturingDeviationHoldsResponseObject {
			return gen.ReleaseManufacturingDeviationHolds404Response{}
		},
		conflict: func(p gen.ManufacturingProblem) gen.ReleaseManufacturingDeviationHoldsResponseObject {
			return gen.ReleaseManufacturingDeviationHolds409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.ManufacturingProblem) gen.ReleaseManufacturingDeviationHoldsResponseObject {
			return gen.ReleaseManufacturingDeviationHolds503ApplicationProblemPlusJSONResponse(p)
		},
	}
	if f := s.unknownProperties(ctx, revisionInputSchema); f.failed() {
		return answer(refuseInvalid("The holds were not released", f), reply)
	}
	pre, err := store.New(s.deps.Pool).GetDeviation(ctx, req.Id)
	if noRows(err) {
		return gen.ReleaseManufacturingDeviationHolds404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withDeviation(ctx, pre, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		d, err := txq.GetDeviationForUpdate(ctx, req.Id)
		if err != nil {
			return err
		}
		switch {
		case d.Status == deviationOpen:
			return conflict(codeDeviationClosed, "The deviation is open", "An open deviation's holds go when it is closed; close it first.")
		case d.HoldsReleased:
			return nil
		case d.Revision != req.Body.Revision:
			return revisionConflict("deviation", d.Revision, req.Body.Revision)
		}
		if _, err := txq.MarkDeviationReleased(ctx, d.ID); err != nil {
			return err
		}
		if err := s.releaseDeviationHolds(ctx, tx, d.ID, who); err != nil {
			return err
		}
		return notifyDeviation(ctx, tx, d.ID)
	})
	if err != nil {
		return answer(err, reply)
	}
	out, err := s.deviationAnswer(ctx, req.Id)
	if err != nil {
		return answer(err, reply)
	}
	return gen.ReleaseManufacturingDeviationHolds200JSONResponse(out), nil
}
