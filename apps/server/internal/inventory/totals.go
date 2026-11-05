package inventory

import (
	"context"
	"fmt"
	"math/big"

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
// product list's column (I33); a variant with neither stock nor a
// reservation is absent. Products
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
	rows, err := store.New(s.deps.Pool).StockTotals(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("inventory: read stock totals: %w", err)
	}
	out := gen.InventoryStockTotals{Totals: make([]gen.InventoryStockTotal, 0, len(rows))}
	for _, r := range rows {
		onHand, reserved := ratFromText(r.OnHand), ratFromText(r.Reserved)
		out.Totals = append(out.Totals, gen.InventoryStockTotal{
			VariantId: r.VariantID, OnHand: wireQuantity(onHand), Reserved: wireQuantity(reserved),
			Available: wireQuantity(new(big.Rat).Sub(onHand, reserved)),
		})
	}
	return gen.ListInventoryStockTotals200JSONResponse(out), nil
}
