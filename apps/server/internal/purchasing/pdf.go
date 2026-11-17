package purchasing

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
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// This file is the purchase order's PDF (P17, P21, A19), in Norwegian or
// English: our company from SellerProfile, as Orders' documents name it, the
// supplier's snapshot, the delivery address, the lines net of VAT and their
// total, the line asking the supplier to quote our order number on the
// invoice (P20, the match's key), Amounts exclude VAT, the settings' text
// and the seller's footer — laid out with maroto v2 and Orders' embedded
// Noto Sans. A revised order is marked Revised, a cancellation Cancelled, a
// draft's print Draft. The model — every word, in order — is built first
// and is what the tests read.

// The font: Noto Sans Regular and Bold (SIL Open Font License 1.1, whose text
// is fonts/LICENSE), for æøå and the en dash.
var (
	//go:embed fonts/NotoSans-Regular.ttf
	notoSansRegular []byte
	//go:embed fonts/NotoSans-Bold.ttf
	notoSansBold []byte
)

const fontFamily = "noto-sans"

// pdfModificationDate is the fixed /ModDate of every PDF, as Orders': gofpdf
// would write time.Now() otherwise.
var pdfModificationDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func init() {
	gofpdf.SetDefaultCatalogSort(true)
	gofpdf.SetDefaultModificationDate(pdfModificationDate)
}

// The kinds of PDF an order has (P21, A16), as purchasing.documents stores
// them.
const (
	docOrder        = "order"
	docCancellation = "cancellation"
)

// pdfLabels are a purchase order's fixed words in one language.
type pdfLabels struct {
	title, revised, cancelled, draftMark, cancelledMark              string
	orderNumber, version, date, expected, deliveryAddress            string
	supplierNumber, yourRef, ourRef, supplierRef, orgNumber          string
	description, itemNumber, quantity, unit, price, discount, amount string
	total, exclVAT, quoteNumber, cancelledNotice                     string
}

var pdfLabelSets = map[string]pdfLabels{
	"nb": {
		title: "Innkjøpsordre", revised: "Revidert innkjøpsordre", cancelled: "Kansellert innkjøpsordre",
		draftMark: "UTKAST", cancelledMark: "KANSELLERT",
		orderNumber: "Ordre %d", version: "versjon %d", date: "Ordredato", expected: "Forventet levering",
		deliveryAddress: "Leveringsadresse", supplierNumber: "Leverandørnr.", yourRef: "Deres ref.", ourRef: "Vår ref.",
		supplierRef: "Deres ordrenr.", orgNumber: "Org.nr.",
		description: "Beskrivelse", itemNumber: "Deres varenr.", quantity: "Antall", unit: "Enhet", price: "Pris",
		discount: "Rabatt %", amount: "Beløp", total: "Sum eks. mva", exclVAT: "Beløpene er uten mva.",
		quoteNumber: "Oppgi vårt ordrenummer %d på fakturaen.", cancelledNotice: "Denne ordren er kansellert. Den skal ikke leveres.",
	},
	"en": {
		title: "Purchase order", revised: "Revised purchase order", cancelled: "Cancelled purchase order",
		draftMark: "DRAFT", cancelledMark: "CANCELLED",
		orderNumber: "Order %d", version: "version %d", date: "Order date", expected: "Expected delivery",
		deliveryAddress: "Delivery address", supplierNumber: "Supplier no.", yourRef: "Your ref.", ourRef: "Our ref.",
		supplierRef: "Your order no.", orgNumber: "Org. no.",
		description: "Description", itemNumber: "Your item no.", quantity: "Quantity", unit: "Unit", price: "Price",
		discount: "Discount %", amount: "Amount", total: "Total excl. VAT", exclVAT: "Amounts exclude VAT.",
		quoteNumber: "Please quote our order number %d on your invoice.", cancelledNotice: "This order is cancelled. It is not to be delivered.",
	},
}

// printable is everything a purchase order's PDF is printed from.
type printable struct {
	kind     string // docOrder or docCancellation
	draft    bool
	order    store.PurchasingOrder
	supplier snapshot
	delivery address
	lines    []store.PurchasingOrderLine
	seller   contracts.SellerProfile
	footer   string
}

// pdfModel is every word a purchase order's PDF prints, in order.
type pdfModel struct {
	watermark string
	title     string
	subtitle  string
	seller    []string
	supplier  []string
	meta      [][2]string
	header    []string
	rows      [][]string
	totals    [][2]string
	notes     []string
	footer    []string
	created   time.Time
}

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
// "1,234.50" in English — with at least minPlaces and at most maxPlaces.
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
	if c := strings.ToUpper(deref(country)); c != "" && c != "NO" {
		out = append(out, c)
	}
	return out
}

