package contracts

import (
	"context"
	"time"
)

// SalesJournal is the one sanctioned way a module reads what Invoices has
// issued and been paid, in an order it can resume from (accounting design
// A12, R1): a single-provider, read-only, in-process port served by Invoices.
// Compose and Workers wire it into every module's Deps (see
// Module.SalesJournal); it is nil when invoices is disabled.
//
// It is the source a ledger posts from; Invoices' events
// (invoices.document.issued, invoices.payment.received and
// invoices.payment.removed) only make a reader prompt, and a charge payment
// has none: its facts are found by the reader's next pass. A reader keeps its
// own cursors — the last number, the last seq — and asks after them again
// until a page comes back short. Both cursors never skip:
//
//   - A document's number comes from Invoices' documents counter, whose row
//     every issue holds locked from the number's allocation to its commit, so
//     numbers become visible in order: when number n is visible, every number
//     below it that will ever be issued is too.
//   - A fact's seq is taken at commit, by a deferred trigger, from a counter
//     row held for that instant: seqs become visible in order too. Facts
//     exist for every payment and charge payment registered or removed since
//     Invoices' migration 00180, and for every one before it, numbered then
//     in the order they happened: a registration at its registration's
//     moment, a removal at its removal's, a registration first at the same
//     moment.
//
// Every method runs on the pool, holds no lock and is called with no
// transaction of the caller's open (module-boundaries rule 8's discipline);
// OpenAt opens a read-only one of its own, for its snapshot. It performs no
// authorization. Every amount is exact decimal text with two
// decimals, as every money figure crosses a contract. Invoices issues NOK
// only in this phase; an amount named NOK is the document's own figure at its
// own exchange rate, which is 1 for NOK. Credit notes, and every line and
// summary of one, are answered positive, as Invoices stores them: the kind
// says which way they go.
type SalesJournal interface {
	// Documents is up to limit issued invoices and credit notes numbered
	// after afterNumber, in number order (0 reads from the first).
	Documents(ctx context.Context, afterNumber int64, limit int) ([]JournalDocument, error)
	// Document is the issued invoice or credit note with that id, as
	// Documents answers it, read by its key: nil, with no error, when there
	// is none or it is a draft. A ledger reads a written-off invoice's VAT
	// summaries through it, and a fact it retries.
	Document(ctx context.Context, id int64) (*JournalDocument, error)
	// CreditNotesOf is the issued credit notes of the invoice with that id,
	// as Documents answers them, in number order: none when it has none. A
	// ledger shares a write-off's VAT correction over what the invoice's VAT
	// summaries less its credit notes' leave (accounting design R15).
	CreditNotesOf(ctx context.Context, invoiceID int64) ([]JournalDocument, error)
	// Facts is up to limit facts with a seq after afterSeq, in seq order (0
	// reads from the first).
	Facts(ctx context.Context, afterSeq int64, limit int) ([]JournalFact, error)
	// OpenAt is up to limit invoices numbered after afterNumber (0 reads
	// from the first), in number order, that were issued on or before day
	// and whose open amount at the end of that day is not zero: its gross,
	// less the credit notes issued on or before day, less the payments paid
	// on or before day and not removed by then (a payment removed on a later
	// business day still counts). Charges are never in it. Below zero is
	// credit the customer has to come back.
	//
	// The page is read in one snapshot, and AsOfSeq says which: the highest
	// seq of a fact committed in it (0 when there is none). For every
	// invoice numbered within the page — after afterNumber, and up to its
	// last document's number, or without end on a page that comes back
	// short — a fact with a seq up to AsOfSeq is in the open amount as the
	// day rule above says, and a fact after it is not, whatever its day.
	OpenAt(ctx context.Context, day time.Time, afterNumber int64, limit int) (OpenPage, error)
}

// MaxJournalPage is the most a SalesJournal page answers; a larger limit is
// read as this, and a limit below 1 as 1.
const MaxJournalPage = 500

// The kinds of JournalDocument.Kind.
const (
	JournalInvoice    = "invoice"
	JournalCreditNote = "credit_note"
)

// JournalDocument is one issued invoice or credit note. Dates are UTC
// midnight of the calendar day.
type JournalDocument struct {
	ID     int64
	Number int64
	// Kind is JournalInvoice or JournalCreditNote.
	Kind       string
	IssueDate  time.Time
	DueDate    *time.Time // an invoice's; nil on a credit note
	CustomerID int32
	Currency   string
	// CreditsID and CreditsNumber are, on a credit note, the invoice it
	// credits; zero on an invoice.
	CreditsID, CreditsNumber int64
	// Net, VAT and Gross are the document's totals in its currency;
	// VATNOK is its VAT in NOK.
	Net, VAT, Gross, VATNOK string
	Lines                   []JournalLine
	VATSummaries            []JournalVATSummary
}

// The kinds of JournalLine.Kind: what the line was made from, snapshotted at
// issue. A line issued before Invoices' migration 00180 has "".
const (
	LineKindHours              = "hours"
	LineKindExpense            = "expense"
	LineKindMilestone          = "milestone"
	LineKindSettlement         = "settlement"
	LineKindOrderLine          = "order_line"
	LineKindSubscriptionPeriod = "subscription_period"
	LineKindDeduction          = "deduction"
	LineKindManual             = "manual"
	LineKindMixed              = "mixed"
)

