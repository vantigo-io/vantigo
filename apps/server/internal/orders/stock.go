package orders

// Stock (orders design O45, O46, O48, module-boundaries rule 24): an order
// names the Inventory site it is fulfilled from, and its goods lines reserve
// their open quantity there through contracts.StockLedger, on the order's own
// transaction.
//
// A goods line at a site is a product line whose variant the catalog calls
// Goods, on an order with a site, while Inventory keeps stock (Deps.Stock):
// its delivery method is delivery (O47) and its invoice policy defaults to
// delivery (O48). Every other line is delivered by hand (O32) and invoiced on
// order unless the user chose otherwise.
//
// What a line holds reserved is Inventory's to say (ReservationsFor); Orders
// keeps no copy. The rules:
//
//   - Confirm reserves every goods line's open quantity — ordered less
//     cancelled less delivered — unless the user confirms without reserving.
//   - Reserve now reserves what of each goods line's open quantity is not
//     reserved yet.
//   - An edit of a confirmed order that holds reservations reserves what a
//     goods line's open quantity grew by, a new goods line's whole, and
//     releases what a line holds beyond its open quantity; a line whose item
//     changed releases all and reserves its whole open quantity of the new
//     variant; an order holding none reserves nothing on an edit.
//   - A removed line and a cancel release all; a cancel of what remains
//     releases what each line holds beyond what is still open.
//   - A manual marking releases as much as it marked; its undo reserves
//     nothing again (Reserve now does).
//   - A delivery's post issues against the reservations, which Inventory
//     consumes.
//
// Releasing never depends on Deps.Stock: the ledger is composed whatever
// MODULES says, so a reservation made while Inventory was on is released
// after it is switched off. Reserving and issuing do: with Inventory off, an
// order reserves nothing and its lines are delivered by hand.

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The invoice policies (O48) beside policyOnOrder, and the delivery methods
// (O32, O47).
const (
	policyOnDelivery = "delivery"
	methodManual     = "manual"
	methodDelivery   = "delivery"
)

// The stock refusal codes Orders answers beside Inventory's own.
const (
	codeOrderHasNoSite     = "order_has_no_site"
	codeOrderHasDeliveries = "order_has_deliveries"
	codeSiteNotFulfilling  = contracts.StockSiteNotFulfilling
)

// actionConfirmedWithoutReserving is a confirm the user made without
// reserving (O46); actionSiteChanged a change of the order's site, from and
// to its id.
const (
	actionConfirmedWithoutReserving = "confirmed_without_reserving"
	actionSiteChanged               = "site_changed"
	actionReserved                  = "reserved"
)

// orderStock is an order as the ledger names it.
func orderStock(id int64) contracts.StockSource {
	return contracts.StockSource{Module: moduleName, Kind: ownerOrder, ID: id}
}

// stockKept reports whether Inventory keeps stock: orders then reserve and
// are delivered by delivery documents.
func (s *server) stockKept() bool { return s.deps.Stock != nil }

// atSite reports whether an order's goods lines are kept at a site.
func (s *server) atSite(site *int64) bool { return site != nil && s.stockKept() }

// isGoods reports whether a variant locked on this transaction is goods, and
// whether the catalog answered it at all.
func isGoods(ctx context.Context, variantID *int32) (goods, known bool) {
	if variantID == nil {
		return false, true
	}
	rec := lockedVariantsOf(ctx)
	if rec == nil {
		return false, false
	}
	e, ok := rec.byID[*variantID]
	if !ok {
		return false, false
	}
	return e.ProductType == "Goods", true
}

// settlePolicies settles each line's delivery method and invoice policy
// before it is written (O48), on the transaction that locked the lines'
// variants: a goods line at a site is delivered by delivery documents and
// invoiced on delivery by default; a kept line keeps its policy unless the
// request names one or redefault is set (a draft's site changed); a line
// whose variant the catalog no longer answers keeps what it had.
func settlePolicies(ctx context.Context, lines []pricedLine, old map[int64]store.OrdersLine, atSite, redefault bool) {
	for i := range lines {
		l := &lines[i]
		if l.kind != kindProduct {
			l.deliveryMethod, l.invoicePolicy = methodManual, policyOnOrder
			continue
		}
		var was *store.OrdersLine
		if l.id != nil {
			if o, ok := old[*l.id]; ok {
				was = &o
			}
		}
		goods, known := isGoods(ctx, l.variantID)
		if !known && was != nil {
			goods = was.DeliveryMethod == methodDelivery
		}
		method, policy := methodManual, policyOnOrder
		if goods && atSite {
			method, policy = methodDelivery, policyOnDelivery
		}
		switch {
		case l.requestedPolicy != nil:
			policy = *l.requestedPolicy
		case was != nil && !redefault:
			policy = was.InvoicePolicy
		}
		l.deliveryMethod, l.invoicePolicy = method, policy
	}
}

