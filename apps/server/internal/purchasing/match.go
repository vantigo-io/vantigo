package purchasing

// The invoice match (purchasing design P36–P40, A9, A10, A14, A20): which
// order lines a supplier invoice of Accounting's bills. A match is one line
// of the invoice, by its position, billing one order line for a quantity in
// that line's unit (A70). Accounting owns the invoice; Purchasing owns the
// match and order_lines.invoiced, which counts every match, provisional or
// posted — an invoice's raising it, a credit note's lowering it.
//
// Every write of an invoice's matches locks in one order: the invoice's own
// row in purchasing.invoice_states, then the orders in id order, then their
// lines. The supplier invoice is read through contracts.SupplierInvoices on
// the pool before the transaction opens (MB rule 21).

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// A match's states and an invoice's kinds.
const (
	matchProvisional    = "provisional"
	matchPosted         = "posted"
	invoiceKind         = "invoice"
	creditNoteKind      = "credit_note"
	invoiceStateOpen    = "open"
	invoiceStatePost    = "posted"
	invoiceStateRev     = "reversed"
	invoiceStateDeleted = "deleted"
	accountingPosted    = "posted"
	accountingReversed  = "reversed"
)

// maxMatchLines is what one allocation names at most: a draft's lines.
const maxMatchLines = 200

// billable is what of a line there is to invoice: a goods line's received
// quantity; a text or service line's quantity once it is marked delivered.
func billable(l store.PurchasingOrderLine) *big.Rat {
	if l.Kind == kindVariant && l.ProductType != nil && *l.ProductType == typeGoods {
		return ratOrZero(l.Received)
	}
	if l.DeliveredAt != nil {
		return ratOrZero(l.Quantity)
	}
	return new(big.Rat)
}

// toInvoice is what of a line is billable and not yet invoiced, never below
// nothing.
func toInvoice(l store.PurchasingOrderLine) *big.Rat {
	left := new(big.Rat).Sub(billable(l), ratOrZero(l.Invoiced))
	if left.Sign() < 0 {
		return new(big.Rat)
	}
	return left
}

// unitNet is a line's net per unit of its quantity, the discount taken.
func unitNet(l store.PurchasingOrderLine) *big.Rat {
	q := ratOrZero(l.Quantity)
	if q.Sign() == 0 {
		return new(big.Rat)
	}
	return new(big.Rat).Quo(ratOrZero(l.LineNet), q)
}

// netOf is what quantity of a line comes to at the order's net, two places;
// the line's own net when it is the whole line.
func netOf(l store.PurchasingOrderLine, quantity *big.Rat) *big.Rat {
	if quantity.Cmp(ratOrZero(l.Quantity)) == 0 {
		return ratOrZero(l.LineNet)
	}
	return roundTo(new(big.Rat).Mul(quantity, unitNet(l)), amountPlaces)
}

// signed is a match's effect on its line's invoiced quantity.
func signed(kind string, quantity *big.Rat) *big.Rat {
	if kind == creditNoteKind {
		return new(big.Rat).Neg(quantity)
	}
	return new(big.Rat).Set(quantity)
}

// matchWarnings are a match's differences (P38, A9): its line invoiced
// beyond what is received and beyond what is ordered, the invoice line's net
// over the matched quantity against the order's net per unit by more than
// the tolerance in percent, and another currency than the order's.
func matchWarnings(m store.PurchasingInvoiceMatch, o store.PurchasingOrder, l store.PurchasingOrderLine, tolerance *big.Rat) []gen.PurchasingInvoiceWarning {
	out := []gen.PurchasingInvoiceWarning{}
	invoiced := ratOrZero(l.Invoiced)
	if invoiced.Cmp(billable(l)) > 0 {
		out = append(out, gen.InvoicedOverReceived)
	}
	if invoiced.Cmp(ratOrZero(l.Quantity)) > 0 {
		out = append(out, gen.InvoicedOverOrdered)
	}
	if m.InvoiceCurrency != o.Currency {
		return append(out, gen.CurrencyDiffers)
	}
	if priceDiffers(ratOrZero(m.LineNet), netOf(l, ratOrZero(m.Quantity)), tolerance) {
		out = append(out, gen.PriceDiffers)
	}
	return out
}

// differences counts the matches' warnings: a line's own — invoiced beyond
// what is received or ordered — once per order line however many matches
// bill it, a match's own once per match.
func differences(views []gen.PurchasingInvoiceMatch) int32 {
	seen := map[int64]map[gen.PurchasingInvoiceWarning]bool{}
	n := int32(0)
	for _, v := range views {
		for _, w := range v.Warnings {
			if w == gen.InvoicedOverReceived || w == gen.InvoicedOverOrdered {
				if seen[v.OrderLineId] == nil {
					seen[v.OrderLineId] = map[gen.PurchasingInvoiceWarning]bool{}
				}
				if seen[v.OrderLineId][w] {
					continue
				}
				seen[v.OrderLineId][w] = true
			}
			n++
		}
	}
	return n
}

