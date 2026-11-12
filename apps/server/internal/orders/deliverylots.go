package orders

// Deliveries with lots (manufacturing phase 2 M75–M79). A delivery line of a
// variant tracked by lot or serial holds its quantity in picks — one row of
// orders.delivery_line_lots per lot and location — and its own from-location
// stays null. A new delivery proposes the picks FEFO from Stock.Lots, read on
// the pool before its transaction (contracts.ProposeLots, M76); a person
// changes them on the draft (M77), which marks them chosen; the post issues
// one ledger line per pick and, when a refusal falls only on proposed picks,
// proposes those lines again once and posts again (M78, M65a). The lot's
// number, expiry and the location's code are kept on the row for the pick
// list and the packing slip (M79).

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The deliveries' lot codes: Orders' own, and Inventory's it answers on a
// line before the ledger is called.
const (
	codePicksMismatch = "picks_mismatch"
	codeLotRequired   = contracts.StockLotRequired
	codeLotUnknown    = contracts.StockLotUnknown
	codeLotNotAllowed = contracts.StockLotNotAllowed
)

// isTracked reports whether a variant is tracked by lot or serial number.
func isTracked(e contracts.VariantEntry) bool {
	return e.Tracking == contracts.TrackingLot || e.Tracking == contracts.TrackingSerial
}

// trackedLocked reports whether a variant locked on this transaction is
// tracked, as the catalog says while its lock is held.
func trackedLocked(ctx context.Context, variantID int32) bool {
	rec := lockedVariantsOf(ctx)
	if rec == nil {
		return false
	}
	e, ok := rec.byID[variantID]
	return ok && isTracked(e)
}

