package contracts

import (
	"strconv"
	"time"
)

// The events the invoices module publishes onto the platform outbox (virtual
// employees design V30). Each is a versioned wire form; a payload carries ids,
// numbers, amounts and dates and never personal data (O3): no buyer name, address
// or e-mail. grossTotal and amount are the exact decimal text of the figure.

// InvoiceOverdue is invoices.invoice.overdue v1: an issued invoice's state became
// overdue on Day, the business calendar day (a date). It is published once for
// each invoice, by the invoices-overdue-events worker, while the worker holds the
// invoice's row.
type InvoiceOverdue struct {
	InvoiceID  int64  `json:"invoiceId"`
	Number     int64  `json:"number"`
	CustomerID int32  `json:"customerId"`
	DueDate    string `json:"dueDate"`
	Day        string `json:"day"`
	Currency   string `json:"currency"`
	GrossTotal string `json:"grossTotal"`
}

func (InvoiceOverdue) EventName() string     { return "invoices.invoice.overdue" }
func (InvoiceOverdue) EventVersion() int     { return 1 }
func (e InvoiceOverdue) AggregateID() string { return strconv.FormatInt(e.InvoiceID, 10) }

// PaymentReceived is invoices.payment.received v1: a payment was registered
// against an issued invoice, by a person, a bank line or a Vipps capture. It is
// published by a trigger on invoices.payments, on the transaction of the
// registration and under the invoice's lock, because four write paths register
// payments. Source is manual, ocr, camt054 or vipps.
type PaymentReceived struct {
	PaymentID  int64     `json:"paymentId"`
	InvoiceID  int64     `json:"invoiceId"`
	Number     int64     `json:"number"`
	CustomerID int32     `json:"customerId"`
	Amount     string    `json:"amount"`
	Currency   string    `json:"currency"`
	PaidOn     string    `json:"paidOn"`
	Source     string    `json:"source"`
	Registered time.Time `json:"registeredAt"`
}

func (PaymentReceived) EventName() string     { return "invoices.payment.received" }
func (PaymentReceived) EventVersion() int     { return 1 }
func (e PaymentReceived) AggregateID() string { return strconv.FormatInt(e.PaymentID, 10) }
