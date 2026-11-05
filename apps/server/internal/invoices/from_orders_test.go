package invoices_test

import (
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The From orders wizard (orders design O36–O39), through the API: the
// orders as Orders answers them (fakeOrders), the customers as the directory
// does, every kind's holder faked. The harness's clock stands on 12 September
// 2026.

const (
	fromOrdersPath    = "/api/v1/invoices/from-orders"
	fromOrdersDuePath = "/api/v1/invoices/from-orders/due"

	order2210 = 5210
	order2211 = 5211
	order2212 = 5212
)

// ordersFixture is an installation with Orders' reads faked, every holder
// faked and a complete, VAT-registered seller.
type ordersFixture struct {
	h       *harness
	orders  *fakeOrders
	holders *fakeHolders
}

func newOrdersFixture(t *testing.T, opts ...modtest.Option) ordersFixture {
	t.Helper()
	f := ordersFixture{orders: newFakeOrders(), holders: newFakeHolders()}
	all := append(f.orders.options(), f.holders.options()...)
	f.h = newHarness(t, append(all, opts...)...)
	saveSeller(t, f.h, completeSeller(1))
	return f
}

// acmeOrder is Acme's order number at id, in NOK, with ref as the buyer's
// reference, delivered on day, on 30 days' terms.
func acmeOrder(id, number int64, ref, day string) contracts.DueOrder {
	terms := int32(30)
	o := contracts.DueOrder{
		ID: id, Number: number, CustomerID: customerAcme, Currency: "NOK", YourReference: ref, PaymentTermsDays: &terms,
		DeliveryAddress: &contracts.CustomerAddressEntry{Label: "Lager", Line1: "Havnegata 1", PostalCode: "9008", City: "Tromsø", Country: "NO"},
	}
	if day != "" {
		o.DeliveryDate = dayOf(day)
	}
	return o
}

type orderSourceJSON struct {
	Kind       string  `json:"kind"`
	ID         int64   `json:"id"`
	ProjectID  *int32  `json:"projectId"`
	Subkind    *string `json:"subkind"`
	PeriodFrom *string `json:"periodFrom"`
	Quantity   float64 `json:"quantity"`
	Amount     float64 `json:"amount"`
	State      string  `json:"state"`
}

type orderDocJSON struct {
	ID               int64   `json:"id"`
	Revision         int32   `json:"revision"`
	CustomerID       int32   `json:"customerId"`
	YourReference    string  `json:"yourReference"`
	OrderReference   string  `json:"orderReference"`
	DeliveryDate     *string `json:"deliveryDate"`
	DeliveryFrom     *string `json:"deliveryFrom"`
	DeliveryTo       *string `json:"deliveryTo"`
	PaymentTermsDays *int32  `json:"paymentTermsDays"`
	DeliveryAddress  *struct {
		Line1   string `json:"line1"`
		City    string `json:"city"`
		Country string `json:"country"`
	} `json:"deliveryAddress"`
	ProjectID  *int32   `json:"projectId"`
	NetTotal   float64  `json:"netTotal"`
	VatTotal   float64  `json:"vatTotal"`
	GrossTotal float64  `json:"grossTotal"`
	Warnings   []string `json:"warnings"`
	Lines      []struct {
		Position        int32             `json:"position"`
		Description     string            `json:"description"`
		Unit            string            `json:"unit"`
		Quantity        float64           `json:"quantity"`
		UnitPrice       float64           `json:"unitPrice"`
		DiscountPercent float64           `json:"discountPercent"`
		VatCodeID       int32             `json:"vatCodeId"`
		LineNet         float64           `json:"lineNet"`
		Sources         []orderSourceJSON `json:"sources"`
		Warnings        []string          `json:"warnings"`
	} `json:"lines"`
	ReleasedSources []refJSON `json:"releasedSources"`
}

type fromOrdersDraftJSON struct {
	InvoiceID    int64    `json:"invoiceId"`
	CustomerID   int32    `json:"customerId"`
	OrderIDs     []int64  `json:"orderIds"`
	OrderNumbers []int64  `json:"orderNumbers"`
	SeparatedBy  []string `json:"separatedBy"`
}

func pick(orderID, lineID int64, quantity float64) map[string]any {
	return map[string]any{"orderId": orderID, "lineId": lineID, "quantity": quantity}
}

func postFromOrders(t *testing.T, h *harness, combine bool, lines ...map[string]any) *modtest.Response {
	t.Helper()
	return creator(t, h).Do(http.MethodPost, fromOrdersPath, map[string]any{"combine": combine, "lines": lines})
}

// fromOrders posts the lines and answers the drafts made, failing unless 201.
func fromOrders(t *testing.T, h *harness, combine bool, lines ...map[string]any) []fromOrdersDraftJSON {
	t.Helper()
	res := postFromOrders(t, h, combine, lines...)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /invoices/from-orders = %d %s, want 201", res.Status, res.Body)
	}
	var out struct {
		Invoices []fromOrdersDraftJSON `json:"invoices"`
	}
	res.JSON(&out)
	return out.Invoices
}

