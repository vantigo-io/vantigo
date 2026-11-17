package contracts

import (
	"context"
	"time"
)

// PurchaseDemand is what should be bought (purchasing design P31, A4): a
// many-provider slot, filled by Inventory's low-stock rule and by
// Manufacturing's buy suggestions, that Purchasing's proposals read. It is
// declared ahead of its providers and its slot, which come with delivery C.
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
