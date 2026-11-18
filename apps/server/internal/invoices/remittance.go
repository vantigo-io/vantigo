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
// whitespace and the text's start and end (I14).
const tokenBoundary = ",;/:#"

// currencyCodes are the words an amount follows, never an invoice number:
// "NOK 1001" is an amount.
var currencyCodes = []string{"nok", "kr", "eur", "usd", "sek", "dkk", "gbp", "chf", "isk", "pln"}

// amountOrDate is what is read away before any number is: an amount with
// thousands grouped by a space, a no-break space or a dot and its decimals
// ("1 250,00", "12.500,-"), a date ("17.11.2026", "17/11/26", "2026-11-17")
// and an account number in its groups ("1234 56 78903"), so no part of one
// is ever an invoice number.
var amountOrDate = regexp.MustCompile(`\d{1,3}(?:[ \x{00A0}.]\d{3})+[,.](?:\d{1,2}|-)|` +
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

// wholeNumbers is every number that stands in text as a whole token (I14):
// a run of digits, not inside a longer one, bounded on each side by the
// text's start or end, whitespace or one of ",;/:#"; not right after a
// currency code ("NOK 1001", "kr. 1001"); not beside a decimal separator
// (a comma or a dot between digits, or before "-": "1250,00", "1250,-"); not
// part of a grouped amount or a date; and never with a leading zero, which
// no invoice number has, so a KID's digits are never one. At most 18
// digits, which always fit an int64.
func wholeNumbers(text string) []int64 {
	rs := []rune(amountOrDate.ReplaceAllStringFunc(text, func(m string) string {
		return strings.Repeat(" ", len([]rune(m)))
	}))
	boundary := func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(tokenBoundary, r) }
	var out []int64
	for i := 0; i < len(rs); {
		if !isDigit(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isDigit(rs[j]) {
			j++
		}
		start, end := i, j
		i = j
		switch {
		case start > 0 && !boundary(rs[start-1]):
			continue
		case end < len(rs) && !boundary(rs[end]):
			continue
		case end < len(rs) && (rs[end] == ',') && end+1 < len(rs) && (isDigit(rs[end+1]) || rs[end+1] == '-'):
			continue
		case start > 1 && rs[start-1] == ',' && isDigit(rs[start-2]):
			continue
		case rs[start] == '0', end-start > 18:
			continue
		case afterCurrency(rs[:start]):
			continue
		}
		n, err := strconv.ParseInt(string(rs[start:end]), 10, 64)
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
