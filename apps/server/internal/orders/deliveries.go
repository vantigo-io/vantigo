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
	kits := kitsOf(lines)
	for _, l := range lines {
		if reservable(l, kits) && !slices.Contains(ids, *l.VariantID) {
			ids = append(ids, *l.VariantID)
		}
	}
	slices.Sort(ids)
	return ids
}

// withDeliveryVariants adds the variants a delivery's lines issue to ids,
// sorted.
func withDeliveryVariants(ids []int32, dlines []store.OrdersDeliveryLine) []int32 {
	for _, dl := range dlines {
		if !slices.Contains(ids, dl.VariantID) {
			ids = append(ids, dl.VariantID)
		}
	}
	slices.Sort(ids)
	return ids
}

// deliveryView is a delivery as the API answers it: o is its order, lines
// its order's lines, drafted what other drafts hold of them, and locations,
// on a draft, the site's locations holding each variant.
func deliveryView(d store.OrdersDelivery, o store.OrdersOrder, dlines []store.OrdersDeliveryLine, lines []store.OrdersLine,
	drafted map[int64]*big.Rat, locations map[int32][]contracts.StockLocation, lv deliveryLotsView) gen.OrdersDelivery {
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
		line.Tracked, line.Lots, line.LotChoices, line.Short = lv.lotsOut(dl)
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
	// picks are the tracked lines' lots, by delivery line (M75).
	picks map[int64][]store.OrdersDeliveryLineLot
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
	picks, err := q.DeliveryLineLotsOf(ctx, d.ID)
	if err != nil {
		return r, fmt.Errorf("orders: read delivery %d's lots: %w", d.ID, err)
	}
	r.picks = picksByLine(picks)
	r.drafted, err = draftedElsewhere(ctx, q, d.OrderID, d.ID)
	return r, err
}

// deliveryLots is what a read delivery's view shows of its lots, read
// outside any transaction: which of its variants are tracked (the catalog,
// when Products is on) and, on a draft, the site's lots of each.
func (s *server) deliveryLots(ctx context.Context, r deliveryRead) (deliveryLotsView, error) {
	lv := deliveryLotsView{picks: r.picks, tracked: map[int32]bool{}}
	if len(r.dlines) == 0 {
		return lv, nil
	}
	var ids []int32
	for _, dl := range r.dlines {
		if !slices.Contains(ids, dl.VariantID) {
			ids = append(ids, dl.VariantID)
		}
	}
	slices.Sort(ids)
	if s.deps.Products != nil {
		tracked, err := s.trackedOf(ctx, ids)
		if err != nil {
			return lv, err
		}
		lv.tracked = tracked
	}
	if r.d.Status != deliveryDraft {
		return lv, nil
	}
	var trackedIDs []int32
	for _, id := range ids {
		if lv.tracked[id] {
			trackedIDs = append(trackedIDs, id)
		}
	}
	if len(trackedIDs) == 0 {
		return lv, nil
	}
	today, err := s.todayOf(ctx)
	if err != nil {
		return lv, err
	}
	lv.today = today
	lv.choices, err = s.stockLots(ctx, trackedIDs, r.d.SiteID)
	return lv, err
}

