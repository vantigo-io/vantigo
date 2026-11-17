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
//     taken first.
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
	if st.State != "open" {
		// Posted already, or reversed: a reversal delivered first wins.
		return nil
	}
	if err := q.SetInvoiceState(ctx, store.SetInvoiceStateParams{ID: e.InvoiceID, State: invoiceStatePost, Now: now}); err != nil {
		return fmt.Errorf("purchasing: mark invoice %d posted: %w", e.InvoiceID, err)
	}
	matches, err := q.InvoiceMatches(ctx, e.InvoiceID)
	if err != nil || len(matches) == 0 {
		return err
	}
	orders, err := lockOrdersOf(ctx, q, matches)
	if err != nil {
		return err
	}
	var gone []store.PurchasingInvoiceMatch
	for _, m := range matches {
		if orders[m.OrderID].SupplierID != e.SupplierID {
			gone = append(gone, m)
		}
	}
	if err := releaseMatches(ctx, q, gone); err != nil {
		return err
	}
	date, err := time.Parse(time.DateOnly, e.InvoiceDate)
	if err != nil {
		return fmt.Errorf("purchasing: read event %d's invoice date: %w", ev.ID, err)
	}
	if err := q.PostInvoiceMatches(ctx, store.PostInvoiceMatchesParams{ID: e.InvoiceID, Now: &now, Currency: e.Currency,
		InvoiceDate: dayCol(date), InvoiceNumber: e.Number}); err != nil {
		return fmt.Errorf("purchasing: fix invoice %d's matches: %w", e.InvoiceID, err)
	}
	return q.QueueFollowup(ctx, store.QueueFollowupParams{ID: e.InvoiceID, Now: now})
}

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
	var copySt store.PurchasingInvoiceState
	if copyID != nil {
		if copySt, err = lockInvoice(ctx, q, e.CopyID, now); err != nil {
			return err
		}
	}
	if _, err := lockOrdersOf(ctx, q, matches); err != nil {
		return err
	}
	if err := releaseMatches(ctx, q, matches); err != nil {
		return err
	}
	if copyID == nil || copySt.State == invoiceStateRev {
		return nil
	}
	taken, err := q.InvoiceMatches(ctx, e.CopyID)
	if err != nil {
		return fmt.Errorf("purchasing: read invoice %d's matches: %w", e.CopyID, err)
	}
	at := map[int32]bool{}
	for _, m := range taken {
		at[m.LinePosition] = true
	}
	posted := copySt.State == invoiceStatePost
	for _, m := range matches {
		if at[m.LinePosition] {
			continue
		}
		p := store.InsertMatchParams{SupplierInvoiceID: e.CopyID, LinePosition: m.LinePosition, OrderID: m.OrderID,
			OrderLineID: m.OrderLineID, Kind: m.Kind, Quantity: m.Quantity, LineNet: m.LineNet, InvoiceNumber: m.InvoiceNumber,
			InvoiceDate: m.InvoiceDate, InvoiceCurrency: m.InvoiceCurrency, State: matchProvisional,
			MatchedByUserID: m.MatchedByUserID, MatchedByDisplay: m.MatchedByDisplay, Now: now}
		if posted {
			p.State, p.PostedAt = matchPosted, &now
		}
		if _, err := q.InsertMatch(ctx, p); err != nil {
			return fmt.Errorf("purchasing: give invoice %d's match to its copy: %w", e.InvoiceID, err)
		}
		if err := q.AddInvoiced(ctx, store.AddInvoicedParams{ID: m.OrderLineID,
			Delta: numeric(signed(m.Kind, ratOrZero(m.Quantity)), quantityPlaces)}); err != nil {
			return fmt.Errorf("purchasing: move line %d's invoiced: %w", m.OrderLineID, err)
		}
	}
	if posted {
		return q.QueueFollowup(ctx, store.QueueFollowupParams{ID: e.CopyID, Now: now})
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
			s.deps.Logger.WarnContext(ctx, "purchasing: a posted invoice could not be finished", "supplier_invoice_id", row.SupplierInvoiceID, "error", err.Error())
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
				InvoiceNumber: inv.Number, InvoiceDate: dayCol(inv.InvoiceDate), Currency: inv.Currency, Kind: inv.Kind, Now: now}); err != nil {
				return fmt.Errorf("purchasing: refresh match %d: %w", m.ID, err)
			}
			m.LineNet = numeric(net, amountPlaces)
			kept = append(kept, m)
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
	for _, it := range items {
		if it.Currency != inv.Currency {
			continue
		}
		if it.PriceSetOn.Valid && it.PriceSetOn.Time.After(invoiceDay) {
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
