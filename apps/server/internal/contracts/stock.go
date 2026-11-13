package contracts

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

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
//     corrections may take on hand below what is reserved, and a hold put on
//     reserved stock lowers it (manufacturing phase 2 M63).
//   - Available is judged on usable on hand (M63): the balances with no lot,
//     plus those of lots with no active hold and not expired today in the
//     workspace's zone. OnHand is everything that lies there.
//   - It refuses more than MaxStockVariants variants, or a variant named
//     twice (ValidateStockVariants), as a failure.
type Stock interface {
	// OnHand is each variant's on hand: with siteID nil, across every site
	// and per site holding it; with siteID set, at that site only.
	OnHand(ctx context.Context, variantIDs []int32, siteID *int64) ([]StockQuantity, error)
	// Available is each variant's usable on hand less what is reserved, the
	// same way: Sites lists every site where the variant lies or is reserved.
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
	// Lots is, per variant, every lot holding more than 0 at an active location of
	// the site, with its location, in FEFO order (manufacturing phase 2 M65). A
	// variant with none has no row.
	Lots(ctx context.Context, variantIDs []int32, siteID int64) ([]StockLot, error)
	// LotRefs is the lots named, by id, whatever they hold: what a caller
	// that keeps a lot's id reads of it before its transaction — its
	// variant, its number and its derived status (manufacturing phase 2 M99,
	// a deviation opened from a lot). An id that names no lot has no entry.
	// It refuses more than MaxStockVariants ids, as a failure.
	LotRefs(ctx context.Context, lotIDs []int64) ([]LotRef, error)
	// Placements is every placement at the site with a reorder point, with
	// what the low-stock rule reads (inventory phase 2 I56): usable on hand
	// (manufacturing phase 2 M63: held and expired lots left out), reserved
	// and incoming by transfer. Manufacturing adds its own orders and
	// Orders' unreserved demand to it (manufacturing phase 2 M106, M107). It
	// answers them by variant id, in one snapshot, none for a site that is
	// unknown or not active; more than MaxStockPlacements is a failure.
	Placements(ctx context.Context, siteID int64) ([]StockPlacement, error)
}

// MaxStockPlacements is the most placements one Stock.Placements read
// answers (manufacturing phase 2 M106).
const MaxStockPlacements = 5000

// StockPlacement is one placement as Stock.Placements answers it: the
// rule's figures as exact decimal text with no trailing zeros. Maximum and
// Multiple are "" when unset; OnHand is usable on hand at the site.
type StockPlacement struct {
	VariantID                  int32
	ReorderPoint               string
	Maximum, Multiple          string
	OnHand, Reserved, Incoming string
}

// LotRef is one lot as Stock.LotRefs answers it.
type LotRef struct {
	ID        int64
	VariantID int32
	Number    string
	Status    string // "available" | "quarantine" | "blocked"
}

// StockLot is one lot of a variant on one location of a site, as Stock.Lots
// answers it (manufacturing phase 2 M67).
type StockLot struct {
	VariantID  int32
	LotID      int64
	Number     string
	Serial     bool
	Expiry     *time.Time // a calendar date at midnight UTC, nil when none
	BestBefore *time.Time // the same
	ReceivedAt time.Time
	LocationID int64
	FullCode   string
	Quantity   string // exact decimal text, > 0
	Status     string // "available" | "quarantine" | "blocked", derived (M64)
}

// The statuses of a lot (manufacturing phase 2 M64), derived from its active
// holds: blocked while any is a block, quarantine while any is a quarantine.
const (
	LotAvailable  = "available"
	LotQuarantine = "quarantine"
	LotBlocked    = "blocked"
)

// LotPick is one lot a FEFO proposal takes from (manufacturing phase 2 M65).
type LotPick struct {
	LotID      int64
	LocationID int64
	Quantity   string
}

