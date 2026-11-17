package purchasing

// Accounting's supplier-invoice events (purchasing design P5, P37, P39,
// A15, A22; module-boundaries rule 23). Each handler runs on the delivery's
// own transaction, writes only this module's schema, takes the event once by
// its id, and reads no contract:
//
//   - purchasing.invoice-posted, on accounting.supplier_invoice.posted,
//     fixes the invoice's provisional matches, releases those whose order is
//     no longer the invoice's supplier's (§11), and queues the invoice for the
//     purchasing.invoice-posted worker, which reads the posted lines through
//     contracts.SupplierInvoices on the pool;
//   - purchasing.invoice-reversed, on accounting.supplier_invoice.reversed,
//     releases the invoice's matches and gives the copy Reverse and copy made
//     the same matches, provisional — posted when the copy's own posting was
//     taken first, following the copy's own copy when it was reversed first;
//   - purchasing.invoice-deleted, on accounting.supplier_invoice.deleted,
//     releases a deleted draft's matches (A79).
//
// What Purchasing knows of the invoice is its row in invoice_states, taken
// FOR UPDATE first: a reversal wins over a posted event delivered after it.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// The handlers' names.
const (
	invoicePostedHandler   = "purchasing.invoice-posted"
	invoiceReversedHandler = "purchasing.invoice-reversed"
	invoiceDeletedHandler  = "purchasing.invoice-deleted"
)

// eventHandlers are the module's handlers (module.Module.EventHandlers).
// They run whatever MODULES says: a match is Purchasing's own row, and an
// invoice posted while Purchasing is off still fixes it.
func eventHandlers(d module.Deps) []events.Handler {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	return []events.Handler{
		{
			Name:   invoicePostedHandler,
			Events: []events.EventRef{{Name: contracts.SupplierInvoicePosted{}.EventName(), Version: 1}},
			Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
				return handleInvoicePosted(ctx, tx, ev, clock())
			},
		},
		{
			Name:   invoiceReversedHandler,
			Events: []events.EventRef{{Name: contracts.SupplierInvoiceReversed{}.EventName(), Version: 1}},
			Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
				return handleInvoiceReversed(ctx, tx, ev, clock())
			},
		},
		{
			Name:   invoiceDeletedHandler,
			Events: []events.EventRef{{Name: contracts.SupplierInvoiceDeleted{}.EventName(), Version: 1}},
			Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
				return handleInvoiceDeleted(ctx, tx, ev, clock())
			},
		},
	}
}

// firstDelivery records the event by its id and reports whether this is the
// first time it is taken.
func firstDelivery(ctx context.Context, q *store.Queries, ev events.Delivered, invoiceID int64, now time.Time) (bool, error) {
	n, err := q.InsertInvoiceEvent(ctx, store.InsertInvoiceEventParams{EventID: ev.ID, Name: ev.Name, SupplierInvoiceID: invoiceID, Now: now})
	if err != nil {
		return false, fmt.Errorf("purchasing: record event %d: %w", ev.ID, err)
	}
	return n == 1, nil
}

// handleInvoicePosted fixes the invoice's matches (P37).
func handleInvoicePosted(ctx context.Context, tx pgx.Tx, ev events.Delivered, now time.Time) error {
	var e contracts.SupplierInvoicePosted
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return fmt.Errorf("purchasing: decode event %d: %w", ev.ID, err)
	}
	q := store.New(tx)
	if first, err := firstDelivery(ctx, q, ev, e.InvoiceID, now); err != nil || !first {
		return err
	}
	st, err := lockInvoice(ctx, q, e.InvoiceID, now)
	if err != nil {
		return err
	}
	if st.State != invoiceStateOpen {
		// Posted already — by a match that read it posted (A80) —, or
		// reversed: a reversal delivered first wins.
		return nil
	}
	date, err := time.Parse(time.DateOnly, e.InvoiceDate)
	if err != nil {
		return fmt.Errorf("purchasing: read event %d's invoice date: %w", ev.ID, err)
	}
	return markPosted(ctx, q, postedFacts{id: e.InvoiceID, supplierID: e.SupplierID, kind: e.Kind, number: e.Number,
		date: date, currency: e.Currency}, now)
}

