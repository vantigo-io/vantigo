package inventory

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is how a quantity crosses the wire (design I11). HTTP numbers are
// number/double in this codebase, so a quantity arrives as a float64; it
// becomes an exact decimal through its shortest round-tripping text (strconv
// 'f', -1) and big.Rat.SetString, as Invoices' ratFromFloat does
// (internal/invoices/decimal.go, copied because no module imports another) —
// never SetFloat64, which keeps the binary value, so 0.1 would not be 0.1. No
// arithmetic on a quantity is done in floating point: a balance is read as a
// numeric, summed as a big.Rat, and written back as a numeric. The float64 a
// response carries is the decimal's nearest double, made only for the wire.

// quantityPlaces is how many decimals a quantity has: numeric(14,3).
const quantityPlaces = 3

// maxQuantity is the largest quantity numeric(14,3) holds.
var maxQuantity = mustRat("99999999999.999")

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("inventory: not a decimal: " + s)
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

// decimalPlaces is how many decimals a JSON number was written with, judged on
// the same shortest text ratFromFloat reads.
func decimalPlaces(v float64) int {
	text := strconv.FormatFloat(v, 'f', -1, 64)
	if _, decimals, ok := strings.Cut(text, "."); ok {
		return len(decimals)
	}
	return 0
}

// The ways a quantity may be read: a posted quantity is more than 0; a
// change is signed and not 0; a set on-hand is 0 or more.
type quantityRule int

const (
	positiveQuantity quantityRule = iota
	signedQuantity
	nonNegativeQuantity
)

// parseQuantity is v as an exact decimal under rule, or why it is not one
// (quantity_invalid): not finite, more than three decimals — refused, never
// rounded — out of the column's range, or on the wrong side of zero.
func parseQuantity(v float64, rule quantityRule) (*big.Rat, string) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, "The quantity is not a number"
	}
	if decimalPlaces(v) > quantityPlaces {
		return nil, fmt.Sprintf("The quantity %s has more than three decimals", strconv.FormatFloat(v, 'f', -1, 64))
	}
	r := ratFromFloat(v)
	abs := new(big.Rat).Abs(r)
	if abs.Cmp(maxQuantity) > 0 {
		return nil, "The quantity is more than 99,999,999,999.999"
	}
	switch rule {
	case positiveQuantity:
		if r.Sign() <= 0 {
			return nil, "The quantity must be more than 0"
		}
	case signedQuantity:
		if r.Sign() == 0 {
			return nil, "A change must not be 0"
		}
	case nonNegativeQuantity:
		if r.Sign() < 0 {
			return nil, "The new on hand must be 0 or more"
		}
	}
	return r, ""
}

// numericFromRat stores an exact decimal as a numeric(14,3) column's value.
// The decimal has at most three places by the time it gets here, so
// FloatString loses nothing.
func numericFromRat(v *big.Rat) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(v.FloatString(quantityPlaces)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("inventory: %s is not a storable quantity: %w", v.FloatString(quantityPlaces), err)
	}
	return n, nil
}

// ratFromNumeric is a numeric column's value as an exact decimal; NULL is 0.
func ratFromNumeric(n pgtype.Numeric) *big.Rat {
	if !n.Valid || n.Int == nil {
		return new(big.Rat)
	}
	r := new(big.Rat).SetInt(n.Int)
	if n.Exp == 0 {
		return r
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt32(n.Exp))), nil)
	if n.Exp > 0 {
		return r.Mul(r, new(big.Rat).SetInt(scale))
	}
	return r.Quo(r, new(big.Rat).SetInt(scale))
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// ratFromText is a numeric's text (a query's ::text) as an exact decimal; ""
// is 0.
func ratFromText(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return new(big.Rat)
	}
	return r
}

// wireQuantity is an exact decimal as the JSON number a response carries.
func wireQuantity(v *big.Rat) float64 {
	f, _ := strconv.ParseFloat(v.FloatString(quantityPlaces), 64)
	return f
}

// decimalText is an exact decimal as the shortest text that says it: "34",
// "1.5", "0.125" — how a quantity is named in a refusal's detail and in a
// VariantHold.
func decimalText(v *big.Rat) string {
	s := v.FloatString(quantityPlaces)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}
