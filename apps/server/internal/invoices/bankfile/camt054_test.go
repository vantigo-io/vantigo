package bankfile_test

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
)

const (
	v02 = "camt.054.001.02"
	v08 = "camt.054.001.08"
)

func camtFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "camt054", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustCamt(t *testing.T, b []byte) *bankfile.File {
	t.Helper()
	f, err := bankfile.ParseCamt054(b, today)
	if err != nil {
		t.Fatalf("ParseCamt054: %v", err)
	}
	return f
}

// swap is b with old replaced by new, n times (every time for n < 0); it
// fails the test when old is not in b, so a case never tests the file it
// meant to break.
func swap(t *testing.T, b []byte, old, new string, n int) []byte {
	t.Helper()
	if !bytes.Contains(b, []byte(old)) {
		t.Fatalf("%q is not in the file", old)
	}
	return bytes.Replace(b, []byte(old), []byte(new), n)
}

// noFingerprints clears what Fingerprints filled, after checking it did.
func noFingerprints(t *testing.T, f *bankfile.File) {
	t.Helper()
	for i := range f.Transactions {
		if len(f.Transactions[i].Fingerprint) != 64 || f.Transactions[i].Ordinal < 1 {
			t.Errorf("transaction %d: fingerprint %q, ordinal %d", i, f.Transactions[i].Fingerprint, f.Transactions[i].Ordinal)
		}
		f.Transactions[i].Fingerprint = ""
	}
}

func ignored(debit, notBooked, zero int) map[bankfile.IgnoredKind]int {
	return map[bankfile.IgnoredKind]int{bankfile.IgnoredDebit: debit, bankfile.IgnoredNotBooked: notBooked,
		bankfile.IgnoredCardInformation: 0, bankfile.IgnoredZeroAmount: zero}
}

