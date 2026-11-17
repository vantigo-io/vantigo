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
// (YYYY-MM-DD). None has a consumer in this phase: they are the seam for a
// supplier timeline and for Manufacturing's subcontracting.

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