// postedFacts are what a posting tells of an invoice: its event's, or
// Accounting's read of an invoice posted before its event was taken (A80).
type postedFacts struct {
	id, supplierID         int64
	kind, number, currency string
	date                   time.Time
}

// markPosted moves an open invoice, already locked, to posted: it fixes the
// provisional matches, releases those whose order is no longer the
// invoice's supplier's (§11) or that were made while the draft was of the
// other kind, and queues the invoice for the worker.
func markPosted(ctx context.Context, q *store.Queries, f postedFacts, now time.Time) error {
	if err := q.SetInvoiceState(ctx, store.SetInvoiceStateParams{ID: f.id, State: invoiceStatePost, Now: now}); err != nil {
		return fmt.Errorf("purchasing: mark invoice %d posted: %w", f.id, err)
	}
	matches, err := q.InvoiceMatches(ctx, f.id)
	if err != nil || len(matches) == 0 {
		return err
	}
	orders, err := lockOrdersOf(ctx, q, matches)
	if err != nil {
		return err
	}
	var gone []store.PurchasingInvoiceMatch
	for _, m := range matches {
		if orders[m.OrderID].SupplierID != f.supplierID || m.Kind != f.kind {
			gone = append(gone, m)
		}
	}
	if gone, err = releasable(ctx, q, gone); err != nil {
		return err
	}
	if err := releaseMatches(ctx, q, gone); err != nil {
		return err
	}
	if err := q.PostInvoiceMatches(ctx, store.PostInvoiceMatchesParams{ID: f.id, Now: &now, Currency: f.currency,
		InvoiceDate: dayCol(f.date), InvoiceNumber: f.number}); err != nil {
		return fmt.Errorf("purchasing: fix invoice %d's matches: %w", f.id, err)
	}
	return q.QueueFollowup(ctx, store.QueueFollowupParams{ID: f.id, Now: now})
}

// handleInvoiceDeleted releases a deleted draft's matches (A79): what they
// counted as invoiced is to invoice again. A match a credit note offsets
// stays, with its warnings, for a person to unmatch from the order.
func handleInvoiceDeleted(ctx context.Context, tx pgx.Tx, ev events.Delivered, now time.Time) error {
	var e contracts.SupplierInvoiceDeleted
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return fmt.Errorf("purchasing: decode event %d: %w", ev.ID, err)
	}
	q := store.New(tx)
	if first, err := firstDelivery(ctx, q, ev, e.InvoiceID, now); err != nil || !first {
		return err
	}
	st, err := lockInvoice(ctx, q, e.InvoiceID, now)
	if err != nil || st.State == invoiceStateDeleted {
		return err
	}
	if err := q.SetInvoiceState(ctx, store.SetInvoiceStateParams{ID: e.InvoiceID, State: invoiceStateDeleted, Now: now}); err != nil {
		return fmt.Errorf("purchasing: mark invoice %d deleted: %w", e.InvoiceID, err)
	}
	matches, err := q.InvoiceMatches(ctx, e.InvoiceID)
	if err != nil || len(matches) == 0 {
		return err
	}
	if _, err := lockOrdersOf(ctx, q, matches); err != nil {
		return err
	}
	kept, err := releasable(ctx, q, matches)
	if err != nil {
		return err
	}
	return releaseMatches(ctx, q, kept)
}

// maxCopyHops bounds the copies a reversal follows: a copy reversed in its
// turn before its original's reversal is taken hands the matches on to its
// own copy.
const maxCopyHops = 50

