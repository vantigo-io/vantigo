package inventory

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The posting engine (design I8–I15, §4): the one function every change of
// stock goes through. The API's three handlers open the transaction and call
// it; the count post (count_post.go) and the opening-balance import
// (import.go) call it inside theirs, with no line cap. A movement is never edited or deleted, and the balance per
// (variant, location, lot) is kept by the same transaction that writes it.
//
// The lock order, in every posting (manufacturing phase 2 M58):
//
//  1. The clientRequestId is read with a plain SELECT. A hit answers the
//     first movement with the same body, or client_request_reused (I14): a
//     replay is answered even if its variant has since been discontinued.
//  2. ProductCatalog.LockVariants takes FOR SHARE on the variants and their
//     products, in variant id order, on the posting's own transaction (I22):
//     Products' rows before any of Inventory's — but for the two documents
//     whose own row is taken first, a count's at its post (count_post.go) and
//     a transfer's at its send, receive and cancel, with its two sites FOR
//     SHARE after it (phase 2 design I38, transfer_actions.go). There is no
//     cycle: Products never waits on a count, a transfer or a site.
//  3. The lot counter — the inventory.counters row "lot", taken by its
//     upsert — only when a lot number is to be generated (L<n>). It
//     serialises the postings that generate one for the rest of their
//     transaction, and no writer takes it after a lot.
//  4. New lots, inserted in (variant id, lower(number)) order with INSERT …
//     ON CONFLICT DO NOTHING, a number the variant has read back (M57): two
//     postings creating one number serialise on the unique index, and the
//     second reads the first's lot once it commits.
//  5. Every lot the posting names — and the lots an AutoLot line may take,
//     read just before — in id order: FOR SHARE, or FOR NO KEY UPDATE for a
//     lot a line increases whose variant is serial (M53). A hold change
//     takes the lot FOR NO KEY UPDATE (lots.go), so it waits for the posting
//     or is waited for. The lot rules (M56) are judged on the locked lots.
//  6. The key is claimed by writing the movement's header, INSERT … ON
//     CONFLICT DO NOTHING RETURNING. A conflict means a concurrent first
//     attempt committed; it is read back and answered as in step 1.
//  7. The locations FOR SHARE, in id order. A posting writes nothing to a
//     location, so two postings never wait for each other there.
//  8. Every balance created at zero where missing and locked, in (variant,
//     location, lot) order with no lot first, by one INSERT … ON CONFLICT DO
//     UPDATE … RETURNING (I10): two postings to a new key — no lot included,
//     the key being NULLS NOT DISTINCT — serialise on the row rather than
//     collide on its key, and a balance deleted at zero under the statement
//     is inserted again by it, so no lock is held across a second attempt.
//
// Then the AutoLot lines take their lots FEFO under those locks (M65a), the
// lines are applied in order, the balance checked after each (so -5 then +5
// against 0 fails at the first), a serial lot that is increased summed over
// every location under its lock (M53), the movement's lines written, and the
// balances left at zero deleted. Every failing line is answered at once: the
// shape, the catalog's, the lots' and the locations' refusals together, then
// — only when those pass — the balances'.
//
// An API posting that loses a race anyway — a deadlock or a serialization
// failure — runs again from step 1 in a new transaction (postMovement), a
// bounded number of times. The retry is safe for the key: the lost attempt
// rolled back and its claim with it, so the retry claims the key afresh or,
// when a concurrent first attempt committed meanwhile, answers it as a replay.

// postOptions is how a caller of the engine posts.
type postOptions struct {
	// maxLines caps the lines: 500 for the API (I15), 0 — none — for an
	// internal caller.
	maxLines int
	// receipt refuses a Discontinued product's variant (I12), and a line
	// into a blocked lot (M56).
	receipt bool
	// check, when set, judges the posting once its variants and balances are
	// locked, its entries resolved to units, and before any line is applied:
	// the count post's moved lines (I19), a transfer action's per-line rules
	// (phase 2 design I38–I44: a receive's outstanding, a send's goods). A
	// refusal it answers rolls the posting back. lines are the posting's own,
	// resolved; the check reads them and changes nothing.
	check func(lines []engineLine, variants map[int32]contracts.VariantEntry, balances map[balanceKey]*big.Rat) error
	// today is the workspace zone's date (manufacturing phase 2 M62), read
	// by the caller on the pool before its transaction: what an expiry is
	// judged against and a new lot's dates default from. A posting that
	// needs it and has none fails.
	today time.Time
	// takes marks the from-only lines as issue or consume lines (M56): a lot
	// on hold is refused them, and an expired one unless the line allows it
	// and expiredOverride holds. Moves, adjustments, counts and transfers
	// ignore holds and expiry, as does an unbuild.
	takes           bool
	expiredOverride bool
	// lotOrigin is the origin kind the lots this posting makes are named by,
	// with its movement's id: "movement" when empty, "import" for the
	// opening-balance import's (M71).
	lotOrigin string
}

// maxAPILines is the most lines a movement posted through the API has.
const maxAPILines = 500

