package invoices

import (
	"bytes"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/invoices/ehf"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file tests ehfDocumentOf: the store rows of an issued document as the
// EHF Document the ehf package renders (EHF and KID design D4).

func num(t *testing.T, s string, places int) pgtype.Numeric {
	t.Helper()
	n, err := numericFromRat(rat(s), places)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// issuedRows is an issued invoice with a discounted 25 % line and an exempt
// one, its VAT rows and its KID, as the store holds them.
func issuedRows(t *testing.T) (store.InvoicesInvoice, []store.InvoicesLine, []store.InvoicesVatSummary) {
	t.Helper()
	s := func(v string) *string { return &v }
	b := func(v bool) *bool { return &v }
	number, issuedAt := int64(1001), time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	inv := store.InvoicesInvoice{
		ID: 42, Kind: kindInvoice, Status: statusIssued, Number: &number, Currency: "NOK",
		IssueDate: pgDate(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)), DueDate: pgDate(time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)),
		DeliveryFrom: pgDate(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)), DeliveryTo: pgDate(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)),
		DeliveryAddressLine1: s("Lagerveien 5"), DeliveryPostalCode: s("0581"), DeliveryCity: s("Oslo"), DeliveryCountry: s("NO"),
		YourReference: "Ola Nordmann", OrderReference: "PO-9", OurReference: "Kari",
		BuyerType: s("business"), BuyerName: s("Nordlys Bygg AS"), BuyerOrganisationNumber: s("987654325"),
		BuyerAddressLine1: s("Industriveien 12"), BuyerPostalCode: s("7080"), BuyerCity: s("Heimdal"), BuyerRegion: s("Trøndelag"),
		BuyerCountry: s("NO"), BuyerPeppolID: s("0192:987654325"), BuyerLanguage: s("nb"),
		SellerLegalName: s("Fjordkraft Konsult AS"), SellerOrganisationNumber: s("923456783"), SellerVatRegistered: b(true),
		SellerInForetaksregisteret: b(true), SellerAddressLine1: s("Storgata 1"), SellerPostalCode: s("0155"), SellerCity: s("Oslo"),
		SellerCountry: s("NO"), SellerBankAccount: s("86011117947"), SellerIban: s("NO9386011117947"), SellerBic: s("DNBANOKKXXX"),
		SellerEmail: s("faktura@example.no"),
		NetTotal:    num(t, "9996.40", 2), VatTotal: num(t, "2249.10", 2), GrossTotal: num(t, "12245.50", 2),
		IssuedAt: &issuedAt, Kid: s("00010017"), KidAlgorithm: s("mod10"),
	}
	lines := []store.InvoicesLine{
		{Position: 1, Description: "Kontorstoler", Quantity: num(t, "4", 3), Unit: "stk.", UnitPrice: num(t, "2499", 4),
			DiscountPercent: num(t, "10", 2), LineGross: num(t, "9996", 2), LineAllowance: num(t, "999.60", 2), LineNet: num(t, "8996.40", 2),
			VatRatePercent: num(t, "25", 2), VatCategory: s("S"), SafTCode: s("3")},
		{Position: 2, Description: "Utleie", Quantity: num(t, "1", 3), Unit: "mnd", UnitPrice: num(t, "1000", 4),
			DiscountPercent: num(t, "0", 2), LineGross: num(t, "1000", 2), LineAllowance: num(t, "0", 2), LineNet: num(t, "1000", 2),
			VatRatePercent: num(t, "0", 2), VatCategory: s("E"), SafTCode: s("6"), ExemptionReason: s("Unntatt fra merverdiavgift (mval. kap. 3)")},
	}
	sums := []store.InvoicesVatSummary{
		{VatCategory: "S", RatePercent: num(t, "25", 2), SafTCode: "3", TaxableAmount: num(t, "8996.40", 2), VatAmount: num(t, "2249.10", 2)},
		{VatCategory: "E", RatePercent: num(t, "0", 2), SafTCode: "6", ExemptionReason: s("Unntatt fra merverdiavgift (mval. kap. 3)"),
			TaxableAmount: num(t, "1000", 2), VatAmount: num(t, "0", 2)},
	}
	return inv, lines, sums
}

