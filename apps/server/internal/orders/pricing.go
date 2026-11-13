package orders

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file prices a document's lines (O15, O16, O19) and sums them (O17):
// every amount is internal/docmath's, Invoices' own arithmetic, so a line is
// the same number on the order and on the invoice by construction.

// The line kinds of this phase (O15); the set is open.
const (
	kindProduct = "product"
	kindSection = "section"
	kindNote    = "note"
	// kindShipping is the freight the customer pays (O53): priced like a
	// product line, quantity 1, invoiced on order, and counted delivered when
	// the order's first shipment is booked. Quotes and orders only.
	kindShipping = "shipping"
)

// pricedKind reports whether a line of kind carries a price and an amount:
// a product line or a shipping line.
func pricedKind(kind string) bool { return kind == kindProduct || kind == kindShipping }

// The price sources (O16, O53).
const (
	priceCatalog = "catalog"
	priceManual  = "manual"
	priceChannel = "channel"
	// priceCarrier is a shipping line's price from the carrier's estimate
	// with the markup (O53).
	priceCarrier = "carrier"
)

// maxLines is how many lines one document may have (too_many_lines).
const maxLines = 500

// lineInput is one line as a save, a template or a copy hands it in, before
// it is priced. The wire's numbers are kept as they came; unitPriceNet is a
// price already net and exact — a template's override, a copied manual line's
// — which bypasses the including-VAT entry of O19.
type lineInput struct {
	id                *int64
	kind              string
	variantID         *int32
	name, description *string
	unit              *string
	quantity          *float64
	unitPrice         *float64
	unitPriceNet      *big.Rat
	discount          *float64
	packagingID       *int32
	packagingQuantity *float64
	vatCodeID         *int32
	optional          bool
	// skippable is a template's or a copy's line, left out rather than
	// refused when its variant went away (O23); skipDiscontinued, a
	// template's, also when its product was discontinued.
	skippable, skipDiscontinued bool
	// invoicePolicy is an order line's policy as the request names it (O48),
	// nil when it names none.
	invoicePolicy *string
	// priceSource is carrier for a shipping line's estimated price (O53),
	// nil otherwise.
	priceSource *string
}

// lineInputsOf reads the API's lines.
func lineInputsOf(in []gen.OrdersLineInput) []lineInput {
	out := make([]lineInput, 0, len(in))
	for _, l := range in {
		li := lineInput{
			id: l.Id, kind: string(l.Kind), variantID: l.VariantId, name: l.Name, description: l.Description, unit: l.Unit,
			quantity: l.Quantity, unitPrice: l.UnitPrice, discount: l.DiscountPercent, packagingID: l.PackagingId,
			packagingQuantity: l.PackagingQuantity, vatCodeID: l.VatCodeId,
		}
		if l.Optional != nil {
			li.optional = *l.Optional
		}
		if l.InvoicePolicy != nil {
			li.invoicePolicy = ptr(string(*l.InvoicePolicy))
		}
		if l.PriceSource != nil {
			li.priceSource = ptr(string(*l.PriceSource))
		}
		out = append(out, li)
	}
	return out
}

// pricedLine is one line ready to store: its snapshot, its price and VAT, and
// docmath's amounts.
type pricedLine struct {
	id                *int64
	kind              string
	variantID         *int32
	sku, name         *string
	unit, description string
	quantity          *big.Rat
	unitPrice         *big.Rat
	discount          *big.Rat
	packagingID       *int32
	packagingQuantity *big.Rat
	// packagingName is the packaging's name when the line was entered in
	// one, kept for the PDF (O15); productID the variant's product, kept for
	// the warnings and the analysis (O33, O35).
	packagingName  *string
	productID      *int32
	priceSource    *string
	vatCodeID      *int32
	vatCode        *string
	vatRate        *big.Rat
	vatCategory    *string
	amounts        docmath.LineAmounts
	externalLineID *string
	optional       bool
	// frozen is a confirmed order's kept line priced from its own row
	// (keptFrozen): its variant is held by lockLines, not judged.
	frozen bool
	// validFrom and validTo date a subscription's line (O44); NULL on every
	// other owner's.
	validFrom, validTo pgtype.Date
	// chainID is the first version of the subscription line this one
	// continues (O44); nil on a first version and on every other owner's.
	chainID *int64
	// invoicePolicy is an order line's invoice policy (O48) and
	// deliveryMethod how its delivery is registered (O32, O47): "" until
	// settlePolicies settles them, which writes "order" and "manual".
	// requestedPolicy is the policy the request named, nil for none.
	invoicePolicy, deliveryMethod string
	requestedPolicy               *string
	// kit is, on an order's kit child line, the index of its kit line in the
	// lines written, and perKit how much of it one kit holds (manufacturing
	// phase 2 M145); nil on every other line.
	kit    *int
	perKit *big.Rat
}

