package invoices_test

import (
	"cmp"
	"context"
	"errors"
	"math/big"
	"slices"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// fakeOrders is Orders' invoicing reads and commands (contracts.BillableOrders)
// over orders a test puts in, for the From orders wizard, a held
// allocation's freshness, the release after a draft's delete or save and the
// billing run, without composing orders (depguard keeps it out of this
// package's tests). It keeps Orders' arithmetic: a line's to-invoice is its
// ordered quantity less every allocation not released, and an allocation's
// amount is docmath's line net. Every call is recorded with whether it was
// made inside one of this module's locked transactions — which it never may
// be. Safe for concurrent use.
type fakeOrders struct {
	mu          sync.Mutex
	orders      map[int64]fakeOrder
	allocations map[int64]contracts.OrderAllocation
	next        int64
	calls       []ordersCall
	released    []int64
	// failRelease, when set, is what every ReleaseAllocations fails with.
	failRelease error
	// periods are the subscription periods Allocate gives a line's
	// allocation, by line id.
	periods map[int64][2]*time.Time
	// variants are the product variants of order lines, by line id; a line
	// without one is free text.
	variants map[int64]int32
}

// fakeOrder is one order as Orders holds it: the header DueOrders answers and
// each line's ordered quantity.
type fakeOrder struct {
	head    contracts.DueOrder
	lines   []contracts.DueOrderLine
	ordered map[int64]string
}

type ordersCall struct {
	method string
	ids    []int64
	locked bool
}

var _ contracts.BillableOrders = (*fakeOrders)(nil)

func newFakeOrders() *fakeOrders {
	return &fakeOrders{orders: map[int64]fakeOrder{}, allocations: map[int64]contracts.OrderAllocation{}, periods: map[int64][2]*time.Time{}, variants: map[int64]int32{}, next: 7001}
}

func (f *fakeOrders) options() []modtest.Option {
	return []modtest.Option{modtest.WithBillableOrders(f)}
}

// orderLine is an order line of quantity at price and discount on code.
type orderLine struct {
	id                        int64
	description, unit         string
	quantity, price, discount string
	vatCode                   int32
}

// put stores order with its lines, each ordered in full and nothing invoiced.
func (f *fakeOrders) put(head contracts.DueOrder, lines ...orderLine) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := fakeOrder{head: head, ordered: map[int64]string{}}
	for i, l := range lines {
		o.lines = append(o.lines, contracts.DueOrderLine{
			LineID: l.id, Position: int32(i + 1), Description: l.description, Unit: l.unit,
			UnitPrice: l.price, DiscountPercent: l.discount, VATCodeID: l.vatCode, InvoicePolicy: "order",
		})
		o.ordered[l.id] = l.quantity
	}
	f.orders[head.ID] = o
}

// block sets an order's Blocked.
func (f *fakeOrders) block(orderID int64, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[orderID]
	o.head.Blocked = reason
	f.orders[orderID] = o
}

// periodOf makes line's allocations bill a subscription period, first to
// last, as delivery S's do.
func (f *fakeOrders) periodOf(lineID int64, first, last string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.periods[lineID] = [2]*time.Time{dayOf(first), dayOf(last)}
}

// allocation answers a stored allocation as it stands.
func (f *fakeOrders) allocation(id int64) contracts.OrderAllocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.allocations[id]
}

// live is the allocations not released, by id.
func (f *fakeOrders) live() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int64
	for id, a := range f.allocations {
		if a.State != contracts.AllocationReleased {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// releases is every id ReleaseAllocations was asked to release, in order.
func (f *fakeOrders) releases() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.released)
}

// nextAllocation is the id the next allocation gets.
func (f *fakeOrders) nextAllocation() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.next
}

func (f *fakeOrders) failReleases(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failRelease = err
}

func (f *fakeOrders) reads() []ordersCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeOrders) record(ctx context.Context, method string, ids []int64) {
	f.calls = append(f.calls, ordersCall{method: method, ids: slices.Clone(ids), locked: invoices.InLockedTx(ctx)})
}

// open is what is left to invoice of line l of o: ordered less every live
// allocation. Called with f.mu held.
func (f *fakeOrders) open(o fakeOrder, lineID int64) *big.Rat {
	left, _ := new(big.Rat).SetString(o.ordered[lineID])
	for _, a := range f.allocations {
		if a.LineID == lineID && a.State != contracts.AllocationReleased {
			q, _ := new(big.Rat).SetString(a.Quantity)
			left.Sub(left, q)
		}
	}
	return left
}