// The committed fixtures to the field, both versions; then one refusal per
// rule, each a fixture or a built file broken in one place, naming its
// element; then the hardening's caps, each at its bound.
func TestBankFileCamt_Parse(t *testing.T) {
	t.Parallel()

	t.Run("R4's example in both versions to the field", func(t *testing.T) {
		t.Parallel()
		var prints []string
		for _, c := range []struct{ name, version string }{
			{"v02-r4-example.xml", v02}, {"v08-r4-example.xml", v08},
		} {
			f := mustCamt(t, camtFixture(t, c.name))
			prints = append(prints, f.Transactions[0].Fingerprint)
			noFingerprints(t, f)
			want := &bankfile.File{
				Format: bankfile.FormatCamt054, Version: c.version,
				Identity:      "NO20261006-000123|2026-10-06T17:35:00+02:00",
				Accounts:      []string{accountA},
				Ignored:       ignored(0, 0, 0),
				FirstBookedOn: day(6), LastBookedOn: day(6),
				Transactions: []bankfile.Transaction{{
					LineRef: "20261006173500NOK8903/1/1", Account: accountA, BookedOn: day(6), ValueOn: ptr(day(6)),
					AmountMinor: 125000, Currency: "NOK", KID: "0010017", DebtorName: "Kunde AS",
					DebtorAccount: "98765432109", ArchiveRef: "123456789", BankCode: "PMNT/RCDT/VCOM", Ordinal: 1,
				}},
			}
			if !reflect.DeepEqual(f, want) {
				t.Errorf("%s =\n%+v\nwant\n%+v", c.name, f, want)
			}
		}
		// One payment, one fingerprint, whichever version reported it.
		if prints[0] != prints[1] {
			t.Error("the same payment in .02 and .08 has two fingerprints")
		}
		f, err := bankfile.Parse(camtFixture(t, "v02-r4-example.xml"), today)
		if err != nil || f.Format != bankfile.FormatCamt054 || len(f.Transactions) != 1 {
			t.Errorf("Parse does not dispatch camt.054: %+v, %v", f, err)
		}
	})

	t.Run("Danske-shaped: the KID on SCOR under NTAV, no archive reference", func(t *testing.T) {
		t.Parallel()
		f := mustCamt(t, camtFixture(t, "v02-danske-shaped.xml"))
		noFingerprints(t, f)
		want := []bankfile.Transaction{{
			LineRef: "DBNO2026100600017/1/1", Account: accountA, BookedOn: day(5), ValueOn: ptr(day(5)),
			AmountMinor: 78000, Currency: "NOK", KID: "0010025", DebtorName: "Ola Nordmann",
			DebtorAccount: "15030123456", BankCode: "PMNT/NTAV/NTAV", Ordinal: 1,
		}, {
			LineRef: "DBNO2026100600017/2/1", Account: accountA, BookedOn: day(6), ValueOn: ptr(day(6)),
			AmountMinor: 150050, Currency: "NOK", KID: "0010033", DebtorName: "Kari Nordmann",
			BankCode: "PMNT/NTAV/NTAV", Ordinal: 1,
		}}
		if !reflect.DeepEqual(f.Transactions, want) {
			t.Errorf("transactions =\n%+v\nwant\n%+v", f.Transactions, want)
		}
		if f.Identity != "DBNO-20261006-4711|2026-10-06T06:12:44.123" || !f.FirstBookedOn.Equal(day(5)) || !f.LastBookedOn.Equal(day(6)) {
			t.Errorf("identity %q, booked %v–%v", f.Identity, f.FirstBookedOn, f.LastBookedOn)
		}
	})

	t.Run("a batch: three transactions, the text joined, the payout's kept", func(t *testing.T) {
		t.Parallel()
		f := mustCamt(t, camtFixture(t, "v02-batch.xml"))
		noFingerprints(t, f)
		base := bankfile.Transaction{Account: accountA, BookedOn: day(6), ValueOn: ptr(day(6)), Currency: "NOK",
			BankCode: "PMNT/RCDT/DMCT", Ordinal: 1}
		tx := func(n string, amount int64, kid, text, name, account, archive string) bankfile.Transaction {
			t := base
			t.LineRef, t.AmountMinor, t.KID, t.RemittanceText = "20261006180000NOK8903/1/"+n, amount, kid, text
			t.DebtorName, t.DebtorAccount, t.ArchiveRef = name, account, archive
			return t
		}
		want := []bankfile.Transaction{
			tx("1", 200000, "0010041", "", "Kunde AS", "98765432109", "223456781"),
			tx("2", 150000, "", "Faktura 1005 takk for  sist", "Snekker Hansen", "NO9386011117947", "223456782"),
			tx("3", 124750, "", "Utb. 06.10 Vippsnr 123456", "Vipps MobilePay AS", "", "223456783"),
		}
		if !reflect.DeepEqual(f.Transactions, want) {
			t.Errorf("transactions =\n%+v\nwant\n%+v", f.Transactions, want)
		}
	})

	t.Run("reversals: two debits kept, a plain debit counted", func(t *testing.T) {
		t.Parallel()
		f := mustCamt(t, camtFixture(t, "v02-reversals.xml"))
		noFingerprints(t, f)
		want := []bankfile.Transaction{{
			LineRef: "20261006183000NOK8903/1/1", Account: accountA, Debit: true, BookedOn: day(6), ValueOn: ptr(day(6)),
			AmountMinor: 125000, Currency: "NOK", KID: "0010017", DebtorName: "Kunde AS", DebtorAccount: "98765432109",
			ArchiveRef: "123456789", BankCode: "PMNT/RCDT/VCOM", Ordinal: 1,
		}, {
			LineRef: "20261006183000NOK8903/2/1", Account: accountA, Debit: true, BookedOn: day(6),
			AmountMinor: 49950, Currency: "NOK", RemittanceText: "Tilbakeført: mottaker ukjent",
			ArchiveRef: "234567890", BankCode: "PMNT/ICDT/RRTN", Ordinal: 1,
		}, {
			LineRef: "20261006183000NOK8903/4/1", Account: accountA, BookedOn: day(6),
			AmountMinor: 30000, Currency: "NOK", KID: "0010058", ArchiveRef: "345678901", BankCode: "PMNT/RCDT/VCOM", Ordinal: 1,
		}}
		if !reflect.DeepEqual(f.Transactions, want) || !reflect.DeepEqual(f.Ignored, ignored(1, 0, 0)) {
			t.Errorf("transactions =\n%+v\nignored %v; want\n%+v", f.Transactions, f.Ignored, want)
		}
	})

	t.Run("statuses: pending counted, an entry without TxDtls kept, 0.00 counted", func(t *testing.T) {
		t.Parallel()
		f := mustCamt(t, camtFixture(t, "v02-statuses.xml"))
		noFingerprints(t, f)
		want := []bankfile.Transaction{{
			LineRef: "20261006190000NOK8903/2/1", Account: accountA, BookedOn: day(5), ValueOn: ptr(day(6)),
			AmountMinor: 12575, Currency: "NOK", RemittanceText: "Renter september", BankCode: "INT", Ordinal: 1,
		}, {
			LineRef: "20261006190000NOK8903/3/2", Account: accountA, BookedOn: day(6),
			AmountMinor: 60000, Currency: "NOK", KID: "0010082", ArchiveRef: "456789012", BankCode: "PMNT/RCDT/VCOM", Ordinal: 1,
		}}
		if !reflect.DeepEqual(f.Transactions, want) || !reflect.DeepEqual(f.Ignored, ignored(0, 1, 1)) {
			t.Errorf("transactions =\n%+v\nignored %v; want\n%+v", f.Transactions, f.Ignored, want)
		}
		// The booking date of a DtTm is the date the bank wrote, in its
		// own offset, at UTC midnight as every other date here.
		if f.Transactions[0].BookedOn.Location() != time.UTC {
			t.Errorf("BookedOn in %v", f.Transactions[0].BookedOn.Location())
		}
	})

	t.Run(".08: an IBAN, a copy, a batch, a reversal, pending and 0.00", func(t *testing.T) {
		t.Parallel()
		f := mustCamt(t, camtFixture(t, "v08-iban.xml"))
		if f.Version != v08 || !reflect.DeepEqual(f.Accounts, []string{accountB}) || len(f.Transactions) != 1 ||
			f.Transactions[0].Account != accountB || f.Transactions[0].AmountMinor != 49950 || f.Transactions[0].DebtorName != "Kunde AS" {
			t.Errorf("v08-iban.xml = %+v", f)
		}

		f = mustCamt(t, camtFixture(t, "v08-copy-batch.xml"))
		noFingerprints(t, f)
		want := []bankfile.Transaction{{
			LineRef: "20261006210000NOK8903/1/1", Account: accountA, BookedOn: day(6), ValueOn: ptr(day(6)),
			AmountMinor: 200000, Currency: "NOK", KID: "0010090", DebtorName: "Kunde AS", DebtorAccount: "98765432109",
			ArchiveRef: "678901231", BankCode: "PMNT/RCDT/DMCT", Ordinal: 1,
		}, {
			LineRef: "20261006210000NOK8903/1/2", Account: accountA, BookedOn: day(6), ValueOn: ptr(day(6)),
			AmountMinor: 100000, Currency: "NOK", RemittanceText: "Faktura 1010", DebtorName: "Snekker Hansen",
			ArchiveRef: "678901232", BankCode: "PMNT/RCDT/DMCT", Ordinal: 1,
		}, {
			LineRef: "20261006210000NOK8903/2/1", Account: accountA, Debit: true, BookedOn: day(6),
			AmountMinor: 200000, Currency: "NOK", KID: "0010090", DebtorName: "Kunde AS",
			ArchiveRef: "678901231", BankCode: "PMNT/RCDT/DMCT", Ordinal: 1,
		}}
		if !reflect.DeepEqual(f.Transactions, want) || !reflect.DeepEqual(f.Ignored, ignored(0, 1, 1)) ||
			f.Identity != "NO20261007-000001|2026-10-06T21:00:00.5+02:00" || !reflect.DeepEqual(f.Accounts, []string{accountA}) {
			t.Errorf("v08-copy-batch.xml = %+v\nwant transactions\n%+v", f, want)
		}
	})

	t.Run("encodings: ISO-8859-1 declared, a BOM, an encoding not read", func(t *testing.T) {
		t.Parallel()
		utf := camtFixture(t, "v02-reversals.xml")
		want := mustCamt(t, utf)
		var latin []byte
		for _, r := range string(swap(t, utf, `encoding="UTF-8"`, `encoding="ISO-8859-1"`, 1)) {
			latin = append(latin, byte(r))
		}
		if got := mustCamt(t, latin); !reflect.DeepEqual(got, want) {
			t.Errorf("ISO-8859-1: %+v", got)
		}
		if got := mustCamt(t, append([]byte("\xEF\xBB\xBF"), utf...)); !reflect.DeepEqual(got, want) {
			t.Errorf("with a BOM: %+v", got)
		}
		_, err := bankfile.ParseCamt054(swap(t, utf, `encoding="UTF-8"`, `encoding="EBCDIC-NO"`, 1), today)
		refusal(t, "EBCDIC", err, "file", "EBCDIC-NO", "UTF-8, ISO-8859-1 and US-ASCII")
	})

	t.Run("two notifications", func(t *testing.T) {
		t.Parallel()
		r4 := string(camtFixture(t, "v02-r4-example.xml"))
		start, end := strings.Index(r4, "<Ntfctn>"), strings.Index(r4, "</Ntfctn>")+len("</Ntfctn>")
		second := strings.NewReplacer("20261006173500NOK8903", "20261006173500NOK7947",
			"<Id>12345678903</Id>", "<Id>86011117947</Id>", "123456789", "123456780").Replace(r4[start:end])
		two := []byte(r4[:end] + second + r4[end:])
		f := mustCamt(t, two)
		if !reflect.DeepEqual(f.Accounts, []string{accountA, accountB}) || len(f.Transactions) != 2 ||
			f.Transactions[1].LineRef != "20261006173500NOK7947/1/1" || f.Transactions[1].Account != accountB {
			t.Errorf("two notifications: %+v", f)
		}
		broken := []byte(r4[:end] + strings.Replace(second, "<AcctSvcrRef>123456780</AcctSvcrRef>",
			"<AcctSvcrRef>"+strings.Repeat("9", 36)+"</AcctSvcrRef>", 1) + r4[end:])
		_, err := bankfile.ParseCamt054(broken, today)
		refusal(t, "the second's archive reference", err, "Ntfctn[2]/Ntry[1]/NtryDtls/TxDtls[1]/Refs/AcctSvcrRef", "36 characters")
	})

	r4 := camtFixture(t, "v02-r4-example.xml")
	r408 := camtFixture(t, "v08-r4-example.xml")
	batch := camtFixture(t, "v02-batch.xml")
	reversals := camtFixture(t, "v02-reversals.xml")
	copyBatch := camtFixture(t, "v08-copy-batch.xml")
	// deep is R4's example with n elements nested in its transaction's
	// SplmtryData envelope, which sits at depth 8.
	deep := func(n int) []byte {
		return swap(t, r4, "</RltdDts>", "</RltdDts><SplmtryData><Envlp>"+strings.Repeat("<x>", n)+
			strings.Repeat("</x>", n)+"</Envlp></SplmtryData>", 1)
	}
	// wide is R4's example with n empty elements there; bound is the most
	// it may hold with its one transaction, 10 000 + 100 elements.
	bound := 10_100 - countElements(t, r4) - 2
	wide := func(n int) []byte {
		return swap(t, r4, "</RltdDts>", "</RltdDts><SplmtryData><Envlp>"+strings.Repeat("<x/>", n)+"</Envlp></SplmtryData>", 1)
	}

	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name  string
			file  []byte
			today time.Time
			where string
			want  []string
		}{
			{"the TxDtls not summing to the entry", swap(t, batch, `<Amt Ccy="NOK">4747.50</Amt>`, `<Amt Ccy="NOK">4747.51</Amt>`, 1), today,
				"Ntry[1]", []string{"sum to 4747.50", "4747.51"}},
			{"NbOfTxs", swap(t, batch, "<NbOfTxs>3</NbOfTxs>", "<NbOfTxs>4</NbOfTxs>", 1), today,
				"Ntry[1]/NtryDtls/Btch/NbOfTxs", []string{"4", "3 TxDtls"}},
			{"TxsSummry's count of credits", swap(t, batch, "<TtlCdtNtries><NbOfNtries>1<", "<TtlCdtNtries><NbOfNtries>2<", 1), today,
				"Ntfctn/TxsSummry/TtlCdtNtries/NbOfNtries", []string{"2 entries", "holds 1"}},
			{"TxsSummry's sum of debits", swap(t, reversals, "<Sum>1794.50</Sum>", "<Sum>1794.40</Sum>", 1), today,
				"Ntfctn/TxsSummry/TtlDbtNtries/Sum", []string{"1794.40", "1794.50"}},
			{"TxsSummry's net, .02", swap(t, reversals, "<TtlNetNtryAmt>1494.50<", "<TtlNetNtryAmt>1494.40<", 1), today,
				"Ntfctn/TxsSummry/TtlNtries/TtlNetNtryAmt", []string{"-1494.40", "-1494.50"}},
			{"TxsSummry's net direction, .08", swap(t, copyBatch, "<CdtDbtInd>CRDT</CdtDbtInd></TtlNetNtry>", "<CdtDbtInd>DBIT</CdtDbtInd></TtlNetNtry>", 1), today,
				"Ntfctn/TxsSummry/TtlNtries/TtlNetNtry", []string{"-1150.00", "1150.00"}},
			{"USD on the account", swap(t, r4, "<Ccy>NOK</Ccy>", "<Ccy>USD</Ccy>", 1), today,
				"Ntfctn/Acct/Ccy", []string{"USD"}},
			{"USD on a transaction", swap(t, r4, `<TxAmt><Amt Ccy="NOK">`, `<TxAmt><Amt Ccy="USD">`, 1), today,
				"Ntry[1]/NtryDtls/TxDtls[1]/AmtDtls/TxAmt/Amt", []string{"USD", "NOK"}},
			// Currency is the file's: an entry that would only be counted
			// still refuses it (research case m).
			{"USD on a debit that is only counted", swap(t, reversals, `<Amt Ccy="NOK">45.00</Amt>`, `<Amt Ccy="USD">45.00</Amt>`, 1), today,
				"Ntry[3]/Amt", []string{"USD"}},
			{"USD on .08's TxDtls/Amt", swap(t, r408, `<Amt Ccy="NOK">1250.00</Amt>`+"\n            <CdtDbtInd>", `<Amt Ccy="USD">1250.00</Amt>`+"\n            <CdtDbtInd>", 1), today,
				"Ntry[1]/NtryDtls/TxDtls[1]/Amt", []string{"USD", "booked amount"}},
			{"an Ntry/Amt without Ccy", swap(t, r4, `<Amt Ccy="NOK">1250.00</Amt>`+"\n        <CdtDbtInd>", `<Amt>1250.00</Amt>`+"\n        <CdtDbtInd>", 1), today,
				"Ntry[1]/Amt", []string{`""`, "NOK"}},
			{"a million attributes on one element", swap(t, r4, "<Nm>", "<Nm"+strings.Repeat(` a=""`, 1_000_000)+">", 1), today,
				"file", []string{"a tag longer than 4096 bytes"}},
			{"a quoted value of 5 000 '>'", swap(t, r4, "<Nm>", `<Nm a="`+strings.Repeat(">", 5000)+`">`, 1), today,
				"file", []string{"a tag longer than 4096 bytes"}},
			{"a bomb of namespace declarations", swap(t, r4, "<Nm>", "<Nm"+strings.Repeat(` xmlns:p="u"`, 100_000)+">", 1), today,
				"file", []string{"a tag longer than 4096 bytes"}},
			{"a CreDtTm of 42", swap(t, r4, "<CreDtTm>2026-10-06T17:35:00+02:00</CreDtTm>\n    </GrpHdr>",
				"<CreDtTm>2026-10-06T17:35:00.1234567890123456+02:00</CreDtTm>\n    </GrpHdr>", 1), today,
				"GrpHdr/CreDtTm", []string{"42 characters", "at most 35"}},
			{"three decimals", swap(t, r4, `<TxAmt><Amt Ccy="NOK">1250.00<`, `<TxAmt><Amt Ccy="NOK">1250.001<`, 1), today,
				"Ntry[1]/NtryDtls/TxDtls[1]/AmtDtls/TxAmt/Amt", []string{"1250.001", "two decimals"}},
			{"a negative amount", swap(t, r4, `<Amt Ccy="NOK">1250.00</Amt>`, `<Amt Ccy="NOK">-1250.00</Amt>`, 1), today,
				"Ntry[1]/Amt", []string{"not an amount"}},
			{"a decimal comma", swap(t, r4, `<Amt Ccy="NOK">1250.00</Amt>`, `<Amt Ccy="NOK">1250,00</Amt>`, 1), today,
				"Ntry[1]/Amt", []string{"not an amount"}},
			{"an amount past numeric(14,2)", swap(t, r4, `<Amt Ccy="NOK">1250.00</Amt>`, `<Amt Ccy="NOK">1000000000000.00</Amt>`, 1), today,
				"Ntry[1]/Amt", []string{"more than an amount can be"}},
			{"a DOCTYPE", swap(t, r4, `<Document `, `<!DOCTYPE Document><Document `, 1), today,
				"file", []string{"DOCTYPE"}},
			{"an XXE", swap(t, swap(t, r4, `<Document `, `<!DOCTYPE Document [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><Document `, 1),
				"Kunde AS", "&xxe;", 1), today, "file", []string{"DOCTYPE"}},
			{"an undeclared entity", swap(t, r4, "Kunde AS", "&xxe;", 1), today,
				"file", []string{"not well-formed", "xxe"}},
			{"not well-formed", swap(t, r4, "</Ntry>", "</Ntryx>", 1), today, "file", []string{"not well-formed"}},
			{"65 levels", deep(57), today, "file", []string{"65 levels", "at most 64"}},
			{"an element bomb", wide(bound + 1), today, "file", []string{"more than 10100 elements", "1 transactions"}},
			{"a third namespace", swap(t, r4, "camt.054.001.02", "camt.054.001.14", 1), today,
				"Document", []string{"camt.054.001.14"}},
			{"a second root", append(append([]byte{}, r4...), "<Document/>"...), today, "file", []string{"second root"}},
			{"a booking date after today", r4, day(5), "Ntry[1]/BookgDt", []string{"2026-10-06 is after today, 2026-10-05"}},
			{"a booking date before 2000", swap(t, r4, "<BookgDt><Dt>2026-10-06", "<BookgDt><Dt>1999-12-31", 1), today,
				"Ntry[1]/BookgDt", []string{"before 2000-01-01"}},
			{"a booked entry without a booking date", swap(t, r4, "<BookgDt><Dt>2026-10-06</Dt></BookgDt>", "", 1), today,
				"Ntry[1]/BookgDt", []string{"without a booking date"}},
			{"a booking date that is no date", swap(t, r4, "<BookgDt><Dt>2026-10-06", "<BookgDt><Dt>2026-02-30", 1), today,
				"Ntry[1]/BookgDt", []string{"2026-02-30"}},
			{"a value date that is no date", swap(t, r4, "<ValDt><Dt>2026-10-06", "<ValDt><Dt>06.10.2026", 1), today,
				"Ntry[1]/ValDt", []string{"06.10.2026"}},
			{"CdtDbtInd", swap(t, r4, "<CdtDbtInd>CRDT", "<CdtDbtInd>CRDIT", 1), today, "Ntry[1]/CdtDbtInd", []string{"CRDIT"}},
			{"an entry without an amount", swap(t, r4, `<Amt Ccy="NOK">1250.00</Amt>`+"\n        <CdtDbtInd>", "<CdtDbtInd>", 1), today,
				"Ntry[1]/Amt", []string{"no amount"}},
			{"a transaction without an amount", swap(t, r4, `<AmtDtls><TxAmt><Amt Ccy="NOK">1250.00</Amt></TxAmt></AmtDtls>`, "", 1), today,
				"Ntry[1]/NtryDtls/TxDtls[1]", []string{"no amount"}},
			{".02 without its status", swap(t, r4, "<Sts>BOOK</Sts>", "<Sts><Cd>BOOK</Cd></Sts>", 1), today,
				"Ntry[1]/Sts", []string{"no status"}},
			{".08 without its status", swap(t, r408, "<Sts><Cd>BOOK</Cd></Sts>", "<Sts>BOOK</Sts>", 1), today,
				"Ntry[1]/Sts", []string{"no status"}},
			{".08's two amounts disagreeing", swap(t, r408, `<TxAmt><Amt Ccy="NOK">1250.00<`, `<TxAmt><Amt Ccy="NOK">1250.01<`, 1), today,
				"Ntry[1]/NtryDtls/TxDtls[1]/Amt", []string{"1250.00", "1250.01"}},
			{".08's transaction against its entry", swap(t, r408, "<CdtDbtInd>CRDT</CdtDbtInd>\n            <AmtDtls>", "<CdtDbtInd>DBIT</CdtDbtInd>\n            <AmtDtls>", 1), today,
				"Ntry[1]/NtryDtls/TxDtls[1]/CdtDbtInd", []string{"DBIT", "CRDT"}},
			{"an account that is none", swap(t, r4, "<Id>12345678903</Id>", "<Id>1234567890</Id>", 1), today,
				"Ntfctn/Acct/Id", []string{"1234567890"}},
			{"an IBAN failing mod-97", swap(t, camtFixture(t, "v08-iban.xml"), "NO93 8601", "NO94 8601", 1), today,
				"Ntfctn/Acct/Id", []string{"NO94"}},
			{"no MsgId", swap(t, r4, "<MsgId>NO20261006-000123</MsgId>", "<MsgId> </MsgId>", 1), today,
				"GrpHdr/MsgId", []string{"no identification"}},
			{"a MsgId of 36", swap(t, r4, "NO20261006-000123", strings.Repeat("M", 36), 1), today,
				"GrpHdr/MsgId", []string{"36 characters", "at most 35"}},
			{"a CreDtTm that is none", swap(t, r4, "<CreDtTm>2026-10-06T17:35:00+02:00", "<CreDtTm>i dag", 1), today,
				"GrpHdr/CreDtTm", []string{"i dag"}},
			{"no notification Id", swap(t, r4, "<Id>20261006173500NOK8903</Id>", "", 1), today,
				"Ntfctn/Id", []string{"no identification"}},
			{"no notification", bytes.Join([][]byte{r4[:bytes.Index(r4, []byte("<Ntfctn>"))], r4[bytes.Index(r4, []byte("</Ntfctn>"))+9:]}, nil), today,
				"file", []string{"no notification"}},
			{"no entry", bytes.Join([][]byte{r4[:bytes.Index(r4, []byte("<Ntry>"))], r4[bytes.Index(r4, []byte("</Ntry>"))+7:]}, nil), today,
				"file", []string{"no entries"}},
			{"no message", []byte(`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.054.001.02"/>`), today,
				"Document", []string{"BkToCstmrDbtCdtNtfctn"}},
			{"an archive reference of 36", swap(t, r4, "<AcctSvcrRef>123456789</AcctSvcrRef>", "<AcctSvcrRef>"+strings.Repeat("9", 36)+"</AcctSvcrRef>", 1), today,
				"Ntry[1]/NtryDtls/TxDtls[1]/Refs/AcctSvcrRef", []string{"36 characters"}},
			{"a debtor account of 35", swap(t, r4, "<Id>98765432109</Id>", "<Id>"+strings.Repeat("9", 35)+"</Id>", 1), today,
				"Ntry[1]/NtryDtls/TxDtls[1]/RltdPties/DbtrAcct/Id", []string{"35 characters"}},
			{"a proprietary code of 36", swap(t, swap(t, r4, "<Domn><Cd>PMNT</Cd><Fmly><Cd>RCDT</Cd><SubFmlyCd>VCOM</SubFmlyCd></Fmly></Domn>", "", 1),
				"<Cd>230</Cd>", "<Cd>"+strings.Repeat("2", 36)+"</Cd>", 1), today, "Ntry[1]/BkTxCd", []string{"36 characters"}},
		} {
			when := c.today
			_, err := bankfile.ParseCamt054(c.file, when)
			refusal(t, c.name, err, c.where, c.want...)
		}
	})

	t.Run("the caps at their bounds", func(t *testing.T) {
		t.Parallel()
		if _, err := bankfile.ParseCamt054(deep(56), today); err != nil {
			t.Errorf("64 levels: %v", err)
		}
		if _, err := bankfile.ParseCamt054(wide(bound), today); err != nil {
			t.Errorf("10 100 elements with one transaction: %v", err)
		}

		// A lawful file at the transaction cap, every TxDtls of 30
		// elements, is read (m4): 5 000 × 30 = 150 000 elements, within
		// 10 000 + 100 × 5 000.
		lawful := thirtyElementTxs(t, bankfile.MaxTransactions)
		if n := countElements(t, lawful); n < 30*bankfile.MaxTransactions {
			t.Fatalf("the lawful file has %d elements", n)
		}
		f, err := bankfile.ParseCamt054(lawful, today)
		if err != nil || len(f.Transactions) != bankfile.MaxTransactions {
			t.Fatalf("5 000 transactions of 30 elements: %v", err)
		}
		_, err = bankfile.ParseCamt054(thirtyElementTxs(t, bankfile.MaxTransactions+1), today)
		refusal(t, "5 001 transactions", err, "Ntry[51]/NtryDtls/TxDtls[1]", "more than 5000 transactions")

		_, err = bankfile.ParseCamt054(append(append([]byte{}, r4...), bytes.Repeat([]byte(" "), bankfile.MaxBytes)...), today)
		refusal(t, "past MaxBytes", err, "file", "10 MiB")
	})

	t.Run("what the format allows", func(t *testing.T) {
		t.Parallel()
		// An amount with fewer decimals, or trailing zeros past two, is
		// the same amount.
		f := mustCamt(t, swap(t, r4, "1250.00<", "1250.000<", -1))
		if f.Transactions[0].AmountMinor != 125000 {
			t.Errorf("1250.000 = %d øre", f.Transactions[0].AmountMinor)
		}
		f = mustCamt(t, swap(t, r4, "1250.00<", "1250<", -1))
		if f.Transactions[0].AmountMinor != 125000 {
			t.Errorf("1250 = %d øre", f.Transactions[0].AmountMinor)
		}
		// A KID is the SCOR reference only: another type is no KID.
		f = mustCamt(t, swap(t, r4, "<Cd>SCOR</Cd>", "<Cd>RADM</Cd>", 1))
		if f.Transactions[0].KID != "" {
			t.Errorf("a RADM reference read as the KID %q", f.Transactions[0].KID)
		}
		// Control characters in a text become spaces; a long text is cut
		// to 1 000 characters, a long name to 140.
		long := strings.Repeat("æ", 140)
		f = mustCamt(t, swap(t, swap(t, r4, "<RmtInf>", "<RmtInf>"+strings.Repeat("<Ustrd>"+long+"</Ustrd>", 8)+
			"<Ustrd>a&#9;b</Ustrd>", 1), "Kunde AS", strings.Repeat("ø", 141), 1))
		if got := f.Transactions[0].RemittanceText; len([]rune(got)) != 1000 || !strings.HasPrefix(got, long+" "+long) {
			t.Errorf("the text is %d characters", len([]rune(got)))
		}
		if got := f.Transactions[0].DebtorName; got != strings.Repeat("ø", 140) {
			t.Errorf("the name is %d characters", len([]rune(got)))
		}
		f = mustCamt(t, swap(t, r4, "<RmtInf>", "<RmtInf><Ustrd>a&#9;b&#10;c</Ustrd>", 1))
		if got := f.Transactions[0].RemittanceText; got != "a b c" {
			t.Errorf("control characters: %q", got)
		}
		// Only a booked amount's currency is judged: an instructed, a
		// counter-value or a remitted amount, a charge or the entry's own
		// AmtDtls in euros say what was sent, not what was booked.
		for name, b := range map[string][]byte{
			"a remitted amount":    swap(t, r4, `<RmtdAmt Ccy="NOK">`, `<RmtdAmt Ccy="EUR">`, 1),
			"an instructed amount": swap(t, r4, "<AmtDtls><TxAmt>", `<AmtDtls><InstdAmt><Amt Ccy="EUR">110.00</Amt></InstdAmt><TxAmt>`, 1),
			"a counter-value":      swap(t, r4, "</TxAmt></AmtDtls>", `</TxAmt><CntrValAmt><Amt Ccy="EUR">110.00</Amt></CntrValAmt></AmtDtls>`, 1),
			"a charge, .02":        swap(t, r4, "</AmtDtls>", `</AmtDtls><Chrgs><Amt Ccy="EUR">2.00</Amt></Chrgs>`, 1),
			"a charge, .08":        swap(t, r408, "</AmtDtls>", `</AmtDtls><Chrgs><Rcrd><Amt Ccy="EUR">2.00</Amt></Rcrd></Chrgs>`, 1),
			"the entry's AmtDtls":  swap(t, r4, "<BkTxCd>", `<AmtDtls><InstdAmt><Amt Ccy="EUR">110.00</Amt></InstdAmt></AmtDtls><BkTxCd>`, 1),
		} {
			if got, err := bankfile.ParseCamt054(b, today); err != nil || got.Transactions[0].AmountMinor != 125000 {
				t.Errorf("EUR on %s: %v", name, err)
			}
		}
		// A SCOR reference longer than a KID's 25 bytes is no KID: it
		// leads the text, and the line goes to the queue without one.
		for name, c := range map[string]struct{ ref, ustrd, want string }{
			"26 digits":                 {strings.Repeat("1", 26), "", "SCOR " + strings.Repeat("1", 26)},
			"13 characters in 26 bytes": {strings.Repeat("æ", 13), "", "SCOR " + strings.Repeat("æ", 13)},
			"with a text":               {strings.Repeat("1", 26), "Faktura 1001", "SCOR " + strings.Repeat("1", 26) + " Faktura 1001"},
		} {
			b := swap(t, r4, "<Ref>0010017</Ref>", "<Ref>"+c.ref+"</Ref>", 1)
			if c.ustrd != "" {
				b = swap(t, b, "<RmtInf>", "<RmtInf><Ustrd>"+c.ustrd+"</Ustrd>", 1)
			}
			got := mustCamt(t, b).Transactions[0]
			if got.KID != "" || got.RemittanceText != c.want {
				t.Errorf("a SCOR reference of %s: KID %q, text %q", name, got.KID, got.RemittanceText)
			}
		}
		if got := mustCamt(t, swap(t, r4, "<Ref>0010017</Ref>", "<Ref>"+strings.Repeat("1", 25)+"</Ref>", 1)).Transactions[0]; got.KID != strings.Repeat("1", 25) {
			t.Errorf("a SCOR reference of 25: KID %q", got.KID)
		}
		// A reference is measured in characters, as varchar(n) is: 35 of
		// them in 70 bytes is an archive reference.
		if got := mustCamt(t, swap(t, r4, "<AcctSvcrRef>123456789</AcctSvcrRef>", "<AcctSvcrRef>"+strings.Repeat("æ", 35)+"</AcctSvcrRef>", 1)).Transactions[0]; got.ArchiveRef != strings.Repeat("æ", 35) {
			t.Errorf("35 characters in 70 bytes: %q", got.ArchiveRef)
		}
		// A comment or CDATA longer than a tag may be is no tag.
		longTags := mustCamt(t, swap(t, r4, "<Nm>Kunde AS</Nm>", "<!--"+strings.Repeat("x", 10_000)+"--><Nm><![CDATA["+strings.Repeat("y", 5000)+"]]></Nm>", 1))
		if got := longTags.Transactions[0].DebtorName; got != strings.Repeat("y", 140) {
			t.Errorf("a long comment and CDATA: name %q", got)
		}

		// Without Ustrd, the entry's AddtlNtryInf is the text.
		withInfo := swap(t, r4, "</NtryDtls>", "</NtryDtls><AddtlNtryInf>Innbetaling</AddtlNtryInf>", 1)
		if got := mustCamt(t, withInfo).Transactions[0].RemittanceText; got != "Innbetaling" {
			t.Errorf("AddtlNtryInf: %q", got)
		}
		// A prefixed namespace is the same file.
		prefixed := strings.NewReplacer("<Document xmlns=", "<c:Document xmlns:c=", "</", "</c:", "<", "<c:").
			Replace(string(r4[bytes.Index(r4, []byte("<Document")):]))
		if got := mustCamt(t, []byte(prefixed)); len(got.Transactions) != 1 || got.Transactions[0].KID != "0010017" {
			t.Errorf("prefixed: %+v", got)
		}
	})
}

