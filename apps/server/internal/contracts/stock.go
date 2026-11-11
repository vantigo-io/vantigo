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
	// Sites is every site that is not archived, by code: what a caller
	// offers as the site an order is fulfilled from, and checks one against
	// (orders design O46).
	Sites(ctx context.Context) ([]StockSite, error)
	// Locations is, per variant, every active location of the site that
	// holds more than 0 of it, by full code: where a picker takes it from
	// (orders design O47's pick list). A variant held nowhere there has no
	// entry.
	Locations(ctx context.Context, variantIDs []int32, siteID int64) ([]StockLocation, error)
	// SiteLocations is every location of the site, archived ones too, by
	// full code: what a caller offers as the location a document takes stock
	// from or puts it on (manufacturing design M2). An unknown site has none.
	SiteLocations(ctx context.Context, siteID int64) ([]StockSiteLocation, error)
}

// StockSite is a site as Stock answers it: its id, code and name, whether it
// fulfils orders, whether it is active, and its root location — the site's
// own node of the location tree, where stock lies when it has no place.
type StockSite struct {
	ID             int64
	Code, Name     string
	FulfilsOrders  bool
	Active         bool
	RootLocationID int64
}

// StockSiteLocation is one location of a site as Stock answers it: its id,
// its parent (nil for the root), its kind and full code, whether it is
// active (an archived location takes no stock), and whether it is the
// site's root.
type StockSiteLocation struct {
	ID       int64
	ParentID *int64
	Kind     string
	FullCode string
	Active   bool
	Root     bool
}

// StockLocation is what one location of a site holds of a variant: the
// location's id and full code, and its on hand (exact decimal text, more
// than 0).
type StockLocation struct {
	VariantID  int32
	LocationID int64
	FullCode   string
	Quantity   string
	// Primary reports the location as the variant's primary location at its
	// site (inventory phase 2 I55): a suggestion, never a rule — a pick list
	// puts it first, and nothing refuses taking stock elsewhere.
	Primary bool
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
// design O45, manufacturing design M2–M3, module-boundaries rule 24): it
// reserves stock for a document's lines at a site, releases it, issues stock
// against it, posts a production that consumes and produces it, and reads
// what a document holds — every call inside the caller's transaction. It is composed
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
//   - It refuses only through *StockRefusal, naming every failing line.
//     Reserve, ReserveForProduction and Release write nothing when they
//     refuse. After a refusal from Issue or Produce the caller must roll back
//     its transaction (or a savepoint it took before the call): the posting
//     may already have claimed its movement and created balances. Anything
//     else is a failure.
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
	// by that source: a second Issue for one document with the same body —
	// the same lines in the same order, the same quantities as text — answers
	// the first movement and consumes nothing again; one with another body is
	// refused (StockDocumentReused). Stock the location does not hold
	// is refused (StockInsufficientStock); reservations of other documents
	// never stop an issue.
	Issue(ctx context.Context, tx pgx.Tx, src StockSource, issue StockIssue) (movementID int64, err error)
	// ReservationsFor is every reservation the document holds, by line id
	// then site id.
	ReservationsFor(ctx context.Context, tx pgx.Tx, src StockSource) ([]Reservation, error)
	// ReserveForProduction is Reserve without the check that the site
	// fulfils orders (manufacturing design M3): a plant that sells nothing
	// still reserves its components. Every other check stays — the variant is
	// goods, the site exists and is active (an archived one is
	// StockSiteArchived), and its available covers the line — and Release
	// and ReservationsFor treat the reservation as any other.
	ReserveForProduction(ctx context.Context, tx pgx.Tx, src StockSource, siteID int64, lines []StockLine) error
	// Produce posts one movement of kind "production" at the site
	// (manufacturing design M2): each consume line takes its quantity off its
	// from-location, each produce line puts its quantity on its to-location,
	// consume lines first. Consuming a line uses up as much of the line's
	// reservation at the site as it consumed (never below zero), as Issue
	// does. The movement's source is p.Document (src when zero), and posting
	// is idempotent by that source: a second Produce for one document with the
	// same body — the same lines in the same order, the same quantities as
	// text — answers the first movement and consumes nothing again; one with
	// another body is refused (StockDocumentReused). A production posted in
	// parts gives each posting its own Document. At least one line is
	// given; a production of consume lines only is a scrap of components.
	// The movement keeps src's id as its source line id, so the movement
	// names the producing order beside the production record (M5).
	// The site need only exist and be active (StockSiteUnknown,
	// StockSiteArchived), not fulfil orders; every location lies in it. A
	// production of no lines is refused (StockNothingToPost), as is a line's
	// bad quantity (StockQuantityInvalid); a variant that is unknown or not
	// goods (StockVariantNotStocked) — a Discontinued one is still produced;
	// and stock a location does not hold (StockInsufficientStock).
	// Reservations of other documents never stop a production.
	Produce(ctx context.Context, tx pgx.Tx, src StockSource, p StockProduction) (movementID int64, err error)
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

// StockProduction is a movement of stock within a site for a producing
// document: the document the movement records (the production record; the
// reserving source when zero), the site, the components consumed and the
// items produced, and who posted it.
type StockProduction struct {
	Document StockSource
	SiteID   int64
	Consume  []ConsumeLine
	Produce  []ProduceLine
	// PostedByUserID and PostedByDisplay are the poster, as the movement
	// keeps it; both may be empty for a system posting.
	PostedByUserID  *uuid.UUID
	PostedByDisplay string
}

// ConsumeLine is one component a production uses up: the variant, the
// reserving document's line id whose reservation it consumes, the location
// it is taken from, and a quantity more than 0 (exact decimal text, at most
// three decimals).
type ConsumeLine struct {
	VariantID      int32
	LineID         int64
	FromLocationID int64
	Quantity       string
}

// ProduceLine is one item a production puts in stock: the variant, the
// producing document's line id, the location it is put on, and a quantity
// more than 0 (exact decimal text, at most three decimals).
type ProduceLine struct {
	VariantID    int32
	LineID       int64
	ToLocationID int64
	Quantity     string
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
	// StockInsufficientStock: the location does not hold what an issue or a
	// production takes (Inventory's own code, I10).
	StockInsufficientStock = "insufficient_stock"
	// StockSiteNotFulfilling: the site is archived or does not fulfil orders
	// (an order's reserve and issue).
	StockSiteNotFulfilling = "site_not_fulfilling"
	// StockSiteArchived: the site is archived (a production and its reserve,
	// which do not ask that the site fulfil orders).
	StockSiteArchived = "site_archived"
	// StockQuantityInvalid: a line's quantity is not more than 0, has more
	// than three decimals, or would take a balance beyond
	// 99,999,999,999.999 (a production's produce line).
	StockQuantityInvalid = "quantity_invalid"
	// StockNothingToPost: a production names no line.
	StockNothingToPost = "nothing_to_post"
	// StockVariantNotStocked: the variant is unknown or not goods.
	StockVariantNotStocked = "variant_not_stocked"
	// StockSiteUnknown: no site has the id.
	StockSiteUnknown = "site_unknown"
	// StockLocationUnknown: an issue's or a production's location is
	// unknown, archived or in another site.
	StockLocationUnknown = "location_unknown"
	// StockDocumentReused: an issue or a production names a document already
	// posted with another body (other lines, order or quantities); the first
	// posting stands.
	StockDocumentReused = "document_reused"
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
