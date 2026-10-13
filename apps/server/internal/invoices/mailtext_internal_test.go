package invoices

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// mailDocument is an issued document as the cover mail reads it: number, the
// seller's name and bank details, the buyer's language, the gross and the due
// date. iban and bic may be empty.
func mailDocument(t *testing.T, kind, language string, number int64, gross, iban, bic string) store.InvoicesInvoice {
	t.Helper()
	n, err := numericFromRat(mustRat(gross), 2)
	if err != nil {
		t.Fatal(err)
	}
	inv := store.InvoicesInvoice{
		Kind: kind, Status: statusIssued, Number: &number, Currency: "NOK", GrossTotal: n,
		BuyerLanguage: &language, SellerLegalName: ptr("Kraft-Verket AS"), SellerBankAccount: ptr("86011117947"),
		SellerIban: &iban, SellerBic: &bic,
	}
	if kind == kindInvoice {
		inv.DueDate = pgDate(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))
	}
	return inv
}

// Each cover mail, word for word (D4): an invoice in Norwegian with the
// payment paragraph in full, partly and not at all; an invoice in English
// naming the IBAN and the BIC, the IBAN alone, and the domestic account; a
// credit note in each language. Money and dates are the PDF's own — a
// no-break space between thousands in Norwegian.
func TestCoverMail_TheTexts(t *testing.T) {
	t.Parallel()
	gross := "15045"
	credited := store.InvoicesInvoice{Number: ptr(int64(1001))}
	for _, c := range []struct {
		name          string
		inv           store.InvoicesInvoice
		original      *store.InvoicesInvoice
		open          *big.Rat
		subject, body string
	}{
		{
			name: "nb, in full", inv: mailDocument(t, kindInvoice, "nb", 1001, gross, "NO9386011117947", "DNBANOKKXXX"), open: mustRat(gross),
			subject: "Faktura 1001 fra Kraft-Verket AS",
			body: "Hei,\n\n" +
				"Vedlagt følger faktura 1001 fra Kraft-Verket AS på NOK 15 045,00, med forfall 12.10.2026.\n" +
				"Beløpet betales til kontonummer 86011117947. Merk betalingen med fakturanummer 1001.\n\n" +
				"Med vennlig hilsen\nKraft-Verket AS\n",
		},
		{
			name: "nb, partly", inv: mailDocument(t, kindInvoice, "nb", 1001, gross, "", ""), open: mustRat("5045.5"),
			subject: "Faktura 1001 fra Kraft-Verket AS",
			body: "Hei,\n\n" +
				"Vedlagt følger faktura 1001 fra Kraft-Verket AS på NOK 15 045,00, med forfall 12.10.2026.\n" +
				"Utestående beløp er NOK 5 045,50, som betales til kontonummer 86011117947. Merk betalingen med fakturanummer 1001.\n\n" +
				"Med vennlig hilsen\nKraft-Verket AS\n",
		},
		{
			name: "nb, nothing (paid)", inv: mailDocument(t, kindInvoice, "nb", 1001, gross, "", ""), open: new(big.Rat),
			subject: "Faktura 1001 fra Kraft-Verket AS",
			body: "Hei,\n\n" +
				"Vedlagt følger faktura 1001 fra Kraft-Verket AS på NOK 15 045,00, med forfall 12.10.2026.\n" +
				"Fakturaen er gjort opp. Det er ingenting å betale.\n\n" +
				"Med vennlig hilsen\nKraft-Verket AS\n",
		},
		{
			name: "nb, nothing (a refund due)", inv: mailDocument(t, kindInvoice, "nb", 1001, gross, "", ""), open: mustRat("-300"),
			subject: "Faktura 1001 fra Kraft-Verket AS",
			body: "Hei,\n\n" +
				"Vedlagt følger faktura 1001 fra Kraft-Verket AS på NOK 15 045,00, med forfall 12.10.2026.\n" +
				"Fakturaen er gjort opp. Det er ingenting å betale.\n\n" +
				"Med vennlig hilsen\nKraft-Verket AS\n",
		},
		{
			name: "en, in full, IBAN and BIC", inv: mailDocument(t, kindInvoice, "en", 1001, gross, "NO9386011117947", "DNBANOKKXXX"), open: mustRat(gross),
			subject: "Invoice 1001 from Kraft-Verket AS",
			body: "Hello,\n\n" +
				"Please find attached invoice 1001 from Kraft-Verket AS for NOK 15,045.00, due 2026-10-12.\n" +
				"Please pay to IBAN NO9386011117947 (BIC DNBANOKKXXX), quoting invoice number 1001.\n\n" +
				"Kind regards\nKraft-Verket AS\n",
		},
		{
			name: "en, partly, IBAN without a BIC", inv: mailDocument(t, kindInvoice, "en", 1001, gross, "NO9386011117947", ""), open: mustRat("45"),
			subject: "Invoice 1001 from Kraft-Verket AS",
			body: "Hello,\n\n" +
				"Please find attached invoice 1001 from Kraft-Verket AS for NOK 15,045.00, due 2026-10-12.\n" +
				"The outstanding amount is NOK 45.00; please pay it to IBAN NO9386011117947, quoting invoice number 1001.\n\n" +
				"Kind regards\nKraft-Verket AS\n",
		},
		{
			name: "en, in full, the domestic account", inv: mailDocument(t, kindInvoice, "en", 1001, gross, "", ""), open: mustRat(gross),
			subject: "Invoice 1001 from Kraft-Verket AS",
			body: "Hello,\n\n" +
				"Please find attached invoice 1001 from Kraft-Verket AS for NOK 15,045.00, due 2026-10-12.\n" +
				"Please pay to account 86011117947, quoting invoice number 1001.\n\n" +
				"Kind regards\nKraft-Verket AS\n",
		},
		{
			name: "en, partly, the domestic account", inv: mailDocument(t, kindInvoice, "en", 1001, gross, "", ""), open: mustRat("1000"),
			subject: "Invoice 1001 from Kraft-Verket AS",
			body: "Hello,\n\n" +
				"Please find attached invoice 1001 from Kraft-Verket AS for NOK 15,045.00, due 2026-10-12.\n" +
				"The outstanding amount is NOK 1,000.00; please pay it to account 86011117947, quoting invoice number 1001.\n\n" +
				"Kind regards\nKraft-Verket AS\n",
		},
		{
			name: "en, nothing", inv: mailDocument(t, kindInvoice, "en", 1001, gross, "NO9386011117947", "DNBANOKKXXX"), open: new(big.Rat),
			subject: "Invoice 1001 from Kraft-Verket AS",
			body: "Hello,\n\n" +
				"Please find attached invoice 1001 from Kraft-Verket AS for NOK 15,045.00, due 2026-10-12.\n" +
				"The invoice has been settled. Nothing is due.\n\n" +
				"Kind regards\nKraft-Verket AS\n",
		},
		{
			name: "nb, a credit note", inv: mailDocument(t, kindCreditNote, "nb", 1002, "3000", "", ""), original: &credited,
			subject: "Kreditnota 1002 fra Kraft-Verket AS",
			body: "Hei,\n\n" +
				"Vedlagt følger kreditnota 1002 fra Kraft-Verket AS på NOK 3 000,00, som krediterer faktura 1001.\n\n" +
				"Med vennlig hilsen\nKraft-Verket AS\n",
		},
		{
			name: "en, a credit note", inv: mailDocument(t, kindCreditNote, "en", 1002, "3000", "NO9386011117947", ""), original: &credited,
			subject: "Credit note 1002 from Kraft-Verket AS",
			body: "Hello,\n\n" +
				"Please find attached credit note 1002 from Kraft-Verket AS for NOK 3,000.00, crediting invoice 1001.\n\n" +
				"Kind regards\nKraft-Verket AS\n",
		},
	} {
		got, err := coverMail(c.inv, c.original, c.open, "")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got.subject != c.subject {
			t.Errorf("%s: subject = %q, want %q", c.name, got.subject, c.subject)
		}
		if got.body != c.body {
			t.Errorf("%s: body =\n%q\nwant\n%q", c.name, got.body, c.body)
		}
	}
}