// handleInvoiceReversed releases the invoice's matches and gives them to the
// copy (A15).
func handleInvoiceReversed(ctx context.Context, tx pgx.Tx, ev events.Delivered, now time.Time) error {
	var e contracts.SupplierInvoiceReversed
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return fmt.Errorf("purchasing: decode event %d: %w", ev.ID, err)
	}
	q := store.New(tx)
	if first, err := firstDelivery(ctx, q, ev, e.InvoiceID, now); err != nil || !first {
		return err
	}
	st, err := lockInvoice(ctx, q, e.InvoiceID, now)
	if err != nil {
		return err
	}
	if st.State == invoiceStateRev {
		return nil
	}
	var copyID *int64
	if e.CopyID > 0 {
		copyID = &e.CopyID
	}
	if err := q.SetInvoiceState(ctx, store.SetInvoiceStateParams{ID: e.InvoiceID, State: invoiceStateRev, CopyID: copyID, Now: now}); err != nil {
		return fmt.Errorf("purchasing: mark invoice %d reversed: %w", e.InvoiceID, err)
	}
	matches, err := q.InvoiceMatches(ctx, e.InvoiceID)
	if err != nil || len(matches) == 0 {
		return err
	}
	// The copy that takes the matches: the one the reversal made, or — when
	// that copy was reversed in its turn first, events of different
	// invoices being taken in any order — the copy made of it, and so on
	// (A81). A deleted copy, or a chain that ends reversed, takes none.
	var copySt store.PurchasingInvoiceState
	for hops := 0; copyID != nil; hops++ {
		if copySt, err = lockInvoice(ctx, q, *copyID, now); err != nil {
			return err
		}
		if copySt.State != invoiceStateRev || copySt.CopyID == nil || hops == maxCopyHops {
			break
		}
		copyID = copySt.CopyID
	}
	if copyID != nil && (copySt.State == invoiceStateRev || copySt.State == invoiceStateDeleted) {
		copyID = nil
	}
	if _, err := lockOrdersOf(ctx, q, matches); err != nil {
		return err
	}
	// The matches the copy takes move to it as they are, so what is
	// invoiced stays; the rest are released.
	at := map[int32]bool{}
	if copyID != nil {
		taken, err := q.InvoiceMatches(ctx, *copyID)
		if err != nil {
			return fmt.Errorf("purchasing: read invoice %d's matches: %w", *copyID, err)
		}
		for _, m := range taken {
			at[m.LinePosition] = true
		}
	}
	var moved, released []store.PurchasingInvoiceMatch
	for _, m := range matches {
		if copyID != nil && !at[m.LinePosition] {
			moved = append(moved, m)
		} else {
			released = append(released, m)
		}
	}
	kept, err := releasable(ctx, q, released)
	if err != nil {
		return err
	}
	if err := releaseMatches(ctx, q, kept); err != nil {
		return err
	}
	posted := copySt.State == invoiceStatePost
	for _, m := range moved {
		if err := q.DeleteMatch(ctx, m.ID); err != nil {
			return fmt.Errorf("purchasing: move match %d to the copy: %w", m.ID, err)
		}
		p := store.InsertMatchParams{SupplierInvoiceID: *copyID, LinePosition: m.LinePosition, OrderID: m.OrderID,
			OrderLineID: m.OrderLineID, Kind: m.Kind, Quantity: m.Quantity, LineNet: m.LineNet, InvoiceNumber: m.InvoiceNumber,
			InvoiceDate: m.InvoiceDate, InvoiceCurrency: m.InvoiceCurrency, State: matchProvisional,
			MatchedByUserID: m.MatchedByUserID, MatchedByDisplay: m.MatchedByDisplay, Now: now}
		if posted {
			p.State, p.PostedAt = matchPosted, &now
		}
		if _, err := q.InsertMatch(ctx, p); err != nil {
			return fmt.Errorf("purchasing: give invoice %d's match to its copy: %w", e.InvoiceID, err)
		}
	}
	if posted && len(moved) > 0 {
		return q.QueueFollowup(ctx, store.QueueFollowupParams{ID: *copyID, Now: now})
	}
	return nil
}

// The posted invoices' worker.
const (
	followupWorkerName     = "purchasing.invoice-posted"
	followupWorkerInterval = 30 * time.Second
	followupPerCycle       = 20
	followupLease          = 2 * time.Minute
	followupMaxAttempts    = 8
)

// followupWorker finishes what a posted invoice changes once its lines can
// be read (P39, A9): each match's net from the posted line, a match whose
// line is gone released, and the last prices.
type followupWorker struct{ s *server }

func (w *followupWorker) Name() string            { return followupWorkerName }
func (w *followupWorker) Interval() time.Duration { return followupWorkerInterval }

func (w *followupWorker) Run(ctx context.Context) error {
	return runEvery(ctx, followupWorkerInterval, w.cycle)
}

