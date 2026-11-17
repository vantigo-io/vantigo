package purchasing

// An order's lines (P16, P17, P18): how a line as written becomes a line as
// stored. A variant line names a variant and takes, for every field left
// out, the supplier item's: its number, its price — when it is in the
// order's currency —, its packaging while that is still one of the variant's,
// and its expected day, the order's day plus the lead time. Its quantity in
// the variant's unit is the order quantity times the packaging's (three
// places), and its net round2(order quantity × unit price × (1 − discount /
// 100)). A Discontinued goods variant is refused on a new line; a service is
// ordered. A text line is a description, a unit, a quantity and a price. A
// saved line keeps every field the request leaves out; on a sent order its
// quantity goes no lower than what is received, a closed line does not
// change, and the variant, the kind and the packaging stay.

import (
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// maxLines is the most lines an order has (P16).
const maxLines = 500

// The line kinds and the product types.
const (
	kindVariant         = "variant"
	kindText            = "text"
	typeGoods           = "Goods"
	typeService         = "Service"
	productDiscontinued = "Discontinued"
)

// lineInput is one line of a request with the keys it carries.
type lineInput struct {
	gen.PurchasingLineInput
	keys map[string]bool
}

// has reports whether the line carries key: sent with a value, or sent as
// null when the raw body was kept.
func (l lineInput) has(key string, value bool) bool { return value || l.keys[key] }

// linesIn pairs a request's lines with the keys the raw body shows.
func linesIn(lines []gen.PurchasingLineInput, presence []map[string]bool) []lineInput {
	out := make([]lineInput, len(lines))
	for i, l := range lines {
		out[i] = lineInput{PurchasingLineInput: l, keys: map[string]bool{}}
		if i < len(presence) {
			out[i].keys = presence[i]
		}
	}
	return out
}

// plannedLine is a line as it is to be stored: a new one (existing nil) or a
// saved one changed.
type plannedLine struct {
	existing           *store.PurchasingOrderLine
	kind               string
	variantID          *int32
	sku                *string
	name               string
	productType        *string
	supplierItemNumber *string
	description        string
	unit               string
	packagingID        *int32
	packagingName      *string
	packagingFactor    *big.Rat
	orderQuantity      *big.Rat
	quantity           *big.Rat
	unitPrice          *big.Rat
	discount           *big.Rat
	net                *big.Rat
	expectedOn         *time.Time
}

// lineContext is what judging an order's lines reads: the order's supplier,
// currency and day, the variants as locked, the supplier's items by variant
// and the order's saved lines by id.
type lineContext struct {
	currency  string
	orderDate time.Time
	sent      bool
	variants  map[int32]contracts.VariantEntry
	items     map[int32]store.PurchasingSupplierItem
	existing  map[int64]store.PurchasingOrderLine
}

// variantIDsOf is the variants a request's lines name, and the saved lines'
// they keep.
func variantIDsOf(in []lineInput, existing map[int64]store.PurchasingOrderLine) []int32 {
	var ids []int32
	for _, l := range in {
		if l.Id != nil {
			if e, ok := existing[*l.Id]; ok && e.VariantID != nil {
				ids = append(ids, *e.VariantID)
				continue
			}
		}
		if l.VariantId != nil {
			ids = append(ids, *l.VariantId)
		}
	}
	return distinctSorted(ids)
}

// packagingOf is the variant's packaging of id, nil when it has none such.
func packagingOf(v contracts.VariantEntry, id int32) *contracts.PackagingEntry {
	for _, p := range v.Packagings {
		if p.ID == id {
			return &p
		}
	}
	return nil
}

// planLines judges the request's lines against lc and answers them planned,
// every failing line at once, or a conflict.
func planLines(lc lineContext, in []lineInput) ([]plannedLine, []lineFailure, error) {
	if len(in) > maxLines {
		return nil, []lineFailure{{index: maxLines, code: codeTooManyLines, detail: fmt.Sprintf("An order has at most %d lines", maxLines)}}, nil
	}
	var failures []lineFailure
	out := make([]plannedLine, 0, len(in))
	seen := map[int64]bool{}
	for i, l := range in {
		fail := func(code, detail string) {
			failures = append(failures, lineFailure{index: i, code: code, detail: detail})
		}
		var p plannedLine
		var code, detail string
		var conflictErr error
		if l.Id != nil {
			e, ok := lc.existing[*l.Id]
			if !ok || seen[*l.Id] {
				fail(codeLineNotFound, fmt.Sprintf("The order has no line %d", *l.Id))
				continue
			}
			seen[*l.Id] = true
			p, code, detail, conflictErr = planSavedLine(lc, l, e)
		} else {
			p, code, detail = planNewLine(lc, l)
		}
		if conflictErr != nil {
			return nil, nil, conflictErr
		}
		if code != "" {
			fail(code, detail)
			continue
		}
		if p.net != nil && p.net.Cmp(maxAmount) > 0 {
			fail(codeAmountOutOfRange, "The line's amount is more than an amount can be (999 999 999 999,99)")
			continue
		}
		out = append(out, p)
	}
	if len(failures) == 0 && netTotal(out).Cmp(maxAmount) > 0 {
		return nil, nil, refuse(problem(http.StatusBadRequest, codeAmountOutOfRange, invalidOrderTitle,
			"The order's total is more than an amount can be (999 999 999 999,99)."))
	}
	return out, failures, nil
}

// readQuantity, readPrice and readDiscount read a line's decimals.
func readQuantity(text string) (*big.Rat, bool) {
	q, ok := parseDecimal(text, quantityPlaces, maxQuantity)
	return q, ok && q.Sign() > 0
}

func readPrice(text *string) (*big.Rat, bool) {
	if text == nil {
		return new(big.Rat), true
	}
	return parseDecimal(*text, pricePlaces, maxPrice)
}

func readDiscount(text *string) (*big.Rat, bool) {
	if text == nil {
		return new(big.Rat), true
	}
	return parseDecimal(*text, discountPlaces, hundred)
}

// planNewLine judges a line the order does not have yet.
func planNewLine(lc lineContext, l lineInput) (plannedLine, string, string) {
	p := plannedLine{kind: string(l.Kind)}
	orderQuantity, ok := readQuantity(l.OrderQuantity)
	if !ok {
		return p, codeQuantityInvalid, "A quantity is more than 0, with at most three decimals"
	}
	p.orderQuantity = orderQuantity
	if l.Description != nil {
		p.description = strings.TrimSpace(*l.Description)
		if utf8.RuneCountInString(p.description) > 2000 {
			return p, codeLineInvalid, "A description is at most 2000 characters"
		}
	}
	if l.DiscountPercent != nil {
		d, ok := readDiscount(l.DiscountPercent)
		if !ok {
			return p, codeDiscountInvalid, "A discount is 0 to 100 percent, with at most two decimals"
		}
		p.discount = d
	} else {
		p.discount = new(big.Rat)
	}
	p.expectedOn = dateFromAPI(l.ExpectedOn)
	switch l.Kind {
	case gen.PurchasingLineInputKindText:
		if p.description == "" {
			return p, codeLineInvalid, "A text line has a description"
		}
		if l.VariantId != nil {
			return p, codeLineInvalid, "A text line names no variant"
		}
		p.unit = strings.TrimSpace(deref(l.Unit))
		if utf8.RuneCountInString(p.unit) > 20 {
			return p, codeLineInvalid, "A unit is at most 20 characters"
		}
		price, ok := readPrice(l.UnitPrice)
		if !ok {
			return p, codePriceInvalid, "A price is 0 or more, with at most four decimals"
		}
		p.unitPrice, p.quantity = price, orderQuantity
		p.name = p.description
		if utf8.RuneCountInString(p.name) > 255 {
			p.name = string([]rune(p.name)[:255])
		}
	case gen.PurchasingLineInputKindVariant:
		if l.VariantId == nil {
			return p, codeLineInvalid, "A variant line names its variant"
		}
		v, ok := lc.variants[*l.VariantId]
		if !ok {
			return p, codeVariantNotFound, fmt.Sprintf("No variant has the id %d", *l.VariantId)
		}
		if v.ProductType == typeGoods && v.ProductStatus == productDiscontinued {
			return p, codeVariantDiscontinued, fmt.Sprintf("%s is discontinued and is not ordered any more", v.SKU)
		}
		item, hasItem := lc.items[v.ID]
		p.variantID, p.sku, p.name, p.productType, p.unit = &v.ID, ptr(v.SKU), v.ProductName, ptr(v.ProductType), v.Unit
		// The supplier's item number, the packaging and the price come from
		// the supplier item when the line leaves them out.
		switch {
		case l.has("supplierItemNumber", l.SupplierItemNumber != nil):
			p.supplierItemNumber = textPtr(strings.TrimSpace(deref(l.SupplierItemNumber)))
			if utf8.RuneCountInString(deref(p.supplierItemNumber)) > 60 {
				return p, codeLineInvalid, "A supplier's item number is at most 60 characters"
			}
		case hasItem:
			p.supplierItemNumber = item.SupplierItemNumber
		}
		var packaging *int32
		switch {
		case l.has("packagingId", l.PackagingId != nil):
			packaging = l.PackagingId
			if packaging != nil && packagingOf(v, *packaging) == nil {
				return p, codePackagingNotFound, fmt.Sprintf("%s has no packaging %d", v.SKU, *packaging)
			}
		case hasItem && item.PackagingID != nil && packagingOf(v, *item.PackagingID) != nil:
			packaging = item.PackagingID
		}
		if code, detail := p.setPackaging(v, packaging); code != "" {
			return p, code, detail
		}
		switch {
		case l.has("unitPrice", l.UnitPrice != nil):
			price, ok := readPrice(l.UnitPrice)
			if !ok {
				return p, codePriceInvalid, "A price is 0 or more, with at most four decimals"
			}
			p.unitPrice = price
		case hasItem && item.Currency == lc.currency && itemPriceFor(v, ratOf(item.Price), item.PackagingID, p.packagingID) != nil:
			p.unitPrice = itemPriceFor(v, ratOf(item.Price), item.PackagingID, p.packagingID)
		default:
			p.unitPrice = new(big.Rat)
		}
		if !l.has("expectedOn", l.ExpectedOn != nil) && hasItem && item.LeadTimeDays != nil {
			p.expectedOn = ptr(lc.orderDate.AddDate(0, 0, int(*item.LeadTimeDays)))
		}
	default:
		return p, codeLineInvalid, "A line is a variant line or a text line"
	}
	p.net = lineNet(p.orderQuantity, p.unitPrice, p.discount)
	return p, "", ""
}

// packagingFactor is how many of the variant's unit packaging id holds: 1
// for none, false when the variant has no such packaging or it holds no
// quantity.
func packagingFactor(v contracts.VariantEntry, id *int32) (*big.Rat, bool) {
	if id == nil {
		return big.NewRat(1, 1), true
	}
	pk := packagingOf(v, *id)
	if pk == nil {
		return nil, false
	}
	f, ok := new(big.Rat).SetString(pk.Quantity)
	if !ok || f.Sign() <= 0 {
		return nil, false
	}
	return f, true
}

// itemPriceFor is a supplier item's price for a line in linePackaging: the
// item's price is per its own packaging, so a line in another one takes it
// converted — price × the line's factor / the item's, at four decimals. nil
// when there is no price, when either factor is unknown, or when the
// converted price is out of range: the line is then left at 0.
func itemPriceFor(v contracts.VariantEntry, price *big.Rat, itemPackaging, linePackaging *int32) *big.Rat {
	if price == nil {
		return nil
	}
	if samePackaging(itemPackaging, linePackaging) {
		return price
	}
	itemFactor, ok := packagingFactor(v, itemPackaging)
	if !ok {
		return nil
	}
	lineFactor, ok := packagingFactor(v, linePackaging)
	if !ok {
		return nil
	}
	converted := roundTo(new(big.Rat).Quo(new(big.Rat).Mul(price, lineFactor), itemFactor), pricePlaces)
	if converted.Cmp(maxPrice) > 0 {
		return nil
	}
	return converted
}

// setPackaging sets the line's packaging and its quantity in the variant's
// unit from its order quantity.
func (p *plannedLine) setPackaging(v contracts.VariantEntry, id *int32) (string, string) {
	p.packagingID, p.packagingName, p.packagingFactor = nil, nil, nil
	factor := big.NewRat(1, 1)
	if id != nil {
		pk := packagingOf(v, *id)
		if pk == nil {
			return codePackagingNotFound, fmt.Sprintf("%s has no packaging %d", v.SKU, *id)
		}
		f, ok := new(big.Rat).SetString(pk.Quantity)
		if !ok || f.Sign() <= 0 {
			return codePackagingNotFound, fmt.Sprintf("The packaging %s holds no quantity", pk.Name)
		}
		p.packagingID, p.packagingName, p.packagingFactor = &pk.ID, ptr(pk.Name), f
		factor = f
	}
	p.quantity = roundTo(new(big.Rat).Mul(p.orderQuantity, factor), quantityPlaces)
	if p.quantity.Sign() <= 0 || p.quantity.Cmp(maxQuantity) > 0 {
		return codeQuantityInvalid, "The quantity in the variant's unit is out of range"
	}
	return "", ""
}

// planSavedLine judges a change of a line the order has.
func planSavedLine(lc lineContext, l lineInput, e store.PurchasingOrderLine) (plannedLine, string, string, error) {
	p := plannedLine{
		existing: &e, kind: e.Kind, variantID: e.VariantID, sku: e.Sku, name: e.Name, productType: e.ProductType,
		supplierItemNumber: e.SupplierItemNumber, description: e.Description, unit: e.Unit, packagingID: e.PackagingID,
		packagingName: e.PackagingName, packagingFactor: ratOf(e.PackagingFactor), orderQuantity: ratOrZero(e.OrderQuantity),
		quantity: ratOrZero(e.Quantity), unitPrice: ratOrZero(e.UnitPrice), discount: ratOrZero(e.DiscountPercent),
		expectedOn: timeOfDate(e.ExpectedOn),
	}
	if string(l.Kind) != e.Kind || (l.VariantId != nil && (e.VariantID == nil || *l.VariantId != *e.VariantID)) {
		return p, "", "", conflict(codeLineKindChanged, "A line's kind does not change",
			fmt.Sprintf("Line %d is a %s line of its own variant; remove it and add a new line instead.", e.ID, e.Kind))
	}
	orderQuantity, ok := readQuantity(l.OrderQuantity)
	if !ok {
		return p, codeQuantityInvalid, "A quantity is more than 0, with at most three decimals", nil
	}
	p.orderQuantity = orderQuantity
	if l.has("description", l.Description != nil) {
		p.description = strings.TrimSpace(deref(l.Description))
		if utf8.RuneCountInString(p.description) > 2000 {
			return p, codeLineInvalid, "A description is at most 2000 characters", nil
		}
		if e.Kind == kindText && p.description == "" {
			return p, codeLineInvalid, "A text line has a description", nil
		}
	}
	if e.Kind == kindText && l.has("unit", l.Unit != nil) {
		p.unit = strings.TrimSpace(deref(l.Unit))
		if utf8.RuneCountInString(p.unit) > 20 {
			return p, codeLineInvalid, "A unit is at most 20 characters", nil
		}
	}
	if l.has("unitPrice", l.UnitPrice != nil) {
		price, ok := readPrice(l.UnitPrice)
		if !ok {
			return p, codePriceInvalid, "A price is 0 or more, with at most four decimals", nil
		}
		p.unitPrice = price
	}
	if l.has("discountPercent", l.DiscountPercent != nil) {
		d, ok := readDiscount(l.DiscountPercent)
		if !ok {
			return p, codeDiscountInvalid, "A discount is 0 to 100 percent, with at most two decimals", nil
		}
		p.discount = d
	}
	if l.has("expectedOn", l.ExpectedOn != nil) {
		p.expectedOn = dateFromAPI(l.ExpectedOn)
	}
	if l.has("supplierItemNumber", l.SupplierItemNumber != nil) {
		p.supplierItemNumber = textPtr(strings.TrimSpace(deref(l.SupplierItemNumber)))
		if utf8.RuneCountInString(deref(p.supplierItemNumber)) > 60 {
			return p, codeLineInvalid, "A supplier's item number is at most 60 characters", nil
		}
	}
	packagingChanged := l.has("packagingId", l.PackagingId != nil) && !samePackaging(l.PackagingId, e.PackagingID)
	switch {
	case e.Kind == kindText:
		p.quantity = orderQuantity
	case packagingChanged && lc.sent:
		return p, codeLineInvalid, "A sent order's line keeps its packaging; change its quantity instead", nil
	case packagingChanged:
		v, ok := lc.variants[*e.VariantID]
		if !ok {
			return p, codeVariantNotFound, fmt.Sprintf("No variant has the id %d", *e.VariantID), nil
		}
		if code, detail := p.setPackaging(v, l.PackagingId); code != "" {
			return p, code, detail, nil
		}
	default:
		factor := p.packagingFactor
		if factor == nil {
			factor = big.NewRat(1, 1)
		}
		p.quantity = roundTo(new(big.Rat).Mul(orderQuantity, factor), quantityPlaces)
		if p.quantity.Sign() <= 0 || p.quantity.Cmp(maxQuantity) > 0 {
			return p, codeQuantityInvalid, "The quantity in the variant's unit is out of range", nil
		}
	}
	p.net = lineNet(p.orderQuantity, p.unitPrice, p.discount)
	if e.ClosedAt != nil && p.differs(e) {
		return p, "", "", conflict(codeLineClosed, "The line is closed",
			fmt.Sprintf("Line %d was closed and does not change.", e.ID))
	}
	if received := ratOrZero(e.Received); p.quantity.Cmp(received) < 0 {
		return p, codeQuantityBelowReceived, fmt.Sprintf("%s is received already; the quantity goes no lower", trimmed(received, quantityPlaces)), nil
	}
	return p, "", "", nil
}

// samePackaging reports whether two packaging ids are the same, nil the unit.
func samePackaging(a, b *int32) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// differs reports whether the plan changes the saved line e.
func (p plannedLine) differs(e store.PurchasingOrderLine) bool {
	return len(p.changes(e)) > 0
}

// lineChange is one field of a saved line a save changes, as the change log
// writes it.
type lineChange struct{ field, from, to string }

// changes is every field of e the plan changes.
func (p plannedLine) changes(e store.PurchasingOrderLine) []lineChange {
	var out []lineChange
	add := func(field, from, to string) {
		if from != to {
			out = append(out, lineChange{field, from, to})
		}
	}
	add("orderQuantity", trimmed(ratOrZero(e.OrderQuantity), quantityPlaces), trimmed(p.orderQuantity, quantityPlaces))
	add("unitPrice", priceText(ratOrZero(e.UnitPrice)), priceText(p.unitPrice))
	add("discountPercent", trimmed(ratOrZero(e.DiscountPercent), discountPlaces), trimmed(p.discount, discountPlaces))
	add("expectedOn", dayText(timeOfDate(e.ExpectedOn)), dayText(p.expectedOn))
	add("description", e.Description, p.description)
	add("unit", e.Unit, p.unit)
	add("supplierItemNumber", deref(e.SupplierItemNumber), deref(p.supplierItemNumber))
	add("packagingId", packagingText(e.PackagingID), packagingText(p.packagingID))
	return out
}

func packagingText(id *int32) string {
	if id == nil {
		return ""
	}
	return fmt.Sprint(*id)
}

// dayText is a calendar date as YYYY-MM-DD, "" for none.
func dayText(d *time.Time) string {
	if d == nil {
		return ""
	}
	return d.Format(time.DateOnly)
}

// netTotal is the sum of the lines' nets (P17).
func netTotal(lines []plannedLine) *big.Rat {
	sum := new(big.Rat)
	for _, l := range lines {
		sum.Add(sum, l.net)
	}
	return sum
}

// insertParams is a planned new line as its row.
func (p plannedLine) insertParams(orderID int64, position int32) store.InsertLineParams {
	return store.InsertLineParams{
		OrderID: orderID, Position: position, Kind: p.kind, VariantID: p.variantID, Sku: p.sku, Name: p.name,
		ProductType: p.productType, SupplierItemNumber: p.supplierItemNumber, Description: p.description, Unit: p.unit,
		PackagingID: p.packagingID, PackagingName: p.packagingName, PackagingFactor: numeric(p.packagingFactor, quantityPlaces),
		OrderQuantity: numeric(p.orderQuantity, quantityPlaces), Quantity: numeric(p.quantity, quantityPlaces),
		UnitPrice: numeric(p.unitPrice, pricePlaces), DiscountPercent: numeric(p.discount, discountPlaces),
		LineNet: numeric(p.net, amountPlaces), ExpectedOn: dateOf(p.expectedOn),
	}
}

// updateParams is a planned saved line as its row.
func (p plannedLine) updateParams(position int32) store.UpdateLineParams {
	return store.UpdateLineParams{
		ID: p.existing.ID, Position: position, SupplierItemNumber: p.supplierItemNumber, Description: p.description,
		Unit: p.unit, PackagingID: p.packagingID, PackagingName: p.packagingName,
		PackagingFactor: numeric(p.packagingFactor, quantityPlaces), OrderQuantity: numeric(p.orderQuantity, quantityPlaces),
		Quantity: numeric(p.quantity, quantityPlaces), UnitPrice: numeric(p.unitPrice, pricePlaces),
		DiscountPercent: numeric(p.discount, discountPlaces), LineNet: numeric(p.net, amountPlaces),
		ExpectedOn: dateOf(p.expectedOn),
	}
}

// lineOpen is what of a line is still to come: ordered and not received,
// nothing when it is closed or marked delivered (P16, A16).
func lineOpen(l store.PurchasingOrderLine) *big.Rat {
	if l.ClosedAt != nil || l.DeliveredAt != nil {
		return new(big.Rat)
	}
	open := new(big.Rat).Sub(ratOrZero(l.Quantity), ratOrZero(l.Received))
	if open.Sign() < 0 {
		return new(big.Rat)
	}
	return open
}

// lineWarnings are a variant line's warnings against the supplier item (P16):
// below its minimum, off its multiple — in purchase packagings, judged when
// the line is in the item's packaging.
func lineWarnings(l store.PurchasingOrderLine, item *store.PurchasingSupplierItem) []gen.PurchasingLineWarning {
	out := []gen.PurchasingLineWarning{}
	if item == nil || l.Kind != kindVariant || !samePackaging(l.PackagingID, item.PackagingID) {
		return out
	}
	q := ratOrZero(l.OrderQuantity)
	if minimum := ratOf(item.MinimumQuantity); minimum != nil && q.Cmp(minimum) < 0 {
		out = append(out, gen.BelowMinimum)
	}
	if multiple := ratOf(item.OrderMultiple); multiple != nil && multiple.Sign() > 0 && !new(big.Rat).Quo(q, multiple).IsInt() {
		out = append(out, gen.OffMultiple)
	}
	return out
}

// lineView is a stored line as the API answers it.
func lineView(l store.PurchasingOrderLine, item *store.PurchasingSupplierItem) gen.PurchasingOrderLine {
	out := gen.PurchasingOrderLine{
		Id: l.ID, Position: l.Position, Kind: gen.PurchasingOrderLineKind(l.Kind), VariantId: l.VariantID, Sku: l.Sku,
		Name: l.Name, SupplierItemNumber: l.SupplierItemNumber, Description: l.Description, Unit: l.Unit,
		PackagingId: l.PackagingID, PackagingName: l.PackagingName, PackagingQuantity: numericText(l.PackagingFactor, quantityPlaces),
		OrderQuantity: trimmed(ratOrZero(l.OrderQuantity), quantityPlaces), Quantity: trimmed(ratOrZero(l.Quantity), quantityPlaces),
		UnitPrice: priceText(ratOrZero(l.UnitPrice)), DiscountPercent: trimmed(ratOrZero(l.DiscountPercent), discountPlaces),
		LineNet: amountText(ratOrZero(l.LineNet)), ExpectedOn: dateOut(l.ExpectedOn), ConfirmedOn: dateOut(l.ConfirmedOn),
		Received: trimmed(ratOrZero(l.Received), quantityPlaces), Invoiced: trimmed(ratOrZero(l.Invoiced), quantityPlaces),
		Open: trimmed(lineOpen(l), quantityPlaces), ClosedAt: l.ClosedAt, Warnings: lineWarnings(l, item),
	}
	if l.ProductType != nil {
		out.ProductType = ptr(gen.PurchasingOrderLineProductType(*l.ProductType))
	}
	return out
}