// Every field the EHF needs comes from the document's own rows, the seller's
// Peppol id from the caller, and the PDF's bytes and name from the store.
func TestEHFDocumentOf_TheRows(t *testing.T) {
	t.Parallel()
	inv, lines, sums := issuedRows(t)
	pdf := []byte("%PDF-1.4 stored")
	d, err := ehfDocumentOf(inv, lines, sums, nil, "0192:923456783", pdf)
	if err != nil {
		t.Fatalf("ehfDocumentOf: %v", err)
	}
	if d.Kind != ehf.KindInvoice || d.Language != "nb" || d.Number != "1001" || d.IssueDate != "2026-10-01" || d.DueDate != "2026-10-15" ||
		d.Currency != "NOK" || d.BuyerReference != "Ola Nordmann" || d.OrderReference != "PO-9" || d.Original != nil {
		t.Errorf("header = %+v", d)
	}
	if d.DeliveryDate != "" || d.DeliveryFrom != "2026-09-01" || d.DeliveryTo != "2026-09-30" ||
		d.DeliveryPlace == nil || *d.DeliveryPlace != (ehf.Address{Line1: "Lagerveien 5", PostalCode: "0581", City: "Oslo", Country: "NO"}) {
		t.Errorf("delivery = %q %q %q %+v", d.DeliveryDate, d.DeliveryFrom, d.DeliveryTo, d.DeliveryPlace)
	}
	wantSeller := ehf.Seller{
		PeppolID: "0192:923456783", Name: "Fjordkraft Konsult AS",
		Address:            ehf.Address{Line1: "Storgata 1", PostalCode: "0155", City: "Oslo", Country: "NO"},
		OrganisationNumber: "923456783", VATRegistered: true, Foretaksregisteret: true, Email: "faktura@example.no",
	}
	if d.Seller != wantSeller {
		t.Errorf("seller = %+v", d.Seller)
	}
	wantBuyer := ehf.Buyer{
		PeppolID: "0192:987654325", Name: "Nordlys Bygg AS",
		Address:            ehf.Address{Line1: "Industriveien 12", PostalCode: "7080", City: "Heimdal", Region: "Trøndelag", Country: "NO"},
		OrganisationNumber: "987654325",
	}
	if d.Buyer != wantBuyer {
		t.Errorf("buyer = %+v", d.Buyer)
	}
	if d.Payment != (ehf.Payment{BankAccount: "86011117947", IBAN: "NO9386011117947", BIC: "DNBANOKKXXX", KID: "00010017", KIDAlgorithm: "mod10"}) {
		t.Errorf("payment = %+v", d.Payment)
	}
	if len(d.Lines) != 2 {
		t.Fatalf("%d lines", len(d.Lines))
	}
	l := d.Lines[0]
	if l.ID != "1" || l.Description != "Kontorstoler" || l.Unit != "stk." || l.Category != "S" || l.Quantity.Cmp(rat("4")) != 0 ||
		l.UnitPrice.Cmp(rat("2499")) != 0 || l.DiscountPercent.Cmp(rat("10")) != 0 || l.Gross.Cmp(rat("9996")) != 0 ||
		l.Allowance.Cmp(rat("999.6")) != 0 || l.Net.Cmp(rat("8996.4")) != 0 || l.Rate.Cmp(rat("25")) != 0 {
		t.Errorf("line 1 = %+v", l)
	}
	if d.Lines[1].Category != "E" || d.Lines[1].ID != "2" {
		t.Errorf("line 2 = %+v", d.Lines[1])
	}
	if len(d.VAT) != 2 || d.VAT[0].Category != "S" || d.VAT[0].Amount.Cmp(rat("2249.10")) != 0 ||
		d.VAT[1].Category != "E" || d.VAT[1].ExemptionReason != "Unntatt fra merverdiavgift (mval. kap. 3)" {
		t.Errorf("VAT = %+v", d.VAT)
	}
	if d.NetTotal.Cmp(rat("9996.40")) != 0 || d.VATTotal.Cmp(rat("2249.10")) != 0 || d.GrossTotal.Cmp(rat("12245.50")) != 0 {
		t.Errorf("totals = %v %v %v", d.NetTotal, d.VATTotal, d.GrossTotal)
	}
	if !bytes.Equal(d.PDF, pdf) || d.PDFName != "faktura-1001.pdf" {
		t.Errorf("PDF = %q %q", d.PDF, d.PDFName)
	}

	// And it renders into a document the pre-check and the invariants pass.
	body, err := ehf.Render(d)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if rules, err := ehf.Precheck(body); err != nil || len(rules) != 0 {
		t.Errorf("Precheck = %v, %v", rules, err)
	}
	if rules, err := ehf.Invariants(body, d); err != nil || len(rules) != 0 {
		t.Errorf("Invariants = %v, %v", rules, err)
	}
}

