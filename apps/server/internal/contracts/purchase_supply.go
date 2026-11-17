package contracts

import (
	"context"
	"time"
)

// PurchaseSupply is Purchasing's open purchases (purchasing design P4, A3):
// what is ordered and not yet received on sent orders, in the variant's own
// unit, per variant and receiving site. It is a single-provider slot
// Purchasing fills, optional for its readers — Inventory's low-stock list and
// stock card, Manufacturing's replenishment — which read nil (Purchasing off)
// as "nothing is on order" and behave as before. It reads on the provider's
// pool, before any transaction, checks no user permission, and answers
// quantities as exact decimal text with no trailing zeros.
type PurchaseSupply interface {
	// OpenByVariants is the open purchases of the variants given, one entry
	// per (variant, site) with anything open: at that site only when siteID
	// is set. More than MaxStockVariants variants is a failure.
	OpenByVariants(ctx context.Context, siteID *int64, variantIDs []int32) ([]OpenPurchase, error)
	// OpenAll is every (variant, site) with anything open: what a statement
	// over every placement — the low-stock list and its count — joins in.
	OpenAll(ctx context.Context) ([]OpenPurchase, error)
}

// OpenPurchase is what is on order of one variant for one site.
type OpenPurchase struct {
	VariantID int32
	SiteID    int64
	// Quantity is ordered and not yet received, more than 0.
	Quantity string
	// ExpectedOn is the earliest expected day of the open lines, a calendar
	// date at midnight UTC; nil when none says.
	ExpectedOn *time.Time
}
