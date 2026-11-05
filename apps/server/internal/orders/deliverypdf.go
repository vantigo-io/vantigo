package orders

// A delivery's printouts (O47), in the module's PDF style (pdf.go): the pick
// list (plukkliste), printed on demand and never stored — its lines sorted by
// the location's full code, each with the site's locations holding its
// variant — and the packing slip (pakkseddel), stored when the delivery is
// posted: the lines with what was ordered, what this delivery takes and what
// remains as back order, and the order's delivery address. The consignment's
// carrier, number and tracking are delivery D's.

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/line"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// docPickList is the pick list; it is never stored.
const docPickList = "pick_list"

// deliveryLabels are a delivery document's fixed words in one language.
type deliveryLabels struct {
	pickList, packingSlip, draft, deliveryNumber, orderNumber string
	date, deliveryAddress, yourRef, customerNumber, contact   string
	description, location, holding, quantity, ordered         string
	backOrder, unit, picked, notPicked                        string
}

var deliveryLabelSets = map[string]deliveryLabels{
	"nb": {
		pickList: "Plukkliste", packingSlip: "Pakkseddel", draft: "UTKAST", deliveryNumber: "Leveranse %d", orderNumber: "Ordre %d",
		date: "Dato", deliveryAddress: "Leveringsadresse", yourRef: "Deres ref.", customerNumber: "Kundenr.", contact: "Kontakt",
		description: "Beskrivelse", location: "Lokasjon", holding: "Lokasjoner med beholdning", quantity: "Antall",
		ordered: "Bestilt", backOrder: "Restordre", unit: "Enhet", picked: "Plukket", notPicked: "Ikke valgt",
	},
	"en": {
		pickList: "Pick list", packingSlip: "Packing slip", draft: "DRAFT", deliveryNumber: "Delivery %d", orderNumber: "Order %d",
		date: "Date", deliveryAddress: "Delivery address", yourRef: "Your ref.", customerNumber: "Customer no.", contact: "Contact",
		description: "Description", location: "Location", holding: "Locations holding stock", quantity: "Quantity",
		ordered: "Ordered", backOrder: "Back order", unit: "Unit", picked: "Picked", notPicked: "Not chosen",
	},
}

// deliveryPrintable is everything a delivery's PDF is printed from.
type deliveryPrintable struct {
	kind      string // docPickList or docPackingSlip
	d         store.OrdersDelivery
	o         store.OrdersOrder
	dlines    []store.OrdersDeliveryLine
	lines     []store.OrdersLine
	locations map[int32][]contracts.StockLocation
	seller    contracts.SellerProfile
	date      time.Time
}