// pricedLineOf is a stored line as it would be written again, unchanged:
// accept and copy carry a line with its frozen price this way.
func pricedLineOf(l store.OrdersLine) pricedLine {
	return pricedLine{
		kind: l.Kind, variantID: l.VariantID, sku: l.Sku, name: l.Name, unit: l.Unit, description: l.Description,
		quantity: ratOf(l.Quantity), unitPrice: ratOf(l.UnitPrice), discount: ratOrZero(l.DiscountPercent),
		packagingID: l.PackagingID, packagingQuantity: ratOf(l.PackagingQuantity), packagingName: l.PackagingName,
		productID: l.ProductID, priceSource: l.PriceSource,
		vatCodeID: l.VatCodeID, vatCode: l.VatCode, vatRate: ratOf(l.VatRatePercent), vatCategory: l.VatCategory,
		amounts: docmath.LineAmounts{
			Gross: ratOrZero(l.LineGross), Allowance: ratOrZero(l.LineAllowance), Net: ratOrZero(l.LineNet),
		},
		externalLineID: l.ExternalLineID, optional: l.Optional,
	}
}

// computeAmounts sets a product line's amounts from its quantity, price and
// discount (O17); a line without a price is 0 until it has one.
func (p *pricedLine) computeAmounts() {
	if !pricedKind(p.kind) || p.quantity == nil || p.unitPrice == nil {
		p.amounts = docmath.LineAmounts{Gross: new(big.Rat), Allowance: new(big.Rat), Net: new(big.Rat)}
		return
	}
	p.amounts = docmath.Line(p.quantity, p.unitPrice, p.discount)
}

// pricingReads are the catalog's and the VAT tables' answers a set of lines
// is priced from, all read before any transaction (O16).
type pricingReads struct {
	variants map[int32]contracts.VariantEntry
	// prices are the list prices in the document's currency at the day, nil
	// for a variant with none.
	prices map[int32]*big.Rat
	// taxes map a tax category to its VAT code at the day.
	taxes map[int32]contracts.VATCodeEntry
	// codes are every active VAT code at the day, for a free-text line.
	codes []contracts.VATCodeEntry
}

// readPricing reads what lines priced in currency at at need: the variants,
// their list prices, their categories' VAT codes, and the VAT codes for the
// free-text lines.
func (s *server) readPricing(ctx context.Context, lines []lineInput, currency string, at time.Time) (pricingReads, error) {
	r := pricingReads{prices: map[int32]*big.Rat{}}
	var ids []int32
	freeText := false
	for _, l := range lines {
		if !pricedKind(l.kind) {
			continue
		}
		if l.variantID == nil {
			freeText = true
			continue
		}
		if !slices.Contains(ids, *l.variantID) {
			ids = append(ids, *l.variantID)
		}
	}
	slices.Sort(ids)
	var err error
	if r.variants, err = s.catalogVariants(ctx, ids); err != nil {
		return r, err
	}
	var categories []int32
	for _, id := range ids {
		v, ok := r.variants[id]
		if !ok {
			continue
		}
		m, err := s.listPrice(ctx, id, currency, at)
		if err != nil {
			return r, err
		}
		if m != nil && strings.EqualFold(m.Currency, currency) {
			if price, ok := new(big.Rat).SetString(m.Amount); ok {
				r.prices[id] = price
			}
		}
		if !slices.Contains(categories, v.TaxCategoryID) {
			categories = append(categories, v.TaxCategoryID)
		}
	}
	if r.taxes, err = s.taxCodes(ctx, categories, at); err != nil {
		return r, err
	}
	if freeText || slices.ContainsFunc(lines, func(l lineInput) bool { return l.vatCodeID != nil }) {
		if r.codes, err = s.vatCodes(ctx, at); err != nil {
			return r, err
		}
	}
	return r, nil
}

