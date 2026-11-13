package inventory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The stock contracts (orders design O45, module-boundaries rule 24):
// contracts.Stock, the read of on hand and available, and
// contracts.StockLedger, the write of reservations and issues on another
// module's transaction.
//
// A reservation is stock promised to a line of another module's document at
// a site. Available at a site is its on hand, summed over its locations,
// less every reservation there; the balances keep no reserved column (design
// §12.6). Inventory's own adjustments, moves and counts are corrections of
// fact and ignore reservations, so available may go negative; only a
// reservation checks it, and an issue checks on hand as every posting does.
//
// The lock order inside a ledger call, after the caller's own: Products'
// variants (LockVariants, again, on the caller's transaction — the caller
// already holds them, so this waits for nothing), then the lots (posting.go's
// steps 3–5; EnsureLots and the holds in lots.go), then the balances in
// (variant, location, lot) order with no lot first, then the reservations.
// No ledger call makes a contract call of its own: today and the expired
// override come from the caller, read on the pool (manufacturing phase 2
// M62). A reserve locks the
// variants' balances at the site, so two reserves of one variant at one site
// — two orders confirmed for the last unit — serialise there, and the second
// reads what the first reserved. An issue posts through the engine
// (posting.go), whose own order ends at the balances, and then locks the
// document's reservations; a production (manufacturing design M2) the same. A release locks only the reservations: it can
// only make more available, so it need not wait for anyone's balances.

// The movement kinds the ledger posts (design §12: the kinds are an open set
// of application constants, not a database enum): an issue for another
// module's delivery, a production for its producing document (manufacturing
// design M1), and an unbuild that reverses one (manufacturing phase 2 M123).
const (
	movementKindIssue      = "issue"
	movementKindProduction = "production"
	movementKindUnbuild    = "unbuild"
)

// issueKeySpace is the UUID namespace an issue's client request id is made
// in from its document, so a second issue of one document is a replay.
var issueKeySpace = uuid.MustParse("6f1f2f0e-7f5c-4b8e-9a51-3d2b8c1e4a70")

// productionKeySpace is the same for a production: its own space, so a
// production and an issue never share a key.
var productionKeySpace = uuid.MustParse("0b6c2d4e-93a1-4f57-8c2e-5d7a1e9f3b46")

// stock is the module's contracts.Stock.
type stock struct{ s *server }

var _ contracts.Stock = stock{}

// newStock is Module's Stock.
func newStock(d module.Deps) contracts.Stock { return stock{s: newServer(d)} }

// ledger is the module's contracts.StockLedger. It is built for a disabled
// module too (module.Module.StockLedger): it needs the pool's clock and, to
// reserve or issue, the product catalog.
type ledger struct{ s *server }

var _ contracts.StockLedger = ledger{}

// newLedger is Module's StockLedger.
func newLedger(d module.Deps) contracts.StockLedger { return ledger{s: newServer(d)} }

// OnHand answers each variant's on hand, in total and per site holding it.
func (st stock) OnHand(ctx context.Context, variantIDs []int32, siteID *int64) ([]contracts.StockQuantity, error) {
	return st.read(ctx, variantIDs, siteID, false)
}

// Available answers each variant's usable on hand less what is reserved, in
// total and per site where it lies or is reserved (manufacturing phase 2
// M63).
func (st stock) Available(ctx context.Context, variantIDs []int32, siteID *int64) ([]contracts.StockQuantity, error) {
	return st.read(ctx, variantIDs, siteID, true)
}

// Lots answers, per variant, every lot holding more than 0 at an active
// location of the site, with its location, in FEFO order (manufacturing
// phase 2 M65, M67).
func (st stock) Lots(ctx context.Context, variantIDs []int32, siteID int64) ([]contracts.StockLot, error) {
	if err := contracts.ValidateStockVariants(variantIDs); err != nil {
		return nil, err
	}
	rows, err := store.New(st.s.deps.Pool).StockLotsAt(ctx, store.StockLotsAtParams{VariantIds: variantIDs, SiteID: siteID})
	if err != nil {
		return nil, fmt.Errorf("inventory: read the lots at site %d: %w", siteID, err)
	}
	out := make([]contracts.StockLot, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.StockLot{
			VariantID: r.VariantID, LotID: r.LotID, Number: r.Number, Serial: r.Serial, Expiry: dateOrNil(r.Expiry),
			BestBefore: dateOrNil(r.BestBefore), ReceivedAt: r.ReceivedAt, LocationID: r.LocationID, FullCode: r.FullCode,
			Quantity: decimalText(ratFromText(r.OnHand)), Status: r.Status,
		})
	}
	return out, nil
}

// Placements answers every placement with a reorder point at an active
// site, with the low-stock rule's figures on usable on hand (manufacturing
// phase 2 M106), in one statement on the pool.
func (st stock) Placements(ctx context.Context, siteID int64) ([]contracts.StockPlacement, error) {
	today, err := st.s.today(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := store.New(st.s.deps.Pool).StockPlacementsAt(ctx, store.StockPlacementsAtParams{
		Today: pgtype.Date{Time: today, Valid: true}, SiteID: siteID, RowLimit: contracts.MaxStockPlacements + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("inventory: read the placements at site %d: %w", siteID, err)
	}
	if len(rows) > contracts.MaxStockPlacements {
		return nil, fmt.Errorf("inventory: site %d has more than %d placements with a reorder point", siteID, contracts.MaxStockPlacements)
	}
	out := make([]contracts.StockPlacement, 0, len(rows))
	for _, r := range rows {
		p := contracts.StockPlacement{
			VariantID: r.VariantID, ReorderPoint: decimalText(ratFromText(r.ReorderPoint)),
			OnHand: decimalText(ratFromText(r.OnHand)), Reserved: decimalText(ratFromText(r.Reserved)),
			Incoming: decimalText(ratFromText(r.Incoming)),
		}
		if m := optionalRat(r.Maximum); m != nil {
			p.Maximum = decimalText(m)
		}
		if m := optionalRat(r.Multiple); m != nil {
			p.Multiple = decimalText(m)
		}
		out = append(out, p)
	}
	return out, nil
}

// LotRefs answers the lots named, by id (manufacturing phase 2 M99).
func (st stock) LotRefs(ctx context.Context, lotIDs []int64) ([]contracts.LotRef, error) {
	if len(lotIDs) > contracts.MaxStockVariants {
		return nil, fmt.Errorf("inventory: at most %d lots a read", contracts.MaxStockVariants)
	}
	if len(lotIDs) == 0 {
		return nil, nil
	}
	rows, err := store.New(st.s.deps.Pool).LotsByIDs(ctx, lotIDs)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the lots: %w", err)
	}
	out := make([]contracts.LotRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.LotRef{ID: r.ID, VariantID: r.VariantID, Number: r.Number, Status: r.Status})
	}
	return out, nil
}

