package inventory

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// Adjustments (design I15): lines of a variant, a location, a mode and a
// quantity, for a reason. change takes a signed delta, not 0: an increase is
// a line to the location, a decrease a line from it. set takes the new on
// hand, 0 or more, and is posted as the difference against the balance the
// engine has locked; a line whose difference is 0 is dropped, and a movement
// left with none is refused (nothing_to_post). An adjustment accepts a
// discontinued product's variant, so the last stock can be cleared (I12).
// A line may give what was entered in packagings instead of the quantity
// (I25): under change it is added, or taken away with decrease; under set it
// is the new on hand.

// PostInventoryAdjustment Adjust stock
// (POST /api/v1/inventory/movements/adjustments)
func (s *server) PostInventoryAdjustment(ctx context.Context, req gen.PostInventoryAdjustmentRequestObject) (gen.PostInventoryAdjustmentResponseObject, error) {
	if req.Body == nil {
		return gen.PostInventoryAdjustment400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, "", invalidPostingTitle, "The request has no body.")), nil
	}
	in := *req.Body
	f := s.unknownProperties(ctx, adjustmentInputSchema)
	if !in.Reason.Valid() {
		f.add("reason", "The reason must be one of correction, damaged, lost, found, opening_balance or other")
	}
	note := optionalText(&f, "note", in.Note, maxNoteLength)
	lines := make([]engineLine, 0, len(in.Lines))
	namesLots := false
	for i, l := range in.Lines {
		field := fmt.Sprintf("lines[%d]", i)
		// A line names its lot by id, or — for an increase — by number, to
		// be found or made (manufacturing phase 2 M57, M59); set mode sets
		// one (location, lot). An adjustment ignores holds and expiry.
		line := engineLine{index: i, variantID: l.VariantId, lotID: lotIDOf(&f, field+".lotId", l.LotId), newLot: readNewLot(&f, field+".lot", l.Lot)}
		namesLots = namesLots || line.newLot != nil
		if line.lotID != 0 && line.newLot != nil {
			f.add(field, "A line names a lotId or a lot, not both")
		}
		location := l.LocationId
		decrease := l.Decrease != nil && *l.Decrease
		switch l.Mode {
		case gen.Change:
			q, e, code, detail := readEntered(l.Quantity, l.Entered, signedQuantity)
			switch {
			case code != "":
				line.failCode, line.failText = code, detail
			case decrease && e == nil:
				line.failCode, line.failText = codeEnteredInvalid, "decrease goes with entered; a quantity under change carries its own sign."
			case e != nil && decrease:
				line.from, line.entry = &location, e
			case e != nil:
				line.to, line.entry = &location, e
			case q.Sign() > 0:
				line.to, line.quantity = &location, q
			default:
				line.from, line.quantity = &location, q.Neg(q)
			}
		case gen.Set:
			line.setAt = &location
			line.setTo, line.entry, line.failCode, line.failText = readEntered(l.Quantity, l.Entered, nonNegativeQuantity)
			if decrease {
				line.failCode, line.failText = codeEnteredInvalid, "decrease goes with change; set makes the entry the new on hand."
			}
		default:
			f.add(fmt.Sprintf("lines[%d].mode", i), "The mode must be change or set")
		}
		if line.newLot != nil && line.from != nil {
			f.add(field+".lot", "A lot by number is named only by an increase; a decrease names its lotId")
		}
		lines = append(lines, line)
	}
	if f.failed() {
		return gen.PostInventoryAdjustment400ApplicationProblemPlusJSONResponse(invalid(invalidPostingTitle, f)), nil
	}
	// The day a new lot's dates default from (M62), read before the
	// transaction; an adjustment naming no new lot needs none.
	var today time.Time
	if namesLots {
		var err error
		if today, err = s.today(ctx); err != nil {
			return nil, err
		}
	}
	hash, err := requestHash(string(gen.Adjustment), in)
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	reason := string(in.Reason)
	res, err := s.postMovement(ctx, movementHeader{
		clientRequestID: in.ClientRequestId, requestHash: hash, siteID: in.SiteId, kind: string(gen.Adjustment),
		reason: &reason, note: note, poster: who,
	}, lines, false, today)
	if status, p, ok := answerOf(err); ok {
		switch status {
		case http.StatusBadRequest:
			return gen.PostInventoryAdjustment400ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return gen.PostInventoryAdjustment409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusServiceUnavailable:
			return gen.PostInventoryAdjustment503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	mv, err := s.movementOf(ctx, res.movementID)
	if err != nil {
		return nil, err
	}
	if res.replay {
		return gen.PostInventoryAdjustment200JSONResponse(mv), nil
	}
	return gen.PostInventoryAdjustment201JSONResponse(mv), nil
}