// defaultCode is a free-text line's VAT code when it names none (O16): the
// standard category's, the highest rate if several; nil when there is none.
func (r pricingReads) defaultCode() *contracts.VATCodeEntry {
	var best *contracts.VATCodeEntry
	for i := range r.codes {
		c := &r.codes[i]
		if c.Category != "S" {
			continue
		}
		if best == nil || mustRat(c.RatePercent).Cmp(mustRat(best.RatePercent)) > 0 {
			best = c
		}
	}
	return best
}

// code is the active VAT code id names, nil when none.
func (r pricingReads) code(id int32) *contracts.VATCodeEntry {
	for i := range r.codes {
		if r.codes[i].ID == id {
			return &r.codes[i]
		}
	}
	return nil
}

// priceOptions are what pricing a document's lines depends on beyond the
// lines: whether prices are entered including VAT (O19), whether optional
// lines are allowed (quotes and templates, O21), and whether a kept catalog
// line is priced again (O16: the currency or the day moved since it was).
type priceOptions struct {
	pricesIncludeVat bool
	allowOptional    bool
	repriceKept      bool
	// frozenKept prices a confirmed order's kept lines from their own rows
	// (keptFrozen).
	frozenKept bool
	// allowShipping admits shipping lines (O53): quotes and orders.
	allowShipping bool
}

// keptFrozen reports whether a confirmed order's save keeps line l over its
// stored row old as frozen (O16, O26): a product line naming the same variant
// (or none, free text, as before) and no other packaging. Such a line keeps
// its item, SKU, catalog name, unit and VAT code, rate and category as they
// were confirmed — the catalog is not read for it, so a variant deleted or
// re-categorised since changes nothing — and takes only what O26 lets a save
// change: its quantity, price, discount and text.
func keptFrozen(l lineInput, old store.OrdersLine) bool {
	return l.kind == kindProduct && old.Kind == kindProduct && equalInt32(l.variantID, old.VariantID) &&
		(l.packagingID == nil || (equalInt32(l.packagingID, old.PackagingID) && old.PackagingQuantity.Valid && old.Quantity.Valid))
}

// judgeKinds refuses a save that changes a kept line's kind
// (line_kind_changed): a line keeps its kind for as long as it keeps its id;
// a client removes it and adds a new one instead.
func judgeKinds(in []lineInput, existing []store.OrdersLine) error {
	old := byID(existing)
	for _, l := range in {
		if l.id == nil {
			continue
		}
		if o, ok := old[*l.id]; ok && o.Kind != l.kind {
			return lineKindChanged(o)
		}
	}
	return nil
}

// lineKindChanged is the refusal of a kept line whose kind changed.
func lineKindChanged(o store.OrdersLine) error {
	return conflict(codeLineKindChanged, "A line's kind cannot change",
		fmt.Sprintf("Line %d is a %s; a line keeps its kind. Remove it and add a new line instead.", o.Position, o.Kind))
}

// priceLines prices every line in (O16), over existing — the document's
// lines now, by id, on a save — and answers them in order, or every failing
// line at once.
func priceLines(in []lineInput, existing map[int64]store.OrdersLine, r pricingReads, opts priceOptions) ([]pricedLine, []lineFailure) {
	var failures []lineFailure
	fail := func(i int, code, detail string) {
		failures = append(failures, lineFailure{index: i, code: code, detail: detail})
	}
	if len(in) > maxLines {
		fail(maxLines, codeTooManyLines, fmt.Sprintf("A document has at most %d lines.", maxLines))
		return nil, failures
	}
	seen := map[int64]bool{}
	out := make([]pricedLine, 0, len(in))
	for i, l := range in {
		var old *store.OrdersLine
		if l.id != nil {
			o, ok := existing[*l.id]
			if !ok || seen[*l.id] {
				fail(i, codeLineInvalid, fmt.Sprintf("Line %d is not one of this document's lines.", *l.id))
				continue
			}
			seen[*l.id] = true
			old = &o
		}
		p, code, detail := priceLine(l, old, r, opts)
		if code != "" {
			fail(i, code, detail)
			continue
		}
		out = append(out, p)
	}
	return out, failures
}

