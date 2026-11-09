package inventory

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"slices"
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
// (variant, location) is kept by the same transaction that writes it.
//
// The lock order, in every posting:
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
//  3. The key is claimed by writing the movement's header, INSERT … ON
//     CONFLICT DO NOTHING RETURNING. A conflict means a concurrent first
//     attempt committed; it is read back and answered as in step 1.
//  4. The locations FOR SHARE, in id order. A posting writes nothing to a
//     location, so two postings never wait for each other there.
//  5. Every balance created at zero where missing and locked, in (variant,
//     location) order, by one INSERT … ON CONFLICT DO UPDATE … RETURNING
//     (I10): two postings to a new pair serialise on the row rather than
//     collide on its key, and a balance deleted at zero under the statement
//     is inserted again by it, so no lock is held across a second attempt.
//
// Then the lines are applied in order, the balance checked after each (so
// -5 then +5 against 0 fails at the first), the movement's lines written,
// and the balances left at zero deleted. Every failing line is answered at
// once: the shape and the catalog's and the locations' refusals together,
// then — only when those pass — the balances'.
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
	// receipt refuses a Discontinued product's variant (I12).
	receipt bool
	// check, when set, judges the posting once its variants and balances are
	// locked, its entries resolved to units, and before any line is applied:
	// the count post's moved lines (I19), a transfer action's per-line rules
	// (phase 2 design I38–I44: a receive's outstanding, a send's goods). A
	// refusal it answers rolls the posting back. lines are the posting's own,
	// resolved; the check reads them and changes nothing.
	check func(lines []engineLine, variants map[int32]contracts.VariantEntry, balances map[balanceKey]*big.Rat) error
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
}

