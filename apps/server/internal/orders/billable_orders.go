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

// policyOnOrder is a line invoiced on order; policyOnDelivery (stock.go) one
// invoiced as it is delivered (O48).
const policyOnOrder = "order"

// billableOrders is Orders' contracts.BillableOrders, on the pool, the clock
// and the business calendar, and nothing else.
type billableOrders struct {
	pool  *pgxpool.Pool
	clock func() time.Time
	// calendar is the business calendar a subscription's today is judged in
	// (O43); nil is UTC.
	calendar contracts.BusinessCalendar
	// projects is whether Projects is on: an allocation names its order's
	// project only then.
	projects bool
}

var _ contracts.BillableOrders = (*billableOrders)(nil)

// newBillableOrders is Module's BillableOrders: built from d.Pool, d.Clock
// and d.Calendar, and whether d.Projects is composed.
func newBillableOrders(d module.Deps) contracts.BillableOrders {
	return &billableOrders{pool: d.Pool, clock: d.Clock, calendar: d.Calendar, projects: d.Projects != nil}
}

// zone is the business time zone, read before any transaction: the zone a
// subscription's today — the default through date and the twelve-month
// limit (O43) — is judged in, as Invoices judges its own.
func (p *billableOrders) zone(ctx context.Context) (*time.Location, error) {
	if p.calendar == nil {
		return time.UTC, nil
	}
	loc, err := p.calendar.Zone(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: read the business time zone: %w", err)
	}
	return loc, nil
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
	var page contracts.DueOrdersPage
	// Orders first: every request but one naming subscriptions only.
	var rows []store.DueOrderLinesRow
	if req.Every || len(req.CustomerIDs) > 0 || len(req.OrderIDs) > 0 {
		var err error
		rows, err = store.New(p.pool).DueOrderLines(ctx, store.DueOrderLinesParams{
			Every: req.Every, CustomerIds: nonNil(req.CustomerIDs), OrderIds: nonNil(req.OrderIDs),
			Until: until, RowLimit: contracts.MaxBillableRows + 1,
		})
		if err != nil {
			return contracts.DueOrdersPage{}, fmt.Errorf("orders: read due order lines: %w", err)
		}
	}
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
	if page.More || (len(req.OrderIDs) > 0 && len(req.SubscriptionIDs) == 0) {
		return page, nil
	}
	return p.dueSubscriptions(ctx, req, page)
}

// dueSubscriptions adds to page, after its orders, the subscriptions the
// request names with their due periods (O43): every period due through
// Until — today in the business zone when zero — and starting no later than
// twelve months after today, each line in force on its first day with no live
// allocation, in subscription, period and position order. The lines stop at
// contracts.MaxBillableRows all told: a subscription cut short is left out,
// unless it is the first document, and More is set.
func (p *billableOrders) dueSubscriptions(ctx context.Context, req contracts.DueOrdersRequest, page contracts.DueOrdersPage) (contracts.DueOrdersPage, error) {
	loc, err := p.zone(ctx)
	if err != nil {
		return page, err
	}
	today := dayOf(p.clock(), loc)
	through := today
	if !req.Until.IsZero() {
		through = civil(req.Until)
	}
	q := store.New(p.pool)
	subs, err := q.BillableSubscriptions(ctx, store.BillableSubscriptionsParams{
		Every: req.Every, CustomerIds: nonNil(req.CustomerIDs), SubscriptionIds: nonNil(req.SubscriptionIDs),
	})
	if err != nil {
		return page, fmt.Errorf("orders: read the billable subscriptions: %w", err)
	}
	schedules, err := readSchedules(ctx, q, subs)
	if err != nil {
		return page, err
	}
	count := 0
	for _, o := range page.Orders {
		count += len(o.Lines)
	}
	for _, sch := range schedules {
		periods := sch.due(through, today)
		if len(periods) == 0 {
			continue
		}
		d := dueSubscriptionOf(sch.sub)
		for _, dp := range periods {
			start, end := dp.period.start, dp.period.end
			for _, l := range dp.lines {
				a := periodAmountOf(dp.period, ratOf(l.Quantity), ratOf(l.UnitPrice), ratOrZero(l.DiscountPercent))
				d.Lines = append(d.Lines, contracts.DueOrderLine{
					LineID: l.ID, Position: l.Position, Description: l.Description, Unit: l.Unit,
					ToInvoice: a.quantity.FloatString(quantityPlaces), UnitPrice: a.unitPrice.FloatString(pricePlaces),
					DiscountPercent: a.discount.FloatString(discountPlaces), VATCodeID: derefInt32(l.VatCodeID),
					InvoicePolicy: policyOnOrder, PeriodStart: &start, PeriodEnd: &end,
				})
			}
		}
		if count+len(d.Lines) > contracts.MaxBillableRows {
			page.More = true
			if len(page.Orders) == 0 {
				d.Lines = d.Lines[:contracts.MaxBillableRows]
				page.Orders = append(page.Orders, d)
			}
			return page, nil
		}
		count += len(d.Lines)
		page.Orders = append(page.Orders, d)
	}
	return page, nil
}

