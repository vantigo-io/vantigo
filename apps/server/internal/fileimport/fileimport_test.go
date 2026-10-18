package fileimport

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// This file is the reader of the person's file (customers import wizard
// design D2), without a server: the encoding, the separator, the header, the
// rows, the caps, the formula guard and the header key.

// testLimits are wide enough for every file below but the refusals'.
var testLimits = Limits{MaxBytes: 50 * 1024 * 1024, MaxRows: 100_000, MaxColumns: 200}

func TestRead_DetectsTheSeparatorFromTheHeader(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		text string
		sep  rune
		want []string
	}{
		{"semicolons", "name;email\r\nAcme;post@acme.no\r\n", ';', []string{"name", "email"}},
		{"commas", "name,email\nAcme,post@acme.no\n", ',', []string{"name", "email"}},
		{"tabs", "name\temail\nAcme\tpost@acme.no\n", '\t', []string{"name", "email"}},
		// A quoted comma does not count: two semicolons beat one bare comma.
		{"a comma inside quotes", "\"name, full\";email;phone\nAcme;a@b.no;1\n", ';', []string{"name, full", "email", "phone"}},
		// A tie goes to the semicolon, the export's own.
		{"a tie", "a;b,c\n1;2,3\n", ';', []string{"a", "b,c"}},
		{"no separator at all", "name\nAcme\n", ';', []string{"name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			file, refusal := Read([]byte(tc.text), testLimits)
			if refusal != "" {
				t.Fatalf("refused: %s", refusal)
			}
			if file.Separator != tc.sep {
				t.Errorf("separator = %q, want %q", file.Separator, tc.sep)
			}
			if strings.Join(file.Header, "|") != strings.Join(tc.want, "|") {
				t.Errorf("header = %q, want %q", file.Header, tc.want)
			}
			if len(file.Rows) != 1 {
				t.Errorf("rows = %d, want 1", len(file.Rows))
			}
		})
	}
}

func TestRead_ReadsUTF8WithOrWithoutABOM_AndWindows1252Otherwise(t *testing.T) {
	t.Parallel()
	utf8 := "name;city\r\nBlåbær AS;Tromsø\r\n"
	for _, tc := range []struct {
		name     string
		data     []byte
		encoding string
	}{
		{"utf-8", []byte(utf8), EncodingUTF8},
		{"utf-8 with a byte order mark", []byte(byteOrderMark + utf8), EncodingUTF8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			file, refusal := Read(tc.data, testLimits)
			if refusal != "" {
				t.Fatalf("refused: %s", refusal)
			}
			if file.Encoding != tc.encoding || file.Rows[0].Cells[0] != "Blåbær AS" || file.Rows[0].Cells[1] != "Tromsø" {
				t.Errorf("encoding %q cells %q", file.Encoding, file.Rows[0].Cells)
			}
		})
	}
	t.Run("windows-1252", func(t *testing.T) {
		t.Parallel()
		data, err := charmap.Windows1252.NewEncoder().Bytes([]byte(utf8))
		if err != nil {
			t.Fatal(err)
		}
		file, refusal := Read(data, testLimits)
		if refusal != "" {
			t.Fatalf("refused: %s", refusal)
		}
		if file.Encoding != EncodingWindows1252 || file.Rows[0].Cells[0] != "Blåbær AS" || file.Rows[0].Cells[1] != "Tromsø" {
			t.Errorf("encoding %q cells %q, want windows-1252 read as Blåbær AS / Tromsø", file.Encoding, file.Rows[0].Cells)
		}
	})
}

func TestRead_TheHeaderIsTheFirstNonBlankRow_AndBlankRowsTakeNoNumber(t *testing.T) {
	t.Parallel()
	text := "\r\n;;\r\n  name ;email;\r\nAcme;a@b.no;\r\n;;\r\n\"Quoted;Name\";\"two\r\nlines\";'=guarded\r\nShort\r\nLong;1;2;3\r\n"
	file, refusal := Read([]byte(text), testLimits)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	if got := strings.Join(file.Header, "|"); got != "name|email|" {
		t.Errorf("header = %q, want the names trimmed and the nameless column kept as blank", got)
	}
	if len(file.Rows) != 4 {
		t.Fatalf("rows = %d, want 4 (the blank ones skipped)", len(file.Rows))
	}
	if file.Rows[0].Row != 1 || file.Rows[1].Row != 2 || file.Rows[2].Row != 3 || file.Rows[3].Row != 4 {
		t.Errorf("row numbers = %d %d %d %d, want 1 2 3 4", file.Rows[0].Row, file.Rows[1].Row, file.Rows[2].Row, file.Rows[3].Row)
	}
	if r := file.Rows[1].Cells; r[0] != "Quoted;Name" || r[1] != "two\nlines" || r[2] != "=guarded" {
		t.Errorf("quoted row = %q, want the separator kept, the line break kept as encoding/csv normalises it, and the guard taken off", r)
	}
	// A row is kept as wide as it is; the mapping decides what that means.
	if len(file.Rows[2].Cells) != 1 || len(file.Rows[3].Cells) != 4 {
		t.Errorf("short row has %d cells and long row %d, want 1 and 4", len(file.Rows[2].Cells), len(file.Rows[3].Cells))
	}
}

// TestRead_Refusals: the caps are the caller's, and a refusal names the cap
// it was given.
func TestRead_Refusals(t *testing.T) {
	t.Parallel()
	limits := Limits{MaxRows: 3, MaxColumns: 4}
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"empty", "", EmptyMessage},
		{"only blank rows", "\r\n;;\r\n", EmptyMessage},
		{"too wide", "a;b;c;d;e\n", "The file has more than 4 columns, which is more than one import takes"},
		{"too many rows", "name\nx\nx\nx\nx\n", "The file holds more than 3 rows, which is more than one import takes; import it in slices of at most 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, refusal := Read([]byte(tc.data), limits)
			if refusal != tc.want {
				t.Errorf("refusal = %q, want %q", refusal, tc.want)
			}
		})
	}
	// Exactly at the caps is read.
	file, refusal := Read([]byte("a;b;c;d\nx\nx\nx\n"), limits)
	if refusal != "" || len(file.Header) != limits.MaxColumns || len(file.Rows) != limits.MaxRows {
		t.Errorf("at the caps: refusal %q, %d columns, %d rows", refusal, len(file.Header), len(file.Rows))
	}
}

// TestUnguard_TakesOffOnlyTheApostropheTheGuardPutOn: the importer reads back
// what an export wrote, so "'+47 …" is a phone number again — but an
// apostrophe a person typed in front of anything else is theirs, and stays.
func TestUnguard_TakesOffOnlyTheApostropheTheGuardPutOn(t *testing.T) {
	t.Parallel()
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
		if got := Unguard(in); got != want {
			t.Errorf("Unguard(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeaderKey_DropsEverythingButLettersAndDigits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"Org.nr", "orgnr"}, {"ORG NR", "orgnr"}, {" E-post ", "epost"}, {"Kjøperreferanse", "kjøperreferanse"}, {"Adresse 1", "adresse1"},
	} {
		if got := HeaderKey(tc.in); got != tc.want {
			t.Errorf("HeaderKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
