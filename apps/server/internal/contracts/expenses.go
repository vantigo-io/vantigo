package contracts

import "context"

// MaxExpensesProjects is the most projects one ExpensesForProjects call may
// name. It is MaxActualsRequests deliberately: a portfolio page asks the two
// providers about the same set of projects in the same request, so one cap
// covers both batches and a caller that has already paged for one never has
// to page again for the other.
const MaxExpensesProjects = MaxActualsRequests

// ProjectExpenses is what a project's expenses cost and bill, as the module
// that owns the expenses reports it: a read-only, in-process port a module
// owning budgets (projects) reads recorded costs through, without importing
// the owning module (barred by depguard) or reading its PostgreSQL schema
// (barred by internal/db/schema_test.go). Whichever enabled module owns
// expenses implements it; Compose wires that implementation into every
// module's Deps before any Mount runs (see Module.Expenses). It is nil when
// no enabled module provides one (expenses disabled), which a caller reads as
// "expense tracking is off" — never as "nothing has been recorded".
//
// It is the mirror of ProjectActuals, and everything that contract says about
// its own boundary holds here word for word.
//
// It performs no authorization. Every caller has already decided that whoever
// is asking may see the project and, separately, may see amounts at all: the
// answer carries what the company paid and what the customer will be charged
// together, and shaping them to the caller's rights is the caller's job.
//
// It never calls back into ProjectDirectory. A provider serves from its own
// tables alone; asking the project directory anything while serving projects'
// own request would be a module cycle at request time. That is also why
// nothing here takes a currency in the way ActualsRequest does: an expense
// carries its own currency per line, and rather than fold the lines into a
// currency the provider would have to ask for, the answer is reported *per
// currency* and the caller — which owns the project's currency — decides
// which of them is the project's and how to present the rest. Nothing is ever
// converted: adding two currencies would produce a number in neither.
//
// A provider fails rather than under-reports: an error means "the figures
// could not be read", never "there are none".
type ProjectExpenses interface {
	// ExpensesForProjects reports, per project and per currency, what the
	// project's expenses cost and bill. A project with nothing recorded is
	// absent.
	//
	// Unlike ActualsForProjects, which answers a zero-valued entry for every
	// project it was asked about, a project with nothing recorded against it
	// is simply not in the map: expenses are optional on a project in a way
	// hours are not, and "no key" is the same answer as a zero-valued one
	// for every consumer that ranges over what it got.
	//
	// No project ids answers an empty map; more than MaxExpensesProjects is
	// an error, and so is naming one project twice — one project has one
	// answer, and two requests for it are the caller's bug however they
	// agree.
	ExpensesForProjects(ctx context.Context, projectIDs []int32) (map[int32]ProjectExpenseTotals, error)
}

// ProjectExpenseTotals is everything recorded against one project, split by
// the currency each line was recorded in.
type ProjectExpenseTotals struct {
	// Currencies is one entry per currency anything was recorded in, by ISO
	// 4217 code ascending. A currency nothing was recorded in is absent
	// rather than zero — the provider knows only what was recorded, and never
	// which currency the project itself carries.
	//
	// There is no total across currencies, and a consumer must not build one:
	// a project in NOK with a EUR receipt has two figures, not one, and the
	// second is reported so it can be shown as what it is rather than
	// dropped.
	Currencies []CurrencyExpenses
	// LastEntryDate is the date of the most recently dated line, over every
	// currency and every bucket, as YYYY-MM-DD. It is never nil on a project
	// that is in the map at all, because a project with nothing recorded is
	// absent.
	LastEntryDate *string
}

