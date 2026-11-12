package inventory

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// Posting a count (design I19). Under the count row's lock (§4) the engine
// posts ONE count movement, with no line cap, of the differences between what
// was counted and the balance at posting: every counted line is posted as a
// set of its location's on hand to the counted quantity, so the engine
// computes the difference against the balance it has locked. A line nobody
// counted keeps its balance (uncounted keep) or is set to 0 (zero).
//
// A posted line whose balance moved since the count opened — the locked
// balance differs from what the line expected — refuses the post with
// count_lines_moved, every such line named, unless acceptMoved is given. A
// line that posts nothing (keep) is not judged.
//
// A count whose lines change nothing — every counted quantity equals its
// balance — is still posted, with no movement: the count records that the
// stock was right. The engine's attempt runs under a savepoint, so its
// claimed key is rolled back with it.
//
// Every line sets its own balance — (location, lot) for a tracked variant
// (manufacturing phase 2 M69). A count opened before its variant became
// tracked has lines of it without a lot: they are posted as they are — that
// is, left out — only when every such line of the variant expected 0 and
// counted 0 (or is uncounted); otherwise the post refuses them lot_required
// and the count must be counted again. The variants are taken FOR SHARE
// (LockVariants) after the count's row, as the engine takes them again.
//
// The movement's key is derived from the count, so the post is its own
// replay; the count row's lock and status make a second post count_not_open
// anyway. Each refused line's index is its place in the count's lines as GET
// /counts/{id} answers them (by full code, then SKU).

// PostInventoryCount Post a count
// (POST /api/v1/inventory/counts/{id}/post)
func (s *server) PostInventoryCount(ctx context.Context, req gen.PostInventoryCountRequestObject) (gen.PostInventoryCountResponseObject, error) {
	if req.Body == nil {
		return gen.PostInventoryCount400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidCountTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, countPostInputSchema)
	if !in.Uncounted.Valid() {
		f.add("uncounted", "uncounted must be keep or zero")
	}
	if f.failed() {
		return gen.PostInventoryCount400ApplicationProblemPlusJSONResponse(invalid(invalidCountTitle, f)), nil
	}
	acceptMoved := in.AcceptMoved != nil && *in.AcceptMoved

	// The lines' order as GET answers it, read with the catalog before the
	// transaction, so a refused line's index names the row the app shows.
	before, err := s.countOf(ctx, req.Id)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusNotFound:
			return gen.PostInventoryCount404Response{}, nil
		case http.StatusServiceUnavailable:
			return gen.PostInventoryCount503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	position := map[int64]int{}
	if before.Lines != nil {
		for i, l := range *before.Lines {
			position[l.Id] = i
		}
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = db.RetrySerializable(ctx, postAttempts, func() error {
		return s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
			return s.postCount(ctx, tx, txq, who, req.Id, string(in.Uncounted), acceptMoved, position)
		})
	})
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PostInventoryCount400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusNotFound:
			return gen.PostInventoryCount404Response{}, nil
		case http.StatusConflict:
			return gen.PostInventoryCount409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PostInventoryCount503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	c, err := s.countOf(ctx, req.Id)
	if status, p, ok := answerOf(err); ok && status == http.StatusServiceUnavailable {
		return gen.PostInventoryCount503ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostInventoryCount200JSONResponse(c), nil
}

// countKeySpace is the namespace a count movement's clientRequestId is
// derived in.
var countKeySpace = uuid.MustParse("6f1d3c52-6a43-4f57-9a43-2b1f0c6d2e01")

