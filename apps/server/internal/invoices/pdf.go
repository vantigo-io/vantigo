package invoices

import (
	_ "embed"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/phpdave11/gofpdf"
)

// This file is the PDF (D7): a document's model — every word it prints, in
// the order it prints them — and the renderer that lays the model out with
// maroto v2 (pure Go; the runtime image has no fonts, so the font is embedded).
//
// What makes a stored PDF lawful is store-once (pdfstore.go), not determinism.
// Reproducible bytes are a nice-to-have, and the renderer gets them where the
// libraries let it: gofpdf would write /ModDate as time.Now() and font objects
// in Go map order, and maroto exposes neither, so init sets gofpdf's two
// process-global defaults — catalog sorting on, a fixed modification date —
// as constants, never per-document values. The creation date is the
// document's own issued_at. Rendering is sequential, never concurrent.
//
// There is no PDF text extractor in this module's dependencies that reads an
// embedded-subset font back into words, so the tests assert on the model
// (pdfModel) for what a document says, and on the bytes only for
// reproducibility.

// The font: Noto Sans Regular and Bold (SIL Open Font License 1.1, whose
// text is fonts/LICENSE), for æøå and the en dash in the reverse-charge text.
var (
	//go:embed fonts/NotoSans-Regular.ttf
	notoSansRegular []byte
	//go:embed fonts/NotoSans-Bold.ttf
	notoSansBold []byte
)

const fontFamily = "noto-sans"

// pdfModificationDate is the fixed /ModDate of every PDF (see above).
var pdfModificationDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func init() {
	gofpdf.SetDefaultCatalogSort(true)
	gofpdf.SetDefaultModificationDate(pdfModificationDate)
}

// reverseChargeText is the regulation's own wording (§ 5-1-1), printed in
// Norwegian whatever the document's language.
const reverseChargeText = "Omvendt avgiftsplikt – Merverdiavgift ikke beregnet"

// pdfLabels are a document's fixed words in one language (§ 5-1-1a allows
// English).
type pdfLabels struct {
	invoice, creditNote, number, issueDate, deliveryDate, deliveryPeriod, deliveryPlace string
	dueDate, terms, termsDays, yourRef, ourRef, orderRef, project, creditsFor           string
	description, quantity, unit, unitPrice, discount, vat, amount                       string
	vatBasis, vatAmount, vatCategory, net, vatTotal, gross, toPay                       string
	payment, account, iban, bic, payWithNumber, orgNumber, foreignID, watermark         string
	kid, payWithKid                                                                     string
}

var labels = map[string]pdfLabels{
	"nb": {
		invoice: "Faktura", creditNote: "Kreditnota", number: "Nummer", issueDate: "Fakturadato",
		deliveryDate: "Leveringsdato", deliveryPeriod: "Leveringsperiode", deliveryPlace: "Leveringssted",
		dueDate: "Forfallsdato", terms: "Betalingsbetingelser", termsDays: "%d dager",
		yourRef: "Deres ref.", ourRef: "Vår ref.", orderRef: "Ordrereferanse", project: "Prosjekt", creditsFor: "Kreditnota til faktura %d av %s",
		description: "Beskrivelse", quantity: "Antall", unit: "Enhet", unitPrice: "Enhetspris", discount: "Rabatt %",
		vat: "MVA %", amount: "Beløp", vatBasis: "Grunnlag", vatAmount: "MVA", vatCategory: "MVA-sats",
		net: "Sum eks. MVA", vatTotal: "MVA", gross: "Sum", toPay: "Å betale",
		payment: "Betaling", account: "Kontonummer", iban: "IBAN", bic: "BIC",
		payWithNumber: "Vennligst oppgi fakturanummer ved betaling", orgNumber: "Org.nr.", foreignID: "VAT/Reg. no.",
		watermark: "UTKAST — ikke et salgsdokument",
		kid:       "KID", payWithKid: "Vennligst bruk KID ved betaling",
	},
	"en": {
		invoice: "Invoice", creditNote: "Credit note", number: "Number", issueDate: "Invoice date",
		deliveryDate: "Delivery date", deliveryPeriod: "Delivery period", deliveryPlace: "Place of delivery",
		dueDate: "Due date", terms: "Payment terms", termsDays: "%d days",
		yourRef: "Your ref.", ourRef: "Our ref.", orderRef: "Order reference", project: "Project", creditsFor: "Credit note for invoice %d of %s",
		description: "Description", quantity: "Quantity", unit: "Unit", unitPrice: "Unit price", discount: "Discount %",
		vat: "VAT %", amount: "Amount", vatBasis: "Basis", vatAmount: "VAT", vatCategory: "VAT rate",
		net: "Total excl. VAT", vatTotal: "VAT", gross: "Total", toPay: "Amount due",
		payment: "Payment", account: "Account number", iban: "IBAN", bic: "BIC",
		payWithNumber: "Please state the invoice number with your payment", orgNumber: "Org. no.", foreignID: "VAT/Reg. no.",
		watermark: "UTKAST — ikke et salgsdokument",
		kid:       "KID", payWithKid: "Please use the KID with your payment",
	},
}