// cycle finishes up to followupPerCycle due invoices and answers how many it
// claimed.
func (w *followupWorker) cycle(ctx context.Context) int {
	s := w.s
	q := store.New(s.deps.Pool)
	n := 0
	for ; n < followupPerCycle && ctx.Err() == nil; n++ {
		now := s.deps.Clock()
		row, err := q.ClaimFollowup(ctx, store.ClaimFollowupParams{LeaseUntil: now.Add(followupLease), Now: now, MaxAttempts: followupMaxAttempts})
		if noRows(err) {
			return n
		}
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "purchasing: the posted invoices' worker could not claim", "error", err.Error())
			return n
		}
		if err := s.followup(ctx, row.SupplierInvoiceID); err != nil {
			if row.Attempts >= followupMaxAttempts {
				// The last attempt: the worker gives up on the invoice, so its
				// matches keep the draft's nets and no last price is set.
				s.deps.Logger.ErrorContext(ctx, "purchasing: a posted invoice could not be finished and is given up", "supplier_invoice_id",
					row.SupplierInvoiceID, "attempts", row.Attempts, "error", err.Error())
			} else {
				s.deps.Logger.WarnContext(ctx, "purchasing: a posted invoice could not be finished", "supplier_invoice_id", row.SupplierInvoiceID, "error", err.Error())
			}
			if ferr := q.FailFollowup(context.WithoutCancel(ctx), store.FailFollowupParams{ID: row.SupplierInvoiceID,
				LastError: ptr(truncate(err.Error(), 500)), NextAttemptAt: now.Add(mailBackoff(row.Attempts))}); ferr != nil {
				s.deps.Logger.ErrorContext(ctx, "purchasing: a posted invoice's failure could not be recorded", "error", ferr.Error())
			}
			continue
		}
		if err := q.CompleteFollowup(context.WithoutCancel(ctx), store.CompleteFollowupParams{ID: row.SupplierInvoiceID, Now: &now,
			RequestedAt: row.RequestedAt}); err != nil {
			s.deps.Logger.ErrorContext(ctx, "purchasing: a posted invoice could not be marked finished", "error", err.Error())
		}
	}
	return n
}

// followup reads the posted invoice and the catalog on the pool, then writes
// the matches' facts and the last prices in one transaction.
func (s *server) followup(ctx context.Context, invoiceID int64) error {
	inv, err := s.supplierInvoice(ctx, invoiceID)
	if err != nil {
		return err
	}
	if inv == nil || inv.State != accountingPosted {
		return nil
	}
	matches, err := s.store().InvoiceMatches(ctx, invoiceID)
	if err != nil || len(matches) == 0 {
		return err
	}
	lineIDs := map[int64]bool{}
	for _, m := range matches {
		lineIDs[m.OrderLineID] = true
	}
	var variantIDs []int32
	lines, err := s.store().OrderLinesOf(ctx, distinctOrders(matches))
	if err != nil {
		return fmt.Errorf("purchasing: read the matched lines: %w", err)
	}
	for _, l := range lines {
		if lineIDs[l.ID] && l.VariantID != nil {
			variantIDs = append(variantIDs, *l.VariantID)
		}
	}
	variants, err := s.catalogVariants(ctx, variantIDs)
	if err != nil {
		return err
	}
	return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		now := s.deps.Clock()
		st, err := lockInvoice(ctx, txq, invoiceID, now)
		if err != nil || st.State == invoiceStateRev {
			return err
		}
		matches, err := txq.InvoiceMatches(ctx, invoiceID)
		if err != nil || len(matches) == 0 {
			return err
		}
		orders, err := lockOrdersOf(ctx, txq, matches)
		if err != nil {
			return err
		}
		current, err := txq.OrderLinesOf(ctx, distinctOrders(matches))
		if err != nil {
			return fmt.Errorf("purchasing: read the matched lines: %w", err)
		}
		byID := map[int64]store.PurchasingOrderLine{}
		for _, l := range current {
			byID[l.ID] = l
		}
		var gone, kept []store.PurchasingInvoiceMatch
		for _, m := range matches {
			net, ok := lineNetAt(inv, m.LinePosition)
			if !ok {
				gone = append(gone, m)
				continue
			}
			if err := txq.RefreshMatchFacts(ctx, store.RefreshMatchFactsParams{ID: m.ID, LineNet: numeric(net, amountPlaces),
				InvoiceNumber: inv.Number, InvoiceDate: dayCol(inv.InvoiceDate), Currency: inv.Currency, Now: now}); err != nil {
				return fmt.Errorf("purchasing: refresh match %d: %w", m.ID, err)
			}
			m.LineNet = numeric(net, amountPlaces)
			kept = append(kept, m)
		}
		if gone, err = releasable(ctx, txq, gone); err != nil {
			return err
		}
		if err := releaseMatches(ctx, txq, gone); err != nil {
			return err
		}
		if inv.Kind != invoiceKind {
			return nil
		}
		return setLastPrices(ctx, txq, inv, kept, orders, byID, variants, now)
	})
}

