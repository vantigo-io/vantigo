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
// already holds them, so this waits for nothing), then the balances in
// (variant, location) order, then the reservations. A reserve locks the
// variants' balances at the site, so two reserves of one variant at one site
// — two orders confirmed for the last unit — serialise there, and the second
// reads what the first reserved. An issue posts through the engine
// (posting.go), whose own order ends at the balances, and then locks the
// document's reservations. A release locks only the reservations: it can
// only make more available, so it need not wait for anyone's balances.

// The movement kind an issue is posted as (design §12: the kinds are an open
// set of application constants, not a database enum).
const movementKindIssue = "issue"

// issueKeySpace is the UUID namespace an issue's client request id is made
// in from its document, so a second issue of one document is a replay.
var issueKeySpace = uuid.MustParse("6f1f2f0e-7f5c-4b8e-9a51-3d2b8c1e4a70")

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

// Available answers each variant's on hand less what is reserved, in total
// and per site where it lies or is reserved.
func (st stock) Available(ctx context.Context, variantIDs []int32, siteID *int64) ([]contracts.StockQuantity, error) {
	return st.read(ctx, variantIDs, siteID, true)
}

// Sites answers every site that is not archived, by code.
func (st stock) Sites(ctx context.Context) ([]contracts.StockSite, error) {
	rows, err := store.New(st.s.deps.Pool).StockSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("inventory: read the sites: %w", err)
	}
	out := make([]contracts.StockSite, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.StockSite{ID: r.ID, Code: r.Code, Name: r.Name, FulfilsOrders: r.FulfilsOrders, Active: r.Status == "active"})
	}
	return out, nil
}

// Locations answers, per variant, the site's active locations holding more
// than 0 of it, by full code.
func (st stock) Locations(ctx context.Context, variantIDs []int32, siteID int64) ([]contracts.StockLocation, error) {
	if err := contracts.ValidateStockVariants(variantIDs); err != nil {
		return nil, err
	}
	rows, err := store.New(st.s.deps.Pool).StockLocationsAt(ctx, store.StockLocationsAtParams{VariantIds: variantIDs, SiteID: siteID})
	if err != nil {
		return nil, fmt.Errorf("inventory: read the locations holding stock: %w", err)
	}
	out := make([]contracts.StockLocation, 0, len(rows))
	for _, r := range rows {
		out = append(out, contracts.StockLocation{VariantID: r.VariantID, LocationID: r.LocationID, FullCode: r.FullCode,
			Quantity: decimalText(ratFromText(r.OnHand))})
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
			if r := onHand[k]; r != nil {
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
// archived or not fulfilling orders. It locks nothing: a flag turned off
// under a reserve is the same race as one turned off just after it.
func fulfillingSite(ctx context.Context, txq *store.Queries, siteID int64) (store.GetSiteRow, error) {
	site, err := txq.GetSite(ctx, siteID)
	if noRows(err) {
		return site, siteRefusal(contracts.StockSiteUnknown, fmt.Sprintf("No site has the id %d.", siteID))
	}
	if err != nil {
		return site, fmt.Errorf("inventory: read site %d: %w", siteID, err)
	}
	switch {
	case site.Status != string(gen.Active):
		return site, siteRefusal(contracts.StockSiteNotFulfilling, fmt.Sprintf("The site %s is archived.", site.Code))
	case !site.FulfilsOrders:
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
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockVariantNotStocked, VariantID: variantID, LineID: lineID,
				Detail: fmt.Sprintf("No variant has the id %d.", variantID)})
		case v.ProductType != productTypeGoods:
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockVariantNotStocked, VariantID: variantID, LineID: lineID,
				Detail: fmt.Sprintf("%s is a service; only goods are kept in stock.", v.SKU)})
		}
	}
	return variants, failures, nil
}

