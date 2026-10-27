package invoices_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// fakeConsumption is Energy's consumption reads (contracts.ConsumptionProvider)
// over settlements and due supplies a test puts in, for the From consumption
// wizard, a held settlement's freshness and refresh and the issue, without
// composing energy (depguard keeps it out of this package's tests). Every
// call is recorded with what it asked for and whether it was made inside one
// of this module's locked transactions — which it never may be. Settle
// answers every draft settlement of the request's customers (or supply
// periods) whose period ends on or before Through; Settlements answers by id
// whatever state a settlement is in; DueSupplies with Every answers every
// supply put in. Safe for concurrent use.
type fakeConsumption struct {
	mu          sync.Mutex
	settlements map[int64]contracts.Settlement
	supplies    []contracts.DueSupply
	calls       []consumptionCall
	// more is what every page answers as More.
	more bool
	// settleErr, when set, is what every Settle fails with.
	settleErr error
	// onSettle, when set, runs at the start of every Settle, after the
	// record — what happens while a billing run item is being carried.
	onSettle func()
}

// consumptionCall is one call: which, for whom, and whether under a lock.
type consumptionCall struct {
	method    string
	customers []int32
	ids       []int64
	through   time.Time
	locked    bool
}

var _ contracts.ConsumptionProvider = (*fakeConsumption)(nil)

func newFakeConsumption() *fakeConsumption {
	return &fakeConsumption{settlements: map[int64]contracts.Settlement{}}
}

// options composes the fake as the Consumption slot.
func (f *fakeConsumption) options() []modtest.Option {
	return []modtest.Option{modtest.WithConsumption(f)}
}

// put stores s as Energy answers it now; drop forgets it (Energy deleted
// the draft).
func (f *fakeConsumption) put(s contracts.Settlement) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settlements[s.ID] = s
}

func (f *fakeConsumption) drop(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.settlements, id)
}

// get answers a stored settlement as it stands.
func (f *fakeConsumption) get(id int64) (contracts.Settlement, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.settlements[id]
	return s, ok
}

// putSupply adds a due supply DueSupplies answers.
func (f *fakeConsumption) putSupply(d contracts.DueSupply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.supplies = append(f.supplies, d)
}

// answerMore makes every page say More.
func (f *fakeConsumption) answerMore(more bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.more = more
}

// failSettle makes every Settle fail with err; nil lets them through.
func (f *fakeConsumption) failSettle(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settleErr = err
}

// duringSettle sets onSettle.
func (f *fakeConsumption) duringSettle(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onSettle = fn
}

// reads is every call made so far.
func (f *fakeConsumption) reads() []consumptionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeConsumption) record(ctx context.Context, c consumptionCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.locked = invoices.InLockedTx(ctx)
	f.calls = append(f.calls, c)
}

func (f *fakeConsumption) DueSupplies(ctx context.Context, req contracts.DueSuppliesRequest) (contracts.DueSuppliesPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.DueSuppliesPage{}, err
	}
	f.record(ctx, consumptionCall{method: "DueSupplies", customers: slices.Clone(req.CustomerIDs), through: req.Through})
	f.mu.Lock()
	defer f.mu.Unlock()
	page := contracts.DueSuppliesPage{Supplies: []contracts.DueSupply{}, More: f.more}
	for _, d := range f.supplies {
		if req.Every || slices.Contains(req.CustomerIDs, d.CustomerID) {
			page.Supplies = append(page.Supplies, d)
		}
	}
	return page, nil
}

func (f *fakeConsumption) Settle(ctx context.Context, req contracts.SettleRequest) (contracts.SettlementsPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.SettlementsPage{}, err
	}
	f.record(ctx, consumptionCall{method: "Settle", customers: slices.Clone(req.CustomerIDs), through: req.Through})
	f.mu.Lock()
	during := f.onSettle
	f.mu.Unlock()
	if during != nil {
		during()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settleErr != nil {
		return contracts.SettlementsPage{}, f.settleErr
	}
	page := contracts.SettlementsPage{Settlements: []contracts.Settlement{}, More: f.more}
	for _, s := range f.settlements {
		mine := slices.Contains(req.CustomerIDs, s.CustomerID) || slices.Contains(req.SupplyPeriodIDs, s.SupplyPeriodID)
		if mine && s.State == contracts.SettlementDraft && !s.PeriodEnd.After(req.Through) {
			page.Settlements = append(page.Settlements, s)
		}
	}
	slices.SortFunc(page.Settlements, func(a, b contracts.Settlement) int { return int(a.ID - b.ID) })
	return page, nil
}

func (f *fakeConsumption) Settlements(ctx context.Context, ids []int64) (contracts.SettlementsPage, error) {
	if err := contracts.ValidateSettlementIDs(ids); err != nil {
		return contracts.SettlementsPage{}, err
	}
	f.record(ctx, consumptionCall{method: "Settlements", ids: slices.Clone(ids)})
	f.mu.Lock()
	defer f.mu.Unlock()
	page := contracts.SettlementsPage{Settlements: []contracts.Settlement{}, More: f.more}
	for _, id := range ids {
		if s, ok := f.settlements[id]; ok {
			page.Settlements = append(page.Settlements, s)
		}
	}
	return page, nil
}

// marketDay is midnight of a civil day in the power market's zone, as
// Energy's period bounds are.
func marketDay(y int, m time.Month, d int) time.Time {
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		panic(err)
	}
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// monthSettlement is a regular draft settlement of customer for one civil
// month of a point: spot energy, day grid energy and a monthly fee, every
// line at hint, its lines' amounts summing to its amount.
func monthSettlement(id int64, customer, supply int32, gsrn string, year int, month time.Month, hint string) contracts.Settlement {
	return contracts.Settlement{
		ID: id, Revision: 1, Kind: contracts.SettlementRegular, SupplyPeriodID: supply, MeteringPointID: supply + 100,
		CustomerID: customer, GSRN: gsrn, Address: "Storgata 1, 9008 Tromsø", PriceArea: "NO4",
		Household: hint == contracts.VATHintExemptHouseholdNorth, ContractName: "Spot Nord",
		PeriodStart: marketDay(year, month, 1), PeriodEnd: marketDay(year, month+1, 1),
		KWh: "1500.250", Currency: "NOK", Amount: "1187.07", State: contracts.SettlementDraft,
		Lines: []contracts.SettlementLine{
			{Kind: "spot_energy", Text: "Spot NO4", Quantity: "1500.250", Unit: contracts.SettlementUnitKWh, UnitPrice: "0.512345", Amount: "768.65", VATHint: hint},
			{Kind: "grid_energy", Text: "Energiledd", Band: contracts.SettlementBandDay, Quantity: "1000.125", Unit: contracts.SettlementUnitKWh, UnitPrice: "0.333333", Amount: "333.38", VATHint: hint},
			{Kind: "monthly_fee", Text: "Fastledd", Quantity: "1.000", Unit: contracts.SettlementUnitMonth, UnitPrice: "85.040000", Amount: "85.04", VATHint: hint},
		},
	}
}
