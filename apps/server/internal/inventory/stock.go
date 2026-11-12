package inventory

import (
	"cmp"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// What lies where (design I27, I29): a location's contents, a product's
// stock per variant, and one variant's stock — each read from the balances
// in one snapshot, the variants named by the catalog outside it.

// balance is one balance row as the reads carry it.
type balance struct {
	variantID  int32
	locationID int64
	onHand     *big.Rat
}

// stockSnapshot is the balances a read found, with their locations and sites,
// and — for a variant's or a product's stock — what is reserved of the
// variants per site (orders design O45) and what is on its way to each site
// on open transfers (phase 2 design I46).
type stockSnapshot struct {
	balances  []balance
	reserved  map[siteKey]*big.Rat
	incoming  map[siteKey]*big.Rat
	locations map[int64]store.InventoryLocation
	sites     map[int64]store.InventorySite
	// placements are the variants' placements per site (I54), for a
	// variant's or a product's stock and I53's batch.
	placements map[siteKey]store.InventoryPlacement
	// lots are the balances in lots, in FEFO order (manufacturing phase 2
	// M63), and today the day their expiry is judged on.
	lots  []lotBalance
	today time.Time
}

// lotBalance is one balance in a lot as the reads carry it.
type lotBalance struct {
	variantID  int32
	locationID int64
	onHand     *big.Rat
	lot        store.LotStockOfVariantsRow
}

// unusable reports whether a lot's stock is not usable on today: on hold, or
// expired (M63).
func (b lotBalance) unusable(today time.Time) bool {
	return b.lot.Status != string(gen.InventoryLotStatusAvailable) || contracts.LotExpired(dateOrNil(b.lot.Expiry), today)
}

// held is what lies of the variant at the site in lots on hold or expired
// (M63): on hand less held is usable on hand, what can be promised.
func (sn *stockSnapshot) held(k siteKey) *big.Rat {
	out := new(big.Rat)
	for _, b := range sn.lots {
		if b.variantID == k.variantID && sn.locations[b.locationID].SiteID == k.siteID && b.unusable(sn.today) {
			out.Add(out, b.onHand)
		}
	}
	return out
}

// lotsAt is the variant's lots on the location, in FEFO order; nil when none.
func (sn *stockSnapshot) lotsAt(variantID int32, locationID int64) *[]gen.InventoryBalanceLot {
	var out []gen.InventoryBalanceLot
	for _, b := range sn.lots {
		if b.variantID != variantID || b.locationID != locationID {
			continue
		}
		out = append(out, gen.InventoryBalanceLot{
			LotId: b.lot.LotID, Number: b.lot.Number, Status: gen.InventoryLotStatus(b.lot.Status),
			Expired: contracts.LotExpired(dateOrNil(b.lot.Expiry), sn.today), Expiry: wireDate(dateOrNil(b.lot.Expiry)),
			BestBefore: wireDate(dateOrNil(b.lot.BestBefore)), OnHand: wireQuantity(b.onHand), ReceivedAt: b.lot.ReceivedAt,
		})
	}
	if out == nil {
		return nil
	}
	return &out
}

// siteKey names a variant at a site: where a reservation is held.
type siteKey struct {
	variantID int32
	siteID    int64
}

// readPlaces reads the locations the balances lie on, and their sites, into
// the snapshot.
func (sn *stockSnapshot) readPlaces(ctx context.Context, q *store.Queries) error {
	ids := make([]int64, 0, len(sn.balances))
	for _, b := range sn.balances {
		ids = append(ids, b.locationID)
	}
	rows, err := q.LocationsByIDs(ctx, distinct(ids))
	if err != nil {
		return fmt.Errorf("inventory: read the locations of balances: %w", err)
	}
	sn.locations = make(map[int64]store.InventoryLocation, len(rows))
	siteIDs := make([]int64, 0, len(rows)+len(sn.reserved))
	for _, r := range rows {
		sn.locations[r.ID] = r
		siteIDs = append(siteIDs, r.SiteID)
	}
	for k := range sn.reserved {
		siteIDs = append(siteIDs, k.siteID)
	}
	for k := range sn.incoming {
		siteIDs = append(siteIDs, k.siteID)
	}
	for k := range sn.placements {
		siteIDs = append(siteIDs, k.siteID)
	}
	sites, err := q.SitesByIDs(ctx, distinct(siteIDs))
	if err != nil {
		return fmt.Errorf("inventory: read the sites of balances: %w", err)
	}
	sn.sites = make(map[int64]store.InventorySite, len(sites))
	for _, s := range sites {
		sn.sites[s.ID] = s
	}
	return nil
}

// row is one balance as the contract answers it.
func (sn *stockSnapshot) row(b balance, ref gen.InventoryVariantRef) gen.InventoryBalanceRow {
	return gen.InventoryBalanceRow{Variant: ref, Location: locationRefOf(sn.locations[b.locationID]), OnHand: wireQuantity(b.onHand)}
}

// variantStock is one variant's stock by site, sites by code and rows by full
// code, with the totals summed exactly: on hand, reserved, held — in lots on
// hold or expired (manufacturing phase 2 M63) — and available: usable on hand
// (on hand less held) less reserved, negative when a correction or a hold
// took it below what is reserved (orders design O45); and incoming per site,
// in transit to it (I46). Each row carries the variant's lots on it. A site holding only a reservation, only incoming, or only a
// placement at an active site is listed with no rows. A site's placement rides on its row, and
// its primary location's row comes first, marked (I55).
func (sn *stockSnapshot) variantStock(ref gen.InventoryVariantRef) gen.InventoryVariantStock {
	total, totalReserved, totalHeld := new(big.Rat), new(big.Rat), new(big.Rat)
	bySite := map[int64]*big.Rat{}
	rows := map[int64][]gen.InventoryBalanceRow{}
	for _, b := range sn.balances {
		if b.variantID != ref.Id {
			continue
		}
		siteID := sn.locations[b.locationID].SiteID
		if bySite[siteID] == nil {
			bySite[siteID] = new(big.Rat)
		}
		bySite[siteID].Add(bySite[siteID], b.onHand)
		total.Add(total, b.onHand)
		row := sn.row(b, ref)
		row.Lots = sn.lotsAt(b.variantID, b.locationID)
		rows[siteID] = append(rows[siteID], row)
	}
	for k, q := range sn.reserved {
		if k.variantID != ref.Id {
			continue
		}
		if bySite[k.siteID] == nil {
			bySite[k.siteID] = new(big.Rat)
		}
		totalReserved.Add(totalReserved, q)
	}
	for k := range sn.incoming {
		if k.variantID == ref.Id && bySite[k.siteID] == nil {
			bySite[k.siteID] = new(big.Rat)
		}
	}
	for k := range sn.placements {
		if k.variantID == ref.Id && bySite[k.siteID] == nil && sn.sites[k.siteID].Status == string(gen.Active) {
			bySite[k.siteID] = new(big.Rat)
		}
	}
	for siteID := range bySite {
		totalHeld.Add(totalHeld, sn.held(siteKey{ref.Id, siteID}))
	}
	usable := new(big.Rat).Sub(total, totalHeld)
	out := gen.InventoryVariantStock{
		Variant: ref, TotalOnHand: wireQuantity(total), TotalReserved: wireQuantity(totalReserved),
		TotalAvailable: wireQuantity(new(big.Rat).Sub(usable, totalReserved)), Sites: make([]gen.InventorySiteStock, 0, len(bySite)),
	}
	for siteID, onHand := range bySite {
		r := rows[siteID]
		if r == nil {
			r = []gen.InventoryBalanceRow{}
		}
		k := siteKey{ref.Id, siteID}
		placement, placed := sn.placements[k]
		for i := range r {
			r[i].Primary = ptr(placed && placement.PrimaryLocationID != nil && *placement.PrimaryLocationID == r[i].Location.Id)
		}
		slices.SortFunc(r, func(a, b gen.InventoryBalanceRow) int {
			return cmp.Or(-cmpBool(*a.Primary, *b.Primary), strings.Compare(a.Location.FullCode, b.Location.FullCode))
		})
		reserved, incoming, held := orZero(sn.reserved[k]), orZero(sn.incoming[k]), sn.held(k)
		usable := new(big.Rat).Sub(onHand, held)
		row := gen.InventorySiteStock{
			Site: siteRefOf(sn.sites[siteID]), OnHand: wireQuantity(onHand), Reserved: wireQuantity(reserved),
			Available: wireQuantity(new(big.Rat).Sub(usable, reserved)), Held: wireQuantity(held), Rows: r,
			Incoming: wireQuantity(incoming),
		}
		if placed {
			row.Placement = ptr(placementOf(placement, sn.sites[siteID], usable, reserved, incoming))
		}
		out.Sites = append(out.Sites, row)
	}
	slices.SortFunc(out.Sites, func(a, b gen.InventorySiteStock) int { return strings.Compare(a.Site.Code, b.Site.Code) })
	return out
}

// orZero is q, or 0 for none.
func orZero(q *big.Rat) *big.Rat {
	if q == nil {
		return new(big.Rat)
	}
	return q
}

// balancesOf reads the variants' balances, what is reserved and incoming of
// them, their lots, and their places in one snapshot, with today read on the
// pool before it.
func (s *server) balancesOf(ctx context.Context, ids []int32, then func(ctx context.Context, q *store.Queries) error) (stockSnapshot, error) {
	var sn stockSnapshot
	today, err := s.today(ctx)
	if err != nil {
		return sn, err
	}
	sn.today = today
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.BalancesOfVariants(ctx, ids)
		if err != nil {
			return fmt.Errorf("inventory: read balances: %w", err)
		}
		for _, r := range rows {
			sn.balances = append(sn.balances, balance{r.VariantID, r.LocationID, ratFromNumeric(r.OnHand)})
		}
		reserved, err := q.ReservedOfVariants(ctx, ids)
		if err != nil {
			return fmt.Errorf("inventory: read reservations: %w", err)
		}
		sn.reserved = make(map[siteKey]*big.Rat, len(reserved))
		for _, r := range reserved {
			sn.reserved[siteKey{r.VariantID, r.SiteID}] = ratFromText(r.Reserved)
		}
		incoming, err := q.IncomingOfVariants(ctx, ids)
		if err != nil {
			return fmt.Errorf("inventory: read what is incoming: %w", err)
		}
		sn.incoming = make(map[siteKey]*big.Rat, len(incoming))
		for _, r := range incoming {
			sn.incoming[siteKey{r.VariantID, r.SiteID}] = ratFromText(r.Incoming)
		}
		lots, err := q.LotStockOfVariants(ctx, ids)
		if err != nil {
			return fmt.Errorf("inventory: read the lots' stock: %w", err)
		}
		for _, r := range lots {
			sn.lots = append(sn.lots, lotBalance{r.VariantID, r.LocationID, ratFromNumeric(r.OnHand), r})
		}
		placements, err := q.PlacementsOfVariants(ctx, ids)
		if err != nil {
			return fmt.Errorf("inventory: read placements: %w", err)
		}
		sn.placements = make(map[siteKey]store.InventoryPlacement, len(placements))
		for _, p := range placements {
			sn.placements[siteKey{p.VariantID, p.SiteID}] = p
		}
		if err := sn.readPlaces(ctx, q); err != nil {
			return err
		}
		if then != nil {
			return then(ctx, q)
		}
		return nil
	})
	return sn, err
}

