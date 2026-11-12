package inventory

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Counting (design I18). A request records up to 500 counted quantities: a
// pair with a line is changed, a pair with none gets a line expecting 0 and
// marked added. A counted quantity is 0 or more, or what was entered in
// packagings, resolved against the variant's packagings as LockVariants
// answers them when the line is recorded; the line keeps the entry. Each
// change records who counted and when. A pair named twice in one request
// takes its last value. The whole request is refused when any line fails,
// every failing line answered: a location outside the count's scope is
// location_not_found, a variant the catalog does not know variant_not_found,
// and a service added to a count variant_not_stockable.
//
// A line is one balance (manufacturing phase 2 M69): a tracked variant's
// line names its lot — lotId for one the variant has, or lot to find or make
// one by number as a receipt does — and an untracked one's none
// (lot_required, lot_not_allowed, lot_unknown); a line the count froze
// without a lot is still counted as it is, and the post judges it. A serial
// is counted 0 or 1 (serial_quantity).
//
// The lock order is the count post's: the count row FOR UPDATE, then the
// variants (LockVariants), then the lot counter and the new lots, made with
// the count as their origin, then the lots named FOR SHARE in id order (M58),
// then the lines' locations FOR SHARE in id order, then the rows written.

// countLine is one counted line as the request gave it, judged.
type countLine struct {
	index      int
	variantID  int32
	locationID int64
	lotID      int64
	newLot     *lotRequest
	counted    *big.Rat
	entry      *entry
	failCode   string
	failText   string
}

// lotOriginCount is the origin kind of a lot a count line made (M69).
const lotOriginCount = "count"

