package staff

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// The register's file (staff records plan, Task 3) is customers' form, ported:
// these are customers' own csvfile tests, run against this module's copy, plus
// what the copy adds — the refusals carry the module's codes, and a
// comma-separated file is told apart before a row is read.

// The cell rule, verbatim: a cell a spreadsheet would run as a formula gets an
// apostrophe, and a cell holding the separator, a quote or a line break is
// quoted with its quotes doubled — the guard first, so a quoted formula is
// still guarded.
func TestCSVCell_GuardsFormulasAndQuotesWhatWouldEndTheCell(t *testing.T) {
	for in, want := range map[string]string{
		"Kari Nordmann":     "Kari Nordmann",
		"":                  "",
		"=SUM(A1)":          "'=SUM(A1)",
		"+47 22 33 44 55":   "'+47 22 33 44 55",
		"-5":                "'-5",
		"@home":             "'@home",
		"\tindented":        "'\tindented",
		"\rcarriage":        "\"'\rcarriage\"",
		"a;b":               `"a;b"`,
		`say "hi"`:          `"say ""hi"""`,
		"two\nlines":        "\"two\nlines\"",
		"=a;b":              `"'=a;b"`,
		"not-a-formula = 1": "not-a-formula = 1",
	} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q) = %q, want %q", in, got, want)
		}
	}
}

// The importer reads back what the export wrote, so "'+47 …" is a phone
// number again — but an apostrophe a person typed in front of anything else is
// theirs, and stays.
func TestCSVUnguard_TakesOffOnlyTheApostropheTheGuardPutOn(t *testing.T) {
	for in, want := range map[string]string{
		"'+47 22 33 44 55": "+47 22 33 44 55",
		"'=SUM(A1)":        "=SUM(A1)",
		"'-5":              "-5",
		"'@home":           "@home",
		"'\tx":             "\tx",
		"'s-Hertogenbosch": "'s-Hertogenbosch",
		"'":                "'",
		"O'Brien":          "O'Brien",
		"":                 "",
	} {
		if got := csvUnguard(in); got != want {
			t.Errorf("csvUnguard(%q) = %q, want %q", in, got, want)
		}
	}
}

// The writer's file is the Expenses form: a byte order mark, semicolons and
// CRLF after every row, the header included.
func TestWriteCSVRow_SemicolonsAndCRLF(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, []string{"employeeNumber", "givenName"})
	writeCSVRow(&b, []string{"E-1", "Kari"})
	if got, want := b.String(), "\ufeffemployeeNumber;givenName\r\nE-1;Kari\r\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// The round trip the import exists for: a file written the export's way —
// BOM, semicolons, CRLF, quoting, the guard — reads back as the very cells
// that went in, numbered from 1. encoding/csv folds a quoted CRLF to LF, so
// the line break is written as LF.
func TestReadCSVFile_ReadsBackWhatTheWriterWrites(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, []string{"street", "phoneWork"})
	writeCSVRow(&b, []string{`Storgata 1; "bakgården"`, "+47 22 33 44 55"})
	writeCSVRow(&b, []string{"to\nlinjer", "=1+1"})

	file, f := readCSVFile(b.Bytes(), 5000)
	if f.failed() {
		t.Fatalf("refusal = %v, want none", f.errs)
	}
	if !reflect.DeepEqual(file.Header, []string{"street", "phoneWork"}) {
		t.Errorf("header = %q", file.Header)
	}
	want := []csvRecord{
		{Row: 1, Cells: []string{`Storgata 1; "bakgården"`, "+47 22 33 44 55"}},
		{Row: 2, Cells: []string{"to\nlinjer", "=1+1"}},
	}
	if !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
	}
}

// A file saved by something other than the export still reads: LF, no BOM. A
// record whose every cell is blank — the trailing ";;;" rows a spreadsheet
// leaves — is not a row: it is skipped and takes no number.
func TestReadCSVFile_TakesLFAndNoBOM_AndSkipsBlankRecordsUnnumbered(t *testing.T) {
	file, f := readCSVFile([]byte("givenName;workEmail\nA;a@x.no\n\n; \nB;\n"), 5000)
	if f.failed() {
		t.Fatalf("refusal = %v, want none", f.errs)
	}
	want := []csvRecord{{Row: 1, Cells: []string{"A", "a@x.no"}}, {Row: 2, Cells: []string{"B", ""}}}
	if !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
	}
}

