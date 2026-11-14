package accounting_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"math/big"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/saft"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Delivery E: SAF-T Financial (A24, A25). The export is written by the
// worker as module.Workers builds it, downloaded through the API, validated
// against Skatteetaten's 1.40 XSD (xmllint, when present) and held to the
// books: the totals to the lines, the accounts to the trial balance, the
// parties to their reports, every reference to the master files. The import
// is the other way to set up the books, and a round trip gives the same
// balances back.

const xsd140 = "saft/testdata/xsd/Norwegian_SAF-T_Financial_Schema_v_1.40.xsd"

var fjordSeller = contracts.SellerProfile{LegalName: "Fjord Bygg AS", OrganisationNumber: "923 609 016", VATRegistered: true,
	AddressLine1: "Kaigata 4", PostalCode: "5003", City: "Bergen", Country: "NO", Email: "post@fjordbygg.no", Phone: "55000000",
	BankAccount: "1234.56.78903"}

// fakeProjects is Projects' directory as far as the export reads it.
type fakeProjects struct{ contracts.ProjectDirectory }

func (fakeProjects) Projects(_ context.Context, ids []int32) ([]contracts.ProjectEntry, error) {
	out := []contracts.ProjectEntry{}
	for _, id := range ids {
		out = append(out, contracts.ProjectEntry{ID: id, Code: fmt.Sprintf("P%d", id), Name: "Kaia"})
	}
	return out, nil
}

// saftBooks is books set up from 2026-01-01 in Norwegian, bimonthly, with
// the seller, the projects and an object store.
func saftBooks(t *testing.T, opts ...modtest.Option) *books {
	t.Helper()
	h := newHarness(t, append([]modtest.Option{modtest.WithSalesTax(fakeSalesTax{seller: fjordSeller}),
		modtest.WithProjects(fakeProjects{})}, opts...)...)
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	return &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
}

// sys posts a system voucher through the engine, as a source would.
func (b *books) sys(t *testing.T, kind, date string, source accounting.Source, lines ...accounting.Line) int64 {
	t.Helper()
	d, _ := time.Parse(time.DateOnly, date)
	var posted accounting.Posted
	err := db.WithTx(t.Context(), b.h.Pool(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		posted, err = accounting.Post(t.Context(), tx, accounting.Voucher{Kind: kind, Date: d, Description: kind + " " + date,
			Source: &source, Rule: "test." + kind, RuleVersion: 1, At: b.h.Now(), Lines: lines})
		return err
	})
	if err != nil {
		t.Fatalf("post %s: %v", kind, err)
	}
	return posted.ID
}

func rat(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }

// supplier makes a supplier through the API.
func (b *books) supplier(t *testing.T, name, org string) int64 {
	t.Helper()
	res := b.c.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": name, "organisationNumber": org,
		"addressLine": "Verkstedveien 2", "postalCode": "7010", "city": "Trondheim", "bankAccount": "12345678903"})
	if res.Status != http.StatusCreated {
		t.Fatalf("supplier = %d %s", res.Status, res.Body)
	}
	var s gen.AccountingSupplier
	res.JSON(&s)
	return s.Id
}

// ledger posts the small fixed ledger the golden is written from: every
// voucher kind built so far — the opening, a manual journal with a project,
// a sales invoice, its credit note, a payment, a charge, a write-off with its
// VAT taken back (tapPåKrav), a supplier invoice in euro with input VAT, a
// supplier credit note, a supplier payment, an expense and its
// reimbursement, reverse charge and VAT not deductible, a reversal and a
// closed VAT term's settlement.
func (b *books) ledger(t *testing.T) {
	t.Helper()
	if res := b.c.Do(http.MethodPost, base+"/opening", map[string]any{"lines": []map[string]any{
		{"accountId": b.acc("1920"), "amount": "50000.00"}, {"accountId": b.acc("2000"), "amount": "-30000.00"},
	}}); res.Status != http.StatusCreated {
		t.Fatalf("opening = %d %s", res.Status, res.Body)
	}
	mustPost(t, b.c, "2026-01-03", "Leie januar",
		line{"accountId": b.acc("6300"), "amount": "8000.00", "taxCodeId": b.code("1"), "projectId": 7},
		line{"accountId": b.acc("1920"), "amount": "-10000.00"})
	acme, beta := customerAcme, customerBeta
	c3 := b.code("3")
	b.sys(t, "sales_invoice", "2026-01-10", accounting.Source{Module: "invoices", Kind: "document", ID: 1},
		accounting.Line{Role: "receivables", Amount: rat("12500"), CustomerID: &acme, Item: &accounting.OpenItemSpec{DocumentNumber: "10001"}},
		accounting.Line{AccountID: b.acc("3000"), Amount: rat("-10000"), TaxCodeID: &c3, TaxRole: "base"},
		accounting.Line{AccountID: b.acc("2700"), Amount: rat("-2500"), TaxCodeID: &c3, TaxRole: "tax", TaxBase: rat("-10000")})
	b.sys(t, "sales_invoice", "2026-01-12", accounting.Source{Module: "invoices", Kind: "document", ID: 2},
		accounting.Line{Role: "receivables", Amount: rat("2500"), CustomerID: &beta, Item: &accounting.OpenItemSpec{DocumentNumber: "10002"}},
		accounting.Line{AccountID: b.acc("3000"), Amount: rat("-2000"), TaxCodeID: &c3, TaxRole: "base", ProjectID: ptrTo(int32(7))},
		accounting.Line{AccountID: b.acc("2700"), Amount: rat("-500"), TaxCodeID: &c3, TaxRole: "tax", TaxBase: rat("-2000")})
	b.sys(t, "sales_credit_note", "2026-01-15", accounting.Source{Module: "invoices", Kind: "document", ID: 3},
		accounting.Line{Role: "receivables", Amount: rat("-1250"), CustomerID: &acme},
		accounting.Line{AccountID: b.acc("3000"), Amount: rat("1000"), TaxCodeID: &c3, TaxRole: "base"},
		accounting.Line{AccountID: b.acc("2700"), Amount: rat("250"), TaxCodeID: &c3, TaxRole: "tax", TaxBase: rat("1000")})
	b.sys(t, "customer_payment", "2026-01-20", accounting.Source{Module: "invoices", Kind: "payment", ID: 1},
		accounting.Line{Role: "bank", Amount: rat("5000")},
		accounting.Line{Role: "receivables", Amount: rat("-5000"), CustomerID: &acme})
	c7 := b.code("7")
	b.sys(t, "charge", "2026-01-21", accounting.Source{Module: "invoices", Kind: "charge_payment", ID: 1},
		accounting.Line{Role: "bank", Amount: rat("70")},
		accounting.Line{Role: "charge_income", Amount: rat("-70"), TaxCodeID: &c7, TaxRole: "base"})
	b.writeOff(t, "2026-01-25", 2, "2000.00", "500.00")
	supplier := b.supplier(t, "Verksted AS", "923456783")
	c1 := b.code("1")
	b.sys(t, "supplier_invoice", "2026-02-02", accounting.Source{Module: "accounting", Kind: "supplier_invoice", ID: 1},
		accounting.Line{Role: "payables", Amount: rat("-11250"), SupplierID: &supplier, Item: &accounting.OpenItemSpec{DocumentNumber: "F-77"},
			Currency: "EUR", CurrencyAmount: rat("-1000"), ExchangeRate: rat("11.25")},
		accounting.Line{AccountID: b.acc("4005"), Amount: rat("9000"), TaxCodeID: &c1, TaxRole: "base", Currency: "EUR", CurrencyAmount: rat("800"), ExchangeRate: rat("11.25")},
		accounting.Line{AccountID: b.acc("2710"), Amount: rat("2250"), TaxCodeID: &c1, TaxRole: "tax", TaxBase: rat("9000"), Currency: "EUR", CurrencyAmount: rat("200"), ExchangeRate: rat("11.25")})
	b.sys(t, "supplier_credit_note", "2026-02-05", accounting.Source{Module: "accounting", Kind: "supplier_invoice", ID: 2},
		accounting.Line{Role: "payables", Amount: rat("1250"), SupplierID: &supplier},
		accounting.Line{AccountID: b.acc("4005"), Amount: rat("-1000"), TaxCodeID: &c1, TaxRole: "base"},
		accounting.Line{AccountID: b.acc("2710"), Amount: rat("-250"), TaxCodeID: &c1, TaxRole: "tax", TaxBase: rat("-1000")})
	b.sys(t, "supplier_payment", "2026-02-10", accounting.Source{Module: "accounting", Kind: "supplier_payment", ID: 1, Part: "a"},
		accounting.Line{Role: "payables", Amount: rat("5000"), SupplierID: &supplier},
		accounting.Line{Role: "bank", Amount: rat("-5000")})
	b.sys(t, "expense", "2026-02-12", accounting.Source{Module: "expenses", Kind: "entry.approved", ID: 1, Part: "1"},
		accounting.Line{AccountID: b.acc("7700"), Amount: rat("400")},
		accounting.Line{Role: "employee_payables", Amount: rat("-400")})
	b.sys(t, "reimbursement", "2026-02-14", accounting.Source{Module: "expenses", Kind: "entry.reimbursed", ID: 1, Part: "2"},
		accounting.Line{Role: "employee_payables", Amount: rat("400")},
		accounting.Line{Role: "bank", Amount: rat("-400")})
	b.taxed(t, "2026-02-16", "6300", "86", "2000.00")
	b.taxed(t, "2026-02-17", "6300", "87", "1000.00")
	wrong := mustPost(t, b.c, "2026-02-18", "Feil",
		line{"accountId": b.acc("6300"), "amount": "300.00"}, line{"accountId": b.acc("1920"), "amount": "-300.00"})
	if res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/vouchers/%d/reverse", base, wrong.Id), map[string]any{"date": "2026-02-19", "reason": "Feil konto"}); res.Status != http.StatusCreated {
		t.Fatalf("reverse = %d %s", res.Status, res.Body)
	}
	b.close(t, 2026, 1)
	// A loss's VAT taken back with no base posting beside it.
	b.sys(t, "write_off", "2026-03-10", accounting.Source{Module: "invoices", Kind: "write_off", ID: 99},
		accounting.Line{AccountID: b.acc("2700"), Amount: rat("100"), TaxCodeID: &c3, TaxRole: "tax", TaxBase: rat("400"), TaxSpecification: "tapPåKrav"},
		accounting.Line{Role: "receivables", Amount: rat("-100"), CustomerID: &acme})
	mustPost(t, b.c, "2026-03-05", "Leie mars",
		line{"accountId": b.acc("6300"), "amount": "8000.00", "taxCodeId": b.code("1")},
		line{"accountId": b.acc("1920"), "amount": "-10000.00"})
}

