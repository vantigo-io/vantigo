package contracts

import "context"

// MaxActualsRequests is the most projects one ActualsForProjects call may
// name. A provider refuses a longer batch rather than building a query of
// unbounded size; a caller paging over a large portfolio caps its own pages
// at this number too.
const MaxActualsRequests = 2000

// ProjectActuals is what has been logged against projects, as the module
// that owns the hours reports it: a read-only, in-process port a module
// owning budgets (projects) reads actual, logged work through, without
// importing the owning module (barred by depguard) or reading its
// PostgreSQL schema (barred by internal/db/schema_test.go). Whichever
// enabled module owns logged work implements it; Compose wires that
// implementation into every module's Deps before any Mount runs (see
// Module.Actuals). It is nil when no enabled module provides one (time
// disabled), which a caller reads as "time tracking is off" — never as
// "nothing has been logged".
//
// It performs no authorization. Every caller has already decided that
// whoever is asking may see the project and, separately, may see amounts at
// all: the answer carries hours, bill amounts and cost amounts together, and
// shaping them to the caller's rights is the caller's job.
//
// The caller owns the currency fact. A project's currency lives in the
// module that owns projects, and a provider must never call back into a
// directory to learn it while serving — that would be a module cycle at
// request time — so ActualsRequest carries the currency in. An amount counts
// only when the logged work's own currency equals the requested one;
// anything logged in another currency contributes its hours and no amount,
// because adding two currencies would produce a number in neither.
//
// With no currency requested no amounts are reported at all — every bucket's
// BillAmount and CostAmount is "0.00", and the hours are all unpriced and
// uncosted. A provider must not infer a currency from what happens to be
// logged: a project that carries no amounts has none, and a figure the
// caller never asked for is a figure nobody can label.
//
// A provider fails rather than under-reports: an error means "the figures
// could not be read", never "there are none". A project with nothing logged
// is a present, zero-valued ActualsTotals, and it is present in a batch
// result too.
type ProjectActuals interface {
	// Actuals is one project's totals and the same totals split per billing
	// line. A project with nothing logged answers zero totals and no lines.
	Actuals(ctx context.Context, req ActualsRequest) (ProjectActualsEntry, error)
	// ActualsForProjects is the totals of many projects at once, keyed by
	// project id: every requested project is in the result, zero-valued when
	// it has nothing logged. Different projects may be asked in different
	// currencies in one call. No requests answers an empty map; more than
	// MaxActualsRequests is an error, and so is naming one project twice —
	// one project has one answer, and two requests for it are the caller's
	// bug however they agree.
	ActualsForProjects(ctx context.Context, reqs []ActualsRequest) (map[int32]ActualsTotals, error)
}

// ActualsRequest names a project and the currency its amounts are wanted in.
// Currency is nil when the project carries no amounts: hours are still
// reported, and no amount is.
type ActualsRequest struct {
	ProjectID int32
	Currency  *string // ISO 4217, nil: the project carries no amounts
}

// ActualsBucket is what one bucket of logged work amounts to. The hours are
// exact; the amounts are decimal text, so no float ever rounds money on its
// way between modules, and they hold only what was logged in the requested
// currency.
//
// Each bucket is rounded once, on its own, after everything in it has been
// added up exactly. Two buckets, or two lines, therefore need not add up to
// the cent — three buckets of 0.005 each are "0.01" three times over while
// the work is worth 0.02 altogether. A consumer that wants a subject's whole
// figure takes ActualsTotals.Total, which is rounded once from the unrounded
// sum, and never adds buckets or lines up itself.
type ActualsBucket struct {
	// HoursHundredths is hundredths of an hour: 1.25 h is 125. Exact.
	HoursHundredths int64
	// BillAmount is what the bucket's hours are worth at the bill rates the
	// work was logged at, as decimal text with two decimals ("1234.50"),
	// "0.00" when there is nothing. Only work logged in the requested
	// currency is in it.
	BillAmount string
	// CostAmount is the same for what the bucket's hours cost the company,
	// at the cost rates the work was logged at. "0.00" when there is
	// nothing, and only work whose cost is in the requested currency is in
	// it.
	CostAmount string
}