// priceDiffers reports whether an invoice line's net differs from what the
// order makes of the same quantity by more than tolerance percent of it. Both
// are amounts of the same quantity, so a unit price's rounding is no
// difference.
func priceDiffers(invoiced, ordered, tolerance *big.Rat) bool {
	diff := new(big.Rat).Abs(new(big.Rat).Sub(invoiced, ordered))
	if diff.Sign() == 0 {
		return false
	}
	if ordered.Sign() == 0 {
		return true
	}
	limit := new(big.Rat).Mul(new(big.Rat).Abs(ordered), new(big.Rat).Quo(tolerance, big.NewRat(100, 1)))
	return diff.Cmp(limit) > 0
}

// referenceNumbers are the whole digit runs of an invoice's reference, the
// order numbers it may name (P36).
var digitRuns = regexp.MustCompile(`[0-9]+`)

func referenceNumbers(reference string) []int64 {
	var out []int64
	for _, run := range digitRuns.FindAllString(reference, -1) {
		if len(run) > 18 {
			continue
		}
		n, err := strconv.ParseInt(run, 10, 64)
		if err != nil || n <= 0 || slices.Contains(out, n) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// matchContext is what a match view is built from: the orders and their
// lines by id, and the settings' tolerance.
type matchContext struct {
	orders    map[int64]store.PurchasingOrder
	lines     map[int64]store.PurchasingOrderLine
	tolerance *big.Rat
}

// readMatchContext reads the orders of the matches with their lines and the
// tolerance, on q.
func readMatchContext(ctx context.Context, q *store.Queries, matches []store.PurchasingInvoiceMatch, extraOrders ...int64) (matchContext, error) {
	ids := append([]int64{}, extraOrders...)
	for _, m := range matches {
		ids = append(ids, m.OrderID)
	}
	ids = distinctSorted(ids)
	mc := matchContext{orders: map[int64]store.PurchasingOrder{}, lines: map[int64]store.PurchasingOrderLine{}}
	st, err := readSettings(ctx, q)
	if err != nil {
		return mc, err
	}
	mc.tolerance = st.priceTolerance
	if len(ids) == 0 {
		return mc, nil
	}
	orders, err := q.MatchedOrdersOf(ctx, ids)
	if err != nil {
		return mc, fmt.Errorf("purchasing: read the matched orders: %w", err)
	}
	for _, o := range orders {
		mc.orders[o.ID] = o
	}
	lines, err := q.OrderLinesOf(ctx, ids)
	if err != nil {
		return mc, fmt.Errorf("purchasing: read the matched orders' lines: %w", err)
	}
	for _, l := range lines {
		mc.lines[l.ID] = l
	}
	return mc, nil
}

// view is one match as the API answers it; the invoice line's amounts only
// when visible (A20).
func (mc matchContext) view(m store.PurchasingInvoiceMatch, visible bool) gen.PurchasingInvoiceMatch {
	o, l := mc.orders[m.OrderID], mc.lines[m.OrderLineID]
	name := l.Name
	if name == "" {
		name = l.Description
	}
	out := gen.PurchasingInvoiceMatch{
		Id: m.ID, SupplierInvoiceId: m.SupplierInvoiceID, InvoiceNumber: m.InvoiceNumber,
		InvoiceDate: openapi_types.Date{Time: m.InvoiceDate.Time}, Currency: m.InvoiceCurrency,
		Kind: m.Kind, State: m.State,
		LinePosition: m.LinePosition, OrderId: m.OrderID, OrderNumber: o.Number, OrderLineId: m.OrderLineID,
		OrderLinePosition: l.Position, Name: name, Unit: l.Unit, Quantity: trimmed(ratOrZero(m.Quantity), quantityPlaces),
		OrderUnitNet: priceText(roundTo(unitNet(l), pricePlaces)), Warnings: matchWarnings(m, o, l, mc.tolerance),
	}
	if visible {
		net := ratOrZero(m.LineNet)
		out.LineNet = ptr(amountText(net))
		if q := ratOrZero(m.Quantity); q.Sign() > 0 {
			out.InvoiceUnitNet = ptr(priceText(roundTo(new(big.Rat).Quo(net, q), pricePlaces)))
		}
	}
	return out
}

// invoiceOf reads the supplier invoice on the pool: 404 when it does not
// exist.
func (s *server) invoiceOf(ctx context.Context, id int64) (*contracts.SupplierInvoice, error) {
	inv, err := s.supplierInvoice(ctx, id)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, notFound("supplier invoice")
	}
	return inv, nil
}

// panel is the Purchase order panel of an invoice (P36–P38, A20).
func (s *server) panel(ctx context.Context, inv *contracts.SupplierInvoice) (gen.PurchasingInvoicePanel, error) {
	visible := s.has(ctx, permAccountingRead)
	sup, err := s.supplier(ctx, inv.SupplierID)
	if err != nil {
		return gen.PurchasingInvoicePanel{}, err
	}
	out := gen.PurchasingInvoicePanel{
		InvoiceId: inv.ID, SupplierId: inv.SupplierID, Kind: inv.Kind, Number: inv.Number,
		InvoiceDate: openapi_types.Date{Time: inv.InvoiceDate}, Currency: inv.Currency, State: inv.State,
		LinesVisible: visible, Lines: []gen.PurchasingInvoiceLine{}, Matches: []gen.PurchasingInvoiceMatch{},
		Suggestions: []gen.PurchasingInvoiceSuggestion{},
	}
	if sup != nil {
		out.SupplierName = sup.Name
	}
	if visible {
		out.Reference = ptr(inv.Reference)
	}
	for _, l := range inv.Lines {
		line := gen.PurchasingInvoiceLine{Position: l.Position}
		if visible {
			line.Text, line.Net = ptr(l.Text), ptr(l.Net)
		}
		out.Lines = append(out.Lines, line)
	}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		matches, err := q.InvoiceMatches(ctx, inv.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read invoice %d's matches: %w", inv.ID, err)
		}
		var suggested []store.PurchasingOrder
		if numbers := referenceNumbers(inv.Reference); len(numbers) > 0 {
			if suggested, err = q.SuggestedOrders(ctx, store.SuggestedOrdersParams{SupplierID: inv.SupplierID, Numbers: numbers}); err != nil {
				return fmt.Errorf("purchasing: read the orders invoice %d names: %w", inv.ID, err)
			}
		}
		extra := make([]int64, 0, len(suggested))
		for _, o := range suggested {
			extra = append(extra, o.ID)
		}
		mc, err := readMatchContext(ctx, q, matches, extra...)
		if err != nil {
			return err
		}
		for _, m := range matches {
			out.Matches = append(out.Matches, mc.view(m, visible))
		}
		out.Differences = differences(out.Matches)
		left := map[int64]int32{}
		for _, l := range mc.lines {
			if toInvoice(l).Sign() > 0 {
				left[l.OrderID]++
			}
		}
		for _, o := range suggested {
			out.Suggestions = append(out.Suggestions, gen.PurchasingInvoiceSuggestion{OrderId: o.ID, Number: deref64(o.Number),
				Status: o.Status, Currency: o.Currency, NetTotal: amountText(ratOrZero(o.NetTotal)), ToInvoice: left[o.ID]})
		}
		return nil
	})
	return out, err
}