// refusedFromOrders asserts the wizard answers 409 code and answers the
// problem's order line.
func refusedFromOrders(t *testing.T, h *harness, code string, combine bool, lines ...map[string]any) *int64 {
	t.Helper()
	res := postFromOrders(t, h, combine, lines...)
	if res.Status != http.StatusConflict {
		t.Fatalf("POST /invoices/from-orders = %d %s, want 409 %s", res.Status, res.Body, code)
	}
	var p struct {
		Code        string `json:"code"`
		OrderLineID *int64 `json:"orderLineId"`
	}
	res.JSON(&p)
	if p.Code != code {
		t.Fatalf("POST /invoices/from-orders = %s, want %s (%s)", p.Code, code, res.Body)
	}
	return p.OrderLineID
}

func orderDoc(t *testing.T, h *harness, id int64) orderDocJSON {
	t.Helper()
	res := creator(t, h).Do(http.MethodGet, invoicePath(id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/%d = %d %s", id, res.Status, res.Body)
	}
	var d orderDocJSON
	res.JSON(&d)
	return d
}

// noLockedOrderCalls fails when any of Orders' calls was made under a lock.
func noLockedOrderCalls(t *testing.T, f *fakeOrders) {
	t.Helper()
	for _, c := range f.reads() {
		if c.locked {
			t.Errorf("BillableOrders.%s was called under a lock", c.method)
		}
	}
}

// putTwoOrders stores Acme's orders 2210 (two lines) and 2211 (one line),
// both with the buyer's reference PO-1.
func putTwoOrders(f *fakeOrders) {
	f.put(acmeOrder(order2210, 2210, "PO-1", "2026-09-01"),
		orderLine{id: 1, description: "Hylle", unit: "stk", quantity: "5", price: "1200.00000000", discount: "10.00", vatCode: vat25},
		orderLine{id: 2, description: "Montering", unit: "timer", quantity: "3.5", price: "850.12345678", discount: "0.00", vatCode: vat25})
	f.put(acmeOrder(order2211, 2211, "PO-1", "2026-09-05"),
		orderLine{id: 3, description: "Bok", unit: "stk", quantity: "2", price: "399.00000000", discount: "0.00", vatCode: vat15})
}

// The due list: each order with its lines left to invoice and their nets,
// the customer named, a held order listed with its reason; the query names
// exactly one of customers, orders and every order.
func TestFromOrders_TheDueList(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	f.orders.block(order2211, contracts.DueOrderBlockedOnHold)

	res := creator(t, f.h).Do(http.MethodGet, fmt.Sprintf("%s?customerId=%d", fromOrdersDuePath, customerAcme), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET due = %d %s", res.Status, res.Body)
	}
	var v struct {
		Orders []struct {
			ID           int64   `json:"id"`
			Number       int64   `json:"number"`
			CustomerName *string `json:"customerName"`
			Blocked      *string `json:"blocked"`
			DeliveryDate *string `json:"deliveryDate"`
			Lines        []struct {
				LineID    int64   `json:"lineId"`
				ToInvoice float64 `json:"toInvoice"`
				UnitPrice float64 `json:"unitPrice"`
				Amount    float64 `json:"amount"`
			} `json:"lines"`
		} `json:"orders"`
		Warnings []string `json:"warnings"`
	}
	res.JSON(&v)
	if len(v.Orders) != 2 || v.Orders[0].Number != 2210 || v.Orders[0].CustomerName == nil || *v.Orders[0].CustomerName != "Acme AS" {
		t.Fatalf("orders = %+v, want 2210 and 2211 of Acme AS", v.Orders)
	}
	if l := v.Orders[0].Lines; len(l) != 2 || l[0].ToInvoice != 5 || l[0].Amount != 5400 || l[1].UnitPrice != 850.12345678 || l[1].Amount != 2975.43 {
		t.Errorf("2210's lines = %+v, want 5 × 1 200 less 10 %% = 5 400 and 3.5 × 850.12345678 = 2 975.43", l)
	}
	if v.Orders[0].Blocked != nil || v.Orders[1].Blocked == nil || *v.Orders[1].Blocked != "on_hold" {
		t.Errorf("blocked = %v, %v; want only 2211 on hold", v.Orders[0].Blocked, v.Orders[1].Blocked)
	}
	for _, q := range []string{"", "every=true&customerId=2001"} {
		if res := creator(t, f.h).Do(http.MethodGet, fromOrdersDuePath+"?"+q, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET due?%s = %d, want 400", q, res.Status)
		}
	}
	if res := f.h.SignIn(t, "invoices:access").Do(http.MethodGet, fromOrdersDuePath+"?every=true", nil); res.Status != http.StatusForbidden {
		t.Errorf("GET due without invoices:create = %d, want 403", res.Status)
	}
}

// One invoice per order: each draft carries its order's lines — description,
// quantity, unit, unit price at eight decimals, discount and VAT code — each
// holding its allocation; its header the order's reference, number, delivery
// date, address and terms; no section line.
func TestFromOrders_OneInvoicePerOrder(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)

	drafts := fromOrders(t, f.h, false, pick(order2210, 1, 5), pick(order2210, 2, 3.5), pick(order2211, 3, 2))
	if len(drafts) != 2 || !slices.Equal(drafts[0].OrderNumbers, []int64{2210}) || !slices.Equal(drafts[1].OrderNumbers, []int64{2211}) {
		t.Fatalf("drafts = %+v, want one for 2210 and one for 2211", drafts)
	}
	d := orderDoc(t, f.h, drafts[0].InvoiceID)
	if d.OrderReference != "2210" || d.YourReference != "PO-1" || d.DeliveryDate == nil || *d.DeliveryDate != "2026-09-01" ||
		d.PaymentTermsDays == nil || *d.PaymentTermsDays != 30 {
		t.Errorf("header = %+v, want 2210, PO-1, 2026-09-01 and 30 days", d)
	}
	if d.DeliveryAddress == nil || d.DeliveryAddress.Line1 != "Havnegata 1" || d.DeliveryAddress.Country != "NO" {
		t.Errorf("delivery address = %+v, want the order's", d.DeliveryAddress)
	}
	if len(d.Lines) != 2 {
		t.Fatalf("lines = %+v, want two", d.Lines)
	}
	first, second := d.Lines[0], d.Lines[1]
	if first.Description != "Hylle" || first.Unit != "stk" || first.Quantity != 5 || first.UnitPrice != 1200 || first.DiscountPercent != 10 ||
		first.VatCodeID != vat25 || first.LineNet != 5400 {
		t.Errorf("line 1 = %+v, want 5 stk Hylle at 1 200 less 10 %%", first)
	}
	if second.UnitPrice != 850.12345678 || second.LineNet != 2975.43 {
		t.Errorf("line 2 = %+v, want the price at eight decimals and its net 2 975.43", second)
	}
	for _, l := range d.Lines {
		if len(l.Sources) != 1 || l.Sources[0].Kind != "orders.allocation" || l.Sources[0].State != "held" ||
			l.Sources[0].Subkind == nil || *l.Sources[0].Subkind != "order_line" || len(l.Warnings) != 0 {
			t.Errorf("line %d's sources = %+v %v, want one held order_line allocation, no warning", l.Position, l.Sources, l.Warnings)
		}
	}
	if len(f.orders.live()) != 3 {
		t.Errorf("live allocations = %v, want three", f.orders.live())
	}
	noLockedOrderCalls(t, f.orders)
}