// postResult is a posting's outcome: the movement, and whether it was posted
// before under the same key and body (a replay, answered 200).
type postResult struct {
	movementID int64
	replay     bool
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

// balanceKey names one balance row: a variant on a location. It names a row
// of the ledger's table, never the whole key of a balance (design §12: a lot
// joins it later).
type balanceKey struct {
	variantID  int32
	locationID int64
}

// post is the engine: it posts the movement and its lines inside tx, in the
// lock order above, and answers the movement — or a refusal naming every
// failing line, after which the caller rolls back.
func (s *server) post(ctx context.Context, tx pgx.Tx, txq *store.Queries, m movementHeader, lines []engineLine, opts postOptions) (postResult, error) {
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
	fail := func(l engineLine, code, detail string) {
		if _, ok := failures[l.index]; !ok {
			failures[l.index] = lineFailure{Index: l.index, Code: code, Detail: detail}
		}
	}
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

	// 3. The key, claimed.
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

	// 4. The locations, shared in id order.
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
				fail(l, codeLocationNotFound, fmt.Sprintf("No location has the id %d.", *p))
			case loc.SiteID != m.siteID:
				fail(l, codeMoveCrossesSites, fmt.Sprintf("%s is in another site; a movement stays in its site.", loc.FullCode))
			case loc.Status != string(gen.Active):
				fail(l, codeLocationArchived, fmt.Sprintf("%s is archived and takes no stock.", loc.FullCode))
			}
		}
	}
	if len(failures) > 0 {
		return postResult{}, refuseLines(sortedFailures(failures))
	}

	// 5. The balances, created at zero where missing, then locked.
	keys := balanceKeys(lines)
	balances, err := lockBalances(ctx, txq, keys, s.deps.Clock())
	if err != nil {
		return postResult{}, err
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
			key := balanceKey{l.variantID, *l.setAt}
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
			key := balanceKey{l.variantID, *l.from}
			left := new(big.Rat).Sub(balances[key], l.quantity)
			if left.Sign() < 0 {
				fail(l, codeInsufficientStock, fmt.Sprintf("%s at %s: %s on hand, the line takes %s.",
					v.SKU, loc[*l.from].FullCode, decimalText(balances[key]), decimalText(l.quantity)))
				continue
			}
			balances[key] = left
		}
		if l.to != nil {
			key := balanceKey{l.variantID, *l.to}
			after := new(big.Rat).Add(balances[key], l.quantity)
			if after.Cmp(maxQuantity) > 0 {
				fail(l, codeQuantityInvalid, fmt.Sprintf("%s at %s would hold more than 99,999,999,999.999.", v.SKU, loc[*l.to].FullCode))
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

	// The movement's lines, the balances, and the zeros gone.
	if err := writeLines(ctx, txq, id, posted, variants); err != nil {
		return postResult{}, err
	}
	if err := writeBalances(ctx, txq, keys, balances, s.deps.Clock()); err != nil {
		return postResult{}, err
	}
	return postResult{movementID: id}, nil
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

// balanceKeys is every balance the lines touch, in (variant, location) order.
func balanceKeys(lines []engineLine) []balanceKey {
	var keys []balanceKey
	for _, l := range lines {
		for _, p := range []*int64{l.from, l.to, l.setAt} {
			if p != nil {
				keys = append(keys, balanceKey{l.variantID, *p})
			}
		}
	}
	slices.SortFunc(keys, func(a, b balanceKey) int {
		return cmp.Or(cmp.Compare(a.variantID, b.variantID), cmp.Compare(a.locationID, b.locationID))
	})
	return slices.Compact(keys)
}

// keyColumns is keys as the two arrays the queries unnest side by side.
func keyColumns(keys []balanceKey) ([]int32, []int64) {
	variants, locations := make([]int32, len(keys)), make([]int64, len(keys))
	for i, k := range keys {
		variants[i], locations[i] = k.variantID, k.locationID
	}
	return variants, locations
}

// lockBalances creates the missing balances at zero and locks them all, in
// (variant, location) order, in one statement (step 5), and answers what each
// holds. A balance a concurrent posting deleted at zero is inserted again by
// the same statement, so no lock is held while another posting's insert is
// waited for.
func lockBalances(ctx context.Context, txq *store.Queries, keys []balanceKey, now time.Time) (map[balanceKey]*big.Rat, error) {
	variants, locations := keyColumns(keys)
	rows, err := txq.LockBalances(ctx, store.LockBalancesParams{VariantIds: variants, LocationIds: locations, Now: now})
	if err != nil {
		return nil, fmt.Errorf("inventory: lock balances: %w", err)
	}
	if len(rows) != len(keys) {
		return nil, fmt.Errorf("inventory: locked %d of a posting's %d balances", len(rows), len(keys))
	}
	out := make(map[balanceKey]*big.Rat, len(rows))
	for _, r := range rows {
		out[balanceKey{r.VariantID, r.LocationID}] = ratFromNumeric(r.OnHand)
	}
	return out, nil
}

// writeLines writes the posted lines, numbered from 1, with the variants'
// snapshots as LockVariants answered them (I9) and each entry as it was
// resolved (I25).
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
	}
	if err := txq.InsertMovementLines(ctx, p); err != nil {
		return fmt.Errorf("inventory: write the lines of movement %d: %w", movementID, err)
	}
	return nil
}

// writeBalances writes the balances the lines left, then deletes those at
// zero, so "where does it lie" lists only places with stock (I10).
func writeBalances(ctx context.Context, txq *store.Queries, keys []balanceKey, balances map[balanceKey]*big.Rat, now time.Time) error {
	variants, locations := keyColumns(keys)
	onHands := make([]pgtype.Numeric, len(keys))
	for i, k := range keys {
		n, err := numericFromRat(balances[k])
		if err != nil {
			return err
		}
		onHands[i] = n
	}
	if err := txq.SetBalances(ctx, store.SetBalancesParams{VariantIds: variants, LocationIds: locations, OnHands: onHands, Now: now}); err != nil {
		return fmt.Errorf("inventory: write balances: %w", err)
	}
	if err := txq.DeleteZeroBalances(ctx, store.DeleteZeroBalancesParams{VariantIds: variants, LocationIds: locations}); err != nil {
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
func (s *server) postMovement(ctx context.Context, m movementHeader, lines []engineLine, receipt bool) (postResult, error) {
	var res postResult
	err := db.RetrySerializable(ctx, postAttempts, func() error {
		return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			var err error
			res, err = s.post(ctx, tx, txq, m, lines, postOptions{maxLines: maxAPILines, receipt: receipt})
			return err
		})
	})
	return res, err
}
