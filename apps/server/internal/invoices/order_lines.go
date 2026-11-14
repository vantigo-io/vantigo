package invoices

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
)

// This file is how an order line's allocation becomes a draft's line, and how
// orders and subscriptions become drafts (orders design O36–O39, O43) — a
// subscription is grouped and drafted as an order is, its due periods' lines
// standing for an order's lines: one invoice line per
// allocation, with the order line's description, quantity, unit, unit price,
// discount and VAT code; the one source row the line holds; the grouping of
// orders into drafts — one per order, or combined per customer where the
// orders agree (samlefaktura); and each draft's header. It reads nothing and
// writes nothing: the orders and their allocations come in, the drafts'
// lines, sources and headers go out. The From orders wizard and the billing
// run's orders source build with it.

// The fields a combined draft's orders must agree on (O39), as separatedBy
// names them.
const (
	separatedByCurrency      = "currency"
	separatedByYourReference = "your_reference"
	separatedByProject       = "project"
	separatedByPaymentTerms  = "payment_terms"
	separatedByAddress       = "delivery_address"
)

// maxUnit is a line unit's column bound.
const maxUnit = 20

// allocationSource is the row an allocation's line holds (O38): its identity
// and revision, its kind as the subkind, its order's project when it has
// one, its quantity and exact amount, and as its date a subscription
// period's last day, else date — the order's delivery date, or the day the
// draft was made. A period's first day is kept as periodFrom, and the tax
// category of its variant's product, from categories (variant → category),
// as taxCategoryID (accounting design R6).
func allocationSource(a contracts.OrderAllocation, date time.Time, categories map[int32]int32) (heldSource, error) {
	quantity, err := billableAmount(a.Quantity)
	if err != nil {
		return heldSource{}, err
	}
	amount, err := billableAmount(a.Amount)
	if err != nil {
		return heldSource{}, err
	}
	kind := contracts.AllocationOrderLine
	var from *time.Time
	if a.PeriodStart != nil && a.PeriodEnd != nil {
		kind = contracts.AllocationSubscriptionPeriod
		first := utcDay(*a.PeriodStart)
		from, date = &first, utcDay(*a.PeriodEnd)
	}
	project := int32(0)
	if a.ProjectID != nil {
		project = *a.ProjectID
	}
	category := int32(0)
	if a.VariantID != nil {
		category = categories[*a.VariantID]
	}
	return heldSource{
		kind: contracts.WorkSourceOrderAllocation, id: a.ID, revision: a.Revision, subkind: &kind, projectID: project,
		quantity: quantity, amount: amount, currency: a.Currency, date: utcDay(date), periodFrom: from,
		taxCategoryID: category,
	}, nil
}

// allocationLine is an allocation's invoice line (O39): the order line's
// description — a subscription period's followed by the period, in language —
// unit, quantity, unit price at its eight decimals, discount and VAT code,
// naming the allocation as its one source. docmath computes its amounts from
// the same three numbers Orders computed the allocation's amount from, so the
// line's net is the allocation's.
func allocationLine(language string, a contracts.OrderAllocation) (draftLine, error) {
	quantity, err := billableAmount(a.Quantity)
	if err != nil {
		return draftLine{}, err
	}
	price, err := billableAmount(a.UnitPrice)
	if err != nil {
		return draftLine{}, err
	}
	discount, err := billableAmount(a.DiscountPercent)
	if err != nil {
		return draftLine{}, err
	}
	description := strings.TrimSpace(a.Description)
	if a.PeriodStart != nil && a.PeriodEnd != nil {
		period := periodText(language, utcDay(*a.PeriodStart), utcDay(*a.PeriodEnd))
		description = strings.TrimPrefix(description+", "+period, ", ")
	}
	if description == "" {
		description = orderSectionText(language, allocationDocKind(a), a.OrderNumber, "")
	}
	l := draftLine{
		description: clip(description), unit: clipUnit(strings.TrimSpace(a.Unit)), quantity: quantity, unitPrice: price,
		discount: discount, vatCodeID: a.VATCodeID,
		sources: []sourceRef{{kind: contracts.WorkSourceOrderAllocation, id: a.ID}}, sourcesGiven: true,
	}
	l.amounts = computeLine(l.quantity, l.unitPrice, l.discount)
	return l, nil
}

