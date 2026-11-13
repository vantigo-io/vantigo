package manufacturing

// An order's cost (manufacturing design M38–M41, §15 Rounding): the
// estimate fixed at confirm, the running actual while the order is open, and
// the actual fixed at close with the cost per unit and the deviation. Every
// amount is computed exactly (big.Rat) and rounded half-up to two decimals
// once — when it is stored, or when it is shown. Only a holder of
// manufacturing:costs-view is answered any of it (M38); without the key the
// cost is left out of the order, never refused.

import (
	"encoding/json"
	"math/big"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

// costParts is a cost in its three parts, exact; incomplete when a figure
// it needed had no value and was counted as 0.
type costParts struct {
	components, machine, labour *big.Rat
	incomplete                  bool
}

func newCostParts() costParts {
	return costParts{components: new(big.Rat), machine: new(big.Rat), labour: new(big.Rat)}
}

func (c costParts) total() *big.Rat {
	t := new(big.Rat).Add(c.components, c.machine)
	return t.Add(t, c.labour)
}

// storedCost is a cost as the order's estimate or actual column keeps it:
// each amount half-up to two decimals, the total the exact sum rounded once.
type storedCost struct {
	Components  string  `json:"components"`
	Machine     string  `json:"machine"`
	Labour      string  `json:"labour"`
	Total       string  `json:"total"`
	Incomplete  bool    `json:"incomplete"`
	CostPerUnit *string `json:"costPerUnit,omitempty"`
	// MainTotal and ByProducts are the split of the total over the main
	// product and the by-products, fixed at close (manufacturing phase 2
	// M122); absent for an order with no by-products.
	MainTotal  *string           `json:"mainTotal,omitempty"`
	ByProducts []storedByProduct `json:"byProducts,omitempty"`
}

// storedByProduct is a by-product's part of the actual as fixed at close
// (M122): its share's amount, its cost per unit, and whether it produced
// nothing — its share then went to the main product, and amount is the part
// it would have had.
type storedByProduct struct {
	OutputID    int64   `json:"outputId"`
	VariantID   int32   `json:"variantId"`
	Share       string  `json:"share"`
	Amount      string  `json:"amount"`
	Produced    string  `json:"produced"`
	CostPerUnit *string `json:"costPerUnit,omitempty"`
	NotProduced bool    `json:"notProduced,omitempty"`
}

// splitCost splits a cost total over the by-products and the main product
// (M122): each by-product carries its share percent of it, exact; one that
// produced nothing has no cost per unit and its share stays with the main
// product, which takes what the others do not.
func splitCost(total *big.Rat, outputs []store.ManufacturingOrderOutput) (*big.Rat, []storedByProduct) {
	main := new(big.Rat).Set(total)
	parts := make([]storedByProduct, 0, len(outputs))
	for _, x := range outputs {
		part := new(big.Rat).Mul(total, ratOrZero(x.CostShare))
		part.Quo(part, hundred)
		produced := ratOrZero(x.Produced)
		p := storedByProduct{OutputID: x.ID, VariantID: x.VariantID, Share: numericText(x.CostShare, percentPlaces), Amount: amountText(part),
			Produced: text(produced, quantityPlaces)}
		if produced.Sign() <= 0 {
			p.NotProduced = true
		} else {
			main.Sub(main, part)
			p.CostPerUnit = costPerUnitOf(part, produced)
		}
		parts = append(parts, p)
	}
	return main, parts
}

func storedOf(c costParts) storedCost {
	return storedCost{Components: amountText(c.components), Machine: amountText(c.machine), Labour: amountText(c.labour),
		Total: amountText(c.total()), Incomplete: c.incomplete}
}

// hours is minutes as hours, exact.
func hours(minutes *big.Rat) *big.Rat { return new(big.Rat).Quo(minutes, sixty) }

// durationRat is d in minutes, exact to the nanosecond.
func durationMinutes(d time.Duration) *big.Rat {
	return new(big.Rat).SetFrac(big.NewInt(int64(d)), big.NewInt(int64(time.Minute)))
}

// timesRate adds minutes × rate per hour to into; a missing rate on time
// spent makes the cost incomplete.
func timesRate(into *big.Rat, minutes *big.Rat, rate *big.Rat, incomplete *bool) {
	if minutes.Sign() == 0 {
		return
	}
	if rate == nil {
		*incomplete = true
		return
	}
	into.Add(into, new(big.Rat).Mul(hours(minutes), rate))
}

// estimateOf is M39's estimate: components Σ required × the standard cost
// snapshotted at confirm; machine Σ expected hours × the machine rate;
// labour Σ expected hours × the labour rate, one operator. The rates are the
// work orders' snapshots (M27a).
func estimateOf(components []store.ManufacturingOrderComponent, wos []store.ManufacturingWorkOrder) costParts {
	c := newCostParts()
	for _, l := range components {
		required := ratOrZero(l.Required)
		if required.Sign() == 0 {
			continue
		}
		cost := ratOf(l.StandardCost)
		if cost == nil {
			c.incomplete = true
			continue
		}
		c.components.Add(c.components, new(big.Rat).Mul(required, cost))
	}
	for _, w := range wos {
		if w.Status == workOrderCancelled {
			continue
		}
		minutes := big.NewRat(w.ExpectedMinutes, 1)
		timesRate(c.machine, minutes, ratOf(w.MachineRate), &c.incomplete)
		timesRate(c.labour, minutes, ratOf(w.LabourRate), &c.incomplete)
	}
	return c
}

// logSpan is a log's time, an open one to now.
func logSpan(l store.ManufacturingTimeLog, now time.Time) span {
	end := now
	if l.EndedAt != nil {
		end = *l.EndedAt
	}
	if end.Before(l.StartedAt) {
		end = l.StartedAt
	}
	return span{start: l.StartedAt, end: end}
}

// actualOf is M40's actual: components Σ (consumed + scrapped) × the
// standard cost at confirm; machine, per work order, the union of its
// productive logs × its machine rate; labour Σ every productive log × the
// labour rate. Open logs count to now. Downtime is answered apart, by
// reason, as the union of the order's downtime logs, and never charged.
func actualOf(components []store.ManufacturingOrderComponent, wos []store.ManufacturingWorkOrder, logs []store.ManufacturingTimeLog, now time.Time) (costParts, map[string]time.Duration) {
	c := newCostParts()
	for _, l := range components {
		used := new(big.Rat).Add(ratOrZero(l.Consumed), ratOrZero(l.Scrapped))
		if used.Sign() == 0 {
			continue
		}
		cost := ratOf(l.StandardCost)
		if cost == nil {
			c.incomplete = true
			continue
		}
		c.components.Add(c.components, new(big.Rat).Mul(used, cost))
	}
	productive := map[int64][]span{}
	downtime := map[string][]span{}
	for _, l := range logs {
		if l.WorkOrderID == nil {
			continue
		}
		switch l.Kind {
		case logProductive:
			productive[*l.WorkOrderID] = append(productive[*l.WorkOrderID], logSpan(l, now))
		case logDowntime:
			if l.Reason != nil {
				downtime[*l.Reason] = append(downtime[*l.Reason], logSpan(l, now))
			}
		}
	}
	for _, w := range wos {
		var labour time.Duration
		for _, s := range productive[w.ID] {
			labour += s.length()
		}
		timesRate(c.machine, durationMinutes(unionLength(productive[w.ID])), ratOf(w.MachineRate), &c.incomplete)
		timesRate(c.labour, durationMinutes(labour), ratOf(w.LabourRate), &c.incomplete)
	}
	stopped := map[string]time.Duration{}
	for reason, spans := range downtime {
		stopped[reason] = unionLength(spans)
	}
	return c, stopped
}

// costPerUnitOf is the actual total ÷ the good output, half-up to two
// decimals (M41); nil when nothing good was produced.
func costPerUnitOf(total *big.Rat, produced *big.Rat) *string {
	if produced.Sign() <= 0 {
		return nil
	}
	t := amountText(new(big.Rat).Quo(total, produced))
	return &t
}

// marshalCost is a stored cost as the column's value.
func marshalCost(c storedCost) []byte {
	b, err := json.Marshal(c)
	if err != nil {
		panic("manufacturing: a cost does not encode: " + err.Error())
	}
	return b
}

// unmarshalCost is a cost column, nil when it is NULL or unreadable.
func unmarshalCost(b []byte) *storedCost {
	if len(b) == 0 {
		return nil
	}
	var c storedCost
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &c
}

func partsOf(c storedCost) gen.ManufacturingCostParts {
	return gen.ManufacturingCostParts{Components: c.Components, Machine: c.Machine, Labour: c.Labour, Total: c.Total, Incomplete: c.Incomplete}
}

// deviationPart is actual − estimate and its percent of the estimate, half-up
// to one decimal, null when the estimate is 0 (M41).
func deviationPart(actual, estimate string) gen.ManufacturingCostDeviationPart {
	a, e := mustRat(actual), mustRat(estimate)
	d := new(big.Rat).Sub(a, e)
	return gen.ManufacturingCostDeviationPart{Amount: d.FloatString(amountPlaces), Percent: percentOf(d, e)}
}

func deviationOf(actual, estimate storedCost) gen.ManufacturingCostDeviation {
	return gen.ManufacturingCostDeviation{
		Components: deviationPart(actual.Components, estimate.Components), Machine: deviationPart(actual.Machine, estimate.Machine),
		Labour: deviationPart(actual.Labour, estimate.Labour), Total: deviationPart(actual.Total, estimate.Total),
	}
}

// downtimeOf is the downtime by reason in whole minutes, by reason.
func downtimeOf(stopped map[string]time.Duration) []gen.ManufacturingDowntimeMinutes {
	out := make([]gen.ManufacturingDowntimeMinutes, 0, len(stopped))
	for _, reason := range slices.Sorted(func(yield func(string) bool) {
		for r := range stopped {
			if !yield(r) {
				return
			}
		}
	}) {
		out = append(out, gen.ManufacturingDowntimeMinutes{Reason: gen.ManufacturingDowntimeReason(reason), Minutes: int64(stopped[reason] / time.Minute)})
	}
	return out
}

// orderCostOf is the order's cost as answered (M38–M41): the stored
// estimate; the stored actual once done, else the running actual to now;
// the deviation when both are there; the cost per unit at close.
func orderCostOf(o store.ManufacturingOrder, components []store.ManufacturingOrderComponent, wos []store.ManufacturingWorkOrder,
	logs []store.ManufacturingTimeLog, now time.Time, outputs []store.ManufacturingOrderOutput, entries map[int32]contracts.VariantEntry) *gen.ManufacturingOrderCost {
	out := &gen.ManufacturingOrderCost{Downtime: []gen.ManufacturingDowntimeMinutes{}, ByProducts: []gen.ManufacturingByproductCost{}}
	if o.Status == orderDraft {
		return out
	}
	estimate := unmarshalCost(o.Estimate)
	if estimate != nil {
		p := partsOf(*estimate)
		out.Estimate = &p
	}
	live, stopped := actualOf(components, wos, logs, now)
	out.Downtime = downtimeOf(stopped)
	actual := unmarshalCost(o.Actual)
	if o.Status == orderDone && actual != nil {
		out.ActualFixed = true
		out.CostPerUnit = actual.CostPerUnit
	} else {
		s := storedOf(live)
		actual = &s
	}
	p := partsOf(*actual)
	out.Actual = &p
	if estimate != nil {
		d := deviationOf(*actual, *estimate)
		out.Deviation = &d
	}
	// The split over the by-products (M122): fixed at close, else the
	// running actual split as close would.
	main, parts := splitCost(mustRat(actual.Total), outputs)
	mainText := amountText(main)
	if out.ActualFixed && actual.MainTotal != nil {
		mainText, parts = *actual.MainTotal, actual.ByProducts
	}
	out.MainActual = &mainText
	byOutput := map[int64]storedByProduct{}
	for _, part := range parts {
		byOutput[part.OutputID] = part
	}
	for _, x := range outputs {
		part := byOutput[x.ID]
		c := gen.ManufacturingByproductCost{OutputId: x.ID, Variant: refOf(entries, x.VariantID, true), CostShare: numericText(x.CostShare, percentPlaces),
			Produced: numericText(x.Produced, quantityPlaces), NotProduced: ratOrZero(x.Produced).Sign() <= 0}
		if c.Variant.Sku == "" {
			c.Variant.Sku, c.Variant.ProductName, c.Variant.Unit = x.Sku, x.ProductName, x.Unit
		}
		if part.Amount != "" {
			amount := part.Amount
			c.Actual = &amount
			c.NotProduced = part.NotProduced
		}
		if out.ActualFixed {
			c.CostPerUnit = part.CostPerUnit
		}
		if estimate != nil {
			e := new(big.Rat).Mul(mustRat(estimate.Total), ratOrZero(x.CostShare))
			t := amountText(e.Quo(e, hundred))
			c.Estimate = &t
		}
		out.ByProducts = append(out.ByProducts, c)
	}
	return out
}

// workOrderDuration is a work order's actual minutes — the union of its
// productive logs, open ones to now, rounded down — its deviation from the
// expected and that deviation's percent (M41).
func workOrderDuration(w store.ManufacturingWorkOrder, logs []store.ManufacturingTimeLog, now time.Time) (int64, int64, *string) {
	var spans []span
	for _, l := range logs {
		if l.Kind == logProductive {
			spans = append(spans, logSpan(l, now))
		}
	}
	actual := int64(unionLength(spans) / time.Minute)
	deviation := actual - w.ExpectedMinutes
	return actual, deviation, percentOf(big.NewRat(deviation, 1), big.NewRat(w.ExpectedMinutes, 1))
}
