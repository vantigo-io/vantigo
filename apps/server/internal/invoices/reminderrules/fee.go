package reminderrules

import (
	"math/big"
	"time"
)

// ReminderFee is the fee of a purring or an inkassovarsel (R8): 1/20 of the
// inkassosats in force on the letter's day, rounded to the nearest krone with
// .50 up (INKF § 1-2 fourth paragraph; 750 → 38).
func ReminderFee(inkassosats *big.Rat) *big.Rat {
	return round(new(big.Rat).Quo(inkassosats, big.NewRat(20, 1)), 0)
}

// feeChainCount is D8's two-fee counter with its six-month reset (R9, R11):
// the sent fee-bearing letters — a waived fee still counts, it was claimed —
// counted back from the latest, stopping at the first gap of more than six
// months between two consecutive ones; 0 when L is more than six months after
// the latest. The anniversary is still inside the six months, so the fee is
// allowed from the day after it (`>`, domstolloven § 148's month rule; the
// spec's reading 32). A fee is allowed while the count is below 2.
func feeChainCount(letters []Letter, L time.Time) int {
	var fees []Letter
	for _, l := range sentLetters(letters) {
		if l.FeeKind == FeeReminder {
			fees = append(fees, l)
		}
	}
	if len(fees) == 0 || L.After(AddMonthsClamped(*fees[len(fees)-1].SentOn, 6)) {
		return 0
	}
	count := 1
	for i := len(fees) - 1; i > 0; i-- {
		if fees[i].SentOn.After(AddMonthsClamped(*fees[i-1].SentOn, 6)) {
			break
		}
		count++
	}
	return count
}

// round is v at places decimals, the half away from zero (big.Rat's own rule,
// and the module's).
func round(v *big.Rat, places int) *big.Rat {
	r, _ := new(big.Rat).SetString(v.FloatString(places))
	return r
}
