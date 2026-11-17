package contracts

import (
	"strconv"
	"time"
)

// The events the purchasing module publishes onto the platform outbox
// (purchasing design §7), on the transaction of the change, while it holds
// the purchase order's row lock. A payload carries ids, numbers, dates and
// states (rule 23) — never the supplier's name, an address or a note, which a
// consumer reads from its own copy or not at all. Dates are calendar dates
// (YYYY-MM-DD). Manufacturing handles order.sent, receipt.posted and
// receipt.undone by queuing a replenishment run for the order's site (A4);
// the others are the seam for a supplier timeline and for subcontracting.

// PurchaseOrderSent is purchasing.order.sent.v1: a purchase order was sent —
// by e-mail or marked as sent — and got its number.
type PurchaseOrderSent struct {
	OrderID    int64     `json:"orderId"`
	Number     int64     `json:"number"`
	SupplierID int64     `json:"supplierId"`
	SiteID     int64     `json:"siteId"`
	Currency   string    `json:"currency"`
	NetTotal   string    `json:"netTotal"`
	OrderDate  string    `json:"orderDate"`
	ExpectedOn string    `json:"expectedOn,omitempty"`
	SendMethod string    `json:"sendMethod"` // "mail" | "manual"
	SentAt     time.Time `json:"sentAt"`
}

func (PurchaseOrderSent) EventName() string     { return "purchasing.order.sent" }
func (PurchaseOrderSent) EventVersion() int     { return 1 }
func (e PurchaseOrderSent) AggregateID() string { return strconv.FormatInt(e.OrderID, 10) }

// PurchaseOrderClosed is purchasing.order.closed.v1: a sent order was ended
// with its remainder — by Close, or by its last open line's Close line — and
// what was left open stopped counting as on order.
type PurchaseOrderClosed struct {
	OrderID    int64     `json:"orderId"`
	Number     int64     `json:"number"`
	SupplierID int64     `json:"supplierId"`
	SiteID     int64     `json:"siteId"`
	ClosedAt   time.Time `json:"closedAt"`
}

func (PurchaseOrderClosed) EventName() string     { return "purchasing.order.closed" }
func (PurchaseOrderClosed) EventVersion() int     { return 1 }
func (e PurchaseOrderClosed) AggregateID() string { return strconv.FormatInt(e.OrderID, 10) }

// PurchaseReceiptLine is one line of a receipt as its events carry it: the
// order line, the variant and the quantity in the variant's unit, as exact
// decimal text.
type PurchaseReceiptLine struct {
	OrderLineID int64  `json:"orderLineId"`
	VariantID   int32  `json:"variantId"`
	Quantity    string `json:"quantity"`
}

// PurchaseReceiptPosted is purchasing.receipt.posted.v1: goods were received
// against a purchase order (purchasing design P24–P28) — one receipt, one
// Inventory movement of kind receipt. Its aggregate is the order, whose row
// lock the receipt holds.
type PurchaseReceiptPosted struct {
	ReceiptID   int64                 `json:"receiptId"`
	Number      int64                 `json:"number"`
	OrderID     int64                 `json:"orderId"`
	OrderNumber int64                 `json:"orderNumber"`
	SupplierID  int64                 `json:"supplierId"`
	SiteID      int64                 `json:"siteId"`
	MovementID  int64                 `json:"movementId"`
	ReceivedOn  string                `json:"receivedOn"`
	Lines       []PurchaseReceiptLine `json:"lines"`
	PostedAt    time.Time             `json:"postedAt"`
}

func (PurchaseReceiptPosted) EventName() string     { return "purchasing.receipt.posted" }
func (PurchaseReceiptPosted) EventVersion() int     { return 1 }
func (e PurchaseReceiptPosted) AggregateID() string { return strconv.FormatInt(e.OrderID, 10) }

// PurchaseReceiptUndone is purchasing.receipt.undone.v1: a receipt was taken
// back (P29, A5) — one Inventory movement of kind receipt_reversal — and its
// lines' quantities stopped counting as received. Its aggregate is the order.
type PurchaseReceiptUndone struct {
	ReceiptID          int64                 `json:"receiptId"`
	Number             int64                 `json:"number"`
	OrderID            int64                 `json:"orderId"`
	OrderNumber        int64                 `json:"orderNumber"`
	SupplierID         int64                 `json:"supplierId"`
	SiteID             int64                 `json:"siteId"`
	ReversalMovementID int64                 `json:"reversalMovementId"`
	Lines              []PurchaseReceiptLine `json:"lines"`
	UndoneAt           time.Time             `json:"undoneAt"`
}

func (PurchaseReceiptUndone) EventName() string     { return "purchasing.receipt.undone" }
func (PurchaseReceiptUndone) EventVersion() int     { return 1 }
func (e PurchaseReceiptUndone) AggregateID() string { return strconv.FormatInt(e.OrderID, 10) }
