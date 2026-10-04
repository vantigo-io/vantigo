package ehf

import (
	"bytes"
	"encoding/xml"
	"math/big"
)

// writer is the hand writer over encoding/xml's encoder (EHF and KID design
// D4): element names are the literal prefixed strings ("cac:Party",
// "cbc:ID") and the root declares the three namespaces itself, because
// encoding/xml cannot emit prefixes from struct tags — and writing each
// element by hand makes the order explicit, which the UBL schema enforces.
// The encoder escapes the text; the writer adds nothing that varies between
// two runs over the same Document.
//
// The first error sticks, and every later call is a no-op; bytes answers it.
type writer struct {
	buf bytes.Buffer
	enc *xml.Encoder
	err error
}

func newWriter() *writer {
	w := &writer{}
	w.buf.WriteString(xml.Header)
	w.enc = xml.NewEncoder(&w.buf)
	w.enc.Indent("", "  ")
	return w
}

func attr(name, value string) xml.Attr { return xml.Attr{Name: xml.Name{Local: name}, Value: value} }

// start opens an element.
func (w *writer) start(name string, attrs ...xml.Attr) {
	if w.err == nil {
		w.err = w.enc.EncodeToken(xml.StartElement{Name: xml.Name{Local: name}, Attr: attrs})
	}
}

// end closes the element start opened.
func (w *writer) end(name string) {
	if w.err == nil {
		w.err = w.enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: name}})
	}
}

// leaf writes <name attrs>value</name> — and nothing at all for an empty
// value: Peppol refuses empty elements (PEPPOL-EN16931-R008).
func (w *writer) leaf(name, value string, attrs ...xml.Attr) {
	if value == "" || w.err != nil {
		return
	}
	w.start(name, attrs...)
	if w.err == nil {
		w.err = w.enc.EncodeToken(xml.CharData(value))
	}
	w.end(name)
}

// amount writes an amount with exactly two decimals and its currency.
func (w *writer) amount(name string, v *big.Rat, currency string) {
	w.leaf(name, decimal(v, 2), attr("currencyID", currency))
}

// bytes is the document, or the first error.
func (w *writer) bytes() ([]byte, error) {
	if w.err == nil {
		w.err = w.enc.Close()
	}
	if w.err != nil {
		return nil, w.err
	}
	w.buf.WriteByte('\n')
	return w.buf.Bytes(), nil
}

// decimal is v at places decimals, the half away from zero (big.Rat's
// FloatString rule, the module's), never in exponent form; a nil value is
// zero.
func decimal(v *big.Rat, places int) string {
	if v == nil {
		v = new(big.Rat)
	}
	return v.FloatString(places)
}
