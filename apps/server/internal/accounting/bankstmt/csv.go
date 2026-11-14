package bankstmt

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"
)

// CSV statements: a bank's export read by the mapping a person saves for the
// account — the separator, the encoding, the header rows, the date format,
// the decimal and thousands separators, and which column holds what, with
// one signed amount or separate debit and credit columns.

// Columns are the mapping's columns, counted from 0; nil is none.
type Columns struct {
	Date                                  int
	ValueDate, Amount, Debit, Credit      *int
	Text, Reference, KID                  *int
	CounterpartyName, CounterpartyAccount *int
	BankID                                *int
}

// Mapping is how a CSV statement is read.
type Mapping struct {
	Delimiter          rune
	Encoding           string // utf-8 or iso-8859-1
	HeaderRows         int
	DateFormat         string // one of DateFormats' keys
	DecimalSeparator   string // , or .
	ThousandsSeparator string // "", " ", ".", "," or "'"
	// InvertSign reads an amount column that shows money out as positive.
	InvertSign bool
	Columns    Columns
}

// DateFormats are the date formats a mapping may name, as Go layouts.
var DateFormats = map[string]string{
	"DD.MM.YYYY": "02.01.2006", "DD.MM.YY": "02.01.06", "YYYY-MM-DD": "2006-01-02",
	"DD/MM/YYYY": "02/01/2006", "MM/DD/YYYY": "01/02/2006", "YYYYMMDD": "20060102",
}

// Check is the mapping's own rules: a known encoding, date format and
// separators; an amount column or both debit and credit, not both kinds;
// no column twice.
func (m Mapping) Check() error {
	switch {
	case m.Delimiter == 0 || m.Delimiter == '"' || m.Delimiter == '\r' || m.Delimiter == '\n':
		return invalid("the separator is one character, not a quote or a line break")
	case m.Encoding != "utf-8" && m.Encoding != "iso-8859-1":
		return invalid("the encoding is utf-8 or iso-8859-1")
	case DateFormats[m.DateFormat] == "":
		return invalid("the date format is not one of those offered")
	case m.DecimalSeparator != "," && m.DecimalSeparator != ".":
		return invalid("the decimal separator is a comma or a point")
	case m.ThousandsSeparator == m.DecimalSeparator:
		return invalid("the thousands separator is not the decimal separator")
	case m.HeaderRows < 0 || m.HeaderRows > 20:
		return invalid("at most 20 header rows")
	}
	c := m.Columns
	if (c.Amount == nil) == (c.Debit == nil && c.Credit == nil) {
		return invalid("an amount column, or debit and credit columns — one of them")
	}
	if c.Amount == nil && (c.Debit == nil || c.Credit == nil) {
		return invalid("both a debit and a credit column")
	}
	seen := map[int]bool{c.Date: true}
	for _, p := range []*int{c.ValueDate, c.Amount, c.Debit, c.Credit, c.Text, c.Reference, c.KID, c.CounterpartyName,
		c.CounterpartyAccount, c.BankID} {
		if p == nil {
			continue
		}
		if *p < 0 || *p > 200 || seen[*p] {
			return invalid("a column is named twice, or out of range")
		}
		seen[*p] = true
	}
	if c.Date < 0 || c.Date > 200 {
		return invalid("the date column is out of range")
	}
	return nil
}

// Row is one row read: its line, or why it cannot be read.
type Row struct {
	// N is the file's row, counted from 1.
	N    int
	Line Line
	Err  error
}

// ReadCSV reads every row after the header rows. A row that is blank is
// left out; one that cannot be read is answered with its error, so a preview
// shows it and an import refuses the file. The file is decoded as the
// mapping says: a UTF-8 byte order mark is dropped, ISO-8859-1 read byte for
// rune; UTF-8 that is not valid is refused.
func ReadCSV(data []byte, m Mapping, lim Limits) ([]Row, error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	if lim.Bytes > 0 && int64(len(data)) > lim.Bytes {
		return nil, ErrTooLarge
	}
	text, err := decode(data, m.Encoding)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = m.Delimiter
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var out []Row
	n := 0
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		n++
		if err != nil {
			return nil, invalid(fmt.Sprintf("row %d is not readable CSV (%s)", n, err.Error()))
		}
		if n <= m.HeaderRows || blank(rec) {
			continue
		}
		if lim.Lines > 0 && len(out) >= lim.Lines {
			return nil, ErrTooLarge
		}
		l, err := m.row(rec)
		out = append(out, Row{N: n, Line: l, Err: err})
	}
	return out, nil
}

