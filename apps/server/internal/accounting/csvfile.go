package accounting

import (
	"bytes"
	"strings"
)

// The accountant's file's form, Invoices' and Expenses' (duplicated, since
// depguard keeps modules from importing one another): semicolons, a byte
// order mark, CRLF after every row, RFC 4180 quoting, the decimal comma, and
// the formula guard on the text a person typed — never on an amount or a date.
const (
	csvByteOrderMark = "\ufeff"
	csvSeparator     = ';'
	csvLineEnd       = "\r\n"
)

// csvValue is one cell before it is written: its text, and whether the
// formula guard applies.
type csvValue struct {
	text  string
	guard bool
}

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
// anything that would otherwise end the cell or the row.
func csvCell(value string, guard bool) string {
	if guard && strings.IndexAny(value, "=+-@\t\r") == 0 {
		value = "'" + value
	}
	if !strings.ContainsAny(value, ";\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
