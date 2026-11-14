package integration_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/bankfeed/folio/foliotest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The bank end to end (accounting design A26–A29): the real customers,
// invoices and accounting modules on one database. An invoice paid through
// Invoices' OCR import posts its payment to the ledger account of the bank
// account it was received on, and the account's camt.053 statement line
// matches that posting by the bank's shared reference; and a bank account
// read through the feed Invoices holds — Folio's fake behind Invoices'
// contracts.BankFeed, the worker built by module.Workers — gets the account's
// every movement, a card purchase Invoices never keeps among them.

// bankLineJSON is a reconciliation line as the story reads it.
type bankLineJSON struct {
	ID          int64  `json:"id"`
	Amount      string `json:"amount"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Source      string `json:"source"`
	Suggestions []struct {
		Key  string `json:"key"`
		Kind string `json:"kind"`
	} `json:"suggestions"`
}

// accountingWorkers is the accounting module's workers as worker mode builds
// them over names.
func accountingWorkers(t *testing.T, h *modtest.Harness, names []string) (*accounting.CatchUpWorker, *accounting.BankFeedWorker) {
	t.Helper()
	mods := make([]module.Module, 0, len(names))
	for _, n := range names {
		mods = append(mods, moduleNamed(t, n))
	}
	var catchUp *accounting.CatchUpWorker
	var feed *accounting.BankFeedWorker
	for _, w := range module.Workers(h.Deps(), mods...) {
		switch x := w.(type) {
		case *accounting.CatchUpWorker:
			catchUp = x
		case *accounting.BankFeedWorker:
			feed = x
		}
	}
	if catchUp == nil || feed == nil {
		t.Fatal("module.Workers built no catch-up or bank feed worker")
	}
	return catchUp, feed
}

func TestAccounting_APaymentFromAnInvoicesBankFileMatchesItsStatementLine(t *testing.T) {
	names := []string{modCustomers, modInvoices, modAccounting}
	h, c, _, _ := installInvoices(t, names, []string{"accounting:read", "accounting:post", "accounting:close"})
	var seller struct {
		Revision int32 `json:"revision"`
	}
	okJSON(t, c, http.MethodGet, invoicesBase+"/settings", nil, &seller)
	okJSON(t, c, http.MethodPut, invoicesBase+"/settings", map[string]any{
		"legalName": "Kraft-Verket AS", "organisationNumber": "974 760 673",
		"vatRegistered": true, "inForetaksregisteret": true,
		"addressLine1": "Storgata 1", "addressLine2": "", "postalCode": "0155", "city": "Oslo", "country": "no",
		"bankAccount": "8601.11.17947", "iban": "NO93 8601 1117 947", "bic": "dnbanokkxxx",
		"email": sellerEmail, "defaultPaymentTermsDays": 14, "defaultCurrency": "NOK",
		"footerText": "Takk for handelen.", "seriesStart": 1,
		"peppolId": nil, "kidLength": 7, "kidAlgorithm": kid.Mod10, "revision": seller.Revision,
		"workVatCodes":     map[string]any{"hours": 1, "expenses": 1, "milestones": 1},
		"timesheetDefault": false, "timesheetPersonLabel": "initials",
	}, nil)
	okJSON(t, c, http.MethodPost, accountingBase+"/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "bimonthly", "language": "en"}, nil)

	// The seller's account is offered as the first bank account, on 1920.
	var list struct {
		Suggested *struct {
			AccountNumber   string `json:"accountNumber"`
			Name            string `json:"name"`
			LedgerAccountID int32  `json:"ledgerAccountId"`
		} `json:"suggested"`
	}
	okJSON(t, c, http.MethodGet, accountingBase+"/bank/accounts", nil, &list)
	if list.Suggested == nil || list.Suggested.AccountNumber != receivablesAccount {
		t.Fatalf("the seller's account is not offered: %+v", list.Suggested)
	}
	var bank struct {
		ID int64 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, accountingBase+"/bank/accounts", map[string]any{"name": list.Suggested.Name,
		"accountNumber": list.Suggested.AccountNumber, "ledgerAccountId": list.Suggested.LedgerAccountID}, &bank)

	customer := invoiceNewBusiness(t, c, "Kunde AS", "Kunde AS", "923609016")
	issued := issueDoc(t, c, newDraft(t, c, customer.Id).ID)
	doc := readReceivable(t, c, issued.ID)
	if doc.Kid == nil {
		t.Fatal("the invoice has no KID")
	}
	booked := time.Date(h.Now().Year(), h.Now().Month(), h.Now().Day(), 0, 0, 0, 0, time.UTC)
	uploadBankFile(t, c, bankfiletest.OCR("0000001", bankfiletest.OCRPayment{Type: 10, Account: receivablesAccount, Settled: booked,
		Ordered: booked, AmountMinor: 1250000, KID: *doc.Kid, ArchiveRef: "260912077"}))
	catchUp, _ := accountingWorkers(t, h, names)
	if err := catchUp.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	day := booked.Format(time.DateOnly)
	statement := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02"><BkToCstmrStmt>
<GrpHdr><MsgId>M1</MsgId><CreDtTm>%[1]sT06:00:00+02:00</CreDtTm></GrpHdr>
<Stmt><Id>S1</Id><CreDtTm>%[1]sT06:00:00+02:00</CreDtTm><Acct><Id><Othr><Id>%[2]s</Id></Othr></Id><Ccy>NOK</Ccy></Acct>
<Ntry><Amt Ccy="NOK">12500.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>%[1]s</Dt></BookgDt>
<AcctSvcrRef>260912077</AcctSvcrRef><AddtlNtryInf>OCR giro</AddtlNtryInf></Ntry>
</Stmt></BkToCstmrStmt></Document>`, day, receivablesAccount)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", "camt053.xml")
	_, _ = part.Write([]byte(statement))
	_ = w.Close()
	if res := c.Do(http.MethodPost, accountingBase+"/bank/imports", nil, modtest.RawBody(w.FormDataContentType(), body.Bytes())); res.Status != http.StatusCreated {
		t.Fatalf("the statement = %d %s", res.Status, res.Body)
	}
	var rec struct {
		Lines []bankLineJSON `json:"lines"`
	}
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d/reconciliation?from=%s&to=%s", accountingBase, bank.ID, day, day), nil, &rec)
	if len(rec.Lines) != 1 || len(rec.Lines[0].Suggestions) == 0 || rec.Lines[0].Suggestions[0].Kind != "feed_payment" {
		t.Fatalf("the line = %+v, want the payment's posting by its reference first", rec.Lines)
	}
	var matched bankLineJSON
	okJSON(t, c, http.MethodPost, fmt.Sprintf("%s/bank/lines/%d/accept", accountingBase, rec.Lines[0].ID),
		map[string]any{"key": rec.Lines[0].Suggestions[0].Key}, &matched)
	if matched.State != "matched" {
		t.Fatalf("the line after the accept = %+v", matched)
	}
}

