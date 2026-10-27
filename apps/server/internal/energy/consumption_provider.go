package energy

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.ConsumptionProvider (energy consumption
// billing design D1, energy settlements plan readings 7–9): what a customer
// owes for its metering points, settled per supply period and billing period.
// DueSupplies writes nothing; Settle and Settlements write draft rows, each in
// a short transaction of its own (writeSettlementDraft), so the revision they
// answer is the one stored. It never reads a directory.

// untouchedDraftAge is how long a draft nobody has read lives (plan reading
// 7): Energy cannot see which drafts an invoice holds, so a held draft is one
// Invoices' freshness reads keep touching.
const untouchedDraftAge = 30 * 24 * time.Hour

// correctionWindowMonths is how far back an invoiced period is still
// corrected (design D5): its end within the last twelve months.
const correctionWindowMonths = 12

// consumptionProvider is Energy's contracts.ConsumptionProvider, on the pool
// and the clock and nothing else.
type consumptionProvider struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

var _ contracts.ConsumptionProvider = (*consumptionProvider)(nil)

// newConsumptionProvider is Module's Consumption: built from d.Pool and
// d.Clock (plan reading 9).
func newConsumptionProvider(d module.Deps) contracts.ConsumptionProvider {
	return &consumptionProvider{pool: d.Pool, clock: d.Clock}
}

func (p *consumptionProvider) now() time.Time {
	if p.clock != nil {
		return p.clock().UTC()
	}
	return time.Now().UTC()
}

// duePeriodsOf is the supply period's billing periods closed on or before
// both through and now (plan reading 2); none for a cancelled one.
func duePeriodsOf(subj settlementSubject, through, now time.Time) ([]billingPeriod, error) {
	if subj.Status == statusCancelled {
		return nil, nil
	}
	if now.Before(through) {
		through = now
	}
	return billingPeriods(subj.SupplyStart, subj.SupplyEnd, subj.Cadence, subj.Loc, through)
}

// liveKey is a settlement's live key within one supply period: its period's
// start and its kind (ux_settlements_live).
type liveKey struct {
	start int64
	kind  string
}

func liveKeyOf(start time.Time, kind string) liveKey {
	return liveKey{start: start.UnixNano(), kind: kind}
}

// isLive is whether the state holds the live key (draft or invoiced).
func isLive(state string) bool {
	return state == contracts.SettlementDraft || state == contracts.SettlementInvoiced
}

// compareSettlements is the order every answer keeps: supply period, period
// start, regular before correction, then id.
func compareSettlements(a, b settlement) int {
	if c := cmp.Compare(a.SupplyPeriodID, b.SupplyPeriodID); c != 0 {
		return c
	}
	if c := a.PeriodStart.Compare(b.PeriodStart); c != 0 {
		return c
	}
	if c := cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func kindRank(kind string) int {
	if kind == contracts.SettlementRegular {
		return 0
	}
	return 1
}

// pageOf is the settlements in the answer's order as the contract carries
// them, at most MaxBillableRows, More set when there were more.
func pageOf(all []settlement) contracts.SettlementsPage {
	slices.SortFunc(all, compareSettlements)
	page := contracts.SettlementsPage{Settlements: make([]contracts.Settlement, 0, min(len(all), contracts.MaxBillableRows))}
	if len(all) > contracts.MaxBillableRows {
		all, page.More = all[:contracts.MaxBillableRows], true
	}
	for _, s := range all {
		page.Settlements = append(page.Settlements, s.toContract())
	}
	return page
}

// correctionBaseline is what was invoiced for the period of the regular
// settlement regularID (plan reading 8): the regular as invoiced, then its
// invoiced corrections in id order. Empty when the regular is no longer
// invoiced (released, or gone): nothing is left to correct.
func correctionBaseline(ctx context.Context, q *store.Queries, supplyPeriodID int32, regularID int64) ([]settlement, error) {
	rows, err := q.ListSettlementsBySupplyPeriods(ctx, []int32{supplyPeriodID})
	if err != nil {
		return nil, fmt.Errorf("energy: read the settlements of supply period %d: %w", supplyPeriodID, err)
	}
	return baselineFromRows(ctx, q, rows, regularID)
}

// baselineFromRows is correctionBaseline over rows already read (every
// settlement of the regular's supply period).
func baselineFromRows(ctx context.Context, q *store.Queries, rows []store.EnergySettlement, regularID int64) ([]settlement, error) {
	var regular *store.EnergySettlement
	var corrections []store.EnergySettlement
	for i, r := range rows {
		switch {
		case r.ID == regularID && r.Kind == contracts.SettlementRegular && r.State == contracts.SettlementInvoiced:
			regular = &rows[i]
		case r.Kind == contracts.SettlementCorrection && r.State == contracts.SettlementInvoiced &&
			r.CorrectsID != nil && *r.CorrectsID == regularID:
			corrections = append(corrections, r)
		}
	}
	if regular == nil {
		return nil, nil
	}
	slices.SortFunc(corrections, func(a, b store.EnergySettlement) int { return cmp.Compare(a.ID, b.ID) })
	return readSettlements(ctx, q, append([]store.EnergySettlement{*regular}, corrections...))
}

// recomputeDraft is a stored draft's figures as they stand now: a regular one
// computed over its period as stored; a correction recomputed against its
// baseline (correctionOf). It answers false when a correction has nothing
// left to correct — its recomputation equals what was invoiced, or its
// original is no longer invoiced. A correction whose recomputation is blocked
// is answered as stored with the blocker, no lines and a zero amount: a
// blocked recomputation writes no correction (plan reading 8). The answer
// keeps the stored row's id, revision and state.
func recomputeDraft(ctx context.Context, q *store.Queries, stored settlement, subj settlementSubject, now time.Time) (settlement, bool, error) {
	period := billingPeriod{Start: stored.PeriodStart, End: stored.PeriodEnd}
	computed, err := computeSettlement(ctx, q, subj, period, now)
	if err != nil {
		return settlement{}, false, err
	}
	if stored.Kind == contracts.SettlementCorrection {
		if stored.CorrectsID == nil {
			return settlement{}, false, fmt.Errorf("energy: correction %d names no original", stored.ID)
		}
		baseline, err := correctionBaseline(ctx, q, stored.SupplyPeriodID, *stored.CorrectsID)
		if err != nil {
			return settlement{}, false, err
		}
		if len(baseline) == 0 {
			return settlement{}, false, nil
		}
		if computed.Blocked != "" {
			blocked := stored
			blocked.Blocked, blocked.BlockedDetail = computed.Blocked, computed.BlockedDetail
			blocked.Lines, blocked.Amount = nil, zeroRat()
			return blocked, true, nil
		}
		correction, ok := correctionOf(computed, baseline)
		if !ok {
			return settlement{}, false, nil
		}
		computed = correction
	}
	computed.ID, computed.Revision, computed.State = stored.ID, stored.Revision, stored.State
	return computed, true, nil
}

// supplyPeriodIDs is the request's supply periods: the customers' (every
// non-cancelled one started before through) or the ones named.
func supplyPeriodIDsOf(ctx context.Context, q *store.Queries, customerIDs, supplyPeriodIDs []int32, through time.Time) ([]int32, error) {
	if len(customerIDs) == 0 {
		ids := slices.Clone(supplyPeriodIDs)
		slices.Sort(ids)
		return ids, nil
	}
	ids, err := q.ListSettlementSupplyPeriodIDsByCustomers(ctx, store.ListSettlementSupplyPeriodIDsByCustomersParams{
		CustomerIds: customerIDs, Through: through,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: read the customers' supply periods: %w", err)
	}
	return ids, nil
}

// settlementsBySupplyPeriod is every settlement of the supply periods,
// grouped by supply period.
func settlementsBySupplyPeriod(ctx context.Context, q *store.Queries, ids []int32) (map[int32][]store.EnergySettlement, error) {
	out := make(map[int32][]store.EnergySettlement, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.ListSettlementsBySupplyPeriods(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("energy: read the supply periods' settlements: %w", err)
	}
	for _, r := range rows {
		out[r.SupplyPeriodID] = append(out[r.SupplyPeriodID], r)
	}
	return out, nil
}

// DueSupplies lists the customers' supply periods with a billing period
// closed on or before Through and not invoiced (plan reading 9). It writes
// nothing: Blocked is the first such period computed in memory.
func (p *consumptionProvider) DueSupplies(ctx context.Context, req contracts.DueSuppliesRequest) (contracts.DueSuppliesPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.DueSuppliesPage{}, err
	}
	now := p.now()
	q := store.New(p.pool)
	ids, err := supplyPeriodIDsOf(ctx, q, req.CustomerIDs, nil, req.Through)
	if err != nil {
		return contracts.DueSuppliesPage{}, err
	}
	subjects, err := loadSettlementSubjects(ctx, q, ids)
	if err != nil {
		return contracts.DueSuppliesPage{}, err
	}
	stored, err := settlementsBySupplyPeriod(ctx, q, ids)
	if err != nil {
		return contracts.DueSuppliesPage{}, err
	}
	var supplies []contracts.DueSupply
	for _, id := range ids {
		subj, ok := subjects[id]
		if !ok {
			continue
		}
		periods, err := duePeriodsOf(subj, req.Through, now)
		if err != nil {
			return contracts.DueSuppliesPage{}, err
		}
		invoiced := map[liveKey]bool{}
		var billedThrough *time.Time
		for _, r := range stored[id] {
			if r.Kind != contracts.SettlementRegular || r.State != contracts.SettlementInvoiced {
				continue
			}
			invoiced[liveKeyOf(r.PeriodStart, r.Kind)] = true
			if billedThrough == nil || r.PeriodEnd.After(*billedThrough) {
				end := r.PeriodEnd
				billedThrough = &end
			}
		}
		var first *billingPeriod
		due := 0
		for i, period := range periods {
			if invoiced[liveKeyOf(period.Start, contracts.SettlementRegular)] {
				continue
			}
			if first == nil {
				first = &periods[i]
			}
			due++
		}
		if due == 0 {
			continue
		}
		computed, err := computeSettlement(ctx, q, subj, *first, now)
		if err != nil {
			return contracts.DueSuppliesPage{}, err
		}
		supplies = append(supplies, contracts.DueSupply{
			CustomerID: subj.CustomerID, SupplyPeriodID: subj.SupplyPeriodID, MeteringPointID: subj.MeteringPointID,
			GSRN: subj.Gsrn, Address: subj.address(), PriceArea: subj.PriceArea, ContractName: subj.ContractName,
			Cadence: subj.Cadence, BilledThrough: billedThrough, DuePeriods: due, Blocked: computed.Blocked,
		})
	}
	slices.SortFunc(supplies, func(a, b contracts.DueSupply) int {
		return cmp.Or(cmp.Compare(a.CustomerID, b.CustomerID), cmp.Compare(a.SupplyPeriodID, b.SupplyPeriodID))
	})
	page := contracts.DueSuppliesPage{Supplies: supplies}
	if len(supplies) > contracts.MaxBillableRows {
		page.Supplies, page.More = supplies[:contracts.MaxBillableRows], true
	}
	if page.Supplies == nil {
		page.Supplies = []contracts.DueSupply{}
	}
	return page, nil
}

// Settle writes and answers the drafts of the request's supply periods
// (plan readings 7, 8): a regular draft for every due period never invoiced,
// a correction draft for every invoiced period of the last twelve months
// whose figures changed since; then the housekeeping. It stops settling
// further supply periods once it holds more than MaxBillableRows answers.
func (p *consumptionProvider) Settle(ctx context.Context, req contracts.SettleRequest) (contracts.SettlementsPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.SettlementsPage{}, err
	}
	now := p.now()
	q := store.New(p.pool)
	ids, err := supplyPeriodIDsOf(ctx, q, req.CustomerIDs, req.SupplyPeriodIDs, req.Through)
	if err != nil {
		return contracts.SettlementsPage{}, err
	}
	subjects, err := loadSettlementSubjects(ctx, q, ids)
	if err != nil {
		return contracts.SettlementsPage{}, err
	}
	stored, err := settlementsBySupplyPeriod(ctx, q, ids)
	if err != nil {
		return contracts.SettlementsPage{}, err
	}
	var out []settlement
	for _, id := range ids {
		subj, ok := subjects[id]
		if !ok {
			continue // a supply period named that does not exist is absent
		}
		answered, err := p.settleSupplyPeriod(ctx, q, subj, stored[id], req.Through, now)
		if err != nil {
			return contracts.SettlementsPage{}, err
		}
		out = append(out, answered...)
		if len(out) > contracts.MaxBillableRows {
			break
		}
	}
	if _, err := q.DeleteUntouchedSettlementDrafts(ctx, now.Add(-untouchedDraftAge)); err != nil {
		return contracts.SettlementsPage{}, fmt.Errorf("energy: delete untouched settlement drafts: %w", err)
	}
	return pageOf(out), nil
}

// settleSupplyPeriod is Settle for one supply period: its regular drafts, its
// corrections and its own housekeeping — a draft whose period is no longer
// due, or a correction draft whose original is no longer corrected, is
// deleted. rows is every stored settlement of the supply period.
func (p *consumptionProvider) settleSupplyPeriod(ctx context.Context, q *store.Queries, subj settlementSubject,
	rows []store.EnergySettlement, through, now time.Time) ([]settlement, error) {
	periods, err := duePeriodsOf(subj, through, now)
	if err != nil {
		return nil, err
	}
	live := map[liveKey]store.EnergySettlement{}
	for _, r := range rows {
		if isLive(r.State) {
			live[liveKeyOf(r.PeriodStart, r.Kind)] = r
		}
	}
	var out []settlement
	keep := map[int64]bool{}
	for _, period := range periods {
		if r, ok := live[liveKeyOf(period.Start, contracts.SettlementRegular)]; ok && r.State == contracts.SettlementInvoiced {
			continue // an invoiced period changes only through a correction
		}
		computed, err := computeSettlement(ctx, q, subj, period, now)
		if err != nil {
			return nil, err
		}
		written, err := writeSettlementDraft(ctx, p.pool, computed, now)
		if err != nil {
			return nil, err
		}
		keep[written.ID] = true
		if written.State == contracts.SettlementDraft {
			out = append(out, written)
		}
	}
	if subj.Status != statusCancelled {
		corrections, err := p.settleCorrections(ctx, q, subj, rows, live, now, keep)
		if err != nil {
			return nil, err
		}
		out = append(out, corrections...)
	}
	for _, r := range rows {
		if r.State != contracts.SettlementDraft || keep[r.ID] {
			continue
		}
		if _, err := q.DeleteSettlementDraft(ctx, r.ID); err != nil {
			return nil, fmt.Errorf("energy: delete settlement draft %d: %w", r.ID, err)
		}
	}
	return out, nil
}

// settleCorrections writes the correction drafts of the supply period's
// invoiced periods (plan reading 8). A period is recomputed when a correction
// draft is already live for it or its inputs changed after the latest
// invoiced baseline's computation; a correction with every difference zero is
// not written (and a live draft of it is left to the housekeeping, by not
// being kept); a blocked recomputation writes nothing and keeps a live draft
// as it stands. keep collects the drafts that stay.
func (p *consumptionProvider) settleCorrections(ctx context.Context, q *store.Queries, subj settlementSubject,
	rows []store.EnergySettlement, live map[liveKey]store.EnergySettlement, now time.Time, keep map[int64]bool) ([]settlement, error) {
	windowStart := now.AddDate(0, -correctionWindowMonths, 0)
	var tariffIDs []int32
	for _, id := range []*int32{subj.TariffID, subj.GridTariffID} {
		if id != nil {
			tariffIDs = append(tariffIDs, *id)
		}
	}
	var out []settlement
	for _, r := range rows {
		if r.Kind != contracts.SettlementRegular || r.State != contracts.SettlementInvoiced || !r.PeriodEnd.After(windowStart) {
			continue
		}
		baseline, err := baselineFromRows(ctx, q, rows, r.ID)
		if err != nil {
			return nil, err
		}
		since := r.ComputedAt
		for _, b := range baseline {
			if b.ComputedAt.After(since) {
				since = b.ComputedAt
			}
		}
		draft, hasDraft := live[liveKeyOf(r.PeriodStart, contracts.SettlementCorrection)]
		hasDraft = hasDraft && draft.State == contracts.SettlementDraft
		if !hasDraft {
			changed, err := q.SettlementInputsChangedSince(ctx, store.SettlementInputsChangedSinceParams{
				MeteringPointID: subj.MeteringPointID, FromTs: r.PeriodStart, ToTs: r.PeriodEnd, Since: since,
				PriceArea: subj.PriceArea, TariffIds: tariffIDs,
			})
			if err != nil {
				return nil, fmt.Errorf("energy: judge whether settlement %d's inputs changed: %w", r.ID, err)
			}
			if !changed {
				continue
			}
		}
		computed, err := computeSettlement(ctx, q, subj, billingPeriod{Start: r.PeriodStart, End: r.PeriodEnd}, now)
		if err != nil {
			return nil, err
		}
		if computed.Blocked != "" {
			if hasDraft {
				keep[draft.ID] = true
			}
			continue
		}
		correction, ok := correctionOf(computed, baseline)
		if !ok {
			continue
		}
		written, err := writeSettlementDraft(ctx, p.pool, correction, now)
		if err != nil {
			return nil, err
		}
		keep[written.ID] = true
		if written.State == contracts.SettlementDraft {
			out = append(out, written)
		}
	}
	return out, nil
}

// Settlements answers settlements by id as they stand now (plan reading 7):
// a draft recomputed and written when a figure changed, so the revision
// answered is the one stored; an invoiced or released one as frozen; a
// missing id, or a correction draft with nothing left to correct (deleted
// here), absent.
func (p *consumptionProvider) Settlements(ctx context.Context, ids []int64) (contracts.SettlementsPage, error) {
	if err := contracts.ValidateSettlementIDs(ids); err != nil {
		return contracts.SettlementsPage{}, err
	}
	now := p.now()
	q := store.New(p.pool)
	rows, err := q.ListSettlementsByIDs(ctx, ids)
	if err != nil {
		return contracts.SettlementsPage{}, fmt.Errorf("energy: read settlements by id: %w", err)
	}
	stored, err := readSettlements(ctx, q, rows)
	if err != nil {
		return contracts.SettlementsPage{}, err
	}
	periodIDs := make([]int32, 0, len(rows))
	for _, r := range rows {
		if r.State == contracts.SettlementDraft && !slices.Contains(periodIDs, r.SupplyPeriodID) {
			periodIDs = append(periodIDs, r.SupplyPeriodID)
		}
	}
	subjects, err := loadSettlementSubjects(ctx, q, periodIDs)
	if err != nil {
		return contracts.SettlementsPage{}, err
	}
	out := make([]settlement, 0, len(stored))
	for _, s := range stored {
		if s.State != contracts.SettlementDraft {
			out = append(out, s)
			continue
		}
		current, ok, err := recomputeDraft(ctx, q, s, subjects[s.SupplyPeriodID], now)
		if err != nil {
			return contracts.SettlementsPage{}, err
		}
		if !ok {
			if _, err := q.DeleteSettlementDraft(ctx, s.ID); err != nil {
				return contracts.SettlementsPage{}, fmt.Errorf("energy: delete settlement draft %d: %w", s.ID, err)
			}
			continue
		}
		if s.Kind == contracts.SettlementCorrection && current.Blocked != "" {
			out = append(out, current) // a blocked correction is not written
			continue
		}
		current.ID, current.Revision, current.State = 0, 0, contracts.SettlementDraft
		written, err := writeSettlementDraft(ctx, p.pool, current, now)
		if err != nil {
			return contracts.SettlementsPage{}, err
		}
		out = append(out, written)
	}
	return pageOf(out), nil
}
