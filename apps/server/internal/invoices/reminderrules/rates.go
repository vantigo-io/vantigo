package reminderrules

import (
	"math/big"
	"time"
)

// The three kinds of collection rate (D6), as invoices.collection_rates
// stores them.
const (
	KindLateInterest = "late_interest_percent"
	KindInkassosats  = "inkassosats"
	KindCompensation = "b2b_compensation_nok"
)

// Rate is one invoices.collection_rates row: in force from ValidFrom until
// the next row of its kind.
type Rate struct {
	ID        int64
	Kind      string
	ValidFrom time.Time
	Value     *big.Rat
}

// Regime is the inkassolov a letter is judged under, recorded on it.
type Regime string

const (
	Regime1988 Regime = "inkassolov_1988"
	Regime2026 Regime = "inkassolov_2026"
)

// RegimeOn is the regime of a letter dated day (D6): the 2026 regime once
// inkassolov_2026_from is set and reached, the 1988 regime otherwise.
func RegimeOn(s Settings, day time.Time) Regime {
	if s.Inkassolov2026From != nil && !day.Before(*s.Inkassolov2026From) {
		return Regime2026
	}
	return Regime1988
}

// RateOn is the row of kind in force on day: the latest one starting on or
// before it.
func RateOn(rates []Rate, kind string, day time.Time) (Rate, bool) {
	var best Rate
	found := false
	for _, r := range rates {
		if r.Kind != kind || r.ValidFrom.After(day) {
			continue
		}
		if !found || r.ValidFrom.After(best.ValidFrom) {
			best, found = r, true
		}
	}
	return best, found
}

// OutdatedRate names the rate a letter needs and the half-year it is missing
// for, as collection_rates_outdated answers it.
type OutdatedRate struct {
	Kind     string
	HalfYear string // "2027-H1"
}

// Outdated is the first half-year from from to to that has no row of kind
// starting on its first day (plan reading 5): after the last row, in a gap
// between rows, or before the first — so an old rate is never carried across
// a missing half-year. The inkassosats, which is not half-yearly, only needs a
// row in force on from. Nil when every day is covered.
func Outdated(rates []Rate, kind string, from, to time.Time) *OutdatedRate {
	if kind != KindLateInterest && kind != KindCompensation {
		if _, ok := RateOn(rates, kind, from); !ok {
			return &OutdatedRate{Kind: kind, HalfYear: halfYearName(from)}
		}
		return nil
	}
	for h := halfYearStart(from); !h.After(to); h = AddMonthsClamped(h, 6) {
		if !startsOn(rates, kind, h) {
			return &OutdatedRate{Kind: kind, HalfYear: halfYearName(h)}
		}
	}
	return nil
}

// startsOn reports a row of kind starting on day.
func startsOn(rates []Rate, kind string, day time.Time) bool {
	for _, r := range rates {
		if r.Kind == kind && r.ValidFrom.Equal(day) {
			return true
		}
	}
	return false
}