// A credit note names the invoice it credits: without its original, or with
// an original that has no number, the cover mail is an error — never a mail
// that credits "faktura 0".
func TestCoverMail_ACreditNoteWithoutItsOriginalIsAnError(t *testing.T) {
	t.Parallel()
	credit := mailDocument(t, kindCreditNote, "nb", 1002, "3000", "", "")
	for name, original := range map[string]*store.InvoicesInvoice{
		"no original": nil, "an original without a number": {},
	} {
		if got, err := coverMail(credit, original, nil, ""); err == nil {
			t.Errorf("%s: cover mail = %q, want an error", name, got.body)
		}
	}
}

// With a KID the payment paragraph asks for it in place of the invoice
// number (EHF and KID design D3, reading 7), in full and in part, in each
// language; a settled invoice still says nothing is due.
func TestCoverMail_TheKIDSentence(t *testing.T) {
	t.Parallel()
	withKid := func(language string) store.InvoicesInvoice {
		inv := mailDocument(t, kindInvoice, language, 1001, "15045", "NO9386011117947", "DNBANOKKXXX")
		inv.Kid, inv.KidAlgorithm = ptr("0010017"), ptr("mod10")
		return inv
	}
	for _, c := range []struct {
		name, language, open, payment string
	}{
		{"nb, in full", "nb", "15045", "Beløpet betales til kontonummer 86011117947. Merk betalingen med KID 0010017.\n"},
		{"nb, partly", "nb", "5045.5", "Utestående beløp er NOK 5\u00a0045,50, som betales til kontonummer 86011117947. Merk betalingen med KID 0010017.\n"},
		{"nb, settled", "nb", "0", "Fakturaen er gjort opp. Det er ingenting å betale.\n"},
		{"en, in full", "en", "15045", "Please pay to IBAN NO9386011117947 (BIC DNBANOKKXXX), quoting KID 0010017.\n"},
		{"en, partly", "en", "5045.5", "The outstanding amount is NOK 5,045.50; please pay it to IBAN NO9386011117947 (BIC DNBANOKKXXX), quoting KID 0010017.\n"},
	} {
		got, err := coverMail(withKid(c.language), nil, mustRat(c.open), "")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !strings.Contains(got.body, c.payment) || strings.Contains(got.body, "invoice number") || strings.Contains(got.body, "fakturanummer") {
			t.Errorf("%s: body\n%s\nwant the payment line %q and no invoice number", c.name, got.body, c.payment)
		}
	}
}

