package orders_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// The tests of invoicing from orders on Orders' side (orders design O26,
// O36–O38, O41, §12 B): the allocations, the holder, the release's lock
// order, and the rules a draft allocation sets on the order.

// allocate allocates qty of a line and answers the one allocation, failing
// the test on any error.
func allocate(t *testing.T, p contracts.BillableOrders, lineID int64, qty string) contracts.OrderAllocation {
	t.Helper()
	got, err := p.Allocate(context.Background(), contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: lineID, Quantity: qty}}})
	if err != nil || len(got) != 1 {
		t.Fatalf("allocate %s of line %d: %v, %v", qty, lineID, got, err)
	}
	return got[0]
}

// expectAllocationRefusal fails the test unless err is an AllocationRefusal
// with code.
func expectAllocationRefusal(t *testing.T, err error, code, what string) {
	t.Helper()
	var r *contracts.AllocationRefusal
	if !errors.As(err, &r) || r.Code != code {
		t.Fatalf("%s: %v; want an allocation refusal %s", what, err, code)
	}
}

// sourceOf is an allocation as an invoice line's source.
func sourceOf(a contracts.OrderAllocation) contracts.WorkSource {
	return contracts.WorkSource{Kind: contracts.WorkSourceOrderAllocation, ID: a.ID, Revision: a.Revision, Currency: a.Currency, Amount: a.Amount}
}

// invoiceRef is an invoice's stamp at the harness's now.
func invoiceRef(h *harness, id, number int64) contracts.InvoiceRef {
	return contracts.InvoiceRef{ID: id, Number: number, IssueDate: h.Now().Truncate(24 * time.Hour), IssuedAt: h.Now(),
		IssuedBy: uuid.New(), IssuedByDisplay: "Kari"}
}

// mark runs the holder's MarkInvoiced on a transaction of its own and
// answers its error; the transaction commits only when it succeeds.
func mark(t *testing.T, h *harness, ref contracts.InvoiceRef, sources ...contracts.WorkSource) error {
	t.Helper()
	holder := orders.Module().InvoicedWork(h.Deps())
	ctx := context.Background()
	return pgx.BeginFunc(ctx, h.Pool(), func(tx pgx.Tx) error { return holder.MarkInvoiced(ctx, tx, ref, sources) })
}

// expectSourceRefusal fails the test unless err is a WorkSourceRefusal with
// code.
func expectSourceRefusal(t *testing.T, err error, code, what string) {
	t.Helper()
	var r *contracts.WorkSourceRefusal
	if !errors.As(err, &r) || r.Code != code {
		t.Fatalf("%s: %v; want a source refusal %s", what, err, code)
	}
}

// Allocate writes a new draft row each time, at docmath's amount and
// revision 1, with the order's references; a quantity above what is left is
// allocation_exceeds_open and a line of a draft, held or unknown order is
// line_not_invoiceable, and neither writes anything. A released draft frees
// its quantity, and a repeat release is harmless.
func TestAllocate_NewRowsRefusalsAndRelease(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	p := orders.Module().BillableOrders(h.Deps())
	ctx := context.Background()
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10)))
	bolt := lineOf(t, o, varBolt)

	first := allocate(t, p, bolt.Id, "4")
	if first.Revision != 1 || first.State != contracts.AllocationDraft || first.Quantity != "4.000" || first.Amount != "400.00" ||
		first.UnitPrice != "100.00000000" || first.DiscountPercent != "0.00" || first.OrderID != o.Id || first.OrderNumber != o.Number ||
		first.Currency != "NOK" || first.YourReference != "PO-77" || first.VATCodeID != codeStandard || first.ProjectID != nil {
		t.Errorf("first allocation %+v; want a draft of 4 at 400.00 with the order's references", first)
	}
	second := allocate(t, p, bolt.Id, "4")
	if second.ID == first.ID {
		t.Error("a second Allocate rewrote the first draft")
	}
	_, err := p.Allocate(ctx, contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: bolt.Id, Quantity: "2.001"}}})
	expectAllocationRefusal(t, err, contracts.AllocationExceedsOpen, "above what is left")
	page, err := p.DueOrders(ctx, contracts.DueOrdersRequest{OrderIDs: []int64{o.Id}})
	if err != nil || len(page.Orders) != 1 || page.Orders[0].Lines[0].ToInvoice != "2.000" {
		t.Fatalf("due %+v, %v; want 2 left to invoice", page, err)
	}

	draft := createOrder(t, c, custBusiness, productLine(varBolt, 1))
	_, err = p.Allocate(ctx, contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: draft.Lines[0].Id, Quantity: "1"}}})
	expectAllocationRefusal(t, err, contracts.AllocationLineNotInvoiceable, "a draft order's line")
	_, err = p.Allocate(ctx, contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: 999999, Quantity: "1"}}})
	expectAllocationRefusal(t, err, contracts.AllocationLineNotInvoiceable, "an unknown line")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "hold"), gen.OrdersHoldInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version}),
		http.StatusOK, "hold")
	_, err = p.Allocate(ctx, contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: bolt.Id, Quantity: "1"}}})
	expectAllocationRefusal(t, err, contracts.AllocationLineNotInvoiceable, "a held order's line")
	if n := h.Count(t, `SELECT count(*) FROM orders.invoice_allocations WHERE order_id = $1`, o.Id); n != 2 {
		t.Errorf("%d allocations; the refusals wrote some", n)
	}

	if err := p.ReleaseAllocations(ctx, []int64{first.ID}); err != nil {
		t.Fatal(err)
	}
	if err := p.ReleaseAllocations(ctx, []int64{first.ID, 424242}); err != nil {
		t.Fatalf("a repeat release: %v", err)
	}
	got, err := p.Allocations(ctx, []int64{first.ID, second.ID, 424242})
	if err != nil || len(got) != 2 || got[0].State != contracts.AllocationReleased || got[1].State != contracts.AllocationDraft {
		t.Fatalf("allocations %+v, %v; want the first released, the second a draft, the unknown absent", got, err)
	}
	page, err = p.DueOrders(ctx, contracts.DueOrdersRequest{OrderIDs: []int64{o.Id}})
	if err != nil || page.Orders[0].Lines[0].ToInvoice != "6.000" {
		t.Fatalf("due %+v, %v; want the released 4 offered again", page, err)
	}
}

