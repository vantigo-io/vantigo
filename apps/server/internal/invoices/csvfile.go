package invoices

import (
	"bytes"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is the accountant's file's form (payments and delivery design
// D5): the expenses payroll export's, which the customers file copies too —
// semicolons, a byte order mark, CRLF after every row, RFC 4180 quoting, the
// decimal comma and the formula guard — because that is the form a Norwegian
// Excel opens without an import dialog. The constants and csvCell are
// duplicated from internal/customers rather than shared: depguard keeps
// modules from importing one another.
//
// One difference, and it is the point: the guard is per cell, not per file.
// The payroll file guards every cell because none of its amounts is ever
// negative; this file's credit notes are, and a guarded -1234,50 is text in a
// spreadsheet, not a number. So the text columns — the ones a person typed
// somewhere — are guarded, and a number, a date, a rate or an amount never.
const (
	csvByteOrderMark = "\ufeff"
	csvSeparator     = ';'
	csvLineEnd       = "\r\n"
	// invoicesFileMaxRows is the export's cap (D5): the customers export's and
	// the payroll file's five thousand.
	invoicesFileMaxRows = 5000
)

// csvValue is one cell before it is written: its text, and whether it is a
// text column the formula guard applies to.
type csvValue struct {
	text  string
	guard bool
}

// writeCSVRow writes one row, each cell guarded when it is text and quoted as
// it needs.
func writeCSVRow(b *bytes.Buffer, cells []csvValue) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteRune(csvSeparator)
		}
		b.WriteString(csvCell(cell.text, cell.guard))
	}
	b.WriteString(csvLineEnd)
}

// csvCell is one cell as it goes into the file: when guard, an apostrophe in
// front of what a spreadsheet would read as a formula, then quotes if it holds
// anything that would otherwise end the cell or the row — internal/expenses'
// rule, word for word, with the guard made the caller's choice.
func csvCell(value string, guard bool) string {
	if guard && strings.IndexAny(value, "=+-@\t\r") == 0 {
		value = "'" + value
	}
	if !strings.ContainsAny(value, ";\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// csvDecimal is a stored numeric as the file carries it: exact, two places,
// the decimal comma, and negated for a credit note — negated as a number, so
// a credit note's 0,00 stays 0,00 rather than becoming -0,00.
func csvDecimal(n pgtype.Numeric, negate bool) (string, error) {
	r, err := ratFromNumeric(n)
	if err != nil {
		return "", err
	}
	return csvAmount(r, negate), nil
}

// csvAmount is an exact decimal with the decimal comma at two places, the
// half away from zero, negated when negate. It rounds first and negates the
// rounded value, so an amount that rounds to nothing is 0,00, never -0,00.
func csvAmount(r *big.Rat, negate bool) string {
	r = round2(r)
	if negate {
		r.Neg(r)
	}
	return strings.Replace(decimalText(r, 2), ".", ",", 1)
}