// ProposeLots is the FEFO proposal (manufacturing phase 2 M65): it takes
// quantity from rows in their order, skipping rows not available or expired on
// today, and answers the picks and what it could not cover. rows are one
// variant's at one site, as Stock.Lots answers them. The order is expiry
// (none last), then received, then lot id: FIFO when no lot expires. A serial
// row is taken whole or not at all, and "" short means all was covered.
func ProposeLots(rows []StockLot, quantity string, today time.Time) (picks []LotPick, short string, err error) {
	left, ok := new(big.Rat).SetString(quantity)
	if !ok || left.Sign() <= 0 {
		return nil, "", fmt.Errorf("contracts: propose lots for the quantity %q", quantity)
	}
	ordered := slices.Clone(rows)
	slices.SortStableFunc(ordered, func(a, b StockLot) int {
		switch {
		case a.Expiry == nil && b.Expiry != nil:
			return 1
		case a.Expiry != nil && b.Expiry == nil:
			return -1
		case a.Expiry != nil && b.Expiry != nil:
			if c := a.Expiry.Compare(*b.Expiry); c != 0 {
				return c
			}
		}
		return cmp.Or(a.ReceivedAt.Compare(b.ReceivedAt), cmp.Compare(a.LotID, b.LotID), cmp.Compare(a.LocationID, b.LocationID))
	})
	for _, r := range ordered {
		if left.Sign() == 0 {
			break
		}
		if r.Status != LotAvailable || LotExpired(r.Expiry, today) {
			continue
		}
		q, ok := new(big.Rat).SetString(r.Quantity)
		if !ok || q.Sign() <= 0 {
			return nil, "", fmt.Errorf("contracts: lot %d has the quantity %q", r.LotID, r.Quantity)
		}
		if r.Serial && q.Cmp(left) > 0 {
			continue
		}
		take := q
		if q.Cmp(left) > 0 {
			take = new(big.Rat).Set(left)
		}
		left.Sub(left, take)
		picks = append(picks, LotPick{LotID: r.LotID, LocationID: r.LocationID, Quantity: decimalString(take)})
	}
	if left.Sign() > 0 {
		short = decimalString(left)
	}
	return picks, short, nil
}

// LotExpired reports whether a lot of this expiry is expired on today (both
// dates): its expiry lies before today (manufacturing phase 2 M62). A lot
// with no expiry never expires. Each date is its own year, month and day as
// given, in whatever zone it carries — never moved to UTC first, so a today
// built in the workspace zone is the day it names.
func LotExpired(expiry *time.Time, today time.Time) bool {
	if expiry == nil {
		return false
	}
	e, t := *expiry, today
	return time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, time.UTC).Before(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC))
}