// countElements counts b's start elements.
func countElements(t *testing.T, b []byte) int {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(b))
	n := 0
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return n
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := tok.(xml.StartElement); ok {
			n++
		}
	}
}

// thirtyElementTxs is a .02 file of n transactions in entries of 100, each
// TxDtls padded to 30 elements with unstructured lines.
func thirtyElementTxs(t *testing.T, n int) []byte {
	t.Helper()
	var entries []bankfiletest.CamtEntry
	for i := 0; i < n; i++ {
		if i%100 == 0 {
			entries = append(entries, bankfiletest.CamtEntry{BookedOn: day(6), BankDomain: "PMNT/RCDT/VCOM"})
		}
		e := &entries[len(entries)-1]
		e.Txs = append(e.Txs, bankfiletest.CamtTx{AmountMinor: int64(100 + i), KID: "0010017", Ustrd: "Faktura",
			Debtor: "Kunde AS", DebtorAccount: "98765432109", AcctSvcrRef: "123456789"})
	}
	b := bankfiletest.Camt054(v02, "LAWFUL-1", day(6), accountA, entries...)
	one := b[bytes.Index(b, []byte("<TxDtls>")) : bytes.Index(b, []byte("</TxDtls>"))+len("</TxDtls>")]
	pad := 30 - countElements(t, one)
	if pad < 0 {
		t.Fatalf("a TxDtls of %d elements", 30-pad)
	}
	return bytes.ReplaceAll(b, []byte("<RmtInf>"), []byte("<RmtInf>"+strings.Repeat("<Ustrd>x</Ustrd>", pad)))
}

