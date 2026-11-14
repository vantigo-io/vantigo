package accounting_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Delivery F, the bank (A26–A29, R5, R11): bank accounts, statements from
// camt.053, CSV and the feed, and reconciliation with each suggestion kind,
// to the øre.

// The company's accounts in the fixtures.
const (
	operating = "15030012345"
	savings   = "12060543214"
	cardAcct  = "97100000001"
)

// fixture is a statement file of bankstmt's testdata.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("bankstmt/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// bankAccount adds a bank account and answers it.
func bankAccount(t *testing.T, b *books, name, number, ledger string) gen.AccountingBankAccount {
	t.Helper()
	res := b.c.Do(http.MethodPost, base+"/bank/accounts", map[string]any{"name": name, "accountNumber": number,
		"ledgerAccountId": b.acc(ledger)})
	if res.Status != http.StatusCreated {
		t.Fatalf("create bank account %s = %d %s", number, res.Status, res.Body)
	}
	var out gen.AccountingBankAccount
	res.JSON(&out)
	return out
}

// statementBody is a multipart body with the file, and a mapping when given.
func statementBody(name string, data []byte, mapping any) (string, []byte) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", name)
	_, _ = part.Write(data)
	if mapping != nil {
		raw, _ := json.Marshal(mapping)
		_ = w.WriteField("mapping", string(raw))
	}
	_ = w.Close()
	return w.FormDataContentType(), body.Bytes()
}

// importFile imports a statement file, a CSV one for account.
func importFile(c *modtest.Client, name string, data []byte, account int64) *modtest.Response {
	ct, body := statementBody(name, data, nil)
	path := base + "/bank/imports"
	if account != 0 {
		path += fmt.Sprintf("?bankAccountId=%d", account)
	}
	return c.Do(http.MethodPost, path, nil, modtest.RawBody(ct, body))
}

// imported imports a file that must import.
func imported(t *testing.T, b *books, name string, data []byte, account int64) gen.AccountingBankImportResult {
	t.Helper()
	res := importFile(b.c, name, data, account)
	if res.Status != http.StatusCreated {
		t.Fatalf("import %s = %d %s", name, res.Status, res.Body)
	}
	var out gen.AccountingBankImportResult
	res.JSON(&out)
	return out
}

// reconcile is a bank account's reconciliation of September.
func reconcile(t *testing.T, b *books, id int64) gen.AccountingBankReconciliation {
	t.Helper()
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d/reconciliation?from=2026-09-01&to=2026-09-30", base, id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("reconcile %d = %d %s", id, res.Status, res.Body)
	}
	var out gen.AccountingBankReconciliation
	res.JSON(&out)
	return out
}

// lineOf is the reconciliation's line of that amount and day.
func lineOf(t *testing.T, r gen.AccountingBankReconciliation, day, amount string) gen.AccountingBankLine {
	t.Helper()
	for _, l := range r.Lines {
		if l.BookedOn.String() == day && l.Amount == amount {
			return l
		}
	}
	t.Fatalf("no line of %s on %s in %+v", amount, day, r.Lines)
	return gen.AccountingBankLine{}
}

// first is a line's first suggestion, which must be of kind.
func first(t *testing.T, l gen.AccountingBankLine, kind string) gen.AccountingBankSuggestion {
	t.Helper()
	if len(l.Suggestions) == 0 || l.Suggestions[0].Kind != kind {
		t.Fatalf("line %s %s: suggestions %+v, want %s first", l.BookedOn, l.Amount, l.Suggestions, kind)
	}
	return l.Suggestions[0]
}

// acceptOn accepts a suggestion.
func acceptOn(c *modtest.Client, line int64, key string) *modtest.Response {
	return c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/accept", base, line), map[string]any{"key": key})
}

// accepted accepts a suggestion that must be accepted and answers the line.
func accepted(t *testing.T, b *books, line int64, key string) gen.AccountingBankLine {
	t.Helper()
	res := acceptOn(b.c, line, key)
	if res.Status != http.StatusOK {
		t.Fatalf("accept %s on %d = %d %s", key, line, res.Status, res.Body)
	}
	var out gen.AccountingBankLine
	res.JSON(&out)
	if out.State != "matched" || out.Match == nil {
		t.Fatalf("accepted line = %+v", out)
	}
	return out
}

// undone undoes a match that must undo.
func undone(t *testing.T, b *books, line int64) gen.AccountingBankLine {
	t.Helper()
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/undo", base, line), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("undo %d = %d %s", line, res.Status, res.Body)
	}
	var out gen.AccountingBankLine
	res.JSON(&out)
	if out.State != "open" || out.Match != nil {
		t.Fatalf("undone line = %+v", out)
	}
	return out
}

// matchedVoucher is the voucher a line's match posted, through the API.
func matchedVoucher(t *testing.T, b *books, l gen.AccountingBankLine) gen.AccountingVoucher {
	t.Helper()
	if l.Match == nil || l.Match.VoucherId == nil {
		t.Fatalf("line %d's match posted nothing: %+v", l.Id, l.Match)
	}
	return voucherByID(t, b, *l.Match.VoucherId)
}