// pdfParty is a seller or a buyer as a document prints it.
type pdfParty struct {
	name, line1, line2, postalCode, city, country string
	organisationNumber, foreignID, email          string
	vatRegistered, foretaksregisteret             bool
}

// pdfLine is one line as a document prints it.
type pdfLine struct {
	description, unit                   string
	quantity, unitPrice, discount, rate *big.Rat
	net                                 *big.Rat
}

// pdfDocument is everything a PDF is rendered from — for an issued document,
// only its own rows and snapshots (pdfDocumentOf); for a draft's preview, what
// the draft would be if issued today (preview in pdfstore.go).
type pdfDocument struct {
	kind, language                         string
	currency                               string // the document's, ISO 4217
	number                                 *int64
	issueDate                              time.Time
	dueDate                                *time.Time
	paymentTermsDays                       *int32
	deliveryDate, deliveryFrom, deliveryTo *time.Time
	deliveryPlace                          *pdfParty
	yourReference, ourReference, orderRef  string
	// projectRef is the project the work belongs to, as the document took
	// it (invoices work design D9); empty for none.
	projectRef             string
	note, footer           string
	seller, buyer          pdfParty
	bankAccount, iban, bic string
	// kid is an issued invoice's KID, verified against its algorithm
	// (pdfDocumentOf); empty without one (EHF and KID design D3).
	kid       string
	lines     []pdfLine
	summaries []vatSummary
	totals    documentTotals
	credits   *struct {
		number    int64
		issueDate time.Time
	}
	created time.Time
	preview bool
}

// pdfModel is every word a document prints, in the order it prints them: what
// the tests read, and all the renderer lays out.
type pdfModel struct {
	watermark    string
	title        string
	seller       []string
	buyer        []string
	meta         [][2]string
	creditsLine  string
	lineHeader   []string
	lines        [][]string
	vatHeader    []string
	vatRows      [][]string
	reasons      []string
	totals       [][2]string
	payment      [][2]string
	paymentNote  string
	note, footer string
	created      time.Time
}

