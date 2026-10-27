package energy

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is a billing period priced (energy consumption billing design D5,
// energy settlements plan readings 3–6, 8): the period's intervals judged,
// the blockers in their order, and the correction of an invoiced period as
// the difference between it recomputed and what was invoiced. The lines
// themselves are settlement_pricing.go's.

// consumptionQualityEstimated is the interval quality that makes a settlement
// Estimated (plan reading 3: Corrected is a measurement, not an estimate).
const consumptionQualityEstimated = "Estimated"

// assumedIntervalLength is the expected unit of a period with no interval at
// all, when no finest length is present to judge by: an hour, the coarsest
// resolution the module stores, so the expected count is the period's hours.
const assumedIntervalLength = time.Hour

// settlementInterval is one current consumption interval of the point.
type settlementInterval struct {
	Start, End time.Time
	KWh        *big.Rat
	Estimated  bool
	// Day is the civil day (market zone) of Start, as civilDate answers it:
	// the day whose tariff version prices the interval (plan reading 5).
	Day time.Time
}

// computeSettlement prices one billing period of subj as the database q
// reads now (energy settlements plan readings 3–6): the regular settlement,
// ID 0, Revision 0, State draft, ComputedAt now, Currency NOK, every figure
// set, blocked with no lines when a blocker holds. Deterministic for a
// database state; q may be a pool's or a transaction's (the invoiced-work
// holder recomputes under its lock with the issue's clock as now).
//
// The blockers are judged in D5's order and the first that holds wins:
// period_open, no_tariff, no_consumption, incomplete, price_gap,
// price_preliminary. A blocked settlement carries its kWh and its counts as
// found, no lines and an amount of zero.
func computeSettlement(ctx context.Context, q *store.Queries, subj settlementSubject, period billingPeriod, now time.Time) (settlement, error) {
	out := settlement{
		Kind: contracts.SettlementRegular, State: contracts.SettlementDraft, ComputedAt: now,
		Currency:       settlementCurrency,
		SupplyPeriodID: subj.SupplyPeriodID, MeteringPointID: subj.MeteringPointID, CustomerID: subj.CustomerID,
		GSRN: subj.Gsrn, Address: subj.address(), PriceArea: subj.PriceArea, Household: subj.Household,
		ContractName: subj.ContractName,
		PeriodStart:  period.Start, PeriodEnd: period.End,
		KWh: zeroRat(), Amount: zeroRat(),
	}
	block := func(reason, detail string) (settlement, error) {
		out.Blocked, out.BlockedDetail = reason, detail
		out.Lines = nil
		out.Amount = zeroRat()
		return out, nil
	}

	rows, err := q.ListSettlementIntervals(ctx, store.ListSettlementIntervalsParams{
		MeteringPointID: subj.MeteringPointID, ToTs: period.End, FromTs: period.Start,
	})
	if err != nil {
		return settlement{}, fmt.Errorf("energy: read the intervals of metering point %d: %w", subj.MeteringPointID, err)
	}
	judged, err := judgeIntervals(rows, period, subj.Loc)
	if err != nil {
		return settlement{}, err
	}
	out.KWh = judged.kwh
	out.Estimated = judged.estimated
	out.IntervalCount, out.ExpectedIntervalCount = len(judged.inside), judged.expected

	if period.End.After(now) {
		return block(contracts.SettlementPeriodOpen, "the period ends "+formatInstant(period.End, subj.Loc))
	}

	// The tariffs (reading 5): the power tariff is required, the grid tariff
	// optional; each civil day the period touches needs a version of each.
	if subj.TariffID == nil {
		return block(contracts.SettlementNoTariff, "no power tariff")
	}
	days := touchedDays(period, subj.Loc)
	books := make([]tariffBook, 0, 2)
	power, err := loadTariffBook(ctx, q, *subj.TariffID, subj.ContractName, tariffRankPower)
	if err != nil {
		return settlement{}, err
	}
	books = append(books, power)
	if subj.GridTariffID != nil {
		t, err := q.GetTariff(ctx, *subj.GridTariffID)
		if err != nil {
			return settlement{}, fmt.Errorf("energy: read grid tariff %d: %w", *subj.GridTariffID, err)
		}
		grid, err := loadTariffBook(ctx, q, t.ID, t.Name, tariffRankGrid)
		if err != nil {
			return settlement{}, err
		}
		books = append(books, grid)
	}
	for i := range books {
		if day, ok := books[i].cover(days); !ok {
			return block(contracts.SettlementNoTariff, fmt.Sprintf("%s has no version on %s", books[i].Name, day.Format(time.DateOnly)))
		}
	}
	power = books[0] // with the days its versions cover

	if len(rows) == 0 {
		return block(contracts.SettlementNoConsumption, "no consumption in the period")
	}
	if judged.incomplete != "" {
		return block(contracts.SettlementIncomplete, judged.incomplete)
	}

	// The prices (reading 4), read only when a power version of the period
	// holds spot_energy, and judged only for the intervals such a version
	// prices.
	var costs []*big.Rat
	if power.pricesSpot() {
		priced := make([]bool, len(judged.inside))
		for i, iv := range judged.inside {
			priced[i] = power.versionHolds(power.dayVersion[iv.Day], componentSpotEnergy)
		}
		prices, err := q.ListSettlementSpotPrices(ctx, store.ListSettlementSpotPricesParams{
			PriceArea: subj.PriceArea, ToTs: period.End, FromTs: period.Start,
		})
		if err != nil {
			return settlement{}, fmt.Errorf("energy: read the spot prices of %s: %w", subj.PriceArea, err)
		}
		var res spotPricing
		costs, res, err = priceIntervals(judged.inside, priced, prices)
		if err != nil {
			return settlement{}, err
		}
		switch {
		case res.gapAt != nil:
			return block(contracts.SettlementPriceGap,
				fmt.Sprintf("no price for %s at %s", subj.PriceArea, formatInstant(*res.gapAt, subj.Loc)))
		case res.preliminaryAt != nil:
			return block(contracts.SettlementPricePreliminary,
				fmt.Sprintf("prices for %s are preliminary", civilDate(*res.preliminaryAt, subj.Loc).Format(time.DateOnly)))
		}
	}

	out.Lines, err = priceLines(books, judged.inside, costs, period, days, subj)
	if err != nil {
		return settlement{}, err
	}
	out.Amount = sumLineAmounts(out.Lines)
	return out, nil
}

// judgedIntervals is a period's intervals as reading 3 judges them.
type judgedIntervals struct {
	// inside is the intervals lying inside the period, by start.
	inside    []settlementInterval
	kwh       *big.Rat
	estimated bool
	expected  int
	// incomplete is the detail of the first reason the period is
	// incomplete, "" when it is not.
	incomplete string
}

// judgeIntervals reads rows (every current interval overlapping the period,
// by start then end) against period (plan reading 3). An interval inside
// counts; one overlapping a bound without lying inside makes the period
// incomplete, as do two inside that overlap each other, so no kWh is priced
// twice. The expected count is the count plus the missing ones,
// ceil((period length − Σ lengths) / the finest length present); a period
// with no interval expects one an hour (assumedIntervalLength).
func judgeIntervals(rows []store.ListSettlementIntervalsRow, period billingPeriod, loc *time.Location) (judgedIntervals, error) {
	out := judgedIntervals{kwh: zeroRat()}
	var crossing, overlap *time.Time
	var covered, finest time.Duration
	var lastEnd time.Time
	for _, r := range rows {
		if r.Start.Before(period.Start) || r.End.After(period.End) {
			if crossing == nil {
				bound := period.Start
				if r.End.After(period.End) && !r.Start.Before(period.Start) {
					bound = period.End
				}
				crossing = &bound
			}
			continue
		}
		kwh, err := ratFromNumeric(r.QuantityKwh)
		if err != nil {
			return judgedIntervals{}, fmt.Errorf("energy: the interval from %s: %w", r.Start.Format(time.RFC3339), err)
		}
		if len(out.inside) > 0 && r.Start.Before(lastEnd) && overlap == nil {
			at := r.Start
			overlap = &at
		}
		if r.End.After(lastEnd) {
			lastEnd = r.End
		}
		length := r.End.Sub(r.Start)
		covered += length
		if finest == 0 || length < finest {
			finest = length
		}
		out.kwh.Add(out.kwh, kwh)
		est := r.Quality == consumptionQualityEstimated
		out.estimated = out.estimated || est
		out.inside = append(out.inside, settlementInterval{
			Start: r.Start, End: r.End, KWh: kwh, Estimated: est, Day: civilDate(r.Start, loc),
		})
	}
	if finest == 0 {
		finest = assumedIntervalLength
	}
	missing := 0
	if gap := period.End.Sub(period.Start) - covered; gap > 0 {
		missing = int((gap + finest - 1) / finest)
	}
	out.expected = len(out.inside) + missing
	switch {
	case crossing != nil:
		out.incomplete = "an interval crosses " + formatInstant(*crossing, loc)
	case overlap != nil:
		out.incomplete = "intervals overlap at " + formatInstant(*overlap, loc)
	case missing > 0:
		out.incomplete = fmt.Sprintf("%d of %d intervals are missing", missing, out.expected)
	}
	return out, nil
}

// formatInstant is t as a detail names it: RFC 3339 in the market zone.
func formatInstant(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(time.RFC3339)
}

// touchedDays is every civil day (civilDate) the period touches, the first
// holding its start to the one holding its last instant: the days that need
// a tariff version (plan reading 5).
func touchedDays(period billingPeriod, loc *time.Location) []time.Time {
	var days []time.Time
	last := civilDate(period.End.Add(-time.Nanosecond), loc)
	for d := civilDate(period.Start, loc); !d.After(last); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}
	return days
}

