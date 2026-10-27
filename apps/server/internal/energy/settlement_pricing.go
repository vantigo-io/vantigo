package energy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is a settlement's prices and lines (energy consumption billing
// design D3's kinds table, D5, energy settlements plan readings 4–6): the
// tariff versions by civil day, the spot prices time-weighted over each
// interval, and one line per (version, component) — two for grid_energy with
// a night rate, one per calendar month for grid_capacity — in the kinds
// table's order.

// A tariff's rank in the line order: power before grid within monthly_fee,
// the one kind both may hold (reading 6).
const (
	tariffRankPower = 0
	tariffRankGrid  = 1
)

// componentKindRank is each kind's place in the kinds table (design D3),
// the first key of the line order.
var componentKindRank = func() map[string]int {
	m := make(map[string]int, len(componentKinds))
	for i, k := range componentKinds {
		m[k] = i
	}
	return m
}()

// tariffBook is one tariff as a settlement reads it: its versions by
// valid_from, each with its components by position, and which version
// prices each civil day of the period.
type tariffBook struct {
	ID       int32
	Name     string
	Rank     int
	Versions []tariffBookVersion
	// dayVersion is the index into Versions of the version valid on each
	// civil day of the period (cover fills it).
	dayVersion map[time.Time]int
}

type tariffBookVersion struct {
	ValidFrom  time.Time
	ValidTo    *time.Time
	Components []store.EnergyTariffComponent
}

// loadTariffBook reads tariff id's versions and components, as of the
// computation: an archived tariff still prices (reading 5).
func loadTariffBook(ctx context.Context, q *store.Queries, id int32, name string, rank int) (tariffBook, error) {
	versions, err := q.ListTariffVersions(ctx, id)
	if err != nil {
		return tariffBook{}, fmt.Errorf("energy: read the versions of tariff %d: %w", id, err)
	}
	components, err := q.ListTariffComponents(ctx, id)
	if err != nil {
		return tariffBook{}, fmt.Errorf("energy: read the components of tariff %d: %w", id, err)
	}
	book := tariffBook{ID: id, Name: name, Rank: rank, Versions: make([]tariffBookVersion, len(versions))}
	index := make(map[int32]int, len(versions))
	for i, v := range versions {
		bv := tariffBookVersion{ValidFrom: v.ValidFrom.Time}
		if v.ValidTo.Valid {
			to := v.ValidTo.Time
			bv.ValidTo = &to
		}
		book.Versions[i] = bv
		index[v.ID] = i
	}
	for _, c := range components {
		if i, ok := index[c.VersionID]; ok {
			book.Versions[i].Components = append(book.Versions[i].Components, c)
		}
	}
	return book, nil
}

// cover finds the version valid on each of days ([valid_from, valid_to)),
// answering the first day none is valid on and false when there is one.
func (b *tariffBook) cover(days []time.Time) (time.Time, bool) {
	b.dayVersion = make(map[time.Time]int, len(days))
	for _, d := range days {
		found := -1
		for i, v := range b.Versions {
			if !d.Before(v.ValidFrom) && (v.ValidTo == nil || d.Before(*v.ValidTo)) {
				found = i
				break
			}
		}
		if found < 0 {
			return d, false
		}
		b.dayVersion[d] = found
	}
	return time.Time{}, true
}