// saftWorker is the SAF-T worker as worker mode builds it.
func saftWorker(t *testing.T, h *harness) *accounting.SaftWorker {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if w.Name() == "accounting.saft" {
			sw, ok := w.(*accounting.SaftWorker)
			if !ok {
				t.Fatalf("accounting.saft is a %T", w)
			}
			return sw
		}
	}
	t.Fatal("module.Workers builds no accounting.saft")
	return nil
}

// export asks for an export, runs the worker and answers the export with its
// files' contents.
func (b *books) export(t *testing.T, body map[string]any) (gen.AccountingSaftExport, [][]byte) {
	t.Helper()
	res := b.c.Do(http.MethodPost, base+"/saft-exports", body)
	if res.Status != http.StatusAccepted {
		t.Fatalf("export = %d %s", res.Status, res.Body)
	}
	var created gen.AccountingSaftExportCreated
	res.JSON(&created)
	if created.Export.State != gen.Queued {
		t.Errorf("state = %s", created.Export.State)
	}
	if err := saftWorker(t, b.h).RunOnce(t.Context()); err != nil {
		t.Fatalf("worker: %v", err)
	}
	return b.exportDone(t, created.Export.Id)
}

// validate holds a file to the 1.40 XSD with xmllint when it is on the PATH.
func validate(t *testing.T, doc []byte) {
	t.Helper()
	ran, err := saft.Xmllint(doc, xsd140)
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Log("xmllint is not on the PATH: the file is not validated here; tools/saft/validate.sh does it in CI")
	}
}

func (b *books) exportDone(t *testing.T, id int64) (gen.AccountingSaftExport, [][]byte) {
	t.Helper()
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/saft-exports/%d", base, id), nil)
	var e gen.AccountingSaftExport
	res.JSON(&e)
	if e.State != gen.Done {
		t.Fatalf("export %d = %s %v", id, e.State, deref(e.Error))
	}
	var files [][]byte
	for _, f := range e.Files {
		dl := b.c.Do(http.MethodGet, fmt.Sprintf("%s/saft-exports/%d/files/%d", base, id, f.Part), nil)
		if dl.Status != http.StatusOK {
			t.Fatalf("download = %d %s", dl.Status, dl.Body)
		}
		if !strings.Contains(dl.Header("Content-Disposition"), f.FileName) || !strings.HasPrefix(f.FileName, "SAF-T Financial_923609016_") {
			t.Errorf("file name %q, disposition %q", f.FileName, dl.Header("Content-Disposition"))
		}
		if int64(len(dl.Body)) != f.SizeBytes {
			t.Errorf("size %d, recorded %d", len(dl.Body), f.SizeBytes)
		}
		validate(t, dl.Body)
		files = append(files, dl.Body)
	}
	return e, files
}

// The file's shape as the checks read it.
type xAmount struct {
	Amount, CurrencyCode, CurrencyAmount string
}
type xBalances struct {
	AccountID                                 string
	OpeningDebitBalance, OpeningCreditBalance string
	ClosingDebitBalance, ClosingCreditBalance string
}
type xFile struct {
	Header struct {
		SelectionCriteria struct{ SelectionStartDate, SelectionEndDate string }
	}
	Accounts []struct {
		AccountID, GroupingCategory, GroupingCode string
		xBalances
	} `xml:"MasterFiles>GeneralLedgerAccounts>Account"`
	Customers []struct {
		CustomerID, Name, RegistrationNumber string
		BalanceAccount                       []xBalances
	} `xml:"MasterFiles>Customers>Customer"`
	Suppliers []struct {
		SupplierID, Name, RegistrationNumber string
		BalanceAccount                       []xBalances
	} `xml:"MasterFiles>Suppliers>Supplier"`
	TaxCodes []struct{ TaxCode, StandardTaxCode, BaseRate string } `xml:"MasterFiles>TaxTable>TaxTableEntry>TaxCodeDetails"`
	Analysis []struct{ AnalysisType, AnalysisID string }           `xml:"MasterFiles>AnalysisTypeTable>AnalysisTypeTableEntry"`
	Entries  struct {
		NumberOfEntries         int64
		TotalDebit, TotalCredit string
		Journals                []struct {
			JournalID    string
			Transactions []struct {
				TransactionID      string
				Period, PeriodYear int
				TransactionDate    string
				SystemEntryDate    string
				Lines              []struct {
					AccountID, CustomerID, SupplierID string
					Debit                             *xAmount `xml:"DebitAmount"`
					Credit                            *xAmount `xml:"CreditAmount"`
					Analysis                          []struct{ AnalysisType, AnalysisID string }
					Tax                               []struct {
						TaxCode, TaxBase string
						Debit            *xAmount `xml:"DebitTaxAmount"`
						Credit           *xAmount `xml:"CreditTaxAmount"`
					} `xml:"TaxInformation"`
				} `xml:"Line"`
			} `xml:"Transaction"`
		} `xml:"Journal"`
	} `xml:"GeneralLedgerEntries"`
}

func parseFile(t *testing.T, doc []byte) xFile {
	t.Helper()
	var f xFile
	if err := xml.Unmarshal(doc, &f); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

// net is a debit and a credit balance as one signed amount.
func net(debit, credit string) *big.Rat {
	d, c := new(big.Rat), new(big.Rat)
	if debit != "" {
		d = rat(debit)
	}
	if credit != "" {
		c = rat(credit)
	}
	return d.Sub(d, c)
}

// report reads a report's table.
func (b *books) report(t *testing.T, kind, from, to string) gen.AccountingReport {
	t.Helper()
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/reports/%s?from=%s&to=%s", base, kind, from, to), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("report %s = %d %s", kind, res.Status, res.Body)
	}
	var r gen.AccountingReport
	res.JSON(&r)
	return r
}