func TestAccounting_TheBankFeedThroughInvoices(t *testing.T) {
	names := []string{modCustomers, modInvoices, modAccounting}
	folio := foliotest.New(t)
	folio.SetAccounts(foliotest.Account{AccountNumber: receivablesAccount, Name: "Drift", Type: "Operational", Balance: "0.00"},
		foliotest.Account{AccountNumber: "97100000001", Name: "Kort", Type: "Card", Balance: "0.00"})
	day := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
	folio.AddTransaction("97100000001", foliotest.Transaction{ID: "card-1", EventID: "e1", BookingDate: day, Description: "ADOBE",
		Debtor:   &foliotest.Party{Name: "Kraft-Verket AS", AccountNumber: "97100000001"},
		Creditor: &foliotest.Party{Name: "Adobe"}, TransactionAmount: foliotest.Amount{Amount: "349.00", Currency: "NOK"},
		CardAuthorization: &foliotest.CardAuthorization{CardID: "c1", MerchantName: "Adobe", Amount: foliotest.Amount{Amount: "349.00", Currency: "NOK"}},
		Complete:          true})
	h, c, _, _ := installInvoices(t, names, []string{"accounting:read", "accounting:post", "accounting:close"},
		modtest.WithEnv("INVOICES_BANK_FEED_ENABLED", "1"), modtest.WithEnv("INVOICES_FOLIO_BASE_URL", folio.URL()),
		modtest.WithTransport(folio.Transport()))
	okJSON(t, c, http.MethodPut, invoicesBase+"/bank-feed", map[string]any{"provider": "folio", "apiKey": foliotest.APIKey}, nil)
	okJSON(t, c, http.MethodPost, accountingBase+"/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "bimonthly", "language": "en"}, nil)
	var accounts struct {
		Items []struct {
			ID     int32  `json:"id"`
			Number string `json:"number"`
		} `json:"items"`
	}
	okJSON(t, c, http.MethodGet, accountingBase+"/accounts", nil, &accounts)
	ledger := int32(0)
	for _, a := range accounts.Items {
		if a.Number == "1900" {
			ledger = a.ID
		}
	}
	var card struct {
		ID int64 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, accountingBase+"/bank/accounts", map[string]any{"name": "Kort", "accountNumber": "97100000001",
		"ledgerAccountId": ledger}, &card)
	okJSON(t, c, http.MethodPut, fmt.Sprintf("%s/bank/accounts/%d/source", accountingBase, card.ID),
		map[string]any{"source": "feed", "startDay": "2026-09-01"}, nil)
	_, feed := accountingWorkers(t, h, names)
	if err := feed.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Lines []bankLineJSON `json:"lines"`
	}
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/bank/accounts/%d/reconciliation?from=2026-09-01&to=2026-09-30", accountingBase, card.ID), nil, &rec)
	if len(rec.Lines) != 1 || rec.Lines[0].Amount != "-349.00" || rec.Lines[0].Kind != "card" || rec.Lines[0].Source != "feed" {
		t.Fatalf("the card account's lines = %+v", rec.Lines)
	}
}
