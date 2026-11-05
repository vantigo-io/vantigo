package orders

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is the module's contracts.BillableOrders (orders design O36,
// O37): the order lines due to be invoiced, and the allocations an invoice
// line is built from. DueOrders writes nothing; Allocate and
// ReleaseAllocations write allocations, each in a short transaction of its
// own. It never reads a directory.

// errAllocationsNotYet answers the allocation commands until delivery B's
// Orders lane writes them.
var errAllocationsNotYet = errors.New("orders: invoice allocations are not implemented yet")

// billableOrders is Orders' contracts.BillableOrders, on the pool and the
// clock and nothing else.
type billableOrders struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

var _ contracts.BillableOrders = (*billableOrders)(nil)

// newBillableOrders is Module's BillableOrders: built from d.Pool and
// d.Clock.
func newBillableOrders(d module.Deps) contracts.BillableOrders {
	return &billableOrders{pool: d.Pool, clock: d.Clock}
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

// Allocate is not written yet (errAllocationsNotYet).
func (p *billableOrders) Allocate(_ context.Context, req contracts.AllocateRequest) ([]contracts.OrderAllocation, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return nil, errAllocationsNotYet
}

// Allocations is not written yet (errAllocationsNotYet).
func (p *billableOrders) Allocations(_ context.Context, ids []int64) ([]contracts.OrderAllocation, error) {
	if err := contracts.ValidateAllocationIDs(ids); err != nil {
		return nil, err
	}
	return nil, errAllocationsNotYet
}

// ReleaseAllocations is not written yet (errAllocationsNotYet).
func (p *billableOrders) ReleaseAllocations(_ context.Context, ids []int64) error {
	if err := contracts.ValidateAllocationIDs(ids); err != nil {
		return err
	}
	return errAllocationsNotYet
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
