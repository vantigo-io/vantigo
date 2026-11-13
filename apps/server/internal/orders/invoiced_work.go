package orders

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"math/big"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is the module's contracts.InvoicedWorkHolder (orders design O38,
// module-boundaries rule 10): the invoices issue stamps the allocations an
// invoice bills as invoiced, inside its own transaction, and the issue of the
// credit note that returns an allocation's line in full releases it. It runs
// on the caller's transaction and touches nothing else: no pool, no
// directory, no clock — every timestamp it writes is ref.IssuedAt. It is the
// one holder that publishes (MB rule 10, as amended by orders design O38):
// orders.sales_order.invoiced.v1 goes to the platform outbox on the issue's
// own transaction.

// invoicedWorkHolder is this module's contracts.InvoicedWorkHolder. It holds
// a logger and nothing else: its constructor runs for a module that is
// switched off too, and every row it writes arrives through the caller's tx.
type invoicedWorkHolder struct {
	logger *slog.Logger
}

var _ contracts.InvoicedWorkHolder = (*invoicedWorkHolder)(nil)

// newInvoicedWorkHolder is Module's InvoicedWork. It keeps d.Logger only.
func newInvoicedWorkHolder(d module.Deps) contracts.InvoicedWorkHolder {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &invoicedWorkHolder{logger: logger}
}

// Kinds is the one kind this module holds: an invoice allocation.
func (h *invoicedWorkHolder) Kinds() []contracts.WorkSourceKind {
	return []contracts.WorkSourceKind{contracts.WorkSourceOrderAllocation}
}

