package contracts

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
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
	// Barcode is the variant's own GTIN, nil when it has none.
	Barcode *string
	// Packagings are the variant's packagings (inventory design I24), by sort
	// order then id; empty when it has none. Every read of the catalog fills
	// them, LockVariants included, so a posting converts what was entered in
	// packagings at the quantities that hold while it is locked.
	Packagings []PackagingEntry
}

// PackagingEntry is a packaging of a variant — a box of 15, a Euro pallet of
// 40 boxes — in which stock may be entered and shown, never kept: stock is
// always in the variant's own unit (inventory design I24). Quantity is the
// packaging's content in the variant's unit, derived when the packaging is
// built from another one.
type PackagingEntry struct {
	ID        int32
	Name      string
	Kind      string // "inner" | "case" | "pallet" | "other"
	Quantity  string // a decimal in the variant's unit, three places
	Barcode   *string
	SortOrder int32
}

// CodeMatch is one answer of LookupCode: a variant, and the packaging whose
// barcode matched when it was one.
type CodeMatch struct {
	Variant   VariantEntry
	Packaging *PackagingEntry
}

// Money is an amount in a currency, the shape ListPrice answers a variant's
// effective price in.
type Money struct {
	Amount   float64
	Currency string
}

// ProductCatalog is the one sanctioned way a module reads products' catalog
// data: an in-process port over variants and their pricing, so a
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
//
// Every method but LockVariants runs on the pool and is called before the
// caller opens a transaction (module-boundaries rule 8's discipline).
// LockVariants is the one locking read run inside the caller's transaction
// (inventory design I22): it takes FOR SHARE on the variants and their
// products, so a posting's view of a variant — that it exists, is goods and
// is not discontinued — holds until the posting commits, and a variant's
// delete or a product's change to Service waits for it. The lock order is
// Products' rows first, then the caller's, everywhere; the provider never
// calls back into the caller.
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
	// VariantsOfProduct is every variant of the product in the catalog's
	// order, empty when the product does not exist.
	VariantsOfProduct(ctx context.Context, productID int32) ([]VariantEntry, error)
	// LookupCode answers every variant whose SKU equals code, ignoring case,
	// or whose barcode equals it exactly — and, from Inventory's delivery B,
	// every packaging whose barcode does. None is an empty slice, not an
	// error.
	LookupCode(ctx context.Context, code string) ([]CodeMatch, error)
	// SearchVariants answers at most limit variants of Goods products whose
	// SKU, product name or barcode contains q, ignoring case, by SKU.
	SearchVariants(ctx context.Context, q string, limit int) ([]VariantEntry, error)
	// LockVariants takes FOR SHARE on the variants' rows and their products'
	// rows, in variant id order, inside tx, and answers the entries. An id
	// that does not exist is absent from the answer. It never begins, commits
	// or rolls back tx.
	LockVariants(ctx context.Context, tx pgx.Tx, ids []int32) ([]VariantEntry, error)
}
