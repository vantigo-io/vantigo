package inventory

import (
	"context"
	"fmt"
	"math/big"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// maxTotalVariants is the most variants one request of totals names: a page
// of the product list (I33).
const maxTotalVariants = 100

// ListInventoryStockTotals Stock totals of variants
// (GET /api/v1/inventory/stock)
//
// Each variant's on hand across every location, summed exactly, what is
// reserved of it across every site, and available, the difference, for the
// product list's column (I33), and what of it is on its way between sites
// (inTransit, phase 2 design I46: not part of onHand); a variant with no
// stock, no reservation and nothing in transit is absent. Products
// reads nothing of Inventory: the host composes the column from this.
func (s *server) ListInventoryStockTotals(ctx context.Context, req gen.ListInventoryStockTotalsRequestObject) (gen.ListInventoryStockTotalsResponseObject, error) {
	ids := distinct(req.Params.VariantIds)
	var f fieldErrors
	switch {
	case len(ids) == 0:
		f.add("variantIds", "Name at least one variant")
	case len(ids) > maxTotalVariants:
		f.add("variantIds", fmt.Sprintf("Name at most %d variants", maxTotalVariants))
	}
	if f.failed() {
		return gen.ListInventoryStockTotals400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	var (
		rows    []store.StockTotalsRow
		transit []store.InTransitOfVariantsRow
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if rows, err = q.StockTotals(ctx, ids); err != nil {
			return fmt.Errorf("inventory: read stock totals: %w", err)
		}
		if transit, err = q.InTransitOfVariants(ctx, ids); err != nil {
			return fmt.Errorf("inventory: read what is in transit: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	totals := map[int32]*gen.InventoryStockTotal{}
	order := make([]int32, 0, len(rows)+len(transit))
	for _, r := range rows {
		onHand, reserved := ratFromText(r.OnHand), ratFromText(r.Reserved)
		totals[r.VariantID] = &gen.InventoryStockTotal{
			VariantId: r.VariantID, OnHand: wireQuantity(onHand), Reserved: wireQuantity(reserved),
			Available: wireQuantity(new(big.Rat).Sub(onHand, reserved)),
		}
		order = append(order, r.VariantID)
	}
	// A variant wholly in transit keeps its row (phase 2 design I46).
	for _, r := range transit {
		t, ok := totals[r.VariantID]
		if !ok {
			t = &gen.InventoryStockTotal{VariantId: r.VariantID}
			totals[r.VariantID] = t
			order = append(order, r.VariantID)
		}
		t.InTransit = wireQuantity(ratFromText(r.InTransit))
	}
	slices.Sort(order)
	out := gen.InventoryStockTotals{Totals: make([]gen.InventoryStockTotal, 0, len(order))}
	for _, id := range order {
		out.Totals = append(out.Totals, *totals[id])
	}
	return gen.ListInventoryStockTotals200JSONResponse(out), nil
}
