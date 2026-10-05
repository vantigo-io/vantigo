package invoices

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file tests the PDF through its model: no PDF text extractor in this
// module's dependencies reads an embedded-subset font back into words, so what
// a document says is asserted on the words the renderer lays out
// (buildPDFModel), and the bytes only for being reproducible.

func rat(s string) *big.Rat { return mustRat(s) }

// anInvoice is an issued invoice from a VAT-registered AS in Foretaksregisteret
// with a 25 % line and a zero-rated one, to a Norwegian business.
func anInvoice() pdfDocument {
	number, terms := int64(1000), int32(14)
	issued := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	due := issued.AddDate(0, 0, 14)
	delivered := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	zeroReason := "Fritatt for merverdiavgift"
	return pdfDocument{
		kind: kindInvoice, language: "nb", currency: "NOK", number: &number, issueDate: issued, dueDate: &due, paymentTermsDays: &terms,
		deliveryDate: &delivered, yourReference: "PO-77", ourReference: "Ola Nordmann",
		seller: pdfParty{name: "Kraft-Verket AS", line1: "Storgata 1", postalCode: "0155", city: "Oslo", country: "NO",
			organisationNumber: "974760673", email: "faktura@kraft-verket.no", vatRegistered: true, foretaksregisteret: true},
		buyer: pdfParty{name: "Acme Norge AS", line1: "Kundeveien 2", postalCode: "0150", city: "Oslo", country: "NO",
			organisationNumber: "923609016"},
		bankAccount: "86011117947", iban: "NO9386011117947", bic: "DNBANOKKXXX",
		lines: []pdfLine{
			{description: "Konsulenttime", unit: "timer", quantity: rat("10"), unitPrice: rat("1200"), discount: rat("0"), rate: rat("25"), net: rat("12000")},
			{description: "Eksport", unit: "stk", quantity: rat("1.5"), unitPrice: rat("33.3333"), discount: rat("10"), rate: rat("0"), net: rat("45")},
		},
		summaries: []vatSummary{
			{category: "S", rate: rat("25"), safT: "3", taxable: rat("12000"), vat: rat("3000")},
			{category: "Z", rate: rat("0"), safT: "5", reason: &zeroReason, taxable: rat("45"), vat: rat("0")},
		},
		totals:  documentTotals{net: rat("12045"), vat: rat("3000"), gross: rat("15045")},
		note:    "Takk for handelen.",
		footer:  "Kraft-Verket AS · Storgata 1 · 0155 Oslo",
		created: time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC),
	}
}

func TestPDFModel_AnInvoice(t *testing.T) {
	t.Parallel()
	m := buildPDFModel(anInvoice())

	if m.title != "Faktura" || m.watermark != "" {
		t.Errorf("title %q, watermark %q", m.title, m.watermark)
	}
	if want := []string{"Kraft-Verket AS", "Storgata 1", "0155 Oslo", "Org.nr. 974 760 673 MVA", "Foretaksregisteret", "faktura@kraft-verket.no"}; !slices.Equal(m.seller, want) {
		t.Errorf("seller = %q, want %q", m.seller, want)
	}
	if want := []string{"Acme Norge AS", "Kundeveien 2", "0150 Oslo", "Org.nr. 923 609 016"}; !slices.Equal(m.buyer, want) {
		t.Errorf("buyer = %q, want %q", m.buyer, want)
	}
	if want := [][2]string{
		{"Nummer", "1000"}, {"Fakturadato", "12.09.2026"}, {"Leveringsdato", "10.09.2026"},
		{"Forfallsdato", "26.09.2026"}, {"Betalingsbetingelser", "14 dager"}, {"Deres ref.", "PO-77"}, {"Vår ref.", "Ola Nordmann"},
	}; !slices.Equal(m.meta, want) {
		t.Errorf("meta = %q, want %q", m.meta, want)
	}
	if want := []string{"Eksport", "1,5", "stk", "33,3333", "10", "0", "45,00"}; !slices.Equal(m.lines[1], want) {
		t.Errorf("line 2 = %q, want %q", m.lines[1], want)
	}
	if want := []string{"Konsulenttime", "10", "timer", "1 200,00", "0", "25", "12 000,00"}; !slices.Equal(m.lines[0], want) {
		t.Errorf("line 1 = %q, want %q", m.lines[0], want)
	}
	if want := [][]string{{"S 25 %", "12 000,00", "3 000,00"}, {"Z 0 %", "45,00", "0,00"}}; !slices.Equal(m.vatRows[0], want[0]) || !slices.Equal(m.vatRows[1], want[1]) {
		t.Errorf("VAT rows = %q, want a row per rate, the 0 %% one included", m.vatRows)
	}
	if !slices.Equal(m.reasons, []string{"Fritatt for merverdiavgift"}) {
		t.Errorf("reasons = %q, want the Z row's", m.reasons)
	}
	// The currency, once where it binds every amount (§ 5-1-1 nr. 6).
	if m.totals[2] != [2]string{"Å betale", "NOK 15 045,00"} {
		t.Errorf("gross = %q, want Å betale NOK 15 045,00", m.totals[2])
	}
	if m.lineHeader[6] != "Beløp (NOK)" || m.vatHeader[2] != "MVA (NOK)" {
		t.Errorf("headers = %q %q, want Beløp (NOK) and MVA (NOK)", m.lineHeader, m.vatHeader)
	}
	if want := [][2]string{{"Kontonummer", "86011117947"}, {"IBAN", "NO9386011117947"}, {"BIC", "DNBANOKKXXX"}, {"Forfallsdato", "26.09.2026"}}; !slices.Equal(m.payment, want) ||
		m.paymentNote != "Vennligst oppgi fakturanummer ved betaling" {
		t.Errorf("payment = %q %q", m.payment, m.paymentNote)
	}
	if m.note != "Takk for handelen." || m.footer == "" {
		t.Errorf("note %q footer %q", m.note, m.footer)
	}
}

