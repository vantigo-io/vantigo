package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	invgen "github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	ordgen "github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// Orders become invoices (orders design O36–O41, delivery B), end to end
// against the real modules: Products' catalog prices the order, Invoices'
// mapping taxes it, the From orders wizard allocates its lines through
// Orders' own BillableOrders, the issue stamps them through Orders' own
// holder inside its transaction, and a credit note gives them back. Each
// module's suite proves its half against a fake of the other; this proves
// the halves agree — down to the document's totals being the order's.

const (
	ordersOrdersPath = "/api/v1/orders/orders"
	fromOrdersPath   = invoicesBase + "/from-orders"
	allocationKind   = "orders.allocation"
)

// orderPermissions are what the one caller of an orders installation holds
// beyond customers' and invoices': the catalog, and selling and confirming
// orders.
var orderPermissions = []string{
	"products:products-view", "products:products-manage", "products:variants-view", "products:variants-manage",
	"products:pricing-view", "products:pricing-manage", "products:categories-view",
	"products:tax-categories-view", "products:tax-categories-manage",
	"orders:access", "orders:sales-manage", "orders:sales-confirm", "orders:subscriptions-manage",
}

// ordersInstallation is customers, products, invoices and orders composed in
// the binary's order, the seller complete, with the two tax categories the
// catalog sells under mapped to Invoices' seeded 25 % (1) and 15 % (2)
// codes, a business customer and three priced variants.
type ordersInstallation struct {
	h        *modtest.Harness
	c        *modtest.Client
	customer int32
	shelf    int32 // 1 249.50, 25 %
	coffee   int32 // 89.90, 15 %
	screw    int32 // 3.35, 25 %
}

func newOrdersInstallation(t *testing.T) ordersInstallation {
	t.Helper()
	h, c, _, _ := installInvoices(t, []string{modCustomers, modProducts, modInvoices, modOrders}, orderPermissions)
	standard := taxCategory(t, c, "Standard", 0.25, 1)
	food := taxCategory(t, c, "Næringsmidler", 0.15, 2)
	return ordersInstallation{
		h: h, c: c,
		customer: invoiceNewBusiness(t, c, "Fjord Nord", "Fjord Nord AS", "923609016").Id,
		shelf:    pricedVariant(t, c, standard, "Hylle", "HYL-1", 1249.50),
		coffee:   pricedVariant(t, c, food, "Kaffe", "KAF-1", 89.90),
		screw:    pricedVariant(t, c, standard, "Skrue", "SKR-1", 3.35),
	}
}

