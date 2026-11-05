package orders

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is the module's contracts.BillableOrders (orders design O36,
// O37): the order lines due to be invoiced, and the allocations an invoice
// line is built from. DueOrders writes nothing; Allocate and
// ReleaseAllocations write allocations, each in a short transaction of its
// own. It never reads a directory.

// policyOnOrder is a line invoiced on order; "delivery" is delivery C's.
const policyOnOrder = "order"

// billableOrders is Orders' contracts.BillableOrders, on the pool and the
// clock and nothing else.
type billableOrders struct {
	pool  *pgxpool.Pool
	clock func() time.Time
	// projects is whether Projects is on: an allocation names its order's
	// project only then.
	projects bool
}

var _ contracts.BillableOrders = (*billableOrders)(nil)

// newBillableOrders is Module's BillableOrders: built from d.Pool and
// d.Clock, and whether d.Projects is composed.
func newBillableOrders(d module.Deps) contracts.BillableOrders {
	return &billableOrders{pool: d.Pool, clock: d.Clock, projects: d.Projects != nil}
}

// DueOrders lists the orders the request names with their lines to invoice,
// in order id and line position order. A draft order and one on hold are
// answered with Blocked set. At most contracts.MaxBillableRows lines: when
// there were more, the last order — cut short — is left out, unless it is
// the only one, and More is set.
func (p *billableOrders) DueOrders(ctx context.Context, req contracts.DueOrdersRequest) (contracts.DueOrdersPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.DueOrdersPage{}, err
	}
	var until pgtype.Date
	if !req.Until.IsZero() {
		y, m, d := req.Until.Date()
		until = pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
	}
	rows, err := store.New(p.pool).DueOrderLines(ctx, store.DueOrderLinesParams{
		Every: req.Every, CustomerIds: nonNil(req.CustomerIDs), OrderIds: nonNil(req.OrderIDs),
		Until: until, RowLimit: contracts.MaxBillableRows + 1,
	})
	if err != nil {
		return contracts.DueOrdersPage{}, fmt.Errorf("orders: read due order lines: %w", err)
	}
	var page contracts.DueOrdersPage
	if len(rows) > contracts.MaxBillableRows {
		page.More = true
		rows = rows[:contracts.MaxBillableRows]
		last := rows[len(rows)-1].OrderID
		if rows[0].OrderID != last {
			for len(rows) > 0 && rows[len(rows)-1].OrderID == last {
				rows = rows[:len(rows)-1]
			}
		}
	}
	for _, r := range rows {
		if n := len(page.Orders); n == 0 || page.Orders[n-1].ID != r.OrderID {
			page.Orders = append(page.Orders, dueOrderOf(r))
		}
		o := &page.Orders[len(page.Orders)-1]
		o.Lines = append(o.Lines, contracts.DueOrderLine{
			LineID: r.LineID, Position: r.Position, Description: r.Description, Unit: r.Unit,
			ToInvoice: ratOrZero(r.ToInvoice).FloatString(quantityPlaces),
			UnitPrice: ratOrZero(r.UnitPrice).FloatString(pricePlaces), DiscountPercent: ratOrZero(r.DiscountPercent).FloatString(discountPlaces),
			VATCodeID: derefInt32(r.VatCodeID), InvoicePolicy: r.InvoicePolicy,
		})
	}
	return page, nil
}

// dueOrderOf is a due line's order header.
func dueOrderOf(r store.DueOrderLinesRow) contracts.DueOrder {
	terms := r.PaymentTermsDays
	o := contracts.DueOrder{
		ID: r.OrderID, Number: r.OrderNumber, CustomerID: r.CustomerID, Currency: r.Currency,
		ProjectID: r.ProjectID, YourReference: r.YourReference, PaymentTermsDays: &terms,
	}
	if r.DeliveryDate.Valid {
		d := r.DeliveryDate.Time
		o.DeliveryDate = &d
	}
	if r.DeliveryAddressLine1 != nil {
		o.DeliveryAddress = &contracts.CustomerAddressEntry{
			ID: derefInt32(r.DeliveryAddressID), Label: deref(r.DeliveryName), Line1: *r.DeliveryAddressLine1,
			Line2: deref(r.DeliveryAddressLine2), PostalCode: deref(r.DeliveryPostalCode),
			City: deref(r.DeliveryCity), Country: deref(r.DeliveryCountry),
		}
	}
	switch {
	case r.OrderStatus == orderDraft:
		o.Blocked = contracts.DueOrderBlockedDraft
	case r.OnHold:
		o.Blocked = contracts.DueOrderBlockedOnHold
	}
	return o
}