// GetInventoryLocationContents List what lies on a location
// (GET /api/v1/inventory/locations/{id}/contents)
//
// What lies on the location and, unless subtree is false, below it (I29), by
// full code then SKU.
func (s *server) GetInventoryLocationContents(ctx context.Context, req gen.GetInventoryLocationContentsRequestObject) (gen.GetInventoryLocationContentsResponseObject, error) {
	subtree := req.Params.Subtree == nil || *req.Params.Subtree
	var sn stockSnapshot
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetLocation(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("inventory: read location %d: %w", req.Id, err)
		}
		rows, err := q.BalancesAt(ctx, store.BalancesAtParams{ID: req.Id, Subtree: subtree})
		if err != nil {
			return fmt.Errorf("inventory: read the contents of location %d: %w", req.Id, err)
		}
		for _, r := range rows {
			sn.balances = append(sn.balances, balance{r.VariantID, r.LocationID, ratFromNumeric(r.OnHand)})
		}
		return sn.readPlaces(ctx, q)
	})
	if status, _, ok := answerOf(err); ok && status == http.StatusNotFound {
		return gen.GetInventoryLocationContents404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]int32, 0, len(sn.balances))
	for _, b := range sn.balances {
		ids = append(ids, b.variantID)
	}
	refs, err := s.variantRefs(ctx, ids)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.GetInventoryLocationContents503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	out := gen.InventoryLocationContents{Rows: make([]gen.InventoryBalanceRow, 0, len(sn.balances))}
	for _, b := range sn.balances {
		out.Rows = append(out.Rows, sn.row(b, refs[b.variantID]))
	}
	slices.SortStableFunc(out.Rows, func(a, b gen.InventoryBalanceRow) int {
		return cmp.Or(strings.Compare(a.Location.FullCode, b.Location.FullCode), strings.Compare(a.Variant.Sku, b.Variant.Sku))
	})
	return gen.GetInventoryLocationContents200JSONResponse(out), nil
}

