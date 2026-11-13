package contracts

import (
	"context"
	"time"
)

// MaxProductionLines is the most order lines one SalesOrderLines.Lines or
// Production.ForOrderLines read names, and the most variants one
// Production.HasBOM read names.
const MaxProductionLines = 500

// SalesOrderLines is Orders' read of its sales orders' goods lines for
// Manufacturing (manufacturing design M43, M43a): what a make-to-order line
// asks to be made. It is a single-provider slot, nil when Orders is off.
//
// It is consumer-gated (module-boundaries rule 12): the provider performs no
// authorization, because the rule that decides who may see a line is the
// consumer's — Manufacturing's worker calls it with no principal, and its
// new-order form only for a caller holding manufacturing:orders-manage. The
// rules a provider keeps:
//
//   - It reads on the pool, in one snapshot per call, never under a lock of
//     the caller's; the caller never calls it inside a transaction.
//   - It answers the product lines naming a variant of sales orders (never a
//     quote's, a template's or a subscription's), in the order's line order;
//     an id that is not such a line is absent, never an error.
//   - It carries no personal data: the customer's id, never their name.
//   - It refuses more than MaxProductionLines ids as a failure.
type SalesOrderLines interface {
	// Lines is the lines named by lineIDs.
	Lines(ctx context.Context, lineIDs []int64) ([]SalesOrderLine, error)
	// OrderLines is every such line of one sales order; none when the order
	// does not exist.
	OrderLines(ctx context.Context, orderID int64) ([]SalesOrderLine, error)
	// Unreserved is what open sales order lines of the variants, fulfilled
	// from the site, still need that they do not hold reserved
	// (manufacturing phase 2 M107a): per line of a confirmed sales order, the
	// open quantity less what it holds reserved, > 0 only, by order then
	// line. It names at most MaxStockPlacements variants.
	Unreserved(ctx context.Context, variantIDs []int32, siteID int64) ([]UnreservedDemand, error)
}

// UnreservedDemand is one sales order line's unreserved open quantity, as
// SalesOrderLines.Unreserved answers it: exact decimal text, more than 0.
type UnreservedDemand struct {
	OrderID, LineID int64
	VariantID       int32
	Quantity        string
}

// SalesOrderLine is one goods line of a sales order as SalesOrderLines
// answers it. Quantity is the open quantity — ordered less cancelled less
// delivered, at least 0 — as exact decimal text with no trailing zeros.
// DeliveryDate is the order's delivery date, SiteID the site it is fulfilled
// from, either nil when it names none. OrderStatus is the order's status
// (draft, confirmed, done, cancelled).
type SalesOrderLine struct {
	LineID       int64
	OrderID      int64
	OrderNumber  int64
	CustomerID   int32
	VariantID    int32
	Quantity     string
	DeliveryDate *time.Time
	SiteID       *int64
	OrderStatus  string
}

// Production is Manufacturing's read of what is being made for sales order
// lines, and of which variants it can make (manufacturing design M45, M46,
// M46a). It is a single-provider slot, nil when Manufacturing is off.
//
// It is provider-gated (module-boundaries rule 12): Manufacturing checks
// manufacturing:access against the principal in ctx and answers nothing, not
// an error, to a caller without it and to a context with no request. Orders,
// its consumer, calls it with the request's context, on the pool and never
// inside a transaction, and checks nothing itself. The rules a provider keeps:
//
//   - It reads on the pool, in one snapshot per call.
//   - ForOrderLines answers every manufacturing order naming one of the lines
//     as its sales-order origin, backorders included (M44b), cancelled ones
//     left out, by line then number.
//   - NotMade answers why make-to-order made no order for a line of one
//     sales order (M44a): the lines its request left out, and the whole
//     order when the request failed for good. It leaves out the lines that
//     were never Manufacturing's to make — no bill of materials, or one not
//     made to order — and those that already have an order; a request still
//     pending, finished with nothing left out, or cancelled answers none.
//   - It refuses more than MaxProductionLines ids as a failure.
type Production interface {
	// ForOrderLines is the manufacturing orders made for the lines.
	ForOrderLines(ctx context.Context, lineIDs []int64) ([]LineProduction, error)
	// HasBOM is, for each variant asked, whether it has a default active bill
	// of materials; a variant with none is absent or false.
	HasBOM(ctx context.Context, variantIDs []int32) (map[int32]bool, error)
	// NotMade is why make-to-order made no manufacturing order for lines of
	// the sales order salesOrderID.
	NotMade(ctx context.Context, salesOrderID int64) ([]LineNotMade, error)
}

// NotMadeFailed is LineNotMade's Code when the sales order's request failed
// for good, after its last attempt: no line of it was judged.
const NotMadeFailed = "failed"

// LineNotMade is why make-to-order made no manufacturing order for a sales
// order line. LineID is 0 when it is the whole sales order's — Code is then
// NotMadeFailed. Code is otherwise the reason the line was left out
// (no_site, nothing_open, variant_not_goods, or the code of the rule the
// order's creation or confirmation refused), and Detail says it in English.
type LineNotMade struct {
	LineID int64
	Code   string
	Detail string
}

// LineProduction is one manufacturing order made for a sales order line.
// OrderID is the manufacturing order's id and Number its display number
// (MO-12, MO-12-2); Quantity and Produced are exact decimal text; SiteID is
// the site it is made at, which a reservation for the sales order needs to
// be the order's own (M48a).
type LineProduction struct {
	LineID        int64
	OrderID       int64
	Number        string
	Status        string
	Quantity      string
	Produced      string
	SiteID        int64
	PlannedFinish *time.Time
	DueDate       *time.Time
}
