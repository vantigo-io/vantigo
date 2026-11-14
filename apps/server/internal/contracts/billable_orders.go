package contracts

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// WorkSourceOrderAllocation is an order line's invoice allocation as an
// invoice's source (orders design O36, O38): the part of an order line one
// invoice line bills, stamped by Orders' InvoicedWorkHolder. An order line
// may be invoiced in several parts, and an invoice line holds one live source
// per source, so the source is the allocation, never the line.
const WorkSourceOrderAllocation WorkSourceKind = "orders.allocation"

// An allocation's kind: a part of an order line (B), or a subscription's
// billing period (S), which carries its period.
const (
	AllocationOrderLine          = "order_line"
	AllocationSubscriptionPeriod = "subscription_period"
)

// An allocation's state: a draft holds its quantity from being offered
// again; an invoiced one carries its invoice's stamp; a released one gave its
// quantity back — its draft deleted, swept, released by hand, or its invoice
// line credited in full.
const (
	AllocationDraft    = "draft"
	AllocationInvoiced = "invoiced"
	AllocationReleased = "released"
)

// The documents a DueOrder is (orders design O37, O43): a sales order, whose
// lines are due by quantity, or a subscription, whose lines are due per
// billing period.
const (
	DueKindOrder        = "order"
	DueKindSubscription = "subscription"
)

// Why a due order cannot be invoiced now (DueOrder.Blocked): it is still a
// draft, or it is on hold.
const (
	DueOrderBlockedDraft  = "draft"
	DueOrderBlockedOnHold = "on_hold"
)

// The codes an AllocationRefusal carries: a quantity above what the line has
// left to invoice, or a line that cannot be invoiced at all (not an order's
// product line, its order a draft, on hold or cancelled); a subscription
// that bills nothing now — not active, paused or ended, or gone — or one
// with no period due through the date asked.
const (
	AllocationExceedsOpen                = "allocation_exceeds_open"
	AllocationLineNotInvoiceable         = "line_not_invoiceable"
	AllocationSubscriptionNotInvoiceable = "subscription_not_invoiceable"
	AllocationNothingDue                 = "nothing_due"
)

// BillableOrders is the Orders module's read for invoicing (orders design
// O37): what can be invoiced of its orders now, and the allocations an
// invoice line is built from. It is a single-provider slot, resolved after
// the consumption provider, optional for its consumer (nil when Orders is
// off). The rules a provider keeps — ConsumptionProvider's:
//
//   - It reads and writes on the pool, never under a lock of the caller's,
//     and the caller never calls it inside a transaction that holds one.
//   - It never calls a directory while serving: the customer's facts are the
//     caller's to read.
//   - It refuses a request Validate refuses, answers at most MaxBillableRows
//     rows and sets More when there were more.
//   - Read by ids it answers only allocations that exist; a missing id is
//     absent, never an error.
//   - It fails rather than under-reports: an error means "the orders could
//     not be read", never "there are none".
//   - Allocate and ReleaseAllocations are provider commands (module-boundaries
//     rule 5), the second and third after ConsumptionProvider.Settle: each
//     writes only the provider's own schema, on the provider's own
//     transaction.
//   - A blocked order is still answered, with Blocked set, so the caller can
//     say why an order is not invoiced instead of skipping it silently.
type BillableOrders interface {
	// DueOrders lists, per order, the lines with a quantity to invoice now —
	// ordered − cancelled − invoiced − draft-allocated for a line invoiced on
	// order — and, per subscription, each due period's lines (Kind
	// DueKindSubscription), without allocating anything.
	DueOrders(ctx context.Context, req DueOrdersRequest) (DueOrdersPage, error)
	// Allocate writes a new draft allocation for each line named, under the
	// lines' lock, and answers them in the request's order; then, for each
	// subscription named, one draft allocation per period due through its
	// date and per line in force on the period's first day, under the
	// subscription's lock, in subscription, period and line order. It never
	// rewrites an existing draft. A quantity above what the line has left to
	// invoice, a line or a subscription that cannot be invoiced, or a
	// subscription with no period due, is refused as *AllocationRefusal, and
	// nothing is written.
	Allocate(ctx context.Context, req AllocateRequest) ([]OrderAllocation, error)
	// Allocations answers allocations by id as they stand now. At most
	// MaxBillableRows ids (ValidateAllocationIDs).
	Allocations(ctx context.Context, ids []int64) ([]OrderAllocation, error)
	// ReleaseAllocations turns the draft allocations named released; one
	// already invoiced or released is left as it is. The invoicing module
	// calls it after a draft's delete, or a line's removal from a draft,
	// commits — best effort, logged on failure.
	ReleaseAllocations(ctx context.Context, ids []int64) error
}

