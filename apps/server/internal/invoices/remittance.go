package invoices

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file reads what a bank line's remittance names (invoices phase 6
// design I14, plan reading 10): every structured reference a camt.054 line
// kept (remittance_refs), its text and its KID. It is pure: it reads no
// database, so its rule is tested on its own against the text a payer
// writes — amounts, dates, account numbers — and the split (split.go)
// judges what it names against the invoices.

// invoiceSeries is this installation's invoice numbers: from first, the
// settings' series start, up to next, the documents counter's next value,
// which no invoice has yet. Nothing is issued when next is first.
type invoiceSeries struct{ first, next int64 }

// remittanceRefs is what a line's remittance names: every token that is a
// valid KID, and every number of the series that stands as a whole token,
// each once, in the order first found.
type remittanceRefs struct {
	kids    []string
	numbers []int64
}

// maxRemittanceTokens bounds what one line is read for.
const maxRemittanceTokens = 50

// tokenBoundary is what may stand beside an invoice number besides
// whitespace and the text's start and end (I14). A comma, a dot and a
// hyphen are judged by what stands around them (wholeNumbers).
const tokenBoundary = ",;/:#+&()"

// currencyCodes are the words an amount follows, never an invoice number:
// "NOK 1001" is an amount.
var currencyCodes = []string{"nok", "kr", "eur", "usd", "sek", "dkk", "gbp", "chf", "isk", "pln"}

// amountOrDate is what is read away before any number is, each anchored at
// a word's start so it never takes the tail of an invoice number: an amount
// the currency follows ("3000 kr", "1 250,50 NOK"); an amount with
// thousands grouped by a space, a no-break space or a dot and its decimals
// ("1 250,00", "12.500,-"); a date ("17.11.2026", "17/11/26",
// "2026-11-17"); and an account number in its groups ("1234 56 78903").
var amountOrDate = regexp.MustCompile(`(?i)\b(?:\d{1,3}(?:[ \x{00A0}.]\d{3})+|\d+)(?:[,.](?:\d{1,2}|-))?[ \x{00A0}]*(?:kr|nok)\b|` +
	`\b\d{1,3}(?:[ \x{00A0}.]\d{3})+[,.](?:\d{1,2}|-)|` +
	`\b\d{1,2}[./-]\d{1,2}[./-]\d{2,4}\b|\b\d{4}-\d{1,2}-\d{1,2}\b|\b\d{4}[ .]\d{2}[ .]\d{5}\b`)

// referencesOf is what line's remittance names within series (I14): the
// KIDs — every token of its references, its text and its KID field that is
// a valid KID under MOD10 or MOD11 — and the invoice numbers of the series
// that stand in them as whole tokens (wholeNumbers).
func referencesOf(line store.InvoicesBankTransaction, series invoiceSeries) remittanceRefs {
	var out remittanceRefs
	texts := append(slices.Clone(line.RemittanceRefs), line.RemittanceText)
	if line.Kid != nil {
		texts = append(texts, *line.Kid)
	}
	for _, t := range texts {
		for _, tok := range strings.FieldsFunc(t, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(tokenBoundary, r)
		}) {
			if len(out.kids) < maxRemittanceTokens && validKid(tok) && !slices.Contains(out.kids, tok) {
				out.kids = append(out.kids, tok)
			}
		}
		for _, n := range wholeNumbers(t) {
			if n >= series.first && n < series.next && len(out.numbers) < maxRemittanceTokens && !slices.Contains(out.numbers, n) {
				out.numbers = append(out.numbers, n)
			}
		}
	}
	return out
}

// validKid reports whether tok is a KID whose check character verifies
// under MOD10 or MOD11 and whose body fits an invoice number: what
// invoiceOfKid then looks up and compares with the invoice's own KID.
func validKid(tok string) bool {
	body, _, fits, ok := kid.Parse(tok)
	if !ok || !fits {
		return false
	}
	check := tok[len(tok)-1]
	return check == kid.CheckMod10(body) || check == kid.CheckMod11(body)
}

