package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Delivery B's review (orders design O36, O39, as amended): Invoices owns
// the cleanup of the allocations it took — the ledger, the reconcile, a
// billing run item's retry — and keeps a line billing an allocation as the
// allocation is; an order in another currency is blocked, not refused whole;
// a combined draft states one delivery address.

// takenRow is an allocation's ledger row: whether it is there, and released.
func takenRow(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `
		SELECT coalesce((SELECT CASE WHEN released_at IS NULL THEN 'open' ELSE 'released' END
			FROM invoices.order_allocations_taken WHERE allocation_id = $1), 'absent')`, id)
}

// reconcileOnPoolOfOne runs one reconcile cycle on a pool of one connection
// — a cycle that held one while asking for another would wait for itself, and
// the deadline turns that into a failure — and answers how many it released.
func reconcileOnPoolOfOne(t *testing.T, h *harness) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	one, err := db.Open(ctx, h.Pool().Config().ConnString(), db.WithMaxConns(1))
	if err != nil {
		t.Fatalf("open a pool of one: %v", err)
	}
	defer one.Close()
	n, err := invoices.NewAllocationReconcileWorker(h.DepsOn(one)).RunCycle(ctx)
	if err != nil {
		t.Fatalf("reconcile on a pool of one: %v", err)
	}
	return n
}

