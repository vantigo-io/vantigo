package contracts

import (
	"context"
	"time"
)

// VariantEntry is a product variant as another module may reference it:
// enough to name it, know its unit and what kind of product it belongs to,
// never enough to manage the catalog itself — that stays behind products'
// own contract and permissions.
type VariantEntry struct {
	ID, ProductID int32
	ProductName   string
	SKU           string
	Unit          string
	ProductType   string // "Goods" | "Service"
	ProductStatus string
}

// Money is an amount in a currency, the shape ListPrice answers a variant's
// effective price in.
type Money struct {
	Amount   float64
	Currency string
}

// ProductCatalog is the one sanctioned way a module reads products' catalog
// data: a read-only, in-process port over variants and their pricing, so a
// module can name a variant on a billing line and price it, without either
// importing the products package (barred by depguard) or reading its
// PostgreSQL schema (barred by internal/db/schema_test.go). Whichever
// enabled module owns catalog data implements it; Compose wires that
// implementation into every module's Deps before any Mount runs (see
// Module.Products). It is nil when no enabled module provides one (products
// disabled).
//
// A missing row is (nil, nil) from Variant, or simply absent from Variants'
// result — never an error. A caller tells "does not exist" from "the lookup
// failed" by checking err, never by treating a nil result or a shorter
// slice as failure. The same holds for ListPrice: a variant with no price in
// currency at the given moment is (nil, nil), not an error.
type ProductCatalog interface {
	// Variant looks up a variant by ID. It returns (nil, nil) if id does
	// not exist.
	Variant(ctx context.Context, id int32) (*VariantEntry, error)
	// Variants looks up variants by ID. An id that does not exist is simply
	// absent from the result, not an error.
	Variants(ctx context.Context, ids []int32) ([]VariantEntry, error)
	// ListPrice is the variant's effective price in currency at the given
	// moment, nil when it has none.
	ListPrice(ctx context.Context, variantID int32, currency string, at time.Time) (*Money, error)
}