// Two concurrent Allocates of one line serialise on the order's lock: with
// 10 ordered and 6 asked by each, exactly one succeeds (O36).
func TestAllocate_ConcurrentNeverOverAllocates(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	p := orders.Module().BillableOrders(h.Deps())
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10)))
	lineID := o.Lines[0].Id
	for round := range 5 {
		h.Exec(t, `DELETE FROM orders.invoice_allocations WHERE order_id = $1`, o.Id)
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				<-start
				_, errs[i] = p.Allocate(context.Background(), contracts.AllocateRequest{Lines: []contracts.AllocateLine{{LineID: lineID, Quantity: "6"}}})
			})
		}
		close(start)
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
				continue
			}
			expectAllocationRefusal(t, err, contracts.AllocationExceedsOpen, fmt.Sprintf("round %d's loser", round))
		}
		if ok != 1 {
			t.Fatalf("round %d: %d of two Allocates succeeded; want exactly one", round, ok)
		}
		if n := h.Count(t, `SELECT count(*) FROM orders.invoice_allocations WHERE order_id = $1 AND state = 'draft'`, o.Id); n != 1 {
			t.Fatalf("round %d: %d drafts; want one", round, n)
		}
	}
}

// The holder stamps the allocations invoiced, adds them to the lines'
// invoiced, recomputes the invoice status and publishes
// orders.sales_order.invoiced.v1 once per order; the order page lists the
// invoice (O41). A full credit releases an allocation and gives its
// quantity back; a release of an allocation without the stamp is tolerated.
func TestInvoicedWork_MarkAndRelease(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	p := orders.Module().BillableOrders(h.Deps())
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10)))
	lineID := o.Lines[0].Id
	a := allocate(t, p, lineID, "4")
	b := allocate(t, p, lineID, "6")
	ref := invoiceRef(h, 77, 5001)
	if err := mark(t, h, ref, sourceOf(b), sourceOf(a)); err != nil {
		t.Fatal(err)
	}
	res := c.Do(http.MethodGet, orderPath(o.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	got := orderOf(t, res)
	if got.Lines[0].QtyInvoiced != 10 || got.Lines[0].QtyDraftAllocated != 0 || got.InvoiceStatus != gen.OrdersOrderInvoiceStatusInvoiced ||
		got.Version != o.Version {
		t.Errorf("order %+v; want 10 invoiced, the order invoiced at its version", got)
	}
	if len(got.Invoices) != 1 || got.Invoices[0].InvoiceId != 77 || got.Invoices[0].InvoiceNumber != 5001 || got.Invoices[0].Amount != 1000 ||
		got.Invoices[0].Currency != "NOK" || got.Invoices[0].State != gen.OrdersOrderInvoiceStateInvoiced {
		t.Errorf("invoices %+v; want invoice 5001 of 1000 NOK, invoiced", got.Invoices)
	}
	if n := eventsNamed(t, h, "orders.sales_order.invoiced", o.Id); n != 1 {
		t.Errorf("%d invoiced events, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.invoice_allocations WHERE order_id = $1 AND state = 'invoiced'
		AND invoice_id = 77 AND invoice_number = 5001 AND invoiced_at = $2 AND invoiced_by_display = 'Kari'`, o.Id, ref.IssuedAt); n != 2 {
		t.Errorf("%d allocations carry the stamp, want 2", n)
	}

	holder := orders.Module().InvoicedWork(h.Deps())
	credit := ref
	credit.IssuedAt = h.Now().Add(time.Hour)
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		return holder.ReleaseInvoiced(ctx, tx, credit, []contracts.WorkSource{sourceOf(a)})
	})
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		return holder.ReleaseInvoiced(ctx, tx, credit, []contracts.WorkSource{sourceOf(a), {Kind: contracts.WorkSourceOrderAllocation, ID: 424242}})
	})
	got = orderOf(t, c.Do(http.MethodGet, orderPath(o.Id), nil))
	if got.Lines[0].QtyInvoiced != 6 || got.InvoiceStatus != gen.OrdersOrderInvoiceStatusPartial ||
		len(got.Invoices) != 1 || got.Invoices[0].State != gen.OrdersOrderInvoiceStateInvoiced || got.Invoices[0].Amount != 600 ||
		got.Invoices[0].ReleasedAmount != 400 {
		t.Errorf("after one credit: %+v, invoices %+v; want 6 invoiced, partial, the invoice still invoiced at 600, 400 released",
			got.Lines[0], got.Invoices)
	}
	page, err := p.DueOrders(context.Background(), contracts.DueOrdersRequest{OrderIDs: []int64{o.Id}})
	if err != nil || len(page.Orders) != 1 || page.Orders[0].Lines[0].ToInvoice != "4.000" {
		t.Errorf("due %+v, %v; want the credited 4 to invoice again", page, err)
	}
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		return holder.ReleaseInvoiced(ctx, tx, credit, []contracts.WorkSource{sourceOf(b)})
	})
	got = orderOf(t, c.Do(http.MethodGet, orderPath(o.Id), nil))
	if got.Lines[0].QtyInvoiced != 0 || got.InvoiceStatus != gen.OrdersOrderInvoiceStatusNone || got.Invoices[0].State != gen.OrdersOrderInvoiceStateReleased ||
		got.Invoices[0].Amount != 0 || got.Invoices[0].ReleasedAmount != 1000 {
		t.Errorf("after the full credit: %+v, invoices %+v; want nothing invoiced and the invoice released, 0 billed and 1000 released",
			got.Lines[0], got.Invoices)
	}
}

// ReleaseAllocations takes the order, the lines and then the allocations, as
// the holder does (O38): while an issue's holder holds an order, a release of
// another allocation of it waits on the order's row rather than locking
// allocations out of order, and then releases it; a release naming the
// invoiced one leaves it.
func TestReleaseAllocations_WaitsOnTheHolderInLockOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	p := orders.Module().BillableOrders(h.Deps())
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10), productLine(varBolt, 5)))
	a := allocate(t, p, o.Lines[0].Id, "4")
	b := allocate(t, p, o.Lines[1].Id, "5")
	holder := orders.Module().InvoicedWork(h.Deps())
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := holder.MarkInvoiced(ctx, tx, invoiceRef(h, 7, 7), []contracts.WorkSource{sourceOf(a)}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.ReleaseAllocations(ctx, []int64{b.ID}) }()
	select {
	case err := <-done:
		t.Fatalf("the release finished (%v) while the holder held the order", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the release never finished")
	}
	if err := p.ReleaseAllocations(ctx, []int64{a.ID, b.ID}); err != nil {
		t.Fatalf("a release naming the invoiced one: %v", err)
	}
	got, err := p.Allocations(ctx, []int64{a.ID, b.ID})
	if err != nil || len(got) != 2 || got[0].State != contracts.AllocationInvoiced || got[1].State != contracts.AllocationReleased {
		t.Errorf("allocations %+v, %v; want the stamped one invoiced and the other released", got, err)
	}
}

// The holder's three refusals, in the contract's order, each writing
// nothing: already invoiced; released, held or no longer open
// (source_not_invoiceable); a revision or amount other than the source's
// (source_changed).
func TestInvoicedWork_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	p := orders.Module().BillableOrders(h.Deps())
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10)))
	lineID := o.Lines[0].Id

	done := allocate(t, p, lineID, "1")
	if err := mark(t, h, invoiceRef(h, 1, 1), sourceOf(done)); err != nil {
		t.Fatal(err)
	}
	expectSourceRefusal(t, mark(t, h, invoiceRef(h, 2, 2), sourceOf(done)), contracts.SourceAlreadyInvoiced, "invoiced again")

	released := allocate(t, p, lineID, "1")
	if err := p.ReleaseAllocations(context.Background(), []int64{released.ID}); err != nil {
		t.Fatal(err)
	}
	expectSourceRefusal(t, mark(t, h, invoiceRef(h, 3, 3), sourceOf(released)), contracts.SourceNotInvoiceable, "a released allocation")

	draft := allocate(t, p, lineID, "2")
	changed := sourceOf(draft)
	changed.Amount = "200.01"
	expectSourceRefusal(t, mark(t, h, invoiceRef(h, 4, 4), changed), contracts.SourceChanged, "another amount")
	changed = sourceOf(draft)
	changed.Amount = "200"
	if err := mark(t, h, invoiceRef(h, 5, 5), changed, sourceOf(done)); err == nil {
		t.Fatal("a batch with an invoiced source was stamped")
	}
	changed.Revision = 2
	expectSourceRefusal(t, mark(t, h, invoiceRef(h, 6, 6), changed), contracts.SourceChanged, "another revision")

	// The open quantity shrank under the draft (by hand, as no edit may).
	h.Exec(t, `UPDATE orders.lines SET qty_cancelled = 8 WHERE id = $1`, lineID)
	expectSourceRefusal(t, mark(t, h, invoiceRef(h, 7, 7), sourceOf(draft)), contracts.SourceNotInvoiceable, "no longer open")
	h.Exec(t, `UPDATE orders.lines SET qty_cancelled = 0 WHERE id = $1`, lineID)

	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "hold"), gen.OrdersHoldInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: o.Version}),
		http.StatusOK, "hold")
	expectSourceRefusal(t, mark(t, h, invoiceRef(h, 8, 8), sourceOf(draft)), contracts.SourceNotInvoiceable, "a held order")
	if n := h.Count(t, `SELECT count(*) FROM orders.invoice_allocations WHERE id = $1 AND state = 'draft'`, draft.ID); n != 1 {
		t.Error("a refused issue wrote the draft")
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.lines WHERE id = $1 AND qty_invoiced = 1`, lineID); n != 1 {
		t.Error("a refused issue moved the line's invoiced")
	}
	if n := eventsNamed(t, h, "orders.sales_order.invoiced", o.Id); n != 1 {
		t.Errorf("%d invoiced events; want only the first issue's", n)
	}
}

// A draft allocation counts as progress on an edit (O26) and stops a cancel
// (O36) until the order page releases it, which is logged.
func TestDraftAllocations_EditAndCancelRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	p := orders.Module().BillableOrders(h.Deps())
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10), productLine(varFood, 2)))
	bolt := lineOf(t, o, varBolt)
	allocate(t, p, bolt.Id, "4")
	o = orderOf(t, c.Do(http.MethodGet, orderPath(o.Id), nil))
	if l := lineOf(t, o, varBolt); l.QtyDraftAllocated != 4 {
		t.Errorf("draft-allocated %v, want 4", l.QtyDraftAllocated)
	}

	lines := keep(o)
	lines[0].Quantity = f64(3.999)
	expectRefusal(t, save(c, o, lines, nil), http.StatusBadRequest, "quantity_below_progress", "below the draft")
	lines = keep(o)
	lines[0].UnitPrice = f64(90)
	expectRefusal(t, save(c, o, lines, nil), http.StatusConflict, "line_in_draft_invoice", "a price change")
	lines = keep(o)
	lines[0].DiscountPercent = f64(5)
	expectRefusal(t, save(c, o, lines, nil), http.StatusConflict, "line_in_draft_invoice", "a discount change")
	expectRefusal(t, save(c, o, keep(o)[1:], nil), http.StatusConflict, "line_has_progress", "remove the line")
	lines = keep(o)
	lines[0].Quantity = f64(4)
	res := save(c, o, lines, nil)
	mustStatus(t, res, http.StatusOK, "down to the draft")
	o = orderOf(t, res)

	in := gen.OrdersVersionInput{ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version}
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), in), http.StatusConflict, "order_in_draft_invoice", "cancel")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel-remaining"), in), http.StatusConflict, "order_in_draft_invoice", "cancel remaining")

	res = c.Do(http.MethodPost, orderPath(o.Id, "release-draft-allocations"), in)
	mustStatus(t, res, http.StatusOK, "release draft allocations")
	o = orderOf(t, res)
	if l := lineOf(t, o, varBolt); l.QtyDraftAllocated != 0 || o.Version != in.Version {
		t.Errorf("after the release: line %+v, order at version %d; want nothing drafted at the same version", l, o.Version)
	}
	changes := changesOf(t, c, o.Id)
	if last := strings.Join(changes[len(changes)-1], ","); last != "draft_allocations_released" {
		t.Errorf("last change %s; want draft_allocations_released", last)
	}
	again := c.Do(http.MethodPost, orderPath(o.Id, "release-draft-allocations"), gen.OrdersVersionInput{ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version})
	mustStatus(t, again, http.StatusOK, "a release with none")
	if n := len(changesOf(t, c, o.Id)); n != len(changes) {
		t.Errorf("a release with nothing to release logged an entry")
	}

	lines = keep(o)
	lines[0].UnitPrice = f64(90)
	res = save(c, o, lines, nil)
	mustStatus(t, res, http.StatusOK, "a price change once released")
	o = orderOf(t, res)
	mustStatus(t, save(c, o, keep(o)[1:], nil), http.StatusOK, "a line released from its draft is removed")
	o = orderOf(t, c.Do(http.MethodGet, orderPath(o.Id), nil))
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version}),
		http.StatusOK, "cancel once released")
}