// "MVA" and "Foretaksregisteret" follow the flags (§ 5-1-2); a foreign buyer
// is named by its VAT/Reg. no.; the reverse-charge text is the regulation's
// Norwegian on an English document; a place of delivery prints only when it
// is not the buyer's address.
func TestPDFModel_TheFlagsAndTheLanguage(t *testing.T) {
	t.Parallel()
	d := anInvoice()
	d.seller.vatRegistered, d.seller.foretaksregisteret = false, false
	if m := buildPDFModel(d); slices.Contains(m.seller, "Foretaksregisteret") || !slices.Contains(m.seller, "Org.nr. 974 760 673") {
		t.Errorf("an unregistered seller = %q, want the number without MVA and no Foretaksregisteret", m.seller)
	}

	en := anInvoice()
	en.language = "en"
	en.buyer = pdfParty{name: "Svenska Aktiebolaget AB", line1: "Storgatan 1", postalCode: "111 22", city: "Stockholm", country: "SE", foreignID: "SE556677889901"}
	reason := "Omvendt avgiftsplikt – Merverdiavgift ikke beregnet"
	en.summaries = append(en.summaries, vatSummary{category: "AE", rate: rat("0"), safT: "51", reason: &reason, taxable: rat("100"), vat: rat("0")})
	en.deliveryPlace = &pdfParty{line1: "Byggeplassen", city: "Bergen", country: "NO"}
	m := buildPDFModel(en)
	if m.title != "Invoice" || !slices.Contains(m.buyer, "VAT/Reg. no. SE556677889901") || !slices.Contains(m.buyer, "SE") {
		t.Errorf("an English invoice to Sweden = %q %q", m.title, m.buyer)
	}
	if !slices.Contains(m.reasons, reverseChargeText) {
		t.Errorf("reasons = %q, want the Norwegian reverse-charge text", m.reasons)
	}
	if !slices.Contains(m.meta, [2]string{"Place of delivery", "Byggeplassen, Bergen"}) {
		t.Errorf("meta = %q, want the place of delivery", m.meta)
	}
	if m.lines[0][3] != "1,200.00" || m.meta[1] != [2]string{"Invoice date", "2026-09-12"} {
		t.Errorf("English numbers and dates = %q %q", m.lines[0][3], m.meta[1])
	}
	if m.totals[2] != [2]string{"Amount due", "NOK 15,045.00"} || m.lineHeader[6] != "Amount (NOK)" || m.vatHeader[2] != "VAT (NOK)" {
		t.Errorf("English currency = %q, %q, %q; want Amount due NOK 15,045.00, Amount (NOK), VAT (NOK)", m.totals[2], m.lineHeader[6], m.vatHeader[2])
	}
	same := anInvoice()
	same.deliveryPlace = &pdfParty{line1: "kundeveien 2", postalCode: "0150", city: "OSLO", country: "NO"}
	for _, kv := range buildPDFModel(same).meta {
		if kv[0] == "Leveringssted" {
			t.Error("the buyer's own address printed as the place of delivery")
		}
	}
}