func (f *fakeOrders) DueOrders(ctx context.Context, req contracts.DueOrdersRequest) (contracts.DueOrdersPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.DueOrdersPage{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(ctx, "DueOrders", req.OrderIDs)
	var page contracts.DueOrdersPage
	for _, o := range f.orders {
		switch {
		case len(req.CustomerIDs) > 0 && !slices.Contains(req.CustomerIDs, o.head.CustomerID),
			len(req.OrderIDs) > 0 && !slices.Contains(req.OrderIDs, o.head.ID),
			!req.Until.IsZero() && o.head.DeliveryDate != nil && o.head.DeliveryDate.After(req.Until):
			continue
		}
		head := o.head
		head.Lines = nil
		for _, l := range o.lines {
			if left := f.open(o, l.LineID); left.Sign() > 0 {
				l.ToInvoice = left.FloatString(3)
				head.Lines = append(head.Lines, l)
			}
		}
		if len(head.Lines) > 0 {
			page.Orders = append(page.Orders, head)
		}
	}
	slices.SortFunc(page.Orders, func(a, b contracts.DueOrder) int { return cmp.Compare(a.ID, b.ID) })
	return page, nil
}

func (f *fakeOrders) Allocate(ctx context.Context, req contracts.AllocateRequest) ([]contracts.OrderAllocation, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(ctx, "Allocate", nil)
	type found struct {
		order fakeOrder
		line  contracts.DueOrderLine
	}
	var lines []found
	for _, l := range req.Lines {
		var hit *found
		for _, o := range f.orders {
			if i := slices.IndexFunc(o.lines, func(dl contracts.DueOrderLine) bool { return dl.LineID == l.LineID }); i >= 0 {
				hit = &found{order: o, line: o.lines[i]}
			}
		}
		if hit == nil || hit.order.head.Blocked != "" {
			return nil, &contracts.AllocationRefusal{LineID: l.LineID, Code: contracts.AllocationLineNotInvoiceable, Detail: "not invoiceable"}
		}
		q, _ := new(big.Rat).SetString(l.Quantity)
		if q.Cmp(f.open(hit.order, l.LineID)) > 0 {
			return nil, &contracts.AllocationRefusal{LineID: l.LineID, Code: contracts.AllocationExceedsOpen, Detail: "more than is left"}
		}
		lines = append(lines, *hit)
	}
	out := make([]contracts.OrderAllocation, 0, len(lines))
	for i, l := range req.Lines {
		h, dl := lines[i].order.head, lines[i].line
		q, _ := new(big.Rat).SetString(l.Quantity)
		price, _ := new(big.Rat).SetString(dl.UnitPrice)
		discount, _ := new(big.Rat).SetString(dl.DiscountPercent)
		a := contracts.OrderAllocation{
			ID: f.next, Revision: 1, OrderID: h.ID, OrderNumber: h.Number, LineID: l.LineID,
			Quantity: q.FloatString(3), UnitPrice: dl.UnitPrice, DiscountPercent: dl.DiscountPercent,
			Amount: docmath.Line(q, price, discount).Net.FloatString(2), VATCodeID: dl.VATCodeID,
			Description: dl.Description, Unit: dl.Unit, Currency: h.Currency, ProjectID: h.ProjectID,
			YourReference: h.YourReference, State: contracts.AllocationDraft,
		}
		if p, ok := f.periods[l.LineID]; ok {
			a.PeriodStart, a.PeriodEnd = p[0], p[1]
		}
		if v, ok := f.variants[l.LineID]; ok {
			a.VariantID = &v
		}
		f.next++
		f.allocations[a.ID] = a
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeOrders) Allocations(ctx context.Context, ids []int64) ([]contracts.OrderAllocation, error) {
	if err := contracts.ValidateAllocationIDs(ids); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(ctx, "Allocations", ids)
	var out []contracts.OrderAllocation
	for _, id := range ids {
		if a, ok := f.allocations[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeOrders) ReleaseAllocations(ctx context.Context, ids []int64) error {
	if err := contracts.ValidateAllocationIDs(ids); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(ctx, "ReleaseAllocations", ids)
	if f.failRelease != nil {
		return f.failRelease
	}
	f.released = append(f.released, ids...)
	for _, id := range ids {
		if a, ok := f.allocations[id]; ok && a.State == contracts.AllocationDraft {
			a.State = contracts.AllocationReleased
			f.allocations[id] = a
		}
	}
	return nil
}

// invoice stamps an allocation invoiced, as Orders' holder does at the
// issue.
func (f *fakeOrders) invoice(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.allocations[id]
	a.State = contracts.AllocationInvoiced
	f.allocations[id] = a
}

var errReleaseDown = errors.New("orders is down")

// dayOf is a date as the contract carries it.
func dayOf(s string) *time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return &d
}
