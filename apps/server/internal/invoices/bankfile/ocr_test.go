package bankfile_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
)

func ptr(t time.Time) *time.Time { return &t }

// R4 §3.1's example, read to the field; then one refusal per rule, each a
// built file corrupted in one place; then what the format allows.
func TestBankFileOCR_Parse(t *testing.T) {
	t.Parallel()

	t.Run("R4's example to the field", func(t *testing.T) {
		t.Parallel()
		f := mustParse(t, fixture(t, "r4-example.ocr"))
		want := &bankfile.File{
			Format:   bankfile.FormatOCR,
			Identity: "00008080:0000123:00012345",
			Accounts: []string{accountA},
			Ignored: map[bankfile.IgnoredKind]int{bankfile.IgnoredDebit: 0, bankfile.IgnoredNotBooked: 0,
				bankfile.IgnoredCardInformation: 0, bankfile.IgnoredZeroAmount: 0},
			FirstBookedOn: day(6), LastBookedOn: day(6),
			Transactions: []bankfile.Transaction{{
				LineRef: "1/1", Account: accountA, BookedOn: day(6), OrderedOn: ptr(day(5)),
				AmountMinor: 125000, Currency: "NOK", KID: "0010017",
				DebtorAccount: "98765432109", ArchiveRef: "123456789", BankCode: "10", Ordinal: 1,
			}, {
				LineRef: "1/2", Account: accountA, BookedOn: day(6), OrderedOn: ptr(day(6)),
				AmountMinor: 49950, Currency: "NOK", KID: "0010025",
				DebtorAccount: "12340567894", ArchiveRef: "234567890", BankCode: "16", Ordinal: 1,
			}},
		}
		for i := range f.Transactions {
			if len(f.Transactions[i].Fingerprint) != 64 {
				t.Errorf("transaction %d: fingerprint %q", i, f.Transactions[i].Fingerprint)
			}
			f.Transactions[i].Fingerprint = ""
		}
		if !reflect.DeepEqual(f, want) {
			t.Errorf("ParseOCR(r4-example.ocr) =\n%+v\nwant\n%+v", f, want)
		}
		crlf := mustParse(t, bytes.ReplaceAll(fixture(t, "r4-example.ocr"), []byte("\n"), []byte("\r\n")))
		for i := range crlf.Transactions {
			crlf.Transactions[i].Fingerprint = ""
		}
		if !reflect.DeepEqual(crlf, want) {
			t.Errorf("with CR/LF: %+v", crlf)
		}
	})

	// base is two assignments: a giro and a type 20 with its item 3 on A,
	// a BTG on B. Its records, 1-based: 1 start; 2 assignment A; 3–4 the
	// giro; 5–7 the type 20; 8 A's end; 9 assignment B; 10–11 the BTG;
	// 12 B's end; 13 the end.
	p1 := bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(5), Ordered: day(4), AmountMinor: 125000,
		KID: "0010017", ArchiveRef: "1", DebtorAccount: "98765432109"}
	p2 := bankfiletest.OCRPayment{Type: 20, Account: accountA, Settled: day(6), AmountMinor: 3000, Text: "retur"}
	p3 := bankfiletest.OCRPayment{Type: 13, Account: accountB, Settled: day(6), Ordered: day(6), AmountMinor: 49950, KID: "0010025"}
	base := records(bankfiletest.OCR("0000042", p1, p2, p3))
	if len(base) != 13 {
		t.Fatalf("the base file has %d records, want 13", len(base))
	}
	if _, err := bankfile.ParseOCR(join(base), today); err != nil {
		t.Fatalf("the base file is refused: %v", err)
	}
	edit := func(i int, pos int, s string) func([]string) []string {
		return func(r []string) []string { r[i] = put(r[i], pos, s); return r }
	}
	without := func(from, to int) func([]string) []string {
		return func(r []string) []string { return slices.Delete(r, from, to) }
	}
	insert := func(i int, rec string) func([]string) []string {
		return func(r []string) []string { return slices.Insert(r, i, rec) }
	}
	built := func(payments ...bankfiletest.OCRPayment) []byte { return bankfiletest.OCR("0000043", payments...) }
	many := func(n int) []bankfiletest.OCRPayment {
		ps := make([]bankfiletest.OCRPayment, n)
		for i := range ps {
			ps[i] = bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), AmountMinor: 100, KID: "0010017"}
		}
		return ps
	}

	for _, c := range []struct {
		name   string
		mutate func([]string) []string
		file   []byte // instead of mutate, a file built for the case
		where  string
		want   []string
	}{
		{name: "a 79-character record", mutate: func(r []string) []string { r[3] = r[3][:79]; return r },
			where: "record 4", want: []string{"79 characters"}},
		{name: "an 81-character record", mutate: func(r []string) []string { r[3] += "0"; return r },
			where: "record 4", want: []string{"81 characters"}},
		{name: "a blank record inside", mutate: insert(5, ""), where: "record 6", want: []string{"0 characters"}},
		{name: "a record not starting NY", mutate: edit(3, 1, "XY"), where: "record 4", want: []string{"NY"}},
		{name: "the start record missing", mutate: without(0, 1), where: "record 1", want: []string{"NY000010"}},
		{name: "a second start record", mutate: insert(1, base[0]), where: "record 2", want: []string{"NY000010"}},
		{name: "the end record missing", mutate: without(12, 13), where: "file", want: []string{"NY000089"}},
		{name: "a record after the end", mutate: func(r []string) []string { return append(r, r[9]) },
			where: "record 14", want: []string{"after the transmission's end"}},
		{name: "a transmission with no assignments", mutate: func(r []string) []string {
			return []string{r[0], put(put(put(r[12], 9, "00000000"), 17, "00000002"), 25, "00000000000000000")}
		}, where: "record 2", want: []string{"no assignments"}},
		{name: "the assignment end missing", mutate: without(7, 8), where: "record 8", want: []string{"NY090020"}},
		{name: "an assignment without transactions", mutate: without(9, 11), where: "record 10", want: []string{"no transactions"}},
		{name: "item 2 without item 1", mutate: without(2, 3), where: "record 3", want: []string{"item 2"}},
		{name: "item 1 without item 2", mutate: without(3, 4), where: "record 4", want: []string{"item 2"}},
		{name: "an unknown record type", mutate: edit(3, 7, "40"), where: "record 4", want: []string{"record type 40"}},
		{name: "an unknown transaction type", mutate: func(r []string) []string {
			r[2], r[3] = put(r[2], 5, "22"), put(r[3], 5, "22")
			return r
		}, where: "record 3", want: []string{"transaction type 22"}},
		{name: "item 2's transaction number", mutate: edit(3, 9, "0000009"), where: "record 4", want: []string{"transaction number"}},
		{name: "item 2's transaction type", mutate: edit(3, 5, "13"), where: "record 4", want: []string{"transaction type"}},
		{name: "item 3 on a type 10", mutate: insert(4, "NY0910320000001"+fmt.Sprintf("%-40s", "tekst")+strings.Repeat("0", 25)),
			where: "record 5", want: []string{"item 3"}},
		{name: "item 3 missing on a type 20", mutate: without(6, 7), where: "record 7", want: []string{"item 3"}},
		{name: "item 3's transaction number", mutate: edit(6, 9, "0000009"), where: "record 7", want: []string{"transaction number"}},
		{name: "the assignment's transaction count", mutate: edit(7, 9, "00000003"), where: "record 8", want: []string{"transactions"}},
		{name: "the assignment's record count", mutate: edit(7, 17, "00000008"), where: "record 8", want: []string{"records"}},
		{name: "the assignment's sum", mutate: edit(7, 25, "00000000000128001"), where: "record 8", want: []string{"sum"}},
		{name: "the assignment's first date", mutate: edit(7, 48, "041026"), where: "record 8", want: []string{"first settlement date"}},
		{name: "the assignment's last date", mutate: edit(7, 54, "051026"), where: "record 8", want: []string{"last settlement date"}},
		{name: "the assignment's count not digits", mutate: edit(7, 9, "0000000x"), where: "record 8", want: []string{"digits"}},
		{name: "the transmission's transaction count", mutate: edit(12, 9, "00000004"), where: "record 13", want: []string{"transactions"}},
		{name: "the transmission's record count", mutate: edit(12, 17, "00000012"), where: "record 13", want: []string{"records"}},
		{name: "the transmission's sum", mutate: edit(12, 25, "00000000000177949"), where: "record 13", want: []string{"sum"}},
		{name: "service 08 on the assignment", mutate: edit(1, 3, "08"), where: "record 2", want: []string{"service code 08"}},
		{name: "service 08 on an item", mutate: edit(2, 3, "08"), where: "record 3", want: []string{"service code 08"}},
		{name: "a settlement date 320226", mutate: edit(2, 16, "320226"), where: "record 3", want: []string{"320226"}},
		{name: "an order date 310926", mutate: edit(3, 42, "310926"), where: "record 4", want: []string{"310926"}},
		{name: "a KID with a letter", mutate: edit(2, 50, fmt.Sprintf("%25s", "001O017")), where: "record 3", want: []string{"KID"}},
		{name: "a KID with a dash inside", mutate: edit(2, 50, fmt.Sprintf("%25s", "0010-17")), where: "record 3", want: []string{"KID"}},
		{name: "a KID with a space inside", mutate: edit(2, 50, fmt.Sprintf("%25s", "001 017")), where: "record 3", want: []string{"KID"}},
		{name: "a sign other than '-' or '0'", mutate: edit(2, 32, "+"), where: "record 3", want: []string{"sign"}},
		{name: "an amount not digits", mutate: edit(2, 33, "0000000000012500x"), where: "record 3", want: []string{"amount"}},
		{name: "an account of 10 digits", file: built(bankfiletest.OCRPayment{Type: 10, Account: "1234567890",
			Settled: day(6), AmountMinor: 100, KID: "0010017"}), where: "record 2", want: []string{"account"}},
		{name: "a booking after today", file: built(bankfiletest.OCRPayment{Type: 10, Account: accountA,
			Settled: day(7), AmountMinor: 100, KID: "0010017"}), where: "record 3", want: []string{"after"}},
		{name: "an amount past numeric(14,2)", file: built(bankfiletest.OCRPayment{Type: 10, Account: accountA,
			Settled: day(6), AmountMinor: 100_000_000_000_000, KID: "0010017"}), where: "record 3", want: []string{"amount"}},
		{name: "5 001 transactions", file: built(many(bankfile.MaxTransactions + 1)...),
			where: fmt.Sprintf("record %d", 2+2*bankfile.MaxTransactions+1), want: []string{"5000"}},
	} {
		b := c.file
		if b == nil {
			b = join(c.mutate(slices.Clone(base)))
		}
		f, err := bankfile.ParseOCR(b, today)
		if f != nil {
			t.Errorf("%s: a file was returned beside the refusal", c.name)
		}
		refusal(t, c.name, err, c.where, c.want...)
	}

	t.Run("what the format allows", func(t *testing.T) {
		t.Parallel()

		// Blank lines before the first record and after the last are
		// passed over, and a refusal still names the record's own line.
		padded := append([]byte("\r\n  \n\t\r\n"), fixture(t, "r4-example.ocr")...)
		padded = append(padded, "\r\n\n  \n"...)
		if f := mustParse(t, padded); len(f.Transactions) != 2 {
			t.Errorf("a padded file: %d transactions", len(f.Transactions))
		}
		_, err := bankfile.ParseOCR(append(padded, "NY"...), today)
		refusal(t, "a record after blank lines after the end", err, "record 15", "after the transmission's end")
		_, err = bankfile.ParseOCR(bytes.Replace(padded, []byte("NY090088"), []byte("NY090089"), 1), today)
		refusal(t, "a padded file's broken record", err, "record 10", "NY0000")

		f := mustParse(t, built(many(bankfile.MaxTransactions)...))
		if len(f.Transactions) != bankfile.MaxTransactions || f.Transactions[4999].Ordinal != bankfile.MaxTransactions {
			t.Errorf("5 000 transactions: %d, the last ordinal %d", len(f.Transactions), f.Transactions[4999].Ordinal)
		}

		// The century window reads 01.01.00 as 2000-01-01, the earliest a
		// DDMMYY can say, so OCR cannot express a date before 2000; the
		// order date 000000 is none.
		f = mustParse(t, built(bankfiletest.OCRPayment{Type: 10, Account: accountA,
			Settled: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), AmountMinor: 100, KID: "0010017"}))
		if tx := f.Transactions[0]; !tx.BookedOn.Equal(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || tx.OrderedOn != nil {
			t.Errorf("01.01.00: booked %v, ordered %v", tx.BookedOn, tx.OrderedOn)
		}
		f = mustParse(t, built(bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6),
			AmountMinor: 99_999_999_999_999, KID: "0010017"}))
		if f.Transactions[0].AmountMinor != 99_999_999_999_999 {
			t.Errorf("the largest amount: %d", f.Transactions[0].AmountMinor)
		}

		f = mustParse(t, fixture(t, "two-assignments.ocr"))
		var refs, codes []string
		for _, tx := range f.Transactions {
			refs, codes = append(refs, tx.Account+" "+tx.LineRef), append(codes, tx.BankCode)
		}
		if !slices.Equal(f.Accounts, []string{accountA, accountB}) ||
			!slices.Equal(refs, []string{accountA + " 1/1", accountA + " 1/2", accountB + " 2/1"}) ||
			!slices.Equal(codes, []string{"10", "13", "16"}) ||
			!f.FirstBookedOn.Equal(day(5)) || !f.LastBookedOn.Equal(day(6)) || f.Identity != "00008080:0000124:00012345" {
			t.Errorf("two assignments: accounts %v, lines %v, codes %v, %v–%v, %s",
				f.Accounts, refs, codes, f.FirstBookedOn, f.LastBookedOn, f.Identity)
		}
		if tx := f.Transactions[1]; tx.DebtorAccount != "" || tx.ArchiveRef != "300000002" {
			t.Errorf("a line without a debtor account: %q, %q", tx.DebtorAccount, tx.ArchiveRef)
		}

		// Card information is checked and summed, a type 18 added though
		// its sign is '-', and counted, not kept; its item 3 is read as
		// ISO-8859-1, or as UTF-8 when a file was re-encoded.
		latin := fixture(t, "card-information.ocr")
		var utf []byte
		for _, c := range latin {
			utf = utf8.AppendRune(utf, rune(c))
		}
		for name, b := range map[string][]byte{"ISO-8859-1": latin, "UTF-8": utf} {
			f = mustParse(t, b)
			if len(f.Transactions) != 1 || f.Transactions[0].AmountMinor != 10000 ||
				f.Ignored[bankfile.IgnoredCardInformation] != 2 {
				t.Errorf("card information (%s): %d transactions, ignored %v", name, len(f.Transactions), f.Ignored)
			}
		}

		// A negative line is a transaction marked so, of its amount; the
		// sums are net.
		f = mustParse(t, fixture(t, "negative-line.ocr"))
		if len(f.Transactions) != 2 || f.Transactions[0].Negative || !f.Transactions[1].Negative ||
			f.Transactions[1].AmountMinor != 25000 {
			t.Errorf("the negative line: %+v", f.Transactions)
		}

		f = mustParse(t, fixture(t, "mod11-dash.ocr"))
		if f.Transactions[0].KID != "001009-" {
			t.Errorf("the MOD11 dash: KID %q", f.Transactions[0].KID)
		}

		// A giro of 0.00 is counted, not kept: a payment is never zero.
		f = mustParse(t, built(
			bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), AmountMinor: 0, KID: "0010017"},
			bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), AmountMinor: 100, KID: "0010025"}))
		if len(f.Transactions) != 1 || f.Transactions[0].LineRef != "1/2" || f.Ignored[bankfile.IgnoredZeroAmount] != 1 {
			t.Errorf("a zero amount: %d transactions, ignored %v", len(f.Transactions), f.Ignored)
		}
	})
}

