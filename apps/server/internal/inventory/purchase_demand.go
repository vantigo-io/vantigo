package inventory

// The purchase demand (purchasing design P31, A4): contracts.PurchaseDemand
// from the low-stock list — the same statement, the same rule — one need per
// low placement at an active site, in the variant's unit, rounded to the
// placement's multiple and not to a packaging: Purchasing rounds once, to its
// supplier item. It is needed now, and computed when it is read: the time is
// taken before the open purchases are, so an order sent while it is read is
// counted at most twice, never missed. It is composed only where
// Manufacturing is not (module.Module.PurchaseDemandSupersedes): there,
// Manufacturing's buy suggestions are the demand. It reads on the pool,
// outside any transaction, checks no user permission, and refreshes no
// snapshot — the list's own reads do.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// sourceLowStock is the demand's source label.
const sourceLowStock = "low_stock"

// purchaseDemand is the module's contracts.PurchaseDemand.
type purchaseDemand struct{ s *server }

var _ contracts.PurchaseDemand = purchaseDemand{}

// newPurchaseDemand is Module's PurchaseDemand.
func newPurchaseDemand(d module.Deps) contracts.PurchaseDemand {
	return purchaseDemand{s: newServer(d)}
}

// Demand is every low placement's need, at the site when one is given.
func (p purchaseDemand) Demand(ctx context.Context, siteID *int64) ([]contracts.PurchaseNeed, error) {
	s := p.s
	computedAt := s.deps.Clock()
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	params := lowStockFilter(&fieldErrors{}, siteID, nil)
	params.Today = pgtype.Date{Time: today, Valid: true}
	if params.OpenVariantIds, params.OpenSiteIds, params.OpenQuantities, err = s.openPurchaseColumns(ctx); err != nil {
		return nil, err
	}
	out := []contracts.PurchaseNeed{}
	for {
		rows, last, err := s.lowStockRows(ctx, params, int(lowStockExportPage.Load()), staleSnapshots{})
		if err != nil {
			return nil, fmt.Errorf("inventory: read the purchase demand: %w", err)
		}
		for _, r := range rows {
			if r.verdict.beforePackaging == nil || r.verdict.beforePackaging.Sign() <= 0 {
				continue
			}
			out = append(out, contracts.PurchaseNeed{
				VariantID: r.row.VariantID, SiteID: r.row.SiteID, Quantity: decimalText(r.verdict.beforePackaging),
				Source: sourceLowStock, ComputedAt: computedAt,
			})
		}
		if last == nil {
			return out, nil
		}
		params.CursorSiteCode, params.CursorSku, params.CursorVariantID = &last.SiteCode, &last.Sku, &last.VariantID
	}
}
