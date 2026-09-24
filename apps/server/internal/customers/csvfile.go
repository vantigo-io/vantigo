package customers

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file is the customers file (customers import/export design D1): one
// canonical CSV that GET /customers/export writes, GET /customers/import/template
// starts, and POST /customers/import reads back. Its form is the expenses
// payroll export's, verbatim — semicolons, a byte order mark, CRLF, RFC 4180
// quoting, the decimal comma and the formula guard — because that is the form
// a Norwegian Excel opens without an import dialog, and one form in the product
// is one thing to know. The constants and csvCell are duplicated from
// internal/expenses rather than shared: depguard keeps modules from importing
// one another, and ten lines of formatting are not worth a platform package.
//
// The header names are the API's own JSON names, so docs/customers.md's field
// tables describe the file too. No competitor's layout is documented anywhere
// this module could read it from, and one honest format beats three guessed
// ones: onboarding from Tripletex, Fiken or PowerOffice is "export there,
// rename the columns, import here".
const (
	csvByteOrderMark = "\ufeff"
	csvSeparator     = ';'
	csvLineEnd       = "\r\n"
	// csvTagSeparator joins a customer's tag names in the one tags cell. A tag
	// whose own name holds it cannot be named by a file — the only name no
	// file can reach, and a vocabulary word nobody has had reason to write.
	csvTagSeparator = "|"
	// customersFileMaxRows is the export's cap and the import's (design D2, D3):
	// the expenses precedent's five thousand, and one number for both so that a
	// file the export wrote always fits the import.
	customersFileMaxRows = 5000
)

// csvGroup is which part of a customer a column belongs to — the unit the
// import applies (design D3): a group is written through its own endpoint's
// rules, whole, or not at all. Its value is the words a refusal names it by.
type csvGroup string

const (
	csvGroupKey        csvGroup = "customer number"
	csvGroupRow        csvGroup = "customer"
	csvGroupIdentity   csvGroup = "legal identity"
	csvGroupContact    csvGroup = "contact info"
	csvGroupPostal     csvGroup = "postal address"
	csvGroupInvoice    csvGroup = "invoice address"
	csvGroupBilling    csvGroup = "billing profile"
	csvGroupMembership csvGroup = "group"
	csvGroupTags       csvGroup = "tags"
	csvGroupExportOnly csvGroup = "export only"
)

// csvImportGroups are the groups whose columns come together or not at all
// (import.go's importLayoutFor). The row's own name/type/status are not one of
// them: each already means something when it is absent. The key and the two
// relationship columns are a single column each.
var csvImportGroups = []csvGroup{csvGroupIdentity, csvGroupContact, csvGroupPostal, csvGroupInvoice, csvGroupBilling}

// csvColumn is one column of the file: its header name and its group.
type csvColumn struct {
	Name  string
	Group csvGroup
}

// customerCSVColumns is design D1's table, in its order. The export writes
// these (less the legal identity's four for a caller who may not see them),
// the template writes all but the export-only four, and the importer knows a
// column only if it is here.
var customerCSVColumns = []csvColumn{
	{"customerNumber", csvGroupKey},
	{"name", csvGroupRow}, {"type", csvGroupRow}, {"status", csvGroupRow},
	{"legalCountry", csvGroupIdentity}, {"legalType", csvGroupIdentity}, {"legalId", csvGroupIdentity}, {"legalName", csvGroupIdentity},
	{"email", csvGroupContact}, {"phone", csvGroupContact}, {"website", csvGroupContact},
	{"postalLine1", csvGroupPostal}, {"postalLine2", csvGroupPostal}, {"postalPostalCode", csvGroupPostal},
	{"postalCity", csvGroupPostal}, {"postalRegion", csvGroupPostal}, {"postalCountry", csvGroupPostal},
	{"invoiceLine1", csvGroupInvoice}, {"invoiceLine2", csvGroupInvoice}, {"invoicePostalCode", csvGroupInvoice},
	{"invoiceCity", csvGroupInvoice}, {"invoiceRegion", csvGroupInvoice}, {"invoiceCountry", csvGroupInvoice},
	{"invoiceEmail", csvGroupBilling}, {"reminderEmail", csvGroupBilling}, {"paymentTermsDays", csvGroupBilling},
	{"currency", csvGroupBilling}, {"language", csvGroupBilling}, {"invoiceDelivery", csvGroupBilling},
	{"reminderDelivery", csvGroupBilling}, {"peppolId", csvGroupBilling}, {"gln", csvGroupBilling},
	{"buyerReference", csvGroupBilling}, {"defaultBillRate", csvGroupBilling},
	{"group", csvGroupMembership}, {"tags", csvGroupTags},
	{"id", csvGroupExportOnly}, {"ownerName", csvGroupExportOnly}, {"createdAt", csvGroupExportOnly}, {"updatedAt", csvGroupExportOnly},
}

// csvColumnsOf is one group's column names, in the table's order.
func csvColumnsOf(g csvGroup) []string {
	var names []string
	for _, c := range customerCSVColumns {
		if c.Group == g {
			names = append(names, c.Name)
		}
	}
	return names
}

// csvColumnNames is columns' header row.
func csvColumnNames(columns []csvColumn) []string {
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = c.Name
	}
	return names
}

// writeCSVRow writes one row, each cell guarded and quoted as it needs.
func writeCSVRow(b *bytes.Buffer, cells []string) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteRune(csvSeparator)
		}
		b.WriteString(csvCell(cell))
	}
	b.WriteString(csvLineEnd)
}