// The fingerprint over camt lines: two identical TxDtls stay two; a
// reversal is never its credit; the same payment in a re-ordered copy, or
// in the other version, is the same line; Danske's repeated entry
// reference changes nothing.
func TestFingerprint_CamtLines(t *testing.T) {
	t.Parallel()
	same := bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017", Debtor: "Kunde AS", DebtorAccount: "98765432109"}
	f := mustCamt(t, bankfiletest.Camt054(v02, "FP-1", day(6), accountA,
		bankfiletest.CamtEntry{BookedOn: day(6), Txs: []bankfiletest.CamtTx{same, same}}))
	a, b := f.Transactions[0], f.Transactions[1]
	if a.Ordinal != 1 || b.Ordinal != 2 || a.Fingerprint == b.Fingerprint {
		t.Errorf("identical TxDtls: ordinals %d, %d; fingerprints equal %v", a.Ordinal, b.Ordinal, a.Fingerprint == b.Fingerprint)
	}

	if f.Transactions[0].BankCode != "PMNT/RCDT/VCOM" {
		t.Errorf("the builder's default bank code: %q", f.Transactions[0].BankCode)
	}

	f = mustCamt(t, bankfiletest.Camt054(v02, "FP-2", day(6), accountA,
		bankfiletest.CamtEntry{BookedOn: day(6), Txs: []bankfiletest.CamtTx{same}},
		bankfiletest.CamtEntry{BookedOn: day(6), CreditDebit: "DBIT", Reversal: true, Txs: []bankfiletest.CamtTx{same}}))
	if len(f.Transactions) != 2 || !f.Transactions[1].Debit || f.Transactions[0].Fingerprint == f.Transactions[1].Fingerprint ||
		f.Transactions[1].Ordinal != 1 {
		t.Errorf("a reversal and its credit: %+v", f.Transactions)
	}

	// A re-ordered copy: CpyDplctInd COPY and a new MsgId — another file,
	// the same lines.
	r4 := camtFixture(t, "v02-r4-example.xml")
	copied := swap(t, swap(t, r4, "<MsgId>NO20261006-000123", "<MsgId>NO20261007-000999", 1),
		"<CreDtTm>2026-10-06T17:35:00+02:00</CreDtTm>\n      <Acct>",
		"<CreDtTm>2026-10-06T17:35:00+02:00</CreDtTm>\n      <CpyDplctInd>COPY</CpyDplctInd>\n      <Acct>", 1)
	original, copy := mustCamt(t, r4), mustCamt(t, copied)
	if original.Identity == copy.Identity || original.Transactions[0].Fingerprint != copy.Transactions[0].Fingerprint {
		t.Errorf("a copy: identities %q, %q; fingerprints equal %v", original.Identity, copy.Identity,
			original.Transactions[0].Fingerprint == copy.Transactions[0].Fingerprint)
	}

	danske := mustCamt(t, camtFixture(t, "v02-danske-shaped.xml"))
	if danske.Transactions[0].Fingerprint == danske.Transactions[1].Fingerprint {
		t.Error("Danske's two entries sharing an entry reference have one fingerprint")
	}
}

