package contracts

import (
	"context"
	"time"
)

// PurchaseDemand is what should be bought (purchasing design P31, A4, A51):
// a slot with one source, Deps.PurchaseDemand, that Purchasing's proposals
// read. Inventory's low-stock rule and Manufacturing's buy suggestions both
// provide one; Manufacturing's supersedes Inventory's
// (Module.PurchaseDemandSupersedes), so where Manufacturing is enabled its
// buy suggestions are the demand, and Inventory's low stock where it is not.
type PurchaseDemand interface {
	// Demand is the need at the site, every site when siteID is nil.
	Demand(ctx context.Context, siteID *int64) ([]PurchaseNeed, error)
}

// PurchaseNeed is one variant's need at one site as its provider computed
// it, before the provider's own rounding: Purchasing rounds once, to its
// supplier item.
type PurchaseNeed struct {
	VariantID int32
	SiteID    int64
	// Quantity is the need in the variant's unit, more than 0.
	Quantity string
	// NeededOn is the day it is needed, nil when now.
	NeededOn *time.Time
	// Source names the provider's rule for a person: "low_stock",
	// "replenishment".
	Source string
	// ComputedAt is when the provider computed it: what was sent after it is
	// not yet in it.
	ComputedAt time.Time
}
