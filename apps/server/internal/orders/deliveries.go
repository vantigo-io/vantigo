package orders

// Deliveries (leveranse, orders design O47): what of a confirmed order leaves
// its site. A draft is created proposing every open goods line at what is
// available, edited and picked — each line's from-location — and posted or
// cancelled. Posting issues the stock through contracts.StockLedger on the
// order's transaction (module-boundaries rule 24), adds to the order lines'
// delivered quantities, publishes orders.delivery.posted.v1 and stores the
// packing slip after the commit. A posted delivery is immutable; what is not
// delivered stays open on the order as its back order (restordre), and no
// back-order document is made.
//
// Every write locks in the one order: the order lines' variants
// (ProductCatalog.LockVariants, MB rule 21), the order's row, the delivery's,
// then — inside the ledger — Inventory's balances and reservations.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The delivery states (O47).
const (
	deliveryDraft     = "draft"
	deliveryPosted    = "posted"
	deliveryCancelled = "cancelled"
)

// ownerDelivery is a delivery as orders.documents names its owner, and the
// kind its stock movement's document is.
const ownerDelivery = "delivery"

// docPackingSlip is a delivery's stored packing slip (O47).
const docPackingSlip = "packing_slip"

// The deliveries' refusal codes.
const (
	codeDeliveryNotDraft = "delivery_not_draft"
	codeNothingToDeliver = "nothing_to_deliver"
	codeLocationMissing  = "location_missing"
	codeLocationUnknown  = contracts.StockLocationUnknown
)

// deliveryStock is a delivery as the ledger names the movement it posts.
func deliveryStock(id int64) contracts.StockSource {
	return contracts.StockSource{Module: moduleName, Kind: ownerDelivery, ID: id}
}

// deliveryAnswers are the typed refusals most delivery writes share.
func deliveryAnswers[R any](bad func(gen.OrdersProblem) R, nf func() R, cf func(gen.OrdersProblem) R, un func(gen.OrdersProblem) R) answers[R] {
	return answers[R]{badRequest: bad, notFound: nf, conflict: cf, unavailable: un}
}

// judgeDelivery refuses a write of a delivery that is not a draft, or made
// from a version of it that is not its current one.
func judgeDelivery(d store.OrdersDelivery, version int32, expected time.Time) error {
	if d.Version != version {
		return versionConflict("delivery's version", d.Version, version)
	}
	if err := judgeFresh("delivery", d.UpdatedAt, expected); err != nil {
		return err
	}
	if d.Status != deliveryDraft {
		return conflict(codeDeliveryNotDraft, "The delivery is not a draft",
			fmt.Sprintf("Delivery %d is %s; a posted delivery is immutable.", d.Number, d.Status))
	}
	return nil
}

// judgeDeliverable refuses a delivery of an order that is not confirmed, is
// on hold (O18) or names no site.
func (s *server) judgeDeliverable(o store.OrdersOrder) error {
	if err := judgeConfirmed(o); err != nil {
		return err
	}
	if o.OnHold {
		return conflict(codeOrderOnHold, "The order is on hold",
			fmt.Sprintf("Order %d is on hold; release it before delivering.", o.Number))
	}
	if !s.atSite(o.SiteID) {
		return conflict(codeOrderHasNoSite, "The order has no site",
			fmt.Sprintf("Order %d names no site its stock is kept at; its lines are delivered by hand.", o.Number))
	}
	return nil
}

// draftedElsewhere is, per order line, what the order's other draft
// deliveries hold of it.
func draftedElsewhere(ctx context.Context, q *store.Queries, orderID, exceptID int64) (map[int64]*big.Rat, error) {
	rows, err := q.DraftDeliveredOfOrder(ctx, store.DraftDeliveredOfOrderParams{OrderID: orderID, ExceptID: exceptID})
	if err != nil {
		return nil, fmt.Errorf("orders: read order %d's draft deliveries: %w", orderID, err)
	}
	out := map[int64]*big.Rat{}
	for _, r := range rows {
		out[r.OrderLineID] = ratOrZero(r.Quantity)
	}
	return out, nil
}

// openForDelivery is what of an order line a delivery may take: ordered less
// cancelled less delivered less what other drafts hold, never below 0.
func openForDelivery(l store.OrdersLine, drafted map[int64]*big.Rat) *big.Rat {
	open := openToDeliver(l)
	if d := drafted[l.ID]; d != nil {
		open.Sub(open, d)
	}
	if open.Sign() < 0 {
		return new(big.Rat)
	}
	return open
}

// goodsVariantsOf are the variants of the order's goods lines at its site.
func goodsVariantsOf(lines []store.OrdersLine) []int32 {
	var ids []int32
	for _, l := range lines {
		if reservable(l) && !slices.Contains(ids, *l.VariantID) {
			ids = append(ids, *l.VariantID)
		}
	}
	slices.Sort(ids)
	return ids
}