// ownPanel is the panel from what Purchasing keeps of the invoice alone —
// for a caller without accounting:read (A20, A83), and for an invoice
// Accounting no longer has (A79): the header the matches were made with,
// the matches with no amounts, no lines and no suggestions. header names the
// invoice when no match is left; with neither it is not found.
func (s *server) ownPanel(ctx context.Context, id int64, header *store.PurchasingInvoiceMatch) (gen.PurchasingInvoicePanel, error) {
	out := gen.PurchasingInvoicePanel{InvoiceId: id, Lines: []gen.PurchasingInvoiceLine{}, Matches: []gen.PurchasingInvoiceMatch{},
		Suggestions: []gen.PurchasingInvoiceSuggestion{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		matches, err := q.InvoiceMatches(ctx, id)
		if err != nil {
			return fmt.Errorf("purchasing: read invoice %d's matches: %w", id, err)
		}
		h := header
		if len(matches) > 0 {
			h = &matches[0]
		}
		if h == nil {
			return notFound("supplier invoice")
		}
		mc, err := readMatchContext(ctx, q, matches, h.OrderID)
		if err != nil {
			return err
		}
		o := mc.orders[h.OrderID]
		out.SupplierId, out.SupplierName = o.SupplierID, o.SupplierName
		out.Kind, out.Number, out.Currency = h.Kind, h.InvoiceNumber, h.InvoiceCurrency
		out.InvoiceDate = openapi_types.Date{Time: h.InvoiceDate.Time}
		out.State = "draft"
		st, err := q.InvoiceState(ctx, id)
		if err != nil && !noRows(err) {
			return fmt.Errorf("purchasing: read invoice %d's state: %w", id, err)
		}
		if st.State != "" && st.State != invoiceStateOpen {
			out.State = st.State
		}
		for _, m := range matches {
			out.Matches = append(out.Matches, mc.view(m, false))
		}
		out.Differences = differences(out.Matches)
		return nil
	})
	return out, err
}

// answerPanel is the panel a write answers: Accounting's invoice to a holder
// of accounting:read, Purchasing's own facts otherwise or when the invoice
// is gone.
func (s *server) answerPanel(ctx context.Context, id int64, inv *contracts.SupplierInvoice, header *store.PurchasingInvoiceMatch) (gen.PurchasingInvoicePanel, error) {
	if inv != nil && s.has(ctx, permAccountingRead) {
		return s.panel(ctx, inv)
	}
	return s.ownPanel(ctx, id, header)
}

// invoiceAnswers are the typed refusals the four invoice operations share.
func invoiceAnswers[R any](bad, notFound, conflict, unavailable func(gen.PurchasingProblem) R) answers[R] {
	return answers[R]{badRequest: bad, notFound: notFound, conflict: conflict, unavailable: unavailable}
}

