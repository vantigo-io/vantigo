package invoices

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is D5's arithmetic, and nothing else: pure functions over exact
// decimals (math/big.Rat), with no database, no request and no float in them.
// Every rounding is to two decimals, the half away from zero — big.Rat's own
// FloatString rule, and the codebase's (docs/expenses.md).

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

// round2 is v at two decimals, the half away from zero.
func round2(v *big.Rat) *big.Rat { return mustRat(v.FloatString(2)) }

// lineAmounts is one line's gross, its discount as an allowance, and its net.
// A line is stored as gross minus allowance rather than as one rounded
// product: EHF expresses a line discount as an allowance, and
// round(qty × price) − round(allowance) can differ from round(qty × price ×
// (1 − d)) by an øre, which PEPPOL-EN16931-R120 checks. Storing the two parts
// makes the PDF and a later EHF agree by construction.
type lineAmounts struct {
	gross, allowance, net *big.Rat
}

// computeLine is D5's line rule: line_gross = round(quantity × unit_price),
// line_allowance = round(line_gross × discount / 100), line_net = gross −
// allowance.
func computeLine(quantity, unitPrice, discountPercent *big.Rat) lineAmounts {
	gross := round2(new(big.Rat).Mul(quantity, unitPrice))
	allowance := round2(new(big.Rat).Quo(new(big.Rat).Mul(gross, discountPercent), big.NewRat(100, 1)))
	return lineAmounts{gross: gross, allowance: allowance, net: new(big.Rat).Sub(gross, allowance)}
}

// taxedLine is a line's net with the VAT treatment it carries.
type taxedLine struct {
	net      *big.Rat
	category string
	rate     *big.Rat
	safT     string
	reason   *string
}

// vatSummary is one (category, rate) row of a document (D4, D5).
type vatSummary struct {
	category string
	rate     *big.Rat
	safT     string
	reason   *string
	taxable  *big.Rat
	vat      *big.Rat
	vatNOK   *big.Rat
}

// documentTotals are a document's four totals.
type documentTotals struct {
	net, vat, gross, vatNOK *big.Rat
}

// summarize groups the lines per (category, rate) and computes VAT per rate on
// the sum of the lines' nets (Peppol BR-CO-17), never per line: taxable =
// Σ line_net, vat = round(taxable × rate / 100), vat_nok = round(vat ×
// exchange rate). A 0 % category gets its row too (§ 5-1-5). There is no øre
// rounding of the total: payment is electronic.
//
// ambiguous is true when two lines share a (category, rate) but carry
// different SAF-T codes, so one summary row could not name its code
// (vat_codes_ambiguous, D6). The rows come highest rate first, then by
// category, so a document always lists them the same way.
func summarize(lines []taxedLine, exchangeRate *big.Rat) (rows []vatSummary, totals documentTotals, ambiguous bool) {
	type key struct{ category, rate string }
	byKey := map[key]*vatSummary{}
	var order []key
	for _, l := range lines {
		k := key{l.category, l.rate.FloatString(2)}
		row, ok := byKey[k]
		if !ok {
			row = &vatSummary{category: l.category, rate: l.rate, safT: l.safT, reason: l.reason, taxable: new(big.Rat)}
			byKey[k] = row
			order = append(order, k)
		} else if row.safT != l.safT {
			ambiguous = true
		}
		row.taxable.Add(row.taxable, l.net)
	}
	totals = documentTotals{net: new(big.Rat), vat: new(big.Rat), gross: new(big.Rat), vatNOK: new(big.Rat)}
	for _, k := range order {
		row := byKey[k]
		row.vat = round2(new(big.Rat).Quo(new(big.Rat).Mul(row.taxable, row.rate), big.NewRat(100, 1)))
		row.vatNOK = round2(new(big.Rat).Mul(row.vat, exchangeRate))
		totals.net.Add(totals.net, row.taxable)
		totals.vat.Add(totals.vat, row.vat)
		totals.vatNOK.Add(totals.vatNOK, row.vatNOK)
		rows = append(rows, *row)
	}
	totals.gross.Add(totals.net, totals.vat)
	sort.SliceStable(rows, func(i, j int) bool {
		if c := rows[i].rate.Cmp(rows[j].rate); c != 0 {
			return c > 0
		}
		return rows[i].category < rows[j].category
	})
	return rows, totals, ambiguous
}

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