// deliveryView is a delivery as the API answers it: o is its order, lines
// its order's lines, drafted what other drafts hold of them, and locations,
// on a draft, the site's locations holding each variant.
func deliveryView(d store.OrdersDelivery, o store.OrdersOrder, dlines []store.OrdersDeliveryLine, lines []store.OrdersLine,
	drafted map[int64]*big.Rat, locations map[int32][]contracts.StockLocation) gen.OrdersDelivery {
	h := headerOfOrder(o)
	out := gen.OrdersDelivery{
		Id: d.ID, Number: d.Number, OrderId: o.ID, OrderNumber: o.Number, CustomerId: o.CustomerID, CustomerName: o.CustomerName,
		OrderOnHold: o.OnHold, SiteId: d.SiteID, Status: gen.OrdersDeliveryStatus(d.Status), MovementId: d.MovementID,
		PostedAt: d.PostedAt, PostedBy: personOut(d.PostedByUserID, d.PostedByDisplay), CancelledAt: d.CancelledAt,
		PackingSlipStored: d.PackingSlipDocumentID != nil, Version: d.Version, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		CreatedBy: personOut(d.CreatedByUserID, d.CreatedByDisplay),
		DeliveryAddress: &gen.OrdersAddress{
			Name: h.DeliveryName, Line1: h.DeliveryAddressLine1, Line2: h.DeliveryAddressLine2,
			PostalCode: h.DeliveryPostalCode, City: h.DeliveryCity, Country: h.DeliveryCountry,
		},
		Lines: make([]gen.OrdersDeliveryLine, 0, len(dlines)),
	}
	byLine := byID(lines)
	for _, dl := range dlines {
		l := byLine[dl.OrderLineID]
		line := gen.OrdersDeliveryLine{
			Id: dl.ID, Position: dl.Position, OrderLineId: dl.OrderLineID, VariantId: dl.VariantID,
			Sku: l.Sku, Name: l.Name, Unit: l.Unit, Quantity: wire(ratOrZero(dl.Quantity), quantityPlaces),
			Ordered: wire(ratOrZero(l.Quantity), quantityPlaces), Open: wire(openForDelivery(l, drafted), quantityPlaces),
			FromLocationId: dl.FromLocationID, FromLocationCode: dl.FromLocationCode, Locations: []gen.OrdersStockLocation{},
		}
		for _, loc := range locations[dl.VariantID] {
			line.Locations = append(line.Locations, gen.OrdersStockLocation{
				LocationId: loc.LocationID, FullCode: loc.FullCode, Quantity: wire(mustRat(loc.Quantity), quantityPlaces),
			})
		}
		out.Lines = append(out.Lines, line)
	}
	return out
}

// deliveryRead is a delivery with everything its view and its PDFs need,
// read in one snapshot.
type deliveryRead struct {
	d       store.OrdersDelivery
	o       store.OrdersOrder
	dlines  []store.OrdersDeliveryLine
	lines   []store.OrdersLine
	drafted map[int64]*big.Rat
}

// readDelivery reads a delivery, its order and both's lines on q; notFound
// when there is none.
func readDelivery(ctx context.Context, q *store.Queries, id int64) (deliveryRead, error) {
	var r deliveryRead
	d, err := q.GetDelivery(ctx, id)
	if noRows(err) {
		return r, notFound()
	}
	if err != nil {
		return r, fmt.Errorf("orders: read delivery %d: %w", id, err)
	}
	r.d = d
	if r.o, r.lines, err = readOrder(ctx, q, d.OrderID); err != nil {
		return r, err
	}
	if r.dlines, err = q.DeliveryLinesOf(ctx, d.ID); err != nil {
		return r, fmt.Errorf("orders: read delivery %d's lines: %w", d.ID, err)
	}
	r.drafted, err = draftedElsewhere(ctx, q, d.OrderID, d.ID)
	return r, err
}

// deliveryLocations are the site's locations holding the delivery's
// variants, read through contracts.Stock outside any transaction: a draft's
// picker and the pick list. None for a delivery no longer a draft, or when
// stock is not kept.
func (s *server) deliveryLocations(ctx context.Context, r deliveryRead, always bool) (map[int32][]contracts.StockLocation, error) {
	if (r.d.Status != deliveryDraft && !always) || len(r.dlines) == 0 {
		return nil, nil
	}
	var ids []int32
	for _, dl := range r.dlines {
		if !slices.Contains(ids, dl.VariantID) {
			ids = append(ids, dl.VariantID)
		}
	}
	slices.Sort(ids)
	return s.stockLocations(ctx, ids, r.d.SiteID)
}

