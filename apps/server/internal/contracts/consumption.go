package contracts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// WorkSourceSettlement is an energy settlement as an invoice's source (energy
// consumption billing design D6): stamped by Energy's InvoicedWorkHolder.
const WorkSourceSettlement WorkSourceKind = "energy.settlement"

// A settlement's kind: a regular one prices a billing period; a correction is
// the difference between that period recomputed and what was invoiced for it.
const (
	SettlementRegular    = "regular"
	SettlementCorrection = "correction"
)

// A settlement's state: a draft is recomputed on every read; an invoiced one
// is frozen with its invoice's stamp; a released one was returned in full by
// a credit note, and its period is drafted afresh.
const (
	SettlementDraft    = "draft"
	SettlementInvoiced = "invoiced"
	SettlementReleased = "released"
)

// Why a settlement is blocked, in the order they are judged: the first that
// holds is the one answered.
const (
	SettlementPeriodOpen       = "period_open"
	SettlementNoTariff         = "no_tariff"
	SettlementNoConsumption    = "no_consumption"
	SettlementIncomplete       = "incomplete"
	SettlementPriceGap         = "price_gap"
	SettlementPricePreliminary = "price_preliminary"
)

// A settlement line's unit: kilowatt-hours, months (prorated by civil days),
// years (prorated by days) or pieces.
const (
	SettlementUnitKWh   = "kWh"
	SettlementUnitMonth = "month"
	SettlementUnitYear  = "year"
	SettlementUnitPcs   = "pcs"
)

// A grid_energy line's band, when the component has a night rate.
const (
	SettlementBandDay   = "day"
	SettlementBandNight = "night"
)

// A settlement line's VAT hint (design D8): not a VAT code, which is the
// invoicing module's to choose from its settings.
const (
	VATHintStandard             = "standard"
	VATHintExemptHouseholdNorth = "exempt_household_north"
)

// A supply period's billing cadence.
const (
	CadenceMonthly   = "monthly"
	CadenceQuarterly = "quarterly"
	CadenceYearly    = "yearly"
)

// ConsumptionProvider is the energy module's read for invoicing: what a
// customer owes for its metering points, settled per supply period and
// billing period, priced by the module that owns the prices (design D1). It
// is a single-provider slot, resolved after the billable reads, optional for
// its consumer (nil when Energy is off). The rules a provider keeps:
//
//   - It reads and writes on the pool, never under a lock of the caller's,
//     and the caller never calls it inside a transaction that holds one.
//   - It never calls a directory while serving: the customer's facts are the
//     caller's to read.
//   - It refuses a request Validate refuses, answers at most MaxBillableRows
//     settlements and sets More when there were more.
//   - Read by ids it answers only settlements that exist; a missing id is
//     absent, never an error.
//   - It fails rather than under-reports: an error means "the settlements
//     could not be read", never "there are none".
//   - Settle writes: draft rows, idempotently, in short transactions of its
//     own — the one method of any contract that does, and it is named a
//     command for it (readings on the record 7). Settlements writes the same
//     way when a draft it recomputes has changed, so the revision it answers
//     is the one stored.
//   - A blocked settlement is still answered, with Blocked set and no lines,
//     so the caller can say why a point is not billed instead of skipping it
//     silently.
type ConsumptionProvider interface {
	// DueSupplies lists, per customer, the supply periods with at least one
	// billing period closed on or before Through and not yet invoiced,
	// without settling anything.
	DueSupplies(ctx context.Context, req DueSuppliesRequest) (DueSuppliesPage, error)
	// Settle computes (or recomputes, while still draft) the settlements of
	// the customers' or supply periods' billing periods closed on or before
	// Through, writes them as draft rows and answers them — regular ones for
	// periods never invoiced, corrections for invoiced periods whose figures
	// changed since.
	Settle(ctx context.Context, req SettleRequest) (SettlementsPage, error)
	// Settlements answers settlements by id as they stand now: a draft one
	// recomputed, an invoiced or released one as frozen. At most
	// MaxBillableRows ids (ValidateSettlementIDs).
	Settlements(ctx context.Context, ids []int64) (SettlementsPage, error)
}

// DueSuppliesRequest names the customers whose due supply periods are
// listed — or, with Every, every customer's: the billing run's read, which
// has no list of customers to name (billing runs plan reading 2). Through is
// an instant: a billing period is due when it ends on or before it (the
// invoicing module passes the start of the day after its "through" date).
type DueSuppliesRequest struct {
	CustomerIDs []int32
	Every       bool
	Through     time.Time
}

// Validate is the request's shape: exactly one of 1 to MaxActualsRequests
// customers, none twice, and Every; and a Through.
func (r DueSuppliesRequest) Validate() error {
	switch {
	case r.Every && len(r.CustomerIDs) > 0:
		return errors.New("contracts: a due-supplies request names customers and every customer")
	case !r.Every && len(r.CustomerIDs) == 0:
		return errors.New("contracts: a due-supplies request names no customer")
	case len(r.CustomerIDs) > MaxActualsRequests:
		return fmt.Errorf("contracts: a due-supplies request names %d customers, more than %d", len(r.CustomerIDs), MaxActualsRequests)
	case r.Through.IsZero():
		return errors.New("contracts: a due-supplies request has no through")
	}
	if id, ok := firstDuplicate(r.CustomerIDs); ok {
		return fmt.Errorf("contracts: a due-supplies request names customer %d twice", id)
	}
	return nil
}

// SettleRequest names what to settle: customers (every supply period of
// theirs) or supply periods, exactly one of the two, through Through (see
// DueSuppliesRequest).
type SettleRequest struct {
	CustomerIDs     []int32
	SupplyPeriodIDs []int32
	Through         time.Time
}