// buildPDFModel is a purchase order as words.
func buildPDFModel(d printable) pdfModel {
	o := d.order
	lang := o.Language
	if _, ok := pdfLabelSets[lang]; !ok {
		lang = "nb"
	}
	l := pdfLabelSets[lang]
	m := pdfModel{created: o.OrderDate.Time}
	revised := o.Version > 1
	switch {
	case d.kind == docCancellation:
		m.title, m.watermark = l.cancelled, l.cancelledMark
	case revised && !d.draft:
		m.title = l.revised
	default:
		m.title = l.title
	}
	if d.draft {
		m.watermark = l.draftMark
	}
	if o.Number != nil {
		m.subtitle = fmt.Sprintf(l.orderNumber, *o.Number)
		if revised && d.kind == docOrder {
			m.subtitle += " – " + fmt.Sprintf(l.version, o.Version)
		}
	}

	sp := d.seller
	m.seller = addressLines(sp.LegalName, &sp.AddressLine1, &sp.AddressLine2, &sp.PostalCode, &sp.City, ptr(strings.ToUpper(sp.Country)))
	if sp.OrganisationNumber != "" {
		org := l.orgNumber + " " + organisationNumber(sp.OrganisationNumber)
		if sp.VATRegistered {
			org += " MVA"
		}
		m.seller = append(m.seller, org)
	}
	for _, c := range []string{sp.Email, sp.Phone} {
		if c != "" {
			m.seller = append(m.seller, c)
		}
	}
	sup := d.supplier
	m.supplier = addressLines(sup.name, sup.addressLine, nil, sup.postalCode, sup.city, sup.country)
	if sup.organisationNumber != nil {
		m.supplier = append(m.supplier, l.orgNumber+" "+organisationNumber(*sup.organisationNumber))
	}

	m.meta = append(m.meta, [2]string{l.date, formatDate(o.OrderDate.Time, lang)})
	if o.ExpectedOn.Valid {
		m.meta = append(m.meta, [2]string{l.expected, formatDate(o.ExpectedOn.Time, lang)})
	}
	dl := d.delivery
	if place := addressLines(deref(dl.name), dl.line1, dl.line2, dl.postalCode, dl.city, dl.country); len(place) > 0 {
		m.meta = append(m.meta, [2]string{l.deliveryAddress, strings.Join(place, ", ")})
	}
	if sup.number != nil {
		m.meta = append(m.meta, [2]string{l.supplierNumber, fmt.Sprint(*sup.number)})
	}
	if o.SupplierContact != "" {
		m.meta = append(m.meta, [2]string{l.yourRef, o.SupplierContact})
	}
	if b := deref(o.BuyerDisplay); b != "" {
		m.meta = append(m.meta, [2]string{l.ourRef, b})
	}
	if r := deref(o.SupplierReference); r != "" {
		m.meta = append(m.meta, [2]string{l.supplierRef, r})
	}

	inCurrency := func(label string) string { return label + " (" + o.Currency + ")" }
	m.header = []string{l.description, l.itemNumber, l.quantity, l.unit, l.price, l.discount, inCurrency(l.amount)}
	total := new(big.Rat)
	for _, ln := range d.lines {
		m.rows = append(m.rows, printedLine(ln, lang))
		total.Add(total, ratOrZero(ln.LineNet))
	}
	m.totals = [][2]string{{l.total, o.Currency + " " + money(total, lang)}}
	m.notes = append(m.notes, l.exclVAT)
	if o.Number != nil && d.kind == docOrder {
		m.notes = append(m.notes, fmt.Sprintf(l.quoteNumber, *o.Number))
	}
	if d.kind == docCancellation {
		m.notes = append(m.notes, l.cancelledNotice)
	}
	if n := strings.TrimSpace(o.Note); n != "" {
		m.notes = append(m.notes, n)
	}
	for _, f := range []string{d.footer, sp.FooterText} {
		if f = strings.TrimSpace(f); f != "" {
			m.footer = append(m.footer, f)
		}
	}
	if pdfModelBuilt != nil {
		pdfModelBuilt(m)
	}
	return m
}