// GetPurchasingInvoiceMatch Get a supplier invoice's match
// (GET /api/v1/purchasing/supplier-invoices/{invoiceId}/match)
func (s *server) GetPurchasingInvoiceMatch(ctx context.Context, req gen.GetPurchasingInvoiceMatchRequestObject) (gen.GetPurchasingInvoiceMatchResponseObject, error) {
	resp := invoiceAnswers[gen.GetPurchasingInvoiceMatchResponseObject](nil,
		func(p gen.PurchasingProblem) gen.GetPurchasingInvoiceMatchResponseObject {
			return gen.GetPurchasingInvoiceMatch404ApplicationProblemPlusJSONResponse(p)
		}, nil,
		func(p gen.PurchasingProblem) gen.GetPurchasingInvoiceMatchResponseObject {
			return gen.GetPurchasingInvoiceMatch503ApplicationProblemPlusJSONResponse(p)
		})
	// Without accounting:read Accounting is not asked at all: the caller
	// learns of an invoice only what its matches already tell (A83).
	if !s.has(ctx, permAccountingRead) {
		out, err := s.ownPanel(ctx, req.InvoiceId, nil)
		if err != nil {
			return answer(err, resp)
		}
		return gen.GetPurchasingInvoiceMatch200JSONResponse(out), nil
	}
	// An invoice Accounting no longer has is read from what Purchasing keeps
	// of it while a match is left (A79).
	inv, err := s.supplierInvoice(ctx, req.InvoiceId)
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.answerPanel(ctx, req.InvoiceId, inv, nil)
	if err != nil {
		return answer(err, resp)
	}
	return gen.GetPurchasingInvoiceMatch200JSONResponse(out), nil
}

// allocation is one invoice line billing one order line.
type allocation struct {
	index       int
	position    int32
	orderLineID int64
	quantity    *big.Rat
}

// judgeInvoiceFor refuses to match an invoice to an order it cannot bill: a
// reversed invoice, another supplier's order, another currency, an order
// not sent (P37, A9).
func judgeInvoiceFor(inv *contracts.SupplierInvoice, o store.PurchasingOrder) error {
	switch {
	case inv.State == accountingReversed:
		return conflict(codeInvoiceReversed, "The invoice is reversed", "A reversed invoice is matched to nothing; match the copy made of it.")
	case o.Status == statusDraft || o.Status == statusCancelled:
		return conflict(codeOrderNotSent, "The order is not sent", "Only a sent order is matched to an invoice.")
	case inv.SupplierID != o.SupplierID:
		return conflict(codeInvoiceSupplierDiffers, "The order is another supplier's",
			"The invoice and the order name different suppliers.")
	case inv.Currency != o.Currency:
		return conflict(codeInvoiceCurrencyDiffers, "The invoice is in another currency",
			fmt.Sprintf("The invoice is in %s and the order in %s.", inv.Currency, o.Currency))
	}
	return nil
}

// lineNetAt is the invoice line's net at a position, false when the invoice
// has no line there.
func lineNetAt(inv *contracts.SupplierInvoice, position int32) (*big.Rat, bool) {
	for _, l := range inv.Lines {
		if l.Position == position {
			return mustRat(l.Net), true
		}
	}
	return nil, false
}

// conflictLines is a 409 naming every failing line under code.
func conflictLines(title, code, detail string, failures []lineFailure) error {
	p := linesProblem(title, failures)
	status := int32(http.StatusConflict)
	p.Status, p.Code, p.Detail = &status, &code, &detail
	return refuse(p)
}

// lockInvoice takes the invoice's own row FOR UPDATE, made when it has none:
// the first lock of every write of its matches.
func lockInvoice(ctx context.Context, txq *store.Queries, id int64, now time.Time) (store.PurchasingInvoiceState, error) {
	if err := txq.EnsureInvoiceState(ctx, store.EnsureInvoiceStateParams{ID: id, Now: now}); err != nil {
		return store.PurchasingInvoiceState{}, fmt.Errorf("purchasing: make invoice %d's row: %w", id, err)
	}
	st, err := txq.InvoiceStateForUpdate(ctx, id)
	if err != nil {
		return st, fmt.Errorf("purchasing: lock invoice %d: %w", id, err)
	}
	return st, nil
}

// matchInsert is a new match's row from the invoice as read.
func matchInsert(inv *contracts.SupplierInvoice, position int32, o store.PurchasingOrder, lineID int64, quantity, net *big.Rat,
	st store.PurchasingInvoiceState, by actor, now time.Time) store.InsertMatchParams {
	p := store.InsertMatchParams{SupplierInvoiceID: inv.ID, LinePosition: position, OrderID: o.ID, OrderLineID: lineID,
		Kind: inv.Kind, Quantity: numeric(quantity, quantityPlaces), LineNet: numeric(net, amountPlaces), InvoiceNumber: inv.Number,
		InvoiceDate: dayCol(inv.InvoiceDate), InvoiceCurrency: inv.Currency, State: matchProvisional,
		MatchedByUserID: by.id, MatchedByDisplay: by.display, Now: now}
	if st.State == invoiceStatePost {
		p.State, p.PostedAt = matchPosted, &now
	}
	return p
}