// decimalString is r as the shortest exact decimal text with at most three
// decimals: "12", "2.5".
func decimalString(r *big.Rat) string {
	s := r.FloatString(3)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
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
//     ProductCatalog.LockVariants, on tx. What it needs from outside
//     Inventory — today's date in the workspace zone, whether the principal
//     may use an expired lot — the caller reads on the pool before its
//     transaction and passes as data (manufacturing phase 2 M62).
//   - The lock order is the caller's: ProductCatalog.LockVariants first, then
//     the caller's own rows, then — inside these calls — Inventory's: the lot
//     counter (only to generate a number), new lots in (variant, number)
//     order, every lot named in id order (FOR SHARE, FOR NO KEY UPDATE for a
//     serial lot increased, or for a hold), the movement's header, the
//     locations FOR SHARE, the balances in (variant, location, lot) order
//     with no lot first, and the reservations (manufacturing phase 2 M58).
//     A caller never locks Inventory's rows another way and never calls the
//     ledger before it has locked the variants; EnsureLots runs before the
//     Produce that uses its lots.
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
	// active and fulfils orders, and that the site's available — usable on
	// hand summed over its locations (no held or expired lot, judged on
	// today, a date in the workspace zone the caller read on the pool), less
	// every reservation there — covers it.
	Reserve(ctx context.Context, tx pgx.Tx, src StockSource, siteID int64, today time.Time, lines []StockLine) error
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
	// never stop an issue. A line of a tracked variant names its lot, or
	// AutoLot lets Inventory take the lots FEFO at its location under its own
	// locks; a lot on hold is refused (StockLotBlocked), an expired one too
	// unless the line AllowExpired and the call's ExpiredOverride
	// (StockLotExpired). It answers the movement and the lots AutoLot took.
	Issue(ctx context.Context, tx pgx.Tx, src StockSource, issue StockIssue) (StockPosted, error)
	// ReservationsFor is every reservation the document holds, by line id
	// then site id. It takes no lock, so a caller may read it in a read-only
	// transaction (Orders' SalesOrderLines.Unreserved does).
	ReservationsFor(ctx context.Context, tx pgx.Tx, src StockSource) ([]Reservation, error)
	// ReservationsForSources is ReservationsFor of many documents in one
	// read (manufacturing phase 2 M107a): every reservation each source holds,
	// by source id, line id then site id, a source named twice answered once.
	// It takes no lock either. More than MaxStockSources sources is a failure.
	ReservationsForSources(ctx context.Context, tx pgx.Tx, srcs []StockSource) ([]SourceReservation, error)
	// ReserveForProduction is Reserve without the check that the site
	// fulfils orders (manufacturing design M3): a plant that sells nothing
	// still reserves its components. Every other check stays — the variant is
	// goods, the site exists and is active (an archived one is
	// StockSiteArchived), and its available covers the line — and Release
	// and ReservationsFor treat the reservation as any other.
	ReserveForProduction(ctx context.Context, tx pgx.Tx, src StockSource, siteID int64, today time.Time, lines []StockLine) error
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
	// Reservations of other documents never stop a production. Consume
	// lines keep Issue's lot rules (an unbuild's may take held and expired
	// lots); a produce line of a tracked variant names a lot EnsureLots made.
	Produce(ctx context.Context, tx pgx.Tx, src StockSource, p StockProduction) (StockPosted, error)
	// EnsureLots finds or creates each lot, inside tx, after the caller's rows and
	// before Produce, and answers the lot ids in the order given. A lot that exists
	// for the variant is answered as is: only the dates the NewLot states are
	// compared (StockLotDatesDiffer); nil dates are ignored for it. A NewLot that
	// MustCreate is refused StockLotExists instead when its number is taken —
	// by an existing lot, or by an earlier NewLot of the call. A new serial
	// whose number holds stock is StockSerialQuantity. Each refusal's Index is
	// the NewLot's. After a refusal the caller rolls back, as after Produce (or
	// to a savepoint it took before the call).
	EnsureLots(ctx context.Context, tx pgx.Tx, src StockSource, today time.Time, lots []NewLot) ([]int64, error)
	// HoldLots puts one hold per entry for src's document (Q's deviation), inside
	// tx, taking the lots FOR NO KEY UPDATE in id order. A lot that already holds
	// an active hold of src with the same kind is left as is.
	HoldLots(ctx context.Context, tx pgx.Tx, src StockSource, holds []LotHold) error
	// ReleaseLotHolds releases every active hold src's document put on the lots
	// named (on every lot when lotIDs is nil). Holds of any other source stay.
	ReleaseLotHolds(ctx context.Context, tx pgx.Tx, src StockSource, lotIDs []int64, by LotActor) error
}

// StockPosted is what Issue and Produce posted (manufacturing phase 2 M67):
// the movement, and the lots each AutoLot line took.
type StockPosted struct {
	MovementID int64
	Picks      []StockPick // one per lot an AutoLot line took, by line index
}

// StockPick is one lot an AutoLot line took: the line's index in Lines or
// Consume, the lot, its location and the quantity.
type StockPick struct {
	Index      int // the line's index in Lines or Consume
	LotID      int64
	LocationID int64
	Quantity   string
}

// NewLot is a lot EnsureLots finds by number or creates (manufacturing phase
// 2 M57, M80). Its dates are calendar dates: their year, month and day are
// read as given, by convention at midnight UTC.
type NewLot struct {
	VariantID      int32
	Number         string // "" to generate L<n>
	SupplierLot    string
	ProductionDate time.Time  // required: today for a production, unless the person gave one
	BestBefore     *time.Time // nil: production date + BestBeforeDays, on creation only
	Expiry         *time.Time // nil: production date + ExpiryDays, on creation only
	// MustCreate refuses the lot StockLotExists rather than answering an
	// existing one: a production's counted lot number or serial, which names
	// a new lot or none.
	MustCreate bool
}

// LotHold is one hold HoldLots puts (manufacturing phase 2 M64).
type LotHold struct {
	LotID       int64
	Kind, Cause string // "quarantine" | "block"; M64's causes
	Note        string
	By          LotActor
}