// deliveryLocations are the site's locations holding the delivery's
// variants, read through contracts.Stock outside any transaction: a draft's
// picker and the pick list, each variant's primary location first
// (stockLocations). None for a delivery no longer a draft, or when stock is
// not kept.
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
			Statuses: statuses, OrderID: p.OrderId, CustomerID: p.CustomerId, SiteID: p.SiteId, Q: search, PageSize: size, PageOffset: (page - 1) * size,
		})
		if err != nil {
			return fmt.Errorf("orders: list deliveries: %w", err)
		}
		if out.Total, err = q.CountDeliveries(ctx, store.CountDeliveriesParams{Statuses: statuses, OrderID: p.OrderId, CustomerID: p.CustomerId, SiteID: p.SiteId, Q: search}); err != nil {
			return fmt.Errorf("orders: count deliveries: %w", err)
		}
		// Each delivery's lot numbers, once each, in line order (M79): a
		// posted one's and a draft's so far, never a cancelled one's.
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			if r.OrdersDelivery.Status != deliveryCancelled {
				ids = append(ids, r.OrdersDelivery.ID)
			}
		}
		picks, err := q.DeliveryLineLotsOfDeliveries(ctx, ids)
		if err != nil {
			return fmt.Errorf("orders: read the deliveries' lots: %w", err)
		}
		lotsOf := map[int64][]string{}
		for _, pk := range picks {
			if !slices.Contains(lotsOf[pk.DeliveryID], pk.LotNumber) {
				lotsOf[pk.DeliveryID] = append(lotsOf[pk.DeliveryID], pk.LotNumber)
			}
		}
		for _, r := range rows {
			d := r.OrdersDelivery
			out.Items = append(out.Items, gen.OrdersDeliverySummary{
				Id: d.ID, Number: d.Number, OrderId: d.OrderID, OrderNumber: r.OrderNumber, CustomerId: r.CustomerID,
				CustomerName: r.CustomerName, SiteId: d.SiteID, Status: gen.OrdersDeliverySummaryStatus(d.Status),
				Lines: r.Lines, Lots: append([]string{}, lotsOf[d.ID]...), PostedAt: d.PostedAt, UpdatedAt: d.UpdatedAt,
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
	lv, err := s.deliveryLots(ctx, r)
	if err != nil {
		return gen.OrdersDelivery{}, err
	}
	return deliveryView(r.d, r.o, r.dlines, r.lines, r.drafted, locations, lv), nil
}

// CreateDelivery Create a delivery
// (POST /api/v1/orders/deliveries)
//
// Every open goods line of the order is proposed at what of it can go from
// the site: what the line holds reserved, and what is available there beyond
// every reservation, shared in line order between the lines of one variant —
// never more than is open. Each proposed line is picked from the first
// location that holds all of it — the variant's primary location at the
// site first, then by full code (stockLocations) — else none. Lines nothing can
// go of are left out; an order nothing can go of is refused
// (nothing_to_deliver).
//
// A tracked variant's line (manufacturing phase 2 M76) is picked by lot
// instead: its quantity is proposed FEFO from the site's lots
// (contracts.ProposeLots over Stock.Lots, read on the pool before the
// transaction), the lines of one variant drawing on them in line order. A
// line the lots cannot cover keeps its quantity with the picks they give;
// the page marks it short and the post refuses it until the picks add up.
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
	lots, err := s.stockLots(ctx, variants, *cur.SiteID)
	if err != nil {
		return nil, err
	}
	today, err := s.todayOf(ctx)
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
		kits := kitsOf(lines)
		for _, l := range lines {
			if !reservable(l, kits) {
				continue
			}
			open := openForDelivery(l, drafted)
			if open.Sign() <= 0 {
				continue
			}
			// The line's reservation less what its other drafts already
			// take of it, then what is free at the site.
			own := new(big.Rat).Set(orZero(reserved[l.ID]))
			if d := drafted[l.ID]; d != nil {
				own.Sub(own, d)
			}
			if own.Sign() < 0 {
				own.SetInt64(0)
			}
			can := new(big.Rat).Set(own)
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
			if extra := new(big.Rat).Sub(qty, own); extra.Sign() > 0 && free[*l.VariantID] != nil {
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
		lv := deliveryLotsView{tracked: map[int32]bool{}, picks: map[int64][]store.OrdersDeliveryLineLot{}, choices: lots, today: today}
		pool := newLotPool(lots, today)
		for i, p := range picks {
			variant := *p.line.VariantID
			tracked := trackedLocked(ctx, variant)
			lv.tracked[variant] = tracked
			var from *int64
			var code *string
			for _, loc := range locations[variant] {
				if !tracked && mustRat(loc.Quantity).Cmp(p.qty) >= 0 {
					from, code = &loc.LocationID, &loc.FullCode
					break
				}
			}
			row, err := txq.InsertDeliveryLine(ctx, store.InsertDeliveryLineParams{
				DeliveryID: d.ID, Position: int32(i + 1), OrderLineID: p.line.ID, VariantID: variant,
				FromLocationID: from, FromLocationCode: code, Quantity: numeric(p.qty, quantityPlaces),
			})
			if err != nil {
				return fmt.Errorf("orders: insert delivery %d's line: %w", d.ID, err)
			}
			dlines = append(dlines, row)
			if !tracked {
				continue
			}
			proposed, _, err := pool.propose(variant, p.qty)
			if err != nil {
				return err
			}
			if lv.picks[row.ID], err = writePicks(ctx, txq, row.ID, proposed); err != nil {
				return err
			}
		}
		out = deliveryView(d, o, dlines, lines, drafted, locations, lv)
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
//
// A tracked line names its lots instead (manufacturing phase 2 M77), each a
// lot of the site holding the variant at its location (lot_unknown), once;
// an untracked line names none (lot_not_allowed); a serial variant's lot
// takes 1 (serial_quantity); a new or changed lot allowed expired needs
// inventory:expired-override, read on the pool (expired_override_required).
// Lots sent unchanged keep whether they were chosen; any change marks them
// all chosen, so the post no longer proposes them again. A tracked line sent
// without lots keeps its picks when a person chose them — its quantity
// changed alone, the page shows it short or over — or while they add up to
// its quantity, and is proposed again (FEFO) otherwise. proposeAgain (§19
// Propose again) proposes the line again whatever its picks, sent without
// lots (line_invalid).
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
	lots, err := s.stockLots(ctx, variants, cur.d.SiteID)
	if err != nil {
		return nil, err
	}
	today, err := s.todayOf(ctx)
	if err != nil {
		return nil, err
	}
	override := s.has(ctx, permInventoryExpiredOverride)
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
		old, err := picksOfOrderLines(ctx, txq, d.ID)
		if err != nil {
			return err
		}
		pool := newLotPool(lots, today)
		byLine := byID(lines)
		kits := kitsOf(lines)
		seen := map[int64]bool{}
		var failures []lineFailure
		type written struct {
			line     store.OrdersLine
			qty      *big.Rat
			from     *int64
			fromCode *string
			tracked  bool
			picks    []lotPick
			propose  bool
		}
		var writes []written
		for i, in := range body.Lines {
			l, ok := byLine[in.OrderLineId]
			switch {
			case !ok || !reservable(l, kits):
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
			if trackedLocked(ctx, *l.VariantID) {
				w.tracked = true
				again := in.ProposeAgain != nil && *in.ProposeAgain
				if again && in.Lots != nil {
					failures = append(failures, lineFailure{index: i, code: codeLineInvalid,
						detail: fmt.Sprintf("Line %d names its lots and asks to propose them again; send one.", l.Position)})
					continue
				}
				picks, lineFailures := judgePicks(i, *l.VariantID, serialLocked(ctx, *l.VariantID), override, in.Lots, old[l.ID], pool)
				if len(lineFailures) > 0 {
					failures = append(failures, lineFailures...)
					continue
				}
				switch {
				case again:
					w.propose = true
				case in.Lots != nil:
					w.picks = picks
				case len(old[l.ID]) > 0 && (anyChosen(old[l.ID]) || sumPicks(old[l.ID]).Cmp(qty) == 0):
					w.picks = old[l.ID]
				default:
					w.propose = true
				}
				writes = append(writes, w)
				continue
			}
			if in.Lots != nil && len(*in.Lots) > 0 {
				failures = append(failures, lineFailure{index: i, code: codeLotNotAllowed,
					detail: fmt.Sprintf("Line %d's item is not tracked by lot; it names no lot.", l.Position)})
				continue
			}
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
		// The picks kept or chosen take their lots first; the lines proposed
		// again draw on what is left, in line order.
		for _, w := range writes {
			for _, p := range w.picks {
				pool.take(*w.line.VariantID, lotSpot{p.lotID, p.locationID}, p.qty)
			}
		}
		for i := range writes {
			if writes[i].propose {
				if writes[i].picks, _, err = pool.propose(*writes[i].line.VariantID, writes[i].qty); err != nil {
					return err
				}
			}
		}
		lv := deliveryLotsView{tracked: map[int32]bool{}, picks: map[int64][]store.OrdersDeliveryLineLot{}, choices: lots, today: today}
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
			lv.tracked[row.VariantID] = w.tracked
			if w.tracked {
				if lv.picks[row.ID], err = writePicks(ctx, txq, row.ID, w.picks); err != nil {
					return err
				}
			}
		}
		if d, err = txq.SetDeliveryTouched(ctx, store.SetDeliveryTouchedParams{ID: d.ID, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("orders: save delivery %d: %w", d.ID, err)
		}
		out = deliveryView(d, o, dlines, lines, drafted, locations, lv)
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
//
// A tracked line (manufacturing phase 2 M78) names a lot on every pick
// (lot_required) and its picks add up to its quantity (picks_mismatch); it
// issues one ledger line per pick, each with the order line's id, so the
// reservation is consumed as before, and Inventory's refusals — lot_expired,
// lot_blocked, insufficient_stock — name the line and the pick. A refusal of
// insufficient_stock or lot_blocked that falls only on lines whose picks
// were all proposed proposes those lines again from the lots as they are now
// and posts once more (M65a); the delivery keeps the new picks. A refusal of
// the second post answers the first, which names the picks the delivery
// still holds.
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
	// The day a lot's expiry is judged on, and whether the poster may take
	// an expired lot, are read here on the pool: the ledger makes no call of
	// its own inside the transaction (manufacturing phase 2 M62, rule 24).
	today, err := s.todayOf(ctx)
	if err != nil {
		return nil, err
	}
	post := deliveryPost{
		id: req.Id, version: req.Body.Version, expected: req.Body.ExpectedUpdatedAt, cur: cur, by: by, printer: p,
		today: today, expiredOverride: s.has(ctx, permInventoryExpiredOverride),
	}
	posted, printed, err := s.postDeliveryOnce(ctx, post)
	var stale *staleLots
	if errors.As(err, &stale) {
		// Outside the first transaction: the lots as they are now, on the
		// pool, and the refused lines proposed again from them, once. The
		// second post's picks roll back with its refusal, so the first
		// refusal is answered: its lot rows are the ones still stored.
		first := stale.refusal
		if post.repropose, err = s.reproposeLots(ctx, cur.d.SiteID, today, stale); err == nil {
			posted, printed, err = s.postDeliveryOnce(ctx, post)
		}
		if errors.As(err, &stale) {
			err = first
		}
	}
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

// deliveryPost is one post of a delivery: the request, the delivery as read
// before the transaction, what was read on the pool for it, and — on the
// second attempt — the refused lines' new picks, by delivery line id.
type deliveryPost struct {
	id              int64
	version         int32
	expected        time.Time
	cur             deliveryRead
	by              actor
	printer         printer
	today           time.Time
	expiredOverride bool
	repropose       map[int64][]lotPick
}

// staleLots is a first post refused on proposed picks only (M65a): the
// refusal to answer when no second proposal can be made, and the refused
// lines with every line's picks, as they stood under the locks.
type staleLots struct {
	refusal error
	refused []store.OrdersDeliveryLine
	picks   map[int64][]lotPick
	dlines  []store.OrdersDeliveryLine
}

func (e *staleLots) Error() string { return e.refusal.Error() }

// issueRef is what one ledger line of a delivery's issue stands for: the
// delivery line, by its index, and — for a tracked line — its pick.
type issueRef struct {
	line int
	lot  int // the pick's index among the line's picks, -1 for none
	pick lotPick
}

// variantsGrew is a post that found, under the order's and the delivery's
// locks, a variant it had not locked: the transaction is rolled back and
// tried again with every variant locked first (MB rule 21's order).
type variantsGrew struct{ all []int32 }

func (e *variantsGrew) Error() string {
	return "orders: the delivery's variants changed under the lock"
}

// postDeliveryOnce posts the delivery: on one transaction, tried again — at
// most twice — when its variants grew between the read and the locks.
func (s *server) postDeliveryOnce(ctx context.Context, in deliveryPost) (store.OrdersDelivery, []byte, error) {
	variants := withDeliveryVariants(goodsVariantsOf(in.cur.lines), in.cur.dlines)
	for range 2 {
		posted, printed, err := s.postDeliveryTx(ctx, in, variants)
		var grew *variantsGrew
		if !errors.As(err, &grew) {
			return posted, printed, err
		}
		variants = grew.all
	}
	posted, printed, err := s.postDeliveryTx(ctx, in, variants)
	if errors.As(err, new(*variantsGrew)) {
		return posted, printed, conflict(codeVersionConflict, "The delivery changed",
			fmt.Sprintf("Delivery %d's lines kept changing while it was being posted. Try again.", in.cur.d.Number))
	}
	return posted, printed, err
}

// postDeliveryTx is one transaction of a post, variants locked first.
func (s *server) postDeliveryTx(ctx context.Context, in deliveryPost, variants []int32) (store.OrdersDelivery, []byte, error) {
	var posted store.OrdersDelivery
	var printed []byte
	err := s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := s.lockVariants(ctx, tx, variants); err != nil {
			return err
		}
		o, err := lockOrder(ctx, txq, in.cur.o.ID)
		if err != nil {
			return err
		}
		d, err := txq.LockDelivery(ctx, in.id)
		if err != nil {
			return fmt.Errorf("orders: lock delivery %d: %w", in.id, err)
		}
		if err := judgeDelivery(d, in.version, in.expected); err != nil {
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
		// The variants as they are under the lock: an edit or a save between
		// the read and the lock may have named one not locked yet. It is
		// never locked after the order's row: the post is tried again with
		// every variant locked first.
		all := slices.Clone(variants)
		for _, id := range withDeliveryVariants(goodsVariantsOf(lines), dlines) {
			if !slices.Contains(all, id) {
				all = append(all, id)
			}
		}
		if len(all) > len(variants) {
			slices.Sort(all)
			return &variantsGrew{all: all}
		}
		rows, err := txq.DeliveryLineLotsOf(ctx, d.ID)
		if err != nil {
			return fmt.Errorf("orders: read delivery %d's lots: %w", d.ID, err)
		}
		stored := picksByLine(rows)
		// The second attempt's picks replace the refused lines' (M65a).
		for _, dl := range dlines {
			again, ok := in.repropose[dl.ID]
			if !ok {
				continue
			}
			if err := txq.DeleteDeliveryLineLots(ctx, dl.ID); err != nil {
				return fmt.Errorf("orders: clear delivery line %d's lots: %w", dl.ID, err)
			}
			if stored[dl.ID], err = writePicks(ctx, txq, dl.ID, again); err != nil {
				return err
			}
		}
		at := map[int64]int{}
		for i, l := range lines {
			at[l.ID] = i
		}
		var failures []lineFailure
		issue := contracts.StockIssue{Document: deliveryStock(d.ID), SiteID: d.SiteID, PostedByUserID: in.by.id, PostedByDisplay: deref(in.by.display),
			Today: in.today, ExpiredOverride: in.expiredOverride}
		var refs []issueRef
		picks := map[int64][]lotPick{}
		var actions []changeAction
		for i, dl := range dlines {
			idx, ok := at[dl.OrderLineID]
			if !ok {
				failures = append(failures, lineFailure{index: i, code: codeLineNotFound, detail: "The order line is gone from the order."})
				continue
			}
			l := lines[idx]
			qty := ratOrZero(dl.Quantity)
			if open := openToDeliver(l); qty.Cmp(open) > 0 {
				failures = append(failures, lineFailure{index: i, code: codeOverDelivery,
					detail: fmt.Sprintf("Order line %d has %s left to deliver.", l.Position, open.FloatString(quantityPlaces))})
				continue
			}
			if trackedLocked(ctx, dl.VariantID) {
				for _, r := range stored[dl.ID] {
					picks[dl.ID] = append(picks[dl.ID], pickOfRow(r))
				}
				switch sum := sumPicks(picks[dl.ID]); {
				case len(picks[dl.ID]) == 0:
					failures = append(failures, lineFailure{index: i, code: codeLotRequired,
						detail: fmt.Sprintf("Line %d's item is tracked by lot: choose the lots it is taken from.", dl.Position)})
					continue
				case sum.Cmp(qty) != 0:
					failures = append(failures, lineFailure{index: i, code: codePicksMismatch,
						detail: fmt.Sprintf("Line %d's lots take %s of its %s.", dl.Position, sum.FloatString(quantityPlaces), qty.FloatString(quantityPlaces))})
					continue
				}
				for j, pk := range picks[dl.ID] {
					issue.Lines = append(issue.Lines, contracts.IssueLine{
						VariantID: dl.VariantID, LineID: l.ID, FromLocationID: pk.locationID, Quantity: stockText(pk.qty),
						LotID: pk.lotID, AllowExpired: pk.allowExpired,
					})
					refs = append(refs, issueRef{line: i, lot: j, pick: pk})
				}
			} else {
				if dl.FromLocationID == nil {
					failures = append(failures, lineFailure{index: i, code: codeLocationMissing,
						detail: fmt.Sprintf("Line %d is not picked: choose the location it is taken from.", dl.Position)})
					continue
				}
				issue.Lines = append(issue.Lines, contracts.IssueLine{
					VariantID: dl.VariantID, LineID: l.ID, FromLocationID: *dl.FromLocationID, Quantity: stockText(qty),
				})
				refs = append(refs, issueRef{line: i, lot: -1})
			}
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
		issued, err := s.deps.StockLedger.Issue(ctx, tx, orderStock(o.ID), issue)
		if err != nil {
			stale, refused := issueRefused(err, refs)
			if in.repropose != nil {
				// The second post's refusal names picks that roll back with
				// it: PostDelivery answers the first one's instead.
				if errors.As(err, new(*contracts.StockRefusal)) {
					return &staleLots{refusal: refused}
				}
				return refused
			}
			if len(stale) > 0 {
				st := &staleLots{refusal: refused, picks: picks, dlines: dlines}
				for _, i := range stale {
					st.refused = append(st.refused, dlines[i])
				}
				return st
			}
			return refused
		}
		movement := issued.MovementID
		// A kit line counts the whole kits its children now make (M145).
		syncKits(lines)
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
		if err := logChange(ctx, txq, row.ID, row.Version, in.by, now, actions); err != nil {
			return err
		}
		if posted, err = txq.PostDeliveryRow(ctx, store.PostDeliveryRowParams{
			ID: d.ID, MovementID: &movement, Now: &now, ByUserID: in.by.id, ByDisplay: in.by.display,
		}); err != nil {
			return fmt.Errorf("orders: post delivery %d: %w", d.ID, err)
		}
		if err := events.Publish(ctx, tx, moduleName, contracts.DeliveryPosted{
			DeliveryID: posted.ID, Number: posted.Number, OrderID: row.ID, SiteID: posted.SiteID, PostedAt: now,
		}, now); err != nil {
			return err
		}
		serial := map[int32]bool{}
		for _, dl := range dlines {
			serial[dl.VariantID] = serialLocked(ctx, dl.VariantID)
		}
		printed, err = renderDeliveryDoc(deliveryPrintable{
			kind: docPackingSlip, d: posted, o: row, dlines: dlines, lines: lines, picks: stored, serial: serial, seller: in.printer.seller,
			date: dayOf(now, in.printer.zone),
		})
		return err
	})
	return posted, printed, err
}

// reproposeLots proposes the refused lines of a first post again (M65a),
// outside any transaction: from the site's lots as Stock.Lots answers them
// now, less what the delivery's other lines pick, in line order. A line the
// lots no longer cover answers the first refusal.
func (s *server) reproposeLots(ctx context.Context, siteID int64, today time.Time, st *staleLots) (map[int64][]lotPick, error) {
	var ids []int32
	for _, dl := range st.refused {
		if !slices.Contains(ids, dl.VariantID) {
			ids = append(ids, dl.VariantID)
		}
	}
	slices.Sort(ids)
	lots, err := s.stockLots(ctx, ids, siteID)
	if err != nil {
		return nil, err
	}
	pool := newLotPool(lots, today)
	again := map[int64][]lotPick{}
	for _, dl := range st.refused {
		again[dl.ID] = nil
	}
	for _, dl := range st.dlines {
		if _, ok := again[dl.ID]; ok {
			continue
		}
		for _, p := range st.picks[dl.ID] {
			pool.take(dl.VariantID, lotSpot{p.lotID, p.locationID}, p.qty)
		}
	}
	for _, dl := range st.refused {
		picks, short, err := pool.propose(dl.VariantID, ratOrZero(dl.Quantity))
		if err != nil {
			return nil, err
		}
		if short.Sign() > 0 {
			return nil, st.refusal
		}
		again[dl.ID] = picks
	}
	return again, nil
}

// The ledger's codes a post of proposed picks answers by proposing them
// again (M65a): the lot no longer holds the quantity, or is on hold now.
var reproposedCodes = []string{contracts.StockInsufficientStock, contracts.StockLotBlocked}

// issueRefused turns Inventory's refusal of an issue into the delivery's
// 409, naming every failing line by its index in the delivery and, on a
// tracked line, the pick by its index among the line's (refs map the
// ledger's line indexes back, M78). stale are the delivery lines, by index,
// that may be proposed again: when every failing ledger line is one of
// reproposedCodes on a line whose picks are all proposed; nil otherwise.
func issueRefused(err error, refs []issueRef) (stale []int, refused error) {
	var r *contracts.StockRefusal
	if !errors.As(err, &r) {
		return nil, fmt.Errorf("orders: the stock ledger failed: %w", err)
	}
	var failures []lineFailure
	proposed := map[int]bool{}
	for _, ref := range refs {
		if _, seen := proposed[ref.line]; !seen {
			proposed[ref.line] = ref.lot >= 0
		}
		if ref.lot >= 0 && ref.pick.chosen {
			proposed[ref.line] = false
		}
	}
	again := len(r.Lines) > 0
	for _, fl := range r.Lines {
		if fl.Index < 0 || fl.Index >= len(refs) {
			again = false
			continue
		}
		ref := refs[fl.Index]
		f := lineFailure{index: ref.line, code: fl.Code, detail: fl.Detail}
		if ref.lot >= 0 {
			f.lot = &lotFailure{index: ref.lot, lotID: ref.pick.lotID}
			f.detail = fmt.Sprintf("Lot %s at %s: %s", ref.pick.number, ref.pick.code, fl.Detail)
		}
		failures = append(failures, f)
		if !proposed[ref.line] || !slices.Contains(reproposedCodes, fl.Code) {
			again = false
		} else if !slices.Contains(stale, ref.line) {
			stale = append(stale, ref.line)
		}
	}
	if !again {
		stale = nil
	}
	slices.Sort(stale)
	p := linesProblem("The delivery cannot be posted", failures)
	status, code := int32(http.StatusConflict), r.Code
	p.Status, p.Code = &status, &code
	if len(failures) == 0 {
		p.Lines = nil
		p.Detail = &r.Detail
	}
	return stale, refuse(p)
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
		kind: docPickList, d: r.d, o: r.o, dlines: r.dlines, lines: r.lines, picks: r.picks, locations: locations, seller: p.seller,
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
// Once a shipment of it is booked, it is printed now with each booked
// shipment's carrier, service, consignment number and tracking (O52), dated
// the day the delivery was posted.
func (s *server) GetDeliveryPackingSlip(ctx context.Context, req gen.GetDeliveryPackingSlipRequestObject) (gen.GetDeliveryPackingSlipResponseObject, error) {
	var r deliveryRead
	var doc *store.OrdersDocument
	var shipped []shipmentPrint
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if r, err = readDelivery(ctx, q, req.Id); err != nil {
			return err
		}
		if shipped, err = bookedShipmentsOf(ctx, q, r.d.ID, r.o.Language); err != nil {
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
	if doc != nil && len(shipped) == 0 {
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
		serial, err := s.serialsOf(ctx, r.dlines)
		if err != nil {
			return nil, err
		}
		if body, err = renderDeliveryDoc(deliveryPrintable{
			kind: docPackingSlip, d: r.d, o: r.o, dlines: r.dlines, lines: r.lines, picks: r.picks, serial: serial, seller: p.seller, date: date, shipments: shipped,
		}); err != nil {
			return nil, err
		}
		if r.d.Status == deliveryPosted && doc == nil && len(shipped) == 0 {
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
	// The day the ledger judges a lot's expiry on, read on the pool (rule 24).
	today, err := s.todayOf(ctx)
	if err != nil {
		return nil, err
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
			reserved, err := s.reserveOpen(ctx, tx, o, lines, today, invalidOrderTitle)
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

// bookedShipmentsOf are a delivery's booked shipments as its packing slip
// prints them (O52), oldest first; a cancelled or draft shipment is left out.
func bookedShipmentsOf(ctx context.Context, q *store.Queries, deliveryID int64, language string) ([]shipmentPrint, error) {
	rows, err := q.ShipmentsOfDelivery(ctx, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("orders: read delivery %d's shipments: %w", deliveryID, err)
	}
	var out []shipmentPrint
	for _, sh := range rows {
		if sh.ConsignmentNumber == nil || sh.Status == shipmentCancelled || sh.Status == shipmentDraft {
			continue
		}
		a, err := q.GetCarrierAccount(ctx, sh.CarrierAccountID)
		if err != nil {
			return nil, fmt.Errorf("orders: read carrier account %d: %w", sh.CarrierAccountID, err)
		}
		out = append(out, shipmentPrint{
			carrier: carrierName(a.Carrier), service: serviceName(a.Carrier, cmpOr(deref(sh.BookedService), sh.Service), language),
			consignment: *sh.ConsignmentNumber, trackingURL: deref(sh.TrackingUrl),
		})
	}
	return out, nil
}