// allocate writes the allocations of one invoice to one order under the
// invoice's and the order's locks, judging each against the line as it
// stands then: an invoice line matched to another order line
// (invoice_line_matched), more than received and not invoiced without
// acceptOver (over_invoiced), a credit note beyond what is invoiced
// (credit_exceeds_invoiced, A14). A line matched to the same order line
// again takes the new quantity.
func (s *server) allocate(ctx context.Context, inv *contracts.SupplierInvoice, orderID int64, allocs []allocation, acceptOver bool, by actor) error {
	return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		now := s.deps.Clock()
		st, err := lockInvoice(ctx, txq, inv.ID, now)
		if err != nil {
			return err
		}
		switch {
		case st.State == invoiceStateRev:
			return conflict(codeInvoiceReversed, "The invoice is reversed", "A reversed invoice is matched to nothing; match the copy made of it.")
		case st.State == invoiceStateDeleted:
			return notFound("supplier invoice")
		case st.State == invoiceStateOpen && inv.State == accountingPosted:
			// Posted before Purchasing took its event — before delivery D, or
			// with the event still queued: the read moves it forward, so the
			// match is posted and followed up (A80).
			if err := markPosted(ctx, txq, postedFacts{id: inv.ID, supplierID: inv.SupplierID, kind: inv.Kind, number: inv.Number,
				date: inv.InvoiceDate, currency: inv.Currency}, now); err != nil {
				return err
			}
			st.State = invoiceStatePost
		}
		o, err := txq.LockOrder(ctx, orderID)
		if noRows(err) {
			return notFound("purchase order")
		}
		if err != nil {
			return fmt.Errorf("purchasing: lock order %d: %w", orderID, err)
		}
		if err := judgeInvoiceFor(inv, o); err != nil {
			return err
		}
		current, err := txq.OrderLines(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's lines: %w", o.ID, err)
		}
		lines := make(map[int64]store.PurchasingOrderLine, len(current))
		for _, l := range current {
			lines[l.ID] = l
		}
		existing, err := txq.InvoiceMatches(ctx, inv.ID)
		if err != nil {
			return fmt.Errorf("purchasing: read invoice %d's matches: %w", inv.ID, err)
		}
		at := make(map[int32]store.PurchasingInvoiceMatch, len(existing))
		for _, m := range existing {
			at[m.LinePosition] = m
		}
		var matched, over, credit []lineFailure
		delta := map[int64]*big.Rat{}
		var lineOrder []int64
		for _, a := range allocs {
			if _, ok := lines[a.orderLineID]; !ok {
				return refuseLines(invalidMatchTitle, []lineFailure{{index: a.index, code: codeLineNotFound, detail: "Not a line of this order."}})
			}
			change := signed(inv.Kind, a.quantity)
			if m, ok := at[a.position]; ok {
				if m.OrderLineID != a.orderLineID {
					matched = append(matched, lineFailure{index: a.index, code: codeInvoiceLineMatched,
						detail: fmt.Sprintf("Invoice line %d is matched to another order line. Unmatch it first.", a.position)})
					continue
				}
				change.Sub(change, signed(m.Kind, ratOrZero(m.Quantity)))
			}
			if _, ok := delta[a.orderLineID]; !ok {
				delta[a.orderLineID] = new(big.Rat)
				lineOrder = append(lineOrder, a.orderLineID)
			}
			delta[a.orderLineID].Add(delta[a.orderLineID], change)
		}
		if len(matched) > 0 {
			return conflictLines("The invoice line is matched", codeInvoiceLineMatched,
				"An invoice line bills one order line. Unmatch it before matching it to another.", matched)
		}
		for _, a := range allocs {
			l := lines[a.orderLineID]
			after := new(big.Rat).Add(ratOrZero(l.Invoiced), delta[a.orderLineID])
			switch {
			case after.Sign() < 0:
				credit = append(credit, lineFailure{index: a.index, code: codeCreditExceedsInvoiced, detail: fmt.Sprintf(
					"%s: %s %s is invoiced; the credit note would take it below nothing.", l.Name, trimmed(ratOrZero(l.Invoiced), quantityPlaces), l.Unit)})
			case inv.Kind != creditNoteKind && !acceptOver && delta[a.orderLineID].Sign() > 0 && after.Cmp(billable(l)) > 0:
				over = append(over, lineFailure{index: a.index, code: codeOverInvoiced, detail: fmt.Sprintf(
					"%s: %s %s is received and %s invoiced.", l.Name, trimmed(billable(l), quantityPlaces), l.Unit, trimmed(ratOrZero(l.Invoiced), quantityPlaces))})
			}
		}
		if len(credit) > 0 {
			return conflictLines("The credit note is more than is invoiced", codeCreditExceedsInvoiced,
				"A credit note lowers what is invoiced on a line, never below nothing: unmatch the credit note first.", credit)
		}
		if len(over) > 0 {
			return conflictLines("More than is received", codeOverInvoiced,
				"The invoice bills more than is received and not invoiced. Accept it to match anyway.", over)
		}
		queued := false
		for _, a := range allocs {
			net, _ := lineNetAt(inv, a.position)
			if m, ok := at[a.position]; ok {
				if err := txq.DeleteMatch(ctx, m.ID); err != nil {
					return fmt.Errorf("purchasing: replace match %d: %w", m.ID, err)
				}
			}
			p := matchInsert(inv, a.position, o, a.orderLineID, a.quantity, net, st, by, now)
			if _, err := txq.InsertMatch(ctx, p); err != nil {
				return fmt.Errorf("purchasing: match invoice %d line %d: %w", inv.ID, a.position, err)
			}
			queued = queued || p.State == matchPosted
		}
		for _, id := range lineOrder {
			if err := txq.AddInvoiced(ctx, store.AddInvoicedParams{ID: id, Delta: numeric(delta[id], quantityPlaces)}); err != nil {
				return fmt.Errorf("purchasing: move line %d's invoiced: %w", id, err)
			}
		}
		if queued {
			return txq.QueueFollowup(ctx, store.QueueFollowupParams{ID: inv.ID, Now: now})
		}
		return nil
	})
}