// Every parsed file's Accounts is a slice, never nil: the import writes it
// to bank_files.accounts, which is NOT NULL.
func TestBankFile_AccountsNeverNil(t *testing.T) {
	t.Parallel()
	var f bankfile.File
	bankfile.Finish(&f)
	if f.Accounts == nil || len(f.Accounts) != 0 {
		t.Errorf("a file of no accounts: Accounts = %#v, want []string{}", f.Accounts)
	}
}

// A hostile file costs time in proportion to its size, never more: the
// shapes that once read the rest of the file again for every record — a
// cap's worth of transactions padded with megabytes of white space — and
// a megabyte of blank lines before the records, and of XML comments before
// a root. Linear, each takes milliseconds (a few hundred under -race);
// quadratic, the first took twenty seconds.
func TestBankFile_HostileInputIsLinear(t *testing.T) {
	t.Parallel()
	recs := records(fixture(t, "r4-example.ocr"))
	items := []string{recs[0], recs[1]}
	for range bankfile.MaxTransactions + 1 {
		items = append(items, recs[2], recs[3])
	}
	padded := append(join(items), bytes.Repeat([]byte(" "), 4<<20)...)
	blank := append(bytes.Repeat([]byte("\n"), 1<<20), fixture(t, "r4-example.ocr")...)
	comments := append([]byte(`<?xml version="1.0"?>`), bytes.Repeat([]byte("<!---->"), 1<<17)...)
	for name, b := range map[string][]byte{"padded": padded, "blank lines": blank, "comments": comments} {
		start := time.Now()
		_, _ = bankfile.Parse(b, today)
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: %v for %d bytes", name, d, len(b))
		}
	}
}