// A credit note carries the original's number and date and no KID; a
// person, no legal id; an English buyer, the English file name.
func TestEHFDocumentOf_ACreditNoteToAPerson(t *testing.T) {
	t.Parallel()
	inv, lines, sums := issuedRows(t)
	original := inv
	originalNumber := int64(990)
	original.Number = &originalNumber
	original.IssueDate = pgDate(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC))
	inv.Kind, inv.DueDate, inv.Kid, inv.KidAlgorithm = kindCreditNote, pgtype.Date{}, nil, nil
	person, en := "person", "en"
	inv.BuyerType, inv.BuyerOrganisationNumber, inv.BuyerLanguage = &person, nil, &en
	d, err := ehfDocumentOf(inv, lines, sums, &original, "0192:923456783", []byte("%PDF"))
	if err != nil {
		t.Fatalf("ehfDocumentOf: %v", err)
	}
	if d.Kind != ehf.KindCreditNote || d.DueDate != "" || d.Payment.KID != "" || d.Payment.KIDAlgorithm != "" {
		t.Errorf("credit note = %+v", d)
	}
	if d.Original == nil || *d.Original != (ehf.DocumentReference{Number: "990", IssueDate: "2026-09-15"}) {
		t.Errorf("original = %+v", d.Original)
	}
	if !d.Buyer.Person || d.Buyer.OrganisationNumber != "" || d.Language != "en" || d.PDFName != "credit-note-1001.pdf" {
		t.Errorf("buyer = %+v, language %q, file %q", d.Buyer, d.Language, d.PDFName)
	}
}

// What cannot be an EHF is an error: a draft, a stored KID that does not
// verify against its stored algorithm (D3), a line without its VAT snapshot.
func TestEHFDocumentOf_Refuses(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*store.InvoicesInvoice, []store.InvoicesLine){
		"a draft":              func(inv *store.InvoicesInvoice, _ []store.InvoicesLine) { inv.Status = statusDraft },
		"a tampered KID":       func(inv *store.InvoicesInvoice, _ []store.InvoicesLine) { k := "00010018"; inv.Kid = &k },
		"another algorithm":    func(inv *store.InvoicesInvoice, _ []store.InvoicesLine) { a := "mod11"; inv.KidAlgorithm = &a },
		"no VAT snapshot":      func(_ *store.InvoicesInvoice, l []store.InvoicesLine) { l[1].VatCategory = nil },
		"an unreadable amount": func(_ *store.InvoicesInvoice, l []store.InvoicesLine) { l[0].LineGross = pgtype.Numeric{} },
	} {
		inv, lines, sums := issuedRows(t)
		mutate(&inv, lines)
		if _, err := ehfDocumentOf(inv, lines, sums, nil, "0192:923456783", nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