// LotActor is who puts or releases a hold, as the hold keeps it; both may be
// empty for a system change.
type LotActor struct {
	UserID  *uuid.UUID
	Display string
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
	// Today is the workspace zone's date, read by the caller on the pool
	// before its transaction (manufacturing phase 2 M62); zero is a
	// programming error. By convention it is that date at midnight UTC; its
	// year, month and day are read as given, in whatever zone it carries.
	Today time.Time
	// ExpiredOverride is whether the principal holds
	// inventory:expired-override, read by the caller on the pool; a worker
	// with no request passes false.
	ExpiredOverride bool
}

// IssueLine is one line of an issue: the variant, the reserving document's
// line id whose reservation it consumes, the location it is taken from, and
// a quantity more than 0 (exact decimal text, at most three decimals).
type IssueLine struct {
	VariantID      int32
	LineID         int64
	FromLocationID int64
	Quantity       string
	LotID          int64 // 0 for none; required for a tracked variant unless AutoLot
	AutoLot        bool  // M65a: Inventory picks the lots FEFO at FromLocationID under its locks
	AllowExpired   bool  // M62: honoured only when the call's ExpiredOverride is true
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
	// Today and ExpiredOverride are StockIssue's (manufacturing phase 2 M62).
	Today           time.Time
	ExpiredOverride bool
	// Unbuild is a movement of kind "unbuild" (M123), whose consume lines may
	// take held and expired lots.
	Unbuild bool
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
	LotID          int64 // 0 for none; required for a tracked variant unless AutoLot
	AutoLot        bool  // M65a: Inventory picks the lots FEFO at FromLocationID under its locks
	AllowExpired   bool  // M62: honoured only when the call's ExpiredOverride is true
}

// ProduceLine is one item a production puts in stock: the variant, the
// producing document's line id, the location it is put on, and a quantity
// more than 0 (exact decimal text, at most three decimals). A production's
// produce lines are keyed by (VariantID, LineID): a by-product's LineID is its
// order_outputs id, which may equal a component's (M121).
type ProduceLine struct {
	VariantID    int32
	LineID       int64
	ToLocationID int64
	Quantity     string
	LotID        int64 // 0 for none; a lot made by EnsureLots, required for a tracked variant
}

// Reservation is what one line of a document holds at one site.
type Reservation struct {
	VariantID int32
	LineID    int64
	SiteID    int64
	Quantity  string
}

// SourceReservation is one reservation as ReservationsForSources answers
// it: the document that holds it beside the reservation.
type SourceReservation struct {
	Source StockSource
	Reservation
}

// MaxStockSources is the most documents one ReservationsForSources read
// names.
const MaxStockSources = 1000

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
	// The lot rules (manufacturing phase 2 M56, M57).
	StockLotRequired    = "lot_required"
	StockLotUnknown     = "lot_unknown"
	StockLotNotAllowed  = "lot_not_allowed"
	StockSerialQuantity = "serial_quantity"
	StockLotExpired     = "lot_expired"
	StockLotBlocked     = "lot_blocked"
	StockLotDatesDiffer = "lot_dates_differ"
	// StockLotExists: an EnsureLots NewLot that MustCreate names a number the
	// variant already has (manufacturing phase 2 M80, M81).
	StockLotExists = "lot_exists"
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

// StockLineRefusal is one failing line of a StockRefusal. Index is the
// line's index in the call's lines (Issue's Lines; Produce's Consume, then
// Produce after them; EnsureLots' lots; Reserve's lines), and LotID and
// LocationID the lot and location it failed at, 0 when none — so a caller maps
// a refusal to the exact row when one document line became several ledger
// lines (manufacturing phase 2 M67).
type StockLineRefusal struct {
	Code       string
	VariantID  int32
	LineID     int64
	Detail     string
	Index      int
	LotID      int64
	LocationID int64
}

func (r *StockRefusal) Error() string {
	if r.LineID != 0 {
		return fmt.Sprintf("contracts: stock refused line %d: %s: %s", r.LineID, r.Code, r.Detail)
	}
	return fmt.Sprintf("contracts: stock refused: %s: %s", r.Code, r.Detail)
}
