package orders

import (
	_ "embed" // the PDF font
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

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is the PDFs (O24): the quote, the order confirmation
// (ordrebekreftelse) and the pro forma print of either, laid out with maroto
// v2 as Invoices' PDFs are (internal/invoices/pdf.go), with Orders' own
// embedded Noto Sans. A document's model — every word it prints, in order —
// is built first and is what the tests read; the renderer lays it out.

// The font: Noto Sans Regular and Bold (SIL Open Font License 1.1, whose text
// is fonts/LICENSE), for æøå and the en dash.
var (
	//go:embed fonts/NotoSans-Regular.ttf
	notoSansRegular []byte
	//go:embed fonts/NotoSans-Bold.ttf
	notoSansBold []byte
)

const fontFamily = "noto-sans"

// pdfModificationDate is the fixed /ModDate of every PDF, as Invoices': gofpdf
// would write time.Now() otherwise.
var pdfModificationDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func init() {
	gofpdf.SetDefaultCatalogSort(true)
	gofpdf.SetDefaultModificationDate(pdfModificationDate)
}

// The kinds of document a PDF prints (O24), as orders.documents stores them.
const (
	docQuote        = "quote"
	docConfirmation = "confirmation"
)

// pdfLabels are a document's fixed words in one language.
type pdfLabels struct {
	quote, confirmation, revisedConfirmation, proforma, draft string
	quoteNumber, revision, orderNumber, version               string
	date, validUntil, deliveryDate, deliveryAddress, contact  string
	terms, termsDays, yourRef, ourRef, customerNumber         string
	orgNumber, foreignID                                      string
	description, quantity, unit, unitPrice, unitPriceInclVAT  string
	netUnitPrice, netUnitPriceInclVAT, discount, amount       string
	amountInclVAT, optional, vatRate, vatBasis, vatAmount     string
	net, vat, gross                                           string
}

var pdfLabelSets = map[string]pdfLabels{
	"nb": {
		quote: "Tilbud", confirmation: "Ordrebekreftelse", revisedConfirmation: "Revidert ordrebekreftelse",
		proforma: "Proforma – ikke en faktura", draft: "UTKAST",
		quoteNumber: "Tilbud %d", revision: "revisjon %d", orderNumber: "Ordre %d", version: "versjon %d",
		date: "Dato", validUntil: "Gyldig til", deliveryDate: "Leveringsdato", deliveryAddress: "Leveringsadresse",
		contact: "Kontakt", terms: "Betalingsbetingelser", termsDays: "%d dager", yourRef: "Deres ref.", ourRef: "Vår ref.",
		customerNumber: "Kundenr.", orgNumber: "Org.nr.", foreignID: "VAT/Reg. no.",
		description: "Beskrivelse", quantity: "Antall", unit: "Enhet", unitPrice: "Pris", unitPriceInclVAT: "Pris inkl. mva",
		netUnitPrice: "Nettopris", netUnitPriceInclVAT: "Nettopris inkl. mva", discount: "Rabatt %",
		amount: "Beløp", amountInclVAT: "Beløp inkl. mva", optional: "Valgfritt",
		vatRate: "MVA-sats", vatBasis: "Grunnlag", vatAmount: "MVA", net: "Sum eks. mva", vat: "MVA", gross: "Sum inkl. mva",
	},
	"en": {
		quote: "Quote", confirmation: "Order confirmation", revisedConfirmation: "Revised order confirmation",
		proforma: "Pro forma – not an invoice", draft: "DRAFT",
		quoteNumber: "Quote %d", revision: "revision %d", orderNumber: "Order %d", version: "version %d",
		date: "Date", validUntil: "Valid until", deliveryDate: "Delivery date", deliveryAddress: "Delivery address",
		contact: "Contact", terms: "Payment terms", termsDays: "%d days", yourRef: "Your ref.", ourRef: "Our ref.",
		customerNumber: "Customer no.", orgNumber: "Org. no.", foreignID: "VAT/Reg. no.",
		description: "Description", quantity: "Quantity", unit: "Unit", unitPrice: "Price", unitPriceInclVAT: "Price incl. VAT",
		netUnitPrice: "Net price", netUnitPriceInclVAT: "Net price incl. VAT", discount: "Discount %",
		amount: "Amount", amountInclVAT: "Amount incl. VAT", optional: "Optional",
		vatRate: "VAT rate", vatBasis: "Basis", vatAmount: "VAT", net: "Total excl. VAT", vat: "VAT", gross: "Total incl. VAT",
	},
}

// printable is everything a quote's or an order's PDF is printed from: the
// document's own rows and snapshots, and the seller as Invoices holds it.
type printable struct {
	kind     string // docQuote or docConfirmation
	proforma bool
	// draft prints the draft watermark: a quote not yet sent, an order not
	// yet confirmed, printed on demand and never stored.
	draft bool
	// revised prints a confirmation as revised, with its version (O26).
	revised  bool
	number   int64
	version  int32 // a quote's revision, an order's version
	date     time.Time
	header   header
	valid    *time.Time
	lines    []store.OrdersLine
	seller   contracts.SellerProfile
	language string
}

// printableOfQuote and printableOfOrder are a stored document as its PDF
// prints it. date is the document's date: when it was sent or confirmed, or
// now for a draft.
func printableOfQuote(q store.OrdersQuote, lines []store.OrdersLine, seller contracts.SellerProfile, date time.Time) printable {
	p := printable{
		kind: docQuote, draft: q.Status == quoteDraft, number: q.Number, version: q.Revision, date: date,
		header: headerOfQuote(q), lines: lines, seller: seller, language: q.Language,
	}
	if q.ValidUntil.Valid {
		v := q.ValidUntil.Time
		p.valid = &v
	}
	return p
}

func printableOfOrder(o store.OrdersOrder, lines []store.OrdersLine, seller contracts.SellerProfile, date time.Time) printable {
	return printable{
		kind: docConfirmation, draft: o.Status == orderDraft, revised: o.Version > 1 && o.Status != orderDraft,
		number: o.Number, version: o.Version, date: date, header: headerOfOrder(o), lines: lines, seller: seller,
		language: o.Language,
	}
}

// pdfRow is one printed line of the line table: a product's cells, a
// section's title or a note's text.
type pdfRow struct {
	kind  string
	cells []string
}

// pdfModel is every word a document prints, in the order it prints them.
type pdfModel struct {
	watermark     string
	title         string
	subtitle      string
	seller        []string
	buyer         []string
	meta          [][2]string
	intro         string
	header        []string
	rows          []pdfRow
	optionalTitle string
	optionalRows  []pdfRow
	vatHeader     []string
	vatRows       [][]string
	totals        [][2]string
	closing       string
	footer        string
	created       time.Time
	// codes are a packing slip's shipments (O52): each consignment number
	// printed as Code 128 beside its tracking URL as a QR code.
	codes []pdfCode
}

// pdfCode is one shipment's codes on a packing slip.
type pdfCode struct{ label, barcode, qr string }

// pdfModelBuilt, when a test sets it, hears every model built: the PDF's text
// is embedded-subset and cannot be read back from the bytes.
var pdfModelBuilt func(m pdfModel)

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
// decimals.
func formatDecimal(v *big.Rat, minPlaces, maxPlaces int, language string) string {
	text := v.FloatString(maxPlaces)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	whole, frac, _ := strings.Cut(text, ".")
	for len(frac) > minPlaces && strings.HasSuffix(frac, "0") {
		frac = frac[:len(frac)-1]
	}
	thousands, point := " ", ","
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

// addressLines are an address's printed lines.
func addressLines(name string, line1, line2, postalCode, city, country *string) []string {
	var out []string
	if name != "" {
		out = append(out, name)
	}
	for _, l := range []*string{line1, line2} {
		if l != nil && *l != "" {
			out = append(out, *l)
		}
	}
	if place := strings.TrimSpace(deref(postalCode) + " " + deref(city)); place != "" {
		out = append(out, place)
	}
	if c := deref(country); c != "" && c != "NO" {
		out = append(out, c)
	}
	return out
}

// buildPDFModel is a document as words (O24): the seller, the customer's
// snapshot, the references, the lines with their sections, notes and the
// optional block in the discount's mode (O22) and the VAT mode (O19), VAT per
// rate, the totals, the payment terms, a quote's valid-until and the texts.
func buildPDFModel(d printable) pdfModel {
	lang := d.language
	if _, ok := pdfLabelSets[lang]; !ok {
		lang = "nb"
	}
	l := pdfLabelSets[lang]
	h := d.header
	m := pdfModel{created: d.date}

	switch {
	case d.proforma:
		m.title = l.proforma
	case d.kind == docQuote:
		m.title = l.quote
	case d.revised:
		m.title = l.revisedConfirmation
	default:
		m.title = l.confirmation
	}
	if d.draft && !d.proforma {
		m.watermark = l.draft
	}
	if d.kind == docQuote {
		m.subtitle = fmt.Sprintf(l.quoteNumber, d.number)
		if d.version > 1 {
			m.subtitle += " – " + fmt.Sprintf(l.revision, d.version)
		}
	} else {
		m.subtitle = fmt.Sprintf(l.orderNumber, d.number)
		if d.revised {
			m.subtitle += " – " + fmt.Sprintf(l.version, d.version)
		}
	}

	// The parties.
	sp := d.seller
	m.seller = addressLines(sp.LegalName, &sp.AddressLine1, &sp.AddressLine2, &sp.PostalCode, &sp.City, ptr(strings.ToUpper(sp.Country)))
	if sp.OrganisationNumber != "" {
		org := l.orgNumber + " " + organisationNumber(sp.OrganisationNumber)
		if sp.VATRegistered {
			org += " MVA"
		}
		m.seller = append(m.seller, org)
	}
	if sp.InForetaksregisteret {
		m.seller = append(m.seller, "Foretaksregisteret")
	}
	for _, c := range []string{sp.Email, sp.Phone} {
		if c != "" {
			m.seller = append(m.seller, c)
		}
	}
	m.buyer = addressLines(h.CustomerName, h.InvoiceAddressLine1, h.InvoiceAddressLine2, h.InvoicePostalCode, h.InvoiceCity, h.InvoiceCountry)
	switch {
	case h.CustomerOrganisationNumber != nil:
		m.buyer = append(m.buyer, l.orgNumber+" "+organisationNumber(*h.CustomerOrganisationNumber))
	case h.CustomerForeignID != nil:
		m.buyer = append(m.buyer, l.foreignID+" "+*h.CustomerForeignID)
	}

	// The meta block: the date, a quote's valid-until, the delivery, the
	// contact, the references and the payment terms.
	m.meta = append(m.meta, [2]string{l.date, formatDate(d.date, lang)})
	if d.valid != nil {
		m.meta = append(m.meta, [2]string{l.validUntil, formatDate(*d.valid, lang)})
	}
	if h.DeliveryDate.Valid {
		m.meta = append(m.meta, [2]string{l.deliveryDate, formatDate(h.DeliveryDate.Time, lang)})
	}
	if place := addressLines(deref(h.DeliveryName), h.DeliveryAddressLine1, h.DeliveryAddressLine2, h.DeliveryPostalCode, h.DeliveryCity, h.DeliveryCountry); len(place) > 0 {
		m.meta = append(m.meta, [2]string{l.deliveryAddress, strings.Join(place, ", ")})
	}
	if contact := strings.TrimSpace(strings.Join(nonEmpty(deref(h.ContactName), deref(h.ContactEmail), deref(h.ContactPhone)), ", ")); contact != "" {
		m.meta = append(m.meta, [2]string{l.contact, contact})
	}
	if h.CustomerNumber != nil {
		m.meta = append(m.meta, [2]string{l.customerNumber, fmt.Sprint(*h.CustomerNumber)})
	}
	if h.YourReference != "" {
		m.meta = append(m.meta, [2]string{l.yourRef, h.YourReference})
	}
	if s := deref(h.SalespersonDisplay); s != "" {
		m.meta = append(m.meta, [2]string{l.ourRef, s})
	}
	m.meta = append(m.meta, [2]string{l.terms, fmt.Sprintf(l.termsDays, h.PaymentTermsDays)})
	m.intro, m.closing, m.footer = h.IntroText, h.ClosingText, sp.FooterText

	// The lines (O19, O22). The amounts carry the currency once, in the
	// header, where it binds every amount.
	currency := h.Currency
	inCurrency := func(label string) string { return label + " (" + currency + ")" }
	amountLabel, priceLabel, netPriceLabel := l.amount, l.unitPrice, l.netUnitPrice
	if h.PricesIncludeVat {
		amountLabel, priceLabel, netPriceLabel = l.amountInclVAT, l.unitPriceInclVAT, l.netUnitPriceInclVAT
	}
	if h.DiscountVisible {
		m.header = []string{l.description, l.quantity, l.unit, priceLabel, l.discount, inCurrency(amountLabel)}
	} else {
		m.header = []string{l.description, l.quantity, l.unit, netPriceLabel, inCurrency(amountLabel)}
	}
	for _, ln := range d.lines {
		row := printedLine(ln, h, lang)
		if ln.Optional {
			m.optionalRows = append(m.optionalRows, row)
			continue
		}
		m.rows = append(m.rows, row)
	}
	if len(m.optionalRows) > 0 {
		m.optionalTitle = l.optional
	}

	// VAT per rate and the totals, leaving out the optional lines (O21).
	t := totalsOfRows(d.lines)
	m.vatHeader = []string{l.vatRate, l.vatBasis, inCurrency(l.vatAmount)}
	for _, r := range t.summaries {
		m.vatRows = append(m.vatRows, []string{
			r.Category + " " + formatDecimal(r.Rate, 0, 2, lang) + " %", money(r.Taxable, lang), money(r.VAT, lang),
		})
	}
	m.totals = [][2]string{
		{l.net, money(t.net, lang)}, {l.vat, money(t.vat, lang)}, {l.gross, currency + " " + money(t.gross, lang)},
	}
	if pdfModelBuilt != nil {
		pdfModelBuilt(m)
	}
	return m
}

// printedLine is one line's printed row. A product prints its name, SKU and
// description; its unit price is the list price with the discount shown when
// the discount is visible, else the net unit price — price × (1 − discount)
// rounded to øre (O22) — and both, and the amount, include VAT when the
// document's prices do (O19).
func printedLine(ln store.OrdersLine, h header, lang string) pdfRow {
	switch ln.Kind {
	case kindSection:
		return pdfRow{kind: kindSection, cells: []string{strings.TrimSpace(deref(ln.Name) + " " + ln.Description)}}
	case kindNote:
		return pdfRow{kind: kindNote, cells: []string{strings.TrimSpace(strings.Join(nonEmpty(deref(ln.Name), ln.Description), " – "))}}
	}
	desc := strings.Join(nonEmpty(deref(ln.Name), ln.Description), " – ")
	if sku := deref(ln.Sku); sku != "" {
		desc = sku + " " + desc
	}
	price := ratOrZero(ln.UnitPrice)
	discount := ratOrZero(ln.DiscountPercent)
	amount := ratOrZero(ln.LineNet)
	rate := ratOrZero(ln.VatRatePercent)
	quantity := formatDecimal(ratOrZero(ln.Quantity), 0, 3, lang)
	if packed := packagingText(ln, quantity, lang); packed != "" {
		desc = strings.Join(nonEmpty(desc, packed), " – ")
	}
	if ln.ParentLineID != nil {
		// A kit's child line prints under its kit with no price: the kit
		// carries it (manufacturing phase 2 M145).
		cells := []string{kitIndent + desc, quantity, ln.Unit, "", ""}
		if h.DiscountVisible {
			cells = append(cells, "")
		}
		return pdfRow{kind: kindProduct, cells: cells}
	}
	shown := func(v *big.Rat) *big.Rat {
		if h.PricesIncludeVat {
			return withVAT(v, rate)
		}
		return v
	}
	if h.DiscountVisible {
		return pdfRow{kind: kindProduct, cells: []string{
			desc, quantity, ln.Unit, formatDecimal(shown(price), 2, 4, lang), formatDecimal(discount, 0, 2, lang), money(shown(amount), lang),
		}}
	}
	net := round2(new(big.Rat).Mul(price, new(big.Rat).Sub(big.NewRat(1, 1), new(big.Rat).Quo(discount, hundred))))
	return pdfRow{kind: kindProduct, cells: []string{desc, quantity, ln.Unit, money(shown(net), lang), money(shown(amount), lang)}}
}

// packagingText is how a line entered in a packaging prints it (O15): "3 ×
// Eske à 15 (45 stk)" — the count, the packaging as the line remembers it,
// its size and the quantity in the line's unit — or "" for a line entered in
// its unit.
func packagingText(ln store.OrdersLine, quantity, lang string) string {
	count := ratOf(ln.PackagingQuantity)
	if ln.PackagingName == nil || count == nil || count.Sign() <= 0 {
		return ""
	}
	size := new(big.Rat).Quo(ratOrZero(ln.Quantity), count)
	of := "à"
	if lang == "en" {
		of = "of"
	}
	return fmt.Sprintf("%s × %s %s %s (%s)", formatDecimal(count, 0, 3, lang), *ln.PackagingName, of,
		formatDecimal(size, 0, 3, lang), strings.TrimSpace(quantity+" "+ln.Unit))
}

// nonEmpty is the non-empty strings of in.
func nonEmpty(in ...string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// renderPDF lays a model out on A4 and answers the bytes.
func renderPDF(m pdfModel) ([]byte, error) {
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(fontFamily, fontstyle.Normal, notoSansRegular).
		AddUTF8FontFromBytes(fontFamily, fontstyle.Bold, notoSansBold).
		Load()
	if err != nil {
		return nil, fmt.Errorf("orders: load the PDF font: %w", err)
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
	doc.AddRows(text.NewRow(7, m.subtitle, props.Text{Style: fontstyle.Bold, Size: 11}))

	for i := range max(len(m.seller), len(m.buyer)) {
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
	if m.intro != "" {
		doc.AddAutoRow(text.NewCol(12, m.intro, props.Text{Top: 3}))
	}
	doc.AddRows(line.NewRow(4))

	sizes := []int{5, 1, 1, 2, 1, 2}
	if len(m.header) == 5 {
		sizes = []int{6, 1, 1, 2, 2}
	}
	table := func(r pdfRow, header bool) {
		if r.kind == kindSection || r.kind == kindNote {
			style := props.Text{Top: 1}
			if r.kind == kindSection {
				style.Style = fontstyle.Bold
			}
			doc.AddAutoRow(text.NewCol(12, r.cells[0], style))
			return
		}
		cols := make([]core.Col, 0, len(r.cells))
		for i, c := range r.cells {
			style := props.Text{}
			if i > 0 && i != 2 {
				style.Align = align.Right
			}
			if header {
				style.Style = fontstyle.Bold
			}
			cols = append(cols, text.NewCol(sizes[i], c, style))
		}
		doc.AddAutoRow(cols...)
	}
	table(pdfRow{kind: kindProduct, cells: m.header}, true)
	for _, r := range m.rows {
		table(r, false)
	}
	if len(m.optionalRows) > 0 {
		doc.AddRows(text.NewRow(8, m.optionalTitle, props.Text{Style: fontstyle.Bold, Size: 11, Top: 3}))
		for _, r := range m.optionalRows {
			table(r, false)
		}
	}
	doc.AddRows(line.NewRow(4))

	doc.AddRow(4.5, col.New(6), text.NewCol(2, m.vatHeader[0], bold), text.NewCol(2, m.vatHeader[1], boldRight), text.NewCol(2, m.vatHeader[2], boldRight))
	for _, r := range m.vatRows {
		doc.AddRow(4.5, col.New(6), text.NewCol(2, r[0]), text.NewCol(2, r[1], right), text.NewCol(2, r[2], right))
	}
	doc.AddRows(line.NewRow(4))
	for i, kv := range m.totals {
		style := right
		if i == len(m.totals)-1 {
			style = boldRight
		}
		doc.AddRow(5, col.New(6), text.NewCol(3, kv[0], style), text.NewCol(3, kv[1], style))
	}
	if m.closing != "" {
		doc.AddAutoRow(text.NewCol(12, m.closing, props.Text{Top: 3}))
	}
	if m.footer != "" {
		doc.AddAutoRow(text.NewCol(12, m.footer, props.Text{Top: 3, Size: 8}))
	}
	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("orders: render the PDF: %w", err)
	}
	return out.GetBytes(), nil
}

// renderPrintable renders a document's PDF.
func renderPrintable(d printable) ([]byte, error) { return renderPDF(buildPDFModel(d)) }

// renderOrderConfirmation is an order's confirmation PDF (O24): the order
// confirmation, revised with its version after a change (O26), or its pro
// forma print when proforma is set. date is the confirmation's date — when it
// was confirmed — and seller the seller block read before (s.seller). The
// orders lane stores it on confirm and on send (storeDocument).
func renderOrderConfirmation(o store.OrdersOrder, lines []store.OrdersLine, seller contracts.SellerProfile, date time.Time, proforma bool) ([]byte, error) {
	p := printableOfOrder(o, lines, seller, date)
	p.proforma = proforma
	return renderPrintable(p)
}

// renderQuote is a quote's PDF, or its pro forma print.
func renderQuote(q store.OrdersQuote, lines []store.OrdersLine, seller contracts.SellerProfile, date time.Time, proforma bool) ([]byte, error) {
	p := printableOfQuote(q, lines, seller, date)
	p.proforma = proforma
	return renderPrintable(p)
}