// A credit note names its original and has no due date or payment block; a
// preview carries the watermark and no number.
func TestPDFModel_ACreditNoteAndAPreview(t *testing.T) {
	t.Parallel()
	c := anInvoice()
	c.kind = kindCreditNote
	c.credits = &struct {
		number    int64
		issueDate time.Time
	}{999, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
	m := buildPDFModel(c)
	if m.title != "Kreditnota" || m.creditsLine != "Kreditnota til faktura 999 av 01.09.2026" {
		t.Errorf("credit note = %q, %q", m.title, m.creditsLine)
	}
	if len(m.payment) != 0 || m.paymentNote != "" || m.totals[2][0] != "Sum" {
		t.Errorf("a credit note's payment = %q %q, gross %q; want none and Sum", m.payment, m.paymentNote, m.totals[2])
	}
	for _, kv := range m.meta {
		if kv[0] == "Forfallsdato" || kv[0] == "Betalingsbetingelser" {
			t.Errorf("a credit note prints %q", kv[0])
		}
	}

	p := anInvoice()
	p.preview, p.number = true, nil
	pm := buildPDFModel(p)
	if pm.watermark != "UTKAST — ikke et salgsdokument" || pm.meta[0][0] == "Nummer" {
		t.Errorf("preview = watermark %q, first meta %q", pm.watermark, pm.meta[0])
	}
	// A preview of an incomplete seller prints no bare "Org.nr.", and an
	// empty place of delivery no bare "Leveringssted".
	p.seller.organisationNumber, p.deliveryPlace = "", &pdfParty{}
	pm = buildPDFModel(p)
	for _, line := range pm.seller {
		if strings.HasPrefix(line, "Org.nr.") {
			t.Errorf("an incomplete seller prints %q", line)
		}
	}
	for _, kv := range pm.meta {
		if kv[0] == "Leveringssted" {
			t.Errorf("an empty place of delivery prints %q", kv)
		}
	}
}

// The same document renders to the same bytes, in-process, twice: catalog
// sorting and the fixed modification date are set (D7). The bytes may change
// with an upgrade of maroto, gofpdf or the font; stored PDFs never do.
func TestRenderPDF_IsReproducible(t *testing.T) {
	t.Parallel()
	first, err := renderPDF(buildPDFModel(anInvoice()))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // a second later, so a wall-clock date would differ
	second, err := renderPDF(buildPDFModel(anInvoice()))
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two renders of one document differ")
	}
	if !bytes.HasPrefix(first, []byte("%PDF-")) {
		t.Errorf("not a PDF: %q", first[:8])
	}
	for _, want := range []string{"/CreationDate (D:20260912103000", "/ModDate (D:20260101000000"} {
		if !strings.Contains(string(first), want) {
			t.Errorf("the PDF has no %s", want)
		}
	}
	// One fixed render, pinned: a change here means every re-render of an
	// unstored document would hash differently from a render before it. The
	// pin changes on an upgrade of maroto, gofpdf or the font, and on any
	// change to what the page says — update it in that commit; stored PDFs are
	// never re-rendered.
	sum := sha256.Sum256(first)
	if got := hex.EncodeToString(sum[:]); got != pinnedRenderSHA256 {
		t.Errorf("the fixed render's SHA-256 = %s, want %s", got, pinnedRenderSHA256)
	}
}

const pinnedRenderSHA256 = "d1e3dada3c358bf3fd4b8d2d2704a1c15a908ab0ddb36fc0a3646191b6d1a868"

// A download or a send whose store-once path fails answers a 503 only for
// the object store's failure: a document that cannot be rendered, or a
// database that fails, is a 500 — retrying does not mend it.
func TestStoreOnceFailed_OnlyTheObjectStoreIsA503(t *testing.T) {
	t.Parallel()
	p := storeOnceFailed(fmt.Errorf("%w: %w", errObjectStore, errors.New("disk full")))
	if p.unavailable == nil || p.unavailable.Code == nil || *p.unavailable.Code != codeStorageUnavailable || p.err != nil {
		t.Errorf("an object-store failure = %+v; want the 503", p)
	}
	render := fmt.Errorf("invoices: render document 7: %w", errors.New("font"))
	if p := storeOnceFailed(render); p.unavailable != nil || p.broken || !errors.Is(p.err, render) {
		t.Errorf("a render failure = %+v; want the error, which the server answers with a 500", p)
	}
}

// An issued document's (and a preview's) PDF is in the document's own
// currency: pdfDocumentOf carries it from the row.
func TestPDFDocumentOf_CarriesTheCurrency(t *testing.T) {
	t.Parallel()
	number := int64(7)
	zero, err := numericFromRat(new(big.Rat), 2)
	if err != nil {
		t.Fatal(err)
	}
	d, err := pdfDocumentOf(store.InvoicesInvoice{
		Kind: kindInvoice, Currency: "SEK", Number: &number, IssueDate: pgDate(time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)),
		NetTotal: zero, VatTotal: zero, GrossTotal: zero,
	}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("pdfDocumentOf: %v", err)
	}
	if m := buildPDFModel(d); d.currency != "SEK" || m.lineHeader[6] != "Beløp (SEK)" {
		t.Errorf("currency = %q, header %q; want SEK throughout", d.currency, m.lineHeader[6])
	}
}