// movementHeader is a movement as its caller describes it: its key and the
// hash of the body that carried it, its site and kind, the adjustment's
// reason, the note and the receipt's supplier fields, and who posts it.
type movementHeader struct {
	clientRequestID   uuid.UUID
	requestHash       []byte
	siteID            int64
	kind              string
	reason            *string
	note              *string
	supplierName      *string
	supplierReference *string
	countID           *int64
	poster            actor
	// source is the document the movement was posted for: another module's
	// (an issue's, orders design O45), or a transfer's, whose movements name
	// the transfer and, as the line, the action that posted them (phase 2
	// design I45). It is nil for every other posting of the module's own.
	source *movementSource
}

// movementSource is a movement's source reference (design §12), as text.
type movementSource struct {
	module, kind, id string
	lineID           *string
}

// engineLine is one line as the engine takes it: a variant, from and to (each
// nil for none), and a quantity more than 0 — or, for an adjustment in set
// mode, the on-hand to set at a location, posted as the difference. A line
// entered in packagings carries its entry instead of the quantity (or the
// on-hand to set), resolved once its variant is locked (I25). A line its
// caller already refused carries the refusal and is only reported.
type engineLine struct {
	index     int
	variantID int32
	from, to  *int64
	quantity  *big.Rat
	setAt     *int64
	setTo     *big.Rat
	entry     *entry
	entered   []byte
	failCode  string
	failText  string
	// lotID is the line's lot, 0 for none; newLot names one by number to be
	// found or made instead (a receipt's, an adjustment up's, M57).
	lotID  int64
	newLot *lotRequest
	// autoLot lets the engine take the lots FEFO at from, under its locks
	// (M65a); allowExpired lets an issue or consume line take an expired lot
	// on the call's override (M62), and expiredOverride records that it did.
	autoLot         bool
	allowExpired    bool
	expiredOverride bool
}

// postResult is a posting's outcome: the movement, whether it was posted
// before under the same key and body (a replay, answered 200), and the lots
// its AutoLot lines took.
type postResult struct {
	movementID int64
	replay     bool
	picks      []contracts.StockPick
}

// requestHash is the SHA-256 of a posting's canonical body: its kind and the
// typed request encoded by encoding/json, whose field order is the struct's
// and whose numbers are formatted one way, so the same body always hashes the
// same (I14).
func requestHash(kind string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("inventory: encode a %s for its hash: %w", kind, err)
	}
	sum := sha256.Sum256(append([]byte(kind+"\n"), b...))
	return sum[:], nil
}

// balanceKey names one balance row: a variant on a location in a lot, 0 for
// none — the whole key of a balance (manufacturing phase 2 M55).
type balanceKey struct {
	variantID  int32
	locationID int64
	lotID      int64
}

// errCandidatesShort is a posting whose AutoLot lines locked only the lots
// that looked, read plainly, to cover them, and found under the locks that
// they do not: it runs again with every candidate.
var errCandidatesShort = errors.New("inventory: the auto lots locked do not cover the line")

// lotMargin is how many usable-looking lots beyond those covering its
// quantity an AutoLot line locks, for a lot held or emptied under the read.
const lotMargin = 2

// post is the engine: it posts the movement and its lines inside tx, in the
// lock order above, and answers the movement — or a refusal naming every
// failing line, after which the caller rolls back. A posting with an AutoLot
// line locks only the candidates that look to cover it, FEFO and with a
// margin (M65a), on a savepoint; when they fall short under the locks, the
// savepoint is rolled back — its locks with it — and the posting runs again
// with every candidate.
func (s *server) post(ctx context.Context, tx pgx.Tx, txq *store.Queries, m movementHeader, lines []engineLine, opts postOptions) (postResult, error) {
	if !slices.ContainsFunc(lines, func(l engineLine) bool { return l.autoLot }) {
		return s.postOnce(ctx, tx, txq, m, lines, opts, false)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return postResult{}, fmt.Errorf("inventory: open a posting's savepoint: %w", err)
	}
	res, err := s.postOnce(ctx, sp, store.New(sp), m, lines, opts, true)
	if errors.Is(err, errCandidatesShort) {
		if err := sp.Rollback(ctx); err != nil {
			return postResult{}, fmt.Errorf("inventory: roll a posting's savepoint back: %w", err)
		}
		return s.postOnce(ctx, tx, txq, m, lines, opts, false)
	}
	if err != nil {
		return res, err
	}
	if err := sp.Commit(ctx); err != nil {
		return postResult{}, fmt.Errorf("inventory: release a posting's savepoint: %w", err)
	}
	return res, nil
}