// orderSectionLine is the text line that opens an order's lines on a draft of
// several orders (O39): "Ordre 2210, Deres ref. …" — or "Abonnement 3001" for
// a subscription — in language, 1 × 0.00 at the VAT code of the document's
// first line: a line of its own that bills nothing and names no source, as
// this module has no line without an amount.
func orderSectionLine(language, kind string, number int64, yourReference string, vatCodeID int32) draftLine {
	l := draftLine{
		description: clip(orderSectionText(language, kind, number, yourReference)), quantity: big.NewRat(1, 1),
		unitPrice: new(big.Rat), discount: new(big.Rat), vatCodeID: vatCodeID, sources: []sourceRef{}, sourcesGiven: true,
	}
	l.amounts = computeLine(l.quantity, l.unitPrice, l.discount)
	return l
}

// orderSectionText is an order's or a subscription's section text: its
// number and, when it has one, the buyer's reference.
func orderSectionText(language, kind string, number int64, yourReference string) string {
	order, ref := "Ordre", "Deres ref."
	if language == "en" {
		order, ref = "Order", "your ref."
	}
	if kind == contracts.DueKindSubscription {
		order = "Abonnement"
		if language == "en" {
			order = "Subscription"
		}
	}
	s := fmt.Sprintf("%s %d", order, number)
	if yourReference = strings.TrimSpace(yourReference); yourReference != "" {
		s += ", " + ref + " " + yourReference
	}
	return s
}

func clipUnit(s string) string {
	if utf8.RuneCountInString(s) <= maxUnit {
		return s
	}
	return string([]rune(s)[:maxUnit])
}

// docKey is a document a draft bills: an order or a subscription, which are
// numbered apart, so its kind and its id together.
type docKey struct {
	kind string
	id   int64
}

// keyOf is a due document's key; a DueOrder without a kind is an order.
func keyOf(o contracts.DueOrder) docKey {
	if o.Kind == contracts.DueKindSubscription {
		return docKey{kind: contracts.DueKindSubscription, id: o.ID}
	}
	return docKey{kind: contracts.DueKindOrder, id: o.ID}
}

// allocationDocKind is the kind of document an allocation bills.
func allocationDocKind(a contracts.OrderAllocation) string {
	if a.Kind == contracts.AllocationSubscriptionPeriod {
		return contracts.DueKindSubscription
	}
	return contracts.DueKindOrder
}

// allocationKey is the document an allocation bills.
func allocationKey(a contracts.OrderAllocation) docKey {
	return docKey{kind: allocationDocKind(a), id: a.OrderID}
}

// orderGroupKey is what a combined draft's orders share (O39): currency, the
// buyer's reference, the project (0 for none), the payment terms (-1 for
// none, the customer's own) and the delivery address, normalised ("" for
// none) — a draft states one place of delivery, so orders delivered to two
// places are two drafts.
type orderGroupKey struct {
	currency, yourReference, address string
	project, terms                   int32
}

func groupKeyOf(o contracts.DueOrder) orderGroupKey {
	k := orderGroupKey{currency: o.Currency, yourReference: strings.TrimSpace(o.YourReference), address: addressKey(o.DeliveryAddress), terms: -1}
	if o.ProjectID != nil {
		k.project = *o.ProjectID
	}
	if o.PaymentTermsDays != nil {
		k.terms = *o.PaymentTermsDays
	}
	return k
}

// addressKey is a delivery address as the grouping compares it: each part
// trimmed, its inner spaces collapsed and case folded, the country upper
// case; "" for none. The delivery name is not part of it.
func addressKey(a *contracts.CustomerAddressEntry) string {
	if a == nil {
		return ""
	}
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	return strings.Join([]string{norm(a.Line1), norm(a.Line2), norm(a.PostalCode), norm(a.City), strings.ToUpper(strings.TrimSpace(a.Country))}, "\x1f")
}