// No input makes the parser panic, a refusal is a *bankfile.Error in UTF-8,
// and whatever it accepts is what the import may store: amounts within
// numeric(14,2) and above zero, booking dates from 2000-01-01 to today,
// accounts that normalise and are the file's own, a KID of at most 25
// characters and a line reference of at most 60, valid UTF-8 without NUL,
// at most MaxTransactions, fingerprinted.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"r4-example.ocr", "two-assignments.ocr", "card-information.ocr",
		"negative-line.ocr", "mod11-dash.ocr", "identical-lines.ocr", "overlap-a.ocr"} {
		b, err := readFixture(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	camt, err := os.ReadDir(filepath.Join("testdata", "camt054"))
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range camt {
		b, err := os.ReadFile(filepath.Join("testdata", "camt054", e.Name()))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("NY000010\n"))
	f.Add([]byte(`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.054.001.02"/>`))
	f.Add([]byte(`<!DOCTYPE d><Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.054.001.08"/>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		file, err := bankfile.Parse(b, today)
		if err != nil {
			var e *bankfile.Error
			if !errors.As(err, &e) || file != nil || !utf8.ValidString(e.Error()) {
				t.Fatalf("Parse = %v, %q; want only a *bankfile.Error in UTF-8", file, err)
			}
			return
		}
		if len(file.Transactions) > bankfile.MaxTransactions || utf8.RuneCountInString(file.Identity) > 200 {
			t.Fatalf("%d transactions, the identity %q", len(file.Transactions), file.Identity)
		}
		earliest := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
		for _, tx := range file.Transactions {
			if _, ok := bankfile.NormaliseAccount(tx.Account); !ok || !slices.Contains(file.Accounts, tx.Account) ||
				tx.AmountMinor <= 0 || tx.AmountMinor > 99_999_999_999_999 ||
				tx.BookedOn.Before(earliest) || tx.BookedOn.After(today) ||
				len(tx.KID) > 25 || len(tx.Fingerprint) != 64 || tx.Ordinal < 1 {
				t.Fatalf("an unstorable transaction: %+v", tx)
			}
			// Each string fits its bank_transactions column (D3's schema).
			for _, c := range []struct {
				s    string
				size int
			}{{tx.LineRef, 60}, {tx.KID, 25}, {tx.RemittanceText, 1000}, {tx.DebtorName, 140},
				{tx.DebtorAccount, 34}, {tx.ArchiveRef, 35}, {tx.BankCode, 35}} {
				if !utf8.ValidString(c.s) || strings.ContainsRune(c.s, 0) || utf8.RuneCountInString(c.s) > c.size {
					t.Fatalf("an unstorable string %q in %+v", c.s, tx)
				}
			}
		}
	})
}
