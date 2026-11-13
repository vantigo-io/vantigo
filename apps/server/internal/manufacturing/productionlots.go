package manufacturing

// Lots on a production (manufacturing phase 2 M80–M86). A production of a
// tracked product names the lot its good output goes into — the order's lot
// pattern rendered, or the number sent — or, for a serial product, one serial
// per unit; Manufacturing finds or creates them through StockLedger.EnsureLots
// after the order's lock and before Produce (M86 step 7a), and passes each on
// its produce line. A tracked component is consumed from the lots the request
// names, each its own consume line; a tracked component sent without lots is
// consumed with AutoLot, Inventory taking the lots FEFO under its own locks,
// and the lots it took are written back. production_line_lots keeps the split
// and production_outputs the lots made, with their numbers as they were.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
)

const (
	maxLotRows       = 100
	maxSupplierLot   = 40
	sqlStateDeadlock = "40P01"
	// maxCounterSkips is how many counted lot numbers ({N}) a production
	// skips because another document's lot has them (M57) before it answers
	// lot_exists.
	maxCounterSkips = 100
)

// lotRow is one lot a production line takes from (M82): a lot at a location,
// what is consumed and scrapped of it there, and whether the person chose it
// although it is expired.
type lotRow struct {
	lotID, locationID  int64
	consumed, scrapped *big.Rat
	allowExpired       bool
}

// total is what the row takes off the shelf.
func (r lotRow) total() *big.Rat { return new(big.Rat).Add(r.consumed, r.scrapped) }

// outputLotValues is the output lot as the request sends it (M80): every
// field optional.
type outputLotValues struct {
	number, supplierLot            string
	production, expiry, bestBefore *time.Time
}

// serialValues is the output's serial numbers as the request sends them
// (M81): a first counted up, or one number per unit.
type serialValues struct {
	first   string
	numbers []string
}

// dayText is a date as YYYY-MM-DD, "" for none.
func dayText(d *time.Time) string {
	if d == nil {
		return ""
	}
	return d.Format(time.DateOnly)
}

// dayOf is an API date as a calendar date at midnight UTC, nil for none.
func dayOf(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	return &t
}

// dateOut is a calendar date as the API's, nil for none.
func dateOut(d *time.Time) *openapi_types.Date {
	if d == nil {
		return nil
	}
	return &openapi_types.Date{Time: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)}
}

// pgDay is a calendar date as a date column's value, NULL for none.
func pgDay(d *time.Time) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