// priceLine prices one line, or names why it cannot be.
func priceLine(l lineInput, old *store.OrdersLine, r pricingReads, opts priceOptions) (pricedLine, string, string) {
	p := pricedLine{kind: l.kind, id: l.id, discount: new(big.Rat), requestedPolicy: l.invoicePolicy}
	text := func(in *string, was string, maxLen int, what string) (string, string) {
		v := was
		if in != nil {
			v = strings.TrimSpace(*in)
		}
		if utf8.RuneCountInString(v) > maxLen {
			return "", fmt.Sprintf("The %s is at most %d characters.", what, maxLen)
		}
		return v, ""
	}
	oldDescription := ""
	if old != nil {
		oldDescription = old.Description
	}
	desc, msg := text(l.description, oldDescription, 2000, "description")
	if msg != "" {
		return p, codeLineInvalid, msg
	}
	p.description = desc

	switch l.kind {
	case kindSection, kindNote:
		if l.name != nil {
			name, msg := text(l.name, "", 255, "name")
			if msg != "" {
				return p, codeLineInvalid, msg
			}
			p.name = textPtr(name)
		}
		if l.optional {
			return p, codeOptionalNotAllowed, "Only a product line can be optional."
		}
		p.computeAmounts()
		return p, "", ""
	case kindProduct:
	case kindShipping:
		if !opts.allowShipping {
			return p, codeLineInvalid, "A shipping line belongs on a quote or an order."
		}
		if l.optional {
			return p, codeOptionalNotAllowed, "A shipping line cannot be optional."
		}
		if l.packagingID != nil {
			return p, codePackagingNotFound, "A shipping line has no packaging."
		}
		if l.quantity != nil && *l.quantity != 1 {
			return p, codeQuantityInvalid, "A shipping line's quantity is 1."
		}
		l.quantity = ptr(1.0)
	default:
		return p, codeLineInvalid, "A line is a product, a section, a note or shipping."
	}

	if l.optional && !opts.allowOptional {
		return p, codeOptionalNotAllowed, "Only a quote's lines can be optional."
	}
	p.optional = l.optional

	frozen := opts.frozenKept && old != nil && keptFrozen(l, *old)
	p.frozen = frozen

	// The catalog item and its snapshot: a frozen line's from its row.
	var variant *contracts.VariantEntry
	switch {
	case frozen && old.VariantID != nil:
		p.variantID, p.sku, p.name, p.unit, p.productID = old.VariantID, old.Sku, old.Name, old.Unit, old.ProductID
	case l.variantID != nil:
		v, ok := r.variants[*l.variantID]
		if !ok {
			return p, codeVariantNotFound, fmt.Sprintf("Variant %d is not in the catalog.", *l.variantID)
		}
		variant = &v
		p.variantID = &v.ID
		p.productID = &v.ProductID
		p.sku = textPtr(v.SKU)
		p.name = textPtr(v.ProductName)
		p.unit = v.Unit
		if l.description == nil && (old == nil || old.VariantID == nil || *old.VariantID != v.ID) {
			p.description = v.Description
		}
	default:
		oldName := ""
		if old != nil && old.VariantID == nil {
			oldName = deref(old.Name)
		}
		name, msg := text(l.name, oldName, 255, "name")
		if msg != "" {
			return p, codeLineInvalid, msg
		}
		if name == "" && p.description == "" {
			return p, codeLineInvalid, "A free-text line needs a name or a description."
		}
		p.name = textPtr(name)
	}
	switch {
	case frozen:
		if l.unit != nil && strings.TrimSpace(*l.unit) != old.Unit {
			return p, codeLineInvalid, "A confirmed order's line keeps its unit; add a new line instead."
		}
		p.unit = old.Unit
	case l.unit != nil || variant == nil:
		oldUnit := ""
		if old != nil {
			oldUnit = old.Unit
		}
		unit, msg := text(l.unit, oldUnit, 20, "unit")
		if msg != "" {
			return p, codeLineInvalid, msg
		}
		p.unit = unit
	}

	// The VAT code: a frozen line's as it was confirmed, a catalog line's
	// from its category (O16), a free-text line's as picked, else the
	// standard category's.
	if frozen {
		if l.vatCodeID != nil && !equalInt32(l.vatCodeID, old.VatCodeID) {
			return p, codeLineInvalid, "A confirmed order's line keeps its VAT code; add a new line instead."
		}
		p.vatCodeID, p.vatCode, p.vatRate, p.vatCategory = old.VatCodeID, old.VatCode, ratOf(old.VatRatePercent), old.VatCategory
	}
	var code *contracts.VATCodeEntry
	switch {
	case frozen:
	case variant != nil:
		if c, ok := r.taxes[variant.TaxCategoryID]; ok {
			code = &c
		}
	default:
		switch {
		case l.vatCodeID != nil:
			if code = r.code(*l.vatCodeID); code == nil {
				return p, codeVATCodeUnknown, fmt.Sprintf("VAT code %d is not an active code.", *l.vatCodeID)
			}
		case old != nil && old.VariantID == nil && old.VatCodeID != nil:
			if code = r.code(*old.VatCodeID); code == nil {
				code = r.defaultCode()
			}
		default:
			code = r.defaultCode()
		}
	}
	if code != nil {
		id, c, category := code.ID, code.Code, code.Category
		p.vatCodeID, p.vatCode, p.vatCategory = &id, &c, &category
		p.vatRate = mustRat(code.RatePercent)
	}

	// The quantity, through a packaging when one is named (O15): the count
	// times the packaging's size, which must come out at the quantity's three
	// decimals exactly, so the stored quantity is always count × size and a
	// later save can read the size back from the row.
	switch {
	case l.packagingID != nil:
		var size *big.Rat
		switch {
		case frozen:
			// The packaging the line was confirmed in, its size from the row.
			size = storedPackagingSize(old)
			p.packagingID, p.packagingName = old.PackagingID, old.PackagingName
		case variant == nil:
			return p, codePackagingNotFound, "A free-text line has no packagings."
		default:
			for i := range variant.Packagings {
				if pack := variant.Packagings[i]; pack.ID == *l.packagingID {
					size = mustRat(pack.Quantity)
					p.packagingID, p.packagingName = &pack.ID, textPtr(pack.Name)
				}
			}
			if size == nil && old != nil && equalInt32(old.PackagingID, l.packagingID) && equalInt32(old.VariantID, p.variantID) {
				// The packaging the line was saved in, since removed from
				// the catalog: kept as it was, its size from the row.
				size = storedPackagingSize(old)
				p.packagingID, p.packagingName = old.PackagingID, old.PackagingName
			}
		}
		if size == nil {
			return p, codePackagingNotFound, fmt.Sprintf("Packaging %d is not one of the variant's.", *l.packagingID)
		}
		if l.packagingQuantity == nil {
			return p, codeQuantityInvalid, "A line entered in a packaging needs how many of it."
		}
		count, msg := parseQuantity(*l.packagingQuantity)
		if msg != "" {
			return p, codeQuantityInvalid, msg
		}
		p.packagingQuantity = count
		p.quantity = new(big.Rat).Mul(count, size)
		if p.quantity.Cmp(maxQuantity) > 0 {
			return p, codeQuantityInvalid, "The quantity is too large."
		}
		if !hasPlaces(p.quantity, quantityPlaces) {
			return p, codeQuantityInvalid, "That many of the packaging comes to more than three decimals; enter the quantity without the packaging."
		}
	case l.quantity != nil:
		q, msg := parseQuantity(*l.quantity)
		if msg != "" {
			return p, codeQuantityInvalid, msg
		}
		p.quantity = q
	case old != nil && pricedKind(old.Kind) && old.Quantity.Valid:
		p.quantity = ratOf(old.Quantity)
	default:
		return p, codeQuantityInvalid, "A product line needs a quantity."
	}

	// The discount.
	switch {
	case l.discount != nil:
		d := *l.discount
		if !finite(d) || d < 0 || d > 100 || decimalPlaces(d) > discountPlaces {
			return p, codeDiscountInvalid, "A discount is 0 to 100 percent, with at most two decimals."
		}
		p.discount = ratFromFloat(d)
	case old != nil:
		p.discount = ratOrZero(old.DiscountPercent)
	}

	// The price (O16, O19): typed is manual — net, or including VAT when the
	// document's prices do — a kept manual price stays, and a catalog line
	// takes its list price, none leaving it unpriced (line_unpriced at send).
	switch {
	case l.unitPriceNet != nil:
		p.unitPrice = l.unitPriceNet
		p.priceSource = ptr(priceManual)
	case l.unitPrice != nil:
		v := *l.unitPrice
		if !finite(v) || v < 0 || decimalPlaces(v) > entryPlaces || ratFromFloat(v).Cmp(maxPrice) > 0 {
			return p, codePriceInvalid, "A unit price is 0 or more, with at most four decimals."
		}
		price := ratFromFloat(v)
		if opts.pricesIncludeVat && p.vatRate != nil {
			price = withoutVAT(price, p.vatRate)
		}
		p.unitPrice = price
		p.priceSource = ptr(priceManual)
		if p.kind == kindShipping && l.priceSource != nil && *l.priceSource == priceCarrier {
			p.priceSource = ptr(priceCarrier)
		}
	case old != nil && keepsPrice(*old, p.variantID, opts.repriceKept):
		p.unitPrice = ratOf(old.UnitPrice)
		p.priceSource = old.PriceSource
	case variant != nil:
		p.priceSource = ptr(priceCatalog)
		if price, ok := r.prices[variant.ID]; ok {
			p.unitPrice = price
		}
	}
	p.computeAmounts()
	return p, "", ""
}