// wholeNumbers is every number that stands in text as a whole token (I14,
// as the review of delivery B-2 settled the rule): a run of digits, not
// inside a longer one, bounded on each side by the text's start or end,
// whitespace or one of ",;/:#+&()", or by
//   - a "." or "-" followed by whitespace, the end or a non-digit;
//   - a "," or "." between two runs, which is a list ("1001,1002",
//     "1001.1002") unless the run after it is one or two digits, or it is
//     followed by "-": then it is a decimal separator, and neither run is a
//     number ("1250,00", "1001.50", "1250,-");
//   - a "-" between two runs of three digits or more, which names both ends
//     and never the numbers between ("1001-1002"); between shorter runs it
//     joins them, and neither is one.
//
// Never right after a currency code ("NOK 1001", "kr. 1001"), never part of
// an amount or a date read away first (amountOrDate), never with a leading
// zero, which no invoice number has, so a KID's digits are never one; at
// most 18 digits, which always fit an int64.
func wholeNumbers(text string) []int64 {
	rs := []rune(amountOrDate.ReplaceAllStringFunc(text, func(m string) string {
		return strings.Repeat(" ", len([]rune(m)))
	}))
	type digitRun struct {
		start, end int
		rejected   bool
	}
	var runs []digitRun
	for i := 0; i < len(rs); {
		if !isDigit(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isDigit(rs[j]) {
			j++
		}
		runs = append(runs, digitRun{start: i, end: j})
		i = j
	}
	plain := func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(tokenBoundary, r) }
	// adjacent[k] is whether one character alone stands between runs k and
	// k+1; separated[k] whether it separates them, two numbers.
	adjacent, separated := make([]bool, len(runs)), make([]bool, len(runs))
	for k := 0; k+1 < len(runs); k++ {
		a, b := &runs[k], &runs[k+1]
		if a.end+1 != b.start {
			continue
		}
		adjacent[k] = true
		switch c := rs[a.end]; {
		case c == ',' || c == '.':
			if b.end-b.start <= 2 {
				a.rejected, b.rejected = true, true
			} else {
				separated[k] = true
			}
		case c == '-':
			if a.end-a.start >= 3 && b.end-b.start >= 3 {
				separated[k] = true
			} else {
				a.rejected, b.rejected = true, true
			}
		case plain(c):
			separated[k] = true
		}
	}
	var out []int64
	for k, r := range runs {
		if r.rejected {
			continue
		}
		var left bool
		switch {
		case r.start == 0:
			left = true
		case k > 0 && adjacent[k-1]:
			left = separated[k-1]
		default:
			left = plain(rs[r.start-1])
		}
		var right bool
		switch {
		case r.end == len(rs):
			right = true
		case adjacent[k]:
			right = separated[k]
		default:
			c := rs[r.end]
			var next rune
			if r.end+1 < len(rs) {
				next = rs[r.end+1]
			}
			switch {
			case (c == ',' || c == '.') && next == '-':
				// "1250,-": a decimal separator.
			case c == '.' || c == '-':
				right = true // followed by whitespace, the end or a non-digit
			default:
				right = plain(c)
			}
		}
		switch {
		case !left, !right, rs[r.start] == '0', r.end-r.start > 18, afterCurrency(rs[:r.start]):
			continue
		}
		n, err := strconv.ParseInt(string(rs[r.start:r.end]), 10, 64)
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// afterCurrency reports whether before, the text before a number, ends in a
// currency code, with whitespace or a dot between them, or none.
func afterCurrency(before []rune) bool {
	k := len(before)
	for k > 0 && unicode.IsSpace(before[k-1]) {
		k--
	}
	if k > 0 && before[k-1] == '.' {
		k--
	}
	w := k
	for w > 0 && unicode.IsLetter(before[w-1]) {
		w--
	}
	return w < k && slices.Contains(currencyCodes, strings.ToLower(string(before[w:k])))
}

// isDigit is an ASCII digit: invoice numbers are written with no other.
func isDigit(r rune) bool { return r >= '0' && r <= '9' }