// dueSubscriptionOf is a due subscription's header, as a DueOrder of kind
// subscription. A subscription has no delivery date and is never blocked:
// one that cannot bill has no due period.
func dueSubscriptionOf(sub store.OrdersSubscription) contracts.DueOrder {
	terms := sub.PaymentTermsDays
	d := contracts.DueOrder{
		Kind: contracts.DueKindSubscription, ID: sub.ID, Number: sub.Number, CustomerID: sub.CustomerID, Currency: sub.Currency,
		ProjectID: sub.ProjectID, YourReference: sub.YourReference, PaymentTermsDays: &terms,
	}
	if sub.DeliveryAddressLine1 != nil {
		d.DeliveryAddress = &contracts.CustomerAddressEntry{
			ID: derefInt32(sub.DeliveryAddressID), Label: deref(sub.DeliveryName), Line1: *sub.DeliveryAddressLine1,
			Line2: deref(sub.DeliveryAddressLine2), PostalCode: deref(sub.DeliveryPostalCode),
			City: deref(sub.DeliveryCity), Country: deref(sub.DeliveryCountry),
		}
	}
	return d
}

// readSchedules reads the lines, live allocations and skips of subs on q
// and answers their schedules, in subs' order.
func readSchedules(ctx context.Context, q *store.Queries, subs []store.OrdersSubscription) ([]schedule, error) {
	if len(subs) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(subs))
	for i, sub := range subs {
		ids[i] = sub.ID
	}
	lines, err := q.SubscriptionLinesOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read the subscriptions' lines: %w", err)
	}
	allocations, err := q.LiveAllocationsOfSubscriptions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read the subscriptions' allocations: %w", err)
	}
	skips, err := q.SkipsOfSubscriptions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("orders: read the subscriptions' skipped periods: %w", err)
	}
	linesBy := map[int64][]store.OrdersLine{}
	for _, l := range lines {
		linesBy[l.OwnerID] = append(linesBy[l.OwnerID], l)
	}
	allocationsBy := map[int64][]store.OrdersInvoiceAllocation{}
	for _, a := range allocations {
		if a.SubscriptionID != nil {
			allocationsBy[*a.SubscriptionID] = append(allocationsBy[*a.SubscriptionID], a)
		}
	}
	skipsBy := map[int64][]pgtype.Date{}
	for _, sk := range skips {
		skipsBy[sk.SubscriptionID] = append(skipsBy[sk.SubscriptionID], sk.PeriodStart)
	}
	out := make([]schedule, 0, len(subs))
	for _, sub := range subs {
		out = append(out, newSchedule(sub, linesBy[sub.ID], allocationsBy[sub.ID], skipsBy[sub.ID]))
	}
	return out, nil
}