// keepsPrice reports whether a kept line keeps its stored price on a save
// that names none: a manual (or channel) price always, while the line keeps
// its variant; a catalog price unless the currency or the day moved.
func keepsPrice(old store.OrdersLine, variantID *int32, reprice bool) bool {
	if !old.UnitPrice.Valid || !pricedKind(old.Kind) {
		return false
	}
	sameVariant := (old.VariantID == nil && variantID == nil) ||
		(old.VariantID != nil && variantID != nil && *old.VariantID == *variantID)
	if !sameVariant {
		return false
	}
	if old.PriceSource != nil && *old.PriceSource == priceCatalog {
		return !reprice
	}
	return true
}

// parseQuantity is a quantity as an exact decimal, or why it is not one:
// more than 0, at most three decimals.
func parseQuantity(v float64) (*big.Rat, string) {
	if !finite(v) || v <= 0 {
		return nil, "A quantity is more than 0."
	}
	if decimalPlaces(v) > quantityPlaces {
		return nil, "A quantity has at most three decimals."
	}
	q := ratFromFloat(v)
	if q.Cmp(maxQuantity) > 0 {
		return nil, "The quantity is too large."
	}
	return q, ""
}

// storedPackagingSize is the size of the packaging a stored line was entered
// in: its quantity over its count, exact since a quantity is always stored
// as count × size (O15); nil for a line entered in none.
func storedPackagingSize(old *store.OrdersLine) *big.Rat {
	q, n := ratOf(old.Quantity), ratOf(old.PackagingQuantity)
	if old.PackagingID == nil || q == nil || n == nil || n.Sign() == 0 {
		return nil
	}
	return new(big.Rat).Quo(q, n)
}