// dayOfColumn is a date column's value, nil for NULL.
func dayOfColumn(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

// judgeLotRows is a line's lots (M82): each lot and location once, a
// quantity of 0 or more with at most three decimals for each of consumed and
// scrapped, more than 0 together, and the rows summing to the line's consumed
// and scrap (picks_mismatch). Nil when none were sent.
func judgeLotRows(f *fieldErrors, at string, in gen.ManufacturingProductionLineInput, line productionLine) ([]lotRow, bool) {
	proposed := in.LotsProposed != nil && *in.LotsProposed
	if in.Lots == nil {
		return nil, proposed
	}
	if len(*in.Lots) > maxLotRows {
		f.add(at+"lots", "At most 100 lots")
		return nil, proposed
	}
	rows := make([]lotRow, 0, len(*in.Lots))
	seen := map[[2]int64]bool{}
	consumed, scrapped := new(big.Rat), new(big.Rat)
	bad := false
	for j, l := range *in.Lots {
		rowAt := fmt.Sprintf("%slots[%d].", at, j)
		r := lotRow{lotID: l.LotId, locationID: l.LocationId, consumed: new(big.Rat), scrapped: new(big.Rat),
			allowExpired: l.AllowExpired != nil && *l.AllowExpired}
		if l.LotId < 1 {
			f.add(rowAt+"lotId", "Not a lot")
			bad = true
		}
		if l.LocationId < 1 {
			f.add(rowAt+"locationId", "Not a location")
			bad = true
		}
		key := [2]int64{l.LotId, l.LocationId}
		if seen[key] {
			f.add(rowAt+"lotId", "Two rows name this lot at this location")
			bad = true
		}
		seen[key] = true
		if l.Consumed != nil {
			q, msg := parseDecimal(*l.Consumed, quantityPlaces, maxQuantity)
			f.addCoded(rowAt+"consumed", codeQuantityInvalid, msg)
			if q == nil {
				bad = true
			} else {
				r.consumed = q
			}
		}
		if l.Scrapped != nil {
			q, msg := parseDecimal(*l.Scrapped, quantityPlaces, maxQuantity)
			f.addCoded(rowAt+"scrapped", codeQuantityInvalid, msg)
			if q == nil {
				bad = true
			} else {
				r.scrapped = q
			}
		}
		if !bad && r.total().Sign() == 0 {
			f.addCoded(rowAt+"consumed", codeQuantityInvalid, "A lot row consumes or scraps more than 0")
			bad = true
		}
		consumed.Add(consumed, r.consumed)
		scrapped.Add(scrapped, r.scrapped)
		rows = append(rows, r)
	}
	if bad {
		return rows, proposed
	}
	wantConsumed := new(big.Rat)
	if line.consumed != nil {
		wantConsumed = line.consumed
	}
	if line.scrap != nil && (consumed.Cmp(wantConsumed) != 0 || scrapped.Cmp(line.scrap) != 0) {
		f.addCoded(at+"lots", codePicksMismatch, fmt.Sprintf("The lots consume %s and scrap %s; the line consumes %s and scraps %s.",
			text(consumed, quantityPlaces), text(scrapped, quantityPlaces), text(wantConsumed, quantityPlaces), text(line.scrap, quantityPlaces)))
	}
	return rows, proposed
}

// judgeOutputLot is the output lot's shape (M80), its errors at prefix and
// field: a production's outputLot, a by-product's or a component's lot.
func judgeOutputLot(f *fieldErrors, prefix, field string, in *gen.ManufacturingOutputLotInput) *outputLotValues {
	if in == nil {
		return nil
	}
	v := &outputLotValues{production: dayOf(in.ProductionDate), expiry: dayOf(in.Expiry), bestBefore: dayOf(in.BestBefore)}
	if in.Number != nil {
		v.number = strings.TrimSpace(*in.Number)
		if v.number != "" && !lotNumberPattern.MatchString(v.number) {
			f.add(prefix+field+".number", "1–20 characters A–Z, a–z, 0–9 and . _ / -")
		}
	}
	if in.SupplierLot != nil {
		v.supplierLot = strings.TrimSpace(*in.SupplierLot)
		if len([]rune(v.supplierLot)) > maxSupplierLot {
			f.add(prefix+field+".supplierLot", "At most 40 characters")
		}
	}
	return v
}

// judgeSerials is the output serials' shape (M81): first or numbers, not
// both; each number a lot number, once.
func judgeSerials(f *fieldErrors, prefix string, in *gen.ManufacturingOutputSerialsInput) *serialValues {
	if in == nil {
		return nil
	}
	v := &serialValues{}
	if in.First != nil {
		v.first = strings.TrimSpace(*in.First)
	}
	if in.Numbers != nil {
		if len(*in.Numbers) == 0 {
			f.add(prefix+"outputSerials.numbers", "At least one serial")
		}
		seen := map[string]bool{}
		for i, n := range *in.Numbers {
			n = strings.TrimSpace(n)
			at := fmt.Sprintf("%soutputSerials.numbers[%d]", prefix, i)
			switch {
			case !lotNumberPattern.MatchString(n):
				f.add(at, "1–20 characters A–Z, a–z, 0–9 and . _ / -")
			case seen[strings.ToLower(n)]:
				f.add(at, "Named twice")
			}
			seen[strings.ToLower(n)] = true
			v.numbers = append(v.numbers, n)
		}
		if len(v.numbers) > maxSerialOutputs {
			f.add(prefix+"outputSerials.numbers", "At most 500 serials")
		}
	}
	if v.first != "" && in.Numbers != nil {
		f.add(prefix+"outputSerials", "Send first or numbers, not both")
	}
	return v
}

// consumeRef is what one ledger consume line stands for: its component line
// and, for a lot the request named, the row's index in the line's lots (-1
// for an AutoLot or untracked line), and whether the row was proposed.
type consumeRef struct {
	componentID int64
	row         int
	auto        bool
	proposed    bool
}

// outputLot is a lot a production makes, as EnsureLots is asked for it;
// counted is a number the order's {N} counter handed out.
type outputLot struct {
	number   string
	serial   bool
	counted  bool
	quantity *big.Rat
	lotID    int64
}

// outputLots is the lots a production's good output goes into (M80, M81),
// for the order's product as LockVariants answered it: none for an untracked
// product; the request's number or the order's pattern rendered on the
// production date for a lot-tracked one; one serial per unit for a serial
// one. A lot or serials sent for an untracked product is lot_not_allowed.
// A counted number and every serial must be a new lot (MustCreate); a
// number typed for a lot-tracked product names its lot (M57). at is where
// the production sits in the body.
func (s *server) outputLots(ctx context.Context, txq *store.Queries, o store.ManufacturingOrder, e contracts.VariantEntry, v productionValues,
	today time.Time, at string) ([]outputLot, []contracts.NewLot, error) {
	field := func(name, code, msg string) error { return refuseField(productionTitle, at+name, code, msg) }
	switch e.Tracking {
	case contracts.TrackingLot, contracts.TrackingSerial:
	default:
		if v.outputLot != nil || v.serials != nil {
			return nil, nil, field("outputLot", codeLotNotAllowed, fmt.Sprintf("%s is not tracked in lots or serials.", e.SKU))
		}
		return nil, nil, nil
	}
	if v.quantity.Sign() == 0 {
		return nil, nil, nil
	}
	mo := orderNumber(o.Number, o.Suffix)
	lot := contracts.NewLot{VariantID: o.VariantID, ProductionDate: today}
	if l := v.outputLot; l != nil {
		lot.SupplierLot, lot.Expiry, lot.BestBefore = l.supplierLot, l.expiry, l.bestBefore
		if l.production != nil {
			lot.ProductionDate = *l.production
		}
	}
	if e.Tracking == contracts.TrackingLot {
		if v.serials != nil {
			return nil, nil, field("outputSerials", codeLotNotAllowed, fmt.Sprintf("%s is tracked in lots, not serials.", e.SKU))
		}
		number, counted := "", false
		if v.outputLot != nil {
			number = v.outputLot.number
		}
		if number == "" {
			var err error
			if number, counted, err = renderLotNumber(ctx, txq, o.LotPattern, mo, lot.ProductionDate, e.SKU, at); err != nil {
				return nil, nil, err
			}
		}
		lot.Number, lot.MustCreate = number, counted
		return []outputLot{{number: number, counted: counted, quantity: v.quantity}}, []contracts.NewLot{lot}, nil
	}
	// A serial product: one serial per whole unit (M81).
	if v.outputLot != nil && v.outputLot.number != "" {
		return nil, nil, field("outputLot.number", codeLotNotAllowed, "A serial product's units are numbered by outputSerials.")
	}
	if !v.quantity.IsInt() {
		return nil, nil, field("quantity", codeSerialQuantity, "A serial product is produced in whole units, one serial each.")
	}
	if !v.quantity.Num().IsInt64() || v.quantity.Num().Int64() > maxSerialOutputs {
		return nil, nil, field("quantity", codeSerialQuantity, "At most 500 serials in one production.")
	}
	count := int(v.quantity.Num().Int64())
	var numbers []string
	switch {
	case v.serials != nil && v.serials.numbers != nil:
		if len(v.serials.numbers) != count {
			return nil, nil, field("outputSerials.numbers", codeSerialQuantity,
				fmt.Sprintf("%d serial numbers for %d units: one per unit.", len(v.serials.numbers), count))
		}
		numbers = v.serials.numbers
	default:
		first := ""
		if v.serials != nil {
			first = v.serials.first
		}
		if first == "" {
			before, err := txq.SerialOutputNumbersOfOrder(ctx, o.ID)
			if err != nil {
				return nil, nil, err
			}
			first = defaultSerialFirst(mo, before)
		}
		var msg string
		if numbers, msg = countUpSerials(first, count); msg != "" {
			return nil, nil, field("outputSerials.first", codeSerialQuantity, msg)
		}
	}
	outs := make([]outputLot, 0, count)
	lots := make([]contracts.NewLot, 0, count)
	for _, n := range numbers {
		l := lot
		l.Number, l.MustCreate = n, true
		lots = append(lots, l)
		outs = append(outs, outputLot{number: n, serial: true, quantity: big.NewRat(1, 1)})
	}
	return outs, lots, nil
}

// ensureOutputLots is step 7a (M80, M81, M86): the output's lots found or
// made by EnsureLots, their ids set on outs. A counted number another
// document's lot has already — one a person typed — is skipped under the
// counter's row lock and the next taken, each try in a savepoint so a refused
// one leaves nothing behind, at most maxCounterSkips times. Inventory's
// refusal of the output is the 400 of the field it names (outputRefusal).
func (s *server) ensureOutputLots(ctx context.Context, tx pgx.Tx, txq *store.Queries, ledger contracts.StockLedger, o store.ManufacturingOrder,
	e contracts.VariantEntry, prep *preparedProduction, productionID int64, outs []outputLot, lots []contracts.NewLot) error {
	src := productionSource(productionID)
	counted := len(outs) == 1 && outs[0].counted
	for skips := 0; ; skips++ {
		var ids []int64
		var err error
		if counted {
			ids, err = ensureInSavepoint(ctx, tx, ledger, src, prep.today, lots)
		} else {
			ids, err = ledger.EnsureLots(ctx, tx, src, prep.today, lots)
		}
		var r *contracts.StockRefusal
		if errors.As(err, &r) && counted && skips < maxCounterSkips && onlyCode(r, contracts.StockLotExists) {
			number, _, err := renderLotNumber(ctx, txq, o.LotPattern, orderNumber(o.Number, o.Suffix), lots[0].ProductionDate, e.SKU, prep.prefix)
			if err != nil {
				return err
			}
			lots[0].Number, outs[0].number = number, number
			continue
		}
		if r != nil {
			return outputRefusal(r, prep.prefix, prep.v, outs)
		}
		if err != nil {
			return fmt.Errorf("manufacturing: ensure production %d's lots: %w", productionID, err)
		}
		for i := range outs {
			outs[i].lotID = ids[i]
		}
		return nil
	}
}

// ensureInSavepoint is EnsureLots in a savepoint of tx, rolled back when it
// fails, so a refused try creates no lot and holds no lot's lock.
func ensureInSavepoint(ctx context.Context, tx pgx.Tx, ledger contracts.StockLedger, src contracts.StockSource, today time.Time,
	lots []contracts.NewLot) ([]int64, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := ledger.EnsureLots(ctx, sp, src, today, lots)
	if err != nil {
		if rerr := sp.Rollback(ctx); rerr != nil {
			return nil, errors.Join(err, rerr)
		}
		return nil, err
	}
	return ids, sp.Commit(ctx)
}

// onlyCode reports whether every line of r is refused code.
func onlyCode(r *contracts.StockRefusal, code string) bool {
	if len(r.Lines) == 0 {
		return r.Code == code
	}
	for _, l := range r.Lines {
		if l.Code != code {
			return false
		}
	}
	return true
}

// outputRefusal is Inventory's refusal of a production's output lots: the
// 400 of the field each line names — a stated date that differs from the
// existing lot's (lot_dates_differ) at outputLot, a number taken
// (lot_exists) at outputLot.number, a serial's at outputSerials.numbers[i] or
// outputSerials.first — and any other the 409 passed through.
func outputRefusal(r *contracts.StockRefusal, at string, v productionValues, outs []outputLot) error {
	lines := r.Lines
	if len(lines) == 0 {
		lines = []contracts.StockLineRefusal{{Code: r.Code, Detail: r.Detail}}
	}
	var f fieldErrors
	for _, l := range lines {
		switch l.Code {
		case contracts.StockLotDatesDiffer, contracts.StockSerialQuantity, contracts.StockLotExists:
		default:
			return stockRefusal(r, nil)
		}
		field := at + "outputLot"
		switch {
		case l.Index < 0 || l.Index >= len(outs):
		case outs[l.Index].serial && v.serials != nil && v.serials.numbers != nil:
			field = fmt.Sprintf("%soutputSerials.numbers[%d]", at, l.Index)
		case outs[l.Index].serial:
			field = at + "outputSerials.first"
		case l.Code == contracts.StockLotExists:
			field = at + "outputLot.number"
		}
		f.addCoded(field, l.Code, l.Detail)
	}
	return refuseInvalid(productionTitle, f)
}

// splitAuto spreads what a component line consumed and scrapped over the
// lots Inventory took for it, consumed first, in the order taken.
func splitAuto(consumed, scrapped *big.Rat, picks []contracts.StockPick) []lotRow {
	c, sc := new(big.Rat).Set(consumed), new(big.Rat).Set(scrapped)
	var out []lotRow
	for _, p := range picks {
		q := mustRat(p.Quantity)
		row := lotRow{lotID: p.LotID, locationID: p.LocationID, consumed: new(big.Rat), scrapped: new(big.Rat)}
		take := minRat(q, c)
		row.consumed.Set(take)
		c.Sub(c, take)
		q.Sub(q, take)
		take = minRat(q, sc)
		row.scrapped.Set(take)
		sc.Sub(sc, take)
		out = append(out, row)
	}
	return out
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) < 0 {
		return new(big.Rat).Set(a)
	}
	return new(big.Rat).Set(b)
}