// checkFile holds a file to the books beyond the XSD (A24): the totals to
// the lines, debit to credit, each account's closing balance to the trial
// balance, the customers' and suppliers' balances to their reports, and
// every account, party, tax code and project a line names to the master
// files.
func (b *books) checkFile(t *testing.T, doc []byte, from, to string) xFile {
	t.Helper()
	f := parseFile(t, doc)
	debit, credit, n := new(big.Rat), new(big.Rat), int64(0)
	accountIDs, customers, suppliers, taxCodes, projects := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range f.Accounts {
		accountIDs[a.AccountID] = true
	}
	for _, c := range f.Customers {
		customers[c.CustomerID] = true
	}
	for _, s := range f.Suppliers {
		suppliers[s.SupplierID] = true
	}
	for _, c := range f.TaxCodes {
		taxCodes[c.TaxCode] = true
		if c.StandardTaxCode != c.TaxCode {
			t.Errorf("tax code %s's standard code %s", c.TaxCode, c.StandardTaxCode)
		}
	}
	if len(f.TaxCodes) != len(b.codes) {
		t.Errorf("the tax table has %d codes, the books %d", len(f.TaxCodes), len(b.codes))
	}
	for _, a := range f.Analysis {
		projects[a.AnalysisType+"/"+a.AnalysisID] = true
	}
	for _, j := range f.Entries.Journals {
		for _, tx := range j.Transactions {
			n++
			for _, l := range tx.Lines {
				if (l.Debit == nil) == (l.Credit == nil) {
					t.Errorf("%s: a line with both or neither of DebitAmount and CreditAmount", tx.TransactionID)
				}
				if l.Debit != nil {
					debit.Add(debit, rat(l.Debit.Amount))
				}
				if l.Credit != nil {
					credit.Add(credit, rat(l.Credit.Amount))
				}
				if !accountIDs[l.AccountID] {
					t.Errorf("%s names account %s, not in MasterFiles", tx.TransactionID, l.AccountID)
				}
				if l.CustomerID != "" && !customers[l.CustomerID] {
					t.Errorf("%s names customer %s, not in MasterFiles", tx.TransactionID, l.CustomerID)
				}
				if l.SupplierID != "" && !suppliers[l.SupplierID] {
					t.Errorf("%s names supplier %s, not in MasterFiles", tx.TransactionID, l.SupplierID)
				}
				for _, x := range l.Tax {
					if !taxCodes[x.TaxCode] {
						t.Errorf("%s names tax code %s, not in the TaxTable", tx.TransactionID, x.TaxCode)
					}
				}
				for _, a := range l.Analysis {
					if !projects[a.AnalysisType+"/"+a.AnalysisID] {
						t.Errorf("%s names analysis %s/%s, not in the AnalysisTypeTable", tx.TransactionID, a.AnalysisType, a.AnalysisID)
					}
				}
			}
		}
	}
	if n != f.Entries.NumberOfEntries || debit.Cmp(rat(f.Entries.TotalDebit)) != 0 || credit.Cmp(rat(f.Entries.TotalCredit)) != 0 || debit.Cmp(credit) != 0 {
		t.Errorf("entries %d of %d, debit %s of %s, credit %s of %s", n, f.Entries.NumberOfEntries, debit.FloatString(2),
			f.Entries.TotalDebit, credit.FloatString(2), f.Entries.TotalCredit)
	}
	// The accounts to the trial balance; the earlier years' unclosed result,
	// a line of its own there, is on the year_result account in the file.
	tb := b.report(t, "trial_balance", from, to)
	want := map[string]*big.Rat{}
	earlier := new(big.Rat)
	for _, r := range tb.Rows {
		if r.Style != gen.Line {
			continue
		}
		if acc := r.Cells["account"]; acc != "" {
			want[acc] = rat(r.Cells["closing"])
		} else {
			earlier = rat(r.Cells["closing"])
		}
	}
	sum := new(big.Rat)
	for _, a := range f.Accounts {
		got := net(a.ClosingDebitBalance, a.ClosingCreditBalance)
		sum.Add(sum, got)
		w := want[a.AccountID]
		if w == nil {
			w = new(big.Rat)
		}
		if a.AccountID == "2050" {
			w = new(big.Rat).Add(w, earlier)
		}
		if got.Cmp(w) != 0 {
			t.Errorf("account %s closes at %s in the file, %s in the trial balance", a.AccountID, got.FloatString(2), w.FloatString(2))
		}
	}
	if sum.Sign() != 0 {
		t.Errorf("the accounts' closing balances sum to %s", sum.FloatString(2))
	}
	// The parties to their reports' balances.
	partyBalances := func(kind string) map[string]*big.Rat {
		out := map[string]*big.Rat{}
		party := ""
		for _, r := range b.report(t, kind, from, to).Rows {
			switch r.Style {
			case gen.Heading:
				// A customer's heading reads "<id> <name>".
				if fields := strings.Fields(r.Cells["party"]); len(fields) > 0 {
					party = fields[0]
				}
				if r.SupplierId != nil {
					party = fmt.Sprint(*r.SupplierId)
				}
			case gen.Subtotal:
				out[party] = rat(r.Cells["balance"])
			}
		}
		return out
	}
	recv := partyBalances("customers")
	for _, c := range f.Customers {
		got := new(big.Rat)
		for _, ba := range c.BalanceAccount {
			got.Add(got, net(ba.ClosingDebitBalance, ba.ClosingCreditBalance))
		}
		var id int64
		_, _ = fmt.Sscan(c.CustomerID, &id)
		if w := recv[fmt.Sprint(id-10000)]; w == nil || got.Cmp(w) != 0 {
			t.Errorf("customer %s closes at %s, the report says %v", c.CustomerID, got.FloatString(2), w)
		}
	}
	pay := partyBalances("suppliers")
	paySum, fileSum := new(big.Rat), new(big.Rat)
	for _, v := range pay {
		paySum.Add(paySum, v)
	}
	for _, s := range f.Suppliers {
		for _, ba := range s.BalanceAccount {
			fileSum.Add(fileSum, net(ba.ClosingDebitBalance, ba.ClosingCreditBalance))
		}
	}
	if paySum.Cmp(fileSum) != 0 {
		t.Errorf("suppliers close at %s in the file, %s in the report", fileSum.FloatString(2), paySum.FloatString(2))
	}
	return f
}

// TestSaftExport_TheLedgerGolden exports the fixed ledger, validates it,
// holds it to the books, and compares it with the golden file.
func TestSaftExport_TheLedgerGolden(t *testing.T) {
	b := saftBooks(t)
	b.ledger(t)
	check := b.c.Do(http.MethodGet, base+"/saft-exports/check?fiscalYear=2026", nil)
	var pre gen.AccountingSaftExportCheck
	check.JSON(&pre)
	if check.Status != http.StatusOK || pre.SellerMissing || pre.Vouchers == 0 || len(pre.UnmappedAccounts) != 0 {
		t.Fatalf("check = %d %+v", check.Status, pre)
	}
	e, files := b.export(t, map[string]any{"fiscalYear": 2026})
	if len(files) != 1 || e.Files[0].Part != 1 || e.Files[0].Parts != 1 || e.RequestedByName == nil || e.TotalBytes == nil {
		t.Fatalf("export = %+v", e)
	}
	f := b.checkFile(t, files[0], "2026-01-01", "2026-12-31")
	journals := map[string]int{}
	for _, j := range f.Entries.Journals {
		journals[j.JournalID] = len(j.Transactions)
	}
	if journals["SALES"] != 6 || journals["PURCHASES"] != 3 || journals["BANK"] != 3 || journals["GENERAL"] != 8 {
		t.Errorf("journals = %v", journals)
	}
	// TaxInformation as Skatteetaten's example file has it: on the base line,
	// its base and the voucher's VAT for the code; none on a VAT line, but for
	// a loss's VAT taken back with no base line beside it.
	var sale, euro, reverse, loss, lossAlone bool
	for _, j := range f.Entries.Journals {
		for _, tx := range j.Transactions {
			for _, l := range tx.Lines {
				if len(l.Tax) > 0 && (l.AccountID == "2700" || l.AccountID == "2710" || l.AccountID == "2705" || l.AccountID == "2715") {
					if l.AccountID == "2700" && l.Debit != nil && l.Debit.Amount == "100.00" && l.Tax[0].TaxBase == "400.00" && l.Tax[0].Debit != nil && l.Tax[0].Debit.Amount == "100.00" {
						lossAlone = true
					} else {
						t.Errorf("%s: a VAT line on %s carries TaxInformation %+v", tx.TransactionID, l.AccountID, l.Tax)
					}
				}
				for _, x := range l.Tax {
					switch {
					case l.AccountID == "3000" && x.TaxCode == "3" && x.TaxBase == "10000.00" && x.Credit != nil && x.Credit.Amount == "2500.00":
						sale = true
					case l.AccountID == "4005" && x.TaxCode == "1" && x.TaxBase == "9000.00" && x.Debit != nil && x.Debit.Amount == "2250.00":
						euro = true
					case l.AccountID == "6300" && x.TaxCode == "86" && x.TaxBase == "2000.00" && x.Credit != nil && x.Credit.Amount == "500.00":
						reverse = true
					case l.AccountID == "7830" && x.TaxCode == "3" && x.TaxBase == "2000.00" && x.Debit != nil && x.Debit.Amount == "500.00":
						loss = true
					}
				}
				if l.AccountID == "2400" && l.Credit != nil && l.Credit.Amount == "11250.00" && (l.Credit.CurrencyCode != "EUR" || l.Credit.CurrencyAmount != "1000.00") {
					t.Errorf("the euro payable = %+v", l.Credit)
				}
			}
		}
	}
	if !sale || !euro || !reverse || !loss || !lossAlone {
		t.Errorf("tax information: sale %v, euro %v, reverse charge %v, loss %v, a loss's VAT alone %v", sale, euro, reverse, loss, lossAlone)
	}
	// Every voucher was entered on the harness's day: no wall clock in the
	// file (the golden would move with the date the test runs on).
	for _, j := range f.Entries.Journals {
		for _, tx := range j.Transactions {
			if tx.SystemEntryDate != b.h.Now().Format(time.DateOnly) {
				t.Errorf("%s entered %s, the harness's day is %s", tx.TransactionID, tx.SystemEntryDate, b.h.Now().Format(time.DateOnly))
			}
		}
	}
	// BaseRate 0 for exactly the input codes whose VAT is a cost — no VAT
	// account to deduct on — as the reference names them.
	notDeductible := map[string]bool{"82": true, "84": true, "85": true, "87": true, "89": true, "92": true}
	for _, c := range f.TaxCodes {
		if want := map[bool]string{true: "0", false: "100"}[notDeductible[c.TaxCode]]; c.BaseRate != want {
			t.Errorf("code %s base rate %s, want %s", c.TaxCode, c.BaseRate, want)
		}
	}
	golden := filepath.Join("saft", "testdata", "golden", "2026-ledger.xml")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, files[0], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if !bytes.Equal(normalise(want), normalise(files[0])) {
		t.Errorf("the file differs from %s; run with -update and read the diff", golden)
	}
}

