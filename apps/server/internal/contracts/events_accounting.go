package contracts

import (
	"strconv"
	"time"
)

// The events the accounting module publishes onto the platform outbox
// (purchasing design P5), on the transaction of the change, while it holds
// the supplier invoice's row lock. A payload carries ids, numbers, dates,
// amounts and states (rule 23) — never the invoice's free-text reference or
// description, which a consumer reads through SupplierInvoices.Invoice; Total
// is the exact decimal text of the invoice's total in its currency, and the
// dates are calendar dates (YYYY-MM-DD).

// SupplierInvoicePosted is accounting.supplier_invoice.posted v1: a supplier
// invoice or credit note was posted.
type SupplierInvoicePosted struct {
	InvoiceID   int64     `json:"invoiceId"`
	SupplierID  int64     `json:"supplierId"`
	Kind        string    `json:"kind"`
	Number      string    `json:"number"`
	InvoiceDate string    `json:"invoiceDate"`
	DueDate     string    `json:"dueDate,omitempty"`
	Currency    string    `json:"currency"`
	Total       string    `json:"total"`
	PostedAt    time.Time `json:"postedAt"`
}

func (SupplierInvoicePosted) EventName() string     { return "accounting.supplier_invoice.posted" }
func (SupplierInvoicePosted) EventVersion() int     { return 1 }
func (e SupplierInvoicePosted) AggregateID() string { return strconv.FormatInt(e.InvoiceID, 10) }

// SupplierInvoiceReversed is accounting.supplier_invoice.reversed v1: a
// posted supplier invoice was reversed, and CopyID is the draft Reverse and
// copy made from it.
type SupplierInvoiceReversed struct {
	InvoiceID   int64     `json:"invoiceId"`
	SupplierID  int64     `json:"supplierId"`
	Kind        string    `json:"kind"`
	Number      string    `json:"number"`
	InvoiceDate string    `json:"invoiceDate"`
	DueDate     string    `json:"dueDate,omitempty"`
	Currency    string    `json:"currency"`
	Total       string    `json:"total"`
	CopyID      int64     `json:"copyId,omitempty"`
	ReversedAt  time.Time `json:"reversedAt"`
}

func (SupplierInvoiceReversed) EventName() string     { return "accounting.supplier_invoice.reversed" }
func (SupplierInvoiceReversed) EventVersion() int     { return 1 }
func (e SupplierInvoiceReversed) AggregateID() string { return strconv.FormatInt(e.InvoiceID, 10) }
