package staff

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file is the register's file (design S19; plan Task 3): one CSV that
// GET /people/export.csv writes, GET /people/import-template.csv starts and
// POST /people/import reads back. Its form is customers' (and before them the
// expenses payroll export's), ported because no module imports another —
// semicolons, a byte order mark, CRLF, RFC 4180 quoting, the decimal comma
// and the formula guard — since that is the form a Norwegian Excel opens
// without an import dialog, and one form in the product is one thing to know.
//
// The header names are the API's own JSON names where a column is a field of
// the person (StaffPersonRequest), so the reference's field tables describe
// the file too; the first employment's columns are the employment's and its
// terms' names, the structure named as a person reads it (a business unit by
// name or org number, a department by code, a location by name, a manager by
// employee number).

const (
	csvByteOrderMark = "\ufeff"
	csvSeparator     = ';'
	csvLineEnd       = "\r\n"
)

// csvGroup is which part of the register a column belongs to. Its value is
// the words a refusal names it by.
type csvGroup string

const (
	csvGroupKey        csvGroup = "employee number"
	csvGroupPerson     csvGroup = "person"
	csvGroupNationalID csvGroup = "national identity number"
	csvGroupBank       csvGroup = "bank account"
	csvGroupEmployment csvGroup = "first employment"
	csvGroupExportOnly csvGroup = "export only"
)

// csvColumn is one column of the file: its header name and its group.
type csvColumn struct {
	Name  string
	Group csvGroup
}

// registerColumns is the file's table, in its order. The export writes the
// key, the person's and the export-only columns; the template writes the key,
// the person's, the sealed ones for a staff:personal-data holder and the first
// employment's; the importer knows a column only if it is here.
var registerColumns = []csvColumn{
	{"employeeNumber", csvGroupKey},
	// The work group, then the private group (shaping.go's fieldGroups).
	{"givenName", csvGroupPerson}, {"familyName", csvGroupPerson}, {"displayName", csvGroupPerson},
	{"workEmail", csvGroupPerson}, {"phoneWork", csvGroupPerson}, {"workCountry", csvGroupPerson},
	{"preferredLanguage", csvGroupPerson},
	{"birthDate", csvGroupPerson}, {"gender", csvGroupPerson}, {"phoneMobile", csvGroupPerson},
	{"phonePrivate", csvGroupPerson}, {"privateEmail", csvGroupPerson}, {"street", csvGroupPerson},
	{"postalCode", csvGroupPerson}, {"city", csvGroupPerson}, {"country", csvGroupPerson},
	// The sealed identifiers: imported only by staff:personal-data, never
	// exported.
	{"nationalIdKind", csvGroupNationalID}, {"nationalIdCountry", csvGroupNationalID}, {"nationalId", csvGroupNationalID},
	{"bankAccountKind", csvGroupBank}, {"bankAccount", csvGroupBank},
	// The first employment and its first terms row, written on a create that
	// has a startDate; never exported, never read on an update.
	{"startDate", csvGroupEmployment}, {"businessUnit", csvGroupEmployment}, {"employmentType", csvGroupEmployment},
	{"temporaryGround", csvGroupEmployment}, {"expectedEndDate", csvGroupEmployment}, {"ftePercent", csvGroupEmployment},
	{"appointmentForm", csvGroupEmployment}, {"workingHoursScheme", csvGroupEmployment},
	{"fullTimeHoursPerWeek", csvGroupEmployment}, {"occupationCode", csvGroupEmployment}, {"title", csvGroupEmployment},
	{"department", csvGroupEmployment}, {"location", csvGroupEmployment}, {"managerEmployeeNumber", csvGroupEmployment},
	// Where the person stands today and whether a national identity number is
	// on file: the export writes them, the import ignores them.
	{"status", csvGroupExportOnly}, {"currentTitle", csvGroupExportOnly}, {"currentDepartment", csvGroupExportOnly},
	{"currentBusinessUnit", csvGroupExportOnly}, {"currentLocation", csvGroupExportOnly},
	{"currentManagerEmployeeNumber", csvGroupExportOnly}, {"currentFtePercent", csvGroupExportOnly},
	{"nationalIdOnFile", csvGroupExportOnly},
}