func blank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// decode is data as text in the encoding.
func decode(data []byte, encoding string) (string, error) {
	if encoding == "iso-8859-1" {
		var b strings.Builder
		b.Grow(len(data))
		for _, c := range data {
			b.WriteRune(rune(c))
		}
		return b.String(), nil
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return "", invalid("the file is not UTF-8: choose ISO-8859-1 as its encoding")
	}
	return string(data), nil
}

// row is one record as a line.
func (m Mapping) row(rec []string) (Line, error) {
	field := func(p *int) string {
		if p == nil || *p >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[*p])
	}
	c := m.Columns
	date := c.Date
	booked, err := time.Parse(DateFormats[m.DateFormat], field(&date))
	if err != nil {
		return Line{}, fmt.Errorf("the date %q is not %s", field(&date), m.DateFormat)
	}
	l := Line{BookedOn: booked, Kind: KindTransfer}
	if v := field(c.ValueDate); v != "" {
		if d, err := time.Parse(DateFormats[m.DateFormat], v); err == nil {
			l.ValueOn = &d
		}
	}
	if c.Amount != nil {
		a, err := m.amount(field(c.Amount))
		if err != nil {
			return Line{}, err
		}
		if m.InvertSign {
			a.Neg(a)
		}
		l.Amount = a
	} else {
		debit, credit := field(c.Debit), field(c.Credit)
		total := new(big.Rat)
		if debit != "" {
			d, err := m.amount(debit)
			if err != nil {
				return Line{}, err
			}
			total.Sub(total, new(big.Rat).Abs(d))
		}
		if credit != "" {
			cr, err := m.amount(credit)
			if err != nil {
				return Line{}, err
			}
			total.Add(total, new(big.Rat).Abs(cr))
		}
		l.Amount = total
	}
	if l.Amount.Sign() == 0 {
		return Line{}, errors.New("the amount is zero")
	}
	l.Text = clip(field(c.Text), 1000)
	l.Reference = clip(field(c.Reference), 140)
	l.KID = kid([]string{field(c.KID)})
	l.CounterpartyName = clip(field(c.CounterpartyName), 140)
	l.CounterpartyAccount = clip(field(c.CounterpartyAccount), 34)
	if bban, ok := NormaliseAccount(l.CounterpartyAccount); ok {
		l.CounterpartyAccount = bban
	}
	l.BankID = clip(field(c.BankID), 140)
	return l, nil
}

// amount reads a figure with the mapping's separators: "-1 234,50", "1.234,50",
// "(12.00)" for a negative one.
func (m Mapping) amount(s string) (*big.Rat, error) {
	text := strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		neg, text = true, strings.TrimSpace(text[1:len(text)-1])
	}
	text = strings.NewReplacer(" ", "", "kr", "", "NOK", "").Replace(text)
	text = strings.TrimSpace(text)
	if m.ThousandsSeparator != "" {
		text = strings.ReplaceAll(text, m.ThousandsSeparator, "")
	}
	text = strings.ReplaceAll(text, " ", "")
	if m.DecimalSeparator == "," {
		text = strings.ReplaceAll(text, ",", ".")
	}
	text = strings.Replace(text, "−", "-", 1)
	r, ok := new(big.Rat).SetString(text)
	if !ok || text == "" || strings.ContainsAny(text, "eE/") || !new(big.Rat).Mul(r, big.NewRat(100, 1)).IsInt() {
		return nil, fmt.Errorf("the amount %q is not a figure with at most two decimals", s)
	}
	if neg {
		r.Neg(r)
	}
	return r, nil
}
