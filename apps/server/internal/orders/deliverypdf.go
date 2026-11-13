package orders

// A delivery's printouts (O47), in the module's PDF style (pdf.go): the pick
// list (plukkliste), printed on demand and never stored — its lines sorted by
// the location's full code, each with the site's locations holding its
// variant — and the packing slip (pakkseddel), stored when the delivery is
// posted: the lines with what was ordered, what this delivery takes and what
// remains as back order, and the order's delivery address, and — once a
// shipment is booked — each shipment's carrier, service and consignment
// number, as text and Code 128, with its tracking URL as a QR code (O52).
//
// A tracked line prints its lots (manufacturing phase 2 M79): the pick list
// a row per pick, by location, with the lot number and expiry in its Lot
// column; the packing slip the lot numbers and expiries under the line, so
// the customer receives the one-step-forward record on paper. Serial numbers
// are listed and wrapped.
//
// A kit line is never picked itself (manufacturing phase 2 M145): the pick
// list prints its children only, the packing slip the kit with its children
// under it.

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/code"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
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
	shipment, lot, lots, expires                              string
}

var deliveryLabelSets = map[string]deliveryLabels{
	"nb": {
		pickList: "Plukkliste", packingSlip: "Pakkseddel", draft: "UTKAST", deliveryNumber: "Leveranse %d", orderNumber: "Ordre %d",
		date: "Dato", deliveryAddress: "Leveringsadresse", yourRef: "Deres ref.", customerNumber: "Kundenr.", contact: "Kontakt",
		description: "Beskrivelse", location: "Lokasjon", holding: "Lokasjoner med beholdning", quantity: "Antall",
		ordered: "Bestilt", backOrder: "Restordre", unit: "Enhet", picked: "Plukket", notPicked: "Ikke valgt",
		shipment: "Sending", lot: "Parti", lots: "Partier", expires: "utløper",
	},
	"en": {
		pickList: "Pick list", packingSlip: "Packing slip", draft: "DRAFT", deliveryNumber: "Delivery %d", orderNumber: "Order %d",
		date: "Date", deliveryAddress: "Delivery address", yourRef: "Your ref.", customerNumber: "Customer no.", contact: "Contact",
		description: "Description", location: "Location", holding: "Locations holding stock", quantity: "Quantity",
		ordered: "Ordered", backOrder: "Back order", unit: "Unit", picked: "Picked", notPicked: "Not chosen",
		shipment: "Shipment", lot: "Lot", lots: "Lots", expires: "expires",
	},
}

// deliveryPrintable is everything a delivery's PDF is printed from.
type deliveryPrintable struct {
	kind   string // docPickList or docPackingSlip
	d      store.OrdersDelivery
	o      store.OrdersOrder
	dlines []store.OrdersDeliveryLine
	lines  []store.OrdersLine
	// picks are the tracked lines' lots, by delivery line (M75).
	picks map[int64][]store.OrdersDeliveryLineLot
	// serial is, per variant, whether it is tracked by serial number: the
	// packing slip lists its numbers without quantities.
	serial    map[int32]bool
	locations map[int32][]contracts.StockLocation
	seller    contracts.SellerProfile
	date      time.Time
	// shipments are the delivery's booked shipments, printed on the packing
	// slip (O52).
	shipments []shipmentPrint
}

// kindLots is a packing slip's row of a line's lots, under the line.
const kindLots = "lots"

// kindKit is a packing slip's row of a kit line, its children under it,
// each description led by kitIndent (manufacturing phase 2 M145).
const (
	kindKit   = "kit"
	kitIndent = "    – "
)

// lotText is a pick's lot as a delivery document prints it: the number and,
// when it has one, the expiry.
func lotText(r store.OrdersDeliveryLineLot, l deliveryLabels, lang string) string {
	if !r.Expiry.Valid {
		return r.LotNumber
	}
	return fmt.Sprintf("%s (%s %s)", r.LotNumber, l.expires, formatDate(r.Expiry.Time, lang))
}

