package contracts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MaxStockVariants is the most variants one Stock read names.
const MaxStockVariants = 1000

// Stock is Inventory's read of what lies in stock and what of it is free
// (orders design O45, inventory design §11): on hand, and available — on
// hand less what is reserved — per variant, in total and per site. It is a
// single-provider slot, nil when Inventory is off, which a caller reads as
// "stock is not kept". The rules a provider keeps:
//
//   - It reads on the pool, in one snapshot per call, never under a lock of
//     the caller's; the caller never calls it inside a transaction that holds
//     one.
//   - It answers every variant asked, in the order asked, a variant with no
//     stock and no reservation at 0 with no sites.
//   - Quantities are exact decimal text, in the variant's own unit, with no
//     trailing zeros ("12", "2.5"). Available may be negative: Inventory's own
//     corrections may take on hand below what is reserved.
//   - It refuses more than MaxStockVariants variants, or a variant named
//     twice (ValidateStockVariants), as a failure.
type Stock interface {
	// OnHand is each variant's on hand: with siteID nil, across every site
	// and per site holding it; with siteID set, at that site only.
	OnHand(ctx context.Context, variantIDs []int32, siteID *int64) ([]StockQuantity, error)
	// Available is each variant's on hand less what is reserved, the same
	// way: Sites lists every site where the variant lies or is reserved.
	Available(ctx context.Context, variantIDs []int32, siteID *int64) ([]StockQuantity, error)
}

// StockQuantity is one variant's quantity, in total and per site.
type StockQuantity struct {
	VariantID int32
	Quantity  string
	// Sites are the sites the variant lies or is reserved in, by id.
	Sites []SiteQuantity
}

// SiteQuantity is a variant's quantity at one site.
type SiteQuantity struct {
	SiteID   int64
	Quantity string
}

// ValidateStockVariants is a Stock read's shape: at least one variant, at
// most MaxStockVariants, none named twice.
func ValidateStockVariants(ids []int32) error {
	switch {
	case len(ids) == 0:
		return errors.New("contracts: a stock read names no variant")
	case len(ids) > MaxStockVariants:
		return fmt.Errorf("contracts: a stock read names %d variants, more than %d", len(ids), MaxStockVariants)
	}
	if id, ok := firstDuplicate(ids); ok {
		return fmt.Errorf("contracts: a stock read names variant %d twice", id)
	}
	return nil
}

// StockLedger is Inventory's write for another module's documents (orders
// design O45, module-boundaries rule 24): it reserves stock for a document's
// lines at a site, releases it, issues stock against it, and reads what a
// document holds — every call inside the caller's transaction. It is composed
// whatever MODULES says, as the reference holders are: a reservation made
// while Inventory was on must still be released by a cancel, an edit or a
// manual marking after it is switched off. A caller decides whether to
// reserve and issue by Deps.Stock (nil when Inventory is off); it releases
// whenever StockLedger is there. The rules a provider keeps — rule 8's,
// with rule 24's writes:
//
//   - It never begins, commits or rolls back a transaction, and never touches
//     a pool: tx is the caller's. Its SQL is its own, on its own schema.
//   - It reads no directory and calls no contract but
//     ProductCatalog.LockVariants, on tx.
//   - The lock order is the caller's: ProductCatalog.LockVariants first, then
//     the caller's own rows, then — inside these calls — Inventory's
//     balances, in (variant, location) order, and its reservations. A caller
//     never locks Inventory's rows another way and never calls the ledger
//     before it has locked the variants.
//   - It refuses only through *StockRefusal, naming every failing line, and
//     writes nothing when it refuses; the caller rolls back or carries on
//     without the write. Anything else is a failure.
//   - Every call names its document by a StockSource, the reference
//     Inventory's movements and reservations keep.
type StockLedger interface {
	// Reserve adds each line's quantity to the line's reservation at the
	// site, after checking that the variant is goods and the site exists, is
	// active and fulfils orders, and that the site's available — on hand
	// summed over its locations, less every reservation there — covers it.
	Reserve(ctx context.Context, tx pgx.Tx, src StockSource, siteID int64, lines []StockLine) error
	// Release gives back reservations of the document: with no releases,
	// every reservation it holds; otherwise, per release, the line's whole
	// reservation (Quantity "") or that quantity of it, at every site the
	// line is reserved in, by site id, never below zero. A line holding no
	// reservation is no refusal: it holds nothing to release.
	Release(ctx context.Context, tx pgx.Tx, src StockSource, releases []StockRelease) error
	// Issue posts one movement of kind "issue", taking each line's quantity
	// off its from-location at the site, and consumes as much of each line's
	// reservation at the site as it issued (never below zero). The movement's
	// source is issue.Document (src when zero), and posting it is idempotent
	// by that source: a second Issue for one document answers the first
	// movement and consumes nothing again. Stock the location does not hold
	// is refused (StockInsufficientStock); reservations of other documents
	// never stop an issue.
	Issue(ctx context.Context, tx pgx.Tx, src StockSource, issue StockIssue) (movementID int64, err error)
	// ReservationsFor is every reservation the document holds, by line id
	// then site id.
	ReservationsFor(ctx context.Context, tx pgx.Tx, src StockSource) ([]Reservation, error)
}