// TestBankAccounts: the seller's account offered first on the bank role's
// account; the number held to mod 11, NOK only, the ledger an asset account
// one bank account holds; a change under its revision; a delete refused
// once statements exist.
func TestBankAccounts(t *testing.T) {
	h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: contracts.SellerProfile{LegalName: "Fjord Bygg AS",
		BankAccount: "1503.00.12345", IBAN: "NO4615030012345", BIC: "DNBANOKK"}}))
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	b := &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
	res := c.Do(http.MethodGet, base+"/bank/accounts", nil)
	var list gen.AccountingBankAccountList
	res.JSON(&list)
	if list.Suggested == nil || list.Suggested.AccountNumber != operating || deref(list.Suggested.LedgerAccountId) != b.acc("1920") ||
		list.Feed.Enabled || len(list.Items) != 0 {
		t.Fatalf("list = %+v", list)
	}
	bad := map[string]map[string]any{
		"bank_account_number_invalid": {"name": "X", "accountNumber": "15030012346", "ledgerAccountId": b.acc("1920")},
		"bank_account_currency":       {"name": "X", "accountNumber": operating, "currency": "EUR", "ledgerAccountId": b.acc("1920")},
		"bank_account_invalid":        {"name": "X", "accountNumber": operating, "ledgerAccountId": b.acc("3000")},
	}
	for code, body := range bad {
		expectProblem(t, c.Do(http.MethodPost, base+"/bank/accounts", body), http.StatusBadRequest, code)
	}
	op := bankAccount(t, b, "Driftskonto", "NO46 1503 0012 345", "1920")
	if op.AccountNumber != operating || op.Source != "file" || op.LedgerAccountNumber != "1920" || !op.Active {
		t.Fatalf("created = %+v", op)
	}
	expectProblem(t, c.Do(http.MethodPost, base+"/bank/accounts", map[string]any{"name": "Again", "accountNumber": operating,
		"ledgerAccountId": b.acc("1950")}), http.StatusConflict, "bank_account_number_taken")
	expectProblem(t, c.Do(http.MethodPost, base+"/bank/accounts", map[string]any{"name": "Sparing", "accountNumber": savings,
		"ledgerAccountId": b.acc("1920")}), http.StatusConflict, "bank_account_ledger_taken")
	res = c.Do(http.MethodGet, base+"/bank/accounts", nil)
	res.JSON(&list)
	if list.Suggested != nil || len(list.Items) != 1 {
		t.Fatalf("the seller's account is offered while a bank account has it: %+v", list)
	}
	res = c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), map[string]any{"name": "Drift", "accountNumber": operating,
		"ledgerAccountId": b.acc("1920"), "iban": "NO4615030012345", "bic": "DNBANOKK", "revision": op.Revision})
	if res.Status != http.StatusOK {
		t.Fatalf("update = %d %s", res.Status, res.Body)
	}
	expectProblem(t, c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), map[string]any{"name": "Drift",
		"accountNumber": operating, "ledgerAccountId": b.acc("1920"), "revision": op.Revision}), http.StatusConflict, "revision_conflict")
	res = c.Do(http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), nil)
	var got gen.AccountingBankAccount
	res.JSON(&got)
	if got.Name != "Drift" || deref(got.Iban) != "NO4615030012345" || deref(got.Bic) != "DNBANOKK" {
		t.Fatalf("read = %+v", got)
	}
	sp := bankAccount(t, b, "Sparing", savings, "1950")
	if res := c.Do(http.MethodDelete, fmt.Sprintf("%s/bank/accounts/%d", base, sp.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Status, res.Body)
	}
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	expectProblem(t, c.Do(http.MethodDelete, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), nil), http.StatusConflict,
		"bank_account_has_statements")
}

// TestBankImport_Camt053 imports two banks' shapes: the file's statements
// routed by account, an account not here named and left out, booked entries
// only, the batch kept, the reversal marked; the file again adds nothing;
// the next statement follows on from the first's balances, a gap is
// flagged; two identical lines of one day both stay; the file is kept.
func TestBankImport_Camt053(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	sp := bankAccount(t, b, "Sparing", savings, "1950")
	out := imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	if len(out.Statements) != 2 || !slices.Equal(out.UnknownAccounts, []string{"30001234567"}) || out.NotBooked != 1 {
		t.Fatalf("import = %+v", out)
	}
	if s := out.Statements[0]; s.BankAccountId != op.Id || s.Imported != 5 || s.Skipped != 0 || s.Continuity != "ok" || s.Source != "camt053" {
		t.Fatalf("operating = %+v", s)
	}
	if s := out.Statements[1]; s.BankAccountId != sp.Id || s.Imported != 1 {
		t.Fatalf("savings = %+v", s)
	}
	again := imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	if !again.Statements[0].Repeated || again.Statements[0].Skipped != 5 || again.Statements[0].Imported != 0 {
		t.Fatalf("again = %+v", again)
	}
	// The same lines in another file are skipped by the bank's ids.
	renamed := bytes.ReplaceAll(fixture(t, "dnb-camt053-001-02.xml"), []byte("DNB-STMT-20260907-0001"), []byte("DNB-STMT-COPY"))
	renamed = bytes.ReplaceAll(renamed, []byte("15030012345-2026-09-07"), []byte("copy"))
	copied := imported(t, b, "copy.xml", renamed, 0)
	if copied.Statements[0].Repeated || copied.Statements[0].Skipped != 5 || copied.Statements[0].Imported != 0 ||
		copied.Statements[0].Continuity != "overlap" {
		t.Fatalf("copy = %+v", copied)
	}
	sb1 := imported(t, b, "sb1.xml", fixture(t, "sparebank1-camt053-001-08.xml"), 0)
	if s := sb1.Statements[0]; s.Imported != 6 || s.Continuity != "ok" {
		t.Fatalf("sparebank 1 = %+v", s)
	}
	r := reconcile(t, b, op.Id)
	fees := 0
	for _, l := range r.Lines {
		if l.Amount == "-50.00" && l.BookedOn.String() == "2026-09-12" {
			fees++
		}
	}
	if fees != 2 || len(r.Lines) != 11 {
		t.Fatalf("lines = %d, the two identical fees %d", len(r.Lines), fees)
	}
	batch := lineOf(t, r, "2026-09-02", "3750.00")
	if len(batch.Batch) != 3 || batch.Batch[1].Amount != "1500.00" || deref(batch.Batch[1].Kid) != "0000102010" || !batch.CustomerPayment {
		t.Fatalf("batch = %+v", batch)
	}
	if rev := lineOf(t, r, "2026-09-05", "6250.00"); !rev.Reversal {
		t.Fatalf("reversal = %+v", rev)
	}
	if card := lineOf(t, r, "2026-09-11", "-349.00"); card.Kind != "card" || card.Source != "camt053" {
		t.Fatalf("card = %+v", card)
	}
	if r.Foot.StatementBalance == nil || *r.Foot.StatementBalance != "73544.00" || r.Foot.LedgerBalance != "0.00" ||
		deref(r.Foot.Difference) != "73544.00" {
		t.Fatalf("foot = %+v", r.Foot)
	}
	// A statement starting after a gap is flagged, not refused.
	gap := bytes.ReplaceAll(fixture(t, "sparebank1-camt053-001-08.xml"), []byte("2026-09-1"), []byte("2026-09-2"))
	gap = bytes.ReplaceAll(gap, []byte("2026-09-08"), []byte("2026-09-18"))
	gap = bytes.ReplaceAll(gap, []byte("SB1-15030012345-37"), []byte("SB1-15030012345-39"))
	g := imported(t, b, "gap.xml", gap, 0)
	if g.Statements[0].Continuity != "gap" || !strings.Contains(g.Statements[0].ContinuityDetail, "2026-09-15 to 2026-09-17") {
		t.Fatalf("gap = %+v", g.Statements[0])
	}
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/bank/statements?bankAccountId=%d", base, op.Id), nil)
	var list gen.AccountingBankStatementList
	res.JSON(&list)
	if res.Status != http.StatusOK || len(list.Items) != 4 || !list.Items[0].HasFile {
		t.Fatalf("statements = %d %+v", res.Status, list)
	}
	dl := b.c.Do(http.MethodGet, fmt.Sprintf("%s/bank/statements/%d/file", base, out.Statements[0].StatementId), nil)
	if dl.Status != http.StatusOK || !bytes.Equal(dl.Body, fixture(t, "dnb-camt053-001-02.xml")) {
		t.Fatalf("download = %d", dl.Status)
	}
}