// csvCell is one cell as it goes into the file: guarded against a spreadsheet
// reading it as a formula, then quoted if it holds anything that would
// otherwise end the cell or the row — internal/expenses' rule, word for word.
// Unlike the payroll file, this one carries values nobody vetted in almost
// every column (a phone number typed "+47 …" is exactly the case), which is why
// the importer takes the apostrophe off again (csvUnguard).
func csvCell(value string) string {
	if strings.IndexAny(value, "=+-@\t\r") == 0 {
		value = "'" + value
	}
	if !strings.ContainsAny(value, ";\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// csvUnguard is csvCell's guard taken off: an apostrophe in front of one of the
// six characters the guard protects is the guard's, and goes; any other
// apostrophe is the person's own and stays. A file the export wrote therefore
// imports as the values it was written from.
func csvUnguard(value string) string {
	if len(value) >= 2 && value[0] == '\'' && strings.IndexAny(value[1:2], "=+-@\t\r") == 0 {
		return value[1:]
	}
	return value
}

// csvDecimalPattern is the one shape a number cell may have once its spaces are
// gone and its decimal comma is a point: digits, optionally a sign, optionally
// a fraction. No exponent, no NaN, no thousands separator — ParseFloat would
// take all three, and a spreadsheet cell holding any of them is not a price.
var csvDecimalPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// csvThousandsPattern is the one shape parseCSVDecimal refuses as ambiguous:
// up to three digits, a point and exactly three digits.
var csvThousandsPattern = regexp.MustCompile(`^-?[0-9]{1,3}\.[0-9]{3}$`)

// formatCSVDecimal is money as the file carries it: two decimals and the
// decimal comma, the payroll export's csvAmount. nil is the empty cell.
func formatCSVDecimal(v *float64) string {
	if v == nil {
		return ""
	}
	return strings.Replace(strconv.FormatFloat(*v, 'f', 2, 64), ".", ",", 1)
}

// parseCSVDecimal reads a number cell back: whitespace dropped (a Norwegian
// spreadsheet groups thousands with a space, often a no-break one), and either
// mark taken as the decimal one — but never both, since "1.250,50" says
// nothing a reader can be sure of. That "never both" is enforced by
// csvDecimalPattern alone, with no separate check needed: only the first
// comma is turned into a point below, so a cell that already had a point
// ends up with two, and csvDecimalPattern accepts at most one — a decimal
// stricter than this would have to change that pattern, not add a check
// here. The value's own rules (greater than zero, two decimals) are its
// validator's, not this function's.
func parseCSVDecimal(raw string) (float64, bool) {
	compact := stripWhitespace(raw)
	// One point followed by exactly three digits after at most three — "1.250"
	// — is a thousands separator to one reader and a decimal to another, so it
	// is refused rather than read as 1.25. "1250.555" is a number with three
	// decimals, and its validator says so.
	if csvThousandsPattern.MatchString(compact) {
		return 0, false
	}
	compact = strings.Replace(compact, ",", ".", 1)
	if !csvDecimalPattern.MatchString(compact) {
		return 0, false
	}
	v, err := strconv.ParseFloat(compact, 64)
	return v, err == nil
}

// csvRecord is one data row: its number as an import result reports it — the
// first row under the header is 1 — and its cells with the guard taken off.
type csvRecord struct {
	Row   int
	Cells []string
}

// csvFile is a file read: its header as written, and its data rows.
type csvFile struct {
	Header []string
	Rows   []csvRecord
}

// The reader's refusals, each a sentence a person can act on.
const (
	csvEmptyMessage   = "The file is empty; it needs a header row naming its columns"
	csvNotUTF8Message = "The file is not UTF-8 text; save it from the spreadsheet as CSV UTF-8"
)

// readCSVFile reads data as the customers file: a byte order mark taken off if
// there is one, UTF-8 required, semicolons, CRLF or LF, RFC 4180 quoting. The
// refusal is "" on success. FieldsPerRecord is left at -1 (variable), so a row
// with fewer or more cells than the header is never itself a file-level
// error — a short row's Cells is simply shorter than Header, a long row's
// longer, neither padded nor truncated; a row that disagrees with the header
// this way is that row's problem to report (import.go), not the file's to
// refuse. A record whose every cell is blank is skipped and numbered not at
// all, and more than maxRows rows is refused before the rest is read.
func readCSVFile(data []byte, maxRows int) (csvFile, string) {
	data = bytes.TrimPrefix(data, []byte(csvByteOrderMark))
	if !utf8.Valid(data) {
		return csvFile{}, csvNotUTF8Message
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = csvSeparator
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return csvFile{}, csvEmptyMessage
	}
	if err != nil {
		return csvFile{}, fmt.Sprintf("The file is not a semicolon-separated CSV file: %v", err)
	}
	file := csvFile{Header: header}
	for {
		cells, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return file, ""
		}
		if err != nil {
			return csvFile{}, fmt.Sprintf("The file is not a semicolon-separated CSV file: %v", err)
		}
		if allBlank(cells) {
			continue
		}
		if len(file.Rows) == maxRows {
			return csvFile{}, fmt.Sprintf("The file holds more than %d rows, which is more than one import takes; import it in slices of at most %d", maxRows, maxRows)
		}
		for i, cell := range cells {
			cells[i] = csvUnguard(cell)
		}
		file.Rows = append(file.Rows, csvRecord{Row: len(file.Rows) + 1, Cells: cells})
	}
}

// allBlank reports whether every cell is empty or whitespace.
func allBlank(cells []string) bool {
	for _, cell := range cells {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}
