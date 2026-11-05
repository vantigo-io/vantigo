package orders

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is how an amount crosses the wire and the database. HTTP numbers
// are number/double in this codebase, so a quantity or a price arrives as a
// float64; it becomes an exact decimal through its shortest round-tripping
// text (strconv 'f', -1) and big.Rat.SetString, as Invoices' and Inventory's
// ratFromFloat do — never SetFloat64. Every amount is computed as a big.Rat by
// internal/docmath (O17) and stored as a numeric; the float64 a response
// carries is made only for the wire.

// The decimals each stored value has (O15).
const (
	quantityPlaces  = 3 // numeric(12,3)
	pricePlaces     = 8 // numeric(18,8), of which the editor takes four
	entryPlaces     = 4 // what a typed unit price may have
	discountPlaces  = 2 // numeric(5,2)
	amountPlaces    = 2 // numeric(14,2)
	ratePlaces      = 2 // numeric(5,2)
	maxQuantityText = "999999999.999"
	maxPriceText    = "9999999999.9999"
)

var (
	maxQuantity = mustRat(maxQuantityText)
	maxPrice    = mustRat(maxPriceText)
	hundred     = big.NewRat(100, 1)
)

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("orders: not a decimal: " + s)
	}
	return r
}

// ratFromFloat is a JSON number as the exact decimal it was written as.
func ratFromFloat(v float64) *big.Rat {
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	if !ok {
		return new(big.Rat)
	}
	return r
}

// decimalPlaces is how many decimals a JSON number was written with.
func decimalPlaces(v float64) int {
	text := strconv.FormatFloat(v, 'f', -1, 64)
	if _, decimals, ok := strings.Cut(text, "."); ok {
		return len(decimals)
	}
	return 0
}

// hasPlaces reports whether v is exact at places decimals.
func hasPlaces(v *big.Rat, places int) bool {
	scaled := new(big.Rat).Mul(v, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)))
	return scaled.IsInt()
}

// finite reports whether v is a number.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// numeric is v as a column's value at places decimals, rounded half away from
// zero (big.Rat's rule); nil is NULL.
func numeric(v *big.Rat, places int) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	if err := n.Scan(v.FloatString(places)); err != nil {
		panic(fmt.Sprintf("orders: %s is not a storable decimal: %v", v.FloatString(places), err))
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

// wire is an exact decimal as the JSON number a response carries, at places.
func wire(v *big.Rat, places int) float64 {
	if v == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(v.FloatString(places), 64)
	return f
}

// wirePtr is wire for a value that may be NULL.
func wirePtr(v *big.Rat, places int) *float64 {
	if v == nil {
		return nil
	}
	f := wire(v, places)
	return &f
}

// round2 is v at two decimals, half away from zero, as docmath rounds.
func round2(v *big.Rat) *big.Rat { return mustRat(v.FloatString(2)) }

// withVAT is v with VAT at rate percent added, at two decimals (O19: the
// gross unit price and line total a document including VAT shows).
func withVAT(v, rate *big.Rat) *big.Rat {
	factor := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(rate, hundred))
	return round2(new(big.Rat).Mul(v, factor))
}

// withoutVAT is a unit price typed including VAT as the net stored (O19):
// gross ÷ (1 + rate), rounded half-up to four decimals.
func withoutVAT(gross, rate *big.Rat) *big.Rat {
	factor := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(rate, hundred))
	return mustRat(new(big.Rat).Quo(gross, factor).FloatString(entryPlaces))
}