// TestBankImport_Refusals: a DOCTYPE, another camt message, a file of only
// unknown accounts, a CSV without its account or mapping.
func TestBankImport_Refusals(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	evil := []byte(`<?xml version="1.0"?><!DOCTYPE d [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;">]>` +
		`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">&b;</Document>`)
	expectProblem(t, importFile(b.c, "evil.xml", evil, 0), http.StatusBadRequest, "bank_statement_invalid")
	c54 := bytes.ReplaceAll(fixture(t, "dnb-camt053-001-02.xml"), []byte("camt.053.001.02"), []byte("camt.054.001.02"))
	expectProblem(t, importFile(b.c, "c54.xml", c54, 0), http.StatusBadRequest, "bank_statement_invalid")
	stranger := bytes.ReplaceAll(fixture(t, "sparebank1-camt053-001-08.xml"), []byte("NO4615030012345"), []byte("NO8330001234567"))
	res := importFile(b.c, "stranger.xml", stranger, 0)
	expectProblem(t, res, http.StatusBadRequest, "bank_statement_account_unknown")
	if !strings.Contains(string(res.Body), "30001234567") {
		t.Fatalf("the refusal names no account: %s", res.Body)
	}
	expectProblem(t, importFile(b.c, "x.csv", []byte("a;b\n"), 0), http.StatusBadRequest, "bank_statement_account_required")
	expectProblem(t, importFile(b.c, "x.csv", []byte("a;b\n"), op.Id), http.StatusConflict, "bank_csv_mapping_required")
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/csv-mapping", base, op.Id), map[string]any{
		"delimiter": ";", "encoding": "utf-8", "headerRows": 0, "dateFormat": "DD.MM.YYYY", "decimalSeparator": ",",
		"thousandsSeparator": "", "invertSign": false, "columns": map[string]any{"date": 0}}), http.StatusBadRequest, "bank_csv_mapping_invalid")
}

// csvMapping is a mapping of a bank's CSV export in ISO-8859-1: two header
// rows, comma decimals, separate out and in columns.
var latinMapping = map[string]any{"delimiter": ";", "encoding": "iso-8859-1", "headerRows": 2, "dateFormat": "DD.MM.YYYY",
	"decimalSeparator": ",", "thousandsSeparator": " ", "invertSign": false,
	"columns": map[string]any{"date": 0, "text": 1, "debit": 2, "credit": 3, "counterpartyAccount": 4}}

// TestBankImport_CSV: the mapping saved and previewed, the file imported in
// ISO-8859-1 with comma decimals and debit and credit columns, imported again
// with nothing added, two identical lines both kept.
func TestBankImport_CSV(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	data := []byte("Kontoutskrift\nDato;Forklaring;Ut;Inn;Konto\n" +
		"01.09.2026;Innskudd fra Bj\xf8rn;;10 000,00;\n" +
		"03.09.2026;Gebyr;12,50;;\n" +
		"03.09.2026;Gebyr;12,50;;\n" +
		"04.09.2026;Husleie september;8 000,00;;70580001112\n")
	ct, body := statementBody("utskrift.csv", data, latinMapping)
	res := b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/accounts/%d/csv-preview", base, op.Id), nil, modtest.RawBody(ct, body))
	var preview gen.AccountingBankCsvPreview
	res.JSON(&preview)
	if res.Status != http.StatusOK || preview.Total != 4 || preview.Errors != 0 || deref(preview.Rows[0].Amount) != "10000.00" ||
		preview.Rows[0].Text != "Innskudd fra Bjørn" || deref(preview.Rows[1].Amount) != "-12.50" {
		t.Fatalf("preview = %d %+v", res.Status, preview)
	}
	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/csv-mapping", base, op.Id), latinMapping)
	if res.Status != http.StatusOK {
		t.Fatalf("mapping = %d %s", res.Status, res.Body)
	}
	out := imported(t, b, "utskrift.csv", data, op.Id)
	if s := out.Statements[0]; s.Imported != 4 || s.Source != "csv" || s.PeriodFrom.String() != "2026-09-01" || s.Continuity != "unknown" {
		t.Fatalf("import = %+v", out)
	}
	again := imported(t, b, "utskrift.csv", data, op.Id)
	if s := again.Statements[0]; s.Imported != 0 || s.Skipped != 4 {
		t.Fatalf("again = %+v", again)
	}
	r := reconcile(t, b, op.Id)
	if len(r.Lines) != 4 || r.Foot.StatementBalance != nil {
		t.Fatalf("lines = %+v", r)
	}
	broken := append(append([]byte{}, data...), []byte("31.02.2026;Feil;1,00;;\n")...)
	expectProblem(t, importFile(b.c, "broken.csv", broken, op.Id), http.StatusBadRequest, "bank_statement_invalid")
}

// paymentBooks is books with the operating and savings accounts, and three
// invoices Invoices issued and was paid by OCR on the operating account on
// 2026-09-02, one more paid into the savings account and one into an account
// the books do not know.
func paymentBooks(t *testing.T) (*books, gen.AccountingBankAccount, gen.AccountingBankAccount) {
	t.Helper()
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	sp := bankAccount(t, b, "Sparing", savings, "1950")
	for i, gross := range []string{"1250.00", "1500.00", "1000.00", "400.00", "300.00"} {
		id, number := int64(9101+i), int64(201+i)
		net := mustRat(t, gross)
		net.Quo(net, mustRat(t, "1.25"))
		b.h.journal.addDocument(invoice(id, number, day(t, "2026-08-20"), []contracts.JournalLine{jline(1, net.FloatString(2), "3")},
			jsum("3", "25.00", net.FloatString(2), mustRat(t, gross).Sub(mustRat(t, gross), net).FloatString(2))))
		account := operating
		switch i {
		case 3:
			account = savings
		case 4:
			account = "30001234567"
		}
		b.h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPayment, SubjectID: int64(701 + i), Date: day(t, "2026-09-02"),
			InvoiceID: id, InvoiceNumber: number, CustomerID: customerAcme, AmountNOK: gross, Source: contracts.PaymentSourceOCR,
			BankAccount: account, BankReference: fmt.Sprintf("OCR-%d", i)})
	}
	catchUp(t, b.h)
	return b, op, sp
}