// Sites answers every site that is not archived, by code.
func (st stock) Sites(ctx context.Context) ([]contracts.StockSite, error) {
	rows, err := store.New(st.s.deps.Pool).StockSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the sites: %w", err)
	}
	out := make([]contracts.StockSite, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.StockSite{ID: r.ID, Code: r.Code, Name: r.Name, FulfilsOrders: r.FulfilsOrders, Active: r.Status == "active",
			RootLocationID: r.RootLocationID})
	}
	return out, nil
}

// SiteLocations answers every location of the site, archived ones too, by
// full code.
func (st stock) SiteLocations(ctx context.Context, siteID int64) ([]contracts.StockSiteLocation, error) {
	rows, err := store.New(st.s.deps.Pool).StockSiteLocations(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the locations of site %d: %w", siteID, err)
	}
	out := make([]contracts.StockSiteLocation, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.StockSiteLocation{ID: r.ID, ParentID: r.ParentID, Kind: r.Kind, FullCode: r.FullCode,
			Active: r.Status == string(gen.Active), Root: r.ParentID == nil})
	}
	return out, nil
}

// Locations answers, per variant, the site's active locations holding more
// than 0 of it, by full code, each marked when it is the variant's primary
// location at the site (phase 2 I55) — the caller puts it first.
func (st stock) Locations(ctx context.Context, variantIDs []int32, siteID int64) ([]contracts.StockLocation, error) {
	if err := contracts.ValidateStockVariants(variantIDs); err != nil {
		return nil, err
	}
	var rows []store.StockLocationsAtRow
	primary := map[int32]int64{}
	err := st.s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if rows, err = q.StockLocationsAt(ctx, store.StockLocationsAtParams{VariantIds: variantIDs, SiteID: siteID}); err != nil {
			return fmt.Errorf("inventory: read the locations holding stock: %w", err)
		}
		placed, err := q.PrimaryLocationsAt(ctx, store.PrimaryLocationsAtParams{SiteID: siteID, VariantIds: variantIDs})
		if err != nil {
			return fmt.Errorf("inventory: read the primary locations: %w", err)
		}
		for _, p := range placed {
			primary[p.VariantID] = p.PrimaryLocationID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]contracts.StockLocation, 0, len(rows))
	for _, r := range rows {
		id, placed := primary[r.VariantID]
		out = append(out, contracts.StockLocation{VariantID: r.VariantID, LocationID: r.LocationID, FullCode: r.FullCode,
			Quantity: decimalText(ratFromText(r.OnHand)), Primary: placed && id == r.LocationID})
	}
	return out, nil
}

