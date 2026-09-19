package expenses

import "math/big"

// This file is design §4's arithmetic, and nothing else: pure functions over
// exact decimals (math/big.Rat), with no database, no request and no float
// anywhere in them.
//
// Two rules hold throughout. Everything rounds **half up** — the half away
// from zero, so 2.625 is 2.63 — because that is what a person totalling a
// receipt by hand does and what the numeric columns' own scale would do.
// And everything rounds **once**, at the end: kilometres times a rate plus
// kilometres times a passenger rate is one sum rounded once, never two
// roundings added together, which is why the rates arrive here as exact
// decimals rather than as the two-place numbers they are stored as.

// moneyPlaces is the scale every stored amount carries (numeric(12,2)).
const moneyPlaces = 2

// hundred is the divisor a percentage is applied through.
var hundred = big.NewRat(100, 1)

// netOf is the project's cost and the markup's base (decision X6): the gross
// less the VAT, which is nil when none was entered.
func netOf(gross, vat *big.Rat) *big.Rat {
	net := new(big.Rat).Set(gross)
	if vat != nil {
		net.Sub(net, vat)
	}
	return roundHalfUp(net, moneyPlaces)
}

// mileageAmount is what a mileage line pays its owner (design §4): the
// kilometres at the reimbursement rate, plus the kilometres at the passenger
// supplement for each passenger carried. passengerRate is nil when the table
// prices no supplement, which only ever happens with no passengers. The whole
// sum is rounded once.
func mileageAmount(km, rate, passengerRate *big.Rat, passengers int) *big.Rat {
	amount := new(big.Rat).Mul(km, rate)
	if passengerRate != nil && passengers > 0 {
		supplement := new(big.Rat).Mul(km, passengerRate)
		supplement.Mul(supplement, new(big.Rat).SetInt64(int64(passengers)))
		amount.Add(amount, supplement)
	}
	return roundHalfUp(amount, moneyPlaces)
}

// outlayBillAmount is what a billable outlay bills the customer (decision X7):
// the net plus the markup percentage of it.
func outlayBillAmount(net, markupPercent *big.Rat) *big.Rat {
	factor := new(big.Rat).Quo(markupPercent, hundred)
	factor.Add(factor, big.NewRat(1, 1))
	return roundHalfUp(new(big.Rat).Mul(net, factor), moneyPlaces)
}

// mileageBillAmount is what billable mileage bills the customer (decision X7):
// the kilometres at the customer's own rate per kilometre, which has nothing
// to do with what the owner is reimbursed.
func mileageBillAmount(km, billRatePerKm *big.Rat) *big.Rat {
	return roundHalfUp(new(big.Rat).Mul(km, billRatePerKm), moneyPlaces)
}

// roundHalfUp is v at places decimals, the half rounded away from zero. It
// goes through the exact decimal text big.Rat renders with that very rule, so
// there is one rounding implementation in the module and it is the standard
// library's.
func roundHalfUp(v *big.Rat, places int) *big.Rat {
	rounded, ok := new(big.Rat).SetString(decimalText(v, places))
	if !ok {
		// FloatString always renders a decimal SetString reads back.
		return new(big.Rat).Set(v)
	}
	return rounded
}

// decimalText is v as the exact decimal text a numeric column stores, the half
// rounded away from zero — the only way a computed amount reaches the
// database, so no float ever stands between the arithmetic and the column.
func decimalText(v *big.Rat, places int) string { return v.FloatString(places) }