// orderGroup is one draft to make: a customer's orders, in order number
// order, and what kept it apart from the customer's other drafts.
type orderGroup struct {
	customerID  int32
	key         orderGroupKey
	orders      []contracts.DueOrder
	separatedBy []string
}

// groupOrders is O39's grouping of orders into drafts, by customer and then
// by each order's first appearance: one per order, or with combine the orders
// of a customer that share currency, your reference, project, payment terms
// and delivery address. With combine, a customer's drafts each say which of
// the five set them apart from the customer's others.
func groupOrders(orders []contracts.DueOrder, combine bool) []orderGroup {
	sorted := slices.Clone(orders)
	slices.SortStableFunc(sorted, func(a, b contracts.DueOrder) int {
		return cmp.Or(cmp.Compare(a.CustomerID, b.CustomerID), cmp.Compare(keyOf(a).kind, keyOf(b).kind),
			cmp.Compare(a.Number, b.Number), cmp.Compare(a.ID, b.ID))
	})
	var groups []orderGroup
	for _, o := range sorted {
		key := groupKeyOf(o)
		if combine {
			i := slices.IndexFunc(groups, func(g orderGroup) bool { return g.customerID == o.CustomerID && g.key == key })
			if i >= 0 {
				groups[i].orders = append(groups[i].orders, o)
				continue
			}
		}
		groups = append(groups, orderGroup{customerID: o.CustomerID, key: key, orders: []contracts.DueOrder{o}})
	}
	for i := range groups {
		groups[i].separatedBy = []string{}
		if !combine {
			continue
		}
		for _, other := range groups {
			if other.customerID != groups[i].customerID || other.key == groups[i].key {
				continue
			}
			g := &groups[i]
			for _, f := range []struct {
				name    string
				differs bool
			}{
				{separatedByCurrency, g.key.currency != other.key.currency},
				{separatedByYourReference, g.key.yourReference != other.key.yourReference},
				{separatedByProject, g.key.project != other.key.project},
				{separatedByPaymentTerms, g.key.terms != other.key.terms},
				{separatedByAddress, g.key.address != other.key.address},
			} {
				if f.differs && !slices.Contains(g.separatedBy, f.name) {
					g.separatedBy = append(g.separatedBy, f.name)
				}
			}
		}
	}
	return groups
}

// orderHeader is a draft's header from its orders (O39). A subscription is
// no order: a draft of one subscription carries no order reference.
type orderHeader struct {
	yourReference, orderReference          string
	terms                                  *int32
	deliveryDate, deliveryFrom, deliveryTo pgtype.Date
	address                                *gen.InvoicesDeliveryAddress
}

// headerOf is a group's header: the orders' your reference, else the
// customer's buyer reference; their payment terms, else defaultTerms; the
// order number as the order reference when the draft holds one order; the
// latest delivery date of the orders, today when none has one — or, when the
// draft bills a subscription period, the span from the earliest period's
// first day to the latest's last, which takes the orders' delivery dates in
// too; and the delivery address when every order has the same one, which a
// place of delivery must be complete to be.
func headerOf(g orderGroup, rows []heldSource, buyerReference string, defaultTerms *int32, today time.Time) orderHeader {
	h := orderHeader{yourReference: g.key.yourReference, terms: defaultTerms}
	if h.yourReference == "" {
		h.yourReference = buyerReference
	}
	if utf8.RuneCountInString(h.yourReference) > 100 {
		h.yourReference = string([]rune(h.yourReference)[:100])
	}
	if g.key.terms >= 0 {
		terms := g.key.terms
		h.terms = &terms
	}
	if len(g.orders) == 1 && keyOf(g.orders[0]).kind == contracts.DueKindOrder {
		h.orderReference = strconv.FormatInt(g.orders[0].Number, 10)
	}
	var latest *time.Time
	for _, o := range g.orders {
		if o.DeliveryDate != nil {
			d := utcDay(*o.DeliveryDate)
			if latest == nil || d.After(*latest) {
				latest = &d
			}
		}
	}
	var first, last *time.Time
	for _, r := range rows {
		if r.periodFrom == nil {
			continue
		}
		if first == nil || r.periodFrom.Before(*first) {
			first = r.periodFrom
		}
		if last == nil || r.date.After(*last) {
			d := r.date
			last = &d
		}
	}
	switch {
	case first != nil:
		if latest != nil && latest.Before(*first) {
			first = latest
		}
		if latest != nil && latest.After(*last) {
			last = latest
		}
		h.deliveryFrom, h.deliveryTo = pgDate(*first), pgDate(*last)
	case latest != nil:
		h.deliveryDate = pgDate(*latest)
	default:
		h.deliveryDate = pgDate(today)
	}
	h.address = sharedAddress(g.orders)
	return h
}