// read is OnHand and Available, from one snapshot of the balances and the
// reservations.
func (st stock) read(ctx context.Context, variantIDs []int32, siteID *int64, available bool) ([]contracts.StockQuantity, error) {
	if err := contracts.ValidateStockVariants(variantIDs); err != nil {
		return nil, err
	}
	onHand := map[siteKey]*big.Rat{}
	reserved := map[siteKey]*big.Rat{}
	usable := map[siteKey]*big.Rat{}
	// Available is judged on usable on hand, on today in the workspace zone
	// (M63), read before the snapshot.
	var today time.Time
	if available {
		var err error
		if today, err = st.s.today(ctx); err != nil {
			return nil, err
		}
	}
	err := st.s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.OnHandBySite(ctx, variantIDs)
		if err != nil {
			return fmt.Errorf("inventory: read on hand by site: %w", err)
		}
		for _, r := range rows {
			onHand[siteKey{r.VariantID, r.SiteID}] = ratFromText(r.OnHand)
		}
		if !available {
			return nil
		}
		use, err := q.UsableBySite(ctx, store.UsableBySiteParams{VariantIds: variantIDs, Today: pgtype.Date{Time: today, Valid: true}})
		if err != nil {
			return fmt.Errorf("inventory: read usable on hand by site: %w", err)
		}
		for _, r := range use {
			usable[siteKey{r.VariantID, r.SiteID}] = ratFromText(r.OnHand)
		}
		res, err := q.ReservedOfVariants(ctx, variantIDs)
		if err != nil {
			return fmt.Errorf("inventory: read reservations: %w", err)
		}
		for _, r := range res {
			reserved[siteKey{r.VariantID, r.SiteID}] = ratFromText(r.Reserved)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sites := map[int32][]int64{}
	for _, m := range []map[siteKey]*big.Rat{onHand, reserved} {
		for k := range m {
			if siteID == nil || k.siteID == *siteID {
				sites[k.variantID] = append(sites[k.variantID], k.siteID)
			}
		}
	}
	out := make([]contracts.StockQuantity, 0, len(variantIDs))
	for _, id := range variantIDs {
		ids := slices.Compact(slices.Sorted(slices.Values(sites[id])))
		q := contracts.StockQuantity{VariantID: id, Sites: make([]contracts.SiteQuantity, 0, len(ids))}
		total := new(big.Rat)
		for _, site := range ids {
			k := siteKey{id, site}
			v := new(big.Rat)
			figure := onHand
			if available {
				figure = usable
			}
			if r := figure[k]; r != nil {
				v.Add(v, r)
			}
			if r := reserved[k]; r != nil {
				v.Sub(v, r)
			}
			total.Add(total, v)
			q.Sites = append(q.Sites, contracts.SiteQuantity{SiteID: site, Quantity: decimalText(v)})
		}
		q.Quantity = decimalText(total)
		out = append(out, q)
	}
	return out, nil
}

// ledgerQuantity is a ledger call's quantity text as an exact decimal: more
// than 0, at most three decimals, within numeric(14,3). A caller's bad
// quantity is a failure, not a refusal.
func ledgerQuantity(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok || strings.ContainsAny(s, "eE/") {
		return nil, fmt.Errorf("inventory: %q is not a decimal quantity", s)
	}
	if _, decimals, found := strings.Cut(s, "."); found && len(decimals) > quantityPlaces {
		return nil, fmt.Errorf("inventory: the quantity %s has more than three decimals", s)
	}
	if r.Sign() <= 0 || r.Cmp(maxQuantity) > 0 {
		return nil, fmt.Errorf("inventory: the quantity %s is not more than 0 and at most 99,999,999,999.999", s)
	}
	return r, nil
}

// lineIDText is a source line id as the reservations and movements keep it.
func lineIDText(id int64) string { return strconv.FormatInt(id, 10) }

// refusalOf is the StockRefusal naming every failing line, the first
// repeated at the top.
func refusalOf(lines []contracts.StockLineRefusal) error {
	first := lines[0]
	return &contracts.StockRefusal{Code: first.Code, VariantID: first.VariantID, LineID: first.LineID, Detail: first.Detail, Lines: lines}
}

// siteRefusal is a StockRefusal about the site, naming no line.
func siteRefusal(code, detail string) error {
	return &contracts.StockRefusal{Code: code, Detail: detail}
}

// fulfillingSite reads the site plainly and refuses one that is unknown,
// archived or — when fulfils, as for an order's reserve and issue — not
// fulfilling orders. Without fulfils (a production and its reserve), an
// archived site is site_archived: there is no flag to name. It locks nothing: a flag turned off under a reserve is
// the same race as one turned off just after it.
func fulfillingSite(ctx context.Context, txq *store.Queries, siteID int64, fulfils bool) (store.GetSiteRow, error) {
	site, err := txq.GetSite(ctx, siteID)
	if noRows(err) {
		return site, siteRefusal(contracts.StockSiteUnknown, fmt.Sprintf("No site has the id %d.", siteID))
	}
	if err != nil {
		return site, fmt.Errorf("inventory: read site %d: %w", siteID, err)
	}
	switch {
	case site.Status != string(gen.Active) && !fulfils:
		return site, siteRefusal(contracts.StockSiteArchived, fmt.Sprintf("The site %s is archived.", site.Code))
	case site.Status != string(gen.Active):
		return site, siteRefusal(contracts.StockSiteNotFulfilling, fmt.Sprintf("The site %s is archived.", site.Code))
	case fulfils && !site.FulfilsOrders:
		return site, siteRefusal(contracts.StockSiteNotFulfilling, fmt.Sprintf("The site %s does not fulfil orders.", site.Code))
	}
	return site, nil
}

// stockedVariants locks the variants on tx (again: the caller holds them)
// and answers them by id, with a refusal line for each of lines whose
// variant is unknown or not goods.
func (l ledger) stockedVariants(ctx context.Context, tx pgx.Tx, ids []int32, lineOf func(i int) (int32, int64), n int) (map[int32]contracts.VariantEntry, []contracts.StockLineRefusal, error) {
	sorted := slices.Compact(slices.Sorted(slices.Values(ids)))
	entries, err := l.s.lockVariants(ctx, tx, sorted)
	if err != nil {
		return nil, nil, err
	}
	variants := make(map[int32]contracts.VariantEntry, len(entries))
	for _, e := range entries {
		variants[e.ID] = e
	}
	var failures []contracts.StockLineRefusal
	for i := range n {
		variantID, lineID := lineOf(i)
		v, ok := variants[variantID]
		switch {
		case !ok:
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockVariantNotStocked, VariantID: variantID, LineID: lineID, Index: i,
				Detail: fmt.Sprintf("No variant has the id %d.", variantID)})
		case v.ProductType != productTypeGoods:
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockVariantNotStocked, VariantID: variantID, LineID: lineID, Index: i,
				Detail: fmt.Sprintf("%s is a service; only goods are kept in stock.", v.SKU)})
		}
	}
	return variants, failures, nil
}

// Reserve adds each line's quantity to its reservation at the site, when the
// site's available — usable on hand on today less reserved (M63) — covers it.
func (l ledger) Reserve(ctx context.Context, tx pgx.Tx, src contracts.StockSource, siteID int64, today time.Time, lines []contracts.StockLine) error {
	return l.reserve(ctx, tx, src, siteID, today, lines, true)
}

// ReserveForProduction is Reserve at a site that need not fulfil orders
// (manufacturing design M3).
func (l ledger) ReserveForProduction(ctx context.Context, tx pgx.Tx, src contracts.StockSource, siteID int64, today time.Time, lines []contracts.StockLine) error {
	return l.reserve(ctx, tx, src, siteID, today, lines, false)
}