// distinctOrders is the matches' orders, in id order.
func distinctOrders(matches []store.PurchasingInvoiceMatch) []int64 {
	ids := make([]int64, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.OrderID)
	}
	return distinctSorted(ids)
}

// setLastPrices makes each matched variant line's unit price — the posted
// line's net over the matched quantity, in the supplier item's packaging —
// the supplier item's price, set by the invoice on its date (P39, A9),
// unless the invoice is in another currency than the item's, or the price
// was set after the invoice's date — by a person, an import or a later
// invoice. Of two lines of one variant the
// later on the invoice wins.
func setLastPrices(ctx context.Context, txq *store.Queries, inv *contracts.SupplierInvoice, matches []store.PurchasingInvoiceMatch,
	orders map[int64]store.PurchasingOrder, lines map[int64]store.PurchasingOrderLine, variants map[int32]contracts.VariantEntry, now time.Time) error {
	unit := map[int32]*big.Rat{}
	var ids []int32
	for _, m := range matches {
		l := lines[m.OrderLineID]
		q := ratOrZero(m.Quantity)
		if l.VariantID == nil || q.Sign() <= 0 || orders[m.OrderID].SupplierID != inv.SupplierID {
			continue
		}
		if _, ok := unit[*l.VariantID]; !ok {
			ids = append(ids, *l.VariantID)
		}
		unit[*l.VariantID] = new(big.Rat).Quo(ratOrZero(m.LineNet), q)
	}
	if len(ids) == 0 {
		return nil
	}
	items, err := txq.SupplierItemsFor(ctx, store.SupplierItemsForParams{SupplierID: inv.SupplierID, VariantIds: ids})
	if err != nil {
		return fmt.Errorf("purchasing: lock the supplier items: %w", err)
	}
	invoiceDay := inv.InvoiceDate
	// The posting, as Purchasing took it: the earliest of the matches'.
	var postedAt *time.Time
	for _, m := range matches {
		if m.PostedAt != nil && (postedAt == nil || m.PostedAt.Before(*postedAt)) {
			postedAt = m.PostedAt
		}
	}
	for _, it := range items {
		if it.Currency != inv.Currency {
			continue
		}
		// A later price stands; so does one a person or an import set on the
		// invoice's own day after the invoice was posted, which a second run of
		// the worker would otherwise take back (A85). One set that day before
		// the posting, or by an invoice, is replaced.
		if it.PriceSetOn.Valid && (it.PriceSetOn.Time.After(invoiceDay) ||
			(it.PriceSetOn.Time.Format(time.DateOnly) == invoiceDay.Format(time.DateOnly) &&
				(it.PriceSetBy == nil || *it.PriceSetBy != "invoice") && postedAt != nil && it.UpdatedAt.After(*postedAt))) {
			continue
		}
		factor := big.NewRat(1, 1)
		if it.PackagingID != nil {
			v, ok := variants[it.VariantID]
			if !ok {
				continue
			}
			f, ok := packagingFactor(v, it.PackagingID)
			if !ok {
				continue
			}
			factor = f
		}
		price := roundTo(new(big.Rat).Mul(unit[it.VariantID], factor), pricePlaces)
		if price.Cmp(maxPrice) > 0 {
			continue
		}
		if err := txq.SetInvoicePrice(ctx, store.SetInvoicePriceParams{ID: it.ID, Price: numeric(price, pricePlaces),
			SetOn: dayCol(invoiceDay), Now: now}); err != nil {
			return fmt.Errorf("purchasing: set supplier item %d's price: %w", it.ID, err)
		}
	}
	return nil
}
