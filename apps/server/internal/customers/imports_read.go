package customers

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// This file reads the file a person uploads (customers import wizard design
// D2): their file, with their encoding, their separator and their headers —
// not the export's. The export's reader (csvfile.go's readCSVFile) still
// serves the one caller that reads the export's own form; this one guesses
// nothing it cannot show. Whatever it decides — the encoding, the separator —
// is recorded on the import and said on the page, and the mapping table shows
// the columns it made, so a wrong guess is seen before a row is written.

// The upload's bounds (design D2). maxImportUploadRequestBytes is the router's
// cap for the operation (module.RouterOptions.BodyLimits): the file plus room
// for the multipart framing, the attachment upload's margin.
const (
	maxImportUploadBytes        = 50 * 1024 * 1024
	maxImportUploadRequestBytes = maxImportUploadBytes + 64*1024
	maxImportUploadRows         = 100_000
	maxImportUploadColumns      = 200
	// importSampleRows is how many rows the import carries as its sample: the
	// preview the mapping step is judged on.
	importSampleRows = 20
)

// The encodings a file is read as, as the import records them.
const (
	importEncodingUTF8        = "utf-8"
	importEncodingWindows1252 = "windows-1252"
)

// importSeparators are the separators detected, in the order a tie is broken:
// the semicolon first, the export's own and a Norwegian Excel's.
var importSeparators = []rune{';', ',', '\t'}

// importFile is a file read: how it was read, its header as written, and its
// data rows numbered from 1 with the formula guard taken off.
type importFile struct {
	Encoding  string
	Separator rune
	Header    []string
	Rows      []csvRecord
}

// The reader's refusals, each a sentence a person can act on (design D2).
const (
	importFileEmptyMessage       = "The file is empty; it needs a header row naming its columns"
	importFileTooWideMessage     = "The file has more than %d columns, which is more than one import takes"
	importFileTooManyRowsMessage = "The file holds more than %d rows, which is more than one import takes; import it in slices of at most %d"
	importFileUnreadableMessage  = "The file could not be read as CSV: %v"
	invalidImportUploadMessage   = "A customer import is one CSV file of at most 50 MB, sent as the multipart part named 'file'"
)

// decodeImportText is the file as text: UTF-8 when its bytes are, with a byte
// order mark taken off, and Windows-1252 otherwise — the encoding of every CSV
// a Norwegian Excel saved without "UTF-8" in the name, where æ, ø and å are
// the bytes UTF-8 refuses. Every byte is some Windows-1252 character, so this
// decoding never fails; the preview is where a wrong guess is seen.
func decodeImportText(data []byte) (string, string) {
	data = bytes.TrimPrefix(data, []byte(csvByteOrderMark))
	if utf8.Valid(data) {
		return string(data), importEncodingUTF8
	}
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(data)
	if err != nil {
		// The decoder maps every byte, the five undefined ones to U+FFFD; it
		// does not fail. Should it ever, the bytes as they are beat nothing.
		return string(data), importEncodingWindows1252
	}
	return string(decoded), importEncodingWindows1252
}

// detectImportSeparator is the separator the header line uses: the one of
// the three that occurs most outside quotes, a tie going to the earlier of
// importSeparators. A line with none of them is one column, read with the
// semicolon.
func detectImportSeparator(line string) rune {
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
	best := importSeparators[0]
	for _, sep := range importSeparators[1:] {
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

// readImportFile reads data as the person's file (design D2): decoded, its
// separator detected, RFC 4180 quoting with lazy quotes (a stray quote in an
// unquoted cell is a cell with a quote in it, not a refused file), CRLF or
// LF, the first non-blank row the header, blank rows skipped and never
// numbered, the formula guard taken off every cell. The refusal is "" on
// success. A row is kept as wide as it is — shorter than the header or
// longer; the mapping decides what that means (import.go).
func readImportFile(data []byte) (importFile, string) {
	text, encoding := decodeImportText(data)
	sep := detectImportSeparator(firstLine(text))
	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = sep
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	file := importFile{Encoding: encoding, Separator: sep}
	for {
		cells, err := reader.Read()
		if errors.Is(err, io.EOF) {
			if file.Header == nil {
				return importFile{}, importFileEmptyMessage
			}
			return file, ""
		}
		if err != nil {
			return importFile{}, fmt.Sprintf(importFileUnreadableMessage, err)
		}
		if allBlank(cells) {
			continue
		}
		if file.Header == nil {
			if len(cells) > maxImportUploadColumns {
				return importFile{}, fmt.Sprintf(importFileTooWideMessage, maxImportUploadColumns)
			}
			for i, cell := range cells {
				cells[i] = strings.TrimSpace(cell)
			}
			file.Header = cells
			continue
		}
		if len(file.Rows) == maxImportUploadRows {
			return importFile{}, fmt.Sprintf(importFileTooManyRowsMessage, maxImportUploadRows, maxImportUploadRows)
		}
		for i, cell := range cells {
			cells[i] = csvUnguard(cell)
		}
		file.Rows = append(file.Rows, csvRecord{Row: len(file.Rows) + 1, Cells: cells})
	}
}
