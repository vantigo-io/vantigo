package manufacturing

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is how a decimal crosses the wire and the database. Every
// quantity, time and amount of this module is exact decimal text on the wire
// (manufacturing design §11) — never a float — and a numeric in the database;
// between the two it is a big.Rat, as Orders' and Inventory's decimal helpers
// (internal/orders/decimal.go, internal/inventory/quantity.go) keep it,
// copied because no module imports another. Nothing is computed in floating
// point.

// The decimals each stored value has (the baseline migration's columns).
const (
	quantityPlaces = 3 // numeric(14,3): a BoM's output and a line's quantity
	minutePlaces   = 2 // numeric(10,2): setup, cleanup and minutes per cycle
	ratePlaces     = 2 // numeric(12,2): a work center's cost rates
	percentPlaces  = 2 // numeric(5,2): a line's scrap
	amountPlaces   = 2 // an amount at display, half-up (Rounding and codes)
)

var (
	maxQuantity = mustRat("99999999999.999")
	maxMinutes  = mustRat("99999999.99")
	maxRate     = mustRat("9999999999.99")
	maxScrap    = mustRat("99.99")
	hundred     = big.NewRat(100, 1)
	sixty       = big.NewRat(60, 1)
)

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("manufacturing: not a decimal: " + s)
	}
	return r
}

// decimalText matches what this module reads as a decimal: digits, with an
// optional sign and an optional fraction — never an exponent or a fraction
// bar, which big.Rat would also take.
var decimalText = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// parseDecimal is s as an exact decimal of at most places decimals, 0 or more
// and at most limit, or why it is not one. The caller judges a rule beyond
// that (more than 0).
func parseDecimal(s string, places int, limit *big.Rat) (*big.Rat, string) {
	s = strings.TrimSpace(s)
	if !decimalText.MatchString(s) {
		return nil, "Not a decimal number"
	}
	if _, fraction, ok := strings.Cut(s, "."); ok && len(fraction) > places {
		return nil, fmt.Sprintf("At most %d decimals", places)
	}
	r := mustRat(s)
	if r.Sign() < 0 {
		return nil, "Must be 0 or more"
	}
	if r.Cmp(limit) > 0 {
		return nil, "Must be at most " + limit.FloatString(places)
	}
	return r, ""
}

// parsePositive is parseDecimal for a value that must be more than 0.
func parsePositive(s string, places int, limit *big.Rat) (*big.Rat, string) {
	r, msg := parseDecimal(s, places, limit)
	if msg != "" {
		return nil, msg
	}
	if r.Sign() <= 0 {
		return nil, "Must be more than 0"
	}
	return r, ""
}

// numeric is v as a column's value at places decimals; nil is NULL. v has at
// most places decimals by the time it is stored, so nothing is rounded.
func numeric(v *big.Rat, places int) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	if err := n.Scan(v.FloatString(places)); err != nil {
		panic(fmt.Sprintf("manufacturing: %s is not a storable decimal: %v", v.FloatString(places), err))
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

// text is an exact decimal as the shortest text that says it at places:
// "34", "1.5", "0.125". It rounds half away from zero (big.Rat's rule), which
// is half-up for the non-negative values this module keeps.
func text(v *big.Rat, places int) string {
	s := v.FloatString(places)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

// numericText is a numeric column's value as text at places; NULL is "0".
func numericText(n pgtype.Numeric, places int) string {
	return text(ratOrZero(n), places)
}

// amountText is an amount at display: half-up at two decimals, both kept
// ("12.50").
func amountText(v *big.Rat) string { return v.FloatString(amountPlaces) }

// ceilPlaces is v rounded up to places decimals (M18's ceiling).
func ceilPlaces(v *big.Rat, places int) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	scaled := new(big.Rat).Mul(v, new(big.Rat).SetInt(scale))
	return new(big.Rat).SetFrac(ceilInt(scaled), scale)
}

// ceilInt is the smallest integer not less than v.
func ceilInt(v *big.Rat) *big.Int {
	q, m := new(big.Int).DivMod(v.Num(), v.Denom(), new(big.Int))
	if m.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// roundInt is v rounded half-up to an integer, for v ≥ 0.
func roundInt(v *big.Rat) *big.Int {
	half := new(big.Rat).Add(v, big.NewRat(1, 2))
	return new(big.Int).Quo(half.Num(), half.Denom())
}

// The cost-shaped values (M38): a field a caller without
// manufacturing:costs-view never sees is absent — an empty RawMessage, which
// the generated types omit — and one with no value is null.
var jsonNull = json.RawMessage("null")

// costJSON is a cost field's value: absent when hidden, null when v is nil,
// else v's text.
func costJSON(show bool, v *string) json.RawMessage {
	if !show {
		return nil
	}
	if v == nil {
		return jsonNull
	}
	b, _ := json.Marshal(*v)
	return b
}

// amountJSON is costJSON of an amount, at display.
func amountJSON(show bool, v *big.Rat) json.RawMessage {
	if v == nil {
		return costJSON(show, nil)
	}
	s := amountText(v)
	return costJSON(show, &s)
}