// ptr is a pointer to v.
func ptr[T any](v T) *T { return &v }

// docTotals are a document's VAT rows and totals (O17), leaving out a quote's
// optional lines (O21).
type docTotals struct {
	summaries       []docmath.VATSummary
	net, vat, gross *big.Rat
}

// taxable is one line as the totals see it.
type taxable struct {
	net      *big.Rat
	category *string
	rate     *big.Rat
}

// sumLines is the totals of lines: VAT per (category, rate) on the sum of the
// nets, never per line. A line without a VAT code yet — refused at send and
// confirm (vat_code_unmapped) — adds its net to the totals and no VAT.
func sumLines(lines []taxable) docTotals {
	var taxed []docmath.TaxedLine
	untaxed := new(big.Rat)
	for _, l := range lines {
		if l.category == nil || l.rate == nil {
			untaxed.Add(untaxed, l.net)
			continue
		}
		taxed = append(taxed, docmath.TaxedLine{Net: l.net, Category: *l.category, Rate: l.rate})
	}
	rows, totals, _ := docmath.Summarize(taxed, big.NewRat(1, 1))
	return docTotals{
		summaries: rows,
		net:       new(big.Rat).Add(totals.Net, untaxed),
		vat:       totals.VAT,
		gross:     new(big.Rat).Add(totals.Gross, untaxed),
	}
}