// MatchPurchasingInvoice Match a supplier invoice to a purchase order
// (POST /api/v1/purchasing/supplier-invoices/{invoiceId}/matches)
func (s *server) MatchPurchasingInvoice(ctx context.Context, req gen.MatchPurchasingInvoiceRequestObject) (gen.MatchPurchasingInvoiceResponseObject, error) {
	resp := invoiceAnswers(
		func(p gen.PurchasingProblem) gen.MatchPurchasingInvoiceResponseObject {
			return gen.MatchPurchasingInvoice400ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.MatchPurchasingInvoiceResponseObject {
			return gen.MatchPurchasingInvoice404ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.MatchPurchasingInvoiceResponseObject {
			return gen.MatchPurchasingInvoice409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.MatchPurchasingInvoiceResponseObject {
			return gen.MatchPurchasingInvoice503ApplicationProblemPlusJSONResponse(p)
		})
	b := *req.Body
	if len(b.Lines) == 0 || len(b.Lines) > maxMatchLines {
		return answer(refuseField(invalidMatchTitle, "lines", "", fmt.Sprintf("A match names 1 to %d lines", maxMatchLines)), resp)
	}
	inv, err := s.invoiceOf(ctx, req.InvoiceId)
	if err != nil {
		return answer(err, resp)
	}
	var failures []lineFailure
	allocs := make([]allocation, 0, len(b.Lines))
	seen := map[int32]bool{}
	for i, l := range b.Lines {
		q, ok := parseDecimal(l.Quantity, quantityPlaces, maxQuantity)
		_, onInvoice := lineNetAt(inv, l.LinePosition)
		switch {
		case !onInvoice || seen[l.LinePosition]:
			failures = append(failures, lineFailure{index: i, code: codeInvoiceLineUnknown,
				detail: "The invoice has no line at this position, or the request names it twice."})
		case !ok || q.Sign() <= 0:
			failures = append(failures, lineFailure{index: i, code: codeQuantityInvalid,
				detail: "A quantity is more than 0, with at most three decimals."})
		default:
			allocs = append(allocs, allocation{index: i, position: l.LinePosition, orderLineID: l.OrderLineId, quantity: q})
		}
		seen[l.LinePosition] = true
	}
	if len(failures) > 0 {
		return answer(refuseLines(invalidMatchTitle, failures), resp)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.allocate(ctx, inv, b.OrderId, allocs, b.AcceptOver != nil && *b.AcceptOver, by); err != nil {
		return answer(err, resp)
	}
	out, err := s.answerPanel(ctx, inv.ID, inv, nil)
	if err != nil {
		return answer(err, resp)
	}
	return gen.MatchPurchasingInvoice200JSONResponse(out), nil
}

// MatchAllPurchasingInvoice Match all that is received to a supplier invoice
// (POST /api/v1/purchasing/supplier-invoices/{invoiceId}/match-all)
func (s *server) MatchAllPurchasingInvoice(ctx context.Context, req gen.MatchAllPurchasingInvoiceRequestObject) (gen.MatchAllPurchasingInvoiceResponseObject, error) {
	resp := invoiceAnswers(
		func(p gen.PurchasingProblem) gen.MatchAllPurchasingInvoiceResponseObject {
			return gen.MatchAllPurchasingInvoice400ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.MatchAllPurchasingInvoiceResponseObject {
			return gen.MatchAllPurchasingInvoice404ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.MatchAllPurchasingInvoiceResponseObject {
			return gen.MatchAllPurchasingInvoice409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.MatchAllPurchasingInvoiceResponseObject {
			return gen.MatchAllPurchasingInvoice503ApplicationProblemPlusJSONResponse(p)
		})
	inv, err := s.invoiceOf(ctx, req.InvoiceId)
	if err != nil {
		return answer(err, resp)
	}
	nothing := conflict(codeNothingToMatch, "Nothing to match",
		"The invoice has no unmatched line, or the order nothing received and not invoiced. A credit note is matched line by line.")
	if inv.Kind == creditNoteKind {
		return answer(nothing, resp)
	}
	// The pairing is read on the pool; allocate judges every pair again
	// under the locks.
	o, lines, err := s.orderAndLines(ctx, req.Body.OrderId)
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeInvoiceFor(inv, o); err != nil {
		return answer(err, resp)
	}
	existing, err := s.store().InvoiceMatches(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: read invoice %d's matches: %w", inv.ID, err)
	}
	taken := map[int32]bool{}
	for _, m := range existing {
		taken[m.LinePosition] = true
	}
	var free []int32
	for _, l := range inv.Lines {
		if !taken[l.Position] {
			free = append(free, l.Position)
		}
	}
	slices.Sort(free)
	var allocs []allocation
	for _, l := range lines {
		if len(allocs) == len(free) {
			break
		}
		if left := toInvoice(l); left.Sign() > 0 {
			allocs = append(allocs, allocation{index: len(allocs), position: free[len(allocs)], orderLineID: l.ID, quantity: left})
		}
	}
	if len(allocs) == 0 {
		return answer(nothing, resp)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.allocate(ctx, inv, o.ID, allocs, false, by); err != nil {
		return answer(err, resp)
	}
	out, err := s.answerPanel(ctx, inv.ID, inv, nil)
	if err != nil {
		return answer(err, resp)
	}
	return gen.MatchAllPurchasingInvoice200JSONResponse(out), nil
}

// releaseMatches deletes matches and takes them off their lines' invoiced
// quantities, the orders already locked. A release that would leave a line
// invoiced below nothing — an invoice's match a credit note offsets — is
// refused with credit_exceeds_invoiced; the caller judges it first
// (releasable) where it cannot refuse.
func releaseMatches(ctx context.Context, txq *store.Queries, matches []store.PurchasingInvoiceMatch) error {
	if len(matches) == 0 {
		return nil
	}
	left, err := invoicedAfter(ctx, txq, matches)
	if err != nil {
		return err
	}
	for id, v := range left {
		if v.Sign() < 0 {
			return conflict(codeCreditExceedsInvoiced, "A credit note stands on the line",
				fmt.Sprintf("Order line %d would be left invoiced below nothing: unmatch the credit note first.", id))
		}
	}
	for _, m := range matches {
		if err := txq.DeleteMatch(ctx, m.ID); err != nil {
			return fmt.Errorf("purchasing: release match %d: %w", m.ID, err)
		}
		back := new(big.Rat).Neg(signed(m.Kind, ratOrZero(m.Quantity)))
		if err := txq.AddInvoiced(ctx, store.AddInvoicedParams{ID: m.OrderLineID, Delta: numeric(back, quantityPlaces)}); err != nil {
			return fmt.Errorf("purchasing: move line %d's invoiced: %w", m.OrderLineID, err)
		}
	}
	return nil
}

// invoicedAfter is each line's invoiced quantity once the matches are
// released, read under the orders' locks.
func invoicedAfter(ctx context.Context, txq *store.Queries, matches []store.PurchasingInvoiceMatch) (map[int64]*big.Rat, error) {
	lines, err := txq.OrderLinesOf(ctx, distinctOrders(matches))
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the matched lines: %w", err)
	}
	out := map[int64]*big.Rat{}
	for _, l := range lines {
		out[l.ID] = ratOrZero(l.Invoiced)
	}
	after := map[int64]*big.Rat{}
	for _, m := range matches {
		if _, ok := after[m.OrderLineID]; !ok {
			after[m.OrderLineID] = new(big.Rat).Set(out[m.OrderLineID])
		}
		after[m.OrderLineID].Sub(after[m.OrderLineID], signed(m.Kind, ratOrZero(m.Quantity)))
	}
	return after, nil
}

// releasable is the matches a handler or the worker may release: those whose
// release leaves every line invoiced at nothing or more. A match it keeps —
// an invoice's a credit note offsets — stays, with its warnings, for a
// person to unmatch; a handler never fails on the CHECK (A14).
func releasable(ctx context.Context, txq *store.Queries, matches []store.PurchasingInvoiceMatch) ([]store.PurchasingInvoiceMatch, error) {
	if len(matches) == 0 {
		return nil, nil
	}
	lines, err := txq.OrderLinesOf(ctx, distinctOrders(matches))
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the matched lines: %w", err)
	}
	now := map[int64]*big.Rat{}
	for _, l := range lines {
		now[l.ID] = ratOrZero(l.Invoiced)
	}
	var out []store.PurchasingInvoiceMatch
	// Credit notes' first: releasing one raises what is invoiced.
	ordered := append([]store.PurchasingInvoiceMatch{}, matches...)
	slices.SortStableFunc(ordered, func(a, b store.PurchasingInvoiceMatch) int {
		return signed(a.Kind, big.NewRat(1, 1)).Cmp(signed(b.Kind, big.NewRat(1, 1)))
	})
	for _, m := range ordered {
		after := new(big.Rat).Sub(now[m.OrderLineID], signed(m.Kind, ratOrZero(m.Quantity)))
		if after.Sign() < 0 {
			continue
		}
		now[m.OrderLineID] = after
		out = append(out, m)
	}
	return out, nil
}

// lockOrdersOf locks the orders of the matches, in id order.
func lockOrdersOf(ctx context.Context, txq *store.Queries, matches []store.PurchasingInvoiceMatch) (map[int64]store.PurchasingOrder, error) {
	ids := make([]int64, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.OrderID)
	}
	out := map[int64]store.PurchasingOrder{}
	if len(ids) == 0 {
		return out, nil
	}
	orders, err := txq.LockOrdersByID(ctx, distinctSorted(ids))
	if err != nil {
		return nil, fmt.Errorf("purchasing: lock the matched orders: %w", err)
	}
	for _, o := range orders {
		out[o.ID] = o
	}
	return out, nil
}

// UnmatchPurchasingInvoice Unmatch a supplier invoice
// (POST /api/v1/purchasing/supplier-invoices/{invoiceId}/unmatch)
func (s *server) UnmatchPurchasingInvoice(ctx context.Context, req gen.UnmatchPurchasingInvoiceRequestObject) (gen.UnmatchPurchasingInvoiceResponseObject, error) {
	resp := invoiceAnswers(
		func(p gen.PurchasingProblem) gen.UnmatchPurchasingInvoiceResponseObject {
			return gen.UnmatchPurchasingInvoice400ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.UnmatchPurchasingInvoiceResponseObject {
			return gen.UnmatchPurchasingInvoice404ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.UnmatchPurchasingInvoiceResponseObject {
			return gen.UnmatchPurchasingInvoice409ApplicationProblemPlusJSONResponse(p)
		},
		func(p gen.PurchasingProblem) gen.UnmatchPurchasingInvoiceResponseObject {
			return gen.UnmatchPurchasingInvoice503ApplicationProblemPlusJSONResponse(p)
		})
	// An invoice Accounting no longer has — a deleted draft whose match a
	// credit note holds (A79) — is unmatched from what Purchasing keeps.
	inv, err := s.supplierInvoice(ctx, req.InvoiceId)
	if err != nil {
		return answer(err, resp)
	}
	id := req.InvoiceId
	b := req.Body
	var header *store.PurchasingInvoiceMatch
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := lockInvoice(ctx, txq, id, s.deps.Clock()); err != nil {
			return err
		}
		all, err := txq.InvoiceMatches(ctx, id)
		if err != nil {
			return fmt.Errorf("purchasing: read invoice %d's matches: %w", id, err)
		}
		if inv == nil && len(all) == 0 {
			return notFound("supplier invoice")
		}
		if len(all) > 0 {
			header = &all[0]
		}
		var named []store.PurchasingInvoiceMatch
		for _, m := range all {
			if (b.OrderId == nil || m.OrderID == *b.OrderId) && (b.LinePosition == nil || m.LinePosition == *b.LinePosition) {
				named = append(named, m)
			}
		}
		if len(named) == 0 {
			return conflict(codeNothingToMatch, "Nothing is matched", "Nothing of what the request names is matched to the invoice.")
		}
		if _, err := lockOrdersOf(ctx, txq, named); err != nil {
			return err
		}
		return releaseMatches(ctx, txq, named)
	})
	if err != nil {
		return answer(err, resp)
	}
	out, err := s.answerPanel(ctx, id, inv, header)
	if err != nil {
		return answer(err, resp)
	}
	return gen.UnmatchPurchasingInvoice200JSONResponse(out), nil
}

// ListPurchasingOrderInvoices List the supplier invoices matched to a
// purchase order (GET /api/v1/purchasing/orders/{orderId}/invoices)
func (s *server) ListPurchasingOrderInvoices(ctx context.Context, req gen.ListPurchasingOrderInvoicesRequestObject) (gen.ListPurchasingOrderInvoicesResponseObject, error) {
	visible := s.has(ctx, permAccountingRead)
	out := gen.PurchasingInvoiceOrderMatches{LinesVisible: visible, Matches: []gen.PurchasingInvoiceMatch{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetOrder(ctx, req.OrderId); noRows(err) {
			return notFound("purchase order")
		} else if err != nil {
			return fmt.Errorf("purchasing: read order %d: %w", req.OrderId, err)
		}
		matches, err := q.OrderMatches(ctx, req.OrderId)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's matches: %w", req.OrderId, err)
		}
		mc, err := readMatchContext(ctx, q, matches, req.OrderId)
		if err != nil {
			return err
		}
		for _, m := range matches {
			out.Matches = append(out.Matches, mc.view(m, visible))
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.ListPurchasingOrderInvoicesResponseObject]{
			notFound: func(p gen.PurchasingProblem) gen.ListPurchasingOrderInvoicesResponseObject {
				return gen.ListPurchasingOrderInvoices404ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.ListPurchasingOrderInvoices200JSONResponse(out), nil
}