// ListDeliveries List deliveries
// (GET /api/v1/orders/deliveries)
func (s *server) ListDeliveries(ctx context.Context, req gen.ListDeliveriesRequestObject) (gen.ListDeliveriesResponseObject, error) {
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
		return gen.ListDeliveries400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	statuses := []string{}
	if p.Status != nil {
		for _, st := range *p.Status {
			statuses = append(statuses, string(st))
		}
	}
	search := ""
	if p.Q != nil {
		search = strings.TrimSpace(*p.Q)
	}
	out := gen.OrdersDeliveryPage{Items: []gen.OrdersDeliverySummary{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.ListDeliveries(ctx, store.ListDeliveriesParams{
			Statuses: statuses, OrderID: p.OrderId, SiteID: p.SiteId, Q: search, PageSize: size, PageOffset: (page - 1) * size,
		})
		if err != nil {
			return fmt.Errorf("orders: list deliveries: %w", err)
		}
		if out.Total, err = q.CountDeliveries(ctx, store.CountDeliveriesParams{Statuses: statuses, OrderID: p.OrderId, SiteID: p.SiteId, Q: search}); err != nil {
			return fmt.Errorf("orders: count deliveries: %w", err)
		}
		for _, r := range rows {
			d := r.OrdersDelivery
			out.Items = append(out.Items, gen.OrdersDeliverySummary{
				Id: d.ID, Number: d.Number, OrderId: d.OrderID, OrderNumber: r.OrderNumber, CustomerId: r.CustomerID,
				CustomerName: r.CustomerName, SiteId: d.SiteID, Status: gen.OrdersDeliverySummaryStatus(d.Status),
				Lines: r.Lines, PostedAt: d.PostedAt, UpdatedAt: d.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.ListDeliveries200JSONResponse(out), nil
}

// GetDelivery Get a delivery
// (GET /api/v1/orders/deliveries/{id})
func (s *server) GetDelivery(ctx context.Context, req gen.GetDeliveryRequestObject) (gen.GetDeliveryResponseObject, error) {
	out, err := s.deliveryOut(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetDeliveryResponseObject]{notFound: func() gen.GetDeliveryResponseObject { return gen.GetDelivery404Response{} }})
	}
	return gen.GetDelivery200JSONResponse(out), nil
}

// deliveryOut reads a delivery in one snapshot and answers it, a draft's
// lines with the site's locations holding each variant.
func (s *server) deliveryOut(ctx context.Context, id int64) (gen.OrdersDelivery, error) {
	var r deliveryRead
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		r, err = readDelivery(ctx, q, id)
		return err
	})
	if err != nil {
		return gen.OrdersDelivery{}, err
	}
	locations, err := s.deliveryLocations(ctx, r, false)
	if err != nil {
		return gen.OrdersDelivery{}, err
	}
	return deliveryView(r.d, r.o, r.dlines, r.lines, r.drafted, locations), nil
}

// CreateDelivery Create a delivery
// (POST /api/v1/orders/deliveries)
//
// Every open goods line of the order is proposed at what of it can go from
// the site: what the line holds reserved, and what is available there beyond
// every reservation, shared in line order between the lines of one variant —
// never more than is open. Each proposed line is picked from the first
// location, by full code, that holds all of it, else none. Lines nothing can
// go of are left out; an order nothing can go of is refused
// (nothing_to_deliver).
func (s *server) CreateDelivery(ctx context.Context, req gen.CreateDeliveryRequestObject) (gen.CreateDeliveryResponseObject, error) {
	resp := deliveryAnswers(
		func(p gen.OrdersProblem) gen.CreateDeliveryResponseObject {
			return gen.CreateDelivery400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.CreateDeliveryResponseObject { return gen.CreateDelivery404Response{} },
		func(p gen.OrdersProblem) gen.CreateDeliveryResponseObject {
			return gen.CreateDelivery409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.CreateDeliveryResponseObject {
			return gen.CreateDelivery503ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, deliveryInputSchema); f.failed() {
		return gen.CreateDelivery400ApplicationProblemPlusJSONResponse(invalid(invalidDeliveryTitle, f)), nil
	}
	if !s.stockKept() {
		return answer(errDependencyUnavailable, resp)
	}
	cur, curLines, err := readOrder(ctx, store.New(s.deps.Pool), req.Body.OrderId)
	if err != nil {
		return answer(err, resp)
	}
	if err := s.judgeDeliverable(cur); err != nil {
		return answer(err, resp)
	}
	variants := goodsVariantsOf(curLines)
	available, err := s.stockAvailable(ctx, variants, *cur.SiteID)
	if err != nil {
		return nil, err
	}
	locations, err := s.stockLocations(ctx, variants, *cur.SiteID)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.OrdersDelivery
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := s.lockVariants(ctx, tx, variants); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, cur.ID)
		if err != nil {
			return err
		}
		if err := s.judgeDeliverable(o); err != nil {
			return err
		}
		if !equalInt64(o.SiteID, cur.SiteID) {
			return conflict(codeVersionConflict, "The order changed",
				fmt.Sprintf("Order %d's site changed while the delivery was being made. Try again.", o.Number))
		}
		lines, err := linesOf(ctx, txq, ownerOrder, o.ID)
		if err != nil {
			return err
		}
		drafted, err := draftedElsewhere(ctx, txq, o.ID, 0)
		if err != nil {
			return err
		}
		reserved, err := s.reservedByLine(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		free := map[int32]*big.Rat{}
		for id, q := range available {
			free[id] = new(big.Rat).Set(q)
		}
		type proposed struct {
			line store.OrdersLine
			qty  *big.Rat
		}
		var picks []proposed
		for _, l := range lines {
			if !reservable(l) {
				continue
			}
			open := openForDelivery(l, drafted)
			if open.Sign() <= 0 {
				continue
			}
			can := new(big.Rat).Set(orZero(reserved[l.ID]))
			if f := free[*l.VariantID]; f != nil && f.Sign() > 0 {
				can.Add(can, f)
			}
			qty := open
			if qty.Cmp(can) > 0 {
				qty = can
			}
			if qty.Sign() <= 0 {
				continue
			}
			// What went beyond the line's own reservation came from what is
			// free at the site.
			if extra := new(big.Rat).Sub(qty, orZero(reserved[l.ID])); extra.Sign() > 0 && free[*l.VariantID] != nil {
				free[*l.VariantID].Sub(free[*l.VariantID], extra)
			}
			picks = append(picks, proposed{line: l, qty: qty})
		}
		if len(picks) == 0 {
			return conflict(codeNothingToDeliver, "Nothing to deliver",
				fmt.Sprintf("Nothing of order %d is open and available at its site.", o.Number))
		}
		number, err := allocateNumber(ctx, txq, seriesDelivery, 1)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		d, err := txq.InsertDelivery(ctx, store.InsertDeliveryParams{
			Number: number, OrderID: o.ID, SiteID: *o.SiteID, CreatedByUserID: by.id, CreatedByDisplay: by.display, Now: now,
		})
		if err != nil {
			return fmt.Errorf("orders: insert order %d's delivery: %w", o.ID, err)
		}
		dlines := make([]store.OrdersDeliveryLine, 0, len(picks))
		for i, p := range picks {
			var from *int64
			var code *string
			for _, loc := range locations[*p.line.VariantID] {
				if mustRat(loc.Quantity).Cmp(p.qty) >= 0 {
					from, code = &loc.LocationID, &loc.FullCode
					break
				}
			}
			row, err := txq.InsertDeliveryLine(ctx, store.InsertDeliveryLineParams{
				DeliveryID: d.ID, Position: int32(i + 1), OrderLineID: p.line.ID, VariantID: *p.line.VariantID,
				FromLocationID: from, FromLocationCode: code, Quantity: numeric(p.qty, quantityPlaces),
			})
			if err != nil {
				return fmt.Errorf("orders: insert delivery %d's line: %w", d.ID, err)
			}
			dlines = append(dlines, row)
		}
		out = deliveryView(d, o, dlines, lines, drafted, locations)
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.CreateDelivery201JSONResponse(out), nil
}

// UpdateDelivery Save a delivery
// (PUT /api/v1/orders/deliveries/{id})
//
// The lines are replaced whole. Each names a goods line of the order, once
// (line_invalid), takes at most what is open of it outside this delivery
// (over_delivery), and is picked from a location of the site holding its
// variant (location_unknown) or not yet.
func (s *server) UpdateDelivery(ctx context.Context, req gen.UpdateDeliveryRequestObject) (gen.UpdateDeliveryResponseObject, error) {
	resp := deliveryAnswers(
		func(p gen.OrdersProblem) gen.UpdateDeliveryResponseObject {
			return gen.UpdateDelivery400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.UpdateDeliveryResponseObject { return gen.UpdateDelivery404Response{} },
		func(p gen.OrdersProblem) gen.UpdateDeliveryResponseObject {
			return gen.UpdateDelivery409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.UpdateDeliveryResponseObject {
			return gen.UpdateDelivery503ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, deliveryUpdateSchema); f.failed() {
		return gen.UpdateDelivery400ApplicationProblemPlusJSONResponse(invalid(invalidDeliveryTitle, f)), nil
	}
	body := *req.Body
	if len(body.Lines) == 0 || len(body.Lines) > maxLines {
		return answer(refuseField(invalidDeliveryTitle, "lines", "", "One to 500 lines"), resp)
	}
	var cur deliveryRead
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		cur, err = readDelivery(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeDelivery(cur.d, body.Version, body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	if !s.stockKept() {
		return answer(errDependencyUnavailable, resp)
	}
	variants := goodsVariantsOf(cur.lines)
	locations, err := s.stockLocations(ctx, variants, cur.d.SiteID)
	if err != nil {
		return nil, err
	}
	var out gen.OrdersDelivery
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := s.lockVariants(ctx, tx, variants); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, cur.o.ID)
		if err != nil {
			return err
		}
		d, err := txq.LockDelivery(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("orders: lock delivery %d: %w", req.Id, err)
		}
		if err := judgeDelivery(d, body.Version, body.ExpectedUpdatedAt); err != nil {
			return err
		}
		lines, err := linesOf(ctx, txq, ownerOrder, o.ID)
		if err != nil {
			return err
		}
		drafted, err := draftedElsewhere(ctx, txq, o.ID, d.ID)
		if err != nil {
			return err
		}
		byLine := byID(lines)
		seen := map[int64]bool{}
		var failures []lineFailure
		type written struct {
			line     store.OrdersLine
			qty      *big.Rat
			from     *int64
			fromCode *string
		}
		var writes []written
		for i, in := range body.Lines {
			l, ok := byLine[in.OrderLineId]
			switch {
			case !ok || !reservable(l):
				failures = append(failures, lineFailure{index: i, code: codeLineNotFound,
					detail: fmt.Sprintf("Line %d is not a goods line of order %d delivered from its site.", in.OrderLineId, o.Number)})
				continue
			case seen[l.ID]:
				failures = append(failures, lineFailure{index: i, code: codeLineInvalid,
					detail: fmt.Sprintf("Line %d is named twice.", l.Position)})
				continue
			}
			seen[l.ID] = true
			qty, msg := parseQuantity(in.Quantity)
			if msg != "" {
				failures = append(failures, lineFailure{index: i, code: codeQuantityInvalid, detail: msg})
				continue
			}
			if open := openForDelivery(l, drafted); qty.Cmp(open) > 0 {
				failures = append(failures, lineFailure{index: i, code: codeOverDelivery,
					detail: fmt.Sprintf("Line %d has %s left to deliver outside this delivery.", l.Position, open.FloatString(quantityPlaces))})
				continue
			}
			w := written{line: l, qty: qty}
			if in.FromLocationId != nil {
				for _, loc := range locations[*l.VariantID] {
					if loc.LocationID == *in.FromLocationId {
						w.from, w.fromCode = &loc.LocationID, &loc.FullCode
					}
				}
				if w.from == nil {
					failures = append(failures, lineFailure{index: i, code: codeLocationUnknown,
						detail: fmt.Sprintf("Location %d of the site holds none of line %d's item.", *in.FromLocationId, l.Position)})
					continue
				}
			}
			writes = append(writes, w)
		}
		if len(failures) > 0 {
			return refuseLines(invalidDeliveryTitle, failures)
		}
		if err := txq.DeleteDeliveryLines(ctx, d.ID); err != nil {
			return fmt.Errorf("orders: clear delivery %d's lines: %w", d.ID, err)
		}
		dlines := make([]store.OrdersDeliveryLine, 0, len(writes))
		for i, w := range writes {
			row, err := txq.InsertDeliveryLine(ctx, store.InsertDeliveryLineParams{
				DeliveryID: d.ID, Position: int32(i + 1), OrderLineID: w.line.ID, VariantID: *w.line.VariantID,
				FromLocationID: w.from, FromLocationCode: w.fromCode, Quantity: numeric(w.qty, quantityPlaces),
			})
			if err != nil {
				return fmt.Errorf("orders: write delivery %d's line: %w", d.ID, err)
			}
			dlines = append(dlines, row)
		}
		if d, err = txq.SetDeliveryTouched(ctx, store.SetDeliveryTouchedParams{ID: d.ID, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("orders: save delivery %d: %w", d.ID, err)
		}
		out = deliveryView(d, o, dlines, lines, drafted, locations)
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.UpdateDelivery200JSONResponse(out), nil
}

// CancelDelivery Cancel a delivery
// (POST /api/v1/orders/deliveries/{id}/cancel)
func (s *server) CancelDelivery(ctx context.Context, req gen.CancelDeliveryRequestObject) (gen.CancelDeliveryResponseObject, error) {
	resp := deliveryAnswers(nil,
		func() gen.CancelDeliveryResponseObject { return gen.CancelDelivery404Response{} },
		func(p gen.OrdersProblem) gen.CancelDeliveryResponseObject {
			return gen.CancelDelivery409ApplicationProblemPlusJSONResponse(p)
		}, nil)
	d, err := store.New(s.deps.Pool).GetDelivery(ctx, req.Id)
	if noRows(err) {
		return gen.CancelDelivery404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read delivery %d: %w", req.Id, err)
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := lockOrder(ctx, txq, d.OrderID); err != nil {
			return err
		}
		locked, err := txq.LockDelivery(ctx, d.ID)
		if err != nil {
			return fmt.Errorf("orders: lock delivery %d: %w", d.ID, err)
		}
		if err := judgeDelivery(locked, req.Body.Version, req.Body.ExpectedUpdatedAt); err != nil {
			return err
		}
		now := s.deps.Clock()
		if _, err := txq.CancelDeliveryRow(ctx, store.CancelDeliveryRowParams{ID: d.ID, Now: &now}); err != nil {
			return fmt.Errorf("orders: cancel delivery %d: %w", d.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.deliveryOut(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	return gen.CancelDelivery200JSONResponse(out), nil
}

// PostDelivery Post a delivery
// (POST /api/v1/orders/deliveries/{id}/post)
//
// Under the variants', the order's and the delivery's locks: every line is
// picked (location_missing) and within what is open (over_delivery), and the
// order is confirmed and not on hold (order_on_hold). Inventory issues the
// lines from their locations in one movement whose document is the delivery,
// consuming the order lines' reservations (StockLedger.Issue); a refusal —
// insufficient_stock above all — names every short line by its index in the
// delivery. The order lines' delivered quantities grow, the order's delivery
// status is recomputed, delivery_posted is logged and
// orders.delivery.posted.v1 published on the same transaction. The packing
// slip is printed there and stored after the commit.
func (s *server) PostDelivery(ctx context.Context, req gen.PostDeliveryRequestObject) (gen.PostDeliveryResponseObject, error) {
	resp := deliveryAnswers(
		func(p gen.OrdersProblem) gen.PostDeliveryResponseObject {
			return gen.PostDelivery400ApplicationProblemPlusJSONResponse(p)
		},
		func() gen.PostDeliveryResponseObject { return gen.PostDelivery404Response{} },
		func(p gen.OrdersProblem) gen.PostDeliveryResponseObject {
			return gen.PostDelivery409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.OrdersProblem) gen.PostDeliveryResponseObject {
			return gen.PostDelivery503ApplicationProblemPlusJSONResponse(p)
		})
	if f := s.unknownProperties(ctx, versionInputSchema); f.failed() {
		return answer(refuseInvalid(invalidDeliveryTitle, f), resp)
	}
	var cur deliveryRead
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		cur, err = readDelivery(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeDelivery(cur.d, req.Body.Version, req.Body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	if !s.stockKept() || s.deps.StockLedger == nil {
		return answer(errDependencyUnavailable, resp)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.readPrinter(ctx)
	if err != nil {
		return nil, err
	}
	variants := goodsVariantsOf(cur.lines)
	var posted store.OrdersDelivery
	var printed []byte
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := s.lockVariants(ctx, tx, variants); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, cur.o.ID)
		if err != nil {
			return err
		}
		d, err := txq.LockDelivery(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("orders: lock delivery %d: %w", req.Id, err)
		}
		if err := judgeDelivery(d, req.Body.Version, req.Body.ExpectedUpdatedAt); err != nil {
			return err
		}
		if err := s.judgeDeliverable(o); err != nil {
			return err
		}
		lines, err := linesOf(ctx, txq, ownerOrder, o.ID)
		if err != nil {
			return err
		}
		dlines, err := txq.DeliveryLinesOf(ctx, d.ID)
		if err != nil {
			return fmt.Errorf("orders: read delivery %d's lines: %w", d.ID, err)
		}
		at := map[int64]int{}
		for i, l := range lines {
			at[l.ID] = i
		}
		var failures []lineFailure
		issue := contracts.StockIssue{Document: deliveryStock(d.ID), SiteID: d.SiteID, PostedByUserID: by.id, PostedByDisplay: deref(by.display)}
		var actions []changeAction
		for i, dl := range dlines {
			idx, ok := at[dl.OrderLineID]
			if !ok {
				failures = append(failures, lineFailure{index: i, code: codeLineNotFound, detail: "The order line is gone from the order."})
				continue
			}
			l := lines[idx]
			qty := ratOrZero(dl.Quantity)
			if dl.FromLocationID == nil {
				failures = append(failures, lineFailure{index: i, code: codeLocationMissing,
					detail: fmt.Sprintf("Line %d is not picked: choose the location it is taken from.", dl.Position)})
				continue
			}
			if open := openToDeliver(l); qty.Cmp(open) > 0 {
				failures = append(failures, lineFailure{index: i, code: codeOverDelivery,
					detail: fmt.Sprintf("Order line %d has %s left to deliver.", l.Position, open.FloatString(quantityPlaces))})
				continue
			}
			issue.Lines = append(issue.Lines, contracts.IssueLine{
				VariantID: dl.VariantID, LineID: l.ID, FromLocationID: *dl.FromLocationID, Quantity: stockText(qty),
			})
			before := ratOrZero(l.QtyDelivered)
			after := new(big.Rat).Add(before, qty)
			lines[idx].QtyDelivered = numeric(after, quantityPlaces)
			id := l.ID
			actions = append(actions, changeAction{Kind: actionDeliveryPosted, LineID: &id,
				From: ptr(before.FloatString(quantityPlaces)), To: ptr(after.FloatString(quantityPlaces))})
		}
		if len(failures) > 0 {
			return refuseLines(invalidDeliveryTitle, failures)
		}
		movement, err := s.deps.StockLedger.Issue(ctx, tx, orderStock(o.ID), issue)
		if err != nil {
			return issueRefused(err, dlines)
		}
		for _, l := range lines {
			if err := txq.SetLineBuckets(ctx, store.SetLineBucketsParams{
				ID: l.ID, QtyCancelled: l.QtyCancelled, QtyDelivered: l.QtyDelivered, QtyInvoiced: l.QtyInvoiced,
			}); err != nil {
				return fmt.Errorf("orders: deliver line %d: %w", l.ID, err)
			}
		}
		row, err := s.bumpProgress(ctx, tx, txq, o, lines, o.Version)
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		if err := logChange(ctx, txq, row.ID, row.Version, by, now, actions); err != nil {
			return err
		}
		if posted, err = txq.PostDeliveryRow(ctx, store.PostDeliveryRowParams{
			ID: d.ID, MovementID: &movement, Now: &now, ByUserID: by.id, ByDisplay: by.display,
		}); err != nil {
			return fmt.Errorf("orders: post delivery %d: %w", d.ID, err)
		}
		if err := events.Publish(ctx, tx, moduleName, contracts.DeliveryPosted{
			DeliveryID: posted.ID, Number: posted.Number, OrderID: row.ID, SiteID: posted.SiteID, PostedAt: now,
		}, now); err != nil {
			return err
		}
		printed, err = renderDeliveryDoc(deliveryPrintable{
			kind: docPackingSlip, d: posted, o: row, dlines: dlines, lines: lines, seller: p.seller, date: dayOf(now, p.zone),
		})
		return err
	})
	if err != nil {
		return answer(err, resp)
	}
	if _, err := s.storePackingSlip(context.WithoutCancel(ctx), posted, printed); err != nil {
		// The packing slip is stored by its first download instead.
		s.deps.Logger.WarnContext(ctx, "orders: a packing slip could not be stored", "delivery_id", posted.ID, "error", err.Error())
	}
	out, err := s.deliveryOut(ctx, posted.ID)
	if err != nil {
		return nil, err
	}
	return gen.PostDelivery200JSONResponse(out), nil
}

// issueRefused turns Inventory's refusal of an issue into the delivery's
// 409, naming every failing line by its index in the delivery.
func issueRefused(err error, dlines []store.OrdersDeliveryLine) error {
	var r *contracts.StockRefusal
	if !errors.As(err, &r) {
		return fmt.Errorf("orders: the stock ledger failed: %w", err)
	}
	var failures []lineFailure
	for _, fl := range r.Lines {
		for i, dl := range dlines {
			if dl.OrderLineID == fl.LineID && dl.VariantID == fl.VariantID {
				failures = append(failures, lineFailure{index: i, code: fl.Code, detail: fl.Detail})
				break
			}
		}
	}
	p := linesProblem("The delivery cannot be posted", failures)
	status, code := int32(http.StatusConflict), r.Code
	p.Status, p.Code = &status, &code
	if len(failures) == 0 {
		p.Lines = nil
		p.Detail = &r.Detail
	}
	return refuse(p)
}

// storePackingSlip stores a posted delivery's packing slip and links it,
// outside any transaction but the row's: two racing it record one row.
func (s *server) storePackingSlip(ctx context.Context, d store.OrdersDelivery, body []byte) (store.OrdersDocument, error) {
	stored, err := s.storeDocument(ctx, ownerDelivery, d.ID, d.Number, docPackingSlip, 1, body)
	if err != nil {
		return store.OrdersDocument{}, err
	}
	var doc store.OrdersDocument
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := recordDocument(ctx, q, ownerDelivery, d.ID, docPackingSlip, 1, stored, s.deps.Clock())
		if err != nil {
			return err
		}
		doc = row
		return q.SetDeliveryPackingSlip(ctx, store.SetDeliveryPackingSlipParams{ID: d.ID, DocumentID: &row.ID})
	})
	if db.IsUniqueViolation(err, "ux_documents_owner_version") {
		return store.New(s.deps.Pool).DocumentOf(ctx, store.DocumentOfParams{OwnerKind: ownerDelivery, OwnerID: d.ID, Kind: docPackingSlip, Version: 1})
	}
	return doc, err
}

// deliveryPDF answers a delivery's PDF with the headers every PDF carries.
type deliveryPDF struct {
	pick        *gen.GetDeliveryPickList200ApplicationpdfResponse
	slip        *gen.GetDeliveryPackingSlip200ApplicationpdfResponse
	disposition string
}

func (r deliveryPDF) VisitGetDeliveryPickListResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.pick.VisitGetDeliveryPickListResponse(w)
}

func (r deliveryPDF) VisitGetDeliveryPackingSlipResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.slip.VisitGetDeliveryPackingSlipResponse(w)
}

// deliveryFileName is a delivery document's download name in the order's
// language.
func deliveryFileName(kind string, d store.OrdersDelivery, language string) string {
	name := map[string][2]string{docPickList: {"plukkliste", "pick-list"}, docPackingSlip: {"pakkseddel", "packing-slip"}}[kind]
	if language == "en" {
		return fmt.Sprintf("%s-%d.pdf", name[1], d.Number)
	}
	return fmt.Sprintf("%s-%d.pdf", name[0], d.Number)
}

// GetDeliveryPickList Download a pick list
// (GET /api/v1/orders/deliveries/{id}/pick-list)
func (s *server) GetDeliveryPickList(ctx context.Context, req gen.GetDeliveryPickListRequestObject) (gen.GetDeliveryPickListResponseObject, error) {
	var r deliveryRead
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		r, err = readDelivery(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return answer(err, answers[gen.GetDeliveryPickListResponseObject]{
			notFound: func() gen.GetDeliveryPickListResponseObject { return gen.GetDeliveryPickList404Response{} },
			unavailable: func(p gen.OrdersProblem) gen.GetDeliveryPickListResponseObject {
				return gen.GetDeliveryPickList503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	locations, err := s.deliveryLocations(ctx, r, true)
	if err != nil {
		return nil, err
	}
	p, err := s.readPrinter(ctx)
	if err != nil {
		return nil, err
	}
	body, err := renderDeliveryDoc(deliveryPrintable{
		kind: docPickList, d: r.d, o: r.o, dlines: r.dlines, lines: r.lines, locations: locations, seller: p.seller,
		date: dayOf(s.deps.Clock(), p.zone),
	})
	if err != nil {
		return nil, err
	}
	resp := gen.GetDeliveryPickList200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}
	return deliveryPDF{pick: &resp, disposition: fmt.Sprintf("inline; filename=%q", deliveryFileName(docPickList, r.d, r.o.Language))}, nil
}

// GetDeliveryPackingSlip Download a packing slip
// (GET /api/v1/orders/deliveries/{id}/packing-slip)
//
// A posted delivery's is the stored one — stored now when its post could
// not; a draft's, or a cancelled one's, is printed now with the draft mark.
func (s *server) GetDeliveryPackingSlip(ctx context.Context, req gen.GetDeliveryPackingSlipRequestObject) (gen.GetDeliveryPackingSlipResponseObject, error) {
	var r deliveryRead
	var doc *store.OrdersDocument
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if r, err = readDelivery(ctx, q, req.Id); err != nil {
			return err
		}
		d, err := q.DocumentOf(ctx, store.DocumentOfParams{OwnerKind: ownerDelivery, OwnerID: r.d.ID, Kind: docPackingSlip, Version: 1})
		switch {
		case err == nil:
			doc = &d
		case !noRows(err):
			return fmt.Errorf("orders: read delivery %d's packing slip: %w", r.d.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.GetDeliveryPackingSlipResponseObject]{
			notFound: func() gen.GetDeliveryPackingSlipResponseObject { return gen.GetDeliveryPackingSlip404Response{} },
		})
	}
	var body []byte
	if doc != nil {
		if body, err = s.loadDocument(ctx, *doc); err != nil {
			return nil, err
		}
	} else {
		p, err := s.readPrinter(ctx)
		if err != nil {
			return nil, err
		}
		date := dayOf(s.deps.Clock(), p.zone)
		if r.d.PostedAt != nil {
			date = dayOf(*r.d.PostedAt, p.zone)
		}
		if body, err = renderDeliveryDoc(deliveryPrintable{
			kind: docPackingSlip, d: r.d, o: r.o, dlines: r.dlines, lines: r.lines, seller: p.seller, date: date,
		}); err != nil {
			return nil, err
		}
		if r.d.Status == deliveryPosted {
			if _, err := s.storePackingSlip(ctx, r.d, body); err != nil {
				return nil, err
			}
		}
	}
	resp := gen.GetDeliveryPackingSlip200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}
	return deliveryPDF{slip: &resp, disposition: fmt.Sprintf("attachment; filename=%q", deliveryFileName(docPackingSlip, r.d, r.o.Language))}, nil
}

// ReserveSalesOrder Reserve an order's stock
// (POST /api/v1/orders/orders/{id}/reserve)
//
// Reserve now (O46): what of each goods line's open quantity is not reserved
// yet, at the order's site, logged reserved. A shortfall refuses it all.
func (s *server) ReserveSalesOrder(ctx context.Context, req gen.ReserveSalesOrderRequestObject) (gen.ReserveSalesOrderResponseObject, error) {
	resp := orderAnswers(nil,
		func() gen.ReserveSalesOrderResponseObject { return gen.ReserveSalesOrder404Response{} },
		func(p gen.OrdersProblem) gen.ReserveSalesOrderResponseObject {
			return gen.ReserveSalesOrder409ApplicationProblemPlusJSONResponse(p)
		})
	resp.unavailable = func(p gen.OrdersProblem) gen.ReserveSalesOrderResponseObject {
		return gen.ReserveSalesOrder503ApplicationProblemPlusJSONResponse(p)
	}
	if !s.stockKept() || s.deps.StockLedger == nil {
		return answer(errDependencyUnavailable, resp)
	}
	out, err := s.changeOrder(ctx, req.Id, req.Body.Version, req.Body.ExpectedUpdatedAt, true,
		func(ctx context.Context, tx pgx.Tx, txq *store.Queries, o store.OrdersOrder, lines []store.OrdersLine) (store.OrdersOrder, []changeAction, error) {
			if err := judgeConfirmed(o); err != nil {
				return o, nil, err
			}
			if !s.atSite(o.SiteID) {
				return o, nil, conflict(codeOrderHasNoSite, "The order has no site",
					fmt.Sprintf("Order %d names no site to reserve at.", o.Number))
			}
			reserved, err := s.reserveOpen(ctx, tx, o, lines, invalidOrderTitle)
			if err != nil || !reserved {
				return o, nil, err
			}
			row, err := txq.SetOrderProgress(ctx, store.SetOrderProgressParams{
				ID: o.ID, DeliveryStatus: o.DeliveryStatus, InvoiceStatus: o.InvoiceStatus, Version: o.Version, Now: s.deps.Clock(),
			})
			if err != nil {
				return o, nil, fmt.Errorf("orders: touch order %d: %w", o.ID, err)
			}
			return row, []changeAction{{Kind: actionReserved}}, nil
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.ReserveSalesOrder200JSONResponse(out), nil
}