// sharedAddress is the orders' delivery address when every one has the same
// complete one — a first line, a city and a two-letter country — else nil.
func sharedAddress(orders []contracts.DueOrder) *gen.InvoicesDeliveryAddress {
	var out *gen.InvoicesDeliveryAddress
	for _, o := range orders {
		a := o.DeliveryAddress
		if a == nil {
			return nil
		}
		addr := gen.InvoicesDeliveryAddress{
			Line1: strings.TrimSpace(a.Line1), City: strings.TrimSpace(a.City), Country: strings.ToUpper(strings.TrimSpace(a.Country)),
		}
		if v := strings.TrimSpace(a.Line2); v != "" {
			addr.Line2 = &v
		}
		if v := strings.TrimSpace(a.PostalCode); v != "" {
			addr.PostalCode = &v
		}
		if addr.Line1 == "" || addr.City == "" || !validCountry(addr.Country) {
			return nil
		}
		if out != nil && !sameDeliveryAddress(*out, addr) {
			return nil
		}
		out = &addr
	}
	return out
}

func sameDeliveryAddress(a, b gen.InvoicesDeliveryAddress) bool {
	return a.Line1 == b.Line1 && a.City == b.City && a.Country == b.Country &&
		deref(a.Line2) == deref(b.Line2) && deref(a.PostalCode) == deref(b.PostalCode)
}

// orderDraftLines is a group's lines and their sources (O39), in its orders'
// order: on a draft of several documents each one's lines opened by its
// section line; each allocation's line, by period — a subscription's — then
// the line's position. byOrder is each document's allocations; positions each
// line's position; dates each document's held rows' date. held[i] is the source line i holds, if any,
// with the line's position from 1.
func orderDraftLines(language string, g orderGroup, byOrder map[docKey][]contracts.OrderAllocation, positions map[int64]int32,
	dates map[docKey]time.Time, categories map[int32]int32,
) ([]draftLine, []heldSource, error) {
	var lines []draftLine
	var rows []heldSource
	for _, o := range g.orders {
		key := keyOf(o)
		allocations := slices.Clone(byOrder[key])
		slices.SortStableFunc(allocations, func(a, b contracts.OrderAllocation) int {
			return cmp.Or(comparePeriods(a, b), cmp.Compare(positions[a.LineID], positions[b.LineID]), cmp.Compare(a.ID, b.ID))
		})
		if len(allocations) == 0 {
			continue
		}
		if len(g.orders) > 1 {
			lines = append(lines, orderSectionLine(language, key.kind, o.Number, o.YourReference, allocations[0].VATCodeID))
		}
		for _, a := range allocations {
			l, err := allocationLine(language, a)
			if err != nil {
				return nil, nil, err
			}
			src, err := allocationSource(a, dates[key], categories)
			if err != nil {
				return nil, nil, err
			}
			lines = append(lines, l)
			src.state, src.linePosition = sourceHeld, int32(len(lines))
			rows = append(rows, src)
		}
	}
	return lines, rows, nil
}

// comparePeriods orders a subscription's allocations by their period's first
// day; an order line's have none and compare equal.
func comparePeriods(a, b contracts.OrderAllocation) int {
	if a.PeriodStart == nil || b.PeriodStart == nil {
		return 0
	}
	return a.PeriodStart.Compare(*b.PeriodStart)
}

// allocationIDs is the order allocations among refs, by id.
func allocationIDs(refs []sourceRef) []int64 {
	var out []int64
	for _, r := range refs {
		if r.kind == contracts.WorkSourceOrderAllocation {
			out = append(out, r.id)
		}
	}
	return out
}
