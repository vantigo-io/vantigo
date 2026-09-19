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
// because adding two currencies would produce a number in neither. With no
// currency requested no amounts are summed at all.
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
	// MaxActualsRequests is an error.
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
// (design §2 E2), plus the figures that span all three.
type ActualsTotals struct {
	// Approved is work that has been approved, invoiced work included.
	Approved ActualsBucket
	// Submitted is work submitted and waiting for a decision.
	Submitted ActualsBucket
	// Draft is work nobody has been asked to accept yet: drafts, and work
	// sent back to its owner.
	Draft ActualsBucket
	// UnpricedHoursHundredths is the hours, across all three buckets, whose
	// bill amount is not in BillAmount: work logged without a bill rate, and
	// work whose bill rate is in a currency other than the requested one.
	// With no currency requested only work without a rate is unpriced.
	UnpricedHoursHundredths int64
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
	// Lines is one entry per billing line anything was logged on, by line id
	// ascending, and last — when there is any — the work logged on no line
	// at all (BillingLineID nil). A line with nothing logged on it is absent
	// rather than zero: the provider knows only what was logged, never which
	// lines exist.
	Lines []LineActuals
}

// LineActuals is what was logged on one billing line, or, with a nil
// BillingLineID, on the project without naming a line.
type LineActuals struct {
	BillingLineID *int32
	Totals        ActualsTotals
}