// Combined per customer (samlefaktura): the orders that agree make one draft,
// each order's lines opened by its section text; no order reference; the
// latest delivery date. An order with another reference makes its own draft,
// and both say what kept them apart.
func TestFromOrders_CombinedPerCustomer(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	f.orders.put(acmeOrder(order2212, 2212, "PO-2", "2026-09-03"),
		orderLine{id: 4, description: "Lampe", unit: "stk", quantity: "1", price: "500.00000000", discount: "0.00", vatCode: vat25})

	drafts := fromOrders(t, f.h, true, pick(order2210, 1, 5), pick(order2211, 3, 2), pick(order2212, 4, 1))
	if len(drafts) != 2 || !slices.Equal(drafts[0].OrderNumbers, []int64{2210, 2211}) || !slices.Equal(drafts[1].OrderNumbers, []int64{2212}) {
		t.Fatalf("drafts = %+v, want 2210 with 2211, and 2212 apart", drafts)
	}
	if !slices.Equal(drafts[0].SeparatedBy, []string{"your_reference"}) || !slices.Equal(drafts[1].SeparatedBy, []string{"your_reference"}) {
		t.Errorf("separatedBy = %v, %v; want your_reference", drafts[0].SeparatedBy, drafts[1].SeparatedBy)
	}
	d := orderDoc(t, f.h, drafts[0].InvoiceID)
	if d.OrderReference != "" || d.YourReference != "PO-1" || d.DeliveryDate == nil || *d.DeliveryDate != "2026-09-05" {
		t.Errorf("header = %q %q %v, want no order reference, PO-1 and the latest delivery 2026-09-05", d.OrderReference, d.YourReference, d.DeliveryDate)
	}
	var texts []string
	for _, l := range d.Lines {
		texts = append(texts, fmt.Sprintf("%s %v", l.Description, l.LineNet))
	}
	if want := []string{"Ordre 2210, Deres ref. PO-1 0", "Hylle 5400", "Ordre 2211, Deres ref. PO-1 0", "Bok 798"}; !slices.Equal(texts, want) {
		t.Errorf("lines = %v, want %v", texts, want)
	}
	if len(d.Lines[0].Sources) != 0 || d.Lines[0].VatCodeID != vat25 || d.Lines[2].VatCodeID != vat15 {
		t.Errorf("section lines = %+v, %+v; want no source, at their order's first line's code", d.Lines[0], d.Lines[2])
	}
	if lone := orderDoc(t, f.h, drafts[1].InvoiceID); lone.OrderReference != "2212" || len(lone.Lines) != 1 {
		t.Errorf("2212's draft = %+v, want its number and its one line", lone)
	}
}

