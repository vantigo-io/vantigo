package inventory

import (
	"cmp"
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Stock of variants at sites (phase 2 design I53): one batch read for the
// transfer form's two columns and the send dialog's defaults — per variant
// and site asked for, what lies there, what is reserved, available, what is
// on its way to it, and the locations holding it, most first, the primary
// location marked; and the placement there (I54). Every pair asked for has
// its row, whether or not anything lies there.

// The most a request names.
const (
	maxStockSitesVariants = 100
	maxStockSites         = 20
)

// ListInventoryStockAtSites Stock of variants at sites
// (GET /api/v1/inventory/stock/sites)
func (s *server) ListInventoryStockAtSites(ctx context.Context, req gen.ListInventoryStockAtSitesRequestObject) (gen.ListInventoryStockAtSitesResponseObject, error) {
	variantIDs, siteIDs := distinct(req.Params.VariantIds), distinct(req.Params.SiteIds)
	var f fieldErrors
	switch {
	case len(variantIDs) == 0:
		f.add("variantIds", "Name at least one variant")
	case len(variantIDs) > maxStockSitesVariants:
		f.add("variantIds", fmt.Sprintf("Name at most %d variants", maxStockSitesVariants))
	}
	switch {
	case len(siteIDs) == 0:
		f.add("siteIds", "Name at least one site")
	case len(siteIDs) > maxStockSites:
		f.add("siteIds", fmt.Sprintf("Name at most %d sites", maxStockSites))
	}
	if f.failed() {
		return gen.ListInventoryStockAtSites400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	var known map[int64]bool
	sn, err := s.balancesOf(ctx, variantIDs, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.SitesByIDs(ctx, siteIDs)
		if err != nil {
			return fmt.Errorf("inventory: read the sites asked for: %w", err)
		}
		known = make(map[int64]bool, len(rows))
		for _, r := range rows {
			known[r.ID] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, id := range siteIDs {
		if !known[id] {
			f.add("siteIds", fmt.Sprintf("No site has the id %d", id))
		}
	}
	if f.failed() {
		return gen.ListInventoryStockAtSites400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	return gen.ListInventoryStockAtSites200JSONResponse(sn.atSites(variantIDs, siteIDs)), nil
}

// atSites is the snapshot as I53's rows: one per variant and site, in the
// order asked, each site's locations most first, then by full code.
func (sn *stockSnapshot) atSites(variantIDs []int32, siteIDs []int64) gen.InventoryStockAtSites {
	onHand := map[siteKey]*big.Rat{}
	locations := map[siteKey][]balance{}
	for _, b := range sn.balances {
		k := siteKey{b.variantID, sn.locations[b.locationID].SiteID}
		if onHand[k] == nil {
			onHand[k] = new(big.Rat)
		}
		onHand[k].Add(onHand[k], b.onHand)
		locations[k] = append(locations[k], b)
	}
	out := gen.InventoryStockAtSites{Stock: make([]gen.InventoryStockAtSite, 0, len(variantIDs)*len(siteIDs))}
	for _, v := range variantIDs {
		for _, site := range siteIDs {
			k := siteKey{v, site}
			here, reserved := orZero(onHand[k]), orZero(sn.reserved[k])
			held := locations[k]
			slices.SortFunc(held, func(a, b balance) int {
				return cmp.Or(b.onHand.Cmp(a.onHand), strings.Compare(sn.locations[a.locationID].FullCode, sn.locations[b.locationID].FullCode))
			})
			placement, placed := sn.placements[k]
			rows := make([]gen.InventoryStockAtSiteLocation, 0, len(held))
			for _, b := range held {
				rows = append(rows, gen.InventoryStockAtSiteLocation{
					Location: locationRefOf(sn.locations[b.locationID]), OnHand: wireQuantity(b.onHand),
					Primary: placed && placement.PrimaryLocationID != nil && *placement.PrimaryLocationID == b.locationID,
				})
			}
			incoming := orZero(sn.incoming[k])
			row := gen.InventoryStockAtSite{
				VariantId: v, SiteId: site, OnHand: wireQuantity(here), Reserved: wireQuantity(reserved),
				Available: wireQuantity(new(big.Rat).Sub(here, reserved)), Incoming: wireQuantity(incoming),
				Locations: rows,
			}
			if placed {
				row.Placement = ptr(placementOf(placement, sn.sites[site], here, reserved, incoming))
			}
			out.Stock = append(out.Stock, row)
		}
	}
	return out
}