// postCount is PostInventoryCount's transaction.
func (s *server) postCount(ctx context.Context, tx pgx.Tx, txq *store.Queries, who actor, id int64, uncounted string, acceptMoved bool, position map[int64]int) error {
	c, err := lockOpenCount(ctx, txq, id)
	if err != nil {
		return err
	}
	rows, err := txq.CountLines(ctx, c.ID)
	if err != nil {
		return fmt.Errorf("inventory: read the lines of count %d: %w", c.ID, err)
	}
	stale, err := s.staleCountVariants(ctx, tx, rows)
	if err != nil {
		return err
	}

	// The lines to post: each counted line set to what was counted, each
	// uncounted one set to 0 under zero and left out under keep.
	type posting struct {
		row  store.CountLinesRow
		line engineLine
	}
	var postings []posting
	unknown := len(position)
	for _, r := range rows {
		index, ok := position[r.ID]
		if !ok {
			index, unknown = unknown, unknown+1
		}
		if r.LotID == nil && stale[r.VariantID] {
			continue
		}
		location := r.LocationID
		line := engineLine{index: index, variantID: r.VariantID, setAt: &location}
		if r.LotID != nil {
			line.lotID = *r.LotID
		}
		switch counted := countedOf(r.Counted); {
		case counted != nil:
			line.setTo = counted
		case uncounted == string(gen.Zero):
			line.setTo = new(big.Rat)
		default:
			continue
		}
		postings = append(postings, posting{row: r, line: line})
	}

	// Moved lines, judged against the balances the engine has locked.
	check := func(_ []engineLine, variants map[int32]contracts.VariantEntry, balances map[balanceKey]*big.Rat) error {
		var moved []lineFailure
		for _, p := range postings {
			now, expected := balances[balanceKey{p.row.VariantID, p.row.LocationID, p.line.lotID}], ratFromNumeric(p.row.Expected)
			if now.Cmp(expected) == 0 {
				continue
			}
			sku := variants[p.row.VariantID].SKU
			moved = append(moved, lineFailure{Index: p.line.index, Code: codeCountLinesMoved, Detail: fmt.Sprintf(
				"%s at %s: %s expected when the count opened, %s on hand now.", sku, p.row.FullCode, decimalText(expected), decimalText(now))})
		}
		if len(moved) == 0 || acceptMoved {
			return nil
		}
		return countLinesMoved(moved)
	}

	var movementID *int64
	if len(postings) > 0 {
		lines := make([]engineLine, 0, len(postings))
		for _, p := range postings {
			lines = append(lines, p.line)
		}
		hash := sha256.Sum256([]byte("count\n" + strconv.FormatInt(c.ID, 10)))
		header := movementHeader{
			clientRequestID: uuid.NewSHA1(countKeySpace, []byte(strconv.FormatInt(c.ID, 10))), requestHash: hash[:],
			siteID: c.SiteID, kind: string(gen.Count), countID: &c.ID, poster: who,
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return fmt.Errorf("inventory: begin the post of count %d: %w", c.ID, err)
		}
		res, err := s.post(ctx, sp, store.New(sp), header, lines, postOptions{check: check})
		switch {
		case nothingPosted(err):
			if err := sp.Rollback(ctx); err != nil {
				return fmt.Errorf("inventory: roll back the post of count %d: %w", c.ID, err)
			}
		case err != nil:
			_ = sp.Rollback(ctx)
			return err
		default:
			if err := sp.Commit(ctx); err != nil {
				return fmt.Errorf("inventory: release the post of count %d: %w", c.ID, err)
			}
			movementID = &res.movementID
		}
	}

	if err := txq.PostCount(ctx, store.PostCountParams{
		ID: c.ID, Uncounted: &uncounted, MovementID: movementID, PostedByUserID: who.UserID, PostedByDisplay: &who.Display,
		PostedAt: s.deps.Clock(),
	}); err != nil {
		return fmt.Errorf("inventory: post count %d: %w", c.ID, err)
	}
	return s.recordEvent(ctx, txq, who, event{
		EntityKind: entityCount, EntityID: c.ID, SiteID: &c.SiteID, Kind: eventCountPosted,
		Payload: map[string]any{"movementId": movementID, "uncounted": uncounted, "acceptMoved": acceptMoved, "lines": len(rows)},
	})
}

// staleCountVariants is the tracked variants whose every line in the count
// is without a lot, expected 0 and counted 0 or not at all: a count opened
// before the variant became tracked, whose lines post as they are — nothing
// (M69). A tracked variant with any other line without a lot is not among
// them, so the engine refuses that line lot_required.
func (s *server) staleCountVariants(ctx context.Context, tx pgx.Tx, rows []store.CountLinesRow) (map[int32]bool, error) {
	var ids []int32
	for _, r := range rows {
		if r.LotID == nil {
			ids = append(ids, r.VariantID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	ids = distinct(ids)
	slices.Sort(ids)
	entries, err := s.lockVariants(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	out := map[int32]bool{}
	for _, e := range entries {
		if tracked(e) {
			out[e.ID] = true
		}
	}
	for _, r := range rows {
		if r.LotID != nil || !out[r.VariantID] {
			continue
		}
		counted := countedOf(r.Counted)
		if ratFromNumeric(r.Expected).Sign() != 0 || (counted != nil && counted.Sign() != 0) {
			delete(out, r.VariantID)
		}
	}
	// Only the lines without a lot are left out: a variant's lines with one
	// post as any other.
	return out, nil
}

// countLinesMoved is the 409 of a count post whose lines' balances moved
// since the count opened (I19), naming every one.
func countLinesMoved(moved []lineFailure) error {
	lines := make([]gen.InventoryProblemLine, 0, len(moved))
	for _, l := range moved {
		lines = append(lines, gen.InventoryProblemLine{Index: int32(l.Index), Code: l.Code, Detail: l.Detail})
	}
	p := problem(http.StatusConflict, codeCountLinesMoved, "Count lines moved",
		fmt.Sprintf("%d line(s) changed since the count opened; post with acceptMoved to count them anyway.", len(moved)))
	p.Lines = &lines
	return refuse(p)
}

// nothingPosted reports whether err is the engine's nothing_to_post: every
// line left the stock as it was.
func nothingPosted(err error) bool {
	var r *refusal
	return errors.As(err, &r) && r.problem.Code != nil && *r.problem.Code == codeNothingToPost
}