// GetInventoryProductStock Get a product's stock
// (GET /api/v1/inventory/products/{productId}/stock)
//
// The product page's Stock card (I27): every goods variant of the product in
// the catalog's order, with or without stock, each by site and location. A
// product the catalog does not know is the 404; a Service product answers
// no variants.
func (s *server) GetInventoryProductStock(ctx context.Context, req gen.GetInventoryProductStockRequestObject) (gen.GetInventoryProductStockResponseObject, error) {
	entries, err := s.catalogVariantsOfProduct(ctx, req.ProductId)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.GetInventoryProductStock503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return gen.GetInventoryProductStock404Response{}, nil
	}
	goods := slices.DeleteFunc(slices.Clone(entries), func(e contracts.VariantEntry) bool { return e.ProductType != productTypeGoods })
	ids := make([]int32, 0, len(goods))
	for _, e := range goods {
		ids = append(ids, e.ID)
	}
	sn, err := s.balancesOf(ctx, ids, nil)
	if err != nil {
		return nil, err
	}
	out := gen.InventoryProductStock{ProductId: req.ProductId, Variants: make([]gen.InventoryVariantStock, 0, len(goods))}
	for _, e := range goods {
		out.Variants = append(out.Variants, sn.variantStock(variantRefOf(e)))
	}
	return gen.GetInventoryProductStock200JSONResponse(out), nil
}