// postOnce is one run of the engine; limit locks only the AutoLot
// candidates that look to cover their lines.
func (s *server) postOnce(ctx context.Context, tx pgx.Tx, txq *store.Queries, m movementHeader, lines []engineLine, opts postOptions, limit bool) (postResult, error) {
	// 1. The key, before anything else.
	if res, done, err := replayOf(ctx, txq, m); done || err != nil {
		return res, err
	}
	// The lines are the caller's; an attempt resolves its entries on its own
	// copy, so a retry starts from what the caller gave.
	lines = slices.Clone(lines)

	// The request as a whole.
	if opts.maxLines > 0 && len(lines) > opts.maxLines {
		return postResult{}, refuse(problem(http.StatusBadRequest, codeTooManyLines, invalidPostingTitle,
			fmt.Sprintf("A movement has at most %d lines; this one has %d.", opts.maxLines, len(lines))))
	}
	if len(lines) == 0 {
		return postResult{}, nothingToPost()
	}
	if _, err := txq.GetSite(ctx, m.siteID); noRows(err) {
		var f fieldErrors
		f.add("siteId", "No site has this id")
		return postResult{}, refuseInvalid(invalidPostingTitle, f)
	} else if err != nil {
		return postResult{}, fmt.Errorf("inventory: read site %d: %w", m.siteID, err)
	}

	failures := map[int]lineFailure{}
	failAt := func(l engineLine, code, detail string, lotID, locationID int64) {
		if _, ok := failures[l.index]; !ok {
			failures[l.index] = lineFailure{Index: l.index, Code: code, Detail: detail, LotID: lotID, LocationID: locationID}
		}
	}
	fail := func(l engineLine, code, detail string) { failAt(l, code, detail, l.lotID, 0) }
	for _, l := range lines {
		if l.failCode != "" {
			fail(l, l.failCode, l.failText)
		}
	}

	// 2. The variants, locked on this transaction.
	variantIDs := make([]int32, 0, len(lines))
	for _, l := range lines {
		variantIDs = append(variantIDs, l.variantID)
	}
	slices.Sort(variantIDs)
	variantIDs = slices.Compact(variantIDs)
	entries, err := s.lockVariants(ctx, tx, variantIDs)
	if err != nil {
		return postResult{}, err
	}
	variants := make(map[int32]contracts.VariantEntry, len(entries))
	for _, e := range entries {
		if e.Tracking == "" {
			return postResult{}, fmt.Errorf("inventory: the catalog answered variant %d with no tracking", e.ID)
		}
		variants[e.ID] = e
	}
	for _, l := range lines {
		v, ok := variants[l.variantID]
		switch {
		case !ok:
			fail(l, codeVariantNotFound, fmt.Sprintf("No variant has the id %d.", l.variantID))
		case v.ProductType != productTypeGoods && l.increases():
			fail(l, codeVariantNotStockable, fmt.Sprintf("%s is a service; only goods are kept in stock.", v.SKU))
		case opts.receipt && v.ProductStatus == productStatusDiscontinued:
			fail(l, codeVariantDiscontinued, fmt.Sprintf("%s is discontinued and is no longer received.", v.SKU))
		}
	}

	// The variants inspected on receipt (manufacturing phase 2 M100), read
	// under their lock.
	var inspected map[int32]bool
	if opts.receipt {
		if inspected, err = inspectedVariants(ctx, txq, lines, variants); err != nil {
			return postResult{}, err
		}
	}

	// The entries, resolved against the packagings as they are locked (I25).
	for i := range lines {
		l := &lines[i]
		v, ok := variants[l.variantID]
		if l.entry == nil || l.failCode != "" || !ok {
			continue
		}
		q, snap, code, detail := l.entry.resolve(v)
		switch {
		case code != "":
			fail(*l, code, detail)
		case l.setAt != nil:
			l.setTo, l.entered = q, snap
		case q.Sign() == 0:
			fail(*l, codeQuantityInvalid, "The entry comes to 0; the quantity must be more than 0")
		default:
			l.quantity, l.entered = q, snap
		}
	}

	// The lot's shape, judged as the variant is now (M56): a line of a
	// tracked variant names a lot, one of an untracked variant none, a serial
	// line moves exactly 1.
	for i := range lines {
		l := &lines[i]
		v, ok := variants[l.variantID]
		if !ok {
			continue
		}
		names := l.lotID != 0 || l.newLot != nil
		switch {
		case !tracked(v) && l.autoLot && !names:
			// Nothing to pick: an untracked variant's line posts without a lot.
			l.autoLot = false
		case !tracked(v) && names:
			fail(*l, codeLotNotAllowed, fmt.Sprintf("%s is not tracked in lots; the line names none.", v.SKU))
		case tracked(v) && !names && (!l.autoLot || l.from == nil || l.to != nil):
			fail(*l, codeLotRequired, fmt.Sprintf("%s is tracked in lots; the line must name one.", v.SKU))
		case v.Tracking == contracts.TrackingSerial && names && l.setAt == nil && l.quantity != nil && l.quantity.Cmp(big.NewRat(1, 1)) != 0:
			fail(*l, codeSerialQuantity, fmt.Sprintf("%s is tracked in serials; a line moves exactly 1 of a serial.", v.SKU))
		case v.Tracking == contracts.TrackingSerial && names && l.setAt != nil && l.setTo != nil && l.setTo.Sign() != 0 && l.setTo.Cmp(big.NewRat(1, 1)) != 0:
			fail(*l, codeSerialQuantity, fmt.Sprintf("%s is tracked in serials; a serial holds 0 or 1.", v.SKU))
		}
	}

	// 3 and 4. The lot counter and the new lots.
	var reqs []lotRequest
	var reqLines []int
	for i, l := range lines {
		if l.newLot == nil {
			continue
		}
		if _, failed := failures[l.index]; failed {
			continue
		}
		r := *l.newLot
		r.variantID = l.variantID
		reqs = append(reqs, r)
		reqLines = append(reqLines, i)
	}
	var created []int64
	var createdLots []store.InventoryLot
	if len(reqs) > 0 {
		outcomes, err := s.ensureLots(ctx, txq, reqs, variants, opts.today, nil, m.poster)
		if err != nil {
			return postResult{}, err
		}
		for j, o := range outcomes {
			l := &lines[reqLines[j]]
			l.lotID = o.lot.ID
			if o.created {
				created = append(created, o.lot.ID)
				createdLots = append(createdLots, o.lot)
			}
			if o.failCode != "" {
				fail(*l, o.failCode, o.failText)
			}
		}
	}

	// The lots an AutoLot line may take: those holding its variant at its
	// location now — when limit, only the FEFO run that looks to cover it.
	// A lot that appears after this read is not used.
	candidates := map[int][]int64{}
	limited, err := s.lotCandidates(ctx, txq, lines, candidates, opts.today, limit)
	if err != nil {
		return postResult{}, err
	}

	// 5. Every lot named, in id order.
	var lotIDs []int64
	update := map[int64]bool{}
	for i, l := range lines {
		ids := candidates[i]
		if l.lotID != 0 {
			ids = append(ids, l.lotID)
		}
		lotIDs = append(lotIDs, ids...)
		if v, ok := variants[l.variantID]; ok && v.Tracking == contracts.TrackingSerial && (l.to != nil || l.setAt != nil) {
			for _, id := range ids {
				update[id] = true
			}
		}
		// A lot an inspected receipt holds is taken as the hold takes it.
		if inspected[l.variantID] && l.lotID != 0 {
			update[l.lotID] = true
		}
		// A lot an unbuild restores to may be quarantined after the posting
		// (manufacturing phase 2 M123): taken as the hold takes it, so two
		// unbuilds into one lot wait for each other rather than deadlock.
		if m.kind == movementKindUnbuild && l.to != nil && l.lotID != 0 {
			update[l.lotID] = true
		}
	}
	lots, err := lockLots(ctx, txq, lotIDs, update)
	if err != nil {
		return postResult{}, err
	}
	for i := range lines {
		l := &lines[i]
		if l.lotID == 0 {
			continue
		}
		lot, ok := lots[l.lotID]
		if !ok || lot.VariantID != l.variantID {
			fail(*l, codeLotUnknown, fmt.Sprintf("The variant has no lot with the id %d.", l.lotID))
			continue
		}
		s.judgeLot(l, lot, opts, fail)
	}

	// 6. The key, claimed.
	claim := store.ClaimMovementParams{
		ClientRequestID: m.clientRequestID, RequestHash: m.requestHash, SiteID: m.siteID, Kind: m.kind,
		Reason: m.reason, Note: m.note, SupplierName: m.supplierName, SupplierReference: m.supplierReference,
		PostedByUserID: m.poster.UserID, PostedByDisplay: &m.poster.Display, PostedAt: s.deps.Clock(), CountID: m.countID,
	}
	if src := m.source; src != nil {
		claim.SourceModule, claim.SourceKind, claim.SourceID, claim.SourceLineID = &src.module, &src.kind, &src.id, src.lineID
	}
	id, err := txq.ClaimMovement(ctx, claim)
	if noRows(err) {
		// A concurrent first attempt committed meanwhile; the lots this one
		// made are its own and named by nothing.
		if len(created) > 0 {
			if err := txq.DeleteLots(ctx, created); err != nil {
				return postResult{}, fmt.Errorf("inventory: remove the lots of a replay: %w", err)
			}
		}
		res, _, err := replayOf(ctx, txq, m)
		return res, err
	}
	if db.IsForeignKeyViolation(err) {
		var f fieldErrors
		f.add("siteId", "No site has this id")
		return postResult{}, refuseInvalid(invalidPostingTitle, f)
	}
	if err != nil {
		return postResult{}, fmt.Errorf("inventory: claim movement %s: %w", m.clientRequestID, err)
	}
	if len(created) > 0 {
		movement := strconv.FormatInt(id, 10)
		kind := cmp.Or(opts.lotOrigin, lotOriginMovement)
		if err := txq.SetLotOrigin(ctx, store.SetLotOriginParams{OriginModule: ptr(lotOriginModule), OriginKind: &kind, OriginID: &movement, Ids: created}); err != nil {
			return postResult{}, fmt.Errorf("inventory: name the lots' movement: %w", err)
		}
		if err := s.recordLotsCreated(ctx, txq, createdLots, m.poster); err != nil {
			return postResult{}, err
		}
	}

	// 7. The locations, shared in id order.
	locationIDs := make([]int64, 0, 2*len(lines))
	for _, l := range lines {
		for _, p := range []*int64{l.from, l.to, l.setAt} {
			if p != nil {
				locationIDs = append(locationIDs, *p)
			}
		}
	}
	slices.Sort(locationIDs)
	locationIDs = slices.Compact(locationIDs)
	rows, err := txq.ShareLocations(ctx, locationIDs)
	if err != nil {
		return postResult{}, fmt.Errorf("inventory: share the locations of movement %s: %w", m.clientRequestID, err)
	}
	locations := make(map[int64]store.InventoryLocation, len(rows))
	for _, r := range rows {
		locations[r.ID] = r
	}
	for _, l := range lines {
		for _, p := range []*int64{l.from, l.to, l.setAt} {
			if p == nil {
				continue
			}
			loc, ok := locations[*p]
			switch {
			case !ok:
				failAt(l, codeLocationNotFound, fmt.Sprintf("No location has the id %d.", *p), l.lotID, *p)
			case loc.SiteID != m.siteID:
				failAt(l, codeMoveCrossesSites, fmt.Sprintf("%s is in another site; a movement stays in its site.", loc.FullCode), l.lotID, *p)
			case loc.Status != string(gen.Active):
				failAt(l, codeLocationArchived, fmt.Sprintf("%s is archived and takes no stock.", loc.FullCode), l.lotID, *p)
			}
		}
	}
	if len(failures) > 0 {
		return postResult{}, refuseLines(sortedFailures(failures))
	}

	// 8. The balances, created at zero where missing, then locked.
	keys := balanceKeys(lines, candidates)
	balances, err := lockBalances(ctx, txq, keys, s.deps.Clock())
	if err != nil {
		return postResult{}, err
	}
	before := make(map[balanceKey]*big.Rat, len(balances))
	for k, v := range balances {
		before[k] = new(big.Rat).Set(v)
	}

	// The AutoLot lines take their lots FEFO under the locks (M65a).
	var picks []contracts.StockPick
	var short bool
	lines, picks, short = s.pickLots(lines, candidates, limited, lots, balances, opts.today, variants, locations, failAt)
	if short {
		return postResult{}, errCandidatesShort
	}
	if len(failures) > 0 {
		return postResult{}, refuseLines(sortedFailures(failures))
	}
	if opts.check != nil {
		if err := opts.check(lines, variants, balances); err != nil {
			return postResult{}, err
		}
	}

	// The lines, applied in order.
	posted := make([]engineLine, 0, len(lines))
	for _, l := range lines {
		v, loc := variants[l.variantID], locations
		if l.setAt != nil {
			key := balanceKey{l.variantID, *l.setAt, l.lotID}
			diff := new(big.Rat).Sub(l.setTo, balances[key])
			switch diff.Sign() {
			case 0:
				continue
			case 1:
				if v.ProductType != productTypeGoods {
					fail(l, codeVariantNotStockable, fmt.Sprintf("%s is a service; only goods are kept in stock.", v.SKU))
					continue
				}
				l.to, l.quantity = l.setAt, diff
			default:
				l.from, l.quantity = l.setAt, diff.Neg(diff)
			}
		}
		if l.from != nil {
			key := balanceKey{l.variantID, *l.from, l.lotID}
			left := new(big.Rat).Sub(balances[key], l.quantity)
			if left.Sign() < 0 {
				failAt(l, codeInsufficientStock, fmt.Sprintf("%s at %s%s: %s on hand, the line takes %s.",
					v.SKU, loc[*l.from].FullCode, lotWords(lots, l.lotID), decimalText(balances[key]), decimalText(l.quantity)), l.lotID, *l.from)
				continue
			}
			balances[key] = left
		}
		if l.to != nil {
			key := balanceKey{l.variantID, *l.to, l.lotID}
			after := new(big.Rat).Add(balances[key], l.quantity)
			if after.Cmp(maxQuantity) > 0 {
				failAt(l, codeQuantityInvalid, fmt.Sprintf("%s at %s would hold more than 99,999,999,999.999.", v.SKU, loc[*l.to].FullCode), l.lotID, *l.to)
				continue
			}
			balances[key] = after
		}
		posted = append(posted, l)
	}
	if len(failures) > 0 {
		return postResult{}, refuseLines(sortedFailures(failures))
	}
	if len(posted) == 0 {
		return postResult{}, nothingToPost()
	}
	// A serial holds at most 1 over every location (M53), summed under the
	// lot's lock: a posting increasing it elsewhere has committed, or waits.
	if err := serialRule(ctx, txq, posted, variants, lots, keys, before, balances, failAt); err != nil {
		return postResult{}, err
	}
	if len(failures) > 0 {
		return postResult{}, refuseLines(sortedFailures(failures))
	}

	// The movement's lines, the balances, and the zeros gone.
	if err := writeLines(ctx, txq, id, posted, variants); err != nil {
		return postResult{}, err
	}
	if err := writeBalances(ctx, txq, keys, balances, s.deps.Clock()); err != nil {
		return postResult{}, err
	}
	// The inspection holds of a receipt (M100), last.
	if len(inspected) > 0 {
		if err := s.holdForInspection(ctx, tx, txq, id, posted, inspected, m.poster); err != nil {
			return postResult{}, err
		}
	}
	// The lots' screens hear of their stock on the posting's own transaction
	// (manufacturing phase 2 §17), whichever module's it is.
	touched := make([]int64, 0, len(posted))
	for _, l := range posted {
		touched = append(touched, l.lotID)
	}
	if err := notifyLots(ctx, tx, touched); err != nil {
		return postResult{}, err
	}
	return postResult{movementID: id, picks: picks}, nil
}