// taxCategory creates a Products tax category and maps it to Invoices' VAT
// code vatCode.
func taxCategory(t *testing.T, c *modtest.Client, name string, rate float64, vatCode int32) int32 {
	t.Helper()
	var cat struct {
		Id int32 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/products/tax-categories", map[string]any{"name": name, "kind": "Standard", "rate": rate}, &cat)
	okJSON(t, c, http.MethodPut, fmt.Sprintf("%s/settings/tax-category-vat-codes/%d", invoicesBase, cat.Id), map[string]any{"vatCodeId": vatCode}, nil)
	return cat.Id
}

// pricedVariant creates an active product of one variant under taxCategory
// and gives the variant a NOK list price, answering the variant's id.
func pricedVariant(t *testing.T, c *modtest.Client, taxCategory int32, name, sku string, price float64) int32 {
	t.Helper()
	return pricedVariantOf(t, c, "Goods", taxCategory, name, sku, price)
}

// pricedVariantOf is pricedVariant of a product of type kind, Goods or
// Service.
func pricedVariantOf(t *testing.T, c *modtest.Client, kind string, taxCategory int32, name, sku string, price float64) int32 {
	t.Helper()
	var p struct {
		Id       int32 `json:"id"`
		Variants []struct {
			Id int32 `json:"id"`
		} `json:"variants"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/products", map[string]any{
		"name": name, "type": kind, "status": "Active", "taxCategoryId": taxCategory,
		"variants": []map[string]any{{"sku": sku}},
	}, &p)
	okJSON(t, c, http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", p.Id, p.Variants[0].Id),
		map[string]any{"currency": "NOK", "amount": price}, nil)
	return p.Variants[0].Id
}

// orderLine is a catalog line of qty of variant at discount percent.
func orderLine(variant int32, qty, discount float64) ordgen.OrdersLineInput {
	l := ordgen.OrdersLineInput{Kind: ordgen.OrdersLineInputKindProduct, VariantId: &variant, Quantity: &qty}
	if discount != 0 {
		l.DiscountPercent = &discount
	}
	return l
}

// confirmedOrder creates an order for the customer with lines and confirms it.
func (o ordersInstallation) confirmedOrder(t *testing.T, yourReference string, lines ...ordgen.OrdersLineInput) ordgen.OrdersOrder {
	t.Helper()
	var draft ordgen.OrdersOrder
	okJSON(t, o.c, http.MethodPost, ordersOrdersPath, ordgen.OrdersOrderInput{
		ClientRequestId: uuid.New(), CustomerId: o.customer, Lines: &lines, YourReference: &yourReference,
	}, &draft)
	var confirmed ordgen.OrdersOrder
	okJSON(t, o.c, http.MethodPost, fmt.Sprintf("%s/%d/confirm", ordersOrdersPath, draft.Id),
		ordgen.OrdersVersionInput{ExpectedUpdatedAt: draft.UpdatedAt, Version: draft.Version}, &confirmed)
	if confirmed.Status != ordgen.OrdersOrderStatusConfirmed {
		t.Fatalf("order %d = %s, want confirmed", draft.Id, confirmed.Status)
	}
	return confirmed
}

// order is an order as it now stands.
func (o ordersInstallation) order(t *testing.T, id int64) ordgen.OrdersOrder {
	t.Helper()
	var got ordgen.OrdersOrder
	okJSON(t, o.c, http.MethodGet, fmt.Sprintf("%s/%d", ordersOrdersPath, id), nil, &got)
	return got
}

// deliverAll marks every line of the order delivered in full, by hand.
func (o ordersInstallation) deliverAll(t *testing.T, id int64) {
	t.Helper()
	cur := o.order(t, id)
	var lines []ordgen.OrdersDeliveredLine
	for _, l := range cur.Lines {
		lines = append(lines, ordgen.OrdersDeliveredLine{LineId: l.Id, Quantity: *l.Quantity - l.QtyDelivered})
	}
	okJSON(t, o.c, http.MethodPost, fmt.Sprintf("%s/%d/mark-delivered", ordersOrdersPath, id),
		ordgen.OrdersDeliveredInput{ExpectedUpdatedAt: cur.UpdatedAt, Version: cur.Version, Lines: lines}, nil)
}

// pickLine is one line of a From orders body.
func pickLine(order, line int64, qty float64) invgen.InvoicesFromOrdersLine {
	return invgen.InvoicesFromOrdersLine{OrderId: order, LineId: line, Quantity: qty}
}

// whole is every line of the order at its full quantity.
func whole(o ordgen.OrdersOrder) []invgen.InvoicesFromOrdersLine {
	var out []invgen.InvoicesFromOrdersLine
	for _, l := range o.Lines {
		out = append(out, pickLine(o.Id, l.Id, *l.Quantity))
	}
	return out
}

// fromOrders runs the wizard and answers the drafts it made.
func (o ordersInstallation) fromOrders(t *testing.T, combine bool, lines ...invgen.InvoicesFromOrdersLine) []invgen.InvoicesFromOrdersDraft {
	t.Helper()
	res := o.c.Do(http.MethodPost, fromOrdersPath, invgen.InvoicesFromOrdersRequest{Combine: combine, Lines: lines})
	if res.Status != http.StatusCreated {
		t.Fatalf("POST %s = %d %s, want 201", fromOrdersPath, res.Status, res.Body)
	}
	var out invgen.InvoicesFromOrdersResponse
	res.JSON(&out)
	return out.Invoices
}

// toInvoice is the due list's quantity to invoice of each of the order's
// lines, by line id.
func (o ordersInstallation) toInvoice(t *testing.T, order int64) map[int64]float64 {
	t.Helper()
	var due invgen.InvoicesDueOrdersResponse
	okJSON(t, o.c, http.MethodGet, fmt.Sprintf("%s/due?orderId=%d", fromOrdersPath, order), nil, &due)
	out := map[int64]float64{}
	for _, d := range due.Orders {
		for _, l := range d.Lines {
			out[l.LineId] = l.ToInvoice
		}
	}
	return out
}

// doc is an invoices document as it now stands.
func (o ordersInstallation) doc(t *testing.T, id int64) invgen.InvoicesInvoiceResponse {
	t.Helper()
	var d invgen.InvoicesInvoiceResponse
	okJSON(t, o.c, http.MethodGet, invoiceAt(id), nil, &d)
	return d
}

// issue issues document id today.
func (o ordersInstallation) issue(t *testing.T, id int64) invgen.InvoicesInvoiceResponse {
	t.Helper()
	var d invgen.InvoicesInvoiceResponse
	okJSON(t, o.c, http.MethodPost, invoiceAt(id)+"/issue", map[string]any{}, &d)
	if d.Status != "issued" || d.Number == nil {
		t.Fatalf("issue %d = %s number %v, want issued and numbered", id, d.Status, d.Number)
	}
	return d
}

// allocations is the document's held order allocations' ids, line by line.
func allocations(d invgen.InvoicesInvoiceResponse) []int64 {
	var out []int64
	for _, l := range d.Lines {
		if l.Sources == nil {
			continue
		}
		for _, s := range *l.Sources {
			if s.Kind == allocationKind {
				out = append(out, s.Id)
			}
		}
	}
	return out
}

// invoicedEvents counts orders.sales_order.invoiced about the order.
func invoicedEvents(t *testing.T, h *modtest.Harness, order int64) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM platform.events WHERE name = 'orders.sales_order.invoiced' AND aggregate_id = $1`,
		fmt.Sprint(order))
}

// An order of three lines, one discounted, invoiced from the wizard: part of
// one line drafted and given back by deleting the draft, and again — its
// draft gone without a release, as a customer's anonymisation deletes it —
// by Invoices' reconcile an hour on; the rest in one
// invoice whose issue is refused while an allocation it holds has changed;
// issued, the order is invoiced and done, lists the invoice and publishes
// once, and the invoice's totals are the order's; a full credit gives every
// quantity back to invoice again.
func TestOrdersInvoicing_AnOrderInvoicedIssuedAndCredited(t *testing.T) {
	o := newOrdersInstallation(t)
	order := o.confirmedOrder(t, "PO-4711",
		orderLine(o.shelf, 10, 12.5), orderLine(o.coffee, 3, 0), orderLine(o.screw, 7, 0))
	shelf := order.Lines[0]

	// Part of a line: four of the ten shelves, drafted at the order's price
	// and discount, held by an allocation the order line counts.
	partial := o.fromOrders(t, false, pickLine(order.Id, shelf.Id, 4))
	if len(partial) != 1 || !slices.Equal(partial[0].OrderIds, []int64{order.Id}) {
		t.Fatalf("the partial wizard made %+v, want one draft of the order", partial)
	}
	draft := o.doc(t, partial[0].InvoiceId)
	if len(draft.Lines) != 1 || draft.Lines[0].Quantity != 4 || draft.Lines[0].UnitPrice != 1249.5 ||
		draft.Lines[0].DiscountPercent != 12.5 || draft.Lines[0].VatCodeId != 1 || len(allocations(draft)) != 1 {
		t.Errorf("the partial draft's lines = %+v, want 4 × 1 249.50 less 12.5 %% on code 1, held by one allocation", draft.Lines)
	}
	if draft.OrderReference != fmt.Sprint(order.Number) || draft.YourReference != "PO-4711" {
		t.Errorf("the partial draft's references = %v / %q, want the order's number and PO-4711", draft.OrderReference, draft.YourReference)
	}
	if l := o.order(t, order.Id).Lines[0]; l.QtyDraftAllocated != 4 || l.QtyInvoiced != 0 {
		t.Errorf("the shelf line = %v draft-allocated, %v invoiced; want 4 and 0", l.QtyDraftAllocated, l.QtyInvoiced)
	}
	if due := o.toInvoice(t, order.Id); due[shelf.Id] != 6 {
		t.Errorf("the shelf line's to invoice = %v, want the 6 left", due[shelf.Id])
	}

	// Deleting the draft gives the allocation back: the ten are offered again.
	okJSON(t, o.c, http.MethodDelete, invoiceAt(draft.Id), nil, nil)
	if l := o.order(t, order.Id).Lines[0]; l.QtyDraftAllocated != 0 {
		t.Errorf("the shelf line after the delete = %v draft-allocated, want 0", l.QtyDraftAllocated)
	}
	if due := o.toInvoice(t, order.Id); due[shelf.Id] != 10 {
		t.Errorf("the shelf line's to invoice after the delete = %v, want all 10 again", due[shelf.Id])
	}
	if n := o.h.Count(t, `SELECT count(*) FROM orders.invoice_allocations WHERE order_id = $1 AND state = 'released'`, order.Id); n != 1 {
		t.Errorf("%d released allocations after the delete, want 1", n)
	}
	if n := o.h.Count(t, `SELECT count(*) FROM invoices.order_allocations_taken WHERE released_at IS NOT NULL`); n != 1 {
		t.Errorf("%d ledger rows released after the delete, want the draft's one", n)
	}

	// A draft deleted with no release after it — as a customer's
	// anonymisation deletes its drafts — leaves its allocation to Invoices'
	// reconcile, which gives it back once an hour old.
	lost := o.fromOrders(t, false, pickLine(order.Id, shelf.Id, 3))
	o.h.Exec(t, `DELETE FROM invoices.invoices WHERE id = $1`, lost[0].InvoiceId)
	var reconcile *invoices.AllocationReconcileWorker
	for _, w := range o.h.Workers(o.h.Deps()) {
		if r, ok := w.(*invoices.AllocationReconcileWorker); ok {
			reconcile = r
		}
	}
	if reconcile == nil {
		t.Fatal("no invoices.allocation_reconcile worker is composed")
	}
	if n, err := reconcile.RunCycle(context.Background()); err != nil || n != 0 {
		t.Errorf("a reconcile within the hour = %d, %v; want 0", n, err)
	}
	o.h.Advance(2 * time.Hour)
	if n, err := reconcile.RunCycle(context.Background()); err != nil || n != 1 {
		t.Errorf("the reconcile = %d, %v; want the lost draft's allocation", n, err)
	}
	if l := o.order(t, order.Id).Lines[0]; l.QtyDraftAllocated != 0 {
		t.Errorf("the shelf line after the reconcile = %v draft-allocated, want 0", l.QtyDraftAllocated)
	}

	// The whole order in one invoice, delivered by hand first so that the
	// issue leaves it done.
	o.deliverAll(t, order.Id)
	full := o.fromOrders(t, false, whole(order)...)
	if len(full) != 1 {
		t.Fatalf("the full wizard made %+v, want one draft", full)
	}
	draft = o.doc(t, full[0].InvoiceId)
	held := allocations(draft)
	if len(draft.Lines) != 3 || len(held) != 3 {
		t.Fatalf("the full draft = %d lines holding %v, want three lines each holding an allocation", len(draft.Lines), held)
	}

	// An allocation that changed under the draft is found on the GET and
	// refuses the issue. Nothing in the product rewrites an allocation
	// (O36) — the revision is moved here to stand in for one that did.
	o.h.Exec(t, `UPDATE orders.invoice_allocations SET revision = 2 WHERE id = $1`, held[0])
	if d := o.doc(t, draft.Id); !slices.Contains(d.Warnings, "source_changed") {
		t.Errorf("the draft's warnings with a changed allocation = %v, want source_changed", d.Warnings)
	}
	invoiceRefusedAs(t, "issue with a changed allocation", o.c.Do(http.MethodPost, invoiceAt(draft.Id)+"/issue", map[string]any{}), "source_changed")
	if l := o.order(t, order.Id).Lines[0]; l.QtyInvoiced != 0 || l.QtyDraftAllocated != 10 {
		t.Errorf("the shelf line after the refused issue = %v invoiced, %v draft-allocated; want 0 and 10", l.QtyInvoiced, l.QtyDraftAllocated)
	}
	o.h.Exec(t, `UPDATE orders.invoice_allocations SET revision = 1 WHERE id = $1`, held[0])

	inv := o.issue(t, draft.Id)
	got := o.order(t, order.Id)
	for _, l := range got.Lines {
		if l.QtyInvoiced != *l.Quantity || l.QtyDraftAllocated != 0 {
			t.Errorf("line %d after the issue = %v invoiced, %v draft-allocated of %v; want all invoiced", l.Id, l.QtyInvoiced, l.QtyDraftAllocated, *l.Quantity)
		}
	}
	if got.InvoiceStatus != ordgen.OrdersOrderInvoiceStatusInvoiced || !got.Done {
		t.Errorf("the order after the issue = %s, done %v; want invoiced and done", got.InvoiceStatus, got.Done)
	}
	if len(got.Invoices) != 1 || got.Invoices[0].InvoiceId != inv.Id || got.Invoices[0].InvoiceNumber != *inv.Number ||
		got.Invoices[0].State != ordgen.OrdersOrderInvoiceStateInvoiced || got.Invoices[0].Amount != inv.NetTotal {
		t.Errorf("the order's invoices = %+v, want invoice %d of %v, invoiced", got.Invoices, *inv.Number, inv.NetTotal)
	}
	if n := invoicedEvents(t, o.h, order.Id); n != 1 {
		t.Errorf("%d orders.sales_order.invoiced events, want 1", n)
	}
	if n := o.h.Count(t, `SELECT count(*) FROM invoices.order_allocations_taken WHERE allocation_id = ANY($1)`, held); n != 0 {
		t.Errorf("%d ledger rows of the issued allocations, want none: Orders holds them now", n)
	}

	// An order invoiced in full by one invoice: the document's totals are
	// the order's.
	if inv.NetTotal != got.Totals.Net || inv.VatTotal != got.Totals.Vat || inv.GrossTotal != got.Totals.Gross {
		t.Errorf("the invoice's totals = %v / %v / %v, want the order's %v / %v / %v",
			inv.NetTotal, inv.VatTotal, inv.GrossTotal, got.Totals.Net, got.Totals.Vat, got.Totals.Gross)
	}

	// A full credit releases every allocation and gives the quantities back.
	var creditDraft invgen.InvoicesInvoiceResponse
	okJSON(t, o.c, http.MethodPost, invoiceAt(inv.Id)+"/credit", nil, &creditDraft)
	o.issue(t, creditDraft.Id)
	got = o.order(t, order.Id)
	for _, l := range got.Lines {
		if l.QtyInvoiced != 0 {
			t.Errorf("line %d after the credit = %v invoiced, want 0", l.Id, l.QtyInvoiced)
		}
	}
	if got.InvoiceStatus != ordgen.OrdersOrderInvoiceStatusNone || len(got.Invoices) != 1 ||
		got.Invoices[0].State != ordgen.OrdersOrderInvoiceStateReleased || got.Invoices[0].Amount != 0 ||
		got.Invoices[0].ReleasedAmount != inv.NetTotal {
		t.Errorf("the order after the credit = %s, invoices %+v; want nothing invoiced and the invoice released", got.InvoiceStatus, got.Invoices)
	}
	if n := o.h.Count(t, `SELECT count(*) FROM orders.invoice_allocations WHERE id = ANY($1) AND state = 'released'`, held); n != 3 {
		t.Errorf("%d of the invoice's allocations released by the credit, want 3", n)
	}
	if due := o.toInvoice(t, order.Id); due[shelf.Id] != 10 {
		t.Errorf("the shelf line's to invoice after the credit = %v, want all 10 again", due[shelf.Id])
	}
	if n := invoicedEvents(t, o.h, order.Id); n != 1 {
		t.Errorf("%d orders.sales_order.invoiced events after the credit, want still 1", n)
	}
}

// Two orders of one customer under one reference combined into one invoice
// (samlefaktura; another reference would separate them): one draft naming
// both, a section line per order, and an issue that stamps both orders
// invoiced, publishing once for each; the invoice's totals are the two
// orders' together.
func TestOrdersInvoicing_TwoOrdersCombined(t *testing.T) {
	o := newOrdersInstallation(t)
	first := o.confirmedOrder(t, "PO-1", orderLine(o.shelf, 2, 10), orderLine(o.coffee, 5, 0))
	second := o.confirmedOrder(t, "PO-1", orderLine(o.screw, 100, 0))

	drafts := o.fromOrders(t, true, append(whole(first), whole(second)...)...)
	if len(drafts) != 1 || !slices.Equal(drafts[0].OrderIds, []int64{first.Id, second.Id}) {
		t.Fatalf("the combined wizard made %+v, want one draft of both orders", drafts)
	}
	draft := o.doc(t, drafts[0].InvoiceId)
	var sections int
	for _, l := range draft.Lines {
		if l.Sources == nil || len(*l.Sources) == 0 {
			sections++
			if l.Quantity != 1 || l.UnitPrice != 0 {
				t.Errorf("section line %q = %v × %v, want 1 × 0", l.Description, l.Quantity, l.UnitPrice)
			}
		}
	}
	if sections != 2 || len(allocations(draft)) != 3 {
		t.Errorf("the combined draft has %d section lines and %d allocations, want 2 and 3", sections, len(allocations(draft)))
	}
	if draft.OrderReference != "" {
		t.Errorf("the combined draft's order reference = %q, want none for two orders", draft.OrderReference)
	}

	inv := o.issue(t, draft.Id)
	var net, vat, gross float64
	for _, id := range []int64{first.Id, second.Id} {
		got := o.order(t, id)
		if got.InvoiceStatus != ordgen.OrdersOrderInvoiceStatusInvoiced || len(got.Invoices) != 1 || got.Invoices[0].InvoiceId != inv.Id {
			t.Errorf("order %d after the issue = %s, invoices %+v; want invoiced by %d", id, got.InvoiceStatus, got.Invoices, inv.Id)
		}
		if n := invoicedEvents(t, o.h, id); n != 1 {
			t.Errorf("order %d: %d invoiced events, want 1", id, n)
		}
		net, vat, gross = net+got.Totals.Net, vat+got.Totals.Vat, gross+got.Totals.Gross
	}
	if !same(inv.NetTotal, net) || !same(inv.VatTotal, vat) || !same(inv.GrossTotal, gross) {
		t.Errorf("the combined invoice's totals = %v / %v / %v, want the orders' %v / %v / %v",
			inv.NetTotal, inv.VatTotal, inv.GrossTotal, net, vat, gross)
	}
}
