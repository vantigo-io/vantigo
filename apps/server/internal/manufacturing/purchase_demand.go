package manufacturing

// The purchase demand (purchasing design P31, A4): contracts.PurchaseDemand
// from the replenishment's stored buy suggestions — the open ones, of the
// last run per site — each its quantity as the run stored it (rounded to the
// placement's multiple, never to a packaging: Purchasing rounds once, to its
// supplier item), needed by its need-by day, computed when its run started,
// before the run read anything. Where Manufacturing is enabled these rows are
// the proposals and Inventory's low stock is not asked
// (module.Module.PurchaseDemandSupersedes): a make suggestion is never
// proposed for purchase. It reads on the pool, outside any transaction, and
// checks no user permission.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// sourceReplenishment is the demand's source label.
const sourceReplenishment = "replenishment"

// purchaseDemand is the module's contracts.PurchaseDemand.
type purchaseDemand struct{ pool *pgxpool.Pool }

var _ contracts.PurchaseDemand = purchaseDemand{}

// newPurchaseDemand is Module's PurchaseDemand.
func newPurchaseDemand(d module.Deps) contracts.PurchaseDemand { return purchaseDemand{pool: d.Pool} }

// Demand is the open buy suggestions, at the site when one is given.
func (p purchaseDemand) Demand(ctx context.Context, siteID *int64) ([]contracts.PurchaseNeed, error) {
	rows, err := store.New(p.pool).OpenBuySuggestions(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: read the buy suggestions: %w", err)
	}
	out := make([]contracts.PurchaseNeed, 0, len(rows))
	for _, r := range rows {
		q := textRat(r.Quantity)
		if q == nil || q.Sign() <= 0 {
			continue
		}
		n := contracts.PurchaseNeed{VariantID: r.VariantID, SiteID: r.SiteID, Quantity: text(q, quantityPlaces),
			Source: sourceReplenishment, ComputedAt: r.ComputedAt}
		if r.NeedBy.Valid {
			d := time.Date(r.NeedBy.Time.Year(), r.NeedBy.Time.Month(), r.NeedBy.Time.Day(), 0, 0, 0, 0, time.UTC)
			n.NeededOn = &d
		}
		out = append(out, n)
	}
	return out, nil
}