// printedLine is one line's printed row: the SKU, name and description, the
// supplier's item number, the quantity in the purchase packaging, the unit
// or packaging, the price, the discount and the net.
func printedLine(ln store.PurchasingOrderLine, lang string) []string {
	desc := strings.Join(nonEmpty(ln.Name, ln.Description), " – ")
	if ln.Kind == kindText {
		desc = ln.Description
	}
	if sku := deref(ln.Sku); sku != "" {
		desc = sku + " " + desc
	}
	unit := ln.Unit
	if ln.PackagingName != nil {
		unit = *ln.PackagingName
		if f := ratOf(ln.PackagingFactor); f != nil {
			of := "à"
			if lang == "en" {
				of = "of"
			}
			unit = fmt.Sprintf("%s %s %s %s", *ln.PackagingName, of, formatDecimal(f, 0, 3, lang), ln.Unit)
		}
	}
	return []string{
		desc, deref(ln.SupplierItemNumber), formatDecimal(ratOrZero(ln.OrderQuantity), 0, 3, lang), unit,
		formatDecimal(ratOrZero(ln.UnitPrice), 2, 4, lang), formatDecimal(ratOrZero(ln.DiscountPercent), 0, 2, lang),
		money(ratOrZero(ln.LineNet), lang),
	}
}

// renderPDF lays a model out on A4 and answers the bytes.
func renderPDF(m pdfModel) ([]byte, error) {
	fonts, err := fontrepository.New().
		AddUTF8FontFromBytes(fontFamily, fontstyle.Normal, notoSansRegular).
		AddUTF8FontFromBytes(fontFamily, fontstyle.Bold, notoSansBold).
		Load()
	if err != nil {
		return nil, fmt.Errorf("purchasing: load the PDF font: %w", err)
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
	boldRight := props.Text{Style: fontstyle.Bold, Align: align.Right}

	if m.watermark != "" {
		doc.AddRows(text.NewRow(10, m.watermark, props.Text{Style: fontstyle.Bold, Size: 14, Align: align.Center,
			Color: &props.Color{Red: 200, Green: 30, Blue: 30}}))
	}
	doc.AddRows(text.NewRow(12, m.title, props.Text{Style: fontstyle.Bold, Size: 18}))
	if m.subtitle != "" {
		doc.AddRows(text.NewRow(7, m.subtitle, props.Text{Style: fontstyle.Bold, Size: 11}))
	}
	for i := range max(len(m.seller), len(m.supplier)) {
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
		doc.AddRow(4.5, text.NewCol(6, cell(m.seller), style), text.NewCol(6, cell(m.supplier), style))
	}
	doc.AddRows(line.NewRow(4))
	for _, kv := range m.meta {
		doc.AddRow(4.5, text.NewCol(3, kv[0], bold), text.NewCol(9, kv[1]))
	}
	doc.AddRows(line.NewRow(4))

	sizes := []int{4, 2, 1, 2, 1, 1, 1}
	row := func(cells []string, header bool) {
		cols := make([]core.Col, 0, len(cells))
		for i, c := range cells {
			style := props.Text{}
			if i >= 4 || i == 2 {
				style.Align = align.Right
			}
			if header {
				style.Style = fontstyle.Bold
			}
			cols = append(cols, text.NewCol(sizes[i], c, style))
		}
		doc.AddAutoRow(cols...)
	}
	row(m.header, true)
	for _, r := range m.rows {
		row(r, false)
	}
	doc.AddRows(line.NewRow(4))
	for _, kv := range m.totals {
		doc.AddRow(5, col.New(6), text.NewCol(3, kv[0], boldRight), text.NewCol(3, kv[1], boldRight))
	}
	for _, n := range m.notes {
		doc.AddAutoRow(text.NewCol(12, n, props.Text{Top: 3}))
	}
	for _, f := range m.footer {
		doc.AddAutoRow(text.NewCol(12, f, props.Text{Top: 3, Size: 8}))
	}
	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("purchasing: render the PDF: %w", err)
	}
	return out.GetBytes(), nil
}

// renderOrder renders a purchase order's PDF.
func renderOrder(d printable) ([]byte, error) { return renderPDF(buildPDFModel(d)) }

// fileName is a PDF's download name in the order's language.
func fileName(o store.PurchasingOrder, kind string) string {
	en := o.Language == "en"
	if o.Number == nil {
		if en {
			return fmt.Sprintf("purchase-order-draft-%d.pdf", o.ID)
		}
		return fmt.Sprintf("innkjopsordre-utkast-%d.pdf", o.ID)
	}
	switch {
	case kind == docCancellation && en:
		return fmt.Sprintf("purchase-order-%d-cancelled.pdf", *o.Number)
	case kind == docCancellation:
		return fmt.Sprintf("innkjopsordre-%d-kansellert.pdf", *o.Number)
	case en:
		return fmt.Sprintf("purchase-order-%d-v%d.pdf", *o.Number, o.Version)
	}
	return fmt.Sprintf("innkjopsordre-%d-v%d.pdf", *o.Number, o.Version)
}
