package inventory

import (
	"cmp"
	"context"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
	"github.com/vantigo-io/vantigo/server/internal/live"
)

// Lots (manufacturing phase 2 M52–M58, M64): a numbered quantity of one
// variant, created by the posting that first names it by number — a receipt
// or an adjustment up (posting.go), or another module's EnsureLots — and
// restricted by holds, each owned by the document that put it. A lot's status
// is derived from its active holds and rewritten, under the lot's FOR NO KEY
// UPDATE, by every hold change; nothing sets it directly.
//
// Every writer takes the lots before the movement's header and the
// locations (M58): the lot counter when a number is generated, the new lots
// in (variant, lower(number)) order, then every lot named in id order — FOR
// SHARE, or FOR NO KEY UPDATE for a serial lot the posting increases (M53)
// and for a hold change. So no posting holds a location and then waits for a
// lot, and two increases of one serial lot serialise on it.

// The entity kind of a lot's events, and their kinds (M64).
const (
	entityLot         = "lot"
	eventLotCreated   = "lot_created"
	eventLotHeld      = "lot_held"
	eventLotReleased  = "lot_released"
	lotNumberPrefix   = "L"
	lotOriginModule   = "inventory"
	lotOriginMovement = "movement"
)

// The refusal codes of the lot rules (M56, M57), the contract's words.
const (
	codeLotRequired        = contracts.StockLotRequired
	codeLotNotAllowed      = contracts.StockLotNotAllowed
	codeLotUnknown         = contracts.StockLotUnknown
	codeSerialQuantity     = contracts.StockSerialQuantity
	codeSerialFirstInvalid = "serial_first_invalid"
	codeLotExpired         = contracts.StockLotExpired
	codeLotBlocked         = contracts.StockLotBlocked
	codeLotDatesDiffer     = contracts.StockLotDatesDiffer
)