// csvColumnsWhere is the table's columns of the groups keep says yes to, in
// the table's order.
func csvColumnsWhere(keep func(csvGroup) bool) []csvColumn {
	var out []csvColumn
	for _, c := range registerColumns {
		if keep(c.Group) {
			out = append(out, c)
		}
	}
	return out
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
// otherwise end the cell or the row — customers' rule, word for word. A phone
// number typed "+47 …" is exactly the case, which is why the importer takes
// the apostrophe off again (csvUnguard).
func csvCell(value string) string {
	if strings.IndexAny(value, "=+-@\t\r") == 0 {
		value = "'" + value
	}
	if !strings.ContainsAny(value, ";\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// csvUnguard is csvCell's guard taken off: an apostrophe in front of one of
// the six characters the guard protects is the guard's, and goes; any other
// apostrophe is the person's own and stays. A file the export wrote therefore
// imports as the values it was written from.
func csvUnguard(value string) string {
	if len(value) >= 2 && value[0] == '\'' && strings.IndexAny(value[1:2], "=+-@\t\r") == 0 {
		return value[1:]
	}
	return value
}

// csvDecimalPattern is the one shape a number cell may have once its spaces
// are gone and its decimal comma is a point: digits, optionally a sign,
// optionally a fraction. No exponent, no NaN, no thousands separator.
var csvDecimalPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// csvThousandsPattern is the one shape parseCSVDecimal refuses as ambiguous:
// up to three digits, a point and exactly three digits.
var csvThousandsPattern = regexp.MustCompile(`^-?[0-9]{1,3}\.[0-9]{3}$`)

// formatCSVDecimal is a number as the file carries it: two decimals and the
// decimal comma. nil is the empty cell.
func formatCSVDecimal(v *float64) string {
	if v == nil {
		return ""
	}
	return strings.Replace(strconv.FormatFloat(*v, 'f', 2, 64), ".", ",", 1)
}

// parseCSVDecimal reads a number cell back: whitespace dropped (a Norwegian
// spreadsheet groups thousands with a space, often a no-break one), and either
// mark taken as the decimal one — but never both, which csvDecimalPattern
// enforces alone: only the first comma becomes a point, so a cell that already
// had a point ends with two. A lone point before exactly three digits is
// refused as ambiguous. The value's own rules are its validator's.
func parseCSVDecimal(raw string) (float64, bool) {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, raw)
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

// importFileField is the multipart part the file travels in, and the field
// every refusal of the file itself is reported on.
const importFileField = "file"

// The reader's refusals, each a sentence a person can act on.
const (
	csvEmptyMessage       = "The file is empty; it needs a header row naming its columns"
	csvNotUTF8Message     = "The file is not UTF-8 text; save it from the spreadsheet as CSV UTF-8"
	csvNotSemicolonPrefix = "The file is not a semicolon-separated CSV file: "
	csvCommaMessage       = "The file is comma-separated; the import reads semicolon-separated files, the form the export writes and a Norwegian Excel saves as CSV"
)

// readCSVFile reads data as the register's file: a byte order mark taken off
// if there is one, UTF-8 required, semicolons, CRLF or LF, RFC 4180 quoting.
// A header of one cell holding a comma is a comma-separated file, refused
// import_not_semicolon before a row is read. FieldsPerRecord is -1, so a row
// wider or narrower than the header is that row's problem (the import's), not
// the file's. A record whose every cell is blank is skipped and numbered not
// at all, and more than maxRows rows is import_too_many_rows before the rest
// is read. Every refusal is on "file".
func readCSVFile(data []byte, maxRows int) (csvFile, fieldErrors) {
	var f fieldErrors
	data = bytes.TrimPrefix(data, []byte(csvByteOrderMark))
	if !utf8.Valid(data) {
		f.add(importFileField, csvNotUTF8Message)
		return csvFile{}, f
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = csvSeparator
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		f.add(importFileField, csvEmptyMessage)
		return csvFile{}, f
	}
	if len(header) == 1 && strings.Contains(header[0], ",") {
		f.addCoded(importFileField, codeImportNotSemicolon, csvCommaMessage)
		return csvFile{}, f
	}
	if err != nil {
		f.add(importFileField, fmt.Sprintf("%s%v", csvNotSemicolonPrefix, err))
		return csvFile{}, f
	}
	file := csvFile{Header: header}
	for {
		cells, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return file, f
		}
		if err != nil {
			f.add(importFileField, fmt.Sprintf("%s%v", csvNotSemicolonPrefix, err))
			return csvFile{}, f
		}
		if allBlank(cells) {
			continue
		}
		if len(file.Rows) == maxRows {
			f.addCoded(importFileField, codeImportTooManyRows, fmt.Sprintf(
				"The file holds more than %d rows, which is more than one import takes; import it in slices of at most %d", maxRows, maxRows))
			return csvFile{}, f
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