// StockSource names a document of another module: "orders", "order", the
// order's id. Module and Kind are lower-case words without "/".
type StockSource struct {
	Module, Kind string
	ID           int64
}

// Validate is the source's shape.
func (s StockSource) Validate() error {
	if s.Module == "" || s.Kind == "" || s.ID <= 0 || strings.Contains(s.Module, "/") || strings.Contains(s.Kind, "/") {
		return fmt.Errorf("contracts: invalid stock source %q/%q/%d", s.Module, s.Kind, s.ID)
	}
	return nil
}

// IsZero reports whether the source is unset.
func (s StockSource) IsZero() bool { return s == StockSource{} }

// StockLine is one line of a document to reserve for: its variant, the
// document's line id and a quantity more than 0, exact decimal text with at
// most three decimals.
type StockLine struct {
	VariantID int32
	LineID    int64
	Quantity  string
}

// StockRelease is one line whose reservation is given back: all of it when
// Quantity is "", else that quantity (more than 0, at most three decimals).
type StockRelease struct {
	LineID   int64
	Quantity string
}

// StockIssue is a movement of stock out of a site for a document: the
// document the movement records (the delivery; the reserving source when
// zero), the site, the lines, and who posted it.
type StockIssue struct {
	Document StockSource
	SiteID   int64
	Lines    []IssueLine
	// PostedByUserID and PostedByDisplay are the poster, as the movement
	// keeps it; both may be empty for a system posting.
	PostedByUserID  *uuid.UUID
	PostedByDisplay string
}

// IssueLine is one line of an issue: the variant, the reserving document's
// line id whose reservation it consumes, the location it is taken from, and
// a quantity more than 0 (exact decimal text, at most three decimals).
type IssueLine struct {
	VariantID      int32
	LineID         int64
	FromLocationID int64
	Quantity       string
}

// Reservation is what one line of a document holds at one site.
type Reservation struct {
	VariantID int32
	LineID    int64
	SiteID    int64
	Quantity  string
}

// The codes a StockRefusal carries.
const (
	// StockInsufficientAvailable: the site's available does not cover a
	// reservation.
	StockInsufficientAvailable = "insufficient_available"
	// StockInsufficientStock: the location does not hold what an issue takes
	// (Inventory's own code, I10).
	StockInsufficientStock = "insufficient_stock"
	// StockSiteNotFulfilling: the site is archived or does not fulfil orders.
	StockSiteNotFulfilling = "site_not_fulfilling"
	// StockVariantNotStocked: the variant is unknown or not goods.
	StockVariantNotStocked = "variant_not_stocked"
	// StockSiteUnknown: no site has the id.
	StockSiteUnknown = "site_unknown"
	// StockLocationUnknown: an issue's from-location is unknown, archived or
	// in another site.
	StockLocationUnknown = "location_unknown"
)

// StockRefusal is the one error a StockLedger write answers for what it will
// not do: Lines name every failing line, and Code, VariantID, LineID and
// Detail repeat the first (a site's refusal has no line). The caller finds it
// with errors.As; anything else is a failure.
type StockRefusal struct {
	Code      string
	VariantID int32
	LineID    int64
	Detail    string
	Lines     []StockLineRefusal
}

// StockLineRefusal is one failing line of a StockRefusal.
type StockLineRefusal struct {
	Code      string
	VariantID int32
	LineID    int64
	Detail    string
}

func (r *StockRefusal) Error() string {
	if r.LineID != 0 {
		return fmt.Sprintf("contracts: stock refused line %d: %s: %s", r.LineID, r.Code, r.Detail)
	}
	return fmt.Sprintf("contracts: stock refused: %s: %s", r.Code, r.Detail)
}
