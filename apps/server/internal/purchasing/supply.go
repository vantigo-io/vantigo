package purchasing

// The open purchases (P4, A3, decision 17): contracts.PurchaseSupply, per
// (variant, site), the quantity ordered and not received on the goods variant
// lines of sent and partially received orders that are not closed, with the
// earliest expected day — the line's, else the order's. Drafts, closed and
// cancelled orders count nothing. It is what Inventory's low stock shows as
// on order, and what Manufacturing's replenishment counts as incoming. It
// reads on the module's pool, outside any transaction, and checks no user
// permission.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// purchaseSupply is the provider of contracts.PurchaseSupply.
type purchaseSupply struct{ pool *pgxpool.Pool }

var _ contracts.PurchaseSupply = purchaseSupply{}

func newPurchaseSupply(d module.Deps) contracts.PurchaseSupply { return purchaseSupply{pool: d.Pool} }

// OpenByVariants is the open purchases of the variants given, at the site
// when one is given.
func (p purchaseSupply) OpenByVariants(ctx context.Context, siteID *int64, variantIDs []int32) ([]contracts.OpenPurchase, error) {
	if len(variantIDs) > contracts.MaxStockVariants {
		return nil, fmt.Errorf("purchasing: %d variants is more than the %d one read takes", len(variantIDs), contracts.MaxStockVariants)
	}
	if len(variantIDs) == 0 {
		return []contracts.OpenPurchase{}, nil
	}
	return p.read(ctx, store.OpenPurchasesParams{VariantIds: variantIDs, SiteID: siteID})
}

// OpenAll is every (variant, site) with anything open.
func (p purchaseSupply) OpenAll(ctx context.Context) ([]contracts.OpenPurchase, error) {
	return p.read(ctx, store.OpenPurchasesParams{VariantIds: []int32{}})
}

func (p purchaseSupply) read(ctx context.Context, params store.OpenPurchasesParams) ([]contracts.OpenPurchase, error) {
	rows, err := store.New(p.pool).OpenPurchases(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the open purchases: %w", err)
	}
	out := make([]contracts.OpenPurchase, 0, len(rows))
	for _, r := range rows {
		op := contracts.OpenPurchase{VariantID: r.VariantID, SiteID: r.SiteID, Quantity: trimmed(ratOrZero(r.Quantity), quantityPlaces)}
		if r.ExpectedOn.Valid {
			d := time.Date(r.ExpectedOn.Time.Year(), r.ExpectedOn.Time.Month(), r.ExpectedOn.Time.Day(), 0, 0, 0, 0, time.UTC)
			op.ExpectedOn = &d
		}
		out = append(out, op)
	}
	return out, nil
}
