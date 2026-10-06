package bankfiletest_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
)

// -update rewrites the OCR fixtures from fixtures below; the test then
// holds them to it.
var update = flag.Bool("update", false, "rewrite testdata/ocr from the builder")

func day(d int) time.Time { return time.Date(2026, time.October, d, 0, 0, 0, 0, time.UTC) }

var today = day(6)

const (
	accountA = "12345678903"
	accountB = "86011117947"
)

// fixtures is every committed OCR fixture as the builder's call that makes
// it. r4-example.ocr is R4 §3.1's example, copied verbatim from the
// research: the builder reproducing it byte for byte is what vouches for
// the builder, and so for the other fixtures.
var fixtures = map[string][]byte{
	"r4-example.ocr": bankfiletest.OCR("0000123",
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), Ordered: day(5), AmountMinor: 125000,
			KID: "0010017", ArchiveRef: "123456789", DebtorAccount: "98765432109"},
		bankfiletest.OCRPayment{Type: 16, Account: accountA, Settled: day(6), Ordered: day(6), AmountMinor: 49950,
			KID: "0010025", ArchiveRef: "234567890", DebtorAccount: "12340567894"}),
	"two-assignments.ocr": bankfiletest.OCR("0000124",
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(5), Ordered: day(4), AmountMinor: 100000,
			KID: "0010033", ArchiveRef: "300000001", DebtorAccount: "98765432109"},
		bankfiletest.OCRPayment{Type: 13, Account: accountA, Settled: day(6), Ordered: day(5), AmountMinor: 20050,
			KID: "0010041", ArchiveRef: "300000002"},
		bankfiletest.OCRPayment{Type: 16, Account: accountB, Settled: day(6), Ordered: day(6), AmountMinor: 75000,
			KID: "0010058", ArchiveRef: "300000003", DebtorAccount: "12340567894"}),
	"card-information.ocr": bankfiletest.OCR("0000125",
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), Ordered: day(5), AmountMinor: 10000,
			KID: "0010017", ArchiveRef: "400000001"},
		bankfiletest.OCRPayment{Type: 18, Account: accountA, Settled: day(6), AmountMinor: 5000, Negative: true,
			KID: "0010025", ArchiveRef: "400000002"},
		bankfiletest.OCRPayment{Type: 21, Account: accountA, Settled: day(6), AmountMinor: 3000,
			ArchiveRef: "400000003", Text: "Kjøp på nett, ordre 77"}),
	"negative-line.ocr": bankfiletest.OCR("0000126",
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(5), Ordered: day(5), AmountMinor: 100000,
			KID: "0010033", ArchiveRef: "500000001"},
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), Ordered: day(6), AmountMinor: 25000,
			Negative: true, KID: "0010041", ArchiveRef: "500000002"}),
	"mod11-dash.ocr": bankfiletest.OCR("0000127",
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), Ordered: day(6), AmountMinor: 50000,
			KID: "001009-", ArchiveRef: "600000001"}),
	"identical-lines.ocr": bankfiletest.OCR("0000128",
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), Ordered: day(6), AmountMinor: 125000,
			KID: "0010017", DebtorAccount: "98765432109"},
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), Ordered: day(6), AmountMinor: 125000,
			KID: "0010017", DebtorAccount: "98765432109"}),
	"overlap-a.ocr": bankfiletest.OCR("0000129", overlapX, overlapY,
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), Ordered: day(6), AmountMinor: 10000,
			KID: "0010058", ArchiveRef: "700000003"}),
	"overlap-b.ocr": bankfiletest.OCR("0000130", overlapX, overlapY,
		bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(6), Ordered: day(6), AmountMinor: 20000,
			KID: "0010066", ArchiveRef: "700000004"}),
}