// TestReconcile_InvoicesPayments: a payment from Invoices posts to the
// ledger account of the bank account it was received on — the savings
// account's 1950 — or to the bank role's when the books do not know it, and
// the rule's version says so; a KID batch is matched to its three payments by
// the batch's transactions; undone, the postings are let go.
func TestReconcile_InvoicesPayments(t *testing.T) {
	b, op, _ := paymentBooks(t)
	v := expectVoucher(t, b, "payment", 704, "1950 400.00", "1500 -400.00")
	if deref(v.RuleVersion) != 2 || deref(v.Rule) != "sales.payment" {
		t.Fatalf("rule = %v %v", v.Rule, v.RuleVersion)
	}
	expectVoucher(t, b, "payment", 705, "1920 300.00", "1500 -300.00")
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	r := reconcile(t, b, op.Id)
	batch := lineOf(t, r, "2026-09-02", "3750.00")
	sg := first(t, batch, "batch")
	if len(sg.Postings) != 3 || sg.Certainty != "exact" || !strings.Contains(sg.Reason, "3") {
		t.Fatalf("batch suggestion = %+v", sg)
	}
	line := accepted(t, b, batch.Id, sg.Key)
	if line.Match.Kind != "batch" || len(line.Match.Postings) != 3 || line.Match.VoucherId != nil {
		t.Fatalf("batch match = %+v", line.Match)
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.vouchers WHERE kind = 'bank'`); n != 0 {
		t.Fatalf("a batch match posted %d vouchers", n)
	}
	r = reconcile(t, b, op.Id)
	if r.Foot.UnmatchedPostingsTotal != 1 {
		t.Fatalf("unmatched postings = %+v", r.Foot.UnmatchedPostings)
	}
	undone(t, b, batch.Id)
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.statement_match_postings WHERE standing`); n != 0 {
		t.Fatalf("standing links after the undo = %d", n)
	}
	// The same key again after the undo matches again.
	accepted(t, b, batch.Id, sg.Key)
}

// TestReconcile_SupplierPayments: an outgoing line with the supplier
// invoice's KID registers its payment through the supplier payment's own
// path; undone, the payment is removed and the invoice owed again. A payment
// of an invoice in euros in more NOK than its rate gives posts the loss.
func TestReconcile_SupplierPayments(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	s := supplier(t, b, map[string]any{"name": "Kontorrekvisita AS", "bankAccount": "86011117947"})
	inv := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "4711", "invoiceDate": "2026-08-25",
		"kid": "4711000058", "total": "6250.00",
		"lines": []map[string]any{{"accountId": b.acc("6300"), "taxCodeId": b.code("1"), "amount": "6250.00", "entry": "gross"}}})
	postInvoice(t, b, inv.Id)
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	r := reconcile(t, b, op.Id)
	pay := lineOf(t, r, "2026-09-03", "-6250.00")
	sg := first(t, pay, "supplier_invoice")
	if sg.Certainty != "exact" || deref(sg.SupplierInvoiceId) != inv.Id || len(sg.Lines) != 2 {
		t.Fatalf("supplier suggestion = %+v", sg)
	}
	line := accepted(t, b, pay.Id, sg.Key)
	expectPostings(t, "supplier payment", matchedVoucher(t, b, line), "2400 6250.00", "1920 -6250.00")
	res := b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil)
	var after gen.AccountingSupplierInvoice
	res.JSON(&after)
	if after.State != "paid" || len(after.Payments) != 1 || after.Payments[0].PaidOn.String() != "2026-09-03" {
		t.Fatalf("invoice after = %+v", after)
	}
	undone(t, b, pay.Id)
	res = b.c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, inv.Id), nil)
	res.JSON(&after)
	if after.State != "posted" || after.Payments[0].RemovalVoucher == nil || deref(after.Balance) != "-6250.00" {
		t.Fatalf("invoice after the undo = %+v", after)
	}

	// In euros: 100 EUR at 11.5, paid with 1180.00 NOK from the CSV.
	eur := supplier(t, b, map[string]any{"name": "Cloud GmbH", "country": "DE", "currency": "EUR", "bankAccount": "70580001112"})
	foreign := draft(t, b, map[string]any{"supplierId": eur.Id, "kind": "invoice", "invoiceNumber": "INV-77", "invoiceDate": "2026-08-20",
		"exchangeRate": "11.5", "total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}})
	postInvoice(t, b, foreign.Id)
	if res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/csv-mapping", base, op.Id), latinMapping); res.Status != http.StatusOK {
		t.Fatalf("mapping = %d", res.Status)
	}
	imported(t, b, "eur.csv", []byte("x\nDato;Tekst;Ut;Inn;Konto\n15.09.2026;Cloud GmbH INV-77;1 180,00;;70580001112\n"), op.Id)
	r = reconcile(t, b, op.Id)
	eurLine := lineOf(t, r, "2026-09-15", "-1180.00")
	sg = first(t, eurLine, "supplier_invoice")
	line = accepted(t, b, eurLine.Id, sg.Key)
	expectPostings(t, "euro payment", matchedVoucher(t, b, line), "2400 1150.00", "1920 -1180.00", "8160 30.00")
}

// vatPaid is books whose VAT terms 1 to 4 of 2026 are closed, term 4 owing
// 21 512 kroner, with the operating and savings accounts and the two files.
func vatPaid(t *testing.T) (*books, gen.AccountingBankAccount, gen.AccountingBankAccount) {
	t.Helper()
	b := vatBooks(t)
	b.taxed(t, "2026-08-10", "3000", "3", "-86048.00")
	for term := 1; term <= 4; term++ {
		b.close(t, 2026, term)
	}
	op := bankAccount(t, b, "Drift", operating, "1920")
	sp := bankAccount(t, b, "Sparing", savings, "1950")
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	imported(t, b, "sb1.xml", fixture(t, "sparebank1-camt053-001-08.xml"), 0)
	return b, op, sp
}