// normalise takes out what moves between runs: the software version.
func normalise(doc []byte) []byte {
	s := string(doc)
	if i := strings.Index(s, "<SoftwareVersion>"); i >= 0 {
		if j := strings.Index(s[i:], "</SoftwareVersion>"); j >= 0 {
			s = s[:i] + s[i+j:]
		}
	}
	return []byte(s)
}

// TestSaftExport_EmptyYearPeriodsAndRefusals: an empty year is a valid file;
// a period within the year opens at the balances before it; a missing
// seller, a year outside the books and periods out of order are refused; a
// reader may not export.
func TestSaftExport_EmptyYearPeriodsAndRefusals(t *testing.T) {
	b := saftBooks(t)
	_, files := b.export(t, map[string]any{"fiscalYear": 2026})
	f := b.checkFile(t, files[0], "2026-01-01", "2026-12-31")
	if f.Entries.NumberOfEntries != 0 || len(f.Accounts) != len(b.chart) {
		t.Errorf("empty year: %d entries, %d accounts of %d", f.Entries.NumberOfEntries, len(f.Accounts), len(b.chart))
	}
	b.ledger(t)
	_, files = b.export(t, map[string]any{"fiscalYear": 2026, "fromPeriod": 2, "toPeriod": 3})
	f = b.checkFile(t, files[0], "2026-02-01", "2026-03-31")
	if s := f.Header.SelectionCriteria; s.SelectionStartDate != "2026-02-01" || s.SelectionEndDate != "2026-03-31" {
		t.Errorf("selection = %+v", s)
	}
	tb := b.report(t, "trial_balance", "2026-02-01", "2026-03-31")
	opening := map[string]string{}
	for _, r := range tb.Rows {
		opening[r.Cells["account"]] = r.Cells["opening"]
	}
	for _, a := range f.Accounts {
		if want := opening[a.AccountID]; want != "" && net(a.OpeningDebitBalance, a.OpeningCreditBalance).Cmp(rat(want)) != 0 {
			t.Errorf("account %s opens at %s, the trial balance at %s", a.AccountID, net(a.OpeningDebitBalance, a.OpeningCreditBalance).FloatString(2), want)
		}
	}
	for _, j := range f.Entries.Journals {
		for _, tx := range j.Transactions {
			if tx.TransactionDate < "2026-02-01" || tx.TransactionDate > "2026-03-31" || tx.Period < 2 || tx.Period > 3 {
				t.Errorf("%s dated %s, period %d", tx.TransactionID, tx.TransactionDate, tx.Period)
			}
		}
	}

	expectProblem(t, b.c.Do(http.MethodPost, base+"/saft-exports", map[string]any{"fiscalYear": 2025}), http.StatusBadRequest, "period_invalid")
	expectProblem(t, b.c.Do(http.MethodPost, base+"/saft-exports", map[string]any{"fiscalYear": 2026, "fromPeriod": 5, "toPeriod": 4}), http.StatusBadRequest, "period_invalid")
	expectProblem(t, b.c.Do(http.MethodGet, base+"/saft-exports/check?fiscalYear=2030", nil), http.StatusBadRequest, "period_invalid")
	if res := b.h.SignIn(t, reader...).Do(http.MethodPost, base+"/saft-exports", map[string]any{"fiscalYear": 2026}); res.Status != http.StatusForbidden {
		t.Errorf("a reader's export = %d", res.Status)
	}
	if res := b.h.SignIn(t, closer...).Do(http.MethodGet, base+"/saft-exports", nil); res.Status != http.StatusForbidden {
		t.Errorf("a closer's list = %d", res.Status)
	}
	if res := b.h.SignIn(t, exporter...).Do(http.MethodGet, base+"/saft-exports", nil); res.Status != http.StatusOK {
		t.Errorf("an exporter's list = %d", res.Status)
	}
	if res := b.c.Do(http.MethodGet, base+"/saft-exports/999", nil); res.Status != http.StatusNotFound {
		t.Errorf("unknown export = %d", res.Status)
	}

	noSeller := newHarness(t)
	c := noSeller.SignIn(t, accountant...)
	setUp(t, c, "nb")
	expectProblem(t, c.Do(http.MethodPost, base+"/saft-exports", map[string]any{"fiscalYear": 2026}), http.StatusConflict, "saft_seller_missing")
}

// TestSaftExport_DeviatingFiscalYear: a fiscal year from July names its
// periods from July and its year as the one it begins in.
func TestSaftExport_DeviatingFiscalYear(t *testing.T) {
	h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: fjordSeller}))
	c := h.SignIn(t, accountant...)
	if res := c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2025-07-01", "fiscalYearStartMonth": 7,
		"vatTerm": "bimonthly", "language": "en"}); res.Status != http.StatusCreated {
		t.Fatalf("setup = %d %s", res.Status, res.Body)
	}
	b := &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
	mustPost(t, c, "2025-08-04", "August", line{"accountId": b.acc("6300"), "amount": "100.00"}, line{"accountId": b.acc("1920"), "amount": "-100.00"})
	mustPost(t, c, "2026-02-04", "February", line{"accountId": b.acc("6300"), "amount": "50.00"}, line{"accountId": b.acc("1920"), "amount": "-50.00"})
	_, files := b.export(t, map[string]any{"fiscalYear": 2025})
	f := b.checkFile(t, files[0], "2025-07-01", "2026-06-30")
	periods := map[string]int{}
	for _, j := range f.Entries.Journals {
		for _, tx := range j.Transactions {
			periods[tx.TransactionDate] = tx.Period
			if tx.PeriodYear != 2025 {
				t.Errorf("%s period year %d", tx.TransactionID, tx.PeriodYear)
			}
		}
	}
	if periods["2025-08-04"] != 2 || periods["2026-02-04"] != 8 {
		t.Errorf("periods = %v", periods)
	}
}

// TestSaftExport_SplitsByPeriod: past the split threshold the file is
// written as parts by period, each valid alone, together the whole.
func TestSaftExport_SplitsByPeriod(t *testing.T) {
	b := saftBooks(t, modtest.WithEnv("ACCOUNTING_SAFT_SPLIT_BYTES", "75000"))
	b.ledger(t)
	e, files := b.export(t, map[string]any{"fiscalYear": 2026, "toPeriod": 6})
	if len(files) < 2 {
		t.Fatalf("parts = %d, want a split", len(files))
	}
	next, total := 1, int64(0)
	for i, part := range e.Files {
		if part.Part != int32(i+1) || part.Parts != int32(len(files)) || part.FromPeriod != int32(next) ||
			!strings.HasSuffix(part.FileName, fmt.Sprintf("_%d_%d.xml", i+1, len(files))) {
			t.Errorf("part %d = %+v", i, part)
		}
		next = int(part.ToPeriod) + 1
		f := b.checkFile(t, files[i], part.From.String(), part.To.String())
		total += f.Entries.NumberOfEntries
		if len(files[i]) > 75000 {
			t.Errorf("part %d is %d bytes", i, len(files[i]))
		}
	}
	whole := b.report(t, "journal", "2026-01-01", "2026-06-30")
	vouchers := map[string]bool{}
	for _, r := range whole.Rows {
		if r.VoucherId != nil {
			vouchers[fmt.Sprint(*r.VoucherId)] = true
		}
	}
	if next != 7 || total != int64(len(vouchers)) {
		t.Errorf("parts cover periods to %d and %d vouchers, the journal %d", next-1, total, len(vouchers))
	}
}

