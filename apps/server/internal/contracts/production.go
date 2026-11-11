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
//   - It refuses more than MaxProductionLines ids as a failure.
type Production interface {
	// ForOrderLines is the manufacturing orders made for the lines.
	ForOrderLines(ctx context.Context, lineIDs []int64) ([]LineProduction, error)
	// HasBOM is, for each variant asked, whether it has a default active bill
	// of materials; a variant with none is absent or false.
	HasBOM(ctx context.Context, variantIDs []int32) (map[int32]bool, error)
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