// TestReconcile_TheBanksVouchers: a Vipps payout with its fees, the VAT
// payment that clears 2740 to zero, a transfer matched on both statements,
// a bank rule's fee and a card purchase coded with VAT — each to the øre,
// and each undone by its reversal.
func TestReconcile_TheBanksVouchers(t *testing.T) {
	b, op, sp := vatPaid(t)
	// The clearing account holds 4 990 from Vipps' sales of the day before.
	mustPost(t, b.c, "2026-09-07", "Vipps-salg", line{"accountId": b.acc("1580"), "amount": "4990.00"},
		line{"accountId": b.acc("2900"), "amount": "-4990.00"})
	res := b.c.Do(http.MethodPost, base+"/bank/rules", map[string]any{"name": "Bankgebyr", "textContains": "gebyr nettbank",
		"direction": "out", "accountId": b.acc("7770")})
	if res.Status != http.StatusCreated {
		t.Fatalf("rule = %d %s", res.Status, res.Body)
	}
	var rule gen.AccountingBankRule
	res.JSON(&rule)

	r := reconcile(t, b, op.Id)
	vipps := lineOf(t, r, "2026-09-08", "4850.00")
	sg := first(t, vipps, "vipps_payout")
	if !strings.Contains(sg.Reason, "140.00") || len(sg.Lines) != 3 || deref(sg.Fee) != "140.00" || deref(sg.FeeMax) != "242.50" {
		t.Fatalf("vipps suggestion = %+v", sg)
	}
	l := accepted(t, b, vipps.Id, sg.Key)
	v := matchedVoucher(t, b, l)
	expectPostings(t, "vipps payout", v, "1920 4850.00", "7770 140.00", "1580 -4990.00")
	if v.Kind != "bank" || deref(v.Rule) != "bank.vipps_payout" || deref(v.RuleVersion) != 1 {
		t.Fatalf("vipps voucher = %+v", v)
	}

	vat := lineOf(t, r, "2026-09-10", "-21512.00")
	sg = first(t, vat, "vat")
	l = accepted(t, b, vat.Id, sg.Key)
	expectPostings(t, "vat payment", matchedVoucher(t, b, l), "1920 -21512.00", "2740 21512.00")
	if bal := b.balances(t, "2026-09-30")["2740"]; bal != "0.00" && bal != "" {
		t.Fatalf("2740 after the payment = %s", bal)
	}

	tr := lineOf(t, r, "2026-09-06", "-15000.00")
	sg = first(t, tr, "transfer")
	if sg.Certainty != "exact" || sg.OtherLineId == nil {
		t.Fatalf("transfer suggestion = %+v", sg)
	}
	l = accepted(t, b, tr.Id, sg.Key)
	expectPostings(t, "transfer", matchedVoucher(t, b, l), "1920 -15000.00", "1950 15000.00")
	other := lineOf(t, reconcile(t, b, sp.Id), "2026-09-06", "15000.00")
	if other.State != "matched" || other.Match == nil || deref(other.Match.VoucherId) != *l.Match.VoucherId {
		t.Fatalf("the savings side = %+v", other)
	}

	fee := lineOf(t, r, "2026-09-04", "-95.00")
	sg = first(t, fee, "rule")
	if deref(sg.BankRuleId) != rule.Id {
		t.Fatalf("rule suggestion = %+v", sg)
	}
	l = accepted(t, b, fee.Id, sg.Key)
	expectPostings(t, "fee", matchedVoucher(t, b, l), "1920 -95.00", "7770 95.00")
	expectProblem(t, b.c.Do(http.MethodDelete, fmt.Sprintf("%s/bank/rules/%d", base, rule.Id), nil), http.StatusConflict, "bank_rule_in_use")

	card := lineOf(t, r, "2026-09-11", "-349.00")
	code := func(key string) *modtest.Response {
		return b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/code", base, card.Id), map[string]any{"key": key,
			"description": "Adobe", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "349.00", "taxCodeId": b.code("1")}}})
	}
	res = code("k-1")
	if res.Status != http.StatusCreated {
		t.Fatalf("code = %d %s", res.Status, res.Body)
	}
	var coded gen.AccountingBankLine
	res.JSON(&coded)
	expectPostings(t, "coded", matchedVoucher(t, b, coded), "1920 -349.00", "6300 279.20 1 base", "2710 69.80 1 tax")
	if res := code("k-1"); res.Status != http.StatusCreated {
		t.Fatalf("the same code again = %d %s", res.Status, res.Body)
	}
	expectProblem(t, code("k-2"), http.StatusConflict, "bank_line_matched")
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.vouchers WHERE rule = 'bank.coded'`); n != 1 {
		t.Fatalf("coded vouchers = %d", n)
	}

	// Each is undone by its reversal; the transfer opens both lines.
	for _, id := range []int64{vipps.Id, vat.Id, fee.Id, card.Id, tr.Id} {
		undone(t, b, id)
	}
	if again := lineOf(t, reconcile(t, b, sp.Id), "2026-09-06", "15000.00"); again.State != "open" {
		t.Fatalf("the savings side after the undo = %+v", again)
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.vouchers WHERE rule = 'bank.match_undone'`); n != 5 {
		t.Fatalf("reversals = %d", n)
	}
	if bal := b.balances(t, "2026-09-30")["2740"]; bal != "-21512.00" {
		t.Fatalf("2740 after the undo = %s", bal)
	}
}

// TestReconcile_CustomerPaymentGuard (R11): money in with a KID that
// Invoices has not registered is pointed to Invoices, never coded here — nor
// coded to a sales income account.
func TestReconcile_CustomerPaymentGuard(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	imported(t, b, "sb1.xml", fixture(t, "sparebank1-camt053-001-08.xml"), 0)
	r := reconcile(t, b, op.Id)
	kid := lineOf(t, r, "2026-09-12", "2000.00")
	first(t, kid, "invoices")
	if !kid.CustomerPayment {
		t.Fatalf("line = %+v", kid)
	}
	expectProblem(t, acceptOn(b.c, kid.Id, "invoices"), http.StatusConflict, "bank_line_customer_payment")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/code", base, kid.Id), map[string]any{"key": "x",
		"lines": []map[string]any{{"accountId": b.acc("8050"), "amount": "-2000.00"}}}), http.StatusConflict, "bank_line_customer_payment")
	vipps := lineOf(t, r, "2026-09-08", "4850.00")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/code", base, vipps.Id), map[string]any{"key": "y",
		"lines": []map[string]any{{"accountId": b.acc("3000"), "amount": "-4850.00"}}}), http.StatusConflict, "bank_line_customer_payment")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/code", base, vipps.Id), map[string]any{"key": "z",
		"lines": []map[string]any{{"accountId": b.acc("8050"), "amount": "-4000.00"}}}), http.StatusBadRequest, "voucher_unbalanced")
	expectProblem(t, acceptOn(b.c, vipps.Id, "posting:1"), http.StatusConflict, "bank_suggestion_stale")
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/undo", base, vipps.Id), nil), http.StatusConflict,
		"bank_line_not_matched")
}