// reserve is Reserve and ReserveForProduction: fulfils is whether the site
// must fulfil orders.
func (l ledger) reserve(ctx context.Context, tx pgx.Tx, src contracts.StockSource, siteID int64, today time.Time, lines []contracts.StockLine, fulfils bool) error {
	if err := src.Validate(); err != nil {
		return err
	}
	if today.IsZero() {
		return errors.New("contracts: a stock call without today")
	}
	if len(lines) == 0 {
		return nil
	}
	quantities := make([]*big.Rat, len(lines))
	variantOfLine := map[int64]int32{}
	ids := make([]int32, 0, len(lines))
	for i, line := range lines {
		q, err := ledgerQuantity(line.Quantity)
		if err != nil {
			return err
		}
		if line.LineID <= 0 {
			return fmt.Errorf("inventory: a reserve names line id %d", line.LineID)
		}
		if v, ok := variantOfLine[line.LineID]; ok && v != line.VariantID {
			return fmt.Errorf("inventory: a reserve names line %d with two variants", line.LineID)
		}
		variantOfLine[line.LineID] = line.VariantID
		quantities[i] = q
		ids = append(ids, line.VariantID)
	}
	txq := store.New(tx)
	site, err := fulfillingSite(ctx, txq, siteID, fulfils)
	if err != nil {
		return err
	}
	variants, failures, err := l.stockedVariants(ctx, tx, ids, func(i int) (int32, int64) { return lines[i].VariantID, lines[i].LineID }, len(lines))
	if err != nil {
		return err
	}
	if len(failures) > 0 {
		return refusalOf(failures)
	}
	sorted := slices.Compact(slices.Sorted(slices.Values(ids)))
	if _, err := txq.LockSiteBalances(ctx, store.LockSiteBalancesParams{VariantIds: sorted, SiteID: siteID}); err != nil {
		return fmt.Errorf("inventory: lock the balances of site %d: %w", siteID, err)
	}
	// Read after the lock, in a statement of its own, so a reserve that held
	// the balances before this one is seen with what it reserved.
	figures, err := txq.SiteStockOf(ctx, store.SiteStockOfParams{VariantIds: sorted, SiteID: siteID, Today: pgtype.Date{Time: today, Valid: true}})
	if err != nil {
		return fmt.Errorf("inventory: read the stock of site %d: %w", siteID, err)
	}
	left := make(map[int32]*big.Rat, len(figures))
	for _, f := range figures {
		left[f.VariantID] = new(big.Rat).Sub(ratFromText(f.OnHand), ratFromText(f.Reserved))
	}
	byLine := map[int64]*big.Rat{}
	for i, line := range lines {
		avail := left[line.VariantID]
		if avail.Cmp(quantities[i]) < 0 {
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockInsufficientAvailable, VariantID: line.VariantID, LineID: line.LineID, Index: i,
				Detail: fmt.Sprintf("%s at %s: %s available, the line reserves %s.", variants[line.VariantID].SKU, site.Code,
					decimalText(maxRat(avail, new(big.Rat))), decimalText(quantities[i]))})
			continue
		}
		avail.Sub(avail, quantities[i])
		if byLine[line.LineID] == nil {
			byLine[line.LineID] = new(big.Rat)
		}
		byLine[line.LineID].Add(byLine[line.LineID], quantities[i])
	}
	if len(failures) > 0 {
		return refusalOf(failures)
	}
	p := store.AddReservationsParams{
		SiteID: siteID, SourceModule: src.Module, SourceKind: src.Kind, SourceID: strconv.FormatInt(src.ID, 10), Now: l.s.deps.Clock(),
	}
	lineIDs := slices.Sorted(maps.Keys(byLine))
	for _, id := range lineIDs {
		n, err := numericFromRat(byLine[id])
		if err != nil {
			return err
		}
		p.VariantIds = append(p.VariantIds, variantOfLine[id])
		p.LineIds = append(p.LineIds, lineIDText(id))
		p.Quantities = append(p.Quantities, n)
	}
	n, err := txq.AddReservations(ctx, p)
	if err != nil {
		return fmt.Errorf("inventory: reserve for %s/%s/%d: %w", src.Module, src.Kind, src.ID, err)
	}
	// A line already holding another variant at the site: the caller
	// changed the line's item without releasing it first.
	if n != int64(len(lineIDs)) {
		return fmt.Errorf("inventory: reserve for %s/%s/%d: a line already holds another variant at site %d; release it first",
			src.Module, src.Kind, src.ID, siteID)
	}
	return nil
}

// maxRat is the larger of a and b.
func maxRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

// Release gives back the document's reservations: all of them, or per
// release the line's whole reservation or a quantity of it.
func (l ledger) Release(ctx context.Context, tx pgx.Tx, src contracts.StockSource, releases []contracts.StockRelease) error {
	if err := src.Validate(); err != nil {
		return err
	}
	type release struct {
		all      bool
		quantity *big.Rat
	}
	wanted := map[string]*release{}
	for _, r := range releases {
		if r.LineID <= 0 {
			return fmt.Errorf("inventory: a release names line id %d", r.LineID)
		}
		key := lineIDText(r.LineID)
		w := wanted[key]
		if w == nil {
			w = &release{quantity: new(big.Rat)}
			wanted[key] = w
		}
		if r.Quantity == "" {
			w.all = true
			continue
		}
		q, err := ledgerQuantity(r.Quantity)
		if err != nil {
			return err
		}
		w.quantity.Add(w.quantity, q)
	}
	txq := store.New(tx)
	rows, err := txq.LockSourceReservations(ctx, sourceParams(src))
	if err != nil {
		return fmt.Errorf("inventory: lock the reservations of %s/%s/%d: %w", src.Module, src.Kind, src.ID, err)
	}
	// A line's reservations are given back site by site, in site id order.
	slices.SortFunc(rows, func(a, b store.InventoryReservation) int {
		return cmp.Or(strings.Compare(a.SourceLineID, b.SourceLineID), cmp.Compare(a.SiteID, b.SiteID))
	})
	left := make([]*big.Rat, len(rows))
	for i, r := range rows {
		left[i] = ratFromNumeric(r.Quantity)
		if len(releases) == 0 {
			left[i] = new(big.Rat)
			continue
		}
		w := wanted[r.SourceLineID]
		switch {
		case w == nil:
		case w.all:
			left[i] = new(big.Rat)
		default:
			take := minRat(left[i], w.quantity)
			left[i].Sub(left[i], take)
			w.quantity.Sub(w.quantity, take)
		}
	}
	return l.writeReservations(ctx, txq, rows, left)
}

// minRat is the smaller of a and b, as a new value.
func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