// groupDigits writes an integer part with sep between thousands.
func groupDigits(digits, sep string) string {
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// formatDecimal is v in the document's language — "1 234,50" in Norwegian,
// "1,234.50" in English — with at least minPlaces and at most maxPlaces
// decimals, trailing zeros beyond minPlaces dropped.
func formatDecimal(v *big.Rat, minPlaces, maxPlaces int, language string) string {
	text := v.FloatString(maxPlaces)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	whole, frac, _ := strings.Cut(text, ".")
	for len(frac) > minPlaces && strings.HasSuffix(frac, "0") {
		frac = frac[:len(frac)-1]
	}
	thousands, point := " ", ","
	if language == "en" {
		thousands, point = ",", "."
	}
	out := groupDigits(whole, thousands)
	if frac != "" {
		out += point + frac
	}
	if negative {
		out = "-" + out
	}
	return out
}

func money(v *big.Rat, language string) string { return formatDecimal(v, 2, 2, language) }

// formatDate is a date in the document's language.
func formatDate(d time.Time, language string) string {
	if language == "en" {
		return d.Format(time.DateOnly)
	}
	return d.Format("02.01.2006")
}

// organisationNumber prints nine digits in groups of three.
func organisationNumber(n string) string {
	if len(n) != 9 {
		return n
	}
	return n[0:3] + " " + n[3:6] + " " + n[6:9]
}

// partyLines are a party's address lines.
func partyLines(p pdfParty) []string {
	out := []string{p.name}
	for _, l := range []string{p.line1, p.line2} {
		if l != "" {
			out = append(out, l)
		}
	}
	if place := strings.TrimSpace(p.postalCode + " " + p.city); place != "" {
		out = append(out, place)
	}
	if p.country != "" && p.country != "NO" {
		out = append(out, p.country)
	}
	return out
}

// sameAddress reports whether a place of delivery is the buyer's own address,
// which is then not printed (D4).
func sameAddress(a, b pdfParty) bool {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return norm(a.line1) == norm(b.line1) && norm(a.line2) == norm(b.line2) &&
		norm(a.postalCode) == norm(b.postalCode) && norm(a.city) == norm(b.city) && norm(a.country) == norm(b.country)
}

// buildPDFModel is D7's layout as words.
func buildPDFModel(d pdfDocument) pdfModel {
	lang := d.language
	if _, ok := labels[lang]; !ok {
		lang = "nb"
	}
	l := labels[lang]
	m := pdfModel{title: l.invoice, created: d.created}
	if d.kind == kindCreditNote {
		m.title = l.creditNote
	}
	if d.preview {
		m.watermark = l.watermark
	}

	// The seller: name, address, the organisation number followed by MVA
	// when VAT-registered, Foretaksregisteret when registered there (§ 5-1-2).
	// Only a preview of an incomplete seller lacks the number, and then
	// prints no bare label.
	m.seller = partyLines(d.seller)
	if d.seller.organisationNumber != "" {
		orgLine := l.orgNumber + " " + organisationNumber(d.seller.organisationNumber)
		if d.seller.vatRegistered {
			orgLine += " MVA"
		}
		m.seller = append(m.seller, orgLine)
	}
	if d.seller.foretaksregisteret {
		m.seller = append(m.seller, "Foretaksregisteret")
	}
	if d.seller.email != "" {
		m.seller = append(m.seller, d.seller.email)
	}

	// The buyer: name, address, and the organisation number or the foreign id.
	m.buyer = partyLines(d.buyer)
	switch {
	case d.buyer.organisationNumber != "":
		m.buyer = append(m.buyer, l.orgNumber+" "+organisationNumber(d.buyer.organisationNumber))
	case d.buyer.foreignID != "":
		m.buyer = append(m.buyer, l.foreignID+" "+d.buyer.foreignID)
	}

	// The meta block.
	if d.number != nil {
		m.meta = append(m.meta, [2]string{l.number, fmt.Sprint(*d.number)})
	}
	m.meta = append(m.meta, [2]string{l.issueDate, formatDate(d.issueDate, lang)})
	switch {
	case d.deliveryDate != nil:
		m.meta = append(m.meta, [2]string{l.deliveryDate, formatDate(*d.deliveryDate, lang)})
	case d.deliveryFrom != nil && d.deliveryTo != nil:
		m.meta = append(m.meta, [2]string{l.deliveryPeriod, formatDate(*d.deliveryFrom, lang) + " – " + formatDate(*d.deliveryTo, lang)})
	}
	if d.deliveryPlace != nil && !sameAddress(*d.deliveryPlace, d.buyer) {
		if place := strings.Join(partyLines(*d.deliveryPlace)[1:], ", "); place != "" {
			m.meta = append(m.meta, [2]string{l.deliveryPlace, place})
		}
	}
	if d.kind == kindInvoice {
		if d.dueDate != nil {
			m.meta = append(m.meta, [2]string{l.dueDate, formatDate(*d.dueDate, lang)})
		}
		if d.paymentTermsDays != nil {
			m.meta = append(m.meta, [2]string{l.terms, fmt.Sprintf(l.termsDays, *d.paymentTermsDays)})
		}
	}
	for _, ref := range [][2]string{{l.yourRef, d.yourReference}, {l.ourRef, d.ourReference}, {l.orderRef, d.orderRef}, {l.project, d.projectRef}} {
		if ref[1] != "" {
			m.meta = append(m.meta, ref)
		}
	}
	if d.credits != nil {
		m.creditsLine = fmt.Sprintf(l.creditsFor, d.credits.number, formatDate(d.credits.issueDate, lang))
	}

	// The currency, stated once where it binds every amount: the line
	// amounts' and the VAT's headers and the amount to pay (§ 5-1-1 nr. 6).
	// Stored PDFs are never re-rendered, so a page without it would stay
	// without it for the whole retention period.
	currency := d.currency
	if currency == "" {
		currency = "NOK" // the column's default; never printed as "()"
	}
	inCurrency := func(label string) string { return label + " (" + currency + ")" }

	// The lines.
	m.lineHeader = []string{l.description, l.quantity, l.unit, l.unitPrice, l.discount, l.vat, inCurrency(l.amount)}
	for _, line := range d.lines {
		m.lines = append(m.lines, []string{
			line.description, formatDecimal(line.quantity, 0, 3, lang), line.unit,
			formatDecimal(line.unitPrice, 2, 4, lang), formatDecimal(line.discount, 0, 2, lang),
			formatDecimal(line.rate, 0, 2, lang), money(line.net, lang),
		})
	}

	// VAT per (category, rate), 0 % categories included, and under it each
	// non-S category's reason — the reverse-charge text in Norwegian always.
	m.vatHeader = []string{l.vatCategory, l.vatBasis, inCurrency(l.vatAmount)}
	seen := map[string]bool{}
	for _, r := range d.summaries {
		m.vatRows = append(m.vatRows, []string{
			r.category + " " + formatDecimal(r.rate, 0, 2, lang) + " %", money(r.taxable, lang), money(r.vat, lang),
		})
		if r.category == "S" || r.reason == nil || seen[*r.reason] {
			continue
		}
		seen[*r.reason] = true
		reason := *r.reason
		if r.category == "AE" {
			reason = reverseChargeText
		}
		m.reasons = append(m.reasons, reason)
	}

	grossLabel := l.gross
	if d.kind == kindInvoice {
		grossLabel = l.toPay
	}
	m.totals = [][2]string{
		{l.net, money(d.totals.net, lang)}, {l.vatTotal, money(d.totals.vat, lang)}, {grossLabel, currency + " " + money(d.totals.gross, lang)},
	}

	// The payment block, on an invoice only.
	if d.kind == kindInvoice {
		m.payment = append(m.payment, [2]string{l.account, d.bankAccount})
		if d.kid != "" {
			m.payment = append(m.payment, [2]string{l.kid, d.kid})
		}
		if d.iban != "" {
			m.payment = append(m.payment, [2]string{l.iban, d.iban})
		}
		if d.bic != "" {
			m.payment = append(m.payment, [2]string{l.bic, d.bic})
		}
		if d.dueDate != nil {
			m.payment = append(m.payment, [2]string{l.dueDate, formatDate(*d.dueDate, lang)})
		}
		m.paymentNote = l.payWithNumber
		if d.kid != "" {
			m.paymentNote = l.payWithKid
		}
	}
	m.note, m.footer = d.note, d.footer
	return m
}

// renderPDF lays a model out on A4 and answers the bytes.
func renderPDF(m pdfModel) ([]byte, error) {
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(fontFamily, fontstyle.Normal, notoSansRegular).
		AddUTF8FontFromBytes(fontFamily, fontstyle.Bold, notoSansBold).
		Load()
	if err != nil {
		return nil, fmt.Errorf("invoices: load the PDF font: %w", err)
	}
	cfg := config.NewBuilder().
		WithCustomFonts(fonts).
		WithDefaultFont(&props.Font{Family: fontFamily, Size: 9}).
		WithSequentialMode().
		WithLeftMargin(15).WithRightMargin(15).WithTopMargin(15).
		WithCreationDate(m.created).
		WithTitle(m.title, true).
		Build()
	doc := maroto.New(cfg)
	bold := props.Text{Style: fontstyle.Bold}
	right := props.Text{Align: align.Right}
	boldRight := props.Text{Style: fontstyle.Bold, Align: align.Right}

	if m.watermark != "" {
		doc.AddRows(text.NewRow(10, m.watermark, props.Text{Style: fontstyle.Bold, Size: 14, Align: align.Center,
			Color: &props.Color{Red: 200, Green: 30, Blue: 30}}))
	}
	doc.AddRows(text.NewRow(12, m.title, props.Text{Style: fontstyle.Bold, Size: 18}))

	// The parties, side by side.
	rows := max(len(m.seller), len(m.buyer))
	for i := range rows {
		cell := func(lines []string) string {
			if i < len(lines) {
				return lines[i]
			}
			return ""
		}
		style := props.Text{}
		if i == 0 {
			style = bold
		}
		doc.AddRow(4.5, text.NewCol(6, cell(m.seller), style), text.NewCol(6, cell(m.buyer), style))
	}
	doc.AddRows(line.NewRow(4))

	for _, kv := range m.meta {
		doc.AddRow(4.5, text.NewCol(3, kv[0], bold), text.NewCol(9, kv[1]))
	}
	if m.creditsLine != "" {
		doc.AddRows(text.NewRow(6, m.creditsLine, props.Text{Style: fontstyle.Bold, Top: 1}))
	}
	doc.AddRows(line.NewRow(4))

	// The line table: description, quantity, unit, unit price, discount, VAT, net.
	sizes := []int{4, 1, 1, 2, 1, 1, 2}
	tableRow := func(cells []string, header bool) core.Row {
		cols := make([]core.Col, 0, len(cells))
		for i, c := range cells {
			style := props.Text{}
			if i > 0 && i != 2 {
				style.Align = align.Right
			}
			if header {
				style.Style = fontstyle.Bold
			}
			cols = append(cols, text.NewCol(sizes[i], c, style))
		}
		return doc.AddAutoRow(cols...)
	}
	tableRow(m.lineHeader, true)
	for _, l := range m.lines {
		tableRow(l, false)
	}
	doc.AddRows(line.NewRow(4))

	// VAT per rate, then the reasons, then the totals.
	doc.AddRow(4.5, col.New(6), text.NewCol(2, m.vatHeader[0], bold), text.NewCol(2, m.vatHeader[1], boldRight), text.NewCol(2, m.vatHeader[2], boldRight))
	for _, r := range m.vatRows {
		doc.AddRow(4.5, col.New(6), text.NewCol(2, r[0]), text.NewCol(2, r[1], right), text.NewCol(2, r[2], right))
	}
	for _, reason := range m.reasons {
		doc.AddAutoRow(text.NewCol(12, reason, props.Text{Top: 1}))
	}
	doc.AddRows(line.NewRow(4))
	for i, kv := range m.totals {
		style := right
		if i == len(m.totals)-1 {
			style = boldRight
		}
		doc.AddRow(5, col.New(6), text.NewCol(3, kv[0], style), text.NewCol(3, kv[1], style))
	}

	if len(m.payment) > 0 {
		doc.AddRows(line.NewRow(4))
		for _, kv := range m.payment {
			doc.AddRow(4.5, text.NewCol(3, kv[0], bold), text.NewCol(9, kv[1]))
		}
		doc.AddRows(text.NewRow(6, m.paymentNote, props.Text{Top: 1}))
	}
	if m.note != "" {
		doc.AddAutoRow(text.NewCol(12, m.note, props.Text{Top: 3}))
	}
	if m.footer != "" {
		doc.AddAutoRow(text.NewCol(12, m.footer, props.Text{Top: 3, Size: 8}))
	}

	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("invoices: render the PDF: %w", err)
	}
	return out.GetBytes(), nil
}