// TestSaftExport_ACrashedWorkerIsTakenOver: an export whose worker stopped
// is taken up again once its lease runs out, and not before.
func TestSaftExport_ACrashedWorkerIsTakenOver(t *testing.T) {
	b := saftBooks(t)
	b.ledger(t)
	res := b.c.Do(http.MethodPost, base+"/saft-exports", map[string]any{"fiscalYear": 2026})
	var created gen.AccountingSaftExportCreated
	res.JSON(&created)
	id := created.Export.Id
	// A worker took it and stopped: running, its lease alive.
	b.h.Exec(t, `UPDATE accounting.saft_exports SET state = 'running', attempts = 1, lease_id = gen_random_uuid(),
		lease_until = now() + interval '1 hour' WHERE id = $1`, id)
	if err := saftWorker(t, b.h).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got gen.AccountingSaftExport
	b.c.Do(http.MethodGet, fmt.Sprintf("%s/saft-exports/%d", base, id), nil).JSON(&got)
	if got.State != gen.Running || len(got.Files) != 0 {
		t.Fatalf("a live lease was taken: %+v", got)
	}
	b.h.Exec(t, `UPDATE accounting.saft_exports SET lease_until = '2000-01-01' WHERE id = $1`, id)
	if err := saftWorker(t, b.h).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	e, files := b.exportDone(t, id)
	if e.Attempts != 2 || len(files) != 1 {
		t.Errorf("taken over: attempts %d, %d files", e.Attempts, len(files))
	}
	if n := b.h.Count(t, `SELECT count(*) FROM accounting.saft_exports`); n != 1 {
		t.Errorf("exports = %d", n)
	}
	if err := func() error {
		_, err := b.h.Pool().Exec(t.Context(), `DELETE FROM accounting.saft_exports WHERE id = $1`, id)
		return err
	}(); err == nil {
		t.Error("an export was deleted")
	}
}

// upload sends a file to the import.
func upload(c *modtest.Client, name string, data []byte) *modtest.Response {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", name)
	_, _ = part.Write(data)
	_ = w.Close()
	return c.Do(http.MethodPost, base+"/saft-imports", nil, modtest.RawBody(w.FormDataContentType(), body.Bytes()))
}

func uploadOK(t *testing.T, c *modtest.Client, name string, data []byte) gen.AccountingSaftImport {
	t.Helper()
	res := upload(c, name, data)
	if res.Status != http.StatusCreated {
		t.Fatalf("upload = %d %s", res.Status, res.Body)
	}
	var p gen.AccountingSaftImport
	res.JSON(&p)
	return p
}

// TestSaftImport_RoundTrip exports a ledger and imports the file into new
// books: the same chart with its groupings, the suppliers, the parties'
// balances and the trial balance — but for the documented regroupings: the
// VAT accounts' balances on the settlement account, the result on the
// opening account.
func TestSaftImport_RoundTrip(t *testing.T) {
	old := saftBooks(t)
	old.ledger(t)
	_, files := old.export(t, map[string]any{"fiscalYear": 2026})

	h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: fjordSeller}))
	c := h.SignIn(t, accountant...)
	preview := uploadOK(t, c, "fjord.xml", files[0])
	if !preview.CanApply || preview.AuditFileVersion != "1.40" || preview.BooksStart.String() != "2027-01-01" ||
		preview.Opening.ReceivablesDifference != "0.00" || preview.Opening.PayablesDifference != "0.00" {
		t.Fatalf("preview = %+v", preview)
	}
	if res := c.Do(http.MethodGet, fmt.Sprintf("%s/saft-imports/%d", base, preview.Id), nil); res.Status != http.StatusOK {
		t.Fatalf("get = %d", res.Status)
	}
	res := c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, preview.Id), map[string]any{"vatTerm": "bimonthly", "language": "nb",
		"fiscalYearStartMonth": 1, "booksStart": "2027-01-01"})
	if res.Status != http.StatusCreated {
		t.Fatalf("apply = %d %s", res.Status, res.Body)
	}
	var applied gen.AccountingSaftImport
	res.JSON(&applied)
	if applied.State != "applied" || applied.OpeningVoucherId == nil {
		t.Fatalf("applied = %+v", applied)
	}
	nb := &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
	for number, a := range old.chart {
		got, ok := nb.chart[number]
		if !ok || got.Name != a.Name || got.Kind != a.Kind {
			t.Errorf("account %s = %+v, was %+v", number, got, a)
			continue
		}
		if len(a.Groupings) > 0 && (len(got.Groupings) == 0 || got.Groupings[0].Code != a.Groupings[0].Code) {
			t.Errorf("account %s's grouping = %+v, was %+v", number, got.Groupings, a.Groupings)
		}
		if deref(a.SystemRole) != deref(got.SystemRole) {
			t.Errorf("account %s's role = %v, was %v", number, got.SystemRole, a.SystemRole)
		}
	}
	var sup gen.AccountingSupplierList
	c.Do(http.MethodGet, base+"/suppliers", nil).JSON(&sup)
	if len(sup.Items) != 1 || sup.Items[0].Name != "Verksted AS" || deref(sup.Items[0].OrganisationNumber) != "923456783" || deref(sup.Items[0].BankAccount) != "12345678903" {
		t.Errorf("suppliers = %+v", sup.Items)
	}
	// The trial balance: every balance-sheet account as it closed; the VAT
	// accounts' together on 2740; the result on 2060.
	before := map[string]*big.Rat{}
	for _, r := range old.report(t, "trial_balance", "2026-01-01", "2026-12-31").Rows {
		if r.Style == gen.Line && r.Cells["account"] != "" {
			before[r.Cells["account"]] = rat(r.Cells["closing"])
		}
	}
	after := map[string]*big.Rat{}
	for _, r := range nb.report(t, "trial_balance", "2027-01-01", "2027-01-01").Rows {
		if r.Style == gen.Line && r.Cells["account"] != "" {
			after[r.Cells["account"]] = rat(r.Cells["closing"])
		}
	}
	vat, result := new(big.Rat), new(big.Rat)
	vatAccounts := map[string]bool{"2700": true, "2701": true, "2702": true, "2703": true, "2705": true, "2710": true, "2711": true,
		"2712": true, "2713": true, "2714": true, "2715": true}
	for number, v := range before {
		kind := old.chart[number].Kind
		switch {
		case kind == "income" || kind == "expense":
			result.Add(result, v)
		case vatAccounts[number]:
			vat.Add(vat, v)
		case number == "2740" || number == "2060":
		default:
			if a := orZero(after[number]); a.Cmp(v) != 0 {
				t.Errorf("account %s closed at %s and opens at %v", number, v.FloatString(2), a)
			}
		}
	}
	wantVat := new(big.Rat).Add(vat, orZero(before["2740"]))
	if a := orZero(after["2740"]); a.Cmp(wantVat) != 0 || vat.Sign() == 0 {
		t.Errorf("2740 opens at %s, want the settlement's %s and the VAT accounts' %s", a.FloatString(2), orZero(before["2740"]).FloatString(2), vat.FloatString(2))
	}
	wantOpening := new(big.Rat).Add(result, orZero(before["2060"]))
	if a := orZero(after["2060"]); a.Cmp(wantOpening) != 0 {
		t.Errorf("2060 opens at %s, want %s", a.FloatString(2), wantOpening.FloatString(2))
	}
	for number := range vatAccounts {
		if a := after[number]; a != nil && a.Sign() != 0 {
			t.Errorf("VAT account %s opens at %s", number, a.FloatString(2))
		}
	}
	// The parties' balances, one open item each.
	var items gen.AccountingOpenItemList
	c.Do(http.MethodGet, base+"/open-items", nil).JSON(&items)
	balances := map[string]string{}
	for _, it := range items.Items {
		key := "s"
		if it.CustomerId != nil {
			key = fmt.Sprint("c", *it.CustomerId)
		}
		balances[key] = it.Balance
	}
	var oldItems gen.AccountingOpenItemList
	old.c.Do(http.MethodGet, base+"/open-items", nil).JSON(&oldItems)
	oldBalances := map[string]*big.Rat{}
	for _, it := range oldItems.Items {
		key := "s"
		if it.CustomerId != nil {
			key = fmt.Sprint("c", *it.CustomerId)
		}
		if oldBalances[key] == nil {
			oldBalances[key] = new(big.Rat)
		}
		oldBalances[key].Add(oldBalances[key], rat(it.Balance))
	}
	for key, v := range oldBalances {
		if balances[key] != v.FloatString(2) {
			t.Errorf("party %s's balance %s, was %s", key, balances[key], v.FloatString(2))
		}
	}
	// The import is the other way to set up: not twice, not beside setup.
	expectProblem(t, c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, preview.Id), map[string]any{"vatTerm": "bimonthly", "language": "nb"}),
		http.StatusConflict, "saft_import_applied")
	expectProblem(t, upload(c, "again.xml", files[0]), http.StatusConflict, "saft_import_ledger_has_vouchers")
	expectProblem(t, c.Do(http.MethodPost, base+"/setup", map[string]any{"booksStart": "2027-01-01", "vatTerm": "bimonthly", "language": "nb"}),
		http.StatusConflict, "books_already_set_up")
	var list gen.AccountingSaftImportList
	c.Do(http.MethodGet, base+"/saft-imports", nil).JSON(&list)
	if len(list.Items) != 1 || list.Items[0].Sha256 != preview.Sha256 {
		t.Errorf("imports = %+v", list.Items)
	}
	if dl := c.Do(http.MethodGet, fmt.Sprintf("%s/saft-imports/%d/file", base, preview.Id), nil); dl.Status != http.StatusOK || !bytes.Equal(dl.Body, files[0]) {
		t.Errorf("the kept file = %d, %d bytes", dl.Status, len(dl.Body))
	}
}