// The two lines overlap-a.ocr and overlap-b.ocr share.
var (
	overlapX = bankfiletest.OCRPayment{Type: 10, Account: accountA, Settled: day(5), Ordered: day(5), AmountMinor: 125000,
		KID: "0010017", ArchiveRef: "700000001", DebtorAccount: "98765432109"}
	overlapY = bankfiletest.OCRPayment{Type: 13, Account: accountA, Settled: day(6), Ordered: day(5), AmountMinor: 49950,
		KID: "0010025", ArchiveRef: "700000002"}
)

// Every committed fixture is exactly the builder's output for its call —
// R4's example included, which was copied from the research, not built —
// and every record of it is 80 characters.
func TestBankfiletest_TheFixturesAreItsOutput(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "testdata", "ocr")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !*update && len(entries) != len(fixtures) {
		t.Errorf("testdata/ocr holds %d files, the builder %d fixtures", len(entries), len(fixtures))
	}
	for name, built := range fixtures {
		path := filepath.Join(dir, name)
		if *update && name != "r4-example.ocr" {
			if err := os.WriteFile(path, built, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		committed, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(committed, built) {
			t.Errorf("%s differs from the builder's output:\n%s\nbuilt:\n%s", name, committed, built)
		}
		for i, rec := range bytes.Split(bytes.TrimSuffix(committed, []byte("\n")), []byte("\n")) {
			if len(rec) != 80 {
				t.Errorf("%s record %d is %d bytes", name, i+1, len(rec))
			}
		}
	}
}

// Whatever the builder writes, the parser accepts, and the parsed file
// agrees with what was built: the transactions of types 10–17, their sum,
// the card information counted, the zero amounts counted.
func TestBankfiletest_OCRIsAccepted(t *testing.T) {
	t.Parallel()
	every := []bankfiletest.OCRPayment{}
	for tt := 10; tt <= 21; tt++ {
		every = append(every, bankfiletest.OCRPayment{Type: tt, Account: accountA, Settled: day(1 + tt%5),
			Ordered: day(1), AmountMinor: int64(1000 * tt), KID: "0010017", ArchiveRef: "1", Text: "tekst"})
	}
	every = append(every,
		bankfiletest.OCRPayment{Type: 10, Account: accountB, Settled: day(6), AmountMinor: 0, KID: "0010025"},
		bankfiletest.OCRPayment{Type: 12, Account: accountB, Settled: day(6), AmountMinor: 700, Negative: true, KID: "0010033"},
		bankfiletest.OCRPayment{Type: 20, Account: accountB, Settled: day(6), AmountMinor: 900, Negative: true, Text: "retur"},
	)
	for name, b := range fixtures {
		if _, err := bankfile.ParseOCR(b, today); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, payments := range map[string][]bankfiletest.OCRPayment{"every type, two accounts": every} {
		f, err := bankfile.ParseOCR(bankfiletest.OCR("0000001", payments...), today)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var wantTxs, wantCard, wantZero int
		var wantSum, gotSum int64
		for _, p := range payments {
			switch {
			case p.Type >= 18:
				wantCard++
			case p.AmountMinor == 0:
				wantZero++
			default:
				wantTxs++
				wantSum += p.AmountMinor
			}
		}
		for _, tx := range f.Transactions {
			gotSum += tx.AmountMinor
		}
		if len(f.Transactions) != wantTxs || gotSum != wantSum ||
			f.Ignored[bankfile.IgnoredCardInformation] != wantCard || f.Ignored[bankfile.IgnoredZeroAmount] != wantZero {
			t.Errorf("%s: %d transactions of %d øre, %d card, %d zero; want %d of %d, %d, %d", name,
				len(f.Transactions), gotSum, f.Ignored[bankfile.IgnoredCardInformation], f.Ignored[bankfile.IgnoredZeroAmount],
				wantTxs, wantSum, wantCard, wantZero)
		}
		if len(f.Accounts) != 2 || f.Accounts[0] != accountA || f.Accounts[1] != accountB {
			t.Errorf("%s: accounts %v", name, f.Accounts)
		}
	}
}
