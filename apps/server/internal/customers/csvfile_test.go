package customers

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestCustomerCSVColumns_AreDesignD1sInOrder pins the file's columns — the
// API's own JSON names, in D1's group order, export-only last. The export's
// header, the template and the importer's layout all read this one table, so a
// column renamed here is renamed in all three at once, and this test is where
// that shows.
func TestCustomerCSVColumns_AreDesignD1sInOrder(t *testing.T) {
	want := []string{
		"customerNumber", "name", "type", "status",
		"legalCountry", "legalType", "legalId", "legalName",
		"email", "phone", "website",
		"postalLine1", "postalLine2", "postalPostalCode", "postalCity", "postalRegion", "postalCountry",
		"invoiceLine1", "invoiceLine2", "invoicePostalCode", "invoiceCity", "invoiceRegion", "invoiceCountry",
		"invoiceEmail", "reminderEmail", "paymentTermsDays", "currency", "language", "invoiceDelivery",
		"reminderDelivery", "peppolId", "gln", "buyerReference", "defaultBillRate",
		"group", "tags",
		"id", "ownerName", "createdAt", "updatedAt",
	}
	if got := csvColumnNames(customerCSVColumns); !reflect.DeepEqual(got, want) {
		t.Errorf("columns =\n%v\nwant\n%v", got, want)
	}
	if got := csvColumnsOf(csvGroupPostal); !reflect.DeepEqual(got, want[11:17]) {
		t.Errorf("postal columns = %v, want %v", got, want[11:17])
	}
}

// TestCSVCell_GuardsFormulasAndQuotesWhatWouldEndTheCell is the payroll
// export's cell rule, verbatim (design D1): a cell a spreadsheet would run as a
// formula gets an apostrophe, and a cell holding the separator, a quote or a
// line break is quoted with its quotes doubled — the guard first, so a quoted
// formula is still guarded.
func TestCSVCell_GuardsFormulasAndQuotesWhatWouldEndTheCell(t *testing.T) {
	for in, want := range map[string]string{
		"Fjord AS":          "Fjord AS",
		"":                  "",
		"=SUM(A1)":          "'=SUM(A1)",
		"+47 22 33 44 55":   "'+47 22 33 44 55",
		"-5":                "'-5",
		"@home":             "'@home",
		"\tindented":        "'\tindented",
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

// TestCSVUnguard_TakesOffOnlyTheApostropheTheGuardPutOn: the importer reads
// back what the export wrote, so "'+47 …" is a phone number again — but an
// apostrophe a person typed in front of anything else is theirs, and stays.
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

// TestReadCSVFile_ReadsBackWhatTheWriterWrites is the round trip the import
// exists for: a file written the export's way — BOM, semicolons, CRLF, quoting,
// the guard — reads back as the very cells that went in, numbered from 1.
// encoding/csv folds a quoted CRLF to LF, so the line break is written as LF.
func TestReadCSVFile_ReadsBackWhatTheWriterWrites(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, []string{"name", "phone"})
	writeCSVRow(&b, []string{`Fjord; "Nord" AS`, "+47 22 33 44 55"})
	writeCSVRow(&b, []string{"to\nlinjer", "=1+1"})

	file, refusal := readCSVFile(b.Bytes(), customersFileMaxRows)
	if refusal != "" {
		t.Fatalf("refusal = %q, want none", refusal)
	}
	if !reflect.DeepEqual(file.Header, []string{"name", "phone"}) {
		t.Errorf("header = %q", file.Header)
	}
	want := []csvRecord{
		{Row: 1, Cells: []string{`Fjord; "Nord" AS`, "+47 22 33 44 55"}},
		{Row: 2, Cells: []string{"to\nlinjer", "=1+1"}},
	}
	if !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
	}
}

// TestReadCSVFile_TakesLFAndNoBOM_AndSkipsBlankRecordsUnnumbered: a file saved
// by something other than the export still reads. A record whose every cell is
// blank — the trailing ";;;" rows a spreadsheet leaves — is not a row: it is
// skipped and takes no number, so row 2 is the second row a person filled in,
// which is also how the browser numbers it (lib/csv.ts).
func TestReadCSVFile_TakesLFAndNoBOM_AndSkipsBlankRecordsUnnumbered(t *testing.T) {
	file, refusal := readCSVFile([]byte("name;email\nA;a@x.no\n\n; \nB;\n"), customersFileMaxRows)
	if refusal != "" {
		t.Fatalf("refusal = %q, want none", refusal)
	}
	want := []csvRecord{{Row: 1, Cells: []string{"A", "a@x.no"}}, {Row: 2, Cells: []string{"B", ""}}}
	if !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
	}
}

// TestReadCSVFile_RefusesWhatIsNotACustomersFile: each way a file is not one
// we can read is a sentence the person can act on — and a file with more rows
// than the cap is refused before a row is looked at.
func TestReadCSVFile_RefusesWhatIsNotACustomersFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "The file is empty; it needs a header row naming its columns"},
		{"only a BOM", csvByteOrderMark, "The file is empty; it needs a header row naming its columns"},
		{"not UTF-8", "name\n\xff\xfe\n", "The file is not UTF-8 text; save it from the spreadsheet as CSV UTF-8"},
		{"a bare quote", "name\nA \"b\" c\n", "The file is not a semicolon-separated CSV file: "},
		{"past the cap", "name\nA\nB\nC\n", "The file holds more than 2 rows, which is more than one import takes; import it in slices of at most 2"},
	} {
		_, refusal := readCSVFile([]byte(tc.data), 2)
		if !strings.HasPrefix(refusal, tc.want) {
			t.Errorf("%s: refusal = %q, want it to start %q", tc.name, refusal, tc.want)
		}
	}
	if _, refusal := readCSVFile([]byte("name\nA\nB\n"), 2); refusal != "" {
		t.Errorf("exactly at the cap: refusal = %q, want none", refusal)
	}
}

// TestCSVDecimal_WritesTheDecimalCommaAndReadsEitherMark: money leaves with
// the decimal comma a Norwegian spreadsheet expects and two decimals, and comes
// back from whichever mark the spreadsheet used — but only as plain digits: a
// thousands separator next to a decimal mark is ambiguous and refused, never
// guessed at, and so is a lone point before exactly three digits ("1.250").
func TestCSVDecimal_WritesTheDecimalCommaAndReadsEitherMark(t *testing.T) {
	rate := 1250.5
	if got := formatCSVDecimal(&rate); got != "1250,50" {
		t.Errorf("formatCSVDecimal(1250.5) = %q, want 1250,50", got)
	}
	if got := formatCSVDecimal(nil); got != "" {
		t.Errorf("formatCSVDecimal(nil) = %q, want empty", got)
	}
	for in, want := range map[string]float64{"1250,50": 1250.5, "1250.5": 1250.5, " 1 250,50 ": 1250.5, "1 250": 1250, "-3": -3} {
		if got, ok := parseCSVDecimal(in); !ok || got != want {
			t.Errorf("parseCSVDecimal(%q) = %v, %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"1.250,50", "1.250", "12.500", "abc", "", "NaN", "Inf", "1e3", "12,5,0"} {
		if got, ok := parseCSVDecimal(in); ok {
			t.Errorf("parseCSVDecimal(%q) = %v, want refused", in, got)
		}
	}
}
