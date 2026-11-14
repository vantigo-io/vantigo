package saft

import (
	"bytes"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRead_SkatteetatensExamples reads Skatteetaten's own 1.30 and 1.40
// example files: their header, accounts with balances, parties and tax table.
func TestRead_SkatteetatensExamples(t *testing.T) {
	for _, tc := range []struct{ file, version string }{
		{"example-1.30.xml", "1.30"}, {"example-1.40.xml", "1.40"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "examples", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			f, err := Read(bytes.NewReader(data), Limits{})
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if f.Version != tc.version || f.CompanyNumber != "999999999" {
				t.Errorf("header = %s %s", f.Version, f.CompanyNumber)
			}
			if len(f.Accounts) == 0 || len(f.Customers) == 0 || len(f.Suppliers) == 0 || len(f.TaxCodes) == 0 {
				t.Fatalf("master files = %d accounts, %d customers, %d suppliers, %d tax codes",
					len(f.Accounts), len(f.Customers), len(f.Suppliers), len(f.TaxCodes))
			}
			if f.Entries == 0 {
				t.Error("the transactions were not counted")
			}
			sum := new(big.Rat)
			for _, a := range f.Accounts {
				c, _ := Amount(a.Closing)
				sum.Add(sum, c)
			}
			if sum.Sign() != 0 {
				t.Errorf("the closing balances sum to %s", sum.FloatString(2))
			}
			if f.SelectionEnd.Before(f.SelectionStart) {
				t.Errorf("selection %v–%v", f.SelectionStart, f.SelectionEnd)
			}
		})
	}
}

const head = `<?xml version="1.0" encoding="UTF-8"?>`

// TestRead_RefusesWhatIsNotAFile holds the reader to what it must never do:
// expand an entity, follow an external reference, read a version before
// 1.30 or another root.
func TestRead_RefusesWhatIsNotAFile(t *testing.T) {
	root := `<AuditFile xmlns="urn:StandardAuditFile-Taxation-Financial:NO"><Header><AuditFileVersion>%s</AuditFileVersion></Header></AuditFile>`
	cases := map[string]string{
		"xxe": head + `<!DOCTYPE AuditFile [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>` +
			`<AuditFile xmlns="urn:StandardAuditFile-Taxation-Financial:NO"><Header><AuditFileVersion>&xxe;</AuditFileVersion></Header></AuditFile>`,
		"billion laughs": head + `<!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">` +
			`<!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">]>` +
			`<AuditFile xmlns="urn:StandardAuditFile-Taxation-Financial:NO"><Header><AuditFileVersion>&lol3;</AuditFileVersion></Header></AuditFile>`,
		"undeclared entity": head + strings.Replace(root, "%s", "&lol;", 1),
		"another root":      head + `<Invoice xmlns="urn:StandardAuditFile-Taxation-Financial:NO"/>`,
		"another namespace": head + `<AuditFile xmlns="urn:example"><Header/></AuditFile>`,
		"not xml":           "PK\x03\x04 a zip file",
		"latin-1":           `<?xml version="1.0" encoding="ISO-8859-1"?>` + strings.Replace(root, "%s", "1.30", 1),
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Read(strings.NewReader(doc), Limits{})
			var inv *InvalidError
			if !errors.As(err, &inv) {
				t.Fatalf("Read = %v, want an InvalidError", err)
			}
		})
	}
	for _, v := range []string{"1.20", "1.10", "1.0", "a"} {
		_, err := Read(strings.NewReader(head+strings.Replace(root, "%s", v, 1)), Limits{})
		if !errors.Is(err, ErrUnsupportedVersion) {
			t.Errorf("version %s: Read = %v, want ErrUnsupportedVersion", v, err)
		}
	}
}