// The payment block's KID line (D3), in each language, and the note asking
// for it in place of the invoice number; none without a KID. pdfDocumentOf
// verifies the stored KID against the stored algorithm and the number, and
// refuses one that does not verify, or a half of the pair.
func TestPDFModel_TheKIDLine(t *testing.T) {
	t.Parallel()
	d := anInvoice()
	d.kid = "0010009"
	m := buildPDFModel(d)
	if want := [][2]string{{"Kontonummer", "86011117947"}, {"KID", "0010009"}, {"IBAN", "NO9386011117947"}, {"BIC", "DNBANOKKXXX"}, {"Forfallsdato", "26.09.2026"}}; !slices.Equal(m.payment, want) ||
		m.paymentNote != "Vennligst bruk KID ved betaling" {
		t.Errorf("nb payment = %q %q", m.payment, m.paymentNote)
	}
	d.language = "en"
	if m := buildPDFModel(d); m.payment[1] != [2]string{"KID", "0010009"} || m.paymentNote != "Please use the KID with your payment" {
		t.Errorf("en payment = %q %q", m.payment, m.paymentNote)
	}
	if m := buildPDFModel(anInvoice()); slices.ContainsFunc(m.payment, func(kv [2]string) bool { return kv[0] == "KID" }) {
		t.Errorf("without a KID the payment block = %q", m.payment)
	}

	number := int64(1000)
	row := func(kid, algorithm string) store.InvoicesInvoice {
		zero, err := numericFromRat(new(big.Rat), 2)
		if err != nil {
			t.Fatal(err)
		}
		inv := store.InvoicesInvoice{Kind: kindInvoice, Status: statusIssued, Number: &number, Currency: "NOK",
			NetTotal: zero, VatTotal: zero, GrossTotal: zero}
		if kid != "" {
			inv.Kid = &kid
		}
		if algorithm != "" {
			inv.KidAlgorithm = &algorithm
		}
		return inv
	}
	if d, err := pdfDocumentOf(row("0010009", "mod10"), nil, nil, nil, nil); err != nil || d.kid != "0010009" {
		t.Errorf("a KID that verifies = %q, %v; want it carried", d.kid, err)
	}
	for _, c := range [][2]string{{"0010009", "mod11"}, {"0010005", "mod10"}, {"0010009", ""}, {"", "mod10"}} {
		if _, err := pdfDocumentOf(row(c[0], c[1]), nil, nil, nil, nil); err == nil {
			t.Errorf("kid %q algorithm %q: no error, want one", c[0], c[1])
		}
	}
}

// The project the document's work belongs to prints in the meta block after
// the references — "Prosjekt" / "Project" and the reference — from the
// document's own row (invoices work design D9), on an issued document and a
// preview alike; a document that names none prints no line.
func TestPDF_TheProjectLine(t *testing.T) {
	t.Parallel()
	number, reference, project := int64(7), "P-41", int32(41)
	zero, err := numericFromRat(new(big.Rat), 2)
	if err != nil {
		t.Fatal(err)
	}
	d, err := pdfDocumentOf(store.InvoicesInvoice{
		Kind: kindInvoice, Currency: "NOK", Number: &number, IssueDate: pgDate(time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)),
		OrderReference: "PO-9", ProjectID: &project, ProjectReference: &reference,
		NetTotal: zero, VatTotal: zero, GrossTotal: zero,
	}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("pdfDocumentOf: %v", err)
	}
	m := buildPDFModel(d)
	if last := m.meta[len(m.meta)-1]; last != [2]string{"Prosjekt", "P-41"} || m.meta[len(m.meta)-2] != [2]string{"Ordrereferanse", "PO-9"} {
		t.Errorf("nb meta = %q, want Prosjekt P-41 after the order reference", m.meta)
	}
	d.language = "en"
	if m := buildPDFModel(d); m.meta[len(m.meta)-1] != [2]string{"Project", "P-41"} {
		t.Errorf("en meta = %q, want Project P-41 last", m.meta)
	}
	if m := buildPDFModel(anInvoice()); slices.ContainsFunc(m.meta, func(kv [2]string) bool { return kv[0] == "Prosjekt" }) {
		t.Errorf("without a project the meta = %q", m.meta)
	}
}