// MarkInvoiced locks the sources' orders by id, then their lines, then the
// allocations, each in id order, and judges each allocation by id as it
// stands under the locks, in the contract's order: already invoiced
// (source_already_invoiced); then missing, released, its order no longer
// confirmed or on hold, its line no longer invoiceable, or its quantity no
// longer within what the line has open — ordered − cancelled − invoiced,
// counting the allocations before it in this issue
// (source_not_invoiceable); then its revision, currency or exact amount
// other than the source's (source_changed). The first refusal answers and
// nothing is written. Otherwise every allocation is stamped invoiced with
// ref, its quantity added to its line's invoiced, each order's invoice
// status recomputed and its updated_at moved to ref.IssuedAt — its version
// left, as a delivery marking leaves it — and orders.sales_order.invoiced.v1
// published once per order, at ref.IssuedAt (MB rule 10, as amended for a
// holder that publishes).
func (h *invoicedWorkHolder) MarkInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	ids, err := sourceAllocationIDs(sources)
	if err != nil || len(ids) == 0 {
		return err
	}
	q := store.New(tx)
	held, err := lockAllocationRows(ctx, q, ids)
	if err != nil {
		return err
	}
	sorted := slices.Clone(sources)
	slices.SortFunc(sorted, func(a, b contracts.WorkSource) int { return cmp.Compare(a.ID, b.ID) })
	invoiced := map[int64]*big.Rat{}
	for _, l := range held.lines {
		if l.OwnerKind == ownerOrder {
			invoiced[l.ID] = ratOrZero(l.QtyInvoiced)
		}
	}
	for _, source := range sorted {
		if refusal := judgeAllocation(held, invoiced, source); refusal != nil {
			return refusal
		}
		if a := held.allocations[source.ID]; a.Kind == contracts.AllocationOrderLine {
			invoiced[a.LineID] = new(big.Rat).Add(invoiced[a.LineID], ratOrZero(a.Quantity))
		}
	}
	params := store.StampAllocationsInvoicedParams{
		InvoiceID: &ref.ID, InvoiceNumber: &ref.Number, IssueDate: pgtype.Date{Time: ref.IssueDate, Valid: true},
		IssuedAt: &ref.IssuedAt, InvoicedByDisplay: textPtr(ref.IssuedByDisplay), Ids: ids,
	}
	if ref.IssuedBy != uuid.Nil {
		by := ref.IssuedBy
		params.InvoicedBy = &by
	}
	stamped, err := q.StampAllocationsInvoiced(ctx, params)
	if err != nil {
		return fmt.Errorf("orders: stamp allocations invoiced by invoice %d: %w", ref.ID, err)
	}
	if len(stamped) != len(ids) {
		return fmt.Errorf("orders: stamped %d of %d allocations invoiced by invoice %d", len(stamped), len(ids), ref.ID)
	}
	orders, err := h.writeInvoiced(ctx, q, held, invoiced, ref.IssuedAt)
	if err != nil {
		return err
	}
	if err := writeBilledThrough(ctx, q, held, ref.IssuedAt); err != nil {
		return err
	}
	for _, o := range orders {
		if err := events.Publish(ctx, tx, moduleName, contracts.SalesOrderInvoiced{
			OrderID: o.ID, Number: o.Number, CustomerID: o.CustomerID, InvoiceID: ref.ID, InvoiceNumber: ref.Number,
			InvoicedAt: ref.IssuedAt,
		}, ref.IssuedAt); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseInvoiced locks exactly as MarkInvoiced does, before it writes, and
// moves every source allocation carrying ref's stamp to released at
// ref.IssuedAt, the stamp kept as the order's history (O41); its quantity is
// taken off its line's invoiced, so it is to invoice again, and each order's
// invoice status recomputed. An allocation without ref's stamp — gone,
// released already, or stamped by another invoice — is left as it is and
// named in a warning: a credit note is never blocked. Nothing is published:
// no module consumes a release, and the customer's timeline has the credit
// note from Invoices.
func (h *invoicedWorkHolder) ReleaseInvoiced(ctx context.Context, tx pgx.Tx, ref contracts.InvoiceRef, sources []contracts.WorkSource) error {
	ids, err := sourceAllocationIDs(sources)
	if err != nil || len(ids) == 0 {
		return err
	}
	q := store.New(tx)
	held, err := lockAllocationRows(ctx, q, ids)
	if err != nil {
		return err
	}
	invoiced := map[int64]*big.Rat{}
	for _, l := range held.lines {
		if l.OwnerKind == ownerOrder {
			invoiced[l.ID] = ratOrZero(l.QtyInvoiced)
		}
	}
	release := make([]int64, 0, len(ids))
	for _, id := range ids {
		a, ok := held.allocations[id]
		if ok && a.State == contracts.AllocationInvoiced && a.InvoiceID != nil && *a.InvoiceID == ref.ID {
			if a.Kind == contracts.AllocationSubscriptionPeriod {
				// A period has no line progress: its release makes the
				// period due again (O43), and billed through is derived.
				release = append(release, id)
				continue
			}
			if _, ok := invoiced[a.LineID]; ok {
				release = append(release, id)
				left := new(big.Rat).Sub(invoiced[a.LineID], ratOrZero(a.Quantity))
				if left.Sign() < 0 {
					h.logger.WarnContext(ctx, "orders: a credit note releases more of an order line than is invoiced; its invoiced is set to 0",
						"allocation_id", id, "line_id", a.LineID, "invoice_id", ref.ID)
					left = new(big.Rat)
				}
				invoiced[a.LineID] = left
				continue
			}
		}
		var state string
		var stampedBy any
		if ok {
			state = a.State
			if a.InvoiceID != nil {
				stampedBy = *a.InvoiceID
			}
		}
		h.logger.WarnContext(ctx, "orders: a credit note releases an allocation that does not carry its invoice's stamp; left as it is",
			"allocation_id", id, "found", ok, "state", state, "invoice_id", ref.ID, "invoice_number", ref.Number,
			"stamped_by_invoice_id", stampedBy)
	}
	if len(release) == 0 {
		return nil
	}
	released, err := q.ReleaseAllocationsInvoiced(ctx, store.ReleaseAllocationsInvoicedParams{
		ReleasedAt: &ref.IssuedAt, Ids: release, InvoiceID: &ref.ID,
	})
	if err != nil {
		return fmt.Errorf("orders: release allocations invoiced by invoice %d: %w", ref.ID, err)
	}
	if len(released) != len(release) {
		return fmt.Errorf("orders: released %d of %d allocations invoiced by invoice %d", len(released), len(release), ref.ID)
	}
	if _, err = h.writeInvoiced(ctx, q, held, invoiced, ref.IssuedAt); err != nil {
		return err
	}
	return writeBilledThrough(ctx, q, held, ref.IssuedAt)
}

// heldAllocations are the rows the holder locked: the orders, the
// subscriptions, the lines and the allocations, by id.
type heldAllocations struct {
	orders        map[int64]store.OrdersOrder
	subscriptions map[int64]store.OrdersSubscription
	lines         map[int64]store.OrdersLine
	allocations   map[int64]store.OrdersInvoiceAllocation
}

// lockAllocationRows reads the allocations' orders, subscriptions and lines,
// then locks the orders, the subscriptions, the lines and the allocations,
// each in id order (O38, O43): the order every writer of an order or a
// subscription takes its row in first, so the holder waits on an edit, an
// Allocate or a release rather than crossing one.
func lockAllocationRows(ctx context.Context, q *store.Queries, ids []int64) (heldAllocations, error) {
	refs, err := q.AllocationOrders(ctx, ids)
	if err != nil {
		return heldAllocations{}, fmt.Errorf("orders: read the invoiced allocations' orders: %w", err)
	}
	orderIDs := make([]int64, 0, len(refs))
	subscriptionIDs := make([]int64, 0, len(refs))
	lineIDs := make([]int64, 0, len(refs))
	for _, r := range refs {
		if r.OrderID != nil {
			orderIDs = append(orderIDs, *r.OrderID)
		}
		if r.SubscriptionID != nil {
			subscriptionIDs = append(subscriptionIDs, *r.SubscriptionID)
		}
		lineIDs = append(lineIDs, r.LineID)
	}
	slices.Sort(orderIDs)
	slices.Sort(subscriptionIDs)
	slices.Sort(lineIDs)
	held := heldAllocations{orders: map[int64]store.OrdersOrder{}, subscriptions: map[int64]store.OrdersSubscription{},
		lines: map[int64]store.OrdersLine{}, allocations: map[int64]store.OrdersInvoiceAllocation{}}
	if len(orderIDs) > 0 {
		orders, err := q.LockOrdersByIDs(ctx, slices.Compact(orderIDs))
		if err != nil {
			return held, fmt.Errorf("orders: lock the invoiced allocations' orders: %w", err)
		}
		for _, o := range orders {
			held.orders[o.ID] = o
		}
	}
	if len(subscriptionIDs) > 0 {
		subs, err := q.LockSubscriptionsByIDs(ctx, slices.Compact(subscriptionIDs))
		if err != nil {
			return held, fmt.Errorf("orders: lock the invoiced allocations' subscriptions: %w", err)
		}
		for _, s := range subs {
			held.subscriptions[s.ID] = s
		}
	}
	lines, err := q.LockLinesByIDs(ctx, slices.Compact(lineIDs))
	if err != nil {
		return held, fmt.Errorf("orders: lock the invoiced allocations' lines: %w", err)
	}
	held.lines = byID(lines)
	allocations, err := q.LockAllocationsByIDs(ctx, ids)
	if err != nil {
		return held, fmt.Errorf("orders: lock the invoiced allocations: %w", err)
	}
	for _, a := range allocations {
		held.allocations[a.ID] = a
	}
	return held, nil
}

// judgeAllocation judges one source against its locked rows, in the
// contract's order; invoiced is each line's invoiced quantity with the
// allocations judged before this one counted.
func judgeAllocation(held heldAllocations, invoiced map[int64]*big.Rat, source contracts.WorkSource) *contracts.WorkSourceRefusal {
	refuse := func(code, detail string) *contracts.WorkSourceRefusal {
		return &contracts.WorkSourceRefusal{Source: source, Code: code, Detail: detail}
	}
	a, ok := held.allocations[source.ID]
	if !ok {
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Allocation %d was not found", source.ID))
	}
	if a.State == contracts.AllocationInvoiced {
		return refuse(contracts.SourceAlreadyInvoiced, fmt.Sprintf("Allocation %d is already invoiced", a.ID))
	}
	if a.State != contracts.AllocationDraft {
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Allocation %d is %s", a.ID, a.State))
	}
	if a.Kind == contracts.AllocationSubscriptionPeriod {
		return judgePeriodAllocation(held, a, source, refuse)
	}
	if a.OrderID == nil {
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Allocation %d bills no order", a.ID))
	}
	o, l := held.orders[*a.OrderID], held.lines[a.LineID]
	if why := notInvoiceable(o, l); why != "" {
		return refuse(contracts.SourceNotInvoiceable, why)
	}
	open := invoiceOpen(l, orZero(invoiced[l.ID]))
	if open.Cmp(ratOrZero(a.Quantity)) < 0 {
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Line %d of order %d has %s open, less than allocation %d's %s",
			l.Position, o.Number, open.FloatString(quantityPlaces), a.ID, ratOrZero(a.Quantity).FloatString(quantityPlaces)))
	}
	amount, ok := new(big.Rat).SetString(source.Amount)
	switch {
	case a.Revision != source.Revision:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Allocation %d is at revision %d, not %d", a.ID, a.Revision, source.Revision))
	case o.Currency != source.Currency:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Allocation %d bills in %q, not %q", a.ID, o.Currency, source.Currency))
	case !ok || amount.Cmp(ratOrZero(a.Amount)) != 0:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Allocation %d's amount is %s, not %s", a.ID,
			ratOrZero(a.Amount).FloatString(amountPlaces), source.Amount))
	}
	return nil
}