// judgePolicies refuses a confirmed order's save that changes the invoice
// policy of a line with anything invoiced or in a draft invoice
// (line_invoiced, O48).
func judgePolicies(lines []pricedLine, current []store.OrdersLine, allocs lineAllocations) error {
	old := byID(current)
	for _, l := range lines {
		if l.id == nil {
			continue
		}
		o, ok := old[*l.id]
		if !ok || o.InvoicePolicy == l.invoicePolicy {
			continue
		}
		if ratOrZero(o.QtyInvoiced).Sign() > 0 || allocs.drafted(o.ID).Sign() > 0 {
			return conflict(codeLineInvoiced, "The line is invoiced",
				fmt.Sprintf("Something of line %d is invoiced or in a draft invoice; its invoice policy no longer changes.", o.Position))
		}
	}
	return nil
}

// openToDeliver is what remains of a line to deliver: ordered less cancelled
// less delivered, never below 0.
func openToDeliver(l store.OrdersLine) *big.Rat {
	return openQuantity(ratOrZero(l.Quantity), ratOrZero(l.QtyCancelled), ratOrZero(l.QtyDelivered))
}

// reservable reports whether a stored line reserves stock: a product line
// with a variant, delivered by delivery documents, that is not a kit line —
// kits names the order's kit lines (kitsOf), whose children reserve instead
// (manufacturing phase 2 M145).
func reservable(l store.OrdersLine, kits map[int64]bool) bool {
	return l.Kind == kindProduct && l.VariantID != nil && l.Quantity.Valid && l.DeliveryMethod == methodDelivery && !kits[l.ID]
}

// reservedByLine is what the order holds reserved, per line, summed over the
// sites it is reserved at, read on tx. Without a ledger it holds nothing.
func (s *server) reservedByLine(ctx context.Context, tx pgx.Tx, orderID int64) (map[int64]*big.Rat, error) {
	out := map[int64]*big.Rat{}
	if s.deps.StockLedger == nil {
		return out, nil
	}
	rows, err := s.deps.StockLedger.ReservationsFor(ctx, tx, orderStock(orderID))
	if err != nil {
		return nil, fmt.Errorf("orders: read order %d's reservations: %w", orderID, err)
	}
	for _, r := range rows {
		q, ok := new(big.Rat).SetString(r.Quantity)
		if !ok {
			return nil, fmt.Errorf("orders: order %d's reservation of line %d is not a number: %q", orderID, r.LineID, r.Quantity)
		}
		if cur := out[r.LineID]; cur != nil {
			q.Add(q, cur)
		}
		out[r.LineID] = q
	}
	return out, nil
}

// holdsReservations reports whether any line of the order holds stock.
func holdsReservations(reserved map[int64]*big.Rat) bool {
	for _, q := range reserved {
		if q.Sign() > 0 {
			return true
		}
	}
	return false
}

// stockText is a quantity as the ledger takes it: exact, at most three
// decimals, no trailing zeros.
func stockText(v *big.Rat) string {
	return trimZeros(v.FloatString(quantityPlaces))
}

// trimZeros drops a decimal's trailing zeros and a bare point.
func trimZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// permInventoryExpiredOverride is Inventory's key that lets a person take an
// expired lot (manufacturing phase 2 M62, M68): Orders reads it on the pool
// before a delivery's transaction and passes it to the ledger as data.
const permInventoryExpiredOverride = "inventory:expired-override"

// permInventoryLocationsView and permInventoryStockView are Inventory's two
// views, which its lot page and trace need (manufacturing phase 2 M94): a
// delivery's lots link there only for a caller holding both.
const (
	permInventoryLocationsView = "inventory:locations-view"
	permInventoryStockView     = "inventory:stock-view"
)

// reserveLines reserves want of each line at the order's site, on tx: lines
// with nothing wanted are skipped, and a refusal names the order's lines by
// their index in lines (stockRefused). today is the business day, read on
// the pool before the transaction: the day the ledger judges a lot's expiry
// on (manufacturing phase 2 M63), never read inside it (rule 24).
func (s *server) reserveLines(ctx context.Context, tx pgx.Tx, o store.OrdersOrder, lines []store.OrdersLine, want map[int64]*big.Rat, today time.Time, title string) error {
	if !s.atSite(o.SiteID) || s.deps.StockLedger == nil {
		return nil
	}
	var reserve []contracts.StockLine
	kits := kitsOf(lines)
	for _, l := range lines {
		q := want[l.ID]
		if q == nil || q.Sign() <= 0 || !reservable(l, kits) {
			continue
		}
		reserve = append(reserve, contracts.StockLine{VariantID: *l.VariantID, LineID: l.ID, Quantity: stockText(q)})
	}
	if len(reserve) == 0 {
		return nil
	}
	if err := s.deps.StockLedger.Reserve(ctx, tx, orderStock(o.ID), *o.SiteID, today, reserve); err != nil {
		return stockRefused(err, lines, title)
	}
	return nil
}

