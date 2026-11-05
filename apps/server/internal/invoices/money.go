package invoices

import (
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// This file is D5's arithmetic over exact decimals (math/big.Rat), with no
// database, no request and no float in it. Every rounding is to two decimals,
// the half away from zero — big.Rat's own FloatString rule, and the codebase's
// (docs/src/content/docs/en/reference/expenses.md).

// The bounds a document's amounts are held to before the database could
// overflow (D5).
var (
	maxLineGross  = mustRat("999999999.99")
	maxGrossTotal = mustRat("99999999999.99")
)

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("invoices: not a decimal: " + s)
	}
	return r
}

// The line and summary arithmetic is the platform's docmath (orders design
// O17), which Invoices and Orders share; these names keep Invoices' own.
type (
	lineAmounts    = docmath.LineAmounts
	taxedLine      = docmath.TaxedLine
	vatSummary     = docmath.VATSummary
	documentTotals = docmath.Totals
)

var (
	round2        = docmath.Round2
	computeLine   = docmath.Line
	summarize     = docmath.Summarize
	sortSummaries = docmath.SortSummaries
)

// ratFromNumeric reads a numeric column as the exact decimal it holds — Int ×
// 10^Exp, never through a float.
func ratFromNumeric(n pgtype.Numeric) (*big.Rat, error) {
	if !n.Valid || n.Int == nil {
		return nil, fmt.Errorf("invoices: read a stored decimal: the column holds no number")
	}
	if n.NaN || n.InfinityModifier != pgtype.Finite {
		return nil, fmt.Errorf("invoices: read a stored decimal: the column holds %v", n.InfinityModifier)
	}
	value := new(big.Rat).SetInt(n.Int)
	exp := n.Exp
	if exp < 0 {
		exp = -exp
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil))
	if n.Exp < 0 {
		return value.Quo(value, scale), nil
	}
	return value.Mul(value, scale), nil
}

// floatFromRat is an exact decimal onto the wire as a JSON number, at places
// decimals. The wire carries doubles; the arithmetic never did.
func floatFromRat(v *big.Rat, places int) float64 {
	f, _ := mustRat(v.FloatString(places)).Float64()
	return f
}