// dueOrderOf is a due line's order header.
func dueOrderOf(r store.DueOrderLinesRow) contracts.DueOrder {
	terms := r.PaymentTermsDays
	o := contracts.DueOrder{
		Kind: contracts.DueKindOrder, ID: r.OrderID, Number: r.OrderNumber, CustomerID: r.CustomerID, Currency: r.Currency,
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
	var orderIDs []int64
	if len(ids) > 0 {
		owners, err := store.New(p.pool).LineOrders(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("orders: read the allocated lines' orders: %w", err)
		}
		for _, o := range owners {
			orderIDs = append(orderIDs, o.OrderID)
		}
	}
	slices.Sort(orderIDs)
	orderIDs = slices.Compact(orderIDs)
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	subscriptionIDs := make([]int64, 0, len(req.Subscriptions))
	for _, sub := range req.Subscriptions {
		subscriptionIDs = append(subscriptionIDs, sub.SubscriptionID)
	}
	slices.Sort(subscriptionIDs)
	loc := time.UTC
	if len(req.Subscriptions) > 0 {
		var err error
		if loc, err = p.zone(ctx); err != nil {
			return nil, err
		}
	}
	out := make([]contracts.OrderAllocation, 0, len(req.Lines))
	err := db.WithTx(ctx, p.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		out = out[:0]
		var orders []store.OrdersOrder
		var lines []store.OrdersLine
		var drafts []store.DraftAllocatedByLineRow
		if len(sorted) > 0 {
			var err error
			if orders, err = q.LockOrdersByIDs(ctx, orderIDs); err != nil {
				return fmt.Errorf("orders: lock the allocated lines' orders: %w", err)
			}
		}
		var subs []store.OrdersSubscription
		if len(subscriptionIDs) > 0 {
			var err error
			if subs, err = q.LockSubscriptionsByIDs(ctx, subscriptionIDs); err != nil {
				return fmt.Errorf("orders: lock the allocated subscriptions: %w", err)
			}
		}
		if len(sorted) > 0 {
			var err error
			if lines, err = q.LockOrderLinesByIDs(ctx, sorted); err != nil {
				return fmt.Errorf("orders: lock the allocated lines: %w", err)
			}
			if drafts, err = q.DraftAllocatedByLine(ctx, sorted); err != nil {
				return fmt.Errorf("orders: sum the allocated lines' drafts: %w", err)
			}
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
				LineID: l.ID, OrderID: &o.ID, Quantity: numeric(qty, quantityPlaces), UnitPrice: l.UnitPrice,
				DiscountPercent: l.DiscountPercent, Amount: numeric(allocationAmount(qty, price, discount), amountPlaces),
				VatCodeID: *l.VatCodeID, Now: now,
			})
			if err != nil {
				return fmt.Errorf("orders: allocate line %d: %w", l.ID, err)
			}
			out = append(out, p.allocationOf(store.AllocationsByIDsRow{
				ID: row.ID, Revision: row.Revision, Kind: row.Kind, OrderID: o.ID, OrderNumber: o.Number, LineID: l.ID,
				Quantity: row.Quantity, UnitPrice: row.UnitPrice, DiscountPercent: row.DiscountPercent, Amount: row.Amount,
				VatCodeID: row.VatCodeID, Description: l.Description, Unit: l.Unit, Currency: o.Currency,
				ProjectID: o.ProjectID, YourReference: o.YourReference, State: row.State,
			}))
			drafted[l.ID] = new(big.Rat).Add(orZero(drafted[l.ID]), qty)
		}
		if len(req.Subscriptions) == 0 {
			return nil
		}
		made, err := p.allocatePeriods(ctx, q, subs, req.Subscriptions, now, dayOf(now, loc))
		if err != nil {
			return err
		}
		out = append(out, made...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// allocatePeriods writes, for each subscription asked in the request's
// order, one draft allocation per period due through its date and per line
// in force on the period's first day without a live allocation (O43), on
// q's transaction, which holds the subscriptions' rows; today is the
// business day of now, which bounds the twelve months ahead. A subscription gone,
// a draft or cancelled is subscription_not_invoiceable; one with no period
// due nothing_due. The unique live period index refuses a second live
// allocation of a line's period, should any writer bypass the subscription's
// lock.
func (p *billableOrders) allocatePeriods(ctx context.Context, q *store.Queries, locked []store.OrdersSubscription,
	asked []contracts.AllocateSubscription, now, today time.Time) ([]contracts.OrderAllocation, error) {
	schedules, err := readSchedules(ctx, q, locked)
	if err != nil {
		return nil, err
	}
	byID := map[int64]schedule{}
	for _, sch := range schedules {
		byID[sch.sub.ID] = sch
	}
	var out []contracts.OrderAllocation
	for _, in := range asked {
		sch, ok := byID[in.SubscriptionID]
		if !ok {
			return nil, &contracts.AllocationRefusal{SubscriptionID: in.SubscriptionID, Code: contracts.AllocationSubscriptionNotInvoiceable,
				Detail: "There is no such subscription."}
		}
		sub := sch.sub
		switch sub.Status {
		case subscriptionActive, subscriptionPaused, subscriptionEnded:
		default:
			return nil, &contracts.AllocationRefusal{SubscriptionID: sub.ID, Code: contracts.AllocationSubscriptionNotInvoiceable,
				Detail: fmt.Sprintf("Subscription %d is %s.", sub.Number, sub.Status)}
		}
		periods := sch.due(in.Through, today)
		if len(periods) == 0 {
			return nil, &contracts.AllocationRefusal{SubscriptionID: sub.ID, Code: contracts.AllocationNothingDue,
				Detail: fmt.Sprintf("Subscription %d has no period due through %s.", sub.Number, civil(in.Through).Format(time.DateOnly))}
		}
		for _, dp := range periods {
			start, end := dp.period.start, dp.period.end
			for _, l := range dp.lines {
				a := periodAmountOf(dp.period, ratOf(l.Quantity), ratOf(l.UnitPrice), ratOrZero(l.DiscountPercent))
				row, err := q.InsertPeriodAllocation(ctx, store.InsertPeriodAllocationParams{
					LineID: l.ID, SubscriptionID: &sub.ID, PeriodStart: pgDate(&start), PeriodEnd: pgDate(&end),
					Quantity: numeric(a.quantity, quantityPlaces), UnitPrice: numeric(a.unitPrice, pricePlaces),
					DiscountPercent: numeric(a.discount, discountPlaces), Amount: numeric(a.amount, amountPlaces),
					VatCodeID: *l.VatCodeID, Now: now,
				})
				if err != nil {
					return nil, fmt.Errorf("orders: allocate subscription %d's line %d for %s: %w", sub.ID, l.ID, start.Format(time.DateOnly), err)
				}
				out = append(out, p.allocationOf(store.AllocationsByIDsRow{
					ID: row.ID, Revision: row.Revision, Kind: row.Kind, OrderID: sub.ID, OrderNumber: sub.Number, LineID: l.ID,
					Quantity: row.Quantity, UnitPrice: row.UnitPrice, DiscountPercent: row.DiscountPercent, Amount: row.Amount,
					VatCodeID: row.VatCodeID, Description: l.Description, Unit: l.Unit, Currency: sub.Currency,
					ProjectID: sub.ProjectID, YourReference: sub.YourReference, State: row.State,
					PeriodStart: row.PeriodStart, PeriodEnd: row.PeriodEnd,
				}))
			}
		}
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
// be a priced product line with a VAT code, invoiced on order or on delivery
// (O48), of a confirmed order not on hold, and not a kit's child line — its
// kit line is invoiced for it (manufacturing phase 2 M145).
func notInvoiceable(o store.OrdersOrder, l store.OrdersLine) string {
	switch {
	case l.ParentLineID != nil:
		return fmt.Sprintf("Line %d is a component of a kit; the kit's line is invoiced.", l.Position)
	case !pricedKind(l.Kind) || !l.Quantity.Valid || !l.UnitPrice.Valid || l.VatCodeID == nil:
		return fmt.Sprintf("Line %d is not a priced product or shipping line.", l.Position)
	case l.InvoicePolicy != policyOnOrder && l.InvoicePolicy != policyOnDelivery:
		return fmt.Sprintf("Line %d has an invoice policy this version does not invoice.", l.Position)
	case o.Status == orderDraft:
		return fmt.Sprintf("Order %d is a draft.", o.Number)
	case o.Status != orderConfirmed:
		return fmt.Sprintf("Order %d is %s.", o.Number, o.Status)
	case o.OnHold:
		return fmt.Sprintf("Order %d is on hold.", o.Number)
	}
	return ""
}

// toInvoice is what an order line has left to invoice, never below 0 (O36,
// O48): on order, ordered − cancelled − invoiced − drafted; on delivery,
// delivered − invoiced − drafted.
func toInvoice(l store.OrdersLine, drafted *big.Rat) *big.Rat {
	done := new(big.Rat).Add(ratOrZero(l.QtyInvoiced), drafted)
	return invoiceOpen(l, done)
}

// invoiceOpen is what of an order line is billable less done, never below 0:
// what is ordered less cancelled on order, what is delivered on delivery
// (O48).
func invoiceOpen(l store.OrdersLine, done *big.Rat) *big.Rat {
	if l.InvoicePolicy == policyOnDelivery {
		return openQuantity(ratOrZero(l.QtyDelivered), new(big.Rat), done)
	}
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

// ReleaseAllocations turns the named draft allocations released on one
// transaction of its own; one invoiced, released or missing is left as it
// is, so a repeat is harmless. It takes its locks in the order every writer
// of an allocation does (O38): the allocations' orders, their lines, then
// the allocations, each in id order (lockAllocationRows) — so it waits on an
// issue's holder or an Allocate rather than crossing one.
func (p *billableOrders) ReleaseAllocations(ctx context.Context, ids []int64) error {
	if err := contracts.ValidateAllocationIDs(ids); err != nil {
		return err
	}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	return db.WithTx(ctx, p.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := lockAllocationRows(ctx, q, sorted); err != nil {
			return err
		}
		now := p.clock()
		if _, err := q.ReleaseDraftAllocations(ctx, store.ReleaseDraftAllocationsParams{Now: &now, Ids: sorted}); err != nil {
			return fmt.Errorf("orders: release draft allocations: %w", err)
		}
		return nil
	})
}

// allocationOf is an allocation as the contract answers it. The order's
// project is carried only while Projects is on (O38): Invoices refuses a
// source naming a project it cannot read.
func (p *billableOrders) allocationOf(r store.AllocationsByIDsRow) contracts.OrderAllocation {
	a := contracts.OrderAllocation{
		ID: r.ID, Revision: r.Revision, Kind: r.Kind, OrderID: r.OrderID, OrderNumber: r.OrderNumber, LineID: r.LineID,
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
