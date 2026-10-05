package contracts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MaxBillableRows is the most rows one billable read answers, and the most
// ids one request may name. A read by project that finds more says so in its
// page's More rather than answering a page of unbounded size.
const MaxBillableRows = 5000

// BillableRequest names the work a billable read answers: either projects
// (at most MaxActualsRequests) or exact rows by id (at most MaxBillableRows,
// a held source's freshness) — exactly one of the two, neither naming one
// twice.
type BillableRequest struct {
	ProjectIDs []int32
	// IDs answers only the rows that are still billable: a row since
	// invoiced, or no longer invoiceable, is absent.
	IDs []int64
	// Until bounds the work by its date, on or before; zero is no bound.
	Until time.Time
}

// Validate is the request's shape: exactly one of ProjectIDs and IDs, within
// its bound, without duplicates. A provider refuses a request that fails it.
func (r BillableRequest) Validate() error {
	switch {
	case len(r.ProjectIDs) == 0 && len(r.IDs) == 0:
		return errors.New("contracts: a billable request names no project and no id")
	case len(r.ProjectIDs) > 0 && len(r.IDs) > 0:
		return errors.New("contracts: a billable request names both projects and ids")
	case len(r.ProjectIDs) > MaxActualsRequests:
		return fmt.Errorf("contracts: a billable request names %d projects, more than %d", len(r.ProjectIDs), MaxActualsRequests)
	case len(r.IDs) > MaxBillableRows:
		return fmt.Errorf("contracts: a billable request names %d ids, more than %d", len(r.IDs), MaxBillableRows)
	}
	if id, ok := firstDuplicate(r.ProjectIDs); ok {
		return fmt.Errorf("contracts: a billable request names project %d twice", id)
	}
	if id, ok := firstDuplicate(r.IDs); ok {
		return fmt.Errorf("contracts: a billable request names id %d twice", id)
	}
	return nil
}

func firstDuplicate[T comparable](values []T) (T, bool) {
	seen := make(map[T]bool, len(values))
	for _, v := range values {
		if seen[v] {
			return v, true
		}
		seen[v] = true
	}
	var zero T
	return zero, false
}

// BillableHour is one approved, billable, priced time entry not yet invoiced
// (status approved, billable, a bill rate). Amount is hours × bill rate ×
// the multiplier (100 % for none), exact decimal text with up to eight
// decimals. The entry's note is never here: it is the person's own text, not
// the customer's.
type BillableHour struct {
	ID                    int64
	Revision, ProjectID   int32
	BillingLineID         *int32
	UserID                uuid.UUID
	Date                  time.Time
	HoursHundredths       int64
	BillRate, Currency    string
	BillMultiplierPercent *string
	WorkTypeID            *int32
	WorkTypeName          string
	TaskTitle             string
	Amount                string
}

// BillableExpense is one expense line ready to invoice and not yet invoiced
// (expenses.ready_to_invoice: approved, billable, not per diem, priced).
// BillAmount is what the customer is billed, the markup included.
type BillableExpense struct {
	ID                      int64
	Revision, ProjectID     int32
	ClaimID                 *int64
	Kind                    string
	Date                    time.Time
	Description             string
	Supplier                string
	SupplierInvoiceNumber   string
	NetAmount               string
	MarkupPercent           *string
	DistanceKm              *string
	BillRatePerKm           *string
	BillAmount, Currency    string
	SupplierInvoiceRebilled bool
}

// BillableMilestone is one billing milestone ready to invoice (status
// ready). Amount is its effective amount — a percent of the fixed price
// resolved by Projects — as decimal text with two decimals.
type BillableMilestone struct {
	ID                  int64
	Revision, ProjectID int32
	Name, Description   string
	PlannedDate         *time.Time
	ReadyAt             time.Time
	Amount, Currency    string
}

// BillableHoursPage is a billable read's answer; More says there was more
// than MaxBillableRows and the rest is not here.
type BillableHoursPage struct {
	Hours []BillableHour
	More  bool
}

// BillableExpensesPage is BillableHoursPage for expenses.
type BillableExpensesPage struct {
	Expenses []BillableExpense
	More     bool
}

// BillableMilestonesPage is BillableHoursPage for milestones.
type BillableMilestonesPage struct {
	Milestones []BillableMilestone
	More       bool
}

// BillableHours, BillableExpenses and BillableMilestones are the line-level
// reads an invoice is built from: work not yet invoiced, row by row, as the
// module that owns it judges it billable. Each is a single-provider slot,
// resolved after Projects, optional for its consumer (nil when the owning
// module is off). The rules a provider keeps:
//
//   - It reads on the pool, never under a lock of the caller's, and the
//     caller never calls it inside a transaction that holds one.
//   - It serves from its own tables and never calls a directory back while
//     serving: the project's facts are the caller's to read.
//   - It refuses a request Validate refuses, answers at most MaxBillableRows
//     rows, and sets More when there were more.
//   - Read by IDs it answers only the rows still billable; one since invoiced
//     or no longer invoiceable is absent, never an error.
//   - It fails rather than under-reports: an error means "the rows could not
//     be read", never "there are none".
type BillableHours interface {
	BillableHours(ctx context.Context, req BillableRequest) (BillableHoursPage, error)
}

// BillableExpenses is BillableHours for expense lines (see BillableHours).
type BillableExpenses interface {
	BillableExpenses(ctx context.Context, req BillableRequest) (BillableExpensesPage, error)
}

// BillableMilestones is BillableHours for billing milestones (see
// BillableHours).
type BillableMilestones interface {
	BillableMilestones(ctx context.Context, req BillableRequest) (BillableMilestonesPage, error)
}