// DueOrdersRequest names the documents whose due lines are listed:
// customers (every order and subscription of theirs), documents — orders
// and subscriptions by id — or, with Every, every order and subscription,
// the billing run's read; exactly one of the three. Until bounds the orders
// by their delivery date, on or before — an order without one is always
// due, and zero is no bound — and is the date a subscription's periods are
// due through (orders design O43): the provider's today when zero.
type DueOrdersRequest struct {
	CustomerIDs     []int32
	OrderIDs        []int64
	SubscriptionIDs []int64
	Every           bool
	Until           time.Time
}

// Validate is the request's shape: exactly one of CustomerIDs (at most
// MaxActualsRequests), the documents — OrderIDs and SubscriptionIDs, at most
// MaxBillableRows together — and Every, none named twice.
func (r DueOrdersRequest) Validate() error {
	named := 0
	for _, ok := range []bool{len(r.CustomerIDs) > 0, len(r.OrderIDs)+len(r.SubscriptionIDs) > 0, r.Every} {
		if ok {
			named++
		}
	}
	switch {
	case named == 0:
		return errors.New("contracts: a due-orders request names no customer, no order or subscription and not every order")
	case named > 1:
		return errors.New("contracts: a due-orders request names more than one of customers, documents and every order")
	case len(r.CustomerIDs) > MaxActualsRequests:
		return fmt.Errorf("contracts: a due-orders request names %d customers, more than %d", len(r.CustomerIDs), MaxActualsRequests)
	case len(r.OrderIDs)+len(r.SubscriptionIDs) > MaxBillableRows:
		return fmt.Errorf("contracts: a due-orders request names %d documents, more than %d", len(r.OrderIDs)+len(r.SubscriptionIDs), MaxBillableRows)
	}
	if id, ok := firstDuplicate(r.CustomerIDs); ok {
		return fmt.Errorf("contracts: a due-orders request names customer %d twice", id)
	}
	if id, ok := firstDuplicate(r.OrderIDs); ok {
		return fmt.Errorf("contracts: a due-orders request names order %d twice", id)
	}
	if id, ok := firstDuplicate(r.SubscriptionIDs); ok {
		return fmt.Errorf("contracts: a due-orders request names subscription %d twice", id)
	}
	return nil
}

// DueOrder is one order or subscription with lines to invoice, as Orders'
// snapshot holds it. Kind is DueKindOrder or DueKindSubscription; ID and
// Number are the order's or the subscription's — the two are numbered apart,
// so a caller keys a document by its kind and id together.
type DueOrder struct {
	Kind          string
	ID, Number    int64
	CustomerID    int32
	Currency      string
	ProjectID     *int32
	YourReference string
	DeliveryDate  *time.Time
	// DeliveryAddress is the document's delivery address, its Label the
	// delivery name; nil when it has none. A subscription has no delivery
	// date.
	DeliveryAddress  *CustomerAddressEntry
	PaymentTermsDays *int32
	// Blocked is "" or why the order cannot be invoiced now
	// (DueOrderBlockedDraft, DueOrderBlockedOnHold); a subscription is
	// listed only with periods it can bill.
	Blocked string
	Lines   []DueOrderLine
}

// DueOrderLine is one order line with a quantity to invoice, or one
// subscription line in one due period — a subscription line is listed once
// per period it is due in, so LineID may repeat, with PeriodStart and
// PeriodEnd telling them apart. Quantities and money are exact decimal text.
type DueOrderLine struct {
	LineID            int64
	Position          int32
	Description, Unit string
	// ToInvoice is what is left to invoice (three decimals).
	ToInvoice string
	// UnitPrice has eight decimals, DiscountPercent two.
	UnitPrice, DiscountPercent string
	VATCodeID                  int32
	// InvoicePolicy is "order" or "delivery" (C).
	InvoicePolicy string
	// PeriodStart and PeriodEnd are a subscription period's first and last
	// day, nil for an order line. A partial first period is pro-rated: its
	// UnitPrice is the pro-rated amount ÷ ToInvoice at eight decimals and
	// its DiscountPercent 0 (orders design O37, O43).
	PeriodStart, PeriodEnd *time.Time
}

// DueOrdersPage is DueOrders' answer; More says there were more than
// MaxBillableRows lines and the rest is not here.
type DueOrdersPage struct {
	Orders []DueOrder
	More   bool
}

// AllocateRequest names the order lines to allocate and how much of each,
// and the subscriptions whose due periods to allocate.
type AllocateRequest struct {
	Lines         []AllocateLine
	Subscriptions []AllocateSubscription
}

// AllocateSubscription is one subscription to allocate: every period due
// through Through (orders design O43) — its first day on or before it when
// billed in advance, its last when in arrears — and none starting more than
// twelve months after the provider's today.
type AllocateSubscription struct {
	SubscriptionID int64
	Through        time.Time
}

// AllocateLine is one line to allocate: Quantity is a positive decimal with
// at most three decimals, as text.
type AllocateLine struct {
	LineID   int64
	Quantity string
}

// maxAllocationQuantity is numeric(12,3)'s bound.
var maxAllocationQuantity = big.NewRat(999999999999, 1000)

