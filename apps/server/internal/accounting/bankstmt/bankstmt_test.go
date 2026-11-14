package bankstmt_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/accounting/bankstmt"
)

func read(t *testing.T, name string) []bankstmt.Statement {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if !bankstmt.IsXML(data) {
		t.Fatalf("%s is not taken for XML", name)
	}
	out, err := bankstmt.ReadCamt053(bytes.NewReader(data), bankstmt.DefaultLimits)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return out
}

// DNB's shape (.02): three statements routed by account — an IBAN, a BBAN
// under Othr, and one not Norwegian-checked — booked entries only, a KID
// batch with its transactions, a reversal, an internal transfer.
func TestCamt053_DNB(t *testing.T) {
	st := read(t, "dnb-camt053-001-02.xml")
	if len(st) != 3 || st[0].Account != "15030012345" || st[1].Account != "12060543214" || st[2].Account != "30001234567" {
		t.Fatalf("accounts = %+v", st)
	}
	s := st[0]
	if s.Ref != "15030012345-2026-09-07" || s.From.Format("2006-01-02") != "2026-09-01" || s.To.Format("2006-01-02") != "2026-09-07" ||
		s.Opening.FloatString(2) != "100000.00" || s.Closing.FloatString(2) != "88655.00" || s.NotBooked != 1 || len(s.Lines) != 5 {
		t.Fatalf("statement = %+v", s)
	}
	batch := s.Lines[0]
	if batch.Amount.FloatString(2) != "3750.00" || len(batch.Batch) != 3 || batch.Batch[1].KID != "0000102010" ||
		batch.Batch[0].Amount.FloatString(2) != "1250.00" || batch.BankID != "DNB-20260902-0001" || batch.Text != "OCR GIRO INNBETALINGER" {
		t.Fatalf("batch = %+v", batch)
	}
	pay := s.Lines[1]
	if pay.Amount.FloatString(2) != "-6250.00" || pay.KID != "4711000058" || pay.CounterpartyName != "Kontorrekvisita AS" ||
		pay.CounterpartyAccount != "86011117947" || pay.Reference != "E2E-LEV-4711" {
		t.Fatalf("payment = %+v", pay)
	}
	if fee := s.Lines[2]; fee.Amount.FloatString(2) != "-95.00" || fee.Text != "Gebyr nettbank september" || fee.Reversal {
		t.Fatalf("fee = %+v", fee)
	}
	if rev := s.Lines[3]; !rev.Reversal || rev.Amount.FloatString(2) != "6250.00" {
		t.Fatalf("reversal = %+v", rev)
	}
	if tr := s.Lines[4]; tr.Kind != bankstmt.KindInternal || tr.CounterpartyAccount != "12060543214" || tr.Text != "Overføring til sparekonto" {
		t.Fatalf("transfer = %+v", tr)
	}
	if in := st[1].Lines[0]; in.CounterpartyAccount != "15030012345" || in.Amount.FloatString(2) != "15000.00" {
		t.Fatalf("the savings side = %+v", in)
	}
	if st[2].Opening != nil || st[2].From.Format("2006-01-02") != "2026-09-03" {
		t.Fatalf("a statement with no balances and no period = %+v", st[2])
	}
}

// SpareBank 1's shape (.08): the status a code, date-times, Pty names, the
// previous closing balance as the opening, a card purchase, two identical
// lines that both stay.
func TestCamt053_SpareBank1(t *testing.T) {
	st := read(t, "sparebank1-camt053-001-08.xml")
	if len(st) != 1 {
		t.Fatalf("statements = %d", len(st))
	}
	s := st[0]
	if s.Account != "15030012345" || s.Opening.FloatString(2) != "88655.00" || s.Closing.FloatString(2) != "73544.00" || len(s.Lines) != 6 {
		t.Fatalf("statement = %+v", s)
	}
	if v := s.Lines[0]; v.CounterpartyName != "Vipps MobilePay AS" || v.BookedOn.Format("2006-01-02") != "2026-09-08" || v.Text != "Utbetaling Vipps 2026-09-07" {
		t.Fatalf("vipps = %+v", v)
	}
	if c := s.Lines[2]; c.Kind != bankstmt.KindCard {
		t.Fatalf("card = %+v", c)
	}
	a, b := s.Lines[4], s.Lines[5]
	if bankstmt.Fingerprint(s.Account, a) != bankstmt.Fingerprint(s.Account, b) || a.BankID != "" {
		t.Fatalf("the identical fees differ: %+v %+v", a, b)
	}
}