// Delfakturering: part of a line is invoiced, and the rest stays due — a
// second draft takes it, and no more.
func TestFromOrders_PartOfALine(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)

	first := fromOrders(t, f.h, false, pick(order2210, 1, 2))
	if d := orderDoc(t, f.h, first[0].InvoiceID); len(d.Lines) != 1 || d.Lines[0].Quantity != 2 || d.Lines[0].LineNet != 2160 {
		t.Errorf("draft = %+v, want 2 of 5 at 2 160", d.Lines)
	}
	if line := refusedFromOrders(t, f.h, "allocation_exceeds_open", false, pick(order2210, 1, 4)); line == nil || *line != 1 {
		t.Errorf("orderLineId = %v, want line 1", line)
	}
	fromOrders(t, f.h, false, pick(order2210, 1, 3))
	if line := refusedFromOrders(t, f.h, "line_not_invoiceable", false, pick(order2210, 1, 1)); line == nil || *line != 1 {
		t.Errorf("orderLineId = %v, want line 1", line)
	}
	if len(f.orders.live()) != 2 {
		t.Errorf("live allocations = %v, want the two drafts' only", f.orders.live())
	}
}

// The refusals: Orders off, an order on hold, a body out of shape, a
// customer blocked — and none of them leaves anything allocated.
func TestFromOrders_Refusals(t *testing.T) {
	t.Parallel()
	off := newHarness(t)
	if res := postFromOrders(t, off, false, pick(order2210, 1, 1)); res.Status != http.StatusConflict {
		t.Errorf("Orders off = %d %s, want 409 orders_unavailable", res.Status, res.Body)
	}

	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	f.orders.block(order2211, contracts.DueOrderBlockedDraft)
	if line := refusedFromOrders(t, f.h, "order_blocked", false, pick(order2210, 1, 1), pick(order2211, 3, 1)); line == nil || *line != 3 {
		t.Errorf("orderLineId = %v, want line 3", line)
	}
	for _, body := range [][]map[string]any{{}, {pick(order2210, 1, 0)}, {pick(order2210, 1, 1.0001)}, {pick(order2210, 1, 1), pick(order2210, 1, 1)}} {
		if res := postFromOrders(t, f.h, false, body...); res.Status != http.StatusBadRequest {
			t.Errorf("POST %v = %d %s, want 400", body, res.Status, res.Body)
		}
	}
	f.h.customers.setStatus(customerAcme, "disabled")
	if res := postFromOrders(t, f.h, false, pick(order2210, 1, 1)); res.Status != http.StatusConflict {
		t.Errorf("a blocked customer = %d %s, want 409", res.Status, res.Body)
	}
	if live := f.orders.live(); len(live) != 0 {
		t.Errorf("live allocations = %v, want none after refusals", live)
	}
}