func orZero(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return r
}

// TestSaftImport_SkatteetatensExamples: Skatteetaten's own 1.30 and 1.40
// examples preview; their customer is not in Customers, which the preview
// says and Apply refuses until it is linked.
func TestSaftImport_SkatteetatensExamples(t *testing.T) {
	for _, name := range []string{"example-1.30.xml", "example-1.40.xml"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			c := h.SignIn(t, accountant...)
			data, err := os.ReadFile(filepath.Join("saft", "testdata", "examples", name))
			if err != nil {
				t.Fatal(err)
			}
			p := uploadOK(t, c, name, data)
			if len(p.Accounts) == 0 || len(p.Customers) == 0 || len(p.Roles) == 0 || len(p.TaxCodes) == 0 {
				t.Fatalf("preview = %+v", p)
			}
			unmatched := false
			for _, cu := range p.Customers {
				if cu.Match == "unmatched" {
					unmatched = true
				}
			}
			if !unmatched || p.CanApply || len(p.Blockers) == 0 {
				t.Fatalf("the unmatched customer: %+v, blockers %v", p.Customers, p.Blockers)
			}
			// The examples name their selection by periods: the month is asked.
			if !p.NeedsFiscalYearStartMonth {
				t.Error("the periods' month is not asked for")
			}
			apply := map[string]any{"vatTerm": "bimonthly", "language": "en"}
			res := c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, p.Id), apply)
			if res.Status != http.StatusBadRequest || !strings.Contains(string(res.Body), "fiscalYearStartMonth") {
				t.Fatalf("apply without the month = %d %s", res.Status, res.Body)
			}
			apply["fiscalYearStartMonth"] = 1
			expectProblem(t, c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, p.Id), apply), http.StatusConflict, "saft_import_unmatched_customers")
			for _, cu := range attention(t, c, p.Id) {
				link(t, c, p.Id, cu.Position, customerAcme)
			}
			res = c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, p.Id), apply)
			if res.Status != http.StatusCreated {
				t.Fatalf("apply with links = %d %s", res.Status, res.Body)
			}
		})
	}
}

// TestSaftImport_RefusesWhatItMust: XXE, an entity bomb, a file over the
// limit, a 1.20 file, a file that is not SAF-T, an import into books with
// vouchers, and a closer's permission.
func TestSaftImport_RefusesWhatItMust(t *testing.T) {
	h := newHarness(t, modtest.WithEnv("ACCOUNTING_SAFT_IMPORT_MAX_BYTES", "1048576"))
	c := h.SignIn(t, accountant...)
	root := `<AuditFile xmlns="urn:StandardAuditFile-Taxation-Financial:NO"><Header><AuditFileVersion>%s</AuditFileVersion></Header></AuditFile>`
	xxe := `<?xml version="1.0"?><!DOCTYPE AuditFile [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + fmt.Sprintf(root, "&x;")
	bomb := `<?xml version="1.0"?><!DOCTYPE l [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;"><!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;">]>` + fmt.Sprintf(root, "&c;")
	expectProblem(t, upload(c, "xxe.xml", []byte(xxe)), http.StatusBadRequest, "saft_file_invalid")
	expectProblem(t, upload(c, "bomb.xml", []byte(bomb)), http.StatusBadRequest, "saft_file_invalid")
	expectProblem(t, upload(c, "zip.xml", []byte("PK\x03\x04")), http.StatusBadRequest, "saft_file_invalid")
	expectProblem(t, upload(c, "old.xml", []byte(fmt.Sprintf(root, "1.20"))), http.StatusBadRequest, "saft_version_unsupported")
	big := bytes.Repeat([]byte(" "), 1100*1024)
	expectProblem(t, upload(c, "big.xml", append([]byte(fmt.Sprintf(root, "1.40")), big...)), http.StatusBadRequest, "saft_file_too_large")
	if res := upload(h.SignIn(t, poster...), "x.xml", []byte(fmt.Sprintf(root, "1.40"))); res.Status != http.StatusForbidden {
		t.Errorf("a poster's upload = %d", res.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM accounting.saft_imports`); n != 0 {
		t.Errorf("imports kept = %d", n)
	}

	b := newBooks(t)
	mustPost(t, b.c, "2026-01-05", "Something", line{"accountId": b.acc("6300"), "amount": "1.00"}, line{"accountId": b.acc("1920"), "amount": "-1.00"})
	data, _ := os.ReadFile(filepath.Join("saft", "testdata", "examples", "example-1.40.xml"))
	expectProblem(t, upload(b.c, "x.xml", data), http.StatusConflict, "saft_import_ledger_has_vouchers")
}

// TestSaftImport_PartiesThatDoNotAddUp: a file whose customers' balances
// differ from the receivables account's is refused with the difference, and
// an account whose number does not fit is listed with its fix.
func TestSaftImport_PartiesThatDoNotAddUp(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	data, _ := os.ReadFile(filepath.Join("saft", "testdata", "examples", "example-1.40.xml"))
	doc := strings.Replace(string(data), "<ClosingDebitBalance>12500.00</ClosingDebitBalance>\n                </BalanceAccount>",
		"<ClosingDebitBalance>12000.00</ClosingDebitBalance>\n                </BalanceAccount>", 1)
	doc = strings.Replace(doc, "<AccountID>4000</AccountID>\n                <AccountDescription>", "<AccountID>40-00.A</AccountID>\n                <AccountDescription>", 1)
	p := uploadOK(t, c, "odd.xml", []byte(doc))
	if p.Opening.ReceivablesDifference != "-500.00" {
		t.Errorf("receivables difference = %s", p.Opening.ReceivablesDifference)
	}
	found := false
	for _, a := range p.Accounts {
		if a.FileAccountId == "40-00.A" && a.Number != nil && *a.Number == "4000" && a.Problem != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("accounts = %+v", p.Accounts)
	}
	for _, cu := range attention(t, c, p.Id) {
		link(t, c, p.Id, cu.Position, customerAcme)
	}
	expectProblem(t, c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, p.Id), map[string]any{"vatTerm": "none", "language": "en",
		"fiscalYearStartMonth": 1}), http.StatusConflict, "saft_import_parties_mismatch")
	expectProblem(t, c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, p.Id), map[string]any{"vatTerm": "none", "language": "en",
		"fiscalYearStartMonth": 1, "booksStart": "2026-02-01"}), http.StatusBadRequest, "saft_import_books_start_mismatch")
}

