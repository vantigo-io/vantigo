package accounting

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// Every amount crosses this module as exact decimal text — the contract's,
// the columns' (read ::text, written ::text::numeric) and the contracts' — and
// is computed as a big.Rat, never a float.

// amountPattern is an amount as the contract takes it: signed, at most two
// decimals, at most twelve digits before the point (numeric(14,2)).
var amountPattern = regexp.MustCompile(`^-?[0-9]{1,12}(\.[0-9]{1,2})?$`)

// parseAmount is text as an exact amount, or false when it is not one.
func parseAmount(text string) (*big.Rat, bool) {
	text = strings.TrimSpace(text)
	if !amountPattern.MatchString(text) {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(text)
	return r, ok
}

// mustRat is a column's decimal text as a big.Rat; a column always holds one.
func mustRat(text string) *big.Rat {
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return new(big.Rat)
	}
	return r
}

// money is r at two decimals, the half away from zero, with no "-0.00".
func money(r *big.Rat) string {
	r = docmath.Round2(r)
	if r.Sign() == 0 {
		return "0.00"
	}
	return r.FloatString(2)
}

// ratPercent is a rate's decimal text as a big.Rat.
func ratPercent(text string) *big.Rat { return mustRat(text) }

var hundred = big.NewRat(100, 1)

// add, sub and neg are big.Rat arithmetic that leaves its operands alone.
func add(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func sub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func neg(a *big.Rat) *big.Rat    { return new(big.Rat).Neg(a) }
func abs(a *big.Rat) *big.Rat    { return new(big.Rat).Abs(a) }

// taxOf is the tax and the base of an entered amount at rate percent (A6):
// net, the tax is round(amount × rate / 100) on the amount as the base;
// gross, the tax is round(amount × rate / (100 + rate)) and the base is the
// rest — docmath's rounding, half away from zero, each way.
func taxOf(amount, rate *big.Rat, gross bool) (base, tax *big.Rat) {
	if gross {
		tax = docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(amount, rate), add(hundred, rate)))
		return sub(amount, tax), tax
	}
	tax = docmath.Round2(new(big.Rat).Quo(new(big.Rat).Mul(amount, rate), hundred))
	return new(big.Rat).Set(amount), tax
}

// numText is a nullable numeric column as its decimal text, nil for NULL.
func numText(n pgtype.Numeric) *string {
	if !n.Valid {
		return nil
	}
	v, err := n.Value()
	if err != nil || v == nil {
		return nil
	}
	text := fmt.Sprint(v)
	return &text
}
