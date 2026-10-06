package bankfile

import (
	"bytes"
	"encoding/xml"
	"io"
)

// notABankFile is the one refusal of a file of neither format.
const notABankFile = "Not an OCR giro or camt.054 file"

// The camt.054 namespaces Vantigo reads, and the version each names.
var camtNamespaces = map[string]string{
	"urn:iso:std:iso:20022:tech:xsd:camt.054.001.02": "camt.054.001.02",
	"urn:iso:std:iso:20022:tech:xsd:camt.054.001.08": "camt.054.001.08",
}

var bom = []byte("\xEF\xBB\xBF")

// Detect names b's format (D3 step 2, reading 20), a UTF-8 byte-order mark
// dropped first: OCR giro when the first non-blank line, CR/LF stripped, is
// 80 characters beginning NY000010; camt.054 when the root element is
// Document in either namespace. Anything else is the one refusal.
func Detect(b []byte) (Format, error) {
	b = bytes.TrimPrefix(b, bom)
	first := b
	for len(first) > 0 {
		line, rest, _ := bytes.Cut(first, []byte("\n"))
		if len(bytes.TrimSpace(line)) > 0 {
			first = bytes.TrimRight(line, "\r")
			break
		}
		first = rest
	}
	if len(first) == 80 && bytes.HasPrefix(first, []byte("NY000010")) {
		return FormatOCR, nil
	}
	if _, ok := camtVersion(b); ok {
		return FormatCamt054, nil
	}
	return "", &Error{Where: "file", Message: notABankFile}
}

// camtVersion reads b up to its root element and answers the camt.054
// version that root names, if it is one: only the prolog and one start
// element are read, whatever follows.
func camtVersion(b []byte) (string, bool) {
	d := xml.NewDecoder(bytes.NewReader(b))
	// The root's name is ASCII in every encoding a bank declares; the
	// parser proper decides what the declaration may say.
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	for {
		tok, err := d.RawToken()
		if err != nil {
			return "", false
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local != "Document" {
				return "", false
			}
			ns := ""
			for _, a := range se.Attr {
				if (a.Name.Space == "" && a.Name.Local == "xmlns" && se.Name.Space == "") ||
					(a.Name.Space == "xmlns" && a.Name.Local == se.Name.Space) {
					ns = a.Value
				}
			}
			v, ok := camtNamespaces[ns]
			return v, ok
		}
	}
}