// judgePeriodAllocation judges a subscription period's allocation (O43): its
// subscription still bills — not a draft and not cancelled — then its
// revision, currency and exact amount. A period's quantity is no line's
// progress, so nothing is open or not.
func judgePeriodAllocation(held heldAllocations, a store.OrdersInvoiceAllocation, source contracts.WorkSource,
	refuse func(code, detail string) *contracts.WorkSourceRefusal) *contracts.WorkSourceRefusal {
	if a.SubscriptionID == nil {
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Allocation %d bills no subscription", a.ID))
	}
	sub, ok := held.subscriptions[*a.SubscriptionID]
	switch {
	case !ok:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Allocation %d's subscription was not found", a.ID))
	case sub.Status == subscriptionDraft || sub.Status == subscriptionCancelled:
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Subscription %d is %s", sub.Number, sub.Status))
	}
	if _, ok := held.lines[a.LineID]; !ok {
		return refuse(contracts.SourceNotInvoiceable, fmt.Sprintf("Allocation %d's line was not found", a.ID))
	}
	amount, ok := new(big.Rat).SetString(source.Amount)
	switch {
	case a.Revision != source.Revision:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Allocation %d is at revision %d, not %d", a.ID, a.Revision, source.Revision))
	case sub.Currency != source.Currency:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Allocation %d bills in %q, not %q", a.ID, sub.Currency, source.Currency))
	case !ok || amount.Cmp(ratOrZero(a.Amount)) != 0:
		return refuse(contracts.SourceChanged, fmt.Sprintf("Allocation %d's amount is %s, not %s", a.ID,
			ratOrZero(a.Amount).FloatString(amountPlaces), source.Amount))
	}
	return nil
}