// judgeLot is the lot rules of a line naming a locked lot (M56, M62): an
// issue or consume line refuses a lot on hold and an expired one without the
// override; a receipt refuses a blocked lot.
func (s *server) judgeLot(l *engineLine, lot store.InventoryLot, opts postOptions, fail func(engineLine, string, string)) {
	takes := opts.takes && l.from != nil && l.to == nil
	switch {
	case takes && lot.Status != contracts.LotAvailable:
		fail(*l, codeLotBlocked, fmt.Sprintf("The lot %s is on hold (%s) and cannot be taken.", lot.Number, lot.Status))
	case takes && lotExpired(lot, opts.today):
		if l.allowExpired && opts.expiredOverride {
			l.expiredOverride = true
			return
		}
		fail(*l, codeLotExpired, fmt.Sprintf("The lot %s expired on %s.", lot.Number, lot.Expiry.Time.Format(time.DateOnly)))
	case opts.receipt && l.to != nil && lot.Status == contracts.LotBlocked:
		fail(*l, codeLotBlocked, fmt.Sprintf("The lot %s is blocked; goods do not join a blocked lot.", lot.Number))
	}
}

// lotCandidates reads, for every AutoLot line, the lots holding its variant
// at its location, on the posting's transaction, before the lots are locked.
// When limit, a (variant, location) keeps only its FEFO run of lots that look
// usable — available and not expired on today, read plainly — until they
// cover what its AutoLot lines take, and lotMargin more; it answers the lines
// whose candidates were cut so.
func (s *server) lotCandidates(ctx context.Context, txq *store.Queries, lines []engineLine, out map[int][]int64, today time.Time, limit bool) (map[int]bool, error) {
	type pair struct {
		variant  int32
		location int64
	}
	var variants []int32
	var locations []int64
	demand := map[pair]*big.Rat{}
	for _, l := range lines {
		if !l.autoLot || l.from == nil {
			continue
		}
		variants = append(variants, l.variantID)
		locations = append(locations, *l.from)
		k := pair{l.variantID, *l.from}
		if demand[k] == nil {
			demand[k] = new(big.Rat)
		}
		if l.quantity == nil {
			limit = false // an entry not resolved: nothing to cover by
		} else {
			demand[k].Add(demand[k], l.quantity)
		}
	}
	if len(variants) == 0 {
		return nil, nil
	}
	rows, err := txq.LotCandidates(ctx, store.LotCandidatesParams{VariantIds: variants, LocationIds: locations})
	if err != nil {
		return nil, fmt.Errorf("inventory: read the lots an auto line may take: %w", err)
	}
	byPair := map[pair][]int64{}
	cut := map[pair]bool{}
	covered := map[pair]*big.Rat{}
	beyond := map[pair]int{}
	for _, r := range rows {
		k := pair{r.VariantID, r.LocationID}
		if !limit {
			byPair[k] = append(byPair[k], r.LotID)
			continue
		}
		if r.Status != contracts.LotAvailable || contracts.LotExpired(dateOrNil(r.Expiry), today) {
			cut[k] = true
			continue
		}
		if covered[k] == nil {
			covered[k] = new(big.Rat)
		}
		if covered[k].Cmp(demand[k]) >= 0 {
			if beyond[k] == lotMargin {
				cut[k] = true
				continue
			}
			beyond[k]++
		}
		covered[k].Add(covered[k], ratFromText(r.OnHand))
		byPair[k] = append(byPair[k], r.LotID)
	}
	limited := map[int]bool{}
	for i, l := range lines {
		if l.autoLot && l.from != nil {
			k := pair{l.variantID, *l.from}
			out[i] = byPair[k]
			limited[i] = cut[k]
		}
	}
	return limited, nil
}