// GetInventoryVariantStock Get a variant's stock
// (GET /api/v1/inventory/variants/{variantId}/stock)
//
// One variant's stock by site and location (I27). A variant the catalog no
// longer knows is named by its latest movement line's snapshot; one neither
// the catalog nor the ledger knows is the 404.
func (s *server) GetInventoryVariantStock(ctx context.Context, req gen.GetInventoryVariantStockRequestObject) (gen.GetInventoryVariantStockResponseObject, error) {
	entries, err := s.catalogVariants(ctx, []int32{req.VariantId})
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.GetInventoryVariantStock503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	var ref *gen.InventoryVariantRef
	if len(entries) == 1 {
		ref = ptr(variantRefOf(entries[0]))
	}
	sn, err := s.balancesOf(ctx, []int32{req.VariantId}, func(ctx context.Context, q *store.Queries) error {
		if ref != nil {
			return nil
		}
		snapshots, err := q.LastLineSnapshots(ctx, []int32{req.VariantId})
		if err != nil {
			return fmt.Errorf("inventory: read the snapshot of variant %d: %w", req.VariantId, err)
		}
		if len(snapshots) == 1 {
			r := snapshots[0]
			ref = ptr(deletedVariantRef(r.VariantID, r.Sku, r.ProductName, r.Unit))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return gen.GetInventoryVariantStock404Response{}, nil
	}
	return gen.GetInventoryVariantStock200JSONResponse(sn.variantStock(*ref)), nil
}