// A line an invoice stamped is kept by a removal even after a credit gave
// its allocation back: the order's invoices list it (O41).
func TestDraftAllocations_StampedLineIsKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	p := orders.Module().BillableOrders(h.Deps())
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10), productLine(varFood, 2)))
	a := allocate(t, p, lineOf(t, o, varBolt).Id, "10")
	ref := invoiceRef(h, 11, 11)
	if err := mark(t, h, ref, sourceOf(a)); err != nil {
		t.Fatal(err)
	}
	holder := orders.Module().InvoicedWork(h.Deps())
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		return holder.ReleaseInvoiced(ctx, tx, ref, []contracts.WorkSource{sourceOf(a)})
	})
	o = orderOf(t, c.Do(http.MethodGet, orderPath(o.Id), nil))
	expectRefusal(t, save(c, o, keep(o)[1:], nil), http.StatusConflict, "line_has_progress", "remove a credited line")
}

// The allocation's amount is docmath's line net — the rule Invoices builds
// its line by — for its quantity, unit price and discount, over generated
// lines: the order line's amount and the invoice line's always agree (O37).
// The document totals of one full invoice are the Invoices lane's
// integration test.
func TestAllocationAmount_EqualsTheInvoiceLine(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(37, 38))
	decimal := func(max, places int) *big.Rat {
		scale := 1
		for range places {
			scale *= 10
		}
		return big.NewRat(int64(rng.IntN(max*scale)+1), int64(scale))
	}
	for range 5000 {
		qty, price, discount := decimal(10000, 3), decimal(100000, 8), decimal(100, 2)
		discount = new(big.Rat).Sub(discount, big.NewRat(1, 100))
		got := orders.AllocationAmount(qty, price, discount)
		want := docmath.Line(qty, price, discount).Net
		if got.Cmp(want) != 0 {
			t.Fatalf("q %s p %s d %s: allocation %s, invoice line %s", qty.FloatString(3), price.FloatString(8), discount.FloatString(2),
				got.FloatString(4), want.FloatString(4))
		}
	}

	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	p := orders.Module().BillableOrders(h.Deps())
	var in []gen.OrdersLineInput
	for range 20 {
		l := productLine(varBolt, float64(rng.IntN(100000)+1)/1000)
		l.UnitPrice = f64(float64(rng.IntN(10000000)) / 10000)
		l.DiscountPercent = f64(float64(rng.IntN(10001)) / 100)
		in = append(in, l)
	}
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, in...))
	for _, l := range o.Lines {
		qty, _ := new(big.Rat).SetString(strconv.FormatFloat(*l.Quantity, 'f', -1, 64))
		part := new(big.Rat).Quo(qty, big.NewRat(int64(rng.IntN(3)+1), 1))
		text := part.FloatString(3)
		if text == "0.000" {
			text = "0.001"
		}
		a := allocate(t, p, l.Id, text)
		q, _ := new(big.Rat).SetString(a.Quantity)
		up, _ := new(big.Rat).SetString(a.UnitPrice)
		d, _ := new(big.Rat).SetString(a.DiscountPercent)
		if want := docmath.Line(q, up, d).Net.FloatString(2); a.Amount != want {
			t.Errorf("line %d: allocation amount %s; the invoice line built from %s × %s − %s%% nets %s", l.Position, a.Amount, a.Quantity, a.UnitPrice,
				a.DiscountPercent, want)
		}
	}
}
