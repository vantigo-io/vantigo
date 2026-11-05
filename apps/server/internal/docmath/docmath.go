// Package docmath is a sales document's arithmetic — a line's amounts and a
// document's VAT summary — lifted verbatim from Invoices (orders design O17,
// docs/superpowers/specs/2026-11-05-orders-design.md), so Invoices and Orders
// compute the same numbers by construction.
//
// It is pure functions over exact decimals (math/big.Rat), with no database,
// no request and no float in them. Every rounding is to two decimals, the half
// away from zero — big.Rat's own FloatString rule, and the codebase's
// (docs/src/content/docs/en/reference/expenses.md) — on purpose, for
// PEPPOL-EN16931-R120.
//
// It is a platform package: it imports no business module.
package docmath

import (
	"math/big"
	"sort"
)

// Round2 is v at two decimals, the half away from zero.
func Round2(v *big.Rat) *big.Rat {
	r, ok := new(big.Rat).SetString(v.FloatString(2))
	if !ok {
		panic("docmath: not a decimal: " + v.FloatString(2))
	}
	return r
}

// LineAmounts is one line's gross, its discount as an allowance, and its net.
// A line is stored as gross minus allowance rather than as one rounded
// product: EHF expresses a line discount as an allowance, and
// round(qty × price) − round(allowance) can differ from round(qty × price ×
// (1 − d)) by an øre, which PEPPOL-EN16931-R120 checks. Storing the two parts
// makes the PDF and a later EHF agree by construction.
type LineAmounts struct {
	Gross, Allowance, Net *big.Rat
}

// Line is the line rule: line_gross = round(quantity × unit_price),
// line_allowance = round(line_gross × discount / 100), line_net = gross −
// allowance.
func Line(quantity, unitPrice, discountPercent *big.Rat) LineAmounts {
	gross := Round2(new(big.Rat).Mul(quantity, unitPrice))
	allowance := Round2(new(big.Rat).Quo(new(big.Rat).Mul(gross, discountPercent), big.NewRat(100, 1)))
	return LineAmounts{Gross: gross, Allowance: allowance, Net: new(big.Rat).Sub(gross, allowance)}
}

// TaxedLine is a line's net with the VAT treatment it carries.
type TaxedLine struct {
	Net      *big.Rat
	Category string
	Rate     *big.Rat
	SAFT     string
	Reason   *string
}

// VATSummary is one (category, rate) row of a document.
type VATSummary struct {
	Category string
	Rate     *big.Rat
	SAFT     string
	Reason   *string
	Taxable  *big.Rat
	VAT      *big.Rat
	VATNOK   *big.Rat
}

// Totals are a document's four totals.
type Totals struct {
	Net, VAT, Gross, VATNOK *big.Rat
}

// Summarize groups the lines per (category, rate) and computes VAT per rate on
// the sum of the lines' nets (Peppol BR-CO-17), never per line: taxable =
// Σ line_net, vat = round(taxable × rate / 100), vat_nok = round(vat ×
// exchange rate). A 0 % category gets its row too (§ 5-1-5). There is no øre
// rounding of the total: payment is electronic.
//
// ambiguous is true when two lines share a (category, rate) but carry
// different SAF-T codes, so one summary row could not name its code
// (Invoices' vat_codes_ambiguous). Two codes may share a (category, rate) and
// a SAF-T code yet word their exemption reasons differently: the row then
// carries the first line's reason, in the order the lines are given — by
// position — and each line keeps its own code's reason in its snapshot. The
// rows come highest rate first, then by category, so a document always lists
// them the same way.
func Summarize(lines []TaxedLine, exchangeRate *big.Rat) (rows []VATSummary, totals Totals, ambiguous bool) {
	type key struct{ category, rate string }
	byKey := map[key]*VATSummary{}
	var order []key
	for _, l := range lines {
		k := key{l.Category, l.Rate.FloatString(2)}
		row, ok := byKey[k]
		if !ok {
			row = &VATSummary{Category: l.Category, Rate: l.Rate, SAFT: l.SAFT, Reason: l.Reason, Taxable: new(big.Rat)}
			byKey[k] = row
			order = append(order, k)
		} else if row.SAFT != l.SAFT {
			ambiguous = true
		}
		row.Taxable.Add(row.Taxable, l.Net)
	}
	totals = Totals{Net: new(big.Rat), VAT: new(big.Rat), Gross: new(big.Rat), VATNOK: new(big.Rat)}
	for _, k := range order {
		row := byKey[k]
		row.VAT = Round2(new(big.Rat).Quo(new(big.Rat).Mul(row.Taxable, row.Rate), big.NewRat(100, 1)))
		row.VATNOK = Round2(new(big.Rat).Mul(row.VAT, exchangeRate))
		totals.Net.Add(totals.Net, row.Taxable)
		totals.VAT.Add(totals.VAT, row.VAT)
		totals.VATNOK.Add(totals.VATNOK, row.VATNOK)
		rows = append(rows, *row)
	}
	totals.Gross.Add(totals.Net, totals.VAT)
	SortSummaries(rows)
	return rows, totals, ambiguous
}

// SortSummaries puts VAT summary rows in a document's order: the highest rate
// first, then by category.
func SortSummaries(rows []VATSummary) {
	sort.SliceStable(rows, func(i, j int) bool {
		if c := rows[i].Rate.Cmp(rows[j].Rate); c != 0 {
			return c > 0
		}
		return rows[i].Category < rows[j].Category
	})
}
