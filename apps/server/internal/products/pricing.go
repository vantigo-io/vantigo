package products

import (
	"sort"
	"strings"
	"time"
)

// This file ports Domain/Products/ProductPricing.cs and the parts of
// Domain/Products/ProductPrice.cs it depends on (IsBounded, IsValidAt,
// Overlaps): effective-price resolution and the overlap-conflict rule
// (products inventory §2). productPrice is kept independent of the
// sqlc-generated row and the contract's DTOs, the same way customers'
// value objects stay independent of gen.*, so this file (and its tests,
// ported from Domain/Products/ProductPricingTests.cs) has no generated-code
// dependency.

// productPrice is one price row: ProductPrice.cs's Id, Currency, Amount,
// ValidFrom and ValidTo.
type productPrice struct {
	ID        int32
	Currency  string
	Amount    float64
	ValidFrom *time.Time
	ValidTo   *time.Time
}

// timeMin and timeMax stand in for DateTimeOffset.MinValue/MaxValue, the
// sentinels Overlaps substitutes for an open bound (ProductPrice.cs:49-52).
var (
	timeMin = time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	timeMax = time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC)
)

// isBounded is ProductPrice.IsBounded (:39): whether the price has any
// validity bound at all, i.e. is a campaign/sale price rather than the
// open-ended base price.
func (p productPrice) isBounded() bool {
	return p.ValidFrom != nil || p.ValidTo != nil
}

// isValidAt is ProductPrice.IsValidAt (:42-44): ValidFrom <= moment <
// ValidTo, either bound open when nil.
func (p productPrice) isValidAt(moment time.Time) bool {
	if p.ValidFrom != nil && p.ValidFrom.After(moment) {
		return false
	}
	if p.ValidTo != nil && !moment.Before(*p.ValidTo) {
		return false
	}
	return true
}

// overlaps is ProductPrice.Overlaps (:47-54): half-open interval
// intersection, with timeMin/timeMax substituted for an open bound.
func (p productPrice) overlaps(other productPrice) bool {
	thisFrom, thisTo := timeOrMin(p.ValidFrom), timeOrMax(p.ValidTo)
	otherFrom, otherTo := timeOrMin(other.ValidFrom), timeOrMax(other.ValidTo)
	return thisFrom.Before(otherTo) && otherFrom.Before(thisTo)
}

func timeOrMin(t *time.Time) time.Time {
	if t == nil {
		return timeMin
	}
	return *t
}

func timeOrMax(t *time.Time) time.Time {
	if t == nil {
		return timeMax
	}
	return *t
}

// pricesConflict is ProductPricing.Conflicts (:51-64): two prices in the
// same currency (ordinal-ignore-case), of the same boundedness, whose
// windows overlap. A bounded campaign price never conflicts with the
// open-ended base price of the same currency — the intended "campaign
// overrides base" mechanism.
func pricesConflict(candidate, existing productPrice) bool {
	if !strings.EqualFold(candidate.Currency, existing.Currency) {
		return false
	}
	if candidate.isBounded() != existing.isBounded() {
		return false
	}
	return candidate.overlaps(existing)
}

// betterEffectivePrice reports whether a should be preferred over b when
// both are valid at the resolution moment: bounded (campaign) beats
// open-ended (base); ties broken by the latest ValidFrom (nil treated as
// timeMin); ties on that broken by the highest Id (insertion-order
// tiebreak) — ProductPricing.GetEffectivePrices' OrderByDescending chain
// (:21-24).
func betterEffectivePrice(a, b productPrice) bool {
	if a.isBounded() != b.isBounded() {
		return a.isBounded()
	}
	af, bf := timeOrMin(a.ValidFrom), timeOrMin(b.ValidFrom)
	if !af.Equal(bf) {
		return af.After(bf)
	}
	return a.ID > b.ID
}

// getEffectivePrices is ProductPricing.GetEffectivePrices (:14-28): the one
// applicable price per currency at moment, sorted by currency
// (ordinal-ignore-case).
func getEffectivePrices(prices []productPrice, moment time.Time) []productPrice {
	var order []string
	groups := map[string][]productPrice{}
	for _, p := range prices {
		if !p.isValidAt(moment) {
			continue
		}
		key := strings.ToUpper(p.Currency)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], p)
	}

	result := make([]productPrice, 0, len(groups))
	for _, key := range order {
		group := groups[key]
		best := group[0]
		for _, candidate := range group[1:] {
			if betterEffectivePrice(candidate, best) {
				best = candidate
			}
		}
		result = append(result, best)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToUpper(result[i].Currency) < strings.ToUpper(result[j].Currency)
	})
	return result
}

// getEffectivePrice is ProductPricing.GetEffectivePrice (:34-43): the
// effective price in a single currency at moment, or the zero value with ok
// false when none is valid.
func getEffectivePrice(prices []productPrice, currency string, moment time.Time) (productPrice, bool) {
	var matching []productPrice
	for _, p := range prices {
		if strings.EqualFold(p.Currency, currency) {
			matching = append(matching, p)
		}
	}
	effective := getEffectivePrices(matching, moment)
	if len(effective) == 0 {
		return productPrice{}, false
	}
	return effective[0], true
}
