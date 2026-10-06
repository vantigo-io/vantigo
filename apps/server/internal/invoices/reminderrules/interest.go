package reminderrules

import (
	"math/big"
	"slices"
	"time"
)

// Segment is a run of days bearing interest on one base at one rate: a
// letter shows them (D8, D10).
type Segment struct {
	From, To time.Time // both counted
	Rate     *big.Rat  // percent a year
	Base     *big.Rat  // the principal the days bear interest on
}

// Interest is the late interest claimable on L (D8): nothing unless
// late_interest is on, the mode is normal and the invoice was delivered on or
// before its due date; otherwise simple interest, always cumulative from the
// day after E to L inclusive — an interest waiver is an amount beside it,
// never a new start (NI1). Day d bears gross − the credit notes issued on or
// before d − the payments paid on or before d − 1 (plan reading 7: a credit
// note lowers its own day, a payment the day after its paid_on), at the rate
// in force on d, actual/365; the segments split at every rate row, credit
// note and payment, and the sum is rounded to the øre once. Interest is never
// on fees or the compensation. outdated names a half-year of the period —
// to L, or to the day before the principal first reaches zero — without its
// rate row, and then the total is zero.
func Interest(in Input) (total *big.Rat, from *time.Time, segs []Segment, outdated *OutdatedRate) {
	r := interest(in)
	return r.total, r.from, r.segs, r.outdated
}

type interestResult struct {
	total    *big.Rat
	from     *time.Time
	segs     []Segment
	outdated *OutdatedRate
	rateIDs  []int64
}

// interestApplies is whether a letter may claim interest at all.
func interestApplies(in Input) bool {
	return in.Settings.LateInterest && in.Mode == ModeNormal && Delivered(in)
}

func interest(in Input) interestResult {
	res := interestResult{total: new(big.Rat)}
	if !interestApplies(in) {
		return res
	}
	start := addDays(EffectiveDue(in.Invoice.DueDate), 1)
	if in.L.Before(start) {
		return res
	}
	// Each day the base or the rate may change starts a segment.
	cuts := []time.Time{start}
	cut := func(d time.Time) {
		if d.After(start) && !d.After(in.L) {
			cuts = append(cuts, d)
		}
	}
	for _, r := range in.Rates {
		if r.Kind == KindLateInterest {
			cut(r.ValidFrom)
		}
	}
	for _, c := range in.Credits {
		cut(c.IssueDate)
	}
	for _, p := range in.Payments {
		cut(addDays(p.PaidOn, 1))
	}
	slices.SortFunc(cuts, func(a, b time.Time) int { return a.Compare(b) })
	cuts = slices.CompactFunc(cuts, func(a, b time.Time) bool { return a.Equal(b) })

	// The open principal only falls, so the period needing rates ends the day
	// before it first reaches zero: an invoice settled in a half-year whose
	// successor has no row yet is never outdated.
	end := in.L
	for _, c := range cuts {
		if openOn(in, c).Sign() <= 0 {
			end = addDays(c, -1)
			break
		}
	}
	if end.Before(start) {
		return res
	}
	if res.outdated = Outdated(in.Rates, KindLateInterest, start, end); res.outdated != nil {
		return res
	}

	sum := new(big.Rat)
	for i, from := range cuts {
		to := in.L
		if i+1 < len(cuts) {
			to = addDays(cuts[i+1], -1)
		}
		base := openOn(in, from)
		if base.Sign() <= 0 {
			continue
		}
		rate, _ := RateOn(in.Rates, KindLateInterest, from) // Outdated vouched for every day
		amount := new(big.Rat).Mul(base, rate.Value)
		amount.Mul(amount, new(big.Rat).SetInt64(daysFrom(from, to)))
		amount.Quo(amount, big.NewRat(36500, 1))
		sum.Add(sum, amount)
		res.segs = append(res.segs, Segment{From: from, To: to, Rate: new(big.Rat).Set(rate.Value), Base: base})
		if !slices.Contains(res.rateIDs, rate.ID) {
			res.rateIDs = append(res.rateIDs, rate.ID)
		}
	}
	res.total = round(sum, 2)
	res.from = &start
	return res
}

// openOn is the principal day d bears interest on: gross less the credit
// notes issued on or before d and the payments paid before d.
func openOn(in Input, d time.Time) *big.Rat {
	open := new(big.Rat).Set(in.Invoice.Gross)
	for _, c := range in.Credits {
		if !c.IssueDate.After(d) {
			open.Sub(open, c.Gross)
		}
	}
	for _, p := range in.Payments {
		if p.PaidOn.Before(d) {
			open.Sub(open, p.Amount)
		}
	}
	return open
}