// lotNumberPattern is a lot number's shape (M52): GS1's limit for AI (10)
// and (21), so every lot prints.
var lotNumberPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,20}$`)

// The holds' kinds and causes (M64).
var (
	holdKinds  = []string{"quarantine", "block"}
	holdCauses = []string{"inspection", "deviation", "complaint", "recall", "other"}
)

// tracked reports whether a variant's stock is kept in lots.
func tracked(v contracts.VariantEntry) bool { return v.Tracking != contracts.TrackingNone }

// lotRequest is a lot a writer names by number: a receipt's or an
// adjustment's new lot, or one of EnsureLots'. Number "" generates L<n>. The
// dates are those stated, nil when not; production is the base of the
// defaults — the receipt's day when not stated.
type lotRequest struct {
	variantID   int32
	number      string
	supplierLot *string
	note        *string
	production  *time.Time
	bestBefore  *time.Time
	expiry      *time.Time
	// compareProduction is whether a stated production date is compared
	// with an existing lot's: the API's, not EnsureLots' (whose production
	// date is always given — today for a production).
	compareProduction bool
	// noDefaults is whether a lot made here takes only the dates stated, not
	// the variant's day offsets: a count's (its lot was made on a day nobody
	// knows).
	noDefaults bool
}

// lotOutcome is a lotRequest found or made: its lot, whether this writer
// created it, and its refusal ("" when none).
type lotOutcome struct {
	lot      store.InventoryLot
	created  bool
	failCode string
	failText string
}

// lotKey is a lot's key: its variant and its number, lower-cased.
type lotKey struct {
	variantID int32
	number    string
}

// ensureLots finds or creates the lots reqs name, in M58's steps 3 and 4:
// the counter for the numbers to generate, then each distinct (variant,
// number) inserted in key order, a number the variant has read back. The
// stated dates of every request naming an existing lot are compared with it;
// the defaults apply only to a lot made here. origin names the document that
// made them (nil: set by the caller once its movement is claimed, and their
// lot_created events recorded then). It answers one outcome per request, in
// order.
func (s *server) ensureLots(ctx context.Context, txq *store.Queries, reqs []lotRequest, variants map[int32]contracts.VariantEntry,
	today time.Time, origin *movementSource, who actor,
) ([]lotOutcome, error) {
	out := make([]lotOutcome, len(reqs))
	// 3. The counter, for the numbers to generate.
	generate := 0
	for _, r := range reqs {
		if r.number == "" {
			generate++
		}
	}
	numbers := make([]string, len(reqs))
	if generate > 0 {
		first, err := txq.AllocateLotNumbers(ctx, int64(generate))
		if err != nil {
			return nil, fmt.Errorf("inventory: allocate lot numbers: %w", err)
		}
		for i, r := range reqs {
			numbers[i] = r.number
			if r.number == "" {
				numbers[i] = lotNumberPrefix + strconv.FormatInt(first, 10)
				first++
			}
		}
	} else {
		for i, r := range reqs {
			numbers[i] = r.number
		}
	}
	// 4. The lots, in (variant, lower(number)) order, one insert per key; the
	// first request of a key gives a new lot its fields. A generated number is
	// a key of its own: one a typed request of this call names already takes
	// the next number.
	firstOf := map[lotKey]int{}
	generated := map[lotKey]bool{}
	for i, r := range reqs {
		k := lotKey{r.variantID, strings.ToLower(numbers[i])}
		if _, ok := firstOf[k]; !ok && r.number != "" {
			firstOf[k] = i
		}
	}
	for i, r := range reqs {
		if r.number != "" {
			continue
		}
		k := lotKey{r.variantID, strings.ToLower(numbers[i])}
		for _, taken := firstOf[k]; taken; _, taken = firstOf[k] {
			next, err := txq.AllocateLotNumbers(ctx, 1)
			if err != nil {
				return nil, fmt.Errorf("inventory: allocate lot numbers: %w", err)
			}
			numbers[i] = lotNumberPrefix + strconv.FormatInt(next, 10)
			k = lotKey{r.variantID, strings.ToLower(numbers[i])}
		}
		firstOf[k], generated[k] = i, true
	}
	keys := make([]lotKey, 0, len(firstOf))
	for k := range firstOf {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b lotKey) int {
		return cmp.Or(cmp.Compare(a.variantID, b.variantID), strings.Compare(a.number, b.number))
	})
	found := make(map[lotKey]lotOutcome, len(keys))
	for _, k := range keys {
		r := reqs[firstOf[k]]
		v := variants[r.variantID]
		p := store.InsertLotParams{
			VariantID: r.variantID, Number: numbers[firstOf[k]], Serial: v.Tracking == contracts.TrackingSerial,
			SupplierLot: r.supplierLot, Note: r.note, Now: s.deps.Clock(), Sku: v.SKU, ProductName: v.ProductName,
			CreatedByUserID: who.UserID, CreatedByDisplay: &who.Display,
		}
		base := today
		if r.production != nil {
			base = *r.production
			p.ProductionDate = pgDate(r.production)
		}
		if base.IsZero() {
			return nil, fmt.Errorf("contracts: a stock call without today")
		}
		p.BestBefore = pgDate(r.bestBefore)
		if r.bestBefore == nil && v.BestBeforeDays != nil && !r.noDefaults {
			p.BestBefore = pgDate(ptr(base.AddDate(0, 0, int(*v.BestBeforeDays))))
		}
		p.Expiry = pgDate(r.expiry)
		if r.expiry == nil && v.ExpiryDays != nil && !r.noDefaults {
			p.Expiry = pgDate(ptr(base.AddDate(0, 0, int(*v.ExpiryDays))))
		}
		if origin != nil {
			p.OriginModule, p.OriginKind, p.OriginID = &origin.module, &origin.kind, &origin.id
		}
		id, err := txq.InsertLot(ctx, p)
		// A generated number another writer typed already: never join its lot
		// (H1) — under the counter row's lock, the next number, until one is
		// free.
		for generated[k] && noRows(err) {
			next, aerr := txq.AllocateLotNumbers(ctx, 1)
			if aerr != nil {
				return nil, fmt.Errorf("inventory: allocate lot numbers: %w", aerr)
			}
			p.Number = lotNumberPrefix + strconv.FormatInt(next, 10)
			if _, typed := firstOf[lotKey{r.variantID, strings.ToLower(p.Number)}]; typed {
				continue // this call names that number itself
			}
			id, err = txq.InsertLot(ctx, p)
		}
		created := err == nil
		if noRows(err) {
			err = nil
		}
		if err != nil {
			return nil, fmt.Errorf("inventory: create lot %s of variant %d: %w", p.Number, r.variantID, err)
		}
		lot, err := txq.LotByNumber(ctx, store.LotByNumberParams{VariantID: r.variantID, Number: p.Number})
		if err != nil {
			return nil, fmt.Errorf("inventory: read lot %s of variant %d: %w", p.Number, r.variantID, err)
		}
		if created && lot.ID != id {
			return nil, fmt.Errorf("inventory: lot %s of variant %d read back as %d, created as %d", p.Number, r.variantID, lot.ID, id)
		}
		found[k] = lotOutcome{lot: lot, created: created}
		// A posting's lots are recorded once its movement is claimed, so a
		// replay that removes them leaves no event behind (recordLotsCreated).
		if created && origin != nil {
			if err := s.recordLotsCreated(ctx, txq, []store.InventoryLot{lot}, who); err != nil {
				return nil, err
			}
		}
	}
	for i, r := range reqs {
		k := lotKey{r.variantID, strings.ToLower(numbers[i])}
		o := found[k]
		// The request that created the lot gave it its fields; every other
		// is compared, on the dates it states only (M57).
		if !o.created || firstOf[k] != i {
			if field := differingDate(o.lot, r); field != "" {
				o.failCode, o.failText = codeLotDatesDiffer, fmt.Sprintf("The lot %s has another %s; leave the date out to add to it.", o.lot.Number, field)
			}
		}
		if firstOf[k] != i {
			o.created = false
		}
		out[i] = o
	}
	return out, nil
}

// recordLotsCreated records the lot_created event of each lot a posting made,
// once its movement is claimed.
func (s *server) recordLotsCreated(ctx context.Context, txq *store.Queries, lots []store.InventoryLot, who actor) error {
	for _, lot := range lots {
		if err := s.recordEvent(ctx, txq, eventActor(who), event{EntityKind: entityLot, EntityID: lot.ID, Kind: eventLotCreated,
			Payload: map[string]any{"number": lot.Number, "variantId": lot.VariantID}}); err != nil {
			return err
		}
	}
	return nil
}

// differingDate is the first date r states that differs from the lot's, by
// its field name, "" when none does.
func differingDate(lot store.InventoryLot, r lotRequest) string {
	differs := func(stated *time.Time, stored pgtype.Date) bool {
		return stated != nil && (!stored.Valid || !sameDay(*stated, stored.Time))
	}
	switch {
	case differs(r.expiry, lot.Expiry):
		return "expiry"
	case differs(r.bestBefore, lot.BestBefore):
		return "best-before date"
	case r.compareProduction && differs(r.production, lot.ProductionDate):
		return "production date"
	}
	return ""
}

// sameDay reports whether two dates name one day.
func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// pgDate is a date as a column takes it, NULL for nil.
func pgDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

// dateOrNil is a date column as a time, nil when NULL.
func dateOrNil(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	return &d.Time
}

// lockLots takes the lots, in id order (M58 step 5): FOR NO KEY UPDATE for
// those in update, FOR SHARE for the rest — each run of one mode in one
// statement, so the order holds across the two. It answers the lots found.
func lockLots(ctx context.Context, txq *store.Queries, ids []int64, update map[int64]bool) (map[int64]store.InventoryLot, error) {
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	out := make(map[int64]store.InventoryLot, len(ids))
	for start := 0; start < len(ids); {
		end := start + 1
		for end < len(ids) && update[ids[end]] == update[ids[start]] {
			end++
		}
		run := ids[start:end]
		var rows []store.InventoryLot
		var err error
		if update[run[0]] {
			rows, err = txq.LockLots(ctx, run)
		} else {
			rows, err = txq.ShareLots(ctx, run)
		}
		if err != nil {
			return nil, fmt.Errorf("inventory: lock lots: %w", err)
		}
		for _, r := range rows {
			out[r.ID] = r
		}
		start = end
	}
	return out, nil
}

// lotExpired reports whether the lot is expired on today (M62).
func lotExpired(lot store.InventoryLot, today time.Time) bool {
	return contracts.LotExpired(dateOrNil(lot.Expiry), today)
}

// eventActor is a poster as an event's actor: a user when it names one, the
// system otherwise.
func eventActor(who actor) actor {
	if who.Kind != "" {
		return who
	}
	if who.UserID != nil {
		return actor{Kind: actorKindUser, Display: who.Display, UserID: who.UserID}
	}
	display := who.Display
	if display == "" {
		display = systemActorDisplayName
	}
	return actor{Kind: actorKindSystem, Display: display}
}

// EnsureLots finds or creates each lot inside tx (manufacturing phase 2 M67,
// M80), after the caller's rows and before its Produce, in M58's steps 3–5,
// and answers the lot ids in the order given.
func (l ledger) EnsureLots(ctx context.Context, tx pgx.Tx, src contracts.StockSource, today time.Time, lots []contracts.NewLot) ([]int64, error) {
	if err := src.Validate(); err != nil {
		return nil, err
	}
	if today.IsZero() {
		return nil, fmt.Errorf("contracts: a stock call without today")
	}
	if len(lots) == 0 {
		return nil, nil
	}
	ids := make([]int32, 0, len(lots))
	reqs := make([]lotRequest, len(lots))
	for i, n := range lots {
		if n.ProductionDate.IsZero() {
			return nil, fmt.Errorf("inventory: lot %d of an EnsureLots has no production date", i)
		}
		if n.Number != "" && !lotNumberPattern.MatchString(n.Number) {
			return nil, fmt.Errorf("inventory: %q is not a lot number: 1–20 characters A–Z, a–z, 0–9 and . _ / -", n.Number)
		}
		if len([]rune(n.SupplierLot)) > 40 {
			return nil, fmt.Errorf("inventory: a supplier lot has at most 40 characters")
		}
		ids = append(ids, n.VariantID)
		production := n.ProductionDate
		reqs[i] = lotRequest{variantID: n.VariantID, number: n.Number, production: &production, bestBefore: n.BestBefore, expiry: n.Expiry}
		if n.SupplierLot != "" {
			reqs[i].supplierLot = ptr(n.SupplierLot)
		}
	}
	lineOf := func(i int) (int32, int64) { return lots[i].VariantID, 0 }
	variants, failures, err := l.stockedVariants(ctx, tx, ids, lineOf, len(lots))
	if err != nil {
		return nil, err
	}
	for i, n := range lots {
		if v, ok := variants[n.VariantID]; ok && v.ProductType == productTypeGoods && !tracked(v) {
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockLotNotAllowed, VariantID: n.VariantID, Index: i,
				Detail: fmt.Sprintf("%s is not tracked in lots.", v.SKU)})
		}
	}
	if len(failures) > 0 {
		return nil, refusalOf(sortedRefusals(failures))
	}
	for _, v := range variants {
		if v.Tracking == "" {
			return nil, fmt.Errorf("inventory: the catalog answered variant %d with no tracking", v.ID)
		}
	}
	txq := store.New(tx)
	origin := &movementSource{module: src.Module, kind: src.Kind, id: strconv.FormatInt(src.ID, 10)}
	outcomes, err := l.s.ensureLots(ctx, txq, reqs, variants, today, origin, actor{})
	if err != nil {
		return nil, err
	}
	// 5. The lots, in id order, every one FOR NO KEY UPDATE: a serial's for
	// the Produce that increases it, and every lot's so that a HoldLots after
	// it in the same transaction — a production's quarantine on its output —
	// holds the lock it needs already rather than upgrading a FOR SHARE, which
	// two such transactions on one lot would deadlock on (M86).
	lotIDs := make([]int64, len(outcomes))
	update := map[int64]bool{}
	serial := map[int64]bool{}
	for i, o := range outcomes {
		lotIDs[i] = o.lot.ID
		update[o.lot.ID] = true
		if variants[lots[i].VariantID].Tracking == contracts.TrackingSerial {
			serial[o.lot.ID] = true
		}
	}
	if _, err := lockLots(ctx, txq, lotIDs, update); err != nil {
		return nil, err
	}
	// A new serial whose number already holds stock (M57).
	var serialIDs []int64
	for id := range serial {
		serialIDs = append(serialIDs, id)
	}
	held := map[int64]*big.Rat{}
	if len(serialIDs) > 0 {
		totals, err := txq.LotTotals(ctx, serialIDs)
		if err != nil {
			return nil, fmt.Errorf("inventory: read the serials' stock: %w", err)
		}
		for _, t := range totals {
			held[t.LotID] = ratFromText(t.OnHand)
		}
	}
	for i, o := range outcomes {
		switch {
		case o.failCode != "":
			failures = append(failures, contracts.StockLineRefusal{Code: o.failCode, VariantID: lots[i].VariantID, Index: i, LotID: o.lot.ID, Detail: o.failText})
		case serial[o.lot.ID] && held[o.lot.ID] != nil && held[o.lot.ID].Sign() > 0:
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockSerialQuantity, VariantID: lots[i].VariantID, Index: i, LotID: o.lot.ID,
				Detail: fmt.Sprintf("The serial %s is in stock already.", o.lot.Number)})
		}
	}
	if len(failures) > 0 {
		return nil, refusalOf(failures)
	}
	return lotIDs, nil
}

// sortedRefusals is failures by index.
func sortedRefusals(failures []contracts.StockLineRefusal) []contracts.StockLineRefusal {
	slices.SortStableFunc(failures, func(a, b contracts.StockLineRefusal) int { return cmp.Compare(a.Index, b.Index) })
	return failures
}

// HoldLots puts one hold per entry for src's document (manufacturing phase 2
// M64, M67), inside tx: the lots FOR NO KEY UPDATE in id order, a hold of src
// of the same kind already active left as is, the status derived again.
func (l ledger) HoldLots(ctx context.Context, tx pgx.Tx, src contracts.StockSource, holds []contracts.LotHold) error {
	if err := src.Validate(); err != nil {
		return err
	}
	if len(holds) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(holds))
	for _, h := range holds {
		if !slices.Contains(holdKinds, h.Kind) || !slices.Contains(holdCauses, h.Cause) {
			return fmt.Errorf("inventory: a hold of kind %q and cause %q", h.Kind, h.Cause)
		}
		if len([]rune(h.Note)) > 1000 {
			return fmt.Errorf("inventory: a hold's note has at most 1000 characters")
		}
		ids = append(ids, h.LotID)
	}
	return l.s.holdLots(ctx, tx, store.New(tx), sourceText(src), holds, ids)
}

// sourceText is a document as a hold keeps it.
func sourceText(src contracts.StockSource) movementSource {
	return movementSource{module: src.Module, kind: src.Kind, id: strconv.FormatInt(src.ID, 10)}
}

// holdLots is HoldLots' work, for any source: the lot page's too. It tells
// the lots' live topics on q, the transaction that holds them.
func (s *server) holdLots(ctx context.Context, q live.Execer, txq *store.Queries, src movementSource, holds []contracts.LotHold, ids []int64) error {
	update := map[int64]bool{}
	for _, id := range ids {
		update[id] = true
	}
	lots, err := lockLots(ctx, txq, ids, update)
	if err != nil {
		return err
	}
	var failures []contracts.StockLineRefusal
	for i, h := range holds {
		if _, ok := lots[h.LotID]; !ok {
			failures = append(failures, contracts.StockLineRefusal{Code: contracts.StockLotUnknown, Index: i, LotID: h.LotID,
				Detail: fmt.Sprintf("No lot has the id %d.", h.LotID)})
		}
	}
	if len(failures) > 0 {
		return refusalOf(failures)
	}
	active, err := txq.ActiveHoldsOfSource(ctx, store.ActiveHoldsOfSourceParams{LotIds: ids, SourceModule: src.module, SourceKind: src.kind, SourceID: src.id})
	if err != nil {
		return fmt.Errorf("inventory: read the holds of %s/%s/%s: %w", src.module, src.kind, src.id, err)
	}
	type held struct {
		lot  int64
		kind string
	}
	has := map[held]bool{}
	for _, a := range active {
		has[held{a.LotID, a.Kind}] = true
	}
	for _, h := range holds {
		if has[held{h.LotID, h.Kind}] {
			continue
		}
		has[held{h.LotID, h.Kind}] = true
		who := eventActor(actor{UserID: h.By.UserID, Display: h.By.Display})
		var note *string
		if h.Note != "" {
			note = ptr(h.Note)
		}
		id, err := txq.InsertLotHold(ctx, store.InsertLotHoldParams{
			LotID: h.LotID, Kind: h.Kind, Cause: h.Cause, Note: note, SourceModule: src.module, SourceKind: src.kind, SourceID: src.id,
			CreatedByUserID: who.UserID, CreatedByDisplay: &who.Display, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("inventory: hold lot %d: %w", h.LotID, err)
		}
		if err := s.recordEvent(ctx, txq, who, event{EntityKind: entityLot, EntityID: h.LotID, Kind: eventLotHeld,
			Payload: map[string]any{"holdId": id, "kind": h.Kind, "cause": h.Cause, "source": src.module + "/" + src.kind + "/" + src.id}}); err != nil {
			return err
		}
	}
	if _, err := txq.DeriveLotStatuses(ctx, store.DeriveLotStatusesParams{Ids: ids, Now: s.deps.Clock()}); err != nil {
		return fmt.Errorf("inventory: derive the lots' statuses: %w", err)
	}
	return notifyLots(ctx, q, ids)
}

// ReleaseLotHolds releases every active hold src's document put on the lots
// named, every lot it holds when lotIDs is nil (manufacturing phase 2 M64,
// M67). Holds of any other source stay; the status is derived again.
func (l ledger) ReleaseLotHolds(ctx context.Context, tx pgx.Tx, src contracts.StockSource, lotIDs []int64, by contracts.LotActor) error {
	if err := src.Validate(); err != nil {
		return err
	}
	txq := store.New(tx)
	text := sourceText(src)
	if lotIDs == nil {
		var err error
		if lotIDs, err = txq.LotsHeldBySource(ctx, store.LotsHeldBySourceParams{SourceModule: text.module, SourceKind: text.kind, SourceID: text.id}); err != nil {
			return fmt.Errorf("inventory: read the lots %s/%s/%s holds: %w", text.module, text.kind, text.id, err)
		}
	}
	if len(lotIDs) == 0 {
		return nil
	}
	return l.s.releaseHolds(ctx, tx, txq, text, lotIDs, by)
}

// releaseHolds is ReleaseLotHolds' work, for any source, telling the lots'
// live topics on q.
func (s *server) releaseHolds(ctx context.Context, q live.Execer, txq *store.Queries, src movementSource, lotIDs []int64, by contracts.LotActor) error {
	update := map[int64]bool{}
	for _, id := range lotIDs {
		update[id] = true
	}
	if _, err := lockLots(ctx, txq, lotIDs, update); err != nil {
		return err
	}
	who := eventActor(actor{UserID: by.UserID, Display: by.Display})
	released, err := txq.ReleaseSourceHolds(ctx, store.ReleaseSourceHoldsParams{
		LotIds: lotIDs, SourceModule: src.module, SourceKind: src.kind, SourceID: src.id,
		ReleasedByUserID: who.UserID, ReleasedByDisplay: &who.Display, Now: s.deps.Clock(),
	})
	if err != nil {
		return fmt.Errorf("inventory: release the holds of %s/%s/%s: %w", src.module, src.kind, src.id, err)
	}
	for _, r := range released {
		if err := s.recordEvent(ctx, txq, who, event{EntityKind: entityLot, EntityID: r.LotID, Kind: eventLotReleased,
			Payload: map[string]any{"holdId": r.ID, "kind": r.Kind, "cause": r.Cause, "source": src.module + "/" + src.kind + "/" + src.id}}); err != nil {
			return err
		}
	}
	if _, err := txq.DeriveLotStatuses(ctx, store.DeriveLotStatusesParams{Ids: lotIDs, Now: s.deps.Clock()}); err != nil {
		return fmt.Errorf("inventory: derive the lots' statuses: %w", err)
	}
	return notifyLots(ctx, q, lotIDs)
}