// The ledger records every allocation the wizard takes; a deleted draft's
// whose release failed is given back by the reconcile once an hour old, on a
// pool of one; a long-lived draft's are never touched; the issue forgets
// what it stamped; with Orders off a cycle does nothing.
func TestFromOrders_TheReconcileReleasesWhatNoDraftHolds(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	kept := fromOrders(t, f.h, false, pick(order2210, 1, 5))
	gone := fromOrders(t, f.h, false, pick(order2211, 3, 2))
	keptID := orderDoc(t, f.h, kept[0].InvoiceID).Lines[0].Sources[0].ID
	goneID := orderDoc(t, f.h, gone[0].InvoiceID).Lines[0].Sources[0].ID
	if a, b := takenRow(t, f.h, keptID), takenRow(t, f.h, goneID); a != "open" || b != "open" {
		t.Fatalf("ledger = %s, %s; want both recorded", a, b)
	}

	f.orders.failReleases(errReleaseDown)
	if res := creator(t, f.h).Do(http.MethodDelete, invoicePath(gone[0].InvoiceID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", res.Status, res.Body)
	}
	f.orders.failReleases(nil)
	if got := takenRow(t, f.h, goneID); got != "open" {
		t.Errorf("after a failed release the ledger row is %s, want open", got)
	}
	if n := reconcileOnPoolOfOne(t, f.h); n != 0 {
		t.Errorf("a reconcile within the hour released %d, want 0", n)
	}

	f.h.Advance(2 * time.Hour)
	if n := reconcileOnPoolOfOne(t, f.h); n != 1 {
		t.Fatalf("the reconcile released %d, want the deleted draft's one", n)
	}
	if a := f.orders.allocation(goneID); a.State != contracts.AllocationReleased {
		t.Errorf("the deleted draft's allocation = %s, want released", a.State)
	}
	if a := f.orders.allocation(keptID); a.State != contracts.AllocationDraft {
		t.Errorf("the long-lived draft's allocation = %s, want draft still", a.State)
	}
	if got := takenRow(t, f.h, goneID); got != "released" {
		t.Errorf("the ledger row = %s, want released", got)
	}
	if n := reconcileOnPoolOfOne(t, f.h); n != 0 {
		t.Errorf("a second cycle released %d, want 0", n)
	}

	issued(t, f.h, kept[0].InvoiceID)
	if got := takenRow(t, f.h, keptID); got != "absent" {
		t.Errorf("after the issue the ledger row is %s, want it forgotten", got)
	}
	noLockedOrderCalls(t, f.orders)

	off := newHarness(t)
	if n, err := invoices.NewAllocationReconcileWorker(off.Deps()).RunCycle(context.Background()); n != 0 || err != nil {
		t.Errorf("the reconcile with Orders off = %d, %v; want 0, nil", n, err)
	}
}

// A billing run item that ended between Allocate and its drafts left the
// lines allocated to nothing; its retry gives them back first and invoices
// them, rather than skipping the customer no_orders_due.
func TestFromOrders_ARunItemRetriesAfterACrashBetweenAllocateAndItsDrafts(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	maker, _ := runMaker(t, f.h)
	run := startRun(t, maker, map[string]any{"issue": false, "source": "orders", "through": "2026-09-30"})
	item := modtest.One[int64](t, f.h.Harness, `SELECT id FROM invoices.billing_run_items WHERE run_id = $1`, run.ID)

	// The crashed attempt: Allocate answered and the ledger recorded it under
	// the item, and then nothing.
	crashed, err := f.orders.Allocate(context.Background(), contracts.AllocateRequest{Lines: []contracts.AllocateLine{
		{LineID: 1, Quantity: "5"}, {LineID: 2, Quantity: "3.5"}, {LineID: 3, Quantity: "2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, a := range crashed {
		ids = append(ids, a.ID)
	}
	f.h.Exec(t, `INSERT INTO invoices.order_allocations_taken (allocation_id, taken_at, run_item_id)
		SELECT unnest($1::bigint[]), now(), $2`, ids, item)

	if n := carryAll(t, f.h); n != 1 {
		t.Fatalf("items carried = %d, want 1", n)
	}
	got := onlyItem(t, f.h, run.ID)
	if got.Status != "drafted" || len(got.Invoices) != 2 {
		t.Fatalf("item = %+v, want drafted with one invoice per order", got)
	}
	if released := f.orders.releases(); !slices.Equal(released, ids) {
		t.Errorf("released = %v, want the crashed attempt's %v first", released, ids)
	}
	for _, id := range ids {
		if row := takenRow(t, f.h, id); row != "released" {
			t.Errorf("the crashed allocation %d's ledger row = %s, want released", id, row)
		}
	}
	if n := f.h.Count(t, `SELECT count(*) FROM invoices.order_allocations_taken WHERE run_item_id = $1 AND released_at IS NULL`, item); n != 3 {
		t.Errorf("open ledger rows of the item = %d, want the three its drafts hold", n)
	}
}

// Nothing due because drafts of the customer hold it: the item says so,
// orders_held_by_drafts, rather than no_orders_due.
func TestFromOrders_ARunItemSaysDraftsHoldTheOrders(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	maker, _ := runMaker(t, f.h)
	run := startRun(t, maker, map[string]any{"issue": false, "source": "orders", "through": "2026-09-30"})
	fromOrders(t, f.h, false, pick(order2210, 1, 5), pick(order2210, 2, 3.5), pick(order2211, 3, 2))

	carryAll(t, f.h)
	got := onlyItem(t, f.h, run.ID)
	if got.Status != "skipped" || got.Code == nil || *got.Code != "orders_held_by_drafts" {
		t.Errorf("item = %+v, want skipped orders_held_by_drafts", got)
	}
}

// An order in another currency than the settings': blocked in the due list
// with its reason; refused currency_not_nok when the wizard is asked for it
// by name; left out of a billing run and named in the item's detail, or the
// item skipped currency_not_supported when nothing else is due.
func TestFromOrders_AnotherCurrencyIsBlockedNotRefusedWhole(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	euro := acmeOrder(order2212, 2212, "PO-1", "2026-09-02")
	euro.Currency = "EUR"
	f.orders.put(euro, orderLine{id: 4, description: "Lampe", unit: "stk", quantity: "1", price: "50.00000000", discount: "0.00", vatCode: vat25})
	person := acmeOrder(5213, 2213, "", "2026-09-02")
	person.CustomerID, person.Currency = customerPerson, "SEK"
	f.orders.put(person, orderLine{id: 5, description: "Bok", unit: "stk", quantity: "1", price: "100.00000000", discount: "0.00", vatCode: vat25})

	res := creator(t, f.h).Do(http.MethodGet, fromOrdersDuePath+"?customerId=2001", nil)
	var v struct {
		Orders []struct {
			Number  int64   `json:"number"`
			Blocked *string `json:"blocked"`
		} `json:"orders"`
	}
	res.JSON(&v)
	blocked := map[int64]string{}
	for _, o := range v.Orders {
		if o.Blocked != nil {
			blocked[o.Number] = *o.Blocked
		}
	}
	if len(v.Orders) != 3 || len(blocked) != 1 || blocked[2212] != "currency_not_supported" {
		t.Errorf("due = %+v, want 2212 alone blocked currency_not_supported", v.Orders)
	}
	refusedFromOrders(t, f.h, "currency_not_nok", false, pick(order2210, 1, 1), pick(order2212, 4, 1))

	maker, _ := runMaker(t, f.h)
	run := startRun(t, maker, map[string]any{"issue": false, "source": "orders", "combine": true, "through": "2026-09-30"})
	if run.Customers != 2 {
		t.Fatalf("run = %+v, want Acme and the person", run)
	}
	carryAll(t, f.h)
	items := runDetail(t, f.h, run.ID, "").Items
	byCustomer := map[int32]billingRunItemJSON{}
	for _, it := range items {
		byCustomer[it.CustomerID] = it
	}
	acme := byCustomer[customerAcme]
	if acme.Status != "drafted" || len(acme.Invoices) != 1 || acme.Detail == nil || !strings.Contains(*acme.Detail, "2212 (EUR)") {
		t.Errorf("Acme's item = %+v, want drafted, 2212 named left out", acme)
	}
	if inv := getInvoice(t, f.h, acme.Invoices[0].InvoiceID); len(inv.Lines) != 5 {
		t.Errorf("Acme's invoice has %d lines, want 2210 and 2211 alone", len(inv.Lines))
	}
	if p := byCustomer[customerPerson]; p.Status != "skipped" || p.Code == nil || *p.Code != "currency_not_supported" {
		t.Errorf("the person's item = %+v, want skipped currency_not_supported", p)
	}
	if a := f.orders.live(); slices.ContainsFunc(a, func(id int64) bool { return f.orders.allocation(id).OrderID == order2212 }) {
		t.Errorf("the EUR order was allocated: %v", a)
	}
}

// A line billing an order allocation keeps its quantity, unit price,
// discount and VAT code: a save changing one is 409
// line_holds_order_allocation, naming the line; its description may change.
// Should one differ all the same, the issue refuses source_changed.
func TestFromOrders_ALineHoldingAnAllocationKeepsItsNumbers(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	drafts := fromOrders(t, f.h, false, pick(order2210, 1, 5), pick(order2210, 2, 3.5))
	d := orderDoc(t, f.h, drafts[0].InvoiceID)
	put := func(revision int32, edit func(first map[string]any)) *modtest.Response {
		lines := make([]map[string]any, 0, len(d.Lines))
		for _, l := range d.Lines {
			lines = append(lines, map[string]any{
				"description": l.Description, "quantity": l.Quantity, "unit": l.Unit, "unitPrice": l.UnitPrice,
				"discountPercent": l.DiscountPercent, "vatCodeId": l.VatCodeID,
				"sources": []refJSON{ref("orders.allocation", l.Sources[0].ID)},
			})
		}
		edit(lines[0])
		return creator(t, f.h).Do(http.MethodPut, invoicePath(d.ID), map[string]any{
			"customerId": customerAcme, "revision": revision, "deliveryDate": *d.DeliveryDate, "paymentTermsDays": 30,
			"yourReference": d.YourReference, "orderReference": d.OrderReference, "lines": lines,
		})
	}
	for name, edit := range map[string]func(map[string]any){
		"quantity":   func(l map[string]any) { l["quantity"] = 4 },
		"unit price": func(l map[string]any) { l["unitPrice"] = 1100 },
		"discount":   func(l map[string]any) { l["discountPercent"] = 0 },
		"VAT code":   func(l map[string]any) { l["vatCodeId"] = vat15 },
	} {
		res := put(d.Revision, edit)
		var p struct {
			Code         string `json:"code"`
			LinePosition *int32 `json:"linePosition"`
		}
		res.JSON(&p)
		if res.Status != http.StatusConflict || p.Code != "line_holds_order_allocation" || p.LinePosition == nil || *p.LinePosition != 1 {
			t.Errorf("PUT changing the %s = %d %s, want 409 line_holds_order_allocation on line 1", name, res.Status, res.Body)
		}
	}
	res := put(d.Revision, func(l map[string]any) { l["description"] = "Hylle, eik" })
	if res.Status != http.StatusOK {
		t.Fatalf("PUT changing the description = %d %s, want 200", res.Status, res.Body)
	}

	f.h.Exec(t, `UPDATE invoices.lines SET quantity = 4 WHERE invoice_id = $1 AND position = 1`, d.ID)
	issue := issueWith(t, f.h, d.ID, "")
	var p struct {
		Code     string `json:"code"`
		SourceID *int64 `json:"sourceId"`
	}
	issue.JSON(&p)
	if issue.Status != http.StatusConflict || p.Code != "source_changed" || p.SourceID == nil || *p.SourceID != d.Lines[0].Sources[0].ID {
		t.Errorf("issue of a drifted line = %d %s, want 409 source_changed naming its allocation", issue.Status, issue.Body)
	}
	if calls := f.holders.callsOf(contracts.WorkSourceOrderAllocation); len(calls) != 0 {
		t.Errorf("holder calls = %+v, want none", calls)
	}
}

// Combined drafts state one delivery address: orders delivered to another
// place are kept apart (delivery_address); the same address written with
// other spaces and case is the same place.
func TestFromOrders_CombinedDraftsSplitByDeliveryAddress(t *testing.T) {
	t.Parallel()
	f := newOrdersFixture(t)
	putTwoOrders(f.orders)
	same := acmeOrder(order2212, 2212, "PO-1", "2026-09-03")
	same.DeliveryAddress = &contracts.CustomerAddressEntry{Line1: "  havnegata  1 ", PostalCode: "9008", City: "TROMSØ", Country: "no"}
	f.orders.put(same, orderLine{id: 4, description: "Lampe", unit: "stk", quantity: "1", price: "500.00000000", discount: "0.00", vatCode: vat25})
	elsewhere := acmeOrder(5213, 2213, "PO-1", "2026-09-04")
	elsewhere.DeliveryAddress = &contracts.CustomerAddressEntry{Line1: "Storgata 9", PostalCode: "0155", City: "Oslo", Country: "NO"}
	f.orders.put(elsewhere, orderLine{id: 5, description: "Bord", unit: "stk", quantity: "1", price: "900.00000000", discount: "0.00", vatCode: vat25})

	drafts := fromOrders(t, f.h, true, pick(order2210, 1, 5), pick(order2211, 3, 2), pick(order2212, 4, 1), pick(5213, 5, 1))
	if len(drafts) != 2 || !slices.Equal(drafts[0].OrderNumbers, []int64{2210, 2211, 2212}) || !slices.Equal(drafts[1].OrderNumbers, []int64{2213}) {
		t.Fatalf("drafts = %+v, want 2210–2212 together and 2213 apart", drafts)
	}
	for _, d := range drafts {
		if !slices.Equal(d.SeparatedBy, []string{"delivery_address"}) {
			t.Errorf("draft %v separatedBy = %v, want delivery_address", d.OrderNumbers, d.SeparatedBy)
		}
	}
	if d := orderDoc(t, f.h, drafts[1].InvoiceID); d.DeliveryAddress == nil || d.DeliveryAddress.Line1 != "Storgata 9" {
		t.Errorf("2213's draft delivers to %+v, want Storgata 9", d.DeliveryAddress)
	}
}