// releaseLines gives back the named reservations of the order on tx; none
// named releases nothing (releaseAll releases everything).
func (s *server) releaseLines(ctx context.Context, tx pgx.Tx, orderID int64, releases map[int64]*big.Rat) error {
	if s.deps.StockLedger == nil || len(releases) == 0 {
		return nil
	}
	var out []contracts.StockRelease
	for id, q := range releases {
		if q == nil {
			out = append(out, contracts.StockRelease{LineID: id})
			continue
		}
		if q.Sign() > 0 {
			out = append(out, contracts.StockRelease{LineID: id, Quantity: stockText(q)})
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.SortFunc(out, func(a, b contracts.StockRelease) int { return int(a.LineID - b.LineID) })
	if err := s.deps.StockLedger.Release(ctx, tx, orderStock(orderID), out); err != nil {
		return fmt.Errorf("orders: release order %d's reservations: %w", orderID, err)
	}
	return nil
}

// releaseAll gives back every reservation of the order on tx.
func (s *server) releaseAll(ctx context.Context, tx pgx.Tx, orderID int64) error {
	if s.deps.StockLedger == nil {
		return nil
	}
	if err := s.deps.StockLedger.Release(ctx, tx, orderStock(orderID), nil); err != nil {
		return fmt.Errorf("orders: release order %d's reservations: %w", orderID, err)
	}
	return nil
}

// stockRefused turns the ledger's refusal into the order's 409, naming every
// failing line by its index in lines; anything else is the failure it is.
func stockRefused(err error, lines []store.OrdersLine, title string) error {
	var r *contracts.StockRefusal
	if !errors.As(err, &r) {
		return fmt.Errorf("orders: the stock ledger failed: %w", err)
	}
	index := map[int64]int{}
	for i, l := range lines {
		index[l.ID] = i
	}
	failures := make([]lineFailure, 0, len(r.Lines))
	for _, fl := range r.Lines {
		i, ok := index[fl.LineID]
		if !ok {
			continue
		}
		failures = append(failures, lineFailure{index: i, code: fl.Code, detail: fl.Detail})
	}
	p := linesProblem(title, failures)
	status, code := int32(http.StatusConflict), r.Code
	p.Status, p.Code = &status, &code
	if len(failures) == 0 {
		p.Lines = nil
		p.Detail = &r.Detail
	}
	return refuse(p)
}

// reserveOpen reserves what of each goods line's open quantity is not held
// yet: confirm's reservation and Reserve now's.
func (s *server) reserveOpen(ctx context.Context, tx pgx.Tx, o store.OrdersOrder, lines []store.OrdersLine, today time.Time, title string) (bool, error) {
	reserved, err := s.reservedByLine(ctx, tx, o.ID)
	if err != nil {
		return false, err
	}
	want := map[int64]*big.Rat{}
	kits := kitsOf(lines)
	for _, l := range lines {
		if !reservable(l, kits) {
			continue
		}
		q := openToDeliver(l)
		if held := reserved[l.ID]; held != nil {
			q.Sub(q, held)
		}
		if q.Sign() > 0 {
			want[l.ID] = q
		}
	}
	if len(want) == 0 {
		return false, nil
	}
	return true, s.reserveLines(ctx, tx, o, lines, want, today, title)
}

// adjustReservations follows a confirmed order's edit (O26, O46) on tx:
// before are its lines under the lock, after as written. A line removed
// releases all it holds, and so does a line whose item changed — it then
// reserves its whole open quantity of the new variant; a line holding more
// than its open quantity releases the excess; and, while the order holds
// reservations, a goods line reserves what its open quantity grew by — a new
// line its whole open quantity. With Inventory off a kept line keeps how it
// is delivered (saveConfirmedOrder), so it releases only what it no longer
// has open, never because Inventory is off.
func (s *server) adjustReservations(ctx context.Context, tx pgx.Tx, o store.OrdersOrder, before, after []store.OrdersLine, today time.Time, title string) error {
	reserved, err := s.reservedByLine(ctx, tx, o.ID)
	if err != nil {
		return err
	}
	holds := holdsReservations(reserved)
	if !holds {
		return nil
	}
	was := byID(before)
	now := byID(after)
	kitsBefore, kitsAfter := kitsOf(before), kitsOf(after)
	// itemChanged reports whether a kept line's variant changed.
	itemChanged := func(l store.OrdersLine) bool {
		old, ok := was[l.ID]
		return ok && !equalInt32(old.VariantID, l.VariantID)
	}
	release := map[int64]*big.Rat{}
	for id, q := range reserved {
		l, kept := now[id]
		switch {
		case !kept, itemChanged(l), !reservable(l, kitsAfter) && s.stockKept():
			release[id] = nil
		default:
			if open := openToDeliver(l); q.Cmp(open) > 0 {
				release[id] = new(big.Rat).Sub(q, open)
			}
		}
	}
	if err := s.releaseLines(ctx, tx, o.ID, release); err != nil {
		return err
	}
	want := map[int64]*big.Rat{}
	for _, l := range after {
		if !reservable(l, kitsAfter) {
			continue
		}
		open := openToDeliver(l)
		grew := open
		if old, ok := was[l.ID]; ok && reservable(old, kitsBefore) && !itemChanged(l) {
			grew = new(big.Rat).Sub(open, openToDeliver(old))
		}
		held := new(big.Rat).Set(orZero(reserved[l.ID]))
		if r, ok := release[l.ID]; ok {
			if r == nil {
				held = new(big.Rat)
			} else {
				held = new(big.Rat).Sub(held, r)
			}
		}
		// Never beyond what is open.
		if room := new(big.Rat).Sub(open, held); grew.Cmp(room) > 0 {
			grew = room
		}
		if grew.Sign() > 0 {
			want[l.ID] = grew
		}
	}
	return s.reserveLines(ctx, tx, o, after, want, today, title)
}

// releaseBeyondOpen releases what each line holds reserved beyond its open
// quantity, on tx: a manual marking's release (O45).
func (s *server) releaseBeyondOpen(ctx context.Context, tx pgx.Tx, orderID int64, lines []store.OrdersLine) error {
	reserved, err := s.reservedByLine(ctx, tx, orderID)
	if err != nil || !holdsReservations(reserved) {
		return err
	}
	now := byID(lines)
	release := map[int64]*big.Rat{}
	for id, q := range reserved {
		l, ok := now[id]
		if !ok {
			release[id] = nil
			continue
		}
		if open := openToDeliver(l); q.Cmp(open) > 0 {
			release[id] = new(big.Rat).Sub(q, open)
		}
	}
	return s.releaseLines(ctx, tx, orderID, release)
}

// judgeSite checks a site through contracts.Stock before any transaction:
// an active site that fulfils orders (site_not_fulfilling otherwise).
// Without Inventory no site is judged usable.
func (s *server) judgeSite(ctx context.Context, f *fieldErrors, field string, id int64) error {
	sites, err := s.stockSites(ctx)
	if err != nil {
		return err
	}
	for _, site := range sites {
		if site.ID == id {
			if !site.Active || !site.FulfilsOrders {
				f.addCoded(field, codeSiteNotFulfilling, "The site is archived or does not fulfil orders")
			}
			return nil
		}
	}
	f.addCoded(field, codeSiteNotFulfilling, "There is no such site, or Inventory keeps no stock")
	return nil
}

// stockSites is Inventory's sites, before any transaction; none when it
// keeps no stock.
func (s *server) stockSites(ctx context.Context) ([]contracts.StockSite, error) {
	if err := noteContractCall(ctx, "Stock.Sites"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, nil
	}
	sites, err := s.deps.Stock.Sites(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: read the sites: %w", err)
	}
	return sites, nil
}

// stockAvailable is each variant's available at the site, before any
// transaction; nil when stock is not kept.
func (s *server) stockAvailable(ctx context.Context, variantIDs []int32, siteID int64) (map[int32]*big.Rat, error) {
	if err := noteContractCall(ctx, "Stock.Available"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil || len(variantIDs) == 0 {
		return nil, nil
	}
	rows, err := s.deps.Stock.Available(ctx, variantIDs, &siteID)
	if err != nil {
		return nil, fmt.Errorf("orders: read what is available: %w", err)
	}
	out := map[int32]*big.Rat{}
	for _, r := range rows {
		if q, ok := new(big.Rat).SetString(r.Quantity); ok {
			out[r.VariantID] = q
		}
	}
	return out, nil
}

// stockLocations is, per variant, the site's locations holding it, before
// any transaction; nil when stock is not kept.
func (s *server) stockLocations(ctx context.Context, variantIDs []int32, siteID int64) (map[int32][]contracts.StockLocation, error) {
	if err := noteContractCall(ctx, "Stock.Locations"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil || len(variantIDs) == 0 {
		return nil, nil
	}
	rows, err := s.deps.Stock.Locations(ctx, variantIDs, siteID)
	if err != nil {
		return nil, fmt.Errorf("orders: read the locations holding stock: %w", err)
	}
	out := map[int32][]contracts.StockLocation{}
	for _, r := range rows {
		out[r.VariantID] = append(out[r.VariantID], r)
	}
	// The primary location first (inventory phase 2 I55): the draft's
	// picker, its proposal and the pick list all start where the variant is
	// meant to be taken from; the rest keep Stock's order, by full code.
	for _, held := range out {
		slices.SortStableFunc(held, func(a, b contracts.StockLocation) int {
			switch {
			case a.Primary == b.Primary:
				return 0
			case a.Primary:
				return -1
			}
			return 1
		})
	}
	return out, nil
}

// withStockFigures adds each line's reserved quantity and, when stock is
// kept and the order names a site, what is available of its variant there
// (O45), after the order's transaction: the reservations are read on a
// read-only transaction of their own, and the available through
// contracts.Stock outside any.
func (s *server) withStockFigures(ctx context.Context, out *gen.OrdersOrder) error {
	if out == nil {
		return nil
	}
	// What Manufacturing makes for the goods lines, and the lines produced,
	// ready to reserve (production.go).
	if err := s.withProduction(ctx, out); err != nil {
		return err
	}
	if s.deps.StockLedger == nil {
		return nil
	}
	var reserved map[int64]*big.Rat
	err := s.withLedgerRead(ctx, func(tx pgx.Tx) error {
		var err error
		reserved, err = s.reservedByLine(ctx, tx, out.Id)
		return err
	})
	if err != nil {
		return err
	}
	var available map[int32]*big.Rat
	if out.SiteId != nil && s.stockKept() {
		var ids []int32
		for _, l := range out.Lines {
			if l.VariantId != nil && l.DeliveryMethod == gen.OrdersLineDeliveryMethodDelivery && !slices.Contains(ids, *l.VariantId) {
				ids = append(ids, *l.VariantId)
			}
		}
		if available, err = s.stockAvailable(ctx, ids, *out.SiteId); err != nil {
			return err
		}
	}
	for i := range out.Lines {
		l := &out.Lines[i]
		zero := 0.0
		l.QtyReserved = &zero
		if q := reserved[l.Id]; q != nil {
			l.QtyReserved = ptr(wire(q, quantityPlaces))
		}
		if l.VariantId != nil && available != nil {
			if q, ok := available[*l.VariantId]; ok {
				l.Available = ptr(wire(q, quantityPlaces))
			}
		}
	}
	// A kit line's available is the kits its children make (M146).
	if available != nil {
		children := map[int64][]store.OrdersLine{}
		for _, l := range out.Lines {
			if l.ParentLineId != nil && l.PerKit != nil {
				per, _ := new(big.Rat).SetString(strconv.FormatFloat(*l.PerKit, 'f', -1, 64))
				children[*l.ParentLineId] = append(children[*l.ParentLineId], store.OrdersLine{VariantID: l.VariantId, PerKit: numeric(per, quantityPlaces)})
			}
		}
		for i := range out.Lines {
			l := &out.Lines[i]
			if kids := children[l.Id]; len(kids) > 0 {
				l.Available = nil
				if q := kitAvailable(kids, available); q != nil {
					l.Available = ptr(wire(q, quantityPlaces))
				}
			}
		}
	}
	return nil
}

// withLedgerRead runs fn on a read-only transaction of the pool, outside the
// module's own marks: the ledger's ReservationsFor takes a transaction.
func (s *server) withLedgerRead(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.deps.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("orders: begin a read of the reservations: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	return fn(tx)
}

// ListOrdersSites List the sites
// (GET /api/v1/orders/sites)
func (s *server) ListOrdersSites(ctx context.Context, _ gen.ListOrdersSitesRequestObject) (gen.ListOrdersSitesResponseObject, error) {
	sites, err := s.stockSites(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.OrdersSiteList{Items: make([]gen.OrdersSite, 0, len(sites))}
	for _, site := range sites {
		out.Items = append(out.Items, gen.OrdersSite{Id: site.ID, Code: site.Code, Name: site.Name, FulfilsOrders: site.FulfilsOrders, Active: site.Active})
	}
	return gen.ListOrdersSites200JSONResponse(out), nil
}