// writeReservations writes what each locked reservation has left, deleting
// those left at zero; one left as it was is not written.
func (l ledger) writeReservations(ctx context.Context, txq *store.Queries, rows []store.InventoryReservation, left []*big.Rat) error {
	var gone, changed []int64
	var quantities []pgtype.Numeric
	for i, r := range rows {
		switch {
		case left[i].Sign() <= 0:
			gone = append(gone, r.ID)
		case left[i].Cmp(ratFromNumeric(r.Quantity)) != 0:
			n, err := numericFromRat(left[i])
			if err != nil {
				return err
			}
			changed = append(changed, r.ID)
			quantities = append(quantities, n)
		}
	}
	if len(changed) > 0 {
		if err := txq.SetReservations(ctx, store.SetReservationsParams{Ids: changed, Quantities: quantities, Now: l.s.deps.Clock()}); err != nil {
			return fmt.Errorf("inventory: write reservations: %w", err)
		}
	}
	if len(gone) > 0 {
		if err := txq.DeleteReservations(ctx, gone); err != nil {
			return fmt.Errorf("inventory: delete reservations: %w", err)
		}
	}
	return nil
}

// sourceParams is a document as the reservation queries name it.
func sourceParams(src contracts.StockSource) store.LockSourceReservationsParams {
	return store.LockSourceReservationsParams{SourceModule: src.Module, SourceKind: src.Kind, SourceID: strconv.FormatInt(src.ID, 10)}
}

// ReservationsFor answers every reservation the document holds, by line id
// then site id. It takes no lock and must not: Orders reads it in a
// read-only transaction.
func (l ledger) ReservationsFor(ctx context.Context, tx pgx.Tx, src contracts.StockSource) ([]contracts.Reservation, error) {
	if err := src.Validate(); err != nil {
		return nil, err
	}
	rows, err := store.New(tx).SourceReservations(ctx, store.SourceReservationsParams(sourceParams(src)))
	if err != nil {
		return nil, fmt.Errorf("inventory: read the reservations of %s/%s/%d: %w", src.Module, src.Kind, src.ID, err)
	}
	out := make([]contracts.Reservation, 0, len(rows))
	for _, r := range rows {
		lineID, err := strconv.ParseInt(r.SourceLineID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("inventory: reservation %d has the line id %q: %w", r.ID, r.SourceLineID, err)
		}
		out = append(out, contracts.Reservation{VariantID: r.VariantID, LineID: lineID, SiteID: r.SiteID, Quantity: decimalText(ratFromNumeric(r.Quantity))})
	}
	slices.SortFunc(out, func(a, b contracts.Reservation) int {
		return cmp.Or(cmp.Compare(a.LineID, b.LineID), cmp.Compare(a.SiteID, b.SiteID))
	})
	return out, nil
}

// ReservationsForSources answers every reservation of the documents, in
// one statement and without a lock, by source id, line id then site id
// (manufacturing phase 2 M107a).
func (l ledger) ReservationsForSources(ctx context.Context, tx pgx.Tx, srcs []contracts.StockSource) ([]contracts.SourceReservation, error) {
	if len(srcs) > contracts.MaxStockSources {
		return nil, fmt.Errorf("inventory: a reservations read names %d documents, more than %d", len(srcs), contracts.MaxStockSources)
	}
	if len(srcs) == 0 {
		return nil, nil
	}
	params := store.ReservationsOfSourcesParams{}
	for _, src := range srcs {
		if err := src.Validate(); err != nil {
			return nil, err
		}
		params.SourceModules = append(params.SourceModules, src.Module)
		params.SourceKinds = append(params.SourceKinds, src.Kind)
		params.SourceIds = append(params.SourceIds, strconv.FormatInt(src.ID, 10))
	}
	rows, err := store.New(tx).ReservationsOfSources(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the reservations of %d documents: %w", len(srcs), err)
	}
	out := make([]contracts.SourceReservation, 0, len(rows))
	for _, r := range rows {
		id, err := strconv.ParseInt(r.SourceID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("inventory: reservation %d has the source id %q: %w", r.ID, r.SourceID, err)
		}
		lineID, err := strconv.ParseInt(r.SourceLineID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("inventory: reservation %d has the line id %q: %w", r.ID, r.SourceLineID, err)
		}
		out = append(out, contracts.SourceReservation{
			Source:      contracts.StockSource{Module: r.SourceModule, Kind: r.SourceKind, ID: id},
			Reservation: contracts.Reservation{VariantID: r.VariantID, LineID: lineID, SiteID: r.SiteID, Quantity: decimalText(ratFromNumeric(r.Quantity))},
		})
	}
	slices.SortFunc(out, func(a, b contracts.SourceReservation) int {
		return cmp.Or(cmp.Compare(a.Source.Module, b.Source.Module), cmp.Compare(a.Source.Kind, b.Source.Kind),
			cmp.Compare(a.Source.ID, b.Source.ID), cmp.Compare(a.LineID, b.LineID), cmp.Compare(a.SiteID, b.SiteID))
	})
	return out, nil
}

// issueBody is what an issue's request hash is taken over: the site and the
// lines, as given — their lots, AutoLot and AllowExpired included — and the
// override the call carried.
type issueBody struct {
	Source          contracts.StockSource
	SiteID          int64
	Lines           []contracts.IssueLine
	ExpiredOverride bool
}

