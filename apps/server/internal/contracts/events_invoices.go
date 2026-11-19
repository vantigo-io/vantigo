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
// payments. Source is manual, ocr, camt054, vipps, bank_feed or credit (credit
// applied, invoices phase 6 design I11). AmountNOK is the NOK the payment
// brought (I13): its amount on a NOK invoice. It was added in version 1, being
// additive.
type PaymentReceived struct {
	PaymentID  int64     `json:"paymentId"`
	InvoiceID  int64     `json:"invoiceId"`
	Number     int64     `json:"number"`
	CustomerID int32     `json:"customerId"`
	Amount     string    `json:"amount"`
	Currency   string    `json:"currency"`
	AmountNOK  string    `json:"amountNok"`
	PaidOn     string    `json:"paidOn"`
	Source     string    `json:"source"`
	Registered time.Time `json:"registeredAt"`
}

func (PaymentReceived) EventName() string     { return "invoices.payment.received" }
func (PaymentReceived) EventVersion() int     { return 1 }
func (e PaymentReceived) AggregateID() string { return strconv.FormatInt(e.PaymentID, 10) }

// DocumentIssued is invoices.document.issued v1: an invoice or a credit note
// was issued (accounting design A12). It is published by the issue, on its
// transaction and under the document's lock, so it commits with the document
// or not at all. Kind is "invoice" or "credit_note"; IssueDate is the
// document's issue date (a date); GrossTotal its total in Currency, positive
// on a credit note too. contracts.SalesJournal's Documents is the source;
// this only makes a reader prompt.
type DocumentIssued struct {
	DocumentID int64  `json:"documentId"`
	Number     int64  `json:"number"`
	Kind       string `json:"kind"`
	CustomerID int32  `json:"customerId"`
	IssueDate  string `json:"issueDate"`
	GrossTotal string `json:"grossTotal"`
	Currency   string `json:"currency"`
}

func (DocumentIssued) EventName() string     { return "invoices.document.issued" }
func (DocumentIssued) EventVersion() int     { return 1 }
func (e DocumentIssued) AggregateID() string { return strconv.FormatInt(e.DocumentID, 10) }

// PaymentRemoved is invoices.payment.removed v1: a payment registration was
// removed — by a person, or as a reversal the bank reported. It is published
// by a trigger on invoices.payments, on the removal's transaction and under the
// invoice's lock, as PaymentReceived is. Removed is the moment of the removal:
// its business day is the day it falls on in the workspace's business time
// zone, which contracts.SalesJournal's Facts answers as the removal fact's
// Date. PaidOn and Source are the registration's.
type PaymentRemoved struct {
	PaymentID  int64     `json:"paymentId"`
	InvoiceID  int64     `json:"invoiceId"`
	Number     int64     `json:"number"`
	CustomerID int32     `json:"customerId"`
	Amount     string    `json:"amount"`
	Currency   string    `json:"currency"`
	PaidOn     string    `json:"paidOn"`
	Source     string    `json:"source"`
	Removed    time.Time `json:"removedAt"`
}

func (PaymentRemoved) EventName() string     { return "invoices.payment.removed" }
func (PaymentRemoved) EventVersion() int     { return 1 }
func (e PaymentRemoved) AggregateID() string { return strconv.FormatInt(e.PaymentID, 10) }

// EfakturaTransmissionChanged is invoices.efaktura.transmission_changed v1
// (invoices phase 6 design I31, §7): an invoice's eFaktura transmission was
// queued or moved — Status is queued, submitted, delivered, failed,
// unconfirmed or cancelled, and PaymentType efaktura or both. It carries no
// eFaktura identifier and no name. Published on the transaction, or the
// statement, that moved the row.
type EfakturaTransmissionChanged struct {
	TransmissionID int64  `json:"transmissionId"`
	InvoiceID      int64  `json:"invoiceId"`
	Status         string `json:"status"`
	PaymentType    string `json:"paymentType"`
}

func (EfakturaTransmissionChanged) EventName() string {
	return "invoices.efaktura.transmission_changed"
}
func (EfakturaTransmissionChanged) EventVersion() int { return 1 }
func (e EfakturaTransmissionChanged) AggregateID() string {
	return strconv.FormatInt(e.TransmissionID, 10)
}

// AvtaleGiroClaimChanged is invoices.avtalegiro.claim_changed v1 (I33, §7):
// an AvtaleGiro claim was made or moved — Status is submitted, accepted,
// rejected, cancel_requested, cancelled or paid; Amount the claim's exact
// decimal text in NOK and DueDate its due date. No KID and no name.
type AvtaleGiroClaimChanged struct {
	ClaimID   int64  `json:"claimId"`
	InvoiceID int64  `json:"invoiceId"`
	Status    string `json:"status"`
	Amount    string `json:"amount"`
	DueDate   string `json:"dueDate"`
}

func (AvtaleGiroClaimChanged) EventName() string     { return "invoices.avtalegiro.claim_changed" }
func (AvtaleGiroClaimChanged) EventVersion() int     { return 1 }
func (e AvtaleGiroClaimChanged) AggregateID() string { return strconv.FormatInt(e.ClaimID, 10) }

// SendJobFinished is invoices.send_job.finished v1 (invoices phase 6 design
// I36, plan E3): the invoices-send worker ended one document of a send batch.
// State is sent, handed_over, awaiting_print, skipped, failed or uncertain;
// Channel the channel it went by, "" when none was tried; Code the skip's or
// the refusal's, "" otherwise. No recipient and no name.
type SendJobFinished struct {
	JobID     int64  `json:"jobId"`
	BatchID   int64  `json:"batchId"`
	InvoiceID int64  `json:"invoiceId"`
	Channel   string `json:"channel"`
	State     string `json:"state"`
	Code      string `json:"code"`
}

func (SendJobFinished) EventName() string     { return "invoices.send_job.finished" }
func (SendJobFinished) EventVersion() int     { return 1 }
func (e SendJobFinished) AggregateID() string { return strconv.FormatInt(e.JobID, 10) }

// CustomerCreditChanged is invoices.customer_credit.changed v1: a customer's
// credit in a currency moved (invoices phase 6 design I8, §7) — an
// overpayment kept, a credit note's surplus moved, credit applied or
// refunded, a removal reversed — or a merge moved it from one customer to
// another, which publishes one for each. It is published by a trigger on the
// movement, under the customer's credit lock. Balance is the credit after
// it. contracts.SalesJournal's Facts is the source; this only makes a reader
// prompt.
type CustomerCreditChanged struct {
	CustomerID int32  `json:"customerId"`
	Currency   string `json:"currency"`
	Balance    string `json:"balance"`
}

func (CustomerCreditChanged) EventName() string { return "invoices.customer_credit.changed" }
func (CustomerCreditChanged) EventVersion() int { return 1 }
func (e CustomerCreditChanged) AggregateID() string {
	return strconv.FormatInt(int64(e.CustomerID), 10)
}