// pickLots replaces every AutoLot line by one line per lot it takes, FEFO —
// expiry (none last), received, lot id — skipping lots on hold, expired lots
// (even on the override) and serials that would split, under the locks the
// posting holds (M65a). A line its lots do not cover is insufficient_stock —
// or, when its candidates were cut (limited), short: the posting runs again
// with every candidate. It answers the lines and the picks, by the line's
// index.
func (s *server) pickLots(lines []engineLine, candidates map[int][]int64, limited map[int]bool, lots map[int64]store.InventoryLot, balances map[balanceKey]*big.Rat,
	today time.Time, variants map[int32]contracts.VariantEntry, locations map[int64]store.InventoryLocation,
	failAt func(engineLine, string, string, int64, int64),
) ([]engineLine, []contracts.StockPick, bool) {
	out := make([]engineLine, 0, len(lines))
	var picks []contracts.StockPick
	taken := map[balanceKey]*big.Rat{}
	for i, l := range lines {
		if !l.autoLot || l.from == nil {
			out = append(out, l)
			continue
		}
		ids := slices.Clone(candidates[i])
		slices.SortFunc(ids, func(a, b int64) int {
			x, y := lots[a], lots[b]
			switch {
			case !x.Expiry.Valid && y.Expiry.Valid:
				return 1
			case x.Expiry.Valid && !y.Expiry.Valid:
				return -1
			case x.Expiry.Valid && y.Expiry.Valid:
				if c := x.Expiry.Time.Compare(y.Expiry.Time); c != 0 {
					return c
				}
			}
			return cmp.Or(x.ReceivedAt.Compare(y.ReceivedAt), cmp.Compare(a, b))
		})
		left := new(big.Rat).Set(l.quantity)
		for _, id := range ids {
			if left.Sign() == 0 {
				break
			}
			lot, ok := lots[id]
			if !ok || lot.Status != contracts.LotAvailable || lotExpired(lot, today) {
				continue
			}
			key := balanceKey{l.variantID, *l.from, id}
			have := new(big.Rat).Set(balances[key])
			if t := taken[key]; t != nil {
				have.Sub(have, t)
			}
			if have.Sign() <= 0 || (lot.Serial && have.Cmp(left) > 0) {
				continue
			}
			take := minRat(have, left)
			left.Sub(left, take)
			if taken[key] == nil {
				taken[key] = new(big.Rat)
			}
			taken[key].Add(taken[key], take)
			pick := l
			pick.autoLot, pick.lotID, pick.quantity = false, id, take
			out = append(out, pick)
			picks = append(picks, contracts.StockPick{Index: l.index, LotID: id, LocationID: *l.from, Quantity: decimalText(take)})
		}
		if left.Sign() > 0 && limited[i] {
			return nil, nil, true
		}
		if left.Sign() > 0 {
			failAt(l, codeInsufficientStock, fmt.Sprintf("%s at %s: the lots that may be taken do not cover %s; %s short.",
				variants[l.variantID].SKU, locations[*l.from].FullCode, decimalText(l.quantity), decimalText(left)), 0, *l.from)
		}
	}
	return out, picks, false
}