// TestSaftExport_TaxInformationReproducesTheVatSpecification: summed per
// code over the file, the bases and the tax amounts of TaxInformation are the
// VAT specification of the same period (H2) — each base once, each VAT once.
func TestSaftExport_TaxInformationReproducesTheVatSpecification(t *testing.T) {
	b := saftBooks(t)
	b.ledger(t)
	_, files := b.export(t, map[string]any{"fiscalYear": 2026})
	f := parseFile(t, files[0])
	bases, vat := map[string]*big.Rat{}, map[string]*big.Rat{}
	add := func(m map[string]*big.Rat, code string, r *big.Rat) {
		if m[code] == nil {
			m[code] = new(big.Rat)
		}
		m[code].Add(m[code], r)
	}
	for _, j := range f.Entries.Journals {
		for _, tx := range j.Transactions {
			for _, l := range tx.Lines {
				for _, x := range l.Tax {
					// A VAT line keeps TaxInformation only with no base
					// line beside it, its base the one it was computed
					// from: not a base the voucher posts.
					if !strings.HasPrefix(l.AccountID, "27") {
						tb := rat(x.TaxBase)
						if l.Credit != nil {
							tb.Neg(tb)
						}
						add(bases, x.TaxCode, tb)
					}
					if x.Debit != nil {
						add(vat, x.TaxCode, rat(x.Debit.Amount))
					} else {
						add(vat, x.TaxCode, new(big.Rat).Neg(rat(x.Credit.Amount)))
					}
				}
			}
		}
	}
	res := b.c.Do(http.MethodGet, base+"/vat/specification?from=2026-01-01&to=2026-12-31", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("specification = %d %s", res.Status, res.Body)
	}
	var spec gen.AccountingVatSpecification
	res.JSON(&spec)
	wantBase, wantVat := map[string]*big.Rat{}, map[string]*big.Rat{}
	for _, c := range append(spec.Codes, spec.NotReported...) {
		// The specification's signs are the return's: a base positive for
		// sales and purchases, VAT owed positive, deducted negative. In the
		// ledger's: an output code's base is a credit; the VAT is what the
		// tax postings carry — for a reverse charge its calculated side once.
		bse := rat(c.Base)
		if (c.Direction == gen.AccountingTaxDirectionOutput || c.Code == "7") && (c.Specification == nil || *c.Specification != "tapPåKrav") {
			bse.Neg(bse)
		}
		add(wantBase, c.Code, bse)
		v := new(big.Rat)
		if c.ReverseCharge {
			v.Neg(rat(c.CalculatedVat))
		} else {
			v.Neg(rat(c.OutputVat))
			v.Sub(v, rat(c.DeductedVat))
			v.Sub(v, rat(c.LossVat))
			v.Add(v, rat(c.NotDeductedVat))
		}
		add(wantVat, c.Code, v)
	}
	for code, w := range wantBase {
		if got := orZero(bases[code]); got.Cmp(w) != 0 {
			t.Errorf("code %s: TaxBase sums to %s, the specification's base %s", code, got.FloatString(2), w.FloatString(2))
		}
		if got := orZero(vat[code]); got.Cmp(wantVat[code]) != 0 {
			t.Errorf("code %s: the tax amounts sum to %s, the specification's VAT %s", code, got.FloatString(2), wantVat[code].FloatString(2))
		}
	}
	for code := range bases {
		if wantBase[code] == nil {
			t.Errorf("code %s is in the file's TaxInformation and not in the specification", code)
		}
	}
}

// TestSaftExport_BoundWaitsForAPostInFlight: the read bound is taken under
// the settings row FOR UPDATE, so a voucher being posted when the run starts
// is in the file whole, or the run waits for it (M1).
func TestSaftExport_BoundWaitsForAPostInFlight(t *testing.T) {
	b := saftBooks(t)
	b.ledger(t)
	res := b.c.Do(http.MethodPost, base+"/saft-exports", map[string]any{"fiscalYear": 2026})
	var created gen.AccountingSaftExportCreated
	res.JSON(&created)
	tx, err := b.h.Pool().Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	d, _ := time.Parse(time.DateOnly, "2026-03-20")
	if _, err := accounting.Post(t.Context(), tx, accounting.Voucher{Kind: "manual", Date: d, Description: "In flight", At: b.h.Now(),
		Lines: []accounting.Line{{AccountID: b.acc("6300"), Amount: rat("77")}, {AccountID: b.acc("1920"), Amount: rat("-77")}}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- saftWorker(t, b.h).RunOnce(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("the worker did not wait for the post in flight: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, files := b.exportDone(t, created.Export.Id)
	if !bytes.Contains(files[0], []byte("<Description>In flight</Description>")) {
		t.Error("the voucher in flight is not in the file")
	}
	b.checkFile(t, files[0], "2026-01-01", "2026-12-31")
}

// TestSaftExport_GivesUpAfterFiveAttempts: an export whose runs keep
// stopping is failed with its reason rather than taken up for ever (M2).
func TestSaftExport_GivesUpAfterFiveAttempts(t *testing.T) {
	b := saftBooks(t)
	res := b.c.Do(http.MethodPost, base+"/saft-exports", map[string]any{"fiscalYear": 2026})
	var created gen.AccountingSaftExportCreated
	res.JSON(&created)
	b.h.Exec(t, `UPDATE accounting.saft_exports SET state = 'running', attempts = 5, lease_id = gen_random_uuid(),
		lease_until = '2000-01-01' WHERE id = $1`, created.Export.Id)
	if err := saftWorker(t, b.h).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got gen.AccountingSaftExport
	b.c.Do(http.MethodGet, fmt.Sprintf("%s/saft-exports/%d", base, created.Export.Id), nil).JSON(&got)
	if got.State != gen.Failed || got.Error == nil || !strings.Contains(*got.Error, "5 attempts") {
		t.Errorf("export = %s %v", got.State, deref(got.Error))
	}
}

// attention is the import's customers with a balance that are not matched
// or linked.
func attention(t *testing.T, c *modtest.Client, id int64) []gen.AccountingSaftImportCustomer {
	t.Helper()
	res := c.Do(http.MethodGet, fmt.Sprintf("%s/saft-imports/%d/customers?attention=true", base, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("customers = %d %s", res.Status, res.Body)
	}
	var list gen.AccountingSaftImportCustomerList
	res.JSON(&list)
	return list.Items
}

// link links a file customer to a customer in Customers.
func link(t *testing.T, c *modtest.Client, id int64, position, customer int32) gen.AccountingSaftImportCustomer {
	t.Helper()
	res := c.Do(http.MethodPut, fmt.Sprintf("%s/saft-imports/%d/customers/%d/link", base, id, position), map[string]any{"customerId": customer})
	if res.Status != http.StatusOK {
		t.Fatalf("link = %d %s", res.Status, res.Body)
	}
	var cu gen.AccountingSaftImportCustomer
	res.JSON(&cu)
	return cu
}

// TestSaftImport_LinksAreCustomersPicked (H3): a link names a customer of
// Customers by its id, as the picker found it: an id Customers does not hold
// is refused, a customer merged away links to the one it went into, and the
// preview shows the linked customer's name and organisation number.
func TestSaftImport_LinksAreCustomersPicked(t *testing.T) {
	h := newHarness(t, modtest.WithDirectory(fakeDirectory{
		names:  map[int32]string{customerAcme: "Acme AS", customerBeta: "Beta AS"},
		legal:  map[int32]string{customerAcme: "914778271", customerBeta: "987654325"},
		merged: map[int32]int32{customerBeta: customerAcme}}))
	c := h.SignIn(t, accountant...)
	data, _ := os.ReadFile(filepath.Join("saft", "testdata", "examples", "example-1.40.xml"))
	p := uploadOK(t, c, "x.xml", data)
	need := attention(t, c, p.Id)
	if len(need) != 1 {
		t.Fatalf("attention = %+v", need)
	}
	expectProblem(t, c.Do(http.MethodPut, fmt.Sprintf("%s/saft-imports/%d/customers/%d/link", base, p.Id, need[0].Position),
		map[string]any{"customerId": 10501}), http.StatusBadRequest, "saft_import_link_unknown")
	got := link(t, c, p.Id, need[0].Position, customerBeta)
	if got.Match != "linked" || deref(got.CustomerId) != customerAcme || deref(got.CustomerName) != "Acme AS" ||
		deref(got.CustomerOrganisationNumber) != "914778271" {
		t.Errorf("linked = %+v", got)
	}
	var again gen.AccountingSaftImport
	c.Do(http.MethodGet, fmt.Sprintf("%s/saft-imports/%d", base, p.Id), nil).JSON(&again)
	if again.Customers[0].Match != "linked" || deref(again.Customers[0].CustomerOrganisationNumber) != "914778271" || len(attention(t, c, p.Id)) != 0 {
		t.Errorf("preview after the link = %+v", again.Customers)
	}
}

// TestSaftImport_MatchedOnceAndOnAsk (M6): the customers are matched at the
// upload and when a person asks — not on every read — and the lists are
// paged.
func TestSaftImport_MatchedOnceAndOnAsk(t *testing.T) {
	calls := 0
	h := newHarness(t, modtest.WithDirectory(fakeDirectory{names: map[int32]string{customerAcme: "Acme AS"},
		legal: map[int32]string{customerAcme: "914778271"}, byLegalID: &calls}))
	c := h.SignIn(t, accountant...)
	data, _ := os.ReadFile(filepath.Join("saft", "testdata", "examples", "example-1.40.xml"))
	// Skatteetaten's example customer's number does not pass mod 11: one
	// that does, Acme's.
	data = []byte(strings.Replace(string(data), "<RegistrationNumber>922222229</RegistrationNumber>", "<RegistrationNumber>914778271</RegistrationNumber>", 1))
	p := uploadOK(t, c, "x.xml", data)
	if calls != 1 || p.Customers[0].Match != "matched" || p.MatchedAt == nil || p.CustomersTotal != 1 {
		t.Fatalf("after the upload: %d calls, %+v", calls, p.Customers)
	}
	for range 3 {
		c.Do(http.MethodGet, fmt.Sprintf("%s/saft-imports/%d", base, p.Id), nil)
	}
	if calls != 1 {
		t.Errorf("reads matched again: %d calls", calls)
	}
	if res := c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/match", base, p.Id), nil); res.Status != http.StatusOK || calls != 2 {
		t.Errorf("match = %d, %d calls", res.Status, calls)
	}
	var sup gen.AccountingSaftImportSupplierList
	c.Do(http.MethodGet, fmt.Sprintf("%s/saft-imports/%d/suppliers?offset=0&limit=1", base, p.Id), nil).JSON(&sup)
	if sup.Total != 1 || len(sup.Items) != 1 {
		t.Errorf("suppliers = %+v", sup)
	}
}

// TestSaftImport_RefusesTooMuchAndWhatIsNotAnAmount (M6, M7): a file with
// more accounts than a company has is refused, and so is a balance that is
// not the XSD's monetary type, named by its element.
func TestSaftImport_RefusesTooMuchAndWhatIsNotAnAmount(t *testing.T) {
	h := newHarness(t)
	c := h.SignIn(t, accountant...)
	data, _ := os.ReadFile(filepath.Join("saft", "testdata", "examples", "example-1.40.xml"))
	comma := strings.Replace(string(data), "<ClosingDebitBalance>12500.00</ClosingDebitBalance>", "<ClosingDebitBalance>12500,00</ClosingDebitBalance>", 1)
	res := upload(c, "comma.xml", []byte(comma))
	expectProblem(t, res, http.StatusBadRequest, "saft_file_invalid")
	if !strings.Contains(string(res.Body), "Account[1500]/Closing") {
		t.Errorf("the refusal does not name the element: %s", res.Body)
	}
	var many strings.Builder
	i := strings.Index(string(data), "<Account>")
	j := strings.Index(string(data), "</GeneralLedgerAccounts>")
	many.WriteString(string(data)[:i])
	for n := range 20001 {
		fmt.Fprintf(&many, "<Account><AccountID>%d</AccountID><AccountDescription>A</AccountDescription><GroupingCategory>NA</GroupingCategory><GroupingCode>NA</GroupingCode><AccountType>GL</AccountType><OpeningDebitBalance>0</OpeningDebitBalance><ClosingDebitBalance>0</ClosingDebitBalance></Account>", 100000+n)
	}
	many.WriteString(string(data)[j:])
	expectProblem(t, upload(c, "many.xml", []byte(many.String())), http.StatusBadRequest, "saft_file_too_large")
}

// TestSaftImport_AnotherCompanysFile (L2): a file of another company than
// the seller in Invoices needs a person's confirmation.
func TestSaftImport_AnotherCompanysFile(t *testing.T) {
	h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: fjordSeller}))
	c := h.SignIn(t, accountant...)
	data, _ := os.ReadFile(filepath.Join("saft", "testdata", "examples", "example-1.40.xml"))
	p := uploadOK(t, c, "x.xml", data)
	if !p.CompanyMismatch || deref(p.SellerOrganisationNumber) != "923609016" {
		t.Fatalf("preview = %v %v", p.CompanyMismatch, p.SellerOrganisationNumber)
	}
	for _, cu := range attention(t, c, p.Id) {
		link(t, c, p.Id, cu.Position, customerAcme)
	}
	body := map[string]any{"vatTerm": "bimonthly", "language": "nb", "fiscalYearStartMonth": 1}
	expectProblem(t, c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, p.Id), body), http.StatusConflict, "saft_import_other_company")
	body["confirmOtherCompany"] = true
	if res := c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, p.Id), body); res.Status != http.StatusCreated {
		t.Fatalf("confirmed = %d %s", res.Status, res.Body)
	}
}