// lotInfo is what a production keeps of a lot: its number and expiry, from
// Stock.Lots read before the transaction.
type lotInfo struct {
	number string
	expiry *time.Time
}

// errLotsMoved is a post that took a lot it read nothing of before the
// transaction (a lot received meanwhile): the transaction rolls back and the
// post reads the stock again, as for errResplit.
var errLotsMoved = errors.New("manufacturing: a lot taken was not read before the transaction; read again")

// writeLineLots writes the lots a production took per component line (M82):
// the rows the request named, and the lots Inventory took for the AutoLot
// lines, consumed first. A lot it has no number of is errLotsMoved, on every
// attempt: a row is never written without its number.
func writeLineLots(ctx context.Context, txq *store.Queries, productionID int64, lines []productionLine, refs []consumeRef,
	posted contracts.StockPosted, known map[int64]lotInfo) (int, error) {
	picksOf := map[int64][]contracts.StockPick{}
	for _, p := range posted.Picks {
		if p.Index < len(refs) && refs[p.Index].auto {
			picksOf[refs[p.Index].componentID] = append(picksOf[refs[p.Index].componentID], p)
		}
	}
	n := 0
	for _, l := range lines {
		rows := l.lots
		if rows == nil {
			consumed := new(big.Rat)
			if l.consumed != nil {
				consumed = l.consumed
			}
			rows = splitAuto(consumed, l.scrap, picksOf[l.componentID])
		}
		for _, r := range rows {
			if r.total().Sign() == 0 {
				continue
			}
			info, ok := known[r.lotID]
			if !ok {
				return 0, errLotsMoved
			}
			if err := txq.InsertProductionLineLot(ctx, store.InsertProductionLineLotParams{
				ProductionID: productionID, ComponentID: l.componentID, LotID: r.lotID, LocationID: r.locationID,
				LotNumber: info.number, Expiry: pgDay(info.expiry), Consumed: numeric(r.consumed, quantityPlaces),
				Scrapped: numeric(r.scrapped, quantityPlaces), AllowExpired: r.allowExpired,
			}); err != nil {
				return 0, err
			}
			n++
		}
	}
	return n, nil
}