// writeBilledThrough recomputes and stores each held subscription's billed
// through (O43) from its rows as they stand on q — after the allocations'
// stamp or release — at at.
func writeBilledThrough(ctx context.Context, q *store.Queries, held heldAllocations, at time.Time) error {
	ids := slices.Collect(maps.Keys(held.subscriptions))
	slices.Sort(ids)
	for _, id := range ids {
		if err := refreshBilledThrough(ctx, q, held.subscriptions[id], at); err != nil {
			return err
		}
	}
	return nil
}

// refreshBilledThrough derives one subscription's billed through from its
// lines, allocations and skips on q and stores it when it moved.
func refreshBilledThrough(ctx context.Context, q *store.Queries, sub store.OrdersSubscription, at time.Time) error {
	sch, err := readSchedule(ctx, q, sub)
	if err != nil {
		return err
	}
	through := sch.billedThrough()
	if equalDate(sub.BilledThrough, through) {
		return nil
	}
	if _, err := q.SetSubscriptionBilledThrough(ctx, store.SetSubscriptionBilledThroughParams{
		ID: sub.ID, BilledThrough: pgDate(through), Now: at,
	}); err != nil {
		return fmt.Errorf("orders: store subscription %d's billed through: %w", sub.ID, err)
	}
	return nil
}