// A failure after Allocate gives the allocations back: here the draft's hold
// is refused because another document holds the allocation id Orders hands
// out next.
func TestFromOrders_AFailureAfterAllocateReleases(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	other := createDraft(t, f.h, draftBody(customerAcme, line("Annet", 1, 100, vat25)))
	f.h.Exec(t, `
		INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
			quantity, amount, currency, source_date)
		SELECT l.id, l.invoice_id, 'orders.allocation', $2, 1, 'order_line', 1, 100, 'NOK', DATE '2026-09-01'
		FROM invoices.lines l WHERE l.invoice_id = $1`, other.ID, f.orders.nextAllocation())

	if res := postFromOrders(t, f.h, false, pick(order2210, 1, 1)); res.Status != http.StatusConflict {
		t.Fatalf("POST = %d %s, want 409 source_held_elsewhere", res.Status, res.Body)
	}
	if live := f.orders.live(); len(live) != 0 {
		t.Errorf("live allocations = %v, want the one made released again", live)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE id <> $1`, other.ID); n != 0 {
		t.Errorf("drafts = %d, want none written", n)
	}
}

// Deleting a draft gives its allocations back once the delete has
// committed; a release that fails is logged and the delete stands.
func TestFromOrders_DeletingTheDraftReleases(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	drafts := fromOrders(t, f.h, false, pick(order2210, 1, 5), pick(order2210, 2, 1))
	held := f.orders.live()

	if res := creator(t, f.h).Do(http.MethodDelete, invoicePath(drafts[0].InvoiceID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	if got := f.orders.releases(); !slices.Equal(got, held) {
		t.Errorf("released = %v, want %v", got, held)
	}
	if live := f.orders.live(); len(live) != 0 {
		t.Errorf("live allocations = %v, want none", live)
	}

	again := fromOrders(t, f.h, false, pick(order2210, 1, 5))
	f.orders.failReleases(errReleaseDown)
	if res := creator(t, f.h).Do(http.MethodDelete, invoicePath(again[0].InvoiceID), nil); res.Status != http.StatusNoContent {
		t.Errorf("DELETE with Orders failing = %d %s, want 204 regardless", res.Status, res.Body)
	}
	noLockedOrderCalls(t, f.orders)
}

// A save that drops an order line's line gives its allocation back, and
// names it released; the line kept keeps its allocation.
func TestFromOrders_RemovingALineReleases(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	drafts := fromOrders(t, f.h, false, pick(order2210, 1, 5), pick(order2210, 2, 3.5))
	d := orderDoc(t, f.h, drafts[0].InvoiceID)
	kept, dropped := d.Lines[0].Sources[0].ID, d.Lines[1].Sources[0].ID

	body := map[string]any{
		"customerId": customerAcme, "revision": d.Revision, "deliveryDate": *d.DeliveryDate, "paymentTermsDays": 30,
		"yourReference": d.YourReference, "orderReference": d.OrderReference,
		"lines": []map[string]any{{
			"description": "Hylle", "quantity": 5, "unit": "stk", "unitPrice": 1200, "discountPercent": 10, "vatCodeId": vat25,
			"sources": []refJSON{ref("orders.allocation", kept)},
		}},
	}
	res := creator(t, f.h).Do(http.MethodPut, invoicePath(d.ID), body)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	var saved orderDocJSON
	res.JSON(&saved)
	if !slices.Equal(saved.ReleasedSources, []refJSON{ref("orders.allocation", dropped)}) {
		t.Errorf("releasedSources = %v, want allocation %d", saved.ReleasedSources, dropped)
	}
	if got := f.orders.releases(); !slices.Equal(got, []int64{dropped}) {
		t.Errorf("released = %v, want %d", got, dropped)
	}
	if a := f.orders.allocation(kept); a.State != contracts.AllocationDraft {
		t.Errorf("kept allocation = %s, want draft", a.State)
	}
}

// The issue stamps the allocations through Orders' holder, last, and needs
// no project directory for an order's project (Q5): an allocation's project
// is carried, never judged by the issue.
func TestFromOrders_TheIssueStampsThroughTheHolder(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	project := int32(project41)
	o := acmeOrder(order2210, 2210, "PO-1", "2026-09-01")
	o.ProjectID = &project
	f.orders.put(o, orderLine{id: 1, description: "Hylle", unit: "stk", quantity: "2", price: "1000.00000000", discount: "0.00", vatCode: vat25})
	drafts := fromOrders(t, f.h, false, pick(order2210, 1, 2))
	d := orderDoc(t, f.h, drafts[0].InvoiceID)
	if src := d.Lines[0].Sources[0]; src.ProjectID == nil || *src.ProjectID != project41 {
		t.Errorf("source = %+v, want the order's project", src)
	}

	inv := issued(t, f.h, d.ID)
	calls := f.holders.callsOf(contracts.WorkSourceOrderAllocation)
	if len(calls) != 1 || calls[0].op != "mark" || !calls[0].locked || len(calls[0].sources) != 1 ||
		calls[0].sources[0].ID != d.Lines[0].Sources[0].ID || calls[0].sources[0].Amount != "2000.00000000" ||
		calls[0].sources[0].ProjectID != project41 {
		t.Fatalf("holder calls = %+v, want one mark of the allocation at 2 000 under the issue's lock", calls)
	}
	if calls[0].ref.Number == 0 || inv.Number == nil || calls[0].ref.Number != *inv.Number {
		t.Errorf("stamp = %+v, want the invoice's number %v", calls[0].ref, inv.Number)
	}
}

// Document totals: an order invoiced in full on one invoice nets, taxes and
// totals what the order does — docmath over the order's own lines.
func TestFromOrders_ADocumentInFullEqualsTheOrder(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	lines := []orderLine{
		{id: 1, description: "A", unit: "stk", quantity: "3", price: "333.33333333", discount: "7.50", vatCode: vat25},
		{id: 2, description: "B", unit: "stk", quantity: "1.125", price: "99.99000000", discount: "0.00", vatCode: vat15},
		{id: 3, description: "C", unit: "timer", quantity: "7.5", price: "1249.50000000", discount: "12.25", vatCode: vat25},
		{id: 4, description: "D", unit: "stk", quantity: "11", price: "0.45000000", discount: "33.33", vatCode: vatZero},
	}
	f.orders.put(acmeOrder(order2210, 2210, "PO-1", "2026-09-01"), lines...)
	var picks []map[string]any
	rates := map[int32]*big.Rat{vat25: big.NewRat(25, 1), vat15: big.NewRat(15, 1), vatZero: new(big.Rat)}
	categories := map[int32]string{vat25: "S", vat15: "S", vatZero: "Z"}
	var taxed []docmath.TaxedLine
	for _, l := range lines {
		q, _ := new(big.Rat).SetString(l.quantity)
		price, _ := new(big.Rat).SetString(l.price)
		discount, _ := new(big.Rat).SetString(l.discount)
		qf, _ := q.Float64()
		picks = append(picks, pick(order2210, l.id, qf))
		taxed = append(taxed, docmath.TaxedLine{Net: docmath.Line(q, price, discount).Net, Category: categories[l.vatCode], Rate: rates[l.vatCode]})
	}
	_, order, _ := docmath.Summarize(taxed, big.NewRat(1, 1))

	drafts := fromOrders(t, f.h, false, picks...)
	inv := issued(t, f.h, drafts[0].InvoiceID)
	for _, c := range []struct {
		name string
		got  float64
		want *big.Rat
	}{{"net", inv.NetTotal, order.Net}, {"VAT", inv.VatTotal, order.VAT}, {"gross", inv.GrossTotal, order.Gross}} {
		if want, _ := c.want.Float64(); c.got != want {
			t.Errorf("%s = %v, want the order's %v", c.name, c.got, want)
		}
	}
}

// A billing run of orders (orders design O40): every customer with order
// lines due through the date, combined as the run says, drafted through the
// wizard's own core and — when the run issues — issued; a run of orders asks
// for Orders, and combine is for orders only.
func TestFromOrders_TheBillingRunTakesOrders(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	later := acmeOrder(order2212, 2212, "PO-1", "2026-10-15")
	f.orders.put(later, orderLine{id: 4, description: "Lampe", unit: "stk", quantity: "1", price: "500.00000000", discount: "0.00", vatCode: vat25})
	maker, _ := runMaker(t, f.h)

	if res := maker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false, "combine": true}); res.Status != http.StatusBadRequest {
		t.Errorf("combine on consumption = %d %s, want 400", res.Status, res.Body)
	}
	run := startRun(t, maker, map[string]any{"issue": true, "source": "orders", "combine": true, "through": "2026-09-30"})
	if run.Customers != 1 {
		t.Fatalf("run = %+v, want Acme alone", run)
	}
	if n := carryAll(t, f.h); n != 1 {
		t.Fatalf("items carried = %d, want 1", n)
	}
	item := onlyItem(t, f.h, run.ID)
	if item.Status != "issued" || len(item.Invoices) != 1 || item.Invoices[0].Number == nil {
		t.Fatalf("item = %+v, want one issued invoice", item)
	}
	inv := getInvoice(t, f.h, item.Invoices[0].InvoiceID)
	if len(inv.Lines) != 5 || inv.OrderReference != "" {
		t.Errorf("invoice = %d lines, order reference %q; want 2210 and 2211 combined — two sections and three lines — and no order 2212", len(inv.Lines), inv.OrderReference)
	}
	if calls := f.holders.callsOf(contracts.WorkSourceOrderAllocation); len(calls) != 1 || len(calls[0].sources) != 3 {
		t.Errorf("holder calls = %+v, want one mark of three allocations", calls)
	}

	off := newHarness(t)
	offMaker, _ := runMaker(t, off)
	if res := offMaker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false, "source": "orders"}); res.Status != http.StatusConflict {
		t.Errorf("a run of orders with Orders off = %d %s, want 409 orders_unavailable", res.Status, res.Body)
	}
}

// A subscription period's allocation: its line carries its period, its
// source the period's first day, and the draft's delivery spans the periods.
func TestFromOrders_ASubscriptionPeriodSpansTheDelivery(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	f.orders.put(acmeOrder(order2210, 2210, "PO-1", ""),
		orderLine{id: 1, description: "Abonnement", unit: "mnd", quantity: "1", price: "299.00000000", discount: "0.00", vatCode: vat25})
	f.orders.periodOf(1, "2026-08-01", "2026-08-31")

	drafts := fromOrders(t, f.h, false, pick(order2210, 1, 1))
	d := orderDoc(t, f.h, drafts[0].InvoiceID)
	if d.DeliveryDate != nil || d.DeliveryFrom == nil || *d.DeliveryFrom != "2026-08-01" || d.DeliveryTo == nil || *d.DeliveryTo != "2026-08-31" {
		t.Errorf("delivery = %v %v %v, want 1–31 August as a period", d.DeliveryDate, d.DeliveryFrom, d.DeliveryTo)
	}
	src := d.Lines[0].Sources[0]
	if d.Lines[0].Description != "Abonnement, august 2026" || src.Subkind == nil || *src.Subkind != "subscription_period" ||
		src.PeriodFrom == nil || *src.PeriodFrom != "2026-08-01" {
		t.Errorf("line = %q, source = %+v; want the period on both", d.Lines[0].Description, src)
	}
}

// A held allocation is judged fresh on GET through Allocations: one Orders
// no longer holds as a draft — invoiced elsewhere, released — warns
// source_not_invoiceable on its line.
func TestFromOrders_AHeldAllocationIsJudgedOnGet(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	drafts := fromOrders(t, f.h, false, pick(order2210, 1, 5), pick(order2210, 2, 1))
	d := orderDoc(t, f.h, drafts[0].InvoiceID)
	if slices.Contains(d.Warnings, "source_not_invoiceable") {
		t.Fatalf("warnings = %v, want the fresh draft clean", d.Warnings)
	}
	f.orders.invoice(d.Lines[1].Sources[0].ID)
	d = orderDoc(t, f.h, drafts[0].InvoiceID)
	if !slices.Contains(d.Warnings, "source_not_invoiceable") || !slices.Contains(d.Lines[1].Warnings, "source_not_invoiceable") ||
		slices.Contains(d.Lines[0].Warnings, "source_not_invoiceable") {
		t.Errorf("warnings = %v, lines %v %v; want line 2's allocation no longer invoiceable", d.Warnings, d.Lines[0].Warnings, d.Lines[1].Warnings)
	}
}