// Each way a file is not one the import reads is a sentence on "file" a person
// can act on, the coded ones with the module's code: a comma-separated file
// is import_not_semicolon before a row is read, and more rows than the bound
// import_too_many_rows before the rest is.
func TestReadCSVFile_RefusesWhatIsNotARegisterFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		code string
		want string
	}{
		{"empty", "", "", "The file is empty; it needs a header row naming its columns"},
		{"only a BOM", csvByteOrderMark, "", "The file is empty; it needs a header row naming its columns"},
		{"not UTF-8", "givenName\n\xff\xfe\n", "", "The file is not UTF-8 text; save it from the spreadsheet as CSV UTF-8"},
		{"a bare quote", "givenName\nA \"b\" c\n", "", "The file is not a semicolon-separated CSV file: "},
		{"comma-separated", "employeeNumber,givenName\nE-1,Kari\n", codeImportNotSemicolon, "The file is comma-separated"},
		{"comma-separated with a bare quote after", "employeeNumber,givenName\nE-1,\"Ka\"ri\n", codeImportNotSemicolon, "The file is comma-separated"},
		{"past the bound", "givenName\nA\nB\nC\n", codeImportTooManyRows, "The file holds more than 2 rows"},
	} {
		_, f := readCSVFile([]byte(tc.data), 2)
		msgs := f.errs["file"]
		if len(msgs) != 1 || !strings.HasPrefix(msgs[0], tc.want) || f.code != tc.code {
			t.Errorf("%s: refusal = %v %q, want one on file starting %q with code %q", tc.name, f.errs, f.code, tc.want, tc.code)
		}
	}
	if _, f := readCSVFile([]byte("givenName\nA\nB\n"), 2); f.failed() {
		t.Errorf("exactly at the bound: refusal = %v, want none", f.errs)
	}
	if _, f := readCSVFile([]byte("givenName\nKari, Ola\n"), 2); f.failed() {
		t.Errorf("a comma in a cell of a semicolon file: refusal = %v, want none", f.errs)
	}
}

// A template, or a file never filled in, has a header and no rows: not the
// "empty" refusal.
func TestReadCSVFile_HeaderOnlyFileHasNoDataRows(t *testing.T) {
	file, f := readCSVFile([]byte("givenName;workEmail\n"), 5000)
	if f.failed() {
		t.Fatalf("refusal = %v, want none", f.errs)
	}
	if !reflect.DeepEqual(file.Header, []string{"givenName", "workEmail"}) || len(file.Rows) != 0 {
		t.Errorf("file = %+v, want the header and no rows", file)
	}
}

// FieldsPerRecord = -1: a row is never refused, padded or truncated for
// disagreeing with the header's width; the import judges it row by row.
func TestReadCSVFile_RowsKeepTheirOwnWidth(t *testing.T) {
	file, f := readCSVFile([]byte("givenName;workEmail;phoneWork\nA;a@x.no\nB;b@x.no;123;extra\n"), 5000)
	if f.failed() {
		t.Fatalf("refusal = %v, want none", f.errs)
	}
	want := []csvRecord{
		{Row: 1, Cells: []string{"A", "a@x.no"}},
		{Row: 2, Cells: []string{"B", "b@x.no", "123", "extra"}},
	}
	if !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
	}
}

// A lone CR inside a quoted cell comes back exactly as written.
func TestReadCSVFile_ABareCRInAQuotedCellRoundTrips(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, []string{"street"})
	writeCSVRow(&b, []string{"a\rb"})
	file, f := readCSVFile(b.Bytes(), 5000)
	if f.failed() {
		t.Fatalf("refusal = %v, want none", f.errs)
	}
	if want := []csvRecord{{Row: 1, Cells: []string{"a\rb"}}}; !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
	}
}

// Numbers leave with the decimal comma and two decimals, and come back from
// whichever mark the spreadsheet used — never both, and never a lone point
// before exactly three digits ("1.250"), which is ambiguous.
func TestCSVDecimal_WritesTheDecimalCommaAndReadsEitherMark(t *testing.T) {
	v := 37.5
	if got := formatCSVDecimal(&v); got != "37,50" {
		t.Errorf("formatCSVDecimal(37.5) = %q, want 37,50", got)
	}
	if got := formatCSVDecimal(nil); got != "" {
		t.Errorf("formatCSVDecimal(nil) = %q, want empty", got)
	}
	for in, want := range map[string]float64{"37,5": 37.5, "37.5": 37.5, " 1 250,50 ": 1250.5, "100": 100, "-3": -3} {
		if got, ok := parseCSVDecimal(in); !ok || got != want {
			t.Errorf("parseCSVDecimal(%q) = %v, %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"1.250,50", "1.250", "abc", "", "NaN", "Inf", "1e3", "12,5,0"} {
		if got, ok := parseCSVDecimal(in); ok {
			t.Errorf("parseCSVDecimal(%q) = %v, want refused", in, got)
		}
	}
}