// versionHolds is whether version i holds a component of kind.
func (b tariffBook) versionHolds(i int, kind string) bool {
	for _, c := range b.Versions[i].Components {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

// pricesSpot is whether a version of the period holds spot_energy: only
// then are prices read (reading 4).
func (b tariffBook) pricesSpot() bool {
	for _, i := range b.dayVersion {
		if b.versionHolds(i, componentSpotEnergy) {
			return true
		}
	}
	return false
}

// spotPricing is what judging the prices found: the first instant no price
// covers, and the start of the first preliminary price used.
type spotPricing struct {
	gapAt, preliminaryAt *time.Time
}

// priceIntervals prices each interval that priced marks by the spot prices
// covering it, time-weighted: its kWh spread evenly over its length, each
// part at the price of the price interval holding it (reading 4). When one
// price interval holds the whole consumption interval that is exactly its
// price; when prices are finer the interval takes their mean. costs[i] is
// the exact NOK of interval i, nil when it is not priced. Only final prices
// price (spot-price service design S10): a preliminary one is reported, as
// is the first instant no price covers.
func priceIntervals(intervals []settlementInterval, priced []bool, rows []store.ListSettlementSpotPricesRow) ([]*big.Rat, spotPricing, error) {
	type spotPrice struct {
		start, end  time.Time
		price       *big.Rat
		preliminary bool
	}
	prices := make([]spotPrice, 0, len(rows))
	for _, r := range rows {
		p, err := ratFromNumeric(r.NokPerKwh)
		if err != nil {
			return nil, spotPricing{}, fmt.Errorf("energy: the spot price from %s: %w", r.Start.Format(time.RFC3339), err)
		}
		prices = append(prices, spotPrice{start: r.Start, end: r.End, price: p, preliminary: r.State != "final"})
	}

	var res spotPricing
	costs := make([]*big.Rat, len(intervals))
	j := 0
	for i, iv := range intervals {
		if !priced[i] {
			continue
		}
		for j < len(prices) && !prices[j].end.After(iv.Start) {
			j++
		}
		length := big.NewInt(int64(iv.End.Sub(iv.Start)))
		cost := zeroRat()
		cursor := iv.Start
		for k := j; k < len(prices) && prices[k].start.Before(iv.End); k++ {
			p := prices[k]
			if p.start.After(cursor) {
				break // a gap at cursor
			}
			end := p.end
			if end.After(iv.End) {
				end = iv.End
			}
			if p.preliminary && res.preliminaryAt == nil {
				at := p.start
				res.preliminaryAt = &at
			}
			share := new(big.Rat).SetFrac(big.NewInt(int64(end.Sub(cursor))), length)
			part := new(big.Rat).Mul(iv.KWh, share)
			cost.Add(cost, part.Mul(part, p.price))
			cursor = end
		}
		if cursor.Before(iv.End) {
			if res.gapAt == nil {
				at := cursor
				res.gapAt = &at
			}
			continue
		}
		costs[i] = cost
	}
	return costs, res, nil
}

// lineSlot is one line being summed, keyed by tariff, version, component,
// band and month.
type lineSlot struct {
	rank, version, component int
	band                     string
	month                    time.Time
}

// lineAcc is a line's figures as they are summed.
type lineAcc struct {
	slot      lineSlot
	validFrom time.Time
	comp      store.EnergyTariffComponent
	kwh, cost *big.Rat
	// monthDays is the counted days per calendar month (its first day),
	// yearDays per calendar year: the proration of a monthly or yearly figure.
	monthDays map[time.Time]int
	yearDays  map[int]int
	// hours is grid_capacity's kWh per clock hour.
	hours map[time.Time]*big.Rat
}

// priceLines is the lines of an unblocked period (reading 6): every
// component of every version valid on a day the period touches, summed over
// the intervals of its days (kWh) and over the period's counted days
// (proration), each rounded and in the line order. costs is each interval's
// spot cost (priceIntervals), nil when no version prices spot. A prorated
// line (by the month or the year) of quantity and amount zero — a period
// counting no day of it — is left out.
func priceLines(books []tariffBook, intervals []settlementInterval, costs []*big.Rat, period billingPeriod, days []time.Time, subj settlementSubject) ([]settlementLine, error) {
	accs := map[lineSlot]*lineAcc{}
	get := func(b tariffBook, v, c int, band string, month time.Time) *lineAcc {
		slot := lineSlot{rank: b.Rank, version: v, component: c, band: band, month: month}
		acc, ok := accs[slot]
		if !ok {
			acc = &lineAcc{
				slot: slot, validFrom: b.Versions[v].ValidFrom, comp: b.Versions[v].Components[c],
				kwh: zeroRat(), cost: zeroRat(),
				monthDays: map[time.Time]int{}, yearDays: map[int]int{}, hours: map[time.Time]*big.Rat{},
			}
			accs[slot] = acc
		}
		return acc
	}
	// bandsOf is a component's bands: day and night for grid_energy with a
	// night rate, none otherwise.
	bandsOf := func(c store.EnergyTariffComponent) []string {
		if c.Kind == componentGridEnergy && c.RateNight.Valid {
			return []string{contracts.SettlementBandDay, contracts.SettlementBandNight}
		}
		return []string{""}
	}
	// priced is whether the component makes a line for this point at all.
	priced := func(c store.EnergyTariffComponent) bool {
		return c.Kind != componentElectricityTax || subj.ElectricityTaxClass != "exempt"
	}

	// Every component of every version valid on a touched day has its line
	// (lines), grid_capacity one per calendar month of those days.
	for _, b := range books {
		for _, d := range days {
			v := b.dayVersion[d]
			for c, comp := range b.Versions[v].Components {
				if !priced(comp) {
					continue
				}
				var month time.Time
				if comp.Kind == componentGridCapacity {
					month = monthOf(d)
				}
				for _, band := range bandsOf(comp) {
					get(b, v, c, band, month)
				}
			}
		}
	}

	// The kWh, by the version of each interval's day.
	for i, iv := range intervals {
		hour := iv.Start.Truncate(time.Hour)
		for _, b := range books {
			v := b.dayVersion[iv.Day]
			for c, comp := range b.Versions[v].Components {
				if !priced(comp) {
					continue
				}
				switch comp.Kind {
				case componentSpotEnergy:
					acc := get(b, v, c, "", time.Time{})
					acc.kwh.Add(acc.kwh, iv.KWh)
					if costs != nil && costs[i] != nil {
						acc.cost.Add(acc.cost, costs[i])
					}
				case componentFixedEnergy, componentMarkup, componentElectricityTax:
					acc := get(b, v, c, "", time.Time{})
					acc.kwh.Add(acc.kwh, iv.KWh)
				case componentEnovaLevy:
					if subj.Household {
						acc := get(b, v, c, "", time.Time{})
						acc.kwh.Add(acc.kwh, iv.KWh)
					}
				case componentGridEnergy:
					band := ""
					if comp.RateNight.Valid {
						band = gridEnergyBand(comp, iv.Start.In(subj.Loc))
					}
					acc := get(b, v, c, band, time.Time{})
					acc.kwh.Add(acc.kwh, iv.KWh)
				case componentGridCapacity:
					acc := get(b, v, c, "", monthOf(iv.Day))
					sum, ok := acc.hours[hour]
					if !ok {
						sum = zeroRat()
						acc.hours[hour] = sum
					}
					sum.Add(sum, iv.KWh)
				}
			}
		}
	}

	// The proration, by the counted days: a day counts for the period
	// holding its midnight (reading 5).
	for _, d := range countedDays(period.Start, period.End, subj.Loc) {
		for _, b := range books {
			v := b.dayVersion[d]
			for c, comp := range b.Versions[v].Components {
				switch {
				case comp.Kind == componentMonthlyFee || comp.Kind == componentGridFixed:
					get(b, v, c, "", time.Time{}).monthDays[monthOf(d)]++
				case comp.Kind == componentGridCapacity:
					get(b, v, c, "", monthOf(d)).monthDays[monthOf(d)]++
				case comp.Kind == componentEnovaLevy && !subj.Household:
					get(b, v, c, "", time.Time{}).yearDays[d.Year()]++
				}
			}
		}
	}

	ordered := make([]*lineAcc, 0, len(accs))
	for _, acc := range accs {
		ordered = append(ordered, acc)
	}
	sort.Slice(ordered, func(i, j int) bool { return lineBefore(ordered[i], ordered[j]) })

	hint := subj.vatHint()
	lines := make([]settlementLine, 0, len(ordered))
	for _, acc := range ordered {
		line, err := acc.line(subj)
		if err != nil {
			return nil, err
		}
		if (line.Unit == contracts.SettlementUnitMonth || line.Unit == contracts.SettlementUnitYear) &&
			line.Quantity.Sign() == 0 && line.Amount.Sign() == 0 {
			continue // a prorated line with no counted day (a first period starting after midnight) says nothing
		}
		line.VATHint = hint
		lines = append(lines, line)
	}
	return lines, nil
}

// lineBefore is the line order (reading 6): the kinds table, power before
// grid, the version's valid_from, the component's position, day before
// night, the month.
func lineBefore(a, b *lineAcc) bool {
	if ka, kb := componentKindRank[a.comp.Kind], componentKindRank[b.comp.Kind]; ka != kb {
		return ka < kb
	}
	if a.slot.rank != b.slot.rank {
		return a.slot.rank < b.slot.rank
	}
	if !a.validFrom.Equal(b.validFrom) {
		return a.validFrom.Before(b.validFrom)
	}
	if a.comp.Position != b.comp.Position {
		return a.comp.Position < b.comp.Position
	}
	if a.slot.band != b.slot.band {
		return a.slot.band == contracts.SettlementBandDay
	}
	return a.slot.month.Before(b.slot.month)
}

// gridEnergyBand is the band of an interval starting at local (reading 6):
// day when its hour is in [day_from, day_to), except on Saturday and Sunday
// when weekdays_only, which are night throughout; night otherwise.
func gridEnergyBand(c store.EnergyTariffComponent, local time.Time) string {
	if c.WeekdaysOnly && (local.Weekday() == time.Saturday || local.Weekday() == time.Sunday) {
		return contracts.SettlementBandNight
	}
	if c.DayFrom != nil && c.DayTo != nil && local.Hour() >= int(*c.DayFrom) && local.Hour() < int(*c.DayTo) {
		return contracts.SettlementBandDay
	}
	return contracts.SettlementBandNight
}

// monthOf is the first day of d's calendar month (d a civilDate).
func monthOf(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// line is the summed figures as a line, rounded (reading 6): quantities
// three decimals — a prorated quantity before its amount is taken, so
// quantity × unit price is the amount printed —, unit prices six, amounts
// two, half away from zero.
func (acc *lineAcc) line(subj settlementSubject) (settlementLine, error) {
	c := acc.comp
	line := settlementLine{Kind: c.Kind, Text: c.Name, Band: acc.slot.band}
	rateOf := func(n string, field pgtype.Numeric) (*big.Rat, error) {
		v, err := ratFromNumeric(field)
		if err != nil {
			return nil, fmt.Errorf("energy: component %d (%s)'s %s: %w", c.ID, c.Kind, n, err)
		}
		return v, nil
	}
	perKWh := func(rate *big.Rat) settlementLine {
		line.Quantity = roundRat(acc.kwh, quantityPlaces)
		line.Unit = contracts.SettlementUnitKWh
		line.UnitPrice = roundRat(rate, unitPricePlaces)
		line.Amount = roundRat(new(big.Rat).Mul(acc.kwh, rate), amountPlaces)
		return line
	}
	prorated := func(quantity, rate *big.Rat, unit string) settlementLine {
		line.Quantity = roundRat(quantity, quantityPlaces)
		line.Unit = unit
		line.UnitPrice = roundRat(rate, unitPricePlaces)
		line.Amount = roundRat(new(big.Rat).Mul(line.Quantity, rate), amountPlaces)
		return line
	}

	switch c.Kind {
	case componentSpotEnergy:
		line.Quantity = roundRat(acc.kwh, quantityPlaces)
		line.Unit = contracts.SettlementUnitKWh
		line.Amount = roundRat(acc.cost, amountPlaces)
		line.UnitPrice = zeroRat()
		if acc.kwh.Sign() != 0 {
			line.UnitPrice = roundRat(new(big.Rat).Quo(acc.cost, acc.kwh), unitPricePlaces)
		}
		return line, nil
	case componentFixedEnergy, componentMarkup:
		rate, err := rateOf("rate", c.Rate)
		if err != nil {
			return settlementLine{}, err
		}
		return perKWh(rate), nil
	case componentGridEnergy:
		field, n := c.Rate, "rate"
		if acc.slot.band == contracts.SettlementBandNight {
			field, n = c.RateNight, "night rate"
		}
		rate, err := rateOf(n, field)
		if err != nil {
			return settlementLine{}, err
		}
		return perKWh(rate), nil
	case componentElectricityTax:
		field, n := c.Rate, "rate"
		if subj.ElectricityTaxClass == "reduced" {
			field, n = c.RateReduced, "reduced rate"
		}
		rate, err := rateOf(n, field)
		if err != nil {
			return settlementLine{}, err
		}
		return perKWh(rate), nil
	case componentEnovaLevy:
		if subj.Household {
			rate, err := rateOf("rate", c.Rate)
			if err != nil {
				return settlementLine{}, err
			}
			return perKWh(rate), nil
		}
		rate, err := rateOf("business rate", c.RateBusiness)
		if err != nil {
			return settlementLine{}, err
		}
		years := zeroRat()
		for y, n := range acc.yearDays {
			years.Add(years, big.NewRat(int64(n), int64(daysInYear(time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)))))
		}
		return prorated(years, rate, contracts.SettlementUnitYear), nil
	case componentMonthlyFee, componentGridFixed:
		rate, err := rateOf("rate", c.Rate)
		if err != nil {
			return settlementLine{}, err
		}
		return prorated(acc.months(), rate, contracts.SettlementUnitMonth), nil
	case componentGridCapacity:
		tiers, err := capacityTiers(c.Tiers)
		if err != nil {
			return settlementLine{}, fmt.Errorf("energy: component %d's tiers: %w", c.ID, err)
		}
		month := acc.slot.month
		line.Month = &month
		return prorated(acc.months(), tierFor(tiers, acc.capacityAverage(subj.Loc)), contracts.SettlementUnitMonth), nil
	}
	return settlementLine{}, fmt.Errorf("energy: component %d has the unknown kind %q", c.ID, c.Kind)
}

// months is the months covered: Σ per calendar month of its counted days ÷
// its days.
func (acc *lineAcc) months() *big.Rat {
	sum := zeroRat()
	for m, n := range acc.monthDays {
		sum.Add(sum, big.NewRat(int64(n), int64(daysInMonth(m))))
	}
	return sum
}

// capacityAverage is grid_capacity's measure (design D3, R5 §5.4): the kWh
// per clock hour (sub-hour intervals summed, and kWh in an hour is kW), the
// highest hour of each civil day, the three highest days averaged — fewer
// when the month's segment has fewer days.
func (acc *lineAcc) capacityAverage(loc *time.Location) *big.Rat {
	daily := map[time.Time]*big.Rat{}
	for hour, kwh := range acc.hours {
		d := civilDate(hour, loc)
		if top, ok := daily[d]; !ok || kwh.Cmp(top) > 0 {
			daily[d] = kwh
		}
	}
	maxima := make([]*big.Rat, 0, len(daily))
	for _, v := range daily {
		maxima = append(maxima, v)
	}
	sort.Slice(maxima, func(i, j int) bool { return maxima[i].Cmp(maxima[j]) > 0 })
	if len(maxima) > 3 {
		maxima = maxima[:3]
	}
	sum := zeroRat()
	for _, v := range maxima {
		sum.Add(sum, v)
	}
	if len(maxima) == 0 {
		return sum
	}
	return sum.Quo(sum, big.NewRat(int64(len(maxima)), 1))
}

// capacityTier is one tier of grid_capacity, exact.
type capacityTier struct {
	fromKW, perMonth *big.Rat
}

// capacityTiers reads the tiers jsonb ([{"fromKw":0,"perMonth":135}, …]) as
// exact decimals.
func capacityTiers(raw []byte) ([]capacityTier, error) {
	var tiers []struct {
		FromKw   json.Number `json:"fromKw"`
		PerMonth json.Number `json:"perMonth"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&tiers); err != nil {
		return nil, err
	}
	if len(tiers) == 0 {
		return nil, fmt.Errorf("no tiers")
	}
	out := make([]capacityTier, 0, len(tiers))
	for _, t := range tiers {
		from, ok1 := new(big.Rat).SetString(t.FromKw.String())
		per, ok2 := new(big.Rat).SetString(t.PerMonth.String())
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("the tier %s/%s is not a number", t.FromKw, t.PerMonth)
		}
		out = append(out, capacityTier{fromKW: from, perMonth: per})
	}
	return out, nil
}

// tierFor is the monthly price of the tier whose fromKw is the highest not
// above kw; the lowest tier's when kw is below them all.
func tierFor(tiers []capacityTier, kw *big.Rat) *big.Rat {
	best := tiers[0]
	for _, t := range tiers[1:] {
		if t.fromKW.Cmp(kw) <= 0 && t.fromKW.Cmp(best.fromKW) > 0 {
			best = t
		}
	}
	return best.perMonth
}