// TestReconcile_DoubleClicksAndTwoPeople: the same accept twice posts once;
// two people accepting one line at once leave one match and one voucher.
func TestReconcile_DoubleClicksAndTwoPeople(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	res := b.c.Do(http.MethodPost, base+"/bank/rules", map[string]any{"name": "Gebyr", "textContains": "gebyr", "accountId": b.acc("7770")})
	if res.Status != http.StatusCreated {
		t.Fatalf("rule = %d %s", res.Status, res.Body)
	}
	fee := lineOf(t, reconcile(t, b, op.Id), "2026-09-04", "-95.00")
	sg := first(t, fee, "rule")
	other := b.h.SignIn(t, poster...)
	var wg sync.WaitGroup
	statuses := make([]int, 4)
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := b.c
			if i%2 == 1 {
				c = other
			}
			statuses[i] = acceptOn(c, fee.Id, sg.Key).Status
		}(i)
	}
	wg.Wait()
	for i, s := range statuses {
		if s != http.StatusOK {
			t.Errorf("accept %d = %d", i, s)
		}
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.vouchers WHERE rule = 'bank.rule'`); n != 1 {
		t.Fatalf("vouchers = %d", n)
	}
	if n := b.h.Count(t, `SELECT count(*)::int FROM accounting.statement_matches WHERE undone_at IS NULL`); n != 1 {
		t.Fatalf("matches = %d", n)
	}
}

// TestReconcile_LockAndStatus: the lock moves forward over lines not
// matched only when confirmed; a match in a locked month stands; the status
// says what is not reconciled; the bulk accept takes the exact ones only.
func TestReconcile_LockAndStatus(t *testing.T) {
	b, op, _ := paymentBooks(t)
	imported(t, b, "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), 0)
	r := reconcile(t, b, op.Id)
	ids := make([]int64, 0, len(r.Lines))
	for _, l := range r.Lines {
		ids = append(ids, l.Id)
	}
	res := b.c.Do(http.MethodPost, base+"/bank/lines/accept-exact", map[string]any{"lineIds": ids})
	var bulk gen.AccountingBankBulkAcceptResult
	res.JSON(&bulk)
	batch := lineOf(t, r, "2026-09-02", "3750.00")
	if res.Status != http.StatusOK || !slices.Equal(bulk.Accepted, []int64{batch.Id}) || len(bulk.Skipped) != len(ids)-1 {
		t.Fatalf("bulk = %d %+v", res.Status, bulk)
	}
	res = b.c.Do(http.MethodGet, base+"/bank/status?through=2026-09-30", nil)
	var st gen.AccountingBankStatus
	res.JSON(&st)
	if res.Status != http.StatusOK || st.Reconciled || len(st.Accounts) != 2 || st.Accounts[0].OpenLines != 4 {
		t.Fatalf("status = %+v", st)
	}
	expectProblem(t, b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-09-30"}), http.StatusConflict,
		"bank_unreconciled")
	if res := b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-08-31"}); res.Status != http.StatusOK {
		t.Fatalf("lock August = %d %s", res.Status, res.Body)
	}
	if res := b.c.Do(http.MethodPut, base+"/lock", map[string]any{"lockedThrough": "2026-09-30", "confirmUnreconciled": true}); res.Status != http.StatusOK {
		t.Fatalf("lock confirmed = %d %s", res.Status, res.Body)
	}
	expectProblem(t, b.c.Do(http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/undo", base, batch.Id), nil), http.StatusConflict, "period_locked")
	res = b.c.Do(http.MethodGet, base+"/meta", nil)
	var meta gen.AccountingMeta
	res.JSON(&meta)
	if meta.OpenBankLines != 5 || meta.BankFeedEnabled || meta.BankFeedProblems != 0 {
		t.Fatalf("meta = %+v", meta)
	}
}

// fakeFeed is Invoices' bank feed as a test drives it.
type fakeFeed struct {
	mu       sync.Mutex
	status   contracts.BankFeedStatus
	accounts []bankfeed.Account
	txs      map[string][]bankfeed.Transaction
	fail     error
	asked    []string
}

func (f *fakeFeed) Status(context.Context) (contracts.BankFeedStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}

func (f *fakeFeed) Accounts(context.Context) ([]bankfeed.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accounts, nil
}

func (f *fakeFeed) Transactions(_ context.Context, account string, from, to time.Time) ([]bankfeed.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, account+" "+from.Format(time.DateOnly)+" "+to.Format(time.DateOnly))
	if f.fail != nil {
		return nil, f.fail
	}
	var out []bankfeed.Transaction
	for _, t := range f.txs[account] {
		if !t.BookedOn.Before(from) && !t.BookedOn.After(to) {
			out = append(out, t)
		}
	}
	return out, nil
}

// feedWorker is the bank feed's worker as module.Workers builds it.
func feedWorker(t *testing.T, h *harness) *accounting.BankFeedWorker {
	t.Helper()
	for _, w := range h.Workers(h.Deps()) {
		if w.Name() == "accounting.bankfeed" {
			fw, ok := w.(*accounting.BankFeedWorker)
			if !ok {
				t.Fatalf("accounting.bankfeed is a %T", w)
			}
			return fw
		}
	}
	t.Fatal("module.Workers builds no accounting.bankfeed")
	return nil
}

// TestBankFeed: an account the feed reads switched to Feed, cards too; the
// worker built by module.Workers writes every direction and kind with the
// bank's ids; the overlap is read again without duplicates; a refused key
// and a rate limit are recorded on the account; a credit Invoices registered
// from the same transaction is matched by its id.
func TestBankFeed(t *testing.T) {
	feed := &fakeFeed{status: contracts.BankFeedStatus{Connected: true, Provider: "folio", Interval: 30 * time.Minute},
		accounts: []bankfeed.Account{{Number: "NO4615030012345", Name: "Drift", Kind: bankfeed.AccountOperational},
			{Number: cardAcct, Name: "Kort", Kind: bankfeed.AccountCard}},
		txs: map[string][]bankfeed.Transaction{
			operating: {
				{ID: "FOLIO-TX-1", BookedOn: day(t, "2026-09-02"), AmountMinor: 125000, Currency: "NOK", Direction: bankfeed.DirectionCredit,
					Kind: bankfeed.KindTransfer, KID: "0000101012", DebtorName: "Acme AS"},
				{ID: "FOLIO-TX-2", BookedOn: day(t, "2026-09-03"), AmountMinor: 9500, Currency: "NOK", Direction: bankfeed.DirectionDebit,
					Kind: bankfeed.KindTransfer, Text: "Gebyr"},
				{ID: "FOLIO-TX-3", BookedOn: day(t, "2026-09-04"), AmountMinor: 100, Currency: "NOK", Direction: bankfeed.DirectionUnknown},
			},
			cardAcct: {{ID: "FOLIO-CARD-1", BookedOn: day(t, "2026-09-05"), AmountMinor: 34900, Currency: "NOK",
				Direction: bankfeed.DirectionDebit, Kind: bankfeed.KindCard, Text: "ADOBE"}},
		}}
	h := newHarness(t, modtest.WithBankFeed(feed))
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	b := &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
	op := bankAccount(t, b, "Drift", operating, "1920")
	card := bankAccount(t, b, "Kort", cardAcct, "1900")
	other := bankAccount(t, b, "Sparing", savings, "1950")
	res := c.Do(http.MethodGet, base+"/bank/feed/accounts", nil)
	var offered gen.AccountingBankFeedAccountList
	res.JSON(&offered)
	if res.Status != http.StatusOK || len(offered.Items) != 2 || offered.Items[0].Number != operating ||
		deref(offered.Items[0].BankAccountId) != op.Id || offered.Items[1].Kind != "card" {
		t.Fatalf("feed accounts = %d %+v", res.Status, offered)
	}
	expectProblem(t, c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", base, other.Id), map[string]any{"source": "feed"}),
		http.StatusConflict, "bank_feed_account_not_offered")
	for _, a := range []gen.AccountingBankAccount{op, card} {
		res := c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", base, a.Id), map[string]any{"source": "feed", "startDay": "2026-09-01"})
		if res.Status != http.StatusOK {
			t.Fatalf("switch %d = %d %s", a.Id, res.Status, res.Body)
		}
	}
	// Invoices registered the first credit from the same transaction.
	h.journal.addDocument(invoice(9301, 301, day(t, "2026-08-20"), []contracts.JournalLine{jline(1, "1000.00", "3")},
		jsum("3", "25.00", "1000.00", "250.00")))
	h.journal.addFact(contracts.JournalFact{Kind: contracts.FactPayment, SubjectID: 901, Date: day(t, "2026-09-02"), InvoiceID: 9301,
		InvoiceNumber: 301, CustomerID: customerAcme, AmountNOK: "1250.00", Source: contracts.PaymentSourceBankFeed,
		BankAccount: operating, BankReference: "FOLIO-TX-1"})
	catchUp(t, h)

	w := feedWorker(t, h)
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatalf("feed: %v", err)
	}
	r := reconcile(t, b, op.Id)
	if len(r.Lines) != 2 || lineOf(t, r, "2026-09-03", "-95.00").Source != "feed" || deref(lineOf(t, r, "2026-09-02", "1250.00").BankId) != "FOLIO-TX-1" {
		t.Fatalf("feed lines = %+v", r.Lines)
	}
	if cl := lineOf(t, reconcile(t, b, card.Id), "2026-09-05", "-349.00"); cl.Kind != "card" {
		t.Fatalf("card line = %+v", cl)
	}
	credit := lineOf(t, r, "2026-09-02", "1250.00")
	sg := first(t, credit, "feed_payment")
	if !sg.Exact {
		t.Fatalf("feed payment suggestion = %+v", sg)
	}
	res = c.Do(http.MethodPost, base+"/bank/lines/accept-exact", map[string]any{"lineIds": []int64{credit.Id}})
	var bulk gen.AccountingBankBulkAcceptResult
	res.JSON(&bulk)
	if !slices.Equal(bulk.Accepted, []int64{credit.Id}) {
		t.Fatalf("bulk = %+v", bulk)
	}
	res = c.Do(http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), nil)
	var got gen.AccountingBankAccount
	res.JSON(&got)
	if got.Feed == nil || deref(got.Feed.Status) != "ok" || got.Feed.LastSyncedAt == nil || !strings.Contains(got.Feed.Detail, "1 transactions") {
		t.Fatalf("after the sync = %+v", got.Feed)
	}

	// Not due yet: nothing is read. Due: the overlap again, nothing new.
	asked := len(feed.asked)
	if err := w.RunOnce(t.Context()); err != nil || len(feed.asked) != asked {
		t.Fatalf("a second run read the bank: %v %v", err, feed.asked)
	}
	h.Advance(31 * time.Minute)
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatalf("feed again: %v", err)
	}
	if n := h.Count(t, `SELECT count(*)::int FROM accounting.statement_lines`); n != 3 {
		t.Fatalf("lines after the overlap = %d", n)
	}
	if n := h.Count(t, `SELECT count(*)::int FROM accounting.statements`); n != 2 {
		t.Fatalf("statements after the overlap = %d", n)
	}

	// A rate limit waits the bank's wait; a refused key is shown.
	feed.mu.Lock()
	feed.fail = &bankfeed.ErrRateLimited{RetryAfter: 2 * time.Hour}
	feed.mu.Unlock()
	h.Advance(31 * time.Minute)
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatalf("limited: %v", err)
	}
	res = c.Do(http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d", base, op.Id), nil)
	res.JSON(&got)
	if deref(got.Feed.Status) != "rate_limited" || got.Feed.NextSyncAt == nil || got.Feed.NextSyncAt.Before(h.Now().Add(119*time.Minute)) {
		t.Fatalf("rate limited = %+v", got.Feed)
	}
	feed.mu.Lock()
	feed.fail = bankfeed.ErrUnauthorized
	feed.mu.Unlock()
	h.Advance(3 * time.Hour)
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatalf("refused: %v", err)
	}
	res = c.Do(http.MethodGet, base+"/meta", nil)
	var meta gen.AccountingMeta
	res.JSON(&meta)
	if !meta.BankFeedEnabled || meta.BankFeedProblems != 2 {
		t.Fatalf("meta = %+v", meta)
	}
	feed.mu.Lock()
	feed.status.Refused = true
	feed.mu.Unlock()
	expectProblem(t, c.Do(http.MethodGet, base+"/bank/feed/accounts", nil), http.StatusConflict, "bank_feed_refused")
	feed.mu.Lock()
	feed.status = contracts.BankFeedStatus{}
	feed.mu.Unlock()
	expectProblem(t, c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", base, other.Id), map[string]any{"source": "feed"}),
		http.StatusConflict, "bank_feed_not_connected")
	res = c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", base, op.Id), map[string]any{"source": "file"})
	if res.Status != http.StatusOK {
		t.Fatalf("back to file = %d %s", res.Status, res.Body)
	}
}

// Without the feed composed — Invoices off or its switch off — the source
// cannot be the feed and the worker reads nothing.
func TestBankFeed_Off(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	expectProblem(t, b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", base, op.Id), map[string]any{"source": "feed"}),
		http.StatusConflict, "bank_feed_off")
	expectProblem(t, b.c.Do(http.MethodGet, base+"/bank/feed/accounts", nil), http.StatusConflict, "bank_feed_off")
	if err := feedWorker(t, b.h).RunOnce(t.Context()); err != nil {
		t.Fatalf("feed off: %v", err)
	}
}

// TestBankRules: a rule's change is a new version; one never used is
// deleted.
func TestBankRules(t *testing.T) {
	b := newBooks(t)
	res := b.c.Do(http.MethodPost, base+"/bank/rules", map[string]any{"name": "Renter", "counterpartyContains": "DNB",
		"direction": "in", "accountId": b.acc("8050")})
	var r gen.AccountingBankRule
	res.JSON(&r)
	if res.Status != http.StatusCreated || r.Version != 1 || r.AccountNumber != "8050" {
		t.Fatalf("create = %d %+v", res.Status, r)
	}
	expectProblem(t, b.c.Do(http.MethodPost, base+"/bank/rules", map[string]any{"name": "Nothing", "accountId": b.acc("8050")}),
		http.StatusBadRequest, "")
	res = b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/rules/%d", base, r.Id), map[string]any{"name": "Renteinntekter",
		"counterpartyContains": "DNB", "direction": "in", "accountId": b.acc("8050"), "taxCodeId": b.code("7")})
	res.JSON(&r)
	if res.Status != http.StatusOK || r.Version != 2 || deref(r.TaxCode) != "7" {
		t.Fatalf("update = %d %+v", res.Status, r)
	}
	res = b.c.Do(http.MethodGet, base+"/bank/rules", nil)
	var list gen.AccountingBankRuleList
	res.JSON(&list)
	if len(list.Items) != 1 {
		t.Fatalf("list = %+v", list)
	}
	if res := b.c.Do(http.MethodDelete, fmt.Sprintf("%s/bank/rules/%d", base, r.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Status, res.Body)
	}
}

// TestPermissions_Bank: reading needs accounting:read, importing and
// reconciling accounting:post, the bank accounts and rules accounting:close.
func TestPermissions_Bank(t *testing.T) {
	b := newBooks(t)
	op := bankAccount(t, b, "Drift", operating, "1920")
	rd, ps, cl := b.h.SignIn(t, reader...), b.h.SignIn(t, poster...), b.h.SignIn(t, closer...)
	ct, body := statementBody("x.xml", fixture(t, "dnb-camt053-001-02.xml"), nil)
	cases := []struct {
		name   string
		c      *modtest.Client
		method string
		path   string
		body   any
		raw    bool
		want   int
	}{
		{"reader lists", rd, http.MethodGet, base + "/bank/accounts", nil, false, http.StatusOK},
		{"reader reconciles", rd, http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d/reconciliation?from=2026-09-01&to=2026-09-30", base, op.Id), nil, false, http.StatusOK},
		{"reader status", rd, http.MethodGet, base + "/bank/status?through=2026-09-30", nil, false, http.StatusOK},
		{"reader rules", rd, http.MethodGet, base + "/bank/rules", nil, false, http.StatusOK},
		{"reader imports", rd, http.MethodPost, base + "/bank/imports", nil, true, http.StatusForbidden},
		{"reader accepts", rd, http.MethodPost, base + "/bank/lines/1/accept", map[string]any{"key": "x"}, false, http.StatusForbidden},
		{"poster adds an account", ps, http.MethodPost, base + "/bank/accounts", map[string]any{"name": "X", "accountNumber": savings, "ledgerAccountId": b.acc("1950")}, false, http.StatusForbidden},
		{"poster adds a rule", ps, http.MethodPost, base + "/bank/rules", map[string]any{"name": "X", "textContains": "x", "accountId": b.acc("7770")}, false, http.StatusForbidden},
		{"poster switches the source", ps, http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", base, op.Id), map[string]any{"source": "file"}, false, http.StatusForbidden},
		{"poster maps CSV", ps, http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/csv-mapping", base, op.Id), latinMapping, false, http.StatusForbidden},
		{"closer imports", cl, http.MethodPost, base + "/bank/imports", nil, true, http.StatusForbidden},
		{"closer codes", cl, http.MethodPost, base + "/bank/lines/1/code", map[string]any{"key": "x", "lines": []any{}}, false, http.StatusForbidden},
		{"closer undoes", cl, http.MethodPost, base + "/bank/lines/1/undo", nil, false, http.StatusForbidden},
		{"closer bulk", cl, http.MethodPost, base + "/bank/lines/accept-exact", map[string]any{"lineIds": []int64{1}}, false, http.StatusForbidden},
		{"poster imports", ps, http.MethodPost, base + "/bank/imports", nil, true, http.StatusCreated},
	}
	for _, tc := range cases {
		var res *modtest.Response
		if tc.raw {
			res = tc.c.Do(tc.method, tc.path, nil, modtest.RawBody(ct, body))
		} else {
			res = tc.c.Do(tc.method, tc.path, tc.body)
		}
		if res.Status != tc.want {
			t.Errorf("%s = %d %s, want %d", tc.name, res.Status, res.Body, tc.want)
		}
	}
}

// The SAF-T file carries the company's bank accounts, each with its ledger
// account, where the books keep them; it still validates.
func TestSaftExport_TheBankAccounts(t *testing.T) {
	b := saftBooks(t)
	bankAccount(t, b, "Driftskonto", operating, "1920")
	sp := bankAccount(t, b, "Sparekonto", savings, "1950")
	res := b.c.Do(http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d", base, sp.Id), map[string]any{"name": "Sparekonto",
		"accountNumber": savings, "ledgerAccountId": b.acc("1950"), "bic": "DNBANOKK", "revision": sp.Revision})
	if res.Status != http.StatusOK {
		t.Fatalf("update = %d %s", res.Status, res.Body)
	}
	_, files := b.export(t, map[string]any{"fiscalYear": 2026})
	doc := string(files[0])
	for _, want := range []string{"<BankAccountNumber>15030012345</BankAccountNumber><BankAccountName>Driftskonto</BankAccountName><GeneralLedgerAccountID>1920</GeneralLedgerAccountID>",
		"<BankAccountNumber>12060543214</BankAccountNumber><BankAccountName>Sparekonto</BankAccountName><BIC>DNBANOKK</BIC><GeneralLedgerAccountID>1950</GeneralLedgerAccountID>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("the header has no %s", want)
		}
	}
	validate(t, files[0])
}

// The seed's entry points take the API's paths: a bank account added, a
// statement imported with its duplicate guard, a suggestion accepted.
func TestBank_EntryPointsForASeed(t *testing.T) {
	b := newBooks(t)
	ctx, user := t.Context(), uuid.New()
	id, err := accounting.AddBankAccount(ctx, b.h.Deps(), gen.AccountingBankAccountInput{Name: "Drift", AccountNumber: operating,
		LedgerAccountId: b.acc("1920")})
	if err != nil || id == 0 {
		t.Fatalf("add = %d %v", id, err)
	}
	out, err := accounting.ImportBankStatement(ctx, b.h.Deps(), "dnb.xml", fixture(t, "dnb-camt053-001-02.xml"), nil, user)
	if err != nil || len(out.Statements) != 1 || out.Statements[0].Imported != 5 {
		t.Fatalf("import = %+v %v", out, err)
	}
	if res := b.c.Do(http.MethodPost, base+"/bank/rules", map[string]any{"name": "Gebyr", "textContains": "gebyr",
		"accountId": b.acc("7770")}); res.Status != http.StatusCreated {
		t.Fatalf("rule = %d %s", res.Status, res.Body)
	}
	fee := lineOf(t, reconcile(t, b, id), "2026-09-04", "-95.00")
	keys, err := accounting.BankSuggestionKeys(ctx, b.h.Deps(), fee.Id)
	if err != nil || len(keys) == 0 {
		t.Fatalf("keys = %v %v", keys, err)
	}
	if err := accounting.AcceptBankSuggestion(ctx, b.h.Deps(), fee.Id, keys[0], user); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if l := lineOf(t, reconcile(t, b, id), "2026-09-04", "-95.00"); l.State != "matched" {
		t.Fatalf("line = %+v", l)
	}
}