// JournalLine is one line of a document, in position order.
type JournalLine struct {
	ID       int64
	Position int32
	// NetNOK is the line's net in NOK: negative on a deduction line.
	NetNOK string
	// Kind is one of the LineKind constants, "" for a line issued before
	// the snapshot existed (fall back to the line's tax code). A credit
	// note's line has its original line's kind, tax category and project.
	Kind string
	// TaxCategoryID is the product's tax category in Products, nil when the
	// line was not made from a product's order line.
	TaxCategoryID *int32
	// ProjectID is the project every source of the line belongs to, nil
	// when none or more than one.
	ProjectID *int32
	// SAFT is the SAF-T standard tax code the line was issued with, Category
	// its UNCL5305 VAT category and RatePercent its rate ("25.00").
	SAFT, Category, RatePercent string
}

// JournalVATSummary is one VAT summary row of a document: its base and its
// VAT per category and rate, with the SAF-T code.
type JournalVATSummary struct {
	SAFT, Category, RatePercent string
	BaseNOK, VATNOK             string
}

// The kinds of JournalFact.Kind. Later kinds are added to the end; a reader
// leaves a kind it does not know unposted.
const (
	FactPayment              = "payment"
	FactPaymentRemoved       = "payment_removed"
	FactChargePayment        = "charge_payment"
	FactChargePaymentRemoved = "charge_payment_removed"
	// FactWriteOff is an invoice's open amount given up (accounting design
	// A16, R4), FactWriteOffUndone its undoing; both carry WriteOff.
	FactWriteOff       = "write_off"
	FactWriteOffUndone = "write_off_undone"
	// FactRecoveredPayment is money that arrived after a write-off: not a
	// payment of the open amount, which the write-off closed, but a
	// recovery of what was written off. It and its removal carry the
	// write-off it recovers in WriteOff.
	FactRecoveredPayment        = "recovered_payment"
	FactRecoveredPaymentRemoved = "recovered_payment_removed"
)

// The reasons of JournalWriteOff.Reason.
const (
	// WriteOffLoss is a loss that is konstatert; only it may carry the VAT
	// correction of mval. § 4-7.
	WriteOffLoss = "loss"
	// WriteOffRounding is a small difference, which a ledger posts to its
	// rounding account.
	WriteOffRounding = "rounding"
	WriteOffOther    = "other"
)

// JournalWriteOff is the write-off a fact is about, or the one a recovered
// payment recovers.
type JournalWriteOff struct {
	ID int64
	// Reason is one of the WriteOff constants.
	Reason string
	// VATCorrection is whether the loss takes its VAT back: the write-off's
	// amount is then shared in proportion over the invoice's VAT summaries
	// less its credit notes' (read them with Document and CreditNotesOf).
	VATCorrection bool
	// AmountNOK is the open amount the write-off closed, in NOK.
	AmountNOK string
	// RecoveredBeforeNOK is, on a recovered payment's fact, what the
	// write-off had recovered before it: the amounts of its recovered
	// payments registered at a lower seq and not removed at a lower seq, in
	// NOK, so a ledger scales each recovery cumulatively (R15). "0.00" on
	// every other fact.
	RecoveredBeforeNOK string
}

// The sources of JournalFact.Source, as Invoices stores them. A bank_feed
// payment came from a line the bank feed read, matched by the feed with no
// registering user or applied by a person: a bank payment as ocr and camt054
// are.
const (
	PaymentSourceManual   = "manual"
	PaymentSourceOCR      = "ocr"
	PaymentSourceCamt054  = "camt054"
	PaymentSourceVipps    = "vipps"
	PaymentSourceBankFeed = "bank_feed"
)

// JournalFact is one registration or removal of a payment of an invoice's
// principal or of its charges, or one write-off made or undone.
type JournalFact struct {
	Seq int64
	// Kind is one of the Fact constants.
	Kind string
	// SubjectID is the payment's, or the charge payment's, own id: a
	// registration and its removal share it.
	SubjectID int64
	// Date is the business day the fact belongs to: a registration's paid-on
	// day, a removal's the day it was removed on in the workspace's business
	// time zone. UTC midnight.
	Date          time.Time
	InvoiceID     int64
	InvoiceNumber int64
	CustomerID    int32
	// AmountNOK is the payment's amount in NOK, positive on a removal too. A
	// charge payment's is all it carries of what it paid: it is not split
	// by the charges it covered.
	AmountNOK string
	// Source is one of the PaymentSource constants (a charge payment is never
	// vipps).
	Source string
	// BankAccount is the account number of the receiving account the bank
	// file or the bank feed named, when the payment came from a bank line
	// (ocr, camt054 or bank_feed); "" otherwise.
	BankAccount string
	// WriteOff is, on a write-off's fact, that write-off, and on a recovered
	// payment's, the write-off it recovers; nil on every other kind. A
	// write-off's AmountNOK is its amount, its Source manual.
	WriteOff *JournalWriteOff
}

// OpenPage is one page of OpenAt: the invoices open at its day, and the
// snapshot they were read in.
type OpenPage struct {
	// AsOfSeq is the highest seq of a fact the page's snapshot saw, 0 when
	// it saw none.
	AsOfSeq   int64
	Documents []OpenDocument
}

// OpenDocument is one invoice open at OpenAt's day.
type OpenDocument struct {
	ID, Number int64
	CustomerID int32
	IssueDate  time.Time
	DueDate    time.Time
	Currency   string
	// OpenNOK is the open amount in NOK at the day; below zero is credit due
	// back to the customer.
	OpenNOK string
}