// What is refused: a DOCTYPE and its entities, another camt message, a
// version before .02, another currency, an amount with three decimals, an
// entry without CRDT or DBIT, a file too large.
func TestCamt053_Refusals(t *testing.T) {
	ok, err := os.ReadFile("testdata/dnb-camt053-001-02.xml")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(ok)
	cases := map[string]string{
		"doctype":        `<?xml version="1.0"?><!DOCTYPE Document [<!ENTITY x "xx">]><Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02"><BkToCstmrStmt>&x;</BkToCstmrStmt></Document>`,
		"billion laughs": `<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;">]><Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">&lol2;</Document>`,
		"external":       `<?xml version="1.0"?><!DOCTYPE Document SYSTEM "file:///etc/passwd"><Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02"/>`,
		"camt.054":       strings.ReplaceAll(doc, "camt.053.001.02", "camt.054.001.02"),
		"version 01":     strings.ReplaceAll(doc, "camt.053.001.02", "camt.053.001.01"),
		"currency":       strings.Replace(doc, `<Ccy>NOK</Ccy>`, `<Ccy>EUR</Ccy>`, 1),
		"three decimals": strings.Replace(doc, `>95.00<`, `>95.001<`, 1),
		"no indicator":   strings.Replace(doc, `<CdtDbtInd>DBIT</CdtDbtInd>`, `<CdtDbtInd>X</CdtDbtInd>`, 1),
		"not a document": `<Foo/>`,
		"no statement":   `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02"><BkToCstmrStmt/></Document>`,
	}
	for name, body := range cases {
		_, err := bankstmt.ReadCamt053(strings.NewReader(body), bankstmt.DefaultLimits)
		var inv *bankstmt.InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%s: %v, want an InvalidError", name, err)
		}
	}
	if _, err := bankstmt.ReadCamt053(strings.NewReader(doc), bankstmt.Limits{Bytes: 1000}); !errors.Is(err, bankstmt.ErrTooLarge) {
		t.Errorf("over the bytes: %v", err)
	}
	if _, err := bankstmt.ReadCamt053(strings.NewReader(doc), bankstmt.Limits{Lines: 3}); !errors.Is(err, bankstmt.ErrTooLarge) {
		t.Errorf("over the lines: %v", err)
	}
	latin := strings.ReplaceAll(strings.Replace(doc, `encoding="UTF-8"`, `encoding="ISO-8859-1"`, 1), "Overføring", "Overf\xf8ring")
	st, err := bankstmt.ReadCamt053(strings.NewReader(latin), bankstmt.DefaultLimits)
	if err != nil || st[0].Lines[4].Text != "Overføring til sparekonto" {
		t.Errorf("ISO-8859-1: %v", err)
	}
}

func col(i int) *int { return &i }

// CSV: comma decimals and a thousands space, ISO-8859-1, separate debit and
// credit columns, header rows; a byte order mark in UTF-8; a row that cannot
// be read is answered with its error.
func TestCSV(t *testing.T) {
	m := bankstmt.Mapping{Delimiter: ';', Encoding: "iso-8859-1", HeaderRows: 2, DateFormat: "DD.MM.YYYY", DecimalSeparator: ",",
		ThousandsSeparator: " ", Columns: bankstmt.Columns{Date: 0, Text: col(1), Debit: col(2), Credit: col(3), KID: col(4)}}
	data := []byte("Kontoutskrift for 1503.00.12345\nDato;Forklaring;Ut;Inn;KID\n" +
		"02.09.2026;Innbetaling fra Bj\xf8rn;;1 250,50;0000101012\n" +
		"03.09.2026;Gebyr;95,00;;\n" +
		"03.09.2026;Gebyr;95,00;;\n" +
		"\n" +
		"31.02.2026;Feil;1,00;;\n")
	rows, err := bankstmt.ReadCSV(data, m, bankstmt.DefaultLimits)
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows = %v %v", rows, err)
	}
	if l := rows[0].Line; rows[0].Err != nil || l.Amount.FloatString(2) != "1250.50" || l.Text != "Innbetaling fra Bjørn" || l.KID != "0000101012" || rows[0].N != 3 {
		t.Fatalf("first = %+v", rows[0])
	}
	if l := rows[1].Line; l.Amount.FloatString(2) != "-95.00" || bankstmt.Fingerprint("x", l) != bankstmt.Fingerprint("x", rows[2].Line) {
		t.Fatalf("fees = %+v %+v", rows[1], rows[2])
	}
	if rows[3].Err == nil {
		t.Fatalf("a bad date reads")
	}
	utf := bankstmt.Mapping{Delimiter: ',', Encoding: "utf-8", DateFormat: "YYYY-MM-DD", DecimalSeparator: ".", InvertSign: true,
		Columns: bankstmt.Columns{Date: 0, Amount: col(1), Text: col(2)}}
	rows, err = bankstmt.ReadCSV([]byte("\xef\xbb\xbf2026-09-02,\"1,000.00\",Husleie\n"), utf, bankstmt.DefaultLimits)
	if err != nil || len(rows) != 1 || rows[0].Err == nil {
		t.Fatalf("a thousands comma with a point for decimals and no thousands separator reads: %+v %v", rows, err)
	}
	utf.ThousandsSeparator = "."
	if _, err := bankstmt.ReadCSV(nil, utf, bankstmt.DefaultLimits); err == nil {
		t.Fatalf("the same separator for decimals and thousands is a mapping")
	}
	utf.ThousandsSeparator = ""
	rows, err = bankstmt.ReadCSV([]byte("\xef\xbb\xbf2026-09-02,1000.00,Husleie\n"), utf, bankstmt.DefaultLimits)
	if err != nil || rows[0].Line.Amount.FloatString(2) != "-1000.00" || !rows[0].Line.BookedOn.Equal(rows[0].Line.BookedOn) {
		t.Fatalf("utf-8 = %+v %v", rows, err)
	}
	if _, err := bankstmt.ReadCSV([]byte("2026-09-02,1\xff,x\n"), utf, bankstmt.DefaultLimits); err == nil {
		t.Fatalf("invalid UTF-8 reads")
	}
	bad := utf
	bad.Columns.Debit = col(3)
	if err := bad.Check(); err == nil {
		t.Fatalf("an amount and a debit column at once is a mapping")
	}
}

func TestNormaliseAccount(t *testing.T) {
	for in, want := range map[string]string{"1503.00.12345": "15030012345", "NO46 1503 0012 345": "15030012345", "15030012346": "",
		"NO4715030012345": "", "abc": ""} {
		got, ok := bankstmt.NormaliseAccount(in)
		if (want == "") == ok || got != want {
			t.Errorf("%q = %q %v, want %q", in, got, ok, want)
		}
	}
}