// Allocate writes a new draft allocation for each line named (O36), on one
// transaction of its own: the lines' orders are read first — a line never
// moves to another order — then the orders are locked FOR UPDATE in id
// order, then the lines, so two concurrent Allocates serialise on the orders
// and the second sees what the first allocated. Each line is judged under the
// locks — an order's priced product line invoiced on order, its order
// confirmed and not on hold (line_not_invoiceable), and a quantity within
// ordered − cancelled − invoiced − draft-allocated (allocation_exceeds_open)
// — and the first refusal answers and writes nothing. The allocations are
// answered in the request's order.
//
// The catalog's variants are not locked first (MB rule 21): an allocation
// writes no line and changes nothing a variant's delete or type change asks
// about — whether a document is open, what it names — so there is nothing
// for a variant's lock to hold still. Allocate never calls a contract.
func (p *billableOrders) Allocate(ctx context.Context, req contracts.AllocateRequest) ([]contracts.OrderAllocation, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	ids := make([]int64, len(req.Lines))
	for i, l := range req.Lines {
		ids[i] = l.LineID
	}
	owners, err := store.New(p.pool).LineOrders(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read the allocated lines' orders: %w", err)
	}
	orderIDs := make([]int64, 0, len(owners))
	for _, o := range owners {
		orderIDs = append(orderIDs, o.OrderID)
	}
	slices.Sort(orderIDs)
	orderIDs = slices.Compact(orderIDs)
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	out := make([]contracts.OrderAllocation, 0, len(req.Lines))
	err = db.WithTx(ctx, p.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		orders, err := q.LockOrdersByIDs(ctx, orderIDs)
		if err != nil {
			return fmt.Errorf("orders: lock the allocated lines' orders: %w", err)
		}
		lines, err := q.LockOrderLinesByIDs(ctx, sorted)
		if err != nil {
			return fmt.Errorf("orders: lock the allocated lines: %w", err)
		}
		drafts, err := q.DraftAllocatedByLine(ctx, sorted)
		if err != nil {
			return fmt.Errorf("orders: sum the allocated lines' drafts: %w", err)
		}
		orderByID := make(map[int64]store.OrdersOrder, len(orders))
		for _, o := range orders {
			orderByID[o.ID] = o
		}
		lineByID := byID(lines)
		drafted := make(map[int64]*big.Rat, len(drafts))
		for _, d := range drafts {
			drafted[d.LineID] = ratOrZero(d.Allocated)
		}
		now := p.clock()
		for _, in := range req.Lines {
			l, ok := lineByID[in.LineID]
			if !ok {
				return &contracts.AllocationRefusal{LineID: in.LineID, Code: contracts.AllocationLineNotInvoiceable,
					Detail: "The line is not a line of an order."}
			}
			o := orderByID[l.OwnerID]
			if why := notInvoiceable(o, l); why != "" {
				return &contracts.AllocationRefusal{LineID: in.LineID, Code: contracts.AllocationLineNotInvoiceable, Detail: why}
			}
			qty := mustRat(in.Quantity)
			open := toInvoice(l, orZero(drafted[l.ID]))
			if qty.Cmp(open) > 0 {
				return &contracts.AllocationRefusal{LineID: in.LineID, Code: contracts.AllocationExceedsOpen,
					Detail: fmt.Sprintf("Line %d of order %d has %s left to invoice, not %s.", l.Position, o.Number,
						open.FloatString(quantityPlaces), qty.FloatString(quantityPlaces))}
			}
			price, discount := ratOf(l.UnitPrice), ratOrZero(l.DiscountPercent)
			row, err := q.InsertAllocation(ctx, store.InsertAllocationParams{
				LineID: l.ID, OrderID: o.ID, Quantity: numeric(qty, quantityPlaces), UnitPrice: l.UnitPrice,
				DiscountPercent: l.DiscountPercent, Amount: numeric(allocationAmount(qty, price, discount), amountPlaces),
				VatCodeID: *l.VatCodeID, Now: now,
			})
			if err != nil {
				return fmt.Errorf("orders: allocate line %d: %w", l.ID, err)
			}
			out = append(out, p.allocationOf(store.AllocationsByIDsRow{
				ID: row.ID, Revision: row.Revision, OrderID: o.ID, OrderNumber: o.Number, LineID: l.ID,
				Quantity: row.Quantity, UnitPrice: row.UnitPrice, DiscountPercent: row.DiscountPercent, Amount: row.Amount,
				VatCodeID: row.VatCodeID, Description: l.Description, Unit: l.Unit, Currency: o.Currency,
				ProjectID: o.ProjectID, YourReference: o.YourReference, State: row.State,
			}))
			drafted[l.ID] = new(big.Rat).Add(orZero(drafted[l.ID]), qty)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// allocationAmount is an allocation's amount: docmath's line net for its
// quantity, unit price and discount (O37) — the rule Invoices builds its
// line by, so the two always agree.
func allocationAmount(quantity, unitPrice, discountPercent *big.Rat) *big.Rat {
	return docmath.Line(quantity, unitPrice, discountPercent).Net
}

// notInvoiceable is why an order line cannot be invoiced now, or "": it must
// be a priced product line with a VAT code, invoiced on order (on delivery is
// delivery C's), of a confirmed order not on hold.
func notInvoiceable(o store.OrdersOrder, l store.OrdersLine) string {
	switch {
	case l.Kind != kindProduct || !l.Quantity.Valid || !l.UnitPrice.Valid || l.VatCodeID == nil:
		return fmt.Sprintf("Line %d is not a priced product line.", l.Position)
	case l.InvoicePolicy != policyOnOrder:
		return fmt.Sprintf("Line %d is invoiced on delivery.", l.Position)
	case o.Status == orderDraft:
		return fmt.Sprintf("Order %d is a draft.", o.Number)
	case o.Status != orderConfirmed:
		return fmt.Sprintf("Order %d is %s.", o.Number, o.Status)
	case o.OnHold:
		return fmt.Sprintf("Order %d is on hold.", o.Number)
	}
	return ""
}

// toInvoice is what an order line invoiced on order has left to invoice:
// ordered − cancelled − invoiced − drafted, never below 0 (O36).
func toInvoice(l store.OrdersLine, drafted *big.Rat) *big.Rat {
	done := new(big.Rat).Add(ratOrZero(l.QtyInvoiced), drafted)
	return openQuantity(orZero(ratOf(l.Quantity)), ratOrZero(l.QtyCancelled), done)
}

// Allocations answers the allocations named as they stand, in id order; an
// id that does not exist is absent.
func (p *billableOrders) Allocations(ctx context.Context, ids []int64) ([]contracts.OrderAllocation, error) {
	if err := contracts.ValidateAllocationIDs(ids); err != nil {
		return nil, err
	}
	rows, err := store.New(p.pool).AllocationsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read allocations: %w", err)
	}
	out := make([]contracts.OrderAllocation, 0, len(rows))
	for _, r := range rows {
		out = append(out, p.allocationOf(r))
	}
	return out, nil
}

// ReleaseAllocations turns the named draft allocations released, in one
// statement; one invoiced, released or missing is left as it is, so a
// repeat is harmless.
func (p *billableOrders) ReleaseAllocations(ctx context.Context, ids []int64) error {
	if err := contracts.ValidateAllocationIDs(ids); err != nil {
		return err
	}
	now := p.clock()
	if _, err := store.New(p.pool).ReleaseDraftAllocations(ctx, store.ReleaseDraftAllocationsParams{Now: &now, Ids: ids}); err != nil {
		return fmt.Errorf("orders: release draft allocations: %w", err)
	}
	return nil
}

// allocationOf is an allocation as the contract answers it. The order's
// project is carried only while Projects is on (O38): Invoices refuses a
// source naming a project it cannot read.
func (p *billableOrders) allocationOf(r store.AllocationsByIDsRow) contracts.OrderAllocation {
	a := contracts.OrderAllocation{
		ID: r.ID, Revision: r.Revision, OrderID: r.OrderID, OrderNumber: r.OrderNumber, LineID: r.LineID,
		Quantity: ratOrZero(r.Quantity).FloatString(quantityPlaces), UnitPrice: ratOrZero(r.UnitPrice).FloatString(pricePlaces),
		DiscountPercent: ratOrZero(r.DiscountPercent).FloatString(discountPlaces), Amount: ratOrZero(r.Amount).FloatString(amountPlaces),
		VATCodeID: r.VatCodeID, Description: r.Description, Unit: r.Unit, Currency: r.Currency,
		YourReference: r.YourReference, State: r.State,
	}
	if p.projects {
		a.ProjectID = r.ProjectID
	}
	if r.PeriodStart.Valid && r.PeriodEnd.Valid {
		start, end := r.PeriodStart.Time, r.PeriodEnd.Time
		a.PeriodStart, a.PeriodEnd = &start, &end
	}
	return a
}

// derefInt32 is *p, 0 for nil.
func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// nonNil is s, or an empty slice for nil, so the query's ANY reads an empty
// array rather than NULL.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
