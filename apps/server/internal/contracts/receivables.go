package contracts

import "context"

// CustomerReceivables is what one customer has been invoiced and what they
// still owe, as the module that owns the sales documents reports it
// (customers receivables design D1): a read-only, in-process port the module
// owning customers reads the figures through, without importing the owning
// module (barred by depguard) or reading its PostgreSQL schema (barred by
// internal/db/schema_test.go). Whichever enabled module owns invoices
// implements it; Compose wires that implementation into every module's Deps
// before any Mount runs (see Module.Receivables). It is nil when no enabled
// module provides one (invoices disabled), which a caller reads as "invoicing
// is off" — never as "nothing is owed".
//
// It is written to the rules ProjectActuals and ProjectExpenses state for
// their own boundary, which hold here word for word, and three of its own.
//
// The provider owns "today". A document's state — open, partially paid,
// overdue, paid, credited — is judged on a day, and the provider judges it on
// the business day of its own clock (BusinessCalendar), as every screen of
// the owning module does; a consumer that passed a day of its own could show
// a state the owning module's app does not. AsOf says which day it was.
//
// It performs no authorization. The caller has already decided that whoever
// is asking may see the customer and may see money at all.
//
// It never calls back into CustomerDirectory. A provider serves from its own
// tables alone — asking the customer directory anything while serving
// customers' own request would be a module cycle at request time — and it
// holds no transaction: the figures are a panel's, and need not be one
// snapshot.
//
// A provider fails rather than under-reports: an error means "the figures
// could not be read", never "nothing is owed".
type CustomerReceivables interface {
	// ReceivablesForCustomer reports what one customer has been invoiced and
	// what they owe, judged on the provider's own today. A customer with no
	// issued document answers the zero value with AsOf set — never an error,
	// never nil Currencies that mean anything other than "none".
	//
	// Drafts are never counted: a draft is not a sales document. A merged-away
	// customer's documents were re-pointed to the survivor and answer there;
	// an anonymised customer's documents stay, kept for bookkeeping, and still
	// answer.
	ReceivablesForCustomer(ctx context.Context, customerID int32) (CustomerReceivableTotals, error)
}

// CustomerReceivableTotals is everything issued to one customer and what
// stands open of it, split by the currency each document is in.
type CustomerReceivableTotals struct {
	// Currencies is one entry per currency any issued document of the
	// customer is in, by ISO 4217 code ascending — a currency whose every
	// figure is zero today (an invoice paid years ago) is still an entry,
	// because the customer has been invoiced in it; a currency no document
	// was ever issued in is absent. A consumer shows a figure, not an entry.
	//
	// There is no total across currencies, and a consumer must not build one:
	// a NOK invoice and a EUR invoice are two figures, not one, and the
	// second is reported so it can be shown as what it is rather than
	// dropped.
	Currencies []CurrencyReceivables
	// OutstandingCount is the issued invoices whose state is open, partially
	// paid or overdue; OverdueCount the overdue ones among them. Both are
	// across every currency: a count is not money.
	OutstandingCount, OverdueCount int
	// OnHoldCount and AtCollectionCount are the outstanding invoices with a
	// live hold, and with a live hand-off to a collection agency. A lifted
	// hold and a withdrawn hand-off count for nothing.
	OnHoldCount, AtCollectionCount int
	// OldestOverdueDueOn is the earliest due date among the overdue
	// invoices, as YYYY-MM-DD; nil when none is overdue.
	OldestOverdueDueOn *string
	// LastIssuedOn is the latest issue date of any issued document of the
	// customer, invoice or credit note; LastPaymentOn the latest paid-on date
	// of any live payment against one of them. Each is YYYY-MM-DD, and nil
	// when there is none.
	LastIssuedOn, LastPaymentOn *string
	// AsOf is the business day the states were judged on, as
	// YYYY-MM-DD. Always set.
	AsOf string
}

// CurrencyReceivables is what was issued in one currency and what stands
// open of it. Every amount is decimal text, exact, two decimals, as every
// money figure crosses a contract — "0.00" when there is nothing, never "".
type CurrencyReceivables struct {
	// Currency is the ISO 4217 code.
	Currency string
	// IssuedThisYear and CreditedThisYear are the gross of the invoices and
	// of the credit notes issued in the calendar year AsOf falls in;
	// IssuedLastYear and CreditedLastYear the same for the year before. A
	// credit note counts in the year it was issued, whatever year the invoice
	// it credits was.
	IssuedThisYear, CreditedThisYear, IssuedLastYear, CreditedLastYear string
	// Outstanding is the open amounts — gross less credited less paid — of
	// the outstanding invoices, summed; Overdue the overdue ones'. Reminder
	// fees, compensation and interest are never in either: the owning module
	// keeps its charges apart from the principal.
	Outstanding, Overdue string
}