// errRepropose is a post refused insufficient_stock or lot_blocked on lots
// only the draft proposed (M65a): the transaction rolls back and the lines
// named are proposed again from the stock then, once. refusal is the
// refusal's answer, given when the stock no longer covers a line.
type errRepropose struct {
	components map[int64]bool
	refusal    error
}

func (e *errRepropose) Error() string {
	return "manufacturing: proposed lots were refused; propose them again"
}

// reproposable is the components to propose again when every line of r is
// a proposed lot row refused insufficient_stock or lot_blocked; nil
// otherwise.
func reproposable(r *contracts.StockRefusal, refs []consumeRef) map[int64]bool {
	lines := r.Lines
	if len(lines) == 0 {
		return nil
	}
	out := map[int64]bool{}
	for _, l := range lines {
		if l.Code != contracts.StockInsufficientStock && l.Code != contracts.StockLotBlocked {
			return nil
		}
		if l.Index < 0 || l.Index >= len(refs) || !refs[l.Index].proposed || refs[l.Index].row < 0 {
			return nil
		}
		out[refs[l.Index].componentID] = true
	}
	return out
}

// repropose proposes the lots of the named lines again (M65a), FEFO from
// Stock.Lots read now, at the line's location, else the order's component
// location, else the whole site; consumed first, then scrap. When the stock
// no longer covers a line in full, the post is refused as it was the first
// time (again.refusal): a part of a line is never posted for the whole.
func (s *server) repropose(ctx context.Context, prep *preparedProduction, componentLocation *int64, again *errRepropose) error {
	components := again.components
	byComponent, err := store.New(s.deps.Pool).ListOrderComponents(ctx, prep.orderID)
	if err != nil {
		return err
	}
	variantOf := map[int64]int32{}
	var ids []int32
	for _, c := range byComponent {
		variantOf[c.ID] = c.VariantID
		if components[c.ID] {
			ids = append(ids, c.VariantID)
		}
	}
	lots, err := s.stockLots(ctx, ids, prep.siteID)
	if err != nil {
		return err
	}
	pool := newLotPool(lots)
	for i, l := range prep.v.lines {
		if !components[l.componentID] {
			continue
		}
		at := l.from
		if at == nil {
			at = componentLocation
		}
		consumed := new(big.Rat)
		if l.consumed != nil {
			consumed = l.consumed
		}
		total := new(big.Rat).Add(consumed, l.scrap)
		picks, short, err := pool.propose(variantOf[l.componentID], total, at, prep.today)
		if err != nil {
			return err
		}
		if short.Sign() > 0 {
			return again.refusal
		}
		prep.v.lines[i].lots = splitAuto(consumed, l.scrap, picks)
	}
	return nil
}