// Reserve adds each line's quantity to its reservation at the site, when the
// site's available covers it.
func (l ledger) Reserve(ctx context.Context, tx pgx.Tx, src contracts.StockSource, siteID int64, lines []contracts.StockLine) error {
	if err := src.Validate(); err != nil {
		return err
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
	site, err := fulfillingSite(ctx, txq, siteID)
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
	figures, err := txq.SiteStockOf(ctx, store.SiteStockOfParams{VariantIds: sorted, SiteID: siteID})
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
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockInsufficientAvailable, VariantID: line.VariantID, LineID: line.LineID,
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
	if err := txq.AddReservations(ctx, p); err != nil {
		return fmt.Errorf("inventory: reserve for %s/%s/%d: %w", src.Module, src.Kind, src.ID, err)
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
// then site id.
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

// issueBody is what an issue's request hash is taken over: the site and the
// lines, as given.
type issueBody struct {
	Source contracts.StockSource
	SiteID int64
	Lines  []contracts.IssueLine
}

// Issue posts the issue as one movement through the engine and consumes the
// reservations of the lines it issued.
func (l ledger) Issue(ctx context.Context, tx pgx.Tx, src contracts.StockSource, issue contracts.StockIssue) (int64, error) {
	if err := src.Validate(); err != nil {
		return 0, err
	}
	doc := issue.Document
	if doc.IsZero() {
		doc = src
	}
	if err := doc.Validate(); err != nil {
		return 0, err
	}
	if len(issue.Lines) == 0 {
		return 0, errors.New("inventory: an issue has no lines")
	}
	lines := make([]engineLine, len(issue.Lines))
	issued := map[int64]*big.Rat{}
	for i, line := range issue.Lines {
		q, err := ledgerQuantity(line.Quantity)
		if err != nil {
			return 0, err
		}
		if line.LineID <= 0 {
			return 0, fmt.Errorf("inventory: an issue names line id %d", line.LineID)
		}
		from := line.FromLocationID
		lines[i] = engineLine{index: i, variantID: line.VariantID, from: &from, quantity: q}
		if issued[line.LineID] == nil {
			issued[line.LineID] = new(big.Rat)
		}
		issued[line.LineID].Add(issued[line.LineID], q)
	}
	hash, err := requestHash(movementKindIssue, issueBody{Source: doc, SiteID: issue.SiteID, Lines: issue.Lines})
	if err != nil {
		return 0, err
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
	// A replay first, before the site or the variants are judged again (I14).
	if res, done, err := replayOf(ctx, txq, header); done || err != nil {
		if err != nil {
			return 0, issueError(err, issue.Lines)
		}
		return res.movementID, nil
	}
	if _, err := fulfillingSite(ctx, txq, issue.SiteID); err != nil {
		return 0, err
	}
	ids := make([]int32, 0, len(issue.Lines))
	for _, line := range issue.Lines {
		ids = append(ids, line.VariantID)
	}
	_, failures, err := l.stockedVariants(ctx, tx, ids, func(i int) (int32, int64) { return issue.Lines[i].VariantID, issue.Lines[i].LineID }, len(issue.Lines))
	if err != nil {
		return 0, err
	}
	if len(failures) > 0 {
		return 0, refusalOf(failures)
	}
	res, err := l.s.post(ctx, tx, txq, header, lines, postOptions{})
	if err != nil {
		return 0, issueError(err, issue.Lines)
	}
	if res.replay {
		return res.movementID, nil
	}
	// The reservations of the lines issued, at the site, consumed as far as
	// they go.
	rows, err := txq.LockSourceReservations(ctx, sourceParams(src))
	if err != nil {
		return 0, fmt.Errorf("inventory: lock the reservations of %s/%s/%d: %w", src.Module, src.Kind, src.ID, err)
	}
	left := make([]*big.Rat, len(rows))
	for i, r := range rows {
		left[i] = ratFromNumeric(r.Quantity)
		if r.SiteID != issue.SiteID {
			continue
		}
		lineID, err := strconv.ParseInt(r.SourceLineID, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("inventory: reservation %d has the line id %q: %w", r.ID, r.SourceLineID, err)
		}
		if q := issued[lineID]; q != nil {
			take := minRat(left[i], q)
			left[i].Sub(left[i], take)
			q.Sub(q, take)
		}
	}
	if err := l.writeReservations(ctx, txq, rows, left); err != nil {
		return 0, err
	}
	return res.movementID, nil
}

// issueError is the engine's refusal of an issue as the contract's: each
// failing line's code as a StockRefusal code. A refusal that is no line's — a
// reused key, an unknown site — or a code the contract has no word for is a
// failure.
func issueError(err error, lines []contracts.IssueLine) error {
	var r *refusal
	if !errors.As(err, &r) || r.problem.Lines == nil || len(*r.problem.Lines) == 0 {
		return err
	}
	var out []contracts.StockLineRefusal
	for _, f := range *r.problem.Lines {
		if int(f.Index) < 0 || int(f.Index) >= len(lines) {
			return err
		}
		line := lines[f.Index]
		var code string
		switch f.Code {
		case codeInsufficientStock:
			code = contracts.StockInsufficientStock
		case codeLocationNotFound, codeLocationArchived, codeMoveCrossesSites:
			code = contracts.StockLocationUnknown
		case codeVariantNotFound, codeVariantNotStockable:
			code = contracts.StockVariantNotStocked
		default:
			return err
		}
		out = append(out, contracts.StockLineRefusal{Code: code, VariantID: line.VariantID, LineID: line.LineID, Detail: f.Detail})
	}
	return refusalOf(out)
}