// buildDeliveryModel is a delivery document as words.
func buildDeliveryModel(p deliveryPrintable) pdfModel {
	lang := p.o.Language
	if _, ok := deliveryLabelSets[lang]; !ok {
		lang = "nb"
	}
	l := deliveryLabelSets[lang]
	h := headerOfOrder(p.o)
	m := pdfModel{created: p.date}
	m.title = l.packingSlip
	if p.kind == docPickList {
		m.title = l.pickList
	}
	if p.d.Status != deliveryPosted && p.kind == docPackingSlip {
		m.watermark = l.draft
	}
	m.subtitle = fmt.Sprintf(l.deliveryNumber, p.d.Number) + " – " + fmt.Sprintf(l.orderNumber, p.o.Number)

	sp := p.seller
	m.seller = addressLines(sp.LegalName, &sp.AddressLine1, &sp.AddressLine2, &sp.PostalCode, &sp.City, ptr(strings.ToUpper(sp.Country)))
	m.buyer = addressLines(deref(h.DeliveryName), h.DeliveryAddressLine1, h.DeliveryAddressLine2, h.DeliveryPostalCode, h.DeliveryCity, h.DeliveryCountry)
	if len(m.buyer) == 0 || deref(h.DeliveryName) == "" {
		m.buyer = append([]string{h.CustomerName}, m.buyer...)
	}

	m.meta = append(m.meta, [2]string{l.date, formatDate(p.date, lang)})
	if place := addressLines(deref(h.DeliveryName), h.DeliveryAddressLine1, h.DeliveryAddressLine2, h.DeliveryPostalCode, h.DeliveryCity, h.DeliveryCountry); len(place) > 0 {
		m.meta = append(m.meta, [2]string{l.deliveryAddress, strings.Join(place, ", ")})
	}
	if contact := strings.Join(nonEmpty(deref(h.ContactName), deref(h.ContactPhone)), ", "); contact != "" {
		m.meta = append(m.meta, [2]string{l.contact, contact})
	}
	if h.CustomerNumber != nil {
		m.meta = append(m.meta, [2]string{l.customerNumber, fmt.Sprint(*h.CustomerNumber)})
	}
	if h.YourReference != "" {
		m.meta = append(m.meta, [2]string{l.yourRef, h.YourReference})
	}

	byLine := byID(p.lines)
	describe := func(ol store.OrdersLine) string {
		desc := deref(ol.Name)
		if sku := deref(ol.Sku); sku != "" {
			desc = sku + " " + desc
		}
		return desc
	}
	qty := func(v *big.Rat) string { return formatDecimal(v, 0, 3, lang) }
	if p.kind == docPickList {
		m.header = []string{l.location, l.description, l.quantity, l.unit, l.holding}
		sorted := slices.Clone(p.dlines)
		// By the location's full code; the lines not yet picked last.
		slices.SortStableFunc(sorted, func(a, b store.OrdersDeliveryLine) int {
			switch {
			case a.FromLocationCode == nil && b.FromLocationCode == nil:
				return cmp.Compare(a.Position, b.Position)
			case a.FromLocationCode == nil:
				return 1
			case b.FromLocationCode == nil:
				return -1
			}
			return cmp.Compare(*a.FromLocationCode, *b.FromLocationCode)
		})
		for _, dl := range sorted {
			ol := byLine[dl.OrderLineID]
			var holding []string
			for _, loc := range p.locations[dl.VariantID] {
				holding = append(holding, loc.FullCode+" ("+qty(mustRat(loc.Quantity))+")")
			}
			location := l.notPicked
			if dl.FromLocationCode != nil {
				location = *dl.FromLocationCode
			}
			m.rows = append(m.rows, pdfRow{kind: kindProduct, cells: []string{
				location, describe(ol), qty(ratOrZero(dl.Quantity)), ol.Unit, strings.Join(holding, ", "),
			}})
		}
	} else {
		m.header = []string{l.description, l.ordered, l.quantity, l.backOrder, l.unit}
		for _, dl := range p.dlines {
			ol := byLine[dl.OrderLineID]
			// What remains once this delivery is out: posted, it is already
			// in delivered; a draft's is not yet.
			left := openToDeliver(ol)
			if p.d.Status != deliveryPosted {
				left.Sub(left, ratOrZero(dl.Quantity))
				if left.Sign() < 0 {
					left = new(big.Rat)
				}
			}
			m.rows = append(m.rows, pdfRow{kind: kindProduct, cells: []string{
				describe(ol), qty(ratOrZero(ol.Quantity)), qty(ratOrZero(dl.Quantity)), qty(left), ol.Unit,
			}})
		}
	}
	m.footer = sp.FooterText
	if pdfModelBuilt != nil {
		pdfModelBuilt(m)
	}
	return m
}

// renderDeliveryDoc renders a delivery's PDF.
func renderDeliveryDoc(p deliveryPrintable) ([]byte, error) {
	return renderListPDF(buildDeliveryModel(p), p.kind == docPackingSlip)
}

// renderListPDF lays a delivery's model out on A4: the title, the parties,
// the meta block and the line table, with no money.
func renderListPDF(m pdfModel, packingSlip bool) ([]byte, error) {
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
	doc.AddRows(line.NewRow(4))
	// The pick list leads with the location; the packing slip with the item.
	sizes := []int{2, 4, 1, 1, 4}
	numeric := map[int]bool{2: true}
	if packingSlip {
		sizes = []int{6, 2, 2, 1, 1}
		numeric = map[int]bool{1: true, 2: true, 3: true}
	}
	row := func(cells []string, header bool) {
		cols := make([]core.Col, 0, len(cells))
		for i, c := range cells {
			style := props.Text{}
			if numeric[i] {
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
		row(r.cells, false)
	}
	if m.footer != "" {
		doc.AddRows(line.NewRow(4))
		doc.AddAutoRow(text.NewCol(12, m.footer, props.Text{Top: 3, Size: 8}))
	}
	out, err := doc.Generate()
	if err != nil {
		return nil, fmt.Errorf("orders: render the PDF: %w", err)
	}
	return out.GetBytes(), nil
}