// serialRule refuses a posting that would leave a serial lot it increases
// holding more than 1 over every location (M53). The posting holds the lot
// FOR NO KEY UPDATE and its own balances locked; the lot's total is read
// after them, and this posting's change added to it.
func serialRule(ctx context.Context, txq *store.Queries, posted []engineLine, variants map[int32]contracts.VariantEntry, lots map[int64]store.InventoryLot,
	keys []balanceKey, before, after map[balanceKey]*big.Rat, failAt func(engineLine, string, string, int64, int64),
) error {
	increased := map[int64]engineLine{}
	for _, l := range posted {
		if l.lotID != 0 && l.to != nil && variants[l.variantID].Tracking == contracts.TrackingSerial {
			if _, ok := increased[l.lotID]; !ok {
				increased[l.lotID] = l
			}
		}
	}
	if len(increased) == 0 {
		return nil
	}
	ids := slices.Sorted(maps.Keys(increased))
	rows, err := txq.LotTotals(ctx, ids)
	if err != nil {
		return fmt.Errorf("inventory: read the serials' stock: %w", err)
	}
	total := map[int64]*big.Rat{}
	for _, r := range rows {
		total[r.LotID] = ratFromText(r.OnHand)
	}
	for _, k := range keys {
		if _, ok := increased[k.lotID]; !ok {
			continue
		}
		if total[k.lotID] == nil {
			total[k.lotID] = new(big.Rat)
		}
		total[k.lotID].Add(total[k.lotID], new(big.Rat).Sub(after[k], before[k]))
	}
	for _, id := range ids {
		if total[id] != nil && total[id].Cmp(big.NewRat(1, 1)) > 0 {
			l := increased[id]
			failAt(l, codeSerialQuantity, fmt.Sprintf("The serial %s would be in stock %s times; a serial holds at most 1.",
				lots[id].Number, decimalText(total[id])), id, *l.to)
		}
	}
	return nil
}