// The paths R4 §3.2's table names, pinned one by one in place of the XSD
// oracle (reading 36): each element's value is read from its version's path
// and from no other, so a path that moves breaks a case here.
func TestBankFileCamt_ElementPaths(t *testing.T) {
	t.Parallel()
	r4 := map[string][]byte{v02: camtFixture(t, "v02-r4-example.xml"), v08: camtFixture(t, "v08-r4-example.xml")}
	tx := func(f *bankfile.File) bankfile.Transaction { return f.Transactions[0] }
	for _, c := range []struct {
		version, path, old, new string
		got                     func(*bankfile.File) string
		want                    string
	}{
		{v02, "GrpHdr/MsgId", "NO20261006-000123", "M-1", func(f *bankfile.File) string { return f.Identity },
			"M-1|2026-10-06T17:35:00+02:00"},
		{v08, "GrpHdr/CreDtTm", "<CreDtTm>2026-10-06T17:35:00+02:00</CreDtTm>\n    </GrpHdr>",
			"<CreDtTm>2026-10-06T17:35:01</CreDtTm>\n    </GrpHdr>", func(f *bankfile.File) string { return f.Identity },
			"NO20261006-000123|2026-10-06T17:35:01"},
		{v02, "Ntfctn/Id", "20261006173500NOK8903", "N-1", func(f *bankfile.File) string { return tx(f).LineRef }, "N-1/1/1"},
		{v08, "Ntfctn/Acct/Id/Othr/Id", "<Othr><Id>12345678903</Id>", "<Othr><Id>86011117947</Id>",
			func(f *bankfile.File) string { return f.Accounts[0] + " " + tx(f).Account }, accountB + " " + accountB},
		{v02, "Ntfctn/Acct/Id/IBAN", "<Othr><Id>12345678903</Id><SchmeNm><Cd>BBAN</Cd></SchmeNm></Othr>", "<IBAN>NO9386011117947</IBAN>",
			func(f *bankfile.File) string { return tx(f).Account }, accountB},
		{v02, "Ntry/Amt and TxDtls/AmtDtls/TxAmt/Amt", "1250.00<", "99.90<",
			func(f *bankfile.File) string { return num(tx(f).AmountMinor) }, "9990"},
		{v08, "Ntry/Amt and TxDtls/Amt (and AmtDtls/TxAmt/Amt)", "1250.00<", "99.90<",
			func(f *bankfile.File) string { return num(tx(f).AmountMinor) }, "9990"},
		{v08, "TxDtls/Amt alone", `<AmtDtls><TxAmt><Amt Ccy="NOK">1250.00</Amt></TxAmt></AmtDtls>`, "",
			func(f *bankfile.File) string { return num(tx(f).AmountMinor) }, "125000"},
		{v02, "Ntry/Sts is BOOK, not pending", "<Sts>BOOK</Sts>", "<Sts>PDNG</Sts>",
			func(f *bankfile.File) string { return num(int64(f.Ignored[bankfile.IgnoredNotBooked])) }, "1"},
		{v08, "Ntry/Sts/Cd is BOOK, not pending", "<Sts><Cd>BOOK</Cd></Sts>", "<Sts><Cd>PDNG</Cd></Sts>",
			func(f *bankfile.File) string { return num(int64(f.Ignored[bankfile.IgnoredNotBooked])) }, "1"},
		{v02, "Ntry/BookgDt/Dt", "<BookgDt><Dt>2026-10-06", "<BookgDt><Dt>2026-10-01",
			func(f *bankfile.File) string { return tx(f).BookedOn.Format("2006-01-02") }, "2026-10-01"},
		{v08, "Ntry/BookgDt/DtTm", "<BookgDt><Dt>2026-10-06</Dt>", "<BookgDt><DtTm>2026-10-02T00:30:00+02:00</DtTm>",
			func(f *bankfile.File) string { return tx(f).BookedOn.Format(time.RFC3339) }, "2026-10-02T00:00:00Z"},
		{v02, "Ntry/ValDt/Dt", "<ValDt><Dt>2026-10-06", "<ValDt><Dt>2026-10-03",
			func(f *bankfile.File) string { return tx(f).ValueOn.Format("2006-01-02") }, "2026-10-03"},
		{v02, "Ntry/CdtDbtInd and RvslInd", "<CdtDbtInd>CRDT</CdtDbtInd>", "<CdtDbtInd>DBIT</CdtDbtInd><RvslInd>true</RvslInd>",
			func(f *bankfile.File) string { return num(boolInt(tx(f).Debit)) }, "1"},
		{v08, "Ntry/BkTxCd/Domn", "<SubFmlyCd>VCOM", "<SubFmlyCd>RRTN",
			func(f *bankfile.File) string { return tx(f).BankCode }, "PMNT/RCDT/RRTN"},
		{v02, "Ntry/BkTxCd/Prtry/Cd without Domn", "<Domn><Cd>PMNT</Cd><Fmly><Cd>RCDT</Cd><SubFmlyCd>VCOM</SubFmlyCd></Fmly></Domn>", "",
			func(f *bankfile.File) string { return tx(f).BankCode }, "230"},
		{v02, "TxDtls/Refs/AcctSvcrRef, not the entry's", "<AcctSvcrRef>123456789</AcctSvcrRef>", "",
			func(f *bankfile.File) string { return tx(f).ArchiveRef }, ""},
		{v02, "RltdPties/Dbtr/Nm", "<Nm>Kunde AS</Nm>", "<Nm>Annen AS</Nm>",
			func(f *bankfile.File) string { return tx(f).DebtorName }, "Annen AS"},
		{v02, "not .08's RltdPties/Dbtr/Pty/Nm", "<Dbtr><Nm>Kunde AS</Nm></Dbtr>", "<Dbtr><Pty><Nm>Kunde AS</Nm></Pty></Dbtr>",
			func(f *bankfile.File) string { return tx(f).DebtorName }, ""},
		{v08, "RltdPties/Dbtr/Pty/Nm", "<Nm>Kunde AS</Nm>", "<Nm>Annen AS</Nm>",
			func(f *bankfile.File) string { return tx(f).DebtorName }, "Annen AS"},
		{v08, "not .02's RltdPties/Dbtr/Nm", "<Dbtr><Pty><Nm>Kunde AS</Nm></Pty></Dbtr>", "<Dbtr><Nm>Kunde AS</Nm></Dbtr>",
			func(f *bankfile.File) string { return tx(f).DebtorName }, ""},
		{v02, "RltdPties/DbtrAcct/Id/IBAN", "<DbtrAcct><Id><Othr><Id>98765432109</Id></Othr></Id>", "<DbtrAcct><Id><IBAN>NO9386011117947</IBAN></Id>",
			func(f *bankfile.File) string { return tx(f).DebtorAccount }, "NO9386011117947"},
		{v08, "RmtInf/Strd/CdtrRefInf/Ref", "<Ref>0010017</Ref>", "<Ref> 0010025 </Ref>",
			func(f *bankfile.File) string { return tx(f).KID }, "0010025"},
		{v02, "CdtrRefInf/Tp/CdOrPrtry/Cd = SCOR", "<Cd>SCOR</Cd>", "<Cd>scor</Cd>",
			func(f *bankfile.File) string { return tx(f).KID }, ""},
		{v08, "RmtInf/Ustrd", "<RmtInf>", "<RmtInf><Ustrd>Faktura 1001</Ustrd>",
			func(f *bankfile.File) string { return tx(f).RemittanceText }, "Faktura 1001"},
	} {
		f, err := bankfile.ParseCamt054(swap(t, r4[c.version], c.old, c.new, -1), today)
		if err != nil {
			t.Errorf("%s %s: %v", c.version, c.path, err)
			continue
		}
		if got := c.got(f); got != c.want {
			t.Errorf("%s %s: read %q, want %q", c.version, c.path, got, c.want)
		}
	}
}