// ActualsTotals is everything logged against one project (or one of its
// billing lines), in the three buckets every surface shows the split of
// (design §2 E2), plus the figures that span all three — and Invoiced, the
// part of Approved that has already been billed.
type ActualsTotals struct {
	// Approved is work that has been approved, invoiced work included.
	Approved ActualsBucket
	// Invoiced is the part of Approved that has already been invoiced
	// (customer 360 design D1). It is not a fourth bucket beside the three:
	// every invoiced hour and amount is in Approved too, Total counts it once,
	// and a consumer that ignores this field reads exactly what it read
	// before. Approved less Invoiced is the work approved and not yet billed.
	//
	// Like every bucket it is rounded on its own, so Approved.BillAmount less
	// Invoiced.BillAmount can be a cent away from the not-yet-invoiced work
	// rounded once. Hours subtract exactly.
	Invoiced ActualsBucket
	// Submitted is work submitted and waiting for a decision.
	Submitted ActualsBucket
	// Draft is work nobody has been asked to accept yet: drafts, and work
	// sent back to its owner.
	Draft ActualsBucket
	// Total is the three buckets together, and it is the figure a consumer
	// reports as the subject's own. Its hours are their hours added up, which
	// is exact either way; its amounts are the *unrounded* sums of everything
	// in all three buckets, rounded once — so it can differ by a cent or two
	// from adding the three published bucket amounts, each of which was
	// rounded on its own first.
	//
	// It exists because the rounding rule makes that difference unavoidable
	// and the sum of three rounded figures is the wrong one: a total, a
	// margin and a budget percentage must all be computed from here.
	//
	// Every provider fills it. A consumer may refuse totals whose Total hours
	// are not exactly its three buckets' hours — that is a provider that
	// forgot the field, not a project with nothing logged.
	Total ActualsBucket
	// UnpricedHoursHundredths is the *billable* hours, across all three
	// buckets, whose bill amount is not in BillAmount: billable work logged
	// without a bill rate, and billable work whose bill rate is in a
	// currency other than the requested one. With no currency requested only
	// billable work without a rate is unpriced.
	//
	// Non-billable work is never unpriced — it was never meant to carry a
	// price, and counting it here would send someone looking for a missing
	// rate that does not exist. NonBillableHoursHundredths is where those
	// hours are reported.
	//
	// It is *not* unconditionally the same figure the owning module shows on
	// its own surfaces, and a consumer must not present it as one. It agrees
	// with a per-project summary that covers only work waiting for or past a
	// decision when two things hold: nothing billable and unpriced sits in a
	// bucket that summary leaves out (a draft or a rejected entry is counted
	// here and is outside such a summary entirely), and the project carries a
	// currency — because with Currency nil this reports no amounts at all and
	// calls every priced billable hour unpriced, while a surface free to
	// infer a currency from what happens to be logged would call the same
	// hours priced.
	UnpricedHoursHundredths int64
	// UncostedHoursHundredths is the hours, across all three buckets and
	// billable or not, whose cost is not in CostAmount: work logged without
	// a cost rate, and work whose cost is in a currency other than the
	// requested one. With no currency requested only work without a cost
	// rate is uncosted.
	//
	// Unlike unpriced hours this covers non-billable work too: work nobody
	// is billed for still costs the company. A consumer showing cost or
	// margin must surface this — a margin computed from CostAmount alone is
	// overstated by exactly the cost of these hours, and cost sits behind a
	// sensitive permission precisely because people decide things on it.
	UncostedHoursHundredths int64
	// BillableHoursHundredths and NonBillableHoursHundredths split the
	// hours of all three buckets by whether the work is billable at all.
	// Together they are the three buckets' hours.
	BillableHoursHundredths    int64
	NonBillableHoursHundredths int64
	// LastEntryDate is the date of the most recently dated work, in all
	// three buckets, as YYYY-MM-DD; nil when nothing has been logged.
	LastEntryDate *string
}

// ProjectActualsEntry is one project's totals and the same totals per
// billing line.
type ProjectActualsEntry struct {
	Totals ActualsTotals
	// Lines is one entry per billing line anything was logged on, by billing
	// line id ascending, and last — when there is any — the work logged on
	// no line at all (BillingLineID nil). A line with nothing logged on it
	// is absent rather than zero: the provider knows only what was logged,
	// never which lines exist, and it cannot order them by anything it does
	// not know, a line's code included. A consumer wanting every line, in
	// code order, merges this against its own list of lines.
	//
	// Adding the lines up does not reliably give Totals: each is rounded on
	// its own (see ActualsBucket). Totals — and inside it Totals.Total — is
	// the project's figure.
	Lines []LineActuals
}

// LineActuals is what was logged on one billing line, or, with a nil
// BillingLineID, on the project without naming a line.
type LineActuals struct {
	BillingLineID *int32
	Totals        ActualsTotals
}