// shipmentPrint is a booked shipment as a packing slip prints it.
type shipmentPrint struct{ carrier, service, consignment, trackingURL string }

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
	if p.kind == docPackingSlip {
		for _, sh := range p.shipments {
			text := strings.Join(nonEmpty(sh.carrier, sh.service, sh.consignment), " – ")
			m.meta = append(m.meta, [2]string{l.shipment, text})
			m.codes = append(m.codes, pdfCode{label: text, barcode: sh.consignment, qr: sh.trackingURL})
		}
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
		m.header = []string{l.location, l.description, l.lot, l.quantity, l.unit, l.holding}
		// A row per line, or per pick of a tracked line, and one for what a
		// tracked line's picks leave short.
		type pickRow struct {
			code     *string
			position int32
			cells    []string
		}
		var rows []pickRow
		for _, dl := range p.dlines {
			ol := byLine[dl.OrderLineID]
			var holding []string
			for _, loc := range p.locations[dl.VariantID] {
				holding = append(holding, loc.FullCode+" ("+qty(mustRat(loc.Quantity))+")")
			}
			held := strings.Join(holding, ", ")
			picks := p.picks[dl.ID]
			if len(picks) == 0 {
				location := l.notPicked
				if dl.FromLocationCode != nil {
					location = *dl.FromLocationCode
				}
				rows = append(rows, pickRow{code: dl.FromLocationCode, position: dl.Position, cells: []string{
					location, describe(ol), "", qty(ratOrZero(dl.Quantity)), ol.Unit, held,
				}})
				continue
			}
			for _, r := range picks {
				code := r.LocationCode
				rows = append(rows, pickRow{code: &code, position: dl.Position, cells: []string{
					code, describe(ol), lotText(r, l, lang), qty(ratOrZero(r.Quantity)), ol.Unit, held,
				}})
			}
			if left := new(big.Rat).Sub(ratOrZero(dl.Quantity), sumRows(picks)); left.Sign() > 0 {
				rows = append(rows, pickRow{position: dl.Position, cells: []string{l.notPicked, describe(ol), "", qty(left), ol.Unit, held}})
			}
		}
		// By the location's full code; the rows not yet picked last.
		slices.SortStableFunc(rows, func(a, b pickRow) int {
			switch {
			case a.code == nil && b.code == nil:
				return cmp.Compare(a.position, b.position)
			case a.code == nil:
				return 1
			case b.code == nil:
				return -1
			}
			return cmp.Compare(*a.code, *b.code)
		})
		for _, r := range rows {
			m.rows = append(m.rows, pdfRow{kind: kindProduct, cells: r.cells})
		}
	} else {
		m.header = []string{l.description, l.ordered, l.quantity, l.backOrder, l.unit}
		// A kit's children print under their kit, which is never delivered
		// itself (manufacturing phase 2 M145): a row of the kit with what was
		// ordered of it, then each child, indented.
		dlines := slices.Clone(p.dlines)
		at := func(dl store.OrdersDeliveryLine) int32 {
			ol := byLine[dl.OrderLineID]
			if ol.ParentLineID != nil {
				return byLine[*ol.ParentLineID].Position
			}
			return ol.Position
		}
		slices.SortStableFunc(dlines, func(a, b store.OrdersDeliveryLine) int {
			return cmp.Or(cmp.Compare(at(a), at(b)), cmp.Compare(byLine[a.OrderLineID].Position, byLine[b.OrderLineID].Position))
		})
		var kit *int64
		for _, dl := range dlines {
			ol := byLine[dl.OrderLineID]
			if ol.ParentLineID != nil && (kit == nil || *kit != *ol.ParentLineID) {
				k := byLine[*ol.ParentLineID]
				m.rows = append(m.rows, pdfRow{kind: kindKit, cells: []string{describe(k), qty(ratOrZero(k.Quantity)), "", "", k.Unit}})
			}
			kit = ol.ParentLineID
			// What remains once this delivery is out: posted, it is already
			// in delivered; a draft's is not yet.
			left := openToDeliver(ol)
			if p.d.Status != deliveryPosted {
				left.Sub(left, ratOrZero(dl.Quantity))
				if left.Sign() < 0 {
					left = new(big.Rat)
				}
			}
			desc := describe(ol)
			if ol.ParentLineID != nil {
				desc = kitIndent + desc
			}
			m.rows = append(m.rows, pdfRow{kind: kindProduct, cells: []string{
				desc, qty(ratOrZero(ol.Quantity)), qty(ratOrZero(dl.Quantity)), qty(left), ol.Unit,
			}})
			// The line's lots under it, each once — what it takes summed over
			// its locations — with what it takes when the line takes more
			// than one lot of a variant not tracked by serial number.
			if picks := p.picks[dl.ID]; len(picks) > 0 {
				type lotSum struct {
					row store.OrdersDeliveryLineLot
					qty *big.Rat
				}
				var sums []lotSum
				for _, r := range picks {
					at := slices.IndexFunc(sums, func(s lotSum) bool { return s.row.LotID == r.LotID })
					if at < 0 {
						sums = append(sums, lotSum{row: r, qty: new(big.Rat)})
						at = len(sums) - 1
					}
					sums[at].qty.Add(sums[at].qty, ratOrZero(r.Quantity))
				}
				seen := make([]string, 0, len(sums))
				for _, sum := range sums {
					text := lotText(sum.row, l, lang)
					if len(sums) > 1 && !p.serial[dl.VariantID] {
						text += " – " + qty(sum.qty)
					}
					seen = append(seen, text)
				}
				label := l.lot
				if len(seen) > 1 {
					label = l.lots
				}
				m.rows = append(m.rows, pdfRow{kind: kindLots, cells: []string{label + ": " + strings.Join(seen, ", ")}})
			}
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
	// Each shipment's consignment number as Code 128, its tracking URL as QR.
	for _, c := range m.codes {
		cols := []core.Col{code.NewBarCol(7, c.barcode, props.Barcode{Percent: 90, Proportion: props.Proportion{Width: 20, Height: 4}})}
		if c.qr != "" {
			cols = append(cols, col.New(2), code.NewQrCol(3, c.qr, props.Rect{Percent: 100, Center: true}))
		}
		doc.AddRow(24, cols...)
	}
	doc.AddRows(line.NewRow(4))
	// The pick list leads with the location; the packing slip with the item.
	sizes := []int{2, 3, 2, 1, 1, 3}
	numeric := map[int]bool{3: true}
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
		if r.kind == kindKit {
			row(r.cells, true)
			continue
		}
		if r.kind == kindLots {
			// A line's lots, under it, wrapped across the table.
			doc.AddAutoRow(col.New(1), text.NewCol(11, r.cells[0], props.Text{Size: 8, Bottom: 1}))
			continue
		}
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
