package invoices

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is how a number crosses the wire into this module (D5). HTTP
// numbers are number/double in this codebase, so every amount arrives as a
// float64; it becomes an exact decimal through its shortest round-tripping
// text (strconv 'f', -1) and big.Rat.SetString — never SetFloat64, which keeps
// the binary value, so 0.1 would not be 0.1.

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

// finite is false for NaN and the infinities, which no JSON body can carry
// but a Go caller could.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// decimalText is v as the exact decimal text a numeric column stores, at
// places decimals, the half rounded away from zero — big.Rat's own rule.
func decimalText(v *big.Rat, places int) string { return v.FloatString(places) }

// numericFromRat stores an exact decimal as a column's value, at places
// decimals — the one road from this module's arithmetic into the database.
func numericFromRat(v *big.Rat, places int) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(decimalText(v, places)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("invoices: %v is not a storable decimal: %w", v, err)
	}
	return n, nil
}