// TestSaftImport_MidYear (H4): a file of January to June sets up books from
// 1 July that keep the year's result where it was: each income and expense
// account opens with its year-to-date balance, and nothing goes to equity.
func TestSaftImport_MidYear(t *testing.T) {
	old := saftBooks(t)
	old.ledger(t)
	_, files := old.export(t, map[string]any{"fiscalYear": 2026, "toPeriod": 6})
	h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: fjordSeller}))
	c := h.SignIn(t, accountant...)
	p := uploadOK(t, c, "h1.xml", files[0])
	if !p.MidYear || p.BooksStart.String() != "2026-07-01" || p.FiscalYearStartMonth != 1 || p.Opening.Result != "0.00" {
		t.Fatalf("preview = midYear %v, %s, month %d, result %s", p.MidYear, p.BooksStart, p.FiscalYearStartMonth, p.Opening.Result)
	}
	if res := c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, p.Id), map[string]any{"vatTerm": "bimonthly", "language": "nb"}); res.Status != http.StatusCreated {
		t.Fatalf("apply = %d %s", res.Status, res.Body)
	}
	nb := &books{h: h, c: c}
	result := func(b *books, from, to string) map[string]string {
		out := map[string]string{}
		for _, r := range b.report(t, "trial_balance", from, to).Rows {
			if acc := r.Cells["account"]; acc != "" && (acc[0] >= '3') {
				out[acc] = r.Cells["closing"]
			}
		}
		return out
	}
	was, is := result(old, "2026-01-01", "2026-06-30"), result(nb, "2026-07-01", "2026-07-01")
	for acc, v := range was {
		if v != "0.00" && is[acc] != v {
			t.Errorf("account %s: the year so far %s in the file's books, %s after the import", acc, v, is[acc])
		}
	}
	if len(was) == 0 {
		t.Fatal("no income or expense in the half year")
	}
}

// TestSaftExport_FilesRemovedAfterTheirRetention (M5): an export's files
// carry personal data and are deleted once the retention has passed; the
// record of the export and its hash stay, and the download says so.
func TestSaftExport_FilesRemovedAfterTheirRetention(t *testing.T) {
	b := saftBooks(t, modtest.WithEnv("ACCOUNTING_SAFT_EXPORT_RETENTION_DAYS", "30"))
	b.ledger(t)
	e, _ := b.export(t, map[string]any{"fiscalYear": 2026})
	key := ""
	for k := range b.h.objects.data {
		if strings.HasPrefix(k, "accounting/saft-exports/") || strings.Contains(k, "saft-exports/") {
			key = k
		}
	}
	if key == "" {
		t.Fatal("no object written")
	}
	b.h.Advance(29 * 24 * time.Hour)
	if err := saftWorker(t, b.h).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.h.objects.data[key]; !ok {
		t.Fatal("removed before its retention")
	}
	b.h.Advance(2 * 24 * time.Hour)
	if err := saftWorker(t, b.h).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.h.objects.data[key]; ok {
		t.Error("the object is still there")
	}
	c := b.h.SignIn(t, accountant...)
	var got gen.AccountingSaftExport
	c.Do(http.MethodGet, fmt.Sprintf("%s/saft-exports/%d", base, e.Id), nil).JSON(&got)
	if got.Files[0].RemovedAt == nil || got.Files[0].Sha256 != e.Files[0].Sha256 {
		t.Errorf("file = %+v", got.Files[0])
	}
	expectProblem(t, c.Do(http.MethodGet, fmt.Sprintf("%s/saft-exports/%d/files/1", base, e.Id), nil), http.StatusConflict, "saft_export_file_removed")
	if _, err := b.h.Pool().Exec(t.Context(), `UPDATE accounting.saft_export_files SET sha256 = 'x' WHERE export_id = $1`, e.Id); err == nil {
		t.Error("a file's row was changed")
	}
}
