package inventory

import (
	"cmp"
	"math/big"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The rule and the rounding (phase 2 design I56, I57), one pure function the
// low-stock list, its CSV, the dashboard's count and a site's stock row all
// judge a placement by — the SQL statement of I58 filters on the same
// inequality, and this decides what the row says:
//
//	available = onHand − reserved
//	projected = available + incoming
//	low       = reorderPoint is set and projected ≤ reorderPoint
//	target    = maximum, or reorderPoint + 1 when there is no maximum
//	needed    = target − projected, at least 1 when low
//
// The suggestion is needed rounded up to a whole number of the placement's
// multiple when it has one (manufacturing phase 2 M105), then to a whole
// number of the variant's smallest packaging — fewest units, then sort order, then id — or needed
// itself when the variant has none. It may pass the maximum, and says by how
// much.

// lowLevels is what the rule reads of one variant at one site.
type lowLevels struct {
	onHand, reserved, incoming *big.Rat
	// reorderPoint, maximum and multiple are nil when unset.
	reorderPoint, maximum, multiple *big.Rat
	packagings                      []contracts.PackagingEntry
}

// lowVerdict is the rule's answer. needed, suggested and packaging are set
// only when low.
type lowVerdict struct {
	available, projected *big.Rat
	low                  bool
	needed, suggested    *big.Rat
	// packaging is the smallest packaging the suggestion was rounded to, and
	// count how many of it; nil when the variant has none.
	packaging *contracts.PackagingEntry
	count     int64
	// aboveMaximum is how far the suggestion passes the maximum, nil when it
	// does not (I57's "… 5 above the maximum of 40").
	aboveMaximum *big.Rat
}

// judgeLow is the rule (I56) and the rounding (I57).
func judgeLow(l lowLevels) lowVerdict {
	v := lowVerdict{available: new(big.Rat).Sub(orZero(l.onHand), orZero(l.reserved))}
	v.projected = new(big.Rat).Add(v.available, orZero(l.incoming))
	if l.reorderPoint == nil || v.projected.Cmp(l.reorderPoint) > 0 {
		return v
	}
	v.low = true
	target := new(big.Rat).Add(l.reorderPoint, big.NewRat(1, 1))
	if l.maximum != nil {
		target = l.maximum
	}
	v.needed = new(big.Rat).Sub(target, v.projected)
	if one := big.NewRat(1, 1); v.needed.Cmp(one) < 0 {
		v.needed = one
	}
	v.suggested = v.needed
	if l.multiple != nil && l.multiple.Sign() > 0 {
		v.suggested = new(big.Rat).Mul(l.multiple, new(big.Rat).SetInt64(ceilQuotient(v.needed, l.multiple)))
	}
	if p := smallestPackaging(l.packagings); p != nil {
		size := ratFromText(p.Quantity)
		v.packaging = p
		v.count = ceilQuotient(v.suggested, size)
		v.suggested = new(big.Rat).Mul(size, new(big.Rat).SetInt64(v.count))
	}
	if l.maximum != nil {
		if over := new(big.Rat).Sub(new(big.Rat).Add(v.projected, v.suggested), l.maximum); over.Sign() > 0 {
			v.aboveMaximum = over
		}
	}
	return v
}

// smallestPackaging is the packaging holding the fewest units, then the
// lowest sort order, then the lowest id; nil when none holds more than 0.
func smallestPackaging(packagings []contracts.PackagingEntry) *contracts.PackagingEntry {
	var candidates []contracts.PackagingEntry
	for _, p := range packagings {
		if ratFromText(p.Quantity).Sign() > 0 {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	p := slices.MinFunc(candidates, func(a, b contracts.PackagingEntry) int {
		return cmp.Or(ratFromText(a.Quantity).Cmp(ratFromText(b.Quantity)), cmp.Compare(a.SortOrder, b.SortOrder), cmp.Compare(a.ID, b.ID))
	})
	return &p
}

// ceilQuotient is ⌈a / b⌉ for a and b more than 0.
func ceilQuotient(a, b *big.Rat) int64 {
	q := new(big.Rat).Quo(a, b)
	n, rem := new(big.Int).QuoRem(q.Num(), q.Denom(), new(big.Int))
	if rem.Sign() > 0 {
		n.Add(n, big.NewInt(1))
	}
	return n.Int64()
}
