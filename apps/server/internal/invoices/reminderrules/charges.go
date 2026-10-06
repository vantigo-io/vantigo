package reminderrules

import (
	"math/big"
)

// The kinds of charge waiver (D9), as invoices.charge_waivers stores them.
const (
	WaiverFee          = "fee"
	WaiverCompensation = "compensation"
	WaiverInterest     = "interest"
)

// ChargeState is an invoice's charges (D9): what its sent letters claimed,
// what was waived and paid, and what is outstanding — fees and compensation
// apart from interest — or due back.
type ChargeState struct {
	Claimed, Waived, Paid *big.Rat
	// Outstanding is FeesOutstanding + InterestOutstanding, never negative;
	// RefundDue is what was paid beyond every charge less its waivers.
	Outstanding, RefundDue               *big.Rat
	FeesOutstanding, InterestOutstanding *big.Rat
}

// Charges is D9's formula over an invoice's letters, waivers and live charge
// payments:
//
//	  Σ fee + Σ compensation over its sent letters − their waivers
//	+ the latest sent letter's cumulative interest − Σ interest waivers
//	− Σ its live charge payments
//
// the same terms a letter's own total states, so the two never disagree
// (NI1). Below zero it is a refund due, and outstanding is zero. Letters not
// sent claim nothing; a removed charge payment is not live and is left out by
// the caller.
func Charges(letters []Letter, waivers []Waiver, payments []ChargePayment) ChargeState {
	sent := sentLetters(letters)
	claimed := new(big.Rat)
	for _, l := range sent {
		claimed.Add(claimed, amount(l.Fee))
		claimed.Add(claimed, amount(l.Compensation))
	}
	latest := latestInterest(sent)
	claimed.Add(claimed, latest)
	waived := new(big.Rat)
	for _, w := range waivers {
		waived.Add(waived, w.Amount)
	}
	perLetter, interestPaid, refund := Allocate(letters, waivers, payments)
	fees := new(big.Rat)
	for _, l := range sent {
		fees.Add(fees, chargeNet(l, waivers))
		fees.Sub(fees, perLetter[l.ID])
	}
	interestLeft := new(big.Rat).Sub(interestCap(latest, waivers), interestPaid)
	return ChargeState{
		Claimed:             claimed,
		Waived:              waived,
		Paid:                sumPayments(payments),
		Outstanding:         new(big.Rat).Add(fees, interestLeft),
		RefundDue:           refund,
		FeesOutstanding:     fees,
		InterestOutstanding: interestLeft,
	}
}

// Allocate is the allocation within charges (spec reading 31, plan reading
// 8), recomputed over every live charge payment: they pay the sent letters'
// fees and compensation net of their waivers, oldest letter first, then
// interest — the latest letter's cumulative interest less the interest
// waivers, never more (a payment beyond it is never interest paid twice);
// what is left is the refund due. The payments' order does not change the
// figures, since all of them meet the same charges in the same order.
func Allocate(letters []Letter, waivers []Waiver, payments []ChargePayment) (perLetter map[int64]*big.Rat, interestPaid, refund *big.Rat) {
	left := sumPayments(payments)
	sent := sentLetters(letters)
	perLetter = make(map[int64]*big.Rat, len(sent))
	for _, l := range sent {
		share := minRat(left, chargeNet(l, waivers))
		perLetter[l.ID] = share
		left.Sub(left, share)
	}
	interestPaid = minRat(left, interestCap(latestInterest(sent), waivers))
	return perLetter, interestPaid, left.Sub(left, interestPaid)
}

// chargeNet is a letter's fee and compensation less their waivers.
func chargeNet(l Letter, waivers []Waiver) *big.Rat {
	net := new(big.Rat).Add(amount(l.Fee), amount(l.Compensation))
	for _, w := range waivers {
		if w.ReminderID == l.ID && w.Kind != WaiverInterest {
			net.Sub(net, w.Amount)
		}
	}
	return maxZero(net)
}

// interestCap is the interest still claimable: the latest claim less every
// interest waiver.
func interestCap(latest *big.Rat, waivers []Waiver) *big.Rat {
	return maxZero(new(big.Rat).Sub(latest, interestWaived(waivers)))
}

// interestWaived is Σ interest waivers.
func interestWaived(waivers []Waiver) *big.Rat {
	sum := new(big.Rat)
	for _, w := range waivers {
		if w.Kind == WaiverInterest {
			sum.Add(sum, w.Amount)
		}
	}
	return sum
}

// latestInterest is the cumulative interest the latest sent letter claimed.
func latestInterest(sent []Letter) *big.Rat {
	if len(sent) == 0 {
		return new(big.Rat)
	}
	return amount(sent[len(sent)-1].Interest)
}

func sumPayments(payments []ChargePayment) *big.Rat {
	sum := new(big.Rat)
	for _, p := range payments {
		sum.Add(sum, p.Amount)
	}
	return sum
}

// amount is a letter's figure, zero when it has none.
func amount(v *big.Rat) *big.Rat {
	if v == nil {
		return new(big.Rat)
	}
	return v
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) < 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

func maxZero(v *big.Rat) *big.Rat {
	if v.Sign() < 0 {
		return new(big.Rat)
	}
	return v
}