// Issue posts the issue as one movement through the engine and consumes the
// reservations of the lines it issued.
func (l ledger) Issue(ctx context.Context, tx pgx.Tx, src contracts.StockSource, issue contracts.StockIssue) (contracts.StockPosted, error) {
	if err := src.Validate(); err != nil {
		return contracts.StockPosted{}, err
	}
	if issue.Today.IsZero() {
		return contracts.StockPosted{}, errors.New("contracts: a stock call without today")
	}
	doc := issue.Document
	if doc.IsZero() {
		doc = src
	}
	if err := doc.Validate(); err != nil {
		return contracts.StockPosted{}, err
	}
	if len(issue.Lines) == 0 {
		return contracts.StockPosted{}, errors.New("inventory: an issue has no lines")
	}
	lines := make([]engineLine, len(issue.Lines))
	// What is issued per line and variant.
	issued := map[takenKey]*big.Rat{}
	for i, line := range issue.Lines {
		q, err := ledgerQuantity(line.Quantity)
		if err != nil {
			return contracts.StockPosted{}, err
		}
		if line.LineID <= 0 {
			return contracts.StockPosted{}, fmt.Errorf("inventory: an issue names line id %d", line.LineID)
		}
		if line.AutoLot && line.LotID != 0 {
			return contracts.StockPosted{}, fmt.Errorf("inventory: an issue line names lot %d and AutoLot", line.LotID)
		}
		from := line.FromLocationID
		lines[i] = engineLine{index: i, variantID: line.VariantID, from: &from, quantity: q, lotID: line.LotID, autoLot: line.AutoLot, allowExpired: line.AllowExpired}
		takenOf(issued, line.LineID, line.VariantID, q)
	}
	hash, err := requestHash(movementKindIssue, issueBody{Source: doc, SiteID: issue.SiteID, Lines: issue.Lines, ExpiredOverride: issue.ExpiredOverride})
	if err != nil {
		return contracts.StockPosted{}, err
	}
	display := issue.PostedByDisplay
	if utf8.RuneCountInString(display) > 200 {
		display = string([]rune(display)[:200])
	}
	header := movementHeader{
		clientRequestID: uuid.NewSHA1(issueKeySpace, []byte(fmt.Sprintf("%s\x00%s\x00%d", doc.Module, doc.Kind, doc.ID))),
		requestHash:     hash, siteID: issue.SiteID, kind: movementKindIssue,
		poster: actor{UserID: issue.PostedByUserID, Display: display},
		source: &movementSource{module: doc.Module, kind: doc.Kind, id: strconv.FormatInt(doc.ID, 10)},
	}
	txq := store.New(tx)
	lineOf := issueLineOf(issue.Lines)
	// A replay first, before the site or the variants are judged again (I14).
	if res, done, err := replayOf(ctx, txq, header); done || err != nil {
		if err != nil {
			return contracts.StockPosted{}, issueError(err, lineOf, len(issue.Lines))
		}
		return l.replayed(ctx, txq, res.movementID, lines)
	}
	if _, err := fulfillingSite(ctx, txq, issue.SiteID, true); err != nil {
		return contracts.StockPosted{}, err
	}
	ids := make([]int32, 0, len(issue.Lines))
	for _, line := range issue.Lines {
		ids = append(ids, line.VariantID)
	}
	_, failures, err := l.stockedVariants(ctx, tx, ids, lineOf, len(issue.Lines))
	if err != nil {
		return contracts.StockPosted{}, err
	}
	if len(failures) > 0 {
		return contracts.StockPosted{}, refusalOf(failures)
	}
	res, err := l.s.post(ctx, tx, txq, header, lines, postOptions{today: issue.Today, takes: true, expiredOverride: issue.ExpiredOverride})
	if err != nil {
		return contracts.StockPosted{}, issueError(err, lineOf, len(issue.Lines))
	}
	if res.replay {
		return l.replayed(ctx, txq, res.movementID, lines)
	}
	if err := l.consumeReservations(ctx, txq, src, issue.SiteID, issued); err != nil {
		return contracts.StockPosted{}, err
	}
	return contracts.StockPosted{MovementID: res.movementID, Picks: res.picks}, nil
}

// replayed is a replayed issue's or production's answer: the first
// movement, with the lots its AutoLot lines took read back from its lines.
// The engine wrote the lines in the order given, an AutoLot line as one line
// per lot it took, so they are walked in step: a line that is not AutoLot is
// one movement line, an AutoLot line the movement lines of its variant and
// location that sum to its quantity — or, for an untracked variant, the one
// movement line with no lot it posted as.
func (l ledger) replayed(ctx context.Context, txq *store.Queries, movementID int64, lines []engineLine) (contracts.StockPosted, error) {
	out := contracts.StockPosted{MovementID: movementID}
	auto := false
	for _, line := range lines {
		auto = auto || line.autoLot
	}
	if !auto {
		return out, nil
	}
	posted, err := txq.MovementLines(ctx, movementID)
	if err != nil {
		return out, fmt.Errorf("inventory: read the lines of movement %d: %w", movementID, err)
	}
	next := 0
	for _, line := range lines {
		if !line.autoLot {
			next++
			continue
		}
		// An AutoLot line of an untracked variant posted as one line with no
		// lot: it picked nothing.
		if next < len(posted) {
			p := posted[next]
			if p.LotID == nil && p.VariantID == line.variantID && p.FromLocationID != nil && *p.FromLocationID == *line.from {
				next++
				continue
			}
		}
		left := new(big.Rat).Set(line.quantity)
		for left.Sign() > 0 && next < len(posted) {
			p := posted[next]
			if p.VariantID != line.variantID || p.FromLocationID == nil || *p.FromLocationID != *line.from || p.LotID == nil {
				break
			}
			q := ratFromNumeric(p.Quantity)
			out.Picks = append(out.Picks, contracts.StockPick{Index: line.index, LotID: *p.LotID, LocationID: *p.FromLocationID, Quantity: decimalText(q)})
			left.Sub(left, q)
			next++
		}
	}
	return out, nil
}

// takenKey is a line of the reserving document and the variant taken for
// it: a line's reservation is consumed only by stock taken of the variant it
// holds.
type takenKey struct {
	line    int64
	variant int32
}

// takenOf adds q to what is taken for the line and variant.
func takenOf(taken map[takenKey]*big.Rat, lineID int64, variantID int32, q *big.Rat) {
	k := takenKey{lineID, variantID}
	if taken[k] == nil {
		taken[k] = new(big.Rat)
	}
	taken[k].Add(taken[k], q)
}

