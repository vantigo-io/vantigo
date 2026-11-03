package inventory

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
)

// Adjustments (design I15): lines of a variant, a location, a mode and a
// quantity, for a reason. change takes a signed delta, not 0: an increase is
// a line to the location, a decrease a line from it. set takes the new on
// hand, 0 or more, and is posted as the difference against the balance the
// engine has locked; a line whose difference is 0 is dropped, and a movement
// left with none is refused (nothing_to_post). An adjustment accepts a
// discontinued product's variant, so the last stock can be cleared (I12).

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
	for i, l := range in.Lines {
		line := engineLine{index: i, variantID: l.VariantId}
		location := l.LocationId
		switch l.Mode {
		case gen.Change:
			q, msg := parseQuantity(l.Quantity, signedQuantity)
			switch {
			case msg != "":
				line.failCode, line.failText = codeQuantityInvalid, msg
			case q.Sign() > 0:
				line.to, line.quantity = &location, q
			default:
				line.from, line.quantity = &location, q.Neg(q)
			}
		case gen.Set:
			q, msg := parseQuantity(l.Quantity, nonNegativeQuantity)
			if msg != "" {
				line.failCode, line.failText = codeQuantityInvalid, msg
			}
			line.setAt, line.setTo = &location, q
		default:
			f.add(fmt.Sprintf("lines[%d].mode", i), "The mode must be change or set")
		}
		lines = append(lines, line)
	}
	if f.failed() {
		return gen.PostInventoryAdjustment400ApplicationProblemPlusJSONResponse(invalid(invalidPostingTitle, f)), nil
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
	}, lines, false)
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
