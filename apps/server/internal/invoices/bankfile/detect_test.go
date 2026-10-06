package bankfile_test

import (
	"bytes"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
)

// Detection reads the first non-blank line for OCR, after a byte-order
// mark, and the root element for camt.054 in either namespace; anything
// else is the one 400 (reading 20).
func TestBankFile_Detect(t *testing.T) {
	t.Parallel()
	ocr := fixture(t, "r4-example.ocr")
	crlf := bytes.ReplaceAll(ocr, []byte("\n"), []byte("\r\n"))
	bom := []byte("\xEF\xBB\xBF")
	camt := func(ns string) []byte {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<!-- a notification --><Document xmlns="` + ns + `"><BkToCstmrDbtCdtNtfctn/></Document>`)
	}
	const v02 = "urn:iso:std:iso:20022:tech:xsd:camt.054.001.02"
	const v08 = "urn:iso:std:iso:20022:tech:xsd:camt.054.001.08"
	for _, c := range []struct {
		name string
		in   []byte
		want bankfile.Format
	}{
		{"OCR", ocr, bankfile.FormatOCR},
		{"OCR with CR/LF", crlf, bankfile.FormatOCR},
		{"OCR with a BOM", append(append([]byte{}, bom...), crlf...), bankfile.FormatOCR},
		{"OCR after blank lines", append([]byte("\r\n  \n\t\r\n"), ocr...), bankfile.FormatOCR},
		{"camt.054.001.02", camt(v02), bankfile.FormatCamt054},
		{"camt.054.001.08", camt(v08), bankfile.FormatCamt054},
		{"camt.054.001.08 with a BOM", append(append([]byte{}, bom...), camt(v08)...), bankfile.FormatCamt054},
		{"camt.054 prefixed", []byte(`<n:Document xmlns:n="` + v02 + `"><n:BkToCstmrDbtCdtNtfctn/></n:Document>`), bankfile.FormatCamt054},
		{"a third namespace", camt("urn:iso:std:iso:20022:tech:xsd:camt.054.001.14"), ""},
		{"a Document of another message", camt("urn:iso:std:iso:20022:tech:xsd:camt.053.001.02"), ""},
		{"a Document of no namespace", []byte(`<Document><BkToCstmrDbtCdtNtfctn/></Document>`), ""},
		{"another root in the namespace", []byte(`<Notification xmlns="` + v02 + `"/>`), ""},
		{"an empty file", nil, ""},
		{"blank lines only", []byte("\n\r\n  \n"), ""},
		{"a BOM only", bom, ""},
		{"a CSV", []byte("date;amount;kid\n2026-10-06;1250,00;0010017\n"), ""},
		{"an 80-character line of another kind", []byte(bytes.Repeat([]byte("0"), 80)), ""},
		{"an OCR start cut to 79", ocr[:79], ""},
		{"an OCR start record not first", append([]byte("NY090020\n"), ocr...), ""},
		{"XML that breaks before its root", []byte(`<?xml version="1.0"?><`), ""},
	} {
		got, err := bankfile.Detect(c.in)
		if c.want != "" {
			if err != nil || got != c.want {
				t.Errorf("%s: Detect = %q, %v; want %q", c.name, got, err, c.want)
			}
			continue
		}
		if got != "" {
			t.Errorf("%s: Detect = %q, want nothing", c.name, got)
		}
		refusal(t, c.name, err, "file", "Not an OCR giro or camt.054 file")
	}
}

// Parse refuses a file past MaxBytes before reading it, whatever its
// format, and ParseOCR on its own does too; an unknown file is refused as
// Detect refuses it.
func TestBankFile_ParseLimitsAndDispatch(t *testing.T) {
	t.Parallel()
	pad := bytes.Repeat([]byte("\n"), bankfile.MaxBytes)
	ocr := append(fixture(t, "r4-example.ocr"), pad...)
	camt := append([]byte(`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.054.001.02"/>`), pad...)
	for name, b := range map[string][]byte{"an OCR file": ocr, "a camt.054 file": camt} {
		_, err := bankfile.Parse(b, today)
		refusal(t, name+" past MaxBytes", err, "file", "10 MiB")
	}
	_, err := bankfile.ParseOCR(ocr, today)
	refusal(t, "ParseOCR past MaxBytes", err, "file", "10 MiB")
	_, err = bankfile.Parse([]byte("a,b\n"), today)
	refusal(t, "a CSV", err, "file", "Not an OCR giro")
	f, err := bankfile.Parse(fixture(t, "r4-example.ocr"), today)
	if err != nil || f.Format != bankfile.FormatOCR || len(f.Transactions) != 2 {
		t.Errorf("Parse(r4-example.ocr) = %+v, %v", f, err)
	}
}