// consumeReservations consumes the reservations of src's lines taken, at the
// site and of the variant taken, as far as they go — never below zero. It
// locks the document's reservations after the posting's balances.
func (l ledger) consumeReservations(ctx context.Context, txq *store.Queries, src contracts.StockSource, siteID int64, taken map[takenKey]*big.Rat) error {
	rows, err := txq.LockSourceReservations(ctx, sourceParams(src))
	if err != nil {
		return fmt.Errorf("inventory: lock the reservations of %s/%s/%d: %w", src.Module, src.Kind, src.ID, err)
	}
	left := make([]*big.Rat, len(rows))
	for i, r := range rows {
		left[i] = ratFromNumeric(r.Quantity)
		if r.SiteID != siteID {
			continue
		}
		lineID, err := strconv.ParseInt(r.SourceLineID, 10, 64)
		if err != nil {
			return fmt.Errorf("inventory: reservation %d has the line id %q: %w", r.ID, r.SourceLineID, err)
		}
		if q := taken[takenKey{lineID, r.VariantID}]; q != nil {
			take := minRat(left[i], q)
			left[i].Sub(left[i], take)
			q.Sub(q, take)
		}
	}
	return l.writeReservations(ctx, txq, rows, left)
}

// productionBody is what a production's request hash is taken over: the
// document and the reserving source, the site and the lines, as given — lots
// included — the override and whether it is an unbuild.
type productionBody struct {
	Source          contracts.StockSource
	Reserving       contracts.StockSource
	SiteID          int64
	Consume         []contracts.ConsumeLine
	Produce         []contracts.ProduceLine
	ExpiredOverride bool
	Unbuild         bool
}

// Produce posts the production as one movement through the engine — the
// consume lines, from-only, then the produce lines, to-only — and consumes
// the reservations of the lines it consumed (manufacturing design M1–M2).
func (l ledger) Produce(ctx context.Context, tx pgx.Tx, src contracts.StockSource, p contracts.StockProduction) (contracts.StockPosted, error) {
	if err := src.Validate(); err != nil {
		return contracts.StockPosted{}, err
	}
	if p.Today.IsZero() {
		return contracts.StockPosted{}, errors.New("contracts: a stock call without today")
	}
	doc := p.Document
	if doc.IsZero() {
		doc = src
	}
	if err := doc.Validate(); err != nil {
		return contracts.StockPosted{}, err
	}
	n := len(p.Consume) + len(p.Produce)
	if n == 0 {
		return contracts.StockPosted{}, siteRefusal(contracts.StockNothingToPost, "A production names no line; nothing was posted.")
	}
	// The engine's lines are the consume lines, then the produce lines;
	// lineOf names the contract's line behind the engine's index.
	lineOf := func(i int) (int32, int64) {
		if i < len(p.Consume) {
			return p.Consume[i].VariantID, p.Consume[i].LineID
		}
		return p.Produce[i-len(p.Consume)].VariantID, p.Produce[i-len(p.Consume)].LineID
	}
	// A bad quantity is the caller's to show, as a refusal naming every
	// such line.
	quantities := make([]*big.Rat, n)
	var bad []contracts.StockLineRefusal
	for i := range n {
		var text string
		if i < len(p.Consume) {
			text = p.Consume[i].Quantity
		} else {
			text = p.Produce[i-len(p.Consume)].Quantity
		}
		q, err := ledgerQuantity(text)
		if err != nil {
			variantID, lineID := lineOf(i)
			bad = append(bad, contracts.StockLineRefusal{Code: contracts.StockQuantityInvalid, VariantID: variantID, LineID: lineID, Index: i,
				Detail: fmt.Sprintf("The quantity %q is not more than 0 with at most three decimals.", text)})
			continue
		}
		quantities[i] = q
	}
	if len(bad) > 0 {
		return contracts.StockPosted{}, refusalOf(bad)
	}
	lines := make([]engineLine, 0, n)
	consumed := map[takenKey]*big.Rat{}
	for i, line := range p.Consume {
		q := quantities[i]
		if line.LineID <= 0 {
			return contracts.StockPosted{}, fmt.Errorf("inventory: a production consumes for line id %d", line.LineID)
		}
		if line.AutoLot && line.LotID != 0 {
			return contracts.StockPosted{}, fmt.Errorf("inventory: a consume line names lot %d and AutoLot", line.LotID)
		}
		from := line.FromLocationID
		lines = append(lines, engineLine{index: i, variantID: line.VariantID, from: &from, quantity: q, lotID: line.LotID, autoLot: line.AutoLot,
			allowExpired: line.AllowExpired})
		takenOf(consumed, line.LineID, line.VariantID, q)
	}
	for i, line := range p.Produce {
		q := quantities[len(p.Consume)+i]
		if line.LineID <= 0 {
			return contracts.StockPosted{}, fmt.Errorf("inventory: a production produces for line id %d", line.LineID)
		}
		to := line.ToLocationID
		lines = append(lines, engineLine{index: len(p.Consume) + i, variantID: line.VariantID, to: &to, quantity: q, lotID: line.LotID})
	}
	kind := movementKindProduction
	if p.Unbuild {
		kind = movementKindUnbuild
	}
	hash, err := requestHash(kind, productionBody{Source: doc, Reserving: src, SiteID: p.SiteID, Consume: p.Consume, Produce: p.Produce,
		ExpiredOverride: p.ExpiredOverride, Unbuild: p.Unbuild})
	if err != nil {
		return contracts.StockPosted{}, err
	}
	display := p.PostedByDisplay
	if utf8.RuneCountInString(display) > 200 {
		display = string([]rune(display)[:200])
	}
	header := movementHeader{
		clientRequestID: uuid.NewSHA1(productionKeySpace, []byte(fmt.Sprintf("%s\x00%s\x00%d", doc.Module, doc.Kind, doc.ID))),
		requestHash:     hash, siteID: p.SiteID, kind: kind,
		poster: actor{UserID: p.PostedByUserID, Display: display},
		// The source line is the reserving document's id — the order the
		// record produces for — so the movement links to it (M5).
		source: &movementSource{module: doc.Module, kind: doc.Kind, id: strconv.FormatInt(doc.ID, 10), lineID: ptr(strconv.FormatInt(src.ID, 10))},
	}
	txq := store.New(tx)
	// A replay first, before the site or the variants are judged again (I14).
	if res, done, err := replayOf(ctx, txq, header); done || err != nil {
		if err != nil {
			return contracts.StockPosted{}, issueError(err, lineOf, n)
		}
		return l.replayed(ctx, txq, res.movementID, lines)
	}
	// A plant need not fulfil orders; it need only be active (M3).
	if _, err := fulfillingSite(ctx, txq, p.SiteID, false); err != nil {
		return contracts.StockPosted{}, err
	}
	ids := make([]int32, 0, n)
	for i := range n {
		id, _ := lineOf(i)
		ids = append(ids, id)
	}
	// Unknown variants and services are refused on both sides; a
	// Discontinued variant is still produced and consumed (M1).
	_, failures, err := l.stockedVariants(ctx, tx, ids, lineOf, n)
	if err != nil {
		return contracts.StockPosted{}, err
	}
	if len(failures) > 0 {
		return contracts.StockPosted{}, refusalOf(failures)
	}
	// An unbuild's consume lines may take held and expired lots (M123).
	res, err := l.s.post(ctx, tx, txq, header, lines, postOptions{today: p.Today, takes: !p.Unbuild, expiredOverride: p.ExpiredOverride})
	if err != nil {
		return contracts.StockPosted{}, issueError(err, lineOf, n)
	}
	if res.replay {
		return l.replayed(ctx, txq, res.movementID, lines)
	}
	if len(consumed) > 0 {
		if err := l.consumeReservations(ctx, txq, src, p.SiteID, consumed); err != nil {
			return contracts.StockPosted{}, err
		}
	}
	var quarantined []int64
	if p.Unbuild {
		if quarantined, err = l.quarantineRestored(ctx, tx, txq, doc, p, res.picks); err != nil {
			return contracts.StockPosted{}, err
		}
	}
	return contracts.StockPosted{MovementID: res.movementID, Picks: res.picks, Quarantined: quarantined}, nil
}