// lineKey is what a correction sums and compares lines by (plan reading 8).
type lineKey struct {
	kind, text, band, unit string
	month                  time.Time
}

func keyOf(l settlementLine) lineKey {
	k := lineKey{kind: l.Kind, text: l.Text, band: l.Band, unit: l.Unit}
	if l.Month != nil {
		k.month = *l.Month
	}
	return k
}

// keyedLine is the lines of one key summed: quantity and amount, the first
// line's unit price, VAT hint and month.
type keyedLine struct {
	first            settlementLine
	quantity, amount *big.Rat
}

// sumByKey is lines summed per key, the keys in the order they first appear.
func sumByKey(lines []settlementLine, into map[lineKey]*keyedLine, order []lineKey) []lineKey {
	for _, l := range lines {
		k := keyOf(l)
		acc, ok := into[k]
		if !ok {
			acc = &keyedLine{first: l, quantity: zeroRat(), amount: zeroRat()}
			into[k] = acc
			order = append(order, k)
		}
		acc.quantity.Add(acc.quantity, l.Quantity)
		acc.amount.Add(acc.amount, l.Amount)
	}
	return order
}

// correctionOf is the correction of an invoiced period (plan reading 8):
// recomputed is the period computed now, baseline its invoiced regular
// settlement followed by its invoiced corrections. It answers the correction
// draft — Kind correction, CorrectsID the regular's id, the lines the
// differences per key, KWh and Amount the differences — and true, or false
// when recomputed is blocked or every difference is zero.
//
// Both sides are summed per key (kind, text, band, month, unit) — a version
// change gives one key two lines, and a baseline holds the regular and its
// corrections. The differences come in the recomputed order, then the keys
// only the baseline holds; a difference whose quantity and amount are both
// zero is left out. A line's unit price is the recomputed's first line of
// its key, the baseline's for a vanished key.
func correctionOf(recomputed settlement, baseline []settlement) (settlement, bool) {
	if recomputed.Blocked != "" || len(baseline) == 0 {
		return settlement{}, false
	}
	now := map[lineKey]*keyedLine{}
	nowOrder := sumByKey(recomputed.Lines, now, nil)
	was := map[lineKey]*keyedLine{}
	var wasOrder []lineKey
	baseKWh := zeroRat()
	for _, b := range baseline {
		wasOrder = sumByKey(b.Lines, was, wasOrder)
		if b.KWh != nil {
			baseKWh.Add(baseKWh, b.KWh)
		}
	}

	var lines []settlementLine
	diff := func(k lineKey) {
		cur, prev := now[k], was[k]
		line := settlementLine{Quantity: zeroRat(), Amount: zeroRat()}
		if cur != nil {
			line = cur.first
			line.Quantity = new(big.Rat).Set(cur.quantity)
			line.Amount = new(big.Rat).Set(cur.amount)
		} else {
			line = prev.first
			line.Quantity, line.Amount = zeroRat(), zeroRat()
		}
		if prev != nil {
			line.Quantity.Sub(line.Quantity, prev.quantity)
			line.Amount.Sub(line.Amount, prev.amount)
		}
		if line.Quantity.Sign() == 0 && line.Amount.Sign() == 0 {
			return
		}
		lines = append(lines, line)
	}
	for _, k := range nowOrder {
		diff(k)
	}
	for _, k := range wasOrder {
		if now[k] == nil {
			diff(k)
		}
	}
	if len(lines) == 0 {
		return settlement{}, false
	}

	out := recomputed
	out.ID, out.Revision = 0, 0
	out.Kind = contracts.SettlementCorrection
	correctsID := baseline[0].ID
	out.CorrectsID = &correctsID
	out.State = contracts.SettlementDraft
	out.Stamp = nil
	out.KWh = new(big.Rat).Sub(ratOrZero(recomputed.KWh), baseKWh)
	out.Lines = lines
	out.Amount = sumLineAmounts(lines)
	return out, true
}

// ratOrZero is v, or zero when nil.
func ratOrZero(v *big.Rat) *big.Rat {
	if v == nil {
		return zeroRat()
	}
	return v
}