func num(n int64) string { return strconv.FormatInt(n, 10) }

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Whatever the builder writes, in either version and every entry shape,
// the parser accepts, and the parsed file agrees with what was built.
func TestBankfiletest_CamtIsAccepted(t *testing.T) {
	t.Parallel()
	kid := bankfiletest.CamtTx{AmountMinor: 125000, KID: "0010017", Debtor: "Kunde AS", DebtorAccount: "98765432109", AcctSvcrRef: "1"}
	text := bankfiletest.CamtTx{AmountMinor: 49950, Ustrd: "Faktura 1002 & co", Debtor: "Ærlig Øst <AS>", DebtorAccount: "NO9386011117947"}
	zero := bankfiletest.CamtTx{AmountMinor: 0, KID: "0010025"}
	entries := []bankfiletest.CamtEntry{
		{BookedOn: day(5), BankDomain: "PMNT/RCDT/VCOM", BankProprietary: "230", Txs: []bankfiletest.CamtTx{kid, text, zero}},
		{BookedOn: day(6), CreditDebit: "DBIT", Reversal: true, Txs: []bankfiletest.CamtTx{kid}},
		{BookedOn: day(6), CreditDebit: "DBIT", BankDomain: "PMNT/ICDT/RRTN", Txs: []bankfiletest.CamtTx{text}},
		{BookedOn: day(6), CreditDebit: "DBIT", BankDomain: "ACMT/MDOP/CHRG", AmountMinor: 4500, AddtlInfo: "Gebyr"},
		{Status: "PDNG", Txs: []bankfiletest.CamtTx{kid}},
		{BookedOn: day(6), BankProprietary: "INT", AmountMinor: 12575, AddtlInfo: "Renter"},
	}
	for _, version := range []string{v02, v08} {
		for _, account := range []string{accountA, "NO9386011117947"} {
			b := bankfiletest.Camt054(version, "BUILT-1", time.Date(2026, 10, 6, 18, 0, 0, 0, time.UTC), account, entries...)
			f, err := bankfile.ParseCamt054(b, today)
			if err != nil {
				t.Errorf("%s on %s: %v", version, account, err)
				continue
			}
			want := []struct {
				debit  bool
				amount int64
				kid    string
				text   string
				name   string
			}{
				{false, 125000, "0010017", "", "Kunde AS"},
				{false, 49950, "", "Faktura 1002 & co", "Ærlig Øst <AS>"},
				{true, 125000, "0010017", "", "Kunde AS"},
				{true, 49950, "", "Faktura 1002 & co", "Ærlig Øst <AS>"},
				{false, 12575, "", "Renter", ""},
			}
			if len(f.Transactions) != len(want) || !reflect.DeepEqual(f.Ignored, ignored(1, 1, 1)) || f.Version != version ||
				f.Identity != "BUILT-1|2026-10-06T18:00:00+00:00" {
				t.Errorf("%s on %s: %d transactions, ignored %v, %q %q", version, account, len(f.Transactions), f.Ignored, f.Version, f.Identity)
				continue
			}
			for i, w := range want {
				got := f.Transactions[i]
				if got.Debit != w.debit || got.AmountMinor != w.amount || got.KID != w.kid || got.RemittanceText != w.text || got.DebtorName != w.name {
					t.Errorf("%s on %s: transaction %d = %+v, want %+v", version, account, i, got, w)
				}
			}
		}
	}
}