// quarantineRestored is an unbuild's hold on what it restores (manufacturing
// phase 2 M123): when a lot it consumed holds an active hold, every lot it
// restores to gets a quarantine hold from the unbuild, with the consumed
// lot's cause — a block's first — so held material does not come back
// usable. It runs inside the same Produce, after the posting, and answers
// the lots it held, in id order.
func (l ledger) quarantineRestored(ctx context.Context, tx pgx.Tx, txq *store.Queries, doc contracts.StockSource, p contracts.StockProduction,
	picks []contracts.StockPick) ([]int64, error) {
	var consumed []int64
	for _, c := range p.Consume {
		if c.LotID != 0 {
			consumed = append(consumed, c.LotID)
		}
	}
	for _, pk := range picks {
		consumed = append(consumed, pk.LotID)
	}
	var restored []int64
	for _, pr := range p.Produce {
		if pr.LotID != 0 && !slices.Contains(restored, pr.LotID) {
			restored = append(restored, pr.LotID)
		}
	}
	if len(consumed) == 0 || len(restored) == 0 {
		return nil, nil
	}
	held, err := txq.ActiveHoldsOfLots(ctx, consumed)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the holds of the lots an unbuild took: %w", err)
	}
	if len(held) == 0 {
		return nil, nil
	}
	slices.Sort(restored)
	holds := make([]contracts.LotHold, 0, len(restored))
	by := contracts.LotActor{UserID: p.PostedByUserID, Display: p.PostedByDisplay}
	for _, id := range restored {
		holds = append(holds, contracts.LotHold{LotID: id, Kind: "quarantine", Cause: held[0].Cause, By: by,
			Note: "Restored by an unbuild of a held lot"})
	}
	if err := l.s.holdLots(ctx, tx, txq, sourceText(doc), holds, restored); err != nil {
		return nil, err
	}
	return restored, nil
}

// issueLineOf is an issue's lineOf for issueError: line i's variant and line
// id.
func issueLineOf(lines []contracts.IssueLine) func(i int) (int32, int64) {
	return func(i int) (int32, int64) { return lines[i].VariantID, lines[i].LineID }
}

// issueError is the engine's refusal of an issue or a production as the
// contract's: each failing line's code as a StockRefusal code, the line
// named by lineOf from the engine's index among n. A refusal that is no
// line's — an unknown site — or a code the contract has no
// word for is a failure.
func issueError(err error, lineOf func(i int) (int32, int64), n int) error {
	var r *refusal
	if !errors.As(err, &r) {
		return err
	}
	// A document posted before with another body is the contract's own
	// refusal, so a caller tells it from a failure.
	if r.problem.Code != nil && *r.problem.Code == codeClientRequestReused {
		return siteRefusal(contracts.StockDocumentReused, "The document was posted before with other lines or quantities; a new posting needs a new document.")
	}
	if r.problem.Lines == nil || len(*r.problem.Lines) == 0 {
		return err
	}
	var out []contracts.StockLineRefusal
	for _, f := range *r.problem.Lines {
		if int(f.Index) < 0 || int(f.Index) >= n {
			return err
		}
		variantID, lineID := lineOf(int(f.Index))
		var code string
		switch f.Code {
		case codeInsufficientStock:
			code = contracts.StockInsufficientStock
		case codeLocationNotFound, codeLocationArchived, codeMoveCrossesSites:
			code = contracts.StockLocationUnknown
		case codeVariantNotFound, codeVariantNotStockable:
			code = contracts.StockVariantNotStocked
		case codeQuantityInvalid:
			code = contracts.StockQuantityInvalid
		case codeLotRequired, codeLotNotAllowed, codeLotUnknown, codeSerialQuantity, codeLotExpired, codeLotBlocked, codeLotDatesDiffer:
			code = f.Code
		default:
			return err
		}
		out = append(out, contracts.StockLineRefusal{Code: code, VariantID: variantID, LineID: lineID, Detail: f.Detail, Index: int(f.Index),
			LotID: r.lots[int(f.Index)].lot, LocationID: r.lots[int(f.Index)].location})
	}
	return refusalOf(out)
}
