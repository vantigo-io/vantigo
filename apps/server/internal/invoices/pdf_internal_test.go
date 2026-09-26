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

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
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
		kind: kindInvoice, language: "nb", number: &number, issueDate: issued, dueDate: &due, paymentTermsDays: &terms,
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
	if m.totals[2] != [2]string{"Å betale", "15 045,00"} {
		t.Errorf("gross = %q, want Å betale 15 045,00", m.totals[2])
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
	// pin may change on an upgrade of maroto, gofpdf or the font — update it
	// then, in the upgrade's commit; stored PDFs are never re-rendered.
	sum := sha256.Sum256(first)
	if got := hex.EncodeToString(sum[:]); got != pinnedRenderSHA256 {
		t.Errorf("the fixed render's SHA-256 = %s, want %s", got, pinnedRenderSHA256)
	}
}

const pinnedRenderSHA256 = "0a4817fc89b7841163410f204a030cce25705faba105f05375553372a7d18830"

// A download whose store-once path fails answers a 503 only for the object
// store's failure: a document that cannot be rendered, or a database that
// fails, is a 500 — retrying does not mend it.
func TestStoreOnceFailed_OnlyTheObjectStoreIsA503(t *testing.T) {
	t.Parallel()
	res, err := storeOnceFailed(fmt.Errorf("%w: %w", errObjectStore, errors.New("disk full")))
	if _, ok := res.(gen.GetInvoicesByIdPdf503ApplicationProblemPlusJSONResponse); !ok || err != nil {
		t.Errorf("an object-store failure = %T, %v; want the 503", res, err)
	}
	render := fmt.Errorf("invoices: render document 7: %w", errors.New("font"))
	if res, err := storeOnceFailed(render); res != nil || !errors.Is(err, render) {
		t.Errorf("a render failure = %T, %v; want the error, which the server answers with a 500", res, err)
	}
}