// TestWriter_ValidAgainstTheXSD writes a small file with every structure the
// writer has and holds it to the 1.40 XSD with xmllint, when present.
func TestWriter_ValidAgainstTheXSD(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	day := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	w.Start(Header{Created: day, SoftwareCompany: "Vantigo", SoftwareID: "Vantigo", SoftwareVersion: "dev",
		Company: Company{RegistrationNumber: "999999999", Name: "Firma <&> AS", VATNumber: "999999999MVA",
			Address: &Address{Street: "Gata 1", City: "Oslo", PostalCode: "0150", Country: "NO", Type: "StreetAddress"}},
		From: day, To: day.AddDate(1, 0, -1)})
	r := func(s string) *big.Rat { v, _ := new(big.Rat).SetString(s); return v }
	w.MasterFiles(
		[]Account{{ID: "1500", Description: "Kundefordringer", GroupingCategory: "balanseverdiForOmloepsmiddel", GroupingCode: "1500", Opening: r("0"), Closing: r("125")},
			{ID: "3000", Description: "Salg", GroupingCategory: "NA", GroupingCode: "NA", Opening: r("0"), Closing: r("-100")},
			{ID: "2700", Description: "Utgående mva", GroupingCategory: "kortsiktigGjeld", GroupingCode: "2740", Opening: r("0"), Closing: r("-25")}},
		[]Party{{ID: "10001", Company: Company{Name: "Kunde AS", RegistrationNumber: "922222229"}, Type: "Company", Active: true,
			Balances: []BalanceAccount{{AccountID: "1500", Opening: r("0"), Closing: r("125")}}}},
		nil,
		[]TaxCode{{Code: "3", Description: "Utgående", Percentage: "25.00", StandardCode: "3", BaseRate: "100", Effective: &day}},
		[]AnalysisEntry{{Type: "P", TypeDescription: "Prosjekt", ID: "7", Description: "Bygg", Status: "Active"}})
	w.BeginEntries(1, r("125"), r("125"))
	w.BeginJournal(Journal{ID: "SALES", Description: "Salg", Type: "AR"})
	w.Transaction(Transaction{ID: "2026-000001", Period: 3, PeriodYear: 2026, Date: day, VoucherType: "sales_invoice",
		Description: "Faktura 1", SystemEntry: day, GLPosting: day, SystemID: "1001", Lines: []Line{
			{RecordID: "1", AccountID: "1500", CustomerID: "10001", Description: "", Amount: r("125"), ReferenceNumber: "1"},
			{RecordID: "2", AccountID: "3000", AnalysisType: "P", AnalysisID: "7", Description: "Salg", Amount: r("-100"),
				Tax: &TaxInformation{Code: "3", Percentage: "25.00", Base: r("100"), Amount: r("0")}},
			{RecordID: "3", AccountID: "2700", Description: "Mva", Amount: r("-25"),
				Tax: &TaxInformation{Code: "3", Percentage: "25.00", Base: r("100"), Amount: r("-25")}},
		}})
	w.EndJournal()
	if err := w.End(true); err != nil {
		t.Fatal(err)
	}
	n, d, c := w.Written()
	if n != 1 || d.Cmp(r("125")) != 0 || c.Cmp(r("125")) != 0 {
		t.Errorf("written = %d %s %s", n, d.FloatString(2), c.FloatString(2))
	}
	if !strings.Contains(buf.String(), "Firma &lt;&amp;&gt; AS") {
		t.Error("the company's name is not escaped")
	}
	ValidateWithXmllint(t, buf.Bytes(), filepath.Join("testdata", "xsd", "Norwegian_SAF-T_Financial_Schema_v_1.40.xsd"))
	if got := FileName("999999999", time.Date(2026, 11, 14, 9, 5, 7, 0, time.UTC), 1, 2); got != "SAF-T Financial_999999999_20261114090507_1_2.xml" {
		t.Errorf("FileName = %q", got)
	}
}

// ValidateWithXmllint validates doc against xsd when xmllint is on the PATH,
// and says in the log when it is not.
func ValidateWithXmllint(t *testing.T, doc []byte, xsd string) {
	t.Helper()
	ran, err := Xmllint(doc, xsd)
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Log("xmllint is not on the PATH: the file is not validated here; tools/saft/validate.sh does it in CI")
	}
}

// TestRead_ByteOrderMarkAndAmounts: a UTF-8 byte order mark is skipped; an
// amount is the XSD's monetary type or the file is refused, the element
// named; a limit on parties is held.
func TestRead_ByteOrderMarkAndAmounts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "examples", "example-1.40.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(bytes.NewReader(append([]byte("\xef\xbb\xbf"), data...)), Limits{}); err != nil {
		t.Errorf("with a byte order mark: %v", err)
	}
	for _, bad := range []string{"12500,00", "1.25e4", "12 500.00", "12500.001", "abc"} {
		doc := strings.Replace(string(data), "<ClosingDebitBalance>12500.00</ClosingDebitBalance>", "<ClosingDebitBalance>"+bad+"</ClosingDebitBalance>", 1)
		_, err := Read(strings.NewReader(doc), Limits{})
		var inv *InvalidError
		if !errors.As(err, &inv) || !strings.Contains(inv.Reason, "Account[1500]/Closing") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	var many *TooManyError
	if _, err := Read(bytes.NewReader(data), Limits{Parties: 1}); !errors.As(err, &many) {
		t.Errorf("a party past the limit: %v", err)
	}
	for _, good := range []string{"12500", "+12500.0", ".50", "0"} {
		if _, ok := Amount(good); !ok {
			t.Errorf("%q is an amount", good)
		}
	}
}

// TestXmllint_MissingInCIFails: where CI is set a missing xmllint is a
// failure, so no golden goes unvalidated in the test job (M4).
func TestXmllint_MissingInCIFails(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CI", "")
	if ran, err := Xmllint([]byte("<x/>"), "x.xsd"); ran || err != nil {
		t.Errorf("locally: ran %v, %v", ran, err)
	}
	t.Setenv("CI", "true")
	if _, err := Xmllint([]byte("<x/>"), "x.xsd"); err == nil {
		t.Error("in CI a missing xmllint passed")
	}
}