// lotPool is the site's lots of some variants as proposals draw on them:
// what each holds at each location, less what earlier proposals took.
type lotPool struct {
	rows map[int32][]contracts.StockLot
}

func newLotPool(lots map[int32][]contracts.StockLot) *lotPool {
	p := &lotPool{rows: map[int32][]contracts.StockLot{}}
	for v, rows := range lots {
		p.rows[v] = slices.Clone(rows)
	}
	return p
}

// propose is the FEFO proposal of qty of the variant (contracts.ProposeLots,
// M65) at location at (any when nil), taken from the pool, as picks with
// their indexes unset; short is what it could not cover.
func (p *lotPool) propose(variant int32, qty *big.Rat, at *int64, today time.Time) ([]contracts.StockPick, *big.Rat, error) {
	var rows []contracts.StockLot
	for _, r := range p.rows[variant] {
		if at == nil || r.LocationID == *at {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 || qty.Sign() <= 0 {
		return nil, new(big.Rat).Set(qty), nil
	}
	picks, short, err := contracts.ProposeLots(rows, text(qty, quantityPlaces), today)
	if err != nil {
		return nil, nil, fmt.Errorf("manufacturing: propose the lots of variant %d: %w", variant, err)
	}
	out := make([]contracts.StockPick, 0, len(picks))
	for _, pk := range picks {
		out = append(out, contracts.StockPick{LotID: pk.LotID, LocationID: pk.LocationID, Quantity: pk.Quantity})
		p.take(variant, pk.LotID, pk.LocationID, mustRat(pk.Quantity))
	}
	left := new(big.Rat)
	if short != "" {
		left = mustRat(short)
	}
	return out, left, nil
}

// take lowers what a lot holds at a location by qty; a row left at 0 is no
// longer proposed.
func (p *lotPool) take(variant int32, lot, location int64, qty *big.Rat) {
	rows := p.rows[variant]
	for i := range rows {
		if rows[i].LotID != lot || rows[i].LocationID != location {
			continue
		}
		left := new(big.Rat).Sub(mustRat(rows[i].Quantity), qty)
		if left.Sign() <= 0 {
			p.rows[variant] = slices.Delete(rows, i, i+1)
			return
		}
		rows[i].Quantity = text(left, quantityPlaces)
		return
	}
}

// usableLocations is where each component lies as a split of its
// consumption reads it (M22a): for a tracked component, what its lots that
// are neither held nor expired on today hold at each location, in the
// locations' order — AutoLot takes no other lot (M65a); for an untracked
// one, the locations as read.
func usableLocations(held map[int32][]contracts.StockLocation, lots map[int32][]contracts.StockLot, today time.Time) map[int32][]contracts.StockLocation {
	out := make(map[int32][]contracts.StockLocation, len(held))
	for variant, locations := range held {
		rows, tracked := lots[variant]
		if !tracked {
			out[variant] = locations
			continue
		}
		usable := map[int64]*big.Rat{}
		for _, r := range rows {
			if r.Status != contracts.LotAvailable || contracts.LotExpired(r.Expiry, today) {
				continue
			}
			if usable[r.LocationID] == nil {
				usable[r.LocationID] = new(big.Rat)
			}
			usable[r.LocationID].Add(usable[r.LocationID], mustRat(r.Quantity))
		}
		for _, l := range locations {
			if q := usable[l.LocationID]; q != nil {
				l.Quantity = text(q, quantityPlaces)
				out[variant] = append(out[variant], l)
			}
		}
	}
	return out
}

// lotInfos is the number and expiry of every lot of lots.
func lotInfos(lots map[int32][]contracts.StockLot) map[int64]lotInfo {
	out := map[int64]lotInfo{}
	for _, rows := range lots {
		for _, r := range rows {
			out[r.LotID] = lotInfo{number: r.Number, expiry: r.Expiry}
		}
	}
	return out
}

// isDeadlock reports whether err is PostgreSQL's deadlock_detected (40P01).
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateDeadlock
}

// lotsMovedConflict is the 409 of a production whose lots taken were still
// not all read on its last attempt: other writers kept changing them.
func lotsMovedConflict() error {
	return conflict(codeRevisionConflict, "Revision conflict",
		"The lots the production took kept changing under it. Nothing was changed; try again.")
}

// deadlockConflict is the 409 of a production that deadlocked twice (M86).
func deadlockConflict() error {
	return conflict(codeRevisionConflict, "Revision conflict",
		"The production met another write on the same lots twice. Nothing was changed; try again.")
}

// stockLots is, per variant, the site's lots holding it, by location, in
// FEFO order, before any transaction (M65, M82).
func (s *server) stockLots(ctx context.Context, variantIDs []int32, siteID int64) (map[int32][]contracts.StockLot, error) {
	out := map[int32][]contracts.StockLot{}
	ids := slices.Compact(slices.Sorted(slices.Values(variantIDs)))
	if len(ids) == 0 {
		return out, nil
	}
	if err := noteContractCall(ctx, "Stock.Lots"); err != nil {
		return nil, err
	}
	if s.deps.Stock == nil {
		return nil, errDependencyUnavailable
	}
	for batch := range slices.Chunk(ids, contracts.MaxStockVariants) {
		rows, err := s.deps.Stock.Lots(ctx, batch, siteID)
		if err != nil {
			return nil, fmt.Errorf("manufacturing: read the lots of the components: %w", err)
		}
		for _, r := range rows {
			out[r.VariantID] = append(out[r.VariantID], r)
		}
	}
	return out, nil
}

// isTracked reports whether a catalog entry is tracked in lots or serials.
func isTracked(e contracts.VariantEntry) bool {
	return e.Tracking == contracts.TrackingLot || e.Tracking == contracts.TrackingSerial
}

// trackingOf is a catalog entry's tracking as the API answers it.
func trackingOf(e contracts.VariantEntry) gen.ManufacturingTracking {
	switch e.Tracking {
	case contracts.TrackingLot:
		return gen.Lot
	case contracts.TrackingSerial:
		return gen.Serial
	}
	return gen.None
}

// trackedVariants is the variants of entries tracked in lots or serials.
func trackedVariants(entries map[int32]contracts.VariantEntry) []int32 {
	var out []int32
	for _, id := range slices.Sorted(maps.Keys(entries)) {
		if isTracked(entries[id]) {
			out = append(out, id)
		}
	}
	return out
}

// productionLotsOf is the lots of productions as the API answers them: per
// production, the lots made, and per production and component line, the
// lots taken.
func productionLotsOf(ctx context.Context, q *store.Queries, ids []int64) (map[int64][]gen.ManufacturingProductionOutput,
	map[[2]int64][]gen.ManufacturingProductionLineLot, error) {
	outputs, err := q.ProductionOutputs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	rows, err := q.ProductionLineLots(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	made := map[int64][]gen.ManufacturingProductionOutput{}
	for _, o := range outputs {
		made[o.ProductionID] = append(made[o.ProductionID], gen.ManufacturingProductionOutput{LotId: o.LotID, LotNumber: o.LotNumber,
			Serial: o.Serial, Quantity: numericText(o.Quantity, quantityPlaces)})
	}
	taken := map[[2]int64][]gen.ManufacturingProductionLineLot{}
	for _, r := range rows {
		k := [2]int64{r.ProductionID, r.ComponentID}
		taken[k] = append(taken[k], gen.ManufacturingProductionLineLot{LotId: r.LotID, LotNumber: r.LotNumber, Expiry: dateOut(dayOfColumn(r.Expiry)),
			LocationId: r.LocationID, Consumed: numericText(r.Consumed, quantityPlaces), Scrapped: numericText(r.Scrapped, quantityPlaces),
			AllowExpired: r.AllowExpired})
	}
	return made, taken, nil
}

// GetManufacturingOrderLots is the lots an order's productions made and
// consumed (M83), from Manufacturing's own rows.
func (s *server) GetManufacturingOrderLots(ctx context.Context, req gen.GetManufacturingOrderLotsRequestObject) (gen.GetManufacturingOrderLotsResponseObject, error) {
	out := gen.ManufacturingOrderLots{Outputs: []gen.ManufacturingOrderOutputLot{}, Components: []gen.ManufacturingOrderComponentLots{},
		ByProducts: []gen.ManufacturingProductionByproduct{}}
	var components []store.ManufacturingOrderComponent
	var outputs []store.ManufacturingProductionOutput
	var rows []store.ManufacturingProductionLineLot
	var byRows []store.ProductionByproductsRow
	var unbuilt []store.UnbuiltOfOrdersRow
	var mo store.ManufacturingOrder
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if mo, err = q.GetOrder(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		ids, err := q.ProductionIDsOfOrder(ctx, req.Id)
		if err != nil {
			return err
		}
		if components, err = q.ListOrderComponents(ctx, req.Id); err != nil {
			return err
		}
		if outputs, err = q.ProductionOutputs(ctx, ids); err != nil {
			return err
		}
		if byRows, err = q.ProductionByproducts(ctx, ids); err != nil {
			return err
		}
		if unbuilt, err = q.UnbuiltOfOrders(ctx, []int64{req.Id}); err != nil {
			return err
		}
		rows, err = q.ProductionLineLots(ctx, ids)
		return err
	})
	var r *refusal
	if errors.As(err, &r) {
		return gen.GetManufacturingOrderLots404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	// The product's tracking, from the catalog after the snapshot (M123).
	entries, err := s.catalogVariants(ctx, []int32{mo.VariantID})
	if err != nil {
		return answer(err, answers[gen.GetManufacturingOrderLotsResponseObject]{
			unavailable: func(p gen.ManufacturingProblem) gen.GetManufacturingOrderLotsResponseObject {
				return gen.GetManufacturingOrderLots503ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	out.Tracking = trackingOf(entries[mo.VariantID])
	made := map[int64]int{}
	for _, o := range outputs {
		i, ok := made[o.LotID]
		if !ok {
			made[o.LotID] = len(out.Outputs)
			out.Outputs = append(out.Outputs, gen.ManufacturingOrderOutputLot{LotId: o.LotID, LotNumber: o.LotNumber, Serial: o.Serial, Quantity: "0", Unbuilt: "0"})
			i = made[o.LotID]
		}
		row := &out.Outputs[i]
		row.Quantity = text(new(big.Rat).Add(mustRat(row.Quantity), ratOrZero(o.Quantity)), quantityPlaces)
		row.Productions++
	}
	for _, u := range unbuilt {
		if i, ok := made[u.LotID]; ok {
			out.Outputs[i].Unbuilt = numericText(u.Quantity, quantityPlaces)
		}
	}
	costs := s.has(ctx, permCostsView)
	for _, r := range byRows {
		if r.LotID != nil {
			out.ByProducts = append(out.ByProducts, byproductAnswer(r, costs))
		}
	}
	type key struct{ component, lot int64 }
	sums := map[key]*gen.ManufacturingOrderConsumedLot{}
	order := map[int64][]int64{}
	for _, r := range rows {
		k := key{r.ComponentID, r.LotID}
		c := sums[k]
		if c == nil {
			c = &gen.ManufacturingOrderConsumedLot{LotId: r.LotID, LotNumber: r.LotNumber, Expiry: dateOut(dayOfColumn(r.Expiry)), Consumed: "0", Scrapped: "0"}
			sums[k] = c
			order[r.ComponentID] = append(order[r.ComponentID], r.LotID)
		}
		c.Consumed = text(new(big.Rat).Add(mustRat(c.Consumed), ratOrZero(r.Consumed)), quantityPlaces)
		c.Scrapped = text(new(big.Rat).Add(mustRat(c.Scrapped), ratOrZero(r.Scrapped)), quantityPlaces)
	}
	for _, c := range components {
		lots := order[c.ID]
		if len(lots) == 0 {
			continue
		}
		entry := gen.ManufacturingOrderComponentLots{ComponentId: c.ID, Variant: componentRef(nil, c, costs),
			Lots: make([]gen.ManufacturingOrderConsumedLot, 0, len(lots))}
		entry.Variant.ProductType = goodsType
		for _, lot := range lots {
			entry.Lots = append(entry.Lots, *sums[key{c.ID, lot}])
		}
		out.Components = append(out.Components, entry)
	}
	return gen.GetManufacturingOrderLots200JSONResponse(out), nil
}