// lotWords names a lot inside a refusal's detail: " in lot L1001", "" for
// none.
func lotWords(lots map[int64]store.InventoryLot, id int64) string {
	if lot, ok := lots[id]; ok {
		return " in lot " + lot.Number
	}
	return ""
}

// The catalog's words the engine reads (contracts.VariantEntry).
const (
	productTypeGoods          = "Goods"
	productStatusDiscontinued = "Discontinued"
)

// increases reports whether the line puts stock down without taking it from
// anywhere — a receipt's line, an adjustment up, a set (judged again once its
// difference is known): what a Service product's variant may not do (I12,
// design §9). A move and a decrease still post, so stock left on a product
// turned into a service can be cleared.
func (l engineLine) increases() bool { return l.to != nil && l.from == nil }

// replayOf answers a posting whose key was posted before: the first movement
// when the body is the same (a replay), client_request_reused when it is
// not. done is false when the key is new.
func replayOf(ctx context.Context, txq *store.Queries, m movementHeader) (postResult, bool, error) {
	prior, err := txq.MovementByClientRequest(ctx, m.clientRequestID)
	if noRows(err) {
		return postResult{}, false, nil
	}
	if err != nil {
		return postResult{}, true, fmt.Errorf("inventory: read movement %s: %w", m.clientRequestID, err)
	}
	if !bytes.Equal(prior.RequestHash, m.requestHash) {
		return postResult{}, true, refuse(problem(http.StatusConflict, codeClientRequestReused, "Client request reused",
			fmt.Sprintf("The clientRequestId %s was posted before with a different body; make a new one for a new movement.", m.clientRequestID)))
	}
	return postResult{movementID: prior.ID, replay: true}, true, nil
}

// nothingToPost is the refusal of a movement left with no lines.
func nothingToPost() error {
	return refuse(problem(http.StatusBadRequest, codeNothingToPost, invalidPostingTitle,
		"Every line leaves the stock as it is; nothing was posted."))
}

// sortedFailures is the failures in the request's line order.
func sortedFailures(failures map[int]lineFailure) []lineFailure {
	out := make([]lineFailure, 0, len(failures))
	for _, f := range failures {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b lineFailure) int { return cmp.Compare(a.Index, b.Index) })
	return out
}

