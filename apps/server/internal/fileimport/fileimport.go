// Package fileimport reads the file a person uploads to an import: their file,
// with their encoding, their separator and their headers — not an export's.
// Whatever it decides — the encoding, the separator — is returned for the
// caller to record on its import and say on the page, and the caller's mapping
// table shows the columns it made, so a wrong guess is seen before a row is
// written (customers import wizard design D2).
//
// It is shared by the modules that import a file (energy import design D2):
// reading is the same everywhere, the import is each module's. It holds no SQL
// and no tables, imports no module, and leaves the caps to the caller (Limits):
// each module keeps its own imports, rows, worker and endpoints.
package fileimport

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

// The encodings a file is read as, as an import records them.
const (
	EncodingUTF8        = "utf-8"
	EncodingWindows1252 = "windows-1252"
)

// byteOrderMark is the one a UTF-8 file may start with: an export's own form
// writes it, and Excel's "CSV UTF-8" does too.
const byteOrderMark = "\ufeff"

// separators are the separators detected, in the order a tie is broken: the
// semicolon first, the customers export's own and a Norwegian Excel's.
var separators = []rune{';', ',', '\t'}

// The reader's refusals, each a sentence a person can act on (customers import
// wizard design D2). The wording is the customers import's, which its tests
// pin; a module that imports a file says the same.
const (
	EmptyMessage       = "The file is empty; it needs a header row naming its columns"
	TooWideMessage     = "The file has more than %d columns, which is more than one import takes"
	TooManyRowsMessage = "The file holds more than %d rows, which is more than one import takes; import it in slices of at most %d"
	UnreadableMessage  = "The file could not be read as CSV: %v"
)

// Limits are one import's bounds. MaxBytes is the upload's size, which the
// caller enforces while it reads the request (Read is given the bytes already
// read); MaxRows counts data rows, the header not included, and MaxColumns the
// header's cells. Each module sets its own: a consumption file is longer and
// narrower than a customer list.
type Limits struct {
	MaxBytes   int
	MaxRows    int
	MaxColumns int
}

// Record is one data row: its number as an import result reports it — the
// first row under the header is 1, blank rows taking no number — and its cells
// with the formula guard taken off.
type Record struct {
	Row   int
	Cells []string
}

// File is a file read: how it was read, its header as written (each name
// trimmed), and its data rows.
type File struct {
	Encoding  string
	Separator rune
	Header    []string
	Rows      []Record
}

// Read reads data as the person's file: decoded, its separator detected,
// RFC 4180 quoting with lazy quotes (a stray quote in an unquoted cell is a
// cell with a quote in it, not a refused file), CRLF or LF, the first
// non-blank row the header, blank rows skipped and never numbered, the formula
// guard taken off every cell (Unguard). The refusal is "" on success, else one
// of the messages above filled in from limits. A row is kept as wide as it is
// — shorter than the header or longer; the caller's mapping decides what that
// means.
func Read(data []byte, limits Limits) (File, string) {
	text, encoding := decode(data)
	sep := detectSeparator(firstLine(text))
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = sep
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	file := File{Encoding: encoding, Separator: sep}
	for {
		cells, err := reader.Read()
		if errors.Is(err, io.EOF) {
			if file.Header == nil {
				return File{}, EmptyMessage
			}
			return file, ""
		}
		if err != nil {
			return File{}, fmt.Sprintf(UnreadableMessage, err)
		}
		if AllBlank(cells) {
			continue
		}
		if file.Header == nil {
			if len(cells) > limits.MaxColumns {
				return File{}, fmt.Sprintf(TooWideMessage, limits.MaxColumns)
			}
			for i, cell := range cells {
				cells[i] = strings.TrimSpace(cell)
			}
			file.Header = cells
			continue
		}
		if len(file.Rows) == limits.MaxRows {
			return File{}, fmt.Sprintf(TooManyRowsMessage, limits.MaxRows, limits.MaxRows)
		}
		for i, cell := range cells {
			cells[i] = Unguard(cell)
		}
		file.Rows = append(file.Rows, Record{Row: len(file.Rows) + 1, Cells: cells})
	}
}

// decode is the file as text: UTF-8 when its bytes are, with a byte order mark
// taken off, and Windows-1252 otherwise — the encoding of every CSV a
// Norwegian Excel saved without "UTF-8" in the name, where æ, ø and å are the
// bytes UTF-8 refuses. Every byte is some Windows-1252 character, so this
// decoding never fails; the preview is where a wrong guess is seen.
func decode(data []byte) (string, string) {
	data = bytes.TrimPrefix(data, []byte(byteOrderMark))
	if utf8.Valid(data) {
		return string(data), EncodingUTF8
	}
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(data)
	if err != nil {
		// The decoder maps every byte, the five undefined ones to U+FFFD; it
		// does not fail. Should it ever, the bytes as they are beat nothing.
		return string(data), EncodingWindows1252
	}
	return string(decoded), EncodingWindows1252
}

// detectSeparator is the separator the header line uses: the one of the three
// that occurs most outside quotes, a tie going to the earlier of separators. A
// line with none of them is one column, read with the semicolon.
func detectSeparator(line string) rune {
	counts := map[rune]int{}
	quoted := false
	for _, r := range line {
		if r == '"' {
			quoted = !quoted
			continue
		}
		if !quoted {
			counts[r]++
		}
	}
	best := separators[0]
	for _, sep := range separators[1:] {
		if counts[sep] > counts[best] {
			best = sep
		}
	}
	return best
}

// firstLine is text's first line that is not blank, without its line end.
func firstLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// Unguard takes off the formula guard an export puts on a cell (an apostrophe
// in front of a cell a spreadsheet would read as a formula): an apostrophe in
// front of one of the six characters the guard protects is the guard's, and
// goes; any other apostrophe is the person's own and stays. A file an export
// wrote therefore imports as the values it was written from.
func Unguard(value string) string {
	if len(value) >= 2 && value[0] == '\'' && strings.IndexAny(value[1:2], "=+-@\t\r") == 0 {
		return value[1:]
	}
	return value
}

// AllBlank reports whether every cell is empty or whitespace.
func AllBlank(cells []string) bool {
	for _, cell := range cells {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// HeaderKey is a header as an alias table matches it (customers import wizard
// design D3): NFC, lowercased, and nothing but letters and digits, so "Org.nr",
// "org nr" and "ORGNR" are one word and punctuation and spacing never decide.
func HeaderKey(header string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(norm.NFC.String(header)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