// totalsOfPriced and totalsOfRows are the totals of priced or stored lines.
func totalsOfPriced(lines []pricedLine) docTotals {
	var in []taxable
	for _, l := range lines {
		if pricedKind(l.kind) && !l.optional {
			in = append(in, taxable{net: l.amounts.Net, category: l.vatCategory, rate: l.vatRate})
		}
	}
	return sumLines(in)
}

func totalsOfRows(lines []store.OrdersLine) docTotals {
	var in []taxable
	for _, l := range lines {
		if pricedKind(l.Kind) && !l.Optional {
			in = append(in, taxable{net: ratOrZero(l.LineNet), category: l.VatCategory, rate: ratOf(l.VatRatePercent)})
		}
	}
	return sumLines(in)
}

// totalsOut is a document's totals as the API answers them.
func totalsOut(t docTotals) gen.OrdersTotals {
	out := gen.OrdersTotals{
		Net: wire(t.net, amountPlaces), Vat: wire(t.vat, amountPlaces), Gross: wire(t.gross, amountPlaces),
		VatSummaries: []gen.OrdersVatSummary{},
	}
	for _, r := range t.summaries {
		out.VatSummaries = append(out.VatSummaries, gen.OrdersVatSummary{
			Category: r.Category, RatePercent: wire(r.Rate, ratePlaces),
			TaxableAmount: wire(r.Taxable, amountPlaces), VatAmount: wire(r.VAT, amountPlaces),
		})
	}
	return out
}

// sendable is the lines' refusal at send or confirm (O16, O18): every product
// line priced (line_unpriced) and with a VAT code (vat_code_unmapped),
// optional lines too, since the customer may take them.
func sendable(lines []store.OrdersLine) []lineFailure {
	var failures []lineFailure
	for i, l := range lines {
		if !pricedKind(l.Kind) {
			continue
		}
		switch {
		case !l.UnitPrice.Valid:
			failures = append(failures, lineFailure{index: i, code: codeLineUnpriced,
				detail: fmt.Sprintf("Line %d has no price in the document's currency.", l.Position)})
		case l.VatCodeID == nil:
			failures = append(failures, lineFailure{index: i, code: codeVATCodeUnmapped,
				detail: fmt.Sprintf("Line %d's tax category is mapped to no VAT code.", l.Position)})
		}
	}
	return failures
}

// variantIDsOf are the variants lines name, sorted and once each: what
// lockVariants takes.
func variantIDsOf(lines []pricedLine) []int32 {
	var ids []int32
	for _, l := range lines {
		if l.variantID != nil && !slices.Contains(ids, *l.variantID) {
			ids = append(ids, *l.variantID)
		}
	}
	slices.Sort(ids)
	return ids
}

// lockLines takes the lines' variants' locks on tx first (MB rule 21) and
// refuses a line whose variant went away since it was priced.
func (s *server) lockLines(ctx context.Context, tx pgx.Tx, title string, lines []pricedLine) error {
	ids := variantIDsOf(lines)
	if len(ids) == 0 {
		return nil
	}
	locked, err := s.lockVariants(ctx, tx, ids)
	if err != nil {
		return err
	}
	present := map[int32]bool{}
	for _, v := range locked {
		present[v.ID] = true
	}
	var failures []lineFailure
	for i, l := range lines {
		if l.variantID != nil && !present[*l.variantID] && !l.frozen {
			failures = append(failures, lineFailure{index: i, code: codeVariantNotFound,
				detail: fmt.Sprintf("Variant %d is no longer in the catalog.", *l.variantID)})
		}
	}
	if len(failures) > 0 {
		return refuseLines(title, failures)
	}
	return nil
}