// CurrencyExpenses is what the lines recorded in one currency cost and bill,
// in the three buckets every surface shows the split of, plus the figures
// that span all three.
//
// The bucket a line falls in is its *unit's* status, never its own: a travel
// claim's line is judged through its claim, whose status is the one somebody
// approved or sent back, while the line's own status column stays at the
// default nobody reads. Approved is what has been approved, invoiced lines
// included — invoicing is a stamp here, not a status. Submitted is waiting
// for a decision. Draft is what nobody has been asked to accept yet, and a
// *rejected* unit is in it: it is back with its owner to fix and resubmit,
// exactly as ProjectActuals buckets a rejected time entry.
type CurrencyExpenses struct {
	// Currency is the ISO 4217 code every figure below is in.
	Currency string
	// Approved, Submitted and Draft are the three buckets; Total is all three
	// together and is the figure a consumer reports as the project's own in
	// this currency.
	//
	// Total's amounts are the *unrounded* sums of everything in all three
	// buckets, rounded once — so it can differ by a cent or two from adding
	// the three published bucket amounts, each of which was rounded on its
	// own first. It exists because that difference is unavoidable and the sum
	// of three rounded figures is the wrong one: a total, a margin and a
	// percentage must all be computed from here, and a consumer never adds
	// buckets up itself. Its Count is their counts added, which is exact
	// either way.
	Approved, Submitted, Draft, Total ExpenseBucket
	// ReadyCount and ReadyAmount are the lines that can go on an invoice
	// today: the unit approved, the line billable, a bill amount present,
	// invoiced_at not set, and never a per diem day, which bills nobody
	// anything. They span the buckets' definition rather than sitting inside
	// one — only approved lines qualify — and are given separately because
	// "ready to invoice" is a decision about the invoicing track, not about
	// the approval one. ReadyAmount is decimal text in ExpenseBucket's
	// format: two decimals, half away from zero, "0.00" for nothing, and it
	// is this currency's alone — never added to another's.
	ReadyCount  int64
	ReadyAmount string
	// InvoicedCount and InvoicedAmount are the lines already stamped as
	// invoiced, and what was billed for them. Invoicing is a stamp, not a
	// status: an invoiced line is still in the Approved bucket, and these two
	// figures say how much of it has already left the building.
	// InvoicedAmount carries the same decimal text and the same rule as
	// ReadyAmount: this currency's own figure, never added across currencies.
	InvoicedCount  int64
	InvoicedAmount string
	// UnpricedCount is the billable lines, in any bucket, that carry no bill
	// amount — billable mileage with no customer rate per kilometre, a
	// billable outlay nobody has priced yet; per diem days are out of it, as
	// they are out of ReadyCount. They are counted here and are
	// *not* in any BillAmount, because a missing price is not a price of
	// nothing: a consumer showing what a project will bill must surface this,
	// or the figure it shows is short by however much these lines turn out to
	// be worth.
	UnpricedCount int64
	// SupplierInvoices is the part of this currency's three buckets and their
	// total that is supplier invoices (supplier invoices design D3) — nil when
	// the currency holds none. It is a sub-figure, never a split of the
	// figures above: Approved, Submitted, Draft and Total keep meaning every
	// line, supplier invoices included, so a consumer that knows nothing of
	// this field reads exactly what it always read. Its buckets follow the
	// same rules as those — the unit's status, rejected as draft, cost the
	// net, bill the billable lines' stored amounts, each rounded once on its
	// own — and its Total is rounded once from the unrounded whole, as Total
	// is.
	SupplierInvoices *ExpenseSplit
}

// ExpenseBucket is what one bucket of recorded expenses amounts to. The count
// is exact; the amounts are decimal text, so no float ever rounds money on
// its way between modules.
//
// Each bucket is rounded once, on its own, after everything in it has been
// added up exactly. Two buckets therefore need not add up to the cent — three
// buckets of 0.005 each are "0.01" three times over while the expenses are
// worth 0.02 altogether — which is what CurrencyExpenses.Total is for.
type ExpenseBucket struct {
	// Count is how many lines are in the bucket. A travel claim's lines count
	// one each: a trip is one unit of approval, but it is its lines that cost
	// money.
	Count int64
	// CostAmount is what the bucket's lines cost the company: the net, the
	// gross less the VAT, whoever paid. An outlay the company paid and one
	// the employee is reimbursed for cost the project the same thing, and
	// mileage and per diem carry no VAT, so their net is their gross. Decimal
	// text with two decimals ("1234.50"), rounded half away from zero, "0.00"
	// when there is nothing.
	CostAmount string
	// BillAmount is what the bucket's *billable* lines will charge the
	// customer: their stored bill amounts, which are the net plus a markup
	// for an outlay and the kilometres at the customer's own rate for
	// mileage. A non-billable line contributes nothing, and neither does a
	// billable line with no bill amount — that one is counted in
	// CurrencyExpenses.UnpricedCount instead of being read as zero. Per diem
	// is never billable. Same text rule as CostAmount.
	BillAmount string
}

// ExpenseSplit is one kind's share of a currency's buckets: the same three
// buckets and the across-bucket total, over the lines of that kind alone. It
// is only ever the supplier invoices' (CurrencyExpenses.SupplierInvoices).
type ExpenseSplit struct {
	Approved, Submitted, Draft, Total ExpenseBucket
}