// balanceKeys is every balance the lines touch — and every one an AutoLot
// line may take from — in (variant, location, lot) order, no lot first.
func balanceKeys(lines []engineLine, candidates map[int][]int64) []balanceKey {
	var keys []balanceKey
	for i, l := range lines {
		for _, p := range []*int64{l.from, l.to, l.setAt} {
			if p == nil {
				continue
			}
			if l.autoLot {
				for _, lot := range candidates[i] {
					keys = append(keys, balanceKey{l.variantID, *p, lot})
				}
				continue
			}
			keys = append(keys, balanceKey{l.variantID, *p, l.lotID})
		}
	}
	slices.SortFunc(keys, func(a, b balanceKey) int {
		return cmp.Or(cmp.Compare(a.variantID, b.variantID), cmp.Compare(a.locationID, b.locationID), cmp.Compare(a.lotID, b.lotID))
	})
	return slices.Compact(keys)
}

// keyColumns is keys as the three arrays the queries unnest side by side, a
// lot of 0 for none.
func keyColumns(keys []balanceKey) ([]int32, []int64, []int64) {
	variants, locations, lots := make([]int32, len(keys)), make([]int64, len(keys)), make([]int64, len(keys))
	for i, k := range keys {
		variants[i], locations[i], lots[i] = k.variantID, k.locationID, k.lotID
	}
	return variants, locations, lots
}

// lockBalances creates the missing balances at zero and locks them all, in
// (variant, location, lot) order, in one statement (step 8), and answers what
// each holds. A balance a concurrent posting deleted at zero is inserted
// again by the same statement, so no lock is held while another posting's
// insert is waited for.
func lockBalances(ctx context.Context, txq *store.Queries, keys []balanceKey, now time.Time) (map[balanceKey]*big.Rat, error) {
	out := make(map[balanceKey]*big.Rat, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	variants, locations, lots := keyColumns(keys)
	rows, err := txq.LockBalances(ctx, store.LockBalancesParams{VariantIds: variants, LocationIds: locations, LotIds: lots, Now: now})
	if err != nil {
		return nil, fmt.Errorf("inventory: lock balances: %w", err)
	}
	if len(rows) != len(keys) {
		return nil, fmt.Errorf("inventory: locked %d of a posting's %d balances", len(rows), len(keys))
	}
	for _, r := range rows {
		out[balanceKey{r.VariantID, r.LocationID, r.LotID}] = ratFromNumeric(r.OnHand)
	}
	return out, nil
}

// writeLines writes the posted lines, numbered from 1, with the variants'
// snapshots as LockVariants answered them (I9), each entry as it was
// resolved (I25), and the lot and override of each (M55, M62).
func writeLines(ctx context.Context, txq *store.Queries, movementID int64, lines []engineLine, variants map[int32]contracts.VariantEntry) error {
	p := store.InsertMovementLinesParams{MovementID: movementID}
	for i, l := range lines {
		v := variants[l.variantID]
		q, err := numericFromRat(l.quantity)
		if err != nil {
			return err
		}
		p.LineNos = append(p.LineNos, int32(i+1))
		p.VariantIds = append(p.VariantIds, l.variantID)
		p.Skus = append(p.Skus, v.SKU)
		p.ProductNames = append(p.ProductNames, v.ProductName)
		p.Units = append(p.Units, v.Unit)
		p.FromIds = append(p.FromIds, idOrZero(l.from))
		p.ToIds = append(p.ToIds, idOrZero(l.to))
		p.Quantities = append(p.Quantities, q)
		p.Entereds = append(p.Entereds, string(l.entered))
		p.LotIds = append(p.LotIds, l.lotID)
		p.ExpiredOverrides = append(p.ExpiredOverrides, l.expiredOverride)
	}
	if err := txq.InsertMovementLines(ctx, p); err != nil {
		return fmt.Errorf("inventory: write the lines of movement %d: %w", movementID, err)
	}
	return nil
}

// writeBalances writes the balances the lines left, then deletes those at
// zero, so "where does it lie" lists only places with stock (I10).
func writeBalances(ctx context.Context, txq *store.Queries, keys []balanceKey, balances map[balanceKey]*big.Rat, now time.Time) error {
	variants, locations, lots := keyColumns(keys)
	onHands := make([]pgtype.Numeric, len(keys))
	for i, k := range keys {
		n, err := numericFromRat(balances[k])
		if err != nil {
			return err
		}
		onHands[i] = n
	}
	if err := txq.SetBalances(ctx, store.SetBalancesParams{VariantIds: variants, LocationIds: locations, LotIds: lots, OnHands: onHands, Now: now}); err != nil {
		return fmt.Errorf("inventory: write balances: %w", err)
	}
	if err := txq.DeleteZeroBalances(ctx, store.DeleteZeroBalancesParams{VariantIds: variants, LocationIds: locations, LotIds: lots}); err != nil {
		return fmt.Errorf("inventory: delete zero balances: %w", err)
	}
	return nil
}

// idOrZero is *p, 0 for none — the queries' word for a missing from or to.
func idOrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// postAttempts is how many times an API posting runs when it loses a race to
// a deadlock or a serialization failure.
const postAttempts = 5

// postMovement runs the engine for one of the API's postings in its own
// transaction, with the API's line cap, and runs it again in a new one when
// it loses a race (see the lock order above).
func (s *server) postMovement(ctx context.Context, m movementHeader, lines []engineLine, receipt bool, today time.Time) (postResult, error) {
	var res postResult
	err := db.RetrySerializable(ctx, postAttempts, func() error {
		return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			var err error
			res, err = s.post(ctx, tx, txq, m, lines, postOptions{maxLines: maxAPILines, receipt: receipt, today: today})
			return err
		})
	})
	return res, err
}