// Validate is the request's shape: exactly one of CustomerIDs (at most
// MaxActualsRequests) and SupplyPeriodIDs (at most MaxBillableRows), none
// named twice, and a Through.
func (r SettleRequest) Validate() error {
	switch {
	case len(r.CustomerIDs) == 0 && len(r.SupplyPeriodIDs) == 0:
		return errors.New("contracts: a settle request names no customer and no supply period")
	case len(r.CustomerIDs) > 0 && len(r.SupplyPeriodIDs) > 0:
		return errors.New("contracts: a settle request names both customers and supply periods")
	case len(r.CustomerIDs) > MaxActualsRequests:
		return fmt.Errorf("contracts: a settle request names %d customers, more than %d", len(r.CustomerIDs), MaxActualsRequests)
	case len(r.SupplyPeriodIDs) > MaxBillableRows:
		return fmt.Errorf("contracts: a settle request names %d supply periods, more than %d", len(r.SupplyPeriodIDs), MaxBillableRows)
	case r.Through.IsZero():
		return errors.New("contracts: a settle request has no through")
	}
	if id, ok := firstDuplicate(r.CustomerIDs); ok {
		return fmt.Errorf("contracts: a settle request names customer %d twice", id)
	}
	if id, ok := firstDuplicate(r.SupplyPeriodIDs); ok {
		return fmt.Errorf("contracts: a settle request names supply period %d twice", id)
	}
	return nil
}

// ValidateSettlementIDs is Settlements' request shape: 1 to MaxBillableRows
// ids, none twice.
func ValidateSettlementIDs(ids []int64) error {
	switch {
	case len(ids) == 0:
		return errors.New("contracts: a settlements read names no id")
	case len(ids) > MaxBillableRows:
		return fmt.Errorf("contracts: a settlements read names %d ids, more than %d", len(ids), MaxBillableRows)
	}
	if id, ok := firstDuplicate(ids); ok {
		return fmt.Errorf("contracts: a settlements read names id %d twice", id)
	}
	return nil
}

// DueSupply is one supply period with billing periods due and not invoiced.
type DueSupply struct {
	CustomerID, SupplyPeriodID, MeteringPointID int32
	GSRN, Address, PriceArea, ContractName      string
	// Cadence is monthly, quarterly or yearly.
	Cadence string
	// BilledThrough is the end of the last invoiced period, nil when none.
	BilledThrough *time.Time
	// DuePeriods is how many periods closed through Through are not invoiced.
	DuePeriods int
	// Blocked is "" or the first due period's blocker (Settlement.Blocked).
	Blocked string
}

// DueSuppliesPage is DueSupplies' answer; More says there was more than
// MaxBillableRows and the rest is not here.
type DueSuppliesPage struct {
	Supplies []DueSupply
	More     bool
}

// Settlement is what a customer owes for a metering point over one billing
// period of a supply period. Money and quantities are exact decimal text.
type Settlement struct {
	// ID is the stored row's; 0 for one computed without being written.
	ID       int64
	Revision int32
	// Kind is SettlementRegular or SettlementCorrection.
	Kind string
	// CorrectsID is a correction's invoiced original.
	CorrectsID                                  *int64
	SupplyPeriodID, MeteringPointID, CustomerID int32
	GSRN, Address, PriceArea                    string
	Household                                   bool
	// ContractName is the power tariff's name (§ 7-2 d).
	ContractName string
	// PeriodStart and PeriodEnd are half-open, the civil bounds of the
	// period in the point's market time zone, clipped to the supply period.
	PeriodStart, PeriodEnd time.Time
	// KWh has three decimals; a correction's is the difference.
	KWh string
	// Estimated is whether any interval is an estimate.
	Estimated bool
	// MissingIntervals is expected − present; 0 is complete.
	MissingIntervals int
	Currency         string
	Lines            []SettlementLine
	// Amount is Σ lines, two decimals; negative only on a correction.
	Amount string
	// Blocked is "" or one of the Settlement* blockers; a blocked settlement
	// has no lines. BlockedDetail is its sentence ("prices for 2026-10-14
	// are preliminary").
	Blocked, BlockedDetail string
	// State is draft, invoiced or released.
	State string
	// InvoicedBy is the stamp, nil while draft.
	InvoicedBy *InvoiceStampRef
}

// SettlementLine is one priced element of a settlement.
type SettlementLine struct {
	// Kind is the tariff component's kind: spot_energy, fixed_energy,
	// markup, monthly_fee, grid_energy, grid_capacity, grid_fixed,
	// electricity_tax or enova_levy.
	Kind string
	// Text is the component's own name, language-neutral.
	Text string
	// Band is day or night for a grid_energy component with a night rate,
	// "" otherwise.
	Band string
	// Month is the calendar month (its first day) a grid_capacity line
	// prices, nil otherwise.
	Month *time.Time
	// Quantity has three decimals: kWh, months, years, or 1.
	Quantity string
	// Unit is kWh, month, year or pcs.
	Unit string
	// UnitPrice has six decimals, in NOK.
	UnitPrice string
	// Amount has two decimals; negative allowed on a correction.
	Amount string
	// VATHint is standard or exempt_household_north (design D8).
	VATHint string
}

// InvoiceStampRef is the invoice a settlement was stamped by.
type InvoiceStampRef struct {
	InvoiceID, Number int64
	IssueDate         time.Time
	InvoicedAt        time.Time
	// InvoicedBy is the issuer, uuid.Nil when the directory did not know them.
	InvoicedBy uuid.UUID
	// ReleasedAt is when a credit note took the stamp back, nil while invoiced.
	ReleasedAt *time.Time
}

// SettlementsPage is Settle's and Settlements' answer; More says there was
// more than MaxBillableRows and the rest is not here.
type SettlementsPage struct {
	Settlements []Settlement
	More        bool
}
