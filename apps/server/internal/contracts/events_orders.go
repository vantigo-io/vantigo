package contracts

import (
	"strconv"
	"time"
)

// The events the orders module publishes onto the platform outbox (orders
// design O2). Each is a versioned wire form; a payload carries ids, numbers,
// amounts and states and never personal data (O3). netTotal is the exact
// decimal text of the document's net total.

// QuoteSent is orders.quote.sent v1: a quote was sent, frozen at Revision.
type QuoteSent struct {
	QuoteID    int64     `json:"quoteId"`
	Number     int64     `json:"number"`
	Revision   int32     `json:"revision"`
	CustomerID int32     `json:"customerId"`
	Currency   string    `json:"currency"`
	NetTotal   string    `json:"netTotal"`
	SentAt     time.Time `json:"sentAt"`
}

func (QuoteSent) EventName() string     { return "orders.quote.sent" }
func (QuoteSent) EventVersion() int     { return 1 }
func (e QuoteSent) AggregateID() string { return strconv.FormatInt(e.QuoteID, 10) }

// QuoteAccepted is orders.quote.accepted v1: a quote was accepted into an
// order or a subscription (AcceptedIntoKind "order" or "subscription").
type QuoteAccepted struct {
	QuoteID          int64     `json:"quoteId"`
	Number           int64     `json:"number"`
	Revision         int32     `json:"revision"`
	CustomerID       int32     `json:"customerId"`
	AcceptedIntoKind string    `json:"acceptedIntoKind"`
	AcceptedIntoID   int64     `json:"acceptedIntoId"`
	AcceptedAt       time.Time `json:"acceptedAt"`
}

func (QuoteAccepted) EventName() string     { return "orders.quote.accepted" }
func (QuoteAccepted) EventVersion() int     { return 1 }
func (e QuoteAccepted) AggregateID() string { return strconv.FormatInt(e.QuoteID, 10) }

// SalesOrderConfirmed is orders.sales_order.confirmed v1: an order was
// confirmed at Version.
type SalesOrderConfirmed struct {
	OrderID     int64     `json:"orderId"`
	Number      int64     `json:"number"`
	Version     int32     `json:"version"`
	CustomerID  int32     `json:"customerId"`
	Currency    string    `json:"currency"`
	NetTotal    string    `json:"netTotal"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

func (SalesOrderConfirmed) EventName() string     { return "orders.sales_order.confirmed" }
func (SalesOrderConfirmed) EventVersion() int     { return 1 }
func (e SalesOrderConfirmed) AggregateID() string { return strconv.FormatInt(e.OrderID, 10) }

// SalesOrderCancelled is orders.sales_order.cancelled v1.
type SalesOrderCancelled struct {
	OrderID     int64     `json:"orderId"`
	Number      int64     `json:"number"`
	CustomerID  int32     `json:"customerId"`
	CancelledAt time.Time `json:"cancelledAt"`
}

func (SalesOrderCancelled) EventName() string     { return "orders.sales_order.cancelled" }
func (SalesOrderCancelled) EventVersion() int     { return 1 }
func (e SalesOrderCancelled) AggregateID() string { return strconv.FormatInt(e.OrderID, 10) }

// SalesOrderChanged is orders.sales_order.changed v1: a confirmed order was
// edited, or what remained of it cancelled, and is now at Version (O26).
type SalesOrderChanged struct {
	OrderID    int64     `json:"orderId"`
	Number     int64     `json:"number"`
	Version    int32     `json:"version"`
	CustomerID int32     `json:"customerId"`
	ChangedAt  time.Time `json:"changedAt"`
}

func (SalesOrderChanged) EventName() string     { return "orders.sales_order.changed" }
func (SalesOrderChanged) EventVersion() int     { return 1 }
func (e SalesOrderChanged) AggregateID() string { return strconv.FormatInt(e.OrderID, 10) }

// SalesOrderInvoiced is orders.sales_order.invoiced v1: an invoice's issue
// stamped allocations of the order invoiced (O38). It is published by Orders'
// InvoicedWorkHolder on the issue's transaction, once per order the invoice
// bills, at the issue's time.
type SalesOrderInvoiced struct {
	OrderID       int64     `json:"orderId"`
	Number        int64     `json:"number"`
	CustomerID    int32     `json:"customerId"`
	InvoiceID     int64     `json:"invoiceId"`
	InvoiceNumber int64     `json:"invoiceNumber"`
	InvoicedAt    time.Time `json:"invoicedAt"`
}

func (SalesOrderInvoiced) EventName() string     { return "orders.sales_order.invoiced" }
func (SalesOrderInvoiced) EventVersion() int     { return 1 }
func (e SalesOrderInvoiced) AggregateID() string { return strconv.FormatInt(e.OrderID, 10) }