// writeInvoiced stores each held line's invoiced quantity from invoiced and
// each held order's invoice status from its lines, its updated_at moved to
// at and its version left, and answers the orders in id order.
func (h *invoicedWorkHolder) writeInvoiced(ctx context.Context, q *store.Queries, held heldAllocations, invoiced map[int64]*big.Rat,
	at time.Time) ([]store.OrdersOrder, error) {
	for _, l := range held.lines {
		if l.OwnerKind != ownerOrder {
			continue
		}
		if err := q.SetLineBuckets(ctx, store.SetLineBucketsParams{
			ID: l.ID, QtyCancelled: l.QtyCancelled, QtyDelivered: l.QtyDelivered, QtyInvoiced: numeric(invoiced[l.ID], quantityPlaces),
		}); err != nil {
			return nil, fmt.Errorf("orders: store line %d's invoiced: %w", l.ID, err)
		}
	}
	ids := slices.Collect(maps.Keys(held.orders))
	slices.Sort(ids)
	out := make([]store.OrdersOrder, 0, len(ids))
	for _, id := range ids {
		o := held.orders[id]
		lines, err := linesOf(ctx, q, ownerOrder, id)
		if err != nil {
			return nil, err
		}
		// A kit's children count as invoiced what its invoice covers
		// (manufacturing phase 2 M145).
		for _, i := range syncKits(lines) {
			l := lines[i]
			if err := q.SetLineBuckets(ctx, store.SetLineBucketsParams{
				ID: l.ID, QtyCancelled: l.QtyCancelled, QtyDelivered: l.QtyDelivered, QtyInvoiced: l.QtyInvoiced,
			}); err != nil {
				return nil, fmt.Errorf("orders: store kit line %d's progress: %w", l.ID, err)
			}
		}
		delivery, invoice := progressOf(lines)
		row, err := q.SetOrderProgress(ctx, store.SetOrderProgressParams{
			ID: id, DeliveryStatus: delivery, InvoiceStatus: invoice, Version: o.Version, Now: at,
		})
		if err != nil {
			return nil, fmt.Errorf("orders: store order %d's progress: %w", id, err)
		}
		out = append(out, row)
	}
	return out, nil
}

// sourceAllocationIDs is the sources' allocation ids ascending. A source of
// another kind, or one named twice, is the caller's bug, never a refusal.
func sourceAllocationIDs(sources []contracts.WorkSource) ([]int64, error) {
	ids := make([]int64, 0, len(sources))
	seen := make(map[int64]bool, len(sources))
	for _, s := range sources {
		if s.Kind != contracts.WorkSourceOrderAllocation {
			return nil, fmt.Errorf("orders: the invoiced-work holder was handed a %q source; it holds %q only", s.Kind, contracts.WorkSourceOrderAllocation)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("orders: the invoiced-work holder was handed allocation %d twice", s.ID)
		}
		seen[s.ID] = true
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	return ids, nil
}