// Validate is the request's shape: 1 to MaxBillableRows lines and
// subscriptions together, none named twice, each quantity a positive decimal
// of at most three decimals within numeric(12,3), each subscription with a
// date to bill through.
func (r AllocateRequest) Validate() error {
	switch {
	case len(r.Lines)+len(r.Subscriptions) == 0:
		return errors.New("contracts: an allocate request names no line and no subscription")
	case len(r.Lines)+len(r.Subscriptions) > MaxBillableRows:
		return fmt.Errorf("contracts: an allocate request names %d lines and subscriptions, more than %d", len(r.Lines)+len(r.Subscriptions), MaxBillableRows)
	}
	subscriptions := make([]int64, len(r.Subscriptions))
	for i, sub := range r.Subscriptions {
		subscriptions[i] = sub.SubscriptionID
		if sub.Through.IsZero() {
			return fmt.Errorf("contracts: an allocate request's subscription %d has no date to bill through", sub.SubscriptionID)
		}
	}
	if id, ok := firstDuplicate(subscriptions); ok {
		return fmt.Errorf("contracts: an allocate request names subscription %d twice", id)
	}
	ids := make([]int64, len(r.Lines))
	for i, l := range r.Lines {
		ids[i] = l.LineID
		q, ok := new(big.Rat).SetString(l.Quantity)
		if !ok || strings.ContainsAny(l.Quantity, "eE/") {
			return fmt.Errorf("contracts: an allocate request's line %d has quantity %q, not a decimal", l.LineID, l.Quantity)
		}
		if q.Sign() <= 0 || q.Cmp(maxAllocationQuantity) > 0 {
			return fmt.Errorf("contracts: an allocate request's line %d has quantity %s, not within (0, %s]", l.LineID, l.Quantity, maxAllocationQuantity.FloatString(3))
		}
		if scaled := new(big.Rat).Mul(q, big.NewRat(1000, 1)); !scaled.IsInt() {
			return fmt.Errorf("contracts: an allocate request's line %d has quantity %s, more than three decimals", l.LineID, l.Quantity)
		}
	}
	if id, ok := firstDuplicate(ids); ok {
		return fmt.Errorf("contracts: an allocate request names line %d twice", id)
	}
	return nil
}

// ValidateAllocationIDs is Allocations' and ReleaseAllocations' request
// shape: 1 to MaxBillableRows ids, none twice.
func ValidateAllocationIDs(ids []int64) error {
	switch {
	case len(ids) == 0:
		return errors.New("contracts: an allocations request names no id")
	case len(ids) > MaxBillableRows:
		return fmt.Errorf("contracts: an allocations request names %d ids, more than %d", len(ids), MaxBillableRows)
	}
	if id, ok := firstDuplicate(ids); ok {
		return fmt.Errorf("contracts: an allocations request names id %d twice", id)
	}
	return nil
}

// OrderAllocation is the part of an order line one invoice line bills, or one
// subscription line's period. Amount is docmath's line net for Quantity,
// UnitPrice and DiscountPercent, so an invoice line built from the same three
// numbers has the same net. Money and quantities are exact decimal text.
type OrderAllocation struct {
	ID       int64
	Revision int32
	// Kind is AllocationOrderLine or AllocationSubscriptionPeriod. OrderID
	// and OrderNumber are the order's, or for a subscription period the
	// subscription's.
	Kind        string
	OrderID     int64
	OrderNumber int64
	LineID      int64
	// Quantity has three decimals, UnitPrice eight, DiscountPercent and
	// Amount two.
	Quantity, UnitPrice, DiscountPercent, Amount string
	VATCodeID                                    int32
	Description, Unit                            string
	// VariantID is the order line's product variant, nil for a line of free
	// text: Invoices reads its tax category through the product catalog, so
	// an issued line remembers it (accounting design R6).
	VariantID     *int32
	Currency      string
	ProjectID     *int32
	YourReference string
	// State is AllocationDraft, AllocationInvoiced or AllocationReleased.
	State string
	// PeriodStart and PeriodEnd are a subscription period's first and last
	// day, nil for an order line's allocation.
	PeriodStart, PeriodEnd *time.Time
}

// AllocationRefusal is the one error Allocate answers for a line or a
// subscription it will not allocate, Code one of AllocationExceedsOpen and
// AllocationLineNotInvoiceable for a line (LineID set), and
// AllocationSubscriptionNotInvoiceable and AllocationNothingDue for a
// subscription (SubscriptionID set); the caller finds it with errors.As.
// Anything else Allocate returns is a failure.
type AllocationRefusal struct {
	LineID         int64
	SubscriptionID int64
	Code           string
	Detail         string
}

func (r *AllocationRefusal) Error() string {
	if r.SubscriptionID != 0 {
		return fmt.Sprintf("contracts: subscription %d refused: %s: %s", r.SubscriptionID, r.Code, r.Detail)
	}
	return fmt.Sprintf("contracts: order line %d refused: %s: %s", r.LineID, r.Code, r.Detail)
}