// PutInventoryCountLines Record counted quantities
// (PUT /api/v1/inventory/counts/{id}/lines)
func (s *server) PutInventoryCountLines(ctx context.Context, req gen.PutInventoryCountLinesRequestObject) (gen.PutInventoryCountLinesResponseObject, error) {
	if req.Body == nil {
		return gen.PutInventoryCountLines400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidCountTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, countLinesInputSchema)
	if len(in.Lines) == 0 {
		f.add("lines", "Give at least one counted line")
	}
	if f.failed() {
		return gen.PutInventoryCountLines400ApplicationProblemPlusJSONResponse(invalid(invalidCountTitle, f)), nil
	}
	if len(in.Lines) > maxAPILines {
		return gen.PutInventoryCountLines400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, codeTooManyLines, invalidCountTitle,
			fmt.Sprintf("A request counts at most %d lines; this one has %d.", maxAPILines, len(in.Lines)))), nil
	}
	lines := make([]countLine, 0, len(in.Lines))
	newLots := false
	for i, l := range in.Lines {
		line := countLine{index: i, variantID: l.VariantId, locationID: l.LocationId}
		line.counted, line.entry, line.failCode, line.failText = readEntered(l.Counted, l.Entered, nonNegativeQuantity)
		line.lotID = lotIDOf(&f, fmt.Sprintf("lines[%d].lotId", i), l.LotId)
		line.newLot = readNewLot(&f, fmt.Sprintf("lines[%d].lot", i), l.Lot)
		if l.LotId != nil && l.Lot != nil {
			f.add(fmt.Sprintf("lines[%d].lot", i), "Name the lot by lotId or by lot, not both")
		}
		newLots = newLots || line.newLot != nil
		lines = append(lines, line)
	}
	if f.failed() {
		return gen.PutInventoryCountLines400ApplicationProblemPlusJSONResponse(invalid(invalidCountTitle, f)), nil
	}
	// Today, on the pool before the transaction (M62): what a new lot's
	// dates default from.
	var today time.Time
	if newLots {
		var err error
		if today, err = s.today(ctx); err != nil {
			return nil, err
		}
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = db.RetrySerializable(ctx, postAttempts, func() error {
		return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			return s.recordCounted(ctx, tx, txq, who, req.Id, lines, today)
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PutInventoryCountLines400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PutInventoryCountLines404Response{}, nil
		case http.StatusConflict:
			return gen.PutInventoryCountLines409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PutInventoryCountLines503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	c, err := s.countOf(ctx, req.Id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.PutInventoryCountLines503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutInventoryCountLines200JSONResponse(c), nil
}

// recordCounted is PutInventoryCountLines' transaction.
func (s *server) recordCounted(ctx context.Context, tx pgx.Tx, txq *store.Queries, who actor, id int64, lines []countLine, today time.Time) error {
	c, err := lockOpenCount(ctx, txq, id)
	if err != nil {
		return err
	}
	scope, err := txq.GetLocation(ctx, c.ScopeLocationID)
	if err != nil {
		return fmt.Errorf("inventory: read the scope of count %d: %w", c.ID, err)
	}
	variantIDs := make([]int32, 0, len(lines))
	locationIDs := make([]int64, 0, len(lines))
	for _, l := range lines {
		variantIDs, locationIDs = append(variantIDs, l.variantID), append(locationIDs, l.locationID)
	}
	slices.Sort(variantIDs)
	entries, err := s.lockVariants(ctx, tx, slices.Compact(variantIDs))
	if err != nil {
		return err
	}
	variants := make(map[int32]int, len(entries))
	byID := make(map[int32]contracts.VariantEntry, len(entries))
	for i, e := range entries {
		variants[e.ID], byID[e.ID] = i, e
	}

	var failures []lineFailure
	fail := func(l countLine, code, detail string) {
		failures = append(failures, lineFailure{Index: l.index, Code: code, Detail: detail})
	}
	// The lots (M69, M58 steps 3–5): the new ones found or made, the count
	// their origin, then every lot named shared in id order and judged
	// against its variant.
	var reqs []lotRequest
	var reqLines []int
	for i, l := range lines {
		if l.newLot == nil || l.failCode != "" {
			continue
		}
		if v, ok := byID[l.variantID]; !ok || !tracked(v) {
			continue // refused below
		}
		r := *l.newLot
		r.variantID = l.variantID
		reqs, reqLines = append(reqs, r), append(reqLines, i)
	}
	if len(reqs) > 0 {
		origin := &movementSource{module: lotOriginModule, kind: lotOriginCount, id: strconv.FormatInt(c.ID, 10)}
		outcomes, err := s.ensureLots(ctx, txq, reqs, byID, today, origin, who)
		if err != nil {
			return err
		}
		for j, o := range outcomes {
			l := &lines[reqLines[j]]
			l.lotID = o.lot.ID
			if o.failCode != "" {
				l.failCode, l.failText = o.failCode, o.failText
			}
		}
	}
	var lotIDs []int64
	for _, l := range lines {
		if l.lotID != 0 {
			lotIDs = append(lotIDs, l.lotID)
		}
	}
	lots, err := lockLots(ctx, txq, lotIDs, nil)
	if err != nil {
		return err
	}

	// The lines' locations FOR SHARE in id order, as a posting takes them: a
	// delete racing the request waits, or has removed a location already and
	// the line is location_not_found.
	slices.Sort(locationIDs)
	rows, err := txq.ShareLocations(ctx, slices.Compact(locationIDs))
	if err != nil {
		return fmt.Errorf("inventory: share the locations of count %d's lines: %w", c.ID, err)
	}
	locations := make(map[int64]store.InventoryLocation, len(rows))
	for _, r := range rows {
		locations[r.ID] = r
	}
	pairs, err := txq.CountLinePairs(ctx, c.ID)
	if err != nil {
		return fmt.Errorf("inventory: read the lines of count %d: %w", c.ID, err)
	}
	existing := make(map[balanceKey]bool, len(pairs))
	for _, p := range pairs {
		existing[balanceKey{p.VariantID, p.LocationID, p.LotID}] = true
	}

	type counted struct {
		quantity *big.Rat
		entered  []byte
	}
	values := map[balanceKey]counted{}
	var order []balanceKey
	for _, l := range lines {
		if l.failCode != "" {
			fail(l, l.failCode, l.failText)
			continue
		}
		key := balanceKey{l.variantID, l.locationID, l.lotID}
		loc, ok := locations[l.locationID]
		if !ok || (loc.ID != scope.ID && !slices.Contains(loc.Ancestors, scope.ID)) {
			fail(l, codeLocationNotFound, fmt.Sprintf("No location %d lies in the count's scope %s.", l.locationID, scope.FullCode))
			continue
		}
		if loc.Status != string(gen.Active) {
			fail(l, codeLocationArchived, fmt.Sprintf("%s is archived and takes no stock.", loc.FullCode))
			continue
		}
		i, ok := variants[l.variantID]
		if !ok {
			fail(l, codeVariantNotFound, fmt.Sprintf("No variant has the id %d.", l.variantID))
			continue
		}
		v := entries[i]
		if !existing[key] && v.ProductType != productTypeGoods {
			fail(l, codeVariantNotStockable, fmt.Sprintf("%s is a service; only goods are kept in stock.", v.SKU))
			continue
		}
		// The lot, judged as the variant is now; a line the count froze
		// without one stands as it is (the post judges it).
		switch lot, named := lots[l.lotID]; {
		case !tracked(v) && (l.lotID != 0 || l.newLot != nil):
			fail(l, codeLotNotAllowed, fmt.Sprintf("%s is not tracked in lots; the line names none.", v.SKU))
			continue
		case tracked(v) && l.lotID == 0 && !existing[key]:
			fail(l, codeLotRequired, fmt.Sprintf("%s is tracked in lots; the line must name one.", v.SKU))
			continue
		case l.lotID != 0 && (!named || lot.VariantID != l.variantID):
			fail(l, codeLotUnknown, fmt.Sprintf("The variant has no lot with the id %d.", l.lotID))
			continue
		}
		value := counted{quantity: l.counted}
		if l.entry != nil {
			q, snap, code, detail := l.entry.resolve(v)
			if code != "" {
				fail(l, code, detail)
				continue
			}
			value = counted{quantity: q, entered: snap}
		}
		if v.Tracking == contracts.TrackingSerial && l.lotID != 0 && value.quantity.Cmp(big.NewRat(1, 1)) > 0 {
			fail(l, codeSerialQuantity, fmt.Sprintf("%s is tracked in serials; a serial is counted 0 or 1.", v.SKU))
			continue
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = value
	}
	if len(failures) > 0 {
		return refuseLines(failures)
	}

	p := store.UpsertCountLinesParams{CountID: c.ID, CountedByUserID: who.UserID, CountedByDisplay: &who.Display, CountedAt: s.deps.Clock()}
	for _, key := range order {
		v := values[key]
		n, err := numericFromRat(v.quantity)
		if err != nil {
			return err
		}
		p.VariantIds = append(p.VariantIds, key.variantID)
		p.LocationIds = append(p.LocationIds, key.locationID)
		p.LotIds = append(p.LotIds, key.lotID)
		p.Counteds = append(p.Counteds, n)
		p.Entereds = append(p.Entereds, string(v.entered))
	}
	if err := txq.UpsertCountLines(ctx, p); err != nil {
		return fmt.Errorf("inventory: record the lines of count %d: %w", c.ID, err)
	}
	if err := txq.TouchCount(ctx, c.ID); err != nil {
		return fmt.Errorf("inventory: touch count %d: %w", c.ID, err)
	}
	return nil
}

// countedOf is a count line's counted quantity, nil when nobody counted it.
func countedOf(n pgtype.Numeric) *big.Rat {
	if !n.Valid {
		return nil
	}
	return ratFromNumeric(n)
}