// trackedOf is, per variant, whether it is tracked, read from the catalog
// before any transaction.
func (s *server) trackedOf(ctx context.Context, ids []int32) (map[int32]bool, error) {
	entries, err := s.catalogVariants(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := map[int32]bool{}
	for id, e := range entries {
		out[id] = isTracked(e)
	}
	return out, nil
}

// stockLots is, per variant, the site's lots holding it, by location, in
// FEFO order, before any transaction; nil when stock is not kept.
func (s *server) stockLots(ctx context.Context, variantIDs []int32, siteID int64) (map[int32][]contracts.StockLot, error) {
	if err := noteContractCall(ctx, "Stock.Lots"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil || len(variantIDs) == 0 {
		return nil, nil
	}
	rows, err := s.deps.Stock.Lots(ctx, variantIDs, siteID)
	if err != nil {
		return nil, fmt.Errorf("orders: read the lots holding stock: %w", err)
	}
	out := map[int32][]contracts.StockLot{}
	for _, r := range rows {
		out[r.VariantID] = append(out[r.VariantID], r)
	}
	return out, nil
}

// lotSpot is a lot at a location.
type lotSpot struct{ lot, location int64 }

// lotPick is one pick of a tracked delivery line as it is written.
type lotPick struct {
	lotID, locationID    int64
	number, code         string
	expiry               *time.Time
	qty                  *big.Rat
	allowExpired, chosen bool
}

// pickOfRow is a stored pick as lotPick.
func pickOfRow(r store.OrdersDeliveryLineLot) lotPick {
	var expiry *time.Time
	if r.Expiry.Valid {
		e := r.Expiry.Time
		expiry = &e
	}
	return lotPick{lotID: r.LotID, locationID: r.LocationID, number: r.LotNumber, code: r.LocationCode, expiry: expiry,
		qty: ratOrZero(r.Quantity), allowExpired: r.AllowExpired, chosen: r.Chosen}
}

// sumPicks is what the picks take together.
func sumPicks(picks []lotPick) *big.Rat {
	out := new(big.Rat)
	for _, p := range picks {
		out.Add(out, p.qty)
	}
	return out
}

// sumRows is what stored picks take together.
func sumRows(rows []store.OrdersDeliveryLineLot) *big.Rat {
	out := new(big.Rat)
	for _, r := range rows {
		out.Add(out, ratOrZero(r.Quantity))
	}
	return out
}

// lotPool is the site's lots of a delivery's variants as a proposal draws
// on them: what each lot holds at each location, less what the delivery's
// other picks already take of it.
type lotPool struct {
	rows  map[int32][]contracts.StockLot
	today time.Time
}

func newLotPool(lots map[int32][]contracts.StockLot, today time.Time) *lotPool {
	p := &lotPool{rows: map[int32][]contracts.StockLot{}, today: today}
	for v, rows := range lots {
		p.rows[v] = slices.Clone(rows)
	}
	return p
}

// find is a lot of the variant at a location as Stock.Lots answered it.
func (p *lotPool) find(variant int32, spot lotSpot) (contracts.StockLot, bool) {
	for _, r := range p.rows[variant] {
		if r.LotID == spot.lot && r.LocationID == spot.location {
			return r, true
		}
	}
	return contracts.StockLot{}, false
}

// take lowers what a lot holds at a location by qty, never below 0; a row
// left at 0 is no longer proposed.
func (p *lotPool) take(variant int32, spot lotSpot, qty *big.Rat) {
	rows := p.rows[variant]
	for i := range rows {
		if rows[i].LotID != spot.lot || rows[i].LocationID != spot.location {
			continue
		}
		left := new(big.Rat).Sub(mustRat(rows[i].Quantity), qty)
		if left.Sign() <= 0 {
			p.rows[variant] = slices.Delete(rows, i, i+1)
			return
		}
		rows[i].Quantity = stockText(left)
		return
	}
}

// propose is the FEFO proposal of qty of the variant from what is left
// (contracts.ProposeLots, M65), taken from the pool; short is what it could
// not cover, 0 when none.
func (p *lotPool) propose(variant int32, qty *big.Rat) ([]lotPick, *big.Rat, error) {
	rows := p.rows[variant]
	if len(rows) == 0 || qty.Sign() <= 0 {
		return nil, new(big.Rat).Set(qty), nil
	}
	picks, short, err := contracts.ProposeLots(rows, stockText(qty), p.today)
	if err != nil {
		return nil, nil, fmt.Errorf("orders: propose the lots of variant %d: %w", variant, err)
	}
	out := make([]lotPick, 0, len(picks))
	for _, pk := range picks {
		spot := lotSpot{pk.LotID, pk.LocationID}
		r, _ := p.find(variant, spot)
		q := mustRat(pk.Quantity)
		out = append(out, lotPick{lotID: pk.LotID, locationID: pk.LocationID, number: r.Number, code: r.FullCode, expiry: r.Expiry, qty: q})
		p.take(variant, spot, q)
	}
	left := new(big.Rat)
	if short != "" {
		left = mustRat(short)
	}
	return out, left, nil
}

// writePicks writes a delivery line's picks, in the order given.
func writePicks(ctx context.Context, txq *store.Queries, deliveryLineID int64, picks []lotPick) ([]store.OrdersDeliveryLineLot, error) {
	out := make([]store.OrdersDeliveryLineLot, 0, len(picks))
	for _, p := range picks {
		row, err := txq.InsertDeliveryLineLot(ctx, store.InsertDeliveryLineLotParams{
			DeliveryLineID: deliveryLineID, LotID: p.lotID, LotNumber: p.number, Expiry: pgDate(p.expiry), LocationID: p.locationID,
			LocationCode: p.code, Quantity: numeric(p.qty, quantityPlaces), AllowExpired: p.allowExpired, Chosen: p.chosen,
		})
		if err != nil {
			return nil, fmt.Errorf("orders: write delivery line %d's lots: %w", deliveryLineID, err)
		}
		out = append(out, row)
	}
	return out, nil
}

// picksByLine groups a delivery's stored picks by its line.
func picksByLine(rows []store.OrdersDeliveryLineLot) map[int64][]store.OrdersDeliveryLineLot {
	out := map[int64][]store.OrdersDeliveryLineLot{}
	for _, r := range rows {
		out[r.DeliveryLineID] = append(out[r.DeliveryLineID], r)
	}
	return out
}

// deliveryLotsView is what a delivery's view shows of its lots: which
// variants are tracked, each line's picks, and — on a draft — the site's
// lots of each variant for Change lot, judged expired on today.
type deliveryLotsView struct {
	tracked map[int32]bool
	picks   map[int64][]store.OrdersDeliveryLineLot
	choices map[int32][]contracts.StockLot
	today   time.Time
}

// lotsOut is a line's picks as the API answers them, and what they leave
// short of qty when the line is tracked.
func (v deliveryLotsView) lotsOut(dl store.OrdersDeliveryLine) (tracked bool, lots []gen.OrdersDeliveryLineLot, choices []gen.OrdersDeliveryLotChoice, short float64) {
	rows := v.picks[dl.ID]
	tracked = v.tracked[dl.VariantID] || len(rows) > 0
	lots = make([]gen.OrdersDeliveryLineLot, 0, len(rows))
	for _, r := range rows {
		lots = append(lots, gen.OrdersDeliveryLineLot{
			LotId: r.LotID, LotNumber: r.LotNumber, Expiry: dateOut(r.Expiry), LocationId: r.LocationID, LocationCode: r.LocationCode,
			Quantity: wire(ratOrZero(r.Quantity), quantityPlaces), AllowExpired: r.AllowExpired, Chosen: r.Chosen,
		})
	}
	choices = make([]gen.OrdersDeliveryLotChoice, 0)
	for _, c := range v.choices[dl.VariantID] {
		choices = append(choices, gen.OrdersDeliveryLotChoice{
			LotId: c.LotID, LotNumber: c.Number, Expiry: dateOutOf(c.Expiry), BestBefore: dateOutOf(c.BestBefore),
			LocationId: c.LocationID, FullCode: c.FullCode, Quantity: wire(mustRat(c.Quantity), quantityPlaces),
			Status: gen.OrdersDeliveryLotChoiceStatus(c.Status), Expired: contracts.LotExpired(c.Expiry, v.today),
		})
	}
	if tracked {
		if left := new(big.Rat).Sub(ratOrZero(dl.Quantity), sumRows(rows)); left.Sign() > 0 {
			short = wire(left, quantityPlaces)
		}
	}
	return tracked, lots, choices, short
}

// dateOutOf is a day Inventory answers as the API's date, nil for none.
func dateOutOf(d *time.Time) *openapi_types.Date {
	return dateOut(pgDate(d))
}

// picksOfOrderLines is a delivery's stored picks by the order line each of
// its lines delivers, read under the delivery's lock.
func picksOfOrderLines(ctx context.Context, txq *store.Queries, deliveryID int64) (map[int64][]lotPick, error) {
	dlines, err := txq.DeliveryLinesOf(ctx, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("orders: read delivery %d's lines: %w", deliveryID, err)
	}
	rows, err := txq.DeliveryLineLotsOf(ctx, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("orders: read delivery %d's lots: %w", deliveryID, err)
	}
	byLine := picksByLine(rows)
	out := map[int64][]lotPick{}
	for _, dl := range dlines {
		for _, r := range byLine[dl.ID] {
			out[dl.OrderLineID] = append(out[dl.OrderLineID], pickOfRow(r))
		}
	}
	return out, nil
}

// judgePicks judges the lots a tracked line is saved with (M77), the line
// at index i of the request: each a lot holding the variant at the location
// (lot_unknown) — a pick the line already had stands even when the lot no
// longer shows there — at a quantity more than 0 with at most three
// decimals, once per lot and location. Picks sent as the line had them keep
// whether each was chosen; any change marks them all chosen. Nil in is no
// lots sent.
func judgePicks(i int, variant int32, in *[]gen.OrdersDeliveryLotInput, old []lotPick, pool *lotPool) ([]lotPick, []lineFailure) {
	if in == nil {
		return nil, nil
	}
	var failures []lineFailure
	picks := make([]lotPick, 0, len(*in))
	seen := map[lotSpot]bool{}
	for j, li := range *in {
		at := &lotFailure{index: j, lotID: li.LotId}
		spot := lotSpot{li.LotId, li.LocationId}
		qty, msg := parseQuantity(li.Quantity)
		if msg != "" {
			failures = append(failures, lineFailure{index: i, code: codeQuantityInvalid, detail: msg, lot: at})
			continue
		}
		if seen[spot] {
			failures = append(failures, lineFailure{index: i, code: codeLineInvalid, detail: "A lot is named twice at one location.", lot: at})
			continue
		}
		seen[spot] = true
		var pick *lotPick
		for _, p := range old {
			if p.lotID == spot.lot && p.locationID == spot.location {
				pick = &p
				break
			}
		}
		if pick == nil {
			r, ok := pool.find(variant, spot)
			if !ok {
				failures = append(failures, lineFailure{index: i, code: codeLotUnknown,
					detail: fmt.Sprintf("Lot %d holds none of the item at location %d of the site.", li.LotId, li.LocationId), lot: at})
				continue
			}
			pick = &lotPick{lotID: r.LotID, locationID: r.LocationID, number: r.Number, code: r.FullCode, expiry: r.Expiry}
		}
		p := *pick
		p.qty, p.allowExpired = qty, li.AllowExpired != nil && *li.AllowExpired
		picks = append(picks, p)
	}
	if len(failures) > 0 {
		return nil, failures
	}
	same := len(picks) == len(old)
	for j := 0; same && j < len(picks); j++ {
		a, b := picks[j], old[j]
		same = a.lotID == b.lotID && a.locationID == b.locationID && a.qty.Cmp(b.qty) == 0 && a.allowExpired == b.allowExpired
	}
	for j := range picks {
		picks[j].chosen = !same || old[j].chosen
	}
	return picks, nil
}