// The invoice's live pay link (D15) is offered after the payment paragraph,
// in the document's language, while something is open — in full and in
// part — and never once it is settled, nor without a link, nor on a credit
// note.
func TestCoverMail_PayLine(t *testing.T) {
	t.Parallel()
	const url = "https://pay.example.com/pay?token=abc_DEF-123"
	for _, c := range []struct {
		name, language, open, want string
	}{
		{"nb in full", "nb", "15045", "\nDu kan også betale med Vipps: " + url + "\n"},
		{"nb in part", "nb", "5000", "\nDu kan også betale med Vipps: " + url + "\n"},
		{"en in full", "en", "15045", "\nYou can also pay with Vipps: " + url + "\n"},
		{"en in part", "en", "5000", "\nYou can also pay with Vipps: " + url + "\n"},
	} {
		inv := mailDocument(t, kindInvoice, c.language, 1001, "15045", "", "")
		got, err := coverMail(inv, nil, mustRat(c.open), url)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		payment := strings.Index(got.body, "betales til kontonummer")
		if c.language == "en" {
			payment = strings.Index(got.body, "to account")
		}
		at := strings.Index(got.body, c.want)
		if at < 0 || payment < 0 || at < payment {
			t.Errorf("%s: body =\n%s\nwant %q after the payment paragraph", c.name, got.body, c.want)
		}
		without, err := coverMail(inv, nil, mustRat(c.open), "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(without.body, "Vipps") {
			t.Errorf("%s without a link: body =\n%s\nwant no pay line", c.name, without.body)
		}
	}
	for _, language := range []string{"nb", "en"} {
		settled, err := coverMail(mailDocument(t, kindInvoice, language, 1001, "15045", "", ""), nil, mustRat("0"), url)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(settled.body, url) {
			t.Errorf("%s settled: body =\n%s\nwant no pay line", language, settled.body)
		}
	}
	original := mailDocument(t, kindInvoice, "nb", 1001, "3000", "", "")
	credit, err := coverMail(mailDocument(t, kindCreditNote, "nb", 1002, "3000", "", ""), &original, nil, url)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(credit.body, url) {
		t.Errorf("a credit note's body =\n%s\nwant no pay line", credit.body)
	}
}
