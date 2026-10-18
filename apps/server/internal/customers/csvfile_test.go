package customers

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/fileimport"
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

// TestReadImportFile_ReadsBackWhatTheWriterWrites: the export's own form —
// a quoted separator and quotes, a line break, a guarded formula, a bare CR —
// comes back from the upload's reader (internal/fileimport) as the values it was written from
// (customers import wizard design D2), so an export re-imports as it is.
func TestReadImportFile_ReadsBackWhatTheWriterWrites(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, []string{"name", "phone", "note"})
	writeCSVRow(&b, []string{`Fjord; "Nord" AS`, "+47 22 33 44 55", "a\rb"})
	writeCSVRow(&b, []string{"to\nlinjer", "=1+1", ""})

	file, refusal := fileimport.Read(b.Bytes(), importUploadLimits)
	if refusal != "" {
		t.Fatalf("refusal = %q, want none", refusal)
	}
	if !reflect.DeepEqual(file.Header, []string{"name", "phone", "note"}) || file.Separator != ';' {
		t.Errorf("header = %q separator %q", file.Header, file.Separator)
	}
	want := []fileimport.Record{
		{Row: 1, Cells: []string{`Fjord; "Nord" AS`, "+47 22 33 44 55", "a\rb"}},
		{Row: 2, Cells: []string{"to\nlinjer", "=1+1", ""}},
	}
	if !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
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
