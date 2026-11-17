package purchasing

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// This file is how a quantity, a price and an amount cross the wire and the
// database. The contract carries every one as decimal text (P16, P17), so it
// is read with big.Rat.SetString — never through a float — computed as a
// big.Rat and stored as a numeric. A response writes an amount with two
// places, a price with two to four, and a quantity or a percentage with no
// trailing zeros.

// The decimals each stored value has (§9).
const (
	quantityPlaces = 3 // numeric(12,3)
	pricePlaces    = 4 // numeric(18,4)
	discountPlaces = 2 // numeric(5,2)
	amountPlaces   = 2 // numeric(14,2)
)

var (
	maxQuantity = mustRat("999999999.999")
	maxPrice    = mustRat("99999999999999.9999")
	maxAmount   = mustRat("999999999999.99") // numeric(14,2)
	hundred     = big.NewRat(100, 1)
	// decimalText is a decimal as the API takes it: digits, an optional
	// point and decimals, no sign, no exponent, no thousands separator.
	decimalText = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
)

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("purchasing: not a decimal: " + s)
	}
	return r
}

// parseDecimal reads text as a decimal of 0 or more with at most places
// decimals, at most limit; ok is false otherwise.
func parseDecimal(text string, places int, limit *big.Rat) (*big.Rat, bool) {
	t := strings.TrimSpace(text)
	if !decimalText.MatchString(t) {
		return nil, false
	}
	if _, decimals, found := strings.Cut(t, "."); found && len(decimals) > places {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(t)
	if !ok || r.Cmp(limit) > 0 {
		return nil, false
	}
	return r, true
}

// numeric is v as a column's value at places decimals, rounded half away from
// zero (big.Rat's rule); nil is NULL.
func numeric(v *big.Rat, places int) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	if err := n.Scan(v.FloatString(places)); err != nil {
		panic(fmt.Sprintf("purchasing: %s is not a storable decimal: %v", v.FloatString(places), err))
	}
	return n
}

// ratOf is a numeric column's value as an exact decimal; NULL is nil.
func ratOf(n pgtype.Numeric) *big.Rat {
	if !n.Valid || n.Int == nil {
		return nil
	}
	r := new(big.Rat).SetInt(n.Int)
	if n.Exp == 0 {
		return r
	}
	exp := int64(n.Exp)
	if exp < 0 {
		exp = -exp
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(exp), nil))
	if n.Exp > 0 {
		return r.Mul(r, scale)
	}
	return r.Quo(r, scale)
}

// ratOrZero is ratOf with NULL as 0.
func ratOrZero(n pgtype.Numeric) *big.Rat {
	if r := ratOf(n); r != nil {
		return r
	}
	return new(big.Rat)
}

// roundTo is v at places decimals, half away from zero.
func roundTo(v *big.Rat, places int) *big.Rat { return mustRat(v.FloatString(places)) }

// trimmed is v with at most places decimals and no trailing zeros: "12",
// "2.5" — the quantities of the API and of contracts.PurchaseSupply.
func trimmed(v *big.Rat, places int) string {
	if v == nil {
		return "0"
	}
	text := v.FloatString(places)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" {
		return "0"
	}
	return text
}

// amountText is an amount with two places: "1234.50".
func amountText(v *big.Rat) string {
	if v == nil {
		v = new(big.Rat)
	}
	return v.FloatString(amountPlaces)
}

// priceText is a price with two to four places: "12.50", "0.0125".
func priceText(v *big.Rat) string {
	text := v.FloatString(pricePlaces)
	whole, frac, _ := strings.Cut(text, ".")
	for len(frac) > 2 && strings.HasSuffix(frac, "0") {
		frac = frac[:len(frac)-1]
	}
	return whole + "." + frac
}

// numericText is a numeric column as trimmed text, nil for NULL.
func numericText(n pgtype.Numeric, places int) *string {
	r := ratOf(n)
	if r == nil {
		return nil
	}
	return ptr(trimmed(r, places))
}

// lineNet is round2(orderQuantity × unitPrice × (1 − discount/100)) (P17).
func lineNet(orderQuantity, unitPrice, discount *big.Rat) *big.Rat {
	factor := new(big.Rat).Sub(big.NewRat(1, 1), new(big.Rat).Quo(discount, hundred))
	return roundTo(new(big.Rat).Mul(new(big.Rat).Mul(orderQuantity, unitPrice), factor), amountPlaces)
}

// dateOf is a date column from a calendar date; nil is NULL.
func dateOf(d *time.Time) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *d, Valid: true}
}

// dayCol is a date column from a calendar date.
func dayCol(d time.Time) pgtype.Date { return pgtype.Date{Time: d, Valid: true} }

// dateFromAPI is an API date as a calendar date at midnight UTC; nil stays nil.
func dateFromAPI(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	return &t
}

// dateOut is a date column as the API answers it.
func dateOut(d pgtype.Date) *openapi_types.Date {
	if !d.Valid {
		return nil
	}
	return &openapi_types.Date{Time: d.Time}
}

// timeOfDate is a date column as a calendar date, nil for NULL.
func timeOfDate(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

// dayOf is the civil day of t in loc, at midnight UTC.
func dayOf(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ptr is a pointer to v.
func ptr[T any](v T) *T { return &v }

// deref is *p, "" for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// textPtr is s trimmed, nil when empty.
func textPtr(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

// distinctSorted is ids sorted, once each.
func distinctSorted[T int32 | int64](ids []T) []T {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}
